// Package supervisor starts, monitors and restarts one worker process per
// configured Telegram client.
//
// A supervisor never starts two processes for one key, never deletes a file
// that is not a socket, and keeps one worker's failure from affecting the
// others. A worker that keeps failing is left in PhaseFailed instead of being
// retried forever.
package supervisor

import (
	"context"
	"errors"
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/mirza/telesync/internal/appconfig"
)

// Phase is the supervisor's view of a worker process.
type Phase string

const (
	// PhaseStarting means the process exists but its RPC socket is not ready.
	PhaseStarting Phase = "starting"
	// PhaseRunning means the worker answered a readiness probe.
	PhaseRunning Phase = "running"
	// PhaseBackoff means the worker exited and a restart is pending.
	PhaseBackoff Phase = "backoff"
	// PhaseFailed means the restart budget is exhausted; the worker stays down.
	PhaseFailed Phase = "failed"
	// PhaseStopped means the supervisor shut the worker down.
	PhaseStopped Phase = "stopped"
	// PhaseDeferred means the account is waiting for a free worker slot.
	PhaseDeferred Phase = "deferred"
)

// Launch describes one worker invocation.
type Launch struct {
	// Key is the configured client key. It is only set on the legacy
	// client-keyed path and is never an identity.
	Key string
	// AccountID is the account this worker serves. Set on the account path,
	// which is the real one.
	AccountID  string
	ConfigPath string
	SocketPath string
	Args       []string
}

// Process is a child process handle.
type Process interface {
	Pid() int
	Wait() error
	Signal(sig os.Signal) error
	Kill() error
}

// Launcher starts a child process.
type Launcher interface {
	Start(Launch) (Process, error)
}

// ReadyProber reports whether a worker's RPC socket answers.
type ReadyProber interface {
	Ready(ctx context.Context, socketPath string) error
}

// Options configures the supervisor.
type Options struct {
	BinaryPath          string
	ConfigPath          string
	RunDir              string
	RestartInitialDelay time.Duration
	RestartMaxDelay     time.Duration
	MaxRestartAttempts  int
	StartupTimeout      time.Duration
	ShutdownTimeout     time.Duration
	ProbeInterval       time.Duration
	// StableAfter is how long a worker must stay ready before its restart
	// budget is reset. Zero means twice the initial delay.
	StableAfter time.Duration
	// LivenessInterval is how often a running worker's socket is probed. Zero
	// means 15 seconds.
	LivenessInterval time.Duration
	// LivenessFailures is how many consecutive failed probes mean the process
	// is wedged and must be replaced. Zero means 3. A worker that is merely
	// disconnected from Telegram still answers its socket, so this never
	// restarts a client just because Telegram is unreachable.
	LivenessFailures int
	// SessionDir is where sealed account sessions live. It must not be inside
	// RunDir: a session is a credential, not a runtime file.
	SessionDir string
	// PeerDir is where each account's peer cache lives.
	PeerDir string
	// MaxWorkers bounds how many account workers run at once. Zero means the
	// TELESYNC_ACCOUNT_MAX_WORKERS default of 50. Accounts beyond the budget are
	// deferred until a slot frees up; they are never dropped.
	MaxWorkers int
	// ReconnectBackoff is the base delay before an account's worker is
	// restarted. Zero means the TELESYNC_ACCOUNT_RECONNECT_BACKOFF default of 5s.
	ReconnectBackoff time.Duration
	// Logf receives lifecycle events. Nil discards them.
	Logf func(format string, args ...any)
	// Sleep waits for d, honouring ctx. Tests replace it.
	Sleep func(ctx context.Context, d time.Duration) error
}

// State is the supervisor's report on one worker.
type State struct {
	// Key is the legacy client key, set only on the client-keyed path.
	Key string
	// AccountID is the account this worker serves, on the account path.
	AccountID   string
	Phase       Phase
	PID         int
	Restarts    int
	LastError   string
	StartedAt   time.Time
	OwnerUserID int64
}

// DefaultMaxWorkers is the TELESYNC_ACCOUNT_MAX_WORKERS default: the bound on
// how many account workers run at once.
const DefaultMaxWorkers = 50

// DefaultReconnectBackoff is the TELESYNC_ACCOUNT_RECONNECT_BACKOFF default: the
// base delay before an account's worker is restarted.
const DefaultReconnectBackoff = 5 * time.Second

// Environment variables that bound the account worker pool.
const (
	MaxWorkersEnv       = "TELESYNC_ACCOUNT_MAX_WORKERS"
	ReconnectBackoffEnv = "TELESYNC_ACCOUNT_RECONNECT_BACKOFF"
)

// Supervisor owns every worker process.
type Supervisor struct {
	opts     Options
	launcher Launcher
	prober   ReadyProber

	mu    sync.Mutex
	state map[string]*State
	// slots bounds how many workers run at once. A send takes a slot; a
	// receive frees one.
	slots chan struct{}
	// slotsByID is each account's stop handle.
	slotsByID map[string]*accountSlot
	// accountRefs retains the trusted metadata used to make duplicate Add calls
	// idempotent without allowing an id to change owners or server definitions.
	accountRefs map[string]AccountRef
	// closed stops new accounts starting once the supervisor is shutting down.
	closed bool
	// ctx is the supervisor's own lifetime: every account worker runs under it,
	// so an account added after a login is stopped by the same shutdown as one
	// started at startup.
	ctx     context.Context
	stopAll context.CancelFunc
	// wg counts the account workers this supervisor owns.
	wg sync.WaitGroup
}

// New builds a supervisor.
func New(opts Options, launcher Launcher, prober ReadyProber) *Supervisor {
	if opts.Sleep == nil {
		opts.Sleep = sleepCtx
	}
	if opts.ProbeInterval <= 0 {
		opts.ProbeInterval = 100 * time.Millisecond
	}
	if opts.StableAfter <= 0 {
		opts.StableAfter = 2 * opts.RestartInitialDelay
	}
	if opts.LivenessInterval <= 0 {
		opts.LivenessInterval = 15 * time.Second
	}
	if opts.LivenessFailures <= 0 {
		opts.LivenessFailures = 3
	}
	if opts.Logf == nil {
		opts.Logf = func(string, ...any) {}
	}
	if opts.MaxWorkers <= 0 {
		opts.MaxWorkers = envInt(MaxWorkersEnv, DefaultMaxWorkers)
	}
	if opts.MaxWorkers <= 0 {
		opts.MaxWorkers = DefaultMaxWorkers
	}
	if opts.ReconnectBackoff <= 0 {
		opts.ReconnectBackoff = envDuration(ReconnectBackoffEnv, DefaultReconnectBackoff)
	}
	s := &Supervisor{
		opts:        opts,
		launcher:    launcher,
		prober:      prober,
		state:       map[string]*State{},
		slotsByID:   map[string]*accountSlot{},
		accountRefs: map[string]AccountRef{},
	}
	s.slots = make(chan struct{}, opts.MaxWorkers)
	return s
}

// envInt reads a positive integer from the environment, falling back to def.
func envInt(name string, def int) int {
	v := strings.TrimSpace(os.Getenv(name))
	if v == "" {
		return def
	}
	n, err := strconv.Atoi(v)
	if err != nil || n <= 0 {
		return def
	}
	return n
}

// envDuration reads a positive duration from the environment, falling back to
// def. A plain number means seconds, matching how an operator thinks ("5" is
// five seconds).
func envDuration(name string, def time.Duration) time.Duration {
	v := strings.TrimSpace(os.Getenv(name))
	if v == "" {
		return def
	}
	if secs, err := strconv.Atoi(v); err == nil {
		if secs <= 0 {
			return def
		}
		return time.Duration(secs) * time.Second
	}
	d, err := time.ParseDuration(v)
	if err != nil || d <= 0 {
		return def
	}
	return d
}

func sleepCtx(ctx context.Context, d time.Duration) error {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

// Run starts every enabled client and blocks until ctx is cancelled.
//
// A worker that exhausts its restart budget does not stop the supervisor; the
// others keep running and the failed one stays visible in States.
func (s *Supervisor) Run(ctx context.Context, clients []appconfig.Client) error {
	// Compatibility path: one worker per configured client key. The account
	// path is RunAccounts; this exists so the configured-client daemon keeps
	// working while internal/runtime is refactored.
	targets := make([]workerTarget, 0, len(clients))
	for _, cl := range clients {
		if !cl.Enabled {
			continue
		}
		targets = append(targets, workerTarget{
			Key:        cl.Key,
			SocketPath: cl.RPCSocketPath,
			Args:       s.argsFor(cl.Key, ""),
		})
	}
	return s.superviseAll(ctx, targets, false)
}

// superviseAll registers every target and blocks until ctx ends.
func (s *Supervisor) superviseAll(ctx context.Context, targets []workerTarget, budgeted bool) error {
	var wg sync.WaitGroup
	for _, t := range targets {
		if !s.register(t) {
			continue
		}
		wg.Add(1)
		go func(t workerTarget) {
			defer wg.Done()
			s.supervise(ctx, t, budgeted, false)
		}(t)
	}

	<-ctx.Done()
	s.stopping()
	wg.Wait()
	return nil
}

// register records a target unless one with the same identity is already
// supervised: one account never gets two workers.
func (s *Supervisor) register(t workerTarget) bool {
	key := t.scopeKey()
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, dup := s.state[key]; dup {
		return false
	}
	s.state[key] = &State{Key: t.Key, AccountID: t.AccountID, OwnerUserID: t.OwnerUserID, Phase: PhaseStarting}
	return true
}

// argsFor builds the worker invocation. The account is named by its own opaque
// id; the legacy path names the configured client key.
func (s *Supervisor) argsFor(key, accountID string) []string {
	args := []string{s.opts.BinaryPath, "worker"}
	if accountID != "" {
		args = append(args, "--account", accountID)
	} else {
		args = append(args, "--key", key)
	}
	return append(args, "--config", s.opts.ConfigPath)
}

// workerTarget is what one supervised worker is launched from.
//
// It carries either an account identity (the real path) or a legacy client key
// (the compatibility path, still used by the configured-client daemon). The
// scope key — the name every log line, state entry and path is keyed by — is
// the account id whenever one is present, and the client key otherwise.
type workerTarget struct {
	Key         string
	AccountID   string
	OwnerUserID int64
	SocketPath  string
	Args        []string
	Metadata    AccountMetadata
}

// scopeKey is the identity this worker is supervised under.
func (t workerTarget) scopeKey() string {
	if t.AccountID != "" {
		return t.AccountID
	}
	return t.Key
}

// procHandle owns the single Wait call for a child process.
type procHandle struct {
	proc Process
	done chan struct{}
	mu   sync.Mutex
	err  error
}

func newProcHandle(p Process) *procHandle {
	h := &procHandle{proc: p, done: make(chan struct{})}
	go func() {
		err := p.Wait()
		h.mu.Lock()
		h.err = err
		h.mu.Unlock()
		close(h.done)
	}()
	return h
}

func (h *procHandle) exited() bool {
	select {
	case <-h.done:
		return true
	default:
		return false
	}
}

func (h *procHandle) waitErr() error {
	<-h.done
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.err
}

// supervise runs one worker until it fails permanently or ctx ends.
//
// It holds one worker slot for as long as a process is running, so the worker
// budget bounds live connections rather than registered accounts. An account
// waiting for a slot is visible as PhaseDeferred and starts as soon as one
// frees up.
func (s *Supervisor) supervise(ctx context.Context, t workerTarget, budgeted, held bool) {
	key := t.scopeKey()
	// A slot taken before the goroutine started is still owed back, including
	// when the account is removed before its first attempt.
	defer func() {
		if held {
			s.release(key, budgeted)
			held = false
		}
	}()
	restarts := 0
	for ctx.Err() == nil {
		if !held {
			if !s.acquire(ctx, key, budgeted) {
				return
			}
			held = true
		}

		keep := s.attempt(ctx, t, &restarts, s.restartBase(budgeted))
		s.release(key, budgeted)
		held = false
		if !keep {
			return
		}
	}
}

// attempt runs one worker generation. It returns false when supervision of
// this target must end rather than retry.
func (s *Supervisor) attempt(ctx context.Context, t workerTarget, restarts *int, base time.Duration) bool {
	key := t.scopeKey()
	if err := RemoveStaleSocket(t.SocketPath); err != nil {
		s.fail(key, err)
		s.opts.Logf("supervisor: %s: %v", key, err)
		return false
	}

	launch := Launch{
		Key:        t.Key,
		AccountID:  t.AccountID,
		ConfigPath: s.opts.ConfigPath,
		SocketPath: t.SocketPath,
		Args:       t.Args,
	}
	proc, err := s.launcher.Start(launch)
	if err != nil {
		*restarts++
		s.opts.Logf("supervisor: %s: start failed (restart %d/%d): %v",
			key, *restarts, s.opts.MaxRestartAttempts, err)
		return s.retry(ctx, key, *restarts, err, base)
	}

	handle := newProcHandle(proc)
	s.update(key, func(st *State) {
		st.Phase = PhaseStarting
		st.PID = proc.Pid()
		st.StartedAt = time.Now().UTC()
		st.LastError = ""
	})
	s.opts.Logf("supervisor: %s: started pid=%d", key, proc.Pid())

	reason, ready, probeErr := s.awaitReady(ctx, handle, t.SocketPath, t)
	if ctx.Err() != nil {
		s.stop(handle)
		return false
	}
	if !ready {
		s.stop(handle)
		if probeErr != nil && errors.Is(probeErr, ErrAccountIdentityMismatch) {
			s.fail(key, probeErr)
			s.opts.Logf("supervisor: %s: %v", key, probeErr)
			return false
		}
		*restarts++
		s.opts.Logf("supervisor: %s: %s (restart %d/%d)",
			key, reason, *restarts, s.opts.MaxRestartAttempts)
		return s.retry(ctx, key, *restarts, errors.New(reason), base)
	}

	s.update(key, func(st *State) { st.Phase = PhaseRunning })
	s.opts.Logf("supervisor: %s: ready", key)

	exitErr, wedged, stable, identityErr := s.watchRunning(ctx, handle, t.SocketPath, t)
	if identityErr != nil {
		s.stop(handle)
		s.fail(key, identityErr)
		s.opts.Logf("supervisor: %s: %v", key, identityErr)
		return false
	}
	if stable {
		// The worker proved stable, so the next failure starts a new budget.
		*restarts = 0
		s.update(key, func(st *State) { st.Restarts = 0 })
	}
	if wedged {
		*restarts++
		s.opts.Logf("supervisor: %s: stopped answering its socket (%d failed probes); replacing it",
			key, s.opts.LivenessFailures)
		s.stop(handle)
		return s.retry(ctx, key, *restarts, errors.New("worker stopped answering its socket"), base)
	}
	if ctx.Err() != nil {
		s.stop(handle)
		return false
	}

	*restarts++
	s.recordExit(key, exitErr)
	s.opts.Logf("supervisor: %s: exited (restart %d/%d): %v",
		key, *restarts, s.opts.MaxRestartAttempts, exitErr)
	return s.retry(ctx, key, *restarts, exitReason(exitErr), base)
}

func exitReason(err error) error {
	if err == nil {
		return errors.New("worker exited with status 0")
	}
	return fmt.Errorf("worker exited unexpectedly: %w", err)
}

// watchRunning blocks while the worker runs, probing its socket periodically.
//
// It returns the exit error when the process ends, or wedged=true when the
// process is alive but has stopped answering LivenessFailures consecutive
// probes. The restart-budget reset happens here too, once the worker has stayed
// ready for StableAfter.
func (s *Supervisor) watchRunning(ctx context.Context, handle *procHandle, socket string, target workerTarget) (exitErr error, wedged, stable bool, identityErr error) {
	stableTimer := time.NewTimer(s.opts.StableAfter)
	defer stableTimer.Stop()
	liveness := time.NewTicker(s.opts.LivenessInterval)
	defer liveness.Stop()

	wasStable := false
	failures := 0
	for {
		select {
		case <-ctx.Done():
			return nil, false, wasStable, nil
		case <-handle.done:
			return handle.waitErr(), false, wasStable, nil
		case <-stableTimer.C:
			wasStable = true
			stableTimer.Stop()
		case <-liveness.C:
			probeCtx, cancel := context.WithTimeout(ctx, s.opts.LivenessInterval)
			err := s.probeReady(probeCtx, socket, target)
			cancel()
			if err == nil {
				failures = 0
				continue
			}
			if errors.Is(err, ErrAccountIdentityMismatch) {
				return nil, false, wasStable, err
			}
			if handle.exited() {
				// The process is gone; the exit path reports why.
				return handle.waitErr(), false, wasStable, nil
			}
			failures++
			s.opts.Logf("supervisor: pid %d: probe %d/%d failed: %v",
				handle.proc.Pid(), failures, s.opts.LivenessFailures, err)
			if failures >= s.opts.LivenessFailures {
				return nil, true, wasStable, nil
			}
		}
	}
}

// awaitReady polls the worker's RPC socket until it answers or the startup
// timeout expires.
func (s *Supervisor) awaitReady(ctx context.Context, handle *procHandle, socket string, target workerTarget) (string, bool, error) {
	deadline := time.Now().Add(s.opts.StartupTimeout)
	for {
		if ctx.Err() != nil {
			return "", false, nil
		}
		if handle.exited() {
			return "process exited before it became ready", false, nil
		}

		probeCtx, cancel := context.WithTimeout(ctx, s.opts.ProbeInterval*5)
		err := s.probeReady(probeCtx, socket, target)
		cancel()
		if err == nil {
			return "", true, nil
		}
		if errors.Is(err, ErrAccountIdentityMismatch) {
			return err.Error(), false, err
		}
		if time.Now().After(deadline) {
			return fmt.Sprintf("did not become ready within %s", s.opts.StartupTimeout), false, nil
		}
		if err := s.opts.Sleep(ctx, s.opts.ProbeInterval); err != nil {
			return "", false, nil
		}
	}
}

func (s *Supervisor) probeReady(ctx context.Context, socket string, target workerTarget) error {
	if target.AccountID == "" {
		return s.prober.Ready(ctx, socket)
	}
	prober, ok := s.prober.(AccountReadyProber)
	if !ok {
		return ErrAccountReadinessUnsupported
	}
	return prober.ReadyAccount(ctx, socket, target.Metadata)
}

// retry records a failure and waits before the next start. It returns false
// when the budget is exhausted (leaving the worker failed) or ctx ended.
func (s *Supervisor) retry(ctx context.Context, key string, attempt int, cause error, base time.Duration) bool {
	if attempt > s.opts.MaxRestartAttempts {
		s.fail(key, cause)
		s.opts.Logf("supervisor: %s: giving up after %d restart attempts", key, s.opts.MaxRestartAttempts)
		return false
	}
	delay := Backoff(base, s.opts.RestartMaxDelay, attempt, rand.Float64())
	s.update(key, func(st *State) {
		st.Phase = PhaseBackoff
		st.Restarts = attempt
		st.LastError = cause.Error()
		st.PID = 0
	})
	s.opts.Logf("supervisor: %s: restarting in %s (restart %d/%d)",
		key, delay.Round(time.Millisecond), attempt, s.opts.MaxRestartAttempts)
	if err := s.opts.Sleep(ctx, delay); err != nil {
		return false
	}
	return ctx.Err() == nil
}

// restartBase is the delay a restart sequence starts from.
//
// An account worker is bounded by TELESYNC_ACCOUNT_RECONNECT_BACKOFF (default
// 5s), so a deployment can tune how quickly a dropped personal session comes
// back. The legacy configured-client path keeps the operator's
// runtime.restart_initial_delay.
func (s *Supervisor) restartBase(accountPath bool) time.Duration {
	if accountPath {
		return s.opts.ReconnectBackoff
	}
	if s.opts.RestartInitialDelay > 0 {
		return s.opts.RestartInitialDelay
	}
	return s.opts.ReconnectBackoff
}

// stop asks a child to exit, then kills it if it does not.
//
// The child is given the full shutdown timeout to flush its session and close
// its socket before it is killed.
func (s *Supervisor) stop(handle *procHandle) {
	if handle == nil || handle.exited() {
		return
	}
	_ = handle.proc.Signal(os.Interrupt)
	select {
	case <-handle.done:
		return
	case <-time.After(s.opts.ShutdownTimeout):
	}
	s.opts.Logf("supervisor: pid %d ignored the interrupt; killing it", handle.proc.Pid())
	_ = handle.proc.Kill()
	select {
	case <-handle.done:
	case <-time.After(s.opts.ShutdownTimeout):
	}
}

// takeSlot reserves a worker slot without waiting. It is what decides whether
// an account can start right now.
func (s *Supervisor) takeSlot() bool {
	if s.slots == nil {
		return true
	}
	select {
	case s.slots <- struct{}{}:
		return true
	default:
		return false
	}
}

// acquire takes a worker slot, or marks the account deferred while it waits.
//
// budgeted is false on the legacy configured-client path, which is unbounded
// because its size is the operator's configuration, not a runtime pool.
func (s *Supervisor) acquire(ctx context.Context, key string, budgeted bool) bool {
	if !budgeted {
		return ctx.Err() == nil
	}
	if s.takeSlot() {
		s.update(key, func(st *State) { st.Phase = PhaseStarting })
		return true
	}
	s.update(key, func(st *State) {
		st.Phase = PhaseDeferred
		st.PID = 0
		st.LastError = ""
	})
	select {
	case s.slots <- struct{}{}:
		s.update(key, func(st *State) { st.Phase = PhaseStarting })
		return true
	case <-ctx.Done():
		return false
	}
}

// release frees a worker slot and wakes whatever was waiting for one.
func (s *Supervisor) release(key string, budgeted bool) {
	if !budgeted {
		return
	}
	select {
	case <-s.slots:
	default:
	}
}

// stopping ends every wait for a worker slot, so shutdown cannot block on one.
func (s *Supervisor) stopping() {
	s.mu.Lock()
	s.closed = true
	s.mu.Unlock()
}

func (s *Supervisor) update(key string, fn func(*State)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if st := s.state[key]; st != nil {
		fn(st)
	}
}

func (s *Supervisor) recordExit(key string, err error) {
	s.update(key, func(st *State) {
		st.PID = 0
		st.LastError = exitReason(err).Error()
	})
}

func (s *Supervisor) fail(key string, cause error) {
	s.update(key, func(st *State) {
		st.Phase = PhaseFailed
		st.LastError = cause.Error()
		st.PID = 0
	})
}

// States reports the current state of every supervised worker, keyed by the
// account id on the account path and by the client key on the legacy one.
func (s *Supervisor) States() map[string]State {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make(map[string]State, len(s.state))
	for k, st := range s.state {
		out[k] = *st
	}
	return out
}

// Backoff computes the delay before restart number attempt, with jitter
// fraction in [0,1]. Jitter spreads delays by ±25% so several workers that fail
// together do not retry in lockstep.
func Backoff(initial, max time.Duration, attempt int, jitter float64) time.Duration {
	if initial <= 0 {
		initial = time.Second
	}
	if max <= 0 {
		max = initial
	}
	if attempt < 1 {
		attempt = 1
	}
	d := initial
	for i := 1; i < attempt; i++ {
		if d >= max {
			break
		}
		d *= 2
	}
	if d > max {
		d = max
	}
	if jitter < 0 {
		jitter = 0
	}
	if jitter > 1 {
		jitter = 1
	}
	d += time.Duration(float64(d) * 0.25 * (2*jitter - 1))
	if d < 0 {
		d = 0
	}
	return d
}

// RemoveStaleSocket deletes a leftover socket file before a worker binds it.
//
// Only paths ending in .sock are considered, paths containing .. are refused,
// and a non-socket at that path is reported rather than deleted: an unrelated
// file must never be destroyed by the supervisor.
func RemoveStaleSocket(path string) error {
	if path == "" {
		return fmt.Errorf("socket path is empty")
	}
	if strings.Contains(path, "..") {
		return fmt.Errorf("refusing to remove a socket path containing %q: %s", "..", path)
	}
	cleaned := filepath.Clean(path)
	if !strings.HasSuffix(cleaned, ".sock") {
		return fmt.Errorf("refusing to remove %q: not a .sock path", path)
	}
	info, err := os.Lstat(cleaned)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("inspect %q: %w", cleaned, err)
	}
	if info.Mode()&os.ModeSocket != 0 {
		if err := os.Remove(cleaned); err != nil {
			return fmt.Errorf("remove stale socket %q: %w", cleaned, err)
		}
		return nil
	}
	return fmt.Errorf("%q exists and is not a socket; move it aside manually", cleaned)
}
