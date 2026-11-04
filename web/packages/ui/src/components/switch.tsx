import { Switch as SwitchPrimitive } from "@base-ui/react/switch"
import * as React from "react"

import { cn } from "@workspace/ui/lib/utils"

// Switch is a thin wrapper over Base UI's, matching the panel's field styling.
// The account-status row uses it; the track and thumb follow the same border and
// ring language as the rest of the controls so a switch does not read as a
// foreign element next to a checkbox.

function Switch({
  className,
  ...props
}: SwitchPrimitive.Root.Props & { className?: string }) {
  return (
    <SwitchPrimitive.Root
      data-slot="switch"
      className={cn(
        "peer inline-flex h-5 w-9 shrink-0 items-center rounded-full border border-input bg-muted transition-colors outline-none focus-visible:border-ring focus-visible:ring-3 focus-visible:ring-ring/50 disabled:cursor-not-allowed disabled:opacity-50 data-[checked]:border-primary data-[checked]:bg-primary",
        className
      )}
      {...props}
    >
      <SwitchPrimitive.Thumb
        data-slot="switch-thumb"
        className="block size-3.5 rounded-full bg-background shadow-sm transition-transform data-[checked]:translate-x-4.5 data-[unchecked]:translate-x-0.5 rtl:data-[checked]:-translate-x-4.5 rtl:data-[unchecked]:-translate-x-0.5"
      />
    </SwitchPrimitive.Root>
  )
}

export { Switch }
