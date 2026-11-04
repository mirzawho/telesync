import { ScrollArea as ScrollAreaPrimitive } from "@base-ui/react/scroll-area"
import * as React from "react"

import { cn } from "@workspace/ui/lib/utils"

// ScrollArea is the panel's scrollable region: an overflow box with a scrollbar
// that fits the theme. It is used where two regions scroll independently —
// the channel list beside the message timeline — so neither drags the other.

function ScrollArea({
  className,
  children,
  ...props
}: ScrollAreaPrimitive.Root.Props) {
  return (
    <ScrollAreaPrimitive.Root
      data-slot="scroll-area"
      className={cn("relative overflow-hidden", className)}
      {...props}
    >
      <ScrollAreaPrimitive.Viewport
        data-slot="scroll-area-viewport"
        className="size-full rounded-[inherit]"
      >
        {children}
      </ScrollAreaPrimitive.Viewport>
      <ScrollBar />
      <ScrollAreaPrimitive.Corner />
    </ScrollAreaPrimitive.Root>
  )
}

function ScrollBar({
  className,
  orientation = "vertical",
  ...props
}: ScrollAreaPrimitive.Scrollbar.Props & {
  orientation?: "vertical" | "horizontal"
}) {
  return (
    <ScrollAreaPrimitive.Scrollbar
      data-slot="scroll-area-scrollbar"
      orientation={orientation}
      className={cn(
        "flex touch-none rounded-full border bg-muted/60 p-px transition-colors select-none",
        orientation === "vertical" && "h-full w-2",
        orientation === "horizontal" && "h-2 flex-col",
        className
      )}
      {...props}
    >
      <ScrollAreaPrimitive.Thumb
        data-slot="scroll-area-thumb"
        className="relative flex-1 rounded-full bg-muted-foreground/40"
      />
    </ScrollAreaPrimitive.Scrollbar>
  )
}

export { ScrollArea, ScrollBar }
