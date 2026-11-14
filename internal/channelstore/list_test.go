package channelstore_test

import (
	"context"
	"fmt"
	"path/filepath"
	"testing"

	"github.com/mirza/telesync/internal/accountid"
	"github.com/mirza/telesync/internal/channelstore"
)

func openStore(t *testing.T) *channelstore.Store {
	t.Helper()
	store, err := channelstore.Open(filepath.Join(t.TempDir(), "state.db"), channelstore.Options{})
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return store
}

func upsert(t *testing.T, store *channelstore.Store, key string, telegramID int64, title, username string) channelstore.Channel {
	t.Helper()
	ch, err := store.Upsert(context.Background(), channelstore.Channel{
		ClientKey:        key,
		ChannelID:        telegramID,
		PeerType:         channelstore.PeerChannel,
		Title:            title,
		Username:         username,
		MembershipStatus: channelstore.StatusMember,
	})
	if err != nil {
		t.Fatalf("upsert %s/%d: %v", key, telegramID, err)
	}
	return ch
}

func TestListChannelsPaginatesWithATotal(t *testing.T) {
	store := openStore(t)
	for i := int64(1); i <= 5; i++ {
		upsert(t, store, "official", i, fmt.Sprintf("Channel %d", i), "")
	}

	page, total, err := store.ListChannels(context.Background(), channelstore.ChannelListQuery{Page: 2, PerPage: 2})
	if err != nil {
		t.Fatalf("ListChannels: %v", err)
	}
	if total != 5 {
		t.Fatalf("total = %d, want 5", total)
	}
	if len(page) != 2 {
		t.Fatalf("page = %d rows, want 2", len(page))
	}
}

func TestListChannelsForOwnerUnionsAccountsAndKeepsDuplicateRegistrationsDistinct(t *testing.T) {
	ctx := context.Background()
	store := openStore(t)
	owner := mustUser(t, store, "channel-owner", true)
	other := mustUser(t, store, "channel-other", true)
	newID := func() string {
		t.Helper()
		id, err := accountid.New()
		if err != nil {
			t.Fatal(err)
		}
		return id
	}
	a := mustAccount(t, store, channelstore.Account{
		ID: newID(), OwnerUserID: owner.ID, ServerKey: "official",
		NetworkType: channelstore.NetworkTelegram, RemoteUserID: 101, Status: channelstore.AccountStatusConnected,
	})
	b := mustAccount(t, store, channelstore.Account{
		ID: newID(), OwnerUserID: owner.ID, ServerKey: "official",
		NetworkType: channelstore.NetworkTelegram, RemoteUserID: 102, Status: channelstore.AccountStatusDisconnected,
	})
	foreign := mustAccount(t, store, channelstore.Account{
		ID: newID(), OwnerUserID: other.ID, ServerKey: "official",
		NetworkType: channelstore.NetworkTelegram, RemoteUserID: 103, Status: channelstore.AccountStatusConnected,
	})

	register := func(accountID string, telegramID int64, title string) channelstore.Channel {
		t.Helper()
		ch, err := store.RegisterForAccount(ctx, channelstore.Channel{
			AccountID: accountID, ClientKey: "official", ChannelID: telegramID,
			PeerType: channelstore.PeerChannel, Title: title,
			MembershipStatus: channelstore.StatusMember,
		})
		if err != nil {
			t.Fatalf("RegisterForAccount: %v", err)
		}
		return ch
	}
	first := register(a.ID, 700, "Shared under A")
	second := register(b.ID, 700, "Shared under B")
	register(foreign.ID, 800, "Not this owner")

	got, total, err := store.ListChannelsForOwner(ctx, owner.ID, nil, channelstore.ChannelListQuery{})
	if err != nil {
		t.Fatalf("ListChannelsForOwner: %v", err)
	}
	if total != 2 || len(got) != 2 {
		t.Fatalf("owner union = %d/%d, want 2/2", total, len(got))
	}
	seen := map[int64]bool{}
	for _, ch := range got {
		seen[ch.ID] = true
		if ch.ChannelID != 700 {
			t.Errorf("unexpected remote channel %d", ch.ChannelID)
		}
	}
	if !seen[first.ID] || !seen[second.ID] {
		t.Fatalf("duplicate registrations were collapsed: got local ids %v", channelIDs(got))
	}

	filtered, filteredTotal, err := store.ListChannelsForOwner(ctx, owner.ID, []string{b.ID}, channelstore.ChannelListQuery{})
	if err != nil {
		t.Fatalf("ListChannelsForOwner account filter: %v", err)
	}
	if filteredTotal != 1 || len(filtered) != 1 || filtered[0].ID != second.ID {
		t.Fatalf("account filter = %v/%d, want only local row %d", channelIDs(filtered), filteredTotal, second.ID)
	}
	foreignFilter, foreignTotal, err := store.ListChannelsForOwner(ctx, owner.ID, []string{foreign.ID}, channelstore.ChannelListQuery{})
	if err != nil {
		t.Fatalf("ListChannelsForOwner foreign filter: %v", err)
	}
	if foreignTotal != 0 || len(foreignFilter) != 0 {
		t.Fatalf("foreign account filter exposed rows: %v/%d", channelIDs(foreignFilter), foreignTotal)
	}
}

func TestGetByIDForOwnerDoesNotResolveAnotherOwnersRow(t *testing.T) {
	ctx := context.Background()
	store := openStore(t)
	owner := mustUser(t, store, "channel-row-owner", true)
	other := mustUser(t, store, "channel-row-other", true)
	id, err := accountid.New()
	if err != nil {
		t.Fatal(err)
	}
	account := mustAccount(t, store, channelstore.Account{
		ID: id, OwnerUserID: owner.ID, ServerKey: "official",
		NetworkType: channelstore.NetworkTelegram, RemoteUserID: 201, Status: channelstore.AccountStatusDisconnected,
	})
	row, err := store.RegisterForAccount(ctx, channelstore.Channel{
		AccountID: account.ID, ClientKey: "official", ChannelID: 900,
		PeerType: channelstore.PeerChannel, Title: "Cached while disconnected",
		MembershipStatus: channelstore.StatusMember,
	})
	if err != nil {
		t.Fatal(err)
	}

	got, found, err := store.GetByIDForOwner(ctx, owner.ID, row.ID)
	if err != nil || !found || got.AccountID != account.ID {
		t.Fatalf("owner read = (%+v, %v, %v)", got, found, err)
	}
	got, found, err = store.GetByIDForOwner(ctx, other.ID, row.ID)
	if err != nil || found {
		t.Fatalf("foreign owner read = (%+v, %v, %v), want not found", got, found, err)
	}
}

func TestListChannelsSearchesTitleUsernameAndClientKey(t *testing.T) {
	store := openStore(t)
	upsert(t, store, "official", 1, "Technology News", "technews")
	upsert(t, store, "official", 2, "Sports", "sport_daily")
	upsert(t, store, "the-cat", 3, "Studio", "studio")

	for _, tc := range []struct {
		q    string
		want []int64
	}{
		{"technology", []int64{1}},
		{"SPORT_", []int64{2}},
		{"the-cat", []int64{3}},
		{"o", []int64{1, 2, 3}},
	} {
		got, total, err := store.ListChannels(context.Background(), channelstore.ChannelListQuery{Page: 1, PerPage: 20, Query: tc.q})
		if err != nil {
			t.Fatalf("q=%q: %v", tc.q, err)
		}
		if total != len(tc.want) {
			t.Fatalf("q=%q: total = %d, want %d", tc.q, total, len(tc.want))
		}
		seen := map[int64]bool{}
		for _, ch := range got {
			seen[ch.ChannelID] = true
		}
		for _, id := range tc.want {
			if !seen[id] {
				t.Fatalf("q=%q: channel %d missing from %+v", tc.q, id, seen)
			}
		}
	}
}

func TestListChannelsTreatsSearchTextAsLiteral(t *testing.T) {
	store := openStore(t)
	upsert(t, store, "official", 1, "100% real", "")
	upsert(t, store, "official", 2, "100x real", "")

	got, total, err := store.ListChannels(context.Background(), channelstore.ChannelListQuery{Page: 1, PerPage: 20, Query: "0%"})
	if err != nil {
		t.Fatalf("ListChannels: %v", err)
	}
	if total != 1 || len(got) != 1 || got[0].ChannelID != 1 {
		t.Fatalf("got %d/%d, want only the channel whose title contains \"0%%\"", total, len(got))
	}
}

func TestListChannelsIsEmptyWithoutRows(t *testing.T) {
	store := openStore(t)
	got, total, err := store.ListChannels(context.Background(), channelstore.ChannelListQuery{Page: 1, PerPage: 20})
	if err != nil {
		t.Fatalf("ListChannels: %v", err)
	}
	if total != 0 || len(got) != 0 {
		t.Fatalf("got %d/%d rows, want none", len(got), total)
	}
}

func TestListChannelsByIDsPagesOverExactlyThoseRows(t *testing.T) {
	store := openStore(t)
	a := upsert(t, store, "official", 1, "A", "")
	b := upsert(t, store, "official", 2, "B", "")
	upsert(t, store, "official", 3, "C", "")

	got, err := store.ListChannelsByIDs(context.Background(), []int64{a.ID, b.ID})
	if err != nil {
		t.Fatalf("ListChannelsByIDs: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d rows, want 2", len(got))
	}
	for _, ch := range got {
		if ch.ID != a.ID && ch.ID != b.ID {
			t.Fatalf("unexpected row %+v", ch)
		}
	}

	empty, err := store.ListChannelsByIDs(context.Background(), nil)
	if err != nil || len(empty) != 0 {
		t.Fatalf("empty ids = %v, %v", empty, err)
	}
}

func TestListChannelsForUserIsScopedByTheAssignment(t *testing.T) {
	ctx := context.Background()
	store := open(t)
	a, _ := store.Upsert(ctx, upsert(t, store, "official", 1001, "A", ""))
	b, _ := store.Upsert(ctx, upsert(t, store, "official", 1002, "B", ""))
	u := mustUser(t, store, "test", true)

	ids := func() []int64 {
		t.Helper()
		rows, err := store.ListChannelsForUser(ctx, u.ID, channelstore.ChannelListQuery{})
		if err != nil {
			t.Fatal(err)
		}
		out := []int64{}
		for _, ch := range rows {
			out = append(out, ch.ID)
		}
		return out
	}
	want := func(what string, got []int64, want ...int64) {
		t.Helper()
		if len(got) != len(want) {
			t.Fatalf("%s = %v, want %v", what, got, want)
		}
		for i := range want {
			if got[i] != want[i] {
				t.Fatalf("%s = %v, want %v", what, got, want)
			}
		}
	}

	// No assignment and no global permission selects nothing.
	want("nothing granted", ids())

	// One explicit allow selects that record and only it.
	if err := store.ReplacePermissions(ctx, u.ID, channelstore.PermissionSet{
		Channels: []channelstore.ResourcePermissions{{ResourceID: a.ID, Permissions: []channelstore.ResourcePermission{allow(channelstore.ActionView)}}},
	}); err != nil {
		t.Fatal(err)
	}
	want("one allow", ids(), a.ID)

	// The global view selects the catalog; a deny removes one record from it.
	if err := store.ReplacePermissions(ctx, u.ID, channelstore.PermissionSet{
		Global:   []channelstore.GlobalPermission{channelstore.PermissionChannelsView},
		Channels: []channelstore.ResourcePermissions{{ResourceID: b.ID, Permissions: []channelstore.ResourcePermission{deny(channelstore.ActionView)}}},
	}); err != nil {
		t.Fatal(err)
	}
	want("global view with a deny", ids(), a.ID)

	// An explicit allow on one record and an update grant on the other lists
	// only the first: a write grant is never a view.
	if err := store.ReplacePermissions(ctx, u.ID, channelstore.PermissionSet{
		Channels: []channelstore.ResourcePermissions{
			{ResourceID: a.ID, Permissions: []channelstore.ResourcePermission{allow(channelstore.ActionView)}},
			{ResourceID: b.ID, Permissions: []channelstore.ResourcePermission{allow(channelstore.ActionUpdate)}},
		},
	}); err != nil {
		t.Fatal(err)
	}
	want("update grant without view", ids(), a.ID)
}

func TestListChannelsFiltersByAtLeastOneSelectedCategory(t *testing.T) {
	ctx := context.Background()
	store := openStore(t)
	account := categoryTestAccount(t, store, "official", "acc_category_filter")
	upsertAccountChannel := func(id int64, title, username string) channelstore.Channel {
		t.Helper()
		ch, err := store.UpsertFor(ctx, account.ID, channelstore.Channel{
			ClientKey: "official", AccountID: account.ID, ChannelID: id,
			PeerType: channelstore.PeerChannel, Title: title, Username: username,
			MembershipStatus: channelstore.StatusMember,
		})
		if err != nil {
			t.Fatalf("upsert account channel: %v", err)
		}
		return ch
	}
	tech := upsertAccountChannel(1, "Tech Daily", "tech")
	sports := upsertAccountChannel(2, "Sports Hour", "sports")
	plain := upsert(t, store, "official", 3, "Untagged", "plain")
	newsCat, err := store.CreateCategory(ctx, "News", "")
	if err != nil {
		t.Fatalf("CreateCategory: %v", err)
	}
	sportsCat, err := store.CreateCategory(ctx, "Sports", "")
	if err != nil {
		t.Fatalf("CreateCategory: %v", err)
	}
	if err := store.ReplaceCategories(ctx, tech.AccountID, tech.ChannelID, []int64{newsCat.ID}); err != nil {
		t.Fatalf("ReplaceCategories: %v", err)
	}
	if err := store.ReplaceCategories(ctx, sports.AccountID, sports.ChannelID, []int64{sportsCat.ID}); err != nil {
		t.Fatalf("ReplaceCategories: %v", err)
	}

	one, total, err := store.ListChannels(ctx, channelstore.ChannelListQuery{
		Page: 1, PerPage: 20, CategoryIDs: []int64{newsCat.ID},
	})
	if err != nil {
		t.Fatalf("ListChannels: %v", err)
	}
	if total != 1 || len(one) != 1 || one[0].ID != tech.ID {
		t.Fatalf("news filter = %d/%d rows (ids %v), want only the tech channel", total, len(one), channelIDs(one))
	}

	// OR semantics: either selected category admits its channels, but an
	// untagged channel never appears.
	both, total, err := store.ListChannels(ctx, channelstore.ChannelListQuery{
		Page: 1, PerPage: 20, CategoryIDs: []int64{newsCat.ID, sportsCat.ID},
	})
	if err != nil {
		t.Fatalf("ListChannels: %v", err)
	}
	if total != 2 || len(both) != 2 {
		t.Fatalf("both categories = %d/%d rows (ids %v), want the 2 tagged channels", total, len(both), channelIDs(both))
	}
	for _, ch := range both {
		if ch.ID == plain.ID {
			t.Fatal("untagged channel appeared under a category filter")
		}
	}

	// A filter of no categories behaves as no filter at all.
	_, unfiltered, err := store.ListChannels(ctx, channelstore.ChannelListQuery{Page: 1, PerPage: 20})
	if err != nil {
		t.Fatalf("ListChannels: %v", err)
	}
	if unfiltered != 3 {
		t.Fatalf("no filter total = %d, want 3", unfiltered)
	}
}

func channelIDs(rows []channelstore.Channel) []int64 {
	out := make([]int64, 0, len(rows))
	for _, ch := range rows {
		out = append(out, ch.ID)
	}
	return out
}
