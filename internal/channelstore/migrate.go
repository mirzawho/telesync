package channelstore

import (
	"context"
	"fmt"
)

// migration is one versioned, forward-only schema change.
//
// Migrations are applied inside a transaction together with the schema_version
// bump, so a failure leaves the database exactly as it was.
type migration struct {
	version int
	name    string
	stmts   []string
}

// migrations is the ordered schema history. Never edit an applied migration:
// add a new one instead.
var migrations = []migration{
	{
		version: 1,
		name:    "managed_channels",
		stmts: []string{
			`CREATE TABLE managed_channels (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	client_key TEXT NOT NULL,
	channel_id INTEGER NOT NULL,
	peer_type TEXT NOT NULL CHECK (peer_type IN ('channel', 'group')),
	title TEXT NOT NULL DEFAULT '',
	username TEXT NOT NULL DEFAULT '',
	description TEXT NOT NULL DEFAULT '',
	avatar_photo_id INTEGER NOT NULL DEFAULT 0,
	membership_status TEXT NOT NULL CHECK (membership_status IN ('member', 'join_pending', 'unknown')),
	participants_count INTEGER,
	created_at TEXT NOT NULL,
	updated_at TEXT NOT NULL,
	last_synced_at TEXT NOT NULL,
	UNIQUE (client_key, channel_id)
)`,
			// Listing one client's selection is the common read.
			`CREATE INDEX managed_channels_client_idx ON managed_channels (client_key)`,
		},
	},
	{
		version: 2,
		name:    "categories",
		stmts: []string{
			// COLLATE NOCASE makes the name unique ignoring ASCII case, so
			// "Technology" and "technology" cannot both exist.
			`CREATE TABLE categories (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	name TEXT NOT NULL COLLATE NOCASE CHECK (length(trim(name)) > 0),
	description TEXT NOT NULL DEFAULT '',
	created_at TEXT NOT NULL,
	updated_at TEXT NOT NULL,
	UNIQUE (name)
)`,
			// An assignment references a channel by client key and Telegram
			// id only. There is deliberately no foreign key to
			// managed_channels: a category may hold a channel that was not
			// joined through this application, and deleting a category must
			// never touch channel data.
			`CREATE TABLE channel_categories (
	client_key TEXT NOT NULL CHECK (length(trim(client_key)) > 0),
	channel_id INTEGER NOT NULL CHECK (channel_id > 0),
	category_id INTEGER NOT NULL REFERENCES categories (id) ON DELETE CASCADE,
	created_at TEXT NOT NULL,
	PRIMARY KEY (client_key, channel_id, category_id)
)`,
			`CREATE INDEX channel_categories_category_idx ON channel_categories (category_id)`,
		},
	},
	{
		version: 3,
		name:    "users_and_permissions",
		stmts: []string{
			// A local operator account. password_hash holds an encoded Argon2id
			// hash, never a password.
			`CREATE TABLE users (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	fullname TEXT NOT NULL CHECK (length(trim(fullname)) > 0),
	username TEXT NOT NULL COLLATE NOCASE UNIQUE CHECK (length(trim(username)) > 0),
	password_hash TEXT NOT NULL CHECK (length(password_hash) > 0),
	is_active INTEGER NOT NULL DEFAULT 1 CHECK (is_active IN (0, 1)),
	created_at TEXT NOT NULL,
	updated_at TEXT NOT NULL
)`,
			// Global permissions: "users.manage", "categories.create", ...
			`CREATE TABLE user_permissions (
	user_id INTEGER NOT NULL REFERENCES users (id) ON DELETE CASCADE,
	permission TEXT NOT NULL CHECK (length(trim(permission)) > 0),
	created_at TEXT NOT NULL,
	updated_at TEXT NOT NULL,
	PRIMARY KEY (user_id, permission)
)`,
			// Per-channel permissions. channel_id is the local managed_channels
			// row, never Telegram's channel id; deleting either side removes
			// the assignment.
			`CREATE TABLE user_channels_permission (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	user_id INTEGER NOT NULL REFERENCES users (id) ON DELETE CASCADE,
	channel_id INTEGER NOT NULL REFERENCES managed_channels (id) ON DELETE CASCADE,
	permission TEXT NOT NULL CHECK (permission IN ('view', 'update', 'delete')),
	effect TEXT NOT NULL CHECK (effect IN ('allow', 'deny')),
	created_at TEXT NOT NULL,
	updated_at TEXT NOT NULL,
	UNIQUE (user_id, channel_id, permission)
)`,
			`CREATE INDEX user_channels_permission_channel_idx ON user_channels_permission (channel_id)`,
			// Per-category permissions, keyed by the local categories row.
			`CREATE TABLE user_categories_permission (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	user_id INTEGER NOT NULL REFERENCES users (id) ON DELETE CASCADE,
	category_id INTEGER NOT NULL REFERENCES categories (id) ON DELETE CASCADE,
	permission TEXT NOT NULL CHECK (permission IN ('view', 'update', 'delete')),
	effect TEXT NOT NULL CHECK (effect IN ('allow', 'deny')),
	created_at TEXT NOT NULL,
	updated_at TEXT NOT NULL,
	UNIQUE (user_id, category_id, permission)
)`,
			`CREATE INDEX user_categories_permission_category_idx ON user_categories_permission (category_id)`,
		},
	},
	{
		version: 4,
		name:    "wordpress_sites",
		stmts: []string{
			// A registered WordPress destination. The application password is
			// never stored: credential_* holds an AES-256-GCM box, sealed with a
			// key that lives only in the configuration's environment variable.
			// url is the canonical site URL, unique ignoring ASCII case.
			`CREATE TABLE wordpress_sites (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	url TEXT NOT NULL COLLATE NOCASE UNIQUE CHECK (length(trim(url)) > 0),
	title TEXT NOT NULL DEFAULT '',
	site_icon_url TEXT,
	username TEXT NOT NULL CHECK (length(trim(username)) > 0),
	credential_version INTEGER NOT NULL CHECK (credential_version >= 1),
	credential_nonce BLOB NOT NULL CHECK (length(credential_nonce) > 0),
	credential_ciphertext BLOB NOT NULL CHECK (length(credential_ciphertext) > 0),
	credential_sealed_at TEXT NOT NULL,
	created_at TEXT NOT NULL,
	updated_at TEXT NOT NULL
)`,
			// Per-site permissions keyed by the local row: deleting a site
			// removes its assignments, deleting an account removes its own.
			`CREATE TABLE user_wordpress_permission (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	user_id INTEGER NOT NULL REFERENCES users (id) ON DELETE CASCADE,
	site_id INTEGER NOT NULL REFERENCES wordpress_sites (id) ON DELETE CASCADE,
	permission TEXT NOT NULL CHECK (permission IN ('view', 'update', 'delete')),
	effect TEXT NOT NULL CHECK (effect IN ('allow', 'deny')),
	created_at TEXT NOT NULL,
	updated_at TEXT NOT NULL,
	UNIQUE (user_id, site_id, permission)
)`,
			`CREATE INDEX user_wordpress_permission_site_idx ON user_wordpress_permission (site_id)`,
		},
	},
	{
		version: 5,
		name:    "wordpress_site_categories",
		stmts: []string{
			// The categories a site publishes into: the same global categories
			// channels are filed under. Both sides cascade, so deleting a
			// category drops it everywhere and deleting a site drops its links.
			`CREATE TABLE wordpress_site_categories (
	site_id INTEGER NOT NULL REFERENCES wordpress_sites (id) ON DELETE CASCADE,
	category_id INTEGER NOT NULL REFERENCES categories (id) ON DELETE CASCADE,
	created_at TEXT NOT NULL,
	PRIMARY KEY (site_id, category_id)
)`,
			`CREATE INDEX wordpress_site_categories_category_idx ON wordpress_site_categories (category_id)`,
		},
	},
	{
		version: 6,
		name:    "managed_channel_notices",
		stmts: []string{
			// Telegram's verified / scam / fake notices, as the last report
			// carried them. Not reported is not the same as "not flagged": a row
			// written before these columns existed, or by a client that never
			// reported the notices, has them unknown (notices_known = 0) rather
			// than claiming false.
			`ALTER TABLE managed_channels ADD COLUMN verified INTEGER NOT NULL DEFAULT 0 CHECK (verified IN (0, 1))`,
			`ALTER TABLE managed_channels ADD COLUMN scam INTEGER NOT NULL DEFAULT 0 CHECK (scam IN (0, 1))`,
			`ALTER TABLE managed_channels ADD COLUMN fake INTEGER NOT NULL DEFAULT 0 CHECK (fake IN (0, 1))`,
			`ALTER TABLE managed_channels ADD COLUMN notices_known INTEGER NOT NULL DEFAULT 0 CHECK (notices_known IN (0, 1))`,
			// Whether a photo was reported at all, which avatar_photo_id cannot
			// say when a backend reports the photo without its version. A row
			// from before this column keeps what its photo version already said.
			`ALTER TABLE managed_channels ADD COLUMN avatar_available INTEGER NOT NULL DEFAULT 0 CHECK (avatar_available IN (0, 1))`,
			`UPDATE managed_channels SET avatar_available = 1 WHERE avatar_photo_id <> 0`,
			// The four normalized permissions as the last report normalized them.
			// Every writer of this table has them (a join resolves the channel and
			// a membership check reports them with the membership), so only a row
			// from before this column set is unknown.
			`ALTER TABLE managed_channels ADD COLUMN permission_new INTEGER NOT NULL DEFAULT 0 CHECK (permission_new IN (0, 1))`,
			`ALTER TABLE managed_channels ADD COLUMN permission_edit INTEGER NOT NULL DEFAULT 0 CHECK (permission_edit IN (0, 1))`,
			`ALTER TABLE managed_channels ADD COLUMN permission_delete INTEGER NOT NULL DEFAULT 0 CHECK (permission_delete IN (0, 1))`,
			`ALTER TABLE managed_channels ADD COLUMN permission_read INTEGER NOT NULL DEFAULT 0 CHECK (permission_read IN (0, 1))`,
			`ALTER TABLE managed_channels ADD COLUMN permissions_known INTEGER NOT NULL DEFAULT 0 CHECK (permissions_known IN (0, 1))`,
			`DROP INDEX IF EXISTS managed_channels_client_channel_idx`,
			`CREATE INDEX managed_channels_client_channel_idx ON managed_channels (client_key, channel_id)`,
			`DROP INDEX IF EXISTS managed_channels_client_idx`,
		},
	},
	{
		version: 7,
		name:    "publish_operations",
		stmts: []string{
			// One row per publishing attempt, keyed by the caller's
			// Idempotency-Key. It is not a publication history: it holds only
			// what a retry needs to know, and the result is the response body
			// the destination already confirmed.
			`CREATE TABLE publish_operations (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	user_id INTEGER NOT NULL REFERENCES users (id) ON DELETE CASCADE,
	idempotency_key TEXT NOT NULL CHECK (length(trim(idempotency_key)) > 0),
	request_hash TEXT NOT NULL CHECK (length(trim(request_hash)) > 0),
	destination_type TEXT NOT NULL CHECK (destination_type IN ('telegram', 'wordpress')),
	destination_id TEXT NOT NULL CHECK (length(trim(destination_id)) > 0),
	status TEXT NOT NULL CHECK (status IN ('in_flight', 'succeeded', 'failed', 'unknown')),
	result TEXT NOT NULL DEFAULT '',
	error_code TEXT NOT NULL DEFAULT '',
	created_at TEXT NOT NULL,
	updated_at TEXT NOT NULL,
	UNIQUE (user_id, idempotency_key))`,
			`CREATE INDEX publish_operations_user_idx ON publish_operations (user_id, created_at)`,
		},
	},
}

// migrate brings the schema up to the latest version.
func (s *Store) migrate(ctx context.Context) error {
	if _, err := s.db.ExecContext(ctx,
		`CREATE TABLE IF NOT EXISTS schema_version (
	version INTEGER NOT NULL,
	applied_at TEXT NOT NULL
)`); err != nil {
		return fmt.Errorf("channelstore: create schema_version: %w", err)
	}

	current, err := s.currentVersion(ctx)
	if err != nil {
		return err
	}
	for _, m := range migrations {
		if m.version <= current {
			continue
		}
		if err := s.applyMigration(ctx, m); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) currentVersion(ctx context.Context) (int, error) {
	var version int
	err := s.db.QueryRowContext(ctx, `SELECT COALESCE(MAX(version), 0) FROM schema_version`).Scan(&version)
	if err != nil {
		return 0, fmt.Errorf("channelstore: read schema version: %w", err)
	}
	return version, nil
}

func (s *Store) applyMigration(ctx context.Context, m migration) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("channelstore: migrate to %d (%s): %w", m.version, m.name, err)
	}
	defer func() { _ = tx.Rollback() }()

	for _, stmt := range m.stmts {
		if _, err := tx.ExecContext(ctx, stmt); err != nil {
			return fmt.Errorf("channelstore: migrate to %d (%s): %w", m.version, m.name, err)
		}
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO schema_version (version, applied_at) VALUES (?, ?)`,
		m.version, formatTime(s.now()),
	); err != nil {
		return fmt.Errorf("channelstore: record migration %d: %w", m.version, err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("channelstore: migrate to %d (%s): %w", m.version, m.name, err)
	}
	return nil
}

// SchemaVersion reports the applied migration version.
func (s *Store) SchemaVersion(ctx context.Context) (int, error) {
	return s.currentVersion(ctx)
}
