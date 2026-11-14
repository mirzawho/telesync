import assert from "node:assert/strict"
import { test } from "node:test"

import {
  aiStatusLabels,
  automationApi,
  automationFormLinkState,
  automationReturnTo,
  automationRoute,
  bodyView,
  bodyViewOf,
  canRetryDelivery,
  deliveryStatusLabels,
  destinationTypeLabels,
  emptyWebhookDraft,
  emptyWorkflowDraft,
  explainAutomationError,
  headerLines,
  hiddenHeaderCount,
  isCredentialHeader,
  isDuplicateWorkflow,
  isEnableRefusal,
  enableRefusalReasonOf,
  methodLabels,
  readAIStatus,
  readAuthMode,
  readDelivery,
  readDeliveryStatus,
  readDestinationType,
  readMethod,
  readPublicationStatus,
  readRun,
  readRunStatus,
  readStatus,
  statusRecord,
  readWebhook,
  readWorkflow,
  runStatusLabels,
  webhookBody,
  webhookDraftOf,
  workflowBody,
  workflowDraftOf,
  workflowEnableBlocker,
  type DeliveryItem,
  type RunItem,
  type WebhookItem,
  type WorkflowItem,
} from "@/lib/automation"

// --- wire mapping -----------------------------------------------------------

const wireWebhook = {
  id: 7,
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
  allow_localhost: false,
  source_channel_ids: [3, 9],
  created_at: "2026-01-01T00:00:00Z",
  updated_at: "2026-01-02T00:00:00Z",
  last_delivery: {
    status: "failed",
    http_status: 502,
    at: "2026-01-03T00:00:00Z",
    attempt_count: 3,
  },
  summary: { succeeded: 4, failed: 1, pending: 2 },
}

test("readWebhook maps every documented field", () => {
  const item = readWebhook(wireWebhook)
  assert.deepEqual(item, {
    id: 7,
    // The wire record carries no account, so it reads as not bound — never as
    // "whatever account is handy".
    accountID: "",
    name: "گیرنده اخبار",
    description: "ارسال رویدادها",
    enabled: true,
    url: "https://example.com/hook",
    method: "PUT",
    authMode: "hmac",
    authHeader: "",
    hasSecret: true,
    timeoutMs: 4500,
    maxAttempts: 5,
    allowLocalhost: false,
    sourceChannelIds: [3, 9],
    createdAt: "2026-01-01T00:00:00Z",
    updatedAt: "2026-01-02T00:00:00Z",
    lastDelivery: {
      status: "failed",
      httpStatus: 502,
      at: "2026-01-03T00:00:00Z",
      attemptCount: 3,
    },
    summary: { succeeded: 4, failed: 1, pending: 2 },
  })
})

test("a webhook model never carries a plaintext secret", () => {
  const item = readWebhook(wireWebhook) as unknown as Record<string, unknown>
  assert.equal(item.has_secret, undefined, "the wire name is not on the model")
  assert.ok(!("secret" in item), "no secret field at all")
  assert.ok(!("secret_ciphertext" in item))
  const serialized = JSON.stringify(item)
  assert.ok(!/secret["']?\s*:/.test(serialized), "nothing secret-shaped in JSON")
})

test("a summary the API omits is read as not reported, not as zeroes", () => {
  const item = readWebhook({ id: 1, name: "x" })
  assert.equal(item.summary, null)
  assert.equal(item.lastDelivery, null)
  assert.deepEqual(item.sourceChannelIds, [])
})

const wireDelivery = {
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
    headers: { "X-Signature": "[redacted]", "Content-Type": "application/json" },
    body: '{"id":1,"secret":"[redacted]"}',
    body_truncated: true,
  },
  attempts: [
    {
      id: 101,
      attempt_number: 1,
      request_method: "POST",
      request_url: "https://example.com/hook",
      request_headers: { Authorization: "[redacted]" },
      request_body: "{}",
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
      request_headers: { Authorization: "[redacted]" },
      request_body: "{}",
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
}

test("readDelivery keeps the persisted request and every attempt record", () => {
  const delivery = readDelivery(wireDelivery) as DeliveryItem
  assert.equal(delivery.id, 44)
  assert.equal(delivery.webhookID, 7)
  assert.equal(delivery.eventID, "evt_abc")
  assert.equal(delivery.attemptCount, 2)
  assert.equal(delivery.nextAttemptAt, null)
  assert.equal(delivery.completedAt, "2026-01-01T01:00:00Z")
  assert.equal(delivery.attempts.length, 2)
  assert.equal(delivery.attempts[0]?.attemptNumber, 1)
  assert.equal(delivery.attempts[0]?.responseStatus, 500)
  assert.equal(delivery.attempts[1]?.errorCode, "timeout")
  assert.equal(delivery.attempts[1]?.finishedAt, null)
})

test("redaction and truncation survive the mapping untouched", () => {
  const delivery = readDelivery(wireDelivery)
  assert.equal(delivery.request?.headers["X-Signature"], "[redacted]")
  assert.equal(delivery.request?.truncated, true)
  assert.equal(delivery.request?.body, '{"id":1,"secret":"[redacted]"}')
  assert.equal(delivery.attempts[0]?.request.headers.Authorization, "[redacted]")
  // Nothing in the mapping rewrites a marker back into a value.
  for (const attempt of delivery.attempts) {
    for (const value of Object.values(attempt.request.headers)) {
      assert.ok(!/^[A-Za-z0-9_-]{20,}$/.test(value), value)
    }
  }
})

test("a delivery with no history still reads cleanly", () => {
  const delivery = readDelivery({ id: 1, status: "pending" })
  assert.deepEqual(delivery.attempts, [])
  assert.equal(delivery.request, null)
  assert.equal(delivery.status, "pending")
})

const wireWorkflow = {
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
  ai_enabled: true,
  ai_prompt: "لحن رسمی",
  ai_send_images: false,
  ai_publish_on_failure: true,
  config_revision: 3,
  destinations: [
    {
      id: 1,
      destination_type: "telegram",
      account_id: "acc_destination",
      client_key: "official",
      channel_id: -100999,
      site_id: 0,
      options: { silent: true },
      position: 0,
      enabled: true,
    },
    {
      id: 2,
      destination_type: "wordpress",
      account_id: "",
      client_key: "",
      channel_id: 0,
      site_id: 4,
      options: {},
      position: 1,
      enabled: false,
    },
  ],
  created_at: "2026-01-01T00:00:00Z",
  updated_at: "2026-01-02T00:00:00Z",
  last_run: {
    id: 90,
    status: "partially_published",
    created_at: "2026-01-03T00:00:00Z",
    completed_at: null,
  },
}

test("readWorkflow maps the source channel, AI flags and destinations", () => {
  const workflow = readWorkflow(wireWorkflow) as WorkflowItem
  assert.equal(workflow.managedChannelID, 5)
  assert.equal(workflow.channel?.title, "کانال خبری")
  assert.equal(workflow.aiEnabled, true)
  assert.equal(workflow.aiPublishOnFailure, true)
  assert.equal(workflow.configRevision, 3)
  assert.deepEqual(
    workflow.destinations.map((destination) => [destination.type, destination.channelID, destination.siteID]),
    [
      ["telegram", -100999, 0],
      ["wordpress", 0, 4],
    ]
  )
  assert.equal(workflow.lastRun?.status, "partially_published")
})

test("the source channel's two ids are read apart and never mixed up", () => {
  const workflow = readWorkflow(wireWorkflow) as WorkflowItem
  // `id` is the local managed_channels.id the permissions are decided on;
  // `channelID` is the Telegram peer id. The wire name of the peer id is
  // telegram_channel_id, and reading the wrong one would show a local row id
  // where a Telegram id belongs.
  assert.equal(workflow.channel?.id, 5, "the local managed channel id")
  assert.equal(workflow.channel?.channelID, -100123, "the Telegram peer id")
  assert.equal(workflow.channel?.clientKey, "official")
  assert.equal(workflow.channel?.username, "news")
})

test("a channel the server did not report is null, never a zero row", () => {
  assert.equal(readWorkflow({ id: 1, managed_channel_id: 5 }).channel, null)
  assert.equal(readWorkflow({ id: 1, channel: {} }).channel, null)
})

test("workflowBody writes exactly one address per destination", () => {
  const draft = workflowDraftOf(readWorkflow(wireWorkflow))
  const body = workflowBody(draft) as {
    managed_channel_id: number
    destinations: Record<string, unknown>[]
  }
  assert.equal(body.managed_channel_id, 5)
  const [telegram, wordpress] = body.destinations
  assert.equal(telegram?.destination_type, "telegram")
  assert.equal(telegram?.client_key, "official")
  assert.equal(telegram?.channel_id, -100999)
  assert.equal(telegram?.site_id, 0, "a telegram row never names a site")
  assert.equal(telegram?.position, 0)
  assert.equal(wordpress?.destination_type, "wordpress")
  assert.equal(wordpress?.site_id, 4)
  assert.equal(wordpress?.channel_id, 0, "a wordpress row never names a channel")
  assert.equal(wordpress?.client_key, "", "a wordpress row never names a client")
  assert.equal(wordpress?.position, 1)
})

test("an empty workflow draft posts defaults: AI off, one source unset", () => {
  const body = workflowBody({
    managedChannelID: null,
    enabled: true,
    aiEnabled: false,
    aiPrompt: "  ",
    aiSendImages: false,
    aiPublishOnFailure: false,
    destinations: [],
  }) as Record<string, unknown>
  assert.equal(body.ai_enabled, false)
  assert.equal(body.ai_send_images, false)
  assert.equal(body.ai_publish_on_failure, false)
  assert.equal(body.ai_prompt, "")
  assert.deepEqual(body.destinations, [])
})

const wireRun = {
  id: 300,
  workflow_id: 12,
  event_id: "evt_run",
  status: "published_with_fallback",
  source_message_id: 8899,
  original_content: "متن اصلی",
  polished_content: "متن ویرایش‌شده",
  ai_status: "fallback_used",
  ai_fallback_used: true,
  ai_model: "gpt-test",
  ai_error_code: "ai_timeout",
  ai_error_message: "پاسخ نرسید",
  ai_input_tokens: 120,
  ai_output_tokens: 45,
  ai_duration_ms: 2100,
  config_revision: 3,
  attempt_count: 1,
  last_error_code: "",
  last_error_message: "",
  created_at: "2026-01-01T00:00:00Z",
  started_at: "2026-01-01T00:00:01Z",
  completed_at: "2026-01-01T00:00:05Z",
  publications: [
    {
      id: 1,
      destination_type: "telegram",
      account_id: "acc_destination",
      client_key: "official",
      channel_id: -100999,
      site_id: 0,
      status: "published",
      remote_message_id: 555,
      remote_post_id: 0,
      remote_url: "",
      attempt_count: 1,
      last_error_code: "",
      last_error_message: "",
      completed_at: "2026-01-01T00:00:04Z",
    },
    {
      id: 2,
      destination_type: "wordpress",
      account_id: "",
      client_key: "",
      channel_id: 0,
      site_id: 4,
      status: "failed",
      remote_message_id: 0,
      remote_post_id: 0,
      remote_url: "",
      attempt_count: 2,
      last_error_code: "wp_unreachable",
      last_error_message: "سایت در دسترس نبود",
      completed_at: null,
    },
  ],
}

test("readRun keeps original and polished content apart and every publication", () => {
  const run = readRun(wireRun) as RunItem
  assert.equal(run.originalContent, "متن اصلی")
  assert.equal(run.polishedContent, "متن ویرایش‌شده")
  assert.notEqual(run.originalContent, run.polishedContent)
  assert.equal(run.aiStatus, "fallback_used")
  assert.equal(run.aiFallbackUsed, true)
  assert.equal(run.aiInputTokens, 120)
  assert.equal(run.aiOutputTokens, 45)
  assert.equal(run.aiDurationMs, 2100)
  assert.equal(run.publications.length, 2)
  assert.equal(run.publications[0]?.remoteMessageID, 555)
  assert.equal(run.publications[0]?.remoteURL, "")
  assert.equal(run.publications[1]?.lastErrorCode, "wp_unreachable")
  assert.equal(run.publications[1]?.completedAt, null)
})

test("readStatus maps queue health and recent failures", () => {
  const status = readStatus({
    enabled: true,
    workers: 2,
    pending_jobs: 5,
    retry_backlog: 1,
    processing: 3,
    failed_recent: 2,
    recent_failures: [
      {
        job_type: "webhook_delivery",
        target_id: 44,
        error_code: "timeout",
        error_message: "timed out",
        at: "2026-01-01T00:00:00Z",
      },
      {
        job_type: "workflow_run",
        target_id: 300,
        error_code: "ai_timeout",
        error_message: "ai timed out",
        at: "2026-01-01T01:00:00Z",
      },
    ],
  })
  assert.equal(status.pendingJobs, 5)
  assert.equal(status.retryBacklog, 1)
  assert.deepEqual(
    status.recentFailures.map((failure) => failure.jobType),
    ["webhook_delivery", "workflow_run"]
  )
})

// --- enums and labels -------------------------------------------------------

test("every enum value has a Persian label", () => {
  for (const [value, label] of Object.entries(deliveryStatusLabels)) {
    assert.equal(value, readDeliveryStatus(value))
    assert.ok(label.length > 0, value)
  }
  for (const [value, label] of Object.entries(runStatusLabels)) {
    assert.equal(value, readRunStatus(value))
    assert.ok(label.length > 0, value)
  }
  for (const [value, label] of Object.entries(aiStatusLabels)) {
    assert.equal(value, readAIStatus(value))
    assert.ok(label.length > 0, value)
  }
  for (const [value, label] of Object.entries(destinationTypeLabels)) {
    assert.equal(value, readDestinationType(value))
    assert.ok(label.length > 0, value)
  }
  assert.deepEqual(
    Object.keys(methodLabels).sort(),
    ["PATCH", "POST", "PUT"]
  )
})

test("an unknown enum value falls back instead of inventing a status", () => {
  assert.equal(readDeliveryStatus("exploded"), "pending")
  assert.equal(readRunStatus("exploded"), "queued")
  assert.equal(readAIStatus("exploded"), "not_used")
  assert.equal(readPublicationStatus("exploded"), "unknown")
  assert.equal(readMethod("DELETE"), "POST")
  assert.equal(readAuthMode("oauth"), "none")
  assert.equal(readDestinationType("bluesky"), "telegram")
})

// --- retry eligibility ------------------------------------------------------

test("manual retry is offered only for a terminal failure", () => {
  const cases: [DeliveryItem["status"], boolean][] = [
    ["failed", true],
    ["pending", false],
    ["delivering", false],
    ["succeeded", false],
    ["cancelled", false],
  ]
  for (const [status, expected] of cases) {
    assert.equal(
      canRetryDelivery({ status }),
      expected,
      `${status} should ${expected ? "" : "not "}offer retry`
    )
  }
})

// --- secrets ----------------------------------------------------------------

test("a create payload omits the secret until the operator types one", () => {
  const draft = emptyWebhookDraft()
  draft.name = "نام"
  draft.url = "https://example.com/hook"
  assert.equal(draft.secret, null)
  const body = webhookBody(draft) as Record<string, unknown>
  assert.ok(!("secret" in body), "an untouched secret field posts nothing")
  draft.secret = "typed-once"
  assert.equal(
    (webhookBody(draft) as Record<string, unknown>).secret,
    "typed-once"
  )
})

test("editing a webhook never seeds the secret field from the response", () => {
  const draft = webhookDraftOf(readWebhook(wireWebhook))
  assert.equal(draft.secret, null)
  assert.ok(
    !("secret_value" in (draft as unknown as Record<string, unknown>)),
    "the draft carries no value field of any other name"
  )
  assert.equal(draft.name, "گیرنده اخبار")
  assert.equal(draft.authMode, "hmac")
  assert.deepEqual(draft.sourceChannelIds, [3, 9])
})

test("the custom header is sent only for the header auth mode", () => {
  const draft = emptyWebhookDraft()
  draft.authMode = "bearer"
  draft.authHeader = "X-Token"
  assert.equal((webhookBody(draft) as Record<string, unknown>).auth_header, "")
  draft.authMode = "header"
  assert.equal(
    (webhookBody(draft) as Record<string, unknown>).auth_header,
    "X-Token"
  )
})

// --- recorded bodies --------------------------------------------------------

test("a JSON body is rendered as readable JSON", () => {
  const view = bodyViewOf('{"a":1,"b":[2,3]}', false)
  assert.equal(view.json, true)
  assert.equal(view.empty, false)
  assert.equal(view.text, '{\n  "a": 1,\n  "b": [\n    2,\n    3\n  ]\n}')
})

test("a non-JSON body is shown verbatim and marked as such", () => {
  const view = bodyViewOf("<html>err</html>", true)
  assert.equal(view.json, false)
  assert.equal(view.truncated, true)
  assert.equal(view.text, "<html>err</html>")
})

test("an empty body is marked empty and never rendered as {}", () => {
  const view = bodyView({ headers: {}, body: "   ", truncated: false })
  assert.equal(view.empty, true)
  assert.equal(view.json, false)
  assert.equal(view.text, "")
})

test("a redacted value is rendered exactly as stored, marker and all", () => {
  assert.deepEqual(
    headerLines({ "X-Hook-Mark": "[redacted]", "X-Trace": "abc" }),
    ["X-Hook-Mark: [redacted]", "X-Trace: abc"]
  )
})

test("a header that carries a credential is never in the rendered lines", () => {
  for (const name of ["Authorization", "authorization", "X-Api-Key", "Cookie"]) {
    assert.deepEqual(
      headerLines({ [name]: "hunter2", "X-Trace": "abc" }),
      ["X-Trace: abc"],
      `${name} is withheld`
    )
    assert.equal(hiddenHeaderCount({ [name]: "hunter2", "X-Trace": "abc" }), 1)
    assert.ok(isCredentialHeader(name))
  }
  assert.equal(isCredentialHeader("X-Trace"), false)
})

// --- refusals ---------------------------------------------------------------

test("the duplicate-workflow refusal is recognised and named", () => {
  assert.equal(isDuplicateWorkflow({ code: "conflict" }), true)
  assert.equal(isDuplicateWorkflow({ code: "not_found" }), false)
  assert.match(explainAutomationError({ code: "conflict" }), /فقط یک ورک‌فلو/)
})

test("a missing public base URL explains what to configure", () => {
  const message = explainAutomationError({ code: "public_base_url_required" })
  assert.match(message, /public_base_url/)
  assert.match(message, /وب‌هوک/)
})

test("an unlisted refusal keeps the server's own sentence", () => {
  assert.equal(
    explainAutomationError({ code: "network_error", message: "ارتباط برقرار نشد." }),
    "ارتباط برقرار نشد."
  )
  assert.equal(explainAutomationError(new Error("x")), "x")
  assert.equal(explainAutomationError(null), "عملیات انجام نشد.")
})

// --- paths ------------------------------------------------------------------

test("the API paths the screens call are the documented ones", () => {
  assert.equal(automationApi.status, "/local/automation/status")
  assert.equal(automationApi.webhooks, "/local/automation/webhooks")
  assert.equal(automationApi.webhook(7), "/local/automation/webhooks/7")
  assert.equal(
    automationApi.deliveries(7),
    "/local/automation/webhooks/7/deliveries"
  )
  assert.equal(
    automationApi.delivery(7, 44),
    "/local/automation/webhooks/7/deliveries/44"
  )
  assert.equal(
    automationApi.retryDelivery(7, 44),
    "/local/automation/webhooks/7/deliveries/44/retry"
  )
  assert.equal(automationApi.testWebhook(7), "/local/automation/webhooks/7/test")
  assert.equal(automationApi.workflows, "/local/automation/workflows")
  assert.equal(automationApi.workflow(12), "/local/automation/workflows/12")
  assert.equal(automationApi.runs(12), "/local/automation/workflows/12/runs")
  assert.equal(
    automationApi.run(12, 300),
    "/local/automation/workflows/12/runs/300"
  )
})

test("the panel routes are router paths, without the /admin basename", () => {
  for (const path of [
    automationRoute.root,
    automationRoute.webhooks,
    automationRoute.newWebhook,
    automationRoute.editWebhook(7),
    automationRoute.deliveries(7),
    automationRoute.delivery(7, 44),
    automationRoute.workflows,
    automationRoute.newWorkflow,
    automationRoute.editWorkflow(12),
    automationRoute.workflow(12),
    automationRoute.runs(12),
    automationRoute.run(12, 300),
  ]) {
    assert.ok(path.startsWith("/"), path)
    assert.ok(!path.startsWith("/admin"), "basename is added by the router")
    assert.ok(!/^https?:/.test(path))
  }
  assert.equal(automationRoute.newWebhook, "/automation/webhooks/new")
  assert.equal(automationRoute.editWebhook(42), "/automation/webhooks/42/edit")
  assert.equal(automationRoute.newWorkflow, "/automation/workflows/new")
  assert.equal(automationRoute.editWorkflow(42), "/automation/workflows/42/edit")
})

// --- the way back to the listing ---------------------------------------------
//
// A form that was opened from the listing returns to the exact view it left —
// the tab, the query, the page — and one opened straight from the address bar
// has no such history and falls back to the collection's own tab. The listing
// builds `from`; every form reads it through the same two helpers, so the pair
// cannot drift apart.

test("a form opened from the listing goes back to the view it left", () => {
  const from = "/automation?tab=webhooks&q=%D8%AE%D8%A8%D8%B1&page=2"
  assert.deepEqual(automationFormLinkState(from), { from })
  assert.equal(
    automationReturnTo(automationFormLinkState(from), "webhooks"),
    from,
    "the query and the page come back with it"
  )
  assert.equal(
    automationReturnTo(automationFormLinkState("/automation?tab=workflows"), "workflows"),
    "/automation?tab=workflows"
  )
})

test("a form opened from an address bar falls back to its own tab", () => {
  for (const state of [undefined, null, {}, { from: 7 }, { from: "" }]) {
    assert.equal(automationReturnTo(state, "webhooks"), "/automation?tab=webhooks")
    assert.equal(automationReturnTo(state, "workflows"), "/automation?tab=workflows")
  }
})

test("a `from` that is not this area is never followed", () => {
  // The listing is the only thing that builds one, but a crafted or stale
  // state must not be able to send a save anywhere else.
  assert.equal(
    automationReturnTo({ from: "https://evil.example/collect" }, "webhooks"),
    "/automation?tab=webhooks"
  )
  assert.equal(
    automationReturnTo({ from: "//evil.example/collect" }, "webhooks"),
    "/automation?tab=webhooks"
  )
  assert.equal(
    automationReturnTo({ from: "/administrators" }, "webhooks"),
    "/automation?tab=webhooks"
  )
})

// --- the persistence contract ------------------------------------------------
//
// The wire payload and the model it is read back into are the two halves of one
// round trip. These tests pin both halves together, so a rename on either side
// breaks here rather than in production as a field that silently stops saving.

test("a webhook save posts exactly the documented fields, in snake_case", () => {
  const body = webhookBody({
    accountID: "acc_01hxyzabcdefghijklmnopqrstuv",
    name: "  گیرنده  ",
    description: " توضیح ",
    enabled: true,
    url: " https://example.com/hook ",
    method: "PUT",
    authMode: "header",
    authHeader: " X-Api-Key ",
    secret: null,
    timeoutMs: 4500,
    maxAttempts: 5,
    sourceChannelIds: [3, 9],
  }) as Record<string, unknown>
  assert.deepEqual(Object.keys(body).sort(), [
    "account_id",
    "allow_localhost",
    "auth_header",
    "auth_mode",
    "description",
    "enabled",
    "max_attempts",
    "method",
    "name",
    "source_channel_ids",
    "timeout_ms",
    "url",
  ])
  assert.equal(
    body.account_id,
    "acc_01hxyzabcdefghijklmnopqrstuv",
    "the account the webhook observes under"
  )
  assert.equal(body.name, "گیرنده", "trimmed on the way out")
  assert.equal(body.url, "https://example.com/hook")
  assert.equal(body.auth_header, "X-Api-Key")
  assert.equal(body.timeout_ms, 4500)
  assert.equal(body.max_attempts, 5)
  assert.deepEqual(body.source_channel_ids, [3, 9])
  assert.ok(!("secret" in body), "an untouched secret posts no key at all")
})

test("the secret rules are exactly: absent leaves it, empty removes it, a value replaces it", () => {
  const draft = emptyWebhookDraft()
  // null — the operator did not touch the field. Nothing at all is posted, so
  // the stored secret stays as it is.
  assert.ok(!("secret" in webhookBody({ ...draft, secret: null })))
  // "" — the operator asked for it to be removed. The key is posted, empty.
  assert.equal(
    (webhookBody({ ...draft, secret: "" }) as Record<string, unknown>).secret,
    ""
  )
  // A value — the operator typed a replacement. The plaintext goes out once and
  // is never read back.
  assert.equal(
    (webhookBody({ ...draft, secret: "hunter2" }) as Record<string, unknown>)
      .secret,
    "hunter2"
  )
})

test("a webhook edit never carries the stored secret back into the form", () => {
  const item = readWebhook({ ...wireWebhook, has_secret: true })
  const draft = webhookDraftOf(item)
  assert.equal(draft.secret, null, "untouched: the stored one stays")
  const body = webhookBody(draft) as Record<string, unknown>
  assert.ok(!("secret" in body), "saving without typing posts no secret key")
})

test("a webhook round-trips: what is posted is what is read back", () => {
  const draft = {
    name: "گیرنده",
    description: "ارسال رویدادها",
    enabled: true,
    url: "https://example.com/hook",
    method: "PATCH" as const,
    authMode: "bearer" as const,
    authHeader: "",
    secret: null,
    timeoutMs: 4500,
    maxAttempts: 5,
    sourceChannelIds: [3, 9],
  }
  const body = webhookBody(draft) as Record<string, unknown>
  // What the store keeps, rendered the way the gateway renders it.
  const stored = readWebhook({ id: 7, ...body, has_secret: false })
  const back = webhookDraftOf(stored)
  assert.equal(back.name, draft.name)
  assert.equal(back.description, draft.description)
  assert.equal(back.enabled, draft.enabled)
  assert.equal(back.url, draft.url)
  assert.equal(back.method, draft.method)
  assert.equal(back.authMode, draft.authMode)
  assert.equal(back.authHeader, draft.authHeader)
  assert.equal(back.timeoutMs, draft.timeoutMs)
  assert.equal(back.maxAttempts, draft.maxAttempts)
  assert.deepEqual(back.sourceChannelIds, draft.sourceChannelIds)
  assert.equal(back.secret, null, "never read back, in either direction")
})

test("a workflow save posts managed_channel_id as a local id and one address per destination", () => {
  const body = workflowBody({
    managedChannelID: 5,
    accountID: "acc_01hxyzabcdefghijklmnopqrstuv",
    enabled: false,
    aiEnabled: true,
    aiPrompt: "  لحن رسمی ",
    aiSendImages: true,
    aiPublishOnFailure: true,
    destinations: [
      {
        type: "telegram",
        accountID: "acc_destination",
        clientKey: "official",
        channelID: -100999,
        siteID: null,
        options: { silent: true },
        enabled: true,
      },
      {
        type: "wordpress",
        accountID: "",
        clientKey: "",
        channelID: null,
        siteID: 4,
        options: {},
        enabled: true,
      },
    ],
  }) as Record<string, unknown>
  assert.deepEqual(Object.keys(body).sort(), [
    "account_id",
    "ai_enabled",
    "ai_prompt",
    "ai_publish_on_failure",
    "ai_send_images",
    "destinations",
    "enabled",
    "managed_channel_id",
  ])
  assert.equal(
    body.account_id,
    "acc_01hxyzabcdefghijklmnopqrstuv",
    "the account the workflow runs as"
  )
  assert.equal(body.managed_channel_id, 5, "the local managed_channels.id")
  const destinations = body.destinations as Record<string, unknown>[]
  // A telegram destination is addressed by client_key + Telegram channel_id;
  // a wordpress one by site_id. The schema refuses a row that mixes them.
  assert.deepEqual(destinations[0], {
    destination_type: "telegram",
    account_id: "acc_destination",
    client_key: "official",
    channel_id: -100999,
    site_id: 0,
    options: { silent: true },
    position: 0,
    enabled: true,
  })
  assert.deepEqual(destinations[1], {
    destination_type: "wordpress",
    account_id: "",
    client_key: "",
    channel_id: 0,
    site_id: 4,
    options: {},
    position: 1,
    enabled: true,
  })
})

test("a destination's options survive an edit this panel has no editor for", () => {
  const workflow = readWorkflow(wireWorkflow)
  const draft = workflowDraftOf(workflow)
  assert.deepEqual(draft.destinations[0]?.options, { silent: true })
  const body = workflowBody(draft) as { destinations: Record<string, unknown>[] }
  assert.deepEqual(
    body.destinations[0]?.options,
    { silent: true },
    "saving must not erase what another client stored"
  )
})

test("a workflow round-trips: what is posted is what is read back", () => {
  const draft = {
    managedChannelID: 5,
    accountID: "acc_source",
    enabled: true,
    aiEnabled: true,
    aiPrompt: "لحن رسمی",
    aiSendImages: false,
    aiPublishOnFailure: true,
    destinations: [
      {
        type: "telegram" as const,
        accountID: "acc_destination",
        clientKey: "official",
        channelID: -100999,
        siteID: null,
        options: { silent: true },
        enabled: true,
      },
    ],
  }
  const body = workflowBody(draft) as Record<string, unknown>
  const stored = readWorkflow({ id: 12, ...body })
  const back = workflowDraftOf(stored)
  assert.equal(back.managedChannelID, draft.managedChannelID)
  assert.equal(back.enabled, draft.enabled)
  assert.equal(back.aiEnabled, draft.aiEnabled)
  assert.equal(back.aiPrompt, draft.aiPrompt)
  assert.equal(back.aiSendImages, draft.aiSendImages)
  assert.equal(back.aiPublishOnFailure, draft.aiPublishOnFailure)
  assert.deepEqual(back.destinations, draft.destinations)
})

// --- refusals the operator must see ------------------------------------------

test("a new workflow starts off: switching one on is its own step", () => {
  assert.equal(emptyWorkflowDraft().enabled, false)
})

test("a workflow with no enabled destination cannot be switched on", () => {
  const blocked = workflowEnableBlocker({
    destinations: [
      {
        type: "telegram",
        clientKey: "official",
        channelID: -100999,
        siteID: null,
        options: {},
        enabled: false,
      },
    ],
  })
  assert.ok(blocked, "every destination disabled is still nowhere to publish")
  assert.equal(
    workflowEnableBlocker({
      destinations: [
        {
          type: "telegram",
          accountID: "acc_destination",
          clientKey: "official",
          channelID: -100999,
          siteID: null,
          options: {},
          enabled: true,
        },
      ],
    }),
    null
  )
  assert.ok(workflowEnableBlocker({ destinations: [] }))
})

test("the backend's enable refusals are recognised and their reason kept", () => {
  const noDestination = {
    code: "invalid_input",
    message: "a workflow needs at least one enabled destination before it can be enabled",
  }
  const noOwner = {
    code: "invalid_input",
    message:
      "enabling a workflow needs an authenticated account: a workflow publishes under the authority of the account that created it.",
  }
  for (const refusal of [noDestination, noOwner]) {
    assert.equal(isEnableRefusal(refusal), true)
    assert.equal(enableRefusalReasonOf(refusal), refusal.message)
    assert.equal(
      explainAutomationError(refusal),
      refusal.message,
      "the reason is shown as the server wrote it, never paraphrased away"
    )
  }
  assert.equal(
    isEnableRefusal({ code: "invalid_input", message: "method must be one of POST, PUT or PATCH." }),
    false
  )
  assert.equal(
    isEnableRefusal({ code: "conflict", message: "a workflow already exists" }),
    false
  )
})

test("an invalid input refusal keeps the server's own sentence", () => {
  assert.equal(
    explainAutomationError({
      code: "invalid_input",
      message: "timeout_ms must be between 100 and 300000.",
    }),
    "timeout_ms must be between 100 and 300000."
  )
  assert.equal(
    explainAutomationError({
      code: "store_error",
      message: "the database is read-only",
    }),
    "the database is read-only"
  )
  // A code the panel speaks for keeps its own sentence when the server sent a
  // fixed generic one.
  assert.equal(
    explainAutomationError({ code: "not_found", message: "gone" }),
    "موردی با این شناسه پیدا نشد."
  )
  assert.equal(
    explainAutomationError({ code: "conflict" }),
    "برای این کانال ورک‌فلوی دیگری ثبت شده است. هر کانال فقط یک ورک‌فلو دارد؛ همان را ویرایش کنید."
  )
})

test("a 409 conflict is recognised as the one refusal with somewhere else to go", () => {
  assert.equal(isDuplicateWorkflow(new Error("nope") as unknown), false)
  assert.equal(isDuplicateWorkflow({ code: "conflict" }), true)
  assert.equal(isDuplicateWorkflow({ code: "invalid_input" }), false)
})

// The status route answers {data:{status:{…}}}. Reading the envelope as the
// record renders a disabled subsystem with empty queues even when the backend
// reports the opposite — the bug this pins.
test("the status envelope is unwrapped, so a live subsystem is not shown as disabled", () => {
  const data = {
    status: {
      enabled: true,
      workers: 2,
      pending_jobs: 3,
      retry_backlog: 1,
      processing: 1,
      failed_recent: 0,
      recent_failures: [],
    },
  }
  const status = readStatus(statusRecord(data))
  assert.equal(status.enabled, true)
  assert.equal(status.workers, 2)
  assert.equal(status.pendingJobs, 3)
  assert.equal(status.retryBacklog, 1)
  assert.equal(status.processing, 1)

  // And a missing or empty answer still renders as disabled rather than
  // throwing.
  assert.equal(readStatus(statusRecord(undefined)).enabled, false)
  assert.equal(readStatus(statusRecord({})).enabled, false)
})
