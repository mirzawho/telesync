import {
  Link,
  redirect,
  useFetcher,
  useNavigation,
  useSearchParams,
  useSubmit,
} from "react-router"
import { MoreHorizontalIcon, PlusIcon, UserCogIcon } from "lucide-react"
import * as React from "react"

import { MultiPicker } from "@/components/multi-picker"
import { ConfirmDialog } from "@/components/confirm-dialog"
import { PageHeader } from "@/components/page-header"
import { PageError, PageShell, pageMeta, section } from "@/lib/page"
import { faDateTime, faDigits } from "@/components/persian-digits"
import { SearchInput } from "@/components/search-input"
import { EmptyState, ErrorState, LoadingState } from "@/components/states"
import { Badge } from "@workspace/ui/components/badge"
import { Button } from "@workspace/ui/components/button"
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@workspace/ui/components/dialog"
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from "@workspace/ui/components/dropdown-menu"
import {
  Field,
  FieldControl,
  FieldDescription,
  FieldError,
  FieldLabel,
} from "@workspace/ui/components/field"

import { ApiError, apiRequest } from "@/lib/api"
import { LOGIN_REDIRECT_TO, listAll, requireSession } from "@/lib/session"
import { strip, requestURL } from "@/path"
import type {
  Category,
  Channel,
  GlobalPermission,
  Permissions,
  ResourcePermissions,
  User,
} from "@/lib/types"

import type { Route } from "./+types/administrators"

const PAGE_SIZE = 20

interface UserList {
  users: User[]
}

interface UserResponse {
  user: User
}

// The valid global permissions, with Persian labels.
const globalPermissionOptions: {
  value: GlobalPermission
  label: string
}[] = [
  { value: "users.view", label: "مشاهده مدیرها" },
  { value: "users.manage", label: "مدیریت مدیرها" },
  { value: "channels.view", label: "مشاهده کانال‌ها" },
  { value: "channels.create", label: "مدیریت کانال‌ها" },
  { value: "categories.view", label: "مشاهده دسته‌بندی‌ها" },
  { value: "categories.create", label: "مدیریت دسته‌بندی‌ها" },
  { value: "wordpress.view", label: "مشاهده سایت‌های وردپرسی" },
  { value: "wordpress.create", label: "مدیریت سایت‌های وردپرسی" },
  { value: "servers.view", label: "مشاهده وضعیت سرورهای تلگرام" },
  { value: "content.publish", label: "ارسال و انتشار محتوا" },
]

// A managing permission needs its viewing permission; the API refuses a set
// that has one without the other.
const manageNeedsView: Partial<Record<GlobalPermission, GlobalPermission>> = {
  "users.manage": "users.view",
  "channels.create": "channels.view",
  "categories.create": "categories.view",
  "wordpress.create": "wordpress.view",
}

// withViewDependencies adds the view permission a managing permission needs and
// drops a managing permission whose view permission is gone.
function withViewDependencies(selected: Iterable<GlobalPermission>) {
  const out = new Set(selected)
  for (;;) {
    let changed = false
    for (const [manage, view] of Object.entries(manageNeedsView)) {
      if (out.has(manage as GlobalPermission) && !out.has(view)) {
        out.add(view as GlobalPermission)
        changed = true
      }
      if (
        !out.has(view as GlobalPermission) &&
        out.delete(manage as GlobalPermission)
      ) {
        changed = true
      }
    }
    if (!changed) return out
  }
}

// The permissions dialog's boxes are rendered from React state, not from the
// DOM's own form controls, so submitting the <form> directly posts no
// `global_permissions` and saving stores an empty set. Forms still submit in a
// runner that does not run React effects; a form that carries the boxes but
// posts nothing under that name is this bug.
if (import.meta.env.DEV) {
  document.addEventListener("submit", (event) => {
    const form = event.target
    if (
      form instanceof HTMLFormElement &&
      form.querySelector('[id^="permissions-form-"]') &&
      !form.elements.namedItem("global_permissions")
    ) {
      console.warn(
        "administrators: the permissions form posted no global_permissions"
      )
    }
  })
}

type PageTitle = { title: string }

type LoaderData =
  | {
      status: "ok"
      users: User[]
      currentPage: number
      hasMore: boolean
      q: string
      canManage: boolean
      meId: number
      page: PageTitle
    }
  | { status: "forbidden"; message: string; page: PageTitle }
  | { status: "error"; message: string; page: PageTitle }

function positiveInt(raw: string | null): number {
  const value = Number(raw)
  return Number.isInteger(value) && value > 0 ? value : 1
}

function loginRedirect(request: Request): Response {
  const url = requestURL(request)
  return redirect(
    `${LOGIN_REDIRECT_TO}?expired=1&next=${encodeURIComponent(strip(`${url.pathname}${url.search}`))}`
  )
}

export async function clientLoader({
  request,
  context,
}: Route.LoaderArgs): Promise<LoaderData> {
  const { token, me } = await requireSession(request)
  const url = requestURL(request)
  const q = (url.searchParams.get("q") ?? "").trim()
  const currentPage = positiveInt(url.searchParams.get("page"))
  const page: PageTitle = { title: "مدیرها" }

  try {
    const { data, meta } = await apiRequest<UserList>("GET", "/local/users", {
      token,
      query: { page: currentPage, per_page: PAGE_SIZE, q: q || undefined },
    })
    return {
      status: "ok",
      users: data.users ?? [],
      currentPage,
      hasMore: meta.has_more ?? false,
      q,
      canManage: Boolean(me.access?.users),
      meId: me.id,
      page,
    }
  } catch (error) {
    if (error instanceof ApiError) {
      if (error.status === 401) throw loginRedirect(request)
      if (error.isForbidden) {
        return {
          status: "forbidden",
          message: "شما مجوز مشاهده مدیرها را ندارید.",
          page,
        }
      }
      return { status: "error", message: error.message, page }
    }
    return { status: "error", message: "بارگذاری مدیرها ناموفق بود.", page }
  }
}

interface FieldErrors {
  fullname?: string
  username?: string
  password?: string
}

interface AdminActionData {
  error?: string
  intent?: string
  values?: {
    id?: string
    fullname?: string
    username?: string
    is_active?: string
  }
  fieldErrors?: FieldErrors
  // Successful permission load, served through the permissions fetcher.
  permissions?: Permissions
  permissionsError?: string
  // The catalog the pickers choose from; null when it could not be loaded or
  // was not asked for.
  channels?: Channel[] | null
  categories?: Category[] | null
}

interface PermissionsPayload {
  global: string[]
  channels: ResourcePermissions[]
  categories: ResourcePermissions[]
}

const intents = new Set([
  "create",
  "update",
  "delete",
  "permissions",
  "permissions-load",
])

function userPath(id: number) {
  return `/local/users/${id}`
}

// checkedGlobals reads the selected global permissions. The dialog renders its
// checkboxes from React state, so on submit they are not the DOM's own form
// controls and the page posts the whole selection as one JSON entry under the
// form's own field name; an entry per box is read too. A permission posted
// both ways counts once.
function checkedGlobals(form: FormData): GlobalPermission[] {
  const out = new Set<GlobalPermission>()
  const known = (value: unknown): value is GlobalPermission =>
    typeof value === "string" &&
    globalPermissionOptions.some((option) => option.value === value)
  for (const raw of form.getAll("global_permissions")) {
    if (known(raw)) {
      out.add(raw)
      continue
    }
    let posted: unknown
    try {
      posted = JSON.parse(String(raw))
    } catch {
      continue
    }
    if (Array.isArray(posted)) {
      for (const value of posted) if (known(value)) out.add(value)
    }
  }
  return [...withViewDependencies(out)]
}

// pickerIDs reads a picker's JSON list of local ids.
function pickerIDs(form: FormData, field: string): number[] {
  let posted: unknown
  try {
    posted = JSON.parse(String(form.get(field) ?? "[]"))
  } catch {
    return []
  }
  if (!Array.isArray(posted)) return []
  return posted.filter(
    (value): value is number => Number.isInteger(value) && value > 0
  )
}

// viewOnlyGrants turns picked ids into per-resource grant rows: the picked
// items are the ones the manager may see; none picked means every item.
function viewOnlyGrants(form: FormData, field: "channel_ids" | "category_ids") {
  const idKey = field === "channel_ids" ? "channel_id" : "category_id"
  return pickerIDs(form, field).map((id) => ({
    [idKey]: id,
    permissions: { view: "allow" },
  }))
}

function parseId(raw: FormDataEntryValue | null): number | null {
  const value = Number(raw)
  return Number.isInteger(value) && value > 0 ? value : null
}

// The PUT replaces the whole permission set, so the loaded per-resource grants
// are converted back into the PUT shape unchanged. `view` etc. are passed
// through as the API sent them ("allow"/"deny"); only present keys are sent.
function toPutShape(rows: ResourcePermissions[]): Record<string, string>[] {
  return rows.map((row) => {
    const permissions: Record<string, string> = {}
    for (const key of ["view", "update", "delete"] as const) {
      const value = row[key]
      if (value) permissions[key] = value
    }
    return permissions
  })
}

export async function clientAction({
  request,
  context,
}: Route.ActionArgs): Promise<AdminActionData | Response> {
  const { token, me } = await requireSession(request)
  const form = await request.formData()
  const intent = String(form.get("intent") ?? "")
  const id = parseId(form.get("id"))
  const fullname = String(form.get("fullname") ?? "").trim()
  const username = String(form.get("username") ?? "").trim()
  const password = String(form.get("password") ?? "")
  const isActive = form.get("is_active") === "true"
  const values = {
    id: id ? String(id) : undefined,
    fullname,
    username,
    is_active: String(isActive),
  }

  if (!intents.has(intent)) {
    return { error: "درخواست نامعتبر است.", intent, values }
  }
  if (intent !== "create" && id === null) {
    return { error: "شناسه مدیر نامعتبر است.", intent, values }
  }

  try {
    switch (intent) {
      case "create": {
        const fieldErrors: FieldErrors = {}
        if (!fullname) fieldErrors.fullname = "نام کامل الزامی است."
        if (!username) fieldErrors.username = "نام کاربری الزامی است."
        if (password.length < 8)
          fieldErrors.password = "رمز عبور باید دست‌کم ۸ نویسه باشد."
        if (Object.keys(fieldErrors).length > 0) {
          return { error: "فرم را کامل کنید.", intent, values, fieldErrors }
        }
        await apiRequest<UserResponse>("POST", "/local/users", {
          token,
          body: { fullname, username, password, is_active: isActive },
        })
        return redirect(strip(requestURL(request).pathname))
      }
      case "update": {
        const fieldErrors: FieldErrors = {}
        if (!fullname) fieldErrors.fullname = "نام کامل الزامی است."
        if (!username) fieldErrors.username = "نام کاربری الزامی است."
        if (password && password.length < 8)
          fieldErrors.password = "رمز عبور باید دست‌کم ۸ نویسه باشد."
        if (Object.keys(fieldErrors).length > 0) {
          return { error: "فرم را کامل کنید.", intent, values, fieldErrors }
        }
        // An empty password field means "leave the password unchanged".
        const body: Record<string, unknown> = {
          fullname,
          username,
          is_active: isActive,
        }
        if (password) body.password = password
        await apiRequest<UserResponse>("PUT", userPath(id as number), {
          token,
          body,
        })
        return redirect(strip(requestURL(request).pathname))
      }
      case "delete": {
        await apiRequest<{ deleted: boolean }>(
          "DELETE",
          userPath(id as number),
          { token }
        )
        return redirect(strip(requestURL(request).pathname))
      }
      case "permissions-load": {
        const { data } = await apiRequest<UserResponse>(
          "GET",
          userPath(id as number),
          { token }
        )
        const permissions = data.user.permissions ?? {
          global: [],
          channels: [],
          categories: [],
        }
        // The pickers need the whole catalog, not just the rows the subject may
        // see. Loaded only when the dialog can show a picker at all.
        const canPickChannels =
          me.access?.channels || permissions.channels.length > 0
        const canPickCategories =
          me.access?.categories || permissions.categories.length > 0
        const [channels, categories] = await Promise.all([
          canPickChannels
            ? pickOrNull(
                listAll<Channel>(
                  token,
                  "/local/channels",
                  (d) => (d as { channels: Channel[] }).channels
                )
              )
            : null,
          canPickCategories
            ? pickOrNull(
                listAll<Category>(
                  token,
                  "/local/categories",
                  (d) => (d as { categories: Category[] }).categories
                )
              )
            : null,
        ])
        return {
          permissions,
          channels,
          categories,
        }
      }
      default: {
        // intent === "permissions": replace the whole set.
        if (id === me.id) {
          return {
            error: "نمی‌توانید دسترسی‌های خودتان را تغییر دهید.",
            intent,
          }
        }
        if (!me.access?.users) {
          return { error: "شما مجوز مدیریت دسترسی‌ها را ندارید.", intent }
        }
        const globals = checkedGlobals(form)
        const channelPermissions = globals.includes("channels.view")
          ? viewOnlyGrants(form, "channel_ids")
          : []
        const categoryPermissions = globals.includes("categories.view")
          ? viewOnlyGrants(form, "category_ids")
          : []
        await apiRequest<unknown>(
          "PUT",
          `${userPath(id as number)}/permissions`,
          {
            token,
            body: {
              global_permissions: globals,
              channel_permissions: channelPermissions,
              category_permissions: categoryPermissions,
            },
          }
        )
        return redirect(strip(requestURL(request).pathname))
      }
    }
  } catch (error) {
    if (error instanceof ApiError) {
      if (error.status === 401) throw loginRedirect(request)
      if (intent === "permissions-load") {
        return { permissionsError: error.message }
      }
      if (error.isForbidden) {
        return { error: "شما مجوز انجام این عمل را ندارید.", intent, values }
      }
      // 409: a duplicate username, or "the last active administrator".
      return { error: error.message, intent, values }
    }
    if (error instanceof SyntaxError) {
      return { error: "داده دسترسی نامعتبر است.", intent }
    }
    if (intent === "permissions-load") {
      return { permissionsError: "بارگذاری دسترسی‌ها ناموفق بود." }
    }
    return { error: "عملیات انجام نشد.", intent, values }
  }
}

export const meta = () => pageMeta("مدیرها")

export const handle = section("مدیرها")

type DialogState =
  | { kind: "create" }
  | { kind: "edit"; user: User }
  | { kind: "permissions"; user: User }
  | null

export function ErrorBoundary({ error }: Route.ErrorBoundaryProps) {
  return (
    <PageShell title="مدیرها">
      <PageError error={error} />
    </PageShell>
  )
}

export default function Administrators({
  loaderData,
  actionData,
}: Route.ComponentProps) {
  const navigation = useNavigation()
  const submit = useSubmit()
  const [searchParams, setSearchParams] = useSearchParams()
  const submitting = navigation.state === "submitting"
  const loading = navigation.state === "loading"

  const [dialog, setDialog] = React.useState<DialogState>(null)
  const [deleting, setDeleting] = React.useState<User | null>(null)

  const initialQ = loaderData.status === "ok" ? loaderData.q : ""
  const [q, setQ] = React.useState(initialQ)
  React.useEffect(() => setQ(initialQ), [initialQ])

  // Reopen the dialog that produced an error, so the message appears in it.
  React.useEffect(() => {
    if (!actionData?.error) return
    const id = Number(actionData.values?.id)
    const known =
      loaderData.status === "ok"
        ? loaderData.users.find((u) => u.id === id)
        : undefined
    if (actionData.intent === "create") {
      setDialog({ kind: "create" })
    } else if (
      known &&
      (actionData.intent === "update" || actionData.intent === "permissions")
    ) {
      setDialog({
        kind: actionData.intent === "update" ? "edit" : "permissions",
        user: known,
      })
    }
  }, [actionData, loaderData])

  const wasSubmitting = React.useRef(false)
  React.useEffect(() => {
    // Only page-level submits (the dialog's own Save) close it; the fetcher's
    // permission loads never move the page navigation.
    if (wasSubmitting.current && !submitting && !actionData?.error) {
      setDialog(null)
      setDeleting(null)
    }
    wasSubmitting.current = submitting
  }, [submitting, actionData])

  const onSearch = (event: React.FormEvent<HTMLFormElement>) => {
    event.preventDefault()
    const next = new URLSearchParams(searchParams)
    if (q.trim()) next.set("q", q.trim())
    else next.delete("q")
    next.delete("page")
    setSearchParams(next)
  }

  const dialogError =
    dialog !== null && actionData?.error && actionData.intent === dialog.kind
      ? actionData.error
      : undefined
  const deleteError =
    deleting && actionData?.intent === "delete" ? actionData.error : null

  return (
    <div className="flex flex-col gap-6">
      <PageHeader
        title="مدیرها"
        description="حساب‌های مدیریتی پنل و دسترسی‌هایشان. هر مدیر با نام کاربری یکتا وارد می‌شود."
        actions={
          loaderData.status === "ok" && loaderData.canManage ? (
            <Button onClick={() => setDialog({ kind: "create" })}>
              <PlusIcon data-icon="inline-start" />
              مدیر جدید
            </Button>
          ) : null
        }
      />

      {loaderData.status !== "ok" ? (
        <ErrorState
          title={
            loaderData.status === "forbidden" ? "دسترسی مجاز نیست" : undefined
          }
          message={loaderData.message}
          onRetry={() => window.location.reload()}
        />
      ) : (
        <>
          <form
            onSubmit={onSearch}
            className="flex flex-wrap items-center gap-2"
            role="search"
          >
            <SearchInput
              name="q"
              value={q}
              onChange={setQ}
              placeholder="جستجوی مدیر…"
              className="w-full sm:w-72"
            />
            <Button type="submit" variant="outline" size="sm">
              جستجو
            </Button>
            {loaderData.q && (
              <Button
                type="button"
                variant="ghost"
                size="sm"
                onClick={() => {
                  setQ("")
                  setSearchParams({})
                }}
              >
                حذف جستجو
              </Button>
            )}
          </form>

          <div className="rounded-xl border bg-card">
            {loading ? (
              <LoadingState label="در حال بارگذاری مدیرها…" />
            ) : loaderData.users.length === 0 ? (
              <EmptyState
                icon={UserCogIcon}
                title={
                  loaderData.q ? "نتیجه‌ای پیدا نشد" : "هنوز مدیری ثبت نشده است"
                }
                description={
                  loaderData.q
                    ? "عبارت جستجو را تغییر دهید یا حذف کنید."
                    : "با دکمه «مدیر جدید» اولین حساب مدیریتی را بسازید."
                }
              />
            ) : (
              <div className="overflow-x-auto">
                <table className="w-full text-sm">
                  <thead>
                    <tr className="border-b text-start text-xs text-muted-foreground">
                      <th
                        scope="col"
                        className="h-10 px-3 text-start font-medium"
                      >
                        نام کامل
                      </th>
                      <th
                        scope="col"
                        className="h-10 px-3 text-start font-medium"
                      >
                        نام کاربری
                      </th>
                      <th
                        scope="col"
                        className="h-10 px-3 text-start font-medium"
                      >
                        وضعیت
                      </th>
                      <th
                        scope="col"
                        className="h-10 px-3 text-start font-medium"
                      >
                        تاریخ ایجاد
                      </th>
                      <th scope="col" className="h-10 w-12 px-3">
                        <span className="sr-only">عملیات</span>
                      </th>
                    </tr>
                  </thead>
                  <tbody>
                    {loaderData.users.map((user) => (
                      <tr
                        key={user.id}
                        className="border-b last:border-0 hover:bg-muted/50"
                      >
                        <td className="px-3 py-3 font-medium">
                          {user.fullname}
                        </td>
                        <td
                          className="px-3 py-3 text-muted-foreground"
                          dir="ltr"
                        >
                          {user.username}
                        </td>
                        <td className="px-3 py-3">
                          {user.is_active ? (
                            <Badge variant="success">فعال</Badge>
                          ) : (
                            <Badge variant="muted">غیرفعال</Badge>
                          )}
                        </td>
                        <td className="px-3 py-3 text-muted-foreground">
                          {faDateTime(user.created_at)}
                        </td>
                        <td className="px-3 py-3 text-end">
                          {loaderData.canManage && (
                            <UserMenu
                              user={user}
                              self={user.id === loaderData.meId}
                              onEdit={() => setDialog({ kind: "edit", user })}
                              onPermissions={() =>
                                setDialog({ kind: "permissions", user })
                              }
                              onDelete={() => setDeleting(user)}
                            />
                          )}
                        </td>
                      </tr>
                    ))}
                  </tbody>
                </table>
              </div>
            )}
          </div>

          <nav
            className="flex items-center justify-between"
            aria-label="صفحه‌بندی"
          >
            <p className="text-xs text-muted-foreground">
              صفحه {faDigits(loaderData.currentPage)}
            </p>
            <div className="flex items-center gap-2">
              <PageLink
                params={searchParams}
                target={loaderData.currentPage - 1}
                enabled={loaderData.currentPage > 1 && !loading}
              >
                صفحه قبل
              </PageLink>
              <PageLink
                params={searchParams}
                target={loaderData.currentPage + 1}
                enabled={loaderData.hasMore && !loading}
              >
                صفحه بعد
              </PageLink>
            </div>
          </nav>
        </>
      )}

      {dialog?.kind === "create" || dialog?.kind === "edit" ? (
        <UserDialog
          key={dialog.kind === "edit" ? `edit-${dialog.user.id}` : "create"}
          user={dialog.kind === "edit" ? dialog.user : null}
          error={dialogError}
          fieldErrors={actionData?.error ? actionData.fieldErrors : undefined}
          pending={submitting}
          defaultValues={actionData?.values}
          onClose={() => setDialog(null)}
          onSubmit={(values) => submit(values, { method: "post" })}
        />
      ) : null}

      {dialog?.kind === "permissions" ? (
        <PermissionsDialog
          key={`permissions-${dialog.user.id}`}
          user={dialog.user}
          error={dialogError}
          onClose={() => setDialog(null)}
          onSave={(form) => submit(form, { method: "post" })}
        />
      ) : null}

      <ConfirmDialog
        open={deleting !== null}
        onOpenChange={(open) => !open && setDeleting(null)}
        title="حذف مدیر"
        description={
          deleting
            ? `حساب «${deleting.fullname}» (${deleting.username}) حذف شود؟ اگر آخرین مدیر فعال باشد، سرور حذف را رد می‌کند و پیام آن همین‌جا نمایش داده می‌شود.`
            : ""
        }
        confirmLabel="حذف"
        pending={submitting}
        error={deleteError}
        onConfirm={() => {
          if (!deleting) return
          submit(
            { intent: "delete", id: String(deleting.id) },
            { method: "post" }
          )
        }}
      />
    </div>
  )
}

function UserMenu({
  user,
  self,
  onEdit,
  onPermissions,
  onDelete,
}: {
  user: User
  self: boolean
  onEdit: () => void
  onPermissions: () => void
  onDelete: () => void
}) {
  return (
    <DropdownMenu>
      <DropdownMenuTrigger
        render={
          <Button
            variant="ghost"
            size="icon-sm"
            aria-label={`عملیات ${user.fullname}`}
          />
        }
      >
        <MoreHorizontalIcon />
      </DropdownMenuTrigger>
      <DropdownMenuContent align="end">
        <DropdownMenuItem onClick={onEdit}>ویرایش</DropdownMenuItem>
        <DropdownMenuItem
          disabled={self}
          title={
            self ? "نمی‌توانید دسترسی‌های خودتان را تغییر دهید." : undefined
          }
          onClick={self ? undefined : onPermissions}
        >
          دسترسی‌ها
        </DropdownMenuItem>
        <DropdownMenuSeparator />
        <DropdownMenuItem
          variant="destructive"
          disabled={self}
          onClick={self ? undefined : onDelete}
        >
          حذف
        </DropdownMenuItem>
      </DropdownMenuContent>
    </DropdownMenu>
  )
}

function PageLink({
  params,
  target,
  enabled,
  children,
}: {
  params: URLSearchParams
  target: number
  enabled: boolean
  children: React.ReactNode
}) {
  const next = new URLSearchParams(params)
  if (target <= 1) next.delete("page")
  else next.set("page", String(target))
  const query = next.toString()
  const to = query ? `?${query}` : "?"

  return (
    <Button
      variant="outline"
      size="sm"
      disabled={!enabled}
      render={enabled ? <Link to={to} preventScrollReset /> : undefined}
    >
      {children}
    </Button>
  )
}

// ActiveSwitch is a role="switch" Button toggle: the only control the field
// list allows for a boolean (no switch component is available here).
function ActiveSwitch({
  checked,
  onChange,
  id,
}: {
  checked: boolean
  onChange: (next: boolean) => void
  id: string
}) {
  return (
    <div className="flex items-center gap-2">
      <button
        id={id}
        type="button"
        role="switch"
        aria-checked={checked}
        onClick={() => onChange(!checked)}
        className={`relative inline-flex h-5 w-9 shrink-0 items-center rounded-full border transition-colors outline-none focus-visible:ring-3 focus-visible:ring-ring/50 ${
          checked ? "border-primary bg-primary" : "border-input bg-muted"
        }`}
      >
        <span
          aria-hidden="true"
          className={`block size-3.5 rounded-full bg-background shadow transition-transform ${
            checked
              ? "translate-x-4.5 rtl:-translate-x-4.5"
              : "translate-x-0.5 rtl:-translate-x-0.5"
          }`}
        />
      </button>
      <span className="text-sm">{checked ? "فعال" : "غیرفعال"}</span>
    </div>
  )
}

function UserDialog({
  user,
  error,
  fieldErrors,
  pending,
  defaultValues,
  onClose,
  onSubmit,
}: {
  user: User | null
  error?: string
  fieldErrors?: FieldErrors
  pending: boolean
  defaultValues?: AdminActionData["values"]
  onClose: () => void
  onSubmit: (values: Record<string, string>) => void
}) {
  const [fullname, setFullname] = React.useState(
    user?.fullname ?? defaultValues?.fullname ?? ""
  )
  const [username, setUsername] = React.useState(
    user?.username ?? defaultValues?.username ?? ""
  )
  const [password, setPassword] = React.useState("")
  const [isActive, setIsActive] = React.useState(
    user
      ? user.is_active
      : defaultValues?.is_active
        ? defaultValues.is_active === "true"
        : true
  )
  const [touched, setTouched] = React.useState(false)

  const clientErrors: FieldErrors = {}
  if (touched) {
    if (!fullname.trim()) clientErrors.fullname = "نام کامل را وارد کنید."
    if (!username.trim()) clientErrors.username = "نام کاربری را وارد کنید."
    if (!user && password.length < 8)
      clientErrors.password = "رمز عبور باید دست‌کم ۸ نویسه باشد."
    if (user && password && password.length < 8)
      clientErrors.password = "رمز عبور باید دست‌کم ۸ نویسه باشد."
  }
  const errors = { ...fieldErrors, ...clientErrors }

  return (
    <Dialog open onOpenChange={(open) => !open && !pending && onClose()}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>{user ? "ویرایش مدیر" : "مدیر جدید"}</DialogTitle>
          <DialogDescription>
            {user
              ? "برای نگه‌داشتن رمز فعلی، فیلد رمز را خالی بگذارید."
              : "نام کاربری باید یکتا باشد و رمز عبور دست‌کم ۸ نویسه."}
          </DialogDescription>
        </DialogHeader>
        <form
          className="flex flex-col gap-4"
          noValidate
          onSubmit={(event) => {
            event.preventDefault()
            setTouched(true)
            if (
              !fullname.trim() ||
              !username.trim() ||
              (!user && password.length < 8) ||
              (password && password.length < 8)
            ) {
              return
            }
            onSubmit({
              intent: user ? "update" : "create",
              ...(user ? { id: String(user.id) } : {}),
              fullname: fullname.trim(),
              username: username.trim(),
              password,
              is_active: String(isActive),
            })
          }}
        >
          <Field>
            <FieldLabel htmlFor="admin-fullname">نام کامل</FieldLabel>
            <FieldControl
              id="admin-fullname"
              autoComplete="name"
              value={fullname}
              onChange={(event) => setFullname(event.target.value)}
              onBlur={() => setTouched(true)}
              required
            />
            {errors.fullname && <FieldError>{errors.fullname}</FieldError>}
          </Field>
          <Field>
            <FieldLabel htmlFor="admin-username">نام کاربری</FieldLabel>
            <FieldControl
              id="admin-username"
              dir="ltr"
              autoComplete="username"
              value={username}
              onChange={(event) => setUsername(event.target.value)}
              onBlur={() => setTouched(true)}
              required
            />
            {errors.username && <FieldError>{errors.username}</FieldError>}
          </Field>
          <Field>
            <FieldLabel htmlFor="admin-password">
              {user ? "رمز عبور جدید" : "رمز عبور"}
            </FieldLabel>
            <FieldControl
              id="admin-password"
              type="password"
              dir="ltr"
              autoComplete="new-password"
              value={password}
              onChange={(event) => setPassword(event.target.value)}
              onBlur={() => setTouched(true)}
              required={!user}
            />
            {errors.password ? (
              <FieldError>{errors.password}</FieldError>
            ) : (
              <FieldDescription>
                {user ? "خالی بگذارید تا تغییر نکند." : "دست‌کم ۸ نویسه."}
              </FieldDescription>
            )}
          </Field>
          <Field>
            <FieldLabel htmlFor="admin-active">وضعیت حساب</FieldLabel>
            <ActiveSwitch
              id="admin-active"
              checked={isActive}
              onChange={setIsActive}
            />
            <FieldDescription>
              مدیر غیرفعال نمی‌تواند وارد شود.
            </FieldDescription>
          </Field>
          {error && (
            <p
              role="alert"
              className="rounded-lg bg-destructive/10 px-3 py-2 text-sm text-destructive"
            >
              {error}
            </p>
          )}
          <DialogFooter>
            <Button
              type="button"
              variant="outline"
              onClick={onClose}
              disabled={pending}
            >
              انصراف
            </Button>
            <Button type="submit" disabled={pending}>
              {pending ? "در حال ذخیره…" : "ذخیره"}
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  )
}

function PermissionsDialog({
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
  const fetcher = useFetcher<AdminActionData>()
  const navigation = useNavigation()
  const pending = navigation.state === "submitting"
  const [loaded, setLoaded] = React.useState<PermissionsPayload | null>(null)
  const [globals, setGlobals] = React.useState<Set<GlobalPermission>>(new Set())
  const [channelIDs, setChannelIDs] = React.useState<number[]>([])
  const [categoryIDs, setCategoryIDs] = React.useState<number[]>([])

  // Load the current permissions once when the dialog opens via the fetcher.
  const requested = React.useRef(false)
  const retry = () => loadPermissions(fetcher, user.id)
  React.useEffect(() => {
    if (!requested.current) {
      requested.current = true
      loadPermissions(fetcher, user.id)
    }
  }, [fetcher, user.id])

  React.useEffect(() => {
    if (fetcher.data?.permissions) {
      const permissions = fetcher.data.permissions
      setLoaded(permissions)
      setGlobals(withViewDependencies(permissions.global as GlobalPermission[]))
      // An allow row is a picked item when the manager also holds the global
      // view permission; otherwise it is how a manager without it sees these.
      setChannelIDs(pickedIDs(permissions.channels, "channel_id"))
      setCategoryIDs(pickedIDs(permissions.categories, "category_id"))
    }
  }, [fetcher.data])

  const toggle = (value: GlobalPermission) => {
    setGlobals((previous) => {
      const next = new Set(previous)
      if (next.has(value)) next.delete(value)
      else next.add(value)
      return withViewDependencies(next)
    })
  }

  const missingCatalog = fetcher.data && !fetcher.data.permissionsError
  const channels = fetcher.data?.channels
  const categories = fetcher.data?.categories
  const loadError = fetcher.data?.permissionsError

  return (
    <Dialog open onOpenChange={(open) => !open && !pending && onClose()}>
      <DialogContent className="max-w-2xl">
        <DialogHeader>
          <DialogTitle>دسترسی‌های {user.fullname}</DialogTitle>
          <DialogDescription>
            دسترسی‌های عمومی را انتخاب کنید. مدیریت هر بخش نیازمند دسترسی مشاهده
            همان بخش است و خودکار افزوده می‌شود.
          </DialogDescription>
        </DialogHeader>

        {!loaded ? (
          loadError ? (
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
            <LoadingState label="در حال بارگذاری دسترسی‌ها…" />
          )
        ) : (
          <form
            className="flex max-h-[65vh] flex-col gap-5 overflow-y-auto pe-1"
            id={`permissions-form-${user.id}`}
            onSubmit={(event) => {
              event.preventDefault()
              onSave({
                intent: "permissions",
                id: String(user.id),
                global_permissions: JSON.stringify([...globals]),
                channel_ids: JSON.stringify(channelIDs),
                category_ids: JSON.stringify(categoryIDs),
              })
            }}
          >
            <fieldset className="flex flex-col gap-2">
              <legend className="mb-1 text-sm font-medium">
                دسترسی‌های عمومی
              </legend>
              <div className="grid gap-2 sm:grid-cols-2">
                {globalPermissionOptions.map((option) => {
                  const inputId = `perm-${user.id}-${option.value.replace(".", "-")}`
                  return (
                    <label
                      key={option.value}
                      htmlFor={inputId}
                      className="flex cursor-pointer items-start gap-2 rounded-lg border px-3 py-2 hover:bg-muted/50"
                    >
                      <input
                        id={inputId}
                        type="checkbox"
                        name="global_permissions"
                        value={option.value}
                        checked={globals.has(option.value)}
                        disabled={
                          manageNeedsView[option.value] !== undefined &&
                          !globals.has(manageNeedsView[option.value]!)
                        }
                        onChange={() => toggle(option.value)}
                        className="mt-0.5 size-4 accent-primary"
                      />
                      <span className="text-sm">{option.label}</span>
                    </label>
                  )
                })}
              </div>
            </fieldset>

            {globals.has("channels.view") && (
              <PermissionPicker
                title="کانال‌هایی که این مدیر می‌بیند"
                emptyText={
                  missingCatalog
                    ? "فهرست کانال‌ها در دسترس نیست."
                    : "کانالی یافت نشد."
                }
                placeholder="همه کانال‌ها"
                items={channels ?? []}
                value={channelIDs}
                onValueChange={setChannelIDs}
                labelOf={(channel) => channel.title || channel.client_key}
              />
            )}
            {globals.has("categories.view") && (
              <PermissionPicker
                title="دسته‌بندی‌هایی که این مدیر می‌بیند"
                emptyText={
                  missingCatalog
                    ? "فهرست دسته‌بندی‌ها در دسترس نیست."
                    : "دسته‌بندی‌ای یافت نشد."
                }
                placeholder="همه دسته‌بندی‌ها"
                items={categories ?? []}
                value={categoryIDs}
                onValueChange={setCategoryIDs}
                labelOf={(category) => category.name}
              />
            )}

            {error && (
              <p
                role="alert"
                className="rounded-lg bg-destructive/10 px-3 py-2 text-sm text-destructive"
              >
                {error}
              </p>
            )}

            <DialogFooter>
              <Button
                type="button"
                variant="outline"
                onClick={onClose}
                disabled={pending}
              >
                انصراف
              </Button>
              <Button type="submit" disabled={pending}>
                {pending ? "در حال ذخیره…" : "ذخیره دسترسی‌ها"}
              </Button>
            </DialogFooter>
          </form>
        )}
      </DialogContent>
    </Dialog>
  )
}

// PermissionPicker is a multi-select of the items a manager may see. Nothing
// picked means every item; the picks are the exception list. Typing in its
function PermissionPicker<T extends { id: number }>({
  title,
  placeholder,
  emptyText,
  items,
  value,
  onValueChange,
  labelOf,
}: {
  title: string
  placeholder: string
  emptyText: string
  items: T[]
  value: number[]
  onValueChange: (ids: number[]) => void
  labelOf: (item: T) => string
}) {
  return (
    <MultiPicker
      id={`permission-picker-${title}`}
      title={title}
      description="خالی بگذارید تا به همه دسترسی داشته باشد؛ هر مورد انتخاب‌شده یعنی دسترسی فقط به همان موارد."
      placeholder={placeholder}
      emptyText={emptyText}
      items={items}
      value={value}
      onValueChange={onValueChange}
      labelOf={labelOf}
    />
  )
}

// pickOrNull resolves a catalog listing to its rows, or null when it could not
// be loaded: the dialog then shows its picker empty rather than failing to
// open because a side list did not arrive.
async function pickOrNull<T>(listing: Promise<{ rows: T[] }>) {
  try {
    return (await listing).rows
  } catch {
    return null
  }
}

// pickedIDs turns stored per-resource rows into picker ids: the rows that allow
// view. Saving rewrites the picker's rows with view alone, which is what this
// dialog edits.
function pickedIDs(
  rows: ResourcePermissions[],
  idKey: "channel_id" | "category_id"
) {
  const out: number[] = []
  for (const row of rows) {
    const id = row[idKey]
    if (id && row.view === "allow") out.push(id)
  }
  return out
}

// loadPermissions asks this route's action (through a fetcher, without leaving
// the page) for the user's current permissions.
function loadPermissions(
  // The fetcher's submit signature is identical on the client and server
  // variants; a narrow structural type avoids depending on either.
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
