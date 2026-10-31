<!-- archived: the page this documentation replaced -->
# Channels

- [List channels](#list-channels) — `GET .../channels`
- [Find channels](#find-channels) — `GET .../channels/find?q=`
- [Get one channel](#get-one-channel) — `GET .../channels/{channel_id}`
- [Join a channel](#join-a-channel) — `POST .../channels/{channel_id}/join`
- [Channel avatar](#channel-avatar) — `GET .../channels/{channel_id}/avatar`

All paths are under `/api/v1/telegram/{key}`. The [channel object(../schemas/telegram.md#channel-object)
returned by `find`, `/channels/{id}` and `join` is the same everywhere.

> `{channel_id}` in a path is a **Telegram** channel id (bare `1671633563` or the
> Bot-API form `-1001671633563`) or an invite hash written `+AbCdEf123456`.
> `channel_id` inside a **permission** body is a **local** row id — see
> [Conventions(../schemas/conventions.md#identifiers).

---

## List channels

### `GET /api/v1/telegram/{key}/channels?page=1&per_page=20`

The channels and supergroups the signed-in account is in, as Telegram reports
them.

- **Authentication:** `channels.view` or any per-channel assignment. Each listed
  channel is then filtered by [the channel rule(../authentication.md#the-channel-rule).
- **Query:** `page`, `per_page` — see [Pagination](../pagination.md).
- **Local data:** none. This route never reads or writes the category/channel
  database, and reading a dialog does not add it to the managed set.

### Success — `200`

```json
{
  "data": {
    "channels": [
      {
        "id": 1234567890,
        "title": "Example channel",
        "username": "example",
        "kind": "channel",
        "has_avatar": true,
        "unread_count": 4,
        "last_message_id": 1234,
        "permissions": {"new": false, "edit": false, "delete": false, "read": true}
      }
    ]
  },
  "meta": {
    "request_id": "…", "generated_at": "…",
    "page": 1, "per_page": 20, "total": 12, "has_more": false
  }
}
```

See [Channel summary fields(../schemas/telegram.md#channel-summary-fields).

- `id` is a **number** here (a dialog's id fits), unlike the channel object where
  it is a decimal **string**.
- Only broadcast channels and supergroups; private groups and users are not
  included.
- `unread_count` is **absent** when Telegram did not report one; it is never
  defaulted to 0.
- `has_avatar` says whether a photo exists; the list does not download it.
- `permissions` are computed exactly as in the channel object.
- `total` is Telegram's dialog count unless channels were filtered out, in which
  case it is the number on the page (see [Pagination(../pagination.md#telegram-channels-a-snapshot-not-a-cursor)).
- Pages are sliced from a dialog snapshot reused for up to 60 s.

### Errors

| Status | `code` | When |
| --- | --- | --- |
| 400 | `invalid_input` | bad `page`/`per_page`, or any other query parameter |
| 401, 403, 404, 409, 502, 503, 504 | see [Errors](../errors.md) | credential, permission, key, authorization, worker |

### Example

```bash
curl --fail-with-body --silent --show-error \
  "${BASE_URL}/api/v1/telegram/official/channels?page=1&per_page=5" \
  --header "Accept: application/json" \
  --header "Authorization: Bearer ${TELESYNC_TOKEN}"
```

---

## Find channels

### `GET /api/v1/telegram/{key}/channels/find?q={query}`

Discovers channels through the configured client. Returns an **array**, even for
one match, and an empty array when a valid query matches nothing. Search never
joins and never records anything locally.

- **Authentication:** same as [List channels](#list-channels); results that are
  not permitted are removed from the array.
- **Query parameters:** `q` only. Any other parameter — including `page` or
  `per_page` — is `400`.

### `q` forms

`q` is trimmed; it must be 1–256 characters and contain no line break or tab.

| Form | Example | Resolution |
| --- | --- | --- |
| username with `@` | `@telegram` | `contacts.resolveUsername`; must match `[A-Za-z][A-Za-z0-9_]{2,31}` |
| public link | `https://t.me/telegram`, `t.me/s/telegram`, `telegram.me/…` | as a username |
| invite link | `https://t.me/+AbCdEf123456`, `t.me/joinchat/AbCdEf123456` | `messages.checkChatInvite`; hash must be 8–64 of `[A-Za-z0-9_-]` |
| numeric id | `1671633563`, `-1001671633563` | this worker's peer cache (access hash) |
| free text | `technology news` | `contacts.search`, then `messages.searchGlobal` |

A bare word without `@` is ambiguous with text and is treated as **text**.

### Success — `200`

```json
{
  "data": {
    "channels": [
      {
        "id": "1671633563",
        "type": "channel",
        "title": "Example channel",
        "username": "example",
        "description": "About text",
        "avatar_available": true,
        "avatar_url": "/api/v1/telegram/official/channels/1671633563/avatar",
        "is_member": true,
        "is_managed": false,
        "membership_status": "member",
        "permissions": {"new": false, "edit": false, "delete": false, "read": true},
        "verified": false,
        "scam": false,
        "fake": false,
        "participants_count": 1234
      }
    ]
  },
  "meta": {"request_id": "…", "generated_at": "…", "count": 1}
}
```

`meta` also carries `request_id` and `generated_at`; `count` is the number of
channels **returned**.

### Behaviour and limits

- **`id` is a decimal string** (channel ids exceed JavaScript's safe integer
  range).
- **A numeric id needs peer information.** MTProto addresses a channel by (id,
  access hash), and the hash cannot be derived from the id. An id this client has
  never resolved answers `400 peer_unresolved`; resolve it once by `@username`
  or a link, after which the worker remembers the hash in `runtime.peer_dir`.
- **An unjoined invite reveals no id.** Such a result has `"id": ""`,
  no `avatar_url`, and every permission `false`.
- **Text search uses two methods.** `contacts.search` covers chats the account
  knows; `messages.searchGlobal` covers the public directory under a 5 s
  deadline and is best-effort: if Telegram accepts it and never answers, the
  account's own results are still returned. The request fails only when neither
  method produced anything.
- **`description`, `participants_count` and the permission set** need a
  `channels.getFullChannel` read, which fails for a channel the account has not
  joined; those fields are then absent, `null`, or empty rather than guessed.
- `username`, `description` and `avatar_url` are omitted when unavailable.
- Each search method returns at most 20 results; this route does not page.

### Errors

| Status | `code` | When |
| --- | --- | --- |
| 400 | `invalid_input` | missing/empty `q`, `q` longer than 256, a line break in `q`, a malformed `@username`/link/invite, or any parameter other than `q` |
| 400 | `peer_unresolved` | numeric id with no cached access hash |
| 400 | `invite_invalid` | malformed, revoked or expired invite (invite resolution path) |

Plus the shared credential/key/worker errors in [Errors(../errors.md).

### Examples

```bash
# By @username
curl --fail-with-body --silent --show-error \
  --get "${BASE_URL}/api/v1/telegram/official/channels/find" \
  --data-urlencode "q=@telegram" \
  --header "Accept: application/json" \
  --header "Authorization: Bearer ${TELESYNC_TOKEN}"

# Free text (spaces must be URL-encoded; --data-urlencode does it)
curl --fail-with-body --silent --show-error \
  --get "${BASE_URL}/api/v1/telegram/official/channels/find" \
  --data-urlencode "q=technology news" \
  --header "Accept: application/json" \
  --header "Authorization: Bearer ${TELESYNC_TOKEN}"
```

---

## Get one channel

### `GET /api/v1/telegram/{key}/channels/{channel_id}`

One channel's current Telegram state, read fresh.

- **Authentication:** [the channel rule(../authentication.md#the-channel-rule)
  for that channel. An invite reference (no id yet) requires `channels.view`
  instead, because there is no local row to check.
- **Path parameter:** `channel_id` — decimal id (bare or `-100…`) or
  `+<invite hash>`.
- **Local data:** the category database is **read** to fill `is_managed`, never
  written.

### Success — `200`

```json
{
  "data": {
    "channel": {
      "id": "1671633563",
      "type": "channel",
      "title": "Example channel",
      "username": "example",
      "description": "About text",
      "avatar_available": true,
      "avatar_url": "/api/v1/telegram/official/channels/1671633563/avatar",
      "is_member": true,
      "is_managed": false,
      "membership_status": "member",
      "permissions": {"new": false, "edit": false, "delete": false, "read": true},
      "verified": false,
      "scam": false,
      "fake": false,
      "participants_count": 1234
    }
  },
  "meta": {"request_id": "…", "generated_at": "…"}
}
```

See [Channel object(../schemas/telegram.md#channel-object).

### Errors

| Status | `code` | When |
| --- | --- | --- |
| 400 | `invalid_input` | not a decimal id or `+invite` |
| 400 | `peer_unresolved` | numeric id this client has never resolved |
| 400 | `invite_invalid` | unusable invite |
| 403 | `forbidden` | not permitted by the channel rule |
| 404 | `channel_not_found` | the account cannot see the channel |

### Examples

```bash
curl --fail-with-body --silent --show-error \
  "${BASE_URL}/api/v1/telegram/official/channels/1671633563" \
  --header "Accept: application/json" \
  --header "Authorization: Bearer ${TELESYNC_TOKEN}"

# By invite (the + must stay in the path; quote the URL)
curl --fail-with-body --silent --show-error \
  "${BASE_URL}/api/v1/telegram/official/channels/+AbCdEf123456" \
  --header "Accept: application/json" \
  --header "Authorization: Bearer ${TELESYNC_TOKEN}"
```

---

## Join a channel

### `POST /api/v1/telegram/{key}/channels/{channel_id}/join`

Joins the channel through the configured client **when needed**, then records it
in the local managed-channel database. This is the only Telegram route that
changes state, and the only one that is `POST`.

- **Authentication:** `channels.create`; otherwise `403 forbidden`.
- **Method:** only `POST`. `GET` on this path is `405` with `Allow: POST`.
- **Path parameter:** `channel_id` — decimal id (bare or `-100…`) or
  `+<invite hash>`.
- **Request body:** none is read.
- **Required configuration:** the local channel store. Without it
  (`gateway.Config.Channels == nil`) the answer is `503 worker_unavailable`
  ("Local channel storage is not configured.").
- **Local effect:** upserts one `managed_channels` row keyed by
  `(client_key, telegram channel id)`, with the membership status, title,
  username, description, avatar photo id and participant count that Telegram
  reported.

### Order of operations

1. Parse the reference.
2. Read whether the channel is already managed (decides the reported `result`).
3. Resolve and join via the worker (skipped when already a member; an invite
   imports the hash).
4. Parse the channel id from the result.
5. Upsert the local record.
6. Answer.

### Success — `200`

```json
{
  "data": {
    "channel": {
      "id": "1671633563",
      "type": "channel",
      "title": "Example channel",
      "username": "example",
      "avatar_available": true,
      "avatar_url": "/api/v1/telegram/official/channels/1671633563/avatar",
      "is_member": true,
      "is_managed": true,
      "membership_status": "member",
      "permissions": {"new": false, "edit": false, "delete": false, "read": true}
    },
    "result": "joined_and_added_to_management"
  },
  "meta": {"request_id": "…", "generated_at": "…"}
}
```

`result` is one of:

| `result` | Meaning |
| --- | --- |
| `joined_and_added_to_management` | the account was not a member; the join succeeded and the record was created |
| `already_member_added_to_management` | the account was already a member; nothing was sent to Telegram, the record was created |
| `already_managed` | a local record already existed; its metadata was refreshed |
| `join_pending` | Telegram accepted a join **request**; membership is **not** confirmed and the stored status is `join_pending`, never `member` |

The returned `channel.is_managed` is `true` in every success case, because the
record was just written.

### Idempotency and partial failure

- Repeated calls are idempotent in both systems: no join is attempted for a
  channel the account is in, and the local row is unique on
  `(client_key, telegram channel id)`.
- **A rejected join writes nothing**: resolution failure, invite errors,
  membership limit (`409 membership_limit`) and rate limits (`429 rate_limited`)
  leave the database untouched.
- **Telegram and SQLite are not one transaction.** If Telegram confirms and the
  local write fails, the answer is `500 worker_error` stating that membership is
  confirmed but the record was not saved, and that retrying is safe. The retry
  takes the already-a-member path and completes the write.
- An invite that Telegram declined to identify cannot be recorded: `502
  worker_error` ("…did not identify the channel…").

### Errors

| Status | `code` | When |
| --- | --- | --- |
| 400 | `invalid_input` | malformed reference |
| 400 | `invite_invalid` | malformed, revoked or expired invite |
| 400 | `peer_unresolved` | numeric id with no cached access hash |
| 403 | `forbidden` | no `channels.create` |
| 405 | `method_not_allowed` | not `POST` |
| 409 | `membership_limit` | the account cannot join more channels |
| 409 | `needs_auth` | the Telegram account is not authorized |
| 429 | `rate_limited` | Telegram flood wait; see `Retry-After` |
| 500 | `worker_error` | membership confirmed but not stored — retry |
| 502 | `worker_error` | the channel could not be identified |

### Side effects elsewhere

The new row is what makes per-channel permissions possible for that channel
(their `channel_id` is this local row id), and `is_managed` starts appearing on
the channel object. Nothing is written to Telegram beyond the join itself.

### Examples

```bash
curl --fail-with-body --silent --show-error \
  --request POST \
  "${BASE_URL}/api/v1/telegram/official/channels/1671633563/join" \
  --header "Accept: application/json" \
  --header "Authorization: Bearer ${TELESYNC_TOKEN}"

curl --fail-with-body --silent --show-error \
  --request POST \
  "${BASE_URL}/api/v1/telegram/official/channels/+AbCdEf123456/join" \
  --header "Accept: application/json" \
  --header "Authorization: Bearer ${TELESYNC_TOKEN}"
```

---

## Channel avatar

### `GET /api/v1/telegram/{key}/channels/{channel_id}/avatar`

The channel's profile photo as **image bytes**, not JSON. `avatar_url` in
`find`, `join` and the channel object points here.

- **Authentication:** [the channel rule(../authentication.md#the-channel-rule)
  for that channel.
- **Path parameter:** a **numeric** channel id only. `+invite` is
  `400 invalid_input` ("avatar needs a numeric channel_id; resolve the invite
  first.") because the cache is keyed by id.
- **Response headers:**
  - `Content-Type` — from Telegram's metadata; sniffed from the bytes when the
    backend reports none.
  - `Content-Length`
  - `ETag` — Telegram's photo id, quoted (`"123456"`). It changes when the
    avatar changes.
  - `Cache-Control: private, max-age=<seconds remaining>`.
  - `Warning: 110 - "Response is stale"` and `Cache-Control: private, max-age=0`
    when a stale cached image is served because the refresh failed.
  - `X-Request-Id`, `X-Content-Type-Options: nosniff`.
- **Caching:** server-side for `runtime.avatar_ttl` (24 h by default) under
  `runtime.cache_dir/<client_key>/<channel_id>/`, so one client's image can never
  be served for another. One download per key in flight; concurrent requests
  share it. A failed download is never cached. An entry that expired is
  refreshed; if the refresh fails and the entry is within 6 h of expiry it is
  served stale as above, past that it is an error.
- A browser `If-None-Match` is **not** evaluated by the gateway: it returns `200`
  with the bytes and the headers. Use the `ETag` yourself if you need to skip
  re-downloads.

### Errors

| Status | `code` | When |
| --- | --- | --- |
| 400 | `invalid_input` | invite reference, or not a channel id |
| 403 | `forbidden` | not permitted by the channel rule |
| 404 | `not_found` | the channel has no photo |
| 404 | `channel_not_found` | the account cannot see the channel |

### Examples

Save the image and inspect the headers:

```bash
curl --fail-with-body --silent --show-error \
  --dump-header /dev/stderr \
  --output /tmp/channel-avatar.jpg \
  "${BASE_URL}/api/v1/telegram/official/channels/1671633563/avatar" \
  --header "Authorization: Bearer ${TELESYNC_TOKEN}"
```

Related: [Messages(messages.md), [Channel object schema](../schemas/telegram.md#channel-object).
