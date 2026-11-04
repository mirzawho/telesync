import {
  AlertCircleIcon,
  ArrowDownIcon,
  ArrowRightIcon,
  RefreshCwIcon,
} from "lucide-react"
import * as React from "react"

import { ChannelAvatar } from "@/components/channel-avatar"
import { ChannelMessageItem } from "@/components/channel-explorer/message-item"
import { faDigits } from "@/components/persian-digits"
import { PublishDialog } from "@/components/publish-dialog"
import { EmptyState, ErrorState } from "@/components/states"
import { useMessageTimeline } from "@/hooks/use-message-timeline"
import {
  captureAnchor,
  restoreScrollTop,
  type ItemBox,
  type ScrollAnchor,
} from "@/lib/scroll-anchor"
import { followNewest, showJumpToLatest } from "@/lib/message-timeline"
import type { ChannelDetail, ChannelMessage } from "@/lib/types"
import { Button } from "@workspace/ui/components/button"
import { Skeleton } from "@workspace/ui/components/skeleton"
import { cn } from "@workspace/ui/lib/utils"

// The left pane: one channel's history as a timeline.
//
// The history is chronological, oldest at the top and newest at the bottom,
// because that is how a reader follows a channel. Opening a channel puts the
// newest message on screen; scrolling up grows the history above; scrolling
// down only ever reveals what is already in hand, unless something above is
// known to be missing.
//
// The two rules that make it feel solid: a reader's place is kept by naming the
// message they are on rather than by remembering a pixel height, and nothing
// fetches without either the reader moving or a gap being known to exist.

// How close to the bottom counts as "reading the newest message". Small enough
// to feel immediate, large enough that a rounded scrollbar does not flicker.
const NEAR_BOTTOM = 80

// How close to the top triggers the next page of history on its own.
const NEAR_TOP = 400

export function ChannelMessagesPane({
  channel,
  channelId,
  client,
  onBack,
}: {
  channel: ChannelDetail
  // channelId is the local managed-channel id. It is what the messages route
  // takes and what the URL holds; the channel object's own id is Telegram's.
  channelId: number
  // client is the record's Telegram client key: a custom-emoji reaction is
  // fetched through it, the same client the messages were read from.
  client: string
  onBack?: () => void
}) {
  const { state, requestOlder, requestNewer, retry } = useMessageTimeline(channelId)
  const scroller = React.useRef<HTMLDivElement>(null)
  const content = React.useRef<HTMLDivElement>(null)
  const topSentinel = React.useRef<HTMLDivElement>(null)
  const bottomSentinel = React.useRef<HTMLDivElement>(null)

  // The reader's place across a prepend, and whether they are following the
  // newest message. Both are refs so a scroll never costs a render.
  const anchor = React.useRef<ScrollAnchor | null>(null)
  const following = React.useRef(true)
  const [nearBottom, setNearBottom] = React.useState(true)
  const [publishing, setPublishing] = React.useState<ChannelMessage | null>(null)

  // "Loading" until the first answer is in: the pane must not claim the channel
  // is empty in the moment before the first page has been asked for.
  const asked = state.page > 0 || state.loading !== null || state.olderError !== null
  const loadingInitial = state.messages.length === 0 && !asked
  const loadingOlder = state.loading === "older"
  const loadingNewer = state.loading === "newer"

  // --- reading position ----------------------------------------------------

  const boxes = React.useCallback((): ItemBox[] => {
    const wrap = content.current
    const box = scroller.current
    if (!wrap || !box) return []
    const wrapTop = wrap.getBoundingClientRect().top
    return [...wrap.querySelectorAll<HTMLElement>("[data-message-id]")].map((el) => ({
      id: Number(el.dataset.messageId),
      top: el.getBoundingClientRect().top - wrapTop,
      height: el.offsetHeight,
    }))
  }, [])

  const capture = React.useCallback(() => {
    const box = scroller.current
    if (!box) return
    anchor.current = captureAnchor(
      { scrollTop: box.scrollTop, height: box.clientHeight },
      boxes()
    )
  }, [boxes])

  const restore = React.useCallback(() => {
    const box = scroller.current
    const saved = anchor.current
    if (!box || !saved) return
    const wanted = restoreScrollTop(
      { scrollTop: box.scrollTop, height: box.clientHeight },
      boxes(),
      saved,
      box.scrollHeight
    )
    if (wanted !== null) box.scrollTop = wanted
  }, [boxes])

  // Keep the reader where they were whenever history arrives above them.
  React.useLayoutEffect(() => {
    if (state.loading !== null) return
    restore()
  }, [state.messages, state.loading, restore])

  // Media answers late and changes heights above the reader. While an anchor is
  // held, content resizes put it back; the observer is only attached then, so
  // nothing measures the DOM while the reader is simply scrolling.
  React.useEffect(() => {
    const wrap = content.current
    if (!wrap || typeof ResizeObserver === "undefined") return
    const observer = new ResizeObserver(() => {
      if (anchor.current) restore()
    })
    observer.observe(wrap)
    return () => observer.disconnect()
  }, [restore])

  // --- following the newest message ---------------------------------------

  // The first page puts the reader at the newest message, where they would open
  // the channel in Telegram itself. Once per channel: media arriving later must
  // not throw them back to the bottom while they read.
  const settled = React.useRef<number | null>(null)
  React.useLayoutEffect(() => {
    const box = scroller.current
    if (!box || state.messages.length === 0) return
    if (settled.current === channelId) return
    settled.current = channelId
    following.current = true
    box.scrollTop = box.scrollHeight
    setNearBottom(true)
  }, [channelId, state.messages.length])

  // Newer messages follow the reader only when they were already at the
  // bottom. Appending never disturbs someone reading back through history.
  const newestId = state.messages.at(-1)?.id
  React.useLayoutEffect(() => {
    if (newestId === undefined) return
    if (!following.current || !followNewest(state, nearBottom)) return
    const box = scroller.current
    if (box) box.scrollTop = box.scrollHeight
    // The message identity is what changed; re-running on every state tweak
    // would move the reader for reasons that are not new messages.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [newestId])

  // Scroll reporting is coalesced: the state only changes when the reader
  // crosses the threshold, not once per pixel.
  React.useEffect(() => {
    const box = scroller.current
    if (!box) return
    let frame = 0
    const read = () => {
      frame = 0
      const atBottom =
        box.scrollHeight - box.scrollTop - box.clientHeight <= NEAR_BOTTOM
      following.current = atBottom
      // A reader who scrolls back into history stops being followed; being at
      // the bottom starts it again.
      anchor.current = null
      setNearBottom((was) => (was === atBottom ? was : atBottom))
    }
    const onScroll = () => {
      if (frame) return
      frame = requestAnimationFrame(read)
    }
    box.addEventListener("scroll", onScroll, { passive: true })
    return () => {
      if (frame) cancelAnimationFrame(frame)
      box.removeEventListener("scroll", onScroll)
    }
  }, [])

  // --- paging --------------------------------------------------------------

  // Reaching the top of the history asks for the page below the oldest message
  // in hand. Reaching the bottom asks only when something above is missing:
  // scrolling through what is loaded is not a reason to talk to the API.
  React.useEffect(() => {
    const top = topSentinel.current
    if (!top || typeof IntersectionObserver === "undefined") return
    const observer = new IntersectionObserver(
      (entries) => {
        if (!entries.some((entry) => entry.isIntersecting)) return
        capture()
        requestOlder()
      },
      { root: scroller.current, rootMargin: `${NEAR_TOP}px 0px` }
    )
    observer.observe(top)
    return () => observer.disconnect()
  }, [capture, requestOlder])

  React.useEffect(() => {
    const bottom = bottomSentinel.current
    if (!bottom || typeof IntersectionObserver === "undefined") return
    const observer = new IntersectionObserver(
      (entries) => {
        if (!entries.some((entry) => entry.isIntersecting)) return
        requestNewer()
      },
      { root: scroller.current, rootMargin: `${NEAR_BOTTOM}px 0px` }
    )
    observer.observe(bottom)
    return () => observer.disconnect()
  }, [requestNewer])

  const jumpToLatest = () => {
    requestNewer()
    const box = scroller.current
    if (box) box.scrollTo({ top: box.scrollHeight, behavior: "smooth" })
    following.current = true
    setNearBottom(true)
    anchor.current = null
  }

  const showJump = showJumpToLatest(state, nearBottom)

  return (
    <section
      data-slot="channel-messages-pane"
      aria-label={`پیام‌های ${channel.title}`}
      className="flex min-h-0 min-w-0 flex-1 flex-col"
    >
      <header
        data-slot="channel-messages-header"
        className="flex shrink-0 items-center gap-3 border-b px-3 py-2.5"
      >
        {onBack && (
          <Button
            variant="ghost"
            size="icon-sm"
            onClick={onBack}
            aria-label="بازگشت به فهرست کانال‌ها"
            className="lg:hidden"
          >
            <ArrowRightIcon />
          </Button>
        )}
        <ChannelAvatar id={channelId} title={channel.title} className="size-9 shrink-0" />
        <div className="flex min-w-0 flex-col">
          <h1 className="truncate text-sm font-semibold">{channel.title}</h1>
          <span className="text-muted-foreground truncate text-xs">
            {channel.username ? `@${channel.username}` : `#${faDigits(channelId)}`}
          </span>
        </div>
      </header>

      <div className="relative min-h-0 flex-1">
        <div ref={scroller} className="h-full overflow-y-auto overscroll-contain">
          <div
            ref={content}
            className="mx-auto flex w-full max-w-3xl flex-col gap-3 px-3 py-4"
          >
            <div ref={topSentinel} aria-hidden="true" className="h-px" />

            {loadingInitial ? (
              <TimelineSkeleton />
            ) : state.messages.length === 0 && state.olderError ? (
              <ErrorState
                title="بارگذاری پیام‌ها ناموفق بود"
                message={state.olderError}
                onRetry={() => retry("older")}
              />
            ) : state.messages.length === 0 ? (
              <EmptyState
                title="این کانال پیامی ندارد"
                description="تلگرام برای این کانال هیچ پیامی برنگرداند."
              />
            ) : (
              <>
                <HistoryTop
                  loading={loadingOlder}
                  hasOlder={state.hasOlder}
                  error={state.olderError}
                  stalled={state.stalled}
                  gap={state.gap}
                  onRetry={() => retry("older")}
                  onAsk={requestOlder}
                  onRecover={requestNewer}
                />

                <ol className="flex flex-col gap-3">
                  {state.messages.map((message, index) => {
                    const previous = state.messages[index - 1]
                    const newDay =
                      !previous || dayOf(previous.date) !== dayOf(message.date)
                    return (
                      <React.Fragment key={message.id}>
                        {newDay && <DaySeparator date={message.date} />}
                        <li data-message-id={message.id}>
                          <ChannelMessageItem
                            message={message}
                            client={client}
                            onPublish={() => setPublishing(message)}
                          />
                        </li>
                      </React.Fragment>
                    )
                  })}
                </ol>

                <HistoryBottom loading={loadingNewer} gap={state.gap} />
                <div ref={bottomSentinel} aria-hidden="true" className="h-px" />
              </>
            )}
          </div>
        </div>

        {showJump && (
          <Button
            variant="secondary"
            size="sm"
            onClick={jumpToLatest}
            className="absolute bottom-4 left-1/2 z-10 -translate-x-1/2 gap-1.5 rounded-full px-3 shadow-none ring-2 ring-background"
          >
            <ArrowDownIcon />
            آخرین پیام‌ها
          </Button>
        )}
      </div>

      {publishing && (
        <PublishDialog
          open
          onOpenChange={(next) => !next && setPublishing(null)}
          localChannelId={channelId}
          message={publishing}
          onSuccess={() => setPublishing(null)}
        />
      )}
    </section>
  )
}

// HistoryTop is what sits above the oldest message in hand: the control that
// asks for more, and the honest answer when there is no more, when the walk
// stalled, or when the history below cannot be trusted.
function HistoryTop({
  loading,
  hasOlder,
  error,
  stalled,
  gap,
  onRetry,
  onAsk,
  onRecover,
}: {
  loading: boolean
  hasOlder: boolean
  error: string | null
  stalled: boolean
  gap: boolean
  onRetry: () => void
  onAsk: () => void
  onRecover: () => void
}) {
  if (loading) {
    return (
      <p role="status" className="py-1 text-center text-xs text-muted-foreground">
        در حال بارگذاری پیام‌های قدیمی‌تر…
      </p>
    )
  }
  if (error) {
    return (
      <div
        role="alert"
        className="flex flex-col items-center gap-2 rounded-xl border border-dashed bg-muted/40 px-4 py-3 text-center"
      >
        <p className="text-xs text-destructive">{error}</p>
        <Button variant="outline" size="sm" onClick={onRetry}>
          <RefreshCwIcon />
          تلاش دوباره
        </Button>
      </div>
    )
  }
  if (gap) {
    // A hole in the history: the timeline says so rather than drawing a
    // continuous conversation that is not continuous.
    return (
      <div className="flex flex-col items-center gap-2 rounded-xl border border-dashed bg-muted/40 px-4 py-3 text-center">
        <AlertCircleIcon className="size-5 text-muted-foreground" aria-hidden="true" />
        <p className="max-w-md text-xs leading-6 text-muted-foreground">
          بخشی از تاریخچهٔ این کانال بارگذاری نشده است؛ پیام‌های این فهرست به‌هم
          پیوسته نیستند.
        </p>
        <Button variant="outline" size="sm" onClick={onRecover}>
          بارگذاری از ابتدا
        </Button>
      </div>
    )
  }
  if (stalled) {
    return (
      <div className="flex flex-col items-center gap-2 rounded-xl border border-dashed bg-muted/40 px-4 py-3 text-center">
        <p className="text-xs leading-6 text-muted-foreground">
          بارگذاری پیام‌های قدیمی‌تر ادامه پیدا نکرد.
        </p>
        <Button variant="outline" size="sm" onClick={onRetry}>
          <RefreshCwIcon />
          تلاش دوباره
        </Button>
      </div>
    )
  }
  if (!hasOlder) {
    return (
      <p className="py-1 text-center text-xs text-muted-foreground/80">
        به ابتدای تاریخچهٔ کانال رسیدید.
      </p>
    )
  }
  // The sentinel above is what asks; this is only a way to ask again by hand.
  return (
    <div className="flex justify-center py-1">
      <Button variant="ghost" size="sm" onClick={onAsk}>
        بارگذاری پیام‌های قدیمی‌تر
      </Button>
    </div>
  )
}

// HistoryBottom is the small indicator for a newer range being fetched.
function HistoryBottom({ loading, gap }: { loading: boolean; gap: boolean }) {
  if (loading) {
    return (
      <p role="status" className="py-1 text-center text-xs text-muted-foreground">
        در حال بارگذاری پیام‌های تازه‌تر…
      </p>
    )
  }
  if (gap) {
    return (
      <p className="py-1 text-center text-xs text-muted-foreground">
        پیام‌های تازه‌تری هست که هنوز بارگذاری نشده‌اند.
      </p>
    )
  }
  return null
}

// DaySeparator is the one place a date is written out: the clock time in each
// message's footer is enough on every other row.
function DaySeparator({ date }: { date: string }) {
  return (
    <li className="flex items-center gap-3 py-1" aria-hidden="true">
      <span className="h-px flex-1 bg-border" />
      <time dateTime={date} className="text-xs text-muted-foreground">
        {dayOf(date)}
      </time>
      <span className="h-px flex-1 bg-border" />
    </li>
  )
}

function dayOf(value: string): string {
  const date = new Date(value)
  return Number.isNaN(date.getTime())
    ? value
    : date.toLocaleDateString("fa-IR-u-ca-persian", { dateStyle: "medium" })
}

function TimelineSkeleton() {
  return (
    <div
      className="flex flex-col gap-3"
      aria-busy="true"
      aria-label="در حال بارگذاری پیام‌ها…"
    >
      {Array.from({ length: 5 }).map((_, index) => (
        <div
          key={index}
          className={cn(
            "flex flex-col gap-2 rounded-2xl bg-muted px-3.5 py-2.5",
            index % 2 === 0 ? "w-2/3" : "w-1/2"
          )}
        >
          <Skeleton className="h-4 w-4/5" />
          <Skeleton className="h-4 w-2/3" />
          <Skeleton className="mt-1 h-3 w-24" />
        </div>
      ))}
    </div>
  )
}
