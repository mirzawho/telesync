import {
  Link,
  useBlocker,
  useLocation,
  useNavigate,
  useNavigation,
  useSubmit,
} from "react-router"
import { ArrowRightIcon, WorkflowIcon } from "lucide-react"
import * as React from "react"

import {
  WorkflowForm,
  isCompleteDestination,
  trimChannels,
  type ChannelOptions,
  type NamedOption,
  type TakenWorkflow,
} from "@/components/automation/workflow-form"
import type { SourceChannel } from "@/components/automation/source-selection"
import {
  emptySiteTerms,
  type SiteTerms,
} from "@/components/automation/workflow-wordpress-options"
import { EmptyState, ErrorState, LoadingState } from "@/components/states"
import { Button } from "@workspace/ui/components/button"
import { useToast } from "@workspace/ui/components/toast"

import {
  automationApi,
  automationReturnTo,
  automationRoute,
  emptyWorkflowDraft,
  errorCodeOf,
  explainAutomationError,
  isDuplicateWorkflow,
  isEnableRefusal,
  readWorkflow,
  readWorkflowRecord,
  workflowBody,
  workflowDraftOf,
  workflowRows,
  type DestinationDraft,
  type WorkflowDraft,
  type WorkflowItem,
} from "@/lib/automation"
import { isAuthLost, isForbidden, loginRedirect } from "@/lib/automation-access"
import { apiRequest, errorMessage } from "@/lib/api"
import { PageError, pageMeta, section } from "@/lib/page"
import { PageHeader } from "@/components/page-header"
import { getToken, requireSession } from "@/lib/session"
import type { TelegramServer, WordPressSite } from "@/lib/types"

import type { Route } from "./+types/automation-workflow-form"

// The workflow create/edit page.
//
// One module answers both addresses: `/automation/workflows/new` has no
// `workflowId` and creates, `/automation/workflows/:workflowId/edit` names the
// record it edits. There is one form and one copy of it, so the two can never
// drift apart.
//
// Every write lands through `workflowBody`, which is the only place the wire's
// field names are written. What the page shows after a save — and after a
// reload of that same URL — is what the server kept, not what the form last
// held.

export const meta = ({ params }: Route.MetaArgs) =>
  pageMeta(isCreation(params.workflowId) ? "افزودن ورک‌فلو" : "ویرایش ورک‌فلو")

export const handle = section("اتوماسیون")

type LoaderData =
  | {
      status: "ok"
      creating: boolean
      workflow: WorkflowItem | null
      sourceChannels: SourceChannel[]
      taken: Record<number, TakenWorkflow>
      servers: TelegramServer[]
      sites: NamedOption[]
      canManage: boolean
    }
  | { status: "missing" }
  | { status: "forbidden"; message: string }
  | { status: "error"; message: string }

// `new` is the one non-numeric value of the id segment: it means "no workflow
// yet". Anything else that is not a positive integer is not a record.
function validID(raw: string | undefined): number | null {
  return raw !== undefined && /^[0-9]+$/.test(raw) ? Number(raw) : null
}

function isCreation(raw: string | undefined): boolean {
  return raw === undefined || raw === "new"
}

// Where this page goes when it is done. A link that came from the listing
// carries the view it was on — the tab, the query, the page — so a save and a
// cancel both land back in that exact view; a bookmark, a paste or a refresh
// has no such history and falls back to the Workflows tab.
export function workflowFormReturnTo(state: unknown): string {
  return automationReturnTo(state, "workflows")
}

export async function clientLoader({
  params,
  request,
}: Route.LoaderArgs): Promise<LoaderData> {
  const { token, me } = await requireSession(request)
  // A restricted administrator is told so from the session itself: this page
  // must never fall back to a silently empty form because the calls returned
  // nothing.
  const canManage = me.access?.automation !== false
  if (!canManage) {
    return {
      status: "forbidden",
      message: "شما مجوز مشاهده ورک‌فلوها را ندارید.",
    }
  }
  const creating = isCreation(params.workflowId)
  const id = validID(params.workflowId)
  if (!creating && id === null) return { status: "missing" }

  try {
    const [channels, workflows, servers, sites] = await Promise.all([
      apiRequest<{
        channels?: { id: number; title: string; client_key?: string }[]
      }>("GET", "/local/channels", {
        token,
        query: { page: 1, per_page: 100 },
      }),
      apiRequest<unknown>("GET", automationApi.workflows, {
        token,
        query: { page: 1, per_page: 100 },
      }),
      apiRequest<{ servers?: TelegramServer[] }>("GET", "/telegram", { token }),
      apiRequest<{ sites?: WordPressSite[] }>("GET", "/local/wordpress", {
        token,
      }),
    ])

    const rows = workflowRows(workflows.data)
    const all = rows.map(readWorkflow)
    // The schema allows one workflow per managed channel, and this page refuses
    // to offer a second one: a taken channel names its workflow instead. The
    // workflow being edited is not one of them — its own source channel is not
    // "taken", it is the channel this record already holds.
    const taken: Record<number, TakenWorkflow> = {}
    for (const workflow of all) {
      if (workflow.managedChannelID > 0 && workflow.id !== id) {
        taken[workflow.managedChannelID] = {
          id: workflow.id,
          channelLabel:
            workflow.channel?.title ?? `کانال ${workflow.managedChannelID}`,
        }
      }
    }

    let workflow: WorkflowItem | null = null
    if (id !== null) {
      const found = all.find((row) => row.id === id)
      // Not on the first page of the list: read it on its own.
      workflow =
        found ??
        readWorkflowRecord(
          (
            await apiRequest<unknown>("GET", automationApi.workflow(id), {
              token,
            })
          ).data
        )
      // The list said nothing and the record's own URL answered 404: it is gone.
      if (workflow.id <= 0) return { status: "missing" }
    }

    return {
      status: "ok",
      creating,
      workflow,
      sourceChannels: (channels.data?.channels ?? []).map((channel) => ({
        id: channel.id,
        label: channel.title,
        clientKey: channel.client_key ?? "",
        detail: channel.client_key,
      })),
      taken,
      servers: servers.data?.servers ?? [],
      sites: (sites.data?.sites ?? []).map((site) => ({
        id: site.id,
        label: site.title,
        detail: site.domain,
      })),
      // `automation.view` is what the summary reports. An account whose summary
      // omits the flag is not denied by it — the API stays the authority and its
      // own refusal is still reported. Everything below only runs for an
      // account that got past that, so the form is offered here.
      canManage,
    }
  } catch (error) {
    if (isAuthLost(error)) throw loginRedirect(request)
    if (isForbidden(error)) {
      return {
        status: "forbidden",
        message: "شما مجوز ویرایش ورک‌فلوها را ندارید.",
      }
    }
    // A deleted workflow is a missing one: the page says so and links back, it
    // does not report a failure the operator cannot act on.
    if (errorCodeOf(error) === "not_found") return { status: "missing" }
    return { status: "error", message: errorMessage(error) }
  }
}

interface ActionData {
  error?: string
  intent?: string
  saved?: boolean
  // A refusal that names one part of the configuration is shown beside it as
  // well as in the page's error slot.
  fieldErrors?: Partial<
    Record<"source" | "destinations" | "ai" | "status", string>
  >
  values?: { managed_channel_id?: string }
  // The workflow that already holds this channel, when a write was refused as a
  // duplicate. A 409 is the one refusal the user can act on by going somewhere
  // else, so it is answered with a way to get there rather than only with "no".
  conflictWorkflowID?: number | null
  conflictChannelLabel?: string
  // The server refused to switch this workflow on: the enabled flag must go
  // back to the value the store holds rather than staying on in the form.
  revertEnabled?: boolean
}

// Where a refusal points. The server's own code and its own sentence decide:
// nothing here paraphrases a reason the service already named.
function fieldErrorsOf(error: unknown): ActionData["fieldErrors"] {
  const message = explainAutomationError(error)
  const code = errorCodeOf(error)
  if (code === "automation_source_forbidden") return { source: message }
  if (code === "automation_destination_forbidden")
    return { destinations: message }
  if (isEnableRefusal(error)) return { status: message }
  if (isDuplicateWorkflow(error)) return { source: message }
  // The schema names the destination it refused as `destinations[n] …`.
  if (/destinations\[\d+\]/.test(message)) return { destinations: message }
  return {}
}

export async function clientAction({
  request,
  params,
}: Route.ActionArgs): Promise<ActionData> {
  const { token } = await requireSession(request)
  const form = await request.formData()
  const creating = isCreation(params.workflowId)
  const id = validID(params.workflowId)
  const intent = creating ? "create" : "update"
  if (!creating && id === null) {
    return { error: "شناسه ورک‌فلو نامعتبر است.", intent }
  }
  const rawChannel = String(form.get("managed_channel_id") ?? "")
  const managedChannelID = Number(rawChannel)
  const destinations = readDestinations(form.get("destinations"))

  const draft: WorkflowDraft = {
    managedChannelID:
      Number.isInteger(managedChannelID) && managedChannelID > 0
        ? managedChannelID
        : null,
    enabled: form.get("enabled") === "true",
    aiEnabled: form.get("ai_enabled") === "true",
    aiPrompt: String(form.get("ai_prompt") ?? ""),
    aiSendImages: form.get("ai_send_images") === "true",
    aiPublishOnFailure: form.get("ai_publish_on_failure") === "true",
    destinations,
  }

  if (draft.managedChannelID === null) {
    return {
      error: "کانال مبدأ را انتخاب کنید.",
      intent,
      fieldErrors: { source: "کانال مبدأ را انتخاب کنید." },
      values: { managed_channel_id: rawChannel },
    }
  }

  // A destination that names no channel and no site is not a destination: it is
  // never written, and a save that would carry one is refused here rather than
  // posting an id the schema would reject or, worse, store.
  const incomplete = destinations.filter(
    (destination) => !isCompleteDestination(destination)
  )
  if (incomplete.length > 0) {
    return {
      error:
        "دست‌کم یک مقصد هنوز کامل نیست. اتصال و کانال مقصد را انتخاب کنید، یا آن مقصد را حذف کنید.",
      intent,
      fieldErrors: {
        destinations:
          "دست‌کم یک مقصد هنوز کامل نیست. اتصال و کانال مقصد را انتخاب کنید، یا آن مقصد را حذف کنید.",
      },
      values: { managed_channel_id: rawChannel },
    }
  }

  try {
    const body = workflowBody(draft)
    if (creating) {
      await apiRequest("POST", automationApi.workflows, { token, body })
    } else {
      await apiRequest("PATCH", automationApi.workflow(id as number), {
        token,
        body,
      })
    }
    // Nothing here claims a result for a write that did not happen: the caller
    // only leaves the page when this comes back without an error.
    return { intent, saved: true }
  } catch (error) {
    if (isAuthLost(error)) throw loginRedirect(request)
    // Every refusal is answered in the service's own words, and one that names
    // a part of the configuration is shown beside that part as well.
    const explained = explainAutomationError(error)
    const fieldErrors = fieldErrorsOf(error)
    // A duplicate is the one refusal the user can act on here, so it names the
    // workflow that holds this channel instead of only saying no.
    if (isDuplicateWorkflow(error)) {
      const existing = await findWorkflowForChannel(
        token,
        draft.managedChannelID
      )
      return {
        error: explained,
        intent,
        fieldErrors,
        values: { managed_channel_id: rawChannel },
        conflictWorkflowID: existing?.id ?? null,
        conflictChannelLabel: existing?.channelLabel,
      }
    }
    // The server refused to switch this workflow on. Nothing here may leave the
    // switch looking active: the refusal is shown in the server's own words and
    // the caller puts the flag back where the store has it.
    return {
      error: explained,
      intent,
      fieldErrors,
      values: { managed_channel_id: rawChannel },
      ...(isEnableRefusal(error) ? { revertEnabled: true } : {}),
    }
  }
}

// findWorkflowForChannel names the workflow that already holds one source
// channel, so a duplicate refusal can link straight to it. The list is the only
// place that knows the pairing, and this runs on the conflict path alone.
async function findWorkflowForChannel(
  token: string | null,
  managedChannelID: number | null
): Promise<{ id: number; channelLabel: string } | null> {
  if (managedChannelID === null) return null
  try {
    const page = await apiRequest<unknown>("GET", automationApi.workflows, {
      token,
      query: { page: 1, per_page: 100 },
    })
    for (const raw of workflowRows(page.data)) {
      const workflow = readWorkflow(raw)
      if (workflow.managedChannelID === managedChannelID) {
        return {
          id: workflow.id,
          channelLabel:
            workflow.channel?.title ?? `کانال ${workflow.managedChannelID}`,
        }
      }
    }
  } catch {
    // A lookup that fails leaves the refusal reported without a link; it never
    // turns a refusal into a success.
  }
  return null
}

// termsOf reads one page of a site's own WordPress terms. This is
// /local/wordpress and not /local/automation, so its wire names are read here
// rather than in the automation client; a term with no name is shown by its id
// rather than given one.
function termsOf(data: unknown): NamedOption[] {
  const envelope = (data ?? {}) as { terms?: unknown }
  const rows = Array.isArray(envelope.terms) ? envelope.terms : []
  return rows.flatMap((raw) => {
    const term = (raw ?? {}) as { id?: unknown; name?: unknown }
    const id = Number(term.id)
    if (!Number.isInteger(id) || id <= 0) return []
    return [
      {
        id,
        label:
          typeof term.name === "string" && term.name !== ""
            ? term.name
            : `#${id}`,
      },
    ]
  })
}

// eligibleChannels reads the channels one Telegram connection may publish to.
// The connection decides alone: a peer id read against one account says nothing
// about another, so this is asked per key and the answer is never shared.
//
// The backend already narrows the list to channels the account may post in
// (`capability=send`). The permission flag on each row is still read rather than
// assumed, because an account that may see a channel is not thereby allowed to
// post in it.
export async function eligibleChannels(
  token: string | null,
  clientKey: string,
  signal?: AbortSignal
): Promise<NamedOption[]> {
  const res = await apiRequest<{
    channels?: {
      id: number | string
      title: string
      username?: string
      permissions?: { new?: boolean }
    }[]
  }>("GET", `/telegram/${encodeURIComponent(clientKey)}/channels`, {
    token,
    query: { page: 1, per_page: 100, capability: "send" },
    signal,
  })
  return (res.data?.channels ?? []).flatMap((channel) => {
    // The channel object's `id` is Telegram's own channel id, answered as a
    // decimal string so no client truncates it. This area stores it as the
    // number the automation contract carries; an id that is not one is not
    // offered, rather than being rounded into an id that names another channel.
    const id = Number(channel.id)
    if (!Number.isInteger(id) || id === 0) return []
    if (channel.permissions?.new === false) return []
    return [
      {
        id,
        label: channel.title,
        detail: channel.username ? `@${channel.username}` : undefined,
      },
    ]
  })
}

// The destinations the form posted, as the payload builders expect them. The
// wire names never appear here: the form sends the presentation model and
// `workflowBody` is the only place the contract's field names are written.
// `options` is carried through untouched — it is the destination's own
// publishing settings, and a field this page does not show must survive a save.
function readDestinations(raw: FormDataEntryValue | null): DestinationDraft[] {
  let parsed: unknown
  try {
    parsed = JSON.parse(String(raw ?? "[]"))
  } catch {
    return []
  }
  if (!Array.isArray(parsed)) return []
  // The two addresses have the two rules the schema has: a WordPress site is a
  // positive local id, a Telegram destination is a peer id beside its connection
  // and may be any integer but zero. Anything else is read as "no address" and
  // refused before it is written.
  const toPeer = (value: unknown): number | null =>
    Number.isInteger(value) && (value as number) !== 0
      ? (value as number)
      : null
  const toSite = (value: unknown): number | null =>
    Number.isInteger(value) && (value as number) > 0 ? (value as number) : null
  return parsed.map((entry) => {
    const row = (entry ?? {}) as Record<string, unknown>
    return {
      type:
        row.type === "wordpress"
          ? ("wordpress" as const)
          : ("telegram" as const),
      clientKey: typeof row.clientKey === "string" ? row.clientKey : "",
      channelID: toPeer(row.channelID),
      siteID: toSite(row.siteID),
      options:
        row.options && typeof row.options === "object"
          ? { ...(row.options as Record<string, unknown>) }
          : {},
      enabled: row.enabled !== false,
    }
  })
}

export function ErrorBoundary({ error }: Route.ErrorBoundaryProps) {
  return (
    <>
      <PageHeader title="ورک‌فلو" />
      <PageError error={error} />
    </>
  )
}

export default function AutomationWorkflowForm({
  loaderData,
  actionData,
  params,
}: Route.ComponentProps) {
  const navigation = useNavigation()
  const submit = useSubmit()
  const navigate = useNavigate()
  const location = useLocation()
  const { toast } = useToast()
  const submitting = navigation.state === "submitting"

  const record = loaderData.status === "ok" ? loaderData.workflow : null
  const creating = loaderData.status === "ok" && loaderData.creating
  // The record's own identity: when it changes, the form is re-seeded from the
  // stored row and from nothing else. A form is never left holding a previous
  // record's values, and a re-seed never wipes values the operator just typed
  // for the same record.
  const recordKey = record ? `${record.id}:${record.updatedAt}` : "new"
  const seedOf = React.useCallback(
    (workflow: WorkflowItem | null): WorkflowDraft =>
      workflow ? workflowDraftOf(workflow) : emptyWorkflowDraft(),
    []
  )

  const [draft, setDraft] = React.useState<WorkflowDraft>(() => seedOf(record))
  const [seed, setSeed] = React.useState<WorkflowDraft>(() => seedOf(record))
  const [seedKey, setSeedKey] = React.useState(recordKey)
  React.useEffect(() => {
    if (seedKey === recordKey) return
    const next = seedOf(record)
    setSeedKey(recordKey)
    setSeed(next)
    setDraft(next)
  }, [recordKey, record, seedKey, seedOf])

  // One conversation per Telegram connection and per WordPress site: the
  // channels of a connection and the terms of a site belong to it alone.
  const connectionKeys = React.useMemo(() => {
    const keys = new Set<string>()
    for (const destination of draft.destinations) {
      if (
        destination.type === "telegram" &&
        destination.clientKey.trim() !== ""
      ) {
        keys.add(destination.clientKey.trim())
      }
    }
    return [...keys].sort()
  }, [draft.destinations])
  // The set of keys, as one value: the effect below runs when the set changes,
  // not on every edit that leaves it the same.
  const connections = connectionKeys.join(" ")
  const [channelsByKey, setChannelsByKey] = React.useState<
    Map<string, ChannelOptions>
  >(new Map())
  const requestedConnections = React.useRef<Set<string>>(new Set())

  React.useEffect(() => {
    const missing = connectionKeys.filter(
      (clientKey) => !requestedConnections.current.has(clientKey)
    )
    if (missing.length === 0) return
    for (const clientKey of missing) {
      requestedConnections.current.add(clientKey)
      setChannelsByKey((previous) => {
        const next = new Map(previous)
        if (!next.has(clientKey)) {
          next.set(clientKey, { items: [], loading: true, error: null })
        }
        return next
      })
      void (async () => {
        try {
          const items = await eligibleChannels(getToken(), clientKey)
          setChannelsByKey((previous) => {
            const next = new Map(previous)
            next.set(clientKey, { items, loading: false, error: null })
            return next
          })
          // A channel this connection does not offer is not a destination of
          // it. What stays selected is what is still valid; what does not is
          // dropped here rather than posted as a peer id the account cannot
          // publish to.
          setDraft((previous) => ({
            ...previous,
            destinations: trimChannels(
              previous.destinations,
              clientKey,
              items.map((channel) => channel.id)
            ),
          }))
        } catch (error) {
          const message = errorMessage(error, "")
          // A call this page never asked about is not an error to report.
          if (message === "") return
          setChannelsByKey((previous) => {
            const next = new Map(previous)
            next.set(clientKey, { items: [], loading: false, error: message })
            return next
          })
        }
      })()
    }
  }, [connections])

  // A WordPress destination publishes only term ids its own site already has,
  // so each selected site's categories and tags are read through the backend's
  // WordPress connection. The browser never holds a WordPress credential, and a
  // term id is never guessed here: what the picker offers is what the site
  // reported. A site whose terms cannot be read keeps whatever ids the record
  // already holds, named as unresolved rather than dropped.
  const selectedSites = React.useMemo(() => {
    const ids = new Set<number>()
    for (const destination of draft.destinations) {
      const id = destination.siteID
      if (destination.type === "wordpress" && id !== null && id > 0) ids.add(id)
    }
    return [...ids].sort((a, b) => a - b)
  }, [draft.destinations])
  const siteKeys = selectedSites.join(",")
  const [siteTerms, setSiteTerms] = React.useState<Map<number, SiteTerms>>(
    new Map()
  )
  const requestedSites = React.useRef<Set<number>>(new Set())

  React.useEffect(() => {
    const missing = selectedSites.filter(
      (id) => !requestedSites.current.has(id)
    )
    if (missing.length === 0) return
    for (const siteID of missing) {
      requestedSites.current.add(siteID)
      void (async () => {
        try {
          const [categories, tags] = await Promise.all([
            apiRequest<unknown>(
              "GET",
              `/local/wordpress/${siteID}/categories`,
              {
                token: getToken(),
                query: { page: 1, per_page: 100 },
              }
            ),
            apiRequest<unknown>("GET", `/local/wordpress/${siteID}/tags`, {
              token: getToken(),
              query: { page: 1, per_page: 100 },
            }),
          ])
          setSiteTerms((previous) => {
            const next = new Map(previous)
            next.set(siteID, {
              categories: termsOf(categories.data),
              tags: termsOf(tags.data),
              loading: false,
              error: null,
            })
            return next
          })
        } catch (error) {
          const message = errorMessage(error, "")
          // A call this page never asked about is not an error to report.
          if (message === "") return
          setSiteTerms((previous) => {
            const next = new Map(previous)
            next.set(siteID, {
              ...emptySiteTerms(),
              loading: false,
              error: message,
            })
            return next
          })
        }
      })()
    }
  }, [siteKeys])

  // Leaving the page with unsaved work asks once. The guard is armed only when
  // the draft actually differs from the row it was seeded from, so an untouched
  // edit and every read-only visit pass without a word.
  const dirty =
    JSON.stringify(workflowBody(draft)) !== JSON.stringify(workflowBody(seed))
  const dirtyRef = React.useRef(dirty)
  dirtyRef.current = dirty
  const leaving = React.useRef(false)
  const blocker = useBlocker(
    React.useCallback(() => dirtyRef.current && !leaving.current, [])
  )
  React.useEffect(() => {
    if (blocker.state !== "blocked") return
    if (
      window.confirm("تغییرات ذخیره‌نشده‌ای دارید. این صفحه را ترک می‌کنید؟")
    ) {
      blocker.proceed()
    } else {
      blocker.reset()
    }
  }, [blocker])

  // A save lands without a redirect, so the page reports it itself and goes
  // back to the view the operator came from. Nothing is claimed for a write that
  // did not happen: a refusal arrives as `actionData.error` and never reaches
  // this.
  const wasSubmitting = React.useRef(false)
  React.useEffect(() => {
    if (wasSubmitting.current && !submitting) {
      if (actionData && !actionData.error) {
        if (actionData.intent === "create") {
          toast({ variant: "success", title: "ورک‌فلو ساخته شد." })
        } else if (actionData.intent === "update") {
          toast({ variant: "success", title: "تغییرات ورک‌فلو ذخیره شد." })
        }
        leaving.current = true
        navigate(workflowFormReturnTo(location.state))
      } else {
        // A refusal keeps this page open on what was typed, so the guard goes
        // back on: a later navigation still warns about the unsaved draft.
        leaving.current = false
      }
    }
    wasSubmitting.current = submitting
  }, [submitting, actionData, toast, navigate, location])

  // A refusal to switch the workflow on puts the flag back where the store has
  // it. The switch must never keep looking active for a workflow the server
  // said it would not run.
  React.useEffect(() => {
    if (!actionData?.revertEnabled) return
    setDraft((previous) => ({
      ...previous,
      enabled: record?.enabled ?? false,
    }))
  }, [actionData, record])

  // A 409 names the workflow that already holds this channel, so the refusal
  // carries a way to get to it rather than only saying no.
  const conflict =
    actionData?.conflictWorkflowID != null
      ? {
          id: actionData.conflictWorkflowID,
          channelLabel: actionData.conflictChannelLabel ?? "",
        }
      : null

  const taken = React.useMemo(() => {
    const map = new Map<number, TakenWorkflow>()
    if (loaderData.status === "ok") {
      for (const [id, entry] of Object.entries(loaderData.taken)) {
        map.set(Number(id), entry)
      }
    }
    return map
  }, [loaderData])

  const goBack = () => {
    leaving.current = true
    navigate(workflowFormReturnTo(location.state))
  }

  if (loaderData.status === "missing") {
    return (
      <div className="flex flex-col gap-6">
        <PageHeader title="ورک‌فلو" />
        <EmptyState
          icon={WorkflowIcon}
          title="ورک‌فلو پیدا نشد"
          description="شناسه در نشانی معتبر نیست، یا این ورک‌فلو حذف شده است."
          action={
            <Button
              variant="outline"
              render={<Link to={automationRoute.root} />}
            >
              <ArrowRightIcon />
              بازگشت به اتوماسیون
            </Button>
          }
        />
      </div>
    )
  }

  if (loaderData.status !== "ok") {
    return (
      <div className="flex flex-col gap-6">
        <PageHeader title="ورک‌فلو" />
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

  // A different record is on its way. The form for the previous one is never
  // shown under this URL, and a form that has not been seeded yet is never
  // shown as if it were empty.
  const requestedID = params.workflowId ?? ""
  const loadedID = record ? String(record.id) : ""
  if (navigation.state === "loading" && requestedID !== loadedID) {
    return (
      <div className="flex flex-col gap-6">
        <PageHeader title={creating ? "افزودن ورک‌فلو" : "ویرایش ورک‌فلو"} />
        <LoadingState label="در حال بارگذاری ورک‌فلو…" />
      </div>
    )
  }

  return (
    <div className="flex flex-col gap-6">
      {!loaderData.canManage ? (
        <>
          <PageHeader title={creating ? "افزودن ورک‌فلو" : "ویرایش ورک‌فلو"} />
          <EmptyState
            icon={WorkflowIcon}
            title="دسترسی مجاز نیست"
            description="شما مجوز تغییر ورک‌فلوها را ندارید."
          />
        </>
      ) : (
        <WorkflowForm
          title={creating ? "افزودن ورک‌فلو" : "ویرایش ورک‌فلو"}
          submitLabel={creating ? "افزودن" : "ذخیره تغییرات"}
          draft={draft}
          onDraftChange={setDraft}
          sourceChannels={loaderData.sourceChannels}
          taken={taken}
          conflict={conflict}
          servers={loaderData.servers}
          sites={loaderData.sites}
          channelsByKey={channelsByKey}
          siteTerms={siteTerms}
          error={actionData?.error}
          fieldErrors={actionData?.fieldErrors}
          pending={submitting}
          sourceLocked={!creating}
          onCancel={goBack}
          onSubmit={(next) => {
            // A taken channel is refused before the request goes out: the form
            // already names the workflow that holds it.
            if (next.managedChannelID === null) return
            if (creating && taken.has(next.managedChannelID)) return
            // A save is the one navigation the guard must never stand in front
            // of: the draft is being handed to the store, which is the opposite
            // of losing it.
            leaving.current = true
            submit(
              {
                intent: creating ? "create" : "update",
                managed_channel_id: String(next.managedChannelID),
                enabled: String(next.enabled),
                ai_enabled: String(next.aiEnabled),
                ai_prompt: next.aiPrompt,
                ai_send_images: String(next.aiSendImages),
                ai_publish_on_failure: String(next.aiPublishOnFailure),
                destinations: JSON.stringify(
                  next.destinations.map((destination) => ({
                    type: destination.type,
                    clientKey: destination.clientKey,
                    channelID: destination.channelID,
                    siteID: destination.siteID,
                    options: destination.options,
                    enabled: destination.enabled,
                  }))
                ),
              },
              { method: "post" }
            )
          }}
        />
      )}
    </div>
  )
}
