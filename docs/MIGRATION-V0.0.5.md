# Migrating to v0.0.5 — personal Telegram/Cat accounts

v0.0.5 replaces centrally shared Telegram/Cat sessions with **user-owned
accounts**: every Telesync user connects and authenticates their own Telegram or
Cat account, and every channel, message, automation and background job runs
under the account that owns it.

This is a data migration, not a feature flag. Read this before upgrading a
deployment that has anything in it.

---

## 1. What changes

| | v0.0.4 | v0.0.5 |
| --- | --- | --- |
| Telegram identity | one shared session per connection key (`official`, `cat`) | one session **per user-owned account** |
| Who may use it | an administrator assigns access | the user who authenticated it |
| `managed_channels` uniqueness | `(client_key, channel_id)` | `(account_id, channel_id)` |
| Workers | one per connection key | one per account |
| Update state, peer caches, jobs | keyed by connection key | keyed by account |
| Session storage | plaintext JSON per client | AES-256-GCM sealed per account |

**Server definitions are unchanged.** `official` and `cat` still describe the
MTProto network, endpoint and credentials. What is new is that they are
*settings*, not identities — a user may hold several accounts on one server.

**Unchanged:** users and roles, categories, WordPress sites and their
permissions, channel Telegram capabilities (`read`/`new`/`edit`/`delete`),
Channel Explorer, messaging, media, albums, webhooks, workflows, AI polishing
and the durable queue.

**Retired:** assigning users access to a shared Telegram connection. That
concept is gone from the UI *and* from the authorization code. It is not merely
hidden.

---

## 2. Before you start

### Back up

The migrations are forward-only. Nothing below deletes your data, but a backup
is the only way back.

```bash
# stop the daemon first
systemctl stop telesync        # or however it runs

# the database
cp -p data/db/channels.db "backup/channels-$(date +%Y%m%d).db"
# WAL/SHM travel with it if the daemon is stopped
cp -p data/db/channels.db-wal "backup/" 2>/dev/null || true

# existing Telegram sessions — these are the credentials being migrated
cp -a data/sessions "backup/sessions-$(date +%Y%m%d)"
```

Keep the backup somewhere the daemon cannot write.

### Set the session encryption key

Every authorized MTProto session becomes a sealed box under one key. **Generate
it outside the application and keep it safe:**

```bash
openssl rand -base64 32
export TELESYNC_SESSION_ENCRYPTION_KEY='...paste it...'
```

Or in the configuration file, under `accounts` (see §5).

> **There is no recovery from a lost key.** A new key cannot open sessions
> sealed with the old one; regenerating it means every connected account has to
> log in again. Store it with the same care as the database backup.

The key is deliberately **not** the WordPress key. Rotating one must not disturb
the other.

---

## 3. What the migration does

On first start, migration 21 applies and:

1. **Creates `telegram_accounts`.** Every account has an explicit owner
   (`owner_user_id`), a server definition (`server_key`) and an opaque stable id
   (`account_…`).

2. **Quarantines the legacy sessions.** For each existing connection key it
   creates one account row named `legacy_<key>` with `status = 'quarantined'`
   and **no owner**, and points that key's channels at it.

   A quarantined account:
   - appears in **nobody's** account list;
   - runs **no** worker;
   - is **never** selected by a legacy route;
   - can be reached only through the legacy key, and only by an operator
     resolving it.

   This is deliberate. The old sessions belonged to the deployment, not to a
   person, and inventing an owner would hand someone else's Telegram identity to
   the wrong user. See §4.

3. **Rebuilds `managed_channels`** so that uniqueness is per account rather than
   per connection key. Two users may now independently register the same remote
   channel and each keeps their own row, categories, permissions, workflows and
   history. **Every existing `id` is preserved**, so permission assignments and
   automation records stay linked.

4. **Rebuilds `automation_events`** so event identity includes the account.
   This is the fix for a real defect: v0.0.3 deduplicated events *across*
   connections, so if two users watched the same channel the second user's
   automation was silently starved. Existing rows are preserved.

5. **Adds `account_id` (and, where needed, `owner_user_id`) to** webhooks,
   webhook sources, workflows, automation events/jobs/state, publish
   operations, send operations, edit operations and channel creation
   operations. Existing rows keep a null account and are treated as
   legacy — see §6.

6. **Adds `login_attempts`** and **`legacy_session_adoptions`**.

Nothing is deleted. WordPress sites, categories, users and their permissions are
untouched.

---

## 4. Adopting a legacy session

A quarantined session becomes usable when an operator says who owns it. That is
**explicit and auditable** — there is no automatic assignment, and an
application administrator does **not** inherit Telegram accounts by virtue of
being an administrator.

Before adopting, be certain. Adoption moves a live Telegram identity under one
person's control. Ask:

- Whose phone number authenticated this session?
- Who has been using the channels it is registered against?
- Is that person still with the organization?

If you cannot answer, **leave it quarantined.** A quarantined session is inert;
a wrongly adopted one is someone else's Telegram account in the wrong hands.

### The procedure

1. List what is quarantined:

   ```sql
   SELECT id, server_key, status, created_at
   FROM telegram_accounts
   WHERE status = 'quarantined';
   ```

2. See what would move with it:

   ```sql
   SELECT id, channel_id, title FROM managed_channels WHERE account_id = ?;
   SELECT id, name FROM webhooks WHERE account_id = ?;
   SELECT id FROM workflows WHERE account_id = ?;
   ```

3. Have the intended owner log in through the panel and connect their own
   account (`افزودن حساب`). That creates a **real, owner-bound** account with
   their own session.

4. Record the adoption. It names the legacy key, the target account, who
   decided and why:

   ```sql
   INSERT INTO legacy_session_adoptions
     (legacy_key, account_id, adopted_by, adopted_at, reason)
   VALUES ('official', 'acc_…', 1, '2026-11-12T10:00:00Z', 'Ticket OPS-421');
   ```

5. Re-point the dependent rows **only** after the adoption is recorded, and only
   the ones that belong:

   ```sql
   UPDATE managed_channels SET account_id = 'acc_…' WHERE account_id = 'legacy_official';
   ```

6. Bring the account out of quarantine:

   ```sql
   UPDATE telegram_accounts
   SET owner_user_id = 1, status = 'disconnected', network_type = 'telegram'
   WHERE id = 'acc_…';
   ```

7. The owner reconnects from the panel.

### What not to do

- Do **not** give every administrator the legacy session.
- Do **not** set `owner_user_id` on a quarantined row without recording the
  adoption — the audit row is the evidence of who decided.
- Do **not** delete the encrypted legacy session bytes. Keep them until the
  adoption is confirmed correct.
- Do **not** move a workflow to a user who does not own its source channel.

---

## 5. Configuration

New settings. None of them hold a user's phone number, login code or session —
those live in the database, sealed.

```json
{
  "accounts": {
    "session_encryption_key_env": "TELESYNC_SESSION_ENCRYPTION_KEY"
  }
}
```

`session_encryption_key_path` and `session_encryption_key` are the file and
literal forms; set **exactly one**. If `TELESYNC_SESSION_ENCRYPTION_KEY` is set
in the environment it wins and the file must not also give one.

| variable | default | meaning |
| --- | --- | --- |
| `TELESYNC_SESSION_ENCRYPTION_KEY` | — | AES-256 key sealing every account's session |
| `TELESYNC_LOGIN_ATTEMPT_TTL` | `10m` | how long a login attempt stays open |
| `TELESYNC_LOGIN_RATE_LIMIT` | `5` | login code submissions allowed per attempt |
| `TELESYNC_ACCOUNT_MAX_WORKERS` | `50` | bound on concurrent account workers |
| `TELESYNC_ACCOUNT_RECONNECT_BACKOFF` | `5s` | reconnect delay for a disconnected account |

**Missing key behaviour.** Starting without a key is not an error on its own —
an installation that connects no personal account has no session to seal, and
refusing to boot would break every existing install on upgrade. The refusal
happens at the moment a session would actually be written. If you plan to have
users connect accounts, set the key before they do.

---

## 6. Legacy rows after the migration

Rows written before v0.0.5 keep a null `account_id`. They are **not** silently
run under whichever account happens to be connected:

| row kind | behaviour |
| --- | --- |
| automation job | paused, claimable by nobody, visible in the queue |
| webhook delivery | cannot be retried until its ownership is resolved |
| publish/send/edit operation | not executable until bound to an account |
| workflow | disabled until its source channel has an owning account |
| managed channel | reachable through its legacy key only |

A job with no owning account is work nothing may execute. That is the honest
state: it is held and visible, not lost and not run as somebody else.

---

## 7. After upgrading

Run these checks before declaring the migration good:

```bash
gofmt -l . && go vet ./... && go test ./... -count=1
go build -o ./bin/telesync ./cmd/telesync
go run ./cmd/docsgen --check
```

Then, in the running deployment:

1. **Nothing was lost.** User count, category count, WordPress site count and
   channel count match the backup.
2. **Existing users still log in** to Telesync itself.
3. **Quarantined accounts are visible to nobody.** Sign in as each user and
   confirm `حساب‌های من` is empty or shows only their own accounts.
4. **A legacy key no longer serves a session.** Any `/api/v1/telegram/{key}/…`
   call must resolve only to the *caller's own* account on that server — never to
   `legacy_<key>`, and never to another user's.
5. **Cross-user isolation.** With two users each holding an account: neither can
   list, read, send through, or see the history of the other's accounts,
   channels, media, webhooks or workflows. This holds for an administrator too.
6. **Restart recovery.** Restart the daemon. Connected accounts reconnect
   **without a new login code**, and each recovers its own update state.

Anything not verified is recorded as *not verified* — never as passed.

---

## 8. Rolling back

Migrations are forward-only; there is no "down" script. To go back:

1. Stop the daemon.
2. Restore the database from the backup in §2.
3. Restore `data/sessions` from the backup if you adopted or removed anything.
4. Run the previous binary.

If you must roll back **after** users have connected personal accounts, those
sessions are sealed under the v0.0.5 key and the old binary cannot read them.
Those users will have to log in again. Keep the encryption key anyway — the
sealed blobs are still valid for a forward return to v0.0.5.

---

## 9. Frequently asked

**Will my existing channels disappear?**
No. Every `managed_channels` row survives with its id intact, so permissions and
category assignments stay linked. They simply belong to a quarantined account
until an operator resolves ownership.

**Can I keep running on the shared session?**
No. That is the model v0.0.5 retires. The legacy execution path is not the
supported one, and the quarantine exists precisely so shared sessions are inert
rather than quietly shared.

**Two of my users want the same channel.**
That is now supported and is the point of the change: each registers it under
their own account and gets their own row, permissions, workflows and history.

**What happens when someone leaves?**
Disable or delete their Telesync user. Their workers stop and their queued work
is paused. Their Telegram *channels* are not deleted — removing someone from
Telesync is not deleting their Telegram account.

**I lost the encryption key.**
Every sealed session is unreadable. Remove the affected accounts and have their
owners reconnect. There is no way to recover the key.