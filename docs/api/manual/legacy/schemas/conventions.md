<!-- archived: the page this documentation replaced -->
# Conventions

## Envelopes

| Kind | Shape | Routes |
| --- | --- | --- |
| Success (JSON) | `{"data": …, "meta": {…}}` | every route except `/healthz`, login, and byte responses |
| Failure | `{"error": {"code", "message", "request_id"}}` | every route |
| Bare | `/healthz` → `{"status","time"}`; login → `{"access_token","token_type","expires_in"}` | those two |
| Bytes | image / attachment content | avatar, media |

`meta` always contains `request_id` and `generated_at`. Paged responses add
`page`, `per_page`, `has_more` and (where it exists) `total`, `total_pages`,
`next_page`. `find` adds `count` only.

`date`, `started_at`, `last_success_at`, `generated_at`, `created_at` and
`updated_at` are RFC 3339 in UTC (`2026-10-25T10:00:00Z`). They come from the
daemon or the worker's clock; Telegram's message dates are converted from Unix
seconds to the same format.

## Request IDs

- Every response carries `X-Request-Id`.
- A caller-supplied `X-Request-Id` is echoed **only** when it is 1–64 characters
  of `[A-Za-z0-9._-]`; anything else is replaced by a generated 16-hex-character
  id.
- The same value appears as `meta.request_id` (or `error.request_id`), so a log
  line can be matched to the response.
- Worker and store failures are logged with it; error messages never carry
  internals.

## Headers

| Header | Direction | Meaning |
| --- | --- | --- |
| `Authorization: Bearer <token>` | request | required on every route except login (and `/healthz` on loopback) |
| `Content-Type: application/json` | request | required on every write body; anything else is `415` |
| `Accept: application/json` | request | optional; the API always answers JSON except for byte routes |
| `X-Request-Id` | request/response | see above |
| `Retry-After` | response (429) | seconds to wait, from Telegram's flood wait |
| `Allow` | response (405) | methods the path serves |
| `WWW-Authenticate: Bearer realm="telesync"` | response (401) | present on authentication failures |
| `ETag` | response (avatar) | Telegram photo id; changes when the avatar changes |
| `Warning: 110 - "Response is stale"` | response (avatar) | stale cached image served |
| `Cache-Control` | response (avatar, media) | `private, max-age=…` |
| `X-Content-Type-Options: nosniff` | response | always |

CORS headers are not sent.

## Identifiers

| Identifier | Type | Where | Meaning |
| --- | --- | --- | --- |
| client key | string | `{key}` in `/api/v1/telegram/{key}/...` | the `key` of a client in the configuration file; case-sensitive; never a Telegram id |
| Telegram channel id | integer or decimal string | path `{channel_id}`; `channel.id`; message media/upload paths | Telegram's own id. Bare (`1671633563`) or Bot-API (`-1001671633563`); both are accepted in a path and normalised to the positive id. **A string** in the channel object (too large for JS numbers), a **number** in the channel summary and messages' media URL |
| invite hash | string | path `+<hash>`, `find?q=…` | `t.me/+<hash>`; 8–64 of `[A-Za-z0-9_-]`; reveals no channel id until joined |
| local managed-channel id | integer | `channel_id` in a **permission** body | the row id of the `managed_channels` table, created by `POST .../join`. Different from Telegram's id |
| category id | integer | `/api/v1/local/categories/{id}` | local row id |
| user id | integer | `/api/v1/local/users/{id}`, JWT `sub` | local row id |
| message id | integer | `/{message_id}` | per-channel message number |
| media id | string | `media.id` | opaque, `<channel_id>:<message_id>` |

The two `id` shapes for channels are deliberate and documented at each use; a
parser that assumes one type will break on the other.

## Sizes and limits

| Limit | Value | Where |
| --- | --- | --- |
| `per_page` max | 100 | every paged route |
| `q` length | 256 (Telegram find), 500 (categories), 200 (users) | see [Pagination](../pagination.md) |
| JSON body | 16 KiB (categories), 64 KiB (users, login) | local writes and login |
| Category name / description | 100 / 500 characters after trimming | category writes |
| User fullname / username / password | 200 / 64 / 8–1024 characters | user writes |
| One attachment download | 32 MiB | worker (`D27`) |
| Request timeout for a worker call | `api.request_timeout`, default 30 s | every Telegram route |
| Dialog snapshot reuse | 60 s per worker | `/channels` |
| Avatar cache | `runtime.avatar_ttl`, default 24 h; stale window 6 h | avatar route |
| Access token life | `api.jwt.token_ttl`, default 1 h, max 24 h | login |
