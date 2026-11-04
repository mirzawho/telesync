/// <reference types="node" />
import assert from "node:assert/strict"
import { test } from "node:test"
import { createElement as h } from "react"
import { renderToStaticMarkup } from "react-dom/server"

import {
  Table,
  TableActionsCell,
  TableActionsHead,
  TableBody,
  TableCaption,
  TableCell,
  TableFooter,
  TableHead,
  TableHeader,
  TableRow,
} from "@workspace/ui/components/table"

function classesOf(html: string, slot: string): string[] {
  const re = new RegExp(`<[a-z]+[^>]*data-slot="${slot}"[^>]*>`)
  const tag = html.match(re)?.[0]
  if (tag === undefined)
    assert.fail(`no element with data-slot="${slot}" in ${html}`)
  const raw = tag.match(/class="([^"]*)"/)?.[1] ?? ""
  const decoded = raw
    .replaceAll("&gt;", ">")
    .replaceAll("&lt;", "<")
    .replaceAll("&quot;", '"')
    .replaceAll("&#x27;", "'")
    .replaceAll("&amp;", "&")
  return decoded.split(/\s+/).filter(Boolean)
}

function countOccurrences(html: string, needle: string): number {
  return html.split(needle).length - 1
}

test("Table: single scroll container, table grows past viewport", () => {
  const html = renderToStaticMarkup(
    h(
      Table,
      null,
      h(TableHeader, null, h(TableRow, null, h(TableHead, null, "A")))
    )
  )
  assert.equal(countOccurrences(html, "overflow-x-auto"), 1)
  const container = classesOf(html, "table-container")
  assert.ok(container.includes("overflow-x-auto"))
  assert.ok(container.includes("w-full"))
  assert.ok(container.includes("min-w-0"))
  const table = classesOf(html, "table")
  assert.ok(table.includes("min-w-max"))
  assert.ok(table.includes("w-full"))
  assert.equal(table.filter((c) => c.startsWith("overflow")).length, 0)
})

test("TableHead/TableCell keep whitespace-nowrap and consumer overrides win", () => {
  const plain = renderToStaticMarkup(
    h(
      Table,
      null,
      h(
        TableBody,
        null,
        h(TableRow, null, h(TableHead, null, "H"), h(TableCell, null, "C"))
      )
    )
  )
  assert.ok(classesOf(plain, "table-head").includes("whitespace-nowrap"))
  assert.ok(classesOf(plain, "table-cell").includes("whitespace-nowrap"))

  const overridden = renderToStaticMarkup(
    h(
      Table,
      null,
      h(
        TableBody,
        null,
        h(
          TableRow,
          null,
          h(TableHead, { className: "w-12 text-end" }, "A"),
          h(
            TableCell,
            { className: "max-w-72 truncate whitespace-normal" },
            "C"
          )
        )
      )
    )
  )
  const cell = classesOf(overridden, "table-cell")
  assert.ok(cell.includes("whitespace-normal"))
  assert.ok(!cell.includes("whitespace-nowrap"))
  assert.ok(cell.includes("max-w-72"))
  assert.ok(cell.includes("truncate"))
  const head = classesOf(overridden, "table-head")
  assert.ok(head.includes("w-12"))
  assert.ok(head.includes("text-end"))
  assert.ok(!head.includes("text-start"))
})

test("TableActionsHead/Cell stick to physical left with opaque background and z-index", () => {
  const html = renderToStaticMarkup(
    h(
      Table,
      null,
      h(
        TableHeader,
        null,
        h(
          TableRow,
          null,
          h(TableActionsHead, null, h("span", { className: "sr-only" }, "ops"))
        )
      ),
      h(
        TableBody,
        null,
        h(TableRow, null, h(TableActionsCell, { className: "text-end" }, "x"))
      )
    )
  )
  const head = classesOf(html, "table-actions-head")
  const cell = classesOf(html, "table-actions-cell")
  for (const cls of [head, cell]) {
    assert.ok(cls.includes("sticky"))
    assert.ok(cls.includes("left-0"))
    assert.ok(!cls.includes("right-0"))
    assert.ok(!cls.includes("start-0"))
    assert.ok(!cls.includes("end-0"))
    assert.ok(cls.includes("bg-background"))
    assert.ok(cls.includes("whitespace-nowrap"))
  }
  assert.ok(head.includes("z-20"))
  assert.ok(!head.includes("z-10"))
  assert.ok(cell.includes("z-10"))
  assert.ok(cell.includes("text-end"))
  // Underlying primitives still apply.
  assert.ok(head.includes("h-10"))
  assert.ok(cell.includes("px-3"))
  // DOM stays th/td.
  assert.match(html, /<th[^>]*data-slot="table-actions-head"/)
  assert.match(html, /<td[^>]*data-slot="table-actions-cell"/)
})

test("default row/header/body/footer/caption classes unchanged", () => {
  const html = renderToStaticMarkup(
    h(
      Table,
      null,
      h(
        TableHeader,
        null,
        h(TableRow, null, h(TableHead, null, "H"), h(TableCell, null, "C"))
      ),
      h(TableBody, null, h(TableRow, null, h(TableCell, null, "B"))),
      h(TableFooter, null, h(TableRow, null, h(TableCell, null, "F"))),
      h(TableCaption, null, "cap")
    )
  )
  assert.deepEqual(classesOf(html, "table-header"), ["[&_tr]:border-b"])
  assert.deepEqual(classesOf(html, "table-body"), [
    "[&_tr:last-child]:border-0",
  ])
  assert.deepEqual(classesOf(html, "table-row"), [
    "border-b",
    "transition-colors",
    "hover:bg-muted/50",
    "data-[state=selected]:bg-muted",
  ])
  assert.deepEqual(classesOf(html, "table-footer"), [
    "border-t",
    "bg-muted/50",
    "font-medium",
    "[&>tr]:last:border-b-0",
  ])
  assert.deepEqual(classesOf(html, "table-caption"), [
    "mt-4",
    "text-sm",
    "text-muted-foreground",
  ])
})
