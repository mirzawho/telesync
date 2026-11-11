// The channel list's search and category filtering.
//
// These are pure decisions about a list already in hand: no request, no DOM.
// The pane filters what it has so typing is instant and the selected channel
// never moves because of a filter change.

import type { Channel } from "@/lib/types"

// ChannelFilter is what the reader asked for. `filed` is which category ids
// hold each channel, keyed by the channel's local id.
export interface ChannelFilter {
  search: string
  categoryIds: readonly number[]
  filed: Readonly<Record<number, readonly number[]>>
  // Empty means all connections; otherwise keys are Telegram client keys.
  serverKeys?: readonly string[]
}

// toggleCategory is one click on a category chip: an inactive category is
// added, an active one is removed. The result is a new list, de-duplicated by
// id, and it never mutates what it was given.
export function toggleCategory(
  selected: readonly number[],
  id: number
): number[] {
  return selected.includes(id)
    ? selected.filter((value) => value !== id)
    : [...selected, id]
}

// matchesSearch is a case-insensitive, whitespace-trimmed match against the
// channel's title and username. An empty query matches everything: clearing the
// search must restore the list, not narrow it to nothing.
export function matchesSearch(channel: Channel, search: string): boolean {
  const needle = search.trim().toLocaleLowerCase("fa")
  if (needle === "") return true
  const haystack = `${channel.title ?? ""}\n${channel.username ?? ""}`
  return haystack.toLocaleLowerCase("fa").includes(needle)
}

// filterChannels applies the search and the category chips to a channel list.
//
// A channel matches when the search matches AND it is in at least one of the
// selected categories. No selected categories means every channel qualifies on
// that axis, so clearing the chips restores the whole list. A channel filed
// under no category is never matched by a category filter, because it is not in
// the one being looked at.
export function filterChannels(
  channels: readonly Channel[],
  filter: ChannelFilter
): Channel[] {
  const wanted = filter.categoryIds
  return channels.filter((channel) => {
    if (filter.serverKeys?.length && !filter.serverKeys.includes(channel.client_key)) return false
    if (!matchesSearch(channel, filter.search)) return false
    if (wanted.length === 0) return true
    const under = filter.filed[channel.id] ?? []
    return wanted.some((id) => under.includes(id))
  })
}
