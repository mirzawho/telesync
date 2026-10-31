<!-- archived: the page this documentation replaced -->
# Pagination

Five routes page their results: `GET .../channels`, `GET .../channels/{id}/messages`,
`GET /api/v1/local/categories` and `GET /api/v1/local/users` (and
`GET /api/v1/telegram/{key}/channels/find`, which does **not** page — see the
end of this page). `.../channels/{id}` and `.../messages/{id}` return one object
with no pagination metadata.

## Query parameters

| Parameter | Type | Default | Valid values | Invalid → |
| --- | --- | --- | --- | --- |
| `page` | integer | `1` | ≥ 1 | `400 invalid_input` "page must be an integer of at least 1." |
| `per_page` | integer | `20` | 1–100 | `400 invalid_input` "per_page must be an integer between 1 and 100." |
| `q` | string | — | category/user lists only; ≤ 500 characters there, ≤ 200 for users | `400 invalid_input` |

Rules that apply to every paged route:

- A parameter given more than once (`?page=1&page=2`) is `400`.
- An **unknown** query parameter is `400 "unknown query parameter \"…\"."`, so a
  typo cannot silently fall back to a default.
- Values are not trimmed or clamped; `page=0`, `per_page=101` and `page=abc` are
  all rejected, not corrected.

## Response metadata

Every paged response carries `meta`:

```json
{
  "meta": {
    "request_id": "…",
    "generated_at": "2026-10-25T10:00:00Z",
    "page": 2,
    "per_page": 20,
    "total": 57,
    "has_more": true,
    "next_page": 3
  }
}
```

| Field | Telegram `channels` | Telegram `messages` | Local `categories` / `users` |
| --- | --- | --- | --- |
| `page` | echoed request | echoed request (see caveat) | echoed request |
| `per_page` | echoed request | echoed request | echoed request |
| `total` | number of dialogs, after authorization filtering | **absent** | number of matching rows |
| `has_more` | `true` when more dialogs follow | `true` when more messages follow | `page*per_page < total` |
| `next_page` | present when `has_more` | present when `has_more` | **absent** (compute `page+1`) |
| `total_pages` | absent | absent | present, `ceil(total/per_page)` |

A page past the end is `200` with an empty array, not an error.

## Ordering

| Collection | Order |
| --- | --- |
| Channels | Telegram's dialog order (most recently active first). Stable only within the snapshot window below. |
| Messages | newest first; page N+1 continues strictly below the oldest id on page N |
| Categories | `created_at DESC, id DESC` — stable while nothing is created |
| Users | `created_at DESC, id DESC` |

## Telegram channels: a snapshot, not a cursor

Telegram has no page index for dialogs. The worker downloads the dialog list,
keeps it for up to 60 seconds (`worker.Options.CacheTTL`; the daemon uses the
default), and slices each page from that snapshot:

- Pages read inside one 60 s window are consistent with each other.
- A dialog added or removed mid-walk shifts later pages; there is no cursor to
  resume from.
- The snapshot is per worker, so it is discarded when the worker restarts.

`total` is Telegram's dialog count. When the caller lacks `channels.view` and
channels are filtered out (see
[the channel rule](authentication.md#the-channel-rule)), `total` is restated as
the number of channels on the returned page, while `has_more` stays Telegram's
answer. Treat `total` on `/channels` as a hint when per-channel permissions are in
play.

## Telegram messages: page N is only as reliable as the pages before it

Messages are paged by **message id**. The worker maps `page` onto a `before_id`
cursor: it remembers, per channel, the id to page below (a bounded map of 64
channels), and page N+1 continues below the oldest message served for page N.

- Page N requested without pages 1..N-1 having been fetched by **this worker
  process** restarts from the newest page. `meta.page` echoes what was asked for;
  the content is the newest page.
- After a worker restart, a deep page also restarts from newest.
- To resume reliably, treat page 1 as "newest" and walk forward, or use the
  returned message ids as your own cursor (`id` values are decreasing).
- There is no `total` for messages: Telegram does not report a channel's message
  count, and the API does not invent one.

## Local categories and users

Plain offset pagination over SQLite: `page` and `per_page` become `LIMIT/OFFSET`.
No caching, no cursors.

- `q` filters categories on name **or** description and users on username **or**
  full name, case-insensitively, as a literal substring (`%` and `_` are not
  wildcards; the store uses `instr(lower(...), lower(?))`). The lower-casing is
  SQLite's `lower()`, which folds ASCII only; non-ASCII case variants do not
  match each other.
- Categories: without `categories.view`, the page is built from exactly the
  categories the caller may view, then filtered and sliced, so `page`, `per_page`
  and `total` describe what the caller may read.
- Users: without `users.view`, the list is narrowed to the caller and (with
  `users.manage`) accounts holding any permission.
- Because ordering is `created_at DESC`, inserting a category or user while
  walking pages can move an item between pages.

## `channels/find` does not page

`GET .../channels/find?q=…` returns every result from a single search, with
`meta.count` and no `page`, `per_page`, `has_more` or `next_page`. Each Telegram
search method returns at most 20 results; the API does not paginate further.
Passing `page` or `per_page` to it is `400` (only `q` is accepted).
