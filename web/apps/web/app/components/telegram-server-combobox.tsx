import {
  Combobox,
  ComboboxChips,
  ComboboxChipsInput,
  ComboboxContent,
  ComboboxEmpty,
  ComboboxItem,
  ComboboxList,
  useComboboxAnchor,
} from "@workspace/ui/components/combobox"
import { cn } from "@workspace/ui/lib/utils"

import { connectionLabels, connectionOf } from "@/lib/server-state"
import type { TelegramServer } from "@/lib/types"

// TelegramServerCombobox picks one Telegram connection. The caller loads
// /telegram in its own loader and passes the rows in: this component never
// fetches, so loading, error and permission states stay the page's to render.
export function TelegramServerCombobox({
  servers,
  value,
  onChange,
  placeholder = "— اتصال را انتخاب کنید —",
  disabled,
  emptyText = "سروری یافت نشد.",
  className,
  id,
  "aria-label": ariaLabel = "اتصال تلگرام",
}: {
  servers: TelegramServer[]
  value: string | undefined
  onChange: (key: string | undefined) => void
  placeholder?: string
  disabled?: boolean
  emptyText?: string
  className?: string
  id?: string
  "aria-label"?: string
}) {
  const anchor = useComboboxAnchor()
  // A value that is not in `servers` (stale key, stripped list) selects
  // nothing; the field falls back to its placeholder.
  const chosen =
    value === undefined
      ? undefined
      : (servers.find((server) => server.key === value) ?? null)

  return (
    <div className={cn("w-full", className)}>
      <Combobox
        items={servers}
        autoHighlight
        disabled={disabled}
        value={chosen}
        onValueChange={(next) => onChange((next as TelegramServer | null)?.key)}
        itemToStringLabel={(server) => server.key}
      >
        <ComboboxChips
          ref={anchor}
          className="w-full"
          data-disabled={disabled ? "" : undefined}
        >
          <ComboboxChipsInput
            id={id}
            aria-label={ariaLabel}
            placeholder={placeholder}
            className="text-sm"
          />
        </ComboboxChips>
        <ComboboxContent anchor={anchor}>
          <ComboboxEmpty>{emptyText}</ComboboxEmpty>
          <ComboboxList>
            {(server: TelegramServer) => (
              <ComboboxItem key={server.key} value={server}>
                <span className="flex min-w-0 flex-col">
                  <span className="font-mono text-sm">{server.key}</span>
                  <span className="text-muted-foreground text-xs">
                    {server.type === "custom" ? "سرور اختصاصی" : "رسمی تلگرام"}
                    {` — ${connectionLabels[connectionOf(server)]}`}
                  </span>
                </span>
              </ComboboxItem>
            )}
          </ComboboxList>
        </ComboboxContent>
      </Combobox>
    </div>
  )
}