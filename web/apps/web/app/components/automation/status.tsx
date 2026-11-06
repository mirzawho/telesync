import {
  CheckIcon,
  CircleCheckIcon,
  CircleXIcon,
  CopyIcon,
} from "lucide-react"
import * as React from "react"

import { Badge } from "@workspace/ui/components/badge"
import { Button } from "@workspace/ui/components/button"

import {
  aiStatusLabels,
  aiStatusTone,
  deliveryStatusLabels,
  deliveryStatusTone,
  headerLines,
  hiddenHeaderCount,
  publicationStatusLabels,
  publicationStatusTone,
  runStatusLabels,
  runStatusTone,
  visibleHeaders,
  type AIStatus,
  type DeliveryStatus,
  type PublicationStatus,
  type RunStatus,
  type Tone,
} from "@/lib/automation"
import { faDigits } from "@/components/persian-digits"

// The one badge vocabulary for this area: a status word in Persian plus the
// tone that carries the meaning without the colour. Every screen reads a status
// through these, so a new status is one label and one tone here.

type BadgeVariant = React.ComponentProps<typeof Badge>["variant"]

const toneVariant: Record<Tone, BadgeVariant> = {
  default: "default",
  success: "success",
  warning: "warning",
  destructive: "destructive",
  muted: "muted",
}

function ToneBadge({ tone, children }: { tone: Tone; children: React.ReactNode }) {
  return <Badge variant={toneVariant[tone]}>{children}</Badge>
}

export function DeliveryStatusBadge({ status }: { status: DeliveryStatus }) {
  return <ToneBadge tone={deliveryStatusTone(status)}>{deliveryStatusLabels[status]}</ToneBadge>
}

export function RunStatusBadge({ status }: { status: RunStatus }) {
  return <ToneBadge tone={runStatusTone(status)}>{runStatusLabels[status]}</ToneBadge>
}

export function PublicationStatusBadge({ status }: { status: PublicationStatus }) {
  return (
    <ToneBadge tone={publicationStatusTone(status)}>
      {publicationStatusLabels[status]}
    </ToneBadge>
  )
}

export function AIStatusBadge({ status }: { status: AIStatus }) {
  return <ToneBadge tone={aiStatusTone(status)}>{aiStatusLabels[status]}</ToneBadge>
}

export function EnabledBadge({ enabled }: { enabled: boolean }) {
  return (
    <ToneBadge tone={enabled ? "success" : "muted"}>
      {enabled ? "فعال" : "غیرفعال"}
    </ToneBadge>
  )
}

// An on/off switch reads as a word as well as a colour, so the state is never
// carried by the hue alone.
export function SwitchState({ on, onLabel, offLabel }: {
  on: boolean
  onLabel: string
  offLabel: string
}) {
  return (
    <span className="inline-flex items-center gap-1.5">
      {on ? (
        <CircleCheckIcon className="size-4 text-emerald-600" aria-hidden="true" />
      ) : (
        <CircleXIcon className="size-4 text-muted-foreground" aria-hidden="true" />
      )}
      <span className="text-sm">{on ? onLabel : offLabel}</span>
    </span>
  )
}

// ---------------------------------------------------------------------------
// Copying a recorded body. The clipboard call is the browser's own and fails
// silently into a refusal toast; nothing here rewrites the text it copies.
// ---------------------------------------------------------------------------

export function CopyButton({
  value,
  label = "کپی",
  size = "icon-sm",
}: {
  value: string
  label?: string
  size?: "icon-sm" | "sm"
}) {
  // No toast here: a copy is a two-second acknowledgement on the button
  // itself, and a refusal is one line under it. Keeping the feedback local
  // means a recorded body can be copied from anywhere on the page without the
  // shell having to be mounted around it.
  const [state, setState] = React.useState<"idle" | "copied" | "failed">("idle")
  const timer = React.useRef<ReturnType<typeof setTimeout> | null>(null)
  React.useEffect(
    () => () => {
      if (timer.current) clearTimeout(timer.current)
    },
    []
  )

  return (
    <span className="flex items-center gap-1.5">
      <Button
        type="button"
        variant="ghost"
        size={size}
        aria-label={label}
        title={label}
        onClick={async () => {
          try {
            await navigator.clipboard.writeText(value)
            setState("copied")
          } catch {
            setState("failed")
          }
          if (timer.current) clearTimeout(timer.current)
          timer.current = setTimeout(() => setState("idle"), 2000)
        }}
      >
        {state === "copied" ? (
          <CheckIcon className="size-4" data-icon="inline-start" />
        ) : (
          <CopyIcon className="size-4" data-icon="inline-start" />
        )}
        {size === "sm" ? (state === "copied" ? "کپی شد" : label) : null}
      </Button>
      {state === "failed" && (
        <span role="alert" className="text-xs text-destructive">
          کپی انجام نشد.
        </span>
      )}
    </span>
  )
}

// ---------------------------------------------------------------------------
// Recorded request/response bodies.
//
// The server redacts and bounds these before storing them. This view renders
// exactly what arrived: a redaction marker stays a marker, a cut-off body stays
// cut off and says so.
// ---------------------------------------------------------------------------

export function CodeBlock({
  text,
  label,
  truncated,
  emptyText = "متنی ثبت نشده است.",
  copyable = true,
}: {
  text: string
  label: string
  truncated?: boolean
  emptyText?: string
  copyable?: boolean
}) {
  if (text === "") {
    return (
      <p className="text-muted-foreground rounded-lg border border-dashed px-3 py-2 text-xs">
        {emptyText}
      </p>
    )
  }
  return (
    <div className="flex flex-col gap-1.5">
      <div className="flex items-center justify-between gap-2">
        <h4 className="text-xs font-medium text-muted-foreground">{label}</h4>
        {copyable && <CopyButton value={text} label={`کپی ${label}`} />}
      </div>
      <pre
        dir="ltr"
        className="max-h-80 overflow-auto rounded-lg border bg-muted/40 px-3 py-2 text-start text-xs leading-relaxed whitespace-pre-wrap break-all"
      >
        {text}
      </pre>
      {truncated && (
        <p className="text-xs text-muted-foreground">
          این متن در زمان ثبت بریده شده است و بخشی از آن نگهداری نشده است.
        </p>
      )}
    </div>
  )
}

export function HeaderBlock({
  headers,
  label = "هدرها",
}: {
  headers: Record<string, string>
  label?: string
}) {
  // A header that carries or may carry a credential is withheld by name and the
  // count is said, so a record never reads as "there were no headers" and never
  // shows one either. The value is not rendered here and not copied out.
  const shown = visibleHeaders(headers)
  const hidden = hiddenHeaderCount(headers)
  const entries = Object.entries(shown)
  if (entries.length === 0) {
    return (
      <p className="text-muted-foreground rounded-lg border border-dashed px-3 py-2 text-xs">
        {hidden > 0
          ? `${faDigits(hidden)} هدر حاوی اطلاعات احراز ثبت شده است و نمایش داده نمی‌شود.`
          : "هدری ثبت نشده است."}
      </p>
    )
  }
  return (
    <div className="flex flex-col gap-1.5">
      <div className="flex items-center justify-between gap-2">
        <h4 className="text-xs font-medium text-muted-foreground">{label}</h4>
        <CopyButton
          value={headerLines(headers).join("\n")}
          label={`کپی ${label}`}
        />
      </div>
      <dl
        dir="ltr"
        className="divide-y overflow-auto rounded-lg border text-start text-xs"
      >
        {entries.map(([name, value]) => (
          <div key={name} className="flex gap-2 px-3 py-1.5">
            <dt className="font-mono text-muted-foreground shrink-0">{name}</dt>
            {/* dir="auto": a redacted marker or a value in either script. */}
            <dd dir="auto" className="min-w-0 break-all">
              {value}
            </dd>
          </div>
        ))}
      </dl>
      {hidden > 0 && (
        <p className="text-xs text-muted-foreground">
          {faDigits(hidden)} هدر حاوی اطلاعات احراز نمایش داده نمی‌شود.
        </p>
      )}
    </div>
  )
}

// ---------------------------------------------------------------------------
// Queue status: a few counts, never a chart.
// ---------------------------------------------------------------------------

export function StatusCount({
  label,
  value,
  tone = "muted",
}: {
  label: string
  value: string
  tone?: Tone
}) {
  return (
    <div className="flex min-w-0 flex-col gap-0.5 rounded-lg border px-3 py-2">
      <span className="text-xs text-muted-foreground">{label}</span>
      <span
        className={
          tone === "destructive"
            ? "text-base font-semibold text-destructive"
            : tone === "success"
              ? "text-base font-semibold text-emerald-700"
              : "text-base font-semibold"
        }
      >
        {value}
      </span>
    </div>
  )
}
