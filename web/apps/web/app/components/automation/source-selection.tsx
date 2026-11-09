import { Field, FieldError, FieldLabel } from "@workspace/ui/components/field"
import { cn } from "@workspace/ui/lib/utils"

import { TelegramServerCombobox } from "@/components/telegram-server-combobox"
import type { TelegramServer } from "@/lib/types"

// A managed channel a form may name as its source, together with the Telegram
// connection it belongs to. The id is the local managed_channels.id — never a
// Telegram peer id — and `clientKey` is the connection that can reach it.
export interface SourceChannel {
  id: number
  label: string
  clientKey: string
  // A second line for the picker, when the caller has one to show.
  detail?: string
}

// The name the webhook table has always read this shape by.
export type SourceChannelOption = SourceChannel

export const SERVER_REQUIRED = "انتخاب سرور تلگرام الزامی است."
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
