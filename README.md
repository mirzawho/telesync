# telesync

A multi-instance runtime for Telegram MTProto clients.

It runs **one supervised worker process per configured Telegram client** and
serves a JSON HTTP API that routes to the right worker by client key:

```
HTTP client → daemon (public API) → worker RPC (Unix socket) → Telegram MTProto
```

- Official clients use Telegram production defaults (gotd's bundled datacenter
  list, resolver and RSA keys).
- Custom clients use an explicit host, port, datacenter ID, RSA public key,
  transport and routing policy, and never fall back to official Telegram.
- Adding a client is a configuration edit, not a code change.

telesync is a Telegram **content management and distribution** system. The
acquire → manage → read path is done: it reads channel content, records the
channels you join, files them into categories, and registers WordPress sites as
publishing destinations. The select → prepare → publish path is implemented:
a source message can be prepared as an outgoing post and sent to a Telegram
destination channel or registered as a WordPress post. **Publishing is
implemented but not live-verified against real Telegram or a real WordPress
site.** Reading never marks messages as read; publishing is always an explicit
user action. The status of every capability is the matrix in
[PROJECT_BRIEF.md](PROJECT_BRIEF.md#9-implementation-status).

v0.0.4 adds a **channel explorer**: browse a channel's history in the panel,
compose a new message or an attachment and send it, edit or delete an existing
message, and publish a message or a native album onward. Sends and edits are
**asynchronous** — the API answers `202` with an operation id and
`GET .../messages/send/{operation_id}` (or `.../messages/{id}/edit/{operation_id}`)
reports the outcome, so nothing is sent inside the request. A recording goes out
as a real Telegram voice message. See [docs/VERIFYING-MEDIA-PUBLISH.md](docs/VERIFYING-MEDIA-PUBLISH.md)
for the media kinds and their wire shapes.

v0.0.3 added **background automation**: new messages in a managed channel become
durable events that drive outbound **webhooks** and per-channel publishing
**workflows** (optionally polished by an OpenAI-compatible model first), with no
browser open and no HTTP request made. See [docs/AUTOMATION.md](docs/AUTOMATION.md).
`telesync serve-webhook` runs a local receiver for testing deliveries.

The binary also carries the admin panel at `/admin`: `make build` builds it and
folds it in, so one process serves the panel, its API and the documentation.

Start with:

- [docs/RUNTIME.md](docs/RUNTIME.md) — build, authenticate, run, inspect, stop.
- [docs/CONFIGURATION.md](docs/CONFIGURATION.md) — the JSON schema and rules.
- [docs/api/](docs/api/README.md) — the API reference, generated from the
  source and served as HTML at `GET /docs`; see
  [docs/api/DEVELOPING.md](docs/api/DEVELOPING.md) to regenerate it.
- [PROJECT_BRIEF.md](PROJECT_BRIEF.md) — the product (roles, workflows, domain
  glossary), the implementation status of every capability, and the open
  decisions.

## Quick start

```bash
make build          # the panel, then ./bin/telesync (needs node for the panel)
# or, API only, without a panel embedded:
go build -trimpath -o bin/telesync ./cmd/telesync

# no config yet? running the binary with no arguments (or any runtime command)
# writes a starter at ~/.telesync/config.json and tells you its path:
./bin/telesync

# fill in app_id in ~/.telesync/config.json, then export what it names:
export OFFICIAL_API_HASH=...
export OFFICIAL_PHONE=+15551234567

./bin/telesync config validate
./bin/telesync auth --key official       # interactive, once per client
./bin/telesync daemon
```

Each client reconnects on its own when the network drops. `"reconnect": 10`
inside `runtime` is the retry interval in seconds (the default); a client kept
off the network — a filtered link, a VPN that is not up yet — comes back as soon
as it is reachable, and every connect/disconnect is logged.

The configuration lives at `$HOME/.telesync/config.json` unless `--config` or
`TELESYNC_CONFIG` names another path. Only the default location is created
automatically; a path you name yourself must already exist.

Open http://127.0.0.1:8080/admin and sign in with an account `make admin`
created. The panel's API, the documentation and health are under `/admin` too:

```bash
curl -s localhost:8080/admin/healthz
curl -s localhost:8080/admin/api/v1/telegram/official/health | jq
curl -s 'localhost:8080/admin/api/v1/telegram/official/channels?page=1&per_page=20' | jq
curl -s 'localhost:8080/api/v1/telegram/official/channels/1671633563' | jq
curl -s 'localhost:8080/api/v1/telegram/official/channels/1234567890/messages/179' | jq
curl -s -o photo.jpg 'localhost:8080/api/v1/telegram/official/channels/1234567890/messages/179/media'

# discovery, selection and avatars
curl -s 'localhost:8080/api/v1/telegram/official/channels/find?q=@telegram' | jq
curl -s 'localhost:8080/api/v1/telegram/official/channels/find?q=https://t.me/example' | jq
curl -s -X POST 'localhost:8080/api/v1/telegram/official/channels/1671633563/join' | jq
curl -s -o avatar.jpg 'localhost:8080/api/v1/telegram/official/channels/1671633563/avatar'
```

`join` records the channel in the local SQLite database
(`data/db/channels.db`); `leave` and the publish routes are the other
state-changing calls. `/channels` keeps listing what the account can see on
Telegram, not the local selection. Avatars are cached on disk
for 24 hours. Full behaviour: [docs/api/](docs/api/README.md) and
[docs/CHANNELS.md](docs/CHANNELS.md).

## Requirements

- Go 1.27.3+ (matches `go.mod`), or Docker.
- Official Telegram API credentials from <https://my.telegram.org/apps>, passed
  as environment variables.
- For a custom client: the backend's host, port, datacenter ID, and its RSA
  **public** key in PEM form.

## Commands

| Command | Purpose |
| --- | --- |
| `telesync config validate [--json]` | validate the configuration file; no network |
| `telesync admin` | create an administrator (asks for username and password; every permission) |
| `telesync hash-password` | print an Argon2id hash of the password read from stdin |
| `telesync auth --key <client>` | one-time interactive authorization, saves the session |
| `telesync daemon` | supervise every enabled client and serve the API |
| `telesync worker --key <client>` | one worker process; started by the daemon |

Exit codes: `0` success, `1` failure (invalid config, failed auth, daemon error),
`2` usage error.

## Configuration

One JSON file, named by `TELESYNC_CONFIG` or `--config`. It holds no secret
values: `app_hash_env` and `phone_env` name environment variables. Relative
paths resolve against the configuration file's directory.

```json
{
  "api":    {"listen_addr": "127.0.0.1:8080", "request_timeout": "30s"},
  "runtime": {"run_dir": "./run", "session_dir": "./data/sessions"},
  "clients": [
    {"key": "official", "type": "official", "app_id": 123456,
     "app_hash_env": "OFFICIAL_API_HASH", "phone_env": "OFFICIAL_PHONE"}
  ]
}
```

Full schema and validation rules: [docs/CONFIGURATION.md](docs/CONFIGURATION.md).
A complete sample: [docs/telesync.example.json](docs/telesync.example.json).

### Custom client RSA public key

Put the backend's PEM-encoded **public** key at the path named by
`server.rsa_public_key_path`:

```bash
openssl rsa -pubout -in server_private.pem -out secrets/the-cat-rsa-public.pem
```

An empty, missing or malformed key is a hard error. telesync never substitutes
Telegram's bundled keys, and never falls back to official endpoints.

## Sessions

Each client owns one file (default `<session_dir>/<key>.json`), mode `0600`
inside a `0700` directory. They are authentication credentials:

- Back them up encrypted; a copied session grants account access.
- Never copy a session between keys or machines.
- To re-authorize, run `telesync auth --key <key>` again.
- They are git-ignored and excluded from Docker images and build context.

A worker whose session is missing or revoked stays up reporting `needs_auth`; it
is never restarted in a loop.

## Docker

```bash
mkdir -p secrets
# put the backend public key at secrets/the-cat-rsa-public.pem

# the container mounts ./telesync.json read-only and points TELESYNC_CONFIG at it
cp docs/telesync.example.json telesync.json
docker compose config
docker compose build
docker compose run --rm telesync config validate
docker compose run --rm telesync auth --key official     # interactive, has a TTY
docker compose up telesync
```

Sessions persist in the `sessdata` named volume across container recreation.

## Continuous integration

`.github/workflows/ci.yml` validates the project (gofmt, vet, tests, docs,
frontend typecheck and tests) and builds the Docker image, **pushing it to
GHCR**:

| trigger | image tags |
| --- | --- |
| push to a branch, `workflow_dispatch` | `ghcr.io/mirzawho/telesync:development` |
| git tag `v*` | `ghcr.io/mirzawho/telesync:<tag>` and `:production` |
| pull request | built only, not pushed |

It needs no credential beyond the run's own `GITHUB_TOKEN`
(`permissions: packages: write`) and deploys nothing. The image version in the
panel comes from the tag (`--stamp`), so a version only appears on a tagged
build. Publishing to GHCR was made a requested change on 2026-10-31.

Local equivalents:

```bash
make check     # gofmt, go vet, Go & panel-tooling tests, docs current, build
make build     # panel + docs + ./bin/telesync
```

## Versions

`make version v1.0.2` releases: it writes `1.0.2` into the panel's
`package.json` and creates the git tag `v1.0.2` at HEAD. The panel imports that
file and shows what it holds; `make build` builds whatever is there. With no
version in the file the nearest git tag is the fallback, so a checkout whose
tag is missing still shows the release. Tagging and pushing stay under your
control — CI builds and validates; it does not publish.

```bash
make version v1.0.2   # writes the version + tags HEAD (only in a git repo)
make build            # builds the panel with 1.0.2
```

## Testing

```bash
make check       # gofmt, go vet, go test, panel-tooling tests, docs current, build
# or the individual checks:
gofmt -l .
go vet ./...
go test ./...
go build ./...

# panel build tooling (version stamping)
node --test web/scripts/version.test.mjs

# frontend (npm, from web/)
cd web && npm run typecheck
cd web/apps/web && npm test
cd web && npm run build   # or make panel for build + embed into the binary
```

The Go suite needs no credentials and no network. `internal/runtime/e2e_test.go`
drives the whole chain — gateway HTTP → worker RPC → a real MTProto handshake
against an in-process `tgtest` server — over `net.Pipe`.

Tests that bind sockets skip where binding is denied (some sandboxes). A skip is
not a pass; report it as unverified.

## Troubleshooting

- **`client "x": app_hash_env names X_HASH, which is not set`** — export the
  variable before starting the daemon. The config names variables; it never holds
  values.
- **`api.listen_addr ... is not loopback; set api.jwt.secret_env`** — exposing the
  API is an explicit decision. Set the JWT signing key, or bind loopback.
- **`401 unauthorized` on every request** — log in at `POST /api/v1/auth/login`
  and send the token. Without a configured `api.jwt.secret_env` there is no
  login, and only the loopback operator token (if configured) works.
- **No account exists yet** — run `telesync admin`; it asks for a username and
  password and creates a full administrator. See
  [docs/API_V1.md](docs/API_V1.md#initial-administrator).
- **`needs_auth` in `/health`** — the connection works but the account is not
  signed in. Run `telesync auth --key <client>`.
- **`worker_unavailable` / `503`** — the worker socket does not answer. Check the
  daemon log for the restart reason.
- **`refusing to connect to DC n`** — the backend redirected the client to a
  datacenter other than `server.dc_id`. Fix the backend or its DC id; only use
  `"routing": "map-all"` if the backend genuinely serves every DC ID.
- **`CDN connections are not supported`** — a custom client will never fall back
  to Telegram's CDN hosts.
- **Auth times out** — TCP or the handshake never finished. Check reachability,
  then `server.transport`, then the RSA public key; retry with a larger
  `--timeout`.
