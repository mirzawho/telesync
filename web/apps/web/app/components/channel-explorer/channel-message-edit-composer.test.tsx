import assert from "node:assert/strict"
import { test } from "node:test"
import { renderToStaticMarkup } from "react-dom/server"

import { ChannelMessageComposer } from "@/components/channel-explorer/channel-message-composer"
import type { ChannelMessage } from "@/lib/types"

const message: ChannelMessage = {
  id: 72,
  date: "2026-11-10T12:00:00Z",
  text: "متن اصلی برای ویرایش",
  media: { id: "photo-72", type: "photo", label: "عکس", filename: "original.jpg", url: "/media/72" },
  views: 3,
  reactions: [],
  entities: [],
}

const render = (originalMessage: ChannelMessage) => renderToStaticMarkup(
  <ChannelMessageComposer
    writable
    submitting={false}
    error={null}
    mode={{ type: "edit", messageId: originalMessage.id, originalMessage }}
    onSubmit={() => undefined}
    onSaveEdit={() => true}
    onCancelEdit={() => undefined}
  />
)

test("edit mode reuses one compact composer textarea and replaces Send with Save", () => {
  const markup = render(message)
  assert.equal((markup.match(/<textarea\b/g) ?? []).length, 1)
  assert.match(markup, /متن اصلی برای ویرایش/)
  assert.match(markup, /data-slot="edit-context"/)
  assert.match(markup, /ویرایش پیام/)
  assert.match(markup, /original\.jpg/)
  assert.match(markup, /ذخیره تغییرات/)
  assert.doesNotMatch(markup, /ارسال پیام|data-slot="channel-message-edit"|<fieldset|type="radio"|<select/)
})

test("media-only edit context uses media metadata instead of invented text", () => {
  const markup = render({ ...message, text: "", media: { ...message.media!, type: "voice", label: "پیام صوتی", filename: undefined } })
  assert.match(markup, /پیام صوتی/)
  assert.doesNotMatch(markup, /متن اصلی برای ویرایش/)
})
