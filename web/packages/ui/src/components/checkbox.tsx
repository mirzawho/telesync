import { Checkbox as CheckboxPrimitive } from "@base-ui/react/checkbox"
import { CheckIcon } from "lucide-react"
import * as React from "react"

import { cn } from "@workspace/ui/lib/utils"

// Checkbox is a thin wrapper over Base UI's, matching the panel's field
// styling. The root renders a <span> and a hidden <input>, so `id` addresses
// the real input and a plain <label for> works with it — which is what keeps a
// checkbox's label reachable by a screen reader and a click.

function Checkbox({
  className,
  ...props
}: CheckboxPrimitive.Root.Props) {
  return (
    <CheckboxPrimitive.Root
      data-slot="checkbox"
      className={cn(
        "flex size-4 shrink-0 items-center justify-center rounded-[4px] border border-input bg-transparent transition-colors outline-none focus-visible:border-ring focus-visible:ring-3 focus-visible:ring-ring/50 disabled:cursor-not-allowed disabled:opacity-50 aria-invalid:border-destructive data-[checked]:border-primary data-[checked]:bg-primary data-[checked]:text-primary-foreground",
        className
      )}
      {...props}
    >
      <CheckboxPrimitive.Indicator
        data-slot="checkbox-indicator"
        className="flex items-center justify-center text-current"
      >
        <CheckIcon className="size-3" />
      </CheckboxPrimitive.Indicator>
    </CheckboxPrimitive.Root>
  )
}

export { Checkbox }
