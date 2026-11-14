import assert from "node:assert/strict"
import test from "node:test"
import { createElement } from "react"
import { renderToStaticMarkup } from "react-dom/server"

import { CreateChannelForm, createChannelSubmitIsValid, validateChannelImage, validatePublicUsername } from "./create-channel-form"
import type { TelegramAccount } from "@/lib/types"

const ACCOUNT: TelegramAccount = {
  id: "acc_aaaaaaaaaaaaaaaaaaaaaaaaaa",
  server_key: "official",
  network_type: "telegram",
  remote_display_name: "حساب اصلی",
  remote_username: "main",
  phone_number_masked: "+98 *** **12",
  status: "connected",
}

test("creation form selects an explicit personal account and keeps the existing private/media layout", () => {
  const html = renderToStaticMarkup(
    createElement(CreateChannelForm, {
      accounts: [ACCOUNT],
      onSubmit: () => undefined,
      onCheckUsername: async () => ({ state: "unclaimed" as const }),
    })
  )

  assert.match(html, /حساب تلگرام/)
  assert.match(html, /نام کانال/)
  assert.match(html, /خصوصی/)
  assert.doesNotMatch(html, /شناسه عمومی کانال/)
  assert.match(html, /type="radio"/)
  assert.match(html, /role="radiogroup"/)
  assert.match(html, /type="file"[^>]*class="sr-only"/)
  assert.match(html, /rounded-full/)
  assert.doesNotMatch(html, /<span[^>]*data-slot="switch"/)
})

test("channel creation cannot be valid without an explicit account id", () => {
  const base = {
    accountID: "",
    title: "Title",
    description: "",
    visibility: "private" as const,
  }
  assert.equal(createChannelSubmitIsValid({ ...base, accountID: "" }), false)
  assert.equal(
    createChannelSubmitIsValid({ ...base, accountID: ACCOUNT.id }),
    true
  )
})

test("public usernames are checked locally before the availability callback", () => {
  assert.equal(validatePublicUsername("news_room"), null)
  assert.ok(validatePublicUsername("ab"))
  assert.ok(validatePublicUsername("-news"))
  assert.ok(validatePublicUsername("has space"))
})

test("public creation requires a username while private creation does not", () => {
  const base = { accountID: ACCOUNT.id, title: "Title", description: "", visibility: "public" as const }
  assert.equal(createChannelSubmitIsValid({ ...base, username: "" }), false)
  assert.equal(createChannelSubmitIsValid({ ...base, username: "news_room" }), true)
  assert.equal(createChannelSubmitIsValid({ ...base, visibility: "private", username: "" }), true)
})

test("channel image validation requires a bounded image MIME", () => {
  assert.equal(validateChannelImage({ type: "image/png", size: 1024 } as File), null)
  assert.match(validateChannelImage({ type: "text/plain", size: 12 } as File) ?? "", /تصویر/)
  assert.match(validateChannelImage({ type: "image/jpeg", size: 6 * 1024 * 1024 } as File) ?? "", /۵ مگابایت/)
})
