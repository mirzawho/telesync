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
import { useToast } from "@workspace/ui/components/toast"

import { PasswordInput } from "@/components/password-input"
import { SubmitButton } from "@/components/submit-button"
import { apiRequest, errorMessage, type ApiError } from "@/lib/api"
import { getToken } from "@/lib/session"

// The one response the password route returns, unpacked as the API sends it.
export interface ChangePasswordResult {
  access_token: string
  token_type: string
  expires_in: number
}

// ChangePasswordDialog posts the signed-in account's own password change to
// POST /auth/password. It never names another user: the route targets the
// bearer token's own account and the form sends no user id.
//
// Honest scope: this changes the password only. Tokens the API issued before
// the change are NOT revoked — they are stateless HS256 tokens, so another
// signed-in session keeps working until its token expires. The dialog says so
// rather than pretending the old sessions end here.
export function ChangePasswordDialog({
  open,
  onOpenChange,
  onSuccess,
}: {
  open: boolean
  onOpenChange: (open: boolean) => void
  onSuccess?: (result: ChangePasswordResult) => void
}) {
  const [currentPassword, setCurrentPassword] = React.useState("")
  const [newPassword, setNewPassword] = React.useState("")
  const [confirmPassword, setConfirmPassword] = React.useState("")
  const [showCurrent, setShowCurrent] = React.useState(false)
  const [showNew, setShowNew] = React.useState(false)
  const [showConfirm, setShowConfirm] = React.useState(false)
  // attempted turns client validation on once the user tried to submit;
  // before that an empty field is not an error, it is just unfilled.
  const [attempted, setAttempted] = React.useState(false)
  const [submitting, setSubmitting] = React.useState(false)
  const [error, setError] = React.useState<string | null>(null)
  const { toast } = useToast()

  // A reopened dialog always starts empty: a password form must not keep the
  // previous attempt's secrets or messages around.
  React.useEffect(() => {
    if (!open) return
    setCurrentPassword("")
    setNewPassword("")
    setConfirmPassword("")
    setShowCurrent(false)
    setShowNew(false)
    setShowConfirm(false)
    setAttempted(false)
    setSubmitting(false)
    setError(null)
  }, [open])

  // channelstore.MinPasswordLen is 8 (internal/channelstore/users.go). The
  // form only asserts newPassword.length >= 8 so it fails before a round trip;
  // the API stays the authority on the policy.
  const currentError =
    attempted && currentPassword === "" ? "رمز عبور فعلی را وارد کنید." : null
  const newError =
    attempted && newPassword === ""
      ? "رمز عبور جدید را وارد کنید."
      : attempted && newPassword.length < 8
        ? "رمز عبور جدید باید دست‌کم ۸ نویسه باشد."
        : null
  // confirm_password is client-side only and never leaves the browser: it
  // only guards against a typo in the new password.
  const confirmError =
    attempted && confirmPassword === ""
      ? "تکرار رمز عبور جدید را وارد کنید."
      : attempted && confirmPassword !== newPassword
        ? "تکرار رمز عبور جدید با رمز عبور جدید یکسان نیست."
        : null

  const onSubmit = async (event: React.FormEvent<HTMLFormElement>) => {
    event.preventDefault()
    setAttempted(true)
    if (
      currentPassword === "" ||
      newPassword.length < 8 ||
      confirmPassword !== newPassword
    ) {
      return
    }
    setSubmitting(true)
    setError(null)
    try {
      const token = getToken()
      if (!token) {
        setError("نشست شما معتبر نیست. دوباره وارد شوید.")
        return
      }
      const parsed = await apiRequest<ChangePasswordResult>(
        "POST",
        "/auth/password",
        {
          token,
          // confirm_password is deliberately absent: the API accepts
          // current_password and new_password and nothing else.
          body: {
            current_password: currentPassword,
            new_password: newPassword,
          },
        }
      )
      onSuccess?.(parsed.data)
      // Success is a toast (the dialog closes before it can show a message);
      // validation and API failures stay inline so they are read in place.
      toast({
        variant: "success",
        title: "رمز عبور تغییر کرد.",
        description: "از ورود بعدی رمز تازه به کار می‌رود.",
      })
      onOpenChange(false)
    } catch (caught) {
      setError(errorMessage(caught, "تغییر رمز عبور انجام نشد."))
    } finally {
      setSubmitting(false)
    }
  }

  return (
    <Dialog
      open={open}
      onOpenChange={(next: boolean) =>
        !next && !submitting && onOpenChange(false)
      }
    >
      <DialogContent>
        <DialogHeader>
          <DialogTitle>تغییر رمز عبور</DialogTitle>
          <DialogDescription>
            رمز عبور تازه در ورود بعدی به کار می‌رود؛ نشست‌ها و توکن‌هایی که
            اکنون صادر شده‌اند باطل نمی‌شوند و تا پایان اعتبارشان همچنان معتبر
            می‌مانند.
          </DialogDescription>
        </DialogHeader>
        <form className="flex flex-col gap-4" noValidate onSubmit={onSubmit}>
          <PasswordInput
            id="current-password"
            name="current_password"
            label="رمز عبور فعلی"
            autoComplete="current-password"
            value={currentPassword}
            show={showCurrent}
            onToggleShow={() => setShowCurrent((value) => !value)}
            onChange={setCurrentPassword}
            error={currentError}
          />
          <PasswordInput
            id="new-password"
            name="new_password"
            label="رمز عبور جدید"
            autoComplete="new-password"
            value={newPassword}
            show={showNew}
            onToggleShow={() => setShowNew((value) => !value)}
            onChange={setNewPassword}
            error={newError}
            description="دست‌کم ۸ نویسه."
          />
          <PasswordInput
            id="confirm-password"
            name="confirm_password"
            label="تکرار رمز عبور جدید"
            autoComplete="new-password"
            value={confirmPassword}
            show={showConfirm}
            onToggleShow={() => setShowConfirm((value) => !value)}
            onChange={setConfirmPassword}
            error={confirmError}
            description="فقط برای بررسی یکسان بودن؛ به سریس فرستاده نمی‌شود."
          />
          {error && (
            <p
              role="alert"
              className="rounded-lg bg-destructive/10 px-3 py-2 text-sm text-destructive"
            >
              {error}
            </p>
          )}
          <DialogFooter>
            <Button
              type="button"
              variant="outline"
              onClick={() => onOpenChange(false)}
              disabled={submitting}
            >
              انصراف
            </Button>
            {/* Router navigation state (useNavigation / useFetcher) only tracks
                route form actions; this dialog's POST is its own apiRequest, so
                the pending flag here is local to that call. */}
            <SubmitButton pending={submitting} submittingLabel="در حال ذخیره…">
              ذخیره
            </SubmitButton>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  )
}
