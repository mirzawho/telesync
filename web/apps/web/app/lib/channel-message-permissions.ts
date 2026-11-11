import type { ChannelPermissions } from "@/lib/types"

export interface ChannelMessageCapabilities {
  read: boolean
  create: boolean
  edit: boolean
  delete: boolean
}

// Only an explicit true from the channel detail authorizes a UI capability.
// Membership, managed status, and an absent permissions object are not grants.
export function channelMessageCapabilities(
  permissions?: ChannelPermissions | null
): ChannelMessageCapabilities {
  return {
    read: permissions?.read === true,
    create: permissions?.new === true,
    edit: permissions?.edit === true,
    delete: permissions?.delete === true,
  }
}
