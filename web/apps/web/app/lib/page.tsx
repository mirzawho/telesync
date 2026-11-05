import type * as React from "react"

import { PageHeader } from "@/components/page-header"
import { ErrorState } from "@/components/states"
import { errorMessage } from "@/lib/api"

// Every page under _panel names its section with a route `handle`, so the
// layout's breadcrumbs and the document title know it without owning the page's
// data.
export interface SectionHandle {
  section?: string
  // fullBleed asks the layout to drop the page column's max-width, centering
  // and gutters for this route. It is for a workspace that is the screen — the
  // channel explorer — not for a document page, and it is opt-in so every other
  // route keeps its margins.
  fullBleed?: boolean
  // hideBottomNav asks the shell to tuck the phone/tablet bottom navigation
  // away for this route: the message timeline reads edge to edge and the bar
  // would sit over the newest messages.
  hideBottomNav?: boolean
}

export function section(name: string): SectionHandle {
  return { section: name }
}

export function fullBleedSection(name: string): SectionHandle {
  return { section: name, fullBleed: true }
}

export function hiddenNavSection(name: string): SectionHandle {
  return { section: name, fullBleed: true, hideBottomNav: true }
}

export function pageMeta(name: string) {
  return [{ title: `${name} | تیل‌سینک` }]
}

// PageShell is the page frame: the title and description sit above whatever
// state the data is in, so the header does not jump when the data arrives.
export function PageShell({
  title,
  description,
  actions,
  children,
}: {
  title: string
  description?: string
  actions?: React.ReactNode
  children: React.ReactNode
}) {
  return (
    <div className="flex flex-col gap-6">
      <PageHeader title={title} description={description} actions={actions} />
      {children}
    </div>
  )
}

// PageError is a whole-page failure, with the layout and the page's title
// still in place.
export function PageError({ error }: { error: unknown }) {
  return (
    <ErrorState
      message={errorMessage(error)}
      onRetry={() => window.location.reload()}
    />
  )
}
