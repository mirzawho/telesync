// Response shapes the gateway serves, mirrored for the panel. They match the
// JSON in docs/api; a field the API omits is optional here too.

export type { Me } from "./session"

export interface Category {
  id: number
  name: string
  description: string
  created_at: string
  updated_at: string
}

// WordPressSite is one registered publishing destination from
// GET /api/v1/local/wordpress. The API never returns the site's password.
// `site_icon_url` is null when the site has no icon.
export interface WordPressSite {
  id: number
  // category_ids are global categories (the ones channels are filed under)
  // this site publishes into.
  category_ids: number[]
  url: string
  domain: string
  title: string
  site_icon_url: string | null
  username: string
  created_at: string
  updated_at: string
}

// WordPressWrite is the answer to a successful POST, PATCH or DELETE on the
// WordPress routes. Those routes answer with a document rather than a
// redirect: `saved.refresh` says the list should be reloaded, `redirect_to`
// is where the client goes next (the site after a create or update, the
// collection after a delete).
export interface WordPressWrite {
  site?: WordPressSite
  deleted?: boolean
  id?: number
  saved?: { id: number; refresh: boolean; redirect_to: string }
  redirect_to?: string
}

export type MembershipStatus = "member" | "join_pending" | "unknown" | string

// ChannelPermissions is the channel-level capability set, the same four
// booleans the API answers with.
export interface ChannelPermissions {
  new: boolean
  edit: boolean
  delete: boolean
  read: boolean
}

// ChannelDetail is the one channel object the API has, answered by
// GET /telegram/{key}/channels/{channel_id} from Telegram and by
// GET /local/channels/{id} from the stored snapshot. `id` is Telegram's channel
// id, never the local managed-channel row id.
export interface ChannelDetail {
  id: string
  type: string
  title: string
  username?: string
  description?: string
  avatar_available: boolean
  avatar_url?: string
  is_member: boolean
  is_managed: boolean
  membership_status: MembershipStatus
  permissions: ChannelPermissions
  verified: boolean
  scam: boolean
  fake: boolean
  participants_count: number | null
}

// Channel is one row of GET /local/channels: the managed-channel record as
// stored, addressed by its local `id`. It is the listing's shape, not the
// channel object; see ChannelDetail for the channel itself.
export interface Channel {
  id: number
  client_key: string
  channel_id: number
  type: string
  title: string
  username?: string
  description?: string
  avatar_available: boolean
  avatar_url?: string
  membership_status: MembershipStatus
  participants_count: number | null
  created_at: string
  updated_at: string
  last_synced_at: string
}

// MessageSender is whoever posted a message, when Telegram reported one. A
// channel's own posts usually carry no sender at all.
export interface MessageSender {
  id: number
  name?: string
  username?: string
}

export type MediaType =
  | "photo"
  | "video"
  | "document"
  | "audio"
  | "voice"
  | "sticker"
  | "gif"
  | "poll"
  | "geo"
  | "webpage"
  | "other"
  | string

// MessageMedia is the descriptor of one attachment. `url` is an API path that
// streams the bytes through the Telegram media route (the same route the
// channel's client answers), never an absolute URL. The API sets it for every
// media; whether the route actually has bytes to send depends on the backend.
export interface MessageMedia {
  id?: string
  type: MediaType
  label?: string
  mime_type?: string
  size?: number
  url?: string
}

// MessageReaction is one reaction tally on a post. `type` keeps standard and
// custom emoji apart; `document_id` identifies a custom emoji.
export interface MessageReaction {
  type: "emoji" | "custom_emoji" | "other" | string
  emoji?: string
  document_id?: number
  count: number
  // null when the response did not say whether this account reacted.
  chosen: boolean | null
}

// ChannelMessage is one post from GET .../channels/{id}/messages. The API
// omits `text`, `sender`, `media` and `reply_to_message_id` when it has none:
// a message is not always text, and never needs a sender. `views` and
// `reactions` are always present; `views` is null when Telegram did not
// report a count, and `reactions` is an empty array when there are none.
export interface ChannelMessage {
  id: number
  date: string
  text?: string
  sender?: MessageSender
  media?: MessageMedia
  reply_to_message_id?: number
  views: number | null
  reactions: MessageReaction[]
}

export type GlobalPermission =
  | "users.view"
  | "users.manage"
  | "channels.view"
  | "channels.create"
  | "categories.view"
  | "categories.create"
  | "wordpress.view"
  | "wordpress.create"
  | "servers.view"
  | "content.publish"

export interface ResourcePermissions {
  channel_id?: number
  category_id?: number
  view?: string
  update?: string
  delete?: string
}

export interface Permissions {
  global: string[]
  channels: ResourcePermissions[]
  categories: ResourcePermissions[]
}

export interface User {
  id: number
  fullname: string
  username: string
  is_active: boolean
  created_at: string
  updated_at: string
  permissions?: Permissions
  access?: {
    channels: boolean
    categories: boolean
    users: boolean
    servers: boolean
    content: boolean
  }
}

export type ChannelAvailability =
  | {
      state: "ready"
      channels: Channel[]
      meta: {
        total?: number
        has_more?: boolean
        page?: number
        total_pages?: number
      }
    }
  | { state: "not_configured"; message: string }
  | { state: "unavailable"; message: string; code: string }

export type TelegramClient = {
  key: string
  health: "ok" | "error"
  detail?: string
}

// TelegramServer is one row of GET /api/v1/telegram. `enabled` is
// configuration; `state` and `transport` are the worker's live report, and
// `authorized` is the account's, null when it is not known.
export interface TelegramServer {
  key: string
  type: string
  enabled: boolean
  health_checkable: boolean
  state: string
  authorized: boolean | null
  transport: string
  last_success_at: string | null
  last_error: string
  // user is the server's own Telegram account, null when it is not known (a
  // server that is not connected has none).
  user?: TelegramUser | null
}

export interface TelegramUser {
  id: number
  first_name: string
  last_name: string
  username: string
}

// FoundChannel is one hit from GET .../channels/find. Join takes its decimal
// `id` as the path segment.
// It carries the whole channel object, permissions included, like every other
// channel response.
export type FoundChannel = ChannelDetail
