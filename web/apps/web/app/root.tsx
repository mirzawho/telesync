import {
  Links,
  Meta,
  Outlet,
  Scripts,
  ScrollRestoration,
  isRouteErrorResponse,
} from "react-router"

import type { Route } from "./+types/root"
import { ToastProvider } from "@workspace/ui/components/toast"
import "@workspace/ui/globals.css"

export const links: Route.LinksFunction = () => [
  {
    rel: "icon",
    href: `${import.meta.env.BASE_URL}favicon.svg`,
    type: "image/svg+xml",
  },
]

// Persian-first and right to left for every page; the login page shares it.
export function Layout({ children }: { children: React.ReactNode }) {
  return (
    <html lang="fa" dir="rtl">
      <head>
        <meta charSet="utf-8" />
        <meta name="viewport" content="width=device-width, initial-scale=1" />
        <Meta />
        <Links />
      </head>
      <body>
        <div id="root" className="h-full">
          {children}
        </div>
        <ScrollRestoration />
        <Scripts />
      </body>
    </html>
  )
}

// The app is a single page: the first load renders the matched route, and
// every route after it is drawn by the browser router. Until the browser has
// the router, the shell shows a quiet placeholder.
export function HydrateFallback() {
  return (
    <div className="flex min-h-svh items-center justify-center bg-background text-sm text-muted-foreground">
      در حال بارگذاری…
    </div>
  )
}

// The provider root for every page in the panel — login included — so a route
// can call useToast() without its own mount. One ToastProvider only: the panel
// layout must not add a second one.
export default function App() {
  return (
    <ToastProvider>
      <Outlet />
    </ToastProvider>
  )
}

export function ErrorBoundary({ error }: Route.ErrorBoundaryProps) {
  let status = "خطا"
  let message = "خطایی پیش‌بینی‌نشده رخ داد."

  if (isRouteErrorResponse(error)) {
    status = String(error.status)
    message =
      error.status === 404
        ? "این صفحه پیدا نشد."
        : error.statusText || String(error.data || message)
  } else if (import.meta.env.DEV && error && error instanceof Error) {
    message = error.message
  }

  return (
    <main className="mx-auto flex min-h-svh max-w-md flex-col items-center justify-center gap-2 p-6 text-center">
      <p className="text-sm text-muted-foreground">{status}</p>
      <h1 className="text-lg font-semibold">{message}</h1>
      <a
        href={import.meta.env.BASE_URL}
        className="mt-2 text-sm underline-offset-4 hover:underline"
      >
        بازگشت به پیشخان
      </a>
    </main>
  )
}
