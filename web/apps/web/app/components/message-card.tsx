import {
  FileAudioIcon,
  FileTextIcon,
  ImageIcon,
  LinkIcon,
  MapPinIcon,
  MusicIcon,
  EyeIcon,
  PaperclipIcon,
  ReplyIcon,
  StickerIcon,
  VideoIcon,
  VoteIcon,
} from "lucide-react"
import * as React from "react"

import { faDateTime, faDigits } from "@/components/persian-digits"
import { downloadMedia, releaseMedia, fetchMedia } from "@/lib/message-media"
import {
  downloadName,
  formatSize,
  mediaExtension,
  mediaKind,
  mediaTitle,
  type MediaKind,
} from "@/lib/media-view"
import type {
  ChannelMessage,
  MessageMedia,
  MessageReaction,
} from "@/lib/types"
import { Badge } from "@workspace/ui/components/badge"
import { cn } from "@workspace/ui/lib/utils"

// A channel message is a standalone post, not a chat bubble: a sender line
// only when Telegram reported one, the text, then whatever media it carries.

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

// INLINE_KINDS are shown as their own content: the picture, the frame of the
// video. Everything else is an attachment the card describes and offers to
// save. Media with bytes the panel can play gets them fetched; a document does
// not, so listing a channel of large files downloads nothing until asked.
function inlineKind(kind: MediaKind): boolean {
  return kind === "image" || kind === "video"
}

function needsBytes(kind: MediaKind): boolean {
  return inlineKind(kind) || kind === "audio"
}

export function MessageCard({
  message,
  client,
  newDay,
  onPublish,
}: {
  message: ChannelMessage
  // client: the Telegram client key the message came from. Reaction custom
  // emoji are fetched through it, so they follow the message's own source.
  client: string
  // newDay: this is the first message of a calendar day, which gets a small
  // date heading above it instead of repeating the date on every card.
  newDay: boolean
  // onPublish, when present, opens the publishing dialog for this message.
  onPublish?: () => void
}) {
  // text may be absent, or present as null: the API omits it when it has none.
  const text = typeof message.text === "string" ? message.text : ""
  const hasText = text.trim() !== ""
  const author = message.sender?.name || message.sender?.username

  return (
    <article className="flex flex-col gap-2">
      {newDay && (
        <div className="flex items-center gap-3 pt-4 first:pt-0">
          <span className="h-px flex-1 bg-border" aria-hidden="true" />
          <time
            dateTime={message.date}
            className="text-xs font-medium text-muted-foreground"
          >
            {faDateTime(message.date, false)}
          </time>
          <span className="h-px flex-1 bg-border" aria-hidden="true" />
        </div>
      )}

      <div className="flex flex-col gap-3 rounded-xl border bg-card p-4">
        <header className="flex flex-wrap items-center gap-x-2 gap-y-1">
          {author ? (
            <span className="text-sm font-medium">{author}</span>
          ) : null}
          {message.sender?.username ? (
            <span className="text-xs text-muted-foreground" dir="ltr">
              @{message.sender.username}
            </span>
          ) : null}
          {!author ? (
            <span className="text-xs text-muted-foreground">ارسال کانال</span>
          ) : null}
          <span className="ms-auto flex items-center gap-2 text-xs text-muted-foreground">
            <span className="tabular-nums">{faDateTime(message.date)}</span>
            <span dir="ltr" className="text-muted-foreground/70 tabular-nums">
              #{faDigits(message.id)}
            </span>
          </span>
        </header>

        {message.reply_to_message_id ? (
          <p className="flex items-center gap-1 text-xs text-muted-foreground">
            <ReplyIcon className="size-3.5" aria-hidden="true" />
            پاسخ به پیام {faDigits(message.reply_to_message_id)}
          </p>
        ) : null}

        {message.media && <MessageMediaView media={message.media} />}

        {hasText ? (
          <div className="text-sm leading-7 [overflow-wrap:anywhere] whitespace-pre-wrap">
            <Linkified text={text} />
          </div>
        ) : !message.media ? (
          <p className="text-sm text-muted-foreground italic">
            این پیام متن یا پیوستی ندارد.
          </p>
        ) : null}

        <MessageStats client={client} message={message} />
        {onPublish && (
          <div className="flex justify-end">
            <button
              type="button"
              onClick={onPublish}
              className="rounded-md border px-3 py-1 text-sm hover:bg-accent"
            >
              ارسال این پیام
            </button>
          </div>
        )}
      </div>
    </article>
  )
}

// MessageStats is the footer every card ends with: Telegram's own view count
// and the reactions it reported. A post Telegram gave no view count for says
// so in words rather than showing a made-up 0; a post with no reactions shows
// no reaction pills at all.
function MessageStats({
  client,
  message,
}: {
  client: string
  message: ChannelMessage
}) {
  const views = message.views
  const reactions = message.reactions ?? []
  return (
    <footer className="flex flex-wrap items-center gap-x-4 gap-y-2 border-t pt-3 text-xs text-muted-foreground">
      <span className="flex items-center gap-1.5">
        <EyeIcon className="size-3.5" aria-hidden="true" />
        {views === null || views === undefined ? (
          <span>بازدید نامشخص</span>
        ) : (
          <span className="tabular-nums">{faDigits(views)} بازدید</span>
        )}
      </span>
      {reactions.length > 0 && (
        <ul className="flex flex-wrap items-center gap-1.5">
          {reactions.map((reaction, index) => (
            <ReactionPill
            key={`${reaction.type}-${index}`}
            client={client}
            reaction={reaction}
          />
          ))}
        </ul>
      )}
    </footer>
  )
}

// ReactionPill is one reaction tally. The count is shown for every kind; a
// custom emoji is drawn from its document id (the API streams its image), so
// it is never replaced by a standard emoji. `chosen` marks the reactions this
// account picked; null means the response did not say, and no mark is drawn
// rather than guessing.
function ReactionPill({
  client,
  reaction,
}: {
  client: string
  reaction: MessageReaction
}) {
  const chosen = reaction.chosen === true
  const isCustom = reaction.type === "custom_emoji"
  return (
    <li
      className={cn(
        "flex items-center gap-1 rounded-full border px-2 py-0.5",
        chosen
          ? "border-primary/40 bg-primary/10 text-foreground"
          : "bg-muted/40"
      )}
      title={
        chosen
          ? "این واکنش را حساب خودتان انتخاب کرده است"
          : isCustom
            ? "واکنش با استیکر سفارشی"
            : undefined
      }
    >
      {isCustom && client && reaction.document_id ? (
        <CustomEmojiImage client={client} documentId={reaction.document_id} />
      ) : reaction.emoji ? (
        <span aria-hidden="true">{reaction.emoji}</span>
      ) : (
        <StickerIcon className="size-3.5" aria-hidden="true" />
      )}
      <span className="tabular-nums">{faDigits(reaction.count)}</span>
    </li>
  )
}

// CustomEmojiImage is a custom reaction emoji's image, fetched through the API
// with the same bearer credential as any other call and shown from a blob
// URL. Telegram's custom emoji are usually animated .tgs stickers, which a
// browser cannot render; the image is the static fallback the same document
// carries, and a document that has none — or a page that never learned the
// client key — falls back to the sticker icon rather than to a different emoji.
function CustomEmojiImage({
  client,
  documentId,
}: {
  // client is the Telegram client the message came from: custom emoji are
  // fetched through the same client's route the message was read with.
  client: string
  documentId: number
}) {
  const [url, setUrl] = React.useState("")
  const [failed, setFailed] = React.useState(false)
  const container = React.useRef<HTMLSpanElement>(null)
  const visible = useInView(container)

  React.useEffect(() => {
    if (!visible || failed) return
    const controller = new AbortController()
    let objectUrl = ""
    let live = true
    fetchMedia(`/telegram/${client}/custom_emoji/${documentId}`, controller.signal)
      .then((value) => {
        if (!live) {
          releaseMedia(value)
          return
        }
        if (!value) {
          setFailed(true)
          return
        }
        objectUrl = value
        setUrl(value)
      })
      .catch(() => {
        if (live) setFailed(true)
      })
    return () => {
      live = false
      controller.abort()
      releaseMedia(objectUrl)
      setUrl("")
    }
  }, [visible, failed, client, documentId])

  return (
    <span ref={container} className="flex size-4 items-center justify-center" aria-hidden="true">
      {url ? (
        <img src={url} alt="" className="size-4 object-contain" draggable={false} />
      ) : (
        <StickerIcon className="size-3.5" aria-hidden="true" />
      )}
    </span>
  )
}

// useInView reports whether the element has come near the viewport once, so a
// long history does not fetch every reaction image as it loads.
function useInView(ref: React.RefObject<HTMLElement | null>): boolean {
  const [seen, setSeen] = React.useState(false)
  React.useEffect(() => {
    const node = ref.current
    if (!node || seen) return
    if (typeof IntersectionObserver === "undefined") {
      setSeen(true)
      return
    }
    const observer = new IntersectionObserver(
      (entries) => {
        if (entries.some((entry) => entry.isIntersecting)) {
          setSeen(true)
          observer.disconnect()
        }
      },
      { rootMargin: "400px 0px" }
    )
    observer.observe(node)
    return () => observer.disconnect()
  }, [ref, seen])
  return seen
}

// Linkified turns bare http(s) URLs into links. Nothing else is interpreted:
// Telegram text is rendered as text, never as HTML.
const urlPattern = /(https?:\/\/[^\s<>"')\]}]+)/gi

function Linkified({ text }: { text: string }) {
  const parts = text.split(urlPattern)
  return (
    <>
      {parts.map((part, index) =>
        index % 2 === 1 ? (
          <a
            key={index}
            href={part}
            target="_blank"
            rel="noopener noreferrer nofollow"
            dir="ltr"
            className="inline text-primary underline decoration-primary/40 underline-offset-2 [unicode-bidi:isolate] hover:decoration-primary"
          >
            {part}
          </a>
        ) : (
          <React.Fragment key={index}>{part}</React.Fragment>
        )
      )}
    </>
  )
}

// MediaStatus says where an attachment's bytes are: still arriving, in hand,
// not available at all, or failed to fetch.
export type MediaStatus = "loading" | "ready" | "empty" | "error"

// MediaAttachmentCard draws one attachment.
//
// It is presentational: the bytes arrive through props, so what is on screen is
// always a straight answer about the state it was given. An audio attachment
// gets a real player, a document gets its name, its size and a download, and
// anything the panel has no drawing for is described rather than misrendered.
export function MediaAttachmentCard({
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
  const title = mediaTitle(media)
  const size = formatSize(media.size)
  const Icon = iconFor[media.type] ?? PaperclipIcon
  const what = media.label || media.type

  if (kind === "audio") {
    return (
      <div className="flex flex-col gap-2 rounded-lg border bg-muted/20 px-3 py-2.5">
        <div className="flex items-center gap-2 text-sm">
          <Icon className="size-4 shrink-0 text-muted-foreground" aria-hidden="true" />
          <span dir="auto" className="min-w-0 flex-1 truncate font-medium" title={title}>
            {title}
          </span>
          {size && (
            <span className="shrink-0 text-xs tabular-nums text-muted-foreground">
              {size}
            </span>
          )}
        </div>
        {status === "ready" && url ? (
          <audio controls preload="metadata" src={url} dir="ltr" className="w-full" />
        ) : (
          <p
            className={cn(
              "text-xs",
              status === "error" ? "text-destructive" : "text-muted-foreground"
            )}
          >
            {status === "loading"
              ? `در حال دریافت ${what}…`
              : status === "empty"
                ? `${what} در دسترس نیست`
                : `دریافت ${what} ناموفق بود`}
          </p>
        )}
      </div>
    )
  }

  if (kind === "document") {
    return (
      <div className="flex items-center gap-3 rounded-lg border bg-muted/20 px-3 py-2.5">
        <span
          className="flex size-9 shrink-0 items-center justify-center rounded-md border bg-background"
          aria-hidden="true"
        >
          <Icon className="size-4 text-muted-foreground" />
        </span>
        <div className="min-w-0 flex-1">
          <p dir="auto" className="truncate text-sm font-medium" title={title}>
            {title}
          </p>
          <p className="flex flex-wrap items-center gap-x-2 text-xs text-muted-foreground">
            {media.label && <span>{media.label}</span>}
            {mediaExtension(media) && (
              <Badge variant="muted" className="text-[0.65rem]">
                {mediaExtension(media)}
              </Badge>
            )}
            {size && <span className="tabular-nums">{size}</span>}
          </p>
        </div>
        <DownloadButton media={media} />
      </div>
    )
  }

  if (kind === "sticker") {
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

  if (!inlineKind(kind)) {
    return (
      <div className="flex items-center gap-3 rounded-lg border border-dashed bg-muted/40 px-3 py-2.5 text-sm text-muted-foreground">
        <Icon className="size-4 shrink-0" aria-hidden="true" />
        <span className="min-w-0 flex-1 truncate">{title}</span>
        {size && <span className="text-xs tabular-nums">{size}</span>}
      </div>
    )
  }

  return (
    <div className="max-w-xl overflow-hidden rounded-lg border">
      {status === "ready" &&
      (media.type === "video" || (media.type === "sticker" && media.mime_type === "video/webm")) &&
      url ? (
        <video
          src={url}
          controls
          preload="metadata"
          className="max-h-96 w-full bg-muted"
        />
      ) : status === "ready" && url ? (
        <img
          src={url}
          alt={title}
          loading="lazy"
          decoding="async"
          className="max-h-96 w-full bg-muted object-contain"
        />
      ) : (
        <div
          className={cn(
            "flex min-h-24 items-center justify-center gap-2 bg-muted/40 px-3 py-6 text-sm",
            status === "error" ? "text-destructive" : "text-muted-foreground"
          )}
        >
          {status === "loading" ? (
            <>
              <Icon className="size-4 animate-pulse" aria-hidden="true" />
              <span>در حال دریافت {what}…</span>
            </>
          ) : status === "empty" ? (
            <>
              <Icon className="size-4" aria-hidden="true" />
              <span>{what} در دسترس نیست</span>
            </>
          ) : (
            <span>دریافت {what} ناموفق بود</span>
          )}
        </div>
      )}
      <div className="flex items-center gap-2 border-t px-3 py-1.5 text-xs text-muted-foreground">
        <Icon className="size-3.5" aria-hidden="true" />
        <span className="truncate">{title}</span>
        {size && <span className="ms-auto tabular-nums">{size}</span>}
        <Badge variant="muted" className="text-[0.65rem]">
          {media.type}
        </Badge>
      </div>
    </div>
  )
}

// DownloadButton saves one attachment under the name the sender gave it.
//
// The link carries the media URL and the filename so the answer is visible and
// linkable, but the bytes are fetched with the panel's bearer credential: a
// plain navigation would arrive at the API unauthenticated. Nothing is put in
// the URL itself.
function DownloadButton({ media }: { media: MessageMedia }) {
  const name = downloadName(media)
  return (
    <a
      href={media.url}
      download={name}
      onClick={(event) => {
        event.preventDefault()
        if (media.url) {
          void downloadMedia(media.url, name).catch(() => {
            // The card says nothing more than it knows: the save simply did
            // not happen, and the button stays available to try again.
          })
        }
      }}
      className="shrink-0 rounded-md border bg-background px-3 py-1 text-sm hover:bg-accent"
    >
      دانلود
    </a>
  )
}

function MessageMediaView({ media }: { media: MessageMedia }) {
  const kind = mediaKind(media)
  const [status, setStatus] = React.useState<MediaStatus>("loading")
  const [url, setUrl] = React.useState("")
  // The bytes only become visible when the card is near the viewport, so a long
  // history does not download every attachment as it loads.
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
    <div ref={box}>
      <MediaAttachmentCard
        media={media}
        status={fetched ? status : "ready"}
        url={url}
      />
    </div>
  )
}
