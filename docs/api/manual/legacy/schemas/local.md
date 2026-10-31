<!-- archived: the page this documentation replaced -->
# Local objects

Objects returned and accepted by `/api/v1/local/...` and `/api/v1/auth/...`.
They come from the daemon's SQLite database (schema in
[docs/CHANNELS.md](../../../../CHANNELS.md)).

## Category object

| Field | Type | Server-generated | Notes |
| --- | --- | --- | --- |
| `id` | integer | yes | local row id |
| `name` | string | no | trimmed; non-empty; ≤ 100 characters; unique ignoring ASCII case |
| `description` | string | no | trimmed; `""` when none; ≤ 500 characters |
| `created_at` | string | yes | RFC 3339 UTC |
| `updated_at` | string | yes | RFC 3339 UTC; refreshed by `PUT` |

Create body: `{"name": <string>, "description": <string, optional>}`.

Replace body: `{"name": <string>, "description": <string>}` — both required.

## User object

| Field | Type | Server-generated | Notes |
| --- | --- | --- | --- |
| `id` | integer | yes | local row id; the JWT `sub` |
| `fullname` | string | no | trimmed; non-empty; ≤ 200 characters |
| `username` | string | no | trimmed; ≤ 64 characters; ASCII letters, digits, `.`, `_`, `-`; unique ignoring case |
| `is_active` | boolean | no | inactive accounts cannot log in |
| `created_at` | string | yes | RFC 3339 UTC |
| `updated_at` | string | yes | RFC 3339 UTC |
| `permissions` | [Permission set](#permission-set) | — | present on `GET /users/{id}`, `PUT /users/{id}`, `GET /auth/me`; **absent** in the list and on create |

The password is write-only (`password` in create/update bodies) and is never
rendered; `password_hash` is not an accepted field.

Create body: `{"fullname", "username", "password", "is_active"?}`.

Update body (partial, at least one field): `{"fullname"?, "username"?, "password"?, "is_active"?}`.

## Permission set

```json
{
  "global": ["channels.view"],
  "channels": [ {"channel_id": 3, "view": "allow", "update": "deny"} ],
  "categories": [ {"category_id": 1, "view": "deny"} ]
}
```

| Field | Type | Notes |
| --- | --- | --- |
| `global` | array of string | always an array (possibly empty), sorted by name on read |
| `channels` | array of [resource entry](#resource-entry) | only explicit assignments; ordered by `channel_id` on read |
| `categories` | array of [resource entry](#resource-entry) | only explicit assignments; ordered by `category_id` on read |

### Resource entry

| Field | Type | Notes |
| --- | --- | --- |
| `channel_id` **or** `category_id` | integer | **local** row id (`managed_channels` / `categories`), not a Telegram id |
| `view`, `update`, `delete` | `"allow"` \| `"deny"` | each **omitted** when there is no override for that action |

An omitted action has no override: the global permission for the kind, or its
absence, decides.

### Global permissions

`users.view`, `users.manage`, `channels.view`, `channels.create`,
`categories.view`, `categories.create` — described in
[Authentication](../authentication.md#permissions).

### Replace request body

`PUT /api/v1/local/users/{id}/permissions` takes the whole set:

```json
{
  "global_permissions": ["channels.view"],
  "channel_permissions": [
    {"channel_id": 3, "permissions": {"view": "allow", "update": "deny"}}
  ],
  "category_permissions": [
    {"category_id": 1, "permissions": {"view": "deny"}}
  ]
}
```

The response body uses the read shape (`data.permissions`, above). The request
shape differs on purpose: it groups effects per resource.

| Request field | Type | Rules |
| --- | --- | --- |
| `global_permissions` | array of string | known global permissions, no duplicates; `[]` clears them |
| `channel_permissions[].channel_id` | integer | existing local `managed_channels` id |
| `channel_permissions[].permissions` | object | action → `"allow"`/`"deny"`; actions `view`, `update`, `delete` only |
| `category_permissions[].category_id` | integer | existing local category id |
| `category_permissions[].permissions` | object | same actions |

Omitted arrays mean "none", so the write removes whatever they held.
