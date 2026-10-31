import type * as React from "react"
import { Link } from "react-router"

import { Separator } from "@workspace/ui/components/separator"
import { cn } from "@workspace/ui/lib/utils"

export interface Crumb {
  label: string
  to?: string
}

export function Breadcrumbs({
  items,
  className,
}: {
  items: Crumb[]
  className?: string
}) {
  return (
    <nav
      aria-label="مسیر صفحه"
      className={cn("text-muted-foreground text-sm", className)}
    >
      <ol className="flex flex-wrap items-center gap-1.5">
        {items.map((item, index) => {
          const last = index === items.length - 1
          return (
            <li
              key={`${item.label}-${index}`}
              className="flex items-center gap-1.5"
            >
              {item.to && !last ? (
                <Link
                  to={item.to}
                  className="hover:text-foreground transition-colors"
                >
                  {item.label}
                </Link>
              ) : (
                <span
                  aria-current={last ? "page" : undefined}
                  className={last ? "text-foreground" : undefined}
                >
                  {item.label}
                </span>
              )}
              {!last && (
                <span aria-hidden="true" className="text-muted-foreground/60">
                  /
                </span>
              )}
            </li>
          )
        })}
      </ol>
    </nav>
  )
}

// PageHeader is the one page top: title, description and the page's primary
// actions. Breadcrumbs live in the layout's header, above every page.
export function PageHeader({
  title,
  description,
  actions,
}: {
  title: string
  description?: string
  actions?: React.ReactNode
}) {
  return (
    <header className="flex flex-col gap-4">
      <div className="flex flex-col gap-3 sm:flex-row sm:items-start sm:justify-between">
        <div className="flex min-w-0 flex-col gap-1">
          <h1 className="text-xl font-semibold tracking-tight">{title}</h1>
          {description && (
            <p className="text-muted-foreground max-w-2xl text-sm">
              {description}
            </p>
          )}
        </div>
        {actions && (
          <div className="flex shrink-0 flex-wrap items-center gap-2">
            {actions}
          </div>
        )}
      </div>
      <Separator />
    </header>
  )
}
