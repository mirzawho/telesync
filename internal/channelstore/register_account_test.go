package channelstore_test

import (
	"context"
	"testing"

	"github.com/mirza/telesync/internal/channelstore"
)

// RegisterForAccount is the account-scoped registration path (contract R8).
//
// Store.Upsert records a channel under the quarantined account of a legacy
// client key, which is correct for a legacy join and wrong for a user-initiated
// one: a channel a user joined through their own account must belong to that
// account. These tests pin that difference.

// ownedAccount records one account owned by the given user.
func ownedAccount(t *testing.T, s *channelstore.Store, remote int64, name string) channelstore.Account {
	t.Helper()
	owner := mustUser(t, s, name, true)
	return mustAccount(t, s, newAccount(owner.ID, accountID(name), "official", remote*100))
}

func TestRegisterForAccountRecordsTheChannelUnderThatAccount(t *testing.T) {
	s := open(t)
	ctx := context.Background()
	acct := ownedAccount(t, s, 7, "alice")

	saved, err := s.RegisterForAccount(ctx, channelstore.Channel{
		ClientKey: "official", AccountID: acct.ID, ChannelID: 42,
		PeerType: channelstore.PeerChannel, Title: "News",
		MembershipStatus: channelstore.StatusMember,
	})
	if err != nil {
		t.Fatalf("RegisterForAccount: %v", err)
	}
	if saved.ID == 0 {
		t.Fatal("the saved channel has no local id")
	}
	if saved.AccountID != acct.ID {
		t.Fatalf("AccountID = %q, want %q", saved.AccountID, acct.ID)
	}
}

func TestRegisterForAccountRefusesAChannelTheOwnerDoesNotHold(t *testing.T) {
	s := open(t)
	ctx := context.Background()
	alice := ownedAccount(t, s, 7, "alice")
	bob := ownedAccount(t, s, 8, "bob")

	// Registering against bob's account while the caller is alice must not
	// succeed: the ownership check is part of the write.
	_, err := s.RegisterForAccount(ctx, channelstore.Channel{
		ClientKey: "official", AccountID: bob.ID, ChannelID: 42,
		PeerType: channelstore.PeerChannel, Title: "News",
		MembershipStatus: channelstore.StatusMember,
	}, channelstore.WithOwner(alice.OwnerUserID))
	if err == nil {
		t.Fatal("RegisterForAccount accepted an account the caller does not own")
	}
}

func TestTwoOwnersRegisterTheSameRemoteChannelSeparately(t *testing.T) {
	s := open(t)
	ctx := context.Background()
	alice := ownedAccount(t, s, 7, "alice")
	bob := ownedAccount(t, s, 8, "bob")

	first, err := s.RegisterForAccount(ctx, channelstore.Channel{
		ClientKey: "official", AccountID: alice.ID, ChannelID: 42,
		PeerType: channelstore.PeerChannel, Title: "News",
		MembershipStatus: channelstore.StatusMember,
	})
	if err != nil {
		t.Fatalf("alice RegisterForAccount: %v", err)
	}
	second, err := s.RegisterForAccount(ctx, channelstore.Channel{
		ClientKey: "official", AccountID: bob.ID, ChannelID: 42,
		PeerType: channelstore.PeerChannel, Title: "News",
		MembershipStatus: channelstore.StatusMember,
	})
	if err != nil {
		t.Fatalf("bob RegisterForAccount: %v", err)
	}
	if first.ID == second.ID {
		t.Fatalf("two owners shared one local channel row (%d): §15 requires two", first.ID)
	}
}

func TestRegisterForAccountIsIdempotentPerAccount(t *testing.T) {
	s := open(t)
	ctx := context.Background()
	acct := ownedAccount(t, s, 7, "alice")

	first, err := s.RegisterForAccount(ctx, channelstore.Channel{
		ClientKey: "official", AccountID: acct.ID, ChannelID: 42,
		PeerType: channelstore.PeerChannel, Title: "News",
		MembershipStatus: channelstore.StatusMember,
	})
	if err != nil {
		t.Fatal(err)
	}
	again, err := s.RegisterForAccount(ctx, channelstore.Channel{
		ClientKey: "official", AccountID: acct.ID, ChannelID: 42,
		PeerType: channelstore.PeerChannel, Title: "News",
		MembershipStatus: channelstore.StatusMember,
	})
	if err != nil {
		t.Fatal(err)
	}
	if first.ID != again.ID {
		t.Fatalf("re-registration created a second row: %d and %d", first.ID, again.ID)
	}
}

func TestUpsertStillLandsOnTheQuarantinedAccount(t *testing.T) {
	s := open(t)
	ctx := context.Background()
	saved, err := s.Upsert(ctx, channelstore.Channel{
		ClientKey: "official", ChannelID: 77, PeerType: channelstore.PeerChannel,
		Title: "Legacy", MembershipStatus: channelstore.StatusMember,
	})
	if err != nil {
		t.Fatalf("Upsert: %v", err)
	}
	legacy, err := s.AccountOfChannel(context.Background(), saved.ID)
	if err != nil {
		t.Fatalf("AccountOfChannel: %v", err)
	}
	if legacy != "legacy_official" {
		t.Fatalf("account = %q, want the quarantined legacy account", legacy)
	}
}

// Account-scoped reads: an owner must see their own registrations and never
// another owner's, nor the quarantined ones.

func TestListChannelsForAccountSeesOnlyThatAccountsRows(t *testing.T) {
	s := open(t)
	ctx := context.Background()
	alice := ownedAccount(t, s, 7, "alice")
	bob := ownedAccount(t, s, 8, "bob")

	if _, err := s.RegisterForAccount(ctx, channelstore.Channel{
		ClientKey: "official", AccountID: alice.ID, ChannelID: 1,
		PeerType: channelstore.PeerChannel, Title: "A", MembershipStatus: channelstore.StatusMember,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.RegisterForAccount(ctx, channelstore.Channel{
		ClientKey: "official", AccountID: bob.ID, ChannelID: 1,
		PeerType: channelstore.PeerChannel, Title: "B", MembershipStatus: channelstore.StatusMember,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Upsert(ctx, channelstore.Channel{
		ClientKey: "official", ChannelID: 3, PeerType: channelstore.PeerChannel,
		Title: "Quarantined", MembershipStatus: channelstore.StatusMember,
	}); err != nil {
		t.Fatal(err)
	}

	rows, err := s.ListChannelsForAccount(ctx, alice.ID)
	if err != nil {
		t.Fatalf("ListChannelsForAccount: %v", err)
	}
	if len(rows) != 1 || rows[0].AccountID != alice.ID {
		t.Fatalf("alice's rows = %+v, want exactly her own", rows)
	}
}

func TestGetForAccountDoesNotAnswerForAnotherAccountsChannel(t *testing.T) {
	s := open(t)
	ctx := context.Background()
	alice := ownedAccount(t, s, 7, "alice")
	bob := ownedAccount(t, s, 8, "bob")

	saved, err := s.RegisterForAccount(ctx, channelstore.Channel{
		ClientKey: "official", AccountID: bob.ID, ChannelID: 1,
		PeerType: channelstore.PeerChannel, Title: "B", MembershipStatus: channelstore.StatusMember,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, found, err := s.GetForAccount(ctx, alice.ID, saved.ChannelID); err != nil {
		t.Fatalf("GetForAccount: %v", err)
	} else if found {
		t.Fatal("alice read a channel registered under bob's account")
	}
	if _, found, err := s.GetForAccount(ctx, bob.ID, saved.ChannelID); err != nil || !found {
		t.Fatalf("bob could not read his own channel: found=%v err=%v", found, err)
	}
}
