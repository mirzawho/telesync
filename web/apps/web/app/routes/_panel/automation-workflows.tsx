import {
  Link,
  redirect,
  useNavigation,
  useSearchParams,
  useSubmit,
} from "react-router"
import { MoreHorizontalIcon, PlusIcon, WorkflowIcon } from "lucide-react"
import * as React from "react"

import {
  EnabledBadge,
  RunStatusBadge,
  SwitchState,
} from "@/components/automation/status"
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
import { Switch } from "@workspace/ui/components/switch"
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
  explainAutomationError,
  readWorkflow,
  workflowRows,
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
import { requestURL, strip } from "@/path"

import type { Route } from "./+types/automation-workflows"

const PAGE_SIZE = 20

export const meta = () => pageMeta("ورک‌فلوها")

export const handle = section("اتوماسیون")

const ACTIONS_HEAD = "sticky left-0 z-20 w-12 bg-background text-end"
const ACTIONS_CELL = "sticky left-0 z-10 bg-background text-end"

type LoaderData =
  | {
      status: "ok"
      workflows: WorkflowItem[]
      // Local managed_channels.id → title. A workflow names its source by id,
      // so the row shows the channel's own name and never the number alone.
      channelTitles: Map<number, string>
      currentPage: number
      hasMore: boolean
      q: string
      canManage: boolean
    }
  | { status: "forbidden"; message: string }
  | { status: "error"; message: string }

function positiveInt(raw: string | null): number {
  const value = Number(raw)
  return Number.isInteger(value) && value > 0 ? value : 1
}

export async function clientLoader({
  request,
}: Route.LoaderArgs): Promise<LoaderData> {
  const { token, me } = await requireSession(request)
  // A restricted administrator is told so from the session itself: the page
  // must never fall back to a silently empty list because the calls returned
  // nothing.
  const denied = automationAccessDenied(me)
  if (denied) return { status: "forbidden", message: denied }
  const url = requestURL(request)
  const q = (url.searchParams.get("q") ?? "").trim()
  const currentPage = positiveInt(url.searchParams.get("page"))

  try {
    const [list, channels] = await Promise.all([
      apiRequest<unknown>("GET", automationApi.workflows, {
        token,
        query: { page: currentPage, per_page: PAGE_SIZE, q: q || undefined },
      }),
      apiRequest<{ channels?: { id: number; title: string }[] }>(
        "GET",
        "/local/channels",
        { token, query: { page: 1, per_page: 100 } }
      ),
    ])
    const rows = workflowRows(list.data)
    const titles = new Map<number, string>()
    for (const channel of channels.data?.channels ?? []) {
      titles.set(channel.id, channel.title)
    }
    return {
      status: "ok",
      workflows: rows.map(readWorkflow),
      channelTitles: titles,
      currentPage,
      hasMore: list.meta.has_more ?? false,
      q,
      canManage: Boolean(me.access?.automation),
    }
  } catch (error) {
    if (isAuthLost(error)) throw loginRedirect(request)
    if (isForbidden(error)) {
      return {
        status: "forbidden",
        message: "شما مجوز مشاهده ورک‌فلوها را ندارید.",
      }
    }
    return { status: "error", message: errorMessage(error) }
  }
}

interface ActionData {
  error?: string
  intent?: string
  values?: { id?: string }
}

export async function clientAction({
  request,
}: Route.ActionArgs): Promise<ActionData | Response> {
  const { token } = await requireSession(request)
  const form = await request.formData()
  const intent = String(form.get("intent") ?? "")
  const rawId = String(form.get("id") ?? "")
  const id = Number(rawId)
  if (intent !== "delete" && intent !== "set-enabled") {
    return { error: "درخواست نامعتبر است.", intent, values: { id: rawId } }
  }
  if (!rawId || !Number.isInteger(id) || id <= 0) {
    return { error: "شناسه ورک‌فلو نامعتبر است.", intent, values: { id: rawId } }
  }
  try {
    if (intent === "set-enabled") {
      // One field only, so switching a workflow on never rewrites its
      // destinations or its AI settings behind the operator's back. The answer
      // is a redirect: the list is re-read from the server afterwards, so the
      // badge shows what was actually stored and nothing flips optimistically.
      await apiRequest("PATCH", automationApi.workflow(id), {
        token,
        body: { enabled: form.get("enabled") === "true" },
      })
      return redirect(
        strip(`${requestURL(request).pathname}${requestURL(request).search}`)
      )
    }
    await apiRequest("DELETE", automationApi.workflow(id), { token })
    return redirect(
      strip(`${requestURL(request).pathname}${requestURL(request).search}`)
    )
  } catch (error) {
    if (isAuthLost(error)) throw loginRedirect(request)
    if (isForbidden(error)) {
      return { error: "شما مجوز این عمل را ندارید.", intent, values: { id: rawId } }
    }
    // A refusal is reported in the server's own words — "a workflow needs at
    // least one enabled destination before it can be enabled", "enabling a
    // workflow needs an authenticated account" — and the row keeps its stored
    // state. Nothing here turns the switch on behind the backend's back.
    return {
      error: explainAutomationError(error),
      intent,
      values: { id: rawId },
    }
  }
}

export function ErrorBoundary({ error }: Route.ErrorBoundaryProps) {
  return (
    <>
      <PageHeader title="ورک‌فلوها" />
      <PageError error={error} />
    </>
  )
}

export function WorkflowsTable({
  workflows,
  channelTitles,
  canManage,
  onDelete,
  onToggle,
  toggleError,
}: {
  workflows: WorkflowItem[]
  channelTitles: Map<number, string>
  canManage: boolean
  onDelete: (workflow: WorkflowItem) => void
  onToggle: (workflow: WorkflowItem, enabled: boolean) => void
  // The backend's refusal of the last toggle, by workflow id. The row shows it
  // and keeps the state the server holds: a switch is never left looking active
  // for a workflow the server said it would not run.
  toggleError?: { id: number; message: string } | null
}) {
  const titleOf = (workflow: WorkflowItem) =>
    workflow.channel?.title ??
    channelTitles.get(workflow.managedChannelID) ??
    `کانال ${workflow.managedChannelID}`
  return (
    <Table>
      <TableHeader>
        <TableRow>
          <TableHead>کانال مبدأ</TableHead>
          <TableHead>وضعیت</TableHead>
          <TableHead>هوش مصنوعی</TableHead>
          <TableHead>مقاصد</TableHead>
          <TableHead>آخرین اجرا</TableHead>
          <TableHead>نتیجه</TableHead>
          <TableHead className={ACTIONS_HEAD}>
            <span className="sr-only">عملیات</span>
          </TableHead>
        </TableRow>
      </TableHeader>
      <TableBody>
        {workflows.map((workflow) => (
          <TableRow key={workflow.id}>
            <TableCell className="font-medium">
              <Link
                to={automationRoute.runs(workflow.id)}
                className="underline-offset-4 hover:underline"
              >
                {titleOf(workflow)}
              </Link>
            </TableCell>
            <TableCell>
              <span className="flex flex-col gap-1">
                <span className="flex items-center gap-2">
                  <EnabledBadge enabled={workflow.enabled} />
                  {canManage && (
                    <Switch
                      checked={workflow.enabled}
                      // No optimistic flip: the badge changes only when the
                      // loader brings back what the server stored.
                      onCheckedChange={(enabled) => onToggle(workflow, enabled)}
                      aria-label={`فعال‌سازی ورک‌فلوی ${titleOf(workflow)}`}
                    />
                  )}
                </span>
                {toggleError && toggleError.id === workflow.id && (
                  <span
                    role="alert"
                    className="max-w-56 text-xs font-medium text-destructive"
                  >
                    {toggleError.message}
                  </span>
                )}
              </span>
            </TableCell>
            <TableCell>
              <SwitchState
                on={workflow.aiEnabled}
                onLabel="روشن"
                offLabel="خاموش"
              />
            </TableCell>
            <TableCell>
              {workflow.destinations.length === 0 ? (
                <span className="text-xs text-muted-foreground">—</span>
              ) : (
                <span className="flex flex-wrap gap-1">
                  {(
                    Object.entries(destinationTypeLabels) as [
                      keyof typeof destinationTypeLabels,
                      string,
                    ][]
                  )
                    .map(([type, label]) => {
                      const count = workflow.destinations.filter(
                        (destination) => destination.type === type
                      ).length
                      return count > 0 ? (
                        <Badge key={type} variant="outline">
                          {label} {faDigits(count)}
                        </Badge>
                      ) : null
                    })
                    .filter(Boolean)}
                </span>
              )}
            </TableCell>
            <TableCell className="text-muted-foreground">
              {workflow.lastRun ? faDateTime(workflow.lastRun.createdAt) : "—"}
            </TableCell>
            <TableCell>
              {workflow.lastRun ? (
                <RunStatusBadge status={workflow.lastRun.status} />
              ) : (
                <span className="text-xs text-muted-foreground">—</span>
              )}
            </TableCell>
            <TableCell className={ACTIONS_CELL}>
              {canManage && (
                <DropdownMenu>
                  <DropdownMenuTrigger
                    render={
                      <Button
                        variant="ghost"
                        size="icon-sm"
                        aria-label={`عملیات ورک‌فلوی ${titleOf(workflow)}`}
                      />
                    }
                  >
                    <MoreHorizontalIcon />
                  </DropdownMenuTrigger>
                  <DropdownMenuContent align="end">
                    <DropdownMenuItem
                      render={<Link to={automationRoute.workflow(workflow.id)} />}
                    >
                      ویرایش
                    </DropdownMenuItem>
                    <DropdownMenuItem
                      render={<Link to={automationRoute.runs(workflow.id)} />}
                    >
                      تاریخچه اجراها
                    </DropdownMenuItem>
                    <DropdownMenuSeparator />
                    <DropdownMenuItem
                      variant="destructive"
                      onClick={() => onDelete(workflow)}
                    >
                      حذف
                    </DropdownMenuItem>
                  </DropdownMenuContent>
                </DropdownMenu>
              )}
            </TableCell>
          </TableRow>
        ))}
      </TableBody>
    </Table>
  )
}

export default function AutomationWorkflows({
  loaderData,
  actionData,
}: Route.ComponentProps) {
  const navigation = useNavigation()
  const { toast } = useToast()
  const submit = useSubmit()
  const [searchParams, setSearchParams] = useSearchParams()
  const loading = navigation.state === "loading"
  const submitting = navigation.state === "submitting"

  const [deleting, setDeleting] = React.useState<WorkflowItem | null>(null)
  // The workflow whose toggle the backend refused last, with the reason. The
  // row keeps the state the server holds and says why it would not change.
  const [toggleError, setToggleError] = React.useState<{
    id: number
    message: string
  } | null>(null)
  const [q, setQ] = React.useState(
    loaderData.status === "ok" ? loaderData.q : ""
  )

  React.useEffect(() => {
    if (loaderData.status === "ok") setQ(loaderData.q)
  }, [loaderData])

  // A successful action answers with a redirect, so the finished intent (read
  // from the submit that just left) is what names the result. Nothing here
  // claims a result for an intent that did not happen.
  const pendingIntent = React.useRef<string | null>(null)
  React.useEffect(() => {
    const formData = navigation.formData
    if (formData) {
      pendingIntent.current = String(formData.get("intent") ?? "")
    }
  }, [navigation.formData])

  const wasSubmitting = React.useRef(false)
  React.useEffect(() => {
    if (wasSubmitting.current && !submitting && !actionData?.error) {
      setDeleting(null)
      setToggleError(null)
      const intent = pendingIntent.current
      if (intent === "delete") toast({ variant: "success", title: "ورک‌فلو حذف شد." })
      else if (intent === "set-enabled")
        toast({
          variant: "success",
          title: "تغییر وضعیت ورک‌فلو ذخیره شد.",
        })
      pendingIntent.current = null
    }
    wasSubmitting.current = submitting
  }, [submitting, actionData, toast])

  React.useEffect(() => {
    if (!actionData?.error) return
    toast({
      variant: "error",
      title: "عملیات انجام نشد.",
      description: actionData.error,
    })
    const id = Number(actionData.values?.id ?? 0)
    if (actionData.intent === "set-enabled" && Number.isInteger(id) && id > 0) {
      // The switch stays where the server has it: the refusal is shown on the
      // row and the flag is never flipped to look right.
      setToggleError({ id, message: actionData.error })
      return
    }
    if (actionData.values?.id) {
      const known =
        loaderData.status === "ok"
          ? loaderData.workflows.find((workflow) => workflow.id === id)
          : undefined
      if (known) setDeleting(known)
    }
  }, [actionData, loaderData, toast])

  const onSearch = (event: React.FormEvent<HTMLFormElement>) => {
    event.preventDefault()
    const next = new URLSearchParams(searchParams)
    if (q.trim()) next.set("q", q.trim())
    else next.delete("q")
    next.delete("page")
    setSearchParams(next)
  }

  return (
    <div className="flex flex-col gap-6">
      <PageHeader
        title="ورک‌فلوها"
        description="برای هر کانال مبدأ یک ورک‌فلو بسازید تا پیام تازه، پس از ویرایش، در مقاصد منتشر شود."
        actions={
          loaderData.status === "ok" && loaderData.canManage ? (
            <Button render={<Link to={automationRoute.newWorkflow} />}>
              <PlusIcon data-icon="inline-start" />
              افزودن ورک‌فلو
            </Button>
          ) : null
        }
      />

      {loaderData.status !== "ok" ? (
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
              placeholder="جستجوی ورک‌فلو…"
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
              <LoadingState label="در حال بارگذاری ورک‌فلوها…" />
            ) : loaderData.workflows.length === 0 ? (
              <EmptyState
                icon={WorkflowIcon}
                title={loaderData.q ? "نتیجه‌ای پیدا نشد" : "هنوز ورک‌فلویی وجود ندارد"}
                description={
                  loaderData.q
                    ? "عبارت جستجو را تغییر دهید یا حذف کنید."
                    : "اولین ورک‌فلو را بسازید تا پیام‌های تازه خودکار منتشر شوند."
                }
                action={
                  !loaderData.q && loaderData.canManage ? (
                    <Button
                      variant="outline"
                      size="sm"
                      render={<Link to={automationRoute.newWorkflow} />}
                    >
                      <PlusIcon data-icon="inline-start" />
                      افزودن ورک‌فلو
                    </Button>
                  ) : undefined
                }
              />
            ) : (
              <WorkflowsTable
                workflows={loaderData.workflows}
                channelTitles={loaderData.channelTitles}
                canManage={loaderData.canManage}
                onDelete={setDeleting}
                onToggle={(workflow, enabled) =>
                  submit(
                    {
                      intent: "set-enabled",
                      id: String(workflow.id),
                      enabled: String(enabled),
                    },
                    { method: "post" }
                  )
                }
                toggleError={toggleError}
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

      <ConfirmDialog
        open={deleting !== null}
        onOpenChange={(open) => !open && setDeleting(null)}
        title="حذف ورک‌فلو"
        description={
          deleting
            ? "این ورک‌فلو حذف شود؟ تاریخچه اجراهای آن از بین می‌رود و برای این کانال دوباره باید ورک‌فلو بسازید."
            : ""
        }
        confirmLabel="حذف"
        pending={submitting}
        error={actionData?.intent === "delete" ? actionData.error : null}
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
