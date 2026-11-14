// What the publishing workspace knows about the world outside the draft: the
// connections, channels, sites and terms the user may pick from.
//
// Servers are the API's own rows, because the workspace hands them straight to
// the reused TelegramServerCombobox. The rest are narrowed views the dialog
// derives from its responses — a channel list already filtered to what this
// account may post to, a term list already the selected site's own — so the
// workspace below never reasons about permissions or pagination again.

import type { TelegramAccount, TelegramServer } from "@/lib/types"

export type ServerOption = TelegramServer

// ChannelOption is one channel this account may actually post in. `canPublish`
// is already resolved: a channel the account can only read is never offered as
// a destination, and one that cannot be posted to is shown disabled.
export interface ChannelOption {
  // id is the Telegram channel id on the selected connection, as a decimal
  // string. It is not the local managed-channel row id.
  id: string
  title: string
  username?: string
  canPublish: boolean
}

// SiteOption is one registered WordPress site: its name and its hostname are
// what the picker shows. No credential ever comes this far.
export interface SiteOption {
  id: number
  title: string
  domain: string
}

// TermOption is one WordPress category or tag. `id` is the WordPress term id of
// the selected site and means nothing on any other site.
export interface TermOption {
  id: number
  name: string
  slug?: string
}

export type OptionList = "servers" | "channels" | "sites" | "categories" | "tags"

export interface PublishingOptions {
  accounts: TelegramAccount[]
  servers: ServerOption[]
  channels: ChannelOption[]
  sites: SiteOption[]
  categories: TermOption[]
  tags: TermOption[]
  loading: Record<OptionList, boolean>
  errors: Partial<Record<OptionList, string | null>>
}

export const emptyOptions: PublishingOptions = {
  accounts: [],
  servers: [],
  channels: [],
  sites: [],
  categories: [],
  tags: [],
  loading: { servers: false, channels: false, sites: false, categories: false, tags: false },
  errors: {},
}
