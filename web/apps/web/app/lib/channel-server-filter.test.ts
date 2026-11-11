import assert from "node:assert/strict"
import { test } from "node:test"

import {
  parseServerSelection,
  toggleServerSelection,
  writeServerSelection,
} from "@/lib/channel-server-filter"

test("no server query means all, repeated values are deduplicated and invalid keys dropped", () => {
  assert.deepEqual(parseServerSelection(new URLSearchParams(), ["official", "cat"]), [])
  assert.deepEqual(
    parseServerSelection(new URLSearchParams("server=cat&server=cat&server=bad"), ["official", "cat"]),
    ["cat"]
  )
})

test("selection toggles into multi-select and final removal normalizes to all", () => {
  const all = ["official", "cat", "other"]
  assert.deepEqual(toggleServerSelection([], "cat", all), ["cat"])
  assert.deepEqual(toggleServerSelection(["cat"], "official", all), ["cat", "official"])
  assert.deepEqual(toggleServerSelection(["cat"], "cat", all), [])
  assert.deepEqual(toggleServerSelection(["cat", "official"], "official", all), ["cat"])
  assert.deepEqual(toggleServerSelection(["cat", "official"], "other", all), [])
})

test("all servers removes only server params and preserves unrelated query state", () => {
  const params = new URLSearchParams("q=news&server=cat&category_ids=10&x=1")
  assert.equal(writeServerSelection(params, []).toString(), "q=news&category_ids=10&x=1")
  assert.equal(
    writeServerSelection(params, ["official", "cat"]).toString(),
    "q=news&category_ids=10&x=1&server=official&server=cat"
  )
})
