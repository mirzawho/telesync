import { Link } from "react-router"
import {
  AlertTriangleIcon,
  ArrowLeftIcon,
  PlusIcon,
  WebhookIcon,
  WorkflowIcon,
} from "lucide-react"
import type * as React from "react"

import {
  DeliveryStatusBadge,
  EnabledBadge,
  RunStatusBadge,
  StatusCount,
} from "@/components/automation/status"
import {
  automationApi,
  automationRoute,
  jobTypeLabels,
  readStatus,
  readWebhook,
  readWorkflow,
  statusRecord,
  webhookRows,
  workflowRows,
  type AutomationStatusItem,
  type WebhookItem,
  type WorkflowItem,
} from "@/lib/automation"
import {
  automationAccessDenied,
  isAuthLost,
  isForbidden,
  loginRedirect,
} from "@/lib/automation-access"
import { faDateTime, faDigits } from "@/components/persian-digits"
import { ErrorState, LoadingState } from "@/components/states"
import { Badge } from "@workspace/ui/components/badge"
import { Button } from "@workspace/ui/components/button"
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@workspace/ui/components/tabs"
import { apiRequest, errorMessage } from "@/lib/api"
import { PageShell, PageError, pageMeta, section } from "@/lib/page"
import { requireSession } from "@/lib/session"

import type { Route } from "./+types/automation"

export const meta = () => pageMeta("اتوماسیون")

export const handle = section("اتوماسیون")

// How many records of each collection the front page shows. It is a summary
// with a door into the section, not the section itself: the tables, their
// paging and their filters live on the two collection screens.
const PREVIEW = 5

type Preview<T> = { rows: T[]; total: number | null }

type LoaderData =
  | {
      status: "ok"
      automation: AutomationStatusItem
      webhooks: Preview<WebhookItem>
      workflows: Preview<WorkflowItem>
    }
  | { status: "forbidden"; message: string }
  | { status: "error"; message: string }

// The area's front page: queue health and the two collections it manages, one
// per tab. It is a status strip and two doors, not a dashboard — the full
// tables and their histories live beside the record they belong to.
export async function clientLoader({
  request,
}: Route.LoaderArgs): Promise<LoaderData> {
  const { token, me } = await requireSession(request)
  // A restricted administrator is told so from the session itself: this page
  // must never fall back to a silently empty view because the calls returned
  // nothing.
  const denied = automationAccessDenied(me)
  if (denied) return { status: "forbidden", message: denied }
  try {
    const status = readStatus(
      statusRecord((await apiRequest<unknown>("GET", automationApi.status, { token })).data)
    )
    // One small page each: the rows are what the tabs list, `meta.total` is
    // what they are counted against. The lists themselves are their own
    // screens.
    const [webhooks, workflows] = await Promise.all([
      apiRequest<unknown>("GET", automationApi.webhooks, {
        token,
        query: { page: 1, per_page: PREVIEW },
      }),
      apiRequest<unknown>("GET", automationApi.workflows, {
        token,
        query: { page: 1, per_page: PREVIEW },
      }),
    ])
    return {
      status: "ok",
      automation: status,
      webhooks: {
        rows: webhookRows(webhooks.data).map(readWebhook),
        total: webhooks.meta.total ?? null,
      },
      workflows: {
        rows: workflowRows(workflows.data).map(readWorkflow),
        total: workflows.meta.total ?? null,
      },
    }
  } catch (error) {
    if (isAuthLost(error)) throw loginRedirect(request)
    if (isForbidden(error)) {
      return {
        status: "forbidden",
        message: "شما مجوز مشاهده اتوماسیون را ندارید.",
      }
    }
    return { status: "error", message: errorMessage(error) }
  }
}

export function ErrorBoundary({ error }: Route.ErrorBoundaryProps) {
  return (
    <PageShell title="اتوماسیون">
      <PageError error={error} />
    </PageShell>
  )
}

export default function AutomationHome({ loaderData }: Route.ComponentProps) {
  return (
    <PageShell
      title="اتوماسیون"
      description="رویدادهای کانال‌ها را به وب‌هوک و ورک‌فلو بسپارید؛ اجراها در پس‌زمینه انجام و ثبت می‌شوند."
    >
      {loaderData.status !== "ok" ? (
        <ErrorState
          title={loaderData.status === "forbidden" ? "دسترسی مجاز نیست" : undefined}
          message={loaderData.message}
          onRetry={() => window.location.reload()}
        />
      ) : (
        <AutomationOverview
          automation={loaderData.automation}
          webhooks={loaderData.webhooks}
          workflows={loaderData.workflows}
        />
      )}
    </PageShell>
  )
}

export function AutomationOverview({
  automation,
  webhooks,
  workflows,
}: {
  automation: AutomationStatusItem
  webhooks: Preview<WebhookItem>
  workflows: Preview<WorkflowItem>
}) {
  return (
    <div className="flex flex-col gap-6">
      <QueueStatus automation={automation} />
      <AutomationTabs webhooks={webhooks} workflows={workflows} />
      <RecentFailures automation={automation} />
    </div>
  )
}

// ---------------------------------------------------------------------------
// The two collections, one per tab.
//
// The tabs are the front page's whole point: the same two sections the sidebar
// and the phone's "more" menu lead to, each showing what it actually manages
// and a way into the screen that manages all of it. The default is the first
// tab, webhooks, exactly as `defaultValue` spells it — there is no second
// notion of "which collection is open" anywhere.
// ---------------------------------------------------------------------------

const TAB_WEBHOOKS = "webhooks"
const TAB_WORKFLOWS = "workflows"

export function AutomationTabs({
  webhooks,
  workflows,
}: {
  webhooks: Preview<WebhookItem>
  workflows: Preview<WorkflowItem>
}) {
  return (
    <Tabs defaultValue={TAB_WEBHOOKS}>
      {/* No className on the triggers: the shared Tabs owns the selected
          background and shadow, and a page must not restyle it. */}
      <TabsList className="w-full">
        <TabsTrigger value={TAB_WEBHOOKS}>وب‌هوک‌ها</TabsTrigger>
        <TabsTrigger value={TAB_WORKFLOWS}>ورک‌فلوها</TabsTrigger>
      </TabsList>
      <TabsContent value={TAB_WEBHOOKS}>
        <WebhooksPanel webhooks={webhooks} />
      </TabsContent>
      <TabsContent value={TAB_WORKFLOWS}>
        <WorkflowsPanel workflows={workflows} />
      </TabsContent>
    </Tabs>
  )
}

// The two panels are exported on their own so their content is provable
// without a click: the primitive only paints the open panel, and the workspace
// has no DOM to click one open in.
export function WebhooksPanel({ webhooks }: { webhooks: Preview<WebhookItem> }) {
  return (
    <CollectionPanel
      total={webhooks.total}
      totalLabel="وب‌هوک ثبت شده است"
      empty={webhooks.rows.length === 0}
      emptyText="هنوز وب‌هوکی ثبت نشده است."
      listTo={automationRoute.webhooks}
      listLabel="همه وب‌هوک‌ها"
      createTo={automationRoute.newWebhook}
      createLabel="افزودن وب‌هوک"
    >
      <ul className="flex flex-col gap-2">
        {webhooks.rows.map((webhook) => (
          <li key={webhook.id}>
            <WebhookRow webhook={webhook} />
          </li>
        ))}
      </ul>
    </CollectionPanel>
  )
}

export function WorkflowsPanel({ workflows }: { workflows: Preview<WorkflowItem> }) {
  return (
    <CollectionPanel
      total={workflows.total}
      totalLabel="ورک‌فلو ثبت شده است"
      empty={workflows.rows.length === 0}
      emptyText="هنوز ورک‌فلویی ثبت نشده است."
      listTo={automationRoute.workflows}
      listLabel="همه ورک‌فلوها"
      createTo={automationRoute.newWorkflow}
      createLabel="افزودن ورک‌فلو"
    >
      <ul className="flex flex-col gap-2">
        {workflows.rows.map((workflow) => (
          <li key={workflow.id}>
            <WorkflowRow workflow={workflow} />
          </li>
        ))}
      </ul>
    </CollectionPanel>
  )
}

// One tab's content: what the collection holds, and the two ways into the
// screen that holds all of it. The children are the records themselves.
function CollectionPanel({
  total,
  totalLabel,
  empty,
  emptyText,
  listTo,
  listLabel,
  createTo,
  createLabel,
  children,
}: {
  total: number | null
  totalLabel: string
  empty: boolean
  emptyText: string
  listTo: string
  listLabel: string
  createTo: string
  createLabel: string
  children: React.ReactNode
}) {
  return (
    <section className="flex flex-col gap-3">
      <div className="flex flex-wrap items-center justify-between gap-2">
        <p className="text-sm text-muted-foreground">
          {total === null ? (
            <LoadingState label="در حال شمارش…" />
          ) : (
            <>
              <span className="font-semibold text-foreground">
                {faDigits(total)}
              </span>{" "}
              {totalLabel}
            </>
          )}
        </p>
        <div className="flex flex-wrap items-center gap-2">
          <Button variant="outline" size="sm" render={<Link to={listTo} />}>
            {listLabel}
            <ArrowLeftIcon data-icon="inline-end" />
          </Button>
          <Button size="sm" render={<Link to={createTo} />}>
            <PlusIcon data-icon="inline-start" />
            {createLabel}
          </Button>
        </div>
      </div>
      {empty ? <p className="text-sm text-muted-foreground">{emptyText}</p> : children}
    </section>
  )
}

// A row is the record itself, linked to the screen that edits it, carrying the
// few facts that tell it apart. Its history is reached from that screen, so
// this list stays one line per record.
function WebhookRow({ webhook }: { webhook: WebhookItem }) {
  return (
    <Link
      to={automationRoute.editWebhook(webhook.id)}
      className="flex flex-wrap items-center gap-2 rounded-lg border px-3 py-2 text-sm transition-colors hover:bg-muted/50 focus-visible:ring-2 focus-visible:ring-ring focus-visible:outline-none"
    >
      <WebhookIcon
        className="size-4 shrink-0 text-muted-foreground"
        aria-hidden="true"
      />
      <span className="min-w-0 flex-1 truncate font-medium">{webhook.name}</span>
      <EnabledBadge enabled={webhook.enabled} />
      {webhook.lastDelivery && (
        <DeliveryStatusBadge status={webhook.lastDelivery.status} />
      )}
      {webhook.summary && (
        <span className="flex items-center gap-1">
          <Badge variant="success">{faDigits(webhook.summary.succeeded)}</Badge>
          <Badge variant="destructive">{faDigits(webhook.summary.failed)}</Badge>
          <Badge variant="muted">{faDigits(webhook.summary.pending)}</Badge>
        </span>
      )}
      <ArrowLeftIcon
        className="size-4 shrink-0 text-muted-foreground"
        aria-hidden="true"
      />
    </Link>
  )
}

function WorkflowRow({ workflow }: { workflow: WorkflowItem }) {
  const destinations = workflow.destinations.filter((d) => d.enabled).length
  return (
    <Link
      to={automationRoute.workflow(workflow.id)}
      className="flex flex-wrap items-center gap-2 rounded-lg border px-3 py-2 text-sm transition-colors hover:bg-muted/50 focus-visible:ring-2 focus-visible:ring-ring focus-visible:outline-none"
    >
      <WorkflowIcon
        className="size-4 shrink-0 text-muted-foreground"
        aria-hidden="true"
      />
      <span className="min-w-0 flex-1 truncate font-medium">
        {workflow.channel?.title || `کانال ${faDigits(workflow.managedChannelID)}`}
      </span>
      <EnabledBadge enabled={workflow.enabled} />
      <Badge variant="outline">
        {faDigits(destinations)} مقصد
      </Badge>
      {workflow.lastRun && <RunStatusBadge status={workflow.lastRun.status} />}
      <ArrowLeftIcon
        className="size-4 shrink-0 text-muted-foreground"
        aria-hidden="true"
      />
    </Link>
  )
}

// ---------------------------------------------------------------------------
// Queue health. A few counts and the failures behind one of them — never a
// chart, and never a total that is not backed by a row somewhere.
// ---------------------------------------------------------------------------

export function QueueStatus({
  automation,
}: {
  automation: AutomationStatusItem
}) {
  return (
    <section className="flex flex-col gap-3" aria-labelledby="automation-health">
      <div className="flex flex-wrap items-center justify-between gap-2">
        <h2 id="automation-health" className="text-sm font-semibold">
          وضعیت صف
        </h2>
        <span className="text-xs text-muted-foreground">
          {automation.enabled ? "اتوماسیون فعال است" : "اتوماسیون غیرفعال است"}
        </span>
      </div>
      <div className="grid grid-cols-2 gap-2 sm:grid-cols-3 lg:grid-cols-5">
        <StatusCount label="در صف" value={faDigits(automation.pendingJobs)} />
        <StatusCount label="در حال پردازش" value={faDigits(automation.processing)} />
        <StatusCount label="در انتظار تلاش دوباره" value={faDigits(automation.retryBacklog)} />
        <StatusCount
          label="شکست‌های اخیر"
          value={faDigits(automation.failedRecent)}
          tone={automation.failedRecent > 0 ? "destructive" : "muted"}
        />
        <StatusCount label="پردازشگرها" value={faDigits(automation.workers)} />
      </div>
    </section>
  )
}

export function RecentFailures({
  automation,
}: {
  automation: AutomationStatusItem
}) {
  return (
    <section className="flex flex-col gap-3" aria-labelledby="automation-failures">
      <h2 id="automation-failures" className="text-sm font-semibold">
        شکست‌های اخیر
      </h2>
      {automation.recentFailures.length === 0 ? (
        <p className="text-sm text-muted-foreground">
          در این مدت شکستی ثبت نشده است.
        </p>
      ) : (
        <ul className="flex flex-col gap-2">
          {automation.recentFailures.map((failure, index) => (
            <li
              key={`${failure.jobType}-${failure.targetID}-${index}`}
              className="flex flex-wrap items-start gap-2 rounded-lg border px-3 py-2 text-sm"
            >
              <AlertTriangleIcon
                className="mt-0.5 size-4 text-destructive"
                aria-hidden="true"
              />
              <span className="flex min-w-0 flex-1 flex-col gap-0.5">
                <span className="flex flex-wrap items-center gap-2">
                  <span className="font-medium">
                    {jobTypeLabels[failure.jobType]}
                  </span>
                  <span dir="ltr" className="text-xs text-muted-foreground">
                    #{failure.targetID}
                  </span>
                  <span dir="ltr" className="text-xs text-muted-foreground">
                    {failure.errorCode || "—"}
                  </span>
                </span>
                <span dir="auto" className="text-xs text-muted-foreground">
                  {failure.errorMessage || "—"}
                </span>
              </span>
              <span className="text-xs text-muted-foreground">
                {faDateTime(failure.at)}
              </span>
            </li>
          ))}
        </ul>
      )}
    </section>
  )
}
