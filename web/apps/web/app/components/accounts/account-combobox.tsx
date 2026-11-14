import {
  Combobox,
  ComboboxChips,
  ComboboxChipsInput,
  ComboboxContent,
  ComboboxEmpty,
  ComboboxItem,
  ComboboxList,
  useComboboxAnchor,
} from "@workspace/ui/components/combobox"
import { cn } from "@workspace/ui/lib/utils"

import {
  accountStatusText,
  isAccountConnected,
} from "@/lib/telegram-account"
import {
  AccountOption,
  accountOptionLabel,
} from "@/components/accounts/account-option"
import type { TelegramAccount } from "@/lib/types"

// AccountCombobox picks one of the reader's own accounts — the identity a
// workflow runs as, a webhook observes under, a message is published with.
//
// It is the account counterpart of `TelegramServerCombobox`, and the difference
// matters: a server key names a configured backend, an account id names one
// person's authenticated session on it. A workflow must be bound to the second,
// because that is what decides whose channels it reads and whose identity it
// publishes under. Everything in `accounts` is already the caller's own; this
// component cannot name another user's account because the list has none.
export function AccountCombobox({
  accounts,
  value,
  onChange,
  placeholder = "— حساب را انتخاب کنید —",
  disabled,
  emptyText = "حسابی یافت نشد.",
  className,
  id,
  "aria-label": ariaLabel = "حساب تلگرام",
}: {
  accounts: TelegramAccount[]
  value: string | undefined
  onChange: (accountID: string | undefined) => void
  placeholder?: string
  disabled?: boolean
  emptyText?: string
  className?: string
  id?: string
  "aria-label"?: string
}) {
  const anchor = useComboboxAnchor()
  // A value that is not in `accounts` — a removed account, a stale id —
  // selects nothing; the field falls back to its placeholder rather than
  // silently showing the first account as though it had been chosen.
  const chosen =
    value === undefined || value === ""
      ? undefined
      : (accounts.find((account) => account.id === value) ?? null)

  return (
    <div className={cn("w-full", className)}>
      <Combobox
        items={accounts}
        autoHighlight
        disabled={disabled}
        value={chosen}
        onValueChange={(next) =>
          onChange((next as TelegramAccount | null)?.id)
        }
         itemToStringLabel={accountOptionLabel}
      >
        <ComboboxChips
          ref={anchor}
          className="w-full"
          data-disabled={disabled ? "" : undefined}
        >
          <ComboboxChipsInput
            id={id}
            aria-label={ariaLabel}
            placeholder={placeholder}
            className="text-sm"
          />
        </ComboboxChips>
        <ComboboxContent anchor={anchor}>
          <ComboboxEmpty>{emptyText}</ComboboxEmpty>
          <ComboboxList>
            {(account: TelegramAccount) => (
              <ComboboxItem key={account.id} value={account}>
                <AccountOption account={account} />
              </ComboboxItem>
            )}
          </ComboboxList>
        </ComboboxContent>
      </Combobox>
    </div>
  )
}

// AccountPausedNotice is what a workflow or webhook shows when the account it
// is bound to is no longer connected.
//
// It is deliberately a state and not a repair. The panel must never reroute a
// workflow through another account: one user's session is not a fallback for
// another (or for their own disconnected one), and quietly publishing under a
// different identity would be the worst possible outcome of a dropped
// connection. The reader picks a different account or reconnects this one.
export function AccountPausedNotice({
  account,
  onReconnect,
  className,
}: {
  account: TelegramAccount | undefined
  onReconnect?: () => void
  className?: string
}) {
  if (!account) {
    return (
      <p
        data-slot="account-unbound"
        role="status"
        className={cn(
          "bg-amber-500/10 text-amber-800 rounded-lg px-3 py-2 text-sm",
          className
        )}
      >
        این مورد هنوز به حسابی متصل نشده است و اجرا نمی‌شود. حسابی را انتخاب
        کنید تا فعال شود.
      </p>
    )
  }
  if (isAccountConnected(account)) return null
  return (
    <div
      data-slot="account-paused"
      role="status"
      className={cn(
        "bg-amber-500/10 text-amber-800 flex flex-wrap items-center gap-2 rounded-lg px-3 py-2 text-sm",
        className
      )}
    >
      <span className="min-w-0 flex-1">
        حساب «{accountOptionLabel(account)}» متصل نیست ({accountStatusText(account)}).
        این مورد متوقف مانده است و از طریق حساب دیگری اجرا نمی‌شود.
      </span>
      {onReconnect && (
        <button
          type="button"
          className="underline underline-offset-4"
          onClick={onReconnect}
        >
          اتصال دوباره
        </button>
      )}
    </div>
  )
}
