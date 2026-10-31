<!-- component: categories -->
<!-- manual: categories -->
# Categories

A category is an operator's own label for channels, stored only in the local
SQLite database. Creating one does **not** create a Telegram folder, join a
channel or read any message, and a category may name a channel this application
has never joined. These routes answer from SQLite alone, so they keep working
while every worker is down.

## Objects

A category is `{"id", "name", "description", "created_at", "updated_at"}`:
`id` is a local row id; `name` is trimmed, non-empty and at most 100 characters;
`description` is trimmed, may be empty and is at most 500 characters; both
timestamps are RFC 3339 UTC.

## Rules

- **Create** takes `{"name", "description"?}` and answers `201`.
- **Replace** (`PUT`) takes both fields, so `"description": ""` clears it;
  `created_at` is kept and `updated_at` refreshed.
- Names are unique ignoring ASCII case (SQLite `NOCASE`), so `"technology"`
  after `"Technology"` is `409 conflict`; non-ASCII case variants stay distinct.
- Bodies must be `application/json` (otherwise `415`); unknown fields and
  trailing JSON are rejected rather than ignored.
- **Delete** removes the category and its channel assignments in one
  transaction. Nothing else changes: no Telegram state, no managed-channel row,
  no user. A second delete is `404 not_found`.
- Access: `categories.create` to create; a category `view`, `update` or `delete`
  assignment for the per-category routes. The global permissions do not grant
  update or delete; the operator token does.
- Assigning a channel to a category has repository methods but **no HTTP route**.

## `GET /api/v1/local/categories/{id}/channels`

The managed channels filed under one category, ordered by title. `404` when the
category does not exist; a caller holding only per-category rights must be
allowed to `view` it (`403` otherwise). The response is
`{"data":{"channels":[...]}}` with the same channel objects as
`GET /api/v1/local/channels`.

A channel filed through discovery (the `join` route) is in this list once its
record exists; an assignment whose channel is not managed has no row to show
and is not invented.

## `GET` / `PUT /api/v1/local/channels/{id}/categories`

The categories one managed channel is filed under. The channel's own route
sets them, because the channel is what is known to exist: a caller files only a
channel it may view (`404` when there is none, `403` when it may not see it).

- **`GET`** returns `{"data":{"categories":[...]}}`, the same category objects
  as `GET /api/v1/local/categories`.
- **`PUT`** sets the complete list: `{"category_ids":[1,2]}`. It is one
  transaction, so either the whole set is written or nothing is:

  - every id must name a category the caller may view (`404` for none, `403`
    for one it may not);
  - a category already holding the channel stays (filing twice is a no-op);
  - a category not in the list no longer holds it;
  - `[]` files it nowhere; `null` is `400`, a repeated id is `400`.

  The response is the new list, in the shape of the `GET` above.
