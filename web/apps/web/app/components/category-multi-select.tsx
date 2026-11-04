import { MultiPicker } from "@/components/multi-picker"

// CategoryMultiSelect is the one-way pages import to pick categories: a thin
// typed wrapper over the shared MultiPicker, so every dialog's category field
// looks and behaves the same. `name` is optional: when a dialog keeps the
// selection in state, it also mirrors the ids as repeated hidden inputs so a
// submitted form still carries every chosen id (clientAction's
// form.getAll("category_ids")).
export function CategoryMultiSelect({
  id,
  title,
  categories,
  value,
  onChange,
  placeholder = "جستجوی دسته‌بندی…",
  emptyText = "دسته‌بندی‌ای یافت نشد.",
  description,
  disabled,
  className,
  name,
}: {
  id: string
  title?: string
  categories: { id: number; name: string }[]
  value: number[]
  onChange: (ids: number[]) => void
  placeholder?: string
  emptyText?: string
  description?: string
  disabled?: boolean
  className?: string
  name?: string
}) {
  return (
    <>
      <MultiPicker
        id={id}
        title={title}
        description={description}
        placeholder={placeholder}
        emptyText={emptyText}
        items={categories}
        value={value}
        onValueChange={onChange}
        labelOf={(category) => category.name}
        disabled={disabled}
        className={className}
      />
      {name &&
        value.map((categoryID) => (
          <input key={categoryID} type="hidden" name={name} value={categoryID} />
        ))}
    </>
  )
}