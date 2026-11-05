import { NavLink } from "react-router"

import { cn } from "@workspace/ui/lib/utils"

import { visibleNavItems } from "@/lib/nav"

// The phone/tablet navigation: one floating, icon-only bar below lg, where
// the sidebar and the header are not shown at all. DOM order follows
// `navItems` (dashboard, categories, channels, wordpress, administrators);
// in RTL that renders right to left, which is the order the product wants.

type NavAccess = { channels: boolean; categories: boolean; users: boolean }

export function bottomNavItems(access?: NavAccess) {
  return visibleNavItems(access)
}

export type BottomNavItemData = ReturnType<typeof bottomNavItems>[number]

// Active is a filled pill, inactive is bare; both are 44px round touch
// targets with a visible keyboard ring.
function itemClassName(isActive: boolean) {
  return cn(
    "flex size-11 shrink-0 items-center justify-center rounded-full transition-colors",
    "focus-visible:ring-2 focus-visible:ring-ring focus-visible:outline-none",
    isActive
      ? "bg-muted text-foreground"
      : "bg-transparent text-muted-foreground hover:bg-muted/50"
  )
}

// Pure presentation of one item for a given active state, so the class sets
// can be asserted without a router. Rendered inside a MemoryRouter it is the
// same link; outside one it is an inert span with the same classes.
export function BottomNavItem({
  item,
  isActive,
}: {
  item: BottomNavItemData
  isActive: boolean
}) {
  const Icon = item.icon
  return (
    <span
      data-testid="bottom-nav-item"
      data-active={isActive ? "true" : undefined}
      className={itemClassName(isActive)}
    >
      <Icon className="size-5" />
    </span>
  )
}

// The link itself; active state comes from the router (NavLink), which also
// sets aria-current="page" on the current item.
function BottomNavLink({ item }: { item: BottomNavItemData }) {
  return (
    <NavLink
      to={item.to}
      // Every destination is a direct child of the panel layout, so an exact
      // match for all of them keeps /dashboard from lighting up on siblings.
      end
      aria-label={item.label}
      title={item.label}
      className={({ isActive }) => itemClassName(isActive)}
    >
      <item.icon className="size-5" />
    </NavLink>
  )
}

// hidden: the route wants the whole screen (the message timeline). The bar
// stays mounted and slides down out of view, so moving between pages is an
// animation rather than a jump; it is also inert and invisible to assistive
// technology while away.
export function BottomNav({
  access,
  hidden = false,
}: {
  access?: NavAccess
  hidden?: boolean
}) {
  const items = bottomNavItems(access)
  if (items.length === 0) return null
  return (
    <nav
      aria-label="ناوبری اصلی"
      aria-hidden={hidden || undefined}
      inert={hidden || undefined}
      className={cn(
        "fixed inset-x-4 z-40 mx-auto flex w-auto max-w-[420px] items-center justify-between gap-1 rounded-full border border-border/70 bg-background/95 p-2 shadow-lg backdrop-blur-md lg:hidden",
        "bottom-[calc(1rem+env(safe-area-inset-bottom))]",
        "transition-[translate,opacity] duration-300 ease-out motion-reduce:transition-none",
        hidden
          ? "pointer-events-none translate-y-[calc(100%+2rem)] opacity-0"
          : "translate-y-0 opacity-100"
      )}
    >
      {items.map((item) => (
        <BottomNavLink key={item.to} item={item} />
      ))}
    </nav>
  )
}
