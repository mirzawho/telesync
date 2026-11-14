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
	// The row is written with the columns a version-6 schema has: the
	// repository writes every current column, which this schema does not have.
	if _, err := old.db.ExecContext(ctx, `INSERT INTO managed_channels
		(client_key, channel_id, peer_type, title, membership_status, created_at, updated_at, last_synced_at)
		VALUES ('official', 123, 'channel', 'Kept', 'member', 'now', 'now', 'now')`); err != nil {
		t.Fatalf("seed v6: %v", err)
	}
	ch, ok, err := old.Get(ctx, "official", 123)
	if err != nil || !ok {
		t.Fatalf("read the seeded channel back: ok=%v err=%v", ok, err)
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

func TestMigrationTwentyThreeScopesPublishKeysAndPreservesLegacyRows(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "state", "channels.db")
	old := openInternalAt(t, path, 22)
	user, err := old.CreateUser(ctx, "Operator", "operator", "$argon2id$v=19$m=1,t=1,p=1$c2FsdA$aGFzaA", true)
	if err != nil {
		t.Fatal(err)
	}
	accountID := "acc_aaaaaaaaaaaaaaaaaaaaaaaaaa"
	if _, err := old.Create(ctx, Account{
		ID: accountID, OwnerUserID: user.ID, ServerKey: "official",
		NetworkType: NetworkTelegram, RemoteUserID: 991,
		Status: AccountStatusConnected, SessionEncKeyVersion: 1,
	}); err != nil {
		t.Fatal(err)
	}
	for _, row := range []struct {
		key, destination string
	}{
		{"account-op", accountID + ":official:555"},
		{"unresolved-legacy-op", "official:555"},
	} {
		if _, err := old.db.ExecContext(ctx, `INSERT INTO publish_operations
			(user_id, idempotency_key, request_hash, destination_type, destination_id,
			 status, result, error_code, created_at, updated_at, account_id)
			VALUES (?, ?, 'hash', 'telegram', ?, 'succeeded', '{"message":{"id":7}}', '', 'now', 'now', NULL)`,
			user.ID, row.key, row.destination); err != nil {
			t.Fatalf("seed pre-v23 operation %q: %v", row.key, err)
		}
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
	if err != nil || version != 23 {
		t.Fatalf("schema version = %d, err=%v; want 23", version, err)
	}
	bound, ok, err := upgraded.GetPublishOperationForAccount(ctx, user.ID, accountID, "account-op")
	if err != nil || !ok || bound.AccountID != accountID || bound.Status != PublishSucceeded {
		t.Fatalf("bound operation = %+v, ok=%v err=%v", bound, ok, err)
	}
	legacy, ok, err := upgraded.GetPublishOperationForAccount(ctx, user.ID, "", "unresolved-legacy-op")
	if err != nil || !ok || legacy.AccountID != "" {
		t.Fatalf("unresolved legacy operation = %+v, ok=%v err=%v", legacy, ok, err)
	}
}
