// The two controls the publishing workspace needs that are not single
// components anywhere else: an editable chip list, and the searchable
// multi-select list the destinations and the article's terms share.
//
// Everything else is imported from @workspace/ui — the checkbox and the
// textarea are the shared ones, so this module never restates the design
// system. What is left here is behaviour, not styling.

import { XIcon } from "lucide-react"
import * as React from "react"

import { Badge } from "@workspace/ui/components/badge"
import { Checkbox } from "@workspace/ui/components/checkbox"
import { Input } from "@workspace/ui/components/input"
import { cn } from "@workspace/ui/lib/utils"

// display shows a token with its prefix exactly once: a mention the user typed
// with its '@' keeps it, and one they typed bare is given it. The stored value
// is never rewritten to match the picture.
function display(prefix: string, value: string): string {
  return value.startsWith(prefix) ? value : `${prefix}${value}`
}

/**
 * ChipInput is an editable list of short tokens — hashtags, mentions.
 *
 * Each entry is a chip that can be removed, and a new one is committed on Enter
 * or on the add button. The chips and the input are siblings, never nested
 * interactive elements, and every remove button carries its own label.
 */
export function ChipInput({
  values,
  onChange,
  prefix,
  placeholder,
  addLabel,
  removeLabel,
  emptyLabel,
  disabled,
  id,
  className,
  "aria-describedby": ariaDescribedBy,
}: {
  values: string[]
  onChange: (next: string[]) => void
  // prefix is how a token is shown ("#", "@"); it is not stored.
  prefix: string
  placeholder: string
  addLabel: string
  removeLabel: (value: string) => string
  emptyLabel: string
  disabled?: boolean
  id?: string
  className?: string
  "aria-describedby"?: string
}) {
  const [draft, setDraft] = React.useState("")

  const commit = () => {
    const value = draft.trim()
    if (!value) return
    if (!values.includes(value)) onChange([...values, value])
    setDraft("")
  }

  return (
    <div className={cn("flex flex-col gap-2", className)}>
      <div className="flex flex-wrap items-center gap-1.5">
        {values.length === 0 && (
          <span className="text-xs text-muted-foreground">{emptyLabel}</span>
        )}
        {values.map((value) => (
          <Badge key={value} variant="secondary" className="gap-1">
            <span dir="ltr">{display(prefix, value)}</span>
            {!disabled && (
              <button
                type="button"
                aria-label={removeLabel(value)}
                className="rounded-full p-0.5 text-muted-foreground outline-none hover:text-destructive focus-visible:ring-2 focus-visible:ring-ring"
                onClick={() => onChange(values.filter((v) => v !== value))}
              >
                <XIcon className="size-3" aria-hidden="true" />
              </button>
            )}
          </Badge>
        ))}
      </div>
      {!disabled && (
        <div className="flex gap-2">
          <Input
            id={id}
            value={draft}
            dir="ltr"
            placeholder={placeholder}
            aria-describedby={ariaDescribedBy}
            onChange={(e: React.ChangeEvent<HTMLInputElement>) => setDraft(e.target.value)}
            onKeyDown={(e: React.KeyboardEvent<HTMLInputElement>) => {
              if (e.key === "Enter") {
                e.preventDefault()
                commit()
              }
            }}
          />
          <button
            type="button"
            className="shrink-0 rounded-lg border border-input px-3 text-sm hover:bg-accent disabled:opacity-50"
            onClick={commit}
            disabled={!draft.trim()}
          >
            {addLabel}
          </button>
        </div>
      )}
    </div>
  )
}

/**
 * SearchableCheckList is the multi-select the workspace uses everywhere: a
 * search box over a scrollable list of checkboxes, with a count underneath.
 *
 * Searching never changes what is selected — it only narrows what is listed —
 * so a filter applied in passing cannot quietly drop a choice. An item that
 * exists but may not be picked is shown disabled rather than hidden, so the
 * user can see it and understand why.
 */
export function SearchableCheckList<T extends { id: string | number }>({
  items,
  selected,
  onToggle,
  searchPlaceholder,
  emptyText,
  noMatchText,
  labelOf,
  detailOf,
  disabledOf,
  footer,
  id,
  className,
}: {
  items: T[]
  selected: Array<string | number>
  onToggle: (id: string | number) => void
  searchPlaceholder: string
  emptyText: string
  noMatchText: string
  labelOf: (item: T) => string
  detailOf?: (item: T) => string | undefined
  disabledOf?: (item: T) => boolean
  footer?: React.ReactNode
  id?: string
  className?: string
}) {
  const [query, setQuery] = React.useState("")
  const shown = React.useMemo(() => {
    const needle = query.trim().toLowerCase()
    if (!needle) return items
    return items.filter((item) => {
      const detail = detailOf?.(item) ?? ""
      return (
        labelOf(item).toLowerCase().includes(needle) || detail.toLowerCase().includes(needle)
      )
    })
  }, [items, query, labelOf, detailOf])

  return (
    <div className={cn("flex flex-col gap-2", className)}>
      <input
        id={id}
        type="search"
        value={query}
        placeholder={searchPlaceholder}
        className="flex h-8 w-full rounded-lg border border-input bg-transparent px-2.5 text-sm outline-none placeholder:text-muted-foreground focus-visible:border-ring focus-visible:ring-3 focus-visible:ring-ring/50"
        onChange={(e: React.ChangeEvent<HTMLInputElement>) => setQuery(e.target.value)}
      />
      <div className="max-h-64 overflow-y-auto rounded-lg border">
        {shown.length === 0 && (
          <p className="px-3 py-6 text-center text-xs text-muted-foreground">
            {items.length === 0 ? emptyText : noMatchText}
          </p>
        )}
        <ul className="flex flex-col">
          {shown.map((item) => {
            const label = labelOf(item)
            const detail = detailOf?.(item)
            const disabled = disabledOf?.(item) ?? false
            const checked = selected.includes(item.id)
            return (
              <li key={String(item.id)}>
                <label
                  className={cn(
                    "flex cursor-pointer items-center gap-2.5 px-3 py-2 text-sm hover:bg-accent",
                    disabled && "cursor-not-allowed opacity-60 hover:bg-transparent",
                  )}
                >
                  <Checkbox
                    checked={checked}
                    disabled={disabled}
                    onCheckedChange={() => onToggle(item.id)}
                    aria-label={disabled ? `${label} (قابل انتخاب نیست)` : label}
                  />
                  <span className="min-w-0 flex-1 truncate">{label}</span>
                  {detail && (
                    <span dir="ltr" className="shrink-0 text-xs text-muted-foreground">
                      {detail}
                    </span>
                  )}
                </label>
              </li>
            )
          })}
        </ul>
      </div>
      {footer}
    </div>
  )
}

/**
 * Field is one labelled control with its optional error.
 *
 * `required` draws the red asterisk the rest of the panel uses for a field the
 * API needs, and the error is wired to the control through aria-describedby so
 * a screen reader reads it with the field rather than as loose text.
 */
export function Field({
  label,
  htmlFor,
  required,
  hint,
  error,
  children,
  className,
}: {
  label: string
  htmlFor?: string
  required?: boolean
  hint?: string
  error?: string | undefined
  children: React.ReactNode
  className?: string
}) {
  const hintId = htmlFor && hint ? `${htmlFor}-hint` : undefined
  const errorId = htmlFor && error ? `${htmlFor}-error` : undefined
  return (
    <div className={cn("flex flex-col gap-1.5", className)}>
      <label htmlFor={htmlFor} className="text-sm font-medium">
        {label}
        {required && (
          <span aria-hidden="true" className="ms-1 text-destructive">
            *
          </span>
        )}
      </label>
      {children}
      {hint && (
        <p id={hintId} className="text-xs text-muted-foreground">
          {hint}
        </p>
      )}
      {error && (
        <p id={errorId} role="alert" className="text-xs font-medium text-destructive">
          {error}
        </p>
      )}
    </div>
  )
}
