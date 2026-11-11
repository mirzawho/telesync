import * as React from "react"
import { useNavigate } from "react-router"

import { CreateChannelForm, type CreateChannelDraft, type UsernameAvailability } from "@/components/channel-explorer/create-channel-form"
import type { TelegramServer } from "@/lib/types"
import { ApiError, apiRequest } from "@/lib/api"
import { Button } from "@workspace/ui/components/button"
import { Dialog, DialogContent, DialogDescription, DialogHeader, DialogTitle } from "@workspace/ui/components/dialog"
import { useToast } from "@workspace/ui/components/toast"

export interface ChannelCreationResponse {
  local_channel_id: number | null
  remote_channel_id: string
  channel_created: boolean
  username_assigned: boolean
  avatar_updated: boolean
  locally_registered: boolean
  status: string
  error_code?: string
  error_message?: string
  replayed?: boolean
}

export function CreateChannelDialog({
  open, onOpenChange, token, servers, initialClientKey, canCreate = true, onCreated,
}: {
  open: boolean
  onOpenChange: (open: boolean) => void
  token: string
  servers: TelegramServer[]
  initialClientKey?: string
  canCreate?: boolean
  onCreated: (result: ChannelCreationResponse, clientKey: string) => void
}) {
  const navigate = useNavigate()
  const { toast } = useToast()
  const [pending, setPending] = React.useState(false)
  const [error, setError] = React.useState<string | null>(null)
  const [result, setResult] = React.useState<ChannelCreationResponse | null>(null)
  const [resultKey, setResultKey] = React.useState("")
  const [dirty, setDirty] = React.useState(false)
  const [formSession, setFormSession] = React.useState(0)
  const [idempotencyKey, setIdempotencyKey] = React.useState(() => crypto.randomUUID())

  const checkUsername = React.useCallback(async (clientKey: string, username: string, signal: AbortSignal): Promise<UsernameAvailability> => {
    const { data } = await apiRequest<{ username: string; status: string }>("GET", `/telegram/${encodeURIComponent(clientKey)}/channels/username-availability`, { token, query: { username }, signal })
    return { state: data.status === "occupied" ? "claimed" : data.status === "unclaimed" ? "unclaimed" : "unknown" }
  }, [token])

  const close = () => {
    if (pending) return
    if (dirty && !window.confirm("اطلاعات واردشده ذخیره نشده است. از بستن پنجره و دورریختن آن‌ها مطمئن هستید؟")) return
    setDirty(false)
    setFormSession((session) => session + 1)
    setError(null)
    onOpenChange(false)
  }

  const submit = async (draft: CreateChannelDraft) => {
    if (pending) return
    setPending(true)
    setError(null)
    const body = new FormData()
    body.append("request", JSON.stringify({
      title: draft.title,
      description: draft.description,
      visibility: draft.visibility,
      ...(draft.username ? { username: draft.username } : {}),
      ...(draft.image ? { avatar: { kind: "photo", name: draft.image.name, mime_type: draft.image.type } } : {}),
    }))
    if (draft.image) body.append("media", draft.image, draft.image.name)
    try {
      const { data } = await apiRequest<ChannelCreationResponse>("POST", `/telegram/${encodeURIComponent(draft.clientKey)}/channels`, {
        token, headers: { "Idempotency-Key": idempotencyKey }, body,
      })
      setResult(data)
      setResultKey(draft.clientKey)
      if (data.status.toLowerCase().includes("partial") || !data.locally_registered) {
        toast({ variant: "warning", title: "کانال ساخته شد؛ بخشی از کار کامل نشد.", description: data.error_message })
        onCreated(data, draft.clientKey)
      } else {
        toast({ variant: "success", title: "کانال ساخته و ثبت شد." })
        onOpenChange(false)
        setDirty(false)
        setFormSession((session) => session + 1)
        onCreated(data, draft.clientKey)
      }
    } catch (caught) {
      setError(caught instanceof ApiError ? caught.message : "ساخت کانال کامل نشد. برای جلوگیری از ایجاد تکراری، وضعیت درخواست را بررسی کنید.")
    } finally {
      setPending(false)
    }
  }

  return <Dialog open={open} onOpenChange={(next) => { if (next) onOpenChange(true); else close() }}>
    <DialogContent dir="rtl" className="flex max-h-[90dvh] w-[calc(100%-1rem)] max-w-[600px] flex-col overflow-hidden rounded-2xl p-0 sm:w-[calc(100%-2rem)]">
      <DialogHeader className="shrink-0 px-5 pb-0 pt-5 text-right sm:px-6 sm:pt-6">
        <DialogTitle>ساخت کانال</DialogTitle>
        <DialogDescription>یک کانال جدید در سرور تلگرام انتخاب‌شده ایجاد کنید.</DialogDescription>
      </DialogHeader>
      {!canCreate ? <div role="alert" className="px-5 py-4 text-sm text-destructive sm:px-6">مجوز ساخت کانال را ندارید.</div> : result ? <div className="min-h-0 space-y-4 overflow-y-auto px-5 py-4 sm:px-6">
        <div role="status" className="space-y-2">
          <h3 className="font-semibold">{result.status.toLowerCase().includes("partial") ? "کانال ساخته شد؛ تنظیمات ناقص است" : "کانال ساخته شد"}</h3>
          <p className="text-sm text-muted-foreground">شناسه تلگرام: <bdi dir="ltr">{result.remote_channel_id}</bdi></p>
          {result.error_message && <p className="text-sm text-destructive">{result.error_message}</p>}
        </div>
        <div className="flex flex-row-reverse flex-wrap gap-2">
          {result.locally_registered && result.local_channel_id && <Button onClick={() => navigate(`/channels/${result.local_channel_id}/messages?server=${encodeURIComponent(resultKey)}`)}>رفتن به کانال</Button>}
          <Button variant="outline" onClick={() => { setResult(null); setIdempotencyKey(crypto.randomUUID()) }}>ساخت کانال دیگر</Button>
          <Button variant="ghost" onClick={close}>بستن</Button>
        </div>
      </div> : <CreateChannelForm
        key={formSession}
        servers={servers}
        initialClientKey={initialClientKey}
        pending={pending}
        error={error}
        onSubmit={submit}
        onCheckUsername={checkUsername}
        onDirtyChange={setDirty}
        onCancel={close}
      />}
    </DialogContent>
  </Dialog>
}
