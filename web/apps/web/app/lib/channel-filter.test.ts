import assert from "node:assert/strict"
import { test } from "node:test"

import {
  matchesSearch,
  filterChannels,
  toggleCategory,
} from "@/lib/channel-filter"
import type { Channel } from "@/lib/types"

function channel(id: number, title: string, username?: string): Channel {
  return {
    id,
    client_key: "cat",
    channel_id: 1000 + id,
    type: "channel",
    title,
    username,
    avatar_available: false,
    membership_status: "member",
    participants_count: null,
    created_at: "",
    updated_at: "",
    last_synced_at: "",
  }
}

const CHANNELS = [
  channel(1, "خبر فناوری", "technews"),
  channel(2, "اقتصاد روز", "economy"),
  channel(3, "Tech Weekly", "techweekly"),
  channel(4, "سرگرمی", "fun"),
]

// channel id -> the category ids it is filed under
const FILED: Record<number, number[]> = {
  1: [10, 20],
  2: [20],
  3: [],
  4: [30],
}

const ids = (rows: Channel[]) => rows.map((c) => c.id)

test("toggleCategory adds an inactive category and removes an active one", () => {
  assert.deepEqual(toggleCategory([], 10), [10])
  assert.deepEqual(toggleCategory([10], 10), [])
  assert.deepEqual(toggleCategory([10], 20), [10, 20])
  assert.deepEqual(toggleCategory([10, 20], 10), [20])
  // The list is id-keyed: adding twice cannot duplicate.
  assert.deepEqual(toggleCategory([10], 10), [])
})

test("search matches the title case-insensitively", () => {
  assert.equal(matchesSearch(CHANNELS[0], "فناوری"), true)
  assert.equal(matchesSearch(CHANNELS[2], "tech"), true)
  assert.equal(matchesSearch(CHANNELS[2], "TECH"), true)
  assert.equal(matchesSearch(CHANNELS[1], "فناوری"), false)
})

test("search matches the username too", () => {
  assert.equal(matchesSearch(CHANNELS[1], "economy"), true)
  assert.equal(matchesSearch(CHANNELS[1], "@economy"), false)
})

test("search trims surrounding whitespace and ignores an empty query", () => {
  assert.equal(matchesSearch(CHANNELS[0], "   "), true)
  assert.equal(matchesSearch(CHANNELS[0], "  فناوری  "), true)
  assert.equal(matchesSearch(CHANNELS[0], "\t\n"), true)
})

test("an unfiltered list is returned untouched", () => {
  const rows = filterChannels(CHANNELS, { search: "", categoryIds: [], filed: FILED })
  assert.deepEqual(ids(rows), [1, 2, 3, 4])
})

test("selected categories match by OR across membership", () => {
  const rows = filterChannels(CHANNELS, {
    search: "",
    categoryIds: [10, 30],
    filed: FILED,
  })
  assert.deepEqual(ids(rows), [1, 4])
})

test("a single category matches only its own channels", () => {
  const rows = filterChannels(CHANNELS, {
    search: "",
    categoryIds: [20],
    filed: FILED,
  })
  assert.deepEqual(ids(rows), [1, 2])
})

test("search and category filters combine with AND", () => {
  const rows = filterChannels(CHANNELS, {
    search: "tech",
    categoryIds: [10],
    filed: FILED,
  })
  assert.deepEqual(ids(rows), [1])
})

test("clearing every category shows all channels again", () => {
  const rows = filterChannels(CHANNELS, {
    search: "",
    categoryIds: [],
    filed: FILED,
  })
  assert.equal(rows.length, CHANNELS.length)
})

test("a channel with no categories is never matched by a category filter", () => {
  const rows = filterChannels(CHANNELS, {
    search: "",
    categoryIds: [10, 20, 30],
    filed: FILED,
  })
  assert.deepEqual(ids(rows), [1, 2, 4])
})

test("a filter with no match returns an empty list, never the full one", () => {
  const rows = filterChannels(CHANNELS, {
    search: "چیزی که نیست",
    categoryIds: [],
    filed: FILED,
  })
  assert.deepEqual(rows, [])
})

test("the input list is never mutated", () => {
  const before = [...CHANNELS]
  filterChannels(CHANNELS, { search: "tech", categoryIds: [10], filed: FILED })
  assert.deepEqual(CHANNELS, before)
})
