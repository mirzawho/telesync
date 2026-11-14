package channelstore

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
)

// These tests reach the database handle directly, as schema_internal_test.go
// does, because the point is what migration 21's schema itself enforces and
// what a rebuild of managed_channels carries across. The repository-level
// behaviour of the account and login stores is covered by their own tests.

func seedVersionTwenty(t *testing.T, path string) {
	t.Helper()
	ctx := context.Background()
	old := openInternalAt(t, path, 20)
	stmts := []string{
		`INSERT INTO users (id, fullname, username, password_hash, is_active, created_at, updated_at)
			VALUES (7, 'Operator', 'operator', 'hash', 1, '2026-01-01T00:00:00Z', '2026-01-01T00:00:00Z')`,
		`INSERT INTO managed_channels (id, client_key, channel_id, peer_type, title, membership_status,
			participants_count, created_at, updated_at, last_synced_at)
			VALUES (101, 'official', 111, 'channel', 'News', 'member', 10,
			'2026-01-01T00:00:00Z', '2026-01-01T00:00:00Z', '2026-01-01T00:00:00Z')`,
		`INSERT INTO managed_channels (id, client_key, channel_id, peer_type, title, membership_status,
			created_at, updated_at, last_synced_at)
			VALUES (102, 'cat', 222, 'group', 'Chat', 'member',
			'2026-01-01T00:00:00Z', '2026-01-01T00:00:00Z', '2026-01-01T00:00:00Z')`,
		`INSERT INTO user_channels_permission (id, user_id, channel_id, permission, effect, created_at, updated_at)
			VALUES (501, 7, 101, 'view', 'allow', '2026-01-01T00:00:00Z', '2026-01-01T00:00:00Z')`,
		`INSERT INTO categories (id, name, description, created_at, updated_at)
			VALUES (31, 'Technology', '', '2026-01-01T00:00:00Z', '2026-01-01T00:00:00Z')`,
		`INSERT INTO channel_categories (client_key, channel_id, category_id, created_at)
			VALUES ('official', '111', 31, '2026-01-01T00:00:00Z')`,
		`INSERT INTO workflows (id, managed_channel_id, enabled, created_by, created_at, updated_at)
			VALUES (41, 101, 1, 7, '2026-01-01T00:00:00Z', '2026-01-01T00:00:00Z')`,
		`INSERT INTO automation_events (id, type, network_key, source_client_key, managed_channel_id,
			telegram_channel_id, message_id, occurred_at, received_at, created_at)
			VALUES ('evt-1', 'message', 'telegram', 'official', 101, 111, 5,
			'2026-01-01T00:00:00Z', '2026-01-01T00:00:00Z', '2026-01-01T00:00:00Z')`,
		`INSERT INTO automation_jobs (id, event_id, job_type, target_id, status, max_attempts,
			available_at, created_at, updated_at)
			VALUES (61, 'evt-1', 'webhook_delivery', 1, 'pending', 5,
			'2026-01-01T00:00:00Z', '2026-01-01T00:00:00Z', '2026-01-01T00:00:00Z')`,
		`INSERT INTO webhooks (id, name, url, method, auth_mode, created_by, created_at, updated_at)
			VALUES (71, 'Forward', 'https://example.test/hook', 'POST', 'none', 7,
			'2026-01-01T00:00:00Z', '2026-01-01T00:00:00Z')`,
		`INSERT INTO webhook_sources (webhook_id, managed_channel_id, created_at)
			VALUES (71, 101, '2026-01-01T00:00:00Z')`,
	}
	for _, stmt := range stmts {
		if _, err := old.db.ExecContext(ctx, stmt); err != nil {
			t.Fatalf("seed version 20: %v\n%s", err, stmt)
		}
	}
	if err := old.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestMigrationTwentyTwoIsRecordedOnAFreshDatabase(t *testing.T) {
	ctx := context.Background()
	store := openInternal(t)
	version, err := store.SchemaVersion(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if version != 23 {
		t.Fatalf("schema version = %d, want 23", version)
	}
	for _, table := range []string{"telegram_accounts", "login_attempts", "legacy_session_adoptions"} {
		var name string
		err := store.db.QueryRowContext(ctx,
			`SELECT name FROM sqlite_master WHERE type = 'table' AND name = ?`, table).Scan(&name)
		if err != nil {
			t.Fatalf("table %s after migration 22: %v", table, err)
		}
	}
}

func seedVersionTwentyOneWorkflowDestinations(t *testing.T, path string) {
	t.Helper()
	ctx := context.Background()
	store := openInternalAt(t, path, 21)
	stmts := []string{
		`INSERT INTO users (id, fullname, username, password_hash, created_at, updated_at)
			VALUES (1, 'Owner', 'owner', 'hash', 'now', 'now')`,
		`INSERT INTO users (id, fullname, username, password_hash, created_at, updated_at)
			VALUES (2, 'Other', 'other', 'hash', 'now', 'now')`,
		`INSERT INTO telegram_accounts (id, owner_user_id, server_key, network_type, remote_user_id, status, created_at, updated_at)
			VALUES ('acc_source', 1, 'official', 'telegram', 1, 'connected', 'now', 'now')`,
		`INSERT INTO telegram_accounts (id, owner_user_id, server_key, network_type, remote_user_id, status, created_at, updated_at)
			VALUES ('acc_destination_a', 1, 'official', 'telegram', 2, 'connected', 'now', 'now')`,
		`INSERT INTO telegram_accounts (id, owner_user_id, server_key, network_type, remote_user_id, status, created_at, updated_at)
			VALUES ('acc_destination_b', 1, 'official', 'telegram', 3, 'disconnected', 'now', 'now')`,
		`INSERT INTO telegram_accounts (id, owner_user_id, server_key, network_type, remote_user_id, status, created_at, updated_at)
			VALUES ('acc_other_owner', 2, 'official', 'telegram', 4, 'connected', 'now', 'now')`,
		`INSERT INTO managed_channels (id, client_key, channel_id, peer_type, membership_status, created_at, updated_at, last_synced_at, account_id)
			VALUES (101, 'official', 100, 'channel', 'member', 'now', 'now', 'now', 'acc_source')`,
		`INSERT INTO managed_channels (id, client_key, channel_id, peer_type, membership_status, created_at, updated_at, last_synced_at, account_id)
			VALUES (201, 'official', 900, 'channel', 'member', 'now', 'now', 'now', 'acc_destination_a')`,
		`INSERT INTO managed_channels (id, client_key, channel_id, peer_type, membership_status, created_at, updated_at, last_synced_at, account_id)
			VALUES (202, 'official', 900, 'channel', 'member', 'now', 'now', 'now', 'acc_destination_b')`,
		`INSERT INTO managed_channels (id, client_key, channel_id, peer_type, membership_status, created_at, updated_at, last_synced_at, account_id)
			VALUES (203, 'official', 900, 'channel', 'member', 'now', 'now', 'now', 'acc_other_owner')`,
		`INSERT INTO managed_channels (id, client_key, channel_id, peer_type, membership_status, created_at, updated_at, last_synced_at, account_id)
			VALUES (204, 'official', 901, 'channel', 'member', 'now', 'now', 'now', 'acc_destination_a')`,
		`INSERT INTO categories (id, name, description, created_at, updated_at)
			VALUES (61, 'Migration category', '', 'now', 'now')`,
		`INSERT INTO channel_categories (client_key, channel_id, category_id, created_at)
			VALUES ('official', 900, 61, '2026-01-02T03:04:05Z')`,
		`INSERT INTO channel_categories (client_key, channel_id, category_id, created_at)
			VALUES ('official', 901, 61, '2026-01-02T03:05:06Z')`,
		`INSERT INTO workflows (id, managed_channel_id, enabled, created_by, created_at, updated_at, account_id, owner_user_id)
			VALUES (41, 101, 1, 1, 'now', 'now', 'acc_source', 1)`,
		`INSERT INTO automation_events (id, type, network_key, source_client_key, managed_channel_id, telegram_channel_id, message_id, occurred_at, received_at, created_at, account_id)
			VALUES ('evt_workflow', 'message', 'telegram', 'official', 101, 100, 5, 'now', 'now', 'now', 'acc_source')`,
		`INSERT INTO workflow_runs (id, workflow_id, event_id, status, source_message_id, ai_status, created_at)
			VALUES (51, 41, 'evt_workflow', 'queued', 5, 'not_used', 'now')`,
		`INSERT INTO workflow_destinations (id, workflow_id, destination_type, client_key, channel_id, site_id, created_at, updated_at)
			VALUES (301, 41, 'telegram', 'official', 900, 0, 'now', 'now')`,
		`INSERT INTO workflow_destinations (id, workflow_id, destination_type, client_key, channel_id, site_id, created_at, updated_at)
			VALUES (302, 41, 'telegram', 'official', 901, 0, 'now', 'now')`,
		`INSERT INTO workflow_destinations (id, workflow_id, destination_type, client_key, channel_id, site_id, created_at, updated_at)
			VALUES (303, 41, 'wordpress', '', 0, 77, 'now', 'now')`,
		`INSERT INTO workflow_publications (id, workflow_run_id, destination_type, client_key, channel_id, site_id, status, created_at)
			VALUES (401, 51, 'telegram', 'official', 900, 0, 'published', 'now')`,
		`INSERT INTO workflow_publications (id, workflow_run_id, destination_type, client_key, channel_id, site_id, status, created_at)
			VALUES (402, 51, 'telegram', 'official', 901, 0, 'published', 'now')`,
		`INSERT INTO workflow_publications (id, workflow_run_id, destination_type, client_key, channel_id, site_id, status, created_at)
			VALUES (403, 51, 'wordpress', '', 0, 77, 'published', 'now')`,
	}
	for _, stmt := range stmts {
		if _, err := store.db.ExecContext(ctx, stmt); err != nil {
			t.Fatalf("seed version 21 workflow data: %v\n%s", err, stmt)
		}
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestMigrationTwentyTwoScopesWorkflowDestinationsAndPreservesPublicationHistory(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "workflow-destinations.db")
	seedVersionTwentyOneWorkflowDestinations(t, path)

	store, err := Open(path, Options{})
	if err != nil {
		t.Fatalf("upgrade 21 to 22: %v", err)
	}
	defer store.Close()
	if version, err := store.SchemaVersion(ctx); err != nil || version != 23 {
		t.Fatalf("schema version = %d, err=%v; want 23", version, err)
	}

	for _, tc := range []struct {
		table string
		id    int64
		want  sql.NullString
	}{
		{"workflow_destinations", 301, sql.NullString{}}, // two accounts of the owner hold channel 900
		{"workflow_destinations", 302, sql.NullString{String: "acc_destination_a", Valid: true}},
		{"workflow_destinations", 303, sql.NullString{}}, // WordPress remains accountless
		{"workflow_publications", 401, sql.NullString{}}, // ambiguous target history stays unbound
		{"workflow_publications", 402, sql.NullString{String: "acc_destination_a", Valid: true}},
		{"workflow_publications", 403, sql.NullString{}},
	} {
		var id int64
		var account sql.NullString
		if err := store.db.QueryRowContext(ctx,
			`SELECT id, account_id FROM `+tc.table+` WHERE id = ?`, tc.id).Scan(&id, &account); err != nil {
			t.Fatalf("%s row %d: %v", tc.table, tc.id, err)
		}
		if id != tc.id || account != tc.want {
			t.Errorf("%s row = (id %d, account_id %v), want (id %d, account_id %v)",
				tc.table, id, account, tc.id, tc.want)
		}
	}
	for _, tc := range []struct {
		channelID int64
		accountID string
		createdAt string
	}{
		{900, "legacy_official", "2026-01-02T03:04:05Z"}, // account mapping is ambiguous
		{901, "acc_destination_a", "2026-01-02T03:05:06Z"},
	} {
		var accountID, createdAt string
		if err := store.db.QueryRowContext(ctx, `
			SELECT account_id, created_at FROM channel_categories
			WHERE client_key = 'official' AND channel_id = ? AND category_id = 61`, tc.channelID).
			Scan(&accountID, &createdAt); err != nil {
			t.Fatalf("category assignment for channel %d: %v", tc.channelID, err)
		}
		if accountID != tc.accountID || createdAt != tc.createdAt {
			t.Errorf("channel %d assignment = (%q, %q), want (%q, %q)",
				tc.channelID, accountID, createdAt, tc.accountID, tc.createdAt)
		}
	}
	var quarantinedOwner sql.NullInt64
	var quarantinedStatus, quarantinedNetwork string
	if err := store.db.QueryRowContext(ctx,
		`SELECT owner_user_id, status, network_type FROM telegram_accounts WHERE id = 'legacy_official'`).
		Scan(&quarantinedOwner, &quarantinedStatus, &quarantinedNetwork); err != nil {
		t.Fatalf("fallback quarantine account: %v", err)
	}
	if quarantinedOwner.Valid || quarantinedStatus != AccountStatusQuarantined || quarantinedNetwork != "" {
		t.Fatalf("fallback account = (owner %v, status %q, network %q), want ownerless quarantine with unknown network", quarantinedOwner, quarantinedStatus, quarantinedNetwork)
	}

	// The new destination rows can distinguish two accounts that publish to
	// the same remote channel, while new Telegram destinations cannot omit the
	// account and WordPress cannot carry one.
	for _, stmt := range []string{
		`INSERT INTO workflow_destinations (workflow_id, destination_type, client_key, channel_id, site_id, account_id, created_at, updated_at)
			VALUES (41, 'telegram', 'official', 900, 0, 'acc_destination_a', 'now', 'now')`,
		`INSERT INTO workflow_destinations (workflow_id, destination_type, client_key, channel_id, site_id, account_id, created_at, updated_at)
			VALUES (41, 'telegram', 'official', 900, 0, 'acc_destination_b', 'now', 'now')`,
		`INSERT INTO workflow_destinations (workflow_id, destination_type, client_key, channel_id, site_id, created_at, updated_at)
			VALUES (41, 'wordpress', '', 0, 77, 'now', 'now')`,
	} {
		if _, err := store.db.ExecContext(ctx, stmt); err != nil {
			t.Fatalf("valid workflow destination rejected: %v\n%s", err, stmt)
		}
	}
	for _, stmt := range []string{
		`INSERT INTO workflow_destinations (workflow_id, destination_type, client_key, channel_id, site_id, created_at, updated_at)
			VALUES (41, 'telegram', 'official', 900, 0, 'now', 'now')`,
		`INSERT INTO workflow_destinations (workflow_id, destination_type, client_key, channel_id, site_id, account_id, created_at, updated_at)
			VALUES (41, 'wordpress', '', 0, 77, 'acc_destination_a', 'now', 'now')`,
	} {
		if _, err := store.db.ExecContext(ctx, stmt); err == nil {
			t.Errorf("invalid workflow destination accepted: %s", stmt)
		}
	}

	// Legacy NULL Telegram history retains its prior dedupe behavior. Bound
	// histories for two different account destinations remain distinct.
	duplicateLegacy := `INSERT INTO workflow_publications
		(workflow_run_id, destination_type, client_key, channel_id, site_id, status, created_at)
		VALUES (51, 'telegram', 'official', 900, 0, 'published', 'now')`
	if _, err := store.db.ExecContext(ctx, duplicateLegacy); err == nil {
		t.Fatal("the migration allowed duplicate legacy NULL-account publication history")
	}
	for _, accountID := range []string{"acc_destination_a", "acc_destination_b"} {
		if _, err := store.db.ExecContext(ctx, `INSERT INTO workflow_publications
			(workflow_run_id, destination_type, client_key, channel_id, site_id, account_id, status, created_at)
			VALUES (51, 'telegram', 'official', 900, 0, ?, 'published', 'now')`, accountID); err != nil {
			t.Fatalf("publication for account %s was rejected: %v", accountID, err)
		}
	}
	if _, err := store.db.ExecContext(ctx, `INSERT INTO workflow_publications
		(workflow_run_id, destination_type, client_key, channel_id, site_id, account_id, status, created_at)
		VALUES (51, 'telegram', 'official', 900, 0, 'acc_destination_a', 'published', 'now')`); err == nil {
		t.Fatal("the migration allowed duplicate publication history for one account")
	}
}

func TestMigrationsTwentyOneAndTwentyTwoPreserveExistingRowsAndRelationships(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "upgrade.db")
	seedVersionTwenty(t, path)

	store, err := Open(path, Options{})
	if err != nil {
		t.Fatalf("upgrade to latest schema (23): %v", err)
	}
	defer store.Close()
	if version, _ := store.SchemaVersion(ctx); version != 23 {
		t.Fatalf("schema version = %d, want 23", version)
	}

	// Every id survives the rebuild of managed_channels, so the foreign keys
	// that name those ids still resolve.
	counts := map[string]int{
		`SELECT COUNT(*) FROM managed_channels WHERE id IN (101, 102)`:                            2,
		`SELECT COUNT(*) FROM user_channels_permission WHERE id = 501 AND channel_id = 101`:       1,
		`SELECT COUNT(*) FROM channel_categories WHERE category_id = 31`:                          1,
		`SELECT COUNT(*) FROM workflows WHERE id = 41 AND managed_channel_id = 101`:               1,
		`SELECT COUNT(*) FROM automation_events WHERE id = 'evt-1' AND managed_channel_id = 101`:  1,
		`SELECT COUNT(*) FROM automation_jobs WHERE id = 61 AND event_id = 'evt-1'`:               1,
		`SELECT COUNT(*) FROM webhook_sources WHERE webhook_id = 71 AND managed_channel_id = 101`: 1,
	}
	for query, want := range counts {
		var got int
		if err := store.db.QueryRowContext(ctx, query).Scan(&got); err != nil {
			t.Fatalf("%s: %v", query, err)
		}
		if got != want {
			t.Errorf("%s = %d, want %d", query, got, want)
		}
	}

	var paused int
	if err := store.db.QueryRowContext(ctx, `SELECT paused FROM automation_jobs WHERE id = 61`).Scan(&paused); err != nil {
		t.Fatal(err)
	}
	if paused != 1 {
		t.Errorf("legacy ownerless job paused = %d, want 1", paused)
	}

	// The rebuild must leave no dangling reference behind.
	rows, err := store.db.QueryContext(ctx, `PRAGMA foreign_key_check`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		t.Error("a foreign key violation remains after migration 21")
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}

	// The copy keeps every column it inherited, not just the ones the old
	// uniqueness constraint named.
	var title string
	var participants int
	if err := store.db.QueryRowContext(ctx,
		`SELECT title, participants_count FROM managed_channels WHERE id = 101`).Scan(&title, &participants); err != nil {
		t.Fatal(err)
	}
	if title != "News" || participants != 10 {
		t.Errorf("channel 101 = (%q, %d), want (\"News\", 10)", title, participants)
	}

	// Every legacy channel is moved onto the quarantined account of the client
	// key it was registered under: it is never left without an account, and
	// never handed to a user by guessing who owns it.
	for _, row := range []struct {
		channelID int64
		account   string
	}{{101, "legacy_official"}, {102, "legacy_cat"}} {
		var got string
		if err := store.db.QueryRowContext(ctx,
			`SELECT account_id FROM managed_channels WHERE id = ?`, row.channelID).Scan(&got); err != nil {
			t.Fatal(err)
		}
		if got != row.account {
			t.Errorf("channel %d account_id = %q, want %q", row.channelID, got, row.account)
		}
		var owner sql.NullInt64
		var status string
		if err := store.db.QueryRowContext(ctx,
			`SELECT owner_user_id, status FROM telegram_accounts WHERE id = ?`, row.account).Scan(&owner, &status); err != nil {
			t.Fatalf("quarantined account %s: %v", row.account, err)
		}
		if owner.Valid || status != "quarantined" {
			t.Errorf("account %s = (owner %v, %q), want no owner and quarantined", row.account, owner, status)
		}
	}
}

func TestAccountDeletePausesProcessingJobs(t *testing.T) {
	ctx := context.Background()
	store := openInternal(t)
	for _, stmt := range []string{
		`INSERT INTO users (id, fullname, username, password_hash, created_at, updated_at)
			VALUES (1, 'One', 'one', 'h', 'now', 'now')`,
		`INSERT INTO telegram_accounts (id, owner_user_id, server_key, network_type, status, created_at, updated_at)
			VALUES ('acc_one', 1, 'official', 'telegram', 'connected', 'now', 'now')`,
		`INSERT INTO automation_jobs (event_id, job_type, target_id, status, max_attempts,
			available_at, created_at, updated_at, account_id, owner_user_id, paused)
			VALUES ('send-one', 'telegram_message_send', 1, 'processing', 5,
			'now', 'now', 'now', 'acc_one', 1, 0)`,
	} {
		if _, err := store.db.ExecContext(ctx, stmt); err != nil {
			t.Fatal(err)
		}
	}

	if err := store.Delete(ctx, 1, "acc_one"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	var paused int
	if err := store.db.QueryRowContext(ctx,
		`SELECT paused FROM automation_jobs WHERE event_id = 'send-one'`).Scan(&paused); err != nil {
		t.Fatal(err)
	}
	if paused != 1 {
		t.Fatalf("processing job paused = %d, want 1", paused)
	}
}

func TestLegacyAdoptionSchemaRejectsDuplicateAndDanglingRecords(t *testing.T) {
	ctx := context.Background()
	store := openInternal(t)
	for _, stmt := range []string{
		`INSERT INTO users (id, fullname, username, password_hash, created_at, updated_at)
			VALUES (1, 'One', 'one', 'h', 'now', 'now')`,
		`INSERT INTO telegram_accounts (id, owner_user_id, server_key, network_type, status, created_at, updated_at)
			VALUES ('acc_one', 1, 'official', 'telegram', 'connected', 'now', 'now')`,
		`INSERT INTO legacy_session_adoptions (legacy_key, account_id, adopted_by, adopted_at, reason)
			VALUES ('official', 'acc_one', 1, '2026-01-01T00:00:00Z', 'confirmed')`,
	} {
		if _, err := store.db.ExecContext(ctx, stmt); err != nil {
			t.Fatal(err)
		}
	}
	for _, stmt := range []string{
		`INSERT INTO legacy_session_adoptions (legacy_key, account_id, adopted_by, adopted_at, reason)
			VALUES ('official', 'missing-account', 1, '2026-01-02T00:00:00Z', 'bad target')`,
		`INSERT INTO legacy_session_adoptions (legacy_key, account_id, adopted_by, adopted_at, reason)
			VALUES ('cat', 'acc_one', 99, '2026-01-02T00:00:00Z', 'bad operator')`,
		`INSERT INTO legacy_session_adoptions (legacy_key, account_id, adopted_by, adopted_at, reason)
			VALUES ('official', 'acc_one', 1, '2026-01-01T00:00:00Z', 'confirmed')`,
	} {
		if _, err := store.db.ExecContext(ctx, stmt); err == nil {
			t.Errorf("schema accepted invalid adoption: %s", stmt)
		}
	}
}

func TestManagedChannelUniquenessIsAccountScoped(t *testing.T) {
	ctx := context.Background()
	store := openInternal(t)
	for _, stmt := range []string{
		`INSERT INTO users (id, fullname, username, password_hash, created_at, updated_at)
			VALUES (1, 'One', 'one', 'h', 'now', 'now')`,
		`INSERT INTO users (id, fullname, username, password_hash, created_at, updated_at)
			VALUES (2, 'Two', 'two', 'h', 'now', 'now')`,
		`INSERT INTO telegram_accounts (id, owner_user_id, server_key, network_type, status, created_at, updated_at)
			VALUES ('acc_a', 1, 'official', 'telegram', 'connected', 'now', 'now')`,
		`INSERT INTO telegram_accounts (id, owner_user_id, server_key, network_type, status, created_at, updated_at)
			VALUES ('acc_b', 1, 'official', 'telegram', 'connected', 'now', 'now')`,
		`INSERT INTO telegram_accounts (id, owner_user_id, server_key, network_type, status, created_at, updated_at)
			VALUES ('acc_c', 2, 'official', 'telegram', 'connected', 'now', 'now')`,
	} {
		if _, err := store.db.ExecContext(ctx, stmt); err != nil {
			t.Fatal(err)
		}
	}

	// Two accounts on the same server may register the same remote channel:
	// that is the whole point of the v0.0.5 ownership model.
	insert := `INSERT INTO managed_channels (account_id, client_key, channel_id, peer_type, membership_status, created_at, updated_at, last_synced_at)
		VALUES (?, 'official', 900, 'channel', 'member', 'now', 'now', 'now')`
	if _, err := store.db.ExecContext(ctx, insert, "acc_a"); err != nil {
		t.Fatalf("first registration: %v", err)
	}
	if _, err := store.db.ExecContext(ctx, insert, "acc_b"); err != nil {
		t.Fatalf("a second account on the same server may not register the same channel: %v", err)
	}
	if _, err := store.db.ExecContext(ctx, insert, "acc_c"); err != nil {
		t.Fatalf("an account of another owner may not register the same channel: %v", err)
	}
	if _, err := store.db.ExecContext(ctx, insert, "acc_a"); err == nil {
		t.Fatal("the schema accepted the same channel twice for one account")
	}
}

func TestRemoteIdentityUniquenessIsOwnerScoped(t *testing.T) {
	ctx := context.Background()
	store := openInternal(t)
	for _, stmt := range []string{
		`INSERT INTO users (id, fullname, username, password_hash, created_at, updated_at)
			VALUES (1, 'One', 'one', 'h', 'now', 'now')`,
		`INSERT INTO users (id, fullname, username, password_hash, created_at, updated_at)
			VALUES (2, 'Two', 'two', 'h', 'now', 'now')`,
	} {
		if _, err := store.db.ExecContext(ctx, stmt); err != nil {
			t.Fatal(err)
		}
	}

	// Two different owners authenticating the same remote identity get two
	// separate accounts and two separate sessions. No global constraint may
	// force them to share one.
	insert := `INSERT INTO telegram_accounts (id, owner_user_id, server_key, network_type, remote_user_id, status, created_at, updated_at)
		VALUES (?, ?, 'official', 'telegram', 1, 'connected', 'now', 'now')`
	if _, err := store.db.ExecContext(ctx, insert, "acc_owner1", 1); err != nil {
		t.Fatalf("first owner: %v", err)
	}
	if _, err := store.db.ExecContext(ctx, insert, "acc_owner2", 2); err != nil {
		t.Fatalf("a second owner may hold the same remote identity: %v", err)
	}
	if _, err := store.db.ExecContext(ctx, insert, "acc_owner1_again", 1); err == nil {
		t.Fatal("the schema accepted the same remote identity twice for one owner")
	}

	// The same remote identity on another server is a different account.
	other := `INSERT INTO telegram_accounts (id, owner_user_id, server_key, network_type, remote_user_id, status, created_at, updated_at)
		VALUES ('acc_cat', 1, 'cat', 'telegram', 1, 'connected', 'now', 'now')`
	if _, err := store.db.ExecContext(ctx, other); err != nil {
		t.Fatalf("the same remote identity on another server: %v", err)
	}

	// A deleted account frees the identity for the same owner again: that is
	// what the index' deleted_at filter is for.
	if _, err := store.db.ExecContext(ctx,
		`UPDATE telegram_accounts SET deleted_at = 'now', status = 'removed' WHERE id = 'acc_owner1'`); err != nil {
		t.Fatal(err)
	}
	if _, err := store.db.ExecContext(ctx, insert, "acc_owner1_replaced", 1); err != nil {
		t.Fatalf("a deleted account must free its remote identity: %v", err)
	}

	// A remote identity that is not known yet is never deduplicated.
	pending := `INSERT INTO telegram_accounts (id, owner_user_id, server_key, network_type, remote_user_id, status, created_at, updated_at)
		VALUES (?, 1, 'official', 'telegram', 0, 'connecting', 'now', 'now')`
	for _, id := range []string{"acc_pending_a", "acc_pending_b"} {
		if _, err := store.db.ExecContext(ctx, pending, id); err != nil {
			t.Fatalf("an account with no remote identity yet (%s): %v", id, err)
		}
	}
}

func TestLoginAttemptsSchemaRefusesAnUnknownStateMethodOrTransition(t *testing.T) {
	ctx := context.Background()
	store := openInternal(t)
	for _, stmt := range []string{
		`INSERT INTO users (id, fullname, username, password_hash, created_at, updated_at)
			VALUES (1, 'One', 'one', 'h', 'now', 'now')`,
		`INSERT INTO login_attempts (id, owner_user_id, server_key, method, state, expires_at, created_at, updated_at)
			VALUES ('lga_ok', 1, 'official', 'phone', 'created', 'later', 'now', 'now')`,
	} {
		if _, err := store.db.ExecContext(ctx, stmt); err != nil {
			t.Fatal(err)
		}
	}
	for _, stmt := range []string{
		`INSERT INTO login_attempts (id, owner_user_id, server_key, method, state, expires_at, created_at, updated_at)
			VALUES ('lga_1', 1, 'official', 'sms', 'created', 'later', 'now', 'now')`,
		`INSERT INTO login_attempts (id, owner_user_id, server_key, method, state, expires_at, created_at, updated_at)
			VALUES ('lga_2', 1, 'official', 'phone', 'waiting_for_lunch', 'later', 'now', 'now')`,
	} {
		if _, err := store.db.ExecContext(ctx, stmt); err == nil {
			t.Errorf("the schema accepted %q", stmt)
		}
	}
}

func TestQuarantinedLegacyChannelsAreReachableOnlyThroughTheirLegacyKey(t *testing.T) {
	ctx := context.Background()
	store := openInternal(t)
	for _, stmt := range []string{
		`INSERT INTO users (id, fullname, username, password_hash, created_at, updated_at)
			VALUES (1, 'One', 'one', 'h', 'now', 'now')`,
		`INSERT INTO telegram_accounts (id, owner_user_id, server_key, network_type, status, created_at, updated_at)
			VALUES ('acc_a', 1, 'official', 'telegram', 'connected', 'now', 'now')`,
		`INSERT INTO telegram_accounts (id, owner_user_id, server_key, network_type, status, created_at, updated_at)
			VALUES ('legacy_official', NULL, 'official', '', 'quarantined', 'now', 'now')`,
		// A channel of a personal account, and a channel of the shared legacy
		// central session of the same server.
		`INSERT INTO managed_channels (account_id, client_key, channel_id, peer_type, membership_status, created_at, updated_at, last_synced_at)
			VALUES ('acc_a', 'official', 900, 'channel', 'member', 'now', 'now', 'now')`,
		`INSERT INTO managed_channels (account_id, client_key, channel_id, peer_type, membership_status, created_at, updated_at, last_synced_at)
			VALUES ('legacy_official', 'official', 901, 'channel', 'member', 'now', 'now', 'now')`,
	} {
		if _, err := store.db.ExecContext(ctx, stmt); err != nil {
			t.Fatal(err)
		}
	}

	// The shape every account-scoped read takes. A quarantined account is
	// named by no user's account, so no user's read reaches its channels.
	rows, err := store.db.QueryContext(ctx,
		`SELECT channel_id FROM managed_channels WHERE account_id = ? ORDER BY channel_id`, "acc_a")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var got []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			t.Fatal(err)
		}
		got = append(got, id)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0] != 900 {
		t.Fatalf("account-scoped read = %v, want [900]", got)
	}

	// The legacy key is what reaches the quarantined channel.
	legacy, err := store.ResolveLegacy(ctx, "official")
	if err != nil {
		t.Fatal(err)
	}
	if legacy != "legacy_official" {
		t.Fatalf("ResolveLegacy(official) = %q, want legacy_official", legacy)
	}
	var channelID int64
	if err := store.db.QueryRowContext(ctx,
		`SELECT channel_id FROM managed_channels WHERE account_id = ?`, legacy).Scan(&channelID); err != nil {
		t.Fatal(err)
	}
	if channelID != 901 {
		t.Fatalf("the legacy key reached channel %d, want 901", channelID)
	}
}

func TestTelegramAccountsAllowAMissingOwnerOnlyForQuarantine(t *testing.T) {
	ctx := context.Background()
	store := openInternal(t)
	for _, stmt := range []string{
		`INSERT INTO users (id, fullname, username, password_hash, created_at, updated_at)
			VALUES (1, 'One', 'one', 'h', 'now', 'now')`,
		`INSERT INTO telegram_accounts (id, owner_user_id, server_key, network_type, status, created_at, updated_at)
			VALUES ('legacy_official', NULL, 'official', '', 'quarantined', 'now', 'now')`,
	} {
		if _, err := store.db.ExecContext(ctx, stmt); err != nil {
			t.Fatal(err)
		}
	}
	// "Ownership is unclear" is the only thing a missing owner may mean.
	if _, err := store.db.ExecContext(ctx,
		`INSERT INTO telegram_accounts (id, owner_user_id, server_key, network_type, status, created_at, updated_at)
			VALUES ('acc_orphan', NULL, 'official', 'telegram', 'connected', 'now', 'now')`); err == nil {
		t.Fatal("the schema accepted an ownerless account that is not quarantined")
	}
}

func TestLoginAttemptsKeepTheDeliveryAndRateLimitRecord(t *testing.T) {
	ctx := context.Background()
	store := openInternal(t)
	for _, stmt := range []string{
		`INSERT INTO users (id, fullname, username, password_hash, created_at, updated_at)
			VALUES (1, 'One', 'one', 'h', 'now', 'now')`,
		`INSERT INTO login_attempts (id, owner_user_id, server_key, network_type, method, state,
			phone_number_masked, delivery, delivery_hint, code_length, code_submissions,
			next_step, qr_token, qr_expires_at, expires_at, created_at, updated_at, authorized_at)
			VALUES ('lga_1', 1, 'official', 'custom', 'qr', 'waiting_for_qr',
			'+98 *** **12', 'app', 'check your email', 5, 2,
			'show_qr', 'short-lived-token', '2026-05-01T12:05:00Z', '2026-05-01T12:10:00Z',
			'now', 'now', '2026-05-01T12:06:00Z')`,
	} {
		if _, err := store.db.ExecContext(ctx, stmt); err != nil {
			t.Fatal(err)
		}
	}
	var delivery, hint, token string
	var submissions, length int
	if err := store.db.QueryRowContext(ctx, `
		SELECT delivery, delivery_hint, qr_token, code_submissions, code_length
		FROM login_attempts WHERE id = 'lga_1'`).Scan(&delivery, &hint, &token, &submissions, &length); err != nil {
		t.Fatal(err)
	}
	if delivery != "app" || hint != "check your email" || token != "short-lived-token" ||
		submissions != 2 || length != 5 {
		t.Errorf("delivery record = (%q, %q, %q, %d, %d)", delivery, hint, token, submissions, length)
	}
	// The rate limit is a count, and it cannot run backwards.
	if _, err := store.db.ExecContext(ctx,
		`UPDATE login_attempts SET code_submissions = -1 WHERE id = 'lga_1'`); err == nil {
		t.Error("the schema accepted a negative submission count")
	}
}
