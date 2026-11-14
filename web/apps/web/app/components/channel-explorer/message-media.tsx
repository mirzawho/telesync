import {
  FileAudioIcon,
  FileTextIcon,
  ImageIcon,
  LinkIcon,
  MapPinIcon,
  MusicIcon,
  PaperclipIcon,
  PlayIcon,
  StickerIcon,
  VideoIcon,
  VoteIcon,
} from "lucide-react"
import * as React from "react"

import {
  Attachment,
  AttachmentAction,
  AttachmentContent,
  AttachmentDescription,
  AttachmentIcon,
  AttachmentName,
} from "@workspace/ui/components/attachment"
import { Button } from "@workspace/ui/components/button"
import { cn } from "@workspace/ui/lib/utils"

import { downloadMedia, fetchMedia, releaseMedia } from "@/lib/message-media"
import {
  downloadName,
  formatSize,
  mediaExtension,
  mediaKind,
  mediaTitle,
  type MediaKind,
} from "@/lib/media-view"
import type { MessageMedia } from "@/lib/types"

// What one attachment looks like inside a message bubble.
//
// A picture, a video, a voice note are the message itself and are drawn as
// content: no grey card around the photo, no "عکس" badge over a photo, no file
// size beside a picture. Only a real file — a document, an archive, an APK —
// becomes an Attachment with a name and a download, because that is the only
// kind whose name and size are what the reader needs.

const iconFor: Record<string, React.ComponentType<{ className?: string }>> = {
  photo: ImageIcon,
  video: VideoIcon,
  gif: VideoIcon,
  document: FileTextIcon,
  audio: FileAudioIcon,
  voice: MusicIcon,
  sticker: StickerIcon,
  poll: VoteIcon,
  geo: MapPinIcon,
  webpage: LinkIcon,
  other: PaperclipIcon,
}

export type MediaStatus = "loading" | "ready" | "empty" | "error"

// Content kinds are shown as their content. Everything else is an attachment
// the panel describes and offers to save.
function isContentKind(kind: MediaKind): boolean {
  return kind === "image" || kind === "video" || kind === "audio"
}

// Only content the browser can play or draw is fetched. A document is not, so
// listing a channel of large files downloads nothing until the reader asks.
function needsBytes(kind: MediaKind): boolean {
  return isContentKind(kind)
}

export function ChannelMessageMedia({ media }: { media: MessageMedia }) {
  const kind = mediaKind(media)
  const [status, setStatus] = React.useState<MediaStatus>("loading")
  const [url, setUrl] = React.useState("")
  // Bytes are only fetched once the message is near the viewport: a long
  // history must not download every attachment just because it was listed.
  const [visible, setVisible] = React.useState(false)
  const box = React.useRef<HTMLDivElement>(null)
  const fetched = needsBytes(kind)

  React.useEffect(() => {
    if (!fetched) return
    const node = box.current
    if (!node) return
    if (typeof IntersectionObserver === "undefined") {
      setVisible(true)
      return
    }
    const observer = new IntersectionObserver(
      (entries) => {
        if (entries.some((entry) => entry.isIntersecting)) {
          setVisible(true)
          observer.disconnect()
        }
      },
      { rootMargin: "400px 0px" }
    )
    observer.observe(node)
    return () => observer.disconnect()
  }, [fetched])

  React.useEffect(() => {
    if (!fetched || !visible || !media.url) return
    const controller = new AbortController()
    let objectUrl = ""
    let live = true
    fetchMedia(media.url, controller.signal)
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
        if (live) setStatus("error")
      })
    return () => {
      live = false
      controller.abort()
      releaseMedia(objectUrl)
      setUrl("")
      setStatus("loading")
    }
  }, [fetched, visible, media.url])

  return (
    <div ref={box} data-slot="message-media">
      <MediaView media={media} status={fetched ? status : "ready"} url={url} />
    </div>
  )
}

// MediaView is the drawing itself: what is on screen is always a straight
// answer about the state it was handed. It knows nothing about the network.
export function MediaView({
  media,
  status = "loading",
  url = "",
}: {
  media: MessageMedia
  status?: MediaStatus
  // url is the fetched object URL, empty while the bytes are not in hand.
  url?: string
}) {
  const kind = mediaKind(media)

  if (kind === "image") return <ImageMedia media={media} status={status} url={url} />
  if (kind === "video") return <VideoMedia media={media} status={status} url={url} />
  if (kind === "audio") return <AudioMedia media={media} status={status} url={url} />
  if (kind === "sticker") return <StickerFallback media={media} />
  return <FileAttachment media={media} />
}

// TGS is not natively rendered by browsers. Keep the semantic sticker identity
// visible and accessible instead of presenting it as a generic download card.
function StickerFallback({ media }: { media: MessageMedia }) {
  return (
    <span
      role="img"
      aria-label={media.sticker_emoji ? `استیکر ${media.sticker_emoji}` : "استیکر"}
      className="flex size-24 items-center justify-center rounded-xl bg-muted/40 text-muted-foreground"
    >
      <StickerIcon className="size-10" aria-hidden="true" />
    </span>
  )
}

// The picture is the message. It keeps its own proportions, gets a soft corner
// and nothing else: no frame, no badge, no size, no caption bar.
function ImageMedia({
  media,
  status,
  url,
}: {
  media: MessageMedia
  status: MediaStatus
  url: string
}) {
  return (
    <span className="relative block overflow-hidden rounded-xl">
      {status === "ready" && url ? (
        <img
          src={url}
          alt={media.filename || "تصویر پیام"}
          loading="lazy"
          decoding="async"
          className="w-full object-contain"
        />
      ) : (
        <>
          {/* The element is always drawn so the picture's place is reserved;
              the bytes arrive over it and the overlay steps aside. */}
          <img alt="" className="max-h-[28rem] max-w-[min(100%,32rem)] w-auto" />
          <span
            role="status"
            className={cn(
              "absolute inset-0 flex items-center justify-center rounded-xl bg-muted/50 text-xs",
              status === "error" || status === "empty"
                ? "text-destructive"
                : "text-muted-foreground"
            )}
          >
            {status === "loading"
              ? "در حال بارگذاری تصویر…"
              : status === "empty"
                ? "این تصویر در دسترس نیست"
                : "دریافت تصویر ناموفق بود"}
          </span>
        </>
      )}
    </span>
  )
}

function VideoMedia({
  media,
  status,
  url,
}: {
  media: MessageMedia
  status: MediaStatus
  url: string
}) {
  return (
    <span className="relative block overflow-hidden rounded-xl bg-black/5">
      <video
        src={url || undefined}
        controls
        preload="metadata"
        poster={undefined}
        aria-label={media.filename || "ویدیوی پیام"}
        className="w-full"
      />
      {status !== "ready" && (
        <span
          role="status"
          className={cn(
            "absolute inset-0 flex items-center justify-center gap-2 rounded-xl bg-muted/50 text-xs",
            status === "error" || status === "empty"
              ? "text-destructive"
              : "text-muted-foreground"
          )}
        >
          <PlayIcon className="size-4" aria-hidden="true" />
          {status === "loading"
            ? "در حال بارگذاری ویدیو…"
            : status === "empty"
              ? "این ویدیو در دسترس نیست"
              : "دریافت ویدیو ناموفق بود"}
        </span>
      )}
    </span>
  )
}

// Audio and voice are both a player. The distinction Telegram reported is kept
// in the label, not flattened into a file-size row: a voice note is something
// to listen to, not a document.
function AudioMedia({
  media,
  status,
  url,
}: {
  media: MessageMedia
  status: MediaStatus
  url: string
}) {
  const isVoice = media.type === "voice"
  return (
    <span className="flex w-full max-w-[min(100%,24rem)] flex-col gap-1.5">
      <span className="flex items-center gap-2 text-xs text-muted-foreground">
        <span
          className="flex size-7 shrink-0 items-center justify-center rounded-full bg-background/60"
          aria-hidden="true"
        >
          {isVoice ? (
            <MusicIcon className="size-3.5" />
          ) : (
            <FileAudioIcon className="size-3.5" />
          )}
        </span>
        <span dir="auto" className="min-w-0 flex-1 truncate">
          {media.filename || (isVoice ? "پیام صوتی" : "فایل صوتی")}
        </span>
      </span>
      <audio
        src={url || undefined}
        controls
        preload="metadata"
        dir="ltr"
        aria-label={media.filename || (isVoice ? "پیام صوتی" : "فایل صوتی")}
        className="w-full"
      />
      {status !== "ready" && (
        <span
          role="status"
          className={cn(
            "text-xs",
            status === "error" || status === "empty"
              ? "text-destructive"
              : "text-muted-foreground"
          )}
        >
          {status === "loading"
            ? "در حال دریافت صدا…"
            : status === "empty"
              ? "این صدا در دسترس نیست"
              : "دریافت صدا ناموفق بود"}
        </span>
      )}
    </span>
  )
}

// A real file: its name, its size and a download. The name the sender gave it
// is what is shown — an APK says what it is, not "فایل".
function FileAttachment({ media }: { media: MessageMedia }) {
  const Icon = iconFor[media.type] ?? PaperclipIcon
  const title = mediaTitle(media)
  const size = formatSize(media.size)
  return (
    <Attachment>
      <AttachmentIcon>
        <Icon />
      </AttachmentIcon>
      <AttachmentContent>
        <AttachmentName title={title}>{title}</AttachmentName>
        <AttachmentDescription>
          {mediaExtension(media) && <span>{mediaExtension(media)}</span>}
          {size && <span className="tabular-nums">{size}</span>}
        </AttachmentDescription>
      </AttachmentContent>
      <AttachmentAction>
        <DownloadButton media={media} />
      </AttachmentAction>
    </Attachment>
  )
}

// The save fetches the bytes with the panel's own credential and hands the
// browser a file under the sender's name. A plain link would arrive at the API
// unauthenticated, so the URL never carries a token.
function DownloadButton({ media }: { media: MessageMedia }) {
  const name = downloadName(media)
  return (
    <Button
      variant="outline"
      size="sm"
      aria-label={`دانلود ${name}`}
      disabled={!media.url}
      onClick={() => {
        if (media.url) {
          void downloadMedia(media.url, name).catch(() => {
            // Nothing more is known than that the save did not happen; the
            // button stays available to try again.
          })
        }
      }}
    >
      دانلود
    </Button>
  )
}
