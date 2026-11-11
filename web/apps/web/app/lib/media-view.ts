import { faDigits } from "@/components/persian-digits"
import type { MessageMedia } from "@/lib/types"

// Every decision the panel makes about how one attachment is presented is
// written here, and none of it touches the network or the DOM. The shapes,
// names and sizes below are facts about what the API returned, so they can be
// checked without a browser.

// MediaKind is the small set of shapes this panel knows how to draw. An
// attachment the panel has no drawing for is "other" and is described rather
// than misrendered.
export type MediaKind = "image" | "video" | "audio" | "document" | "other"

// INLINE_KINDS are the kinds shown as their content in the message card. Every
// other kind is shown as an attachment with its name and a download.
export const INLINE_KINDS: ReadonlySet<MediaKind> = new Set([
  "image",
  "video",
])

// mediaKind classifies one attachment by the type the API reported. The MIME
// type is deliberately not consulted: it describes the bytes, and the API has
// already named what kind of media this is.
export function mediaKind(media: MessageMedia): MediaKind {
  switch (media.type) {
    case "photo":
      return "image"
    case "video":
    case "gif":
    case "round_video":
      return "video"
    case "audio":
    case "voice":
      return "audio"
    case "document":
      return "document"
    default:
      return "other"
  }
}

// mediaTitle is the name shown to a person looking at the card.
//
// It is the sender's filename whenever Telegram reported one. When it did not,
// all that is known is what kind of media this is, and that description is
// shown as itself — it is never presented as the name of the file.
export function mediaTitle(media: MessageMedia): string {
  if (media.filename) return media.filename
  return media.label || media.type || "پیوست"
}

// downloadName is the name a saved file is given.
//
// Only the sender's filename is ever used. Without one the file is saved as a
// plain placeholder: a name invented here would look like a name the sender
// chose when they never chose one.
export function downloadName(media: MessageMedia): string {
  return media.filename || "attachment"
}

// mediaExtension is the file's extension as the sender's filename spells it,
// uppercased for display, or null when there is no filename or it has no
// extension. It is never derived from the MIME type.
export function mediaExtension(media: MessageMedia): string | null {
  const name = media.filename
  if (!name) return null
  const dot = name.lastIndexOf(".")
  if (dot <= 0 || dot === name.length - 1) return null
  return name.slice(dot + 1).toUpperCase()
}

// formatSize renders a byte count the way this panel writes numbers and units.
// A count the API did not report renders as nothing at all rather than as zero.
export function formatSize(bytes?: number): string | null {
  if (!bytes || bytes <= 0) return null
  if (bytes < 1024) return `${faDigits(bytes)} بایت`
  if (bytes < 1024 * 1024)
    return `${faDigits((bytes / 1024).toFixed(1))} کیلوبایت`
  return `${faDigits((bytes / (1024 * 1024)).toFixed(1))} مگابایت`
}
