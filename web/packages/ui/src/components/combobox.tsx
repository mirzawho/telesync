import { Combobox as ComboboxPrimitive } from "@base-ui/react/combobox"
import * as React from "react"

import { cn } from "@workspace/ui/lib/utils"

// Combobox in the project's base-ui style: a text field with a filtered list,
// and, with `multiple`, the chosen items as chips inside the field.

const Combobox = ComboboxPrimitive.Root

const ComboboxValue = ComboboxPrimitive.Value

function ComboboxChips({
  className,
  ...props
}: ComboboxPrimitive.Chips.Props & { className?: string }) {
  return (
    <ComboboxPrimitive.Chips
      data-slot="combobox-chips"
      className={cn(
        "flex min-h-8 w-full flex-wrap items-center gap-1 rounded-lg border border-input bg-transparent px-1.5 py-1 text-sm transition-colors focus-within:border-ring focus-within:ring-3 focus-within:ring-ring/50 data-[invalid]:border-destructive data-[invalid]:ring-3 data-[invalid]:ring-destructive/20 dark:bg-input/30",
        className
      )}
      {...props}
    />
  )
}

function ComboboxChip({
  className,
  children,
  ...props
}: ComboboxPrimitive.Chip.Props & { className?: string }) {
  return (
    <ComboboxPrimitive.Chip
      data-slot="combobox-chip"
      className={cn(
        "inline-flex items-center gap-1 rounded-md bg-secondary px-1.5 py-0.5 text-xs font-medium text-secondary-foreground",
        className
      )}
      {...props}
    >
      {children}
    </ComboboxPrimitive.Chip>
  )
}

function ComboboxChipRemove(props: ComboboxPrimitive.ChipRemove.Props) {
  return (
    <ComboboxPrimitive.ChipRemove
      data-slot="combobox-chip-remove"
      className="text-muted-foreground hover:text-foreground inline-flex size-4 items-center justify-center rounded-sm outline-none"
      {...props}
    >
      <span aria-hidden="true" className="text-[10px] leading-none">
        ✕
      </span>
    </ComboboxPrimitive.ChipRemove>
  )
}

function ComboboxChipsInput({
  className,
  ...props
}: ComboboxPrimitive.Input.Props & { className?: string }) {
  return (
    <ComboboxPrimitive.Input
      data-slot="combobox-chips-input"
      className={cn(
        "h-6 min-w-24 flex-1 bg-transparent px-1 outline-none placeholder:text-muted-foreground disabled:cursor-not-allowed disabled:opacity-50",
        className
      )}
      {...props}
    />
  )
}

// useComboboxAnchor is the chips element the popup positions against.
function useComboboxAnchor() {
  return React.useRef<HTMLDivElement>(null)
}

// ComboboxContent anchors the popup to the chips; without the anchor it would
// open against the pointer, not the field.
function ComboboxContent({
  className,
  anchor,
  children,
  ...props
}: ComboboxPrimitive.Popup.Props & {
  className?: string
  anchor?: React.RefObject<HTMLElement | null>
}) {
  return (
    <ComboboxPrimitive.Portal>
      <ComboboxPrimitive.Positioner
        anchor={anchor}
        sideOffset={6}
        className="z-50 outline-none"
      >
        <ComboboxPrimitive.Popup
          data-slot="combobox-content"
          className={cn(
            "max-h-64 w-full min-w-(--anchor-width) overflow-y-auto rounded-lg bg-popover p-1 text-popover-foreground shadow-md ring-1 ring-foreground/10 outline-none data-open:animate-in data-open:fade-in-0 data-open:zoom-in-95 data-closed:animate-out data-closed:fade-out-0 data-closed:zoom-out-95",
            className
          )}
          {...props}
        >
          {children}
        </ComboboxPrimitive.Popup>
      </ComboboxPrimitive.Positioner>
    </ComboboxPrimitive.Portal>
  )
}

function ComboboxList(props: ComboboxPrimitive.List.Props) {
  return <ComboboxPrimitive.List data-slot="combobox-list" {...props} />
}

function ComboboxItem({
  className,
  children,
  ...props
}: ComboboxPrimitive.Item.Props & { className?: string }) {
  return (
    <ComboboxPrimitive.Item
      data-slot="combobox-item"
      className={cn(
        "relative flex cursor-default items-center gap-2 rounded-md px-2 py-1.5 text-sm outline-hidden select-none data-highlighted:bg-accent data-highlighted:text-accent-foreground data-disabled:pointer-events-none data-disabled:opacity-50",
        className
      )}
      {...props}
    >
      {children}
    </ComboboxPrimitive.Item>
  )
}

function ComboboxEmpty({
  className,
  ...props
}: ComboboxPrimitive.Empty.Props & { className?: string }) {
  return (
    <ComboboxPrimitive.Empty
      data-slot="combobox-empty"
      className={cn("px-2 py-3 text-center text-sm text-muted-foreground", className)}
      {...props}
    />
  )
}

// MultiCombobox is the one multi-select the panel uses: chips in the field,
// a list filtered as you type, and item labels read from `labelOf`.
function MultiCombobox<T>({
  items,
  value,
  onValueChange,
  placeholder,
  emptyText,
  labelOf,
  className,
}: {
  items: T[]
  value: T[]
  onValueChange: (value: T[]) => void
  placeholder?: string
  emptyText?: string
  labelOf: (item: T) => string
  className?: string
}) {
  const anchor = useComboboxAnchor()
  return (
    <Combobox
      multiple
      autoHighlight
      items={items}
      value={value}
      onValueChange={(next) => onValueChange((next as T[] | null) ?? [])}
      itemToStringLabel={labelOf}
      itemToStringValue={labelOf}
    >
      <ComboboxChips ref={anchor} className={cn("w-full", className)}>
        <ComboboxValue>
          {(values: T[]) => (
            <React.Fragment>
              {values.map((item) => (
                <ComboboxChip key={labelOf(item)}>{labelOf(item)}</ComboboxChip>
              ))}
              <ComboboxChipsInput placeholder={placeholder} />
            </React.Fragment>
          )}
        </ComboboxValue>
      </ComboboxChips>
      <ComboboxContent anchor={anchor}>
        <ComboboxEmpty>{emptyText ?? "Nothing found."}</ComboboxEmpty>
        <ComboboxList>
          {(item: T) => (
            <ComboboxItem key={labelOf(item)} value={item}>
              {labelOf(item)}
            </ComboboxItem>
          )}
        </ComboboxList>
      </ComboboxContent>
    </Combobox>
  )
}

export {
  Combobox,
  ComboboxChip,
  ComboboxChips,
  ComboboxChipsInput,
  ComboboxContent,
  ComboboxEmpty,
  ComboboxItem,
  ComboboxList,
  ComboboxValue,
  MultiCombobox,
  useComboboxAnchor,
}
