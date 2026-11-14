package channelstore_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/mirza/telesync/internal/channelstore"
)

func newAttempt(owner int64, id string) channelstore.LoginAttempt {
	return channelstore.LoginAttempt{
		ID:          id,
		OwnerUserID: owner,
		ServerKey:   "official",
		Method:      channelstore.LoginMethodPhone,
		State:       channelstore.LoginStateCreated,
		PhoneMasked: "+98 *** **12",
		NextStep:    "submit_code",
		ExpiresAt:   time.Date(2026, 5, 1, 12, 10, 0, 0, time.UTC),
		CreatedAt:   time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC),
		UpdatedAt:   time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC),
	}
}

func mustAttempt(t *testing.T, attempts channelstore.LoginAttemptStore, a channelstore.LoginAttempt) channelstore.LoginAttempt {
	t.Helper()
	saved, err := attempts.Create(context.Background(), a)
	if err != nil {
		t.Fatalf("Create(%s): %v", a.ID, err)
	}
	return saved
}

func TestLoginAttemptLifecycle(t *testing.T) {
	ctx := context.Background()
	store := open(t)
	attempts := store.LoginAttempts()
	owner := mustUser(t, store, "owner", true)

	// An authorized attempt names the account that now exists, so the one its
	// login created is recorded first.
	mustAccount(t, store, newAccount(owner.ID, accountID("a"), "official", 1))

	a := mustAttempt(t, attempts, newAttempt(owner.ID, "lga_00000000000000000000000001"))
	if a.State != channelstore.LoginStateCreated {
		t.Fatalf("state after Create = %q, want created", a.State)
	}
	if a.ID != "lga_00000000000000000000000001" || a.OwnerUserID != owner.ID {
		t.Fatalf("identity = (%q, %d)", a.ID, a.OwnerUserID)
	}

	// The whole attempt is persisted as it now stands, so a restart resumes
	// where the login stopped instead of starting over.
	steps := []struct {
		state string
		next  string
	}{
		{channelstore.LoginStateConnecting, "wait_for_code"},
		{channelstore.LoginStateWaitingForCode, "submit_code"},
		{channelstore.LoginStateWaitingForPassword, "submit_password"},
		{channelstore.LoginStateAuthorized, ""},
	}
	current := a
	for _, step := range steps {
		current.State = step.state
		current.NextStep = step.next
		if step.state == channelstore.LoginStateAuthorized {
			current.AccountID = accountID("a")
		}
		if err := attempts.UpdateState(ctx, owner.ID, current); err != nil {
			t.Fatalf("UpdateState(%s): %v", step.state, err)
		}
		got, err := attempts.ByID(ctx, owner.ID, a.ID)
		if err != nil {
			t.Fatal(err)
		}
		if got.State != step.state || got.NextStep != step.next {
			t.Errorf("after %s: state = %q, next = %q", step.state, got.State, got.NextStep)
		}
		if got.PhoneMasked != "+98 *** **12" || got.Method != channelstore.LoginMethodPhone {
			t.Errorf("the update lost the attempt's own fields: %+v", got)
		}
		current = got
	}
	if current.AccountID != accountID("a") {
		t.Errorf("account_id = %q, want %q", current.AccountID, accountID("a"))
	}
	if !current.ExpiresAt.Equal(newAttempt(owner.ID, "x").ExpiresAt) {
		t.Errorf("expires_at = %v, want the time the attempt was created with", current.ExpiresAt)
	}
}

func TestLoginAttemptByIDIsScopedToItsOwner(t *testing.T) {
	// Another user may not inspect, complete, cancel or hijack an attempt, so
	// a foreign attempt is "not found" rather than "forbidden".
	ctx := context.Background()
	store := open(t)
	attempts := store.LoginAttempts()
	owner := mustUser(t, store, "owner", true)
	stranger := mustUser(t, store, "stranger", true)
	mustAttempt(t, attempts, newAttempt(owner.ID, "lga_00000000000000000000000001"))

	if _, err := attempts.ByID(ctx, stranger.ID, "lga_00000000000000000000000001"); !errors.Is(err, channelstore.ErrLoginAttemptNotFound) {
		t.Fatalf("ByID by another owner = %v, want ErrLoginAttemptNotFound", err)
	}
	if _, err := attempts.ByID(ctx, owner.ID, "lga_00000000000000000000000001"); err != nil {
		t.Fatalf("ByID by its owner: %v", err)
	}

	a := newAttempt(owner.ID, "lga_00000000000000000000000001")
	a.State = channelstore.LoginStateCanceled
	if err := attempts.UpdateState(ctx, stranger.ID, a); !errors.Is(err, channelstore.ErrLoginAttemptNotFound) {
		t.Fatalf("UpdateState by another owner = %v, want ErrLoginAttemptNotFound", err)
	}
	if err := attempts.TouchResend(ctx, stranger.ID, a.ID, time.Now()); !errors.Is(err, channelstore.ErrLoginAttemptNotFound) {
		t.Fatalf("TouchResend by another owner = %v, want ErrLoginAttemptNotFound", err)
	}
	kept, err := attempts.ByID(ctx, owner.ID, a.ID)
	if err != nil {
		t.Fatal(err)
	}
	if kept.State != channelstore.LoginStateCreated {
		t.Errorf("another owner changed the attempt's state to %q", kept.State)
	}
}

func TestLoginAttemptRefusesAnUnknownStateMethodOrAnUnusableRecord(t *testing.T) {
	ctx := context.Background()
	store := open(t)
	attempts := store.LoginAttempts()
	owner := mustUser(t, store, "owner", true)

	for name, mutate := range map[string]func(*channelstore.LoginAttempt){
		"no id":      func(a *channelstore.LoginAttempt) { a.ID = "" },
		"no owner":   func(a *channelstore.LoginAttempt) { a.OwnerUserID = 0 },
		"no server":  func(a *channelstore.LoginAttempt) { a.ServerKey = "  " },
		"no method":  func(a *channelstore.LoginAttempt) { a.Method = "" },
		"bad method": func(a *channelstore.LoginAttempt) { a.Method = "sms" },
		"bad state":  func(a *channelstore.LoginAttempt) { a.State = "waiting_for_lunch" },
	} {
		t.Run(name, func(t *testing.T) {
			a := newAttempt(owner.ID, "lga_00000000000000000000000001")
			mutate(&a)
			if _, err := attempts.Create(ctx, a); err == nil {
				t.Fatal("Create accepted a record the schema would refuse")
			}
		})
	}

	// The state values the schema allows are exactly the state machine's.
	for _, state := range []string{
		channelstore.LoginStateCreated, channelstore.LoginStateConnecting,
		channelstore.LoginStateWaitingForCode, channelstore.LoginStateWaitingForPassword,
		channelstore.LoginStateWaitingForQR, channelstore.LoginStateAuthorized,
		channelstore.LoginStateFailed, channelstore.LoginStateExpired,
		channelstore.LoginStateCanceled,
	} {
		a := newAttempt(owner.ID, "lga_s_"+state)
		a.State = state
		mustAttempt(t, attempts, a)
	}

	if err := attempts.UpdateState(ctx, owner.ID, channelstore.LoginAttempt{
		ID: "lga_s_" + channelstore.LoginStateCreated, OwnerUserID: owner.ID, State: "napping",
	}); err == nil {
		t.Error("UpdateState accepted a state the schema does not define")
	}
}

func TestLoginAttemptCreateRefusesAReusedID(t *testing.T) {
	ctx := context.Background()
	store := open(t)
	attempts := store.LoginAttempts()
	owner := mustUser(t, store, "owner", true)
	other := mustUser(t, store, "other", true)

	mustAttempt(t, attempts, newAttempt(owner.ID, "lga_00000000000000000000000001"))
	// An id is server-generated and never reused, so reusing one is a bug
	// rather than a user error — even in another owner's name.
	if _, err := attempts.Create(ctx, newAttempt(other.ID, "lga_00000000000000000000000001")); !errors.Is(err, channelstore.ErrLoginAttemptExists) {
		t.Fatalf("Create with a reused id = %v, want ErrLoginAttemptExists", err)
	}
}

func TestLoginAttemptExpiryCleanup(t *testing.T) {
	ctx := context.Background()
	store := open(t)
	attempts := store.LoginAttempts()
	owner := mustUser(t, store, "owner", true)
	other := mustUser(t, store, "other", true)

	expired := newAttempt(owner.ID, "lga_expired000000000000000001")
	expired.ExpiresAt = time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC)
	mustAttempt(t, attempts, expired)

	live := newAttempt(owner.ID, "lga_live0000000000000000000001")
	live.ExpiresAt = time.Date(2026, 5, 1, 13, 0, 0, 0, time.UTC)
	mustAttempt(t, attempts, live)

	otherOwner := newAttempt(other.ID, "lga_other000000000000000000001")
	otherOwner.ExpiresAt = time.Date(2026, 5, 1, 11, 0, 0, 0, time.UTC)
	mustAttempt(t, attempts, otherOwner)

	sweep := time.Date(2026, 5, 1, 12, 30, 0, 0, time.UTC)
	removed, err := attempts.DeleteExpired(ctx, sweep)
	if err != nil {
		t.Fatalf("DeleteExpired: %v", err)
	}
	if removed != 2 {
		t.Fatalf("DeleteExpired removed %d attempts, want 2", removed)
	}
	if _, err := attempts.ByID(ctx, owner.ID, live.ID); err != nil {
		t.Errorf("a live attempt was swept away: %v", err)
	}
	if _, err := attempts.ByID(ctx, owner.ID, expired.ID); !errors.Is(err, channelstore.ErrLoginAttemptNotFound) {
		t.Errorf("an expired attempt survived the sweep: %v", err)
	}

	// An attempt with no expiry is not swept: it is not "expired", it is
	// unbounded.
	noExpiry := newAttempt(owner.ID, "lga_noexpiry0000000000000001")
	noExpiry.ExpiresAt = time.Time{}
	mustAttempt(t, attempts, noExpiry)
	if removed, err = attempts.DeleteExpired(ctx, sweep.Add(24*time.Hour)); err != nil || removed != 1 {
		t.Fatalf("DeleteExpired = (%d, %v), want only the live attempt removed", removed, err)
	}
	if _, err := attempts.ByID(ctx, owner.ID, noExpiry.ID); err != nil {
		t.Errorf("an attempt with no expiry was swept away: %v", err)
	}
}

func TestLoginAttemptDeleteByOwner(t *testing.T) {
	ctx := context.Background()
	store := open(t)
	attempts := store.LoginAttempts()
	owner := mustUser(t, store, "owner", true)
	other := mustUser(t, store, "other", true)

	mustAttempt(t, attempts, newAttempt(owner.ID, "lga_00000000000000000000000001"))
	mustAttempt(t, attempts, newAttempt(owner.ID, "lga_00000000000000000000000002"))
	mustAttempt(t, attempts, newAttempt(other.ID, "lga_00000000000000000000000003"))

	removed, err := attempts.DeleteByOwner(ctx, owner.ID)
	if err != nil {
		t.Fatalf("DeleteByOwner: %v", err)
	}
	if removed != 2 {
		t.Fatalf("DeleteByOwner removed %d attempts, want 2", removed)
	}
	if _, err := attempts.ByID(ctx, other.ID, "lga_00000000000000000000000003"); err != nil {
		t.Errorf("another owner's attempt was deleted too: %v", err)
	}
	if list, err := attempts.DeleteByOwner(ctx, owner.ID); err != nil || list != 0 {
		t.Fatalf("DeleteByOwner again = (%d, %v), want 0", list, err)
	}
}

func TestLoginAttemptTouchResend(t *testing.T) {
	ctx := context.Background()
	store := open(t)
	attempts := store.LoginAttempts()
	owner := mustUser(t, store, "owner", true)
	a := mustAttempt(t, attempts, newAttempt(owner.ID, "lga_00000000000000000000000001"))

	if got, err := attempts.ByID(ctx, owner.ID, a.ID); err != nil || !got.ResendAfter.IsZero() {
		t.Fatalf("resend_after = (%v, %v), want zero", got.ResendAfter, err)
	}

	at := time.Date(2026, 5, 1, 12, 2, 0, 0, time.UTC)
	if err := attempts.TouchResend(ctx, owner.ID, a.ID, at); err != nil {
		t.Fatalf("TouchResend: %v", err)
	}
	got, err := attempts.ByID(ctx, owner.ID, a.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !got.ResendAfter.Equal(at) {
		t.Errorf("resend_after = %v, want %v", got.ResendAfter, at)
	}
	// Touching the resend timer is not a state change.
	if got.State != channelstore.LoginStateCreated {
		t.Errorf("state = %q, want created", got.State)
	}
}

func TestLoginAttemptKeepsTheDeliveryTheServerActuallyUsed(t *testing.T) {
	ctx := context.Background()
	store := open(t)
	attempts := store.LoginAttempts()
	owner := mustUser(t, store, "owner", true)

	mustAccount(t, store, newAccount(owner.ID, accountID("a"), "official", 1))

	a := newAttempt(owner.ID, "lga_qr000000000000000000000001")
	a.Method = channelstore.LoginMethodQR
	a.State = channelstore.LoginStateWaitingForQR
	a.NetworkType = channelstore.NetworkCustom
	// The delivery is what the server said it did, never what the code assumed.
	a.Delivery = "email"
	a.DeliveryHint = "check the address on your account"
	a.CodeLength = 5
	a.QRToken = "short-lived-login-token"
	a.QRExpiresAt = time.Date(2026, 5, 1, 12, 5, 0, 0, time.UTC)
	a.CodeSubmissions = 2
	a.AuthorizedAt = time.Time{}

	saved := mustAttempt(t, attempts, a)
	got, err := attempts.ByID(ctx, owner.ID, a.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Delivery != "email" || got.DeliveryHint != "check the address on your account" {
		t.Errorf("delivery = (%q, %q)", got.Delivery, got.DeliveryHint)
	}
	if got.NetworkType != channelstore.NetworkCustom || got.CodeLength != 5 || got.CodeSubmissions != 2 {
		t.Errorf("record = (%q, %d, %d)", got.NetworkType, got.CodeLength, got.CodeSubmissions)
	}
	if got.QRToken != "short-lived-login-token" || !got.QRExpiresAt.Equal(a.QRExpiresAt) {
		t.Errorf("qr = (%q, %v)", got.QRToken, got.QRExpiresAt)
	}

	// The rate limit counts, so it survives a round trip and is never negative.
	current := got
	current.CodeSubmissions = 3
	current.State = channelstore.LoginStateAuthorized
	current.AccountID = accountID("a")
	current.AuthorizedAt = time.Date(2026, 5, 1, 12, 6, 0, 0, time.UTC)
	if err := attempts.UpdateState(ctx, owner.ID, current); err != nil {
		t.Fatal(err)
	}
	after, err := attempts.ByID(ctx, owner.ID, a.ID)
	if err != nil {
		t.Fatal(err)
	}
	if after.CodeSubmissions != 3 || !after.AuthorizedAt.Equal(current.AuthorizedAt) {
		t.Errorf("after the update: submissions = %d, authorized_at = %v", after.CodeSubmissions, after.AuthorizedAt)
	}
	if after.AccountID != accountID("a") {
		t.Errorf("account_id = %q", after.AccountID)
	}
	_ = saved
}

func TestLoginAttemptIncompleteFindsOnlyWhatAHasToCleanUp(t *testing.T) {
	ctx := context.Background()
	store := open(t)
	attempts := store.LoginAttempts()
	owner := mustUser(t, store, "owner", true)

	sweep := time.Date(2026, 5, 1, 12, 30, 0, 0, time.UTC)

	// A login that stopped halfway and has expired: the one a restart has to
	// clean up so no permanent session is left behind.
	stopped := newAttempt(owner.ID, "lga_stopped00000000000000001")
	stopped.State = channelstore.LoginStateWaitingForCode
	stopped.ExpiresAt = time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC)
	mustAttempt(t, attempts, stopped)

	// A finished login is a record, not an orphan.
	done := newAttempt(owner.ID, "lga_done00000000000000000001")
	done.State = channelstore.LoginStateAuthorized
	done.ExpiresAt = time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC)
	mustAttempt(t, attempts, done)

	// A login that stopped but has not expired yet is still running.
	running := newAttempt(owner.ID, "lga_running00000000000000001")
	running.State = channelstore.LoginStateConnecting
	running.ExpiresAt = time.Date(2026, 5, 1, 13, 0, 0, 0, time.UTC)
	mustAttempt(t, attempts, running)

	incomplete, err := attempts.Incomplete(ctx, sweep)
	if err != nil {
		t.Fatalf("Incomplete: %v", err)
	}
	if len(incomplete) != 1 || incomplete[0].ID != stopped.ID {
		t.Fatalf("Incomplete = %d attempts, want just %s", len(incomplete), stopped.ID)
	}
}
