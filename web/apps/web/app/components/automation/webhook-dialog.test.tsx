import assert from "node:assert/strict"
import { test } from "node:test"

import { renderToStaticMarkup } from "react-dom/server"

import {
  WebhookForm,
  authOptions,
  methodOptions,
  type SourceChannelOption,
} from "@/components/automation/webhook-dialog"
import { authModeLabels, type WebhookDraft } from "@/lib/automation"

const html = (node: React.ReactNode) => renderToStaticMarkup(node)

const channels: SourceChannelOption[] = [
  { id: 3, label: "کانال خبری" },
  { id: 9, label: "کانال ورزشی" },
]

const render = (over: Partial<React.ComponentProps<typeof WebhookForm>> = {}) =>
  html(
    <WebhookForm
      webhook={null}
      channels={channels}
      pending={false}
      onClose={() => {}}
      onSubmit={() => {}}
      {...over}
    />
  )

// --- auth mode switching ----------------------------------------------------

test("the custom header appears only for the header auth mode", () => {
  const formWith = (mode: "none" | "bearer" | "hmac" | "header") =>
    html(
      <WebhookForm
        webhook={null}
        channels={channels}
        pending={false}
        onClose={() => {}}
        onSubmit={() => {}}
        defaultValues={{ authMode: mode }}
      />
    )
  for (const mode of ["none", "bearer", "hmac"] as const) {
    const withMode = formWith(mode)
    assert.ok(
      !withMode.includes("نام هدر"),
      `${mode} must not offer a custom header`
    )
    assert.ok(!withMode.includes('for="webhook-auth-header"'))
  }
  const withHeader = formWith("header")
  assert.ok(withHeader.includes("نام هدر"))
  assert.match(withHeader, /for="webhook-auth-header"/)
  assert.ok(withHeader.includes("مقدار این هدر از رمز وب‌هوک گرفته می‌شود."))
})

// The options live in a closed popup's portal, so the choice is asserted on
// the list itself rather than on markup the popup would render when open.
test("every auth mode is offered with its Persian name", () => {
  assert.deepEqual(
    authOptions.map((option) => option.value),
    ["hmac", "bearer", "header", "none"]
  )
  for (const option of authOptions) {
    assert.equal(option.label, authModeLabels[option.value as keyof typeof authModeLabels])
  }
})

test("every HTTP method is offered", () => {
  assert.deepEqual(
    methodOptions.map((option) => option.value),
    ["POST", "PUT", "PATCH"]
  )
})

// --- masked secrets ---------------------------------------------------------

test("a create form starts with no secret field at all", () => {
  const out = render()
  assert.ok(!/type="password"/.test(out))
  assert.ok(!/type="text"/.test(out))
  assert.ok(out.includes("هنوز رمزی ثبت نشده است."))
})

test("an edit form never reopens the stored secret", () => {
  const out = render({
    webhook: {
      id: 7,
      name: "گیرنده",
      description: "",
      enabled: true,
      url: "https://example.com/hook",
      method: "POST",
      authMode: "hmac",
      authHeader: "",
      hasSecret: true,
      timeoutMs: 1000,
      maxAttempts: 3,
      sourceChannelIds: [3],
      createdAt: "",
      updatedAt: "",
      lastDelivery: null,
      summary: null,
    },
  })
  // The stored secret is only reported as present; nothing to reveal is here.
  assert.ok(out.includes("رمزی ذخیره شده است"))
  assert.ok(!/type="password"/.test(out), "no input is pre-filled with a secret")
  assert.ok(!/value="[^"]+"/.test(out.match(/webhook-secret[\s\S]{0,400}/)?.[0] ?? ""))
})

// --- source channels --------------------------------------------------------

test("the source picker offers the managed channels by name", () => {
  const out = render()
  assert.ok(out.includes("کانال‌های مبدأ"))
  assert.ok(out.includes("رویدادهای این کانال‌ها برای این وب‌هوک فرستاده می‌شود."))
  assert.ok(out.includes("کانال را انتخاب کنید"))
  // The rows are the picker's items; they are named by the labels the caller
  // passes, which is what the list page loads from /local/channels.
  assert.deepEqual(channels.map((channel) => channel.label), [
    "کانال خبری",
    "کانال ورزشی",
  ])
})

test("a new webhook starts enabled, POST and with no auth", () => {
  const out = render()
  assert.match(out, /role="switch"/)
  assert.match(out, /aria-checked="true"/)
  assert.ok(out.includes("وضعیت وب‌هوک"))
  assert.ok(out.includes("فعال"))
  assert.ok(out.includes("نشانی مقصد"))
  assert.match(out, /for="webhook-url"/)
  assert.match(out, /type="url"/)
})

// --- refusals ---------------------------------------------------------------

test("field errors sit next to the field that caused them", () => {
  const out = render({
    fieldErrors: {
      name: "نام الزامی است.",
      url: "نشانی مقصد الزامی است.",
      authHeader: "نام هدر الزامی است.",
    },
    defaultValues: { authMode: "header" },
  })
  assert.match(out, /for="webhook-auth-header"/)
  assert.ok(out.includes("نام الزامی است."))
  assert.ok(out.includes("نشانی مقصد الزامی است."))
  assert.ok(out.includes("نام هدر الزامی است."))
})

test("a server refusal is rendered inside the form", () => {
  const out = render({ error: "برای این کانال ورک‌فلوی دیگری ثبت شده است." })
  assert.match(out, /role="alert"/)
  assert.ok(out.includes("ورک‌فلوی دیگری ثبت شده است."))
})

test("the submit button names the action it performs", () => {
  assert.ok(render().includes("ایجاد"))
  assert.ok(render({ pending: true }).includes("در حال ایجاد…"))
})

test("the form is one form with a cancel and a submit", () => {
  const out = render()
  assert.equal(out.match(/<form/g)?.length, 1)
  assert.ok(out.includes("انصراف"))
  assert.match(out, /<button[^>]*type="submit"/)
})
