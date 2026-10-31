import assert from "node:assert/strict"
import { test } from "node:test"

import { compose, parse } from "./content.js"

test("extracts several hashtags and mentions", () => {
  const got = parse("درباره #فناوری و #خبر از @alice و @bob")
  assert.deepEqual(got.hashtags, ["فناوری", "خبر"])
  assert.deepEqual(got.mentions, ["@alice", "@bob"])
  // The tokens leave the text; the words around them stay.
  assert.match(got.main, /^درباره/)
  assert.match(got.main, /و/)
  assert.doesNotMatch(got.main, /#/)
  assert.doesNotMatch(got.main, /@/)
})

test("persian and english hashtags survive intact", () => {
  const got = parse("#technology #فناوری")
  assert.deepEqual(got.hashtags, ["technology", "فناوری"])
})

test("emoji next to a hashtag does not swallow it", () => {
  const got = parse("🙂 #news 🚀")
  assert.deepEqual(got.hashtags, ["news"])
})

test("a hash inside a URL is not a hashtag", () => {
  const got = parse("see https://example.com/a#section and #real")
  assert.deepEqual(got.hashtags, ["real"])
})

test("an at sign inside a URL is not a mention", () => {
  const got = parse("see https://example.com/@user/page and @real")
  assert.deepEqual(got.mentions, ["@real"])
})

test("an email address is not a mention", () => {
  const got = parse("write to me@example.com or @real")
  assert.deepEqual(got.mentions, ["@real"])
})

test("punctuation around a token does not join it", () => {
  const got = parse("(#news), [@alice]; end.")
  assert.deepEqual(got.hashtags, ["news"])
  assert.deepEqual(got.mentions, ["@alice"])
})

test("a message with no tokens keeps its text", () => {
  const got = parse("just text")
  assert.equal(got.main, "just text")
  assert.deepEqual(got.hashtags, [])
  assert.deepEqual(got.mentions, [])
})

test("empty text with media parses to empty sections", () => {
  const got = parse("")
  assert.equal(got.main, "")
  assert.deepEqual(got.hashtags, [])
  assert.deepEqual(got.mentions, [])
})

test("only mentions leaves an empty main", () => {
  const got = parse("@alice")
  assert.equal(got.main, "")
  assert.deepEqual(got.mentions, ["@alice"])
})

test("telegram entities are preferred over the lexer", () => {
  // The text contains a hash inside a URL; the entity metadata says where the
  // real hashtag is. The metadata wins.
  const text = "https://x.com/#notreal #real"
  const start = text.indexOf("#real")
  const got = parse(text, [{ type: "hashtag", offset: start, length: 5 }])
  assert.deepEqual(got.hashtags, ["real"])
})

test("formatting spans are kept and remapped onto the main text", () => {
  const text = "hello #news world"
  const got = parse(text, [
    { type: "bold", offset: 0, length: 5 },
    { type: "hashtag", offset: 6, length: 5 },
  ])
  assert.match(got.main, /hello/)
  assert.match(got.main, /world/)
  assert.equal(got.entities.length, 1)
  assert.equal(got.entities[0].offset, 0)
  assert.equal(got.entities[0].length, 5)
})

test("a span that touched an extracted token is dropped, not stretched", () => {
  const text = "ab #news cd"
  const got = parse(text, [{ type: "bold", offset: 3, length: 4 }])
  assert.equal(got.entities.length, 0)
})

test("original text is never changed", () => {
  const text = "درباره #فناوری"
  const got = parse(text)
  assert.equal(got.original, text)
})

test("compose puts hashtags and mentions on their own lines", () => {
  const got = compose("hello world", ["news", "tech"], ["@alice"])
  assert.equal(got.text, "hello world\n\n#news #tech\n@alice")
})

test("compose does not duplicate a token already in the main text", () => {
  const got = compose("hello #news world", ["news"], [])
  assert.equal(got.text, "hello #news world")
})

test("compose emits no trailing block when there is nothing to add", () => {
  const got = compose("just text", [], [])
  assert.equal(got.text, "just text")
})

test("compose computes UTF-16 offsets the way Telegram does", () => {
  // "🙂 " is two UTF-16 units, so the hashtag starts at 4.
  const got = compose("🙂 hello", ["news"], [])
  assert.equal(got.text, "🙂 hello\n\n#news")
  const tag = got.entities.find((e) => e.type === "hashtag")
  assert.ok(tag)
  assert.equal(got.text.slice(tag.offset, tag.offset + tag.length), "#news")
})

test("compose keeps formatting spans that stay inside the main text", () => {
  const got = compose("hello", [], [], [{ type: "bold", offset: 0, length: 5 }])
  assert.equal(got.entities.length, 1)
  assert.equal(got.entities[0].type, "bold")
})

test("compose drops a formatting span that falls outside the main text", () => {
  const got = compose("hi", ["news"], [], [{ type: "bold", offset: 0, length: 50 }])
  assert.equal(got.entities.filter((e) => e.type === "bold").length, 0)
})

test("editing the text and recomposing never duplicates tokens", () => {
  const first = parse("a #one #two")
  const edited = compose(first.main, first.hashtags, first.mentions, first.entities)
  const second = parse(edited.text, edited.entities)
  assert.deepEqual(second.hashtags, ["one", "two"])
  const again = compose(second.main, second.hashtags, second.mentions, second.entities)
  assert.equal(again.text, edited.text)
})
