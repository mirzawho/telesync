package channelstore_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/mirza/telesync/internal/accountauth"
	"github.com/mirza/telesync/internal/channelstore"
	"github.com/mirza/telesync/internal/secrets"
)

// accountID builds an account id the way the login service does: opaque, and
// chosen by the server rather than by whoever asks for the account.
func accountID(seed string) string {
	return "acc_" + strings.Repeat(seed, 26/len(seed)+1)[:26]
}

func TestPendingAccountIsStableOwnedAndOnlyCompletesAfterRemoteAuthorization(t *testing.T) {
	ctx := context.Background()
	store := open(t)
	owner := mustUser(t, store, "pending-owner", true)
	id := accountID("p")

	if err := store.CreatePending(ctx, owner.ID, id, "official", channelstore.NetworkTelegram); err != nil {
		t.Fatalf("CreatePending: %v", err)
	}
	pending, err := store.ByID(ctx, owner.ID, id)
	if err != nil {
		t.Fatalf("pending account ByID: %v", err)
	}
	if pending.ID != id || pending.Status != channelstore.AccountStatusConnecting || pending.RemoteUserID != 0 {
		t.Fatalf("pending account = %+v; want stable id, connecting status, no remote identity", pending)
	}

	user := accountauth.AuthorizedUser{
		RemoteUserID: 731, RemoteUsername: "confirmed", RemoteFirstName: "First",
		RemoteLastName: "Last", PhoneMasked: "+••••1234",
	}
	if err := store.CompletePending(ctx, owner.ID, id, user); err != nil {
		t.Fatalf("CompletePending: %v", err)
	}
	connected, err := store.ByID(ctx, owner.ID, id)
	if err != nil {
		t.Fatalf("completed account ByID: %v", err)
	}
	if connected.ID != id || connected.Status != channelstore.AccountStatusConnected ||
		connected.RemoteUserID != user.RemoteUserID || connected.RemoteUsername != user.RemoteUsername ||
		connected.RemoteDisplayName != "First Last" || connected.PhoneNumberMasked != user.PhoneMasked ||
		connected.AuthorizedAt.IsZero() {
		t.Fatalf("completed account = %+v; remote identity must be server-confirmed on the original ID", connected)
	}
	if err := store.ReleasePending(ctx, owner.ID, id); err != nil {
		t.Fatalf("ReleasePending after completion: %v", err)
	}
	if _, err := store.ByID(ctx, owner.ID, id); err != nil {
		t.Fatalf("terminal cleanup removed an authorized account: %v", err)
	}

	pendingID := accountID("q")
	if err := store.CreatePending(ctx, owner.ID, pendingID, "cat", channelstore.NetworkCustom); err != nil {
		t.Fatalf("CreatePending for cleanup: %v", err)
	}
	if err := store.ReleasePending(ctx, owner.ID, pendingID); err != nil {
		t.Fatalf("ReleasePending: %v", err)
	}
	if _, err := store.ByID(ctx, owner.ID, pendingID); !errors.Is(err, channelstore.ErrAccountNotFound) {
		t.Fatalf("released pending account ByID = %v, want not found", err)
	}
}

func mustAccount(t *testing.T, store *channelstore.Store, a channelstore.Account) channelstore.Account {
	t.Helper()
	saved, err := store.Create(context.Background(), a)
	if err != nil {
		t.Fatalf("Create(%s): %v", a.ID, err)
	}
	return saved
}

func newAccount(owner int64, id, serverKey string, remote int64) channelstore.Account {
	return channelstore.Account{
		ID:             id,
		OwnerUserID:    owner,
		ServerKey:      serverKey,
		NetworkType:    channelstore.NetworkTelegram,
		RemoteUserID:   remote,
		RemoteUsername: "example",
		Status:         channelstore.AccountStatusConnected,
	}
}

func TestAccountCreateStoresTheWholeAccount(t *testing.T) {
	ctx := context.Background()
	store := open(t)
	owner := mustUser(t, store, "owner", true)
	connected := time.Date(2026, 3, 1, 10, 0, 0, 0, time.UTC)

	box := secrets.Box{
		Version:    secrets.Version1,
		Nonce:      []byte("nonce-1234"),
		Ciphertext: []byte("sealed-session-bytes"),
	}
	in := newAccount(owner.ID, accountID("a"), "official", 424242)
	in.RemoteDisplayName = "Example Channel Owner"
	in.PhoneNumberMasked = "+98 *** **12"
	in.Session = box
	in.SessionEncKeyVersion = 3
	in.AuthorizedAt = connected.Add(-time.Hour)
	in.LastConnectedAt = connected
	in.LastDisconnectedAt = connected.Add(time.Hour)
	in.LastErrorCode = "connection_reset"

	saved := mustAccount(t, store, in)
	if saved.ID != in.ID || saved.OwnerUserID != owner.ID {
		t.Fatalf("identity = (%q, %d), want (%q, %d)", saved.ID, saved.OwnerUserID, in.ID, owner.ID)
	}

	got, err := store.ByID(ctx, owner.ID, in.ID)
	if err != nil {
		t.Fatalf("ByID: %v", err)
	}
	if got.ServerKey != "official" || got.NetworkType != channelstore.NetworkTelegram {
		t.Errorf("server identity = (%q, %q), want (official, telegram)", got.ServerKey, got.NetworkType)
	}
	if got.RemoteUserID != 424242 || got.RemoteUsername != "example" || got.RemoteDisplayName != "Example Channel Owner" {
		t.Errorf("remote identity = (%d, %q, %q)", got.RemoteUserID, got.RemoteUsername, got.RemoteDisplayName)
	}
	if got.PhoneNumberMasked != "+98 *** **12" {
		t.Errorf("phone_number_masked = %q", got.PhoneNumberMasked)
	}
	if got.Status != channelstore.AccountStatusConnected {
		t.Errorf("status = %q", got.Status)
	}
	if got.Session.Version != box.Version || string(got.Session.Nonce) != string(box.Nonce) ||
		string(got.Session.Ciphertext) != string(box.Ciphertext) {
		t.Errorf("session round-tripped as %+v, want %+v", got.Session, box)
	}
	if got.SessionEncKeyVersion != 3 {
		t.Errorf("session_enc_key_version = %d, want 3", got.SessionEncKeyVersion)
	}
	if !got.LastConnectedAt.Equal(connected) || !got.LastDisconnectedAt.Equal(connected.Add(time.Hour)) {
		t.Errorf("connection times = (%v, %v)", got.LastConnectedAt, got.LastDisconnectedAt)
	}
	if !got.AuthorizedAt.Equal(connected.Add(-time.Hour)) {
		t.Errorf("authorized_at = %v", got.AuthorizedAt)
	}
	if got.LastErrorCode != "connection_reset" {
		t.Errorf("last_error_code = %q", got.LastErrorCode)
	}
	if got.CreatedAt.IsZero() {
		t.Error("created_at was not recorded")
	}
}

func TestAccountByIDIsScopedToItsOwner(t *testing.T) {
	// Ownership is an authorization boundary, not a convenience filter: an id
	// must never confirm that another user's account exists.
	ctx := context.Background()
	store := open(t)
	owner := mustUser(t, store, "owner", true)
	stranger := mustUser(t, store, "stranger", true)
	mustAccount(t, store, newAccount(owner.ID, accountID("a"), "official", 1))

	if _, err := store.ByID(ctx, stranger.ID, accountID("a")); !errors.Is(err, channelstore.ErrAccountNotFound) {
		t.Fatalf("ByID by another owner = %v, want ErrAccountNotFound", err)
	}
	if _, err := store.ByID(ctx, owner.ID, accountID("a")); err != nil {
		t.Fatalf("ByID by its owner: %v", err)
	}
	if _, err := store.ByID(ctx, owner.ID, accountID("z")); !errors.Is(err, channelstore.ErrAccountNotFound) {
		t.Fatalf("ByID for a missing account = %v, want ErrAccountNotFound", err)
	}
}

func TestClearAccountSessionIsOwnerScopedAndLeavesNoSealedBytes(t *testing.T) {
	ctx := context.Background()
	store := open(t)
	owner := mustUser(t, store, "clear-session-owner", true)
	stranger := mustUser(t, store, "clear-session-stranger", true)
	rec := newAccount(owner.ID, accountID("c"), "official", 4242)
	rec.Session = secrets.Box{
		Version: secrets.Version1, Nonce: []byte("nonce-1234"),
		Ciphertext: []byte("sealed-account-session"),
	}
	rec.SessionEncKeyVersion = 1
	mustAccount(t, store, rec)

	if err := store.ClearSession(ctx, stranger.ID, rec.ID); !errors.Is(err, channelstore.ErrAccountNotFound) {
		t.Fatalf("ClearSession by another owner = %v, want ErrAccountNotFound", err)
	}
	if err := store.ClearSession(ctx, owner.ID, rec.ID); err != nil {
		t.Fatalf("ClearSession by owner: %v", err)
	}
	got, err := store.ByID(ctx, owner.ID, rec.ID)
	if err != nil {
		t.Fatalf("ByID after ClearSession: %v", err)
	}
	if got.Session.Version != 0 || len(got.Session.Nonce) != 0 || len(got.Session.Ciphertext) != 0 {
		t.Fatalf("sealed session remained after ClearSession: %+v", got.Session)
	}
	if got.RemoteUserID != rec.RemoteUserID || got.SessionEncKeyVersion != rec.SessionEncKeyVersion {
		t.Fatalf("clearing session changed account identity/key version: %+v", got)
	}
}

func TestAccountStoreAllowsTwoOwnersToHoldTheSameRemoteIdentity(t *testing.T) {
	ctx := context.Background()
	store := open(t)
	first := mustUser(t, store, "first", true)
	second := mustUser(t, store, "second", true)

	mustAccount(t, store, newAccount(first.ID, accountID("a"), "official", 1))
	mustAccount(t, store, newAccount(second.ID, accountID("b"), "official", 1))

	// Two separate accounts, two separate sessions: no constraint may force
	// the two owners to share one.
	a, err := store.ByID(ctx, first.ID, accountID("a"))
	if err != nil {
		t.Fatal(err)
	}
	b, err := store.ByID(ctx, second.ID, accountID("b"))
	if err != nil {
		t.Fatal(err)
	}
	if a.ID == b.ID {
		t.Fatalf("both owners resolved to account %q", a.ID)
	}
}

func TestAccountStoreRefusesTheSameRemoteIdentityTwiceForOneOwner(t *testing.T) {
	ctx := context.Background()
	store := open(t)
	owner := mustUser(t, store, "owner", true)

	mustAccount(t, store, newAccount(owner.ID, accountID("a"), "official", 1))
	dup := newAccount(owner.ID, accountID("b"), "official", 1)
	if _, err := store.Create(ctx, dup); !errors.Is(err, channelstore.ErrDuplicateAccount) {
		t.Fatalf("second account for one owner = %v, want ErrDuplicateAccount", err)
	}

	// The same remote identity on another server is a different account.
	other := newAccount(owner.ID, accountID("c"), "cat", 1)
	if _, err := store.Create(ctx, other); err != nil {
		t.Fatalf("the same remote identity on another server: %v", err)
	}
}

func TestAccountDeleteHidesTheAccountAndFreesItsRemoteIdentity(t *testing.T) {
	ctx := context.Background()
	store := open(t)
	owner := mustUser(t, store, "owner", true)
	mustAccount(t, store, newAccount(owner.ID, accountID("a"), "official", 1))

	if err := store.Delete(ctx, owner.ID, accountID("a")); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := store.ByID(ctx, owner.ID, accountID("a")); !errors.Is(err, channelstore.ErrAccountNotFound) {
		t.Fatalf("ByID after Delete = %v, want ErrAccountNotFound", err)
	}
	if list, err := store.ListByOwner(ctx, owner.ID); err != nil || len(list) != 0 {
		t.Fatalf("ListByOwner after Delete = (%v, %v), want empty", list, err)
	}
	// The owner may register the same remote identity again: the deleted
	// record must not hold it forever.
	if _, err := store.Create(ctx, newAccount(owner.ID, accountID("b"), "official", 1)); err != nil {
		t.Fatalf("re-registering a deleted account's remote identity: %v", err)
	}
	// A deletion is owner-scoped like everything else.
	if err := store.Delete(ctx, owner.ID, accountID("b")); err != nil {
		t.Fatal(err)
	}
	if err := store.Delete(ctx, owner.ID+1, accountID("b")); err == nil {
		t.Fatal("another owner deleted an account")
	}
}

func TestAccountListByOwnerExcludesOtherOwners(t *testing.T) {
	ctx := context.Background()
	store := open(t)
	owner := mustUser(t, store, "owner", true)
	other := mustUser(t, store, "other", true)
	mustAccount(t, store, newAccount(owner.ID, accountID("a"), "official", 1))
	mustAccount(t, store, newAccount(owner.ID, accountID("b"), "cat", 2))
	mustAccount(t, store, newAccount(other.ID, accountID("c"), "official", 3))

	list, err := store.ListByOwner(ctx, owner.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 2 {
		t.Fatalf("ListByOwner = %d accounts, want 2", len(list))
	}
	for _, a := range list {
		if a.OwnerUserID != owner.ID {
			t.Errorf("ListByOwner returned account %q of owner %d", a.ID, a.OwnerUserID)
		}
	}
}

func TestAccountSessionAndStatusAreWrittenInTheOwnersName(t *testing.T) {
	ctx := context.Background()
	store := open(t)
	owner := mustUser(t, store, "owner", true)
	stranger := mustUser(t, store, "stranger", true)
	mustAccount(t, store, newAccount(owner.ID, accountID("a"), "official", 1))

	box := secrets.Box{Version: secrets.Version1, Nonce: []byte("nonce-5678"), Ciphertext: []byte("other-session")}
	if err := store.UpdateSession(ctx, owner.ID, accountID("a"), box, 7); err != nil {
		t.Fatalf("UpdateSession: %v", err)
	}
	got, err := store.ByID(ctx, owner.ID, accountID("a"))
	if err != nil {
		t.Fatal(err)
	}
	if string(got.Session.Ciphertext) != "other-session" || got.SessionEncKeyVersion != 7 {
		t.Errorf("session = %+v (key version %d)", got.Session, got.SessionEncKeyVersion)
	}

	// Neither write may be made in another owner's name.
	if err := store.UpdateSession(ctx, stranger.ID, accountID("a"), box, 1); !errors.Is(err, channelstore.ErrAccountNotFound) {
		t.Errorf("UpdateSession by another owner = %v, want ErrAccountNotFound", err)
	}
	if err := store.SetStatus(ctx, stranger.ID, accountID("a"), channelstore.AccountStatusError, "x"); !errors.Is(err, channelstore.ErrAccountNotFound) {
		t.Errorf("SetStatus by another owner = %v, want ErrAccountNotFound", err)
	}

	if err := store.SetStatus(ctx, owner.ID, accountID("a"), channelstore.AccountStatusDisconnected, "connection_reset"); err != nil {
		t.Fatalf("SetStatus: %v", err)
	}
	if got, err = store.ByID(ctx, owner.ID, accountID("a")); err != nil {
		t.Fatal(err)
	}
	if got.Status != channelstore.AccountStatusDisconnected || got.LastErrorCode != "connection_reset" {
		t.Errorf("status = %q (%q), want disconnected (connection_reset)", got.Status, got.LastErrorCode)
	}

	if err := store.SetStatus(ctx, owner.ID, accountID("a"), "reconnecting_forever", ""); err == nil {
		t.Error("SetStatus accepted a status the schema does not define")
	}

	at := time.Date(2026, 4, 2, 8, 30, 0, 0, time.UTC)
	if err := store.TouchConnected(ctx, accountID("a"), at); err != nil {
		t.Fatalf("TouchConnected: %v", err)
	}
	if got, err = store.ByID(ctx, owner.ID, accountID("a")); err != nil {
		t.Fatal(err)
	}
	if !got.LastConnectedAt.Equal(at) {
		t.Errorf("last_connected_at = %v, want %v", got.LastConnectedAt, at)
	}
}

func TestResolveLegacyNamesTheAdoptedAccount(t *testing.T) {
	ctx := context.Background()
	store := open(t)
	owner := mustUser(t, store, "owner", true)
	mustAccount(t, store, newAccount(owner.ID, accountID("a"), "official", 1))

	// A legacy central session nobody adopted belongs to nobody.
	if got, err := store.ResolveLegacy(ctx, "official"); err != nil || got != "" {
		t.Fatalf("ResolveLegacy before adoption = (%q, %v), want \"\"", got, err)
	}

	if err := store.RecordLegacyAdoption(ctx, "official", accountID("a"), owner.ID, "the operator owns this session"); err != nil {
		t.Fatalf("RecordLegacyAdoption: %v", err)
	}
	got, err := store.ResolveLegacy(ctx, "official")
	if err != nil {
		t.Fatal(err)
	}
	if got != accountID("a") {
		t.Fatalf("ResolveLegacy = %q, want %q", got, accountID("a"))
	}
	if got, err = store.ResolveLegacy(ctx, "cat"); err != nil || got != "" {
		t.Fatalf("ResolveLegacy for another legacy key = (%q, %v), want \"\"", got, err)
	}
}

func TestRecordLegacyAdoptionRequiresARealAccountAndOperator(t *testing.T) {
	ctx := context.Background()
	store := open(t)
	owner := mustUser(t, store, "owner", true)
	inactive := mustUser(t, store, "inactive", false)
	mustAccount(t, store, newAccount(owner.ID, accountID("a"), "official", 1))

	for name, args := range map[string]struct {
		legacyKey string
		accountID string
		adoptedBy int64
	}{
		"missing account":   {"official", accountID("z"), owner.ID},
		"missing operator":  {"official", accountID("a"), owner.ID + 100},
		"inactive operator": {"official", accountID("a"), inactive.ID},
		"empty legacy key":  {" ", accountID("a"), owner.ID},
	} {
		t.Run(name, func(t *testing.T) {
			if err := store.RecordLegacyAdoption(ctx, args.legacyKey, args.accountID, args.adoptedBy, "reason"); err == nil {
				t.Fatal("RecordLegacyAdoption accepted invalid adoption data")
			}
		})
	}

	if err := store.RecordLegacyAdoption(ctx, "official", accountID("a"), owner.ID, "reason"); err != nil {
		t.Fatalf("valid adoption: %v", err)
	}
	// A later, separately reasoned adoption remains auditable history.
	if err := store.RecordLegacyAdoption(ctx, "official", accountID("a"), owner.ID, "confirmed again"); err != nil {
		t.Fatalf("later adoption history: %v", err)
	}
}

func TestAccountCreateRefusesAnAccountThatIsNotOne(t *testing.T) {
	ctx := context.Background()
	store := open(t)
	owner := mustUser(t, store, "owner", true)

	for name, mutate := range map[string]func(*channelstore.Account){
		"no id":       func(a *channelstore.Account) { a.ID = "  " },
		"no owner":    func(a *channelstore.Account) { a.OwnerUserID = 0 },
		"no server":   func(a *channelstore.Account) { a.ServerKey = "" },
		"no network":  func(a *channelstore.Account) { a.NetworkType = "" },
		"bad status":  func(a *channelstore.Account) { a.Status = "napping" },
		"negative id": func(a *channelstore.Account) { a.RemoteUserID = -1 },
	} {
		t.Run(name, func(t *testing.T) {
			a := newAccount(owner.ID, accountID("a"), "official", 1)
			mutate(&a)
			if _, err := store.Create(ctx, a); !errors.Is(err, channelstore.ErrInvalidAccount) {
				t.Fatalf("Create = %v, want ErrInvalidAccount", err)
			}
		})
	}
}

func TestQuarantinedLegacyAccountsAreOutsideEveryOwnersListing(t *testing.T) {
	ctx := context.Background()
	store := open(t)
	owner := mustUser(t, store, "owner", true)

	// A join that names no user lands on the quarantined account of that
	// server's legacy session.
	if _, err := store.Upsert(ctx, channelstore.Channel{
		ClientKey: "official", ChannelID: 901,
		PeerType: channelstore.PeerChannel, MembershipStatus: channelstore.StatusMember,
	}); err != nil {
		t.Fatalf("Upsert: %v", err)
	}

	list, err := store.ListByOwner(ctx, owner.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 0 {
		t.Fatalf("ListByOwner = %d accounts, want none of the quarantined session", len(list))
	}
	if _, err := store.ByID(ctx, owner.ID, "legacy_official"); !errors.Is(err, channelstore.ErrAccountNotFound) {
		t.Fatalf("ByID for a quarantined account = %v, want ErrAccountNotFound", err)
	}

	// The legacy key is what reaches it, and that is the only way.
	legacy, err := store.ResolveLegacy(ctx, "official")
	if err != nil {
		t.Fatal(err)
	}
	if legacy != "legacy_official" {
		t.Fatalf("ResolveLegacy(official) = %q, want legacy_official", legacy)
	}
	// A second join through the same key is still one channel: the pair
	// (account, channel) is unique, so a repeated join updates rather than
	// duplicates.
	saved, err := store.Upsert(ctx, channelstore.Channel{
		ClientKey: "official", ChannelID: 901, Title: "Second look",
		PeerType: channelstore.PeerChannel, MembershipStatus: channelstore.StatusMember,
	})
	if err != nil {
		t.Fatal(err)
	}
	if saved.Title != "Second look" {
		t.Fatalf("a repeated join made a second row: %+v", saved)
	}
}

func TestEnsureRecordsTheAccountOnceAndNeverOverwritesIt(t *testing.T) {
	ctx := context.Background()
	store := open(t)
	owner := mustUser(t, store, "owner", true)

	identity := channelstore.AccountIdentity{
		OwnerUserID:       owner.ID,
		ServerKey:         "official",
		NetworkType:       channelstore.NetworkTelegram,
		RemoteUserID:      424242,
		RemoteUsername:    "example",
		RemoteDisplayName: "First name",
		PhoneNumberMasked: "+98 *** **12",
	}
	id, err := store.Ensure(ctx, identity)
	if err != nil {
		t.Fatalf("Ensure: %v", err)
	}

	// A later login with the same identity is the same account, and it does not
	// rewrite what the first one confirmed.
	identity.RemoteDisplayName = "Renamed by a later login"
	again, err := store.Ensure(ctx, identity)
	if err != nil {
		t.Fatal(err)
	}
	if again != id {
		t.Fatalf("Ensure = %q then %q, want one account", id, again)
	}
	got, err := store.ByID(ctx, owner.ID, id)
	if err != nil {
		t.Fatal(err)
	}
	if got.RemoteDisplayName != "First name" {
		t.Errorf("a later login overwrote the account: display name = %q", got.RemoteDisplayName)
	}
	if got.OwnerUserID != owner.ID || got.RemoteUserID != 424242 {
		t.Errorf("account = owner %d, remote %d", got.OwnerUserID, got.RemoteUserID)
	}
}

func TestEnsureGivesEachOwnerTheirOwnAccount(t *testing.T) {
	ctx := context.Background()
	store := open(t)
	first := mustUser(t, store, "first", true)
	second := mustUser(t, store, "second", true)

	identity := channelstore.AccountIdentity{
		OwnerUserID: first.ID, ServerKey: "official",
		NetworkType: channelstore.NetworkTelegram, RemoteUserID: 1,
	}
	mine, err := store.Ensure(ctx, identity)
	if err != nil {
		t.Fatal(err)
	}
	identity.OwnerUserID = second.ID
	theirs, err := store.Ensure(ctx, identity)
	if err != nil {
		t.Fatal(err)
	}
	if mine == theirs {
		t.Fatalf("both owners resolved to account %q", mine)
	}
	// The same owner may not hold it twice through Ensure either.
	identity.OwnerUserID = first.ID
	if again, err := store.Ensure(ctx, identity); err != nil || again != mine {
		t.Fatalf("Ensure again = (%q, %v), want %q", again, err, mine)
	}
}

func TestDropIncompleteLeavesAnAccountThatHasASession(t *testing.T) {
	ctx := context.Background()
	store := open(t)
	owner := mustUser(t, store, "owner", true)

	identity := channelstore.AccountIdentity{
		OwnerUserID: owner.ID, ServerKey: "official",
		NetworkType: channelstore.NetworkTelegram, RemoteUserID: 7,
	}
	id, err := store.Ensure(ctx, identity)
	if err != nil {
		t.Fatal(err)
	}

	// A login that never got as far as storing a session leaves an account that
	// cannot act. Dropping it is what keeps a restart from leaving it behind.
	if err := store.DropIncomplete(ctx, identity); err != nil {
		t.Fatalf("DropIncomplete: %v", err)
	}
	if _, err := store.ByID(ctx, owner.ID, id); !errors.Is(err, channelstore.ErrAccountNotFound) {
		t.Fatalf("an account without a session survived: %v", err)
	}

	// One that holds a session is a real account and is never dropped.
	id, err = store.Ensure(ctx, identity)
	if err != nil {
		t.Fatal(err)
	}
	box := secrets.Box{Version: secrets.Version1, Nonce: []byte("nonce-1234"), Ciphertext: []byte("sealed")}
	if err := store.UpdateSession(ctx, owner.ID, id, box, 1); err != nil {
		t.Fatal(err)
	}
	if err := store.DropIncomplete(ctx, identity); err != nil {
		t.Fatal(err)
	}
	if _, err := store.ByID(ctx, owner.ID, id); err != nil {
		t.Fatalf("an account with a session was dropped: %v", err)
	}

	// Another owner's account is not this owner's to drop, even for the same
	// remote identity on the same server.
	other := mustUser(t, store, "other", true)
	stranger := channelstore.AccountIdentity{
		OwnerUserID: other.ID, ServerKey: "official",
		NetworkType: channelstore.NetworkTelegram, RemoteUserID: 7,
	}
	if _, err := store.Ensure(ctx, stranger); err != nil {
		t.Fatal(err)
	}
	if err := store.DropIncomplete(ctx, identity); err != nil {
		t.Fatal(err)
	}
	if list, err := store.ListByOwner(ctx, other.ID); err != nil || len(list) != 1 {
		t.Fatalf("another owner's account was dropped: (%v, %v)", list, err)
	}
}
