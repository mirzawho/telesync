// The webhook screens that the create and edit pages sit beside: the delivery
// history, its retry, and the list rows the automation overview draws.
//
// The forms themselves are proved in `webhook-form.test.tsx`; what is proved
// here is everything around them that still has to hold — what a recorded
// exchange renders, when a retry may be offered, and what a row of the
// collection says about the record it carries.

import assert from "node:assert/strict"
import { test } from "node:test"

import { MemoryRouter } from "react-router"
import { renderToStaticMarkup } from "react-dom/server"

import {
  AttemptCard,
  DeliveryActions,
  DeliveryHistory,
  DeliverySummary,
  retryOffered,
} from "../../routes/_panel/automation-delivery"
import {
  deliveryDurationMs,
  DeliveriesTable,
} from "../../routes/_panel/automation-deliveries"
import { WebhooksTable } from "@/components/automation/webhooks-table"
import { faDigits } from "@/components/persian-digits"
import {
  readDelivery,
  readWebhook,
  type DeliveryItem,
} from "@/lib/automation"

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

// A stored webhook exactly as the gateway renders it back. Nothing here is a
// secret: the row may name that one exists and never what it is.
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

// A delivery as the store holds it: two attempts, one answered and one not, and
// a body the server cut short.
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
  completed_at: "2026-01-01T01:00:00Z",
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

// --- the recorded exchange ---------------------------------------------------

test("the three tabs render the persisted request and every attempt", () => {
  const out = text(html(<DeliveryHistory delivery={persistedDelivery} />))
  for (const tab of ["درخواست", "پاسخ", "تلاش‌ها"]) {
    assert.ok(out.includes(tab), tab)
  }
  // The stored request: address, headers and body, exactly as recorded. The tab
  // the router opens on is the request, and a static render draws it.
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

test("an authorization header is never rendered and never copied", () => {
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

// --- manual retry -----------------------------------------------------------

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

// --- the collection's rows ---------------------------------------------------

test("a row names the values the store kept and never a secret", () => {
  const out = html(
    <WebhooksTable
      webhooks={[readWebhook(storedWebhook)]}
      channels={[
        { id: 3, label: "کانال خبری", clientKey: "official" },
        { id: 9, label: "کانال ورزشی", clientKey: "official" },
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

// --- durations -------------------------------------------------------------

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
