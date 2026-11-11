import type { ChannelMessage } from "@/lib/types"

export type MessageRun =
  | { kind: "message"; key: string; messages: [ChannelMessage] }
  | { kind: "album"; key: string; groupedID: string; messages: ChannelMessage[] }

/** Group adjacent album members without changing timeline order or swallowing
 * an unrelated message that happens to reuse a grouped_id. */
export function groupMessageAlbums(messages: readonly ChannelMessage[]): MessageRun[] {
  const runs: MessageRun[] = []
  for (const message of messages) {
    const groupedID = message.grouped_id
    const previous = runs.at(-1)
    if (groupedID && previous?.kind === "album" && previous.groupedID === groupedID) {
      if (!previous.messages.some((member) => member.id === message.id)) previous.messages.push(message)
      continue
    }
    if (groupedID) {
      runs.push({ kind: "album", key: `album:${groupedID}:${message.id}`, groupedID, messages: [message] })
    } else {
      runs.push({ kind: "message", key: `message:${message.id}`, messages: [message] })
    }
  }
  return runs
}
