import {
  AlertCircleIcon,
  ArrowDownIcon,
  ArrowRightIcon,
  Clock3Icon,
  LoaderCircleIcon,
  RefreshCwIcon,
} from "lucide-react"
import * as React from "react"

import { ChannelAvatar } from "@/components/channel-avatar"
import { ChannelMessageComposer, type ChannelMessageMediaKind, type EditComposerDraft } from "@/components/channel-explorer/channel-message-composer"
import { ChannelMessageItem } from "@/components/channel-explorer/message-item"
import { ChannelMessageAlbum } from "@/components/channel-explorer/message-album"
import { faDigits } from "@/components/persian-digits"
import { PublishDialog } from "@/components/publish-dialog"
import { EmptyState, ErrorState } from "@/components/states"
import { useMessageTimeline } from "@/hooks/use-message-timeline"
import {
  captureAnchor,
  restoreScrollTop,
  type ItemBox,
  type ScrollAnchor,
} from "@/lib/scroll-anchor"
import { followNewest, showJumpToLatest } from "@/lib/message-timeline"
import { groupMessageAlbums } from "@/lib/message-albums"
import { jumpButtonOffset, messagePaneClearance } from "@/lib/message-pane-layout"
import type { ChannelDetail, ChannelMessage } from "@/lib/types"
import { ApiError, errorMessage } from "@/lib/api"
import {
  deleteChannelMessage,
  editChannelMessage,
  getEditOperation,
  getSendOperation,
  sendChannelMessage,
  type ChannelMessageEditPayload,
  type EditOperation,
  type EditAccepted,
  type SendOperation,
  type SendAccepted,
} from "@/lib/channel-message-api"
import { getToken } from "@/lib/session"
import { channelMessageCapabilities } from "@/lib/channel-message-permissions"
import { Button } from "@workspace/ui/components/button"
import { Skeleton } from "@workspace/ui/components/skeleton"
import { cn } from "@workspace/ui/lib/utils"

// The left pane: one channel's history as a timeline.
//
// The history is chronological, oldest at the top and newest at the bottom,
// because that is how a reader follows a channel. Opening a channel puts the
// newest message on screen; scrolling up grows the history above; scrolling
// down only ever reveals what is already in hand, unless something above is
// known to be missing.
//
// The two rules that make it feel solid: a reader's place is kept by naming the
// message they are on rather than by remembering a pixel height, and nothing
// fetches without either the reader moving or a gap being known to exist.

// How close to the bottom counts as "reading the newest message". Small enough
// to feel immediate, large enough that a rounded scrollbar does not flicker.
const NEAR_BOTTOM = 80

// How close to the top triggers the next page of history on its own.
const NEAR_TOP = 400

export type PendingSend = {
  localID: string
  operationID?: string
  identity: string
  status: string
  text: string
  file: File | null
  mediaKind: ChannelMessageMediaKind | null
  error: string | null
}

type RecoveredDraft = { key: string; text: string; file: File | null; mediaKind: ChannelMessageMediaKind | null }

function newIntentKey(): string {
  return typeof crypto !== "undefined" && "randomUUID" in crypto
    ? crypto.randomUUID()
    : `${Date.now()}-${Math.random().toString(36).slice(2)}`
}

function statusText(status: string, operation: "send" | "edit"): string {
  if (status === "queued") return "در صف انجام…"
  if (status === "processing") return operation === "send" ? "در حال ارسال…" : "در حال ویرایش…"
  if (status === "retrying") return "در صف تلاش دوباره…"
  if (status === "sent") return "در حال همگام‌سازی پیام…"
  if (status === "sync_failed") return "ارسال تأیید شد؛ همگام‌سازی تاریخچه کامل نشد."
  if (status === "edited") return "پیام ویرایش شد."
  if (status === "failed") return "عملیات ناموفق بود."
  if (status === "unknown") return "نتیجه نامشخص است؛ برای جلوگیری از تکرار، خودکار دوباره ارسال نمی‌شود."
  return `وضعیت عملیات: ${status}`
}

function waitForPoll(signal: AbortSignal, ms: number): Promise<void> {
  return new Promise((resolve, reject) => {
    if (signal.aborted) {
      reject(new Error("aborted"))
      return
    }
    const timeout = window.setTimeout(() => {
      signal.removeEventListener("abort", abort)
      resolve()
    }, ms)
    const abort = () => {
      window.clearTimeout(timeout)
      reject(new Error("aborted"))
    }
    signal.addEventListener("abort", abort, { once: true })
  })
}

async function pollOperation<T extends { status: string }>(
  read: (signal: AbortSignal) => Promise<T>,
  signal: AbortSignal,
  terminal: (status: string) => boolean,
  update: (operation: T) => void
): Promise<T | null> {
  let delay = 900
  for (let attempt = 0; attempt < 40; attempt++) {
    await waitForPoll(signal, delay)
    if (signal.aborted) return null
    const operation = await read(signal)
    update(operation)
    if (terminal(operation.status)) return operation
    delay = Math.min(5_000, Math.round(delay * 1.45))
  }
  return null
}

export function ChannelMessagesPane({
  channel,
  channelId,
  client,
  onBack,
}: {
  channel: ChannelDetail
  // channelId is the local managed-channel id. It is what the messages route
  // takes and what the URL holds; the channel object's own id is Telegram's.
  channelId: number
  // client is the record's Telegram client key: a custom-emoji reaction is
  // fetched through it, the same client the messages were read from.
  client: string
  onBack?: () => void
}) {
  const capabilities = channelMessageCapabilities(channel.permissions)
  const canRead = capabilities.read
  const remoteIDValid = /^[1-9]\d*$/.test(channel.id)
  const identityValid = Boolean(client.trim()) && remoteIDValid
  const identity = `${client}\u001f${channel.id}`
  const { state, requestOlder, requestNewer, retry, refreshLoaded, replaceMessage, removeMessage } =
    useMessageTimeline(canRead ? channelId : null, identity)
  const scroller = React.useRef<HTMLDivElement>(null)
  const content = React.useRef<HTMLDivElement>(null)
  const footer = React.useRef<HTMLDivElement>(null)
  const topSentinel = React.useRef<HTMLDivElement>(null)
  const bottomSentinel = React.useRef<HTMLDivElement>(null)
  const footerHeightRef = React.useRef(0)
  const [footerHeight, setFooterHeight] = React.useState(0)

  // The reader's place across a prepend, and whether they are following the
  // newest message. Both are refs so a scroll never costs a render.
  const anchor = React.useRef<ScrollAnchor | null>(null)
  const following = React.useRef(true)
  const [nearBottom, setNearBottom] = React.useState(true)
  const [publishing, setPublishing] = React.useState<{ message: ChannelMessage; albumMessages?: ChannelMessage[] } | null>(null)
  const [editing, setEditing] = React.useState<ChannelMessage | null>(null)
  const [editingIdentity, setEditingIdentity] = React.useState<string | null>(null)
  const [editBusy, setEditBusy] = React.useState(false)
  const [editUnknown, setEditUnknown] = React.useState(false)
  const [editError, setEditError] = React.useState<string | null>(null)
  const [editStatus, setEditStatus] = React.useState<string | null>(null)
  const [submitting, setSubmitting] = React.useState(false)
  const [sendError, setSendError] = React.useState<string | null>(null)
  const [pendingSends, setPendingSends] = React.useState<PendingSend[]>([])
  const [recoveredDraft, setRecoveredDraft] = React.useState<RecoveredDraft | null>(null)
  const [deletingID, setDeletingID] = React.useState<number | null>(null)
  const [deleteErrors, setDeleteErrors] = React.useState<Record<number, string>>({})
  const [revoked, setRevoked] = React.useState({ new: false, edit: false, delete: false })
  const [notice, setNotice] = React.useState<string | null>(null)
  const controllers = React.useRef(new Set<AbortController>())
  const identityRef = React.useRef(identity)
  identityRef.current = identity
  const sendIntent = React.useRef<{ fingerprint: string; key: string } | null>(null)
  const editIntent = React.useRef<{ fingerprint: string; key: string } | null>(null)

  const writable = identityValid && capabilities.create && !revoked.new
  const editable = identityValid && capabilities.edit && !revoked.edit
  const deletable = identityValid && capabilities.delete && !revoked.delete
  const activeEditing = editingIdentity === identity ? editing : null
  const showFloatingFooter = writable || Boolean(activeEditing && editable) || Boolean(notice)

  React.useEffect(() => {
    for (const controller of controllers.current) controller.abort()
    controllers.current.clear()
    setPendingSends([])
    setRecoveredDraft(null)
    setEditing(null)
    setEditingIdentity(null)
    setEditBusy(false)
    setEditUnknown(false)
    setEditError(null)
    setEditStatus(null)
    setSubmitting(false)
    setSendError(null)
    setDeletingID(null)
    setDeleteErrors({})
    setRevoked({ new: false, edit: false, delete: false })
    setNotice(null)
    sendIntent.current = null
    editIntent.current = null
  }, [identity])

  React.useEffect(() => () => {
    for (const controller of controllers.current) controller.abort()
    controllers.current.clear()
  }, [])

  // "Loading" until the first answer is in: the pane must not claim the channel
  // is empty in the moment before the first page has been asked for.
  const asked = state.page > 0 || state.loading !== null || state.olderError !== null
  const loadingInitial = state.messages.length === 0 && !asked
  const loadingOlder = state.loading === "older"
  const loadingNewer = state.loading === "newer"

  // --- reading position ----------------------------------------------------

  const boxes = React.useCallback((): ItemBox[] => {
    const wrap = content.current
    const box = scroller.current
    if (!wrap || !box) return []
    const wrapTop = wrap.getBoundingClientRect().top
    return [...wrap.querySelectorAll<HTMLElement>("[data-message-id]")].map((el) => ({
      id: Number(el.dataset.messageId),
      top: el.getBoundingClientRect().top - wrapTop,
      height: el.offsetHeight,
    }))
  }, [])

  const capture = React.useCallback(() => {
    const box = scroller.current
    if (!box) return
    anchor.current = captureAnchor(
      { scrollTop: box.scrollTop, height: box.clientHeight },
      boxes()
    )
  }, [boxes])

  const restore = React.useCallback(() => {
    const box = scroller.current
    const saved = anchor.current
    if (!box || !saved) return
    const wanted = restoreScrollTop(
      { scrollTop: box.scrollTop, height: box.clientHeight },
      boxes(),
      saved,
      box.scrollHeight
    )
    if (wanted !== null) box.scrollTop = wanted
  }, [boxes])

  // Keep the reader where they were whenever history arrives above them.
  React.useLayoutEffect(() => {
    if (state.loading !== null) return
    restore()
  }, [state.messages, state.loading, restore])

  // Media answers late and changes heights above the reader. While an anchor is
  // held, content resizes put it back; the observer is only attached then, so
  // nothing measures the DOM while the reader is simply scrolling.
  React.useEffect(() => {
    const wrap = content.current
    if (!wrap || typeof ResizeObserver === "undefined") return
    const observer = new ResizeObserver(() => {
      if (anchor.current) restore()
    })
    observer.observe(wrap)
    return () => observer.disconnect()
  }, [restore])

  // Measure the complete floating footer, including safe-area padding and any
  // changing composer context, attachment preview, or validation copy.
  React.useEffect(() => {
    const element = footer.current
    if (!element || typeof ResizeObserver === "undefined") return
    const observer = new ResizeObserver(([entry]) => {
      const nextHeight = Math.ceil(entry?.borderBoxSize?.[0]?.blockSize ?? element.getBoundingClientRect().height)
      if (footerHeightRef.current === nextHeight) return
      footerHeightRef.current = nextHeight
      const box = scroller.current
      const wasAtBottom = following.current || Boolean(
        box && box.scrollHeight - box.scrollTop - box.clientHeight <= NEAR_BOTTOM
      )
      setFooterHeight(nextHeight)
      if (wasAtBottom && box) {
        requestAnimationFrame(() => {
          if (!following.current) return
          box.scrollTop = box.scrollHeight
          setNearBottom(true)
        })
      }
    })
    observer.observe(element)
    return () => observer.disconnect()
  }, [showFloatingFooter])

  // --- following the newest message ---------------------------------------

  // The first page puts the reader at the newest message, where they would open
  // the channel in Telegram itself. Once per channel: media arriving later must
  // not throw them back to the bottom while they read.
  const settled = React.useRef<string | null>(null)
  React.useLayoutEffect(() => {
    const box = scroller.current
    if (!box || state.messages.length === 0) return
    if (settled.current === identity) return
    settled.current = identity
    following.current = true
    box.scrollTop = box.scrollHeight
    setNearBottom(true)
  }, [identity, state.messages.length])

  // Newer messages follow the reader only when they were already at the
  // bottom. Appending never disturbs someone reading back through history.
  const newestId = state.messages.at(-1)?.id
  const pendingActivity = pendingSends
    .filter((pending) => pending.identity === identity)
    .map((pending) => `${pending.localID}:${pending.status}`)
    .join("|")
  React.useLayoutEffect(() => {
    if (newestId === undefined) return
    if (!following.current || !followNewest(state, nearBottom)) return
    const box = scroller.current
    if (box) box.scrollTop = box.scrollHeight
    // The message identity is what changed; re-running on every state tweak
    // would move the reader for reasons that are not new messages.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [newestId])

  React.useLayoutEffect(() => {
    if (!pendingActivity || !following.current || !nearBottom) return
    const box = scroller.current
    if (box) box.scrollTop = box.scrollHeight
  }, [pendingActivity, nearBottom])

  // Scroll reporting is coalesced: the state only changes when the reader
  // crosses the threshold, not once per pixel.
  React.useEffect(() => {
    const box = scroller.current
    if (!box) return
    let frame = 0
    const read = () => {
      frame = 0
      const atBottom =
        box.scrollHeight - box.scrollTop - box.clientHeight <= NEAR_BOTTOM
      following.current = atBottom
      // A reader who scrolls back into history stops being followed; being at
      // the bottom starts it again.
      anchor.current = null
      setNearBottom((was) => (was === atBottom ? was : atBottom))
    }
    const onScroll = () => {
      if (frame) return
      frame = requestAnimationFrame(read)
    }
    box.addEventListener("scroll", onScroll, { passive: true })
    return () => {
      if (frame) cancelAnimationFrame(frame)
      box.removeEventListener("scroll", onScroll)
    }
  }, [])

  // --- paging --------------------------------------------------------------

  // Reaching the top of the history asks for the page below the oldest message
  // in hand. Reaching the bottom asks only when something above is missing:
  // scrolling through what is loaded is not a reason to talk to the API.
  React.useEffect(() => {
    const top = topSentinel.current
    if (!top || typeof IntersectionObserver === "undefined") return
    const observer = new IntersectionObserver(
      (entries) => {
        if (!entries.some((entry) => entry.isIntersecting)) return
        capture()
        requestOlder()
      },
      { root: scroller.current, rootMargin: `${NEAR_TOP}px 0px` }
    )
    observer.observe(top)
    return () => observer.disconnect()
  }, [capture, requestOlder])

  React.useEffect(() => {
    const bottom = bottomSentinel.current
    if (!bottom || typeof IntersectionObserver === "undefined") return
    const observer = new IntersectionObserver(
      (entries) => {
        if (!entries.some((entry) => entry.isIntersecting)) return
        requestNewer()
      },
      { root: scroller.current, rootMargin: `${NEAR_BOTTOM}px 0px` }
    )
    observer.observe(bottom)
    return () => observer.disconnect()
  }, [requestNewer])

  const jumpToLatest = () => {
    requestNewer()
    const box = scroller.current
    if (box) box.scrollTo({ top: box.scrollHeight, behavior: "smooth" })
    following.current = true
    setNearBottom(true)
    anchor.current = null
  }

  const showJump = showJumpToLatest(state, nearBottom)

  const revoke = (capability: "new" | "edit" | "delete") => {
    setRevoked((current) => ({ ...current, [capability]: true }))
  }

  const send = async (input: {
    text: string
    file: File | null
    mediaKind: ChannelMessageMediaKind | null
  }) => {
    const startedFor = identity
    if (!writable || !identityValid) {
      setSendError("مجوز یا مقصد ارسال پیام در دسترس نیست.")
      return
    }
    const fingerprint = JSON.stringify([
      input.text,
      input.mediaKind,
      input.file?.name,
      input.file?.size,
      input.file?.lastModified,
    ])
    if (!sendIntent.current || sendIntent.current.fingerprint !== fingerprint) {
      sendIntent.current = { fingerprint, key: newIntentKey() }
    }
    const idempotencyKey = sendIntent.current.key
    const localID = newIntentKey()
    setPendingSends((current) => [...current, {
      localID, identity: startedFor, status: "accepting", text: input.text,
      file: input.file, mediaKind: input.mediaKind, error: null,
    }])
    const token = getToken()
    const payload = {
      text: input.text,
      entities: [],
      ...(input.file && input.mediaKind ? { media: { kind: input.mediaKind } } : {}),
      options: { silent: false, disable_web_page_preview: false },
    }
    setSubmitting(true)
    setSendError(null)
    const submitController = new AbortController()
    controllers.current.add(submitController)
    try {
      const accepted: SendAccepted = await sendChannelMessage(
        client,
        channel.id,
        payload,
        idempotencyKey,
        { token, file: input.file ?? undefined, signal: submitController.signal }
      )
      if (identityRef.current !== startedFor) return
      if (
        !accepted?.operation_id ||
        accepted.destination?.client_key !== client ||
        accepted.destination?.channel_id !== channel.id
      ) {
        throw new Error("پاسخ پذیرش، مقصد پیام را تأیید نکرد؛ نتیجه نامشخص است.")
      }
      sendIntent.current = null
      const pending: PendingSend = {
        localID,
        identity: startedFor,
        operationID: accepted.operation_id,
        status: accepted.status,
        text: input.text,
        file: input.file,
        mediaKind: input.mediaKind,
        error: null,
      }
      setPendingSends((current) => current.map((item) => item.localID === localID ? pending : item))
      const controller = new AbortController()
      controllers.current.add(controller)
      void pollOperation<SendOperation>(
        (signal) => getSendOperation(client, channel.id, accepted.operation_id, { token, signal }),
        controller.signal,
        (status) => !["queued", "processing", "retrying"].includes(status),
        (operation) => {
          if (identityRef.current !== startedFor) return
          setPendingSends((current) => current.map((item) => item.localID === localID
            ? { ...item, status: operation.status, error: operation.error?.message ?? null }
            : item))
        }
      ).then(async (operation) => {
        if (identityRef.current !== startedFor || controller.signal.aborted) return
        const status = operation && ["sent", "failed", "unknown"].includes(operation.status)
          ? operation.status
          : "unknown"
        if (status === "sent" && operation?.result?.message_id) {
          let reconciled = false
          for (let attempt = 0; attempt < 5 && !reconciled; attempt++) {
            if (controller.signal.aborted || identityRef.current !== startedFor) return
            try {
              const message = await refreshLoaded(operation.result.message_id)
              reconciled = Boolean(message)
            } catch {
              // History can lag behind the operation state; retry a bounded number of times.
            }
            if (!reconciled && attempt < 4) await waitForPoll(controller.signal, 700 + attempt * 500)
          }
          if (identityRef.current === startedFor) {
            setPendingSends((current) => reconciled
              ? current.filter((item) => item.localID !== localID)
              : current.map((item) => item.localID === localID
                ? { ...item, status: "sync_failed", error: null }
                : item))
          }
        } else if (status === "sent") {
          setPendingSends((current) => current.map((item) => item.localID === localID
            ? { ...item, status: "sync_failed", error: "شناسهٔ پیام برای همگام‌سازی موجود نیست." }
            : item))
        } else if (status === "failed") {
          setPendingSends((current) => current.map((item) => item.localID === localID
            ? { ...item, status: "failed", error: "ارسال انجام نشد." }
            : item))
        } else if (status === "unknown") {
          setPendingSends((current) => current.map((item) => item.localID === localID
            ? { ...item, status: "unknown", error: "نتیجه نامشخص است؛ برای جلوگیری از پیام تکراری، خودکار دوباره ارسال نمی‌شود." }
            : item))
        } else if (!operation) {
          setPendingSends((current) => current.map((item) => item.localID === localID
            ? { ...item, status: "unknown", error: statusText("unknown", "send") }
            : item))
        }
      }).catch((error: unknown) => {
        if (identityRef.current === startedFor && !controller.signal.aborted) {
          setPendingSends((current) => current.map((item) => item.localID === localID
            ? { ...item, status: "unknown", error: errorMessage(error) }
            : item))
          setPendingSends((current) => current.map((item) => item.localID === localID
            ? { ...item, status: "unknown", error: "وضعیت ارسال مشخص نیست؛ خودکار دوباره ارسال نمی‌شود." }
            : item))
        }
      }).finally(() => controllers.current.delete(controller))
    } catch (error) {
      setPendingSends((current) => current.filter((item) => item.localID !== localID))
      if (identityRef.current === startedFor) {
        if (error instanceof ApiError && error.isForbidden) revoke("new")
        setSendError(errorMessage(error, "ارسال پیام ناموفق بود."))
      }
      throw error
    } finally {
      controllers.current.delete(submitController)
      if (identityRef.current === startedFor) setSubmitting(false)
    }
  }

  const beginEdit = (message: ChannelMessage) => {
    if (!editable || editBusy || editUnknown) return
    setEditing(message)
    setEditingIdentity(identity)
    setEditBusy(false)
    setEditUnknown(false)
    setEditError(null)
    setEditStatus(null)
    editIntent.current = null
  }

  const saveEdit = async (input: EditComposerDraft): Promise<boolean> => {
    const target = editing
    const startedFor = identity
    if (!target || !editable || editUnknown) {
      setEditError(editUnknown
        ? "نتیجه ویرایش نامشخص است؛ برای جلوگیری از اجرای دوباره، عملیات تازه‌ای ارسال نکنید."
        : "مجوز ویرایش پیام در دسترس نیست.")
      return false
    }
    if (input.mediaAction === "replace" && (!input.file || !input.mediaKind)) {
      setEditError("برای جایگزینی رسانه، فایل و نوع آن را انتخاب کنید.")
      return false
    }
    if (input.mediaAction === "remove" && !target.media) {
      setEditError("این پیام رسانه‌ای برای حذف ندارد.")
      return false
    }
    const keepingExisting = input.mediaAction === "keep" && Boolean(target.media && target.media.type !== "webpage")
    const payload: ChannelMessageEditPayload = {
      text: input.text,
      entities: [],
      ...(keepingExisting
        ? { keep_media: true as const }
        : input.mediaAction === "replace" && input.mediaKind
          ? { media: { kind: input.mediaKind } }
          : {}),
      options: { disable_web_page_preview: false },
    }
    const fingerprint = JSON.stringify([
      target.id,
      payload,
      input.file?.name,
      input.file?.size,
      input.file?.lastModified,
    ])
    if (!editIntent.current || editIntent.current.fingerprint !== fingerprint) {
      editIntent.current = { fingerprint, key: newIntentKey() }
    }
    const token = getToken()
    setEditBusy(true)
    setEditError(null)
    const submitController = new AbortController()
    controllers.current.add(submitController)
    let acceptedByServer = false
    try {
      const accepted: EditAccepted = await editChannelMessage(
        client,
        channel.id,
        target.id,
        payload,
        editIntent.current.key,
        { token, file: input.mediaAction === "replace" ? input.file ?? undefined : undefined, signal: submitController.signal }
      )
      if (identityRef.current !== startedFor) return false
      acceptedByServer = true
      if (
        !accepted?.operation_id ||
        accepted.target?.client_key !== client ||
        accepted.target?.channel_id !== channel.id ||
        accepted.target?.message_id !== target.id
      ) {
        setEditUnknown(true)
        setEditError("پاسخ پذیرش مقصد و پیام ویرایش را تأیید نکرد؛ نتیجه نامشخص است.")
        setEditBusy(false)
        return false
      }
      setEditStatus(accepted.status)
      const controller = new AbortController()
      controllers.current.add(controller)
      let operation: EditOperation | null
      try {
        operation = await pollOperation<EditOperation>(
          (signal) => getEditOperation(client, channel.id, target.id, accepted.operation_id, { token, signal }),
          controller.signal,
          (status) => !["queued", "processing", "retrying"].includes(status),
          (current) => {
            if (identityRef.current === startedFor) setEditStatus(current.status)
          }
        )
      } finally {
        controllers.current.delete(controller)
      }
      if (identityRef.current !== startedFor || controller.signal.aborted) return false
      const status = operation && ["edited", "failed", "unknown"].includes(operation.status)
        ? operation.status
        : "unknown"
      if (status === "edited") {
        let fresh: ChannelMessage | null = null
        for (let attempt = 0; attempt < 5 && !fresh; attempt++) {
          if (identityRef.current !== startedFor) return false
          try {
            fresh = await refreshLoaded(target.id)
          } catch {
            // Telegram can confirm the write before the history endpoint catches up.
          }
          if (!fresh && attempt < 4) await waitForPoll(submitController.signal, 500 + attempt * 400)
        }
        if (!fresh) {
          setEditBusy(false)
          setEditUnknown(true)
          setEditError("ویرایش تأیید شد، اما نسخهٔ تازهٔ پیام هنوز در تاریخچه پیدا نشد.")
          return false
        }
        replaceMessage(fresh)
        setEditing(null)
        setEditingIdentity(null)
        setEditBusy(false)
        setEditUnknown(false)
        editIntent.current = null
        return true
      } else if (status === "failed") {
        setEditBusy(false)
        editIntent.current = null
        setEditError(operation?.error?.message ?? "ویرایش پیام ناموفق بود.")
        return false
      } else {
        setEditBusy(false)
        setEditUnknown(true)
        setEditError(operation?.error?.message ?? statusText("unknown", "edit"))
        return false
      }
    } catch (error) {
      if (identityRef.current === startedFor) {
        setEditBusy(false)
        if (acceptedByServer) {
          setEditUnknown(true)
          setEditError("وضعیت ویرایش قابل تأیید نیست؛ برای جلوگیری از تغییر تکراری، دوباره ارسال نکنید.")
        } else {
          if (error instanceof ApiError && error.isForbidden) revoke("edit")
          setEditError(errorMessage(error, "ویرایش پیام ناموفق بود."))
        }
      }
      return false
    } finally {
      controllers.current.delete(submitController)
    }
  }

  const remove = async (message: ChannelMessage) => {
    const startedFor = identity
    if (!deletable) {
      setDeleteErrors((current) => ({ ...current, [message.id]: "مجوز حذف پیام در دسترس نیست." }))
      return
    }
    setDeletingID(message.id)
    setDeleteErrors((current) => ({ ...current, [message.id]: "" }))
    const controller = new AbortController()
    controllers.current.add(controller)
    try {
      const result = await deleteChannelMessage(client, channel.id, message.id, { token: getToken(), signal: controller.signal })
      if (identityRef.current !== startedFor) return
      if (
        result?.deleted !== true ||
        result.connection?.client_key !== client ||
        result.channel_id !== channel.id ||
        result.message_id !== message.id
      ) {
        throw new Error("تلگرام حذف این پیام را تأیید نکرد.")
      }
      removeMessage(message.id)
      if (editing?.id === message.id) setEditing(null)
      setNotice("حذف پیام از سوی تلگرام تأیید شد.")
    } catch (error) {
      if (identityRef.current === startedFor) {
        if (error instanceof ApiError && error.isForbidden) revoke("delete")
        setDeleteErrors((current) => ({ ...current, [message.id]: errorMessage(error, "حذف پیام ناموفق بود.") }))
      }
    } finally {
      controllers.current.delete(controller)
      if (identityRef.current === startedFor) setDeletingID(null)
    }
  }

  return (
    <section
      data-slot="channel-messages-pane"
      aria-label={`پیام‌های ${channel.title}`}
      className="flex min-h-0 min-w-0 flex-1 flex-col"
    >
      <header
        data-slot="channel-messages-header"
        className="flex shrink-0 items-center gap-3 border-b px-3 py-2.5"
      >
        {onBack && (
          <Button
            variant="ghost"
            size="icon-sm"
            onClick={onBack}
            aria-label="بازگشت به فهرست کانال‌ها"
            className="lg:hidden"
          >
            <ArrowRightIcon />
          </Button>
        )}
        <ChannelAvatar id={channelId} title={channel.title} className="size-9 shrink-0" />
        <div className="flex min-w-0 flex-col">
          <h1 className="truncate text-sm font-semibold">{channel.title}</h1>
          <span className="text-muted-foreground truncate text-xs">
            {channel.username ? `@${channel.username}` : `#${faDigits(channelId)}`}
          </span>
        </div>
      </header>

      <div className="relative min-h-0 flex-1">
        <div ref={scroller} data-slot="message-pane-scroll-region" className="h-full overflow-y-auto overscroll-contain">
          <div
            ref={content}
            data-slot="message-timeline-content"
            className="mx-auto flex w-full max-w-3xl flex-col gap-3 px-3 py-4"
            style={{ paddingBottom: `${messagePaneClearance(footerHeight)}px` }}
          >
            <div ref={topSentinel} aria-hidden="true" className="h-px" />

            {!canRead ? (
              <p role="status" className="py-8 text-center text-sm text-muted-foreground">
                مجوز خواندن پیام‌های این کانال در دسترس نیست.
              </p>
            ) : loadingInitial ? (
              <TimelineSkeleton />
            ) : state.messages.length === 0 && state.olderError ? (
              <ErrorState
                title="بارگذاری پیام‌ها ناموفق بود"
                message={state.olderError}
                onRetry={() => retry("older")}
              />
            ) : state.messages.length === 0 ? (
              <EmptyState
                title="این کانال پیامی ندارد"
                description="تلگرام برای این کانال هیچ پیامی برنگرداند."
              />
            ) : (
              <>
                <HistoryTop
                  loading={loadingOlder}
                  hasOlder={state.hasOlder}
                  error={state.olderError}
                  stalled={state.stalled}
                  gap={state.gap}
                  onRetry={() => retry("older")}
                  onAsk={requestOlder}
                  onRecover={requestNewer}
                />

                <ol className="flex flex-col gap-3">
                  {groupMessageAlbums(state.messages).map((run, index, runs) => {
                    const message = run.messages[0]
                    const previous = runs[index - 1]?.messages.at(-1)
                    const newDay =
                      !previous || dayOf(previous.date) !== dayOf(message.date)
                    return (
                      <React.Fragment key={run.key}>
                        {newDay && <DaySeparator date={message.date} />}
                        <li data-message-id={message.id} data-album-id={run.kind === "album" ? run.groupedID : undefined}>
                          {run.kind === "album" && run.messages.length > 1 ? (
                            <ChannelMessageAlbum messages={run.messages} onPublish={() => setPublishing({ message, albumMessages: run.messages })} />
                          ) : (
                          <ChannelMessageItem
                            message={message}
                            client={client}
                            onPublish={() => setPublishing({ message })}
                            canEdit={editable && !editBusy && !editUnknown}
                            canDelete={deletable}
                            deleting={deletingID === message.id}
                            deleteError={deleteErrors[message.id] || null}
                            onEdit={() => beginEdit(message)}
                            onDelete={() => void remove(message)}
                            editing={activeEditing?.id === message.id}
                            editStatus={activeEditing?.id === message.id ? editStatus : null}
                          />
                          )}
                        </li>
                      </React.Fragment>
                    )
                  })}
                </ol>

                <HistoryBottom loading={loadingNewer} gap={state.gap} />
                <div ref={bottomSentinel} aria-hidden="true" className="h-px" />
              </>
            )}

            {pendingSends.some((pending) => pending.identity === identity) && (
              <ol className="flex flex-col gap-3" aria-label="وضعیت پیام‌های در حال ارسال">
                {pendingSends.filter((pending) => pending.identity === identity).map((pending) => (
                  <li key={pending.localID} data-pending-send-id={pending.localID}>
                    <PendingMessage pending={pending} onRestore={() => setRecoveredDraft({
                      key: newIntentKey(), text: pending.text, file: pending.file, mediaKind: pending.mediaKind,
                    })} />
                  </li>
                ))}
              </ol>
            )}
          </div>
        </div>

        {showJump && (
          <Button
            data-slot="jump-to-latest"
            variant="secondary"
            size="sm"
            onClick={jumpToLatest}
            style={{ bottom: `${jumpButtonOffset(footerHeight)}px` }}
            className="absolute left-1/2 z-10 -translate-x-1/2 gap-1.5 rounded-full px-3 shadow-none ring-2 ring-background"
          >
            <ArrowDownIcon />
            آخرین پیام‌ها
          </Button>
        )}
        {showFloatingFooter && (
          <div
            ref={footer}
            data-slot="floating-chat-footer"
            className="pointer-events-none absolute bottom-0 inset-x-0 z-20"
            style={{ paddingBottom: "env(safe-area-inset-bottom)" }}
          >
            <div className="pointer-events-auto mx-auto max-w-3xl md:py-3">
              {/* {notice && (
                <p role="status" aria-live="polite" className="pb-1 text-center text-xs text-muted-foreground">
                  {notice}
                </p>
              )} */}
              {(writable || (activeEditing && editable)) && <ChannelMessageComposer
            key={identity}
            writable={activeEditing ? editable : writable}
            submitting={submitting}
            error={activeEditing ? editError : sendError}
            onSubmit={send}
            onAccepted={() => { sendIntent.current = null }}
            recoveredDraft={recoveredDraft}
            mode={activeEditing ? { type: "edit", messageId: activeEditing.id, originalMessage: activeEditing } : { type: "send" }}
            onSaveEdit={saveEdit}
            onCancelEdit={() => {
              setEditing(null)
              setEditingIdentity(null)
              setEditError(null)
              setEditStatus(null)
              setEditUnknown(false)
            }}
            editBusy={editBusy}
            editSaveDisabled={!editable || editUnknown}
          />}
            </div>
          </div>
        )}
      </div>
      {!activeEditing && !writable && (
        <p className="shrink-0 border-t px-3 py-2 text-center text-xs text-muted-foreground">
          {identityValid ? "امکان ارسال پیام برای این کانال فعال نیست." : "اطلاعات مقصد کانال در دسترس نیست."}
        </p>
      )}
      {publishing && (
        <PublishDialog
          open
          onOpenChange={(next) => !next && setPublishing(null)}
          localChannelId={channelId}
          message={publishing.message}
          albumMessages={publishing.albumMessages}
          onSuccess={() => setPublishing(null)}
        />
      )}
    </section>
  )
}

// HistoryTop is what sits above the oldest message in hand: the control that
// asks for more, and the honest answer when there is no more, when the walk
// stalled, or when the history below cannot be trusted.
function HistoryTop({
  loading,
  hasOlder,
  error,
  stalled,
  gap,
  onRetry,
  onAsk,
  onRecover,
}: {
  loading: boolean
  hasOlder: boolean
  error: string | null
  stalled: boolean
  gap: boolean
  onRetry: () => void
  onAsk: () => void
  onRecover: () => void
}) {
  if (loading) {
    return (
      <p role="status" className="py-1 text-center text-xs text-muted-foreground">
        در حال بارگذاری پیام‌های قدیمی‌تر…
      </p>
    )
  }
  if (error) {
    return (
      <div
        role="alert"
        className="flex flex-col items-center gap-2 rounded-xl border border-dashed bg-muted/40 px-4 py-3 text-center"
      >
        <p className="text-xs text-destructive">{error}</p>
        <Button variant="outline" size="sm" onClick={onRetry}>
          <RefreshCwIcon />
          تلاش دوباره
        </Button>
      </div>
    )
  }
  if (gap) {
    // A hole in the history: the timeline says so rather than drawing a
    // continuous conversation that is not continuous.
    return (
      <div className="flex flex-col items-center gap-2 rounded-xl border border-dashed bg-muted/40 px-4 py-3 text-center">
        <AlertCircleIcon className="size-5 text-muted-foreground" aria-hidden="true" />
        <p className="max-w-md text-xs leading-6 text-muted-foreground">
          بخشی از تاریخچهٔ این کانال بارگذاری نشده است؛ پیام‌های این فهرست به‌هم
          پیوسته نیستند.
        </p>
        <Button variant="outline" size="sm" onClick={onRecover}>
          بارگذاری از ابتدا
        </Button>
      </div>
    )
  }
  if (stalled) {
    return (
      <div className="flex flex-col items-center gap-2 rounded-xl border border-dashed bg-muted/40 px-4 py-3 text-center">
        <p className="text-xs leading-6 text-muted-foreground">
          بارگذاری پیام‌های قدیمی‌تر ادامه پیدا نکرد.
        </p>
        <Button variant="outline" size="sm" onClick={onRetry}>
          <RefreshCwIcon />
          تلاش دوباره
        </Button>
      </div>
    )
  }
  if (!hasOlder) {
    return (
      <p className="py-1 text-center text-xs text-muted-foreground/80">
        به ابتدای تاریخچهٔ کانال رسیدید.
      </p>
    )
  }
  // The sentinel above is what asks; this is only a way to ask again by hand.
  return (
    <div className="flex justify-center py-1">
      <Button variant="ghost" size="sm" onClick={onAsk}>
        بارگذاری پیام‌های قدیمی‌تر
      </Button>
    </div>
  )
}

// HistoryBottom is the small indicator for a newer range being fetched.
function HistoryBottom({ loading, gap }: { loading: boolean; gap: boolean }) {
  if (loading) {
    return (
      <p role="status" className="py-1 text-center text-xs text-muted-foreground">
        در حال بارگذاری پیام‌های تازه‌تر…
      </p>
    )
  }
  if (gap) {
    return (
      <p className="py-1 text-center text-xs text-muted-foreground">
        پیام‌های تازه‌تری هست که هنوز بارگذاری نشده‌اند.
      </p>
    )
  }
  return null
}

// DaySeparator is the one place a date is written out: the clock time in each
// message's footer is enough on every other row.
function DaySeparator({ date }: { date: string }) {
  return (
    <li className="flex items-center gap-3 py-1" aria-hidden="true">
      <span className="h-px flex-1 bg-border" />
      <time dateTime={date} className="text-xs text-muted-foreground">
        {dayOf(date)}
      </time>
      <span className="h-px flex-1 bg-border" />
    </li>
  )
}

function dayOf(value: string): string {
  const date = new Date(value)
  return Number.isNaN(date.getTime())
    ? value
    : date.toLocaleDateString("fa-IR-u-ca-persian", { dateStyle: "medium" })
}

function TimelineSkeleton() {
  return (
    <div
      className="flex flex-col gap-3"
      aria-busy="true"
      aria-label="در حال بارگذاری پیام‌ها…"
    >
      {Array.from({ length: 5 }).map((_, index) => (
        <div
          key={index}
          className={cn(
            "flex flex-col gap-2 rounded-2xl bg-muted px-3.5 py-2.5",
            index % 2 === 0 ? "w-2/3" : "w-1/2"
          )}
        >
          <Skeleton className="h-4 w-4/5" />
          <Skeleton className="h-4 w-2/3" />
          <Skeleton className="mt-1 h-3 w-24" />
        </div>
      ))}
    </div>
  )
}

export function PendingMessage({ pending, onRestore }: { pending: PendingSend; onRestore: () => void }) {
  const active = ["accepting", "queued", "processing", "retrying", "sent"].includes(pending.status)
  const label = pending.status === "accepting" ? "در حال ارسال به سرور…" : statusText(pending.status, "send")
  return (
    <div dir="rtl" className="flex w-full justify-end">
      <div
        role={active ? "status" : "group"}
        aria-label={active ? label : pending.error || label}
        className={cn(
          "flex w-fit max-w-[85%] items-center gap-2 rounded-2xl bg-muted px-3.5 py-2.5 text-xs text-muted-foreground",
          pending.status === "failed" && "text-destructive"
        )}
      >
        {active
          ? pending.status === "queued"
            ? <Clock3Icon className="size-4 shrink-0" aria-hidden="true" />
            : <LoaderCircleIcon className="size-4 shrink-0 motion-safe:animate-spin motion-reduce:animate-none" aria-hidden="true" />
          : <AlertCircleIcon className="size-4 shrink-0" aria-hidden="true" />}
        <span>{pending.status === "failed" ? "ارسال انجام نشد" : pending.error || label}</span>
        {pending.status === "failed" && (
          <button type="button" className="shrink-0 underline underline-offset-2" onClick={onRestore}>
            بازیابی پیش‌نویس
          </button>
        )}
      </div>
    </div>
  )
}
