// The panel's one client for the telesync API.
//
// The panel is served by the telesync binary itself, so the API is on the same
// origin, under the mount the panel was built for: /admin/api/v1/... The
// browser holds the access token; every call sends it as a bearer credential,
// and a 401 ends the session.

// API_BASE is where the panel is served plus the API's own prefix:
// /admin/api/v1 in the built panel and on the dev server alike. BASE_URL ends
// in a slash, so the join needs none. Every path a caller passes is relative
// to it: "/local/users", not "/api/v1/local/users".
export const API_BASE = `${import.meta.env.BASE_URL}api/v1`

export class ApiError extends Error {
  constructor(
    readonly status: number,
    readonly code: string,
    message: string,
    readonly requestId?: string
  ) {
    super(message)
    this.name = "ApiError"
  }

  // isAuthLost: the token is missing, expired, or no longer valid.
  get isAuthLost() {
    return this.status === 401
  }

  get isForbidden() {
    return this.status === 403
  }
}

export interface PageMeta {
  request_id?: string
  generated_at?: string
  page?: number
  per_page?: number
  total?: number
  total_pages?: number
  has_more?: boolean
  next_page?: number
}

export interface ApiRequestOptions {
  token?: string | null
  query?: Record<string, string | number | undefined>
  body?: unknown
  // headers carries request headers the client does not set on its own, such
  // as Idempotency-Key on a publishing call.
  headers?: Record<string, string>
  // signal cancels the call; a caller that supersedes a request aborts the old
  // one so a stale answer never lands.
  signal?: AbortSignal
}

const timeoutMs = 15_000

// apiRequest throws ApiError on every failure (a network failure included as
// status 0), so callers tell "the API said no" from "the API is unreachable".
export async function apiRequest<T>(
  method: "GET" | "POST" | "PUT" | "PATCH" | "DELETE",
  path: string,
  options: ApiRequestOptions = {}
): Promise<{ data: T; meta: PageMeta }> {
  // Absolute: the panel may be served under a path (/admin), and a relative
  // URL has no base to resolve against.
  const url = new URL(API_BASE + path, window.location.origin)
  for (const [key, value] of Object.entries(options.query ?? {})) {
    if (value !== undefined && value !== "") {
      url.searchParams.set(key, String(value))
    }
  }

  const headers: Record<string, string> = {
    Accept: "application/json",
    ...(options.headers ?? {}),
  }
  if (options.token) {
    headers.Authorization = `Bearer ${options.token}`
  }
  const isFormData =
    typeof FormData !== "undefined" && options.body instanceof FormData
  if (options.body !== undefined && !isFormData) {
    headers["Content-Type"] = "application/json"
  }

  let response: Response
  try {
    response = await fetch(url, {
      method,
      headers,
      body:
        options.body === undefined
          ? undefined
          : isFormData
            ? (options.body as FormData)
            : JSON.stringify(options.body),
      // A caller's signal ends only the request it owns; a superseded search
      // aborts its own and the timeout of a call still running stays intact.
      signal: options.signal
        ? AbortSignal.any([options.signal, AbortSignal.timeout(timeoutMs)])
        : AbortSignal.timeout(timeoutMs),
    })
  } catch (error) {
    if (options.signal?.aborted) {
      throw new ApiError(0, "aborted", "درخواست لغو شد.")
    }
    throw new ApiError(0, "network_error", "ارتباط با سرویس برقرار نشد.")
  }

  const text = await response.text()
  let payload: unknown = null
  if (text) {
    try {
      payload = JSON.parse(text)
    } catch {
      throw new ApiError(
        response.status,
        "invalid_response",
        "پاسخ سرویس خوانا نبود."
      )
    }
  }

  if (!response.ok) {
    const error = (
      payload as {
        error?: { code?: string; message?: string; request_id?: string }
      }
    )?.error
    throw new ApiError(
      response.status,
      error?.code ?? "unknown",
      error?.message ?? `سرویس با کد ${response.status} پاسخ داد.`,
      error?.request_id
    )
  }

  const envelope = payload as { data?: unknown; meta?: PageMeta } | null
  return { data: envelope?.data as T, meta: envelope?.meta ?? {} }
}

// errorMessage is the one sentence a page shows for a failed call.
export function errorMessage(
  error: unknown,
  fallback = "بارگذاری ناموفق بود."
): string {
  return error instanceof Error && error.message ? error.message : fallback
}
