import assert from "node:assert/strict"
import test from "node:test"
import { createElement } from "react"
import { renderToStaticMarkup } from "react-dom/server"

import type { ChannelDetail, TelegramAccount } from "@/lib/types"
import {
  channelRequestKey,
  channelMessagePath,
  copyChannelId,
  currentChannelDetail,
  ChannelInformationError,
  ChannelInformationLoading,
  ChannelInformationProfile,
} from "./channel-information-dialog"

test("channel information shows a loading skeleton until real details arrive", () => {
  const html = renderToStaticMarkup(
    createElement(ChannelInformationLoading)
  )

  assert.match(html, /aria-busy="true"/)
  assert.doesNotMatch(html, /access.?hash|token|session/i)
})

test("channel information loading state reserves the profile avatar and muted header", () => {
  const html = renderToStaticMarkup(
    createElement(ChannelInformationLoading)
  )

  assert.match(html, /bg-muted\/60/)
  assert.match(html, /size-28/)
})

const detail = (overrides: Partial<ChannelDetail> = {}): ChannelDetail => ({
  id: "-1009876543210",
  type: "channel",
  title: "خبرهای امروز",
  username: "daily_news",
  description: "خط اول\nخط دوم",
  avatar_available: false,
  is_member: true,
  is_managed: true,
  membership_status: "member",
  permissions: { read: true, new: false, edit: true, delete: false },
  verified: false,
  scam: false,
  fake: false,
  participants_count: 197,
  ...overrides,
})

function renderProfile(value: ChannelDetail) {
  return renderToStaticMarkup(
    createElement(ChannelInformationProfile, {
      detail: value,
      localChannelId: 42,
      onCopyId: () => {},
      onView: () => {},
      onEditCategories: () => {},
      onDelete: () => {},
    })
  )
}

test("channel profile uses the real title, fallback avatar, count and description", () => {
  const html = renderProfile(detail())

  assert.match(html, /خبرهای امروز/)
  assert.match(html, />خ<\//)
  assert.match(html, /۱۹۷ مشترک/)
  assert.match(html, /خط اول\nخط دوم/)
  assert.match(html, /@daily_news/)
  assert.match(html, /اطلاعات بیشتر/)
  assert.match(html, /aria-expanded="false"/)
  assert.match(html, /grid-cols-3/)
  assert.match(html, /بیشتر/)
  assert.match(html, /ویرایش پیام/)
  assert.doesNotMatch(html, /تأییدشده/)
})

test("verified and warning markers render only from the channel response", () => {
  const html = renderProfile(detail({ verified: true, fake: true }))

  assert.match(html, /تأییدشده/)
  assert.match(html, /جعلی/)
})

test("missing counts and unavailable public links are omitted", () => {
  const html = renderProfile(detail({ participants_count: null, username: undefined }))

  assert.doesNotMatch(html, /۰ مشترک|اعضا/)
  assert.doesNotMatch(html, /t\.me|لینک کانال|کپی لینک/)
})

test("view is offered only when read permission is true and IDs remain remote", () => {
  const readable = renderProfile(detail())
  const unreadable = renderProfile(detail({ permissions: { read: false } }))

  assert.match(readable, /مشاهده/)
  assert.doesNotMatch(unreadable, /مشاهده/)
  assert.match(readable, /-1009876543210/)
  assert.doesNotMatch(readable, />42</)
})

test("changing connection or remote channel hides a previously loaded profile", () => {
  const loaded = { key: channelRequestKey("official", "-1009876543210"), detail: detail() }

  assert.equal(currentChannelDetail(true, channelRequestKey("cat", "-1009876543210"), loaded), null)
  assert.equal(currentChannelDetail(true, channelRequestKey("official", "-100123"), loaded), null)
  assert.equal(currentChannelDetail(false, loaded.key, loaded), null)
  assert.equal(currentChannelDetail(true, loaded.key, loaded), loaded.detail)
})

test("view route uses the local managed-channel ID and preserves explorer filters", () => {
  assert.equal(channelMessagePath(42, "server=cat&search=news"), "/channels/42/messages?server=cat&search=news")
  assert.equal(channelMessagePath(42, ""), "/channels/42/messages")
})

test("copying uses the remote Telegram ID and reports success or failure", async () => {
  const written: string[] = []
  const results: boolean[] = []

  await copyChannelId("-1009876543210", async (value) => { written.push(value) }, (ok) => results.push(ok))
  await copyChannelId("-1009876543210", async () => { throw new Error("clipboard unavailable") }, (ok) => results.push(ok))

  assert.deepEqual(written, ["-1009876543210"])
  assert.deepEqual(results, [true, false])
})

test("channel detail error remains an accessible alert", () => {
  const html = renderToStaticMarkup(
    createElement(ChannelInformationError, { message: "دریافت اطلاعات کانال ناموفق بود.", onClose: () => {} })
  )

  assert.match(html, /role="alert"/)
  assert.match(html, /دریافت اطلاعات کانال ناموفق بود/)
})

// --- the account the channel is reached through -----------------------------

const OWN_ACCOUNT: TelegramAccount = {
  id: "acc_aaaaaaaaaaaaaaaaaaaaaaaaaa",
  server_key: "official",
  network_type: "telegram",
  remote_display_name: "میرزا",
  remote_username: "mirza",
  phone_number_masked: "+98 *** **45",
  status: "connected",
}

function renderProfileFor(
  value: ChannelDetail,
  account: TelegramAccount | null | undefined
) {
  return renderToStaticMarkup(
    createElement(ChannelInformationProfile, {
      detail: value,
      localChannelId: 42,
      account,
      onCopyId: () => {},
      onView: () => {},
      onEditCategories: () => {},
      onDelete: () => {},
    })
  )
}

test("the dialog names the account the capabilities belong to", () => {
  const html = renderProfileFor(detail(), OWN_ACCOUNT)
  assert.match(html, /data-slot="channel-account"/)
  assert.match(html, /میرزا/)
  assert.match(html, /رسمی تلگرام/)
  // The rights are labelled as that account's, so two readers of one channel
  // are never told they share a capability set.
  assert.match(html, /دسترسی‌های میرزا/)
})

test("two accounts on one channel are shown as two different capability sets", () => {
  const other: TelegramAccount = {
    ...OWN_ACCOUNT,
    id: "acc_bbbbbbbbbbbbbbbbbbbbbbbbbb",
    remote_display_name: "حساب دوم",
  }
  const mine = renderProfileFor(detail(), OWN_ACCOUNT)
  const theirs = renderProfileFor(detail(), other)
  assert.match(mine, /دسترسی‌های میرزا/)
  assert.match(theirs, /دسترسی‌های حساب دوم/)
  assert.notEqual(mine, theirs)
})

test("an unknown account is not invented; the rights are still shown", () => {
  // The capability set is real either way — it is the account's, and the dialog
  // only stops naming the account it could not read.
  const html = renderProfileFor(detail(), null)
  assert.doesNotMatch(html, /data-slot="channel-account"/)
  assert.match(html, /دسترسی‌ها/)
  assert.match(html, /مشاهده پیام/)
})

test("the account summary carries no credential and no full phone number", () => {
  const html = renderProfileFor(detail(), OWN_ACCOUNT)
  assert.doesNotMatch(html, /\+989\d{9}/)
  assert.doesNotMatch(html, /session|token|password|api_id/i)
  // The masked number is never even shown here: this is about who acts, not
  // about how they signed in.
  assert.doesNotMatch(html, /\+98 \*\*\*/)
})

test("the account's real capabilities are what the dialog reports", () => {
  // The record says read and edit, not new or delete: nothing else may appear
  // under this account's name.
  const html = renderProfileFor(detail(), OWN_ACCOUNT)
  assert.match(html, /مشاهده پیام/)
  assert.match(html, /ویرایش پیام/)
  assert.doesNotMatch(html, /ارسال پیام/)
  assert.doesNotMatch(html, /حذف پیام/)
})
