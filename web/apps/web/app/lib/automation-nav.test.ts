import assert from "node:assert/strict"
import { readFileSync } from "node:fs"
import { resolve } from "node:path"
import { test } from "node:test"

import { createElement, type ReactNode } from "react"
import { renderToStaticMarkup } from "react-dom/server"
import { MemoryRouter, matchRoutes, type RouteObject } from "react-router"

import {
  buildReport,
  globalPermissionLabels,
  manageNeedsView,
  normalizeDraft,
  serializeDraft,
  toggleGlobal,
  withViewDependencies,
} from "@/lib/admin-permissions"
import {
  navItemActive,
  navItemFor,
  navItems,
  overflowNavItems,
  primaryNavItems,
  visibleNavItems,
} from "@/lib/nav"
import {
  AutomationTabs,
  WebhooksPanel,
  WorkflowsPanel,
} from "@/routes/_panel/automation"
import type { WebhookItem, WorkflowItem } from "@/lib/automation"
import routeConfig from "@/routes"

// --- navigation -------------------------------------------------------------

test("the automation destination is the sixth nav item", () => {
  assert.deepEqual(
    navItems.map((item) => item.to),
    [
      "/dashboard",
      "/categories",
      "/channels",
      "/wordpress",
      "/administrators",
      "/automation",
    ]
  )
  const automation = navItems.find((item) => item.to === "/automation")
  assert.equal(automation?.label, "اتوماسیون")
  assert.equal(automation?.access, "automation")
})

test("the bar keeps five first-class destinations and overflows the rest", () => {
  assert.deepEqual(
    navItems.map((item) => item.placement),
    [
      "primary",
      "primary",
      "primary",
      "primary",
      "primary",
      "overflow",
    ]
  )
  const access = {
    channels: true,
    categories: true,
    users: true,
    automation: true,
  }
  assert.deepEqual(
    primaryNavItems(access).map((item) => item.to),
    [
      "/dashboard",
      "/categories",
      "/channels",
      "/wordpress",
      "/administrators",
    ]
  )
  assert.deepEqual(
    overflowNavItems(access).map((item) => item.to),
    ["/automation"]
  )
  // The two lists together are the navigation, in its one order.
  assert.deepEqual(
    [...primaryNavItems(access), ...overflowNavItems(access)].map(
      (item) => item.to
    ),
    navItems.map((item) => item.to)
  )
})

test("automation is shown only with its access flag", () => {
  const granted = visibleNavItems({
    channels: true,
    categories: true,
    users: true,
    automation: true,
  })
  assert.deepEqual(
    granted.map((item) => item.to),
    [
      "/dashboard",
      "/categories",
      "/channels",
      "/wordpress",
      "/administrators",
      "/automation",
    ]
  )
  const denied = visibleNavItems({
    channels: true,
    categories: true,
    users: true,
    automation: false,
  })
  assert.ok(!denied.some((item) => item.to === "/automation"))
  // The five existing destinations are untouched by the new flag.
  assert.deepEqual(
    denied.map((item) => item.to),
    ["/dashboard", "/categories", "/channels", "/wordpress", "/administrators"]
  )
  // Taking the flag away leaves the bar with nothing to overflow to.
  assert.deepEqual(
    overflowNavItems({
      channels: true,
      categories: true,
      users: true,
      automation: false,
    }),
    []
  )
})

test("a summary that omits the flag grants nothing, as everywhere else", () => {
  const items = visibleNavItems({
    channels: true,
    categories: true,
    users: true,
  })
  assert.ok(!items.some((item) => item.to === "/automation"))
  assert.deepEqual(
    overflowNavItems({ channels: true, categories: true, users: true }),
    []
  )
})

test("no access summary at all still shows the whole navigation", () => {
  assert.equal(visibleNavItems(undefined).length, navItems.length)
  assert.equal(visibleNavItems({}).length, 2, "only the unguarded items")
  assert.deepEqual(primaryNavItems({}).map((item) => item.to), [
    "/dashboard",
    "/wordpress",
  ])
})

test("every automation screen is under the automation destination", () => {
  for (const path of [
    "/automation",
    "/automation/webhooks",
    "/automation/webhooks/new",
    "/automation/webhooks/7",
    "/automation/webhooks/7/deliveries",
    "/automation/webhooks/7/deliveries/44",
    "/automation/workflows",
    "/automation/workflows/new",
    "/automation/workflows/12",
    "/automation/workflows/12/runs",
    "/automation/workflows/12/runs/300",
  ]) {
    assert.equal(navItemFor(path)?.to, "/automation", path)
    // The same answer marks the destination current, so a nested screen keeps
    // its section lit wherever the navigation is drawn.
    assert.equal(
      navItemActive(path, "/automation"),
      true,
      `${path} keeps the automation item current`
    )
  }
  // The old destinations keep matching their own subtree and nothing else.
  assert.equal(navItemFor("/channels/2/messages")?.to, "/channels")
  assert.equal(navItemFor("/administrators")?.to, "/administrators")
  assert.equal(navItemFor("/automatically") ?? null, null, "prefix match is exact")
  assert.equal(navItemActive("/automatically", "/automation"), false)
  assert.equal(navItemActive("/automation", "/automation"), true)
})

// --- the route table --------------------------------------------------------

// React Router's own matcher over the app's real route config: what resolves,
// and with which params. The tree carries each entry's file so a test can say
// which screen answered, not just that something did.
type ConfigEntry = {
  path?: string
  id?: string
  file: string
  children?: ConfigEntry[]
}
type TestRoute = { path?: string; id: string; file: string; children?: TestRoute[] }

const routeId = (entry: ConfigEntry) =>
  entry.id ?? entry.file.replace(/\.[a-z0-9]+$/i, "")

function toRouteTree(entries: readonly ConfigEntry[]): TestRoute[] {
  return entries.map((entry) => ({
    path: entry.path,
    id: routeId(entry),
    file: entry.file,
    children: entry.children ? toRouteTree(entry.children) : undefined,
  }))
}

const tree = toRouteTree(routeConfig)

// The panel is served under the basename react-router.config.ts declares
// (/admin/). The route table is written without it — the router adds it — so a
// test that matched the bare paths would not be answering the question a user
// asks, which is whether the address bar's URL resolves. The basename is read
// out of the config rather than repeated here, so the two cannot drift.
const basename =
  readFileSync(resolve(process.cwd(), "react-router.config.ts"), "utf8").match(
    /basename:\s*"([^"]+)"/
  )?.[1] ?? "/admin/"

// `url` is what the browser is given: the basename and all. `matchRoutes`'s own
// third argument is the same basename the app boots with.
function panelRoute(url: string) {
  const matches = matchRoutes(tree as RouteObject[], url, basename)
  assert.ok(matches && matches.length > 0, `${url} resolves without a 404`)
  const last = matches![matches!.length - 1]
  return {
    file: (last.route as unknown as TestRoute).file,
    params: last.params,
  }
}

test("the panel's basename is the one the app is served under", () => {
  assert.equal(basename, "/admin/")
})

// The URLs the automation area is specified by. Each must survive a refresh and
// a direct navigation, which is the same as saying each must match a route: the
// page is a single-page app and the server answers all of them with the shell.
const REQUIRED_URLS: [string, string, Record<string, string>][] = [
  ["/admin/automation", "routes/_panel/automation.tsx", {}],
  ["/admin/automation/webhooks", "routes/_panel/automation-webhooks.tsx", {}],
  [
    "/admin/automation/webhooks/new",
    "routes/_panel/automation-webhooks.tsx",
    { webhookId: "new" },
  ],
  [
    "/admin/automation/webhooks/7",
    "routes/_panel/automation-webhooks.tsx",
    { webhookId: "7" },
  ],
  [
    "/admin/automation/webhooks/7/deliveries",
    "routes/_panel/automation-deliveries.tsx",
    { webhookId: "7" },
  ],
  ["/admin/automation/workflows", "routes/_panel/automation-workflows.tsx", {}],
  [
    "/admin/automation/workflows/new",
    "routes/_panel/automation-workflow-editor.tsx",
    { workflowId: "new" },
  ],
  [
    "/admin/automation/workflows/12",
    "routes/_panel/automation-workflow-editor.tsx",
    { workflowId: "12" },
  ],
  [
    "/admin/automation/workflows/12/runs",
    "routes/_panel/automation-runs.tsx",
    { workflowId: "12" },
  ],
]

test("every required automation URL resolves through the real routes.ts", () => {
  for (const [url, file, params] of REQUIRED_URLS) {
    assert.deepEqual(panelRoute(url), { file, params }, url)
  }
})

test("the webhook create and edit screens answer from the one webhooks page", () => {
  assert.deepEqual(panelRoute("/admin/automation/webhooks"), {
    file: "routes/_panel/automation-webhooks.tsx",
    params: {},
  })
  // Creating is the same screen as editing: one module, two URLs, no second
  // copy of the form to keep in step. The create URL carries the one value the
  // page reads as "no webhook yet" (`creating = params.webhookId === "new"` in
  // automation-webhooks.tsx), so it is never taken for a record id.
  const create = panelRoute("/admin/automation/webhooks/new")
  assert.equal(create.file, "routes/_panel/automation-webhooks.tsx")
  assert.deepEqual(create.params, { webhookId: "new" })
  const edit = panelRoute("/admin/automation/webhooks/7")
  assert.equal(edit.file, "routes/_panel/automation-webhooks.tsx")
  assert.deepEqual(edit.params, { webhookId: "7" })
})

test("the workflow editor keeps its literal `new`, and the histories resolve", () => {
  assert.deepEqual(panelRoute("/admin/automation/workflows/new"), {
    file: "routes/_panel/automation-workflow-editor.tsx",
    params: { workflowId: "new" },
  })
  assert.deepEqual(panelRoute("/admin/automation/workflows/12"), {
    file: "routes/_panel/automation-workflow-editor.tsx",
    params: { workflowId: "12" },
  })
  assert.deepEqual(panelRoute("/admin/automation/workflows/12/runs"), {
    file: "routes/_panel/automation-runs.tsx",
    params: { workflowId: "12" },
  })
  assert.deepEqual(panelRoute("/admin/automation/workflows/12/runs/300"), {
    file: "routes/_panel/automation-run.tsx",
    params: { workflowId: "12", runId: "300" },
  })
  assert.deepEqual(panelRoute("/admin/automation/webhooks/7/deliveries"), {
    file: "routes/_panel/automation-deliveries.tsx",
    params: { webhookId: "7" },
  })
  assert.deepEqual(panelRoute("/admin/automation/webhooks/7/deliveries/44"), {
    file: "routes/_panel/automation-delivery.tsx",
    params: { webhookId: "7", deliveryId: "44" },
  })
})

test("every route keeps its own id, so two screens may share one module", () => {
  // The route manifest is keyed by id; two entries naming the same id stop the
  // build. Sharing a file is fine and is what keeps create and edit one screen.
  const ids: string[] = []
  const walk = (entries: readonly ConfigEntry[]) => {
    for (const entry of entries) {
      ids.push(routeId(entry))
      if (entry.children) walk(entry.children)
    }
  }
  walk(routeConfig)
  assert.equal(new Set(ids).size, ids.length, "route ids are unique")
})

// --- the landing page -------------------------------------------------------

// The area's front page is two tabs, one per collection. What is proved here is
// the markup the shared Tabs paints: the workspace has no DOM to click in, so
// the primitive's own selection is read off the attributes it renders. It
// paints only the open panel, which is also what pins the default down.

const webhookItem = (id: number, name: string): WebhookItem => ({
  id,
  name,
  description: "",
  enabled: true,
  url: "https://example.test/hooks/telesync",
  method: "POST",
  authMode: "hmac",
  authHeader: "X-Signature",
  hasSecret: true,
  timeoutMs: 5000,
  maxAttempts: 3,
  sourceChannelIds: [4],
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

const webhooksPreview = {
  rows: [webhookItem(1, "هوک رویدادها"), webhookItem(2, "هوک پشتیبان")],
  total: 7,
}
const workflowsPreview = {
  rows: [workflowItem(2, "کانال خبری")],
  total: 2,
}

// Links read the router, so the panels are rendered inside one at the area's
// own address.
const inRouter = (node: ReactNode, at = "/automation") =>
  renderToStaticMarkup(
    createElement(MemoryRouter, { initialEntries: [at] }, node)
  )

// The triggers in document order, as their opening tags. Base UI marks the
// selected one with a bare `data-active`, not Radix's `data-state`.
const tabTags = (markup: string) =>
  markup.match(/<button\b[^>]*role="tab"[^>]*>/g) ?? []

test("the landing page is two tabs and opens on the webhooks tab", () => {
  const markup = inRouter(
    createElement(AutomationTabs, {
      webhooks: webhooksPreview,
      workflows: workflowsPreview,
    })
  )
  const tabs = tabTags(markup)
  assert.equal(tabs.length, 2, "exactly the two collections, nothing more")
  assert.deepEqual(
    tabs.map((tag) =>
      markup.slice(markup.indexOf(tag) + tag.length).split("<")[0]
    ),
    ["وب‌هوک‌ها", "ورک‌فلوها"],
    "webhooks first, workflows beside it"
  )
  assert.match(tabs[0], /aria-selected="true"/)
  assert.match(tabs[0], /data-active=/)
  assert.match(tabs[1], /aria-selected="false"/)
  assert.ok(!/data-active=/.test(tabs[1]), "only one tab is selected")
  // Only the open panel is painted, so the webhooks panel being the one on
  // screen is the default being the webhooks tab.
  assert.match(markup, /href="\/automation\/webhooks\/new"/)
  assert.ok(
    !/href="\/automation\/workflows\/new"/.test(markup),
    "the closed tab is not painted, so webhooks is the default"
  )
})

test("the triggers keep the shared Tabs' selected background and shadow", () => {
  const markup = inRouter(
    createElement(AutomationTabs, {
      webhooks: webhooksPreview,
      workflows: workflowsPreview,
    })
  )
  for (const tag of tabTags(markup)) {
    const classes = /class="([^"]*)"/.exec(tag)?.[1] ?? ""
    for (const token of [
      "data-active:bg-background",
      "data-active:text-foreground",
      "data-active:shadow-sm",
      "dark:data-active:bg-input/30",
    ]) {
      assert.ok(classes.split(/\s+/).includes(token), `${token} is left alone`)
    }
  }
  // And nothing was passed that could have overridden them.
  const source = readFileSync(
    resolve(process.cwd(), "app/routes/_panel/automation.tsx"),
    "utf8"
  )
  assert.ok(
    !/<TabsTrigger[^>]*className=/.test(source),
    "the page never restyles a trigger"
  )
})

test("each tab carries its collection's records and the way into the section", () => {
  const hooks = inRouter(createElement(WebhooksPanel, { webhooks: webhooksPreview }))
  // A real count against the real list, not a placeholder.
  assert.match(hooks, /۷/)
  assert.match(hooks, /هوک رویدادها/)
  assert.match(hooks, /هوک پشتیبان/)
  for (const href of [
    "/automation/webhooks",
    "/automation/webhooks/new",
    "/automation/webhooks/1",
    "/automation/webhooks/2",
  ]) {
    assert.ok(hooks.includes(`href="${href}"`), `${href} is linked from the tab`)
  }

  const flows = inRouter(
    createElement(WorkflowsPanel, { workflows: workflowsPreview })
  )
  assert.match(flows, /۲/)
  assert.match(flows, /کانال خبری/)
  for (const href of [
    "/automation/workflows",
    "/automation/workflows/new",
    "/automation/workflows/2",
  ]) {
    assert.ok(flows.includes(`href="${href}"`), `${href} is linked from the tab`)
  }

  // The doors the tabs offer are the two collections the route table serves,
  // so a link from here is never a link to nowhere.
  panelRoute(`${basename.slice(0, -1)}/automation/webhooks`)
  panelRoute(`${basename.slice(0, -1)}/automation/workflows`)
})

test("an empty collection says so and still offers the way in", () => {
  const hooks = inRouter(
    createElement(WebhooksPanel, { webhooks: { rows: [], total: 0 } })
  )
  assert.match(hooks, /هنوز وب‌هوکی ثبت نشده است/)
  assert.match(hooks, /href="\/automation\/webhooks\/new"/)
  assert.ok(!/<ul/.test(hooks), "no empty list where the records would be")
})

// --- permissions ------------------------------------------------------------

test("the three automation permissions have Persian labels", () => {
  assert.equal(globalPermissionLabels["automation.view"], "مشاهده اتوماسیون")
  assert.equal(globalPermissionLabels["webhooks.manage"], "مدیریت وب‌هوک‌ها")
  assert.equal(globalPermissionLabels["workflows.manage"], "مدیریت ورک‌فلوها")
})

test("managing a webhook or a workflow implies viewing automation", () => {
  assert.equal(manageNeedsView["webhooks.manage"], "automation.view")
  assert.equal(manageNeedsView["workflows.manage"], "automation.view")
  assert.deepEqual(
    [...withViewDependencies(["webhooks.manage", "workflows.manage"])].sort(),
    ["automation.view", "webhooks.manage", "workflows.manage"]
  )
})

test("taking automation.view away takes both managing rights with it", () => {
  const draft = normalizeDraft({
    global: ["automation.view", "webhooks.manage", "workflows.manage"],
    categories: [],
    channels: [],
    wordpress: [],
  })
  assert.deepEqual(draft.globals, [
    "automation.view",
    "webhooks.manage",
    "workflows.manage",
  ])
  const next = toggleGlobal(draft, "automation.view")
  assert.deepEqual(next.globals, [])
})

test("a save keeps automation rights the dialog has no editor for", () => {
  const loaded = {
    global: ["automation.view", "webhooks.manage"],
    categories: [],
    channels: [],
    wordpress: [],
  }
  const draft = normalizeDraft(loaded)
  const form = serializeDraft(draft, {})
  assert.deepEqual(JSON.parse(form.global_permissions), [
    "automation.view",
    "webhooks.manage",
  ])
})

test("the report names the automation group", () => {
  const report = buildReport(
    normalizeDraft({
      global: ["automation.view", "workflows.manage"],
      categories: [],
      channels: [],
      wordpress: [],
    }),
    {}
  )
  const automation = report.groups.find((group) => group.key === "automation")
  assert.ok(automation, "the automation group is reported")
  assert.deepEqual(
    automation?.lines.map((line) => [line.key, line.state]),
    [
      ["automation.view", "allowed"],
      ["webhooks.manage", "denied"],
      ["workflows.manage", "allowed"],
    ]
  )
})
