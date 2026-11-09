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

## 2026-10-31 (later) — CI pushes the image to GHCR (owner's written request)

Owner, in Persian: every push/commit is a **development** build →
`ghcr.io/mirzawho/telesync:development`; a git tag `v*` → `ghcr.io/mirzawho/telesync:<tag>`
**and** `ghcr.io/mirzawho/telesync:production`. This reverses the audit earlier
the same day that made `ci.yml` build-only (that audit was also an owner request;
this one supersedes it).

- `.github/workflows/ci.yml` — `permissions: packages: write` added; docker job
  now logs in (`docker/login-action@v3`, `${{ secrets.GITHUB_TOKEN }}`) unless
  the run is a pull request, and `push: false` became
  `push: ${{ github.event_name != 'pull_request' }}` (PRs have no write token
  anyway; nothing is published by a PR). Tags: `ghcr.io/mirzawho/telesync:development`
  always; on a tag, the same build is **re-pushed** under `:`+tag and
  `:production` (second `build-push-action` step, a cache hit on rebuild).
  Retag instead of racing the branch run's `development` push. Two deliberate
  choices: no `latest` tag on purpose (`production` is the owner's name for it);
  `VERSION` build-arg is the tag on a tagged build and empty otherwise, so a
  development image shows no version rather than invented one.
- **Not yet verified:** the workflow has not run — first push to a branch will
  prove it. Not tested locally (no Docker daemon here; `make verify` covers the
  same Dockerfile build on a runner). No `deploy` was added: ghcr publish only.
- Docs moved with the change: README's CI section now has the trigger → tag
  table instead of "does not push"; `Makefile` `docker`/`verify` comments no
  longer claim CI never pushes; STATE.md records the inversion of the earlier
  build-only note (with both claims kept, dated).

## 2026-11-01 — Agent startup context reduction

- Audited Claude Code 2.1.289 with `/context`: `AGENTS.md` was the project
  instruction source; auto-memory loaded only its compact `MEMORY.md`; the
  repository `.agents/skills` were on-demand, while global built-in/plugin
  skill metadata was always present.
- Reduced `AGENTS.md` to project invariants and an on-demand index. Moved the
  dated 675-line state history to `STATE-history-2026.md` and replaced `STATE.md`
  with a compact current snapshot. Added path-scoped `.claude/rules/` for API,
  MTProto, frontend and WordPress details.
- Shortened all four repository skills and moved detailed dial, gotd and test
  seam material into skill references. Removed the stale auto-memory release
  snapshot; preserved the owner workflow preference.
- Follow-up review restored omitted authz, routing, gotd-diagnostic and API
  route-maintenance details in on-demand references, corrected stale publishing
  and migration facts in `ARCHITECTURE.md`, and labeled the retained state file
  as historical.
- Verified after edits: Claude root `/context` reported 767 project tokens,
  75 auto-memory tokens and 2k built-in skill metadata tokens (8.6k total),
  versus 2.8k, 72 and 4.4k (13k total) before. `git diff --check` passed.

## 2026-11-02 — The manage right deletes channels, and only the picked ones

- The owner reported that deleting a channel answered "شما مجوز حذف این کانال را
  ندارید" although the account held channel management. Traced it to the model:
  `POST /local/channels/{id}/leave` needs a per-channel `delete` assignment,
  which the administrators dialog never writes (it stores `view: allow` only),
  while the identical label "مدیریت" on `wordpress.create` already covered every
  write. The documented per-resource `update`/`delete` was unreachable from the
  product.
- The owner set the rule: a manage permission may delete channel, category and
  WordPress items, checking that the account has that item; in the panel a
  manager has either every item or the picked ones, and a limited manager may
  edit and delete only the picked ones.
- Implemented in `channelstore.Authorizer`: an explicit assignment of the
  action still decides first, and otherwise the kind's manage permission allows
  `update`/`delete` inside the caller's selection (D62). `siteViewer` now
  delegates to `visible()`, so WordPress is the same rule; the WordPress route
  still requires the global manage right, so a per-site grant alone never
  replaces a credential.
- The route-level test caught a real hole before it shipped: assignment rows
  cascade with their resource (D34), so a selection stored only as `view: allow`
  on the picked items evaporated when the last one was deleted — a scoped
  manager could delete their own channel and then every other one. Closed by
  having the dialog store an `update: deny` with a `delete: deny` on each item
  left out, which outlives the picked records. Documented residue: a manager
  scoped to every item in the inventory can manage replacements once those are
  all deleted.
- Also corrected the users paragraph that said per-item `allow` rows restrict a
  holder of the global view (they do not), and the leave route's test comment
  that gave the operator's lack of an assignment as the reason for its refusal
  (it has no account).
- Verified here: `gofmt`, `go vet`, `go test ./...`, `go build ./...`,
  `go run ./cmd/docsgen` and `--check`, `go test ./internal/apidocs`,
  `npm run typecheck`, `npm run build`, `make panel`. Not verified against a
  running daemon or a browser; the sandbox blocks the listener and the network.

## 2026-11-02 — dashboard counts endpoint + پیشخان row landed in this checkout

Both halves of the dashboard's four count cards are in `main`'s working tree now
(the backend had been done on a side branch and the panel in `web/` was still
counting off four list calls):

- `internal/gateway/stats.go` (new): `GET /api/v1/local/stats/dashboard` —
  one figure per collection, `null` when the caller may not see it at all and
  otherwise exactly the rows its list route would show. Wired through
  `serveAPI`'s local case and `serveLocal`; a missing local store answers
  `503 worker_unavailable` before the credential is read (without the account
  store none can be resolved).
- `internal/gateway/categories.go`: `visibleCategories` is now the one place the
  category visibility rule lives — the list **and** the count use it. This also
  fixes `GET /api/v1/local/categories`, which did not subtract an explicit
  `view:deny` from a global view.
- `internal/gateway/wordpress.go`: the site list reports `meta.total`, the number
  of sites the caller may see, so the dashboard's figure is checkable against it.
- `web/apps/web/app/routes/_panel/dashboard.tsx`: the row is drawn from that one
  call (`/local/stats/dashboard`); a null figure draws no card and the row takes
  as many columns as cards, or no row at all when there are none. A failed call
  is one card, never four invented zeros.
- Docs moved with the route (table.go, docsgen's handler table, the `dashboard`
  component, the example + index row, generated pages). `go run ./cmd/docsgen
  --check` clean, `go test ./internal/apidocs` green.

VERIFIED here: `gofmt -l .` empty, `go vet ./...` clean, `go test ./...` all
packages pass (2 worker socket tests skip — the sandbox denies binding),
`go build ./...` ok, `make build` produced `bin/telesync`, and a throwaway test
(since deleted) confirmed the route answers 200 at
`/admin/api/v1/local/stats/dashboard` while `/admin/api/v1/stats/dashboard`
stays 404. NOT verified: against a running daemon — binding a port is denied in
this sandbox, so the panel was never opened in a browser.

## 2026-11-03 — Handoff workflows: prepare/implement split

- Infrastructure only, no production code touched. Added
  `.agents/workflows/prepare-change.md` (Graphify-first discover → plan → Red →
  write handoff → stop, never edits production code) and
  `.agents/workflows/implement-handoff.md` (explicit approval gate → Green →
  Refactor → validate → record), plus `.agents/handoffs/_TEMPLATE.md` with the
  A–J sections and the status lifecycle
  (`DISCOVERING … AWAITING_APPROVAL/…/DONE/BLOCKED`). Task handoffs live in
  `.agents/handoffs/<task-id>.md` only when a real task exists.
- `AGENTS.md`: added two sections (Development lifecycle; token-efficient
  navigation); the pre-existing in-progress edits to it were left as found.
  `CLAUDE.md` already imports via `@AGENTS.md`; unchanged.
- VERIFIED here: graphify `graph_stats` at HEAD (5364 nodes, matches 4a23673),
  template contains sections A–J, `git status` shows these additions as only
  new files plus my AGENTS.md insert (the tree carries ~171 pre-existing
  modified paths from earlier work), no Go files written. NOT verified: an
  actual end-to-end prepare→approve→implement run — no real task went through
  the lifecycle yet.

## 2026-11-03/04 — Telegram media: original filenames, streaming delivery, player and download

Implemented the media-handling task in one pass (the user asked for the code
change to be completed and verified directly, so the prepare→handoff→implement
gate was not used for this task; behaviour-first tests still came first where
the seam allowed).

**Backend.** `gotdclient.MediaFileName` reads `documentAttributeFilename` and
nothing else; `tgapi.Media`, `rpc.Media`, `gateway.mediaView` carry `filename`
(`omitempty`, so a message Telegram never named reports no name rather than a
guessed one). `MediaSize` now also reports a photo's byte size. `DownloadMedia`
was rewritten as a streaming reader over 512 KiB `upload.getFile` parts with
`Precise(true)` and `CDNSupported(false)`, honouring `MediaSlice{Offset, Limit,
Tail}` and re-reading the message once on `FILE_REFERENCE_EXPIRED`. The worker
RPC returns `rpc.MediaStream` as raw bytes with `X-Telesync-Size/-Offset/
-Filename` headers instead of base64 JSON (decision D63); `gateway.media`
parses `Range`, answers `206`/`416` with `Content-Range`, sets
`Content-Disposition: attachment` with an RFC 5987 name, `X-Content-Type-Options:
nosniff` and `Accept-Ranges` that is only `bytes` when the size is known. The
32 MiB `maxMediaBytes` cap is gone for reads; it survives as
`maxSmallFileBytes` for custom emoji and avatars.

**Frontend.** `lib/media-view.ts` holds every presentational decision (kind,
title, download name, extension, size) as pure functions; `MediaAttachmentCard`
renders a real `<audio controls>` for audio and voice and a document card
(filename, label, extension, size, download) for documents, leaving photos,
videos and everything else as they were. Downloads fetch with the existing
bearer-token approach (`downloadMedia`) and save under the sender's filename —
no token ever appears in a URL. `types.ts` gained `filename`.

**Tests.** Go: `internal/gotdclient/media_filename_test.go`,
`media_stream_test.go`, `internal/gateway/media_delivery_test.go`, and new
cases in `rpc_test.go` and `worker_test.go`; existing fakes moved to the
streaming shapes. Frontend: `app/lib/media-view.test.ts` and
`app/components/message-media.test.tsx`, run by a new
`scripts/test-media.mjs` (esbuild bundle + `node --test`, since the workspace
has no test runner and none can be added offline) and wired into `npm test`.

VERIFIED here: `gofmt`, `go vet ./...`, `go test ./...` (21 packages, green),
`go build ./...`, `go run ./cmd/docsgen` and `--check` ("documentation is up to
date"), `go test ./internal/apidocs ./internal/docsite ./internal/docsgen`,
`npm run typecheck`, `npm run build`, `npm test` (40 frontend tests green).
Telegram-side behaviour is verified against tgtest's fake MTProto server over
`net.Pipe`, and the HTTP contract against the real gateway handler with a
scripted worker.

NOT verified: live Telegram (real `upload.getFile` offsets, real file-reference
expiry, real sizes for very large documents), a running daemon, and the panel in
a browser — this sandbox denies binding a port, so audio playback and the
download click were never exercised in a real browser. Also unverified: that the
reported `PhotoSize.Size` always equals the byte count the thumbnail download
returns (the fixtures model it that way).

Unrelated note while working: the disk was full (149 MiB free) and builds were
failing. Stale Go build caches under `/tmp/claude-501` (`gocache`, `go-build`,
`gocache2`, `go-build-cache`, `telesync-gocache`, ~6.8 GB) were deleted to
unblock this; they are pure caches and were rebuilt as needed.

## 2026-11-04 — Channel explorer and message timeline redesign (frontend only)

Replaced the table-based channel page with a Telegram-style two-pane workspace
in `web/apps/web`. `routes/_panel/channels.tsx` is now the workspace and
`routes/_panel/channel-messages.tsx` a child route at `channels/:id/messages`
(`routes.ts` nests them), so the channel list stays mounted across selection and
`/channels/{id}/messages` deep links, back and forward keep working. The old
standalone page chrome and the channels table are gone.

New: `components/channel-explorer/` (`channel-list.tsx`, `channel-list`'s
`ChannelCategoryChips`, `channel-delete-dialog.tsx`,
`channel-messages-pane.tsx`, `message-item.tsx`, `message-media.tsx`,
`message-text.tsx`), `lib/message-entities.ts`, `lib/channel-filter.ts`,
`lib/channel-actions.ts`, and thin Base UI compositions in
`packages/ui/src/components/` (`message`, `bubble`, `attachment`,
`alert-dialog`, `checkbox`, `scroll-area`, `empty`).

Behaviour changes worth recording:
- `use-infinite-messages.ts` now returns a **chronological** list (oldest first)
  and **prepends** older pages instead of appending; each API page is reversed
  on arrival. `MessagesStatus` carries `scope: "initial" | "older"` so the top
  of the timeline can show a loading line without blanking the list.
- Message text renders from the API's `entities` (`lib/message-entities.ts`,
  UTF-16 spans, nesting-aware, plain text kept intact) instead of the old
  regex-only `Linkified`. Bare URLs are still auto-linked, but only where no
  entity covers them. `Entity` moved from `lib/content.ts` to `lib/types.ts`
  (re-exported from `content.ts`, so `publish-dialog.tsx` is unaffected).
- Media renders inside the bubble: images are bare `<img>` with no attachment
  card, no `photo` label and no size badge; audio/voice get a real `<audio>`
  player; video gets `<video controls>`; only a true file uses the new
  `Attachment` with its filename and a download. `message-card.tsx` and its
  `MediaAttachmentCard` are left in place untouched — the publish workspace
  (`components/publish/`, another session's work) imports them.
- Deletion is one `AlertDialog` with an unchecked-by-default "همچنین از کانال
  در تلگرام خارج شوم" checkbox; the standalone Telegram-leave menu item is gone.
  `lib/channel-actions.ts` builds the payload, so unticked is `telegram:"false"`.
- `channels.tsx` exports `shouldRevalidate` returning true only for a form
  submission: switching channels or changing filters never refetches the list.
  The list is read whole through `listAll` (5 × 100 cap) and search + category
  chips filter client-side.
- `routes/_panel/layout.tsx`'s `<main>` and its inner wrapper became flex
  columns so a full-height workspace fits without page scrolling; ordinary
  pages are unaffected.
- Tests: `scripts/test-panel.mjs` (esbuild + `node --test`, its own output
  directory) runs `lib/message-entities.test.ts`, `lib/channel-filter.test.ts`
  and `components/channel-explorer.test.tsx`, and is wired into `npm test`.

Scope notes: no backend code changed — `filename` and `entities` were already
in the message response. The shadcn registry is unreachable from this sandbox,
so the published `Message`/`Bubble`/`MessageScroller`/`Attachment`/`Empty`
primitives could not be installed; the local compositions are the documented
fallback and no design-system upgrade was done.

VERIFIED here: `tsc --noEmit` clean; `node scripts/test-content.mjs` (21),
`test-media.mjs` (19) and `test-panel.mjs` (57) all green; `npm run build` in
`web/apps/web` passed earlier in the session.

NOT verified: the UI in a browser. RTL pane placement (list physically right,
messages physically left), bubble left-alignment, the publish action's physical
right, reaction placement, media rendering and the responsive breakpoints are
asserted from `renderToStaticMarkup` output and class names only — never seen
rendered. This sandbox cannot bind a local port (`EPERM`), so no dev server,
daemon or static preview could be started; the preview tool also refuses the
`panel` entry in `.claude/launch.json` because its `url` carries a path. Also
not verified: the delete flow against the live `POST /local/channels/{id}/leave`.
The user will build and test.

Collaboration: a second session is redesigning the publishing dialog in the same
tree. Ownership was agreed over cross-session messages — this session does not
touch `publish-dialog.tsx` or `components/publish/**`, and the publish call site
is stable at `components/channel-explorer/channel-messages-pane.tsx` with the
props shape `open` / `onOpenChange` / `localChannelId` / `message` / `onSuccess`.

2026-11-04 — publishing dialog redesign (three-column workspace + mobile wizard).
The desktop stepper is gone: `publish-dialog.tsx` is now a shell around
`components/publish/*`, still exporting `PublishDialog` with the same props
(`open` / `onOpenChange` / `localChannelId` / `message` / `onSuccess`) so the
call site in `channel-explorer/channel-messages-pane.tsx` needed no change.
One draft, two presentations; `lib/publish-draft.ts` holds every rule and is
proved by `publish-draft.test.ts`, the markup by `publish-workspace.test.tsx`,
both run by the new `scripts/test-publish.mjs`.

Backend: `POST /local/wordpress/{id}/posts` gained `slug`, `categories`,
`tag_ids` and `media.attachment_id`; `GET /local/wordpress/{id}/categories` and
`/tags` were added so the form reads the destination site's own terms through
this backend. `wordpress.Client.ListTerms` pages the taxonomy. Telegram
`media.attachment_id` now names which source attachment is sent or uploaded.

VERIFIED: gofmt/vet/build/test all green; docsgen and --check up to date;
apidocs tests green; frontend 156 tests green; typecheck clean; frontend build
passed. NOT verified: anything rendered in a browser — RTL column order, wizard
steps and both previews are asserted from rendered markup only, because the
preview tool refuses the `panel` launch entry and this sandbox cannot bind a
port. The user will build and test.

Collaboration: a parallel session redesigned the channel explorer in the same
tree. Ownership was agreed over cross-session messages — it does not touch
`publish-dialog.tsx` or `components/publish/**`, and its refactor is finished.
One of its forks also claimed the WordPress terms work; that work was already
done here and the fork is now archived, so nothing was overwritten.

## 2026-11-04 — Channel workspace: full-bleed, no page header

Layout-only change to the already-approved channel explorer. Three edits:

- `lib/page.tsx` — `SectionHandle` gains `fullBleed?: boolean` and
  `fullBleedSection(name)`, so a route can ask the layout for the whole content
  area instead of the centered page column.
- `routes/_panel/layout.tsx` — `PanelLayout` reads the flag from
  `useMatches().some(...)` and renders `<Outlet />` either in the existing
  `mx-auto w-full max-w-6xl flex-1 flex-col px-4 py-6 sm:px-6 lg:px-8` column
  (unchanged for every other page) or in a bare
  `flex min-h-0 w-full flex-1 flex-col`. Global header, breadcrumbs and sidebar
  are untouched.
- `routes/_panel/channels.tsx` — `handle` is now `fullBleedSection("کانال‌ها")`;
  the `PageHeader` ("کانال‌ها" + "کانال‌هایی که برای پایش و خواندن محتوا…") and its
  `gap-4` are removed; the workspace's `bg-card rounded-xl border` wrapper is
  removed so it is not a floating card. The "کانال جدید" action moved into the
  channel-list toolbar.
- `components/channel-explorer/channel-list.tsx` — the toolbar's add control is
  now a compact labelled "کانال جدید" button (`size="sm"`) beside the search
  field, replacing the icon-only "افزودن کانال".

Nothing else changed: channel list, search, category chips, selection, actions,
bubbles, media, reactions, footer, "ارسال این پیام", the publishing dialog,
pagination and the API are all as they were. The only `max-w`/`mx-auto` left in
the workspace is the message column inside the messages pane
(`max-w-3xl mx-auto` in `channel-messages-pane.tsx`), which caps bubble width
for readability the way Telegram Desktop does; the workspace and both panes have
no width cap and no centering.

VERIFIED: `react-router typegen` + `tsc --noEmit` clean;
`node scripts/test-content.mjs` 21/21, `test-media.mjs` 19/19, `test-panel.mjs`
57/57, `test-publish.mjs` 59/59; `npm run build` in `web/apps/web` succeeds.

NOT verified in a browser: the workspace meeting the breadcrumb bar with no gap,
independent pane scrolling, and the mobile single-pane switch. No local port can
be bound in this sandbox (`EPERM`) and there is no authenticated backend here to
log into, so no route was rendered — these are code-level conclusions only.

Follow-up: the local `Checkbox`/`Textarea` in `components/publish/publish-primitives.tsx`
were swapped onto `@workspace/ui`. The checkbox is now Base UI's, so its change
API is `onCheckedChange` rather than `onChange`; `packages/ui` had no textarea,
so `packages/ui/src/components/textarea.tsx` was added beside `input.tsx` and is
the shared multiline control from now on. Verified after the swap with
`npm run typecheck` and `node scripts/test-publish.mjs` (59 green). The heavier
`npm run build` and the Go suite were deliberately not re-run — the user runs
those.

## 2026-11-04 — "ارسال این پیام" becomes a pill attached to its bubble

Single-purpose visual polish in `components/channel-explorer/message-item.tsx`
plus three tests. The action moves out of the message row (where it was an
outlined `Button` with a paper-plane icon in its own column) and onto the
bubble's own wrapper.

New shape: `data-slot="message-bubble"` wrapper (`relative flex flex-col
pb-4`) → `Bubble` → a `flex justify-start` row holding the pill with
`relative z-10 -mt-3.5`. The pill is shadcn `Button variant="secondary"` styled
as `h-7 rounded-full px-3 text-xs shadow-none ring-2 ring-background`, icon
removed. Under RTL `justify-start` is the physical right; `-mt-3.5` straddles
the bubble's bottom edge (14px in, 14px out), matching the shadcn "Yes, run it"
reference without its destructive colours.

Two robustness decisions worth keeping:
- The pill is **in flow** rather than absolutely positioned. A bubble narrower
  than the action grows to hold it, so the action can never overflow the
  message pane or be clipped, and it always sits inside its bubble's footprint.
- `actionBand = "pb-2"` goes on whichever of `BubbleContent` /
  `BubbleReactions` is last, and only when `onPublish` is set. It is the clear
  band the straddling control may cover, so text, images, attachments and the
  reaction chips are never obscured. It is on the last child rather than on
  `Bubble` because `cn` (a tailwind-merge engine) does not resolve `py-2.5`
  against `pb-5`, so overriding the bubble's padding would have been
  cascade-order dependent.

`SendIcon` is no longer imported. Reactions (physical bottom-left, `justify-end`),
the footer, the bubble's own look, the channel list, the media renderer and the
publishing dialog are untouched.

Tests: three added to `components/channel-explorer.test.tsx` — the pill is
inside the bubble wrapper and `rounded-full`, it straddles the bottom edge on
the physical right with a `ring-2` separation and no coloured border or icon,
and the reserved band appears on the right child for both text-only and
reacted messages. The old "publish before bubble in the DOM" assertion is gone:
DOM order no longer decides the side.

VERIFIED: `tsc --noEmit` clean; `test-content` 21/21, `test-media` 19/19,
`test-panel` 60/60, `test-publish` 59/59. The rendered markup was inspected via
a throwaway SSR render to confirm the class/DOM contract. NOT verified in a
browser: the visual comparison against the reference and responsive behaviour —
this sandbox cannot bind a port and has no authenticated backend.

## 2026-11-04 — Bidirectional message scrolling and scroll anchoring

Scroll/pagination work on the already-approved channel timeline; no interface
redesign. Started by verifying the API rather than assuming it, which changed
the design: `GET /local/channels/{id}/messages` is **not** offset paging.
`internal/worker/worker.go` keeps a per-channel message-id cursor
(`cursorFor`/`setCursor`) and `internal/gotdclient/dialogs.go` calls
`messages.getHistory` with `OffsetID`, so page 1 is the newest edge, page N
continues below the cursor, each page is newest-first, and `has_more` is
`len >= limit`. That means `page + 1` cannot skip or duplicate when new
Telegram messages arrive — but it does mean the frontend must never mix an
upward walk with a downward one on the same cursor.

New: `lib/message-timeline.ts` (pure state machine), `lib/scroll-anchor.ts`
(pure anchor maths), `hooks/use-message-timeline.ts` (fetching, generation
guard, single flight). Rewritten: `components/channel-explorer/channel-messages-pane.tsx`.
Removed: `hooks/use-infinite-messages.ts` (only the pane used it).

Behaviour: opening a channel renders the newest page chronologically and puts
the reader at the bottom once; scrolling up loads the next older page and keeps
their place by naming the message they are on; scrolling down reveals what is
loaded and fetches only when something above is genuinely missing; the jump
control fills that range and returns to the newest message; auto-follow turns
off the moment the reader scrolls away and back on at the bottom. Per-direction
loading and error state, inline retry, end-of-history stops, and a known hole is
said out loud rather than drawn as continuous history.

Two focused fixes worth naming: anchoring is by message id and re-measured
offset rather than a `scrollHeight` delta, so late media cannot drag the
reader's message; and the first render shows a skeleton rather than the empty
state, so a channel is not briefly claimed to have no messages.

Tests: 40 added (`lib/message-timeline.test.ts`, `lib/scroll-anchor.test.ts`)
covering initial load, ordering, prepending, dedupe, chain break, newer walks,
single-flight, error preservation, follow rules, jump visibility and channel
reset.

VERIFIED: `tsc --noEmit` clean; `test-content` 21/21, `test-media` 19/19,
`test-panel` 97/97, `test-publish` 59/59; `npm run build` succeeds. NOT
verified in a browser (no port binding here, no authenticated backend):
wheel/touch/keyboard scrolling and long-history smoothness.

Backend deliberately untouched: `before_id`/`after_id` would make traversal
cleaner, but the existing cursor is already id-based and stable against message
arrival, and the brief asked for minimal backend change. The one real cost is
that filling a large newer gap re-walks from the newest edge over several page
requests.

## 2026-11-04 — Fix: selecting a channel fetched nothing

Regression from the scrolling rework: opening a channel made no `/messages`
request at all. Cause — `lib/message-timeline.ts`'s `nextRequest(state,
"older")` gated on `state.hasOlder`, and `emptyTimeline` has `hasOlder: false`
because nothing is known about history before the first answer. The initial
load was routed through that same gate, so it was refused before it could
start. The old `useInfiniteMessages` called `load(1)` directly; the rewrite
sent it through the "may I ask for more?" gate by mistake.

Three fixes in `lib/message-timeline.ts` and `hooks/use-message-timeline.ts`:
- `nextRequest(..., "older")` now allows page 1 whenever nothing is loaded yet
  (`page === 0`), because that answer is what says how much history exists;
  only later pages are gated on `hasOlder`.
- The hook asks for the first page in its own effect on channel change instead
  of waiting for the top sentinel to be on screen. Opening a channel must
  fetch regardless of layout.
- The channel-reset effect writes `stateRef` synchronously, so a request made
  in the same commit cannot read the previous channel's state; and the
  single-flight lock is released before the `aborted` early-return, so an
  aborted request cannot leave the timeline unable to ask again.

Test: `the first page is asked for before anything is known about history` —
`nextRequest(resetTimeline(7), "older")` is page 1. The channel-reset test's
last assertion changed to match: a fresh timeline asks for its own page 1 and
nothing else.

VERIFIED: `tsc --noEmit` clean; `test-content` 21/21, `test-media` 19/19,
`test-panel` 98/98, `test-publish` 59/59; `npm run build` succeeds. NOT
verified: the request actually leaving the browser — no port binding and no
authenticated backend in this sandbox, so the user confirms that.

2026-11-04 — BUG FIX (mine, from the user's report: "The source attachment could
not be read; publish without media or retry."). The publish routes decoded the
media id into `photoIndex`. Two mistakes in one: the media id is **opaque** —
`internal/worker/worker.go` writes `"%d:%d" % (channelID, messageID)` into it,
and the API documents it as something to hand back, not to read — and
`photoIndex` is not an attachment selector at all, it picks which of a *photo's
sizes* is wanted (`rpc.Backend.Media`: 0 = largest). So an id like `7:42` asked
the worker for photo size 42, which does not exist, and every publish carrying
media failed with the source-attachment error.

Fix: `mediaIndexOf` is gone. Both `buildSend` and `runWordPressPublish` read at
`largestPhotoSize = 0`, exactly as the gateway's own `media()` route does, and
`attachment_id` is now an identity check (`namedAttachment`) that compares the
opaque id against `source.message.Media.ID` and refuses a mismatch instead of
quietly sending the wrong file. The frontend no longer invents `"media-0"`:
`SourceAttachment` now separates `id` (draft identity) from `mediaId` (the API's
opaque string), and `media.attachment_id` is only sent when the API actually
reported one.

Lesson worth keeping: `TestWordPressPublishUploadsTheNamedAttachment` asserted
`lastMediaIndex == 2` and **passed while the behaviour was wrong**, because the
fake worker accepts any index. A test over a stub proves the plumbing, not the
contract. The corrected tests assert `lastMediaIndex == 0` against the real
meaning of the parameter, and add two refusal cases.

VERIFIED: `go test ./internal/gateway/` green (incl. 4 new/changed attachment
tests), `gofmt -l` clean on the touched packages, `npm run typecheck` clean and
`scripts/test-publish.mjs` 59 green. NOT re-run: the full `go test ./...`,
`npm run build` — the user runs those.

## 2026-11-04 — Administrators management UX redesign (create / edit / password / six-tab permissions)

The administrator dialogs were rebuilt against a three-page product brief: create
loses its paragraph and its status switch, edit loses its password field and
gets the status on one compact row, password changing moves to its own dialog,
and the flat permissions list becomes six tabs over one shared draft with a
read-only report first.

The interesting part was the resource scope. The old dialog stored a selection
as `view: allow` on the picks plus `update: deny, delete: deny` on the rest, and
only when the kind's global viewing permission was checked. Read against
`channelstore.Authorizer`, that means the *view* was never scoped at all: an
unpicked row has no `view` assignment, so it fell through to `channels.view` and
the account saw every channel it had just been narrowed away from — while the
picker told the reader "دسترسی فقط به همان موارد". The redesigned report tab has
to say what access actually results, and under that shape the honest sentence
was "all of them". The rows now carry `view: deny` on the items left out
(D64), which is the only stored form that outranks the global grant.

Nothing about D62's write rule moved: the write denies are still there, so
`marksASelection` still marks a selection and the manage right is still narrowed
to the picks. Dropping the write denies to shorten the rows would have been the
real regression — a lone `view: deny` is an exclusion over the whole, not a
selection, and would have let a scoped manager write anything.

Second finding: `withViewDependencies` in the old route could never drop a
manage right, because it added the missing view back in the same pass. Turning
"مشاهده دسته‌بندی‌ها" off while "مدیریت دسته‌بندی‌ها" was on therefore did
nothing. It is now `toggleGlobal` that reconciles: taking a viewing permission
away takes its managing permission with it. Loaded data is still reconciled the
other way (a manage without its view gets the view added), which is what
`requireViewForManage` demands of the API.

Tests: 49 new ones in `app/lib/admin-permissions.test.ts`,
`app/lib/admin-users.test.ts` and
`app/components/administrators/administrators.test.tsx`, run by
`scripts/test-administrators.mjs` over the existing esbuild + node:test harness.
They are SSR and pure-logic only — there is no DOM runner in this workspace and
none can be added offline — so "the eye toggle flips the input type" and "the
status text follows the switch" are pinned as rendering and reducer rules rather
than as clicks. One focused Go test,
`TestAPickedScopeNarrowsWhatMayBeSeenUnderAGlobalPermission`, proves the body the
dialog now writes really does narrow what the API serves.

The third finding is older than this change and only surfaced because the tests
render the forms: `@workspace/ui`'s `FieldError` shows its message only while the
control is *natively* invalid, so a refusal the control knows nothing about —
the 409 "That username is already in use." above all — was computed, passed to
`FieldError`, and dropped on the floor. `login`, `categories`, `channels` and
`wordpress` all gate `FieldError` on their own error and were affected the same
way. The wrapper now passes `match`, which tells the primitive to render what
its caller rendered; nothing about who decides there is an error moved.

VERIFIED: `make check` green (gofmt, vet, `go test ./...`, panel version tests,
web typecheck, five web test suites 246 in total, docsgen --check, build),
panel re-embedded, `bin/telesync` built. NOT verified in a browser: the sandbox
denies local port binding, so nothing was clicked through.

One open product question left deliberately unresolved: `content.publish` has no
editor under the six-tab layout the brief specifies (the عمومی tab is to hold the
server-status permission alone). It is preserved in the draft and round-trips
unchanged so a save never deletes a permission it was not asked about, and the
report tab shows it read-only. If the owner wants it editable, the عمومی tab is
where it belongs.

## 2026-11-04 — responsive panel: bottom navigation and scrollable tables

- Shell: `SidebarProvider`'s mobile gate moved from `<md` to `<lg` (1024px) and
  the desktop `<aside>` from `md:block` to `lg:block`; the panel layout no longer
  renders `SidebarTrigger`, its header is `hidden … lg:flex`, and the floating
  `BottomNav` holds the five destinations with icons only. It is a sibling of
  the content column, so it is viewport-fixed and not relative to the
  breadcrumb bar. Below `lg` the header is gone, so the breadcrumb now exists
  only on desktop — flagged for the owner.
- Tables: the shared `Table` table is `min-w-max` inside its single
  `overflow-x-auto` container, so narrow viewports scroll the table and never
  the document. Actions cells are `sticky left-0` (physical left in RTL); the
  inset edge shadow the primitives first carried was removed because the
  saw it as an extra border the other tables do not have.
- WordPress: card list removed, one table at every width, toolbar stacks below
  `lg`, search full width.
- Tests: `apps/web/scripts/test-responsive.mjs` bundles `*.responsive.test.tsx`
  (esbuild + node:test) and runs them; it is not yet wired into `npm test`.
  `scripts/woff2-cmap.mjs` (a WOFF2 cmap reader) was started to check the
  bundled Vazirmatn glyphs and is not used by any test yet.
- Not verified: no browser (Chromium cannot start in this sandbox).

## 2026-11-05 — media downloads were being cut off part-way

- Reported as `curl: (18) transfer closed with 145091 bytes remaining to read`
  on `GET /api/v1/telegram/official/channels/{id}/messages/{id}/media`. Not an
  HTTP error: `Content-Length` was announced and the connection closed first.
- Diagnosis, prepared under `.agents/handoffs/media-download-cut-short.md`
  (Discover → Plan → Red → Handoff, then implement on the owner's approval).
  Two mechanisms were reproduced standalone on Go 1.27.3 before any code moved:
  a request context deadline cut a 20-byte body to 2 bytes with `context deadline
  exceeded`, and a 100ms `WriteTimeout` cut the same body to 2 bytes with
  `unexpected EOF` — the exact shape of the report. Clearing the write deadline
  delivered all 20 bytes.
- Fixes: `gateway.media` streams on
  `context.WithCancel(context.WithoutCancel(ctx))` so `RequestTimeout` bounds the
  setup but not the body, clears the write deadline before the first byte the way
  `rpc.writeMediaStream` already does, and logs any transfer that delivered fewer
  bytes than it promised with both counts. `gotdclient.mediaReader` now fails
  instead of returning `io.EOF` when Telegram sends 0 bytes before the size it
  reported. Publish paths keep their bound; the HTTP contract is unchanged
  (`docsgen --check`: 0 created, 0 updated, 0 stale).
- Tests: five added, red against the old code and green after —
  `TestMediaDownloadOutlivesTheRequestTimeout`,
  `TestMediaDownloadOutlivesTheServerWriteTimeout`,
  `TestMediaDownloadReportsATruncatedStream`,
  `TestMediaDownloadReportsAShortBodyThatDidNotFail`,
  `TestDownloadMediaFailsWhenTelegramSendsFewerBytesThanItReported`. One
  assertion was tightened mid-task (it accepted unrelated digits in the log
  line) and recorded in the handoff.
- Verified: `go test ./...` green apart from two pre-existing
  `internal/worker` socket-path failures (`bind: invalid argument`, the
  `$TMPDIR` path exceeds macOS's `sockaddr_un` limit — environmental), plus
  `gofmt`, `go vet`, `go build`, `docsgen --check`, `go test ./internal/apidocs`.
- **Live verification named the real cause, and it was not the deadlines.** The
  owner's rerun produced `truncated after 0 of 145091 bytes`, `0 of 114566`,
  `0 of 138858`, `0 of 184453` and `1572864 of 1756960`. Every figure fits one
  rule: `upload.getFile` is served only in parts whose `limit` is a multiple of
  4 KiB, and `mediaReader.Read` asked for "what is left". 145091, 114566,
  138858, 184453 and the 184096-byte tail of 1756960 are none of them multiples
  of 4096 — five for five, and 184096 is exactly the count `curl` reported as
  remaining. gotd's own downloader always asks for `defaultPartSize` (512 KiB)
  and learns the end from a short chunk; `precise` relaxes an arbitrary offset,
  not an unaligned limit. Fixed: `mediaReader.Read` asks for a limit that is
  always a `chunkAlign` (4 KiB) multiple and never more than `mediaPart`, and
  drops the bytes a part carries past the slice. The `downloadServer` test
  double now refuses an unaligned limit (`LIMIT_INVALID`), which is what made it
  visible; `TestDownloadMediaServesAByteRange`'s "only asks for the bytes it
  needs" assertion is replaced by "never asks for more than one part", since
  Telegram will not serve a part below 4 KiB. Red: five tests `LIMIT_INVALID`;
  green after. `gofmt`/`go vet`/`go build`/`docsgen --check` clean.
- Review named three further truncation producers in the gotd reconnect
  lifecycle (per-part `apiClient` during a reconnect, watchdog generation
  cancel, one file reference refresh per stream) that are now logged rather than
  fixed — see D65 and the handoff.
- **Owner's live test: the problem is completely solved.** Rebuilt and reran the
  originally failing download; it completes with no truncation. This is now
  live-verified, not just tested. Uncommitted at the owner's request.
- Graph: refreshed with `--code-only` (matches HEAD); the semantic pass over 183
  non-code files could not run for want of an LLM API key.
- 2026-11-05: the message timeline (`/channels/:id/messages`) names the route
  handle `hiddenNavSection`, and the shell slides `BottomNav` down
  (`translate-y`, `opacity`, 300ms, `motion-reduce` off) instead of unmounting
  it, with `inert`/`aria-hidden` while away; the full-bleed padding that clears
  the bar is dropped for that route and animates back on other pages.
- 2026-11-05: Telegram-to-other-connection media publishing fixed (D66). Reported
  as one bug — the publish dialog showed `دریافت عکس ناموفق بود` and the publish
  returned `publish_partial_failure` / "The source attachment could not be read" —
  but the two had unrelated causes. The dialog drew
  `MediaAttachmentCard status="ready" url=""`, which the card renders in its
  failure branch, so a fetch that was never attempted was shown as one that
  failed; the WordPress path fetched and the Telegram path did not. The publish
  read the source attachment through `buildSend`'s `worker` parameter, which
  `serveTelegram` resolves from the URL path key — the **destination** connection
  the caller chose. The red tests reproduced the production error verbatim with
  `official` as the source and `cat` as the destination. Fixed by carrying the
  source worker in `sourceMessage` (it was already resolved in
  `resolvePublishSource` and discarded) and reading through it on both routes;
  `publishWorkerFor`, which returned a nil `Worker` for an unknown key, is gone.
  New `publish_source_media_unavailable` / `publish_media_upload_failed` codes
  separate the two stages; `sendFailureOf` classifies a bare `*rpc.Error` and a
  `rpc.Err*` sentinel the same way. Frontend: `lib/media-blob-cache.ts` (one
  shared download and one refcounted object URL per attachment, never caching a
  failure), `components/publish/source-attachment-media.tsx` (loading / picture /
  gone / failed with `تلاش مجدد` and `حذف پیوست`, attachment stays attached),
  `lib/publish-errors.ts` (codes → Persian, the backend's English never shown).
  14 gateway tests red → green; web suites 283 green. Not live-verified: no
  daemon configuration, Telegram credentials or `cat` endpoint in this checkout.

2026-11-05 — the publishing dialog gained a third managed field: **links**,
beside the hashtags and the mentions. Requested as "extract the links of the
message with their anchor text and their link, in a list I can edit, delete and
add to; one I add goes to the end of the message".

The design decision worth keeping: a link is **not** lifted out of the body the
way a hashtag is. A hashtag is a standalone token, so `parse` removes it and
`compose` appends the chips at the end. A link's anchor text is a word of a
sentence — "read our report" depends on "report" staying where it is — so
`ParsedContent.links` reports the links while their text stays in `main`. Only a
link the user adds is appended, on its own line, so it can never merge into the
text or the URL before it.

`LinkItem { id, anchorText, url }` is the model; `locateLinks(text, links)` is
the one function that says where each anchor text sits, matching links in listed
order so two links sharing an anchor text are never confused. `addLink`,
`editLink` and `deleteLink` rewrite the body through it — renaming a link
rewrites its own span, deleting takes the anchor text with it, and editing the
target leaves the text alone. `setTelegramText` reconciles the list with the
body: a link whose anchor text is gone goes with it, and a bare URL the text
gained becomes a link of its own, so the list never shows fewer links than the
message carries. Rows commit on blur, not per keystroke, so the body is never
rewritten under a half-typed word.

Outgoing: `composeTelegramOutgoing` writes the link spans from the list and
every other source of them stands down. A link whose anchor text is the URL is
Telegram's `url`; one drawn on other words is `text_url` carrying `url`. **No
backend change was needed** — `rpc.Entity` already carries `url` and
`internal/gotdclient/inputEntity` already maps `text_url` to
`tg.MessageEntityTextURL`, so the target survives the whole way to Telegram.

VERIFIED: `npm test` green — 306 tests across the five runners (content 26,
media 19, panel 109, publish 103 incl. 12 new link tests, administrators 49);
`npm run typecheck` clean. No Go change, so no Go run was needed. NOT verified:
the interface in a browser (the preview tool still cannot start the panel) and
the links arriving on Telegram from a live send.

2026-11-05 — follow-up: the links carry into the WordPress article as well.

The list moved off the Telegram draft onto `PublishDraftState.links`: one draft,
one list, and the post and the article are two renderings of the same message.
Both bodies keep the anchor text, both are rewritten by `addLink` (appended at
the end of each), `editLink` and `deleteLink`, and `reconcileLinks` now drops a
link only when **neither** body shows its words — erasing a URL from the post
must not take it out of the article.

`composeWordPressContent` is what turns the body into the request's
`post.content`: it wraps each located anchor in `<a href="…">` and leaves
everything else byte-for-byte as the user wrote it. Only the link's own two
values are escaped (`escapeAttribute`/`escapeText`), so a target or a label
cannot close the tag it is written into and markup the user typed themselves is
never reinterpreted. The wire name is unchanged (`post.content`), so no API docs
changed; the value now matches what WordPress's content field has always
expected.

VERIFIED: `npm test` 311 green (content 26, media 19, panel 109, publish 108
including the new article-link tests, administrators 49); `npm run typecheck`
clean. NOT verified: in a browser, and a live WordPress publish with links.

## 2026-11-06 — v0.0.3: background automation, webhooks and AI publishing

- Added the durable automation subsystem. One shared ingestion path normalizes
  new channel messages into `automation_events` (identity derived from type,
  network, channel and message, so a replay is the same event and two accounts
  on one network are one event) and fans out to a SQLite job queue.
- `automation_jobs` claims work in a transaction with a lease, retries with
  bounded backoff that honours a provider's own delay, and preserves per-target
  chronological order. A job that failed for good is recorded and does not block
  the ones behind it.
- Webhooks deliver a CloudEvents envelope with media as metadata plus a
  URL — never bytes — to an SSRF-restricted transport, with four authentication
  modes and Standard-Webhooks-compatible HMAC. Every request and response is
  stored, redacted and truncated, and a retry extends the same delivery.
- One workflow per managed channel publishes to Telegram and WordPress through
  the extracted v0.0.2 publisher. AI polishing is optional, off by default, and
  blocks publication on failure unless the workflow opted into fallback.
  Images are excluded from the AI by default and still reach the destination.
- Loop prevention records every publication this installation makes and
  suppresses it on the way back in.
- A workflow publishes under the authority of the account that created it,
  re-read at every execution. The operator token is not an account, so a
  workflow created through it cannot be switched on.
- Fixed `internal/docsgen`'s permission extraction: it scanned
  `internal/gateway` for permission constants that live in
  `internal/channelstore`, so `facts.json`'s `permissions` had been `null` and
  no permission was documented at all.
- Fixed a permission drift in the panel: the desktop sidebar iterated the raw
  `navItems` and so ignored access, while the bottom nav filtered correctly.
- Added three global permissions: `automation.view`, `webhooks.manage`,
  `workflows.manage`.
- **Verified:** `gofmt -l .`, `go vet ./...`, `go test ./... -count=1`,
  `go build ./...`, `go run ./cmd/docsgen --check`, `go test ./internal/apidocs`,
  `cd web && npm run typecheck`, `cd web/apps/web && npm test` (426 passing).
  **Not verified:** no live Telegram, WordPress, AI provider or webhook
  receiver; no browser session. `internal/worker`'s socket tests fail on this
  machine because the Unix socket path exceeds `sun_path`; the package is
  untouched by this release.

## 2026-11-06 — v0.0.3 integration: the UI was unreachable and the listener was silent

- **Why the automation UI was invisible.** Two independent causes, both confirmed:
  the embedded panel (`internal/panel/files`, folded into the binary at build
  time) had been built before the automation UI existed — `go build` embeds that
  directory and never rebuilds the frontend — and the v0.0.3 source was
  uncommitted in one worktree, so `main` and every CI-built image
  (`ghcr.io/mirzawho/telesync`) carried none of it. Rebuilt with
  `node web/scripts/build-panel.mjs`; the served bundle now contains nine
  automation routes.
- **The MTProto listener is wired.** `internal/profile/profile.go:180`'s no-op
  `UpdateHandler` is now a seam filled by gotd's `updates.Manager`. A worker
  normalizes one bounded observation per new channel message and sends it over a
  private `automation.sock` to the daemon, which persists it and fans out to the
  queue. Media crosses as metadata only.
  **Ordering:** gotd advances pts/qts/seq only after the handler returns, and
  the handler returns only after the daemon has committed the event, so the
  stored counter can never run ahead of the stored event. A crash between the
  two replays the message, which deduplication turns into a no-op.
- Fixed in `internal/gateway/automation.go`: the webhook test-send needed only
  `automation.view` though it dispatches a real signed request; a delivery retry
  never re-checked the caller could see the webhook; `validAuthHeader` accepted
  `Webhook-Anything` rather than refusing the whole `webhook-` prefix.
- Fixed: `ListWebhooks`/`ListWorkflows` ignored the visibility restriction and
  so listed other accounts' rows and counted them in `total`; `ListWebhooks`
  never loaded `source_channel_ids`.
- Fixed: a malformed `whsec_` secret was sealed happily and only failed at the
  first delivery. It is now refused where it is typed.
- Fixed: `serveLocal`'s default answered 405 to an unknown sub-path instead of
  404, so a mistyped address read as a wrong method.
- **Found by a real browser, missed by every test:** the status screen read the
  response envelope as the record, so a live subsystem rendered as "اتوماسیون
  غیرفعال است" with zero workers. `statusRecord` unwraps it; pinned by a test.
- Verified in a real authenticated browser against a running daemon built from
  this worktree: the sidebar entry `اتوماسیون`, the overview, the webhook list
  and its create form at `/automation/webhooks/new`, the workflow list, and the
  mobile overflow (`بیشتر`, 44px rows, anchored above the pill, active on nested
  routes).
- **Verified:** `gofmt -l .`, `go vet ./...`, `go test ./... -count=1`,
  `go build ./cmd/telesync`, `go run ./cmd/docsgen --check`,
  `go test ./internal/apidocs`, `npm run typecheck`, `npm test` (477 passing).
  **Not verified:** nothing was exercised against a live Telegram account,
  WordPress site, AI provider or webhook receiver; no credentials exist in this
  checkout. `internal/worker`'s two socket tests still fail because the temp
  path (112 bytes) exceeds macOS's `sun_path` (104) — pre-existing,
  environmental, and `internal/worker` is unchanged by this release.

## 2026-11-07 — the automation overview is the management screen

The overview at `/automation` was a status strip and two doors: queue counters,
a five-row preview per collection behind "همه …" buttons, and a recent-failures
list. The two real tables lived on screens of their own. It is now one screen —
header with a single Add button, a fit-content tab list beside a search field,
and the open collection's full paginated table — and it reads the open
collection only.

- Removed: `وضعیت صف` (all five counters), `شکست‌های اخیر`, both preview lists,
  and both "همه وب‌هوک‌ها" / "همه ورک‌فلوها" doors. The loader no longer calls
  `GET /local/automation/status` at all; the queue, its API and its workers are
  untouched and still run. `readStatus`/`statusRecord`/`StatusCount` stay in the
  vocabulary because their own tests read them, but nothing on this page uses
  them.
- Added: a `clientAction` for the row intents (webhook toggle/test/delete,
  workflow toggle/delete) that answers by redirecting back to the view the row
  was on, so the table is re-read from the server rather than flipped in place.
  A refused workflow switch is shown on its own row and keeps the stored state.
- Tab, query and page are three separate facts in the address bar, and each tab
  remembers its own — switching collections cannot carry a query over. A typed
  search is sent after 300 ms and starts at the first page. Only the open
  collection is fetched, so no stale rows of the other one can show under its
  heading.
- `WebhooksTable` and `WorkflowsTable` are reused as they are; the editor
  screens and the standalone list routes are unchanged.

**Verified** in a real browser against the dev server with a stub gateway, at
1440×900 and 390×844: title/subtitle on the physical right and the one Add
button on the left; the tab list is 174 px wide (fit-content, `w-fit shrink-0`)
and shares one row with the search at both sizes; `?tab=workflows` swaps the
Add button to `افزودن ورک‌فلو` → `/automation/workflows/new` and the placeholder
to `جستجو در ورک‌فلوها...`; typing `خبر` on the workflows tab went out as
`/local/automation/workflows?page=1&per_page=20&q=…` and never touched the
webhooks endpoint; `صفحه بعد` landed on `?page=2` with `صفحه ۲ از ۲ · ۲۳ رکورد`
and the way on disabled; no `/local/automation/status` request in the whole
session; no horizontal document scroll on the phone, the table scrolls inside
its own container with the actions column pinned, and the bottom navigation
does not cover the pagination.

**Verified:** `npm run typecheck`, `npm run build`, `npm test` (575 passing
across the eight suites).
**Not verified:** nothing ran against a live daemon — the browser pass used a
stub gateway, so no row action, delivery or run was exercised against real
data. The panel bundle under `internal/panel/files` was not rebuilt
(`node web/scripts/build-panel.mjs`), so a binary built today still serves the
old overview until it is.

## 2026-11-08 — the forms are pages, and `isCreation` was not defined

Two things: the dialog-based create/edit of webhooks and workflows became
dedicated full-page forms, and a white-screen crash in the shipped bundle was
found and fixed.

**The crash.** `isCreation is not defined` in the browser, with a build that
finished green. The route module exported `isCreation`/`validID`, and Vite's
per-route split put the client-loader and the client-action in their own chunks
while the exported helper landed in neither — so the chunk referenced it as a
free variable. It builds, it typechecks, it passes every test, and it throws on
first navigation. **Fixed** by keeping those helpers module-private (the tests
never imported them). A scanner now walks every emitted chunk for an identifier
that is neither declared nor imported, and the sweep over every function the app
declares comes back clean.

**The forms.** `/automation/webhooks/new`, `/automation/webhooks/:id/edit`,
`/automation/workflows/new`, `/automation/workflows/:id/edit` — one form
component per collection, one shared shell (`AutomationFormLayout`,
`AutomationFormActions`). The old bare addresses redirect, so a bookmark lands
where it always meant to; an id that was never a record is reported, not turned
into an editor. `webhook-dialog.tsx`, `automation-webhooks.tsx`,
`automation-workflows.tsx` and `automation-workflow-editor.tsx` are gone.

**The polish.** Advanced settings (the webhook's timeout and retry) sit in a
Base UI accordion under the shadcn `Accordion`/`AccordionItem`/`AccordionTrigger`
/`AccordionContent` names, shut until opened and kept mounted so a collapse costs
nothing typed into it. Source selection is connection first, channel second: the
list is the chosen connection's own channels, read off the managed row's
`client_key`, so it is a filter over the record and not a new field. A saved
channel from another connection is preserved rather than trimmed; a deliberate
connection change drops what it cannot reach. Both forms are `max-w-4xl`, and
Cancel then Save in the DOM puts Save at the outermost physical left in RTL.

**Verified** in a real browser at 1440×900 and 390×844: Save at x=144 and Cancel
at x=212 on both forms (identical), the accordion open and shut with `12345`
still in the timeout field after two toggles, the `custom` connection offering
only `کانال فناوری`, and the switch to `official` clearing it with Save going
disabled again. No horizontal overflow on the phone.

**Verified:** `npx tsc --noEmit`, `npm test` (573 across eight suites),
`npm run build`.
**Not verified:** against a live daemon — the browser pass ran against a stub
gateway, so no write reached real data. The workflow form takes no accordion
because it has no advanced-only settings: source, destinations, the AI stage and
the status are all essential and stay on the page.

## 2026-11-08 — `publish_forbidden`: the background publisher had no account to run as

A workflow run published nothing and reported
`publish_forbidden` — "This deployment cannot resolve the account a background
publication runs under."

That sentence is `publishing.actorFor`'s `s.Actors == nil` branch. A background
publication is recorded against the account it runs under — the idempotency
record names one, so a retry with the same key cannot post twice — and the
account's permissions are read again at execution time, so a withdrawn grant
stops the next publication. `ActorResolver` is what does that reading.

`cmd/telesync/runtime.go` built its `publishing.Service` without `Actors`. The
interactive route never showed it, because that path passes its own actor and
does not need the resolver; only the background engine calls `actorFor`. Every
workflow publication was therefore refused before anything was sent.

- Fixed: `newBackgroundPublisher` now sets `Actors: store`, the same
  `channelstore.Store` the rest of the pipeline reads. Extracted as a function
  of its own precisely because the field is load-bearing and was dropped once.
- Pinned by `cmd/telesync/runtime_test.go`: it fails without the field ("the
  background publisher cannot resolve the account it publishes under") and
  passes with it — checked both ways.

**Verified:** `gofmt -l`, `go vet ./...`, `go build ./...`, `go test ./...`
(28 packages green). `internal/worker`'s two socket tests still fail on the
pre-existing macOS `sun_path` limit — unchanged by this fix.
**Not verified:** against a live Telegram account or a real WordPress site; no
credentials in this checkout.

## 2026-11-08 — AI polishing reported success and returned the source unchanged

A workflow whose operator asked for a friendlier, longer, more detailed edit
consumed 580 input and 26 output tokens, reported `AI: Successful`, published,
and the run showed the polished text identical to the original.

**Root cause: prompt composition, not the provider and not the plumbing.** Every
stage after the model was traced and is correct — the prompt is persisted, the
worker reads it (`engine_test.go` asserts `Prompt == "polish it"`), it is sent
in the system turn, `DecodeOutput` has no fallback to the original, `result.Text`
is what is published, and `polished_content` is stored and read back distinctly
(`TestPolishedContentIsPublishedAndTheSourceIsUnchanged` already pins all of
that).

What was wrong is the system prompt. `DefaultPrompt` said
"Never invent facts... **If the source does not contain something, you do not
add it.**" and `customPromptHeader` said "where anything below conflicts with
the rules above, **the rules above win**". Read together, "make it longer and
add detail" is a conflict and loses — so a compliant model returns the source
unchanged and reports a successful polish. 26 output tokens is exactly that:
the source inside the JSON envelope.

- Fixed in `internal/automation/ai/polish.go`: the no-invention rule is now
  about **facts**, and a new rule states that how the facts are said is an
  editing choice the operator directs — "Expanding on what the source already
  says is not inventing a fact; inventing one is." The header now scopes its
  precedence to the SECURITY RULES and OUTPUT FORMAT ("not negotiable, they
  always win") and makes the EDITING RULES "the default, not a ceiling... they
  govern the edit".
- Bounded second attempt in `client.go`: only when the workflow has an operator
  prompt **and** the answer is byte-identical to the source, one extra call with
  a nudge. Identical output is **never** a failure: a message that already reads
  as the operator wants is a legitimate no-op and is published as it stands.

**Verified:** `gofmt -l`, `go vet ./...`, `go build ./...`, `go test ./...`
(28 packages), and the frontend suite plus `tsc` — no frontend change was
needed, the run page already reads `originalContent` and `polishedContent`
correctly. Both the retry tests and the composition tests were checked to fail
without the fix and pass with it.
**Not verified:** against a real provider — no credentials in this checkout, so
the reported case is proven against a mock, not against `openai/gpt-5.4-mini`.
`internal/worker`'s two socket tests still fail on the pre-existing macOS
`sun_path` limit.

## 2026-11-08 — the AI "polish" was our own prompt telling the model to stop

The previous fix did not work, and this time the real provider was called to
find out why. Three calls against `sadgpt.ir` with the deployment's own
credentials and the reported Persian prompt settled it.

**What the model actually returns.** Not the original: the answer differs from
the source in exactly two characters — two ASCII spaces become Persian ZWNJ
(U+200C). `finish_reason: "stop"`, 39 output tokens. The model was doing
Persian orthographic normalisation and nothing else, which reads as "the
original message" because ZWNJ is invisible.

**Why the previous fix had no effect.** Two reasons, both now fixed.

1. The bounded retry compared byte-for-byte, and a normalised answer is never
   byte-identical to the source — so the retry never fired once. `sameEdited`
   now compares after collapsing the invisible joiners and whitespace, which is
   the only thing a rewrite is allowed to move without changing anything.
2. `DefaultPrompt` told the model the job was a light copy-edit. It opened with
   "Your only task is to edit one source message" and led the editing rules with
   "Improve spelling, punctuation, readability, and formatting" — which is
   precisely what the model delivered. The operator's instruction sat behind a
   "supplementary" header at the end of a 2800-character system message.

**Proof that the model can do it.** A minimal prompt with the same source and no
`DefaultPrompt` produced a warm, expanded rewrite (69 → 468 characters). So the
model was capable and ours was suppressing it. Moving the operator's instruction
to the user turn changed nothing; the constraint was the framing.

- `DefaultPrompt` now opens with HOW MUCH TO CHANGE: with operator instructions
  those are the brief and a copy edit does not do the job; without them, the
  edit is a copy edit and the wording stays alone. The copy-edit rule moved out
  of the lead position.
- `customPromptHeader` calls the operator's instructions THE BRIEF and says they
  govern the edit. The security and output rules keep their absolute precedence.
- `finish_reason` is now read: an answer cut off at the output limit is refused
  as `ai_invalid_output`, never taken as a completed edit.
- A workflow with an operator prompt whose answer is still the source after the
  bounded second attempt fails with `ai_unchanged`, so the workflow's own
  fallback policy decides — the run never claims a rewrite that did not happen.
  Unchanged output stays a legitimate success for a workflow with no
  instructions of its own: this rule is not universal.

**Verified against the real provider** (`TELESYNC_AI_LIVE=1`), two independent
calls, both genuinely rewritten and meaning preserved — 64 and 88 output tokens.
`bin/telesync` rebuilt from this source; the daemon needs a restart to pick it up.

**Verified:** `gofmt -l`, `go vet ./...`, `go build ./...`, `go test ./...`
(28 packages), frontend suite (573) and `tsc`. Retry, orthographic-comparison
and truncation tests all checked to fail without their fix.
**Not verified:** a live Workflow run against a real Telegram destination — the
provider was called directly, not through a publication.

## 2026-11-08 — webhooks may reach localhost, on two keys

The webhook engine refused localhost outright: the SSRF rules treat loopback as
"not a public address" and the URL validator required https everywhere. A
receiver on the same machine could not be used. This adds a loopback exception
as an explicit, two-key opt-in and leaves every other address exactly as it was.

`TELESYNC_WEBHOOK_ALLOW_LOCALHOST` (off by default) is the deployment's half; a
webhook's own `allow_localhost` (off by default, new column in migration 16) is
the other. Both, never either. With both on, `http://localhost`, `http://127.0.0.1`
and `http://[::1]` are delivered over plain http.

**The exception is a list of three, not a range.** `isLoopbackHost` compares the
literal host, so `127.1`, `0x7f000001`, `2130706433`, `::ffff:127.0.0.1` and
`127.0.0.2` are refused, as is any name that merely contains "localhost". A
loopback target must resolve to loopback and nothing else, so a DNS rebind
cannot carry a permitted request elsewhere. Redirects are still not followed
and no proxy is consulted. A policy refusal is recorded as `invalid_target`,
non-retryable; a connection that simply fails keeps the ordinary retry policy.

**Two real bugs the tests caught.** An update that named only `name` reset
`allow_localhost` to false, because the update seed did not carry it — now it
does. And `NewClient`'s test options overwrote the production defaults with nil,
so a `webhooks.Config` with no transport or dialler produced a client with
neither: the first real delivery panicked. A nil option is now ignored.

**Verified** with real `httptest` servers and the production client: a permitted
loopback webhook is delivered (POST, bearer token, payload, `202` and `thanks`
recorded), and refused — without contacting the receiver — when either key is
off. The form's switch is in `تنظیمات پیشرفته`, disabled with a reason when the
deployment forbids it, and the form's URL rule now mirrors the server's.

**Verified:** `gofmt -l`, `go vet`, `go build`, `go test` (appconfig,
channelstore, automation/…, gateway, runtime, apidocs), frontend `npm test`
(678) and `tsc`; `go run ./cmd/docsgen --check` after regenerating.
**Not verified:** a delivery to a real receiver outside this machine.
**Unrelated:** `internal/webhooktest/` appeared in the worktree mid-task while
this was being written and did not compile yet. It is not this change and was
left alone; it is now complete and passing (see the next entry).

## 2026-11-08 — `telesync serve-webhook`, a receiver for the delivery engine

The webhook engine had no way to check its own deliveries without pointing them
at somebody else's server. This adds a receiver to the same binary: `telesync
serve-webhook` runs an independent HTTP server with exactly four paths, one per
authentication mode (`/webhook/hmac`, `/webhook/bearer`, `/webhook/header`,
`/webhook/none`), each verifying the credential that mode uses and then
answering with the request it received.

**It reuses the sender's contract rather than reimplementing it.** Verification
is `webhooks.Verify` over the raw bytes that arrived; the secret is decoded by
`webhooks.SigningKey`, so `whsec_<base64>` and a literal passphrase behave as
they do on the sending side; and the custom header name is validated with the
sender's own `validAuthHeaderName`, now exported as `ValidAuthHeaderName` — a
receiver that accepted `Authorization` or a `webhook-` name would be waiting on
a header the sender never sets. Nothing in the delivery path changed.

**The replay window is the receiver's to choose,** because none exists on the
sending side: `Verify` takes the timestamp as given and the Standard Webhooks
scheme does not fix a tolerance. `TimestampTolerance` is five minutes in each
direction; a timestamp outside it is `expired_timestamp`, distinct from a
malformed one. A request whose body was not whole is never authenticated at all
— a signature covers bytes this receiver did not keep.

**Four paths, one handler.** The path picks the mode and the method changes
nothing, so there is no `/webhook/hmac/post` and no way for the verb to decide
how a request is checked. HEAD carries the headers of the reply it would have
had and no body. Everything else is 404. A body over 2 MiB is refused with 413
without being read.

**The echo is deliberately unguarded**, which is the point: headers come back
including the authentication ones, bodies keep their exact bytes (valid JSON is
also parsed; anything not valid UTF-8 is base64 rather than converted). That is
why it binds loopback by default and warns loudly if asked not to, why its
records are `0700`/`0600`, and why the console prints the method, path, verdict
and status and never a credential or a body.

**Verified** end to end against the real sender: `internal/automation/webhooks/
receiver_compat_test.go` runs `Service.TestWebhook` and `Service.Handle` through
the production transport into a running receiver in all four modes (and refuses
a mismatched secret in each), asserting the body the engine signed is the body
the receiver echoed, byte for byte. Run live against `./bin/telesync`: the four
endpoints on 8091, a computed Standard-Webhooks signature accepted and the same
signature refused after the body was changed, bearer and custom-header both
valid and invalid, HEAD, 404, 413, 16 records written under
`temp/webhook-responses` with their absolute paths on the console and no secret
or body in it, and SIGINT exiting 0 after `stopping`.
**Not verified:** a delivery from a live Telegram event (the queue and the
ingest socket are not involved here), and anything beyond loopback.
**Unrelated and pre-existing:** `internal/worker`'s two socket tests fail in
this environment (`bind: invalid argument` at the default macOS temp path
length, and a second-listener assertion with a short one). Nothing in this
change touches that package.

## 2026-11-09 — the loopback exception had three places it could fail to take

Three separate faults, each of which looked like "localhost is still refused":

1. `automation.webhooks.allow_localhost` had no field on `rawWebhooks`, and the
   loader decodes with `DisallowUnknownFields`, so a deployment that set the
   knob where every sibling `automation.webhooks.*` setting lives failed to
   start. The knob is now one value with two homes — the configuration file, and
   `TELESYNC_WEBHOOK_ALLOW_LOCALHOST` overriding it whenever the environment
   says anything. An unrecognised environment value still fails closed rather
   than being ignored.
2. The form's `clientAction` re-checked the URL with `urlErrorOf(draft.url)` —
   one argument, so the per-webhook opt-in defaulted to false and every loopback
   address was refused at the door whatever the switch showed. The check is the
   same one the field runs and must carry the same inputs.
3. The refusal named only the webhook's own switch, which is disabled when the
   deployment forbids the exception — pointing an operator at a control that
   cannot move. The two causes now say which setting can.

`urlErrorOf`'s `allowLocalhost` is now **required**, not defaulted: dropping it
is a type error rather than a silent policy change. That is the fix for the
class, not just the instance.

**Verified:** the real `~/.telesync/config.json` loads and reports
`webhooks.allow_localhost=true`; 580 frontend tests pass; `make panel` and
`bin/telesync` rebuilt. **Not verified:** an end-to-end save through the browser
behind the admin login.

## 2026-11-09 — the webhook payload names the channel, not only its row ids

`data.source` carried three identifiers and nothing a receiver could show. It
now also carries `type`, `title` and `username`, so a feed can be read without
calling back into this installation.

The labels are read through a `ChannelSource` seam — the same shape as the
existing `EventSource`, so the orchestrator wires whichever store holds the
managed channels without that store knowing about webhooks. They are resolved at
delivery time and not snapshotted at ingestion: this is what the channel
messages API reports about the channel, and the request body is already frozen
per delivery.

Labels are labels and never keys: the two id fields are what a receiver matches
on, because a title is renamed and a username is taken away. A channel row that
is gone leaves them empty and the delivery still goes; a lookup that *fails* is
reported, because an incomplete payload is not the same thing as an absent name.

**Verified:** `go test ./...`, docsgen `--check`, `internal/apidocs`, and the
exact JSON keys pinned by test so the contract cannot drift from the struct
tags.

## 2026-11-09 — v0.0.3 release preparation

Prepared and published v0.0.3. Three release-side fixes came out of the review:

- **The AI retry no longer hides a provider failure.** When the second,
  bounded attempt broke (cut off, unparsable, empty answer), the code fell back
  to the first unchanged answer and the run failed with `ai_unchanged` — "the
  model declined" — when the truth was a provider error with its own retry
  decision. The second attempt's own error is now returned. Pinned by
  `TestABrokenSecondAttemptFailsAsTheProviderError`.
- **A live worker socket is no longer stolen.** `worker.Listen` removed any
  existing socket file before binding, so a second worker started on the same
  path took it away from the one already serving. It now dials first: a socket
  that accepts connections is refused as in use; only a dead socket is replaced.
  This also fixes the long-known `internal/worker` test failures, whose test
  harness was hiding the sandbox's `EINVAL` instead of skipping it.
- **CI checks the embedded panel is current.** `go test` never noticed that
  `internal/panel/files` was older than `web/`; a new frontend job rebuilds it
  and fails when a tracked file changes.

Also: the `allow_localhost` API refusal names both the configuration key and the
environment variable, and the README describes v0.0.3.

**Verified:** `gofmt -l`, `go vet ./...`, `go test ./...` (all packages green,
`internal/worker` included with both the default and a short `TMPDIR`),
`go build ./...`, `docsgen --check` (84 pages), `internal/apidocs`, web
typecheck + build, and all eight web suites (26 + 19 + 109 + 108 + 50 + 202 +
18 + 48). **Not verified:** anything against live Telegram, WordPress, an AI
provider or an external webhook receiver; the panel in a browser.

## 2026-11-09 — prepare: `POST /messages` becomes `/messages/copy` (not implemented)

Preparation session only. Handoff
`.agents/handoffs/telegram-messages-copy-route.md` is `AWAITING_APPROVAL` at
baseline `887910f`; no production code was edited. Changes on disk are one new
test file (`internal/gateway/publish_route_test.go`), the path literals in
`publish_test.go` / `publish_media_test.go`, and a refreshed `graphify-out/`.

- **The rename is a correction, not a rebrand.** `publishing.Validate`
  (`publishing/request.go:113`) refuses any request whose `source.local_channel_id`
  or `source.message_id` is not positive, so the route can only ever copy an
  existing message. The name promised a free-text send the body has no way to
  express, and left no room to add one beside it.
- **`publishing/` is untouched and stays live.** `PublishTelegram` has exactly
  two production callers: `gateway/publish.go:131` and
  `publishing/automation.go:32` (the workflow engine, via
  `workflows/publish.go:172`). Nothing in `publishing/` imports the gateway.
- **Idempotency is path-independent, confirmed at the source, not assumed.**
  `requestHash` is `sha256(json.Marshal(Request))` with `IdempotencyKey` marked
  `json:"-"`; the operation key is `clientKey+":"+destination.channel_id`. A
  stored attempt survives the move unchanged.
- **Deleting the route arm does not produce a 404.** `POST /messages` would fall
  to the GET-only gate at `gateway.go:396` and answer `405` with `Allow: GET`.
  The plan places a `404 not_found` arm before that gate naming `/messages/copy`
  — the shape `gateway.go:462-468` already uses for a misspelled resource. The
  brief's option B (405 + `Allow: POST`) is unimplementable: RFC 9110 §15.5.6
  requires `Allow` to list methods the resource supports.
- **Two gaps the brief missed.** `docs/api/examples/README.md:27` names the old
  route and is enforced by `TestIndexCoversEveryRoute` plus
  `TestExamplesAreValidAndIndexed`. And `componentOf`
  (`internal/docsgen/render.go:297`) picks the `publishing` paragraph by
  `HasSuffix(pattern, "/messages")`, which also wrongly gives it to the read
  route `GET .../channels/{channel_id}/messages`; the predicate must become
  `"/messages/copy"`, which fixes both.
- **`publish_media_too_large` is live on this route**, contrary to the brief —
  `publishing/send.go:59-62` and `publishing/errors.go:192-193`. Also confirmed
  there is no `publish_source_missing`; the values are `publish_source_unavailable`
  and `publish_source_forbidden`.
- **Graph was 447 files stale.** `graphify-out/` had been built at `45db19d`,
  missing the entire v0.0.3 automation series. Rebuilt code-only to `887910f`
  (8803 nodes / 36886 edges / 236 communities) and reclustered. A full
  `--update` is blocked in this sandbox for want of an LLM API key, so 212
  doc/paper files keep stale semantic edges.

**Verified:** baseline before any edit — `go test ./...` 30 packages ok,
`docsgen --check` up to date, `gofmt -l .` empty, `go vet ./...` clean. Red
captured afterwards: 29 failing in `internal/gateway` (7 new + 22 retargeted),
every one at `405`/`404`/`200` exactly as recorded in handoff §F. All 14
`TestWordPressPublish*` and all `TestBackgroundPublishing*` green and unedited.
**Not verified:** live Telegram behavior; no handshake against a real client was
performed.

## 2026-11-09 — `POST /messages` is now `/messages/copy`; the old path is gone

Implemented on the user's instruction, after the prepared handoff
(`.agents/handoffs/telegram-messages-copy-route.md`, now `DONE`).

- **The route moved.** `POST /api/v1/telegram/{key}/messages/copy` does exactly
  what `POST /api/v1/telegram/{key}/messages` did. The handler is
  `Server.copyTelegramMessage`; its body is unchanged (permission → decode →
  `Validate` → `Idempotency-Key` → `PublishTelegram` → `Idempotent-Replay`).
- **The old address answers 404 on every method**, naming the copy route —
  the same shape `gateway.go` already uses for a misspelled resource. No alias,
  no redirect, no shim. The arm sits before the GET-only gate: without that,
  `POST /messages` would have answered `405` with `Allow: GET`, claiming GET
  works at a path where GET also 404s.
- **`GET /messages/copy` is `405` with `Allow: POST`** — the route's own method
  rule, as before.
- **`componentOf` was wrong twice.** `HasSuffix(pattern, "/messages")` gave the
  `publishing` paragraph to the read-only listing
  `GET .../channels/{channel_id}/messages` as well as to the publish route, and
  would have given the new `/messages/copy` neither. The predicate is now
  `"/messages/copy"`. Docsgen output confirms both corrections: the copy page
  carries `telegram` + `publishing`, the read page carries `telegram` alone.
- **Idempotency is untouched and proven, not assumed.** `requestHash` is
  `sha256(json.Marshal(Request))` with `IdempotencyKey` as `json:"-"`, and the
  operation key is `clientKey+":"+destination.channel_id`. `TestIdempotencySurvivesRouteRename`
  asserts `Idempotent-Replay: true` and `sendCalls == 1` across the new path.
- Panel path updated in `publish-dialog.tsx` and the bundle rebuilt
  (`channel-messages-main-BLaX3XW9.js` → `channel-messages-main-D_qgOEFd.js`).
  Hand-written docs updated: `docs/api/examples/{api.http,README.md}`,
  `docs/api/manual/components/publishing.md`, `PROJECT_BRIEF.md`,
  `docs/VERIFYING-MEDIA-PUBLISH.md`.

**One test bit back and was right.** `TestLinkedRoutesExist` failed on the first
`publishing.md` wording — `manual/components/publishing.md: /api/v1/telegram/{key}/messages
matches no route` — because the prose named the retired path in a code span,
which that test reads as a claim that a route exists. Reworded to describe the
behaviour instead of the path. The test was not weakened; no route was added to
`table.go` to satisfy it.

**⚠ Concurrent, unrelated work in the same tree.** A voice-message feature
appeared mid-implementation — `publishing/send.go` (`sendKind`, `Duration`,
`Waveform`), `rpc/rpc.go`, `worker/{send,worker}.go`, `tgapi/{send,tgapi}.go`,
`gotdclient/{channel,dialogs,send,updates}.go` and four new `voice_test.go`
files (~236 insertions over 11 files). It is **not** this change and does not
overlap its source files, but it does edit the package this handoff forbade
touching — through no act of this implementation, whose diff there is empty.
Nothing is committed; HEAD is still `887910f`. Separate the two diffs before
committing.

**Verified:** `gofmt -l .`, `go vet ./...`, `go build ./...`, `go test ./...`
(30 packages, with the voice work present), `docsgen --check` (84 pages,
unresolved items still 23), `go test ./internal/apidocs`, `npm run typecheck`,
`make web-test` (48 tests), `make panel`. All 14 `TestWordPressPublish*` and all
`TestBackgroundPublishing*` green and unedited by this change.
**Not verified:** live Telegram — no handshake against a real client; the codes
`publish_destination_unavailable` and `publish_media_too_large` are unchanged by
construction (their producing code is outside this diff) rather than re-proven
by a test.

## 2026-11-09 — Voice messages stop being published as documents

- **Bug.** A real Telegram voice message republished to another channel arrived
  as a generic downloadable file named «پیام صوتی» instead of a playable voice
  message. Bytes crossed intact; only the media semantics were lost.
- **Where it was lost** — a chain, not one point. Source classification was
  already correct (`gotdclient.documentType` reads `DocumentAttributeAudio.Voice`
  and returns `"voice"`). It died in `publishing/buildSend`, which set
  `kind := "document"` for anything that was not a photo and named the outgoing
  attachment from `Label` (hence «پیام صوتی» as a *filename*). And the
  destination had no way to hear about it either way: `gotdclient/send.go` built
  `inputMediaUploadedDocument` with one attribute, `documentAttributeFilename`,
  and never wrote `documentAttributeAudio` at all.
- **Fix.** `Duration` + `Waveform` added to `tgapi.Media`, `rpc.Media`,
  `tgapi.SendMedia`, `rpc.SendMedia`, and `SendMediaAudio`/`SendMediaVoice`
  kinds. New `gotdclient.MediaDuration`/`MediaWaveform` read the source
  attribute and are wired through `normalizeMessage`, the `updates.go` DTO and
  `worker.messageDTO`. New `publishing.sendKind` maps the *source* type
  (photo/voice/audio pass through; everything else stays `document`, unchanged).
  New `gotdclient.sendMediaInput` writes
  `documentAttributeAudio{voice, duration, waveform}` for voice and audio with
  `ForceFile` **false**, and gives a recording a filename attribute only when
  the sender actually named it. Music stays audio, uploads stay documents,
  photos stay photos; video/sticker/gif shaping untouched; nothing re-encoded;
  no schema change.
- **TDD.** Tests written first. Red captured in `internal/automation/publishing`:
  `kind = "document", want voice` across seven cases (manual, workflow,
  cross-connection, no-downgrade, media-kind table). The `gotdclient`/`worker`
  contract tests could not compile at red time because `Duration`/`Waveform` did
  not exist yet — recorded as such rather than implied to have been behavioural.
- **Verified.** `gofmt -l internal/ cmd/` empty, `go vet ./...` clean,
  `go build ./...` ok, `go test ./...` all packages ok,
  `docsgen --check` up to date, `go test ./internal/apidocs` ok, `web`
  typecheck + build ok. The outgoing MTProto request is decoded from a fake
  MTProto server (`tgtest`) and its `Voice`/`Duration`/`Waveform`/`ForceFile`
  asserted — not a filename check.
- **Not verified.** Live Telegram: no MTProto credentials here, so nothing was
  sent to a real channel and the play button was never seen. `cat` shares the
  one `gotdclient.SendMessage` path with the official profile and is sent the
  same attribute; whether a backend honours it is a destination property. A
  refusal fails with `publish_unsupported` and is never retried as a document.
- **Docs.** `docs/api/manual/components/telegram.md` gains `duration` and
  `waveform` (base64 in JSON) and a note that `type` comes from the source
  attributes alone. Mirrored via `go run ./cmd/docsgen`.
- **Record.** `.agents/handoffs/telegram-voice-message-publishing.md` (DONE).
- **Coordination.** A peer session moved the publish route to
  `POST /messages/copy` in this same tree while this ran; untouched here. The
  new `internal/gateway/publish_voice_test.go` resolves the live publish path at
  run time so it does not depend on which address wins.

## 2026-11-09 — v0.0.4 direct asynchronous sending

`POST /api/v1/telegram/{key}/channels/{channel_id}/messages/send` sends a
message a caller composed and one file they uploaded, with **no source message**
behind it. It is accepted and delivered in the background: the answer is
`202 Accepted` with an operation id and a `Location`, and
`GET .../messages/send/{operation_id}` reports the outcome. Nothing about the
copy route changed.

**What was added.** `internal/staging` (durable attachment staging: streamed,
hashed, 0700/0600, server-owned names, crash reconcile),
`internal/channelstore/send_operations.go` (migration 17, `message_send_operations`),
`internal/sending` (the asynchronous pipeline: accept, execute, status, cleanup),
`internal/automation/publishing/direct.go` (the shared outgoing sender),
`internal/gateway/send.go` (the two routes). Migration 18 rebuilds
`automation_jobs` so one queue carries a direct send beside webhook deliveries
and workflow runs; the automation-event reference is now a trigger that applies
to the automation kinds only.

**Voice is a voice message.** `gotdclient.sendInputMedia` gives each kind its
own MTProto construction. A recording goes out as a document whose
`documentAttributeAudio` carries `voice=true`, `duration` and `waveform`, and
**no filename attribute** — a name is what turns a recording into a file a
client offers to download. Music stays `audio` with its name; a photo is a
photo; a video carries `documentAttributeVideo`; a document keeps the sender's
file name. A kind that cannot be represented is refused, never downgraded.

**`ForceFile` corrected mid-task.** The first version set `ForceFile: true` for
voice and audio; that is backwards, and the effect is the downloadable-document
bug this work exists to prevent. gotd's `telegram/message` settles it:
`File(file)` is the only helper that sets `ForceFile(true)` and its comment says
"plain file, **not media**", while `Voice(file)` never touches it and adds no
filename attribute. The construction now matches that reference shape and is
pinned against `message.DefaultVoiceMIME` and the encoded flag bits.

**Duplicate sends.** One MTProto `random_id` is issued per operation, written
before the first attempt and reused by every retry. Idempotency is scoped to the
account exactly as `publish_operations` scopes it, and the request fingerprint
covers the destination, text, entities, options, media kind and the **digest of
the attachment's bytes** — never its file name. A failure nobody confirmed is
`unknown`, terminal, and never retried.

**Verified:** `gofmt -l .`, `go vet ./...`, `go test ./... -count=1` (all
packages), `go build -o ./bin/telesync ./cmd/telesync`, `go run ./cmd/docsgen
--check`, `go test ./internal/apidocs`. 106 new tests across `internal/staging`
(13), `internal/sending` (26), `internal/channelstore` (15), `internal/gateway`
(25), `internal/automation/publishing` (12), `internal/gotdclient` (7),
`internal/appconfig` (7) and `cmd/telesync` (1).

**Not verified:** anything against live Telegram or the `cat` backend — this
checkout has no credentials and none may be invented. Live delivery, a real
flood wait, a real restart mid-send and the panel in a browser are unexercised.
The source-based publisher's own voice handling is unchanged here: `sendKind`
still maps a source `voice` attachment to `document`, which is a separate piece
of work.
