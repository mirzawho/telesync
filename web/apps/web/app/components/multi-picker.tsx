import * as React from "react"

import { MultiCombobox } from "@workspace/ui/components/combobox"

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
}: {
  id: string
  title: string
  description?: string
  placeholder: string
  emptyText: string
  items: T[]
  value: number[]
  onValueChange: (ids: number[]) => void
  labelOf: (item: T) => string
}) {
  const byID = React.useMemo(
    () => new Map(items.map((item) => [item.id, item])),
    [items]
  )
  const selected = value
    .map((itemID) => byID.get(itemID))
    .filter((item): item is T => item !== undefined)
  return (
    <section className="flex flex-col gap-2" aria-labelledby={id}>
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
      />
      {description && (
        <p className="text-xs text-muted-foreground">{description}</p>
      )}
    </section>
  )
}
