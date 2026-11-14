import assert from "node:assert/strict"
import { test } from "node:test"
import { createElement } from "react"
import { renderToStaticMarkup } from "react-dom/server"

import { NoAccountsState } from "@/components/accounts/account-list"
import {
  channelDiscoveryErrorText,
  clientAction,
  createdChannelNavigation,
  shouldShowNoAccountsState,
} from "./channels"

const ACCOUNT_ID = "acc_aaaaaaaaaaaaaaaaaaaaaaaaaa"

test("cached channel workspace is not replaced by the no-account state for offline accounts", () => {
  assert.equal(shouldShowNoAccountsState([{ id: ACCOUNT_ID, server_key: "official", network_type: "telegram", status: "disconnected" }], undefined), false)
  assert.equal(shouldShowNoAccountsState([], undefined), true)
  assert.equal(shouldShowNoAccountsState([], "accounts unavailable"), false)
})

test("the true no-account state retains its add-account action", () => {
  const html = renderToStaticMarkup(createElement(NoAccountsState, { onAdd: () => undefined }))
  assert.match(html, /data-slot="no-accounts-message"/)
  assert.match(html, /data-slot="add-account-action"/)
  assert.match(html, /افزودن حساب/)
})

test("created channel navigation selects the created account, not its server key", () => {
  const location = createdChannelNavigation(
    { locally_registered: true, local_channel_id: 42, status: "created" },
    ACCOUNT_ID,
    new URLSearchParams("q=updates&create=1")
  )
  assert.deepEqual(location, {
    pathname: "/channels/42/messages",
    search: `q=updates&account=${ACCOUNT_ID}`,
  })
  const params = new URLSearchParams(location?.search)
  assert.deepEqual(params.getAll("account"), [ACCOUNT_ID])
  assert.equal(params.has("server"), false)
  assert.equal(params.has("create"), false)
})

test("account-scoped discovery errors name account state, channel availability, and permission", () => {
  assert.match(channelDiscoveryErrorText("account_not_connected"), /حساب متصل نیست/)
  assert.match(channelDiscoveryErrorText("account_reconnecting"), /حساب در حال اتصال دوباره/)
  assert.match(channelDiscoveryErrorText("needs_auth"), /این حساب به احراز هویت نیاز دارد/)
  assert.match(channelDiscoveryErrorText("channel_not_found"), /برای حساب انتخاب‌شده در دسترس نیست/)
  assert.match(channelDiscoveryErrorText("forbidden"), /این حساب اجازه/)
  assert.doesNotMatch(channelDiscoveryErrorText("worker_unavailable"), /سرور تلگرام/)
})

async function callAction(intent: string) {
  const originalWindow = globalThis.window
  const originalFetch = globalThis.fetch
  const calls: Array<{ url: string; method: string }> = []
  globalThis.window = {
    location: { origin: "https://panel.test" },
    sessionStorage: {
      getItem: () => "test-token",
      setItem: () => undefined,
      removeItem: () => undefined,
    },
  } as unknown as Window & typeof globalThis
  globalThis.fetch = (async (input: RequestInfo | URL, init?: RequestInit) => {
    const url = String(input)
    const method = init?.method ?? "GET"
    calls.push({ url, method })
    const pathname = new URL(url).pathname
    const data = pathname.endsWith("/auth/me")
      ? { id: 1, fullname: "user", username: "user", is_active: true }
      : pathname.endsWith("/channels/find")
        ? { channels: [{ id: "123", title: "کانال", type: "channel" }] }
        : { result: "joined" }
    return new Response(JSON.stringify({ data, meta: {} }), {
      status: 200,
      headers: { "Content-Type": "application/json" },
    })
  }) as typeof fetch

  try {
    const form = new FormData()
    form.set("intent", intent)
    form.set("account_id", ACCOUNT_ID)
    form.set("ref", intent === "join" ? "123" : "@channel")
    const result = await clientAction({
      request: new Request("https://panel.test/admin/channels", {
        method: "POST",
        body: form,
      }),
    } as never)
    return { calls, result }
  } finally {
    globalThis.window = originalWindow
    globalThis.fetch = originalFetch
  }
}

test("join action routes by explicit account_id without requiring a server identity", async () => {
  const { calls, result } = await callAction("join")
  assert.deepEqual(result, {
    ok: true,
    notice: "به کانال پیوستید و در فهرست محلی ثبت شد.",
    intent: "join",
  })
  assert.deepEqual(calls.map(({ url, method }) => [new URL(url).pathname, method]), [
    ["/api/v1/auth/me", "GET"],
    [`/api/v1/telegram/accounts/${ACCOUNT_ID}/channels/123/join`, "POST"],
  ])
})

test("define action discovers and joins on the same explicit account", async () => {
  const { calls } = await callAction("define")
  assert.deepEqual(calls.slice(1).map(({ url, method }) => [new URL(url).pathname, method]), [
    [`/api/v1/telegram/accounts/${ACCOUNT_ID}/channels/find`, "GET"],
    [`/api/v1/telegram/accounts/${ACCOUNT_ID}/channels/123/join`, "POST"],
  ])
})
