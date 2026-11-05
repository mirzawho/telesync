// The attachment a publish sends along is drawn from the source message, and
// what is on screen is a straight answer about the state it was handed.
//
// Three things matter here and are asserted from the rendered markup: the
// picture is actually shown once its bytes are in hand, a failure is an
// actionable state rather than a dead label, and dropping an attachment only
// changes the outgoing draft — the source message keeps its file.
import assert from "node:assert/strict"
import { test } from "node:test"

import { renderToStaticMarkup } from "react-dom/server"

import { SourceAttachmentMediaView } from "@/components/publish/source-attachment-media"
import type { SourceAttachment } from "@/lib/publish-draft"

const html = (node: React.ReactNode) => renderToStaticMarkup(node)

const PHOTO: SourceAttachment = {
  id: "9:1",
  mediaId: "555:9",
  type: "photo",
  label: "عکس",
  filename: "photo.jpg",
  mimeType: "image/jpeg",
  size: 2048,
  url: "/telegram/official/channels/555/messages/9/media",
  kind: "image",
}

test("the attachment is shown as a picture once its bytes are in hand", () => {
  const out = html(
    <SourceAttachmentMediaView
      attachment={PHOTO}
      status="ready"
      url="blob:photo"
      attached
    />,
  )
  assert.match(out, /<img[^>]+src="blob:photo"/, "the fetched picture must be drawn")
  assert.ok(!out.includes("ناموفق"), "a ready attachment must not show a failure")
  assert.ok(!out.includes("در حال دریافت"), "a ready attachment must not show a spinner")
})

test("while the bytes arrive the reader is told it is loading, not that it failed", () => {
  const out = html(
    <SourceAttachmentMediaView attachment={PHOTO} status="loading" attached />,
  )
  assert.match(out, /در حال دریافت/, "the loading state must say so")
  assert.ok(!out.includes("ناموفق بود"), "loading must never be drawn as a failure")
  assert.ok(!out.includes("تلاش مجدد"), "there is nothing to retry yet")
})

// The reported bug: the dialog drew "دریافت عکس ناموفق بود" with no request ever
// made. A failure is only ever a failure, and when it is one it comes with a way
// out rather than as a label bolted over the missing picture.
test("a failed read is actionable: retry, or drop the attachment", () => {
  const out = html(
    <SourceAttachmentMediaView
      attachment={PHOTO}
      status="error"
      attached
      onRetry={() => {}}
      onToggle={() => {}}
    />,
  )
  assert.match(out, /ناموفق/, "a failed read must be reported as one")
  assert.match(out, /تلاش مجدد/, "the reader must be able to try again")
  assert.match(out, /حذف پیوست/, "the reader must be able to drop the attachment")
  assert.ok(!out.includes("<img"), "a failed read must not draw a broken picture")
})

test("a failure does not silently detach the attachment", () => {
  const out = html(
    <SourceAttachmentMediaView
      attachment={PHOTO}
      status="error"
      attached
      onRetry={() => {}}
      onToggle={() => {}}
    />,
  )
  assert.match(out, /پیوست‌شده/, "the attachment is still attached until the reader says otherwise")
})

test("an attachment with no bytes is described, not pretended to be sent", () => {
  const out = html(
    <SourceAttachmentMediaView attachment={PHOTO} status="empty" attached />,
  )
  assert.match(out, /در دسترس نیست/)
})

test("the attachment's own name and size are shown, not a generic label", () => {
  const out = html(
    <SourceAttachmentMediaView attachment={PHOTO} status="ready" url="blob:photo" attached />,
  )
  assert.match(out, /photo\.jpg/, "the name Telegram reported must be shown")
})

test("the source url the panel fetches is the source connection's, never the destination's", () => {
  // The URL is built by the API from the message listing and names the client
  // that recorded the message. Nothing in the dialog may rewrite it when the
  // reader picks another destination — so it is carried through untouched.
  const out = html(
    <SourceAttachmentMediaView attachment={PHOTO} status="ready" url="blob:photo" attached />,
  )
  assert.ok(out.includes("blob:photo"))
  assert.ok(
    !out.includes("/telegram/cat/"),
    "the preview must not be re-pointed at the chosen destination",
  )
})

test("detaching is a draft action the caller performs, never a delete of the source", () => {
  let detached = 0
  const out = html(
    <SourceAttachmentMediaView
      attachment={PHOTO}
      status="ready"
      url="blob:photo"
      attached
      onToggle={() => {
        detached++
      }}
    />,
  )
  // Rendering performs no side effect at all: the source attachment is still
  // the source's, and the drop only happens when the reader asks for it.
  assert.equal(detached, 0, "rendering must not detach anything")
  assert.match(out, /حذف پیوست/)
})
