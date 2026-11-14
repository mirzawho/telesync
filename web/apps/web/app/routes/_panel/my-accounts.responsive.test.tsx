import assert from "node:assert/strict"
import { test } from "node:test"
import { renderToStaticMarkup } from "react-dom/server"

import type { TelegramAccount } from "@/lib/types"
import {
  ACCOUNT_CONFIRMATIONS,
  confirmationForAccountAction,
  MyAccountsHeader,
} from "./my-accounts"

const ACCOUNT: TelegramAccount = {
  id: "acc_01hxyzabcdefghijklmnopqrstuv",
  server_key: "official",
  network_type: "telegram",
  remote_display_name: "حساب خودم",
  status: "connected",
}

test("My Accounts keeps its subtitle and places add at physical left in RTL", () => {
  const out = renderToStaticMarkup(<MyAccountsHeader onAdd={() => undefined} />)
  assert.match(out, /<h1[^>]*>حساب‌های من<\/h1>/)
  assert.match(out, /حساب‌های تلگرامی که خودتان به پنل متصل کرده‌اید/)
  assert.match(out, /افزودن حساب/)
  const button = out.match(/<button[^>]*data-slot="button"[^>]*>/)?.[0] ?? ""
  assert.match(button, /ms-auto/)
})

test("logout and removal use separate confirmations; reversible disconnect does not", () => {
  assert.deepEqual(confirmationForAccountAction("disconnect", ACCOUNT), null)
  assert.deepEqual(confirmationForAccountAction("logout", ACCOUNT), {
    kind: "logout",
    account: ACCOUNT,
  })
  assert.deepEqual(confirmationForAccountAction("remove", ACCOUNT), {
    kind: "remove",
    account: ACCOUNT,
  })

  assert.notEqual(
    ACCOUNT_CONFIRMATIONS.logout.description,
    ACCOUNT_CONFIRMATIONS.remove.description
  )
  assert.match(ACCOUNT_CONFIRMATIONS.logout.description, /باقی می‌مانند/)
  assert.match(ACCOUNT_CONFIRMATIONS.remove.description, /قابل بازگشت نیست/)
})
