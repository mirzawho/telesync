package channelstore

import (
	"context"
	"path/filepath"
	"testing"
)

// These tests reach the database handle directly, because the repository
// validates before it writes and the point here is what the schema itself
// refuses.

func openInternal(t *testing.T) *Store {
	t.Helper()
	store, err := Open(filepath.Join(t.TempDir(), "schema.db"), Options{})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return store
}

func TestSchemaRejectsADuplicateNameIgnoringCase(t *testing.T) {
	ctx := context.Background()
	store := openInternal(t)
	insert := `INSERT INTO categories (name, description, created_at, updated_at)
		VALUES (?, '', '2026-01-01T00:00:00Z', '2026-01-01T00:00:00Z')`
	if _, err := store.db.ExecContext(ctx, insert, "News"); err != nil {
		t.Fatal(err)
	}
	if _, err := store.db.ExecContext(ctx, insert, "NEWS"); err == nil {
		t.Fatal("the schema accepted a case-insensitive duplicate name")
	}
}

func TestSchemaRejectsABlankCategoryName(t *testing.T) {
	store := openInternal(t)
	if _, err := store.db.ExecContext(context.Background(),
		`INSERT INTO categories (name, description, created_at, updated_at)
		 VALUES ('   ', '', '2026-01-01T00:00:00Z', '2026-01-01T00:00:00Z')`,
	); err == nil {
		t.Fatal("the schema accepted a whitespace-only name")
	}
}

func TestSchemaRejectsAnAssignmentToAMissingCategory(t *testing.T) {
	store := openInternal(t)
	// Only the ON DELETE CASCADE foreign key can refuse an assignment to a
	// category that was never created, which also proves enforcement is on.
	if _, err := store.db.ExecContext(context.Background(),
		`INSERT INTO channel_categories (client_key, channel_id, category_id, created_at)
		 VALUES ('official', '100', 999, '2026-01-01T00:00:00Z')`,
	); err == nil {
		t.Fatal("the schema accepted an assignment to a nonexistent category")
	}
}

func TestForeignKeyEnforcementIsOnEveryConnection(t *testing.T) {
	ctx := context.Background()
	store := openInternal(t)
	// The pool is capped at one connection so the pragma cannot be missing on
	// some other connection. Pin that invariant.
	if stats := store.db.Stats(); stats.MaxOpenConnections != 1 {
		t.Fatalf("MaxOpenConnections = %d, want 1", stats.MaxOpenConnections)
	}
	var on int
	if err := store.db.QueryRowContext(ctx, `PRAGMA foreign_keys`).Scan(&on); err != nil {
		t.Fatal(err)
	}
	if on != 1 {
		t.Fatalf("PRAGMA foreign_keys = %d, want 1", on)
	}
}

func TestMigrationsUpgradeAVersionOneDatabase(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "old.db")

	// Build a database as the previous release left it: migration 1 applied and
	// one managed channel recorded.
	old := openInternalAt(t, path, 1)
	// The row is written with the version-1 columns only: the repository writes
	// every current column, which a version-1 schema does not have.
	if _, err := old.db.ExecContext(ctx, `INSERT INTO managed_channels
		(client_key, channel_id, peer_type, title, membership_status, created_at, updated_at, last_synced_at)
		VALUES ('official', 1234567890, 'channel', 'Example', 'member',
		'2026-01-01T00:00:00Z', '2026-01-01T00:00:00Z', '2026-01-01T00:00:00Z')`); err != nil {
		t.Fatalf("seed v1: %v", err)
	}
	if version, _ := old.SchemaVersion(ctx); version != 1 {
		t.Fatalf("seeded version = %d, want 1", version)
	}
	if err := old.Close(); err != nil {
		t.Fatal(err)
	}

	upgraded, err := Open(path, Options{})
	if err != nil {
		t.Fatalf("upgrade: %v", err)
	}
	defer upgraded.Close()
	if version, _ := upgraded.SchemaVersion(ctx); version != len(migrations) {
		t.Fatalf("version after upgrade = %d, want %d", version, len(migrations))
	}
	if _, ok, err := upgraded.Get(ctx, "official", 1234567890); err != nil || !ok {
		t.Fatalf("existing channel lost in upgrade: ok=%v err=%v", ok, err)
	}
	cat, err := upgraded.CreateCategory(ctx, "Technology", "")
	if err != nil {
		t.Fatalf("categories unavailable after upgrade: %v", err)
	}
	if _, err := upgraded.AssignChannel(ctx, "official", "1234567890", cat.ID); err != nil {
		t.Fatalf("assignment unavailable after upgrade: %v", err)
	}
	if err := upgraded.DeleteCategory(ctx, cat.ID); err != nil {
		t.Fatal(err)
	}
	if _, ok, err := upgraded.Get(ctx, "official", 1234567890); err != nil || !ok {
		t.Fatalf("channel lost when deleting a category: ok=%v err=%v", ok, err)
	}
}

// openInternalAt opens a database holding only the migrations up to version,
// so an upgrade from an older release can be exercised.
func openInternalAt(t *testing.T, path string, version int) *Store {
	t.Helper()
	saved := migrations
	migrations = saved[:version]
	defer func() { migrations = saved }()
	store, err := Open(path, Options{})
	if err != nil {
		t.Fatalf("Open at version %d: %v", version, err)
	}
	return store
}

func TestSchemaRefusesADuplicateAssignment(t *testing.T) {
	ctx := context.Background()
	store := openInternal(t)
	if _, err := store.db.ExecContext(ctx,
		`INSERT INTO users (fullname, username, password_hash, is_active, created_at, updated_at)
		 VALUES ('A', 'a', 'h', 1, 'now', 'now')`); err != nil {
		t.Fatal(err)
	}
	if _, err := store.db.ExecContext(ctx,
		`INSERT INTO managed_channels (client_key, channel_id, peer_type, membership_status, created_at, updated_at, last_synced_at)
		 VALUES ('official', 42, 'channel', 'member', 'now', 'now', 'now')`); err != nil {
		t.Fatal(err)
	}
	insert := `INSERT INTO user_channels_permission (user_id, channel_id, permission, effect, created_at, updated_at)
		VALUES (1, 1, 'view', ?, 'now', 'now')`
	if _, err := store.db.ExecContext(ctx, insert, "allow"); err != nil {
		t.Fatal(err)
	}
	if _, err := store.db.ExecContext(ctx, insert, "deny"); err == nil {
		t.Fatal("the schema accepted two assignments for the same user, channel and permission")
	}
}

func TestSchemaRefusesAnUnknownPermissionOrEffect(t *testing.T) {
	ctx := context.Background()
	store := openInternal(t)
	if _, err := store.db.ExecContext(ctx,
		`INSERT INTO users (fullname, username, password_hash, is_active, created_at, updated_at)
		 VALUES ('A', 'a', 'h', 1, 'now', 'now')`); err != nil {
		t.Fatal(err)
	}
	if _, err := store.db.ExecContext(ctx,
		`INSERT INTO categories (name, description, created_at, updated_at) VALUES ('C', '', 'now', 'now')`); err != nil {
		t.Fatal(err)
	}
	for _, stmt := range []string{
		`INSERT INTO user_categories_permission (user_id, category_id, permission, effect, created_at, updated_at) VALUES (1, 1, 'publish', 'allow', 'now', 'now')`,
		`INSERT INTO user_categories_permission (user_id, category_id, permission, effect, created_at, updated_at) VALUES (1, 1, 'view', 'maybe', 'now', 'now')`,
		`INSERT INTO user_permissions (user_id, permission, created_at, updated_at) VALUES (1, '  ', 'now', 'now')`,
		`INSERT INTO user_channels_permission (user_id, channel_id, permission, effect, created_at, updated_at) VALUES (1, 99, 'view', 'allow', 'now', 'now')`,
	} {
		if _, err := store.db.ExecContext(ctx, stmt); err == nil {
			t.Errorf("the schema accepted %q", stmt)
		}
	}
}
