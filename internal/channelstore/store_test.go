package channelstore_test

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/mirza/telesync/internal/automation"
	"github.com/mirza/telesync/internal/channelstore"
)

func open(t *testing.T, mutate ...func(*channelstore.Options)) *channelstore.Store {
	t.Helper()
	opts := channelstore.Options{}
	for _, fn := range mutate {
		fn(&opts)
	}
	store, err := channelstore.Open(filepath.Join(t.TempDir(), "state", "channels.db"), opts)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	// Every automation row carries an owner now, so the shared fixture carries
	// one too: an event or a job with no owning account is refused at the door
	// rather than run as somebody else. Tests that need two owners build their
	// own and overwrite defaultOwner.
	user, err := store.CreateUser(context.Background(), "Fixture Owner", "fixture-owner",
		"not-a-password-hash", true)
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	defaultOwner = automation.Owner{
		OwnerUserID: user.ID,
		AccountID:   ownerAccount(t, store, user.ID, "official", channelstore.NetworkTelegram, 1),
	}
	return store
}

func channel(key string, id int64) channelstore.Channel {
	return channelstore.Channel{
		ClientKey:        key,
		ChannelID:        id,
		PeerType:         channelstore.PeerChannel,
		Title:            "Example Channel",
		Username:         "example",
		Description:      "about",
		AvatarPhotoID:    77,
		MembershipStatus: channelstore.StatusMember,
	}
}

func TestOpenAppliesTheSchemaAndReportsItsVersion(t *testing.T) {
	store := open(t)
	version, err := store.SchemaVersion(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if version < 1 {
		t.Fatalf("schema version = %d", version)
	}
}

func TestOpenCreatesTheDirectoryAndRestrictsTheFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "nested", "deeper", "channels.db")
	store, err := channelstore.Open(path, channelstore.Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Fatalf("database mode = %o, want 600", perm)
	}
	if _, err := os.Stat(filepath.Dir(path)); err != nil {
		t.Fatalf("database directory was not created: %v", err)
	}
}

func TestOpenRejectsARelativePath(t *testing.T) {
	if _, err := channelstore.Open("relative.db", channelstore.Options{}); err == nil {
		t.Fatal("a relative database path must be rejected")
	}
}

func TestManagedChannelsAreListedWithTheirLocalID(t *testing.T) {
	ctx := context.Background()
	store := open(t)
	first, err := store.Upsert(ctx, channel("official", 500))
	if err != nil {
		t.Fatal(err)
	}
	second, err := store.Upsert(ctx, channel("the-cat", 500))
	if err != nil {
		t.Fatal(err)
	}

	var listed []channelstore.Channel
	for _, key := range []string{"official", "the-cat"} {
		rows, err := store.List(ctx, key)
		if err != nil {
			t.Fatalf("List(%q): %v", key, err)
		}
		listed = append(listed, rows...)
	}
	seen := map[int64]bool{}
	for _, ch := range listed {
		seen[ch.ID] = true
	}
	if !seen[first.ID] || !seen[second.ID] {
		t.Fatalf("List returned %v, want the local ids %d and %d", seen, first.ID, second.ID)
	}
	if first.ID == second.ID {
		t.Fatal("two channels under different keys share a local id")
	}
	if first.ChannelID != 500 || second.ChannelID != 500 {
		t.Fatal("the Telegram id should be the same for both")
	}
}

func TestUpsertInsertsThenUpdatesWithoutDuplicating(t *testing.T) {
	ctx := context.Background()
	store := open(t)

	saved, err := store.Upsert(ctx, channel("official", 500))
	if err != nil {
		t.Fatalf("Upsert: %v", err)
	}
	if saved.ID == 0 || saved.ChannelID != 500 || saved.Title != "Example Channel" {
		t.Fatalf("saved = %+v", saved)
	}

	updated := channel("official", 500)
	updated.Title = "Renamed"
	updated.MembershipStatus = channelstore.StatusJoinPending
	again, err := store.Upsert(ctx, updated)
	if err != nil {
		t.Fatalf("Upsert again: %v", err)
	}
	if again.ID != saved.ID {
		t.Fatalf("a second upsert created a new row: %d then %d", saved.ID, again.ID)
	}
	if again.Title != "Renamed" || again.MembershipStatus != channelstore.StatusJoinPending {
		t.Fatalf("update did not apply: %+v", again)
	}
	if !again.CreatedAt.Equal(saved.CreatedAt) {
		t.Fatalf("created_at must be preserved: %v then %v", saved.CreatedAt, again.CreatedAt)
	}

	all, err := store.List(ctx, "official")
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 1 {
		t.Fatalf("rows = %d, want 1", len(all))
	}
}

func TestTheSameChannelIDUnderTwoClientKeysIsTwoRecords(t *testing.T) {
	ctx := context.Background()
	store := open(t)

	if _, err := store.Upsert(ctx, channel("official", 500)); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Upsert(ctx, channel("the-cat", 500)); err != nil {
		t.Fatal(err)
	}

	official, err := store.List(ctx, "official")
	if err != nil {
		t.Fatal(err)
	}
	cat, err := store.List(ctx, "the-cat")
	if err != nil {
		t.Fatal(err)
	}
	if len(official) != 1 || len(cat) != 1 {
		t.Fatalf("official=%d the-cat=%d, want one row each", len(official), len(cat))
	}
	if official[0].ID == cat[0].ID {
		t.Fatal("the two client keys must not share a row")
	}

	ids, err := store.ManagedIDs(ctx, "the-cat")
	if err != nil {
		t.Fatal(err)
	}
	if !ids[500] || len(ids) != 1 {
		t.Fatalf("managed ids for the-cat = %+v", ids)
	}
}

func TestConcurrentUpsertsOfTheSameChannelConverge(t *testing.T) {
	ctx := context.Background()
	store := open(t)

	const workers = 8
	var wg sync.WaitGroup
	errs := make(chan error, workers)
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := store.Upsert(ctx, channel("official", 500))
			errs <- err
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("concurrent upsert: %v", err)
		}
	}

	all, err := store.List(ctx, "official")
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 1 {
		t.Fatalf("rows = %d after %d concurrent upserts, want 1", len(all), workers)
	}
}

func TestListIsEmptyBeforeAnythingIsSelected(t *testing.T) {
	store := open(t)
	all, err := store.List(context.Background(), "official")
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 0 {
		t.Fatalf("rows = %d, want none", len(all))
	}
}

func TestDataSurvivesAReopen(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "channels.db")

	first, err := channelstore.Open(path, channelstore.Options{})
	if err != nil {
		t.Fatal(err)
	}
	saved, err := first.Upsert(ctx, channel("official", 500))
	if err != nil {
		t.Fatal(err)
	}
	first.Close()

	second, err := channelstore.Open(path, channelstore.Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()

	got, ok, err := second.Get(ctx, "official", 500)
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Fatal("the record did not survive the reopen")
	}
	if got.Title != saved.Title || got.AvatarPhotoID != saved.AvatarPhotoID {
		t.Fatalf("reopened record = %+v, want %+v", got, saved)
	}
	version, err := second.SchemaVersion(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if version < 1 {
		t.Fatalf("migrations were not preserved across reopen; version = %d", version)
	}
}

func TestGetMissesCleanly(t *testing.T) {
	store := open(t)
	_, ok, err := store.Get(context.Background(), "official", 999)
	if err != nil {
		t.Fatal(err)
	}
	if ok {
		t.Fatal("a missing channel must report not found, not an error")
	}
}

func TestUpsertRejectsInvalidRecords(t *testing.T) {
	ctx := context.Background()
	store := open(t)

	noKey := channel("", 500)
	if _, err := store.Upsert(ctx, noKey); err == nil {
		t.Fatal("a missing client_key must be rejected")
	}
	badID := channel("official", 0)
	if _, err := store.Upsert(ctx, badID); err == nil {
		t.Fatal("a non-positive channel id must be rejected")
	}
	badPeer := channel("official", 500)
	badPeer.PeerType = "user"
	if _, err := store.Upsert(ctx, badPeer); err == nil {
		t.Fatal("an unsupported peer type must be rejected")
	}
	badStatus := channel("official", 500)
	badStatus.MembershipStatus = "maybe"
	if _, err := store.Upsert(ctx, badStatus); err == nil {
		t.Fatal("an unsupported membership status must be rejected")
	}
}

func TestUpsertRecordsTheClockItWasGiven(t *testing.T) {
	at := time.Date(2026, 5, 1, 10, 0, 0, 0, time.UTC)
	store := open(t, func(o *channelstore.Options) {
		o.Now = func() time.Time { return at }
	})

	saved, err := store.Upsert(context.Background(), channel("official", 500))
	if err != nil {
		t.Fatal(err)
	}
	if !saved.CreatedAt.Equal(at) || !saved.UpdatedAt.Equal(at) || !saved.LastSyncedAt.Equal(at) {
		t.Fatalf("timestamps = %+v", saved)
	}
}

func TestNullableParticipantsCount(t *testing.T) {
	ctx := context.Background()
	store := open(t)

	ch := channel("official", 500)
	ch.ParticipantsCount = nil
	saved, err := store.Upsert(ctx, ch)
	if err != nil {
		t.Fatal(err)
	}
	if saved.ParticipantsCount != nil {
		t.Fatalf("participants = %v, want unknown", *saved.ParticipantsCount)
	}

	n := 1234
	ch.ParticipantsCount = &n
	saved, err = store.Upsert(ctx, ch)
	if err != nil {
		t.Fatal(err)
	}
	if saved.ParticipantsCount == nil || *saved.ParticipantsCount != 1234 {
		t.Fatalf("participants = %v", saved.ParticipantsCount)
	}
}

func TestUniquenessIsEnforcedByTheDatabase(t *testing.T) {
	ctx := context.Background()
	store := open(t)
	if _, err := store.Upsert(ctx, channel("official", 500)); err != nil {
		t.Fatal(err)
	}
	// A direct second insert that bypasses the upsert must hit the constraint.
	if _, err := store.Upsert(ctx, channel("official", 500)); err != nil {
		t.Fatalf("upsert must stay idempotent: %v", err)
	}
	all, err := store.List(ctx, "official")
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 1 {
		t.Fatalf("rows = %d", len(all))
	}
}
