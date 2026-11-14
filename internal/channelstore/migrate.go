package channelstore

import (
	"context"
	"database/sql"
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
	// rebuildsReferencedTable runs this migration with the two pragmas a
	// rebuild of a table other tables reference needs.
	//
	// SQLite's ALTER TABLE RENAME rewrites the REFERENCES clauses that name the
	// table, so they follow it to its old name and its DROP TABLE then takes
	// every referencing row with it through ON DELETE CASCADE. Renaming with
	// the legacy ALTER TABLE behaviour and foreign key enforcement off leaves
	// those clauses naming the name the rebuilt table takes, so the rebuild is
	// invisible to them. Both pragmas are per connection and foreign_keys is a
	// no-op inside a transaction, so the runner sets them around the
	// transaction, restores them afterwards, and checks the result before the
	// migration is recorded.
	rebuildsReferencedTable bool
}

// migrations is the ordered schema history. Never edit an applied migration:
// add a new one instead.
// accountOwnershipSchemaVersion is the first schema in which every managed
// channel names the account that owns it. Below this a channel row is a legacy
// registration whose owner nobody has claimed.
const accountOwnershipSchemaVersion = 21

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
	{
		version: 8,
		name:    "automation_events",
		stmts: []string{
			// One row per normalized incoming Telegram message. The row id is
			// derived from the identity below, so a replayed update produces the
			// same id and is refused twice over: once by the primary key and once
			// by the identity constraint. The identity deliberately excludes
			// source_client_key: two accounts watching the same channel on the
			// same network describe one logical message and must not run an
			// automation twice.
			`CREATE TABLE automation_events (
	id TEXT PRIMARY KEY,
	type TEXT NOT NULL CHECK (length(trim(type)) > 0),
	network_key TEXT NOT NULL CHECK (length(trim(network_key)) > 0),
	source_client_key TEXT NOT NULL CHECK (length(trim(source_client_key)) > 0),
	managed_channel_id INTEGER NOT NULL REFERENCES managed_channels (id) ON DELETE CASCADE,
	telegram_channel_id INTEGER NOT NULL CHECK (telegram_channel_id <> 0),
	message_id INTEGER NOT NULL CHECK (message_id > 0),
	grouped_id INTEGER,
	occurred_at TEXT NOT NULL,
	received_at TEXT NOT NULL,
	payload TEXT NOT NULL DEFAULT '',
	created_at TEXT NOT NULL,
	UNIQUE (type, network_key, telegram_channel_id, message_id))`,
			`CREATE INDEX automation_events_channel_idx ON automation_events (managed_channel_id, message_id)`,
			`CREATE INDEX automation_events_received_idx ON automation_events (received_at)`,
			// Which connections have observed an event. A duplicate observation is
			// dropped as an event but recorded here, so at least one authorized
			// connection able to read the media stays known even when the
			// connection that created the event row later loses access.
			`CREATE TABLE automation_event_sources (
	event_id TEXT NOT NULL REFERENCES automation_events (id) ON DELETE CASCADE,
	client_key TEXT NOT NULL CHECK (length(trim(client_key)) > 0),
	first_seen_at TEXT NOT NULL,
	last_seen_at TEXT NOT NULL,
	PRIMARY KEY (event_id, client_key))`,
			`CREATE INDEX automation_event_sources_client_idx ON automation_event_sources (client_key)`,
		},
	},
	{
		version: 9,
		name:    "automation_jobs",
		stmts: []string{
			// One durable unit of background work per (kind of work, target,
			// event). Claiming happens inside a transaction so two workers cannot
			// take the same row; a lease that stops being renewed is reclaimed.
			`CREATE TABLE automation_jobs (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	event_id TEXT NOT NULL REFERENCES automation_events (id) ON DELETE CASCADE,
	job_type TEXT NOT NULL CHECK (job_type IN ('webhook_delivery', 'workflow_run')),
	target_id INTEGER NOT NULL CHECK (target_id > 0),
	status TEXT NOT NULL CHECK (status IN ('pending', 'processing', 'retry_wait', 'succeeded', 'failed', 'cancelled')),
	attempt_count INTEGER NOT NULL DEFAULT 0 CHECK (attempt_count >= 0),
	max_attempts INTEGER NOT NULL CHECK (max_attempts > 0),
	available_at TEXT NOT NULL,
	locked_by TEXT NOT NULL DEFAULT '',
	locked_until TEXT,
	last_error_code TEXT NOT NULL DEFAULT '',
	last_error_message TEXT NOT NULL DEFAULT '',
	created_at TEXT NOT NULL,
	updated_at TEXT NOT NULL,
	completed_at TEXT,
	UNIQUE (job_type, target_id, event_id))`,
			`CREATE INDEX automation_jobs_claim_idx ON automation_jobs (status, available_at)`,
			`CREATE INDEX automation_jobs_lease_idx ON automation_jobs (status, locked_until)`,
			// Ordering: jobs for one target run in insertion order, so a job is
			// only claimable once nothing earlier for the same target is left.
			`CREATE INDEX automation_jobs_target_idx ON automation_jobs (job_type, target_id, id)`,
		},
	},
	{
		version: 10,
		name:    "automation_recovery_state",
		stmts: []string{
			// Per-channel high-water mark for the bounded history fallback used
			// when gotd cannot close a gap (ChannelDifferenceTooLong and friends).
			`CREATE TABLE automation_checkpoints (
	client_key TEXT NOT NULL CHECK (length(trim(client_key)) > 0),
	channel_id INTEGER NOT NULL,
	last_message_id INTEGER NOT NULL DEFAULT 0,
	updated_at TEXT NOT NULL,
	PRIMARY KEY (client_key, channel_id))`,
			// Durable backing for gotd's updates.StateStorage, so update state
			// survives a restart and missed updates can be recovered.
			`CREATE TABLE automation_update_states (
	client_key TEXT NOT NULL CHECK (length(trim(client_key)) > 0),
	user_id INTEGER NOT NULL,
	pts INTEGER NOT NULL DEFAULT 0,
	qts INTEGER NOT NULL DEFAULT 0,
	date INTEGER NOT NULL DEFAULT 0,
	seq INTEGER NOT NULL DEFAULT 0,
	updated_at TEXT NOT NULL,
	PRIMARY KEY (client_key, user_id))`,
			`CREATE TABLE automation_channel_pts (
	client_key TEXT NOT NULL CHECK (length(trim(client_key)) > 0),
	user_id INTEGER NOT NULL,
	channel_id INTEGER NOT NULL,
	pts INTEGER NOT NULL,
	updated_at TEXT NOT NULL,
	PRIMARY KEY (client_key, user_id, channel_id))`,
			`CREATE TABLE automation_peer_hashes (
	client_key TEXT NOT NULL CHECK (length(trim(client_key)) > 0),
	user_id INTEGER NOT NULL,
	peer_kind TEXT NOT NULL CHECK (peer_kind IN ('channel', 'user')),
	peer_id INTEGER NOT NULL,
	access_hash INTEGER NOT NULL,
	updated_at TEXT NOT NULL,
	PRIMARY KEY (client_key, user_id, peer_kind, peer_id))`,
		},
	},
	{
		version: 11,
		name:    "webhooks",
		stmts: []string{
			// The secret is sealed exactly like a WordPress credential: a
			// versioned nonce/ciphertext pair the browser never sees again after
			// it is written.
			`CREATE TABLE webhooks (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	name TEXT NOT NULL CHECK (length(trim(name)) > 0),
	description TEXT NOT NULL DEFAULT '',
	enabled INTEGER NOT NULL DEFAULT 1 CHECK (enabled IN (0, 1)),
	url TEXT NOT NULL CHECK (length(trim(url)) > 0),
	method TEXT NOT NULL CHECK (method IN ('POST', 'PUT', 'PATCH')),
	auth_mode TEXT NOT NULL CHECK (auth_mode IN ('hmac', 'bearer', 'header', 'none')),
	auth_header TEXT NOT NULL DEFAULT '',
	secret_version INTEGER NOT NULL DEFAULT 0 CHECK (secret_version >= 0),
	secret_nonce BLOB,
	secret_ciphertext BLOB,
	secret_sealed_at TEXT,
	timeout_ms INTEGER NOT NULL DEFAULT 15000 CHECK (timeout_ms > 0),
	max_attempts INTEGER NOT NULL DEFAULT 5 CHECK (max_attempts > 0),
	created_by INTEGER REFERENCES users (id) ON DELETE CASCADE,
	created_at TEXT NOT NULL,
	updated_at TEXT NOT NULL,
	CHECK (auth_mode <> 'header' OR length(trim(auth_header)) > 0))`,
			`CREATE TABLE webhook_sources (
	webhook_id INTEGER NOT NULL REFERENCES webhooks (id) ON DELETE CASCADE,
	managed_channel_id INTEGER NOT NULL REFERENCES managed_channels (id) ON DELETE CASCADE,
	created_at TEXT NOT NULL,
	PRIMARY KEY (webhook_id, managed_channel_id))`,
			`CREATE INDEX webhook_sources_channel_idx ON webhook_sources (managed_channel_id)`,
		},
	},
	{
		version: 12,
		name:    "webhook_deliveries",
		stmts: []string{
			// One logical delivery per (webhook, event). A retry extends this row
			// and appends an attempt; it never creates a second delivery.
			`CREATE TABLE webhook_deliveries (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	webhook_id INTEGER NOT NULL REFERENCES webhooks (id) ON DELETE CASCADE,
	event_id TEXT NOT NULL REFERENCES automation_events (id) ON DELETE CASCADE,
	status TEXT NOT NULL CHECK (status IN ('pending', 'delivering', 'succeeded', 'failed', 'cancelled')),
	attempt_count INTEGER NOT NULL DEFAULT 0 CHECK (attempt_count >= 0),
	next_attempt_at TEXT,
	last_http_status INTEGER NOT NULL DEFAULT 0,
	last_error TEXT NOT NULL DEFAULT '',
	created_at TEXT NOT NULL,
	updated_at TEXT NOT NULL,
	completed_at TEXT,
	UNIQUE (webhook_id, event_id))`,
			`CREATE INDEX webhook_deliveries_list_idx ON webhook_deliveries (webhook_id, created_at)`,
			`CREATE INDEX webhook_deliveries_status_idx ON webhook_deliveries (status, next_attempt_at)`,
			// Every request and response is kept. Headers and bodies are stored
			// already redacted and already truncated to the configured cap.
			`CREATE TABLE webhook_attempts (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	delivery_id INTEGER NOT NULL REFERENCES webhook_deliveries (id) ON DELETE CASCADE,
	attempt_number INTEGER NOT NULL CHECK (attempt_number > 0),
	request_method TEXT NOT NULL,
	request_url TEXT NOT NULL,
	request_headers TEXT NOT NULL DEFAULT '{}',
	request_body TEXT NOT NULL DEFAULT '',
	request_body_truncated INTEGER NOT NULL DEFAULT 0 CHECK (request_body_truncated IN (0, 1)),
	response_status INTEGER NOT NULL DEFAULT 0,
	response_headers TEXT NOT NULL DEFAULT '{}',
	response_body TEXT NOT NULL DEFAULT '',
	response_body_truncated INTEGER NOT NULL DEFAULT 0 CHECK (response_body_truncated IN (0, 1)),
	response_bytes INTEGER NOT NULL DEFAULT 0,
	duration_ms INTEGER NOT NULL DEFAULT 0,
	error_code TEXT NOT NULL DEFAULT '',
	error_message TEXT NOT NULL DEFAULT '',
	started_at TEXT NOT NULL,
	finished_at TEXT,
	UNIQUE (delivery_id, attempt_number))`,
			`CREATE INDEX webhook_attempts_delivery_idx ON webhook_attempts (delivery_id, attempt_number)`,
		},
	},
	{
		version: 13,
		name:    "workflows",
		stmts: []string{
			// At most one workflow per managed source channel, enforced by the
			// column constraint rather than by application code alone.
			`CREATE TABLE workflows (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	managed_channel_id INTEGER NOT NULL UNIQUE REFERENCES managed_channels (id) ON DELETE CASCADE,
	enabled INTEGER NOT NULL DEFAULT 0 CHECK (enabled IN (0, 1)),
	ai_enabled INTEGER NOT NULL DEFAULT 0 CHECK (ai_enabled IN (0, 1)),
	ai_prompt TEXT NOT NULL DEFAULT '',
	ai_send_images INTEGER NOT NULL DEFAULT 0 CHECK (ai_send_images IN (0, 1)),
	ai_publish_on_failure INTEGER NOT NULL DEFAULT 0 CHECK (ai_publish_on_failure IN (0, 1)),
	config_revision INTEGER NOT NULL DEFAULT 1 CHECK (config_revision > 0),
	created_by INTEGER REFERENCES users (id) ON DELETE CASCADE,
	created_at TEXT NOT NULL,
	updated_at TEXT NOT NULL)`,
			`CREATE TABLE workflow_destinations (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	workflow_id INTEGER NOT NULL REFERENCES workflows (id) ON DELETE CASCADE,
	destination_type TEXT NOT NULL CHECK (destination_type IN ('telegram', 'wordpress')),
	client_key TEXT NOT NULL DEFAULT '',
	channel_id INTEGER NOT NULL DEFAULT 0,
	site_id INTEGER NOT NULL DEFAULT 0,
	options TEXT NOT NULL DEFAULT '{}',
	position INTEGER NOT NULL DEFAULT 0,
	enabled INTEGER NOT NULL DEFAULT 1 CHECK (enabled IN (0, 1)),
	created_at TEXT NOT NULL,
	updated_at TEXT NOT NULL,
	CHECK (
		(destination_type = 'telegram' AND length(trim(client_key)) > 0 AND channel_id <> 0 AND site_id = 0) OR
		(destination_type = 'wordpress' AND site_id > 0 AND length(trim(client_key)) = 0 AND channel_id = 0)
	))`,
			`CREATE INDEX workflow_destinations_workflow_idx ON workflow_destinations (workflow_id, position)`,
		},
	},
	{
		version: 14,
		name:    "workflow_runs",
		stmts: []string{
			`CREATE TABLE workflow_runs (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	workflow_id INTEGER NOT NULL REFERENCES workflows (id) ON DELETE CASCADE,
	event_id TEXT NOT NULL REFERENCES automation_events (id) ON DELETE CASCADE,
	status TEXT NOT NULL CHECK (status IN ('queued', 'processing', 'published', 'published_with_fallback', 'partially_published', 'failed', 'cancelled')),
	source_message_id INTEGER NOT NULL CHECK (source_message_id > 0),
	original_content TEXT NOT NULL DEFAULT '',
	polished_content TEXT NOT NULL DEFAULT '',
	ai_status TEXT NOT NULL CHECK (ai_status IN ('not_used', 'succeeded', 'failed', 'fallback_used')),
	ai_fallback_used INTEGER NOT NULL DEFAULT 0 CHECK (ai_fallback_used IN (0, 1)),
	ai_model TEXT NOT NULL DEFAULT '',
	ai_error_code TEXT NOT NULL DEFAULT '',
	ai_error_message TEXT NOT NULL DEFAULT '',
	ai_input_tokens INTEGER NOT NULL DEFAULT 0,
	ai_output_tokens INTEGER NOT NULL DEFAULT 0,
	ai_duration_ms INTEGER NOT NULL DEFAULT 0,
	config_revision INTEGER NOT NULL DEFAULT 0,
	attempt_count INTEGER NOT NULL DEFAULT 0,
	last_error_code TEXT NOT NULL DEFAULT '',
	last_error_message TEXT NOT NULL DEFAULT '',
	created_at TEXT NOT NULL,
	started_at TEXT,
	completed_at TEXT,
	UNIQUE (workflow_id, event_id))`,
			`CREATE INDEX workflow_runs_list_idx ON workflow_runs (workflow_id, created_at)`,
			`CREATE INDEX workflow_runs_status_idx ON workflow_runs (status, created_at)`,
			// One result per destination. A destination that already published is
			// never published again on retry: this row is the proof.
			`CREATE TABLE workflow_publications (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	workflow_run_id INTEGER NOT NULL REFERENCES workflow_runs (id) ON DELETE CASCADE,
	destination_type TEXT NOT NULL CHECK (destination_type IN ('telegram', 'wordpress')),
	client_key TEXT NOT NULL DEFAULT '',
	channel_id INTEGER NOT NULL DEFAULT 0,
	site_id INTEGER NOT NULL DEFAULT 0,
	status TEXT NOT NULL CHECK (status IN ('pending', 'published', 'failed', 'unknown')),
	remote_message_id INTEGER NOT NULL DEFAULT 0,
	remote_post_id INTEGER NOT NULL DEFAULT 0,
	remote_url TEXT NOT NULL DEFAULT '',
	attempt_count INTEGER NOT NULL DEFAULT 0,
	last_error_code TEXT NOT NULL DEFAULT '',
	last_error_message TEXT NOT NULL DEFAULT '',
	created_at TEXT NOT NULL,
	completed_at TEXT,
	UNIQUE (workflow_run_id, destination_type, client_key, channel_id, site_id))`,
			`CREATE INDEX workflow_publications_remote_idx ON workflow_publications (destination_type, client_key, channel_id, remote_message_id)`,
		},
	},
	{
		version: 15,
		name:    "automation_outbound_messages",
		stmts: []string{
			// Every message Telesync itself put into a Telegram channel, whatever
			// put it there. Ingestion checks this before creating an event, which
			// is what stops a workflow from feeding itself.
			`CREATE TABLE automation_outbound_messages (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	network_key TEXT NOT NULL CHECK (length(trim(network_key)) > 0),
	client_key TEXT NOT NULL CHECK (length(trim(client_key)) > 0),
	channel_id INTEGER NOT NULL,
	remote_message_id INTEGER NOT NULL CHECK (remote_message_id > 0),
	origin TEXT NOT NULL CHECK (origin IN ('manual', 'workflow')),
	origin_id INTEGER NOT NULL DEFAULT 0,
	created_at TEXT NOT NULL,
	UNIQUE (network_key, client_key, channel_id, remote_message_id))`,
			`CREATE INDEX automation_outbound_messages_lookup_idx ON automation_outbound_messages (client_key, channel_id, remote_message_id)`,
		},
	},
	{
		version: 16,
		name:    "webhooks_allow_localhost",
		stmts: []string{
			// The per-webhook half of the loopback exception. It is off for
			// every webhook that already exists: a record written before this
			// column was known to it never asked for it, and a default of 1
			// would hand the exception to every receiver already configured.
			`ALTER TABLE webhooks ADD COLUMN allow_localhost INTEGER NOT NULL DEFAULT 0 CHECK (allow_localhost IN (0, 1))`,
		},
	},
	{
		version: 17,
		name:    "message_send_operations",
		stmts: []string{
			// A send that is accepted now and performed later. The request that
			// created it is answered long before Telegram hears about it, so
			// everything the background worker needs is here: whose it is, where
			// it goes, what it says, which staged attachment it carries, and the
			// client message id every attempt must reuse so a resend after a lost
			// confirmation is the same message rather than a second one.
			//
			// The queue's own attempt count and retry schedule stay in
			// automation_jobs; this table holds what the caller is told.
			`CREATE TABLE message_send_operations (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	public_id TEXT NOT NULL UNIQUE CHECK (length(public_id) > 5),
	user_id INTEGER NOT NULL CHECK (user_id > 0),
	client_key TEXT NOT NULL CHECK (length(trim(client_key)) > 0),
	destination_channel_id TEXT NOT NULL CHECK (length(trim(destination_channel_id)) > 0),
	request_payload TEXT NOT NULL,
	media_kind TEXT NOT NULL DEFAULT '',
	media_staging_name TEXT NOT NULL DEFAULT '',
	media_filename TEXT NOT NULL DEFAULT '',
	media_mime_type TEXT NOT NULL DEFAULT '',
	media_size INTEGER NOT NULL DEFAULT 0 CHECK (media_size >= 0),
	media_sha256 TEXT NOT NULL DEFAULT '',
	idempotency_key TEXT NOT NULL DEFAULT '',
	request_fingerprint TEXT NOT NULL CHECK (length(request_fingerprint) > 0),
	random_id INTEGER NOT NULL DEFAULT 0,
	status TEXT NOT NULL CHECK (status IN ('queued', 'processing', 'retrying', 'sent', 'failed', 'unknown')),
	stage TEXT NOT NULL DEFAULT '',
	attempt_count INTEGER NOT NULL DEFAULT 0 CHECK (attempt_count >= 0),
	next_attempt_at TEXT,
	remote_message_id INTEGER NOT NULL DEFAULT 0 CHECK (remote_message_id >= 0),
	last_error_code TEXT NOT NULL DEFAULT '',
	last_error_message TEXT NOT NULL DEFAULT '',
	created_at TEXT NOT NULL,
	started_at TEXT,
	completed_at TEXT,
	updated_at TEXT NOT NULL)`,
			// Idempotency is scoped to the account, exactly as publish_operations
			// scopes it: one key means one request, whoever repeats it. The index
			// is partial because a request that carried no key joins nothing.
			`CREATE UNIQUE INDEX message_send_operations_idem_idx
	ON message_send_operations (user_id, idempotency_key) WHERE idempotency_key <> ''`,
			`CREATE INDEX message_send_operations_status_idx ON message_send_operations (status, updated_at)`,
			`CREATE INDEX message_send_operations_owner_idx ON message_send_operations (user_id, created_at)`,
			`CREATE INDEX message_send_operations_created_idx ON message_send_operations (created_at)`,
			// What a cleanup sweep reads to decide which staged attachments are
			// still needed. An empty name means a text-only send and names no
			// file, so those rows are excluded from the index.
			`CREATE INDEX message_send_operations_media_idx
	ON message_send_operations (media_staging_name) WHERE media_staging_name <> ''`,
		},
	},
	{
		version: 18,
		name:    "automation_jobs_carry_direct_sends",
		stmts: []string{
			// One queue runs every kind of background work this deployment has,
			// and a direct send is one more: it has an attempt budget, a lease
			// and a retry policy like the rest, and giving it a table of its own
			// would mean two claim protocols to keep in step.
			//
			// What changes is that event_id no longer always names an
			// automation event. For a webhook delivery or a workflow run it
			// still does, and the reference is still enforced — below, by
			// trigger, because a column-level foreign key cannot tell one job
			// kind from another. For a send it names the send operation, whose
			// own table is where that work is described.
			//
			// SQLite cannot alter a CHECK or a foreign key in place, so the
			// table is rebuilt. It is empty of meaning during the copy: the
			// columns are carried across unchanged and the indexes are put back
			// as they were.
			`CREATE TABLE automation_jobs_rebuilt (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	event_id TEXT NOT NULL,
	job_type TEXT NOT NULL CHECK (job_type IN ('webhook_delivery', 'workflow_run', 'telegram_message_send')),
	target_id INTEGER NOT NULL CHECK (target_id > 0),
	status TEXT NOT NULL CHECK (status IN ('pending', 'processing', 'retry_wait', 'succeeded', 'failed', 'cancelled')),
	attempt_count INTEGER NOT NULL DEFAULT 0 CHECK (attempt_count >= 0),
	max_attempts INTEGER NOT NULL CHECK (max_attempts > 0),
	available_at TEXT NOT NULL,
	locked_by TEXT NOT NULL DEFAULT '',
	locked_until TEXT,
	last_error_code TEXT NOT NULL DEFAULT '',
	last_error_message TEXT NOT NULL DEFAULT '',
	created_at TEXT NOT NULL,
	updated_at TEXT NOT NULL,
	completed_at TEXT,
	UNIQUE (job_type, target_id, event_id))`,
			`INSERT INTO automation_jobs_rebuilt (
	id, event_id, job_type, target_id, status, attempt_count, max_attempts,
	available_at, locked_by, locked_until, last_error_code, last_error_message,
	created_at, updated_at, completed_at)
	SELECT id, event_id, job_type, target_id, status, attempt_count, max_attempts,
	available_at, locked_by, locked_until, last_error_code, last_error_message,
	created_at, updated_at, completed_at FROM automation_jobs`,
			`DROP TABLE automation_jobs`,
			`ALTER TABLE automation_jobs_rebuilt RENAME TO automation_jobs`,
			`CREATE INDEX automation_jobs_claim_idx ON automation_jobs (status, available_at)`,
			`CREATE INDEX automation_jobs_lease_idx ON automation_jobs (status, locked_until)`,
			`CREATE INDEX automation_jobs_target_idx ON automation_jobs (job_type, target_id, id)`,
			// The two automation kinds keep exactly the integrity their column
			// used to give them: a job is not queued for an event that is not
			// there, and deleting an event still takes its jobs with it.
			`CREATE TRIGGER automation_jobs_event_insert
	BEFORE INSERT ON automation_jobs
	WHEN NEW.job_type IN ('webhook_delivery', 'workflow_run')
	  AND NOT EXISTS (SELECT 1 FROM automation_events WHERE id = NEW.event_id)
	BEGIN
		SELECT RAISE(ABORT, 'automation_jobs.event_id names no automation event');
	END`,
			`CREATE TRIGGER automation_jobs_event_delete
	AFTER DELETE ON automation_events
	BEGIN
		DELETE FROM automation_jobs
		WHERE event_id = OLD.id AND job_type IN ('webhook_delivery', 'workflow_run');
	END`,
		},
	},
	{
		version: 19,
		name:    "message_edit_operations_and_jobs",
		stmts: []string{
			`CREATE TABLE message_edit_operations (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	public_id TEXT NOT NULL UNIQUE CHECK (length(public_id) > 0),
	user_id INTEGER NOT NULL REFERENCES users (id) ON DELETE CASCADE,
	client_key TEXT NOT NULL CHECK (length(trim(client_key)) > 0),
	destination_channel_id TEXT NOT NULL CHECK (length(trim(destination_channel_id)) > 0),
	target_message_id INTEGER NOT NULL CHECK (target_message_id > 0),
	request_payload TEXT NOT NULL,
	media_kind TEXT NOT NULL DEFAULT '',
	media_staging_name TEXT NOT NULL DEFAULT '',
	media_filename TEXT NOT NULL DEFAULT '',
	media_mime_type TEXT NOT NULL DEFAULT '',
	media_size INTEGER NOT NULL DEFAULT 0 CHECK (media_size >= 0),
	media_sha256 TEXT NOT NULL DEFAULT '',
	idempotency_key TEXT NOT NULL DEFAULT '',
	request_fingerprint TEXT NOT NULL CHECK (length(request_fingerprint) > 0),
	status TEXT NOT NULL CHECK (status IN ('queued', 'processing', 'retrying', 'edited', 'failed', 'unknown')),
	stage TEXT NOT NULL DEFAULT '',
	attempt_count INTEGER NOT NULL DEFAULT 0 CHECK (attempt_count >= 0),
	next_attempt_at TEXT,
	remote_message_id INTEGER NOT NULL DEFAULT 0 CHECK (remote_message_id = 0 OR remote_message_id = target_message_id),
	result_media_kind TEXT NOT NULL DEFAULT '',
	last_error_code TEXT NOT NULL DEFAULT '',
	last_error_message TEXT NOT NULL DEFAULT '',
	created_at TEXT NOT NULL,
	started_at TEXT,
	completed_at TEXT,
	updated_at TEXT NOT NULL)`,
			`CREATE UNIQUE INDEX message_edit_operations_idem_idx
	ON message_edit_operations (user_id, idempotency_key) WHERE idempotency_key <> ''`,
			`CREATE INDEX message_edit_operations_status_idx ON message_edit_operations (status, updated_at)`,
			`CREATE INDEX message_edit_operations_owner_idx ON message_edit_operations (user_id, created_at)`,
			`CREATE INDEX message_edit_operations_target_idx
	ON message_edit_operations (client_key, destination_channel_id, target_message_id, created_at)`,
			`CREATE INDEX message_edit_operations_created_idx ON message_edit_operations (created_at)`,
			`CREATE INDEX message_edit_operations_media_idx
	ON message_edit_operations (media_staging_name) WHERE media_staging_name <> ''`,
			// Adding an edit job kind requires a forward table rebuild: SQLite
			// cannot alter the existing CHECK constraint. Existing rows and ids
			// remain unchanged so in-flight work survives the migration.
			`CREATE TABLE automation_jobs_rebuilt (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	event_id TEXT NOT NULL,
	job_type TEXT NOT NULL CHECK (job_type IN ('webhook_delivery', 'workflow_run', 'telegram_message_send', 'telegram_message_edit')),
	target_id INTEGER NOT NULL CHECK (target_id > 0),
	status TEXT NOT NULL CHECK (status IN ('pending', 'processing', 'retry_wait', 'succeeded', 'failed', 'cancelled')),
	attempt_count INTEGER NOT NULL DEFAULT 0 CHECK (attempt_count >= 0),
	max_attempts INTEGER NOT NULL CHECK (max_attempts > 0),
	available_at TEXT NOT NULL,
	locked_by TEXT NOT NULL DEFAULT '',
	locked_until TEXT,
	last_error_code TEXT NOT NULL DEFAULT '',
	last_error_message TEXT NOT NULL DEFAULT '',
	created_at TEXT NOT NULL,
	updated_at TEXT NOT NULL,
	completed_at TEXT,
	UNIQUE (job_type, target_id, event_id))`,
			`INSERT INTO automation_jobs_rebuilt (
	id, event_id, job_type, target_id, status, attempt_count, max_attempts,
	available_at, locked_by, locked_until, last_error_code, last_error_message,
	created_at, updated_at, completed_at)
	SELECT id, event_id, job_type, target_id, status, attempt_count, max_attempts,
	available_at, locked_by, locked_until, last_error_code, last_error_message,
	created_at, updated_at, completed_at FROM automation_jobs`,
			// This trigger belongs to automation_events, not automation_jobs, so
			// dropping the old queue table would leave it pointing at a missing
			// table during the remainder of the transactional rebuild.
			`DROP TRIGGER automation_jobs_event_delete`,
			`DROP TABLE automation_jobs`,
			`ALTER TABLE automation_jobs_rebuilt RENAME TO automation_jobs`,
			`CREATE INDEX automation_jobs_claim_idx ON automation_jobs (status, available_at)`,
			`CREATE INDEX automation_jobs_lease_idx ON automation_jobs (status, locked_until)`,
			`CREATE INDEX automation_jobs_target_idx ON automation_jobs (job_type, target_id, id)`,
			`CREATE TRIGGER automation_jobs_event_insert
	BEFORE INSERT ON automation_jobs
	WHEN NEW.job_type IN ('webhook_delivery', 'workflow_run')
	  AND NOT EXISTS (SELECT 1 FROM automation_events WHERE id = NEW.event_id)
	BEGIN
		SELECT RAISE(ABORT, 'automation_jobs.event_id names no automation event');
	END`,
			`CREATE TRIGGER automation_jobs_event_delete
	AFTER DELETE ON automation_events
	BEGIN
		DELETE FROM automation_jobs
		WHERE event_id = OLD.id AND job_type IN ('webhook_delivery', 'workflow_run');
	END`,
			`CREATE TRIGGER automation_jobs_edit_operation_insert
	BEFORE INSERT ON automation_jobs
	WHEN NEW.job_type = 'telegram_message_edit'
	  AND NOT EXISTS (SELECT 1 FROM message_edit_operations WHERE public_id = NEW.event_id)
	BEGIN
		SELECT RAISE(ABORT, 'automation_jobs.event_id names no message edit operation');
	END`,
			`CREATE TRIGGER message_edit_operations_job_delete
	AFTER DELETE ON message_edit_operations
	BEGIN
		DELETE FROM automation_jobs
		WHERE event_id = OLD.public_id AND job_type = 'telegram_message_edit';
	END`,
		},
	},
	{
		version: 20,
		name:    "channel_creation_operations",
		stmts: []string{
			`CREATE TABLE channel_creation_operations (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	user_id INTEGER NOT NULL CHECK (user_id >= 0),
	client_key TEXT NOT NULL CHECK (length(trim(client_key)) > 0),
	idempotency_key TEXT NOT NULL CHECK (length(trim(idempotency_key)) > 0),
	request_fingerprint TEXT NOT NULL CHECK (length(request_fingerprint) > 0),
	request_payload TEXT NOT NULL,
	status TEXT NOT NULL CHECK (status IN ('pending', 'unknown', 'created', 'created_partial')),
	remote_channel_id INTEGER NOT NULL DEFAULT 0 CHECK (remote_channel_id >= 0),
	local_channel_id INTEGER NOT NULL DEFAULT 0 CHECK (local_channel_id >= 0),
	channel_created INTEGER NOT NULL DEFAULT 0 CHECK (channel_created IN (0, 1)),
	username_assigned INTEGER NOT NULL DEFAULT 0 CHECK (username_assigned IN (0, 1)),
	avatar_updated INTEGER NOT NULL DEFAULT 0 CHECK (avatar_updated IN (0, 1)),
	locally_registered INTEGER NOT NULL DEFAULT 0 CHECK (locally_registered IN (0, 1)),
	error_code TEXT NOT NULL DEFAULT '',
	error_message TEXT NOT NULL DEFAULT '',
	created_at TEXT NOT NULL,
	updated_at TEXT NOT NULL,
	UNIQUE (user_id, client_key, idempotency_key))`,
			`CREATE INDEX channel_creation_operations_created_idx ON channel_creation_operations (created_at)`,
		},
	},
	{
		version: 21,
		name:    "personal_telegram_accounts",
		// accountOwnershipSchemaVersion names this one, because it is the
		// migration that gives channels an owner.
		// Ownership is an added dimension, not a rename. managed_channels keeps
		// client_key as the server definition ('official', 'cat') and gains
		// account_id as the user-owned authenticated session the row belongs
		// to, so two users may independently subscribe to the same remote
		// channel on the same server.
		//
		// managed_channels is rebuilt because SQLite cannot drop a table's
		// UNIQUE constraint with ALTER. Every id is carried across unchanged, so
		// the foreign keys in user_channels_permission, channel_categories,
		// automation_* and webhook_* still resolve. The rebuild runs under the
		// pragmas a referenced table needs; see migration.rebuildsReferencedTable.
		rebuildsReferencedTable: true,
		stmts: []string{
			// One user-owned Telegram (or Cat) account: an opaque, stable,
			// never-reused id is the authoritative session identity. Nothing
			// here is ever a username, a phone number or a server key.
			`CREATE TABLE telegram_accounts (
	id TEXT PRIMARY KEY,
	owner_user_id INTEGER REFERENCES users (id) ON DELETE CASCADE,
	server_key TEXT NOT NULL,
	network_type TEXT NOT NULL,
	remote_user_id INTEGER NOT NULL DEFAULT 0,
	remote_username TEXT NOT NULL DEFAULT '',
	remote_display_name TEXT NOT NULL DEFAULT '',
	phone_number_masked TEXT NOT NULL DEFAULT '',
	status TEXT NOT NULL
		CHECK (status IN ('connecting', 'connected', 'disconnected', 'reconnecting',
			'authentication_required', 'disabled', 'error', 'removed', 'quarantined')),
	session_enc_version INTEGER NOT NULL DEFAULT 0,
	session_nonce BLOB,
	session_ciphertext BLOB,
	session_enc_key_version INTEGER NOT NULL DEFAULT 1,
	session_storage_ref TEXT NOT NULL DEFAULT '',
	created_at TEXT NOT NULL,
	authorized_at TEXT,
	last_connected_at TEXT,
	last_disconnected_at TEXT,
	last_error_code TEXT NOT NULL DEFAULT '',
	updated_at TEXT NOT NULL,
	deleted_at TEXT,
	-- A missing owner is legal only for a quarantined account: that is exactly
	-- "ownership is unclear", and nothing else may claim it.
	CHECK (owner_user_id IS NOT NULL OR status = 'quarantined')
)`,
			// Owner-scoped dedup of the same remote identity on the same server,
			// deliberately not global: two owners authenticating one remote
			// identity get two accounts and two sessions, and no constraint may
			// force them to share one. A remote identity not known yet (0) is
			// never deduplicated, and a deleted account frees its identity.
			`CREATE UNIQUE INDEX telegram_accounts_owner_remote_idx
	ON telegram_accounts (owner_user_id, server_key, remote_user_id)
	WHERE remote_user_id != 0 AND status != 'quarantined' AND deleted_at IS NULL`,
			`CREATE INDEX telegram_accounts_owner_idx ON telegram_accounts (owner_user_id)`,
			`CREATE INDEX telegram_accounts_status_idx ON telegram_accounts (status)`,

			// The login state machine's durable, bounded, owner-scoped record.
			// A login code or a 2FA password is never in here: only where the
			// attempt got to and what it is waiting for.
			//
			// qr_token is a short-lived login token and is secret-class: never
			// logged, never in error text, and purged when the attempt ends.
			// delivery_hint is third-party text the server sent (an email
			// pattern, an SMS prefix): untrusted, never rendered as HTML, and
			// never allowed to influence control flow. code_submissions bounds
			// a brute force against one attempt and is never serialized to a
			// client.
			`CREATE TABLE login_attempts (
	id TEXT PRIMARY KEY,
	owner_user_id INTEGER NOT NULL REFERENCES users (id) ON DELETE CASCADE,
	account_id TEXT REFERENCES telegram_accounts (id) ON DELETE CASCADE,
	server_key TEXT NOT NULL,
	network_type TEXT NOT NULL DEFAULT '',
	method TEXT NOT NULL CHECK (method IN ('phone', 'qr')),
	state TEXT NOT NULL CHECK (state IN ('created', 'connecting', 'waiting_for_code',
		'waiting_for_password', 'waiting_for_qr', 'authorized', 'failed', 'expired', 'canceled')),
	phone_number_masked TEXT NOT NULL DEFAULT '',
	delivery TEXT NOT NULL DEFAULT '',
	delivery_hint TEXT NOT NULL DEFAULT '',
	code_length INTEGER NOT NULL DEFAULT 0 CHECK (code_length >= 0),
	code_submissions INTEGER NOT NULL DEFAULT 0 CHECK (code_submissions >= 0),
	next_step TEXT NOT NULL DEFAULT '',
	qr_token TEXT NOT NULL DEFAULT '',
	qr_expires_at TEXT,
	resend_after TEXT,
	expires_at TEXT NOT NULL,
	last_error_code TEXT NOT NULL DEFAULT '',
	created_at TEXT NOT NULL,
	updated_at TEXT NOT NULL,
	authorized_at TEXT
)`,
			`CREATE INDEX login_attempts_owner_idx ON login_attempts (owner_user_id, created_at)`,
			`CREATE INDEX login_attempts_account_idx ON login_attempts (account_id)`,
			`CREATE INDEX login_attempts_expires_idx ON login_attempts (expires_at)`,

			// Which operator adopted which quarantined legacy central session,
			// and why. Adoption is explicit and auditable: nothing assigns a
			// shared legacy session to an administrator on its own.
			`CREATE TABLE legacy_session_adoptions (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	legacy_key TEXT NOT NULL CHECK (length(trim(legacy_key)) > 0),
	account_id TEXT NOT NULL REFERENCES telegram_accounts (id),
	adopted_by INTEGER NOT NULL REFERENCES users (id),
	adopted_at TEXT NOT NULL,
	reason TEXT NOT NULL CHECK (length(trim(reason)) > 0),
	UNIQUE (legacy_key, account_id, adopted_by, adopted_at)
)`,
			`CREATE INDEX legacy_session_adoptions_key_idx ON legacy_session_adoptions (legacy_key)`,

			// managed_channels: rebuild so that uniqueness is per account rather
			// than per server definition. The old index goes first so its name is
			// free again after the copy.
			`DROP INDEX IF EXISTS managed_channels_client_channel_idx`,
			`ALTER TABLE managed_channels RENAME TO managed_channels_pre_v21`,
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
	verified INTEGER NOT NULL DEFAULT 0 CHECK (verified IN (0, 1)),
	scam INTEGER NOT NULL DEFAULT 0 CHECK (scam IN (0, 1)),
	fake INTEGER NOT NULL DEFAULT 0 CHECK (fake IN (0, 1)),
	notices_known INTEGER NOT NULL DEFAULT 0 CHECK (notices_known IN (0, 1)),
	avatar_available INTEGER NOT NULL DEFAULT 0 CHECK (avatar_available IN (0, 1)),
	permission_new INTEGER NOT NULL DEFAULT 0 CHECK (permission_new IN (0, 1)),
	permission_edit INTEGER NOT NULL DEFAULT 0 CHECK (permission_edit IN (0, 1)),
	permission_delete INTEGER NOT NULL DEFAULT 0 CHECK (permission_delete IN (0, 1)),
	permission_read INTEGER NOT NULL DEFAULT 0 CHECK (permission_read IN (0, 1)),
	permissions_known INTEGER NOT NULL DEFAULT 0 CHECK (permissions_known IN (0, 1)),
	account_id TEXT NOT NULL REFERENCES telegram_accounts (id) ON DELETE CASCADE,
	UNIQUE (account_id, channel_id)
)`,
			// Every legacy channel is moved onto the quarantined account of the
			// client key it was registered under, so no channel is left without
			// an owner and none is handed to a user by guessing. A quarantined
			// account is reached through its legacy key and never through a
			// user's account listing.
			`INSERT INTO telegram_accounts (
	id, owner_user_id, server_key, network_type, remote_user_id,
	remote_username, remote_display_name, phone_number_masked, status,
	session_enc_version, session_enc_key_version, session_storage_ref,
	last_error_code, created_at, updated_at)
SELECT DISTINCT
	'legacy_' || client_key, NULL, client_key, '', 0,
	'', '', '', 'quarantined',
	0, 1, '',
	'', strftime('%Y-%m-%dT%H:%M:%fZ', 'now'), strftime('%Y-%m-%dT%H:%M:%fZ', 'now')
FROM managed_channels_pre_v21`,
			`INSERT INTO managed_channels (
	id, client_key, channel_id, peer_type, title, username, description,
	avatar_photo_id, membership_status, participants_count,
	created_at, updated_at, last_synced_at,
	verified, scam, fake, notices_known, avatar_available,
	permission_new, permission_edit, permission_delete, permission_read, permissions_known,
	account_id)
SELECT
	id, client_key, channel_id, peer_type, title, username, description,
	avatar_photo_id, membership_status, participants_count,
	created_at, updated_at, last_synced_at,
	verified, scam, fake, notices_known, avatar_available,
	permission_new, permission_edit, permission_delete, permission_read, permissions_known,
	'legacy_' || client_key FROM managed_channels_pre_v21`,
			`DROP TABLE managed_channels_pre_v21`,
			`CREATE INDEX managed_channels_client_channel_idx ON managed_channels (client_key, channel_id)`,
			`CREATE INDEX managed_channels_account_idx ON managed_channels (account_id)`,

			// The same ownership column everywhere a Telegram identity is named.
			// Nullable and never backfilled by guessing: a row that predates the
			// account says so with NULL instead of claiming an owner.
			`ALTER TABLE webhooks ADD COLUMN account_id TEXT REFERENCES telegram_accounts (id) ON DELETE CASCADE`,
			// owner_user_id is the Telesync user the webhook belongs to. It is
			// denormalised beside account_id because every delivery read needs
			// both, and a join to resolve one would be on the hot path of every
			// history page.
			`ALTER TABLE webhooks ADD COLUMN owner_user_id INTEGER`,
			`CREATE INDEX webhooks_account_idx ON webhooks (account_id)`,
			`ALTER TABLE webhook_sources ADD COLUMN account_id TEXT REFERENCES telegram_accounts (id) ON DELETE CASCADE`,
			`CREATE INDEX webhook_sources_account_idx ON webhook_sources (account_id)`,
			`ALTER TABLE workflows ADD COLUMN account_id TEXT REFERENCES telegram_accounts (id) ON DELETE CASCADE`,
			// owner_user_id is the user the workflow belongs to. A workflow is one
			// per user-owned channel registration, so its owner is the owner of
			// that registration.
			`ALTER TABLE workflows ADD COLUMN owner_user_id INTEGER`,
			`CREATE INDEX workflows_account_idx ON workflows (account_id)`,
			// automation_events is rebuilt rather than altered. Its uniqueness
			// is the event deduplication key, and the old key — (type,
			// network_key, telegram_channel_id, message_id) — is exactly the
			// bug this release fixes: two users watching the same remote channel
			// would see the same Telegram message, and the second user's copy
			// was dropped as a duplicate of the first. Ownership is now part of
			// the identity, so each account's observation is its own event.
			// SQLite cannot drop a table-level UNIQUE constraint by ALTER, so
			// the table is rebuilt the way migration 18 rebuilt automation_jobs.
			`CREATE TABLE automation_events_rebuilt (
	id TEXT PRIMARY KEY,
	type TEXT NOT NULL CHECK (length(trim(type)) > 0),
	network_key TEXT NOT NULL CHECK (length(trim(network_key)) > 0),
	source_client_key TEXT NOT NULL CHECK (length(trim(source_client_key)) > 0),
	account_id TEXT REFERENCES telegram_accounts (id) ON DELETE CASCADE,
	source_account_id TEXT,
	owner_user_id INTEGER,
	managed_channel_id INTEGER NOT NULL REFERENCES managed_channels (id) ON DELETE CASCADE,
	telegram_channel_id INTEGER NOT NULL CHECK (telegram_channel_id <> 0),
	message_id INTEGER NOT NULL CHECK (message_id > 0),
	grouped_id INTEGER,
	occurred_at TEXT NOT NULL,
	received_at TEXT NOT NULL,
	payload TEXT NOT NULL DEFAULT '',
	created_at TEXT NOT NULL,
	UNIQUE (account_id, type, network_key, telegram_channel_id, message_id))`,
			`INSERT INTO automation_events_rebuilt (
	id, type, network_key, source_client_key, managed_channel_id,
	telegram_channel_id, message_id, grouped_id, occurred_at, received_at,
	payload, created_at)
SELECT id, type, network_key, source_client_key, managed_channel_id,
	telegram_channel_id, message_id, grouped_id, occurred_at, received_at,
	payload, created_at FROM automation_events`,
			`DROP TABLE automation_events`,
			`ALTER TABLE automation_events_rebuilt RENAME TO automation_events`,
			`CREATE INDEX automation_events_channel_idx ON automation_events (managed_channel_id, message_id)`,
			`CREATE INDEX automation_events_received_idx ON automation_events (received_at)`,
			`CREATE INDEX automation_events_account_idx ON automation_events (account_id)`,
			// This trigger is dropped with the old table and must come back: it
			// is what makes deleting an event take its jobs with it.
			`CREATE TRIGGER automation_jobs_event_delete
	AFTER DELETE ON automation_events
	BEGIN
		DELETE FROM automation_jobs
		WHERE event_id = OLD.id AND job_type IN ('webhook_delivery', 'workflow_run');
	END`,
			`ALTER TABLE automation_event_sources ADD COLUMN account_id TEXT REFERENCES telegram_accounts (id) ON DELETE CASCADE`,
			`CREATE INDEX automation_event_sources_account_idx ON automation_event_sources (account_id)`,
			`ALTER TABLE automation_jobs ADD COLUMN account_id TEXT REFERENCES telegram_accounts (id) ON DELETE CASCADE`,
			// owner_user_id is the Telesync user the work runs for. It is
			// nullable only so a row written before accounts existed can still
			// be stored; such a row names no account either and is refused at
			// the queue door rather than run as somebody else.
			`ALTER TABLE automation_jobs ADD COLUMN owner_user_id INTEGER`,
			// paused is what a disconnected account leaves behind: its jobs are
			// not lost and not silently dropped, they wait until the account is
			// usable again.
			`ALTER TABLE automation_jobs ADD COLUMN paused INTEGER NOT NULL DEFAULT 0 CHECK (paused IN (0, 1))`,
			// Every job predates account ownership at this point. Leave it
			// durable but paused until an operator establishes its owner.
			`UPDATE automation_jobs SET paused = 1 WHERE account_id IS NULL`,
			`CREATE INDEX automation_jobs_account_idx ON automation_jobs (account_id)`,
			`ALTER TABLE automation_update_states ADD COLUMN account_id TEXT REFERENCES telegram_accounts (id) ON DELETE CASCADE`,
			`CREATE INDEX automation_update_states_account_idx ON automation_update_states (account_id)`,
			`ALTER TABLE automation_channel_pts ADD COLUMN account_id TEXT REFERENCES telegram_accounts (id) ON DELETE CASCADE`,
			`CREATE INDEX automation_channel_pts_account_idx ON automation_channel_pts (account_id)`,
			`ALTER TABLE automation_peer_hashes ADD COLUMN account_id TEXT REFERENCES telegram_accounts (id) ON DELETE CASCADE`,
			`CREATE INDEX automation_peer_hashes_account_idx ON automation_peer_hashes (account_id)`,
			`ALTER TABLE publish_operations ADD COLUMN account_id TEXT REFERENCES telegram_accounts (id) ON DELETE CASCADE`,
			`CREATE INDEX publish_operations_account_idx ON publish_operations (account_id)`,
			`ALTER TABLE message_send_operations ADD COLUMN account_id TEXT REFERENCES telegram_accounts (id) ON DELETE CASCADE`,
			`CREATE INDEX message_send_operations_account_idx ON message_send_operations (account_id)`,
			`ALTER TABLE message_edit_operations ADD COLUMN account_id TEXT REFERENCES telegram_accounts (id) ON DELETE CASCADE`,
			`CREATE INDEX message_edit_operations_account_idx ON message_edit_operations (account_id)`,
			`ALTER TABLE channel_creation_operations ADD COLUMN account_id TEXT REFERENCES telegram_accounts (id) ON DELETE CASCADE`,
			`CREATE INDEX channel_creation_operations_account_idx ON channel_creation_operations (account_id)`,
		},
	},
	{
		version:                 22,
		name:                    "account_scoped_workflow_destinations_and_categories",
		rebuildsReferencedTable: true,
		stmts: []string{
			`ALTER TABLE workflow_destinations ADD COLUMN account_id TEXT REFERENCES telegram_accounts (id) ON DELETE NO ACTION`,
			`UPDATE workflow_destinations
			 SET account_id = (
			   SELECT MIN(m.account_id)
			   FROM managed_channels m
			   JOIN workflows w ON w.id = workflow_destinations.workflow_id
			   JOIN telegram_accounts a ON a.id = m.account_id
			   WHERE m.client_key = workflow_destinations.client_key
			     AND m.channel_id = workflow_destinations.channel_id
			     AND a.owner_user_id = w.owner_user_id
			     AND a.deleted_at IS NULL
			   HAVING COUNT(DISTINCT m.account_id) = 1
			 )
			 WHERE destination_type = 'telegram'`,
			`CREATE TRIGGER workflow_destinations_account_insert
			 BEFORE INSERT ON workflow_destinations
			 WHEN (NEW.destination_type = 'telegram' AND NEW.account_id IS NULL)
			   OR (NEW.destination_type = 'wordpress' AND NEW.account_id IS NOT NULL)
			 BEGIN SELECT RAISE(ABORT, 'workflow destination account mismatch'); END`,
			`CREATE TRIGGER workflow_destinations_account_update
			 BEFORE UPDATE OF destination_type, account_id ON workflow_destinations
			 WHEN (NEW.destination_type = 'telegram' AND NEW.account_id IS NULL)
			   OR (NEW.destination_type = 'wordpress' AND NEW.account_id IS NOT NULL)
			 BEGIN SELECT RAISE(ABORT, 'workflow destination account mismatch'); END`,
			`CREATE TABLE workflow_publications_rebuilt (
			 id INTEGER PRIMARY KEY AUTOINCREMENT,
			 workflow_run_id INTEGER NOT NULL REFERENCES workflow_runs (id) ON DELETE CASCADE,
			 destination_type TEXT NOT NULL CHECK (destination_type IN ('telegram', 'wordpress')),
			 client_key TEXT NOT NULL DEFAULT '',
			 channel_id INTEGER NOT NULL DEFAULT 0,
			 site_id INTEGER NOT NULL DEFAULT 0,
			 status TEXT NOT NULL CHECK (status IN ('pending', 'published', 'failed', 'unknown')),
			 remote_message_id INTEGER NOT NULL DEFAULT 0,
			 remote_post_id INTEGER NOT NULL DEFAULT 0,
			 remote_url TEXT NOT NULL DEFAULT '',
			 attempt_count INTEGER NOT NULL DEFAULT 0,
			 last_error_code TEXT NOT NULL DEFAULT '',
			 last_error_message TEXT NOT NULL DEFAULT '',
			 created_at TEXT NOT NULL,
			 completed_at TEXT,
			 account_id TEXT REFERENCES telegram_accounts (id) ON DELETE NO ACTION)`,
			`INSERT INTO workflow_publications_rebuilt
			 (id, workflow_run_id, destination_type, client_key, channel_id, site_id, status,
			  remote_message_id, remote_post_id, remote_url, attempt_count, last_error_code,
			  last_error_message, created_at, completed_at)
			 SELECT id, workflow_run_id, destination_type, client_key, channel_id, site_id, status,
			  remote_message_id, remote_post_id, remote_url, attempt_count, last_error_code,
			  last_error_message, created_at, completed_at FROM workflow_publications`,
			`UPDATE workflow_publications_rebuilt
			 SET account_id = (
			  SELECT MIN(d.account_id)
			  FROM workflow_runs r
			  JOIN workflow_destinations d ON d.workflow_id = r.workflow_id
			  WHERE r.id = workflow_publications_rebuilt.workflow_run_id
			    AND d.destination_type = 'telegram'
			    AND d.client_key = workflow_publications_rebuilt.client_key
			    AND d.channel_id = workflow_publications_rebuilt.channel_id
			  HAVING COUNT(DISTINCT d.account_id) = 1
			 )
			 WHERE destination_type = 'telegram'`,
			`DROP TABLE workflow_publications`,
			`ALTER TABLE workflow_publications_rebuilt RENAME TO workflow_publications`,
			`CREATE UNIQUE INDEX workflow_publications_destination_idx ON workflow_publications
			 (workflow_run_id, destination_type, COALESCE(account_id, ''), client_key, channel_id, site_id)`,
			`CREATE INDEX workflow_publications_remote_idx ON workflow_publications (destination_type, client_key, channel_id, remote_message_id)`,
			`CREATE INDEX workflow_publications_account_idx ON workflow_publications (account_id)`,
			`CREATE TABLE channel_categories_rebuilt (
			 client_key TEXT NOT NULL,
			 channel_id TEXT NOT NULL,
			 category_id INTEGER NOT NULL REFERENCES categories (id) ON DELETE CASCADE,
			 created_at TEXT NOT NULL,
			 account_id TEXT NOT NULL REFERENCES telegram_accounts (id) ON DELETE CASCADE,
			 PRIMARY KEY (account_id, channel_id, category_id))`,
			`INSERT OR IGNORE INTO telegram_accounts
			 (id, owner_user_id, server_key, network_type, status, created_at, updated_at)
			 SELECT DISTINCT 'legacy_' || cc.client_key, NULL, cc.client_key, '',
			  'quarantined', cc.created_at, cc.created_at
			 FROM channel_categories cc
			 WHERE NOT EXISTS (SELECT 1 FROM telegram_accounts a WHERE a.id = 'legacy_' || cc.client_key)`,
			`INSERT INTO channel_categories_rebuilt (client_key, channel_id, category_id, created_at, account_id)
			 SELECT cc.client_key, cc.channel_id, cc.category_id, cc.created_at,
			  COALESCE(
			   (SELECT MIN(m.account_id) FROM managed_channels m
			    WHERE m.client_key = cc.client_key AND CAST(m.channel_id AS TEXT) = cc.channel_id
			    HAVING COUNT(DISTINCT m.account_id) = 1),
			   'legacy_' || cc.client_key)
			 FROM channel_categories cc`,
			`DROP TABLE channel_categories`,
			`ALTER TABLE channel_categories_rebuilt RENAME TO channel_categories`,
			`CREATE INDEX channel_categories_account_idx ON channel_categories (account_id, channel_id)`,
		},
	},
	{
		version: 23,
		name:    "account_scoped_publish_idempotency",
		stmts: []string{
			// Earlier attempts are safely bound only when their stored
			// destination identity begins with an account owned by that same
			// user. Unresolved legacy rows remain NULL and are never guessed.
			`UPDATE publish_operations
			 SET account_id = substr(destination_id, 1, instr(destination_id, ':') - 1)
			 WHERE account_id IS NULL
			   AND destination_type = 'telegram'
			   AND instr(destination_id, ':') > 1
			   AND substr(destination_id, 1, 4) = 'acc_'
			   AND EXISTS (
			     SELECT 1 FROM telegram_accounts a
			     WHERE a.id = substr(publish_operations.destination_id, 1,
			       instr(publish_operations.destination_id, ':') - 1)
			       AND a.owner_user_id = publish_operations.user_id
			   )`,
			`ALTER TABLE publish_operations RENAME TO publish_operations_v22`,
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
			 account_id TEXT REFERENCES telegram_accounts (id) ON DELETE CASCADE)`,
			`INSERT INTO publish_operations
			 (id, user_id, idempotency_key, request_hash, destination_type, destination_id,
			  status, result, error_code, created_at, updated_at, account_id)
			 SELECT id, user_id, idempotency_key, request_hash, destination_type, destination_id,
			  status, result, error_code, created_at, updated_at, account_id
			 FROM publish_operations_v22`,
			`DROP TABLE publish_operations_v22`,
			`CREATE INDEX publish_operations_user_idx ON publish_operations (user_id, created_at)`,
			`CREATE INDEX publish_operations_account_idx ON publish_operations (account_id)`,
			`CREATE UNIQUE INDEX publish_operations_user_account_key_idx ON publish_operations
			 (user_id, COALESCE(account_id, ''), idempotency_key)`,
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
	// The connection is pinned so that a pragma set around the transaction is
	// the one the transaction runs under: the pool is capped at one connection,
	// but a pragma is per connection and foreign_keys is a no-op inside a
	// transaction.
	conn, err := s.db.Conn(ctx)
	if err != nil {
		return fmt.Errorf("channelstore: migrate to %d (%s): %w", m.version, m.name, err)
	}
	defer conn.Close()

	if m.rebuildsReferencedTable {
		for _, stmt := range []string{`PRAGMA legacy_alter_table=ON`, `PRAGMA foreign_keys=OFF`} {
			if _, err := conn.ExecContext(ctx, stmt); err != nil {
				return fmt.Errorf("channelstore: migrate to %d (%s): %w", m.version, m.name, err)
			}
		}
		defer func() {
			_, _ = conn.ExecContext(context.Background(), `PRAGMA foreign_keys=ON`)
			_, _ = conn.ExecContext(context.Background(), `PRAGMA legacy_alter_table=OFF`)
		}()
	}

	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("channelstore: migrate to %d (%s): %w", m.version, m.name, err)
	}
	defer func() { _ = tx.Rollback() }()

	for _, stmt := range m.stmts {
		if _, err := tx.ExecContext(ctx, stmt); err != nil {
			return fmt.Errorf("channelstore: migrate to %d (%s): %w", m.version, m.name, err)
		}
	}
	if m.rebuildsReferencedTable {
		// Enforcement was off for the copy, so what it left behind is checked
		// here and the migration fails rather than record a broken reference.
		if err := requireNoForeignKeyViolations(ctx, tx); err != nil {
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

// requireNoForeignKeyViolations fails when the schema holds a row that points at
// a parent row that is not there.
func requireNoForeignKeyViolations(ctx context.Context, tx *sql.Tx) error {
	rows, err := tx.QueryContext(ctx, `PRAGMA foreign_key_check`)
	if err != nil {
		return err
	}
	defer rows.Close()
	var offenders []string
	for rows.Next() {
		var table, parent, rowid, fkid sql.NullString
		if err := rows.Scan(&table, &rowid, &parent, &fkid); err != nil {
			return err
		}
		offenders = append(offenders, fmt.Sprintf("%s -> %s", table.String, parent.String))
		if len(offenders) == 5 {
			break
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}
	if len(offenders) > 0 {
		return fmt.Errorf("foreign key check reports dangling references: %v", offenders)
	}
	return nil
}

// SchemaVersion reports the applied migration version.
func (s *Store) SchemaVersion(ctx context.Context) (int, error) {
	return s.currentVersion(ctx)
}
