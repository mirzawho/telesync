import {
  redirect,
  useSearchParams,
  useNavigation,
  useRevalidator,
  useSubmit,
} from "react-router"
import {
  EyeIcon,
  EyeOffIcon,
  GlobeIcon,
  MoreHorizontalIcon,
  PencilIcon,
  PlusIcon,
  SearchXIcon,
  Trash2Icon,
} from "lucide-react"
import * as React from "react"

import { CategoryMultiSelect } from "@/components/category-multi-select"
import { ConfirmDialog } from "@/components/confirm-dialog"
import { PageHeader } from "@/components/page-header"
import { PageError, PageShell, pageMeta, section } from "@/lib/page"
import { faDateTime } from "@/components/persian-digits"
import { SearchInput } from "@/components/search-input"
import { EmptyState, ErrorState, LoadingState } from "@/components/states"
import { SubmitButton } from "@/components/submit-button"
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
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@workspace/ui/components/table"
import { useToast } from "@workspace/ui/components/toast"

import { API_BASE, ApiError, apiRequest } from "@/lib/api"
import { LOGIN_REDIRECT_TO, requireSession } from "@/lib/session"
import { strip, requestURL } from "@/path"
import type { Category, WordPressSite, WordPressWrite } from "@/lib/types"

import type { Route } from "./+types/wordpress"

/*
 * The WordPress site management page.
 *
 * A registered site keeps its application password in the backend, encrypted.
 * Nothing here ever receives it: the list carries the site's title, domain and
 * icon, never a credential. Add and edit post their fields to the API's own
 * form action; the backend validates them against the site, which is also
 * where the title and icon come from.
 *
 * The site's read right (`wordpress.view`) is not exposed by /auth/me's
 * `access` summary, so the loader treats this page like the administrators
 * page: a 403 renders the page's forbidden state rather than hiding a link.
 * Writing needs the global `wordpress.create`; the dialogs act as the only
 * write controls, and the API answers 403 when it is missing.
 */

interface SiteList {
  sites: WordPressSite[]
}

type PageTitle = { title: string }

interface CategoryList {
  categories: Category[]
}

// parseCatFilterReads the URL's comma-separated local category ids. An empty
// selection means "no category filter".
function parseCategoryFilter(raw: string | null): number[] {
  if (!raw) return []
  return raw
    .split(",")
    .map((part) => Number(part.trim()))
    .filter((id) => Number.isInteger(id) && id > 0)
}

type LoaderData =
  | {
      status: "ok"
      sites: WordPressSite[]
      categories: Category[]
      q: string
      categoryIDs: number[]
      filtersActive: boolean
      page: PageTitle
    }
  | { status: "forbidden"; message: string; page: PageTitle }
  | { status: "error"; message: string; page: PageTitle }

function loginRedirect(request: Request): Response {
  const url = requestURL(request)
  return redirect(
    `${LOGIN_REDIRECT_TO}?expired=1&next=${encodeURIComponent(strip(`${url.pathname}${url.search}`))}`
  )
}

export async function clientLoader({
  request,
}: Route.LoaderArgs): Promise<LoaderData> {
  const { token } = await requireSession(request)
  const page: PageTitle = { title: "سایت‌های وردپرسی" }
  const url = requestURL(request)
  const q = (url.searchParams.get("q") ?? "").trim()
  const categoryIDs = parseCategoryFilter(url.searchParams.get("category_ids"))

  try {
    // The collection is not paginated: the API answers every visible site.
    // The filters are the API's own query, so the panel asks for a search and
    // a category set rather than narrowing a page it already fetched. The
    // categories are listed beside it, for the dialog's picker and the filter.
    const [{ data }, categories] = await Promise.all([
      apiRequest<SiteList>("GET", "/local/wordpress", {
        token,
        query: {
          q: q || undefined,
          category_ids:
            categoryIDs.length > 0 ? categoryIDs.join(",") : undefined,
        },
      }),
      apiRequest<CategoryList>("GET", "/local/categories", {
        token,
        query: { page: 1, per_page: 100 },
      }),
    ])
    return {
      status: "ok",
      sites: data.sites ?? [],
      categories: categories.data.categories ?? [],
      q,
      categoryIDs,
      filtersActive: q !== "" || categoryIDs.length > 0,
      page,
    }
  } catch (error) {
    if (error instanceof ApiError) {
      if (error.status === 401) throw loginRedirect(request)
      if (error.isForbidden) {
        return {
          status: "forbidden",
          message:
            "شما مجوز مشاهده سایت‌های وردپرسی را ندارید. برای دسترسی، از مدیر بخواهید در بخش مدیرها این اجازه را بدهد.",
          page,
        }
      }
      return { status: "error", message: error.message, page }
    }
    return {
      status: "error",
      message: "بارگذاری سایت‌های وردپرسی ناموفق بود.",
      page,
    }
  }
}

// apiPath turns the API's own path (/api/v1/local/wordpress/3) into the panel's
// address (/admin/wordpress), so a redirect the API names keeps the router's
// base.
function apiPath(to: string | undefined): string {
  const api = API_BASE.replace(/\/$/, "")
  return strip((to ?? "/api/v1/local/wordpress").replace(api, "")).replace(
    /^\/local\/wordpress\/?/,
    "/wordpress"
  )
}

interface SiteActionData {
  error?: string
  // saved: the write succeeded; the page reloads the list and closes its
  // dialogs through the revalidator.
  saved?: { id: number; redirectTo: string; deleted: boolean }
  intent?: "create" | "update" | "delete"
  // Echoed back so a rejected form reopens with what the user typed. The
  // password is only echoed while the user is still typing it in the dialog
  // they are looking at; it is never written to storage, a URL or a log.
  values?: {
    id?: string
    url?: string
    username?: string
    password?: string
    category_ids?: number[]
  }
  fieldErrors?: FormErrors
}

// parseIDs reads the dialog's JSON list of local ids.
function parseIDs(raw: string): number[] {
  if (!raw) return []
  try {
    const parsed = JSON.parse(raw)
    return Array.isArray(parsed)
      ? parsed.filter((id) => Number.isInteger(id))
      : []
  } catch {
    return []
  }
}

// normaliseDomain mirrors the backend's rules closely enough to validate the
// field on the client: https, a host and an optional path prefix, with no
// credentials, query, fragment or port. A bare domain is given https://. The
// backend re-checks all of it and is the authority.
function normaliseDomain(raw: string): string | null {
  let text = raw.trim()
  if (!text) return null
  if (!/^https?:\/\//i.test(text)) {
    text = `https://${text}`
  }
  if (!/^https?:\/\/[^\s/?#@\\]+(\/[^\s?#\\]*)?$/i.test(text)) {
    return null
  }
  if (text.toLowerCase().startsWith("http://")) {
    return null
  }
  return text.replace(/\/+$/, "")
}

export async function clientAction({
  request,
}: Route.ActionArgs): Promise<SiteActionData | Response> {
  const { token } = await requireSession(request)
  const form = await request.formData()
  const intent = String(form.get("intent") ?? "")
  const rawId = String(form.get("id") ?? "")
  const rawUrl = String(form.get("url") ?? "")
  const username = String(form.get("username") ?? "").trim()
  const password = String(form.get("password") ?? "")
  const categoryIDs = parseIDs(String(form.get("category_ids") ?? ""))
  const values = {
    id: rawId || undefined,
    url: rawUrl.trim(),
    username,
    password: password || undefined,
    category_ids: categoryIDs,
  }

  try {
    if (intent === "delete") {
      const id = Number(rawId)
      if (!Number.isInteger(id) || id <= 0) {
        return { error: "شناسه سایت نامعتبر است.", intent: "delete" }
      }
      const { data } = await apiRequest<WordPressWrite>(
        "DELETE",
        `/local/wordpress/${id}`,
        { token }
      )
      return {
        intent: "delete",
        saved: { id, redirectTo: apiPath(data.redirect_to), deleted: true },
      }
    }

    if (intent !== "create" && intent !== "update") {
      return { error: "درخواست نامعتبر است.", intent: "create", values }
    }

    const url = normaliseDomain(rawUrl)
    const fieldErrors: FormErrors = {}
    if (!url) {
      fieldErrors.url =
        "آدرس سایت را با https وارد کنید، مثلاً https://example.com یا https://example.com/blog."
    }
    if (!username) {
      fieldErrors.username = "نام کاربری الزامی است."
    }
    if (intent === "create" && !password) {
      fieldErrors.password = "رمز عبور یا Application Password الزامی است."
    }
    if (Object.keys(fieldErrors).length > 0) {
      return {
        error: "فیلدهای مشخص‌شده را اصلاح کنید.",
        intent,
        values,
        fieldErrors,
      }
    }

    if (intent === "create") {
      const { data } = await apiRequest<WordPressWrite>(
        "POST",
        "/local/wordpress",
        {
          token,
          body: {
            url,
            username,
            password,
            // The dialog's whole category set: an update replaces it.
            category_ids: categoryIDs,
          },
        }
      )
      return {
        intent: "create",
        saved: {
          id: data.saved?.id ?? 0,
          redirectTo: apiPath(data.saved?.redirect_to),
          deleted: false,
        },
      }
    }

    const id = Number(rawId)
    if (!Number.isInteger(id) || id <= 0) {
      return { error: "شناسه سایت نامعتبر است.", intent, values }
    }
    // PATCH keeps what it is not given: url and username are always sent (the
    // form shows their current values), and the password only when the user
    // typed a new one. A blank field is never sent, so it can never clear the
    // stored credential.
    const body: {
      url: string
      username: string
      password?: string
      category_ids: number[]
    } = {
      url: url as string,
      username,
      category_ids: categoryIDs,
    }
    if (password) {
      body.password = password
    }
    const { data } = await apiRequest<WordPressWrite>(
      "PATCH",
      `/local/wordpress/${id}`,
      { token, body }
    )
    return {
      intent: "update",
      saved: {
        id,
        redirectTo: apiPath(data.saved?.redirect_to),
        deleted: false,
      },
    }
  } catch (error) {
    const intentOrCreate = intent === "delete" ? "delete" : intent
    if (error instanceof ApiError) {
      if (error.status === 401) throw loginRedirect(request)
      if (error.isForbidden) {
        return {
          error:
            "شما مجوز ثبت یا ویرایش سایت‌های وردپرسی را ندارید؛ مدیر باید دسترسی «مدیریت سایت‌های وردپرسی» را بدهد.",
          intent: intentOrCreate as SiteActionData["intent"],
          values,
        }
      }
      if (error.code === "duplicate_site") {
        return {
          error: "سایتی با همین آدرس از قبل ثبت شده است.",
          intent: intentOrCreate as SiteActionData["intent"],
          values,
          fieldErrors: { url: "این آدرس از قبل ثبت شده است." },
        }
      }
      if (error.code === "invalid_input" && intentOrCreate !== "delete") {
        return {
          error: error.message,
          intent: intentOrCreate as SiteActionData["intent"],
          values,
          fieldErrors: { url: error.message },
        }
      }
      return {
        error: error.message,
        intent: intentOrCreate as SiteActionData["intent"],
        values,
      }
    }
    return {
      error: "عملیات انجام نشد.",
      intent: intentOrCreate as SiteActionData["intent"],
      values,
    }
  }
}

export const meta = () => pageMeta("سایت‌های وردپرسی")

export const handle = section("سایت‌های وردپرسی")

export function ErrorBoundary({ error }: Route.ErrorBoundaryProps) {
  return (
    <PageShell title="سایت‌های وردپرسی">
      <PageError error={error} />
    </PageShell>
  )
}

export default function WordPressSites({
  loaderData,
  actionData,
}: Route.ComponentProps) {
  const navigation = useNavigation()
  const submit = useSubmit()
  const revalidator = useRevalidator()
  const { toast } = useToast()
  const [searchParams, setSearchParams] = useSearchParams()
  const loading =
    navigation.state === "loading" || revalidator.state === "loading"
  const submitting = navigation.state === "submitting"

  // The filters live in the URL, so a reload and a shared link keep them. The
  // search box is local until Enter submits the form.
  const [search, setSearch] = React.useState(
    loaderData.status === "ok" ? loaderData.q : ""
  )
  React.useEffect(() => {
    if (loaderData.status === "ok") setSearch(loaderData.q)
  }, [loaderData])

  const categoryFilterIDs =
    loaderData.status === "ok" ? loaderData.categoryIDs : []
  const filtersActive = loaderData.status === "ok" && loaderData.filtersActive

  const onSearch = (event: React.FormEvent<HTMLFormElement>) => {
    event.preventDefault()
    const next = new URLSearchParams(searchParams)
    if (search.trim()) next.set("q", search.trim())
    else next.delete("q")
    setSearchParams(next)
  }

  const setCategoryFilter = (ids: number[]) => {
    const next = new URLSearchParams(searchParams)
    if (ids.length > 0) next.set("category_ids", ids.join(","))
    else next.delete("category_ids")
    setSearchParams(next)
  }

  const clearFilters = () => {
    const next = new URLSearchParams(searchParams)
    next.delete("q")
    next.delete("category_ids")
    setSearchParams(next)
    setSearch("")
  }

  // null = dialog closed. A site edits it; "new" creates one.
  const [form, setForm] = React.useState<WordPressSite | "new" | null>(null)
  const [deleting, setDeleting] = React.useState<WordPressSite | null>(null)

  // A successful write: close whatever is open, say so, reload the list from
  // the API. The action has already finished here; the reload runs when the
  // completed action renders. The API answers a write with a document rather
  // than a redirect, so the page itself says what happened — as a toast, the
  // same surface the rest of the panel uses for write results.
  const lastSaved = React.useRef<SiteActionData["saved"]>(undefined)
  React.useEffect(() => {
    const saved = actionData?.saved
    if (!saved || lastSaved.current === saved) return
    lastSaved.current = saved
    setForm(null)
    setDeleting(null)
    if (saved.deleted) {
      toast({ variant: "success", title: "سایت وردپرسی حذف شد." })
    } else if (actionData?.intent === "update") {
      toast({
        variant: "success",
        title: "تغییرات سایت ذخیره شد.",
        description: "عنوان و آیکون دوباره از خود سایت خوانده شد.",
      })
    } else {
      toast({ variant: "success", title: "سایت وردپرسی ثبت شد." })
    }
    revalidator.revalidate()
  }, [actionData, revalidator, toast])

  // Every refusal is also a toast, so it is read without hunting for the open
  // dialog; the dialog keeps showing the message where the work was done.
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

  // A returned error reopens the dialog it belongs to, with the values typed.
  React.useEffect(() => {
    if (!actionData?.error) return
    if (actionData.intent === "create") {
      setForm("new")
    } else if (actionData.intent === "update" && actionData.values?.id) {
      const id = Number(actionData.values.id)
      const known =
        loaderData.status === "ok"
          ? loaderData.sites.find((s) => s.id === id)
          : undefined
      setForm(
        known ?? {
          id,
          category_ids: actionData.values.category_ids ?? [],
          url: actionData.values?.url ?? "",
          domain: "",
          title: "",
          site_icon_url: null,
          username: actionData.values?.username ?? "",
          created_at: "",
          updated_at: "",
        }
      )
    }
  }, [actionData, loaderData])

  const formError =
    form !== null && actionData?.error
      ? (form === "new" ? "create" : "update") === actionData.intent
        ? actionData.error
        : undefined
      : undefined
  const deleteError =
    deleting && actionData?.intent === "delete" ? actionData.error : null

  return (
    <div className="flex flex-col gap-6">
      <PageHeader
        title="سایت‌های وردپرسی"
        description="سایت‌های وردپرسی که محتوای آن‌ها از کانال‌ها می‌آید را این‌جا مدیریت کنید."
        actions={
          loaderData.status === "ok" ? (
            <Button onClick={() => setForm("new")}>
              <PlusIcon data-icon="inline-start" />
              افزودن سایت
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
          <SitesToolbar
            search={search}
            onSearch={onSearch}
            onSearchChange={setSearch}
            categories={loaderData.categories}
            categoryFilterIDs={categoryFilterIDs}
            onCategoryFilterChange={setCategoryFilter}
            filtersActive={filtersActive}
            onClear={clearFilters}
          />

          <div className="rounded-xl border bg-card">
            {loading ? (
              <LoadingState label="در حال بارگذاری سایت‌های وردپرسی…" />
            ) : loaderData.sites.length === 0 ? (
              filtersActive ? (
                <EmptyState
                  icon={SearchXIcon}
                  title="سایتی با این جستجو یا دسته‌بندی پیدا نشد"
                  description="عبارت جستجو یا دسته‌بندی‌های انتخاب‌شده را تغییر دهید، یا فیلترها را پاک کنید."
                  action={
                    <Button variant="outline" size="sm" onClick={clearFilters}>
                      پاک کردن فیلترها
                    </Button>
                  }
                />
              ) : (
                <EmptyState
                  icon={GlobeIcon}
                  title="هنوز سایت وردپرسی ثبت نشده است"
                  description="اولین سایت را ثبت کنید تا بتوانید بعداً محتوای کانال‌ها را در آن منتشر کنید."
                  action={
                    <Button
                      variant="outline"
                      size="sm"
                      onClick={() => setForm("new")}
                    >
                      <PlusIcon data-icon="inline-start" />
                      افزودن سایت
                    </Button>
                  }
                />
              )
            ) : (
              <SitesTable
                sites={loaderData.sites}
                categories={loaderData.categories}
                pending={submitting && deleting !== null}
                onEdit={(site) => setForm(site)}
                onDelete={(site) => setDeleting(site)}
              />
            )}
          </div>
        </>
      )}

      {form !== null && (
        <SiteDialog
          key={form === "new" ? "new" : form.id}
          site={form === "new" ? null : form}
          error={formError}
          fieldErrors={actionData?.error ? actionData.fieldErrors : undefined}
          defaultValues={actionData?.values}
          categories={loaderData.status === "ok" ? loaderData.categories : []}
          pending={submitting}
          onClose={() => setForm(null)}
          onSubmit={(values) => {
            submit(
              {
                ...values,
                category_ids: JSON.stringify(values.categoryIDs),
                intent: form === "new" ? "create" : "update",
                ...(form !== "new" ? { id: String(form.id) } : {}),
              },
              { method: "post" }
            )
          }}
        />
      )}

      <ConfirmDialog
        open={deleting !== null}
        onOpenChange={(open) => !open && setDeleting(null)}
        title="حذف سایت وردپرسی"
        description={
          deleting ? (
            <>
              سایت «<span dir="auto">{deleting.title || deleting.domain}</span>»
              (<span dir="ltr">{deleting.domain}</span>) حذف شود؟ این کار فقط
              ثبت محلی و رمز نگهداری‌شده را پاک می‌کند؛ هیچ تغییری در خود سایت
              وردپرسی ایجاد نمی‌شود.
            </>
          ) : (
            ""
          )
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

// SitesToolbar holds the search and category filter. It stacks full-width
// below `lg` and sits in one row above it.
export function SitesToolbar({
  search,
  onSearch,
  onSearchChange,
  categories,
  categoryFilterIDs,
  onCategoryFilterChange,
  filtersActive,
  onClear,
}: {
  search: string
  onSearch: (event: React.FormEvent<HTMLFormElement>) => void
  onSearchChange: (value: string) => void
  categories: Category[]
  categoryFilterIDs: number[]
  onCategoryFilterChange: (ids: number[]) => void
  filtersActive: boolean
  onClear: () => void
}) {
  return (
    <div className="flex flex-col gap-3 lg:flex-row lg:items-center">
      <form
        onSubmit={onSearch}
        className="w-full min-w-0 lg:max-w-sm"
        role="search"
      >
        <SearchInput
          name="q"
          value={search}
          onChange={onSearchChange}
          placeholder="جستجو در سایت‌ها…"
          className="w-full min-w-0"
        />
      </form>
      <div className="w-full min-w-0 lg:max-w-sm">
        <CategoryMultiSelect
          id="wordpress-category-filter"
          categories={categories}
          value={categoryFilterIDs}
          onChange={onCategoryFilterChange}
          placeholder="جستجوی دسته‌بندی…"
          emptyText="دسته‌بندی‌ای یافت نشد."
        />
      </div>
      {filtersActive && (
        <Button
          variant="outline"
          size="sm"
          onClick={onClear}
          className="self-start"
        >
          پاک کردن فیلترها
        </Button>
      )}
    </div>
  )
}

// SitesTable lists the registered sites. One table at every viewport: it
// scrolls horizontally with the actions column pinned.
export function SitesTable({
  sites,
  categories,
  pending,
  onEdit,
  onDelete,
}: {
  sites: WordPressSite[]
  categories: Category[]
  pending: boolean
  onEdit: (site: WordPressSite) => void
  onDelete: (site: WordPressSite) => void
}) {
  // Names for the category ids each row carries, from the loader's list.
  const categoryNames = React.useMemo(
    () => new Map(categories.map((category) => [category.id, category.name])),
    [categories]
  )
  const categoriesOf = (site: WordPressSite) =>
    site.category_ids
      .map((id) => categoryNames.get(id))
      .filter((name): name is string => name !== undefined)
  return (
    <Table>
      <TableHeader>
        <TableRow>
          <TableHead>سایت</TableHead>
          <TableHead>آدرس</TableHead>
          <TableHead>نام کاربری</TableHead>
          <TableHead>دسته‌بندی‌ها</TableHead>
          <TableHead>تاریخ ثبت</TableHead>
          <TableHead className="sticky left-0 z-20 w-12 bg-background text-end">
            <span className="sr-only">عملیات</span>
          </TableHead>
        </TableRow>
      </TableHeader>
      <TableBody>
        {sites.map((site) => (
          <TableRow key={site.id}>
            <TableCell>
              <div className="flex items-center gap-3">
                <SiteIcon site={site} className="size-9" />
                <div className="flex min-w-0 flex-col">
                  <span className="truncate font-medium" dir="auto">
                    {site.title || "بدون عنوان"}
                  </span>
                  {!site.title && (
                    <span className="text-xs text-muted-foreground">
                      عنوان از سایت خوانده نشد
                    </span>
                  )}
                </div>
              </div>
            </TableCell>
            <TableCell className="text-muted-foreground" dir="ltr">
              {site.domain}
            </TableCell>
            <TableCell dir="ltr">{site.username}</TableCell>
            <TableCell>
              <CategoryChips names={categoriesOf(site)} />
            </TableCell>
            <TableCell className="text-muted-foreground">
              {faDateTime(site.created_at, false)}
            </TableCell>
            <TableCell className="sticky left-0 z-10 bg-background text-end">
              <RowMenu
                site={site}
                disabled={pending}
                onEdit={onEdit}
                onDelete={onDelete}
              />
            </TableCell>
          </TableRow>
        ))}
      </TableBody>
    </Table>
  )
}

// CategoryChips shows a site's categories as small badges, or an em dash when
// it has none.
function CategoryChips({ names }: { names: string[] }) {
  if (names.length === 0) return <>—</>
  return (
    <span className="flex max-w-56 flex-wrap gap-1">
      {names.map((name) => (
        <Badge key={name} variant="secondary" className="text-xs">
          {name}
        </Badge>
      ))}
    </span>
  )
}

function RowMenu({
  site,
  disabled,
  onEdit,
  onDelete,
}: {
  site: WordPressSite
  disabled: boolean
  onEdit: (site: WordPressSite) => void
  onDelete: (site: WordPressSite) => void
}) {
  const name = site.title || site.domain
  return (
    <DropdownMenu>
      <DropdownMenuTrigger
        render={
          <Button
            variant="ghost"
            size="icon-sm"
            aria-label={`عملیات سایت ${name}`}
            disabled={disabled}
          />
        }
      >
        <MoreHorizontalIcon />
      </DropdownMenuTrigger>
      <DropdownMenuContent align="end">
        <DropdownMenuItem onClick={() => onEdit(site)}>
          <PencilIcon />
          ویرایش
        </DropdownMenuItem>
        <DropdownMenuSeparator />
        <DropdownMenuItem variant="destructive" onClick={() => onDelete(site)}>
          <Trash2Icon />
          حذف
        </DropdownMenuItem>
      </DropdownMenuContent>
    </DropdownMenu>
  )
}

// SiteIcon shows the icon URL the API returned. A site with no icon, or an
// icon that fails to load, shows the same neutral fallback.
function SiteIcon({
  site,
  className,
}: {
  site: Pick<WordPressSite, "site_icon_url" | "title" | "domain">
  className?: string
}) {
  const [failed, setFailed] = React.useState(false)
  const icon = site.site_icon_url
  React.useEffect(() => setFailed(false), [icon])

  return (
    <span
      className={`flex shrink-0 items-center justify-center overflow-hidden rounded-lg border bg-muted text-muted-foreground ${className ?? ""}`}
      aria-hidden="true"
    >
      {icon && !failed ? (
        <img
          src={icon}
          alt=""
          loading="lazy"
          referrerPolicy="no-referrer"
          className="size-full object-cover"
          onError={() => setFailed(true)}
        />
      ) : (
        <GlobeIcon className="size-1/2" />
      )}
    </span>
  )
}

interface FormErrors {
  url?: string
  username?: string
  password?: string
}

// SiteDialog is the add and edit form.
//
// Editing never prefills the password: the backend does not return it, and the
// field is how a new one is given. Leaving it empty keeps the stored one (the
// PATCH contract omits it), and the description says so.
function SiteDialog({
  site,
  error,
  fieldErrors,
  defaultValues,
  categories,
  pending,
  onClose,
  onSubmit,
}: {
  site: WordPressSite | null
  error?: string
  fieldErrors?: FormErrors
  defaultValues?: SiteActionData["values"]
  categories: Category[]
  pending: boolean
  onClose: () => void
  onSubmit: (values: {
    url: string
    username: string
    password: string
    categoryIDs: number[]
  }) => void
}) {
  const [url, setUrl] = React.useState(
    defaultValues?.url ?? (site ? site.url : "")
  )
  const [username, setUsername] = React.useState(
    defaultValues?.username ?? site?.username ?? ""
  )
  // Never initialised from the site: there is no stored password to show.
  const [password, setPassword] = React.useState(defaultValues?.password ?? "")
  const [showPassword, setShowPassword] = React.useState(false)
  const [categoryIDs, setCategoryIDs] = React.useState<number[]>(
    defaultValues?.category_ids ?? site?.category_ids ?? []
  )
  const [touched, setTouched] = React.useState(false)
  const [attempted, setAttempted] = React.useState(false)

  const urlError =
    (attempted || touched) && !normaliseDomain(url)
      ? "آدرس سایت را با https وارد کنید، مثلاً https://example.com."
      : fieldErrors?.url
  const usernameError =
    (attempted || touched) && !username.trim()
      ? "نام کاربری را وارد کنید."
      : fieldErrors?.username
  const passwordError =
    (attempted || touched) && !site && !password
      ? "رمز عبور یا Application Password را وارد کنید."
      : fieldErrors?.password

  return (
    <Dialog open onOpenChange={(open) => !open && !pending && onClose()}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>
            {site ? "ویرایش سایت وردپرسی" : "افزودن سایت وردپرسی"}
          </DialogTitle>
          <DialogDescription>
            {site
              ? "با ذخیره، اطلاعات سایت دوباره از وردپرس خوانده می‌شود؛ اگر اعتبارنامه یا آدرس پذیرفته نشود، ثبت فعلی بدون تغییر می‌ماند."
              : "عنوان و آیکون سایت به‌صورت خودکار از وردپرس خوانده می‌شود و نیازی به وارد کردن آن‌ها نیست."}
          </DialogDescription>
        </DialogHeader>
        <form
          className="flex flex-col gap-4"
          noValidate
          autoComplete="off"
          onSubmit={(event) => {
            event.preventDefault()
            setAttempted(true)
            const normal = normaliseDomain(url)
            if (!normal || !username.trim() || (!site && !password)) {
              return
            }
            onSubmit({
              url: normal,
              username: username.trim(),
              password,
              categoryIDs,
            })
          }}
        >
          <Field data-invalid={urlError ? "" : undefined}>
            <FieldLabel htmlFor="wp-url" required>
              دامنه سایت
            </FieldLabel>
            <FieldControl
              id="wp-url"
              name="url"
              dir="ltr"
              inputMode="url"
              autoCapitalize="none"
              autoCorrect="off"
              spellCheck={false}
              autoComplete="off"
              placeholder="https://example.com"
              value={url}
              onChange={(event) => setUrl(event.target.value)}
              onBlur={() => setTouched(true)}
              required
            />
            {urlError ? (
              <FieldError>{urlError}</FieldError>
            ) : (
              <FieldDescription>
                فقط https پذیرفته می‌شود؛ برای وردپرسِ نصب‌شده در زیرپوشه، نشانی
                همان پوشه را وارد کنید (مثلاً https://example.com/blog).
              </FieldDescription>
            )}
          </Field>

          <Field data-invalid={usernameError ? "" : undefined}>
            <FieldLabel htmlFor="wp-username" required>
              نام کاربری
            </FieldLabel>
            <FieldControl
              id="wp-username"
              name="username"
              dir="ltr"
              autoCapitalize="none"
              autoCorrect="off"
              spellCheck={false}
              autoComplete="off"
              value={username}
              onChange={(event) => setUsername(event.target.value)}
              onBlur={() => setTouched(true)}
              required
            />
            {usernameError ? (
              <FieldError>{usernameError}</FieldError>
            ) : (
              <FieldDescription>
                حساب وردپرسی که Application Password برای آن ساخته شده است؛ این
                حساب باید امکان خواندن تنظیمات سایت را داشته باشد (نقش مدیر).
              </FieldDescription>
            )}
          </Field>

          <Field data-invalid={passwordError ? "" : undefined}>
            <FieldLabel htmlFor="wp-password" required={!site}>
              {site
                ? "Application Password جدید"
                : "رمز عبور / Application Password"}
            </FieldLabel>
            <div className="relative">
              <FieldControl
                id="wp-password"
                name="password"
                dir="ltr"
                type={showPassword ? "text" : "password"}
                autoComplete="new-password"
                className="pe-10"
                value={password}
                onChange={(event) => setPassword(event.target.value)}
                onBlur={() => setTouched(true)}
                required={!site}
              />
              <Button
                type="button"
                variant="ghost"
                size="icon-sm"
                className="absolute inset-y-0 end-1 my-auto"
                aria-label={
                  showPassword ? "پنهان کردن رمز عبور" : "نمایش رمز عبور"
                }
                aria-pressed={showPassword}
                onClick={() => setShowPassword((value) => !value)}
              >
                {showPassword ? (
                  <EyeOffIcon className="size-4" />
                ) : (
                  <EyeIcon className="size-4" />
                )}
              </Button>
            </div>
            {passwordError ? (
              <FieldError>{passwordError}</FieldError>
            ) : (
              <FieldDescription>
                {site
                  ? "برای نگه‌داشتن رمز فعلی، این فیلد را خالی بگذارید."
                  : "به‌جای رمز اصلی حساب، از Application Password استفاده کنید (وردپرس ← کاربران ← پروفایل ← Application Passwords). این رمز جداگانه قابل لغو است و با REST API کار می‌کند."}
              </FieldDescription>
            )}
          </Field>

          <CategoryMultiSelect
            id="wp-categories"
            title="دسته‌بندی‌ها"
            description={
              categories.length === 0
                ? "هنوز دسته‌بندی‌ای ساخته نشده است؛ از صفحه دسته‌بندی‌ها بسازید. سایت می‌تواند بدون دسته هم ثبت شود."
                : "دسته‌بندی‌های همین برنامه (همان دسته‌بندی کانال‌ها) که این سایت در آن‌ها منتشر می‌کند؛ یکی یا چند مورد، یا هیچ‌کدام."
            }
            placeholder="انتخاب دسته‌بندی"
            emptyText="دسته‌بندی‌ای یافت نشد."
            categories={categories}
            value={categoryIDs}
            onChange={setCategoryIDs}
          />

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
              submittingLabel={site ? "در حال ذخیره…" : "در حال افزودن…"}
            >
              {site ? "ذخیره" : "افزودن سایت"}
            </SubmitButton>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  )
}
