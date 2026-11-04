// Keeping a reader's place while history arrives above them.
//
// The anchor is one message and how far it sits from the top of the viewport,
// not a pixel height. That is the whole point: when an image finishes loading
// and the content above the reader grows, the height of the list has changed
// and a saved `scrollTop` would now point somewhere else. Naming the message
// and re-measuring it after the update puts the reader back on the message they
// were reading whatever else moved.

export interface ScrollViewport {
  scrollTop: number
  height: number
}

// One message's box, in content coordinates: `top` is its distance from the top
// of the scrollable content, `height` its own.
export interface ItemBox {
  id: number
  top: number
  height: number
}

export interface ScrollAnchor {
  id: number
  // The message's `top` minus the viewport's `scrollTop`: negative when the
  // message starts above the visible area, which is the usual case while
  // reading.
  offset: number
}

// captureAnchor picks the message the reader is actually looking at: the first
// one still reaching into the visible area. A message scrolled entirely off the
// top is not the reader's place.
export function captureAnchor(
  viewport: ScrollViewport,
  boxes: readonly ItemBox[]
): ScrollAnchor | null {
  for (const box of boxes) {
    if (box.top + box.height > viewport.scrollTop) {
      return { id: box.id, offset: box.top - viewport.scrollTop }
    }
  }
  return null
}

// restoreScrollTop is where the viewport must go to put the anchored message
// back exactly where it was, clamped to the content so a short list cannot be
// scrolled past its end. It answers null when the anchored message is gone,
// which is the caller's cue to leave the viewport alone rather than guess.
export function restoreScrollTop(
  viewport: ScrollViewport,
  boxes: readonly ItemBox[],
  anchor: ScrollAnchor,
  contentHeight?: number
): number | null {
  const box = boxes.find((candidate) => candidate.id === anchor.id)
  if (!box) return null
  const wanted = box.top - anchor.offset
  if (contentHeight === undefined) return wanted
  const max = Math.max(0, contentHeight - viewport.height)
  return Math.min(Math.max(0, wanted), max)
}
