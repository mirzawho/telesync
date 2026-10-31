<!-- component: Telegram routes -->
<!-- manual: Telegram routes -->
# Telegram routes

`GET /api/v1/telegram` is the one Telegram route without a key: it lists the
servers a client may choose between.

`/api/v1/telegram/{key}/...` answers for **one configured client**; `{key}` is
its `key` in the configuration file, matched case-sensitively. An unknown or
disabled key is `404 unknown_client` — never another client. A configured client
whose worker is down answers `503`; a client whose account is not authorized
reports `needs_auth` in `/health`, and the routes that need Telegram answer
`409 needs_auth`.

**Reading never marks messages as read.** No route calls a read-history method.

## The server listing

`GET /api/v1/telegram` (any authenticated caller; it needs no global permission,
because it names clients and their state, never an account's data). `data.servers`
holds one row per **enabled** client, in configuration order; a disabled client
is absent, exactly as it is absent from routing (`404 unknown_client`).

| Field | Meaning |
| --- | --- |
| `key` | the configuration key every other route takes as `{key}` |
| `type` | `official` or `custom` |
| `enabled` | always `true`: the row exists because the client is enabled |
| `health_checkable` | a worker answered the health check for this request |
| `state` | the worker's own `rpc.WorkerState` (`connected`, `starting`, `needs_auth`, `disconnected`, `error`, `stopping`), or `unknown` when no worker answered, or `unavailable` when a worker did not answer |
| `authorized` | `true`/`false` from the worker; `null` when not known — never guessed |
| `transport` | the transport report (`ready`, `connecting`, …), empty when unknown |
| `last_success_at` | last confirmed Telegram operation, `null` when none |
| `last_error` | why the state is what it is; empty when the worker reported none |

**Enabled is not online.** A client is listed because it is configured and
enabled; `state` is a live health check made for this request, and a client
whose worker is down is still listed, with `state: "unavailable"` (the worker
could not be reached, or the request timed out) and an `authorized` of `null`.
The listing never claims a server is online when it has not just been checked.

The listing never carries credentials, API hashes, phone numbers, session
paths, custom endpoints or any other configuration field: the type behind it
(`gateway.ServerInfo`) holds only the key, the type, and whether a worker is
registered.

`last_error` for a timeout is one fixed sentence; the underlying error goes to
the daemon log, never to the response.

## The dashboard's server status

`GET /api/v1/telegram` feeds the channel pages' server selector; it needs a
token and no global permission, because it names clients and their state, never
an account's data.

`GET /api/v1/telegram` feeds the dashboard's server status card, beside the
channel pages' server selector. It needs the
global `servers.view` permission (`403 forbidden` without it, and
`GET /api/v1/auth/me` reports it as `access.servers`); `servers.view` grants
nothing else. Each row is the table above plus one field:

| Field | Meaning |
| --- | --- |
| `user` | the server's own Telegram account — `id`, `first_name`, `last_name`, `username` — or `null` when it is not known (no worker, a worker that did not answer, or an account not authorized). The caller's account is never reported here. |

The card reads one of three connection states from `state`, `transport` and
`authorized`, in this order: a worker that has not answered yet or reports
`starting` with transport `connecting` is **در حال اتصال**; `connected` with
transport `ready` is **متصل**; anything else (`error`, `disconnected`,
`unavailable`) is **خطا در اتصال**. A connected server whose `authorized` is
`false` is **نیازمند ورود**. A profile lookup that fails never fails the
listing: the connection facts are still reported and `user` is `null`.

## What comes from where

- `is_member` and `membership_status` come from Telegram and are re-read on
  every request that returns them.
- `is_managed` comes from the local database: it says the operator recorded the
  channel with `POST .../join`. A channel can be a member and not managed, and a
  `join_pending` record exists without membership.
- Nothing is written locally as a side effect of reading.

## `/channels` and `/find`

`/channels` lists broadcast channels and supergroups the account is in, most
recent activity first, sliced from a dialog snapshot (see the pagination
paragraph). `unread_count` is absent when Telegram reported none; it is never
defaulted to 0.

`find?q=` accepts a `@username`, a public `t.me/` link, an invite link, a
numeric channel id and free text; a bare word is text. It returns an array,
empty for no match. A numeric id this client has never resolved answers
`400 peer_unresolved`: MTProto needs the access hash, which only a prior resolve
provides. An unjoined invite reveals no id: such a result carries `"id": ""` and
every permission `false`. Text search uses `contacts.search` and then
`messages.searchGlobal`, the second under a 5 s deadline and best-effort.

## Channel ids and the channel object

`GET /api/v1/telegram/{key}/channels/{channel_id}` answers the one channel
object the API has: `id` (Telegram's id, a string), `type`, `title`, `username`,
`description`, `avatar_available`, `avatar_url`, `is_member`, `is_managed`,
`membership_status`, `permissions`, `verified`, `scam`, `fake` and
`participants_count`, every value read from Telegram on this request. The local
channel detail (`GET /api/v1/local/channels/{id}`) answers the same object from
the stored snapshot; `docs/api/local/channels.md` explains the difference.

## Channel ids

`{channel_id}` in a path is Telegram's id — bare (`1671633563`) or Bot-API
(`-1001671633563`) — or an invite written `+AbCdEf123456`. The channel object
returns the id as a **string** (it exceeds JavaScript's safe integers); the
listing and messages use numbers.

## Messages and media

History is newest first, paged by message id with the caveats in the pagination
paragraph. The list body has no channel object: the channel is in the path.
`media` on a message is a descriptor; `media.url` is the path that streams the
bytes — relative to the API root, so a client joins it to the root it talks to
(`/api/v1/telegram/{key}/...`; behind the panel's mount,
`/admin/api/v1` + the same remainder) — capped at 32 MiB per download. The URL is present only for media that
has bytes (photo, video, document, audio, voice, sticker, gif). Media responses are bytes, not JSON,
with `Cache-Control: private, max-age=86400`.

### Views and reactions

Every message carries `views` and `reactions`; both come from the message
Telegram returned for that request, never from a stored copy.

```json
{
  "id": 9,
  "date": "2026-10-18T12:54:40Z",
  "text": "Message text",
  "views": 1520,
  "reactions": [
    {"type": "emoji", "emoji": "👍", "count": 12, "chosen": false},
    {"type": "emoji", "emoji": "❤️", "count": 8, "chosen": true},
    {"type": "custom_emoji", "document_id": 5368324170671202286, "count": 2, "chosen": null}
  ]
}
```

| Field | Meaning |
| --- | --- |
| `views` | Telegram's own view counter (`message.views`). **`null`** when Telegram did not report one — a post the backend returned without the field — and the real number otherwise, including `0`. It is Telegram's metric, not a count of distinct people. |
| `reactions` | always an array; `[]` when the post has none. |
| `reactions[].type` | `emoji` (a standard reaction), `custom_emoji` (a Telegram custom emoji), or `other` (a reaction kind this API does not model, such as a paid star reaction). |
| `reactions[].emoji` | the standard emoji's character; absent for `custom_emoji` and `other`. |
| `reactions[].document_id` | a custom emoji's document id; absent otherwise. It is what keeps a custom emoji from being read as a standard one. The image is `GET /api/v1/telegram/{key}/custom_emoji/{document_id}`. |
| `reactions[].count` | how many reactions of this kind the post has. |
| `reactions[].chosen` | whether the client's own account picked this reaction (`reactionCount.chosen_order`). `false` when it did not; `null` when the response type does not say. |

Both routes that return messages — the history page and the single message —
and the local channel messages route answer with the same shape. A backend
that does not expose the fields (an older custom backend) answers `null` and
`[]`; nothing is estimated locally.

## Custom emoji images

`GET .../custom_emoji/{document_id}` streams one reaction custom emoji's bytes
(`Content-Type` from the document, usually `application/x-tgsticker`) with
`Cache-Control: private, max-age=86400`: the bytes of a document id never
change. The id is the `reactions[].document_id` a message reported. The route
needs no channel permission — it is not tied to a channel — but it does need
the worker's account to be authorized (`409 needs_auth`). An id Telegram does
not serve — a recycled document, or one this account cannot read — is
`404 not_found`; the bytes come from Telegram's reaction sticker set, which the
worker reads at most once an hour and shares with its other downloads of these
images.

## Join and avatars

`POST .../join` is the only state-changing Telegram route (`channels.create`).
It joins only when the account is not a member, then records the channel. Its
`result` says what happened on each side: `joined_and_added_to_management`,
`already_member_added_to_management`, `already_managed`, `join_pending`. A
rejected join writes nothing. If Telegram confirms and the local write then
fails, the answer is `500 worker_error` saying so, and retrying is safe.

Avatars are cached server-side for `runtime.avatar_ttl` (24 h) per client and
channel, `ETag` from Telegram's photo id. A refresh failure inside the stale
window is served with `Warning: 110` and `max-age=0`; past it, an error.

## Limits that are Telegram's

Live behaviour — join outcomes, public search, avatars — is covered by tests
against a scripted MTProto server, not against Telegram itself. A custom
backend's support for media or search is a property of that backend: the route
reports Telegram's answer.
