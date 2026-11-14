import assert from "node:assert/strict"
import { test } from "node:test"
import { readFileSync } from "node:fs"

import { renderToStaticMarkup } from "react-dom/server"

import {
  ACCOUNT_DETAILS_DIALOG_CLASS,
  AccountDetailsContent,
  AccountList,
  AccountStatusBadge,
  NoAccountsState,
} from "@/components/accounts/account-list"
import { accountActionsFor } from "@/lib/telegram-account"
import {
  ACCOUNT_LOGIN_DIALOG_CLASS,
  AuthenticateStep,
  CodePanel,
  DoneStep,
  PhoneForm,
  QrCodeDisplay,
  QrPanel,
  ServerStep,
  TwoFactorForm,
  type WizardServer,
} from "@/components/accounts/account-login-wizard"
import { AccountOption } from "@/components/accounts/account-option"
import { AccountCombobox } from "@/components/accounts/account-combobox"
import { AccountMultiSelect } from "@/components/accounts/account-multi-select"
import {
  emptyLoginDraft,
  loginViewState,
  normalizeLoginAttempt,
  reduceLogin,
  type LoginDraft,
  type LoginEvent,
} from "@/lib/login-flow"
import type { LoginAttempt, TelegramAccount } from "@/lib/types"

const html = (node: React.ReactNode) => renderToStaticMarkup(node)

const NOW = Date.parse("2026-11-12T10:00:00Z")

function assertLabelTargetsInput(
  markup: string,
  labelText: string,
  inputName: string
) {
  const label = [...markup.matchAll(/<label\b[^>]*>[\s\S]*?<\/label>/g)]
    .map(([tag]) => tag)
    .find((tag) => tag.includes(labelText))
  const input = [...markup.matchAll(/<input\b[^>]*>/g)]
    .map(([tag]) => tag)
    .find((tag) => tag.includes(`name="${inputName}"`))
  assert.ok(label, `missing label: ${labelText}`)
  assert.ok(input, `missing input: ${inputName}`)
  const labelFor = label.match(/\bfor="([^"]+)"/)?.[1]
  const inputId = input.match(/\bid="([^"]+)"/)?.[1]
  assert.ok(labelFor, `label has no control association: ${labelText}`)
  assert.ok(inputId, `input has no id: ${inputName}`)
  assert.equal(labelFor, inputId)
}

const ACCOUNT: TelegramAccount = {
  id: "acc_01hxyzabcdefghijklmnopqrstuv",
  server_key: "official",
  network_type: "telegram",
  remote_user_id: 424242,
  remote_username: "mirza",
  remote_display_name: "میرزا",
  phone_number_masked: "+98 *** **45",
  status: "connected",
  last_connected_at: new Date(NOW - 5 * 60_000).toISOString(),
}

const OFFICIAL: WizardServer = {
  key: "official",
  type: "telegram",
  network_type: "telegram",
  login_methods: ["phone", "qr"],
}
const CAT: WizardServer = {
  key: "cat",
  type: "custom",
  network_type: "custom",
  login_methods: ["phone"],
}

function attempt(
  overrides: Partial<LoginAttempt> & {
    delivery_hint?: string
    code_length?: number
    delivery?: string
    phone_masked?: string
    error_code?: string
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

function renderAuthenticate(
  draft: LoginDraft,
  errorCode: string | null = null
) {
  return html(
    <AuthenticateStep
      draft={draft}
      selectedServerName="تلگرام رسمی"
      busy={false}
      errorCode={errorCode}
      view={loginViewState(draft, NOW)}
      methods={["phone", "qr"]}
      onUseQr={() => undefined}
      onBackToServer={() => undefined}
      onStartAttempt={() => undefined}
      onCode={() => undefined}
      onPassword={() => undefined}
      onResend={() => undefined}
      onBackToPhone={() => undefined}
      onPhoneChanged={() => undefined}
      onCodeChanged={() => undefined}
      onPasswordChanged={() => undefined}
      poll={async () => attempt()}
      onPoll={() => undefined}
      onQrRefresh={() => undefined}
      now={() => NOW}
    />
  )
}

// --- the account list -----------------------------------------------------

test("the accounts table renders identity, server, status, last connection and actions", () => {
  const out = html(
    <AccountList accounts={[ACCOUNT]} onAction={() => undefined} now={NOW} />
  )
  assert.match(out, /rounded-xl border bg-card/)
  assert.match(out, /data-slot="table"/)
  assert.match(out, /حساب/)
  assert.match(out, /سرور/)
  assert.match(out, /وضعیت/)
  assert.match(out, /آخرین اتصال/)
  assert.match(out, /عملیات/)
  assert.match(out, /میرزا/)
  assert.match(out, /@mirza/)
  assert.match(out, /رسمی تلگرام/)
  assert.match(out, /متصل/)
  assert.match(out, /آخرین اتصال/)
  assert.match(out, /dropdown-menu-trigger/)
  assert.match(out, /overflow-x-auto/)
  assert.match(out, /sticky left-0/)
  assert.doesNotMatch(out, /h-\[68px\]/)
  assert.match(out, /whitespace-nowrap/)
  assert.match(out, /table-actions-head[^>]*w-12 text-end/)
  assert.match(
    out,
    /table-actions-cell[^>]*sticky left-0 z-10 bg-background w-12 text-end/
  )
})

test("an account row never renders a credential or a full phone number", () => {
  const out = html(
    <AccountList accounts={[ACCOUNT]} onAction={() => undefined} now={NOW} />
  )
  // The masked number is the only phone-shaped thing that can appear.
  assert.doesNotMatch(out, /\+989\d{9}/)
  for (const secret of ["session", "token", "password", "api_id", "api_hash"]) {
    assert.doesNotMatch(out, new RegExp(secret, "i"), secret)
  }
})

test("the details view shows the opaque id and still no credential", () => {
  const out = html(<AccountDetailsContent account={ACCOUNT} now={NOW} />)
  assert.match(out, /data-slot="avatar"/)
  assert.match(out, /font-bold/)
  assert.match(out, /@mirza/)
  assert.match(out, /وضعیت اتصال/)
  assert.match(out, /جزئیات فنی/)
  assert.match(out, /acc_01hxyz/)
  assert.match(out, /کپی/)
  assert.match(out, /جزئیات فنی/)
  assert.doesNotMatch(out, /\+989\d{9}/)
  assert.doesNotMatch(out, /session_ciphertext|session_nonce/i)
})

test("unavailable account timestamps render an em dash", () => {
  const out = html(
    <AccountDetailsContent
      account={{
        ...ACCOUNT,
        last_connected_at: undefined,
        phone_number_masked: undefined,
      }}
      now={NOW}
    />
  )
  assert.match(out, /آخرین اتصال/)
  assert.match(out, />—</)
})

test("the list keeps every account as a table row and does not become cards", () => {
  const other: TelegramAccount = {
    ...ACCOUNT,
    id: "acc_02hxyzabcdefghijklmnopqrstuv",
    remote_display_name: "حساب دوم",
    remote_username: "mirza2",
  }
  const out = html(
    <AccountList
      accounts={[ACCOUNT, other]}
      onAction={() => undefined}
      now={NOW}
    />
  )
  assert.match(out, /میرزا/)
  assert.match(out, /حساب دوم/)
  assert.equal((out.match(/data-slot="table-row"/g) ?? []).length, 3)
  assert.doesNotMatch(out, /data-slot="account-card"/)
})

test("missing optional account values render safe placeholders in the table and details", () => {
  const incomplete: TelegramAccount = {
    id: "acc_incomplete",
    server_key: "custom-backend",
    network_type: "custom",
    status: "disconnected",
  }
  const list = html(
    <AccountList accounts={[incomplete]} onAction={() => undefined} now={NOW} />
  )
  const details = html(<AccountDetailsContent account={incomplete} now={NOW} />)
  assert.match(list, /سرور اختصاصی/)
  assert.match(list, /—/)
  assert.match(details, /—/)
  assert.doesNotMatch(details, /@undefined|undefined/)
})

test("the account cell omits an empty username placeholder", () => {
  const out = html(
    <AccountList
      accounts={[
        {
          ...ACCOUNT,
          remote_username: undefined,
          last_connected_at: "2026-11-14T10:00:00Z",
        },
      ]}
      onAction={() => undefined}
      now={NOW}
    />
  )
  assert.doesNotMatch(out, />—</)
})

test("the status badge says what the status is", () => {
  const out = html(
    <AccountStatusBadge
      account={{ ...ACCOUNT, status: "authentication_required" }}
    />
  )
  assert.match(out, /نیازمند ورود دوباره/)
})

test("single and multiple account selectors share account labels and show persisted statuses", () => {
  const disconnected = {
    ...ACCOUNT,
    id: "acc_disconnected",
    status: "disconnected",
  }
  const single = html(
    <AccountCombobox
      accounts={[ACCOUNT, disconnected]}
      value={ACCOUNT.id}
      onChange={() => undefined}
    />
  )
  assert.match(single, /میرزا/)

  const connectedOption = html(<AccountOption account={ACCOUNT} />)
  const disconnectedOption = html(
    <AccountOption account={disconnected} layout="inline" />
  )
  assert.match(connectedOption, /متصل/)
  assert.match(disconnectedOption, /قطع/)
  assert.match(connectedOption, /رسمی تلگرام/)
  assert.match(connectedOption, /@mirza/)
  assert.doesNotMatch(connectedOption, /official/)
  assert.match(connectedOption, /lucide-user-round/)

  const multi = html(
    <AccountMultiSelect
      accounts={[ACCOUNT, disconnected]}
      selected={[disconnected.id]}
      onToggle={() => undefined}
      onSelectAll={() => undefined}
    />
  )
  assert.match(multi, /account-multi-select/)
  assert.match(multi, /میرزا/)
})

test("an account option shows its name and username together", () => {
  const out = html(<AccountOption account={ACCOUNT} />)
  assert.match(out, /میرزا/)
  assert.match(out, /@mirza/)
})

test("an account option uses a safe non-phone fallback and neutral avatar", () => {
  const anonymous: TelegramAccount = {
    ...ACCOUNT,
    remote_display_name: "",
    remote_username: "",
    phone_number_masked: "+98 *** **45",
  }
  const out = html(<AccountOption account={anonymous} />)
  assert.match(out, /حساب تلگرام/)
  assert.match(out, /lucide-user-round/)
  assert.doesNotMatch(out, /\+98/)
  assert.doesNotMatch(out, /acc_01hxyz/)
})

test("same-name accounts remain distinguishable by their usernames", () => {
  const first = {
    ...ACCOUNT,
    remote_display_name: "حساب شخصی",
    remote_username: "personal",
  }
  const second = {
    ...ACCOUNT,
    id: "acc_second",
    remote_display_name: "حساب شخصی",
    remote_username: "work",
  }
  const out = html(
    <>
      <AccountOption account={first} />
      <AccountOption account={second} />
    </>
  )
  assert.equal((out.match(/حساب شخصی/g) ?? []).length, 2)
  assert.match(out, /@personal/)
  assert.match(out, /@work/)
})

test("status badges reflect the account record rather than server health", () => {
  const statuses = [
    ["connected", "متصل"],
    ["connecting", "در حال اتصال"],
    ["reconnecting", "در حال اتصال دوباره"],
    ["disconnected", "قطع"],
    ["authentication_required", "نیازمند ورود دوباره"],
    ["disabled", "غیرفعال"],
    ["error", "خطا"],
    ["removed", "حذف‌شده"],
  ] as const
  for (const [status, label] of statuses) {
    const out = html(<AccountStatusBadge account={{ ...ACCOUNT, status }} />)
    assert.match(out, new RegExp(label))
  }
})

test("the account menu offers only actions appropriate to its real status", () => {
  const connected = accountActionsFor(ACCOUNT)
  assert.ok(connected.includes("disconnect"))
  assert.ok(connected.includes("logout"))
  assert.ok(connected.includes("remove"))
  assert.match(
    html(
      <AccountList accounts={[ACCOUNT]} onAction={() => undefined} now={NOW} />
    ),
    /dropdown-menu-trigger/
  )

  const needsLogin = { ...ACCOUNT, status: "authentication_required" }
  const needsLoginActions = accountActionsFor(needsLogin)
  assert.ok(needsLoginActions.includes("connect"))
  assert.ok(!needsLoginActions.includes("disconnect"))
  assert.ok(!needsLoginActions.includes("logout"))
})

test("an already removed account has no empty actions menu", () => {
  const out = html(
    <AccountList
      accounts={[{ ...ACCOUNT, status: "removed" }]}
      onAction={() => undefined}
      now={NOW}
    />
  )
  assert.match(out, /حذف‌شده/)
  assert.doesNotMatch(out, /dropdown-menu-trigger/)
})

test("the empty state says to add an account, never that permission is missing", () => {
  const out = html(<NoAccountsState onAdd={() => undefined} />)
  assert.match(out, /هنوز هیچ حساب تلگرامی متصل نکرده‌اید\./)
  assert.match(out, /افزودن حساب/)
  assert.doesNotMatch(out, /مجوز/)
  assert.doesNotMatch(out, /دسترسی شما/)
})

test("the My Accounts empty state has compact mobile-friendly spacing", () => {
  const out = html(<NoAccountsState onAdd={() => undefined} compact />)
  assert.match(out, /py-7/)
  assert.match(out, /data-slot="add-account-action"[^>]*class="[^"]*\bh-7\b/)
})

// --- the compact dialog frame ---------------------------------------------

test("the login dialog uses the compact premium width and clips horizontal overflow", () => {
  assert.match(ACCOUNT_LOGIN_DIALOG_CLASS, /sm:max-w-\[440px\]/)
  assert.match(ACCOUNT_LOGIN_DIALOG_CLASS, /rounded-2xl/)
  assert.match(ACCOUNT_LOGIN_DIALOG_CLASS, /max-h-\[min\(90dvh,42rem\)\]/)
  assert.match(ACCOUNT_LOGIN_DIALOG_CLASS, /overflow-x-hidden/)
  assert.match(ACCOUNT_LOGIN_DIALOG_CLASS, /min-w-0/)
  assert.doesNotMatch(ACCOUNT_LOGIN_DIALOG_CLASS, /overflow-x-auto/)
  assert.match(ACCOUNT_DETAILS_DIALOG_CLASS, /max-h-\[min\(90dvh,42rem\)\]/)
  assert.match(ACCOUNT_DETAILS_DIALOG_CLASS, /overflow-x-hidden/)
  assert.match(ACCOUNT_DETAILS_DIALOG_CLASS, /sm:w-\[min\(100%-2rem,440px\)\]/)
})

test("the wizard uses the single shared X close control and has no cancel footer", () => {
  const wizardSource = readFileSync(
    "app/components/accounts/account-login-wizard.tsx",
    "utf8"
  )
  const dialogSource = readFileSync(
    "../../packages/ui/src/components/dialog.tsx",
    "utf8"
  )
  assert.doesNotMatch(wizardSource, /انصراف|DialogFooter/)
  assert.equal(
    (dialogSource.match(/data-slot="dialog-close-x"/g) ?? []).length,
    1
  )
})

// --- step 1: server --------------------------------------------------------

test("server options are compact nonwrapping rows with the icon right and arrow left", () => {
  const out = html(
    <ServerStep
      servers={[OFFICIAL, CAT]}
      chosen="official"
      onChoose={() => undefined}
    />
  )
  assert.match(out, /تلگرام رسمی/)
  assert.match(out, /سرور اختصاصی/)
  assert.match(out, /flex-nowrap/)
  assert.match(out, /justify-between/)
  assert.match(out, /min-w-0/)
  assert.match(out, /hover:bg-muted\/60/)
  assert.match(out, /focus-visible:ring/)
  assert.match(out, /lucide-arrow-left/)
  assert.doesNotMatch(out, /\bofficial\b|\bcat\b|ورود با شماره/)
  assert.doesNotMatch(out, /انصراف/)
})

test("a phone-only server remains selectable without technical secondary text", () => {
  const out = html(
    <ServerStep servers={[CAT]} chosen="" onChoose={() => undefined} />
  )
  assert.match(out, /role="radio"/)
  assert.doesNotMatch(out, /aria-disabled="true"/)
  assert.doesNotMatch(out, / disabled="/)
})

test("an empty server list is an honest refusal, not a blank screen", () => {
  const out = html(
    <ServerStep servers={[]} chosen="" onChoose={() => undefined} />
  )
  assert.match(out, /فهرست سرورها در دسترس نیست/)
})

// --- phone-first authentication -------------------------------------------

test("server selection's next component is the focused phone form", () => {
  const selected = run([
    { type: "select_server", serverKey: "official", methods: ["phone", "qr"] },
  ])
  const out = renderAuthenticate(selected)
  assertLabelTargetsInput(out, "شماره تلفن", "phone")
  assert.match(out, /autofocus=""/)
  assert.match(out, /dir="ltr"/)
  assert.match(out, /دریافت کد ورود/)
  assert.doesNotMatch(out, /روش ورود را انتخاب کنید/)
})

test("the phone form renders server phone errors inline and never uses raw text", () => {
  const out = html(
    <PhoneForm
      phone="+98912"
      busy={false}
      errorCode="login_phone_invalid"
      onChange={() => undefined}
      onSubmit={() => undefined}
    />
  )
  assert.match(out, /شمارهٔ تلفن معتبر نیست/)
  assert.doesNotMatch(out, /login_phone_invalid/)
  assert.match(out, /min-w-0/)
})

test("phone actions are stacked and required validation stays hidden initially", () => {
  const out = html(
    <PhoneForm
      phone=""
      busy={false}
      onChange={() => undefined}
      onSubmit={() => undefined}
      onBack={() => undefined}
    />
  )
  assert.match(out, /flex flex-col gap-2/)
  assert.match(out, /w-full/)
  assert.match(out, /دریافت کد ورود/)
  assert.match(out, /بازگشت/)
  assert.doesNotMatch(out, /شمارهٔ تلفن را وارد کنید/)
  assert.match(out, /placeholder="\+98 912 000 0000"/)
})

test("code and 2FA forms share stacked full-width primary and back actions", () => {
  const draft = run([
    {
      type: "attempt_started",
      attempt: attempt({
        state: "waiting_for_code",
        resend_after: new Date(NOW + 30_000).toISOString(),
      }),
    },
  ])
  const code = html(
    <CodePanel
      draft={draft}
      view={loginViewState(draft, NOW)}
      busy={false}
      onCodeChanged={() => undefined}
      onSubmit={() => undefined}
      onResend={() => undefined}
      onBack={() => undefined}
      now={() => NOW}
    />
  )
  const twoFactor = html(
    <TwoFactorForm
      password=""
      busy={false}
      wrong={false}
      onChange={() => undefined}
      onSubmit={() => undefined}
    />
  )
  for (const markup of [code, twoFactor]) {
    assert.match(markup, /flex flex-col gap-2/)
    assert.match(markup, /w-full/)
    assert.doesNotMatch(markup, /انصراف/)
  }
  assert.match(code, /تأیید کد/)
  assert.match(code, /بازگشت/)
  assert.match(twoFactor, /ورود به حساب/)
})

test("2FA appears only when the backend attempt asks for a password", () => {
  const codeDraft = run([
    {
      type: "attempt_started",
      attempt: attempt({ state: "waiting_for_code" }),
    },
  ])
  const codeMarkup = renderAuthenticate(codeDraft)
  assertLabelTargetsInput(codeMarkup, "کد تأیید", "code")
  assert.match(codeMarkup, /autofocus=""/)
  assert.doesNotMatch(codeMarkup, /رمز دومرحله‌ای/)

  const passwordDraft = run([
    {
      type: "attempt_started",
      attempt: attempt({ state: "waiting_for_password" }),
    },
  ])
  const passwordMarkup = renderAuthenticate(passwordDraft)
  assertLabelTargetsInput(passwordMarkup, "رمز دومرحله‌ای", "password")
  assert.doesNotMatch(passwordMarkup, /کد تأیید/)
})

// --- step 3: authenticate --------------------------------------------------

test("the phone step is one field, focused and typed for a telephone", () => {
  const out = html(
    <PhoneForm
      phone="+98912"
      busy={false}
      onChange={() => undefined}
      onSubmit={() => undefined}
    />
  )
  assert.match(out, /type="tel"/)
  assert.match(out, /inputMode="tel"/)
  assert.match(out, /autoComplete="tel"/)
  assert.match(out, /دریافت کد ورود/)
})

test("the code step reports the delivery method the API named", () => {
  const draft = run([
    {
      type: "attempt_started",
      attempt: attempt({
        state: "waiting_for_code",
        code_delivery: { type: "sentCodeTypeApp" },
        delivery_hint: "Telegram",
        code_length: 5,
        resend_after: new Date(NOW - 1).toISOString(),
      }),
    },
  ])
  const out = html(
    <CodePanel
      draft={draft}
      view={loginViewState(draft, NOW)}
      busy={false}
      onCodeChanged={() => undefined}
      onSubmit={() => undefined}
      onResend={() => undefined}
      onBack={() => undefined}
      now={() => NOW}
    />
  )
  assert.match(out, /داخلی/)
  assert.match(out, /Telegram/)
  assert.match(out, /maxLength="5"/)
  assert.match(out, /inputMode="numeric"/)
  assert.match(out, /کد ۵ رقمی/)
  // The delivery method is what Telegram said, never a claim about SMS.
  assert.doesNotMatch(out, /پیامک/)
})

test("the code step counts down instead of offering a resend it would refuse", () => {
  const draft = run([
    {
      type: "attempt_started",
      attempt: attempt({
        state: "waiting_for_code",
        resend_after: new Date(NOW + 30_000).toISOString(),
      }),
    },
  ])
  const out = html(
    <CodePanel
      draft={draft}
      view={loginViewState(draft, NOW)}
      busy={false}
      onCodeChanged={() => undefined}
      onSubmit={() => undefined}
      onResend={() => undefined}
      onBack={() => undefined}
      now={() => NOW}
    />
  )
  assert.match(out, /درخواست کد تازه \(۳۰\)/)
})

test("a rejected code keeps the code field so one digit can be corrected", () => {
  const draft = run([
    { type: "code_changed", code: "12345" },
    {
      type: "attempt_started",
      attempt: attempt({
        state: "waiting_for_code",
        last_error_code: "login_code_invalid",
        resend_after: new Date(NOW - 1).toISOString(),
      }),
    },
    { type: "attempt_failed", code: "login_code_invalid" },
  ])
  const out = html(
    <CodePanel
      draft={draft}
      view={loginViewState(draft, NOW)}
      busy={false}
      onCodeChanged={() => undefined}
      onSubmit={() => undefined}
      onResend={() => undefined}
      onBack={() => undefined}
      errorCode="login_code_invalid"
      now={() => NOW}
    />
  )
  assert.match(out, /name="code"/)
  assert.match(out, /value="12345"/)
  assert.match(out, /کد واردشده درست نیست/)
  assert.doesNotMatch(out, /login_code_invalid/)
})

test("the 2FA field is a masked password field and carries no value", () => {
  const out = html(
    <TwoFactorForm
      password=""
      busy={false}
      wrong={false}
      onChange={() => undefined}
      onSubmit={() => undefined}
    />
  )
  assert.match(out, /type="password"/)
  assert.match(out, /autoComplete="current-password"/)
  assert.match(out, /مخفی کردن رمز عبور|نمایش رمز عبور/)
})

test("the 2FA field is empty once the password has been submitted", () => {
  // A controlled input carries the value while it is being typed; the rule the
  // product states is about what happens at submission. This walks the actual
  // reducer and then renders the actual field from what the reducer holds.
  const typed = run([{ type: "password_changed", password: "hunter2secret" }])
  assert.equal(typed.password, "hunter2secret")

  const submitted = reduceLogin(typed, { type: "submit_password" })
  const out = html(
    <TwoFactorForm
      password={submitted.password}
      busy={false}
      wrong={false}
      onChange={() => undefined}
      onSubmit={() => undefined}
    />
  )
  assert.equal(submitted.password, "")
  assert.doesNotMatch(out, /hunter2secret/)
  assert.match(out, /type="password"/)
})

test("a refused QR code still shows its token and offers a fresh one", () => {
  const out = html(<QrCodeDisplay token="AgADaj-abc" />)
  assert.match(out, /AgADaj-abc/)
  assert.match(out, /data-slot="qr-code"/)
})

test("the QR step reports waiting, expiry and a countdown as its own states", () => {
  const waiting = run([
    {
      type: "attempt_started",
      attempt: attempt({
        method: "qr",
        state: "waiting_for_qr",
        qr: {
          token: "AgADaj-abc",
          expires_at: new Date(NOW + 60_000).toISOString(),
        },
      }),
    },
  ])
  const out = html(
    <QrPanel
      draft={waiting}
      phase={loginViewState(waiting, NOW).phase}
      busy={false}
      onRefresh={() => undefined}
      poll={async () => attempt()}
      onPoll={() => undefined}
      now={() => NOW}
    />
  )
  assert.match(out, /در انتظار تأیید روی تلگرام/)
  assert.match(out, /۶۰ ثانیه/)
  assert.match(out, /کد تازه/)

  const expired = run([
    {
      type: "attempt_started",
      attempt: attempt({
        method: "qr",
        state: "waiting_for_qr",
        qr: {
          token: "AgADaj-abc",
          expires_at: new Date(NOW - 1).toISOString(),
        },
      }),
    },
  ])
  const dead = html(
    <QrPanel
      draft={expired}
      phase="qr_expired"
      busy={false}
      onRefresh={() => undefined}
      poll={async () => attempt()}
      onPoll={() => undefined}
      now={() => NOW}
    />
  )
  assert.match(dead, /منقضی شده/)
})

test("the finished step says which of success and already-connected happened", () => {
  const ok = html(
    <DoneStep
      phase="success"
      errorCode={null}
      account={ACCOUNT}
      onConnected={() => undefined}
      onRestart={() => undefined}
    />
  )
  assert.match(ok, /ورود حساب تأیید شد/)
  assert.match(ok, /میرزا/)
  assert.match(ok, /تلگرام رسمی/)
  assert.match(ok, /حساب شما با موفقیت آماده استفاده است/)
  assert.doesNotMatch(ok, /Runner/)

  const already = html(
    <DoneStep
      phase="already_connected"
      errorCode={null}
      account={ACCOUNT}
      onConnected={() => undefined}
      onRestart={() => undefined}
    />
  )
  assert.match(already, /از قبل متصل بود/)

  const failed = html(
    <DoneStep
      phase="canceled"
      errorCode="login_attempt_invalid"
      account={null}
      onConnected={() => undefined}
      onRestart={() => undefined}
    />
  )
  assert.match(failed, /دیگر معتبر نیست/)
  assert.match(failed, /تلاش دوباره/)
})

test("authorization is not shown as successful until the safe account is available", () => {
  const out = html(
    <DoneStep
      phase="success"
      errorCode={null}
      account={null}
      onConnected={() => undefined}
      onRestart={() => undefined}
    />
  )
  assert.match(out, /در حال آماده‌سازی حساب/)
  assert.doesNotMatch(out, /ورود حساب تأیید شد/)
})

test("success distinguishes authenticated from a Runner that is still connecting", () => {
  const out = html(
    <DoneStep
      phase="success"
      errorCode={null}
      account={{ ...ACCOUNT, status: "connecting" }}
      onConnected={() => undefined}
      onRestart={() => undefined}
    />
  )
  assert.match(out, /حساب در حال آماده‌سازی است/)
  assert.doesNotMatch(out, /Runner/)
})

test("a failure is always phrased by this build, never by the backend", () => {
  const out = html(
    <DoneStep
      phase="connection_unavailable"
      errorCode="some_future_code"
      account={null}
      onConnected={() => undefined}
      onRestart={() => undefined}
    />
  )
  // The stable code is the input to the mapping and is not what a reader reads.
  assert.doesNotMatch(out, /some_future_code/)
  assert.match(out, /تلاش دوباره/)
  assert.match(out, /حساب متصل نشد/)
})
