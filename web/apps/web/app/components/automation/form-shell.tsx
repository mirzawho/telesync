import type * as React from "react"
import { LoaderCircleIcon } from "lucide-react"

import { Button } from "@workspace/ui/components/button"
import { cn } from "@workspace/ui/lib/utils"

// The one shell both automation forms are drawn in, so the create and edit
// pages of both collections cannot drift apart in width, spacing or in the
// order their actions sit in.
//
// The panel's page column already sets the horizontal gutters and the bottom
// inset for the floating navigation (see `_panel/layout.tsx`), so this adds the
// content width and the vertical rhythm and nothing else.

export function AutomationFormLayout({
  children,
  className,
}: {
  children: React.ReactNode
  className?: string
}) {
  return (
    <div className={cn("mx-auto flex w-full max-w-4xl flex-col gap-6", className)}>
      {children}
    </div>
  )
}

// The two actions every form has, in one order. The container is RTL, so the
// first element in the DOM lands on the physical right: Cancel comes first and
// the primary action second, which puts the primary action at the outermost
// physical LEFT and Cancel immediately to its right.
export function AutomationFormActions({
  formId,
  submitLabel,
  pendingLabel,
  pending,
  disabled,
  onCancel,
}: {
  formId: string
  submitLabel: string
  pendingLabel: string
  pending: boolean
  disabled?: boolean
  onCancel: () => void
}) {
  return (
    <>
      <Button
        type="button"
        variant="outline"
        onClick={onCancel}
        disabled={pending || disabled}
      >
        انصراف
      </Button>
      <Button type="submit" form={formId} disabled={pending || disabled}>
        {pending && (
          <LoaderCircleIcon aria-hidden="true" className="animate-spin" />
        )}
        {pending ? pendingLabel : submitLabel}
      </Button>
    </>
  )
}
