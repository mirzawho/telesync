// Who may write to a webhook. One question, one answer, shared by every screen
// that offers a write.
//
// Every write the gateway accepts here — creating, changing, deleting, sending a
// test and retrying a delivery alike — needs the global `webhooks.manage`.
// `automation.view` is what admits the area and its histories and it grants no
// write on its own, so a screen that read only that would offer buttons the API
// is bound to refuse.
//
// The session summary carries the real permission list in `permissions.global`.
// A summary that omits the list leaves the caller's rights simply unknown, and
// unknown is not denied: the action is offered and the API's own refusal is
// reported as it wrote it.

export function canManageWebhooks(me: {
  access?: { automation?: boolean }
  permissions?: { global?: unknown }
}): boolean {
  const globals = me.permissions?.global
  if (Array.isArray(globals)) return globals.includes("webhooks.manage")
  return Boolean(me.access?.automation)
}
