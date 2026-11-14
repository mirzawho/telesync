import {
  Link,
  useNavigation,
  useRevalidator,
  useRouteLoaderData,
} from "react-router"
import {
  ArrowLeftIcon,
  FolderTreeIcon,
  GlobeIcon,
  LayoutDashboardIcon,
  SendIcon,
  UserCogIcon,
} from "lucide-react"

import { PageHeader } from "@/components/page-header"
import { PageError, PageShell, pageMeta, section } from "@/lib/page"
import { faDigits } from "@/components/persian-digits"
import { EmptyState, ErrorState, LoadingState } from "@/components/states"
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
import { listMyAccounts } from "@/lib/account-api"
import { accountErrorText } from "@/lib/account-errors"
import { accountTitle, serverLabel } from "@/lib/telegram-account"
import { requireSession } from "@/lib/session"
import type { DashboardStats, TelegramAccount } from "@/lib/types"
import {
  AccountAvatar,
  AccountStatusBadge,
} from "@/components/accounts/account-list"

import type { Route } from "./+types/dashboard"

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

type PersonalAccountsResult =
  { ok: true; rows: TelegramAccount[] } | { ok: false; message: string }

function accountFailure(reason: unknown): string {
  return reason instanceof ApiError
    ? accountErrorText(reason.code, reason.message)
    : "بارگذاری وضعیت حساب‌ها ناموفق بود."
}

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

  // Collection metrics and personal account status are independent reads. The
  // account list is the only source of account connectivity: global server
  // health describes configured backends, not this user's sessions.
  const [statsResult, accountsResult] = await Promise.allSettled([
    apiRequest<{ stats: DashboardStats }>("GET", "/local/stats/dashboard", {
      token,
    }),
    listMyAccounts(token),
  ] as const)

  const stats: StatsResult =
    statsResult.status === "fulfilled"
      ? { ok: true, stats: statsResult.value.data.stats }
      : {
          ok: false,
          message: failure(statsResult.reason, "بارگذاری آمار ناموفق بود."),
        }

  const personalAccounts: PersonalAccountsResult =
    accountsResult.status === "fulfilled"
      ? { ok: true, rows: accountsResult.value.accounts }
      : { ok: false, message: accountFailure(accountsResult.reason) }

  return {
    access: me.access ?? {
      channels: false,
      categories: false,
      users: false,
      servers: false,
      automation: false,
    },
    stats,
    personalAccounts,
  }
}

export const meta = () => pageMeta("وضعیت حساب‌های من")

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
    <PageShell title="وضعیت حساب‌های من">
      <PageError error={error} />
    </PageShell>
  )
}

export function DashboardContent({
  loaderData,
  me,
  accountsLoading = false,
  onRetry,
  link,
}: {
  loaderData: Awaited<ReturnType<typeof clientLoader>>
  me: { fullname?: string }
  accountsLoading?: boolean
  onRetry?: () => void
  link?: (to: string) => React.ReactElement
}) {
  const { access, stats, personalAccounts } = loaderData
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
        title="وضعیت حساب‌های من"
        description={
          me.fullname
            ? `${me.fullname}، وضعیت اتصال حساب‌های تلگرامی خود و آمار مجموعه‌های در دسترس را ببینید.`
            : "وضعیت اتصال حساب‌های تلگرامی خود و آمار مجموعه‌های در دسترس را ببینید."
        }
      />

      {cards.length > 0 && <StatCardRow cards={cards} link={link} />}

      <PersonalAccountsSection
        result={personalAccounts}
        loading={accountsLoading}
        onRetry={onRetry}
        link={link}
      />
    </div>
  )
}

export default function Dashboard({ loaderData }: Route.ComponentProps) {
  const me = useMe()
  const navigation = useNavigation()
  const revalidator = useRevalidator()
  return (
    <DashboardContent
      loaderData={loaderData}
      me={me}
      accountsLoading={
        revalidator.state === "loading" ||
        (navigation.state === "loading" &&
          navigation.location?.pathname.endsWith("/dashboard"))
      }
      onRetry={() => revalidator.revalidate()}
    />
  )
}

// This card is intentionally driven by GET /me/telegram-accounts. Its states
// describe the current user's actual account records, never configured server
// health or another user's session.
export function PersonalAccountsSection({
  result,
  loading = false,
  onRetry,
  link,
}: {
  result: PersonalAccountsResult
  loading?: boolean
  onRetry?: () => void
  link?: (to: string) => React.ReactElement
}) {
  const linkFor = link ?? ((to: string) => <Link to={to} />)

  return (
    <section
      aria-labelledby="personal-accounts-heading"
      className="min-w-0 rounded-xl border bg-card p-4 sm:p-5"
    >
      <div className="mb-4 flex flex-wrap items-start justify-between gap-3">
        <div className="flex min-w-0 flex-col gap-1">
          <h2
            id="personal-accounts-heading"
            className="text-base font-semibold"
          >
            حساب‌های تلگرامی من
          </h2>
          <p className="text-sm text-muted-foreground">
            وضعیت واقعی حساب‌هایی که خودتان به پنل متصل کرده‌اید.
          </p>
        </div>
        {result.ok && result.rows.length > 0 && (
          <Button
            variant="outline"
            size="sm"
            className="ms-auto shrink-0"
            render={linkFor("/accounts")}
          >
            مدیریت حساب‌ها
          </Button>
        )}
      </div>

      {loading ? (
        <LoadingState label="در حال بارگذاری وضعیت حساب‌ها…" />
      ) : !result.ok ? (
        <ErrorState
          title="وضعیت حساب‌ها در دسترس نیست"
          message={result.message}
          onRetry={onRetry}
        />
      ) : result.rows.length === 0 ? (
        <EmptyState
          title="هنوز حساب تلگرامی ندارید"
          description="برای دیدن کانال‌ها، حساب تلگرامی خودتان را به پنل اضافه کنید."
          className="py-7"
          action={
            <Button render={linkFor("/accounts")}>
              افزودن حساب
              <ArrowLeftIcon data-icon="inline-end" />
            </Button>
          }
        />
      ) : (
        <ul className="grid min-w-0 grid-cols-1 gap-3 sm:grid-cols-2 lg:grid-cols-3">
          {result.rows.map((account) => {
            return (
              <li key={account.id} className="min-w-0">
                <Card className="h-full py-4">
                  <CardContent className="flex min-w-0 items-center gap-3 px-4">
                    <AccountAvatar account={account} className="size-9" />
                    <div className="flex min-w-0 flex-1 flex-col gap-0.5">
                      <span className="truncate text-sm font-medium">
                        {accountTitle(account)}
                      </span>
                      <span className="truncate text-xs text-muted-foreground">
                        {serverLabel(account.server_key, account.network_type)}
                      </span>
                    </div>
                    <AccountStatusBadge
                      account={account}
                      className="shrink-0"
                    />
                  </CardContent>
                </Card>
              </li>
            )
          })}
        </ul>
      )}
    </section>
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
