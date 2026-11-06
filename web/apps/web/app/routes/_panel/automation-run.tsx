import { Link } from "react-router"
import { ArrowRightIcon, ExternalLinkIcon, InboxIcon } from "lucide-react"
import type * as React from "react"

import {
  AIStatusBadge,
  CopyButton,
  PublicationStatusBadge,
  RunStatusBadge,
  SwitchState,
} from "@/components/automation/status"
import { faDateTime, faDigits } from "@/components/persian-digits"
import { EmptyState, ErrorState } from "@/components/states"
import { Badge } from "@workspace/ui/components/badge"
import { Button } from "@workspace/ui/components/button"

import {
  automationApi,
  automationRoute,
  destinationTypeLabels,
  readRunRecord,
  readWorkflowRecord,
  runDurationMs,
  type RunItem,
  type RunPublication,
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

import type { Route } from "./+types/automation-run"

export const meta = () => pageMeta("جزئیات اجرا")

export const handle = section("اتوماسیون")

type LoaderData =
  | {
      status: "ok"
      workflowID: number
      workflowLabel: string
      run: RunItem
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
  const workflowID = validID(params.workflowId)
  const runID = validID(params.runId)
  if (workflowID === null || runID === null) return { status: "missing" }

  try {
    const run = readRunRecord(
      (await apiRequest<unknown>(
        "GET",
        automationApi.run(workflowID, runID),
        { token }
      )).data
    )
    let workflowLabel = `#${workflowID}`
    try {
      const workflow = readWorkflowRecord(
        (await apiRequest<unknown>(
          "GET",
          automationApi.workflow(workflowID),
          { token }
        )).data
      )
      workflowLabel =
        workflow.channel?.title ?? `کانال ${workflow.managedChannelID}`
    } catch {
      // A workflow that vanished leaves the run readable under its id.
    }
    return { status: "ok", workflowID, workflowLabel, run }
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
        message: "شما مجوز مشاهده این اجرا را ندارید.",
      }
    }
    return { status: "error", message: errorMessage(error) }
  }
}

export function ErrorBoundary({ error }: Route.ErrorBoundaryProps) {
  return (
    <>
      <PageHeader title="جزئیات اجرا" />
      <PageError error={error} />
    </>
  )
}

export default function AutomationRun({ loaderData }: Route.ComponentProps) {
  if (loaderData.status === "missing") {
    return (
      <div className="flex flex-col gap-6">
        <PageHeader title="جزئیات اجرا" />
        <EmptyState
          icon={InboxIcon}
          title="اجرا پیدا نشد"
          description="شناسه در نشانی معتبر نیست، یا این رکورد حذف شده است."
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
        <PageHeader title="جزئیات اجرا" />
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

  return (
    <RunDetail
      run={loaderData.run}
      workflowID={loaderData.workflowID}
      workflowLabel={loaderData.workflowLabel}
    />
  )
}

// RunDetail is the record itself, once one has been read: the shape of an
// execution as the server stored it, with nothing inferred and nothing editable.
export function RunDetail({
  run,
  workflowID,
  workflowLabel,
}: {
  run: RunItem
  workflowID: number
  workflowLabel: string
}) {
  const runsPath = automationRoute.runs(workflowID)
  const duration = runDurationMs(run)

  return (
    <div className="flex flex-col gap-6">
      <PageHeader
        title={`اجرای ${run.id}`}
        description={`ورک‌فلوی «${workflowLabel}» — ${run.eventID || "بدون شناسه رویداد"}`}
        actions={
          <Button variant="outline" render={<Link to={runsPath} />}>
            <ArrowRightIcon data-icon="inline-start" />
            تاریخچه اجراها
          </Button>
        }
      />

      <dl className="grid grid-cols-2 gap-2 rounded-xl border p-4 sm:grid-cols-3 lg:grid-cols-4">
        <Summary label="وضعیت">
          <RunStatusBadge status={run.status} />
        </Summary>
        <Summary label="شناسه اجرا">
          <span dir="ltr" className="text-sm">
            {run.id}
          </span>
        </Summary>
        <Summary label="شناسه رویداد">
          <span dir="ltr" className="text-sm break-all">
            {run.eventID || "—"}
          </span>
        </Summary>
        <Summary label="پیام مبدأ">
          <span dir="ltr" className="text-sm">
            {run.sourceMessageID || "—"}
          </span>
        </Summary>
        <Summary label="زمان دریافت">{faDateTime(run.createdAt)}</Summary>
        <Summary label="زمان شروع">{faDateTime(run.startedAt)}</Summary>
        <Summary label="زمان پایان">{faDateTime(run.completedAt)}</Summary>
        <Summary label="مدت">
          {duration === null ? (
            "—"
          ) : (
            <span dir="ltr" className="text-sm">
              {faDigits(duration)} ms
            </span>
          )}
        </Summary>
        <Summary label="تعداد تلاش">{faDigits(run.attemptCount)}</Summary>
        {/* The revision this run was prepared from: a queued run keeps it, so
            editing the workflow afterwards changed future runs and not this. */}
        <Summary label="نسخه پیکربندی">
          <span dir="ltr" className="text-sm">
            {run.configRevision > 0 ? run.configRevision : "—"}
          </span>
        </Summary>
      </dl>

      {/* A run that reached only some of its destinations is never dressed up
          as a success: the badge carries the warning tone and the page says so
          in words, whatever the successful rows below look like. */}
      {run.status === "partially_published" && (
        <p
          role="alert"
          className="rounded-lg border border-amber-500/40 bg-amber-500/10 px-3 py-2 text-sm text-amber-800"
        >
          این اجرا در همهٔ مقاصد منتشر نشد. نتیجهٔ آن موفق کامل نیست و هر مقصد
          وضعیت خودش را دارد.
        </p>
      )}

      {/* ---------------------------- Content ---------------------------- */}
      <section className="flex flex-col gap-3" aria-labelledby="run-content">
        <h2 id="run-content" className="text-sm font-semibold">
          محتوا
        </h2>
        <ContentBlock label="متن اصلی" text={run.originalContent} />
        <ContentBlock label="متن ویرایش‌شده" text={run.polishedContent} />
      </section>

      {/* ------------------------------- AI ------------------------------ */}
      <section className="flex flex-col gap-3" aria-labelledby="run-ai">
        <h2 id="run-ai" className="text-sm font-semibold">
          هوش مصنوعی
        </h2>
        <dl className="grid grid-cols-2 gap-2 rounded-xl border p-4 sm:grid-cols-3">
          <Summary label="نتیجه">
            <AIStatusBadge status={run.aiStatus} />
          </Summary>
          <Summary label="بازگشت به محتوای اصلی">
            <SwitchState
              on={run.aiFallbackUsed}
              onLabel="استفاده شد"
              offLabel="استفاده نشد"
            />
          </Summary>
          <Summary label="مدل">
            <span dir="ltr" className="text-sm break-all">
              {run.aiModel || "—"}
            </span>
          </Summary>
          <Summary label="توکن ورودی">{faDigits(run.aiInputTokens)}</Summary>
          <Summary label="توکن خروجی">{faDigits(run.aiOutputTokens)}</Summary>
          <Summary label="مدت هوش مصنوعی">
            {run.aiDurationMs > 0 ? (
              <span dir="ltr" className="text-sm">
                {faDigits(run.aiDurationMs)} ms
              </span>
            ) : (
              "—"
            )}
          </Summary>
        </dl>
        {run.aiErrorCode && (
          <p
            role="alert"
            className="rounded-lg bg-destructive/10 px-3 py-2 text-sm text-destructive"
          >
            <span dir="ltr">{run.aiErrorCode}</span>
            {run.aiErrorMessage ? ` — ${run.aiErrorMessage}` : ""}
          </p>
        )}
      </section>

      {/* -------------------------- Destinations ------------------------- */}
      <section className="flex flex-col gap-3" aria-labelledby="run-publications">
        <h2 id="run-publications" className="text-sm font-semibold">
          نتیجه مقاصد
        </h2>
        {run.publications.length === 0 ? (
          <p className="text-sm text-muted-foreground">
            مقصدی برای این اجرا ثبت نشده است.
          </p>
        ) : (
          <ul className="flex flex-col gap-2">
            {run.publications.map((publication, index) => (
              <li key={publication.id || index}>
                <PublicationCard publication={publication} />
              </li>
            ))}
          </ul>
        )}
      </section>

      {(run.lastErrorCode || run.lastErrorMessage) && (
        <p
          role="alert"
          className="rounded-lg bg-destructive/10 px-3 py-2 text-sm text-destructive"
        >
          <span dir="ltr">{run.lastErrorCode}</span>
          {run.lastErrorMessage ? ` — ${run.lastErrorMessage}` : ""}
        </p>
      )}
    </div>
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

// The source text and the polished one are kept apart on purpose: the run never
// overwrites what the channel said. Neither is editable here — an execution is
// a record, not a form.
function ContentBlock({ label, text }: { label: string; text: string }) {
  return (
    <div className="flex flex-col gap-1.5">
      <div className="flex items-center justify-between gap-2">
        <h3 className="text-xs font-medium text-muted-foreground">{label}</h3>
        {text && <CopyButton value={text} label={`کپی ${label}`} />}
      </div>
      {text ? (
        <p
          dir="auto"
          className="max-h-72 overflow-auto rounded-lg border bg-muted/30 px-3 py-2 text-sm leading-relaxed whitespace-pre-wrap"
        >
          {text}
        </p>
      ) : (
        <p className="rounded-lg border border-dashed px-3 py-2 text-xs text-muted-foreground">
          متنی ثبت نشده است.
        </p>
      )}
    </div>
  )
}

function PublicationCard({ publication }: { publication: RunPublication }) {
  return (
    <section className="flex flex-col gap-2 rounded-xl border p-4">
      <header className="flex flex-wrap items-center gap-2">
        <Badge variant="outline">
          {destinationTypeLabels[publication.type]}
        </Badge>
        <PublicationStatusBadge status={publication.status} />
        <span className="ms-auto text-xs text-muted-foreground">
          {faDateTime(publication.completedAt)}
        </span>
      </header>

      <dl className="grid grid-cols-2 gap-2 sm:grid-cols-3">
        <Summary label="اتصال">
          <span dir="ltr" className="text-sm break-all">
            {publication.type === "telegram"
              ? publication.clientKey || "—"
              : `site ${publication.siteID || "—"}`}
          </span>
        </Summary>
        {publication.type === "telegram" ? (
          <Summary label="شناسه کانال تلگرام">
            <span dir="ltr" className="text-sm">
              {publication.channelID || "—"}
            </span>
          </Summary>
        ) : (
          <Summary label="شناسه سایت">
            <span dir="ltr" className="text-sm">
              {publication.siteID || "—"}
            </span>
          </Summary>
        )}
        <Summary label="شناسه پیام دور">
          <span dir="ltr" className="text-sm">
            {publication.remoteMessageID || "—"}
          </span>
        </Summary>
        <Summary label="شناسه نوشته دور">
          <span dir="ltr" className="text-sm">
            {publication.remotePostID || "—"}
          </span>
        </Summary>
        <Summary label="تعداد تلاش">{faDigits(publication.attemptCount)}</Summary>
        <Summary label="پیوند">
          {publication.remoteURL ? (
            <a
              dir="ltr"
              href={publication.remoteURL}
              target="_blank"
              rel="noreferrer noopener"
              className="inline-flex items-center gap-1 text-start text-xs break-all underline-offset-4 hover:underline"
            >
              <ExternalLinkIcon className="size-3.5 shrink-0" aria-hidden="true" />
              {publication.remoteURL}
            </a>
          ) : (
            "—"
          )}
        </Summary>
      </dl>

      {publication.lastErrorCode && (
        <p
          role="alert"
          className="rounded-lg bg-destructive/10 px-3 py-2 text-sm text-destructive"
        >
          <span dir="ltr">{publication.lastErrorCode}</span>
          {publication.lastErrorMessage
            ? ` — ${publication.lastErrorMessage}`
            : ""}
        </p>
      )}
    </section>
  )
}
