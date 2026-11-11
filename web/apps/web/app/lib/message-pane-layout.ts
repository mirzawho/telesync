// Keep the scrollable timeline and its floating controls in sync with the
// observed footer height. The additional 16px is breathing room, not a fixed
// footer estimate.
export function messagePaneClearance(footerHeight: number): number {
  return Math.max(0, Math.ceil(footerHeight)) + 16
}

export function jumpButtonOffset(footerHeight: number): number {
  return messagePaneClearance(footerHeight)
}
