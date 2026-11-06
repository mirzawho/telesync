import { useEffect, useRef, useState } from "react"
import { NavLink, useLocation } from "react-router"
import { EllipsisIcon } from "lucide-react"

import { cn } from "@workspace/ui/lib/utils"

import {
  navItemActive,
  overflowNavItems,
  primaryNavItems,
  type NavAccess,
  type NavItemData,
} from "@/lib/nav"

// The phone/tablet navigation: one floating, icon-only bar below lg, where the
// sidebar and the header are not shown at all. DOM order follows `navItems`
// (dashboard, categories, channels, wordpress, administrators); in RTL that
// renders right to left, which is the order the product wants.
//
// The bar carries the five first-class destinations and nothing else. Anything
// past them is reached through the compact "more" disclosure at the bar's end,
// which opens a menu above the pill — no sixth oversized icon, no drawer, and
// no need to know a URL to get there.

// Active is a filled pill, inactive is bare; both are round touch targets with
// a visible keyboard ring.
function itemClassName(isActive: boolean, size: "size-11" | "size-9") {
  return cn(
    "flex shrink-0 items-center justify-center rounded-full transition-colors",
    "focus-visible:ring-2 focus-visible:ring-ring focus-visible:outline-none",
    size,
    isActive
      ? "bg-muted text-foreground"
      : "bg-transparent text-muted-foreground hover:bg-muted/50"
  )
}

// Pure presentation of one destination for a given active state, so the class
// sets can be asserted without a router. Rendered inside a MemoryRouter it is
// the same link; outside one it is an inert span with the same classes.
export function BottomNavItem({
  item,
  isActive,
}: {
  item: NavItemData
  isActive: boolean
}) {
  const Icon = item.icon
  return (
    <span
      data-testid="bottom-nav-item"
      data-active={isActive ? "true" : undefined}
      className={itemClassName(isActive, "size-11")}
    >
      <Icon className="size-5" />
    </span>
  )
}

// The link itself; active state comes from the router (NavLink), which also
// sets aria-current="page" on the current item.
function BottomNavLink({ item }: { item: NavItemData }) {
  return (
    <NavLink
      to={item.to}
      // No `end`: a destination matches its whole subtree, so a nested screen
      // (/automation/webhooks/9, /channels/3/messages) keeps its section lit.
      // Matching is segment-wise, so /automatically is not /automation.
      aria-label={item.label}
      title={item.label}
      className={({ isActive }) => itemClassName(isActive, "size-11")}
    >
      <item.icon className="size-5" />
    </NavLink>
  )
}

// The overflow: one compact disclosure, not a sixth destination. It is the one
// target in the bar that is deliberately under 44px — it opens a menu rather
// than navigating, and the row that does navigate is a full 44px target. It is
// only rendered when there is something behind it, so an account without the
// automation right gets the plain five-icon bar and nothing more.
export function BottomNavOverflow({ items }: { items: NavItemData[] }) {
  const [open, setOpen] = useState(false)
  const wrapper = useRef<HTMLDivElement>(null)
  const trigger = useRef<HTMLButtonElement>(null)
  const { pathname } = useLocation()
  // The disclosure marks where the user is when one of its destinations is
  // current, computed from the same URL and the same matcher the links use.
  const current = items.some((item) => navItemActive(pathname, item.to))

  useEffect(() => {
    if (!open) return
    const onPointerDown = (event: PointerEvent) => {
      if (!wrapper.current?.contains(event.target as Node)) setOpen(false)
    }
    const onKeyDown = (event: KeyboardEvent) => {
      if (event.key !== "Escape") return
      setOpen(false)
      trigger.current?.focus()
    }
    document.addEventListener("pointerdown", onPointerDown)
    document.addEventListener("keydown", onKeyDown)
    return () => {
      document.removeEventListener("pointerdown", onPointerDown)
      document.removeEventListener("keydown", onKeyDown)
    }
  }, [open])

  return (
    <div ref={wrapper} className="relative">
      <button
        ref={trigger}
        type="button"
        data-testid="bottom-nav-more"
        data-active={current ? "true" : undefined}
        aria-label="بیشتر"
        aria-expanded={open}
        aria-controls="bottom-nav-overflow"
        onClick={() => setOpen((value) => !value)}
        className={itemClassName(current, "size-9")}
      >
        <EllipsisIcon className="size-5" />
      </button>
      {/* Rendered in the markup and hidden with display:none when closed: it is
          out of the tab order and the accessibility tree while shut, and a test
          can still see that the destination behind the disclosure is real. */}
      <div
        id="bottom-nav-overflow"
        className={cn(
          "absolute bottom-full end-0 z-50 mb-2 w-48",
          "rounded-xl border border-border/70 bg-background p-1 shadow-lg",
          !open && "hidden"
        )}
      >
        <ul className="flex flex-col gap-1">
          {items.map((item) => (
            <li key={item.to}>
              <NavLink
                to={item.to}
                aria-label={item.label}
                title={item.label}
                onClick={() => setOpen(false)}
                className={({ isActive }) =>
                  cn(
                    "flex min-h-11 items-center gap-2 rounded-lg px-3 text-sm transition-colors",
                    "focus-visible:ring-2 focus-visible:ring-ring focus-visible:outline-none",
                    isActive
                      ? "bg-muted text-foreground"
                      : "text-muted-foreground hover:bg-muted/50"
                  )
                }
              >
                <item.icon className="size-5" />
                <span>{item.label}</span>
              </NavLink>
            </li>
          ))}
        </ul>
      </div>
    </div>
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
  access?: Partial<NavAccess>
  hidden?: boolean
}) {
  const items = primaryNavItems(access)
  const overflow = overflowNavItems(access)
  if (items.length === 0 && overflow.length === 0) return null
  return (
    <nav
      aria-label="ناوبری اصلی"
      aria-hidden={hidden || undefined}
      inert={hidden || undefined}
      className={cn(
        "fixed inset-x-4 z-40 mx-auto flex w-auto max-w-[420px] items-center justify-between gap-1 rounded-full border border-border/70 bg-background/95 p-2 shadow-lg backdrop-blur-md lg:hidden",
        // Five 44px destinations plus the compact overflow, their gaps and the
        // pill's padding need 292px. `inset-x-4` leaves 288px on a 320px
        // viewport, so there the side inset gives way instead of the targets.
        "max-[339px]:inset-x-2",
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
      {overflow.length > 0 && <BottomNavOverflow items={overflow} />}
    </nav>
  )
}
