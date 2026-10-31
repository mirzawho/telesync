<!-- archived: the page this documentation replaced -->
# Messages and media

- [List messages](#list-messages) — `GET .../channels/{channel_id}/messages`
- [Get one message](#get-one-message) — `GET .../channels/{channel_id}/messages/{message_id}`
- [Download media](#download-media) — `GET .../channels/{channel_id}/messages/{message_id}/media`

All paths are under `/api/v1/telegram/{key}`. `{channel_id}` is a positive
decimal Telegram channel id (bare or `-100…`), and `{message_id}` a positive
integer message id **within that channel**. An invite reference is not accepted
here.

**Reading never marks messages as read.** The only RPC these routes issue is
`messages.getHistory` (list and single message) and an attachment download;
no read-history method is called.

---

## List messages

### `GET /api/v1/telegram/{key}/channels/{channel_id}/messages?page=1&per_page=20`

Channel history, newest first.

- **Authentication:** [the channel rule(../authentication.md#the-channel-rule)
  for that channel.
- **Path parameter:** `channel_id` — positive integer; otherwise
  `400 invalid_input` "channel_id must be a positive integer."
- **Query:** `page`, `per_page` — see [Pagination](../pagination.md).

### Success — `200`

```json
{
  "data": {
    "messages": [
      {
        "id": 1233,
        "date": "2026-10-25T09:00:00Z",
        "text": "…",
        "sender": {"id": 42, "name": "Ali", "username": "ali"},
        "media": {
          "id": "1234567890:1233",
          "type": "photo",
          "label": "photo",
          "mime_type": "image/jpeg",
          "size": 98765,
          "url": "/api/v1/telegram/official/channels/1234567890/messages/1233/media"
        },
        "reply_to_message_id": 1200
      }
    ]
  },
  "meta": {
    "request_id": "…", "generated_at": "…",
    "page": 1, "per_page": 20, "has_more": true, "next_page": 2
  }
}
```

See [Message object(../schemas/telegram.md#message-object). Notes:

- The channel object is **not** repeated in the body; the channel is in the path.
  Use [Get one channel(channels.md#get-one-channel) when you need it.
- `meta` has **no `total`**: Telegram does not report a channel's message count.
- `sender` is absent for anonymous channel posts.
- `media` is absent when the post has no attachment.
- `reply_to_message_id` is present only when Telegram reported a reply header;
  the replied-to message is not fetched.
- `media.url` is a path relative to the listener, not an absolute URL, so it
  stays correct behind any host or proxy.

### Ordering and page semantics

Messages are **newest first**. Page 1 is the newest page; page N+1 continues
strictly below the oldest message id served on page N. The worker keeps a
bounded (64-channel) map of cursors, so:

- a page requested without the pages before it being fetched **by this worker**
  restarts from the newest page — `meta.page` echoes what you asked, the content
  is the newest page;
- a worker restart loses the cursors.

Resume reliably by walking from page 1, or by using `id` values as your own
cursor. See [Pagination(../pagination.md#telegram-messages-page-n-is-only-as-reliable-as-the-pages-before-it).

### Errors

| Status | `code` | When |
| --- | --- | --- |
| 400 | `invalid_input` | non-positive/non-numeric channel id, bad `page`/`per_page`, unknown query parameter |
| 403 | `forbidden` | not permitted by the channel rule |
| 404 | `channel_not_found` | the account cannot see the channel |

### Example

```bash
curl --fail-with-body --silent --show-error \
  "${BASE_URL}/api/v1/telegram/official/channels/1671633563/messages?page=1&per_page=10" \
  --header "Accept: application/json" \
  --header "Authorization: Bearer ${TELESYNC_TOKEN}"
```

---

## Get one message

### `GET /api/v1/telegram/{key}/channels/{channel_id}/messages/{message_id}`

One post, in the same `data`/`meta` envelope and with the same message object as
the list, so a client can reuse its parser.

- **Authentication:** [the channel rule](../authentication.md#the-channel-rule).
- **Path parameters:** `channel_id` and `message_id`, both positive integers;
  otherwise `400 invalid_input`.

### Success — `200`

```json
{
  "data": {
    "messages": [
      {
        "id": 1233,
        "date": "2026-10-25T09:00:00Z",
        "text": "…",
        "media": { "id": "1671633563:1233", "type": "photo", "mime_type": "image/jpeg", "size": 98765, "url": "/api/v1/telegram/official/channels/1671633563/messages/1233/media" },
        "reply_to_message_id": 1200
      }
    ]
  },
  "meta": {"request_id": "…", "generated_at": "…"}
}
```

`messages` has exactly one element, and `meta` carries no pagination.

### Errors

| Status | `code` | When |
| --- | --- | --- |
| 400 | `invalid_input` | a non-positive channel or message id |
| 403 | `forbidden` | not permitted by the channel rule |
| 404 | `not_found` | no message with that id in the channel |
| 404 | `channel_not_found` | the account cannot see the channel |

### Example

```bash
curl --fail-with-body --silent --show-error \
  "${BASE_URL}/api/v1/telegram/official/channels/1671633563/messages/1233" \
  --header "Accept: application/json" \
  --header "Authorization: Bearer ${TELESYNC_TOKEN}"
```

---

## Download media

### `GET /api/v1/telegram/{key}/channels/{channel_id}/messages/{message_id}/media`

The bytes of the message's attachment. One attachment per message in this API's
scope: photos download the largest `PhotoSize`; documents use their own file
reference and MIME type. A single download is capped at 32 MiB in the worker.

- **Authentication:** [the channel rule(../authentication.md#the-channel-rule).
- **Path parameters:** positive integers; otherwise `400 invalid_input`.
- **Response:** the raw bytes, with:
  - `Content-Type` — Telegram's reported type, or sniffed with
    `http.DetectContentType` when it reported none (a document with no MIME type
    is therefore classified from its first bytes, which can be
    `application/octet-stream`),
  - `Content-Length`,
  - `Cache-Control: private, max-age=86400` (an attachment never changes),
  - `X-Request-Id`, `X-Content-Type-Options: nosniff`.
- **Not JSON**, so failures in the middle of the response are not in the error
  envelope; every failure this route produces is decided before the first byte
  is written.
- Read-only: downloading a photo or document does not change the account's read
  state.

### Errors

| Status | `code` | When |
| --- | --- | --- |
| 400 | `invalid_input` | a non-positive channel or message id |
| 403 | `forbidden` | not permitted by the channel rule |
| 404 | `not_found` | the message has no downloadable attachment, or its attachment is empty |
| 404 | `channel_not_found` | the account cannot see the channel |
| 500 | `worker_error` | download failed (`internal` from the worker) |
| 504 | `worker_timeout` | the download did not finish within `api.request_timeout` |

> A download must finish within `api.request_timeout` (default 30 s) or the
> answer is `504 worker_timeout`. The HTTP server's own write timeout is 120 s.

### Example

Save the bytes and inspect the headers:

```bash
curl --fail-with-body --silent --show-error \
  --dump-header /dev/stderr \
  --output /tmp/attachment.bin \
  "${BASE_URL}/api/v1/telegram/official/channels/1671633563/messages/1233/media" \
  --header "Authorization: Bearer ${TELESYNC_TOKEN}"
```

Related: [Media object(../schemas/telegram.md#media-object), [Channel avatar](channels.md#channel-avatar).
