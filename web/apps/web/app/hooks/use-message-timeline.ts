import * as React from "react"

import {
  applyBatch,
  beginLoad,
  failLoad,
  MESSAGE_PAGE_SIZE,
  nextRequest,
  resetTimeline,
  retry as retryDirection,
  type Direction,
  type TimelineState,
} from "@/lib/message-timeline"
import { ApiError, apiRequest, errorMessage, type PageMeta } from "@/lib/api"
import { getToken } from "@/lib/session"
import type { ChannelMessage } from "@/lib/types"

// One channel's message history, fetched a page at a time in whichever
// direction the reader is going.
//
// The data rules live in `lib/message-timeline`; this is the part that talks to
// the API and makes sure only one request is in flight per channel, that an
// answer belonging to a channel the reader has already left is thrown away,
// and that the same page is never asked for twice.

export { MESSAGE_PAGE_SIZE }

export function useMessageTimeline(channelId: number | null) {
  const [state, setState] = React.useState<TimelineState>(() => resetTimeline(channelId))
  const stateRef = React.useRef(state)
  stateRef.current = state

  // Every request carries the generation it was made under. A channel change
  // bumps it, so an answer from the previous channel can never be spliced into
  // the next one — switching channels quickly must not mix two histories.
  const generation = React.useRef(0)
  const inFlight = React.useRef<{
    direction: Direction
    page: number
    controller: AbortController
  } | null>(null)

  React.useEffect(() => {
    generation.current++
    inFlight.current?.controller.abort()
    inFlight.current = null
    // The ref is written here as well as through the render, so a request made
    // in this same commit sees the new channel and not the one just left.
    const fresh = resetTimeline(channelId)
    stateRef.current = fresh
    setState(fresh)
  }, [channelId])

  const request = React.useCallback(
    (want: "older" | "newer") => {
      if (channelId === null) return
      // One at a time: the API's cursor moves page by page, and two answers
      // arriving out of order would be spliced wrongly.
      if (inFlight.current) return
      const current = stateRef.current
      if (current.channelId !== channelId) return
      const plan = nextRequest(current, want)
      if (!plan) return

      // The first page of a channel is not "older" or "newer": it opens the
      // window at the newest edge and is labelled as such.
      const direction: Direction = current.page === 0 ? "initial" : want
      const controller = new AbortController()
      const token = getToken()
      const gen = generation.current
      inFlight.current = { direction, page: plan.page, controller }
      setState((s) => beginLoad(s, direction))

      if (!token) {
        inFlight.current = null
        setState((s) => failLoad(s, direction, "نشست شما معتبر نیست."))
        return
      }

      void apiRequest<{ messages?: ChannelMessage[] }>(
        "GET",
        `/local/channels/${channelId}/messages`,
        {
          token,
          query: { page: plan.page, per_page: MESSAGE_PAGE_SIZE },
          signal: controller.signal,
        }
      )
        .then(({ data, meta }) => {
          if (gen !== generation.current) return
          const messages = data?.messages ?? []
          inFlight.current = null
          setState((s) =>
            s.channelId === channelId
              ? applyBatch(
                  s,
                  { messages, page: plan.page, hasMore: readHasMore(meta, plan.page) },
                  direction
                )
              : s
          )
        })
        .catch((error: unknown) => {
          if (gen !== generation.current) return
          // The lock goes whatever the outcome: an aborted request must not
          // leave the timeline unable to ask for anything again.
          inFlight.current = null
          if (error instanceof ApiError && error.code === "aborted") return
          setState((s) =>
            s.channelId === channelId
              ? failLoad(s, direction, explainMessagesError(error))
              : s
          )
        })
    },
    [channelId]
  )

  // The first page is asked for as soon as a channel is set. It must not depend
  // on a sentinel happening to be on screen: opening a channel has to fetch.
  React.useEffect(() => {
    if (channelId === null) return
    request("older")
  }, [channelId, request])

  // A walk back up to the newest edge continues on its own until the hole it is
  // filling closes. Nothing else fetches upwards on its own: a reader scrolling
  // down through what is already loaded is not a reason to ask the API for
  // anything.
  React.useEffect(() => {
    if (state.walkingNewer && state.gap) request("newer")
  }, [state, request])

  return {
    state,
    requestOlder: React.useCallback(() => request("older"), [request]),
    requestNewer: React.useCallback(() => request("newer"), [request]),
    retry: React.useCallback(
      (direction: "older" | "newer") => setState((s) => retryDirection(s, direction)),
      []
    ),
  }
}

// readHasMore trusts the API's has_more and next_page; an absent next_page with
// a full page is still "walk on", because the API omits next_page only when
// nothing follows. A short page is the real end of history.
function readHasMore(meta: PageMeta, page: number): boolean {
  if (typeof meta.has_more === "boolean") return meta.has_more
  return (meta.next_page ?? 0) > page
}

// explainMessagesError maps the API's codes onto the one sentence a reader can
// act on.
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
