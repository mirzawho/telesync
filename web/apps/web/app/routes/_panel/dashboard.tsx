import { Link, useRevalidator, useRouteLoaderData } from "react-router"
import {
  ArrowLeftIcon,
  AtSignIcon,
  FolderTreeIcon,
  GlobeIcon,
  LayoutDashboardIcon,
  RefreshCwIcon,
  SendIcon,
  ServerCogIcon,
  ShieldOffIcon,
  UserCogIcon,
} from "lucide-react"

import { PageHeader } from "@/components/page-header"
import { PageError, PageShell, pageMeta, section } from "@/lib/page"
import { faDigits } from "@/components/persian-digits"
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
import type { DashboardStats, TelegramServer, TelegramUser } from "@/lib/types"

import type { Route } from "./+types/dashboard"

interface ServerList {
  servers: TelegramServer[]
}

// One resource's summary: either counts/rows the API returned, or the
// failure to report in that card alone while the rest of the page renders.
type Resource<T> =
  { ok: true; total: number | null; rows: T[] } | { ok: false; message: string }

// failure names why a call did not answer, in the API's own words when it had
// some and in the panel's otherwise.
function failure(reason: unknown, fallback: string): string {
  return reason instanceof ApiError ? reason.message : fallback
}

// The dashboard's four figures, or the failure to load them. The same
// two-state shape every other card uses; on success each figure decides for
// itself whether a card is drawn at all.
type StatsResult =
  { ok: true; stats: DashboardStats } | { ok: false; message: string }

// Tailwind generates only the class names it finds written out, so the column
// count is picked from a literal per case and never built as `grid-cols-${n}`.
const GRID_COLUMNS = {
  1: "grid-cols-1",
  2: "grid-cols-2",
  3: "grid-cols-3",
  4: "grid-cols-4",
} as const

// columnsFor is how many columns the figure row takes: one per card that is
// drawn, clamped into the cases above. A row with no cards draws no grid at all.
function columnsFor(count: number): keyof typeof GRID_COLUMNS {
  return Math.min(Math.max(count, 1), 4) as keyof typeof GRID_COLUMNS
}

// One card per figure. A figure the caller may not see is null, and then there
// is no card at all — not a zero, not a "no access" notice. `access` names the
// /auth/me flag the sidebar uses for the same link, so a card links exactly
// where the navigation goes; the WordPress page has no such flag and is always
// reachable, like its sidebar item.
const STAT_CARDS = [
  {
    key: "categories",
    icon: FolderTreeIcon,
    title: "دسته‌بندی‌ها",
    to: "/categories",
    access: "categories",
  },
  {
    key: "channels",
    icon: SendIcon,
    title: "کانال‌ها",
    to: "/channels",
    access: "channels",
  },
  {
    key: "wordpress_sites",
    icon: GlobeIcon,
    title: "سایت‌های وردپرسی",
    to: "/wordpress",
    access: null,
  },
  {
    key: "administrators",
    icon: UserCogIcon,
    title: "مدیرها",
    to: "/administrators",
    access: "users",
  },
] as const

type StatCardData = {
  key: string
  icon: typeof FolderTreeIcon
  title: string
  to?: string
  resource: Resource<unknown>
}

// statCards draws the cards whose figures are actually there. A key the API
// left out is read like null: no card, never a zero.
function statCards(
  stats: DashboardStats | undefined,
  access: { channels: boolean; categories: boolean; users: boolean }
): StatCardData[] {
  const cards: StatCardData[] = []
  for (const card of STAT_CARDS) {
    const value = stats?.[card.key]
    if (typeof value !== "number") {
      continue
    }
    cards.push({
      key: card.key,
      icon: card.icon,
      title: card.title,
      to: card.access === null || access[card.access] ? card.to : undefined,
      resource: { ok: true, total: value, rows: [] },
    })
  }
  return cards
}

// hydrate lets this screen render straight from the first payload, with no
// second request for the same counts.
export async function clientLoader({ request }: Route.LoaderArgs) {
  const { token, me } = await requireSession(request)

  // The four figures come from one call. The server status card reads
  // GET /api/v1/telegram, which the API holds behind "servers.view"; a failing
  // call degrades its own card, never the page.
  const [statsResult, serversResult] = await Promise.allSettled([
    apiRequest<{ stats: DashboardStats }>("GET", "/local/stats/dashboard", {
      token,
    }),
    me.access?.servers
      ? apiRequest<ServerList>("GET", "/telegram", { token })
      : Promise.resolve(null),
  ] as const)

  const stats: StatsResult =
    statsResult.status === "fulfilled"
      ? { ok: true, stats: statsResult.value.data.stats }
      : {
          ok: false,
          message: failure(statsResult.reason, "بارگذاری آمار ناموفق بود."),
        }

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
            message: failure(
              serversResult.reason,
              "بارگذاری وضعیت سرورها ناموفق بود."
            ),
          }
        : null

  return {
    access: me.access ?? {
      channels: false,
      categories: false,
      users: false,
      servers: false,
    },
    stats,
    servers: serverResource,
  }
}

export const meta = () => pageMeta("پیشخان")

export const handle = section("پیشخان")

// The account the layout's loader already read for this navigation.
function useMe() {
  return (
    useRouteLoaderData("routes/_panel/layout")?.me ?? {
      fullname: "",
      username: "",
    }
  )
}

export function ErrorBoundary({ error }: Route.ErrorBoundaryProps) {
  return (
    <PageShell title="پیشخان">
      <PageError error={error} />
    </PageShell>
  )
}

export default function Dashboard({ loaderData }: Route.ComponentProps) {
  const { access, stats, servers } = loaderData
  const me = useMe()

  // The figure row draws a card for each figure the caller may see: a null
  // figure is a card that is not there, and the grid is sized from what is
  // left. A call that failed is one card of its own — never four invented
  // zeros.
  const cards: StatCardData[] = stats.ok
    ? statCards(stats.stats, access)
    : [
        {
          key: "stats",
          icon: LayoutDashboardIcon,
          title: "آمار کلی",
          resource: { ok: false, message: stats.message },
        },
      ]

  return (
    <div className="flex min-w-0 flex-col gap-6">
      <PageHeader
        title="پیشخان"
        description={
          me.fullname
            ? `${me.fullname}، خوش آمدید. یک نگاه به دسته‌بندی‌ها، کانال‌ها، سایت‌های وردپرسی، مدیرها و وضعیت سرورهای تلگرام.`
            : "یک نگاه به دسته‌بندی‌ها، کانال‌ها، سایت‌های وردپرسی، مدیرها و وضعیت سرورهای تلگرام."
        }
      />

      {cards.length > 0 && <StatCardRow cards={cards} />}

      <ServerStatusCard
        resource={access.servers ? servers : null}
        description="هر سرور تلگرامی که این سرویس راه انداخته، و این‌که همین حالا به تلگرام وصل است یا نه."
      />
    </div>
  )
}

// Below lg the cards are one row that scrolls sideways, each at a fixed width
// so none is squeezed; at lg they go back to the grid. Each wrapper stretches
// its card to the row's height so the cards line up whichever of them wraps.
export const STAT_ROW_CLASS =
  "flex w-full min-w-0 snap-x snap-proximity gap-4 overflow-x-auto overscroll-x-contain pb-2 lg:grid lg:overflow-visible"
export const STAT_CARD_WRAPPER_CLASS =
  "flex w-[240px] shrink-0 snap-start lg:w-auto lg:min-w-0"

export function StatCardRow({
  cards,
  link,
}: {
  cards: StatCardData[]
  link?: (to: string) => React.ReactElement
}) {
  // The router's Link by default; the responsive test swaps in a plain <a>, so
  // the row renders without a router around it.
  const linkFor = link ?? ((to: string) => <Link to={to} />)
  return (
    <section
      aria-label="آمار کلی"
      className={`${STAT_ROW_CLASS} ${GRID_COLUMNS[columnsFor(cards.length)]}`}
    >
      {cards.map(({ key, ...card }) => (
        <div key={key} className={STAT_CARD_WRAPPER_CLASS}>
          <StatCard {...card} link={linkFor} />
        </div>
      ))}
    </section>
  )
}

function StatCard({
  icon: Icon,
  title,
  resource,
  to,
  link,
}: {
  icon: typeof FolderTreeIcon
  title: string
  resource: Resource<unknown>
  to?: string
  // The router's Link; the responsive test swaps in a plain <a>.
  link: (to: string) => React.ReactElement
}) {
  return (
    <Card className="h-full w-full">
      <CardHeader>
        <CardDescription className="flex items-center gap-2">
          <span className="flex size-7 items-center justify-center rounded-lg bg-muted text-foreground">
            <Icon className="size-4" aria-hidden="true" />
          </span>
          {title}
        </CardDescription>
        {resource.ok ? (
          <CardTitle className="text-2xl tabular-nums">
            {faDigits(resource.total)}
          </CardTitle>
        ) : (
          <CardTitle className="text-sm font-normal text-destructive">
            در دسترس نیست
          </CardTitle>
        )}
      </CardHeader>
      <CardContent>
        {!resource.ok && (
          <p role="status" className="text-xs text-muted-foreground">
            {resource.message}
          </p>
        )}
      </CardContent>
      {to && (
        // Pinned to the bottom so a card that failed (its message takes room)
        // still lines its link up with the others in the row.
        <CardFooter className="mt-auto justify-end pt-1">
          <Button variant="ghost" size="sm" render={link(to)}>
            مشاهده
            <ArrowLeftIcon data-icon="inline-end" />
          </Button>
        </CardFooter>
      )}
    </Card>
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
      <span className="inline-flex items-center gap-1 text-xs text-muted-foreground">
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
        <span className="inline-flex shrink-0 items-center gap-0.5 text-xs text-muted-foreground">
          <AtSignIcon className="size-3" aria-hidden="true" />
          {user.username}
        </span>
      ) : (
        <span className="shrink-0 text-xs text-muted-foreground">
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
            <RefreshCwIcon
              className={refreshing ? "animate-spin" : undefined}
            />
            به‌روزرسانی
          </Button>
        </div>
      </CardHeader>
      <CardContent>
        {resource === null ? (
          <p
            role="status"
            className="py-6 text-center text-sm text-muted-foreground"
          >
            شما دسترسی خواندن وضعیت سرورها را ندارید.
          </p>
        ) : !resource.ok ? (
          <p
            role="status"
            className="py-6 text-center text-sm text-muted-foreground"
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
                  <span className="text-xs text-muted-foreground">
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
