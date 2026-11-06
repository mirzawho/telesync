import * as React from "react"

import { MultiPicker } from "@/components/multi-picker"
import { SecretField } from "@/components/automation/secret-field"
import { OptionCombobox } from "@/components/automation/option-combobox"
import { SubmitButton } from "@/components/submit-button"
import { Button } from "@workspace/ui/components/button"
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@workspace/ui/components/dialog"
import {
  Field,
  FieldControl,
  FieldDescription,
  FieldError,
  FieldLabel,
} from "@workspace/ui/components/field"
import { Label } from "@workspace/ui/components/label"
import { Switch } from "@workspace/ui/components/switch"
import { Textarea } from "@workspace/ui/components/textarea"

import {
  authModeLabels,
  emptyWebhookDraft,
  methodLabels,
  webhookDraftOf,
  type AuthMode,
  type HttpMethod,
  type WebhookDraft,
  type WebhookItem,
} from "@/lib/automation"

// One managed channel, as the source picker names it. The ids are local
// managed_channels.id values — the API's own subscription key — never Telegram
// ids, so the picker offers the stored rows and nothing else.
export interface SourceChannelOption {
  id: number
  label: string
  detail?: string
}

export interface WebhookFieldErrors {
  name?: string
  url?: string
  authHeader?: string
  secret?: string
  sourceChannelIds?: string
  timeoutMs?: string
  maxAttempts?: string
}

// The same bounds the API enforces, read here so a value outside them is
// refused before the request goes out rather than coming back as one.
const MIN_TIMEOUT_MS = 100
const MAX_TIMEOUT_MS = 300_000
const MIN_ATTEMPTS = 1
const MAX_ATTEMPTS = 100

const validTimeout = (value: number) =>
  Number.isInteger(value) && value >= MIN_TIMEOUT_MS && value <= MAX_TIMEOUT_MS
const validAttempts = (value: number) =>
  Number.isInteger(value) && value >= MIN_ATTEMPTS && value <= MAX_ATTEMPTS

// The address check is the API's own save-time rule and nothing more: an
// absolute http(s) address with a host, no credentials and no fragment. Whether
// the host is one this installation may connect to is the transport's answer,
// given on every attempt and never guessable here.
export function urlErrorOf(raw: string): string | null {
  const value = raw.trim()
  if (value === "") return "نشانی مقصد الزامی است."
  let parsed: URL
  try {
    parsed = new URL(value)
  } catch {
    return "نشانی باید کامل باشد و با http:// یا https:// آغاز شود."
  }
  if (parsed.protocol !== "http:" && parsed.protocol !== "https:") {
    return "نشانی باید با http:// یا https:// آغاز شود."
  }
  if (parsed.hostname === "") {
    return "نشانی باید میزبان داشته باشد."
  }
  if (parsed.username !== "" || parsed.password !== "") {
    return "نشانی نباید نام کاربری یا گذرواژه داشته باشد."
  }
  if (parsed.hash !== "") {
    return "نشانی نباید بخش # داشته باشد."
  }
  return null
}

// The headers a configuration may never name: the ones the transport owns, and
// the Standard Webhooks ones this build already signs with. A configuration that
// could pick one of these would overwrite it on the way out.
const reservedHeaders = new Set([
  "host",
  "content-length",
  "content-type",
  "connection",
  "transfer-encoding",
  "upgrade",
  "te",
  "trailer",
  "authorization",
  "proxy-authorization",
  "webhook-id",
  "webhook-timestamp",
  "webhook-signature",
])

// RFC 7230 token characters: what a usable HTTP field name is made of.
const headerNameShape = /^[!#$%&'*+\-.^_`|~0-9A-Za-z]+$/

export function authHeaderErrorOf(raw: string): string | null {
  const name = raw.trim()
  if (name === "") return "نام هدر الزامی است."
  if (!headerNameShape.test(name)) {
    return "نام هدر یک نام معتبر HTTP نیست."
  }
  const lower = name.toLowerCase()
  if (lower.startsWith("webhook-") || reservedHeaders.has(lower)) {
    return "این نام هدر را نمی‌توان برای احراز انتخاب کرد."
  }
  return null
}

export const methodOptions = (["POST", "PUT", "PATCH"] as HttpMethod[]).map((value) => ({
  value,
  label: methodLabels[value],
  detail: value === "POST" ? "پیش‌فرض" : undefined,
}))

export const authOptions = (["hmac", "bearer", "header", "none"] as AuthMode[]).map(
  (value) => ({
    value,
    label: authModeLabels[value],
    detail:
      value === "hmac"
        ? "امضا با رمز مشترک"
        : value === "bearer"
          ? "توکن در هدر Authorization"
          : value === "header"
            ? "نام هدر را خودتان مشخص می‌کنید"
            : "بدون هدر احراز",
  })
)

// The webhook create/edit form. It reports a draft and never submits: the page
// owns the action, so a refusal can reopen this form with what was typed.
export function WebhookForm({
  webhook,
  channels,
  error,
  fieldErrors,
  pending,
  defaultValues,
  onClose,
  onSubmit,
}: {
  webhook: WebhookItem | null
  channels: SourceChannelOption[]
  error?: string | null
  fieldErrors?: WebhookFieldErrors
  pending: boolean
  defaultValues?: Partial<WebhookDraft>
  onClose: () => void
  onSubmit: (draft: WebhookDraft) => void
}) {
  const [draft, setDraft] = React.useState<WebhookDraft>(() => ({
    ...(webhook ? webhookDraftOf(webhook) : emptyWebhookDraft()),
    ...defaultValues,
  }))
  const [touched, setTouched] = React.useState(false)

  const patch = (next: Partial<WebhookDraft>) =>
    setDraft((previous) => ({ ...previous, ...next }))

  // The custom header only exists for one auth mode. Leaving its value in
  // state keeps the operator's typing, but the payload posts it only there.
  const needsHeader = draft.authMode === "header"

  const nameError =
    (touched && draft.name.trim() === "" ? "نام وب‌هوک الزامی است." : null) ??
    fieldErrors?.name
  const urlError =
    (touched ? urlErrorOf(draft.url) : null) ?? fieldErrors?.url
  const authHeaderError =
    (touched && needsHeader ? authHeaderErrorOf(draft.authHeader) : null) ??
    fieldErrors?.authHeader
  const timeoutError =
    (touched && !validTimeout(draft.timeoutMs)
      ? `مهلت باید بین ${MIN_TIMEOUT_MS} تا ${MAX_TIMEOUT_MS} میلی‌ثانیه باشد.`
      : null) ?? fieldErrors?.timeoutMs
  const attemptsError =
    (touched && !validAttempts(draft.maxAttempts)
      ? `تعداد تلاش‌ها باید بین ${MIN_ATTEMPTS} تا ${MAX_ATTEMPTS} باشد.`
      : null) ?? fieldErrors?.maxAttempts

  const complete =
    draft.name.trim() !== "" &&
    urlErrorOf(draft.url) === null &&
    (!needsHeader || authHeaderErrorOf(draft.authHeader) === null) &&
    validTimeout(draft.timeoutMs) &&
    validAttempts(draft.maxAttempts)

  return (
        <form
          className="flex flex-col gap-4"
          noValidate
          onSubmit={(event) => {
            event.preventDefault()
            setTouched(true)
            if (!complete) return
            onSubmit(draft)
          }}
        >
          <Field data-invalid={nameError ? "" : undefined}>
            <FieldLabel htmlFor="webhook-name" required>
              نام
            </FieldLabel>
            <FieldControl
              id="webhook-name"
              name="name"
              value={draft.name}
              onChange={(event) => patch({ name: event.target.value })}
              onBlur={() => setTouched(true)}
              required
            />
            {nameError && <FieldError>{nameError}</FieldError>}
          </Field>

          <Field>
            <FieldLabel htmlFor="webhook-description">توضیحات</FieldLabel>
            <Textarea
              id="webhook-description"
              name="description"
              value={draft.description}
              onChange={(event) => patch({ description: event.target.value })}
              className="min-h-20"
            />
          </Field>

          <MultiPicker
            id="webhook-sources"
            title="کانال‌های مبدأ"
            description="رویدادهای این کانال‌ها برای این وب‌هوک فرستاده می‌شود."
            placeholder="کانال را انتخاب کنید"
            emptyText="کانالی در دسترس نیست."
            items={channels}
            value={draft.sourceChannelIds}
            onValueChange={(sourceChannelIds) => patch({ sourceChannelIds })}
            labelOf={(channel) => channel.label}
          />
          {fieldErrors?.sourceChannelIds && (
            <p role="alert" className="text-xs font-medium text-destructive">
              {fieldErrors.sourceChannelIds}
            </p>
          )}

          <Field data-invalid={urlError ? "" : undefined}>
            <FieldLabel htmlFor="webhook-url" required>
              نشانی مقصد
            </FieldLabel>
            <FieldControl
              id="webhook-url"
              name="url"
              dir="ltr"
              type="url"
              inputMode="url"
              placeholder="https://example.com/hook"
              value={draft.url}
              onChange={(event) => patch({ url: event.target.value })}
              onBlur={() => setTouched(true)}
              required
            />
            <FieldDescription>
              نشانی کامل با میزبان، بدون نام کاربری و بدون بخش{" "}
              <span dir="ltr">#</span>. فقط{" "}
              <span dir="ltr">http://</span> یا <span dir="ltr">https://</span>{" "}
              پذیرفته می‌شود و در استقرارهای عمومی{" "}
              <span dir="ltr">https</span> الزامی است.
            </FieldDescription>
            {urlError && <FieldError>{urlError}</FieldError>}
          </Field>

          <Field>
            <FieldLabel htmlFor="webhook-method">روش درخواست</FieldLabel>
            <OptionCombobox
              id="webhook-method"
              label="روش درخواست"
              options={methodOptions}
              value={draft.method}
              onChange={(method) => patch({ method: method as HttpMethod })}
              placeholder="روش را انتخاب کنید"
            />
          </Field>

          <Field>
            <FieldLabel htmlFor="webhook-auth">شیوه احراز</FieldLabel>
            <OptionCombobox
              id="webhook-auth"
              label="شیوه احراز"
              options={authOptions}
              value={draft.authMode}
              onChange={(authMode) => patch({ authMode: authMode as AuthMode })}
              placeholder="شیوه احراز را انتخاب کنید"
            />
          </Field>

          {needsHeader && (
            <Field data-invalid={authHeaderError ? "" : undefined}>
              <FieldLabel htmlFor="webhook-auth-header" required>
                نام هدر
              </FieldLabel>
              <FieldControl
                id="webhook-auth-header"
                name="auth_header"
                dir="ltr"
                placeholder="X-Api-Key"
                value={draft.authHeader}
                onChange={(event) => patch({ authHeader: event.target.value })}
                onBlur={() => setTouched(true)}
                required
              />
              <FieldDescription>
                مقدار این هدر از رمز وب‌هوک گرفته می‌شود. نام‌هایی مانند{" "}
                <span dir="ltr">Authorization</span> و هدرهای{" "}
                <span dir="ltr">webhook-</span> پذیرفته نمی‌شوند.
              </FieldDescription>
              {authHeaderError && <FieldError>{authHeaderError}</FieldError>}
            </Field>
          )}

          {/* Two numbers the store keeps and the delivery honours: how long one
              attempt may take, and how many attempts one event may cost. Both
              are bounded by the same ranges the API enforces. */}
          <div className="grid gap-4 sm:grid-cols-2">
            <Field data-invalid={timeoutError ? "" : undefined}>
              <FieldLabel htmlFor="webhook-timeout" required>
                مهلت هر تلاش (میلی‌ثانیه)
              </FieldLabel>
              <FieldControl
                id="webhook-timeout"
                name="timeout_ms"
                dir="ltr"
                type="number"
                inputMode="numeric"
                min={100}
                max={300000}
                value={String(draft.timeoutMs)}
                onChange={(event) =>
                  patch({ timeoutMs: Number(event.target.value) })
                }
                onBlur={() => setTouched(true)}
                required
              />
              <FieldDescription>
                بین ۱۰۰ تا ۳۰۰۰۰۰ میلی‌ثانیه.
              </FieldDescription>
              {timeoutError && <FieldError>{timeoutError}</FieldError>}
            </Field>

            <Field data-invalid={attemptsError ? "" : undefined}>
              <FieldLabel htmlFor="webhook-attempts" required>
                بیشینه تلاش‌ها
              </FieldLabel>
              <FieldControl
                id="webhook-attempts"
                name="max_attempts"
                dir="ltr"
                type="number"
                inputMode="numeric"
                min={1}
                max={100}
                value={String(draft.maxAttempts)}
                onChange={(event) =>
                  patch({ maxAttempts: Number(event.target.value) })
                }
                onBlur={() => setTouched(true)}
                required
              />
              <FieldDescription>بین ۱ تا ۱۰۰ تلاش.</FieldDescription>
              {attemptsError && <FieldError>{attemptsError}</FieldError>}
            </Field>
          </div>

          <SecretField
            id="webhook-secret"
            hasSecret={webhook?.hasSecret ?? false}
            value={draft.secret}
            onValueChange={(secret) => patch({ secret })}
            error={fieldErrors?.secret}
            disabled={pending}
          />

          <div className="flex flex-nowrap items-center justify-between gap-4 rounded-lg border px-3 py-2.5">
            <Label htmlFor="webhook-enabled" className="font-semibold">
              وضعیت وب‌هوک
            </Label>
            <div className="flex items-center gap-2">
              <span className="text-sm text-muted-foreground">
                {draft.enabled ? "فعال" : "غیرفعال"}
              </span>
              <Switch
                id="webhook-enabled"
                checked={draft.enabled}
                onCheckedChange={(enabled) => patch({ enabled })}
                disabled={pending}
              />
            </div>
          </div>

          {error && (
            <p
              role="alert"
              className="rounded-lg bg-destructive/10 px-3 py-2 text-sm text-destructive"
            >
              {error}
            </p>
          )}

          <DialogFooter>
            <Button
              type="button"
              variant="outline"
              onClick={onClose}
              disabled={pending}
            >
              انصراف
            </Button>
            <SubmitButton
              pending={pending}
              submittingLabel={webhook ? "در حال ذخیره…" : "در حال ایجاد…"}
            >
              {webhook ? "ذخیره" : "ایجاد"}
            </SubmitButton>
          </DialogFooter>
        </form>
  )
}

// The same form inside a dialog, which is how the list page opens it.
export function WebhookDialog(props: {
  webhook: WebhookItem | null
  channels: SourceChannelOption[]
  error?: string | null
  fieldErrors?: WebhookFieldErrors
  pending: boolean
  defaultValues?: Partial<WebhookDraft>
  onClose: () => void
  onSubmit: (draft: WebhookDraft) => void
}) {
  const { webhook, pending, onClose } = props
  return (
    <Dialog open onOpenChange={(open) => !open && !pending && onClose()}>
      <DialogContent className="max-h-[88dvh] max-w-[560px] overflow-y-auto">
        <DialogHeader>
          <DialogTitle>{webhook ? "ویرایش وب‌هوک" : "وب‌هوک جدید"}</DialogTitle>
          <DialogDescription>
            {webhook
              ? "هر بار ذخیره، همه فیلدها را جایگزین می‌کند. رمز ذخیره‌شده فقط با وارد کردن مقدار جدید تغییر می‌کند."
              : "رویدادهای کانال‌های انتخاب‌شده به نشانی مقصد فرستاده می‌شود."}
          </DialogDescription>
        </DialogHeader>
        <WebhookForm {...props} />
      </DialogContent>
    </Dialog>
  )
}
