// The shared Tabs wrapper must style the selected tab off the attribute the
// installed primitive actually renders.
//
// @base-ui/react/tabs marks the selected tab with `data-active` — bare, and as
// an empty attribute — not Radix's `data-state="active"`, and not the
// `data-selected` that the same package's Select and Combobox items use. The
// shadcn `data-active` variant (packages/ui/src/styles/globals.css imports
// shadcn/tailwind.css, where the variant is declared) matches
// `[data-active]:not([data-active="false"])`. A class list that keys off any
// other attribute leaves a selected tab with no surface at all, which is what
// this pins down.
//
// Interaction (clicking, arrow keys) cannot be exercised here: the workspace
// has no DOM test environment. What is proved is the markup the primitive
// produces and the classes that respond to it.
import assert from "node:assert/strict"
import { test } from "node:test"

import { renderToStaticMarkup } from "react-dom/server"

import {
  Tabs,
  TabsContent,
  TabsList,
  TabsTrigger,
} from "@workspace/ui/components/tabs"

const html = (node: React.ReactNode) => renderToStaticMarkup(node)

// The trigger tags in document order, as attribute maps, so a test can ask
// what a tab is rather than search a class string for a token.
const triggers = (markup: string) =>
  [...markup.matchAll(/<button\b[^>]*>/g)].map(([tag]) => {
    const attributes = new Map<string, string>()
    for (const [, name, value] of tag.matchAll(/([:\w-]+)="([^"]*)"/g)) {
      attributes.set(name, value)
    }
    const classAttribute = /class="([^"]*)"/.exec(tag)?.[1] ?? ""
    return { attributes, classes: classAttribute.split(/\s+/).filter(Boolean) }
  })

const simpleTabs = (props: {
  value?: string
  disabledSecond?: boolean
} = {}) =>
  html(
    <Tabs defaultValue={props.value ?? "overview"}>
      <TabsList>
        <TabsTrigger value="overview">نمای کلی</TabsTrigger>
        <TabsTrigger value="analytics" disabled={props.disabledSecond}>
          آمار
        </TabsTrigger>
        <TabsTrigger value="settings">تنظیمات</TabsTrigger>
      </TabsList>
      <TabsContent value="overview">محتوا</TabsContent>
    </Tabs>,
  )

test("the selected tab carries the primitive's active attribute", () => {
  const [active, inactive] = triggers(simpleTabs())

  assert.equal(active.attributes.get("aria-selected"), "true")
  assert.equal(active.attributes.has("data-active"), true)
  assert.equal(inactive.attributes.get("aria-selected"), "false")
  assert.equal(inactive.attributes.has("data-active"), false)
})

test("selecting another tab moves the active attribute", () => {
  const [first, second] = triggers(simpleTabs({ value: "analytics" }))

  assert.equal(first.attributes.has("data-active"), false)
  assert.equal(second.attributes.has("data-active"), true)
})

test("the active styling keys off the attribute the primitive renders", () => {
  const [active] = triggers(simpleTabs())

  assert.ok(active.classes.includes("data-active:bg-background"))
  assert.ok(active.classes.includes("data-active:text-foreground"))
  assert.ok(active.classes.includes("data-active:shadow-sm"))
  // `--background` is darker than the muted list in dark mode; the raised
  // surface there must not fall back to a hole.
  assert.ok(active.classes.includes("dark:data-active:bg-input/30"))
})

test("no trigger keys styling off an attribute the primitive never renders", () => {
  // `data-selected` belongs to Select/Combobox items; a Tabs trigger carrying
  // it never matches anything.
  for (const { classes } of triggers(simpleTabs({ value: "analytics" }))) {
    assert.equal(
      classes.some((name) => name.startsWith("data-selected:")),
      false,
    )
  }
})

test("inactive tabs carry none of the selected appearance", () => {
  const [, inactive] = triggers(simpleTabs())

  assert.equal(inactive.attributes.has("data-active"), false)
  for (const token of [
    "data-active:bg-background",
    "data-active:text-foreground",
    "data-active:shadow-sm",
  ]) {
    // The class is present on every trigger but only the active attribute
    // makes it apply, so the guarantee is the missing attribute above; this
    // also catches a plain `bg-background` that would paint every tab.
    assert.equal(inactive.classes.includes(token.replace("data-active:", "")), false)
  }
})

test("a trigger keeps its width when the list scrolls sideways", () => {
  // The list is a horizontal scroller; a trigger that shrinks would squeeze its
  // label instead of letting the list scroll.
  for (const { classes } of triggers(simpleTabs())) {
    assert.ok(classes.includes("shrink-0"))
    assert.ok(classes.includes("whitespace-nowrap"))
  }
})

test("a caller's className keeps the default active styling", () => {
  // Both real consumers pass layout classes (the permissions dialog wraps its
  // six tabs, the publish dialog stretches two). Class merging must not drop
  // the active-state utilities those callers never mention.
  const markup = html(
    <Tabs defaultValue="a">
      <TabsList className="h-auto w-full flex-wrap justify-start">
        <TabsTrigger value="a" className="flex-1">
          الف
        </TabsTrigger>
      </TabsList>
    </Tabs>,
  )
  const [trigger] = triggers(markup)

  assert.equal(trigger.attributes.has("data-active"), true)
  for (const token of [
    "flex-1",
    "data-active:bg-background",
    "data-active:text-foreground",
    "data-active:shadow-sm",
  ]) {
    assert.ok(trigger.classes.includes(token), token)
  }
})

test("the list keeps its muted surface and padding", () => {
  const markup = simpleTabs()
  const list = /<div\b[^>]*role="tablist"[^>]*>/.exec(markup)?.[0] ?? ""
  assert.notEqual(list, "")

  for (const token of [
    "bg-muted",
    "p-1",
    "rounded-lg",
    "text-muted-foreground",
    "items-center",
    // A list too wide for its container scrolls sideways, never wraps.
    "overflow-x-auto",
    "overflow-y-hidden",
  ]) {
    assert.match(list, new RegExp(`(^|\\s|")${token}(\\s|")`))
  }
})

test("a disabled tab is marked and never active", () => {
  const [, disabled] = triggers(simpleTabs({ disabledSecond: true }))

  assert.equal(disabled.attributes.get("aria-disabled"), "true")
  assert.equal(disabled.attributes.has("data-disabled"), true)
  assert.equal(disabled.attributes.has("data-active"), false)
  // Base UI leaves the native attribute off so the tab stays reachable by
  // keyboard, so the disabled styling must key off `data-disabled`.
  assert.equal(disabled.attributes.has("disabled"), false)
  assert.ok(disabled.classes.includes("data-disabled:opacity-50"))
})

test("the trigger uses no physical-direction utilities, so RTL is preserved", () => {
  // The panel document is dir="rtl"; classes that name a physical side would
  // put the tabs on the wrong side of the list. Logical and symmetric
  // utilities (justify-center, px-3) are what the component must use.
  for (const { classes } of triggers(simpleTabs())) {
    const physical = classes.filter((name) =>
      /^-?(ml|mr|pl|pr|left|right|text-left|text-right|border-l|border-r|rounded-l|rounded-r)-?/.test(
        name,
      ),
    )
    assert.deepEqual(physical, [])
  }
})

test("the trigger has no fixed direction of its own", () => {
  assert.equal(/<[^>]*\bdir=/.test(simpleTabs()), false)
})
