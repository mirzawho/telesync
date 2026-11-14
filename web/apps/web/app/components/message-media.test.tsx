import assert from "node:assert/strict"
import { test } from "node:test"

import { renderToStaticMarkup } from "react-dom/server"

import { MediaAttachmentCard, MessageCard } from "@/components/message-card"
import type { MessageMedia } from "@/lib/types"

const APK: MessageMedia = {
  id: "6:36",
  type: "document",
  label: "فایل",
  filename: "original-app-name.apk",
  mime_type: "application/vnd.android.package-archive",
  size: 59_797_754,
  url: "/telegram/cat/channels/6/messages/36/media",
}

const MP3: MessageMedia = {
  id: "6:24",
  type: "audio",
  label: "صدا",
  filename: "track-01.mp3",
  mime_type: "audio/mpeg",
  size: 3_174_901,
  url: "/telegram/cat/channels/6/messages/24/media",
}

const html = (node: React.ReactNode) => renderToStaticMarkup(node)

test("an audio message renders a real player, not just a label", () => {
  const out = html(
    <MediaAttachmentCard media={MP3} status="ready" url="blob:audio-1" />
  )
  assert.match(out, /<audio/)
  assert.match(out, /controls/)
  assert.match(out, /src="blob:audio-1"/)
  // The name and the size are facts about the file and are shown beside the
  // player rather than instead of it.
  assert.match(out, /track-01\.mp3/)
  assert.match(out, /مگابایت/)
})

test("an audio player says it is loading before the bytes are in hand", () => {
  const out = html(<MediaAttachmentCard media={MP3} status="loading" />)
  assert.doesNotMatch(out, /<audio/)
  assert.match(out, /در حال دریافت/)
})

test("an audio player reports a failed fetch instead of a silent button", () => {
  const out = html(<MediaAttachmentCard media={MP3} status="error" />)
  assert.doesNotMatch(out, /<audio/)
  assert.match(out, /ناموفق/)
})

test("a document renders the filename Telegram reported", () => {
  const out = html(<MediaAttachmentCard media={APK} />)
  assert.match(out, /original-app-name\.apk/)
  // The label is a description of the media kind and stays itself; it never
  // takes the filename's place.
  assert.match(out, /فایل/)
  assert.match(out, /مگابایت/)
})

test("a document download saves the original file under its original name", () => {
  const out = html(<MediaAttachmentCard media={APK} />)
  assert.match(
    out,
    /href="\/telegram\/cat\/channels\/6\/messages\/36\/media"/,
    `download must fetch the message's own media URL: ${out}`
  )
  assert.match(out, /download="original-app-name\.apk"/)
})

test("a document with no filename is not given an invented one", () => {
  const media: MessageMedia = {
    id: "6:36",
    type: "document",
    label: "فایل",
    mime_type: "application/pdf",
    size: 4_096,
    url: "/telegram/cat/channels/6/messages/36/media",
  }
  const out = html(<MediaAttachmentCard media={media} />)
  // The description is shown as itself.
  assert.match(out, /فایل/)
  // The saved file is named plainly: nothing here looks like a name the sender
  // picked.
  assert.match(out, /download="attachment"/)
  assert.doesNotMatch(out, /فایل\.pdf/)
  assert.doesNotMatch(out, /فایل\./)
})

test("a static sticker renders as image content while a TGS sticker has an accessible fallback", () => {
  const staticSticker: MessageMedia = {
    type: "sticker",
    mime_type: "image/webp",
    sticker_emoji: "🙂",
    url: "/sticker.webp",
  }
  const image = html(
    <MediaAttachmentCard media={staticSticker} status="ready" url="blob:sticker" />
  )
  assert.match(image, /<img/)
  assert.match(image, /src="blob:sticker"/)
  assert.doesNotMatch(image, /download=/)

  const animatedSticker: MessageMedia = {
    type: "sticker",
    mime_type: "application/x-tgsticker",
    sticker_emoji: "✨",
    url: "/sticker.tgs",
  }
  const fallback = html(<MediaAttachmentCard media={animatedSticker} />)
  assert.match(fallback, /role="img"/)
  assert.match(fallback, /استیکر ✨/)
  assert.doesNotMatch(fallback, /download=/)
})

test("a long filename cannot break the card's layout", () => {
  const long = `${"very-long-file-name-".repeat(6)}final.apk`
  const out = html(<MediaAttachmentCard media={{ ...APK, filename: long }} />)
  // The name survives intact for the download...
  assert.match(out, new RegExp(`download="${long.replace(/[.*+?^${}()|[\]\\]/g, "\\$&")}"`))
  // ...while the row it is drawn in constrains it.
  assert.match(out, /min-w-0/)
  assert.match(out, /break-words|overflow-wrap|truncate/)
})

test("a document renders even when the message has no text", () => {
  const out = html(
    <MessageCard
      message={{
        id: 36,
        date: "2026-10-25T09:00:00Z",
        text: "",
        media: APK,
        views: 12,
        reactions: [],
      }}
      client="cat"
      newDay={false}
    />
  )
  assert.match(out, /original-app-name\.apk/)
  assert.match(out, /href="\/telegram\/cat\/channels\/6\/messages\/36\/media"/)
  // No text is not "nothing here": the media is the message.
  assert.doesNotMatch(out, /متن یا پیوستی ندارد/)
})

test("images and videos still render inline", () => {
  const photo: MessageMedia = {
    id: "6:1",
    type: "photo",
    label: "عکس",
    mime_type: "image/jpeg",
    size: 4242,
    url: "/telegram/cat/channels/6/messages/1/media",
  }
  const video: MessageMedia = {
    id: "6:2",
    type: "video",
    label: "ویدیو",
    mime_type: "video/mp4",
    size: 4242,
    url: "/telegram/cat/channels/6/messages/2/media",
  }
  assert.match(html(<MediaAttachmentCard media={photo} status="ready" url="blob:p" />), /<img/)
  assert.match(
    html(<MediaAttachmentCard media={video} status="ready" url="blob:v" />),
    /<video/
  )
})

test("a message with neither text nor media still says so", () => {
  const out = html(
    <MessageCard
      message={{
        id: 1,
        date: "2026-10-25T09:00:00Z",
        views: 0,
        reactions: [],
      }}
      client="cat"
      newDay={false}
    />
  )
  assert.match(out, /متن یا پیوستی ندارد/)
})
