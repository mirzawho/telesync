// The dashboard figure row on small screens: one sideways-scrolling row of
// fixed-width cards, which becomes the grid from lg up. Rendered to static
// markup — no DOM, so this proves the classes and the content, not real
// scrolling (RTL start position, snap behaviour).
import assert from "node:assert/strict"
import { test } from "node:test"

import { createElement, type ComponentType, type ReactNode } from "react"
import { renderToStaticMarkup } from "react-dom/server"

import {
  STAT_CARD_WRAPPER_CLASS,
  STAT_ROW_CLASS,
  StatCardRow,
} from "./dashboard"

// Link from react-router needs a router and Base UI's Button needs a client
// render, so the row is rendered in its test seam: plain <a> links.
const plainLink = (to: string) => createElement("a", { href: to }, "مشاهده")

type CardIcon = Parameters<typeof StatCardRow>[0]["cards"][number]["icon"]
const Icon: CardIcon = ((props: { className?: string }) =>
  createElement("svg", props)) as unknown as CardIcon
const count = (total: number) => ({ ok: true as const, total, rows: [] })

type Cards = Parameters<typeof StatCardRow>[0]["cards"]
const card = (
  key: string,
  title: string,
  to: string,
  resource: Cards[number]["resource"]
): Cards[number] => ({ key, icon: Icon, title, to, resource })

const fourCards: Cards = [
  card("categories", "دسته‌بندی‌ها", "/categories", count(3)),
  card("channels", "کانال‌ها", "/channels", count(5)),
  card("wordpress_sites", "سایت‌های وردپرسی", "/wordpress", count(2)),
  card("administrators", "مدیرها", "/administrators", {
    ok: false,
    message: "بارگذاری آمار ناموفق بود.",
  }),
]

function Row({ cards }: { cards: Cards }): ReactNode {
  return createElement(StatCardRow, { cards, link: plainLink })
}

function render(cards: typeof fourCards): string {
  const App: ComponentType = () => createElement(Row, { cards })
  return renderToStaticMarkup(createElement(App))
}

function tokens(classes: string): string[] {
  return classes.split(/\s+/).filter(Boolean)
}

function openingTags(html: string, tag: string): string[] {
  return html.match(new RegExp(`<${tag}\\b[^>]*>`, "g")) ?? []
}

function classOf(tag: string): string {
  return tag.match(/class="([^"]*)"/)?.[1] ?? ""
}

test("the row is one sideways-scrolling flex row below lg", () => {
  const html = render(fourCards)
  const [section] = openingTags(html, "section")
  assert.ok(section, "expected a <section>")
  assert.match(section, /aria-label="آمار کلی"/)
  const set = new Set(tokens(classOf(section)))

  for (const className of [
    "flex",
    "w-full",
    "min-w-0",
    "gap-4",
    "overflow-x-auto",
    "overscroll-x-contain",
    "pb-2",
    "snap-x",
  ]) {
    assert.ok(set.has(className), `section is missing ${className}`)
  }
  assert.ok(!set.has("grid"), "mobile must not be the plain grid")
  assert.ok(!set.has("flex-wrap"), "cards must never wrap")
  assert.ok(!set.has("flex-col"), "cards must sit in one row")
})

test("at lg the section is the four-column grid again", () => {
  const set = new Set(
    tokens(classOf(openingTags(render(fourCards), "section")[0]))
  )
  for (const className of ["lg:grid", "lg:overflow-visible"]) {
    assert.ok(set.has(className), `section is missing ${className}`)
  }
  // The column count is unscoped on purpose: below lg the element is flex,
  // where grid-cols-* does nothing, so only lg:grid turns it into columns.
  assert.ok(set.has("grid-cols-4"), "section is missing grid-cols-4")
})

test("every card wrapper is a fixed 240px card that cannot shrink", () => {
  const html = render(fourCards)
  const wrappers = openingTags(html, "div").filter((tag) =>
    classOf(tag).includes("w-[240px]")
  )
  assert.equal(wrappers.length, fourCards.length)
  assert.equal(new Set(wrappers).size, 1, "wrappers must share one class")

  const set = new Set(tokens(STAT_CARD_WRAPPER_CLASS))
  for (const className of [
    "flex",
    "w-[240px]",
    "shrink-0",
    "snap-start",
    "lg:w-auto",
    "lg:min-w-0",
  ]) {
    assert.ok(set.has(className), `wrapper is missing ${className}`)
  }
})

test("the card stretches inside its wrapper, so heights match", () => {
  const html = render(fourCards)
  const wrapper = openingTags(html, "div").find((tag) =>
    classOf(tag).includes("w-[240px]")
  )
  const after = html.slice(html.indexOf(wrapper!) + wrapper!.length)
  const card = after.match(/^<div [^>]*data-slot="card"[^>]*>/)?.[0] ?? ""
  const tokensOfCard = new Set(tokens(classOf(card)))
  assert.ok(tokensOfCard.has("h-full"), "card must fill its wrapper's height")
  assert.ok(tokensOfCard.has("w-full"), "card must fill its wrapper's width")
})

test("card count, order, links and failure text are preserved", () => {
  const html = render(fourCards)
  assert.equal((html.match(/data-slot="card"/g) ?? []).length, 4)

  let cursor = 0
  for (const title of [
    "دسته‌بندی‌ها",
    "کانال‌ها",
    "سایت‌های وردپرسی",
    "مدیرها",
  ]) {
    const at = html.indexOf(title, cursor)
    assert.ok(at >= 0, `${title} not found in card order`)
    cursor = at + title.length
  }

  const hrefs = [...html.matchAll(/<a href="([^"]*)"/g)].map((m) => m[1])
  assert.deepEqual(hrefs, [
    "/categories",
    "/channels",
    "/wordpress",
    "/administrators",
  ])
  assert.equal((html.match(/مشاهده/g) ?? []).length, 4)

  assert.ok(html.includes("در دسترس نیست"))
  assert.ok(html.includes("بارگذاری آمار ناموفق بود."))
})

test("a row with one card keeps a single 240px card and its own column count", () => {
  const html = render([fourCards[0]])
  const set = tokens(classOf(openingTags(html, "section")[0]))
  assert.ok(set.includes("grid-cols-1"))
  assert.ok(!set.includes("grid-cols-4"))
  assert.equal((html.match(/data-slot="card"/g) ?? []).length, 1)
})
