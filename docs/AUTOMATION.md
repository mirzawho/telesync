# Background automation

Telesync reacts to new messages in a managed source channel without anyone
asking it to. Two things can happen to a message: it can be sent to an external
HTTP API (a **webhook**), and it can be published to other Telegram channels or
registered WordPress sites (a **workflow**). Both are configured once and then
run on their own.

The API reference is in [`docs/api`](api/README.md); this page is about how the
system works and what to do when it does not.

## The shape of it

```
   Telegram connections (worker processes, one per client)
                    |
                    v
        gotd updates.Manager  ←→  durable pts/qts/seq state
                    |
                    v
        normalize to one bounded observation
                    |
                    v
        private ingest socket  (worker → daemon, Unix, 0600)
                    |
                    v
        event store (SQLite)  →  job queue (SQLite)
                    |                    |
                    |                    v
                    |             webhooks / workflows
                    |                    |
                    v                    v
             (deduplication)     HTTP receiver / publisher
```

There is **one** ingestion path. Webhooks and workflows share it, so a message
seen through two connections is one event and runs each automation once. There
is no listener per webhook and no listener per workflow.

Nothing here needs an HTTP request or an open browser. The queue is a table; the
daemon drains it in a background loop beside the process supervisor.

### Why the worker does not write the database

A worker owns one Telegram client and its own private socket; the daemon owns
SQLite. A worker therefore never touches the database — it sends one bounded
observation per new message over the automation ingest socket, and the daemon
persists it. Media bytes never cross that socket: an observation carries media
**metadata** only, and the bytes are read later through the existing authorized
media pipeline.

### What ordering is guaranteed

gotd advances its pts/qts/seq counters **after** the update handler returns, and
the handler only returns once the daemon has committed the event. So the stored
counter can never run ahead of the stored event. If the process dies between the
two, the counter is behind and the next `getDifference` replays the message —
which the deduplication turns into a no-op. If storing fails, the handler
returns an error and gotd does not advance at all.

This is at-least-once with idempotent ingestion, not exactly-once: a request
that times out after the other side accepted it is recorded as `unknown` and not
blindly repeated.

## Event lifecycle

1. Telegram delivers an update to a worker.
2. The worker normalizes it to one message: text, entities, views, reactions and
   **media metadata** — never media bytes.
3. The event id is derived from `(event type, network, channel, message)`. A
   replayed update therefore produces the same id.
4. If this installation published that message, it is suppressed here. A
   workflow that publishes into a monitored channel must not feed itself.
5. The event is written. A duplicate is recorded as a second *source* rather
   than a second event, so an authorized connection able to read the media is
   not lost.
6. One job is queued per subscribed webhook, and one for the channel's workflow.

Steps 5 and 6 happen in that order on purpose. A crash between them leaves a
stored event with no jobs — a gap that is visible and reparable, because
ingesting the same message again is a no-op. The reverse would leave jobs
pointing at an event that does not exist.

### Identifiers

Three numbers appear in this area and they are never interchangeable:

| Name | What it is |
| --- | --- |
| `managed_channel_id` | the local `managed_channels.id` — the only value a permission check may use |
| `channel_id` | a Telegram peer id |
| `client_key` | the configured Telegram connection |

### One message, one event

The event identity excludes the observing connection on purpose. Two accounts
watching the same channel on the same Telegram network describe one logical
message, and both would otherwise run the automations twice. The network *is*
part of the identity, so two independent backends that happen to share channel
and message numbers never collide.

The network key comes from the connection's configuration, never from its
client key: `official` clients share one network, and each custom backend is
its own.

## The queue

One table, `automation_jobs`. A worker claims a row inside a transaction and
holds a lease while it works. Two workers cannot claim the same row; a worker
that dies holds a lease that expires and is reclaimed.

- **Ordering.** A workflow has one source channel and its messages publish in
  the order they arrived. A job is claimable only when nothing earlier for the
  same target is still outstanding. A job that failed for good is recorded and
  left alone — it does not block the ones behind it, because stranding a
  channel's whole future behind one dead message is worse than a gap the record
  already explains.
- **Retries.** Exponential backoff from `automation.retry_delay`, capped. A
  provider-supplied delay (a `Retry-After`, a flood wait) is preferred and also
  capped. `automation.max_attempts` bounds the budget; a job is never retried
  forever.
- **Classification.** A handler returns a typed error saying whether another
  attempt could help. An error nobody classified is treated as retryable,
  because an unclassified failure is not proof the remote side refused — and it
  is still bounded by the budget.
- **Shutdown.** A job interrupted before it ran is released, not failed, so the
  restart retries it instead of recording an error an operator has to explain.

Delivery is **at least once**. Deduplication and idempotency make duplicates
rare, but exactly-once is not claimed across Telegram, WordPress and an external
HTTP API: a request that times out after the other side accepted it is recorded
as `unknown` and not blindly repeated.

## Recovery

Disconnections, restarts and outages are expected. After reconnecting, Telesync
restores its persisted update state and asks Telegram for what it missed.

gotd's update manager (`telegram/updates`) does this using the difference APIs,
backed by durable state in `automation_update_states`, `automation_channel_pts`
and `automation_peer_hashes`. A worker rebuilds a fresh manager per connection
generation and resumes from the counters the daemon holds, so a reconnect closes
the gap rather than starting over.

Where the manager cannot close a gap — some `ChannelDifferenceTooLong` cases — a
**bounded** fallback reads channel history from the stored high-water mark in
`automation_checkpoints`, at most `automation.recovery.batch_size` messages per
pass. The mark advances only past messages that were actually accepted, so one
that could not be stored is retried rather than skipped. A gap that cannot be
closed at all leaves a diagnostic record rather than vanishing.

Recovery is a drain, not a download. Turning a workflow on starts it from **new**
messages; it never publishes a channel's history. There is no claim of
recovering arbitrarily old messages — that is bounded by Telegram's own history
and API limits.

## Webhooks

A webhook posts a CloudEvents-shaped envelope to a URL of your choosing. The
event `id` is stable and derived, so a receiver can use it as an idempotency
key.

**Media is a link, never a file.** No uploads, no base64, no multipart, no
binary in the JSON. The payload carries type, filename, MIME type, size and a
URL to the existing media endpoint, resolved against `api.public_base_url`.

That endpoint is authenticated. A webhook secret is not an API credential and
the two are kept separate on purpose: a receiver that needs the attachment must
authenticate to Telesync like any other client. Making private attachments
publicly readable to simplify a webhook would trade a real security property for
convenience.

Four authentication modes: `hmac` (the default, Standard Webhooks compatible),
`bearer`, a custom header, and `none` when explicitly chosen. Secrets are sealed
at rest and never returned; a saved one is never loaded back into a browser.

Every request is recorded — one delivery per (webhook, event), one attempt per
HTTP exchange — with the real method, URL, headers, bodies, status, duration and
any network or TLS error. Headers and bodies are redacted before storage and
bounded by `webhooks.max_response_bytes`; a longer response is stored truncated,
with the truncation marked and the real status kept.

Outbound requests are SSRF-restricted and checked **at delivery time**, not only
when the URL is saved: HTTPS by default, no loopback, private, link-local or
metadata addresses, no unsafe IPv6, no redirects, and DNS resolved then the
validated address dialled with TLS hostname verification.

## Workflows

At most one workflow per managed channel, enforced by the database. Its source
channel is fixed at creation.

A run prepares the outgoing content, optionally polishes it with AI, publishes
to each destination through the same publisher the manual route uses, and
records each destination's result separately. Destinations that already
succeeded are never published again on retry; only failed or uncertain ones are.

| Run status | Meaning |
| --- | --- |
| `published` | every destination received it |
| `published_with_fallback` | published because AI failed and the workflow allowed it |
| `partially_published` | some succeeded and others did not — never a success |
| `failed` | nothing was published |
| `cancelled` | stopped before it ran |

### Authorization

A workflow publishes under the authority of the **account that created it**, and
that account's permissions are read again at every execution. Delete or
deactivate the account and the workflow stops, with the reason recorded. Nothing
runs with more authority than the person who configured it has right now.

The operator token is a credential and not an account. A workflow created
through it has no owner and cannot be switched on — enabling one needs a real
account.

### AI

Optional, off by default. When enabled with no prompt of its own, a built-in
editing prompt is used. The source message is untrusted content to edit: it is
data, never instructions, and neither it nor a custom prompt can change where
anything is published — there is no field for that.

If AI is enabled and fails, the message is **not published**. That is the
default. A workflow can opt into publishing the original content anyway, and the
run is then `published_with_fallback` rather than a success.

Images are excluded from AI input by default. That is independent of publishing:
an image kept away from the model still reaches the destination.

Configuration is server-side under `automation.ai`, with the key named by
`automation.ai.api_key_env` in the environment. There is no key management UI.

## When something is wrong

| Symptom | Where to look |
| --- | --- |
| Nothing is happening at all | `GET /api/v1/local/automation/status` — is the subsystem enabled, and are jobs pending? |
| One message never ran | the webhook's delivery list, or the workflow's run history. Both name the event and the error |
| A delivery keeps failing | its attempt history: the real request, the real response, the real status |
| A workflow published to some destinations only | the run is `partially_published` and the per-destination records say which |
| A workflow says `workflow_author_missing` | it was created without an owning account and cannot publish |
| A webhook cannot be enabled | `api.public_base_url` is not set, so receivers could not open the media links |
| Media links 404 for a receiver | the receiver needs a Telesync API credential; the webhook secret is not one |
| Messages are missing after a restart | the queue's high-water mark and the gap record; recovery is bounded, not unlimited |

## Configuration

Everything lives in the configuration file under `automation` (see
[`docs/telesync.example.json`](telesync.example.json)); only the AI key is an
environment variable. Every limit has a conservative default: a small worker
pool, bounded retries, bounded response bodies, bounded image input.

## Known limits

- Grouped media (an album) is one event per message and publishes as one
  destination message per item, which is what the v0.0.2 publisher's
  single-media send supports.
- History retention (`history_retention_days`) is read but not yet enforced;
  nothing prunes old rows.
- `timeout_ms` and `max_attempts` on a webhook round-trip unchanged but have no
  controls in the panel this release.
