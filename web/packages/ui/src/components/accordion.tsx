import { Accordion as AccordionPrimitive } from "@base-ui/react/accordion"
import * as React from "react"

import { cn } from "@workspace/ui/lib/utils"

// Accordion over Base UI, in the shape shadcn spells it — Accordion,
// AccordionItem, AccordionTrigger, AccordionContent — so a form says what it
// means and the primitive underneath stays this workspace's own.
//
// The panel keeps its children mounted while closed. An advanced section holds
// live form state, and a section that unmounted on collapse would throw away
// whatever was typed into it.

const Accordion = React.forwardRef<
  React.ComponentRef<typeof AccordionPrimitive.Root>,
  Omit<
    React.ComponentProps<typeof AccordionPrimitive.Root>,
    "multiple" | "defaultValue" | "value" | "onValueChange"
  > & {
    // shadcn spells the shape of an accordion; Base UI spells the mechanism.
    // These are the shadcn words, mapped onto it.
    type?: "single" | "multiple"
    collapsible?: boolean
    defaultValue?: string | string[] | null
    value?: string | string[] | null
    onValueChange?: (value: string | string[] | null) => void
  }
>(function Accordion(
  { type = "single", collapsible, defaultValue, value, onValueChange, ...props },
  ref
) {
  const multiple = type === "multiple"
  return (
    <AccordionPrimitive.Root
      ref={ref}
      multiple={multiple}
      defaultValue={(defaultValue ?? null) as never}
      value={value as never}
      onValueChange={onValueChange as never}
      {...props}
    />
  )
})
Accordion.displayName = "Accordion"

function AccordionItem({
  className,
  ...props
}: React.ComponentProps<typeof AccordionPrimitive.Item>) {
  return (
    <AccordionPrimitive.Item
      data-slot="accordion-item"
      className={cn("border-b last:border-b-0", className)}
      {...props}
    />
  )
}

function AccordionTrigger({
  className,
  children,
  ...props
}: React.ComponentProps<typeof AccordionPrimitive.Trigger>) {
  return (
    <AccordionPrimitive.Header className="flex">
      <AccordionPrimitive.Trigger
        data-slot="accordion-trigger"
        className={cn(
          "flex flex-1 items-center justify-between gap-3 py-4 text-start text-sm font-medium transition-colors outline-none",
          "hover:text-foreground focus-visible:ring-3 focus-visible:ring-ring/50",
          "data-disabled:pointer-events-none data-disabled:opacity-50",
          className
        )}
        {...props}
      >
        {children}
        {/* The chevron is the whole expanded/collapsed cue, so it turns with the
            panel rather than appearing and disappearing. */}
        <svg
          viewBox="0 0 20 20"
          fill="none"
          stroke="currentColor"
          strokeWidth="1.5"
          strokeLinecap="round"
          strokeLinejoin="round"
          aria-hidden="true"
          className="size-4 shrink-0 opacity-60 transition-transform duration-200 ease-out data-[panel-open]:rotate-180"
        >
          <path d="m5 7.5 5 5 5-5" />
        </svg>
      </AccordionPrimitive.Trigger>
    </AccordionPrimitive.Header>
  )
}

function AccordionContent({
  className,
  children,
  ...props
}: React.ComponentProps<typeof AccordionPrimitive.Panel>) {
  return (
    <AccordionPrimitive.Panel
      data-slot="accordion-content"
      keepMounted
      className={cn(
        // Base UI reports the panel's own height as `--accordion-panel-height`,
        // and a grid row sized by it is what turns opening and closing into a
        // transition rather than a jump. The fallback keeps a closed panel
        // collapsed before the first measurement.
        "grid overflow-hidden text-sm transition-[grid-template-rows] duration-200 ease-out",
        "grid-rows-[var(--accordion-panel-height,0px)]",
        className
      )}
      {...props}
    >
      <div className="min-h-0 overflow-hidden">
        <div className="pt-1 pb-4">{children}</div>
      </div>
    </AccordionPrimitive.Panel>
  )
}

export { Accordion, AccordionContent, AccordionItem, AccordionTrigger }
