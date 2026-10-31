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
)

// Launch describes one worker invocation.
type Launch struct {
	Key        string
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
	// Logf receives lifecycle events. Nil discards them.
	Logf func(format string, args ...any)
	// Sleep waits for d, honouring ctx. Tests replace it.
	Sleep func(ctx context.Context, d time.Duration) error
}

// State is the supervisor's report on one worker.
type State struct {
	Key       string
	Phase     Phase
	PID       int
	Restarts  int
	LastError string
	StartedAt time.Time
}

// Supervisor owns every worker process.
type Supervisor struct {
	opts     Options
	launcher Launcher
	prober   ReadyProber

	mu    sync.Mutex
	state map[string]*State
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
	return &Supervisor{opts: opts, launcher: launcher, prober: prober, state: map[string]*State{}}
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
	var wg sync.WaitGroup
	for _, cl := range clients {
		if !cl.Enabled {
			continue
		}
		s.mu.Lock()
		if _, dup := s.state[cl.Key]; dup {
			s.mu.Unlock()
			continue
		}
		s.state[cl.Key] = &State{Key: cl.Key, Phase: PhaseStarting}
		s.mu.Unlock()

		wg.Add(1)
		go func(cl appconfig.Client) {
			defer wg.Done()
			s.supervise(ctx, cl)
		}(cl)
	}

	<-ctx.Done()
	wg.Wait()
	return nil
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
func (s *Supervisor) supervise(ctx context.Context, cl appconfig.Client) {
	restarts := 0
	for ctx.Err() == nil {
		if err := RemoveStaleSocket(cl.RPCSocketPath); err != nil {
			s.fail(cl.Key, err)
			s.opts.Logf("supervisor: %s: %v", cl.Key, err)
			return
		}

		launch := Launch{
			Key:        cl.Key,
			ConfigPath: s.opts.ConfigPath,
			SocketPath: cl.RPCSocketPath,
			Args:       []string{s.opts.BinaryPath, "worker", "--key", cl.Key, "--config", s.opts.ConfigPath},
		}
		proc, err := s.launcher.Start(launch)
		if err != nil {
			restarts++
			s.opts.Logf("supervisor: %s: start failed (restart %d/%d): %v",
				cl.Key, restarts, s.opts.MaxRestartAttempts, err)
			if !s.retry(ctx, cl.Key, restarts, err) {
				return
			}
			continue
		}

		handle := newProcHandle(proc)
		s.update(cl.Key, func(st *State) {
			st.Phase = PhaseStarting
			st.PID = proc.Pid()
			st.StartedAt = time.Now().UTC()
			st.LastError = ""
		})
		s.opts.Logf("supervisor: %s: started pid=%d", cl.Key, proc.Pid())

		reason, ready := s.awaitReady(ctx, handle, cl.RPCSocketPath)
		if ctx.Err() != nil {
			s.stop(handle)
			return
		}
		if !ready {
			restarts++
			s.opts.Logf("supervisor: %s: %s (restart %d/%d)",
				cl.Key, reason, restarts, s.opts.MaxRestartAttempts)
			s.stop(handle)
			if !s.retry(ctx, cl.Key, restarts, errors.New(reason)) {
				return
			}
			continue
		}

		s.update(cl.Key, func(st *State) { st.Phase = PhaseRunning })
		s.opts.Logf("supervisor: %s: ready", cl.Key)

		exitErr, wedged, stable := s.watchRunning(ctx, handle, cl.RPCSocketPath)
		if stable {
			// The worker proved stable, so the next failure starts a new budget.
			restarts = 0
			s.update(cl.Key, func(st *State) { st.Restarts = 0 })
		}
		if wedged {
			restarts++
			s.opts.Logf("supervisor: %s: stopped answering its socket (%d failed probes); replacing it",
				cl.Key, s.opts.LivenessFailures)
			s.stop(handle)
			if !s.retry(ctx, cl.Key, restarts, errors.New("worker stopped answering its socket")) {
				return
			}
			continue
		}
		if ctx.Err() != nil {
			s.stop(handle)
			return
		}

		restarts++
		s.recordExit(cl.Key, exitErr)
		s.opts.Logf("supervisor: %s: exited (restart %d/%d): %v",
			cl.Key, restarts, s.opts.MaxRestartAttempts, exitErr)
		if !s.retry(ctx, cl.Key, restarts, exitReason(exitErr)) {
			return
		}
	}
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
func (s *Supervisor) watchRunning(ctx context.Context, handle *procHandle, socket string) (exitErr error, wedged, stable bool) {
	stableTimer := time.NewTimer(s.opts.StableAfter)
	defer stableTimer.Stop()
	liveness := time.NewTicker(s.opts.LivenessInterval)
	defer liveness.Stop()

	wasStable := false
	failures := 0
	for {
		select {
		case <-ctx.Done():
			return nil, false, wasStable
		case <-handle.done:
			return handle.waitErr(), false, wasStable
		case <-stableTimer.C:
			wasStable = true
			stableTimer.Stop()
		case <-liveness.C:
			probeCtx, cancel := context.WithTimeout(ctx, s.opts.LivenessInterval)
			err := s.prober.Ready(probeCtx, socket)
			cancel()
			if err == nil {
				failures = 0
				continue
			}
			if handle.exited() {
				// The process is gone; the exit path reports why.
				return handle.waitErr(), false, wasStable
			}
			failures++
			s.opts.Logf("supervisor: pid %d: probe %d/%d failed: %v",
				handle.proc.Pid(), failures, s.opts.LivenessFailures, err)
			if failures >= s.opts.LivenessFailures {
				return nil, true, wasStable
			}
		}
	}
}

// awaitReady polls the worker's RPC socket until it answers or the startup
// timeout expires.
func (s *Supervisor) awaitReady(ctx context.Context, handle *procHandle, socket string) (string, bool) {
	deadline := time.Now().Add(s.opts.StartupTimeout)
	for {
		if ctx.Err() != nil {
			return "", false
		}
		if handle.exited() {
			return "process exited before it became ready", false
		}

		probeCtx, cancel := context.WithTimeout(ctx, s.opts.ProbeInterval*5)
		err := s.prober.Ready(probeCtx, socket)
		cancel()
		if err == nil {
			return "", true
		}
		if time.Now().After(deadline) {
			return fmt.Sprintf("did not become ready within %s", s.opts.StartupTimeout), false
		}
		if err := s.opts.Sleep(ctx, s.opts.ProbeInterval); err != nil {
			return "", false
		}
	}
}

// retry records a failure and waits before the next start. It returns false
// when the budget is exhausted (leaving the worker failed) or ctx ended.
func (s *Supervisor) retry(ctx context.Context, key string, attempt int, cause error) bool {
	if attempt > s.opts.MaxRestartAttempts {
		s.fail(key, cause)
		s.opts.Logf("supervisor: %s: giving up after %d restart attempts", key, s.opts.MaxRestartAttempts)
		return false
	}
	delay := Backoff(s.opts.RestartInitialDelay, s.opts.RestartMaxDelay, attempt, rand.Float64())
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

// States reports the current state of every supervised worker.
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
