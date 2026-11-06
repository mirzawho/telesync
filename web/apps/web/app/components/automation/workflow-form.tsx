import { AlertTriangleIcon, LinkIcon } from "lucide-react"
import * as React from "react"
import { Link } from "react-router"

import { OptionCombobox } from "@/components/automation/option-combobox"
import {
  WordPressDestinationOptions,
  type SiteTerms,
} from "@/components/automation/workflow-wordpress-options"
import { MultiPicker } from "@/components/multi-picker"
import { SwitchState } from "@/components/automation/status"
import { SubmitButton } from "@/components/submit-button"
import { TelegramServerCombobox } from "@/components/telegram-server-combobox"
import { faDigits } from "@/components/persian-digits"
import { ErrorState, LoadingState } from "@/components/states"
import { Badge } from "@workspace/ui/components/badge"
import { Button } from "@workspace/ui/components/button"
import {
  Field,
  FieldDescription,
  FieldLabel,
} from "@workspace/ui/components/field"
import { Label } from "@workspace/ui/components/label"
import { Separator } from "@workspace/ui/components/separator"
import { Switch } from "@workspace/ui/components/switch"
import { Textarea } from "@workspace/ui/components/textarea"

import {
  automationRoute,
  destinationTypeLabels,
  telegramDestination,
  workflowEnableBlocker,
  wordpressDestination,
  type DestinationDraft,
  type WorkflowDraft,
} from "@/lib/automation"
import type { TelegramServer } from "@/lib/types"

export interface NamedOption {
  id: number
  label: string
  detail?: string
}

// A managed channel a workflow is already attached to. The schema allows one
// workflow per channel, so a taken channel is offered read-only and points at
// the workflow that holds it rather than letting a second one be created.
export interface TakenWorkflow {
  id: number
  channelLabel: string
}

// A channel whose workflow already exists, named so the conflict can point at
// it instead of only saying no.
export interface ConflictWorkflow {
  id: number
  channelLabel: string
}

export function WorkflowForm({
  draft,
  onDraftChange,
  sourceChannels,
  taken,
  conflict,
  servers,
  sites,
  destinationChannels,
  loadingChannels,
  channelsError,
  siteTerms,
  error,
  pending,
  onSubmit,
  onCancel,
  submitLabel = "ذخیره",
  sourceLocked = false,
  onClientKeyChange,
}: {
  draft: WorkflowDraft
  onDraftChange: (next: WorkflowDraft) => void
  sourceChannels: NamedOption[]
  taken: Map<number, TakenWorkflow>
  conflict?: ConflictWorkflow | null
  servers: TelegramServer[]
  sites: NamedOption[]
  destinationChannels: NamedOption[]
  loadingChannels: boolean
  channelsError?: string | null
  // Each registered site's own WordPress terms, keyed by site id. A workflow
  // publishes only term ids the destination site already has.
  siteTerms?: Map<number, SiteTerms>
  error?: string | null
  pending: boolean
  onSubmit: (draft: WorkflowDraft) => void
  onCancel: () => void
  submitLabel?: string
  // The source channel is fixed at creation: the backend refuses to move one,
  // so an edit says so instead of offering a choice it would reject.
  sourceLocked?: boolean
  onClientKeyChange?: (key: string | undefined) => void
}) {
  const patch = (next: Partial<WorkflowDraft>) => onDraftChange({ ...draft, ...next })

  const telegramDestinations = draft.destinations.filter((d) => d.type === "telegram")
  const siteDestinations = draft.destinations.filter((d) => d.type === "wordpress")
  const clientKey = telegramDestinations[0]?.clientKey ?? ""
  const channelIDs = telegramDestinations
    .map((d) => d.channelID)
    .filter((id): id is number => id !== null && id !== 0)
  const siteIDs = siteDestinations
    .map((d) => d.siteID)
    .filter((id): id is number => id !== null && id !== 0)

  const takenFor = draft.managedChannelID === null ? null : taken.get(draft.managedChannelID)

  // The backend's own rule, read before the request goes out: a workflow that
  // publishes nowhere cannot be switched on.
  const enableBlocker = workflowEnableBlocker(draft)
  const [enableBlocked, setEnableBlocked] = React.useState(false)

  // One connection per workflow: picking another drops the channels of the old
  // one, so a channel id is never posted against a connection it does not live
  // on. A channel that stays keeps whatever options it was stored with: this
  // panel has no editor for them and a save must not erase them.
  const optionsFor = (channelID: number) =>
    telegramDestinations.find((d) => d.channelID === channelID)?.options ?? {}

  const setClientKey = (next: string | undefined) => {
    onClientKeyChange?.(next)
    if (!next) {
      patch({ destinations: draft.destinations.filter((d) => d.type !== "telegram") })
      return
    }
    patch({
      destinations: [
        ...draft.destinations.filter((d) => d.type !== "telegram"),
        ...channelIDs.map((id) => ({
          ...telegramDestination(next, id),
          options: optionsFor(id),
        })),
      ],
    })
  }

  const setChannelIDs = (ids: number[]) => {
    const kept = draft.destinations.filter((d) => d.type !== "telegram")
    patch({
      destinations: [
        ...kept,
        ...ids.map((id) => ({
          ...telegramDestination(clientKey, id),
          options: optionsFor(id),
        })),
      ],
    })
  }

  const setSiteIDs = (ids: number[]) => {
    const siteOptions = (siteID: number) =>
      siteDestinations.find((d) => d.siteID === siteID)?.options ?? {}
    const kept = draft.destinations.filter((d) => d.type !== "wordpress")
    patch({
      destinations: [
        ...kept,
        ...ids.map((id) => ({
          ...wordpressDestination(id),
          options: siteOptions(id),
        })),
      ],
    })
  }

  // One site's own publishing settings. They live in that destination's
  // `options` and travel with it: changing another site's settings, or adding a
  // site, never rewrites these.
  const setSiteOptions = (siteID: number, next: Record<string, unknown>) => {
    patch({
      destinations: draft.destinations.map((destination) =>
        destination.type === "wordpress" && destination.siteID === siteID
          ? { ...destination, options: next }
          : destination
      ),
    })
  }

  const siteByID = new Map(sites.map((site) => [site.id, site]))

  const setManagedChannelID = (raw: string) => {
    const id = Number(raw)
    patch({ managedChannelID: Number.isInteger(id) && id > 0 ? id : null })
  }

  return (
    <form
      className="flex flex-col gap-8"
      noValidate
      onSubmit={(event) => {
        event.preventDefault()
        onSubmit(draft)
      }}
    >
      {/* ------------------------------ Source ----------------------------- */}
      <section className="flex flex-col gap-3" aria-labelledby="workflow-source">
        <h2 id="workflow-source" className="text-sm font-semibold">
          مبدأ
        </h2>
        <Field>
          <FieldLabel htmlFor="workflow-channel" required>
            کانال مبدأ
          </FieldLabel>
          <OptionCombobox
            id="workflow-channel"
            label="کانال مبدأ"
            options={sourceChannels.map((channel) => ({
              value: String(channel.id),
              label: channel.label,
              detail: channel.detail,
            }))}
            value={draft.managedChannelID === null ? "" : String(draft.managedChannelID)}
            onChange={setManagedChannelID}
            placeholder="کانال مبدأ را انتخاب کنید"
            emptyText="کانالی در دسترس نیست."
            disabled={sourceLocked || pending}
          />
          <FieldDescription>
            {sourceLocked
              ? "کانال مبدأ پس از ایجاد ورک‌فلو قابل تغییر نیست. برای کانال دیگر، این ورک‌فلو را حذف و برای آن کانال ورک‌فلوی تازه بسازید."
              : "پیام‌های تازهٔ این کانال، ورک‌فلو را اجرا می‌کنند. برای هر کانال فقط یک ورک‌فلو می‌تواند وجود داشته باشد."}
          </FieldDescription>
        </Field>

        {takenFor && (
          <div
            role="alert"
            className="flex flex-wrap items-center gap-2 rounded-lg border border-amber-500/40 bg-amber-500/10 px-3 py-2 text-sm"
          >
            <AlertTriangleIcon
              className="size-4 text-amber-700"
              aria-hidden="true"
            />
            <span className="min-w-0 flex-1">
              برای «{takenFor.channelLabel}» ورک‌فلوی دیگری ثبت شده است. هر کانال
              فقط یک ورک‌فلو دارد؛ همان را ویرایش کنید.
            </span>
            <Link
              to={automationRoute.workflow(takenFor.id)}
              className="inline-flex items-center gap-1 rounded-md border bg-background px-2 py-1 text-xs font-medium"
            >
              <LinkIcon className="size-3.5" aria-hidden="true" />
              ویرایش ورک‌فلو
            </Link>
          </div>
        )}
      </section>

      <Separator />

      {/* --------------------------- Destinations -------------------------- */}
      <section
        className="flex flex-col gap-4"
        aria-labelledby="workflow-destinations"
      >
        <div className="flex flex-col gap-1">
          <h2 id="workflow-destinations" className="text-sm font-semibold">
            مقاصد
          </h2>
          <p className="text-xs text-muted-foreground">
            پیام برای هر مقصد انتخاب‌شده به‌طور جداگانه منتشر می‌شود.
          </p>
        </div>

        <div className="flex flex-col gap-4 rounded-lg border p-4">
          <h3 className="text-sm font-medium">کانال‌های تلگرامی</h3>
          <Field>
            <FieldLabel htmlFor="workflow-server">اتصال تلگرام</FieldLabel>
            <TelegramServerCombobox
              id="workflow-server"
              servers={servers}
              value={clientKey || undefined}
              onChange={setClientKey}
              placeholder="اتصال تلگرام را انتخاب کنید"
              emptyText="اتصالی یافت نشد."
            />
          </Field>
          {channelsError && <ErrorState message={channelsError} />}
          {loadingChannels && <LoadingState label="در حال بارگذاری کانال‌ها…" />}
          {!loadingChannels && clientKey && (
            <MultiPicker
              id="workflow-destination-channels"
              title="کانال‌های مقصد"
              placeholder="کانال مقصد را انتخاب کنید"
              emptyText="کانالی برای ارسال پیام در دسترس نیست."
              items={destinationChannels}
              value={channelIDs}
              onValueChange={setChannelIDs}
              labelOf={(channel) => channel.label}
            />
          )}
          {!clientKey && (
            <p className="text-xs text-muted-foreground">
              برای افزودن مقصد تلگرامی، اول یک اتصال را انتخاب کنید.
            </p>
          )}
        </div>

        <div className="flex flex-col gap-3 rounded-lg border p-4">
          <h3 className="text-sm font-medium">سایت‌های وردپرسی</h3>
          <MultiPicker
            id="workflow-destination-sites"
            title="سایت‌های مقصد"
            placeholder="سایت مقصد را انتخاب کنید"
            emptyText="سایتی ثبت نشده است."
            items={sites}
            value={siteIDs}
            onValueChange={setSiteIDs}
            labelOf={(site) => site.label}
          />
          {/* Each site keeps its own publishing settings, because a category id
              means something only on the site that reported it. */}
          {siteIDs.map((siteID) => {
            const site = siteByID.get(siteID)
            if (!site) return null
            return (
              <WordPressDestinationOptions
                key={siteID}
                site={site}
                terms={
                  siteTerms?.get(siteID) ?? {
                    categories: [],
                    tags: [],
                    loading: false,
                    error: null,
                  }
                }
                options={
                  siteDestinations.find((d) => d.siteID === siteID)?.options ?? {}
                }
                onOptionsChange={(next) => setSiteOptions(siteID, next)}
                pending={pending}
              />
            )
          })}
        </div>

        <DestinationSummary destinations={draft.destinations} />
      </section>

      <Separator />

      {/* -------------------------------- AI ------------------------------- */}
      <section className="flex flex-col gap-4" aria-labelledby="workflow-ai">
        <h2 id="workflow-ai" className="text-sm font-semibold">
          هوش مصنوعی
        </h2>

        <ToggleRow
          id="workflow-ai-enabled"
          label="ویرایش پیام با هوش مصنوعی"
          checked={draft.aiEnabled}
          onCheckedChange={(aiEnabled) => patch({ aiEnabled })}
          disabled={pending}
        />

        {/* Every AI setting is on the page whether or not the stage is on, so a
            configuration can be written before it is used and the default of
            each is visible rather than implied. */}
        <Field>
          <FieldLabel htmlFor="workflow-ai-prompt">پرامپت اختصاصی</FieldLabel>
          <Textarea
            id="workflow-ai-prompt"
            value={draft.aiPrompt}
            onChange={(event) => patch({ aiPrompt: event.target.value })}
            className="min-h-24"
            placeholder="اختیاری"
            disabled={pending}
          />
          {draft.aiEnabled && draft.aiPrompt.trim() === "" && (
            <FieldDescription>
              در صورت خالی بودن، از پرامپت پیش‌فرض ویرایش محتوا استفاده می‌شود.
            </FieldDescription>
          )}
        </Field>

        <ToggleRow
          id="workflow-ai-images"
          label="ارسال تصاویر به هوش مصنوعی"
          checked={draft.aiSendImages}
          onCheckedChange={(aiSendImages) => patch({ aiSendImages })}
          disabled={pending}
          note="بدون این گزینه، تصاویر همراه متن برای هوش مصنوعی فرستاده نمی‌شوند؛ اما خود تصاویر همچنان در مقصد منتشر می‌شوند."
        />

        <ToggleRow
          id="workflow-ai-fallback"
          label="انتشار در صورت خطای هوش مصنوعی"
          checked={draft.aiPublishOnFailure}
          onCheckedChange={(aiPublishOnFailure) => patch({ aiPublishOnFailure })}
          disabled={pending}
          note="در صورت خطا، همان متن اصلی بدون ویرایش منتشر می‌شود. اگر خاموش باشد، پیام منتشر نمی‌شود."
        />
      </section>

      <Separator />

      {/* ------------------------------ Status ----------------------------- */}
      <section className="flex flex-col gap-3" aria-labelledby="workflow-status">
        <h2 id="workflow-status" className="text-sm font-semibold">
          وضعیت
        </h2>
        <div className="flex flex-nowrap items-center justify-between gap-4 rounded-lg border px-3 py-2.5">
          <Label htmlFor="workflow-enabled" className="font-semibold">
            وضعیت ورک‌فلو
          </Label>
          <div className="flex items-center gap-2">
            <SwitchState
              on={draft.enabled}
              onLabel="فعال"
              offLabel="غیرفعال"
            />
            <Switch
              id="workflow-enabled"
              checked={draft.enabled}
              onCheckedChange={(enabled) => {
                // The backend refuses to switch on a workflow that publishes
                // nowhere. The switch says so here rather than looking active
                // for a configuration the server will not accept; it never
                // enables anything behind the server's back.
                if (enabled && enableBlocker) {
                  setEnableBlocked(true)
                  return
                }
                setEnableBlocked(false)
                patch({ enabled })
              }}
              disabled={pending}
            />
          </div>
        </div>
        {enableBlocker && (
          <p
            role={enableBlocked ? "alert" : "status"}
            className="rounded-lg border border-amber-500/40 bg-amber-500/10 px-3 py-2 text-xs text-amber-800"
          >
            {enableBlocker}
          </p>
        )}
        <p className="text-xs text-muted-foreground">
          ورک‌فلوی غیرفعال، رویدادهای تازه را نمی‌پذیرد و چیزی منتشر نمی‌کند.
        </p>
      </section>

      {error && (
        <div
          role="alert"
          className="flex flex-col gap-2 rounded-lg bg-destructive/10 px-3 py-2 text-sm text-destructive"
        >
          <p>{error}</p>
          {conflict && (
            <Link
              to={automationRoute.workflow(conflict.id)}
              className="inline-flex w-fit items-center gap-1 rounded-md border border-destructive/40 bg-background px-2 py-1 text-xs font-medium text-foreground"
            >
              <LinkIcon className="size-3.5" aria-hidden="true" />
              ویرایش ورک‌فلوی «{conflict.channelLabel}»
            </Link>
          )}
        </div>
      )}

      <div className="flex flex-wrap items-center gap-2">
        {draft.enabled && enableBlocker ? (
          // A save here would be refused by the backend, so the button says so
          // instead of going out and coming back with the same sentence.
          <Button type="button" disabled>
            فعال‌سازی بدون مقصد فعال ممکن نیست
          </Button>
        ) : takenFor ? (
          // The same rule, from the other side: this channel already has its
          // one workflow and the schema refuses a second.
          <Button type="button" disabled>
            این کانال ورک‌فلو دارد
          </Button>
        ) : (
          <SubmitButton pending={pending} submittingLabel="در حال ذخیره…">
            {submitLabel}
          </SubmitButton>
        )}
        <Button
          type="button"
          variant="outline"
          onClick={onCancel}
          disabled={pending}
        >
          انصراف
        </Button>
      </div>
    </form>
  )
}

function ToggleRow({
  id,
  label,
  checked,
  onCheckedChange,
  disabled,
  note,
}: {
  id: string
  label: string
  checked: boolean
  onCheckedChange: (checked: boolean) => void
  disabled?: boolean
  note?: string
}) {
  return (
    <div className="flex flex-col gap-1.5 rounded-lg border px-3 py-2.5">
      <div className="flex flex-nowrap items-center justify-between gap-4">
        <Label htmlFor={id} className="font-semibold">
          {label}
        </Label>
        <div className="flex items-center gap-2">
          <SwitchState on={checked} onLabel="روشن" offLabel="خاموش" />
          <Switch
            id={id}
            checked={checked}
            onCheckedChange={onCheckedChange}
            disabled={disabled}
          />
        </div>
      </div>
      {note && <p className="text-xs text-muted-foreground">{note}</p>}
    </div>
  )
}

// What will actually be saved, below the pickers: the picker's own input holds
// a search, so without this the choice would only be visible while open.
function DestinationSummary({
  destinations,
}: {
  destinations: DestinationDraft[]
}) {
  if (destinations.length === 0) {
    return (
      <p className="text-xs text-muted-foreground">
        هنوز مقصدی انتخاب نشده است؛ پیامی منتشر نمی‌شود.
      </p>
    )
  }
  return (
    <div className="flex flex-col gap-2">
      <h3 className="text-sm font-medium">مقاصد انتخاب‌شده</h3>
      <ul className="flex flex-col gap-1.5">
        {destinations.map((destination, index) => (
          <li
            key={`${destination.type}-${destination.clientKey}-${destination.channelID ?? destination.siteID}-${index}`}
            className="flex flex-wrap items-center gap-2 rounded-lg bg-muted/40 px-3 py-2 text-sm"
          >
            <Badge variant="outline">
              {destinationTypeLabels[destination.type]}
            </Badge>
            <span dir="ltr" className="text-xs text-muted-foreground">
              {destination.type === "telegram"
                ? `${destination.clientKey} · ${destination.channelID ?? "—"}`
                : `site ${destination.siteID ?? "—"}`}
            </span>
          </li>
        ))}
      </ul>
      <p className="text-xs text-muted-foreground">
        <span>{faDigits(destinations.length)}</span> مقصد
      </p>
    </div>
  )
}
