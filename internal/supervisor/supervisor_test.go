package supervisor_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/mirza/telesync/internal/appconfig"
	"github.com/mirza/telesync/internal/supervisor"
)

// fakeProcess is one scripted child process. By default it exits gracefully
// when signalled, like a worker that shuts down cleanly.
type fakeProcess struct {
	pid     int
	signals chan os.Signal
	done    chan struct{}
	once    sync.Once
	mu      sync.Mutex
	// ignoreSignals models a wedged child that must be killed.
	ignoreSignals bool
	killed        bool
}

func newFakeProcess(pid int, ignoreSignals bool) *fakeProcess {
	return &fakeProcess{
		pid: pid, ignoreSignals: ignoreSignals,
		signals: make(chan os.Signal, 4), done: make(chan struct{}),
	}
}

func (p *fakeProcess) Pid() int { return p.pid }

func (p *fakeProcess) Wait() error {
	<-p.done
	return nil
}

func (p *fakeProcess) Signal(sig os.Signal) error {
	p.signals <- sig
	p.mu.Lock()
	ignore := p.ignoreSignals
	p.mu.Unlock()
	if !ignore {
		p.exit()
	}
	return nil
}

func (p *fakeProcess) Kill() error {
	p.mu.Lock()
	p.killed = true
	p.mu.Unlock()
	p.exit()
	return nil
}

func (p *fakeProcess) wasKilled() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.killed
}

// exit simulates an unexpected termination.
func (p *fakeProcess) exit() { p.once.Do(func() { close(p.done) }) }

func (p *fakeProcess) exited() bool {
	select {
	case <-p.done:
		return true
	default:
		return false
	}
}

// fakeLauncher records launches and hands out scripted processes.
type fakeLauncher struct {
	mu            sync.Mutex
	launches      []supervisor.Launch
	procs         []*fakeProcess
	startErr      error
	ignoreSignals bool
	// onStart, when set, runs after a process is created.
	onStart func(n int, l supervisor.Launch)
	nextPID int
}

func (l *fakeLauncher) Start(launch supervisor.Launch) (supervisor.Process, error) {
	l.mu.Lock()
	if l.startErr != nil {
		err := l.startErr
		l.mu.Unlock()
		return nil, err
	}
	l.nextPID++
	p := newFakeProcess(l.nextPID, l.ignoreSignals)
	n := len(l.procs)
	l.procs = append(l.procs, p)
	l.launches = append(l.launches, launch)
	hook := l.onStart
	l.mu.Unlock()
	if hook != nil {
		hook(n, launch)
	}
	return p, nil
}

func (l *fakeLauncher) snapshot() ([]supervisor.Launch, []*fakeProcess) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]supervisor.Launch(nil), l.launches...), append([]*fakeProcess(nil), l.procs...)
}

// fakeProber reports readiness, optionally only after a scripted number of
// attempts.
type fakeProber struct {
	mu       sync.Mutex
	failures map[string]int
	fail     map[string]error
	calls    map[string]int
}

func newProber() *fakeProber {
	return &fakeProber{failures: map[string]int{}, fail: map[string]error{}, calls: map[string]int{}}
}

func (p *fakeProber) Ready(_ context.Context, socket string) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.calls[socket]++
	if err, ok := p.fail[socket]; ok {
		return err
	}
	if left, ok := p.failures[socket]; ok && left > 0 {
		p.failures[socket] = left - 1
		return errors.New("not ready yet")
	}
	return nil
}

func clients(keys ...string) []appconfig.Client {
	out := make([]appconfig.Client, 0, len(keys))
	for _, k := range keys {
		out = append(out, appconfig.Client{
			Key:           k,
			Type:          appconfig.Official,
			Enabled:       true,
			SessionPath:   filepath.Join("/sessions", k+".json"),
			RPCSocketPath: filepath.Join("/run", k+".sock"),
		})
	}
	return out
}

// noSleep records requested delays instead of waiting, so backoff tests run in
// constant time.
type noSleep struct {
	mu     sync.Mutex
	delays []time.Duration
	block  chan struct{}
}

func (s *noSleep) sleep(ctx context.Context, d time.Duration) error {
	s.mu.Lock()
	s.delays = append(s.delays, d)
	s.mu.Unlock()
	if s.block == nil {
		return nil
	}
	select {
	case <-s.block:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (s *noSleep) recorded() []time.Duration {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]time.Duration(nil), s.delays...)
}

func testOptions(t *testing.T) supervisor.Options {
	t.Helper()
	return supervisor.Options{
		BinaryPath:          "/usr/local/bin/telesync",
		ConfigPath:          "/etc/telesync/telesync.json",
		RunDir:              "/run",
		RestartInitialDelay: 100 * time.Millisecond,
		RestartMaxDelay:     time.Second,
		MaxRestartAttempts:  3,
		StartupTimeout:      2 * time.Second,
		ShutdownTimeout:     500 * time.Millisecond,
		ProbeInterval:       10 * time.Millisecond,
	}
}

// waitFor polls until cond holds or the deadline expires.
func waitFor(t *testing.T, cond func() bool, what string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

func TestStartsOneProcessPerEnabledClient(t *testing.T) {
	launcher := &fakeLauncher{}
	sup := supervisor.New(testOptions(t), launcher, newProber())

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- sup.Run(ctx, clients("official", "the-cat")) }()

	waitFor(t, func() bool {
		s, _ := launcher.snapshot()
		return len(s) == 2
	}, "two launches")

	launches, _ := launcher.snapshot()
	keys := map[string]bool{}
	for _, l := range launches {
		keys[l.Key] = true
		if l.ConfigPath != "/etc/telesync/telesync.json" {
			t.Errorf("launch %s config = %q", l.Key, l.ConfigPath)
		}
		if l.SocketPath == "" || l.Key == "" {
			t.Errorf("launch = %+v", l)
		}
	}
	if !keys["official"] || !keys["the-cat"] {
		t.Fatalf("launched keys = %v", keys)
	}
	waitFor(t, func() bool {
		return sup.States()["official"].Phase == supervisor.PhaseRunning &&
			sup.States()["the-cat"].Phase == supervisor.PhaseRunning
	}, "both workers running")

	cancel()
	if err := <-done; err != nil {
		t.Fatalf("Run: %v", err)
	}
}

func TestDoesNotStartDisabledClients(t *testing.T) {
	launcher := &fakeLauncher{}
	sup := supervisor.New(testOptions(t), launcher, newProber())

	off := clients("official")[0]
	off.Enabled = false
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- sup.Run(ctx, append([]appconfig.Client{off}, clients("the-cat")...)) }()

	waitFor(t, func() bool {
		_, procs := launcher.snapshot()
		return len(procs) == 1
	}, "one launch")
	launches, _ := launcher.snapshot()
	if launches[0].Key != "the-cat" {
		t.Fatalf("launched %q", launches[0].Key)
	}
	if _, ok := sup.States()["official"]; ok {
		t.Error("a disabled client must not appear in the supervisor state")
	}
	cancel()
	<-done
}

func TestRestartsAnUnexpectedExitWithBoundedBackoff(t *testing.T) {
	launcher := &fakeLauncher{}
	prober := newProber()
	opts := testOptions(t)
	sleeper := &noSleep{}
	opts.Sleep = sleeper.sleep
	opts.MaxRestartAttempts = 4
	sup := supervisor.New(opts, launcher, prober)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- sup.Run(ctx, clients("official")) }()

	waitFor(t, func() bool {
		_, procs := launcher.snapshot()
		return len(procs) == 1
	}, "first launch")
	waitFor(t, func() bool { return sup.States()["official"].Phase == supervisor.PhaseRunning }, "running")

	// Kill the current process until the restart budget runs out.
	for launched := 1; ; {
		_, procs := launcher.snapshot()
		procs[len(procs)-1].exit()
		waitFor(t, func() bool {
			_, p := launcher.snapshot()
			return len(p) > launched || sup.States()["official"].Phase == supervisor.PhaseFailed
		}, "restart or failure")
		if sup.States()["official"].Phase == supervisor.PhaseFailed {
			break
		}
		launched++
		if launched > 10 {
			t.Fatal("the supervisor restarted far past its budget")
		}
	}

	_, procs := launcher.snapshot()
	if len(procs) != opts.MaxRestartAttempts+1 {
		t.Fatalf("launches = %d, want %d (one initial start plus the restart budget)",
			len(procs), opts.MaxRestartAttempts+1)
	}

	delays := sleeper.recorded()
	if len(delays) < 3 {
		t.Fatalf("backoff delays = %v", delays)
	}
	for i := 1; i < len(delays); i++ {
		if delays[i] < delays[i-1] {
			t.Fatalf("backoff must not shrink: %v", delays)
		}
	}
	for _, d := range delays {
		if d > opts.RestartMaxDelay+opts.RestartMaxDelay/2 {
			t.Fatalf("backoff %s exceeds the configured maximum %s (jitter included)", d, opts.RestartMaxDelay)
		}
	}
}

func TestBackoffIsJittered(t *testing.T) {
	// Jitter 0.5 is the midpoint, i.e. no deviation from the exponential value.
	base := supervisor.Backoff(100*time.Millisecond, time.Second, 3, 0.5)
	if base != 400*time.Millisecond {
		t.Fatalf("midpoint step 3 = %s, want 400ms", base)
	}
	low := supervisor.Backoff(100*time.Millisecond, time.Second, 3, 0.0)
	high := supervisor.Backoff(100*time.Millisecond, time.Second, 3, 1.0)
	if !(low < high) {
		t.Fatalf("jitter must vary the delay: low=%s high=%s", low, high)
	}
	if low < 200*time.Millisecond || high > 600*time.Millisecond {
		t.Fatalf("jitter left the ±25%% band: low=%s high=%s", low, high)
	}
	if got := supervisor.Backoff(100*time.Millisecond, 300*time.Millisecond, 10, 0.5); got > 300*time.Millisecond {
		t.Fatalf("backoff %s exceeds the maximum", got)
	}
	if got := supervisor.Backoff(time.Second, time.Second, 1, 0.5); got != time.Second {
		t.Fatalf("first step = %s", got)
	}
}

func (p *fakeProber) setFailure(socket string, err error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if err == nil {
		delete(p.fail, socket)
		return
	}
	p.fail[socket] = err
}

func TestAHungWorkerIsRestartedAfterRepeatedFailedProbes(t *testing.T) {
	// A process that is alive but no longer answers its socket is not healthy.
	// It must be killed so the normal restart path can replace it.
	// A wedged worker that ignores SIGINT must be killed before replacement.
	launcher := &fakeLauncher{ignoreSignals: true}
	prober := newProber()
	opts := testOptions(t)
	opts.LivenessInterval = 5 * time.Millisecond
	opts.LivenessFailures = 3
	opts.ShutdownTimeout = 20 * time.Millisecond
	opts.Sleep = (&noSleep{}).sleep
	sup := supervisor.New(opts, launcher, prober)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- sup.Run(ctx, clients("official")) }()

	waitFor(t, func() bool { return sup.States()["official"].Phase == supervisor.PhaseRunning }, "running")
	prober.setFailure("/run/official.sock", errors.New("no answer"))

	waitFor(t, func() bool {
		_, procs := launcher.snapshot()
		return len(procs) == 2
	}, "a replacement process")

	_, procs := launcher.snapshot()
	if !procs[0].wasKilled() {
		t.Error("the hung worker should be killed before a replacement is started")
	}
	cancel()
	<-done
}

func TestATransientProbeFailureDoesNotRestart(t *testing.T) {
	launcher := &fakeLauncher{}
	prober := newProber()
	opts := testOptions(t)
	opts.LivenessInterval = 40 * time.Millisecond
	opts.LivenessFailures = 3
	opts.Sleep = (&noSleep{}).sleep
	sup := supervisor.New(opts, launcher, prober)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- sup.Run(ctx, clients("official")) }()
	waitFor(t, func() bool { return sup.States()["official"].Phase == supervisor.PhaseRunning }, "running")

	// One failed probe, then healthy again: below the threshold.
	prober.setFailure("/run/official.sock", errors.New("one slow answer"))
	time.Sleep(60 * time.Millisecond)
	prober.setFailure("/run/official.sock", nil)
	time.Sleep(200 * time.Millisecond)

	_, procs := launcher.snapshot()
	if len(procs) != 1 {
		t.Fatalf("a transient probe failure restarted the worker (%d launches)", len(procs))
	}
	if got := sup.States()["official"].Phase; got != supervisor.PhaseRunning {
		t.Fatalf("phase = %q", got)
	}
	cancel()
	<-done
}

func TestGivesUpAfterTheRestartBudget(t *testing.T) {
	launcher := &fakeLauncher{startErr: errors.New("exec: no such file")}
	opts := testOptions(t)
	sleeper := &noSleep{}
	opts.Sleep = sleeper.sleep
	opts.MaxRestartAttempts = 2
	sup := supervisor.New(opts, launcher, newProber())

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- sup.Run(ctx, clients("official")) }()

	waitFor(t, func() bool { return sup.States()["official"].Phase == supervisor.PhaseFailed }, "failed")

	state := sup.States()["official"]
	if state.LastError == "" {
		t.Error("a failed worker must report why")
	}
	if len(sleeper.recorded()) < 2 {
		t.Errorf("backoff delays = %v", sleeper.recorded())
	}
	cancel()
	<-done
}

func TestAStartupThatNeverBecomesReadyIsRestartedWithinBudget(t *testing.T) {
	launcher := &fakeLauncher{}
	prober := newProber()
	prober.fail["/run/official.sock"] = errors.New("still starting")

	opts := testOptions(t)
	opts.StartupTimeout = 30 * time.Millisecond
	opts.MaxRestartAttempts = 2
	opts.ProbeInterval = 5 * time.Millisecond
	sleeper := &noSleep{}
	opts.Sleep = sleeper.sleep
	sup := supervisor.New(opts, launcher, prober)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- sup.Run(ctx, clients("official")) }()

	waitFor(t, func() bool { return sup.States()["official"].Phase == supervisor.PhaseFailed }, "failed")

	_, procs := launcher.snapshot()
	if len(procs) == 0 {
		t.Fatal("no process was started")
	}
	for _, p := range procs {
		if !p.exited() {
			t.Error("a worker that never became ready must be terminated before retrying")
		}
	}
	if len(procs) < 2 {
		t.Errorf("launches = %d, want a retry", len(procs))
	}
	cancel()
	<-done
}

func TestOneWorkerFailureDoesNotStopTheOthers(t *testing.T) {
	launcher := &fakeLauncher{}
	prober := newProber()
	prober.fail["/run/bad.sock"] = errors.New("refused")

	opts := testOptions(t)
	opts.StartupTimeout = 20 * time.Millisecond
	opts.MaxRestartAttempts = 1
	opts.ProbeInterval = 5 * time.Millisecond
	opts.Sleep = (&noSleep{}).sleep
	sup := supervisor.New(opts, launcher, prober)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- sup.Run(ctx, clients("bad", "good")) }()

	waitFor(t, func() bool {
		states := sup.States()
		return states["bad"].Phase == supervisor.PhaseFailed && states["good"].Phase == supervisor.PhaseRunning
	}, "one failed and one running")

	if err := ctx.Err(); err != nil {
		t.Fatal(err)
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatalf("Run: %v", err)
	}
}

func TestGracefulShutdownSignalsEveryChild(t *testing.T) {
	launcher := &fakeLauncher{}
	prober := newProber()
	sup := supervisor.New(testOptions(t), launcher, prober)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- sup.Run(ctx, clients("official", "the-cat")) }()

	waitFor(t, func() bool { return len(sup.States()) == 2 }, "both registered")
	waitFor(t, func() bool {
		_, procs := launcher.snapshot()
		return len(procs) == 2
	}, "two launches")

	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Run: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not return after cancellation")
	}

	_, procs := launcher.snapshot()
	for i, p := range procs {
		if !p.exited() {
			t.Errorf("process %d did not exit after the interrupt", i)
			continue
		}
		if p.wasKilled() {
			t.Errorf("process %d was killed instead of being allowed to exit gracefully", i)
		}
		select {
		case sig := <-p.signals:
			if sig != os.Interrupt {
				t.Errorf("process %d got %v, want interrupt", i, sig)
			}
		case <-time.After(2 * time.Second):
			t.Errorf("process %d was never signalled", i)
		}
	}
}

func TestShutdownKillsAChildThatIgnoresTheSignal(t *testing.T) {
	launcher := &fakeLauncher{ignoreSignals: true}
	opts := testOptions(t)
	opts.ShutdownTimeout = 30 * time.Millisecond
	sup := supervisor.New(opts, launcher, newProber())

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- sup.Run(ctx, clients("official")) }()
	waitFor(t, func() bool {
		_, procs := launcher.snapshot()
		return len(procs) == 1
	}, "launch")

	cancel()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("Run did not return")
	}
	_, procs := launcher.snapshot()
	if !procs[0].wasKilled() {
		t.Fatal("a child that ignores the signal must be killed after the timeout")
	}
}

func TestStaleSocketFilesAreRemovedButOtherFilesAreNot(t *testing.T) {
	dir := t.TempDir()
	socket := filepath.Join(dir, "official.sock")
	other := filepath.Join(dir, "notes.txt")
	if err := os.WriteFile(other, []byte("keep me"), 0o600); err != nil {
		t.Fatal(err)
	}
	// A regular file at the socket path is not a socket; it must be a hard
	// error rather than silently deleted.
	if err := os.WriteFile(socket, []byte("not a socket"), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := supervisor.RemoveStaleSocket(socket); err == nil {
		t.Fatal("a regular file at the socket path must be an error")
	}
	if _, err := os.Stat(socket); err != nil {
		t.Fatal("a regular file must not be removed")
	}

	if err := os.Remove(socket); err != nil {
		t.Fatal(err)
	}
	if err := supervisor.RemoveStaleSocket(socket); err != nil {
		t.Fatalf("a missing socket is fine: %v", err)
	}
	if err := supervisor.RemoveStaleSocket(filepath.Join(dir, "core.sock")); err != nil {
		t.Fatalf("a missing socket must be fine: %v", err)
	}
	if err := supervisor.RemoveStaleSocket(other); err == nil {
		t.Fatal("a path that is not a .sock must be refused")
	}
	if data, err := os.ReadFile(other); err != nil || string(data) != "keep me" {
		t.Fatal("unrelated files must never be touched")
	}
}

func TestStaleSocketIsRefusedOutsideTheRunDirectory(t *testing.T) {
	dir := t.TempDir()
	// Deliberately not filepath.Join: Join would clean the traversal away.
	if err := supervisor.RemoveStaleSocket(dir + "/../escape.sock"); err == nil {
		t.Fatal("paths containing .. must be rejected")
	}
}

func TestStatesExposeTheProcessView(t *testing.T) {
	launcher := &fakeLauncher{}
	sup := supervisor.New(testOptions(t), launcher, newProber())
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = sup.Run(ctx, clients("official")) }()

	waitFor(t, func() bool { return sup.States()["official"].Phase == supervisor.PhaseRunning }, "running")
	state := sup.States()["official"]
	if state.Key != "official" || state.PID == 0 {
		t.Fatalf("state = %+v", state)
	}
	if state.Restarts != 0 {
		t.Errorf("restarts = %d", state.Restarts)
	}
	if state.StartedAt.IsZero() {
		t.Error("started_at must be set")
	}
	cancel()
}

func TestRestartCounterResetsAfterAStableRun(t *testing.T) {
	launcher := &fakeLauncher{}
	opts := testOptions(t)
	opts.Sleep = (&noSleep{}).sleep
	opts.StableAfter = 20 * time.Millisecond
	sup := supervisor.New(opts, launcher, newProber())

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = sup.Run(ctx, clients("official")) }()
	waitFor(t, func() bool { return sup.States()["official"].Phase == supervisor.PhaseRunning }, "running")

	time.Sleep(40 * time.Millisecond)
	_, procs := launcher.snapshot()
	procs[0].exit()
	waitFor(t, func() bool {
		_, p := launcher.snapshot()
		return len(p) == 2 && sup.States()["official"].Phase == supervisor.PhaseRunning
	}, "restarted")
	// The restart budget resets after a stable run, so the crash that follows
	// starts a new sequence: it is restart 1 of a fresh budget, not restart 2.
	if got := sup.States()["official"].Restarts; got != 1 {
		t.Fatalf("restarts after a stable run = %d, want 1", got)
	}
}
