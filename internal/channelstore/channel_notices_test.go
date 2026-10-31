package channelstore

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/mirza/telesync/internal/tgapi"
)

// Telegram's verified / scam / fake notices were not persisted before
// migration 6. A row written before it can say the notices are unknown; a row
// written after it carries what Telegram reported.

func TestANewDatabaseStoresTheChannelNotices(t *testing.T) {
	ctx := context.Background()
	store := openInternal(t)

	saved, err := store.Upsert(ctx, Channel{
		ClientKey: "official", ChannelID: 1, PeerType: PeerChannel, Title: "Noted",
		MembershipStatus: StatusMember, Verified: true, Scam: true, Fake: true,
	})
	if err != nil {
		t.Fatalf("upsert: %v", err)
	}
	if !saved.Verified || !saved.Scam || !saved.Fake {
		t.Fatalf("saved = %+v", saved)
	}

	got, ok, err := store.GetByID(ctx, saved.ID)
	if err != nil || !ok {
		t.Fatalf("get: ok=%v err=%v", ok, err)
	}
	if !got.Verified || !got.Scam || !got.Fake {
		t.Fatalf("read back = %+v, want every notice set", got)
	}
}

func TestUpsertReplacesTheChannelNotices(t *testing.T) {
	ctx := context.Background()
	store := openInternal(t)
	first, err := store.Upsert(ctx, Channel{
		ClientKey: "official", ChannelID: 1, PeerType: PeerChannel, Title: "Before",
		MembershipStatus: StatusMember, Verified: true, NoticesKnown: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	second, err := store.Upsert(ctx, Channel{
		ClientKey: "official", ChannelID: 1, PeerType: PeerGroup,
		Title: "After", MembershipStatus: StatusMember, Scam: true, NoticesKnown: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if second.ID != first.ID {
		t.Fatalf("upsert created a second row: %d then %d", first.ID, second.ID)
	}
	if second.Verified || !second.Scam || second.Fake {
		t.Fatalf("notices after refresh = %+v, want the latest report", second)
	}
}

func TestARowWrittenBeforeMigrationSixHasUnknownNotices(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "pre-six.db")

	old := openInternalAt(t, path, 5)
	raw := `INSERT INTO managed_channels (client_key, channel_id, peer_type, title,
		membership_status, created_at, updated_at, last_synced_at)
		VALUES ('official', 42, 'channel', 'Old', 'member',
		'2026-01-01T00:00:00Z', '2026-01-01T00:00:00Z', '2026-01-01T00:00:00Z')`
	if _, err := old.db.ExecContext(ctx, raw); err != nil {
		t.Fatalf("seed a pre-migration row: %v", err)
	}
	var seed int64
	if err := old.db.QueryRowContext(ctx,
		`SELECT id FROM managed_channels WHERE client_key = 'official' AND channel_id = 42`).Scan(&seed); err != nil {
		t.Fatal(err)
	}
	if err := old.Close(); err != nil {
		t.Fatal(err)
	}

	upgraded, err := Open(path, Options{})
	if err != nil {
		t.Fatalf("upgrade: %v", err)
	}
	defer upgraded.Close()

	got, ok, err := upgraded.GetByID(ctx, seed)
	if err != nil || !ok {
		t.Fatalf("row lost in upgrade: ok=%v err=%v", ok, err)
	}
	if got.NoticesKnown {
		t.Fatal("a row written before the notices existed claims to know them")
	}
	if got.Verified || got.Scam || got.Fake {
		t.Fatalf("an unknown notice reads as set: %+v", got)
	}

	// A fresh report from Telegram makes them known again.
	refreshed, err := upgraded.Upsert(ctx, Channel{
		ClientKey: "official", ChannelID: 42, PeerType: PeerChannel, Title: "Old",
		MembershipStatus: StatusMember, Fake: true, NoticesKnown: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !refreshed.NoticesKnown || refreshed.Verified || !refreshed.Fake {
		t.Fatalf("refreshed notices = %+v", refreshed)
	}
}

func TestTheUpgradePreservesEverythingAroundTheChannel(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "pre-six.db")

	old := openInternalAt(t, path, 5)
	user, err := old.CreateUser(ctx, "Operator", "operator", "hash", true)
	if err != nil {
		t.Fatal(err)
	}
	// A row exactly as the previous release wrote it: the INSERT names only
	// the columns that existed then.
	if _, err := old.db.ExecContext(ctx, `INSERT INTO managed_channels
		(client_key, channel_id, peer_type, title, username, description, avatar_photo_id,
		 membership_status, participants_count, created_at, updated_at, last_synced_at)
		VALUES ('official', 7, 'channel', 'Kept', '', '', 0, 'member', 99,
		'2026-01-01T00:00:00Z', '2026-01-01T00:00:00Z', '2026-01-01T00:00:00Z')`); err != nil {
		t.Fatalf("seed a pre-migration row: %v", err)
	}
	var row Channel
	if err := old.db.QueryRowContext(ctx,
		`SELECT id FROM managed_channels WHERE client_key = 'official' AND channel_id = 7`).Scan(&row.ID); err != nil {
		t.Fatal(err)
	}
	category, err := old.CreateCategory(ctx, "News", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := old.AssignChannel(ctx, "official", "7", category.ID); err != nil {
		t.Fatal(err)
	}
	if err := old.ReplacePermissions(ctx, user.ID, PermissionSet{
		Global: []GlobalPermission{PermissionChannelsView},
		Channels: []ResourcePermissions{{ResourceID: row.ID, Permissions: []ResourcePermission{
			{Action: ActionView, Effect: EffectAllow},
		}}},
	}); err != nil {
		t.Fatal(err)
	}
	if err := old.Close(); err != nil {
		t.Fatal(err)
	}

	upgraded, err := Open(path, Options{})
	if err != nil {
		t.Fatalf("upgrade: %v", err)
	}
	defer upgraded.Close()

	perm, err := upgraded.PermissionsOf(ctx, user.ID)
	if err != nil {
		t.Fatalf("permissions: %v", err)
	}
	if len(perm.Global) != 1 || perm.Global[0] != PermissionChannelsView {
		t.Fatalf("global permission lost: %+v", perm.Global)
	}
	if len(perm.Channels) != 1 || perm.Channels[0].ResourceID != row.ID ||
		len(perm.Channels[0].Permissions) != 1 ||
		perm.Channels[0].Permissions[0] != (ResourcePermission{Action: ActionView, Effect: EffectAllow}) {
		t.Fatalf("per-channel assignment lost: %+v", perm.Channels)
	}
	ids, err := upgraded.AssignmentCounts(ctx, "official", "7")
	if err != nil || len(ids) != 1 || ids[category.ID] == 0 {
		t.Fatalf("category assignment lost: %v %v", ids, err)
	}
}

func TestAReportWithoutTheNoticesKeepsTheStoredOnes(t *testing.T) {
	ctx := context.Background()
	store := openInternal(t)
	// A report that carried them.
	if _, err := store.Upsert(ctx, Channel{
		ClientKey: "official", ChannelID: 3, PeerType: PeerChannel, Title: "Flagged",
		MembershipStatus: StatusMember, Scam: true, NoticesKnown: true,
	}); err != nil {
		t.Fatal(err)
	}
	// A later write that did not: the stored notices stay, and stay known.
	kept, err := store.Upsert(ctx, Channel{
		ClientKey: "official", ChannelID: 3, PeerType: PeerChannel, Title: "Flagged again",
		MembershipStatus: StatusMember,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !kept.NoticesKnown || !kept.Scam {
		t.Fatalf("a report without notices cleared them: %+v", kept)
	}
}

func TestThePermissionsRoundTripAndSurviveAnUnreportedWrite(t *testing.T) {
	ctx := context.Background()
	store := openInternal(t)
	want := tgapi.Permissions{New: true, Edit: true, Delete: false, Read: true}
	if _, err := store.Upsert(ctx, Channel{
		ClientKey: "official", ChannelID: 8, PeerType: PeerGroup, Title: "Editable",
		MembershipStatus: StatusMember, Permissions: want, PermissionsKnown: true,
	}); err != nil {
		t.Fatal(err)
	}
	got, _, err := store.Get(ctx, "official", 8)
	if err != nil {
		t.Fatal(err)
	}
	if !got.PermissionsKnown || got.Permissions != want {
		t.Fatalf("permissions = %+v known=%v, want %+v", got.Permissions, got.PermissionsKnown, want)
	}

	// A write that carried no permission report leaves them as they were.
	kept, err := store.Upsert(ctx, Channel{
		ClientKey: "official", ChannelID: 8, PeerType: PeerGroup, Title: "Editable",
		MembershipStatus: StatusMember,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !kept.PermissionsKnown || kept.Permissions != want {
		t.Fatalf("an unreported write changed the permissions: %+v", kept)
	}
}
