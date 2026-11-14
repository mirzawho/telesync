package worker_test

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mirza/telesync/internal/rpc"
	"github.com/mirza/telesync/internal/tgapi"
	"github.com/mirza/telesync/internal/worker"
)

// A worker is one account. It carries the account's identity from the verified
// account record, and nothing a request body says can change which account it
// acts as.

const (
	accountA = "acc_aaaaaaaaaaaaaaaaaaaaaaaaaa"
	accountB = "acc_bbbbbbbbbbbbbbbbbbbbbbbbbb"
)

func identity(id string, owner int64) worker.AccountIdentity {
	return worker.AccountIdentity{
		ID:           id,
		OwnerUserID:  owner,
		ServerKey:    "official",
		NetworkType:  "telegram",
		RemoteUserID: 1000 + owner,
		SessionRef:   filepath.Join("/sessions", id+".box"),
	}
}

func accountService(t *testing.T, id worker.AccountIdentity, tg *fakeTelegram) *worker.Service {
	t.Helper()
	s, err := worker.NewServiceForAccount(id, tg, worker.Options{
		Now: func() time.Time { return startedAt().Add(90 * time.Second) },
		PID: 4242,
	})
	if err != nil {
		t.Fatalf("NewServiceForAccount: %v", err)
	}
	return s
}

func TestAWorkerCarriesItsOwnAccountIdentity(t *testing.T) {
	s := accountService(t, identity(accountA, 7), &fakeTelegram{status: tgapi.Status{
		State: tgapi.StateReady, ThisDC: 2,
		User: &tgapi.Account{ID: 1007},
	}})

	got := s.Account()
	if got.ID != accountA {
		t.Errorf("account id = %q, want %q", got.ID, accountA)
	}
	if got.OwnerUserID != 7 {
		t.Errorf("owner = %d, want 7", got.OwnerUserID)
	}
	if got.RemoteUserID != 1007 {
		t.Errorf("remote user = %d, want the verified account record's 1007", got.RemoteUserID)
	}
	if got.ScopeKey() != accountA {
		t.Errorf("ScopeKey() = %q, want the account id", got.ScopeKey())
	}
}

func TestHealthAndProfileReportTheAccountNotTheClientKey(t *testing.T) {
	s := accountService(t, identity(accountA, 7), &fakeTelegram{status: tgapi.Status{
		State: tgapi.StateReady, ThisDC: 2,
		User: &tgapi.Account{ID: 1007, FirstName: "Ada"},
	}})

	h := s.Health(context.Background())
	if h.AccountID != accountA {
		t.Errorf("health account id = %q, want %q", h.AccountID, accountA)
	}
	if h.OwnerUserID != 7 {
		t.Errorf("health owner = %d, want 7", h.OwnerUserID)
	}

	p, err := s.Profile(context.Background())
	if err != nil {
		t.Fatalf("Profile: %v", err)
	}
	if p.AccountID != accountA {
		t.Errorf("profile account id = %q, want %q", p.AccountID, accountA)
	}
}

func TestAccountIdentitySurvivesTheWorkerRPCBoundary(t *testing.T) {
	for _, tc := range []struct {
		id    string
		owner int64
	}{
		{id: accountA, owner: 7},
		{id: accountB, owner: 8},
	} {
		t.Run(tc.id, func(t *testing.T) {
			s := accountService(t, identity(tc.id, tc.owner), &fakeTelegram{status: tgapi.Status{
				State: tgapi.StateReady, ThisDC: 2,
				User: &tgapi.Account{ID: 1000 + tc.owner},
			}})
			server := httptest.NewServer(rpc.NewHandler(s))
			t.Cleanup(server.Close)
			client := rpc.NewClientWithDialer(func(ctx context.Context, _, _ string) (net.Conn, error) {
				var d net.Dialer
				return d.DialContext(ctx, "tcp", server.Listener.Addr().String())
			}, time.Second)
			t.Cleanup(client.Close)

			health, err := client.Health(context.Background())
			if err != nil {
				t.Fatalf("Health: %v", err)
			}
			profile, err := client.Profile(context.Background())
			if err != nil {
				t.Fatalf("Profile: %v", err)
			}
			if health.AccountID != tc.id || health.OwnerUserID != tc.owner {
				t.Errorf("Health identity = %q/%d, want %q/%d",
					health.AccountID, health.OwnerUserID, tc.id, tc.owner)
			}
			if profile.AccountID != tc.id || profile.OwnerUserID != tc.owner {
				t.Errorf("Profile identity = %q/%d, want %q/%d",
					profile.AccountID, profile.OwnerUserID, tc.id, tc.owner)
			}
		})
	}
}

// The authenticated remote user is taken from the verified account record. A
// request that claims a different one is not believed.
func TestTheAuthenticatedRemoteUserComesFromTheAccountRecord(t *testing.T) {
	tg := &fakeTelegram{status: tgapi.Status{
		State: tgapi.StateReady, ThisDC: 2,
		// The transport reports a user; the account record is authoritative.
		User: &tgapi.Account{ID: 999},
	}}
	s := accountService(t, identity(accountA, 7), tg)

	if got := s.Account().RemoteUserID; got != 1007 {
		t.Errorf("remote user = %d, want the account record's 1007, not the transport's 999", got)
	}
}

// Two accounts are two workers with two independent caches. Neither can see the
// other's pagination cursor or dialog snapshot.
func TestTwoAccountsKeepSeparatePeerCachesAndSnapshots(t *testing.T) {
	tgA := &fakeTelegram{
		status:  tgapi.Status{State: tgapi.StateReady, User: &tgapi.Account{ID: 1001}},
		dialogs: []tgapi.Channel{{ID: 10, Title: "A's channel"}},
		hist:    tgapi.HistoryPage{Messages: []tgapi.Message{{ID: 90}}},
	}
	tgB := &fakeTelegram{
		status:  tgapi.Status{State: tgapi.StateReady, User: &tgapi.Account{ID: 1002}},
		dialogs: []tgapi.Channel{{ID: 20, Title: "B's channel"}},
		hist:    tgapi.HistoryPage{Messages: []tgapi.Message{{ID: 40}}},
	}
	sA := accountService(t, identity(accountA, 1), tgA)
	sB := accountService(t, identity(accountB, 2), tgB)

	if _, err := sA.Messages(context.Background(), 10, 2, 1); err != nil {
		t.Fatalf("A Messages: %v", err)
	}
	if _, err := sB.Messages(context.Background(), 20, 2, 1); err != nil {
		t.Fatalf("B Messages: %v", err)
	}

	if sA.CachedChannels() != 1 || sB.CachedChannels() != 1 {
		t.Fatalf("cached channels A=%d B=%d, want one each in their own scope",
			sA.CachedChannels(), sB.CachedChannels())
	}

	// A's cursor is its own; B must not be able to page below it.
	if _, err := sA.Messages(context.Background(), 10, 2, 1); err != nil {
		t.Fatalf("A Messages page 2: %v", err)
	}
	if calls := tgA.historyCalls; len(calls) == 0 || calls[len(calls)-1][0] != 90 {
		t.Errorf("A continued below its own cursor 90, got %v", calls)
	}
	for _, c := range tgB.historyCalls {
		if c[0] == 90 {
			t.Fatal("B paged below A's cursor: the peer cache leaked across accounts")
		}
	}
}

// Channel resolution is account-scoped: a channel one account can see is not
// resolvable through another account's worker.
func TestChannelResolutionIsAccountScoped(t *testing.T) {
	tgA := &fakeTelegram{
		status:  tgapi.Status{State: tgapi.StateReady, User: &tgapi.Account{ID: 1001}},
		dialogs: []tgapi.Channel{{ID: 10, Title: "only A"}},
	}
	tgB := &fakeTelegram{
		status:  tgapi.Status{State: tgapi.StateReady, User: &tgapi.Account{ID: 1002}},
		dialogs: []tgapi.Channel{{ID: 20, Title: "only B"}},
	}
	sA := accountService(t, identity(accountA, 1), tgA)
	sB := accountService(t, identity(accountB, 2), tgB)

	if _, err := sA.Messages(context.Background(), 10, 1, 10); err != nil {
		t.Fatalf("A must resolve its own channel: %v", err)
	}
	if _, err := sB.Messages(context.Background(), 10, 1, 10); err == nil {
		t.Fatal("B resolved a channel that only A can see")
	}
}

// The session is the account's. A worker is built for one account and its
// session reference must name that account and nobody else.
func TestAWorkerRefusesASessionBelongingToAnotherAccount(t *testing.T) {
	other := identity(accountA, 7)
	other.SessionRef = filepath.Join("/sessions", accountB+".box")

	if _, err := worker.NewServiceForAccount(other, &fakeTelegram{}, worker.Options{}); err == nil {
		t.Fatal("a worker was built with another account's session")
	}
}

func TestAWorkerRefusesAnIncompleteAccountIdentity(t *testing.T) {
	for name, mutate := range map[string]func(*worker.AccountIdentity){
		"no id":           func(a *worker.AccountIdentity) { a.ID = "" },
		"no owner":        func(a *worker.AccountIdentity) { a.OwnerUserID = 0 },
		"no server":       func(a *worker.AccountIdentity) { a.ServerKey = "" },
		"no session":      func(a *worker.AccountIdentity) { a.SessionRef = "" },
		"no remote user":  func(a *worker.AccountIdentity) { a.RemoteUserID = 0 },
		"unknown network": func(a *worker.AccountIdentity) { a.NetworkType = "telepathy" },
		"non-canonical account id": func(a *worker.AccountIdentity) {
			a.ID = "acc_" + strings.Repeat("a", 25)
		},
		"confusable account id": func(a *worker.AccountIdentity) {
			a.ID = "acc_" + strings.Repeat("a", 25) + "i"
		},
		"relative session path": func(a *worker.AccountIdentity) {
			a.SessionRef = filepath.Join("sessions", a.ID+".box")
		},
		"session path traversal": func(a *worker.AccountIdentity) {
			a.SessionRef = "/sessions/../other/" + a.ID + ".box"
		},
	} {
		id := identity(accountA, 7)
		mutate(&id)
		if _, err := worker.NewServiceForAccount(id, &fakeTelegram{}, worker.Options{}); err == nil {
			t.Errorf("%s: a worker was built from an incomplete account identity", name)
		}
	}
}

func TestAccountWorkerFailsClosedWhenTheSessionBelongsToAnotherRemoteUser(t *testing.T) {
	tg := &fakeTelegram{
		status: tgapi.Status{
			State: tgapi.StateReady,
			User:  &tgapi.Account{ID: 999},
		},
		dialogs: []tgapi.Channel{{ID: 42, Title: "must not be read"}},
	}
	s := accountService(t, identity(accountA, 7), tg)

	health := s.Health(context.Background())
	if health.Authorized || health.State != rpc.StateError || health.Transport != "ready" {
		t.Errorf("health = %+v, want an identity error on a ready transport", health)
	}
	if health.LastError == "" {
		t.Error("health must explain that the session identity failed validation")
	}

	profile, err := s.Profile(context.Background())
	if err != nil {
		t.Fatalf("Profile: %v", err)
	}
	if profile.Authorized || profile.AuthorizationState != rpc.AuthNeedsAuth || profile.User != nil {
		t.Errorf("profile disclosed a different authenticated user: %+v", profile)
	}

	if _, err := s.Channels(context.Background(), 1, 10); !errors.Is(err, rpc.ErrNeedsAuth) {
		t.Errorf("Channels error = %v, want fail-closed needs-auth", err)
	}
	if tg.dialogCalls != 0 {
		t.Errorf("Telegram dialogs were queried %d times after account identity mismatch", tg.dialogCalls)
	}
}

// Health reports the account's own session and never another's. Stat, not read:
// a health poll must not open the credential file.
func TestHealthReportsOnlyItsOwnSession(t *testing.T) {
	dir := t.TempDir()
	mine := filepath.Join(dir, accountA+".box")
	theirs := filepath.Join(dir, accountB+".box")
	for _, p := range []string{mine, theirs} {
		if err := os.WriteFile(p, []byte("sealed"), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	id := identity(accountA, 7)
	id.SessionRef = mine
	s, err := worker.NewServiceForAccount(id, &fakeTelegram{status: tgapi.Status{
		State: tgapi.StateReady, User: &tgapi.Account{ID: 1007},
	}}, worker.Options{Now: time.Now, PID: 1})
	if err != nil {
		t.Fatalf("NewServiceForAccount: %v", err)
	}

	if h := s.Health(context.Background()); !h.SessionPresent {
		t.Error("the worker must report its own sealed session as present")
	}

	idB := identity(accountB, 8)
	idB.SessionRef = theirs
	os.Remove(theirs)
	sB, err := worker.NewServiceForAccount(idB, &fakeTelegram{status: tgapi.Status{
		State: tgapi.StateReady, User: &tgapi.Account{ID: 1008},
	}}, worker.Options{Now: time.Now, PID: 2})
	if err != nil {
		t.Fatalf("NewServiceForAccount: %v", err)
	}
	if h := sB.Health(context.Background()); h.SessionPresent {
		t.Error("a worker reported a session it does not own")
	}
}

// Worker logs identify the account by its opaque id and never render the
// account's phone number, username or sealed session reference. Those are not
// identities and they are not safe to print.
func TestAnAccountIdentityNeverRendersItsSecrets(t *testing.T) {
	id := identity(accountA, 7)
	id.PhoneMasked = "+98 *** **77"
	id.RemoteUsername = "secret_handle"

	safe := id.LogSafe()
	if safe != accountA {
		t.Errorf("LogSafe() = %q, want the opaque account id", safe)
	}
	for _, forbidden := range []string{"+98", "77", "secret_handle", "sessions", ".box"} {
		if strings.Contains(safe, forbidden) {
			t.Errorf("LogSafe() leaked %q: %q", forbidden, safe)
		}
	}
}

func TestWorkerLogsCarryTheOpaqueAccountID(t *testing.T) {
	var lines []string
	id := identity(accountA, 7)
	id.PhoneMasked = "+98 *** **77"
	id.RemoteUsername = "secret_handle"

	tg := &fakeTelegram{status: tgapi.Status{State: tgapi.StateReady, User: &tgapi.Account{ID: 1007}}}
	s, err := worker.NewServiceForAccount(id, tg, worker.Options{Now: time.Now, PID: 1},
		worker.WithLogf(func(format string, args ...any) {
			lines = append(lines, fmt.Sprintf(format, args...))
		}))
	if err != nil {
		t.Fatalf("NewServiceForAccount: %v", err)
	}

	tg.findErr = errors.New("dial failed")
	if _, err := s.Find(context.Background(), rpc.FindRequest{Kind: rpc.FindByID, ChannelID: 1}); err == nil {
		t.Fatal("expected the underlying failure")
	}

	out := strings.Join(lines, "\n")
	if !strings.Contains(out, accountA) {
		t.Errorf("the worker log must carry the opaque account id; got:\n%s", out)
	}
	for _, forbidden := range []string{"+98", "secret_handle"} {
		if strings.Contains(out, forbidden) {
			t.Errorf("the worker log leaked %q:\n%s", forbidden, out)
		}
	}
}

// Media ids stay opaque: they are written per account, handed back verbatim and
// never decoded into an account or a channel.
func TestMediaIDsRemainOpaqueAndCarryNoAccountIdentity(t *testing.T) {
	tg := &fakeTelegram{
		status:  tgapi.Status{State: tgapi.StateReady, User: &tgapi.Account{ID: 1007}},
		dialogs: []tgapi.Channel{{ID: 10}},
		hist: tgapi.HistoryPage{Messages: []tgapi.Message{{
			ID: 5, Media: &tgapi.Media{Type: "photo", Label: "p"},
		}}},
	}
	a := accountService(t, identity(accountA, 7), tg)
	tgB := &fakeTelegram{
		status:  tgapi.Status{State: tgapi.StateReady, User: &tgapi.Account{ID: 1008}},
		dialogs: []tgapi.Channel{{ID: 10}},
		hist: tgapi.HistoryPage{Messages: []tgapi.Message{{
			ID: 5, Media: &tgapi.Media{Type: "photo", Label: "p"},
		}}},
	}
	b := accountService(t, identity(accountB, 8), tgB)

	page, err := a.Messages(context.Background(), 10, 1, 10)
	if err != nil {
		t.Fatalf("Messages: %v", err)
	}
	if len(page.Messages) != 1 || page.Messages[0].Media == nil {
		t.Fatalf("expected one message with media: %+v", page.Messages)
	}
	got := page.Messages[0].Media.ID
	if strings.Contains(got, accountA) || strings.Contains(got, accountB) {
		t.Errorf("media id %q must not carry an account identity", got)
	}

	other, err := b.Messages(context.Background(), 10, 1, 10)
	if err != nil {
		t.Fatalf("B Messages: %v", err)
	}
	if other.Messages[0].Media.ID != got {
		t.Errorf("media id changed hands: %q vs %q", other.Messages[0].Media.ID, got)
	}
}

// A session that has expired is the account's own problem. The worker reports
// it needs authorization and never borrows another account's session to keep
// working.
func TestAnExpiredSessionStopsOnlyItsOwnAccount(t *testing.T) {
	expired := &fakeTelegram{status: tgapi.Status{
		State: tgapi.StateUnauthorized,
		Err:   "AUTH_KEY_UNREGISTERED",
	}}
	healthy := &fakeTelegram{
		status:  tgapi.Status{State: tgapi.StateReady, User: &tgapi.Account{ID: 1008}},
		dialogs: []tgapi.Channel{{ID: 20}},
	}
	sExpired := accountService(t, identity(accountA, 7), expired)
	sHealthy := accountService(t, identity(accountB, 8), healthy)

	if _, err := sExpired.Channels(context.Background(), 1, 10); !errors.Is(err, rpc.ErrNeedsAuth) {
		t.Fatalf("an expired session must report it needs authorization, got %v", err)
	}
	if _, err := sExpired.Messages(context.Background(), 10, 1, 10); !errors.Is(err, rpc.ErrNeedsAuth) {
		t.Fatalf("an expired session must not read history, got %v", err)
	}

	// The other account is untouched and still serves its own channels.
	page, err := sHealthy.Channels(context.Background(), 1, 10)
	if err != nil {
		t.Fatalf("a healthy account must keep working: %v", err)
	}
	if len(page.Channels) != 1 || page.Channels[0].ID != 20 {
		t.Fatalf("the healthy account served someone else's channels: %+v", page.Channels)
	}
	if sHealthy.Account().ID == sExpired.Account().ID {
		t.Fatal("the two workers collapsed into one account")
	}
}

// Two workers never share a session file, so a restart of one cannot pick up
// the other's credentials.
func TestTwoAccountsNeverShareASessionFile(t *testing.T) {
	a := identity(accountA, 7)
	b := identity(accountB, 8)
	if a.SessionRef == b.SessionRef {
		t.Fatalf("two accounts share the session %q", a.SessionRef)
	}
	// And a worker refuses to be built for the other's.
	a.SessionRef = b.SessionRef
	if _, err := worker.NewServiceForAccount(a, &fakeTelegram{}, worker.Options{}); err == nil {
		t.Fatal("a worker was built on another account's session")
	}
}
