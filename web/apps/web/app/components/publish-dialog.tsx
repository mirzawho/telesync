import * as React from "react"

import { Button } from "@workspace/ui/components/button"
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@workspace/ui/components/dialog"
import { Input } from "@workspace/ui/components/input"
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@workspace/ui/components/tabs"

import { ErrorState, LoadingState } from "./states"
import { compose, parse, type ComposedContent, type Entity } from "../lib/content"
import { apiRequest, ApiError } from "../lib/api"
import { getToken } from "../lib/session"

type Destination =
  | { kind: "telegram"; clientKey: string; channelId: string; title: string }
  | { kind: "wordpress"; siteId: number; title: string; domain: string }

interface TelegramServerRow {
  key: string
  type: string
  state: string
  authorized: boolean | null
}

interface ChannelRow {
  id: number
  title: string
  username?: string
  type: string
  permissions: { new: boolean }
}

// asArray keeps a listing response safe to map. The API answers
// {"data":{"servers":[…]}} / {"data":{"channels":[…]}} / {"data":{"sites":[…]}}
// and loading routes that as objects, so a map call must never receive one.
function asArray<T>(value: unknown): T[] {
  return Array.isArray(value) ? (value as T[]) : []
}

interface SiteRow {
  id: number
  title: string
  domain: string
  site_icon_url: string | null
}

interface MessageLike {
  id: number
  text?: string
  entities?: Entity[]
  media?: { type: string; label?: string; url?: string; mime_type?: string } | null
}

export interface PublishDialogProps {
  open: boolean
  onOpenChange: (open: boolean) => void
  localChannelId: number
  message: MessageLike
  onSuccess: (summary: string) => void
}

// The publishing dialog: choose a destination, then prepare the content and
// send it. The source message is read-only throughout — every edit lands in
// the outgoing state, never back on the message.
export function PublishDialog({
  open,
  onOpenChange,
  localChannelId,
  message,
  onSuccess,
}: PublishDialogProps) {
  const [step, setStep] = React.useState<1 | 2>(1)
  const [destinationTab, setDestinationTab] = React.useState<"telegram" | "wordpress">("telegram")

  const [servers, setServers] = React.useState<TelegramServerRow[]>([])
  const [channels, setChannels] = React.useState<ChannelRow[]>([])
  const [sites, setSites] = React.useState<SiteRow[]>([])
  const [loading, setLoading] = React.useState(false)
  const [loadError, setLoadError] = React.useState<string | null>(null)

  const [clientKey, setClientKey] = React.useState("")
  const [channel, setChannel] = React.useState<ChannelRow | null>(null)
  const [site, setSite] = React.useState<SiteRow | null>(null)

  const parsed = React.useMemo(() => parse(message.text ?? "", message.entities ?? []), [message])
  const [main, setMain] = React.useState("")
  const [hashtags, setHashtags] = React.useState<string[]>([])
  const [mentions, setMentions] = React.useState<string[]>([])
  const [entities, setEntities] = React.useState<Entity[]>([])
  const [title, setTitle] = React.useState("")
  const [keepMedia, setKeepMedia] = React.useState(true)

  const [publishing, setPublishing] = React.useState(false)
  const [actionError, setActionError] = React.useState<{ error?: { message?: string } } | null>(null)

  // Opening the dialog seeds the editor once; going back and forth never
  // loses what was typed.
  const seeded = React.useRef(false)
  React.useEffect(() => {
    if (!open) {
      seeded.current = false
      return
    }
    if (seeded.current) return
    seeded.current = true
    setStep(1)
    setMain(parsed.main)
    setHashtags(parsed.hashtags)
    setMentions(parsed.mentions)
    setEntities(parsed.entities)
    const firstLine = (message.text ?? "").split("\n").find((l) => l.trim().length > 0) ?? ""
    setTitle(firstLine.replace(/^[#@\s]+/, "").slice(0, 120))
    setKeepMedia(Boolean(message.media))
  }, [open, parsed, message])

  // Destination lists are loaded when the dialog opens. A connection change
  // drops the channel that belonged to the previous one.
  React.useEffect(() => {
    if (!open) return
    let cancelled = false
    setLoading(true)
    setLoadError(null)
    const token = getToken()
    Promise.all([
      apiRequest<{ servers?: TelegramServerRow[] }>("GET", "/telegram", { token }),
      apiRequest<{ sites?: SiteRow[] }>("GET", "/local/wordpress", { token }),
    ])
      .then(([serverRes, siteRes]) => {
        if (cancelled) return
        setServers(asArray<TelegramServerRow>(serverRes.data?.servers))
        setSites(asArray<SiteRow>(siteRes.data?.sites))
      })
      .catch((err) => {
        if (cancelled) return
        setLoadError(err instanceof ApiError ? err.message : "بارگذاری مقصد ناموفق بود.")
      })
      .finally(() => !cancelled && setLoading(false))
    return () => {
      cancelled = true
    }
  }, [open])

  React.useEffect(() => {
    setChannel(null)
    if (!clientKey) {
      setChannels([])
      return
    }
    let cancelled = false
    setLoading(true)
    apiRequest<{ channels?: ChannelRow[] }>("GET", `/telegram/${clientKey}/channels`, {
      token: getToken(),
      query: { page: 1, per_page: 100, capability: "send" },
    })
      .then((res) => {
        if (cancelled) return
        setChannels(asArray<ChannelRow>(res.data?.channels))
      })
      .catch((err) => {
        if (cancelled) return
        setLoadError(err instanceof ApiError ? err.message : "بارگذاری کانال‌ها ناموفق بود.")
      })
      .finally(() => !cancelled && setLoading(false))
    return () => {
      cancelled = true
    }
  }, [clientKey])

  const composed: ComposedContent = React.useMemo(
    () => compose(main, hashtags, mentions, entities),
    [main, hashtags, mentions, entities],
  )

  const canContinue =
    destinationTab === "telegram"
      ? Boolean(clientKey && channel)
      : Boolean(site)

  const submit = async () => {
    if (publishing) return
    const token = getToken()
    const body =
      destinationTab === "telegram"
        ? {
            source: { local_channel_id: localChannelId, message_id: message.id },
            destination: { channel_id: String(channel!.id) },
            content: { text: composed.text, entities: composed.entities },
            media: { mode: keepMedia && message.media ? "preserve_source" : "omit" },
            options: { silent: false, disable_web_page_preview: false },
          }
        : {
            source: { local_channel_id: localChannelId, message_id: message.id },
            post: {
              title,
              content: composed.text,
              status: "publish",
              tags: hashtags,
            },
            media: { mode: keepMedia && message.media ? "preserve_source" : "omit" },
            options: { comment_status: "open" },
          }
    const path =
      destinationTab === "telegram"
        ? `/telegram/${clientKey}/messages`
        : `/local/wordpress/${site!.id}/posts`
    setPublishing(true)
    setActionError(null)
    try {
      await apiRequest("POST", path, {
        token,
        body,
        headers: {
          "Idempotency-Key": `pub-${clientKey || String(site!.id)}-${localChannelId}-${message.id}-${Date.now()}`,
        },
      })
      onSuccess("پیام با موفقیت ارسال شد.")
      onOpenChange(false)
    } catch (err) {
      const message =
        err instanceof ApiError ? err.message : "ارسال ناموفق بود."
      setActionError({ error: { message } })
    } finally {
      setPublishing(false)
    }
  }

  return (
    <Dialog open={open} onOpenChange={(next: boolean) => !next && !publishing && onOpenChange(false)}>
      <DialogContent className="max-h-[90vh] overflow-y-auto sm:max-w-2xl">
        <DialogHeader>
          <DialogTitle>ارسال این پیام</DialogTitle>
          <DialogDescription>
            مقصد را انتخاب کنید، محتوا را ویرایش کنید و ارسال کنید. پیام اصلی تغییری نمی‌کند.
          </DialogDescription>
        </DialogHeader>

        {step === 1 ? (
          <div className="flex flex-col gap-4">
            <Tabs
              value={destinationTab}
              onValueChange={(v: string | number | null) => setDestinationTab((v ?? "telegram") as "telegram" | "wordpress")}
            >
              <TabsList>
                <TabsTrigger value="telegram">تلگرام</TabsTrigger>
                <TabsTrigger value="wordpress">وردپرس</TabsTrigger>
              </TabsList>
              <TabsContent value="telegram" className="flex flex-col gap-3">
                <label className="flex flex-col gap-1 text-sm">
                  <span>اتصال تلگرام</span>
                  <select
                    className="rounded-md border bg-background px-3 py-2"
                    value={clientKey}
                    onChange={(e: React.ChangeEvent<HTMLSelectElement>) => setClientKey(e.target.value)}
                  >
                    <option value="">— اتصال را انتخاب کنید —</option>
                    {servers.map((s) => (
                      <option key={s.key} value={s.key}>
                        {s.key} ({s.type})
                      </option>
                    ))}
                  </select>
                </label>
                {loading && <LoadingState />}
                {loadError && <ErrorState message={loadError} onRetry={() => setClientKey(clientKey)} />}
                {clientKey && !loading && (
                  <label className="flex flex-col gap-1 text-sm">
                    <span>کانال مقصد</span>
                    <select
                      className="rounded-md border bg-background px-3 py-2"
                      value={channel ? String(channel.id) : ""}
                      onChange={(e: React.ChangeEvent<HTMLSelectElement>) =>
                        setChannel(channels.find((c) => String(c.id) === e.target.value) ?? null)
                      }
                    >
                      <option value="">— کانال را انتخاب کنید —</option>
                      {channels
                        .filter((c) => c.permissions?.new)
                        .map((c) => (
                          <option key={c.id} value={c.id}>
                            {c.title}
                            {c.username ? ` (@${c.username})` : ""}
                          </option>
                        ))}
                    </select>
                    {channels.filter((c) => c.permissions?.new).length === 0 && (
                      <span className="text-xs text-muted-foreground">
                        هیچ کانالی برای ارسال پیام در دسترس نیست.
                      </span>
                    )}
                  </label>
                )}
              </TabsContent>
              <TabsContent value="wordpress" className="flex flex-col gap-3">
                <label className="flex flex-col gap-1 text-sm">
                  <span>وب‌سایت</span>
                  <select
                    className="rounded-md border bg-background px-3 py-2"
                    value={site?.id ?? ""}
                    onChange={(e: React.ChangeEvent<HTMLSelectElement>) =>
                      setSite(sites.find((s) => String(s.id) === e.target.value) ?? null)
                    }
                  >
                    <option value="">— سایت را انتخاب کنید —</option>
                    {sites.map((s) => (
                      <option key={s.id} value={s.id}>
                        {s.title} — {s.domain}
                      </option>
                    ))}
                  </select>
                  {sites.length === 0 && (
                    <span className="text-xs text-muted-foreground">
                      هیچ سایت وردپرسی ثبت نشده است.
                    </span>
                  )}
                </label>
              </TabsContent>
            </Tabs>
          </div>
        ) : (
          <div className="flex flex-col gap-4">
            {destinationTab === "wordpress" && (
              <label className="flex flex-col gap-1 text-sm">
                <span>عنوان مقاله</span>
                <Input value={title} onChange={(e: React.ChangeEvent<HTMLInputElement>) => setTitle(e.target.value)} required />
              </label>
            )}
            <label className="flex flex-col gap-1 text-sm">
              <span>متن اصلی</span>
              <textarea
                className="min-h-28 rounded-md border bg-background px-3 py-2"
                value={main}
                onChange={(e: React.ChangeEvent<HTMLTextAreaElement>) => setMain(e.target.value)}
              />
            </label>
            <ChipEditor
              label="هشتگ‌ها"
              hint="بدون #"
              values={hashtags}
              onChange={setHashtags}
              prefix="#"
            />
            <ChipEditor
              label="منشن‌ها"
              hint={
                destinationTab === "wordpress"
                  ? "در وردپرس منشن‌های تلگرام ارسال نمی‌شوند"
                  : "با @"
              }
              values={destinationTab === "wordpress" ? [] : mentions}
              onChange={setMentions}
              prefix="@"
              readOnly={destinationTab === "wordpress"}
            />
            {message.media && (
              <label className="flex items-center gap-2 text-sm">
                <input
                  type="checkbox"
                  checked={keepMedia}
                  onChange={(e: React.ChangeEvent<HTMLInputElement>) => setKeepMedia(e.target.checked)}
                />
                <span>
                  ارسال پیوست ({message.media.label ?? message.media.type}) به همراه پیام
                </span>
              </label>
            )}
            <div className="rounded-lg border bg-muted/40 p-3 text-sm">
              <div className="mb-2 font-medium">پیش‌نمایش محتوای ارسالی</div>
              {destinationTab === "wordpress" && (
                <div className="mb-1 font-semibold">{title || "— عنوان —"}</div>
              )}
              <pre className="whitespace-pre-wrap font-sans text-xs">{composed.text}</pre>
              {hashtags.length > 0 && (
                <div className="mt-2 text-xs text-muted-foreground">
                  برچسب‌ها: {hashtags.map((h) => `#${h}`).join(" ")}
                </div>
              )}
              {keepMedia && message.media && (
                <div className="mt-2 text-xs text-muted-foreground">
                  پیوست: {message.media.label ?? message.media.type}
                </div>
              )}
            </div>
          </div>
        )}

        {actionError?.error?.message && (
          <p role="alert" className="rounded-lg bg-destructive/10 px-3 py-2 text-sm text-destructive">
            {actionError.error.message}
          </p>
        )}

        <DialogFooter>
          {step === 1 ? (
            <>
              <Button variant="outline" onClick={() => onOpenChange(false)} disabled={publishing}>
                انصراف
              </Button>
              <Button disabled={!canContinue || publishing} onClick={() => setStep(2)}>
                ادامه
              </Button>
            </>
          ) : (
            <>
              <Button variant="outline" onClick={() => setStep(1)} disabled={publishing}>
                بازگشت
              </Button>
              <Button
                onClick={submit}
                disabled={publishing || (destinationTab === "wordpress" && !title.trim())}
              >
                {publishing ? "در حال ارسال…" : "ارسال پیام"}
              </Button>
            </>
          )}
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}

// ChipEditor is the editable collection of hashtags or mentions.
function ChipEditor({
  label,
  hint,
  values,
  onChange,
  prefix,
  readOnly,
}: {
  label: string
  hint: string
  values: string[]
  onChange: (next: string[]) => void
  prefix: string
  readOnly?: boolean
}) {
  const [draft, setDraft] = React.useState("")
  const add = () => {
    const value = draft.trim().replace(/^[@#]/, "")
    if (!value) return
    if (!values.includes(value)) onChange([...values, value])
    setDraft("")
  }
  return (
    <div className="flex flex-col gap-1 text-sm">
      <span>
        {label} <span className="text-xs text-muted-foreground">({hint})</span>
      </span>
      <div className="flex flex-wrap gap-2">
        {values.map((v, i) => (
          <span
            key={`${v}-${i}`}
            className="inline-flex items-center gap-1 rounded-full border px-2 py-0.5"
            dir="ltr"
          >
            {prefix}
            {v}
            {!readOnly && (
              <button
                type="button"
                aria-label={`حذف ${prefix}${v}`}
                className="text-muted-foreground hover:text-destructive"
                onClick={() => onChange(values.filter((_, j) => j !== i))}
              >
                ×
              </button>
            )}
          </span>
        ))}
      </div>
      {!readOnly && (
        <div className="flex gap-2">
          <Input
            value={draft}
            onChange={(e: React.ChangeEvent<HTMLInputElement>) => setDraft(e.target.value)}
            onKeyDown={(e: React.KeyboardEvent<HTMLInputElement>) => {
              if (e.key === "Enter") {
                e.preventDefault()
                add()
              }
            }}
            placeholder={prefix === "#" ? "#technology" : "@channel"}
            dir="ltr"
          />
          <Button type="button" variant="outline" onClick={add}>
            افزودن
          </Button>
        </div>
      )}
    </div>
  )
}


