import { Field as FieldPrimitive } from "@base-ui/react/field"

import { cn } from "@workspace/ui/lib/utils"

function Field({ className, ...props }: FieldPrimitive.Root.Props & { className?: string }) {
  return (
    <FieldPrimitive.Root
      data-slot="field"
      className={cn("group/field flex flex-col gap-2", className)}
      {...props}
    />
  )
}

function FieldLabel({ className, ...props }: FieldPrimitive.Label.Props & { className?: string }) {
  return (
    <FieldPrimitive.Label
      data-slot="field-label"
      className={cn("text-sm leading-none font-medium", className)}
      {...props}
    />
  )
}

const controlClass =
  "flex h-8 w-full min-w-0 rounded-lg border border-input bg-transparent px-2.5 py-1 text-sm transition-colors outline-none placeholder:text-muted-foreground focus-visible:border-ring focus-visible:ring-3 focus-visible:ring-ring/50 disabled:pointer-events-none disabled:cursor-not-allowed disabled:opacity-50 data-[invalid]:border-destructive data-[invalid]:ring-3 data-[invalid]:ring-destructive/20 dark:bg-input/30 dark:data-[invalid]:border-destructive/50 dark:data-[invalid]:ring-destructive/40"

function FieldControl({
  className,
  ...props
}: FieldPrimitive.Control.Props & { className?: string }) {
  return (
    <FieldPrimitive.Control
      data-slot="field-control"
      className={cn(controlClass, className)}
      {...props}
    />
  )
}

function FieldDescription({
  className,
  ...props
}: FieldPrimitive.Description.Props & { className?: string }) {
  return (
    <FieldPrimitive.Description
      data-slot="field-description"
      className={cn("text-xs text-muted-foreground", className)}
      {...props}
    />
  )
}

function FieldError({
  className,
  children,
  ...props
}: FieldPrimitive.Error.Props & { className?: string }) {
  return (
    <FieldPrimitive.Error
      data-slot="field-error"
      className={cn("text-xs font-medium text-destructive", className)}
      {...props}
    >
      {children}
    </FieldPrimitive.Error>
  )
}

export { Field, FieldControl, FieldDescription, FieldError, FieldLabel }
