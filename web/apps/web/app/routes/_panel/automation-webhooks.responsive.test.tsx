import assert from "node:assert/strict"
import { test } from "node:test"

import { MemoryRouter } from "react-router"
import { renderToStaticMarkup } from "react-dom/server"

import { WebhooksTable } from "@/components/automation/webhooks-table"
import { DeliveriesTable } from "./automation-deliveries"
import { WorkflowsTable } from "@/components/automation/workflows-table"
import { RunsTable } from "./automation-runs"
import { faDigits } from "@/components/persian-digits"
import {
  runDurationMs,
  type DeliveryItem,
  type RunItem,
  type WebhookItem,
  type WorkflowItem,
} from "@/lib/automation"

const routed = (node: React.ReactNode) =>
  renderToStaticMarkup(
    <MemoryRouter initialEntries={["/automation"]}>{node}</MemoryRouter>
  )

const tags = (markup: string, name: "th" | "td"): string[] => {
  const out: string[] = []
  let i = 0
  while ((i = markup.indexOf(`<${name} `, i)) !== -1) {
    const end = markup.indexOf(">", i)
    out.push(markup.slice(i, end))
    i = end
  }
  return out
}
const classes = (attrs: string) =>
  (/class="([^"]*)"/.exec(attrs)?.[1] ?? "").split(/\s+/)

function assertTableShape(markup: string, label: string) {
  // Every header and cell is one line: the container scrolls, the page does
  // not, and the shared Table owns the only scroller.
  const cells = [...tags(markup, "th"), ...tags(markup, "td")].map(classes)
  assert.ok(cells.length > 0, label)
  for (const list of cells) {
    assert.ok(list.includes("whitespace-nowrap"), `${label}: ${list.join(" ")}`)
    assert.ok(!list.includes("whitespace-normal"), label)
  }
  assert.equal(
    markup.split("overflow-x-auto").length - 1,
    1,
    `${label}: one scroll container`
  )
  assert.match(markup, /data-slot="table-container"[^>]*overflow-x-auto/)

  // The actions column is pinned to the physical left over an opaque
  // background, so the rows cannot show through it.
  const sticky = [...tags(markup, "th"), ...tags(markup, "td")]
    .map(classes)
    .filter((list) => list.includes("sticky"))
  assert.ok(sticky.length >= 2, `${label}: header and cells are pinned`)
  for (const list of sticky) {
    assert.ok(list.includes("left-0"), `${label}: physical left`)
    assert.ok(!list.includes("right-0"), `${label}: never the physical right`)
    assert.ok(list.includes("bg-background"), `${label}: opaque pin`)
  }
  assert.ok(classes(tags(markup, "th").at(-1)!).includes("z-20"), label)
  assert.ok(classes(tags(markup, "td").at(-1)!).includes("z-10"), label)
}

const webhook: WebhookItem = {
  id: 7,
  name: "گیرنده اخبار",
  description: "",
  enabled: true,
  url: "https://example.com/a-very-long-path/that/keeps/going/for/a/while",
  method: "POST",
  authMode: "hmac",
  authHeader: "",
  hasSecret: true,
  timeoutMs: 1000,
  maxAttempts: 3,
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
}

test("the webhook table is one scroller with a pinned actions column", () => {
  const markup = routed(
    <WebhooksTable
      webhooks={[webhook]}
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
  assertTableShape(markup, "webhooks")
  for (const text of [
    "نام",
    "کانال‌های مبدأ",
    "نشانی مقصد",
    "روش",
    "شیوه احراز",
    "وضعیت",
    "آخرین تحویل",
    "نتیجه",
  ]) {
    assert.ok(markup.includes(text), text)
  }
  assert.ok(markup.includes("گیرنده اخبار"))
  assert.ok(markup.includes("کانال خبری، کانال ورزشی"))
  assert.ok(markup.includes("امضای HMAC"))
  // The URL is a Latin address and reads left to right.
  assert.match(markup, /dir="ltr"[^>]*>https:\/\/example\.com/)
  // A secret is never in a list row.
  assert.ok(!/secret/i.test(markup), "no secret in the table")
})

const delivery: DeliveryItem = {
  id: 44,
  webhookID: 7,
  eventID: "evt_abc",
  status: "failed",
  attemptCount: 2,
  nextAttemptAt: null,
  lastHttpStatus: 500,
  lastError: "upstream 500",
  createdAt: "2026-01-01T00:00:00Z",
  updatedAt: "2026-01-01T01:00:00Z",
  completedAt: "2026-01-01T01:00:00Z",
  request: {
    method: "POST",
    url: "https://example.com/hook",
    headers: { Authorization: "[redacted]" },
    body: "{}",
    truncated: false,
  },
  attempts: [],
}

test("the delivery history is one scroller with a pinned actions column", () => {
  const markup = routed(<DeliveriesTable deliveries={[delivery]} webhookID={7} />)
  assertTableShape(markup, "deliveries")
  for (const text of [
    "وضعیت",
    "شناسه تحویل",
    "رویداد",
    "کد HTTP",
    "زمان",
    "مدت",
    "تلاش‌ها",
    "خطا",
  ]) {
    assert.ok(markup.includes(text), text)
  }
  assert.ok(markup.includes("evt_abc"))
  // Redaction markers travel into the row untouched.
  assert.ok(!/Authorization/.test(markup), "no credential column")
  // A list row carries no per-attempt timings, so the duration shown is the one
  // the store's own timestamps give and nothing more.
  assert.ok(markup.includes(faDigits(3_600_000)), "the stored interval is shown")
})

const workflow: WorkflowItem = {
  id: 12,
  managedChannelID: 5,
  channel: {
    id: 5,
    title: "کانال خبری",
    username: "news",
    clientKey: "official",
    channelID: -100123,
  },
  enabled: true,
  aiEnabled: true,
  aiPrompt: "",
  aiSendImages: false,
  aiPublishOnFailure: true,
  configRevision: 1,
  destinations: [
    {
      id: 1,
      type: "telegram",
      clientKey: "official",
      channelID: -100999,
      siteID: 0,
      options: {},
      position: 0,
      enabled: true,
    },
    {
      id: 2,
      type: "wordpress",
      clientKey: "",
      channelID: 0,
      siteID: 4,
      options: {},
      position: 1,
      enabled: true,
    },
  ],
  createdAt: "2026-01-01T00:00:00Z",
  updatedAt: "2026-01-02T00:00:00Z",
  lastRun: {
    id: 90,
    status: "partially_published",
    createdAt: "2026-01-03T00:00:00Z",
    completedAt: null,
  },
}

test("the workflow table is one scroller with a pinned actions column", () => {
  const markup = routed(
    <WorkflowsTable
      workflows={[workflow]}
      channelTitles={new Map([[5, "کانال خبری"]])}
      canManage
      onDelete={() => {}}
      onToggle={() => {}}
    />
  )
  assertTableShape(markup, "workflows")
  for (const text of [
    "کانال مبدأ",
    "وضعیت",
    "هوش مصنوعی",
    "مقاصد",
    "آخرین اجرا",
    "نتیجه",
  ]) {
    assert.ok(markup.includes(text), text)
  }
  assert.ok(markup.includes("کانال خبری"))
  assert.ok(markup.includes("بخشی منتشر شد"))
})

const run: RunItem = {
  id: 300,
  workflowID: 12,
  eventID: "evt_run",
  status: "published",
  sourceMessageID: 8899,
  originalContent: "متن اصلی",
  polishedContent: "متن ویرایش‌شده",
  aiStatus: "succeeded",
  aiFallbackUsed: false,
  aiModel: "gpt-test",
  aiErrorCode: "",
  aiErrorMessage: "",
  aiInputTokens: 120,
  aiOutputTokens: 45,
  aiDurationMs: 2100,
  configRevision: 3,
  attemptCount: 1,
  lastErrorCode: "",
  lastErrorMessage: "",
  createdAt: "2026-01-01T00:00:00Z",
  startedAt: "2026-01-01T00:00:01Z",
  completedAt: "2026-01-01T00:00:05Z",
  publications: [
    {
      id: 1,
      type: "telegram",
      clientKey: "official",
      channelID: -100999,
      siteID: 0,
      status: "published",
      remoteMessageID: 555,
      remotePostID: 0,
      remoteURL: "",
      attemptCount: 1,
      lastErrorCode: "",
      lastErrorMessage: "",
      completedAt: "2026-01-01T00:00:04Z",
    },
  ],
}

test("the run history is one scroller with a pinned actions column", () => {
  const markup = routed(<RunsTable runs={[run]} workflowID={12} />)
  assertTableShape(markup, "runs")
  for (const text of [
    "پیام مبدأ",
    "زمان دریافت",
    "وضعیت",
    "نتیجه هوش مصنوعی",
    "بازگشت به محتوای اصلی",
    "نتیجه مقاصد",
    "مدت",
  ]) {
    assert.ok(markup.includes(text), text)
  }
  // The source message id is a number and reads left to right.
  assert.match(markup, /dir="ltr"[^>]*>8899</)
  assert.ok(markup.includes("منتشر شد"))
})

test("a run's duration is the wall clock of the run, and absent while it runs", () => {
  assert.equal(runDurationMs(run), 5000)
  assert.equal(runDurationMs({ ...run, completedAt: null }), null)
  assert.equal(
    runDurationMs({ ...run, completedAt: "1999-01-01T00:00:00Z" }),
    null,
    "a clock that runs backwards is reported as unknown, not negative"
  )
})
