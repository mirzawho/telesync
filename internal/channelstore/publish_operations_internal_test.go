package channelstore

import (
	"context"
	"path/filepath"
	"testing"
)

// A database created before migration 7 must still open, keep everything it
// had, and gain the publish-operation table on upgrade.

func TestMigrationsUpgradeASixDatabase(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "state", "channels.db")

	old := openInternalAt(t, path, 6)
	user, err := old.CreateUser(ctx, "Operator", "operator", "$argon2id$v=19$m=1,t=1,p=1$c2FsdA$aGFzaA", true)
	if err != nil {
		t.Fatal(err)
	}
	ch, err := old.Upsert(ctx, Channel{
		ClientKey:        "official",
		ChannelID:        123,
		PeerType:         PeerChannel,
		Title:            "Kept",
		MembershipStatus: StatusMember,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := old.Close(); err != nil {
		t.Fatal(err)
	}

	upgraded, err := Open(path, Options{})
	if err != nil {
		t.Fatalf("upgrade: %v", err)
	}
	t.Cleanup(func() { _ = upgraded.Close() })

	version, err := upgraded.SchemaVersion(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if version != len(migrations) {
		t.Fatalf("schema version = %d, want %d", version, len(migrations))
	}

	got, ok, err := upgraded.Get(ctx, "official", 123)
	if err != nil || !ok || got.Title != "Kept" {
		t.Fatalf("the channel must survive the upgrade: %+v ok=%v err=%v", got, ok, err)
	}
	if _, ok, err := upgraded.GetUser(ctx, user.ID); err != nil || !ok {
		t.Fatalf("the account must survive the upgrade: ok=%v err=%v", ok, err)
	}

	op, created, err := upgraded.BeginPublishOperation(ctx, PublishOperation{
		UserID:          user.ID,
		IdempotencyKey:  "key-1",
		RequestHash:     "hash-a",
		DestinationType: "telegram",
		DestinationID:   "123",
	})
	if err != nil || !created {
		t.Fatalf("the new table must accept an attempt: created=%v err=%v", created, err)
	}
	if op.ID == 0 {
		t.Fatal("the attempt must have an id")
	}
	if _, err := upgraded.Upsert(ctx, ch); err != nil {
		t.Fatal(err)
	}
}
