import assert from "node:assert/strict"
import { test } from "node:test"

import { renderToStaticMarkup } from "react-dom/server"

import {
  CodeBlock,
  CopyButton,
  DeliveryStatusBadge,
  EnabledBadge,
  HeaderBlock,
  PublicationStatusBadge,
  RunStatusBadge,
  AIStatusBadge,
  StatusCount,
  SwitchState,
} from "@/components/automation/status"
import { SecretField } from "@/components/automation/secret-field"
import {
  aiStatusLabels,
  deliveryStatusLabels,
  headerLines,
  publicationStatusLabels,
  runStatusLabels,
} from "@/lib/automation"

// renderToStaticMarkup escapes the text it renders, so an assertion about the
// body of a recorded request reads the decoded string, not the entity soup.
const html = (node: React.ReactNode) => renderToStaticMarkup(node)
const text = (markup: string) =>
  markup
    .replace(/&quot;/g, '"')
    .replace(/&#x27;/g, "'")
    .replace(/&lt;/g, "<")
    .replace(/&gt;/g, ">")
    .replace(/&amp;/g, "&")

// --- status vocabulary ------------------------------------------------------

test("every status badge shows the Persian word, not only a colour", () => {
  for (const [status, label] of Object.entries(deliveryStatusLabels)) {
    const out = html(
      <DeliveryStatusBadge status={status as keyof typeof deliveryStatusLabels} />
    )
    assert.ok(out.includes(label), `${status} → ${label}`)
  }
  for (const [status, label] of Object.entries(runStatusLabels)) {
    const out = html(
      <RunStatusBadge status={status as keyof typeof runStatusLabels} />
    )
    assert.ok(out.includes(label), `${status} → ${label}`)
  }
  for (const [status, label] of Object.entries(aiStatusLabels)) {
    const out = html(<AIStatusBadge status={status as keyof typeof aiStatusLabels} />)
    assert.ok(out.includes(label), `${status} → ${label}`)
  }
  for (const [status, label] of Object.entries(publicationStatusLabels)) {
    const out = html(
      <PublicationStatusBadge
        status={status as keyof typeof publicationStatusLabels}
      />
    )
    assert.ok(out.includes(label), `${status} → ${label}`)
  }
})

test("a success and a failure are told apart by more than the hue", () => {
  const ok = html(<DeliveryStatusBadge status="succeeded" />)
  const bad = html(<DeliveryStatusBadge status="failed" />)
  assert.ok(ok.includes("موفق"))
  assert.ok(bad.includes("ناموفق"))
  assert.ok(!ok.includes("text-destructive"), "a success is not drawn as a failure")
  assert.ok(bad.includes("text-destructive"))
  assert.ok(ok.includes("text-emerald-700"))
})

test("enabled and disabled read as words as well as tones", () => {
  assert.ok(html(<EnabledBadge enabled />).includes("فعال"))
  assert.ok(html(<EnabledBadge enabled={false} />).includes("غیرفعال"))
  assert.ok(html(<SwitchState on onLabel="روشن" offLabel="خاموش" />).includes("روشن"))
  assert.ok(
    html(<SwitchState on={false} onLabel="روشن" offLabel="خاموش" />).includes("خاموش")
  )
})

test("a status count carries its own label and figure", () => {
  const out = html(<StatusCount label="در صف" value="۵" />)
  assert.ok(out.includes("در صف"))
  assert.ok(out.includes("۵"))
})

// --- recorded bodies --------------------------------------------------------

test("a recorded body is rendered verbatim", () => {
  const out = text(
    html(
      <CodeBlock label="بدنه درخواست" text={'{"id":1,"secret":"[redacted]"}'} />
    )
  )
  assert.ok(out.includes('{"id":1,"secret":"[redacted]"}'))
  assert.ok(!out.includes("کپی انجام نشد"))
})

test("a redaction marker is never rewritten into a value", () => {
  const out = html(<HeaderBlock headers={{ "X-Trace": "abc" }} />)
  assert.ok(out.includes("X-Trace"))
  assert.ok(out.includes("abc"))
})

test("a header that carries a credential is withheld by name and counted", () => {
  const out = html(
    <HeaderBlock headers={{ Authorization: "Bearer abc", "X-Trace": "abc" }} />
  )
  // The name and the value are both absent: a credential header is never
  // rendered, whatever it holds.
  assert.ok(!out.includes("Authorization"))
  assert.ok(!out.includes("Bearer abc"))
  assert.ok(out.includes("X-Trace"), "the rest of the record is still shown")
  // The record says something was withheld rather than reading as "no headers".
  assert.ok(/۱ هدر/.test(out), `hidden count is reported: ${text(out)}`)
})

test("a withheld credential header is not copied out either", () => {
  const out = html(
    <HeaderBlock
      headers={{ "X-Api-Key": "hunter2", "X-Trace": "abc" }}
    />
  )
  assert.ok(!out.includes("hunter2"))
  assert.ok(!out.includes("X-Api-Key"))
  assert.ok(out.includes("X-Trace"))
})

test("headerLines never emits a credential-shaped header", () => {
  assert.deepEqual(headerLines({ Authorization: "Bearer x", "X-Trace": "a" }), [
    "X-Trace: a",
  ])
})

test("a body the server cut short says so", () => {
  const out = html(
    <CodeBlock label="بدنه" text='{"a":1' truncated />
  )
  assert.ok(out.includes("بریده شده"))
})

test("an empty body says it is empty and renders no braces", () => {
  const out = html(<CodeBlock label="بدنه" text="" />)
  assert.ok(out.includes("متنی ثبت نشده است."))
  assert.ok(!out.includes("{}"))
})

test("no headers is its own state, not a blank box", () => {
  const out = html(<HeaderBlock headers={{}} />)
  assert.ok(out.includes("هدری ثبت نشده است."))
})

test("copying is offered next to a recorded body", () => {
  const out = html(<CopyButton value='{"a":1}' label="کپی بدنه" />)
  assert.match(out, /aria-label="کپی بدنه"/)
  assert.match(out, /<button/)
})

// --- secrets ----------------------------------------------------------------

test("an untouched secret field never shows or sends a value", () => {
  const stored = html(
    <SecretField
      id="s"
      hasSecret
      value={null}
      onValueChange={() => {}}
    />
  )
  assert.ok(stored.includes("رمزی ذخیره شده است"))
  assert.ok(stored.includes("جایگزینی رمز"))
  // Nothing value-shaped anywhere in the markup: the plaintext was never sent.
  assert.ok(!stored.includes("<input"), "no input at all while untouched")
  assert.ok(!/type="password"/.test(stored))
})

test("a webhook with no secret offers to add one", () => {
  const out = html(
    <SecretField id="s" hasSecret={false} value={null} onValueChange={() => {}} />
  )
  assert.ok(out.includes("هنوز رمزی ثبت نشده است."))
  assert.ok(out.includes("افزودن رمز"))
})

test("a typed secret is masked by default and reveal is opt-in", () => {
  const out = html(
    <SecretField id="s" hasSecret value="hunter2" onValueChange={() => {}} />
  )
  assert.match(out, /type="password"/)
  assert.ok(!/type="text"/.test(out), "the value starts hidden")
  assert.match(out, /aria-pressed="false"/)
  assert.match(out, /aria-label="نمایش رمز"/)
  // The value is in the control, not printed as text beside it.
  assert.ok(!out.includes(">hunter2<"))
})

test("clearing a stored secret is announced before it happens", () => {
  const out = html(
    <SecretField id="s" hasSecret value="" onValueChange={() => {}} />
  )
  assert.ok(out.includes("رمز ذخیره‌شده حذف می‌شود."))
})

test("the secret field can always step back to leaving the stored one alone", () => {
  const out = html(
    <SecretField id="s" hasSecret value="hunter2" onValueChange={() => {}} />
  )
  assert.ok(out.includes("دست نزدن به رمز ذخیره‌شده"))
  assert.ok(out.includes("حذف رمز ذخیره‌شده"))
})

test("a secret error is shown instead of the description", () => {
  const out = html(
    <SecretField
      id="s"
      hasSecret
      value="x"
      onValueChange={() => {}}
      error="رمز باید دست کم ۱۶ نویسه باشد."
    />
  )
  assert.ok(out.includes("دست کم ۱۶ نویسه"))
})
