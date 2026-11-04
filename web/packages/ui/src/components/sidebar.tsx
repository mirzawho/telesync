import { mergeProps } from "@base-ui/react/merge-props"
import { useRender } from "@base-ui/react/use-render"
import { MenuIcon } from "lucide-react"
import * as React from "react"

import { Button } from "@workspace/ui/components/button"
import {
  Sheet,
  SheetContent,
  SheetDescription,
  SheetHeader,
  SheetTitle,
} from "@workspace/ui/components/sheet"
import { useMediaQuery } from "@workspace/ui/hooks/use-media-query"
import { cn } from "@workspace/ui/lib/utils"

const SIDEBAR_WIDTH = "16rem"
const SIDEBAR_COOKIE_NAME = "sidebar_state"
const SIDEBAR_COOKIE_MAX_AGE = 60 * 60 * 24 * 7
const SIDEBAR_KEYBOARD_SHORTCUT = "b"

// The width transition is on for every render but the first: a collapsed
// sidebar restored by the server must not animate open on hydration.
const SidebarInitialPaint = React.createContext(false)

export function SidebarFirstPaint({ children }: { children: React.ReactNode }) {
  return (
    <SidebarInitialPaint.Provider value={true}>
      {children}
    </SidebarInitialPaint.Provider>
  )
}

type SidebarContextValue = {
  open: boolean
  setOpen: (open: boolean) => void
  openMobile: boolean
  setOpenMobile: (open: boolean) => void
  isMobile: boolean
  toggleSidebar: () => void
  animate: boolean
}

const SidebarContext = React.createContext<SidebarContextValue | null>(null)

function useSidebar() {
  const context = React.useContext(SidebarContext)
  if (!context) {
    throw new Error("useSidebar must be used within a SidebarProvider.")
  }
  return context
}

function SidebarProvider({
  defaultOpen = true,
  open: openProp,
  onOpenChange: setOpenProp,
  className,
  style,
  children,
  ...props
}: React.ComponentProps<"div"> & {
  defaultOpen?: boolean
  open?: boolean
  onOpenChange?: (open: boolean) => void
}) {
  // Below lg only: at 768-1023px the shell shows its own bottom navigation, so
  // there is no drawer there and nothing a trigger could open.
  const isMobile = useMediaQuery("(max-width: 1023px)", true)
  const [openMobile, setOpenMobileState] = React.useState(false)
  const [uncontrolledOpen, setUncontrolledOpen] = React.useState(defaultOpen)
  const open = openProp ?? uncontrolledOpen

  const setOpen = React.useCallback(
    (value: boolean) => {
      if (setOpenProp) {
        setOpenProp(value)
      } else {
        setUncontrolledOpen(value)
      }
      // Persisted for the server, which passes it back as defaultOpen.
      document.cookie = `${SIDEBAR_COOKIE_NAME}=${value}; Path=/; Max-Age=${SIDEBAR_COOKIE_MAX_AGE}`
    },
    [setOpenProp]
  )

  const setOpenMobile = React.useCallback(
    (value: boolean) => setOpenMobileState(value),
    []
  )

  const toggleSidebar = React.useCallback(() => {
    if (isMobile) {
      setOpenMobile(!openMobile)
    } else {
      setOpen(!open)
    }
  }, [isMobile, openMobile, open, setOpen])

  React.useEffect(() => {
    const handleKeyDown = (event: KeyboardEvent) => {
      if (
        event.key === SIDEBAR_KEYBOARD_SHORTCUT &&
        (event.metaKey || event.ctrlKey)
      ) {
        event.preventDefault()
        toggleSidebar()
      }
    }
    window.addEventListener("keydown", handleKeyDown)
    return () => window.removeEventListener("keydown", handleKeyDown)
  }, [toggleSidebar])

  // A resize across the lg breakpoint must not carry the mobile sheet state.
  React.useEffect(() => {
    if (!isMobile) {
      setOpenMobile(false)
    }
  }, [isMobile, setOpenMobile])

  const initialPaint = React.useContext(SidebarInitialPaint)
  const [hydrated, setHydrated] = React.useState(false)
  React.useEffect(() => setHydrated(true), [])

  const value = React.useMemo(
    () => ({
      open,
      setOpen,
      openMobile,
      setOpenMobile,
      isMobile,
      toggleSidebar,
      animate: !initialPaint || hydrated,
    }),
    [
      open,
      setOpen,
      openMobile,
      setOpenMobile,
      isMobile,
      toggleSidebar,
      initialPaint,
      hydrated,
    ]
  )

  return (
    <SidebarContext.Provider value={value}>
      <div
        data-slot="sidebar-wrapper"
        style={
          { "--sidebar-width": SIDEBAR_WIDTH, ...style } as React.CSSProperties
        }
        className={cn("group/sidebar-wrapper flex min-h-svh w-full", className)}
        {...props}
      >
        {children}
      </div>
    </SidebarContext.Provider>
  )
}

// Below lg the same children are shown in a sheet; at lg and up, in a sticky
// column. The UI is the same in both, so a page never has two nav trees.
function Sidebar({
  side = "start",
  className,
  children,
}: {
  side?: "start" | "end"
  className?: string
  children: React.ReactNode
}) {
  const { isMobile, open, openMobile, setOpenMobile, animate } = useSidebar()

  if (isMobile) {
    return (
      <Sheet open={openMobile} onOpenChange={setOpenMobile}>
        <SheetContent
          side={side}
          className="gap-0 bg-sidebar p-0 text-sidebar-foreground"
          data-slot="sidebar"
          data-mobile="true"
        >
          <SheetHeader className="border-b border-sidebar-border">
            <SheetTitle>منوی اصلی</SheetTitle>
            <SheetDescription className="sr-only">
              مسیرهای پنل مدیریت
            </SheetDescription>
          </SheetHeader>
          {children}
        </SheetContent>
      </Sheet>
    )
  }

  return (
    <aside
      data-slot="sidebar"
      data-state={open ? "expanded" : "collapsed"}
      className={cn(
        "hidden shrink-0 overflow-hidden lg:block",
        open ? "w-(--sidebar-width)" : "w-0",
        animate && "transition-[width] duration-200 ease-linear"
      )}
    >
      <div
        className={cn(
          "sticky top-0 flex h-svh w-(--sidebar-width) flex-col border-sidebar-border bg-sidebar text-sidebar-foreground",
          side === "start" ? "border-e" : "border-s",
          className
        )}
      >
        {children}
      </div>
    </aside>
  )
}

function SidebarTrigger({
  className,
  ...props
}: React.ComponentProps<typeof Button>) {
  const { toggleSidebar } = useSidebar()
  return (
    <Button
      data-slot="sidebar-trigger"
      variant="ghost"
      size="icon-sm"
      className={className}
      onClick={toggleSidebar}
      {...props}
    >
      <MenuIcon />
      <span className="sr-only">تغییر نمایش منو</span>
    </Button>
  )
}

function SidebarHeader({ className, ...props }: React.ComponentProps<"div">) {
  return (
    <div
      data-slot="sidebar-header"
      className={cn("flex flex-col gap-2 p-3", className)}
      {...props}
    />
  )
}

function SidebarContent({ className, ...props }: React.ComponentProps<"div">) {
  return (
    <div
      data-slot="sidebar-content"
      className={cn(
        "flex min-h-0 flex-1 flex-col gap-2 overflow-y-auto p-3",
        className
      )}
      {...props}
    />
  )
}

function SidebarFooter({ className, ...props }: React.ComponentProps<"div">) {
  return (
    <div
      data-slot="sidebar-footer"
      className={cn(
        "flex flex-col gap-2 border-t border-sidebar-border p-3",
        className
      )}
      {...props}
    />
  )
}

function SidebarGroup({ className, ...props }: React.ComponentProps<"div">) {
  return (
    <div
      data-slot="sidebar-group"
      className={cn("flex flex-col gap-1", className)}
      {...props}
    />
  )
}

function SidebarGroupLabel({
  className,
  ...props
}: React.ComponentProps<"div">) {
  return (
    <div
      data-slot="sidebar-group-label"
      className={cn(
        "px-2 py-1.5 text-xs font-medium text-muted-foreground",
        className
      )}
      {...props}
    />
  )
}

function SidebarMenu({ className, ...props }: React.ComponentProps<"ul">) {
  return (
    <ul
      data-slot="sidebar-menu"
      className={cn("flex flex-col gap-0.5", className)}
      {...props}
    />
  )
}

function SidebarMenuItem({ className, ...props }: React.ComponentProps<"li">) {
  return (
    <li
      data-slot="sidebar-menu-item"
      className={cn("relative", className)}
      {...props}
    />
  )
}

function SidebarMenuButton({
  isActive = false,
  className,
  render,
  ...props
}: useRender.ComponentProps<"button"> & { isActive?: boolean }) {
  return useRender({
    defaultTagName: "button",
    render,
    props: mergeProps<"button">(
      {
        ...{
          "data-slot": "sidebar-menu-button",
          "data-active": isActive ? "true" : undefined,
          // NavLink (and any router) marks the current item with aria-current;
          // the active style follows it so the two never disagree.
          "aria-current": isActive ? "page" : undefined,
          "data-link-current": undefined,
        },
        className: cn(
          "flex w-full items-center gap-2.5 overflow-hidden rounded-lg px-2.5 py-2 text-start text-sm font-medium transition-colors outline-none hover:bg-sidebar-accent hover:text-sidebar-accent-foreground focus-visible:ring-3 focus-visible:ring-sidebar-ring/50 disabled:pointer-events-none disabled:opacity-50 data-[active]:bg-sidebar-accent data-[active]:text-sidebar-accent-foreground [&_svg]:pointer-events-none [&_svg]:shrink-0 [&_svg:not([class*='size-'])]:size-4",
          className
        ),
      },
      props
    ),
  })
}

export {
  Sidebar,
  SidebarContent,
  SidebarFooter,
  SidebarGroup,
  SidebarGroupLabel,
  SidebarHeader,
  SidebarMenu,
  SidebarMenuButton,
  SidebarMenuItem,
  SidebarProvider,
  SidebarTrigger,
  useSidebar,
}
