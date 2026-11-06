import type { GlobalPermission, ResourcePermissions } from "@/lib/types"

// The administrators dialog edits one permission draft through six tabs. This
// module owns that draft: what the API's stored rows turn into, what the draft
// turns back into on save, and what the report tab may honestly claim. Nothing
// here talks to the network, so the rules can be read and tested on their own.
//
// The scope rule, straight from the backend (internal/channelstore/permission.go):
// a kind with no stored rows and its global viewing permission is the whole
// kind, and a stored row names one resource. A selection is therefore stored as
// "allow" on the picked items and an explicit deny on the ones left out —
// without the deny an unpicked row falls back to the global viewing permission
// and a limited selection quietly becomes access to everything.

// ResourceRow is one stored per-resource assignment. The shared shape names a
// resource by `channel_id`, `category_id` or `site_id`.
export type ResourceRow = ResourcePermissions

// PermissionsPayload is one account's stored set, as GET /local/users/{id}
// reports it. `wordpress` is optional on the shape: an API that omits it is read
// as "this dialog may not speak about sites", and a save then leaves the stored
// site grants alone rather than revoking them.
export interface PermissionsPayload {
  global: string[]
  channels: ResourceRow[]
  categories: ResourceRow[]
  wordpress?: ResourceRow[]
}

export interface PermissionDraft {
  globals: GlobalPermission[]
  categoryIDs: number[]
  channelIDs: number[]
  siteIDs: number[]
  // Whether the loaded shape carried per-site rows at all. False means "the
  // site list is not this dialog's to rewrite".
  sitesEditable: boolean
}

export const globalPermissionLabels: Record<GlobalPermission, string> = {
  "users.view": "مشاهده مدیرها",
  "users.manage": "مدیریت مدیرها",
  "channels.view": "مشاهده کانال‌ها",
  "channels.create": "مدیریت کانال‌ها",
  "categories.view": "مشاهده دسته‌بندی‌ها",
  "categories.create": "مدیریت دسته‌بندی‌ها",
  "wordpress.view": "مشاهده سایت‌های وردپرسی",
  "wordpress.create": "مدیریت سایت‌های وردپرسی",
  "servers.view": "مشاهده وضعیت سرورهای تلگرامی",
  "content.publish": "ارسال و انتشار محتوا",
  "automation.view": "مشاهده اتوماسیون",
  "webhooks.manage": "مدیریت وب‌هوک‌ها",
  "workflows.manage": "مدیریت ورک‌فلوها",
}

// A managing permission needs its viewing permission; the API refuses a set
// that has one without the other.
export const manageNeedsView: Partial<
  Record<GlobalPermission, GlobalPermission>
> = {
  "users.manage": "users.view",
  "channels.create": "channels.view",
  "categories.create": "categories.view",
  "wordpress.create": "wordpress.view",
  "webhooks.manage": "automation.view",
  "workflows.manage": "automation.view",
}

// withViewDependencies adds the view permission a managing permission needs.
// The API refuses a set that manages a kind without being able to see it, so a
// managed group is never left looking at nothing.
export function withViewDependencies(
  selected: Iterable<GlobalPermission>
): Set<GlobalPermission> {
  const out = new Set(selected)
  for (const [manage, view] of Object.entries(manageNeedsView)) {
    if (out.has(manage as GlobalPermission) && !out.has(view)) {
      out.add(view as GlobalPermission)
    }
  }
  return out
}

export const scopeKinds = ["categories", "channels", "wordpress"] as const
export type ScopeKind = (typeof scopeKinds)[number]

const scopeIDsKey = {
  categories: "categoryIDs",
  channels: "channelIDs",
  wordpress: "siteIDs",
} as const

const rowIDKey = {
  categories: "category_id",
  channels: "channel_id",
  wordpress: "site_id",
} as const

const formIDField = {
  categories: "category_ids",
  channels: "channel_ids",
  wordpress: "site_ids",
} as const

const formUnpickedField = {
  categories: "category_unpicked_ids",
  channels: "channel_unpicked_ids",
  wordpress: "site_unpicked_ids",
} as const

const viewPermission = {
  categories: "categories.view",
  channels: "channels.view",
  wordpress: "wordpress.view",
} as const

const managePermission = {
  categories: "categories.create",
  channels: "channels.create",
  wordpress: "wordpress.create",
} as const

// ScopeItem is one resource a picker chooses from. The dialog maps its own
// category/channel/site records onto this shape so the report can name what is
// selected without knowing those records.
export interface ScopeItem {
  id: number
  label?: string
}

export interface ScopeCatalog {
  categories?: ScopeItem[] | null
  channels?: ScopeItem[] | null
  sites?: ScopeItem[] | null
}

const catalogOf = {
  categories: (catalog: ScopeCatalog) => catalog.categories,
  channels: (catalog: ScopeCatalog) => catalog.channels,
  wordpress: (catalog: ScopeCatalog) => catalog.sites,
} as const

export function scopeIDsOf(draft: PermissionDraft, kind: ScopeKind): number[] {
  return draft[scopeIDsKey[kind]]
}

// pickedIDs turns stored per-resource rows into picker ids: the rows that allow
// view. Saving rewrites the rows with view alone, which is what the dialog edits.
export function pickedIDs(rows: ResourceRow[], kind: ScopeKind): number[] {
  const key = rowIDKey[kind]
  const out: number[] = []
  for (const row of rows) {
    const id = row[key]
    if (id && row.view === "allow") out.push(id)
  }
  return out
}

// normalizeDraft is the API shape as the dialog edits it. A stored view grant on
// named resources is that group's viewing permission, so the group's flag is
// derived from the rows too: without this a set saved without the global flag
// shows no access at all and its rows are dropped on the next save.
export function normalizeDraft(loaded: PermissionsPayload): PermissionDraft {
  const globals = new Set<GlobalPermission>()
  for (const raw of loaded.global) {
    const value = raw as GlobalPermission
    if (value in globalPermissionLabels) globals.add(value)
  }
  const categoryIDs = pickedIDs(loaded.categories ?? [], "categories")
  const channelIDs = pickedIDs(loaded.channels ?? [], "channels")
  const siteIDs = pickedIDs(loaded.wordpress ?? [], "wordpress")
  for (const kind of scopeKinds) {
    if (
      scopeIDsOf({ categoryIDs, channelIDs, siteIDs } as PermissionDraft, kind)
        .length > 0
    ) {
      globals.add(viewPermission[kind])
    }
  }
  return {
    globals: [...withViewDependencies(globals)].sort(),
    categoryIDs,
    channelIDs,
    siteIDs,
    sitesEditable: loaded.wordpress !== undefined,
  }
}

// toggleGlobal is one checkbox. Taking a viewing permission away takes its
// managing permission with it: the two cannot stand alone, and silently turning
// the view back on would be worse than losing the manage right the user was
// already being taken off.
export function toggleGlobal(
  draft: PermissionDraft,
  value: GlobalPermission
): PermissionDraft {
  const next = new Set(draft.globals)
  if (next.delete(value)) {
    for (const [manage, view] of Object.entries(manageNeedsView)) {
      if (view === value) next.delete(manage as GlobalPermission)
    }
  } else {
    next.add(value)
  }
  return { ...draft, globals: [...withViewDependencies(next)].sort() }
}

export function setScope(
  draft: PermissionDraft,
  kind: ScopeKind,
  ids: number[]
): PermissionDraft {
  return { ...draft, [scopeIDsKey[kind]]: [...ids] } as PermissionDraft
}

export function holdsGlobal(
  draft: PermissionDraft,
  value: GlobalPermission
): boolean {
  return draft.globals.includes(value)
}

export function draftEquals(a: PermissionDraft, b: PermissionDraft): boolean {
  return (
    a.sitesEditable === b.sitesEditable &&
    sameSet(a.globals, b.globals) &&
    sameIDs(a.categoryIDs, b.categoryIDs) &&
    sameIDs(a.channelIDs, b.channelIDs) &&
    sameIDs(a.siteIDs, b.siteIDs)
  )
}

function sameSet(a: string[], b: string[]): boolean {
  return a.length === b.length && a.every((value) => b.includes(value as never))
}

function sameIDs(a: number[], b: number[]): boolean {
  return a.length === b.length && a.every((value, index) => value === b[index])
}

// unpickedIDs is the catalog minus the picker's selection: the items a scope
// must not reach. An empty catalog means the dialog could not list the items,
// and the selection is then the picked ones alone.
export function unpickedIDs(
  items: ScopeItem[] | null | undefined,
  value: number[]
): number[] {
  const chosen = new Set(value)
  return (items ?? []).map((item) => item.id).filter((id) => !chosen.has(id))
}

// resourceGrants turns one kind's scope into per-resource rows. Nothing picked
// means the whole kind and posts no rows at all — the backend reads "no rows" as
// "no selection", which is the widest access there is. Anything picked is a real
// restriction: the picks are allowed, everything the catalog lists besides them
// is denied outright, and a record added later slips past neither.
export function resourceGrants(
  draft: PermissionDraft,
  kind: ScopeKind,
  items: ScopeItem[] | null | undefined
): Record<string, unknown>[] {
  const key = rowIDKey[kind]
  const picked = scopeIDsOf(draft, kind)
  if (!holdsGlobal(draft, viewPermission[kind])) return []
  if (picked.length === 0) return []
  const rows: Record<string, unknown>[] = picked.map((id) => ({
    [key]: id,
    permissions: { view: "allow" },
  }))
  for (const id of unpickedIDs(items, picked)) {
    rows.push({
      [key]: id,
      permissions: { view: "deny", update: "deny", delete: "deny" },
    })
  }
  return rows
}

// serializeDraft is the draft as the save posts it: the same form fields the
// route's action reads. The site list is posted only when the loaded shape
// carried per-site grants, so a save against a shape without them keeps what is
// stored instead of revoking it.
export function serializeDraft(
  draft: PermissionDraft,
  catalog: ScopeCatalog
): Record<string, string> {
  const form: Record<string, string> = {
    global_permissions: JSON.stringify([...draft.globals]),
    category_ids: JSON.stringify(draft.categoryIDs),
    category_unpicked_ids: JSON.stringify(
      unpickedIDs(catalog.categories, draft.categoryIDs)
    ),
    channel_ids: JSON.stringify(draft.channelIDs),
    channel_unpicked_ids: JSON.stringify(
      unpickedIDs(catalog.channels, draft.channelIDs)
    ),
  }
  if (draft.sitesEditable) {
    form.site_ids = JSON.stringify(draft.siteIDs)
    form.site_unpicked_ids = JSON.stringify(
      unpickedIDs(catalog.sites, draft.siteIDs)
    )
  }
  return form
}

// draftFromForm is a posted save read back as the draft it came from, and
// catalogFromForm is that kind's catalog rebuilt from the ids the form named —
// picked plus left out. The two are the other half of serializeDraft, so the
// wire format can be pinned in both directions.

export function draftFromForm(form: FormData): PermissionDraft {
  return {
    globals: [
      ...withViewDependencies(postedGlobals(form)),
    ].sort() as GlobalPermission[],
    categoryIDs: postedIDs(form, "category_ids"),
    channelIDs: postedIDs(form, "channel_ids"),
    siteIDs: postedIDs(form, "site_ids"),
    sitesEditable: form.get("site_ids") !== null,
  }
}

export function catalogFromForm(form: FormData, kind: ScopeKind): ScopeItem[] {
  return [
    ...postedIDs(form, formIDField[kind]),
    ...postedIDs(form, formUnpickedField[kind]),
  ].map((id) => ({ id }))
}

// postedGlobals reads the selected global permissions. The dialog renders its
// checkboxes from React state, so on submit they are not the DOM's own form
// controls and the page posts the whole selection as one JSON entry under the
// form's own field name; an entry per box is read too. A permission posted
// both ways counts once.
function postedGlobals(form: FormData): GlobalPermission[] {
  const out = new Set<GlobalPermission>()
  const known = (value: unknown): value is GlobalPermission =>
    typeof value === "string" && value in globalPermissionLabels
  for (const raw of form.getAll("global_permissions")) {
    if (known(raw)) {
      out.add(raw)
      continue
    }
    let posted: unknown
    try {
      posted = JSON.parse(String(raw))
    } catch {
      continue
    }
    if (Array.isArray(posted)) {
      for (const value of posted) if (known(value)) out.add(value)
    }
  }
  return [...out]
}

// postedIDs reads a picker's JSON list of local ids.
function postedIDs(form: FormData, field: string): number[] {
  let posted: unknown
  try {
    posted = JSON.parse(String(form.get(field) ?? "[]"))
  } catch {
    return []
  }
  if (!Array.isArray(posted)) return []
  return posted.filter(
    (value): value is number => Number.isInteger(value) && value > 0
  )
}

// --- the report tab ---

export interface ReportLine {
  key: string
  label: string
  state: "allowed" | "denied"
}

export interface ReportGroup {
  key: "categories" | "channels" | "wordpress" | "users" | "automation" | "general"
  title: string
  lines: ReportLine[]
  // The resources this group reaches: null for a group with no resource scope,
  // and a count of 0 meaning the whole kind.
  scope: { count: number; label: string; names: string[] } | null
}

export interface PermissionsReport {
  groups: ReportGroup[]
  // Whether the draft differs from what is stored: the report reads the draft,
  // so an edited set is reported as edited and never as saved.
  unsaved: boolean
}

const scopeTitles: Record<ScopeKind, string> = {
  categories: "دسته‌بندی‌ها",
  channels: "کانال‌ها",
  wordpress: "سایت‌های وردپرسی",
}

const scopeNouns: Record<ScopeKind, string> = {
  categories: "دسته‌بندی",
  channels: "کانال",
  wordpress: "سایت وردپرسی",
}

// permissionScopeOf is what a group's viewing permission actually reaches, as
// the backend resolves it: denied without the flag, every item with the flag and
// an empty selection, and exactly the picks otherwise.
export function permissionScopeOf(
  draft: PermissionDraft,
  kind: ScopeKind,
  items?: ScopeItem[] | null
): { state: "allowed" | "denied"; count: number } {
  void items
  if (!holdsGlobal(draft, viewPermission[kind]))
    return { state: "denied", count: 0 }
  return { state: "allowed", count: scopeIDsOf(draft, kind).length }
}

export function buildReport(
  draft: PermissionDraft,
  catalog: ScopeCatalog,
  loaded?: PermissionsPayload | null
): PermissionsReport {
  const groups: ReportGroup[] = []

  for (const kind of scopeKinds) {
    const ids = scopeIDsOf(draft, kind)
    const items = catalogOf[kind](catalog) ?? []
    groups.push({
      key: kind,
      title: scopeTitles[kind],
      lines: [
        {
          key: viewPermission[kind],
          label: globalPermissionLabels[viewPermission[kind]],
          state: holdsGlobal(draft, viewPermission[kind])
            ? "allowed"
            : "denied",
        },
        {
          key: managePermission[kind],
          label: globalPermissionLabels[managePermission[kind]],
          state: holdsGlobal(draft, managePermission[kind])
            ? "allowed"
            : "denied",
        },
      ],
      scope: holdsGlobal(draft, viewPermission[kind])
        ? {
            count: ids.length,
            label: scopeNouns[kind],
            names: ids.map(
              (id) => items.find((item) => item.id === id)?.label ?? String(id)
            ),
          }
        : null,
    })
  }

  groups.push({
    key: "users",
    title: "مدیرها",
    lines: [
      {
        key: "users.view",
        label: globalPermissionLabels["users.view"],
        state: holdsGlobal(draft, "users.view") ? "allowed" : "denied",
      },
      {
        key: "users.manage",
        label: globalPermissionLabels["users.manage"],
        state: holdsGlobal(draft, "users.manage") ? "allowed" : "denied",
      },
    ],
    scope: null,
  })

  groups.push({
    key: "automation",
    title: "اتوماسیون",
    // Reported, and kept across saves, though this dialog has no editor for
    // them: like content.publish below, a save must never drop a permission it
    // was not asked about.
    lines: [
      {
        key: "automation.view",
        label: globalPermissionLabels["automation.view"],
        state: holdsGlobal(draft, "automation.view") ? "allowed" : "denied",
      },
      {
        key: "webhooks.manage",
        label: globalPermissionLabels["webhooks.manage"],
        state: holdsGlobal(draft, "webhooks.manage") ? "allowed" : "denied",
      },
      {
        key: "workflows.manage",
        label: globalPermissionLabels["workflows.manage"],
        state: holdsGlobal(draft, "workflows.manage") ? "allowed" : "denied",
      },
    ],
    scope: null,
  })

  groups.push({
    key: "general",
    title: "عمومی",
    lines: [
      {
        key: "servers.view",
        label: globalPermissionLabels["servers.view"],
        state: holdsGlobal(draft, "servers.view") ? "allowed" : "denied",
      },
      // Kept in the draft and reported, though this dialog has no editor for it:
      // a save must never drop a permission it was not asked about.
      {
        key: "content.publish",
        label: globalPermissionLabels["content.publish"],
        state: holdsGlobal(draft, "content.publish") ? "allowed" : "denied",
      },
    ],
    scope: null,
  })

  return {
    groups,
    unsaved: loaded ? !draftEquals(draft, normalizeDraft(loaded)) : false,
  }
}
