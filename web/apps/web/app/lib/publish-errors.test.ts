// Publishing failures are shown in Persian, and by category.
//
// The backend's own sentence is English and can change; the panel must not
// paste it into a Persian interface. What a reader needs is which stage failed
// and what to do next, so the API's stable `code` is what the panel branches
// on — and a code it does not know still gets a Persian sentence rather than a
// raw backend string.
import assert from "node:assert/strict"
import { test } from "node:test"

import { publishErrorText, publishErrorTitle } from "@/lib/publish-errors"

const BACKEND = "The source attachment could not be read; publish without media or retry."

test("a source attachment that could not be read is named as a source problem", () => {
  const text = publishErrorText("publish_source_media_unavailable", BACKEND)
  assert.match(text, /مبدأ/, "the message must say the source is at fault")
  assert.ok(!text.includes("The source attachment"), "the backend's English must not reach the reader")
  assert.match(text, /تلاش/, "the reader must be told a retry is possible")
  assert.match(text, /حذف/, "the reader must be told the attachment can be dropped")
})

test("a destination failure is never described as a source failure", () => {
  for (const code of ["publish_media_upload_failed", "publish_unsupported", "publish_permission_denied"]) {
    const text = publishErrorText(code, BACKEND)
    assert.ok(!text.includes("مبدأ"), `${code} must not blame the source`)
  }
  assert.match(publishErrorText("publish_media_upload_failed", ""), /مقصد/)
  assert.match(publishErrorText("publish_unsupported", ""), /پشتیبانی/)
  assert.match(publishErrorText("publish_permission_denied", ""), /اجازه/)
})

test("an unknown outcome is not reported as a plain failure", () => {
  const text = publishErrorText("publish_outcome_unknown", "boom")
  assert.match(text, /مشخص نشد/, "an unknown result must read as unknown")
  assert.match(text, /دوباره/, "the reader must be told it is not retried automatically")
})

test("every known category is a sentence in Persian, never the backend's own words", () => {
  const codes = [
    "publish_source_media_unavailable",
    "publish_source_unavailable",
    "publish_media_upload_failed",
    "publish_media_unsupported",
    "publish_media_too_large",
    "publish_permission_denied",
    "publish_destination_unavailable",
    "publish_forbidden",
    "publish_outcome_unknown",
    "publish_idempotency_conflict",
    "publish_failed",
    "publish_partial_failure",
    "rate_limited",
    "worker_unavailable",
    "not_connected",
    "worker_timeout",
  ]
  for (const code of codes) {
    const text = publishErrorText(code, BACKEND)
    assert.ok(text.length > 0, `${code} must have a message`)
    assert.ok(!/[A-Za-z]{4,}/.test(text), `${code} must not carry English words: ${text}`)
  }
})

test("an unrecognised code falls back to a Persian sentence, not the backend's", () => {
  const text = publishErrorText("something_new", BACKEND)
  assert.ok(text.length > 0)
  assert.ok(!/[A-Za-z]{4,}/.test(text), `the fallback must be Persian: ${text}`)
  assert.ok(!text.includes(BACKEND), "the raw backend sentence must never be shown")
})

test("the short title names the stage too", () => {
  assert.match(publishErrorTitle("publish_source_media_unavailable"), /رسانه/)
  assert.match(publishErrorTitle("publish_media_upload_failed"), /مقصد/)
  assert.ok(!/[A-Za-z]{4,}/.test(publishErrorTitle("unknown_code")))
})
