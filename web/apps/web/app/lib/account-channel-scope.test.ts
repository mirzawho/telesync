import assert from "node:assert/strict"
import { test } from "node:test"

import {
  accountOf,
  accountOriginKey,
  authoritativeAccountFor,
  channelAccountId,
  connectedAccountIds,
  duplicateOriginGroups,
  hasAccountsConnected,
  hasOwner,
  needsAccountControl,
  needsAccountOrigin,
  ownedAccountIds,
  scopeToConnectedAccounts,
  scopeToOwnAccounts,
} from "@/lib/account-channel-scope"
import type { Channel, TelegramAccount } from "@/lib/types"

const ACCOUNT_A: TelegramAccount = {
  id: "acc_aaaaaaaaaaaaaaaaaaaaaaaaaa",
  server_key: "official",
  network_type: "telegram",
  remote_display_name: "حساب نخست",
  status: "connected",
}

const ACCOUNT_B: TelegramAccount = {
  id: "acc_bbbbbbbbbbbbbbbbbbbbbbbbbb",
  server_key: "official",
  network_type: "telegram",
  remote_display_name: "حساب دوم",
  status: "connected",
}

const ACCOUNT_CAT: TelegramAccount = {
  id: "acc_cccccccccccccccccccccccccc",
  server_key: "cat",
  network_type: "custom",
  remote_display_name: "حساب اختصاصی",
  status: "disconnected",
}

function channel(overrides: Partial<Channel> = {}): Channel {
  return {
    id: 1,
    client_key: "official",
    account_id: ACCOUNT_A.id,
    channel_id: 100,
    type: "channel",
    title: "کانال",
    avatar_available: false,
    membership_status: "member",
    participants_count: null,
    created_at: "",
    updated_at: "",
    last_synced_at: "",
    ...overrides,
  }
}

test("the channel list is the reader's own channels and nothing wider", () => {
  const mine = channel({ id: 1 })
  const someoneElses = channel({ id: 2, account_id: "acc_someoneelse000000000000" })
  const rows = scopeToOwnAccounts([mine, someoneElses], [ACCOUNT_A])
  assert.deepEqual(rows.map((row) => row.id), [1])
})

test("being an administrator never widens the list", () => {
  // The API is the only source of accounts. A caller who owns no account sees
  // no channel, whatever role the panel thinks they hold.
  const rows = scopeToOwnAccounts([channel()], [])
  assert.deepEqual(rows, [])
})

test("a row with no owning account is quarantined, not shown with a caveat", () => {
  const legacy = channel({ id: 5, account_id: undefined })
  const alsoLegacy = channel({ id: 6, account_id: "  " })
  assert.deepEqual(scopeToOwnAccounts([legacy, alsoLegacy], [ACCOUNT_A]), [])
  assert.equal(hasOwner(legacy), false)
  assert.equal(hasOwner(channel()), true)
})

test("an id nobody vouched for grants nothing", () => {
  // A channel claiming an account id is not enough: the reader must own that
  // account in the listing the API gave.
  const impostor = channel({ account_id: "acc_neverreturnedbytheapi00000" })
  assert.deepEqual(scopeToOwnAccounts([impostor], [ACCOUNT_A]), [])
})

test("owned and connected are different questions", () => {
  const onDisconnected = channel({ id: 3, account_id: ACCOUNT_CAT.id })
  const all = scopeToOwnAccounts([onDisconnected], [ACCOUNT_A, ACCOUNT_CAT])
  assert.deepEqual(all.map((row) => row.id), [3], "it is still their channel")

  const live = scopeToConnectedAccounts([onDisconnected], [ACCOUNT_A, ACCOUNT_CAT])
  assert.deepEqual(live, [], "but it is not reachable right now")

  assert.deepEqual([...connectedAccountIds([ACCOUNT_A, ACCOUNT_CAT])], [ACCOUNT_A.id])
})

test("two rows for the same remote channel are distinguished, never merged", () => {
  const first = channel({ id: 1, account_id: ACCOUNT_A.id, channel_id: 100 })
  const second = channel({ id: 2, account_id: ACCOUNT_B.id, channel_id: 100 })
  const rows = [first, second]

  // Distinct keys: a React list could not collapse them.
  assert.notEqual(accountOriginKey(first), accountOriginKey(second))
  // Distinct groups: the reader is owed an account label on each.
  assert.equal(duplicateOriginGroups(rows).size, 1)
  assert.equal(needsAccountOrigin(rows, [ACCOUNT_A, ACCOUNT_B]), true)
})

test("the origin key falls back safely for a legacy row", () => {
  const legacy = channel({ id: 7, account_id: undefined, client_key: "cat", channel_id: 55 })
  assert.equal(accountOriginKey(legacy), "legacy:cat:55")
  // The fallback can never collide with a real account id.
  assert.notEqual(accountOriginKey(legacy), accountOriginKey(channel()))
})

test("the account control appears only when a choice is really needed", () => {
  assert.equal(needsAccountControl([ACCOUNT_A], ["official"]), false)
  assert.equal(
    needsAccountControl([ACCOUNT_A, ACCOUNT_B], ["official"]),
    true,
    "two accounts on one server must be told apart"
  )
  assert.equal(
    needsAccountControl([ACCOUNT_A, ACCOUNT_CAT], ["official", "cat"]),
    false,
    "one account per server is already said by the server filter"
  )
})

test("an account label is owed as soon as there is more than one account", () => {
  assert.equal(needsAccountOrigin([], [ACCOUNT_A]), false)
  assert.equal(needsAccountOrigin([], [ACCOUNT_A, ACCOUNT_B]), true)
})

test("the authoritative account comes from the record, never from a filter", () => {
  const row = channel({ account_id: ACCOUNT_B.id, client_key: "official" })
  // Even though the row's client_key points at a server ACCOUNT_A is also on,
  // the account is the one the record names.
  const account = authoritativeAccountFor(row, [ACCOUNT_A, ACCOUNT_B])
  assert.equal(account?.id, ACCOUNT_B.id)
})

test("an unresolvable account is reported as none, never as the first one", () => {
  assert.equal(accountOf(channel({ account_id: "acc_missing00000000000000" }), [ACCOUNT_A]), undefined)
  assert.equal(accountOf(channel({ account_id: undefined }), [ACCOUNT_A]), undefined)
})

test("the empty state asks whether any account is connected at all", () => {
  assert.equal(hasAccountsConnected([ACCOUNT_A]), true)
  assert.equal(hasAccountsConnected([]), false)
  assert.equal(hasAccountsConnected([ACCOUNT_CAT]), false)
})

test("ownedAccountIds is exactly what the API returned", () => {
  assert.deepEqual(
    [...ownedAccountIds([ACCOUNT_A, ACCOUNT_B, { ...ACCOUNT_A, id: "" }])],
    [ACCOUNT_A.id, ACCOUNT_B.id]
  )
})
