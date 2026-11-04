import assert from "node:assert/strict"
import { test } from "node:test"

import { renderToStaticMarkup } from "react-dom/server"

import type { Category, WordPressSite } from "@/lib/types"

import { SitesTable, SitesToolbar } from "./wordpress"

// One table at every viewport, so the markup is inspected directly.
const site = (id: number): WordPressSite => ({
  id,
  category_ids: [1],
  url: `https://site${id}.example`,
  domain: `site${id}.example`,
  title: `سایت ${id}`,
  site_icon_url: null,
  username: `user${id}`,
  created_at: "2026-01-01T00:00:00Z",
  updated_at: "2026-01-01T00:00:00Z",
})

const category: Category = {
  id: 1,
  name: "دسته یک",
  description: "",
  created_at: "2026-01-01T00:00:00Z",
  updated_at: "2026-01-01T00:00:00Z",
}

const tableHtml = renderToStaticMarkup(
  <SitesTable
    sites={[site(1), site(2)]}
    categories={[category]}
    pending={false}
    onEdit={() => {}}
    onDelete={() => {}}
  />
)

const tags = (name: "th" | "td"): string[] => {
  const out: string[] = []
  let i = 0
  while ((i = tableHtml.indexOf(`<${name} `, i)) !== -1) {
    const end = tableHtml.indexOf(">", i)
    out.push(tableHtml.slice(i, end))
    i = end
  }
  return out
}
const classes = (attrs: string) =>
  (/class="([^"]*)"/.exec(attrs)?.[1] ?? "").split(/\s+/)
const cells = [...tags("th"), ...tags("td")].map(classes)

test("renders exactly one table and no card list", () => {
  assert.equal(tableHtml.match(/<table/g)?.length, 1)
  assert.ok(!tableHtml.includes("<ul"))
  assert.ok(!tableHtml.includes("md:hidden"))
  assert.ok(!tableHtml.includes("hidden md:table"))
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

test("every header and cell is single-line", () => {
  assert.equal(cells.length, 18) // 6 headers + 6 cells per row
  for (const list of cells) assert.ok(list.includes("whitespace-nowrap"))
})

test("each column value appears once per row", () => {
  // Word-boundary regex: a naive count would also match "site1" inside
  // "site1.example". Title/username are anchored to their own `<span>`/`<td>`
  // because the menu's aria-label repeats the title.
  for (const row of [site(1), site(2)]) {
    for (const value of [row.domain, `>${row.title}<`, `>${row.username}<`]) {
      const re = new RegExp(
        `(?<![\\w.-])${value.replaceAll(".", "\\.")}(?![\\w.-])`,
        "g"
      )
      assert.equal(tableHtml.match(re)?.length, 1, value)
    }
  }
})

const toolbarHtml = renderToStaticMarkup(
  <SitesToolbar
    search=""
    onSearch={() => {}}
    onSearchChange={() => {}}
    categories={[category]}
    categoryFilterIDs={[]}
    onCategoryFilterChange={() => {}}
    filtersActive
    onClear={() => {}}
  />
)

test("toolbar stacks below lg and the search takes full width", () => {
  const outer = /^<div class="([^"]*)"/.exec(toolbarHtml)![1].split(/\s+/)
  assert.ok(outer.includes("flex-col"))
  assert.ok(outer.includes("lg:flex-row"))
  const searchWrapper = /<form class="([^"]*)"/
    .exec(toolbarHtml)![1]
    .split(/\s+/)
  assert.ok(searchWrapper.includes("w-full"))
  assert.ok(!searchWrapper.includes("sm:w-64"))
  assert.ok(!toolbarHtml.includes("sm:w-64"))
})
