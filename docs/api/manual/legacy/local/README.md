<!-- archived: the page this documentation replaced -->
# Local API

Routes under `/api/v1/local/...` serve this application's own data from the
daemon's SQLite database. They never reach a worker or Telegram, and they keep
answering while every Telegram client is down.

- They are **not** namespaced by a client key.
- Every request needs a valid credential (see [Authentication](../authentication.md));
  what the caller may read or change is documented per route.
- Writes take a JSON body with `Content-Type: application/json`. The decoder
  rejects unknown fields, a trailing second JSON value, and non-JSON content
  types, so a misspelled field is reported instead of silently ignored.
- Body limits: 16 KiB for categories, 64 KiB for users. A larger body fails to
  decode (`400 invalid_input`).
- The database is owned by the daemon (one connection, WAL, foreign keys on).
  See [docs/CHANNELS.md](../../../../CHANNELS.md).

| Document | Routes |
| --- | --- |
| [categories.md(categories.md) | `GET/POST /categories`, `GET/PUT/DELETE /categories/{id}` |
| [users.md](users.md) | `GET/POST /users`, `GET/PUT/DELETE /users/{id}`, `GET/PUT /users/{id}/permissions` |

## Storage not configured

If the daemon was started without a local store (only possible in tests or a
misconfigured embedding), each group answers `503 worker_unavailable`
("Local category storage is not configured." / "Local user storage is not
configured.").
