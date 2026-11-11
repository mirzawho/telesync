import assert from "node:assert/strict"
import { test } from "node:test"

import { renderToStaticMarkup } from "react-dom/server"

import {
  ChannelMessageComposer,
  classifyMediaSuggestion,
  shouldSubmitOnEnter,
} from "@/components/channel-explorer/channel-message-composer"

const html = (node: React.ReactNode) => renderToStaticMarkup(node)

test("composer is a compact Persian RTL messenger input without a media Select", () => {
  const out = html(
    <ChannelMessageComposer
      writable
      submitting={false}
      error={null}
      onSubmit={() => undefined}
    />
  )
  assert.match(out, /dir="rtl"/)
  assert.match(out, /placeholder="پیام خود را بنویسید\.\.\."/)
  assert.match(out, /aria-label="ارسال پیام"/)
  assert.match(out, /aria-label="افزودن پیوست"/)
  assert.match(out, /<svg/)
  assert.match(out, /data-slot="channel-message-composer"/)
  assert.match(out, /rows="1"/)
  assert.doesNotMatch(out, /نوع رسانه|<select/)
})

test("read-only and pending states disable submission and pending is labelled", () => {
  const readOnly = html(
    <ChannelMessageComposer
      writable={false}
      submitting={false}
      error={null}
      onSubmit={() => undefined}
    />
  )
  assert.match(readOnly, /disabled/)

  const pending = html(
    <ChannelMessageComposer
      writable
      submitting
      error={null}
      onSubmit={() => undefined}
    />
  )
  assert.match(pending, /aria-label="ارسال پیام"/)
})

test("acceptance callback is not triggered merely by rendering", () => {
  let accepted = false
  html(
    <ChannelMessageComposer
      writable
      submitting={false}
      error={null}
      onSubmit={() => Promise.resolve()}
      onAccepted={() => { accepted = true }}
    />
  )
  assert.equal(accepted, false)
})

test("classifies MIME only as a suggestion, never inferring voice", () => {
  assert.equal(classifyMediaSuggestion("image/jpeg"), "photo")
  assert.equal(classifyMediaSuggestion("video/mp4"), "video")
  assert.equal(classifyMediaSuggestion("audio/mpeg"), "audio")
  assert.equal(classifyMediaSuggestion("application/pdf"), "document")
  assert.equal(classifyMediaSuggestion(""), null)
})

test("attachment accept filters distinguish voice from ordinary audio", async () => {
  const { attachmentAccept } = await import("@/components/channel-explorer/channel-message-composer")
  assert.equal(attachmentAccept("photo"), "image/*")
  assert.equal(attachmentAccept("video"), "video/*")
  assert.equal(attachmentAccept("audio"), "audio/*")
  assert.equal(attachmentAccept("voice"), ".ogg,.oga,.opus,audio/ogg,audio/opus")
  assert.equal(attachmentAccept("document"), undefined)
})

test("Enter submits only outside Shift and IME composition", () => {
  assert.equal(shouldSubmitOnEnter({ key: "Enter", shiftKey: false, isComposing: false }), true)
  assert.equal(shouldSubmitOnEnter({ key: "Enter", shiftKey: true, isComposing: false }), false)
  assert.equal(shouldSubmitOnEnter({ key: "Enter", shiftKey: false, isComposing: true }), false)
  assert.equal(shouldSubmitOnEnter({ key: "x", shiftKey: false, isComposing: false }), false)
})
