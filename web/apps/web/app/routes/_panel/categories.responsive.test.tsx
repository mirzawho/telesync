import assert from "node:assert/strict"
import { test } from "node:test"

import { renderToStaticMarkup } from "react-dom/server"

import type { Category } from "@/lib/types"

import { CategoriesTable } from "./categories"

// The table is not a form control, so its markup is inspected directly: every
// cell class list must show a single-line, table-scrolling layout.
const category = (id: number, description: string): Category => ({
  id,
  name: `دسته ${id}`,
  description,
  created_at: "2026-01-01T00:00:00Z",
  updated_at: "2026-01-01T00:00:00Z",
})

const html = renderToStaticMarkup(
  <CategoriesTable
    categories={[
      category(1, "توضیح کوتاه"),
      category(2, "توضیح بلند ".repeat(40)),
    ]}
    canManage
    onEdit={() => {}}
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
  assert.equal(cells.length, 12) // all rows share the same cell layout
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
  // Row order is fixed, so the last td of the markup is row 2's actions cell;
  // every actions cell backs its pin with the opaque token too.
  assert.ok(classes(tags("td").at(-1)!).includes("z-10"))
})

test("one scroll container, owned by the table", () => {
  assert.equal(html.split("overflow-x-auto").length - 1, 1)
  assert.match(html, /data-slot="table-container"[^>]*overflow-x-auto/)
})

test("headers and values render", () => {
  for (const text of ["نام", "توضیحات", "تاریخ ایجاد", "عملیات"]) {
    assert.ok(html.includes(text), text)
  }
  assert.ok(html.includes("دسته 1"))
  assert.ok(html.includes("دسته 2"))
  assert.ok(html.includes("توضیح کوتاه"))
})
