import assert from "node:assert/strict"
import { test } from "node:test"

import {
  buildReport,
  catalogFromForm,
  draftEquals,
  draftFromForm,
  holdsGlobal,
  normalizeDraft,
  permissionScopeOf,
  resourceGrants,
  serializeDraft,
  toggleGlobal,
  withViewDependencies,
  type PermissionDraft,
  type PermissionsPayload,
} from "@/lib/admin-permissions"

// The permissions dialog edits one shared draft. These tests pin the two halves
// of it: what the API's stored rows turn into, and what the draft turns back
// into on save. A scope must stay a scope — a picked list may never widen into
// "every item" on the way out.

const loaded: PermissionsPayload = {
  global: ["channels.view", "channels.create", "categories.view"],
  channels: [
    { channel_id: 1, view: "allow" },
    { channel_id: 2, view: "allow" },
    { channel_id: 3, update: "deny", delete: "deny" },
  ],
  categories: [{ category_id: 7, view: "allow" }],
  wordpress: [{ site_id: 4, view: "allow" }],
}

test("a managing permission brings its viewing permission with it", () => {
  const out = withViewDependencies(["categories.create"])
  assert.ok(out.has("categories.view"))
  assert.ok(out.has("categories.create"))
})

test("taking the viewing permission away takes the managing one too", () => {
  const draft = normalizeDraft({
    global: ["categories.view", "categories.create"],
    channels: [],
    categories: [],
    wordpress: [],
  })
  const next = toggleGlobal(draft, "categories.view")
  assert.ok(!next.globals.includes("categories.view"))
  assert.ok(!next.globals.includes("categories.create"))
})

test("the draft keeps every group the dialog edits, in one state", () => {
  const draft = normalizeDraft(loaded)
  assert.deepEqual(draft.channelIDs, [1, 2])
  assert.deepEqual(draft.categoryIDs, [7])
  assert.deepEqual(draft.siteIDs, [4])
  assert.ok(draft.globals.includes("channels.view"))
  assert.ok(draft.globals.includes("channels.create"))
})

test("an unknown permission is never invented and a known one is never dropped", () => {
  const draft = normalizeDraft({
    global: ["content.publish", "not.a.permission"],
    channels: [],
    categories: [],
    wordpress: [],
  })
  assert.ok(draft.globals.includes("content.publish"))
  assert.ok(!draft.globals.includes("not.a.permission" as never))
})

test("stored view grants count as the group's viewing permission", () => {
  // A set saved without the global flag still grants view on the named rows;
  // the dialog must show that group as granted rather than losing the rows on
  // the next save.
  const draft = normalizeDraft({
    global: [],
    channels: [{ channel_id: 5, view: "allow" }],
    categories: [],
    wordpress: [],
  })
  assert.ok(draft.globals.includes("channels.view"))
  assert.deepEqual(draft.channelIDs, [5])
})

test("one draft serves every tab: a change in one group leaves the rest alone", () => {
  const draft = normalizeDraft(loaded)
  const next = toggleGlobal(draft, "categories.create")
  assert.deepEqual(next.channelIDs, draft.channelIDs)
  assert.deepEqual(next.siteIDs, draft.siteIDs)
  assert.ok(next.globals.includes("channels.create"))
})

test("an empty selection means the whole kind, and posts no rows", () => {
  const draft: PermissionDraft = {
    globals: ["channels.view", "channels.create"],
    categoryIDs: [],
    channelIDs: [],
    siteIDs: [],
    sitesEditable: true,
  }
  assert.deepEqual(resourceGrants(draft, "channels", []), [])
})

test("a selection is stored as its picks, never as every item", () => {
  const draft: PermissionDraft = {
    globals: ["channels.view", "channels.create"],
    categoryIDs: [],
    channelIDs: [1, 2],
    siteIDs: [],
    sitesEditable: true,
  }
  const rows = resourceGrants(draft, "channels", [
    { id: 1 },
    { id: 2 },
    { id: 3 },
  ])
  const byID = new Map(rows.map((row) => [row.channel_id, row.permissions]))
  assert.deepEqual(byID.get(1), { view: "allow" })
  assert.deepEqual(byID.get(2), { view: "allow" })
  // The items left out are denied outright: without that an unpicked row falls
  // back to the global viewing permission and the scope stops being a scope.
  assert.deepEqual(byID.get(3), {
    view: "deny",
    update: "deny",
    delete: "deny",
  })
  assert.equal(rows.length, 3)
})

test("the same rows come back for a manager who may only look", () => {
  const draft: PermissionDraft = {
    globals: ["channels.view"],
    categoryIDs: [],
    channelIDs: [1],
    siteIDs: [],
    sitesEditable: true,
  }
  const rows = resourceGrants(draft, "channels", [{ id: 1 }, { id: 2 }])
  assert.deepEqual(
    rows.map((row) => row.channel_id),
    [1, 2]
  )
  assert.deepEqual(rows[1]?.permissions, {
    view: "deny",
    update: "deny",
    delete: "deny",
  })
})

test("no viewing permission means no access at all, whatever the picker held", () => {
  const draft: PermissionDraft = {
    globals: [],
    categoryIDs: [],
    channelIDs: [1],
    siteIDs: [],
    sitesEditable: true,
  }
  assert.deepEqual(resourceGrants(draft, "channels", [{ id: 1 }]), [])
})

test("a save against a shape with no site rows posts no site list at all", () => {
  const draft: PermissionDraft = {
    globals: ["wordpress.view"],
    categoryIDs: [],
    channelIDs: [],
    siteIDs: [4],
    sitesEditable: false,
  }
  const form = serializeDraft(draft, {})
  assert.equal("site_ids" in form, false)
  assert.equal("site_unpicked_ids" in form, false)
})

test("a save against a shape with site rows keeps the site scope", () => {
  const draft: PermissionDraft = {
    globals: ["wordpress.view", "wordpress.create"],
    categoryIDs: [],
    channelIDs: [],
    siteIDs: [4],
    sitesEditable: true,
  }
  const form = serializeDraft(draft, { sites: [{ id: 4 }, { id: 9 }] })
  assert.deepEqual(JSON.parse(form.site_ids ?? "[]"), [4])
  assert.deepEqual(JSON.parse(form.site_unpicked_ids ?? "[]"), [9])
})

test("a draft survives load → save → load unchanged", () => {
  const draft = normalizeDraft(loaded)
  const form = serializeDraft(draft, {
    channels: [{ id: 1 }, { id: 2 }, { id: 3 }],
    categories: [{ id: 7 }, { id: 8 }],
    sites: [{ id: 4 }],
  })
  const again = normalizeDraft({
    global: JSON.parse(form.global_permissions ?? "[]"),
    channels: rowsOf(form, "channel"),
    categories: rowsOf(form, "category"),
    wordpress: rowsOf(form, "site"),
  })
  assert.ok(draftEquals(draft, again))
})

test("the report says what the draft will actually grant", () => {
  const draft = normalizeDraft(loaded)
  const report = buildReport(draft, {
    channels: [{ id: 1 }, { id: 2 }, { id: 3 }],
    categories: [{ id: 7 }, { id: 8 }],
    sites: [{ id: 4 }],
  })
  const channels = report.groups.find((group) => group.key === "channels")
  assert.ok(channels)
  // Viewing is granted, limited to the two picked channels.
  assert.deepEqual(
    permissionScopeOf(draft, "channels", [{ id: 1 }, { id: 2 }, { id: 3 }]),
    {
      state: "allowed",
      count: 2,
    }
  )
  assert.equal(channels.lines[0]?.state, "allowed")
  assert.equal(channels.lines[1]?.state, "allowed")
  assert.equal(channels.scope?.count, 2)
})

test("the report never calls a permission granted when it is not", () => {
  const draft = normalizeDraft({
    global: ["categories.create"],
    channels: [],
    categories: [],
    wordpress: [],
  })
  // "manage" alone is reconciled into "view + manage", so the group is granted;
  // a group that was never named at all is denied.
  const report = buildReport(draft, {})
  const wordpress = report.groups.find((group) => group.key === "wordpress")
  assert.equal(wordpress?.lines[0]?.state, "denied")
  assert.equal(wordpress?.lines[1]?.state, "denied")
  const general = report.groups.find((group) => group.key === "general")
  const publish = general?.lines.find((line) => line.key === "content.publish")
  assert.equal(publish?.state, "denied")
})

test("the report says nothing about the retired shared Telegram access", () => {
  // It is no longer assignable, so it is no longer described: a report that
  // still listed it would be telling an administrator about a grant they can
  // no longer make or see the point of.
  const draft = normalizeDraft({
    global: ["servers.view", "content.publish"],
    channels: [],
    categories: [],
    wordpress: [],
  })
  const general = buildReport(draft, {}).groups.find(
    (group) => group.key === "general"
  )
  assert.deepEqual(
    general?.lines.map((line) => line.key),
    ["content.publish"]
  )
  // The permission is still held and still travels in a save; only its place in
  // this dialog is gone. Removing the editor must never revoke stored data.
  assert.ok(holdsGlobal(draft, "servers.view"))
})

test("the report marks a draft that is not saved yet", () => {
  const draft = normalizeDraft(loaded)
  assert.equal(buildReport(draft, {}, loaded).unsaved, false)
  const edited = toggleGlobal(draft, "servers.view")
  assert.equal(buildReport(edited, {}, loaded).unsaved, true)
})

test("the general group keeps a permission the dialog has no editor for", () => {
  const draft = normalizeDraft({
    global: ["content.publish"],
    channels: [],
    categories: [],
    wordpress: [],
  })
  const general = buildReport(
    draft,
    {},
    {
      global: ["content.publish"],
      channels: [],
      categories: [],
      wordpress: [],
    }
  ).groups.find((group) => group.key === "general")
  const publish = general?.lines.find((line) => line.key === "content.publish")
  assert.equal(publish?.state, "allowed")
})

test("the posted form reads back as the draft that was saved", () => {
  const draft = normalizeDraft(loaded)
  const catalog = {
    channels: [{ id: 1 }, { id: 2 }, { id: 3 }],
    categories: [{ id: 7 }, { id: 8 }],
    sites: [{ id: 4 }],
  }
  const form = new FormData()
  for (const [key, value] of Object.entries(serializeDraft(draft, catalog))) {
    form.append(key, value)
  }
  assert.ok(draftEquals(draft, draftFromForm(form)))
  // The catalog the action rebuilds is every id the form named: what the picker
  // held and what it left out.
  assert.deepEqual(
    catalogFromForm(form, "channels").map((item) => item.id),
    [1, 2, 3]
  )
  assert.deepEqual(
    catalogFromForm(form, "categories").map((item) => item.id),
    [7, 8]
  )
})

test("the posted form carries no site list when the shape had none", () => {
  const draft = normalizeDraft({
    global: [],
    channels: [],
    categories: [],
  })
  const form = new FormData()
  for (const [key, value] of Object.entries(serializeDraft(draft, {}))) {
    form.append(key, value)
  }
  assert.equal(form.get("site_ids"), null)
  assert.equal(draftFromForm(form).sitesEditable, false)
})

function rowsOf(
  form: Record<string, string>,
  idKey: "channel" | "category" | "site"
) {
  const picked: number[] = JSON.parse(form[`${idKey}_ids`] ?? "[]")
  const unpicked: number[] = JSON.parse(form[`${idKey}_unpicked_ids`] ?? "[]")
  return [
    ...picked.map((id) => ({ [`${idKey}_id`]: id, view: "allow" })),
    ...unpicked.map((id) => ({
      [`${idKey}_id`]: id,
      view: "deny",
      update: "deny",
      delete: "deny",
    })),
  ]
}
