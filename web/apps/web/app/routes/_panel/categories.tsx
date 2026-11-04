import {
  Link,
  redirect,
  useNavigation,
  useSearchParams,
  useSubmit,
} from "react-router"
import { FolderTreeIcon, MoreHorizontalIcon, PlusIcon } from "lucide-react"
import * as React from "react"

import { ConfirmDialog } from "@/components/confirm-dialog"
import { PageHeader } from "@/components/page-header"
import { PageError, PageShell, pageMeta, section } from "@/lib/page"
import { faDateTime, faDigits } from "@/components/persian-digits"
import { SearchInput } from "@/components/search-input"
import { EmptyState, ErrorState, LoadingState } from "@/components/states"
import { SubmitButton } from "@/components/submit-button"
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
import { useToast } from "@workspace/ui/components/toast"
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@workspace/ui/components/table"

import { ApiError, apiRequest } from "@/lib/api"
import { LOGIN_REDIRECT_TO, requireSession } from "@/lib/session"
import { strip, requestURL } from "@/path"
import type { Category } from "@/lib/types"

import type { Route } from "./+types/categories"

const PAGE_SIZE = 20

interface CategoryList {
  categories: Category[]
}

// ChannelOption is one managed channel, as the channel dialog names it.

type PageTitle = { title: string }

type LoaderData =
  | {
      status: "ok"
      categories: Category[]
      currentPage: number
      hasMore: boolean
      q: string
      canManage: boolean
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
  // `page` is the loader key the layout's header reads for the breadcrumb title.
  const page: PageTitle = { title: "دسته‌بندی‌ها" }

  try {
    const { data, meta } = await apiRequest<CategoryList>(
      "GET",
      "/local/categories",
      {
        token,
        // Only the documented query parameters: an unknown one is a 400.
        query: { page: currentPage, per_page: PAGE_SIZE, q: q || undefined },
      }
    )
    return {
      status: "ok",
      categories: data.categories ?? [],
      currentPage,
      hasMore: meta.has_more ?? false,
      q,
      canManage: Boolean(me.access?.categories),
      page,
    }
  } catch (error) {
    if (error instanceof ApiError) {
      if (error.status === 401) throw loginRedirect(request)
      if (error.isForbidden) {
        return {
          status: "forbidden",
          message: "شما مجوز مشاهده دسته‌بندی‌ها را ندارید.",
          page,
        }
      }
      return { status: "error", message: error.message, page }
    }
    return {
      status: "error",
      message: "بارگذاری دسته‌بندی‌ها ناموفق بود.",
      page,
    }
  }
}

interface FormErrors {
  name?: string
  description?: string
}

interface CategoryActionData {
  error?: string
  intent?: string
  // Echoed back so the dialog reopens with what the user typed.
  values?: { id?: string; name?: string; description?: string }
  fieldErrors?: FormErrors
}

const intents = new Set(["create", "update", "delete"])

export async function clientAction({
  request,
  context,
}: Route.ActionArgs): Promise<CategoryActionData | Response> {
  const { token } = await requireSession(request)
  const form = await request.formData()
  const intent = String(form.get("intent") ?? "")
  const rawId = String(form.get("id") ?? "")
  const name = String(form.get("name") ?? "").trim()
  const description = String(form.get("description") ?? "").trim()
  const values = { id: rawId || undefined, name, description }

  if (!intents.has(intent)) {
    return { error: "درخواست نامعتبر است.", intent, values }
  }
  const id = Number(rawId)
  if (intent !== "create" && (!rawId || !Number.isInteger(id) || id <= 0)) {
    return { error: "شناسه دسته‌بندی نامعتبر است.", intent, values }
  }

  try {
    let message = "ذخیره شد."
    if (intent === "delete") {
      await apiRequest<{ deleted: boolean; id: number }>(
        "DELETE",
        `/local/categories/${id}`,
        { token }
      )
      message = "دسته‌بندی حذف شد."
    } else {
      if (!name) {
        return {
          error: "نام دسته‌بندی الزامی است.",
          intent,
          values,
          fieldErrors: { name: "نام الزامی است." },
        }
      }
      // PUT replaces the whole record: an empty description clears it.
      const body = { name, description }
      if (intent === "create") {
        await apiRequest<{ category: Category }>("POST", "/local/categories", {
          token,
          body,
        })
        message = "دسته‌بندی ساخته شد."
      } else {
        await apiRequest<{ category: Category }>(
          "PUT",
          `/local/categories/${id}`,
          { token, body }
        )
        message = "تغییرات ذخیره شد."
      }
    }
    // Redirecting reloads the list from the API; the dialog closes when this
    // navigation finishes (see the submitting→idle effect above).
    return redirect(
      strip(`${requestURL(request).pathname}${requestURL(request).search}`)
    )
  } catch (error) {
    if (error instanceof ApiError) {
      if (error.status === 401) throw loginRedirect(request)
      if (error.isForbidden) {
        return { error: "شما مجوز این عمل را ندارید.", intent, values }
      }
      if (error.code === "conflict") {
        // e.g. the category is still in use; the server's own message explains why.
        return { error: error.message, intent, values }
      }
      if (error.status === 400 || error.code === "invalid_input") {
        return { error: `ورودی نامعتبر است: ${error.message}`, intent, values }
      }
      return { error: error.message, intent, values }
    }
    return { error: "عملیات انجام نشد.", intent, values }
  }
}

export const meta = () => pageMeta("دسته‌بندی‌ها")

export const handle = section("دسته‌بندی‌ها")

export function ErrorBoundary({ error }: Route.ErrorBoundaryProps) {
  return (
    <PageShell title="دسته‌بندی‌ها">
      <PageError error={error} />
    </PageShell>
  )
}

// Physical left, not RTL start: the actions column stays pinned while the
// table scrolls under it, and bg-background keeps text from showing through.
const ACTIONS_HEAD = "sticky left-0 z-20 w-12 bg-background text-end"
const ACTIONS_CELL = "sticky left-0 z-10 bg-background text-end"

// Single-line cells keep row heights even; the container scrolls instead of
// the page, so no wrapper is added around the shared Table.
export function CategoriesTable({
  categories,
  canManage,
  onEdit,
  onDelete,
}: {
  categories: Category[]
  canManage: boolean
  onEdit: (category: Category) => void
  onDelete: (category: Category) => void
}) {
  return (
    <Table>
      <TableHeader>
        <TableRow>
          <TableHead>نام</TableHead>
          <TableHead>توضیحات</TableHead>
          <TableHead>تاریخ ایجاد</TableHead>
          <TableHead className={ACTIONS_HEAD}>
            <span className="sr-only">عملیات</span>
          </TableHead>
        </TableRow>
      </TableHeader>
      <TableBody>
        {categories.map((category) => (
          <TableRow key={category.id}>
            <TableCell className="font-medium">{category.name}</TableCell>
            <TableCell
              className="text-muted-foreground"
              title={category.description || undefined}
            >
              {category.description || "—"}
            </TableCell>
            <TableCell className="text-muted-foreground">
              {faDateTime(category.created_at)}
            </TableCell>
            <TableCell className={ACTIONS_CELL}>
              {canManage && (
                <DropdownMenu>
                  <DropdownMenuTrigger
                    render={
                      <Button
                        variant="ghost"
                        size="icon-sm"
                        aria-label={`عملیات دسته‌بندی ${category.name}`}
                      />
                    }
                  >
                    <MoreHorizontalIcon />
                  </DropdownMenuTrigger>
                  <DropdownMenuContent align="end">
                    <DropdownMenuItem onClick={() => onEdit(category)}>
                      ویرایش
                    </DropdownMenuItem>
                    <DropdownMenuSeparator />
                    <DropdownMenuItem
                      variant="destructive"
                      onClick={() => onDelete(category)}
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

export default function Categories({
  loaderData,
  actionData,
}: Route.ComponentProps) {
  const navigation = useNavigation()
  const { toast } = useToast()
  const submit = useSubmit()
  const [searchParams, setSearchParams] = useSearchParams()
  const loading = navigation.state === "loading"
  const submitting = navigation.state === "submitting"
  // A successful action answers with a redirect, so the finished intent (read
  // from the submit that just left) is what names the result for the toast.
  const pendingIntent = React.useRef<string | null>(null)
  React.useEffect(() => {
    const formData = navigation.formData
    if (formData) {
      pendingIntent.current = String(formData.get("intent") ?? "")
    }
  }, [navigation.formData])

  // null = dialog closed. A category object edits it; "new" creates.
  const [form, setForm] = React.useState<Category | "new" | null>(null)
  const [deleting, setDeleting] = React.useState<Category | null>(null)
  const [q, setQ] = React.useState(
    loaderData.status === "ok" ? loaderData.q : ""
  )

  React.useEffect(() => {
    if (loaderData.status === "ok") setQ(loaderData.q)
  }, [loaderData])

  // A returned error reopens the dialog it belongs to, so the message sits
  // next to the form that caused it.
  React.useEffect(() => {
    if (!actionData?.error) return
    if (actionData.intent === "create") {
      setForm("new")
    } else if (actionData.values?.id) {
      const id = Number(actionData.values.id)
      const known =
        loaderData.status === "ok"
          ? loaderData.categories.find((c) => c.id === id)
          : undefined
      setForm(
        known ?? {
          id,
          name: actionData.values.name ?? "",
          description: actionData.values.description ?? "",
          created_at: "",
          updated_at: "",
        }
      )
    }
  }, [actionData, loaderData])

  // A successful save redirects (actionData becomes undefined); close then.
  const wasSubmitting = React.useRef(false)
  React.useEffect(() => {
    if (wasSubmitting.current && !submitting && !actionData?.error) {
      setForm(null)
      setDeleting(null)
      // A redirect carries no payload, so the intent captured above says what
      // was actually saved.
      const intent = pendingIntent.current
      if (intent === "create")
        toast({ variant: "success", title: "دسته‌بندی ساخته شد." })
      else if (intent === "update")
        toast({ variant: "success", title: "تغییرات دسته‌بندی ذخیره شد." })
      else if (intent === "delete")
        toast({ variant: "success", title: "دسته‌بندی حذف شد." })
      pendingIntent.current = null
    }
    wasSubmitting.current = submitting
  }, [submitting, actionData, toast])

  // Every refusal is also a toast, so the message is read even when the dialog
  // reopened elsewhere or the user dismissed it; the form keeps its own inline
  // field errors where the work was done.
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

  const openEdit = (category: Category) => setForm(category)

  const onSearch = (event: React.FormEvent<HTMLFormElement>) => {
    event.preventDefault()
    const next = new URLSearchParams(searchParams)
    if (q.trim()) next.set("q", q.trim())
    else next.delete("q")
    next.delete("page")
    setSearchParams(next)
  }

  const formError =
    form !== null &&
    actionData?.error &&
    (form === "new"
      ? actionData.intent === "create"
      : actionData.intent === "update")
      ? actionData.error
      : undefined
  const deleteError =
    deleting && actionData?.intent === "delete" ? actionData.error : null

  return (
    <div className="flex flex-col gap-6">
      <PageHeader
        title="دسته‌بندی‌ها"
        description="کانال‌های تلگرامی را گروه‌بندی کنید تا پیدا کردن و مدیریت آن‌ها ساده‌تر شود."
        actions={
          loaderData.status === "ok" && loaderData.canManage ? (
            <Button onClick={() => setForm("new")}>
              <PlusIcon data-icon="inline-start" />
              دسته‌بندی جدید
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
              placeholder="جستجوی دسته‌بندی…"
              className="w-full sm:w-72"
            />
            {loaderData.q && (
              <Button
                type="button"
                variant="outline"
                size="sm"
                onClick={() => {
                  setQ("")
                  setSearchParams({})
                }}
              >
                پاک کردن فیلترها
              </Button>
            )}
          </form>

          <div className="rounded-xl border bg-card">
            {loading ? (
              <LoadingState label="در حال بارگذاری دسته‌بندی‌ها…" />
            ) : loaderData.categories.length === 0 ? (
              <EmptyState
                icon={FolderTreeIcon}
                title={
                  loaderData.q
                    ? "نتیجه‌ای پیدا نشد"
                    : "هنوز دسته‌بندی‌ای وجود ندارد"
                }
                description={
                  loaderData.q
                    ? "عبارت جستجو را تغییر دهید یا حذف کنید."
                    : "اولین دسته را بسازید تا کانال‌ها را سازمان‌دهی کنید."
                }
                action={
                  !loaderData.q && loaderData.canManage ? (
                    <Button
                      variant="outline"
                      size="sm"
                      onClick={() => setForm("new")}
                    >
                      <PlusIcon data-icon="inline-start" />
                      دسته‌بندی جدید
                    </Button>
                  ) : undefined
                }
              />
            ) : (
              <CategoriesTable
                categories={loaderData.categories}
                canManage={loaderData.canManage}
                onEdit={openEdit}
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

      {form !== null && (
        <CategoryDialog
          key={form === "new" ? "new" : form.id}
          category={form === "new" ? null : form}
          error={formError}
          fieldErrors={actionData?.error ? actionData.fieldErrors : undefined}
          pending={submitting}
          defaultValues={actionData?.values}
          onClose={() => setForm(null)}
          onSubmit={(method, values) => {
            submit(
              { ...values, ...(form !== "new" ? { id: String(form.id) } : {}) },
              { method }
            )
          }}
        />
      )}

      <ConfirmDialog
        open={deleting !== null}
        onOpenChange={(open) => !open && setDeleting(null)}
        title="حذف دسته‌بندی"
        description={
          deleting
            ? `دسته‌بندی «${deleting.name}» حذف شود؟ اگر دسته هنوز در جای دیگری استفاده شود، سرور حذف را رد می‌کند و پیام آن همین‌جا نمایش داده می‌شود.`
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

function CategoryDialog({
  category,
  error,
  fieldErrors,
  pending,
  defaultValues,
  onClose,
  onSubmit,
}: {
  category: Category | null
  error?: string
  fieldErrors?: FormErrors
  pending: boolean
  defaultValues?: CategoryActionData["values"]
  onClose: () => void
  onSubmit: (method: "post", values: Record<string, string>) => void
}) {
  const [name, setName] = React.useState(
    category?.name ?? defaultValues?.name ?? ""
  )
  const [description, setDescription] = React.useState(
    category?.description ?? defaultValues?.description ?? ""
  )
  const [touched, setTouched] = React.useState(false)
  const clientNameError =
    touched && name.trim() === "" ? "نام دسته‌بندی را وارد کنید." : null
  const nameError = clientNameError ?? fieldErrors?.name

  return (
    <Dialog open onOpenChange={(open) => !open && !pending && onClose()}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>
            {category ? "ویرایش دسته‌بندی" : "دسته‌بندی جدید"}
          </DialogTitle>
          <DialogDescription>
            {category
              ? "همه فیلدها جایگزین می‌شوند؛ برای پاک کردن توضیحات آن را خالی کنید."
              : "نام دسته الزامی است و باید یکتا باشد."}
          </DialogDescription>
        </DialogHeader>
        <form
          className="flex flex-col gap-4"
          noValidate
          onSubmit={(event) => {
            event.preventDefault()
            if (name.trim() === "") {
              setTouched(true)
              return
            }
            onSubmit("post", {
              intent: category ? "update" : "create",
              name: name.trim(),
              description: description.trim(),
            })
          }}
        >
          {category && <input type="hidden" name="id" value={category.id} />}
          <Field data-invalid={nameError ? "" : undefined}>
            <FieldLabel htmlFor="category-name" required>
              نام
            </FieldLabel>
            <FieldControl
              id="category-name"
              name="name"
              value={name}
              onChange={(event) => setName(event.target.value)}
              onBlur={() => setTouched(true)}
              required
            />
            {nameError && <FieldError>{nameError}</FieldError>}
          </Field>
          <Field>
            <FieldLabel htmlFor="category-description">توضیحات</FieldLabel>
            <FieldControl
              id="category-description"
              name="description"
              value={description}
              onChange={(event) => setDescription(event.target.value)}
              className="h-auto min-h-20 py-2"
            />
            <FieldDescription>
              برای ویرایش، ارسال خالی این فیلد توضیحات را پاک می‌کند.
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
            <SubmitButton
              pending={pending}
              submittingLabel={category ? "در حال ذخیره…" : "در حال ایجاد…"}
            >
              {category ? "ذخیره" : "ایجاد"}
            </SubmitButton>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  )
}
