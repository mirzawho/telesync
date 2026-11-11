import assert from "node:assert/strict"
import { test } from "node:test"

import { channelMessageCapabilities } from "@/lib/channel-message-permissions"

test("channel message write capabilities are independent and fail closed", () => {
  const expected = [
    [{ new: true, edit: true, delete: true, read: true }, { create: true, edit: true, delete: true, read: true }],
    [{ new: true, edit: false, delete: false, read: true }, { create: true, edit: false, delete: false, read: true }],
    [{ new: false, edit: true, delete: false, read: true }, { create: false, edit: true, delete: false, read: true }],
    [{ new: false, edit: false, delete: true, read: true }, { create: false, edit: false, delete: true, read: true }],
    [{ new: false, edit: false, delete: false, read: true }, { create: false, edit: false, delete: false, read: true }],
  ] as const
  for (const [permissions, capabilities] of expected) {
    assert.deepEqual(channelMessageCapabilities(permissions), capabilities)
  }
  assert.deepEqual(channelMessageCapabilities(undefined), {
    read: false, create: false, edit: false, delete: false,
  })
  assert.deepEqual(channelMessageCapabilities({ new: true }), {
    read: false, create: true, edit: false, delete: false,
  })
})
