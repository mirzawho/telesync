import { Link, useRevalidator, useRouteLoaderData } from "react-router"
import {
  ArrowLeftIcon,
  AtSignIcon,
  FolderTreeIcon,
  RefreshCwIcon,
  SendIcon,
  ServerCogIcon,
  ShieldOffIcon,
  UserCogIcon,
  Users2Icon,
} from "lucide-react"

import { PageHeader } from "@/components/page-header"
import { PageError, PageShell, pageMeta, section } from "@/lib/page"
import { StatCardSkeleton } from "@/components/states"
import { faDateTime, faDigits } from "@/components/persian-digits"
import { EmptyState } from "@/components/states"
import { Badge } from "@workspace/ui/components/badge"
import { Button } from "@workspace/ui/components/button"
import {
  Card,
  CardContent,
  CardDescription,
  CardFooter,
  CardHeader,
  CardTitle,
} from "@workspace/ui/components/card"

import { ApiError, apiRequest } from "@/lib/api"
import { requireSession } from "@/lib/session"
import { connectionLabels, connectionOf } from "@/lib/server-state"
import type {
  Category,
  Channel,
  ChannelAvailability,
  TelegramServer,
  TelegramUser,
  User,
} from "@/lib/types"

import type { Route } from "./+types/dashboard"

interface CategoryList {
  categories: Category[]
}
interface ChannelList {
  channels: Channel[]
}
interface UserList {
  users: User[]
}
interface ServerList {
  servers: TelegramServer[]
}

// One resource's summary: either counts/rows the API returned, or the
// failure to report in that card alone while the rest of the page renders.
type Resource<T> =
  { ok: true; total: number | null; rows: T[] } | { ok: false; message: string }

function toResource<T>(
  result: PromiseSettledResult<{ data: unknown; meta: { total?: number } }>,
  pick: (data: unknown) => T[]
): Resource<T> {
  if (result.status === "rejected") {
    const error = result.reason
    return {
      ok: false,
      message:
        error instanceof ApiError
          ? error.message
          : "بارگذاری این بخش ناموفق بود.",
    }
  }
  return {
    ok: true,
    total: result.value.meta.total ?? null,
    rows: pick(result.value.data),
  }
}

async function list<T>(
  token: string,
  path: string,
  pick: (data: unknown) => T[]
): Promise<{ data: unknown; meta: { total?: number }; rows: T[] }> {
  const result = await apiRequest<unknown>("GET", path, {
    token,
    query: { per_page: 100 },
  })
  return { ...result, rows: pick(result.data) }
}

// hydrate lets this screen render straight from the first payload, with no
// second request for the same counts.
export async function clientLoader({ request }: Route.LoaderArgs) {
  const { token, me } = await requireSession(request)

  // The three summaries load independently: a failing resource (the local
  // channel store is often not configured) degrades its own card, not the page.
  const [categoriesResult, channelsResult, usersResult, serversResult] =
    await Promise.allSettled([
      list<Category>(
        token,
        "/local/categories",
        (data) => (data as CategoryList).categories ?? []
      ),
      list<Channel>(
        token,
        "/local/channels",
        (data) => (data as ChannelList).channels ?? []
      ),
      me.access?.users
        ? list<User>(
            token,
            "/local/users",
            (data) => (data as UserList).users ?? []
          )
        : Promise.resolve(null),
      // The server status card reads GET /api/v1/telegram, which the API holds
      // behind "servers.view"; the card alone shows a failure, the page loads.
      me.access?.servers
        ? apiRequest<ServerList>("GET", "/telegram", { token })
        : Promise.resolve(null),
    ] as const)

  const channelState: ChannelAvailability = (() => {
    if (channelsResult.status === "rejected") {
      const error = channelsResult.reason
      if (
        error instanceof ApiError &&
        (error.status === 503 || error.status === 400 || error.status === 401)
      ) {
        return { state: "not_configured", message: error.message }
      }
      return {
        state: "unavailable",
        message:
          error instanceof ApiError
            ? error.message
            : "بارگذاری کانال‌ها ناموفق بود.",
        code: error instanceof ApiError ? error.code : "unknown",
      }
    }
    const channels = channelsResult.value.rows
    return {
      state: "ready",
      channels,
      meta: { total: channelsResult.value.meta.total, has_more: undefined },
    }
  })()

  const userResource: Resource<User> | null =
    usersResult.status === "fulfilled" && usersResult.value !== null
      ? {
          ok: true,
          total: usersResult.value.meta.total ?? null,
          rows: usersResult.value.rows,
        }
      : usersResult.status === "rejected"
        ? {
            ok: false,
            message:
              usersResult.reason instanceof ApiError
                ? usersResult.reason.message
                : "بارگذاری مدیرها ناموفق بود.",
          }
        : null

  const serverResource: Resource<TelegramServer> | null =
    serversResult.status === "fulfilled" && serversResult.value !== null
      ? {
          ok: true,
          total: serversResult.value.data.servers?.length ?? 0,
          rows: serversResult.value.data.servers ?? [],
        }
      : serversResult.status === "rejected"
        ? {
            ok: false,
            message:
              serversResult.reason instanceof ApiError
                ? serversResult.reason.message
                : "بارگذاری وضعیت سرورها ناموفق بود.",
          }
        : null

  const categoryResource = toResource(
    categoriesResult,
    (data) => (data as CategoryList).categories ?? []
  )

  return {
    access: me.access ?? {
      channels: false,
      categories: false,
      users: false,
      servers: false,
    },
    categories: categoryResource,
    recentCategories: categoryResource.ok
      ? categoryResource.rows.slice(0, 5)
      : [],
    channels: channelState,
    users: userResource,
    servers: serverResource,
  }
}

export const meta = () => pageMeta("پیشخان")

export const handle = section("پیشخان")

// The account the layout's loader already read for this navigation.
function useMe() {
  return useRouteLoaderData("routes/_panel/layout")?.me ?? { fullname: "", username: "" }
}

export function ErrorBoundary({ error }: Route.ErrorBoundaryProps) {
  return (
    <PageShell title="پیشخان">
      <PageError error={error} />
    </PageShell>
  )
}

export default function Dashboard({ loaderData }: Route.ComponentProps) {
  const { access, categories, recentCategories, channels, servers, users } =
    loaderData
  const me = useMe()

  return (
    <div className="flex flex-col gap-6">
      <PageHeader
        title="پیشخان"
        description={
          me.fullname
            ? `${me.fullname}، خوش آمدید. یک نگاه به کانال‌های تحت پایش، دسته‌بندی‌ها و مدیرهای پنل.`
            : "یک نگاه به کانال‌های تحت پایش، دسته‌بندی‌ها و مدیرهای پنل."
        }
      />

      <section
        aria-label="آمار کلی"
        className="grid gap-4 sm:grid-cols-2 lg:grid-cols-3"
      >
        <StatCard
          icon={FolderTreeIcon}
          title="دسته‌بندی‌ها"
          resource={
            access.categories
              ? categories
              : {
                  ok: false,
                  message: "شما دسترسی خواندن دسته‌بندی‌ها را ندارید.",
                }
          }
          footnote="دسته‌هایی که کانال‌ها را گروه‌بندی می‌کنند"
          to={access.categories ? "/categories" : undefined}
        />
        <StatCard
          icon={SendIcon}
          title="کانال‌ها"
          resource={
            !access.channels
              ? { ok: false, message: "شما دسترسی خواندن کانال‌ها را ندارید." }
              : channels.state === "ready"
                ? {
                    ok: true,
                    total: channels.meta.total ?? null,
                    rows: channels.channels,
                  }
                : { ok: false, message: channels.message }
          }
          footnote="کانال‌هایی که این سرویس دنبال می‌کند"
          to={access.channels ? "/channels" : undefined}
        />
        <StatCard
          icon={UserCogIcon}
          title="مدیرها"
          resource={
            users === null
              ? { ok: false, message: "شما دسترسی خواندن مدیرها را ندارید." }
              : users
          }
          footnote="کاربرانی که به پنل دسترسی دارند"
          to={access.users ? "/administrators" : undefined}
        />
      </section>

      <div className="grid gap-4 lg:grid-cols-2">
        <Card>
          <CardHeader>
            <CardTitle>تازه‌ترین دسته‌بندی‌ها</CardTitle>
            <CardDescription>
              پنج دسته‌ای که آخر از همه ساخته شده‌اند.
            </CardDescription>
          </CardHeader>
          <CardContent>
            {!categories.ok ? (
              <p
                role="status"
                className="text-muted-foreground py-6 text-center text-sm"
              >
                {categories.message}
              </p>
            ) : recentCategories.length === 0 ? (
              <EmptyState
                icon={FolderTreeIcon}
                title="هنوز دسته‌بندی‌ای نساخته‌اید"
                description="برای گروه‌بندی کانال‌ها، از صفحهٔ دسته‌بندی‌ها یکی بسازید."
                className="py-8"
              />
            ) : (
              <ul className="flex flex-col divide-y">
                {recentCategories.map((category) => (
                  <li
                    key={category.id}
                    className="flex flex-col gap-1 py-3 first:pt-0 last:pb-0"
                  >
                    <div className="flex items-baseline justify-between gap-3">
                      <span className="truncate text-sm font-medium">
                        {category.name}
                      </span>
                      <span className="text-muted-foreground shrink-0 text-xs">
                        {faDateTime(category.created_at)}
                      </span>
                    </div>
                    {category.description && (
                      <p className="text-muted-foreground line-clamp-1 text-xs">
                        {category.description}
                      </p>
                    )}
                  </li>
                ))}
              </ul>
            )}
          </CardContent>
          {access.categories && categories.ok && (
            <CardFooter className="justify-end pt-2">
              <Button
                variant="link"
                size="sm"
                render={<Link to="/categories" />}
              >
                همه دسته‌بندی‌ها
                <ArrowLeftIcon data-icon="inline-end" />
              </Button>
            </CardFooter>
          )}
        </Card>

        <Card>
          <CardHeader>
            <CardTitle>کانال‌های تحت پایش</CardTitle>
            <CardDescription>
              کانال‌های تلگرامی که این سرویس دنبال می‌کند.
            </CardDescription>
          </CardHeader>
          <CardContent className="flex-1">
            <ChannelStatus availability={channels} access={access.channels} />
          </CardContent>
          {access.channels && (
            <CardFooter className="justify-end pt-2">
              <Button variant="link" size="sm" render={<Link to="/channels" />}>
                صفحه کانال‌ها
                <ArrowLeftIcon data-icon="inline-end" />
              </Button>
            </CardFooter>
          )}
        </Card>
      </div>

      <ServerStatusCard
        resource={access.servers ? servers : null}
        description="هر سرور تلگرامی که این سرویس راه انداخته، و این‌که همین حالا به تلگرام وصل است یا نه."
      />

      <p className="text-muted-foreground text-xs">
        اگر یکی از کارت‌ها «در دسترس نیست» نشان می‌دهد، یعنی آن بخش الان خواندنی
        نبود؛ عددی به‌جای آن حدس زده نمی‌شود.
      </p>
    </div>
  )
}

function StatCard({
  icon: Icon,
  title,
  resource,
  footnote,
  to,
}: {
  icon: typeof FolderTreeIcon
  title: string
  resource: Resource<unknown>
  footnote: string
  to?: string
}) {
  return (
    <Card>
      <CardHeader>
        <CardDescription className="flex items-center gap-2">
          <span className="bg-muted text-foreground flex size-7 items-center justify-center rounded-lg">
            <Icon className="size-4" aria-hidden="true" />
          </span>
          {title}
        </CardDescription>
        {resource.ok ? (
          <CardTitle className="text-2xl tabular-nums">
            {faDigits(resource.total)}
          </CardTitle>
        ) : (
          <CardTitle className="text-destructive text-sm font-normal">
            در دسترس نیست
          </CardTitle>
        )}
      </CardHeader>
      <CardContent className="flex flex-col gap-1">
        {resource.ok ? (
          <p className="text-muted-foreground text-xs">{footnote}</p>
        ) : (
          <p role="status" className="text-muted-foreground text-xs">
            {resource.message}
          </p>
        )}
      </CardContent>
      {to && (
        <CardFooter className="justify-end pt-1">
          <Button variant="ghost" size="sm" render={<Link to={to} />}>
            مشاهده
            <ArrowLeftIcon data-icon="inline-end" />
          </Button>
        </CardFooter>
      )}
    </Card>
  )
}

function ChannelStatus({
  availability,
  access,
}: {
  availability: ChannelAvailability
  access: boolean
}) {
  if (!access) {
    return (
      <p className="text-muted-foreground py-6 text-center text-sm">
        شما دسترسی خواندن کانال‌ها را ندارید.
      </p>
    )
  }
  if (availability.state === "not_configured") {
    return (
      <EmptyState
        icon={SendIcon}
        title="فهرست کانال‌ها در دسترس نیست"
        description="این نسخه از سرویس هنوز کانال‌ها را در پنل نشان نمی‌دهد. مدیر سرویس باید آن را فعال کند."
        className="py-8"
      />
    )
  }
  if (availability.state === "unavailable") {
    return (
      <EmptyState
        icon={SendIcon}
        title="وضعیت کانال‌ها معلوم نیست"
        description="الان نتوانستیم فهرست کانال‌ها را بخوانیم. کمی بعد دوباره سر بزنید."
        className="py-8"
      />
    )
  }
  if (availability.channels.length === 0) {
    return (
      <EmptyState
        icon={SendIcon}
        title="هنوز کانالی اضافه نشده است"
        description="کانالی که در صفحهٔ کانال‌ها ثبت کنید، این‌جا هم دیده می‌شود."
        className="py-8"
      />
    )
  }
  const joined = availability.channels.filter(
    (channel) => channel.membership_status === "joined"
  ).length
  return (
    <div className="flex flex-col items-center gap-1 py-8 text-center">
      <span className="flex size-10 items-center justify-center rounded-full bg-emerald-500/10 text-emerald-700 dark:text-emerald-400">
        <Users2Icon className="size-5" aria-hidden="true" />
      </span>
      <p className="text-2xl font-semibold tabular-nums">
        {faDigits(availability.channels.length)}
      </p>
      <p className="text-muted-foreground text-sm">
        کانال تحت پایش؛ {faDigits(joined)} تا از آن‌ها با عضویت برقرار.
      </p>
    </div>
  )
}

function ServerStateBadge({ server }: { server: TelegramServer }) {
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

function ServerAccount({ user }: { user?: TelegramUser | null }) {
  if (!user) {
    return (
      <span className="text-muted-foreground inline-flex items-center gap-1 text-xs">
        <ShieldOffIcon className="size-3.5" aria-hidden="true" />
        حساب معلوم نیست
      </span>
    )
  }
  const name = [user.first_name, user.last_name].filter(Boolean).join(" ")
  return (
    <span className="inline-flex min-w-0 items-baseline gap-1.5">
      <span className="truncate text-sm">{name || "بدون نام"}</span>
      {user.username ? (
        <span className="text-muted-foreground inline-flex shrink-0 items-center gap-0.5 text-xs">
          <AtSignIcon className="size-3" aria-hidden="true" />
          {user.username}
        </span>
      ) : (
        <span className="text-muted-foreground shrink-0 text-xs">
          شناسه {faDigits(user.id)}
        </span>
      )}
    </span>
  )
}

// ServerStatusCard is the dashboard's answer to "what is the Telegram
// connection doing". It is its own card, so a failure or a missing permission
// degrades this card alone; the server's Telegram account is shown, never the
// caller's. Its refresh button re-runs this page's loader, which is the one
// request that asks every server for its health again.
function ServerStatusCard({
  resource,
  description,
}: {
  resource: Resource<TelegramServer> | null
  description: string
}) {
  const revalidator = useRevalidator()
  const refreshing = revalidator.state === "loading"
  return (
    <Card>
      <CardHeader>
        <div className="flex flex-wrap items-start justify-between gap-2">
          <div className="flex flex-col gap-1.5">
            <CardTitle>وضعیت سرورهای تلگرام</CardTitle>
            <CardDescription>{description}</CardDescription>
          </div>
          <Button
            variant="outline"
            size="sm"
            onClick={() => revalidator.revalidate()}
            disabled={refreshing || resource === null}
          >
            <RefreshCwIcon className={refreshing ? "animate-spin" : undefined} />
            به‌روزرسانی
          </Button>
        </div>
      </CardHeader>
      <CardContent>
        {resource === null ? (
          <p
            role="status"
            className="text-muted-foreground py-6 text-center text-sm"
          >
            شما دسترسی خواندن وضعیت سرورها را ندارید.
          </p>
        ) : !resource.ok ? (
          <p
            role="status"
            className="text-muted-foreground py-6 text-center text-sm"
          >
            {resource.message}
          </p>
        ) : resource.rows.length === 0 ? (
          <EmptyState
            icon={ServerCogIcon}
            title="سروری پیکربندی نشده است"
            description="هر سروری که در فایل پیکربندی بنویسید و فعال بماند، این‌جا دیده می‌شود."
            className="py-8"
          />
        ) : (
          <ul className="flex flex-col divide-y">
            {resource.rows.map((server) => (
              <li
                key={server.key}
                className="flex flex-wrap items-center justify-between gap-x-4 gap-y-1 py-3 first:pt-0 last:pb-0"
              >
                <div className="flex min-w-0 flex-col">
                  <span className="font-mono text-sm">{server.key}</span>
                  <span className="text-muted-foreground text-xs">
                    {server.type === "custom" ? "سرور اختصاصی" : "رسمی تلگرام"}
                    {server.last_error ? ` — ${server.last_error}` : ""}
                  </span>
                </div>
                <div className="flex flex-col items-end gap-1">
                  <ServerStateBadge server={server} />
                  <ServerAccount user={server.user} />
                </div>
              </li>
            ))}
          </ul>
        )}
      </CardContent>
    </Card>
  )
}
