package channelstore_test

import (
	"context"
	"fmt"
	"path/filepath"
	"testing"

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
