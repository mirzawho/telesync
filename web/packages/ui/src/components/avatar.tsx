import * as React from "react"

import { cn } from "@workspace/ui/lib/utils"

function Avatar({ className, ...props }: React.ComponentProps<"span">) {
  return (
    <span
      data-slot="avatar"
      className={cn(
        "relative flex shrink-0 overflow-hidden rounded-full bg-muted",
        className
      )}
      {...props}
    />
  )
}

function AvatarFallback({ className, ...props }: React.ComponentProps<"span">) {
  return (
    <span
      data-slot="avatar-fallback"
      className={cn(
        "flex size-full items-center justify-center rounded-full text-muted-foreground",
        className
      )}
      {...props}
    />
  )
}

export { Avatar, AvatarFallback }
