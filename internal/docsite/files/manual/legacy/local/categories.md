<!-- archived: the page this documentation replaced -->
# Categories

Categories are an operator's own labels for channels. They live only in the
local SQLite database: creating one does **not** create a Telegram folder, join
a channel or read any message. A category can list any channel, including one
this application has not joined.

- [List categories](#list-categories) — `GET /api/v1/local/categories`
- [Create a category](#create-a-category) — `POST /api/v1/local/categories`
- [Get a category](#get-a-category) — `GET /api/v1/local/categories/{id}`
- [Replace a category](#replace-a-category) — `PUT /api/v1/local/categories/{id}`
- [Delete a category](#delete-a-category) — `DELETE /api/v1/local/categories/{id}`

`{id}` is the category's **local** row id (an integer), not a Telegram id.

Assigning a channel to a category has repository methods (`AssignChannel`,
`ListAssignments`) but **no HTTP route** as of this writing.

---

## List categories

### `GET /api/v1/local/categories?page=1&per_page=20&q=`

- **Authentication:** any valid credential. With `categories.view` the whole
  table is listed; without it the page is drawn from exactly the categories the
  caller holds `view` on (so `total`, `page` and `per_page` describe what the
  caller may read).
- **Query:** `page`, `per_page`, `q` — see [Pagination](../pagination.md).
  `q` filters name **or** description, case-insensitively, as a literal
  substring; at most 500 characters.
- **Order:** `created_at DESC, id DESC`.

### Success — `200`

```json
{
  "data": {
    "categories": [
      {
        "id": 1,
        "name": "Technology",
        "description": "Technology channels",
        "created_at": "2026-10-25T10:00:00Z",
        "updated_at": "2026-10-25T10:00:00Z"
      }
    ]
  },
  "meta": {
    "request_id": "…", "generated_at": "…",
    "page": 1, "per_page": 20, "total": 1, "total_pages": 1, "has_more": false
  }
}
```

An empty result is `200` with `"categories": []` and `"total": 0`.

### Errors

| Status | `code` | When |
| --- | --- | --- |
| 400 | `invalid_input` | bad `page`/`per_page`, `q` longer than 500, unknown query parameter |
| 401 | `unauthorized` | missing/invalid credential |
| 500 | `store_error` | the database failed |

### Example

```bash
curl --fail-with-body --silent --show-error \
  --get "${BASE_URL}/api/v1/local/categories" \
  --data-urlencode "q=technology" \
  --data-urlencode "page=1" \
  --data-urlencode "per_page=10" \
  --header "Accept: application/json" \
  --header "Authorization: Bearer ${TELESYNC_TOKEN}"
```

---

## Create a category

### `POST /api/v1/local/categories`

- **Authentication:** `categories.create`; otherwise `403 forbidden`.

### Request

```json
{
  "name": "Technology",
  "description": "Technology channels"
}
```

| Field | Type | Required | Rules |
| --- | --- | --- | --- |
| `name` | string | yes | trimmed; non-empty; ≤ 100 characters |
| `description` | string | no (defaults `""`) | trimmed; ≤ 500 characters |

Names are **unique ignoring ASCII case** (`UNIQUE(name)` with `COLLATE NOCASE`),
so `"technology"` after `"Technology"` is `409 conflict`. Non-ASCII case
variants are distinct names, which is what SQLite's `NOCASE` does.

### Success — `201`

```json
{
  "data": {
    "category": {
      "id": 1,
      "name": "Technology",
      "description": "Technology channels",
      "created_at": "2026-10-25T10:00:00Z",
      "updated_at": "2026-10-25T10:00:00Z"
    }
  },
  "meta": {"request_id": "…", "generated_at": "…"}
}
```

No channel assignment is created.

### Errors

| Status | `code` | When |
| --- | --- | --- |
| 400 | `invalid_input` | missing `name`, empty name after trimming, too long, unknown field, malformed JSON, trailing content |
| 401 | `unauthorized` | missing/invalid credential |
| 403 | `forbidden` | no `categories.create` |
| 409 | `conflict` | a category with that name exists (ignoring case) |
| 415 | `invalid_input` | `Content-Type` is not `application/json` |

### Example

```bash
curl --fail-with-body --silent --show-error \
  --request POST \
  "${BASE_URL}/api/v1/local/categories" \
  --header "Accept: application/json" \
  --header "Content-Type: application/json" \
  --header "Authorization: Bearer ${TELESYNC_TOKEN}" \
  --data '{
    "name": "Technology",
    "description": "Technology channels"
  }'
```

---

## Get a category

### `GET /api/v1/local/categories/{id}`

- **Authentication:** category-level `view`: an explicit assignment for this
  category, otherwise the global `categories.view`.
- **Path parameter:** `id` — a positive integer; otherwise `400 invalid_input`
  "id must be a positive integer."

### Success — `200`

```json
{
  "data": { "category": { "id": 1, "name": "Technology", "description": "", "created_at": "…", "updated_at": "…" } },
  "meta": {"request_id": "…", "generated_at": "…"}
}
```

### Errors

| Status | `code` | When |
| --- | --- | --- |
| 400 | `invalid_input` | id is not a positive integer |
| 403 | `forbidden` | the category exists but the caller may not view it |
| 404 | `not_found` | no category has that id |

The existence check runs **before** the permission check: a category that does
not exist is `404` to everyone, and one that exists but is not viewable is
`403`.

### Example

```bash
curl --fail-with-body --silent --show-error \
  "${BASE_URL}/api/v1/local/categories/1" \
  --header "Accept: application/json" \
  --header "Authorization: Bearer ${TELESYNC_TOKEN}"
```

---

## Replace a category

### `PUT /api/v1/local/categories/{id}`

`PUT` **replaces** the category: both fields are required, so `description:
""` clears it. `created_at` is kept; `updated_at` is refreshed.

- **Authentication:** an explicit category `update` assignment. The global
  permissions do **not** grant update; the operator token does.
- **Path parameter:** `id` — positive integer.

### Request

```json
{
  "name": "Tech",
  "description": ""
}
```

Both `name` and `description` must be present (the same shape as create). An
omitted field is `400 invalid_input` "A full category is required: name and
description." Channel assignments are never touched by an update. A rename that
only changes the case of the category's own name is not a conflict.

### Success — `200`

`{"data": {"category": {…}}, "meta": {…}}` with the stored category, as in
[Get a category](#success--200).

### Errors

As for create (`400`, `415`, `409`, `403`), plus `404 not_found` when the id does
not exist.

### Example

```bash
curl --fail-with-body --silent --show-error \
  --request PUT \
  "${BASE_URL}/api/v1/local/categories/1" \
  --header "Accept: application/json" \
  --header "Content-Type: application/json" \
  --header "Authorization: Bearer ${TELESYNC_TOKEN}" \
  --data '{
    "name": "Tech",
    "description": "All technology channels"
  }'
```

---

## Delete a category

### `DELETE /api/v1/local/categories/{id}`

> **Effect:** removes the category row and, in the same transaction, every
> assignment that references it (`channel_categories`, via `ON DELETE CASCADE`
> plus an explicit delete). **Nothing else changes**: no Telegram state, no
> managed-channel row, no session, no user.

- **Authentication:** an explicit category `delete` assignment.
- **Method:** only `GET`, `PUT`, `DELETE` are served on this path; anything else
  is `405` with `Allow: GET, PUT, DELETE`.

### Success — `200`

```json
{
  "data": {"deleted": true, "id": 1},
  "meta": {"request_id": "…", "generated_at": "…"}
}
```

### Errors

| Status | `code` | When |
| --- | --- | --- |
| 400 | `invalid_input` | id is not a positive integer |
| 403 | `forbidden` | no category `delete` assignment |
| 404 | `not_found` | no category with that id (including a second delete) |

### Example

```bash
curl --fail-with-body --silent --show-error \
  --request DELETE \
  "${BASE_URL}/api/v1/local/categories/1" \
  --header "Accept: application/json" \
  --header "Authorization: Bearer ${TELESYNC_TOKEN}"
```

Related: [Category schema(../schemas/local.md#category-object), [Users](users.md).
