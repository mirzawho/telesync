import {
  ArrowLeftIcon,
  CheckCircle2Icon,
  ChevronRightIcon,
  EyeIcon,
  EyeOffIcon,
  LoaderCircleIcon,
  QrCodeIcon,
  RefreshCwIcon,
  SmartphoneIcon,
  XIcon,
} from "lucide-react"
import * as React from "react"

import { faDigits } from "@/components/persian-digits"
import { SubmitButton } from "@/components/submit-button"
import { accountErrorText, accountErrorTitle } from "@/lib/account-errors"
import {
  cancelLogin,
  loginStatus,
  startLogin,
  submitLoginCode,
  submitLoginPassword,
} from "@/lib/account-api"
import {
  authPhaseLabels,
  canResend,
  deliveryLabel,
  emptyLoginDraft,
  loginViewState,
  normalizeLoginAttempt,
  normalizeLoginCode,
  qrSecondsLeft,
  qrShouldRefresh,
  resendCooldownSeconds,
  reduceLogin,
  type LoginDraft,
  type LoginEvent,
} from "@/lib/login-flow"
import { accountTitle, loginMethodsOf } from "@/lib/telegram-account"
import { listMyAccounts } from "@/lib/account-api"
import { ApiError } from "@/lib/api"
import type { LoginAttempt, LoginMethod, TelegramAccount } from "@/lib/types"
import { Button } from "@workspace/ui/components/button"
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogHeader,
  DialogTitle,
} from "@workspace/ui/components/dialog"
import {
  Field,
  FieldControl,
  FieldDescription,
  FieldError,
  FieldLabel,
} from "@workspace/ui/components/field"
import { cn } from "@workspace/ui/lib/utils"

// One compact account-login dialog. Picking a server opens the phone form
// immediately; each later view is driven by the persisted API attempt.

export interface WizardServer {
  key: string
  type?: string
  network_type?: TelegramAccount["network_type"]
  login_methods?: LoginMethod[]
}

function wizardServerLabel(server: WizardServer): string {
  const networkType = server.network_type ?? server.type
  if (networkType === "telegram") return "تلگرام رسمی"
  if (networkType === "custom") return "سرور اختصاصی"
  return "سرور"
}

export const ACCOUNT_LOGIN_DIALOG_CLASS =
  "w-[calc(100%-2rem)] max-h-[min(90dvh,42rem)] min-w-0 overflow-x-hidden overflow-y-auto rounded-2xl p-4 gap-5 sm:w-[min(100%-2rem,440px)] sm:max-w-[440px] sm:p-6 motion-reduce:animate-none motion-reduce:transition-none"

export function AddAccountWizard({
  open,
  onOpenChange,
  servers,
  token,
  onConnected,
  now = () => Date.now(),
}: {
  open: boolean
  onOpenChange: (open: boolean) => void
  servers: WizardServer[]
  token: string
  onConnected: (account: TelegramAccount) => void
  now?: () => number
}) {
  const [draft, setDraft] = React.useState<LoginDraft>(emptyLoginDraft)
  const [completedAccount, setCompletedAccount] =
    React.useState<TelegramAccount | null>(null)
  const [busy, setBusy] = React.useState(false)
  const [errorCode, setErrorCode] = React.useState<string | null>(null)
  const dispatch = React.useCallback(
    (event: LoginEvent) => setDraft((current) => reduceLogin(current, event)),
    []
  )

  React.useEffect(() => {
    if (open) return
    const closedView = loginViewState(draft, now(), completedAccount)
    if (draft.attempt && closedView.canCancel) {
      void cancelLogin(token, draft.attempt.id).catch(() => undefined)
    }
    setDraft(emptyLoginDraft)
    setCompletedAccount(null)
    setErrorCode(null)
    setBusy(false)
  }, [open])

  const attemptID = draft.attempt?.id ?? null
  const view = loginViewState(draft, now(), completedAccount)
  const selectedServer =
    servers.find((server) => server.key === draft.serverKey) ?? null
  const selectedServerName = selectedServer
    ? wizardServerLabel(selectedServer)
    : ""
  const dialogTitle =
    view.step === "server"
      ? "افزودن حساب تلگرام"
      : view.step === "done"
        ? view.phase === "success" || view.phase === "already_connected"
          ? "حساب اضافه شد"
          : "ورود انجام نشد"
        : view.step === "authenticate" &&
            !draft.attempt &&
            (draft.method === "phone" || draft.method === "qr")
          ? "ورود به حساب"
          : view.phase === "2fa_required" || view.phase === "wrong_2fa"
            ? "رمز دومرحله‌ای"
            : view.phase === "waiting_for_code" ||
                view.phase === "resend_cooldown" ||
                view.phase === "wrong_code" ||
                view.phase === "expired_code" ||
                view.phase === "flood_wait" ||
                view.phase === "unsupported_delivery_method" ||
                view.phase === "session_expired" ||
                view.phase === "connection_unavailable" ||
                view.phase === "canceled"
              ? "کد تأیید"
              : "ورود به حساب"
  const dialogDescription =
    view.step === "server"
      ? "سرور حساب تلگرام خود را انتخاب کنید."
      : view.step === "authenticate" &&
          !draft.attempt &&
          draft.method === "phone"
        ? "شماره تلفن حساب خود را وارد کنید تا کد ورود برایتان ارسال شود."
        : view.step === "done"
          ? "وضعیت ورود حساب شما"
          : selectedServerName

  const fail = React.useCallback(
    (error: unknown) => {
      const code = error instanceof ApiError ? error.code : "network_error"
      setErrorCode(code)
      dispatch({ type: "attempt_failed", code })
      setBusy(false)
    },
    [dispatch]
  )

  const applyAttempt = React.useCallback(
    (response: LoginAttempt, account?: TelegramAccount) => {
      const attempt = normalizeLoginAttempt(response)
      dispatch({ type: "attempt_updated", attempt })
      setErrorCode(attempt.last_error_code || null)
      setBusy(false)
      if (
        attempt.state === "authorized" &&
        account &&
        (!attempt.account_id || attempt.account_id === account.id)
      ) {
        setCompletedAccount(account)
      }
    },
    [dispatch]
  )

  // Code/password submission returns the attempt first; the owner-scoped list
  // supplies the safe account model (including live Runner status). QR status
  // polling reaches the same lookup without guessing identity from a server key.
  React.useEffect(() => {
    const attempt = draft.attempt
    if (
      !open ||
      attempt?.state !== "authorized" ||
      !attempt.account_id ||
      completedAccount
    ) {
      return
    }
    let active = true
    let inFlight = false
    const resolveAccount = async () => {
      if (inFlight) return
      inFlight = true
      try {
        const { accounts } = await listMyAccounts(token)
        const account = accounts.find((item) => item.id === attempt.account_id)
        if (active && account) setCompletedAccount(account)
      } catch {
        // Keep the authorization in its honest finalizing state. Never surface
        // a raw transport/server message or infer an account by server key.
      } finally {
        inFlight = false
      }
    }
    void resolveAccount()
    const timer = window.setInterval(() => void resolveAccount(), 1200)
    return () => {
      active = false
      window.clearInterval(timer)
    }
  }, [
    open,
    draft.attempt?.id,
    draft.attempt?.state,
    draft.attempt?.account_id,
    completedAccount,
    token,
  ])

  // Start/status may report an intermediate `connecting` state. Poll it until
  // the server says what the reader can do next; never render that as success.
  React.useEffect(() => {
    const attempt = draft.attempt
    if (
      !open ||
      !attempt ||
      (attempt.state !== "created" && attempt.state !== "connecting")
    ) {
      return
    }
    let active = true
    let inFlight = false
    const pollStatus = async () => {
      if (inFlight) return
      inFlight = true
      try {
        const next = normalizeLoginAttempt(await loginStatus(token, attempt.id))
        if (active) applyAttempt(next)
      } catch (error) {
        if (active) fail(error)
      } finally {
        inFlight = false
      }
    }
    const timer = window.setInterval(() => void pollStatus(), 1000)
    return () => {
      active = false
      window.clearInterval(timer)
    }
  }, [open, draft.attempt?.id, draft.attempt?.state, token, applyAttempt, fail])

  // --- phone: ask for the code -------------------------------------------
  const requestCode = async (phone: string) => {
    setBusy(true)
    setErrorCode(null)
    dispatch({ type: "submit_phone" })
    try {
      const attempt = normalizeLoginAttempt(
        await startLogin(token, {
          server_key: draft.serverKey,
          method: draft.method,
          ...(draft.method === "phone" ? { phone_number: phone } : {}),
        })
      )
      dispatch({ type: "attempt_started", attempt })
      setErrorCode(attempt.last_error_code || null)
      setBusy(false)
    } catch (error) {
      fail(error)
    }
  }

  // --- code ---------------------------------------------------------------
  const sendCode = async (code: string) => {
    if (!attemptID) return
    setBusy(true)
    setErrorCode(null)
    dispatch({ type: "submit_code" })
    try {
      const result = await submitLoginCode(token, attemptID, code)
      applyAttempt(result.attempt, result.account)
    } catch (error) {
      fail(error)
    }
  }

  // --- 2FA ---------------------------------------------------------------
  // The password is passed straight through and the reducer clears the field
  // in the same act. It is not copied into this closure's state, not logged and
  // not retried.
  const sendPassword = async (password: string) => {
    if (!attemptID) return
    setBusy(true)
    setErrorCode(null)
    dispatch({ type: "submit_password" })
    try {
      const result = await submitLoginPassword(token, attemptID, password)
      applyAttempt(result.attempt, result.account)
    } catch (error) {
      fail(error)
    }
  }

  const resend = async () => {
    if (!attemptID) return
    setBusy(true)
    setErrorCode(null)
    dispatch({ type: "request_resend" })
    try {
      await cancelLogin(token, attemptID)
      dispatch({ type: "back_to_phone" })
      const attempt = normalizeLoginAttempt(
        await startLogin(token, {
          server_key: draft.serverKey,
          method: "phone",
          phone_number: draft.phone,
        })
      )
      dispatch({ type: "attempt_started", attempt })
      setErrorCode(attempt.last_error_code || null)
      setBusy(false)
    } catch (error) {
      fail(error)
    }
  }

  const backToPhone = async () => {
    if (attemptID && view.canCancel) {
      try {
        await cancelLogin(token, attemptID)
      } catch (error) {
        fail(error)
        return
      }
    }
    dispatch({ type: "back_to_phone" })
    setErrorCode(null)
    setBusy(false)
  }

  // refreshQr replaces an expired QR token. The old token is not shown again
  // while the new one is on its way — `qr_refresh_started` puts the panel into
  // its own state for exactly that gap.
  const refreshQr = async () => {
    setBusy(true)
    dispatch({ type: "qr_refresh_started" })
    try {
      if (attemptID) await cancelLogin(token, attemptID)
      const attempt = normalizeLoginAttempt(
        await startLogin(token, {
          server_key: draft.serverKey,
          method: "qr",
        })
      )
      dispatch({ type: "qr_refreshed", attempt })
      setErrorCode(attempt.last_error_code || null)
      setBusy(false)
    } catch (error) {
      fail(error)
    }
  }

  const cancel = async () => {
    if (completedAccount) onConnected(completedAccount)
    onOpenChange(false)
  }

  return (
    <Dialog open={open} onOpenChange={(next) => !next && void cancel()}>
      <DialogContent className={ACCOUNT_LOGIN_DIALOG_CLASS}>
        <DialogHeader>
          <DialogTitle className="text-lg font-semibold">
            {dialogTitle}
          </DialogTitle>
          {dialogDescription ? (
            <DialogDescription>{dialogDescription}</DialogDescription>
          ) : null}
        </DialogHeader>

        {errorCode &&
        !view.terminal &&
        ![
          "login_phone_invalid",
          "phone_invalid",
          "login_code_invalid",
          "login_code_expired",
          "login_password_invalid",
        ].includes(errorCode) ? (
          <p
            role="alert"
            className="min-w-0 rounded-lg bg-destructive/10 px-3 py-2 text-sm break-words text-destructive"
          >
            {accountErrorText(errorCode)}
          </p>
        ) : null}

        {view.step === "server" && (
          <ServerStep
            servers={servers}
            chosen={draft.serverKey}
            onChoose={(serverKey) =>
              dispatch({
                type: "select_server",
                serverKey,
                methods: loginMethodsOf(
                  servers.find((server) => server.key === serverKey) ?? {}
                ),
              })
            }
          />
        )}

        {view.step === "authenticate" && (
          <AuthenticateStep
            draft={draft}
            selectedServerName={selectedServerName}
            busy={busy}
            errorCode={errorCode}
            view={view}
            methods={loginMethodsOf(
              servers.find((server) => server.key === draft.serverKey) ?? {}
            )}
            onUseQr={() => dispatch({ type: "select_method", method: "qr" })}
            onBackToServer={() => dispatch({ type: "back_to_server" })}
            onStartAttempt={(phone) => void requestCode(phone)}
            onCode={(code) => void sendCode(code)}
            onPassword={(password) => void sendPassword(password)}
            onResend={() => void resend()}
            onBackToPhone={() => void backToPhone()}
            onPhoneChanged={(phone) =>
              dispatch({ type: "phone_changed", phone })
            }
            onCodeChanged={(code) => dispatch({ type: "code_changed", code })}
            onPasswordChanged={(password) =>
              dispatch({ type: "password_changed", password })
            }
            poll={async () => {
              if (!attemptID) throw new Error("no attempt")
              return loginStatus(token, attemptID)
            }}
            onPoll={(attempt) => applyAttempt(attempt)}
            onQrRefresh={() => void refreshQr()}
            now={now}
          />
        )}

        {view.step === "done" && (
          <DoneStep
            phase={view.phase}
            errorCode={errorCode}
            account={completedAccount}
            onConnected={() => void cancel()}
            onRestart={() => dispatch({ type: "restart" })}
          />
        )}
      </DialogContent>
    </Dialog>
  )
}

export function ServerStep({
  servers,
  chosen,
  onChoose,
}: {
  servers: WizardServer[]
  chosen: string
  onChoose: (serverKey: string) => void
}) {
  return (
    <div className="flex flex-col gap-2">
      {servers.length === 0 ? (
        <p
          role="alert"
          className="rounded-lg bg-destructive/10 px-3 py-2 text-sm text-destructive"
        >
          فهرست سرورها در دسترس نیست؛ بعداً دوباره تلاش کنید.
        </p>
      ) : (
        <ul
          className="flex flex-col gap-2"
          role="radiogroup"
          aria-label="سرور تلگرام"
        >
          {servers.map((server) => {
            const active = server.key === chosen
            return (
              <li key={server.key}>
                <button
                  type="button"
                  role="radio"
                  aria-checked={active}
                  dir="rtl"
                  onClick={() => onChoose(server.key)}
                  className={cn(
                    "flex h-14 w-full flex-nowrap items-center justify-between gap-3 rounded-xl border px-4 text-start transition-colors duration-150 hover:bg-muted/60 focus-visible:ring-2 focus-visible:ring-ring focus-visible:ring-offset-2 focus-visible:outline-none motion-reduce:transition-none",
                    active ? "border-primary bg-primary/5" : "border-border"
                  )}
                >
                  <SmartphoneIcon
                    className={cn(
                      "size-4 shrink-0",
                      active ? "text-primary" : "text-muted-foreground"
                    )}
                  />
                  <span className="min-w-0 flex-1 truncate text-sm font-medium whitespace-nowrap">
                    {wizardServerLabel(server)}
                  </span>
                  <ArrowLeftIcon className="size-4 shrink-0 text-muted-foreground" />
                </button>
              </li>
            )
          })}
        </ul>
      )}
    </div>
  )
}

export function AuthenticateStep({
  draft,
  selectedServerName,
  busy,
  errorCode,
  view,
  methods,
  onUseQr,
  onBackToServer,
  onStartAttempt,
  onCode,
  onPassword,
  onResend,
  onBackToPhone,
  onPhoneChanged,
  onCodeChanged,
  onPasswordChanged,
  poll,
  onPoll,
  onQrRefresh,
  now,
}: {
  draft: LoginDraft
  selectedServerName: string
  busy: boolean
  errorCode: string | null
  view: ReturnType<typeof loginViewState>
  methods: LoginMethod[]
  onUseQr: () => void
  onBackToServer: () => void
  onStartAttempt: (phone: string) => void
  onCode: (code: string) => void
  onPassword: (password: string) => void
  onResend: () => void
  onBackToPhone: () => void
  onPhoneChanged: (phone: string) => void
  onCodeChanged: (code: string) => void
  onPasswordChanged: (password: string) => void
  poll: () => Promise<LoginAttempt>
  onPoll: (attempt: LoginAttempt) => void
  onQrRefresh: () => void
  now: () => number
}) {
  if (draft.method === "phone" && !draft.attempt) {
    return (
      <div className="flex min-w-0 flex-col gap-4">
        <ServerContext serverName={selectedServerName} />
        {methods.includes("phone") ? (
          <PhoneForm
            phone={draft.phone}
            busy={busy}
            errorCode={errorCode}
            qrAvailable={methods.includes("qr")}
            onChange={onPhoneChanged}
            onSubmit={onStartAttempt}
            onUseQr={onUseQr}
            onBack={onBackToServer}
          />
        ) : (
          <div className="flex flex-col gap-3">
            <p role="alert" className="text-sm text-destructive">
              {accountErrorText("unsupported_login_method")}
            </p>
            <Button
              type="button"
              variant="ghost"
              size="sm"
              onClick={onBackToServer}
            >
              <ChevronRightIcon />
              تغییر سرور
            </Button>
          </div>
        )}
      </div>
    )
  }

  if (draft.method === "qr" && !draft.attempt) {
    return (
      <div className="flex min-w-0 flex-col gap-3">
        <ServerContext serverName={selectedServerName} />
        <QrStart busy={busy} onStart={() => onStartAttempt("")} />
        <Button
          type="button"
          variant="ghost"
          size="sm"
          onClick={onBackToServer}
        >
          <ChevronRightIcon />
          تغییر سرور
        </Button>
      </div>
    )
  }

  switch (view.phase) {
    case "initializing":
      return <PendingPanel label={authPhaseLabels.initializing} />
    case "finalizing":
      return <PendingPanel label={authPhaseLabels.finalizing} />
    case "qr_waiting":
    case "qr_refreshing":
    case "qr_expired":
      return (
        <QrPanel
          draft={draft}
          phase={view.phase}
          busy={busy}
          onRefresh={onQrRefresh}
          poll={poll}
          onPoll={onPoll}
          now={now}
        />
      )
    case "2fa_required":
    case "wrong_2fa":
      return (
        <div className="flex min-w-0 flex-col gap-4">
          <ServerContext serverName={selectedServerName} />
          <TwoFactorForm
            password={draft.password}
            busy={busy}
            wrong={view.phase === "wrong_2fa"}
            errorCode={errorCode}
            onChange={onPasswordChanged}
            onSubmit={onPassword}
            onBack={view.canCancel ? onBackToPhone : undefined}
          />
        </div>
      )
    case "waiting_for_code":
    case "resend_cooldown":
    case "wrong_code":
    case "expired_code":
    case "flood_wait":
    case "unsupported_delivery_method":
    case "session_expired":
    case "connection_unavailable":
    case "canceled":
      return (
        <div className="flex min-w-0 flex-col gap-4">
          <ServerContext serverName={selectedServerName} />
          <CodePanel
            draft={draft}
            view={view}
            busy={busy}
            onCodeChanged={onCodeChanged}
            onSubmit={onCode}
            onResend={onResend}
            onBack={onBackToPhone}
            errorCode={errorCode}
          />
        </div>
      )
    default:
      return <PendingPanel label={authPhaseLabels.initializing} />
  }
}

function ServerContext({ serverName }: { serverName: string }) {
  return (
    <div className="flex min-w-0 items-center gap-2 text-sm text-muted-foreground">
      <SmartphoneIcon className="size-4 shrink-0" />
      <span className="min-w-0 truncate">{serverName}</span>
    </div>
  )
}

// PhoneForm is the default next view after choosing a server.
export function PhoneForm({
  phone,
  busy,
  errorCode,
  qrAvailable,
  onChange,
  onSubmit,
  onUseQr,
  onBack,
}: {
  phone: string
  busy: boolean
  errorCode?: string | null
  qrAvailable?: boolean
  onChange: (phone: string) => void
  onSubmit: (phone: string) => void
  onUseQr?: () => void
  onBack?: () => void
}) {
  const [touched, setTouched] = React.useState(false)
  const ref = React.useRef<HTMLInputElement>(null)
  React.useEffect(() => {
    ref.current?.focus()
  }, [])
  const invalid = touched && phone.trim() === ""
  const phoneError =
    errorCode === "login_phone_invalid" || errorCode === "phone_invalid"
      ? accountErrorText(errorCode)
      : null

  return (
    <form
      className="flex min-w-0 flex-col gap-4"
      noValidate
      onSubmit={(event) => {
        event.preventDefault()
        if (phone.trim() === "") {
          setTouched(true)
          ref.current?.focus()
          return
        }
        onSubmit(phone.trim())
      }}
    >
      <Field data-invalid={invalid || phoneError ? "" : undefined}>
        <FieldLabel required>شماره تلفن</FieldLabel>
        <FieldControl
          ref={ref}
          name="phone"
          autoFocus
          dir="ltr"
          type="tel"
          inputMode="tel"
          autoComplete="tel"
          placeholder="+98 912 000 0000"
          className="h-11"
          value={phone}
          aria-invalid={invalid || Boolean(phoneError)}
          aria-describedby={
            invalid || phoneError ? "login-phone-error" : "login-phone-help"
          }
          onChange={(event) => onChange(event.target.value)}
          onBlur={() => setTouched(true)}
          required
        />
        {phoneError ? (
          <FieldError id="login-phone-error">{phoneError}</FieldError>
        ) : invalid ? (
          <FieldError id="login-phone-error">
            شمارهٔ تلفن را وارد کنید.
          </FieldError>
        ) : (
          <FieldDescription id="login-phone-help">
            شماره با کد کشور وارد شود؛ کد ورود به همین شماره ارسال می‌شود.
          </FieldDescription>
        )}
      </Field>
      <div className="flex flex-col gap-2">
        <SubmitButton
          pending={busy}
          submittingLabel="در حال ارسال…"
          className="h-11 w-full"
        >
          دریافت کد ورود
        </SubmitButton>
        {qrAvailable && onUseQr ? (
          <Button
            type="button"
            variant="outline"
            className="h-10 w-full"
            disabled={busy}
            onClick={onUseQr}
          >
            ورود با کد تصویری
          </Button>
        ) : null}
        {onBack ? (
          <Button
            type="button"
            variant="ghost"
            className="h-10 w-full text-muted-foreground"
            disabled={busy}
            onClick={onBack}
          >
            <ChevronRightIcon />
            بازگشت
          </Button>
        ) : null}
      </div>
    </form>
  )
}

export function QrStart({
  busy,
  onStart,
}: {
  busy: boolean
  onStart: () => void
}) {
  React.useEffect(() => {
    if (!busy) onStart()
    // Starting is the only thing this step does; it is entered once.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [])
  return <PendingPanel label="در حال ساخت کد تصویری…" />
}

// PendingPanel communicates an API transition without claiming the next state
// before the backend reports it. The dialog footer remains the single cancel.
export function PendingPanel({ label }: { label: string }) {
  return (
    <div className="flex flex-col items-center gap-3 py-6">
      <LoaderCircleIcon className="size-6 animate-spin text-muted-foreground" />
      <p role="status" className="text-sm text-muted-foreground">
        {label}
      </p>
    </div>
  )
}

// QrCodeDisplay renders the token a reader's Telegram app scans.
//
// SEAM: this build has no QR encoder — the workspace has none and none can be
// added offline — so the code image is a placeholder and the token is shown
// beside it. Every state around the code (waiting, refreshing, expiry, success)
// is real and is driven by the login machine; only the picture is missing. A
// caller that has an encoder passes one in through `render` and nothing else
// changes.
export function QrCodeDisplay({
  token,
  render,
}: {
  token: string
  render?: (token: string) => React.ReactNode
}) {
  if (render) return <>{render(token)}</>
  return (
    <div
      data-slot="qr-code"
      className="flex flex-col items-center gap-2 rounded-lg border border-dashed p-4"
    >
      <QrCodeIcon className="size-10 text-muted-foreground" />
      <p className="text-center text-xs text-muted-foreground">
        کد تصویری در این نسخه ساخته نمی‌شود؛ این کد را در تلگرام وارد کنید.
      </p>
      <code
        dir="ltr"
        className="max-w-full rounded bg-muted px-2 py-1 font-mono text-[0.7rem] break-all"
      >
        {token}
      </code>
    </div>
  )
}

export function QrPanel({
  draft,
  phase,
  busy,
  onRefresh,
  poll,
  onPoll,
  now,
}: {
  draft: LoginDraft
  phase: ReturnType<typeof loginViewState>["phase"]
  busy: boolean
  onRefresh: () => void
  poll: () => Promise<LoginAttempt>
  onPoll: (attempt: LoginAttempt) => void
  now: () => number
}) {
  const attempt = draft.attempt
  const token = attempt?.qr?.token ?? ""

  // The server is the only one who knows whether the phone confirmed, so the
  // attempt is asked again while the code is on screen. The interval is tied to
  // the phase: it stops the moment the flow leaves "waiting".
  React.useEffect(() => {
    if (phase !== "qr_waiting" && phase !== "finalizing") return
    const handle = window.setInterval(() => {
      poll()
        .then(onPoll)
        .catch(() => undefined)
    }, 3000)
    return () => window.clearInterval(handle)
  }, [phase, poll, onPoll])

  const seconds = qrSecondsLeft(attempt, now())
  const stale = qrShouldRefresh(attempt, now())

  return (
    <div className="flex flex-col items-center gap-3">
      <QrCodeDisplay token={token} />
      {phase === "qr_expired" || stale ? (
        <p role="status" className="text-sm text-amber-700">
          کد تصویری منقضی شده است.
        </p>
      ) : (
        <p
          role="status"
          className="flex items-center gap-2 text-sm text-muted-foreground"
        >
          <LoaderCircleIcon className="size-4 animate-spin" />
          در انتظار تأیید روی تلگرام…
          {seconds > 0 ? ` (${faDigits(seconds)} ثانیه)` : ""}
        </p>
      )}
      <div className="flex flex-wrap items-center justify-center gap-2">
        <Button
          type="button"
          variant="outline"
          size="sm"
          disabled={busy}
          onClick={onRefresh}
        >
          <RefreshCwIcon />
          کد تازه
        </Button>
        <Button
          type="button"
          variant="ghost"
          size="sm"
          onClick={() =>
            void poll()
              .then(onPoll)
              .catch(() => undefined)
          }
        >
          بررسی وضعیت
        </Button>
      </div>
    </div>
  )
}

// CodePanel is the code step and every failure that leaves the reader at it.
// A rejected code keeps the field and the value: the remedy is to correct one
// digit, not to type the whole code again from a memory the reader does not
// have.
export function CodePanel({
  draft,
  view,
  busy,
  errorCode,
  onCodeChanged,
  onSubmit,
  onResend,
  onBack,
  now = () => Date.now(),
}: {
  draft: LoginDraft
  view: ReturnType<typeof loginViewState>
  busy: boolean
  errorCode?: string | null
  onCodeChanged: (code: string) => void
  onSubmit: (code: string) => void
  onResend: () => void
  onBack: () => void
  now?: () => number
}) {
  const [touched, setTouched] = React.useState(false)
  const [clock, setClock] = React.useState(() => now())
  const ref = React.useRef<HTMLInputElement>(null)
  const needsCode = view.canSubmitCode
  const codeLength = view.codeLength
  const cooldown = resendCooldownSeconds(draft.attempt, clock)
  const resendReady = canResend(draft.attempt, clock)

  React.useEffect(() => {
    if (needsCode) ref.current?.focus()
  }, [needsCode, view.phase])

  React.useEffect(() => {
    const attempt = draft.attempt
    if (!attempt?.resend_after || cooldown === 0) return
    const timer = window.setInterval(() => setClock(now()), 1000)
    return () => window.clearInterval(timer)
  }, [draft.attempt?.resend_after, cooldown])

  const invalid = touched && draft.code.trim() === ""
  const codeError =
    errorCode === "login_code_invalid" || errorCode === "login_code_expired"
      ? accountErrorText(errorCode)
      : null

  return (
    <form
      className="flex min-w-0 flex-col gap-4"
      noValidate
      onSubmit={(event) => {
        event.preventDefault()
        if (draft.code.trim() === "") {
          setTouched(true)
          ref.current?.focus()
          return
        }
        onSubmit(draft.code.trim())
      }}
    >
      <p className="min-w-0 text-sm break-words">
        {view.delivery ?? deliveryLabel(draft.attempt?.code_delivery)}
      </p>
      {view.deliveryHint && (
        <p className="min-w-0 text-xs break-words text-muted-foreground">
          <bdi dir="auto">{view.deliveryHint}</bdi>
        </p>
      )}
      {draft.attempt?.phone_number_masked && (
        <p className="text-xs text-muted-foreground">
          کد برای {draft.attempt.phone_number_masked} ارسال شد.
        </p>
      )}

      {needsCode ? (
        <Field data-invalid={invalid || Boolean(codeError) ? "" : undefined}>
          <FieldLabel required>کد تأیید</FieldLabel>
          <FieldControl
            ref={ref}
            name="code"
            autoFocus={needsCode}
            dir="ltr"
            inputMode="numeric"
            pattern="[0-9]*"
            autoComplete="one-time-code"
            placeholder={codeLength ? "•".repeat(codeLength) : "12345"}
            className="h-11 text-center text-lg tracking-[0.3em]"
            maxLength={codeLength ?? undefined}
            value={draft.code}
            aria-invalid={invalid || Boolean(codeError)}
            aria-describedby={
              invalid || codeError ? "login-code-error" : "login-code-help"
            }
            onChange={(event) =>
              onCodeChanged(
                normalizeLoginCode(event.target.value, codeLength ?? undefined)
              )
            }
            onBlur={() => setTouched(true)}
            required
          />
          {codeError ? (
            <FieldError id="login-code-error">{codeError}</FieldError>
          ) : invalid ? (
            <FieldError id="login-code-error">کد ورود را وارد کنید.</FieldError>
          ) : (
            <FieldDescription id="login-code-help">
              {codeLength
                ? `کد ${faDigits(codeLength)} رقمی را وارد کنید.`
                : "کد عددی‌ای را که دریافت کرده‌اید وارد کنید."}
            </FieldDescription>
          )}
        </Field>
      ) : null}

      {view.phase === "expired_code" && (
        <p className="text-sm text-muted-foreground">
          {accountErrorText("login_code_expired")} برای دریافت کد تازه، دوباره
          درخواست دهید.
        </p>
      )}

      <div className="flex flex-col gap-2">
        {needsCode && (
          <SubmitButton
            pending={busy}
            submittingLabel="در حال بررسی…"
            className="h-11 w-full"
          >
            تأیید کد
          </SubmitButton>
        )}
        <Button
          type="button"
          variant="outline"
          className="h-10 w-full"
          disabled={!resendReady || busy}
          onClick={onResend}
        >
          <RefreshCwIcon />
          {cooldown > 0
            ? `درخواست کد تازه (${faDigits(cooldown)})`
            : "درخواست کد تازه"}
        </Button>
        <Button
          type="button"
          variant="ghost"
          className="h-10 w-full text-muted-foreground"
          disabled={busy}
          onClick={onBack}
        >
          <ChevronRightIcon />
          بازگشت
        </Button>
      </div>
      <span className="sr-only" aria-live="polite">
        {view.title}
      </span>
    </form>
  )
}

// TwoFactorForm is the 2FA step.
//
// The value the reader types lives only in the wizard's reducer and is emptied
// by `submit_password` the moment it is sent: a rejection does not hand it back,
// and there is no state here to keep it in.
export function TwoFactorForm({
  password,
  busy,
  wrong,
  errorCode,
  onChange,
  onSubmit,
  onBack,
}: {
  password: string
  busy: boolean
  wrong: boolean
  errorCode?: string | null
  onChange: (password: string) => void
  onSubmit: (password: string) => void
  onBack?: () => void
}) {
  const [touched, setTouched] = React.useState(false)
  const [showPassword, setShowPassword] = React.useState(false)
  const ref = React.useRef<HTMLInputElement>(null)
  const error =
    wrong || errorCode === "login_password_invalid"
      ? accountErrorText("login_password_invalid")
      : touched && password === ""
        ? "رمز عبور را وارد کنید."
        : null
  React.useEffect(() => {
    ref.current?.focus()
  }, [wrong])

  return (
    <form
      className="flex flex-col gap-4"
      noValidate
      onSubmit={(event) => {
        event.preventDefault()
        if (password === "") {
          setTouched(true)
          ref.current?.focus()
          return
        }
        onSubmit(password)
      }}
    >
      <Field data-invalid={Boolean(error)}>
        <FieldLabel required>رمز دومرحله‌ای</FieldLabel>
        <div className="relative">
          <FieldControl
            ref={ref}
            name="password"
            dir="ltr"
            type={showPassword ? "text" : "password"}
            autoComplete="current-password"
            autoFocus
            className="h-11 pe-10"
            value={password}
            aria-invalid={Boolean(error)}
            aria-describedby={error ? "login-password-error" : undefined}
            onChange={(event) => onChange(event.target.value)}
            onBlur={() => setTouched(true)}
            required
          />
          <Button
            type="button"
            variant="ghost"
            size="icon-sm"
            className="absolute inset-y-0 end-1 my-auto"
            aria-label={showPassword ? "مخفی کردن رمز عبور" : "نمایش رمز عبور"}
            aria-pressed={showPassword}
            title={showPassword ? "مخفی کردن رمز عبور" : "نمایش رمز عبور"}
            onClick={() => setShowPassword((show) => !show)}
          >
            {showPassword ? (
              <EyeOffIcon className="size-4" />
            ) : (
              <EyeIcon className="size-4" />
            )}
          </Button>
        </div>
        {error ? (
          <FieldError id="login-password-error">{error}</FieldError>
        ) : null}
      </Field>
      <div className="flex flex-col gap-2">
        <SubmitButton
          pending={busy}
          submittingLabel="در حال بررسی…"
          className="h-11 w-full"
        >
          ورود به حساب
        </SubmitButton>
        {onBack ? (
          <Button
            type="button"
            variant="ghost"
            className="h-10 w-full text-muted-foreground"
            disabled={busy}
            onClick={onBack}
          >
            <ChevronRightIcon />
            بازگشت
          </Button>
        ) : null}
      </div>
    </form>
  )
}

export function DoneStep({
  phase,
  errorCode,
  account,
  onConnected,
  onRestart,
}: {
  phase: ReturnType<typeof loginViewState>["phase"]
  errorCode: string | null
  account: TelegramAccount | null
  onConnected: () => void
  onRestart: () => void
}) {
  const authorized = phase === "success" || phase === "already_connected"
  if (authorized && !account) {
    return <PendingPanel label={authPhaseLabels.finalizing} />
  }

  const ok = authorized && account !== null
  const runnerConnected = account?.status === "connected"
  const runnerConnecting =
    account?.status === "connecting" || account?.status === "reconnecting"

  return (
    <div className="flex flex-col items-center gap-4 py-2 text-center">
      {ok ? (
        <CheckCircle2Icon className="size-10 text-emerald-600" />
      ) : (
        <XIcon className="size-10 text-destructive" />
      )}
      <p className="text-sm font-medium">
        {ok
          ? phase === "already_connected"
            ? "این حساب از قبل متصل بود."
            : "ورود حساب تأیید شد."
          : accountErrorTitle(errorCode ?? "unknown")}
      </p>
      {ok && account ? (
        <div
          data-slot="account-login-success"
          className="flex w-full min-w-0 flex-col gap-2 rounded-xl bg-muted/50 p-4 text-start"
        >
          <p className="min-w-0 font-medium break-words">
            <bdi dir="auto">
              {account.remote_display_name || accountTitle(account)}
            </bdi>
          </p>
          <p className="text-xs text-muted-foreground">
            {wizardServerLabel({
              key: account.server_key,
              network_type: account.network_type,
            })}
          </p>
          <p className="text-xs text-muted-foreground">
            {runnerConnected
              ? "حساب شما با موفقیت آماده استفاده است."
              : runnerConnecting
                ? "ورود تأیید شد؛ حساب در حال آماده‌سازی است."
                : "ورود تأیید شد."}
          </p>
        </div>
      ) : (
        <p className="max-w-sm text-sm text-muted-foreground">
          {accountErrorText(errorCode ?? "unknown")}
        </p>
      )}
      <Button className="h-11 w-full" onClick={ok ? onConnected : onRestart}>
        {ok ? "بستن" : "تلاش دوباره"}
      </Button>
    </div>
  )
}
