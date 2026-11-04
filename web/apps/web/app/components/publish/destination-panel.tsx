// The right-hand column: where the post is going.
//
// One platform switch at the top, and then the pickers for that platform. The
// platform is part of the shared draft, so switching it here changes the editor
// and the preview beside it without any of them being asked twice.

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
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@workspace/ui/components/tabs"
import { cn } from "@workspace/ui/lib/utils"

import { faDigits } from "@/components/persian-digits"
import { Field, SearchableCheckList } from "@/components/publish/publish-primitives"
import type { PublishingOptions, SiteOption } from "@/components/publish/publish-options"
import { ErrorState, LoadingState } from "@/components/states"
import { TelegramServerCombobox } from "@/components/telegram-server-combobox"
import {
  setChannelIds,
  setClientKey,
  setPlatform,
  setSite,
  type PublishDraftState,
  type PublishPlatform,
} from "@/lib/publish-draft"

export type ApplyChange = (recipe: (state: PublishDraftState) => PublishDraftState) => void

export interface DestinationPanelProps {
  state: PublishDraftState
  options: PublishingOptions
  onChange: ApplyChange
  className?: string
}

export function DestinationPanel({ state, options, onChange, className }: DestinationPanelProps) {
  return (
    <section
      data-panel="destination"
      className={cn("flex min-h-0 flex-col gap-5", className)}
      aria-label="انتخاب مقصد"
    >
      <h2 className="text-sm font-semibold">انتخاب مقصد</h2>

      <Tabs
        value={state.platform}
        onValueChange={(next: string | number | null) =>
          onChange((s) => setPlatform(s, (next ?? "telegram") as PublishPlatform))
        }
      >
        <TabsList className="w-full">
          <TabsTrigger value="telegram" className="flex-1">
            کانال
          </TabsTrigger>
          <TabsTrigger value="wordpress" className="flex-1">
            وردپرس
          </TabsTrigger>
        </TabsList>

        <TabsContent value="telegram" className="mt-4 flex flex-col gap-5">
          <TelegramDestinationSelector state={state} options={options} onChange={onChange} />
        </TabsContent>

        <TabsContent value="wordpress" className="mt-4 flex flex-col gap-5">
          <WordPressDestinationSelector state={state} options={options} onChange={onChange} />
        </TabsContent>
      </Tabs>
    </section>
  )
}

// ---------------------------------------------------------------------------
// Telegram
// ---------------------------------------------------------------------------

function TelegramDestinationSelector({
  state,
  options,
  onChange,
}: {
  state: PublishDraftState
  options: PublishingOptions
  onChange: ApplyChange
}) {
  return (
    <>
      <Field
        label="سرور تلگرام"
        htmlFor="publish-server"
        hint="پیام از حساب همین سرور ارسال می‌شود."
      >
        <TelegramServerCombobox
          id="publish-server"
          servers={options.servers}
          value={state.telegram.clientKey ?? undefined}
          placeholder="سرور تلگرام را انتخاب کنید"
          emptyText="سروری یافت نشد."
          onChange={(key) => onChange((s) => setClientKey(s, key ?? null))}
        />
      </Field>

      {options.loading.channels && <LoadingState label="در حال بارگذاری کانال‌ها…" />}
      {options.errors.channels && <ErrorState message={options.errors.channels} />}

      {state.telegram.clientKey && !options.loading.channels && (
        <Field
          label="کانال‌های مقصد"
          htmlFor="publish-channel-search"
          hint="فقط کانال‌هایی که این حساب اجازهٔ ارسال در آن‌ها را دارد."
        >
          <SearchableCheckList
            id="publish-channel-search"
            items={options.channels}
            selected={state.telegram.channelIds}
            onToggle={(id) =>
              onChange((s) =>
                setChannelIds(
                  s,
                  s.telegram.channelIds.includes(String(id))
                    ? s.telegram.channelIds.filter((v) => v !== String(id))
                    : [...s.telegram.channelIds, String(id)],
                ),
              )
            }
            searchPlaceholder="جستجو در کانال‌ها…"
            emptyText="هیچ کانالی برای ارسال پیام در دسترس نیست."
            noMatchText="کانالی با این جستجو یافت نشد."
            labelOf={(channel) => channel.title}
            detailOf={(channel) => (channel.username ? `@${channel.username}` : undefined)}
            disabledOf={(channel) => !channel.canPublish}
            footer={
              <p className="text-xs text-muted-foreground" role="status">
                {faDigits(state.telegram.channelIds.length)} کانال انتخاب شده
              </p>
            }
          />
        </Field>
      )}
    </>
  )
}

// ---------------------------------------------------------------------------
// WordPress
// ---------------------------------------------------------------------------

function WordPressDestinationSelector({
  state,
  options,
  onChange,
}: {
  state: PublishDraftState
  options: PublishingOptions
  onChange: ApplyChange
}) {
  return (
    <>
      <Field label="سایت وردپرس" htmlFor="publish-site" hint="مقاله در این سایت منتشر می‌شود.">
        <SiteCombobox
          id="publish-site"
          sites={options.sites}
          value={state.wordpress.siteId}
          onChange={(id) => onChange((s) => setSite(s, id))}
        />
      </Field>

      {options.loading.sites && <LoadingState label="در حال بارگذاری سایت‌ها…" />}
      {options.errors.sites && <ErrorState message={options.errors.sites} />}
      {options.sites.length === 0 && !options.loading.sites && (
        <p className="text-xs text-muted-foreground">هیچ سایت وردپرسی ثبت نشده است.</p>
      )}

      <SelectedSiteSummary sites={options.sites} siteId={state.wordpress.siteId} />
    </>
  )
}

// SelectedSiteSummary names the chosen destination below the picker. The
// picker's own input holds a search, so without this the choice would only be
// visible while the dropdown is open.
function SelectedSiteSummary({ sites, siteId }: { sites: SiteOption[]; siteId: number | null }) {
  const site = siteId === null ? null : sites.find((s) => s.id === siteId)
  if (!site) return null
  return (
    <p className="flex flex-wrap items-baseline gap-2 rounded-lg bg-muted/40 px-3 py-2 text-sm">
      <span className="text-muted-foreground">سایت انتخاب‌شده:</span>
      <span className="font-medium">{site.title}</span>
      <span dir="ltr" className="text-xs text-muted-foreground">
        {site.domain}
      </span>
    </p>
  )
}

// SiteCombobox picks one registered site. It follows the same contract as
// TelegramServerCombobox: the caller loads the rows, this one never fetches,
// and the value it reports is the site's own id rather than its display name.
function SiteCombobox({
  sites,
  value,
  onChange,
  id,
}: {
  sites: SiteOption[]
  value: number | null
  onChange: (id: number | null) => void
  id?: string
}) {
  const anchor = useComboboxAnchor()
  // A value that is not in `sites` selects nothing; the field falls back to its
  // placeholder rather than naming a site that is not on the list.
  const chosen = value === null ? undefined : (sites.find((s) => s.id === value) ?? null)
  return (
    <Combobox
      items={sites}
      autoHighlight
      value={chosen}
      onValueChange={(next) => onChange((next as SiteOption | null)?.id ?? null)}
      itemToStringLabel={(site) => site.title}
    >
      <ComboboxChips ref={anchor} className="w-full">
        <ComboboxChipsInput
          id={id}
          aria-label="سایت وردپرس"
          placeholder="سایت مقصد را انتخاب کنید"
          className="text-sm"
        />
      </ComboboxChips>
      <ComboboxContent anchor={anchor}>
        <ComboboxEmpty>سایتی یافت نشد.</ComboboxEmpty>
        <ComboboxList>
          {(site: SiteOption) => (
            <ComboboxItem key={site.id} value={site}>
              <span className="flex min-w-0 flex-col">
                <span className="text-sm">{site.title}</span>
                <span dir="ltr" className="text-xs text-muted-foreground">
                  {site.domain}
                </span>
              </span>
            </ComboboxItem>
          )}
        </ComboboxList>
      </ComboboxContent>
    </Combobox>
  )
}
