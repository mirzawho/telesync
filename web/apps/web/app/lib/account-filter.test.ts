import assert from "node:assert/strict"
import { test } from "node:test"

import {
  defaultAccountIds,
  migrateLegacyServerSelection,
  parseAccountSelection,
  accountQuery,
  selectableAccounts,
  searchAccounts,
  toggleAccountSelection,
  writeAccountSelection,
} from "@/lib/account-filter"
import type { TelegramAccount } from "@/lib/types"

const A = "acc_aaaaaaaaaaaaaaaaaaaaaaaaaa"
const B = "acc_bbbbbbbbbbbbbbbbbbbbbbbbbb"
const C = "acc_cccccccccccccccccccccccccc"

function account(id: string, status = "connected"): TelegramAccount {
  return {
    id,
    server_key: "official",
    network_type: "telegram",
    status,
    remote_display_name: id.slice(4, 8),
  }
}

test("an empty selection means every account", () => {
  assert.deepEqual(parseAccountSelection(new URLSearchParams(), [A, B]), [])
  assert.deepEqual(toggleAccountSelection([], A, [A, B]), [A])
  // Unchecking the only selection returns to all.
  assert.deepEqual(toggleAccountSelection([A], A, [A, B]), [])
})

test("an id that is not one of the reader's own is dropped", () => {
  // This is the whole point of the selection being built from the listing: a
  // value a reader could paste into the address bar selects nobody's account
  // but their own.
  const params = new URLSearchParams([["account", A], ["account", "acc_notmine"]])
  assert.deepEqual(parseAccountSelection(params, [A, B]), [A])
})

test("the selection survives a reload and never touches other query state", () => {
  const current = new URLSearchParams("q=خبر&server=official")
  const written = writeAccountSelection(current, [A, B])
  assert.deepEqual(written.getAll("account"), [A, B])
  assert.equal(written.get("q"), "خبر")
  assert.equal(written.get("server"), "official")

  // Writing a new selection replaces the old one rather than stacking.
  const replaced = writeAccountSelection(written, [C])
  assert.deepEqual(replaced.getAll("account"), [C])
  // Clearing the selection removes the parameter entirely: "all" is not a
  // value, it is the absence of one.
  assert.equal(writeAccountSelection(written, []).has("account"), false)
})

test("selecting every account collapses back to all", () => {
  const all = toggleAccountSelection([A], B, [A, B])
  assert.deepEqual(all, [])
  assert.deepEqual(
    parseAccountSelection(new URLSearchParams(`account=${A}&account=${B}`), [A, B]),
    []
  )
})

test("the default is all accounts regardless of their actual status", () => {
  const accounts = [account(A), account(B, "disconnected"), account(C, "authentication_required")]
  assert.deepEqual(defaultAccountIds(accounts), [A, B, C])
})

test("every owned account is selectable, connected or not", () => {
  // A dropped session is still the reader's account and they need to be able
  // to find its channels to see why nothing works.
  const accounts = [account(A), account(B, "disconnected")]
  assert.deepEqual(selectableAccounts(accounts).map((a) => a.id), [A, B])
})

test("legacy server URLs migrate to every owned account on that server", () => {
  const accounts = [
    account(A),
    { ...account(B), server_key: "cat" },
    { ...account(C), server_key: "cat" },
  ]
  const unique = migrateLegacyServerSelection(
    new URLSearchParams("server=official&q=خبر"),
    accounts
  )
  assert.deepEqual(unique.accountIds, [A])
  assert.equal(unique.params.has("server"), false)
  assert.equal(unique.params.get("q"), "خبر")

  const multiple = migrateLegacyServerSelection(
    new URLSearchParams("server=cat"),
    accounts
  )
  assert.deepEqual(multiple.accountIds, [B, C])
  assert.equal(multiple.params.has("server"), false)
  assert.deepEqual(multiple.params.getAll("account"), [B, C])

  const missing = migrateLegacyServerSelection(
    new URLSearchParams("server=unconfigured"),
    accounts
  )
  assert.deepEqual(missing.accountIds, [])
  assert.equal(missing.params.has("server"), false)
  assert.equal(missing.params.has("account"), false)
})

test("an explicit account URL wins over a legacy server selector", () => {
  const migrated = migrateLegacyServerSelection(
    new URLSearchParams(`server=cat&account=${A}`),
    [account(A), account(B), account(C)]
  )
  assert.deepEqual(migrated.accountIds, [A])
  assert.equal(migrated.params.has("server"), false)
})

test("only an explicit account selection becomes an account_ids backend query", () => {
  assert.deepEqual(accountQuery([]), {})
  assert.deepEqual(accountQuery([A, B]), { account_ids: `${A},${B}` })
  assert.deepEqual(defaultAccountIds([]), [])
})

test("account option search is based on the returned account label or server", () => {
  const accounts = [
    { ...account(A), remote_display_name: "حساب کاری" },
    { ...account(B), remote_display_name: "شخصی", server_key: "cat" },
  ]
  assert.deepEqual(searchAccounts(accounts, "کاری").map((a) => a.id), [A])
  assert.deepEqual(searchAccounts(accounts, "cat").map((a) => a.id), [B])
  assert.deepEqual(searchAccounts(accounts, "not present"), [])
})
