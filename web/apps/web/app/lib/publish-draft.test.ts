// The publishing draft: one authoritative model both the desktop workspace and
// the mobile wizard edit, with no DOM and no network so every rule here is
// provable in node.
import assert from "node:assert/strict"
import { test } from "node:test"

import {
  buildTelegramPayloads,
  buildWordPressPayload,
  canAdvance,
  composeTelegramOutgoing,
  emptyTelegramDraft,
  emptyWordPressDraft,
  initDraftState,
  layoutFor,
  normalizeHashtags,
  normalizeMentions,
  setClientKey,
  setPlatform,
  setSite,
  setSlug,
  setWordPressTitle,
  suggestSlug,
  suggestTitle,
  toggleAttachment,
  validate,
  type PublishDraftState,
  type PublishSource,
} from "@/lib/publish-draft"
import type { Entity } from "@/lib/types"

const TEXT = "سلام دنیا\n\n#فناوری #اخبار @examplechannel"

// The source's spans are written where the text actually puts them, rather than
// at hand-counted offsets: a fixture that lies about an offset would prove
// nothing about the code that consumes it.
const TAG_ONE = "#فناوری"
const TAG_TWO = "#اخبار"
const MENTION = "@examplechannel"
const span = (token: string, type: string): Entity => ({
  type,
  offset: TEXT.indexOf(token),
  length: token.length,
})

const SOURCE: PublishSource = {
  localChannelId: 7,
  messageId: 42,
  text: TEXT,
  entities: [
    span(TAG_ONE, "hashtag"),
    span(TAG_TWO, "hashtag"),
    span(MENTION, "mention"),
    { type: "bold", offset: 0, length: 4 },
  ],
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
    {
      id: "42:2",
      mediaId: "42:2",
      type: "document",
      label: "فایل",
      filename: "report.pdf",
      mimeType: "application/pdf",
      size: 2048,
      url: "/telegram/cat/channels/7/messages/42/media",
      kind: "document",
    },
  ],
}

function state(over: Partial<PublishSource> = {}): PublishDraftState {
  return initDraftState({ ...SOURCE, ...over })
}

// ---------------------------------------------------------------------------
// Initialization
// ---------------------------------------------------------------------------

test("opening a message seeds both drafts from the source once", () => {
  const s = state()
  assert.equal(s.platform, "telegram")
  assert.equal(s.source.localChannelId, 7)
  assert.equal(s.source.messageId, 42)
  assert.deepEqual(s.telegram.hashtags, ["فناوری", "اخبار"])
  assert.deepEqual(s.telegram.mentions, ["@examplechannel"])
  // The main body is the source with the tokens extracted, never the raw text.
  assert.ok(!s.telegram.text.includes("#فناوری"))
  assert.ok(s.telegram.text.includes("سلام دنیا"))
})

test("every source attachment is attached by default", () => {
  const s = state()
  assert.equal(s.telegram.attachments.length, 2)
  assert.ok(s.telegram.attachments.every((a) => a.attached))
})

test("the first eligible image becomes the WordPress featured image", () => {
  const s = state()
  assert.equal(s.wordpress.featuredMediaId, "42:1")
})

test("a message with no image leaves the featured image empty", () => {
  const s = state({ attachments: [SOURCE.attachments[1]] })
  assert.equal(s.wordpress.featuredMediaId, null)
})

test("the WordPress title suggestion is the first meaningful line", () => {
  assert.equal(suggestTitle("   \n\n  سلام دنیا  \nردیف دوم"), "سلام دنیا")
  assert.equal(suggestTitle(""), "")
  assert.equal(suggestTitle("#فناوری اخبار"), "اخبار")
})

test("the source message is never written to", () => {
  const s = state()
  setPlatform(s, "wordpress")
  s.wordpress.title = "یک عنوان تازه"
  toggleAttachment(s, "42:1")
  assert.equal(SOURCE.attachments[0].type, "photo")
  assert.equal(s.source.text, TEXT)
  assert.equal(s.source.attachments.length, 2)
})

// ---------------------------------------------------------------------------
// Platform switching keeps both drafts
// ---------------------------------------------------------------------------

test("switching platforms keeps the edits made in each of them", () => {
  const s = state()
  s.telegram.text = "متن ویرایش‌شده"
  setPlatform(s, "wordpress")
  s.wordpress.title = "عنوان مقاله"
  setPlatform(s, "telegram")
  assert.equal(s.telegram.text, "متن ویرایش‌شده")
  assert.equal(s.wordpress.title, "عنوان مقاله")
  assert.equal(s.platform, "telegram")
})

test("a Telegram draft never carries WordPress destination identifiers", () => {
  const s = state()
  setPlatform(s, "wordpress")
  s.wordpress.siteId = 3
  setPlatform(s, "telegram")
  const payload = buildTelegramPayloads({ ...s, telegram: { ...s.telegram, clientKey: "cat", channelIds: ["5"] } })
  assert.equal(payload.length, 1)
  assert.ok(!("siteId" in s.telegram))
  assert.ok(!("categories" in s.telegram))
})

// ---------------------------------------------------------------------------
// Destination resets
// ---------------------------------------------------------------------------

test("changing the Telegram connection drops the channels of the old one", () => {
  const s = state()
  setClientKey(s, "cat")
  s.telegram.channelIds = ["5", "9"]
  setClientKey(s, "dog")
  assert.deepEqual(s.telegram.channelIds, [])
  assert.equal(s.telegram.clientKey, "dog")
  // Content edits survive the destination change.
  assert.deepEqual(s.telegram.hashtags, ["فناوری", "اخبار"])
})

test("changing the WordPress site drops that site's taxonomy and slug work", () => {
  const s = state()
  setPlatform(s, "wordpress")
  s.wordpress.siteId = 1
  s.wordpress.categoryIds = [4, 5]
  s.wordpress.tagIds = [8]
  setSite(s, 2)
  assert.deepEqual(s.wordpress.categoryIds, [])
  assert.deepEqual(s.wordpress.tagIds, [])
  assert.equal(s.wordpress.siteId, 2)
  // The article itself is the user's, and is kept.
  assert.notEqual(s.wordpress.title, "")
})

test("changing the WordPress site does not reuse another site's term ids", () => {
  const s = state()
  setPlatform(s, "wordpress")
  setSite(s, 1)
  s.wordpress.categoryIds = [4, 5]
  setSite(s, 2)
  const body = buildWordPressPayload(s)
  assert.deepEqual(body.post.categories, [])
})

// ---------------------------------------------------------------------------
// Hashtags and mentions
// ---------------------------------------------------------------------------

test("hashtags are normalised, deduplicated and never empty", () => {
  assert.deepEqual(normalizeHashtags(["#فناوری", "فناوری", "  ", "اخبار", "#اخبار"]), [
    "فناوری",
    "اخبار",
  ])
})

test("mentions keep whatever form the user typed and reject blanks", () => {
  assert.deepEqual(normalizeMentions(["@one", "two", "@two", " "]), ["@one", "two"])
})

test("changing hashtags does not duplicate a tag already in the body", () => {
  const s = state()
  // "#فناوری" was extracted into the chip list; putting it back in the body
  // must not produce a second one on the tag line.
  s.telegram.text = `${s.telegram.text} #فناوری`
  const out = composeTelegramOutgoing(s)
  const count = out.text.split("#فناوری").length - 1
  assert.equal(count, 1, out.text)
})

test("the composed message keeps a hashtag that lives inside a sentence", () => {
  const s = state()
  s.telegram.text = "خبر امروز #فناوری مهم بود"
  s.telegram.hashtags = ["اخبار"]
  const out = composeTelegramOutgoing(s)
  assert.ok(out.text.includes("خبر امروز #فناوری مهم بود"))
  // The managed tag line adds only what the body does not already carry.
  assert.ok(out.text.includes("#اخبار"))
})

test("the outgoing entities match the outgoing text, in UTF-16 units", () => {
  const s = state()
  const out = composeTelegramOutgoing(s)
  for (const e of out.entities) {
    assert.ok(e.offset >= 0, JSON.stringify(e))
    assert.ok(
      e.offset + e.length <= out.text.length,
      `span ${JSON.stringify(e)} falls outside ${JSON.stringify(out.text)}`,
    )
    assert.ok(e.length > 0)
  }
})

test("editing the body drops source offsets that no longer describe it", () => {
  const s = state()
  const before = composeTelegramOutgoing(s)
  assert.ok(before.entities.some((e) => e.type === "bold"), "the source bold span is kept while untouched")

  s.telegram.text = "کاملاً متن تازه"
  const after = composeTelegramOutgoing(s)
  assert.ok(
    !after.entities.some((e) => e.type === "bold"),
    "a stale offset must not be sent after the text changed",
  )
  // ...and the tokens are rebuilt from what the text actually says now.
  assert.ok(after.entities.every((e) => e.offset + e.length <= after.text.length))
})

test("an emoji and Persian text keep their spans on the right characters", () => {
  const s = state()
  s.telegram.text = "🙂 سلام @examplechannel"
  s.telegram.mentions = ["@examplechannel"]
  const out = composeTelegramOutgoing(s)
  const mention = out.entities.find((e) => e.type === "mention")
  assert.ok(mention, JSON.stringify(out.entities))
  assert.equal(out.text.slice(mention!.offset, mention!.offset + mention!.length), "@examplechannel")
})

// ---------------------------------------------------------------------------
// Attachments
// ---------------------------------------------------------------------------

test("detaching media changes only the draft, never the source", () => {
  const s = state()
  toggleAttachment(s, "42:1")
  assert.deepEqual(
    s.telegram.attachments.map((a) => [a.id, a.attached]),
    [["42:1", false], ["42:2", true]],
  )
  assert.equal(s.source.attachments.length, 2)
})

test("the Telegram payload carries only attached media", () => {
  const s = state()
  toggleAttachment(s, "42:1")
  toggleAttachment(s, "42:2")
  const [body] = buildTelegramPayloads({ ...s, telegram: { ...s.telegram, clientKey: "cat", channelIds: ["5"] } })
  assert.equal(body.media.mode, "omit")
})

test("an attached source image is preserved in the Telegram payload", () => {
  const s = state()
  const [body] = buildTelegramPayloads({ ...s, telegram: { ...s.telegram, clientKey: "cat", channelIds: ["5"] } })
  assert.equal(body.media.mode, "preserve_source")
  assert.equal(body.media.attachment_id, "42:1")
})

// ---------------------------------------------------------------------------
// Validation
// ---------------------------------------------------------------------------

test("Telegram needs a connection, a channel and some content", () => {
  const s = state()
  const empty = validate(s)
  assert.equal(empty.valid, false)
  assert.ok(empty.errors.destination)

  setClientKey(s, "cat")
  assert.equal(validate(s).valid, false)

  s.telegram.channelIds = ["5"]
  assert.equal(validate(s).valid, true)
})

test("empty text is allowed when media is still attached", () => {
  const s = state()
  setClientKey(s, "cat")
  s.telegram.channelIds = ["5"]
  s.telegram.text = "   "
  s.telegram.hashtags = []
  s.telegram.mentions = []
  assert.equal(validate(s).valid, true, JSON.stringify(validate(s).errors))
})

test("empty text with nothing attached is not a publishable message", () => {
  const s = state()
  setClientKey(s, "cat")
  s.telegram.channelIds = ["5"]
  s.telegram.text = ""
  s.telegram.hashtags = []
  s.telegram.mentions = []
  for (const a of s.telegram.attachments) toggleAttachment(s, a.id)
  const v = validate(s)
  assert.equal(v.valid, false)
  assert.ok(v.errors.content)
})

test("text beyond Telegram's limit is refused", () => {
  const s = state()
  setClientKey(s, "cat")
  s.telegram.channelIds = ["5"]
  s.telegram.text = "x".repeat(4097)
  const v = validate(s)
  assert.equal(v.valid, false)
  assert.ok(v.errors.content)
})

test("WordPress needs a site, a title and content", () => {
  // A source with nothing in it: the draft starts unfilled, so every rule shows.
  const s = initDraftState({ localChannelId: 7, messageId: 42, text: "", entities: [], attachments: [] })
  setPlatform(s, "wordpress")
  let v = validate(s)
  assert.equal(v.valid, false)
  assert.ok(v.errors.destination)
  assert.ok(v.errors.title)
  assert.ok(v.errors.content)

  setSite(s, 2)
  v = validate(s)
  assert.equal(v.valid, false)
  assert.ok(v.errors.title)
  assert.ok(v.errors.content)

  s.wordpress.title = "عنوان"
  v = validate(s)
  assert.equal(v.valid, false)
  assert.equal(v.errors.title, undefined)
  assert.ok(v.errors.content)

  s.wordpress.content = "بدنهٔ مقاله"
  v = validate(s)
  assert.equal(v.valid, true, JSON.stringify(v.errors))
})

test("WordPress validation never demands a category or a tag", () => {
  const s = state()
  setPlatform(s, "wordpress")
  setSite(s, 2)
  assert.deepEqual(validate(s).errors, {})
})

test("a WordPress featured image must be one of the source attachments", () => {
  const s = state()
  setPlatform(s, "wordpress")
  setSite(s, 2)
  s.wordpress.featuredMediaId = "99:9"
  const v = validate(s)
  assert.equal(v.valid, false)
  assert.ok(v.errors.featured)
})

test("an unfilled field is not an error before the user has touched it", () => {
  const s = initDraftState({ localChannelId: 7, messageId: 42, text: "", entities: [], attachments: [] })
  setPlatform(s, "wordpress")
  assert.equal(validate(s, { touched: {} }).errors.content, undefined)
  assert.ok(validate(s, { touched: { content: true } }).errors.content)
})

// ---------------------------------------------------------------------------
// Slugs
// ---------------------------------------------------------------------------

test("a Latin title suggests a slug and a Persian one leaves it to WordPress", () => {
  assert.equal(suggestSlug("Hello World!"), "hello-world")
  assert.equal(suggestSlug("سلام دنیا"), "")
  assert.equal(suggestSlug("Mixed متن here"), "mixed-here")
})

test("the slug follows the title until the user edits it", () => {
  const s = initDraftState({ localChannelId: 7, messageId: 42, text: "Hello World", entities: [], attachments: [] })
  setPlatform(s, "wordpress")
  assert.equal(s.wordpress.slug, "hello-world")
  setWordPressTitle(s, "Another Title")
  assert.equal(s.wordpress.slug, "another-title")
})

test("a slug the user typed is never overwritten", () => {
  const s = state()
  setPlatform(s, "wordpress")
  setSlug(s, "my-own-slug")
  setWordPressTitle(s, "A Whole New Title")
  assert.equal(s.wordpress.slug, "my-own-slug")
  assert.equal(s.wordpress.slugManuallyEdited, true)
})

// ---------------------------------------------------------------------------
// Payloads
// ---------------------------------------------------------------------------

test("one Telegram request is built per selected channel", () => {
  const s = state()
  setClientKey(s, "cat")
  s.telegram.channelIds = ["5", "9", "12"]
  const bodies = buildTelegramPayloads(s)
  assert.deepEqual(bodies.map((b) => b.destination.channel_id), ["5", "9", "12"])
  for (const b of bodies) {
    assert.equal(b.content.text, composeTelegramOutgoing(s).text)
    assert.equal(b.source.local_channel_id, 7)
    assert.equal(b.source.message_id, 42)
    assert.equal(b.media.mode, "preserve_source")
  }
})

test("a channel is named by the id of the connection that owns it", () => {
  const s = state()
  setClientKey(s, "cat")
  s.telegram.channelIds = ["5"]
  const [body] = buildTelegramPayloads(s)
  assert.equal(body.destination.channel_id, "5")
  assert.ok(!("client_key" in body.destination), "the connection is in the path, not the body")
})

test("the WordPress payload carries the fields the site needs", () => {
  const s = state()
  setPlatform(s, "wordpress")
  setSite(s, 3)
  s.wordpress.title = "عنوان"
  s.wordpress.slug = "onvan"
  s.wordpress.content = "بدنه"
  s.wordpress.categoryIds = [4, 5]
  s.wordpress.tagIds = [8]
  const body = buildWordPressPayload(s)
  assert.deepEqual(body.post, {
    title: "عنوان",
    slug: "onvan",
    content: "بدنه",
    status: "publish",
    categories: [4, 5],
    tag_ids: [8],
  })
  assert.equal(body.media.mode, "preserve_source")
  assert.equal(body.media.attachment_id, "42:1")
  assert.deepEqual(body.source, { local_channel_id: 7, message_id: 42 })
})

test("no WordPress payload ever carries a credential", () => {
  const s = state()
  setPlatform(s, "wordpress")
  setSite(s, 3)
  s.wordpress.content = "بدنه"
  const raw = JSON.stringify(buildWordPressPayload(s))
  assert.doesNotMatch(raw, /password|credential|token|secret|app_pass/i)
})

test("a Telegram payload never carries a credential either", () => {
  const s = state()
  setClientKey(s, "cat")
  s.telegram.channelIds = ["5"]
  const raw = JSON.stringify(buildTelegramPayloads(s))
  assert.doesNotMatch(raw, /password|credential|token|secret|session/i)
})

// ---------------------------------------------------------------------------
// Steps and layout
// ---------------------------------------------------------------------------

test("the mobile wizard refuses to advance on an invalid step", () => {
  const s = state()
  assert.equal(canAdvance(1, s), false)
  setClientKey(s, "cat")
  s.telegram.channelIds = ["5"]
  assert.equal(canAdvance(1, s), true)
  assert.equal(canAdvance(2, s), true)
  setPlatform(s, "wordpress")
  assert.equal(canAdvance(1, s), false, "a platform with no site is not a destination")
})

test("desktop shows the workspace and narrow widths show the wizard", () => {
  assert.equal(layoutFor(1440), "desktop")
  assert.equal(layoutFor(1280), "desktop")
  assert.equal(layoutFor(900), "wizard")
  assert.equal(layoutFor(768), "wizard")
  assert.equal(layoutFor(390), "wizard")
})

test("the empty drafts are what a fresh dialog starts from", () => {
  const tg = emptyTelegramDraft()
  assert.equal(tg.platform, "telegram")
  assert.deepEqual(tg.channelIds, [])
  const wp = emptyWordPressDraft()
  assert.equal(wp.platform, "wordpress")
  assert.deepEqual(wp.categoryIds, [])
  assert.equal(wp.slugManuallyEdited, false)
})
