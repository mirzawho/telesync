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

test("all servers is the default and selected servers filter by client key", () => {
  const crossServer = { ...CHANNELS[0], id: 5, client_key: "official", channel_id: CHANNELS[0].channel_id }
  const rows = filterChannels([...CHANNELS, crossServer], {
    search: "",
    categoryIds: [],
    filed: FILED,
  })
  assert.deepEqual(ids(rows), [1, 2, 3, 4, 5])
  assert.deepEqual(ids(filterChannels([...CHANNELS, crossServer], {
    search: "", categoryIds: [], filed: FILED, serverKeys: ["official"],
  })), [5])
})

test("multiple server filters form a union without conflating Telegram ids", () => {
  const official = { ...CHANNELS[0], id: 5, client_key: "official", channel_id: 1001 }
  const other = { ...CHANNELS[0], id: 6, client_key: "another", channel_id: 1001 }
  const rows = filterChannels([...CHANNELS, official, other], {
    search: "", categoryIds: [], filed: FILED, serverKeys: ["official", "cat"],
  })
  assert.deepEqual(ids(rows), [1, 2, 3, 4, 5])
})

test("all accounts is the default and explicit account IDs filter by account identity", () => {
  const first = { ...CHANNELS[0], id: 7, account_id: "acc_first" }
  const second = { ...CHANNELS[0], id: 8, account_id: "acc_second" }
  const legacy = { ...CHANNELS[0], id: 9, account_id: undefined }
  const rows = [first, second, legacy]
  assert.deepEqual(ids(filterChannels(rows, {
    search: "", categoryIds: [], filed: FILED,
  })), [7, 8, 9])
  assert.deepEqual(ids(filterChannels(rows, {
    search: "", categoryIds: [], filed: FILED, accountIds: ["acc_second"],
  })), [8])
  assert.deepEqual(ids(filterChannels(rows, {
    search: "", categoryIds: [], filed: FILED, accountIds: ["acc_first", "acc_second"],
  })), [7, 8])
})

test("account, search and category filters compose without changing category behavior", () => {
  const rows = [
    { ...CHANNELS[0], id: 7, account_id: "acc_first" },
    { ...CHANNELS[2], id: 8, account_id: "acc_second" },
  ]
  assert.deepEqual(ids(filterChannels(rows, {
    search: "tech", categoryIds: [10], filed: { 7: [10], 8: [10] },
    accountIds: ["acc_second"],
  })), [8])
  assert.deepEqual(ids(filterChannels(rows, {
    search: "tech", categoryIds: [], filed: {},
    accountIds: ["acc_second"],
  })), [8])
})

test("server, search and category filters combine with AND", () => {
  const official = { ...CHANNELS[0], id: 5, client_key: "official", title: "Tech news" }
  const rows = filterChannels([...CHANNELS, official], {
    search: "tech", categoryIds: [10], filed: { ...FILED, 5: [10] }, serverKeys: ["official"],
  })
  assert.deepEqual(ids(rows), [5])
  assert.deepEqual(filterChannels([...CHANNELS, official], {
    search: "missing", categoryIds: [], filed: FILED, serverKeys: ["official"],
  }), [])
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
