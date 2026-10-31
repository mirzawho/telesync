<!-- archived: the page this documentation replaced -->
# Telegram objects

Canonical definitions for the objects the Telegram routes return. Endpoint pages
link here instead of repeating them.

## Health object

Returned by [`GET /api/v1/telegram/{key}/health`(../telegram/health.md).

| Field | Type | Present | Notes |
| --- | --- | --- | --- |
| `key` | string | always | client key |
| `type` | string | always | `official` \| `custom` |
| `state` | string | always | `starting` \| `connected` \| `disconnected` \| `needs_auth` \| `error` \| `stopping` |
| `authorized` | boolean | always | a successful authorized probe; not implied by a connection |
| `transport` | string | always | `ready` \| `connecting` \| `disconnected` \| `error` |
| `rpc_ready` | boolean | always | true in any body the worker returns |
| `pid` | number | always | worker process id |
| `started_at` | string \| null | always | RFC 3339; `null` if the worker reported no start time |
| `uptime_seconds` | number | always | whole seconds |
| `this_dc` | number | omitted when 0 | datacenter id |
| `last_success_at` | string | omitted until a probe succeeded | RFC 3339 |
| `last_error` | string | omitted when empty | sanitized diagnostic |

## Profile object

Returned by [`GET /api/v1/telegram/{key}/profile`](../telegram/profile.md).

| Field | Type | Present | Notes |
| --- | --- | --- | --- |
| `key` | string | always | client key |
| `type` | string | always | `official` \| `custom` |
| `authorized` | boolean | always | |
| `authorization_state` | string | always | `authorized` \| `needs_auth` \| `unknown` |
| `this_dc` | number | omitted when 0 | |
| `user` | [Account](#account) \| null | always | `null` unless `authorization_state` is `authorized` |

### Account

| Field | Type | Notes |
| --- | --- | --- |
| `id` | number | Telegram user id |
| `first_name` | string | `""` when absent |
| `last_name` | string | `""` when absent |
| `username` | string | `""` when absent |

## Channel summary fields

The items of `data.channels` on [`GET .../channels`](../telegram/channels.md#list-channels).

| Field | Type | Present | Notes |
| --- | --- | --- | --- |
| `id` | **number** | always | Telegram channel id, numeric here |
| `title` | string | always | |
| `username` | string | omitted when none | public username without `@` |
| `kind` | string | always | `channel` (broadcast) or `group` (supergroup/megagroup) |
| `has_avatar` | boolean | always | a photo exists; not downloaded by the list |
| `unread_count` | number | **omitted** when Telegram reported none | never defaulted to 0 |
| `last_message_id` | number | omitted when 0 | newest message id in the dialog |
| `permissions` | [Permissions](#permissions) | always | |

## Channel object

Returned as `data.channel` by `find`, `/channels/{id}` and `join`, and as items
of `data.channels` on `find`.

| Field | Type | Present | Notes |
| --- | --- | --- | --- |
| `id` | **string** | always | decimal channel id; `""` for an unjoined invite (find only) |
| `type` | string | always | `channel` or `group`; defaults to `channel` when Telegram did not say |
| `title` | string | always | |
| `username` | string | omitted when none | |
| `description` | string | omitted when empty | needs a full-channel read; absent for a channel not joined |
| `avatar_available` | boolean | always | |
| `avatar_url` | string | omitted unless `avatar_available` **and** `id` is known | API path, e.g. `/api/v1/telegram/official/channels/1671633563/avatar` |
| `is_member` | boolean | always | **Telegram's** answer |
| `is_managed` | boolean | always | **local** database: recorded by `POST .../join` |
| `membership_status` | string | always | `member` \| `not_member` \| `join_pending` \| `unknown` |
| `permissions` | [Permissions](#permissions) | always | |
| `verified` | boolean | always | Telegram's verified badge |
| `scam` | boolean | always | Telegram's scam flag |
| `fake` | boolean | always | Telegram's fake flag |
| `participants_count` | number \| null | always | `null` when Telegram did not report one |

`is_member`/`membership_status` and `is_managed` are independent facts; see
[the README](../authentication.md#permissions).

## Permissions

```json
{"new": false, "edit": false, "delete": false, "read": true}
```

All four are booleans, never strings, numbers or `null`, and are computed in one
place from Telegram's own channel data.

| Field | `true` when |
| --- | --- |
| `read` | the account is a member, not restricted from viewing, and not marked as having left |
| `new` | the account may generally publish a post: creator; admin with `post_messages` (or an unrestricted supergroup admin); or an unrestricted supergroup member. Always `false` for a broadcast-channel subscriber |
| `edit` | creator, or admin holding the channel-level `edit_messages` right |
| `delete` | creator, or admin holding the channel-level `delete_messages` right |

`edit`/`delete` describe a **channel-level capability**, not permission for a
particular message: Telegram still refuses edits and deletions per message based
on ownership and age. A value that cannot be established is `false`. A
non-member always reads `false`, because MTProto answers `CHANNEL_PRIVATE` to an
account that has not joined, even for a public channel.

## Message object

Items of `data.messages` on the history list and the single-message route.

| Field | Type | Present | Notes |
| --- | --- | --- | --- |
| `id` | number | always | message id within the channel |
| `date` | string | always | RFC 3339 UTC |
| `text` | string | always | may be `""` (media-only post) |
| `sender` | [Sender](#sender) | omitted for anonymous channel posts | |
| `media` | [Media](#media-object) | omitted when there is no attachment | descriptor only; bytes are fetched separately |
| `reply_to_message_id` | number | omitted unless Telegram reported a reply header | the replied-to message is not fetched |

Messages are returned newest first. Reading never marks them read.

## Sender

| Field | Type | Notes |
| --- | --- | --- |
| `id` | number | Telegram user id |
| `name` | string | display name; may be `""` |
| `username` | string | omitted when none |

## Media object

| Field | Type | Present | Notes |
| --- | --- | --- | --- |
| `id` | string | omitted only when the worker gave none | opaque `<channel_id>:<message_id>` |
| `type` | string | always | `photo`, `video`, `document`, `audio`, `voice`, `sticker`, `gif`, `poll`, `geo`, `webpage`, `other` |
| `label` | string | omitted when empty | human-readable description |
| `mime_type` | string | omitted when the backend did not report one | |
| `size` | number | omitted when unknown | bytes |
| `url` | string | always | API path to the bytes, relative to the listener |

The URL is a **path**, not an absolute URL: it stays correct behind any host,
port or reverse proxy. Fetch it with the same credential as the listing.

## Byte responses

| Route | Content-Type | Other headers |
| --- | --- | --- |
| `.../channels/{id}/avatar` | from Telegram, else sniffed | `ETag`, `Cache-Control: private, max-age=…` (or `max-age=0` + `Warning` when stale), `Content-Length` |
| `.../messages/{mid}/media` | from Telegram, else sniffed | `Cache-Control: private, max-age=86400`, `Content-Length` |

Neither is JSON; failures are still JSON error bodies because they are decided
before the body is written.
