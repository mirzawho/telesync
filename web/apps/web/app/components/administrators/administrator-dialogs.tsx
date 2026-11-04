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
import {
  Field,
  FieldControl,
  FieldError,
  FieldLabel,
} from "@workspace/ui/components/field"
import { Label } from "@workspace/ui/components/label"
import { Switch } from "@workspace/ui/components/switch"

import { PasswordInput } from "@/components/password-input"
import { SubmitButton } from "@/components/submit-button"
import {
  accountStatusLabel,
  passwordError,
  type AdministratorFieldErrors,
  type CreateAdministratorValues,
  type UpdateAdministratorValues,
} from "@/lib/admin-users"
import type { User } from "@/lib/types"

// The three administrator dialogs. Each one is a small *Content component the
// test can render on its own, wrapped by the dialog that gives it a portal —
// the same split the channel delete dialog uses.
//
// What each one may send is fixed elsewhere: create is always active and has no
// status choice, the profile save carries no password at all, and the password
// dialog sends the password alone. See lib/admin-users.ts.

interface CommonProps {
  error?: string
  fieldErrors?: AdministratorFieldErrors
  pending: boolean
  onClose: () => void
}

// --- create ---

export function CreateAdministratorContent({
  error,
  fieldErrors,
  pending,
  defaultValues,
  onClose,
  onSubmit,
}: CommonProps & {
  defaultValues?: { fullname?: string; username?: string }
  onSubmit: (values: CreateAdministratorValues) => void
}) {
  const [username, setUsername] = React.useState(defaultValues?.username ?? "")
  const [fullname, setFullname] = React.useState(defaultValues?.fullname ?? "")
  const [password, setPassword] = React.useState("")
  const [touched, setTouched] = React.useState(false)

  const clientErrors: AdministratorFieldErrors = {}
  if (touched) {
    if (!username.trim()) clientErrors.username = "نام کاربری را وارد کنید."
    if (!fullname.trim()) clientErrors.fullname = "نام مستعار را وارد کنید."
    const passwordMessage = passwordError(password)
    if (passwordMessage) clientErrors.password = passwordMessage
  }
  const errors = { ...fieldErrors, ...clientErrors }

  return (
    <>
      <DialogHeader>
        <DialogTitle>افزودن مدیر</DialogTitle>
      </DialogHeader>
      <form
        className="flex flex-col gap-4"
        noValidate
        onSubmit={(event) => {
          event.preventDefault()
          setTouched(true)
          if (!username.trim() || !fullname.trim() || passwordError(password))
            return
          onSubmit({
            fullname: fullname.trim(),
            username: username.trim(),
            password,
          })
        }}
      >
        <Field data-invalid={errors.username ? "" : undefined}>
          <FieldLabel htmlFor="admin-username" required>
            نام کاربری
          </FieldLabel>
          <FieldControl
            id="admin-username"
            dir="ltr"
            autoComplete="username"
            value={username}
            onChange={(event) => setUsername(event.target.value)}
            onBlur={() => setTouched(true)}
            required
          />
          {errors.username && <FieldError>{errors.username}</FieldError>}
        </Field>
        <Field data-invalid={errors.fullname ? "" : undefined}>
          <FieldLabel htmlFor="admin-fullname" required>
            نام مستعار
          </FieldLabel>
          <FieldControl
            id="admin-fullname"
            autoComplete="nickname"
            value={fullname}
            onChange={(event) => setFullname(event.target.value)}
            onBlur={() => setTouched(true)}
            required
          />
          {errors.fullname && <FieldError>{errors.fullname}</FieldError>}
        </Field>
        <PasswordInput
          id="admin-password"
          name="password"
          label="رمز عبور"
          value={password}
          onChange={setPassword}
          onBlur={() => setTouched(true)}
          error={errors.password}
        />
        {error && <FormError>{error}</FormError>}
        <DialogFooter>
          <Button
            type="button"
            variant="outline"
            onClick={onClose}
            disabled={pending}
          >
            انصراف
          </Button>
          <SubmitButton pending={pending} submittingLabel="در حال افزودن...">
            افزودن مدیر
          </SubmitButton>
        </DialogFooter>
      </form>
    </>
  )
}

export function CreateAdministratorDialog({
  open,
  onOpenChange,
  ...props
}: CommonProps & {
  open: boolean
  onOpenChange: (open: boolean) => void
  defaultValues?: { fullname?: string; username?: string }
  onSubmit: (values: CreateAdministratorValues) => void
}) {
  return (
    <Dialog
      open={open}
      onOpenChange={(next) => !next && !props.pending && onOpenChange(false)}
    >
      <DialogContent>
        <CreateAdministratorContent {...props} />
      </DialogContent>
    </Dialog>
  )
}

// --- edit ---

export function EditAdministratorContent({
  user,
  self,
  error,
  fieldErrors,
  pending,
  defaultValues,
  onClose,
  onSubmit,
}: CommonProps & {
  user: User
  self: boolean
  defaultValues?: { fullname?: string; username?: string; is_active?: string }
  onSubmit: (values: UpdateAdministratorValues) => void
}) {
  const [username, setUsername] = React.useState(
    defaultValues?.username ?? user.username
  )
  const [fullname, setFullname] = React.useState(
    defaultValues?.fullname ?? user.fullname
  )
  const [isActive, setIsActive] = React.useState(
    defaultValues?.is_active
      ? defaultValues.is_active === "true"
      : user.is_active
  )
  const [touched, setTouched] = React.useState(false)

  const clientErrors: AdministratorFieldErrors = {}
  if (touched) {
    if (!username.trim()) clientErrors.username = "نام کاربری را وارد کنید."
    if (!fullname.trim()) clientErrors.fullname = "نام مستعار را وارد کنید."
  }
  const errors = { ...fieldErrors, ...clientErrors }

  return (
    <>
      <DialogHeader>
        <DialogTitle>ویرایش مدیر</DialogTitle>
      </DialogHeader>
      <form
        className="flex flex-col gap-4"
        noValidate
        onSubmit={(event) => {
          event.preventDefault()
          setTouched(true)
          if (!username.trim() || !fullname.trim()) return
          onSubmit({
            fullname: fullname.trim(),
            username: username.trim(),
            is_active: isActive,
          })
        }}
      >
        <Field data-invalid={errors.username ? "" : undefined}>
          <FieldLabel htmlFor="admin-username" required>
            نام کاربری
          </FieldLabel>
          <FieldControl
            id="admin-username"
            dir="ltr"
            autoComplete="username"
            value={username}
            onChange={(event) => setUsername(event.target.value)}
            onBlur={() => setTouched(true)}
            required
          />
          {errors.username && <FieldError>{errors.username}</FieldError>}
        </Field>
        <Field data-invalid={errors.fullname ? "" : undefined}>
          <FieldLabel htmlFor="admin-fullname" required>
            نام مستعار
          </FieldLabel>
          <FieldControl
            id="admin-fullname"
            autoComplete="nickname"
            value={fullname}
            onChange={(event) => setFullname(event.target.value)}
            onBlur={() => setTouched(true)}
            required
          />
          {errors.fullname && <FieldError>{errors.fullname}</FieldError>}
        </Field>
        <div className="flex flex-nowrap items-center justify-between gap-4 rounded-lg border px-3 py-2.5">
          <Label htmlFor="administrator-active" className="font-semibold">
            وضعیت حساب کاربری
          </Label>
          <div className="flex items-center gap-2">
            <span className="text-sm text-muted-foreground">
              {accountStatusLabel(isActive)}
            </span>
            <Switch
              id="administrator-active"
              checked={isActive}
              onCheckedChange={(checked) => setIsActive(checked)}
              disabled={self}
              title={
                self ? "نمی‌توانید حساب خودتان را غیرفعال کنید." : undefined
              }
            />
          </div>
        </div>
        {error && <FormError>{error}</FormError>}
        <DialogFooter>
          <Button
            type="button"
            variant="outline"
            onClick={onClose}
            disabled={pending}
          >
            انصراف
          </Button>
          <SubmitButton pending={pending} submittingLabel="در حال ذخیره...">
            ذخیره تغییرات
          </SubmitButton>
        </DialogFooter>
      </form>
    </>
  )
}

export function EditAdministratorDialog({
  open,
  onOpenChange,
  ...props
}: CommonProps & {
  open: boolean
  onOpenChange: (open: boolean) => void
  user: User
  self: boolean
  defaultValues?: { fullname?: string; username?: string; is_active?: string }
  onSubmit: (values: UpdateAdministratorValues) => void
}) {
  return (
    <Dialog
      open={open}
      onOpenChange={(next) => !next && !props.pending && onOpenChange(false)}
    >
      <DialogContent>
        <EditAdministratorContent {...props} />
      </DialogContent>
    </Dialog>
  )
}

// --- change password ---

// AdministratorPasswordContent is the whole password dialog: one field, no
// confirmation, and a name for the account it is about. It holds its own
// state, which dies with the component — closing the dialog drops the typed
// secret, and so does a successful save.
export function AdministratorPasswordContent({
  user,
  error,
  pending,
  onClose,
  onSubmit,
}: {
  user: User
  error?: string
  pending: boolean
  onClose: () => void
  onSubmit: (password: string) => void
}) {
  const [password, setPassword] = React.useState("")
  const [touched, setTouched] = React.useState(false)
  const passwordMessage = touched ? passwordError(password) : null

  return (
    <>
      <DialogHeader>
        <DialogTitle>تغییر رمز عبور</DialogTitle>
        <DialogDescription>
          <span dir="ltr">{user.username}</span>
        </DialogDescription>
      </DialogHeader>
      <form
        className="flex flex-col gap-4"
        noValidate
        onSubmit={(event) => {
          event.preventDefault()
          setTouched(true)
          if (passwordError(password)) return
          onSubmit(password)
        }}
      >
        <PasswordInput
          id="administrator-password"
          name="password"
          label="رمز عبور جدید"
          value={password}
          onChange={setPassword}
          onBlur={() => setTouched(true)}
          error={passwordMessage}
        />
        {error && <FormError>{error}</FormError>}
        <DialogFooter>
          <Button
            type="button"
            variant="outline"
            onClick={onClose}
            disabled={pending}
          >
            انصراف
          </Button>
          <SubmitButton pending={pending} submittingLabel="در حال تغییر رمز...">
            تغییر رمز عبور
          </SubmitButton>
        </DialogFooter>
      </form>
    </>
  )
}

export function ChangeAdministratorPasswordDialog({
  open,
  onOpenChange,
  user,
  error,
  pending,
  onClose,
  onSubmit,
}: {
  open: boolean
  onOpenChange: (open: boolean) => void
  user: User
  error?: string
  pending: boolean
  onClose: () => void
  onSubmit: (password: string) => void
}) {
  return (
    <Dialog
      open={open}
      onOpenChange={(next) => !next && !pending && onOpenChange(false)}
    >
      <DialogContent>
        <AdministratorPasswordContent
          user={user}
          error={error}
          pending={pending}
          onClose={onClose}
          onSubmit={onSubmit}
        />
      </DialogContent>
    </Dialog>
  )
}

function FormError({ children }: { children: React.ReactNode }) {
  return (
    <p
      role="alert"
      className="rounded-lg bg-destructive/10 px-3 py-2 text-sm text-destructive"
    >
      {children}
    </p>
  )
}
