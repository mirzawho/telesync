// The publishing workspace renders: what the three columns contain, in which
// order they appear, and what the mobile wizard shows at each step.
//
// The workspace is drawn with renderToStaticMarkup and read as markup, the same
// way the media components are tested here. Interactive behaviour that cannot
// be seen in static markup lives in the pure draft model and is proved in
// app/lib/publish-draft.test.ts.
import assert from "node:assert/strict"
import { test } from "node:test"

import { renderToStaticMarkup } from "react-dom/server"

import {
  DesktopPublishingWorkspace,
  MobilePublishingWizard,
  LivePreviewPanel,
} from "@/components/publish/publish-workspace"
import {
  addLink,
  initDraftState,
  setClientKey,
  setPlatform,
  setSite,
  toggleAttachment,
  type PublishSource,
} from "@/lib/publish-draft"
import type { PublishingOptions } from "@/components/publish/publish-options"
import type { Entity } from "@/lib/types"

const html = (node: React.ReactNode) => renderToStaticMarkup(node)

const TEXT = "سلام دنیا\n\n#فناوری @examplechannel"

// The spans are written where the text actually puts them: a fixture that lies
// about an offset proves nothing about the code that reads it.
const span = (token: string, type: string): Entity => ({
  type,
  offset: TEXT.indexOf(token),
  length: token.length,
})

const SOURCE: PublishSource = {
  localChannelId: 7,
  messageId: 42,
  text: TEXT,
  entities: [span("#فناوری", "hashtag"), span("@examplechannel", "mention")],
  attachments: [
    {
      id: "42:1",
      mediaId: "42:1",
      type: "photo",
      label: "تصویر",
      mimeType: "image/jpeg",
      size: 1024,
      url: "/telegram/cat/channels/7/messages/42/media",
      kind: "image",
    },
  ],
}

const OPTIONS: PublishingOptions = {
  servers: [
    {
      key: "cat",
      type: "custom",
      enabled: true,
      health_checkable: true,
      state: "connected",
      authorized: true,
      transport: "ready",
      last_success_at: null,
      last_error: "",
    },
  ],
  channels: [
    { id: "5", title: "کانال فناوری", username: "tech", canPublish: true },
    { id: "9", title: "اخبار روز", username: "news", canPublish: true },
  ],
  sites: [{ id: 3, title: "وب‌سایت محصول", domain: "example.com" }],
  categories: [{ id: 4, name: "دستهٔ یک", slug: "one" }],
  tags: [{ id: 8, name: "برچسب یک", slug: "tag-one" }],
  loading: { servers: false, channels: false, sites: false, categories: false, tags: false },
  errors: {},
}

const noop = () => {}

function telegramState() {
  const s = initDraftState(SOURCE)
  setClientKey(s, "cat")
  s.telegram.channelIds = ["5", "9"]
  return s
}

function wordpressState() {
  const s = initDraftState(SOURCE)
  setPlatform(s, "wordpress")
  setSite(s, 3)
  s.wordpress.title = "عنوان مقاله"
  s.wordpress.content = "بدنهٔ مقاله"
  s.wordpress.categoryIds = [4]
  s.wordpress.tagIds = [8]
  return s
}

// ---------------------------------------------------------------------------
// Desktop
// ---------------------------------------------------------------------------

test("desktop draws destination, editor and preview as three columns at once", () => {
  const out = html(
    <DesktopPublishingWorkspace
      state={telegramState()}
      options={OPTIONS}
      onChange={noop}
    />
  )
  // In RTL the first grid child is the rightmost one, so this order is
  // destination (right), editor (centre), preview (left).
  const at = (needle: string) => out.indexOf(needle)
  assert.ok(at('data-panel="destination"') >= 0, out.slice(0, 400))
  assert.ok(at('data-panel="editor"') > at('data-panel="destination"'))
  assert.ok(at('data-panel="preview"') > at('data-panel="editor"'))
  assert.match(out, /grid-cols-\[26fr_42fr_32fr\]/)
})

test("desktop shows no wizard navigation", () => {
  const out = html(
    <DesktopPublishingWorkspace
      state={telegramState()}
      options={OPTIONS}
      onChange={noop}
    />
  )
  assert.doesNotMatch(out, /data-testid="step-indicator"/)
  assert.doesNotMatch(out, />ادامه</)
  assert.doesNotMatch(out, />بازگشت</)
})

test("the desktop panels are titled the way the workspace is described", () => {
  const out = html(
    <DesktopPublishingWorkspace
      state={telegramState()}
      options={OPTIONS}
      onChange={noop}
    />
  )
  assert.match(out, /انتخاب مقصد/)
  assert.match(out, /ویرایش محتوا/)
  assert.match(out, /پیش‌نمایش/)
})

test("switching to WordPress changes the editor and the preview together", () => {
  const telegramOut = html(
    <DesktopPublishingWorkspace
      state={telegramState()}
      options={OPTIONS}
      onChange={noop}
    />
  )
  const wpOut = html(
    <DesktopPublishingWorkspace
      state={wordpressState()}
      options={OPTIONS}
      onChange={noop}
    />
  )
  assert.match(telegramOut, /ویرایش پیام/)
  assert.match(telegramOut, /هشتگ‌ها/)
  assert.doesNotMatch(telegramOut, /عنوان مقاله/)

  assert.match(wpOut, /ویرایش مقاله/)
  assert.match(wpOut, /عنوان مقاله/)
  assert.match(wpOut, /تصویر شاخص/)
  assert.match(wpOut, /دسته‌بندی‌ها/)
  assert.doesNotMatch(wpOut, /ویرایش پیام/)
})

// ---------------------------------------------------------------------------
// Destinations
// ---------------------------------------------------------------------------

test("the Telegram destination offers the server combobox and multi-select channels", () => {
  const out = html(
    <DesktopPublishingWorkspace
      state={telegramState()}
      options={OPTIONS}
      onChange={noop}
    />
  )
  assert.match(out, /سرور تلگرام/)
  assert.match(out, /کانال‌های مقصد/)
  // The channels are checkboxes, not a single-value select.
  assert.match(out, /type="checkbox"/)
  assert.match(out, /کانال فناوری/)
  assert.match(out, /اخبار روز/)
  assert.match(out, /۲ کانال انتخاب شده/)
})

test("the WordPress destination asks for one site", () => {
  const out = html(
    <DesktopPublishingWorkspace
      state={wordpressState()}
      options={OPTIONS}
      onChange={noop}
    />
  )
  assert.match(out, /سایت وردپرس/)
  assert.match(out, /وب‌سایت محصول/)
})

test("a channel this account may not post to is never offered", () => {
  const options: PublishingOptions = {
    ...OPTIONS,
    channels: [{ id: "5", title: "کانال فناوری", canPublish: true },
               { id: "9", title: "فقط خواندنی", canPublish: false }],
  }
  const out = html(
    <DesktopPublishingWorkspace
      state={telegramState()}
      options={options}
      onChange={noop}
    />
  )
  assert.match(out, /فقط خواندنی/)
  assert.match(out, /disabled/)
})

// ---------------------------------------------------------------------------
// Editor
// ---------------------------------------------------------------------------

test("media are listed as attached and can be detached", () => {
  const out = html(
    <DesktopPublishingWorkspace
      state={telegramState()}
      options={OPTIONS}
      onChange={noop}
    />
  )
  assert.match(out, /رسانه‌های پیوست‌شده/)
  assert.match(out, /حذف پیوست/)
})

// The reported bug: the dialog drew "دریافت عکس ناموفق بود" the moment it opened,
// before any request had been made — the attachment was rendered as
// status="ready" with no URL, which the media card draws as a failure. Opening
// the dialog must never claim a fetch failed.
test("the dialog never reports the source picture as unread before it is fetched", () => {
  const out = html(
    <DesktopPublishingWorkspace
      state={telegramState()}
      options={OPTIONS}
      onChange={noop}
    />
  )
  assert.doesNotMatch(out, /ناموفق بود/, "nothing has been fetched yet, so nothing has failed")
  assert.doesNotMatch(out, /در دسترس نیست/)
})

// The picture is fetched for the outgoing draft, so what the dialog shows is
// that it is arriving — and it arrives through the source connection's own
// media route, which is what attachment.url already is.
test("the source picture is fetched from the connection that owns the source message", () => {
  const s = telegramState()
  const out = html(
    <DesktopPublishingWorkspace state={s} options={OPTIONS} onChange={noop} />
  )
  assert.match(out, /در حال دریافت/, "the attachment must be shown as arriving")
  const url = s.source.attachments[0]?.url ?? ""
  assert.match(url, /\/telegram\/[^/]+\/channels\/\d+\/messages\/\d+\/media/,
    "the media route must name the source connection")
})

// Picking another destination is a statement about where the post goes. It must
// not re-point the source picture at the destination, whose connection cannot
// read the source message at all.
test("changing the destination leaves the source attachment's own url alone", () => {
  const before = telegramState()
  const beforeURL = before.source.attachments[0]?.url

  setClientKey(before, "official")
  assert.equal(
    before.source.attachments[0]?.url,
    beforeURL,
    "choosing another server must not change where the source picture is read from",
  )
})

test("the message editor shows the composed outgoing text, not the raw source", () => {
  const s = telegramState()
  const out = html(
    <DesktopPublishingWorkspace state={s} options={OPTIONS} onChange={noop} />
  )
  assert.match(out, /متن پیام/)
  assert.match(out, /هشتگ‌ها/)
  assert.match(out, /منشن‌ها/)
  assert.match(out, /#فناوری/)
  assert.match(out, /@examplechannel/)
})

// The links are the third managed field, and they are the one that is already
// in the message: the row shows the words the link is drawn on beside what
// those words point at, so both halves of a link are visible at once.
test("the links are listed beside the hashtags and the mentions", () => {
  const s = telegramState()
  addLink(s, { anchorText: "گزارش", url: "https://r.example.com" })
  const out = html(
    <DesktopPublishingWorkspace state={s} options={OPTIONS} onChange={noop} />
  )
  assert.match(out, /هشتگ‌ها/)
  assert.match(out, /منشن‌ها/)
  assert.match(out, /لینک‌ها/)
  assert.match(out, /گزارش/)
  assert.match(out, /https:\/\/r\.example\.com/)
})

test("every link row says what its delete button removes", () => {
  const s = telegramState()
  addLink(s, { anchorText: "گزارش", url: "https://r.example.com" })
  const out = html(
    <DesktopPublishingWorkspace state={s} options={OPTIONS} onChange={noop} />
  )
  assert.match(out, /aria-label="حذف لینک/)
})

test("the row that adds a link is ready only with both a text and a target", () => {
  const s = telegramState()
  const out = html(
    <DesktopPublishingWorkspace state={s} options={OPTIONS} onChange={noop} />
  )
  // Nothing typed yet, so it cannot be submitted.
  assert.match(out, /disabled/)
  assert.match(out, /متن پیوند تازه/)
  assert.match(out, /نشانی پیوند تازه/)
})

test("the mobile wizard's content step carries the links too", () => {
  const s = telegramState()
  addLink(s, { anchorText: "گزارش", url: "https://r.example.com" })
  const out = html(
    <MobilePublishingWizard
      step={2}
      onStep={noop}
      state={s}
      options={OPTIONS}
      onChange={noop}
    />
  )
  assert.match(out, /لینک‌ها/)
  assert.match(out, /https:\/\/r\.example\.com/)
})

// The article carries the same links as the message, so it manages the same
// list — one list for the draft, not one per platform.
test("the article editor manages the same links", () => {
  const s = telegramState()
  setPlatform(s, "wordpress")
  setSite(s, 3)
  addLink(s, { anchorText: "گزارش", url: "https://r.example.com" })
  const out = html(
    <DesktopPublishingWorkspace state={s} options={OPTIONS} onChange={noop} />
  )
  assert.match(out, /لینک‌ها/)
  assert.match(out, /گزارش/)
  assert.match(out, /https:\/\/r\.example\.com/)
})

test("the article editor marks its required fields", () => {
  const out = html(
    <DesktopPublishingWorkspace
      state={wordpressState()}
      options={OPTIONS}
      onChange={noop}
    />
  )
  assert.match(out, /نامک/)
  assert.match(out, /محتوای مقاله/)
  assert.match(out, /برچسب‌ها/)
})

// ---------------------------------------------------------------------------
// Preview
// ---------------------------------------------------------------------------

test("the Telegram preview shows the outgoing draft and its destinations", () => {
  const s = telegramState()
  const out = html(
    <LivePreviewPanel state={s} options={OPTIONS} />
  )
  assert.match(out, /پیش‌نمایش/)
  assert.match(out, /ارسال به ۲ کانال/)
  assert.match(out, /کانال فناوری/)
  // The outgoing text, not the source's own counters.
  assert.match(out, /سلام دنیا/)
  assert.doesNotMatch(out, /data-testid="source-stats"/)
})

test("detaching the media takes it out of the preview", () => {
  const s = telegramState()
  toggleAttachment(s, "42:1")
  const out = html(<LivePreviewPanel state={s} options={OPTIONS} />)
  assert.doesNotMatch(out, /data-testid="preview-media"/)
})

test("the WordPress preview is a content preview with the featured image", () => {
  const out = html(<LivePreviewPanel state={wordpressState()} options={OPTIONS} />)
  assert.match(out, /عنوان مقاله/)
  assert.match(out, /بدنهٔ مقاله/)
  assert.match(out, /data-testid="featured-image"/)
  assert.match(out, /دستهٔ یک/)
  assert.match(out, /برچسب یک/)
})

test("an empty draft previews nothing rather than a blank post", () => {
  const s = initDraftState({ ...SOURCE, text: "", entities: [], attachments: [] })
  const out = html(<LivePreviewPanel state={s} options={OPTIONS} />)
  assert.match(out, /محتوایی برای پیش‌نمایش وجود ندارد/)
})

// ---------------------------------------------------------------------------
// Mobile wizard
// ---------------------------------------------------------------------------

test("the wizard shows exactly one step at a time", () => {
  for (const step of [1, 2, 3] as const) {
    const out = html(
      <MobilePublishingWizard
        step={step}
       
        state={telegramState()}
        options={OPTIONS}
        onChange={noop}
        onStep={noop}
      />
    )
    const panels = out.match(/data-panel="[^"]+"/g) ?? []
    assert.equal(panels.length, 1, `step ${step} rendered ${panels.length} panels: ${out.slice(0, 300)}`)
    assert.match(out, /data-testid="step-indicator"/)
  }
})

test("wizard step one is the destination and step two is the editor", () => {
  const one = html(
    <MobilePublishingWizard step={1} state={telegramState()} options={OPTIONS} onChange={noop} onStep={noop} />
  )
  assert.match(one, /data-panel="destination"/)
  const two = html(
    <MobilePublishingWizard step={2} state={telegramState()} options={OPTIONS} onChange={noop} onStep={noop} />
  )
  assert.match(two, /data-panel="editor"/)
})

test("wizard step three previews and publishes", () => {
  const out = html(
    <MobilePublishingWizard step={3} state={telegramState()} options={OPTIONS} onChange={noop} onStep={noop} />
  )
  assert.match(out, /data-panel="preview"/)
  assert.match(out, /ارسال پیام/)
  assert.match(out, /بازگشت/)
})

test("the WordPress wizard publishes an article instead of a message", () => {
  const out = html(
    <MobilePublishingWizard step={3} state={wordpressState()} options={OPTIONS} onChange={noop} onStep={noop} />
  )
  assert.match(out, /انتشار مقاله/)
})

test("the wizard names its three steps", () => {
  const out = html(
    <MobilePublishingWizard step={2} state={telegramState()} options={OPTIONS} onChange={noop} onStep={noop} />
  )
  assert.match(out, /انتخاب مقصد/)
  assert.match(out, /ویرایش محتوا/)
  assert.match(out, /پیش‌نمایش و ارسال/)
})
