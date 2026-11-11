import assert from "node:assert/strict"
import { test } from "node:test"
import { createElement } from "react"
import { renderToStaticMarkup } from "react-dom/server"

import { ChannelMessageAlbum } from "@/components/channel-explorer/message-album"
import { groupMessageAlbums } from "@/lib/message-albums"
import { mediaKind } from "@/lib/media-view"
import type { ChannelMessage } from "@/lib/types"

const message = (id: number, grouped_id?: string): ChannelMessage => ({
  id, date: `2026-11-11T00:00:0${id}Z`, grouped_id, views: null, reactions: [],
})

test("groups only contiguous album members and keeps stable order", () => {
  const runs = groupMessageAlbums([
    message(1, "album"), message(2, "album"), message(3),
    message(4, "album"), message(5, "album"),
  ])
  assert.deepEqual(runs.map((run) => [run.kind, run.messages.map((item) => item.id)]), [
    ["album", [1, 2]], ["message", [3]], ["album", [4, 5]],
  ])
})

test("deduplicates repeated message IDs within an album", () => {
  const runs = groupMessageAlbums([message(1, "album"), message(1, "album"), message(2, "album")])
  assert.deepEqual(runs[0].messages.map((item) => item.id), [1, 2])
})

test("round video is displayed with video media controls", () => {
  assert.equal(mediaKind({ type: "round_video" }), "video")
})

test("album renders media in one gallery and only its actual caption", () => {
  const html = renderToStaticMarkup(createElement(ChannelMessageAlbum, { messages: [
    { ...message(1, "album"), media: { type: "photo", url: "/photo" } },
    { ...message(2, "album"), text: "album caption", media: { type: "round_video", url: "/video" } },
  ] }))
  assert.equal((html.match(/data-album-message-id=/g) ?? []).length, 2)
  assert.equal((html.match(/data-album-caption-id=/g) ?? []).length, 1)
  assert.match(html, /<video/)
  assert.match(html, /album caption/)
})
