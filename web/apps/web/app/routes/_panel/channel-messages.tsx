import { Link, redirect } from "react-router"
import {
  ArrowRightIcon,
  AlertCircleIcon,
  ArrowDownToLineIcon,
  MessageSquareTextIcon,
  RefreshCwIcon,
} from "lucide-react"
import * as React from "react"

import { ChannelAvatar } from "@/components/channel-avatar"
import { MessageCard } from "@/components/message-card"
import { PublishDialog } from "@/components/publish-dialog"
import type { ChannelMessage } from "@/lib/types"
import { PageHeader } from "@/components/page-header"
import { faDigits } from "@/components/persian-digits"
import { EmptyState, ErrorState, LoadingState } from "@/components/states"
import { pageMeta, section } from "@/lib/page"
import {
  type MessagesStatus,
  useInfiniteMessages,
} from "@/hooks/use-infinite-messages"
import { ApiError, apiRequest } from "@/lib/api"
import { LOGIN_REDIRECT_TO, requireSession } from "@/lib/session"
import { strip, requestURL } from "@/path"
import type { ChannelDetail } from "@/lib/types"
import { Button } from "@workspace/ui/components/button"
import { Separator } from "@workspace/ui/components/separator"
import { Skeleton } from "@workspace/ui/components/skeleton"

import type { Route } from "./+types/channel-messages"

export const meta = () => pageMeta("پیام‌های کانال")

export const handle = section("کانال‌ها")

type ChannelLookup =
  // clientKey is the record's Telegram client key; empty when the answer did
  // not carry it, in which case reaction images fall back to their icon.
  | { state: "ok"; channel: ChannelDetail; clientKey: string }
  | { state: "missing" }
  | { state: "unavailable"; message: string; code: string }

// loadChannel reads the local record the route's id names. The record is the
// authority on the Telegram channel id and the client the messages come from;
// the route never accepts a Telegram id of its own. The service answers the
// canonical channel object, whose `id` is the channel's Telegram id — the page
// URL and the messages route keep the local id.
async function loadChannel(
  token: string,
  id: number | null,
  url: URL
): Promise<{ lookup: ChannelLookup; token: string }> {
  if (id === null) return { lookup: { state: "missing" }, token }
  try {
    // The record route wraps it: data.channel, not the record itself. The
    // record's client_key names the Telegram client the channel lives in, which
    // the public channel object does not carry; it is read here for the
    // reaction images.
    const { data } = await apiRequest<{ channel?: ChannelDetail; client_key?: string }>(
      "GET",
      `/local/channels/${id}`,
      { token }
    )
    if (!data?.channel || typeof data.channel.id !== "string") {
      return {
        lookup: {
          state: "unavailable",
          message: "پاسخ سرویس کانال را نداشت.",
          code: "invalid_response",
        },
        token,
      }
    }
    return {
      lookup: {
        state: "ok",
        channel: data.channel,
        clientKey: data.client_key ?? "",
      },
      token,
    }
  } catch (error) {
    if (error instanceof ApiError) {
      if (error.isAuthLost) throw redirect(authRedirect(url))
      if (error.status === 404) return { lookup: { state: "missing" }, token }
      if (error.isForbidden) return { lookup: { state: "missing" }, token }
      return {
        lookup: {
          state: "unavailable",
          message: error.message,
          code: error.code,
        },
        token,
      }
    }
    return {
      lookup: {
        state: "unavailable",
        message: "بارگذاری کانال ناموفق بود.",
        code: "network_error",
      },
      token,
    }
  }
}

function authRedirect(url: URL) {
  return `${LOGIN_REDIRECT_TO}?expired=1&next=${encodeURIComponent(strip(`${url.pathname}${url.search}`))}`
}

export async function clientLoader({ params, request }: Route.LoaderArgs) {
  const { token } = await requireSession(request)
  const url = requestURL(request)
  const raw = params.id ?? ""
  const id = /^[0-9]+$/.test(raw) ? Number(raw) : NaN
  const result = await loadChannel(
    token,
    Number.isSafeInteger(id) && id > 0 ? id : null,
    url
  )
  return result
}

export default function ChannelMessages({
  loaderData,
  params,
}: Route.ComponentProps) {
  const { lookup } = loaderData
  // The URL's id is the local record id: it addresses the record and its
  // messages. The channel object's id is Telegram's and is never used as one.
  const localId = Number(params.id)

  if (lookup.state === "missing") {
    return (
      <div className="flex flex-col gap-6">
        <PageHeader title="پیام‌های کانال" />
        <div className="rounded-xl border bg-card">
          <EmptyState
            icon={MessageSquareTextIcon}
            title="کانال پیدا نشد"
            description="کد کانال در نشانی معتبر نیست، یا این کانال در فهرست محلی نیست، یا مجوز خواندن آن را ندارید."
            action={
              <Button variant="outline" render={<Link to="/channels" />}>
                <ArrowRightIcon />
                بازگشت به کانال‌ها
              </Button>
            }
          />
        </div>
      </div>
    )
  }

  if (lookup.state === "unavailable") {
    return (
      <div className="flex flex-col gap-6">
        <PageHeader title="پیام‌های کانال" />
        <ErrorState
          title="وضعیت کانال در دسترس نیست"
          message={`${lookup.message} (کد: ${lookup.code})`}
          onRetry={() => window.location.reload()}
        />
      </div>
    )
  }

  // The page's id is the local record id; the channel object's id is Telegram's.
  return (
    <ChannelFeed
      channel={lookup.channel}
      channelId={localId}
      client={lookup.clientKey}
    />
  )
}

function ChannelFeed({
  channel,
  channelId,
  client,
}: {
  channel: ChannelDetail
  channelId: number
  // client is the record's Telegram client key: custom emoji reactions are
  // fetched through it, the same client the messages were read from.
  client: string
}) {
  const { data, status, fetchMore, retry } = useInfiniteMessages(channelId)
  const sentinel = React.useRef<HTMLDivElement>(null)
  const [publishing, setPublishing] = React.useState<ChannelMessage | null>(null)
  const [notice, setNotice] = React.useState<string | null>(null)
  const [atEnd, setAtEnd] = React.useState(false)
  // The newest message is shown first; this tells the reader where they are in
  // the history.
  const jumpToOldest = () => {
    document
      .getElementById("oldest-loaded")
      ?.scrollIntoView({ behavior: "smooth", block: "center" })
  }

  React.useEffect(() => {
    const node = sentinel.current
    if (!node) return
    if (typeof IntersectionObserver === "undefined") return
    const observer = new IntersectionObserver(
      (entries) => {
        if (entries.some((entry) => entry.isIntersecting)) setAtEnd(true)
        else setAtEnd(false)
      },
      { rootMargin: "300px 0px" }
    )
    observer.observe(node)
    return () => observer.disconnect()
  }, [])

  const loading = status.state === "loading"
  // Ask for the next page only when the end of the list is in reach, nothing
  // is in flight, the API says there is more and the series is intact.
  React.useEffect(() => {
    if (
      atEnd &&
      !loading &&
      data.hasMore &&
      !data.chainBroken &&
      data.page > 0
    ) {
      fetchMore()
    }
  }, [atEnd, loading, data.hasMore, data.chainBroken, data.page, fetchMore])

  const chronological = React.useMemo(
    () => [...data.messages].reverse(),
    [data.messages]
  )
  const dayOf = (value: string) => {
    const date = new Date(value)
    return Number.isNaN(date.getTime())
      ? value
      : date.toLocaleDateString("fa-IR")
  }

  return (
    <div className="flex flex-col gap-6">
      {/* The channel is named once, in the header: avatar, title, handle and
          description on one line, and how much is loaded beside the back
          action. */}
      <header className="flex flex-col gap-4">
        <div className="flex flex-col gap-3 sm:flex-row sm:items-start sm:justify-between">
          <div className="flex min-w-0 items-center gap-3">
            <ChannelAvatar
              id={channelId}
              title={channel.title}
              className="size-11"
            />
            <div className="flex min-w-0 flex-col gap-0.5">
              <h1 className="truncate text-xl font-semibold tracking-tight">
                {channel.title}
              </h1>
              <div className="flex min-w-0 flex-wrap items-center gap-x-2 gap-y-0.5 text-sm text-muted-foreground">
                <span className="truncate" dir="ltr">
                  {channel.username
                    ? `@${channel.username}`
                    : `شناسه کانال: ${faDigits(channel.id)}`}
                </span>
                {data.messages.length > 0 && (
                  <>
                    <span
                      className="text-muted-foreground/60"
                      aria-hidden="true"
                    >
                      ·
                    </span>
                    <span className="tabular-nums">
                      {faDigits(data.messages.length)} پیام بارگذاری‌شده
                    </span>
                  </>
                )}
              </div>
              {channel.description && (
                <p className="mt-1 line-clamp-2 max-w-2xl text-sm text-muted-foreground">
                  {channel.description}
                </p>
              )}
            </div>
          </div>
          <Button
            variant="outline"
            className="shrink-0"
            render={<Link to="/channels" />}
          >
            <ArrowRightIcon />
            کانال‌ها
          </Button>
        </div>
        <Separator />
      </header>

      {data.page === 0 && loading ? (
        <FeedSkeleton />
      ) : data.page === 0 && status.state === "error" ? (
        <ErrorState
          title="بارگذاری پیام‌ها ناموفق بود"
          message={status.message}
          onRetry={status.retryable ? retry : undefined}
        />
      ) : data.page > 0 && data.messages.length === 0 ? (
        <div className="rounded-xl border bg-card">
          <EmptyState
            icon={MessageSquareTextIcon}
            title="این کانال پیامی ندارد"
            description="تلگرام برای این کانال هیچ پیامی برنگرداند."
          />
        </div>
      ) : (
        <section aria-label="پیام‌های کانال" className="flex flex-col">
          {data.messages.length > 0 && (
            <div className="flex items-center justify-between gap-2 pb-1 text-xs text-muted-foreground">
              <span>تازه‌ترین پیام نخست نمایش داده می‌شود.</span>
              <Button variant="ghost" size="xs" onClick={jumpToOldest}>
                <ArrowDownToLineIcon />
                قدیمی‌ترین پیام بارگذاری‌شده
              </Button>
            </div>
          )}
          {chronological.map((message, index) => {
            const previous = chronological[index - 1]
            const oldest = index === 0
            return (
              <div key={message.id} id={oldest ? "oldest-loaded" : undefined}>
                <MessageCard
                  message={message}
                  client={client}
                  newDay={
                    !previous || dayOf(previous.date) !== dayOf(message.date)
                  }
                  onPublish={() => setPublishing(message)}
                />
              </div>
            )
          })}

          <div ref={sentinel} className="h-px" aria-hidden="true" />

          <PaginationFooter
            status={status}
            hasMore={data.hasMore}
            chainBroken={data.chainBroken}
            loaded={data.messages.length}
            onRetry={retry}
          />
        </section>
      )}

      {notice && (
        <div className="mt-3 rounded-lg bg-primary/10 px-3 py-2 text-sm" role="status">
          {notice}
        </div>
      )}

      {publishing && (
        <PublishDialog
          open
          onOpenChange={(next) => !next && setPublishing(null)}
          localChannelId={channelId}
          message={publishing}
          onSuccess={(summary) => {
            setNotice(summary)
            setPublishing(null)
          }}
        />
      )}
    </div>
  )
}

function PaginationFooter({
  status,
  hasMore,
  chainBroken,
  loaded,
  onRetry,
}: {
  status: MessagesStatus
  hasMore: boolean
  chainBroken: boolean
  loaded: number
  onRetry: () => void
}) {
  if (status.state === "loading" && loaded > 0) {
    return <LoadingState label="در حال بارگذاری پیام‌های قدیمی‌تر…" />
  }
  if (status.state === "error") {
    return (
      <div className="py-6">
        <ErrorState
          title="بارگذاری پیام‌های قدیمی‌تر ناموفق بود"
          message={status.message}
          onRetry={status.retryable ? onRetry : undefined}
        />
      </div>
    )
  }
  if (chainBroken) {
    return (
      <div className="my-4 flex flex-col items-center gap-2 rounded-xl border border-dashed bg-muted/40 px-4 py-6 text-center text-sm text-muted-foreground">
        <AlertCircleIcon className="size-5" aria-hidden="true" />
        <p>
          سرور پیام‌های قدیمی‌تر را از ابتدا برگرداند (مثلاً پس از راه‌اندازی
          دوباره سرور). ادامهٔ فهرست قابل اعتماد نیست.
        </p>
        <Button variant="outline" size="sm" onClick={onRetry}>
          <RefreshCwIcon />
          بارگذاری از ابتدا
        </Button>
      </div>
    )
  }
  if (!hasMore) {
    return (
      <p className="py-6 text-center text-xs text-muted-foreground">
        {loaded > 0 ? "به ابتدای تاریخ کانال رسیدید." : ""}
      </p>
    )
  }
  return null
}

function FeedSkeleton() {
  return (
    <div
      className="flex flex-col gap-3"
      aria-busy="true"
      aria-label="در حال بارگذاری پیام‌ها…"
    >
      {Array.from({ length: 5 }).map((_, index) => (
        <div
          key={index}
          className="flex flex-col gap-3 rounded-xl border bg-card p-4"
        >
          <Skeleton className="h-3 w-40" />
          <Skeleton className="h-4 w-full" />
          <Skeleton className="h-4 w-4/5" />
          {index % 2 === 0 && <Skeleton className="h-32 w-full max-w-xl" />}
        </div>
      ))}
    </div>
  )
}
