# v0.0.5 canonical contracts — user-owned Telegram/Cat accounts

Binding contracts for the v0.0.5 refactor. **No agent may design a competing
model.** Where this file and an agent's instinct disagree, this file wins and
the disagreement is reported, not resolved locally.

Written by the lead orchestrator from the Phase 0 inventory (§C of
`.agents/handoffs/v0.0.5-personal-telegram-accounts.md`). Every load-bearing
claim below was read from source, not inferred from filenames.

---

## 0. The one decision that makes this tractable

`managed_channels.client_key` **already holds the server key** (`official`,
`cat`) — verified at `internal/channelstore/migrate.go:25`. That is exactly the
brief's Decision A: the server definition stays global.

Therefore **ownership is an added dimension, not a rename**:

- `client_key` keeps meaning *which server definition* (`official` | `cat`).
  The Channel Explorer server filter, `runtime.Servers`, and the
  `gateway.Registry` server listing keep working untouched.
- `account_id` is the **new** column carrying *which user-owned authenticated
  session* the row belongs to.

This avoids rewriting the 648 `client_key` references in Go. Those that mean
"which server" stay correct. Those that mean "which Telegram identity to act
as" must resolve to an `account_id` — that resolution is one seam
(`AccountResolver`), not 648 edits.

`UNIQUE (client_key, channel_id)` becomes `UNIQUE (account_id, channel_id)`,
which is precisely the brief's §15 "two users independently subscribe to the
same remote channel".

---

## 1. Identity vocabulary

| Name | Meaning | Format | Scope |
|---|---|---|---|
| `server_key` | Server definition | `official`, `cat` (from config) | global, admin-managed |
| `account_id` | User-owned authenticated account | `acc_` + 26 base32 chars | opaque, stable, never reused |
| `owner_user_id` | Telesync user id | `users.id` (`INTEGER`, `int64`) | FK → `users(id)` |
| `remote_user_id` | Telegram user id | `int64` | unique per `(server_key, remote_user_id)` **per owner** |
| `local_channel_id` | `managed_channels.id` | `INTEGER` | authoritative for permissions |
| `remote_channel_id` | Telegram channel id | `int64` | **never** authoritative alone |

Rules that are non-negotiable:

- `account_id` is the **authoritative session identity**. Never a username, a
  phone number, or a `server_key`.
- `account_id` is opaque and server-generated. A client-supplied
  `owner_user_id` is **never** an authorization decision.
- `remote_user_id` is scoped to its network/server, and uniqueness is
  **owner-scoped** (§5): two different users authenticating the same remote
  identity get two separate accounts with two separate sessions. No global
  uniqueness constraint may force a shared session.

ID generation: `acc_` + 26 chars from `crypto/rand` base32 (Crockford,
lowercased), matching the repo's existing "32 hex characters this server chose"
staging convention in spirit — a value no client chooses or predicts.

---

## 2. Database contract — migration 21 (Agent A only)

Appended to `internal/channelstore/migrate.go`'s `migrations` slice as
`version: 21, name: "personal_telegram_accounts"`. **Never edit versions 1–20.**

### 2.1 `telegram_accounts`

```sql
CREATE TABLE telegram_accounts (
    id                      TEXT PRIMARY KEY,              -- account_id, opaque
    owner_user_id           INTEGER NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    server_key              TEXT NOT NULL,                 -- 'official' | 'cat'
    network_type            TEXT NOT NULL,                 -- 'telegram' | 'custom'

    remote_user_id          INTEGER NOT NULL DEFAULT 0,
    remote_username         TEXT NOT NULL DEFAULT '',
    remote_display_name     TEXT NOT NULL DEFAULT '',
    phone_number_masked     TEXT NOT NULL DEFAULT '',      -- masked only, never full

    status                  TEXT NOT NULL
        CHECK (status IN ('connecting','connected','disconnected','reconnecting',
                          'authentication_required','disabled','error','removed')),

    -- Sealed AES-256-GCM box; version/nonce/ciphertext columns mirror
    -- wordpress_sites' credential_* pattern exactly.
    session_enc_version     INTEGER NOT NULL DEFAULT 0,
    session_nonce           BLOB,
    session_ciphertext      BLOB,
    session_enc_key_version INTEGER NOT NULL DEFAULT 1,
    session_storage_ref     TEXT NOT NULL DEFAULT '',      -- opaque, never a path to the browser

    created_at              TEXT NOT NULL,
    authorized_at           TEXT,
    last_connected_at       TEXT,
    last_disconnected_at    TEXT,
    last_error_code         TEXT NOT NULL DEFAULT '',
    updated_at              TEXT NOT NULL,
    deleted_at              TEXT
);

-- Owner-scoped dedup of the same remote identity on the same server.
-- NOT global: two owners may hold the same remote identity separately.
CREATE UNIQUE INDEX telegram_accounts_owner_remote_idx
    ON telegram_accounts (owner_user_id, server_key, remote_user_id)
    WHERE remote_user_id != 0 AND deleted_at IS NULL;

CREATE INDEX telegram_accounts_owner_idx ON telegram_accounts (owner_user_id);
CREATE INDEX telegram_accounts_status_idx ON telegram_accounts (status);
```

Invariants the index enforces: every account has an owner; every account names
one server definition; a remote identity may not be registered twice **by the
same owner** on the same server; nothing blocks two owners from holding the
same remote identity.

### 2.2 `managed_channels` ownership (forward-only ALTER)

```sql
ALTER TABLE managed_channels ADD COLUMN account_id TEXT
    REFERENCES telegram_accounts (id) ON DELETE CASCADE;
CREATE INDEX managed_channels_account_idx ON managed_channels (account_id);
```

`client_key` is **kept** and continues to hold the server key. The old
`UNIQUE (client_key, channel_id)` cannot be dropped by `ALTER` in SQLite; the
migration rebuilds `managed_channels` (the established pattern — migration 18
already rebuilds `automation_jobs`) into a table whose uniqueness is
`UNIQUE (account_id, channel_id)`, preserving `id` values so no foreign key in
`user_channels_permission`, `channel_categories`, `automation_*` or `webhook_*`
is disturbed.

Rows with `account_id IS NULL` are **legacy, quarantined** — see §7.

### 2.3 Ownership columns on dependent tables

All are nullable `TEXT REFERENCES telegram_accounts (id)`, all keep existing
data, none is backfilled by guessing:

- `webhooks.account_id`, `webhook_sources.account_id`
- `workflows.account_id`
- `automation_events.account_id`, `automation_event_sources.account_id`
- `automation_jobs.account_id`, `automation_update_states.account_id`,
  `automation_channel_pts.account_id`, `automation_peer_hashes.account_id`
- `publish_operations.account_id`, `message_send_operations.account_id`,
  `message_edit_operations.account_id`, `channel_creation_operations.account_id`

Plus `login_attempts` (§4) and `legacy_session_adoptions` (§7).

### 2.4 Migration tests (Agent A)

- Applying 1→21 on an empty DB succeeds; `schema_version` is 21.
- Applying 1→20, seeding representative rows, then 21 preserves every `id`,
  every `user_channels_permission` row and every category assignment.
- Two owners may hold `(server_key='official', remote_user_id=1)`; one owner
  may not hold it twice.
- `(account_id, channel_id)` is unique; `(client_key, channel_id)` no longer is.
- A quarantined row (`account_id IS NULL`) is never returned by account-scoped
  reads.

---

## 3. Session encryption contract (Agent B owns the code)

**Reuse `internal/secrets`.** It is already AES-256-GCM with a versioned `Box`
and an env-sourced key, and the WordPress rule mandates exactly that
construction. Do not add a cipher.

Two deliberate extensions, both narrow:

1. **AAD binds the box to the account.** `secrets.Encrypter.Seal` takes no
   additional data (verified: `secrets.go:81`). Sessions need it, because §11
   requires the encryption context to be bound to account identity and the
   §43 matrix requires "wrong encryption context cannot decrypt another
   account's session". Add `SealWithAAD(plaintext string, aad []byte)` /
   `OpenWithAAD(box Box, aad []byte)` beside the existing pair — same AEAD,
   same nonce policy, same `ErrDecrypt` conflation. The AAD is
   `account_id || "\x00" || key_version`. `Seal`/`Open` keep working unchanged
   for WordPress.
2. **Key versioning.** `Box` already has `Version`; the account row carries
   `session_enc_key_version` so rotation is a re-seal, not a migration.

Configuration:

```
TELESYNC_SESSION_ENCRYPTION_KEY=<base64|hex|raw 32 bytes>
```

Loaded through the existing `secrets.ParseKey` / `secrets.KeyFromEnv`. Generate
with `openssl rand -base64 32` (the message `ParseKey` already prints).

Failure policy, non-negotiable:

- Production, key missing → **refuse to start** (`ErrKeyNotSet`). Never an
  ephemeral per-startup key: that would silently orphan every persisted
  session. The brief (§11) calls this out explicitly.
- Never store, log, return or URL-encode session bytes. Never in JSON.
- 2FA passwords and login codes are **transient only** — never persisted,
  never logged, never in analytics.

Session storage stays per-account. `sessionstore.Store` (one file, 0600 in a
0700 dir) is reused as the **bytes** transport; the sealed box is what lands
there. One account's file is never opened by another worker.

---

## 4. Login state machine contract (Agent B)

Persisted, bounded, owner-scoped — **not** a goroutine. Table `login_attempts`:

```
id TEXT PRIMARY KEY,               -- 'lga_' + 26 chars, opaque
owner_user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
account_id TEXT REFERENCES telegram_accounts(id) ON DELETE CASCADE,
server_key TEXT NOT NULL,
method TEXT NOT NULL CHECK (method IN ('phone','qr')),
state TEXT NOT NULL CHECK (state IN (
    'created','connecting','waiting_for_code','waiting_for_password',
    'waiting_for_qr','authorized','failed','expired','canceled')),
phone_number_masked TEXT NOT NULL DEFAULT '',
next_step TEXT NOT NULL DEFAULT '',
resend_after TEXT,
expires_at TEXT NOT NULL,
last_error_code TEXT NOT NULL DEFAULT '',
created_at TEXT NOT NULL, updated_at TEXT NOT NULL
```

States and required transitions:

| From | To | Trigger |
|---|---|---|
| `created` | `connecting` | client connects |
| `connecting` | `waiting_for_code` | `auth.sentCode` accepted |
| `connecting` | `waiting_for_qr` | QR token issued |
| `waiting_for_code` | `waiting_for_password` | 2FA required |
| `waiting_for_code` | `authorized` | code accepted |
| `waiting_for_password` | `authorized` | password accepted |
| `waiting_for_qr` | `authorized` | remote confirmation |
| any | `failed` / `expired` / `canceled` | terminal |

Requirements: attempts expire (`expires_at`, `TELESYNC_LOGIN_ATTEMPT_TTL`,
default `10m`); an attempt is owner-scoped and another user may not inspect,
complete, cancel or hijack it; code submission is rate-limited
(`TELESYNC_LOGIN_RATE_LIMIT`, default 5); invalid transitions are rejected;
an authorized attempt cannot be reused; cancellation releases its MTProto
resources; a restart leaves no orphaned permanent session; a new attempt never
overwrites an already-authorized account.

**Delivery method must be accurate (§7).** Report from `auth.sentCode`'s actual
type. Never claim "SMS sent" unconditionally.

**2FA**: use gotd's SRP helpers. Never a hand-rolled verifier. Never persist the
password.

**QR**: real MTProto QR login (`gotd` QR flow) with short-lived token, display,
expiry/refresh, and confirmation handling. If a server cannot do QR, return an
explicit unsupported-method error and hide it in the UI — never block phone
login, and never silently authenticate against official Telegram instead.

Telesync web auth and MTProto auth stay **two separate systems**. Do not build a
second application-login framework.

---

## 5. Worker identity & RPC contract (Agent C)

Worker identity becomes the account, not the client key:

```
old: worker(official), worker(cat)
new: worker(acc_xxx), worker(acc_yyy), ...
```

Every worker context carries `account_id`, `owner_user_id`, `server_key`,
network identity, sealed-session reference, and the authenticated remote user
id — **from the verified account record**, never from an untrusted RPC payload
field.

`gateway.Registry.Worker(key)` is the single routing seam
(`internal/gateway/gateway.go:65`). Its key becomes `account_id`. All Telegram
routes resolve the account first (§6), then call `Worker(account_id)`.

Socket/session/peer paths are derived per account and must stay inside the
existing guards (`appconfig` already refuses a socket outside `RunDir`, two
clients sharing a session path, or a session path inside `RunDir`). Name them
from the **short opaque id**, never from a username or phone:

```
<RunDir>/<account_id>.sock        -- hashed/short, under the platform path limit
<SessionDir>/<account_id>.box     -- sealed session
<PeerDir>/<account_id>.json       -- account-scoped peer cache
```

Isolation requirements: no worker reads another account's session; no worker
claims another account's jobs; peer caches, channel resolution, update state
and temp media are account-scoped; logs carry only opaque account ids.

Lifecycle: start after successful login; reconnect saved sessions at startup;
graceful shutdown; session expiry; logout; disabled user; crash recovery;
account removal. Bounded by `TELESYNC_ACCOUNT_MAX_WORKERS` (default 50) with
`TELESYNC_ACCOUNT_RECONNECT_BACKOFF` (default 5s). Accounts with active
automation are **not** disconnected just because a browser closed.

Update state (§14) is persisted per `(account_id, server_key, channel)`. Never
one global `pts`. Each account recovers its own gap after reconnect, dedups in
its own scope, and resumes its own background work. No user receives another
user's events.

---

## 6. API & authorization contract (Agent D)

### 6.1 Account-scoped routes (canonical)

```
GET    /api/v1/me/telegram-accounts
GET    /api/v1/me/telegram-accounts/{account_id}
POST   /api/v1/me/telegram-accounts/login/start
GET    /api/v1/me/telegram-accounts/login/{attempt_id}
POST   /api/v1/me/telegram-accounts/login/{attempt_id}/code
POST   /api/v1/me/telegram-accounts/login/{attempt_id}/password
POST   /api/v1/me/telegram-accounts/login/{attempt_id}/cancel
POST   /api/v1/me/telegram-accounts/{account_id}/disconnect
POST   /api/v1/me/telegram-accounts/{account_id}/logout
DELETE /api/v1/me/telegram-accounts/{account_id}
```

Resource routes get an account-scoped family where identity must be explicit:

```
/api/v1/telegram/accounts/{account_id}/channels/{channel_id}/...
```

### 6.2 Authorization pipeline (every account operation)

```
authenticated principal → resolve account_id → verify ownership
                      → verify application permission → account's worker
```

Owner-scoped SQL, always:

```sql
SELECT * FROM telegram_accounts WHERE id = ? AND owner_user_id = ?
```

Never a bare `WHERE id = ?`. Never trust a client-supplied `owner_user_id`.
Never resolve an account from `server_key` alone. Never fall back to a central
session, another user's account, or "the first administrator".

### 6.3 Legacy keyed-route compatibility

`POST /api/v1/telegram/{key}/...` keeps working, resolving against **the
authenticated user's own accounts on that server**:

| Situation | Result |
|---|---|
| exactly one eligible account for `key` | resolve it |
| multiple eligible accounts | `400 account_selection_required` |
| no eligible account | `404`/`409` — never a fallback |
| account not owned by principal | `404` (not `403`, to avoid ID oracle) |

Error codes to add: `account_selection_required`, `account_not_owned`,
`account_not_connected`, `login_attempt_expired`, `login_attempt_invalid`,
`login_rate_limited`, `unsupported_login_method`, `session_encryption_unavailable`.
Route/contract changes require docsgen + `internal/apidocs` (skill
`.agents/skills/api-documentation/SKILL.md`); generated pages are never
hand-edited.

### 6.4 What stays

Users, roles, categories, WordPress sites, WordPress publishing permissions,
and channel Telegram capabilities (`read`/`new`/`edit`/`delete` — real MTProto
rights, §32) are all preserved. Only the shared Telegram access-assignment
mechanism is retired, in the backend **and** the UI — never UI-only.

Application roles stay separate from Telegram rights. An admin manages users
and configuration and must **not** gain another user's messages or session
secrets.

---

## 7. Legacy migration contract (Agent A + orchestrator)

Phase A inventory is §C of the handoff. Disposition rules:

- Legacy central sessions (`official`, `cat` files under `SessionDir`) are
  **quarantined legacy resources**, not shared with anyone. A row with
  `account_id IS NULL` is quarantined and excluded from account-scoped reads.
- Adoption is **explicit and auditable**, operator-driven, recorded in
  `legacy_session_adoptions(legacy_key, account_id, adopted_by, adopted_at,
  reason)`. No automatic assignment to administrators.
- Dependent resources (channels, automations, pending jobs) move **only** once
  ownership is established. Never reroute a job through another account.
- Operator tokens that do not represent a real owner get **no** personal
  session; their automation stays disabled until explicitly resolved.
- Never delete encrypted legacy session bytes in the initial migration.
- `TELESYNC_LEGACY_ACCOUNT_EXECUTION=false` — the old shared-session execution
  path is off at cutover, and the flag is the rollback lever.

Rollback: DB backup first, forward-only migrations with a documented
validation procedure before any legacy path is removed.

---

## 8. File ownership (strict — two writers never touch one file)

| Agent | Exclusive ownership | May not touch |
|---|---|---|
| **A** DB & Migration | `internal/channelstore/migrate.go`, `internal/channelstore/telegram_accounts*.go`, `internal/channelstore/login_attempts*.go`, `internal/channelstore/store.go` (the one `managed_channels` upsert, per R1), tests for those | everything else |
| **B** Auth & Session | `internal/accountauth/**`, `internal/sessionstore/**`, `internal/login/**`, `internal/secrets/**` (AAD only) | migrations, frontend |
| **C** Workers | `internal/supervisor/**`, `internal/worker/**`, `internal/rpc/**`, `internal/gotdclient/manager.go` + `updates.go` + their tests | migrations, frontend |
| **D** API & Authz | `internal/gateway/**`, `internal/runtime/**`, and `internal/channelstore/{store.go,local_channels.go,categories.go,category_channels.go,permissions.go,users.go,wordpress*.go}` **read/ownership methods only** | migrations, frontend |
| **E** Automation & Jobs | `internal/automation/**`, `internal/sending/**`, `internal/staging/**`, and `internal/channelstore/{automation_*.go,webhooks*.go,workflows*.go,publish_operations.go,send_operations.go,message_edit_operations.go,channel_creation_operations.go}` | migrations, frontend |
| **F** Frontend | `web/**` | all Go |
| **Lead** | this file, `.agents/handoffs/v0.0.5-*`, `cmd/**`, release docs, integration | — |

### Sequencing constraint (why D and E wait for A)

`internal/channelstore` is one package but must be written by three hands. The
schema (migration 21) and `AccountStore` are the foundation, so **A completes
first**. Only then do D and E receive their file slices above. Two writers never
touch one file; where D and E both need a store method, it is added in the
slice's own file and the lead reconciles.

`internal/channelstore/store.go` is listed under both A and D on purpose and is
sequenced: **A** changes the single `ON CONFLICT (account_id, channel_id)`
upsert (R1) and stops; **D** may then add owner-scoped read methods in it.

The **lead orchestrator** owns this file, `.agents/handoffs/v0.0.5-*`, the
release docs, integration, and `cmd/telesync` startup wiring.

Agent B depends on A's table shape but only through the Go interface in §9,
which is fixed here — so B may proceed in parallel.

---

## 9. Fixed Go seams (so agents do not invent incompatible interfaces)

```go
// internal/channelstore — Agent A
type Account struct {
    ID, ServerKey, NetworkType        string
    OwnerUserID                       int64
    RemoteUserID                      int64
    RemoteUsername, RemoteDisplayName string
    PhoneNumberMasked                 string
    Status                            string
    Session                           secrets.Box   // sealed; zero = none
    SessionEncKeyVersion              int
    CreatedAt, AuthorizedAt           time.Time
    LastConnectedAt, LastDisconnectedAt time.Time
    LastErrorCode                     string
}

type AccountStore interface {
    Create(ctx context.Context, a Account) (Account, error)
    ByID(ctx context.Context, ownerUserID int64, id string) (Account, error) // owner-scoped
    ListByOwner(ctx context.Context, ownerUserID int64) ([]Account, error)
    UpdateSession(ctx context.Context, ownerUserID int64, id string, box secrets.Box, keyVersion int) error
    SetStatus(ctx context.Context, ownerUserID int64, id, status, errorCode string) error
    TouchConnected(ctx context.Context, id string, at time.Time) error
    Delete(ctx context.Context, ownerUserID int64, id string) error
    // ResolveLegacy maps one legacy client key to an adopted account, or "".
    ResolveLegacy(ctx context.Context, legacyKey string) (string, error)
}
```

```go
// internal/accountauth — Agent B
type LoginAttempt struct {
    ID, ServerKey, Method, State, NextStep string
    OwnerUserID                            int64
    AccountID                              string
    ExpiresAt, ResendAfter                 time.Time
    LastErrorCode                          string
}

type LoginService interface {
    Start(ctx context.Context, owner int64, req StartRequest) (LoginAttempt, error)
    Status(ctx context.Context, owner int64, attemptID string) (LoginAttempt, error)
    SubmitCode(ctx context.Context, owner int64, attemptID, code string) (LoginAttempt, error)
    SubmitPassword(ctx context.Context, owner int64, attemptID, password string) (LoginAttempt, error)
    Cancel(ctx context.Context, owner int64, attemptID string) error
}

// AccountResolver is the ONE seam that turns "which Telegram identity" into an
// account. Every legacy {key} route goes through it. Never falls back.
type AccountResolver interface {
    // Resolve returns the single eligible account for owner on serverKey, or
    // ErrAccountSelectionRequired / ErrNoEligibleAccount.
    Resolve(ctx context.Context, owner int64, serverKey string) (string, error)
    // Owns reports whether owner owns accountID.
    Owns(ctx context.Context, owner int64, accountID string) (bool, error)
}
```

```go
// internal/gateway — Agent D (extension of the existing Registry)
type Registry interface {
    Worker(key string) (Worker, bool)   // key is now account_id
}
```

Any agent needing a seam not listed here **stops and reports** rather than
inventing one.

---

## 9a. Lead rulings on subagent-reported gaps

Binding. Recorded 2026-11-12 after Agent B's report and a source check.

### R1 — Quarantine accounts; `account_id` is NOT NULL (replaces §2.2's nullable column)

**Problem found in source.** `store.go:531` upserts `managed_channels` with
`ON CONFLICT (client_key, channel_id)`. Changing the unique key to
`(account_id, channel_id)` breaks that statement. And a **nullable**
`account_id` would not fix it: SQLite treats NULLs as distinct in a UNIQUE
constraint, so `UNIQUE (account_id, channel_id)` would never dedupe a legacy
row and re-registration would insert duplicates.

**Ruling.** Migration 21 creates **one quarantined account row per legacy
`client_key`** and points that key's channels at it:

- `telegram_accounts.status` gains the value `quarantined`.
- `telegram_accounts.owner_user_id` becomes **nullable**, with
  `CHECK (owner_user_id IS NOT NULL OR status = 'quarantined')`. This is the
  only case a null owner is legal, and it is precisely "ownership is unclear".
- `managed_channels.account_id` is **NOT NULL** in the rebuilt table — every
  row names an account.
- `UNIQUE (account_id, channel_id)` then dedupes correctly and
  `store.go:531` becomes `ON CONFLICT (account_id, channel_id) DO UPDATE`.
  Agent A updates that one statement; it is the only `managed_channels`
  upsert. (`automation_state.go:337`'s `ON CONFLICT (client_key, channel_id)`
  is on `automation_checkpoints`, which keeps its own constraint — **leave it**.)

Effects, all of which the brief requires:
- §15 holds: two users on the same remote channel get two rows, two account ids.
- §33 Phase D holds: legacy sessions are quarantined, not shared, ownership not
  invented.
- Account-scoped reads filter `owner_user_id = ? AND status != 'quarantined'`.
- Adoption = operator sets `owner_user_id` and moves `status` off `quarantined`,
  recorded in `legacy_session_adoptions`. Exactly the brief's "explicit and
  auditable".

### R2 — `login_attempts` gains the fields the brief actually requires

Add to migration 21: `network_type`, `delivery`, `delivery_hint`,
`code_length`, `qr_token`, `qr_expires_at`, `code_submissions`.

Reasons: §7 mandates the delivery method be reported **accurately** from
`auth.sentCode`, which is impossible if `delivery` is not stored; §8's QR flow
needs a token and expiry; §4's rate limit needs a persisted counter.

Handling rules:
- `code_submissions` is `json:"-"` — never serialized to a client.
- `qr_token` is a short-lived login token → **secret-class**: never logged,
  never in error text, purged when the attempt ends.
- `delivery_hint` is **third-party text** from the server (an email pattern, an
  SMS prefix). Treat as untrusted: never render as HTML, sanitize before it
  reaches the panel, and never let it influence control flow.

### R3 — adopt the seams Agent B reported

`LoginAttemptStore` (6 methods: `Create`, `ByID`, `UpdateState`, `TouchResend`,
`DeleteExpired`, `DeleteByOwner`) plus `Incomplete(ctx, at) ([]LoginAttempt,
error)` — without a listing method the §4 rule "a restart leaves no orphaned
permanent session" is unenforceable. Field names reconcile to Agent B's record:
`PhoneMasked`, `ErrorCode` are canonical.

`AccountStore` gains `Ensure(ctx, AccountIdentity) (string, error)` and
`DropIncomplete(ctx, AccountIdentity) error` — the login state machine records
the account when the remote identity is confirmed, and must not import
`internal/channelstore`. Agent B reported these rather than hiding them, which
is correct; they are adopted.

### R4 — claim discipline on QR

QR is real at the wire level (gotd `auth.exportLoginToken` /
`auth.importLoginToken`) and capability-gated, but **not** exercised against a
mock QR session and never against a live server. Reports must say exactly that.
A custom backend implementing `exportLoginToken` differently is an open unknown.

### R5 — one account-id generator (integration ruling, 2026-11-12)

Both A and B independently implemented `acc_` + 26 Crockford base32 chars, and
B's doc comment claimed it was "the one place the id is generated". A third
drawing of the same machinery sits in `accountauth/service.go` for login-attempt
ids. Two implementations of a security-relevant format drift silently: one side
can mint an id the other does not recognise.

**Ruling.** `internal/accountid` is the single implementation; `accountauth.NewAccountID`
and `channelstore.newAccountID` both delegate to it. The `lga_` attempt id stays
local to `accountauth` — it is one concept in one package and crosses no
boundary. Tests pin prefix, length, Crockford charset, the excluded confusable
letters, non-repetition and non-sequentiality.

### R6 — `TouchConnected` is an internal lifecycle write

`channelstore.TouchConnected(ctx, id, at)` is `WHERE id = ?`, not owner-scoped,
which reads against §6.2. Verified at integration time that it has **no
production callers yet** — it is a seam for Agent C. The worker that calls it is
bound to one account by construction and has no separate principal, so adding an
owner parameter would be theatre unless the caller has one.

**Ruling:** the signature stays; the method is documented as a verified-account
lifecycle write reachable only from a worker already bound to that account, and
**never** on an HTTP request path. Agent C's worker-isolation tests are what
guarantee a worker cannot drive it for another account. If a request-scoped
caller ever appears, it must become owner-scoped.

### R7 — quarantine accounts carry `network_type = ''`

The database cannot tell which network a legacy session used, and AGENTS.md
forbids hardcoding client names. `''` already means "not stated" in
`sessionstore/accountbox.go`. The operator sets it at adoption. Do not guess.

### R8 — `Upsert` lands channels on quarantine accounts (transition state)

`Store.Upsert` resolves its account through `ensureLegacyAccount`, which lazily
records `legacy_<client_key>`. So every channel joined through today's API is
registered against a **quarantined** account until an operator adopts it or
Agent D lands the account-scoped registration path. This is correct during
transition and must not be mistaken for finished ownership. Agent D's
account-scoped registration is what moves channels onto real accounts.

### R9 — Allocate the permanent account id when login starts

**Owner decision, 2026-11-14.** The owner approved allocating the permanent
opaque `account_id` at login-attempt creation, before the first MTProto call.
This replaces the former prohibition on any pre-authorization account identity.

The id is not attempt-scoped and is never replaced after authorization:

- Create an owner- and server-bound `telegram_accounts` row in `connecting`
  state with `remote_user_id = 0` and no authorized session.
- Store that exact ID on the persisted login attempt and bind the session
  `AccountBox` AAD/path to it from the first MTProto exchange.
- `connecting` rows are not routable and never start workers.
- Only a server-confirmed authorization may fill remote identity and transition
  the same row to `connected`; only then may the supervisor start its worker.
- Cancel, expiry, terminal failure, and startup recovery retire only an
  incomplete `connecting` row and remove its attempt-owned session material.
  Cleanup must be conditional so a racing completed account is never removed.
- If the confirmed remote identity conflicts with an already-owned account,
  reject the duplicate without overwriting or adopting the existing account's
  session/history.

This is the permanent account identity allocated early, not a temporary
attempt-id/server-key session and not a shared-session fallback. Owner and
server binding are persisted before transport creation.

## 10. Verification contract

Claim discipline (AGENTS.md): tests/build ≠ handshake ≠ authorization ≠ live.
A sandbox skip is not a pass.

Final checks — smallest relevant set during development, all of these at the end:

```
gofmt -l .
go vet ./...
go test ./... -count=1
go build -o ./bin/telesync ./cmd/telesync
go run ./cmd/docsgen --check
go test ./internal/apidocs
```

Frontend: `npm run typecheck`, existing tests, production build in `web/`.

Live Telegram/Cat is **not** verifiable in this checkout (no credentials; none
may be invented). Simulated/mocked integration is reported as simulated, never
as live. Release acceptance is blocked on cross-user isolation and restart
recovery, which are testable without live credentials.
