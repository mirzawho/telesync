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
        "inline-flex h-9 items-center justify-center gap-1 rounded-lg bg-muted p-1 text-muted-foreground",
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
        "inline-flex items-center justify-center whitespace-nowrap rounded-md px-3 py-1 text-sm font-medium transition-colors",
        "data-selected:bg-background data-selected:text-foreground data-selected:shadow-sm",
        "disabled:pointer-events-none disabled:opacity-50",
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
