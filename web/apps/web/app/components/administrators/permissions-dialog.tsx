import * as React from "react"
import { useFetcher, useNavigation } from "react-router"

import { Badge } from "@workspace/ui/components/badge"
import { Button } from "@workspace/ui/components/button"
import { Checkbox } from "@workspace/ui/components/checkbox"
import {
  Dialog,
  DialogContent,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@workspace/ui/components/dialog"
import { Label } from "@workspace/ui/components/label"
import { Separator } from "@workspace/ui/components/separator"
import { Skeleton } from "@workspace/ui/components/skeleton"
import {
  Tabs,
  TabsContent,
  TabsList,
  TabsTrigger,
} from "@workspace/ui/components/tabs"

import { CategoryMultiSelect } from "@/components/category-multi-select"
import { MultiPicker } from "@/components/multi-picker"
import { faDigits } from "@/components/persian-digits"
import { SubmitButton } from "@/components/submit-button"
import {
  buildReport,
  holdsGlobal,
  manageNeedsView,
  normalizeDraft,
  serializeDraft,
  setScope,
  toggleGlobal,
  type PermissionDraft,
  type PermissionsPayload,
  type ReportGroup,
  type ScopeCatalog,
  type ScopeKind,
} from "@/lib/admin-permissions"
import type {
  Category,
  Channel,
  GlobalPermission,
  User,
  WordPressSite,
} from "@/lib/types"

// The permissions dialog: one editable draft behind six tabs. Every tab reads
// and writes that same draft, and nothing here submits on its own — the save is
// the footer's one button, and leaving the dialog throws the draft away.
//
// The first tab is a report of what the draft will actually grant. It is not a
// second editor, and it never claims more than the rules give: a group with no
// viewing permission reaches nothing, and a picked list is a real restriction
// rather than a decoration on top of "everything".

export const permissionsTabs = [
  { value: "report", label: "گزارش دسترسی‌ها" },
  { value: "categories", label: "دسته‌بندی‌ها" },
  { value: "channels", label: "کانال‌ها" },
  { value: "wordpress", label: "سایت‌های وردپرسی" },
  { value: "users", label: "مدیرها" },
  { value: "general", label: "عمومی" },
] as const

export type PermissionsTab = (typeof permissionsTabs)[number]["value"]

export interface PermissionsCatalog {
  channels?: Channel[] | null
  categories?: Category[] | null
  sites?: WordPressSite[] | null
}

export function AdministratorPermissionsContent({
  user,
  draft,
  loaded,
  catalog,
  tab,
  onTabChange,
  pending,
  error,
  onClose,
  onDraftChange,
  onSave,
}: {
  user: User
  draft: PermissionDraft
  loaded: PermissionsPayload | null
  catalog: PermissionsCatalog
  tab?: PermissionsTab
  onTabChange?: (tab: PermissionsTab) => void
  pending: boolean
  error?: string
  onClose: () => void
  onDraftChange: (draft: PermissionDraft) => void
  onSave: () => void
}) {
  const [innerTab, setInnerTab] = React.useState<PermissionsTab>("report")
  const activeTab = tab ?? innerTab
  const items = scopeItemsOf(catalog)

  return (
    <>
      <DialogHeader className="shrink-0">
        <DialogTitle>مدیریت دسترسی‌ها</DialogTitle>
        <p className="text-sm text-muted-foreground">
          مدیر: <span dir="ltr">{user.username}</span>
        </p>
      </DialogHeader>
      <form
        id={`permissions-form-${user.id}`}
        className="flex min-h-0 flex-1 flex-col gap-4"
        noValidate
        onSubmit={(event) => {
          event.preventDefault()
          if (!pending) onSave()
        }}
      >
        <Tabs
          value={activeTab}
          onValueChange={(next) => {
            const value = next as PermissionsTab
            setInnerTab(value)
            onTabChange?.(value)
          }}
          className="flex min-h-0 flex-col"
        >
          <TabsList className="h-auto w-full flex-wrap justify-start">
            {permissionsTabs.map((item) => (
              <TabsTrigger key={item.value} value={item.value}>
                {item.label}
              </TabsTrigger>
            ))}
          </TabsList>

          <div className="mt-3 min-h-0 flex-1 overflow-y-auto pe-1">
            <TabsContent value="report" className="mt-0">
              <PermissionsReportPanel
                draft={draft}
                loaded={loaded}
                items={items}
              />
            </TabsContent>
            <TabsContent value="categories" className="mt-0">
              <PermissionScopeEditor
                title="دسترسی دسته‌بندی‌ها"
                kind="categories"
                draft={draft}
                onDraftChange={onDraftChange}
                picker={
                  holdsGlobal(draft, "categories.view") ? (
                    <CategoryMultiSelect
                      id="permission-categories"
                      title="دسته‌بندی‌هایی که این مدیر می‌بیند"
                      categories={catalog.categories ?? []}
                      value={draft.categoryIDs}
                      onChange={(ids) =>
                        onDraftChange(setScope(draft, "categories", ids))
                      }
                      emptyText={
                        catalog.categories
                          ? "دسته‌بندی‌ای یافت نشد."
                          : "فهرست دسته‌بندی‌ها در دسترس نیست."
                      }
                    />
                  ) : null
                }
              />
            </TabsContent>
            <TabsContent value="channels" className="mt-0">
              <PermissionScopeEditor
                title="دسترسی کانال‌ها"
                kind="channels"
                draft={draft}
                onDraftChange={onDraftChange}
                picker={
                  holdsGlobal(draft, "channels.view") ? (
                    <MultiPicker
                      id="permission-channels"
                      title="کانال‌هایی که این مدیر می‌بیند"
                      placeholder="همه کانال‌ها"
                      emptyText={
                        catalog.channels
                          ? "کانالی یافت نشد."
                          : "فهرست کانال‌ها در دسترس نیست."
                      }
                      items={catalog.channels ?? []}
                      value={draft.channelIDs}
                      onValueChange={(ids) =>
                        onDraftChange(setScope(draft, "channels", ids))
                      }
                      labelOf={(channel) => channel.title || channel.client_key}
                    />
                  ) : null
                }
              />
            </TabsContent>
            <TabsContent value="wordpress" className="mt-0">
              <PermissionScopeEditor
                title="دسترسی سایت‌های وردپرسی"
                kind="wordpress"
                draft={draft}
                onDraftChange={onDraftChange}
                picker={
                  holdsGlobal(draft, "wordpress.view") ? (
                    <MultiPicker
                      id="permission-sites"
                      title="سایت‌های وردپرسی که این مدیر می‌بیند"
                      placeholder="همه سایت‌ها"
                      emptyText={
                        catalog.sites
                          ? "سایت وردپرسی یافت نشد."
                          : "فهرست سایت‌های وردپرسی در دسترس نیست."
                      }
                      items={catalog.sites ?? []}
                      value={draft.siteIDs}
                      onValueChange={(ids) =>
                        onDraftChange(setScope(draft, "wordpress", ids))
                      }
                      labelOf={(site) => site.title || site.domain}
                    />
                  ) : null
                }
              />
            </TabsContent>
            <TabsContent value="users" className="mt-0">
              <PermissionFlags
                title="دسترسی مدیرها"
                draft={draft}
                onDraftChange={onDraftChange}
                flags={[
                  { value: "users.view", label: "مشاهده مدیرها" },
                  { value: "users.manage", label: "مدیریت مدیرها" },
                ]}
              />
            </TabsContent>
            <TabsContent value="general" className="mt-0">
              <PermissionFlags
                title="عمومی"
                draft={draft}
                onDraftChange={onDraftChange}
                flags={[
                  {
                    value: "servers.view",
                    label: "مشاهده وضعیت سرورهای تلگرامی",
                  },
                ]}
              />
            </TabsContent>
          </div>
        </Tabs>

        {error && (
          <p
            role="alert"
            className="rounded-lg bg-destructive/10 px-3 py-2 text-sm text-destructive"
          >
            {error}
          </p>
        )}

        <DialogFooter className="shrink-0">
          <Button
            type="button"
            variant="outline"
            onClick={onClose}
            disabled={pending}
          >
            انصراف
          </Button>
          <SubmitButton pending={pending} submittingLabel="در حال ذخیره...">
            ذخیره دسترسی‌ها
          </SubmitButton>
        </DialogFooter>
      </form>
    </>
  )
}

// AdministratorPermissionsDialog loads the account's stored set and holds the
// draft while it is open. Opening it again starts from what is stored, never
// from the previous visit's unsaved state.
export function AdministratorPermissionsDialog({
  user,
  error,
  onClose,
  onSave,
}: {
  user: User
  error?: string
  onClose: () => void
  onSave: (form: Record<string, string>) => void
}) {
  const fetcher = useFetcher<PermissionsLoadResult>()
  const navigation = useNavigation()
  const pending = navigation.state === "submitting"
  const [draft, setDraft] = React.useState<PermissionDraft | null>(null)
  const [catalog, setCatalog] = React.useState<PermissionsCatalog>({
    channels: null,
    categories: null,
    sites: null,
  })

  const requested = React.useRef(false)
  const retry = () => loadPermissions(fetcher, user.id)
  React.useEffect(() => {
    if (!requested.current) {
      requested.current = true
      loadPermissions(fetcher, user.id)
    }
  }, [fetcher, user.id])

  React.useEffect(() => {
    const data = fetcher.data
    if (!data?.permissions) return
    setDraft(normalizeDraft(data.permissions))
    setCatalog({
      channels: data.channels ?? null,
      categories: data.categories ?? null,
      sites: data.sites ?? null,
    })
  }, [fetcher.data])

  const loadError = fetcher.data?.permissionsError

  return (
    <Dialog open onOpenChange={(next) => !next && !pending && onClose()}>
      <DialogContent className="flex max-h-[88dvh] max-w-[840px] flex-col overflow-hidden">
        {!draft ? (
          <>
            <DialogHeader className="shrink-0">
              <DialogTitle>مدیریت دسترسی‌ها</DialogTitle>
              <p className="text-sm text-muted-foreground">
                مدیر: <span dir="ltr">{user.username}</span>
              </p>
            </DialogHeader>
            {loadError ? (
              <div className="flex flex-col gap-3">
                <p
                  role="alert"
                  className="rounded-lg bg-destructive/10 px-3 py-2 text-sm text-destructive"
                >
                  {loadError}
                </p>
                <Button
                  variant="outline"
                  size="sm"
                  onClick={retry}
                  className="self-start"
                >
                  تلاش دوباره
                </Button>
              </div>
            ) : (
              <div className="flex flex-col gap-3" aria-busy="true">
                <span className="sr-only">در حال بارگذاری دسترسی‌ها…</span>
                <Skeleton className="h-9 w-full" />
                <Skeleton className="h-24 w-full" />
                <Skeleton className="h-24 w-full" />
              </div>
            )}
          </>
        ) : (
          <AdministratorPermissionsContent
            user={user}
            draft={draft}
            loaded={fetcher.data?.permissions ?? null}
            catalog={catalog}
            pending={pending}
            error={error}
            onClose={onClose}
            onDraftChange={setDraft}
            onSave={() =>
              onSave({
                intent: "permissions",
                id: String(user.id),
                ...serializeDraft(draft, scopeItemsOf(catalog)),
              })
            }
          />
        )}
      </DialogContent>
    </Dialog>
  )
}

interface PermissionsLoadResult {
  permissions?: PermissionsPayload
  permissionsError?: string
  channels?: Channel[] | null
  categories?: Category[] | null
  sites?: WordPressSite[] | null
}

// loadPermissions asks this route's action, through a fetcher so the page never
// moves, for the account's stored permissions and the catalogs to pick from.
function loadPermissions(
  fetcher: {
    submit: (
      data: Record<string, string>,
      options: { method: "post" }
    ) => Promise<unknown>
  },
  id: number
) {
  void fetcher.submit(
    { intent: "permissions-load", id: String(id) },
    { method: "post" }
  )
}

// --- the tabs' shared pieces ---

// PermissionFlags is a group of plain global permissions: no resource scope,
// just the flags the group has.
function PermissionFlags({
  title,
  draft,
  onDraftChange,
  flags,
}: {
  title: string
  draft: PermissionDraft
  onDraftChange: (draft: PermissionDraft) => void
  flags: { value: GlobalPermission; label: string }[]
}) {
  return (
    <section className="flex flex-col gap-3">
      <h3 className="text-sm font-medium">{title}</h3>
      <div className="flex flex-col gap-2">
        {flags.map((flag) => (
          <PermissionCheckbox
            key={flag.value}
            id={`permission-${flag.value}`}
            label={flag.label}
            checked={holdsGlobal(draft, flag.value)}
            disabled={!canHold(draft, flag.value)}
            onCheckedChange={() =>
              onDraftChange(toggleGlobal(draft, flag.value))
            }
          />
        ))}
      </div>
    </section>
  )
}

// PermissionScopeEditor is the three resource groups' shared shape: the two
// flags and the picker that names the resources below them. The picker is
// passed in rather than abstracted over, so each group keeps its own selector
// and none of them is a copy of another.
function PermissionScopeEditor({
  title,
  kind,
  draft,
  onDraftChange,
  picker,
}: {
  title: string
  kind: ScopeKind
  draft: PermissionDraft
  onDraftChange: (draft: PermissionDraft) => void
  picker: React.ReactNode
}) {
  const viewValue = viewFlagOf(kind)
  const manageValue = manageFlagOf(kind)
  return (
    <section className="flex flex-col gap-3">
      <h3 className="text-sm font-medium">{title}</h3>
      <div className="flex flex-col gap-2">
        <PermissionCheckbox
          id={`permission-${viewValue}`}
          label={viewLabelOf(kind)}
          checked={holdsGlobal(draft, viewValue)}
          disabled={!canHold(draft, viewValue)}
          onCheckedChange={() => onDraftChange(toggleGlobal(draft, viewValue))}
        />
        <PermissionCheckbox
          id={`permission-${manageValue}`}
          label={manageLabelOf(kind)}
          checked={holdsGlobal(draft, manageValue)}
          disabled={!canHold(draft, manageValue)}
          onCheckedChange={() =>
            onDraftChange(toggleGlobal(draft, manageValue))
          }
        />
      </div>
      {picker}
    </section>
  )
}

function PermissionCheckbox({
  id,
  label,
  checked,
  disabled,
  onCheckedChange,
}: {
  id: string
  label: string
  checked: boolean
  disabled?: boolean
  onCheckedChange: (checked: boolean) => void
}) {
  return (
    <Label
      htmlFor={id}
      className="flex cursor-pointer items-center gap-2 rounded-lg border px-3 py-2 hover:bg-muted/50"
    >
      <Checkbox
        id={id}
        checked={checked}
        disabled={disabled}
        onCheckedChange={onCheckedChange}
      />
      <span className="text-sm">{label}</span>
    </Label>
  )
}

function PermissionsReportPanel({
  draft,
  loaded,
  items,
}: {
  draft: PermissionDraft
  loaded: PermissionsPayload | null
  items: ScopeCatalog
}) {
  const report = buildReport(draft, items, loaded)
  return (
    <div className="flex flex-col gap-4">
      {report.unsaved && <Badge variant="muted">تغییرات ذخیره‌نشده</Badge>}
      {report.groups.map((group, index) => (
        <React.Fragment key={group.key}>
          {index > 0 && <Separator />}
          <ReportSection group={group} />
        </React.Fragment>
      ))}
    </div>
  )
}

function ReportSection({ group }: { group: ReportGroup }) {
  return (
    <section className="flex flex-col gap-2">
      <h3 className="text-sm font-medium">{group.title}</h3>
      <ul className="flex flex-col gap-1.5">
        {group.lines.map((line) => (
          <li key={line.key} className="flex items-center gap-2 text-sm">
            <span className="text-muted-foreground">{line.label}</span>
            {line.state === "allowed" ? (
              <Badge variant="success">مجاز</Badge>
            ) : (
              <Badge variant="muted">غیرمجاز</Badge>
            )}
          </li>
        ))}
      </ul>
      {group.scope && (
        <p className="text-xs text-muted-foreground">
          {group.scope.count === 0
            ? `دسترسی به همهٔ ${group.title}`
            : `دسترسی به ${faDigits(group.scope.count)} ${group.scope.label}`}
          {group.scope.names.length > 0 &&
            `: ${group.scope.names.slice(0, 6).join("، ")}${
              group.scope.names.length > 6 ? "، …" : ""
            }`}
        </p>
      )}
    </section>
  )
}

// --- small mappings ---

function scopeItemsOf(catalog: PermissionsCatalog): ScopeCatalog {
  return {
    categories: (catalog.categories ?? []).map((category) => ({
      id: category.id,
      label: category.name,
    })),
    channels: (catalog.channels ?? []).map((channel) => ({
      id: channel.id,
      label: channel.title || channel.client_key,
    })),
    sites: (catalog.sites ?? []).map((site) => ({
      id: site.id,
      label: site.title || site.domain,
    })),
  }
}

function viewFlagOf(kind: ScopeKind): GlobalPermission {
  switch (kind) {
    case "categories":
      return "categories.view"
    case "channels":
      return "channels.view"
    default:
      return "wordpress.view"
  }
}

function manageFlagOf(kind: ScopeKind): GlobalPermission {
  switch (kind) {
    case "categories":
      return "categories.create"
    case "channels":
      return "channels.create"
    default:
      return "wordpress.create"
  }
}

function viewLabelOf(kind: ScopeKind) {
  switch (kind) {
    case "categories":
      return "مشاهده دسته‌بندی‌ها"
    case "channels":
      return "مشاهده کانال‌ها"
    default:
      return "مشاهده سایت‌های وردپرسی"
  }
}

function manageLabelOf(kind: ScopeKind) {
  switch (kind) {
    case "categories":
      return "مدیریت دسته‌بندی‌ها"
    case "channels":
      return "مدیریت کانال‌ها"
    default:
      return "مدیریت سایت‌های وردپرسی"
  }
}

// canHold is whether the checkbox may be turned on at all: a managing
// permission needs its viewing permission, which is exactly what the API
// refuses without.
function canHold(draft: PermissionDraft, value: GlobalPermission) {
  const needs = manageNeedsView[value]
  return needs === undefined || holdsGlobal(draft, needs)
}
