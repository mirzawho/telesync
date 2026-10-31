# Agent instructions

Read this first, then [PROJECT_BRIEF.md](PROJECT_BRIEF.md) — it defines what the
product is and separates product intent from what is implemented. For current
state and the next step, read [.agents/memory/STATE.md](.agents/memory/STATE.md)
instead of re-deriving it from the code.

## What this product is

`telesync` is a **Telegram content management and distribution system**. It
runs several independent Telegram MTProto accounts as supervised worker
processes behind one HTTP API and one administrator panel, and it organises
what those accounts can see into a local inventory of channels, categories and
permissions.

```
browser / scripts → daemon (gateway + supervisor + SQLite) → worker RPC (Unix socket) → MTProto
```

The direction of the product is **acquire → manage → read → select → prepare →
publish**: a message read from a **source** connection is prepared as an
outgoing post and published to a **destination** — another Telegram channel
(on a destination connection) or a registered WordPress site. **Acquire,
manage, read, select, prepare and publish are all implemented** (publish routes
landed 2026-10-31; they are not live-verified against real Telegram or a real
WordPress site, and live verification targets the custom backend only).
`PROJECT_BRIEF.md` §9 has the exact status of every capability.

Two connection types, strictly isolated: `official` (Telegram production, gotd
defaults) and `custom` (an operator-controlled MTProto backend, explicit
endpoint/keys). Two connection **roles**, independent of the type: **source**
(channels are read) and **destination** (channels receive posts). The owner
calls the destination backend **Cat**; the sample configuration names that
client `the-cat`. Keys are configuration values — never hardcode them.

The panel (React Router, under `/admin`, embedded in the binary) is part of
the product; `web/` is its source. The SQLite database is part of the product.
Neither is "out of scope" any more (this corrects older text in this file).

## Status is not scope

Not being implemented is **not** a reason a capability is forbidden.
Publishing, content preparation and WordPress posting are **intended product**;
do not refuse work on them for being absent from the source, and do not add
scaffolding for them "for later" either. Build them when asked, as a real,
tested feature, in the existing architecture. Conversely, never describe an
intended capability as implemented — `PROJECT_BRIEF.md` and `STATE.md` carry
the status; keep them honest.

## Before you implement anything

Answer, in writing, before proposing a design:

1. Which business workflow (brief §7) does this belong to?
2. What user problem does it solve?
3. Which existing subsystem owns that responsibility (see "Where things are")?
4. Which existing API routes and data models are involved? Trace them in the
   code — gateway handler → service → store or RPC → worker → adapter.
5. Which permissions apply (application user permissions, not Telegram
   capabilities)?
6. Is it implemented, partially implemented, or new?
7. What must remain unchanged (the rules below)?
8. Which decisions in `.agents/memory/DECISIONS.md` constrain it?

Do not invent a service, abstraction, data model, route or frontend pattern
when an equivalent one exists. When a request is incomplete, make the best
effort from the brief and the code instead of asking questions the
documentation already answers. When a genuine product decision is missing,
state it precisely and stop there (brief §11).

Every report must label its claims: **VERIFIED IMPLEMENTATION FACT**,
**EXPLICIT PRODUCT REQUIREMENT**, **PROPOSED DESIGN**, **UNKNOWN / NEEDS
VERIFICATION**. Never present a proposal as if it existed.

## Non-negotiable rules

1. **TDD.** Write the test first, run it, see it fail for the right reason, then
   implement, then refactor. A test that fails only to compile is not a red test;
   make it exercise behaviour. See
   [.agents/skills/tdd-go/SKILL.md](.agents/skills/tdd-go/SKILL.md).
2. **Clients stay isolated.** Separate credentials, separate session files,
   separate datacenter configuration. Official leaves `telegram.Options.DC`,
   `DCList`, `Resolver` and `PublicKeys` zero so gotd uses its own production
   defaults. Custom sets all of them explicitly.
3. **No silent fallback.** A custom client must never reach official Telegram,
   its CDN, or its bundled RSA keys. The resolver ignores `help.getConfig`
   addresses on purpose — see
   [.agents/skills/custom-backend-routing/SKILL.md](.agents/skills/custom-backend-routing/SKILL.md).
   An unknown client key is 404; an unavailable worker is 503; neither is ever
   answered by a different client. A source/destination role never changes
   which endpoint a connection talks to.
4. **Secrets never leak.** No API hash, phone number in clear, login code, 2FA
   password, session body, access hash, bearer token, WordPress site password or
   other key material in output, logs, errors, API responses, or tests' golden
   strings. Use `appconfig.MaskSecret` / `appconfig.MaskPhone`.
5. **No invented values.** Never fabricate a backend host, credential, RSA key,
   channel username, link, permission, unread count, authorization state,
   publishing result, or last-success time. If a value is missing, make it
   configurable (or read it from the destination) and report the gap.
6. **Verify before claiming.** Unit tests passing is not "it works". Distinguish
   build, unit test, TCP/MTProto handshake, authorization, and live end-to-end
   verification — and say which one you reached.
7. **The API documentation is generated, and moves with the API.** Any task that
   adds, removes or changes a route, a request/response field, a status code, a
   permission, a limit or a pagination rule under `internal/gateway` /
   `internal/rpc` must, in the same task: add the route to
   `internal/apidocs/table.go`, run `go run ./cmd/docsgen`, write what the
   generator cannot extract in `docs/api/manual/components/`, and keep
   `go run ./cmd/docsgen --check` and `go test ./internal/apidocs` green. Do not
   hand-rewrite generated pages. See
   [.agents/skills/api-documentation/SKILL.md](.agents/skills/api-documentation/SKILL.md).
8. **Id namespaces never mix.** A Telegram channel id addresses a peer; a local
   channel id (`managed_channels.id`) is what a permission names. Resolve one to
   the other (`gateway.access.go`) — never store a Telegram id where an
   assignment is expected.
9. **One visibility rule.** Every route that returns or names a channel, a
   category or a WordPress site calls `gateway.visible()` (or a thin wrapper
   over it). Deny-by-default, explicit deny beats the global view, a write needs
   the view. A user granted channel A must never see an unrelated channel B;
   `internal/gateway/authz_isolation_test.go` holds that.
10. **Reading never writes.** Telegram content routes never mark messages read,
    never record a channel as a side effect, and never store messages. Telegram
    writes are `join`, `leave` and publishing; publishing is always an explicit
    user action (a publish route never fires as a side effect of a read).
11. **Two systems, no fake transaction.** Telegram/WordPress and SQLite are not
    one transaction; report partial success truthfully (`partial_failure`).
12. **Frontend lives in `web/`.** React Router 8, React 19, Vite, TypeScript,
    Tailwind, `@workspace/ui`, Base UI, Persian RTL. There is no Next.js and no
    second UI framework. The root `panel/` scaffold was removed on 2026-10-31
    (D57); if a template like it reappears, it is not the product — do not edit
    it and do not take components from it.
13. **Keep the WordPress safeguards** (https-only, public-host, resolve-once,
    no-redirect, no-proxy client; AES-256-GCM sealed credentials never returned
    by an API) in any future publishing work, and keep the site password on the
    backend only.

## How to verify

```bash
gofmt -l .                 # must print nothing
go vet ./...
go test ./...
go build ./...
go run ./cmd/docsgen --check
```

`go test ./...` needs no credentials. `go run ./cmd/docsgen --check` proves the
committed documentation matches a fresh generation; `go test ./internal/apidocs`
checks `docs/api/` against the gateway: routes, links, anchors and every example.
The panel typechecks with `npm run typecheck` inside `web/`. The in-memory
end-to-end test in `internal/runtime/e2e_test.go` drives the whole chain —
gateway HTTP → worker RPC → a real MTProto handshake against a `tgtest` server —
over `net.Pipe`; keep it working. Tests that bind a socket skip where the
sandbox denies binding; a skip is not a pass, say so when reporting.

## Where things are

- `cmd/telesync` — entry point, signals, wiring (`runtime.go`: exec launcher,
  socket prober, gotd), `admin`, `hash-password`.
- `internal/cli` — command parsing, exit codes (`daemon`, `worker`, `auth`,
  `config validate`).
- `internal/runtime` — `Registry`, `Servers`, `RunDaemon`, `RunWorker`.
- `internal/supervisor` — one child process per client, readiness and liveness
  probes, bounded restart backoff, graceful shutdown, stale-socket cleanup.
- `internal/worker` — one client's service (health, profile, channels,
  messages, find, join, leave, avatar) and its socket.
- `internal/gateway` — the public API (`/api/v1/telegram/{key}/...`,
  `/api/v1/local/...`, `/api/v1/auth/...`), authentication, the authorization
  rule (`access.go`), `/docs/...`, `/healthz`, the panel mount. Reference:
  [docs/api/](docs/api/README.md).
- `cmd/docsgen`, `internal/docsgen` — the documentation generator.
- `internal/rpc` — the private worker↔gateway contract. A new worker operation
  starts here (`Backend` + `Client` method), then `worker`, then `gateway`.
- `internal/appconfig` — JSON configuration and env-var secret references.
- `internal/profile` — builds an isolated `profile.Spec` from a configured
  client. `internal/keys`, `internal/routing`, `internal/sessionstore` keep a
  custom client isolated; `internal/peerstore` remembers access hashes.
- `internal/gotdclient` — the only package importing `gotd/td/telegram`
  (Manager + interactive auth). `internal/tgapi` — library-independent data.
- `internal/login` — the one-time authorization state machine.
- `internal/channelstore` — SQLite schema, migrations, repositories,
  `Authorizer`. A schema change is a new migration; never edit an applied one.
- `internal/auth` — Argon2id and HS256 tokens. `internal/avatarcache`,
  `internal/secrets`, `internal/wordpress` — support packages.
- `web/` — the panel (routes under `web/apps/web/app/routes/`, client at
  `app/lib/api.ts`, session at `app/lib/session.ts`, components in
  `web/packages/ui`). `internal/panel` embeds its build output; the build is
  `web/scripts/build-panel.mjs` (run by `make panel` / `make build`).
- `internal/apidocs` — the route table and example parser the documentation
  tests check `docs/api/` against. No runtime role.

## Before you stop

Update [.agents/memory/STATE.md](.agents/memory/STATE.md) (what changed, what is
verified, what is not) and append to [.agents/memory/LOG.md](.agents/memory/LOG.md).
Record architectural decisions in [.agents/memory/DECISIONS.md](.agents/memory/DECISIONS.md)
and verified dependency behaviour in [.agents/memory/GOTD_NOTES.md](.agents/memory/GOTD_NOTES.md).
If a capability's status changed, update the matrix in
[PROJECT_BRIEF.md](PROJECT_BRIEF.md) §9.
