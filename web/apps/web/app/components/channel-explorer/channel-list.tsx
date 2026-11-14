import {
  InfoIcon,
  MoreHorizontalIcon,
  MoreVerticalIcon,
  PlusIcon,
  TagsIcon,
  Trash2Icon,
} from "lucide-react"
import * as React from "react"
import { Link, useSearchParams } from "react-router"

import { ChannelAvatar } from "@/components/channel-avatar"
import {
  AccountMultiSelect,
  AccountOriginLabel,
} from "@/components/accounts/account-multi-select"
import { SearchInput } from "@/components/search-input"
import {
  accountOf,
  accountOriginKey,
  needsAccountOrigin,
} from "@/lib/account-channel-scope"
import type { Category, Channel, TelegramAccount } from "@/lib/types"
import { Badge } from "@workspace/ui/components/badge"
import { Button } from "@workspace/ui/components/button"
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from "@workspace/ui/components/dropdown-menu"
import { ScrollArea } from "@workspace/ui/components/scroll-area"
import { cn } from "@workspace/ui/lib/utils"

// The channel list is a local workspace pane beside the message timeline — not
// the application's navigation, which the sidebar keeps owning. It is search,
// a row of category chips, and the channels themselves.

export function ChannelListPane({
  channels,
  categories,
  filed,
  selectedId,
  search,
  onSearchChange,
  selectedCategories,
  onToggleCategory,
  accounts = [],
  selectedAccounts = [],
  onToggleAccount,
  onSelectAllAccounts,
  accountsError,
  showAccountControl = false,
  onAdd,
  onCreate,
  canCreate = true,
  canJoin = true,
  onInformation,
  onEditCategories,
  onDelete,
  hasChannels = true,
  filtersActive = false,
  onClearFilters,
  className,
}: {
  channels: Channel[]
  categories: Category[]
  // filed: which categories hold which channel, keyed by the channel's local
  // id. A channel the reader can see but that is filed nowhere is simply in no
  // category.
  filed: Record<number, number[]>
  // selectedId is the local managed-channel id of the channel whose messages
  // are showing. It is never a Telegram channel id.
  selectedId: number | null
  search: string
  onSearchChange: (value: string) => void
  selectedCategories: number[]
  onToggleCategory: (id: number) => void
  // accounts is the reader's own, and nothing else: it is what tells two rows
  // of the same remote channel apart and what the account control offers.
  accounts?: TelegramAccount[]
  selectedAccounts?: string[]
  onToggleAccount?: (id: string) => void
  onSelectAllAccounts?: () => void
  accountsError?: string
  // showAccountControl is decided by the caller from `needsAccountControl`: the
  // control earns its place only where several accounts share one server.
  showAccountControl?: boolean
  onAdd: () => void
  onCreate?: () => void
  canCreate?: boolean
  canJoin?: boolean
  onInformation?: (channel: Channel) => void
  onEditCategories: (channel: Channel) => void
  onDelete: (channel: Channel) => void
  // hasChannels is about the whole workspace, not the current filter: it
  // separates "nothing registered yet" from "nothing matched".
  hasChannels?: boolean
  filtersActive?: boolean
  onClearFilters?: () => void
  className?: string
}) {
  // Every row carries its account as soon as one account could be mistaken for
  // another. A single account needs no label: the row is unambiguous already.
  const showOrigin = needsAccountOrigin(channels, accounts)
  return (
    <aside
      data-slot="channel-list-pane"
      className={cn(
        "flex min-h-0 w-full shrink-0 flex-col border-border bg-background lg:w-[20rem] lg:border-e",
        className
      )}
    >
      <div className="flex flex-col gap-2.5 border-b p-3">
        <div className="flex items-center gap-2">
          <SearchInput
            name="q"
            value={search}
            onChange={onSearchChange}
            placeholder="جستجو در کانال‌ها..."
            className="min-w-0 flex-1"
          />
          {/* Adding a channel lives here, in the list it adds to, rather than
              in a page header above the workspace. */}
          {(canCreate && onCreate || canJoin) && <DropdownMenu>
            <DropdownMenuTrigger render={<Button variant="outline" size="icon" aria-label="عملیات کانال‌ها" className="shrink-0" />}>
              <MoreVerticalIcon />
            </DropdownMenuTrigger>
            <DropdownMenuContent align="end">
              {canCreate && onCreate && <DropdownMenuItem onClick={onCreate}><PlusIcon />ساخت کانال</DropdownMenuItem>}
              {canJoin && <DropdownMenuItem onClick={onAdd}><PlusIcon />پیوستن به کانال</DropdownMenuItem>}
            </DropdownMenuContent>
          </DropdownMenu>}
        </div>
        <div data-slot="channel-filter-toolbar" dir="rtl" className="flex min-w-0 items-center gap-2">
          {showAccountControl && (
            <AccountMultiSelect
              accounts={accounts}
              selected={selectedAccounts}
              onToggle={onToggleAccount ?? (() => undefined)}
              onSelectAll={onSelectAllAccounts ?? (() => undefined)}
              error={accountsError}
            />
          )}
          <ChannelCategoryChips
            categories={categories}
            selected={selectedCategories}
            onToggle={onToggleCategory}
            className="min-w-0 flex-1"
          />
        </div>
      </div>

      <ScrollArea className="min-h-0 flex-1">
        {channels.length === 0 ? (
          <div className="flex flex-col items-center gap-2 px-4 py-10 text-center">
            <p className="text-muted-foreground text-sm">
              {!hasChannels
                ? "هنوز کانالی ثبت نشده است"
                : filtersActive
                  ? "کانالی با فیلترهای انتخاب‌شده پیدا نشد."
                  : "هنوز کانالی ثبت نشده است"}
            </p>
            {filtersActive && onClearFilters ? (
              <Button variant="outline" size="sm" onClick={onClearFilters}>
                پاک کردن فیلترها
              </Button>
            ) : !hasChannels && canJoin ? (
              <Button variant="outline" size="sm" onClick={onAdd}>
                <PlusIcon data-icon="inline-start" />
                پیوستن به کانال
              </Button>
            ) : null}
          </div>
        ) : (
          <ul className="flex flex-col gap-0.5 p-2">
            {channels.map((channel) => (
              <ChannelListItem
                // The key is the account-scoped origin, so two rows for the same
                // remote channel through two accounts can never collapse into
                // one another in the list.
                key={accountOriginKey(channel)}
                channel={channel}
                account={accountOf(channel, accounts)}
                showAccountOrigin={showOrigin}
                categories={filed[channel.id] ?? []}
                categoryNames={categories}
                selected={channel.id === selectedId}
                onEditCategories={onEditCategories}
                onDelete={onDelete}
                onInformation={onInformation}
              />
            ))}
          </ul>
        )}
      </ScrollArea>
    </aside>
  )
}

// ChannelCategoryChips is one scrolling row of category toggles.
//
// They are real buttons with a pressed state, not a dropdown: a reader filters
// by tapping what they want, and taps it again to take it off. Several can be
// on at once and a channel in any of them counts.
export function ChannelCategoryChips({
  categories,
  selected,
  onToggle,
  className,
}: {
  categories: Category[]
  selected: number[]
  onToggle: (id: number) => void
  className?: string
}) {
  return (
    <div
      data-slot="category-chips"
      role="group"
      aria-label="فیلتر دسته‌بندی"
      className={cn(
        "flex flex-nowrap items-center gap-1.5 overflow-x-auto overflow-y-hidden pb-0.5",
        className
      )}
    >
      {categories.map((category) => {
        const active = selected.includes(category.id)
        return (
          <Badge
            key={category.id}
            variant={active ? "default" : "muted"}
            render={
              <button
                type="button"
                aria-pressed={active}
                onClick={() => onToggle(category.id)}
                className={cn(
                  "cursor-pointer px-2.5 py-1 transition-opacity",
                  "focus-visible:border-ring focus-visible:ring-3 focus-visible:ring-ring/50 focus-visible:outline-none",
                  "hover:opacity-80"
                )}
              />
            }
          >
            {category.name}
          </Badge>
        )
      })}
    </div>
  )
}

// ChannelListItem is one row of the list.
//
// The whole row selects the channel; the menu floats above that hit area so
// opening it never also opens the channel. A selected row is marked by a
// background and an edge bar rather than a heavy border, and is announced as
// the current one.
function ChannelListItem({
  channel,
  account,
  showAccountOrigin,
  categories,
  categoryNames,
  selected,
  onEditCategories,
  onDelete,
  onInformation,
}: {
  channel: Channel
  // account is the account this row is reached through, resolved from the
  // row's own record. It is never taken from whichever server filter is on
  // screen: a filter is a view over a list, not authority over a row.
  account?: TelegramAccount
  showAccountOrigin?: boolean
  categories: number[]
  categoryNames: Category[]
  selected: boolean
  onEditCategories: (channel: Channel) => void
  onDelete: (channel: Channel) => void
  onInformation?: (channel: Channel) => void
}) {
  // Choosing a channel carries the search and the category chips with it, so
  // opening one does not throw away the filter the reader set up to find it.
  const [searchParams] = useSearchParams()
  return (
    <li
      data-slot="channel-list-item"
      data-selected={selected ? "" : undefined}
      className={cn(
        "relative rounded-lg transition-colors",
        selected ? "bg-accent" : "hover:bg-muted/60"
      )}
    >
      {selected && (
        <span
          aria-hidden="true"
          className="bg-primary absolute inset-y-1.5 start-0 w-1 rounded-full"
        />
      )}
      <Link
        to={{
          pathname: `/channels/${channel.id}/messages`,
          search: searchParams.toString(),
        }}
        aria-current={selected ? "page" : undefined}
        className="absolute inset-0 z-0 rounded-lg"
      >
        <span className="sr-only">مشاهدهٔ پیام‌های {channel.title}</span>
      </Link>

      <div className="pointer-events-none flex items-center gap-3 px-2.5 py-2">
        <ChannelAvatar
          id={channel.id}
          title={channel.title}
          className="size-9 shrink-0"
        />
        <div className="flex min-w-0 flex-1 flex-col">
          <span className="truncate text-sm font-medium">{channel.title}</span>
          <span className="text-muted-foreground truncate text-xs">
            {channel.username ? `@${channel.username}` : channel.client_key}
          </span>
          {/* The account is named whenever one account could be mistaken for
              another, so two rows of the same remote channel never look like
              one entry twice. */}
          {showAccountOrigin && (
            <AccountOriginLabel
              account={account}
              className="text-muted-foreground/90 mt-0.5 truncate text-[0.7rem]"
            />
          )}
          {categories.length > 0 && (
            <span className="text-muted-foreground/80 mt-0.5 truncate text-[0.7rem]">
              {categories
                .map(
                  (id) =>
                    categoryNames.find((category) => category.id === id)?.name
                )
                .filter(Boolean)
                .join("، ")}
            </span>
          )}
        </div>

        <div className="pointer-events-auto relative z-10 shrink-0">
          <DropdownMenu>
            <DropdownMenuTrigger
              render={
                <Button
                  variant="ghost"
                  size="icon-sm"
                  aria-label={`عملیات کانال ${channel.title}`}
                />
              }
            >
              <MoreHorizontalIcon />
            </DropdownMenuTrigger>
            <DropdownMenuContent align="end">
              {onInformation && <DropdownMenuItem onClick={() => onInformation(channel)}><InfoIcon />اطلاعات کانال</DropdownMenuItem>}
              <DropdownMenuItem onClick={() => onEditCategories(channel)}>
                <TagsIcon />
                ویرایش دسته‌بندی
              </DropdownMenuItem>
              <DropdownMenuSeparator />
              <DropdownMenuItem
                variant="destructive"
                onClick={() => onDelete(channel)}
              >
                <Trash2Icon />
                حذف کانال
              </DropdownMenuItem>
            </DropdownMenuContent>
          </DropdownMenu>
        </div>
      </div>
    </li>
  )
}
