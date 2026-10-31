import { SearchIcon } from "lucide-react"
import * as React from "react"

import { Input } from "@workspace/ui/components/input"

// SearchInput submits on Enter (the surrounding form does the navigation) and
// the clear button resets it, keeping the address bar the source of truth for
// the filter.
export function SearchInput({
  value,
  onChange,
  placeholder = "جستجو…",
  name = "q",
  className,
}: {
  value: string
  onChange: (value: string) => void
  placeholder?: string
  name?: string
  className?: string
}) {
  const inputId = React.useId()
  return (
    <div className={className}>
      <label htmlFor={inputId} className="sr-only">
        {placeholder}
      </label>
      <div className="relative">
        <SearchIcon className="text-muted-foreground pointer-events-none absolute start-2.5 top-1/2 size-4 -translate-y-1/2" />
        <Input
          id={inputId}
          name={name}
          type="search"
          value={value}
          onChange={(event) => onChange(event.target.value)}
          placeholder={placeholder}
          className="ps-8"
        />
      </div>
    </div>
  )
}
