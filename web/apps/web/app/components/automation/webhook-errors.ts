import { errorCodeOf, explainAutomationError, messageOf } from "@/lib/automation"

// The webhook screens' own reading of a refusal.
//
// Two of this area's refusals are about a setting or a record's state rather
// than about a field, and the shared map explains them in words that belong to
// a workflow. A 409 here is almost always "this delivery is not in a state you
// may resend", and `public_base_url_required` is about the deployment's own
// origin — saying "another workflow already holds this channel" at either of
// those is not a small wording slip, it sends the operator to the wrong screen.
//
// So both are named here, in the operator's language, and the server's own
// sentence is kept beside the explanation: it is what says which record and
// which state is at fault. Anything else falls through to the shared reading,
// which keeps an `invalid_input` sentence verbatim — "the whsec_ secret is not
// valid base64" is the whole answer and must not be summarised away.

export const webhookRefusals = {
  conflict:
    "این عملیات با وضعیت فعلی رکورد ناسازگار است؛ برای نمونه یک تحویل موفق یا در حال ارسال را نمی‌توان دوباره فرستاد.",
  public_base_url_required:
    "برای ساخت و فعال‌سازی وب‌هوک باید نشانی عمومی سرویس (api.public_base_url) در پیکربندی تنظیم شود؛ بدون آن نشانی رسانه‌ها برای گیرنده قابل دسترس نیست.",
} as const

export type WebhookRefusal = keyof typeof webhookRefusals

// webhookRefusalOf names the refusal, or null when it is one the shared map
// already says well. The screens use it to recognise a refusal rather than to
// guess from a sentence.
export function webhookRefusalOf(error: unknown): WebhookRefusal | null {
  const code = errorCodeOf(error)
  return code === "conflict" || code === "public_base_url_required"
    ? (code as WebhookRefusal)
    : null
}

export function isConflictRefusal(error: unknown): boolean {
  return webhookRefusalOf(error) === "conflict"
}

export function isBaseURLRequired(error: unknown): boolean {
  return webhookRefusalOf(error) === "public_base_url_required"
}

// explainWebhookError is the one sentence a webhook screen shows for a failed
// call. A named refusal gets its Persian explanation followed by the server's
// own sentence, so both the meaning and the reason are on screen; everything
// else keeps the shared reading, which never paraphrases a reasoned refusal.
export function explainWebhookError(error: unknown): string {
  const refusal = webhookRefusalOf(error)
  const message = messageOf(error)
  if (!refusal) return explainAutomationError(error)
  const explained = webhookRefusals[refusal]
  return message ? `${explained} — ${message}` : explained
}

// secretFieldErrorOf puts a refusal about the secret on the secret field, which
// is where the operator typed it. It applies only to a request that actually
// carried one: a stored secret the request said nothing about cannot be the
// thing the server is refusing.
export function secretFieldErrorOf(
  error: unknown,
  sentSecret: boolean
): string | null {
  if (!sentSecret || errorCodeOf(error) !== "invalid_input") return null
  const message = messageOf(error)
  return /secret|whsec/i.test(message) ? message : null
}
