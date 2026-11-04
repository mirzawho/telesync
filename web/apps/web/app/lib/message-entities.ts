// Turning one message's text and its Telegram entities into an inline tree the
// renderer can draw.
//
// Telegram measures entity spans in UTF-16 code units. A JavaScript string
// index is already in those units, so `text.slice` is the right tool and no
// conversion happens here. Nothing in this file touches the DOM or the network,
// which is what makes the behaviour testable without a browser.

import type { Entity } from "@/lib/types"

export type InlineKind =
  | "text"
  | "bold"
  | "italic"
  | "underline"
  | "strike"
  | "code"
  | "pre"
  | "spoiler"
  | "blockquote"
  | "link"
  | "email"
  | "phone"
  | "hashtag"
  | "mention"
  | "cashtag"
  | "bot_command"

// InlineNode is one span of a message's text.
//
// `text` is always the exact source slice the node covers, including anything
// nested inside it, so a consumer that only wants the words can read it
// straight off. `children` is present on a node that has spans inside it; a
// leaf has none. `href` is the target of a link, `language` the fenced language
// of a `pre`.
export interface InlineNode {
  kind: InlineKind
  text: string
  href?: string
  language?: string
  children?: InlineNode[]
}

// Spans Telegram reports and this build knows how to draw. Anything else — a
// custom emoji, a paid reaction, a kind added to Telegram after this build —
// is left as the plain text it covers rather than given a made-up style.
const KIND_FOR: Record<string, InlineKind> = {
  bold: "bold",
  italic: "italic",
  underline: "underline",
  strike: "strike",
  code: "code",
  pre: "pre",
  spoiler: "spoiler",
  blockquote: "blockquote",
  url: "link",
  text_url: "link",
  email: "email",
  phone: "phone",
  hashtag: "hashtag",
  mention: "mention",
  mention_name: "mention",
  cashtag: "cashtag",
  bot_command: "bot_command",
}

interface Span {
  start: number
  end: number
  kind: InlineKind
  href?: string
  language?: string
}

// A bare http(s) URL written with no entity around it. Telegram normally sends
// a `url` entity for one; this is the fallback that keeps a link a link when
// the message carried no entities at all. Nothing else is interpreted.
const URL_PATTERN = /(https?:\/\/[^\s<>"')\]}]+)/gi

// splitPlain expands one unstyled slice, linking bare URLs the way the panel
// always has. It returns a list because a slice can hold more than one URL.
function splitPlain(raw: string): InlineNode[] {
  const out: InlineNode[] = []
  let cursor = 0
  for (const match of raw.matchAll(URL_PATTERN)) {
    const at = match.index ?? 0
    const href = match[1]
    if (at > cursor) out.push({ kind: "text", text: raw.slice(cursor, at) })
    out.push({ kind: "link", text: href, href })
    cursor = at + href.length
  }
  if (cursor === 0) return [{ kind: "text", text: raw }]
  if (cursor < raw.length) out.push({ kind: "text", text: raw.slice(cursor) })
  return out
}

// normalize clamps what Telegram sent onto the text it describes. A span that
// starts past the end, runs backwards or covers nothing is dropped rather than
// stretched: an entity is a claim about specific characters, and a claim that
// no longer matches the text is not a licence to style other ones.
function normalize(text: string, entities: readonly Entity[]): Span[] {
  const spans: Span[] = []
  for (const entity of entities) {
    const kind = KIND_FOR[entity.type]
    if (!kind) continue
    const start = Math.max(0, Math.min(entity.offset, text.length))
    const end = Math.max(start, Math.min(entity.offset + entity.length, text.length))
    if (end <= start) continue
    const span: Span = { start, end, kind }
    if (kind === "link") {
      // A `text_url` carries its own target; a bare `url` is its own text.
      span.href = entity.type === "text_url" ? entity.url || text.slice(start, end) : text.slice(start, end)
    }
    if (kind === "pre" && entity.language) span.language = entity.language
    spans.push(span)
  }
  return spans
}

// buildTextNodes splits one message's text into inline nodes.
//
// The spans are turned into a proper tree: a span inside another becomes its
// child, and two spans that only partly overlap are cut at the point where they
// diverge so neither can swallow the other's characters. Every character of the
// source ends up in exactly one leaf.
export function buildTextNodes(
  text: string,
  entities?: readonly Entity[] | null
): InlineNode[] {
  if (!text) return []
  const spans = normalize(text, entities ?? [])
  if (spans.length === 0) return splitPlain(text)

  // Cut the text at every span boundary; inside one of those atomic runs the
  // covering spans are exactly the ones to wrap it in.
  const bounds = new Set<number>([0, text.length])
  for (const span of spans) {
    bounds.add(span.start)
    bounds.add(span.end)
  }
  const points = [...bounds].sort((a, b) => a - b)

  const roots: InlineNode[] = []
  // The open containers, outermost first. `start` is where the node began, so
  // its `text` can be cut out of the source when it closes.
  const open: { node: InlineNode; span: Span; start: number }[] = []

  const emit = (nodes: InlineNode[]) => {
    const holder = open.at(-1)?.node.children ?? roots
    holder.push(...nodes)
  }

  for (let i = 0; i < points.length - 1; i++) {
    const start = points[i]
    const end = points[i + 1]
    if (end <= start) continue
    // Outermost first: the span that starts earliest, and of those the widest.
    const covering = spans
      .filter((span) => span.start <= start && span.end >= end)
      .sort((a, b) => a.start - b.start || b.end - a.end)

    // Close whatever is open that this run is not inside. The common prefix of
    // the two stacks is what stays: a span that merely touched the previous run
    // closes here and, if it still covers this one, is reopened.
    let keep = 0
    while (
      keep < open.length &&
      keep < covering.length &&
      open[keep].span === covering[keep]
    ) {
      keep++
    }
    while (open.length > keep) {
      const frame = open.pop()!
      frame.node.text = text.slice(frame.start, start)
    }
    for (const span of covering.slice(keep)) {
      const node: InlineNode = {
        kind: span.kind,
        text: "",
        children: [],
      }
      if (span.href !== undefined) node.href = span.href
      if (span.language !== undefined) node.language = span.language
      if (open.length === 0) roots.push(node)
      else open.at(-1)!.node.children!.push(node)
      open.push({ node, span, start })
    }
    emit(splitPlain(text.slice(start, end)))
  }

  while (open.length > 0) {
    const frame = open.pop()!
    frame.node.text = text.slice(frame.start, text.length)
  }
  return roots
}
