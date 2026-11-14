// The left-hand column: what is about to be published.
//
// It reads the shared draft through the same composition the request body uses,
// so the preview is the outgoing post rather than a picture of the editor. It
// changes as the user types, with no save and no refresh.

import { ImageIcon } from "lucide-react"

import { Badge } from "@workspace/ui/components/badge"
import { Separator } from "@workspace/ui/components/separator"
import { cn } from "@workspace/ui/lib/utils"

import { faDigits } from "@/components/persian-digits"
import { SourceAttachmentMedia } from "@/components/publish/source-attachment-media"
import type { PublishingOptions } from "@/components/publish/publish-options"
import { mediaTitle } from "@/lib/media-view"
import { fetchMedia, releaseMedia } from "@/lib/message-media"
import * as React from "react"
import {
  attachedMedia,
  composeTelegramOutgoing,
  type PublishDraftState,
  type SourceAttachment,
} from "@/lib/publish-draft"
import type { MessageMedia } from "@/lib/types"

export interface LivePreviewPanelProps {
  state: PublishDraftState
  options: PublishingOptions
  className?: string
}

export function LivePreviewPanel({ state, options, className }: LivePreviewPanelProps) {
  return (
    <section
      data-panel="preview"
      className={cn("flex min-h-0 flex-col gap-4", className)}
      aria-label="پیش‌نمایش"
    >
      <h2 className="text-sm font-semibold">پیش‌نمایش</h2>
      <p className="text-xs text-muted-foreground">محتوای در حال انتشار، نه پیام اصلی.</p>

      {state.platform === "telegram" ? (
        <TelegramPostPreview state={state} options={options} />
      ) : (
        <WordPressArticlePreview state={state} options={options} />
      )}
    </section>
  )
}

const EMPTY_PREVIEW = "محتوایی برای پیش‌نمایش وجود ندارد"

// ---------------------------------------------------------------------------
// Telegram
// ---------------------------------------------------------------------------

function TelegramPostPreview({
  state,
  options,
}: {
  state: PublishDraftState
  options: PublishingOptions
}) {
  // The preview composes the outgoing message exactly as the request will send
  // it, so a tag or mention the composition drops is not shown here either.
  const outgoing = composeTelegramOutgoing(state)
  const media = attachedMedia(state)
  const chosen = options.channels.filter((c) => state.telegram.channelIds.includes(c.id))
  const empty = outgoing.text.trim().length === 0 && media.length === 0

  return (
    <div className="flex flex-col gap-3">
      {state.telegram.accountID && state.telegram.channelIds.length > 0 && (
        <p className="text-xs text-muted-foreground">
          ارسال به {faDigits(chosen.length || state.telegram.channelIds.length)} کانال
          {chosen.length > 0 && (
            <span className="ms-1">({chosen.map((c) => c.title).join("، ")})</span>
          )}
        </p>
      )}

      <div className="rounded-2xl border bg-background p-3">
        {empty ? (
          <p className="py-8 text-center text-sm text-muted-foreground">{EMPTY_PREVIEW}</p>
        ) : (
          <div className="flex flex-col gap-3">
            {media.map((attachment) => (
              <div key={attachment.id} data-testid="preview-media">
                <SourceAttachmentMedia attachment={attachment} attached />
              </div>
            ))}
            {outgoing.text.trim().length > 0 && (
              <p dir="auto" className="text-sm whitespace-pre-wrap">
                {outgoing.text}
              </p>
            )}
          </div>
        )}
      </div>

      {!empty && (
        <p className="text-xs text-muted-foreground">
          این پیش‌نمایش همان چیزی است که ارسال می‌شود؛ آمار بازدید و واکنش‌های پیام اصلی منتقل نمی‌شود.
        </p>
      )}
    </div>
  )
}

// ---------------------------------------------------------------------------
// WordPress
// ---------------------------------------------------------------------------

function WordPressArticlePreview({
  state,
  options,
}: {
  state: PublishDraftState
  options: PublishingOptions
}) {
  const draft = state.wordpress
  const site = options.sites.find((s) => s.id === draft.siteId)
  const featured =
    draft.featuredMediaId === null
      ? null
      : state.source.attachments.find((a) => a.id === draft.featuredMediaId) ?? null
  const categories = options.categories.filter((c) => draft.categoryIds.includes(c.id))
  const tags = options.tags.filter((t) => draft.tagIds.includes(t.id))
  const empty = draft.title.trim().length === 0 && draft.content.trim().length === 0

  return (
    <div className="flex flex-col gap-3">
      {site && (
        <p className="text-xs text-muted-foreground">
          نمایش محتوای مقاله در <span className="font-medium text-foreground">{site.title}</span>
          {" — "}
          <span dir="ltr">{site.domain}</span>
        </p>
      )}

      <div className="overflow-hidden rounded-2xl border bg-background">
        {empty ? (
          <p className="px-3 py-8 text-center text-sm text-muted-foreground">{EMPTY_PREVIEW}</p>
        ) : (
          <>
            {featured && (
              <div data-testid="featured-image" className="bg-muted">
                <PreviewImage attachment={featured} />
              </div>
            )}
            <div className="flex flex-col gap-3 p-4">
              <h3 dir="auto" className="text-lg font-semibold">
                {draft.title || "— عنوان —"}
              </h3>
              {draft.slug && (
                <p dir="ltr" className="text-xs text-muted-foreground">
                  /{draft.slug}
                </p>
              )}
              <p dir="auto" className="text-sm leading-7 whitespace-pre-wrap">
                {draft.content}
              </p>

              {(categories.length > 0 || tags.length > 0) && (
                <>
                  <Separator />
                  <div className="flex flex-wrap items-center gap-1.5">
                    {categories.map((c) => (
                      <Badge key={`c-${c.id}`} variant="secondary">
                        {c.name}
                      </Badge>
                    ))}
                    {tags.map((t) => (
                      <Badge key={`t-${t.id}`} variant="outline">
                        #{t.name}
                      </Badge>
                    ))}
                  </div>
                </>
              )}
            </div>
          </>
        )}
      </div>

      {!empty && (
        <p className="text-xs text-muted-foreground">
          این نمایش، محتوای مقاله است؛ چیدمان نهایی به قالب سایت مقصد بستگی دارد.
        </p>
      )}
    </div>
  )
}

// PreviewImage draws the chosen featured image. Its bytes come through the
// panel's own authenticated media route.
function PreviewImage({ attachment }: { attachment: SourceAttachment }) {
  const [url, setUrl] = React.useState("")

  React.useEffect(() => {
    if (!attachment.url) return
    const controller = new AbortController()
    let live = true
    let objectUrl = ""
    fetchMedia(attachment.url, controller.signal)
      .then((value) => {
        if (!live) {
          if (value) releaseMedia(value)
          return
        }
        objectUrl = value
        setUrl(value ?? "")
      })
      .catch(() => undefined)
    return () => {
      live = false
      controller.abort()
      releaseMedia(objectUrl)
    }
  }, [attachment.url])

  if (url) {
    return (
      <img
        src={url}
        alt={attachment.filename ?? attachment.label ?? "تصویر شاخص"}
        className="max-h-56 w-full object-cover"
      />
    )
  }
  return (
    <div className="flex h-36 w-full items-center justify-center text-muted-foreground">
      <ImageIcon className="size-6" aria-hidden="true" />
      <span className="sr-only">{mediaTitle(toMessageMedia(attachment))}</span>
    </div>
  )
}

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
