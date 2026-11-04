import * as React from "react"

import { MultiCombobox } from "@workspace/ui/components/combobox"
import { cn } from "@workspace/ui/lib/utils"

// MultiPicker is the app's multi-select field: a titled, searchable combobox
// whose picked items are chips. The administrators dialog and the WordPress
// site dialog both choose categories with it, so the two look the same.
export function MultiPicker<T extends { id: number }>({
  id,
  title,
  description,
  placeholder,
  emptyText,
  items,
  value,
  onValueChange,
  labelOf,
  disabled,
  className,
}: {
  id: string
  title?: string
  description?: string
  placeholder: string
  emptyText: string
  items: T[]
  value: number[]
  onValueChange: (ids: number[]) => void
  labelOf: (item: T) => string
  disabled?: boolean
  className?: string
}) {
  const byID = React.useMemo(
    () => new Map(items.map((item) => [item.id, item])),
    [items]
  )
  const selected = value
    .map((itemID) => byID.get(itemID))
    .filter((item): item is T => item !== undefined)
  return (
    <section
      className={cn("flex flex-col gap-2", className)}
      aria-labelledby={id}
      aria-disabled={disabled || undefined}
    >
      <h3 id={id} className="text-sm font-medium">
        {title}
      </h3>
      <MultiCombobox
        items={items}
        value={selected}
        onValueChange={(next) => onValueChange(next.map((item) => item.id))}
        placeholder={placeholder}
        emptyText={emptyText}
        labelOf={labelOf}
        disabled={disabled}
      />
      {description && (
        <p className="text-xs text-muted-foreground">{description}</p>
      )}
    </section>
  )
}
