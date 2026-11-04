// Parsing and composing the outgoing content of a publishing request.
//
// Telegram measures entity spans in UTF-16 code units. JavaScript strings are
// UTF-16 already, so `text.length` and `text.slice` work in exactly those
// units — which is why the editor runs here and not against rune offsets.

import type { Entity } from "@/lib/types"

// Entity is the API's formatting span; it lives with the other response shapes
// and is re-exported here because the composing code is what consumes it.
export type { Entity }

export interface ParsedContent {
  /** The whole source text, untouched. */
  original: string
  /** The text with the extracted hashtags and mentions removed. */
  main: string
  /** Hashtags without their leading '#'. */
  hashtags: string[]
  /** Mentions with their leading '@'. */
  mentions: string[]
  /** Formatting spans kept after extraction, remapped onto main. */
  entities: Entity[]
}

export interface ComposedContent {
  /** The exact outgoing text. */
  text: string
  /** The exact outgoing entities. */
  entities: Entity[]
}

const HASHTAG_RE = /(?:^|[^\p{L}\p{N}_#])((?:#)[\p{L}\p{N}_]+)/gu
const MENTION_RE = /(?:^|[^\p{L}\p{N}_@])((?:@)[\p{L}\p{N}_]+)/gu

function isInsideURL(text: string, index: number): boolean {
  // A '#' or '@' inside a URL is part of the URL, not a token.
  const before = text.slice(0, index)
  return /(https?:\/\/[^\s>]*)$/i.test(before) || /(^|\s)(www\.[^\s>]*)$/i.test(before)
}

function isInsideEmail(text: string, index: number): boolean {
  // "me@example.com": the '@' at index 2 is an email separator, not a mention.
  const before = text.slice(0, index)
  return /[\p{L}\p{N}._%+-]$/u.test(before) && !/\s$/.test(before)
}

/**
 * parse splits a source message into the three editable sections.
 *
 * Hashtags and mentions are taken from Telegram's entity metadata when the
 * message carries any, so a URL containing '#' is never mistaken for one. A
 * message without entities falls back to a lexer that skips URLs and emails.
 */
export function parse(text: string, entities: Entity[] = []): ParsedContent {
  const tokens: { start: number; end: number; kind: "hashtag" | "mention"; value: string }[] = []

  const hasTokenEntities = entities.some(
    (e) => e.type === "hashtag" || e.type === "mention" || e.type === "mention_name",
  )
  if (hasTokenEntities) {
    for (const e of entities) {
      if (e.type === "hashtag" || e.type === "mention" || e.type === "mention_name") {
        const raw = text.slice(e.offset, e.offset + e.length)
        if (e.type === "hashtag") {
          tokens.push({ start: e.offset, end: e.offset + e.length, kind: "hashtag", value: raw.replace(/^#/, "") })
        } else {
          tokens.push({ start: e.offset, end: e.offset + e.length, kind: "mention", value: raw })
        }
      }
    }
  } else {
    for (const m of text.matchAll(HASHTAG_RE)) {
      const whole = m[0]
      const token = m[1]
      const start = m.index! + whole.length - token.length
      if (isInsideURL(text, start)) continue
      tokens.push({ start, end: start + token.length, kind: "hashtag", value: token.replace(/^#/, "") })
    }
    for (const m of text.matchAll(MENTION_RE)) {
      const whole = m[0]
      const token = m[1]
      const start = m.index! + whole.length - token.length
      if (isInsideURL(text, start) || isInsideEmail(text, start)) continue
      tokens.push({ start, end: start + token.length, kind: "mention", value: token })
    }
  }

  tokens.sort((a, b) => a.start - b.start)

  // Remove the tokens from the text, keeping the rest exactly as it was, and
  // remap the formatting spans onto what remains. Every source character gets
  // its new position, or -1 when it was extracted, so a span never lands in
  // the wrong place by accident.
  let main = ""
  let cursor = 0
  const indexMap = new Array<number>(text.length + 1).fill(-1)
  for (const t of tokens) {
    if (t.start < cursor) continue
    for (let i = cursor; i < t.start; i++) indexMap[i] = main.length + (i - cursor)
    main += text.slice(cursor, t.start)
    for (let i = t.start; i < t.end; i++) indexMap[i] = -1
    cursor = t.end
  }
  for (let i = cursor; i <= text.length; i++) indexMap[i] = main.length + (i - cursor)
  main += text.slice(cursor)

  const kept: Entity[] = []
  for (const e of entities) {
    if (e.type === "hashtag" || e.type === "mention" || e.type === "mention_name") continue
    const from = e.offset
    const to = e.offset + e.length
    if (from < 0 || to > text.length) continue
    const start = indexMap[from]
    const end = indexMap[to]
    // A span that touched an extracted token degrades explicitly: it is
    // dropped rather than silently stretched over the gap.
    if (start < 0 || end < 0 || end <= start) continue
    kept.push({ ...e, offset: start, length: end - start })
  }

  return {
    original: text,
    main,
    hashtags: tokens.filter((t) => t.kind === "hashtag").map((t) => t.value),
    mentions: tokens.filter((t) => t.kind === "mention").map((t) => t.value),
    entities: kept,
  }
}

function utf16Len(s: string): number {
  return s.length
}

/**
 * lexEntities reads back the spans a message's text can carry on its own.
 *
 * It is the other half of `parse`: where `parse` takes the spans Telegram
 * reported, `lexEntities` finds the ones the finished text plainly says. A
 * publishing draft needs both — the source's formatting spans while the body is
 * still the source's, and these once the user has rewritten it and the old
 * offsets no longer describe anything.
 *
 * Only hashtags, mentions, links and emails are produced: those are unambiguous
 * in running text. A bold or italic span cannot be recovered from plain text and
 * is never invented.
 */
export function lexEntities(text: string): Entity[] {
  const found: Entity[] = []
  for (const m of text.matchAll(HASHTAG_RE)) {
    const whole = m[0]
    const token = m[1]
    const start = m.index! + whole.length - token.length
    if (isInsideURL(text, start)) continue
    found.push({ type: "hashtag", offset: start, length: utf16Len(token) })
  }
  for (const m of text.matchAll(MENTION_RE)) {
    const whole = m[0]
    const token = m[1]
    const start = m.index! + whole.length - token.length
    if (isInsideURL(text, start) || isInsideEmail(text, start)) continue
    found.push({ type: "mention", offset: start, length: utf16Len(token) })
  }
  for (const m of text.matchAll(/https?:\/\/[^\s>]+/gi)) {
    found.push({ type: "url", offset: m.index!, length: utf16Len(m[0]) })
  }
  for (const m of text.matchAll(/[\p{L}\p{N}._%+-]+@[\p{L}\p{N}.-]+\.[\p{L}]{2,}/gu)) {
    found.push({ type: "email", offset: m.index!, length: utf16Len(m[0]) })
  }
  found.sort((a, b) => a.offset - b.offset || a.length - b.length)
  return found
}

/**
 * compose builds the outgoing text and its entities.
 *
 * The reconstruction policy is deterministic: the main text first, then the
 * hashtags, then the mentions, each group on its own line. A token already
 * present in the main text is not appended a second time.
 */
export function compose(
  rawMain: string,
  hashtags: string[],
  mentions: string[],
  entities: Entity[] = [],
): ComposedContent {
  // Trailing whitespace left by an earlier extraction is not content: trimming
  // it keeps a parse→compose→parse round trip stable.
  const main = rawMain.replace(/[ \t\r\n]+$/, "")
  const cleanTags = hashtags.map((h) => h.replace(/^#/, "").trim()).filter(Boolean)
  const cleanMentions = mentions.map((m) => m.trim()).filter(Boolean)

  const alreadyInMain = (token: string): boolean => {
    const pattern = new RegExp(`(^|[^\\p{L}\\p{N}_])${token.replace(/[.*+?^${}()|[\]\\]/g, "\\$&")}(?![\\p{L}\\p{N}_])`, "u")
    return pattern.test(main)
  }

  const parts: string[] = []
  if (main.length > 0) parts.push(main)

  const outEntities: Entity[] = entities
    .filter((e) => e.offset + e.length <= utf16Len(main))
    .map((e) => ({ ...e }))

  const lineTags = cleanTags.filter((t) => !alreadyInMain(t))
  const lineMentions = cleanMentions.filter((m) => !alreadyInMain(m))

  if (lineTags.length > 0 || lineMentions.length > 0) {
    if (parts.length > 0) parts.push("")
  }

  let text = parts.join("\n")
  const block: string[] = []
  if (lineTags.length > 0) block.push(lineTags.map((t) => `#${t}`).join(" "))
  if (lineMentions.length > 0) block.push(lineMentions.join(" "))
  if (block.length > 0) {
    if (text.length > 0) text += "\n"
    for (const line of block) {
      const start = utf16Len(text)
      text += line
      const isTagLine = line.startsWith("#")
      for (const token of isTagLine ? lineTags : lineMentions) {
        const rendered = isTagLine ? `#${token}` : token
        const at = line.indexOf(rendered)
        if (at < 0) continue
        outEntities.push({
          type: isTagLine ? "hashtag" : "mention",
          offset: start + at,
          length: utf16Len(rendered),
        })
      }
      text += "\n"
    }
    text = text.replace(/\n$/, "")
  }

  return { text, entities: outEntities }
}
