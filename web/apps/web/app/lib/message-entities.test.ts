import assert from "node:assert/strict"
import { test } from "node:test"

import { buildTextNodes, type InlineNode } from "@/lib/message-entities"
import type { Entity } from "@/lib/types"

// Telegram measures entity spans in UTF-16 code units, which is what a
// JavaScript string index already is. Every offset below is counted by hand in
// those units, so a test that passes is a real assertion about positioning.

const kinds = (nodes: InlineNode[]) => nodes.map((n) => n.kind).join("+")
const plain = (nodes: InlineNode[]) => nodes.map((n) => n.text).join("")

test("text with no entities is one plain node, newlines untouched", () => {
  const text = "خط اول\nخط دوم"
  const nodes = buildTextNodes(text, [])
  assert.deepEqual(nodes, [{ kind: "text", text }])
  assert.equal(plain(nodes), text)
})

test("an empty message has no nodes at all", () => {
  assert.deepEqual(buildTextNodes("", []), [])
  assert.deepEqual(buildTextNodes("", undefined), [])
})

test("a url entity becomes a link whose target is the text itself", () => {
  // "ببینید " is 7 units, then "https://t.me/x" is 14.
  const nodes = buildTextNodes("ببینید https://t.me/x را", [
    { type: "url", offset: 7, length: 14 },
  ])
  assert.equal(kinds(nodes), "text+link+text")
  const link = nodes[1]
  assert.ok(link.kind === "link")
  assert.equal(link.href, "https://t.me/x")
  assert.equal(link.text, "https://t.me/x")
})

test("a text_url entity keeps its own target, not its label", () => {
  const nodes = buildTextNodes("اینجا", [
    { type: "text_url", offset: 0, length: 5, url: "https://example.com/a" },
  ])
  assert.equal(nodes.length, 1)
  assert.ok(nodes[0].kind === "link")
  assert.equal(nodes[0].href, "https://example.com/a")
  assert.equal(nodes[0].text, "اینجا")
})

test("nested formatting survives as nesting inside the outer span", () => {
  // "abc" is bold, the middle character is also italic.
  const nodes = buildTextNodes("abc", [
    { type: "bold", offset: 0, length: 3 },
    { type: "italic", offset: 1, length: 1 },
  ])
  assert.equal(kinds(nodes), "bold")
  const outer = nodes[0]
  assert.equal(outer.text, "abc")
  assert.ok(outer.children, "the inner span must not be flattened away")
  assert.equal(kinds(outer.children!), "text+italic+text")
  assert.equal(plain(outer.children!), "abc")
})

test("sibling spans do not swallow each other", () => {
  const nodes = buildTextNodes("aabb", [
    { type: "bold", offset: 0, length: 2 },
    { type: "italic", offset: 2, length: 2 },
  ])
  assert.equal(kinds(nodes), "bold+italic")
  assert.equal(plain(nodes), "aabb")
})

test("a span past the end of the text is clamped, the text survives", () => {
  const nodes = buildTextNodes("abc", [{ type: "bold", offset: 1, length: 99 }])
  assert.equal(plain(nodes), "abc")
})

test("a negative or empty span is dropped rather than throwing", () => {
  assert.equal(plain(buildTextNodes("abc", [{ type: "bold", offset: -3, length: 2 }])), "abc")
  assert.equal(plain(buildTextNodes("abc", [{ type: "bold", offset: 1, length: 0 }])), "abc")
})

test("offsets count UTF-16 units, so an emoji before a span lands right", () => {
  // "🙂" is one code point and two UTF-16 units; "abc" follows at offset 2.
  const nodes = buildTextNodes("🙂abc", [{ type: "bold", offset: 2, length: 3 }])
  assert.equal(kinds(nodes), "text+bold")
  assert.equal(nodes[0].text, "🙂")
  assert.equal(nodes[1].text, "abc")
})

test("a bare URL with no entity is still linked, as before", () => {
  const nodes = buildTextNodes("ببینید https://example.com/p را", [])
  assert.equal(kinds(nodes), "text+link+text")
  assert.ok(nodes[1].kind === "link")
  assert.equal(nodes[1].href, "https://example.com/p")
})

test("a URL already covered by an entity is not linked twice", () => {
  const nodes = buildTextNodes("https://t.me/x", [
    { type: "bold", offset: 0, length: 14 },
  ])
  assert.equal(kinds(nodes), "bold")
})

test("a hashtag entity is its own node and is not read as a URL", () => {
  // "خبر " is 4 units, "#فناوری" is 7.
  const nodes = buildTextNodes("خبر #فناوری", [
    { type: "hashtag", offset: 4, length: 7 },
  ])
  assert.equal(kinds(nodes), "text+hashtag")
  assert.equal(nodes[1].text, "#فناوری")
})

test("a mention entity keeps its own kind", () => {
  // "از " is 3 units, "@telesync" is 9.
  const nodes = buildTextNodes("از @telesync", [
    { type: "mention", offset: 3, length: 9 },
  ])
  assert.equal(kinds(nodes), "text+mention")
  assert.equal(nodes[1].text, "@telesync")
})

test("every source character is accounted for exactly once", () => {
  // "متن " 4 · "https://a.b/c" 13 · " و " 3 · "#تگ" 3 · " و " 3 ·
  // "@کاربر" 6 · " و " 3 · "<code>" 6
  const text = "متن https://a.b/c و #تگ و @کاربر و <code>"
  const nodes = buildTextNodes(text, [
    { type: "url", offset: 4, length: 13 },
    { type: "hashtag", offset: 20, length: 3 },
    { type: "mention", offset: 26, length: 6 },
    { type: "code", offset: 35, length: 6 },
  ])
  assert.equal(plain(nodes), text)
  assert.equal(kinds(nodes), "text+link+text+hashtag+text+mention+text+code")
})

test("partially overlapping spans never lose text", () => {
  const text = "abcdef"
  const nodes = buildTextNodes(text, [
    { type: "bold", offset: 0, length: 4 },
    { type: "italic", offset: 2, length: 4 },
  ])
  assert.equal(plain(nodes), text)
})

test("pre keeps its language so the renderer can fence it", () => {
  const nodes = buildTextNodes("let x = 1", [
    { type: "pre", offset: 0, length: 9, language: "go" },
  ])
  assert.equal(kinds(nodes), "pre")
  assert.ok(nodes[0].kind === "pre")
  assert.equal(nodes[0].language, "go")
})

test("an entity kind this build does not model leaves the text plain", () => {
  const nodes = buildTextNodes("abcdef", [
    { type: "custom_emoji", offset: 0, length: 3 },
    { type: "bold", offset: 3, length: 3 },
  ])
  assert.equal(plain(nodes), "abcdef")
})

test("null or missing entities behave like none", () => {
  const text = "سلام"
  assert.deepEqual(buildTextNodes(text, null), [{ kind: "text", text }])
  assert.deepEqual(buildTextNodes(text), [{ kind: "text", text }])
})

test("the entity list is never mutated", () => {
  const entities: Entity[] = [{ type: "bold", offset: 0, length: 3 }]
  const before = JSON.stringify(entities)
  buildTextNodes("abc", entities)
  assert.equal(JSON.stringify(entities), before)
})
