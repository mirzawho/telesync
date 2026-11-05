// The source message's attachment, as the publishing dialog draws it.
//
// The bytes come from the source connection's own media route, which needs the
// panel's bearer credential — a plain <img src> carries none. So the picture is
// fetched once and drawn from an object URL, the same way the message timeline
// draws it.
//
// The card is presentational: it is handed the state of the bytes and answers
// with a straight picture of it. What it must never do is report a fetch that
// has not been attempted as a fetch that failed, which is exactly what the
// dialog used to do by passing a "ready" status with no URL.

import { RefreshCwIcon, XIcon } from "lucide-react"
import * as React from "react"

import { Badge } from "@workspace/ui/components/badge"

import { MediaAttachmentCard, type MediaStatus } from "@/components/message-card"
import { fetchMedia, releaseMedia } from "@/lib/message-media"
import type { SourceAttachment } from "@/lib/publish-draft"
import type { MessageMedia } from "@/lib/types"

export interface SourceAttachmentMediaViewProps {
  attachment: SourceAttachment
  /** status is where the bytes are: arriving, in hand, gone, or failed. */
  status: MediaStatus
  /** url is the fetched object URL, empty while the bytes are not in hand. */
  url?: string
  /** attached says whether this attachment goes out with the new message. */
  attached: boolean
  onRetry?: () => void
  /**
   * onToggle flips this attachment in the outgoing draft. Giving it is what
   * makes this an editor: the view then also says whether the attachment is
   * currently attached, and offers to drop it. The source message is never
   * touched — this is a statement about the post being composed.
   */
  onToggle?: () => void
}

/** SourceAttachmentMediaView draws one attachment and its failure state. */
export function SourceAttachmentMediaView({
  attachment,
  status,
  url = "",
  attached,
  onRetry,
  onToggle,
}: SourceAttachmentMediaViewProps) {
  return (
    <div className="flex flex-col gap-2">
      <MediaAttachmentCard media={toMessageMedia(attachment)} status={status} url={url} />
      {onToggle && (
        <div className="flex items-center gap-2">
          <Badge variant={attached ? "success" : "muted"}>
            {attached ? "پیوست‌شده" : "جدا شده"}
          </Badge>
          <button
            type="button"
            className="inline-flex items-center gap-1 rounded-lg border border-input px-2.5 py-1 text-xs hover:bg-accent"
            onClick={onToggle}
          >
            <XIcon className="size-3" aria-hidden="true" />
            {attached ? "حذف پیوست" : "پیوست دوباره"}
          </button>
        </div>
      )}
      {status === "error" && (
        <div
          role="alert"
          className="flex flex-wrap items-center gap-2 rounded-lg border border-destructive/30 bg-destructive/5 px-3 py-2"
        >
          <p className="min-w-0 flex-1 text-xs text-destructive">
            دریافت رسانه ناموفق بود. دوباره تلاش کنید یا پیوست را حذف کنید.
          </p>
          {onRetry && (
            <button
              type="button"
              className="inline-flex items-center gap-1 rounded-lg border border-input px-2.5 py-1 text-xs hover:bg-accent"
              onClick={onRetry}
            >
              <RefreshCwIcon className="size-3" aria-hidden="true" />
              تلاش مجدد
            </button>
          )}
          {attached && onToggle && (
            <button
              type="button"
              className="inline-flex items-center gap-1 rounded-lg border border-input px-2.5 py-1 text-xs hover:bg-accent"
              onClick={onToggle}
            >
              <XIcon className="size-3" aria-hidden="true" />
              حذف پیوست
            </button>
          )}
        </div>
      )}
    </div>
  )
}

export interface SourceAttachmentMediaProps
  extends Omit<SourceAttachmentMediaViewProps, "status" | "url"> {
  /** retry is a changing number that asks for the bytes again. */
  retry?: number
}

/**
 * SourceAttachmentMedia fetches one attachment and draws it.
 *
 * The bytes are fetched through the panel's own credential and handed back as
 * an object URL, which the caller of this component never sees: what it owns is
 * the decision to keep the attachment, not the picture's bytes.
 */
export function SourceAttachmentMedia({
  attachment,
  attached,
  retry = 0,
  onRetry,
  onToggle,
}: SourceAttachmentMediaProps) {
  const [status, setStatus] = React.useState<MediaStatus>(attachment.url ? "loading" : "empty")
  const [url, setUrl] = React.useState("")

  React.useEffect(() => {
    if (!attachment.url) {
      setStatus("empty")
      return
    }
    const controller = new AbortController()
    let live = true
    let objectUrl = ""
    setStatus("loading")
    fetchMedia(attachment.url, controller.signal)
      .then((value) => {
        if (!live) {
          releaseMedia(value)
          return
        }
        if (!value) {
          setStatus("empty")
          return
        }
        objectUrl = value
        setUrl(value)
        setStatus("ready")
      })
      .catch(() => {
        // An aborted request is a superseded one, not a failure: the reader
        // asked for something else and this answer no longer describes it.
        if (live && !controller.signal.aborted) setStatus("error")
      })
    return () => {
      live = false
      controller.abort()
      releaseMedia(objectUrl)
      setUrl("")
    }
  }, [attachment.url, retry])

  return (
    <SourceAttachmentMediaView
      attachment={attachment}
      status={status}
      url={url}
      attached={attached}
      onRetry={onRetry}
      onToggle={onToggle}
    />
  )
}

// toMessageMedia hands a normalised attachment back to the panel's media
// renderer, which speaks the API's own field names.
function toMessageMedia(attachment: SourceAttachment): MessageMedia {
  return {
    id: attachment.mediaId,
    type: attachment.type,
    label: attachment.label,
    filename: attachment.filename,
    mime_type: attachment.mimeType,
    size: attachment.size,
    url: attachment.url,
  }
}
