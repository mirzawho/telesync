import { LoaderCircleIcon } from "lucide-react"
import type * as React from "react"

import { Button } from "@workspace/ui/components/button"

type ButtonVariant = React.ComponentProps<typeof Button>["variant"]
type ButtonSize = React.ComponentProps<typeof Button>["size"]

// SubmitButton is the panel's one submit button: it stays disabled while the
// action is pending, spins a loader, and reads the caller's own in-progress
// wording — the component never invents a generic loading phrase.
export function SubmitButton({
  pending,
  submittingLabel,
  children,
  variant,
  size,
  className,
  type = "submit",
}: {
  pending: boolean
  submittingLabel: string
  children: React.ReactNode
  variant?: ButtonVariant | undefined
  size?: ButtonSize | undefined
  className?: string | undefined
  type?: "submit" | "button" | undefined
}) {
  return (
    <Button
      type={type}
      variant={variant}
      size={size}
      className={className}
      disabled={pending}
    >
      {pending && (
        <LoaderCircleIcon aria-hidden="true" className="animate-spin" />
      )}
      {pending ? submittingLabel : children}
    </Button>
  )
}