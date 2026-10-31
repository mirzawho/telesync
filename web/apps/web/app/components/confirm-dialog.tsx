import * as React from "react"

import { Button } from "@workspace/ui/components/button"
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@workspace/ui/components/dialog"

// ConfirmDialog asks before a destructive action and reports the action's own
// error inside the dialog, so a refusal ("still referenced") is visible next to
// the thing that was refused.
export function ConfirmDialog({
  open,
  onOpenChange,
  title,
  description,
  confirmLabel = "تأیید",
  destructive = true,
  pending = false,
  error,
  onConfirm,
}: {
  open: boolean
  onOpenChange: (open: boolean) => void
  title: string
  description: React.ReactNode
  confirmLabel?: string
  destructive?: boolean
  pending?: boolean
  error?: string | null
  onConfirm: () => void
}) {
  return (
    <Dialog open={open} onOpenChange={(next) => !pending && onOpenChange(next)}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>{title}</DialogTitle>
          <DialogDescription>{description}</DialogDescription>
        </DialogHeader>
        {error && (
          <p
            role="alert"
            className="bg-destructive/10 text-destructive rounded-lg px-3 py-2 text-sm"
          >
            {error}
          </p>
        )}
        <DialogFooter>
          <Button
            variant="outline"
            onClick={() => onOpenChange(false)}
            disabled={pending}
          >
            انصراف
          </Button>
          <Button
            variant={destructive ? "destructive" : "default"}
            onClick={onConfirm}
            disabled={pending}
          >
            {pending ? "در حال انجام…" : confirmLabel}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}
