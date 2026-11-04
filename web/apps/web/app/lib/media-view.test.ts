import assert from "node:assert/strict"
import { test } from "node:test"

import {
  downloadName,
  formatSize,
  mediaExtension,
  mediaKind,
  mediaTitle,
} from "@/lib/media-view"

// Every answer here is a fact about what the API sent. The panel is allowed to
// describe an attachment it was given no name for; it is never allowed to
// invent the name.

test("classifies the media types the panel draws", () => {
  assert.equal(mediaKind({ type: "photo", mime_type: "image/jpeg" }), "image")
  assert.equal(mediaKind({ type: "video", mime_type: "video/mp4" }), "video")
  assert.equal(mediaKind({ type: "gif", mime_type: "video/mp4" }), "video")
  assert.equal(
    mediaKind({ type: "audio", mime_type: "audio/mpeg" }),
    "audio"
  )
  assert.equal(
    mediaKind({ type: "voice", mime_type: "audio/ogg" }),
    "audio"
  )
  assert.equal(
    mediaKind({ type: "document", mime_type: "application/pdf" }),
    "document"
  )
  assert.equal(mediaKind({ type: "poll" }), "other")
  assert.equal(mediaKind({ type: "sticker" }), "other")
})

test("uses the sender's filename as the title", () => {
  assert.equal(
    mediaTitle({
      type: "document",
      label: "فایل",
      filename: "original-app-name.apk",
    }),
    "original-app-name.apk"
  )
})

test("falls back to the label when there is no filename", () => {
  // The label says what kind of media this is. It is shown as itself, never
  // dressed up as the name of a file.
  assert.equal(mediaTitle({ type: "document", label: "فایل" }), "فایل")
  assert.equal(mediaTitle({ type: "audio", label: "صدا" }), "صدا")
  assert.equal(mediaTitle({ type: "other" }), "other")
  assert.equal(mediaTitle({ type: "other", label: "" }), "other")
})

test("the exact filename is preserved, spaces and Persian included", () => {
  for (const name of [
    "my app v2 (final).apk",
    "گزارش سالانه.pdf",
    "UPPERCASE.APK",
    " padded .apk ",
  ]) {
    assert.equal(mediaTitle({ type: "document", filename: name }), name)
    assert.equal(downloadName({ type: "document", filename: name }), name)
  }
})

test("downloads are named after the sender's file", () => {
  assert.equal(
    downloadName({
      type: "document",
      label: "فایل",
      filename: "original-app-name.apk",
    }),
    "original-app-name.apk"
  )
})

test("a file with no name gets a plain placeholder", () => {
  // Nothing here pretends to be a name the sender chose.
  assert.equal(downloadName({ type: "document", label: "فایل" }), "attachment")
  assert.equal(downloadName({ type: "audio", label: "صدا" }), "attachment")
})

test("the extension comes from the filename only", () => {
  assert.equal(
    mediaExtension({ type: "document", filename: "original-app-name.apk" }),
    "APK"
  )
  assert.equal(
    mediaExtension({ type: "document", filename: "گزارش سالانه.pdf" }),
    "PDF"
  )
  assert.equal(mediaExtension({ type: "document", filename: "noext" }), null)
  assert.equal(mediaExtension({ type: "document", filename: ".hidden" }), null)
  assert.equal(mediaExtension({ type: "document", filename: "trailing." }), null)
  // A MIME type describes the bytes, not the file's name: it is not a source
  // of an extension.
  assert.equal(
    mediaExtension({
      type: "document",
      label: "فایل",
      mime_type: "application/vnd.android.package-archive",
    }),
    null
  )
})

test("sizes are rendered in Persian units", () => {
  assert.equal(formatSize(3_174_901), "۳.۰ مگابایت")
  assert.equal(formatSize(20_480), "۲۰.۰ کیلوبایت")
  assert.equal(formatSize(512), "۵۱۲ بایت")
})

test("a size the API did not report is not shown as zero", () => {
  assert.equal(formatSize(undefined), null)
  assert.equal(formatSize(0), null)
})
