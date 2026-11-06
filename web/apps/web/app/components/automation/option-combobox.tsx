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

// OptionCombobox is the automation area's single-select field. There is no
// Select component in this workspace, so every choice of one from a short list
// — the HTTP method, the auth mode, a WordPress site — goes through this and
// reads the same. The caller owns the rows; this never fetches, so a loading,
// an error and an empty list stay the page's to render.

export interface ComboOption {
  value: string
  label: string
  detail?: string
}

export function OptionCombobox({
  id,
  label,
  options,
  value,
  onChange,
  placeholder,
  emptyText = "موردی یافت نشد.",
  disabled,
  className,
}: {
  id?: string
  label: string
  options: ComboOption[]
  value: string
  onChange: (value: string) => void
  placeholder: string
  emptyText?: string
  disabled?: boolean
  className?: string
}) {
  const anchor = useComboboxAnchor()
  // A value the options do not name selects nothing: the field falls back to
  // its placeholder rather than claiming a choice that is not on the list.
  const chosen = options.find((option) => option.value === value) ?? null

  return (
    <div className={className ?? "w-full"}>
      <Combobox
        items={options}
        autoHighlight
        disabled={disabled}
        value={chosen}
        onValueChange={(next) => onChange((next as ComboOption | null)?.value ?? value)}
        itemToStringLabel={(option) => option.label}
      >
        <ComboboxChips ref={anchor} className="w-full" data-disabled={disabled ? "" : undefined}>
          <ComboboxChipsInput
            id={id}
            aria-label={label}
            placeholder={placeholder}
            className="text-sm"
          />
        </ComboboxChips>
        <ComboboxContent anchor={anchor}>
          <ComboboxEmpty>{emptyText}</ComboboxEmpty>
          <ComboboxList>
            {(option: ComboOption) => (
              <ComboboxItem key={option.value} value={option}>
                <span className="flex min-w-0 flex-col">
                  <span dir="auto" className="text-sm">
                    {option.label}
                  </span>
                  {option.detail && (
                    <span dir="auto" className="text-xs text-muted-foreground">
                      {option.detail}
                    </span>
                  )}
                </span>
              </ComboboxItem>
            )}
          </ComboboxList>
        </ComboboxContent>
      </Combobox>
    </div>
  )
}
