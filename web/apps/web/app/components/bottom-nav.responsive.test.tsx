import assert from "node:assert/strict"
import { readFileSync } from "node:fs"
import { resolve } from "node:path"
import { test } from "node:test"

import { MemoryRouter } from "react-router"
import { renderToStaticMarkup } from "react-dom/server"

import { navItems } from "@/lib/nav"

import {
  BottomNav,
  BottomNavItem,
  bottomNavItems,
} from "@/components/bottom-nav"

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

const ALL = { channels: true, categories: true, users: true, servers: true }
const RESTRICTED = { channels: false, categories: true, users: false }

const paths = (access?: {
  channels: boolean
  categories: boolean
  users: boolean
}) => bottomNavItems(access).map((item) => item.to)

test("full access: five items in navItems order", () => {
  assert.deepEqual(paths(ALL), [
    "/dashboard",
    "/categories",
    "/channels",
    "/wordpress",
    "/administrators",
  ])
})

test("restricted access: only authorized items, in order, no placeholders", () => {
  assert.deepEqual(paths(RESTRICTED), [
    "/dashboard",
    "/categories",
    "/wordpress",
  ])
})

test("every nav item is represented in the bottom nav (no drift from navItems)", () => {
  const labels = bottomNavItems(ALL).map((item) => item.label)
  assert.deepEqual(
    labels,
    navItems.map((item) => item.label)
  )
})

test("the nav bar is floating, rounded, below lg and clears the safe area", () => {
  const markup = routed(<BottomNav access={ALL} />)
  const nav = markup.match(/^<nav[^>]*>/)?.[0]
  assert.ok(nav, "nav element rendered")
  assert.match(nav, /aria-label="ناوبری اصلی"/)
  const cls = tokens(nav.match(/class="([^"]*)"/)![1])
  assert.ok(cls.has("fixed"))
  assert.ok(cls.has("rounded-full"))
  assert.ok(cls.has("z-40"))
  assert.ok(cls.has("lg:hidden"))
  assert.ok(cls.has("inset-x-4"))
  assert.ok(cls.has("bottom-[calc(1rem+env(safe-area-inset-bottom))]"))
  assert.ok(!cls.has("bottom-4"), "safe-area bottom replaces plain bottom-4")
  assert.ok(!cls.has("md:hidden"), "the bar is governed by lg, not md")
})

test("items are icon-only links, in DOM order, with aria-label and title", () => {
  const markup = routed(<BottomNav access={ALL} />)
  assert.equal(markup.match(/<a /g)?.length, 5)
  // Rely on NavLink's own aria-current, never hand-set: exactly the dashboard
  // is current in this router state.
  assert.equal(markup.match(/aria-current="page"/g)?.length, 1)
  const anchorText = [...markup.matchAll(/<a [^>]*>([\s\S]*?)<\/a>/g)].map(
    (m) => m[1].replace(/<svg[\s\S]*?<\/svg>/g, "").trim()
  )
  assert.deepEqual(
    anchorText,
    ["", "", "", "", ""],
    "no text labels next to icons"
  )
  const anchors = [...markup.matchAll(/<a [^>]*>/g)].map((m) => m[0])
  const expected = bottomNavItems(ALL)
  anchors.forEach((anchor, i) => {
    assert.match(anchor, new RegExp(`aria-label="${expected[i].label}"`))
    assert.match(anchor, new RegExp(`title="${expected[i].label}"`))
    assert.match(anchor, new RegExp(`href="${expected[i].to}"`))
    assert.match(anchor, /size-11/)
  })
  // Every destination is a direct child of the panel layout: exact matching
  // keeps /dashboard from lighting up on its siblings, while /channels/8/messages
  // still matches its parent path segment.
  const source = readFileSync(
    resolve(appDir, "components/bottom-nav.tsx"),
    "utf8"
  )
  assert.match(source, /<NavLink[\s\S]*?\n\s*end\n/)
})

test("active and inactive class sets", () => {
  const item = bottomNavItems(ALL)[1]
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
  assert.match(source, /<BottomNav access=\{me\.access\} \/>/)
  assert.match(
    source,
    /pb-\[calc\(4rem\+env\(safe-area-inset-bottom\)\)\] lg:pb-0/
  )
  assert.match(source, /pb-\[calc\(6rem\+env\(safe-area-inset-bottom\)\)\]/)
  assert.match(source, /lg:pb-6/)

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
