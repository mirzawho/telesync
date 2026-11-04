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

// DOWNLOAD_TIMEOUT_MS bounds one download. A large document legitimately takes
// a while, so it is far longer than a page's own calls.
const DOWNLOAD_TIMEOUT_MS = 300_000

/**
 * downloadMedia fetches one attachment with the panel's bearer credential and
 * asks the browser to save it under the given name.
 *
 * A plain <a href> cannot carry the Authorization header, and putting the token
 * in the URL would leak it into history, logs and referrers. The bytes are
 * therefore fetched the same way the player fetches them, and only then does
 * the save happen — under the name the sender gave the file, passed in by the
 * caller rather than guessed at here.
 */
export async function downloadMedia(
  path: string,
  filename: string,
  signal?: AbortSignal
): Promise<void> {
  const isBrowser = typeof window !== "undefined"
  if (!isBrowser) return
  const token = getToken()
  if (!token) throw new Error("unauthorized")

  await acquire(isBrowser)
  let objectUrl = ""
  try {
    const response = await fetch(
      new URL(API_BASE + path, window.location.origin),
      {
        headers: { Authorization: `Bearer ${token}` },
        signal: signal
          ? AbortSignal.any([signal, AbortSignal.timeout(DOWNLOAD_TIMEOUT_MS)])
          : AbortSignal.timeout(DOWNLOAD_TIMEOUT_MS),
      }
    )
    if (!response.ok) throw new Error(`media_error_${response.status}`)
    objectUrl = URL.createObjectURL(await response.blob())
    const anchor = document.createElement("a")
    anchor.href = objectUrl
    anchor.download = filename
    document.body.appendChild(anchor)
    anchor.click()
    anchor.remove()
    // The blob is only released after the save has been handed to the browser;
    // letting it go on the same tick has been known to lose the file.
    setTimeout(() => URL.revokeObjectURL(objectUrl), 60_000)
    objectUrl = ""
  } finally {
    release(isBrowser)
    if (objectUrl) URL.revokeObjectURL(objectUrl)
  }
}
