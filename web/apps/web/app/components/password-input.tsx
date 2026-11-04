import * as React from "react"

import { EyeIcon, EyeOffIcon } from "lucide-react"

import { Button } from "@workspace/ui/components/button"
import {
  Field,
  FieldControl,
  FieldDescription,
  FieldError,
  FieldLabel,
} from "@workspace/ui/components/field"

// PasswordInput is the panel's one password field: hidden by default, with an
// eye button that flips the input between password and text without touching
// the value. The button sits at the input's own `end` edge — the physical left
// in this RTL panel — and is a plain button, so pressing it never submits the
// form it is inside. The administrator forms and the account's own password
// dialog both use it, so the visibility rule exists once.
export function PasswordInput({
  id,
  name,
  label,
  autoComplete = "new-password",
  value,
  onChange,
  onBlur,
  error,
  description,
  required = true,
  show: controlledShow,
  onToggleShow,
}: {
  id: string
  name?: string
  label: string
  autoComplete?: string
  value: string
  onChange: (value: string) => void
  onBlur?: () => void
  error?: string | null
  description?: string
  required?: boolean
  // Controlled visibility, for a form that shows several fields at once.
  show?: boolean
  onToggleShow?: () => void
}) {
  const [uncontrolledShow, setUncontrolledShow] = React.useState(false)
  const show = controlledShow ?? uncontrolledShow
  const toggle = () => {
    if (onToggleShow) onToggleShow()
    else setUncontrolledShow((previous) => !previous)
  }

  return (
    <Field data-invalid={error ? "" : undefined}>
      <FieldLabel htmlFor={id} required={required}>
        {label}
      </FieldLabel>
      <div className="relative">
        <FieldControl
          id={id}
          name={name}
          dir="ltr"
          type={show ? "text" : "password"}
          autoComplete={autoComplete}
          className="pe-10"
          value={value}
          onChange={(event) => onChange(event.target.value)}
          onBlur={onBlur}
          required={required}
        />
        <Button
          type="button"
          variant="ghost"
          size="icon-sm"
          className="absolute inset-y-0 end-1 my-auto"
          aria-label={show ? "مخفی کردن رمز عبور" : "نمایش رمز عبور"}
          aria-pressed={show}
          title={show ? "مخفی کردن رمز عبور" : "نمایش رمز عبور"}
          onClick={toggle}
        >
          {show ? (
            <EyeOffIcon className="size-4" />
          ) : (
            <EyeIcon className="size-4" />
          )}
        </Button>
      </div>
      {error ? <FieldError>{error}</FieldError> : null}
      {!error && description ? (
        <FieldDescription>{description}</FieldDescription>
      ) : null}
    </Field>
  )
}
