import assert from "node:assert/strict"
import { test } from "node:test"

import { describeWriteFailure } from "@/lib/admin-users"

// A refused write has to land on the right control. These are the two refusals
// the administrator dialogs can cause themselves; anything else keeps the
// server's own sentence rather than getting one invented for it.

test("a taken username is reported on the username field", () => {
  const described = describeWriteFailure(
    "create",
    "That username is already in use."
  )
  assert.equal(described.fieldErrors?.username, "این نام کاربری قبلاً ثبت شده است.")
  assert.match(described.error, /نام کاربری/)
})

test("a taken username on an edit is not blamed on the new account", () => {
  const described = describeWriteFailure(
    "update",
    "That username is already in use."
  )
  assert.equal(described.fieldErrors?.username, "این نام کاربری قبلاً ثبت شده است.")
  assert.match(described.error, /مدیر دیگری/)
})

test("the last administrator refusal names no field", () => {
  const described = describeWriteFailure(
    "permissions",
    "This is the last active account that can manage users; give the permission to another account first."
  )
  assert.equal(described.fieldErrors, undefined)
  assert.match(described.error, /آخرین حساب فعال/)
})

test("a refusal the panel does not recognise keeps the server's own words", () => {
  const described = describeWriteFailure("update", "Something else entirely.")
  assert.deepEqual(described, { error: "Something else entirely." })
})
