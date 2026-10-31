import { Dialog as DialogPrimitive } from "@base-ui/react/dialog"
import { XIcon } from "lucide-react"

import { Button } from "@workspace/ui/components/button"
import { cn } from "@workspace/ui/lib/utils"

const Sheet = DialogPrimitive.Root

function SheetTrigger(props: DialogPrimitive.Trigger.Props) {
  return <DialogPrimitive.Trigger data-slot="sheet-trigger" {...props} />
}

function SheetClose(props: DialogPrimitive.Close.Props) {
  return <DialogPrimitive.Close data-slot="sheet-close" {...props} />
}

function SheetContent({
  className,
  children,
  side = "end",
  ...props
}: DialogPrimitive.Popup.Props & {
  className?: string
  side?: "start" | "end"
}) {
  return (
    <DialogPrimitive.Portal>
      <DialogPrimitive.Backdrop
        data-slot="sheet-backdrop"
        className="fixed inset-0 z-50 bg-black/50 duration-200 data-open:animate-in data-open:fade-in-0 data-closed:animate-out data-closed:fade-out-0"
      />
      <DialogPrimitive.Popup
        data-slot="sheet-content"
        className={cn(
          "fixed inset-y-0 z-50 flex h-full w-3/4 max-w-sm flex-col gap-4 bg-background shadow-lg duration-200 data-open:animate-in data-closed:animate-out data-closed:duration-200",
          side === "end"
            ? "end-0 border-s data-open:slide-in-from-left data-closed:slide-out-to-left rtl:data-open:slide-in-from-right rtl:data-closed:slide-out-to-right"
            : "start-0 border-e data-open:slide-in-from-right data-closed:slide-out-to-right rtl:data-open:slide-in-from-left rtl:data-closed:slide-out-to-left",
          className
        )}
        {...props}
      >
        {children}
        <DialogPrimitive.Close
          data-slot="sheet-close-x"
          render={<Button variant="ghost" size="icon-sm" className="absolute top-3 end-3" />}
        >
          <XIcon />
          <span className="sr-only">بستن</span>
        </DialogPrimitive.Close>
      </DialogPrimitive.Popup>
    </DialogPrimitive.Portal>
  )
}

function SheetHeader({ className, ...props }: React.ComponentProps<"div">) {
  return (
    <div
      data-slot="sheet-header"
      className={cn("flex flex-col gap-1 p-4 pe-12", className)}
      {...props}
    />
  )
}

function SheetTitle({ className, ...props }: DialogPrimitive.Title.Props) {
  return (
    <DialogPrimitive.Title
      data-slot="sheet-title"
      className={cn("text-base font-medium", className)}
      {...props}
    />
  )
}

function SheetDescription({ className, ...props }: DialogPrimitive.Description.Props) {
  return (
    <DialogPrimitive.Description
      data-slot="sheet-description"
      className={cn("text-sm text-muted-foreground", className)}
      {...props}
    />
  )
}

export { Sheet, SheetClose, SheetContent, SheetDescription, SheetHeader, SheetTitle, SheetTrigger }
