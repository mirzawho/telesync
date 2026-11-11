// The publishing draft: the one model the desktop workspace and the mobile
// wizard both edit.
//
// A dialog opened for one source message builds one of these and everything the
// user does lands in it. The source message itself is read once and never
// written to: the outgoing post is a separate, editable thing, and the preview
// and the request bodies are both derived from the draft rather than kept in
// step by hand.
//
// Nothing here touches the DOM, the network or React. Mutators change the state
// they are given and return it, so a caller can chain them; the React hook wraps
// each call in a shallow copy to make the change visible to the renderer.

import { compose, lexEntities, parse, type ComposedContent, type Entity } from "./content"
import { mediaKind, type MediaKind } from "./media-view"
import type { MessageMedia, MediaType } from "./types"

// ---------------------------------------------------------------------------
// Source
// ---------------------------------------------------------------------------

// SourceAttachment is one attachment of the message being republished. It is a
// normalised view of the API's `media` field: the message may carry none, one,
// or (should the API ever grow one) a list.
export interface SourceAttachment {
  // id is stable within one draft, and is what its selections reference. The
  // API may omit a media id entirely, so this never relies on there being one.
  id: string
  // mediaId is the API's own media id. It is opaque — this build writes
  // "channel:message" into it — so a request hands it back and nothing ever
  // reads a position out of it. It is empty when the API reported none.
  mediaId: string
  type: MediaType
  kind: MediaKind
  label?: string
  filename?: string
  mimeType?: string
  size?: number
  // url is the API path that streams the bytes, never an absolute URL.
  url?: string
}

export interface PublishSource {
  localChannelId: number
  messageId: number
  messageIds?: number[]
  text: string
  entities: Entity[]
  attachments: SourceAttachment[]
}

/** sourceAttachmentsFrom normalises the API's attachment field into a list. */
export function sourceAttachmentsFrom(media: MessageMedia | null | undefined): SourceAttachment[] {
  if (!media) return []
  return [
    {
      id: media.id ?? "source-media",
      mediaId: media.id ?? "",
      type: media.type,
      kind: mediaKind(media),
      label: media.label,
      filename: media.filename,
      mimeType: media.mime_type,
      size: media.size,
      url: media.url,
    },
  ]
}

// ---------------------------------------------------------------------------
// Drafts
// ---------------------------------------------------------------------------

export type PublishPlatform = "telegram" | "wordpress"

/** AttachmentState is whether one source attachment goes out with the message. */
export interface AttachmentState {
  // id is the SourceAttachment's own id, not the API's media id.
  id: string
  attached: boolean
}

/**
 * LinkItem is one link of the outgoing message.
 *
 * It is the only token that carries a target, so it keeps both sides of one:
 * the `anchorText` that is drawn in the body and the `url` it points at. The
 * anchor text is ordinary text and stays where the message put it — a link is
 * not lifted out of the body the way a hashtag is.
 *
 * There is one list for the whole draft, not one per platform: a link the
 * source message carried is a link of the message, and the Telegram post and
 * the WordPress article are two ways of sending it. Both bodies keep the anchor
 * text and both are rewritten through this list.
 */
export interface LinkItem {
  // id is stable within one draft, and is what the editor's rows reference.
  id: string
  anchorText: string
  url: string
}

export interface TelegramPublishDraft {
  platform: "telegram"
  // clientKey is the Telegram connection's real key, never its display label.
  clientKey: string | null
  // channelIds are Telegram channel ids on that connection, as decimal strings.
  channelIds: string[]
  // text is the editable body with the managed hashtags and mentions already
  // taken out of it; compose puts them back exactly once.
  text: string
  // hashtags are stored without their leading '#'.
  hashtags: string[]
  // mentions keep whatever form the user typed, '@' included or not.
  mentions: string[]
  attachments: AttachmentState[]
  silent: boolean
  disableWebPagePreview: boolean
}

export interface WordPressPublishDraft {
  platform: "wordpress"
  siteId: number | null
  title: string
  // slug is the post's slug. Empty means "let WordPress generate it".
  slug: string
  // slugManuallyEdited stops the title from rewriting a slug the user chose.
  slugManuallyEdited: boolean
  content: string
  // categoryIds and tagIds are that site's own WordPress term ids. They are
  // dropped when the site changes: another site's ids name nothing here.
  categoryIds: number[]
  tagIds: number[]
  // featuredMediaId names a *source* attachment. The WordPress attachment id it
  // becomes is resolved at publish time and never kept, so no id belonging to
  // one site can leak into a request for another.
  featuredMediaId: string | null
  commentStatus: "open" | "closed"
}

export interface PublishDraftState {
  source: PublishSource
  platform: PublishPlatform
  // Both drafts are kept, so switching platforms never discards an edit.
  telegram: TelegramPublishDraft
  wordpress: WordPressPublishDraft
  // links are the draft's links, shared by both bodies: the anchor text sits
  // in each of them and the target is sent with it.
  links: LinkItem[]
  // sourceEntities are the source's formatting spans, remapped onto the
  // extracted body. They are only sent while the body is still the source's.
  sourceEntities: Entity[]
  // baselineText is what the body was right after extraction. Every edit is
  // measured against it, which is how stale offsets are kept out of a request.
  baselineText: string
  // touched records which fields the user has actually reached. A field nobody
  // has filled in is not an error until it is, so a fresh dialog is not a wall
  // of red.
  touched: Partial<Record<ValidationField, boolean>>
}

export function emptyTelegramDraft(): TelegramPublishDraft {
  return {
    platform: "telegram",
    clientKey: null,
    channelIds: [],
    text: "",
    hashtags: [],
    mentions: [],
    attachments: [],
    silent: false,
    disableWebPagePreview: false,
  }
}

export function emptyWordPressDraft(): WordPressPublishDraft {
  return {
    platform: "wordpress",
    siteId: null,
    title: "",
    slug: "",
    slugManuallyEdited: false,
    content: "",
    categoryIds: [],
    tagIds: [],
    featuredMediaId: null,
    commentStatus: "open",
  }
}

/**
 * initDraftState seeds both drafts from one source message.
 *
 * It runs once per message: hashtags, mentions and formatting come out of the
 * source's entities, every attachment starts attached, and the article fields
 * take a conservative first suggestion. Opening another message calls this
 * again on purpose — a draft is never silently reseeded underneath an edit.
 */
export function initDraftState(input: PublishSource): PublishDraftState {
  const source: PublishSource = {
    ...input,
    entities: input.entities.map((e) => ({ ...e })),
    attachments: input.attachments.map((a) => ({ ...a })),
  }
  const parsed = parse(source.text, source.entities)
  const title = suggestTitle(source.text)
  const firstImage = source.attachments.find((a) => a.kind === "image")

  return {
    source,
    platform: "telegram",
    telegram: {
      ...emptyTelegramDraft(),
      text: parsed.main,
      hashtags: parsed.hashtags,
      mentions: parsed.mentions,
      attachments: source.attachments.map((a) => ({ id: a.id, attached: true })),
    },
    wordpress: {
      ...emptyWordPressDraft(),
      title,
      slug: suggestSlug(title),
      content: source.text,
      featuredMediaId: firstImage ? firstImage.id : null,
    },
    links: parsed.links.map((l, i) => ({ id: `link-${i + 1}`, ...l })),
    sourceEntities: parsed.entities,
    baselineText: parsed.main,
    touched: {},
  }
}

/** marks one field as reached, so its message may be shown beside it. */
function touch(state: PublishDraftState, ...fields: ValidationField[]): void {
  for (const field of fields) state.touched = { ...state.touched, [field]: true }
}

// ---------------------------------------------------------------------------
// Links
// ---------------------------------------------------------------------------

/** LocatedLink is a link together with the span its anchor text covers. */
export interface LocatedLink extends LinkItem {
  start: number
  end: number
}

/**
 * locateLinks finds each link's anchor text in the body.
 *
 * Links are matched in the order they were listed, each one after the previous
 * one ends, so two links that share an anchor text are not confused and a link
 * is never moved onto the wrong words. A link whose anchor text is no longer in
 * the body is simply not there: the list follows the text, never the other way
 * round.
 */
export function locateLinks(text: string, links: readonly LinkItem[]): LocatedLink[] {
  const out: LocatedLink[] = []
  let cursor = 0
  for (const link of links) {
    if (!link.anchorText) continue
    const at = text.indexOf(link.anchorText, cursor)
    if (at < 0) continue
    out.push({ ...link, start: at, end: at + link.anchorText.length })
    cursor = at + link.anchorText.length
  }
  return out
}

/**
 * reconcileLinks is the list that matches the bodies the user has just typed.
 *
 * A link survives while its anchor text is still in **either** body: the
 * Telegram post and the WordPress article are two renderings of one message,
 * and dropping a link because the article was rewritten would take it out of
 * the post too. A bare URL either body now contains becomes a link of its own,
 * so the list never shows fewer links than the message carries.
 */
function reconcileLinks(texts: readonly string[], links: readonly LinkItem[]): LinkItem[] {
  // A link survives while either body still shows its anchor text.
  const alive: LinkItem[] = []
  for (const link of links) {
    const gone = texts.every((t) => t.indexOf(link.anchorText) < 0)
    if (!gone) alive.push({ id: link.id, anchorText: link.anchorText, url: link.url })
  }

  // A bare URL that no link already covers becomes a link of its own.
  const extra: LinkItem[] = []
  for (const text of texts) {
    for (const m of text.matchAll(/https?:\/\/[^\s>]+/gi)) {
      const start = m.index!
      const end = start + m[0].length
      const covered = [...alive, ...extra].some((l) => {
        const at = text.indexOf(l.anchorText)
        return at >= 0 && start < at + l.anchorText.length && end > at
      })
      if (covered) continue
      extra.push({ id: "", anchorText: m[0], url: m[0] })
    }
  }

  // A surviving link keeps its id, so a row the user is looking at is never
  // renamed underneath them. A link the text just gained is appended, which is
  // where it sits in the body too.
  const out = [...alive, ...extra]
  const used = new Set(out.map((l) => l.id))
  return out.map((l) => {
    if (l.id) return l
    let n = 0
    while (used.has(`link-${++n}`)) {
      // the first id that is not already taken
    }
    used.add(`link-${n}`)
    return { ...l, id: `link-${n}` }
  })
}

function nextLinkId(links: readonly LinkItem[]): string {
  let n = 0
  const used = new Set(links.map((l) => l.id))
  while (used.has(`link-${++n}`)) {
    // the first id that is not already taken
  }
  return `link-${n}`
}

// ---------------------------------------------------------------------------
// Mutators
// ---------------------------------------------------------------------------

/** setPlatform switches the active platform. Both drafts survive it. */
export function setPlatform(state: PublishDraftState, platform: PublishPlatform): PublishDraftState {
  state.platform = platform
  return state
}

/**
 * setClientKey selects the Telegram connection.
 *
 * The channels of the previous connection are dropped with it: a channel id
 * only means something inside the connection that reported it, and carrying it
 * over would address a different channel entirely.
 */
export function setClientKey(state: PublishDraftState, clientKey: string | null): PublishDraftState {
  state.telegram = { ...state.telegram, clientKey, channelIds: [] }
  touch(state, "destination")
  return state
}

export function setChannelIds(state: PublishDraftState, channelIds: string[]): PublishDraftState {
  state.telegram = { ...state.telegram, channelIds: [...channelIds] }
  touch(state, "destination")
  return state
}

/**
 * setTelegramText records a body the user typed.
 *
 * The link list follows it. A link whose anchor text is gone from the body goes
 * with it — its target belonged to words that are no longer there — and a bare
 * URL the text gained becomes a link of its own.
 */
export function setTelegramText(state: PublishDraftState, text: string): PublishDraftState {
  state.telegram = { ...state.telegram, text }
  state.links = reconcileLinks([text, state.wordpress.content], state.links)
  touch(state, "content", "links")
  return state
}

/**
 * addLink appends a link to the message.
 *
 * A link the user adds is placed at the end of the message, on its own line: it
 * is appended, never spliced into the middle of a sentence, and a line of its
 * own is what keeps it from merging into the text or the URL just before it.
 */
export function addLink(
  state: PublishDraftState,
  link: { anchorText: string; url: string },
): PublishDraftState {
  const anchorText = link.anchorText.trim()
  const url = link.url.trim()
  if (!anchorText || !url) return state
  state.telegram = { ...state.telegram, text: appendAnchor(state.telegram.text, anchorText) }
  state.wordpress = { ...state.wordpress, content: appendAnchor(state.wordpress.content, anchorText) }
  state.links = [...state.links, { id: nextLinkId(state.links), anchorText, url }]
  touch(state, "content", "links")
  return state
}

/** appendAnchor writes a link's words at the end of a body, on their own line. */
function appendAnchor(body: string, anchorText: string): string {
  const text = body.replace(/[ \t\r\n]+$/, "")
  return text.length > 0 ? `${text}\n${anchorText}` : anchorText
}

/**
 * editLink changes one link's anchor text or its target.
 *
 * Renaming the anchor text rewrites it in the body at the span this link
 * already covers, so a link is never re-pointed at different words. Changing
 * the target leaves the text alone.
 */
export function editLink(
  state: PublishDraftState,
  id: string,
  patch: { anchorText?: string; url?: string },
): PublishDraftState {
  const target = state.links.find((l) => l.id === id)
  if (!target) return state
  const anchorText = patch.anchorText ?? target.anchorText
  const url = patch.url ?? target.url
  if (anchorText === target.anchorText) {
    state.links = state.links.map((l) => (l.id === id ? { ...l, anchorText, url } : l))
    touch(state, "links")
    return state
  }
  // The rename is written into whichever body carries this link's words, and
  // into both when both do: the two renderings of the message stay the same
  // message.
  const rename = (text: string): string => {
    const at = locateLinks(text, state.links).find((l) => l.id === id)
    return at ? text.slice(0, at.start) + anchorText + text.slice(at.end) : text
  }
  state.telegram = { ...state.telegram, text: rename(state.telegram.text) }
  state.wordpress = { ...state.wordpress, content: rename(state.wordpress.content) }
  state.links = state.links.map((l) => (l.id === id ? { ...l, anchorText, url } : l))
  touch(state, "content", "links")
  return state
}

/**
 * deleteLink takes a link out of the message.
 *
 * Its anchor text goes with it: a phrase that was only ever the words a link
 * was drawn on is left meaningless by leaving it behind, and a URL the user
 * deleted is not kept in the text as a stray one.
 */
export function deleteLink(state: PublishDraftState, id: string): PublishDraftState {
  if (!state.links.some((l) => l.id === id)) return state
  const remove = (text: string): string => {
    const at = locateLinks(text, state.links).find((l) => l.id === id)
    if (!at) return text
    let end = at.end
    if (text[end] === " ") end += 1
    return text.slice(0, at.start) + text.slice(end)
  }
  state.telegram = { ...state.telegram, text: remove(state.telegram.text) }
  state.wordpress = { ...state.wordpress, content: remove(state.wordpress.content) }
  state.links = state.links.filter((l) => l.id !== id)
  touch(state, "content", "links")
  return state
}

/**
 * setSite selects the WordPress destination.
 *
 * The article keeps its title, body and slug — those belong to the article. The
 * term ids do not: categories and tags are rows in one site's taxonomy and are
 * meaningless on another, so they are cleared rather than reinterpreted.
 */
export function setSite(state: PublishDraftState, siteId: number | null): PublishDraftState {
  state.wordpress = {
    ...state.wordpress,
    siteId,
    categoryIds: [],
    tagIds: [],
  }
  touch(state, "destination")
  return state
}

export function setCategoryIds(state: PublishDraftState, categoryIds: number[]): PublishDraftState {
  state.wordpress = { ...state.wordpress, categoryIds: [...categoryIds] }
  return state
}

export function setTagIds(state: PublishDraftState, tagIds: number[]): PublishDraftState {
  state.wordpress = { ...state.wordpress, tagIds: [...tagIds] }
  return state
}

export function setWordPressTitle(state: PublishDraftState, title: string): PublishDraftState {
  const slug = state.wordpress.slugManuallyEdited ? state.wordpress.slug : suggestSlug(title)
  state.wordpress = { ...state.wordpress, title, slug }
  touch(state, "title")
  return state
}

export function setWordPressContent(state: PublishDraftState, content: string): PublishDraftState {
  state.wordpress = { ...state.wordpress, content }
  state.links = reconcileLinks([state.telegram.text, content], state.links)
  touch(state, "content", "links")
  return state
}

/** setSlug records a slug the user typed, and stops the title from rewriting it. */
export function setSlug(state: PublishDraftState, slug: string): PublishDraftState {
  state.wordpress = { ...state.wordpress, slug, slugManuallyEdited: true }
  return state
}

export function setFeaturedMediaId(state: PublishDraftState, mediaId: string | null): PublishDraftState {
  state.wordpress = { ...state.wordpress, featuredMediaId: mediaId }
  touch(state, "featured")
  return state
}

/**
 * toggleAttachment attaches or detaches one source attachment.
 *
 * Detaching is a statement about the outgoing post only. The source message,
 * its attachment and the stored record behind it are all left alone: nothing is
 * deleted from anywhere.
 */
export function toggleAttachment(state: PublishDraftState, id: string): PublishDraftState {
  state.telegram = {
    ...state.telegram,
    attachments: state.telegram.attachments.map((a) =>
      a.id === id ? { ...a, attached: !a.attached } : a,
    ),
  }
  return state
}

export function setHashtags(state: PublishDraftState, hashtags: string[]): PublishDraftState {
  state.telegram = { ...state.telegram, hashtags: normalizeHashtags(hashtags) }
  return state
}

export function setMentions(state: PublishDraftState, mentions: string[]): PublishDraftState {
  state.telegram = { ...state.telegram, mentions: normalizeMentions(mentions) }
  return state
}

// ---------------------------------------------------------------------------
// Normalisation
// ---------------------------------------------------------------------------

/**
 * normalizeHashtags is the canonical form of a hashtag list: no leading '#',
 * no blanks, no duplicates. The '#' is presentation and is added back when the
 * outgoing message is composed, so it is never stored twice.
 */
export function normalizeHashtags(values: readonly string[]): string[] {
  const out: string[] = []
  const seen = new Set<string>()
  for (const raw of values) {
    const value = raw.trim().replace(/^#+/, "").trim()
    if (!value) continue
    const key = value.toLowerCase()
    if (seen.has(key)) continue
    seen.add(key)
    out.push(value)
  }
  return out
}

/**
 * normalizeMentions deduplicates mentions on the username underneath, keeping
 * the first spelling the user gave.
 *
 * A mention is not rewritten into "@something" on the way in: the distinction
 * between a plain-text @username and one that names a real account is the
 * user's to keep, and this module refuses to make it for them.
 */
export function normalizeMentions(values: readonly string[]): string[] {
  const out: string[] = []
  const seen = new Set<string>()
  for (const raw of values) {
    const value = raw.trim()
    if (!value) continue
    const key = value.replace(/^@+/, "").trim().toLowerCase()
    if (!key || seen.has(key)) continue
    seen.add(key)
    out.push(value)
  }
  return out
}

// ---------------------------------------------------------------------------
// Composition
// ---------------------------------------------------------------------------

function mergeEntities(a: readonly Entity[], b: readonly Entity[]): Entity[] {
  const seen = new Set<string>()
  const out: Entity[] = []
  for (const e of [...a, ...b]) {
    const key = `${e.type}:${e.offset}:${e.length}`
    if (seen.has(key)) continue
    seen.add(key)
    out.push({ ...e })
  }
  out.sort((x, y) => x.offset - y.offset || x.length - y.length)
  return out
}

/**
 * composeTelegramOutgoing is the exact message that would be sent.
 *
 * The body, the hashtag line and the mention line are assembled once here, and
 * the preview and the request body both read this — never the editor's raw
 * value — so what is shown is what is sent. A tag or mention already present in
 * the body is not appended a second time.
 *
 * The entity policy is the conservative one. While the body is still exactly
 * the source's, its formatting spans are carried over with their remapped
 * offsets. The moment the body is edited those offsets describe nothing, so
 * they are dropped and the outgoing spans are read back from the finished text
 * instead: a span that would be wrong is never sent.
 */
export function composeTelegramOutgoing(state: PublishDraftState): ComposedContent {
  const draft = state.telegram
  const bodyEdited = draft.text !== state.baselineText
  const carried = bodyEdited ? [] : state.sourceEntities
  const composed = compose(draft.text, draft.hashtags, draft.mentions, carried)

  // The link list is the only thing that knows a link's target, so it writes
  // the link spans and every other source of them stands down. Otherwise the
  // source's stale target, or the lexer's bare URL, would sit on the same words
  // as the one the user edited.
  const links = linkEntities(composed.text, state.links)
  const claimed = new Set(links.map((e) => `${e.offset}:${e.length}`))
  const base = mergeEntities(composed.entities, lexEntities(composed.text)).filter(
    (e) => (e.type !== "url" && e.type !== "text_url") || !claimed.has(`${e.offset}:${e.length}`),
  )
  return { text: composed.text, entities: mergeEntities(base, links) }
}

/**
 * linkEntities writes the outgoing span of every link the body still carries.
 *
 * A link whose anchor text is the URL itself is Telegram's plain `url`; one
 * drawn on other words is a `text_url`, which is the only entity that can say
 * what those words point at.
 */
function linkEntities(text: string, links: readonly LinkItem[]): Entity[] {
  const out: Entity[] = []
  for (const l of locateLinks(text, links)) {
    out.push(
      l.anchorText === l.url
        ? { type: "url", offset: l.start, length: l.end - l.start }
        : { type: "text_url", offset: l.start, length: l.end - l.start, url: l.url },
    )
  }
  return out
}

/** attachedMedia lists the source attachments that go out with the message. */
export function attachedMedia(state: PublishDraftState): SourceAttachment[] {
  const on = new Set(state.telegram.attachments.filter((a) => a.attached).map((a) => a.id))
  return state.source.attachments.filter((a) => on.has(a.id))
}

// ---------------------------------------------------------------------------
// Suggestions
// ---------------------------------------------------------------------------

/**
 * suggestTitle is the first meaningful line of the source.
 *
 * A line that is nothing but hashtags and mentions is skipped, because a title
 * of "#فناوری #اخبار" is not a title. It is only ever a starting point: the
 * draft owns the title from then on and nothing here rewrites it again.
 */
export function suggestTitle(text: string): string {
  const line = text.split("\n").find((l) => l.trim().length > 0) ?? ""
  return line.replace(/^(\s*[#@][\p{L}\p{N}_]+\s*)+/u, "").trim().slice(0, 120)
}

/**
 * suggestSlug turns a Latin title into a slug and leaves the rest alone.
 *
 * There is no transliteration here on purpose: mapping Persian to Latin is a
 * choice about the site's permanent URLs, and a guess is worse than none. A
 * title with no Latin in it suggests no slug, and WordPress generates one.
 */
export function suggestSlug(title: string): string {
  return title
    .toLowerCase()
    .replace(/[^a-z0-9]+/g, "-")
    .replace(/^-+|-+$/g, "")
}

// ---------------------------------------------------------------------------
// Validation
// ---------------------------------------------------------------------------

export type ValidationField = "destination" | "content" | "title" | "featured" | "links"

export interface DraftValidation {
  valid: boolean
  errors: Partial<Record<ValidationField, string>>
}

export interface ValidateOptions {
  /**
   * touched narrows the *reported* errors to fields the user has reached. The
   * validity of the draft is measured either way, so the publish button is
   * never enabled by a field nobody has looked at yet.
   */
  touched?: Partial<Record<ValidationField, boolean>>
}

// TELEGRAM_TEXT_LIMIT is the length Telegram accepts for a message or a caption,
// in UTF-16 code units — the same unit a JavaScript string index is in.
export const TELEGRAM_TEXT_LIMIT = 4096

/** validate is the one rule set, shared by every layout. */
export function validate(state: PublishDraftState, options: ValidateOptions = {}): DraftValidation {
  const all: Partial<Record<ValidationField, string>> = {}

  if (state.platform === "telegram") {
    const draft = state.telegram
    if (!draft.clientKey) {
      all.destination = "سرور تلگرام را انتخاب کنید."
    } else if (draft.channelIds.length === 0) {
      all.destination = "دست‌کم یک کانال مقصد را انتخاب کنید."
    }
    const composed = composeTelegramOutgoing(state)
    const hasMedia = attachedMedia(state).length > 0
    if (composed.text.trim().length === 0 && !hasMedia) {
      all.content = "متن پیام یا یک پیوست لازم است."
    } else if (composed.text.length > TELEGRAM_TEXT_LIMIT) {
      all.content = `متن پیام بیش از ${TELEGRAM_TEXT_LIMIT} نویسه است.`
    }
    const broken = state.links.find((l) => !l.url.trim() || !l.anchorText.trim())
    if (broken) {
      all.links = "هر لینک به متن پیوند و نشانی نیاز دارد."
    }
  } else {
    const draft = state.wordpress
    if (draft.siteId === null) {
      all.destination = "سایت وردپرس را انتخاب کنید."
    }
    if (draft.title.trim().length === 0) {
      all.title = "عنوان مقاله لازم است."
    }
    if (draft.content.trim().length === 0) {
      all.content = "محتوای مقاله لازم است."
    }
    if (draft.featuredMediaId !== null) {
      const known = state.source.attachments.some((a) => a.id === draft.featuredMediaId)
      if (!known) all.featured = "تصویر شاخص باید از پیوست‌های همین پیام انتخاب شود."
    }
  }

  const touched = options.touched
  const errors: Partial<Record<ValidationField, string>> = {}
  for (const [field, message] of Object.entries(all) as [ValidationField, string][]) {
    if (touched === undefined || touched[field]) errors[field] = message
  }

  return { valid: Object.keys(all).length === 0, errors }
}

/**
 * canAdvance says whether the mobile wizard may leave a step. Step one is the
 * destination, step two the content; a step never moves forward on a draft that
 * could not be published from the next one.
 */
export function canAdvance(step: number, state: PublishDraftState): boolean {
  const { valid, errors } = validate(state)
  if (step <= 1) return !errors.destination
  if (step === 2) return valid || (!errors.destination && !errors.content)
  return valid
}

export type WorkspaceLayout = "desktop" | "wizard"

/**
 * layoutFor picks the presentation for a viewport width.
 *
 * Three columns are only drawn where they are wide enough to be usable; every
 * narrower width gets the wizard rather than three unusable panes.
 */
export function layoutFor(width: number): WorkspaceLayout {
  return width >= 1200 ? "desktop" : "wizard"
}

// ---------------------------------------------------------------------------
// Request bodies
// ---------------------------------------------------------------------------

export interface TelegramPublishBody {
  source: { local_channel_id: number; message_id: number; message_ids?: number[] }
  // destination carries the channel id only. The connection is the path's own
  // segment, so a body can never name a channel on the wrong one.
  destination: { channel_id: string }
  content: { text: string; entities: Entity[] }
  media: { mode: "preserve_source" | "omit"; attachment_id?: string }
  options: { silent: boolean; disable_web_page_preview: boolean }
}

export interface WordPressPublishBody {
  source: { local_channel_id: number; message_id: number }
  post: {
    title: string
    slug: string
    content: string
    status: string
    // categories and tag_ids are this site's WordPress term ids. Telegram
    // hashtags are never sent as term ids: the two are different things.
    categories: number[]
    tag_ids: number[]
  }
  media: { mode: "preserve_source" | "omit"; attachment_id?: string }
  options: { comment_status: string }
}

/**
 * buildTelegramPayloads builds one request per selected channel.
 *
 * The backend sends to one channel per call, so a multi-channel publish is a
 * list of those. Each carries the same content and its own destination, which
 * is what lets the results be reported per channel instead of all-or-nothing.
 */
export function buildTelegramPayloads(state: PublishDraftState): TelegramPublishBody[] {
  const composed = composeTelegramOutgoing(state)
  const attached = attachedMedia(state)
  const media = mediaFor(attached)
  const source = {
    local_channel_id: state.source.localChannelId,
    message_id: state.source.messageId,
    ...(state.source.messageIds?.length ? { message_ids: state.source.messageIds } : {}),
  }
  return state.telegram.channelIds.map((channelId) => ({
    source,
    destination: { channel_id: channelId },
    content: { text: composed.text, entities: composed.entities },
    media,
    options: {
      silent: state.telegram.silent,
      disable_web_page_preview: state.telegram.disableWebPagePreview,
    },
  }))
}

// mediaFor is the request's media block. It names the attachment by the id the
// API gave it, and names none when the API gave none: an id is handed back, not
// invented, and never read as a coordinate.
function mediaFor(attached: SourceAttachment[]): { mode: "preserve_source" | "omit"; attachment_id?: string } {
  const chosen = attached.find((a) => a.mediaId !== "")
  if (!chosen) return attached.length > 0 ? { mode: "preserve_source" as const } : { mode: "omit" as const }
  return { mode: "preserve_source" as const, attachment_id: chosen.mediaId }
}

/**
 * composeWordPressContent is the article's HTML.
 *
 * The article body keeps each link's words where the sentence put them, and
 * only the target is added here: the words are wrapped in the anchor they
 * belong to. Everything else in the body is left byte-for-byte as the user
 * wrote it, so markup they typed themselves is never reinterpreted or escaped.
 */
export function composeWordPressContent(state: PublishDraftState): string {
  const content = state.wordpress.content
  let out = ""
  let cursor = 0
  for (const l of locateLinks(content, state.links)) {
    out += content.slice(cursor, l.start)
    out += `<a href="${escapeAttribute(l.url)}">${escapeText(l.anchorText)}</a>`
    cursor = l.end
  }
  return out + content.slice(cursor)
}

// escapeAttribute and escapeText are the only two places a link's own values
// reach the HTML, so a target or a label the user typed cannot close the tag it
// is written into.
function escapeAttribute(value: string): string {
  return value.replace(/&/g, "&amp;").replace(/"/g, "&quot;").replace(/</g, "&lt;").replace(/>/g, "&gt;")
}

function escapeText(value: string): string {
  return value.replace(/&/g, "&amp;").replace(/</g, "&lt;").replace(/>/g, "&gt;")
}

/** buildWordPressPayload is the one request that creates the article. */
export function buildWordPressPayload(state: PublishDraftState): WordPressPublishBody {
  const draft = state.wordpress
  const featured = draft.featuredMediaId
  return {
    source: {
      local_channel_id: state.source.localChannelId,
      message_id: state.source.messageId,
    },
    post: {
      title: draft.title,
      slug: draft.slug,
      content: composeWordPressContent(state),
      status: "publish",
      categories: [...draft.categoryIds],
      tag_ids: [...draft.tagIds],
    },
    media: mediaFor(featured === null ? [] : state.source.attachments.filter((a) => a.id === featured)),
    options: { comment_status: draft.commentStatus },
  }
}
