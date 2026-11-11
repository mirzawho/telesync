import {
  BadgeCheckIcon,
  CopyIcon,
  InfoIcon,
  MegaphoneIcon,
  ShieldAlertIcon,
  Trash2Icon,
  TagsIcon,
  EyeIcon,
} from "lucide-react"
import * as React from "react"

import { ChannelAvatar } from "@/components/channel-avatar"
import type { ChannelDetail } from "@/lib/types"
import { Accordion, AccordionContent, AccordionItem, AccordionTrigger } from "@workspace/ui/components/accordion"
import { Button } from "@workspace/ui/components/button"
import { Dialog, DialogContent, DialogTitle } from "@workspace/ui/components/dialog"
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuTrigger,
} from "@workspace/ui/components/dropdown-menu"
import { useToast } from "@workspace/ui/components/toast"

export interface ChannelInformationDialogProps {
  open: boolean
  clientKey: string
  telegramChannelId: string
  localChannelId: number
  onOpenChange: (open: boolean) => void
  onView: () => void
  onEditCategories: () => void
  onDelete: () => void
  fetchDetail: (clientKey: string, telegramChannelId: string, signal: AbortSignal) => Promise<ChannelDetail>
}

export function channelRequestKey(clientKey: string, telegramChannelId: string) {
  return JSON.stringify([clientKey, telegramChannelId])
}

export function channelMessagePath(localChannelId: number, search: string) {
  return `/channels/${localChannelId}/messages${search ? `?${search}` : ""}`
}

export function currentChannelDetail(
  open: boolean,
  requestKey: string,
  loaded: { key: string; detail: ChannelDetail } | null,
) {
  return open && loaded?.key === requestKey ? loaded.detail : null
}

export async function copyChannelId(
  remoteId: string,
  writeText: (value: string) => Promise<void>,
  report: (copied: boolean) => void,
) {
  try {
    await writeText(remoteId)
    report(true)
  } catch {
    report(false)
  }
}

export function ChannelInformationLoading() {
  return (
    <div aria-busy="true" aria-label="در حال بارگذاری" className="overflow-hidden rounded-2xl">
      <div className="flex flex-col items-center gap-3 bg-muted/60 px-5 py-7 sm:py-9">
        <div className="size-28 animate-pulse rounded-full bg-muted" />
        <div className="h-6 w-2/3 animate-pulse rounded bg-muted" />
        <div className="h-4 w-1/3 animate-pulse rounded bg-muted" />
        <div className="mt-2 grid w-full grid-cols-3 gap-2">
          <div className="h-16 animate-pulse rounded-xl bg-muted" />
          <div className="h-16 animate-pulse rounded-xl bg-muted" />
          <div className="h-16 animate-pulse rounded-xl bg-muted" />
        </div>
      </div>
      <div className="space-y-4 px-5 py-5 sm:px-8">
        <div className="h-4 w-1/3 animate-pulse rounded bg-muted" />
        <div className="h-4 w-full animate-pulse rounded bg-muted" />
        <div className="h-4 w-4/5 animate-pulse rounded bg-muted" />
      </div>
    </div>
  )
}

export function ChannelInformationError({ message, onClose }: { message: string; onClose: () => void }) {
  return <div role="alert" className="flex flex-col gap-3 p-6 text-sm"><p>{message}</p><Button variant="outline" onClick={onClose}>بستن</Button></div>
}

export function ChannelInformationProfile({
  detail,
  localChannelId,
  onCopyId,
  onView,
  onEditCategories,
  onDelete,
}: {
  detail: ChannelDetail
  localChannelId: number
  onCopyId: () => void
  onView?: () => void
  onEditCategories?: () => void
  onDelete?: () => void
}) {
  const actions = [
    ...(detail.permissions?.read && onView
      ? [{ key: "view", label: "مشاهده", icon: EyeIcon, onSelect: onView }]
      : []),
    { key: "copy-id", label: "کپی شناسه", icon: CopyIcon, onSelect: onCopyId },
    ...(onEditCategories || onDelete
      ? [{ key: "more", label: "بیشتر", icon: InfoIcon, onSelect: undefined }]
      : []),
  ]
  const count = detail.participants_count
  const hasCount = typeof count === "number" && Number.isFinite(count) && count >= 0
  const actionColumns = actions.length === 3 ? "grid-cols-3" : actions.length === 2 ? "grid-cols-2" : "grid-cols-1"

  return (
    <div className="min-h-0 flex-1 overflow-y-auto">
      <div className="flex flex-col items-center bg-muted/60 px-5 pb-5 pt-10 text-center sm:px-8 sm:pb-7 sm:pt-12">
        <div className="mb-4 flex size-28 shrink-0 items-center justify-center overflow-hidden rounded-full bg-background text-3xl font-semibold text-muted-foreground shadow-sm">
          {detail.avatar_available ? (
            <ChannelAvatar id={localChannelId} title={detail.title} className="size-28 text-3xl" />
          ) : (
            detail.title.trim().charAt(0) || <MegaphoneIcon className="size-10" aria-hidden="true" />
          )}
        </div>

        <div className="flex max-w-full items-center justify-center gap-1.5">
          <h2 className="break-words text-xl font-semibold sm:text-2xl">{detail.title}</h2>
          {detail.verified && <BadgeCheckIcon role="img" aria-label="تأییدشده" className="size-5 shrink-0 text-primary" />}
        </div>
        {detail.username && <bdi dir="ltr" className="mt-1 max-w-full break-all text-sm text-muted-foreground">@{detail.username}</bdi>}
        {hasCount && <p className="mt-1 text-sm text-muted-foreground">{count!.toLocaleString("fa-IR")} {detail.type === "channel" ? "مشترک" : "عضو"}</p>}
        {(detail.scam || detail.fake) && (
          <p className="mt-2 inline-flex items-center gap-1 rounded-full bg-destructive/10 px-2.5 py-1 text-xs text-destructive">
            <ShieldAlertIcon className="size-3.5" aria-hidden="true" />
            {detail.scam && "گزارش‌شده به‌عنوان کلاهبرداری"}{detail.scam && detail.fake && " · "}{detail.fake && "جعلی"}
          </p>
        )}

        <div className={`mt-5 grid w-full ${actionColumns} gap-2`}>
          {actions.map((action) => action.key === "more" ? (
            <DropdownMenu key={action.key}>
              <DropdownMenuTrigger
                render={<Button type="button" variant="ghost" className="h-auto min-h-16 w-full flex-col gap-1 rounded-xl bg-background/80 px-2 py-2.5 text-xs hover:bg-background focus-visible:ring-2" />}
              >
                <InfoIcon className="size-5" aria-hidden="true" />
                <span>بیشتر</span>
              </DropdownMenuTrigger>
              <DropdownMenuContent align="center">
                {onEditCategories && <DropdownMenuItem onClick={onEditCategories}><TagsIcon />ویرایش دسته‌بندی</DropdownMenuItem>}
                {onDelete && <DropdownMenuItem variant="destructive" onClick={onDelete}><Trash2Icon />حذف کانال</DropdownMenuItem>}
              </DropdownMenuContent>
            </DropdownMenu>
          ) : (
            <Button
              key={action.key}
              type="button"
              variant="ghost"
              onClick={action.onSelect}
              className="h-auto min-h-16 w-full flex-col gap-1 rounded-xl bg-background/80 px-2 py-2.5 text-xs hover:bg-background focus-visible:ring-2"
            >
              <action.icon className="size-5" aria-hidden="true" />
              <span className="break-words text-center">{action.label}</span>
            </Button>
          ))}
        </div>
      </div>

      <div className="space-y-5 bg-background px-5 py-5 sm:px-8 sm:py-6">
        {detail.description?.trim() && (
          <section aria-labelledby="channel-description-label" className="space-y-1.5">
            <p className="break-words whitespace-pre-wrap text-sm leading-7">{detail.description}</p>
            <h3 id="channel-description-label" className="text-xs text-muted-foreground">توضیحات</h3>
          </section>
        )}

        <Accordion type="single" collapsible>
          <AccordionItem value="details">
            <AccordionTrigger>اطلاعات بیشتر</AccordionTrigger>
            <AccordionContent>
              <dl className="space-y-3 text-sm">
                <Metadata label="شناسه کانال"><bdi dir="ltr" className="font-mono">{detail.id}</bdi><Button type="button" variant="ghost" size="icon-sm" aria-label="کپی شناسه کانال" onClick={onCopyId}><CopyIcon /></Button></Metadata>
                <Metadata label="نوع کانال">{channelTypeLabel(detail.type)}</Metadata>
                <Metadata label="وضعیت عضویت">{membershipLabel(detail.membership_status)} · {detail.is_member ? "عضو هستید" : "عضو نیستید"}</Metadata>
                <Metadata label="وضعیت مدیریت">{detail.is_managed ? "مدیریت‌شده" : "مدیریت‌نشده"}</Metadata>
                {detail.permissions && (
                  <div>
                    <dt className="mb-1.5 text-xs text-muted-foreground">دسترسی‌ها</dt>
                    <dd className="flex flex-wrap gap-1.5">
                      {detail.permissions.read && <Permission>مشاهده پیام</Permission>}
                      {detail.permissions.new && <Permission>ارسال پیام</Permission>}
                      {detail.permissions.edit && <Permission>ویرایش پیام</Permission>}
                      {detail.permissions.delete && <Permission>حذف پیام</Permission>}
                    </dd>
                  </div>
                )}
              </dl>
            </AccordionContent>
          </AccordionItem>
        </Accordion>
      </div>
    </div>
  )
}

function Metadata({ label, children }: { label: string; children: React.ReactNode }) {
  return <div><dt className="mb-0.5 text-xs text-muted-foreground">{label}</dt><dd className="flex min-h-8 items-center gap-1.5 break-words">{children}</dd></div>
}

function Permission({ children }: { children: React.ReactNode }) {
  return <span className="rounded-full bg-muted px-2 py-1 text-xs">{children}</span>
}

function membershipLabel(status: string) {
  if (status === "member") return "عضو"
  if (status === "join_pending") return "در انتظار عضویت"
  if (status === "unknown") return "نامشخص"
  return status
}

function channelTypeLabel(type: string) {
  if (type === "channel") return "کانال"
  if (type === "chat") return "گروه"
  return type
}

export function ChannelInformationDialog({
  open,
  clientKey,
  telegramChannelId,
  localChannelId,
  onOpenChange,
  onView,
  onEditCategories,
  onDelete,
  fetchDetail,
}: ChannelInformationDialogProps) {
  const { toast } = useToast()
  const requestKey = channelRequestKey(clientKey, telegramChannelId)
  const [loaded, setLoaded] = React.useState<{ key: string; detail: ChannelDetail } | null>(null)
  const [failure, setFailure] = React.useState<{ key: string; message: string } | null>(null)
  const currentDetail = currentChannelDetail(open, requestKey, loaded)
  const currentError = open && failure?.key === requestKey ? failure.message : null

  React.useEffect(() => {
    if (!open) {
      setLoaded(null)
      setFailure(null)
      return
    }
    const controller = new AbortController()
    setFailure(null)
    fetchDetail(clientKey, telegramChannelId, controller.signal)
      .then((result) => { if (!controller.signal.aborted) setLoaded({ key: requestKey, detail: result }) })
      .catch(() => { if (!controller.signal.aborted) setFailure({ key: requestKey, message: "دریافت اطلاعات کانال ناموفق بود." }) })
    return () => controller.abort()
  }, [fetchDetail, open, requestKey, clientKey, telegramChannelId])

  const copyId = async () => {
    if (!currentDetail) return
    await copyChannelId(currentDetail.id, (id) => navigator.clipboard.writeText(id), (copied) => {
      toast(copied
        ? { variant: "success", title: "شناسه کانال کپی شد." }
        : { variant: "error", title: "کپی شناسه کانال ناموفق بود." })
    })
  }

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent dir="rtl" className="flex max-h-[min(88dvh,48rem)] w-[calc(100vw-1rem)] max-w-[560px] flex-col gap-0 overflow-hidden rounded-2xl p-0 sm:max-w-[560px]">
        <DialogTitle className="sr-only">اطلاعات کانال</DialogTitle>
        {!currentDetail && !currentError && <ChannelInformationLoading />}
        {currentError && <ChannelInformationError message={currentError} onClose={() => onOpenChange(false)} />}
        {currentDetail && <ChannelInformationProfile
          detail={currentDetail}
          localChannelId={localChannelId}
          onCopyId={copyId}
          onView={currentDetail.permissions?.read ? onView : undefined}
          onEditCategories={onEditCategories}
          onDelete={onDelete}
        />}
      </DialogContent>
    </Dialog>
  )
}
