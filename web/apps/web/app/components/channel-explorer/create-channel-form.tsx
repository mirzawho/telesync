import { CameraIcon, CheckIcon, Globe2Icon, LockKeyholeIcon, XIcon } from "lucide-react"
import * as React from "react"

import { AccountCombobox } from "@/components/accounts/account-combobox"
import type { TelegramAccount } from "@/lib/types"
import { Button } from "@workspace/ui/components/button"
import { Field, FieldControl, FieldError, FieldLabel } from "@workspace/ui/components/field"
import { DialogFooter } from "@workspace/ui/components/dialog"
import { Textarea } from "@workspace/ui/components/textarea"

export type UsernameAvailability = { state: "claimed" | "unclaimed" | "unknown"; message?: string }
export interface CreateChannelDraft {
  accountID: string
  title: string
  description: string
  visibility: "private" | "public"
  username?: string
  image?: File
}
export interface CreateChannelFormProps {
  accounts: TelegramAccount[]
  initialAccountID?: string
  pending?: boolean
  error?: string | null
  onSubmit: (draft: CreateChannelDraft) => void
  onCheckUsername: (accountID: string, username: string, signal: AbortSignal) => Promise<UsernameAvailability>
  onCancel?: () => void
  onDirtyChange?: (dirty: boolean) => void
  availabilityDebounceMs?: number
}

export const MAX_CHANNEL_IMAGE_BYTES = 5 * 1024 * 1024
const usernamePattern = /^[a-zA-Z][a-zA-Z0-9_]{4,31}$/
export function validatePublicUsername(value: string): string | null {
  if (!usernamePattern.test(value.replace(/^@/, ""))) return "شناسه باید ۵ تا ۳۲ نویسه باشد و با حرف انگلیسی آغاز شود."
  return null
}

export function validateChannelImage(file: File): string | null {
  if (!/^image\/(?:jpeg|png|webp|gif)$/.test(file.type)) return "فقط تصویر JPEG، PNG، WebP یا GIF پذیرفته می‌شود."
  if (file.size <= 0 || file.size > MAX_CHANNEL_IMAGE_BYTES) return "حجم تصویر باید حداکثر ۵ مگابایت باشد."
  return null
}

export function createChannelSubmitIsValid(draft: Pick<CreateChannelDraft, "accountID" | "title" | "visibility" | "username">): boolean {
  return Boolean(draft.accountID && draft.title.trim() && (draft.visibility === "private" || (draft.username?.trim() && !validatePublicUsername(draft.username))))
}

export function CreateChannelForm({
  accounts, initialAccountID, pending = false, error = null, onSubmit, onCheckUsername,
  onCancel, onDirtyChange, availabilityDebounceMs = 400,
}: CreateChannelFormProps) {
  const [accountID, setAccountID] = React.useState(initialAccountID)
  const [title, setTitle] = React.useState("")
  const [description, setDescription] = React.useState("")
  const [isPublic, setIsPublic] = React.useState(false)
  const [username, setUsername] = React.useState("")
  const [image, setImage] = React.useState<File>()
  const [preview, setPreview] = React.useState<string>()
  const [previewFailed, setPreviewFailed] = React.useState(false)
  const [imageError, setImageError] = React.useState<string | null>(null)
  const [availability, setAvailability] = React.useState<UsernameAvailability | null>(null)
  const [availabilityFor, setAvailabilityFor] = React.useState("")
  const [checking, setChecking] = React.useState(false)
  const [touched, setTouched] = React.useState(false)
  const fileInput = React.useRef<HTMLInputElement>(null)
  React.useEffect(() => {
    if (!image) { setPreview(undefined); setPreviewFailed(false); return }
    const url = URL.createObjectURL(image)
    setPreview(url)
    return () => URL.revokeObjectURL(url)
  }, [image])

  const titleError = touched && !title.trim() ? "نام کانال الزامی است." : null
  const usernameError = isPublic && username ? validatePublicUsername(username) : null

  React.useEffect(() => {
    setAvailability(null)
    setAvailabilityFor("")
    if (!isPublic || !accountID || !username || validatePublicUsername(username)) {
      setChecking(false)
      return
    }
    const controller = new AbortController()
    const checkKey = `${accountID}:${username}`
    setChecking(true)
    const timer = window.setTimeout(() => {
      onCheckUsername(accountID, username.replace(/^@/, ""), controller.signal)
        .then((result) => { if (!controller.signal.aborted) { setAvailability(result); setAvailabilityFor(checkKey) } })
        .catch(() => { if (!controller.signal.aborted) { setAvailability({ state: "unknown" }); setAvailabilityFor(checkKey) } })
        .finally(() => { if (!controller.signal.aborted) setChecking(false) })
    }, availabilityDebounceMs)
    return () => { window.clearTimeout(timer); controller.abort() }
  }, [accountID, availabilityDebounceMs, isPublic, onCheckUsername, username])

  const submit = (event: React.FormEvent<HTMLFormElement>) => {
    event.preventDefault()
    setTouched(true)
    const visibility = isPublic ? "public" : "private"
    if (!createChannelSubmitIsValid({ accountID: accountID ?? "", title, visibility, username })) return
    onSubmit({ accountID: accountID!, title: title.trim(), description, visibility,
      ...(isPublic ? { username: username.replace(/^@/, "") } : {}), ...(image ? { image } : {}) })
  }

  const changeAccount = (next: string | undefined) => {
    setAccountID(next)
    setAvailability(null)
    setAvailabilityFor("")
    setChecking(false)
  }
  const changeVisibility = (next: boolean) => {
    setIsPublic(next)
    if (!next) {
      setUsername("")
      setAvailability(null)
      setAvailabilityFor("")
      setChecking(false)
    }
  }
  const availabilityCurrent = availabilityFor === `${accountID ?? ""}:${username}` ? availability : null
  React.useEffect(() => {
    onDirtyChange?.(Boolean(title || description || username || image || accountID !== initialAccountID))
  }, [accountID, description, image, initialAccountID, onDirtyChange, title, username])

  const chooseImage = async (event: React.ChangeEvent<HTMLInputElement>) => {
    const file = event.target.files?.[0]
    event.target.value = ""
    if (!file) return
    const invalid = validateChannelImage(file)
    if (invalid) { setImageError(invalid); return }
    setPreviewFailed(false)
    try {
      const bitmap = await createImageBitmap(file)
      bitmap.close()
      setImageError(null)
      setImage(file)
    } catch {
      setImageError("محتوای فایل تصویر معتبر نیست.")
    }
  }

  return <form dir="rtl" noValidate onSubmit={submit} className="flex min-h-0 flex-1 flex-col">
    <div className="min-h-0 flex-1 space-y-4 overflow-y-auto px-5 py-4 sm:px-6">
      <div className="flex flex-col items-center gap-2 pb-1">
        <input ref={fileInput} type="file" accept="image/jpeg,image/png,image/webp,image/gif" disabled={pending} className="sr-only" aria-label="انتخاب تصویر کانال" onChange={chooseImage} />
        <div className="relative">
          <button type="button" onClick={() => fileInput.current?.click()} disabled={pending} aria-label={image ? "جایگزینی تصویر کانال" : "افزودن تصویر کانال"} className="group relative grid size-28 overflow-hidden rounded-full border border-dashed border-border bg-muted/60 text-muted-foreground shadow-sm outline-none transition hover:border-ring hover:bg-muted focus-visible:ring-2 focus-visible:ring-ring focus-visible:ring-offset-2 disabled:cursor-not-allowed">
            {image && preview && !previewFailed ? <img src={preview} alt="پیش‌نمایش تصویر کانال" onError={() => setPreviewFailed(true)} className="absolute inset-0 size-full rounded-full object-cover" /> : <CameraIcon aria-hidden="true" className="size-8" />}
            {image && <span className="absolute inset-0 grid place-items-center bg-foreground/35 text-white opacity-0 transition group-hover:opacity-100 group-focus-visible:opacity-100"><CameraIcon className="size-6" /></span>}
          </button>
          {image && <button type="button" onClick={() => { setImage(undefined); setImageError(null); setPreviewFailed(false) }} disabled={pending} aria-label="حذف تصویر کانال" className="absolute -end-1 -top-1 grid size-7 place-items-center rounded-full border bg-background text-foreground shadow-sm outline-none hover:bg-muted focus-visible:ring-2 focus-visible:ring-ring"><XIcon className="size-4" /></button>}
        </div>
        <span className="text-xs text-muted-foreground">افزودن تصویر کانال</span>
        {imageError && <p role="alert" className="max-w-sm text-center text-xs text-destructive">{imageError}</p>}
      </div>

      <Field data-invalid={!accountID ? "" : undefined}>
        <FieldLabel htmlFor="create-channel-account" required>حساب تلگرام</FieldLabel>
        <AccountCombobox id="create-channel-account" accounts={accounts} value={accountID} onChange={changeAccount} disabled={pending} />
        {!accountID && <FieldError>حساب تلگرام را انتخاب کنید.</FieldError>}
      </Field>
      <Field data-invalid={titleError ? "" : undefined}>
        <FieldLabel htmlFor="create-channel-title" required>نام کانال</FieldLabel>
        <FieldControl id="create-channel-title" value={title} maxLength={128} onChange={(event) => setTitle(event.target.value)} onBlur={() => setTouched(true)} required disabled={pending} />
        {titleError && <FieldError>{titleError}</FieldError>}
      </Field>
      <Field>
        <FieldLabel htmlFor="create-channel-description">توضیحات</FieldLabel>
        <Textarea id="create-channel-description" value={description} maxLength={255} onChange={(event) => setDescription(event.target.value)} className="min-h-24" disabled={pending} />
      </Field>

      <fieldset className="space-y-2">
        <legend className="text-sm font-medium">نوع کانال</legend>
        <div role="radiogroup" aria-label="نوع کانال" className="grid grid-cols-2 gap-3">
          {([false, true] as const).map((publicOption) => {
            const selected = isPublic === publicOption
            const id = publicOption ? "create-channel-public" : "create-channel-private"
            return <label key={id} htmlFor={id} className={`relative flex min-h-[72px] cursor-pointer items-center gap-3 rounded-xl border p-3 text-right outline-none transition hover:bg-muted/60 focus-within:ring-2 focus-within:ring-ring ${selected ? "border-foreground/50 bg-muted/70" : "border-border bg-background"}`}>
              <input id={id} type="radio" name="channel-visibility" value={publicOption ? "public" : "private"} checked={selected} onChange={() => changeVisibility(publicOption)} disabled={pending} className="peer sr-only" />
              {publicOption ? <Globe2Icon className="size-5 shrink-0 text-muted-foreground" aria-hidden="true" /> : <LockKeyholeIcon className="size-5 shrink-0 text-muted-foreground" aria-hidden="true" />}
              <span className="min-w-0 flex-1"><span className="block text-sm font-medium">{publicOption ? "عمومی" : "خصوصی"}</span><span className="mt-0.5 block text-xs text-muted-foreground">{publicOption ? "قابل دسترسی با شناسه عمومی" : "دسترسی از طریق دعوت یا عضویت"}</span></span>
              <span aria-hidden="true" className={`grid size-4 shrink-0 place-items-center rounded-full border ${selected ? "border-foreground bg-foreground text-background" : "border-muted-foreground/50"}`}>{selected && <CheckIcon className="size-3" />}</span>
            </label>
          })}
        </div>
      </fieldset>

      {isPublic && <Field data-invalid={usernameError ? "" : undefined}>
        <FieldLabel htmlFor="create-channel-username" required>شناسه عمومی کانال</FieldLabel>
        <div className="relative">
          <span aria-hidden="true" className="absolute inset-y-0 start-3 flex items-center text-muted-foreground" dir="ltr">@</span>
          <FieldControl id="create-channel-username" dir="ltr" autoCapitalize="none" autoCorrect="off" value={username} onChange={(event) => setUsername(event.target.value.replace(/^@/, ""))} placeholder="my_channel" className="ps-8" disabled={pending} />
        </div>
        {usernameError && <FieldError>{usernameError}</FieldError>}
        {!usernameError && checking && <p role="status" className="min-h-5 text-xs text-muted-foreground">در حال بررسی شناسه...</p>}
        {!usernameError && availabilityCurrent?.state === "claimed" && <p role="status" className="min-h-5 text-xs text-destructive">این شناسه قبلاً استفاده شده است.</p>}
        {!usernameError && availabilityCurrent?.state === "unclaimed" && <p role="status" className="min-h-5 text-xs text-muted-foreground">این شناسه پیدا نشد؛ تأیید نهایی هنگام ساخت انجام می‌شود.</p>}
        {!usernameError && availabilityCurrent?.state === "unknown" && <p role="status" className="min-h-5 text-xs text-muted-foreground">وضعیت شناسه مشخص نشد؛ تأیید نهایی هنگام ساخت انجام می‌شود.</p>}
      </Field>}
      {error && <p role="alert" className="rounded-lg bg-destructive/10 px-3 py-2 text-sm text-destructive">{error}</p>}
    </div>
    <DialogFooter className="shrink-0 flex-row-reverse justify-start border-t px-5 py-3 pb-[max(0.75rem,env(safe-area-inset-bottom))] sm:px-6">
      <div className="flex flex-row-reverse gap-2">
        <Button type="submit" disabled={pending || !accountID} className="min-w-28">{pending ? "در حال ساخت…" : "ساخت کانال"}</Button>
        {onCancel && <Button type="button" variant="outline" onClick={onCancel} disabled={pending}>انصراف</Button>}
      </div>
    </DialogFooter>
  </form>
}
