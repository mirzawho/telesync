package gateway

import (
	"context"
	"errors"
	"testing"

	"github.com/mirza/telesync/internal/accountauth"
	"github.com/mirza/telesync/internal/channelstore"
)

// The resolver is the ONE seam that turns "which Telegram identity" into an
// account. These are its contract tests at the unit level: the HTTP behaviour
// it feeds is proven separately, over real requests.

// fakeAccounts is an account repository with the four reads the resolver and
// the pipeline make. It is not a mock of the store: the store's own tests pin
// that it answers owner-scoped, and this only pins the resolver's decisions.
type fakeAccounts struct {
	accounts []channelstore.Account
	byID     map[string]channelstore.Account
}

func (f fakeAccounts) ByID(_ context.Context, owner int64, id string) (channelstore.Account, error) {
	rec, ok := f.byID[id]
	if !ok || rec.OwnerUserID != owner {
		return channelstore.Account{}, channelstore.ErrAccountNotFound
	}
	return rec, nil
}

func (f fakeAccounts) ListByOwner(_ context.Context, owner int64) ([]channelstore.Account, error) {
	out := []channelstore.Account{}
	for _, a := range f.accounts {
		if a.OwnerUserID == owner {
			out = append(out, a)
		}
	}
	return out, nil
}

func (f fakeAccounts) SetStatus(context.Context, int64, string, string, string) error { return nil }

func (f fakeAccounts) Delete(context.Context, int64, string) error { return nil }

// accountRec builds one account record.
func accountRec(id string, owner int64, server, status string) channelstore.Account {
	return channelstore.Account{ID: id, OwnerUserID: owner, ServerKey: server, Status: status}
}

func TestStoreAccountResolverImplementsTheContractSeam(t *testing.T) {
	var _ accountauth.AccountResolver = NewStoreAccountResolver(fakeAccounts{})
}

func TestStoreAccountResolverErrorsAreTheContractOnes(t *testing.T) {
	r := NewStoreAccountResolver(fakeAccounts{})
	if _, err := r.Resolve(context.Background(), 1, "official"); !errors.Is(err, accountauth.ErrNoEligibleAccount) {
		t.Fatalf("Resolve with no accounts: err = %v, want the no-eligible sentinel", err)
	}
	multi := fakeAccounts{accounts: []channelstore.Account{
		accountRec("acc_aaaaaaaaaaaaaaaaaaaaaaaaaa", 1, "official", channelstore.AccountStatusConnected),
		accountRec("acc_bbbbbbbbbbbbbbbbbbbbbbbbbb", 1, "official", channelstore.AccountStatusConnected),
	}}
	if _, err := NewStoreAccountResolver(multi).Resolve(context.Background(), 1, "official"); !errors.Is(err, accountauth.ErrAccountSelectionRequired) {
		t.Fatalf("Resolve with two accounts: err = %v, want the selection sentinel", err)
	}
}

func TestStoreAccountResolverPicksTheOnlyEligibleAccount(t *testing.T) {
	one := fakeAccounts{accounts: []channelstore.Account{
		accountRec("acc_aaaaaaaaaaaaaaaaaaaaaaaaaa", 1, "official", channelstore.AccountStatusConnected),
		accountRec("acc_bbbbbbbbbbbbbbbbbbbbbbbbbb", 2, "official", channelstore.AccountStatusConnected),
		accountRec("acc_cccccccccccccccccccccccc", 1, "cat", channelstore.AccountStatusConnected),
	}}
	id, err := NewStoreAccountResolver(one).Resolve(context.Background(), 1, "official")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if id != "acc_aaaaaaaaaaaaaaaaaaaaaaaaaa" {
		t.Fatalf("Resolve = %q, want the caller's own account on that server", id)
	}
}

func TestStoreAccountResolverNeverFallsBackToAQuarantinedAccount(t *testing.T) {
	// A quarantined account belongs to nobody: it is a legacy central session,
	// not anyone's identity, and it is never eligible.
	only := fakeAccounts{accounts: []channelstore.Account{
		accountRec("legacy_official", 0, "official", channelstore.AccountStatusQuarantined),
	}}
	if _, err := NewStoreAccountResolver(only).Resolve(context.Background(), 1, "official"); !errors.Is(err, accountauth.ErrNoEligibleAccount) {
		t.Fatalf("Resolve over a quarantined account: err = %v, want no-eligible", err)
	}
}

func TestStoreAccountResolverSkipsRemovedAccounts(t *testing.T) {
	records := fakeAccounts{accounts: []channelstore.Account{
		accountRec("acc_aaaaaaaaaaaaaaaaaaaaaaaaaa", 1, "official", channelstore.AccountStatusRemoved),
	}}
	if _, err := NewStoreAccountResolver(records).Resolve(context.Background(), 1, "official"); !errors.Is(err, accountauth.ErrNoEligibleAccount) {
		t.Fatalf("Resolve over a removed account: err = %v, want no-eligible", err)
	}
}

func TestStoreAccountResolverOwnsIsOwnerScoped(t *testing.T) {
	store := fakeAccounts{byID: map[string]channelstore.Account{
		"acc_aaaaaaaaaaaaaaaaaaaaaaaaaa": accountRec("acc_aaaaaaaaaaaaaaaaaaaaaaaaaa", 1, "official", channelstore.AccountStatusConnected),
	}}
	r := NewStoreAccountResolver(store)
	if ok, err := r.Owns(context.Background(), 1, "acc_aaaaaaaaaaaaaaaaaaaaaaaaaa"); err != nil || !ok {
		t.Fatalf("Owns by the owner = %v, %v; want true", ok, err)
	}
	if ok, err := r.Owns(context.Background(), 2, "acc_aaaaaaaaaaaaaaaaaaaaaaaaaa"); err != nil || ok {
		t.Fatalf("Owns by another user = %v, %v; want false", ok, err)
	}
}
