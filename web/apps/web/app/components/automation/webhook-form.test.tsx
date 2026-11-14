// The webhook create/edit page against a stand-in of the real API.
//
// What is proved here is the page's contract: that it is a page and not a
// dialog, which fields it offers and under which auth mode, that a stored
// secret is never rendered and never posted, and exactly which payload reaches
// the gateway for a create and for an edit. The API is a stub, so the
// assertions are about the conversation — the paths and the bodies that
// actually go out — and about the markup the page paints.

import assert from "node:assert/strict"
import { test } from "node:test"

import {
  MemoryRouter,
  RouterProvider,
  createMemoryRouter,
  type ShouldRevalidateFunctionArgs,
} from "react-router"
import { renderToStaticMarkup } from "react-dom/server"

import {
  WebhookForm,
  authHeaderErrorOf,
  authOptions,
  methodOptions,
  urlErrorOf,
  type SourceChannelOption,
  type WebhookFieldErrors,
} from "@/components/automation/webhook-form"
import {
  channelsForServer,
  reachableIds,
  serverKeyFor,
  sourceItems,
} from "@/components/automation/source-selection"
import AutomationWebhookForm, {
  clientAction,
  clientLoader,
  handle,
  meta,
  saveReturnTo,
  shouldRevalidate,
  webhookSubmitFields,
  type ActionData,
} from "@/routes/_panel/automation-webhook-form"
import {
  authModeLabels,
  readWebhookRecord,
  webhookBody,
  webhookDraftOf,
  type WebhookDraft,
} from "@/lib/automation"
import type { TelegramAccount } from "@/lib/types"

// --- harness ----------------------------------------------------------------

interface Call {
  method: string
  path: string
  query: string
  body: unknown
}

// A window and a fetch that answer like the gateway: an envelope of {data,
// meta}, or {error:{code,message}} with the status the gateway would use. Every
// call is recorded with the body it carried, so a test can assert what was
// actually sent rather than what was meant.
function stubApi(
  answer: (call: Call) => { status: number; payload: unknown }
): Call[] {
  const calls: Call[] = []
  const stored = new Map<string, string>([["telesync.panel.token", "tok"]])
  ;(globalThis as Record<string, unknown>).window = {
    location: { origin: "http://panel.test" },
    sessionStorage: {
      getItem: (key: string) => stored.get(key) ?? null,
      setItem: (key: string, value: string) => void stored.set(key, value),
      removeItem: (key: string) => void stored.delete(key),
    },
  }
  ;(globalThis as Record<string, unknown>).fetch = async (
    input: unknown,
    init: RequestInit = {}
  ) => {
    const url = new URL(String(input), "http://panel.test")
    const call: Call = {
      method: init.method ?? "GET",
      path: url.pathname,
      query: url.search,
      body: init.body ? JSON.parse(String(init.body)) : undefined,
    }
    calls.push(call)
    const { status, payload } = answer(call)
    return new Response(JSON.stringify(payload), {
      status,
      headers: { "Content-Type": "application/json" },
    })
  }
  return calls
}

const me = {
  id: 1,
  fullname: "Operator",
  username: "op",
  is_active: true,
  created_at: "",
  updated_at: "",
  access: {
    channels: true,
    categories: true,
    users: true,
    servers: true,
    automation: true,
  },
  permissions: { global: ["automation.view", "webhooks.manage"] },
}

const ok = (data: unknown, meta: Record<string, unknown> = {}) => ({
  status: 200,
  payload: { data, meta },
})
const refuse = (status: number, code: string, message: string) => ({
  status,
  payload: { error: { code, message } },
})

const request = (url: string) => new Request(`http://panel.test${url}`)

const submitTo = (path: string, fields: Record<string, string>) => {
  const form = new FormData()
  for (const [key, value] of Object.entries(fields)) form.set(key, value)
  return {
    url: `http://panel.test${path}`,
    formData: async () => form,
  } as unknown as Request
}

// Exactly what the gateway renders back for one webhook. Nothing in it is a
// secret: the view carries `has_secret` and never the value.
const storedWebhook = (over: Record<string, unknown> = {}) => ({
  id: 5,
  name: "گیرنده اخبار",
  description: "رویدادهای خبری",
  enabled: true,
  url: "https://example.com/hook",
  method: "PUT",
  auth_mode: "header",
  auth_header: "X-API-Key",
  has_secret: true,
  timeout_ms: 4500,
  max_attempts: 5,
  source_channel_ids: [3, 9],
  created_at: "2026-01-01T00:00:00Z",
  updated_at: "2026-01-02T00:00:00Z",
  ...over,
})

const answerEditor = (call: Call, webhook: Record<string, unknown>) => {
  if (call.path === "/api/v1/auth/me") return ok(me)
  if (call.path === "/api/v1/me/telegram-accounts") {
    return ok({ accounts: [TEST_ACCOUNT, TEST_ACCOUNT_TWO] })
  }
  if (call.path === "/api/v1/local/channels") {
    return ok({
      channels: [
        {
          id: 3,
          title: "کانال خبری",
          client_key: "official",
          account_id: TEST_ACCOUNT.id,
        },
        {
          id: 9,
          title: "کانال ورزشی",
          client_key: "official",
          account_id: TEST_ACCOUNT_TWO.id,
        },
      ],
    })
  }
  if (call.path === "/api/v1/telegram") {
    return ok({ servers: [telegramServer("official")] })
  }
  if (call.path === "/api/v1/local/automation/webhooks/5") {
    return ok({ webhook })
  }
  return refuse(404, "not_found", "no such route " + call.path)
}

// A Telegram connection as the gateway reports one. The source picker filters
// channels by its key, so the tests can put channels on more than one.
const telegramServer = (key: string) => ({
  key,
  type: "official",
  enabled: true,
  health_checkable: false,
  state: "ready",
  authorized: true,
  transport: "",
  last_success_at: null,
  last_error: "",
})

// Two accounts of the caller's own, both on the official server: exactly the
// case where a source picker must say which account a channel came from.
const TEST_ACCOUNT: TelegramAccount = {
  id: "acc_aaaaaaaaaaaaaaaaaaaaaaaaaa",
  server_key: "official",
  network_type: "telegram",
  remote_display_name: "حساب نخست",
  status: "connected",
}

const TEST_ACCOUNT_TWO: TelegramAccount = {
  id: "acc_bbbbbbbbbbbbbbbbbbbbbbbbbb",
  server_key: "official",
  network_type: "telegram",
  remote_display_name: "حساب دوم",
  status: "connected",
}

// The same rows, as the picker reads them: scoped to the accounts the caller
// owns, so a channel belonging to somebody else is never on the list.
const channels: SourceChannelOption[] = [
  {
    id: 3,
    label: "کانال خبری",
    clientKey: "official",
    accountId: TEST_ACCOUNT.id,
  },
  {
    id: 9,
    label: "کانال ورزشی",
    clientKey: "official",
    accountId: TEST_ACCOUNT_TWO.id,
  },
]

const createSeed = (): WebhookDraft => ({
  accountID: null,
  name: "",
  description: "",
  enabled: true,
  url: "",
  method: "POST",
  authMode: "none",
  authHeader: "",
  secret: null,
  timeoutMs: 5000,
  maxAttempts: 3,
  allowLocalhost: false,
  sourceChannelIds: [],
})

const editSeed = (): WebhookDraft => ({
  accountID: null,
  name: "گیرنده اخبار",
  description: "رویدادهای خبری",
  enabled: true,
  url: "https://example.com/hook",
  method: "PUT",
  authMode: "header",
  authHeader: "X-API-Key",
  secret: null,
  timeoutMs: 4500,
  maxAttempts: 5,
  allowLocalhost: false,
  sourceChannelIds: [3],
})

// The form's own markup, inside the router the page runs under: the account
// picker and the source picker both carry links and state that read the URL.
const html = (node: React.ReactNode) =>
  renderToStaticMarkup(
    <MemoryRouter initialEntries={["/automation/webhooks/new"]}>{node}</MemoryRouter>
  )

const render = (over: Partial<React.ComponentProps<typeof WebhookForm>> = {}) =>
  html(
    <WebhookForm
      record={null}
      seed={createSeed()}
      channels={channels}
      onSubmit={() => {}}
      onCancel={() => {}}
      {...over}
    />
  )

// --- 1. a page, not a dialog ------------------------------------------------

test("the create screen is one full page with one form and no dialog", () => {
  const out = render()
  assert.equal(out.match(/<form/g)?.length, 1, "exactly one form")
  assert.ok(out.includes('id="webhook-form"'), "the form carries the id the header submits")
  assert.ok(!/<dialog\b/i.test(out), "no <dialog> element")
  assert.ok(!out.includes('data-slot="dialog"'), "no Dialog component")
  assert.ok(!out.includes('role="dialog"'), "no dialog role")
  assert.ok(!out.includes("DialogContent"))
  assert.ok(out.includes("افزودن وب‌هوک"), "the title names the action")
})

test("the header carries the page's only save and its only cancel", () => {
  const create = render()
  assert.equal(create.match(/type="submit"/g)?.length, 1, "one submit")
  assert.equal((create.match(/انصراف/g) ?? []).length, 1, "one cancel")
  assert.ok(create.includes("افزودن"), "the create save is named افزودن")
  // The buttons sit outside the form and reach it through its id, so a long
  // page never hides them and the phone's bottom bar cannot cover them.
  assert.match(create, /<button[^>]*type="submit"[^>]*form="webhook-form"/)
  assert.ok(create.includes(">انصراف</button>"))

  const edit = render({ record: { id: 5, hasSecret: true }, seed: editSeed() })
  assert.equal(edit.match(/type="submit"/g)?.length, 1)
  assert.equal((edit.match(/انصراف/g) ?? []).length, 1)
  assert.ok(edit.includes("ذخیره تغییرات"), "the edit save names what it does")
  assert.ok(edit.includes("ویرایش وب‌هوک"))
  assert.ok(edit.includes("آزمایش وب‌هوک"), "an edit offers a test send")
  assert.ok(!render().includes("آزمایش وب‌هوک"), "a create has no record to test")
})

test("the sections are all on the page with their own headings", () => {
  const out = render()
  for (const heading of [
    "اطلاعات وب‌هوک",
    "کانال‌های منبع",
    "تنظیمات درخواست",
    "احراز هویت",
    "وضعیت وب‌هوک",
    "تنظیمات پیشرفته",
  ]) {
    assert.ok(out.includes(heading), `${heading} is a section heading`)
  }
  assert.ok(out.includes("پیام‌های جدید این کانال‌ها به وب‌هوک ارسال می‌شوند."))
  // No card inside a card: the sections are headings and separators.
  assert.equal((out.match(/data-slot="card"/g) ?? []).length, 0)
})

// --- 2. the fields ----------------------------------------------------------

test("the required fields are on the page with their exact names", () => {
  const out = render()
  assert.ok(out.includes("نام وب‌هوک"))
  assert.match(out, /for="webhook-name"/)
  assert.ok(out.includes("توضیحات"))
  assert.match(out, /for="webhook-description"/)
  assert.ok(out.includes("آدرس وب‌هوک"))
  assert.match(out, /for="webhook-url"/)
  assert.match(out, /type="url"/)
  assert.ok(out.includes("https://example.com/webhook"))
  assert.ok(out.includes("متد درخواست"))
  assert.match(out, /for="webhook-method"/)
})

test("the source channels are a multi-select over managed channels", () => {
  // A create has chosen no connection yet, so the channel list waits for one
  // rather than offering channels this webhook could never reach.
  const fresh = render()
  assert.ok(fresh.includes("سرور تلگرام"), "the connection is asked for first")
  assert.ok(fresh.includes("نخست سرور تلگرام را انتخاب کنید"))
  assert.ok(!/<input[^>]*name="source_channel_ids"/.test(fresh), "never a box of ids")

  // A record that already names channels opens on the connection those
  // channels belong to, with the picker live and the stored rows named.
  const picked = render({ seed: { ...createSeed(), sourceChannelIds: [3] } })
  assert.ok(picked.includes("کانال را انتخاب کنید"), "a searchable picker, not a text box")
  // A picked channel is named by its stored row, never by a Telegram peer id.
  assert.ok(picked.includes("کانال خبری"))
  assert.ok(!picked.includes("-100"), "a peer id is never the label")
})

test("the method list is POST, PUT and PATCH and the form starts on POST", () => {
  assert.deepEqual(
    methodOptions.map((option) => option.value),
    ["POST", "PUT", "PATCH"]
  )
  assert.deepEqual(
    methodOptions.map((option) => option.label),
    ["POST", "PUT", "PATCH"]
  )
  assert.equal(methodOptions[0].detail, "پیش‌فرض", "POST says it is the default")
  // The list lives in a closed popup, so the page shows what is chosen and the
  // options themselves are asserted on the list.
  assert.ok(render().includes('value="POST"'))
  assert.ok(render({ seed: { ...createSeed(), method: "PATCH" } }).includes('value="PATCH"'))
})

test("a method chosen in the form is the method that is posted", async () => {
  const fields = webhookSubmitFields(
    {
      ...createSeed(),
      name: "گیرنده",
      url: "https://example.com/hook",
      method: "PATCH",
    },
    "create",
    null
  )
  assert.equal(fields.method, "PATCH")
  const calls = stubApi((call) => {
    if (call.path === "/api/v1/auth/me") return ok(me)
    return { status: 201, payload: { data: { webhook: storedWebhook() }, meta: {} } }
  })
  await clientAction({
    params: { webhookId: "new" },
    request: submitTo("/automation/webhooks/new", fields),
  } as never)
  const body = calls.find((call) => call.method === "POST")?.body as Record<
    string,
    unknown
  >
  assert.equal(body.method, "PATCH")
})

// --- 3. authentication ------------------------------------------------------

test("every auth mode is offered and named", () => {
  assert.deepEqual(
    authOptions.map((option) => option.value),
    ["hmac", "bearer", "header", "none"]
  )
  assert.deepEqual(
    authOptions.map((option) => option.label),
    ["HMAC-SHA256", "Bearer Token", "Custom Header", "بدون احراز هویت"]
  )
  // The canonical Persian name travels with each one, so the picker and the
  // listing never call the same mode by two different names.
  for (const option of authOptions) {
    assert.equal(option.detail, authModeLabels[option.value as keyof typeof authModeLabels])
  }
})

test("the auth mode decides which fields are on the page", () => {
  const withMode = (authMode: WebhookDraft["authMode"]) =>
    render({ seed: { ...createSeed(), authMode } })

  const none = withMode("none")
  assert.ok(!none.includes('for="webhook-secret"'), "no auth means no secret field")
  assert.ok(!none.includes('for="webhook-auth-header"'), "and no header name")
  assert.ok(none.includes("بدون احراز هویت"))

  const hmac = withMode("hmac")
  assert.ok(hmac.includes('for="webhook-secret"'))
  assert.ok(hmac.includes("Secret Key"))
  assert.ok(!hmac.includes('for="webhook-auth-header"'), "HMAC signs, it does not name a header")

  const bearer = withMode("bearer")
  assert.ok(bearer.includes('for="webhook-secret"'))
  assert.ok(bearer.includes("Bearer Token"))
  assert.ok(!bearer.includes('for="webhook-auth-header"'))

  const header = withMode("header")
  assert.ok(header.includes('for="webhook-auth-header"'))
  assert.ok(header.includes('for="webhook-secret"'))
  assert.ok(header.includes("X-API-Key"), "the custom header starts on the documented default")
})

test("the mode's own explanation is on the page, not only in the closed list", () => {
  assert.ok(render({ seed: { ...createSeed(), authMode: "hmac" } }).includes("HMAC-SHA256"))
  assert.ok(render({ seed: { ...createSeed(), authMode: "bearer" } }).includes("Bearer Token"))
  assert.ok(render({ seed: { ...createSeed(), authMode: "header" } }).includes("Custom Header"))
})

test("a header name that the transport owns is refused", () => {
  assert.equal(authHeaderErrorOf("X-API-Key"), null)
  assert.equal(authHeaderErrorOf(""), "نام هدر الزامی است.")
  assert.ok(authHeaderErrorOf("Authorization"))
  assert.ok(authHeaderErrorOf("webhook-signature"))
  assert.ok(authHeaderErrorOf("bad header"))
})

// --- 4. secrets -------------------------------------------------------------

test("a secret is masked and its reveal is opt-in", () => {
  const out = render({
    seed: { ...createSeed(), authMode: "hmac", secret: "typed-by-hand" },
  })
  assert.match(out, /type="password"/, "the input masks what is typed")
  assert.ok(!/type="text"/.test(out), "nothing is revealed by default")
  assert.match(out, /aria-pressed="false"/, "the reveal toggle starts off")
  assert.ok(out.includes("نمایش رمز"))
  assert.ok(!out.includes("مخفی کردن رمز"), "the toggle only knows one way until pressed")
  // What is typed is in the field that holds it and nowhere else on the page.
  const outside = out.replace(/<input[^>]*>/g, "")
  assert.ok(!outside.includes("typed-by-hand"))
})

test("an existing secret is never loaded and never shown", () => {
  const record = { id: 5, hasSecret: true }
  const out = render({ record, seed: { ...editSeed(), authMode: "hmac" } })
  assert.ok(out.includes("رمزی ذخیره شده است"))
  assert.ok(!/type="password"/.test(out), "no input is pre-filled with a secret")
  assert.ok(!out.includes("whsec"))
  // The model agrees: a record carries a flag and no value.
  const item = readWebhookRecord({ webhook: storedWebhook() })
  assert.equal(item.hasSecret, true)
  assert.equal(webhookDraftOf(item).secret, null, "the draft is seeded with nothing to keep")
})

test("an untouched secret posts no secret key and an explicit clear posts an empty one", async () => {
  const post = async (draft: WebhookDraft) => {
    const calls = stubApi((call) => {
      if (call.path === "/api/v1/auth/me") return ok(me)
      return { status: 201, payload: { data: { webhook: storedWebhook() }, meta: {} } }
    })
    await clientAction({
      params: { webhookId: "new" },
      request: submitTo("/automation/webhooks/new", webhookSubmitFields(draft, "create", null)),
    } as never)
    return calls.find((call) => call.method === "POST")?.body as Record<string, unknown>
  }

  const withSecret = (secret: string | null, authMode: WebhookDraft["authMode"] = "hmac") => ({
    ...createSeed(),
    name: "گیرنده",
    url: "https://example.com/hook",
    authMode,
    secret,
  })

  const kept = await post(withSecret(null))
  assert.ok(!("secret" in kept), "saying nothing leaves the stored secret alone")
  assert.ok(!("secret_clear" in kept), "the wire contract has no clear flag either")

  const cleared = await post(withSecret(""))
  assert.equal(cleared.secret, "", "an explicit clear removes it")

  const replaced = await post(withSecret("whsec_new"))
  assert.equal(replaced.secret, "whsec_new")

  const off = await post(withSecret(null, "none"))
  assert.ok(!("secret" in off), "no auth mode sends no secret at all")

  // A mode with no authentication has no secret field at all, so a value left
  // in the form from one that does is never posted against it.
  const stale = await post(withSecret("left-over-from-hmac", "none"))
  assert.ok(!("secret" in stale), "a stale secret is never sent for a mode that has none")
})

// --- 5. the conversation ----------------------------------------------------

test("a create posts exactly the fields the contract names", async () => {
  const draft: WebhookDraft = {
    accountID: null,
    name: " گیرنده اخبار ",
    description: " رویدادها ",
    enabled: false,
    url: " https://example.com/hook ",
    method: "PUT",
    authMode: "header",
    authHeader: " X-API-Key ",
    secret: "whsec_new",
    timeoutMs: 4500,
    maxAttempts: 5,
    allowLocalhost: false,
    sourceChannelIds: [3, 9],
  }
  const calls = stubApi((call) => {
    if (call.path === "/api/v1/auth/me") return ok(me)
    return { status: 201, payload: { data: { webhook: storedWebhook() }, meta: {} } }
  })
  const result = (await clientAction({
    params: { webhookId: "new" },
    request: submitTo("/automation/webhooks/new", webhookSubmitFields(draft, "create", null)),
  } as never)) as ActionData

  const post = calls.find((call) => call.method === "POST")
  assert.equal(post?.path, "/api/v1/local/automation/webhooks")
  assert.deepEqual(post?.body, webhookBody(draft))
  assert.deepEqual(
    Object.keys(post?.body as object).sort(),
    [
      "account_id",
      "allow_localhost",
      "auth_header",
      "auth_mode",
      "description",
      "enabled",
      "max_attempts",
      "method",
      "name",
      "secret",
      "source_channel_ids",
      "timeout_ms",
      "url",
    ],
    "the payload is snake_case and carries nothing extra"
  )
  assert.equal(
    post?.body?.account_id,
    "",
    "an unbound webhook posts an empty account, never a guessed one"
  )
  assert.equal(result.ok, true)
  assert.equal(result.intent, "create")
})

test("an edit patches the record's own URL with the same payload", async () => {
  const draft: WebhookDraft = { ...editSeed(), name: "نام تازه" }
  const calls = stubApi((call) => answerEditor(call, storedWebhook()))
  const result = (await clientAction({
    params: { webhookId: "5" },
    request: submitTo("/automation/webhooks/5/edit", webhookSubmitFields(draft, "update", 5)),
  } as never)) as ActionData

  const patch = calls.find((call) => call.method === "PATCH")
  assert.equal(patch?.path, "/api/v1/local/automation/webhooks/5")
  assert.deepEqual(patch?.body, webhookBody(draft))
  assert.equal(result.ok, true)
  assert.equal(result.intent, "update")
})

test("a save that succeeds returns to the listing view it came from", () => {
  const saved: ActionData = { ok: true, intent: "create" }
  assert.equal(saveReturnTo(saved, null), "/automation?tab=webhooks")
  assert.equal(
    saveReturnTo(saved, { from: "/automation?tab=webhooks&q=%D8%AE%D8%A8%D8%B1&page=2" }),
    "/automation?tab=webhooks&q=%D8%AE%D8%A8%D8%B1&page=2",
    "the listing's own tab, search and page"
  )
  assert.equal(saveReturnTo(saved, { from: "/elsewhere" }), "/automation?tab=webhooks")
  assert.equal(saveReturnTo({ error: "x", intent: "create" }, null), null)
  assert.equal(saveReturnTo({ ok: true, intent: "test" }, null), null, "a test goes nowhere")
  assert.equal(saveReturnTo(undefined, null), null)
})

test("a save that is refused is shown near its field and keeps what was typed", async () => {
  const draft: WebhookDraft = {
    ...createSeed(),
    name: "گیرنده",
    url: "https://example.com/hook",
    authMode: "hmac",
    secret: "not-base64",
  }
  stubApi((call) => {
    if (call.path === "/api/v1/auth/me") return ok(me)
    return refuse(400, "invalid_input", "secret is not valid base64")
  })
  const result = (await clientAction({
    params: { webhookId: "new" },
    request: submitTo("/automation/webhooks/new", webhookSubmitFields(draft, "create", null)),
  } as never)) as ActionData

  assert.notEqual(result.ok, true, "nothing here claims success")
  assert.deepEqual(result.fieldErrors, { secret: "secret is not valid base64" })
  assert.equal(result.values?.name, "گیرنده", "what was typed comes back")
  assert.equal(result.values?.url, "https://example.com/hook")
  assert.ok(
    !("secret" in (result.values ?? {})),
    "a typed secret never travels back through the action"
  )

  // The page reopens on those values, so the operator sees their own input.
  const out = render({ seed: { ...result.values!, secret: null }, fieldErrors: result.fieldErrors })
  assert.ok(out.includes("secret is not valid base64"))
  assert.ok(out.includes('value="گیرنده"'))
  assert.ok(out.includes('value="https://example.com/hook"'))
})

test("an invalid address is refused before it travels", async () => {
  const calls = stubApi((call) => answerEditor(call, storedWebhook()))
  const result = (await clientAction({
    params: { webhookId: "new" },
    request: submitTo(
      "/automation/webhooks/new",
      webhookSubmitFields({ ...createSeed(), name: "x", url: "file:///etc/passwd" }, "create", null)
    ),
  } as never)) as ActionData
  assert.ok(result.fieldErrors?.url)
  assert.equal(urlErrorOf("ftp://example.com/x", false), "نشانی باید با http:// یا https:// آغاز شود.")
  assert.equal(urlErrorOf("https://user:pass@example.com/x", false), "نشانی نباید نام کاربری یا گذرواژه داشته باشد.")
  assert.equal(urlErrorOf("https://example.com/x#part", false), "نشانی نباید بخش # داشته باشد.")
  assert.equal(urlErrorOf("", false), "نشانی مقصد الزامی است.")
  assert.equal(urlErrorOf("https://example.com/hook", false), null)
  assert.ok(
    !calls.some((call) => call.method === "POST"),
    "a form that cannot be valid never reaches the API"
  )
})

test("a loopback address this webhook asked for is not refused at the door", async () => {
  // The exception is checked twice: beside the field as the operator types, and
  // again in the action before the request travels. The second one reads the
  // switch out of the submitted form, and a call that forgets to pass it on
  // refuses every loopback address anyway — leaving the operator staring at a
  // green switch and a sentence saying the address is forbidden. Carrying it is
  // a compile-time requirement now; this is the behaviour that requirement
  // exists to keep.
  stubApi((call) => answerEditor(call, storedWebhook()))
  const result = (await clientAction({
    params: { webhookId: "new" },
    request: submitTo(
      "/automation/webhooks/new",
      webhookSubmitFields(
        {
          ...createSeed(),
          name: "x",
          url: "http://localhost:3000/webhook",
          allowLocalhost: true,
        },
        "create",
        null
      )
    ),
  } as never)) as ActionData
  assert.equal(
    result.fieldErrors?.url ?? null,
    null,
    "a permitted loopback address passes the structural check and reaches the API"
  )
})

test("the advanced settings are bounded by the ranges the API enforces", async () => {
  const fields = (timeoutMs: number, maxAttempts: number) =>
    webhookSubmitFields(
      { ...createSeed(), name: "x", url: "https://example.com/hook", timeoutMs, maxAttempts },
      "create",
      null
    )
  const refuseOnce = async (draft: WebhookDraft) => {
    stubApi((call) =>
      call.path === "/api/v1/auth/me" ? ok(me) : refuse(500, "nope", "nope")
    )
    return (await clientAction({
      params: { webhookId: "new" },
      request: submitTo("/automation/webhooks/new", webhookSubmitFields(draft, "create", null)),
    } as never)) as ActionData
  }
  const bad = await refuseOnce({
    ...createSeed(),
    name: "x",
    url: "https://example.com/hook",
    timeoutMs: 99,
    maxAttempts: 101,
  })
  assert.ok(bad.fieldErrors?.timeoutMs)
  assert.ok(bad.fieldErrors?.maxAttempts)

  assert.equal(fields(5000, 3).timeout_ms, "5000")
  assert.equal(fields(5000, 3).max_attempts, "3")
})

test("the create form starts on the safe defaults", () => {
  const out = render()
  assert.ok(out.includes('value="5000"'), "one attempt may take five seconds")
  assert.ok(out.includes('value="3"'), "one event may cost three of them")
  assert.match(out, /role="switch"[^>]*aria-checked="true"/, "a new webhook starts enabled")
  assert.ok(out.includes("فعال"))
  assert.ok(out.includes("وضعیت وب‌هوک"))
})

test("a disabled webhook is shown as disabled", () => {
  const out = render({ seed: { ...createSeed(), enabled: false } })
  assert.match(out, /aria-checked="false"/)
  assert.ok(out.includes("غیرفعال"))
})

test("a field error sits next to the field that caused it", () => {
  const fieldErrors: WebhookFieldErrors = {
    name: "نام وب‌هوک الزامی است.",
    url: "نشانی مقصد الزامی است.",
    authHeader: "نام هدر الزامی است.",
    timeoutMs: "مهلت باید بین ۱۰۰ تا ۳۰۰۰۰۰ میلی‌ثانیه باشد.",
    maxAttempts: "تعداد تلاش‌ها باید بین ۱ تا ۱۰۰ باشد.",
  }
  const out = render({
    seed: { ...createSeed(), authMode: "header", authHeader: "bad header" },
    fieldErrors,
    error: "برخی فیلدها را کامل کنید.",
  })
  for (const message of Object.values(fieldErrors)) assert.ok(out.includes(message!))
  assert.match(out, /role="alert"/)
  assert.ok(out.includes("برخی فیلدها را کامل کنید."))
})

// --- 6. save, reload, reopen ------------------------------------------------

test("save, reload and reopen shows the values the server kept", async () => {
  stubApi((call) => answerEditor(call, storedWebhook()))
  const data = (await clientLoader({
    params: { webhookId: "5" },
    request: request("/automation/webhooks/5/edit"),
  } as never)) as { status: string; webhook?: ReturnType<typeof readWebhookRecord> }

  assert.equal(data.status, "ok")
  const draft = webhookDraftOf(data.webhook!)
  assert.equal(draft.name, "گیرنده اخبار")
  assert.equal(draft.description, "رویدادهای خبری")
  assert.equal(draft.url, "https://example.com/hook")
  assert.equal(draft.method, "PUT")
  assert.equal(draft.authMode, "header")
  assert.equal(draft.authHeader, "X-API-Key")
  assert.equal(draft.timeoutMs, 4500)
  assert.equal(draft.maxAttempts, 5)
  assert.deepEqual(draft.sourceChannelIds, [3, 9])
  assert.equal(draft.secret, null, "the stored secret is never read back")

  const out = render({
    record: { id: 5, hasSecret: true },
    seed: draft,
  })
  assert.ok(out.includes('value="گیرنده اخبار"'))
  assert.ok(out.includes('value="https://example.com/hook"'))
  assert.ok(out.includes('value="4500"'))
  assert.ok(out.includes('value="5"'))
})

test("a create page loads the channels and no record", async () => {
  stubApi((call) => answerEditor(call, storedWebhook()))
  const data = (await clientLoader({
    params: {},
    request: request("/automation/webhooks/new"),
  } as never)) as {
    status: string
    creating?: boolean
    webhook?: unknown
    channels?: SourceChannelOption[]
  }
  assert.equal(data.status, "ok")
  assert.equal(data.creating, true)
  assert.equal(data.webhook, null)
  // Each row keeps the account it came from: that is what tells two rows on the
  // same server apart and what the source picker filters by.
  assert.deepEqual(data.channels, [
    {
      id: 3,
      label: "کانال خبری",
      clientKey: "official",
      accountId: TEST_ACCOUNT.id,
      detail: "official",
    },
    {
      id: 9,
      label: "کانال ورزشی",
      clientKey: "official",
      accountId: TEST_ACCOUNT_TWO.id,
      detail: "official",
    },
  ])
  assert.equal(
    data.channels?.every((channel) => Number.isInteger(channel.id) && channel.id > 0),
    true,
    "the picker is keyed by managed_channels.id"
  )
})

test("a record the store no longer has is reported as missing, not as an empty form", async () => {
  stubApi((call) => {
    if (call.path === "/api/v1/auth/me") return ok(me)
    if (call.path === "/api/v1/local/channels") return ok({ channels: [] })
    return refuse(404, "not_found", "no such webhook")
  })
  const data = await clientLoader({
    params: { webhookId: "5" },
    request: request("/automation/webhooks/5/edit"),
  } as never)
  assert.equal(data.status, "missing")
})

test("a channel list that cannot be read is reported beside the picker", async () => {
  stubApi((call) => {
    if (call.path === "/api/v1/auth/me") return ok(me)
    if (call.path === "/api/v1/local/channels") {
      return refuse(500, "internal", "channels are unavailable")
    }
    return ok({ webhook: storedWebhook() })
  })
  const data = (await clientLoader({
    params: { webhookId: "5" },
    request: request("/automation/webhooks/5/edit"),
  } as never)) as { status: string; channelsError?: string | null }
  assert.equal(data.status, "ok", "the form stays usable")
  assert.ok(data.channelsError)

  const out = render({
    seed: editSeed(),
    channelsError: data.channelsError ?? null,
  })
  assert.match(out, /role="alert"/)
  assert.ok(out.includes("channels are unavailable"))
})

test("a channel list that is loading says so instead of claiming there are none", () => {
  const out = render({ channels: [], channelsLoading: true })
  assert.match(out, /role="status"/)
  assert.ok(out.includes("در حال بارگذاری کانال‌ها…"))
  assert.ok(!out.includes("کانالی در دسترس نیست."))
})

// --- 7. the page as the router mounts it ------------------------------------

// The page is rendered through a real data router, because the unsaved-work
// guard is a router hook and only exists there. `ToastProvider` is the app's
// own and cannot render under the server renderer — it portals its live region,
// and without a document it drops its context — so the render is walked up to
// that call and stopped there. What it proves is that every router hook before
// it resolves: the navigation, the location, the params, the submit and the
// blocker all run inside a data router without a complaint.
test("the page is wired to the router, guard included", () => {
  const router = createMemoryRouter(
    [
      {
        path: "/automation/webhooks/new",
        element: (
          <AutomationWebhookForm
            loaderData={{
              status: "ok",
              creating: true,
              webhook: null,
              channels,
              accounts: [],
              servers: [],
              localhostAllowed: true,
              channelsError: null,
              canManage: true,
            }}
          />
        ),
      },
    ],
    { initialEntries: ["/automation/webhooks/new"] }
  )
  assert.throws(
    () => renderToStaticMarkup(<RouterProvider router={router} />),
    (error: unknown) => {
      const message = error instanceof Error ? error.message : String(error)
      assert.ok(!/data router/i.test(message), message)
      assert.match(message, /ToastProvider/)
      return true
    }
  )
})

test("the module is the route shape the router registers", () => {
  assert.equal(typeof AutomationWebhookForm, "function")
  assert.equal(typeof clientLoader, "function")
  assert.equal(typeof clientAction, "function")
  assert.equal(typeof meta, "function")
  assert.ok(handle && typeof handle === "object")
  assert.equal(typeof shouldRevalidate, "function")
})

test("the loader is not re-run behind a form that was just submitted to", () => {
  const args = {
    formMethod: "post",
    currentUrl: new URL("http://panel.test/automation/webhooks/new"),
    nextUrl: new URL("http://panel.test/automation/webhooks/new"),
    currentParams: {},
    nextParams: {},
    formAction: undefined,
    formEncType: undefined,
    text: undefined,
    formData: undefined,
    json: undefined,
    actionResult: undefined,
    actionStatus: undefined,
    defaultShouldRevalidate: true,
  } as unknown as ShouldRevalidateFunctionArgs
  assert.equal(shouldRevalidate(args), false, "a refusal keeps what was typed")
  assert.equal(
    shouldRevalidate({ ...args, formMethod: undefined }),
    true,
    "arriving at the page always reads the stored row"
  )
})

// --- 8. the test send -------------------------------------------------------

test("a test send is its own request and reports the exchange as the service did", async () => {
  const calls = stubApi((call) => {
    if (call.path === "/api/v1/auth/me") return ok(me)
    if (call.path === "/api/v1/local/automation/webhooks/5/test") {
      return ok({
        delivery: {
          id: 1,
          status: "failed",
          last_error: "connection refused",
          last_http_status: 502,
          attempts: [{ response_status: 502, error_message: "connection refused" }],
        },
      })
    }
    return refuse(404, "not_found", "no such route " + call.path)
  })
  const result = (await clientAction({
    params: { webhookId: "5" },
    request: submitTo("/automation/webhooks/5/edit", { intent: "test", id: "5" }),
  } as never)) as ActionData

  const sent = calls.find((call) => call.path.endsWith("/test"))
  assert.equal(sent?.method, "POST")
  assert.ok(!sent?.body, "a test sends no configuration and no secret")
  assert.equal(result.ok, true)
  assert.deepEqual(result.testResult, {
    status: "failed",
    httpStatus: 502,
    error: "connection refused",
  })
  assert.equal(saveReturnTo(result, null), null, "a test never leaves the page")
})

// --- 6. the accordion, the dependent source, the shared shell ----------------

// A button tag with its class list taken off: the shared Button always carries
// `disabled:` variants, which say nothing about the button's own state.
const buttonTag = (out: string, type: string) =>
  ((out.match(/<button\b[^>]*>/g) ?? []).find((tag) =>
    tag.includes(`type="${type}"`)
  ) ?? "").replace(/ class="[^"]*"/, "")

test("advanced settings sit in one accordion, shut until it is opened", () => {
  const out = render()
  assert.ok(out.includes("تنظیمات پیشرفته"), "the section is named")
  assert.ok(out.includes('data-slot="accordion-trigger"'), "it opens and shuts")
  assert.ok(!/data-panel-open/.test(out), "and it starts shut")
  // The two numbers stay mounted while it is shut, so opening and closing the
  // section cannot throw away what has been typed into them.
  assert.ok(out.includes("مهلت هر تلاش (میلی‌ثانیه)"))
  assert.ok(out.includes("بیشینه تلاش‌ها"))
})

test("the connection is chosen before the channels that follow from it", () => {
  const out = render({ servers: [telegramServer("official")] })
  assert.ok(out.includes("سرور تلگرام"), "the connection is asked for")
  assert.ok(
    out.indexOf('id="webhook-source-server"') < out.indexOf('id="webhook-sources"'),
    "and it is above the channel list"
  )
  assert.ok(
    out.includes('aria-disabled="true"'),
    "the channel picker waits for a connection"
  )
  assert.ok(out.includes("نخست سرور تلگرام را انتخاب کنید"))
})

test("a webhook with no source cannot be saved at all", () => {
  const blank = render({ servers: [telegramServer("official")] })
  assert.ok(
    / disabled[ =>/]/.test(buttonTag(blank, "submit")),
    "no connection and no channel: nothing to save"
  )
  const sourced = render({
    servers: [telegramServer("official")],
    seed: { ...createSeed(), sourceChannelIds: [3] },
  })
  assert.ok(
    !/ disabled[ =>/]/.test(buttonTag(sourced, "submit")),
    "a source on the chosen connection is enough to save"
  )
})

test("the source list is the chosen connection's, and a saved one is not dropped", () => {
  const both = [
    { id: 3, label: "کانال خبری", clientKey: "official" },
    { id: 9, label: "کانال ورزشی", clientKey: "custom" },
  ]
  assert.deepEqual(channelsForServer(both, "official").map((c) => c.id), [3])
  assert.deepEqual(channelsForServer(both, undefined), [], "no connection, no list")

  // A record already naming channels from two connections keeps both: they are
  // listed and they are posted, rather than trimmed to the visible filter.
  assert.deepEqual(sourceItems(both, "official", [3, 9]).map((c) => c.id), [3, 9])
  assert.equal(serverKeyFor(both, [9, 3]), "custom", "the first known one names it")

  // But choosing a different connection is a decision, and a channel it cannot
  // reach is not a source that can deliver.
  assert.deepEqual(reachableIds(both, [3, 9], "official"), [3])
})

test("the actions are Cancel then Save, so Save is the outermost control on the left", () => {
  const out = render({
    servers: [telegramServer("official")],
    seed: { ...createSeed(), sourceChannelIds: [3] },
  })
  const cancel = out.indexOf('type="button"')
  const submit = out.indexOf('type="submit"')
  assert.ok(cancel > -1 && submit > cancel, out.slice(cancel, submit + 20))
  assert.ok(out.includes("انصراف"))
  assert.ok(out.includes("ذخیره") || out.includes("افزودن"))
})

test("the page is drawn in the shared automation layout", () => {
  const out = render()
  assert.ok(out.includes("max-w-4xl"), "the shared content width")
  assert.ok(out.includes("mx-auto"), "centred in the panel's own column")
})

// --- the loopback exception -------------------------------------------------

// The localhost switch's own tag. Base UI replaces the id with its own, so the
// switch is found by the label it is next to rather than by an attribute; the
// status row above it is another switch and would answer first.
const switchTag = (out: string) => {
  const at = out.indexOf("اجازه ارسال به localhost")
  return (out.slice(at).match(/<[^>]*data-slot="switch"[^>]*>/g) ?? [""])[0] ?? ""
}

test("the localhost switch sits in Advanced Settings and starts off", () => {
  const out = render({ localhostAllowed: true })
  const trigger = out.indexOf("تنظیمات پیشرفته")
  const switchAt = out.indexOf("اجازه ارسال به localhost")
  assert.ok(trigger > -1 && switchAt > trigger, "the switch is behind the accordion")
  const sw = switchTag(out)
  assert.ok(sw.includes('aria-checked="false"'), "a new webhook does not ask for the exception")
  assert.ok(
    out.includes("اجازه ارسال وب‌هوک به localhost بدون نیاز به HTTPS"),
    "and it says what it is for"
  )
})

test("the switch is disabled and says why when the deployment forbids it", () => {
  const out = render({ localhostAllowed: false })
  assert.ok(out.includes("ارسال به localhost در تنظیمات سرور غیرفعال است."))
  // Base UI marks a disabled switch rather than setting the native attribute.
  assert.ok(
    /data-disabled|aria-disabled="true"/.test(switchTag(out)),
    "a switch the server will not honour is not offered"
  )
})

test("a localhost address is accepted only where the exception is on", () => {
  for (const url of [
    "http://localhost:3000/webhook",
    "http://127.0.0.1:8080/hook",
    "http://[::1]:8080/webhook",
  ]) {
    assert.equal(urlErrorOf(url, true), null, url)
    assert.match(urlErrorOf(url, false) ?? "", /localhost/, url)
  }
})

test("the refusal names the switch that is actually off", () => {
  const url = "http://localhost:3000/webhook"
  // The deployment allows it and this webhook has not asked: the fix is a
  // control on this very page, so the sentence should point at it.
  const webhookOff = urlErrorOf(url, false, true) ?? ""
  assert.match(webhookOff, /تنظیمات پیشرفته/)
  assert.match(webhookOff, /اجازه ارسال به localhost/)
  // The deployment forbids it instead: the form's own switch is then disabled,
  // so pointing at that one would send the operator to a control that cannot
  // change anything. The sentence has to name the setting that can.
  const deploymentOff = urlErrorOf(url, false, false) ?? ""
  assert.match(deploymentOff, /تنظیمات سرور/)
  assert.ok(!deploymentOff.includes("تنظیمات پیشرفته"), deploymentOff)
})

test("a public address keeps the https rule", () => {
  assert.equal(urlErrorOf("https://example.com/hook", true), null)
  assert.match(urlErrorOf("http://example.com/hook", true) ?? "", /https/)
  assert.match(urlErrorOf("http://192.168.1.10/hook", true) ?? "", /https/)
  // The exception is a list, not a range: a name that merely looks local is
  // not one of it, and a disguised spelling of a loopback address is not one
  // either — the server refuses both and so does this form.
  assert.match(urlErrorOf("http://127.1:8080/hook", true) ?? "", /https/)
  assert.match(urlErrorOf("http://0x7f000001:8080/hook", true) ?? "", /https/)
})

test("the saved setting is what the form shows and what it posts", () => {
  const on = render({
    seed: { ...createSeed(), allowLocalhost: true },
    localhostAllowed: true,
  })
  assert.ok(
    switchTag(on).includes('aria-checked="true"'),
    "an edit opens on the stored value"
  )
  const body = webhookBody({ ...createSeed(), allowLocalhost: true })
  assert.equal(body.allow_localhost, true)
  assert.equal(webhookBody(createSeed()).allow_localhost, false, "and it defaults to off")
})
