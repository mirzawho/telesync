import { API_BASE } from "@/lib/api"
import { getToken } from "@/lib/session"

// The media route needs the bearer token, and a browser sends none on an
// <img src> or <video src>. The panel therefore asks the API itself — once per
// message, in parallel up to BROWSER_PARALLEL — and shows the bytes through a
// blob URL, the same approach ChannelAvatar uses for avatars.
const TIMEOUT_MS = 30_000

// BROWSER_PARALLEL caps the simultaneous downloads: attachments can be 32 MiB
// each, and a page of photos must not open twenty at once.
const BROWSER_PARALLEL = 4
let active = 0
const waiting: (() => void)[] = []

async function acquire(isBrowser: boolean) {
  if (!isBrowser) return
  while (active >= BROWSER_PARALLEL) {
    await new Promise<void>((resolve) => waiting.push(resolve))
  }
  active++
}

function release(isBrowser: boolean) {
  if (!isBrowser) return
  active--
  waiting.shift()?.()
}

/**
 * fetchMedia asks one media URL for its bytes.
 *
 * In the browser it resolves to a blob URL (empty string when the route has
 * no store available) and the caller owns the URL; anywhere else — the dev
 * server's client build, a prerender — it resolves to the path itself.
 */
export async function fetchMedia(
  path: string,
  signal?: AbortSignal
): Promise<string> {
  const isBrowser = typeof window !== "undefined"
  if (!isBrowser) return path
  const token = getToken()
  if (!token) throw new Error("unauthorized")

  await acquire(isBrowser)
  try {
    const response = await fetch(
      new URL(API_BASE + path, window.location.origin),
      {
        headers: { Authorization: `Bearer ${token}` },
        signal: signal
          ? AbortSignal.any([signal, AbortSignal.timeout(TIMEOUT_MS)])
          : AbortSignal.timeout(TIMEOUT_MS),
      }
    )
    if (response.status === 404) return ""
    if (!response.ok) throw new Error(`media_error_${response.status}`)
    return URL.createObjectURL(await response.blob())
  } finally {
    release(isBrowser)
  }
}

export function releaseMedia(url: string) {
  if (typeof window !== "undefined" && url.startsWith("blob:")) {
    URL.revokeObjectURL(url)
  }
}
