// The add-account wizard's state, decided without a DOM.
//
// Two things live here and nothing else does: the mapping from the server's
// persisted login attempt onto what the screen says, and a reducer for the
// wizard's own draft. Both are pure so every auth state the product requires
// can be pinned by a test that never opens a browser — and so the one rule that
// must never be forgotten (a 2FA password is dropped the moment it is sent)
// holds in one place rather than in a handler someone can forget to call.
//
// The server's vocabulary is `LoginAttemptState` (`lib/types.ts`, contract §4).
// The wizard's vocabulary is `AuthPhase` below. The two are not the same set:
// the server says where the machine is, the phase says what a reader is looking
// at — "wrong code" and "expired code" are the same server state with two
// different sentences.

import type {
  CodeDelivery,
  LoginAttempt,
  LoginMethod,
  TelegramAccount,
} from "@/lib/types"

export type LoginStep = "server" | "authenticate" | "done"

// AuthPhase is every authentication state the product requires on screen.
export type AuthPhase =
  | "initializing"
  | "waiting_for_code"
  | "resend_cooldown"
  | "wrong_code"
  | "expired_code"
  | "2fa_required"
  | "wrong_2fa"
  | "finalizing"
  | "success"
  | "already_connected"
  | "canceled"
  | "session_expired"
  | "connection_unavailable"
  | "flood_wait"
  | "unsupported_delivery_method"
  | "qr_waiting"
  | "qr_refreshing"
  | "qr_expired"

export const authPhaseLabels: Record<AuthPhase, string> = {
  initializing: "در حال آماده‌سازی ورود…",
  waiting_for_code: "کد ارسال شد",
  resend_cooldown: "کد ارسال شد",
  wrong_code: "کد نادرست",
  expired_code: "کد منقضی شده",
  "2fa_required": "رمز عبور دومرحله‌ای",
  wrong_2fa: "رمز عبور نادرست",
  finalizing: "در حال آماده‌سازی حساب…",
  success: "حساب متصل شد",
  already_connected: "این حساب از قبل متصل است",
  canceled: "ورود لغو شد",
  session_expired: "نشست منقضی شده است",
  connection_unavailable: "ارتباط با سرور برقرار نیست",
  flood_wait: "محدودیت نرخ",
  unsupported_delivery_method: "این روش دریافت کد پشتیبانی نمی‌شود",
  qr_waiting: "در انتظار تأیید روی تلگرام",
  qr_refreshing: "در حال نوسازی کد",
  qr_expired: "کد تصویری منقضی شده",
}

// A phase the reader can only leave by starting over. Everything else has a
// next action of its own.
const TERMINAL_PHASES: ReadonlySet<AuthPhase> = new Set([
  "success",
  "already_connected",
  "canceled",
])

export function isTerminalPhase(phase: AuthPhase): boolean {
  return TERMINAL_PHASES.has(phase)
}

// Errors that mean "the code you typed was wrong", as opposed to "the code is
// no longer good". A panel that conflates them sends people to request a code
// they did not need.
const WRONG_CODE_ERRORS = new Set([
  "login_code_invalid",
  "code_invalid",
  "invalid_code",
])

const EXPIRED_CODE_ERRORS = new Set(["login_code_expired", "code_expired"])

const WRONG_PASSWORD_ERRORS = new Set([
  "login_password_invalid",
  "password_invalid",
  "two_factor_password_invalid",
])

const RATE_LIMIT_ERRORS = new Set(["login_rate_limited", "rate_limited"])

const UNAVAILABLE_ERRORS = new Set([
  "account_not_connected",
  "session_encryption_unavailable",
  "worker_unavailable",
  "not_connected",
  "worker_timeout",
  "unknown_client",
  "network_error",
])

const UNSUPPORTED_ERRORS = new Set(["unsupported_login_method"])

// "Session expired" covers the two ways a window can close on a reader while
// they are signing in: the login attempt's own TTL ran out, and the panel
// session they are signed into died underneath them. Both mean the same thing
// to a reader — start again — and neither is a code that went stale, which is
// `expired_code` and has a different remedy.
const SESSION_EXPIRED_ERRORS = new Set([
  "login_attempt_expired",
  "unauthorized",
  "auth_lost",
])

const INVALID_ATTEMPT_ERRORS = new Set(["login_attempt_invalid"])

// phaseOf is the one place a login attempt becomes a screen state.
//
// Precedence is deliberate: a failure code wins over the stored state, because
// a rejected code leaves the attempt sitting in `waiting_for_code` and the
// reader must be told *why* it is still there. Among failures, an unusable
// delivery method outranks a rate limit, because the remedy differs — waiting
// will not make an unsupported method arrive.
export function phaseOf(
  attempt: LoginAttempt | null | undefined,
  now: number = Date.now(),
  options: { qrRefreshing?: boolean } = {}
): AuthPhase {
  if (!attempt) return "initializing"

  const error = attempt.last_error_code ?? ""

  if (UNSUPPORTED_ERRORS.has(error)) return "unsupported_delivery_method"
  if (RATE_LIMIT_ERRORS.has(error)) return "flood_wait"
  if (WRONG_PASSWORD_ERRORS.has(error)) return "wrong_2fa"
  if (WRONG_CODE_ERRORS.has(error)) return "wrong_code"
  if (EXPIRED_CODE_ERRORS.has(error)) return "expired_code"
  if (SESSION_EXPIRED_ERRORS.has(error)) return "session_expired"
  if (INVALID_ATTEMPT_ERRORS.has(error)) return "canceled"
  if (UNAVAILABLE_ERRORS.has(error)) return "connection_unavailable"

  switch (attempt.state) {
    case "created":
    case "connecting":
      return "initializing"
    case "waiting_for_qr": {
      if (options.qrRefreshing) return "qr_refreshing"
      return qrExpired(attempt, now) ? "qr_expired" : "qr_waiting"
    }
    case "waiting_for_password":
      return "2fa_required"
    case "waiting_for_code":
      return resendCooldownSeconds(attempt, now) > 0
        ? "resend_cooldown"
        : "waiting_for_code"
    case "authorized":
      return attempt.next_step === "already_connected"
        ? "already_connected"
        : "success"
    case "canceled":
      return "canceled"
    case "expired":
      return "expired_code"
    case "failed":
      return "connection_unavailable"
    default:
      return "initializing"
  }
}

// qrExpired: the token's own lifetime ran out. A token the server never gave a
// lifetime for is treated as live — the panel does not expire what it cannot
// measure.
export function qrExpired(
  attempt: LoginAttempt | null | undefined,
  now: number = Date.now()
): boolean {
  const expiresAt = attempt?.qr?.expires_at
  if (!expiresAt) return false
  const at = Date.parse(expiresAt)
  return Number.isFinite(at) && at <= now
}

// qrSecondsLeft is the countdown beside a QR. Zero when the server gave no
// lifetime: an unknown lifetime renders no countdown rather than a fake one.
export function qrSecondsLeft(
  attempt: LoginAttempt | null | undefined,
  now: number = Date.now()
): number {
  const expiresAt = attempt?.qr?.expires_at
  if (!expiresAt) return 0
  const at = Date.parse(expiresAt)
  if (!Number.isFinite(at)) return 0
  return Math.max(0, Math.ceil((at - now) / 1000))
}

// qrShouldRefresh is when the wizard replaces a token rather than keep showing
// a dead one: the token's own refresh hint if it gave one, otherwise once it
// has expired.
export function qrShouldRefresh(
  attempt: LoginAttempt | null | undefined,
  now: number = Date.now()
): boolean {
  const refreshAfter = attempt?.qr?.refresh_after
  if (refreshAfter) {
    const at = Date.parse(refreshAfter)
    if (Number.isFinite(at)) return at <= now
  }
  return qrExpired(attempt, now)
}

// resendCooldownSeconds is how long before "send it again" may be pressed.
// Zero means ready now. A server that never named a time is ready immediately —
// the panel does not impose a cooldown Telegram did not ask for.
export function resendCooldownSeconds(
  attempt: LoginAttempt | null | undefined,
  now: number = Date.now()
): number {
  const after = attempt?.resend_after
  if (!after) return 0
  const at = Date.parse(after)
  if (!Number.isFinite(at)) return 0
  return Math.max(0, Math.ceil((at - now) / 1000))
}

export function canResend(
  attempt: LoginAttempt | null | undefined,
  now: number = Date.now()
): boolean {
  if (!attempt) return false
  if (
    attempt.state !== "waiting_for_code" &&
    attempt.state !== "waiting_for_qr"
  ) {
    return false
  }
  return resendCooldownSeconds(attempt, now) === 0
}

// deliveryLabel describes how the code actually arrived.
//
// The type is `auth.sentCode`'s own. A method this build does not recognise is
// reported as "the method Telegram named" with the raw type kept beside it in
// the record, never guessed at and never down to "SMS sent". This is the rule
// the contract states in as many words: the delivery method must be accurate.
export function deliveryLabel(delivery: CodeDelivery | undefined): string {
  if (!delivery || !delivery.type) {
    return "کد ورود ارسال شد."
  }
  switch (delivery.type) {
    case "sentCodeTypeSms":
    case "sms":
      return "کد ورود با پیامک ارسال شد."
    case "sentCodeTypeApp":
    case "sentCodeTypeAppTelegram":
    case "app":
      return "کد ورود در پیام‌رسان داخلی تلگرام ارسال شد."
    case "sentCodeTypeFragmentSms":
    case "fragment_sms":
      return "کد ورود با پیامک (سرویس فرگمنت) ارسال شد."
    case "sentCodeTypeCall":
    case "call":
      return "کد ورود با تماس تلفنی اعلام می‌شود."
    case "sentCodeTypeFlashCall":
    case "flash_call":
      return "کد ورود با تماس کوتاه اعلام می‌شود."
    case "sentCodeTypeEmailCode":
    case "email":
      return "کد ورود به نشانی ایمیل ثبت‌شده ارسال شد."
    default:
      return "کد ورود از روش اعلام‌شده توسط تلگرام ارسال شد."
  }
}

// deliverySupported is whether this panel can walk a reader through receiving
// the code by that method at all. An unsupported method is an honest refusal
// with a way out, never a spinner that never resolves.
const SUPPORTED_DELIVERY = new Set([
  "sentCodeTypeSms",
  "sentCodeTypeApp",
  "sentCodeTypeAppTelegram",
  "sentCodeTypeFragmentSms",
  "sentCodeTypeCall",
  "sentCodeTypeFlashCall",
  "sentCodeTypeEmailCode",
])

export function deliverySupported(delivery: CodeDelivery | undefined): boolean {
  if (!delivery || !delivery.type) return true
  return SUPPORTED_DELIVERY.has(delivery.type)
}

// The current gateway wire view uses a few names that differ from the shared
// panel type. Normalize them at this component boundary rather than changing
// account-api.ts or lib/types.ts from the dialog slice.
type LoginAttemptWire = LoginAttempt & {
  delivery?: string | CodeDelivery
  delivery_hint?: string
  code_length?: number
  phone_masked?: string
  error_code?: string
  has_qr?: boolean
  qr_expires_at?: string | null
}

const DELIVERY_TYPE: Readonly<Record<string, string>> = {
  app: "sentCodeTypeApp",
  sms: "sentCodeTypeSms",
  call: "sentCodeTypeCall",
  flash_call: "sentCodeTypeFlashCall",
  missed_call: "sentCodeTypeMissedCall",
  email: "sentCodeTypeEmailCode",
  fragment_sms: "sentCodeTypeFragmentSms",
  firebase_sms: "sentCodeTypeFirebaseSms",
  sms_word: "sentCodeTypeSmsWord",
  sms_phrase: "sentCodeTypeSmsPhrase",
}

export function normalizeLoginAttempt(attempt: LoginAttempt): LoginAttempt {
  const wire = attempt as LoginAttemptWire
  const rawDelivery = wire.delivery
  const delivery =
    wire.code_delivery ??
    (typeof rawDelivery === "string"
      ? {
          type: DELIVERY_TYPE[rawDelivery] ?? rawDelivery,
          ...(wire.code_length ? { length: wire.code_length } : {}),
        }
      : rawDelivery)

  return {
    ...wire,
    phone_number_masked: wire.phone_number_masked ?? wire.phone_masked,
    code_delivery: delivery,
    last_error_code: wire.last_error_code ?? wire.error_code,
  }
}

type LoginAttemptPresentation = LoginAttempt & {
  delivery_hint?: string
  code_length?: number
}

function deliveryHintOf(attempt: LoginAttempt | null): string | null {
  const hint = (attempt as LoginAttemptPresentation | null)?.delivery_hint
  if (!hint) return null
  const clean = hint
    .replace(/[\u0000-\u001f\u007f]/g, "")
    .trim()
    .slice(0, 120)
  return clean || null
}

function codeLengthOf(attempt: LoginAttempt | null): number | null {
  const presentation = attempt as LoginAttemptPresentation | null
  const length =
    presentation?.code_length ?? presentation?.code_delivery?.length
  return typeof length === "number" && Number.isInteger(length) && length > 0
    ? Math.min(length, 32)
    : null
}

// Telegram codes are digits. Normalize pasted Persian/Arabic digits and drop
// separators or accidental text without discarding the paste.
export function normalizeLoginCode(value: string, maxLength?: number): string {
  const normalized = value
    .replace(/[۰-۹]/g, (digit) => String("۰۱۲۳۴۵۶۷۸۹".indexOf(digit)))
    .replace(/[٠-٩]/g, (digit) => String("٠١٢٣٤٥٦٧٨٩".indexOf(digit)))
    .replace(/\D/g, "")
  return maxLength && maxLength > 0
    ? normalized.slice(0, maxLength)
    : normalized
}

// --- the wizard's own draft ------------------------------------------------

export interface LoginDraft {
  step: Exclude<LoginStep, "done">
  serverKey: string
  method: LoginMethod
  phone: string
  code: string
  // password is the 2FA secret. It is emptied by `submit_password` in the same
  // reducer step that sends it, so there is no path through this module that
  // keeps it after submission.
  password: string
  attempt: LoginAttempt | null
  errorCode: string | null
  qrRefreshing: boolean
}

export const emptyLoginDraft: LoginDraft = {
  step: "server",
  serverKey: "",
  method: "phone",
  phone: "",
  code: "",
  password: "",
  attempt: null,
  errorCode: null,
  qrRefreshing: false,
}

export type LoginEvent =
  | { type: "select_server"; serverKey: string; methods: LoginMethod[] }
  | { type: "select_method"; method: LoginMethod }
  | { type: "back_to_server" }
  | { type: "back_to_phone" }
  | { type: "phone_changed"; phone: string }
  | { type: "code_changed"; code: string }
  | { type: "password_changed"; password: string }
  | { type: "submit_phone" }
  | { type: "submit_code" }
  | { type: "submit_password" }
  | { type: "request_resend" }
  | { type: "attempt_started"; attempt: LoginAttempt }
  | { type: "attempt_updated"; attempt: LoginAttempt }
  | { type: "attempt_failed"; code: string }
  | { type: "qr_refresh_started" }
  | { type: "qr_refreshed"; attempt: LoginAttempt }
  | { type: "cancel_requested" }
  | { type: "canceled" }
  | { type: "restart" }
  | { type: "close" }

export function reduceLogin(draft: LoginDraft, event: LoginEvent): LoginDraft {
  switch (event.type) {
    case "select_server": {
      // Phone is the natural next step. QR is selected only when the configured
      // server explicitly has no phone capability.
      const method = event.methods.includes("phone")
        ? "phone"
        : event.methods.includes("qr")
          ? "qr"
          : "phone"
      return {
        ...emptyLoginDraft,
        serverKey: event.serverKey,
        method,
        step: "authenticate",
      }
    }
    case "select_method":
      return { ...draft, method: event.method, step: "authenticate" }
    case "back_to_server":
      return { ...emptyLoginDraft }
    case "back_to_phone":
      return {
        ...draft,
        step: "authenticate",
        method: "phone",
        code: "",
        password: "",
        attempt: null,
        errorCode: null,
        qrRefreshing: false,
      }
    case "phone_changed":
      return { ...draft, phone: event.phone }
    case "code_changed":
      return { ...draft, code: event.code }
    case "password_changed":
      return { ...draft, password: event.password }
    case "submit_phone":
      return { ...draft, step: "authenticate", errorCode: null }
    case "submit_code":
      // The code is kept through a rejection: it is not a secret and the reader
      // is going to correct it, not retype it from scratch.
      return { ...draft, errorCode: null }
    case "submit_password":
      // The one rule this reducer exists to hold: the 2FA password is gone the
      // instant it is sent. Not on success, not on failure — on send.
      return { ...draft, password: "", errorCode: null }
    case "request_resend":
      return { ...draft, errorCode: null }
    case "attempt_started":
      return { ...draft, attempt: event.attempt, errorCode: null }
    case "attempt_updated":
      // A fresh record supersedes any error it resolves.
      return {
        ...draft,
        attempt: event.attempt,
        errorCode: event.attempt.last_error_code || null,
      }
    case "attempt_failed":
      return { ...draft, errorCode: event.code }
    case "qr_refresh_started":
      // A token being replaced is its own state: the old one is dead and the
      // new one has not arrived, and showing either would be a lie.
      return { ...draft, qrRefreshing: true, errorCode: null }
    case "qr_refreshed":
      return {
        ...draft,
        attempt: event.attempt,
        qrRefreshing: false,
        errorCode: null,
      }
    case "cancel_requested":
      return { ...draft, qrRefreshing: false }
    case "canceled":
      return {
        ...draft,
        attempt: draft.attempt
          ? { ...draft.attempt, state: "canceled" }
          : draft.attempt,
        errorCode: null,
      }
    case "restart":
      return {
        ...emptyLoginDraft,
        // Keep the server, but return to the phone field without a stale attempt
        // or credential input.
        serverKey: draft.serverKey,
        method: "phone",
        step: "authenticate",
      }
    case "close":
      return { ...emptyLoginDraft }
    default:
      return draft
  }
}

// loginViewState is what the screen renders for a draft right now. One call,
// so the phase, the countdown and the enabled buttons can never disagree.
export interface LoginViewState {
  phase: AuthPhase
  step: LoginStep
  title: string
  delivery: string | null
  deliveryHint: string | null
  codeLength: number | null
  resendCooldownSeconds: number
  canResend: boolean
  canSubmitCode: boolean
  canSubmitPassword: boolean
  canCancel: boolean
  terminal: boolean
}

export function loginViewState(
  draft: LoginDraft,
  now: number = Date.now(),
  account: TelegramAccount | null = null
): LoginViewState {
  const attempt = draft.attempt
  let phase = phaseOf(attempt, now, { qrRefreshing: draft.qrRefreshing })
  if (attempt?.state === "authorized") {
    phase = account
      ? attempt.next_step === "already_connected"
        ? "already_connected"
        : "success"
      : "finalizing"
  }
  const cooldown = resendCooldownSeconds(attempt, now)
  return {
    phase,
    step: stepFor(phase, draft),
    title: authPhaseLabels[phase],
    delivery:
      attempt?.state === "waiting_for_code"
        ? deliveryLabel(attempt.code_delivery)
        : null,
    deliveryHint:
      attempt?.state === "waiting_for_code" ? deliveryHintOf(attempt) : null,
    codeLength:
      attempt?.state === "waiting_for_code" ? codeLengthOf(attempt) : null,
    resendCooldownSeconds: cooldown,
    canResend: canResend(attempt, now),
    canSubmitCode:
      phase === "waiting_for_code" ||
      phase === "resend_cooldown" ||
      phase === "wrong_code",
    canSubmitPassword: phase === "2fa_required" || phase === "wrong_2fa",
    canCancel: attempt !== null && !isTerminalPhase(phase),
    terminal: isTerminalPhase(phase),
  }
}

function stepFor(phase: AuthPhase, draft: LoginDraft): LoginStep {
  if (isTerminalPhase(phase)) return "done"
  if (phase === "finalizing") return "authenticate"
  return draft.step
}
