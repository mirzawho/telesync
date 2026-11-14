import assert from "node:assert/strict"
import { test } from "node:test"

import {
  ACCOUNT_ERROR_CODES,
  accountErrorText,
  accountErrorTitle,
  isAccountErrorKnown,
} from "@/lib/account-errors"

test("every contract error code reaches a reader as a real Persian sentence", () => {
  for (const code of ACCOUNT_ERROR_CODES) {
    const text = accountErrorText(code)
    assert.ok(text.length > 0, `${code} has no sentence`)
    assert.equal(isAccountErrorKnown(code), true, `${code} is not mapped`)
  }
})

test("the backend's own English never reaches the interface", () => {
  // A code this build does not know is still this build's problem to phrase.
  const text = accountErrorText(
    "some_future_failure",
    "rpc: account session box decrypt failed"
  )
  assert.doesNotMatch(text, /[A-Za-z]{4,}/)
  assert.ok(text.length > 0)
})

test("a known code ignores whatever the backend said", () => {
  assert.equal(
    accountErrorText("account_not_owned", "row 42 not owned by principal"),
    "این حساب متعلق به شما نیست."
  )
})

test("the sentences distinguish failures that have different remedies", () => {
  // Wrong code and expired code must not read alike: one is retyped, the other
  // is re-requested.
  assert.notEqual(
    accountErrorText("login_code_invalid"),
    accountErrorText("login_code_expired")
  )
  // "Not yours" and "not connected" must not read alike either.
  assert.notEqual(
    accountErrorText("account_not_owned"),
    accountErrorText("account_not_connected")
  )
})

test("rate limiting is described as waiting, not as a refusal", () => {
  assert.match(accountErrorText("login_rate_limited"), /بعد/)
  assert.match(accountErrorText("rate_limited"), /بعد/)
})

test("the selection error tells the reader to choose, not to ask an admin", () => {
  const text = accountErrorText("account_selection_required")
  assert.match(text, /انتخاب/)
  assert.doesNotMatch(text, /مدیر/)
})

test("a missing session-encryption key is reported as a real blocker", () => {
  // It is not a transient error the reader can retry past.
  const text = accountErrorText("session_encryption_unavailable")
  assert.ok(text.length > 0)
  assert.notEqual(text, accountErrorText("login_rate_limited"))
})

test("titles are short headings and never the backend's", () => {
  assert.equal(accountErrorTitle("account_not_owned"), "حساب در دسترس نیست")
  assert.equal(accountErrorTitle("login_code_invalid"), "کد نادرست")
  // A code with no title still gets one, from the fallback.
  assert.equal(
    accountErrorTitle("something_future"),
    "حساب متصل نشد"
  )
})

test("every code that is known has a title shorter than its sentence", () => {
  for (const code of ACCOUNT_ERROR_CODES) {
    const title = accountErrorTitle(code)
    const text = accountErrorText(code)
    if (title !== "حساب متصل نشد") {
      assert.ok(
        title.length <= text.length,
        `${code}: title longer than its sentence`
      )
    }
  }
})

test("the contract's list is complete and nothing beside it is claimed", () => {
  assert.deepEqual(
    [...ACCOUNT_ERROR_CODES],
    [
      "account_selection_required",
      "account_not_owned",
      "account_not_connected",
      "login_attempt_expired",
      "login_attempt_invalid",
      "login_rate_limited",
      "unsupported_login_method",
      "session_encryption_unavailable",
    ]
  )
})
