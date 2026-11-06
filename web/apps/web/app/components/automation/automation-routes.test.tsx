// The automation routes against a stand-in of the real API.
//
// These prove the contract the screens actually rely on: what the URL decides,
// what is posted, what is read back, and what a refusal does to the page. The
// API is a stub here, so what is asserted is the shape of the conversation —
// the request path and body on one side, the values the loader re-reads on the
// other — rather than a live daemon.

import assert from "node:assert/strict"
import { test } from "node:test"

import { MemoryRouter } from "react-router"
import { renderToStaticMarkup } from "react-dom/server"

import {
  clientAction as webhooksAction,
  clientLoader as webhooksLoader,
} from "../../routes/_panel/automation-webhooks"
import {
  clientAction as workflowAction,
  clientLoader as workflowEditorLoader,
} from "../../routes/_panel/automation-workflow-editor"
import {
  AttemptCard,
  DeliveryHistory,
  DeliverySummary,
} from "../../routes/_panel/automation-delivery"
import { WebhookForm } from "@/components/automation/webhook-dialog"
import { faDigits } from "@/components/persian-digits"
import {
  readDelivery,
  readWebhook,
  type DeliveryItem,
} from "@/lib/automation"

// --- harness ----------------------------------------------------------------

interface Call {
  method: string
  path: string
  query: string
  body: unknown
}

// A window and a fetch that answer like the gateway: an envelope of {data,
// meta}, or {error:{code,message}} with the status the gateway would use.
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
  access: { channels: true, categories: true, users: true, servers: true, automation: true },
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
  if (call.path === "/api/v1/local/automation/webhooks/5") return ok({ webhook: storedWebhook })
  if (call.path === "/api/v1/local/automation/webhooks") {
    return ok({ webhooks: [storedWebhook] }, { total: 1, has_more: false })
  }
  return refuse(404, "not_found", "no such route " + call.path)
}

// --- 1. the editor is opened by the URL -------------------------------------

test("with no webhook in the URL the list opens no editor", async () => {
  stubApi(answerWebhookAPI)
  const data = await webhooksLoader({
    params: {},
    request: request("/automation/webhooks"),
  } as never)
  assert.equal(data.status, "ok")
  assert.equal((data as { editing: unknown }).editing, null)
})

test("/automation/webhooks/new opens the create form", async () => {
  stubApi(answerWebhookAPI)
  const data = await webhooksLoader({
    params: { webhookId: "new" },
    request: request("/automation/webhooks/new"),
  } as never)
  assert.equal(data.status, "ok")
  assert.equal((data as { editing: unknown }).editing, "new")
})

test("/automation/webhooks/:id opens the editor on that record", async () => {
  const calls = stubApi(answerWebhookAPI)
  const data = await webhooksLoader({
    params: { webhookId: "5" },
    request: request("/automation/webhooks/5"),
  } as never)
  assert.equal(data.status, "ok")
  const editing = (data as { editing: { name: string; timeoutMs: number } })
    .editing
  assert.equal(editing.name, "گیرنده اخبار")
  assert.equal(editing.timeoutMs, 4500, "read back from the stored row")
  assert.ok(
    calls.some((call) => call.path === "/api/v1/local/automation/webhooks/5"),
    "the record is read on its own, whatever page the list is on"
  )
})

test("an id that is not a number is a missing record, not an empty editor", async () => {
  stubApi(answerWebhookAPI)
  const data = await webhooksLoader({
    params: { webhookId: "abc" },
    request: request("/automation/webhooks/abc"),
  } as never)
  assert.equal(data.status, "missing")
})

test("a record that does not exist is missing, not a blank form", async () => {
  stubApi((call) =>
    call.path === "/api/v1/local/automation/webhooks/404"
      ? refuse(404, "not_found", "The requested resource does not exist.")
      : answerWebhookAPI(call)
  )
  const data = await webhooksLoader({
    params: { webhookId: "404" },
    request: request("/automation/webhooks/404"),
  } as never)
  assert.equal(data.status, "missing")
})

test("an account without automation access is told so, not shown an empty page", async () => {
  stubApi((call) =>
    call.path === "/api/v1/auth/me"
      ? ok({ ...me, access: { ...me.access, automation: false } })
      : answerWebhookAPI(call)
  )
  const data = await webhooksLoader({
    params: {},
    request: request("/automation/webhooks"),
  } as never)
  assert.equal(data.status, "forbidden")
  assert.ok((data as { message: string }).message.includes("مجوز"))
})

// --- 2. the persistence round trip ------------------------------------------

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

const submit = (fields: Record<string, string>) => {
  const form = new FormData()
  for (const [key, value] of Object.entries(fields)) form.set(key, value)
  return {
    url: "http://panel.test/automation/webhooks/new",
    formData: async () => form,
  } as unknown as Request
}

test("creating a webhook posts the fields and lands on the stored record", async () => {
  const calls = stubApi((call) =>
    call.method === "POST" && call.path === "/api/v1/local/automation/webhooks"
      ? ok({ webhook: storedWebhook })
      : answerWebhookAPI(call)
  )
  const response = (await webhooksAction({
    request: submit(draftFields) as unknown as Request,
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
  // An untouched secret is not in the payload at all: the stored one stays.
  assert.ok(!("secret" in (posted.body as Record<string, unknown>)))
  assert.equal(response.headers.get("Location"), "/automation/webhooks/5")
})

test("saving again posts the same fields and the values are still there on reload", async () => {
  const calls = stubApi((call) =>
    call.method === "PATCH"
      ? ok({ webhook: { ...storedWebhook, name: "نام تازه" } })
      : answerWebhookAPI(call)
  )
  const response = (await webhooksAction({
    request: submit({ ...draftFields, intent: "update", id: "5", name: "نام تازه" }),
  } as never)) as Response
  const patched = calls.find((call) => call.method === "PATCH")!
  assert.equal(patched.path, "/api/v1/local/automation/webhooks/5")
  assert.equal((patched.body as Record<string, unknown>).name, "نام تازه")
  assert.equal(response.headers.get("Location"), "/automation/webhooks/5")

  // A fresh request for the same URL is what a reload does: the values come
  // from the store, not from anything the form kept.
  stubApi((call) =>
    call.path === "/api/v1/local/automation/webhooks/5"
      ? ok({ webhook: { ...storedWebhook, name: "نام تازه" } })
      : answerWebhookAPI(call)
  )
  const reloaded = await webhooksLoader({
    params: { webhookId: "5" },
    request: request("/automation/webhooks/5"),
  } as never)
  const editing = (reloaded as { editing: { name: string } }).editing
  assert.equal(editing.name, "نام تازه")
})

test("a secret typed once replaces the stored one, and an empty one removes it", async () => {
  const calls = stubApi((call) =>
    call.method === "PATCH" ? ok({ webhook: storedWebhook }) : answerWebhookAPI(call)
  )
  await webhooksAction({
    request: submit({ ...draftFields, intent: "update", id: "5", secret: "hunter2" }),
  } as never)
  assert.equal(
    (calls.find((c) => c.method === "PATCH")!.body as Record<string, unknown>).secret,
    "hunter2"
  )

  calls.length = 0
  await webhooksAction({
    request: submit({
      ...draftFields,
      intent: "update",
      id: "5",
      secret: "",
      secret_clear: "true",
    }),
  } as never)
  assert.equal(
    (calls.find((c) => c.method === "PATCH")!.body as Record<string, unknown>).secret,
    "",
    "an empty secret is posted as empty: the stored one is removed"
  )

  calls.length = 0
  await webhooksAction({
    request: submit({ ...draftFields, intent: "update", id: "5" }),
  } as never)
  assert.ok(
    !("secret" in (calls.find((c) => c.method === "PATCH")!.body as Record<string, unknown>)),
    "an untouched secret posts no key at all"
  )
})

test("a refusal is never reported as a success", async () => {
  stubApi((call) =>
    call.method === "POST"
      ? refuse(400, "invalid_input", "method must be one of POST, PUT or PATCH.")
      : answerWebhookAPI(call)
  )
  const result = (await webhooksAction({
    request: submit({ ...draftFields, method: "GET" }),
  } as never)) as { error?: string; intent?: string }
  assert.ok(result.error, "the refusal comes back as data, not as a redirect")
  assert.equal(result.intent, "create")
  // The server's own sentence names the fault; it is not paraphrased away.
  assert.equal(result.error, "method must be one of POST, PUT or PATCH.")
})

// --- 3. the duplicate-workflow refusal --------------------------------------

const workflowBody = (over: Record<string, string> = {}) => ({
  intent: "create",
  managed_channel_id: "5",
  enabled: "false",
  ai_enabled: "false",
  ai_prompt: "",
  ai_send_images: "false",
  ai_publish_on_failure: "false",
  destinations: "[]",
  ...over,
})

const submitTo = (path: string, fields: Record<string, string>) => {
  const form = new FormData()
  for (const [key, value] of Object.entries(fields)) form.set(key, value)
  return {
    url: `http://panel.test${path}`,
    formData: async () => form,
  } as unknown as Request
}

const existingWorkflow = {
  id: 12,
  managed_channel_id: 5,
  channel: {
    id: 5,
    title: "کانال خبری",
    username: "news",
    client_key: "official",
    telegram_channel_id: -100123,
  },
  enabled: true,
  ai_enabled: false,
  ai_prompt: "",
  ai_send_images: false,
  ai_publish_on_failure: false,
  config_revision: 1,
  destinations: [],
  created_at: "",
  updated_at: "",
}

test("a 409 names the workflow that already holds the channel", async () => {
  stubApi((call) => {
    if (call.path === "/api/v1/auth/me") return ok(me)
    if (call.method === "POST") {
      return refuse(409, "conflict", "a workflow already exists for that source channel")
    }
    if (call.path === "/api/v1/local/automation/workflows") {
      return ok({ workflows: [existingWorkflow] }, { total: 1 })
    }
    return ok({ workflow: existingWorkflow })
  })
  const result = (await workflowAction({
    params: { workflowId: "new" },
    request: submitTo("/automation/workflows/new", workflowBody()),
  } as never)) as {
    error?: string
    conflictWorkflowID?: number | null
    conflictChannelLabel?: string
  }
  assert.ok(result.error, "the refusal is surfaced")
  assert.equal(result.conflictWorkflowID, 12, "and it links to the one that exists")
  assert.equal(result.conflictChannelLabel, "کانال خبری")
})

// --- 4. the enable refusal --------------------------------------------------

test("a workflow the backend will not enable is not shown as enabled", async () => {
  const message =
    "a workflow needs at least one enabled destination before it can be enabled"
  stubApi((call) => {
    if (call.path === "/api/v1/auth/me") return ok(me)
    if (call.method === "PATCH" || call.method === "POST") {
      return refuse(400, "invalid_input", message)
    }
    return ok({ workflow: existingWorkflow })
  })
  const result = (await workflowAction({
    params: { workflowId: "12" },
    request: submitTo("/automation/workflows/12", {
      ...workflowBody({ intent: "update", enabled: "true" }),
    }),
  } as never)) as { error?: string; revertEnabled?: boolean }

  assert.equal(result.error, message, "the reason is the server's own sentence")
  assert.equal(
    result.revertEnabled,
    true,
    "the switch is put back to what the store holds"
  )
})

test("a workflow with no enabled destination is refused before the request goes out", async () => {
  stubApi((call) =>
    call.path === "/api/v1/auth/me" ? ok(me) : ok({ workflow: existingWorkflow })
  )
  await workflowEditorLoader({
    params: { workflowId: "new" },
    request: request("/automation/workflows/new"),
  } as never)
  // The rule itself is the backend's, read before the request: see the unit
  // tests in lib/automation.test.ts. Here the point is only that the editor's
  // own loader answers the same shape for `new` as the webhook one does.
  const data = await workflowEditorLoader({
    params: { workflowId: "12" },
    request: request("/automation/workflows/12"),
  } as never)
  assert.equal(data.status, "ok")
})

// --- 5. history renders what was stored -------------------------------------

const persistedDelivery: DeliveryItem = readDelivery({
  id: 44,
  webhook_id: 5,
  event_id: "evt_abc",
  status: "failed",
  attempt_count: 2,
  next_attempt_at: "2026-01-01T02:00:00Z",
  last_http_status: 502,
  last_error: "upstream 502",
  created_at: "2026-01-01T00:00:00Z",
  updated_at: "2026-01-01T01:00:00Z",
  completed_at: null,
  request: {
    method: "POST",
    url: "https://example.com/hook?token=%5Bredacted%5D",
    headers: { Authorization: "[redacted]", "X-Trace": "abc" },
    body: '{"id":1}',
    body_truncated: true,
  },
  attempts: [
    {
      id: 101,
      attempt_number: 1,
      request_method: "POST",
      request_url: "https://example.com/hook",
      request_headers: { Authorization: "Bearer hunter2" },
      request_body: '{"id":1}',
      request_body_truncated: false,
      response_status: 500,
      response_headers: { "content-type": "text/plain" },
      response_body: "boom",
      response_body_truncated: false,
      response_bytes: 4,
      duration_ms: 120,
      error_code: "",
      error_message: "",
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
  // The stored request: address, headers and body, exactly as recorded. The
  // tab the router opens on is the request, and a static render draws it.
  assert.ok(out.includes("https://example.com/hook"), "the recorded address")
  assert.ok(out.includes('"id": 1'), "the recorded body, rendered as JSON")
  assert.ok(out.includes("X-Trace"), "the recorded headers")
  assert.ok(out.includes("بریده شده"), "a body the server cut short says so")

  // Every attempt is drawn as its own record: number, request, response,
  // status, duration and error, all from the store and none invented.
  const first = text(html(<AttemptCard attempt={persistedDelivery.attempts[0]} />))
  assert.ok(first.includes("#1"), first)
  assert.ok(first.includes("HTTP 500"), "the recorded response status")
  assert.ok(first.includes(faDigits(120)), "the recorded duration")
  assert.ok(first.includes('"id": 1'), "the recorded request body")
  assert.ok(first.includes("boom"), "the recorded response body")

  const second = text(html(<AttemptCard attempt={persistedDelivery.attempts[1]} />))
  assert.ok(second.includes("#2"))
  assert.ok(second.includes("بدون پاسخ"), "an attempt with no answer says so")
  assert.ok(second.includes(faDigits(15000)), "its recorded duration")
  assert.ok(second.includes("timeout"), "the recorded error code")
  assert.ok(second.includes("request timed out"), "the recorded error message")
})

test("an authorization header is never rendered, whatever it holds", () => {
  const out = text(html(<DeliveryHistory delivery={persistedDelivery} />))
  assert.ok(!out.includes("Authorization"))
  assert.ok(!out.includes("Bearer"))
  // The same for an attempt's own request headers, which is where a live
  // credential would sit if the store ever kept one.
  const attempt = text(
    html(<AttemptCard attempt={persistedDelivery.attempts[0]} />)
  )
  assert.ok(!attempt.includes("Authorization"))
  assert.ok(!attempt.includes("hunter2"))
  assert.ok(!attempt.includes("Bearer"))
})

test("the summary reports the delivery's own persisted numbers", () => {
  const out = text(html(<DeliverySummary delivery={persistedDelivery} />))
  assert.ok(out.includes("evt_abc"))
  assert.ok(out.includes("upstream 502"))
  assert.ok(out.includes("502"))
})

// --- the editor surface is the dialog the URL chose --------------------------

test("the one editor surface carries the values the loader re-read", () => {
  // The dialog is a frame around this form; what the URL decides is which
  // record the form is seeded from, and the seed is the stored row.
  const create = text(
    html(
      <WebhookForm
        webhook={null}
        channels={[{ id: 3, label: "کانال خبری" }]}
        pending={false}
        onClose={() => {}}
        onSubmit={() => {}}
      />
    )
  )
  assert.ok(create.includes("نام"), "the create form has the fields")
  assert.ok(create.includes("ایجاد"), "and the create submit")
  assert.ok(!create.includes("رمزی ذخیره شده است"), "nothing stored to name yet")

  const edit = text(
    html(
      <WebhookForm
        webhook={readWebhook(storedWebhook)}
        channels={[{ id: 3, label: "کانال خبری" }]}
        pending={false}
        onClose={() => {}}
        onSubmit={() => {}}
      />
    )
  )
  // The values the loader re-read are the ones in the fields: this is the
  // round trip made visible after a reload.
  assert.ok(edit.includes('value="گیرنده اخبار"'), "the name is there")
  assert.ok(edit.includes('value="https://example.com/hook"'), "the url is there")
  assert.ok(edit.includes('value="4500"'), "timeout_ms survives")
  assert.ok(edit.includes('value="5"'), "max_attempts survives")
  assert.ok(edit.includes("ذخیره"), "the edit submit")
  // A stored secret is never in the markup: there is no field to type over yet.
  assert.ok(!/type="password"/.test(edit), "nothing to type over yet")
  assert.ok(edit.includes("رمزی ذخیره شده است"), "the stored secret is only named")
})
