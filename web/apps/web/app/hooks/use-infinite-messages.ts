import * as React from "react"

import { ApiError, apiRequest, errorMessage, type PageMeta } from "@/lib/api"
import { getToken } from "@/lib/session"
import type { ChannelMessage } from "@/lib/types"

// The messages API pages by page number below the newest message and, because
// Telegram has no page index, it only answers page N correctly while the same
// worker process has already walked pages 1..N-1. A new channel (or a worker
// restart) therefore restarts from page 1 and the series must be walked again
// from the top: `reset()`, never a jump to a later page.

export const MESSAGE_PAGE_SIZE = 20

export type MessagesStatus =
  | { state: "idle" }
  | { state: "loading" }
  | { state: "ready" }
  | { state: "error"; message: string; retryable: boolean }

interface MessagesData {
  // API order: newest first. Kept that way so `next_page` always means "the
  // page after the last one loaded".
  messages: ChannelMessage[]
  page: number
  hasMore: boolean
  // chainBroken: the API answered a page other than the one asked for, so its
  // content is not the continuation of what is loaded. The page never splices
  // such an answer in.
  chainBroken: boolean
}

// sameChannel: the API answers a page with the content it thinks that page
// is. The page loaded so far ends at `lastId`; a continuation must start below
// it. When it does not, the worker restarted its walk (or the history moved),
// and the answer is not what follows.
function continuesBelow(
  incoming: ChannelMessage[],
  lastId: number | undefined
): boolean {
  if (incoming.length === 0 || lastId === undefined) return true
  return incoming.every((m) => m.id < lastId)
}

/**
 * useInfiniteMessages walks one channel's history from the newest page
 * downwards.
 *
 * Every fetch is guarded by a generation token: a reset (channel id change, or
 * the caller's reset) bumps it, so an answer that belongs to the previous
 * channel can never be spliced into the current list.
 */
export function useInfiniteMessages(channelId: number | null) {
  const [data, setData] = React.useState<MessagesData>({
    messages: [],
    page: 0,
    hasMore: false,
    chainBroken: false,
  })
  const [status, setStatus] = React.useState<MessagesStatus>({ state: "idle" })

  const generation = React.useRef(0)
  const inFlight = React.useRef<{
    page: number
    controller: AbortController
  } | null>(null)
  // The newest list, for callers that act outside the render cycle.
  const dataRef = React.useRef(data)
  dataRef.current = data

  // fresh: nothing to clear (first mount, or a channel change already cleared
  // the list), so the caller can start page 1 in the same commit and page 1
  // is never answered before page 2's request has retired.
  const reset = React.useCallback((fresh = false) => {
    generation.current++
    inFlight.current?.controller.abort()
    inFlight.current = null
    if (!fresh) {
      setData({ messages: [], page: 0, hasMore: false, chainBroken: false })
      setStatus({ state: "idle" })
    }
  }, [])

  const load = React.useCallback(
    async (page: number) => {
      if (channelId === null) return
      // One request at a time: the API's cursors move page by page, and two
      // answers arriving out of order would be spliced wrongly.
      if (inFlight.current) return
      const controller = new AbortController()
      const token = getToken()
      if (!token) {
        setStatus({
          state: "error",
          message: "نشست شما معتبر نیست.",
          retryable: false,
        })
        return
      }
      const gen = generation.current
      inFlight.current = { page, controller }
      setStatus({ state: "loading" })
      try {
        const { data: body, meta } = await apiRequest<{
          messages?: ChannelMessage[]
        }>("GET", `/local/channels/${channelId}/messages`, {
          token,
          query: { page, per_page: MESSAGE_PAGE_SIZE },
          signal: controller.signal,
        })
        if (gen !== generation.current) return
        const incoming = (body?.messages ?? []).filter(
          (m) => Number.isInteger(m?.id) && m.id > 0
        )
        setData((current) => {
          let chainBroken = current.chainBroken
          const lastId = current.messages.at(-1)?.id
          if (page > 1 && !continuesBelow(incoming, lastId)) {
            chainBroken = true
          }
          // Stable ids: the same message id never appears twice, whichever
          // page it came from.
          const seen = new Set(current.messages.map((m) => m.id))
          const fresh = incoming.filter((m) => !seen.has(m.id))
          return {
            messages: page === 1 ? fresh : [...current.messages, ...fresh],
            page,
            hasMore: readHasMore(meta, page),
            chainBroken,
          }
        })
        setStatus({ state: "ready" })
      } catch (error) {
        if (gen !== generation.current) return
        if (error instanceof ApiError && error.code === "aborted") return
        const retryable = !(error instanceof ApiError && error.isForbidden)
        if (error instanceof ApiError && error.isAuthLost) {
          setStatus({
            state: "error",
            message: "نشست شما منقضی شده است.",
            retryable: false,
          })
          return
        }
        if (error instanceof ApiError && error.code === "not_found") {
          // The channel left the local list: what is loaded stays readable,
          // but nothing more can be fetched without a reload.
          setStatus({
            state: "error",
            message: explainMessagesError(error),
            retryable: false,
          })
          return
        }
        setStatus({
          state: "error",
          message: explainMessagesError(error),
          retryable,
        })
      } finally {
        if (gen === generation.current) {
          inFlight.current = null
        }
      }
    },
    [channelId]
  )

  // A channel change (or the first mount) starts the series over.
  React.useEffect(() => {
    reset(true)
    if (channelId !== null) void load(1)
    // load is keyed on channelId too; this runs once per channel.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [channelId])

  // fetchMore loads the next page; the caller only asks when the sentinel is
  // near the viewport and nothing else is in flight.
  const fetchMore = React.useCallback(() => {
    const current = dataRef.current
    if (status.state === "loading") return
    if (current.page === 0) {
      void load(1)
      return
    }
    if (!current.hasMore || current.chainBroken) return
    void load(current.page + 1)
  }, [load, status.state])

  // restart walks the series from page 1 again, for a continuation the API
  // answered with something else.
  const restart = React.useCallback(() => {
    reset()
    void load(1)
  }, [load, reset])

  // retry re-asks the page that failed.
  const retry = React.useCallback(() => {
    const current = dataRef.current
    if (current.chainBroken) {
      restart()
      return
    }
    const page = current.page === 0 ? 1 : current.page + 1
    if (current.page === 0 || current.hasMore) void load(page)
  }, [load, restart])

  return {
    data,
    status,
    fetchMore,
    retry,
  }
}

// readHasMore trusts the API's has_more and next_page; an absent next_page
// with a full page is still "walk on", because the API omits next_page only
// when nothing follows.
function readHasMore(meta: PageMeta, page: number): boolean {
  if (typeof meta.has_more === "boolean") return meta.has_more
  return (meta.next_page ?? 0) > page
}

// explainMessagesError maps the API's codes onto the one sentence a reader
// can act on.
export function explainMessagesError(error: unknown): string {
  if (error instanceof ApiError) {
    switch (error.code) {
      case "not_found":
        return "این کانال در فهرست محلی نیست."
      case "forbidden":
        return "شما مجوز خواندن پیام‌های این کانال را ندارید."
      case "needs_auth":
        return "حساب سرور این کانال وارد نشده است؛ ابتدا سرور را وارد کنید."
      case "not_connected":
      case "worker_unavailable":
      case "worker_timeout":
        return `${error.message} چند لحظه بعد دوباره تلاش کنید.`
      case "unknown_client":
        return "سرور این کانال دیگر پیکربندی نشده است."
      case "channel_not_found":
        return "تلگرام این کانال را برای این حساب برنگرداند."
      case "rate_limited":
        return "تلگرام تعداد درخواست‌ها را محدود کرده است؛ کمی بعد دوباره تلاش کنید."
      default:
        return error.message
    }
  }
  return errorMessage(error)
}
