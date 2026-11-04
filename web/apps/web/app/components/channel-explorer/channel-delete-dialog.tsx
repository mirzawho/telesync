import * as React from "react"

import type { Channel } from "@/lib/types"
import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
} from "@workspace/ui/components/alert-dialog"
import { Checkbox } from "@workspace/ui/components/checkbox"

// Deleting a managed channel and leaving the Telegram channel are two separate
// effects, and the dialog keeps them separate.
//
// Unticked — the default, and the narrower of the two — the record leaves
// Telesync's list and the account stays a member of the channel. Ticked, the
// Telegram leave is attempted first and the record is removed only as far as
// that actually went. They are not one transaction and are never reported as
// one: a partial result is stated as a partial result.

export interface DeleteChannelOptions {
  leaveTelegram: boolean
}

export function ChannelDeleteDialog({
  open,
  channel,
  pending = false,
  error = null,
  onOpenChange,
  onConfirm,
}: {
  open: boolean
  channel: Channel
  pending?: boolean
  error?: string | null
  onOpenChange: (open: boolean) => void
  onConfirm: (options: DeleteChannelOptions) => void
}) {
  return (
    <AlertDialog open={open} onOpenChange={(next) => !pending && onOpenChange(next)}>
      <AlertDialogContent>
        <ChannelDeleteDialogContent
          open={open}
          channel={channel}
          pending={pending}
          error={error}
          onConfirm={onConfirm}
        />
      </AlertDialogContent>
    </AlertDialog>
  )
}

// ChannelDeleteDialogContent is the question itself, so it can be read and
// tested without a portal: what is being deleted, the optional Telegram leave,
// and the two answers. It owns the tick box, because "unchecked" is the
// default the reader is given and not a value a caller should be able to
// pre-arm by accident.
export function ChannelDeleteDialogContent({
  open,
  channel,
  pending = false,
  error = null,
  onConfirm,
}: {
  open: boolean
  channel: Channel
  pending?: boolean
  error?: string | null
  onConfirm: (options: DeleteChannelOptions) => void
}) {
  const [leaveTelegram, setLeaveTelegram] = React.useState(false)
  const checkboxId = React.useId()

  // Every opening starts from the narrow choice again: a ticked box left over
  // from the last time must not quietly widen the next deletion.
  React.useEffect(() => {
    if (open) setLeaveTelegram(false)
  }, [open])

  return (
    <>
      <AlertDialogHeader>
        <AlertDialogTitle>حذف کانال</AlertDialogTitle>
        <AlertDialogDescription>
          آیا از حذف این کانال از فهرست کانال‌های مدیریت‌شده اطمینان دارید؟
        </AlertDialogDescription>
      </AlertDialogHeader>

      <p className="text-sm text-muted-foreground">
        کانال «{channel.title}»
        {channel.username ? (
          <span dir="ltr">
            {" "}
            @{channel.username}
          </span>
        ) : null}
      </p>

      <div className="flex items-start gap-2.5">
        <Checkbox
          id={checkboxId}
          checked={leaveTelegram}
          onCheckedChange={(checked) => setLeaveTelegram(checked === true)}
          disabled={pending}
          className="mt-0.5"
        />
        <label
          htmlFor={checkboxId}
          className="cursor-pointer text-sm leading-6 select-none"
        >
          همچنین از کانال در تلگرام خارج شوم
        </label>
      </div>

      {error && (
        <p
          role="alert"
          className="bg-destructive/10 text-destructive rounded-lg px-3 py-2 text-sm"
        >
          {error}
        </p>
      )}

      <AlertDialogFooter>
        <AlertDialogCancel disabled={pending}>انصراف</AlertDialogCancel>
        <AlertDialogAction
          disabled={pending}
          onClick={() => onConfirm({ leaveTelegram })}
        >
          {pending ? "در حال حذف…" : "حذف کانال"}
        </AlertDialogAction>
      </AlertDialogFooter>
    </>
  )
}
