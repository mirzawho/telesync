// The automation overview page against a stand-in of the real API.
//
// What is proved here is the page's contract: which collection it reads, what
// the URL decides, what the header and the toolbar offer, and that nothing of
// the queue status the page used to carry survives. The API is a stub, so the
// assertions are about the conversation — the paths and queries that actually
// go out — and about the markup the page paints.

import assert from "node:assert/strict"
import { readFileSync } from "node:fs"
import { resolve } from "node:path"
import { test } from "node:test"

import { MemoryRouter } from "react-router"
import { renderToStaticMarkup } from "react-dom/server"

import {
  AutomationAddButton,
  AutomationContent,
  AutomationPagination,
  AutomationToolbar,
  AutomationView,
  TAB_WEBHOOKS,
  TAB_WORKFLOWS,
  clientLoader,
  readTab,
  searchView,
  viewParams,
  type Listing,
} from "./automation"
import type { WebhookItem, WorkflowItem } from "@/lib/automation"

// --- harness ----------------------------------------------------------------

interface Call {
  method: string
  path: string
  query: string
}

// A window and a fetch that answer like the gateway: an envelope of {data,
// meta}, or {error:{code,message}} with the status the gateway would use. Every
// call is recorded, so a test can assert what was actually asked for.
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

const storedWebhook = {
  id: 5,
  name: "گیرنده اخبار",
  description: "",
  enabled: true,
  url: "https://example.com/hook",
  method: "POST",
  auth_mode: "hmac",
  auth_header: "",
  has_secret: true,
  timeout_ms: 4500,
  max_attempts: 5,
  source_channel_ids: [3],
  created_at: "2026-01-01T00:00:00Z",
  updated_at: "2026-01-02T00:00:00Z",
}

const storedWorkflow = {
  id: 12,
  managed_channel_id: 3,
  channel: {
    id: 3,
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
  destinations: [
    {
      id: 1,
      type: "telegram",
      client_key: "official",
      channel_id: 200,
      site_id: 0,
      options: {},
      position: 0,
      enabled: true,
    },
  ],
  created_at: "2026-01-01T00:00:00Z",
  updated_at: "2026-01-02T00:00:00Z",
}

// Both collections, the channels they name, and the queue counters the page is
// no longer allowed to ask for. A request for the status endpoint is answered
// but recorded, so a test can say it never went out.
function answerAll(call: Call) {
  if (call.path === "/api/v1/auth/me") return ok(me)
  if (call.path === "/api/v1/local/channels") {
    return ok({ channels: [{ id: 3, title: "کانال خبری", client_key: "official" }] })
  }
  if (call.path === "/api/v1/local/automation/status") {
    return ok({
      enabled: true,
      pending_jobs: 4,
      processing: 1,
      retry_backlog: 2,
      failed_recent: 3,
      workers: 2,
      recent_failures: [],
    })
  }
  if (call.path === "/api/v1/local/automation/webhooks") {
    return ok({ webhooks: [storedWebhook] }, { total: 7, total_pages: 4, has_more: true })
  }
  if (call.path === "/api/v1/local/automation/workflows") {
    return ok({ workflows: [storedWorkflow] }, { total: 2, total_pages: 1, has_more: false })
  }
  return refuse(404, "not_found", "no such route " + call.path)
}

// --- markup helpers ----------------------------------------------------------

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

// The tab triggers in document order, as their opening tags. Base UI marks the
// selected one with a bare `data-active`, not Radix's `data-state`.
const tabTags = (markup: string) =>
  markup.match(/<button\b[^>]*role="tab"[^>]*>/g) ?? []

const webhookItem = (id: number, name: string): WebhookItem => ({
  id,
  name,
  description: "",
  enabled: true,
  url: "https://example.test/hooks",
  method: "POST",
  authMode: "hmac",
  authHeader: "X-Signature",
  hasSecret: true,
  timeoutMs: 5000,
  maxAttempts: 3,
  allowLocalhost: false,
  sourceChannelIds: [3],
  createdAt: "2026-01-01T00:00:00Z",
  updatedAt: "2026-01-02T00:00:00Z",
  lastDelivery: {
    status: "succeeded",
    httpStatus: 200,
    at: "2026-01-02T00:00:00Z",
    attemptCount: 1,
  },
  summary: { succeeded: 12, failed: 1, pending: 2 },
})

const workflowItem = (id: number, title: string): WorkflowItem => ({
  id,
  managedChannelID: id,
  channel: {
    id,
    title,
    username: "telesync_channel",
    clientKey: "official",
    channelID: 100,
  },
  enabled: true,
  aiEnabled: true,
  aiPrompt: "",
  aiSendImages: false,
  aiPublishOnFailure: false,
  configRevision: 3,
  destinations: [
    {
      id: 1,
      type: "telegram",
      clientKey: "official",
      channelID: 200,
      siteID: 0,
      options: {},
      position: 0,
      enabled: true,
    },
  ],
  createdAt: "2026-01-01T00:00:00Z",
  updatedAt: "2026-01-02T00:00:00Z",
  lastRun: {
    id: 9,
    status: "published",
    createdAt: "2026-01-02T00:00:00Z",
    completedAt: "2026-01-02T00:00:01Z",
  },
})

const channels = [{ id: 3, label: "کانال خبری", clientKey: "official" }]

const webhooksPage = (over: Partial<Listing> = {}): Listing =>
  ({
    tab: TAB_WEBHOOKS,
    rows: [webhookItem(1, "هوک رویدادها"), webhookItem(2, "هوک پشتیبان")],
    q: "",
    page: 1,
    total: 7,
    totalPages: 4,
    hasMore: true,
    canManage: true,
    ...over,
  }) as Listing

const workflowsPage = (over: Partial<Listing> = {}): Listing =>
  ({
    tab: TAB_WORKFLOWS,
    rows: [workflowItem(2, "کانال خبری")],
    q: "",
    page: 1,
    total: 2,
    totalPages: 1,
    hasMore: false,
    canManage: true,
    ...over,
  }) as Listing

// A page exactly as it is drawn: the listing the loader answered with, the tab
// the URL says is open, and the search box's contents.
const page = (
  listing: Listing | null,
  tab: typeof TAB_WEBHOOKS | typeof TAB_WORKFLOWS = listing?.tab ?? TAB_WEBHOOKS,
  search = ""
) =>
  html(
    <AutomationView
      listing={listing}
      channels={channels}
      tab={tab}
      search={search}
      onTabChange={() => {}}
      onSearchChange={() => {}}
      hrefForPage={(n) => `?page=${n}`}
      onEdit={() => {}}
      onToggle={() => {}}
      onDelete={() => {}}
      onTest={() => {}}
    />
  )

// --- 1. what the page reads ---------------------------------------------------

test("the page reads the open collection only, with its own query", async () => {
  const calls = stubApi(answerAll)
  const data = await clientLoader({
    request: request("/automation?q=%D8%AE%D8%A8%D8%B1&page=2"),
  } as never)
  assert.equal(data.status, "ok")
  const listing = (data as { listing: Listing }).listing
  assert.equal(listing.tab, TAB_WEBHOOKS, "webhooks is the default")
  assert.equal(listing.q, "خبر")
  assert.equal(listing.page, 2)
  assert.equal(listing.rows.length, 1)
  assert.equal(listing.total, 7)
  assert.equal(listing.totalPages, 4, "the count is the backend's")
  assert.equal(listing.hasMore, true)

  const list = calls.find(
    (call) => call.path === "/api/v1/local/automation/webhooks"
  )!
  assert.match(list.query, /page=2/)
  assert.match(list.query, /per_page=20/)
  assert.match(list.query, /q=/)
  assert.ok(
    !calls.some((call) => call.path === "/api/v1/local/automation/workflows"),
    "the closed collection is never fetched"
  )
  assert.ok(
    !calls.some((call) => call.path === "/api/v1/local/automation/status"),
    "the queue is not read: this page has no queue section left"
  )
})

test("a search typed for one collection never reaches the other", async () => {
  const calls = stubApi(answerAll)
  const data = await clientLoader({
    request: request("/automation?tab=workflows&q=%D9%88%D8%B1%DA%A9"),
  } as never)
  assert.equal(data.status, "ok")
  const listing = (data as { listing: Listing }).listing
  assert.equal(listing.tab, TAB_WORKFLOWS)
  assert.equal(listing.rows.length, 1)

  const asked = calls.find(
    (call) => call.path === "/api/v1/local/automation/workflows"
  )!
  assert.match(asked.query, /q=/)
  assert.ok(
    !calls.some((call) => call.path === "/api/v1/local/automation/webhooks"),
    "the webhooks query is not sent for a workflow search"
  )
})

test("a tab the URL does not name falls back to webhooks", () => {
  assert.equal(readTab(null), TAB_WEBHOOKS)
  assert.equal(readTab("webhooks"), TAB_WEBHOOKS)
  assert.equal(readTab("workflows"), TAB_WORKFLOWS)
  assert.equal(readTab("queues"), TAB_WEBHOOKS)
})

test("an account without automation access is told so, not shown an empty page", async () => {
  stubApi((call) =>
    call.path === "/api/v1/auth/me"
      ? ok({ ...me, access: { ...me.access, automation: false } })
      : answerAll(call)
  )
  const data = await clientLoader({ request: request("/automation") } as never)
  assert.equal(data.status, "forbidden")
  assert.ok((data as { message: string }).message.includes("مجوز"))
})

test("a refused list is reported as the service reported it", async () => {
  stubApi((call) =>
    call.path === "/api/v1/local/automation/webhooks"
      ? refuse(500, "internal", "the store is unavailable")
      : answerAll(call)
  )
  const data = await clientLoader({ request: request("/automation") } as never)
  assert.equal(data.status, "error")
  assert.ok((data as { message: string }).message.includes("the store is unavailable"))
})

// --- 2. the address bar, the tab and the search -------------------------------

test("a new search starts at the first page and keeps its own tab", () => {
  const params = searchView(TAB_WORKFLOWS, "خبر")
  assert.equal(params.get("tab"), TAB_WORKFLOWS)
  assert.equal(params.get("q"), "خبر")
  assert.equal(params.get("page"), null, "a search is a new look at the collection")
})

test("changing tabs keeps each collection's own query and page", () => {
  const there = viewParams(TAB_WORKFLOWS, "ورک", 3)
  assert.equal(there.get("tab"), TAB_WORKFLOWS)
  assert.equal(there.get("q"), "ورک")
  assert.equal(there.get("page"), "3")
  // And the defaults are the ones left out, so a plain webhooks view is `/automation`.
  const home = viewParams(TAB_WEBHOOKS, "  ", 1)
  assert.equal(home.toString(), "")
})

test("the webhooks tab holds no tab parameter of its own", () => {
  assert.equal(viewParams(TAB_WEBHOOKS, "خبر", 1).get("tab"), null)
  assert.equal(viewParams(TAB_WEBHOOKS, "خبر", 1).get("q"), "خبر")
})

// --- 3. the header -------------------------------------------------------------

test("the page keeps its title and its one-line description", () => {
  const out = text(page(webhooksPage()))
  assert.ok(out.includes("اتوماسیون"), "the title is the area's own")
  assert.ok(
    out.includes("رویدادهای کانال‌ها را به وب‌هوک و ورک‌فلو بسپارید"),
    "and the subtitle that goes with it"
  )
  assert.ok(
    !/وضعیت صف/.test(out) && !/شکست‌های اخیر/.test(out),
    "no queue dashboard and no failures summary"
  )
  for (const gone of ["در صف", "در حال پردازش", "در انتظار تلاش دوباره", "پردازشگرها"]) {
    assert.ok(!out.includes(gone), `${gone} is gone with the queue section`)
  }
})

test("there is exactly one Add button, and it names the open tab", () => {
  const hooks = text(page(webhooksPage()))
  assert.equal(
    hooks.match(/افزودن/g)?.length,
    1,
    "one primary Add button, not one per section"
  )
  assert.ok(hooks.includes("افزودن وب‌هوک"))
  assert.ok(!hooks.includes("افزودن ورک‌فلو"), "the closed tab has no button here")

  const flows = text(page(workflowsPage(), TAB_WORKFLOWS))
  assert.ok(flows.includes("افزودن ورک‌فلو"))
  assert.ok(!flows.includes("افزودن وب‌هوک"))
})

test("the Add button goes to that collection's own create page", () => {
  const hooks = html(<AutomationAddButton tab={TAB_WEBHOOKS} canManage />)
  assert.match(hooks, /href="\/automation\/webhooks\/new"/)
  assert.ok(!/dialog|dialog/i.test(hooks), "a create is a page, not a dialog")

  const flows = html(<AutomationAddButton tab={TAB_WORKFLOWS} canManage />)
  assert.match(flows, /href="\/automation\/workflows\/new"/)
})

test("a read-only administrator is offered no Add button at all", () => {
  const out = html(<AutomationAddButton tab={TAB_WEBHOOKS} canManage={false} />)
  assert.equal(out, "")
})

test("every way into a form carries the view it was pressed from", () => {
  // A link's `state` is history state and never reaches the markup, so the
  // wiring is read where it is written: a form can only come back to the exact
  // listing view — the tab, the query, the page — if the link that opened it
  // carried it. `automationFormLinkState` → `automationReturnTo` is pinned in
  // app/lib/automation.test.ts; this pins that both halves are actually used.
  const wired = (file: string, needle: string) =>
    readFileSync(resolve(process.cwd(), file), "utf8").split(needle).length - 1

  assert.equal(
    wired(
      "app/routes/_panel/automation.tsx",
      "automationFormLinkState(location.pathname + location.search)"
    ),
    2,
    "the header's Add button and the row's Edit both pass the view they are on"
  )
  assert.equal(
    wired("app/components/automation/workflows-table.tsx", "state={back}"),
    1,
    "and so does the workflow row's own edit link"
  )
})

test("the doors into the whole collection are gone", () => {
  const hooks = text(page(webhooksPage()))
  assert.ok(!hooks.includes("همه وب‌هوک‌ها"))
  assert.ok(!hooks.includes("همه ورک‌فلوها"))
  assert.ok(!/href="\/automation\/webhooks"/.test(hooks))
  assert.ok(!/href="\/automation\/workflows"/.test(hooks))
})

// --- 4. the toolbar -----------------------------------------------------------

test("the toolbar is two tabs, webhooks first, and opens on webhooks", () => {
  const markup = page(webhooksPage())
  const tabs = tabTags(markup)
  assert.equal(tabs.length, 2, "exactly the two collections, nothing more")
  assert.deepEqual(
    tabs.map((tag) => markup.slice(markup.indexOf(tag) + tag.length).split("<")[0]),
    ["وب‌هوک‌ها", "ورک‌فلوها"]
  )
  assert.match(tabs[0], /aria-selected="true"/)
  assert.match(tabs[0], /data-active=/)
  assert.match(tabs[1], /aria-selected="false"/)
  assert.ok(!/data-active=/.test(tabs[1]), "only one tab is selected")
})

test("the tab list is only as wide as its two triggers", () => {
  const markup = page(webhooksPage())
  const list = /<div[^>]*data-slot="tabs-list"[^>]*>/.exec(markup)?.[0] ?? ""
  const classes = (/class="([^"]*)"/.exec(list)?.[1] ?? "").split(/\s+/)
  assert.ok(classes.includes("w-fit"), classes.join(" "))
  assert.ok(classes.includes("shrink-0"), classes.join(" "))
  assert.ok(!classes.includes("w-full"), "it must not stretch across the row")
})

test("the triggers keep the shared Tabs' selected background and shadow", () => {
  for (const tag of tabTags(page(webhooksPage()))) {
    const classes = (/class="([^"]*)"/.exec(tag)?.[1] ?? "").split(/\s+/)
    for (const token of [
      "data-active:bg-background",
      "data-active:text-foreground",
      "data-active:shadow-sm",
      "dark:data-active:bg-input/30",
    ]) {
      assert.ok(classes.includes(token), `${token} is left alone`)
    }
  }
})

test("the search box names the collection it searches", () => {
  assert.match(page(webhooksPage()), /placeholder="جستجو در وب‌هوک‌ها\.\.\."/)
  assert.match(
    page(workflowsPage(), TAB_WORKFLOWS),
    /placeholder="جستجو در ورک‌فلوها\.\.\."/
  )
})

test("the search box is beside the tabs, in one toolbar row", () => {
  const markup = page(webhooksPage())
  assert.match(
    markup,
    /<div class="flex items-center gap-2 sm:gap-3">/,
    "one row at any width: the tabs never wrap their labels and the field shrinks"
  )
  // The field flexes to the row without pushing the tab list across the page:
  // `min-w-0` is what lets it shrink instead of overflowing on a narrow screen.
  assert.match(markup, /<div class="w-full min-w-0 sm:max-w-sm">/)
})

// --- 5. the table --------------------------------------------------------------

test("the webhooks tab renders the whole webhook page as a table", () => {
  const out = text(page(webhooksPage()))
  assert.match(out, /data-slot="table"/)
  for (const heading of [
    "نام",
    "کانال‌های مبدأ",
    "نشانی مقصد",
    "روش",
    "شیوه احراز",
    "وضعیت",
    "آخرین تحویل",
    "نتیجه",
  ]) {
    assert.ok(out.includes(heading), heading)
  }
  assert.ok(out.includes("هوک رویدادها"))
  assert.ok(out.includes("هوک پشتیبان"))
})

test("the workflows tab renders the whole workflow page as a table", () => {
  const out = text(page(workflowsPage(), TAB_WORKFLOWS))
  assert.match(out, /data-slot="table"/)
  for (const heading of [
    "کانال مبدأ",
    "وضعیت",
    "هوش مصنوعی",
    "مقاصد",
    "آخرین اجرا",
    "نتیجه",
  ]) {
    assert.ok(out.includes(heading), heading)
  }
  assert.ok(out.includes("کانال خبری"))
})

test("a row still offers its own actions", () => {
  const out = text(page(webhooksPage()))
  assert.match(out, /aria-label="عملیات وب‌هوک هوک رویدادها"/)
  const flows = text(page(workflowsPage(), TAB_WORKFLOWS))
  assert.match(flows, /aria-label="عملیات ورک‌فلوی کانال خبری"/)
})

test("a search with no matches says so instead of an empty table", () => {
  const out = text(page(webhooksPage({ rows: [], q: "خبر" })))
  assert.ok(out.includes("نتیجه‌ای پیدا نشد"))
  assert.ok(!/data-slot="table"/.test(out), "no table of nothing")
})

test("an empty collection says so and still offers the way in", () => {
  const out = text(page(webhooksPage({ rows: [], total: 0, totalPages: 0 })))
  assert.ok(out.includes("هنوز وب‌هوکی وجود ندارد"))
  assert.ok(out.includes("افزودن وب‌هوک"), "the header's Add button is still there")
})

// --- 6. pagination -------------------------------------------------------------

test("the page reports where it is in the collection and what follows", () => {
  const out = text(page(webhooksPage()))
  assert.ok(out.includes("صفحه"), "the current page")
  assert.ok(out.includes("۷ رکورد"), "the backend's own total")
  assert.ok(out.includes("صفحه قبل"))
  assert.ok(out.includes("صفحه بعد"))
})

test("the first page has no way back and the last has no way on", () => {
  // A step that exists is a link to that page; a step that does not is a
  // button that cannot be pressed. The class list is dropped first: the shared
  // Button always carries `disabled:` variants, which say nothing about state.
  const controls = (markup: string) =>
    (markup.match(/<(?:button|a)\b[^>]*>/g) ?? []).map((tag) =>
      tag.replace(/ class="[^"]*"/, "")
    )

  const first = html(
    <AutomationPagination
      listing={webhooksPage()}
      hrefForPage={(n) => `?page=${n}`}
    />
  )
  const start = controls(first)
  assert.equal(start.length, 2, first)
  assert.match(start[0], /^<button/, "the way back is not a link on page one")
  assert.match(start[0], / disabled/, "and it cannot be pressed")
  assert.match(start[1], /^<a/, "the way on is a link")
  assert.ok(!/ disabled/.test(start[1]), start[1])

  const end = controls(
    html(
      <AutomationPagination
        listing={workflowsPage({ page: 1, hasMore: false })}
        hrefForPage={(n) => `?page=${n}`}
      />
    )
  )
  assert.match(end[1], /^<button/, "the way on is not a link at the end")
  assert.match(end[1], / disabled/, "and it cannot be pressed")
})

test("the next page keeps the query the table is showing", () => {
  const out = text(
    html(
      <AutomationContent
        listing={webhooksPage({ q: "خبر", page: 2 })}
        channels={channels}
        hrefForPage={(n) =>
          n === 3 ? "?q=%D8%AE%D8%A8%D8%B1&page=3" : "?q=%D8%AE%D8%A8%D8%B1"
        }
        onEdit={() => {}}
        onToggle={() => {}}
        onDelete={() => {}}
        onTest={() => {}}
      />
    )
  )
  assert.match(out, /href="[^"]*\?q=%D8%AE%D8%A8%D8%B1&page=3"/)
})

// --- 7. the responsive table shape --------------------------------------------

test("the table is one scroller with the actions pinned to the physical left", () => {
  // Raw markup, not the decoded text: the class lists carry entities, and a
  // structural assertion reads the tags as they are written.
  const out = page(webhooksPage())
  assert.match(
    out,
    /data-slot="table-container" class="[^"]*overflow-x-auto/,
    "the table scrolls sideways on a narrow screen and the page does not"
  )
  for (const tag of out.match(/<(?:th|td)\b[^>]*>/g) ?? []) {
    assert.ok(tag.includes("whitespace-nowrap"), tag)
  }
  const pinned = out.match(/<(?:th|td)\b[^>]*sticky[^>]*>/g) ?? []
  assert.ok(pinned.length >= 2, "header and cells are both pinned")
  for (const tag of pinned) {
    assert.ok(tag.includes("left-0"), tag)
    assert.ok(!tag.includes("right-0"), "never the physical right")
    assert.ok(tag.includes("bg-background"), "an opaque pin, not text over text")
  }
})

// --- 8. the open tab is the one on screen ---------------------------------------

test("while one tab's rows arrive, the other's are not shown under its heading", () => {
  // The loader is answering for the workflows tab and the webhooks tab is what
  // is open: the rows behind the open tab are still on their way.
  const out = text(page(workflowsPage(), TAB_WEBHOOKS))
  assert.ok(out.includes("در حال بارگذاری وب‌هوک‌ها"))
  assert.ok(!out.includes("کانال خبری"), "no workflow row under the webhooks heading")
})
