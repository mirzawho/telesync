package channelstore_test

import (
	"context"
	"errors"
	"reflect"
	"sort"
	"testing"

	"github.com/mirza/telesync/internal/channelstore"
	"github.com/mirza/telesync/internal/secrets"
)

func TestListRunnerAccountsRequiresActiveOwnerAuthorizationAndPersistedSession(t *testing.T) {
	ctx := context.Background()
	store := open(t)
	active := mustUser(t, store, "runner-active", true)
	suspended := mustUser(t, store, "runner-suspended", false)
	box := secrets.Box{
		Version:    secrets.Version1,
		Nonce:      []byte("nonce-1234"),
		Ciphertext: []byte("sealed-session"),
	}

	connected := mustAccount(t, store, newAccount(active.ID, accountID("a"), "official", 101))
	disconnected := mustAccount(t, store, newAccount(active.ID, accountID("b"), "official", 102))
	authRequired := mustAccount(t, store, newAccount(active.ID, accountID("c"), "cat", 103))
	disabled := newAccount(active.ID, accountID("d"), "cat", 104)
	disabled.Status = channelstore.AccountStatusDisabled
	disabled = mustAccount(t, store, disabled)
	suspendedOwner := mustAccount(t, store, newAccount(suspended.ID, accountID("e"), "official", 105))
	noSession := mustAccount(t, store, newAccount(active.ID, accountID("f"), "official", 106))

	for _, id := range []string{connected.ID, disconnected.ID, authRequired.ID, disabled.ID, suspendedOwner.ID} {
		if err := store.UpdateSession(ctx, ownerFor(id, active.ID, suspended.ID, suspendedOwner.ID), id, box, 1); err != nil {
			t.Fatalf("UpdateSession(%s): %v", id, err)
		}
	}
	if err := store.SetStatus(ctx, active.ID, disconnected.ID, channelstore.AccountStatusDisconnected, ""); err != nil {
		t.Fatalf("disconnect account: %v", err)
	}
	if err := store.SetStatus(ctx, active.ID, authRequired.ID, channelstore.AccountStatusAuthenticationRequired, "session_revoked"); err != nil {
		t.Fatalf("mark reauthentication required: %v", err)
	}

	got, err := store.ListRunnerAccounts(ctx)
	if err != nil {
		t.Fatalf("ListRunnerAccounts: %v", err)
	}
	ids := make([]string, 0, len(got))
	for _, account := range got {
		ids = append(ids, account.ID)
		if account.OwnerUserID != active.ID {
			t.Errorf("runner account %s has inactive or wrong owner %d", account.ID, account.OwnerUserID)
		}
		if account.Session.Version != secrets.Version1 || len(account.Session.Ciphertext) == 0 {
			t.Errorf("runner account %s has no persisted encrypted session", account.ID)
		}
	}
	sort.Strings(ids)
	want := []string{connected.ID, disconnected.ID}
	sort.Strings(want)
	if !reflect.DeepEqual(ids, want) {
		t.Fatalf("runner accounts = %v, want only authorized, active, session-backed accounts %v (no-session id %s)",
			ids, want, noSession.ID)
	}
}

func TestAccountForRunnerRejectsSuspendedOrMissingSession(t *testing.T) {
	ctx := context.Background()
	store := open(t)
	active := mustUser(t, store, "runner-active-lookup", true)
	suspended := mustUser(t, store, "runner-suspended-lookup", false)
	activeAcc := mustAccount(t, store, newAccount(active.ID, accountID("g"), "official", 201))
	suspendedAcc := mustAccount(t, store, newAccount(suspended.ID, accountID("h"), "official", 202))

	if _, err := store.AccountForRunner(ctx, activeAcc.ID); !errors.Is(err, channelstore.ErrAccountNotFound) {
		t.Fatalf("AccountForRunner without a session = %v, want not found", err)
	}
	box := secrets.Box{Version: secrets.Version1, Nonce: []byte("nonce-1234"), Ciphertext: []byte("sealed-session")}
	if err := store.UpdateSession(ctx, suspended.ID, suspendedAcc.ID, box, 1); err != nil {
		t.Fatalf("UpdateSession for suspended owner: %v", err)
	}
	if _, err := store.AccountForRunner(ctx, suspendedAcc.ID); !errors.Is(err, channelstore.ErrAccountNotFound) {
		t.Fatalf("AccountForRunner for suspended owner = %v, want not found", err)
	}
}

func ownerFor(id string, activeOwnerID, suspendedOwnerID int64, suspendedAccountID string) int64 {
	if id == suspendedAccountID {
		return suspendedOwnerID
	}
	return activeOwnerID
}
