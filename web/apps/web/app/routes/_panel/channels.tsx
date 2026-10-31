import {
  Link,
  redirect,
  useNavigation,
  useRevalidator,
  useSearchParams,
  useSubmit,
} from "react-router"
import {
  CloudOffIcon,
  MessageSquareTextIcon,
  MoreHorizontalIcon,
  PlusIcon,
  SearchIcon,
  SendIcon,
  UsersIcon,
} from "lucide-react"
import * as React from "react"

import { ChannelAvatar } from "@/components/channel-avatar"
import {
  Combobox,
  ComboboxChip,
  ComboboxChips,
  ComboboxChipsInput,
  ComboboxContent,
  ComboboxEmpty,
  ComboboxItem,
  ComboboxList,
  ComboboxValue,
  useComboboxAnchor,
} from "@workspace/ui/components/combobox"
import { ConfirmDialog } from "@/components/confirm-dialog"
import { PageHeader } from "@/components/page-header"
import { PageError, PageShell, pageMeta, section } from "@/lib/page"
import { faDateTime, faDigits } from "@/components/persian-digits"
import { SearchInput } from "@/components/search-input"
import { EmptyState, ErrorState, LoadingState } from "@/components/states"
import { Badge } from "@workspace/ui/components/badge"
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
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from "@workspace/ui/components/dropdown-menu"
import {
  Field,
  FieldDescription,
  FieldError,
  FieldLabel,
  FieldControl,
} from "@workspace/ui/components/field"
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@workspace/ui/components/table"

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
import { connectionLabels, connectionOf } from "@/lib/server-state"

import type { Route } from "./+types/channels"

const PAGE_SIZE = 20

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

// The server selector is a plain GET form: choosing a server is a navigation
// to ?server=<key>, which reloads the loader from the local store. The table is
// always the local database's; nothing about it is read from Telegram.
export async function clientLoader({ request }: Route.LoaderArgs) {
  const { token } = await requireSession(request)
  const url = requestURL(request)
  const q = (url.searchParams.get("q") ?? "").trim()
  const wanted = (url.searchParams.get("server") ?? "").trim()

  let availability: ChannelAvailability
  let servers: TelegramServer[] = []
  let server: string | undefined
  let serversError: string | undefined
  try {
    // The listing is behind "servers.view". Without it this page still shows
    // the local table; a server is named by typing its key, and the API
    // refuses a key it does not serve.
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
    const { data, meta } = await apiRequest<ChannelList>(
      "GET",
      "/local/channels",
      {
        token,
        query: {
          per_page: PAGE_SIZE,
          page: (url.searchParams.get("page") ?? "") || undefined,
          q: q || undefined,
        },
      }
    )
    availability = {
      state: "ready",
      channels: data.channels ?? [],
      meta: {
        total: meta.total,
        has_more: meta.has_more,
        page: meta.page,
        total_pages: meta.total_pages,
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
  // Every category by name: the dialog picks from this list and the table
  // shows which of them hold each channel.
  const categoryList = await listAll<Category>(
    token,
    "/local/categories",
    (data) => (data as { categories: Category[] }).categories ?? []
  )
    .then((result) => [...result.rows].sort((a, b) => a.name.localeCompare(b.name, "fa")))
    .catch(() => [] as Category[])
  // Which categories hold which channel, asked from the category side: one
  // listing per category, in parallel, and a category that cannot be read is
  // simply not shown against any channel.
  const channelCategories: Record<number, string[]> = {}
  if (availability.state === "ready") {
    const filed = await Promise.all(
      categoryList.map(async (category) => {
        const result = await apiRequest<{ channels: Channel[] }>(
          "GET",
          `/local/categories/${category.id}/channels`,
          { token }
        ).catch(() => null)
        return [category, result?.data?.channels ?? []] as const
      })
    )
    for (const [category, channels] of filed) {
      for (const channel of channels) {
        ;(channelCategories[channel.id] ??= []).push(category.name)
      }
    }
  }

  return {
    token,
    q,
    server,
    servers,
    serversError,
    availability,
    categories: categoryList,
    channelCategories,
  }
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

  if (intent === "define") {
    // One field, one query: the discovery route takes the same forms the others
    // do (an id, @username or a t.me link), so nothing here has to classify it.
    const server = String(form.get("server") ?? "")
    const q = String(form.get("ref") ?? "").trim()
    const categoryIDs = form.getAll("category_ids").map(Number)
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
    return redirect(strip(requestURL(request).pathname))
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
    const selectedCategories = form.getAll("category_ids").map(Number)
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
    return redirect(strip(requestURL(request).pathname))
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
    const next = new URLSearchParams(requestURL(request).searchParams)
    return redirect(
      strip(`${requestURL(request).pathname}?${next.toString()}`)
    )
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
        // retry advice (leave with telegram:false).
        return {
          error: `${error.message} — برای حذف فقط رکورد محلی، دوباره با «حذف رکورد محلی» تلاش کنید.`,
          intent,
        }
      }
      return { error: error.message, intent }
    }
    return { error: "عملیات انجام نشد.", intent }
  }
}

export const meta = () => pageMeta("کانال‌ها")

export const handle = section("کانال‌ها")


function serverStateBadge(server: TelegramServer) {
  const state = connectionOf(server)
  switch (state) {
    case "connected":
      return <Badge variant="success">{connectionLabels[state]}</Badge>
    case "connecting":
      return <Badge variant="muted">{connectionLabels[state]}</Badge>
    case "needs_login":
      return <Badge variant="warning">{connectionLabels[state]}</Badge>
    default:
      return <Badge variant="destructive">{connectionLabels[state]}</Badge>
  }
}

// serverLabel is the selector's text: the key, and what the API says about the
// server's connection. Configuration and health are shown as two facts.
function serverLabel(server: TelegramServer) {
  return `${server.key} (${server.type}) — ${connectionLabels[connectionOf(server)]}`
}

export default function Channels({
  loaderData,
  actionData,
}: Route.ComponentProps) {
  const { categories, channelCategories } = loaderData
  const { token, q, server, servers, serversError, availability } = loaderData
  const navigation = useNavigation()
  const revalidator = useRevalidator()
  const submit = useSubmit()
  const [searchParams, setSearchParams] = useSearchParams()
  const [search, setSearch] = React.useState(q)
  const submitting = navigation.state === "submitting"

  React.useEffect(() => setSearch(q), [q])

  // --- table state --------------------------------------------------------
  const loading =
    navigation.state === "loading" && navigation.location?.pathname === "/channels"

  const [confirming, setConfirming] = React.useState<{
    channel: Channel
    telegram: boolean
  } | null>(null)
  const [defining, setDefining] = React.useState(false)
  const [editing, setEditing] = React.useState<Channel | null>(null)
  // A failed save reopens the dialog it came from with the server's message.
  React.useEffect(() => {
    if (actionData?.intent === "define" && actionData.error) setDefining(true)
    if (actionData?.intent === "categories" && actionData.error && editing) {
      setEditing(editing)
    }
    // Reopening only needs the message to appear; editing is kept above.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [actionData])
  const wasSaving = React.useRef(false)
  React.useEffect(() => {
    if (wasSaving.current && !submitting && !actionData?.error) setEditing(null)
    wasSaving.current = submitting
  }, [submitting, actionData])
  const confirmError =
    confirming && actionData?.intent === "leave" ? actionData.error : null

  const wasSubmitting = React.useRef(false)
  React.useEffect(() => {
    if (wasSubmitting.current && !submitting && !actionData?.error)
      setConfirming(null)
    wasSubmitting.current = submitting
  }, [submitting, actionData])

  const onSearchLocal = (event: React.FormEvent<HTMLFormElement>) => {
    event.preventDefault()
    const next = new URLSearchParams(searchParams)
    if (search.trim()) next.set("q", search.trim())
    else next.delete("q")
    next.delete("page")
    setSearchParams(next)
  }

  const goToPage = (page: number) => {
    const next = new URLSearchParams(searchParams)
    if (page > 1) next.set("page", String(page))
    else next.delete("page")
    setSearchParams(next)
  }

  const page =
    availability.state === "ready" ? (availability.meta.page ?? 1) : 1
  const totalPages =
    availability.state === "ready" ? (availability.meta.total_pages ?? 1) : 1

  return (
    <div className="flex flex-col gap-6">
      <PageHeader
        title="کانال‌ها"
        description="کانال‌هایی که برای پایش و خواندن محتوا در فهرست سرورها ثبت کرده‌اید."
        actions={
          <Button onClick={() => setDefining(true)}>
            <PlusIcon data-icon="inline-start" />
            کانال جدید
          </Button>
        }
      />

      {/* D. The local table — always the local database, never Telegram */}
      <section className="flex flex-col gap-3">
        <div className="flex flex-wrap items-center justify-between gap-2">
          <form onSubmit={onSearchLocal} className="flex items-center gap-2" role="search">
            <SearchInput
              name="q"
              value={search}
              onChange={setSearch}
              placeholder="جستجو در کانال‌های محلی…"
              className="w-full sm:w-64"
            />
            <Button type="submit" variant="outline" size="sm">
              جستجو
            </Button>
          </form>
        </div>

        {availability.state === "not_configured" ? (
          <div className="bg-card rounded-xl border">
            <EmptyState
              icon={CloudOffIcon}
              title="ذخیره‌سازی کانال‌های محلی پیکربندی نشده است"
              description={`در این استقرار، کانال‌ها از پنل قابل مشاهده نیستند. پیام سرور: ${availability.message}`}
            />
          </div>
        ) : availability.state === "unavailable" ? (
          <ErrorState
            title="وضعیت کانال‌ها در دسترس نیست"
            message={`${availability.message} (کد: ${availability.code})`}
            onRetry={() => revalidator.revalidate()}
          />
        ) : loading ? (
          <LoadingState label="در حال بارگذاری کانال‌ها…" />
        ) : availability.channels.length === 0 ? (
          <div className="bg-card rounded-xl border">
            <EmptyState
              icon={SendIcon}
              title={q ? "نتیجه‌ای پیدا نشد" : "هیچ کانالی ذخیره نشده است"}
              description={
                q
                  ? "عبارت جستجو را تغییر دهید."
                  : "کانال‌ها پس از پیوستن موفق به یک کانال در اینجا ظاهر می‌شوند."
              }
            />
          </div>
        ) : (
          <div className="bg-card overflow-x-auto rounded-xl border">
            <Table>
              <TableHeader>
                <TableRow>
                  <TableHead>کانال</TableHead>
                  <TableHead>دسته‌بندی</TableHead>
                  <TableHead>سرور</TableHead>
                  <TableHead>اعضا</TableHead>
                  <TableHead>آخرین همگام‌سازی</TableHead>
                  <TableHead className="w-12 text-end">
                    <span className="sr-only">عملیات</span>
                  </TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {availability.channels.map((channel) => (
                  <TableRow key={channel.id}>
                    <TableCell>
                      <div className="flex min-w-0 items-center gap-3">
                        <ChannelAvatar
                          id={channel.id}
                          title={channel.title}
                          className="size-8"
                        />
                        <div className="flex min-w-0 flex-col">
                          <Link
                            to={`/channels/${channel.id}/messages`}
                            className="hover:text-primary max-w-56 truncate font-medium transition-colors"
                          >
                            {channel.title}
                          </Link>
                          <span
                            className="text-muted-foreground max-w-56 truncate text-xs"
                            dir="ltr"
                          >
                            {channel.username
                              ? `@${channel.username}`
                              : channel.channel_id}
                          </span>
                        </div>
                      </div>
                    </TableCell>
                    <TableCell>
                      <div className="flex flex-wrap gap-1">
                        {(channelCategories[channel.id] ?? []).length === 0 ? (
                          <span className="text-muted-foreground text-xs">—</span>
                        ) : (
                          channelCategories[channel.id].map((name) => (
                            <Badge key={name} variant="outline">
                              {name}
                            </Badge>
                          ))
                        )}
                      </div>
                    </TableCell>
                    <TableCell>
                      <Badge variant="secondary">{channel.client_key}</Badge>
                    </TableCell>
                    <TableCell className="text-muted-foreground tabular-nums">
                      {faDigits(channel.participants_count)}
                    </TableCell>
                    <TableCell className="text-muted-foreground">
                      {faDateTime(channel.last_synced_at)}
                    </TableCell>
                    <TableCell className="text-end">
                      <DropdownMenu>
                        <DropdownMenuTrigger
                          render={
                            <Button
                              variant="ghost"
                              size="icon-sm"
                              aria-label={`عملیات کانال ${channel.title}`}
                            />
                          }
                        >
                          <MoreHorizontalIcon />
                        </DropdownMenuTrigger>
                        <DropdownMenuContent align="end">
                          <DropdownMenuItem
                            render={
                              <Link to={`/channels/${channel.id}/messages`} />
                            }
                          >
                            <MessageSquareTextIcon />
                            مشاهدهٔ پیام‌ها
                          </DropdownMenuItem>
                          <DropdownMenuSeparator />
                          <DropdownMenuItem
                            onClick={() => setEditing(channel)}
                          >
                            ویرایش دسته‌بندی‌ها
                          </DropdownMenuItem>
                          <DropdownMenuSeparator />
                          <DropdownMenuItem
                            onClick={() =>
                              setConfirming({ channel, telegram: false })
                            }
                          >
                            حذف رکورد محلی
                          </DropdownMenuItem>
                          <DropdownMenuSeparator />
                          <DropdownMenuItem
                            variant="destructive"
                            onClick={() =>
                              setConfirming({ channel, telegram: true })
                            }
                          >
                            خروج از کانال (تلگرام)
                          </DropdownMenuItem>
                        </DropdownMenuContent>
                      </DropdownMenu>
                    </TableCell>
                  </TableRow>
                ))}
              </TableBody>
            </Table>
            {(totalPages > 1 || page > 1) && (
              <div className="flex items-center justify-between gap-2 border-t px-3 py-2 text-sm">
                <Button
                  variant="outline"
                  size="sm"
                  disabled={page <= 1 || loading}
                  onClick={() => goToPage(page - 1)}
                >
                  قبلی
                </Button>
                <span className="text-muted-foreground tabular-nums">
                  صفحهٔ {faDigits(page)} از {faDigits(totalPages)}
                </span>
                <Button
                  variant="outline"
                  size="sm"
                  disabled={page >= totalPages || loading}
                  onClick={() => goToPage(page + 1)}
                >
                  بعدی
                </Button>
              </div>
            )}
          </div>
        )}
      </section>

      <ConfirmDialog
        open={confirming !== null}
        onOpenChange={(open) => !open && setConfirming(null)}
        title={
          confirming?.telegram ? "خروج از کانال در تلگرام" : "حذف رکورد محلی"
        }
        description={
          confirming ? (
            confirming.telegram ? (
              <>
                از کانال «{confirming.channel.title}» در تلگرام هم خارج می‌شوید
                و رکورد محلی آن حذف می‌شود. اگر تلگرام قبول کند اما حذف رکورد
                محلی ناموفق باشد، پیام سرور نحوه تلاش دوباره را نشان می‌دهد.
              </>
            ) : (
              <>
                فقط رکورد محلی کانال «{confirming.channel.title}» حذف می‌شود؛
                عضویت در تلگرام و داده‌های کانال دست نمی‌خورد.
              </>
            )
          ) : (
            ""
          )
        }
        confirmLabel={
          confirming?.telegram ? "خروج از تلگرام و حذف" : "حذف رکورد"
        }
        pending={submitting}
        error={confirmError}
        onConfirm={() => {
          if (!confirming) return
          submit(
            {
              intent: "leave",
              id: String(confirming.channel.id),
              telegram: confirming.telegram ? "true" : "false",
            },
            { method: "post" }
          )
        }}
      />

      {editing && (
        <EditChannelDialog
          channel={editing}
          categories={categories}
          initial={categories
            .filter((category) =>
              (channelCategories[editing.id] ?? []).includes(category.name)
            )
            .map((category) => category.id)}
          error={actionData?.intent === "categories" ? actionData.error : undefined}
          pending={submitting}
          onClose={() => setEditing(null)}
        />
      )}

      {defining && (
        <DefineChannelDialog
          servers={servers}
          server={server}
          categories={categories}
          error={actionData?.intent === "define" ? actionData.error : undefined}
          pending={submitting}
          onClose={() => setDefining(false)}
        />
      )}
    </div>
  )
}

// DefineChannelDialog adds one channel: it joins it on the chosen server and,
// when a category is chosen, files it there afterwards.
function DefineChannelDialog({
  servers,
  server,
  categories,
  error,
  pending,
  onClose,
}: {
  servers: TelegramServer[]
  server?: string
  categories: Category[]
  error?: string
  pending: boolean
  onClose: () => void
}) {
  const submit = useSubmit()
  const [ref, setRef] = React.useState("")
  const [chosenServer, setChosenServer] = React.useState(server ?? "")
  // The listing needs "servers.view"; without it the key is typed instead of
  // picked, and the API still refuses a key it does not serve.
  const canPickServer = servers.length > 0
  const [categoryIds, setCategoryIds] = React.useState<number[]>([])
  const [touched, setTouched] = React.useState(false)
  const refError =
    touched && ref.trim() === "" ? "شناسه، نام کاربری یا لینک کانال را وارد کنید." : null

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
        {(
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
                values.category_ids = categoryIds.map(String)
              }
              submit(values, { method: "post" })
              return
            }}
          >
            <Field>
              <FieldLabel htmlFor="define-server">سرور تلگرام</FieldLabel>
              {canPickServer ? (
                <FieldControl
                  id="define-server"
                  value={chosenServer}
                  onChange={(event) => setChosenServer(event.target.value)}
                  render={<select />}
                >
                  {servers.map((s) => (
                    <option key={s.key} value={s.key}>
                      {s.key}
                    </option>
                  ))}
                </FieldControl>
              ) : (
                <FieldControl
                  id="define-server"
                  dir="ltr"
                  value={chosenServer}
                  onChange={(event) => setChosenServer(event.target.value)}
                  placeholder="official"
                  required
                />
              )}
            </Field>
            <Field data-invalid={refError ? "" : undefined}>
              <FieldLabel htmlFor="define-ref">کانال</FieldLabel>
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
            <Field>
              <FieldLabel>دسته‌بندی‌ها (اختیاری)</FieldLabel>
              <CategoryPicker
                categories={categories}
                selected={categoryIds}
                onChange={setCategoryIds}
              />
              <FieldDescription>
                یک کانال می‌تواند هم‌زمان در چند دسته باشد.
              </FieldDescription>
            </Field>
            {error && (
              <p
                role="alert"
                className="bg-destructive/10 text-destructive rounded-lg px-3 py-2 text-sm"
              >
                {error}
              </p>
            )}
            <DialogFooter>
              <Button type="button" variant="outline" onClick={onClose} disabled={pending}>
                انصراف
              </Button>
              <Button type="submit" disabled={pending}>
                {pending ? "در حال ثبت…" : "ثبت کانال"}
              </Button>
            </DialogFooter>
          </form>
        )}
      </DialogContent>
    </Dialog>
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
async function fileChannel(token: string, channelID: number, categoryIDs: number[]) {
  return apiRequest<{ categories: Category[] }>(
    "PUT",
    `/local/channels/${channelID}/categories`,
    { token, body: { category_ids: categoryIDs } }
  )
}

// CategoryPicker is the set of categories a channel is in: a combobox whose
// chosen categories sit in the field as chips, and whose list filters as you
// type. The chosen ones are submitted as repeated `category_ids` fields, so one
// channel can be in as many categories as the operator wants.
function CategoryPicker({
  categories,
  selected,
  onChange,
}: {
  categories: Category[]
  selected: number[]
  onChange: (selected: number[]) => void
}) {
  const anchor = useComboboxAnchor()
  const names = categories.map((category) => category.name)
  const chosen = categories
    .filter((category) => selected.includes(category.id))
    .map((category) => category.name)

  if (categories.length === 0) {
    return (
      <p className="text-muted-foreground text-sm">
        هنوز دسته‌بندی‌ای ساخته نشده؛ از صفحهٔ دسته‌بندی‌ها یکی بسازید.
      </p>
    )
  }

  return (
    <>
      <Combobox
        multiple
        autoHighlight
        items={names}
        value={chosen}
        onValueChange={(value) => {
          const picked = (value as string[] | null) ?? []
          onChange(
            categories
              .filter((category) => picked.includes(category.name))
              .map((category) => category.id)
          )
        }}
      >
        <ComboboxChips ref={anchor} className="w-full">
          <ComboboxValue>
            {(values: string[]) => (
              <React.Fragment>
                {values.map((value) => (
                  <ComboboxChip key={value}>{value}</ComboboxChip>
                ))}
                <ComboboxChipsInput placeholder="جستجوی دسته‌بندی…" />
              </React.Fragment>
            )}
          </ComboboxValue>
        </ComboboxChips>
        <ComboboxContent anchor={anchor}>
          <ComboboxEmpty>دسته‌بندی‌ای پیدا نشد.</ComboboxEmpty>
          <ComboboxList>
            {(item: string) => (
              <ComboboxItem key={item} value={item}>
                {item}
              </ComboboxItem>
            )}
          </ComboboxList>
        </ComboboxContent>
      </Combobox>
      {selected.map((id) => (
        <input key={id} type="hidden" name="category_ids" value={id} />
      ))}
    </>
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
          <DialogTitle>ویرایش دسته‌بندی‌ها</DialogTitle>
          <DialogDescription>
            کانال «{channel.title}» را در هر تعداد دسته‌ای که می‌خواهید قرار
            دهید.
          </DialogDescription>
        </DialogHeader>
        <form
          className="flex flex-col gap-4"
          onSubmit={(event) => {
            event.preventDefault()
            const values: Record<string, string | string[]> = {
              intent: "categories",
              id: String(channel.id),
            }
            if (selected.length > 0) {
              values.category_ids = selected.map(String)
            }
            submit(values, { method: "post" })
          }}
        >
          {!loaded ? (
            <LoadingState label="در حال خواندن دسته‌بندی‌ها…" />
          ) : (
            <CategoryPicker
              categories={categories}
              selected={selected}
              onChange={setSelected}
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
            <Button type="button" variant="outline" onClick={onClose} disabled={pending}>
              انصراف
            </Button>
            <Button type="submit" disabled={pending || !loaded}>
              {pending ? "در حال ذخیره…" : "ذخیره"}
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  )
}
