import assert from "node:assert/strict"
import { test } from "node:test"

import {
  applyBatch,
  beginLoad,
  emptyTimeline,
  failLoad,
  followNewest,
  nextRequest,
  mergeLatest,
  replaceMessage,
  removeMessage,
  resetTimeline,
  retry,
  showJumpToLatest,
  type TimelineState,
} from "@/lib/message-timeline"
import type { ChannelMessage } from "@/lib/types"

// The API facts these tests are written against, verified in the worker and the
// gotd adapter: page 1 is the newest edge, every later page continues below a
// per-channel message-id cursor, each page arrives newest-first, and
// `has_more` is true whenever a page came back full.

const msg = (id: number): ChannelMessage => ({
  id,
  date: `2026-11-04T${String(9 + (id % 10)).padStart(2, "0")}:00:00Z`,
  text: `پیام ${id}`,
  views: id,
  reactions: [],
  entities: [],
})

// A page as the API hands it over: newest first.
const page = (ids: number[], hasMore = true) =>
  ids.map((id) => msg(id)).sort((a, b) => b.id - a.id)

const loaded = (...ids: number[]): TimelineState =>
  applyBatch(emptyTimeline(7), {
    messages: page(ids),
    page: 1,
    hasMore: true,
  }, "initial")

const ids = (state: TimelineState) => state.messages.map((m) => m.id)

test("confirmed newest refresh replaces IDs and deduplicates without resetting loaded pages", () => {
  const state = { ...loaded(81, 82, 83), page: 3, hasOlder: true }
  const edited = { ...msg(82), text: "updated" }
  const refreshed = mergeLatest(state, [msg(84), edited, msg(84)], true)
  assert.deepEqual(ids(refreshed), [81, 82, 83, 84])
  assert.equal(refreshed.messages[1].text, "updated")
  assert.equal(refreshed.page, 3)
  assert.equal(refreshed.hasOlder, true)
})

test("edit replacement preserves position and delete removes only the target ID", () => {
  const state = loaded(81, 82, 83)
  const edited = replaceMessage(state, { ...msg(82), text: "updated" })
  assert.deepEqual(ids(edited), [81, 82, 83])
  assert.equal(edited.messages[1].text, "updated")
  const deleted = removeMessage(edited, 82)
  assert.deepEqual(ids(deleted), [81, 83])
})

// --- opening a channel ----------------------------------------------------

test("the first page lands newest-edge-first and in chronological order", () => {
  const state = loaded(81, 82, 83, 84, 85)
  assert.deepEqual(ids(state), [81, 82, 83, 84, 85])
  // The newest message is the last one on screen, and the edge is in hand.
  assert.equal(state.messages.at(-1)!.id, 85)
  assert.equal(state.isNewest, true)
  assert.equal(state.hasOlder, true)
  assert.equal(state.gap, false)
})

test("the newest message is available as soon as the first page lands", () => {
  const state = loaded(100)
  assert.equal(state.messages.at(-1)!.id, 100)
  assert.equal(state.isNewest, true)
})

test("a short first page means there is no older history to ask for", () => {
  const state = applyBatch(emptyTimeline(7), {
    messages: page([1, 2, 3]),
    page: 1,
    hasMore: false,
  }, "initial")
  assert.equal(state.hasOlder, false)
  assert.equal(nextRequest(state, "older"), null)
})

test("a full first page does not by itself prove more history exists", () => {
  // The backend reports has_more only from its own answer; the timeline trusts
  // that rather than assuming every page is full.
  const state = applyBatch(emptyTimeline(7), {
    messages: page([1, 2, 3]),
    page: 1,
    hasMore: true,
  }, "initial")
  assert.equal(state.hasOlder, true)
})

// --- scrolling upward -----------------------------------------------------

test("the first page is asked for before anything is known about history", () => {
  // Nothing is loaded, so nothing is known yet about how much history exists.
  // The first page must still be asked for — that answer is what tells us.
  const fresh = resetTimeline(7)
  assert.deepEqual(nextRequest(fresh, "older"), { direction: "older", page: 1 })
})

test("scrolling up asks for the next page below the cursor", () => {
  const state = loaded(81, 82, 83)
  assert.deepEqual(nextRequest(state, "older"), { direction: "older", page: 2 })
})

test("older messages are prepended before what is already loaded", () => {
  const state = loaded(81, 82, 83)
  const next = applyBatch(state, {
    messages: page([78, 79, 80]),
    page: 2,
    hasMore: true,
  }, "older")
  assert.deepEqual(ids(next), [78, 79, 80, 81, 82, 83])
  assert.equal(next.page, 2)
})

test("prepending older messages never disturbs the newest edge", () => {
  const before = loaded(81, 82, 83)
  const after = applyBatch(before, {
    messages: page([78, 79, 80]),
    page: 2,
    hasMore: true,
  }, "older")
  assert.equal(after.isNewest, before.isNewest)
  assert.equal(after.messages.at(-1)!.id, 83)
})

test("older messages arriving out of order still land in order", () => {
  const state = applyBatch(loaded(81, 82), {
    messages: page([80, 78, 79]),
    page: 2,
    hasMore: true,
  }, "older")
  assert.deepEqual(ids(state), [78, 79, 80, 81, 82])
})

// --- duplicates and chains ------------------------------------------------

test("the same message id is never kept twice", () => {
  const state = applyBatch(loaded(81, 82, 83), {
    messages: page([80, 81, 82]),
    page: 2,
    hasMore: true,
  }, "older")
  assert.deepEqual(ids(state), [80, 81, 82, 83])
})

test("a batch that only repeats what is loaded stops the older walk", () => {
  // The worker cursor restarted inside our window: nothing here is new, and
  // asking again would be a loop rather than progress.
  const state = applyBatch(loaded(81, 82, 83), {
    messages: page([81, 82, 83]),
    page: 2,
    hasMore: true,
  }, "older")
  assert.deepEqual(ids(state), [81, 82, 83])
  assert.equal(nextRequest(state, "older"), null)
})

test("a batch from the very top means the chain broke, not that history is loaded", () => {
  const state = applyBatch(loaded(81, 82, 83), {
    messages: page([200, 201, 202]),
    page: 2,
    hasMore: true,
  }, "older")
  assert.equal(state.gap, true)
  assert.equal(state.hasOlder, false)
  assert.equal(nextRequest(state, "older"), null)
  // What the reader already had stays on screen.
  assert.deepEqual(ids(state), [81, 82, 83])
})

// --- scrolling downward ---------------------------------------------------

test("scrolling down with everything loaded invents no request", () => {
  const state = loaded(81, 82, 83)
  assert.equal(state.isNewest, true)
  assert.equal(nextRequest(state, "newer"), null)
})

test("a known gap above the window is fetched from the newest edge", () => {
  const state = { ...loaded(81, 82, 83), isNewest: false }
  assert.deepEqual(nextRequest(state, "newer"), { direction: "newer", page: 1 })
})

test("the newer walk continues down until it meets the loaded window", () => {
  const state = { ...loaded(81, 82, 83), isNewest: false }
  const started = applyBatch(state, {
    messages: page([100, 101]),
    page: 1,
    hasMore: true,
  }, "newer")
  // The newest edge is now in hand, but the walk has not reached down to 83.
  assert.equal(started.isNewest, true)
  assert.equal(started.gap, true)
  assert.deepEqual(ids(started), [81, 82, 83, 100, 101])
  assert.deepEqual(nextRequest(started, "newer"), { direction: "newer", page: 2 })
})

test("the newer walk closes the hole and stops", () => {
  const state = { ...loaded(81, 82, 83), isNewest: false }
  const started = applyBatch(state, {
    messages: page([100, 101]),
    page: 1,
    hasMore: true,
  }, "newer")
  // Page 2 continues below page 1 and reaches into what was already loaded,
  // which is what makes the history whole again.
  const closed = applyBatch(started, {
    messages: page([81, 82, 83, 84, 85, 86, 87, 88, 89, 90, 91, 92, 93, 94, 95, 96, 97, 98, 99]),
    page: 2,
    hasMore: true,
  }, "newer")
  assert.equal(closed.gap, false)
  assert.equal(closed.isNewest, true)
  assert.equal(closed.messages.length, 21)
  assert.deepEqual(nextRequest(closed, "newer"), null)
  assert.deepEqual(nextRequest(closed, "older"), { direction: "older", page: 3 })
})

test("a batch reaching the bottom of the loaded window ends the newer walk", () => {
  const state = { ...loaded(81, 82, 83), isNewest: false }
  const next = applyBatch(state, {
    messages: page([82, 83, 84]),
    page: 1,
    hasMore: true,
  }, "newer")
  assert.equal(next.gap, false)
  assert.equal(next.isNewest, true)
})

// --- one request at a time ------------------------------------------------

test("a request already in flight is not asked for again", () => {
  const state = beginLoad(loaded(81, 82, 83), "older")
  assert.equal(nextRequest(state, "older"), null)
})

test("only one direction can be in flight at a time", () => {
  const state = beginLoad({ ...loaded(81, 82, 83), isNewest: false }, "newer")
  assert.equal(nextRequest(state, "older"), null)
})

test("repeated scroll events cannot queue the same page twice", () => {
  let state = loaded(81, 82, 83)
  const first = nextRequest(state, "older")
  state = beginLoad(state, first!.direction)
  assert.equal(nextRequest(state, "older"), null)
  state = applyBatch(state, {
    messages: page([78, 79, 80]),
    page: first!.page,
    hasMore: true,
  }, "older")
  // The walk advanced; the next ask is the following page, not a repeat.
  assert.deepEqual(nextRequest(state, "older"), { direction: "older", page: 3 })
})

// --- errors ---------------------------------------------------------------

test("a failed older load keeps what is on screen and reports the failure", () => {
  const before = loaded(81, 82, 83)
  const after = failLoad(before, "older", "خطا")
  assert.deepEqual(ids(after), [81, 82, 83])
  assert.equal(after.olderError, "خطا")
  assert.equal(after.loading, null)
})

test("a failed newer load leaves the newest edge unknown", () => {
  const before = { ...loaded(81, 82, 83), isNewest: false }
  const after = failLoad(beginLoad(before, "newer"), "newer", "خطا")
  assert.equal(after.isNewest, false)
  assert.equal(after.newerError, "خطا")
  assert.deepEqual(ids(after), [81, 82, 83])
})

test("an error does not retry by itself", () => {
  const failed = failLoad(loaded(81, 82, 83), "older", "خطا")
  // Nothing is queued behind the failure: the reader asks again explicitly.
  assert.equal(failed.loading, null)
  assert.equal(nextRequest(failed, "older"), null)
  const asked = retry(failed, "older")
  assert.equal(asked.olderError, null)
  assert.deepEqual(nextRequest(asked, "older"), { direction: "older", page: 2 })
})

// --- following the newest message -----------------------------------------

test("the reader starts following the newest message", () => {
  assert.equal(followNewest(loaded(81, 82), true), true)
})

test("scrolling away from the bottom stops following", () => {
  assert.equal(followNewest(loaded(81, 82), false), false)
})

test("newer messages only pull the reader down when they were at the bottom", () => {
  const state = loaded(81, 82)
  assert.equal(followNewest(state, true), true)
  assert.equal(followNewest(state, false), false)
})

test("loading older history never counts as following the newest", () => {
  const state = beginLoad(loaded(81, 82), "older")
  assert.equal(followNewest(state, true), false)
})

// --- jump to latest -------------------------------------------------------

test("the jump control is shown only while it is useful", () => {
  assert.equal(showJumpToLatest(loaded(81, 82), true), false)
  assert.equal(showJumpToLatest(loaded(81, 82), false), true)
})

test("the jump control is shown whenever a gap above is known", () => {
  const state = { ...loaded(81, 82), isNewest: false }
  assert.equal(showJumpToLatest(state, true), true)
})

// --- switching channels ---------------------------------------------------

test("switching channels drops the old messages and the pending work", () => {
  const state = beginLoad(loaded(81, 82), "older")
  const fresh = resetTimeline(9)
  assert.equal(fresh.channelId, 9)
  assert.deepEqual(fresh.messages, [])
  assert.equal(fresh.loading, null)
  assert.equal(fresh.page, 0)
  // The new channel starts its own walk at page 1, and only there.
  assert.deepEqual(nextRequest(fresh, "older"), { direction: "older", page: 1 })
  assert.equal(nextRequest(fresh, "newer"), null)
  assert.notEqual(fresh.loading, state.loading)
})
