import assert from "node:assert/strict"
import { test } from "node:test"

import { MemoryRouter } from "react-router"
import { renderToStaticMarkup } from "react-dom/server"

import {
  AddWebhookButton,
  WebhooksTable,
  clientAction as webhooksAction,
  clientLoader as webhooksLoader,
} from "../../routes/_panel/automation-webhooks"
import {
  AttemptCard,
  DeliveryActions,
  DeliveryHistory,
  DeliverySummary,
  clientAction as deliveryAction,
  retryOffered,
} from "../../routes/_panel/automation-delivery"
import {
  deliveryDurationMs,
  DeliveriesTable,
} from "../../routes/_panel/automation-deliveries"
import { WebhookForm } from "@/components/automation/webhook-dialog"
import { faDigits } from "@/components/persian-digits"
import {
  headerLines,
  hiddenHeaderCount,
  readDelivery,
  readWebhook,
  type DeliveryItem,
  type WebhookItem,
} from "@/lib/automation"

// --- harness ----------------------------------------------------------------
//
// A window and a fetch that answer like the gateway: an envelope of {data,
// meta}, or {error:{code,message}} with the status the gateway would use. Every
// call is recorded, so a test can assert the payload that was actually sent
// rather than the one the form meant to send.

interface Call {
  method: string
  path: string
  query: string
  body: unknown
}

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
  permissions: {
    global: ["automation.view", "webhooks.manage"],
    channels: [],
    categories: [],
  },
}

const ok = (data: unknown, meta: Record<string, unknown> = {}) => ({
  status: 200,
  payload: { data, meta },
})
const created = (data: unknown) => ({ status: 201, payload: { data, meta: {} } })
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

// A stored webhook exactly as the gateway renders it back.
const storedWebhook = {
  id: 5,
  name: "گیرنده اخبار",
  description: "ارسال رویدادها",
  enabled: true,
  url: "https://example.com/hook",
  method: "PUT",
  auth_mode: "hmac",
  auth_header: "",
  has_secret: true,
  timeout_ms: 4500,
  max_attempts: 5,
  source_channel_ids: [3, 9],
  created_at: "2026-01-01T00:00:00Z",
  updated_at: "2026-01-02T00:00:00Z",
}

function answerWebhookAPI(call: Call) {
  if (call.path === "/api/v1/auth/me") return ok(me)
  if (call.path === "/api/v1/local/channels") {
    return ok({
      channels: [
        { id: 3, title: "کانال خبری", client_key: "official" },
        { id: 9, title: "کانال ورزشی", client_key: "official" },
      ],
    })
  }
  if (call.path === "/api/v1/local/automation/webhooks/5")
    return ok({ webhook: storedWebhook })
  if (call.path === "/api/v1/local/automation/webhooks") {
    return ok({ webhooks: [storedWebhook] }, { total: 1, has_more: false })
  }
  return refuse(404, "not_found", "no such route " + call.path)
}

const draftFields = {
  intent: "create",
  id: "",
  name: "گیرنده اخبار",
  description: "ارسال رویدادها",
  enabled: "true",
  url: "https://example.com/hook",
  method: "PUT",
  auth_mode: "hmac",
  auth_header: "",
  secret: "",
  secret_clear: "false",
  source_channel_ids: "[3,9]",
  timeout_ms: "4500",
  max_attempts: "5",
}

// --- 1. the URL picks the form ----------------------------------------------

test("with no webhook in the URL the list opens no editor", async () => {
  stubApi(answerWebhookAPI)
  const data = await webhooksLoader({
    params: {},
    request: request("/automation/webhooks"),
  } as never)
  assert.equal(data.status, "ok")
  assert.equal((data as { editing: unknown }).editing, null)
})

test("new is the create form and a number is the edit form for that record", async () => {
  stubApi(answerWebhookAPI)
  const fresh = await webhooksLoader({
    params: { webhookId: "new" },
    request: request("/automation/webhooks/new"),
  } as never)
  assert.equal((fresh as { editing: unknown }).editing, "new")

  const calls = stubApi(answerWebhookAPI)
  const edit = await webhooksLoader({
    params: { webhookId: "5" },
    request: request("/automation/webhooks/5"),
  } as never)
  const editing = (edit as { editing: WebhookItem }).editing
  assert.equal(editing.name, "گیرنده اخبار")
  assert.equal(editing.timeoutMs, 4500, "read back from the stored row")
  assert.ok(
    calls.some((call) => call.path === "/api/v1/local/automation/webhooks/5"),
    "the record is read on its own URL, whatever page the list is on"
  )
})

// --- 2. the persistence round trip ------------------------------------------

test("a save posts the fields and lands on the record's own URL", async () => {
  const calls = stubApi((call) =>
    call.method === "POST" &&
    call.path === "/api/v1/local/automation/webhooks"
      ? created({ webhook: storedWebhook })
      : answerWebhookAPI(call)
  )
  const response = (await webhooksAction({
    request: submitTo("/automation/webhooks/new", draftFields),
  } as never)) as Response

  const posted = calls.find((call) => call.method === "POST")!
  assert.equal(posted.path, "/api/v1/local/automation/webhooks")
  assert.deepEqual(posted.body, {
    name: "گیرنده اخبار",
    description: "ارسال رویدادها",
    enabled: true,
    url: "https://example.com/hook",
    method: "PUT",
    auth_mode: "hmac",
    auth_header: "",
    timeout_ms: 4500,
    max_attempts: 5,
    source_channel_ids: [3, 9],
  })
  assert.equal(response.headers.get("Location"), "/automation/webhooks/5")
})

test("what a save posts is what the loader reads back after a reload", async () => {
  stubApi((call) =>
    call.method === "PATCH" ? ok({ webhook: storedWebhook }) : answerWebhookAPI(call)
  )
  const response = (await webhooksAction({
    request: submitTo("/automation/webhooks/5", {
      ...draftFields,
      intent: "update",
      id: "5",
      name: "نام تازه",
    }),
  } as never)) as Response
  assert.equal(response.headers.get("Location"), "/automation/webhooks/5")

  // A fresh request for the record's own URL is what a reload does: the values
  // come from the store, not from anything the form kept.
  stubApi((call) =>
    call.path === "/api/v1/local/automation/webhooks/5"
      ? ok({ webhook: { ...storedWebhook, name: "نام تازه" } })
      : answerWebhookAPI(call)
  )
  const reloaded = await webhooksLoader({
    params: { webhookId: "5" },
    request: request("/automation/webhooks/5"),
  } as never)
  const editing = (reloaded as { editing: { name: string; url: string } }).editing
  assert.equal(editing.name, "نام تازه")
  assert.equal(editing.url, "https://example.com/hook")
})

test("the editor is seeded with the stored row and never with a secret", () => {
  const out = renderToStaticMarkup(
    <WebhookForm
      webhook={readWebhook(storedWebhook)}
      channels={[{ id: 3, label: "کانال خبری" }]}
      pending={false}
      onClose={() => {}}
      onSubmit={() => {}}
    />
  )
  assert.ok(out.includes('value="گیرنده اخبار"'), "the name the store kept")
  assert.ok(out.includes('value="4500"'), "timeout_ms survives the round trip")
  assert.ok(out.includes('value="5"'), "max_attempts survives the round trip")
  assert.ok(!/type="password"/.test(out), "there is no field holding a secret")
  assert.ok(out.includes("رمزی ذخیره شده است"), "the stored secret is only named")
})

// --- 3. the write-only secret -----------------------------------------------

test("the secret is absent when untouched, empty when removed, a value when replaced", async () => {
  const calls = stubApi((call) =>
    call.method === "PATCH" ? ok({ webhook: storedWebhook }) : answerWebhookAPI(call)
  )
  const patch = () =>
    (calls.find((call) => call.method === "PATCH")!.body ?? {}) as Record<
      string,
      unknown
    >

  await webhooksAction({
    request: submitTo("/automation/webhooks/5", { ...draftFields, intent: "update", id: "5" }),
  } as never)
  assert.ok(!("secret" in patch()), "an untouched secret posts no key at all")

  calls.length = 0
  await webhooksAction({
    request: submitTo("/automation/webhooks/5", {
      ...draftFields,
      intent: "update",
      id: "5",
      secret: "",
      secret_clear: "true",
    }),
  } as never)
  assert.equal(patch().secret, "", "an empty secret is posted as empty: removed")

  calls.length = 0
  await webhooksAction({
    request: submitTo("/automation/webhooks/5", {
      ...draftFields,
      intent: "update",
      id: "5",
      secret: "hunter2",
    }),
  } as never)
  assert.equal(patch().secret, "hunter2", "a typed secret replaces the stored one")
})

test("a secret the server refuses is reported on the secret field", async () => {
  stubApi((call) =>
    call.method === "PATCH"
      ? refuse(
          400,
          "invalid_input",
          "automation: invalid configuration: webhooks: the whsec_ secret is not valid base64"
        )
      : answerWebhookAPI(call)
  )
  const result = (await webhooksAction({
    request: submitTo("/automation/webhooks/5", {
      ...draftFields,
      intent: "update",
      id: "5",
      secret: "whsec_not-base64!",
    }),
  } as never)) as { error?: string; fieldErrors?: { secret?: string } }

  assert.ok(result.error, "the refusal comes back as data, never as a success")
  assert.ok(
    result.error.includes("not valid base64"),
    "the server's own sentence is shown"
  )
  assert.ok(
    result.fieldErrors?.secret?.includes("not valid base64"),
    "and it is shown on the field the operator typed it into"
  )
})

// --- 4. auth mode switching -------------------------------------------------

test("the custom header appears only for the custom-header mode", () => {
  const formWith = (authMode: string) =>
    renderToStaticMarkup(
      <WebhookForm
        webhook={null}
        channels={[{ id: 3, label: "کانال خبری" }]}
        pending={false}
        onClose={() => {}}
        onSubmit={() => {}}
        defaultValues={{ authMode: authMode as never }}
      />
    )
  for (const mode of ["none", "bearer", "hmac"]) {
    const out = formWith(mode)
    assert.ok(!out.includes("نام هدر"), `${mode} offers no custom header`)
    assert.ok(!out.includes('for="webhook-auth-header"'))
  }
  const out = formWith("header")
  assert.ok(out.includes("نام هدر"))
  assert.match(out, /for="webhook-auth-header"/)
})

// --- 5. refusals the page must recognise ------------------------------------

test("a public_base_url_required refusal is shown with its reason, not as a success", async () => {
  stubApi((call) =>
    call.method === "POST"
      ? refuse(
          400,
          "public_base_url_required",
          "api.public_base_url must name the externally reachable origin before a webhook can be enabled, because receivers need an absolute media URL."
        )
      : answerWebhookAPI(call)
  )
  const result = (await webhooksAction({
    request: submitTo("/automation/webhooks/new", draftFields),
  } as never)) as { error?: string; intent?: string }

  assert.ok(result.error, "no redirect and no success on a refusal")
  assert.equal(result.intent, "create")
  assert.ok(result.error.includes("api.public_base_url"), result.error)
  assert.ok(result.error.includes("نشانی عمومی سرویس"), "explained in Persian")
})

test("a 409 conflict is explained in Persian and keeps the server's reason", async () => {
  stubApi((call) =>
    call.path.endsWith("/retry")
      ? refuse(
          409,
          "conflict",
          "automation: conflicting configuration: this delivery is succeeded and cannot be retried"
        )
      : answerWebhookAPI(call)
  )
  const result = (await deliveryAction({
    request: submitTo("/automation/webhooks/5/deliveries/44", { intent: "retry" }),
    params: { webhookId: "5", deliveryId: "44" },
  } as never)) as { error?: string; intent?: string }

  assert.ok(result.error, "a refusal is data, never a redirect")
  assert.equal(result.intent, "retry")
  assert.ok(result.error.includes("ناسازگار"), "explained in Persian")
  assert.ok(
    result.error.includes("cannot be retried"),
    "the server's own reason is kept"
  )
})

test("a save that the server refuses is never reported as a success", async () => {
  stubApi((call) =>
    call.method === "PATCH"
      ? refuse(400, "invalid_input", "method must be one of POST, PUT or PATCH.")
      : answerWebhookAPI(call)
  )
  const result = (await webhooksAction({
    request: submitTo("/automation/webhooks/5", {
      ...draftFields,
      intent: "update",
      id: "5",
      method: "GET",
    }),
  } as never)) as { error?: string }
  assert.equal(result.error, "method must be one of POST, PUT or PATCH.")
})

// --- 6. the delivery history -----------------------------------------------

const persistedDelivery: DeliveryItem = readDelivery({
  id: 44,
  webhook_id: 7,
  event_id: "evt_abc",
  status: "failed",
  attempt_count: 2,
  next_attempt_at: null,
  last_http_status: 500,
  last_error: "upstream 500",
  created_at: "2026-01-01T00:00:00Z",
  updated_at: "2026-01-01T01:00:00Z",
  completed_at: "2026-01-01T01:00:00Z",
  request: {
    method: "POST",
    url: "https://example.com/hook",
    headers: {
      Authorization: "Bearer hunter2",
      "X-Trace": "abc",
      "X-Api-Key": "hunter2",
    },
    body: '{"id": 1}',
    body_truncated: true,
  },
  attempts: [
    {
      id: 101,
      attempt_number: 1,
      request_method: "POST",
      request_url: "https://example.com/hook",
      request_headers: {
        Authorization: "Bearer hunter2",
        "X-Trace": "abc",
      },
      request_body: '{"id": 1}',
      request_body_truncated: false,
      response_status: 500,
      response_headers: { "Content-Type": "application/json" },
      response_body: "boom",
      response_body_truncated: false,
      response_bytes: 4,
      duration_ms: 120,
      error_code: "http_status",
      error_message: "upstream 500",
      started_at: "2026-01-01T00:00:00Z",
      finished_at: "2026-01-01T00:00:01Z",
    },
    {
      id: 102,
      attempt_number: 2,
      request_method: "POST",
      request_url: "https://example.com/hook",
      request_headers: { "X-Trace": "def" },
      request_body: "",
      request_body_truncated: false,
      response_status: 0,
      response_headers: {},
      response_body: "",
      response_body_truncated: false,
      response_bytes: 0,
      duration_ms: 15000,
      error_code: "timeout",
      error_message: "request timed out",
      started_at: "2026-01-01T01:00:00Z",
      finished_at: null,
    },
  ],
})

const html = (node: React.ReactNode) =>
  renderToStaticMarkup(
    <MemoryRouter initialEntries={["/automation"]}>{node}</MemoryRouter>
  )
const text = (markup: string) =>
  markup
    .replace(/&quot;/g, '"')
    .replace(/&#x27;/g, "'")
    .replace(/&lt;/g, "<")
    .replace(/&gt;/g, ">")
    .replace(/&amp;/g, "&")

test("the three tabs render the persisted request and every attempt", () => {
  const out = text(html(<DeliveryHistory delivery={persistedDelivery} />))
  for (const tab of ["درخواست", "پاسخ", "تلاش‌ها"]) {
    assert.ok(out.includes(tab), tab)
  }
  assert.ok(out.includes("https://example.com/hook"), "the recorded address")
  assert.ok(out.includes('"id": 1'), "the recorded body, rendered as JSON")
  assert.ok(out.includes("X-Trace"), "the recorded headers")
  assert.ok(out.includes("بریده شده"), "a body the server cut short says so")

  const first = text(html(<AttemptCard attempt={persistedDelivery.attempts[0]} />))
  assert.ok(first.includes("#1"))
  assert.ok(first.includes("HTTP 500"), "the recorded response status")
  assert.ok(first.includes(faDigits(120)), "the recorded duration")
  assert.ok(first.includes("boom"), "the recorded response body")

  const second = text(html(<AttemptCard attempt={persistedDelivery.attempts[1]} />))
  assert.ok(second.includes("#2"))
  assert.ok(second.includes("بدون پاسخ"), "an attempt with no answer says so")
  assert.ok(second.includes(faDigits(15000)), "its recorded duration")
  assert.ok(second.includes("timeout"), "the recorded error code")
  assert.ok(second.includes("request timed out"), "the recorded error message")
})

test("the summary reports the delivery's own persisted numbers", () => {
  const out = text(html(<DeliverySummary delivery={persistedDelivery} />))
  assert.ok(out.includes("evt_abc"), "the recorded event")
  assert.ok(out.includes("upstream 500"), "the recorded refusal")
  assert.ok(out.includes(faDigits(2)), "the recorded attempt count")
  assert.ok(
    out.includes(faDigits(15120)),
    "the recorded total of the attempt durations"
  )
  assert.ok(
    out.includes(faDigits(15000)),
    "the recorded duration of the last attempt"
  )
})

test("an authorization header is never rendered and never copied", () => {
  for (const node of [
    <DeliveryHistory key="h" delivery={persistedDelivery} />,
    <AttemptCard key="a" attempt={persistedDelivery.attempts[0]} />,
    <DeliverySummary key="s" delivery={persistedDelivery} />,
  ]) {
    const out = text(html(node))
    assert.ok(!out.includes("Authorization"), "the name is withheld")
    assert.ok(!out.includes("hunter2"), "the value is withheld")
    assert.ok(!out.includes("Bearer"), "no shape of it is rendered either")
  }
  // The copy button is handed exactly the lines the reader is shown, so a
  // withheld header cannot leave through the clipboard either.
  for (const headers of [
    persistedDelivery.request!.headers,
    persistedDelivery.attempts[0].request.headers,
  ]) {
    const lines = headerLines(headers)
    assert.ok(!lines.some((line) => /authorization|api-key/i.test(line)))
    assert.ok(hiddenHeaderCount(headers) > 0, "and the count is reported")
  }
})

// --- 7. manual retry --------------------------------------------------------

test("a retry is offered only for an eligible terminal failure", () => {
  for (const status of ["pending", "delivering", "succeeded", "cancelled"]) {
    assert.equal(
      retryOffered({ status: status as never }, true),
      false,
      `${status} must not offer a retry`
    )
  }
  assert.equal(retryOffered({ status: "failed" }, true), true)
  assert.equal(
    retryOffered({ status: "failed" }, false),
    false,
    "a retry is a write and needs webhooks.manage"
  )

  const withRetry = html(
    <DeliveryActions
      history="/automation/webhooks/7/deliveries"
      retryable={retryOffered({ status: "failed" }, true)}
      submitting={false}
      onRetry={() => {}}
    />
  )
  assert.ok(withRetry.includes("تلاش دوباره"))

  const without = html(
    <DeliveryActions
      history="/automation/webhooks/7/deliveries"
      retryable={retryOffered({ status: "succeeded" }, true)}
      submitting={false}
      onRetry={() => {}}
    />
  )
  assert.ok(!without.includes("تلاش دوباره"))
})

// --- 8. the list surfaces ---------------------------------------------------

test("the action button is named افزودن وب‌هوک", () => {
  const out = html(<AddWebhookButton onAdd={() => {}} />)
  assert.ok(out.includes("افزودن وب‌هوک"))
})

test("a row names the values the store kept and never a secret", () => {
  const out = html(
    <WebhooksTable
      webhooks={[readWebhook(storedWebhook)]}
      channels={[
        { id: 3, label: "کانال خبری" },
        { id: 9, label: "کانال ورزشی" },
      ]}
      canManage
      onEdit={() => {}}
      onToggle={() => {}}
      onDelete={() => {}}
      onTest={() => {}}
    />
  )
  assert.ok(out.includes("گیرنده اخبار"))
  assert.ok(out.includes("کانال خبری، کانال ورزشی"))
  assert.ok(out.includes("https://example.com/hook"))
  assert.ok(!/secret/i.test(out), "no secret in a list row")
})

test("the delivery list shows a duration only from stored timestamps", () => {
  const done = { ...persistedDelivery, attempts: [] }
  assert.equal(deliveryDurationMs(done), 3_600_000)
  assert.equal(
    deliveryDurationMs({ ...done, completedAt: null }),
    null,
    "a delivery still going has no duration yet"
  )
  assert.equal(
    deliveryDurationMs({ ...done, completedAt: "1999-01-01T00:00:00Z" }),
    null,
    "a clock that runs backwards is reported as unknown, never negative"
  )
  const out = html(<DeliveriesTable deliveries={[done]} webhookID={7} />)
  assert.ok(out.includes("مدت"))
  assert.ok(out.includes(faDigits(3_600_000)))
})
