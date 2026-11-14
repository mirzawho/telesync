// The one place the personal-account routes are named.
//
// The contract fixes these paths (`V005-ACCOUNT-CONTRACTS.md` §6.1) and the
// panel may not invent one beside them. Writing them out once means a call site
// cannot quietly start addressing an account by username or phone, and a route
// change is one edit rather than nine.

import { apiRequest, type PageMeta } from "@/lib/api"
import type { LoginAttempt, LoginMethod, TelegramAccount } from "@/lib/types"

const ROOT = "/me/telegram-accounts"

export const accountApi = {
  list: () => `${ROOT}`,
  get: (accountID: string) =>
    `${ROOT}/${encodeURIComponent(accountID)}`,
  loginStart: () => `${ROOT}/login/start`,
  loginStatus: (attemptID: string) =>
    `${ROOT}/login/${encodeURIComponent(attemptID)}`,
  loginCode: (attemptID: string) =>
    `${ROOT}/login/${encodeURIComponent(attemptID)}/code`,
  loginPassword: (attemptID: string) =>
    `${ROOT}/login/${encodeURIComponent(attemptID)}/password`,
  loginCancel: (attemptID: string) =>
    `${ROOT}/login/${encodeURIComponent(attemptID)}/cancel`,
  disconnect: (accountID: string) =>
    `${ROOT}/${encodeURIComponent(accountID)}/disconnect`,
  logout: (accountID: string) =>
    `${ROOT}/${encodeURIComponent(accountID)}/logout`,
  remove: (accountID: string) => `${ROOT}/${encodeURIComponent(accountID)}`,
  channelDetail: (accountID: string, channelID: string) =>
    `/telegram/accounts/${encodeURIComponent(accountID)}/channels/${encodeURIComponent(channelID)}`,
  channelList: (accountID: string) =>
    `/telegram/accounts/${encodeURIComponent(accountID)}/channels`,
  channelFind: (accountID: string) =>
    `/telegram/accounts/${encodeURIComponent(accountID)}/channels/find`,
  channelJoin: (accountID: string, channelID: string) =>
    `/telegram/accounts/${encodeURIComponent(accountID)}/channels/${encodeURIComponent(channelID)}/join`,
  messageCopy: (accountID: string) =>
    `/telegram/accounts/${encodeURIComponent(accountID)}/messages/copy`,
} as const

export interface AccountTelegramChannel {
  id: number
  title: string
  username?: string
  has_avatar?: boolean
  permissions: {
    new: boolean
    edit: boolean
    delete: boolean
    read: boolean
  }
}

// Load every page because the destination picker searches the account's full
// dialog list locally. A later-page failure remains an error; callers must not
// mistake a partial list for a complete result.
export async function listAccountChannels(
  token: string,
  accountID: string,
  signal?: AbortSignal,
): Promise<AccountTelegramChannel[]> {
  const channels: AccountTelegramChannel[] = []
  let page = 1
  for (;;) {
    const { data, meta } = await apiRequest<{
      channels?: AccountTelegramChannel[]
    }>("GET", accountApi.channelList(accountID), {
      token,
      query: { page, per_page: 100, capability: "send" },
      signal,
    })
    if (Array.isArray(data?.channels)) channels.push(...data.channels)
    const nextPage = meta.next_page ?? (meta.has_more ? page + 1 : undefined)
    if (!nextPage || nextPage <= page) return channels
    page = nextPage
  }
}

// StartLoginRequest is what begins one login attempt. `server_key` names the
// server definition; `account_id` is never sent to start one, because an
// account does not exist until the attempt succeeds. `phone_number` is typed by
// the reader and is used for this attempt only.
export interface StartLoginRequest {
  server_key: string
  method: LoginMethod
  phone_number?: string
}

export interface AccountListResult {
  accounts: TelegramAccount[]
  meta: PageMeta
}

export async function listMyAccounts(
  token: string,
  signal?: AbortSignal
): Promise<AccountListResult> {
  const { data, meta } = await apiRequest<{ accounts?: TelegramAccount[] }>(
    "GET",
    accountApi.list(),
    { token, signal }
  )
  return { accounts: data?.accounts ?? [], meta }
}

export async function getMyAccount(
  token: string,
  accountID: string,
  signal?: AbortSignal
): Promise<TelegramAccount> {
  const { data } = await apiRequest<{ account?: TelegramAccount }>(
    "GET",
    accountApi.get(accountID),
    { token, signal }
  )
  return data.account as TelegramAccount
}

export async function startLogin(
  token: string,
  request: StartLoginRequest,
  signal?: AbortSignal
): Promise<LoginAttempt> {
  const { data } = await apiRequest<{ attempt?: LoginAttempt }>(
    "POST",
    accountApi.loginStart(),
    { token, body: request, signal }
  )
  return data.attempt as LoginAttempt
}

export async function loginStatus(
  token: string,
  attemptID: string,
  signal?: AbortSignal
): Promise<LoginAttempt> {
  const { data } = await apiRequest<{ attempt?: LoginAttempt }>(
    "GET",
    accountApi.loginStatus(attemptID),
    { token, signal }
  )
  return data.attempt as LoginAttempt
}

// submitCode sends the code the reader typed. The code is transient: it is in
// this request and nowhere else.
export async function submitLoginCode(
  token: string,
  attemptID: string,
  code: string,
  signal?: AbortSignal
): Promise<{ attempt: LoginAttempt; account?: TelegramAccount }> {
  const { data } = await apiRequest<{
    attempt?: LoginAttempt
    account?: TelegramAccount
  }>("POST", accountApi.loginCode(attemptID), {
    token,
    body: { code },
    signal,
  })
  return {
    attempt: data.attempt as LoginAttempt,
    account: data.account,
  }
}

// submitPassword sends a 2FA password. The caller clears its own field in the
// same act (see `reduceLogin`'s `submit_password`); this function keeps no copy
// and the body is not logged, not cached and not retried.
export async function submitLoginPassword(
  token: string,
  attemptID: string,
  password: string,
  signal?: AbortSignal
): Promise<{ attempt: LoginAttempt; account?: TelegramAccount }> {
  const { data } = await apiRequest<{
    attempt?: LoginAttempt
    account?: TelegramAccount
  }>("POST", accountApi.loginPassword(attemptID), {
    token,
    body: { password },
    signal,
  })
  return {
    attempt: data.attempt as LoginAttempt,
    account: data.account,
  }
}

export async function cancelLogin(
  token: string,
  attemptID: string,
  signal?: AbortSignal
): Promise<void> {
  await apiRequest("POST", accountApi.loginCancel(attemptID), { token, signal })
}

export async function disconnectAccount(
  token: string,
  accountID: string
): Promise<TelegramAccount> {
  const { data } = await apiRequest<{ account?: TelegramAccount }>(
    "POST",
    accountApi.disconnect(accountID),
    { token }
  )
  return data.account as TelegramAccount
}

export async function logoutAccount(
  token: string,
  accountID: string
): Promise<TelegramAccount> {
  const { data } = await apiRequest<{ account?: TelegramAccount }>(
    "POST",
    accountApi.logout(accountID),
    { token }
  )
  return data.account as TelegramAccount
}

export async function removeAccount(
  token: string,
  accountID: string
): Promise<void> {
  await apiRequest("DELETE", accountApi.remove(accountID), { token })
}
