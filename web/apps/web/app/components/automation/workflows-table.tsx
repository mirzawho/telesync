import { Link, useLocation } from "react-router"
import { MoreHorizontalIcon } from "lucide-react"
import * as React from "react"

import {
  EnabledBadge,
  RunStatusBadge,
  SwitchState,
} from "@/components/automation/status"
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
import { Switch } from "@workspace/ui/components/switch"
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@workspace/ui/components/table"

import {
  automationFormLinkState,
  automationRoute,
  destinationTypeLabels,
  type WorkflowItem,
} from "@/lib/automation"

// The workflow collection's table, shared by the automation overview and the
// run screens. One workflow per source channel, so a row is a channel and what
// it publishes.

// Physical left, not RTL start: the actions column stays pinned while the
// table scrolls under it, and bg-background keeps text from showing through.
const ACTIONS_HEAD = "sticky left-0 z-20 w-12 bg-background text-end"
const ACTIONS_CELL = "sticky left-0 z-10 bg-background text-end"

export function WorkflowsTable({
  workflows,
  channelTitles,
  canManage,
  onDelete,
  onToggle,
  toggleError,
}: {
  workflows: WorkflowItem[]
  channelTitles: Map<number, string>
  canManage: boolean
  onDelete: (workflow: WorkflowItem) => void
  onToggle: (workflow: WorkflowItem, enabled: boolean) => void
  // The backend's refusal of the last toggle, by workflow id. The row shows it
  // and keeps the state the server holds: a switch is never left looking active
  // for a workflow the server said it would not run.
  toggleError?: { id: number; message: string } | null
}) {
  // The editor is a page of its own, and a save or a cancel there comes back to
  // the view this row was read from — the tab, the query, the page.
  const location = useLocation()
  const back = automationFormLinkState(location.pathname + location.search)
  const titleOf = (workflow: WorkflowItem) =>
    workflow.channel?.title ??
    channelTitles.get(workflow.managedChannelID) ??
    `کانال ${workflow.managedChannelID}`
  return (
    <Table>
      <TableHeader>
        <TableRow>
          <TableHead>کانال مبدأ</TableHead>
          <TableHead>وضعیت</TableHead>
          <TableHead>هوش مصنوعی</TableHead>
          <TableHead>مقاصد</TableHead>
          <TableHead>آخرین اجرا</TableHead>
          <TableHead>نتیجه</TableHead>
          <TableHead className={ACTIONS_HEAD}>
            <span className="sr-only">عملیات</span>
          </TableHead>
        </TableRow>
      </TableHeader>
      <TableBody>
        {workflows.map((workflow) => (
          <TableRow key={workflow.id}>
            <TableCell className="font-medium">
              <Link
                to={automationRoute.runs(workflow.id)}
                className="underline-offset-4 hover:underline"
              >
                {titleOf(workflow)}
              </Link>
            </TableCell>
            <TableCell>
              <span className="flex flex-col gap-1">
                <span className="flex items-center gap-2">
                  <EnabledBadge enabled={workflow.enabled} />
                  {canManage && (
                    <Switch
                      checked={workflow.enabled}
                      // No optimistic flip: the badge changes only when the
                      // loader brings back what the server stored.
                      onCheckedChange={(enabled) => onToggle(workflow, enabled)}
                      aria-label={`فعال‌سازی ورک‌فلوی ${titleOf(workflow)}`}
                    />
                  )}
                </span>
                {toggleError && toggleError.id === workflow.id && (
                  <span
                    role="alert"
                    className="max-w-56 text-xs font-medium text-destructive"
                  >
                    {toggleError.message}
                  </span>
                )}
              </span>
            </TableCell>
            <TableCell>
              <SwitchState
                on={workflow.aiEnabled}
                onLabel="روشن"
                offLabel="خاموش"
              />
            </TableCell>
            <TableCell>
              {workflow.destinations.length === 0 ? (
                <span className="text-xs text-muted-foreground">—</span>
              ) : (
                <span className="flex flex-wrap gap-1">
                  {(
                    Object.entries(destinationTypeLabels) as [
                      keyof typeof destinationTypeLabels,
                      string,
                    ][]
                  )
                    .map(([type, label]) => {
                      const count = workflow.destinations.filter(
                        (destination) => destination.type === type
                      ).length
                      return count > 0 ? (
                        <Badge key={type} variant="outline">
                          {label} {faDigits(count)}
                        </Badge>
                      ) : null
                    })
                    .filter(Boolean)}
                </span>
              )}
            </TableCell>
            <TableCell className="text-muted-foreground">
              {workflow.lastRun ? faDateTime(workflow.lastRun.createdAt) : "—"}
            </TableCell>
            <TableCell>
              {workflow.lastRun ? (
                <RunStatusBadge status={workflow.lastRun.status} />
              ) : (
                <span className="text-xs text-muted-foreground">—</span>
              )}
            </TableCell>
            <TableCell className={ACTIONS_CELL}>
              {canManage && (
                <DropdownMenu>
                  <DropdownMenuTrigger
                    render={
                      <Button
                        variant="ghost"
                        size="icon-sm"
                        aria-label={`عملیات ورک‌فلوی ${titleOf(workflow)}`}
                      />
                    }
                  >
                    <MoreHorizontalIcon />
                  </DropdownMenuTrigger>
                  <DropdownMenuContent align="end">
                    <DropdownMenuItem
                      render={
                        <Link
                          to={automationRoute.editWorkflow(workflow.id)}
                          state={back}
                        />
                      }
                    >
                      ویرایش
                    </DropdownMenuItem>
                    <DropdownMenuItem
                      render={<Link to={automationRoute.runs(workflow.id)} />}
                    >
                      تاریخچه اجراها
                    </DropdownMenuItem>
                    <DropdownMenuSeparator />
                    <DropdownMenuItem
                      variant="destructive"
                      onClick={() => onDelete(workflow)}
                    >
                      حذف
                    </DropdownMenuItem>
                  </DropdownMenuContent>
                </DropdownMenu>
              )}
            </TableCell>
          </TableRow>
        ))}
      </TableBody>
    </Table>
  )
}
