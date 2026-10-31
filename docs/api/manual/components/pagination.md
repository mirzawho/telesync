<!-- component: pagination -->
<!-- manual: pagination -->
# Pagination

`GET .../channels`, `GET .../channels/{id}/messages`, `GET /api/v1/local/channels`,
`GET /api/v1/local/channels/{id}/messages`, `GET /api/v1/local/categories`
and `GET /api/v1/local/users` page their results. `find` does not page, and the
single-channel and single-message routes carry no pagination.

## Parameters

| Parameter | Type | Default | Valid | Invalid → |
| --- | --- | --- | --- | --- |
| `page` | integer | 1 | ≥ 1 | `400 invalid_input` |
| `per_page` | integer | 20 | 1–100 | `400 invalid_input` |
| `q` | string | — | ≤ 500 (categories, local channels), ≤ 200 (users) | `400 invalid_input` |

A parameter given twice is `400`, an unknown parameter is `400`, and values are
not trimmed or clamped: `page=0`, `per_page=101`, `page=abc` are all refused.

## Metadata

```json
{"request_id": "…", "generated_at": "…", "page": 2, "per_page": 20, "total": 57, "has_more": true, "next_page": 3}
```

| Field | Channels | Messages | Categories / users |
| --- | --- | --- | --- |
| `total` | dialogs, after filtering | **absent** | matching rows (local channels: matching records the caller may see) |
| `has_more` | more dialogs follow | more messages follow | `page*per_page < total` |
| `next_page` | when `has_more` | when `has_more` | absent (compute `page+1`) |
| `total_pages` | absent | absent | present |

A page past the end is `200` with an empty array.

## Telegram has no page index

Channels are sliced from a dialog snapshot the worker reuses for up to 60 s:
pages inside one window are consistent, a dialog added or removed mid-walk
shifts later pages, and the snapshot is discarded when the worker restarts.

Messages page by message id. The worker remembers, per channel, the id to page
below (a bounded map of 64 channels). A page requested without the earlier pages
having been fetched **by this worker process** restarts from the newest page —
`meta.page` echoes what you asked, the content is the newest page — and a worker
restart loses the cursors. Resume by walking from page 1, or keep the returned
`id` values as your own cursor. There is no `total` for messages: Telegram does
not report a channel's message count.

## Local lists

Categories and users are plain `LIMIT/OFFSET` pages ordered `created_at DESC,
id DESC`, so inserting while walking can move an item between pages. `q` is a
literal, case-insensitive substring over two columns (`instr(lower(...))`); `%`
and `_` are not wildcards, and the folding is ASCII-only, as SQLite's `lower`
is. Without `categories.view` or `users.view` the page is built from what the
caller may read, so `page`, `per_page` and `total` describe that.

Local channels are the same `LIMIT/OFFSET` shape, ordered by Telegram's
`channel_id DESC, id DESC` (a stable order that does not move when a record is
refreshed by a repeated join), searched over title, username and client key.
Their `/messages` page is Telegram's history, so it follows the message rules
above and has no `total`.

`find` returns one array per search with `meta.count`; passing `page` or
`per_page` to it is `400`.
