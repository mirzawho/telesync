import type * as React from "react"

import { cn } from "@workspace/ui/lib/utils"

// Textarea is Input's multiline sibling, in the same border, radius, focus ring
// and invalid state, so a form does not change character halfway down the page.
// It is a plain element in the panel's tokens rather than a primitive: Base UI
// has no textarea, and the styling is what needs to be shared here.

function Textarea({ className, ...props }: React.ComponentProps<"textarea">) {
  return (
    <textarea
      data-slot="textarea"
      className={cn(
        "flex min-h-20 w-full min-w-0 rounded-lg border border-input bg-transparent px-2.5 py-2 text-sm transition-colors outline-none placeholder:text-muted-foreground focus-visible:border-ring focus-visible:ring-3 focus-visible:ring-ring/50 disabled:pointer-events-none disabled:cursor-not-allowed disabled:opacity-50 data-[invalid]:border-destructive data-[invalid]:ring-3 data-[invalid]:ring-destructive/20 dark:bg-input/30 dark:data-[invalid]:border-destructive/50 dark:data-[invalid]:ring-destructive/40 aria-invalid:border-destructive aria-invalid:ring-3 aria-invalid:ring-destructive/20 dark:aria-invalid:border-destructive/50 dark:aria-invalid:ring-destructive/40",
        className
      )}
      {...props}
    />
  )
}

export { Textarea }
