import { cva, type VariantProps } from "class-variance-authority"
import * as React from "react"

import { cn } from "@workspace/ui/lib/utils"

// Bubble is the neutral surface one message sits on: a calm grey rounded shape
// with no border and no shadow. It is deliberately not a Card — a wall of
// outlined cards is a data table in disguise.

const bubbleVariants = cva(
  "relative flex w-full flex-col gap-1.5 rounded-2xl px-3.5 py-2.5 text-sm leading-7",
  {
    variants: {
      variant: {
        muted: "bg-muted text-foreground",
        secondary: "bg-secondary text-secondary-foreground",
        outline: "border border-border bg-transparent",
      },
    },
    defaultVariants: {
      variant: "muted",
    },
  }
)

function Bubble({
  className,
  variant,
  ...props
}: React.ComponentProps<"div"> & VariantProps<typeof bubbleVariants>) {
  return (
    <div
      data-slot="bubble"
      data-variant={variant ?? "muted"}
      className={cn(bubbleVariants({ variant }), className)}
      {...props}
    />
  )
}

function BubbleContent({ className, ...props }: React.ComponentProps<"div">) {
  return (
    <div
      data-slot="bubble-content"
      className={cn(
        "flex min-w-0 flex-col gap-2 [overflow-wrap:anywhere] whitespace-pre-wrap",
        className
      )}
      {...props}
    />
  )
}

// BubbleReactions is the tally strip at a bubble's foot. The panel is
// `dir="rtl"`, so `justify-end` lands them on the physical LEFT, which is where
// the product wants them. They are a report of what other people did, never a
// control: there is no reaction editing in this panel.
function BubbleReactions({
  className,
  ...props
}: React.ComponentProps<"ul">) {
  return (
    <ul
      data-slot="bubble-reactions"
      className={cn(
        "flex flex-wrap items-center gap-1.5 pt-0.5",
        "justify-end",
        className
      )}
      {...props}
    />
  )
}

function BubbleReaction({
  chosen = false,
  className,
  ...props
}: React.ComponentProps<"li"> & { chosen?: boolean }) {
  return (
    <li
      data-slot="bubble-reaction"
      data-chosen={chosen ? "" : undefined}
      className={cn(
        "flex items-center gap-1 rounded-full bg-background/60 px-2 py-0.5 text-xs",
        "data-[chosen]:bg-primary/15 data-[chosen]:text-foreground",
        className
      )}
      {...props}
    />
  )
}

export {
  Bubble,
  BubbleContent,
  BubbleReaction,
  BubbleReactions,
  bubbleVariants,
}
