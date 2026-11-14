import assert from "node:assert/strict"
import { test } from "node:test"
import { renderToStaticMarkup } from "react-dom/server"

import { ChannelMessageItem } from "@/components/channel-explorer/message-item"
import { ChannelMessagesPane } from "@/components/channel-explorer/channel-messages-pane"
import { jumpButtonOffset, messagePaneClearance } from "@/lib/message-pane-layout"
import type { ChannelDetail, ChannelMessage } from "@/lib/types"

const html = (node: React.ReactNode) => renderToStaticMarkup(node)

const message: ChannelMessage = {
  id: 81,
  date: "2026-11-10T12:00:00Z",
  text: "متن پیام که نباید زیر منو پنهان شود",
  views: 12,
  reactions: [{ type: "emoji", emoji: "❤️", count: 2, chosen: null }],
  entities: [],
}

test("message actions are anchored at the physical bottom-right inside their bubble", () => {
  const markup = html(
    <ChannelMessageItem
      message={message}
      client="cat"
      canEdit
      canDelete
      onEdit={() => undefined}
      onDelete={() => undefined}
      onPublish={() => undefined}
    />
  )
  assert.match(markup, /data-slot="bubble"[^>]*class="[^"]*relative/)
  assert.match(markup, /data-slot="message-actions-position"[^>]*class="[^"]*absolute[^\"]*bottom-1[^\"]*right-1/)
  assert.ok(markup.indexOf('data-slot="message-actions-position"') > markup.indexOf('data-slot="bubble"'))
  assert.match(markup, /متن پیام که نباید زیر منو پنهان شود/)
  assert.match(markup, /data-slot="bubble-reactions"/)
  assert.match(markup, /data-slot="message-footer-time"/)
  assert.match(markup, /data-slot="message-publish"/)
  assert.match(markup, /aria-label="عملیات پیام"/)
})

const channel: ChannelDetail = {
  id: "141",
  account_id: "acc_test",
  type: "channel",
  title: "کانال آزمایشی",
  avatar_available: false,
  is_member: true,
  is_managed: true,
  membership_status: "member",
  permissions: { new: true, edit: true, delete: true, read: true },
  verified: false,
  scam: false,
  fake: false,
  participants_count: 1,
}

test("chat footer overlays the local timeline with translucent blur and measured scroll clearance", () => {
  const markup = html(<ChannelMessagesPane channel={channel} channelId={7} client="cat" accountId="acc_test" />)
  assert.match(markup, /data-slot="message-pane-scroll-region"/)
  assert.match(markup, /data-slot="message-timeline-content"/)
  assert.match(markup, /data-slot="floating-chat-footer"[^>]*class="[^"]*absolute[^\"]*bottom-0[^\"]*inset-x-0[^\"]*bg-background\/80[^\"]*backdrop-blur-xl/)
  assert.match(markup, /data-slot="message-timeline-content"[^>]*style="[^"]*padding-bottom:/)
  assert.doesNotMatch(markup, /data-slot="floating-chat-footer"[^>]*class="[^"]*fixed/)
  assert.match(markup, /bg-background\/80/)
  assert.match(markup, /backdrop-blur-xl/)
  assert.equal(messagePaneClearance(0), 16)
  assert.equal(messagePaneClearance(180), 196)
  assert.equal(jumpButtonOffset(180), messagePaneClearance(180))
})
