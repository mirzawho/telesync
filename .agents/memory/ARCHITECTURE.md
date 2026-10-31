# Architecture

Technical facts only, verified against the source. Product intent, roles and
implementation status live in [PROJECT_BRIEF.md](../../PROJECT_BRIEF.md); this
file describes how the code is built, not what the product is for.

## Components

```
cmd/telesync            main(): signal context, wiring; admin, hash-password
  └─ internal/cli       daemon | worker | auth | config validate | admin
       └─ internal/runtime    Registry, Servers, RunDaemon, RunWorker
            ├─ internal/supervisor   one child process per client
            ├─ internal/gateway      public HTTP API, authz, /docs, /admin mount
            └─ internal/worker       one client's service + its socket
internal/rpc            private wire contract (DTOs, codes, server, client)
internal/appconfig      JSON config, env-var secret references, starter file
internal/profile        appconfig.Client → isolated profile.Spec
internal/gotdclient     gotd/td adapter (Manager + auth flow); only gotd importer
internal/keys           PEM RSA public key loading
internal/routing        custom resolver enforcing endpoint isolation
internal/sessionstore   per-client 0600/0700 session files
internal/peerstore      per-worker channel access-hash memory
internal/login          interactive authorization state machine
internal/tgapi          library-independent data the worker exchanges
internal/auth           Argon2id hashing, HS256 tokens
internal/channelstore   SQLite: migrations 1–5, repositories, Authorizer
internal/avatarcache    persistent 24 h avatar cache
internal/secrets        AES-256-GCM seal for credentials that must be read back
internal/wordpress      WordPress REST client: URL/SSRF rules, settings probe
internal/panel          //go:embed of the built panel (web/ build output)
internal/docsite        //go:embed of docs/api
internal/docsgen        documentation generator; internal/apidocs its route table
```

Removed on 2026-10-24: `internal/check`, `internal/config`, `internal/dotenv`,
`internal/store`, `internal/api`, `internal/server`, and the single-account
checker. The browser panel was removed on 2026-10-28 and reintroduced on
2026-10-29 as part of this binary (`internal/panel` + `web/`), served under
`/admin`. The repository-root `panel/` is an unused scaffold; `web/` is the real
frontend.

Dependency direction: `runtime → {supervisor, gateway, worker, rpc, appconfig}`;
`worker → {rpc, tgapi, appconfig}`; `gateway → rpc` only. `gateway` never imports
`gotdclient`; `worker` never imports `gateway`. `gotdclient` is the only package
that imports `github.com/gotd/td/telegram`.

## Client separation

Configuration has two connection *types* (`clients[].type`); the business
*role* (source / destination) is not in the configuration yet.

| | official | custom |
| --- | --- | --- |
| API credentials | its own `app_id` + `app_hash`/`app_hash_env` | its own |
| `Options.DC` | zero → gotd default | `server.dc_id` |
| `Options.DCList` | zero → `dcs.Prod()` | one entry: configured endpoint |
| `Options.Resolver` | zero → `dcs.DefaultResolver()` | `routing.Resolver` |
| `Options.PublicKeys` | zero → gotd vendored keys | keys from the PEM file |
| Session | `<session_dir>/<key>.json` | same rule, different key |

`profile.Spec` holds the same data the tests assert on, so option construction is
testable without a client. Official `Spec.Endpoint` is empty; the gateway's
`/profile` never returns it, and worker logs say "Telegram production" for an
empty endpoint.

## Connection lifecycle (gotd/td v0.162.0)

1. `telegram.NewClient` applies defaults; official keeps them, custom overrides
   DC list/resolver/keys. A no-op `UpdateHandler` is always installed (D10).
2. `Client.Run` → `restoreConnection` (session loader) → `reconnectUntilClosed`.
3. `manager.Conn` dials via the resolver, does the transport handshake and the
   RSA key exchange.
4. `initConnection` wrapping `help.getConfig` runs; the returned `tg.Config`
   replaces `Client.cfg`, so `dcList()` now returns **server-reported**
   addresses. `internal/routing` ignores that list by design (D1).
5. Connection is ready, `Run`'s callback fires; `Manager.refreshAccount` probes
   `Self` and records authorization.
6. On shutdown gotd writes the session; `Manager.Wait` returns only after that
   write completes, and the worker process exits after it.

`Manager.supervise` rebuilds the whole client when it stays not-connected for
`runtime.reconnect` (D32); `needs_auth` is a state, not a rebuild reason (D15).

## Process topology

- Daemon: supervisor + public HTTP listener + the local SQLite database, the
  avatar cache, the token signer, the WordPress sealer, the docs and the panel.
  Owns no Telegram client.
- Worker: exactly one `gotdclient.Manager` (one session, one endpoint) plus an
  HTTP/JSON server on `run_dir/<key>.sock`. Started with `os/exec`, inheriting
  the environment so `*_env` secrets resolve identically.
- Auth: a one-shot process that builds the same `profile.Spec`, so it authorizes
  exactly the client the worker will later load.

Supervisor: restart on process exit, not-ready within `startup_timeout`, or 3
consecutive failed liveness probes (30 s apart); exponential backoff ±25 %
jitter, budget reset after `2 × restart_initial_delay` of health; SIGINT then
Kill; stale `.sock` removed only when it is a socket (D15, D16, D23).

## Local state

```
daemon process
  gateway ──► channelstore (SQLite, <data_dir>/channels.db)  join/leave, local API
          ──► avatarcache  (<cache_dir>)                      avatars only
          ──► rpc.Client ──► worker socket
worker process (per key)
  worker.Service ──► gotdclient.Manager ──► peerstore (peer_dir/<key>.json)
                                       ──► MTProto
```

- **Migrations 1–5:** `managed_channels`, `categories`, `channel_categories`,
  `users`, `user_permissions`, `user_channels_permission`,
  `user_categories_permission`, `wordpress_sites`, `user_wordpress_permission`,
  `wordpress_site_categories`. **There is no message table**; messages are read
  live from Telegram and never stored.
- Only explicit actions write: `join` (Upsert `managed_channels`), `leave`,
  category/user/site CRUD, and permission PUTs. Reads never write (D28, D29).
- `foreign_keys(1)` in the DSN, `SetMaxOpenConns(1)`, file 0600, directory 0700.
- Workers own the access-hash file because MTProto needs it per client; the
  daemon owns the keyed stores because it is the only component that knows every
  `{key}`.

## Gateway

`Server.ServeHTTP` strips `BasePath` (`/admin` in the daemon), then routes:
`/healthz` and `/docs...` (public), `/api/v1/auth/login` (public),
`/api/v1/...` (`serveAPI`), everything else under the mount → the panel.
`serveTelegram` authenticates **before** looking up the key; an unknown or
disabled key is `404 unknown_client`. Only `channels/{id}/join` is POST on the
Telegram side; the local side has POST/PUT/PATCH/DELETE per resource.

Authorization: `auth.authenticate` (operator token in constant time, or HS256 →
user re-read + `AuthorizerFor` + `PermissionsOf` per request) → handlers →
`access.go` `visible(authr, kind, action, localID)`. Lists filter before paging
(`applyChannelFilter`, `listLocalChannels`, `listWordPressSites`,
`listCategories`; `Store.ListChannelsForUser`/`ListChannelsExcept` do it in SQL).

The gateway never constructs Telegram calls; it holds `rpc.Client` handles per
enabled key, and optional capabilities (local channels, category links, scoped
listings, `Leave`) are found by type assertion at the boundary.

## Authentication and WordPress

Users: `users` table, Argon2id PHC hashes, HS256 JWT (`iss=telesync`,
`aud=telesync-api`), secret from `api.jwt.secret_env` or `api.jwt.secret`, TTL
≤ 24 h. Operator token (`api.auth_token_env`) only on loopback; it holds every
permission but is not an account. WordPress: `internal/secrets` seals the
application password (AES-256-GCM, versioned box) with a key from
`api.wordpress.*` (never generated); `internal/wordpress` probes
`wp/v2/settings` + `wp/v2/media/{id}` through an https-only, public-host,
resolve-once, no-redirect, no-proxy client. `Server.Credential` is the only
decryption path and no route calls it.

## Telegram adapter — current operations

`Manager` (gotdclient): `Dialogs` (`messages.getDialogs`), `History`
(`messages.getHistory`), `Message` (`channels.getMessages`), `DownloadMedia` and
`DownloadChannelAvatar` (downloader, 32 MiB cap), `FindChannels`
(`contacts.resolveUsername`, `messages.checkChatInvite`, `contacts.search` +
`messages.searchGlobal`, peer-cache resolution), `ResolveChannel`,
`JoinChannel` (`channels.joinChannel` / `messages.importChatInvite`),
`LeaveChannel` (`channels.leaveChannel`).

**No send, forward, copy, edit or delete call exists in the adapter.** Adding
publishing is a new adapter operation: `rpc.Backend` → `rpc.Client` →
`worker.Service` → `Manager` → `tg.Client`.

`tgapi.NormalizePermissions(ChannelAccess)` is the single permission
normalizer. The adapter builds `ChannelAccess` from the channel object
(`creator`, `left`, `broadcast`, admin/banned/default rights); the worker and
the gateway only copy the four booleans. These are Telegram-account
capabilities, not application permissions (D30).

## Frontend

`web/` — React Router 8 (framework/SPA mode), React 19, Vite 8, TypeScript,
Tailwind 4, `@workspace/ui` (Base UI, shadcn-style), Persian RTL, Vazirmatn.
`base: "/admin/"`; the API is at `${BASE_URL}api/v1`. `app/lib/api.ts`
(`apiRequest`, `ApiError`), `app/lib/session.ts` (token in sessionStorage,
`requireSession` → `/auth/me`), loaders/actions per route, `intent` form
fields. `web/scripts/build-panel.mjs` builds and mirrors the client into
`internal/panel/files`; `react-router`'s prerender plugin is filtered out and
the page is written by `spa-prerender.ts`. `panel/` at the repository root is a
stale scaffold that nothing builds.

## States

`rpc.WorkerState`: `starting`, `connected`, `disconnected`, `needs_auth`,
`error`, `stopping`. `transport` is reported separately; `authorized` only from
a successful probe. `supervisor.Phase` is the process view: `starting`,
`running`, `backoff`, `failed`, `stopped`. `GET /api/v1/telegram` adds
`unknown` (no worker registered) and `unavailable` (worker did not answer).

## Data flow

`GET /api/v1/telegram/{key}/channels` → gateway validates paging → looks up the
key in its registry (enabled clients only) → `rpc.Client.Channels` over the Unix
socket → worker `Service.Channels` → dialog snapshot (60 s TTL) → DTO slice →
gateway authorization filter → envelope + page meta.

`POST /api/v1/local/channels/{id}/leave` → record + caller checked first →
optional worker `LeaveChannel` on a context detached from the request → row
deleted → `{local_deleted, telegram_left}`; a delete failure after a confirmed
leave is `500 partial_failure` (D42).
