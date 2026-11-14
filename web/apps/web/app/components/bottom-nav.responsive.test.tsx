import assert from "node:assert/strict"
import { readFileSync } from "node:fs"
import { resolve } from "node:path"
import { test } from "node:test"

import { MemoryRouter } from "react-router"
import { renderToStaticMarkup } from "react-dom/server"

import { navItems, overflowNavItems, primaryNavItems } from "@/lib/nav"
import type { Me } from "@/lib/session"

import {
  BottomNav,
  BottomNavItem,
  BottomNavOverflow,
} from "@/components/bottom-nav"
import { SidebarNav } from "@/routes/_panel/layout"

const html = (node: React.ReactNode) => renderToStaticMarkup(node)
// NavLink reads the router; a memory router at the dashboard gives every case
// a current location. Paths here are the router's own (basename added by the
// app only).
const routed = (node: React.ReactNode, at = "/dashboard") =>
  renderToStaticMarkup(
    <MemoryRouter initialEntries={[at]}>{node}</MemoryRouter>
  )
// Tests run from the bundled copy in apps/web/.test-out-responsive; the app
// sources sit one level up. The scripts run node from apps/web.
const appDir = resolve(process.cwd(), "app")
const tokens = (className: string) =>
  new Set(className.split(/\s+/).filter(Boolean))
const navTag = (markup: string) => markup.match(/^<nav[^>]*>/)?.[0] ?? ""
const classOf = (tag: string) => tokens(tag.match(/class="([^"]*)"/)![1])
// The bar's own links come first; everything from the overflow panel on is the
// menu the "more" disclosure carries.
const barAnchors = (markup: string) =>
  (markup.split('id="bottom-nav-overflow"')[0].match(/<a [^>]*>/g) ?? []).map(
    (tag) => tag
  )
const overflowPanel = (markup: string) =>
  markup.slice(markup.indexOf('id="bottom-nav-overflow"'))
// The disclosure carries more than one destination now, so a test names the
// link it means instead of taking whichever comes first.
const panelLinkTo = (markup: string, href: string) =>
  overflowPanel(markup).match(new RegExp(`<a\\b[^>]*href="${href}"[^>]*>`))?.[0] ??
  ""
const triggerTag = (markup: string) =>
  markup.match(/<button[^>]*data-testid="bottom-nav-more"[^>]*>/)?.[0] ?? ""

const ALL = {
  channels: true,
  categories: true,
  users: true,
  servers: true,
  automation: true,
}
const RESTRICTED = {
  channels: false,
  categories: true,
  users: false,
  servers: false,
  automation: false,
}

// --- what the bar carries ---------------------------------------------------

test("full access: five destinations in the bar, the rest behind the overflow", () => {
  assert.deepEqual(
    primaryNavItems(ALL).map((item) => item.to),
    [
      "/dashboard",
      "/categories",
      "/channels",
      "/wordpress",
      "/administrators",
    ]
  )
  assert.deepEqual(
    overflowNavItems(ALL).map((item) => item.to),
    ["/accounts", "/automation"]
  )
  const markup = routed(<BottomNav access={ALL} />)
  assert.equal(barAnchors(markup).length, 5, "the bar is the five destinations")
  assert.ok(
    /data-testid="bottom-nav-more"/.test(markup),
    "the overflow disclosure is the sixth control"
  )
})

test("restricted access: only authorized destinations in the bar", () => {
  assert.deepEqual(
    primaryNavItems(RESTRICTED).map((item) => item.to),
    ["/dashboard", "/categories", "/wordpress"]
  )
  // Personal accounts belong to whoever is signed in, so they survive a
  // restricted summary; automation does not.
  assert.deepEqual(
    overflowNavItems(RESTRICTED).map((item) => item.to),
    ["/accounts"]
  )
  const markup = routed(<BottomNav access={RESTRICTED} />)
  assert.deepEqual(
    barAnchors(markup).map((tag) => tag.match(/href="([^"]*)"/)![1]),
    ["/dashboard", "/categories", "/wordpress"]
  )
  assert.ok(
    !/href="\/automation"/.test(markup),
    "hiding automation leaves the rest of the bar alone"
  )
  // The disclosure is rendered only when something is behind it, so a summary
  // with nothing to offer still leaves the bar at its destinations alone.
  assert.ok(
    /data-testid="bottom-nav-more"/.test(markup),
    "accounts is always there to reach"
  )
  assert.equal(panelLinkTo(markup, "/automation"), "")
})

test("automation is reachable from the bar without knowing a URL", () => {
  const markup = routed(<BottomNav access={ALL} />)
  const panel = overflowPanel(markup)
  const link = panelLinkTo(markup, "/automation")
  assert.match(link, /href="\/automation"/)
  assert.match(link, /aria-label="اتوماسیون"/)
  assert.match(link, /title="اتوماسیون"/)
  // A closed disclosure is display:none: out of the tab order and the
  // accessibility tree, but still the real link the menu opens onto.
  assert.match(panel, /class="[^"]*\bhidden\b[^"]*"/)
  const trigger = triggerTag(markup)
  assert.match(trigger, /aria-expanded="false"/)
  assert.match(trigger, /aria-controls="bottom-nav-overflow"/)
  assert.match(trigger, /aria-label="بیشتر"/)
})

test("the disclosure carries its destinations as 44px rows", () => {
  const markup = routed(<BottomNav access={ALL} />)
  const panel = overflowPanel(markup)
  assert.match(panel, /min-h-11/, "the row that navigates is a 44px target")
  assert.match(panel, /<svg/, "and it is not a bare text link")
  assert.match(panel, /اتوماسیون/)
  // Personal accounts are behind the same disclosure and are named there.
  assert.match(panelLinkTo(markup, "/accounts"), /aria-label="حساب‌های من"/)
})

// --- shape of the pill ------------------------------------------------------

test("the nav bar is floating, rounded, below lg and clears the safe area", () => {
  const nav = navTag(routed(<BottomNav access={ALL} />))
  assert.ok(nav, "nav element rendered")
  assert.match(nav, /aria-label="ناوبری اصلی"/)
  const cls = classOf(nav)
  assert.ok(cls.has("fixed"))
  assert.ok(cls.has("rounded-full"))
  assert.ok(cls.has("z-40"))
  assert.ok(cls.has("lg:hidden"))
  assert.ok(cls.has("inset-x-4"))
  assert.ok(cls.has("bottom-[calc(1rem+env(safe-area-inset-bottom))]"))
  assert.ok(!cls.has("bottom-4"), "safe-area bottom replaces plain bottom-4")
  assert.ok(!cls.has("md:hidden"), "the bar is governed by lg, not md")
})

// Five 44px destinations and the one compact disclosure are what the pill has
// to hold. The bar is `inset-x-4`, so its width is the viewport minus 32px,
// capped at 420px; `p-2` takes 16px of that and `gap-1` takes 4px per gap. This
// pins the arithmetic to the tokens that produce it, and to the narrow-screen
// guard that keeps it true on a 320px viewport.
test("five icons and the compact overflow fit the pill: 2×8 + 5×44 + 36 + 5×4 = 292px", () => {
  const markup = routed(<BottomNav access={ALL} />)
  const cls = classOf(navTag(markup))
  const px = (token: string) => {
    const size = /^p-(\d)$/.exec(token)?.[1] ?? /^gap-(\d)$/.exec(token)?.[1]
    return size === undefined ? null : Number(size) * 0.25 * 16
  }
  const padding = px("p-2")
  const gap = px("gap-1")
  assert.ok(cls.has("p-2"), "the pill's own padding")
  assert.ok(cls.has("gap-1"), "the pill's own gap")
  assert.ok(cls.has("max-w-[420px]"), "the pill is capped, not stretched")
  assert.ok(cls.has("inset-x-4"), "16px each side on a normal phone")

  for (const anchor of barAnchors(markup)) {
    assert.match(anchor, /size-11/)
    assert.match(anchor, /shrink-0/, "a touch target never shrinks")
  }
  // The disclosure is the one target that is deliberately not 44px: it opens a
  // menu instead of navigating, and the row that does navigate is 44px.
  const trigger = triggerTag(markup)
  assert.match(trigger, /size-9/)
  assert.ok(!/size-11/.test(trigger))

  const target = 44
  const overflowTarget = 36
  const needed =
    2 * padding! + 5 * target + overflowTarget + 5 * gap!
  assert.equal(needed, 292, "the pill's own width requirement")
  assert.ok(cls.has("max-[339px]:inset-x-2"), "320px viewports keep the fit")
  const narrowPillWidth = 320 - 2 * 8
  assert.ok(
    narrowPillWidth >= needed,
    `at 320px the pill is ${narrowPillWidth}px for ${needed}px of content`
  )
})

test("the bar's items are icon-only links, in DOM order, with aria-label and title", () => {
  const markup = routed(<BottomNav access={ALL} />)
  const anchors = barAnchors(markup)
  assert.equal(anchors.length, 5)
  // Rely on NavLink's own aria-current, never hand-set: exactly the dashboard
  // is current in this router state.
  assert.equal(markup.match(/aria-current="page"/g)?.length, 1)
  const anchorText = [
    ...markup.split('id="bottom-nav-overflow"')[0].matchAll(
      /<a [^>]*>([\s\S]*?)<\/a>/g
    ),
  ].map((m) => m[1].replace(/<svg[\s\S]*?<\/svg>/g, "").trim())
  assert.deepEqual(
    anchorText,
    ["", "", "", "", ""],
    "no text labels next to icons"
  )
  const expected = primaryNavItems(ALL)
  anchors.forEach((anchor, i) => {
    assert.match(anchor, new RegExp(`aria-label="${expected[i].label}"`))
    assert.match(anchor, new RegExp(`title="${expected[i].label}"`))
    assert.match(anchor, new RegExp(`href="${expected[i].to}"`))
    assert.match(anchor, /size-11/)
  })
})

test("active and inactive class sets", () => {
  const item = primaryNavItems(ALL)[1]
  const active = tokens(
    html(<BottomNavItem item={item} isActive={true} />).match(
      /class="([^"]*)"/
    )![1]
  )
  const inactive = tokens(
    html(<BottomNavItem item={item} isActive={false} />).match(
      /class="([^"]*)"/
    )![1]
  )
  assert.ok(active.has("bg-muted"))
  assert.ok(!active.has("bg-transparent"))
  assert.ok(inactive.has("bg-transparent"))
  assert.ok(!inactive.has("bg-muted"), "inactive has no bare bg-muted token")
  assert.ok(inactive.has("text-muted-foreground"))
  for (const cls of [active, inactive]) {
    assert.ok(cls.has("size-11"))
    assert.ok(cls.has("rounded-full"))
    assert.ok(cls.has("focus-visible:ring-2"))
    assert.ok(cls.has("focus-visible:outline-none"))
  }
})

// --- where the user is ------------------------------------------------------

test("a nested automation screen keeps the automation destination current", () => {
  const markup = routed(<BottomNav access={ALL} />, "/automation/webhooks/7")
  const link = panelLinkTo(markup, "/automation")
  assert.match(link, /aria-current="page"/)
  assert.match(link, /bg-muted/)
  // The disclosure that carries it marks where the user is as well, from the
  // same URL and the same matcher.
  const trigger = triggerTag(markup)
  assert.match(trigger, /data-active="true"/)
  assert.match(trigger, /bg-muted/)
})

test("active state is the URL's, not a tab that can drift", () => {
  const at = (path: string) => routed(<BottomNav access={ALL} />, path)
  const currentCount = (markup: string) =>
    markup.match(/aria-current="page"/g)?.length ?? 0
  // Every nested automation screen lights the automation item and nothing else.
  for (const path of [
    "/automation",
    "/automation/webhooks",
    "/automation/webhooks/new",
    "/automation/workflows/12/runs/300",
  ]) {
    const markup = at(path)
    const automationLink = panelLinkTo(markup, "/automation")
    assert.match(
      automationLink,
      /aria-current="page"/,
      `the automation item is current at ${path}`
    )
    assert.equal(currentCount(markup), 1, `nothing else claims ${path}`)
    assert.match(triggerTag(markup), /data-active="true"/)
  }
  // Matching is segment-wise: a near-miss prefix is a different screen.
  const near = at("/automatically")
  assert.equal(currentCount(near), 0)
  assert.ok(!/data-active/.test(triggerTag(near)))
  // And a nested screen of another section lights that section.
  const messages = at("/channels/2/messages")
  assert.match(barAnchors(messages)[2], /aria-current="page"/)
  assert.equal(currentCount(messages), 1)
  assert.ok(!/data-active/.test(triggerTag(messages)))
})

test("source: the bar marks its items with the router's own matching", () => {
  const source = readFileSync(
    resolve(appDir, "components/bottom-nav.tsx"),
    "utf8"
  )
  // No `end`: a destination matches its whole subtree, so /automation/webhooks/9
  // keeps its item lit. The check is on the JSX prop, not on the word.
  assert.ok(!/^\s*end\s*$/m.test(source), "no `end` prop on any NavLink")
  assert.match(source, /<NavLink[\s\S]*?to=\{item\.to\}/)
  assert.match(
    source,
    /navItemActive\(pathname, item\.to\)/,
    "the disclosure reads the URL through the same matcher"
  )
  assert.ok(
    !/useState\(\s*(true|".*")\s*\)/.test(source),
    "no state stands in for which item is active"
  )
})

// --- hidden bar -------------------------------------------------------------

test("hidden: the bar slides away, stays mounted and leaves the tab order", () => {
  const nav = (hidden: boolean) =>
    navTag(
      routed(<BottomNav access={ALL} hidden={hidden} />, "/channels/2/messages")
    )
  const shown = nav(false)
  const away = nav(true)
  assert.ok(!classOf(shown).has("opacity-0"))
  assert.ok(!/\binert\b/.test(shown), "a visible bar is not inert")
  assert.ok(!/aria-hidden/.test(shown), "a visible bar is exposed")
  const cls = classOf(away)
  assert.ok(cls.has("opacity-0"))
  assert.ok(cls.has("translate-y-[calc(100%+2rem)]"))
  assert.ok(cls.has("pointer-events-none"))
  assert.ok(cls.has("transition-[translate,opacity]"))
  assert.match(away, /aria-hidden="true"/)
  assert.match(away, /\binert\b/)
  // Still the same links: hiding never unmounts the destinations.
  const whole = (hidden: boolean) =>
    routed(<BottomNav access={ALL} hidden={hidden} />, "/channels/2/messages")
  assert.equal(
    whole(true).match(/<a /g)?.length,
    whole(false).match(/<a /g)?.length
  )
})

test("source: the message timeline hides the bar, the other pages do not", () => {
  const handleOf = (route: string) =>
    readFileSync(resolve(appDir, "routes/_panel", route), "utf8")
  assert.match(handleOf("channel-messages.tsx"), /handle = hiddenNavSection\("/)
  for (const route of [
    "dashboard.tsx",
    "categories.tsx",
    "channels.tsx",
    "wordpress.tsx",
    "administrators.tsx",
    "automation.tsx",
    "automation-webhook-form.tsx",
    "automation-legacy-redirect.tsx",
    "automation-deliveries.tsx",
    "automation-delivery.tsx",
    "automation-workflow-form.tsx",
    "automation-runs.tsx",
    "automation-run.tsx",
  ]) {
    assert.ok(!/hiddenNavSection/.test(handleOf(route)), route)
  }
})

// --- the desktop sidebar ----------------------------------------------------

// The sidebar's destinations, rendered on their own. The shared `Sidebar`
// paints a drawer below lg and a column from lg up, and only the drawer branch
// is in the server's markup — and the account footer under the list speaks
// through a toast whose provider deliberately renders nothing outside a
// browser — so the list is reached through the component that draws it. That
// component reads the URL itself, so what is proved here is the real active
// state as well as the real permission filter.
function sidebar(access: Me["access"], at = "/automation/webhooks/7") {
  return renderToStaticMarkup(
    <MemoryRouter initialEntries={[at]}>
      <SidebarNav access={access} />
    </MemoryRouter>
  )
}

// A bare <span> in the sidebar is a destination's own label and nothing else.
const sidebarLabels = (markup: string) =>
  [...markup.matchAll(/<span>([^<]*)<\/span>/g)].map((m) => m[1])
const linkTo = (markup: string, href: string) =>
  markup.match(new RegExp(`<a\\b[^>]*href="${href}"[^>]*>`))?.[0] ?? ""

test("the sidebar shows اتوماسیون to an administrator who has the right", () => {
  const markup = sidebar(ALL)
  assert.deepEqual(sidebarLabels(markup), [
    "پیشخان",
    "دسته‌بندی‌ها",
    "کانال‌ها",
    "سایت‌های وردپرسی",
    "مدیرها",
    "حساب‌های من",
    "اتوماسیون",
  ])
  assert.match(linkTo(markup, "/automation"), /href="\/automation"/)
})

test("a restricted account keeps the rest of the sidebar and loses only اتوماسیون", () => {
  const markup = sidebar(RESTRICTED)
  assert.ok(!markup.includes("اتوماسیون"), "the entry is not drawn at all")
  assert.equal(linkTo(markup, "/automation"), "", "and there is no link to it")
  // Personal accounts are nobody's to grant or withhold: the API scopes the
  // list to whoever is signed in, so the entry survives a restricted summary.
  assert.deepEqual(sidebarLabels(markup), [
    "پیشخان",
    "دسته‌بندی‌ها",
    "سایت‌های وردپرسی",
    "حساب‌های من",
  ])
  for (const href of ["/dashboard", "/categories", "/wordpress", "/accounts"]) {
    assert.ok(markup.includes(`href="${href}"`), `${href} is untouched`)
  }
})

test("a summary that omits the automation flag grants no automation entry", () => {
  const without = { ...ALL } as Record<string, unknown>
  delete without.automation
  const markup = sidebar(without as Me["access"])
  assert.ok(!markup.includes("اتوماسیون"))
  assert.ok(
    markup.includes("مدیرها"),
    "a missing flag hides its own entry and nothing else"
  )
})

test("the sidebar marks the automation entry current at nested automation screens", () => {
  for (const at of [
    "/automation",
    "/automation/webhooks",
    "/automation/webhooks/7",
    "/automation/workflows/12/runs/300",
  ]) {
    const markup = sidebar(ALL, at)
    assert.match(linkTo(markup, "/automation"), /aria-current="page"/, at)
    assert.equal(
      (markup.match(/aria-current="page"/g) ?? []).length,
      1,
      `nothing else claims ${at}`
    )
  }
  // A near-miss prefix is a different screen, and another section's nested
  // screen lights that section and not automation's.
  assert.equal(
    (sidebar(ALL, "/automatically").match(/aria-current="page"/g) ?? []).length,
    0
  )
  const messages = sidebar(ALL, "/channels/2/messages")
  assert.match(linkTo(messages, "/channels"), /aria-current="page"/)
  assert.doesNotMatch(
    linkTo(messages, "/automation"),
    /aria-current="page"/,
    "and the automation entry goes quiet again"
  )
})

// Source-level checks: the shell's layout is what removes the drawer and the
// mobile header, and SSR cannot render the layout without a router session.
test("source: layout renders no SidebarTrigger, a desktop-only header and the bottom nav", () => {
  const layoutPath = resolve(appDir, "routes/_panel/layout.tsx")
  const source = readFileSync(layoutPath, "utf8")
  assert.ok(!/<SidebarTrigger/.test(source), "no SidebarTrigger rendered")
  assert.ok(
    !/useSidebar/.test(source),
    "layout no longer reads the mobile drawer state"
  )
  assert.match(source, /<header[^>]*className="[^"]*hidden[^"]*lg:flex[^"]*"/)
  assert.match(source, /<BottomNav access=\{me\.access\} hidden=\{bottomNavHidden\} \/>/)
  assert.match(source, /hideBottomNav/)
  assert.match(
    source,
    /pb-\[calc\(4rem\+env\(safe-area-inset-bottom\)\)\] lg:pb-0/
  )
  assert.match(source, /pb-\[calc\(6rem\+env\(safe-area-inset-bottom\)\)\]/)
  assert.match(source, /lg:pb-6/)

  // The desktop sidebar must show the same filtered destinations the phone's
  // bar shows. Walking the raw `navItems` here is what used to leave every
  // destination visible to every caller.
  assert.match(source, /\{visibleNavItems\(access\)\.map\(/)
  assert.ok(
    !/\{navItems\.map\(/.test(source),
    "the sidebar never walks the unfiltered list"
  )
  assert.ok(
    !/import \{[^}]*\bnavItems\b[^}]*\} from "@\/lib\/nav"/.test(source),
    "and does not even import the unfiltered list"
  )
  // The list tested above is the list the shell draws: the sidebar body renders
  // it, and the layout renders that body with the session's own access summary.
  assert.match(source, /<SidebarNav access=\{me\.access\} \/>/)
  assert.match(source, /<PanelSidebar me=\{me\} \/>/)

  const sidebarPath = resolve(
    appDir,
    "../../../packages/ui/src/components/sidebar.tsx"
  )
  const sidebar = readFileSync(sidebarPath, "utf8")
  assert.match(
    sidebar,
    /useMediaQuery\("\(max-width: 1023px\)"/,
    "drawer branch is below lg only"
  )
  assert.match(
    sidebar,
    /hidden shrink-0 overflow-hidden lg:block/,
    "desktop aside only from lg"
  )
  assert.ok(!/md:block/.test(sidebar), "no md gate left in the shared sidebar")
})

test("source: the sidebar marks the current section the same way the bar does", () => {
  const source = readFileSync(
    resolve(appDir, "routes/_panel/layout.tsx"),
    "utf8"
  )
  assert.match(source, /isActive=\{navItemActive\(pathname, item\.to\)\}/)
  assert.match(source, /render=\{<NavLink to=\{item\.to\} \/>\}/)
})

// The overflow is only ever a disclosure over more destinations, never a page
// of its own and never a drawer.
test("source: the overflow is a disclosure above the pill, not a drawer", () => {
  const source = readFileSync(
    resolve(appDir, "components/bottom-nav.tsx"),
    "utf8"
  )
  assert.match(source, /aria-expanded=\{open\}/)
  assert.match(source, /bottom-full/, "the menu opens above the pill")
  assert.ok(!/Sheet|Drawer|useSidebar/.test(source), "no drawer comes back")
  assert.ok(
    !/window\.location/.test(source),
    "nothing reaches a destination by typing its URL"
  )
  // The disclosure is rendered from the overflow items alone: no list of
  // destinations is duplicated in the component.
  assert.match(source, /\{overflow\.length > 0 && <BottomNavOverflow/)
  assert.ok(!/\/automation"/.test(source), "no destination is hardcoded here")
})
