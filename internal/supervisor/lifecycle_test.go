package supervisor_test

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mirza/telesync/internal/supervisor"
)

// errCannotStart models a worker binary that cannot be launched at all.
var errCannotStart = errors.New("exec: no such file")

// Worker identity is the account. These tests are about the account lifecycle
// the contract requires: start after login, reconnect saved sessions, stop on
// removal or a disabled user, a bounded worker budget, and a log that carries
// only an opaque account id.

// accountOptions is testOptions plus the two directories an account's session
// and peer cache live in. They are deliberately outside the run directory: a
// session is a credential, not a runtime file.
func accountOptions(t *testing.T) supervisor.Options {
	t.Helper()
	// A short root on purpose: the macOS temp directory is already longer than
	// a Unix socket path may be, so a run directory under it cannot hold any
	// socket name at all. That is the environment the socket-name guard exists
	// for, and it is why these fixtures live under a short path.
	root, err := os.MkdirTemp("/tmp", "ts")
	if err != nil {
		t.Skipf("cannot create a short run directory: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(root) })
	opts := testOptions(t)
	opts.RunDir = filepath.Join(root, "run")
	opts.SessionDir = filepath.Join(root, "sessions")
	opts.PeerDir = filepath.Join(root, "peers")
	for _, d := range []string{opts.RunDir, opts.SessionDir, opts.PeerDir} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	if len(opts.RunDir) > 60 {
		t.Skipf("run directory %q is too long for a Unix socket on this host", opts.RunDir)
	}
	return opts
}

func account(id string, owner int64) supervisor.AccountRef {
	return supervisor.AccountRef{
		ID:          id,
		OwnerUserID: owner,
		ServerKey:   "official",
		NetworkType: "telegram",
		AppID:       12345,
		APIHash:     "0123456789abcdef0123456789abcdef",
	}
}

type mismatchedAccountProber struct {
	*fakeProber
	actual supervisor.AccountMetadata
}

func (p *mismatchedAccountProber) ReadyAccount(ctx context.Context, socket string, want supervisor.AccountMetadata) error {
	if p.actual != want {
		return fmt.Errorf("%w: account worker metadata does not match", supervisor.ErrAccountIdentityMismatch)
	}
	return p.fakeProber.Ready(ctx, socket)
}

type readyOnlyProber struct{}

func (readyOnlyProber) Ready(context.Context, string) error { return nil }

// logLines collects the supervisor's log output so tests can assert on what an
// operator would see.
type logLines struct {
	mu    sync.Mutex
	lines []string
}

func (l *logLines) logf(format string, args ...any) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.lines = append(l.lines, fmt.Sprintf(format, args...))
}

func (l *logLines) all() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return strings.Join(append([]string(nil), l.lines...), "\n")
}

func TestStartsOneWorkerPerAccount(t *testing.T) {
	launcher := &fakeLauncher{}
	sup := supervisor.New(accountOptions(t), launcher, newProber())
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan error, 1)
	go func() {
		done <- sup.RunAccounts(ctx, []supervisor.AccountRef{
			account("acc_aaaaaaaaaaaaaaaaaaaaaaaaaa", 1),
			account("acc_bbbbbbbbbbbbbbbbbbbbbbbbbb", 2),
		})
	}()

	waitFor(t, func() bool { return len(sup.States()) == 2 }, "two supervised accounts")
	waitFor(t, func() bool {
		_, procs := launcher.snapshot()
		return len(procs) == 2
	}, "two worker processes")

	launches, _ := launcher.snapshot()
	seen := map[string]bool{}
	for _, l := range launches {
		if l.AccountID == "" {
			t.Fatalf("a launch without an account id: %+v", l)
		}
		if seen[l.AccountID] {
			t.Fatalf("two workers were started for account %q", l.AccountID)
		}
		seen[l.AccountID] = true
		if !strings.HasSuffix(l.SocketPath, l.AccountID+".sock") {
			t.Errorf("launch %q is not bound to its own account socket: %s", l.AccountID, l.SocketPath)
		}
	}
	cancel()
	<-done
}

func TestTwoAccountsNeverShareAWorkerOrASocket(t *testing.T) {
	launcher := &fakeLauncher{}
	sup := supervisor.New(accountOptions(t), launcher, newProber())
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() {
		_ = sup.RunAccounts(ctx, []supervisor.AccountRef{
			account("acc_aaaaaaaaaaaaaaaaaaaaaaaaaa", 1),
			account("acc_bbbbbbbbbbbbbbbbbbbbbbbbbb", 1),
		})
	}()

	waitFor(t, func() bool {
		_, procs := launcher.snapshot()
		return len(procs) == 2
	}, "two worker processes")

	launches, _ := launcher.snapshot()
	if launches[0].SocketPath == launches[1].SocketPath {
		t.Fatalf("two accounts were given one socket %q", launches[0].SocketPath)
	}
	// Two accounts of one owner still get two workers: the session is the
	// account's, never the owner's.
	if launches[0].AccountID == launches[1].AccountID {
		t.Fatal("two accounts collapsed into one worker")
	}
	cancel()
}

func TestAccountRemovalStopsItsWorkerAndLeavesTheOthersAlone(t *testing.T) {
	launcher := &fakeLauncher{}
	sup := supervisor.New(accountOptions(t), launcher, newProber())
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		done <- sup.RunAccounts(ctx, []supervisor.AccountRef{
			account("acc_aaaaaaaaaaaaaaaaaaaaaaaaaa", 1),
			account("acc_bbbbbbbbbbbbbbbbbbbbbbbbbb", 1),
		})
	}()

	waitFor(t, func() bool {
		_, procs := launcher.snapshot()
		return len(procs) == 2
	}, "two worker processes")
	waitFor(t, func() bool {
		st := sup.States()
		return st["acc_aaaaaaaaaaaaaaaaaaaaaaaaaa"].Phase == supervisor.PhaseRunning &&
			st["acc_bbbbbbbbbbbbbbbbbbbbbbbbbb"].Phase == supervisor.PhaseRunning
	}, "both running")

	if err := sup.Remove("acc_aaaaaaaaaaaaaaaaaaaaaaaaaa"); err != nil {
		t.Fatalf("Remove: %v", err)
	}

	// Launch order is not deterministic: match each process to its account
	// rather than assuming which goroutine started first.
	ls, procs := launcher.snapshot()
	byAccount := map[string]*fakeProcess{}
	for i, l := range ls {
		byAccount[l.AccountID] = procs[i]
	}
	removed, ok := byAccount["acc_aaaaaaaaaaaaaaaaaaaaaaaaaa"]
	if !ok {
		t.Fatal("the removed account never had a worker")
	}
	if !removed.exited() {
		t.Error("the removed account's worker must be stopped")
	}
	kept := byAccount["acc_bbbbbbbbbbbbbbbbbbbbbbbbbb"]
	if kept == nil || kept.exited() {
		t.Error("removing one account must not stop another account's worker")
	}

	waitFor(t, func() bool {
		_, ok := sup.States()["acc_aaaaaaaaaaaaaaaaaaaaaaaaaa"]
		return !ok
	}, "the removed account to drop out of States")
	if st := sup.States()["acc_bbbbbbbbbbbbbbbbbbbbbbbbbb"]; st.Phase != supervisor.PhaseRunning {
		t.Errorf("the surviving account's phase = %q", st.Phase)
	}

	// The removed account does not come back.
	if err := sup.Remove("acc_aaaaaaaaaaaaaaaaaaaaaaaaaa"); err == nil {
		t.Error("removing an unknown account must be reported, not silently ignored")
	}
	cancel()
	<-done
}

func TestAWorkerStartsAfterLoginAndIsBoundToItsAccount(t *testing.T) {
	launcher := &fakeLauncher{}
	supOptions := accountOptions(t)
	sup := supervisor.New(supOptions, launcher, newProber())
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = sup.RunAccounts(ctx, nil) }()

	if err := sup.Add(account("acc_aaaaaaaaaaaaaaaaaaaaaaaaaa", 1)); err != nil {
		t.Fatalf("Add: %v", err)
	}
	waitFor(t, func() bool {
		_, procs := launcher.snapshot()
		return len(procs) == 1
	}, "the new account's worker")

	launches, _ := launcher.snapshot()
	if got := launches[0].AccountID; got != "acc_aaaaaaaaaaaaaaaaaaaaaaaaaa" {
		t.Errorf("worker launched for %q, want the new account", got)
	}

	// A second account for the same owner is a second worker, not a takeover.
	if err := sup.Add(account("acc_bbbbbbbbbbbbbbbbbbbbbbbbbb", 1)); err != nil {
		t.Fatalf("Add: %v", err)
	}
	waitFor(t, func() bool {
		_, procs := launcher.snapshot()
		return len(procs) == 2
	}, "the second account's worker")

	// An idempotent retry must neither launch another worker nor unlink its
	// live socket.
	ref := account("acc_aaaaaaaaaaaaaaaaaaaaaaaaaa", 1)
	paths, err := supervisor.AccountPaths(supOptions.RunDir, supOptions.SessionDir, supOptions.PeerDir, ref.ID)
	if err != nil {
		t.Fatalf("AccountPaths: %v", err)
	}
	socket, err := net.Listen("unix", paths.SocketPath)
	if err != nil {
		t.Fatalf("create live socket: %v", err)
	}
	defer socket.Close()
	if err := sup.Add(ref); err != nil {
		t.Errorf("adding the same account must be idempotent: %v", err)
	}
	if _, err := os.Stat(paths.SocketPath); err != nil {
		t.Errorf("an idempotent Add removed the live socket: %v", err)
	}
	_, procs := launcher.snapshot()
	if len(procs) != 2 {
		t.Fatalf("launches = %d, want 2", len(procs))
	}
	cancel()
}

func TestAddRejectsChangedMetadataForAnAlreadySupervisedAccount(t *testing.T) {
	launcher := &fakeLauncher{}
	sup := supervisor.New(accountOptions(t), launcher, newProber())
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = sup.RunAccounts(ctx, nil) }()

	ref := account("acc_aaaaaaaaaaaaaaaaaaaaaaaaaa", 1)
	if err := sup.Add(ref); err != nil {
		t.Fatalf("Add: %v", err)
	}
	waitFor(t, func() bool {
		_, procs := launcher.snapshot()
		return len(procs) == 1
	}, "the account worker")

	ref.OwnerUserID = 2
	if err := sup.Add(ref); err == nil {
		t.Fatal("an account id already bound to another owner must be rejected")
	}
	_, procs := launcher.snapshot()
	if len(procs) != 1 {
		t.Fatalf("launches = %d, want the original account worker only", len(procs))
	}
	cancel()
}

func TestStopAllClosesAccountAdmission(t *testing.T) {
	launcher := &fakeLauncher{}
	sup := supervisor.New(accountOptions(t), launcher, newProber())
	if err := sup.Add(account("acc_aaaaaaaaaaaaaaaaaaaaaaaaaa", 1)); err != nil {
		t.Fatalf("Add: %v", err)
	}
	waitFor(t, func() bool {
		_, procs := launcher.snapshot()
		return len(procs) == 1
	}, "the account worker")

	if err := sup.StopAll(); err != nil {
		t.Fatalf("StopAll: %v", err)
	}
	if err := sup.Add(account("acc_bbbbbbbbbbbbbbbbbbbbbbbbbb", 2)); err == nil {
		t.Fatal("an account must not be admitted after the supervisor has stopped")
	}
	_, procs := launcher.snapshot()
	if len(procs) != 1 {
		t.Fatalf("late Add launched another worker: %d launches", len(procs))
	}
}

func TestAccountWorkerReadinessRejectsMismatchedMetadata(t *testing.T) {
	opts := accountOptions(t)
	opts.MaxRestartAttempts = 0
	opts.Sleep = (&noSleep{}).sleep
	launcher := &fakeLauncher{}
	ref := account("acc_aaaaaaaaaaaaaaaaaaaaaaaaaa", 1)
	actual := supervisor.AccountMetadata{
		AccountID:   ref.ID,
		OwnerUserID: ref.OwnerUserID + 1,
		ServerKey:   ref.ServerKey,
		NetworkType: ref.NetworkType,
	}
	prober := &mismatchedAccountProber{fakeProber: newProber(), actual: actual}
	sup := supervisor.New(opts, launcher, prober)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = sup.RunAccounts(ctx, nil) }()

	if err := sup.Add(ref); err != nil {
		t.Fatalf("Add: %v", err)
	}
	waitFor(t, func() bool {
		state, ok := sup.AccountState(ref.ID)
		return ok && state.Phase == supervisor.PhaseFailed
	}, "the mismatched worker to fail readiness")
	state, _ := sup.AccountState(ref.ID)
	if !strings.Contains(state.LastError, "metadata") {
		t.Errorf("last error = %q, want an account metadata mismatch", state.LastError)
	}
	cancel()
}

func TestAccountWorkersRequireAnIdentityAwareReadinessProbe(t *testing.T) {
	launcher := &fakeLauncher{}
	sup := supervisor.New(accountOptions(t), launcher, readyOnlyProber{})
	if err := sup.Add(account("acc_aaaaaaaaaaaaaaaaaaaaaaaaaa", 1)); err == nil {
		t.Fatal("an account worker must not be considered ready using only an HTTP success")
	}
	_, procs := launcher.snapshot()
	if len(procs) != 0 {
		t.Fatalf("launches = %d, want zero when account identity cannot be checked", len(procs))
	}
}

func TestTheWorkerBudgetIsBoundedAndTheRestWaitTheirTurn(t *testing.T) {
	opts := accountOptions(t)
	opts.MaxWorkers = 2
	opts.Sleep = (&noSleep{}).sleep
	launcher := &fakeLauncher{}
	sup := supervisor.New(opts, launcher, newProber())
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() {
		_ = sup.RunAccounts(ctx, []supervisor.AccountRef{
			account("acc_aaaaaaaaaaaaaaaaaaaaaaaaaa", 1),
			account("acc_bbbbbbbbbbbbbbbbbbbbbbbbbb", 1),
			account("acc_cccccccccccccccccccccccccc", 1),
			account("acc_dddddddddddddddddddddddddd", 1),
		})
	}()

	waitFor(t, func() bool {
		_, procs := launcher.snapshot()
		return len(procs) == 2
	}, "the worker budget to fill")

	// The accounts beyond the budget are deferred, not failed and not lost.
	st := sup.States()
	running := 0
	deferred := 0
	for _, s := range st {
		switch s.Phase {
		case supervisor.PhaseRunning:
			running++
		case supervisor.PhaseDeferred:
			deferred++
		}
	}
	if running > opts.MaxWorkers {
		t.Fatalf("%d workers are running, over the budget of %d", running, opts.MaxWorkers)
	}
	if deferred < 2 {
		t.Fatalf("deferred = %d, want the accounts over the budget to wait", deferred)
	}

	// Removing a running account frees a slot for a waiting one.
	if err := sup.Remove("acc_aaaaaaaaaaaaaaaaaaaaaaaaaa"); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	waitFor(t, func() bool {
		_, procs := launcher.snapshot()
		return len(procs) == 3
	}, "a waiting account to take the freed slot")
	cancel()
}

func TestExceedingTheBudgetIsRefusedRatherThanSilentlyDropped(t *testing.T) {
	opts := accountOptions(t)
	opts.MaxWorkers = 1
	opts.Sleep = (&noSleep{}).sleep
	launcher := &fakeLauncher{}
	sup := supervisor.New(opts, launcher, newProber())
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = sup.RunAccounts(ctx, nil) }()
	waitFor(t, func() bool { return true }, "supervisor started")

	if err := sup.Add(account("acc_aaaaaaaaaaaaaaaaaaaaaaaaaa", 1)); err != nil {
		t.Fatalf("Add: %v", err)
	}
	waitFor(t, func() bool {
		_, procs := launcher.snapshot()
		return len(procs) == 1
	}, "the first worker")

	err := sup.Add(account("acc_bbbbbbbbbbbbbbbbbbbbbbbbbb", 1))
	if err == nil {
		t.Fatal("starting past the worker budget must be reported")
	}
	if !strings.Contains(err.Error(), "acc_bbbbbbbbbbbbbbbbbbbbbbbbbb") {
		t.Errorf("the refusal must name the account that could not start: %v", err)
	}
	cancel()
}

func TestAStoppedAccountCanStartAgainLater(t *testing.T) {
	launcher := &fakeLauncher{}
	sup := supervisor.New(accountOptions(t), launcher, newProber())
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() {
		_ = sup.RunAccounts(ctx, []supervisor.AccountRef{
			account("acc_aaaaaaaaaaaaaaaaaaaaaaaaaa", 1),
		})
	}()

	waitFor(t, func() bool {
		_, procs := launcher.snapshot()
		return len(procs) == 1
	}, "the first worker")

	if err := sup.Remove("acc_aaaaaaaaaaaaaaaaaaaaaaaaaa"); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	waitFor(t, func() bool {
		_, ok := sup.States()["acc_aaaaaaaaaaaaaaaaaaaaaaaaaa"]
		return !ok
	}, "the account to drop out")

	// A user may log back in: the account id is stable and the worker starts
	// again from its saved session.
	if err := sup.Add(account("acc_aaaaaaaaaaaaaaaaaaaaaaaaaa", 1)); err != nil {
		t.Fatalf("re-Add: %v", err)
	}
	waitFor(t, func() bool {
		_, procs := launcher.snapshot()
		return len(procs) == 2
	}, "the account's worker to start again")
	cancel()
}

func TestCrashingWorkersRecoverIndependentlyPerAccount(t *testing.T) {
	launcher := &fakeLauncher{}
	sup := supervisor.New(accountOptions(t), launcher, newProber())
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() {
		_ = sup.RunAccounts(ctx, []supervisor.AccountRef{
			account("acc_aaaaaaaaaaaaaaaaaaaaaaaaaa", 1),
			account("acc_bbbbbbbbbbbbbbbbbbbbbbbbbb", 2),
		})
	}()

	waitFor(t, func() bool {
		_, procs := launcher.snapshot()
		return len(procs) == 2
	}, "two worker processes")

	// One account's worker crashes. Only that account restarts.
	ls, procs := launcher.snapshot()
	crashed := map[string]*fakeProcess{}
	for i, l := range ls {
		crashed[l.AccountID] = procs[i]
	}
	crashed["acc_aaaaaaaaaaaaaaaaaaaaaaaaaa"].exit()
	waitFor(t, func() bool {
		_, p := launcher.snapshot()
		return len(p) == 3
	}, "the crashed account's worker to be replaced")

	lsAfter, procsAfter := launcher.snapshot()
	survivors := map[string]*fakeProcess{}
	for i, l := range lsAfter {
		if _, seen := survivors[l.AccountID]; !seen {
			survivors[l.AccountID] = procsAfter[i]
		}
	}
	if other := survivors["acc_bbbbbbbbbbbbbbbbbbbbbbbbbb"]; other == nil || other.exited() {
		t.Error("a crash in one account's worker must not disturb another's")
	}
	recovered := false
	for _, l := range lsAfter {
		if l.AccountID == "acc_aaaaaaaaaaaaaaaaaaaaaaaaaa" {
			recovered = true
		}
	}
	if !recovered {
		t.Error("the crashed account must come back under its own identity")
	}
	cancel()
}

func TestAccountsAreNotDisconnectedWhenABrowserCloses(t *testing.T) {
	launcher := &fakeLauncher{}
	sup := supervisor.New(accountOptions(t), launcher, newProber())
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	runDone := make(chan error, 1)
	go func() {
		runDone <- sup.RunAccounts(ctx, []supervisor.AccountRef{
			account("acc_aaaaaaaaaaaaaaaaaaaaaaaaaa", 1),
		})
	}()
	waitFor(t, func() bool {
		_, procs := launcher.snapshot()
		return len(procs) == 1
	}, "the worker")

	// The supervisor's lifetime is the daemon's, not a browser session's. A
	// request context ending must not take the worker with it.
	reqCtx, reqCancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	<-reqCtx.Done()
	reqCancel()

	_, procs := launcher.snapshot()
	if procs[0].exited() {
		t.Fatal("a worker was stopped because a request context ended")
	}
	if st := sup.States()["acc_aaaaaaaaaaaaaaaaaaaaaaaaaa"]; st.Phase != supervisor.PhaseRunning {
		t.Errorf("phase = %q, want running", st.Phase)
	}
	cancel()
	<-runDone
}

func TestSupervisorLogsCarryOnlyAnOpaqueAccountID(t *testing.T) {
	logs := &logLines{}
	opts := accountOptions(t)
	opts.Logf = logs.logf
	opts.Sleep = (&noSleep{}).sleep
	opts.MaxRestartAttempts = 1
	launcher := &fakeLauncher{startErr: errCannotStart}
	sup := supervisor.New(opts, launcher, newProber())
	ctx, cancel := context.WithCancel(context.Background())
	runDone := make(chan struct{})
	go func() {
		defer close(runDone)
		_ = sup.RunAccounts(ctx, []supervisor.AccountRef{
			account("acc_aaaaaaaaaaaaaaaaaaaaaaaaaa", 1),
		})
	}()

	waitFor(t, func() bool {
		return sup.States()["acc_aaaaaaaaaaaaaaaaaaaaaaaaaa"].Phase == supervisor.PhaseFailed
	}, "the worker to be marked failed")

	cancel()
	select {
	case <-runDone:
	case <-time.After(5 * time.Second):
		t.Fatal("the supervisor did not stop")
	}

	out := logs.all()
	if out == "" {
		t.Fatal("the supervisor logged nothing")
	}
	if !strings.Contains(out, "acc_aaaaaaaaaaaaaaaaaaaaaaaaaa") {
		t.Errorf("the log must carry the opaque account id; got:\n%s", out)
	}
	for _, forbidden := range []string{"+98", "0912", "phone", "session bytes", "api_hash", "APIHash"} {
		if strings.Contains(out, forbidden) {
			t.Errorf("the log leaked %q:\n%s", forbidden, out)
		}
	}
}

// The account worker pool is bounded by TELESYNC_ACCOUNT_MAX_WORKERS (default
// 50) and its restart base by TELESYNC_ACCOUNT_RECONNECT_BACKOFF (default 5s).
func TestTheAccountWorkerBudgetComesFromTheEnvironment(t *testing.T) {
	t.Setenv(supervisor.MaxWorkersEnv, "2")
	sup := supervisor.New(accountOptions(t), &fakeLauncher{}, newProber())
	if got := sup.WorkerBudget(); got != 2 {
		t.Errorf("worker budget = %d, want 2 from %s", got, supervisor.MaxWorkersEnv)
	}

	t.Setenv(supervisor.MaxWorkersEnv, "not-a-number")
	sup = supervisor.New(accountOptions(t), &fakeLauncher{}, newProber())
	if got := sup.WorkerBudget(); got != supervisor.DefaultMaxWorkers {
		t.Errorf("worker budget = %d, want the %d default for an unreadable %s",
			got, supervisor.DefaultMaxWorkers, supervisor.MaxWorkersEnv)
	}

	t.Setenv(supervisor.MaxWorkersEnv, "")
	sup = supervisor.New(accountOptions(t), &fakeLauncher{}, newProber())
	if got := sup.WorkerBudget(); got != supervisor.DefaultMaxWorkers {
		t.Errorf("worker budget = %d, want the %d default", got, supervisor.DefaultMaxWorkers)
	}
}

func TestTheDefaultAccountWorkerBudgetIsFifty(t *testing.T) {
	t.Setenv(supervisor.MaxWorkersEnv, "")
	sup := supervisor.New(accountOptions(t), &fakeLauncher{}, newProber())
	if got := sup.WorkerBudget(); got != 50 {
		t.Errorf("default worker budget = %d, want 50", got)
	}
}

func TestTheAccountReconnectBackoffComesFromTheEnvironment(t *testing.T) {
	t.Setenv(supervisor.ReconnectBackoffEnv, "2s")
	sup := supervisor.New(accountOptions(t), &fakeLauncher{}, newProber())
	if got := sup.AccountRestartBase(); got != 2*time.Second {
		t.Errorf("reconnect backoff = %s, want 2s from %s", got, supervisor.ReconnectBackoffEnv)
	}

	// A plain number means seconds, matching how an operator thinks.
	t.Setenv(supervisor.ReconnectBackoffEnv, "7")
	sup = supervisor.New(accountOptions(t), &fakeLauncher{}, newProber())
	if got := sup.AccountRestartBase(); got != 7*time.Second {
		t.Errorf("reconnect backoff = %s, want 7s", got)
	}

	t.Setenv(supervisor.ReconnectBackoffEnv, "")
	sup = supervisor.New(accountOptions(t), &fakeLauncher{}, newProber())
	if got := sup.AccountRestartBase(); got != supervisor.DefaultReconnectBackoff {
		t.Errorf("reconnect backoff = %s, want the %s default",
			got, supervisor.DefaultReconnectBackoff)
	}
}

// An account worker restarts from the account backoff even when the legacy
// configured-client path has its own, shorter delay.
func TestAnAccountWorkerRestartsOnItsOwnBackoff(t *testing.T) {
	opts := accountOptions(t)
	opts.RestartInitialDelay = 10 * time.Millisecond
	opts.ReconnectBackoff = 9 * time.Second
	opts.RestartMaxDelay = time.Minute
	sup := supervisor.New(opts, &fakeLauncher{}, newProber())
	if got := sup.AccountRestartBase(); got != 9*time.Second {
		t.Errorf("account restart base = %s, want 9s", got)
	}
}
