# Historical state archive

This file preserves dated implementation notes for audit, not current status.
Later entries supersede earlier ones; use `STATE.md`, `PROJECT_BRIEF.md`, and the
source for current facts.

**Milestone:** multi-instance MTProto runtime with publishing — supervisor +
per-client worker processes + JSON HTTP API v1 (read, manage, publish).
**Built and unit/integration-tested; not yet run against live Telegram from an
unrestricted shell.**

**Last updated:** 2026-10-31 (later): `ci.yml` now builds **and pushes** the
Docker image to GHCR — every branch push → `ghcr.io/mirzawho/telesync:development`;
a tag `v*` → `:`+tag and `:production`; pull requests build only. Needs
`permissions: packages: write` and `docker/login-action` with the run's own
`GITHUB_TOKEN`. This reverses the "build-only, no registry" note below, which is
what that same day's audit set up (owner's written request, see LOG).

Earlier that day: local channel avatar 502 was classified (`no_avatar`/
`peer_unresolved`); the worker log now keeps the adapter error — not
live-verified, see LOG. Release-readiness audit the same day: stale `panel/`
scaffold removed, CI rewritten to build-only (`ci.yml`, no registry, no
deploy), `version.mjs --stamp` fixed with tests, docs status claims realigned
with the implemented publishing work, `.dockerignore` nested-`node_modules` gap
closed, Makefile gained `verify` and frontend targets.

**Product direction (owner, 2026-10-30):** telesync is a Telegram content
management and distribution system — acquire (source connections) → manage →
read → select → prepare → publish (Telegram destination / WordPress). Every
step is now implemented: publish (`POST /api/v1/telegram/{key}/messages`,
`POST /api/v1/local/wordpress/{id}/posts`), content preparation
(`web/apps/web/app/lib/content.ts`), the WordPress post writer
(`internal/wordpress/publish.go`) and the `content.publish` permission all
landed 2026-10-31. Still absent: `clients[].role` (source/destination roles are
convention until an owner decision lands, §11 #2). Live Telegram and live
WordPress publishing remain **unverified** in this sandbox. The status of every
capability is the matrix in `PROJECT_BRIEF.md` §9.

The legacy single-account panel (SQLite store, HTTP API, browser frontend) and
the two-profile connectivity checker were **removed** on 2026-10-24. Only the
runtime below remains. The panel was reintroduced on 2026-10-29 as part of this
binary (`web/` → `internal/panel`, served at `/admin`). The old root `panel/`
scaffold (D57) was **deleted** on 2026-10-31 as part of the release-readiness
audit; a tarball of its contents is outside the tree if it is ever needed.

## What exists now

```
cmd/telesync              main; runtime wiring in runtime.go (exec launcher, socket prober)
internal/appconfig        JSON config: clients, env-var secrets, path/security validation
internal/rpc              private contract: DTOs, error codes, Unix-socket server + client
internal/supervisor       spawn/monitor/restart workers, backoff, liveness, graceful stop
internal/worker           one client's Service (health/profile/channels/messages) + Listen/Serve
internal/gateway          public /api/v1/telegram/{key}/... , bearer auth, request IDs, errors
internal/channelstore     SQLite: channels selected through join (versioned migrations)
internal/avatarcache      persistent 24h avatar cache (atomic, single-flight, bounded)
internal/peerstore        per-worker channel access-hash memory
internal/runtime          Registry, RunDaemon, RunWorker
internal/cli              daemon, worker --key, auth --key, config validate
internal/profile          appconfig.Client → isolated profile.Spec
internal/gotdclient       gotd adapter: Manager + interactive auth
internal/keys routing sessionstore login tgapi   supporting packages
```

Supporting additions: `profile.FromClient`, `sessionstore.NewAt`,
`gotdclient.NewLoginForSpec`, `gotdclient.Manager.Wait`,
`tgapi.Status.LastSuccess`, `tgapi.Message.ReplyToMessageID`.

Docs: `docs/CONFIGURATION.md`, `docs/api/` (the API reference; `docs/API_V1.md`
is now a pointer to it), `docs/RUNTIME.md`, `docs/telesync.example.json`,
`README.md`, `PROJECT_BRIEF.md`.

## Verified

| Check | Result |
| --- | --- |
| `gofmt -l .` | empty |
| `go vet ./...` | clean |
| `go test ./... -count=1` | all 21 Go packages pass (2026-10-31 audit; 2 worker socket tests skip: sandbox denies binding) |
| `go build ./...` | ok |
| `telesync config validate` (valid file) | exit 0, redacted output |
| first run with no config | creates `$HOME/.telesync/config.json` (dir 0700, file 0600), prints the path, then reports `app_id` / unset hash |
| second run | file left byte-for-byte unchanged |
| `--config` / `TELESYNC_CONFIG` naming a missing file | reported as missing; nothing is created |
| `telesync config validate --json` | `{"clients":["official","the-cat"],"valid":true}` |
| single message | `GET .../channels/{id}/messages/{mid}` returns one post + media URL |
| `channels/find` | username, link, invite, id and text forms classified and routed; array (empty array for no match); `meta.count`; permissions booleans |
| `POST channels/{id}/join` | persists only on confirmed membership; `already_managed` on repeat; pending stored as `join_pending`; GET is 405 |
| local database | `UNIQUE(client_key, channel_id)`; concurrent upserts converge to one row; survives reopen |
| avatar cache | second request served from disk without a worker call; isolated per client; 24h TTL; stale >6h is not served |
| listing independence | `/channels` and `find` make zero store writes; `/channels` makes zero store reads |
| media bytes | `GET .../channels/{id}/messages/{mid}/media` streams the attachment (channels.getMessages lookup) |
| missing secret variable | exit 1, names the variable, not a value |
| non-loopback bind without token | exit 1, explains the requirement |
| in-memory end-to-end | `internal/runtime/e2e_test.go`: gateway → worker RPC → real MTProto handshake against `tgtest`; health, profile, channels, messages, reply metadata, unknown-key 404, session persisted |
| daemon + supervisor restart/shutdown | `internal/supervisor` tests with injected launcher and no-sleep clock |
| wedged worker replaced | liveness probe tests: a process that stops answering is killed and replaced; a transient probe failure is not |
| `internal/gateway` | `-race` clean; auth is checked before key lookup |

## NOT verified (must be stated, not glossed)

- **Public-directory text search against a scripted Telegram.** `tgtest` cannot
  deliver a scripted `messages.searchGlobal` answer to the client's primary
  connection (details in GOTD_NOTES.md). Covered instead by a stall-path test
  plus a direct unit test of the merge step.
- **`find`/`join`/`avatar` against live Telegram.** Same sandbox limits as
  below; the `tgtest` cover is per-operation.

- **Live Telegram.** The sandbox forbids binding *any* socket (Unix and TCP,
  including loopback), so `daemon`, `worker` and the real `/api/v1` surface could
  not be exercised end-to-end with real credentials here. The daemon smoke run
  failed at `listen tcp 127.0.0.1:8080: bind: operation not permitted` — a
  sandbox restriction, not a code defect.
- **Custom backend through the runtime.** `profile.FromClient` reuses the
  verified custom path (explicit DC list, resolver, RSA keys, `single-dc`), but
  the runtime has not been run against `62.220.127.238:30001`.
- **Real process supervision.** `os/exec` launcher, SIGINT delivery to real
  children, and stale-socket cleanup against real processes are covered by fakes
  and by `RemoveStaleSocket`/`worker.Listen` unit tests (the latter skip when
  binding is denied), not by a real multi-process run.

## Exact next step (from an unrestricted shell)

```bash
go build -o telesync ./cmd/telesync
export TELESYNC_CONFIG="$PWD/telesync.json"   # from docs/telesync.example.json
export OFFICIAL_API_HASH=… OFFICIAL_PHONE=+…
./telesync config validate
./telesync auth --key official
./telesync daemon &
curl -s localhost:8080/api/v1/telegram/official/health | jq
curl -s 'localhost:8080/api/v1/telegram/official/channels?page=1&per_page=5' | jq
kill -INT %1
```

Expected: health reaches `state=connected, authorized=true`; channels returns the
account's channels with real titles; the daemon stops the worker within
`shutdown_timeout` and the session file survives.

## Known limitations

See `docs/api/pagination.md`: numeric pagination is an adapter over message ids, and deep
pages restart from newest when the worker has no cursor.

## Environment constraints in this sandbox

- `GOCACHE` must point inside `$TMPDIR` (`GOCACHE=$TMPDIR/gocache`); the default
  cache path is not writable here.
- No socket binding of any kind; `net.Pipe` is used for every integration test.
- Outbound network (module proxy, Telegram) denied from the agent sandbox.

## Local categories (2026-10-25)

`/api/v1/local/categories` CRUD over migration 2. Verified: `gofmt -l` empty,
`go vet ./...` clean, `go test ./... -count=1` all 17 packages pass, `-race`
clean on `channelstore`/`gateway`/`runtime`/`cmd`. Covered: pagination and `q`
filtering, empty list, 404/400 paths, create/update/delete, duplicate names
(409), malformed JSON and content type, bearer auth, fresh-database migrations
and an upgrade from a version-1 database, `PRAGMA foreign_keys` on the pool, and
the regression "assign a channel, delete the category, assignment gone, managed
channel intact". Assigning a channel to a category has a repository method but
no HTTP route yet (out of scope). Not exercised over a real TCP listener here —
the sandbox forbids binding.

## Automatic reconnection (2026-10-25)

`runtime.reconnect` (whole seconds, default 10) drives a fixed reconnect
interval, and a client still down after one interval is rebuilt from scratch.
Connect/disconnect transitions are logged with the client key and endpoint.
See `docs/RUNTIME.md`. Not verified live: the sandbox cannot reach Telegram.

## Users, authentication and permissions (2026-10-26)

Migration 3 adds `users`, `user_permissions`, `user_channels_permission`,
`user_categories_permission` (FKs to the local tables, `ON DELETE CASCADE`).
`internal/auth` does Argon2id hashing and HS256 tokens. The API has
`POST /api/v1/auth/login`, `GET /api/v1/auth/me` and the `/api/v1/local/users`
surface, and every pre-existing route now requires a token and a permission
(default deny; explicit per-resource allow/deny overrides the global view).
Decisions D34–D38; full account in LOG.md.

Verified: `gofmt -l .` empty, `go vet ./...` clean, `go build ./...` ok,
`go test ./... -count=1` all 17 packages pass, `-race` clean on
`gateway`/`channelstore`/`auth`/`apidocs` and on `runtime` when run alone.
Not verified: the API over a real TCP listener (the sandbox denies binding);
one combined `-race` run timed out the MTProto e2e under CPU contention and
passed on retry — treat that as load-sensitive, not as a known defect.

## Exact next step (unchanged from the runtime milestone)

Run the daemon from an unrestricted shell against real credentials, then log in
(`POST /api/v1/auth/login`), create the first/extra accounts and exercise a
limited account's channel and category rights against live Telegram.

## API documentation site and its test (2026-10-26)

`docs/api/` is the API reference: `README.md` (route index), `authentication.md`,
`errors.md`, `pagination.md`, `auth.md`, `telegram/`, `local/`, `schemas/`,
`examples/api.http` (executed) and `examples/README.md` (curl + examples index).
`docs/API_V1.md` is a pointer that keeps only the initial-administrator section
(`docs/RUNTIME.md` links to it); its content lives in `docs/api/local/users.md`.

`internal/apidocs` (no runtime role) holds `apidocs.Routes`, the route table,
and the `.http` example parser. `go test ./internal/apidocs` proves every listed
method is accepted, no unlisted method is, a misspelled resource is refused,
every route and method appears in the docs tables, every prose path exists,
every relative link and `#anchor` resolves, every example is valid, indexed and
runs against a probe gateway, and that deleting a route from the table is
detected (`TestRemovedRouteIsDetected`). Known blind spot: a GET on an unserved
path is 404, indistinguishable from a 404 for data the probe cannot see, so a
GET route missing from the table is only caught by the documentation and index
comparisons, not by the live walk.

Maintenance rules: `.agents/skills/api-documentation/SKILL.md`, referenced from
AGENTS.md rule 7.

Verified 2026-10-26: `gofmt -l .` empty, `go vet ./...` clean, `go build ./...`
ok, `go test ./... -count=1` 16 packages ok (3 have no tests). Live HTTP on a
TCP listener still unverified here (sandbox forbids binding).

## Administrator CLI (2026-10-26)

`telesync admin [--config <path>]` asks for username, full name and password
(twice, hidden), stores only a hash, and creates or promotes the account with
every global permission; re-running resets the password and adds missing
permissions (D39). The separate SQL insert in D38 is withdrawn. Run for real
against a scratch config: exit 0, database created, permissions printed, second
run reports the reset.

## JWT key in the file (2026-10-26, third change)

`api.jwt.secret` (literal, ≥ 32 bytes) now works as well as `api.jwt.secret_env`
(D40); both at once is refused. A missing signer is detected even through a
typed nil (`auth.Missing`), so login answers 503 instead of panicking.

## Local channel API (2026-10-26, fourth change)

`/api/v1/local/channels` over the existing `managed_channels` row (no new
table): `GET` (page/per_page/q/total, per-caller filtering), `GET /{id}`
(local id), `GET /{id}/messages` (the row's client + Telegram id via the worker,
nothing stored), `GET /{id}/avatar` (the avatar cache), `POST /{id}/leave`
(`telegram` false/absent = row only; true = Telegram `channels.leaveChannel`
first, row after, `500 partial_failure` if the delete then fails). D41–D42.
`rpc.Backend`/`rpc.Client` gained `Leave`; `worker.Service.Leave` and
`gotdclient.Manager.LeaveChannel` implement it.

Verified: `gofmt -l .` empty, `go vet ./...` clean, `go build ./...` ok,
`go test ./... -count=1` passes, `go run ./cmd/docsgen --check` up to date.
Covered: listing/search/paging, local id vs Telegram id, messages on the row's
client with no store write, avatar client mapping, leave in all three body
forms, Telegram failure keeps the row, delete failure after leave =
`partial_failure` and the retry, invalid JSON/types/fields, 401/403 per channel,
cascade of the row's permission assignments with the category assignment and
other rows intact.

Not verified: `gotdclient.Manager.LeaveChannel` against tgtest or Telegram (the
in-memory server never routed the leave reply; control experiment with
`joinChannel` failed the same way — see GOTD_NOTES). Live HTTP on a TCP listener
still unverified here (sandbox forbids binding).

## Documentation is embedded; `/docs` is public (2026-10-26, later)

`make build` now runs the generator first, and the generator mirrors `docs/api`
into `internal/docsite/files`, which `internal/docsite` embeds with
`//go:embed`. The daemon serves that tree at `GET /docs` with no `docs_dir`
beside it; `api.docs_dir` names a tree on disk only to override it, and
`telesync config validate` prints `api.docs_dir=(embedded)` or the path. The
starter configuration no longer carries `docs_dir`. `GET /docs` is public (like
`/healthz`): `ServeHTTP` routes it before login and before `requireAuth`, and
`TestGatewayDocsArePublic` holds that. The page reads the whole tree in a
sidebar of nested `<details>` sections (leaf labels without `.md`, a directory's
own README as its "Overview", the section on the path to the current page open);
a directory path without a trailing slash serves the directory. Breadcrumbs are
per directory. The tree is read once, at build time for the embedded copy: after
regenerating, rebuild and restart.

## Admin panel — removed (2026-10-28)

The browser panel and everything that served it were removed at the owner's
request and are no longer part of this project: `web/`, `internal/web`
(and its embedded `files/`), `cmd/webcopy`, `gateway.Config.Admin` with
`AdminSite`/`SiteRoute`, `runtime.DaemonOptions.Admin`, the panel rows in the
route table, and the `admin-panel` documentation page. The gateway answers
every `/admin` path with its usual 404. There are no panel build targets.
Do not reintroduce a web UI, a panel or browser code unless asked (the brief
keeps them out of scope).

## Admin panel — served by the binary (2026-10-29)

The panel is part of the telesync binary, not a second service. `make build`
builds it (node, `web/scripts/build-panel.mjs`), copies the output into
`internal/panel/files`, and `go build` embeds that tree. The daemon serves the
panel, its assets and the API under one mount:

- `GET /admin[/...]` the panel's one page (a single-page app: every route is the
  same document), `GET /admin/assets/...` its files.
- `GET|POST /admin/api/v1/...`, `/admin/healthz`, `/admin/docs/...` the API, the
  health check and the documentation, at the same fixed paths as before, now
  under the mount. `gateway.Config.BasePath` is "/admin"; the old root paths
  answer 404.
- A binary built without the panel (plain `go build`) answers `/admin` with 503
  and the command that builds it.

The panel's browser code (React Router 8, static build) holds the token, not
the server: the login route calls
`/admin/api/v1/auth/login`, the client keeps the token in sessionStorage, and
every page's `clientLoader` calls `requireSession` (in `app/lib/session.ts`)
which asks `/auth/me` and redirects to `/login` on a 401. There is no cookie and
no `TELESYNC_PANEL_SESSION_SECRET` any more.

React Router's prerender plugin is filtered out of the Vite config in
`vite.config.ts`: it ends every static build by starting a preview server and
waiting on it, and where one cannot start the build hangs. The page is written
instead by `web/apps/web/spa-prerender.ts` from the client build's manifest.
`make panel` alone rebuilds the panel.

The `leave` route no longer accepts a `token` in its body: a credential is only
ever the Authorization header.

## Channel management in the panel (2026-10-29)

`GET /api/v1/telegram` lists the **enabled** clients the API routes to: one row
per enabled client in configuration order, authenticated (`requireAuth`) but
needing no global permission. `gateway.Config.Servers []gateway.ServerInfo`
(carried from `runtime.Servers(cfg, workers)`, which never copies credentials,
endpoints or session paths) is the only data it reads. Each row is `key`,
`type`, `enabled`, `health_checkable`, `state`, `authorized` (null when not
known), `transport`, `last_success_at`, `last_error`. `state` is the worker's
live health report (in the real daemon every configured child is in
`DaemonOptions.Workers`), `unknown` when no worker answered (registered but
never built — only possible in a partially wired composition), and
`unavailable` when the health call failed (timeout
message is fixed; the error goes to the log). Enabled is never reported as
online.

No new search/join endpoint was needed: the panel's search uses the existing
`GET .../channels/find?q=` and Join uses the existing
`POST .../channels/{id}/join` with the hit's decimal id; the local table is
`GET /api/v1/local/channels`. `managed_channels.membership_status` stores
`member` (not `joined`); the panel's membership badge accepts both.

The panel page `web/apps/web/app/routes/_panel/channels.tsx` now has: a server
`<select>` (a GET form, navigation to `?server=`) showing key, type and state;
a search box (numeric id, @username, t.me link, invite) with an in-flight
`AbortController` (apiRequest gained `signal`) and a sequence guard, results
cleared on server change via the `resultServer` effect + `key={server}`; a Join
button that posts `ref` back to the loader and shows the server's `result`
sentence; and the local table with pagination (`page`/`total_pages`). The
search posts with the loader's `token` (a browser client-side submit does not
revalidate the loader).

Verified: `gofmt -l .` empty, `go vet ./...` clean, `go build ./...` ok,
`go test ./... -count=1` all packages pass, `go run ./cmd/docsgen --check` up
to date, `go test ./internal/apidocs` green, panel `tsc --noEmit` clean, panel
rebuilt and embedded by `web/scripts/build-panel.mjs`, `make build` not run.

Not verified: the page in a browser (the sandbox denies binding a local port,
so `react-router dev` failed with EPERM); no frontend test runner exists in the
repo. Tests: `internal/gateway/servers_test.go` (eligible servers, no
configuration leaked, 401 anonymous, empty array, 405, unreachable worker),
`internal/runtime` `TestServersListsEnabledClientsOnlyAndNeverTheirConfiguration`.

## Administrators page: saving permissions (2026-10-29)

The permissions dialog (`web/apps/web/app/routes/_panel/administrators.tsx`)
rendered every global-permission checkbox from React state (`checked`/
`onChange`), so on submit the `<form>` held no `global_permissions` entry: the
action read `form.getAll("global_permissions")` as empty and the PUT stored an
empty set — every box looked ticked, saving cleared them. The API was never at
fault (`internal/gateway/users.go` parses and stores correctly).

Now `onSubmit` posts `global_permissions` as one JSON list from state, and the
action reads it through `checkedGlobals` (also accepts one entry per box; a
disabled checkbox posts no value either way, so a disabled box is revoked).
A dev-only submit listener warns when the permissions form posts no
`global_permissions`. No frontend test runner exists (vitest is not installed
and cannot be added offline), so the regression is proven by running the built
route's `clientAction` from Node with a stubbed `fetch`: the dialog's submit
sent `global_permissions: []` before, and `["users.view","channels.view"]`
after. Not verified in a browser: the sandbox cannot chmod the live database
and the daemon cannot bind a port here.

## WordPress site registration (2026-10-29)

`/api/v1/local/wordpress` (`GET`, `POST`) and `/api/v1/local/wordpress/{id}`
(`PATCH`, `DELETE`) register, validate, update, list and delete WordPress
publishing destinations. No publishing exists: nothing creates posts, sends
Telegram content anywhere or runs a background job.

- `internal/secrets` — AES-256-GCM seal/open, versioned box, key from
  `api.wordpress.encryption_key_env` (or a literal `encryption_key`). No key →
  `config validate` fails and the routes answer 503; the key is never generated.
- `internal/wordpress` — URL normalization (https, public hostname, optional
  path, no port/userinfo/query/fragment/IP/internal name), the dial guard
  (resolve once, every answer public, connect to exactly those, no proxy, no
  redirects, 10 s, 1 MiB, JSON only), and `Probe` (settings + icon attachment
  resolution through `wp/v2/media/{id}`).
- `channelstore` migration 4: `wordpress_sites` (unique URL, NOCASE) and
  `user_wordpress_permission`. `KindWordPress`, `wordpress.view`,
  `wordpress.create` (the global manage right), `PermissionSet.Wordsites`.
- `gateway`: the four routes; `Server.Credential` is the only decryption and is
  for the future publisher, never a route; error codes `wordpress_unauthorized`,
  `wordpress_forbidden`, `wordpress_unreachable`, `wordpress_incompatible`,
  `wordpress_timeout`, `wordpress_blocked_address`, `duplicate_site`.
- Decisions D45–D48. Docs: generated pages, `manual/components/wordpress.md`,
  examples, CONFIGURATION.md, `.env.example`, `docs/telesync.example.json`.

Verified: `gofmt -l .` empty, `go vet ./...` clean, `go build ./...` ok,
`go test ./... -count=1` all 22 tested packages pass, `-race` clean on
`secrets`/`wordpress`/`gateway`, `go run ./cmd/docsgen --check` up to date,
`go test ./internal/apidocs` green, `telesync config validate` with and without
the key (exit 0 / exit 1 naming `TS_WP_KEY`).

Not verified: any real WordPress site, a real TLS connection, or the routes
over a TCP listener (the sandbox denies binding and network). WordPress
behaviour is scripted from the documented REST API; a site whose REST API is
disabled or uses a `.local`/IP host cannot be registered (by design).

## Administrators page: permission pickers and manage⇒view (2026-10-29)

The permissions dialog now shows only the Persian labels (no key text), names
`channels.create`/`categories.create` "مدیریت کانال‌ها"/"مدیریت دسته‌بندی‌ها",
adds a managing permission together with the view permission it needs (and
drops it when that view is unchecked), and renders a multi-select combobox
(`packages/ui/src/components/combobox.tsx`, Base UI 1.9 `Combobox`) of channels
and categories whenever `channels.view`/`categories.view` is selected. An empty
picker stores no per-item rows — the global view alone means every item; picks
store `[{id, permissions:{view:"allow"}}]` — the picker is authoritative, so
clearing it removes earlier per-item rows. The catalog is loaded with the
dialog's `permissions-load` fetch (`listAll` over `/local/channels` and
`/local/categories`; a failed catalog shows the picker empty, not a dead
dialog).

Server: `permissionSetOf` (`internal/gateway/users.go`) refuses `users.manage`
without `users.view`, `channels.create` without `channels.view`,
`categories.create` without `categories.view` (400 naming the missing one), so
any client is held to the rule; `/auth/me`'s `access` derivation is unchanged.
Manual doc sentence added to `docs/api/manual/components/users.md`; docs
regenerated, `--check` green, `internal/apidocs` green.

Verified: the built route's `clientAction` run from Node with a stubbed
`fetch` produced the expected PUT bodies (view+no picks → `[]`; two picks →
two allow rows; manage alone → view added; no view → picks dropped), and the
`permissions-load` intent returned the catalog. Not verified in a browser (the
sandbox cannot open the live database or bind a port).

## Panel: server status card on the dashboard (2026-10-30)

The dashboard's پیشخان page has a **کارت وضعیت سرورهای تلگرام** card with a
refresh button. It reads `GET /api/v1/telegram` — there is no separate
`/api/v1/servers` route (`/api/v1/servers` was created and then removed at the
owner's request). That listing needs the global `servers.view`
(`PermissionServersView`, `KindServer`, D51, grantable in the مدیرها dialog,
reported as `access.servers` on `/auth/me`) and each row now carries `user`,
the server's own Telegram account (`null` when not known). The card shows one of
**در حال اتصال / متصل / خطا در اتصال**, and **نیازمند ورود** only when it is
connected and has no account; the rule is `connectionOf` in
`web/apps/web/app/lib/server-state.ts`, also used by the channels page's server
labels. The channels page no longer depends on the listing: without the
permission its "کانال جدید" dialog takes the server key as text. Not verified in
a browser (the owner tests the panel by hand).

Fix found while checking (2026-10-30): `messageView` embedded `rpc.Message`,
which promoted `rpc.Message.MarshalJSON` onto the wrapper and dropped its own
`media` field, so every message's `media.url` came out empty even though the
value was set (the media and e2e tests failed; the value was visible when
marshalled inside the handler). `messageView` now holds a `messageFields` struct
with the same json tags instead of embedding. Also restored
`rpc.Message.MarshalJSON` (reactions always `[]`) and the e2e test's
`views`/`reactions` fields on its decoded message, which the same concurrent
edit had removed. Full verification green.

## Resource-level authorization audit (2026-10-30)

The reported leak (granted Channel A, the account also saw Channel B) was not
reproducible on the local channel list with a per-channel allow alone; the
list already filtered. Around it, the same rule had real holes, now fixed
under one predicate `visible()` (`internal/gateway/access.go`): a global view
plus a deny listed the denied record; a category's channel list and a
channel's category list were unfiltered; WordPress PATCH/DELETE/edit ran
before the record was read, so `wordpress.create` edited any site; and
`update:allow` without the view revealed a record (now a write requires the
view, and a `view:deny` takes writes away). See D52 and LOG.md.

Regression suite: `internal/gateway/authz_isolation_test.go`. Verified
2026-10-30: `gofmt -l .` empty, `go vet ./...` clean, `go build ./...` ok,
`go test ./... -count=1` all packages pass, `-race` clean on
`gateway`/`channelstore`, `docsgen --check` up to date, `apidocs` green.
Not verified: a real TCP listener and the panel in a browser (the sandbox
denies binding).

## One channel object; the local channel detail reads the snapshot (2026-10-30)

`GET /api/v1/local/channels/{id}` now answers the canonical `channelView` — the
same keys, types and nested `permissions` as
`GET /api/v1/telegram/{key}/channels/{channel_id}` — built from the
`managed_channels` row without contacting a worker. `channel.id` is Telegram's
id as a string; the local row id stays in the path, in the listing, and in
leave/categories/messages. Migration **6** (`managed_channel_notices`) adds
`verified`, `scam`, `fake`, `notices_known`, `permission_new|edit|delete|read`,
`permissions_known` and `avatar_available` (backfilled from
`avatar_photo_id`), and replaces `managed_channels_client_idx` with a covering
`(client_key, channel_id)` index. The join route writes notices and permissions;
`Upsert` keeps stored notices/permissions when a write did not report them.
`rpc.ChannelInfo` gained `has_photo`. The admin panel's `ChannelDetail` type
mirrors the object; the messages page uses the URL's local id for the feed and
the object's Telegram id only for display. D58. Docs: regenerated
`local/api-v1-local-channels-id.md`, `manual/components/channels.md` and
`telegram.md`; the docs generator's documented record now carries
permissions/notices.

Verified: `gofmt -l .` empty, `go vet`/`go build` clean for every package this
work touched, `go test` green for channelstore, gateway, apidocs, rpc, worker,
docsgen, docsite, supervisor, auth, appconfig, secrets, wordpress,
`-race` clean on channelstore and gateway, `docsgen --check` up to date,
`go test ./internal/apidocs` green, panel `npm run typecheck` and `npm run
build` clean, `make panel` re-embedded the panel. `go build ./...` and
`go test ./...` fail only in `internal/runtime`, `cmd/telesync`,
`internal/gotdclient` — **another process is concurrently editing the tree**
(`gotdclient/customemoji.go`, `worker.go`, and `WorkerManager` in runtime
missing `DownloadCustomEmoji`), not this work. Not verified: the panel in a
browser (the sandbox denies binding a port), and the new local detail over a
real TCP listener.

## Views, reactions and custom-emoji images (2026-10-30, later)

The panel's message card ([message-card.tsx](../../web/apps/web/app/components/message-card.tsx))
shows a footer per message: Telegram's `views` («نامشخص» when null) and one
pill per reaction. A custom emoji pill draws the document's image through a new
route, `GET /api/v1/telegram/{key}/custom_emoji/{document_id}` (worker
`rpc.Backend.CustomEmoji` → `gotdclient.Manager.DownloadCustomEmoji`), and falls
back to a sticker icon for `.tgs`-only documents, which a browser cannot render.
The client key reaches the card from the local record (`client_key` in
`/local/channels/{id}`), because the public channel object does not carry it.

Limitation found in gotd v0.162.0: `messages.getStickerSet` there has only
`Stickerset` and `Hash`, no `emoji`/`keyword` fields, so the reaction set is
fetched whole and the keyword index cannot be queried; a document Telegram no
longer serves is `404`, and the set is cached in the worker for an hour. The
panel was rebuilt and embedded (`internal/panel/files`). Verified here: gofmt,
vet, build, all 21 packages, `docsgen --check`, `apidocs`, `tsc --noEmit`.
**Not verified:** the page in a browser (the sandbox cannot start the daemon —
port binding is denied), and a real custom-emoji download from Telegram.

## Message views and reactions (2026-10-30)

`rpc.Message` and `tgapi.Message` carry `Views *int` and `Reactions []Reaction`
(`type` = `emoji` | `custom_emoji` | `other`, `emoji`, `document_id`, `count`,
`chosen *bool`). Source: `message.views` (conditional field — absent is null,
present 0 is a real 0) and `message.reactions.results[]`
(`reactionEmoji.emoticon`, `reactionCustomEmoji.document_id`, `reactionCount.count`,
`chosen_order`). The public shape adds `"views"` (null when unreported) and
`"reactions"` (always `[]`, never omitted) to every message on the history, the
single-message and the local channel messages routes; `messageView` declares
both explicitly, and `rpc.Client.messagePage` maps a decoded `null` to `[]`.

Known gaps (2026-10-30, bottom of a long session): `messageView` still embeds
`rpc.Message`; if anything ever adds `MarshalJSON` to `rpc.Message` its fields
disappear from the public message. Do not add one. While this work ran,
**another process edited `internal/gateway`** (its `messageFields` variant,
`servers.go` profile lookup); two runs seen with identical edits passed and
failed differently, and this session's `rpc.go`/`worker.go`/`dialogs.go` edits
were reverted twice. Last full run here was green (gofmt, vet, build, all 21
packages, `docsgen --check`, `apidocs`). Not verified: live Telegram, the
panel in a browser, the custom backend's support for these fields.

## Reported scenario + query scoping (2026-10-30, later)

The full panel path (permissions-load → PUT permissions → login → channel
list) was traced and passes end to end (`TestTheReportedScenarioThroughThePermissionAPI`).
The local channel listing is now decided in SQL (`Store.ListChannelsForUser`:
the caller's own allows, or the global view minus the caller's denies), and a
`PUT /local/users/{id}/permissions` that omits `wordpress_permissions` no
longer revokes the stored site grants. **Another process is editing this tree
concurrently** (gateway.go, servers.go, servers_test.go, apidocs table, docs):
`go test ./internal/apidocs` and `docsgen --check` fail on `/api/v1/servers`
until that process finishes or the generator is run. `gofmt -l` names
`cmd/telesync/main.go` (not files this session wrote).

**Update (same session, owner asked to keep working):** `gofmt -l` is clean
again (my test file only). `internal/apidocs` and `docsgen --check` are green
after regenerating for the live route table (which serves only
`GET /api/v1/telegram` behind `servers.view`); the two manual passages in
`components/users.md` and `components/telegram.md` now say so. Two gateway
tests fail that this session did not write and that exercise the other
process's media work: `TestMediaDescriptorCarriesAFetchableURL` and
`TestSingleMessageMediaCarriesAURL` get `media.url = ""` for `type: "photo"`
from an unmodified `mediaHasBytes`. `servers_permission_test.go` still expects
`GET /api/v1/servers` (403/200) and fails: no such route is served.
`cmd/telesync/main.go` has since been gofmt'd by the other process.

## Publishing: Telegram and WordPress (2026-10-31)

The publish half of the product is implemented: a message read from a source
channel is prepared as an outgoing post and sent to a destination Telegram
channel or a registered WordPress site. The source message is **never modified**;
every publish is a new message/post built from the caller's edits.

### New routes

- `POST /api/v1/telegram/{client_key}/messages` — send a prepared post through
  that connection's worker. Body: `source{local_channel_id,message_id}`,
  `destination{channel_id}`, `content{text,entities}`, `media{mode}`,
  `options{silent,disable_web_page_preview}`. Returns the sent message and the
  destination it landed on.
- `POST /api/v1/local/wordpress/{id}/posts` — publish to the registered site
  `{id}`. Body: `source`, `post{title,content,status,tags}`, `media{mode}`,
  `options{comment_status}`. Returns the created post (id, link, status, tags).
- `GET /api/v1/telegram/{key}/channels?capability=send` — narrows the listing to
  channels Telegram says this account may post to. The publish route revalidates
  the destination with Telegram regardless.

### Layers

- `tgapi.Entity` + `UTF16Len`/`UTF16Slice` (UTF-16 code units, the unit
  Telegram uses for entity offsets); `tgapi.SendRequest`/`SendMedia`/`SendResult`
  and the send error sentinels.
- `gotdclient.Entities`/`InputEntities` convert spans to and from gotd;
  `Manager.SendMessage` sends via `messages.sendMessage` / `messages.sendMedia`
  (uploaded with `telegram/uploader`), extracts the sent message from the
  updates answer, and classifies refusals.
- `rpc.SendRequest`/`SendMedia`/`SendResult`; `POST /messages` on the worker
  socket takes **multipart** form data (a `request` JSON part and an optional
  `media` file part), bounded by `MaxSendMediaBytes` (32 MiB) — a large
  attachment never rides a JSON document.
- `worker.Service.SendMessage` maps the wire DTO and classifies errors;
  `gateway.publishTelegram` resolves source and destination, revalidates the
  send capability, and records the attempt.
- `internal/wordpress` gained `CreatePost`, `UploadMedia`, `FindTag`,
  `CreateTag`, all through the existing SSRF-guarded client.
- Migration **7** (`publish_operations`) is the idempotency record; see D59.
- `content.publish` is the global permission; `/auth/me` reports `access.content`.

### Verified

`gofmt -l .` empty, `go vet ./...` clean, `go build ./...` ok, `go test ./...`
all 21 packages pass, `go run ./cmd/docsgen --check` up to date,
`go test ./internal/apidocs` green, panel `npm run typecheck` clean, panel
rebuilt and embedded (`internal/panel/files`), `web/apps/web` content parser
tests 21/21 (`node scripts/test-content.mjs`).

Covered: entity UTF-16 offsets (ASCII, Persian, emoji, surrogate pairs),
entity conversion round-trip and unsendable-drop reporting, RPC multipart send
round trip with attachment, media cap, JSON body refused, flood-wait mapping,
worker error mapping, permission (content.publish), source/destination
visibility, destination send-capability rejection, idempotency (replay,
conflict, in-flight, unknown, retry-after-failure, per-account key), WordPress
post/tag/media client against scripted REST answers, tag reuse vs creation,
`media.mode` handling, and the parser's hashtag/mention extraction (URLs,
emails, Persian, entities preferred over the lexer, no duplicate tokens).

### NOT verified

- **Live Telegram, of any kind.** The sandbox forbids socket binding and
  network egress, so no daemon, no worker and no real MTProto send ran here.
- **The custom MTProto backend (`the-cat`) end to end.** Per the owner's
  instruction, live verification must target the custom backend only — official
  Telegram is filtered in Iran and would give misleading results. Official
  Telegram therefore stays **unverified and must not be treated as tested**.
- **A real WordPress site.** The client is exercised against scripted REST
  fixtures; no TLS connection to a live site was made.
- **The panel in a browser.** The sandbox denies binding a port, so `react-router
  dev` could not run; the dialog was verified by typecheck and by the parser's
  tests, not by clicking through it.

### Live verification runbook (operator, UNVERIFIED here)

1. `go build -o telesync ./cmd/telesync && make panel` (or `make build`).
2. Configure `the-cat` (custom) in `telesync.json`; set `TS_WP_KEY`.
3. `./telesync auth --key the-cat`, then `./telesync daemon &`.
4. `telesync admin` to create an account; grant it `content.publish`
   (مدیرها dialog → «ارسال و انتشار محتوا»).
5. Open `/admin/channels/:id/messages`, click **ارسال این پیام** on a message,
   choose تلگرام, pick `the-cat`, pick a channel the account can post to,
   edit, preview, send. Confirm the message appears on the destination.
6. Repeat with وردپرس against a registered site; confirm the post, its title,
   its tags and (if kept) its attachment.
7. Re-send the same request with the same `Idempotency-Key` and confirm no
   second post appears.
