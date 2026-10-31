// The app is served under a basename (/admin/). react-router's own Link, Form
// and navigate() add it; anywhere that names a path itself — a redirect, the
// login page's `next`, a hidden field — goes through strip() first, so a path
// never carries the basename twice.

const BASE = import.meta.env.BASE_URL
const BASE_NO_SLASH = BASE.endsWith("/") ? BASE.slice(0, -1) : BASE

// strip is a browser path as one of the router's own: /admin/dashboard becomes
// /dashboard, and a path that has no basename is left as it is.
export function strip(href: string): string {
  if (href === BASE_NO_SLASH) return "/"
  if (href.startsWith(BASE)) return "/" + href.slice(BASE.length)
  return href
}

// requestURL reads a loader's request URL. The browser hands loaders a
// relative URL (the router strips the origin), which `new URL()` refuses on its
// own: the window's origin puts it back.
export function requestURL(request: Request): URL {
  return new URL(request.url, window.location.origin)
}
