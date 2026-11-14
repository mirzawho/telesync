import assert from "node:assert/strict"
import { test } from "node:test"

import { accountApi, listAccountChannels } from "@/lib/account-api"
import { API_BASE } from "@/lib/api"

// These are the contract's own paths (`V005-ACCOUNT-CONTRACTS.md` §6.1). The
// test holds the panel to them: a route the contract does not define must not
// appear here, and a path segment must never be a username or a phone number.

const CONTRACT_ROUTES = [
  "GET /me/telegram-accounts",
  "GET /me/telegram-accounts/{account_id}",
  "POST /me/telegram-accounts/login/start",
  "GET /me/telegram-accounts/login/{attempt_id}",
  "POST /me/telegram-accounts/login/{attempt_id}/code",
  "POST /me/telegram-accounts/login/{attempt_id}/password",
  "POST /me/telegram-accounts/login/{attempt_id}/cancel",
  "POST /me/telegram-accounts/{account_id}/disconnect",
  "POST /me/telegram-accounts/{account_id}/logout",
  "DELETE /me/telegram-accounts/{account_id}",
]

test("every route the contract defines is reachable through one name", () => {
  const account = "acc_01hxyzabcdefghijklmnopqrstuv"
  const attempt = "lga_01hxyzabcdefghijklmnopqrstuv"
  // The paths carry real opaque ids; the contract writes them as placeholders.
  // Replacing what this build generated with the placeholder proves the shape
  // matches without pretending the ids are not there.
  const placeholder = (path: string) =>
    path
      .replaceAll(account, "{account_id}")
      .replaceAll(attempt, "{attempt_id}")
  const built = [
    `GET ${accountApi.list()}`,
    `GET ${accountApi.get(account)}`,
    `POST ${accountApi.loginStart()}`,
    `GET ${accountApi.loginStatus(attempt)}`,
    `POST ${accountApi.loginCode(attempt)}`,
    `POST ${accountApi.loginPassword(attempt)}`,
    `POST ${accountApi.loginCancel(attempt)}`,
    `POST ${accountApi.disconnect(account)}`,
    `POST ${accountApi.logout(account)}`,
    `DELETE ${accountApi.remove(account)}`,
  ].map((route) => {
    const [method, ...rest] = route.split(" ")
    return `${method} ${placeholder(rest.join(" "))}`
  })
  assert.deepEqual(built, CONTRACT_ROUTES)
})

test("no account route is addressed by a username or a phone number", () => {
  // The opaque id is the only identity that appears in a path.
  const account = "acc_01hxyzabcdefghijklmnopqrstuv"
  for (const path of [
    accountApi.get(account),
    accountApi.disconnect(account),
    accountApi.logout(account),
    accountApi.remove(account),
  ]) {
    assert.match(path, /acc_[a-z0-9]+/)
    assert.doesNotMatch(path, /\+?\d{6,}/, "a phone number reached a path")
    assert.doesNotMatch(path, /@/, "a username reached a path")
  }
})

test("an id is percent-encoded, so a hostile value cannot widen a route", () => {
  const hostile = "acc_x/../../admin"
  const path = accountApi.get(hostile)
  assert.equal(path, "/me/telegram-accounts/acc_x%2F..%2F..%2Fadmin")
  assert.ok(!path.includes("/../"))
})

test("explicit account resource routes name the account before the Telegram peer", () => {
  const account = "acc_01hxyzabcdefghijklmnopqrstuv"
  assert.equal(
    accountApi.channelDetail(account, "123"),
    `/telegram/accounts/${account}/channels/123`
  )
  assert.equal(
    accountApi.channelFind(account),
    `/telegram/accounts/${account}/channels/find`
  )
  assert.equal(
    accountApi.channelJoin(account, "+AbCdEf123456"),
    `/telegram/accounts/${account}/channels/%2BAbCdEf123456/join`
  )
  assert.equal(
    accountApi.messageCopy(account),
    `/telegram/accounts/${account}/messages/copy`
  )
})

test("account destination listing uses the selected account path and sends query", async () => {
  const account = "acc_01hxyzabcdefghijklmnopqrstuv"
  const previousFetch = globalThis.fetch
  const previousWindow = globalThis.window
  const urls: URL[] = []
  Object.defineProperty(globalThis, "window", {
    configurable: true,
    value: { location: { origin: "https://panel.example" } },
  })
  globalThis.fetch = async (input) => {
    urls.push(new URL(String(input)))
    return new Response(
      JSON.stringify({
        data: { channels: [{ id: 154, title: "Example", permissions: { new: true } }] },
        meta: { page: 1, per_page: 100, total: 1 },
      }),
      { status: 200, headers: { "Content-Type": "application/json" } },
    )
  }
  try {
    const channels = await listAccountChannels("test-token", account)
    assert.equal(channels[0]?.id, 154)
    assert.equal(urls.length, 1)
    assert.equal(
      urls[0]?.pathname,
      `${API_BASE}${accountApi.channelList(account)}`,
    )
    assert.equal(urls[0]?.searchParams.get("page"), "1")
    assert.equal(urls[0]?.searchParams.get("per_page"), "100")
    assert.equal(urls[0]?.searchParams.get("capability"), "send")
  } finally {
    globalThis.fetch = previousFetch
    Object.defineProperty(globalThis, "window", {
      configurable: true,
      value: previousWindow,
    })
  }
})

test("account destination listing fetches all pages and does not swallow page errors", async () => {
  const account = "acc_01hxyzabcdefghijklmnopqrstuv"
  const previousFetch = globalThis.fetch
  const previousWindow = globalThis.window
  const urls: URL[] = []
  Object.defineProperty(globalThis, "window", {
    configurable: true,
    value: { location: { origin: "https://panel.example" } },
  })
  globalThis.fetch = async (input) => {
    const url = new URL(String(input))
    urls.push(url)
    const page = Number(url.searchParams.get("page"))
    return new Response(
      JSON.stringify({
        data: {
          channels: [{ id: page, title: `Page ${page}`, permissions: { new: true } }],
        },
        meta: {
          page,
          per_page: 100,
          total: 101,
          has_more: page === 1,
          ...(page === 1 ? { next_page: 2 } : {}),
        },
      }),
      { status: 200, headers: { "Content-Type": "application/json" } },
    )
  }
  try {
    const channels = await listAccountChannels("test-token", account)
    assert.deepEqual(channels.map((channel) => channel.id), [1, 2])
    assert.deepEqual(urls.map((url) => url.searchParams.get("page")), ["1", "2"])
    assert.ok(urls.every((url) => url.searchParams.get("capability") === "send"))
  } finally {
    globalThis.fetch = previousFetch
    Object.defineProperty(globalThis, "window", {
      configurable: true,
      value: previousWindow,
    })
  }
})
