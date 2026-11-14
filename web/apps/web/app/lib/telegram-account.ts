// How one user-owned Telegram account is described, decided without a DOM.
//
// `server-state.ts` does the same job for a server *definition*; this module
// does it for an account, which is the identity that actually acts. The two are
// never the same thing: a server key names a configured backend, an account id
// names one person's authenticated session on it. Everything here reads the
// account record the API returned and turns it into what the panel says.

import type {
  AccountStatus,
  LoginMethod,
  TelegramAccount,
  TelegramServer,
} from "@/lib/types"

// AccountTone is the badge mood an account's status wears.
export type AccountTone = "ok" | "busy" | "warn" | "off" | "bad"

// AccountConnection is the coarse state the list and the pickers reason about.
export type AccountConnection =
  | "connected"
  | "connecting"
  | "needs_login"
  | "disconnected"
  | "error"

export const accountStatusLabels: Record<string, string> = {
  connected: "متصل",
  connecting: "در حال اتصال",
  reconnecting: "در حال اتصال دوباره",
  disconnected: "قطع",
  authentication_required: "نیازمند ورود دوباره",
  disabled: "غیرفعال",
  error: "خطا",
  removed: "حذف‌شده",
}

export const accountConnectionLabels: Record<AccountConnection, string> = {
  connected: "متصل",
  connecting: "در حال اتصال",
  needs_login: "نیازمند ورود",
  disconnected: "قطع",
  error: "خطا در اتصال",
}

// connectionOf folds the contract's finer status set onto the four states a
// reader acts on. `authentication_required` is its own case because the remedy
// is signing in again, not reconnecting.
export function connectionOf(account: TelegramAccount): AccountConnection {
  const status = account.status
  if (status === "connected") return "connected"
  if (status === "connecting" || status === "reconnecting") return "connecting"
  if (status === "authentication_required") return "needs_login"
  if (status === "disconnected") return "disconnected"
  return "error"
}

export function accountStatusText(account: TelegramAccount): string {
  return (
    accountStatusLabels[account.status] ??
    accountConnectionLabels[connectionOf(account)]
  )
}

export function accountStatusTone(account: TelegramAccount): AccountTone {
  switch (connectionOf(account)) {
    case "connected":
      return "ok"
    case "connecting":
      return "busy"
    case "needs_login":
      return "warn"
    case "disconnected":
      return "off"
    default:
      return "bad"
  }
}

// isAccountConnected is the one question the rest of the panel asks before it
// offers to act through an account. "Needs login" is not connected, however
// healthy the transport looks.
export function isAccountConnected(account: TelegramAccount): boolean {
  return account.status === "connected"
}

// serverLabel names a server definition. `network_type` is the account's own
// report of which network it is on; a key this build has no name for is shown
// as the key itself rather than described as something it may not be.
export function serverLabel(
  serverKey: string,
  networkType?: TelegramAccount["network_type"]
): string {
  if (networkType === "custom") return "سرور اختصاصی"
  if (networkType === "telegram") return "رسمی تلگرام"
  return serverKey
}

// accountTitle is the line a reader recognises the account by: their own
// display name first, then the handle, then the masked phone, then the server.
// Nothing here may invent a name the API did not send.
export function accountTitle(account: TelegramAccount): string {
  return (
    account.remote_display_name?.trim() ||
    (account.remote_username ? `@${account.remote_username}` : "") ||
    account.phone_number_masked?.trim() ||
    serverLabel(account.server_key, account.network_type)
  )
}

// accountSubtitle is the second line: whichever of handle and masked phone did
// not carry the title, so a row always shows both when it has both.
export function accountSubtitle(account: TelegramAccount): string {
  const parts: string[] = []
  if (account.remote_display_name?.trim() && account.remote_username) {
    parts.push(`@${account.remote_username}`)
  }
  if (account.phone_number_masked?.trim()) {
    parts.push(account.phone_number_masked.trim())
  }
  return parts.join(" — ")
}

// accountOriginLabel is what distinguishes two rows in the channel list that
// are the same remote channel reached through two different accounts. It names
// the account, never the server: one server can hold several accounts and a
// server key would not tell them apart.
export function accountOriginLabel(
  account: TelegramAccount | undefined
): string {
  if (!account) return "حساب نامشخص"
  return accountTitle(account)
}

// AccountAction is one thing the panel offers to do to an account.
export type AccountAction =
  | "connect"
  | "disconnect"
  | "logout"
  | "remove"
  | "details"
  | "channels"

// accountActionsFor lists what is offered for an account in its current state.
//
// The destructive pair is deliberately narrow: `remove` deletes the record and
// is offered whenever the account exists, `logout` ends the remote session and
// is offered only for one that has a session to end. Disconnecting an account
// that is not connected would be a lie about an effect.
export function accountActionsFor(account: TelegramAccount): AccountAction[] {
  const connected = isAccountConnected(account)
  const gone = account.status === "removed"
  const actions: AccountAction[] = []
  if (!gone && !connected) actions.push("connect")
  if (connected) actions.push("disconnect")
  if (connected) actions.push("logout")
  if (!gone) actions.push("details")
  if (!gone) actions.push("channels")
  if (!gone) actions.push("remove")
  return actions
}

// loginMethodsOf is what a server definition offers for signing in.
//
// The API's own `login_methods` is authoritative when it says anything. When it
// says nothing, the panel falls back to a rule it can defend: QR is offered on
// the official Telegram network, and a custom backend gets phone only — the
// handoff records custom-backend QR as unverified, and hiding a method never
// blocks the one that works. A method the API explicitly ruled out is never
// shown, whatever the fallback would have said.
export function loginMethodsOf(server: {
  key?: string
  type?: string
  login_methods?: LoginMethod[]
  network_type?: TelegramAccount["network_type"]
}): LoginMethod[] {
  const reported = server.login_methods
  if (Array.isArray(reported)) {
    return [...new Set(reported)]
  }
  return []
}

// Preserve the backend's capability report while adapting server rows for the
// account wizard. Missing capabilities remain missing; the UI never infers them.
export function wizardServersOf(
  servers: readonly TelegramServer[]
): {
  key: string
  type: string
  network_type: TelegramAccount["network_type"]
  login_methods?: TelegramServer["login_methods"]
}[] {
  return servers.map((server) => ({
    key: server.key,
    type: server.type,
    network_type: server.type === "custom" ? "custom" : "telegram",
    ...(Array.isArray(server.login_methods)
      ? { login_methods: server.login_methods }
      : {}),
  }))
}

// supportsLoginMethod answers the one question the wizard asks before it shows
// a step.
export function supportsLoginMethod(
  server: Parameters<typeof loginMethodsOf>[0],
  method: LoginMethod
): boolean {
  return loginMethodsOf(server).includes(method)
}

// accountsOfServer groups one user's accounts by the server definition they sit
// on. The channel explorer's server filter stays a server filter; this is what
// lets the UI tell several accounts on one server apart.
export function accountsOfServer(
  accounts: readonly TelegramAccount[],
  serverKey: string
): TelegramAccount[] {
  return accounts.filter((account) => account.server_key === serverKey)
}

// serverHasSeveralAccounts is when the UI must offer an account control beside
// the server filter: one server key alone would not say which account to use.
export function serverHasSeveralAccounts(
  accounts: readonly TelegramAccount[],
  serverKey: string
): boolean {
  return accountsOfServer(accounts, serverKey).length > 1
}

// connectedAccounts is the set a reader may act through right now.
export function connectedAccounts(
  accounts: readonly TelegramAccount[]
): TelegramAccount[] {
  return accounts.filter(isAccountConnected)
}

// connectedAgoLabel says how long ago an account was last connected, in Persian
// words a reader can act on.
//
// "Never" is a real answer and is said as one: an account that has not
// connected yet is not "some time ago". An unparseable or absent timestamp gets
// the same treatment rather than a fabricated moment.
export function connectedAgoLabel(
  value: string | null | undefined,
  now: number = Date.now()
): string {
  if (!value) return "هرگز متصل نشده"
  const at = Date.parse(value)
  if (!Number.isFinite(at)) return "هرگز متصل نشده"
  const elapsed = now - at
  if (elapsed < 0) return "همین حالا"
  return agoLabel(elapsed)
}

function agoLabel(elapsedMs: number): string {
  const minute = 60_000
  const hour = 60 * minute
  const day = 24 * hour
  if (elapsedMs < minute) return "همین حالا"
  if (elapsedMs < hour) {
    return `${fa(Math.max(1, Math.floor(elapsedMs / minute)))} دقیقه پیش`
  }
  if (elapsedMs < day) {
    return `${fa(Math.floor(elapsedMs / hour))} ساعت پیش`
  }
  if (elapsedMs < 30 * day) {
    return `${fa(Math.floor(elapsedMs / day))} روز پیش`
  }
  return "بیش از یک ماه پیش"
}

function fa(value: number): string {
  return String(value).replace(/[0-9]/g, (d) => "۰۱۲۳۴۵۶۷۸۹"[Number(d)])
}
