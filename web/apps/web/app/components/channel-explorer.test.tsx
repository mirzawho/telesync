import assert from "node:assert/strict"
import { test } from "node:test"

import { renderToStaticMarkup } from "react-dom/server"
import { MemoryRouter } from "react-router"

import { ChannelMessageItem } from "@/components/channel-explorer/message-item"
import { ChannelMessagesPane } from "@/components/channel-explorer/channel-messages-pane"
import { ChannelCategoryChips, ChannelListPane } from "@/components/channel-explorer/channel-list"
import { accountOriginKey, needsAccountControl } from "@/lib/account-channel-scope"
import { ChannelDeleteDialogContent } from "@/components/channel-explorer/channel-delete-dialog"
import { deleteChannelForm } from "@/lib/channel-actions"
import type { Category, Channel, ChannelDetail, ChannelMessage, TelegramAccount } from "@/lib/types"
import { AlertDialog } from "@workspace/ui/components/alert-dialog"

// The panel is `dir="rtl"` end to end, so the logical utilities map to the
// physical sides the product asks for: `justify-end` puts a row on the physical
// LEFT, `justify-start` on the physical RIGHT. These tests assert the class
// that produces that side on every row, so a change of alignment is caught
// even though no browser is involved.

const html = (node: React.ReactNode) => renderToStaticMarkup(node)
// A channel row carries a link, so a list with rows in it needs the router the
// app runs it under. Empty lists render fine without one.
const routed = (node: React.ReactNode) =>
  renderToStaticMarkup(<MemoryRouter initialEntries={["/channels"]}>{node}</MemoryRouter>)

const TEXT_MESSAGE: ChannelMessage = {
  id: 24,
  date: "2026-10-26T22:30:45Z",
  text: "خبر تازه دربارهٔ فناوری.",
  views: 35,
  reactions: [],
  entities: [],
}

const PHOTO_MESSAGE: ChannelMessage = {
  id: 30,
  date: "2026-10-27T09:00:00Z",
  text: "توضیح کوتاه",
  media: {
    id: "6:30",
    type: "photo",
    label: "عکس",
    size: 240_000,
    url: "/telegram/cat/channels/6/messages/30/media",
  },
  views: 12,
  reactions: [],
  entities: [],
}

const MEDIA_ONLY: ChannelMessage = {
  id: 36,
  date: "2026-10-30T13:45:38Z",
  media: {
    id: "6:36",
    type: "document",
    label: "فایل",
    filename: "original-app-name.apk",
    mime_type: "application/vnd.android.package-archive",
    size: 59_797_754,
    url: "/telegram/cat/channels/6/messages/36/media",
  },
  views: 45,
  reactions: [{ type: "emoji", emoji: "❤️", count: 2, chosen: null }],
  entities: [],
}

test("text and media share one single bubble", () => {
  const out = html(<ChannelMessageItem message={PHOTO_MESSAGE} client="cat" />)
  const bubbles = out.match(/data-slot="bubble"/g) ?? []
  assert.equal(bubbles.length, 1, "media and text must not be in two bubbles")
  // Both the caption and the picture are inside that one bubble.
  assert.match(out, /توضیح کوتاه/)
  assert.match(out, /<img/)
})

test("every message row packs on the same side, never alternating", () => {
  for (const message of [TEXT_MESSAGE, PHOTO_MESSAGE, MEDIA_ONLY]) {
    const out = html(<ChannelMessageItem message={message} client="cat" />)
    const row = out.match(/data-slot="message"[^>]*/)![0]
    assert.match(row, /justify-end/, "the row must be on the physical left")
  }
})

test("the publish action is a pill hanging off its own bubble", () => {
  const out = html(
    <ChannelMessageItem
      message={TEXT_MESSAGE}
      client="cat"
      onPublish={() => undefined}
    />
  )
  // It lives inside the bubble's own wrapper, after the bubble, so it belongs
  // to that bubble rather than sitting in a column beside the message.
  assert.ok(out.includes('data-slot="message-bubble"'))
  assert.ok(out.indexOf('data-slot="message-publish"') > out.indexOf('data-slot="bubble"'))
  assert.match(out, /data-slot="message-publish"[^>]*class="[^"]*rounded-full/)
  assert.match(out, /ارسال این پیام/)
})

test("the publish pill straddles the bubble's bottom edge on the physical right", () => {
  const out = html(
    <ChannelMessageItem
      message={TEXT_MESSAGE}
      client="cat"
      onPublish={() => undefined}
    />
  )
  const publish = out.match(/<button[^>]*data-slot="message-publish"[^>]*>/)![0]
  // Lifted onto the bubble's bottom edge: half of it hangs below.
  assert.match(publish, /-mt-3\.5/)
  // A soft white separation against the bubble, not an outline.
  assert.match(publish, /ring-2/)
  assert.match(publish, /rounded-full/)
  // The button's own border is transparent; no coloured outline is drawn.
  assert.doesNotMatch(publish, /border-(?:border|input|foreground|primary)/)
  const row = out.match(
    /<div class="[^"]*justify-start[^"]*"><button[^>]*data-slot="message-publish"/
  )
  // Under RTL `justify-start` is the physical right, which is where the
  // product wants the action; the reactions stay on the opposite side.
  assert.ok(row, "the action must sit in a row that puts it on the physical right")
})

test("the publish pill has no icon and the bubble keeps clear space for it", () => {
  const out = html(
    <ChannelMessageItem
      message={TEXT_MESSAGE}
      client="cat"
      onPublish={() => undefined}
    />
  )
  const publish = out.match(/<button[^>]*data-slot="message-publish"[\s\S]*?<\/button>/)![0]
  assert.doesNotMatch(publish, /<svg/)
  // The band the straddling control may cover is reserved below the text, so
  // it never lands on the message.
  assert.match(out, /data-slot="bubble-content"[^>]*class="[^"]*pb-2/)
})

test("a message with reactions keeps them clear of the publish pill", () => {
  const out = html(
    <ChannelMessageItem
      message={MEDIA_ONLY}
      client="cat"
      onPublish={() => undefined}
    />
  )
  // Reactions are last in the bubble and carry the reserved band; the pill is
  // above their foot without touching them.
  assert.match(out, /data-slot="bubble-reactions"[^>]*class="[^"]*pb-2/)
  assert.match(out, /justify-end/, "reactions stay on the physical left")
  assert.match(out, /data-slot="message-publish"/)
})

test("no publish action is offered without a handler", () => {
  const out = html(<ChannelMessageItem message={TEXT_MESSAGE} client="cat" />)
  assert.doesNotMatch(out, /ارسال این پیام/)
  assert.doesNotMatch(out, /data-slot="message-publish"/)
})

test("a message renders no avatar and no per-message sender header", () => {
  const out = html(<ChannelMessageItem message={TEXT_MESSAGE} client="cat" />)
  assert.doesNotMatch(out, /data-slot="message-avatar"/)
  assert.doesNotMatch(out, /<img[^>]*avatar/i)
  assert.doesNotMatch(out, /ارسال کانال/)
})

test("the footer shows views, a separator dot and the send time", () => {
  const out = html(<ChannelMessageItem message={TEXT_MESSAGE} client="cat" />)
  assert.match(out, /data-slot="message-footer"/)
  // 35 views in Persian numerals, then the dot, then the clock time.
  assert.match(out, /۳۵/)
  assert.match(out, /·/)
  assert.match(out, /data-slot="message-footer-time"/)
})

test("a message with no view count says so instead of inventing zero", () => {
  const out = html(
    <ChannelMessageItem
      message={{ ...TEXT_MESSAGE, views: null }}
      client="cat"
    />
  )
  const footer = out.match(/data-slot="message-footer"[^>]*>[\s\S]*$/)![0]
  assert.match(footer, /بازدید نامشخص/)
  // No bare count is rendered in place of the one Telegram never sent.
  assert.doesNotMatch(footer, /<span class="tabular-nums">[۰-۹]+<\/span>/)
})

test("reactions sit at the bottom left and never render when empty", () => {
  const withReactions = html(<ChannelMessageItem message={MEDIA_ONLY} client="cat" />)
  assert.match(withReactions, /data-slot="bubble-reactions"/)
  assert.match(withReactions, /justify-end/, "reactions must be on the physical left")
  assert.match(withReactions, /❤️/)
  assert.match(withReactions, /۲/)

  const without = html(<ChannelMessageItem message={TEXT_MESSAGE} client="cat" />)
  assert.doesNotMatch(without, /data-slot="bubble-reactions"/)
})

test("a read-only reaction is not dressed up as a button", () => {
  const out = html(<ChannelMessageItem message={MEDIA_ONLY} client="cat" />)
  assert.doesNotMatch(out, /<button[^>]*data-slot="bubble-reaction"/)
  assert.match(out, /واکنش/)
})

test("an image renders as the content, not inside a grey attachment card", () => {
  const out = html(<ChannelMessageItem message={PHOTO_MESSAGE} client="cat" />)
  assert.match(out, /<img/)
  assert.doesNotMatch(out, /data-slot="attachment"/)
  // No redundant file facts around a picture.
  assert.doesNotMatch(out, />عکس</)
  assert.doesNotMatch(out, /data-slot="badge"/)
})

test("a document shows its real filename and a download action", () => {
  const out = html(<ChannelMessageItem message={MEDIA_ONLY} client="cat" />)
  assert.match(out, /original-app-name\.apk/)
  assert.match(out, /دانلود/)
  assert.match(out, /data-slot="attachment"/)
})

test("a media-only message is still rendered", () => {
  const out = html(<ChannelMessageItem message={MEDIA_ONLY} client="cat" />)
  assert.match(out, /data-slot="bubble"/)
  assert.match(out, /<img|<audio|<video|data-slot="attachment"/)
})

test("an audio attachment gets a real player", () => {
  const out = html(
    <ChannelMessageItem
      message={{
        ...TEXT_MESSAGE,
        media: {
          id: "6:24",
          type: "audio",
          label: "صدا",
          mime_type: "audio/mpeg",
          size: 3_174_901,
          url: "/telegram/cat/channels/6/messages/24/media",
        },
      }}
      client="cat"
    />
  )
  assert.match(out, /<audio/)
  assert.match(out, /controls/)
})

test("a voice note keeps its own kind and plays", () => {
  const out = html(
    <ChannelMessageItem
      message={{
        ...TEXT_MESSAGE,
        media: {
          type: "voice",
          label: "صدا",
          mime_type: "audio/ogg",
          url: "/telegram/cat/channels/6/messages/2/media",
        },
      }}
      client="cat"
    />
  )
  assert.match(out, /<audio/)
  assert.doesNotMatch(out, /data-slot="attachment"/)
})

test("a video is playable inside the bubble", () => {
  const out = html(
    <ChannelMessageItem
      message={{
        ...TEXT_MESSAGE,
        media: {
          type: "video",
          label: "ویدیو",
          url: "/telegram/cat/channels/6/messages/9/media",
        },
      }}
      client="cat"
    />
  )
  assert.match(out, /<video/)
  assert.match(out, /controls/)
})

test("message text is rendered as text, never as HTML", () => {
  const out = html(
    <ChannelMessageItem
      message={{ ...TEXT_MESSAGE, text: "<script>alert(1)</script>" }}
      client="cat"
    />
  )
  assert.doesNotMatch(out, /<script/)
  assert.match(out, /&lt;script&gt;/)
})

test("entities survive as real elements, not plain text", () => {
  const out = html(
    <ChannelMessageItem
      message={{
        ...TEXT_MESSAGE,
        text: "ببینید https://t.me/x را",
        entities: [{ type: "url", offset: 7, length: 14 }],
      }}
      client="cat"
    />
  )
  assert.match(out, /<a /)
  assert.match(out, /https:\/\/t\.me\/x/)
})

test("a bold entity becomes a real bold element", () => {
  const out = html(
    <ChannelMessageItem
      message={{
        ...TEXT_MESSAGE,
        text: "مهم",
        entities: [{ type: "bold", offset: 0, length: 3 }],
      }}
      client="cat"
    />
  )
  assert.match(out, /<strong>/)
})

// --- channel list ---------------------------------------------------------

const CATEGORIES: Category[] = [
  { id: 10, name: "اخبار", description: "", created_at: "", updated_at: "" },
  { id: 20, name: "فناوری", description: "", created_at: "", updated_at: "" },
]

test("category chips expose their pressed state to assistive tech", () => {
  const out = html(
    <ChannelCategoryChips
      categories={CATEGORIES}
      selected={[10]}
      onToggle={() => undefined}
    />
  )
  assert.match(out, /aria-pressed="true"/)
  assert.match(out, /aria-pressed="false"/)
  assert.match(out, /type="button"/, "a chip must never submit a form")
  // One row that scrolls sideways, never wraps onto a second line.
  const list = out.match(/data-slot="category-chips"[^>]*/)![0]
  assert.match(list, /flex-nowrap/)
  assert.match(list, /overflow-x-auto/)
})

test("an empty category list renders no chips at all", () => {
  const out = html(
    <ChannelCategoryChips categories={[]} selected={[]} onToggle={() => undefined} />
  )
  assert.doesNotMatch(out, /aria-pressed/)
})

test("channel list replaces the add button with the global channel menu trigger", () => {
  const out = html(
    <ChannelListPane
      channels={[]}
      categories={[]}
      filed={{}}
      selectedId={null}
      search=""
      onSearchChange={() => undefined}
      selectedCategories={[]}
      onToggleCategory={() => undefined}
      onAdd={() => undefined}
      onCreate={() => undefined}
      onEditCategories={() => undefined}
      onDelete={() => undefined}
    />
  )
  assert.match(out, /aria-label="عملیات کانال‌ها"/)
  assert.doesNotMatch(out, /aria-label="کانال جدید"/)
  assert.match(out, /جستجو در کانال‌ها/)
  assert.match(out, /data-slot="channel-filter-toolbar"/)
  assert.doesNotMatch(out, /data-slot="server-multi-select"/)
  assert.doesNotMatch(out, /همه سرورها/)
  const toolbar = out.match(/data-slot="channel-filter-toolbar"[^>]*>/)?.[0] ?? ""
  assert.match(toolbar, /dir="rtl"/)
  assert.match(toolbar, /min-w-0/)
  assert.match(out, /overflow-x-auto/)
})

test("the Explorer does not use server definitions as account identity filters", () => {
  const out = html(
    <ChannelListPane
      channels={[]}
      categories={CATEGORIES}
      filed={{}}
      selectedId={null}
      search=""
      onSearchChange={() => undefined}
      selectedCategories={[]}
      onToggleCategory={() => undefined}
      onAdd={() => undefined}
      onEditCategories={() => undefined}
      onDelete={() => undefined}
    />
  )
  assert.doesNotMatch(out, /server-multi-select/)
  assert.doesNotMatch(out, /فیلتر سرورها/)
})

// --- channel message management permissions -------------------------------

const DETAIL: ChannelDetail = {
  id: "141",
  account_id: "acc_test",
  type: "channel",
  title: "کانال آزمایشی",
  avatar_available: false,
  is_member: true,
  is_managed: true,
  membership_status: "member",
  permissions: { new: true, edit: false, delete: false, read: true },
  verified: false,
  scam: false,
  fake: false,
  participants_count: 1,
}

test("channel message viewer shows the composer only with new permission", () => {
  const writable = html(
    <ChannelMessagesPane channel={DETAIL} channelId={7} client="cat" accountId="acc_test" />
  )
  assert.match(writable, /پیام خود را بنویسید/)
  assert.doesNotMatch(writable, /ویرایش پیام|حذف پیام/)

  const readOnly = html(
    <ChannelMessagesPane
      channel={{ ...DETAIL, permissions: { new: false, edit: false, delete: false, read: true } }}
      channelId={7}
      client="cat"
      accountId="acc_test"
    />
  )
  assert.doesNotMatch(readOnly, /پیام خود را بنویسید/)
})

test("a missing destination is distinct from a confirmed Send denial", () => {
  const allGranted = { ...DETAIL, permissions: { new: true, edit: true, delete: true, read: true } }
  const missingDestination = html(
    <ChannelMessagesPane channel={allGranted} channelId={2} client="" accountId="acc_test" />
  )
  assert.match(missingDestination, /اطلاعات مقصد کانال در دسترس نیست/)
  assert.doesNotMatch(missingDestination, /مقصد یا مجوز کانال در دسترس نیست/)

  const deniedSend = html(
    <ChannelMessagesPane
      channel={{ ...allGranted, permissions: { new: false, edit: true, delete: true, read: true } }}
      channelId={2}
      client="cat"
      accountId="acc_test"
    />
  )
  assert.match(deniedSend, /امکان ارسال پیام برای این کانال فعال نیست/)
  assert.doesNotMatch(deniedSend, /اطلاعات مقصد کانال در دسترس نیست/)
})

// --- deletion -------------------------------------------------------------

const CHANNEL: Channel = {
  id: 7,
  client_key: "cat",
  channel_id: 1007,
  type: "channel",
  title: "کانال آزمایشی",
  avatar_available: false,
  membership_status: "member",
  participants_count: null,
  created_at: "",
  updated_at: "",
  last_synced_at: "",
}

// The dialog's content is rendered under the alert dialog's own root: the root
// supplies the context the title, description and close need, and the portal
// that would move the markup out of the document is what a static render drops.
function DeleteBody({
  pending = false,
  error = null,
}: {
  pending?: boolean
  error?: string | null
}) {
  return (
    <AlertDialog open>
      <ChannelDeleteDialogContent
        open
        channel={CHANNEL}
        pending={pending}
        error={error}
        onConfirm={() => undefined}
      />
    </AlertDialog>
  )
}

test("an unticked deletion never asks Telegram to leave", () => {
  // The dialog's tick box decides the payload, and the payload is what the
  // backend acts on. Unticked — the default — the request is local-only.
  assert.equal(deleteChannelForm(7, { leaveTelegram: false }).telegram, "false")
  assert.equal(deleteChannelForm(7, { leaveTelegram: false }).intent, "leave")
  assert.equal(deleteChannelForm(7, { leaveTelegram: true }).telegram, "true")
})
test("the delete dialog leaves Telegram only when the box is ticked", () => {
  const out = html(<DeleteBody />)
  // Unchecked by default: the destructive default must not be the wider one.
  assert.match(out, /aria-checked="false"/)
  assert.doesNotMatch(out, /aria-checked="true"/)
  assert.match(out, /همچنین از کانال در تلگرام خارج شوم/)
  assert.match(out, /حذف کانال/)
  assert.match(out, /انصراف/)
  assert.match(out, /آیا از حذف این کانال/)
})

test("the checkbox has a real label the reader can click", () => {
  const out = html(<DeleteBody />)
  assert.match(out, /<label[^>]*for="[^"]+"/)
  assert.match(out, /type="checkbox"/)
})

test("the destructive action is disabled and relabelled while pending", () => {
  const out = html(<DeleteBody pending />)
  assert.match(out, /در حال حذف/)
  const buttons = out.match(/<button[^>]*>/g) ?? []
  assert.ok(
    buttons.some((button) => button.includes("disabled")),
    "the destructive action must be disabled while pending"
  )
})

test("a deletion failure is shown in the dialog, not swallowed", () => {
  const out = html(
    <DeleteBody error="تلگرام قبول کرد اما رکورد محلی حذف نشد." />
  )
  assert.match(out, /role="alert"/)
  assert.match(out, /تلگرام قبول کرد اما رکورد محلی حذف نشد./)
})

test("the standalone Telegram-leave action is gone from this interface", () => {
  const out = html(<DeleteBody />)
  assert.doesNotMatch(out, /خروج از کانال \(تلگرام\)/)
  assert.doesNotMatch(out, /حذف رکورد محلی/)
})

// --- account origin in the channel list ------------------------------------

const ACCOUNT_ONE: TelegramAccount = {
  id: "acc_aaaaaaaaaaaaaaaaaaaaaaaaaa",
  server_key: "official",
  network_type: "telegram",
  remote_display_name: "حساب نخست",
  remote_username: "first",
  status: "connected",
}

const ACCOUNT_TWO: TelegramAccount = {
  id: "acc_bbbbbbbbbbbbbbbbbbbbbbbbbb",
  server_key: "official",
  network_type: "telegram",
  remote_display_name: "حساب دوم",
  remote_username: "second",
  status: "connected",
}

function ownChannel(overrides: Partial<Channel> = {}): Channel {
  return {
    id: 1,
    client_key: "official",
    account_id: ACCOUNT_ONE.id,
    channel_id: 100,
    type: "channel",
    title: "کانال مشترک",
    avatar_available: false,
    membership_status: "member",
    participants_count: null,
    created_at: "",
    updated_at: "",
    last_synced_at: "",
    ...overrides,
  }
}

const listProps = (channels: Channel[], accounts: TelegramAccount[]) => ({
  channels,
  accounts,
  categories: [] as Category[],
  filed: {} as Record<number, number[]>,
  selectedId: null,
  search: "",
  onSearchChange: () => undefined,
  selectedCategories: [] as number[],
  onToggleCategory: () => undefined,
  onAdd: () => undefined,
  onEditCategories: () => undefined,
  onDelete: () => undefined,
})

test("two rows for one remote channel are told apart by their account", () => {
  const first = ownChannel({ id: 1, account_id: ACCOUNT_ONE.id })
  const second = ownChannel({ id: 2, account_id: ACCOUNT_TWO.id })
  const out = routed(
    <ChannelListPane {...listProps([first, second], [ACCOUNT_ONE, ACCOUNT_TWO])} />
  )
  // Both rows are drawn: one is never collapsed into the other.
  assert.equal((out.match(/data-slot="channel-list-item"/g) ?? []).length, 2)
  // Each names the account it is reached through.
  assert.match(out, /حساب نخست/)
  assert.match(out, /حساب دوم/)
  assert.equal((out.match(/data-slot="account-origin"/g) ?? []).length, 2)
})

test("a row is never keyed by something two accounts would share", () => {
  // The same remote channel through two accounts would be one React key if it
  // were keyed by the channel; the list keys it by the account-scoped origin.
  const first = ownChannel({ id: 1, account_id: ACCOUNT_ONE.id, channel_id: 100 })
  const second = ownChannel({ id: 2, account_id: ACCOUNT_TWO.id, channel_id: 100 })
  assert.equal(first.channel_id, second.channel_id)
  assert.notEqual(accountOriginKey(first), accountOriginKey(second))

  const out = routed(
    <ChannelListPane {...listProps([first, second], [ACCOUNT_ONE, ACCOUNT_TWO])} />
  )
  // An ambiguous row says "account unknown" rather than passing for one.
  assert.doesNotMatch(out, /حساب نامشخص/)
})

test("one account is unambiguous and needs no label on every row", () => {
  const out = routed(
    <ChannelListPane {...listProps([ownChannel()], [ACCOUNT_ONE])} />
  )
  assert.equal((out.match(/data-slot="channel-list-item"/g) ?? []).length, 1)
  assert.equal((out.match(/data-slot="account-origin"/g) ?? []).length, 0)
})

test("an unowned row is labelled as unknown, never as somebody's own", () => {
  // The pane draws exactly what it is given; the ownership gate is
  // scopeToOwnAccounts, and a caller that skipped it is the bug. What the pane
  // must never do is pass such a row off as one of the accounts it knows.
  const legacy = ownChannel({ id: 9, account_id: undefined })
  const out = routed(
    <ChannelListPane
      {...listProps([ownChannel(), legacy], [ACCOUNT_ONE, ACCOUNT_TWO])}
    />
  )
  assert.match(out, /حساب نامشخص/)
  assert.match(out, /حساب نخست/)
  // One label per row: the unknown one never borrows the other's name.
  assert.equal((out.match(/data-slot="account-origin"/g) ?? []).length, 2)
})

test("the account control appears only where the server filter cannot say", () => {
  const two = [ACCOUNT_ONE, ACCOUNT_TWO]
  assert.equal(needsAccountControl(two, ["official"]), true)
  const shown = routed(
    <ChannelListPane
      {...listProps([ownChannel()], two)}
      showAccountControl={needsAccountControl(two, ["official"])}
      onToggleAccount={() => undefined}
      onSelectAllAccounts={() => undefined}
      selectedAccounts={[]}
    />
  )
  assert.match(shown, /data-slot="account-multi-select"/)
  assert.match(shown, /همه حساب‌ها/)

  const one = routed(
    <ChannelListPane
      {...listProps([ownChannel()], [ACCOUNT_ONE])}
      showAccountControl={needsAccountControl([ACCOUNT_ONE], ["official"])}
    />
  )
  assert.doesNotMatch(one, /data-slot="account-multi-select"/)
})
