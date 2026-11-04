import * as React from "react"

import { cn } from "@workspace/ui/lib/utils"

// Message, MessageContent and MessageFooter are the timeline row.
//
// The panel is `dir="rtl"`, so the logical sides map to the physical ones the
// product asks for: `align="end"` packs a row onto the physical LEFT, which is
// where every channel message sits. There is no sender side to alternate
// between — a channel viewer shows one stream of posts — so the alignment is a
// constant of the layout rather than a per-message property.

type MessageAlign = "start" | "end"

function Message({
  align = "end",
  className,
  ...props
}: React.ComponentProps<"div"> & { align?: MessageAlign }) {
  return (
    <div
      data-slot="message"
      data-align={align}
      className={cn(
        "flex w-full items-start gap-2",
        align === "end" ? "justify-end" : "justify-start",
        className
      )}
      {...props}
    />
  )
}

function MessageContent({ className, ...props }: React.ComponentProps<"div">) {
  return (
    <div
      data-slot="message-content"
      // fit-content: a bubble is as wide as what it holds, up to a readable
      // maximum, and a long line wraps instead of stretching the row.
      className={cn(
        "flex w-fit max-w-[min(100%,40rem)] flex-col items-end gap-1",
        className
      )}
      {...props}
    />
  )
}

function MessageFooter({ className, ...props }: React.ComponentProps<"div">) {
  return (
    <div
      data-slot="message-footer"
      className={cn(
        "flex items-center gap-1.5 px-1 text-xs text-muted-foreground",
        // `justify-end` is the physical left under RTL: the metadata sits under
        // the bubble's own leading edge rather than floating to its far side.
        "justify-end",
        className
      )}
      {...props}
    />
  )
}

export { Message, MessageContent, MessageFooter }
export type { MessageAlign }
