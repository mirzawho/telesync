import {
  Link,
  redirect,
  useNavigate,
  useNavigation,
  useSearchParams,
  useSubmit,
} from "react-router"
import { MoreHorizontalIcon, PlusIcon, WebhookIcon } from "lucide-react"
import * as React from "react"

import { DeliveryStatusBadge, EnabledBadge } from "@/components/automation/status"
import {
  WebhookDialog,
  authHeaderErrorOf,
  urlErrorOf,
  type SourceChannelOption,
  type WebhookFieldErrors,
} from "@/components/automation/webhook-dialog"
import { canManageWebhooks } from "@/components/automation/webhook-access"
import {
  explainWebhookError,
  secretFieldErrorOf,
} from "@/components/automation/webhook-errors"
import { ConfirmDialog } from "@/components/confirm-dialog"
import { faDateTime, faDigits } from "@/components/persian-digits"
import { SearchInput } from "@/components/search-input"
import { EmptyState, ErrorState, LoadingState } from "@/components/states"
import { Badge } from "@workspace/ui/components/badge"
import { Button } from "@workspace/ui/components/button"
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from "@workspace/ui/components/dropdown-menu"
import { useToast } from "@workspace/ui/components/toast"
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@workspace/ui/components/table"

import {
  automationApi,
  automationRoute,
  authModeLabels,
  methodLabels,
  readDeliveryRecord,
  readWebhook,
  readWebhookRecord,
  webhookBody,
  webhookRows,
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
import { PageError, pageMeta, section } from "@/lib/page"
import { PageHeader } from "@/components/page-header"
import { requireSession } from "@/lib/session"
import { requestURL, strip } from "@/path"

import type { Route } from "./+types/automation-webhooks"

const PAGE_SIZE = 20

export const meta = () => pageMeta("وب‌هوک‌ها")

export const handle = section("اتوماسیون")

type LoaderData =
  | {
      status: "ok"
      webhooks: WebhookItem[]
      channels: SourceChannelOption[]
      currentPage: number
      hasMore: boolean
      q: string
      canManage: boolean
      // The editor the URL names, if any. `new` is the create form; a record is
      // the edit form for that webhook. This is what makes direct navigation and
      // a browser refresh open the same editor on the same record.
      editing: WebhookItem | "new" | null
    }
  | { status: "missing" }
  | { status: "forbidden"; message: string }
  | { status: "error"; message: string }

function positiveInt(raw: string | null): number {
  const value = Number(raw)
  return Number.isInteger(value) && value > 0 ? value : 1
}

// `new` is the one non-numeric value of the segment: it means "no webhook yet".
function validID(raw: string | undefined): number | null {
  return raw !== undefined && /^[0-9]+$/.test(raw) ? Number(raw) : null
}

export async function clientLoader({
  params,
  request,
}: Route.LoaderArgs): Promise<LoaderData> {
  const { token, me } = await requireSession(request)
  // A restricted administrator is told so, from the session itself: the page
  // must never fall back to a silently empty list because the calls returned
  // nothing.
  const denied = automationAccessDenied(me)
  if (denied) return { status: "forbidden", message: denied }
  const url = requestURL(request)
  const q = (url.searchParams.get("q") ?? "").trim()
  const currentPage = positiveInt(url.searchParams.get("page"))

  const segment = params.webhookId
  const creating = segment === "new"
  const wantedID = creating ? null : validID(segment)
  if (segment !== undefined && !creating && wantedID === null) {
    return { status: "missing" }
  }

  try {
    const [list, channels] = await Promise.all([
      apiRequest<unknown>("GET", automationApi.webhooks, {
        token,
        query: {
          page: currentPage,
          per_page: PAGE_SIZE,
          q: q || undefined,
        },
      }),
      apiRequest<{ channels?: { id: number; title: string }[] }>(
        "GET",
        "/local/channels",
        { token, query: { page: 1, per_page: 100 } }
      ),
    ])
    const webhooks = webhookRows(list.data).map(readWebhook)

    // The list is one page and may be filtered, so a webhook addressed by its
    // own URL is always read on its own. That is what makes a refresh of
    // /webhooks/42 show the values of 42 whatever page the list is on — and
    // what makes the editor show what the store holds rather than what a row
    // happened to carry.
    let editing: WebhookItem | "new" | null = creating ? "new" : null
    if (wantedID !== null) {
      try {
        editing = readWebhookRecord(
          (await apiRequest<unknown>("GET", automationApi.webhook(wantedID), {
            token,
          })).data
        )
      } catch (error) {
        if (isAuthLost(error)) throw loginRedirect(request)
        if (
          error instanceof Error &&
          "status" in error &&
          (error as { status: number }).status === 404
        ) {
          return { status: "missing" }
        }
        throw error
      }
    }

    return {
      status: "ok",
      webhooks,
      // A webhook subscribes to local managed_channels.id values, so the
      // labels come from the stored channel rows and nowhere else.
      channels: (channels.data?.channels ?? []).map((channel) => ({
        id: channel.id,
        label: channel.title,
      })),
      currentPage,
      hasMore: list.meta.has_more ?? false,
      q,
      canManage: canManageWebhooks(me),
      editing,
    }
  } catch (error) {
    if (isAuthLost(error)) throw loginRedirect(request)
    if (isForbidden(error)) {
      return {
        status: "forbidden",
        message: "شما مجوز مشاهده وب‌هوک‌ها را ندارید.",
      }
    }
    return { status: "error", message: errorMessage(error) }
  }
}

// What one test send reported. It is never a persisted delivery: the service
// writes nothing to the history for a test, so the outcome is shown from this
// response and nowhere else. Only the outcome travels back to the page — never
// the request or the response the exchange carried.
export interface TestOutcome {
  status: DeliveryItem["status"]
  httpStatus: number
  error: string
}

interface ActionData {
  error?: string
  intent?: string
  values?: Partial<WebhookDraft> & { id?: string }
  fieldErrors?: WebhookFieldErrors
  testResult?: TestOutcome
}

const intents = new Set(["create", "update", "delete", "test", "toggle"])

export async function clientAction({
  request,
}: Route.ActionArgs): Promise<ActionData | Response> {
  const { token } = await requireSession(request)
  const form = await request.formData()
  const intent = String(form.get("intent") ?? "")
  const rawId = String(form.get("id") ?? "")
  const id = Number(rawId)

  const values: ActionData["values"] = {
    id: rawId || undefined,
    name: String(form.get("name") ?? ""),
    description: String(form.get("description") ?? ""),
    url: String(form.get("url") ?? ""),
    method: String(form.get("method") ?? "") as WebhookDraft["method"],
    authMode: String(form.get("auth_mode") ?? "") as WebhookDraft["authMode"],
    authHeader: String(form.get("auth_header") ?? ""),
    enabled: form.get("enabled") === "true",
  }
  // A secret posted as an empty hidden field means "do not touch the stored
  // one"; only a non-empty value replaces it.
  const rawSecret = String(form.get("secret") ?? "")
  const clearSecret = form.get("secret_clear") === "true"
  const secret = clearSecret ? "" : rawSecret === "" ? null : rawSecret

  const sourceChannelIds = readIDs(form.get("source_channel_ids"))
  const timeoutMs = readInt(form.get("timeout_ms"), 10_000)
  const maxAttempts = readInt(form.get("max_attempts"), 3)

  if (!intents.has(intent)) {
    return { error: "درخواست نامعتبر است.", intent, values }
  }
  if (intent !== "create" && (!rawId || !Number.isInteger(id) || id <= 0)) {
    return { error: "شناسه وب‌هوک نامعتبر است.", intent, values }
  }

  try {
    if (intent === "delete") {
      await apiRequest("DELETE", automationApi.webhook(id), { token })
      return redirect(strip(automationRoute.webhooks))
    }
    if (intent === "toggle") {
      // The switch changes one field and nothing else: a body carrying only
      // `enabled` leaves the stored secret, the sources and the bounds alone,
      // which is what makes a one-click switch safe to offer in a row.
      await apiRequest("PATCH", automationApi.webhook(id), {
        token,
        body: { enabled: form.get("enabled") === "true" },
      })
      return redirect(
        strip(`${requestURL(request).pathname}${requestURL(request).search}`)
      )
    }
    if (intent === "test") {
      // A test is not a delivery and the service records nothing for it, so the
      // outcome comes back from this one response. Reporting it as history
      // would claim a record the store never wrote.
      const sent = await apiRequest<unknown>(
        "POST",
        automationApi.testWebhook(id),
        { token }
      )
      return {
        intent: "test",
        testResult: testOutcomeOf(readDeliveryRecord(sent.data)),
      }
    }

    const draft: WebhookDraft = {
      name: values.name ?? "",
      description: values.description ?? "",
      enabled: values.enabled ?? true,
      url: values.url ?? "",
      method: values.method ?? "POST",
      authMode: values.authMode ?? "none",
      authHeader: values.authHeader ?? "",
      secret,
      timeoutMs,
      maxAttempts,
      sourceChannelIds,
    }
    const fieldErrors: WebhookFieldErrors = {}
    if (draft.name.trim() === "") fieldErrors.name = "نام الزامی است."
    // The same structural checks the API runs on a save, so an address or a
    // header name it will not accept is refused here rather than travelling and
    // coming back as one.
    const urlProblem = urlErrorOf(draft.url)
    if (urlProblem) fieldErrors.url = urlProblem
    if (draft.authMode === "header") {
      const headerProblem = authHeaderErrorOf(draft.authHeader)
      if (headerProblem) fieldErrors.authHeader = headerProblem
    }
    // The same bounds the API enforces, so an out-of-range number is refused
    // here rather than travelling and coming back as one.
    if (!Number.isInteger(draft.timeoutMs) || draft.timeoutMs < 100 || draft.timeoutMs > 300_000) {
      fieldErrors.timeoutMs = "مهلت باید بین ۱۰۰ تا ۳۰۰۰۰۰ میلی‌ثانیه باشد."
    }
    if (!Number.isInteger(draft.maxAttempts) || draft.maxAttempts < 1 || draft.maxAttempts > 100) {
      fieldErrors.maxAttempts = "تعداد تلاش‌ها باید بین ۱ تا ۱۰۰ باشد."
    }
    if (Object.keys(fieldErrors).length > 0) {
      return {
        error: "برخی فیلدها را کامل کنید.",
        intent,
        values,
        fieldErrors,
      }
    }

    const body = webhookBody(draft)
    // Both writes land on the record's own URL, whose loader reads the stored
    // row back. That is the round-trip made visible: what the editor shows
    // after a save is what the server kept, not what the form last held.
    let savedID = id
    if (intent === "create") {
      const created = await apiRequest<{ webhook?: { id?: number } }>(
        "POST",
        automationApi.webhooks,
        { token, body }
      )
      savedID = Number(created.data?.webhook?.id ?? 0)
    } else {
      await apiRequest("PATCH", automationApi.webhook(id), { token, body })
    }
    if (!Number.isInteger(savedID) || savedID <= 0) {
      // The write succeeded but named no record; the list is the only honest
      // place to land rather than an editor for an id nobody has.
      return redirect(strip(automationRoute.webhooks))
    }
    return redirect(strip(automationRoute.editWebhook(savedID)))
  } catch (error) {
    if (isAuthLost(error)) throw loginRedirect(request)
    if (isForbidden(error)) {
      return { error: "شما مجوز این عمل را ندارید.", intent, values }
    }
    // A refusal is reported as the server reported it: nothing here claims
    // success, and the editor stays open on what was typed. A refusal about the
    // secret goes back onto the field it was typed into, which is where an
    // operator can act on it.
    const secretProblem = secretFieldErrorOf(error, secret !== null)
    return {
      error: explainWebhookError(error),
      intent,
      values,
      fieldErrors: secretProblem ? { secret: secretProblem } : undefined,
    }
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

// The outcome of one test send, read off the exchange the service returned.
// Only the outcome travels on: a test's request and response carry the same
// headers a real one does, and this page never renders them.
function testOutcomeOf(delivery: DeliveryItem): TestOutcome {
  const attempt = delivery.attempts.at(-1)
  return {
    status: delivery.status,
    httpStatus: attempt?.responseStatus ?? delivery.lastHttpStatus,
    error: delivery.lastError || attempt?.errorMessage || "",
  }
}

export function ErrorBoundary({ error }: Route.ErrorBoundaryProps) {
  return (
    <>
      <PageHeader title="وب‌هوک‌ها" />
      <PageError error={error} />
    </>
  )
}

// Physical left, not RTL start: the actions column stays pinned while the
// table scrolls under it, and bg-background keeps text from showing through.
const ACTIONS_HEAD = "sticky left-0 z-20 w-12 bg-background text-end"
const ACTIONS_CELL = "sticky left-0 z-10 bg-background text-end"

// The one button that opens the create form. It is the same action wherever the
// page offers it — in the header of a full list and in the empty state of a
// blank one — so it carries its own name and cannot drift between the two.
export function AddWebhookButton({
  onAdd,
  variant = "default",
  size,
}: {
  onAdd: () => void
  variant?: "default" | "outline"
  size?: "sm"
}) {
  return (
    <Button variant={variant} size={size} onClick={onAdd}>
      <PlusIcon data-icon="inline-start" />
      افزودن وب‌هوک
    </Button>
  )
}

export function WebhooksTable({
  webhooks,
  channels,
  canManage,
  onEdit,
  onToggle,
  onDelete,
  onTest,
}: {
  webhooks: WebhookItem[]
  channels: SourceChannelOption[]
  canManage: boolean
  onEdit: (webhook: WebhookItem) => void
  onToggle: (webhook: WebhookItem) => void
  onDelete: (webhook: WebhookItem) => void
  onTest: (webhook: WebhookItem) => void
}) {
  const labelOf = (id: number) =>
    channels.find((channel) => channel.id === id)?.label ?? `کانال ${id}`
  return (
    <Table>
      <TableHeader>
        <TableRow>
          <TableHead>نام</TableHead>
          <TableHead>کانال‌های مبدأ</TableHead>
          <TableHead>نشانی مقصد</TableHead>
          <TableHead>روش</TableHead>
          <TableHead>شیوه احراز</TableHead>
          <TableHead>وضعیت</TableHead>
          <TableHead>آخرین تحویل</TableHead>
          <TableHead>نتیجه</TableHead>
          <TableHead className={ACTIONS_HEAD}>
            <span className="sr-only">عملیات</span>
          </TableHead>
        </TableRow>
      </TableHeader>
      <TableBody>
        {webhooks.map((webhook) => (
          <TableRow key={webhook.id}>
            <TableCell className="font-medium">
              <Link
                to={automationRoute.deliveries(webhook.id)}
                className="underline-offset-4 hover:underline"
              >
                {webhook.name}
              </Link>
            </TableCell>
            <TableCell className="text-muted-foreground">
              {webhook.sourceChannelIds.length === 0
                ? "—"
                : webhook.sourceChannelIds.map(labelOf).join("، ")}
            </TableCell>
            <TableCell className="max-w-56">
              <span
                dir="ltr"
                className="block truncate text-start text-xs"
                title={webhook.url}
              >
                {webhook.url || "—"}
              </span>
            </TableCell>
            <TableCell>
              <span dir="ltr" className="text-xs font-medium">
                {methodLabels[webhook.method]}
              </span>
            </TableCell>
            <TableCell className="text-muted-foreground whitespace-nowrap">
              {authModeLabels[webhook.authMode]}
            </TableCell>
            <TableCell>
              <EnabledBadge enabled={webhook.enabled} />
            </TableCell>
            <TableCell>
              {webhook.lastDelivery ? (
                <span className="flex flex-col gap-0.5">
                  <DeliveryStatusBadge status={webhook.lastDelivery.status} />
                  <span className="text-xs text-muted-foreground">
                    {webhook.lastDelivery.httpStatus > 0 ? (
                      <span dir="ltr">HTTP {webhook.lastDelivery.httpStatus}</span>
                    ) : (
                      "—"
                    )}
                    {" · "}
                    {faDateTime(webhook.lastDelivery.at)}
                  </span>
                </span>
              ) : (
                <span className="text-xs text-muted-foreground">—</span>
              )}
            </TableCell>
            <TableCell>
              {webhook.summary ? (
                <span className="flex flex-wrap gap-1">
                  <Badge variant="success">{faDigits(webhook.summary.succeeded)}</Badge>
                  <Badge variant="destructive">{faDigits(webhook.summary.failed)}</Badge>
                  <Badge variant="muted">{faDigits(webhook.summary.pending)}</Badge>
                </span>
              ) : (
                <span className="text-xs text-muted-foreground">—</span>
              )}
            </TableCell>
            <TableCell className={ACTIONS_CELL}>
              <DropdownMenu>
                <DropdownMenuTrigger
                  render={
                    <Button
                      variant="ghost"
                      size="icon-sm"
                      aria-label={`عملیات وب‌هوک ${webhook.name}`}
                    />
                  }
                >
                  <MoreHorizontalIcon />
                </DropdownMenuTrigger>
                <DropdownMenuContent align="end">
                  {/* Reading the history is a read and stays available to anyone
                      who may see the automation area. Every other row action is
                      a write, and a write here needs webhooks.manage. */}
                  <DropdownMenuItem
                    render={
                      <Link to={automationRoute.deliveries(webhook.id)} />
                    }
                  >
                    تاریخچه تحویل‌ها
                  </DropdownMenuItem>
                  {canManage && (
                    <>
                      <DropdownMenuItem onClick={() => onEdit(webhook)}>
                        ویرایش
                      </DropdownMenuItem>
                      <DropdownMenuItem onClick={() => onToggle(webhook)}>
                        {webhook.enabled ? "غیرفعال‌سازی" : "فعال‌سازی"}
                      </DropdownMenuItem>
                      <DropdownMenuItem onClick={() => onTest(webhook)}>
                        ارسال آزمایشی
                      </DropdownMenuItem>
                      <DropdownMenuSeparator />
                      <DropdownMenuItem
                        variant="destructive"
                        onClick={() => onDelete(webhook)}
                      >
                        حذف
                      </DropdownMenuItem>
                    </>
                  )}
                </DropdownMenuContent>
              </DropdownMenu>
            </TableCell>
          </TableRow>
        ))}
      </TableBody>
    </Table>
  )
}

export default function AutomationWebhooks({
  loaderData,
  actionData,
}: Route.ComponentProps) {
  const navigation = useNavigation()
  const { toast } = useToast()
  const submit = useSubmit()
  const navigate = useNavigate()
  const [searchParams, setSearchParams] = useSearchParams()
  const loading = navigation.state === "loading"
  const submitting = navigation.state === "submitting"

  // The editor is opened by the URL, not by local state: /webhooks/new is the
  // create form and /webhooks/:id is the edit form for that record, so a direct
  // navigation and a browser refresh land on the same editor with the same
  // values. The list's own buttons only navigate to those URLs.
  const editing = loaderData.status === "ok" ? loaderData.editing : null
  const [deleting, setDeleting] = React.useState<WebhookItem | null>(null)
  const [q, setQ] = React.useState(
    loaderData.status === "ok" ? loaderData.q : ""
  )

  React.useEffect(() => {
    if (loaderData.status === "ok") setQ(loaderData.q)
  }, [loaderData])

  // A successful action answers with a redirect, so the finished intent (read
  // from the submit that just left) is what names the result for the toast.
  // Nothing here claims success on a refusal: an action that failed returns
  // data carrying an error and never redirects.
  const pendingIntent = React.useRef<{
    intent: string
    enabled: boolean
  } | null>(null)
  React.useEffect(() => {
    const formData = navigation.formData
    if (formData) {
      pendingIntent.current = {
        intent: String(formData.get("intent") ?? ""),
        enabled: formData.get("enabled") === "true",
      }
    }
  }, [navigation.formData])

  const wasSubmitting = React.useRef(false)
  React.useEffect(() => {
    if (wasSubmitting.current && !submitting && !actionData?.error) {
      setDeleting(null)
      const pending = pendingIntent.current
      const intent = pending?.intent
      if (intent === "create") toast({ variant: "success", title: "وب‌هوک ساخته شد." })
      else if (intent === "update")
        toast({ variant: "success", title: "تغییرات وب‌هوک ذخیره شد." })
      else if (intent === "delete") toast({ variant: "success", title: "وب‌هوک حذف شد." })
      else if (intent === "toggle")
        toast({
          variant: "success",
          title: pending?.enabled ? "وب‌هوک فعال شد." : "وب‌هوک غیرفعال شد.",
        })
      else if (intent === "test") {
        // A test is reported as the exchange reported it. It is never a
        // delivery: the service writes nothing to the history for one, so the
        // outcome is the only thing here that may be called a result.
        const result = actionData?.testResult
        const answered = (result?.httpStatus ?? 0) > 0
        toast({
          variant: result?.status === "succeeded" ? "success" : "error",
          title:
            result?.status === "succeeded"
              ? "درخواست آزمایشی پذیرفته شد."
              : "درخواست آزمایشی ناموفق بود.",
          description: [
            answered ? `HTTP ${result?.httpStatus}` : "بدون پاسخ",
            result?.error || "",
            "این آزمایش در تاریخچه تحویل‌ها ثبت نمی‌شود.",
          ]
            .filter(Boolean)
            .join(" — "),
        })
      }
      pendingIntent.current = null
    }
    wasSubmitting.current = submitting
  }, [submitting, actionData, toast])

  const toastedError = React.useRef<string | null>(null)
  React.useEffect(() => {
    if (!actionData?.error) return
    const key = `${actionData.intent ?? ""}:${actionData.error}`
    if (toastedError.current === key) return
    toastedError.current = key
    toast({
      variant: "error",
      title: "عملیات انجام نشد.",
      description: actionData.error,
    })
  }, [actionData, toast])

  // A returned error keeps the editor open on what was typed: the URL still
  // names the record (or `new`) and the action's values are passed back in.
  const onSearch = (event: React.FormEvent<HTMLFormElement>) => {
    event.preventDefault()
    const next = new URLSearchParams(searchParams)
    if (q.trim()) next.set("q", q.trim())
    else next.delete("q")
    next.delete("page")
    setSearchParams(next)
  }

  const formError =
    editing !== null &&
    actionData?.error &&
    (editing === "new"
      ? actionData.intent === "create"
      : actionData.intent === "update")
      ? actionData.error
      : null
  const deleteError =
    deleting && actionData?.intent === "delete" ? actionData.error : null

  return (
    <div className="flex flex-col gap-6">
      <PageHeader
        title="وب‌هوک‌ها"
        description="رویدادهای کانال‌های مبدأ را به نشانی مقصد بفرستید و هر درخواست و پاسخ را بازبینی کنید."
        actions={
          loaderData.status === "ok" && loaderData.canManage ? (
            <AddWebhookButton onAdd={() => navigate(automationRoute.newWebhook)} />
          ) : null
        }
      />

      {loaderData.status === "missing" ? (
        <EmptyState
          icon={WebhookIcon}
          title="وب‌هوک پیدا نشد"
          description="شناسه در نشانی معتبر نیست، یا این وب‌هوک حذف شده است."
          action={
            <Button
              variant="outline"
              render={<Link to={automationRoute.webhooks} />}
            >
              بازگشت به وب‌هوک‌ها
            </Button>
          }
        />
      ) : loaderData.status !== "ok" ? (
        <ErrorState
          title={loaderData.status === "forbidden" ? "دسترسی مجاز نیست" : undefined}
          message={loaderData.message}
          onRetry={() => window.location.reload()}
        />
      ) : (
        <>
          <form onSubmit={onSearch} className="flex flex-wrap items-center gap-2" role="search">
            <SearchInput
              name="q"
              value={q}
              onChange={setQ}
              placeholder="جستجوی وب‌هوک…"
              className="w-full sm:w-72"
            />
            {loaderData.q && (
              <Button
                type="button"
                variant="outline"
                size="sm"
                onClick={() => {
                  setQ("")
                  setSearchParams({})
                }}
              >
                پاک کردن فیلترها
              </Button>
            )}
          </form>

          <div className="rounded-xl border bg-card">
            {loading ? (
              <LoadingState label="در حال بارگذاری وب‌هوک‌ها…" />
            ) : loaderData.webhooks.length === 0 ? (
              <EmptyState
                icon={WebhookIcon}
                title={loaderData.q ? "نتیجه‌ای پیدا نشد" : "هنوز وب‌هوکی وجود ندارد"}
                description={
                  loaderData.q
                    ? "عبارت جستجو را تغییر دهید یا حذف کنید."
                    : "اولین وب‌هوک را بسازید تا رویدادها به بیرون فرستاده شوند."
                }
                action={
                  !loaderData.q && loaderData.canManage ? (
                    <AddWebhookButton
                      variant="outline"
                      size="sm"
                      onAdd={() => navigate(automationRoute.newWebhook)}
                    />
                  ) : undefined
                }
              />
            ) : (
              <WebhooksTable
                webhooks={loaderData.webhooks}
                channels={loaderData.channels}
                canManage={loaderData.canManage}
                onEdit={(webhook) =>
                  navigate(automationRoute.editWebhook(webhook.id))
                }
                onToggle={(webhook) =>
                  submit(
                    {
                      intent: "toggle",
                      id: String(webhook.id),
                      enabled: String(!webhook.enabled),
                    },
                    { method: "post" }
                  )
                }
                onDelete={setDeleting}
                onTest={(webhook) =>
                  submit(
                    { intent: "test", id: String(webhook.id) },
                    { method: "post" }
                  )
                }
              />
            )}
          </div>

          <nav className="flex items-center justify-between" aria-label="صفحه‌بندی">
            <p className="text-xs text-muted-foreground">
              صفحه {faDigits(loaderData.currentPage)}
            </p>
            <div className="flex items-center gap-2">
              <PageLink
                params={searchParams}
                target={loaderData.currentPage - 1}
                enabled={loaderData.currentPage > 1 && !loading}
              >
                صفحه قبل
              </PageLink>
              <PageLink
                params={searchParams}
                target={loaderData.currentPage + 1}
                enabled={loaderData.hasMore && !loading}
              >
                صفحه بعد
              </PageLink>
            </div>
          </nav>
        </>
      )}

      {editing !== null && loaderData.status === "ok" && (
        <WebhookDialog
          key={editing === "new" ? "new" : editing.id}
          webhook={editing === "new" ? null : editing}
          channels={loaderData.channels}
          error={formError}
          fieldErrors={actionData?.error ? actionData.fieldErrors : undefined}
          pending={submitting}
          defaultValues={actionData?.values}
          onClose={() => navigate(automationRoute.webhooks)}
          onSubmit={(draft) => {
            const intent = editing === "new" ? "create" : "update"
            submit(
              {
                intent,
                id: editing === "new" ? "" : String(editing.id),
                name: draft.name,
                description: draft.description,
                enabled: String(draft.enabled),
                url: draft.url,
                method: draft.method,
                auth_mode: draft.authMode,
                auth_header: draft.authHeader,
                // null posts nothing at all: the stored secret stays as it is.
                secret: draft.secret ?? "",
                secret_clear: draft.secret === "" ? "true" : "false",
                source_channel_ids: JSON.stringify(draft.sourceChannelIds),
                timeout_ms: String(draft.timeoutMs),
                max_attempts: String(draft.maxAttempts),
              },
              { method: "post" }
            )
          }}
        />
      )}

      <ConfirmDialog
        open={deleting !== null}
        onOpenChange={(open) => !open && setDeleting(null)}
        title="حذف وب‌هوک"
        description={
          deleting
            ? `وب‌هوک «${deleting.name}» حذف شود؟ تاریخچه تحویل‌های آن هم پاک می‌شود و قابل بازگرداندن نیست.`
            : ""
        }
        confirmLabel="حذف"
        pending={submitting}
        error={deleteError}
        onConfirm={() => {
          if (!deleting) return
          submit({ intent: "delete", id: String(deleting.id) }, { method: "post" })
        }}
      />
    </div>
  )
}

function PageLink({
  params,
  target,
  enabled,
  children,
}: {
  params: URLSearchParams
  target: number
  enabled: boolean
  children: React.ReactNode
}) {
  const next = new URLSearchParams(params)
  if (target <= 1) next.delete("page")
  else next.set("page", String(target))
  const query = next.toString()
  const to = query ? `?${query}` : "?"
  return (
    <Button
      variant="outline"
      size="sm"
      disabled={!enabled}
      render={enabled ? <Link to={to} preventScrollReset /> : undefined}
    >
      {children}
    </Button>
  )
}
