import assert from "node:assert/strict"
import { test } from "node:test"
import { createElement, type ReactElement } from "react"
import { renderToStaticMarkup } from "react-dom/server"

import {
  clientLoader,
  DashboardContent,
  PersonalAccountsSection,
} from "./dashboard"
import type { TelegramAccount } from "@/lib/types"

const account: TelegramAccount = {
  id: "acc_01hxyzabcdefghijklmnopqrstuv",
  server_key: "official",
  network_type: "telegram",
  remote_display_name: "حساب خودم",
  status: "authentication_required",
}

const stats = {
  categories: 2,
  channels: 3,
  wordpress_sites: 1,
  administrators: 4,
}

const plainLink = (to: string): ReactElement => createElement("a", { href: to })

function render(element: ReactElement): string {
  return renderToStaticMarkup(element)
}

test("dashboard loads personal account status and never requests global server health", async () => {
  const originalWindow = globalThis.window
  const originalFetch = globalThis.fetch
  const requests: string[] = []

  Object.defineProperty(globalThis, "window", {
    configurable: true,
    value: {
      location: { origin: "http://localhost" },
      sessionStorage: { getItem: () => "panel-token" },
    },
  })

  globalThis.fetch = async (input) => {
    const path = new URL(String(input)).pathname
    requests.push(path)
    const data = path.endsWith("/auth/me")
      ? { access: { channels: true, categories: true, users: true } }
      : path.endsWith("/local/stats/dashboard")
        ? {
            stats: {
              categories: 2,
              channels: 3,
              wordpress_sites: 1,
              administrators: 4,
            },
          }
        : path.endsWith("/me/telegram-accounts")
          ? { accounts: [account] }
          : { servers: [] }
    return new Response(JSON.stringify({ data }), {
      status: 200,
      headers: { "content-type": "application/json" },
    })
  }

  try {
    const result = await clientLoader({
      request: new Request("http://localhost/admin/dashboard"),
    } as never)

    assert.deepEqual(result.personalAccounts, { ok: true, rows: [account] })
    assert.ok(
      requests.some((path) => path.endsWith("/me/telegram-accounts")),
      "the dashboard should use the personal accounts API"
    )
    assert.ok(
      !requests.some((path) => path.endsWith("/telegram")),
      "personal account connectivity must not come from global server health"
    )
    assert.deepEqual(result.stats, { ok: true, stats })
  } finally {
    globalThis.fetch = originalFetch
    Object.defineProperty(globalThis, "window", {
      configurable: true,
      value: originalWindow,
    })
  }
})

test("dashboard keeps collection metrics beside personal accounts, with no server-health card", () => {
  const out = render(
    <DashboardContent
      loaderData={{
        access: {
          channels: true,
          categories: true,
          users: true,
          servers: false,
          automation: false,
        },
        stats: { ok: true, stats },
        personalAccounts: { ok: true, rows: [account] },
      }}
      me={{ fullname: "میرزا" }}
      link={plainLink}
    />
  )

  assert.match(out, /<h1[^>]*>وضعیت حساب‌های من<\/h1>/)
  assert.match(out, /حساب‌های تلگرامی من/)
  assert.match(out, /میرزا/)
  assert.match(out, /data-slot="avatar"/)
  assert.match(out, /رسمی تلگرام/)
  assert.match(out, /data-slot="badge"/)
  assert.match(out, /نیازمند ورود دوباره/)
  assert.match(out, /دسته‌بندی‌ها/)
  assert.match(out, /کانال‌ها/)
  assert.match(out, /سایت‌های وردپرسی/)
  assert.match(out, /مدیرها/)
  assert.doesNotMatch(out, /وضعیت سرورهای تلگرام|سروری پیکربندی نشده است/)
})

test("empty personal accounts offer the add experience at the accounts route", () => {
  const out = render(
    <PersonalAccountsSection result={{ ok: true, rows: [] }} link={plainLink} />
  )

  assert.match(out, /هنوز حساب تلگرامی ندارید/)
  assert.match(out, /افزودن حساب/)
  assert.match(out, /href="\/accounts"/)
  assert.doesNotMatch(out, /مجوز|دسترسی شما/)
})

test("personal accounts show an explicit loading state instead of an empty state", () => {
  const out = render(
    <PersonalAccountsSection
      result={{ ok: true, rows: [] }}
      loading
      link={plainLink}
    />
  )

  assert.match(out, /role="status"/)
  assert.match(out, /در حال بارگذاری وضعیت حساب‌ها/)
  assert.doesNotMatch(out, /هنوز حساب تلگرامی ندارید|افزودن حساب/)
})

test("personal account load failure remains an error, not an empty list", () => {
  const out = render(
    <PersonalAccountsSection
      result={{ ok: false, message: "ارتباط با سرویس برقرار نشد." }}
      link={plainLink}
    />
  )

  assert.match(out, /وضعیت حساب‌ها در دسترس نیست/)
  assert.match(out, /ارتباط با سرویس برقرار نشد/)
  assert.doesNotMatch(out, /هنوز حساب تلگرامی ندارید|افزودن حساب/)
})
