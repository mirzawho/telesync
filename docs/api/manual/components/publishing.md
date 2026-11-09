<!-- title: Publishing -->
<!-- manual: publishing -->

# Publishing

Publishing turns a message that was **read** from a source channel into a **new**
post somewhere else: a channel on a Telegram connection, or a registered
WordPress site. It is the one part of this API that writes to Telegram's content
and to a website.

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
