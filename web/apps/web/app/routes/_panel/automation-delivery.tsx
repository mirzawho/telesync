import {
  Link,
  redirect,
  useNavigation,
  useSubmit,
} from "react-router"
import { ArrowRightIcon, InboxIcon, RotateCcwIcon } from "lucide-react"
import * as React from "react"

import {
  CodeBlock,
  CopyButton,
  DeliveryStatusBadge,
  HeaderBlock,
} from "@/components/automation/status"
import { canManageWebhooks } from "@/components/automation/webhook-access"
import { explainWebhookError } from "@/components/automation/webhook-errors"
import { faDateTime, faDigits } from "@/components/persian-digits"
import { EmptyState, ErrorState, LoadingState } from "@/components/states"
import { Badge } from "@workspace/ui/components/badge"
import { Button } from "@workspace/ui/components/button"
import {
  Tabs,
  TabsContent,
  TabsList,
  TabsTrigger,
} from "@workspace/ui/components/tabs"
import { useToast } from "@workspace/ui/components/toast"

import {
  automationApi,
  automationRoute,
  bodyView,
  canRetryDelivery,
  deliveryStatusLabels,
  readDeliveryRecord,
  readWebhookRecord,
  type DeliveryAttempt,
  type DeliveryItem,
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

import type { Route } from "./+types/automation-delivery"

export const meta = () => pageMeta("جزئیات تحویل")

export const handle = section("اتوماسیون")

type LoaderData =
  | {
      status: "ok"
      webhookID: number
      webhookName: string
      delivery: DeliveryItem
      // Whether this caller may write here at all. A retry is a write: the API
      // asks for webhooks.manage before it reschedules anything.
      canManage: boolean
    }
  | { status: "missing" }
  | { status: "forbidden"; message: string }
  | { status: "error"; message: string }

function validID(raw: string | undefined): number | null {
  return raw !== undefined && /^[0-9]+$/.test(raw) ? Number(raw) : null
}

export async function clientLoader({
  params,
  request,
}: Route.LoaderArgs): Promise<LoaderData> {
  const { token, me } = await requireSession(request)
  // A restricted administrator is told so from the session itself: this page
  // must never fall back to a silently empty record because the calls returned
  // nothing.
  const denied = automationAccessDenied(me)
  if (denied) return { status: "forbidden", message: denied }
  const webhookID = validID(params.webhookId)
  const deliveryID = validID(params.deliveryId)
  if (webhookID === null || deliveryID === null) return { status: "missing" }

  try {
    const delivery = readDeliveryRecord(
      (await apiRequest<unknown>(
        "GET",
        automationApi.delivery(webhookID, deliveryID),
        { token }
      )).data
    )
    // The webhook's own name is what the header shows; a delivery is always
    // read through its webhook, never on its own.
    let webhookName = ""
    try {
      const webhook = await apiRequest<unknown>(
        "GET",
        automationApi.webhook(webhookID),
        { token }
      )
      webhookName = readWebhookRecord(webhook.data).name
    } catch {
      // A webhook that vanished leaves the delivery readable under its id.
      webhookName = `#${webhookID}`
    }
    return {
      status: "ok",
      webhookID,
      webhookName,
      delivery,
      canManage: canManageWebhooks(me),
    }
  } catch (error) {
    if (isAuthLost(error)) throw loginRedirect(request)
    if (
      error instanceof Error &&
      "status" in error &&
      (error as { status: number }).status === 404
    ) {
      return { status: "missing" }
    }
    if (isForbidden(error)) {
      return {
        status: "forbidden",
        message: "شما مجوز مشاهده این تحویل را ندارید.",
      }
    }
    return { status: "error", message: errorMessage(error) }
  }
}

interface ActionData {
  error?: string
  intent?: string
}

export async function clientAction({
  request,
  params,
}: Route.ActionArgs): Promise<ActionData | Response> {
  const { token } = await requireSession(request)
  const webhookID = validID(params.webhookId)
  const deliveryID = validID(params.deliveryId)
  const form = await request.formData()
  const intent = String(form.get("intent") ?? "")
  if (webhookID === null || deliveryID === null) {
    return { error: "شناسه نامعتبر است.", intent }
  }
  if (intent !== "retry") {
    return { error: "درخواست نامعتبر است.", intent }
  }
  try {
    await apiRequest(
      "POST",
      automationApi.retryDelivery(webhookID, deliveryID),
      { token }
    )
    return redirect(
      strip(`${requestURL(request).pathname}${requestURL(request).search}`)
    )
  } catch (error) {
    if (isAuthLost(error)) throw loginRedirect(request)
    if (isForbidden(error)) {
      return { error: "شما مجوز تلاش دوباره را ندارید.", intent }
    }
    return { error: explainWebhookError(error), intent }
  }
}

export function ErrorBoundary({ error }: Route.ErrorBoundaryProps) {
  return (
    <>
      <PageHeader title="جزئیات تحویل" />
      <PageError error={error} />
    </>
  )
}

// Whether this screen offers a manual retry at all. Both conditions must hold:
// the delivery has to be an eligible terminal failure — anything still in
// flight, already delivered or cancelled is left alone, since a retry there
// would either duplicate work or undo an operator's cancellation — and the
// caller has to be one the API will let reschedule it, which is webhooks.manage
// and not merely the right to read the history.
export function retryOffered(
  delivery: Pick<DeliveryItem, "status">,
  canManage: boolean
): boolean {
  return canRetryDelivery(delivery) && canManage
}

// The delivery page's own actions. Exported so the rule above can be proved
// against the button it decides, rather than against the rule alone.
export function DeliveryActions({
  history,
  retryable,
  submitting,
  onRetry,
}: {
  history: string
  retryable: boolean
  submitting: boolean
  onRetry: () => void
}) {
  return (
    <>
      <Button variant="outline" render={<Link to={history} />}>
        <ArrowRightIcon data-icon="inline-start" />
        تاریخچه تحویل‌ها
      </Button>
      {retryable && (
        <Button onClick={onRetry} disabled={submitting}>
          <RotateCcwIcon data-icon="inline-start" />
          {submitting ? "در حال ارسال…" : "تلاش دوباره"}
        </Button>
      )}
    </>
  )
}

export default function AutomationDelivery({
  loaderData,
  actionData,
}: Route.ComponentProps) {
  const navigation = useNavigation()
  const submit = useSubmit()
  const { toast } = useToast()
  const submitting = navigation.state === "submitting"

  const pendingIntent = React.useRef<string | null>(null)
  React.useEffect(() => {
    const formData = navigation.formData
    if (formData) pendingIntent.current = String(formData.get("intent") ?? "")
  }, [navigation.formData])

  const wasSubmitting = React.useRef(false)
  React.useEffect(() => {
    if (wasSubmitting.current && !submitting && !actionData?.error) {
      if (pendingIntent.current === "retry") {
        toast({
          variant: "success",
          title: "تحویل دوباره در صف قرار گرفت.",
          description: "نتیجه تلاش تازه در همین صفحه ثبت می‌شود.",
        })
      }
      pendingIntent.current = null
    }
    wasSubmitting.current = submitting
  }, [submitting, actionData, toast])

  React.useEffect(() => {
    if (!actionData?.error) return
    toast({
      variant: "error",
      title: "تلاش دوباره انجام نشد.",
      description: actionData.error,
    })
  }, [actionData, toast])

  if (loaderData.status === "missing") {
    return (
      <div className="flex flex-col gap-6">
        <PageHeader title="جزئیات تحویل" />
        <EmptyState
          icon={InboxIcon}
          title="تحویل پیدا نشد"
          description="شناسه در نشانی معتبر نیست، یا این رکورد حذف شده است."
          action={
            <Button
              variant="outline"
              render={<Link to={automationRoute.webhooks} />}
            >
              <ArrowRightIcon />
              بازگشت به وب‌هوک‌ها
            </Button>
          }
        />
      </div>
    )
  }

  if (loaderData.status !== "ok") {
    return (
      <div className="flex flex-col gap-6">
        <PageHeader title="جزئیات تحویل" />
        <ErrorState
          title={loaderData.status === "forbidden" ? "دسترسی مجاز نیست" : undefined}
          message={loaderData.message}
          onRetry={() => window.location.reload()}
        />
      </div>
    )
  }

  const { delivery, webhookID, webhookName, canManage } = loaderData
  const history = automationRoute.deliveries(webhookID)
  const retryable = retryOffered(delivery, canManage)

  return (
    <div className="flex flex-col gap-6">
      <PageHeader
        title={`تحویل ${delivery.id}`}
        description={
          webhookName
            ? `وب‌هوک «${webhookName}» — ${deliveryStatusLabels[delivery.status]}`
            : deliveryStatusLabels[delivery.status]
        }
        actions={
          <DeliveryActions
            history={history}
            retryable={retryable}
            submitting={submitting}
            onRetry={() => submit({ intent: "retry" }, { method: "post" })}
          />
        }
      />

      {actionData?.error && (
        <p
          role="alert"
          className="rounded-lg bg-destructive/10 px-3 py-2 text-sm text-destructive"
        >
          {actionData.error}
        </p>
      )}

      <DeliverySummary delivery={delivery} />

      <DeliveryHistory delivery={delivery} />
    </div>
  )
}

// The three tabs of one delivery: the request that was sent, the response that
// came back, and every attempt in between. Each reads straight off the
// persisted record — nothing here is filled in, rounded or reconstructed.
export function DeliveryHistory({ delivery }: { delivery: DeliveryItem }) {
  const lastAttempt = delivery.attempts.at(-1)
  return (
    <Tabs defaultValue="request">
      <TabsList className="w-full">
        <TabsTrigger value="request" className="flex-1">
          درخواست
        </TabsTrigger>
        <TabsTrigger value="response" className="flex-1">
          پاسخ
        </TabsTrigger>
        <TabsTrigger value="attempts" className="flex-1">
          تلاش‌ها
        </TabsTrigger>
      </TabsList>

      <TabsContent value="request" className="mt-4 flex flex-col gap-4">
        {delivery.request ? (
          <>
            <div className="flex flex-wrap items-center gap-2">
              <Badge variant="outline">
                <span dir="ltr">{delivery.request.method}</span>
              </Badge>
              <span dir="ltr" className="text-sm break-all">
                {delivery.request.url}
              </span>
            </div>
            <HeaderBlock headers={delivery.request.headers} />
            <CodeBlock
              label="بدنه درخواست"
              text={bodyView(delivery.request).text}
              truncated={delivery.request.truncated}
              emptyText="بدنه‌ای ارسال نشده است."
            />
          </>
        ) : (
          <EmptyState
            icon={InboxIcon}
            title="درخواستی ثبت نشده است"
            description="هنوز تلاشی برای این تحویل انجام نشده است."
          />
        )}
      </TabsContent>

      <TabsContent value="response" className="mt-4 flex flex-col gap-4">
        {lastAttempt ? (
          <AttemptResponse attempt={lastAttempt} />
        ) : (
          <EmptyState
            icon={InboxIcon}
            title="پاسخی ثبت نشده است"
            description="این تحویل هنوز پاسخی دریافت نکرده است."
          />
        )}
      </TabsContent>

      <TabsContent value="attempts" className="mt-4 flex flex-col gap-4">
        {delivery.attempts.length === 0 ? (
          <EmptyState
            icon={InboxIcon}
            title="تلاشی ثبت نشده است"
            description="رکورد تلاش‌ها پس از اولین ارسال این‌جا می‌آید."
          />
        ) : (
          <ol className="flex flex-col gap-4">
            {delivery.attempts.map((attempt) => (
              <li key={attempt.id}>
                <AttemptCard attempt={attempt} />
              </li>
            ))}
          </ol>
        )}
      </TabsContent>
    </Tabs>
  )
}

// Exported for the tests that prove the history renders what was stored. These
// are the record views: they take a DeliveryItem and draw it, nothing else.
export function DeliverySummary({ delivery }: { delivery: DeliveryItem }) {
  const lastAttempt = delivery.attempts.at(-1)
  const totalMs = delivery.attempts.reduce(
    (sum, attempt) => sum + attempt.durationMs,
    0
  )
  return (
    <dl className="grid grid-cols-2 gap-2 rounded-xl border p-4 sm:grid-cols-3 lg:grid-cols-4">
      <Summary label="وضعیت">
        <DeliveryStatusBadge status={delivery.status} />
      </Summary>
      <Summary label="شناسه تحویل">
        <span dir="ltr" className="text-sm">
          {delivery.id}
        </span>
      </Summary>
      <Summary label="شناسه رویداد">
        <span dir="ltr" className="text-sm break-all">
          {delivery.eventID || "—"}
        </span>
      </Summary>
      <Summary label="کد HTTP">
        <span dir="ltr" className="text-sm">
          {delivery.lastHttpStatus > 0 ? delivery.lastHttpStatus : "—"}
        </span>
      </Summary>
      <Summary label="روش">
        <span dir="ltr" className="text-sm">
          {delivery.request?.method || "—"}
        </span>
      </Summary>
      <Summary label="نشانی مقصد">
        <span dir="ltr" className="text-sm break-all">
          {delivery.request?.url || "—"}
        </span>
      </Summary>
      <Summary label="زمان ثبت">{faDateTime(delivery.createdAt)}</Summary>
      <Summary label="زمان پایان">{faDateTime(delivery.completedAt)}</Summary>
      <Summary label="تعداد تلاش">{faDigits(delivery.attemptCount)}</Summary>
      <Summary label="مدت کل">
        {totalMs > 0 ? `${faDigits(totalMs)} میلی‌ثانیه` : "—"}
      </Summary>
      <Summary label="مدت آخرین تلاش">
        {lastAttempt && lastAttempt.durationMs > 0
          ? `${faDigits(lastAttempt.durationMs)} میلی‌ثانیه`
          : "—"}
      </Summary>
      <Summary label="تلاش بعدی">
        {delivery.nextAttemptAt ? faDateTime(delivery.nextAttemptAt) : "—"}
      </Summary>
      <Summary label="خطا">
        <span dir="auto" className="text-sm break-words">
          {delivery.lastError || "—"}
        </span>
      </Summary>
    </dl>
  )
}

function Summary({
  label,
  children,
}: {
  label: string
  children: React.ReactNode
}) {
  return (
    <div className="flex min-w-0 flex-col gap-1">
      <dt className="text-xs text-muted-foreground">{label}</dt>
      <dd className="min-w-0">{children}</dd>
    </div>
  )
}

function AttemptResponse({ attempt }: { attempt: DeliveryAttempt }) {
  const body = bodyView(attempt.response)
  return (
    <>
      <div className="flex flex-wrap items-center gap-2">
        <Badge
          variant={
            attempt.responseStatus >= 200 && attempt.responseStatus < 300
              ? "success"
              : attempt.responseStatus > 0
                ? "destructive"
                : "muted"
          }
        >
          <span dir="ltr">
            {attempt.responseStatus > 0
              ? `HTTP ${attempt.responseStatus}`
              : "بدون پاسخ"}
          </span>
        </Badge>
        <span className="text-xs text-muted-foreground">
          تلاش {faDigits(attempt.attemptNumber)}
        </span>
      </div>
      <HeaderBlock headers={attempt.response.headers} label="هدرهای پاسخ" />
      <CodeBlock
        label="بدنه پاسخ"
        text={body.text}
        truncated={attempt.response.truncated}
        emptyText="بدنه‌ای دریافت نشده است."
      />
    </>
  )
}

// One persisted HTTP exchange, exactly as stored: the redaction markers and the
// truncation flags the server wrote are shown, never undone.
export function AttemptCard({ attempt }: { attempt: DeliveryAttempt }) {
  const requestBody = bodyView(attempt.request)
  const responseBody = bodyView(attempt.response)
  return (
    <section className="flex flex-col gap-3 rounded-xl border p-4">
      <header className="flex flex-wrap items-center gap-2">
        <Badge variant="outline">
          <span dir="ltr">#{attempt.attemptNumber}</span>
        </Badge>
        <span dir="ltr" className="text-xs text-muted-foreground">
          {attempt.requestMethod}
        </span>
        <span className="text-xs text-muted-foreground">
          {faDateTime(attempt.startedAt)}
          {attempt.finishedAt ? ` — ${faDateTime(attempt.finishedAt)}` : ""}
        </span>
        <span className="ms-auto inline-flex items-center gap-2">
          {attempt.durationMs > 0 && (
            <span className="text-xs text-muted-foreground">
              {faDigits(attempt.durationMs)} میلی‌ثانیه
            </span>
          )}
          <Badge
            variant={
              attempt.responseStatus >= 200 && attempt.responseStatus < 300
                ? "success"
                : attempt.responseStatus > 0
                  ? "destructive"
                  : "muted"
            }
          >
            <span dir="ltr">
              {attempt.responseStatus > 0
                ? `HTTP ${attempt.responseStatus}`
                : "بدون پاسخ"}
            </span>
          </Badge>
        </span>
      </header>

      {attempt.errorCode && (
        <p
          role="alert"
          className="rounded-lg bg-destructive/10 px-3 py-2 text-sm text-destructive"
        >
          <span dir="ltr">{attempt.errorCode}</span>
          {attempt.errorMessage ? ` — ${attempt.errorMessage}` : ""}
        </p>
      )}

      <div className="flex flex-col gap-2">
        <div className="flex flex-wrap items-center gap-2">
          <h4 className="text-xs font-medium text-muted-foreground">
            نشانی درخواست
          </h4>
          <CopyButton value={attempt.requestUrl} label="کپی نشانی" />
        </div>
        <p dir="ltr" className="rounded-lg border px-3 py-2 text-start text-xs break-all">
          {attempt.requestUrl}
        </p>
      </div>

      <HeaderBlock headers={attempt.request.headers} label="هدرهای درخواست" />
      <CodeBlock
        label="بدنه درخواست"
        text={requestBody.text}
        truncated={attempt.request.truncated}
        emptyText="بدنه‌ای ارسال نشده است."
      />
      <HeaderBlock headers={attempt.response.headers} label="هدرهای پاسخ" />
      <CodeBlock
        label="بدنه پاسخ"
        text={responseBody.text}
        truncated={attempt.response.truncated}
        emptyText="بدنه‌ای دریافت نشده است."
      />
      <p className="text-xs text-muted-foreground">
        {attempt.responseBytes > 0
          ? `${faDigits(attempt.responseBytes)} بایت پاسخ`
          : "اندازه پاسخ ثبت نشده است."}
      </p>
    </section>
  )
}
