import {
  MoreHorizontalIcon,
  PlusIcon,
  TagsIcon,
  Trash2Icon,
} from "lucide-react"
import * as React from "react"
import { Link, useSearchParams } from "react-router"

import { ChannelAvatar } from "@/components/channel-avatar"
import { SearchInput } from "@/components/search-input"
import type { Category, Channel } from "@/lib/types"
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
  onAdd,
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
  onAdd: () => void
  onEditCategories: (channel: Channel) => void
  onDelete: (channel: Channel) => void
  // hasChannels is about the whole workspace, not the current filter: it
  // separates "nothing registered yet" from "nothing matched".
  hasChannels?: boolean
  filtersActive?: boolean
  onClearFilters?: () => void
  className?: string
}) {
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
          <Button
            variant="outline"
            size="icon"
            onClick={onAdd}
            title="کانال جدید"
            aria-label="کانال جدید"
            className="shrink-0"
          >
            <PlusIcon data-icon="inline-start" />
          </Button>
        </div>
        <ChannelCategoryChips
          categories={categories}
          selected={selectedCategories}
          onToggle={onToggleCategory}
        />
      </div>

      <ScrollArea className="min-h-0 flex-1">
        {channels.length === 0 ? (
          <div className="flex flex-col items-center gap-2 px-4 py-10 text-center">
            <p className="text-muted-foreground text-sm">
              {!hasChannels
                ? "هنوز کانالی ثبت نشده است"
                : filtersActive
                  ? "نتیجه‌ای پیدا نشد"
                  : "هنوز کانالی ثبت نشده است"}
            </p>
            {filtersActive && onClearFilters ? (
              <Button variant="outline" size="sm" onClick={onClearFilters}>
                پاک کردن فیلترها
              </Button>
            ) : !hasChannels ? (
              <Button variant="outline" size="sm" onClick={onAdd}>
                <PlusIcon data-icon="inline-start" />
                کانال جدید
              </Button>
            ) : null}
          </div>
        ) : (
          <ul className="flex flex-col gap-0.5 p-2">
            {channels.map((channel) => (
              <ChannelListItem
                key={channel.id}
                channel={channel}
                categories={filed[channel.id] ?? []}
                categoryNames={categories}
                selected={channel.id === selectedId}
                onEditCategories={onEditCategories}
                onDelete={onDelete}
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
  categories,
  categoryNames,
  selected,
  onEditCategories,
  onDelete,
}: {
  channel: Channel
  categories: number[]
  categoryNames: Category[]
  selected: boolean
  onEditCategories: (channel: Channel) => void
  onDelete: (channel: Channel) => void
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
