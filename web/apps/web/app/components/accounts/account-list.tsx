import {
  InfoIcon,
  CopyIcon,
  Link2Icon,
  LogOutIcon,
  MoreVerticalIcon,
  PlugZapIcon,
  RadioTowerIcon,
  SendIcon,
  TagsIcon,
  Trash2Icon,
  UnplugIcon,
} from "lucide-react"
import * as React from "react"

import {
  accountActionsFor,
  accountStatusText,
  accountStatusTone,
  accountTitle,
  serverLabel,
  type AccountAction,
  type AccountTone,
} from "@/lib/telegram-account"
import type { TelegramAccount } from "@/lib/types"
import { Badge } from "@workspace/ui/components/badge"
import { Avatar, AvatarFallback } from "@workspace/ui/components/avatar"
import { Button } from "@workspace/ui/components/button"
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogHeader,
  DialogTitle,
} from "@workspace/ui/components/dialog"
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from "@workspace/ui/components/dropdown-menu"
import { cn } from "@workspace/ui/lib/utils"
import {
  Table,
  TableActionsCell,
  TableActionsHead,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@workspace/ui/components/table"

// What is never shown is as important as what is: no session bytes, credential,
// full phone number, or invented profile image. The phone shown in details is
// only the masked value supplied by the API.

const TONE_VARIANT: Record<
  AccountTone,
  "success" | "warning" | "muted" | "secondary" | "destructive"
> = {
  ok: "success",
  busy: "secondary",
  warn: "warning",
  off: "muted",
  bad: "destructive",
}

// AccountIcon is the network the account is on. It names the network, not the
// brand of a session: a custom backend is drawn as a server, not as Telegram.
export function AccountIcon({
  account,
  className,
}: {
  account: TelegramAccount
  className?: string
}) {
  const Icon = account.network_type === "custom" ? RadioTowerIcon : SendIcon
  return (
    <div
      aria-hidden="true"
      className={cn(
        "flex size-10 shrink-0 items-center justify-center rounded-full bg-muted text-muted-foreground",
        className
      )}
    >
      <Icon className="size-5" />
    </div>
  )
}

export function AccountStatusBadge({
  account,
  className,
}: {
  account: TelegramAccount
  className?: string
}) {
  return (
    <Badge
      variant={TONE_VARIANT[accountStatusTone(account)]}
      className={className}
    >
      {accountStatusText(account)}
    </Badge>
  )
}

export function AccountAvatar({
  account,
  className,
}: {
  account: TelegramAccount
  className?: string
}) {
  const name = accountTitle(account)
  const initial = [...name.trim()][0] || "?"
  return (
    <Avatar aria-hidden="true" className={cn("size-10", className)}>
      <AvatarFallback className="bg-primary/10 font-semibold text-primary">
        {initial}
      </AvatarFallback>
    </Avatar>
  )
}

export type AccountActionHandler = (
  account: TelegramAccount,
  action: AccountAction
) => void

export function AccountList({
  accounts,
  onAction,
  now = Date.now(),
  className,
}: {
  accounts: TelegramAccount[]
  onAction: AccountActionHandler
  now?: number
  className?: string
}) {
  return (
    <div className="rounded-xl border bg-card">
      <Table
        dir="rtl"
        data-account-list=""
        aria-label="حساب‌های من"
        className={className}
      >
        <TableHeader>
          <TableRow>
            <TableHead className="min-w-[240px]">حساب</TableHead>
            <TableHead className="min-w-[150px]">سرور</TableHead>
            <TableHead>وضعیت</TableHead>
            <TableHead className="min-w-[170px]">آخرین اتصال</TableHead>
            <TableActionsHead className="w-12 text-end">
              عملیات
            </TableActionsHead>
          </TableRow>
        </TableHeader>
        <TableBody>
          {accounts.map((account) => (
            <AccountTableRow
              key={account.id}
              account={account}
              onAction={onAction}
              now={now}
            />
          ))}
        </TableBody>
      </Table>
    </div>
  )
}

function AccountTableRow({
  account,
  onAction,
  now = Date.now(),
}: {
  account: TelegramAccount
  onAction: AccountActionHandler
  now?: number
}) {
  const actions = accountActionsFor(account)
  const username = account.remote_username
  return (
    <TableRow data-account-id={account.id}>
      <TableCell className="min-w-[240px]">
        <div className="flex min-w-0 items-center gap-2">
          <AccountAvatar account={account} className="size-8" />
          <div className="flex min-w-0 items-baseline gap-2">
            <span className="max-w-[150px] truncate text-sm font-medium">
              {accountTitle(account)}
            </span>
            {username && accountTitle(account) !== `@${username}` ? (
              <span
                dir="ltr"
                className="shrink-0 text-xs text-muted-foreground"
              >
                @{username}
              </span>
            ) : null}
          </div>
        </div>
      </TableCell>
      <TableCell>
        {serverLabel(account.server_key, account.network_type)}
      </TableCell>
      <TableCell>
        <AccountStatusBadge account={account} />
      </TableCell>
      <TableCell dir="ltr" className="text-start">
        {formatAccountDate(account.last_connected_at)}
      </TableCell>
      <TableActionsCell className="w-12 text-end">
        {actions.length > 0 && (
          <AccountMenu
            account={account}
            actions={actions}
            onAction={onAction}
          />
        )}
      </TableActionsCell>
    </TableRow>
  )
}

export function formatAccountDate(value: string | null | undefined): string {
  if (!value) return "—"
  const date = new Date(value)
  if (!Number.isFinite(date.getTime())) return "—"
  return new Intl.DateTimeFormat("fa-IR", {
    dateStyle: "medium",
    timeStyle: "short",
  }).format(date)
}

function AccountMenu({
  account,
  actions,
  onAction,
}: {
  account: TelegramAccount
  actions: AccountAction[]
  onAction: AccountActionHandler
}) {
  const has = (action: AccountAction) => actions.includes(action)
  return (
    <DropdownMenu>
      <DropdownMenuTrigger
        render={
          <Button
            variant="ghost"
            size="icon-sm"
            aria-label={`عملیات حساب ${accountTitle(account)}`}
          />
        }
      >
        <MoreVerticalIcon />
      </DropdownMenuTrigger>
      <DropdownMenuContent align="end">
        {has("connect") && (
          <DropdownMenuItem onClick={() => onAction(account, "connect")}>
            <PlugZapIcon />
            {account.last_connected_at ? "اتصال دوباره" : "اتصال"}
          </DropdownMenuItem>
        )}
        {has("disconnect") && (
          <DropdownMenuItem onClick={() => onAction(account, "disconnect")}>
            <UnplugIcon />
            قطع اتصال
          </DropdownMenuItem>
        )}
        {has("channels") && (
          <DropdownMenuItem onClick={() => onAction(account, "channels")}>
            <TagsIcon />
            کانال‌های این حساب
          </DropdownMenuItem>
        )}
        {has("details") && (
          <DropdownMenuItem onClick={() => onAction(account, "details")}>
            <InfoIcon />
            جزئیات حساب
          </DropdownMenuItem>
        )}
        {has("logout") && (
          <>
            <DropdownMenuSeparator />
            <DropdownMenuItem onClick={() => onAction(account, "logout")}>
              <LogOutIcon />
              خروج از حساب تلگرام
            </DropdownMenuItem>
          </>
        )}
        {has("remove") && (
          <DropdownMenuItem
            variant="destructive"
            onClick={() => onAction(account, "remove")}
          >
            <Trash2Icon />
            حذف حساب
          </DropdownMenuItem>
        )}
      </DropdownMenuContent>
    </DropdownMenu>
  )
}

function DetailRow({
  label,
  children,
}: {
  label: string
  children: React.ReactNode
}) {
  return (
    <div className="flex min-w-0 items-center justify-between gap-4 border-b py-2.5 last:border-b-0">
      <span className="shrink-0 text-sm text-muted-foreground">{label}</span>
      <span className="min-w-0 text-end text-sm">{children}</span>
    </div>
  )
}

// AccountDetailsContent is what the details view shows about one account and
// nothing else. There is no field here for a session, a token or a full phone
// number, because there is no such field on the record. It is its own component
// so the dialog's portal cannot be the only place this content exists.
export function AccountDetailsContent({
  account,
  now = Date.now(),
}: {
  account: TelegramAccount | null
  now?: number
}) {
  if (!account) return null
  return (
    <div className="flex min-w-0 flex-col">
      <div className="mb-5 flex flex-col items-center gap-1.5 text-center">
        <AccountAvatar account={account} className="size-20 text-2xl" />
        <h3 className="max-w-full text-lg font-bold break-words">
          {accountTitle(account)}
        </h3>
        {account.remote_username &&
          accountTitle(account) !== `@${account.remote_username}` && (
            <p dir="ltr" className="text-sm text-muted-foreground">
              @{account.remote_username}
            </p>
          )}
        <AccountStatusBadge account={account} className="mt-1" />
        <p className="text-xs text-muted-foreground">
          {serverLabel(account.server_key, account.network_type)}
        </p>
      </div>

      <div className="divide-y rounded-lg bg-muted/30 px-3">
        <DetailRow label="سرور">
          {serverLabel(account.server_key, account.network_type)}
        </DetailRow>
        <DetailRow label="وضعیت اتصال">
          <AccountStatusBadge account={account} />
        </DetailRow>
        <DetailRow label="شماره تلفن">
          <span dir="ltr" className="inline-block">
            {account.phone_number_masked || "—"}
          </span>
        </DetailRow>
        <DetailRow label="آخرین اتصال">
          <span dir="ltr" className="inline-block">
            {formatAccountDate(account.last_connected_at)}
          </span>
        </DetailRow>
      </div>

      <div className="mt-3 flex items-center justify-between gap-3 text-xs text-muted-foreground">
        <span>جزئیات فنی</span>
        <div className="flex min-w-0 items-center gap-2">
          <span dir="ltr" className="truncate font-mono">
            {account.id}
          </span>
          <Button
            type="button"
            variant="ghost"
            size="sm"
            aria-label="کپی شناسهٔ حساب"
            onClick={() => {
              if (typeof navigator !== "undefined" && navigator.clipboard) {
                void navigator.clipboard.writeText(account.id)
              }
            }}
          >
            <CopyIcon />
            کپی
          </Button>
        </div>
      </div>
    </div>
  )
}

export function AccountDetailsDialog({
  account,
  open,
  onOpenChange,
  now = Date.now(),
}: {
  account: TelegramAccount | null
  open: boolean
  onOpenChange: (open: boolean) => void
  now?: number
}) {
  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className={ACCOUNT_DETAILS_DIALOG_CLASS}>
        <DialogHeader>
          <DialogTitle className="sr-only">جزئیات حساب</DialogTitle>
          <DialogDescription className="sr-only">
            اطلاعات حساب تلگرامی شما
          </DialogDescription>
        </DialogHeader>
        <AccountDetailsContent account={account} now={now} />
      </DialogContent>
    </Dialog>
  )
}

export const ACCOUNT_DETAILS_DIALOG_CLASS =
  "max-h-[min(90dvh,42rem)] min-w-0 gap-3 overflow-y-auto overflow-x-hidden p-5 sm:w-[min(100%-2rem,440px)]"

// NoAccountsState is what a reader with no account sees anywhere an account is
// needed. The words are the product's own: it says to add an account, and it
// never says "permission denied", which would send them to an administrator
// who cannot help them.
export function NoAccountsState({
  onAdd,
  icon: Icon = Link2Icon,
  compact = false,
}: {
  onAdd: () => void
  icon?: React.ComponentType<{ className?: string }>
  compact?: boolean
}) {
  return (
    <div
      className={cn(
        "flex flex-col items-center justify-center gap-3 px-6 py-12 text-center",
        compact && "gap-2 px-4 py-7"
      )}
    >
      <div className="flex size-12 items-center justify-center rounded-full bg-muted text-muted-foreground">
        <Icon className="size-6" />
      </div>
      <p data-slot="no-accounts-message" className="text-sm font-medium">
        هنوز هیچ حساب تلگرامی متصل نکرده‌اید.
      </p>
      <p className="max-w-sm text-sm text-muted-foreground">
        برای دیدن کانال‌ها و ارسال پیام، نخست یکی از حساب‌های تلگرامی خودتان را
        به پنل متصل کنید.
      </p>
      <Button
        onClick={onAdd}
        data-slot="add-account-action"
        size={compact ? "sm" : "default"}
      >
        <Link2Icon data-icon="inline-start" />
        افزودن حساب
      </Button>
    </div>
  )
}
