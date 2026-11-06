import { Link, useNavigation, useSearchParams } from "react-router"
import { ArrowRightIcon, InboxIcon, WorkflowIcon } from "lucide-react"
import * as React from "react"

import {
  AIStatusBadge,
  PublicationStatusBadge,
  RunStatusBadge,
  SwitchState,
} from "@/components/automation/status"
import { faDateTime, faDigits } from "@/components/persian-digits"
import { EmptyState, ErrorState, LoadingState } from "@/components/states"
import { Badge } from "@workspace/ui/components/badge"
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
  destinationTypeLabels,
  readRun,
  readWorkflowRecord,
  runRows,
  runDurationMs,
  type RunItem,
  type RunPublication,
  type RunStatus,
  type WorkflowItem,
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
import { requestURL } from "@/path"

import type { Route } from "./+types/automation-runs"

const PAGE_SIZE = 20

export const meta = () => pageMeta("تاریخچه اجراها")

export const handle = section("اتوماسیون")

const ACTIONS_HEAD = "sticky left-0 z-20 w-12 bg-background text-end"
const ACTIONS_CELL = "sticky left-0 z-10 bg-background text-end"

const statusFilters: (RunStatus | "all")[] = [
  "all",
  "queued",
  "processing",
  "published",
  "published_with_fallback",
  "partially_published",
  "failed",
  "cancelled",
]

const statusLabels: Record<RunStatus | "all", string> = {
  all: "همه",
  queued: "در صف",
  processing: "در حال پردازش",
  published: "منتشر شد",
  published_with_fallback: "منتشر شد با محتوای اصلی",
  partially_published: "بخشی منتشر شد",
  failed: "ناموفق",
  cancelled: "لغو شد",
}

type LoaderData =
  | {
      status: "ok"
      workflow: WorkflowItem
      workflowLabel: string
      runs: RunItem[]
      currentPage: number
      hasMore: boolean
      filter: RunStatus | "all"
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
  const id = validID(params.workflowId)
  if (id === null) return { status: "missing" }

  const rawFilter = url.searchParams.get("status") ?? "all"
  const filter = (
    statusFilters.includes(rawFilter as RunStatus | "all") ? rawFilter : "all"
  ) as RunStatus | "all"
  const currentPage = positiveInt(url.searchParams.get("page"))

  try {
    const [workflow, page] = await Promise.all([
      apiRequest<unknown>("GET", automationApi.workflow(id), { token }),
      apiRequest<unknown>("GET", automationApi.runs(id), {
        token,
        query: {
          page: currentPage,
          per_page: PAGE_SIZE,
          status: filter === "all" ? undefined : filter,
        },
      }),
    ])
    const item = readWorkflowRecord(workflow.data)
    return {
      status: "ok",
      workflow: item,
      workflowLabel: item.channel?.title ?? `کانال ${item.managedChannelID}`,
      runs: runRows(page.data).map(readRun),
      currentPage,
      hasMore: page.meta.has_more ?? false,
      filter,
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
        message: "شما مجوز مشاهده تاریخچه اجراها را ندارید.",
      }
    }
    return { status: "error", message: errorMessage(error) }
  }
}

export function ErrorBoundary({ error }: Route.ErrorBoundaryProps) {
  return (
    <>
      <PageHeader title="تاریخچه اجراها" />
      <PageError error={error} />
    </>
  )
}

// remoteIDOf is the id the destination confirmed, in the numbering of that
// destination: a Telegram message id for a Telegram publication, a WordPress
// post id for a WordPress one. The two are never interchangeable, so the type
// decides which is read and a destination that confirmed nothing shows none.
function remoteIDOf(publication: RunPublication): number {
  return publication.type === "telegram"
    ? publication.remoteMessageID
    : publication.remotePostID
}

export function RunsTable({
  runs,
  workflowID,
}: {
  runs: RunItem[]
  workflowID: number
}) {
  return (
    <Table>
      <TableHeader>
        <TableRow>
          <TableHead>پیام مبدأ</TableHead>
          <TableHead>زمان دریافت</TableHead>
          <TableHead>وضعیت</TableHead>
          <TableHead>نتیجه هوش مصنوعی</TableHead>
          <TableHead>بازگشت به محتوای اصلی</TableHead>
          <TableHead>نتیجه مقاصد</TableHead>
          <TableHead>مدت</TableHead>
          <TableHead className={ACTIONS_HEAD}>
            <span className="sr-only">عملیات</span>
          </TableHead>
        </TableRow>
      </TableHeader>
      <TableBody>
        {runs.map((run) => {
          const duration = runDurationMs(run)
          return (
            <TableRow key={run.id}>
              <TableCell>
                <Link
                  to={automationRoute.run(workflowID, run.id)}
                  dir="ltr"
                  className="text-start text-xs underline-offset-4 hover:underline"
                >
                  {run.sourceMessageID || "—"}
                </Link>
              </TableCell>
              <TableCell className="text-muted-foreground">
                {faDateTime(run.createdAt)}
              </TableCell>
              <TableCell>
                <span className="flex flex-col gap-1">
                  <RunStatusBadge status={run.status} />
                  {/* Retry information and the last refusal travel with the
                      status: a run that failed says why and how many times it
                      tried, in the same cell as the verdict. */}
                  <span className="text-xs text-muted-foreground">
                    تلاش {faDigits(run.attemptCount)}
                  </span>
                  {(run.lastErrorCode || run.lastErrorMessage) && (
                    <span
                      role="alert"
                      className="max-w-56 text-xs font-medium text-destructive"
                    >
                      <span dir="ltr">{run.lastErrorCode}</span>
                      {run.lastErrorMessage ? ` — ${run.lastErrorMessage}` : ""}
                    </span>
                  )}
                </span>
              </TableCell>
              <TableCell>
                <AIStatusBadge status={run.aiStatus} />
              </TableCell>
              <TableCell>
                <SwitchState
                  on={run.aiFallbackUsed}
                  onLabel="استفاده شد"
                  offLabel="استفاده نشد"
                />
              </TableCell>
              <TableCell>
                {run.publications.length === 0 ? (
                  <span className="text-xs text-muted-foreground">—</span>
                ) : (
                  <ul className="flex flex-col gap-1">
                    {run.publications.map((publication, index) => (
                      <li
                        key={publication.id || index}
                        className="flex flex-wrap items-center gap-1.5"
                      >
                        <Badge variant="outline">
                          {destinationTypeLabels[publication.type]}
                        </Badge>
                        <PublicationStatusBadge status={publication.status} />
                        {/* The remote id is the proof a publication happened:
                            a Telegram peer's message id or the WordPress post
                            id. A destination that published nothing shows none
                            rather than a zero. */}
                        {remoteIDOf(publication) > 0 && (
                          <span
                            dir="ltr"
                            className="text-xs text-muted-foreground"
                          >
                            #{remoteIDOf(publication)}
                          </span>
                        )}
                      </li>
                    ))}
                  </ul>
                )}
              </TableCell>
              <TableCell className="text-muted-foreground">
                {duration === null ? (
                  "—"
                ) : (
                  <span dir="ltr" className="text-xs">
                    {faDigits(duration)} ms
                  </span>
                )}
              </TableCell>
              <TableCell className={ACTIONS_CELL}>
                <Button
                  variant="ghost"
                  size="icon-sm"
                  render={
                    <Link
                      to={automationRoute.run(workflowID, run.id)}
                      aria-label={`جزئیات اجرای ${run.id}`}
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

export default function AutomationRuns({ loaderData }: Route.ComponentProps) {
  const navigation = useNavigation()
  const [searchParams, setSearchParams] = useSearchParams()
  const loading = navigation.state === "loading"

  if (loaderData.status === "missing") {
    return (
      <div className="flex flex-col gap-6">
        <PageHeader title="تاریخچه اجراها" />
        <EmptyState
          icon={InboxIcon}
          title="ورک‌فلو پیدا نشد"
          description="شناسه در نشانی معتبر نیست، یا این ورک‌فلو حذف شده است."
          action={
            <Button
              variant="outline"
              render={<Link to={automationRoute.workflows} />}
            >
              <ArrowRightIcon />
              بازگشت به ورک‌فلوها
            </Button>
          }
        />
      </div>
    )
  }

  if (loaderData.status !== "ok") {
    return (
      <div className="flex flex-col gap-6">
        <PageHeader title="تاریخچه اجراها" />
        <ErrorState
          title={
            loaderData.status === "forbidden" ? "دسترسی مجاز نیست" : undefined
          }
          message={loaderData.message}
          onRetry={() => window.location.reload()}
        />
      </div>
    )
  }

  const { workflow, workflowLabel, runs, currentPage, hasMore, filter } =
    loaderData
  const setFilter = (next: RunStatus | "all") => {
    const params = new URLSearchParams(searchParams)
    if (next === "all") params.delete("status")
    else params.set("status", next)
    params.delete("page")
    setSearchParams(params)
  }

  return (
    <div className="flex flex-col gap-6">
      <PageHeader
        title={`تاریخچه اجراهای «${workflowLabel}»`}
        description="هر پیام تازه یک اجرا می‌سازد. اجراها خودکارند و نیازی به تأیید ندارند."
        actions={
          <>
            <Button
              variant="outline"
              render={<Link to={automationRoute.workflows} />}
            >
              <ArrowRightIcon data-icon="inline-start" />
              ورک‌فلوها
            </Button>
            <Button
              variant="outline"
              render={<Link to={automationRoute.workflow(workflow.id)} />}
            >
              ویرایش ورک‌فلو
            </Button>
          </>
        }
      />

      <div
        className="flex flex-wrap items-center gap-2"
        role="group"
        aria-label="فیلتر وضعیت"
      >
        {statusFilters.map((value) => (
          <Button
            key={value}
            type="button"
            size="sm"
            variant={filter === value ? "default" : "outline"}
            aria-pressed={filter === value}
            onClick={() => setFilter(value)}
          >
            {statusLabels[value]}
          </Button>
        ))}
      </div>

      <div className="rounded-xl border bg-card">
        {loading ? (
          <LoadingState label="در حال بارگذاری اجراها…" />
        ) : runs.length === 0 ? (
          <EmptyState
            icon={WorkflowIcon}
            title={
              filter === "all"
                ? "هنوز اجرایی ثبت نشده است"
                : "نتیجه‌ای با این فیلتر نیست"
            }
            description={
              filter === "all"
                ? "با رسیدن پیام تازه در کانال مبدأ، اجراها این‌جا ثبت می‌شوند."
                : "فیلتر را تغییر دهید."
            }
          />
        ) : (
          <RunsTable runs={runs} workflowID={workflow.id} />
        )}
      </div>

      <nav className="flex items-center justify-between" aria-label="صفحه‌بندی">
        <p className="text-xs text-muted-foreground">
          صفحه {faDigits(currentPage)}
        </p>
        <div className="flex items-center gap-2">
          <PageLink
            params={searchParams}
            target={currentPage - 1}
            enabled={currentPage > 1 && !loading}
          >
            صفحه قبل
          </PageLink>
          <PageLink
            params={searchParams}
            target={currentPage + 1}
            enabled={hasMore && !loading}
          >
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

