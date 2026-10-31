<!-- archived: the page this documentation replaced -->
# Users

Operator accounts for this API. They are rows in the local SQLite database and
have nothing to do with Telegram accounts: a Telegram account is reported by
[`GET /api/v1/telegram/{key}/profile`](../telegram/profile.md).

- [List users](#list-users) — `GET /api/v1/local/users`
- [Create a user](#create-a-user) — `POST /api/v1/local/users`
- [Get a user](#get-a-user) — `GET /api/v1/local/users/{id}`
- [Update a user](#update-a-user) — `PUT /api/v1/local/users/{id}`
- [Delete a user](#delete-a-user) — `DELETE /api/v1/local/users/{id}`
- [Read permissions](#read-permissions) — `GET /api/v1/local/users/{id}/permissions`
- [Replace permissions](#replace-permissions) — `PUT /api/v1/local/users/{id}/permissions`

`{id}` is the local user id. Passwords are stored as Argon2id hashes and are
**never** returned; `password_hash` is not an accepted field anywhere.

The permission model is in [Authentication](../authentication.md#permissions).

## The first account (created with the CLI)

Every write here needs `users.manage`, which only an existing account can hold,
and there is no HTTP registration route. The first account — and any later
administrator — is created with:

```bash
telesync admin [--config <path>]
```

```text
Username: admin
Full name [admin]: Administrator
Password (input hidden):
Repeat the password:
created administrator "admin" (id 1)
  permissions: [users.view users.manage channels.view channels.create categories.view categories.create]
```

- The password is typed twice, read without echo on a terminal, and stored only
  as an Argon2id hash; it is never printed.
- The account gets **every** global permission, so it is a full administrator.
- The database is `<runtime.data_dir>/channels.db` and is created if missing.
  Stop the daemon while the command runs if the daemon already uses that file.
- Re-running it for an existing username reactivates the account, resets its
  password and adds missing permissions (it never removes one, never creates a
  duplicate); that makes it the recovery path for a forgotten password.
- Nothing else is created: every later account goes through `POST
  /api/v1/local/users`, every permission through `PUT
  /api/v1/local/users/{id}/permissions`.

Compare with `telesync hash-password`, which only prints a hash and is for
scripts; it needs no database.

---

## List users

### `GET /api/v1/local/users?page=1&per_page=20&q=`

- **Authentication:** any valid credential.
  - With `users.view`: every account.
  - Without it: the caller's own account, plus — if the caller holds
    `users.manage` — every account holding at least one permission (the accounts
    it could administer). A caller with neither sees only itself.
- **Query:** `page`, `per_page`, `q` — see [Pagination](../pagination.md).
  `q` matches username **or** full name, case-insensitively, literally; at most
  200 characters.
- **Order:** `created_at DESC, id DESC`.

### Success — `200`

```json
{
  "data": {
    "users": [
      {
        "id": 1,
        "fullname": "Administrator",
        "username": "admin",
        "is_active": true,
        "created_at": "2026-10-25T09:00:00Z",
        "updated_at": "2026-10-25T09:00:00Z"
      }
    ]
  },
  "meta": {
    "request_id": "…", "generated_at": "…",
    "page": 1, "per_page": 20, "total": 1, "total_pages": 1, "has_more": false
  }
}
```

The list never includes `permissions`; use [Get a user](#get-a-user) or
[Read permissions](#read-permissions) for that. `total` counts the rows the
caller is allowed to see.

### Errors

| Status | `code` | When |
| --- | --- | --- |
| 400 | `invalid_input` | bad `page`/`per_page`, `q` longer than 200, unknown query parameter |
| 401 | `unauthorized` | missing/invalid credential, or the operator token |

### Example

```bash
curl --fail-with-body --silent --show-error \
  --get "${BASE_URL}/api/v1/local/users" \
  --data-urlencode "q=admin" \
  --header "Accept: application/json" \
  --header "Authorization: Bearer ${TELESYNC_TOKEN}"
```

---

## Create a user

### `POST /api/v1/local/users`

- **Authentication:** `users.manage`; otherwise `403 forbidden`.

### Request

```json
{
  "fullname": "Example Operator",
  "username": "example-user",
  "password": "example password 123",
  "is_active": true
}
```

| Field | Type | Required | Rules |
| --- | --- | --- | --- |
| `fullname` | string | yes | trimmed; non-empty; ≤ 200 characters |
| `username` | string | yes | trimmed; non-empty; ≤ 64 characters; ASCII letters, digits, `.`, `_`, `-` only; unique ignoring case |
| `password` | string | yes | 8–1024 characters; stored as an Argon2id hash |
| `is_active` | boolean | no (default `true`) | an inactive account cannot log in |

### Success — `201`

```json
{
  "data": {
    "user": {
      "id": 2,
      "fullname": "Example Operator",
      "username": "example-user",
      "is_active": true,
      "created_at": "2026-10-25T10:00:00Z",
      "updated_at": "2026-10-25T10:00:00Z"
    }
  },
  "meta": {"request_id": "…", "generated_at": "…"}
}
```

The new account has **no permissions**. Give it some with
[Replace permissions](#replace-permissions).

### Errors

| Status | `code` | When |
| --- | --- | --- |
| 400 | `invalid_input` | missing/short/long password, invalid fullname or username, unknown field (e.g. `password_hash`), malformed JSON |
| 401 | `unauthorized` | missing/invalid credential, or the operator token |
| 403 | `forbidden` | no `users.manage` |
| 409 | `conflict` | username already in use (ignoring case) |
| 415 | `invalid_input` | `Content-Type` is not `application/json` |

### Example

```bash
curl --fail-with-body --silent --show-error \
  --request POST \
  "${BASE_URL}/api/v1/local/users" \
  --header "Accept: application/json" \
  --header "Content-Type: application/json" \
  --header "Authorization: Bearer ${TELESYNC_TOKEN}" \
  --data '{
    "fullname": "Example Operator",
    "username": "example-user",
    "password": "example password 123"
  }'
```

---

## Get a user

### `GET /api/v1/local/users/{id}`

- **Authentication:** `users.view`, or `users.manage`, or the caller's own id;
  otherwise `403 forbidden`.
- **Path parameter:** positive integer; otherwise `400 invalid_input`.

### Success — `200`

```json
{
  "data": {
    "user": {
      "id": 1,
      "fullname": "Administrator",
      "username": "admin",
      "is_active": true,
      "created_at": "2026-10-25T09:00:00Z",
      "updated_at": "2026-10-25T09:00:00Z",
      "permissions": {
        "global": ["users.view", "channels.view"],
        "channels": [ {"channel_id": 3, "view": "allow"} ],
        "categories": [ {"category_id": 1, "view": "deny"} ]
      }
    }
  },
  "meta": {"request_id": "…", "generated_at": "…"}
}
```

Same `User` shape as [`GET /api/v1/auth/me`](../auth.md#get-apiv1authme), with
`permissions` described there.

### Errors

| Status | `code` | When |
| --- | --- | --- |
| 400 | `invalid_input` | id is not a positive integer |
| 403 | `forbidden` | the caller may not read this account |
| 404 | `not_found` | no user has that id |

### Example

```bash
curl --fail-with-body --silent --show-error \
  "${BASE_URL}/api/v1/local/users/1" \
  --header "Accept: application/json" \
  --header "Authorization: Bearer ${TELESYNC_TOKEN}"
```

---

## Update a user

### `PUT /api/v1/local/users/{id}`

A **partial** update: only the fields you send change; omitted ones stay. At
least one field must be sent.

- **Authentication:** `users.manage`.

### Request

```json
{
  "fullname": "Example O.",
  "is_active": false
}
```

| Field | Type | Rules |
| --- | --- | --- |
| `fullname` | string | as on create |
| `username` | string | as on create; unique ignoring case |
| `password` | string | 8–1024 characters; replaces the hash |
| `is_active` | boolean | `false` deactivates the account |

`password_hash` is not accepted. Sending none of the four fields is `400
invalid_input` "Name at least one of fullname, username, password or
is_active.".

### Rules

- You cannot deactivate your own account (`400 invalid_input`).
- Deactivating the **last active account holding `users.manage`** is `409
  conflict`; promote another account first.
- A deactivated or deleted account's tokens stop working on its next request.

### Success — `200`

The stored account with its permissions, as in [Get a user](#success--200)
above.

### Errors

As for [Get a user](#errors) (400 for a bad id, 403 without `users.manage`,
404 for an unknown id), plus `409 conflict` for a duplicate username or the last
administrator.

### Example

```bash
curl --fail-with-body --silent --show-error \
  --request PUT \
  "${BASE_URL}/api/v1/local/users/{id}" \
  --header "Accept: application/json" \
  --header "Content-Type: application/json" \
  --header "Authorization: Bearer ${TELESYNC_TOKEN}" \
  --data '{
    "fullname": "Example O."
  }'
```

Replace `{id}` with the account you created (the `id` in its `201` response).

---

## Delete a user

### `DELETE /api/v1/local/users/{id}`

> **Effect:** removes the account row; its global and per-resource assignments
> are removed by the foreign-key cascade. Tokens it holds stop working on the
> next request.

- **Authentication:** `users.manage`.
- You cannot delete your own account (`400 invalid_input`).
- Deleting the last active account holding `users.manage` is `409 conflict`.

### Success — `200`

```json
{
  "data": {"deleted": true, "id": 2},
  "meta": {"request_id": "…", "generated_at": "…"}
}
```

### Errors

| Status | `code` | When |
| --- | --- | --- |
| 400 | `invalid_input` | bad id, or the caller's own id |
| 403 | `forbidden` | no `users.manage` |
| 404 | `not_found` | no user with that id |
| 409 | `conflict` | the last active user administrator |

### Example

```bash
curl --fail-with-body --silent --show-error \
  --request DELETE \
  "${BASE_URL}/api/v1/local/users/{id}" \
  --header "Accept: application/json" \
  --header "Authorization: Bearer ${TELESYNC_TOKEN}"
```

---

## Read permissions

### `GET /api/v1/local/users/{id}/permissions`

- **Authentication:** the caller's own id, or `users.view`, or `users.manage`.

### Success — `200`

```json
{
  "data": {
    "permissions": {
      "global": ["users.view"],
      "channels": [ {"channel_id": 3, "view": "allow", "update": "deny"} ],
      "categories": []
    }
  },
  "meta": {"request_id": "…", "generated_at": "…"}
}
```

The stored set, as is: a `deny` stays visible as a `deny`. `channel_id` and
`category_id` are local row ids. `global` is sorted by name; `channels` and
`categories` are ordered by their id and their actions within an entry are in no
particular order.

### Errors

| Status | `code` | When |
| --- | --- | --- |
| 400 | `invalid_input` | id is not a positive integer |
| 403 | `forbidden` | none of own id / `users.view` / `users.manage` |
| 404 | `not_found` | no user with that id |

### Example

```bash
curl --fail-with-body --silent --show-error \
  "${BASE_URL}/api/v1/local/users/1/permissions" \
  --header "Accept: application/json" \
  --header "Authorization: Bearer ${TELESYNC_TOKEN}"
```

---

## Replace permissions

### `PUT /api/v1/local/users/{id}/permissions`

**Replaces the whole set.** Anything the body does not list is removed.

- **Authentication:** `users.manage`, and the target must **not** be the caller.
  Changing your own set is `403 forbidden` ("You cannot change your own
  permissions; ask another administrator."), so an administrator cannot widen
  their own access.

### Request

```json
{
  "global_permissions": ["channels.view", "categories.view"],
  "channel_permissions": [
    {"channel_id": 3, "permissions": {"view": "allow", "update": "deny"}}
  ],
  "category_permissions": [
    {"category_id": 1, "permissions": {"view": "allow"}}
  ]
}
```

| Field | Type | Required | Rules |
| --- | --- | --- | --- |
| `global_permissions` | array of string | no (default `[]`) | each a known global permission; no duplicates |
| `channel_permissions` | array of object | no | `channel_id`: positive **local** `managed_channels` id that exists; `permissions`: map of action → effect |
| `category_permissions` | array of object | no | `category_id`: positive local category id that exists; same map |

- Actions per resource: `view`, `update`, `delete`. `create` is not accepted in a
  resource entry: creating a channel or category is a global permission.
- Effects: `"allow"` or `"deny"`.
- A resource id listed twice is rejected.
- Every referenced channel and category must exist; a set naming one that does
  not changes **nothing** (validated before any write, then written in one
  transaction).
- An empty `permissions` map for a resource is allowed; it stores no assignment.

### Success — `200`

The stored set after the write:

```json
{
  "data": {
    "permissions": {
      "global": ["categories.view", "channels.view"],
      "channels": [ {"channel_id": 3, "view": "allow", "update": "deny"} ],
      "categories": [ {"category_id": 1, "view": "allow"} ]
    }
  },
  "meta": {"request_id": "…", "generated_at": "…"}
}
```

`global` comes back sorted by name; do not rely on the order within an entry's
actions.

### Errors

| Status | `code` | When |
| --- | --- | --- |
| 400 | `invalid_input` | unknown global permission, unknown action, bad effect, missing/non-positive id, duplicated id or permission, a resource id that does not exist |
| 403 | `forbidden` | target is the caller, or no `users.manage` |
| 404 | `not_found` | no user has that id |
| 409 | `conflict` | the set would leave no active account holding `users.manage` |

### Example

```bash
curl --fail-with-body --silent --show-error \
  --request PUT \
  "${BASE_URL}/api/v1/local/users/{id}/permissions" \
  --header "Accept: application/json" \
  --header "Content-Type: application/json" \
  --header "Authorization: Bearer ${TELESYNC_TOKEN}" \
  --data '{
    "global_permissions": ["channels.view", "categories.view"],
    "channel_permissions": [
      {"channel_id": 3, "permissions": {"view": "allow"}}
    ],
    "category_permissions": []
  }'
```

`channel_id: 3` is a **local** `managed_channels` row, created by
`POST .../join` — not Telegram's channel id. Replace `{id}` with the account you
are granting to.

Related: [User schema(../schemas/local.md#user-object), [Authentication](../authentication.md#permissions).
