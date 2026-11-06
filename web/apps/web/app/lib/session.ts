import { redirect } from "react-router"

import { requestURL, strip } from "@/path"
import { ApiError, apiRequest, type PageMeta } from "./api"

// The browser's session: the access token the login issued, and the account it
// names. The token is kept in sessionStorage, which is per tab and gone when
// the tab is; a new tab starts from the login page.
//
// The API is the authority on the token: /auth/me is asked on every panel
// navigation, so a token that expired or was revoked ends the session on the
// next click rather than as an error inside a page.

const tokenKey = "telesync.panel.token"

export function getToken(): string | null {
  try {
    return window.sessionStorage.getItem(tokenKey)
  } catch {
    return null
  }
}

export function setToken(token: string | null) {
  try {
    if (token) {
      window.sessionStorage.setItem(tokenKey, token)
    } else {
      window.sessionStorage.removeItem(tokenKey)
    }
  } catch {
    // A browser that refuses storage runs the session for this page only.
  }
}

export interface Me {
  id: number
  fullname: string
  username: string
  is_active: boolean
  created_at: string
  updated_at: string
  access?: {
    channels: boolean
    categories: boolean
    users: boolean
    servers: boolean
    // automation is the "automation.view" global permission: the automation
    // area and its histories. A summary that omits the flag grants nothing.
    automation: boolean
  }
}

export interface PanelSession {
  token: string
  me: Me
}

export const LOGIN_REDIRECT_TO = "/login"

// requireSession is the guard every page that needs an account calls first,
// in its loader. No token sends the browser straight to the login page; with
// one, /auth/me decides whether it is still usable. An unreachable API is a
// page-level error, not a logout: a network blip must not discard a valid
// session.
//
// The path is the router's own, without the basename (/login, /dashboard):
// react-router adds the basename when it navigates, so a path that already
// carried it would arrive at /admin/admin/....
export async function requireSession(request: Request): Promise<PanelSession> {
  const url = requestURL(request)
  const redirectTo = strip(`${url.pathname}${url.search}`)
  const token = getToken()
  if (!token) {
    throw redirect(
      `${LOGIN_REDIRECT_TO}?next=${encodeURIComponent(redirectTo)}`
    )
  }

  let me: Me
  try {
    me = (await apiRequest<Me>("GET", "/auth/me", { token })).data
  } catch (error) {
    if (error instanceof ApiError && error.isAuthLost) {
      setToken(null)
      throw redirect(
        `${LOGIN_REDIRECT_TO}?expired=1&next=${encodeURIComponent(redirectTo)}`
      )
    }
    throw error
  }
  return { token, me }
}

// listAll follows the API's pagination to one limit, for a view that needs a
// total (the dashboard's counts) rather than one page.
export async function listAll<T>(
  token: string,
  path: string,
  pick: (data: unknown) => T[],
  query: Record<string, string | number | undefined> = {},
  limit = 5
): Promise<{ rows: T[]; meta: PageMeta }> {
  const rows: T[] = []
  let meta: PageMeta = {}
  for (let page = 1; page <= limit; page++) {
    const result = await apiRequest<unknown>("GET", path, {
      token,
      query: { ...query, page, per_page: 100 },
    })
    rows.push(...pick(result.data))
    meta = result.meta
    if (!meta.has_more) {
      break
    }
  }
  return { rows, meta }
}
