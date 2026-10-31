<!-- component: channels -->
<!-- manual: local channels -->
# Local channels

A **local channel** is a channel this application has been asked to manage, and
it is the same record the Telegram `join` route writes: one row per
`(client key, Telegram channel id)`, kept in SQLite. These routes address that
row, read and remove it, and — for messages and the avatar — ask Telegram
through the worker of the client the row names. The record itself is all this
API needs to know which account to ask.

## Two ids, never one

| Field | Meaning | Used for |
| --- | --- | --- |
| the path's `{id}` and the listing row's `id` | the local row id — the `managed_channels` primary key | every route path on this page; a per-channel permission assignment |
| the detail's `channel.id`, the listing row's `channel_id` | Telegram's channel id | the Telegram routes (`/api/v1/telegram/{key}/channels/{channel_id}/...`) |

They are different namespaces. `GET /api/v1/local/channels/15` is the record
whose **local** id is 15, whatever Telegram calls the channel; the Telegram
routes never accept a local id, and these routes never accept a Telegram id (a
Telegram id that happens to match no local row is `404 not_found`). The record
is resolved first and its `client_key` and `channel_id` are what reaches the
worker, so a client cannot ask for one channel and be shown another.

## One channel object, two sources

`GET /api/v1/local/channels/{id}` answers the **same channel object** as
`GET /api/v1/telegram/{key}/channels/{channel_id}`: the same keys, the same
types, the same nested `permissions`. What differs is where the values come
from.

| Route | Source | Avatar URL |
| --- | --- | --- |
| `/api/v1/telegram/{key}/channels/{channel_id}` | Telegram, read live on every request | `/api/v1/telegram/…/channels/…/avatar` |
| `/api/v1/local/channels/{id}` | the SQLite record, as the last join wrote it | `/api/v1/local/channels/{id}/avatar` |

The local route reads SQLite only: no worker is contacted, no channel is
resolved through MTProto, and nothing is synchronized as a side effect. It is
the stored snapshot, so it answers while every Telegram client is down, and it
can be **stale**: `membership_status`, the participant count, the notices and
the permissions are what Telegram last said, not what it says now. A caller that
needs the current state asks the Telegram route.

```json
{
  "id": "1234567890",
  "type": "channel",
  "title": "کانال نمونه",
  "username": "sample",
  "description": "About the channel",
  "avatar_available": true,
  "avatar_url": "/api/v1/local/channels/15/avatar",
  "is_member": true,
  "is_managed": true,
  "membership_status": "member",
  "permissions": {"new": false, "edit": false, "delete": false, "read": true},
  "verified": false,
  "scam": false,
  "fake": false,
  "participants_count": 1234
}
```

- `id` is Telegram's channel id **as a string**, the same value and type the
  Telegram routes report. It is never the local row id: that stays in the path
  and in every other route's `id` on this page.
- `type` is `channel` or `group` (the stored `peer_type`).
- `avatar_available` is whether Telegram reported a photo; `avatar_url` is
  present only when it did, and points at the local avatar route.
- `is_member` is true only for a record whose `membership_status` is `member`
  — the state Telegram confirmed when the join was recorded. `join_pending` and
  `unknown` are `is_member: false`.
- `is_managed` is always `true` here: the record exists because the channel is
  managed.
- `permissions` are the four rights Telegram normalized when the channel was
  recorded, `false` when they were never reported. `read` is a subscriber's
  ability to read the history.
- `verified`, `scam`, `fake` are Telegram's channel notices as recorded.
- `participants_count` is `null` when Telegram never reported one.
- `username` and `description` are omitted when empty, exactly as the Telegram
  route omits them.

**Unknown is not the same as "no".** A record written before these fields were
stored — or by a join that never confirmed a membership — does not know the
notices or the permissions. It answers them `false` rather than guessing, and
the snapshot is refreshed the next time that channel is joined (a repeated
join rewrites every stored field).

The local storage details — the owning client key, the timestamps, the raw
Telegram id as a number — are not part of this object. They belong to the
record, and the listing below is where a record is shown as a record.

## The listing's row

`GET /api/v1/local/channels` lists **records**, not channel objects: each row
names the local `id`, the `client_key` that answers for it and the Telegram
`channel_id` as a number, with `type`, `title`, `username`, `description`,
`avatar_available`, `avatar_url`, `membership_status`, `participants_count`,
`created_at`, `updated_at` and `last_synced_at`. It is the record view a
management screen needs; the detail route is the channel view.

## `GET /api/v1/local/channels`

One page of stored records, across every configured client, ordered by
`channel_id` descending and then `id` descending. It reads SQLite only: no
worker is contacted, so it answers while every Telegram client is down.

- **Query:** `page` (≥ 1, default 1), `per_page` (1–100, default 20), `q`
  (≤ 500 characters, matched literally and case-insensitively against the title,
  username and client key). An unknown parameter is `400 invalid_input`.
- **Meta:** `page`, `per_page`, `total`, `total_pages`, `has_more`. `total`
  counts what the caller may see, not what exists.
- **Access:** with `channels.view` every record is listed except one carrying an
  explicit `view: deny`. Without it the page is drawn only from records the
  caller holds an explicit `view: allow` on, so a caller with none gets an empty
  `200`, never someone else's channel.

## `GET /api/v1/local/channels/{id}`

One record, as the channel object above. `404 not_found` when no row has that
local id — before permission is considered, so a caller cannot learn "forbidden"
about a row that is not there. A record the caller may not see is `403
forbidden`, with no reason.

## `GET /api/v1/local/channels/{id}/messages`

Telegram's history of that channel, read through the worker of the record's
client with the record's `channel_id`. The `page`/`per_page` rules and the
response are the Telegram messages route's: `data.messages` with each message's
`text`, `date`, `sender`, `reply_to_message_id`, `views`, `reactions` and
`media` (whose `url` points
at the Telegram media route), and `meta` with `page`, `per_page`, `has_more`,
`next_page`. Telegram does not report how many messages a channel has, so there
is no `total`.

**Nothing is persisted.** There is no messages table: the page is fetched,
rendered and discarded, and reading it never marks anything as read
(`messages.getHistory` has no read side effect).

Failures are the Telegram routes' own, and are never answered with an empty
page: no connection is `503 not_connected`, an unauthorized account
`409 needs_auth`, a channel Telegram no longer returns `404 channel_not_found`,
a timeout `504 worker_timeout`, anything else `502 worker_error`. The upstream
message is logged, never returned. If the record's client is no longer
configured, the answer is `404 unknown_client` — never another client's worker.

## `GET /api/v1/local/channels/{id}/avatar`

The profile photo bytes, through the same avatar path the Telegram route uses:
the persistent cache keyed by `(client key, Telegram channel id)`, the same
`ETag` (Telegram's photo id) and `Cache-Control`, so both routes serve one
cached copy. No cache configured streams the bytes directly.

`404 not_found` when the channel has no photo (or the record does not exist),
`502 worker_error` when Telegram fails, `503`/`409` as above. Avatar bytes are
never stored in the database.

## `POST /api/v1/local/channels/{id}/leave`

Two different operations behind one route, decided by the body:

| Body | Telegram | Local record |
| --- | --- | --- |
| omitted, `{}`, or `{"telegram": false}` | untouched — the account stays a member | deleted |
| `{"telegram": true}` | the account leaves (`channels.leaveChannel`) | deleted, only after Telegram confirmed |

- **Body rules:** the body is optional (no `Content-Type`, or no bytes). When
  present it must be `application/json` (`415` otherwise), one JSON object,
  with `telegram` a boolean (`{"telegram": "yes"}` and `{"telegram": null}` are
  `400 invalid_input`). Unknown fields — including `token` — and a trailing
  second value are `400`.
- **Credential:** only the `Authorization` header. A credential is never read
  from a request body.
- **Access:** the record must exist (`404`) and the caller needs a `delete`
  assignment on it (`403`). An explicit `deny` overrides everything. The
  operator token is not an account and has no per-channel assignment, so it is
  refused here with `403 forbidden`; removal is done by an account holding the
  right.
- **Order:** with `telegram` true, Telegram is asked first. A failed leave is
  its own error (`404 channel_not_found`, `503 not_connected`, `409 needs_auth`,
  `504`, `502`) and **the record is kept**, so the request can simply be
  retried. The row is deleted only after Telegram confirmed.
- **Partial failure:** a delete that fails after a confirmed leave answers
  `500 partial_failure`: the message says Telegram's part succeeded and the
  record could not be deleted. Retry with `{"telegram": false}` to remove only
  the record; Telegram is not asked again.
- **Repeated and concurrent calls:** a second request for the same id is `404`
  (the record is gone) or, with `telegram` true, a leave that Telegram answers
  "not a participant" to, which is treated as success. One record's leave is
  serialized within the daemon, so two concurrent requests cannot both reach
  Telegram or race the delete.
- **What is deleted:** the one row. Its rows in `user_channels_permission` go
  with it (`ON DELETE CASCADE`); `channel_categories` assignments are keyed by
  client key and Telegram id and are **not** touched, so filing a channel under
  a category survives the record being removed. No other channel, category,
  user or account is affected.

```json
{"data": {"local_deleted": true, "telegram_left": false}, "meta": {"request_id": "…", "generated_at": "…"}}
```

The envelope is the API's usual one (`data` + `meta`); `telegram_left` is the
semantic distinction asked for by `{"telegram": false}` versus
`{"telegram": true}`.

## What these routes do not do

There is no create, update or join here: a channel becomes a local record through
`POST /api/v1/telegram/{key}/channels/{channel_id}/join`, and joining is not
duplicated. Messages are read only — no send, edit or delete — and nothing about
a channel is mirrored locally beyond the record's metadata.
