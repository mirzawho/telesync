import { LoaderCircleIcon } from "lucide-react"
import * as React from "react"

import { MultiPicker } from "@/components/multi-picker"
import {
  AutomationFormActions,
  AutomationFormLayout,
} from "@/components/automation/form-shell"
import {
  SERVER_REQUIRED,
  SOURCES_REQUIRED,
  SourceServerField,
  channelsForAccount,
  serverKeyFor,
  sourceItems,
  sourceItemsForAccount,
  reachableIds,
  type SourceChannel,
} from "@/components/automation/source-selection"
import {
  AccountCombobox,
  AccountPausedNotice,
} from "@/components/accounts/account-combobox"
import {
  Accordion,
  AccordionContent,
  AccordionItem,
  AccordionTrigger,
} from "@workspace/ui/components/accordion"
import {
  OptionCombobox,
  type ComboOption,
} from "@/components/automation/option-combobox"
import { SecretField } from "@/components/automation/secret-field"
import { LoadingState } from "@/components/states"
import { Button } from "@workspace/ui/components/button"
import {
  Field,
  FieldControl,
  FieldDescription,
  FieldError,
  FieldLabel,
} from "@workspace/ui/components/field"
import { Label } from "@workspace/ui/components/label"
import { Separator } from "@workspace/ui/components/separator"
import { Switch } from "@workspace/ui/components/switch"
import { Textarea } from "@workspace/ui/components/textarea"

import type { TelegramAccount, TelegramServer } from "@/lib/types"
import { PageShell } from "@/lib/page"
import {
  authModeLabels,
  methodLabels,
  type AuthMode,
  type HttpMethod,
  type WebhookDraft,
} from "@/lib/automation"

// The webhook create/edit form, as its own page.
//
// This is the dialog's form lifted out of the list and given a page to live on.
// It holds the draft and reports it: the route owns the request, so a refusal
// can come back to this same form with what was typed still in it. Two rules
// come with it and are not negotiable:
//
//   - A stored secret is never read, never shown and never posted back. The
//     form offers a replacement field; an untouched one posts nothing at all.
//   - The address is checked here only for shape. Whether a host is one this
//     installation may connect to is the transport's answer, given by the
//     backend on every attempt and never guessable in a browser.

// One managed channel, as the source picker names it. The ids are local
// managed_channels.id values — the API's own subscription key — never Telegram
// ids, so the picker offers the stored rows and nothing else.
// The source channel shape lives with the selection logic. The name is kept
// here as well because the webhook table and the automation overview have
// always read it from this module.
export type SourceChannelOption = SourceChannel

export interface WebhookFieldErrors {
  name?: string
  url?: string
  authHeader?: string
  secret?: string
  timeoutMs?: string
  maxAttempts?: string
}

// The same bounds the API enforces, read here so a value outside them is
// refused before the request goes out rather than coming back as one.
export const MIN_TIMEOUT_MS = 100
export const MAX_TIMEOUT_MS = 300_000
export const MIN_ATTEMPTS = 1
export const MAX_ATTEMPTS = 100

// The delivery's own safe starting values: one attempt may take five seconds
// and one event may cost three of them. They are shown rather than hidden,
// because a number the operator cannot see is a number they cannot reason
// about — and they are the values a create sends when nothing is typed.
export const DEFAULT_TIMEOUT_MS = 5_000
export const DEFAULT_MAX_ATTEMPTS = 3

// A custom authentication header starts here rather than at nothing, so the
// common case is one click away and the name is never invented twice.
export const DEFAULT_AUTH_HEADER = "X-API-Key"

const validTimeout = (value: number) =>
  Number.isInteger(value) && value >= MIN_TIMEOUT_MS && value <= MAX_TIMEOUT_MS
const validAttempts = (value: number) =>
  Number.isInteger(value) && value >= MIN_ATTEMPTS && value <= MAX_ATTEMPTS

// The address check is the API's own save-time rule and nothing more: an
// absolute http(s) address with a host, no credentials and no fragment.
// literalHost is the host exactly as the operator typed it.
//
// `new URL` normalises an address literal — "127.1" becomes "127.0.0.1" — and
// the backend's rule is about the literal it was given, so this comparison has
// to be made on the same text or the form would accept an address the server
// is about to refuse.
function literalHost(raw: string): string {
  const authority =
    raw.trim().replace(/^[a-zA-Z][a-zA-Z0-9+.-]*:\/\//, "").split(/[/?#]/)[0] ?? ""
  const withoutUser = authority.slice(authority.lastIndexOf("@") + 1)
  return withoutUser
    .replace(/:\d+$/, "")
    .replace(/^\[/, "")
    .replace(/\]$/, "")
    .toLowerCase()
}

// isLoopbackHost mirrors the backend's list: three targets, not a range.
function isLoopbackHost(host: string): boolean {
  return host === "localhost" || host === "127.0.0.1" || host === "::1"
}

// allowLocalhost is required, not defaulted: a call site that forgets it would
// silently refuse every loopback address while the form in front of the operator
// accepts one, and nothing short of a type error catches that.
export function urlErrorOf(
  raw: string,
  allowLocalhost: boolean,
  deploymentAllowsLocalhost = true
): string | null {
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
  const loopback = isLoopbackHost(literalHost(value))
  if (loopback && !allowLocalhost) {
    // Two switches can be the reason, and only one of them is on this page to
    // be turned. When the deployment forbids it the form's own switch is
    // disabled, so telling the operator to enable that one is a dead end — the
    // sentence has to name the setting that can actually change.
    return deploymentAllowsLocalhost
      ? "این نشانی به localhost اشاره دارد. برای ارسال به آن، گزینه «اجازه ارسال به localhost» را در بخش «تنظیمات پیشرفته» روشن کنید."
      : "این نشانی به localhost اشاره دارد و ارسال به localhost در تنظیمات سرور غیرفعال است."
  }
  // Plain http is only a loopback destination here; every other address stays
  // https-only, exactly as the server's own validator has it.
  if (parsed.protocol === "http:" && !loopback) {
    return "نشانی باید با https:// آغاز شود."
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

export const methodOptions: ComboOption[] = (
  ["POST", "PUT", "PATCH"] as HttpMethod[]
).map((value) => ({
  value,
  label: methodLabels[value],
  detail: value === "POST" ? "پیش‌فرض" : undefined,
}))

// The four authentication modes, named the way the protocol names them and
// glossed in Persian underneath. The row under the picker spells each one out
// again in full, because the list itself lives in a closed popup and an
// operator should not have to open it to learn what a mode does.
export const authOptions: ComboOption[] = (
  ["hmac", "bearer", "header", "none"] as AuthMode[]
).map((value) => ({
  value,
  label:
    value === "hmac"
      ? "HMAC-SHA256"
      : value === "bearer"
        ? "Bearer Token"
        : value === "header"
          ? "Custom Header"
          : "بدون احراز هویت",
  detail: authModeLabels[value],
}))

// What each mode does, in one line, under the picker. The technical name leads
// so the option and the explanation cannot drift apart.
function authModeHint(mode: AuthMode): string {
  switch (mode) {
    case "hmac":
      return "HMAC-SHA256: امضای بدنهٔ درخواست با Secret Key ساخته و در هدر امضا فرستاده می‌شود."
    case "bearer":
      return "Bearer Token: مقدار توکن در هدر Authorization به‌صورت Bearer فرستاده می‌شود."
    case "header":
      return "Custom Header: مقدار API Key در هدری با نام دلخواه فرستاده می‌شود."
    default:
      return "بدون احراز هویت: هیچ هدر احرازی برای این وب‌هوک فرستاده نمی‌شود."
  }
}

// The non-secret fields are the same for a create and an edit, so the two
// screens differ only in their title, their submit label and the record they
// started from. This is what makes one form honest about both: an edit that
// leaves a field alone keeps the stored value, and a create that leaves it
// alone sends the defaults above.
export function WebhookForm({
  formId = "webhook-form",
  record,
  seed,
  channels,
  accounts = [],
  servers = [],
  localhostAllowed = false,
  channelsLoading = false,
  channelsError = null,
  error = null,
  fieldErrors,
  pending = false,
  testing = false,
  onTest,
  onSubmit,
  onCancel,
  onDirtyChange,
}: {
  // The form's own id. The Save button lives in the page header, outside the
  // form element, and reaches it through this id.
  formId?: string
  // null is a create. Otherwise the record being edited — id and whether a
  // secret is stored, and nothing else: the value itself is never in a browser.
  record: { id: number; hasSecret: boolean } | null
  // What the form starts from. It is seeded exactly once per record, so a
  // re-render never writes empty defaults over loaded values.
  seed: WebhookDraft
  channels: SourceChannelOption[]
  // accounts is the reader's own, and nothing else: a webhook is bound to one
  // of them and cannot be offered any it does not own.
  accounts?: TelegramAccount[]
  // The Telegram connections a source channel belongs to. The connection is
  // chosen first and the channel list follows from it: it is a view over the
  // managed channel's own `client_key`, never a new field on the record.
  servers?: TelegramServer[]
  // Whether the deployment offers the loopback exception at all. With it off
  // the switch is disabled and a localhost address is refused by this form,
  // and the server refuses it too.
  localhostAllowed?: boolean
  channelsLoading?: boolean
  channelsError?: string | null
  error?: string | null
  fieldErrors?: WebhookFieldErrors
  pending?: boolean
  // The edit page offers a test send. It is a separate action and never part of
  // a save, and a create has no record to test so it does not offer one.
  testing?: boolean
  onTest?: () => void
  onSubmit: (draft: WebhookDraft) => void
  onCancel: () => void
  // Reports whether the draft differs from what it was seeded with, so the page
  // can guard a navigation away from unsaved work.
  onDirtyChange?: (dirty: boolean) => void
}): React.ReactElement {
  const [draft, setDraft] = React.useState<WebhookDraft>(() => seed)
  const [touched, setTouched] = React.useState(false)

  const patch = (next: Partial<WebhookDraft>) =>
    setDraft((previous) => ({ ...previous, ...next }))

  const editing = record !== null
  const needsHeader = draft.authMode === "header"
  // The loopback exception needs the deployment's permission as well as this
  // webhook's, so the field only accepts a localhost address when both are on.
  const loopbackAllowed = localhostAllowed && draft.allowLocalhost

  // Which connection the source picker is showing. Until the operator chooses
  // one it is the connection the saved channels belong to, so an edit opens on
  // the list its record was built from and a create opens on none. Nothing here
  // rewrites the selection while the channel list is still arriving.
  const [chosenServer, setChosenServer] = React.useState<string | undefined>()
  const sourceServer =
    chosenServer ?? serverKeyFor(channels, draft.sourceChannelIds)
  const changeSourceServer = (key: string | undefined) => {
    setChosenServer(key)
    // A channel this connection cannot reach is not a channel this webhook can
    // be sourced from, so it leaves with the connection rather than being
    // posted as a source that cannot deliver.
    patch({
      sourceChannelIds: reachableIds(channels, draft.sourceChannelIds, key),
    })
  }

  // Which account this webhook observes under. Until the operator chooses one
  // it is the account the saved channels belong to, so an edit opens on its own
  // record and a create opens on none.
  const [chosenAccount, setChosenAccount] = React.useState<string | undefined>()
  const accountID =
    chosenAccount ?? (draft.accountID ? draft.accountID : undefined)
  const account = accounts.find((candidate) => candidate.id === accountID)
  const changeAccount = (id: string | undefined) => {
    setChosenAccount(id)
    // A channel the new account does not hold cannot deliver to this webhook,
    // so those sources leave with the account. The webhook is never handed to a
    // different account instead — the reader chose one and the reader changes
    // it, nothing here does it for them.
    const reachable = channelsForAccount(channels, id)
    patch({
      accountID: id ?? null,
      sourceChannelIds: draft.sourceChannelIds.filter((source) =>
        reachable.some((channel) => channel.id === source)
      ),
    })
  }
  // The source list is the chosen account's own inventory. With no account
  // chosen it falls back to the connection filter, so a legacy record still
  // opens on something rather than on nothing.
  const sourceOptions = accountID
    ? sourceItemsForAccount(channels, accountID, draft.sourceChannelIds)
    : sourceItems(channels, sourceServer, draft.sourceChannelIds)

  // Dirty is "differs from the seed", not "has been touched": an edit that
  // changes nothing must not stand in the way of leaving.
  const dirty = React.useMemo(() => !sameDraft(draft, seed), [draft, seed])
  const notifyDirty = React.useRef(onDirtyChange)
  notifyDirty.current = onDirtyChange
  React.useEffect(() => {
    notifyDirty.current?.(dirty)
  }, [dirty])

  const nameError =
    (touched && draft.name.trim() === "" ? "نام وب‌هوک الزامی است." : null) ??
    fieldErrors?.name
  const urlError =
    (touched ? urlErrorOf(draft.url, loopbackAllowed, localhostAllowed) : null) ??
    fieldErrors?.url
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

  // The source is two choices and both are required: the connection, and at
  // least one channel that connection can reach. A webhook with no source would
  // never receive anything to send on.
  const serverError = touched && !sourceServer ? SERVER_REQUIRED : null
  const sourcesError =
    touched && draft.sourceChannelIds.length === 0 ? SOURCES_REQUIRED : null

  const complete =
    draft.name.trim() !== "" &&
    Boolean(sourceServer) &&
    draft.sourceChannelIds.length > 0 &&
    urlErrorOf(draft.url, loopbackAllowed, localhostAllowed) === null &&
    (!needsHeader || authHeaderErrorOf(draft.authHeader) === null) &&
    validTimeout(draft.timeoutMs) &&
    validAttempts(draft.maxAttempts)

  const setAuthMode = (next: string) => {
    const authMode = next as AuthMode
    // A custom header needs a name, and an empty one is a mistake rather than a
    // choice: switching to this mode fills in the documented default. And a
    // mode with no authentication has no secret field, so a value left over
    // from one that does is dropped here rather than sitting in a payload that
    // will never send it.
    patch({
      authMode,
      authHeader:
        authMode === "header" && draft.authHeader.trim() === ""
          ? DEFAULT_AUTH_HEADER
          : draft.authHeader,
      secret: authMode === "none" ? null : draft.secret,
    })
  }

  return (
    <AutomationFormLayout>
    <PageShell
      title={editing ? "ویرایش وب‌هوک" : "افزودن وب‌هوک"}
      description={
        editing
          ? "هر بار ذخیره، همه فیلدها را جایگزین می‌کند. رمز ذخیره‌شده فقط با وارد کردن مقدار جدید تغییر می‌کند."
          : "رویدادهای کانال‌های انتخاب‌شده به نشانی مقصد فرستاده می‌شود."
      }
      actions={
        /* The page's only Save and its only Cancel, in the header rather
           than in a second action bar under the fields. They are outside the
           form element and reach it through its id, so nothing at the bottom
           of a long page can be hidden behind the phone's bottom bar. The
           shared component owns their order and their styling: in RTL Cancel
           sits to the right of the primary action, which is therefore the
           outermost control on the physical left. */
        <AutomationFormActions
          formId={formId}
          submitLabel={editing ? "ذخیره تغییرات" : "افزودن"}
          pendingLabel={editing ? "در حال ذخیره…" : "در حال ایجاد…"}
          pending={pending}
          // A source is not optional: with no connection, or no channel on the
          // chosen one, there is nothing to save. The button says so rather than
          // accepting a submit that could only come back refused.
          disabled={!sourceServer || draft.sourceChannelIds.length === 0}
          onCancel={onCancel}
        />
      }
    >
      <form
        id={formId}
        className="flex flex-col gap-6"
        noValidate
        onSubmit={(event) => {
          event.preventDefault()
          setTouched(true)
          if (!complete) return
          onSubmit(draft)
        }}
      >
        {/* ------------------------- A. اطلاعات وب‌هوک ------------------------- */}
        <section className="flex flex-col gap-3" aria-labelledby="webhook-info">
          <h2 id="webhook-info" className="text-sm font-semibold">
            اطلاعات وب‌هوک
          </h2>
          <Field data-invalid={nameError ? "" : undefined}>
            <FieldLabel htmlFor="webhook-name" required>
              نام وب‌هوک
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
        </section>

        <Separator />

        {/* ----------------------- B. کانال‌های منبع ------------------------- */}
        <section
          className="flex flex-col gap-3"
          aria-labelledby="webhook-sources-heading"
        >
          <h2 id="webhook-sources-heading" className="text-sm font-semibold">
            کانال‌های منبع
          </h2>
          <p className="text-muted-foreground text-sm">
            پیام‌های جدید این کانال‌ها به وب‌هوک ارسال می‌شوند.
          </p>
          {/* The account is the identity the webhook observes under, and it is
              chosen first: the source channels below are that account's. It is
              never inferred from a server filter and never swapped for another
              account because this one lost its session. */}
          <Field>
            <FieldLabel htmlFor="webhook-account">حساب تلگرام</FieldLabel>
            <AccountCombobox
              id="webhook-account"
              accounts={accounts}
              value={accountID}
              onChange={changeAccount}
              disabled={pending}
              emptyText="حسابی متصل نیست؛ نخست از «حساب‌های من» یک حساب متصل کنید."
            />
            <FieldDescription>
              رویدادهای این حساب به وب‌هوک ارسال می‌شود.
            </FieldDescription>
          </Field>
          <AccountPausedNotice account={account} />
          {/* The connection is chosen first and the channels follow from it:
              only channels this connection can reach are offered, so a source
              id belonging to another one is never on the list to be picked. */}
          <SourceServerField
            id="webhook-source-server"
            servers={servers}
            value={sourceServer}
            onChange={changeSourceServer}
            error={serverError}
            disabled={pending}
          />
          {channelsLoading ? (
            <LoadingState label="در حال بارگذاری کانال‌ها…" />
          ) : channelsError ? (
            <p role="alert" className="text-sm font-medium text-destructive">
              {channelsError}
            </p>
          ) : (
            <>
              <MultiPicker
                id="webhook-sources"
                title="کانال‌های منبع *"
                placeholder={
                  sourceServer
                    ? "کانال را انتخاب کنید"
                    : "نخست سرور تلگرام را انتخاب کنید"
                }
                emptyText="کانالی در دسترس نیست."
                // A saved channel from another connection is listed beside the
                // chosen account's own, so an existing source is preserved and
                // still posted rather than silently trimmed to the filter.
                items={sourceOptions}
                value={draft.sourceChannelIds}
                onValueChange={(sourceChannelIds) => patch({ sourceChannelIds })}
                labelOf={(channel) => channel.label}
                disabled={pending || !sourceServer}
              />
              {sourcesError && (
                <p role="alert" className="text-sm font-medium text-destructive">
                  {sourcesError}
                </p>
              )}
            </>
          )}
        </section>

        <Separator />

        {/* --------------------- C. تنظیمات درخواست ------------------------- */}
        <section
          className="flex flex-col gap-3"
          aria-labelledby="webhook-request"
        >
          <h2 id="webhook-request" className="text-sm font-semibold">
            تنظیمات درخواست
          </h2>
          {/* The address takes the width and the method is a short list beside
              it; on a phone the two stack rather than squeeze. */}
          <div className="grid gap-4 sm:grid-cols-[minmax(0,1fr)_12rem]">
            <Field data-invalid={urlError ? "" : undefined}>
              <FieldLabel htmlFor="webhook-url" required>
                آدرس وب‌هوک
              </FieldLabel>
              <FieldControl
                id="webhook-url"
                name="url"
                dir="ltr"
                type="url"
                inputMode="url"
                placeholder="https://example.com/webhook"
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
              <FieldLabel htmlFor="webhook-method">متد درخواست</FieldLabel>
              <OptionCombobox
                id="webhook-method"
                label="متد درخواست"
                options={methodOptions}
                value={draft.method}
                onChange={(method) => patch({ method: method as HttpMethod })}
                placeholder="متد را انتخاب کنید"
                disabled={pending}
              />
            </Field>
          </div>
        </section>

        <Separator />

        {/* ------------------------- D. احراز هویت -------------------------- */}
        <section
          className="flex flex-col gap-3"
          aria-labelledby="webhook-auth-section"
        >
          <h2 id="webhook-auth-section" className="text-sm font-semibold">
            احراز هویت
          </h2>
          <Field>
            <FieldLabel htmlFor="webhook-auth">شیوه احراز هویت</FieldLabel>
            <OptionCombobox
              id="webhook-auth"
              label="شیوه احراز هویت"
              options={authOptions}
              value={draft.authMode}
              onChange={setAuthMode}
              placeholder="شیوه احراز را انتخاب کنید"
              disabled={pending}
            />
            <FieldDescription>{authModeHint(draft.authMode)}</FieldDescription>
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
                placeholder="X-API-Key"
                value={draft.authHeader}
                onChange={(event) => patch({ authHeader: event.target.value })}
                onBlur={() => setTouched(true)}
                required
              />
              <FieldDescription>
                مقدار این هدر از Secret Key / API Key واردشده در زیر گرفته می‌شود.
                نام‌هایی مانند <span dir="ltr">Authorization</span> و هدرهای{" "}
                <span dir="ltr">webhook-</span> پذیرفته نمی‌شوند.
              </FieldDescription>
              {authHeaderError && <FieldError>{authHeaderError}</FieldError>}
            </Field>
          )}

          {/* No auth mode with no secret sends nothing at all: a stale secret is
              never left in a payload because there is no secret in one. */}
          {draft.authMode !== "none" && (
            <>
              <SecretField
                id="webhook-secret"
                hasSecret={record?.hasSecret ?? false}
                value={draft.secret}
                onValueChange={(secret) => patch({ secret })}
                // The field reports a refusal only while it holds a value: an
                // untouched one says "nothing is stored here" and would swallow
                // the sentence. It is shown just below instead, next to the
                // field it belongs to.
                error={draft.secret === null ? null : fieldErrors?.secret}
                disabled={pending}
                label={
                  draft.authMode === "hmac"
                    ? "Secret Key"
                    : draft.authMode === "bearer"
                      ? "Bearer Token"
                      : "API Key"
                }
              />
              {draft.secret === null && fieldErrors?.secret && (
                <p role="alert" className="text-sm font-medium text-destructive">
                  {fieldErrors.secret}
                </p>
              )}
            </>
          )}
        </section>

        <Separator />

        {/* ------------------------ E. وضعیت وب‌هوک ------------------------- */}
        <section
          className="flex flex-col gap-3"
          aria-labelledby="webhook-status-section"
        >
          <h2 id="webhook-status-section" className="text-sm font-semibold">
            وضعیت وب‌هوک
          </h2>
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
        </section>

        <Separator />

        {/* --------------------- F. تنظیمات پیشرفته ------------------------- */}
        {/* Shut until it is opened: the two numbers it holds are tuning, not
            something every webhook needs looked at. The panel stays mounted, so
            opening and closing it costs nothing that has been typed into it. */}
        <Accordion type="single" collapsible defaultValue={null}>
          <AccordionItem value="advanced">
            <AccordionTrigger>تنظیمات پیشرفته</AccordionTrigger>
            <AccordionContent>
              {/* Two numbers the store keeps and the delivery honours: how long
                  one attempt may take, and how many attempts one event may
                  cost. */}
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
                min={MIN_TIMEOUT_MS}
                max={MAX_TIMEOUT_MS}
                value={String(draft.timeoutMs)}
                onChange={(event) =>
                  patch({
                    timeoutMs:
                      event.target.value === ""
                        ? Number.NaN
                        : Number(event.target.value),
                  })
                }
                onBlur={() => setTouched(true)}
                required
              />
              <FieldDescription>
                بین ۱۰۰ تا ۳۰۰۰۰۰ میلی‌ثانیه؛ پیش‌فرض ۵۰۰۰.
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
                min={MIN_ATTEMPTS}
                max={MAX_ATTEMPTS}
                value={String(draft.maxAttempts)}
                onChange={(event) =>
                  patch({
                    maxAttempts:
                      event.target.value === ""
                        ? Number.NaN
                        : Number(event.target.value),
                  })
                }
                onBlur={() => setTouched(true)}
                required
              />
                  <FieldDescription>بین ۱ تا ۱۰۰ تلاش؛ پیش‌فرض ۳.</FieldDescription>
                  {attemptsError && <FieldError>{attemptsError}</FieldError>}
                </Field>
              </div>

              {/* The loopback exception. It is off unless both the deployment
                  and this webhook ask for it, and the switch says which half is
                  missing rather than being a switch that will not stay on. */}
              <div className="flex flex-nowrap items-center justify-between gap-4 rounded-lg border px-3 py-2.5">
                <div className="flex min-w-0 flex-col gap-0.5">
                  <Label
                    htmlFor="webhook-allow-localhost"
                    className="font-semibold"
                  >
                    اجازه ارسال به localhost
                  </Label>
                  <p className="text-muted-foreground text-xs">
                    {localhostAllowed
                      ? "اجازه ارسال وب‌هوک به localhost بدون نیاز به HTTPS، برای سرویس‌های محلی."
                      : "ارسال به localhost در تنظیمات سرور غیرفعال است."}
                  </p>
                </div>
                <Switch
                  id="webhook-allow-localhost"
                  checked={draft.allowLocalhost}
                  onCheckedChange={(allowLocalhost) => patch({ allowLocalhost })}
                  disabled={pending || !localhostAllowed}
                  aria-label="اجازه ارسال به localhost"
                />
              </div>
            </AccordionContent>
          </AccordionItem>
        </Accordion>

        {/* A test send is not a save and not a delivery: the service records
            nothing for it, so it lives beside the form and never inside the
            submit. Only an edit offers it — a create has no record to test. */}
        {record !== null && (
          <>
            <Separator />
            <section
              className="flex flex-col gap-3"
              aria-labelledby="webhook-test"
            >
              <h2 id="webhook-test" className="text-sm font-semibold">
                آزمایش وب‌هوک
              </h2>
              <div className="flex flex-wrap items-center justify-between gap-3 rounded-lg border px-3 py-2.5">
                <p className="text-muted-foreground max-w-xl text-sm">
                  یک درخواست آزمایشی به نشانی مقصد فرستاده می‌شود. نتیجهٔ آن در
                  تاریخچهٔ تحویل‌ها ثبت نمی‌شود.
                </p>
                <Button
                  type="button"
                  variant="outline"
                  disabled={pending || testing || !onTest}
                  onClick={onTest}
                >
                  {testing && (
                    <LoaderCircleIcon
                      aria-hidden="true"
                      className="animate-spin"
                    />
                  )}
                  {testing ? "در حال ارسال…" : "آزمایش وب‌هوک"}
                </Button>
              </div>
            </section>
          </>
        )}

        {error && (
          <p
            role="alert"
            className="rounded-lg bg-destructive/10 px-3 py-2 text-sm text-destructive"
          >
            {error}
          </p>
        )}
      </form>
    </PageShell>
    </AutomationFormLayout>
  )
}

// The draft as the form sees it is one value: a save, a reload and a reopen of
// the same record must show the same page. Only a value the operator typed
// makes two drafts differ.
function sameDraft(a: WebhookDraft, b: WebhookDraft): boolean {
  return (
    a.name === b.name &&
    a.description === b.description &&
    a.enabled === b.enabled &&
    a.url === b.url &&
    a.method === b.method &&
    a.authMode === b.authMode &&
    a.authHeader === b.authHeader &&
    a.secret === b.secret &&
    a.timeoutMs === b.timeoutMs &&
    a.allowLocalhost === b.allowLocalhost &&
    a.maxAttempts === b.maxAttempts &&
    a.sourceChannelIds.length === b.sourceChannelIds.length &&
    a.sourceChannelIds.every((id, index) => id === b.sourceChannelIds[index])
  )
}
