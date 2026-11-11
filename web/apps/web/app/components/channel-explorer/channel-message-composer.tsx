import * as React from "react"
import {
  CheckIcon,
  FileIcon,
  ImageIcon,
  MicIcon,
  Music2Icon,
  PaperclipIcon,
  PencilIcon,
  SendIcon,
  VideoIcon,
  XIcon,
} from "lucide-react"

import { Sheet, SheetContent, SheetHeader, SheetTitle } from "@workspace/ui/components/sheet"
import type { ChannelMessage, MessageMedia } from "@/lib/types"

export type ChannelMessageMediaKind = "photo" | "video" | "audio" | "voice" | "document"
export type EditMediaAction = "keep" | "replace" | "remove"
export type EditComposerDraft = {
  text: string
  mediaAction: EditMediaAction
  file: File | null
  mediaKind: ChannelMessageMediaKind | null
}
export type ComposerMode = { type: "send" } | { type: "edit"; messageId: number; originalMessage: ChannelMessage }

type ComposerKey = { key: string; shiftKey: boolean; isComposing: boolean }

export function shouldSubmitOnEnter(event: ComposerKey): boolean {
  return event.key === "Enter" && !event.shiftKey && !event.isComposing
}

export function classifyMediaSuggestion(mimeType: string): ChannelMessageMediaKind | null {
  const mime = mimeType.toLowerCase()
  if (mime.startsWith("image/")) return "photo"
  if (mime.startsWith("video/")) return "video"
  if (mime.startsWith("audio/")) return "audio"
  if (mime) return "document"
  return null
}

export function attachmentAccept(kind: ChannelMessageMediaKind): string | undefined {
  switch (kind) {
    case "photo": return "image/*"
    case "video": return "video/*"
    case "audio": return "audio/*"
    case "voice": return ".ogg,.oga,.opus,audio/ogg,audio/opus"
    case "document": return undefined
  }
}

function formatFileSize(size: number): string {
  if (size < 1024) return `${size} بایت`
  if (size < 1024 * 1024) return `${(size / 1024).toFixed(1)} کیلوبایت`
  return `${(size / (1024 * 1024)).toFixed(1)} مگابایت`
}

function mediaLabel(media: MessageMedia): string {
  if (media.type === "photo") return "عکس"
  if (media.type === "video" || media.type === "gif") return "ویدیو"
  if (media.type === "voice") return "پیام صوتی"
  if (media.type === "audio") return "موسیقی / صوت"
  return media.filename || media.label || "فایل"
}

function MediaIcon({ kind, className = "size-5" }: { kind: string; className?: string }) {
  if (kind === "photo") return <ImageIcon className={className} aria-hidden="true" />
  if (kind === "video" || kind === "gif") return <VideoIcon className={className} aria-hidden="true" />
  if (kind === "audio") return <Music2Icon className={className} aria-hidden="true" />
  if (kind === "voice") return <MicIcon className={className} aria-hidden="true" />
  return <FileIcon className={className} aria-hidden="true" />
}

const choices: { kind: ChannelMessageMediaKind; label: string; Icon: typeof ImageIcon }[] = [
  { kind: "photo", label: "عکس", Icon: ImageIcon },
  { kind: "video", label: "ویدیو", Icon: VideoIcon },
  { kind: "audio", label: "موسیقی / صوت", Icon: Music2Icon },
  { kind: "voice", label: "پیام صوتی", Icon: MicIcon },
  { kind: "document", label: "فایل", Icon: FileIcon },
]

export function ChannelMessageComposer({
  writable,
  submitting,
  error,
  onSubmit,
  onAccepted,
  recoveredDraft,
  mode = { type: "send" },
  onSaveEdit,
  onCancelEdit,
  editBusy = false,
  editSaveDisabled = false,
}: {
  writable: boolean
  submitting: boolean
  error: string | null
  onSubmit(input: { text: string; file: File | null; mediaKind: ChannelMessageMediaKind | null }): void | Promise<void>
  onAccepted?: () => void
  recoveredDraft?: { key: string; text: string; file: File | null; mediaKind: ChannelMessageMediaKind | null } | null
  mode?: ComposerMode
  onSaveEdit?(draft: EditComposerDraft): boolean | Promise<boolean>
  onCancelEdit?: () => void
  editBusy?: boolean
  editSaveDisabled?: boolean
}) {
  const [sendDraft, setSendDraft] = React.useState({ text: "", file: null as File | null, mediaKind: null as ChannelMessageMediaKind | null })
  const [editDrafts, setEditDrafts] = React.useState<Record<number, EditComposerDraft>>({})
  const [validation, setValidation] = React.useState("")
  const [sheetOpen, setSheetOpen] = React.useState(false)
  const [restoredKey, setRestoredKey] = React.useState<string | null>(null)
  const textareaRef = React.useRef<HTMLTextAreaElement>(null)
  const inputRef = React.useRef<HTMLInputElement>(null)
  const requestedKind = React.useRef<ChannelMessageMediaKind | null>(null)
  const previousEditID = React.useRef<number | null>(null)

  const activeEditDraft = mode.type === "edit"
    ? editDrafts[mode.messageId] ?? {
      text: mode.originalMessage.text ?? "",
      mediaAction: "keep" as const,
      file: null,
      mediaKind: null,
    }
    : null
  const text = mode.type === "edit" ? activeEditDraft!.text : sendDraft.text
  const file = mode.type === "edit"
    ? activeEditDraft!.mediaAction === "replace" ? activeEditDraft!.file : null
    : sendDraft.file
  const mediaKind = mode.type === "edit"
    ? activeEditDraft!.mediaAction === "replace" ? activeEditDraft!.mediaKind : null
    : sendDraft.mediaKind
  const previewUrl = React.useMemo(
    () => file && file.type.startsWith("image/") ? URL.createObjectURL(file) : "",
    [file]
  )

  React.useEffect(() => () => { if (previewUrl) URL.revokeObjectURL(previewUrl) }, [previewUrl])

  React.useEffect(() => {
    if (!recoveredDraft || recoveredDraft.key === restoredKey) return
    setRestoredKey(recoveredDraft.key)
    setSendDraft({ text: recoveredDraft.text, file: recoveredDraft.file, mediaKind: recoveredDraft.mediaKind })
    setValidation("")
  }, [recoveredDraft, restoredKey])

  const resizeTextarea = React.useCallback(() => {
    const textarea = textareaRef.current
    if (!textarea) return
    textarea.style.height = "auto"
    textarea.style.height = `${Math.min(textarea.scrollHeight, 160)}px`
  }, [])

  React.useEffect(() => {
    if (mode.type !== "edit") {
      previousEditID.current = null
      return
    }
    if (previousEditID.current === mode.messageId) return
    previousEditID.current = mode.messageId
    textareaRef.current?.focus()
    requestAnimationFrame(resizeTextarea)
  }, [mode.type, mode.type === "edit" ? mode.messageId : null, resizeTextarea])

  function updateEditDraft(next: EditComposerDraft) {
    if (mode.type !== "edit") return
    setEditDrafts((current) => ({ ...current, [mode.messageId]: next }))
  }

  const pick = (kind: ChannelMessageMediaKind) => {
    requestedKind.current = kind
    if (inputRef.current) {
      inputRef.current.accept = attachmentAccept(kind) ?? ""
      inputRef.current.click()
    }
    setSheetOpen(false)
  }

  const submit = (event: React.FormEvent<HTMLFormElement>) => {
    event.preventDefault()
    if (!writable || (mode.type === "send" ? submitting : editBusy || editSaveDisabled)) return

    if (mode.type === "edit") {
      if (activeEditDraft!.mediaAction === "replace" && (!file || !mediaKind)) {
        setValidation("برای جایگزینی رسانه، فایل و نوع آن را انتخاب کنید.")
        return
      }
      setValidation("")
      const request = onSaveEdit?.(activeEditDraft!)
      if (request !== undefined && typeof request !== "boolean") {
        void Promise.resolve(request).then((saved) => {
          if (saved) setEditDrafts((current) => {
            const next = { ...current }
            delete next[mode.messageId]
            return next
          })
        })
      } else if (request === true) {
        setEditDrafts((current) => {
          const next = { ...current }
          delete next[mode.messageId]
          return next
        })
      }
      return
    }

    if (!text.trim() && !file) {
      setValidation("متن پیام یا یک فایل را وارد کنید.")
      return
    }
    if (sendDraft.file && !sendDraft.mediaKind) {
      setValidation("نوع فایل پیوست را انتخاب کنید.")
      return
    }
    setValidation("")
    const accepted = onSubmit({ text, file: sendDraft.file, mediaKind: sendDraft.file ? sendDraft.mediaKind : null })
    if (accepted && typeof accepted.then === "function") {
      void accepted.then(() => {
        setSendDraft({ text: "", file: null, mediaKind: null })
        setValidation("")
        if (textareaRef.current) textareaRef.current.style.height = "auto"
        onAccepted?.()
      }).catch(() => {
        // A rejected acceptance leaves the entire Send draft intact.
      })
    }
  }

  const editPreview = mode.type === "edit"
    ? mode.originalMessage.text?.trim() || (mode.originalMessage.media ? mediaLabel(mode.originalMessage.media) : "پیام")
    : ""
  const existingMedia = mode.type === "edit" ? mode.originalMessage.media : undefined

  return (
    <form data-slot="channel-message-composer" dir="rtl" onSubmit={submit} className="w-full bg-background border-t md:bg-background/90 backdrop-blur md:border border-border md:rounded-xl overflow-hidden">
      <input
        ref={inputRef}
        type="file"
        aria-label="انتخاب فایل پیوست"
        className="sr-only"
        disabled={!writable || submitting || editBusy}
        onChange={(event) => {
          const selected = event.currentTarget.files?.[0] ?? null
          const kind = requestedKind.current
          if (selected && kind) {
            const validVoice = kind !== "voice" || /\.(ogg|oga|opus)$/i.test(selected.name) || ["audio/ogg", "audio/opus"].includes(selected.type)
            if (!validVoice) {
              setValidation("برای پیام صوتی، فایل سازگار Ogg/Opus انتخاب کنید.")
            } else {
              if (mode.type === "edit") {
                updateEditDraft({ ...activeEditDraft!, mediaAction: "replace", file: selected, mediaKind: kind })
              } else {
                setSendDraft((current) => ({ ...current, file: selected, mediaKind: kind }))
              }
              setValidation("")
            }
          }
          requestedKind.current = null
          event.currentTarget.value = ""
        }}
      />

      {mode.type === "edit" && (
        <div data-slot="edit-context" className="mb-2 flex min-w-0 items-center gap-2 border-s-2 border-primary/50 ps-2 text-xs">
          <PencilIcon className="size-4 shrink-0 text-primary" aria-hidden="true" />
          <span className="shrink-0 font-medium">ویرایش پیام</span>
          <span className="min-w-0 flex-1 truncate text-muted-foreground">{editPreview}</span>
          <button type="button" aria-label="لغو ویرایش" title="لغو ویرایش" disabled={editBusy} onClick={onCancelEdit} className="flex size-7 shrink-0 items-center justify-center rounded-full hover:bg-muted disabled:opacity-50">
            <XIcon className="size-4" aria-hidden="true" />
          </button>
        </div>
      )}

      <div className="p-2">
        {(file && (mode.type === "send" || activeEditDraft!.mediaAction === "replace")) && (
          <div data-slot="attachment-preview" className="mb-2 flex min-w-0 items-center gap-2 rounded-xl bg-muted px-2.5 py-2">
            {previewUrl ? <img src={previewUrl} alt="پیش‌نمایش فایل" className="size-11 shrink-0 rounded-lg object-cover" /> : <MediaIcon kind={mediaKind ?? "document"} className="size-5 shrink-0 text-muted-foreground" />}
            <span className="min-w-0 flex-1 truncate text-xs">
              <span className="block truncate font-medium">{file.name}</span>
              <span className="text-muted-foreground">{mediaKind === "voice" ? "پیام صوتی · " : ""}{formatFileSize(file.size)}</span>
            </span>
            <button type="button" aria-label="حذف فایل پیوست" onClick={() => {
              if (mode.type === "edit") updateEditDraft({ ...activeEditDraft!, mediaAction: existingMedia ? "keep" : "remove", file: null, mediaKind: null })
              else setSendDraft((current) => ({ ...current, file: null, mediaKind: null }))
            }} className="flex size-8 shrink-0 items-center justify-center rounded-full hover:bg-background">
              <XIcon className="size-4" aria-hidden="true" />
            </button>
          </div>
        )}

        {mode.type === "edit" && existingMedia && activeEditDraft!.mediaAction === "keep" && (
          <div data-slot="existing-media-preview" className="mb-2 flex min-w-0 items-center gap-2 rounded-xl bg-muted px-2.5 py-2 text-xs">
            <MediaIcon kind={existingMedia.type} className="size-5 shrink-0 text-muted-foreground" />
            <span className="min-w-0 flex-1 truncate">{existingMedia.filename || mediaLabel(existingMedia)}</span>
            {existingMedia.size !== undefined && <span className="shrink-0 text-muted-foreground">{formatFileSize(existingMedia.size)}</span>}
            <button type="button" aria-label="حذف پیوست از ویرایش" title="حذف پیوست" onClick={() => updateEditDraft({ ...activeEditDraft!, mediaAction: "remove", file: null, mediaKind: null })} className="flex size-8 shrink-0 items-center justify-center rounded-full hover:bg-background">
              <XIcon className="size-4" aria-hidden="true" />
            </button>
          </div>
        )}
        {mode.type === "edit" && existingMedia && activeEditDraft!.mediaAction === "remove" && (
          <div className="mb-2 flex items-center gap-2 rounded-xl bg-muted px-2.5 py-2 text-xs text-muted-foreground">
            <span className="flex-1">پیوست با ذخیره حذف می‌شود</span>
            <button type="button" onClick={() => updateEditDraft({ ...activeEditDraft!, mediaAction: "keep", file: null, mediaKind: null })} className="underline underline-offset-2">بازگردانی</button>
          </div>
        )}

        <textarea
          ref={textareaRef}
          rows={1}
          value={text}
          disabled={!writable || submitting || editBusy}
          placeholder="پیام خود را بنویسید..."
          aria-label={mode.type === "edit" ? "متن پیام برای ویرایش" : "متن پیام"}
          onChange={(event) => {
            const value = event.currentTarget.value
            if (mode.type === "edit") updateEditDraft({ ...activeEditDraft!, text: value })
            else setSendDraft((current) => ({ ...current, text: value }))
            setValidation("")
            resizeTextarea()
          }}
          onKeyDown={(event) => {
            if (event.key === "Escape" && mode.type === "edit" && !sheetOpen && !editBusy) {
              event.preventDefault()
              onCancelEdit?.()
              return
            }
            const native = event.nativeEvent
            if (shouldSubmitOnEnter({ key: event.key, shiftKey: event.shiftKey, isComposing: native.isComposing || native.keyCode === 229 })) {
              event.preventDefault()
              event.currentTarget.form?.requestSubmit()
            }
          }}
          className="max-h-40 min-h-10 w-full resize-none border-0 bg-transparent px-2 py-2 text-sm outline-none placeholder:text-muted-foreground focus-visible:ring-0"
        />

        <div className="flex items-center justify-between gap-2 pt-1">
          <button type="button" aria-label="افزودن پیوست" title="افزودن پیوست" disabled={!writable || submitting || editBusy} onClick={() => setSheetOpen(true)} className="flex size-10 items-center justify-center rounded-full text-muted-foreground hover:bg-muted disabled:opacity-50">
            <PaperclipIcon className="size-5" aria-hidden="true" />
          </button>
          {mode.type === "edit" ? (
            <button type="submit" disabled={!writable || editBusy || editSaveDisabled} aria-label="ذخیره تغییرات" title="ذخیره تغییرات" className="flex min-h-10 items-center gap-2 rounded-full bg-primary px-4 text-sm text-primary-foreground disabled:cursor-not-allowed disabled:opacity-50">
              {editBusy ? <span className="text-xs" aria-hidden="true">…</span> : <CheckIcon className="size-4" aria-hidden="true" />}
              ذخیره تغییرات
            </button>
          ) : (
            <button type="submit" disabled={!writable || submitting || (!text.trim() && !file)} aria-label="ارسال پیام" title="ارسال پیام" className="flex size-10 items-center justify-center rounded-full bg-primary text-primary-foreground disabled:cursor-not-allowed disabled:opacity-50">
              {submitting ? <span className="text-xs" aria-hidden="true">…</span> : <SendIcon className="size-4" aria-hidden="true" />}
            </button>
          )}
        </div>
      </div>

      {(validation || error) && <p role="alert" className="px-2 pt-2 text-sm text-destructive">{validation || error}</p>}

      <Sheet open={sheetOpen} onOpenChange={setSheetOpen}>
        <SheetContent side="bottom" dir="rtl" aria-describedby={undefined} className="gap-2 px-4 pb-[calc(1rem+env(safe-area-inset-bottom))] pt-1">
          <div className="mx-auto grid w-full grid-cols-5 gap-3 pb-2 pt-10">
            {choices.map(({ kind, label, Icon }) => (
              <button key={kind} type="button" onClick={() => pick(kind)} className="flex min-h-24 flex-col items-center justify-center gap-2 rounded-2xl bg-muted/60 text-sm hover:bg-muted focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring">
                <span className="flex size-12 items-center justify-center text-foreground"><Icon className="size-5" aria-hidden="true" /></span>
                <span className="text-xs md:text-base">{label}</span>
              </button>
            ))}
          </div>
        </SheetContent>
      </Sheet>
    </form>
  )
}
