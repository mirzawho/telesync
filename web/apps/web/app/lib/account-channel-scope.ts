// Which channels one reader is looking at, and which account each came through.
//
// The rule this module enforces is the v0.0.5 rule and it is not a filter: the
// channel list shows the channels reachable through **the signed-in user's own
// accounts**. Being an administrator never widens it. A channel row without an
// owning account is a quarantined legacy record and belongs to nobody's list.
//
// The second job is distinguishing origin. `UNIQUE (account_id, channel_id)`
// means two of one user's accounts can hold the same remote channel as two
// separate rows, and two of *different* users' accounts certainly can. A row
// must never be able to pass for another account's row, so the identity a row
// travels under is its account id and nothing else.

import type { Channel, TelegramAccount } from "@/lib/types"

// channelAccountId is the owning account of one channel row. Empty means the
// row carries no owner — legacy, quarantined, never presented as anybody's.
export function channelAccountId(channel: {
  account_id?: string
}): string {
  return (channel.account_id ?? "").trim()
}

// hasOwner is whether a row is bound to a real account at all.
export function hasOwner(channel: { account_id?: string }): boolean {
  return channelAccountId(channel) !== ""
}

// ownedAccountIds is the set of account ids a reader owns, as the panel must
// gate on it. Only ids the API actually returned are in here: an id nobody
// vouched for grants nothing.
export function ownedAccountIds(
  accounts: readonly TelegramAccount[]
): Set<string> {
  return new Set(
    accounts.map((account) => account.id).filter((id) => typeof id === "string" && id !== "")
  )
}

/**
 * scopeToOwnAccounts is the ownership gate.
 *
 * It keeps a channel only when the row names an account this reader owns. An
 * unowned row is dropped rather than shown with a caveat: the panel must not
 * present a quarantined legacy channel as though it were reachable, and it
 * certainly must not show it because the reader happens to be an administrator.
 */
export function scopeToOwnAccounts(
  channels: readonly Channel[],
  accounts: readonly TelegramAccount[]
): Channel[] {
  const owned = ownedAccountIds(accounts)
  return channels.filter((channel) => owned.has(channelAccountId(channel)))
}

// connectedAccountIds is the subset a reader may act through right now.
export function connectedAccountIds(
  accounts: readonly TelegramAccount[]
): Set<string> {
  return new Set(
    accounts
      .filter((account) => account.status === "connected")
      .map((account) => account.id)
  )
}

/**
 * scopeToConnectedAccounts narrows to the channels reachable *now*.
 *
 * Kept separate from `scopeToOwnAccounts` because the two answer different
 * questions: a channel on a disconnected account is still the reader's own
 * channel and still shows in their inventory, it simply cannot be worked with
 * until the account is back.
 */
export function scopeToConnectedAccounts(
  channels: readonly Channel[],
  accounts: readonly TelegramAccount[]
): Channel[] {
  const live = connectedAccountIds(accounts)
  return channels.filter((channel) => live.has(channelAccountId(channel)))
}

// accountOriginKey is the identity a channel row travels under, used for React
// keys and for telling two rows apart.
//
// The account id is the answer. When a row has none it falls back to the pair
// that still distinguishes it on this installation, and the fallback is
// deliberately namespaced so a legacy row can never collide with a real
// account id.
export function accountOriginKey(channel: {
  id: number
  account_id?: string
  client_key?: string
  channel_id?: number
}): string {
  const account = channelAccountId(channel)
  if (account) return account
  return `legacy:${channel.client_key ?? ""}:${channel.channel_id ?? channel.id}`
}

/**
 * duplicateOriginGroups collects the remote channels that appear more than once
 * in a list, keyed by the pair that identifies the remote channel.
 *
 * A duplicate is not an error — it is exactly the contract's "two users
 * independently subscribe to the same remote channel", and within one user it
 * is two accounts holding the same channel. It only means the UI owes the
 * reader a way to tell the rows apart.
 */
export function duplicateOriginGroups(
  channels: readonly Channel[]
): Map<string, Channel[]> {
  const byRemote = new Map<string, Channel[]>()
  for (const channel of channels) {
    const key = `${channel.client_key ?? ""}:${channel.channel_id ?? channel.id}`
    const rows = byRemote.get(key)
    if (rows) rows.push(channel)
    else byRemote.set(key, [channel])
  }
  const duplicates = new Map<string, Channel[]>()
  for (const [key, rows] of byRemote) {
    if (rows.length > 1) duplicates.set(key, rows)
  }
  return duplicates
}

// needsAccountOrigin distinguishes whether the list owes the reader an account
// label on every row. It is true as soon as one account could be mistaken for
// another: several accounts, or one duplicate remote channel.
export function needsAccountOrigin(
  channels: readonly Channel[],
  accounts: readonly TelegramAccount[]
): boolean {
  if (accounts.length > 1) return true
  return duplicateOriginGroups(channels).size > 0
}

// needsAccountControl is when the workspace must offer an account choice beside
// the server filter.
//
// The control earns its place only where the server filter cannot say which
// account acts: two accounts on one server. One account per server is already
// said by the server filter, and a control that selects between things the
// filter already separates is noise on a 390px screen.
export function needsAccountControl(
  accounts: readonly TelegramAccount[],
  serverKeys?: readonly string[]
): boolean {
  const keys =
    serverKeys ?? [...new Set(accounts.map((account) => account.server_key))]
  return keys.some(
    (key) => accounts.filter((account) => account.server_key === key).length > 1
  )
}

// accountOf resolves a channel row to the account record that owns it. It
// returns undefined for a row whose account the reader does not have — which is
// the honest answer, and never falls back to "the first account" or to the
// server key.
export function accountOf(
  channel: { account_id?: string },
  accounts: readonly TelegramAccount[]
): TelegramAccount | undefined {
  const id = channelAccountId(channel)
  if (!id) return undefined
  return accounts.find((account) => account.id === id)
}

/**
 * authoritativeAccountFor is the account a selected channel is worked with
 * through.
 *
 * This is the seam the product names explicitly: the active account comes from
 * the channel's own record, never from whichever server filter is on screen. A
 * filter is a view over a list; it is not authority over a row.
 */
export function authoritativeAccountFor(
  channel: { account_id?: string },
  accounts: readonly TelegramAccount[]
): TelegramAccount | undefined {
  return accountOf(channel, accounts)
}

// hasAccountsConnected is the question behind the workspace's empty state: a
// reader with no connected account must be told to add one, not shown a
// permission refusal that would send them to an administrator.
export function hasAccountsConnected(
  accounts: readonly TelegramAccount[]
): boolean {
  return accounts.some((account) => account.status === "connected")
}
