import { Link, useNavigation, useSearchParams } from "react-router"
import { ArrowRightIcon, InboxIcon, RotateCcwIcon } from "lucide-react"
import * as React from "react"

import { DeliveryStatusBadge } from "@/components/automation/status"
import { faDateTime, faDigits } from "@/components/persian-digits"
import { EmptyState, ErrorState, LoadingState } from "@/components/states"
import { Button } from "@workspace/ui/components/button"
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
  deliveryStatusLabels,
  deliveryRows,
  readDelivery,
  readWebhookRecord,
  type DeliveryItem,
  type DeliveryStatus,
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

import type { Route } from "./+types/automation-deliveries"

const PAGE_SIZE = 20

export const meta = () => pageMeta("تاریخچه تحویل‌ها")

export const handle = section("اتوماسیون")

const ACTIONS_HEAD = "sticky left-0 z-20 w-12 bg-background text-end"
const ACTIONS_CELL = "sticky left-0 z-10 bg-background text-end"

const statusFilters: (DeliveryStatus | "all")[] = [
  "all",
  "succeeded",
  "failed",
  "pending",
  "delivering",
  "cancelled",
]

type LoaderData =
  | {
      status: "ok"
      webhook: WebhookItem
      deliveries: DeliveryItem[]
      currentPage: number
      hasMore: boolean
      filter: DeliveryStatus | "all"
    }
  | { status: "missing" }
  | { status: "forbidden"; message: string }
  | { status: "error"; message: string }

function positiveInt(raw: string | null): number {
  const value = Number(raw)
  return Number.isInteger(value) && value > 0 ? value : 1
}

function validID(raw: string | undefined): number | null {
  return raw !== undefined && /^[0-9]+$/.test(raw) ? Number(raw) : null
}

// How long one delivery took, from the moment its event arrived to the moment
// the delivery finished. It is computed from the two timestamps the store keeps
// and nothing else: a list row carries no per-attempt timings, so an HTTP
// duration is not one of the numbers this page may claim. A delivery still going
// has no duration yet, and a clock that runs backwards is reported as unknown
// rather than as a negative number.
export function deliveryDurationMs(
  delivery: Pick<DeliveryItem, "createdAt" | "completedAt">
): number | null {
  if (!delivery.completedAt) return null
  const end = new Date(delivery.completedAt).getTime()
  const start = new Date(delivery.createdAt).getTime()
  if (!Number.isFinite(end) || !Number.isFinite(start) || end < start) return null
  return end - start
}

export async function clientLoader({
  params,
  request,
}: Route.LoaderArgs): Promise<LoaderData> {
  const { token, me } = await requireSession(request)
  // A restricted administrator is told so from the session itself: this page
  // must never fall back to a silently empty history because the calls
  // returned nothing.
  const denied = automationAccessDenied(me)
  if (denied) return { status: "forbidden", message: denied }
  const url = requestURL(request)
  const id = validID(params.webhookId)
  if (id === null) return { status: "missing" }

  const rawFilter = url.searchParams.get("status") ?? "all"
  const filter = (
    statusFilters.includes(rawFilter as DeliveryStatus | "all")
      ? rawFilter
      : "all"
  ) as DeliveryStatus | "all"
  const currentPage = positiveInt(url.searchParams.get("page"))

  try {
    const [webhook, page] = await Promise.all([
      apiRequest<unknown>("GET", automationApi.webhook(id), { token }),
      apiRequest<unknown>("GET", automationApi.deliveries(id), {
        token,
        query: {
          page: currentPage,
          per_page: PAGE_SIZE,
          status: filter === "all" ? undefined : filter,
        },
      }),
    ])
    return {
      status: "ok",
      webhook: readWebhookRecord(webhook.data),
      deliveries: deliveryRows(page.data).map(readDelivery),
      currentPage,
      hasMore: page.meta.has_more ?? false,
      filter,
    }
  } catch (error) {
    if (isAuthLost(error)) throw loginRedirect(request)
    if (error instanceof Error && "status" in error && (error as { status: number }).status === 404) {
      return { status: "missing" }
    }
    if (isForbidden(error)) {
      return {
        status: "forbidden",
        message: "شما مجوز مشاهده تاریخچه تحویل‌ها را ندارید.",
      }
    }
    return { status: "error", message: errorMessage(error) }
  }
}

export function ErrorBoundary({ error }: Route.ErrorBoundaryProps) {
  return (
    <>
      <PageHeader title="تاریخچه تحویل‌ها" />
      <PageError error={error} />
    </>
  )
}

export function DeliveriesTable({
  deliveries,
  webhookID,
}: {
  deliveries: DeliveryItem[]
  webhookID: number
}) {
  return (
    <Table>
      <TableHeader>
        <TableRow>
          <TableHead>وضعیت</TableHead>
          <TableHead>شناسه تحویل</TableHead>
          <TableHead>رویداد</TableHead>
          <TableHead>کد HTTP</TableHead>
          <TableHead>زمان</TableHead>
          <TableHead>مدت</TableHead>
          <TableHead>تلاش‌ها</TableHead>
          <TableHead>خطا</TableHead>
          <TableHead className={ACTIONS_HEAD}>
            <span className="sr-only">عملیات</span>
          </TableHead>
        </TableRow>
      </TableHeader>
      <TableBody>
        {deliveries.map((delivery) => {
          const duration = deliveryDurationMs(delivery)
          return (
            <TableRow key={delivery.id}>
              <TableCell>
                <DeliveryStatusBadge status={delivery.status} />
              </TableCell>
              <TableCell>
                <Link
                  to={automationRoute.delivery(webhookID, delivery.id)}
                  dir="ltr"
                  className="text-start text-xs underline-offset-4 hover:underline"
                >
                  {delivery.id}
                </Link>
              </TableCell>
              <TableCell>
                <span dir="ltr" className="text-start text-xs">
                  {delivery.eventID || "—"}
                </span>
              </TableCell>
              <TableCell>
                <span dir="ltr" className="text-xs">
                  {delivery.lastHttpStatus > 0 ? delivery.lastHttpStatus : "—"}
                </span>
              </TableCell>
              <TableCell className="text-muted-foreground">
                {faDateTime(delivery.createdAt)}
              </TableCell>
              <TableCell>
                <span className="text-xs">
                  {duration === null ? "—" : `${faDigits(duration)} میلی‌ثانیه`}
                </span>
              </TableCell>
              <TableCell>
                <span className="inline-flex items-center gap-1 text-xs">
                  {delivery.attemptCount > 1 && (
                    <RotateCcwIcon
                      className="size-3.5 text-muted-foreground"
                      aria-hidden="true"
                    />
                  )}
                  {faDigits(delivery.attemptCount)}
                </span>
              </TableCell>
              <TableCell className="max-w-44">
                <span
                  dir="auto"
                  className="block truncate text-start text-xs text-muted-foreground"
                  title={delivery.lastError}
                >
                  {delivery.lastError || "—"}
                </span>
              </TableCell>
              <TableCell className={ACTIONS_CELL}>
                <Button
                  variant="ghost"
                  size="icon-sm"
                  render={
                    <Link
                      to={automationRoute.delivery(webhookID, delivery.id)}
                      aria-label={`جزئیات تحویل ${delivery.id}`}
                    />
                  }
                >
                  <ArrowRightIcon />
                </Button>
              </TableCell>
            </TableRow>
          )
        })}
      </TableBody>
    </Table>
  )
}

export default function AutomationDeliveries({
  loaderData,
}: Route.ComponentProps) {
  const navigation = useNavigation()
  const [searchParams, setSearchParams] = useSearchParams()
  const loading = navigation.state === "loading"

  if (loaderData.status === "missing") {
    return (
      <div className="flex flex-col gap-6">
        <PageHeader title="تاریخچه تحویل‌ها" />
        <EmptyState
          icon={InboxIcon}
          title="وب‌هوک پیدا نشد"
          description="شناسه در نشانی معتبر نیست، یا این وب‌هوک حذف شده است."
          action={
            <Button variant="outline" render={<Link to={automationRoute.webhooks} />}>
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
        <PageHeader title="تاریخچه تحویل‌ها" />
        <ErrorState
          title={loaderData.status === "forbidden" ? "دسترسی مجاز نیست" : undefined}
          message={loaderData.message}
          onRetry={() => window.location.reload()}
        />
      </div>
    )
  }

  const { webhook, deliveries, currentPage, hasMore, filter } = loaderData
  const setFilter = (next: DeliveryStatus | "all") => {
    const params = new URLSearchParams(searchParams)
    if (next === "all") params.delete("status")
    else params.set("status", next)
    params.delete("page")
    setSearchParams(params)
  }

  return (
    <div className="flex flex-col gap-6">
      <PageHeader
        title={`تاریخچه تحویل‌های «${webhook.name}»`}
        description="هر رویداد یک بار تحویل می‌شود؛ تلاش دوباره همان تحویل را ادامه می‌دهد و رکورد تازه نمی‌سازد."
        actions={
          <Button variant="outline" render={<Link to={automationRoute.webhooks} />}>
            <ArrowRightIcon data-icon="inline-start" />
            وب‌هوک‌ها
          </Button>
        }
      />

      <div className="flex flex-wrap items-center gap-2" role="group" aria-label="فیلتر وضعیت">
        {statusFilters.map((value) => (
          <Button
            key={value}
            type="button"
            size="sm"
            variant={filter === value ? "default" : "outline"}
            aria-pressed={filter === value}
            onClick={() => setFilter(value)}
          >
            {value === "all" ? "همه" : deliveryStatusLabels[value]}
          </Button>
        ))}
      </div>

      <div className="rounded-xl border bg-card">
        {loading ? (
          <LoadingState label="در حال بارگذاری تحویل‌ها…" />
        ) : deliveries.length === 0 ? (
          <EmptyState
            icon={InboxIcon}
            title={filter === "all" ? "هنوز تحویلی ثبت نشده است" : "نتیجه‌ای با این فیلتر نیست"}
            description={
              filter === "all"
                ? "با رسیدن رویداد تازه، تحویل‌ها این‌جا ثبت می‌شوند."
                : "فیلتر را تغییر دهید."
            }
          />
        ) : (
          <DeliveriesTable deliveries={deliveries} webhookID={webhook.id} />
        )}
      </div>

      <nav className="flex items-center justify-between" aria-label="صفحه‌بندی">
        <p className="text-xs text-muted-foreground">صفحه {faDigits(currentPage)}</p>
        <div className="flex items-center gap-2">
          <PageLink
            params={searchParams}
            target={currentPage - 1}
            enabled={currentPage > 1 && !loading}
          >
            صفحه قبل
          </PageLink>
          <PageLink params={searchParams} target={currentPage + 1} enabled={hasMore && !loading}>
            صفحه بعد
          </PageLink>
        </div>
      </nav>
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
