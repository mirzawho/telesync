import { ChevronDownIcon, LoaderCircleIcon, UsersIcon } from "lucide-react"
import * as React from "react"

import {
  AccountOption,
  accountOptionLabel,
} from "@/components/accounts/account-option"
import { searchAccounts } from "@/lib/account-filter"
import type { TelegramAccount } from "@/lib/types"
import { Button } from "@workspace/ui/components/button"
import { Checkbox } from "@workspace/ui/components/checkbox"
import { Popover } from "@base-ui/react/popover"

// AccountMultiSelect picks which of the reader's own accounts the workspace is
// looking through.
//
// It exists only where the server filter cannot say it: two accounts on one
// server are the same key twice, and no amount of server filtering tells them
// apart. Every option is an account the caller already owns — this control
// cannot name another user's account, because the list it is built from does
// not contain one.
export function AccountMultiSelect({
  accounts,
  selected,
  onToggle,
  onSelectAll,
  error,
  loading = false,
  onRetry,
  label = "فیلتر حساب‌ها",
}: {
  accounts: TelegramAccount[]
  selected: string[]
  onToggle: (id: string) => void
  onSelectAll: () => void
  error?: string
  loading?: boolean
  onRetry?: () => void
  label?: string
}) {
  const [query, setQuery] = React.useState("")
  const countLabel = new Intl.NumberFormat("fa").format(selected.length)
  const only = selected.length === 1 ? accounts.find((a) => a.id === selected[0]) : undefined
  const triggerLabel =
    selected.length === 0
      ? "همه حساب‌ها"
      : only
        ? accountOptionLabel(only)
        : `${countLabel} حساب`
  const matches = searchAccounts(accounts, query)
  const checked = (id: string) => selected.includes(id)
  const allChecked = selected.length === 0

  return (
    <Popover.Root>
      <Popover.Trigger
        render={
          <Button
            data-slot="account-multi-select"
            variant="outline"
            size="sm"
            className="h-7 max-w-32 shrink-0 gap-1 rounded-full border-0 bg-muted px-2"
            aria-label={`${label}: ${triggerLabel}`}
            disabled={loading}
          />
        }
      >
        {loading ? <LoaderCircleIcon className="animate-spin" /> : null}
        <span className="truncate">{error ? "حساب‌ها نامشخص" : triggerLabel}</span>
        <ChevronDownIcon className="size-3.5 shrink-0" />
      </Popover.Trigger>
      <Popover.Portal>
        <Popover.Positioner
          side="bottom"
          align="start"
          sideOffset={5}
          className="z-50 max-w-[calc(100vw-1rem)] outline-none"
        >
          <Popover.Popup
            dir="rtl"
            className="w-[min(18rem,calc(100vw-1rem))] rounded-lg border bg-popover p-2 text-popover-foreground shadow-md"
          >
            <label className="sr-only" htmlFor="account-filter-search">
              جستجو در حساب‌ها
            </label>
            <input
              id="account-filter-search"
              type="search"
              value={query}
              onChange={(event) => setQuery(event.target.value)}
              placeholder="جستجو در حساب‌ها..."
              className="mb-2 h-8 w-full rounded-md border border-input bg-transparent px-2 text-sm outline-none focus-visible:ring-2 focus-visible:ring-ring"
            />
            {error ? (
              <div
                className="text-destructive flex items-center justify-between gap-2 py-2 text-xs"
                role="alert"
              >
                <span>بارگذاری حساب‌ها ناموفق بود.</span>
                {onRetry && (
                  <button type="button" className="underline" onClick={onRetry}>
                    تلاش دوباره
                  </button>
                )}
              </div>
            ) : accounts.length === 0 ? (
              <p className="text-muted-foreground py-2 text-center text-xs">
                حسابی متصل نیست.
              </p>
            ) : (
              <>
                <div className="max-h-52 overflow-y-auto" role="group" aria-label={label}>
                  <button
                    type="button"
                    role="checkbox"
                    aria-checked={allChecked}
                    onClick={onSelectAll}
                    className="hover:bg-accent flex w-full items-center gap-2 rounded px-2 py-1.5 text-right text-sm"
                  >
                    <Checkbox checked={allChecked} tabIndex={-1} aria-hidden="true" />
                    <span>همه حساب‌ها</span>
                  </button>
                  <div className="my-1 border-t" />
                  {matches.map((account) => (
                    <button
                      key={account.id}
                      type="button"
                      role="checkbox"
                      aria-checked={checked(account.id)}
                      onClick={() => onToggle(account.id)}
                      className="hover:bg-accent flex w-full items-center gap-2 rounded px-2 py-1.5 text-right text-sm"
                    >
                      <Checkbox
                        checked={checked(account.id)}
                        tabIndex={-1}
                        aria-hidden="true"
                      />
                      <AccountOption
                        account={account}
                        layout="inline"
                        className="flex-1"
                      />
                    </button>
                  ))}
                  {matches.length === 0 && (
                    <p className="text-muted-foreground py-3 text-center text-xs">
                      حسابی یافت نشد.
                    </p>
                  )}
                </div>
                <div className="mt-1 border-t pt-1">
                  <button
                    type="button"
                    onClick={onSelectAll}
                    className="text-muted-foreground hover:bg-accent hover:text-foreground w-full rounded px-2 py-1.5 text-right text-xs"
                  >
                    بازنشانی
                  </button>
                </div>
              </>
            )}
          </Popover.Popup>
        </Popover.Positioner>
      </Popover.Portal>
    </Popover.Root>
  )
}

// AccountOriginLabel is the badge a channel row carries when the same remote
// channel could be reached through more than one of the reader's accounts. It
// names the account, never the server: one server can hold several accounts and
// a server key would not tell two rows apart.
export function AccountOriginLabel({
  account,
  className,
}: {
  account: TelegramAccount | undefined
  className?: string
}) {
  return (
    <span
      data-slot="account-origin"
      className={className}
      title="حسابی که این کانال از طریق آن در دسترس است"
    >
      <UsersIcon aria-hidden="true" className="me-1 inline size-3 align-[-0.15em]" />
      {account ? accountOptionLabel(account) : "حساب نامشخص"}
    </span>
  )
}
