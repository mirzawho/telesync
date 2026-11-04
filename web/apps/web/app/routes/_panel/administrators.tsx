import {
  Link,
  redirect,
  useNavigation,
  useSearchParams,
  useSubmit,
} from "react-router"
import { MoreHorizontalIcon, PlusIcon, UserCogIcon } from "lucide-react"
import * as React from "react"

import {
  ChangeAdministratorPasswordDialog,
  CreateAdministratorDialog,
  EditAdministratorDialog,
} from "@/components/administrators/administrator-dialogs"
import { AdministratorPermissionsDialog } from "@/components/administrators/permissions-dialog"
import { ConfirmDialog } from "@/components/confirm-dialog"
import { PageHeader } from "@/components/page-header"
import { PageError, PageShell, pageMeta, section } from "@/lib/page"
import { faDateTime, faDigits } from "@/components/persian-digits"
import { SearchInput } from "@/components/search-input"
import { EmptyState, ErrorState, LoadingState } from "@/components/states"
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
import { useToast } from "@workspace/ui/components/toast"

import { ApiError, apiRequest } from "@/lib/api"
import {
  catalogFromForm,
  draftFromForm,
  resourceGrants,
  type PermissionsPayload,
} from "@/lib/admin-permissions"
import {
  createUserBody,
  describeWriteFailure,
  passwordBody,
  updateUserBody,
  type AdministratorFieldErrors,
} from "@/lib/admin-users"
import { LOGIN_REDIRECT_TO, listAll, requireSession } from "@/lib/session"
import { strip, requestURL } from "@/path"
import type {
  Category,
  Channel,
  GlobalPermission,
  User,
  WordPressSite,
} from "@/lib/types"

import type { Route } from "./+types/administrators"

const PAGE_SIZE = 20

interface UserList {
  users: User[]
}

interface UserResponse {
  user: User
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

interface AdminActionData {
  error?: string
  intent?: string
  values?: {
    id?: string
    fullname?: string
    username?: string
    is_active?: string
  }
  fieldErrors?: AdministratorFieldErrors
  // Successful permission load, served through the permissions fetcher.
  permissions?: PermissionsPayload
  permissionsError?: string
  // The catalog the pickers choose from; null when it could not be loaded or
  // was not asked for.
  channels?: Channel[] | null
  categories?: Category[] | null
  // The WordPress sites this administrator may see. Unlike the other two there
  // is no /auth/me access flag for them, so the listing itself decides (it
  // answers 403 when none is visible) and null means "not loaded".
  sites?: WordPressSite[] | null
}

const intents = new Set([
  "create",
  "update",
  "delete",
  "password",
  "permissions",
  "permissions-load",
])

function userPath(id: number) {
  return `/local/users/${id}`
}

function parseId(raw: FormDataEntryValue | null): number | null {
  const value = Number(raw)
  return Number.isInteger(value) && value > 0 ? value : null
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
        const fieldErrors: AdministratorFieldErrors = {}
        if (!fullname) fieldErrors.fullname = "نام مستعار الزامی است."
        if (!username) fieldErrors.username = "نام کاربری الزامی است."
        if (password.length < 8)
          fieldErrors.password = "رمز عبور باید دست‌کم ۸ نویسه باشد."
        if (Object.keys(fieldErrors).length > 0) {
          return { error: "فرم را کامل کنید.", intent, values, fieldErrors }
        }
        // The body always carries is_active: a new account is created active
        // and the dialog offers no other choice.
        await apiRequest<UserResponse>("POST", "/local/users", {
          token,
          body: createUserBody({
            fullname,
            username,
            password,
          }),
        })
        return redirect(strip(requestURL(request).pathname))
      }
      case "update": {
        const fieldErrors: AdministratorFieldErrors = {}
        if (!fullname) fieldErrors.fullname = "نام مستعار الزامی است."
        if (!username) fieldErrors.username = "نام کاربری الزامی است."
        if (Object.keys(fieldErrors).length > 0) {
          return { error: "فرم را کامل کنید.", intent, values, fieldErrors }
        }
        // No password here, ever: an empty password field in a profile body is
        // not "leave it alone", and the password has its own intent below.
        await apiRequest<UserResponse>("PUT", userPath(id as number), {
          token,
          body: updateUserBody({ fullname, username, is_active: isActive }),
        })
        return redirect(strip(requestURL(request).pathname))
      }
      case "password": {
        const fieldErrors: AdministratorFieldErrors = {}
        if (password.length < 8)
          fieldErrors.password = "رمز عبور باید دست‌کم ۸ نویسه باشد."
        if (Object.keys(fieldErrors).length > 0) {
          return { error: "فرم را کامل کنید.", intent, values, fieldErrors }
        }
        // The API has no password route: PUT with the password and nothing else
        // is the password change, and leaves every other field untouched.
        await apiRequest<UserResponse>("PUT", userPath(id as number), {
          token,
          body: passwordBody(password),
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
        const permissions: PermissionsPayload = {
          global: data.user.permissions?.global ?? [],
          channels: data.user.permissions?.channels ?? [],
          categories: data.user.permissions?.categories ?? [],
          // Omitted when the shape carried no field, so a save that ran against
          // such a shape posts no site list at all rather than revoking them.
          wordpress: data.user.permissions?.wordpress,
        }
        // The pickers need the whole catalog, not just the rows the subject may
        // see. Loaded only when the dialog can show a picker at all.
        const canPickChannels =
          me.access?.channels || permissions.channels.length > 0
        const canPickCategories =
          me.access?.categories || permissions.categories.length > 0
        // WordPress sits behind no /auth/me access flag: the listing itself is
        // the only answer to "may this administrator see sites". A refusal
        // (403, or an unconfigured store) becomes a null catalog and the save
        // then keeps whatever is stored.
        const [channels, categories, sites] = await Promise.all([
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
          // The collection is not paginated: GET /local/wordpress answers every
          // visible site and refuses page/per_page as unknown query parameters,
          // so listAll must not be used here.
          pickOrNull(
            apiRequest<{ sites: WordPressSite[] }>("GET", "/local/wordpress", {
              token,
            }).then((result) => ({ rows: result.data.sites ?? [] }))
          ),
        ])
        return {
          permissions,
          channels,
          categories,
          sites,
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
        const draft = draftFromForm(form)
        const body: Record<string, unknown> = {
          global_permissions: draft.globals,
          channel_permissions: resourceGrants(
            draft,
            "channels",
            catalogFromForm(form, "channels")
          ),
          category_permissions: resourceGrants(
            draft,
            "categories",
            catalogFromForm(form, "categories")
          ),
        }
        // WordPress is optional on the PUT: `wordpress_permissions` present
        // replaces the stored per-site grants (even as []), absent keeps them.
        // The form posts `site_ids` only when it has a per-site shape to edit.
        if (draft.sitesEditable) {
          body.wordpress_permissions = resourceGrants(
            draft,
            "wordpress",
            catalogFromForm(form, "wordpress")
          )
        }
        await apiRequest<unknown>(
          "PUT",
          `${userPath(id as number)}/permissions`,
          {
            token,
            body,
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
      // A refused write says why it refused. The two this page can cause are
      // phrased for the field they belong to — a taken username goes on the
      // username field, not into a box above the form.
      return { ...describeWriteFailure(intent, error.message), intent, values }
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
  | { kind: "password"; user: User }
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
  const { toast } = useToast()
  const submit = useSubmit()
  const [searchParams, setSearchParams] = useSearchParams()
  const submitting = navigation.state === "submitting"
  const loading = navigation.state === "loading"
  // The intent of the page-level submit in flight, read back when it finishes
  // to say what was saved. Successful actions answer with a redirect, so the
  // result itself carries no success message.
  const pendingIntent = React.useRef<string | null>(null)
  React.useEffect(() => {
    const formData = navigation.formData
    if (formData) {
      pendingIntent.current = String(formData.get("intent") ?? "")
    }
  }, [navigation.formData])

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
    } else if (known) {
      if (actionData.intent === "update")
        setDialog({ kind: "edit", user: known })
      else if (actionData.intent === "password")
        setDialog({ kind: "password", user: known })
      else if (actionData.intent === "permissions")
        setDialog({ kind: "permissions", user: known })
    }
  }, [actionData, loaderData])

  const wasSubmitting = React.useRef(false)
  React.useEffect(() => {
    // Only page-level submits (the dialog's own Save) close it; the fetcher's
    // permission loads never move the page navigation.
    if (wasSubmitting.current && !submitting && !actionData?.error) {
      setDialog(null)
      setDeleting(null)
      // A redirect has no payload, so the finished intent names the result.
      const intent = pendingIntent.current
      if (intent === "create")
        toast({ variant: "success", title: "مدیر جدید ساخته شد." })
      else if (intent === "update")
        toast({ variant: "success", title: "تغییرات مدیر ذخیره شد." })
      else if (intent === "password")
        toast({ variant: "success", title: "رمز عبور مدیر تغییر کرد." })
      else if (intent === "delete")
        toast({ variant: "success", title: "مدیر حذف شد." })
      else if (intent === "permissions")
        toast({ variant: "success", title: "دسترسی‌ها ذخیره شد." })
      pendingIntent.current = null
    }
    wasSubmitting.current = submitting
  }, [submitting, actionData, toast])

  // Every refusal is also a toast, so it is read without hunting for the open
  // dialog; the form keeps showing the message where the work was done.
  const toastedError = React.useRef<string | null>(null)
  React.useEffect(() => {
    if (!actionData?.error) return
    const key = `${actionData.intent ?? ""}:${actionData.error}`
    if (toastedError.current === key) return
    toastedError.current = key
    toast({
      variant: "error",
      title: "عملیات انجام نشد.",
      description: actionData.error,
    })
  }, [actionData, toast])

  const onSearch = (event: React.FormEvent<HTMLFormElement>) => {
    event.preventDefault()
    const next = new URLSearchParams(searchParams)
    if (q.trim()) next.set("q", q.trim())
    else next.delete("q")
    next.delete("page")
    setSearchParams(next)
  }

  // The action names what was attempted and the dialog state names what is
  // open, and the two are not spelled alike: the edit dialog's intent is
  // "update". Matching them by string equality left that dialog showing a
  // refused save as a bare toast with an empty form, so the intent is mapped.
  const dialogKindFor: Record<string, string> = {
    create: "create",
    update: "edit",
    password: "password",
    permissions: "permissions",
  }
  const dialogError =
    dialog !== null &&
    actionData?.error &&
    dialogKindFor[actionData.intent ?? ""] === dialog.kind
      ? actionData.error
      : undefined
  const deleteError =
    deleting && actionData?.intent === "delete" ? actionData.error : null
  const meId = loaderData.status === "ok" ? loaderData.meId : 0

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
              <AdministratorsTable
                users={loaderData.users}
                canManage={loaderData.canManage}
                meId={meId}
                onEdit={(user) => setDialog({ kind: "edit", user })}
                onPassword={(user) => setDialog({ kind: "password", user })}
                onPermissions={(user) =>
                  setDialog({ kind: "permissions", user })
                }
                onDelete={setDeleting}
              />
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

      {dialog?.kind === "create" ? (
        <CreateAdministratorDialog
          key="create"
          open
          onOpenChange={(open) => !open && setDialog(null)}
          error={dialogError}
          fieldErrors={actionData?.error ? actionData.fieldErrors : undefined}
          pending={submitting}
          defaultValues={
            actionData?.intent === "create" ? actionData.values : undefined
          }
          onClose={() => setDialog(null)}
          onSubmit={(values) =>
            submit({ intent: "create", ...values }, { method: "post" })
          }
        />
      ) : null}

      {dialog?.kind === "edit" ? (
        <EditAdministratorDialog
          key={`edit-${dialog.user.id}`}
          open
          onOpenChange={(open) => !open && setDialog(null)}
          user={dialog.user}
          self={dialog.user.id === meId}
          error={dialogError}
          fieldErrors={actionData?.error ? actionData.fieldErrors : undefined}
          pending={submitting}
          defaultValues={
            actionData?.intent === "update" ? actionData.values : undefined
          }
          onClose={() => setDialog(null)}
          onSubmit={(values) =>
            submit(
              { intent: "update", id: String(dialog.user.id), ...values },
              { method: "post" }
            )
          }
        />
      ) : null}

      {dialog?.kind === "password" ? (
        <ChangeAdministratorPasswordDialog
          key={`password-${dialog.user.id}`}
          open
          onOpenChange={(open) => !open && setDialog(null)}
          user={dialog.user}
          error={dialogError}
          pending={submitting}
          onClose={() => setDialog(null)}
          onSubmit={(password) =>
            submit(
              { intent: "password", id: String(dialog.user.id), password },
              { method: "post" }
            )
          }
        />
      ) : null}

      {dialog?.kind === "permissions" ? (
        <AdministratorPermissionsDialog
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

// Physical left, not RTL start: the actions column stays pinned while the
// table scrolls under it, and bg-background keeps text from showing through.
const ACTIONS_HEAD = "sticky left-0 z-20 w-12 bg-background text-end"
const ACTIONS_CELL = "sticky left-0 z-10 bg-background text-end"

// Single-line cells keep row heights even; the shared Table's container scrolls
// instead of the page.
export function AdministratorsTable({
  users,
  canManage,
  meId,
  onEdit,
  onPassword,
  onPermissions,
  onDelete,
}: {
  users: User[]
  canManage: boolean
  meId: number
  onEdit: (user: User) => void
  onPassword: (user: User) => void
  onPermissions: (user: User) => void
  onDelete: (user: User) => void
}) {
  return (
    <Table>
      <TableHeader>
        <TableRow>
          <TableHead>نام مستعار</TableHead>
          <TableHead>نام کاربری</TableHead>
          <TableHead>وضعیت</TableHead>
          <TableHead>تاریخ ایجاد</TableHead>
          <TableHead className={ACTIONS_HEAD}>
            <span className="sr-only">عملیات</span>
          </TableHead>
        </TableRow>
      </TableHeader>
      <TableBody>
        {users.map((user) => (
          <TableRow key={user.id}>
            <TableCell className="font-medium">{user.fullname}</TableCell>
            <TableCell className="text-muted-foreground" dir="ltr">
              {user.username}
            </TableCell>
            <TableCell>
              {user.is_active ? (
                <Badge variant="success">فعال</Badge>
              ) : (
                <Badge variant="muted">غیرفعال</Badge>
              )}
            </TableCell>
            <TableCell className="text-muted-foreground">
              {faDateTime(user.created_at)}
            </TableCell>
            <TableCell className={ACTIONS_CELL}>
              {canManage && (
                <UserMenu
                  user={user}
                  self={user.id === meId}
                  onEdit={() => onEdit(user)}
                  onPassword={() => onPassword(user)}
                  onPermissions={() => onPermissions(user)}
                  onDelete={() => onDelete(user)}
                />
              )}
            </TableCell>
          </TableRow>
        ))}
      </TableBody>
    </Table>
  )
}

function UserMenu({
  user,
  self,
  onEdit,
  onPassword,
  onPermissions,
  onDelete,
}: {
  user: User
  self: boolean
  onEdit: () => void
  onPassword: () => void
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
        <DropdownMenuItem onClick={onPassword}>تغییر رمز عبور</DropdownMenuItem>
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
