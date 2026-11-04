import assert from "node:assert/strict"
import { test } from "node:test"

import { renderToStaticMarkup } from "react-dom/server"

import { Dialog } from "@workspace/ui/components/dialog"

import {
  AdministratorPasswordContent,
  CreateAdministratorContent,
  EditAdministratorContent,
} from "@/components/administrators/administrator-dialogs"
import {
  AdministratorPermissionsContent,
  permissionsTabs,
} from "@/components/administrators/permissions-dialog"
import { PasswordInput } from "@/components/password-input"
import {
  createUserBody,
  describeWriteFailure,
  passwordBody,
  updateUserBody,
} from "@/lib/admin-users"
import {
  normalizeDraft,
  type PermissionsPayload,
} from "@/lib/admin-permissions"
import type { Category, Channel, User, WordPressSite } from "@/lib/types"

// Every dialog part needs its root's context, and the root renders its children
// in place, so the content is read inside an open dialog without a portal.
const html = (node: React.ReactNode) =>
  renderToStaticMarkup(<Dialog open>{node}</Dialog>)

const USER: User = {
  id: 12,
  fullname: "میرزا",
  username: "mirza",
  is_active: true,
  created_at: "2026-01-01T00:00:00Z",
  updated_at: "2026-01-01T00:00:00Z",
}

const category = (id: number): Category => ({
  id,
  name: `دسته ${id}`,
  description: "",
  created_at: "",
  updated_at: "",
})

const channel = (id: number): Channel => ({
  id,
  client_key: "official",
  channel_id: id,
  type: "channel",
  title: `کانال ${id}`,
  avatar_available: false,
  membership_status: "member",
  participants_count: null,
  created_at: "",
  updated_at: "",
  last_synced_at: "",
})

const site = (id: number): WordPressSite => ({
  id,
  category_ids: [],
  url: `https://site${id}.example.com`,
  domain: `site${id}.example.com`,
  title: `سایت ${id}`,
  site_icon_url: null,
  username: "admin",
  created_at: "",
  updated_at: "",
})

const LOADED: PermissionsPayload = {
  global: ["categories.view", "servers.view"],
  channels: [],
  categories: [{ category_id: 3, view: "allow" }],
  wordpress: [{ site_id: 2, view: "allow" }],
}

const noop = () => undefined

// --- create ---

test("create carries no permanent explanation under its title", () => {
  const out = html(
    <CreateAdministratorContent
      pending={false}
      onClose={noop}
      onSubmit={noop}
    />
  )
  assert.doesNotMatch(out, /نام کاربری باید یکتا باشد/)
  assert.doesNotMatch(out, /dialog-description/)
  assert.match(out, /افزودن مدیر/)
})

test("create asks for the nickname under its new label", () => {
  const out = html(
    <CreateAdministratorContent
      pending={false}
      onClose={noop}
      onSubmit={noop}
    />
  )
  assert.match(out, /نام مستعار/)
  assert.doesNotMatch(out, /نام کامل/)
  assert.match(out, /نام کاربری/)
  assert.match(out, /رمز عبور/)
})

test("create hides the password and offers to show it", () => {
  const out = html(
    <CreateAdministratorContent
      pending={false}
      onClose={noop}
      onSubmit={noop}
    />
  )
  assert.match(out, /type="password"/)
  assert.match(out, /aria-pressed="false"/)
  assert.match(out, /aria-label="نمایش رمز عبور"/)
})

test("the visibility button is a button, never a submit", () => {
  const out = html(
    <PasswordInput
      id="p"
      label="رمز عبور"
      autoComplete="new-password"
      value="secret"
      onChange={noop}
    />
  )
  assert.match(out, /<button[^>]*type="button"/)
  assert.match(out, /value="secret"/)
})

test("create has no account status control at all", () => {
  const out = html(
    <CreateAdministratorContent
      pending={false}
      onClose={noop}
      onSubmit={noop}
    />
  )
  assert.doesNotMatch(out, /role="switch"/)
  assert.doesNotMatch(out, /وضعیت حساب/)
})

test("create submits a new account as active", () => {
  const body = createUserBody({
    fullname: "میرزا",
    username: "mirza",
    password: "long-enough",
  })
  assert.equal(body.is_active, true)
  assert.equal(body.password, "long-enough")
})

test("a refused username is shown on the username field", () => {
  const out = html(
    <CreateAdministratorContent
      pending={false}
      fieldErrors={{ username: "این نام کاربری قبلاً ثبت شده است." }}
      onClose={noop}
      onSubmit={noop}
    />
  )
  assert.match(out, /این نام کاربری قبلاً ثبت شده است\./)
  assert.match(out, /data-invalid/)
})

test("create keeps its own loading wording", () => {
  const out = html(
    <CreateAdministratorContent pending onClose={noop} onSubmit={noop} />
  )
  assert.match(out, /در حال افزودن/)
  assert.match(out, /disabled/)
})

// --- edit ---

test("edit carries no permanent explanation under its title", () => {
  const out = html(
    <EditAdministratorContent
      user={USER}
      self={false}
      pending={false}
      onClose={noop}
      onSubmit={noop}
    />
  )
  assert.match(out, /ویرایش مدیر/)
  assert.doesNotMatch(out, /dialog-description/)
  assert.match(out, /نام مستعار/)
  assert.doesNotMatch(out, /نام کامل/)
})

test("edit has no password field to reset one by accident", () => {
  const out = html(
    <EditAdministratorContent
      user={USER}
      self={false}
      pending={false}
      onClose={noop}
      onSubmit={noop}
    />
  )
  assert.doesNotMatch(out, /type="password"/)
  assert.doesNotMatch(out, /رمز عبور/)
  const body = updateUserBody({
    fullname: "میرزا",
    username: "mirza",
    is_active: true,
  })
  assert.equal("password" in body, false)
})

test("edit shows the account status on one compact row", () => {
  const out = html(
    <EditAdministratorContent
      user={USER}
      self={false}
      pending={false}
      onClose={noop}
      onSubmit={noop}
    />
  )
  assert.match(out, /flex flex-nowrap items-center justify-between/)
  assert.match(out, /font-semibold/)
  assert.match(out, /وضعیت حساب کاربری/)
  assert.match(out, /role="switch"/)
})

test("the status wording follows the switch", () => {
  const on = html(
    <EditAdministratorContent
      user={{ ...USER, is_active: true }}
      self={false}
      pending={false}
      onClose={noop}
      onSubmit={noop}
    />
  )
  const off = html(
    <EditAdministratorContent
      user={{ ...USER, is_active: false }}
      self={false}
      pending={false}
      onClose={noop}
      onSubmit={noop}
    />
  )
  assert.match(on, />فعال</)
  assert.doesNotMatch(on, />غیرفعال</)
  assert.match(off, />غیرفعال</)
})

test("an administrator cannot switch their own account off", () => {
  const out = html(
    <EditAdministratorContent
      user={USER}
      self
      pending={false}
      onClose={noop}
      onSubmit={noop}
    />
  )
  const role = out.match(/<[^>]*role="switch"[^>]*/)![0]
  assert.match(role, /disabled/)
})

test("edit keeps its own loading wording", () => {
  const out = html(
    <EditAdministratorContent
      user={USER}
      self={false}
      pending
      onClose={noop}
      onSubmit={noop}
    />
  )
  assert.match(out, /در حال ذخیره/)
})

// --- change password ---

test("the password dialog has one field and no confirmation", () => {
  const out = html(
    <AdministratorPasswordContent
      user={USER}
      pending={false}
      onClose={noop}
      onSubmit={noop}
    />
  )
  assert.match(out, /تغییر رمز عبور/)
  assert.match(out, /رمز عبور جدید/)
  assert.equal((out.match(/type="password"/g) ?? []).length, 1)
  assert.doesNotMatch(out, /تکرار/)
  assert.match(out, /mirza/)
})

test("the password dialog clears nothing into the ordinary update", () => {
  assert.deepEqual(passwordBody("new-secret"), { password: "new-secret" })
})

test("a taken username comes back as an error on its own field", () => {
  const create = describeWriteFailure(
    "create",
    "That username is already in use."
  )
  assert.deepEqual(create.fieldErrors, {
    username: "این نام کاربری قبلاً ثبت شده است.",
  })
  assert.equal(create.error, "این نام کاربری قبلاً ثبت شده است.")
  const update = describeWriteFailure(
    "update",
    "That username is already in use."
  )
  assert.equal(update.error, "این نام کاربری متعلق به مدیر دیگری است.")
})

test("the last-administrator refusal is kept as its own reason", () => {
  const out = describeWriteFailure(
    "update",
    "This is the last active account that can manage users; create or promote another one first."
  )
  assert.equal(out.fieldErrors, undefined)
  assert.match(out.error, /آخرین حساب فعال/)
})

test("a refusal the panel does not recognise keeps the server's own sentence", () => {
  const out = describeWriteFailure("update", "some other refusal")
  assert.deepEqual(out, { error: "some other refusal" })
})

test("the password dialog keeps its own loading wording", () => {
  const out = html(
    <AdministratorPasswordContent
      user={USER}
      pending
      onClose={noop}
      onSubmit={noop}
    />
  )
  assert.match(out, /در حال تغییر رمز/)
})

// --- permissions ---

test("permissions open on exactly six tabs, in the order the product asks for", () => {
  assert.deepEqual(
    permissionsTabs.map((tab) => tab.label),
    [
      "گزارش دسترسی‌ها",
      "دسته‌بندی‌ها",
      "کانال‌ها",
      "سایت‌های وردپرسی",
      "مدیرها",
      "عمومی",
    ]
  )
  assert.equal(permissionsTabs[0]?.value, "report")
})

test("the report is what opens first", () => {
  const out = html(
    <AdministratorPermissionsContent
      user={USER}
      draft={normalizeDraft(LOADED)}
      loaded={LOADED}
      catalog={{ channels: [], categories: [category(3)], sites: [site(2)] }}
      pending={false}
      onClose={noop}
      onDraftChange={noop}
      onSave={noop}
    />
  )
  assert.match(out, /role="tab"/)
  assert.match(out, /aria-selected="true"/)
  assert.match(out, /گزارش دسترسی‌ها/)
  // The first tab is the selected one.
  const firstTab = out.match(/<[^>]*role="tab"[^>]*/)![0]
  assert.equal(firstTab.includes('aria-selected="true"'), true)
})

test("each tab keeps its own controls and no second picker", () => {
  const draft = normalizeDraft(LOADED)
  const catalog = {
    channels: [channel(1)],
    categories: [category(3)],
    sites: [site(2)],
  }
  const categories = html(
    <AdministratorPermissionsContent
      user={USER}
      draft={draft}
      loaded={LOADED}
      catalog={catalog}
      tab="categories"
      pending={false}
      onClose={noop}
      onDraftChange={noop}
      onSave={noop}
    />
  )
  assert.match(categories, /مشاهده دسته‌بندی‌ها/)
  assert.match(categories, /مدیریت دسته‌بندی‌ها/)
  assert.equal((categories.match(/role="combobox"/g) ?? []).length, 1)

  const general = html(
    <AdministratorPermissionsContent
      user={USER}
      draft={draft}
      loaded={LOADED}
      catalog={catalog}
      tab="general"
      pending={false}
      onClose={noop}
      onDraftChange={noop}
      onSave={noop}
    />
  )
  assert.match(general, /مشاهده وضعیت سرورهای تلگرامی/)
  assert.equal((general.match(/type="checkbox"/g) ?? []).length, 1)

  const users = html(
    <AdministratorPermissionsContent
      user={USER}
      draft={draft}
      loaded={LOADED}
      catalog={catalog}
      tab="users"
      pending={false}
      onClose={noop}
      onDraftChange={noop}
      onSave={noop}
    />
  )
  assert.match(users, /مشاهده مدیرها/)
  assert.match(users, /مدیریت مدیرها/)
})

test("the resource picker stays hidden until its viewing permission is granted", () => {
  const catalog = { channels: [channel(1)], categories: [], sites: [] }
  const granted = html(
    <AdministratorPermissionsContent
      user={USER}
      draft={{
        globals: ["channels.view"],
        categoryIDs: [],
        channelIDs: [],
        siteIDs: [],
        sitesEditable: true,
      }}
      loaded={LOADED}
      catalog={catalog}
      tab="channels"
      pending={false}
      onClose={noop}
      onDraftChange={noop}
      onSave={noop}
    />
  )
  assert.match(granted, /role="combobox"/)

  const denied = html(
    <AdministratorPermissionsContent
      user={USER}
      draft={{
        globals: [],
        categoryIDs: [],
        channelIDs: [],
        siteIDs: [],
        sitesEditable: true,
      }}
      loaded={LOADED}
      catalog={catalog}
      tab="channels"
      pending={false}
      onClose={noop}
      onDraftChange={noop}
      onSave={noop}
    />
  )
  assert.doesNotMatch(denied, /role="combobox"/)
})

test("the report counts what the draft holds and says it is not saved", () => {
  const draft = normalizeDraft(LOADED)
  const out = html(
    <AdministratorPermissionsContent
      user={USER}
      draft={draft}
      loaded={LOADED}
      catalog={{ channels: [], categories: [category(3)], sites: [site(2)] }}
      tab="report"
      pending={false}
      onClose={noop}
      onDraftChange={noop}
      onSave={noop}
    />
  )
  assert.match(out, /دسته‌بندی‌ها/)
  assert.match(out, /کانال‌ها/)
  assert.match(out, /سایت‌های وردپرسی/)
  assert.match(out, /مدیرها/)
  assert.match(out, /عمومی/)
})

test("the permissions dialog keeps its own loading wording", () => {
  const out = html(
    <AdministratorPermissionsContent
      user={USER}
      draft={normalizeDraft(LOADED)}
      loaded={LOADED}
      catalog={{}}
      pending
      onClose={noop}
      onDraftChange={noop}
      onSave={noop}
    />
  )
  assert.match(out, /در حال ذخیره/)
})
