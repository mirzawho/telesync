import assert from "node:assert/strict"
import { test } from "node:test"

import {
  accountActionsFor,
  accountOriginLabel,
  accountStatusText,
  accountStatusTone,
  accountSubtitle,
  accountTitle,
  accountsOfServer,
  connectedAccounts,
  connectionOf,
  isAccountConnected,
  loginMethodsOf,
  serverHasSeveralAccounts,
  serverLabel,
  supportsLoginMethod,
  wizardServersOf,
} from "@/lib/telegram-account"
import type { TelegramAccount } from "@/lib/types"

const CONNECTED: TelegramAccount = {
  id: "acc_01hxyzabcdefghijklmnopqrstuvwxyz",
  server_key: "official",
  network_type: "telegram",
  remote_user_id: 424242,
  remote_username: "mirza",
  remote_display_name: "میرزا",
  phone_number_masked: "+98 *** **45",
  status: "connected",
  last_connected_at: "2026-11-12T08:00:00Z",
}

const NEEDS_LOGIN: TelegramAccount = {
  ...CONNECTED,
  id: "acc_02hxyzabcdefghijklmnopqrstuvwxyz",
  status: "authentication_required",
}

const DISCONNECTED: TelegramAccount = {
  ...CONNECTED,
  id: "acc_03hxyzabcdefghijklmnopqrstuvwxyz",
  status: "disconnected",
}

test("an account's status text is Persian and never the raw enum", () => {
  assert.equal(accountStatusText(CONNECTED), "متصل")
  assert.equal(accountStatusText(NEEDS_LOGIN), "نیازمند ورود دوباره")
  assert.equal(accountStatusText(DISCONNECTED), "قطع")
  // A status this build does not know still gets words, not the enum.
  const unknown = { ...CONNECTED, status: "something_new" } as TelegramAccount
  assert.doesNotMatch(accountStatusText(unknown), /something_new/)
  assert.ok(accountStatusText(unknown).length > 0)
})

test("connectionOf separates the states a reader acts on differently", () => {
  assert.equal(connectionOf(CONNECTED), "connected")
  assert.equal(connectionOf(NEEDS_LOGIN), "needs_login")
  assert.equal(connectionOf(DISCONNECTED), "disconnected")
  assert.equal(
    connectionOf({ ...CONNECTED, status: "reconnecting" }),
    "connecting"
  )
  assert.equal(connectionOf({ ...CONNECTED, status: "error" }), "error")
})

test("only a connected account counts as connected", () => {
  assert.equal(isAccountConnected(CONNECTED), true)
  assert.equal(isAccountConnected(NEEDS_LOGIN), false)
  assert.equal(isAccountConnected(DISCONNECTED), false)
  // A transport that looks healthy does not make an account connected.
  assert.equal(isAccountConnected({ ...NEEDS_LOGIN, status: "connecting" }), false)
})

test("the status tone matches what the status means", () => {
  assert.equal(accountStatusTone(CONNECTED), "ok")
  assert.equal(accountStatusTone(NEEDS_LOGIN), "warn")
  assert.equal(accountStatusTone(DISCONNECTED), "off")
  assert.equal(accountStatusTone({ ...CONNECTED, status: "error" }), "bad")
  assert.equal(accountStatusTone({ ...CONNECTED, status: "connecting" }), "busy")
})

test("the server is named by its network, never guessed from the key", () => {
  assert.equal(serverLabel("official", "telegram"), "رسمی تلگرام")
  assert.equal(serverLabel("cat", "custom"), "سرور اختصاصی")
  // A key this build has no name for is shown as itself rather than described.
  assert.equal(serverLabel("mystery", undefined), "mystery")
  assert.equal(serverLabel("mystery", "telegram"), "رسمی تلگرام")
})

test("the account's own display name leads the title", () => {
  assert.equal(accountTitle(CONNECTED), "میرزا")
  const noName = { ...CONNECTED, remote_display_name: "" }
  assert.equal(accountTitle(noName), "@mirza")
  const noHandle = { ...CONNECTED, remote_display_name: "", remote_username: "" }
  assert.equal(accountTitle(noHandle), "+98 *** **45")
  const bare = {
    ...noHandle,
    phone_number_masked: "",
    server_key: "cat",
    network_type: "custom",
  } as TelegramAccount
  assert.equal(accountTitle(bare), "سرور اختصاصی")
})

test("the subtitle carries the handle and the masked phone beside the name", () => {
  assert.equal(accountSubtitle(CONNECTED), "@mirza — +98 *** **45")
  const noName = { ...CONNECTED, remote_display_name: "" }
  assert.equal(accountSubtitle(noName), "+98 *** **45")
})

test("no account row ever renders a full phone number", () => {
  // The API only ever returns a masked one; the panel shows that field as-is
  // and has nowhere a full number could come from.
  const rendered = `${accountTitle(CONNECTED)} ${accountSubtitle(CONNECTED)}`
  assert.doesNotMatch(rendered, /\+989\d{9}/)
  assert.match(rendered, /\*\*\*/)
})

test("the origin label names the account, never only the server", () => {
  // Two accounts on one server must not render the same origin label.
  const other = {
    ...CONNECTED,
    id: "acc_04hxyzabcdefghijklmnopqrstuvwxyz",
    remote_display_name: "حساب دوم",
    remote_username: "mirza2",
  }
  assert.notEqual(accountOriginLabel(CONNECTED), accountOriginLabel(other))
  // A missing account is reported as unknown, never as the first one around.
  assert.equal(accountOriginLabel(undefined), "حساب نامشخص")
})

test("actions offered match what the account can take", () => {
  const connected = accountActionsFor(CONNECTED)
  assert.ok(connected.includes("disconnect"))
  assert.ok(connected.includes("logout"))
  assert.ok(!connected.includes("connect"))

  const idle = accountActionsFor(NEEDS_LOGIN)
  assert.ok(idle.includes("connect"))
  assert.ok(!idle.includes("disconnect"))
  assert.ok(!idle.includes("logout"))

  // A record already gone offers nothing to act on.
  assert.deepEqual(accountActionsFor({ ...CONNECTED, status: "removed" }), [])
})

test("logout is offered only where there is a session to end", () => {
  for (const status of ["connected", "reconnecting"] as const) {
    assert.ok(
      accountActionsFor({ ...CONNECTED, status }).includes("logout") ===
        (status === "connected"),
      `${status} should not offer logout`
    )
  }
})

test("the API's own login methods win over the fallback", () => {
  assert.deepEqual(
    loginMethodsOf({ key: "cat", login_methods: ["phone", "qr"] }),
    ["phone", "qr"]
  )
  assert.deepEqual(loginMethodsOf({ key: "official", login_methods: ["phone"] }), [
    "phone",
  ])
})

test("a server without reported login methods is not guessed from its type", () => {
  assert.deepEqual(loginMethodsOf({ key: "cat", type: "custom" }), [])
  assert.deepEqual(loginMethodsOf({ key: "official", type: "telegram" }), [])
})

test("a method is available only when the API explicitly reports it", () => {
  const server = { key: "official", type: "telegram", login_methods: ["phone"] }
  assert.equal(supportsLoginMethod(server, "qr"), false)
  assert.equal(supportsLoginMethod(server, "phone"), true)
  assert.equal(
    supportsLoginMethod({ key: "official", type: "telegram" }, "phone"),
    false
  )
})

test("wizard server adaptation preserves explicit methods and does not invent missing ones", () => {
  const base = {
    enabled: true,
    health_checkable: true,
    state: "ready",
    authorized: null,
    transport: "",
    last_success_at: null,
    last_error: "",
  }
  const adapted = wizardServersOf([
    { ...base, key: "official", type: "telegram", login_methods: ["phone"] },
    { ...base, key: "cat", type: "custom" },
  ])
  assert.deepEqual(adapted[0].login_methods, ["phone"])
  assert.equal("login_methods" in adapted[1], false)
})

test("account grouping tells several accounts on one server apart", () => {
  const twin = { ...CONNECTED, id: "acc_05hxyzabcdefghijklmnopqrstuvwxyz" }
  const accounts = [CONNECTED, twin, { ...NEEDS_LOGIN, server_key: "cat" }]
  assert.equal(accountsOfServer(accounts, "official").length, 2)
  assert.equal(accountsOfServer(accounts, "cat").length, 1)
  assert.equal(serverHasSeveralAccounts(accounts, "official"), true)
  assert.equal(serverHasSeveralAccounts(accounts, "cat"), false)
})

test("connectedAccounts is exactly the set a reader may act through", () => {
  const accounts = [CONNECTED, NEEDS_LOGIN, DISCONNECTED]
  const live = connectedAccounts(accounts)
  assert.equal(live.length, 1)
  assert.equal(live[0]?.id, CONNECTED.id)
})
