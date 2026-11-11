import assert from "node:assert/strict"
import { test } from "node:test"

import {
  deleteChannelMessage,
  editChannelMessage,
  getEditOperation,
  getSendOperation,
  sendChannelMessage,
  type ChannelMessagePayload,
} from "@/lib/channel-message-api"

const payload: ChannelMessagePayload = {
  text: "سلام",
  entities: [],
  options: { silent: false, disable_web_page_preview: false },
}

function installFetch(responseData: unknown) {
  const originalWindow = globalThis.window
  const originalFetch = globalThis.fetch
  let request: { url: string; init: RequestInit } | undefined
  globalThis.window = { location: { origin: "https://panel.test" } } as Window & typeof globalThis
  globalThis.fetch = (async (input: RequestInfo | URL, init?: RequestInit) => {
    request = { url: String(input), init: init ?? {} }
    return new Response(JSON.stringify({ data: responseData, meta: {} }), {
      status: 200,
      headers: { "Content-Type": "application/json" },
    })
  }) as typeof fetch
  return {
    read: () => request,
    restore: () => {
      globalThis.window = originalWindow
      globalThis.fetch = originalFetch
    },
  }
}

test("text sends use the configured API base and actual Telegram destination", async () => {
  const fetch = installFetch({ operation_id: "send_1", status: "queued" })
  try {
    await sendChannelMessage("official", 141, payload, "intent-1", { token: "token" })
    const request = fetch.read()!
    assert.equal(request.url, "https://panel.test/api/v1/telegram/official/channels/141/messages/send")
    assert.equal(request.init.method, "POST")
    assert.equal(new Headers(request.init.headers).get("Authorization"), "Bearer token")
    assert.equal(new Headers(request.init.headers).get("Idempotency-Key"), "intent-1")
    assert.equal(new Headers(request.init.headers).get("Content-Type"), "application/json")
    assert.deepEqual(JSON.parse(String(request.init.body)), payload)
  } finally {
    fetch.restore()
  }
})

test("multipart send carries one file without overriding its boundary header", async () => {
  const fetch = installFetch({ operation_id: "send_2", status: "queued" })
  try {
    const file = new File(["picture"], "photo.jpg", { type: "image/jpeg" })
    const mediaPayload = { ...payload, media: { kind: "photo" as const } }
    await sendChannelMessage("cat", "141", mediaPayload, "intent-2", { file })
    const request = fetch.read()!
    assert.equal(request.url, "https://panel.test/api/v1/telegram/cat/channels/141/messages/send")
    assert.ok(request.init.body instanceof FormData)
    assert.equal(new Headers(request.init.headers).get("Content-Type"), null)
    const body = request.init.body as FormData
    assert.deepEqual(JSON.parse(String(body.get("payload"))), mediaPayload)
    assert.equal((body.get("file") as File).name, "photo.jpg")
  } finally {
    fetch.restore()
  }
})

test("edit, status polling, and delete address the same Telegram target", async () => {
  const fetch = installFetch({ operation_id: "op", status: "edited" })
  try {
    await editChannelMessage("cat", 141, 123, payload, "edit-1")
    assert.equal(fetch.read()!.url, "https://panel.test/api/v1/telegram/cat/channels/141/messages/123")
    await getEditOperation("cat", 141, 123, "op")
    assert.equal(fetch.read()!.url, "https://panel.test/api/v1/telegram/cat/channels/141/messages/123/edit/op")
    await getSendOperation("cat", 141, "send_1")
    assert.equal(fetch.read()!.url, "https://panel.test/api/v1/telegram/cat/channels/141/messages/send/send_1")
    await deleteChannelMessage("cat", 141, 123)
    assert.equal(fetch.read()!.url, "https://panel.test/api/v1/telegram/cat/channels/141/messages/123")
    assert.equal(fetch.read()!.init.method, "DELETE")
    assert.equal(fetch.read()!.init.body, undefined)
  } finally {
    fetch.restore()
  }
})

test("unsafe destination and message identifiers are rejected before fetch", async () => {
  const fetch = installFetch({})
  try {
    await assert.rejects(sendChannelMessage("", 141, payload, "key"))
    await assert.rejects(editChannelMessage("cat", 141, 0, payload, "key"))
    assert.equal(fetch.read(), undefined)
  } finally {
    fetch.restore()
  }
})
