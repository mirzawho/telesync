import { EyeIcon, StickerIcon } from "lucide-react"
import * as React from "react"

import { ChannelMessageMedia } from "@/components/channel-explorer/message-media"
import { MessageActions } from "@/components/channel-explorer/message-actions"
import { MessageText } from "@/components/channel-explorer/message-text"
import { faDigits, faTime } from "@/components/persian-digits"
import { fetchMedia } from "@/lib/message-media"
import type { ChannelMessage, MessageReaction } from "@/lib/types"
import {
  Bubble,
  BubbleContent,
  BubbleReaction,
  BubbleReactions,
} from "@workspace/ui/components/bubble"
import { Button } from "@workspace/ui/components/button"
import {
  Message,
  MessageContent,
  MessageFooter,
} from "@workspace/ui/components/message"
import { cn } from "@workspace/ui/lib/utils"

// actionBand is the clear space kept at the foot of a bubble whose publishing
// action straddles its bottom edge, so the control never lands on text, media
// or the reaction chips.
const actionBand = "pb-2"

// One channel post, drawn as a message bubble.
//
// This is a channel reader, not a two-person chat: there is one stream of posts
// and no sender to alternate with, so every message sits on the same side and
// carries no avatar and no sender header. The picture and the words share one
// bubble, because in Telegram a caption and its photo are one post.

export function ChannelMessageItem({
  message,
  client,
  onPublish,
  canEdit = false,
  canDelete = false,
  deleting = false,
  deleteError = null,
  onEdit,
  onDelete,
  editing = false,
  editStatus = null,
}: {
  message: ChannelMessage
  // client: the Telegram client key the message came from. A custom-emoji
  // reaction is drawn through it, so it follows the message's own source.
  client: string
  // onPublish, when present, opens the publishing dialog for this message.
  onPublish?: () => void
  canEdit?: boolean
  canDelete?: boolean
  deleting?: boolean
  deleteError?: string | null
  onEdit?: () => void
  onDelete?: () => void
  editing?: boolean
  editStatus?: string | null
}) {
  const text = typeof message.text === "string" ? message.text : ""
  const hasText = text.trim() !== ""
  const reactions = message.reactions ?? []

  return (
    <Message align="end" className="min-w-75">
      <MessageContent className="min-w-75">
        {/* The publishing action belongs to this bubble and hangs off its own
            bottom edge. It is laid out rather than absolutely placed, so a
            bubble shorter than the action simply grows to hold it: the action
            can never push past the pane, and the bubble keeps a clear band
            above it so nothing in the bubble is covered. */}
        <div
          data-slot="message-bubble"
          className={cn("relative flex flex-col", onPublish && "pb-4", "min-w-75")}
        >
          <Bubble variant="muted" className="min-w-75">
            <BubbleContent
              className={
                cn(
                  onPublish && reactions.length === 0 ? actionBand : undefined,
                  (canEdit || canDelete) && reactions.length === 0 && "pb-12"
                )
              }
            >
              {message.media && <ChannelMessageMedia media={message.media} />}
              {hasText && <MessageText text={text} entities={message.entities} />}
            </BubbleContent>

            {reactions.length > 0 && (
              <BubbleReactions
                className={cn(
                  onPublish ? actionBand : undefined,
                  (canEdit || canDelete) && "pb-12"
                )}
                aria-label={reactionLabel(reactions, client)}
              >
                {reactions.map((reaction, index) => (
                  <ReactionChip
                    key={`${reaction.type}-${index}`}
                    client={client}
                    reaction={reaction}
                  />
                ))}
              </BubbleReactions>
            )}
            {(canEdit || canDelete) && (
              <div
                data-slot="message-actions-position"
                className="absolute bottom-1 right-1 z-20"
              >
                <MessageActions
                  canEdit={canEdit}
                  canDelete={canDelete}
                  deleting={deleting}
                  deleteError={deleteError}
                  onEdit={() => onEdit?.()}
                  onDelete={() => onDelete?.()}
                />
              </div>
            )}
          </Bubble>

          {onPublish && (
            // Under RTL `justify-start` is the physical right, which is where
            // the product wants the action; the negative margin lifts it onto
            // the bubble's bottom edge so it reads as part of that bubble.
            <div className="flex justify-start pr-4">
              <Button
                data-slot="message-publish"
                variant="secondary"
                size="sm"
                onClick={onPublish}
                title="ارسال این پیام"
                className="ring-background relative z-10 -mt-3.5 h-7 rounded-full px-3 text-xs shadow-none ring-2"
              >
                ارسال این پیام
              </Button>
            </div>
          )}
        </div>

        <MessageFooter className="-mt-7">
          {editing && editStatus && ["queued", "processing", "retrying"].includes(editStatus) && (
            <span role="status" className="text-muted-foreground">
              {editStatus === "queued" ? "ویرایش در صف…" : editStatus === "retrying" ? "تلاش دوباره برای ویرایش…" : "در حال ویرایش…"}
            </span>
          )}
          <Views message={message} />
          <span aria-hidden="true" className="text-muted-foreground/60">
            ·
          </span>
          <time
            data-slot="message-footer-time"
            dateTime={message.date}
            className="tabular-nums"
          >
            {faTime(message.date)}
          </time>
        </MessageFooter>
      </MessageContent>
    </Message>
  )
}

// Views is Telegram's own count. A post it reported no count for says so,
// rather than showing a zero that was never measured.
function Views({ message }: { message: ChannelMessage }) {
  const views = message.views
  return (
    <span className="flex items-center gap-1">
      <EyeIcon className="size-3.5" aria-hidden="true" />
      {views === null || views === undefined ? (
        <span>بازدید نامشخص</span>
      ) : (
        <span className="tabular-nums">{faDigits(views)}</span>
      )}
    </span>
  )
}

// reactionLabel is the strip's accessible name: what is on the post and how
// many of each. The chips are a report of what other people did — this panel
// has no way to add or remove a reaction — so they are never announced as
// buttons.
function reactionLabel(
  reactions: MessageReaction[],
  client: string
): string {
  const parts = reactions.map((reaction) => {
    const what =
      reaction.emoji ||
      (reaction.type === "custom_emoji" ? "استیکر سفارشی" : "واکنش")
    return `${what} ${faDigits(reaction.count)}`
  })
  return `واکنش‌ها: ${parts.join("، ")}`
}

// ReactionChip is one tally. A custom emoji is drawn from its document id
// through the API — never replaced by a different standard emoji — and a
// reaction this account chose is marked, since that is a fact about it.
function ReactionChip({
  client,
  reaction,
}: {
  client: string
  reaction: MessageReaction
}) {
  const chosen = reaction.chosen === true
  return (
    <BubbleReaction chosen={chosen}>
      <ReactionMark client={client} reaction={reaction} />
      <span className="tabular-nums">{faDigits(reaction.count)}</span>
    </BubbleReaction>
  )
}

function ReactionMark({
  client,
  reaction,
}: {
  client: string
  reaction: MessageReaction
}) {
  if (reaction.type === "custom_emoji" && client && reaction.document_id) {
    return (
      <CustomEmojiImage client={client} documentId={reaction.document_id} />
    )
  }
  if (reaction.emoji) {
    return <span aria-hidden="true">{reaction.emoji}</span>
  }
  return <StickerIcon className="size-3.5" aria-hidden="true" />
}

// CustomEmojiImage is a custom reaction emoji's image, fetched through the API
// with the same bearer credential as any other call and shown from a blob URL.
// Telegram's custom emoji are usually animated .tgs stickers a browser cannot
// render; the image is the static fallback the same document carries, and a
// document that has none falls back to a sticker icon rather than to a
// different emoji.
function CustomEmojiImage({
  client,
  documentId,
}: {
  client: string
  documentId: number
}) {
  const [url, setUrl] = React.useState("")
  const [failed, setFailed] = React.useState(false)

  React.useEffect(() => {
    if (failed) return
    const controller = new AbortController()
    let objectUrl = ""
    let live = true
    fetchCustomEmoji(client, documentId, controller.signal)
      .then((value) => {
        if (!live) {
          if (value) URL.revokeObjectURL(value)
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
      if (objectUrl) URL.revokeObjectURL(objectUrl)
    }
  }, [failed, client, documentId])

  return (
    <span
      className="flex size-4 items-center justify-center"
      aria-hidden="true"
    >
      {url ? (
        <img
          src={url}
          alt=""
          className="size-4 object-contain"
          draggable={false}
        />
      ) : (
        <StickerIcon className="size-3.5" />
      )}
    </span>
  )
}

async function fetchCustomEmoji(
  client: string,
  documentId: number,
  signal: AbortSignal
): Promise<string> {
  return (
    (await fetchMedia(`/telegram/${client}/custom_emoji/${documentId}`, signal)) ??
    ""
  )
}
