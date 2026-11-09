import * as React from "react"
import {
  Link,
  redirect,
  useLocation,
  useNavigate,
  useNavigation,
  useSearchParams,
  useSubmit,
} from "react-router"
import { PlusIcon, WebhookIcon, WorkflowIcon } from "lucide-react"

import { ConfirmDialog } from "@/components/confirm-dialog"
import { faDigits } from "@/components/persian-digits"
import { SearchInput } from "@/components/search-input"
import { EmptyState, ErrorState, LoadingState } from "@/components/states"
import {
  testOutcomeOf,
  WebhooksTable,
  type TestOutcome,
} from "@/components/automation/webhooks-table"
import { WorkflowsTable } from "@/components/automation/workflows-table"
import type { SourceChannelOption } from "@/components/automation/webhook-form"
import { explainWebhookError } from "@/components/automation/webhook-errors"
import {
  automationApi,
  automationFormLinkState,
  automationRoute,
  explainAutomationError,
  readDeliveryRecord,
  readWebhook,
  readWorkflow,
  webhookRows,
  workflowRows,
  type WebhookItem,
  type WorkflowItem,
} from "@/lib/automation"
import {
  automationAccessDenied,
  isAuthLost,
  isForbidden,
  loginRedirect,
} from "@/lib/automation-access"
import { canManageWebhooks } from "@/components/automation/webhook-access"
import { apiRequest, errorMessage } from "@/lib/api"
import { PageShell, PageError, pageMeta, section } from "@/lib/page"
import { requireSession } from "@/lib/session"
import { requestURL, strip } from "@/path"
import { Button } from "@workspace/ui/components/button"
import {
  Tabs,
  TabsContent,
  TabsList,
  TabsTrigger,
} from "@workspace/ui/components/tabs"
import { useToast } from "@workspace/ui/components/toast"

import type { Route } from "./+types/automation"

export const meta = () => pageMeta("اتوماسیون")

export const handle = section("اتوماسیون")

// One page of the open collection. The backend pages both lists itself and
// answers `total`, `total_pages` and `has_more`, so this is the size of a
// request, never the size of what the page pretends to hold.
const PAGE_SIZE = 20

// How long a typed search sits still before it is sent. Short enough to feel
// like typing, long enough that a fast typist makes one request, not one per
// keystroke.
const SEARCH_DEBOUNCE_MS = 300

export const TAB_WEBHOOKS = "webhooks"
export const TAB_WORKFLOWS = "workflows"

export type Tab = typeof TAB_WEBHOOKS | typeof TAB_WORKFLOWS

// What one tab is showing: the query that produced it, the page of it, and how
// much of the collection that page accounts for. It is one tab's worth of a
// collection and never both at once — the closed tab is not fetched, so a
// search typed here can never reach the other collection's records.
export type ListingFrame = {
  q: string
  page: number
  total: number | null
  totalPages: number | null
  hasMore: boolean
  canManage: boolean
}

export type Listing =
  | (ListingFrame & { tab: typeof TAB_WEBHOOKS; rows: WebhookItem[] })
  | (ListingFrame & { tab: typeof TAB_WORKFLOWS; rows: WorkflowItem[] })

type LoaderData =
  | { status: "ok"; listing: Listing; channels: SourceChannelOption[] }
  | { status: "forbidden"; message: string }
  | { status: "error"; message: string }

function positiveInt(raw: string | null): number {
  const value = Number(raw)
  return Number.isInteger(value) && value > 0 ? value : 1
}

// The tab a URL names. Anything else is the default: a stale or hand-typed
// `tab` never selects a tab that does not exist.
export function readTab(raw: string | null): Tab {
  return raw === TAB_WORKFLOWS ? TAB_WORKFLOWS : TAB_WEBHOOKS
}

// The URL of one view. The tab the page is on, the query it is filtered by and
// the page of the result are three separate facts, so leaving one out is what
// means "the default of that one" — which is what makes a search start back at
// the first page and a plain tab change keep its own page and query.
export function viewParams(tab: Tab, q: string, page: number): URLSearchParams {
  const next = new URLSearchParams()
  if (tab !== TAB_WEBHOOKS) next.set("tab", tab)
  const query = q.trim()
  if (query) next.set("q", query)
  if (page > 1) next.set("page", String(page))
  return next
}

// A search is a new look at a collection, so it starts at its first page.
export function searchView(tab: Tab, q: string): URLSearchParams {
  return viewParams(tab, q, 1)
}

const hrefOf = (params: URLSearchParams) => {
  const query = params.toString()
  return query ? `?${query}` : "?"
}

export async function clientLoader({
  request,
}: Route.LoaderArgs): Promise<LoaderData> {
  const { token, me } = await requireSession(request)
  // A restricted administrator is told so from the session itself: this page
  // must never fall back to a silently empty view because the calls returned
  // nothing.
  const denied = automationAccessDenied(me)
  if (denied) return { status: "forbidden", message: denied }

  const url = requestURL(request)
  const tab = readTab(url.searchParams.get("tab"))
  const q = (url.searchParams.get("q") ?? "").trim()
  const page = positiveInt(url.searchParams.get("page"))

  try {
    // Only the open collection is read, and only its own query is sent with it.
    // The other one is not fetched at all: there is no page here that could
    // show it, and a query typed for one must never travel to the other.
    const [list, channels] = await Promise.all([
      apiRequest<unknown>(
        "GET",
        tab === TAB_WEBHOOKS ? automationApi.webhooks : automationApi.workflows,
        { token, query: { page, per_page: PAGE_SIZE, q: q || undefined } }
      ),
      apiRequest<{
        channels?: { id: number; title: string; client_key?: string }[]
      }>(
        "GET",
        "/local/channels",
        { token, query: { page: 1, per_page: 100 } }
      ),
    ])
    // A row names its source channel by title, and the id is a local
    // managed_channels.id, so the labels come from the stored channel rows.
    const options: SourceChannelOption[] = (channels.data?.channels ?? []).map(
      (channel) => ({
        id: channel.id,
        label: channel.title,
        clientKey: channel.client_key ?? "",
      })
    )
    const frame: ListingFrame = {
      q,
      page,
      total: list.meta.total ?? null,
      totalPages: list.meta.total_pages ?? null,
      hasMore: list.meta.has_more ?? false,
      canManage:
        tab === TAB_WEBHOOKS
          ? canManageWebhooks(me)
          : Boolean(me.access?.automation),
    }
    return {
      status: "ok",
      listing:
        tab === TAB_WEBHOOKS
          ? { ...frame, tab, rows: webhookRows(list.data).map(readWebhook) }
          : { ...frame, tab, rows: workflowRows(list.data).map(readWorkflow) },
      channels: options,
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

// What one row may ask of the store. A row of the open collection offers these
// and nothing else; each is one field or one record, so nothing here can reach
// the queue, the listener or the publishing engines.
const rowIntents = new Set([
  "webhook-toggle",
  "webhook-test",
  "webhook-delete",
  "workflow-toggle",
  "workflow-delete",
])

interface ActionData {
  error?: string
  intent?: string
  testResult?: TestOutcome
}

export async function clientAction({
  request,
}: Route.ActionArgs): Promise<ActionData | Response> {
  const { token } = await requireSession(request)
  const form = await request.formData()
  const intent = String(form.get("intent") ?? "")
  const id = Number(form.get("id"))
  if (!rowIntents.has(intent)) {
    return { error: "درخواست نامعتبر است.", intent }
  }
  if (!Number.isInteger(id) || id <= 0) {
    return { error: "شناسه رکورد نامعتبر است.", intent }
  }
  // Every answer that changed something comes back to the view the row was on,
  // so the table is re-read from the server rather than flipped in place.
  const back = () =>
    redirect(strip(`${requestURL(request).pathname}${requestURL(request).search}`))

  try {
    if (intent === "webhook-test") {
      // A test is not a delivery and the service records nothing for it, so the
      // outcome comes back from this one response. Reporting it as history
      // would claim a record the store never wrote.
      const sent = await apiRequest<unknown>("POST", automationApi.testWebhook(id), {
        token,
      })
      return { intent, testResult: testOutcomeOf(readDeliveryRecord(sent.data)) }
    }
    if (intent === "webhook-delete" || intent === "workflow-delete") {
      await apiRequest(
        "DELETE",
        intent === "webhook-delete" ? automationApi.webhook(id) : automationApi.workflow(id),
        { token }
      )
      return back()
    }
    // A switch changes one field and nothing else, which is what makes a
    // one-click switch safe to offer in a row.
    await apiRequest(
      "PATCH",
      intent === "webhook-toggle" ? automationApi.webhook(id) : automationApi.workflow(id),
      { token, body: { enabled: form.get("enabled") === "true" } }
    )
    return back()
  } catch (error) {
    if (isAuthLost(error)) throw loginRedirect(request)
    if (isForbidden(error)) {
      return { error: "شما مجوز این عمل را ندارید.", intent }
    }
    // A refusal is reported in the server's own words — "a workflow needs at
    // least one enabled destination before it can be enabled" — and the row
    // keeps the state the store holds. Nothing here turns a switch on behind
    // the backend's back.
    return {
      error:
        intent === "workflow-toggle" || intent === "workflow-delete"
          ? explainAutomationError(error)
          : explainWebhookError(error),
      intent,
    }
  }
}

export function ErrorBoundary({ error }: Route.ErrorBoundaryProps) {
  return (
    <PageShell title="اتوماسیون">
      <PageError error={error} />
    </PageShell>
  )
}

export default function AutomationPage({
  loaderData,
  actionData,
}: Route.ComponentProps) {
  const navigation = useNavigation()
  const { toast } = useToast()
  const submit = useSubmit()
  const navigate = useNavigate()
  const location = useLocation()
  const [searchParams, setSearchParams] = useSearchParams()

  const listing = loaderData.status === "ok" ? loaderData.listing : null
  // Which tab is open is chosen here, not by the data: the trigger has to light
  // up the moment it is pressed, while the rows behind it are still on their
  // way. The URL is what makes a refresh and a shared link open the same tab,
  // and the two are kept in step below.
  const [tab, setTab] = React.useState<Tab>(listing?.tab ?? TAB_WEBHOOKS)
  const [search, setSearch] = React.useState(listing?.q ?? "")
  const [deleting, setDeleting] = React.useState<
    WebhookItem | WorkflowItem | null
  >(null)
  // The backend's refusal of the last workflow switch, by workflow id. The row
  // shows it and keeps the state the server holds.
  const [toggleError, setToggleError] = React.useState<{
    id: number
    message: string
  } | null>(null)

  // Each tab keeps its own query and page. Switching back to a tab is going
  // back to where it was left, and a query typed for one collection is never
  // carried over to the other.
  const views = React.useRef<Record<Tab, { q: string; page: number }>>({
    [TAB_WEBHOOKS]: {
      q: listing?.tab === TAB_WEBHOOKS ? listing.q : "",
      page: listing?.tab === TAB_WEBHOOKS ? listing.page : 1,
    },
    [TAB_WORKFLOWS]: {
      q: listing?.tab === TAB_WORKFLOWS ? listing.q : "",
      page: listing?.tab === TAB_WORKFLOWS ? listing.page : 1,
    },
  })
  // The tab the page last asked for, and the query the address bar already
  // carries. A change that did not come from here — a back button, a pasted
  // link — is followed; a change that did is not undone while its rows load.
  const askedTab = React.useRef<Tab>(listing?.tab ?? TAB_WEBHOOKS)
  const askedQuery = React.useRef(listing?.q ?? "")

  React.useEffect(() => {
    if (!listing) return
    views.current[listing.tab] = { q: listing.q, page: listing.page }
    if (listing.tab !== askedTab.current) {
      askedTab.current = listing.tab
      askedQuery.current = listing.q
      setTab(listing.tab)
      setSearch(listing.q)
    }
  }, [listing])

  // A typed search is sent once the typing stops, and a new search is a new
  // look at the collection: it starts at its first page.
  React.useEffect(() => {
    if (search === askedQuery.current) return
    const handle = setTimeout(() => {
      askedQuery.current = search
      askedTab.current = tab
      views.current[tab] = { q: search, page: 1 }
      setSearchParams(searchView(tab, search), { preventScrollReset: true })
    }, SEARCH_DEBOUNCE_MS)
    return () => clearTimeout(handle)
  }, [search, tab, setSearchParams])

  const onTabChange = (next: Tab) => {
    if (next === tab) return
    views.current[tab] = {
      q: search,
      page: listing && listing.tab === tab ? listing.page : views.current[tab].page,
    }
    const back = views.current[next]
    askedTab.current = next
    askedQuery.current = back.q
    setTab(next)
    setSearch(back.q)
    setSearchParams(viewParams(next, back.q, back.page), {
      preventScrollReset: true,
    })
  }

  // A successful action answers with a redirect, so the finished intent (read
  // from the submit that just left) is what names the result for the toast.
  // Nothing here claims success on a refusal: an action that failed returns
  // data carrying an error and never redirects.
  const pendingIntent = React.useRef<{
    intent: string
    id: number
    enabled: boolean
  } | null>(null)
  React.useEffect(() => {
    const formData = navigation.formData
    if (formData) {
      pendingIntent.current = {
        intent: String(formData.get("intent") ?? ""),
        id: Number(formData.get("id")),
        enabled: formData.get("enabled") === "true",
      }
    }
  }, [navigation.formData])

  const submitting = navigation.state === "submitting"
  const wasSubmitting = React.useRef(false)
  // One refusal is one report: the same answer arriving twice must not be
  // announced twice.
  const toasted = React.useRef<string | null>(null)
  React.useEffect(() => {
    if (!wasSubmitting.current || submitting) {
      wasSubmitting.current = submitting
      return
    }
    wasSubmitting.current = false
    setDeleting(null)
    const pending = pendingIntent.current
    const intent = pending?.intent
    pendingIntent.current = null

    // A refusal comes back as data, carrying the server's own sentence, and is
    // never reported as a success. A switch the backend refused is shown on its
    // own row, which keeps the state the store holds.
    if (actionData?.error) {
      const key = `${actionData.intent ?? ""}:${actionData.error}`
      if (toasted.current !== key) {
        toasted.current = key
        if (intent === "workflow-toggle" && pending) {
          setToggleError({ id: pending.id, message: actionData.error })
        }
        toast({
          variant: "error",
          title: "عملیات انجام نشد.",
          description: actionData.error,
        })
      }
      return
    }
    toasted.current = null

    if (intent === "webhook-toggle")
      toast({
        variant: "success",
        title: pending?.enabled ? "وب‌هوک فعال شد." : "وب‌هوک غیرفعال شد.",
      })
    else if (intent === "webhook-delete")
      toast({ variant: "success", title: "وب‌هوک حذف شد." })
    else if (intent === "workflow-toggle")
      toast({ variant: "success", title: "تغییر وضعیت ورک‌فلو ذخیره شد." })
    else if (intent === "workflow-delete")
      toast({ variant: "success", title: "ورک‌فلو حذف شد." })
    else if (intent === "webhook-test") {
      // A test is reported as the exchange reported it. It is never a delivery:
      // the service writes nothing to the history for one, so the outcome is
      // the only thing here that may be called a result.
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
  }, [submitting, actionData, toast])

  const onEdit = (webhook: WebhookItem) =>
    navigate(automationRoute.editWebhook(webhook.id), {
      state: automationFormLinkState(location.pathname + location.search),
    })
  const onDelete = (item: WebhookItem | WorkflowItem) => {
    setToggleError(null)
    setDeleting(item)
  }
  const onToggle = (item: WebhookItem | WorkflowItem, enabled: boolean) => {
    setToggleError(null)
    submit(
      {
        intent: isWebhook(item) ? "webhook-toggle" : "workflow-toggle",
        id: String(item.id),
        enabled: String(enabled),
      },
      { method: "post" }
    )
  }
  const onTest = (webhook: WebhookItem) =>
    submit({ intent: "webhook-test", id: String(webhook.id) }, { method: "post" })

  if (loaderData.status !== "ok") {
    return (
      <PageShell
        title="اتوماسیون"
        description="رویدادهای کانال‌ها را به وب‌هوک و ورک‌فلو بسپارید؛ اجراها در پس‌زمینه انجام و ثبت می‌شوند."
      >
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

  return (
    <>
      <AutomationView
        listing={listing}
        channels={loaderData.channels}
        tab={tab}
        search={search}
        onTabChange={onTabChange}
        onSearchChange={setSearch}
        hrefForPage={(page) => hrefOf(viewParams(tab, search, page))}
        onEdit={onEdit}
        onToggle={onToggle}
        onDelete={onDelete}
        onTest={onTest}
        toggleError={toggleError}
      />
      <ConfirmDialog
        open={deleting !== null}
        onOpenChange={(open) => !open && setDeleting(null)}
        title={deleting && isWebhook(deleting) ? "حذف وب‌هوک" : "حذف ورک‌فلو"}
        description={
          deleting
            ? isWebhook(deleting)
              ? `وب‌هوک «${rowName(deleting)}» حذف شود؟ تاریخچه تحویل‌های آن هم پاک می‌شود و قابل بازگرداندن نیست.`
              : "این ورک‌فلو حذف شود؟ تاریخچه اجراهای آن از بین می‌رود و برای این کانال دوباره باید ورک‌فلو بسازید."
            : ""
        }
        confirmLabel="حذف"
        pending={submitting}
        error={
          deleting && actionData?.intent?.endsWith("-delete")
            ? actionData.error ?? null
            : null
        }
        onConfirm={() => {
          if (!deleting) return
          submit(
            {
              intent: isWebhook(deleting) ? "webhook-delete" : "workflow-delete",
              id: String(deleting.id),
            },
            { method: "post" }
          )
        }}
      />
    </>
  )
}

// A row of the two collections is told apart by the fields only one of them
// carries, so no row action can reach the wrong collection's endpoint.
function isWebhook(item: WebhookItem | WorkflowItem): item is WebhookItem {
  return "authMode" in item
}

function rowName(item: WebhookItem | WorkflowItem): string {
  return isWebhook(item)
    ? item.name
    : item.channel?.title || `کانال ${item.managedChannelID}`
}

// ---------------------------------------------------------------------------
// The page itself: its header, one toolbar, and the open collection's table
// under it. It takes everything it shows as props, so the whole screen is
// provable from a render and the workspace has no DOM to click in.
// ---------------------------------------------------------------------------

export function AutomationView({
  listing,
  channels,
  tab,
  search,
  onTabChange,
  onSearchChange,
  hrefForPage,
  onEdit,
  onToggle,
  onDelete,
  onTest,
  toggleError,
}: {
  listing: Listing | null
  channels: SourceChannelOption[]
  tab: Tab
  search: string
  onTabChange: (tab: Tab) => void
  onSearchChange: (search: string) => void
  hrefForPage: (page: number) => string
  onEdit: (webhook: WebhookItem) => void
  onToggle: (item: WebhookItem | WorkflowItem, enabled: boolean) => void
  onDelete: (item: WebhookItem | WorkflowItem) => void
  onTest: (webhook: WebhookItem) => void
  toggleError?: { id: number; message: string } | null
}) {
  // The rows on screen are the ones the address bar asked for. While a
  // different tab's rows are what has arrived, the table says it is loading
  // rather than showing one collection under the other's heading.
  const open = listing !== null && listing.tab === tab ? listing : null
  return (
    <PageShell
      title="اتوماسیون"
      description="رویدادهای کانال‌ها را به وب‌هوک و ورک‌فلو بسپارید؛ اجراها در پس‌زمینه انجام و ثبت می‌شوند."
      actions={<AutomationAddButton tab={tab} canManage={open?.canManage ?? false} />}
    >
      <Tabs value={tab} onValueChange={(value) => onTabChange(value as Tab)}>
        <AutomationToolbar
          tab={tab}
          search={search}
          onSearchChange={onSearchChange}
        />
        <TabsContent value={TAB_WEBHOOKS}>
          {open?.tab === TAB_WEBHOOKS ? (
            <AutomationContent
              listing={open}
              channels={channels}
              hrefForPage={hrefForPage}
              onEdit={onEdit}
              onToggle={onToggle}
              onDelete={onDelete}
              onTest={onTest}
            />
          ) : (
            <LoadingState label="در حال بارگذاری وب‌هوک‌ها…" />
          )}
        </TabsContent>
        <TabsContent value={TAB_WORKFLOWS}>
          {open?.tab === TAB_WORKFLOWS ? (
            <AutomationContent
              listing={open}
              channels={channels}
              hrefForPage={hrefForPage}
              onEdit={onEdit}
              onToggle={onToggle}
              onDelete={onDelete}
              onTest={onTest}
              toggleError={toggleError}
            />
          ) : (
            <LoadingState label="در حال بارگذاری ورک‌فلوها…" />
          )}
        </TabsContent>
      </Tabs>
    </PageShell>
  )
}

// The one button that opens the create form, and the only Add button the page
// has. It names the collection the open tab holds and points at that
// collection's own create route: a create is a page of its own, not a dialog
// dropped over the list. It carries the view it was pressed from, so the form
// knows where to come back to.
export function AutomationAddButton({
  tab,
  canManage,
}: {
  tab: Tab
  canManage: boolean
}) {
  const location = useLocation()
  if (!canManage) return null
  return (
    <Button
      render={
        <Link
          to={
            tab === TAB_WEBHOOKS
              ? automationRoute.newWebhook
              : automationRoute.newWorkflow
          }
          state={automationFormLinkState(location.pathname + location.search)}
        />
      }
    >
      <PlusIcon data-icon="inline-start" />
      {tab === TAB_WEBHOOKS ? "افزودن وب‌هوک" : "افزودن ورک‌فلو"}
    </Button>
  )
}

// Tabs and the search for the open tab, one row. The list is only as wide as
// its two triggers so the search can sit beside it instead of pushing it across
// the page.
export function AutomationToolbar({
  tab,
  search,
  onSearchChange,
}: {
  tab: Tab
  search: string
  onSearchChange: (search: string) => void
}) {
  return (
    <div className="flex items-center gap-2 sm:gap-3">
      {/* No className on the triggers: the shared Tabs owns the selected
          background and shadow, and a page must not restyle it. */}
      <TabsList className="w-fit shrink-0">
        <TabsTrigger value={TAB_WEBHOOKS}>وب‌هوک‌ها</TabsTrigger>
        <TabsTrigger value={TAB_WORKFLOWS}>ورک‌فلوها</TabsTrigger>
      </TabsList>
      <SearchInput
        name="q"
        value={search}
        onChange={onSearchChange}
        placeholder={
          tab === TAB_WEBHOOKS ? "جستجو در وب‌هوک‌ها..." : "جستجو در ورک‌فلوها..."
        }
        className="w-full min-w-0 sm:max-w-sm"
      />
    </div>
  )
}

// The open collection, whole: every record on this page of it, and the way to
// the rest. It is the collection, not a sample of it.
export function AutomationContent({
  listing,
  channels,
  hrefForPage,
  onEdit,
  onToggle,
  onDelete,
  onTest,
  toggleError,
}: {
  listing: Listing
  channels: SourceChannelOption[]
  hrefForPage: (page: number) => string
  onEdit: (webhook: WebhookItem) => void
  onToggle: (item: WebhookItem | WorkflowItem, enabled: boolean) => void
  onDelete: (item: WebhookItem | WorkflowItem) => void
  onTest: (webhook: WebhookItem) => void
  toggleError?: { id: number; message: string } | null
}) {
  const titles = new Map<number, string>()
  for (const channel of channels) titles.set(channel.id, channel.label)
  return (
    <div className="flex flex-col gap-4">
      <div className="rounded-xl border bg-card">
        {listing.rows.length === 0 ? (
          <EmptyState
            icon={listing.tab === TAB_WEBHOOKS ? WebhookIcon : WorkflowIcon}
            title={
              listing.q
                ? "نتیجه‌ای پیدا نشد"
                : listing.tab === TAB_WEBHOOKS
                  ? "هنوز وب‌هوکی وجود ندارد"
                  : "هنوز ورک‌فلویی وجود ندارد"
            }
            description={
              listing.q
                ? "عبارت جستجو را تغییر دهید یا حذف کنید."
                : listing.tab === TAB_WEBHOOKS
                  ? "اولین وب‌هوک را بسازید تا رویدادها به بیرون فرستاده شوند."
                  : "اولین ورک‌فلو را بسازید تا پیام‌های تازه خودکار منتشر شوند."
            }
          />
        ) : listing.tab === TAB_WEBHOOKS ? (
          <WebhooksTable
            webhooks={listing.rows}
            channels={channels}
            canManage={listing.canManage}
            onEdit={onEdit}
            onToggle={(webhook) => onToggle(webhook, !webhook.enabled)}
            onDelete={onDelete}
            onTest={onTest}
          />
        ) : (
          <WorkflowsTable
            workflows={listing.rows}
            channelTitles={titles}
            canManage={listing.canManage}
            onDelete={onDelete}
            onToggle={onToggle}
            toggleError={toggleError}
          />
        )}
      </div>
      <AutomationPagination listing={listing} hrefForPage={hrefForPage} />
    </div>
  )
}

// Where in the collection this page is, and the two steps either way. The
// backend counts the collection, so the totals are its own.
export function AutomationPagination({
  listing,
  hrefForPage,
}: {
  listing: ListingFrame
  hrefForPage: (page: number) => string
}) {
  const previous = listing.page > 1
  const next = listing.hasMore
  return (
    <nav
      className="flex flex-wrap items-center justify-between gap-2"
      aria-label="صفحه‌بندی"
    >
      <p className="text-xs text-muted-foreground">
        صفحه {faDigits(listing.page)}
        {listing.totalPages ? ` از ${faDigits(listing.totalPages)}` : ""}
        {listing.total === null ? "" : ` · ${faDigits(listing.total)} رکورد`}
      </p>
      <div className="flex items-center gap-2">
        <Button
          variant="outline"
          size="sm"
          disabled={!previous}
          render={previous ? <Link to={hrefForPage(listing.page - 1)} /> : undefined}
        >
          صفحه قبل
        </Button>
        <Button
          variant="outline"
          size="sm"
          disabled={!next}
          render={next ? <Link to={hrefForPage(listing.page + 1)} /> : undefined}
        >
          صفحه بعد
        </Button>
      </div>
    </nav>
  )
}
