import assert from "node:assert/strict"
import test from "node:test"
import { createElement } from "react"
import { renderToStaticMarkup } from "react-dom/server"

import { CreateChannelForm, createChannelSubmitIsValid, validateChannelImage, validatePublicUsername } from "./create-channel-form"

test("creation form requires a server, starts Private, and uses a circular hidden-input picker", () => {
  const html = renderToStaticMarkup(
    createElement(CreateChannelForm, {
      servers: [],
      onSubmit: () => undefined,
      onCheckUsername: async () => ({ state: "unclaimed" as const }),
    })
  )

  assert.match(html, /سرور تلگرام/)
  assert.match(html, /نام کانال/)
  assert.match(html, /خصوصی/)
  assert.doesNotMatch(html, /شناسه عمومی کانال/)
  assert.match(html, /type="radio"/)
  assert.match(html, /role="radiogroup"/)
  assert.match(html, /type="file"[^>]*class="sr-only"/)
  assert.match(html, /rounded-full/)
  assert.doesNotMatch(html, /<span[^>]*data-slot="switch"/)
})

test("public usernames are checked locally before the availability callback", () => {
  assert.equal(validatePublicUsername("news_room"), null)
  assert.ok(validatePublicUsername("ab"))
  assert.ok(validatePublicUsername("-news"))
  assert.ok(validatePublicUsername("has space"))
})

test("public creation requires a username while private creation does not", () => {
  const base = { clientKey: "server", title: "Title", description: "", visibility: "public" as const }
  assert.equal(createChannelSubmitIsValid({ ...base, username: "" }), false)
  assert.equal(createChannelSubmitIsValid({ ...base, username: "news_room" }), true)
  assert.equal(createChannelSubmitIsValid({ ...base, visibility: "private", username: "" }), true)
})

test("channel image validation requires a bounded image MIME", () => {
  assert.equal(validateChannelImage({ type: "image/png", size: 1024 } as File), null)
  assert.match(validateChannelImage({ type: "text/plain", size: 12 } as File) ?? "", /تصویر/)
  assert.match(validateChannelImage({ type: "image/jpeg", size: 6 * 1024 * 1024 } as File) ?? "", /۵ مگابایت/)
})
