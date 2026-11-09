// The publishing dialog: one source message, prepared for publication.
//
// This is the shell. It loads what the pickers need, holds the one draft both
// layouts edit, and sends what the draft says. The workspace itself lives under
// components/publish and is drawn either as three columns side by side or as
// three steps, depending on the viewport.
//
// The source message is read once and never written to. Everything the user
// changes lands in the draft, and the outgoing request is built from that
// draft rather than from the editor's own state.

import * as React from "react"

import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogHeader,
  DialogTitle,
} from "@workspace/ui/components/dialog"
import { useToast } from "@workspace/ui/components/toast"

import { ConfirmDialog } from "@/components/confirm-dialog"
import {
  emptyOptions,
  type ChannelOption,
  type PublishingOptions,
  type SiteOption,
} from "@/components/publish/publish-options"
import { usePublishMessageDraft } from "@/components/publish/use-publish-draft"
import {
  DesktopPublishingWorkspace,
  MobilePublishingWizard,
  type WizardStep,
} from "@/components/publish/publish-workspace"
import { faDigits } from "@/components/persian-digits"
import { ErrorState, LoadingState } from "@/components/states"
import { ApiError, apiRequest } from "@/lib/api"
import {
  buildTelegramPayloads,
  buildWordPressPayload,
  layoutFor,
  sourceAttachmentsFrom,
  validate,
  type PublishDraftState,
  type PublishSource,
  type WorkspaceLayout,
} from "@/lib/publish-draft"
import { getToken } from "@/lib/session"
import { publishErrorText } from "@/lib/publish-errors"
import type { Entity, MessageMedia, TelegramServer, WordPressSite } from "@/lib/types"

interface MessageLike {
  id: number
  text?: string
  entities?: Entity[]
  media?: MessageMedia | null
}

export interface PublishDialogProps {
  open: boolean
  onOpenChange: (open: boolean) => void
  localChannelId: number
  message: MessageLike
  onSuccess: (summary: string) => void
}

// ---------------------------------------------------------------------------
// Publishing outcome
// ---------------------------------------------------------------------------

export type PublishStatus =
  | "idle"
  | "validating"
  | "publishing"
  | "success"
  | "partial"
  | "failure"

// DestinationResult is what one destination answered. A multi-channel publish
// reports these individually, so a partial success is never dressed up as a
// complete one.
export interface DestinationResult {
  label: string
  ok: boolean
  detail?: string
  // requestId is the id the API gave the attempt. It is shown so a reader can
  // report a problem, and carries nothing but that id.
  requestId?: string
}

// explainFailure is the sentence a reader is shown for a failed call.
//
// It is the category's own Persian wording, never the backend's English: the
// API's `code` is the contract and its message is not. The request id travels
// beside it as a separate fact, so a reader can report the failure without the
// message carrying anything internal.
function explainFailure(err: unknown, fallback: string): { detail: string; requestId?: string } {
  if (err instanceof ApiError) {
    return { detail: publishErrorText(err.code, err.message), requestId: err.requestId }
  }
  return { detail: fallback }
}

// CONCURRENCY bounds how many Telegram sends run at once. The backend takes one
// destination per call, so a multi-channel publish is a short run of those; two
// at a time keeps a failure from being reported before its neighbours settle,
// without opening a queue.
const CONCURRENCY = 2

// fingerprint is a short stable hash of a request body. It is what makes one
// idempotency key mean "this exact request", so retrying after a network error
// replays instead of posting a second time.
function fingerprint(value: unknown): string {
  const raw = JSON.stringify(value)
  let hash = 5381
  for (let i = 0; i < raw.length; i++) {
    hash = ((hash << 5) + hash + raw.charCodeAt(i)) | 0
  }
  return (hash >>> 0).toString(36)
}

// ---------------------------------------------------------------------------
// Layout
// ---------------------------------------------------------------------------

// useWorkspaceLayout watches the viewport and reports which presentation to
// draw. Both presentations edit the same draft, so switching between them never
// loses anything the user has done.
function useWorkspaceLayout(): WorkspaceLayout {
  const [layout, setLayout] = React.useState<WorkspaceLayout>(() =>
    typeof window === "undefined" ? "desktop" : layoutFor(window.innerWidth),
  )
  React.useEffect(() => {
    const query = window.matchMedia("(min-width: 1200px)")
    const apply = () => setLayout(layoutFor(query.matches ? 1200 : 1199))
    apply()
    query.addEventListener("change", apply)
    return () => query.removeEventListener("change", apply)
  }, [])
  return layout
}

// ---------------------------------------------------------------------------
// Dialog
// ---------------------------------------------------------------------------

export function PublishDialog({
  open,
  onOpenChange,
  localChannelId,
  message,
  onSuccess,
}: PublishDialogProps) {
  const { toast } = useToast()
  const layout = useWorkspaceLayout()
  const [step, setStep] = React.useState<WizardStep>(1)

  // The source is derived once per message. The draft is seeded from it and is
  // never reseeded underneath an edit.
  const source: PublishSource = React.useMemo(
    () => ({
      localChannelId,
      messageId: message.id,
      text: message.text ?? "",
      entities: message.entities ?? [],
      attachments: sourceAttachmentsFrom(message.media),
    }),
    [localChannelId, message],
  )
  const draft = usePublishMessageDraft(source)

  const [options, setOptions] = React.useState<PublishingOptions>(emptyOptions)
  const [optionsError, setOptionsError] = React.useState<string | null>(null)

  const [status, setStatus] = React.useState<PublishStatus>("idle")
  const [results, setResults] = React.useState<DestinationResult[]>([])
  const [actionError, setActionError] = React.useState<string | null>(null)
  const [confirmingClose, setConfirmingClose] = React.useState(false)

  const publishing = status === "publishing"

  // Opening a fresh dialog starts at the first step and drops the last run's
  // answer. A draft is seeded per message, not per open, so a reopened dialog
  // still holds what was typed.
  React.useEffect(() => {
    if (!open) return
    setStep(1)
    setResults([])
    setActionError(null)
    setStatus("idle")
  }, [open])

  // Destinations are loaded once; the site's own terms follow the site.
  React.useEffect(() => {
    if (!open) return
    let cancelled = false
    const controller = new AbortController()
    setOptions((prev) => ({ ...prev, loading: { ...prev.loading, servers: true, sites: true } }))
    setOptionsError(null)
    const token = getToken()

    Promise.all([
      apiRequest<{ servers?: TelegramServer[] }>("GET", "/telegram", { token, signal: controller.signal }),
      apiRequest<{ sites?: WordPressSite[] }>("GET", "/local/wordpress", { token, signal: controller.signal }),
    ])
      .then(([serverRes, siteRes]) => {
        if (cancelled) return
        const servers = Array.isArray(serverRes.data?.servers) ? serverRes.data.servers : []
        const sites = Array.isArray(siteRes.data?.sites) ? (siteRes.data.sites as WordPressSite[]) : []
        setOptions((prev) => ({
          ...prev,
          servers,
          sites: sites.map((s): SiteOption => ({ id: s.id, title: s.title, domain: s.domain })),
          loading: { ...prev.loading, servers: false, sites: false },
        }))
      })
      .catch((err) => {
        if (cancelled || (err instanceof ApiError && err.code === "aborted")) return
        setOptionsError(err instanceof ApiError ? err.message : "بارگذاری مقصد ناموفق بود.")
        setOptions((prev) => ({ ...prev, loading: { ...prev.loading, servers: false, sites: false } }))
      })
    return () => {
      cancelled = true
      controller.abort()
    }
  }, [open])

  // Channels belong to one connection. Changing the connection empties the list
  // first, so a stale answer for the previous one can never be shown against
  // the new one, and a channel id from the old connection can never be sent.
  const clientKey = draft.state.telegram.clientKey
  React.useEffect(() => {
    setOptions((prev) => ({ ...prev, channels: [] }))
    if (!open || !clientKey) return
    let cancelled = false
    const controller = new AbortController()
    setOptions((prev) => ({ ...prev, loading: { ...prev.loading, channels: true } }))
    apiRequest<{ channels?: Array<{ id: number; title: string; username?: string; permissions?: { new?: boolean } }> }>(
      "GET",
      `/telegram/${encodeURIComponent(clientKey)}/channels`,
      { token: getToken(), query: { page: 1, per_page: 100, capability: "send" }, signal: controller.signal },
    )
      .then((res) => {
        if (cancelled) return
        const rows = Array.isArray(res.data?.channels) ? res.data.channels : []
        setOptions((prev) => ({
          ...prev,
          channels: rows.map(
            (c): ChannelOption => ({
              id: String(c.id),
              title: c.title,
              username: c.username,
              // The backend already narrowed this list to channels the account
              // may post in. The flag is still read, never assumed.
              canPublish: c.permissions?.new !== false,
            }),
          ),
          loading: { ...prev.loading, channels: false },
        }))
      })
      .catch((err) => {
        if (cancelled || (err instanceof ApiError && err.code === "aborted")) return
        setOptions((prev) => ({
          ...prev,
          errors: { ...prev.errors, channels: err instanceof ApiError ? err.message : "بارگذاری کانال‌ها ناموفق بود." },
          loading: { ...prev.loading, channels: false },
        }))
      })
    return () => {
      cancelled = true
      controller.abort()
    }
  }, [open, clientKey])

  // WordPress terms are rows in one site's taxonomy. A different site has
  // different rows, so both lists are dropped and refetched when the site
  // changes: an id from one site is never submitted to another.
  const siteId = draft.state.wordpress.siteId
  React.useEffect(() => {
    setOptions((prev) => ({ ...prev, categories: [], tags: [] }))
    if (!open || siteId === null) return
    let cancelled = false
    const controller = new AbortController()
    const token = getToken()
    const base = `/local/wordpress/${siteId}`
    setOptions((prev) => ({
      ...prev,
      loading: { ...prev.loading, categories: true, tags: true },
    }))

    const load = async (path: "categories" | "tags") => {
      const res = await apiRequest<{ terms?: Array<{ id: number; name: string; slug?: string }> }>(
        "GET",
        `${base}/${path}`,
        { token, query: { page: 1, per_page: 100 }, signal: controller.signal },
      )
      const rows = Array.isArray(res.data?.terms) ? res.data.terms : []
      return rows.map((t) => ({ id: t.id, name: t.name, slug: t.slug }))
    }

    Promise.all([load("categories"), load("tags")])
      .then(([categories, tags]) => {
        if (cancelled) return
        setOptions((prev) => ({
          ...prev,
          categories,
          tags,
          loading: { ...prev.loading, categories: false, tags: false },
        }))
      })
      .catch((err) => {
        if (cancelled || (err instanceof ApiError && err.code === "aborted")) return
        const message = err instanceof ApiError ? err.message : "بارگذاری دسته‌بندی‌ها ناموفق بود."
        setOptions((prev) => ({
          ...prev,
          errors: { ...prev.errors, categories: message, tags: message },
          loading: { ...prev.loading, categories: false, tags: false },
        }))
      })
    return () => {
      cancelled = true
      controller.abort()
    }
  }, [open, siteId])

  // A new message starts a new draft and a new wizard run.
  React.useEffect(() => {
    setStep(1)
  }, [localChannelId, message.id])

  const requestClose = React.useCallback(() => {
    if (publishing) return
    // An untouched draft closes without asking. Asking about one nobody typed
    // into is noise.
    if (draft.dirty && status !== "success") {
      setConfirmingClose(true)
      return
    }
    onOpenChange(false)
  }, [publishing, draft.dirty, status, onOpenChange])

  const submit = React.useCallback(async () => {
    if (publishing) return
    const state = draft.state
    const verdict = validate(state)
    if (!verdict.valid) {
      setStatus("failure")
      setActionError(Object.values(verdict.errors)[0] ?? "این پیام هنوز آمادهٔ ارسال نیست.")
      return
    }

    setStatus("publishing")
    setActionError(null)
    setResults([])
    try {
      const outcome = await publish(state, source, (result) =>
        setResults((prev) => [...prev, result]),
      )
      setResults(outcome.results)

      if (outcome.status === "success") {
        const summary =
          state.platform === "wordpress"
            ? "مقاله با موفقیت منتشر شد."
            : `پیام با موفقیت به ${faDigits(outcome.results.length)} کانال ارسال شد.`
        setStatus("success")
        toast({ variant: "success", title: summary })
        onSuccess(summary)
        onOpenChange(false)
        return
      }

      setStatus(outcome.status === "partial" ? "partial" : "failure")
      const failed = outcome.results.filter((r) => !r.ok)
      setActionError(
        outcome.status === "partial"
          ? `${faDigits(failed.length)} مورد ارسال نشد. موارد موفق دوباره ارسال نمی‌شوند.`
          : (failed[0]?.detail ?? "ارسال ناموفق بود."),
      )
    } catch (err) {
      setStatus("failure")
      setActionError(explainFailure(err, "ارسال ناموفق بود.").detail)
    }
  }, [publishing, draft.state, source, toast, onSuccess, onOpenChange])

  const sidebar = (
    <>
      {optionsError && <ErrorState message={optionsError} />}
      {status !== "idle" && status !== "publishing" && (
        <PublishReport status={status} results={results} error={actionError} />
      )}
    </>
  )

  return (
    <>
      <Dialog
        open={open}
        onOpenChange={(next: boolean) => {
          if (!next) requestClose()
        }}
      >
        <DialogContent
          className={
            layout === "desktop"
              ? "flex h-[88dvh] w-[min(1450px,95vw)] max-w-none flex-col gap-0 overflow-hidden p-0 sm:max-w-none"
              : "flex h-[92dvh] w-[min(720px,96vw)] max-w-none flex-col gap-0 overflow-hidden p-0 sm:max-w-none"
          }
        >
          <DialogHeader className="shrink-0 border-b px-5 py-3">
            <DialogTitle>ارسال پیام</DialogTitle>
            <DialogDescription>
              مقصد را انتخاب کنید، محتوا را ویرایش کنید و پیش از انتشار بررسی کنید.
            </DialogDescription>
          </DialogHeader>

          {options.loading.servers && options.servers.length === 0 && !optionsError ? (
            <div className="flex flex-1 items-center justify-center">
              <LoadingState label="در حال بارگذاری مقاصد…" />
            </div>
          ) : layout === "desktop" ? (
            <DesktopPublishingWorkspace
              state={draft.state}
              options={options}
              onChange={draft.update}
              onCancel={requestClose}
              onPublish={submit}
              publishing={publishing}
              errors={draft.display.errors}
            />
          ) : (
            <MobilePublishingWizard
              step={step}
              onStep={setStep}
              state={draft.state}
              options={options}
              onChange={draft.update}
              onCancel={requestClose}
              onPublish={submit}
              publishing={publishing}
              errors={draft.display.errors}
            />
          )}

          {sidebar}
        </DialogContent>
      </Dialog>

      <ConfirmDialog
        open={confirmingClose}
        onOpenChange={setConfirmingClose}
        title="تغییرات ذخیره نشده است"
        description="متنی که ویرایش کرده‌اید ارسال نشده است. با بستن این پنجره از بین می‌رود."
        confirmLabel="بستن بدون ارسال"
        destructive
        onConfirm={() => {
          setConfirmingClose(false)
          onOpenChange(false)
        }}
      />
    </>
  )
}

// ---------------------------------------------------------------------------
// Sending
// ---------------------------------------------------------------------------

interface PublishOutcome {
  status: "success" | "partial" | "failure"
  results: DestinationResult[]
}

// publish sends the draft to every destination it names.
//
// The Telegram backend takes one channel per call, so a multi-channel publish
// is a bounded run of those with one result each. Destinations that succeeded
// are never retried: only the ones that did not are left for the user to decide
// about, and the draft stays exactly as it was.
async function publish(
  state: PublishDraftState,
  source: PublishSource,
  onResult: (result: DestinationResult) => void,
): Promise<PublishOutcome> {
  const token = getToken()

  if (state.platform === "wordpress") {
    const body = buildWordPressPayload(state)
    const siteId = state.wordpress.siteId
    try {
      await apiRequest("POST", `/local/wordpress/${siteId}/posts`, {
        token,
        body,
        headers: { "Idempotency-Key": `pub-wp-${siteId}-${source.localChannelId}-${source.messageId}-${fingerprint(body)}` },
      })
      onResult({ label: "وردپرس", ok: true })
      return { status: "success", results: [{ label: "وردپرس", ok: true }] }
    } catch (err) {
      const { detail, requestId } = explainFailure(err, "انتشار ناموفق بود.")
      onResult({ label: "وردپرس", ok: false, detail, requestId })
      return { status: "failure", results: [{ label: "وردپرس", ok: false, detail, requestId }] }
    }
  }

  const clientKey = state.telegram.clientKey!
  const bodies = buildTelegramPayloads(state)
  const labels = new Map(
    state.telegram.channelIds.map((id) => [id, id] as const),
  )
  const results: DestinationResult[] = new Array(bodies.length)

  let next = 0
  const worker = async () => {
    for (;;) {
      const index = next++
      if (index >= bodies.length) return
      const body = bodies[index]
      const label = labels.get(body.destination.channel_id) ?? body.destination.channel_id
      try {
        await apiRequest("POST", `/telegram/${encodeURIComponent(clientKey)}/messages/copy`, {
          token,
          body,
          headers: {
            "Idempotency-Key": `pub-${clientKey}-${source.localChannelId}-${source.messageId}-${body.destination.channel_id}-${fingerprint(body)}`,
          },
        })
        results[index] = { label, ok: true }
        onResult({ label, ok: true })
      } catch (err) {
        const { detail, requestId } = explainFailure(err, "ارسال ناموفق بود.")
        results[index] = { label, ok: false, detail, requestId }
        onResult({ label, ok: false, detail, requestId })
      }
    }
  }

  await Promise.all(
    Array.from({ length: Math.min(CONCURRENCY, bodies.length) }, () => worker()),
  )

  const ok = results.filter((r) => r?.ok).length
  return {
    status: ok === results.length ? "success" : ok > 0 ? "partial" : "failure",
    results: results.filter(Boolean),
  }
}

// PublishReport is the honest account of what a run achieved. A destination
// that failed is named, and the ones that succeeded are not sent again.
function PublishReport({
  status,
  results,
  error,
}: {
  status: PublishStatus
  results: DestinationResult[]
  error: string | null
}) {
  if (results.length === 0 && !error) return null
  return (
    <div className="border-t px-5 py-3">
      {error && (
        <p role="alert" className="mb-2 rounded-lg bg-destructive/10 px-3 py-2 text-sm text-destructive">
          {error}
        </p>
      )}
      {results.length > 0 && (
        <ul className="flex flex-col gap-1 text-xs">
          {results.map((result, index) => (
            <li key={`${result.label}-${index}`} className="flex items-start gap-2">
              <span aria-hidden="true">{result.ok ? "✓" : "✕"}</span>
              <span className="min-w-0 flex-1">
                <span className="font-medium">{result.label}</span>
                {result.detail && (
                  <span className="text-muted-foreground"> — {result.detail}</span>
                )}
                {result.requestId && (
                  <span className="block text-[0.65rem] text-muted-foreground/70" dir="ltr">
                    کد پیگیری: {result.requestId}
                  </span>
                )}
                <span className="sr-only">{result.ok ? " موفق" : " ناموفق"}</span>
              </span>
            </li>
          ))}
        </ul>
      )}
      {status === "partial" && (
        <p className="mt-2 text-xs text-muted-foreground">
          موارد موفق دوباره ارسال نمی‌شوند؛ متن و مقصدِ موارد ناموفق حفظ شده است.
        </p>
      )}
    </div>
  )
}
