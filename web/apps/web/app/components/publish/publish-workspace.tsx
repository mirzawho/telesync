// The publishing workspace: the same draft, drawn two ways.
//
// On a wide screen the destination, the editor and the preview sit side by side
// and all three are live at once. On a narrow one the very same panels appear
// one after another as three steps. Nothing is duplicated between them — each
// panel is a component, and only the shell around them differs.

import * as React from "react"

import { Button } from "@workspace/ui/components/button"
import { cn } from "@workspace/ui/lib/utils"

import { faDigits } from "@/components/persian-digits"
import { ContentEditorPanel, type PublishErrors } from "@/components/publish/content-editor-panel"
import { DestinationPanel, type ApplyChange } from "@/components/publish/destination-panel"
import { LivePreviewPanel } from "@/components/publish/live-preview-panel"
import type { PublishingOptions } from "@/components/publish/publish-options"
import { canAdvance, validate, type PublishDraftState } from "@/lib/publish-draft"

export type { ApplyChange }
export { LivePreviewPanel }

export interface WorkspaceProps {
  state: PublishDraftState
  options: PublishingOptions
  onChange: ApplyChange
  onRetryChannels?: () => void
  onCancel?: () => void
  onPublish?: () => void
  publishing?: boolean
  // errors are the field messages to show; when they are left out the workspace
  // works them out from the draft itself.
  errors?: PublishErrors
  className?: string
}

const DESKTOP_GRID = "grid grid-cols-[26fr_42fr_32fr]"

// ---------------------------------------------------------------------------
// Desktop
// ---------------------------------------------------------------------------

/**
 * DesktopPublishingWorkspace draws all three columns at once.
 *
 * The DOM order is destination, editor, preview. In this RTL panel the first
 * grid child is the rightmost one, so that order is the required
 * right / centre / left — and it is also the reading order a keyboard or a
 * screen reader follows. It is a workspace, not a wizard: there is no next and
 * no back, and the destination can be changed while the preview is in view.
 */
export function DesktopPublishingWorkspace(props: WorkspaceProps) {
  const { state, options, onChange, errors, onRetryChannels } = props
  return (
    <div
      data-layout="desktop"
      className={cn("flex min-h-0 flex-1 flex-col", props.className)}
    >
      <div className={cn(DESKTOP_GRID, "min-h-0 flex-1 divide-x divide-border overflow-hidden")}>
        <div className="min-h-0 overflow-y-auto px-5 py-4">
          <DestinationPanel
            state={state}
            options={options}
            onChange={onChange}
            onRetryChannels={onRetryChannels}
          />
        </div>
        <div className="min-h-0 overflow-y-auto px-5 py-4">
          <ContentEditorPanel
            state={state}
            options={options}
            onChange={onChange}
            errors={errors ?? validate(state).errors}
          />
        </div>
        <div className="min-h-0 overflow-y-auto bg-muted/40 px-5 py-4">
          <LivePreviewPanel state={state} options={options} />
        </div>
      </div>
      <PublishingFooter {...props} />
    </div>
  )
}

// ---------------------------------------------------------------------------
// Mobile
// ---------------------------------------------------------------------------

export type WizardStep = 1 | 2 | 3

export const WIZARD_STEPS = ["انتخاب مقصد", "ویرایش محتوا", "پیش‌نمایش و ارسال"] as const

// StepIndicator is the wizard's progress: three numbered steps, built from the
// panel's own primitives rather than a stepper package.
function StepIndicator({ step }: { step: WizardStep }) {
  return (
    <ol
      data-testid="step-indicator"
      className="flex items-center gap-2"
      aria-label="مراحل ارسال"
    >
      {WIZARD_STEPS.map((label, index) => {
        const number = (index + 1) as WizardStep
        const current = number === step
        const done = number < step
        return (
          <li
            key={label}
            aria-current={current ? "step" : undefined}
            className="flex min-w-0 flex-1 flex-col items-center gap-1"
          >
            <span
              className={cn(
                "flex size-7 items-center justify-center rounded-full border text-xs",
                current
                  ? "border-primary bg-primary text-primary-foreground"
                  : done
                    ? "border-primary/40 bg-primary/10 text-primary"
                    : "border-border text-muted-foreground",
              )}
            >
              {faDigits(number)}
            </span>
            <span
              className={cn(
                "truncate text-[11px]",
                current ? "text-foreground" : "text-muted-foreground",
              )}
            >
              {label}
            </span>
          </li>
        )
      })}
    </ol>
  )
}

/**
 * MobilePublishingWizard shows one step of the same workspace at a time.
 *
 * The draft is not touched by moving between steps: going back and forward
 * reads and writes one state, so nothing typed is refetched or reset. Each step
 * keeps its own scroll and the controls stay pinned to the bottom, above the
 * mobile keyboard.
 */
export function MobilePublishingWizard({
  step,
  onStep,
  ...props
}: WorkspaceProps & { step: WizardStep; onStep: (step: WizardStep) => void }) {
  const { state, options, onChange, errors, onRetryChannels } = props
  const validation = validate(state)
  const shown = errors ?? validation.errors
  const mayAdvance = canAdvance(step, state)

  return (
    <div
      data-layout="wizard"
      className={cn("flex min-h-0 flex-1 flex-col", props.className)}
    >
      <div className="border-b px-4 py-3">
        <StepIndicator step={step} />
      </div>

      <div className="min-h-0 flex-1 overflow-y-auto px-4 py-4">
        {step === 1 && (
          <DestinationPanel
            state={state}
            options={options}
            onChange={onChange}
            onRetryChannels={onRetryChannels}
          />
        )}
        {step === 2 && (
          <ContentEditorPanel
            state={state}
            options={options}
            onChange={onChange}
            errors={shown}
          />
        )}
        {step === 3 && <LivePreviewPanel state={state} options={options} />}
      </div>

      <WizardFooter
        step={step}
        onStep={onStep}
        mayAdvance={mayAdvance}
        {...props}
      />
    </div>
  )
}

// ---------------------------------------------------------------------------
// Footers
// ---------------------------------------------------------------------------

function publishLabel(state: PublishDraftState, publishing: boolean): string {
  if (publishing) {
    return state.platform === "wordpress" ? "در حال انتشار مقاله…" : "در حال ارسال…"
  }
  return state.platform === "wordpress" ? "انتشار مقاله" : "ارسال پیام"
}

// PublishingFooter is the shared footer: what was chosen, whether it is ready,
// and the two actions. It is the one place a publish is started from.
function PublishingFooter({
  state,
  onCancel,
  onPublish,
  publishing = false,
  errors,
}: WorkspaceProps) {
  const validation = validate(state)
  const shown = errors ?? validation.errors
  const summary = destinationSummary(state)
  const blocking = Object.values(shown)[0]

  return (
    <div className="flex flex-wrap items-center gap-3 border-t px-5 py-3">
      <div className="min-w-0 flex-1">
        <p className="truncate text-xs text-muted-foreground" role="status">
          {summary}
        </p>
        {!validation.valid && blocking && (
          <p className="truncate text-xs text-destructive">{blocking}</p>
        )}
      </div>
      <Button variant="outline" onClick={onCancel} disabled={publishing}>
        انصراف
      </Button>
      <Button onClick={onPublish} disabled={!validation.valid || publishing}>
        {publishLabel(state, publishing)}
      </Button>
    </div>
  )
}

function WizardFooter({
  step,
  onStep,
  mayAdvance,
  state,
  onCancel,
  onPublish,
  publishing = false,
}: WorkspaceProps & { step: WizardStep; onStep: (step: WizardStep) => void; mayAdvance: boolean }) {
  return (
    <div className="flex items-center gap-3 border-t px-4 py-3">
      {step === 1 ? (
        <Button variant="outline" onClick={onCancel} disabled={publishing} className="flex-1">
          انصراف
        </Button>
      ) : (
        <Button
          variant="outline"
          onClick={() => onStep((step - 1) as WizardStep)}
          disabled={publishing}
          className="flex-1"
        >
          بازگشت
        </Button>
      )}

      {step < 3 ? (
        <Button
          onClick={() => onStep((step + 1) as WizardStep)}
          disabled={!mayAdvance || publishing}
          className="flex-1"
        >
          ادامه
        </Button>
      ) : (
        <Button onClick={onPublish} disabled={!validate(state).valid || publishing} className="flex-1">
          {publishLabel(state, publishing)}
        </Button>
      )}
    </div>
  )
}

// destinationSummary is the footer's one-line answer to "where is this going?".
function destinationSummary(state: PublishDraftState): string {
  if (state.platform === "telegram") {
    const count = state.telegram.channelIds.length
    if (!state.telegram.accountID) return "حساب تلگرام انتخاب نشده است."
    return count === 0
      ? "کانالی انتخاب نشده است."
      : `ارسال به ${faDigits(count)} کانال`
  }
  return state.wordpress.siteId === null
    ? "سایت وردپرس انتخاب نشده است."
    : "انتشار در سایت وردپرس انتخاب‌شده"
}
