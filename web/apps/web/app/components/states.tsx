import {
  AlertTriangleIcon,
  InboxIcon,
  LoaderCircleIcon,
  RefreshCwIcon,
} from "lucide-react"
import type * as React from "react"

import { Button } from "@workspace/ui/components/button"
import { Card, CardContent } from "@workspace/ui/components/card"
import { Skeleton } from "@workspace/ui/components/skeleton"
import { cn } from "@workspace/ui/lib/utils"

export function EmptyState({
  icon: Icon = InboxIcon,
  title,
  description,
  action,
  className,
}: {
  icon?: React.ComponentType<{ className?: string }>
  title: string
  description?: string
  action?: React.ReactNode
  className?: string
}) {
  return (
    <div
      className={cn(
        "flex flex-col items-center justify-center gap-2 px-6 py-12 text-center",
        className
      )}
    >
      <div className="bg-muted text-muted-foreground flex size-10 items-center justify-center rounded-full">
        <Icon className="size-5" />
      </div>
      <p className="text-sm font-medium">{title}</p>
      {description && (
        <p className="text-muted-foreground max-w-sm text-sm">{description}</p>
      )}
      {action && <div className="pt-2">{action}</div>}
    </div>
  )
}

export function ErrorState({
  title = "بارگذاری ناموفق بود",
  message,
  onRetry,
}: {
  title?: string
  message: string
  onRetry?: () => void
}) {
  return (
    <Card className="border-destructive/30">
      <CardContent className="flex flex-col items-center gap-3 py-8 text-center">
        <div className="bg-destructive/10 text-destructive flex size-10 items-center justify-center rounded-full">
          <AlertTriangleIcon className="size-5" />
        </div>
        <div className="flex flex-col gap-1">
          <p className="text-sm font-medium">{title}</p>
          <p className="text-muted-foreground max-w-md text-sm">{message}</p>
        </div>
        {onRetry && (
          <Button variant="outline" size="sm" onClick={onRetry}>
            <RefreshCwIcon />
            تلاش دوباره
          </Button>
        )}
      </CardContent>
    </Card>
  )
}

export function LoadingState({
  label = "در حال بارگذاری…",
}: {
  label?: string
}) {
  return (
    <div className="text-muted-foreground flex items-center justify-center gap-2 py-12 text-sm">
      <LoaderCircleIcon className="size-4 animate-spin" aria-hidden="true" />
      <span role="status">{label}</span>
    </div>
  )
}

export function StatCardSkeleton() {
  return (
    <Card className="py-4">
      <CardContent className="flex flex-col gap-3">
        <Skeleton className="h-4 w-24" />
        <Skeleton className="h-7 w-16" />
        <Skeleton className="h-3 w-32" />
      </CardContent>
    </Card>
  )
}
