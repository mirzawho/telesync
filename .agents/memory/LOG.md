# Log (newest last)

## 2026-10-30 — `make version v1.0.2`; Docker Hub production/development images

- Release command: `make version v1.0.2` -> `node web/scripts/version.mjs v1.0.2`
  writes `1.0.2` into `web/apps/web/package.json` and runs `git tag v1.0.2` at
  HEAD (an existing tag is left alone; `--no-tag` skips the tag). The Makefile
  passes the argument through `VERSION_ARG = $(filter-out
  version,$(MAKECMDGOALS))`, `version:` is in `.PHONY`, and a catch-all `%:`
  rule stops make refusing the argument as a target.
- `make build` = `panel` -> `docs` -> `go build`; it no longer stamps anything.
  The panel imports `web/apps/web/package.json`; the file wins, the nearest git
  tag (`git describe --long --match 'v[0-9]*'`, the `v` dropped) is only the
  fallback when the file has no version; the placeholder `0.0.1` means
  unreleased and shows nothing. `make test-panel` runs `node --test
  web/scripts/version.test.mjs` (11 tests) and `make check` includes it.
- Shown in two places: below the sign-in form (`نسخه v1.0.2`) and in the
  sidebar header under the logo, where "پنل مدیریت" used to be
  (`web/apps/web/app/routes/_panel/layout.tsx`). Vite emits one shared
  `app-version-*.js` chunk that both import.
- Panel module: `web/apps/web/app/components/app-version.tsx` exports
  `appVersion` from that package.json import (`resolveJsonModule` is on).
- `Dockerfile` takes `ARG VERSION` in the panel stage and writes it into
  package.json before the panel build; `compose.yaml` passes `VERSION:-`
  (default empty; `VERSION=1.0.2 docker compose build`).
- `.github/workflows/docker.yml` (new; the repo had no workflows): push to any
  branch -> `:development`; push of tag `v*` -> release checks
  (gofmt/vet/test/version.mjs tests/docsgen --check/no uncommitted diff) then
  `:production`, `:<v>`, `:<major.minor>` (not for prereleases) and `:latest`,
  multi-arch amd64+arm64, then the pushed binary is copied out and grepped for
  `version:\`<v>\``. PRs build without pushing. Needs `DOCKERHUB_USERNAME` +
  `DOCKERHUB_TOKEN` secrets and optional `DOCKERHUB_REPOSITORY` variable.
- Earlier in the same day's work, added and then removed on the user's
  correction: `GET /api/v1/version`, `telesync version`, `-X main.version`
  stamps in the Makefile, Dockerfile and CLI. Only the panel shows a version.
- Also fixed at the start: `wordpress.go` had an unused `rec`/undefined `rec`
  pair (restored in `getWordPressSite`); a stale `docs/api` was regenerated.
- The working tree has no `.git`, so the tag step cannot run here; verified in
  a scratch checkout with node_modules linked: `make version v1.0.3` created
  `v1.0.3` and `make panel` built `r=\`1.0.3\`` into the shared chunk.
- Verified: `node --test web/scripts/version.test.mjs` 11/11, `go build ./...`,
  `go test ./... -count=1` and `docsgen --check` green, panel typecheck clean.
  Not verified: the panel in a browser, the daemon over a real listener (the
  sandbox denies bind), and the workflow (no Actions run here).

## 2026-10-30 — Product knowledge realignment (documentation only)

- The previous audit read the current scope (`PROJECT_BRIEF.md`, `AGENTS.md`)
  as the product and concluded that publishing was out of scope. The owner
  corrected that: telesync is a content management and distribution system
  (acquire → manage → read → select → prepare → publish to a Telegram
  destination or a WordPress site); the runtime built so far is the acquire /
  manage / read half.
- `PROJECT_BRIEF.md` rewritten as the canonical product definition: identity,
  problem, users, domain glossary, source vs destination roles (type ≠ role;
  Cat = the destination backend role; keys are configuration values), WordPress
  as destination, current workflows and the intended publishing workflow,
  three kinds of permission (application user / Telegram account / publishing
  authorization, with the report's `content.publish` as requirement only), the
  §9 status matrix, the required `POST /api/v1/content/publish` as PROPOSED
  DESIGN, open owner decisions, standing constraints.
- `AGENTS.md`: "what this product is" corrected; status-is-not-scope rule;
  the 8-question pre-implementation checklist; the report labels
  (VERIFIED IMPLEMENTATION FACT / EXPLICIT PRODUCT REQUIREMENT / PROPOSED
  DESIGN / UNKNOWN); rules added for id namespaces, the one visibility rule,
  reading-never-writes, `web/` vs `panel/`, WordPress safeguards.
- `ARCHITECTURE.md`: current state only (panel re-added, messages not stored,
  adapter operations listed, no send call, frontend under `web/`).
- `DECISIONS.md` D55 (product direction; supersedes the original brief's
  "publishing / web UI / database out of scope"), D56 (roles ≠ types), D57
  (`web/` is the frontend, `panel/` stale).
- Checked against the source, not assumed: no `content.publish`, no
  `clients[].role`, no `ForwardMessages`/`MessagesSendMessage`/`SendMedia`, no
  `wp/v2/posts` call, no message table in migrations 1–5; the root `panel/`
  tree is a shadcn scaffold that nothing builds; `web/` is React Router 8 /
  React 19 / Vite / `@workspace/ui`. `docs/CHANNELS.md` corrected (the local
  channel API exists; the members route is read-only). `README.md` intro
  corrected. `gotd-mtproto-connectivity` skill no longer calls messaging "out
  of scope".
- No source code, schema, route or schema change. `go run ./cmd/docsgen
  --check` run after the edits; see the entry's verification line below.

## 2026-10-30 — One channel object: the local channel detail answers the snapshot

- `GET /api/v1/local/channels/{id}` builds the canonical `channelView` from the
  `managed_channels` row (`localChannelDetail`): `id` is Telegram's id as a
  string, `is_managed` true, membership from the stored status, permissions and
  notices from the stored report, avatar URL on the local route. No worker call.
- Migration 6 adds the stored fields the object needs (`verified`, `scam`,
  `fake`, `notices_known`, the four permissions + `permissions_known`,
  `avatar_available` backfilled from `avatar_photo_id`) and a covering
  `(client_key, channel_id)` index. `Upsert` preserves notices/permissions a
  write did not report. `rpc.ChannelInfo.HasPhoto` added.
- Tests: `internal/channelstore/channel_notices_test.go` (fresh schema, upgrade
  from 5 with a hand-written pre-migration row, preservation, permissions
  round-trip, category/permission rows intact), `internal/gateway/
  channel_contract_test.go` (both routes' key sets and value types compared
  from the real handlers; local ids vs Telegram ids; no worker call; a join
  reads back through both routes). The docs probe and generator use the
  documented record with permissions, so the generated example shows
  `read: true`.
- Verification is in STATE.md. The two pre-migration tests seed rows with raw
  SQL, because `Upsert` writes every current column and an older schema lacks
  them.

## 2026-10-30 — panel: views, reactions, custom-emoji images

- Panel message card gained a footer (views, reaction pills). Asked to render
  custom emoji as their image: needed a route, because the browser cannot send
  the bearer token on `<img src>`, so every image goes through the API with the
  panel's own credential.
- TDD: `gotdclient/customemoji_test.go` (scripted `messages.getStickerSet` +
  `upload.getFile`; red: `DownloadCustomEmoji` undefined), rpc round trip,
  worker mapping, gateway stream/404/400, then the implementations.
- gotd v0.162.0 gotcha: `MessagesGetStickerSetRequest` has no `emoji`/`keyword`
  fields (older layer), so the keyword-indexed half of the reaction set cannot
  be requested; the general list is used and an absent document is a 404.
- `docsgen` cannot document the route (it looks for `writeJSON` and a direct
  worker call in a `switch` case); the generated page has an empty handler line
  but every probe row and the manual paragraph are written by hand.
- Concurrent edits again (gateway_test.go, servers.go, the panel sources) were
  merged by reading them first; one of my own edits briefly dropped
  `client={…}` from `MessageCard` and `tsc` caught it.
- Verified: gofmt clean, vet clean, build ok, `go test ./...` 21/21,
  `docsgen --check` up to date, `tsc --noEmit` clean, panel embedded. Not
  verified: the page in a browser; a live custom-emoji download.


## 2026-10-31 — Publishing (Telegram + WordPress)

Implemented the publish half of the distribution workflow: select → prepare →
publish. Backend (tgapi entity model + UTF-16 helpers, gotdclient send +
entity conversion, rpc multipart send, worker send, gateway publish routes,
idempotency store, WordPress post/tag/media client), authorization
(`content.publish`), frontend (publish dialog, content parser, Tabs component,
admin permission catalog), and generated API docs.

Red-phase notes: `tgapi` entity helpers were stubbed with
`panic("not implemented")` first (real red on behaviour, not compile);
`channelstore` publish-operation methods likewise. The `content.publish`
constant was declared without being registered in `globalPermissions` first, so
`Valid()` returned false and the tests failed for the right reason.

Finds during the work: `permissions.new` already computes the send capability
correctly (D30), so no normalizer change was needed. Entity offsets are UTF-16
code units — a Go rune or byte index is wrong for Persian and emoji, and the
tests pin it. `messageView` embeds `rpc.Message`, so `entities` had to be
shadowed explicitly like `reactions`, or it would have vanished from the public
message.

Not verified: live Telegram of any kind (sandbox denies binding and egress);
the custom backend `the-cat` end to end; a real WordPress site; the panel in a
browser. Official Telegram is deliberately untested — the owner asked that live
verification target the custom backend only.

## 2026-10-31 — release-readiness audit (prepare for GitHub)

Owner asked for a full repository audit and cleanup ahead of a first GitHub
push, with one hard constraint: GitHub Actions must build and validate the
Docker image and must never deploy or publish it.

Findings and changes (the working tree is not a git repository yet; nothing was
committed, tagged or pushed):

- **Stale `panel/` scaffold removed** (D57 addendum): verified it was the
  untouched shadcn template — its one route imports an `app-version` component
  that does not exist there, so the scaffold did not even typecheck; nothing
  referenced it. A tarball was kept outside the repository (`$TMPDIR`) first.
  Also removed `internal/gateway/zz_backup.txt` (a 3-line supersession note) and
  a 21 MB stray `docsgen` binary left in the repo root after a `go build`.
- **CI rewritten** (`.github/workflows/docker.yml` deleted, `ci.yml` added):
  the old workflow signed in to Docker Hub and pushed `production`/`development`
  image tags. The new workflow has three jobs (go, frontend, docker), all
  `permissions: contents: read`, `push: false` on the image build, no registry
  login, no deploy steps. README's "CI publishes the images" section replaced
  with an honest "builds and validates; does not deploy or publish".
- **version.mjs `--stamp` bug fixed**: the Dockerfile calls
  `node web/scripts/version.mjs --stamp "$VERSION"`, but `--stamp` was only
  ignored as a flag and was not treated as "write only, never tag" (which could
  crash or side-effect a tag inside a Docker build with no .git). Red
  phase: 4 new tests asserting the write-only rule failed against the old
  `main()`; green after the fix (`releaseCommand()` is the extracted, tested
  decision). 18/18 green.
- **Docs status claims realigned with the implemented publishing work** of the
  same day: README (publish path, join/leave/publish state-changing calls, CI
  behaviour, testing commands), AGENTS.md (product direction paragraph, rule 10
  now that publish is a third Telegram write, rule 12 pointing at removed
  `panel/`), PROJECT_BRIEF.md §2 intro, §4 glossary (WordPress site), §6
  (sites are written to now), §7.2 and §8.3 retitled IMPLEMENTED with the
  remaining owner decision kept, §9 WordPress row, §10.1 rewritten to name the
  two real publish routes instead of the never-an-envelope `content/publish`
  route, §10.2 `content.publish` confirmed present (D60).
- **.gitignore** extended for `/docsgen`, node/turbo/react-router build
  intermediates at the root, `*.tsbuildinfo`, `*.prof`. **.dockerignore**
  rewritten to also drop host build output, test artifacts and local runtime
  state. macOS `.DS_Store` files deleted; nested empty `.cc-writes`/`.claude`
  tooling stubs removed (the root `.claude/launch.json` is kept; its stale
  `panel-static` entry pointing at a nonexistent `server-static.mjs` dropped).

Sandbox limits this run (say so, do not gloss): `go test`, `go vet`, gofmt,
`go build`, `docsgen --check`, panel typecheck/tests/build all ran with
`GOCACHE` pointed at a writable path and `GOPROXY=off`; the two `worker.Listen`
socket tests skip (no binding allowed); Docker engine is not running so the
image was not built locally — the new `ci.yml` is the build verifier.

Not verified anywhere in this task: live Telegram, a real WordPress site, the
panel in a browser, the Docker image build.

## 2026-10-31 — local channel avatar 502: coded upstream errors classified

- Reported: `GET /api/v1/local/channels/1/avatar` answered
  `502 worker_error` ("The Telegram client failed to complete the request.").
- Root cause of the *message*: `gateway.classify` had no case for a coded
  `rpc.Error` with `CodePeerUnresolved` or `CodeNoAvatar`, and no case for the
  `rpc.ErrPeerUnresolved` sentinel (only the RPC-side `rpc.classify` had them),
  so both fell through to the 502 fallback that names nothing.
- `TestLocalChannelAvatarWithoutAPhotoIs404` was red before the fix (502), green
  after: a coded `no_avatar` from the worker now answers `404 not_found`
  "This channel has no avatar." on every avatar route.
- `peer_unresolved` now answers `400 peer_unresolved` on the public API, matching
  `docs/api/manual/components/errors.md` (it only documented the RPC side).
- `TestLocalChannelAvatarUpstreamFailureIsReportedWithItsCause` pins that a 502
  body never carries the upstream text and that `op=local_channel_avatar` is
  logged with the real error at gateway level.
- Not verified: the live cause behind the reported 502. The daemon was a manual
  foreground process, the sandbox cannot bind a listener or reach the custom
  backend, and the request needs an admin login.

## 2026-10-31 — worker log keeps the adapter error behind "internal"

- The live 502 reproduced after the classify fix; the daemon log showed
  `err=internal: The worker failed to complete the request.` — so the worker,
  not the gateway, had already erased the cause: `rpc.classify` maps any
  unclassified error to `internal` with a fixed message, and neither the RPC
  handler nor the worker logged the original.
- `worker.Service` now has `WithLogf` (wired from `runtime.RunWorker`, which
  already had a logger); `mapDiscoveryError`/`mapSendError` became methods and
  log `worker: client=<key> mapped error <err> -> <code>` before answering.
  The wire answer is unchanged; only the log gained the line.
- Red runs: `TestDiscoverFailuresAreLoggedBeforeTheyBecomeInternal` and
  `TestDiscoverErrorsStillMapToTheirCodes` both failed with `logged ""` before
  the change.
- `TestServiceSendMessageReportsAnUnresolvableDestination` then panicked on a
  nil `s.logf`: `mapSendError`/`mapDiscoveryError` are also reached from a
  service built with no `WithLogf`. The log call goes through `s.log`, which
  guards nil. Full suite green.
- Next live request should name the real cause (e.g. `PEER_ID_INVALID`,
  `CHANNEL_INVALID`, `FILE_REFERENCE_EXPIRED`) in the daemon log.

### Follow-up in the same audit (subagent verification passes)

- Three evidence passes ran in parallel — API contract consistency, a security
  review, and config-schema/doc consistency — plus a frontend type fix.
- **Fixed:** `.dockerignore` had `node_modules/`, which Docker matches only at
  the context root; `web/node_modules` (315 MB) would have entered the build
  context. Added `web/node_modules`, `web/apps/web/node_modules`,
  `web/packages/ui/node_modules`, and `run/`/`*.sock`.
- **Fixed:** `docs/CONFIGURATION.md` said `docs_dir` defaults to `docs/api`
  beside the file; the code defaults to the embedded copy, with a neighbouring
  `docs/api` only as an upward-search fallback.
- **Fixed:** `docs/RUNTIME.md` and `.env.example` now say that
  `docs/telesync.example.json` is container-shaped (its `/data` and
  `/run/secrets` paths do not exist on a host), and that
  `TELESYNC_API_TOKEN` is only a name until `api.auth_token_env` names it.
- **Checked, no change needed:** a reported frontend contract mismatch —
  `publish-dialog.tsx` types the channel listing's `id` as `number`. That is
  correct: `GET /telegram/{key}/channels` emits `rpc.Channel.ID int64` as a JSON
  number (documented in `docs/api/manual/legacy/telegram/channels.md:60`: "`id` is a
  **number** here (a dialog's id fits), unlike the channel object where it is a
  decimal **string**"); only
  the single-channel object, `find` and `join` use the string form, and those
  TypeScript types already say `string`. An `int64 → JS number` precision loss
  would need dialog ids above 2^53, which does not occur in practice.
- **Makefile:** new `web-install`, `web-typecheck`, `web-test`, `docker`
  (build only) and `verify` (everything CI runs, in CI's order) targets;
  `make check` now also runs the frontend typecheck and content tests.
- **Not fixed, reported:** the login route has no in-app rate limiting (a
  reverse proxy could; nothing in the app does), and `publishError` echoes raw
  store errors to the client where `upstreamError` sanitizes (no credential
  material found in those paths). An `ID`-accuracy note on publish responses
  stays open until the response shape is typed.

- **Observed once, did not reproduce:** on one whole-suite run,
  `TestWordPressCreateRejectsACategoryThatDoesNotExist` failed; it passed alone
  and in three further `internal/gateway` runs and two further whole-suite runs.
  Not reproduced, not explained, not fixed — watch it in CI. No test was changed.
