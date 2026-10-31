# Tasks

Status legend: `[x]` done and verified · `[~]` done, partially verified ·
`[ ]` not done · `[!]` blocked.

Scope: the multi-instance runtime only. The legacy two-profile checker, the
single-account panel, its SQLite store and the browser frontend were **removed**
on 2026-10-24; see `.agents/memory/LOG.md`.

## Contracts

### Configuration file (`internal/appconfig`)

JSON, loaded once at startup by the daemon and by every worker. Secrets are
referenced by environment-variable **name**. Full schema and validation rules:
[docs/CONFIGURATION.md](docs/CONFIGURATION.md). Sample:
[docs/telesync.example.json](docs/telesync.example.json).

### Worker lifecycle states

`starting → connected ⇄ disconnected → needs_auth`, plus `error` and
`stopping`. The supervisor reports a separate process view: `starting`,
`running`, `backoff`, `failed`, `stopped`.

### Internal RPC (`internal/rpc`)

HTTP/1.1 + JSON over each worker's Unix socket, mode `0600` in a `0700`
directory. `GET /health`, `/profile`, `/channels?page=&per_page=`,
`/channels/{id}/messages?page=&per_page=`, and `POST /find`, `/channel`,
`/join`, `/avatar` for discovery, membership and profile photos. Never a TCP
listener.

### Public HTTP API

`/api/v1/telegram/{key}/{health,profile,channels,channels/find,channels/{id},channels/{id}/join,channels/{id}/avatar,channels/{id}/messages}`
and `/healthz`. `{"data":…,"meta":…}` on success,
`{"error":{"code","message","request_id"}}` on failure. Contract:
[docs/api/README.md](docs/api/README.md), checked by `go test ./internal/apidocs`.

## Work items

### Runtime
- [x] `internal/appconfig` — JSON config, env-var secrets, path/security checks.
- [x] `internal/rpc` — private contract (DTOs, codes, server, typed client).
- [x] `internal/supervisor` — one child per client, readiness + liveness probes,
      bounded backoff with jitter, graceful stop, stale-socket cleanup.
- [x] `internal/worker` — health, profile, channels, messages; bounded caches;
      Unix socket server.
- [x] `internal/gateway` — config-driven routing, bearer auth, request IDs,
      error mapping, `/healthz`.
- [x] `internal/runtime` — `Registry`, `RunDaemon`, `RunWorker`.
- [x] `internal/profile.FromClient` — per-instance specs, official/custom
      isolation preserved.
- [x] CLI: `daemon`, `worker --key`, `auth --key [--timeout]`,
      `config validate [--json]`.
- [x] In-memory end-to-end test: gateway HTTP → worker RPC → real MTProto
      handshake against `tgtest`.
- [x] `gofmt`, `go vet`, `go test ./...`, `go build ./...` clean.
- [x] Independent review pass; findings applied.
- [!] Live run (real sockets, real credentials): blocked in this sandbox —
      binding any socket is denied. See `.agents/memory/STATE.md`.

### Channel discovery, join and avatars (2026-10-25)
- [x] `internal/tgapi` — query classification, channel metadata, one permission
      normalizer, dedupe; unit tests.
- [x] `internal/gotdclient` — `FindChannels`, `ResolveChannel`, `JoinChannel`,
      `DownloadChannelAvatar`; `tgtest` tests for every input form, pending
      joins, invite errors, access-hash resolution and avatar bytes.
- [x] `internal/peerstore` — per-worker channel access-hash memory.
- [x] `internal/channelstore` — SQLite (`modernc.org/sqlite`) with versioned
      migrations, `(client_key, channel_id)` uniqueness, concurrent-upsert test.
- [x] `internal/avatarcache` — 24 h persistent cache, atomic writes, single
      flight, stale window, cleanup; injectable clock.
- [x] `internal/rpc` + `internal/worker` — the four new private operations.
- [x] `internal/gateway` — `GET /channels/find`, `GET /channels/{id}`,
      `POST /channels/{id}/join`,
      `GET /channels/{id}/avatar`, permissions on `/channels`; join persistence
      and the partial-failure answer.
- [x] Docs: `docs/api/` (formerly `docs/API_V1.md`), `docs/CHANNELS.md`, `docs/CONFIGURATION.md`,
      `docs/telesync.example.json`, README.
- [!] `/channels/find?q=<text>` public-directory results are exercised only
      through the stall path: `tgtest`'s per-connection session tracking makes a
      scripted `messages.searchGlobal` answer unobservable on the client's
      primary connection (see `.agents/memory/GOTD_NOTES.md`). The merge/dedupe
      step is covered directly.
- [!] Live run (real sockets, real credentials): blocked in this sandbox.

### Local categories CRUD (2026-10-25)
- [x] `internal/channelstore` migration 2 — `categories` (unique name ignoring
      case) and `channel_categories` (`ON DELETE CASCADE`, no channel FK).
- [x] `internal/channelstore/categories.go` — create/get/list/update/delete,
      `AssignChannel`, `ListAssignments`, `AssignmentCounts`.
- [x] `internal/gateway/categories.go` — `/api/v1/local/categories` CRUD with
      the shared envelope, bearer auth, pagination and validation.
- [x] Wired through `runtime.DaemonOptions.Categories` and `cmd/telesync`.
- [x] Docs: [docs/api/local/categories.md](docs/api/local/categories.md), [docs/CHANNELS.md](docs/CHANNELS.md).
- [ ] Assignment endpoints (`channel` ↔ `category`): table and repository exist;
      the HTTP surface is deferred by the task scope.

### Removed
- [x] `internal/api`, `internal/panel`, `internal/store`, `internal/server`,
      `data/panel.db*` — the legacy admin panel and its SQLite store.
- [x] `internal/check`, and the `check`/`login`/`config check`/`serve`/
      `bootstrap` commands — the legacy single-account checker.
- [x] Panel-only gotd operations (`Join`, `MarkRead`, `Avatar`, `Channel`) and
      the interactive login state machine inside `Manager` (the `auth` command
      keeps its own flow).
- [x] `internal/dotenv` and the legacy environment schema; the runtime reads its
      JSON config and the process environment.

### Users, authentication and permissions (2026-10-26)
- [x] `internal/channelstore` migration 3: `users`, `user_permissions`,
      `user_channels_permission`, `user_categories_permission`; users
      repository, `ReplacePermissions` (validated, transactional),
      `AuthorizerFor`, `ListCategoriesByIDs`.
- [x] `internal/auth`: Argon2id hashes, unknown-username cost, HS256 tokens.
- [x] `internal/gateway`: login, `/auth/me`, `/local/users` CRUD and
      `/local/users/{id}/permissions`; authentication before routing;
      per-resource authorization on every Telegram and category route.
- [x] `api.jwt.{secret_env,token_ttl}`; operator token loopback-only;
      `telesync hash-password`.
- [x] Docs: `docs/API_V1.md`, `docs/CONFIGURATION.md`, `docs/RUNTIME.md`,
      `README.md`, `.env.example`, `docs/api/` (enforced by `internal/apidocs`).
- [x] `telesync admin`: creates or promotes a full administrator from
      interactive username/password (D39).
- [!] Live HTTP over a real listener: blocked in this sandbox.

### Next
- [ ] Run the daemon against real credentials from an unrestricted shell and
      record the result in `.agents/memory/STATE.md`.
- [ ] Decide whether the custom backend needs Obfuscated2 or another transport
      (configurable, untested end to end).
