// The centre column: the content being prepared.
//
// It becomes a message editor or an article form depending on the platform in
// the shared draft. Both edit that draft and nothing else — the source message
// they were seeded from is never written back to.

import { ImageIcon } from "lucide-react"
import * as React from "react"

import { Input } from "@workspace/ui/components/input"
import { cn } from "@workspace/ui/lib/utils"

import { faDigits } from "@/components/persian-digits"
import { LinkList } from "@/components/publish/link-list"
import { ChipInput, Field, SearchableCheckList } from "@/components/publish/publish-primitives"
import { SourceAttachmentMedia } from "@/components/publish/source-attachment-media"
import { Textarea } from "@workspace/ui/components/textarea"
import type { PublishingOptions } from "@/components/publish/publish-options"
import type { ApplyChange } from "@/components/publish/destination-panel"
import { formatSize, mediaTitle } from "@/lib/media-view"
import { fetchMedia, releaseMedia } from "@/lib/message-media"
import {
  addLink,
  deleteLink,
  editLink,
  setCategoryIds,
  setFeaturedMediaId,
  setHashtags,
  setMentions,
  setSlug,
  setTagIds,
  setTelegramText,
  setWordPressContent,
  setWordPressTitle,
  toggleAttachment,
  type PublishDraftState,
  type SourceAttachment,
  type ValidationField,
} from "@/lib/publish-draft"
import type { MessageMedia } from "@/lib/types"

// PublishErrors are the field messages the workspace is showing right now.
export type PublishErrors = Partial<Record<ValidationField, string>>

export interface ContentEditorPanelProps {
  state: PublishDraftState
  options: PublishingOptions
  onChange: ApplyChange
  errors?: PublishErrors
  className?: string
}

export function ContentEditorPanel({
  state,
  options,
  onChange,
  errors,
  className,
}: ContentEditorPanelProps) {
  return (
    <section
      data-panel="editor"
      className={cn("flex min-h-0 flex-col gap-6", className)}
      aria-label="ویرایش محتوا"
    >
      <h2 className="text-sm font-semibold">ویرایش محتوا</h2>
      {state.platform === "telegram" ? (
        <TelegramMessageEditor state={state} onChange={onChange} errors={errors} />
      ) : (
        <WordPressArticleEditor
          state={state}
          options={options}
          onChange={onChange}
          errors={errors}
        />
      )}
    </section>
  )
}

// ---------------------------------------------------------------------------
// Telegram
// ---------------------------------------------------------------------------

function TelegramMessageEditor({
  state,
  onChange,
  errors,
}: {
  state: PublishDraftState
  onChange: ApplyChange
  errors?: PublishErrors
}) {
  const draft = state.telegram
  return (
    <div className="flex flex-col gap-6">
      <h3 className="text-base font-semibold">ویرایش پیام</h3>

      <Field
        label="متن پیام"
        htmlFor="publish-text"
        error={errors?.content}
        hint="هشتگ‌ها و منشن‌ها در انتهای پیام اضافه می‌شوند؛ لینک‌ها در جای خود می‌مانند."
      >
        <Textarea
          id="publish-text"
          value={draft.text}
          dir="auto"
          rows={8}
          placeholder="متن پیام را بنویسید…"
          aria-invalid={errors?.content ? true : undefined}
          onChange={(e: React.ChangeEvent<HTMLTextAreaElement>) =>
            onChange((s) => setTelegramText(s, e.target.value))
          }
        />
      </Field>

      <Field label="هشتگ‌ها" htmlFor="publish-hashtags" hint="بدون # نوشته می‌شوند.">
        <ChipInput
          id="publish-hashtags"
          values={draft.hashtags}
          prefix="#"
          placeholder="فناوری"
          addLabel="افزودن"
          emptyLabel="هشتگی اضافه نشده است."
          removeLabel={(v) => `حذف #${v}`}
          onChange={(next) => onChange((s) => setHashtags(s, next))}
        />
      </Field>

      <Field label="منشن‌ها" htmlFor="publish-mentions" hint="با @ یا بدون آن.">
        <ChipInput
          id="publish-mentions"
          values={draft.mentions}
          prefix="@"
          placeholder="examplechannel"
          addLabel="افزودن"
          emptyLabel="منشنی اضافه نشده است."
          removeLabel={(v) => `حذف @${v.replace(/^@/, "")}`}
          onChange={(next) => onChange((s) => setMentions(s, next))}
        />
      </Field>

      <Field
        label="لینک‌ها"
        error={errors?.links}
        hint="لینک‌های پیام با متن پیوند و نشانی‌شان اینجا فهرست شده‌اند. لینکی که خودتان اضافه کنید به انتهای پیام می‌رود."
      >
        <LinkList
          links={state.links}
          onAdd={(link) => onChange((s) => addLink(s, link))}
          onEdit={(id, patch) => onChange((s) => editLink(s, id, patch))}
          onDelete={(id) => onChange((s) => deleteLink(s, id))}
        />
      </Field>

      <MediaAttachmentEditor state={state} onChange={onChange} />
    </div>
  )
}

/**
 * MediaAttachmentEditor lists the source message's attachments and says which
 * of them go out with the new message.
 *
 * Detaching is about the outgoing post only. The source message, its attachment
 * and the record behind it are untouched: this list is a statement about what
 * is about to be sent, not a way to delete anything anywhere.
 */
function MediaAttachmentEditor({
  state,
  onChange,
}: {
  state: PublishDraftState
  onChange: ApplyChange
}) {
  const { source } = state
  // A retry asks for the same attachment again. It is counted per attachment
  // rather than stored as a flag so a second retry is a second request even
  // after a failure and a retry have already been seen.
  const [retries, setRetries] = React.useState<Record<string, number>>({})
  const retry = (id: string) => setRetries((prev) => ({ ...prev, [id]: (prev[id] ?? 0) + 1 }))
  if (source.attachments.length === 0) return null
  const byId = new Map(state.telegram.attachments.map((a) => [a.id, a.attached]))

  return (
    <div className="flex flex-col gap-3">
      <h4 className="text-sm font-medium">رسانه‌های پیوست‌شده</h4>
      <ul className="flex flex-col gap-3">
        {source.attachments.map((attachment) => {
          const attached = byId.get(attachment.id) ?? false
          return (
            <li key={attachment.id} className={cn(!attached && "opacity-60")}>
              <SourceAttachmentMedia
                attachment={attachment}
                attached={attached}
                retry={retries[attachment.id] ?? 0}
                onRetry={() => retry(attachment.id)}
                onToggle={() => onChange((s) => toggleAttachment(s, attachment.id))}
              />
            </li>
          )
        })}
      </ul>
      <p className="text-xs text-muted-foreground">
        حذف پیوست فقط از پیام در حال ارسال کنار گذاشته می‌شود؛ پیام اصلی و فایل آن دست‌نخورده می‌مانند.
      </p>
    </div>
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

// ---------------------------------------------------------------------------
// WordPress
// ---------------------------------------------------------------------------

function WordPressArticleEditor({
  state,
  options,
  onChange,
  errors,
}: {
  state: PublishDraftState
  options: PublishingOptions
  onChange: ApplyChange
  errors?: PublishErrors
}) {
  const draft = state.wordpress
  return (
    <div className="flex flex-col gap-6">
      <h3 className="text-base font-semibold">ویرایش مقاله</h3>

      <FeaturedImageSelector state={state} onChange={onChange} error={errors?.featured} />

      <Field label="عنوان مقاله" htmlFor="publish-title" required error={errors?.title}>
        <Input
          id="publish-title"
          value={draft.title}
          dir="auto"
          required
          placeholder="عنوان مقاله"
          aria-invalid={errors?.title ? true : undefined}
          onChange={(e: React.ChangeEvent<HTMLInputElement>) =>
            onChange((s) => setWordPressTitle(s, e.target.value))
          }
        />
      </Field>

      <Field
        label="نامک (Slug)"
        htmlFor="publish-slug"
        hint="خالی بگذارید تا وردپرس خودش بسازد."
      >
        <Input
          id="publish-slug"
          value={draft.slug}
          dir="ltr"
          placeholder="my-article-slug"
          onChange={(e: React.ChangeEvent<HTMLInputElement>) =>
            onChange((s) => setSlug(s, e.target.value))
          }
        />
      </Field>

      <Field
        label="محتوای مقاله"
        htmlFor="publish-content"
        required
        error={errors?.content}
        hint="هر لینکِ این فهرست در متن مقاله به همان نشانی پیوند می‌خورد."
      >
        <Textarea
          id="publish-content"
          value={draft.content}
          dir="auto"
          rows={12}
          placeholder="محتوای مقاله را بنویسید…"
          aria-invalid={errors?.content ? true : undefined}
          onChange={(e: React.ChangeEvent<HTMLTextAreaElement>) =>
            onChange((s) => setWordPressContent(s, e.target.value))
          }
        />
      </Field>

      <Field
        label="لینک‌ها"
        error={errors?.links}
        hint="همان فهرست لینک‌های پیام است: لینکی که اینجا اضافه کنید به انتهای مقاله هم می‌رود."
      >
        <LinkList
          links={state.links}
          onAdd={(link) => onChange((s) => addLink(s, link))}
          onEdit={(id, patch) => onChange((s) => editLink(s, id, patch))}
          onDelete={(id) => onChange((s) => deleteLink(s, id))}
        />
      </Field>

      <Field
        label="دسته‌بندی‌ها"
        htmlFor="publish-category-search"
        hint="دسته‌بندی‌های همین سایت وردپرس."
      >
        <SearchableCheckList
          id="publish-category-search"
          items={options.categories}
          selected={draft.categoryIds}
          onToggle={(id) =>
            onChange((s) =>
              setCategoryIds(
                s,
                s.wordpress.categoryIds.includes(Number(id))
                  ? s.wordpress.categoryIds.filter((v) => v !== Number(id))
                  : [...s.wordpress.categoryIds, Number(id)],
              ),
            )
          }
          searchPlaceholder="جستجو در دسته‌بندی‌ها…"
          emptyText="دسته‌بندی‌ای در این سایت یافت نشد."
          noMatchText="دسته‌بندی‌ای با این جستجو یافت نشد."
          labelOf={(term) => term.name}
        />
      </Field>

      <Field label="برچسب‌ها" htmlFor="publish-tag-search" hint="برچسب‌های همین سایت وردپرس.">
        <SearchableCheckList
          id="publish-tag-search"
          items={options.tags}
          selected={draft.tagIds}
          onToggle={(id) =>
            onChange((s) =>
              setTagIds(
                s,
                s.wordpress.tagIds.includes(Number(id))
                  ? s.wordpress.tagIds.filter((v) => v !== Number(id))
                  : [...s.wordpress.tagIds, Number(id)],
              ),
            )
          }
          searchPlaceholder="جستجو در برچسب‌ها…"
          emptyText="برچسبی در این سایت یافت نشد."
          noMatchText="برچسبی با این جستجو یافت نشد."
          labelOf={(term) => term.name}
        />
      </Field>
    </div>
  )
}

/**
 * FeaturedImageSelector picks the article's thumbnail from the source images.
 *
 * The first one is chosen when the dialog opens, and the user can pick another
 * or drop it entirely. What is stored here is the *source* attachment: the
 * WordPress media id it becomes is resolved by the backend at publish time, so
 * an id belonging to one site's library is never sent to another.
 */
function FeaturedImageSelector({
  state,
  onChange,
  error,
}: {
  state: PublishDraftState
  onChange: ApplyChange
  error?: string | undefined
}) {
  const images = state.source.attachments.filter((a) => a.kind === "image")
  const chosen = state.wordpress.featuredMediaId

  return (
    <Field
      label="تصویر شاخص"
      hint="از تصاویر همین پیام انتخاب می‌شود."
      error={error}
    >
      {images.length === 0 ? (
        <div className="flex items-center gap-2 rounded-lg border border-dashed px-3 py-6 text-sm text-muted-foreground">
          <ImageIcon className="size-4" aria-hidden="true" />
          این پیام تصویری ندارد؛ مقاله بدون تصویر شاخص منتشر می‌شود.
        </div>
      ) : (
        <div className="flex flex-wrap gap-3">
          {images.map((image) => {
            const active = chosen === image.id
            return (
              <div key={image.id} className="flex w-32 flex-col gap-2">
                <button
                  type="button"
                  aria-pressed={active}
                  aria-label={active ? "تصویر شاخص انتخاب‌شده" : "انتخاب به‌عنوان تصویر شاخص"}
                  className={cn(
                    "overflow-hidden rounded-lg border-2 outline-none focus-visible:ring-3 focus-visible:ring-ring/50",
                    active ? "border-primary" : "border-transparent hover:border-border",
                  )}
                  onClick={() => onChange((s) => setFeaturedMediaId(s, active ? null : image.id))}
                >
                  <SourceImage attachment={image} />
                </button>
                <div className="flex items-center justify-between gap-1">
                  <span className="truncate text-xs text-muted-foreground">
                    {formatSize(image.size) ?? mediaTitle(toMessageMedia(image))}
                  </span>
                  {active && (
                    <button
                      type="button"
                      className="shrink-0 text-xs text-destructive hover:underline"
                      onClick={() => onChange((s) => setFeaturedMediaId(s, null))}
                    >
                      حذف
                    </button>
                  )}
                </div>
              </div>
            )
          })}
        </div>
      )}
      {chosen === null && images.length > 0 && (
        <p className="text-xs text-muted-foreground">تصویر شاخصی انتخاب نشده است.</p>
      )}
    </Field>
  )
}

// SourceImage is the picker's thumbnail. The bytes come through the panel's own
// authenticated media route — never from a URL the source channel published —
// and the block stands in for the picture until they are in hand.
function SourceImage({ attachment }: { attachment: SourceAttachment }) {
  const [status, setStatus] = React.useState<"loading" | "ready" | "error">("loading")
  const [url, setUrl] = React.useState("")

  React.useEffect(() => {
    if (!attachment.url) {
      setStatus("error")
      return
    }
    const controller = new AbortController()
    let live = true
    let objectUrl = ""
    fetchMedia(attachment.url, controller.signal)
      .then((value) => {
        if (!live) {
          if (value) releaseMedia(value)
          return
        }
        if (!value) {
          setStatus("error")
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
    }
  }, [attachment.url])

  if (status === "ready" && url) {
    return (
      <img
        src={url}
        alt={attachment.filename ?? attachment.label ?? "تصویر شاخص"}
        className="h-20 w-full object-cover"
      />
    )
  }
  return (
    <span className="flex h-20 w-full items-center justify-center bg-muted text-muted-foreground">
      <ImageIcon className="size-5" aria-hidden="true" />
      <span className="sr-only">{attachment.filename ?? attachment.label ?? "تصویر"}</span>
    </span>
  )
}
