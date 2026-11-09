import {
  Link,
  useBlocker,
  useLocation,
  useNavigate,
  useNavigation,
  useParams,
  useSubmit,
  type ClientActionFunctionArgs,
  type ClientLoaderFunctionArgs,
  type ShouldRevalidateFunctionArgs,
} from "react-router"
import { ArrowRightIcon, WebhookIcon } from "lucide-react"
import * as React from "react"

import {
  WebhookForm,
  authHeaderErrorOf,
  urlErrorOf,
  DEFAULT_MAX_ATTEMPTS,
  DEFAULT_TIMEOUT_MS,
  MAX_ATTEMPTS,
  MAX_TIMEOUT_MS,
  MIN_ATTEMPTS,
  MIN_TIMEOUT_MS,
  type SourceChannelOption,
  type WebhookFieldErrors,
} from "@/components/automation/webhook-form"
import { canManageWebhooks } from "@/components/automation/webhook-access"
import {
  explainWebhookError,
  secretFieldErrorOf,
} from "@/components/automation/webhook-errors"
import { EmptyState, ErrorState, LoadingState } from "@/components/states"
import { Button } from "@workspace/ui/components/button"
import { useToast } from "@workspace/ui/components/toast"

import {
  automationApi,
  automationReturnTo,
  emptyWebhookDraft,
  readDeliveryRecord,
  readWebhookRecord,
  webhookBody,
  webhookDraftOf,
  type DeliveryItem,
  type WebhookDraft,
  type WebhookItem,
} from "@/lib/automation"
import {
  automationAccessDenied,
  isAuthLost,
  isForbidden,
  loginRedirect,
} from "@/lib/automation-access"
import { apiRequest, errorMessage } from "@/lib/api"
import type { TelegramServer } from "@/lib/types"
import { PageShell, PageError, pageMeta, section } from "@/lib/page"
import { requireSession } from "@/lib/session"

// The webhook create/edit page.
//
// One route module answers both addresses: `automation/webhooks/new` has no
// `webhookId` and is a create, `automation/webhooks/:webhookId/edit` has one and
// is an edit of that record. The form itself is shared — see
// `components/automation/webhook-form.tsx` — so the two can never grow apart.
//
// A save is answered in this page rather than with a redirect. The toast has to
// be raised before the page leaves, and the listing it returns to is the view
// the link here was opened from (its tab, its search, its page), which lives in
// the navigation state and nowhere else.

export const meta = ({ params }: { params: Record<string, string | undefined> }) =>
  pageMeta(isCreation(params.webhookId) ? "افزودن وب‌هوک" : "ویرایش وب‌هوک")

export const handle = section("اتوماسیون")

// `new`, and the absence of the segment altogether, both mean "no webhook yet".
// Anything else that is not a positive integer is not a record: it is reported
// as missing rather than guessed at.
//
// Module-private on purpose: a route module is split into a chunk per
// clientLoader/clientAction, and an exported helper can end up in a chunk the
// others do not import — which builds happily and then throws
// "isCreation is not defined" in the browser.
function isCreation(raw: string | undefined): boolean {
  return raw === undefined || raw === "new"
}

function validID(raw: string | undefined): number | null {
  return raw !== undefined && /^[0-9]+$/.test(raw) ? Number(raw) : null
}

// This page owns its own writes. A save that was refused must keep exactly what
// was typed — including a replacement secret, which no loader may read back — so
// the loader is not re-run behind the form after a submission to this same URL.
// A navigation to the page always loads: `formMethod` is set only for a
// submission, so arriving here from anywhere else still reads the stored row.
export function shouldRevalidate({
  formMethod,
}: ShouldRevalidateFunctionArgs): boolean {
  return formMethod === undefined
}

type LoaderData =
  | {
      status: "ok"
      creating: boolean
      // null on a create. Otherwise the stored record, read on its own URL so a
      // refresh shows the row's values whatever page the list happens to be on.
      webhook: WebhookItem | null
      // Local managed_channels.id values only: the picker offers the stored
      // channel rows and never a Telegram peer id.
      channels: SourceChannelOption[]
      // The Telegram connections the source picker filters by. A channel's
      // connection is already known from the managed row's own client_key, so
      // this is a view over the record and not a second thing to store.
      servers: TelegramServer[]
      // Whether the deployment offers the loopback exception at all.
      localhostAllowed: boolean
      // A channel list that could not be read is reported, never silently shown
      // as an empty one that would look like "no channels exist".
      channelsError: string | null
      canManage: boolean
    }
  | { status: "missing" }
  | { status: "forbidden"; message: string }
  | { status: "error"; message: string }

export async function clientLoader({
  params,
  request,
}: ClientLoaderFunctionArgs): Promise<LoaderData> {
  const { token, me } = await requireSession(request)
  // A restricted administrator is told so from the session itself: this page
  // must never fall back to a silently empty form because the calls returned
  // nothing.
  const denied = automationAccessDenied(me)
  if (denied) return { status: "forbidden", message: denied }

  const creating = isCreation(params.webhookId)
  const id = validID(params.webhookId)
  if (!creating && id === null) return { status: "missing" }

  try {
    const [channels, servers, localhostAllowed, webhook] = await Promise.all([
      apiRequest<{
        channels?: { id: number; title: string; client_key?: string }[]
      }>(
        "GET",
        "/local/channels",
        { token, query: { page: 1, per_page: 100 } }
      )
        .then((answer) => ({
          rows: (answer.data?.channels ?? []).map((channel) => ({
            id: channel.id,
            label: channel.title,
            clientKey: channel.client_key ?? "",
          })),
          error: null as string | null,
        }))
        .catch((error) => {
          // A lost session or a refusal is about the page, not about the list:
          // it is rethrown and answered there. Anything else leaves the form
          // usable and says which list it could not read.
          if (isAuthLost(error) || isForbidden(error)) throw error
          return {
            rows: [] as SourceChannelOption[],
            error: errorMessage(error),
          }
        }),
      // The connections a source channel belongs to. A failure here leaves the
      // picker empty rather than taking the whole page down with it: the source
      // is still a choice the operator makes, and the store is the authority on
      // which channel belongs to which connection either way.
      apiRequest<{ servers?: TelegramServer[] }>("GET", "/telegram", { token })
        .then((answer) => answer.data?.servers ?? [])
        .catch(() => [] as TelegramServer[]),
      // The deployment's half of the loopback exception. It is a capability and
      // not a setting: the form needs to know whether its switch can be turned
      // on at all, and there is nothing here to keep secret.
      apiRequest<{ status?: { webhook_allow_localhost?: boolean } }>(
        "GET",
        "/local/automation/status",
        { token }
      )
        .then((answer) => Boolean(answer.data?.status?.webhook_allow_localhost))
        .catch(() => false),
      id === null
        ? Promise.resolve(null)
        : apiRequest<unknown>("GET", automationApi.webhook(id), { token }).then(
            (answer) => readWebhookRecord(answer.data)
          ),
    ])

    return {
      status: "ok",
      creating,
      webhook,
      channels: channels.rows,
      servers,
      localhostAllowed,
      channelsError: channels.error,
      canManage: canManageWebhooks(me),
    }
  } catch (error) {
    if (isAuthLost(error)) throw loginRedirect(request)
    if (isForbidden(error)) {
      return {
        status: "forbidden",
        message: "شما مجوز مشاهده وب‌هوک‌ها را ندارید.",
      }
    }
    if (
      error instanceof Error &&
      "status" in error &&
      (error as { status: number }).status === 404
    ) {
      // A record the store no longer has is a missing record, not a broken page.
      return { status: "missing" }
    }
    return { status: "error", message: errorMessage(error) }
  }
}

// What one test send reported. It is never a persisted delivery: the service
// writes nothing to the history for a test, so the outcome comes from this one
// response and nowhere else. Only the outcome travels back to the page — never
// the request or the response the exchange carried.
export interface TestOutcome {
  status: DeliveryItem["status"]
  httpStatus: number
  error: string
}

export interface ActionData {
  ok?: boolean
  intent?: "create" | "update" | "test"
  error?: string
  // What was posted, for a form that has to be rebuilt. It carries no secret:
  // a typed secret never travels through action data, and a form rebuilt here
  // leaves the stored one untouched rather than guessing at a value.
  values?: Omit<WebhookDraft, "secret">
  fieldErrors?: WebhookFieldErrors
  testResult?: TestOutcome
}

export async function clientAction({
  request,
  params,
}: ClientActionFunctionArgs): Promise<ActionData> {
  const { token } = await requireSession(request)
  const form = await request.formData()
  const intent = String(form.get("intent") ?? "")

  const creating = isCreation(params.webhookId)
  const id = validID(params.webhookId)
  const targetID = creating ? 0 : (id ?? 0)

  if (intent !== "create" && intent !== "update" && intent !== "test") {
    return { error: "درخواست نامعتبر است.", intent: "update" }
  }
  if (intent !== "create" && targetID <= 0) {
    return { error: "شناسه وب‌هوک نامعتبر است.", intent: "update" }
  }

  const values: Omit<WebhookDraft, "secret"> = {
    name: String(form.get("name") ?? ""),
    description: String(form.get("description") ?? ""),
    enabled: form.get("enabled") === "true",
    url: String(form.get("url") ?? ""),
    method: String(form.get("method") ?? "POST") as WebhookDraft["method"],
    authMode: String(form.get("auth_mode") ?? "none") as WebhookDraft["authMode"],
    authHeader: String(form.get("auth_header") ?? ""),
    timeoutMs: readInt(form.get("timeout_ms"), DEFAULT_TIMEOUT_MS),
    maxAttempts: readInt(form.get("max_attempts"), DEFAULT_MAX_ATTEMPTS),
    allowLocalhost: form.get("allow_localhost") === "true",
    sourceChannelIds: readIDs(form.get("source_channel_ids")),
  }

  // A secret posted as an empty hidden field means "do not touch the stored
  // one"; only a non-empty value replaces it, and only an explicit clear
  // removes it. A mode with no authentication carries no secret at all, so a
  // value left in the form from one that does is dropped here rather than
  // stored against a configuration that will never send it.
  const rawSecret = String(form.get("secret") ?? "")
  const clearSecret = form.get("secret_clear") === "true"
  const secret =
    values.authMode === "none"
      ? null
      : clearSecret
        ? ""
        : rawSecret === ""
          ? null
          : rawSecret

  if (intent === "test") {
    try {
      // A test is not a delivery and the service records nothing for it, so the
      // outcome comes back from this one response. Reporting it as history
      // would claim a record the store never wrote.
      const sent = await apiRequest<unknown>(
        "POST",
        automationApi.testWebhook(targetID),
        { token }
      )
      return {
        ok: true,
        intent: "test",
        testResult: testOutcomeOf(readDeliveryRecord(sent.data)),
      }
    } catch (error) {
      if (isAuthLost(error)) throw loginRedirect(request)
      return {
        intent: "test",
        error: isForbidden(error)
          ? "شما مجوز این عمل را ندارید."
          : explainWebhookError(error),
      }
    }
  }

  const draft: WebhookDraft = { ...values, secret }
  const fieldErrors: WebhookFieldErrors = {}
  if (draft.name.trim() === "") fieldErrors.name = "نام وب‌هوک الزامی است."
  // The same structural checks the form runs on submit, repeated here because a
  // request may arrive from anywhere: an address or a header name the API will
  // not accept is refused before it travels. The webhook's own opt-in is part
  // of that shape and must travel with it, or a loopback address is refused
  // here no matter what the form's switch says. Whether the deployment permits
  // the exception at all is the server's answer to give — this half does not
  // read it, and must not pretend to.
  const urlProblem = urlErrorOf(draft.url, draft.allowLocalhost)
  if (urlProblem) fieldErrors.url = urlProblem
  if (draft.authMode === "header") {
    const headerProblem = authHeaderErrorOf(draft.authHeader)
    if (headerProblem) fieldErrors.authHeader = headerProblem
  }
  if (!validTimeout(draft.timeoutMs)) {
    fieldErrors.timeoutMs = `مهلت باید بین ${MIN_TIMEOUT_MS} تا ${MAX_TIMEOUT_MS} میلی‌ثانیه باشد.`
  }
  if (!validAttempts(draft.maxAttempts)) {
    fieldErrors.maxAttempts = `تعداد تلاش‌ها باید بین ${MIN_ATTEMPTS} تا ${MAX_ATTEMPTS} باشد.`
  }
  if (Object.keys(fieldErrors).length > 0) {
    return {
      intent,
      error: "برخی فیلدها را کامل کنید.",
      values,
      fieldErrors,
    }
  }

  try {
    const body = webhookBody(draft)
    if (intent === "create") {
      await apiRequest<unknown>("POST", automationApi.webhooks, { token, body })
    } else {
      await apiRequest("PATCH", automationApi.webhook(targetID), { token, body })
    }
    // The page, not this action, decides where to go: it is the listing's own
    // view and it lives in the navigation state.
    return { ok: true, intent }
  } catch (error) {
    if (isAuthLost(error)) throw loginRedirect(request)
    if (isForbidden(error)) {
      return { intent, error: "شما مجوز این عمل را ندارید.", values }
    }
    // A refusal is reported as the server reported it: nothing here claims
    // success. A refusal about the secret goes back onto the field it was typed
    // into, which is where an operator can act on it.
    const secretProblem = secretFieldErrorOf(error, secret !== null)
    return {
      intent,
      error: explainWebhookError(error),
      values,
      fieldErrors: secretProblem ? { secret: secretProblem } : undefined,
    }
  }
}

// The fields one submit carries. They are the presentation model and nothing
// else: `webhookBody` is the only place the contract's wire names are written,
// and the action rebuilds the draft from these before it calls it. Keeping the
// pair together here is what stops a posted field and the field read from
// drifting apart.
export function webhookSubmitFields(
  draft: WebhookDraft,
  intent: "create" | "update",
  id: number | null
): Record<string, string> {
  return {
    intent,
    id: id === null ? "" : String(id),
    name: draft.name,
    description: draft.description,
    enabled: String(draft.enabled),
    url: draft.url,
    method: draft.method,
    auth_mode: draft.authMode,
    auth_header: draft.authHeader,
    // null posts nothing at all: the stored secret stays as it is. An empty
    // value with the clear flag is the one thing that removes it.
    secret: draft.secret ?? "",
    secret_clear: draft.secret === "" ? "true" : "false",
    source_channel_ids: JSON.stringify(draft.sourceChannelIds),
    timeout_ms: String(draft.timeoutMs),
    max_attempts: String(draft.maxAttempts),
    allow_localhost: String(draft.allowLocalhost),
  }
}

function readIDs(raw: FormDataEntryValue | null): number[] {
  let parsed: unknown
  try {
    parsed = JSON.parse(String(raw ?? "[]"))
  } catch {
    return []
  }
  if (!Array.isArray(parsed)) return []
  return parsed.filter(
    (value): value is number => Number.isInteger(value) && value > 0
  )
}

function readInt(raw: FormDataEntryValue | null, fallback: number): number {
  const value = Number(raw)
  return Number.isInteger(value) && value > 0 ? value : fallback
}

function validTimeout(value: number): boolean {
  return Number.isInteger(value) && value >= MIN_TIMEOUT_MS && value <= MAX_TIMEOUT_MS
}

function validAttempts(value: number): boolean {
  return Number.isInteger(value) && value >= MIN_ATTEMPTS && value <= MAX_ATTEMPTS
}

// The outcome of one test send, read off the exchange the service returned.
// Only the outcome travels on: a test's request and response carry the same
// headers a real one does, and no page here renders them.
// The outcome of one test send, read off the exchange the service returned.
// Module-private for the same reason as `isCreation` above: the action chunk
// has to hold it, and an exported one is not reliably there.
function testOutcomeOf(delivery: DeliveryItem): TestOutcome {
  const attempt = delivery.attempts.at(-1)
  return {
    status: delivery.status,
    httpStatus: attempt?.responseStatus ?? delivery.lastHttpStatus,
    error: delivery.lastError || attempt?.errorMessage || "",
  }
}

// Where a finished save goes. It is the listing's own view — the tab, the
// search and the page a link here was opened from — or the collection's tab
// when the page was opened by a bookmark, a paste or a refresh. A save that did
// not happen goes nowhere.
export function saveReturnTo(
  actionData: ActionData | undefined,
  state: unknown
): string | null {
  if (!actionData?.ok || actionData.intent === "test") return null
  return automationReturnTo(state, "webhooks")
}

export function ErrorBoundary({ error }: { error: unknown }) {
  return (
    <PageShell title="وب‌هوک">
      <PageError error={error} />
    </PageShell>
  )
}

export default function AutomationWebhookForm({
  loaderData,
  actionData,
}: {
  loaderData: LoaderData
  actionData?: ActionData
}) {
  const navigation = useNavigation()
  const navigate = useNavigate()
  const location = useLocation()
  const params = useParams()
  const submit = useSubmit()

  // The unsaved-work guard. It is armed by the form itself and only when the
  // draft differs from what it was seeded with, so an edit that changes nothing
  // never stands in the way of leaving. The answer is the browser's own prompt:
  // this page adds no second dialog system for one question.
  const dirtyRef = React.useRef(false)
  const markDirty = React.useCallback((next: boolean) => {
    dirtyRef.current = next
  }, [])
  // A save is the one navigation the guard must never stand in front of: the
  // draft is being handed to the store, which is the opposite of losing it.
  // The flag is set before the submit leaves, so the guard is off at the moment
  // that navigation is judged, and cleared when the submission settles — which
  // puts it back on for a refusal.
  const savingRef = React.useRef(false)
  const blocker = useBlocker(() => dirtyRef.current && !savingRef.current)
  React.useEffect(() => {
    if (navigation.state === "idle") savingRef.current = false
  }, [navigation.state])
  React.useEffect(() => {
    if (blocker.state !== "blocked") return
    if (window.confirm("تغییرات ذخیره‌نشده‌ای دارید. صفحه ترک شود؟")) {
      blocker.proceed()
    } else {
      blocker.reset()
    }
  }, [blocker])

  const { toast } = useToast()
  const submitting = navigation.state === "submitting"
  const returnTo = automationReturnTo(location.state, "webhooks")

  const record = loaderData.status === "ok" ? loaderData.webhook : null
  const creating = isCreation(params.webhookId)

  // What the form starts from. It is built once per record and the form is
  // keyed on that record, so a page that names one webhook never shows another
  // one's values, and empty defaults never write over loaded ones. A refused
  // write reopens on what was typed.
  const seed = React.useMemo<WebhookDraft>(() => {
    if (actionData?.values) return { ...actionData.values, secret: null }
    return record
      ? webhookDraftOf(record)
      : {
          ...emptyWebhookDraft(),
          // The delivery's own safe starting values, shown rather than hidden.
          timeoutMs: DEFAULT_TIMEOUT_MS,
          maxAttempts: DEFAULT_MAX_ATTEMPTS,
        }
  }, [record, actionData])

  // A successful save leaves the page, so the toast is raised here first and
  // the guard is stood down before the navigation it would otherwise block.
  const done = React.useRef(false)
  const target = saveReturnTo(actionData, location.state)
  React.useEffect(() => {
    if (target === null || done.current) return
    done.current = true
    markDirty(false)
    toast({
      variant: "success",
      title:
        actionData?.intent === "create"
          ? "وب‌هوک ساخته شد."
          : "تغییرات وب‌هوک ذخیره شد.",
    })
    navigate(target)
  }, [target, actionData, navigate, toast, markDirty])

  // A test send is reported as the exchange reported it. It is never a
  // delivery: the service writes nothing to the history for one, so the outcome
  // is the only thing here that may be called a result.
  const tested = React.useRef<string | null>(null)
  React.useEffect(() => {
    if (actionData?.intent !== "test" || !actionData.testResult) return
    const result = actionData.testResult
    const key = `${result.status}:${result.httpStatus}:${result.error}`
    if (tested.current === key) return
    tested.current = key
    const answered = (result.httpStatus ?? 0) > 0
    toast({
      variant: result.status === "succeeded" ? "success" : "error",
      title:
        result.status === "succeeded"
          ? "درخواست آزمایشی پذیرفته شد."
          : "درخواست آزمایشی ناموفق بود.",
      description: [
        answered ? `HTTP ${result.httpStatus}` : "بدون پاسخ",
        result.error || "",
        "این آزمایش در تاریخچه تحویل‌ها ثبت نمی‌شود.",
      ]
        .filter(Boolean)
        .join(" — "),
    })
  }, [actionData, toast])

  const pendingIntent = React.useMemo(() => {
    const formData = navigation.formData
    return formData ? String(formData.get("intent") ?? "") : ""
  }, [navigation.formData])

  const cancel = () => {
    // An explicit cancel is a decision to discard, so it is never blocked: the
    // guard exists for the navigations that are not this one.
    markDirty(false)
    navigate(returnTo)
  }

  if (loaderData.status === "missing") {
    return (
      <PageShell title={creating ? "افزودن وب‌هوک" : "ویرایش وب‌هوک"}>
        <EmptyState
          icon={WebhookIcon}
          title="وب‌هوک پیدا نشد"
          description="شناسه در نشانی معتبر نیست، یا این وب‌هوک حذف شده است."
          action={
            <Button variant="outline" render={<Link to={returnTo} />}>
              <ArrowRightIcon data-icon="inline-start" />
              بازگشت به اتوماسیون
            </Button>
          }
        />
      </PageShell>
    )
  }

  if (loaderData.status !== "ok") {
    return (
      <PageShell title="وب‌هوک">
        <ErrorState
          title={
            loaderData.status === "forbidden" ? "دسترسی مجاز نیست" : undefined
          }
          message={loaderData.message}
          onRetry={() => window.location.reload()}
        />
      </PageShell>
    )
  }

  // A page that is still being loaded shows that it is loading, and never the
  // empty defaults of a record it has not read yet.
  if (navigation.state === "loading") {
    return (
      <PageShell title={creating ? "افزودن وب‌هوک" : "ویرایش وب‌هوک"}>
        <LoadingState label="در حال بارگذاری وب‌هوک…" />
      </PageShell>
    )
  }

  if (!loaderData.canManage) {
    return (
      <PageShell title={creating ? "افزودن وب‌هوک" : "ویرایش وب‌هوک"}>
        <EmptyState
          icon={WebhookIcon}
          title="دسترسی مجاز نیست"
          description="شما مجوز تغییر وب‌هوک‌ها را ندارید."
          action={
            <Button variant="outline" render={<Link to={returnTo} />}>
              <ArrowRightIcon data-icon="inline-start" />
              بازگشت به اتوماسیون
            </Button>
          }
        />
      </PageShell>
    )
  }

  return (
    <>
      <WebhookForm
        key={record ? `webhook-${record.id}` : "webhook-new"}
        record={record ? { id: record.id, hasSecret: record.hasSecret } : null}
        seed={seed}
        channels={loaderData.channels}
        servers={loaderData.servers}
        localhostAllowed={loaderData.localhostAllowed}
        channelsError={loaderData.channelsError}
        error={
          actionData?.error &&
          (actionData.intent === "create" || actionData.intent === "update")
            ? actionData.error
            : null
        }
        fieldErrors={actionData?.error ? actionData.fieldErrors : undefined}
        pending={submitting}
        testing={submitting && pendingIntent === "test"}
        onTest={
          record
            ? () =>
                submit(
                  { intent: "test", id: String(record.id) },
                  { method: "post" }
                )
            : undefined
        }
        onDirtyChange={markDirty}
        onCancel={cancel}
        onSubmit={(draft) => {
          savingRef.current = true
          submit(
            webhookSubmitFields(draft, record ? "update" : "create", record?.id ?? null),
            { method: "post" }
          )
        }}
      />
    </>
  )
}
