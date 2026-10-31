<!-- archived: the page this documentation replaced -->
# Telegram API

Routes under `/api/v1/telegram/{key}/...` answer for **one configured Telegram
client**. `{key}` is the client's `key` from the configuration file, matched
case-sensitively. The gateway holds a registry built at startup:

- An unknown or disabled key is `404 unknown_client` — never another client.
- A configured client whose worker is down answers `503 worker_unavailable` —
  never a substitute.
- A client whose account is not authorized answers `409 needs_auth`.

Every Telegram read reaches the worker over its private Unix socket, and the
worker talks to Telegram with gotd. Reads are read-only towards Telegram: no
route marks messages as read.

| Document | Routes |
| --- | --- |
| [health.md(health.md) | `GET /health` |
| [profile.md(profile.md) | `GET /profile` |
| [channels.md(channels.md) | `GET /channels`, `GET /channels/find`, `GET /channels/{channel_id}`, `POST /channels/{channel_id}/join`, `GET /channels/{channel_id}/avatar` |
| [messages.md(messages.md) | `GET /channels/{channel_id}/messages`, `GET /channels/{channel_id}/messages/{message_id}`, `GET /channels/{channel_id}/messages/{message_id}/media` |

## Permissions

The channel routes require permissions; see
[the channel rule](../authentication.md#the-channel-rule). `health` needs only a
valid credential; `profile` needs `channels.view` or any per-channel assignment.

## Official and custom clients

A client is either `official` (Telegram production infrastructure, gotd's own
defaults) or `custom` (an operator-controlled MTProto backend with explicit
endpoint and keys). The public API does **not** expose which endpoint a client
uses: `/profile` reports `type` and `this_dc` but never the backend host or
port.

Two differences are verified in the code and visible here:

- `custom` clients resolve channels through a resolver that never contacts
  Telegram, its CDN or its bundled keys; if the backend redirects to another
  datacenter under the default `single-dc` routing, the request fails rather than
  following it.
- Everything else in the contract — fields, states, codes — is identical for
  both types.

Whether a given custom backend implements media, search or joins is a property
of that backend, not of this API; the route will report Telegram's answer.
