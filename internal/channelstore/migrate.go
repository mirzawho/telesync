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
