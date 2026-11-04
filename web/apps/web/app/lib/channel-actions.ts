// The form payloads for the channel-management actions.
//
// Kept out of the route so the wire contract is a value that can be checked
// without a browser: what a dialog decides is exactly what the server is told.

export interface DeleteChannelOptions {
  // leaveTelegram is the reader's explicit choice. It is false unless they
  // ticked the box, because leaving a Telegram channel is the wider of the two
  // effects and must never be the default.
  leaveTelegram: boolean
}

// deleteChannelForm is what one deletion submits.
//
// `telegram: "false"` asks for the local record only: the account stays a
// member of the channel. `telegram: "true"` asks for both, and the answer may
// come back as a partial result — those are separate effects and the server
// reports them separately.
export function deleteChannelForm(
  id: number,
  options: DeleteChannelOptions
): Record<string, string> {
  return {
    intent: "leave",
    id: String(id),
    telegram: options.leaveTelegram ? "true" : "false",
  }
}

// fileCategoriesForm is what saving one channel's categories submits: the whole
// set, so a category the reader unticked is removed rather than left behind.
export function fileCategoriesForm(
  id: number,
  categoryIds: readonly number[]
): Record<string, string | string[]> {
  const values: Record<string, string | string[]> = {
    intent: "categories",
    id: String(id),
  }
  if (categoryIds.length > 0) {
    values.category_ids = JSON.stringify([...categoryIds])
  }
  return values
}
