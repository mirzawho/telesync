// The channel workspace's account selection, as URL state.
//
// It is the same idea as `channel-server-filter.ts` — a repeated `account` query
// parameter, an empty selection meaning "all" — but it is a separate decision
// with its own default. A server selection that is empty means every server; an
// account selection that is empty means every **connected** account, because
// that is what the reader is asking to work with. The two are never collapsed
// into one: the server filter says which network, this says which of the
// reader's own identities on it.

import type { TelegramAccount } from "@/lib/types"
import { accountTitle } from "@/lib/telegram-account"

/** Parse repeated `account` query params as opaque account ids. */
export function parseAccountSelection(
  params: URLSearchParams,
  availableIds: readonly string[]
): string[] {
  const available = new Set(availableIds)
  const selected = [
    ...new Set(
      params
        .getAll("account")
        .map((id) => id.trim())
        .filter((id) => id && available.has(id))
    ),
  ]
  return available.size > 0 && [...available].every((id) => selected.includes(id))
    ? []
    : selected
}

/**
 * Migrate the old Explorer `server=` identity selector without guessing.
 *
 * In the Explorer a legacy server key used to stand in for an account filter.
 * Translate it to the union of all of the reader's own accounts on that
 * server. This preserves the old result set without picking an arbitrary
 * identity. An explicit `account=` selection takes precedence.
 */
export function migrateLegacyServerSelection(
  current: URLSearchParams,
  accounts: readonly TelegramAccount[]
): { params: URLSearchParams; accountIds: string[] } {
  const params = new URLSearchParams(current)
  const hasExplicitAccount = params.getAll("account").length > 0
  const explicit = parseAccountSelection(params, accounts.map((account) => account.id))
  const legacyServers = [...new Set(params.getAll("server").map((key) => key.trim()).filter(Boolean))]
  params.delete("server")

  if (hasExplicitAccount) {
    return { params: writeAccountSelection(params, explicit), accountIds: explicit }
  }

  const migrated: string[] = []
  for (const key of legacyServers) {
    const matches = accounts.filter((account) => account.server_key === key)
    migrated.push(...matches.map((account) => account.id))
  }
  const accountIds = [...new Set(migrated)]
  return {
    params: writeAccountSelection(params, accountIds),
    accountIds,
  }
}

/** Empty means no backend restriction; otherwise ask for these opaque IDs. */
export function accountQuery(
  accountIds: readonly string[]
): Record<string, string> {
  const ids = [...new Set(accountIds.filter(Boolean))]
  return ids.length ? { account_ids: ids.join(",") } : {}
}

/**
 * Empty selection means all; unchecking its last id returns to all.
 *
 * The id is opaque and comes from the account listing, so a value a reader
 * could type into the address bar is simply not in `availableIds` and drops
 * out here rather than selecting somebody else's account.
 */
export function toggleAccountSelection(
  selected: readonly string[],
  id: string,
  availableIds: readonly string[]
): string[] {
  const all = [...new Set(availableIds)]
  if (selected.length === 0) {
    return all.length === 1 && all[0] === id ? [] : [id]
  }
  const next = selected.includes(id)
    ? selected.filter((value) => value !== id)
    : [...selected, id]
  const normalized = [...new Set(next)]
  return all.length > 0 && all.every((value) => normalized.includes(value))
    ? []
    : normalized
}

/** Write selection while leaving every unrelated query parameter untouched. */
export function writeAccountSelection(
  current: URLSearchParams,
  selected: readonly string[]
): URLSearchParams {
  const next = new URLSearchParams(current)
  next.delete("account")
  for (const id of new Set(selected)) next.append("account", id)
  return next
}

/**
 * selectableAccounts is what the account control offers.
 *
 * Every account the reader owns is offered, connected or not: an account that
 * dropped its session is still theirs and they need to be able to find its
 * channels to see why nothing works. Which of those are actually reachable is
 * a separate question, answered by `scopeToConnectedAccounts`.
 */
export function selectableAccounts(
  accounts: readonly TelegramAccount[]
): TelegramAccount[] {
  return [...accounts]
}

/** Search option labels without coupling the selector to server health. */
export function searchAccounts(
  accounts: readonly TelegramAccount[],
  query: string
): TelegramAccount[] {
  const needle = query.trim().toLocaleLowerCase("fa")
  if (!needle) return [...accounts]
  return accounts.filter((account) =>
    `${accountTitle(account)} ${account.server_key}`
      .toLocaleLowerCase("fa")
      .includes(needle)
  )
}

// defaultAccountIds is the selection a reader starts from: every connected
// account, which is the set they can actually work with.
export function defaultAccountIds(
  accounts: readonly TelegramAccount[]
): string[] {
  return accounts.map((account) => account.id)
}
