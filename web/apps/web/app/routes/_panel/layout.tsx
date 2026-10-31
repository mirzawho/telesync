import {
  Link,
  Outlet,
  isRouteErrorResponse,
  useLocation,
  useMatches,
  useRouteLoaderData,
  useSubmit,
} from "react-router"
import { ChevronsUpDownIcon, LogOutIcon, RadioTowerIcon } from "lucide-react"

import { Button } from "@workspace/ui/components/button"
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuGroup,
  DropdownMenuItem,
  DropdownMenuLabel,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from "@workspace/ui/components/dropdown-menu"
import {
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
} from "@workspace/ui/components/sidebar"

import { appVersion } from "@/components/app-version"
import { Breadcrumbs } from "@/components/page-header"
import { ErrorState } from "@/components/states"
import { navItems, navItemFor } from "@/lib/nav"
import type { SectionHandle } from "@/lib/page"
import { requireSession } from "@/lib/session"

import type { Route } from "./+types/layout"

// The cookie the sidebar writes when it is collapsed or expanded, read on the
// server so the first paint already has the right width.
function sidebarOpenFromCookie(request: Request): boolean {
  const match = request.headers
    .get("Cookie")
    ?.match(/(?:^|;\s*)sidebar_state=(true|false)/)
  return match ? match[1] === "true" : true
}

export async function clientLoader({ request }: Route.LoaderArgs) {
  const { me } = await requireSession(request)
  return {
    me,
    sidebarOpen: sidebarOpenFromCookie(request),
  }
}

export default function PanelLayout({ loaderData }: Route.ComponentProps) {
  const { me, sidebarOpen } = loaderData

  return (
    <SidebarProvider defaultOpen={sidebarOpen}>
      <Sidebar>
        <SidebarHeader>
          <Link
            to="/dashboard"
            className="flex items-center gap-2.5 rounded-lg px-1.5 py-1"
          >
            <div className="flex size-8 items-center justify-center rounded-lg bg-primary text-primary-foreground">
              <RadioTowerIcon className="size-4" />
            </div>
            <div className="flex flex-col">
              <span className="text-sm font-semibold">تیل‌سینک</span>
              <span className="text-xs text-muted-foreground">
                {appVersion ? `نسخه v${appVersion}` : "پنل مدیریت"}
              </span>
            </div>
          </Link>
        </SidebarHeader>
        <SidebarContent>
          <SidebarGroup>
            <SidebarGroupLabel>پیمایش</SidebarGroupLabel>
            <SidebarMenu>
              {navItems.map((item) => (
                <SidebarMenuItem key={item.to}>
                  <SidebarMenuButton render={<Link to={item.to} />}>
                    <item.icon />
                    <span>{item.label}</span>
                  </SidebarMenuButton>
                </SidebarMenuItem>
              ))}
            </SidebarMenu>
          </SidebarGroup>
        </SidebarContent>
        <SidebarFooter>
          <AccountMenu fullname={me.fullname} username={me.username} />
        </SidebarFooter>
      </Sidebar>

      <div className="flex h-svh min-w-0 flex-1 flex-col">
        <AppHeader me={me} />
        {/* The one scroll container on a panel page: the header stays put and
            the sidebar never moves because the document itself does not scroll. */}
        <main className="min-h-0 flex-1 overflow-y-auto">
          <div className="mx-auto w-full max-w-6xl px-4 py-6 sm:px-6 lg:px-8">
            <Outlet />
          </div>
        </main>
      </div>
    </SidebarProvider>
  )
}

function AppHeader({ me }: { me: { fullname: string; username: string } }) {
  const matches = useMatches()
  const { pathname } = useLocation()

  // The deepest matched route that names a section is the page on screen.
  const named = [...matches]
    .reverse()
    .find((m) => (m.handle as SectionHandle | undefined)?.section)
  const section =
    (named?.handle as SectionHandle | undefined)?.section ??
    navItemFor(pathname)?.label

  return (
    <header className="sticky top-0 z-30 flex h-14 items-center gap-3 border-b bg-background/95 px-4 backdrop-blur sm:px-6 lg:px-8">
      <SidebarTrigger className="-ms-1" />
      <Breadcrumbs
        className="hidden sm:block"
        items={[
          { label: "پیشخان", to: "/dashboard" },
          { label: section ?? "پنل" },
        ]}
      />
      <div className="flex-1" />
      <AccountMenu fullname={me.fullname} username={me.username} compact />
    </header>
  )
}

// compact: the header's small avatar button; otherwise the sidebar footer's
// full-width name-and-username button.
function AccountMenu({
  fullname,
  username,
  compact = false,
}: {
  fullname: string
  username: string
  compact?: boolean
}) {
  const submit = useSubmit()
  const { isMobile, setOpenMobile } = useSidebar()

  const logout = () => {
    if (isMobile) setOpenMobile(false)
    void submit(null, { method: "post", action: "/logout", navigate: true })
  }

  return (
    <DropdownMenu>
      <DropdownMenuTrigger
        render={
          <Button
            variant="ghost"
            aria-label="حساب کاربری"
            className={
              compact
                ? "size-8 rounded-full p-0"
                : "h-auto w-full justify-between px-2 py-2"
            }
          />
        }
      >
        <span className="flex min-w-0 items-center gap-2">
          <span className="flex size-7 shrink-0 items-center justify-center rounded-full bg-muted text-xs font-medium">
            {fullname.slice(0, 1)}
          </span>
          <span
            className={compact ? "hidden" : "flex min-w-0 flex-col items-start"}
          >
            <span className="max-w-40 truncate text-sm font-medium">
              {fullname}
            </span>
            <span
              className="max-w-40 truncate text-xs text-muted-foreground"
              dir="ltr"
            >
              {username}
            </span>
          </span>
        </span>
        {!compact && (
          <ChevronsUpDownIcon className="size-4 text-muted-foreground" />
        )}
      </DropdownMenuTrigger>
      <DropdownMenuContent align="end" className="w-56">
        <DropdownMenuGroup>
          <DropdownMenuLabel className="flex flex-col gap-0.5">
            <span className="text-sm font-medium text-foreground">
              {fullname}
            </span>
            <span dir="ltr">{username}</span>
          </DropdownMenuLabel>
        </DropdownMenuGroup>
        <DropdownMenuSeparator />
        <DropdownMenuItem onClick={logout}>
          <LogOutIcon />
          خروج
        </DropdownMenuItem>
      </DropdownMenuContent>
    </DropdownMenu>
  )
}

export function ErrorBoundary({ error }: Route.ErrorBoundaryProps) {
  let title = "خطایی رخ داد"
  let message = "این صفحه بارگذاری نشد."
  let details: string | undefined
  if (isRouteErrorResponse(error)) {
    title = `خطای ${error.status}`
    message =
      error.status === 503
        ? "ارتباط با سرویس برقرار نشد. اتصال شبکه یا وضعیت سرور را بررسی کنید."
        : error.status === 401 || error.status === 403
          ? "نشست شما معتبر نیست. دوباره وارد شوید."
          : "پاسخ سرویس برای این نشانی یافت نشد."
    details = [
      error.statusText,
      typeof error.data === "string" ? error.data : "",
    ]
      .filter(Boolean)
      .join(" — ")
  } else if (error instanceof Error) {
    message = error.message
  }
  // The router's own message, untranslated, is kept as the detail line: it is
  // what an operator has to report, even when it does not say enough here.
  details =
    (error instanceof Error && error.message ? error.message : details) ??
    undefined

  return (
    <div className="flex min-h-svh flex-col items-center justify-center gap-4 p-6">
      <div className="flex w-full max-w-md flex-col gap-2">
        <ErrorState
          title={title}
          message={message}
          onRetry={() => window.location.reload()}
        />
        {details && (
          <p
            dir="ltr"
            className="rounded-lg bg-muted px-3 py-2 text-start text-xs text-muted-foreground"
          >
            {details}
          </p>
        )}
      </div>
      <Link
        to="/login"
        className="text-sm text-muted-foreground underline-offset-4 hover:underline"
      >
        بازگشت به صفحه ورود
      </Link>
    </div>
  )
}
