import { EyeIcon, EyeOffIcon, KeyRoundIcon, UndoIcon } from "lucide-react"
import * as React from "react"

import { Button } from "@workspace/ui/components/button"
import {
  Field,
  FieldDescription,
  FieldError,
  FieldLabel,
} from "@workspace/ui/components/field"
import { Input } from "@workspace/ui/components/input"

// The webhook secret field.
//
// The API never returns a stored secret, only `has_secret`, so this field can
// never be pre-filled and never shows a value it was not given. Three states:
//
//   null   — untouched. Whatever is stored stays stored.
//   ""     — the stored secret will be removed on save.
//   "…"    — this exact value replaces the stored secret on save.
//
// The mask is the input's own `password` type, so a shoulder-surfer sees dots
// and the reveal button is opt-in and local to the field.

export function SecretField({
  id,
  hasSecret,
  value,
  onValueChange,
  error,
  disabled,
  label = "رمز وب‌هوک",
}: {
  id: string
  hasSecret: boolean
  value: string | null
  onValueChange: (next: string | null) => void
  error?: string | null
  disabled?: boolean
  label?: string
}) {
  const [reveal, setReveal] = React.useState(false)

  if (value === null) {
    return (
      <Field>
        <FieldLabel htmlFor={id}>{label}</FieldLabel>
        <div className="flex flex-wrap items-center gap-2 rounded-lg border px-3 py-2">
          <KeyRoundIcon
            className="size-4 text-muted-foreground"
            aria-hidden="true"
          />
          <p className="min-w-0 flex-1 text-sm text-muted-foreground">
            {hasSecret
              ? "رمزی ذخیره شده است و در این‌جا نمایش داده نمی‌شود."
              : "هنوز رمزی ثبت نشده است."}
          </p>
          <Button
            type="button"
            variant="outline"
            size="sm"
            disabled={disabled}
            onClick={() => onValueChange("")}
          >
            {hasSecret ? "جایگزینی رمز" : "افزودن رمز"}
          </Button>
        </div>
        <FieldDescription>
          رمز هرگز پس از ذخیره نمایش داده نمی‌شود؛ برای تغییر، مقدار جدید را وارد
          کنید.
        </FieldDescription>
      </Field>
    )
  }

  const clearing = value === ""
  return (
    <Field data-invalid={error ? "" : undefined}>
      <FieldLabel htmlFor={id}>{label}</FieldLabel>
      <div className="relative">
        <Input
          id={id}
          dir="ltr"
          type={reveal ? "text" : "password"}
          autoComplete="new-password"
          className="pe-10"
          value={value}
          placeholder={hasSecret ? "رمز جایگزین…" : "رمز وب‌هوک…"}
          disabled={disabled}
          onChange={(event) => onValueChange(event.target.value)}
        />
        <Button
          type="button"
          variant="ghost"
          size="icon-sm"
          className="absolute inset-y-0 end-1 my-auto"
          aria-label={reveal ? "مخفی کردن رمز" : "نمایش رمز"}
          aria-pressed={reveal}
          title={reveal ? "مخفی کردن رمز" : "نمایش رمز"}
          disabled={disabled}
          onClick={() => setReveal((previous) => !previous)}
        >
          {reveal ? (
            <EyeOffIcon className="size-4" />
          ) : (
            <EyeIcon className="size-4" />
          )}
        </Button>
      </div>
      {error ? (
        <FieldError>{error}</FieldError>
      ) : clearing && hasSecret ? (
        <p role="status" className="text-xs font-medium text-destructive">
          با ذخیره، رمز ذخیره‌شده حذف می‌شود.
        </p>
      ) : (
        <FieldDescription>
          مقدار جدید جایگزین رمز قبلی می‌شود. برای دست نزدن به رمز فعلی، انصراف
          دهید.
        </FieldDescription>
      )}
      <div className="flex flex-wrap items-center gap-2">
        {hasSecret && (
          <Button
            type="button"
            variant="ghost"
            size="sm"
            disabled={disabled}
            onClick={() => onValueChange(null)}
          >
            <UndoIcon data-icon="inline-start" />
            دست نزدن به رمز ذخیره‌شده
          </Button>
        )}
        {!clearing && (
          <Button
            type="button"
            variant="ghost"
            size="sm"
            disabled={disabled}
            onClick={() => onValueChange("")}
          >
            حذف رمز ذخیره‌شده
          </Button>
        )}
      </div>
    </Field>
  )
}
