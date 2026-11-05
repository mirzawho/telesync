// The publishing dialog shows the source message's attachment, and it shows it
// through the panel's own authenticated media route: a plain <img src> carries
// no bearer token, so the bytes are fetched and drawn from an object URL.
//
// Two panels in the dialog draw the same attachment — the editor and the live
// preview — and a picture must not be downloaded twice because two components
// asked for it. The cache is what makes one fetch serve both, and what revokes
// the object URL once nothing is showing it any more.
import assert from "node:assert/strict"
import { test } from "node:test"

import {
  loadMediaBlob,
  releaseMediaBlob,
  resetMediaBlobCache,
} from "@/lib/media-blob-cache"

// The cache hands out object URLs, which only a browser makes. The test stands
// in for them and records what was created and revoked, so the assertions are
// about ownership rather than about the platform.
let created = 0
const revoked: string[] = []

globalThis.URL.createObjectURL = () => `blob:test-${++created}`
globalThis.URL.revokeObjectURL = (url: string) => revoked.push(url)

// fetchMedia answers 404 as "nothing to show", not as a failure, so a message
// whose attachment is gone reads as empty rather than broken.
//
// The cache builds an absolute URL from the API prefix, so responses are keyed
// by the media path they end in rather than by the whole URL.
function stubFetch(responses: Record<string, () => Response>) {
  const seen: string[] = []
  globalThis.fetch = (async (input: RequestInfo | URL) => {
    const url = String(input)
    seen.push(url)
    const key = Object.keys(responses).find((path) => url.endsWith(path))
    if (!key) throw new Error(`unexpected fetch: ${url}`)
    return responses[key]()
  }) as typeof fetch
  return seen
}

function ok(body = "jpeg-bytes", type = "image/jpeg") {
  return () =>
    new Response(body, {
      status: 200,
      headers: { "Content-Type": type },
    })
}

test.beforeEach(() => {
  resetMediaBlobCache()
  revoked.length = 0
})

test("two readers of one attachment share a single fetch and one object URL", async () => {
  const seen = stubFetch({ "/media/1": ok() })

  const first = await loadMediaBlob("/media/1", "token")
  const second = await loadMediaBlob("/media/1", "token")

  assert.equal(seen.length, 1, "the attachment must be downloaded once")
  assert.equal(first, second, "both readers must share the same object URL")

  // One reader lets go: the picture is still on screen, so the URL stays.
  releaseMediaBlob("/media/1", first)
  assert.equal(revoked.length, 0, "an object URL still in use must not be revoked")

  releaseMediaBlob("/media/1", second)
  assert.deepEqual(revoked, [first], "the object URL must be revoked once nothing uses it")
})

test("concurrent readers of one attachment do not each start a download", async () => {
  const seen = stubFetch({ "/media/1": ok() })

  const [first, second] = await Promise.all([
    loadMediaBlob("/media/1", "token"),
    loadMediaBlob("/media/1", "token"),
  ])

  assert.equal(seen.length, 1, "two callers arriving together must share one download")
  assert.equal(first, second)
})

test("different attachments are fetched and owned separately", async () => {
  const seen = stubFetch({ "/media/1": ok("one"), "/media/2": ok("two") })

  const first = await loadMediaBlob("/media/1", "token")
  const second = await loadMediaBlob("/media/2", "token")
  assert.equal(seen.length, 2)
  assert.notEqual(first, second)

  releaseMediaBlob("/media/1", first)
  assert.deepEqual(revoked, [first], "letting go of one attachment must not disturb the other")
  releaseMediaBlob("/media/2", second)
})

test("a second attempt after a failure fetches again rather than caching the failure", async () => {
  let attempt = 0
  const seen: string[] = []
  globalThis.fetch = (async (input: RequestInfo | URL) => {
    seen.push(String(input))
    attempt++
    if (attempt === 1) return new Response("", { status: 500 })
    return new Response("jpeg-bytes", { status: 200 })
  }) as typeof fetch

  await assert.rejects(() => loadMediaBlob("/media/1", "token"), /media_error_500/)
  const url = await loadMediaBlob("/media/1", "token")
  assert.ok(url.startsWith("blob:"), "the retry must produce the bytes")
  assert.equal(seen.length, 2, "the retry must reach the server again")
})

test("a missing attachment is empty, not a failure", async () => {
  stubFetch({ "/media/1": () => new Response("", { status: 404 }) })
  assert.equal(await loadMediaBlob("/media/1", "token"), "")
})

test("an unauthorized fetch is reported, never shown as an empty picture", async () => {
  stubFetch({ "/media/1": () => new Response("", { status: 401 }) })
  await assert.rejects(() => loadMediaBlob("/media/1", "token"), /media_error_401/)
})

test("the request carries the panel's bearer credential and nothing in the URL", async () => {
  const headers: Array<Record<string, string>> = []
  const urls: string[] = []
  globalThis.fetch = (async (input: RequestInfo | URL, init?: RequestInit) => {
    urls.push(String(input))
    headers.push((init?.headers ?? {}) as Record<string, string>)
    return new Response("jpeg-bytes", { status: 200 })
  }) as typeof fetch

  await loadMediaBlob("/media/1", "secret-token")

  assert.equal(urls.length, 1)
  assert.ok(!urls[0].includes("secret-token"), "the token must never appear in a URL")
  assert.equal(headers[0].Authorization, "Bearer secret-token")
})

test("a reader that walks away costs nobody else the picture", async () => {
  stubFetch({ "/media/1": ok() })
  const controller = new AbortController()

  // Two readers arrive together and the first one gives up before the bytes
  // are in hand. The download belongs to both of them: it is not torn down
  // underneath the second, and the abandoned reader's claim is returned.
  const gone = loadMediaBlob("/media/1", "token", controller.signal)
  const kept = loadMediaBlob("/media/1", "token")

  controller.abort()
  await assert.rejects(() => gone, (error: Error) => error.name === "AbortError")

  const url = await kept
  assert.ok(url.startsWith("blob:"), "the remaining reader must still get the picture")
  assert.equal(revoked.length, 0, "the picture is still on screen")

  releaseMediaBlob("/media/1", url)
  assert.deepEqual(revoked, [url], "the last reader must revoke the object URL")
})

test("the bytes of a download nobody waited for are not left dangling", async () => {
  stubFetch({ "/media/1": ok() })
  const controller = new AbortController()

  const gone = loadMediaBlob("/media/1", "token", controller.signal)
  controller.abort()
  await assert.rejects(() => gone)

  // Let the shared download finish with no reader left to show it. Its object
  // URL must be released rather than kept for nobody.
  await new Promise((resolve) => setTimeout(resolve, 0))
  assert.equal(revoked.length, 1, "the orphaned object URL must be revoked")
})
