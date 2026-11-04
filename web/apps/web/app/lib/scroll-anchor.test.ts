import assert from "node:assert/strict"
import { test } from "node:test"

import {
  captureAnchor,
  restoreScrollTop,
  type ItemBox,
  type ScrollViewport,
} from "@/lib/scroll-anchor"

// The anchor is the reader's place, expressed as one message and how far it is
// from the top of the viewport. Because it names an element rather than a pixel
// height, a picture that finishes loading somewhere else cannot drag the
// reader's message away.

const viewport = (scrollTop: number, height = 400): ScrollViewport => ({
  scrollTop,
  height,
})

// Boxes are offsets from the top of the scrollable content.
const box = (id: number, top: number, height = 60): ItemBox => ({ id, top, height })
const shortThenTall: ItemBox[] = [
  box(1, 0, 60),
  box(2, 60, 60),
  box(3, 120, 60),
  box(4, 180, 60),
]

test("the anchor is the first message still on screen", () => {
  const anchor = captureAnchor(viewport(90), shortThenTall)
  assert.deepEqual(anchor, { id: 2, offset: -30 })
})

test("a message entirely above the viewport is not the anchor", () => {
  const anchor = captureAnchor(viewport(130), shortThenTall)
  assert.equal(anchor?.id, 3)
})

test("an empty timeline has no anchor at all", () => {
  assert.equal(captureAnchor(viewport(0), []), null)
})

test("restoring puts the anchored message back where it was", () => {
  const before = captureAnchor(viewport(90), shortThenTall)
  // Three older messages were prepended, so every offset below grew by 180.
  const after: ItemBox[] = [
    box(0, 0, 60),
    box(-1, 60, 60),
    box(-2, 120, 60),
    box(1, 180, 60),
    box(2, 240, 60),
    box(3, 300, 60),
    box(4, 360, 60),
  ]
  // Message 2 sat 30px above the viewport top; it must again.
  assert.equal(restoreScrollTop(viewport(90), after, before!), 270)
})

test("a picture finishing loading below the anchor does not move the reader", () => {
  const before = captureAnchor(viewport(90), shortThenTall)
  // Message 4's media resolved and grew; nothing above the anchor changed.
  const after: ItemBox[] = [
    box(1, 0, 60),
    box(2, 60, 60),
    box(3, 120, 60),
    box(4, 180, 400),
  ]
  assert.equal(restoreScrollTop(viewport(90), after, before!), 90)
})

test("a picture finishing loading above the anchor is compensated for", () => {
  const before = captureAnchor(viewport(90), shortThenTall)
  // Message 1's media resolved and grew by 300, pushing 2 down.
  const after: ItemBox[] = [
    box(1, 0, 360),
    box(2, 360, 60),
    box(3, 420, 60),
    box(4, 480, 60),
  ]
  assert.equal(restoreScrollTop(viewport(90), after, before!), 390)
})

test("an anchor whose message is gone does not invent a position", () => {
  const before = captureAnchor(viewport(90), shortThenTall)
  assert.equal(restoreScrollTop(viewport(90), [box(9, 0)], before!), null)
})

test("the anchor never sends the reader below the end of the list", () => {
  const before = captureAnchor(viewport(90), shortThenTall)
  const after: ItemBox[] = [box(2, 0, 60)]
  // Restoring to 240 would be past the bottom of a 60px list; the clamped
  // answer is the largest scroll position the content allows.
  assert.equal(restoreScrollTop(viewport(90, 400), after, before!, 60), 0)
})
