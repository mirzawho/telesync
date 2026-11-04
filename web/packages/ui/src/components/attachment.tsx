import * as React from "react"

import { cn } from "@workspace/ui/lib/utils"

// Attachment is the row that describes a downloadable file: a document, an
// archive, anything that is not something to look at or listen to.
//
// Pictures, audio and video are content and are drawn as content inside the
// bubble. Only a real file becomes an attachment, because only a real file
// needs a name, a size and a download.

function Attachment({ className, ...props }: React.ComponentProps<"div">) {
  return (
    <div
      data-slot="attachment"
      className={cn(
        "flex items-center gap-3 rounded-lg border bg-background/60 px-3 py-2",
        className
      )}
      {...props}
    />
  )
}

function AttachmentIcon({
  className,
  ...props
}: React.ComponentProps<"span">) {
  return (
    <span
      data-slot="attachment-icon"
      className={cn(
        "flex size-9 shrink-0 items-center justify-center rounded-md border bg-background text-muted-foreground [&_svg]:size-4",
        className
      )}
      {...props}
    />
  )
}

function AttachmentContent({ className, ...props }: React.ComponentProps<"div">) {
  return (
    <div
      data-slot="attachment-content"
      className={cn("flex min-w-0 flex-1 flex-col", className)}
      {...props}
    />
  )
}

function AttachmentName({ className, ...props }: React.ComponentProps<"p">) {
  return (
    <p
      data-slot="attachment-name"
      dir="auto"
      title={props.title}
      className={cn("truncate text-sm font-medium", className)}
      {...props}
    />
  )
}

function AttachmentDescription({
  className,
  ...props
}: React.ComponentProps<"p">) {
  return (
    <p
      data-slot="attachment-description"
      className={cn(
        "flex flex-wrap items-center gap-x-2 text-xs text-muted-foreground",
        className
      )}
      {...props}
    />
  )
}

function AttachmentAction({
  className,
  ...props
}: React.ComponentProps<"div">) {
  return (
    <div
      data-slot="attachment-action"
      className={cn("flex shrink-0 items-center gap-1", className)}
      {...props}
    />
  )
}

export {
  Attachment,
  AttachmentAction,
  AttachmentContent,
  AttachmentDescription,
  AttachmentIcon,
  AttachmentName,
}
