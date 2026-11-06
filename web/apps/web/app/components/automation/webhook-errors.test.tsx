import assert from "node:assert/strict"
import { test } from "node:test"

import { canManageWebhooks } from "@/components/automation/webhook-access"
import {
  explainWebhookError,
  isBaseURLRequired,
  isConflictRefusal,
  secretFieldErrorOf,
  webhookRefusalOf,
  webhookRefusals,
} from "@/components/automation/webhook-errors"
import { authHeaderErrorOf, urlErrorOf } from "@/components/automation/webhook-dialog"
import { ApiError } from "@/lib/api"

// The refusals of this area, exactly as the gateway writes them: the code it
// sends and the sentence it sends with it.
const refuse = (status: number, code: string, message: string) =>
  new ApiError(status, code, message)

const conflict = () =>
  refuse(
    409,
    "conflict",
    "automation: conflicting configuration: this delivery is succeeded and cannot be retried"
  )
const baseURLRequired = () =>
  refuse(
    400,
    "public_base_url_required",
    "api.public_base_url must name the externally reachable origin before a webhook can be enabled, because receivers need an absolute media URL."
  )

// --- the two refusals that need words of their own ---------------------------

test("a 409 conflict is recognisable and explained in Persian", () => {
  const error = conflict()
  assert.equal(webhookRefusalOf(error), "conflict")
  assert.ok(isConflictRefusal(error))
  assert.ok(!isBaseURLRequired(error))

  const shown = explainWebhookError(error)
  assert.ok(
    shown.includes(webhookRefusals.conflict),
    "the refusal is explained in the operator's language"
  )
  // The reason the server gave is kept beside the explanation: it is what says
  // which record and which state is at fault.
  assert.ok(
    shown.includes("this delivery is succeeded and cannot be retried"),
    "the server's own sentence is not paraphrased away"
  )
})

test("a public_base_url_required refusal names the setting that is missing", () => {
  const error = baseURLRequired()
  assert.equal(webhookRefusalOf(error), "public_base_url_required")
  assert.ok(isBaseURLRequired(error))

  const shown = explainWebhookError(error)
  assert.ok(shown.includes(webhookRefusals.public_base_url_required))
  assert.ok(shown.includes("api.public_base_url"), "the setting is named")
  assert.ok(
    shown.includes("before a webhook can be enabled"),
    "the server's own sentence travels with it"
  )
})

test("a named refusal is never swallowed into a generic sentence", () => {
  for (const error of [conflict(), baseURLRequired()]) {
    const shown = explainWebhookError(error)
    assert.ok(!/عملیات انجام نشد/.test(shown), shown)
    assert.ok(shown.length > webhookRefusals.conflict.length, shown)
  }
})

// --- refusals the shared map already says well -------------------------------

test("an invalid input refusal keeps the server's own sentence", () => {
  // The one the operator most needs verbatim: it names the field and why.
  const error = refuse(
    400,
    "invalid_input",
    "automation: invalid configuration: webhooks: the whsec_ secret is not valid base64"
  )
  assert.equal(webhookRefusalOf(error), null)
  assert.equal(
    explainWebhookError(error),
    "automation: invalid configuration: webhooks: the whsec_ secret is not valid base64"
  )
})

test("a refusal no map names keeps whatever the server said", () => {
  const error = refuse(500, "store_error", "the store is unavailable")
  assert.equal(explainWebhookError(error), "the store is unavailable")
})

// --- where a refusal about the secret is shown -------------------------------

test("a refusal about the secret is shown on the secret field", () => {
  const error = refuse(
    400,
    "invalid_input",
    "webhooks: the whsec_ secret is not valid base64"
  )
  assert.equal(
    secretFieldErrorOf(error, true),
    "webhooks: the whsec_ secret is not valid base64"
  )
})

test("a refusal about something else is not blamed on the secret", () => {
  const error = refuse(400, "invalid_input", "method must be one of POST, PUT or PATCH.")
  assert.equal(secretFieldErrorOf(error, true), null)
})

test("an untouched secret is never blamed for a refusal", () => {
  const error = refuse(400, "invalid_input", "webhooks: the whsec_ secret is not valid base64")
  assert.equal(
    secretFieldErrorOf(error, false),
    null,
    "a request that carried no secret cannot have its secret refused"
  )
})

// --- who may write -----------------------------------------------------------

test("webhooks.manage is what a write needs, not automation.view", () => {
  assert.equal(
    canManageWebhooks({
      access: { automation: true },
      permissions: { global: ["automation.view"] },
    }),
    false,
    "reading the area grants no write"
  )
  assert.equal(
    canManageWebhooks({
      access: { automation: true },
      permissions: { global: ["automation.view", "webhooks.manage"] },
    }),
    true
  )
})

test("a summary with no permission list does not deny by itself", () => {
  // Unknown is not denied: the action is offered and the API's own refusal is
  // reported as it wrote it.
  assert.equal(canManageWebhooks({ access: { automation: true } }), true)
  assert.equal(canManageWebhooks({ access: { automation: false } }), false)
  assert.equal(canManageWebhooks({}), false)
})

// --- the field rules the API runs on a save ----------------------------------

test("an address is refused unless it is an absolute http(s) one", () => {
  assert.equal(urlErrorOf("https://example.com/hook"), null)
  assert.equal(urlErrorOf("http://example.com/hook"), null)
  for (const bad of [
    "",
    "   ",
    "example.com/hook",
    "/hook",
    "ftp://example.com/hook",
    "https://",
    "https://user:pass@example.com/hook",
    "https://example.com/hook#frag",
  ]) {
    assert.ok(urlErrorOf(bad), `${bad || "(empty)"} must be refused`)
  }
})

test("a header name that would overwrite a transport header is refused", () => {
  assert.equal(authHeaderErrorOf("X-Api-Key"), null)
  assert.equal(authHeaderErrorOf("X-Trace"), null)
  for (const bad of [
    "",
    "Authorization",
    "authorization",
    "proxy-authorization",
    "content-type",
    "host",
    "webhook-signature",
    "Webhook-Id",
    "X Api Key",
  ]) {
    assert.ok(authHeaderErrorOf(bad), `${bad || "(empty)"} must be refused`)
  }
})
