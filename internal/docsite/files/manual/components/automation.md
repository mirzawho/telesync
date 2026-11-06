<!-- title: Automation -->
<!-- manual: automation -->

# Automation

The automation routes manage the background system that reacts to new messages
in a managed source channel: **webhooks**, which send the message to an external
HTTP API, and **workflows**, which publish it to other Telegram channels or
registered WordPress sites. Both are configured once and then run without an
open panel, without an HTTP request, and without any human approval step.

Nothing in this section writes to Telegram. The routes read and write
configuration and history only. The publishing that a workflow performs happens
in the background and is recorded in the workflow's run history.

## Permissions

Three global permissions cover the area, and none of them is implied by
another:

| Permission | Grants |
| --- | --- |
| `automation.view` | see webhook and workflow configuration and their execution history |
| `webhooks.manage` | create, change and delete webhooks, retry deliveries, send tests |
| `workflows.manage` | create, change and delete workflows and their destinations |

Every route needs `automation.view`. The writes need their own managing
permission on top of it. Being able to open the administration page grants
nothing: the check is on the account, not on the page.

Beyond the global permission, a resource is only visible to an account that can
already see the channels behind it. A webhook subscribes to one or more source
channels and is visible only when the caller may see at least one of them; a
workflow belongs to exactly one source channel and is visible only with it. A
resource the caller may not see answers `404`, not `403`, so a route cannot be
used to probe for the existence of another account's configuration.

A write may name only what the caller can already see. Naming a source channel
or a publishing destination the account cannot reach is refused with
`automation_source_forbidden` or `automation_destination_forbidden`. Configuring
a workflow additionally requires the destination to be one the account could
post to by hand.

## Identifiers

Three different numbers appear in these routes and they are never
interchangeable:

| Name | What it is |
| --- | --- |
| `managed_channel_id` | the local `managed_channels.id`, the only value a permission check may use |
| `channel_id` | a Telegram peer id, the number Telegram itself uses |
| `client_key` | the configured Telegram connection the channel belongs to |

`source_channel_ids` on a webhook and `managed_channel_id` on a workflow are
local ids. `channel_id` on a workflow destination and on a publication result is
a Telegram peer id, always paired with a `client_key`. A WordPress destination
names a `site_id`, which is the local id of a registered site.

## One workflow per channel

A managed channel may have **no** workflow or **exactly one**. The database
enforces this, so a second `POST` for a channel that already has one answers
`409` with `conflict`, and the panel points the operator at the existing
workflow instead of creating a duplicate. A workflow's source channel is fixed
at creation: moving it would orphan the work already queued behind it, so
`PATCH` refuses to change `managed_channel_id`.

## Webhook payloads

A webhook receives a CloudEvents-shaped envelope. The event `id` is stable and
derived from the message identity, so a receiver can use it as an idempotency
key.

```json
{
  "specversion": "1.0",
  "id": "evt_…",
  "source": "telesync/telegram/channels/7",
  "type": "telegram.message.created",
  "time": "2026-11-05T12:30:00Z",
  "datacontenttype": "application/json",
  "data": {
    "source": {
      "managed_channel_id": 7,
      "client_key": "official",
      "telegram_channel_id": 123456789
    },
    "message": {
      "id": 36,
      "date": "2026-11-05T12:30:00Z",
      "text": "Example message",
      "media": {
        "type": "document",
        "filename": "example.apk",
        "mime_type": "application/vnd.android.package-archive",
        "size": 59797754,
        "url": "https://telesync.example/admin/api/v1/telegram/official/channels/123456789/messages/36/media"
      },
      "entities": [],
      "reactions": [],
      "views": 45
    }
  }
}
```

### Media is a link, never a file

A webhook never uploads attachments, never base64-encodes them, never sends
multipart and never embeds binary in the JSON. The payload carries the media's
type, filename, MIME type, size and a URL to the existing media endpoint.

The address comes in two fields. `path` is always present and is the media
endpoint's path from the root: the panel's mount, the API's own namespace, and
the endpoint the channel messages API already reports. With the panel mounted
at `/admin`, a message in channel `123456789` is linked as
`/admin/api/v1/telegram/official/channels/123456789/messages/36/media`.

`url` is that same address made absolute, and it appears **only** when
`api.public_base_url` names the origin: with it set to `https://telesync.example`
the link becomes
`https://telesync.example/admin/api/v1/telegram/official/channels/123456789/messages/36/media`.
The setting is optional — without it the API still creates and enables a
webhook, and the payload simply carries `path` so a receiver can prefix the
origin it already knows. An address is never invented: a payload either names a
real absolute URL or gives the path to build one, never something that looks
absolute and cannot be opened.

The media endpoint is **authenticated**. A webhook secret is not an API access
token and the two are deliberately separate: the receiver needs a Telesync API
credential of its own to download the attachment. This is the one gap between a
webhook receiver and an ordinary API client, and it is left open on purpose —
making private attachments anonymously readable to simplify a webhook would
trade a real security property for convenience.

## Webhook authentication

Four modes. The secret is written by the caller and never returned: a saved
secret is not loaded back into the browser, and no response or history record
carries it.

| `auth_mode` | What is sent |
| --- | --- |
| `hmac` | a signature, the default |
| `bearer` | `Authorization: Bearer <secret>` |
| `header` | the named header, e.g. `X-API-Key: <secret>` |
| `none` | nothing, when explicitly chosen |

`hmac` follows the Standard Webhooks convention:

```
webhook-id: <stable delivery id>
webhook-timestamp: <unix seconds>
webhook-signature: v1,<base64 HMAC-SHA256>
```

The signed input is `webhook-id.webhook-timestamp.<the exact bytes sent>`. A
secret written as `whsec_<base64>` is base64-decoded before use, so a receiver
built to the Standard Webhooks spec verifies it unchanged. A `whsec_` secret that
is not valid base64 is refused where it is written rather than at the first
delivery. Any other secret — a passphrase, a hex string the operator chose — is
used as its literal bytes. On a retry the logical delivery id is preserved, the
timestamp is regenerated and the signature is recalculated over the bytes
actually transmitted — the body is never re-serialized after signing.

A custom header name is validated. The transport headers a client must control
(`Host`, `Content-Length`, `Connection`, `Transfer-Encoding`, and the
`webhook-*` headers themselves) cannot be chosen.

## Delivery history

Every request is recorded, whether it succeeded or not: one delivery row per
(webhook, event), and one attempt row per HTTP exchange. A retry extends the
existing delivery; it never creates a second one.

Each attempt keeps the real method and URL, the request and response headers and
bodies, the status, the duration, and the network or TLS error when there was
one. Bodies are bounded by `webhooks.max_response_bytes` and a longer response
is stored as a truncated prefix with a truncation flag and the real HTTP status.
Headers and bodies are redacted before they are written: `Authorization`,
`X-API-Key`, any custom authentication header, `Cookie`, and sensitive query
values never reach the database.

## Delivery semantics

- Any `2xx` is a success.
- Timeouts, connection errors, `429` and transient `5xx` are retried.
- A permanent `4xx` fails without further attempts.
- A `Retry-After` is honoured, bounded.
- The attempt budget is finite. A job is never retried indefinitely.

Retrying an eligible failed delivery is a manual action on one delivery, and it
reuses the delivery's stored event rather than inventing a new one.

Outbound requests are SSRF-restricted: HTTPS by default, no localhost, no
loopback, private, link-local or cloud-metadata addresses, no unsafe IPv6, no
redirects, DNS resolved and the validated address dialled with TLS hostname
verification. The check runs at delivery time and not only when the URL is
saved, and the test route uses the identical transport with a fixed payload.

## Workflows and AI

A workflow takes the source message and publishes it to each of its
destinations. The original message is never modified; the original and the
polished text are kept apart in the run record.

AI polishing is optional and off by default. When it is on and the workflow has
no prompt of its own, a built-in editing prompt is used. The source message is
treated as untrusted content to edit and cannot override the model's rules, and
neither the source nor a custom prompt can change where anything is published.

If AI is enabled and fails, the message is **not published** — that is the
default. A workflow may explicitly opt into publishing the original content
anyway (`ai_publish_on_failure`), and the run is then recorded as
`published_with_fallback` rather than as a success.

Images are excluded from AI input by default. That decision is independent of
publishing: an image kept away from the model still reaches the destination.

## Run statuses

| Status | Meaning |
| --- | --- |
| `queued` | scheduled, not started |
| `processing` | running |
| `published` | every destination received it |
| `published_with_fallback` | published because AI failed and the workflow allowed it |
| `partially_published` | some destinations succeeded and others did not |
| `failed` | nothing was published |
| `cancelled` | stopped before it ran |

A partially published run is never reported as a success. Destinations that
already succeeded are not published again on retry; only the failed or uncertain
ones are attempted, and a result that is genuinely unknown — a timeout after the
remote side may have accepted the request — is recorded as `unknown` rather than
assumed to have failed.

## Status

`GET /api/v1/local/automation/status` reports whether the subsystem is enabled,
its worker count, how many jobs are pending or waiting to be retried, how many
are running, and the most recent terminal failures. It reports queue health
only: no message content and no credential.
