# Managed channels, membership and the avatar cache

This document covers the local state that sits beside the Telegram API: the
SQLite database of selected channels, how membership differs from selection, and
the on-disk avatar cache. The HTTP surface is in [api/README.md](api/README.md).

## Two different questions

| question | source of truth | where it appears |
| --- | --- | --- |
| Is this account a member of the channel? | Telegram | `is_member`, `membership_status` |
| Has the operator selected this channel for management here? | the local database | `is_managed` |

They are independent, and the API never conflates them:

- `is_member: true, is_managed: false` is the normal state of a channel the
  account belongs to but nobody has selected.
- `membership_status: "join_pending", is_managed: true` is a channel whose join
  request was sent and recorded, without a confirmed membership.

`GET /channels` and `GET /channels/find` **read Telegram only**. They never
insert, and they never filter by the local selection: the list is what the
account can see, which is the only honest answer for a Telegram client.

The local database exists for one reason: a `join` call is an explicit
instruction that this channel should be managed from now on, and that
instruction has to survive a restart.

## Database

- File: `<runtime.data_dir>/channels.db` (default `data/db/channels.db` beside
  the configuration file). Directory `0700`, file `0600`.
- Engine: SQLite through `database/sql` (`modernc.org/sqlite`, pure Go — no cgo,
  no server, no ORM).
- One connection, WAL journal, 5 s busy timeout.
- Owned by the **daemon** process. Workers never open it, so there is exactly one
  writer and no competing copies per client.

### Schema and migrations

`schema_version` records every applied migration; each one runs in a transaction
together with its version row, so a failure leaves the database untouched.
Migrations are forward-only and append-only.

Version 1, `managed_channels`:

| column | type | notes |
| --- | --- | --- |
| `id` | INTEGER PK | local row id |
| `client_key` | TEXT | configured client key |
| `channel_id` | INTEGER | Telegram's positive channel id |
| `peer_type` | TEXT | `channel` or `group` (CHECK) |
| `title` | TEXT | last known |
| `username` | TEXT | last known, `""` when none |
| `description` | TEXT | last known |
| `avatar_photo_id` | INTEGER | Telegram photo version, so a changed avatar invalidates the cached image; `0` = not reported |
| `membership_status` | TEXT | `member`, `join_pending` or `unknown` (CHECK) |
| `participants_count` | INTEGER NULL | `NULL` when Telegram did not report one |
| `created_at`, `updated_at`, `last_synced_at` | TEXT | RFC 3339, first selection / last write / last write from a live read |

Version 2, `categories` and `channel_categories`:

| table | column | type | notes |
| --- | --- | --- | --- |
| `categories` | `id` | INTEGER PK | auto-increment |
| | `name` | TEXT | required; `COLLATE NOCASE` + `UNIQUE`, `CHECK (length(trim(name)) > 0)` — names are unique ignoring ASCII case |
| | `description` | TEXT | `''` when none |
| | `created_at`, `updated_at` | TEXT | RFC 3339 |
| `channel_categories` | `client_key` | TEXT | configured client key |
| | `channel_id` | INTEGER | Telegram's positive channel id |
| | `category_id` | INTEGER | `REFERENCES categories(id) ON DELETE CASCADE` |
| | `created_at` | TEXT | RFC 3339 |
| | — | PK | `(client_key, channel_id, category_id)`, so an assignment cannot be duplicated |

`channel_categories` deliberately has **no** foreign key to `managed_channels`.
A category may hold a channel this application has not joined (the assignment
endpoint is not part of the category CRUD surface yet, but the table and its
repository methods exist for it), and the absence of the reference is what
guarantees that `DELETE`ing a category can never remove a channel row: the
cascade reaches only assignment rows. Deleting a category removes its category
row and its assignment rows in one transaction.

Foreign-key enforcement matters for that cascade. `PRAGMA foreign_keys` is set
through the DSN (`_pragma=foreign_keys(1)`), which `modernc.org/sqlite` applies
to every new pooled connection, and the pool is capped at one connection
(`SetMaxOpenConns(1)`); `TestForeignKeyEnforcementIsOnEveryConnection` pins both.

`UNIQUE (client_key, channel_id)`. The same numeric channel under `official` and
under `the-cat` is two rows: they are different Telegram identities reached
through different accounts.

The identity model is deliberately a client key plus a channel id. Category
assignments and any other association can be added as new tables keyed on that
pair without touching it.

### What is written, and when

Only `POST .../join` writes.

1. Resolve the channel through the worker.
2. Join when the account is not already in it.
3. Take what Telegram confirmed (joined, already a member, pending).
4. Upsert the row and report the result.

Never written: channels seen in `/channels`, channels found by `find`, channels
whose join failed, or a `member` status for a pending request.

### Partial failure

Telegram and SQLite are two systems and are not made to look atomic. If Telegram
confirms a join and the write fails, the API answers `500` saying membership is
confirmed but the record was not saved, and that a retry is safe. The retry hits
the already-a-member path and completes the write. Nothing is silently
reported as fully successful.

## Permissions

Computed once, from Telegram's channel object (`tgapi.NormalizePermissions`), so
`find`, `join` and `/channels` cannot disagree. The exact semantics and their
limits are in [api/schemas/telegram.md](api/schemas/telegram.md#permissions).

## Avatar cache

- Directory: `runtime.cache_dir`, default `data/cache/avatars`. Never served as
  files; the gateway reads bytes and sets headers itself.
- Layout: `<cache_dir>/<client_key>/<channel_id>/avatar.img` plus `avatar.json`
  (`content_type`, `photo_id`, `stored_at`, `expires_at`, `bytes`).
- **TTL 24 h** (`runtime.avatar_ttl`). Within it, a request is served without
  contacting Telegram.
- Writes are atomic: a temporary file in the same directory, then a rename. The
  sidecar is written last, so a crash between the two leaves no valid entry.
- Concurrency: one download per cache key; concurrent requests wait for it.
- A failed download is never stored.
- Cleanup runs at daemon start and deletes metadata past `TTL + 6 h`. It walks at
  most 10 000 entries per pass and only removes files it wrote.
- Path safety: the client key must match the configuration's key grammar, and
  the channel id must be positive. No caller-supplied string is ever used as a
  path segment.

## Channel access hashes (peer store)

MTProto cannot address a channel by numeric id; it needs the access hash learned
from an earlier resolve. Each worker keeps its own small cache at
`<runtime.peer_dir>/<key>.json` (`0600`, written atomically, bounded to 2 000
entries, corrupted file treated as empty because it is derived data).

This is why `find?q=<numeric id>` works for channels the client has already
seen (dialog, search hit, username resolve) and answers `peer_unresolved` for
ones it has not.

## Configuration

```json
{
  "runtime": {
    "data_dir": "./data/db",
    "cache_dir": "./data/cache/avatars",
    "peer_dir": "./data/peers",
    "avatar_ttl": "24h"
  }
}
```

All four are optional. `data_dir` and `cache_dir` may not sit inside
`run_dir` (sockets) or overlap `session_dir` (credentials); the loader refuses
that configuration instead of silently honouring it.

## Known limitations

- `/api/v1/telegram/{key}/channels` does not report `is_managed`; discovery
  (`find`), a single channel and `join` do.
- A managed channel is listed, read and removed through
  `/api/v1/local/channels` (removal is `POST .../{id}/leave`), and filed into
  categories (`/api/v1/local/channels/{id}/categories`). Filing from the
  category side (`GET /api/v1/local/categories/{id}/channels`) is read-only.
- No synchronization pass. A channel whose Telegram membership later changes
  keeps its cached status; Telegram remains authoritative through `is_member`.
- `find` returns at most 20 results per search method.
- An invite link to an unjoined channel cannot be recorded by id: Telegram only
  reveals the id after joining, so `join` reports that the channel could not be
  identified rather than storing a row without an id.
