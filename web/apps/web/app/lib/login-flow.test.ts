import assert from "node:assert/strict"
import { test } from "node:test"

import {
  authPhaseLabels,
  canResend,
  deliveryLabel,
  deliverySupported,
  emptyLoginDraft,
  isTerminalPhase,
  loginViewState,
  normalizeLoginAttempt,
  phaseOf,
  qrExpired,
  qrSecondsLeft,
  qrShouldRefresh,
  reduceLogin,
  resendCooldownSeconds,
  normalizeLoginCode,
  type LoginDraft,
  type LoginEvent,
} from "@/lib/login-flow"
import type { LoginAttempt, LoginMethod, TelegramAccount } from "@/lib/types"

const NOW = Date.parse("2026-11-12T10:00:00Z")
const ACCOUNT: TelegramAccount = {
  id: "acc_01hxyzabcdefghijklmnopqrstuv",
  server_key: "official",
  network_type: "telegram",
  remote_user_id: 42,
  remote_display_name: "میرزا",
  remote_username: "mirza",
  phone_number_masked: "+98 *** **45",
  status: "connecting",
}

function attempt(
  overrides: Partial<LoginAttempt> & {
    delivery_hint?: string
    code_length?: number
  } = {}
): LoginAttempt {
  return {
    id: "lga_01hxyzabcdefghijklmnopqrstuv",
    server_key: "official",
    method: "phone",
    state: "waiting_for_code",
    ...overrides,
  }
}

function run(
  events: LoginEvent[],
  from: LoginDraft = emptyLoginDraft
): LoginDraft {
  return events.reduce(reduceLogin, from)
}

// --- the phase map --------------------------------------------------------

test("every authentication state the product requires has a rendered phase", () => {
  const required = [
    "initializing",
    "waiting_for_code",
    "wrong_code",
    "expired_code",
    "resend_cooldown",
    "2fa_required",
    "wrong_2fa",
    "success",
    "already_connected",
    "canceled",
    "session_expired",
    "connection_unavailable",
    "flood_wait",
    "unsupported_delivery_method",
  ]
  for (const phase of required) {
    assert.equal(
      typeof (authPhaseLabels as Record<string, string>)[phase],
      "string",
      `${phase} has no label`
    )
  }
})

test("an attempt still connecting is initializing", () => {
  assert.equal(phaseOf(attempt({ state: "created" }), NOW), "initializing")
  assert.equal(phaseOf(attempt({ state: "connecting" }), NOW), "initializing")
  assert.equal(phaseOf(null, NOW), "initializing")
})

test("a code on its way is waiting for code, and says how it was sent", () => {
  const a = attempt({
    state: "waiting_for_code",
    code_delivery: { type: "sentCodeTypeApp" },
    resend_after: new Date(NOW - 1000).toISOString(),
  })
  assert.equal(phaseOf(a, NOW), "waiting_for_code")
  assert.equal(canResend(a, NOW), true)
})

test("a running resend timer is its own rendered state", () => {
  const a = attempt({
    state: "waiting_for_code",
    resend_after: new Date(NOW + 45_000).toISOString(),
  })
  assert.equal(phaseOf(a, NOW), "resend_cooldown")
  assert.equal(resendCooldownSeconds(a, NOW), 45)
  assert.equal(canResend(a, NOW), false)
  // The countdown is exact and never negative once it runs out.
  assert.equal(resendCooldownSeconds(a, NOW + 45_000), 0)
  assert.equal(resendCooldownSeconds(a, NOW + 90_000), 0)
})

test("the resend countdown is in whole seconds and rounds up a partial one", () => {
  const a = attempt({ resend_after: new Date(NOW + 1_400).toISOString() })
  assert.equal(resendCooldownSeconds(a, NOW), 2)
})

test("no server-supplied cooldown means no invented cooldown", () => {
  const a = attempt({ resend_after: null })
  assert.equal(resendCooldownSeconds(a, NOW), 0)
  assert.equal(canResend(a, NOW), true)
  const missing = attempt({})
  assert.equal(resendCooldownSeconds(missing, NOW), 0)
})

test("a wrong code is its own state and does not reset the attempt", () => {
  const a = attempt({
    state: "waiting_for_code",
    last_error_code: "login_code_invalid",
    resend_after: new Date(NOW - 1).toISOString(),
  })
  assert.equal(phaseOf(a, NOW), "wrong_code")
  // Still waiting for a code — the machine did not expire it over a typo.
  assert.equal(a.state, "waiting_for_code")
})

test("an expired code is told apart from a wrong one", () => {
  assert.equal(
    phaseOf(attempt({ last_error_code: "login_code_expired" }), NOW),
    "expired_code"
  )
  assert.equal(phaseOf(attempt({ state: "expired" }), NOW), "expired_code")
})

test("two-factor is asked for when the server asks, and a wrong one is its own state", () => {
  assert.equal(
    phaseOf(attempt({ state: "waiting_for_password" }), NOW),
    "2fa_required"
  )
  assert.equal(
    phaseOf(
      attempt({
        state: "waiting_for_password",
        last_error_code: "login_password_invalid",
      }),
      NOW
    ),
    "wrong_2fa"
  )
})

test("success and already-connected are told apart", () => {
  assert.equal(phaseOf(attempt({ state: "authorized" }), NOW), "success")
  assert.equal(
    phaseOf(
      attempt({ state: "authorized", next_step: "already_connected" }),
      NOW
    ),
    "already_connected"
  )
})

test("cancellation and an unusable attempt each land somewhere honest", () => {
  assert.equal(phaseOf(attempt({ state: "canceled" }), NOW), "canceled")
  assert.equal(
    phaseOf(attempt({ last_error_code: "login_attempt_invalid" }), NOW),
    "canceled"
  )
})

test("a rate limit is a wait, not a failure the reader caused", () => {
  assert.equal(
    phaseOf(attempt({ last_error_code: "login_rate_limited" }), NOW),
    "flood_wait"
  )
  assert.equal(
    phaseOf(attempt({ last_error_code: "rate_limited" }), NOW),
    "flood_wait"
  )
})

test("an unsupported delivery method is refused instead of left spinning", () => {
  assert.equal(
    phaseOf(attempt({ last_error_code: "unsupported_login_method" }), NOW),
    "unsupported_delivery_method"
  )
  assert.equal(
    phaseOf(attempt({ last_error_code: "unsupported_login_method" }), NOW),
    "unsupported_delivery_method"
  )
})

test("an unreachable connection is reported as unreachable, never as a wrong code", () => {
  for (const code of [
    "account_not_connected",
    "worker_unavailable",
    "not_connected",
    "worker_timeout",
    "session_encryption_unavailable",
    "network_error",
  ]) {
    assert.equal(
      phaseOf(attempt({ last_error_code: code }), NOW),
      "connection_unavailable",
      code
    )
  }
})

test("an unknown failure is reported as unreachable rather than as nothing", () => {
  assert.equal(
    phaseOf(attempt({ state: "failed", last_error_code: "???" }), NOW),
    "connection_unavailable"
  )
})

test("a failure code outranks the stored state it left behind", () => {
  // A rejected code leaves the attempt in waiting_for_code; the reader must be
  // told why, not shown a plain "waiting" screen again.
  assert.equal(
    phaseOf(
      attempt({
        state: "waiting_for_code",
        last_error_code: "login_code_invalid",
      }),
      NOW
    ),
    "wrong_code"
  )
})

test("an unsupported method outranks a rate limit, because waiting will not help", () => {
  // Only one of the two can be shown; the remedy differs, so the refusal wins.
  const a = attempt({
    state: "waiting_for_code",
    last_error_code: "unsupported_login_method",
    resend_after: new Date(NOW + 10_000).toISOString(),
  })
  assert.equal(phaseOf(a, NOW), "unsupported_delivery_method")
})

test("session expiry is told apart from an expired code and is genuinely reachable", () => {
  // A window closing on the reader — the attempt's TTL ran out, or the panel
  // session died underneath them — is not a code that went stale. Both send the
  // reader back to the start, but only one of them offers a fresh code.
  assert.equal(
    phaseOf(attempt({ last_error_code: "login_attempt_expired" }), NOW),
    "session_expired"
  )
  assert.equal(
    phaseOf(attempt({ last_error_code: "unauthorized" }), NOW),
    "session_expired"
  )
  assert.equal(
    phaseOf(attempt({ last_error_code: "login_code_expired" }), NOW),
    "expired_code"
  )
  assert.notEqual(authPhaseLabels.session_expired, authPhaseLabels.expired_code)
  assert.notEqual(
    authPhaseLabels.session_expired,
    authPhaseLabels.connection_unavailable
  )
})

// --- QR -------------------------------------------------------------------

test("the QR flow reports waiting, refreshing and expired as its own states", () => {
  const live = attempt({
    method: "qr",
    state: "waiting_for_qr",
    qr: { token: "AgAD", expires_at: new Date(NOW + 60_000).toISOString() },
  })
  assert.equal(phaseOf(live, NOW), "qr_waiting")
  assert.equal(phaseOf(live, NOW, { qrRefreshing: true }), "qr_refreshing")

  const dead = attempt({
    method: "qr",
    state: "waiting_for_qr",
    qr: { token: "AgAD", expires_at: new Date(NOW - 1).toISOString() },
  })
  assert.equal(phaseOf(dead, NOW), "qr_expired")
})

test("a QR token with no lifetime is not expired by the panel", () => {
  const open = attempt({
    method: "qr",
    state: "waiting_for_qr",
    qr: { token: "AgAD" },
  })
  assert.equal(qrExpired(open, NOW), false)
  assert.equal(phaseOf(open, NOW), "qr_waiting")
  assert.equal(qrSecondsLeft(open, NOW), 0)
})

test("the QR countdown runs to zero and the refresh hint is honoured", () => {
  const a = attempt({
    method: "qr",
    state: "waiting_for_qr",
    qr: {
      token: "AgAD",
      expires_at: new Date(NOW + 30_000).toISOString(),
      refresh_after: new Date(NOW + 10_000).toISOString(),
    },
  })
  assert.equal(qrSecondsLeft(a, NOW), 30)
  assert.equal(qrShouldRefresh(a, NOW), false)
  assert.equal(qrShouldRefresh(a, NOW + 10_000), true)
})

// --- delivery method ------------------------------------------------------

test("the delivery method is reported as Telegram named it", () => {
  assert.equal(
    deliveryLabel({ type: "sentCodeTypeSms" }),
    "کد ورود با پیامک ارسال شد."
  )
  assert.match(deliveryLabel({ type: "sentCodeTypeApp" }), /داخلی/)
  assert.match(deliveryLabel({ type: "sentCodeTypeCall" }), /تماس/)
  assert.match(deliveryLabel({ type: "sentCodeTypeEmailCode" }), /ایمیل/)
})

test("the panel never claims SMS on its own", () => {
  // An unrecognised type is described as the method Telegram named; it is never
  // down to a guess about SMS.
  const unknown = deliveryLabel({ type: "sentCodeTypeSomethingNew" })
  assert.doesNotMatch(unknown, /پیامک/)
  assert.ok(unknown.length > 0)

  // No delivery record at all is a neutral sentence, still not a claim.
  const absent = deliveryLabel(undefined)
  assert.doesNotMatch(absent, /پیامک/)
})

test("an unsupported delivery method is refused rather than walked through", () => {
  assert.equal(deliverySupported({ type: "sentCodeTypeSms" }), true)
  assert.equal(deliverySupported({ type: "sentCodeTypeSomethingNew" }), false)
  // No report at all is not a refusal: nothing said the method is impossible.
  assert.equal(deliverySupported(undefined), true)
})

// --- the wizard draft -----------------------------------------------------

test("choosing a server advances directly to phone authentication", () => {
  const draft = run([
    { type: "select_server", serverKey: "official", methods: ["phone", "qr"] },
  ])
  assert.equal(draft.step, "authenticate")
  assert.equal(draft.serverKey, "official")
  assert.equal(draft.method, "phone")
  assert.equal(draft.attempt, null)
})

test("the selected server picks an advertised method without a method step", () => {
  const qr = run([
    { type: "select_server", serverKey: "qr-only", methods: ["qr"] },
  ])
  assert.equal(qr.method, "qr")
  assert.equal(qr.step, "authenticate")

  const phone = run([
    { type: "select_server", serverKey: "cat", methods: ["phone"] },
  ])
  assert.equal(phone.method, "phone")
  assert.equal(phone.step, "authenticate")
})

test("a 2FA password is cleared the moment it is submitted", () => {
  const draft = run([
    { type: "password_changed", password: "hunter2secret" },
    { type: "submit_password" },
  ])
  assert.equal(draft.password, "")
})

test("the 2FA password is cleared on submission even when it fails", () => {
  // The requirement is unconditional: never retain it after submission, and a
  // rejection is still a submission.
  const draft = run([
    { type: "password_changed", password: "hunter2secret" },
    { type: "submit_password" },
    { type: "attempt_failed", code: "login_password_invalid" },
  ])
  assert.equal(draft.password, "")
})

test("nothing in the reducer can put a submitted password back", () => {
  const after = run([
    { type: "password_changed", password: "hunter2secret" },
    { type: "submit_password" },
  ])
  // Every subsequent event leaves it empty.
  const events: LoginEvent[] = [
    {
      type: "attempt_updated",
      attempt: attempt({ state: "waiting_for_password" }),
    },
    { type: "attempt_failed", code: "login_password_invalid" },
    { type: "code_changed", code: "12345" },
    { type: "phone_changed", phone: "+989120000000" },
    { type: "request_resend" },
    { type: "qr_refreshed", attempt: attempt({ state: "waiting_for_qr" }) },
  ]
  let draft = after
  for (const event of events) {
    draft = reduceLogin(draft, event)
    assert.equal(draft.password, "", `password survived ${event.type}`)
  }
})

test("a typed code survives a wrong-code error so it can be corrected", () => {
  const draft = run([
    { type: "code_changed", code: "12345" },
    { type: "submit_code" },
    { type: "attempt_failed", code: "login_code_invalid" },
  ])
  assert.equal(draft.code, "12345")
})

test("a restart keeps the chosen server but drops what the reader mistyped", () => {
  const draft = run([
    { type: "select_server", serverKey: "official", methods: ["phone", "qr"] },
    { type: "phone_changed", phone: "+989120000000" },
    { type: "code_changed", code: "11111" },
    { type: "restart" },
  ])
  assert.equal(draft.serverKey, "official")
  assert.equal(draft.phone, "")
  assert.equal(draft.code, "")
  assert.equal(draft.step, "authenticate")
})

test("going back from the code step cancels its attempt but keeps the phone", () => {
  const draft = run([
    { type: "select_server", serverKey: "official", methods: ["phone"] },
    { type: "phone_changed", phone: "+989120000000" },
    { type: "code_changed", code: "12345" },
    { type: "password_changed", password: "temporary" },
    { type: "attempt_started", attempt: attempt() },
    { type: "back_to_phone" },
  ])
  assert.equal(draft.step, "authenticate")
  assert.equal(draft.serverKey, "official")
  assert.equal(draft.method, "phone")
  assert.equal(draft.phone, "+989120000000")
  assert.equal(draft.code, "")
  assert.equal(draft.password, "")
  assert.equal(draft.attempt, null)
})

test("closing the wizard leaves nothing behind", () => {
  const draft = run([
    { type: "select_server", serverKey: "official", methods: ["phone", "qr"] },
    { type: "phone_changed", phone: "+989120000000" },
    { type: "password_changed", password: "hunter2secret" },
    { type: "close" },
  ])
  assert.deepEqual(draft, emptyLoginDraft)
})

// --- the rendered view ----------------------------------------------------

test("the view state and the phase can never disagree", () => {
  const waiting = run([
    { type: "select_server", serverKey: "official", methods: ["phone", "qr"] },
    { type: "select_method", method: "phone" },
    {
      type: "attempt_started",
      attempt: attempt({
        state: "waiting_for_code",
        code_delivery: { type: "sentCodeTypeSms" },
        resend_after: new Date(NOW + 20_000).toISOString(),
      }),
    },
  ])
  const view = loginViewState(waiting, NOW)
  assert.equal(view.phase, "resend_cooldown")
  assert.equal(view.resendCooldownSeconds, 20)
  assert.equal(view.canResend, false)
  // A reader can still type the code while the resend timer runs.
  assert.equal(view.canSubmitCode, true)
  assert.equal(view.canCancel, true)
  assert.equal(view.terminal, false)
  assert.match(view.delivery ?? "", /پیامک/)
})

test("the 2FA step is submittable exactly when a password is asked for", () => {
  const needs2fa = run([
    { type: "select_server", serverKey: "official", methods: ["phone"] },
    { type: "select_method", method: "phone" },
    {
      type: "attempt_started",
      attempt: attempt({ state: "waiting_for_password" }),
    },
  ])
  const view = loginViewState(needs2fa, NOW)
  assert.equal(view.phase, "2fa_required")
  assert.equal(view.canSubmitPassword, true)
  assert.equal(view.canSubmitCode, false)

  const wrong = loginViewState(
    {
      ...needs2fa,
      attempt: attempt({
        state: "waiting_for_password",
        last_error_code: "login_password_invalid",
      }),
    },
    NOW
  )
  assert.equal(wrong.canSubmitPassword, true)
})

test("a finished login is terminal and offers no further step", () => {
  for (const [state, next] of [
    ["authorized", undefined],
    ["authorized", "already_connected"],
    ["canceled", undefined],
  ] as const) {
    const view = loginViewState(
      { ...emptyLoginDraft, attempt: attempt({ state, next_step: next }) },
      NOW,
      state === "authorized" ? ACCOUNT : null
    )
    assert.equal(view.terminal, true, `${state}/${next}`)
    assert.equal(view.canCancel, false)
    assert.equal(view.step, "done")
  }
})

test("before any attempt there is nothing to submit and nothing to cancel", () => {
  const view = loginViewState(emptyLoginDraft, NOW)
  assert.equal(view.phase, "initializing")
  assert.equal(view.canSubmitCode, false)
  assert.equal(view.canSubmitPassword, false)
  assert.equal(view.canCancel, false)
  assert.equal(view.step, "server")
})

test("the delivery line is only shown once a code is actually on its way", () => {
  const connecting = loginViewState(
    { ...emptyLoginDraft, attempt: attempt({ state: "connecting" }) },
    NOW
  )
  assert.equal(connecting.delivery, null)
})

test("an authorized attempt waits for its safe account record before success", () => {
  const authorized = attempt({ state: "authorized", account_id: ACCOUNT.id })
  const waiting = loginViewState(
    { ...emptyLoginDraft, attempt: authorized },
    NOW
  )
  assert.equal(waiting.phase, "finalizing")
  assert.equal(waiting.step, "authenticate")
  assert.equal(waiting.terminal, false)

  const completed = loginViewState(
    { ...emptyLoginDraft, attempt: authorized },
    NOW,
    ACCOUNT
  )
  assert.equal(completed.phase, "success")
  assert.equal(completed.step, "done")
  assert.equal(completed.terminal, true)
})

test("an already-connected result is also gated on the account record", () => {
  const authorized = attempt({
    state: "authorized",
    next_step: "already_connected",
    account_id: ACCOUNT.id,
  })
  assert.equal(
    loginViewState({ ...emptyLoginDraft, attempt: authorized }, NOW).phase,
    "finalizing"
  )
  assert.equal(
    loginViewState({ ...emptyLoginDraft, attempt: authorized }, NOW, ACCOUNT)
      .phase,
    "already_connected"
  )
})

test("delivery hint and code length are carried to the code view as plain data", () => {
  const a = attempt({
    state: "waiting_for_code",
    code_delivery: { type: "sentCodeTypeEmailCode" },
    delivery_hint: "m***@example.test",
    code_length: 6,
  })
  const view = loginViewState({ ...emptyLoginDraft, attempt: a }, NOW)
  assert.match(view.delivery ?? "", /ایمیل/)
  assert.equal(view.deliveryHint, "m***@example.test")
  assert.equal(view.codeLength, 6)

  const unsafe = attempt({
    state: "waiting_for_code",
    delivery_hint: "  m***@example.test\n<script>alert(1)</script>  ",
  })
  assert.equal(
    loginViewState({ ...emptyLoginDraft, attempt: unsafe }, NOW).deliveryHint,
    "m***@example.test<script>alert(1)</script>"
  )
})

test("the current API attempt fields normalize locally to the panel's login model", () => {
  const wire = {
    ...attempt(),
    phone_masked: "+98 *** **45",
    delivery: "app",
    delivery_hint: "Telegram",
    code_length: 5,
    error_code: "login_code_invalid",
  } as LoginAttempt
  const normalized = normalizeLoginAttempt(wire)
  assert.equal(normalized.phone_number_masked, "+98 *** **45")
  assert.deepEqual(normalized.code_delivery, {
    type: "sentCodeTypeApp",
    length: 5,
  })
  assert.equal(normalized.last_error_code, "login_code_invalid")

  const view = loginViewState({ ...emptyLoginDraft, attempt: normalized }, NOW)
  assert.match(view.delivery ?? "", /داخلی/)
  assert.equal(view.deliveryHint, "Telegram")
  assert.equal(view.codeLength, 5)
  assert.equal(view.phase, "wrong_code")
})

test("pasted codes keep numeric characters and normalize Persian and Arabic digits", () => {
  assert.equal(normalizeLoginCode("۱۲۳ ٤٥x6"), "123456")
  assert.equal(normalizeLoginCode("1234567", 5), "12345")
  assert.equal(normalizeLoginCode("۱۲x", 4), "12")
})

test("every phase has a Persian label", () => {
  for (const [phase, label] of Object.entries(authPhaseLabels)) {
    assert.ok(label.length > 0, phase)
    assert.doesNotMatch(label, /[A-Za-z]{3,}/, phase)
  }
})
