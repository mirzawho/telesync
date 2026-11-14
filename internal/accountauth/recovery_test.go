package accountauth_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/mirza/telesync/internal/accountauth"
)

// A restart must leave nothing signed in that nobody finished. Whatever a
// half-way login created is released when the process comes back.

func TestRecoveryLeavesNoOrphanedSessionFromAnInterruptedLogin(t *testing.T) {
	store := accountauth.NewMemoryLoginAttemptStore()
	accounts := &fakeAccounts{}
	// The login reaches the account step but is interrupted before it can be
	// finished, so the attempt is still waiting for a code.
	mtproto := &fakeMTProto{passwordRequired: true, statusUser: authorizedUser()}

	svc, _, clock := newServiceOverWithAccounts(t, mtproto, store, accounts)

	attempt, err := svc.Start(context.Background(), owner, accountauth.StartRequest{
		ServerKey: "cat", NetworkType: "custom", Phone: "+491234567890",
	})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	connect(t, svc, owner, attempt.ID)
	if _, err := svc.SubmitCode(context.Background(), owner, attempt.ID, "12345"); err != nil {
		t.Fatalf("SubmitCode: %v", err)
	}

	// The process dies before the password is given. Nothing ran: no logout, no
	// cancel, no chance to finish the login.
	clock.Advance(time.Hour)

	restarted, _, _ := newServiceOn(t, mtproto, store, accounts, clock)
	report, err := restarted.Recover(context.Background())
	if err != nil {
		t.Fatalf("Recover: %v", err)
	}
	if report.Incomplete == 0 {
		t.Fatal("recovery found nothing to settle")
	}
	if report.AccountsReleased == 0 {
		t.Fatal("the interrupted login left an account record behind")
	}
	if len(accounts.dropped) != 1 {
		t.Fatalf("the account store was told to drop %d records, want 1", len(accounts.dropped))
	}
	if accounts.dropped[0].AccountID != attempt.AccountID {
		t.Fatalf("dropped account = %q", accounts.dropped[0].AccountID)
	}

	// The attempt is settled and cannot be driven any further: a login that
	// was interrupted does not become a session on the next boot.
	after, err := restarted.Status(context.Background(), owner, attempt.ID)
	if err != nil {
		if !errors.Is(err, accountauth.ErrAttemptExpired) && !errors.Is(err, accountauth.ErrNotFound) {
			t.Fatalf("Status after recovery error = %v", err)
		}
	} else if after.State != accountauth.StateExpired {
		t.Fatalf("state = %q, want %q", after.State, accountauth.StateExpired)
	}
	if _, err := restarted.SubmitCode(context.Background(), owner, attempt.ID, "12345"); err == nil {
		t.Fatal("a recovered attempt still accepts a code")
	}
}

func TestRecoveryNeverTouchesAnAuthorizedAttempt(t *testing.T) {
	store := accountauth.NewMemoryLoginAttemptStore()
	accounts := &fakeAccounts{nextID: "acc_aaaaaaaaaaaaaaaaaaaaaaaaaa"}
	mtproto := &fakeMTProto{statusUser: authorizedUser()}

	svc, _, _ := newServiceOverWithAccounts(t, mtproto, store, accounts)
	attempt, err := svc.Start(context.Background(), owner, accountauth.StartRequest{
		ServerKey: "cat", NetworkType: "custom", Phone: "+491234567890",
	})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	connect(t, svc, owner, attempt.ID)
	if _, err := svc.SubmitCode(context.Background(), owner, attempt.ID, "12345"); err != nil {
		t.Fatalf("SubmitCode: %v", err)
	}

	report, err := svc.Recover(context.Background())
	if err != nil {
		t.Fatalf("Recover: %v", err)
	}
	if report.AccountsReleased != 0 {
		t.Fatal("recovery dropped an account that finished logging in")
	}
	if len(accounts.dropped) != 0 {
		t.Fatalf("the account store dropped %d records of a signed-in account", len(accounts.dropped))
	}

	after, err := svc.Status(context.Background(), owner, attempt.ID)
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if after.State != accountauth.StateAuthorized {
		t.Fatalf("state = %q, want %q", after.State, accountauth.StateAuthorized)
	}
}

func TestRecoverySettlesAnAttemptThatNeverReachedACode(t *testing.T) {
	store := accountauth.NewMemoryLoginAttemptStore()
	accounts := &fakeAccounts{}
	mtproto := &fakeMTProto{}

	svc, _, clock := newServiceOverWithAccounts(t, mtproto, store, accounts)
	if _, err := svc.Start(context.Background(), owner, accountauth.StartRequest{
		ServerKey: "cat", NetworkType: "custom", Phone: "+491234567890",
	}); err != nil {
		t.Fatalf("Start: %v", err)
	}
	clock.Advance(2 * time.Hour)

	report, err := svc.Recover(context.Background())
	if err != nil {
		t.Fatalf("Recover: %v", err)
	}
	if report.Incomplete != 1 {
		t.Fatalf("incomplete = %d, want 1", report.Incomplete)
	}
	if report.AccountsReleased != 1 {
		t.Fatalf("pending account releases = %d, want 1 even though no code was sent", report.AccountsReleased)
	}
	if mtproto.cancelled == 0 {
		t.Fatal("the half-open MTProto session was left running")
	}
}

func TestRecoveryRunsAgainWithoutFindingAnythingNew(t *testing.T) {
	store := accountauth.NewMemoryLoginAttemptStore()
	accounts := &fakeAccounts{}
	mtproto := &fakeMTProto{}

	svc, _, clock := newServiceOverWithAccounts(t, mtproto, store, accounts)
	attempt, err := svc.Start(context.Background(), owner, accountauth.StartRequest{
		ServerKey: "cat", NetworkType: "custom", Phone: "+491234567890",
	})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	connect(t, svc, owner, attempt.ID)
	clock.Advance(2 * time.Hour)

	if _, err := svc.Recover(context.Background()); err != nil {
		t.Fatalf("Recover: %v", err)
	}
	second, err := svc.Recover(context.Background())
	if err != nil {
		t.Fatalf("second Recover: %v", err)
	}
	if second.Incomplete != 0 {
		t.Fatalf("the second pass settled %d attempts, want 0", second.Incomplete)
	}
}

func TestRecoveryReportsAFactNotAClaim(t *testing.T) {
	store := accountauth.NewMemoryLoginAttemptStore()
	svc, _, clock := newServiceOverWithAccounts(t, &fakeMTProto{}, store, &fakeAccounts{})

	// Nothing to settle: the report says so rather than claiming a clean
	// Telegram session.
	report, err := svc.Recover(context.Background())
	if err != nil {
		t.Fatalf("Recover: %v", err)
	}
	if report.Incomplete != 0 || report.Released != 0 || report.AccountsReleased != 0 {
		t.Fatalf("report = %+v, want no work", report)
	}
	if report.CheckedAt.IsZero() {
		t.Fatal("the report carries no time")
	}
	_ = clock
}
