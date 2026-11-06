import { redirect } from "react-router"

import { ApiError } from "@/lib/api"
import { LOGIN_REDIRECT_TO } from "@/lib/session"
import { requestURL, strip } from "@/path"

// The automation screens share one rule for a failed call: a lost session goes
// back to login with the way back, a refusal becomes a forbidden state, and
// anything else is reported as the service reported it.

export function loginRedirect(request: Request): Response {
  const url = requestURL(request)
  return redirect(
    `${LOGIN_REDIRECT_TO}?expired=1&next=${encodeURIComponent(strip(`${url.pathname}${url.search}`))}`
  )
}

export function authRedirect(url: URL): string {
  return `${LOGIN_REDIRECT_TO}?expired=1&next=${encodeURIComponent(strip(`${url.pathname}${url.search}`))}`
}

// isAuthLost reads an ApiError as "the session is gone", so a loader may throw
// the login redirect instead of rendering a broken page.
export function isAuthLost(error: unknown): boolean {
  return error instanceof ApiError && error.isAuthLost
}

export function isForbidden(error: unknown): boolean {
  return error instanceof ApiError && error.isForbidden
}

export function codeOf(error: unknown): string {
  return error instanceof ApiError ? error.code : ""
}

// automationAccessDenied reads the session's own `me.access.automation` flag,
// which is the automation.view global permission. A restricted administrator is
// answered with an explicit denial here, before any call is made: a page that
// fell through to its calls and rendered what came back would show an empty
// screen and say nothing about why.
//
// An account whose summary omits the flag is not denied by it: the API remains
// the authority and its own refusal is still reported.
export function automationAccessDenied(me: {
  access?: { automation?: boolean }
}): string | null {
  return me.access?.automation === false
    ? "شما مجوز دسترسی به بخش اتوماسیون را ندارید."
    : null
}
