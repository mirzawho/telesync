import { Tabs as TabsPrimitive } from "@base-ui/react/tabs"
import * as React from "react"

import { cn } from "@workspace/ui/lib/utils"

const Tabs = TabsPrimitive.Root

function TabsList({
  className,
  ...props
}: TabsPrimitive.List.Props & { className?: string }) {
  return (
    <TabsPrimitive.List
      data-slot="tabs-list"
      className={cn(
        // A list wider than its container scrolls sideways instead of wrapping;
        // it starts at the leading edge so the first tab is the one shown.
        "inline-flex h-9 items-center justify-start gap-1 overflow-x-auto overflow-y-hidden rounded-lg bg-muted p-1 text-muted-foreground",
        className,
      )}
      {...props}
    />
  )
}

function TabsTrigger({
  className,
  ...props
}: TabsPrimitive.Tab.Props & { className?: string }) {
  return (
    <TabsPrimitive.Tab
      data-slot="tabs-trigger"
      className={cn(
        "inline-flex shrink-0 items-center justify-center whitespace-nowrap rounded-md px-3 py-1 text-sm font-medium transition-all",
        "text-muted-foreground",
        // Base UI's Tab marks the selected tab with a bare `data-active`
        // attribute, not Radix's `data-state="active"`. The `data-active`
        // variant comes from shadcn/tailwind.css, imported by globals.css.
        "data-active:bg-background data-active:text-foreground data-active:shadow-sm",
        // On a dark page `--background` sits below the muted list, so the pill
        // would read as a hole instead of a raised surface.
        "dark:data-active:bg-input/30",
        "outline-none focus-visible:ring-3 focus-visible:ring-ring/50",
        // Base UI keeps a disabled tab focusable and marks it `data-disabled`
        // instead of setting the native attribute, so `disabled:` never fires.
        "data-disabled:pointer-events-none data-disabled:opacity-50",
        className,
      )}
      {...props}
    />
  )
}

function TabsContent({
  className,
  ...props
}: TabsPrimitive.Panel.Props & { className?: string }) {
  return (
    <TabsPrimitive.Panel
      data-slot="tabs-content"
      className={cn("mt-3 focus-visible:outline-none", className)}
      {...props}
    />
  )
}

export { Tabs, TabsList, TabsTrigger, TabsContent }
