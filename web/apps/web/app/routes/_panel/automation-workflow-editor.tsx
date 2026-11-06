import {
  Link,
  redirect,
  useNavigate,
  useNavigation,
  useSubmit,
} from "react-router"
import { ArrowRightIcon, WorkflowIcon } from "lucide-react"
import * as React from "react"

import {
  WorkflowForm,
  type NamedOption,
  type TakenWorkflow,
} from "@/components/automation/workflow-form"
import {
  emptySiteTerms,
  type SiteTerms,
} from "@/components/automation/workflow-wordpress-options"
import { EmptyState, ErrorState } from "@/components/states"
import { Button } from "@workspace/ui/components/button"
import { useToast } from "@workspace/ui/components/toast"

import {
  automationApi,
  automationRoute,
  emptyWorkflowDraft,
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
import {
  automationAccessDenied,
  isAuthLost,
  isForbidden,
  loginRedirect,
} from "@/lib/automation-access"
import { apiRequest, errorMessage } from "@/lib/api"
import { PageError, pageMeta, section } from "@/lib/page"
import { PageHeader } from "@/components/page-header"
import { getToken, requireSession } from "@/lib/session"
import { strip } from "@/path"
import type { TelegramServer, WordPressSite } from "@/lib/types"

import type { Route } from "./+types/automation-workflow-editor"

export const meta = () => pageMeta("ویرایش ورک‌فلو")

export const handle = section("اتوماسیون")

type LoaderData =
  | {
      status: "ok"
      workflow: WorkflowItem | null
      sourceChannels: NamedOption[]
      taken: Record<number, TakenWorkflow>
      servers: TelegramServer[]
      sites: NamedOption[]
      canManage: boolean
    }
  | { status: "missing" }
  | { status: "forbidden"; message: string }
  | { status: "error"; message: string }

// `new` is the one non-numeric value of the segment: it means "no workflow yet".
function validID(raw: string | undefined): number | null {
  return raw !== undefined && /^[0-9]+$/.test(raw) ? Number(raw) : null
}

function isCreation(raw: string | undefined): boolean {
  return raw === undefined || raw === "new"
}

export async function clientLoader({
  params,
  request,
}: Route.LoaderArgs): Promise<LoaderData> {
  const { token, me } = await requireSession(request)
  // A restricted administrator is told so from the session itself: this page
  // must never fall back to a silently empty form because the calls returned
  // nothing.
  if (me.access?.automation === false) {
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
      }>("GET", "/local/channels", { token, query: { page: 1, per_page: 100 } }),
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
    // The schema allows one workflow per managed channel, and this editor
    // refuses to offer a second one: a taken channel names its workflow instead.
    // The workflow being edited is not one of them — its own source channel is
    // not "taken", it is the channel this record already holds.
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
      workflow = found ?? readWorkflowRecord(
        (await apiRequest<unknown>("GET", automationApi.workflow(id), { token }))
          .data
      )
    }

    return {
      status: "ok",
      workflow,
      sourceChannels: (channels.data?.channels ?? []).map((channel) => ({
        id: channel.id,
        label: channel.title,
        detail: channel.client_key,
      })),
      taken,
      servers: servers.data?.servers ?? [],
      sites: (sites.data?.sites ?? []).map((site) => ({
        id: site.id,
        label: site.title,
        detail: site.domain,
      })),
      canManage: Boolean(me.access?.automation),
    }
  } catch (error) {
    if (isAuthLost(error)) throw loginRedirect(request)
    if (isForbidden(error)) {
      return {
        status: "forbidden",
        message: "شما مجوز ویرایش ورک‌فلوها را ندارید.",
      }
    }
    return { status: "error", message: errorMessage(error) }
  }
}

interface ActionData {
  error?: string
  intent?: string
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

export async function clientAction({
  request,
  params,
}: Route.ActionArgs): Promise<ActionData | Response> {
  const { token } = await requireSession(request)
  const form = await request.formData()
  const creating = isCreation(params.workflowId)
  const id = validID(params.workflowId)
  if (!creating && id === null) {
    return { error: "شناسه ورک‌فلو نامعتبر است.", intent: "update" }
  }
  const intent = creating ? "create" : "update"
  const rawChannel = String(form.get("managed_channel_id") ?? "")
  const managedChannelID = Number(rawChannel)

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
    destinations: readDestinations(form.get("destinations")),
  }

  if (draft.managedChannelID === null) {
    return {
      error: "کانال مبدأ را انتخاب کنید.",
      intent,
      values: { managed_channel_id: rawChannel },
    }
  }

  try {
    const body = workflowBody(draft)
    // Both writes land on the record's own URL, whose loader reads the stored
    // row back. That is the round-trip made visible: what the editor shows
    // after a save — and after a reload of that same URL — is what the server
    // kept, not what the form last held.
    let savedID = id
    if (intent === "create") {
      const created = await apiRequest<{ workflow?: { id?: number } }>(
        "POST",
        automationApi.workflows,
        { token, body }
      )
      savedID = Number(created.data?.workflow?.id ?? 0)
    } else {
      await apiRequest("PATCH", automationApi.workflow(id as number), {
        token,
        body,
      })
    }
    if (!savedID || !Number.isInteger(savedID) || savedID <= 0) {
      // The write succeeded but named no record; the list is the only honest
      // place to land rather than an editor for an id nobody has.
      return redirect(strip(automationRoute.workflows))
    }
    return redirect(strip(automationRoute.workflow(savedID)))
  } catch (error) {
    if (isAuthLost(error)) throw loginRedirect(request)
    if (isForbidden(error)) {
      return {
        error: "شما مجوز این عمل را ندارید.",
        intent,
        values: { managed_channel_id: rawChannel },
      }
    }
    // A duplicate is the one refusal the user can act on here, so it names the
    // workflow that holds this channel instead of only saying no.
    if (isDuplicateWorkflow(error)) {
      const existing = await findWorkflowForChannel(token, draft.managedChannelID)
      return {
        error: explainAutomationError(error),
        intent,
        values: { managed_channel_id: rawChannel },
        conflictWorkflowID: existing?.id ?? null,
        conflictChannelLabel: existing?.channelLabel,
      }
    }
    // The server refused to switch this workflow on. Nothing here may leave the
    // switch looking active: the refusal is shown in the server's own words and
    // the caller puts the flag back where the store has it.
    if (isEnableRefusal(error)) {
      return {
        error: explainAutomationError(error),
        intent,
        values: { managed_channel_id: rawChannel },
        revertEnabled: true,
      }
    }
    return {
      error: explainAutomationError(error),
      intent,
      values: { managed_channel_id: rawChannel },
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
    const rows = workflowRows(page.data)
    for (const raw of rows) {
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
        label: typeof term.name === "string" && term.name !== "" ? term.name : `#${id}`,
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
  const toInt = (value: unknown): number | null =>
    Number.isInteger(value) && (value as number) > 0 ? (value as number) : null
  return parsed.map((entry) => {
    const row = (entry ?? {}) as Record<string, unknown>
    return {
      type: row.type === "wordpress" ? ("wordpress" as const) : ("telegram" as const),
      clientKey: typeof row.clientKey === "string" ? row.clientKey : "",
      channelID: toInt(row.channelID),
      siteID: toInt(row.siteID),
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
      <PageHeader title="ویرایش ورک‌فلو" />
      <PageError error={error} />
    </>
  )
}

export default function AutomationWorkflowEditor({
  loaderData,
  actionData,
}: Route.ComponentProps) {
  const navigation = useNavigation()
  const submit = useSubmit()
  const navigate = useNavigate()
  const { toast } = useToast()
  const submitting = navigation.state === "submitting"

  const [draft, setDraft] = React.useState<WorkflowDraft>(() =>
    loaderData.status === "ok" && loaderData.workflow
      ? workflowDraftOf(loaderData.workflow)
      : emptyWorkflowDraft()
  )

  // A save lands on the record's own URL and the loader reads the stored row
  // back, so the form is re-seeded from the server's answer rather than left
  // holding what was typed. Nothing here reports a result for a write that did
  // not happen: the intent is read from the submit that just left, and a
  // refusal arrives as `actionData.error` and never reaches this.
  const record = loaderData.status === "ok" ? loaderData.workflow : null
  const recordKey = record ? `${record.id}:${record.updatedAt}` : "new"
  const [seedKey, setSeedKey] = React.useState(recordKey)
  React.useEffect(() => {
    if (seedKey === recordKey) return
    setSeedKey(recordKey)
    setDraft(record ? workflowDraftOf(record) : emptyWorkflowDraft())
  }, [recordKey, record, seedKey])

  const pendingIntent = React.useRef<string | null>(null)
  React.useEffect(() => {
    const formData = navigation.formData
    if (formData) pendingIntent.current = String(formData.get("intent") ?? "")
  }, [navigation.formData])

  const wasSubmitting = React.useRef(false)
  React.useEffect(() => {
    if (wasSubmitting.current && !submitting && !actionData?.error) {
      const intent = pendingIntent.current
      if (intent === "create") toast({ variant: "success", title: "ورک‌فلو ساخته شد." })
      else if (intent === "update")
        toast({ variant: "success", title: "تغییرات ورک‌فلو ذخیره شد." })
      pendingIntent.current = null
    }
    wasSubmitting.current = submitting
  }, [submitting, actionData, toast])

  // A refusal to switch the workflow on puts the flag back where the store has
  // it. The switch must never keep looking active for a workflow the server
  // said it would not run.
  React.useEffect(() => {
    if (!actionData?.revertEnabled) return
    setDraft((previous) => ({
      ...previous,
      enabled:
        loaderData.status === "ok" ? (loaderData.workflow?.enabled ?? false) : false,
    }))
  }, [actionData, loaderData])

  // A 409 names the workflow that already holds this channel, so the refusal
  // carries a way to get to it rather than only saying no.
  const conflict =
    actionData?.conflictWorkflowID != null
      ? {
          id: actionData.conflictWorkflowID,
          channelLabel: actionData.conflictChannelLabel ?? "",
        }
      : null

  // The destination channels belong to one connection. Changing the connection
  // drops the old rows and refetches, so a channel id is never shown against a
  // connection it does not live on.
  const clientKey =
    draft.destinations.find((destination) => destination.type === "telegram")
      ?.clientKey ?? ""
  const [destinationChannels, setDestinationChannels] = React.useState<
    NamedOption[]
  >([])
  const [loadingChannels, setLoadingChannels] = React.useState(false)
  const [channelsError, setChannelsError] = React.useState<string | null>(null)

  React.useEffect(() => {
    setDestinationChannels([])
    setChannelsError(null)
    if (!clientKey) return
    let cancelled = false
    const controller = new AbortController()
    setLoadingChannels(true)
    apiRequest<{
      channels?: {
        id: number
        title: string
        username?: string
        permissions?: { new?: boolean }
      }[]
    }>("GET", `/telegram/${encodeURIComponent(clientKey)}/channels`, {
      token: getToken(),
      query: { page: 1, per_page: 100, capability: "send" },
      signal: controller.signal,
    })
      .then((res) => {
        if (cancelled) return
        setDestinationChannels(
          (res.data?.channels ?? [])
            // The backend already narrows this list to channels the account may
            // post in. The flag is still read, never assumed.
            .filter((channel) => channel.permissions?.new !== false)
            .map((channel) => ({
              id: channel.id,
              label: channel.title,
              detail: channel.username ? `@${channel.username}` : undefined,
            }))
        )
      })
      .catch((error) => {
        if (cancelled || errorMessage(error) === "") return
        setChannelsError(errorMessage(error))
      })
      .finally(() => {
        if (!cancelled) setLoadingChannels(false)
      })
    return () => {
      cancelled = true
      controller.abort()
    }
  }, [clientKey])

  // A WordPress destination publishes only term ids its own site already has,
  // so each selected site's categories and tags are read through the backend's
  // WordPress connection. The browser never holds a WordPress credential, and a
  // term id is never guessed here: what the picker offers is what the site
  // reported. A site whose terms cannot be read keeps whatever ids the record
  // already holds, named as unresolved rather than dropped.
  const selectedSites = React.useMemo(
    () =>
      draft.destinations
        .filter((destination) => destination.type === "wordpress")
        .map((destination) => destination.siteID)
        .filter((id): id is number => id !== null && id > 0),
    [draft.destinations]
  )
  const [siteTerms, setSiteTerms] = React.useState<Map<number, SiteTerms>>(
    new Map()
  )
  const requestedSites = React.useRef<Set<number>>(new Set())

  React.useEffect(() => {
    const missing = selectedSites.filter((id) => !requestedSites.current.has(id))
    if (missing.length === 0) return
    const controller = new AbortController()
    let cancelled = false
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
                signal: controller.signal,
              }
            ),
            apiRequest<unknown>("GET", `/local/wordpress/${siteID}/tags`, {
              token: getToken(),
              query: { page: 1, per_page: 100 },
              signal: controller.signal,
            }),
          ])
          if (cancelled) return
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
          if (cancelled) return
          const message = errorMessage(error)
          // An aborted request is not an error the operator asked about.
          if (message === "") return
          setSiteTerms((previous) => {
            const next = new Map(previous)
            next.set(siteID, { ...emptySiteTerms(), loading: false, error: message })
            return next
          })
        }
      })()
    }
    return () => {
      cancelled = true
      controller.abort()
    }
  }, [selectedSites])

  const taken = React.useMemo(() => {
    const map = new Map<number, TakenWorkflow>()
    if (loaderData.status === "ok") {
      for (const [id, entry] of Object.entries(loaderData.taken)) {
        map.set(Number(id), entry)
      }
    }
    return map
  }, [loaderData])

  if (loaderData.status === "missing") {
    return (
      <div className="flex flex-col gap-6">
        <PageHeader title="ویرایش ورک‌فلو" />
        <EmptyState
          icon={WorkflowIcon}
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
        <PageHeader title="ویرایش ورک‌فلو" />
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

  const isEdit = loaderData.workflow !== null

  return (
    <div className="flex flex-col gap-6">
      <PageHeader
        title={isEdit ? "ویرایش ورک‌فلو" : "ورک‌فلوی جدید"}
        description="پیام تازهٔ کانال مبدأ، در صورت نیاز با هوش مصنوعی ویرایش و سپس در مقاصد منتشر می‌شود."
        actions={
          <Button
            variant="outline"
            render={<Link to={automationRoute.workflows} />}
          >
            <ArrowRightIcon data-icon="inline-start" />
            ورک‌فلوها
          </Button>
        }
      />

      {!loaderData.canManage ? (
        <EmptyState
          icon={WorkflowIcon}
          title="دسترسی مجاز نیست"
          description="شما مجوز تغییر ورک‌فلوها را ندارید."
        />
      ) : (
        <WorkflowForm
          draft={draft}
          onDraftChange={setDraft}
          sourceChannels={loaderData.sourceChannels}
          taken={taken}
          conflict={conflict}
          servers={loaderData.servers}
          sites={loaderData.sites}
          destinationChannels={destinationChannels}
          loadingChannels={loadingChannels}
          channelsError={channelsError}
          siteTerms={siteTerms}
          error={actionData?.error}
          pending={submitting}
          submitLabel={isEdit ? "ذخیره تغییرات" : "ایجاد ورک‌فلو"}
          sourceLocked={isEdit}
          onCancel={() => navigate({ pathname: automationRoute.workflows })}
          onSubmit={(next) => {
            // A taken channel is refused before the request goes out: the form
            // already names the workflow that holds it.
            if (next.managedChannelID === null) return
            if (!isEdit && taken.has(next.managedChannelID)) return
            submit(
              {
                intent: isEdit ? "update" : "create",
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
