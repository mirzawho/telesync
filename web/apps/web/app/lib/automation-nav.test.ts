import assert from "node:assert/strict"
import { readFileSync } from "node:fs"
import { resolve } from "node:path"
import { test } from "node:test"

import { matchRoutes, type RouteObject } from "react-router"

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
import { clientLoader as legacyLoader } from "@/routes/_panel/automation-legacy-redirect"
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
  [
    "/admin/automation/webhooks/new",
    "routes/_panel/automation-webhook-form.tsx",
    {},
  ],
  [
    "/admin/automation/webhooks/7/edit",
    "routes/_panel/automation-webhook-form.tsx",
    { webhookId: "7" },
  ],
  [
    "/admin/automation/webhooks/7/deliveries",
    "routes/_panel/automation-deliveries.tsx",
    { webhookId: "7" },
  ],
  [
    "/admin/automation/workflows/new",
    "routes/_panel/automation-workflow-form.tsx",
    {},
  ],
  [
    "/admin/automation/workflows/12/edit",
    "routes/_panel/automation-workflow-form.tsx",
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

test("creating and editing are one form module on two routes", () => {
  // One form component, two URLs: `new` is the create page and `:id/edit` is
  // the same form seeded from the stored row. There is no second copy of the
  // form to keep in step, and `new` is a literal so it can never be read as a
  // record id.
  const create = panelRoute("/admin/automation/webhooks/new")
  assert.equal(create.file, "routes/_panel/automation-webhook-form.tsx")
  assert.deepEqual(create.params, {}, "`new` is the create page, not a record")

  const edit = panelRoute("/admin/automation/webhooks/7/edit")
  assert.equal(edit.file, "routes/_panel/automation-webhook-form.tsx")
  assert.deepEqual(edit.params, { webhookId: "7" })

  assert.equal(
    panelRoute("/admin/automation/workflows/new").file,
    "routes/_panel/automation-workflow-form.tsx"
  )
  assert.deepEqual(panelRoute("/admin/automation/workflows/12/edit").params, {
    workflowId: "12",
  })
})

test("the addresses these screens used to have still resolve, and point on", () => {
  // A bookmark, a link in a message and a browser's back button all keep
  // working: the bare collection and the bare record id are routes too, and
  // they answer with where that content lives now.
  const collection = panelRoute("/admin/automation/webhooks")
  assert.equal(collection.file, "routes/_panel/automation-legacy-redirect.tsx")
  assert.deepEqual(collection.params, {})

  const record = panelRoute("/admin/automation/webhooks/7")
  assert.equal(record.file, "routes/_panel/automation-legacy-redirect.tsx")
  assert.deepEqual(record.params, { webhookId: "7" })

  assert.equal(
    panelRoute("/admin/automation/workflows").file,
    "routes/_panel/automation-legacy-redirect.tsx"
  )
  assert.deepEqual(panelRoute("/admin/automation/workflows/12").params, {
    workflowId: "12",
  })
})

test("a legacy address answers with where the content lives now", async () => {
  // `requestURL` resolves a loader's request against the window's origin; a
  // loader under test needs the same one the browser would give it.
  ;(globalThis as Record<string, unknown>).window = {
    location: { origin: "http://panel.test" },
  }
  const to = async (url: string) => {
    const response = (await legacyLoader({
      request: new Request(`http://panel.test${url}`),
    } as never)) as Response
    return response.headers.get("Location")
  }
  assert.equal(await to("/automation/webhooks"), "/automation?tab=webhooks")
  assert.equal(await to("/automation/workflows"), "/automation?tab=workflows")
  assert.equal(await to("/automation/webhooks/7"), "/automation/webhooks/7/edit")
  assert.equal(
    await to("/automation/workflows/12"),
    "/automation/workflows/12/edit"
  )
})

test("an id that was never a record is reported, not turned into an editor", async () => {
  ;(globalThis as Record<string, unknown>).window = {
    location: { origin: "http://panel.test" },
  }
  const data = (await legacyLoader({
    request: new Request("http://panel.test/automation/webhooks/abc"),
  } as never)) as { status: string; collection: string }
  assert.equal(data.status, "missing")
  assert.equal(data.collection, "webhooks")
  assert.equal(
    (await legacyLoader({
      request: new Request("http://panel.test/automation/workflows/abc"),
    } as never) as { collection: string }).collection,
    "workflows"
  )
})

test("the histories resolve beside their record", () => {
  assert.deepEqual(panelRoute("/admin/automation/workflows/12/runs/300"), {
    file: "routes/_panel/automation-run.tsx",
    params: { workflowId: "12", runId: "300" },
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
