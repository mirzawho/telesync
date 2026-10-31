# Public HTTP API v1 — moved

The API reference now lives in **[docs/api/](api/README.md)** and is checked
against the running gateway by `go test ./internal/apidocs`.

| Question | Where to look |
| --- | --- |
| Endpoints, methods and paths | [docs/api/README.md](api/README.md#route-index) |
| Login, tokens, permissions | [docs/api/authentication.md](api/authentication.md), [docs/api/auth.md](api/auth.md) |
| Telegram routes (health, profile, channels, messages) | [docs/api/telegram/](api/telegram/README.md) |
| Local routes (categories, users) | [docs/api/local/](api/local/README.md) |
| Field-by-field shapes | [docs/api/schemas/](api/schemas/README.md) |
| Pagination | [docs/api/pagination.md](api/pagination.md) |
| Errors and status codes | [docs/api/errors.md](api/errors.md) |
| Runnable `curl` examples | [docs/api/examples/](api/examples/README.md) |

The chain the API drives, unchanged:

```
HTTP client → gateway (public, 127.0.0.1 by default) → worker RPC (Unix socket) → MTProto
```

This file used to hold the full reference. It is kept only as a pointer; do not
add endpoint documentation here. The local database, the avatar cache and the
peer store are still described in [CHANNELS.md](CHANNELS.md).

## Initial administrator

Kept here because `docs/RUNTIME.md` links to it. The canonical copy is
[docs/api/local/users.md](api/local/users.md#the-first-account-created-with-the-cli).

There is no registration endpoint: `POST /api/v1/local/users` itself needs
`users.manage`, which only an existing account can hold. The first account —
and any later administrator, or the recovery of a forgotten administrator
password — is created with the CLI:

```bash
telesync admin          # add --config <path> if it is not the default file
```

```text
Username: admin
Full name [admin]: Administrator
Password (input hidden):
Repeat the password:
created administrator "admin" (id 1)
  permissions: [users.view users.manage channels.view channels.create categories.view categories.create]
  log in: curl -s -X POST <api>/api/v1/auth/login ... | jq -r .access_token
```

- It asks for the credentials; the password is typed twice, read **without
  echo** when the input is a terminal, and never printed, logged or stored — the
  account holds an Argon2id hash of it.
- It grants **every global permission** (`users.view`, `users.manage`,
  `channels.view`, `channels.create`, `categories.view`, `categories.create`),
  so the administrator can see and do everything the API offers. Per-channel
  and per-category overrides are still added through the permissions API.
- The database is `<runtime.data_dir>/channels.db` from the configuration, and
  is created if it does not exist yet, so the command may be the first thing
  you run. Stop the daemon while you run it if the daemon already uses that
  file.
- **Re-running it is safe and is the recovery path.** For an existing username
  it reactivates the account, replaces its password and adds any missing
  permission; it never removes a permission and never creates a second account.
  Because it grants everything, run it only on a machine you trust, as the
  operator.
- A password under 8 characters, an unsafe username or a mismatched repeat is
  refused before anything is written.

Then log in (`POST /api/v1/auth/login`) and keep the token. Other accounts are
created through `POST /api/v1/local/users`, with exactly the permissions you
grant them.
