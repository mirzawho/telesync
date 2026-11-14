import { cn } from "@workspace/ui/lib/utils"
import { UserRoundIcon } from "lucide-react"

import {
  accountStatusText,
  isAccountConnected,
  serverLabel,
} from "@/lib/telegram-account"
import type { TelegramAccount } from "@/lib/types"

/** A non-sensitive primary label: never a phone number or opaque account ID. */
export function accountOptionLabel(account: TelegramAccount): string {
  const displayName = account.remote_display_name?.trim()
  if (displayName) return displayName
  const username = account.remote_username?.trim()
  if (username) return `@${username}`
  return "حساب تلگرام"
}

/**
 * Shared personal-account option rendering for both single and multiple
 * selectors. Props are intentionally UI-only; callers supply only accounts
 * returned by the authenticated personal-account API.
 */
export function AccountOption({
  account,
  layout = "stacked",
  className,
}: {
  account: TelegramAccount
  layout?: "stacked" | "inline"
  className?: string
}) {
  const connected = isAccountConnected(account)
  const label = accountOptionLabel(account)
  const username = account.remote_username?.trim()
  const showUsername = Boolean(username && label !== `@${username}`)
  const networkLabel = serverLabel(account.server_key, account.network_type)
  return (
    <span
      className={cn(
        "flex w-full min-w-0 items-center gap-2",
        className
      )}
    >
      <span
        aria-hidden="true"
        className={cn(
          "bg-muted text-muted-foreground flex shrink-0 items-center justify-center rounded-full",
          layout === "stacked" ? "size-8" : "size-7"
        )}
      >
        <UserRoundIcon className="size-4" />
      </span>
      <span className="flex min-w-0 flex-1 flex-col">
        <span className="min-w-0 truncate text-sm">{label}</span>
        {showUsername && (
          <span className="text-muted-foreground truncate text-xs" dir="ltr">
            @{username}
          </span>
        )}
        <span className="text-muted-foreground truncate text-xs">
          {networkLabel}
        </span>
      </span>
      <span
        className={cn(
          "shrink-0 text-xs",
          connected ? "text-muted-foreground" : "text-amber-700"
        )}
      >
        {accountStatusText(account)}
      </span>
    </span>
  )
}
