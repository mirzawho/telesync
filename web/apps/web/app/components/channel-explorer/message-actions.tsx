import * as React from "react"
import { MoreHorizontalIcon, PencilIcon, Trash2Icon } from "lucide-react"

import { AlertDialog } from "@workspace/ui/components/alert-dialog"
import {
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
} from "@workspace/ui/components/alert-dialog"
import { Button } from "@workspace/ui/components/button"
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuTrigger,
} from "@workspace/ui/components/dropdown-menu"

export interface MessageActionsProps {
  canEdit: boolean
  canDelete: boolean
  deleting: boolean
  deleteError: string | null
  onEdit(): void
  onDelete(): void
}

export function MessageActions({
  canEdit,
  canDelete,
  deleting,
  deleteError,
  onEdit,
  onDelete,
}: MessageActionsProps) {
  const [confirmOpen, setConfirmOpen] = React.useState(false)

  if (!canEdit && !canDelete) return null

  return (
    <>
      <DropdownMenu>
        <DropdownMenuTrigger
          aria-label="عملیات پیام"
          title="عملیات پیام"
          render={
            <Button
              type="button"
              variant="ghost"
              size="icon"
              className="size-8"
            >
              <MoreHorizontalIcon aria-hidden="true" className="size-4" />
            </Button>
          }
        />
        <DropdownMenuContent align="end" className="min-w-36">
          {canEdit && (
            <DropdownMenuItem onClick={onEdit}>
              <PencilIcon aria-hidden="true" />
              ویرایش پیام
            </DropdownMenuItem>
          )}
          {canDelete && (
            <DropdownMenuItem
              variant="destructive"
              onClick={() => setConfirmOpen(true)}
            >
              <Trash2Icon aria-hidden="true" />
              حذف پیام
            </DropdownMenuItem>
          )}
        </DropdownMenuContent>
      </DropdownMenu>

      {canDelete && (
        <AlertDialog
          open={confirmOpen}
          onOpenChange={(open) => !deleting && setConfirmOpen(open)}
        >
          <AlertDialogContent>
            <MessageDeleteConfirmation
              deleting={deleting}
              deleteError={deleteError}
              onCancel={() => setConfirmOpen(false)}
              onDelete={onDelete}
            />
          </AlertDialogContent>
        </AlertDialog>
      )}
    </>
  )
}

// Kept separate so the confirmation copy and disabled/error states can be
// checked through server rendering without depending on a browser portal.
export function MessageDeleteConfirmation({
  deleting,
  deleteError,
  onCancel,
  onDelete,
}: Pick<MessageActionsProps, "deleting" | "deleteError" | "onDelete"> & {
  onCancel(): void
}) {
  return (
    <>
      <AlertDialogHeader>
        <AlertDialogTitle>حذف پیام</AlertDialogTitle>
        <AlertDialogDescription>
          آیا مطمئن هستید که می‌خواهید این پیام را حذف کنید؟ این عملیات ممکن است قابل بازگشت نباشد.
        </AlertDialogDescription>
      </AlertDialogHeader>
      {deleteError && (
        <p role="alert" className="rounded-lg bg-destructive/10 px-3 py-2 text-sm text-destructive">
          {deleteError}
        </p>
      )}
      <AlertDialogFooter>
        <AlertDialogCancel disabled={deleting} onClick={onCancel}>
          انصراف
        </AlertDialogCancel>
        <AlertDialogAction
          type="button"
          disabled={deleting}
          onClick={(event) => {
            event.preventDefault()
            if (!deleting) onDelete()
          }}
        >
          {deleting ? "در حال حذف…" : "حذف پیام"}
        </AlertDialogAction>
      </AlertDialogFooter>
    </>
  )
}
