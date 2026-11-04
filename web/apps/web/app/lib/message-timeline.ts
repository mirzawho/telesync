import type { ChannelMessage } from "@/lib/types"

// The state of one channel's message timeline: what is loaded, what is known
// to be missing, and what may be asked for next.
//
// Everything here is a pure decision, so the rules that make a channel
// readable — no duplicate messages, no request loops, no pretending a gap is
// not there — can be checked without a browser.

// The page size the messages API is asked for. A page is not guaranteed to come
// back full: the backend reports `has_more` from its own answer, and that is
// what this uses to decide whether to keep walking.
export const MESSAGE_PAGE_SIZE = 20

// Which way a load is going. `initial` opens the window at the newest edge,
// `older` walks down through history, `newer` walks back up to it.
export type Direction = "initial" | "older" | "newer"

export interface TimelineBatch {
  // As the API hands it over: newest first. Order is corrected on merge.
  messages: readonly ChannelMessage[]
  // The page number this batch answered. The API pages below a per-channel
  // message-id cursor, so the number only matters as "the next one down".
  page: number
  hasMore: boolean
}

export interface TimelineState {
  channelId: number | null
  // Oldest first, newest last — the order the reader sees.
  messages: ChannelMessage[]
  // The last page fetched in the current walk down from the newest edge.
  page: number
  // More history exists below the oldest message in hand.
  hasOlder: boolean
  // The newest edge is in hand: nothing above is waiting to be loaded.
  isNewest: boolean
  // A hole is known to exist inside the loaded range. The timeline says so
  // rather than drawing a continuous history that is not continuous.
  gap: boolean
  // The last batch down brought nothing new, so asking again would loop.
  stalled: boolean
  // A walk back up to the newest edge is in progress, started by the reader.
  // It continues on its own until the hole closes; nothing else fetches
  // upwards on its own.
  walkingNewer: boolean
  loading: Direction | null
  olderError: string | null
  newerError: string | null
}

export interface TimelineRequest {
  direction: Direction
  page: number
}

export function emptyTimeline(channelId: number | null = null): TimelineState {
  return {
    channelId,
    messages: [],
    page: 0,
    hasOlder: false,
    isNewest: true,
    gap: false,
    stalled: false,
    walkingNewer: false,
    loading: null,
    olderError: null,
    newerError: null,
  }
}

// resetTimeline starts a channel over. It is what makes one channel's messages
// impossible to mistake for another's.
export function resetTimeline(channelId: number | null): TimelineState {
  return emptyTimeline(channelId)
}

export function beginLoad(
  state: TimelineState,
  direction: Direction
): TimelineState {
  return {
    ...state,
    loading: direction,
    olderError: direction === "older" ? null : state.olderError,
    newerError: direction === "newer" ? null : state.newerError,
  }
}

export function failLoad(
  state: TimelineState,
  direction: Direction,
  message: string
): TimelineState {
  return {
    ...state,
    loading: null,
    olderError: direction === "older" ? message : state.olderError,
    newerError: direction === "newer" ? message : state.newerError,
  }
}

// retry clears one direction's failure so the reader can ask again. Nothing
// retries on its own.
export function retry(state: TimelineState, direction: Direction): TimelineState {
  return {
    ...state,
    loading: null,
    stalled: false,
    olderError: direction === "older" ? null : state.olderError,
    newerError: direction === "newer" ? null : state.newerError,
  }
}

const ascending = (a: ChannelMessage, b: ChannelMessage) => a.id - b.id

function mergeMessages(
  current: readonly ChannelMessage[],
  incoming: readonly ChannelMessage[]
): { messages: ChannelMessage[]; added: number; lowest: number | null } {
  const seen = new Set(current.map((m) => m.id))
  const fresh = incoming.filter((m) => Number.isInteger(m?.id) && m.id > 0 && !seen.has(m.id))
  let lowest: number | null = null
  for (const m of fresh) {
    if (lowest === null || m.id < lowest) lowest = m.id
  }
  if (fresh.length === 0) {
    return { messages: [...current], added: 0, lowest: null }
  }
  return {
    messages: [...current, ...fresh].sort(ascending),
    added: fresh.length,
    lowest,
  }
}

// applyBatch folds one API answer into the timeline.
//
// The three directions answer three different questions. `initial` opens at the
// newest edge. `older` extends downwards and must actually extend it — a batch
// that repeats what is loaded stalls the walk instead of looping it, and a
// batch from the very top means the worker's cursor restarted, which is a hole
// in the history rather than its end. `newer` walks back up from the newest
// edge and only closes the hole once a batch reaches into what was already
// loaded.
export function applyBatch(
  state: TimelineState,
  batch: TimelineBatch,
  direction: Direction
): TimelineState {
  const beforeNewest = state.messages.at(-1)?.id
  let batchLow: number | null = null
  for (const m of batch.messages) {
    if (Number.isInteger(m?.id) && m.id > 0 && (batchLow === null || m.id < batchLow)) {
      batchLow = m.id
    }
  }

  // A batch from the very top is not history below what is loaded: the worker
  // walk lost its place. The batch is dropped rather than spliced in over a
  // hole, and the timeline says a hole exists.
  const aboveWindow =
    batchLow !== null && beforeNewest !== undefined && batchLow > beforeNewest

  const { messages, added } = mergeMessages(state.messages, batch.messages)

  const base: TimelineState = {
    ...state,
    messages,
    page: batch.page,
    loading: null,
    olderError: direction === "older" ? null : state.olderError,
    newerError: direction === "newer" ? null : state.newerError,
  }

  if (direction === "initial") {
    return {
      ...base,
      isNewest: true,
      hasOlder: batch.hasMore,
      gap: false,
      stalled: false,
      walkingNewer: false,
    }
  }

  if (direction === "older") {
    if (aboveWindow) {
      // The walk lost its place. What is loaded stays readable, but the top is
      // no longer known to be the newest edge and there is a hole to admit to.
      return {
        ...state,
        page: batch.page,
        loading: null,
        gap: true,
        isNewest: false,
        hasOlder: false,
        stalled: true,
        walkingNewer: false,
      }
    }
    // A batch that changed nothing cannot be asked for again without looping.
    if (added === 0) {
      return { ...base, stalled: true, walkingNewer: false }
    }
    return {
      ...base,
      hasOlder: batch.hasMore,
      stalled: false,
      gap: state.gap,
      walkingNewer: false,
    }
  }

  // "newer": the walk starts at the newest edge, so the edge is in hand from
  // the first batch on. The hole closes only when a batch reaches down into
  // what was already loaded.
  return {
    ...base,
    isNewest: true,
    gap: aboveWindow,
    hasOlder: batch.hasMore || state.hasOlder,
    stalled: false,
    // The walk goes on only while a hole is left to fill.
    walkingNewer: aboveWindow,
  }
}

// nextRequest is what may be asked for right now, or null when nothing may.
// It is the single place that decides there is no second request for the same
// page, no request while one is in flight, and no request for history that is
// known to have ended.
export function nextRequest(
  state: TimelineState,
  direction: "older" | "newer"
): TimelineRequest | null {
  if (state.loading !== null) return null

  if (direction === "older") {
    if (state.stalled || state.gap) return null
    if (state.olderError !== null) return null
    // The first page is always worth asking for: that answer is what says how
    // much history exists. Nothing beyond it may be asked until then.
    if (state.page > 0 && !state.hasOlder) return null
    return { direction: "older", page: state.page + 1 }
  }

  if (state.newerError !== null) return null
  // Nothing above is waiting: the newest edge is already on screen.
  if (state.isNewest) {
    return state.gap ? { direction: "newer", page: state.page + 1 } : null
  }
  // The edge is not in hand, so the walk starts over from the top rather than
  // guessing where the gap begins.
  return { direction: "newer", page: 1 }
}

// followNewest is whether the viewport should chase the newest message.
//
// It does while the reader is at the bottom, and it stops the moment they read
// back through history or while history is being fetched — an update arriving
// mid-scroll must never move someone who is reading.
export function followNewest(
  state: TimelineState,
  nearBottom: boolean
): boolean {
  return nearBottom && state.loading === null && !state.gap
}

// showJumpToLatest is whether the "jump to the newest message" control is
// useful: the reader is away from the bottom, or something above is missing.
export function showJumpToLatest(
  state: TimelineState,
  nearBottom: boolean
): boolean {
  return !nearBottom || !state.isNewest || state.gap
}
