import { Link } from "react-router"
import { MoreHorizontalIcon, PlusIcon } from "lucide-react"

import { DeliveryStatusBadge, EnabledBadge } from "@/components/automation/status"
import type { SourceChannelOption } from "@/components/automation/webhook-form"
import { faDateTime, faDigits } from "@/components/persian-digits"
import { Badge } from "@workspace/ui/components/badge"
import { Button } from "@workspace/ui/components/button"
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from "@workspace/ui/components/dropdown-menu"
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@workspace/ui/components/table"

import {
  automationRoute,
  authModeLabels,
  methodLabels,
  type DeliveryItem,
  type WebhookItem,
} from "@/lib/automation"

// The webhook collection's table, shared by the automation overview and the
// delivery screens. It is the collection as rows: what the store holds, one
// line per record, and the actions that act on exactly one of them.

// Physical left, not RTL start: the actions column stays pinned while the
// table scrolls under it, and bg-background keeps text from showing through.
const ACTIONS_HEAD = "sticky left-0 z-20 w-12 bg-background text-end"
const ACTIONS_CELL = "sticky left-0 z-10 bg-background text-end"

// What one test send reported. It is never a persisted delivery: the service
// writes nothing to the history for a test, so the outcome is shown from this
// response and nowhere else. Only the outcome travels back to the page — never
// the request or the response the exchange carried.
export interface TestOutcome {
  status: DeliveryItem["status"]
  httpStatus: number
  error: string
}

// The outcome of one test send, read off the exchange the service returned.
// Only the outcome travels on: a test's request and response carry the same
// headers a real one does, and no page here renders them.
export function testOutcomeOf(delivery: DeliveryItem): TestOutcome {
  const attempt = delivery.attempts.at(-1)
  return {
    status: delivery.status,
    httpStatus: attempt?.responseStatus ?? delivery.lastHttpStatus,
    error: delivery.lastError || attempt?.errorMessage || "",
  }
}

export function WebhooksTable({
  webhooks,
  channels,
  canManage,
  onEdit,
  onToggle,
  onDelete,
  onTest,
}: {
  webhooks: WebhookItem[]
  channels: SourceChannelOption[]
  canManage: boolean
  onEdit: (webhook: WebhookItem) => void
  onToggle: (webhook: WebhookItem) => void
  onDelete: (webhook: WebhookItem) => void
  onTest: (webhook: WebhookItem) => void
}) {
  const labelOf = (id: number) =>
    channels.find((channel) => channel.id === id)?.label ?? `کانال ${id}`
  return (
    <Table>
      <TableHeader>
        <TableRow>
          <TableHead>نام</TableHead>
          <TableHead>کانال‌های مبدأ</TableHead>
          <TableHead>نشانی مقصد</TableHead>
          <TableHead>روش</TableHead>
          <TableHead>شیوه احراز</TableHead>
          <TableHead>وضعیت</TableHead>
          <TableHead>آخرین تحویل</TableHead>
          <TableHead>نتیجه</TableHead>
          <TableHead className={ACTIONS_HEAD}>
            <span className="sr-only">عملیات</span>
          </TableHead>
        </TableRow>
      </TableHeader>
      <TableBody>
        {webhooks.map((webhook) => (
          <TableRow key={webhook.id}>
            <TableCell className="font-medium">
              <Link
                to={automationRoute.deliveries(webhook.id)}
                className="underline-offset-4 hover:underline"
              >
                {webhook.name}
              </Link>
            </TableCell>
            <TableCell className="text-muted-foreground">
              {webhook.sourceChannelIds.length === 0
                ? "—"
                : webhook.sourceChannelIds.map(labelOf).join("، ")}
            </TableCell>
            <TableCell className="max-w-56">
              <span
                dir="ltr"
                className="block truncate text-start text-xs"
                title={webhook.url}
              >
                {webhook.url || "—"}
              </span>
            </TableCell>
            <TableCell>
              <span dir="ltr" className="text-xs font-medium">
                {methodLabels[webhook.method]}
              </span>
            </TableCell>
            <TableCell className="text-muted-foreground whitespace-nowrap">
              {authModeLabels[webhook.authMode]}
            </TableCell>
            <TableCell>
              <EnabledBadge enabled={webhook.enabled} />
            </TableCell>
            <TableCell>
              {webhook.lastDelivery ? (
                <span className="flex flex-col gap-0.5">
                  <DeliveryStatusBadge status={webhook.lastDelivery.status} />
                  <span className="text-xs text-muted-foreground">
                    {webhook.lastDelivery.httpStatus > 0 ? (
                      <span dir="ltr">HTTP {webhook.lastDelivery.httpStatus}</span>
                    ) : (
                      "—"
                    )}
                    {" · "}
                    {faDateTime(webhook.lastDelivery.at)}
                  </span>
                </span>
              ) : (
                <span className="text-xs text-muted-foreground">—</span>
              )}
            </TableCell>
            <TableCell>
              {webhook.summary ? (
                <span className="flex flex-wrap gap-1">
                  <Badge variant="success">{faDigits(webhook.summary.succeeded)}</Badge>
                  <Badge variant="destructive">{faDigits(webhook.summary.failed)}</Badge>
                  <Badge variant="muted">{faDigits(webhook.summary.pending)}</Badge>
                </span>
              ) : (
                <span className="text-xs text-muted-foreground">—</span>
              )}
            </TableCell>
            <TableCell className={ACTIONS_CELL}>
              <DropdownMenu>
                <DropdownMenuTrigger
                  render={
                    <Button
                      variant="ghost"
                      size="icon-sm"
                      aria-label={`عملیات وب‌هوک ${webhook.name}`}
                    />
                  }
                >
                  <MoreHorizontalIcon />
                </DropdownMenuTrigger>
                <DropdownMenuContent align="end">
                  {/* Reading the history is a read and stays available to anyone
                      who may see the automation area. Every other row action is
                      a write, and a write here needs webhooks.manage. */}
                  <DropdownMenuItem
                    render={
                      <Link to={automationRoute.deliveries(webhook.id)} />
                    }
                  >
                    تاریخچه تحویل‌ها
                  </DropdownMenuItem>
                  {canManage && (
                    <>
                      <DropdownMenuItem onClick={() => onEdit(webhook)}>
                        ویرایش
                      </DropdownMenuItem>
                      <DropdownMenuItem onClick={() => onToggle(webhook)}>
                        {webhook.enabled ? "غیرفعال‌سازی" : "فعال‌سازی"}
                      </DropdownMenuItem>
                      <DropdownMenuItem onClick={() => onTest(webhook)}>
                        ارسال آزمایشی
                      </DropdownMenuItem>
                      <DropdownMenuSeparator />
                      <DropdownMenuItem
                        variant="destructive"
                        onClick={() => onDelete(webhook)}
                      >
                        حذف
                      </DropdownMenuItem>
                    </>
                  )}
                </DropdownMenuContent>
              </DropdownMenu>
            </TableCell>
          </TableRow>
        ))}
      </TableBody>
    </Table>
  )
}

// The one button that opens the create form. It is the same action wherever the
// page offers it — in the header of a full list and in the empty state of a
// blank one — so it carries its own name and cannot drift between the two.
export function AddWebhookButton({
  onAdd,
  variant = "default",
  size,
}: {
  onAdd: () => void
  variant?: "default" | "outline"
  size?: "sm"
}) {
  return (
    <Button variant={variant} size={size} onClick={onAdd}>
      <PlusIcon data-icon="inline-start" />
      افزودن وب‌هوک
    </Button>
  )
}
