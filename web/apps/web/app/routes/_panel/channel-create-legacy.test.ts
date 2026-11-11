import assert from "node:assert/strict"
import test from "node:test"

import { legacyCreateChannelDestination } from "./channel-create-legacy"

test("legacy create link returns to the explorer, opens the dialog, and preserves filters", () => {
  const target = new URL(legacyCreateChannelDestination("?server=cat&server=official&q=politics&create=0"), "https://telesync.invalid")
  assert.equal(target.pathname, "/channels")
  assert.equal(target.searchParams.get("create"), "1")
  assert.deepEqual(target.searchParams.getAll("server"), ["cat", "official"])
  assert.equal(target.searchParams.get("q"), "politics")
  assert.equal(target.searchParams.getAll("create").length, 1)
})
