import { Field, FieldError, FieldLabel } from "@workspace/ui/components/field"
import { cn } from "@workspace/ui/lib/utils"

import { TelegramServerCombobox } from "@/components/telegram-server-combobox"
import { ownedAccountIds } from "@/lib/account-channel-scope"
import { listAll } from "@/lib/session"
import type { TelegramAccount, TelegramServer } from "@/lib/types"

// A managed channel a form may name as its source, together with the Telegram
// connection it belongs to. The id is the local managed_channels.id — never a
// Telegram peer id — and `clientKey` is the connection that can reach it.
export interface SourceChannel {
  id: number
  label: string
  clientKey: string
  // accountId is the user-owned account this channel belongs to. It is the
  // authoritative identity behind the row and the thing a workflow or webhook
  // binds to; `clientKey` only says which server definition it sits on.
  accountId?: string
  // A second line for the picker, when the caller has one to show.
  detail?: string
}

// The name the webhook table has always read this shape by.
export type SourceChannelOption = SourceChannel

/**
 * sourceChannelsOf turns the managed-channel listing into picker rows, scoped
 * to the accounts the reader owns.
 *
 * This is the ownership gate for every automation source picker. A row reached
 * through an account the caller does not own is dropped rather than offered —
 * being an administrator never widens it — and a row with no owning account at
 * all is a quarantined legacy record and is dropped for the same reason. What
 * survives is exactly one reader's own inventory.
 */
export function sourceChannelsOf(
  rows:
    | readonly {
        id: number
        title: string
        client_key?: string
        account_id?: string
      }[]
    | undefined,
  accounts: readonly TelegramAccount[]
): SourceChannel[] {
  const owned = ownedAccountIds(accounts)
  return (rows ?? [])
    .filter((row) => owned.has((row.account_id ?? "").trim()))
    .map((row) => ({
      id: row.id,
      label: row.title,
      clientKey: row.client_key ?? "",
      accountId: row.account_id ?? "",
      detail: row.client_key,
    }))
}

// Automation forms need the complete managed inventory before applying the
// ownership gate. A single 100-row page silently hid valid sources on larger
// installations.
export async function listOwnedSourceChannels(
  token: string,
  accounts: readonly TelegramAccount[]
): Promise<SourceChannel[]> {
  const { rows } = await listAll(
    token,
    "/local/channels",
    (data) =>
      (data as {
        channels?: {
          id: number
          title: string
          client_key?: string
          account_id?: string
        }[]
      }).channels ?? [],
    {},
    Number.POSITIVE_INFINITY
  )
  return sourceChannelsOf(rows, accounts)
}

export const SERVER_REQUIRED = "انتخاب سرور تلگرام الزامی است."
export const ACCOUNT_REQUIRED = "انتخاب حساب تلگرام الزامی است."
export const SOURCE_REQUIRED = "انتخاب کانال مبدأ الزامی است."
export const SOURCES_REQUIRED = "انتخاب کانال‌های منبع الزامی است."

// The channels one connection can reach. A source picker offers exactly these,
// so a channel from an unrelated connection is never even on the list.
export function channelsForServer(
  channels: SourceChannel[],
  clientKey: string | undefined
): SourceChannel[] {
  if (!clientKey) return []
  return channels.filter((channel) => channel.clientKey === clientKey)
}

// The channels one of the reader's accounts holds. A workflow or webhook is
// bound to one account, so its source list is that account's inventory and
// nothing wider — never every channel the installation happens to know.
export function channelsForAccount(
  channels: SourceChannel[],
  accountId: string | undefined
): SourceChannel[] {
  if (!accountId) return []
  return channels.filter((channel) => channel.accountId === accountId)
}

// The account a saved selection belongs to: the first channel still on file
// names it, exactly as `serverKeyFor` does for the connection.
export function accountIdFor(
  channels: SourceChannel[],
  ids: number[]
): string | undefined {
  for (const id of ids) {
    const channel = channels.find((candidate) => candidate.id === id)
    if (channel?.accountId) return channel.accountId
  }
  return undefined
}

// What the picker must list when an account is chosen: that account's
// channels, plus any already saved channel that belongs to another one. The
// second half keeps an existing source from being silently trimmed by the
// visible filter — those rows stay selected and stay posted.
export function sourceItemsForAccount(
  channels: SourceChannel[],
  accountId: string | undefined,
  selected: number[]
): SourceChannel[] {
  const eligible = channelsForAccount(channels, accountId)
  const kept = channels.filter(
    (channel) =>
      selected.includes(channel.id) && channel.accountId !== accountId
  )
  return [...eligible, ...kept]
}

// The connection a saved selection belongs to. A selection may have been made
// when the list was not filtered by connection at all and may span more than
// one; the first channel still on file names the connection the picker opens
// on, and `preservedSelection` keeps the rest of the record intact.
export function serverKeyFor(
  channels: SourceChannel[],
  ids: number[]
): string | undefined {
  for (const id of ids) {
    const channel = channels.find((candidate) => candidate.id === id)
    if (channel) return channel.clientKey
  }
  return undefined
}

// What the picker must list: the chosen connection's channels, plus any already
// saved channel that belongs to another one. The second half is what keeps an
// existing multi-connection source from being silently trimmed to the visible
// filter — those rows stay selected and stay posted.
export function sourceItems(
  channels: SourceChannel[],
  clientKey: string | undefined,
  selected: number[]
): SourceChannel[] {
  const eligible = channelsForServer(channels, clientKey)
  const kept = channels.filter(
    (channel) =>
      selected.includes(channel.id) &&
      channel.clientKey !== clientKey
  )
  return [...eligible, ...kept]
}

// What survives a deliberate change of connection: only channels the new one
// can reach. A saved selection is preserved while the picker is merely opened
// (`sourceItems` above), but choosing a different connection is a decision, and
// a channel it cannot reach is not a source that can deliver.
export function reachableIds(
  channels: SourceChannel[],
  ids: number[],
  clientKey: string | undefined
): number[] {
  const reachable = new Set(
    channelsForServer(channels, clientKey).map((channel) => channel.id)
  )
  return ids.filter((id) => reachable.has(id))
}

export function SourceServerField({
  id,
  servers,
  value,
  onChange,
  error,
  disabled,
  className,
}: {
  id: string
  servers: TelegramServer[]
  value: string | undefined
  onChange: (key: string | undefined) => void
  error?: string | null
  disabled?: boolean
  className?: string
}) {
  return (
    <Field data-invalid={error ? "" : undefined} className={className}>
      <FieldLabel htmlFor={id} required>
        سرور تلگرام
      </FieldLabel>
      <TelegramServerCombobox
        id={id}
        servers={servers}
        value={value}
        onChange={onChange}
        disabled={disabled}
        className={cn("w-full")}
      />
      {error && <FieldError>{error}</FieldError>}
    </Field>
  )
}
