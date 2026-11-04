import {
  Link,
  redirect,
  useNavigation,
  useOutlet,
  useParams,
  useSearchParams,
  useSubmit,
  type ShouldRevalidateFunctionArgs,
} from "react-router"
import {
  CloudOffIcon,
  MessageSquareTextIcon,
  PlusIcon,
  SendIcon,
} from "lucide-react"
import * as React from "react"

import { CategoryMultiSelect } from "@/components/category-multi-select"
import { ChannelListPane } from "@/components/channel-explorer/channel-list"
import { ChannelDeleteDialog } from "@/components/channel-explorer/channel-delete-dialog"
import { fullBleedSection, pageMeta } from "@/lib/page"
import { filterChannels, toggleCategory } from "@/lib/channel-filter"
import { deleteChannelForm, fileCategoriesForm } from "@/lib/channel-actions"
import { EmptyState, ErrorState } from "@/components/states"
import { SubmitButton } from "@/components/submit-button"
import { TelegramServerCombobox } from "@/components/telegram-server-combobox"
import { Button } from "@workspace/ui/components/button"
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@workspace/ui/components/dialog"
import {
  Field,
  FieldDescription,
  FieldError,
  FieldLabel,
  FieldControl,
} from "@workspace/ui/components/field"
import { useToast } from "@workspace/ui/components/toast"
import { cn } from "@workspace/ui/lib/utils"

import { ApiError, apiRequest } from "@/lib/api"
import { LOGIN_REDIRECT_TO, getToken, listAll, requireSession } from "@/lib/session"
import { strip, requestURL } from "@/path"
import type {
  Category,
  Channel,
  ChannelAvailability,
  FoundChannel,
  TelegramServer,
} from "@/lib/types"

import type { Route } from "./+types/channels"

// The whole managed-channel list is read into the workspace so the search and
// the category chips answer as the reader types. A deployment with a very large
// inventory sees the first `LIST_PAGES * 100` channels.
const LIST_PAGES = 5

interface ChannelList {
  channels: Channel[]
}

// Channels are a best-effort page. In a custom-backend deployment the local
// channel store is often absent, so a failure is a scoped state, never a crash.
function toAvailability(error: unknown): ChannelAvailability {
  if (error instanceof ApiError) {
    if (error.status === 503 || error.status === 401 || error.status === 400) {
      return { state: "not_configured", message: error.message }
    }
    return { state: "unavailable", message: error.message, code: error.code }
  }
  return {
    state: "unavailable",
    message: "بارگذاری کانال‌ها ناموفق بود.",
    code: "network_error",
  }
}

// parseCategoryFilter reads the URL's category_ids: comma-separated the way the
// backend documents it (`category_ids=1,2,3`), and repeated params as a
// convenience. Anything that is not a positive integer is dropped rather than
// invented.
function parseCategoryFilter(value: string | null): number[] {
  if (!value) return []
  return value
    .split(",")
    .map((part) => Number(part.trim()))
    .filter((id) => Number.isInteger(id) && id > 0)
}

// parseIDList reads a submit payload's category_ids. The dialog sends it as
// one JSON array string; a URLSearchParams-flattened legacy value ("5,12") and
// repeated form entries are read too. Anything that is not a positive integer
// is dropped rather than invented.
function parseIDList(raw: FormDataEntryValue | FormDataEntryValue[]): number[] {
  const parts = (Array.isArray(raw) ? raw : [raw]).flatMap((entry) => {
    if (typeof entry !== "string" || entry === "") return []
    if (entry.startsWith("[")) {
      // A JSON array of ids (numbers or numeric strings); anything else in
      // the array is dropped.
      try {
        const parsed: unknown = JSON.parse(entry)
        if (Array.isArray(parsed)) return parsed.map((id) => String(id))
      } catch {
        return []
      }
    }
    return entry.split(",")
  })
  return [
    ...new Set(
      parts
        .map((part) => Number(String(part).trim()))
        .filter((id) => Number.isInteger(id) && id > 0)
    ),
  ]
}

// The workspace holds the channel list and the categories; which channel is
// open is the child route's job, so that a channel carries no Telegram ids.
export async function clientLoader({ request }: Route.LoaderArgs) {
  const { token } = await requireSession(request)
  const url = requestURL(request)
  const wanted = (url.searchParams.get("server") ?? "").trim()

  let availability: ChannelAvailability
  let servers: TelegramServer[] = []
  let server: string | undefined
  let serversError: string | undefined
  try {
    // The listing is behind "servers.view". Without it this page still shows
    // the local list; a server is named by typing its key, and the API refuses
    // a key it does not serve.
    const { data } = await apiRequest<{ servers: TelegramServer[] }>(
      "GET",
      "/telegram",
      { token }
    )
    servers = data.servers ?? []
    server =
      servers.find((s) => s.key === wanted)?.key ?? servers[0]?.key ?? undefined
  } catch (error) {
    if (error instanceof ApiError && error.isAuthLost) {
      throw redirect(
        `${LOGIN_REDIRECT_TO}?expired=1&next=${encodeURIComponent(strip(`${url.pathname}${url.search}`))}`
      )
    }
    serversError =
      error instanceof ApiError ? error.message : "بارگذاری سرورها ناموفق بود."
  }
  const canPickServer = servers.length > 0

  try {
    const listed = await listAll<Channel>(
      token,
      "/local/channels",
      (data) => (data as ChannelList).channels ?? [],
      {},
      LIST_PAGES
    )
    availability = {
      state: "ready",
      channels: listed.rows,
      meta: {
        total: listed.meta.total,
        has_more: listed.meta.has_more,
        page: listed.meta.page,
        total_pages: listed.meta.total_pages,
      },
    }
  } catch (error) {
    if (error instanceof ApiError && error.status === 401) {
      throw redirect(
        `${LOGIN_REDIRECT_TO}?expired=1&next=${encodeURIComponent(strip(`${url.pathname}${url.search}`))}`
      )
    }
    availability = toAvailability(error)
  }

  // Every category by name: the editor picks from this list and the chips are
  // drawn from it.
  const categoryList = await listAll<Category>(
    token,
    "/local/categories",
    (data) => (data as { categories: Category[] }).categories ?? []
  )
    .then((result) =>
      [...result.rows].sort((a, b) => a.name.localeCompare(b.name, "fa"))
    )
    .catch(() => [] as Category[])

  // Which categories hold which channel, asked from the category side: one
  // listing per category, in parallel, and a category that cannot be read is
  // simply not shown against any channel. The chips filter on these ids.
  const filed: Record<number, number[]> = {}
  if (availability.state === "ready") {
    const under = await Promise.all(
      categoryList.map(async (category) => {
        const result = await apiRequest<{ channels: Channel[] }>(
          "GET",
          `/local/categories/${category.id}/channels`,
          { token }
        ).catch(() => null)
        return [category, result?.data?.channels ?? []] as const
      })
    )
    for (const [category, channels] of under) {
      for (const channel of channels) {
        ;(filed[channel.id] ??= []).push(category.id)
      }
    }
  }

  return {
    token,
    server,
    servers,
    serversError,
    canPickServer,
    availability,
    categories: categoryList,
    filed,
  }
}

// The workspace's list is not a function of which channel is open: moving
// between channels must not throw the list away and rebuild it, and the search
// and the chips are applied to what is already in hand. Only a submission —
// a channel added, refiled or deleted — asks the server for the list again.
export function shouldRevalidate({
  formMethod,
}: ShouldRevalidateFunctionArgs): boolean {
  return Boolean(formMethod)
}

// describeJoin turns a join response into the one sentence it deserves.
function describeJoin(result: string): string {
  switch (result) {
    case "already_managed":
      return "این کانال از قبل در فهرست محلی است."
    case "already_member_added_to_management":
      return "حساب از قبل عضو بود و کانال به فهرست محلی اضافه شد."
    case "join_pending":
      return "درخواست عضویت ثبت شد و در انتظار پذیرش است."
    default:
      return "به کانال پیوستید و در فهرست محلی ثبت شد."
  }
}

// explainJoinError maps what the API actually says onto advice. The backend
// never bypasses Telegram's access rules; the panel must not imply otherwise.
function explainJoinError(error: ApiError): string {
  switch (error.code) {
    case "channel_not_found":
      return `${error.message} — کانال پیدا نشد یا برای این حساب در دسترس نیست.`
    case "needs_auth":
      return "حساب این سرور هنوز وارد نشده است؛ ابتدا سرور را وارد کنید."
    case "not_connected":
    case "worker_unavailable":
    case "worker_timeout":
      return `سرور تلگرام در دسترس نیست. ${error.message}`
    case "rate_limited":
      return "تلگرام تعداد درخواست‌ها را محدود کرده است؛ کمی بعد دوباره تلاش کنید."
    case "worker_error":
      return `${error.message} برای کانال‌های خصوصی، دعوت‌نامه لازم است و این پنل آن را دور نمی‌زند.`
    case "forbidden":
      return "شما مجوز افزودن کانال ندارید."
    default:
      return error.message
  }
}

interface ChannelActionData {
  ok?: true
  error?: string
  notice?: string
  intent?: string
}

export async function clientAction({
  request,
}: Route.ActionArgs): Promise<ChannelActionData | Response> {
  const { token } = await requireSession(request)
  const form = await request.formData()
  const intent = String(form.get("intent") ?? "")
  const url = requestURL(request)

  if (intent === "define") {
    // One field, one query: the discovery route takes the same forms the others
    // do (an id, @username or a t.me link), so nothing here has to classify it.
    const server = String(form.get("server") ?? "")
    const q = String(form.get("ref") ?? "").trim()
    const categoryIDs = parseIDList(form.getAll("category_ids"))
    if (!server || !q) {
      return { error: "شناسه، نام کاربری یا لینک کانال را وارد کنید.", intent }
    }
    let hits: FoundChannel[]
    try {
      const found = await apiRequest<{ channels: FoundChannel[] }>(
        "GET",
        `/telegram/${encodeURIComponent(server)}/channels/find`,
        { token, query: { q } }
      )
      hits = found.data.channels ?? []
    } catch (error) {
      if (error instanceof ApiError) {
        if (error.isAuthLost) {
          throw redirect(`${LOGIN_REDIRECT_TO}?expired=1&next=/channels`)
        }
        return { error: explainSearchError(error), intent }
      }
      return { error: "جستجوی کانال انجام نشد.", intent }
    }
    // Only a channel can be joined; a name that found nothing, or found only
    // users and groups, is the same answer.
    const hit = hits.find((ch) => ch.type === "channel")
    if (!hit) {
      return {
        error: "کانالی با این شناسه یا نام پیدا نشد.",
        intent,
      }
    }
    try {
      await apiRequest<{ result: string }>(
        "POST",
        `/telegram/${encodeURIComponent(server)}/channels/${encodeURIComponent(hit.id)}/join`,
        { token }
      )
    } catch (error) {
      if (error instanceof ApiError) {
        if (error.isAuthLost) {
          throw redirect(`${LOGIN_REDIRECT_TO}?expired=1&next=/channels`)
        }
        return { error: explainJoinError(error), intent }
      }
      return { error: "ثبت کانال انجام نشد.", intent }
    }
    if (categoryIDs.length > 0) {
      // Filing is best effort: the channel exists either way, and it shows
      // under no category rather than losing the definition.
      const listed = await apiRequest<{ channels: Channel[] }>(
        "GET",
        "/local/channels",
        { token, query: { per_page: 100, q: hit.title || q } }
      ).catch(() => null)
      const created =
        listed?.data?.channels.find(
          (ch) => String(ch.channel_id) === hit.id
        ) ?? listed?.data?.channels[0]
      if (created) {
        await fileChannel(token, created.id, categoryIDs)
      }
    }
    return redirect(strip(url.pathname))
  }

  if (intent === "join") {
    const server = String(form.get("server") ?? "")
    // The Join button posts the ref the channel resolved to, not the raw text.
    const ref = String(form.get("ref") ?? "")
    if (!server || !ref) {
      return { error: "شناسه کانال یا سرور نامعتبر است.", intent }
    }
    try {
      const { data } = await apiRequest<{ result: string }>(
        "POST",
        `/telegram/${encodeURIComponent(server)}/channels/${encodeURIComponent(ref)}/join`,
        { token }
      )
      return { ok: true, notice: describeJoin(data.result), intent }
    } catch (error) {
      if (error instanceof ApiError) {
        if (error.isAuthLost) {
          throw redirect(`${LOGIN_REDIRECT_TO}?expired=1&next=/channels`)
        }
        return { error: explainJoinError(error), intent }
      }
      return { error: "پیوستن به کانال انجام نشد.", intent }
    }
  }

  if (intent === "categories") {
    // The edit dialog's save: the whole set of categories for one channel.
    const selectedCategories = parseIDList(form.getAll("category_ids"))
    const id = Number(form.get("id"))
    if (!Number.isInteger(id) || id <= 0) {
      return { error: "شناسه کانال نامعتبر است.", intent }
    }
    try {
      await fileChannel(token, id, selectedCategories)
    } catch (error) {
      if (error instanceof ApiError) {
        if (error.isAuthLost) {
          throw redirect(`${LOGIN_REDIRECT_TO}?expired=1&next=/channels`)
        }
        return { error: error.message, intent }
      }
      return { error: "ذخیرهٔ دسته‌بندی‌ها انجام نشد.", intent }
    }
    return redirect(strip(url.pathname))
  }

  if (intent !== "leave") {
    return { error: "درخواست نامعتبر است.", intent }
  }
  const id = Number(form.get("id"))
  if (!Number.isInteger(id) || id <= 0) {
    return { error: "شناسه کانال نامعتبر است.", intent }
  }
  const telegram = String(form.get("telegram")) === "true"

  try {
    await apiRequest<{ local_deleted: boolean; telegram_left: boolean }>(
      "POST",
      `/local/channels/${id}/leave`,
      { token, body: { telegram } }
    )
    // If the channel being deleted is the one open in the messages pane, that
    // route no longer has a channel to show: go back to the workspace rather
    // than leaving a link to a record that is gone.
    const leaving = parseLocalID(url.pathname.split("/")[2])
    const next = new URLSearchParams(url.searchParams)
    if (leaving === id) return redirect(strip(`/channels${searchSuffix(next)}`))
    return redirect(strip(`${url.pathname}${searchSuffix(next)}`))
  } catch (error) {
    if (error instanceof ApiError) {
      if (error.status === 401) {
        throw redirect(`${LOGIN_REDIRECT_TO}?expired=1&next=/channels`)
      }
      if (error.isForbidden) {
        return { error: "شما مجوز حذف این کانال را ندارید.", intent }
      }
      if (error.code === "partial_failure") {
        // Telegram left but the record stayed: the server's message carries the
        // retry advice. The two effects are separate and are reported as such.
        return {
          error: `${error.message} — رکورد محلی هنوز در فهرست است.`,
          intent,
        }
      }
      return { error: error.message, intent }
    }
    return { error: "عملیات انجام نشد.", intent }
  }
}

function searchSuffix(params: URLSearchParams): string {
  const query = params.toString()
  return query ? `?${query}` : ""
}

// parseLocalID reads a managed-channel id out of a path segment. It is the
// local row id and never a Telegram channel id.
function parseLocalID(raw: string | undefined): number | null {
  if (!raw || !/^[0-9]+$/.test(raw)) return null
  const value = Number(raw)
  return Number.isSafeInteger(value) && value > 0 ? value : null
}

export const meta = () => pageMeta("کانال‌ها")

export const handle = fullBleedSection("کانال‌ها")

export default function Channels({ loaderData, actionData }: Route.ComponentProps) {
  const { categories, filed, availability, servers, server, serversError } =
    loaderData
  const params = useParams()
  const selectedId = parseLocalID(params.id)
  // The open channel's timeline is the child route at `channels/:id/messages`,
  // so a shared link and the back button both keep working.
  const outlet = useOutlet()
  const navigation = useNavigation()
  const submit = useSubmit()
  const { toast } = useToast()
  const [searchParams, setSearchParams] = useSearchParams()

  // The filters live in the URL so a shared link and the browser's back and
  // forward buttons restore them, and are applied to the list already in hand
  // so typing is instant.
  const q = searchParams.get("q") ?? ""
  const categoryFilterIds = [
    ...new Set(searchParams.getAll("category_ids").flatMap(parseCategoryFilter)),
  ]
  const [search, setSearch] = React.useState(q)
  React.useEffect(() => setSearch(q), [q])

  const applySearch = (value: string) => {
    setSearch(value)
    const next = new URLSearchParams(searchParams)
    if (value.trim()) next.set("q", value.trim())
    else next.delete("q")
    // A filter change is not a page in the reader's history.
    setSearchParams(next, { replace: true })
  }

  const applyCategories = (ids: number[]) => {
    const next = new URLSearchParams(searchParams)
    if (ids.length > 0) next.set("category_ids", ids.join(","))
    else next.delete("category_ids")
    setSearchParams(next, { replace: true })
  }

  const clearFilters = () => {
    setSearch("")
    const next = new URLSearchParams(searchParams)
    next.delete("q")
    next.delete("category_ids")
    setSearchParams(next, { replace: true })
  }

  const filtersActive = q.trim() !== "" || categoryFilterIds.length > 0

  const rows =
    availability.state === "ready"
      ? filterChannels(availability.channels, {
          search,
          categoryIds: categoryFilterIds,
          filed,
        })
      : []

  // --- mutations ----------------------------------------------------------
  const submitting = navigation.state === "submitting"
  const [confirming, setConfirming] = React.useState<Channel | null>(null)
  const [defining, setDefining] = React.useState(false)
  const [editing, setEditing] = React.useState<Channel | null>(null)

  // A successful action answers with a redirect, so actionData is gone by the
  // time it lands; the intent recorded from the submit that just left is what
  // names the result for the toast.
  const pendingIntent = React.useRef<string | null>(null)
  React.useEffect(() => {
    const formData = navigation.formData
    if (formData) pendingIntent.current = String(formData.get("intent") ?? "")
  }, [navigation.formData])

  const wasSaving = React.useRef(false)
  React.useEffect(() => {
    if (wasSaving.current && !submitting && !actionData?.error) setEditing(null)
    wasSaving.current = submitting
  }, [submitting, actionData])

  const wasSubmitting = React.useRef(false)
  React.useEffect(() => {
    if (wasSubmitting.current && !submitting && !actionData?.error) {
      setConfirming(null)
    }
    wasSubmitting.current = submitting
  }, [submitting, actionData])

  React.useEffect(() => {
    if (actionData?.intent === "define" && actionData.error) setDefining(true)
    // Reopening only needs the message to appear; `editing` is kept above.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [actionData])

  const confirmError =
    confirming && actionData?.intent === "leave" ? actionData.error : null

  // Mutation feedback: success fires once when a submission ends clean (a
  // redirect dropped actionData, so the recorded intent names the result);
  // failure fires once per message so React's double invocation in development
  // cannot repeat it.
  const toastedError = React.useRef<string | null>(null)
  React.useEffect(() => {
    if (actionData?.error) {
      if (toastedError.current === actionData.error) return
      toastedError.current = actionData.error
      toast({
        variant: "error",
        title: "انجام نشد.",
        description: actionData.error,
      })
      return
    }
    toastedError.current = null
    if (wasSubmitting.current || submitting || actionData?.intent) return
    const intent = pendingIntent.current
    if (!intent) return
    pendingIntent.current = null
    const titles: Record<string, string> = {
      define: "کانال ثبت شد.",
      categories: "دسته‌بندی‌ها ذخیره شد.",
      leave: "کانال از فهرست حذف شد.",
    }
    const title = titles[intent]
    if (title) toast({ variant: "success", title })
  }, [actionData, submitting, toast])

  // On a narrow screen the workspace shows one pane at a time: the list until a
  // channel is chosen, then its messages with a way back.
  const showMessages = selectedId !== null

  return (
    // The workspace is the whole content area, directly under the breadcrumb
    // bar: no page heading, no page column, no card around it. Each pane keeps
    // its own scroll, so reading a channel never moves the list.
    <div className="flex min-h-0 w-full flex-1 flex-col overflow-hidden">
      {/* Under RTL the first child sits on the physical right, which is where
          the product wants the channel list; the messages fill the left. */}
      <div className="flex min-h-0 flex-1 overflow-hidden">
        <div
          className={cn(
            "min-h-0",
            showMessages ? "hidden lg:flex" : "flex w-full lg:w-auto"
          )}
        >
          {availability.state === "not_configured" ? (
            <div className="flex w-full items-center justify-center p-6">
              <EmptyState
                icon={CloudOffIcon}
                title="ذخیره‌سازی کانال‌های محلی پیکربندی نشده است"
                description={`در این استقرار، کانال‌ها از پنل قابل مشاهده نیستند. پیام سرور: ${availability.message}`}
              />
            </div>
          ) : availability.state === "unavailable" ? (
            <div className="flex w-full items-center justify-center p-6">
              <ErrorState
                title="وضعیت کانال‌ها در دسترس نیست"
                message={`${availability.message} (کد: ${availability.code})`}
                onRetry={() => window.location.reload()}
              />
            </div>
          ) : (
            <ChannelListPane
              channels={rows}
              categories={categories}
              filed={filed}
              selectedId={selectedId}
              search={search}
              onSearchChange={applySearch}
              selectedCategories={categoryFilterIds}
              onToggleCategory={(id) =>
                applyCategories(toggleCategory(categoryFilterIds, id))
              }
              onAdd={() => setDefining(true)}
              onEditCategories={setEditing}
              onDelete={setConfirming}
              hasChannels={availability.channels.length > 0}
              filtersActive={filtersActive}
              onClearFilters={clearFilters}
            />
          )}
        </div>

        <div
          className={cn(
            "min-h-0 min-w-0 flex-1",
            showMessages ? "flex" : "hidden lg:flex"
          )}
        >
          {outlet ?? (
            <WorkspaceEmptyState
              hasChannels={
                availability.state === "ready" && availability.channels.length > 0
              }
              onAdd={() => setDefining(true)}
            />
          )}
        </div>
      </div>

      <ChannelDeleteDialog
        open={confirming !== null}
        onOpenChange={(open) => !open && setConfirming(null)}
        channel={confirming ?? PLACEHOLDER_CHANNEL}
        pending={submitting}
        error={confirmError}
        onConfirm={({ leaveTelegram }) => {
          if (!confirming) return
          submit(deleteChannelForm(confirming.id, { leaveTelegram }), {
            method: "post",
          })
        }}
      />

      {editing && (
        <EditChannelDialog
          channel={editing}
          categories={categories}
          initial={(filed[editing.id] ?? []).slice()}
          error={
            actionData?.intent === "categories" ? actionData.error : undefined
          }
          pending={submitting}
          onClose={() => setEditing(null)}
        />
      )}

      {defining && (
        <DefineChannelDialog
          servers={servers}
          server={server}
          canPickServer={loaderData.canPickServer}
          categories={categories}
          error={actionData?.intent === "define" ? actionData.error : undefined}
          pending={submitting}
          onClose={() => setDefining(false)}
        />
      )}
    </div>
  )
}

// The dialog is always mounted so its opening is animated; it is only ever
// about a real channel, and this is what it shows before one is chosen.
const PLACEHOLDER_CHANNEL: Channel = {
  id: 0,
  client_key: "",
  channel_id: 0,
  type: "channel",
  title: "",
  avatar_available: false,
  membership_status: "unknown",
  participants_count: null,
  created_at: "",
  updated_at: "",
  last_synced_at: "",
}

function WorkspaceEmptyState({
  hasChannels,
  onAdd,
}: {
  hasChannels: boolean
  onAdd: () => void
}) {
  return (
    <div className="flex flex-1 items-center justify-center p-6">
      <EmptyState
        icon={hasChannels ? MessageSquareTextIcon : SendIcon}
        title={hasChannels ? "یک کانال انتخاب کنید" : "هنوز کانالی ثبت نشده است"}
        description={
          hasChannels
            ? "برای مشاهده پیام‌ها، یک کانال از فهرست انتخاب کنید یا کانال جدیدی اضافه کنید."
            : "کانال‌ها پس از پیوستن موفق به یک کانال در اینجا ظاهر می‌شوند."
        }
        action={
          <Button onClick={onAdd}>
            <PlusIcon data-icon="inline-start" />
            کانال جدید
          </Button>
        }
      />
    </div>
  )
}

// explainSearchError maps the discovery route's failures onto advice.
function explainSearchError(error: ApiError): string {
  switch (error.code) {
    case "invalid_input":
      return `${error.message} شناسه را به صورت عدد، @username یا لینک t.me وارد کنید.`
    case "channel_not_found":
      return "کانال پیدا نشد یا برای این حساب در دسترس نیست."
    case "needs_auth":
      return "حساب این سرور وارد نشده است؛ ابتدا سرور را وارد کنید."
    case "not_connected":
    case "worker_unavailable":
    case "worker_timeout":
      return `سرور تلگرام در دسترس نیست. ${error.message}`
    case "rate_limited":
      return "تلگرام تعداد درخواست‌ها را محدود کرده است؛ کمی بعد دوباره تلاش کنید."
    case "forbidden":
      return "شما مجوز مشاهدهٔ کانال‌ها را ندارید."
    default:
      return error.message
  }
}

// fileChannel sets one channel's categories to exactly the ids given, through
// the channel's own route.
async function fileChannel(
  token: string,
  channelID: number,
  categoryIDs: number[]
) {
  return apiRequest<{ categories: Category[] }>(
    "PUT",
    `/local/channels/${channelID}/categories`,
    { token, body: { category_ids: categoryIDs } }
  )
}

// DefineChannelDialog adds one channel: it joins it on the chosen server and,
// when a category is chosen, files it there afterwards.
function DefineChannelDialog({
  servers,
  server,
  canPickServer,
  categories,
  error,
  pending,
  onClose,
}: {
  servers: TelegramServer[]
  server?: string
  canPickServer: boolean
  categories: Category[]
  error?: string
  pending: boolean
  onClose: () => void
}) {
  const submit = useSubmit()
  const [ref, setRef] = React.useState("")
  const [chosenServer, setChosenServer] = React.useState(server ?? "")
  const [categoryIds, setCategoryIds] = React.useState<number[]>([])
  const [touched, setTouched] = React.useState(false)
  const refError =
    touched && ref.trim() === ""
      ? "شناسه، نام کاربری یا لینک کانال را وارد کنید."
      : null

  return (
    <Dialog open onOpenChange={(open) => !open && !pending && onClose()}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>کانال جدید</DialogTitle>
          <DialogDescription>
            کانال در سرور انتخاب‌شده پیدا و عضو می‌شود و در فهرست محلی ثبت
            می‌گردد. اگر دسته‌ای انتخاب کنید، بعد از ثبت در همان دسته قرار
            می‌گیرد.
          </DialogDescription>
        </DialogHeader>
        <form
          className="flex flex-col gap-4"
          noValidate
          onSubmit={(event) => {
            event.preventDefault()
            if (ref.trim() === "") {
              setTouched(true)
              return
            }
            const values: Record<string, string | string[]> = {
              intent: "define",
              server: chosenServer,
              ref: ref.trim(),
            }
            if (categoryIds.length > 0) {
              values.category_ids = JSON.stringify(categoryIds.map(Number))
            }
            submit(values, { method: "post" })
          }}
        >
          <Field>
            <FieldLabel htmlFor="define-server" required>
              سرور تلگرام
            </FieldLabel>
            {canPickServer ? (
              <TelegramServerCombobox
                id="define-server"
                servers={servers}
                value={chosenServer || undefined}
                onChange={(key) => setChosenServer(key ?? "")}
              />
            ) : (
              <>
                <FieldControl
                  id="define-server"
                  dir="ltr"
                  value={chosenServer}
                  onChange={(event) => setChosenServer(event.target.value)}
                  placeholder="official"
                  required
                />
                <FieldDescription>
                  فهرست سرورها در دسترس نیست؛ کلید اتصال را بنویسید.
                </FieldDescription>
              </>
            )}
          </Field>
          <Field data-invalid={refError ? "" : undefined}>
            <FieldLabel htmlFor="define-ref" required>
              کانال
            </FieldLabel>
            <FieldControl
              id="define-ref"
              dir="ltr"
              value={ref}
              onChange={(event) => setRef(event.target.value)}
              onBlur={() => setTouched(true)}
              placeholder="@channel یا t.me/channel یا 1234567890"
              required
            />
            {refError ? (
              <FieldError>{refError}</FieldError>
            ) : (
              <FieldDescription>
                نام کاربری (@channel)، لینک t.me، لینک دعوت، شناسهٔ عددی یا
                عبارت جستجو؛ سرور خودش تشخیص می‌دهد.
              </FieldDescription>
            )}
          </Field>
          <CategoryMultiSelect
            id="define-categories"
            title="دسته‌بندی‌ها"
            categories={categories.map((category) => ({
              id: category.id,
              name: category.name,
            }))}
            value={categoryIds}
            onChange={setCategoryIds}
            name="category_ids"
            description={
              categories.length === 0
                ? "هنوز دسته‌بندی‌ای ساخته نشده؛ از صفحهٔ دسته‌بندی‌ها یکی بسازید."
                : "یک کانال می‌تواند هم‌زمان در چند دسته باشد؛ انتخاب دسته الزامی نیست."
            }
          />
          {error && (
            <p
              role="alert"
              className="bg-destructive/10 text-destructive rounded-lg px-3 py-2 text-sm"
            >
              {error}
            </p>
          )}
          <DialogFooter>
            <Button
              type="button"
              variant="outline"
              onClick={onClose}
              disabled={pending}
            >
              انصراف
            </Button>
            <SubmitButton pending={pending} submittingLabel="در حال ثبت…">
              ثبت کانال
            </SubmitButton>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  )
}

// EditChannelDialog changes which categories a channel is in. The channel is
// looked up again when the dialog opens, so it shows the set as it is now, not
// as the list was loaded.
function EditChannelDialog({
  channel,
  categories,
  initial,
  error,
  pending,
  onClose,
}: {
  channel: Channel
  categories: Category[]
  initial: number[]
  error?: string
  pending: boolean
  onClose: () => void
}) {
  const submit = useSubmit()
  const [selected, setSelected] = React.useState<number[]>(initial)
  const [loaded, setLoaded] = React.useState(false)

  React.useEffect(() => {
    const token = getToken()
    if (!token) {
      setLoaded(true)
      return
    }
    let live = true
    apiRequest<{ categories: Category[] }>(
      "GET",
      `/local/channels/${channel.id}/categories`,
      { token }
    )
      .then((result) => {
        if (live) setSelected((result.data.categories ?? []).map((c) => c.id))
      })
      .catch(() => undefined)
      .finally(() => {
        if (live) setLoaded(true)
      })
    return () => {
      live = false
    }
  }, [channel.id])

  return (
    <Dialog open onOpenChange={(open) => !open && !pending && onClose()}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>ویرایش دسته‌بندی</DialogTitle>
          <DialogDescription>
            کانال «{channel.title}» را در هر تعداد دسته‌ای که می‌خواهید قرار
            دهید.
          </DialogDescription>
        </DialogHeader>
        <form
          className="flex flex-col gap-4"
          onSubmit={(event) => {
            event.preventDefault()
            submit(fileCategoriesForm(channel.id, selected), { method: "post" })
          }}
        >
          {!loaded ? (
            <p className="text-muted-foreground py-4 text-sm">
              در حال خواندن دسته‌بندی‌ها…
            </p>
          ) : (
            <CategoryMultiSelect
              id="edit-categories"
              title="دسته‌بندی‌ها"
              categories={categories.map((category) => ({
                id: category.id,
                name: category.name,
              }))}
              value={selected}
              onChange={setSelected}
              name="category_ids"
              description={
                categories.length === 0
                  ? "هنوز دسته‌بندی‌ای ساخته نشده؛ از صفحهٔ دسته‌بندی‌ها یکی بسازید."
                  : "یک کانال می‌تواند هم‌زمان در چند دسته باشد؛ انتخاب دسته الزامی نیست."
              }
            />
          )}
          {error && (
            <p
              role="alert"
              className="bg-destructive/10 text-destructive rounded-lg px-3 py-2 text-sm"
            >
              {error}
            </p>
          )}
          <DialogFooter>
            <Button
              type="button"
              variant="outline"
              onClick={onClose}
              disabled={pending}
            >
              انصراف
            </Button>
            <SubmitButton pending={pending} submittingLabel="در حال ذخیره…">
              ذخیره
            </SubmitButton>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  )
}
