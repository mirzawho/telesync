<!-- title: Publishing -->
<!-- manual: publishing -->

# Publishing

Publishing turns a message that was **read** from a source channel into a **new**
post somewhere else: a channel on a Telegram connection, or a registered
WordPress site. It is the one part of this API that writes to Telegram's content
and to a website.

It is not the only way to put a message in a channel. **Direct sending** — below
— sends a message the caller wrote and a file they uploaded, with no source
message behind it, and is delivered in the background rather than during the
request. Both go out through the same door: the same destination resolution, the
same permissions and the same meaning for a failure.

## What is sent is what was prepared

The request carries the outgoing text and its formatting, not the source
message. The source is read so its attachment can be carried over; it is never
edited, forwarded or deleted by publishing. The caller decides what the
outgoing post says.

## Two destinations, one permission

Both routes need the global `content.publish` permission. Which destinations a
caller may name is decided by the ordinary visibility rule: a WordPress site or
a channel the caller may not see is refused with
`publish_destination_forbidden`. An account may publish only where it can
already look.

## Telegram

`POST /api/v1/telegram/{key}/messages/copy` sends through the connection named by
`{key}` and nowhere else. Before the message goes out the backend resolves the
destination on **that** connection and asks Telegram whether the account may
post there. Reading a channel, being a member of it, or holding a local record
of it is not proof of being able to post to it.

The route is named `copy` because that is what it does: the body must name a
`source` message that already exists, and it carries no file of its own. A
request to the same path with the `copy` segment left off answers
`404 not_found` and names this route; there is no free-text send beside it.

A connection that cannot post answers `publish_permission_denied`. A custom
MTProto backend that does not implement the send answers an error; it is never
answered by another connection.

### Content

`content.text` is the outgoing body and `content.entities` its formatting spans,
measured in **UTF-16 code units** — the same units Telegram uses, so a Persian
letter is one unit and an emoji outside the Basic Multilingual Plane is two. A
span that falls outside the text is refused rather than sent malformed.

A formatting span this adapter has no constructor for is left out and the count
is reported in `unsendable_entities`. The text is sent regardless; the loss is
never hidden.

### Media

`media.mode` is `preserve_source` to carry the source attachment over, or
`omit` for text only. The attachment is read from the **source** connection and
uploaded through the **destination** one: the two sessions never share a file
reference. An attachment larger than 32 MiB cannot be sent
(`publish_media_too_large`), matching the download limit.

## WordPress

`POST /api/v1/local/wordpress/{id}/posts` publishes to the registered site
`{id}`. The site is looked up in the local record and its stored credential is
opened on the backend; no URL or password is ever taken from the request.

`post.title` is required. `post.tags` are WordPress `post_tag` **terms**: each
name is resolved to an existing term id, and a missing one is created when the
WordPress user may do so. A tag that cannot be resolved or created is reported
and no post is published pretending it was assigned.

`media.mode` is `preserve_source` to upload the source attachment to the site's
library and attach it to the post, or `omit`. An upload that fails means no post
is created.

A site that answers a redirect, HTML or a non-REST page is
`publish_wordpress_incompatible`; a site that cannot be reached is
`publish_wordpress_unreachable`. The credential is never returned.

## Direct sending

`POST /api/v1/telegram/{key}/channels/{channel_id}/messages/send` sends a
message the caller composed: text they wrote and, optionally, **one** file they
uploaded. There is no source message. Nothing is read back from Telegram to
build the post, and no source channel, source message id or `media.mode` is
required or accepted.

It is asynchronous. The request is answered with `202 Accepted` once the work is
safely recorded; a background worker uploads the attachment and sends the
message afterwards. `202` means the send is **safe with this installation**, not
that it has happened.

### Route

| Part | Meaning |
| --- | --- |
| `{key}` | the Telegram connection to send through — `official`, `cat`, whatever the deployment named it. The message goes out on that connection and no other |
| `{channel_id}` | the Telegram channel id **on that connection**, as a decimal string. It is not a local `managed_channels` id, and the destination need not be a recorded source channel |
| `{operation_id}` | the opaque id returned when the send was accepted, used only for reading its status |

### Authentication and permissions

The route needs a valid access token and the global `content.publish`
permission. The destination is checked against the ordinary visibility rule —
denied by default, an explicit deny wins, and a write needs the view — after
resolving the Telegram channel id to its local record. The account's current
permissions are read **again** when the send is performed, so a grant withdrawn
while the message was queued stops it.

The operator token has no account behind it and cannot send: a send is recorded
against the account it runs under, and there is none to record it against.

### Request — JSON, text only

```http
POST /api/v1/telegram/cat/channels/98/messages/send
Authorization: Bearer <token>
Content-Type: application/json
Idempotency-Key: hello-001
```

```json
{
  "text": "سلام! این پیام در پس‌زمینه ارسال می‌شود.",
  "entities": [],
  "options": {
    "silent": false,
    "disable_web_page_preview": false
  }
}
```

`text` is required when no file is sent. `entities` are formatting spans in
**UTF-16 code units**, exactly as on the copy route: a span outside the text, a
negative offset or a type nobody recognises is refused. `options.silent` and
`options.disable_web_page_preview` are the only switches.

### Request — multipart, with an attachment

`Content-Type: multipart/form-data` with exactly two fields, and either may come
first:

| Field | Type | Required |
| --- | --- | --- |
| `payload` | the JSON above, as a string | always |
| `file` | the attachment's bytes | when `media` is named |

```bash
curl -X POST \
  "http://localhost:8080/api/v1/telegram/cat/channels/98/messages/send" \
  -H "Authorization: Bearer $TOKEN" \
  -H "Idempotency-Key: photo-001" \
  -F 'payload={"text":"تصویر جدید","media":{"kind":"photo"}};type=application/json' \
  -F "file=@./picture.jpg;type=image/jpeg"
```

A second `file` or `payload` part is refused — never silently ignored, and never
quietly resolved by taking one of them. A field with any other name is refused
too. The `payload` must be valid JSON. The file is streamed into a staging area
as it arrives; it is never held in memory whole.

### Media kinds

`media.kind` is what the caller says the file is, and it is never guessed from a
file name or a content type:

| `kind` | Sent as |
| --- | --- |
| `photo` | a Telegram photograph |
| `video` | a video, with its length |
| `audio` | an ordinary audio attachment, with its name and its length |
| `voice` | a **Telegram voice message** — `documentAttributeAudio` with `voice=true` and no file name |
| `document` | a document, with the sender's file name preserved |

`media.duration_seconds` is the recording length and applies to `audio` and
`voice` only. Zero sends no length rather than a guess.

A **voice message is not a downloadable audio file.** It is a document whose
audio attribute carries `voice=true`, and it deliberately has **no** filename
attribute: adding a name is what turns a recording into a file a client offers
to download instead of play. A voice send is never relabelled into a document,
and a file that is not a recording is never sent as one.

The file's bytes must be compatible with the kind: the first bytes are sniffed
and what they say wins over the content type the client declared. A PDF offered
as `photo` is `invalid_media_kind`; it is not quietly sent as a document.

A message is text, or a file, or both. A request with neither is refused. A
caption beside an attachment is limited to 1024 characters, and a message body
to 4096; nothing is truncated — an over-long body is refused so the caller can
fix it.

### Photo

```bash
curl -X POST "$URL/.../messages/send" -H "Authorization: Bearer $TOKEN" \
  -F 'payload={"text":"یک تصویر آزمایشی","media":{"kind":"photo"}};type=application/json' \
  -F "file=@./photo.jpg;type=image/jpeg"
```

### Document

```bash
curl -X POST "$URL/.../messages/send" -H "Authorization: Bearer $TOKEN" \
  -F 'payload={"text":"فایل مورد نظر پیوست شده است.","media":{"kind":"document"}};type=application/json' \
  -F "file=@./report.pdf;type=application/pdf"
```

The destination receives a document carrying `report.pdf` as its name.

### Voice message

```bash
curl -X POST "$URL/.../messages/send" -H "Authorization: Bearer $TOKEN" \
  -F 'payload={"text":"","media":{"kind":"voice","duration_seconds":5}};type=application/json' \
  -F "file=@./voice.ogg;type=audio/ogg"
```

The destination receives a real Telegram voice message with a five-second
length, not a downloadable OGG file.

### The answer

```http
202 Accepted
Location: /api/v1/telegram/cat/channels/98/messages/send/send_01EXAMPLE
```

```json
{
  "data": {
    "operation_id": "send_01EXAMPLE",
    "status": "queued",
    "accepted_at": "2026-11-09T12:30:00Z",
    "destination": {"client_key": "cat", "channel_id": "98"},
    "status_url": "/api/v1/telegram/cat/channels/98/messages/send/send_01EXAMPLE"
  },
  "meta": {"request_id": "req_example", "generated_at": "2026-11-09T12:30:00Z"}
}
```

Nothing here claims the message was sent, that the attachment reached Telegram,
or that a destination message id exists. Those are answers to poll for. If the
attachment could not be staged, the operation could not be recorded or the job
could not be scheduled, the request is refused instead — there is no `202` for
work that is not safe.

### Reading the outcome

```http
GET /api/v1/telegram/{key}/channels/{channel_id}/messages/send/{operation_id}
```

Reading never sends anything and never advances an operation. An operation
belongs to the account that created it and to the destination named in the path;
an id presented under either a different account or a different channel answers
`404`, and so does an id that was never issued — knowing one must not confirm
that it exists.

| `status` | Meaning |
| --- | --- |
| `queued` | accepted; nothing has been sent |
| `processing` | a worker holds it and is doing the work now (`stage` says which part) |
| `retrying` | an attempt did not complete and another is scheduled |
| `sent` | the destination confirmed the message; `result.message_id` is its id |
| `failed` | it will not be tried again; `error` says why |
| `unknown` | the request may have reached the destination and no answer came back |

```json
{
  "data": {
    "operation_id": "send_01EXAMPLE",
    "status": "sent",
    "attempt_count": 1,
    "destination": {"client_key": "cat", "channel_id": "98"},
    "accepted_at": "2026-11-09T12:30:00Z",
    "completed_at": "2026-11-09T12:30:08Z",
    "result": {"message_id": 1234}
  },
  "meta": {"request_id": "req_example", "generated_at": "2026-11-09T12:30:08Z"}
}
```

A failure reports a code and a sentence and never a staging path, a credential
or anything else about this installation.

### Idempotency

Send an `Idempotency-Key` and a repeated request is safe:

- the same key with the same request **returns the operation that already
  exists** and sends nothing again (`Idempotent-Replay: true`);
- the same key with a different request is `publish_idempotency_conflict`;
- a request that completed is replayed the same way and is never re-sent.

A request is identified by its destination, text, entities, options, media kind
and the **digest of the attachment's bytes** — never by its file name. Two files
called `report.pdf` are the same request only if they are the same bytes. The
key is scoped to the account, so one account's key is never answered with
another's operation.

### Retries and the outcome nobody confirmed

Every attempt sends the same Telegram `random_id`, written down before the first
one. That is what makes a resend after a lost confirmation the same message
rather than a second one, as far as the destination's protocol allows.

A failure the destination answered — a refusal, a flood wait, an unsupported
format — is retried up to the queue's attempt budget with exponential backoff,
honouring any delay the destination asked for. A failure that will never succeed
— a revoked grant, an unsupported media kind, an attachment that has gone
missing — ends the operation and says why.

An outcome that **nobody confirmed** is `unknown`, is terminal, and is never
retried automatically. The message may already be in the channel, and trying
again is the one thing certain to make a second one. Exactly-once delivery
across an arbitrary remote server cannot be guaranteed; this is the strongest
practical protection the protocol offers, and `unknown` is where it stops.

### Background delivery

Both text-only and file sends run through the same durable queue as webhook
deliveries and workflow runs — the same worker pool, the same lease, the same
retry policy. The work survives a restart of this process: the request, the
staged attachment, the send identity and the outcome are all recorded before the
answer is written.

The attachment is held in a staging directory that outlives the process, under a
name this server chose and with permissions only this process can read. It is
removed once the send has finished one way or the other, and an attachment
nothing claims — a crash between writing it and recording what owns it — is
swept up at start-up. An operation whose attachment has gone is stopped and
reported as a failure, never as a delivery.

### Limits

| Limit | Value |
| --- | --- |
| request body | 256 KiB for `payload`, plus the attachment and multipart overhead |
| message text | 4096 characters |
| caption beside an attachment | 1024 characters |
| attachment | 32 MiB by default (`send.max_file_bytes`) |
| attachments per send | 1 |
| idempotency key | 128 characters |

An attachment above the limit is refused **while it arrives** with
`publish_media_too_large` (`413`) and is stored nowhere.

### Errors

| HTTP | `code` | Meaning |
| --- | --- | --- |
| 400 | `invalid_input` | malformed JSON, an empty message, an invalid entity span, or a multipart body with a missing, duplicate or unexpected field |
| 400 | `invalid_media_kind` | an unknown kind, or a file that cannot be what the kind says it is |
| 401 | `unauthorized` | missing or invalid token |
| 403 | `publish_forbidden` | the caller has no `content.publish` permission |
| 403 | `publish_destination_forbidden` | the caller may not name this channel |
| 404 | `unknown_client` | no connection has that key |
| 404 | `invalid_input` | no send operation has that id on this channel |
| 409 | `publish_idempotency_conflict` | the key was used for a different request |
| 413 | `publish_media_too_large` | the attachment is above the limit |
| 415 | `invalid_input` | the body is neither JSON nor multipart |
| 503 | `send_queue_unavailable` | sending is not configured, or the work could not be made durable |

A failure that happens **after** the `202` is reported in the operation's status
and never rewrites the answer.

## Failure stages

A publish with media is two transfers, and they fail separately. The codes name
the stage, so a caller knows which one to do something about:

| `code` | Stage | Nothing was | Retry |
| --- | --- | --- | --- |
| `publish_source_media_unavailable` | the source connection could not read the source attachment | sent anywhere | yes, or `media.mode: "omit"` to publish the text alone |
| `publish_media_upload_failed` | the attachment was read and the destination would not take the post carrying it | published | after checking the destination |
| `publish_unsupported` | the destination cannot take this content at all | published | no |
| `publish_media_too_large` | the attachment is above the 32 MiB send limit | published | no |

`publish_source_media_unavailable` is a source failure and only that: the
destination is never named, blamed or reached, and the draft is exactly as the
caller left it. `publish_media_upload_failed` is the opposite stage and is never
reported in its place. An attachment that is read is read through the connection
that recorded the source message; an attachment that is sent is sent through the
destination named in the path. The two are different accounts and never share a
session, a peer store or a file reference.

Nothing is published without its attachment by accident. A draft that says
`preserve_source` and whose attachment cannot be read is refused before the
message is created; the only way a post leaves without its attachment is an
explicit `omit`.

## Idempotency

Send an `Idempotency-Key` header and the attempt is recorded:

- the same key with the same body **returns the stored result** and posts
  nothing a second time (`Idempotent-Replay: true`);
- the same key with a different body is `publish_idempotency_conflict`;
- an attempt that is still running, or whose outcome is unknown, is
  `publish_idempotency_conflict` / `publish_outcome_unknown` and is never
  re-run behind the caller's back;
- an attempt that failed may be retried with the same key.

Without the header there is no deduplication record. A request that carries one
also gets a stable Telegram `random_id`, so a resend is recognised by Telegram
itself.

## Limits

| Limit | Value |
| --- | --- |
| request body | 256 KiB |
| message text | 4096 characters |
| attachment | 32 MiB |
| idempotency key | 128 characters |

## Not implemented

Scheduling, bulk publishing and a publication history are not part of this API.
A publishing operation record is kept only for retry safety.
