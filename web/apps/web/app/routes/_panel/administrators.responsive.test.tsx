import assert from "node:assert/strict"
import { test } from "node:test"

import { renderToStaticMarkup } from "react-dom/server"

import type { User } from "@/lib/types"

import { AdministratorsTable } from "./administrators"

const user = (id: number, active: boolean): User => ({
  id,
  fullname: `مدیر ${id}`,
  username: `admin${id}`,
  is_active: active,
  created_at: "2026-01-01T00:00:00Z",
  updated_at: "2026-01-01T00:00:00Z",
})

const html = renderToStaticMarkup(
  <AdministratorsTable
    users={[user(1, true), user(2, false)]}
    canManage
    meId={1}
    onEdit={() => {}}
    onPassword={() => {}}
    onPermissions={() => {}}
    onDelete={() => {}}
  />
)

const tags = (name: "th" | "td"): string[] => {
  const out: string[] = []
  let i = 0
  while ((i = html.indexOf(`<${name} `, i)) !== -1) {
    const end = html.indexOf(">", i)
    out.push(html.slice(i, end))
    i = end
  }
  return out
}
const classes = (attrs: string) =>
  (/class="([^"]*)"/.exec(attrs)?.[1] ?? "").split(/\s+/)
const cells = [...tags("th"), ...tags("td")].map(classes)

test("every header and cell is single-line", () => {
  assert.equal(cells.length, 15) // all rows share the same cell layout
  for (const list of cells)
    assert.ok(list.includes("whitespace-nowrap"), list.join(" "))
  for (const list of cells) assert.ok(!list.includes("whitespace-normal"))
})

test("actions column is pinned to the physical left over an opaque background", () => {
  const actions = [...tags("th"), ...tags("td")]
    .map(classes)
    .filter((list) => list.includes("sticky"))
  assert.equal(actions.length, 3)
  for (const list of actions) {
    assert.ok(list.includes("left-0"))
    assert.ok(!list.includes("right-0"))
    assert.ok(list.includes("bg-background"))
  }
  assert.ok(classes(tags("th").at(-1)!).includes("z-20"))
  assert.ok(classes(tags("td").at(-1)!).includes("z-10"))
})

test("one scroll container, owned by the table", () => {
  assert.equal(html.split("overflow-x-auto").length - 1, 1)
  assert.match(html, /data-slot="table-container"[^>]*overflow-x-auto/)
})

test("headers, values and ltr usernames render", () => {
  for (const text of [
    "نام مستعار",
    "نام کاربری",
    "وضعیت",
    "تاریخ ایجاد",
    "عملیات",
  ]) {
    assert.ok(html.includes(text), text)
  }
  for (const text of [
    "مدیر 1",
    "مدیر 2",
    "admin1",
    "admin2",
    "فعال",
    "غیرفعال",
  ]) {
    assert.ok(html.includes(text), text)
  }
  assert.equal(html.split('dir="ltr"').length - 1, 2)
})
