import type * as React from "react"
import {
  FolderTreeIcon,
  GlobeIcon,
  LayoutDashboardIcon,
  SendIcon,
  UserCogIcon,
} from "lucide-react"

// The panel's navigation. `access` names the /auth/me access flag that makes
// the item readable; an item without one is always shown.
type AccessKind = "channels" | "categories" | "users"

interface NavItem {
  to: string
  label: string
  icon: React.ComponentType<{ className?: string }>
  access: AccessKind | null
}

export const navItems: NavItem[] = [
  {
    to: "/dashboard",
    label: "پیشخان",
    icon: LayoutDashboardIcon,
    access: null,
  },
  {
    to: "/categories",
    label: "دسته‌بندی‌ها",
    icon: FolderTreeIcon,
    access: "categories",
  },
  { to: "/channels", label: "کانال‌ها", icon: SendIcon, access: "channels" },
  {
    to: "/wordpress",
    label: "سایت‌های وردپرسی",
    icon: GlobeIcon,
    // /auth/me's access summary does not include WordPress sites, so the item
    // is always shown; the page itself answers with a forbidden state when the
    // caller lacks the right.
    access: null,
  },
  {
    to: "/administrators",
    label: "مدیرها",
    icon: UserCogIcon,
    access: "users",
  },
]

export function visibleNavItems(access?: {
  channels: boolean
  categories: boolean
  users: boolean
}) {
  if (!access) {
    return navItems
  }
  // The navigation names channel, category and account sections; the server
  // status is a card on the dashboard, so servers.view adds no item here.
  const readable: Record<AccessKind, boolean> = {
    channels: access.channels,
    categories: access.categories,
    users: access.users,
  }
  return navItems.filter((item) => item.access === null || readable[item.access])
}

// breadcrumbFor names the current section for the header and the document title.
export function navItemFor(pathname: string) {
  return navItems.find(
    (item) => pathname === item.to || pathname.startsWith(`${item.to}/`)
  )
}
