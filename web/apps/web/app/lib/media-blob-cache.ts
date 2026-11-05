import { API_BASE } from "@/lib/api"

// One shared copy of an attachment's bytes, drawn through an object URL.
//
// The panel's media route needs the bearer token and a browser sends none on an
// <img src>, so a picture is fetched and shown from an object URL. Two panels in
// the publishing dialog draw the same attachment — the editor and the live
// preview — and a picture must not be downloaded twice because two components
// asked for it. One request serves every reader, and the object URL lives until
// the last of them has let go.
//
// Nothing is cached across failures: an attachment that could not be read is
// fetched again the next time it is asked for, because a failure is a fact about
// the moment and not about the file.

const TIMEOUT_MS = 30_000

// BROWSER_PARALLEL caps simultaneous downloads. Attachments can be 32 MiB each
// and a page of photos must not open twenty at once.
const BROWSER_PARALLEL = 4

type Entry = {
  // promise is the one download every reader awaits. It is kept after it
  // settles so a late reader gets the same bytes rather than a second fetch.
  promise: Promise<string>
  // refs counts the readers still showing it. Zero means nobody is.
  refs: number
}

const entries = new Map<string, Entry>()
let active = 0
const waiting: (() => void)[] = []

async function acquire() {
  while (active >= BROWSER_PARALLEL) {
    await new Promise<void>((resolve) => waiting.push(resolve))
  }
  active++
}

function release() {
  active--
  waiting.shift()?.()
}

/**
 * loadMediaBlob asks one media path for its bytes and returns an object URL for
 * them. An empty string means the route has no such attachment — a 404 is
 * "nothing to show", not a failure.
 *
 * The caller owns one claim on the returned URL and must hand it back with
 * releaseMediaBlob, whether it drew the picture or not. A reader that walks
 * away cancels only its own wait: the download is shared and serves whoever is
 * still looking, so it is never torn down underneath them.
 */
export async function loadMediaBlob(
  path: string,
  token: string,
  signal?: AbortSignal
): Promise<string> {
  let entry = entries.get(path)
  if (!entry) {
    entry = { promise: download(path, token), refs: 0 }
    entries.set(path, entry)
    // A failure is a fact about the moment and not about the file: the entry is
    // dropped as soon as the download fails, so the next reader asks again
    // rather than being handed the same rejection forever.
    entry.promise.catch(() => {
      if (entries.get(path) === entry) entries.delete(path)
    })
  }
  entry.refs++
  return claim(path, entry, signal)
}

async function claim(path: string, entry: Entry, signal?: AbortSignal): Promise<string> {
  if (!signal) return entry.promise
  const aborted = new Promise<never>((_, reject) => {
    signal.addEventListener("abort", () => reject(new DOMException("aborted", "AbortError")), {
      once: true,
    })
  })
  try {
    return await Promise.race([entry.promise, aborted])
  } catch (error) {
    // The reader is gone before the bytes were in hand. Its claim is returned;
    // if it was the last one, whatever the download eventually produces is
    // discarded rather than left dangling.
    entry.refs--
    if (entry.refs <= 0 && entries.get(path) === entry) entries.delete(path)
    throw error
  }
}

async function download(path: string, token: string): Promise<string> {
  await acquire()
  let objectUrl = ""
  try {
    const response = await fetch(new URL(API_BASE + path, globalThis.location?.origin ?? "http://localhost"), {
      headers: { Authorization: `Bearer ${token}` },
      signal: AbortSignal.timeout(TIMEOUT_MS),
    })
    // A missing attachment is empty, not broken: the message may simply no
    // longer carry the file, and that is not a read that failed.
    if (response.status === 404) return ""
    if (!response.ok) throw new Error(`media_error_${response.status}`)
    objectUrl = URL.createObjectURL(await response.blob())
    return objectUrl
  } finally {
    release()
    // If the last reader had already walked away, nobody will ever show these
    // bytes: the URL is released here rather than left to leak.
    if (objectUrl && !entries.has(path)) URL.revokeObjectURL(objectUrl)
  }
}

/**
 * releaseMediaBlob hands back one claim. The object URL is revoked once the
 * last reader is done with it — never before, or a picture still on screen
 * would be cut out from under it.
 */
export function releaseMediaBlob(path: string, url: string) {
  const entry = entries.get(path)
  if (entry) {
    entry.refs--
    if (entry.refs > 0) return
    entries.delete(path)
  }
  if (url.startsWith("blob:")) URL.revokeObjectURL(url)
}

// resetMediaBlobCache forgets every claim and every object URL. It exists for
// tests, which own their own fixtures; nothing in the panel calls it.
export function resetMediaBlobCache() {
  entries.clear()
}
