import {
  AlertTriangleIcon,
  LinkIcon,
  LoaderCircleIcon,
  PlusIcon,
  XIcon,
} from "lucide-react"
import * as React from "react"
import { Link } from "react-router"

import { OptionCombobox } from "@/components/automation/option-combobox"
import {
  AutomationFormActions,
  AutomationFormLayout,
} from "@/components/automation/form-shell"
import {
  SERVER_REQUIRED,
  SOURCE_REQUIRED,
  SourceServerField,
  channelsForServer,
  serverKeyFor,
  sourceItems,
  type SourceChannel,
} from "@/components/automation/source-selection"
import {
  WordPressDestinationOptions,
  type SiteTerms,
} from "@/components/automation/workflow-wordpress-options"
import { MultiPicker } from "@/components/multi-picker"
import { SwitchState } from "@/components/automation/status"
import { TelegramServerCombobox } from "@/components/telegram-server-combobox"
import { LoadingState } from "@/components/states"
import { Badge } from "@workspace/ui/components/badge"
import { Button } from "@workspace/ui/components/button"
import {
  Field,
  FieldDescription,
  FieldError,
  FieldLabel,
} from "@workspace/ui/components/field"
import { Label } from "@workspace/ui/components/label"
import { Separator } from "@workspace/ui/components/separator"
import { Switch } from "@workspace/ui/components/switch"
import { Textarea } from "@workspace/ui/components/textarea"

import { PageShell } from "@/lib/page"

import {
  automationRoute,
  destinationTypeLabels,
  workflowEnableBlocker,
  type DestinationDraft,
  type DestinationType,
  type WorkflowDraft,
} from "@/lib/automation"
import type { TelegramServer } from "@/lib/types"

// The one create/edit form of the workflow area.
//
// It is a page, not a dialog: one vertically scrollable column with four
// sections — the source channel, the publishing destinations, the AI stage and
// the workflow's own status. The header carries the page's only Save and Cancel,
// which submit the single form below through its `id`, so the two are never
// inside the scroll area and never hidden by the phone's bottom navigation.
//
// The whole configuration is one `WorkflowDraft`. Destinations below are drawn
// as editable blocks — one Telegram connection with its channels, one WordPress
// site with its own options — but a block is a *view* of that list: every change
// writes rows back and every render derives the blocks again. There is no second
// copy of the destination list to fall out of step with the first.

export const WORKFLOW_FORM_ID = "workflow-form"

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

// The channels one Telegram connection may publish to. They belong to that
// connection alone: a peer id is meaningful only beside the client key it was
// read with, so each key is loaded and shown on its own.
export interface ChannelOptions {
  items: NamedOption[]
  loading: boolean
  error: string | null
}

// ---------------------------------------------------------------------------
// Destinations as blocks.
//
// The wire keeps one row per published channel and one per published site. A
// block groups those rows by the address that identifies a destination: one
// Telegram connection together with the channels picked on it, or one WordPress
// site. A block with nothing chosen yet is kept alive by a row with no address,
// which is never posted — a destination that names no channel and no site is
// refused before the request goes out.
// ---------------------------------------------------------------------------

export interface DestinationBlock {
  key: string
  type: DestinationType
  // Telegram address: the connection, and the peer ids picked on it.
  clientKey: string
  channelIDs: number[]
  // Each channel keeps the options it was stored with. This panel has no editor
  // for them and a save must not erase them.
  optionsByID: Record<number, Record<string, unknown>>
  // WordPress address: the site, and its own publishing settings.
  siteID: number | null
  options: Record<string, unknown>
  enabled: boolean
  complete: boolean
}

const positiveID = (value: unknown): number | null =>
  typeof value === "number" && Number.isInteger(value) && value > 0
    ? value
    : null

// A Telegram destination is addressed by a peer id and a connection. The schema
// refuses zero and nothing else, so every other integer is an address: a peer id
// is what the connection calls a channel, and this page never rewrites one.
const peerIDOf = (value: unknown): number | null =>
  typeof value === "number" && Number.isInteger(value) && value !== 0
    ? value
    : null

// Is this row a destination the backend would accept? A row that names no
// channel and no site is the block's placeholder, and a placeholder is never
// written to the API.
export function isCompleteDestination(destination: DestinationDraft): boolean {
  return destination.type === "telegram"
    ? destination.clientKey.trim() !== "" &&
        peerIDOf(destination.channelID) !== null
    : positiveID(destination.siteID) !== null
}

export function destinationBlocks(
  destinations: DestinationDraft[]
): DestinationBlock[] {
  const blocks: DestinationBlock[] = []
  const byKey = new Map<string, DestinationBlock>()
  destinations.forEach((row, index) => {
    const telegram = row.type === "telegram"
    const address = telegram
      ? row.clientKey.trim()
      : String(positiveID(row.siteID) ?? "")
    // A row with no address is its own block: two unchosen destinations must not
    // collapse into one just because both are still empty.
    const key = telegram
      ? address
        ? `tg:${address}`
        : `tg:new:${index}`
      : address
        ? `wp:${address}`
        : `wp:new:${index}`
    let block = byKey.get(key)
    if (!block) {
      block = {
        key,
        type: row.type,
        clientKey: telegram ? row.clientKey : "",
        channelIDs: [],
        optionsByID: {},
        siteID: telegram ? null : positiveID(row.siteID),
        options: telegram ? {} : { ...row.options },
        enabled: row.enabled,
        complete: false,
      }
      byKey.set(key, block)
      blocks.push(block)
    }
    if (telegram) {
      const channelID = peerIDOf(row.channelID)
      if (channelID !== null) {
        if (!block.channelIDs.includes(channelID))
          block.channelIDs.push(channelID)
        block.optionsByID[channelID] = { ...row.options }
      }
    } else if (block.siteID === null) {
      block.siteID = positiveID(row.siteID)
      block.options = { ...row.options }
    }
    // One switch per block, so a block whose rows disagree is shown as off
    // rather than as something the page would silently rewrite on save.
    block.enabled = block.enabled && row.enabled
    block.complete =
      block.type === "telegram"
        ? block.clientKey.trim() !== "" && block.channelIDs.length > 0
        : block.siteID !== null
  })
  return blocks
}

// The rows behind these blocks. A block with no channel keeps one addressless
// row so the block survives the next derivation; a block with channels writes
// one row per channel, each with the options it already had.
function rowsOf(blocks: DestinationBlock[]): DestinationDraft[] {
  return blocks.flatMap((block): DestinationDraft[] => {
    if (block.type === "telegram") {
      if (block.channelIDs.length === 0) {
        return [
          {
            type: "telegram" as const,
            clientKey: block.clientKey,
            channelID: null,
            siteID: null,
            options: {},
            enabled: block.enabled,
          },
        ]
      }
      return block.channelIDs.map((channelID) => ({
        type: "telegram" as const,
        clientKey: block.clientKey,
        channelID,
        siteID: null,
        options: { ...(block.optionsByID[channelID] ?? {}) },
        enabled: block.enabled,
      }))
    }
    return [
      {
        type: "wordpress" as const,
        clientKey: "",
        channelID: null,
        siteID: block.siteID,
        options: { ...block.options },
        enabled: block.enabled,
      },
    ]
  })
}

export function updateBlock(
  destinations: DestinationDraft[],
  key: string,
  next: Partial<DestinationBlock>
): DestinationDraft[] {
  return rowsOf(
    destinationBlocks(destinations).map((block) =>
      block.key === key ? { ...block, ...next } : block
    )
  )
}

export function addDestination(
  destinations: DestinationDraft[],
  type: DestinationType
): DestinationDraft[] {
  return rowsOf([
    ...destinationBlocks(destinations),
    {
      key: `${type === "telegram" ? "tg" : "wp"}:new:${destinations.length}`,
      type,
      clientKey: "",
      channelIDs: [],
      optionsByID: {},
      siteID: null,
      options: {},
      enabled: true,
      complete: false,
    },
  ])
}

export function removeDestination(
  destinations: DestinationDraft[],
  key: string
): DestinationDraft[] {
  return rowsOf(
    destinationBlocks(destinations).filter((block) => block.key !== key)
  )
}

// trimChannels keeps only the channels this connection may publish to. A peer
// id read against one connection says nothing about another, so a channel that
// the new connection does not offer is dropped and one it does offer is kept.
// A block left with nothing keeps its placeholder, so the operator sees an
// empty picker to fill rather than a destination that quietly vanished.
export function trimChannels(
  destinations: DestinationDraft[],
  clientKey: string,
  eligible: number[]
): DestinationDraft[] {
  const allowed = new Set(eligible)
  return rowsOf(
    destinationBlocks(destinations).map((block) =>
      block.type === "telegram" && block.clientKey === clientKey
        ? {
            ...block,
            channelIDs: block.channelIDs.filter((channelID) =>
              allowed.has(channelID)
            ),
          }
        : block
    )
  )
}

export function WorkflowForm({
  title,
  description = "پیام تازهٔ کانال مبدأ، در صورت نیاز با هوش مصنوعی ویرایش و سپس در مقاصد منتشر می‌شود.",
  submitLabel,
  draft,
  onDraftChange,
  sourceChannels,
  taken,
  conflict,
  servers,
  sites,
  channelsByKey,
  siteTerms,
  error,
  fieldErrors,
  pending,
  onSubmit,
  onCancel,
  sourceLocked = false,
}: {
  title: string
  description?: string
  submitLabel: string
  draft: WorkflowDraft
  onDraftChange: (next: WorkflowDraft) => void
  sourceChannels: SourceChannel[]
  taken: Map<number, TakenWorkflow>
  conflict?: ConflictWorkflow | null
  servers: TelegramServer[]
  sites: NamedOption[]
  channelsByKey?: Map<string, ChannelOptions>
  // Each registered site's own WordPress terms, keyed by site id. A workflow
  // publishes only term ids the destination site already has.
  siteTerms?: Map<number, SiteTerms>
  error?: string | null
  // A refusal that names one part of the configuration is shown next to it as
  // well as in the page's error slot, so the sentence points at what to fix.
  fieldErrors?: Partial<
    Record<"source" | "destinations" | "ai" | "status", string>
  >
  pending: boolean
  onSubmit: (draft: WorkflowDraft) => void
  onCancel: () => void
  // The source channel is fixed at creation: the backend refuses to move one,
  // so an edit says so instead of offering a choice it would reject.
  sourceLocked?: boolean
}) {
  const patch = (next: Partial<WorkflowDraft>) =>
    onDraftChange({ ...draft, ...next })

  const blocks = destinationBlocks(draft.destinations)
  const siteByID = new Map(sites.map((site) => [site.id, site]))

  const takenFor =
    draft.managedChannelID === null ? null : taken.get(draft.managedChannelID)

  // The backend's own rules, read before the request goes out: a workflow that
  // publishes nowhere cannot be switched on, and a destination that names no
  // channel and no site is not a destination at all.
  const incomplete = blocks.filter((block) => !block.complete)
  const enableBlocker = workflowEnableBlocker({
    destinations: draft.destinations.filter(isCompleteDestination),
  })
  const [enableBlocked, setEnableBlocked] = React.useState(false)
  // A save that would post a half-filled destination is refused here, in the
  // words of this page, rather than going out and coming back with the same
  // sentence from the schema.
  const [validationError, setValidationError] = React.useState<string | null>(
    null
  )

  const setManagedChannelID = (raw: string) => {
    setValidationError(null)
    const id = Number(raw)
    patch({ managedChannelID: Number.isInteger(id) && id > 0 ? id : null })
  }

  const change = (key: string, next: Partial<DestinationBlock>) => {
    setValidationError(null)
    onDraftChange({
      ...draft,
      destinations: updateBlock(draft.destinations, key, next),
    })
  }

  const add = (type: DestinationType) => {
    setValidationError(null)
    onDraftChange({
      ...draft,
      destinations: addDestination(draft.destinations, type),
    })
  }

  const remove = (key: string) => {
    setValidationError(null)
    onDraftChange({
      ...draft,
      destinations: removeDestination(draft.destinations, key),
    })
  }

  // Turning AI off hides its settings; it never clears them. The whole
  // configuration lives in the draft, so switching the stage off and back on
  // leaves the typed prompt and both flags exactly as they were.
  const aiOn = draft.aiEnabled

  // Which connection the source picker is showing. Until the operator chooses
  // one it is the connection the saved channel belongs to, so an edit opens on
  // the list its own record was built from and a create opens on none. Nothing
  // here rewrites the saved channel while the list is still arriving.
  const [chosenServer, setChosenServer] = React.useState<string | undefined>()
  const [sourceTouched, setSourceTouched] = React.useState(false)
  const sourceServer =
    chosenServer ??
    serverKeyFor(
      sourceChannels,
      draft.managedChannelID === null ? [] : [draft.managedChannelID]
    )
  const changeSourceServer = (key: string | undefined) => {
    setChosenServer(key)
    setSourceTouched(true)
    // A channel this connection cannot reach is not a channel this workflow can
    // run on, so the choice leaves with the connection rather than being posted
    // as a source that cannot deliver.
    const reachable = channelsForServer(sourceChannels, key)
    if (
      draft.managedChannelID !== null &&
      !reachable.some((channel) => channel.id === draft.managedChannelID)
    ) {
      setManagedChannelID("")
    }
  }
  const serverError = sourceTouched && !sourceServer ? SERVER_REQUIRED : null
  const sourceError =
    sourceTouched && draft.managedChannelID === null ? SOURCE_REQUIRED : null

  // A source is not optional: with no connection, or no channel on the chosen
  // one, there is nothing to save. The button says so rather than accepting a
  // submit that could only come back refused.
  const saveBlocked =
    Boolean(takenFor) ||
    incomplete.length > 0 ||
    !sourceServer ||
    draft.managedChannelID === null

  return (
    <AutomationFormLayout>
    <PageShell
      title={title}
      description={description}
      actions={
        /* The page's only Save and Cancel sit in the page header, above the
           form. They submit it through its id, so the form can scroll for as
           long as it needs to without taking the actions with it, and without
           the phone's bottom navigation ever covering them. The shared
           component owns their order and their styling — the same one the
           webhook form uses — so in RTL the primary action is the outermost
           control on the physical left with Cancel immediately to its right. */
        <AutomationFormActions
          formId={WORKFLOW_FORM_ID}
          submitLabel={submitLabel}
          pendingLabel="در حال ذخیره…"
          pending={pending}
          disabled={saveBlocked}
          onCancel={onCancel}
        />
      }
    >
      <form
        id={WORKFLOW_FORM_ID}
        className="flex flex-col gap-6"
        noValidate
        onSubmit={(event) => {
          event.preventDefault()
          setSourceTouched(true)
          // The source is two choices and both are required: the connection, and
          // a channel that connection can reach. A workflow with no source would
          // never run on anything.
          if (!sourceServer) return
          if (draft.managedChannelID === null) return
          if (incomplete.length > 0) {
            setValidationError(
              "دست‌کم یک مقصد هنوز کامل نیست. اتصال و کانال مقصد را انتخاب کنید، یا آن مقصد را حذف کنید."
            )
            return
          }
          if (takenFor) return
          setValidationError(null)
          onSubmit(draft)
        }}
      >
        {(error || validationError) && (
          <div
            role="alert"
            className="flex flex-col gap-2 rounded-lg bg-destructive/10 px-3 py-2 text-sm text-destructive"
          >
            <p>{validationError ?? error}</p>
            {conflict && (
              <Link
                to={automationRoute.editWorkflow(conflict.id)}
                className="inline-flex w-fit items-center gap-1 rounded-md border border-destructive/40 bg-background px-2 py-1 text-xs font-medium text-foreground"
              >
                <LinkIcon className="size-3.5" aria-hidden="true" />
                ویرایش ورک‌فلوی «{conflict.channelLabel}»
              </Link>
            )}
          </div>
        )}

        {/* ------------------------------ Source ------------------------------ */}
        <section
          className="flex flex-col gap-3"
          aria-labelledby="workflow-source-heading"
        >
          <h2 id="workflow-source-heading" className="text-sm font-semibold">
            کانال مبدأ
          </h2>
          {/* The connection is chosen first and the channel follows from it:
              only channels this connection can reach are offered. The
              connection is the managed channel's own client_key, so this is a
              filter over the record and not a second thing to store. */}
          <SourceServerField
            id="workflow-source-server"
            servers={servers}
            value={sourceServer}
            onChange={changeSourceServer}
            error={serverError}
            disabled={sourceLocked || pending}
          />
          <Field data-invalid={sourceError ? "" : undefined}>
            <FieldLabel htmlFor="workflow-channel" required>
              کانال مبدأ
            </FieldLabel>
            <OptionCombobox
              id="workflow-channel"
              label="کانال مبدأ"
              options={sourceItems(
                sourceChannels,
                sourceServer,
                draft.managedChannelID === null ? [] : [draft.managedChannelID]
              ).map((channel) => ({
                value: String(channel.id),
                label: channel.label,
                detail: channel.detail,
              }))}
              value={
                draft.managedChannelID === null
                  ? ""
                  : String(draft.managedChannelID)
              }
              onChange={setManagedChannelID}
              placeholder={
                sourceServer
                  ? "کانال مبدأ را انتخاب کنید"
                  : "نخست سرور تلگرام را انتخاب کنید"
              }
              emptyText="کانالی در دسترس نیست."
              disabled={sourceLocked || pending || !sourceServer}
            />
            <FieldDescription>
              {sourceLocked
                ? "کانال مبدأ پس از ایجاد ورک‌فلو قابل تغییر نیست. برای کانال دیگر، این ورک‌فلو را حذف و برای آن کانال ورک‌فلوی تازه بسازید."
                : "پیام‌های تازهٔ این کانال، ورک‌فلو را اجرا می‌کنند. برای هر کانال فقط یک ورک‌فلو می‌تواند وجود داشته باشد."}
            </FieldDescription>
            {sourceError && <FieldError>{sourceError}</FieldError>}
          </Field>

          {fieldErrors?.source && (
            <p role="alert" className="text-xs text-destructive">
              {fieldErrors.source}
            </p>
          )}

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
                برای «{takenFor.channelLabel}» ورک‌فلوی دیگری ثبت شده است. هر
                کانال فقط یک ورک‌فلو دارد؛ همان را ویرایش کنید.
              </span>
              <Link
                to={automationRoute.editWorkflow(takenFor.id)}
                className="inline-flex items-center gap-1 rounded-md border bg-background px-2 py-1 text-xs font-medium"
              >
                <LinkIcon className="size-3.5" aria-hidden="true" />
                ویرایش ورک‌فلو
              </Link>
            </div>
          )}
        </section>

        <Separator />

        {/* --------------------------- Destinations --------------------------- */}
        <section
          className="flex flex-col gap-4"
          aria-labelledby="workflow-destinations-heading"
        >
          <div className="flex flex-col gap-1">
            <h2
              id="workflow-destinations-heading"
              className="text-sm font-semibold"
            >
              مقصدهای انتشار
            </h2>
            <p className="text-xs text-muted-foreground">
              مشخص کنید پیام‌های این کانال در کدام مقصدها منتشر شوند.
            </p>
          </div>

          {blocks.length === 0 && (
            <p className="text-xs text-muted-foreground">
              هنوز مقصدی انتخاب نشده است؛ پیامی منتشر نمی‌شود.
            </p>
          )}

          {blocks.map((block) => (
            <div
              key={block.key}
              className="flex flex-col gap-3 rounded-lg border p-4"
            >
              <div className="flex flex-wrap items-center justify-between gap-2">
                <div className="flex min-w-0 flex-wrap items-center gap-2">
                  <Badge variant="outline">
                    {destinationTypeLabels[block.type]}
                  </Badge>
                  {block.type === "telegram" ? (
                    <span
                      dir="ltr"
                      className="truncate text-xs text-muted-foreground"
                    >
                      {block.clientKey || "اتصال انتخاب نشده"}
                    </span>
                  ) : (
                    <span className="truncate text-xs text-muted-foreground">
                      {block.siteID === null
                        ? "سایت انتخاب نشده"
                        : (siteByID.get(block.siteID)?.label ??
                          `سایت ${block.siteID}`)}
                    </span>
                  )}
                </div>
                <div className="flex items-center gap-2">
                  <SwitchState
                    on={block.enabled}
                    onLabel="فعال"
                    offLabel="غیرفعال"
                  />
                  <Switch
                    aria-label="فعال بودن این مقصد"
                    checked={block.enabled}
                    onCheckedChange={(enabled) =>
                      change(block.key, { enabled })
                    }
                    disabled={pending}
                  />
                  <Button
                    type="button"
                    variant="ghost"
                    size="icon-sm"
                    aria-label="حذف این مقصد"
                    onClick={() => remove(block.key)}
                    disabled={pending}
                  >
                    <XIcon />
                  </Button>
                </div>
              </div>

              {block.type === "telegram" ? (
                <>
                  <Field>
                    <FieldLabel htmlFor={`workflow-server-${block.key}`}>
                      اتصال تلگرام
                    </FieldLabel>
                    <TelegramServerCombobox
                      id={`workflow-server-${block.key}`}
                      servers={servers}
                      value={block.clientKey || undefined}
                      onChange={(next) =>
                        change(block.key, { clientKey: next ?? "" })
                      }
                      placeholder="اتصال تلگرام را انتخاب کنید"
                      emptyText="اتصالی یافت نشد."
                      disabled={pending}
                    />
                  </Field>
                  <TelegramChannels
                    block={block}
                    options={channelsByKey?.get(block.clientKey)}
                    pending={pending}
                    onChannels={(channelIDs) =>
                      change(block.key, { channelIDs })
                    }
                  />
                </>
              ) : (
                <>
                  <Field>
                    <FieldLabel htmlFor={`workflow-site-${block.key}`}>
                      سایت وردپرسی
                    </FieldLabel>
                    <OptionCombobox
                      id={`workflow-site-${block.key}`}
                      label="سایت وردپرسی"
                      options={sites.map((site) => ({
                        value: String(site.id),
                        label: site.label,
                        detail: site.detail,
                      }))}
                      value={block.siteID === null ? "" : String(block.siteID)}
                      onChange={(raw) => {
                        const id = Number(raw)
                        change(block.key, {
                          siteID: Number.isInteger(id) && id > 0 ? id : null,
                        })
                      }}
                      placeholder="سایت مقصد را انتخاب کنید"
                      emptyText="سایتی ثبت نشده است."
                      disabled={pending}
                    />
                  </Field>
                  {block.siteID !== null && (
                    <WordPressDestinationOptions
                      site={
                        siteByID.get(block.siteID) ?? {
                          id: block.siteID,
                          label: `سایت ${block.siteID}`,
                        }
                      }
                      terms={
                        siteTerms?.get(block.siteID) ?? {
                          categories: [],
                          tags: [],
                          loading: false,
                          error: null,
                        }
                      }
                      options={block.options}
                      onOptionsChange={(next) =>
                        change(block.key, { options: next })
                      }
                      pending={pending}
                    />
                  )}
                </>
              )}

              {!block.complete && (
                <p className="text-xs text-muted-foreground">
                  این مقصد هنوز کامل نیست؛ تا انتخاب نشدن آن ذخیره نمی‌شود.
                </p>
              )}
            </div>
          ))}

          <div className="flex flex-wrap items-center gap-2">
            <span className="text-xs text-muted-foreground">افزودن مقصد:</span>
            {(["telegram", "wordpress"] as DestinationType[]).map((type) => (
              <Button
                key={type}
                type="button"
                variant="outline"
                size="sm"
                onClick={() => add(type)}
                disabled={pending}
              >
                <PlusIcon data-icon="inline-start" />
                {destinationTypeLabels[type]}
              </Button>
            ))}
          </div>

          {fieldErrors?.destinations && (
            <p role="alert" className="text-xs text-destructive">
              {fieldErrors.destinations}
            </p>
          )}
        </section>

        <Separator />

        {/* --------------------------------- AI ------------------------------- */}
        <section
          className="flex flex-col gap-4"
          aria-labelledby="workflow-ai-heading"
        >
          <h2 id="workflow-ai-heading" className="text-sm font-semibold">
            هوش مصنوعی
          </h2>

          <ToggleRow
            id="workflow-ai-enabled"
            label="ویرایش پیام با هوش مصنوعی"
            checked={draft.aiEnabled}
            onCheckedChange={(aiEnabled) => patch({ aiEnabled })}
            disabled={pending}
            note="با خاموش بودن این گزینه، همان متن اصلی پیام منتشر می‌شود."
          />

          {/* Hidden while the stage is off, never cleared: the typed prompt and
            both flags stay in the draft, so turning AI back on picks them up. */}
          {aiOn && (
            <>
              <Field>
                <FieldLabel htmlFor="workflow-ai-prompt">
                  پرامپت اختصاصی
                </FieldLabel>
                <Textarea
                  id="workflow-ai-prompt"
                  value={draft.aiPrompt}
                  onChange={(event) => patch({ aiPrompt: event.target.value })}
                  className="min-h-24"
                  placeholder="اختیاری"
                  disabled={pending}
                />
                <FieldDescription>
                  در صورت خالی بودن، متن با پرامپت پیش‌فرض ویرایش می‌شود.
                </FieldDescription>
              </Field>

              <ToggleRow
                id="workflow-ai-images"
                label="ارسال تصاویر به هوش مصنوعی"
                checked={draft.aiSendImages}
                onCheckedChange={(aiSendImages) => patch({ aiSendImages })}
                disabled={pending}
                note="این گزینه فقط ورودی هوش مصنوعی را کنترل می‌کند، نه رسانه‌های ارسالی به مقصد."
              />

              <ToggleRow
                id="workflow-ai-fallback"
                label="انتشار پیام در صورت خطای هوش مصنوعی"
                checked={draft.aiPublishOnFailure}
                onCheckedChange={(aiPublishOnFailure) =>
                  patch({ aiPublishOnFailure })
                }
                disabled={pending}
                note="در صورت خطای هوش مصنوعی، پیام اصلی بدون ویرایش منتشر می‌شود."
              />
            </>
          )}

          {fieldErrors?.ai && (
            <p role="alert" className="text-xs text-destructive">
              {fieldErrors.ai}
            </p>
          )}
        </section>

        <Separator />

        {/* ------------------------------- Status ----------------------------- */}
        <section
          className="flex flex-col gap-3"
          aria-labelledby="workflow-status-heading"
        >
          <h2 id="workflow-status-heading" className="text-sm font-semibold">
            وضعیت ورک‌فلو
          </h2>
          <div className="flex flex-nowrap items-center justify-between gap-4 rounded-lg border px-3 py-2.5">
            <Label htmlFor="workflow-enabled" className="font-semibold">
              فعال بودن ورک‌فلو
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
          {fieldErrors?.status && (
            <p role="alert" className="text-xs text-destructive">
              {fieldErrors.status}
            </p>
          )}
        </section>
      </form>
    </PageShell>
    </AutomationFormLayout>
  )
}

// One Telegram connection's own channels. They are loaded per connection and
// shown only for the block that names it, so a peer id is never offered against
// a connection it does not live on.
function TelegramChannels({
  block,
  options,
  pending,
  onChannels,
}: {
  block: DestinationBlock
  options: ChannelOptions | undefined
  pending: boolean
  onChannels: (channelIDs: number[]) => void
}) {
  if (!block.clientKey) {
    return (
      <p className="text-xs text-muted-foreground">
        برای افزودن مقصد تلگرامی، اول یک اتصال را انتخاب کنید.
      </p>
    )
  }
  if (options?.error)
    return (
      <p role="alert" className="text-xs text-destructive">
        {options.error}
      </p>
    )
  if (options?.loading)
    return <LoadingState label="در حال بارگذاری کانال‌ها…" />
  return (
    <MultiPicker
      id={`workflow-destination-channels-${block.key}`}
      title="کانال‌های مقصد"
      placeholder="کانال مقصد را انتخاب کنید"
      emptyText="کانالی برای ارسال پیام در دسترس نیست."
      items={options?.items ?? []}
      value={block.channelIDs}
      onValueChange={onChannels}
      labelOf={(channel) => channel.label}
      disabled={pending}
    />
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
