import { Form, Link, redirect, useNavigation } from "react-router"
import { LockKeyholeIcon, RadioTowerIcon, UserIcon } from "lucide-react"
import * as React from "react"

import { Button } from "@workspace/ui/components/button"
import {
  Field,
  FieldControl,
  FieldError,
  FieldLabel,
} from "@workspace/ui/components/field"

import { appVersion } from "@/components/app-version"
import { LOGIN_REDIRECT_TO, setToken } from "@/lib/session"
import { strip, requestURL } from "@/path"

import type { Route } from "./+types/login"

// The next path a login may return to: one of the panel's own paths, without
// the basename (/dashboard), never another origin and never the login page.
function safeNext(raw: string | null): string {
  const next = strip(raw ?? "")
  if (
    !next ||
    !next.startsWith("/") ||
    next.startsWith("//") ||
    next.startsWith(LOGIN_REDIRECT_TO)
  ) {
    return "/dashboard"
  }
  return next
}

export function meta({ location }: Route.MetaArgs) {
  const notice = new URLSearchParams(location.search).get("expired")
  return [{ title: notice ? "ورود دوباره | تیل‌سینک" : "ورود | تیل‌سینک" }]
}

export function clientLoader({ request }: Route.ClientLoaderArgs) {
  const params = requestURL(request).searchParams
  return {
    next: safeNext(params.get("next")),
    expired: params.get("expired") === "1",
  }
}

interface LoginResult {
  error?: string
  username?: string
}

// The action calls the API from the browser and keeps the token there; the
// page then leaves for where the caller was headed, or the dashboard. Login
// answers its own shape ({"access_token", …}), outside the data envelope.
export async function clientAction({ request }: Route.ClientActionArgs) {
  const form = await request.formData()
  const username = String(form.get("username") ?? "").trim()
  const password = String(form.get("password") ?? "")
  const next = safeNext(String(form.get("next") ?? ""))

  if (!password) {
    return { error: "رمز عبور را وارد کنید.", username } satisfies LoginResult
  }

  let token: string
  try {
    const response = await fetch(
      `${import.meta.env.BASE_URL}api/v1/auth/login`,
      {
        method: "POST",
        headers: {
          "Content-Type": "application/json",
          Accept: "application/json",
        },
        body: JSON.stringify({ username, password }),
      }
    )
    const body = (await response.json().catch(() => null)) as {
      access_token?: string
      error?: { code?: string; message?: string }
    } | null
    if (!response.ok || !body?.access_token) {
      return {
        error: loginError(
          response.status,
          body?.error?.code,
          body?.error?.message
        ),
        username,
      } satisfies LoginResult
    }
    token = body.access_token
  } catch {
    return {
      error: "سرویس در دسترس نیست. اتصال شبکه را بررسی کنید.",
      username,
    } satisfies LoginResult
  }

  setToken(token)
  return redirect(next)
}

function loginError(status: number, code?: string, message?: string): string {
  if (code === "invalid_credentials") {
    return "نام کاربری یا رمز عبور نادرست است."
  }
  if (code === "user_inactive") {
    return "این حساب غیرفعال است. با یک مدیر دیگر تماس بگیرید."
  }
  if (status === 0) {
    return "سرویس در دسترس نیست. اتصال شبکه را بررسی کنید."
  }
  return message || "ورود انجام نشد. دوباره تلاش کنید."
}

// The release this panel was built from, shown as the tag reads: v1.4.2.
function VersionLine() {
  if (!appVersion) return null
  return (
    <p className="text-muted-foreground text-xs">نسخه v{appVersion}</p>
  )
}

export default function Login({
  loaderData,
  actionData,
}: Route.ComponentProps) {
  const { next, expired } = loaderData
  const navigation = useNavigation()
  const submitting = navigation.state === "submitting"
  const [username, setUsername] = React.useState(actionData?.username ?? "")
  const [touched, setTouched] = React.useState(false)

  return (
    <div className="bg-muted/40 flex min-h-svh flex-col items-center justify-center gap-6 p-6 md:p-10">
      <div className="flex w-full max-w-sm flex-col gap-6">
        <Link
          to="/dashboard"
          className="flex items-center justify-center gap-2"
        >
          <div className="bg-primary text-primary-foreground flex size-9 items-center justify-center rounded-lg">
            <RadioTowerIcon className="size-5" />
          </div>
          <span className="text-lg font-semibold">تیل‌سینک</span>
        </Link>

        <div className="bg-card flex flex-col gap-6 rounded-xl border p-6 shadow-sm">
          <div className="flex flex-col gap-1 text-center">
            <h1 className="text-lg font-semibold">ورود به پنل مدیریت</h1>
            <p className="text-muted-foreground text-sm">
              با حساب مدیریتی خود وارد شوید تا به کانال‌ها و دسته‌بندی‌ها دسترسی
              داشته باشید.
            </p>
          </div>

          {expired && (
            <p
              role="status"
              className="bg-muted text-muted-foreground rounded-lg px-3 py-2 text-sm"
            >
              نشست شما پایان یافته است. دوباره وارد شوید.
            </p>
          )}

          <Form method="post" className="flex flex-col gap-4" noValidate>
            <input type="hidden" name="next" value={next} />
            <Field
              data-invalid={touched && username.length === 0 ? "" : undefined}
            >
              <FieldLabel htmlFor="username">نام کاربری</FieldLabel>
              <div className="relative">
                <UserIcon className="text-muted-foreground pointer-events-none absolute start-2.5 top-1/2 size-4 -translate-y-1/2" />
                <FieldControl
                  id="username"
                  name="username"
                  autoComplete="username"
                  dir="ltr"
                  className="ps-8"
                  value={username}
                  onChange={(event) => setUsername(event.target.value)}
                  onBlur={() => setTouched(true)}
                  required
                />
              </div>
              {touched && username.length === 0 && (
                <FieldError>نام کاربری الزامی است.</FieldError>
              )}
            </Field>

            <Field>
              <FieldLabel htmlFor="password">رمز عبور</FieldLabel>
              <div className="relative">
                <LockKeyholeIcon className="text-muted-foreground pointer-events-none absolute start-2.5 top-1/2 size-4 -translate-y-1/2" />
                <FieldControl
                  id="password"
                  name="password"
                  type="password"
                  autoComplete="current-password"
                  dir="ltr"
                  className="ps-8"
                  required
                />
              </div>
            </Field>

            {actionData?.error && (
              <p
                role="alert"
                className="bg-destructive/10 text-destructive rounded-lg px-3 py-2 text-sm"
              >
                {actionData.error}
              </p>
            )}

            <Button
              type="submit"
              size="lg"
              className="w-full"
              disabled={submitting}
            >
              {submitting ? "در حال ورود…" : "ورود"}
            </Button>
          </Form>
        </div>

        <div className="flex flex-col gap-1 px-6 text-center">
          <p className="text-muted-foreground text-xs">
            با ورود، شما با قوانین استفاده از پنل موافقت می‌کنید.
          </p>
          <VersionLine />
        </div>
      </div>
    </div>
  )
}
