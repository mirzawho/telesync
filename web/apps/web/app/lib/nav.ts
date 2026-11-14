import type * as React from "react"
import { matchPath } from "react-router"
import {
  FolderTreeIcon,
  GlobeIcon,
  LayoutDashboardIcon,
  SendIcon,
  UserCogIcon,
  UserRoundIcon,
  WorkflowIcon,
} from "lucide-react"

// The panel's navigation. `access` names the /auth/me access flag that makes
// the item readable; an item without one is always shown.
type AccessKind = "channels" | "categories" | "users" | "automation"

// Where the phone's floating bar keeps an item. The bar has room for the five
// destinations below, each a first-class 44px icon; anything after them is a
// compact "more" menu item, so the bar never grows a sixth oversized icon.
// The desktop sidebar lists every item the same way either way.
type NavPlacement = "primary" | "overflow"

// NavAccess is the part of the account's access summary the navigation reads.
// The summary reports the global viewing permission of each named area, and
// nothing else; a flag the server leaves out is read as "not granted", the same
// deny-by-default rule the API applies to the routes themselves.
export interface NavAccess {
  channels: boolean
  categories: boolean
  users: boolean
  automation: boolean
}

interface NavItem {
  to: string
  label: string
  icon: React.ComponentType<{ className?: string }>
  access: AccessKind | null
  placement: NavPlacement
}

// DOM order is the section order everywhere: the sidebar in this sequence, and
// the phone's bar right to left in this RTL panel. The five first-class
// destinations keep their positions; the sections added after them — personal
// accounts and automation — are overflow items, which keeps the bar at its five
// 44px icons and puts them one tap behind the "more" affordance at its end.
export const navItems: NavItem[] = [
  {
    to: "/dashboard",
    label: "پیشخان",
    icon: LayoutDashboardIcon,
    access: null,
    placement: "primary",
  },
  {
    to: "/categories",
    label: "دسته‌بندی‌ها",
    icon: FolderTreeIcon,
    access: "categories",
    placement: "primary",
  },
  {
    to: "/channels",
    label: "کانال‌ها",
    icon: SendIcon,
    access: "channels",
    placement: "primary",
  },
  {
    to: "/wordpress",
    label: "سایت‌های وردپرسی",
    icon: GlobeIcon,
    // /auth/me's access summary does not include WordPress sites, so the item
    // is always shown; the page itself answers with a forbidden state when the
    // caller lacks the right.
    access: null,
    placement: "primary",
  },
  {
    to: "/administrators",
    label: "مدیرها",
    icon: UserCogIcon,
    access: "users",
    placement: "primary",
  },
  {
    to: "/accounts",
    label: "حساب‌های من",
    icon: UserRoundIcon,
    // Every account owns their own Telegram accounts, so this destination has
    // no permission behind it: the list is scoped to the caller by the API, not
    // by a right that could be granted or withheld.
    access: null,
    placement: "overflow",
  },
  {
    to: "/automation",
    label: "اتوماسیون",
    icon: WorkflowIcon,
    access: "automation",
    placement: "overflow",
  },
]

function readable(access?: Partial<NavAccess>): Record<AccessKind, boolean> {
  // The navigation names channel, category, account and automation sections;
  // the server status is a card on the dashboard, so servers.view adds no item
  // here. A flag the summary omitted is not an implicit grant.
  return {
    channels: access?.channels === true,
    categories: access?.categories === true,
    users: access?.users === true,
    automation: access?.automation === true,
  }
}

export function visibleNavItems(access?: Partial<NavAccess>) {
  if (!access) {
    return navItems
  }
  const granted = readable(access)
  return navItems.filter((item) => item.access === null || granted[item.access])
}

// The phone's bar: its five first-class icons and the items behind the compact
// "more" affordance, both already filtered by access. A caller that may show
// nothing in the overflow never renders the affordance at all.
export function primaryNavItems(access?: Partial<NavAccess>) {
  return visibleNavItems(access).filter((item) => item.placement === "primary")
}

export function overflowNavItems(access?: Partial<NavAccess>) {
  return visibleNavItems(access).filter((item) => item.placement === "overflow")
}

export type NavItemData = (typeof navItems)[number]

// breadcrumbFor names the current section for the header and the document title.
export function navItemFor(pathname: string) {
  return navItems.find(
    (item) => pathname === item.to || pathname.startsWith(`${item.to}/`)
  )
}

// Whether the current URL belongs to a destination: the destination itself or
// anything under it, so /automation/webhooks/9 keeps the automation item marked
// current. This is the router's own segment-wise matching (`matchPath`, the same
// call `NavLink` makes for the same link), so a nav item can never disagree with
// the address bar; there is no second notion of "which tab is active" anywhere.
export function navItemActive(pathname: string, to: string) {
  return matchPath({ path: to, end: false }, pathname) !== null
}
