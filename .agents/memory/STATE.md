# Current state

**Latest update — v0.0.4 release preparation (2026-11-12):** Prepared and
tagged v0.0.4. The version in `web/apps/web/package.json` is `0.0.4`, the README
describes the release, and the tag `v0.0.4` is on the release commit.

Two panel tests (`.agents/handoffs/v0.0.4-floating-message-composer-layout.md`,
approved and DONE 2026-11-11) were failing at `5dad14c`: that commit landed an
older copy of `message-item.tsx` (`absolute -bottom-4 -right-8`) and
`channel-messages-pane.tsx` (no `bg-background/80 backdrop-blur-xl`) together
with the newer `chat-layout.test.tsx`. The approved classes
(`absolute bottom-1 right-1`, and the translucent blurred footer) were restored;
no test was changed. Panel suite 164/164.

**Verified:** `gofmt -l` (only the untracked nested `.claude/worktrees` checkout),
`go vet ./...`, `go test ./... -count=1` (all packages ok), `go build`,
docsgen `--check` (88 unchanged, 0 stale), web typecheck, web build, and all
eight web suites (26, 19, 164, 108, 50, 202, 18, 48) green. **Not verified:**
anything against live Telegram, WordPress, an AI provider or an external
webhook receiver; the panel in a browser.

**Latest update — floating chat footer and message actions (2026-11-11):**
Implemented approved handoff
`.agents/handoffs/v0.0.4-floating-message-composer-layout.md`. Message actions
are positioned at the physical bottom-right inside their own bubble with reserved
content/reaction space. The existing composer now floats inside the message pane
with `bg-background/80 backdrop-blur-xl`; a ResizeObserver measures its full
height and updates timeline clearance and Jump-to-latest offset. Panel tests,
`npm test`, typecheck, production build, and `git diff --check` passed. Browser
verification was unavailable (no browser executable/Playwright command found).

**Latest update — native media semantics and album publishing (2026-11-11):**
Implemented source-native video and round-video classification/metadata through
gotd, worker DTO, RPC and publishing; added destination video attributes and
native `sendMultiMedia` with ordered upload/preparation and no standalone
fallback. RPC/worker forwarding, publishing source-member validation/send,
workflow album-scoped idempotency, and Channel Explorer album grouping/gallery
and publish payload are wired. `go test ./... -count=1`, `go vet ./...`,
`go build ./...`, docsgen check/apidocs tests, web typecheck and `git diff
--check` pass. Panel tests are 163/164; one existing floating-footer class
assertion fails against current working-tree markup. Remaining gaps: Direct Send
durable multi-file operation and Workflow's event-to-album aggregation into one
publication; API cannot verify that supplied source member IDs are the complete
group, and the gallery action currently relies on complete loaded history. Live
Telegram/Cat behavior remains unverified. Approved handoff is still implementing;
preserve all pre-existing dirty worktree edits.

**Latest update — floating message composer layout preparation (2026-11-11):**
Added `web/apps/web/app/components/channel-explorer/chat-layout.test.tsx` and
registered it in the panel test runner. The two new SSR assertions fail against
the current top-left menu and normal-flow composer layout as expected; 158 other
panel tests passed. No production changes made. Handoff
`.agents/handoffs/v0.0.4-floating-message-composer-layout.md` is awaiting
approval. Browser verification not run.

**Latest update — shared composer edit mode (2026-11-11):** Implemented the
approved `.agents/handoffs/v0.0.4-shared-chat-composer-edit-mode.md`. Send and
Edit now use one `ChannelMessageComposer`, one textarea, the existing attachment
Sheet, and a single mode-dependent primary action. Removed the separate
`ChannelMessageEdit` form/component and obsolete tests. Edit drafts are keyed by
message ID; Send draft remains in the shared component while editing. The pane
retains existing PUT and async polling, retries history refresh after confirmed
`edited`, and updates the existing message ID. `npm test`, `npm run typecheck`,
`npm run build`, focused panel tests, and `git diff --check` passed. Browser and
live Telegram verification were not run; media transitions remain unverified
against a live destination. See the handoff for exact results.

**Latest update — message composer/send UX (2026-11-11):** The channel composer
now uses a compact RTL messenger layout with one Paperclip-triggered bottom
sheet, five direct media actions, kind-aware previews, and Ogg/Opus validation
for voice. Accepted sends are represented by content-free timeline placeholders,
polled by operation identity, and reconciled against fetched history with bounded
refreshes; failed drafts can be restored without automatic resend. Focused panel
tests (161), full web tests, typecheck, production build, and `git diff --check`
pass. No browser interaction or live Telegram send was performed. Details and
remaining sync caveat are in `.agents/handoffs/v0.0.4-message-composer-sending-ux.md`.

**Updated:** 2026-11-11 (Create Channel now opens as an explorer Dialog instead
of a dedicated page; the legacy URL redirects into the open dialog while
preserving query filters. The form has a hidden-input circular avatar picker,
public/private radio tiles, inline username feedback, and tested multipart/API
integration. Web typecheck and production build pass; panel tests pass 151/151.
No browser viewport verification was available. Native channel creation/availability/explorer UX is
complete; see `.agents/handoffs/v0.0.4-native-channel-creation-availability-explorer.md`.
Public create and username-advisory routes, worker/gotd pipeline, bounded
multipart avatar path, durable idempotency/partial registration, generated docs,
and explorer form/menu/detail UI are implemented. `go test ./...`, `go vet
./...`, `go build ./...`, docsgen/apidocs checks, web typecheck, 138 panel tests,
and web production build pass. Browser and live Telegram/Cat verification were
not performed. Channel message management is complete in
`.agents/handoffs/v0.0.4-channel-message-management-ui.md`: UI/API operations,
fail-closed permissions, identity-safe polling/reconciliation, and explicit
`keep_media` edit semantics are in place. Typecheck/build, focused feature Go
tests and docs checks pass. Full Go tests and frontend `npm test` (609) pass. No
browser or live Telegram/Cat verification was performed. Graphify stats/currentness could not
be verified.

The Channel Information Dialog profile redesign is implemented and DONE in
`.agents/handoffs/v0.0.4-channel-information-dialog-profile.md`. It uses the
authenticated local-avatar path, distinguishes local route/avatar ID from
remote Telegram detail/copy ID, gates View on actual read permission, and keeps
stale request identity isolated. `npm test` (626), forced frontend typecheck,
production build, and `git diff --check` pass. Public link/server metadata were
omitted because the detail response provides no authoritative safe values.
Browser verification was not performed. Graphify freshness remains unknown.

Permission-resolution preparation is recorded in
`.agents/handoffs/v0.0.4-telegram-channel-permission-resolution.md` (DONE after
explicit approval). The gateway now preserves known Telegram capabilities from
the worker through created-channel persistence, response, replay, and local
detail; focused and full Go tests plus vet/build pass. Live selected-session
identity and channel 98 ownership remain unverified.

Live Local Channel Detail permission refresh is implemented in
`.agents/handoffs/v0.0.4-live-local-channel-permissions.md` (DONE). Detail calls
the local row's exact keyed worker with its stored remote ID, overlays only live
permissions, and returns the classified worker error on failed resolution. The
gotd resolver no longer accepts an incomplete reconstructed dialog as current
rights. Focused Go tests, vet, docs checks, and a separate executable build pass.
DB row 2 is `cat` / remote 98 with an unknown all-false snapshot, but the
authenticated live API and MTProto role remain unverified; running PID 53744
uses the old `bin/telesync` and was not restarted.

Channel Explorer's Persian destination/permissions warning was caused by
missing destination identity, not permission parsing: the child route reads
`data.client_key`, while local detail returned only `data.channel`. Local detail
now adds the stored row's key as sibling `data.client_key`, and the pane labels
missing identity separately from send denial. The exact route and UI regressions,
157 panel tests, typecheck/build, gateway tests, vet, docs checks and a separate
binary build pass. Handoff
`.agents/handoffs/v0.0.4-channel-explorer-destination-key.md` is DONE. Live UI
remains unverified: PID 58331 serves old embedded bundle
`channel-messages-main-DF75S9b3.js` (still with old warning); current source
build produces `channel-messages-main-C886CuXI.js`. API auth returned 401 and
deep-link HTTP returned 404. No embed, restart, or deployment was done.
Earlier state: asynchronous full Telegram message replacement is
implemented and recorded DONE in
`.agents/handoffs/v0.0.4-telegram-message-edit.md`. `go test ./...`,
`go build ./...`, `go vet ./...`, docs generation checks, and API docs tests
pass. The new route uses a durable edit operation, channel `ActionUpdate`, the
selected worker's native `messages.editMessage`, same-ID readback, and read-only
status polling. Live Telegram/backend behavior remains unverified. The separate
approved `.agents/handoffs/v0.0.4-channel-message-delete.md` remains in progress
in the shared worktree; no live Telegram/Cat deletion was attempted. A
repository-wide `gofmt -l .` reports one unrelated nested checkout file,
`.claude/worktrees/full-message-replacement-api-b6e9bd/internal/rpc/rpc.go`;
feature-owned Go packages and changed files are formatted. Earlier that day,
the deletion preparation recorded its route/permission/RPC discovery and red
tests. The
2026-11-09 baseline state follows: three pieces of work landed together. v0.0.4
direct asynchronous sending:
`POST /api/v1/telegram/{key}/channels/{channel_id}/messages/send`
composes and sends a message with an uploaded attachment, with no source message
behind it, and is delivered by the durable queue after the `202` — see the
section below. `POST /api/v1/telegram/{key}/messages` is now `/messages/copy`,
since `publishing.Validate` refuses any request without a positive source and
that route only ever copies a message. A recorded voice message is republished
as a voice message: its kind comes from the source document's
`DocumentAttributeAudio.Voice` and from nothing else. On 2026-11-08
`telesync serve-webhook` adds a local receiver for the webhook delivery engine — see the last section. Webhook and workflow create/edit are dedicated
full-page forms at `/automation/{webhooks,workflows}/{new,:id/edit}`, with one
shared shell, a connection-first source picker and an accordion for the
webhook's advanced tuning. A shipped-bundle crash — `isCreation is not defined`,
caused by a route-module helper crossing a chunk boundary — is fixed and a
chunk scanner now guards it. On 2026-11-07 the automation overview at
`/automation` became the management screen — fit-content tabs and a search
field over the open collection's full paginated table; queue statistics and
recent failures are gone from the UI and the page no longer reads
`/local/automation/status`. On 2026-11-06 v0.0.3 was integrated and verified end to end: the automation UI is
reachable in the served panel, the MTProto listener feeds the durable event
store through a private ingest socket, and background jobs run after an
observed message; the panel bundle must be rebuilt — `node
web/scripts/build-panel.mjs` — before any binary serves it, and that still
holds for this change).

## Milestone

v0.0.4 is released (2026-11-12): direct asynchronous message sending, the
Channel Explorer with native message edit/delete, native media semantics and
album publishing, the copy-route rename, and voice messages that stay voice
messages. v0.0.3 is released (2026-11-09): the automation feature, the
webhook/workflow page forms, the loopback exception, the channel labels in the
webhook payload, `telesync serve-webhook`, and the release fixes in the LOG.
`internal/worker`'s two socket tests now pass as well — one of them had been
hiding a real defect, `worker.Listen` stealing a live socket from another
worker.

Telesync is a multi-client MTProto runtime with a supervised worker per client,
JSON HTTP API v1, SQLite channel/category/user/site inventory, permissions, and
an embedded `/admin` panel. The product path is acquire → manage → read → select
→ prepare → publish. v0.0.3 adds react: incoming channel messages become
durable events that drive webhooks and per-channel workflows in the background.
Telegram and WordPress publishing is implemented, but not live-verified; the
exact product matrix remains in `PROJECT_BRIEF.md` §9.

## v0.0.4 direct asynchronous sending (2026-11-09)

`POST /api/v1/telegram/{key}/channels/{channel_id}/messages/send` sends a
message a caller composed and one file they uploaded. There is **no source
message** — no `source.local_channel_id`, no `source.message_id`, no
`media.mode` — and nothing is read back from Telegram to build the post. It is
the other way to produce an outgoing message and it goes out through the same
door as a publish: `publishing.SendDirect` shares the destination resolution,
the permission rule, the message construction and the classification of a
failure with `PublishTelegram`.

**It is asynchronous, and both halves are.** A text-only request is not sent
synchronously while a file waits in the background. The request stages any
attachment, records the operation, enqueues a job on the durable queue and
answers `202 Accepted` with an opaque `operation_id` and a `Location`.
`GET .../messages/send/{operation_id}` reports `queued`, `processing`,
`retrying`, `sent`, `failed` or `unknown`, and reading it never sends anything.

**What was added.** `internal/staging` (durable attachment staging),
`internal/channelstore/send_operations.go` (migration 17,
`message_send_operations`), `internal/sending` (accept / execute / status /
cleanup), `internal/automation/publishing/direct.go` (the shared outgoing
sender), `internal/gateway/send.go` (the two routes). Migration **18** rebuilds
`automation_jobs` so one queue carries `telegram_message_send` beside webhook
deliveries and workflow runs — its `event_id` names a send operation for that
kind and an automation event for the other two, and the reference is enforced by
trigger because a column-level foreign key cannot tell one job kind from
another.

**Voice is a voice message.** `gotdclient.sendInputMedia` builds each kind on
its own. A recording goes out as a document whose `documentAttributeAudio`
carries `voice=true`, its duration and its waveform, and **no filename
attribute** — a filename is what turns a recording into a file a client offers
to download instead of play. Music stays `audio` with its name; a photo is a
`InputMediaUploadedPhoto`; a video carries `documentAttributeVideo`; a document
keeps the sender's file name. A kind that cannot be represented is refused with
`tgapi.ErrSendUnsupported`, never silently downgraded.

**A filename on a recording — one rule per path, deliberately.** `sendMediaInput`
adds a `documentAttributeFilename` when the caller gives a name, because a source
document may genuinely have had one. The direct-send path sends a recording
**unnamed** regardless: the multipart filename is a file on the caller's disk and
not a property of the recording, so it is kept as the operation's metadata and
never put on the wire. `TestL` pins that. The two rules coexist because the two
paths answer different questions; neither was overwritten during the merge.

**`ForceFile` must be false for voice and audio — corrected here.** The first
version of this code set `ForceFile: true` on both, reasoning that a recording
should not be reinterpreted. That reasoning was backwards and the effect was
exactly the bug this work exists to avoid. gotd's own source settles it:
`telegram/message`'s `File(file)` is the **only** helper that sets
`ForceFile(true)`, and its comment says why — "forces it to be used as plain
file, **not media**". Its `Voice(file)` is `UploadedDocument(file).MIME(DefaultVoiceMIME).Audio().Voice()`,
which never touches `ForceFile` and never adds a filename attribute. The raw
construction now matches that reference shape, and `TestB` pins
`ForceFile == false`, `MimeType == message.DefaultVoiceMIME` and an attribute
list of exactly one element. `TestH` checks the wire form too: `SetFlags()`
puts voice on bit 10 and a non-nil waveform on bit 2, so the flag survives
encoding. A test written from the same wrong assumption as the code had been
asserting the bug; anchoring on gotd's builder is what caught it.

**Nothing is trusted from the client.** The content type is sniffed from the
first 512 bytes and what they say wins whenever they say something specific; a
PDF offered as `photo` is `invalid_media_kind`. The file name is metadata and
never a path. The staging directory is `0700`, its files `0600`, and every
storage name is 32 hex characters this server chose — a client name never
resolves anything, and a planted symlink is not a staged file.

**Duplicate sends.** One MTProto `random_id` is issued per operation, persisted
before the first attempt and reused by every retry — random per operation and
never derived from the request, because two identical sends are two messages.
Idempotency is scoped to `(user_id, idempotency_key)` exactly as
`publish_operations` scopes it, and the fingerprint covers the destination,
text, entities, options, media kind and the **digest of the attachment's bytes**
— never its file name. An outcome nobody confirmed is `unknown`, terminal, and
never retried: the message may already be there.

**Crash consistency.** An attachment is written to `<name>.part`, flushed, and
renamed, so a name that exists is a whole file. At start-up the sweep deletes
what no operation claims, stops an operation whose attachment is gone — as a
failure, never as a delivery — and removes abandoned partials. An attachment is
removed once its send has finished one way or the other, and retention deletes
finished operations after `send.history_retention_days`.

**Configuration** is the `send` block, overridable by `TELESYNC_SEND_ENABLED`,
`TELESYNC_SEND_STAGING_DIR`, `TELESYNC_SEND_MAX_FILE_BYTES` and
`TELESYNC_SEND_HISTORY_RETENTION_DAYS`. Workers, retry policy and lease are
deliberately **not** duplicated: they are the durable queue's, shared with
webhooks and workflows.

**Verified:** `gofmt -l .`, `go vet ./...`, `go test ./... -count=1` (all
packages), `go build -o ./bin/telesync ./cmd/telesync`, `go run ./cmd/docsgen
--check`, `go test ./internal/apidocs`. 106 new tests: `internal/staging` (13),
`internal/sending` (26), `internal/channelstore` (15), `internal/gateway` (25),
`internal/automation/publishing` (12), `internal/gotdclient` (7),
`internal/appconfig` (7), `cmd/telesync` (1). The existing copy route,
`TestWordPressPublish*` and `TestBackgroundPublishing*` are green and unedited.

**Not verified:** anything against live Telegram or the `cat` backend — this
checkout has no credentials and none may be invented. Live delivery, a real
flood wait, a restart mid-send, and the panel in a browser are unexercised.

**Merged with the other two pieces of work.** The source-based publisher's own
voice handling landed at the same time (see the voice section below): it fixes
`publishing.sendKind`, which this entry left mapping a source `voice`
attachment to `document`. Both paths now send a recording as a recording, by
different rules on purpose — see the note on filenames.

## Topology and ownership

`cmd/telesync` wires `runtime`; the daemon owns gateway, supervisor, SQLite,
auth, avatar cache, docs and panel; each worker owns one gotd client, session,
peer store and Unix socket. `gateway` calls `rpc`, never gotd. `gotdclient` is
the only gotd importer. `web/` is the frontend source and `internal/panel` its
embedded build.

Important boundaries: official and custom clients have separate credentials,
sessions, DC configuration and keys; custom routing never falls back to official
Telegram or server-reported addresses. Messages are read live, not stored.
Local permission ids are `managed_channels.id`, not Telegram peer ids.

## Verified baseline

- `gofmt`, `go vet`, `go test ./...`, `go build ./...`, docs generation/checks,
  and panel typecheck/build were green in the latest recorded verification.
- The in-memory runtime test reaches gateway → worker RPC → real MTProto
  handshake over `net.Pipe`; socket tests may skip because this sandbox denies
  binding. Check the latest log before repeating claims.
- Authorization is deny-by-default with explicit deny precedence; reads do not
  write; cross-system operations report partial failure.

## Changed since the last snapshot

- **The webhook payload names the channel (2026-11-09).** `data.source` carried
  only identifiers; it now also carries `type`, `title` and `username`, so a
  receiver can say which channel a message is from without calling back. They
  are labels and never keys — the id fields are what to match on. Read through a
  `ChannelSource` seam beside `EventSource`, resolved at delivery time; a
  channel row that is gone leaves them empty and the delivery still goes.
- **The loopback exception is one knob with two homes (2026-11-09).**
  `automation.webhooks.allow_localhost` in the configuration file, overridden by
  `TELESYNC_WEBHOOK_ALLOW_LOCALHOST` whenever the environment says anything.
  The file had no field and the loader refuses unknown keys, so setting it there
  was a startup failure. `urlErrorOf`'s `allowLocalhost` argument is now
  required: the form's action dropped it and refused every loopback address at
  submit whatever the switch showed.

- **Webhooks may reach localhost on two keys (2026-11-08).** The SSRF rules
  refused loopback outright and the URL validator required https everywhere, so
  a receiver on the same machine could not be used. Now
  `TELESYNC_WEBHOOK_ALLOW_LOCALHOST` (off by default) and a webhook's own
  `allow_localhost` (off by default, migration 16) together permit
  `http://localhost`, `http://127.0.0.1` and `http://[::1]` — and nothing else.
  The exception is a literal list, not a range: `127.1`, `0x7f000001`,
  `::ffff:127.0.0.1` and any name containing "localhost" are refused, a loopback
  target must resolve to loopback only (no DNS rebinding), redirects are still
  unfollowed and no proxy is consulted. A policy refusal is `invalid_target` and
  is not retried; a connection failure keeps the ordinary retry policy. Two real
  bugs were caught by the tests: an unrelated update reset the setting (the
  update seed did not carry it), and `NewClient`'s nil test options wiped the
  production transport and dialler, so the first real delivery panicked.
  **Verified** with real `httptest` servers and the production client, plus
  `gofmt`, `go vet`, `go build`, `go test`, frontend `npm test` (678) and `tsc`,
  and `docsgen --check`. **Not verified:** a receiver outside this machine.
  **Note:** `internal/webhooktest/` was untracked and incomplete mid-task; it
  is finished, tested and tracked in v0.0.3.
- **AI polishing reported success and returned the source unchanged
  (2026-11-08, corrected).** The first fix did not work. Calling the real
  provider settled it: the answer was not the original — it differed in two
  characters, two spaces becoming Persian ZWNJ — so the model was only
  normalising orthography. **Two causes.** (1) `DefaultPrompt` told it to: it
  opened "Your only task is to edit one source message" and led the editing
  rules with "Improve spelling, punctuation, readability, and formatting",
  which is exactly what it delivered, while the operator's instruction sat
  behind a "supplementary" header at the end of 2800 characters. A minimal
  prompt with the same source produced a warm 468-character rewrite, so the
  model was capable and ours suppressed it. (2) The bounded retry compared
  byte-for-byte and a normalised answer is never byte-identical, so it never
  fired. **Fixed:** `DefaultPrompt` now opens with HOW MUCH TO CHANGE (operator
  instructions are the brief; without them it is a copy edit), the header calls
  them THE BRIEF, `sameEdited` compares after collapsing ZWNJ and whitespace,
  `finish_reason == "length"` is refused as `ai_invalid_output`, and a workflow
  whose operator prompt is still unmet after the retry fails with
  `ai_unchanged` so the fallback policy decides. **Verified** against the real
  provider (two calls, both genuinely rewritten) and by `go test ./...`, the
  frontend suite and `tsc`. `bin/telesync` is rebuilt; the daemon needs a
  restart. **Not verified:** a live Workflow run to a real destination.
- **`publish_forbidden` on every background publication (2026-11-08).** A
  workflow run reported `publish_forbidden` — "This deployment cannot resolve
  the account a background publication runs under." That is
  `publishing.actorFor`'s `Actors == nil` branch: `cmd/telesync/runtime.go`
  built its `publishing.Service` without the `ActorResolver`, and only the
  background engine reads one (the interactive route passes its own actor).
  A publication is recorded against the account it runs under, so the account
  and its permissions must be resolvable again at execution time — without a
  resolver every background publish was refused before anything was sent.
  **Fixed** by `newBackgroundPublisher` setting `Actors: store`; pinned by
  `cmd/telesync/runtime_test.go`, which fails without the field and passes with
  it. **Verified:** `gofmt -l`, `go vet ./...`, `go build ./...`,
  `go test ./...` (28 packages). **Not verified:** against a live Telegram
  account or WordPress site.
- **The forms are pages (2026-11-08).** Webhook and workflow create/edit are
  dedicated full-page forms at `/automation/webhooks/{new,:id/edit}` and
  `/automation/workflows/{new,:id/edit}`, one form component per collection
  behind one shared shell. `webhook-dialog.tsx`, `automation-webhooks.tsx`,
  `automation-workflows.tsx` and `automation-workflow-editor.tsx` are gone; the
  old bare addresses redirect and an id that was never a record is reported.
  Advanced settings live in a Base UI accordion (shadcn names) that is shut
  until opened and kept mounted; source selection is connection first, channel
  second, filtered off the managed row's `client_key`; both forms are
  `max-w-4xl` with Save at the outermost physical left.
  **Why a green build was not enough:** `isCreation is not defined` in the
  browser came from a route module exporting a helper that Vite's per-route
  split left out of the client-loader and client-action chunks. Module-private
  helpers fix it, and a chunk scanner now reports any identifier that is
  neither declared nor imported in the emitted chunks.
  **Verified** in a real browser (stub gateway) at 1440×900 and 390×844, and by
  `npx tsc --noEmit`, `npm test` (573), `npm run build`. **Not verified:**
  against a live daemon. The workflow form takes no accordion — it has no
  advanced-only settings.
- **The automation overview is the management screen (2026-11-07).** The page
  at `/automation` was a queue strip plus two five-row previews behind
  "همه …" buttons; the real tables lived elsewhere. It is now header (title,
  subtitle, one Add button that names the open tab and links to that
  collection's create route) → fit-content `TabsList` beside one search field →
  the open collection's full paginated table.
  **Gone from the UI, not from the system:** `وضعیت صف`, `شکست‌های اخیر`, both
  previews and both door buttons. The loader never calls
  `GET /local/automation/status` now — the queue, its API and its workers run
  unchanged. `readStatus`/`statusRecord`/`StatusCount` remain in the shared
  vocabulary (their own tests read them) but this page does not.
  **Reads one collection at a time.** Tab, `q` and `page` are separate facts in
  the address bar; each tab keeps its own query and page; a typed search is
  sent after 300 ms and starts at the first page; the closed collection is
  never fetched.
  **`WebhooksTable` and `WorkflowsTable` are reused as they are,** and the
  editor screens plus the standalone list routes are untouched — a follow-up
  may fold those list screens into the tabs.
  **Verified** in a real browser (stub gateway) at 1440×900 and 390×844, and by
  `npm test` (575), `npm run typecheck`, `npm run build`. **Not verified:**
  against a live daemon; and `internal/panel/files` was not rebuilt, so a
  binary built today still serves the old overview.
- **v0.0.3 integration (2026-11-06).** The release is now connected end to end.
  **Why the UI had been invisible:** `internal/panel/files` is embedded into the
  binary at build time and had been built before the automation UI existed, and
  the source is uncommitted in this worktree so `main` and every CI image lacked
  it. Rebuilt with `node web/scripts/build-panel.mjs`.
  **Listener:** `internal/profile/profile.go:180`'s no-op `UpdateHandler` is a
  seam now filled by gotd's `updates.Manager`; a worker sends one bounded
  observation per new channel message over a private `automation.sock` to the
  daemon, which persists it and fans out to the queue. gotd advances its
  counters only after the handler returns and the handler returns only after the
  event is committed, so state can never run ahead of the store.
  **Verified in a real browser** against a running daemon: sidebar entry,
  overview, webhook list, create form at `/automation/webhooks/new`, workflow
  list, and the mobile overflow with Automation active on nested routes.
  **Not verified:** no live Telegram, WordPress, AI provider or webhook
  receiver; no credentials in this checkout.

- **v0.0.3 background automation (2026-11-06).** A shared event ingestion path
  turns new channel messages into durable events that two independent consumers
  act on: webhooks (HTTP delivery with full request/response history) and
  workflows (automatic publishing through the v0.0.2 publisher, optionally
  polished by an OpenAI-compatible model first).
  **Backend:** migrations 8–15 add `automation_events` (+ sources),
  `automation_jobs`, `automation_checkpoints` / `automation_update_states` /
  `automation_channel_pts` / `automation_peer_hashes`, `webhooks` (+ sources),
  `webhook_deliveries` (+ attempts), `workflows` (+ destinations),
  `workflow_runs` (+ publications) and `automation_outbound_messages`.
  `internal/automation` holds the shared contracts and seven packages:
  `events`, `queue`, `recovery`, `webhooks`, `workflows`, `publishing`, `ai`.
  The v0.0.2 publishing pipeline moved into `internal/automation/publishing` so
  the route and a background job call the same code; `internal/gateway/publish.go`
  keeps only the HTTP shape.
  **Frontend:** an `اتوماسیون` area with two sections (webhooks, workflows),
  delivery history with request/response/attempt tabs, a workflow editor and a
  run history, all Persian RTL and permission-gated.
  **Verification:** `gofmt`, `go vet`, `go test ./...` (except
  `internal/worker`'s socket tests, which fail on this machine's Unix-socket
  path length and are untouched by this release), `go build ./...`,
  `docsgen --check`, `go test ./internal/apidocs`, `npm run typecheck` and
  `npm test` (426 assertions) were all run and green.
  **Not verified:** nothing was sent to Telegram, WordPress, an AI provider or a
  real webhook receiver; no browser session was available.

- Telegram-to-other-connection media publishing is fixed (2026-11-05, D66). Two
  independent defects presented as one symptom — the dialog said `دریافت عکس
  ناموفق بود` and the publish returned `publish_partial_failure` / "The source
  attachment could not be read".
  **Backend:** `gateway.buildSend` fetched the source attachment through the
  `worker` it was handed, which `serveTelegram` resolves from the URL path — the
  **destination** connection. Publishing to `cat` therefore asked `cat` to read
  a channel id that only exists in the source account's peer store
  (`worker.channelByID` → `rpc.ErrChannelNotFound`), and the gateway collapsed
  that into `publish_partial_failure`. `resolvePublishSource` already resolved
  the source worker to read the message and then threw it away; `sourceMessage`
  now carries it and both the Telegram and the WordPress route read through it
  (`publishWorkerFor`, which returned a nil `Worker` for an unknown key, is
  gone). Two stages now have two codes and are never interchangeable:
  `publish_source_media_unavailable` (502, nothing sent, retry or
  `media.mode:"omit"`) and `publish_media_upload_failed` (502, the attachment
  was read and the destination refused the post). `rpc.Err*` sentinels and bare
  `*rpc.Error` codes classify identically (`sendFailureOf`); an unclassified
  failure of a send that carried media is a destination failure, never a source
  one. A source read failure is logged with `client=`, `channel=`, `message=`.
  **Frontend:** the dialog rendered `MediaAttachmentCard` with `status="ready"
  url=""`, which the card draws as `دریافت {label} ناموفق بود` — no request was
  ever made. New `lib/media-blob-cache.ts` (one shared download and one
  refcounted object URL per attachment; a failure is never cached) and
  `components/publish/source-attachment-media.tsx` (fetches through the panel's
  bearer credential, shows loading/picture/gone/failed, and on failure offers
  `تلاش مجدد` and `حذف پیوست` while the attachment stays attached). The editor
  and the live preview share it. `lib/publish-errors.ts` maps API codes to
  Persian sentences, so the backend's English never reaches the interface, and
  the request id is shown per destination as `کد پیگیری`.
  **Verified here:** 14 new gateway tests red → green (the red reproduced the
  production error verbatim), `go test ./...` green apart from the two known
  `internal/worker` socket-bind failures, `gofmt`/`go vet`/`go build` clean,
  `docsgen --check` up to date after `publishing.md` gained a "Failure stages"
  section, `go test ./internal/apidocs` green, and the web suites 21+19+109+85+49
  = 283 green with `npm run typecheck` and `npm run build` clean.
  **Not verified:** anything against a live Telegram account or the `cat`
  backend — this checkout has no daemon configuration, no Telegram credentials
  and no custom-backend endpoint, and none may be invented. The publishing
  dialog was not opened in a browser. Real file-reference expiry, DC migration
  and a real cross-account upload remain exercised only through the fake
  workers.

- A media download is no longer cut off part-way (2026-11-05). Three defects
  stacked: `serveTelegram` gave every Telegram route — media included — a
  `RequestTimeout` context (30s default) that in Go governs the response **body**
  as well as the headers, so any download slower than 30s was aborted mid-stream;
  the daemon's `http.Server` `WriteTimeout: 120s` was never cleared for the
  streaming answer even though the worker clears its own
  (`rpc.writeMediaStream`, `SetWriteDeadline(time.Time{})`); and both `io.Copy`
  results were discarded, so the short transfer reached `curl` as
  `(18) transfer closed with N bytes remaining` with nothing logged.
  `gateway.media` now streams on a context that outlives the request budget but
  is still cancelled when the handler returns, clears the write deadline before
  the first byte, and reports any transfer that delivered fewer bytes than it
  promised through `Config.Logf`. `gotdclient.mediaReader` no longer calls a
  0-byte `upload.getFile` part a clean `io.EOF` when a size was reported.
  Verified: 5 new tests red → green, `go test ./...` green apart from two
  pre-existing `internal/worker` socket-path failures, `gofmt`/`go vet`/`go
  build` clean, `docsgen --check` up to date.

  **Live verification then named the real cause, and it was not the deadlines.**
  `upload.getFile` is served only in parts whose `limit` is a multiple of 4 KiB,
  and `mediaReader.Read` asked for "what is left" — so a download whose size was
  not a chunk multiple delivered **zero** bytes, and a larger one died on its
  tail (three whole 512 KiB parts, then a 184096-byte remainder that is not a
  multiple of 4096). `mediaReader.Read` now asks for a limit that is always a
  `chunkAlign` (4 KiB) multiple and never more than `mediaPart`, and drops the
  bytes a part carries past the slice. `precise` relaxes an arbitrary *offset*,
  not an unaligned *limit* — `mediaPart`'s comment claimed otherwise and is
  corrected. The `downloadServer` test double refuses an unaligned limit, which
  is what made this visible. **Verified**: five tests red (`LIMIT_INVALID`) →
  green, full suite unchanged otherwise. **Live-verified by the owner on
  2026-11-05**: rebuilt and reran the original failing download — complete,
  no truncation. Three further truncation producers in the gotd reconnect
  lifecycle are named in the handoff and deliberately not fixed — see
  `.agents/handoffs/media-download-cut-short.md` and D65.

- Panel responsiveness (2026-11-04, bottom nav hidden on the message timeline 2026-11-05): below `lg` (1024px) the shell has no
  sidebar, drawer, hamburger or header — only a floating icon-only
  `BottomNav` (Dashboard → Categories → Channels → WordPress → Administrators,
  permissions from `visibleNavItems`, active pill `bg-muted`). Dashboard stat
  cards are one 240px-wide horizontal scroller below `lg` and the 4-column grid
  at `lg`. Shared `Table` scrolls (`w-full min-w-max` in its one
  `overflow-x-auto` container); `TableActionsHead`/`TableActionsCell` pin the
  actions column to the physical LEFT. WordPress uses the same table at every
  width (mobile cards removed) with a full-width mobile search. Verified:
  `npx turbo typecheck` and `build`, `test-responsive.mjs` 30/30,
  `test-panel.mjs` 109/109, content/media/publish/administrators suites.
  Unverified: no browser render — this sandbox cannot launch Chromium
  (Mach-port rendezvous denied), so visual/RTL scroll behaviour is
  class-level only; `api.http`/live API for WordPress not exercised.

- Telegram attachments are now delivered properly end to end (2026-11-03/04).
  The API's `media` descriptor carries `filename`, the name Telegram reported
  from `documentAttributeFilename` — omitted when there is none, never invented
  from the label or the MIME type, and never used to replace `label`. The media
  route streams the bytes (no more 32 MiB cap, no JSON/base64 round trip),
  answers HTTP ranges with `206`/`416` and `Content-Range`, and always sends
  `Content-Disposition: attachment` with the sender's name encoded per RFC 5987.
  The panel shows a real `<audio>` player for audio and voice, and a document
  card with the filename, size, extension and a working download for documents.
  Decision D63; gotd facts in `GOTD_NOTES.md`; contract in
  `docs/api/manual/components/telegram.md`.
- A kind's manage permission now allows `update`/`delete`, narrowed to the
  account's selection (D62); `channelstore.Authorizer.allowedWrite` is the one
  rule and `gateway.visible()` routes every write through it. The administrators
  dialog stores a selection as `view: allow` per picked item plus `update: deny`
  with `delete: deny` per item left out, so a scoped manager cannot widen
  themselves by deleting a picked record. Documented in
  `docs/api/manual/components/authentication.md` and `users.md`.
- Verified here: the reported scenario through the permission API and the leave
  route (`TestManageChannelsRemovesARecordAndStaysInsideItsSelection`,
  `TestManageEditsAndDeletesWithinTheSelection`), `gofmt`, `go vet`,
  `go test ./...`, `go build ./...`, `docsgen --check`, `go test
  ./internal/apidocs`, `npm run typecheck` and the panel rebuild. Not verified
  against a running daemon or a browser.
- Verified here for the media work: `gofmt`, `go vet`, `go test ./...` (all 21
  packages), `go build ./...`, `go run ./cmd/docsgen --check`, `go test
  ./internal/apidocs ./internal/docsite ./internal/docsgen`, `npm run typecheck`,
  `npm run build`, and `npm test` in `web/apps/web` (21 content tests + 19 media
  tests). The Go coverage exercises the whole chain through tgtest's fake
  MTProto server and the gateway's own handlers: filename extraction and its
  survival into the JSON, byte ranges (`206`, `416`, invalid ignored), suffix
  ranges, `Content-Disposition` with a Persian name, chunked fetching, the
  expired-file-reference refresh, and unauthenticated media access.

## Not verified here

Live Telegram, the custom backend end to end, a real WordPress site, a real TCP
listener, a real multi-process supervisor run, and browser interaction remain
unverified where the sandbox blocks network/socket/browser execution. Do not
claim live success from unit tests.

## Next live step

`docs/VERIFYING-MEDIA-PUBLISH.md` is the runbook: build, configure both clients,
authenticate, run, then eight scenarios with an observable pass/fail per step —
preview, Telegram→`cat` publish with media, destination switching, explicit
detach, an unreadable source, a real destination failure, partial success and
retry, and file-reference refresh. Use real configured values; never invent
endpoints or credentials. Anything not run is recorded as **not exercised**,
never as passed.

## Topic index

- `STATE-history-2026.md` — dated implementation history retained for audit.
- `ARCHITECTURE.md` — component/data-flow details.
- `DECISIONS.md` — binding architectural/product decisions.
- `GOTD_NOTES.md` — verified dependency behavior and live findings.
- `LOG.md` — chronological work log.

## Dashboard counts endpoint and its panel row (2026-11-02, later)

`GET /api/v1/local/stats/dashboard` is implemented in this checkout and the
panel's پیشخان reads it: one request, one figure per collection
(`categories`, `channels`, `wordpress_sites`, `administrators`). A `null` figure
means the caller may not see that collection — it draws **no card**, never a
zero and never a "no access" notice — and the row takes as many columns as cards
are left (1–4), or nothing at all when there are none. The `/local/users` list
call that existed only to count is gone.

Two behaviour fixes went in with it: `GET /api/v1/local/categories` now
subtracts an explicit `view:deny` from a global view (it did not), and the
WordPress site list reports `meta.total`. Both are what makes the dashboard's
numbers equal to what the matching list route shows.

VERIFIED here: `gofmt`, `go vet`, `go test ./...` (all packages; 2 worker socket
tests skip because the sandbox denies binding), `go build`, `make build`, and
the route answering `200` at `/admin/api/v1/local/stats/dashboard` over the real
handler. NOT verified: a running daemon and the panel in a browser — this
sandbox cannot bind a port.

## Channel explorer and message timeline redesign (2026-11-04)

The channel-management area under `web/apps/web/app/` is now a two-pane
Telegram-style workspace instead of a table. `routes/_panel/channels.tsx` is the
workspace (channel list + filters + management actions) and
`routes/_panel/channel-messages.tsx` is a **child** route at
`channels/:id/messages`, so the list stays mounted across selection and deep
links, back and forward keep working. `routes.ts` nests them.

Layout contract (product requirement, not verified in a browser here): under
`dir="rtl"` the channel list is the first child and lands on the **physical
right**; the message timeline fills the **physical left**. Every message row is
`Message align="end"` (`justify-end`, physical left under RTL), the publish
action is the row's first child so it sits on the physical right of its own
bubble, and reactions use `justify-end` for the physical bottom-left.

Messages are now chronological (oldest first, newest at the bottom).
`useInfiniteMessages` reverses each API page on arrival and **prepends** older
pages; scroll position is anchored across a prepend and the first page scrolls
to the newest message. Its generation-token guard is unchanged and is what keeps
a stale page from another channel out of the current list.

Message text renders from the API's own `entities` (UTF-16 spans) through
`lib/message-entities.ts`; a bare URL is still auto-linked only when no entity
covers it. Nothing is rendered as HTML. Media is drawn **inside** the bubble:
images are bare `<img>` (no attachment card, no type badge, no size), audio and
voice are real `<audio>` players, video is `<video controls>`, and only a true
file becomes an `Attachment` with its filename and a download.

Deleting a channel is one dialog (`channel-delete-dialog.tsx`, `AlertDialog`)
with an optional "همچنین از کانال در تلگرام خارج شوم" checkbox that is
**unchecked by default**. `lib/channel-actions.ts` builds the submit payload, so
unticked is provably `telegram: "false"` (local record only) and the two effects
are never reported as one. The standalone Telegram-leave action is gone from
this interface.

`packages/ui` gained thin Base UI compositions (`message`, `bubble`,
`attachment`, `alert-dialog`, `checkbox`, `scroll-area`, `empty`). The shadcn
registry is **not reachable from this sandbox**, so `Message`/`Bubble`/
`MessageScroller`/`Attachment`/`Empty` could not be installed; these are
equivalent compositions on the project's own Base UI + `cn` + token stack. No
design-system upgrade was done.

Routing detail worth keeping: `channels.tsx` exports `shouldRevalidate` that
returns true only for a form submission, so moving between channels or changing
filters never refetches the channel list. The list is read across all pages with
`listAll`; search + server + category filters apply **client-side**, so changing
them needs no request and no submit button. Pagination follows `meta.has_more`,
so server filtering does not operate on only the first page.

VERIFIED here: `tsc --noEmit` clean; 57 new behaviour tests plus the existing 21
content and 19 media tests pass (`node scripts/test-panel.mjs` and the other
`scripts/test-*.mjs`); `npm run build` in `web/apps/web` passed before work was
stopped. NOT verified: anything in a browser — RTL pane positions, bubble
alignment, media rendering and responsive behaviour are asserted from rendered
markup in tests, not seen rendered. This sandbox cannot bind a port
(`EPERM`), and the panel dev server's `launch.json` `panel` entry has a `url`
with a path, which the preview tool refuses. The user will build and test.

## Media ids are opaque (2026-11-04, hard-won)

`rpc.Media.ID` is **not** a coordinate. `internal/worker/worker.go` writes
`fmt.Sprintf("%d:%d", channelID, messageID)` into it, and the API documents it
as something to pass back. Never decode it.

And `photoIndex` in `Worker.Media(ctx, channelID, messageID, photoIndex, slice)`
is not an attachment selector either: per `rpc.Backend.Media` it "selects the
thumbnail size for photos (0 = the largest)". Every reader of an attachment —
the gateway's `media()` route included — passes `0`. A publish must too.

An `attachment_id` in a publish request is therefore an **identity** to compare
against `source.message.Media.ID`, never a position to compute.

Also NOT verified: the deletion flow against the real `POST /local/channels/{id}/leave`
(believed unchanged; no backend code was modified in this work).

## Publishing workspace (2026-11-04)

The "ارسال این پیام" dialog is no longer a two-step desktop stepper. It is one
shared draft drawn two ways: `components/publish/publish-workspace.tsx` renders
`DesktopPublishingWorkspace` (three columns at ≥1200px) or
`MobilePublishingWizard` (three steps below that), and both edit the same
`PublishDraftState`. The physical desktop column order is destination (right) →
editor (centre) → preview (left), which in RTL means the destination panel is
the **first** DOM child of the grid.

`lib/publish-draft.ts` is the one model. It owns the discriminated drafts
(`TelegramPublishDraft` / `WordPressPublishDraft`), the hashtag/mention
synchronisation through the existing `content.compose`/`parse`, the entity
policy, `validate`, `canAdvance`, `layoutFor` and the request builders. Both
layouts read it; neither keeps a second copy of a rule.

Entity policy worth keeping: the source's formatting spans are carried over
**only while the body is byte-identical to the extracted baseline**. The moment
the body is edited those offsets describe nothing, so they are dropped and the
outgoing spans are re-lexed from the finished text (`content.lexEntities`). A
stale offset is never sent. `content.ts` gained that `lexEntities` export.

Backend gaps that were real and are now filled: `POST /local/wordpress/{id}/posts`
accepted only title/content/status/tag **names**. It now also takes `slug`,
`categories`, `tag_ids` and `media.attachment_id`, and `GET
/local/wordpress/{id}/categories` and `/tags` exist so the form can offer the
destination site's own terms through this backend (the browser never holds a
WordPress credential). `wordpress.Client` gained `ListTerms` (paged, with
`X-WP-Total`/`X-WP-TotalPages`); an empty `slug` is simply not sent, so
WordPress generates one — the right answer for a Persian title. Telegram sends
are still one channel per request; a multi-channel publish is a bounded run of
those with a per-channel result.

Also verified after the swap: `npm run typecheck` clean and `scripts/test-publish.mjs`
59 green (the rendered markup still shows `type="checkbox"` and `disabled`, so
Base UI's hidden input is the one the assertions mean).

VERIFIED here: `gofmt -l .` clean; `go vet ./...`, `go build ./...` and
`go test ./...` all green (including 11 new gateway tests in
`wordpress_terms_test.go`); `go run ./cmd/docsgen` and `--check` up to date;
`go test ./internal/apidocs` green; frontend `npm test` 156 green across
`test-content` (21), `test-media` (19), `test-panel` (57) and the new
`test-publish` (59); `npm run typecheck` clean; `npm run build` in
`web/apps/web` passed.

NOT verified: the interface in a browser. The three-column order, the wizard
steps and the previews are asserted from `renderToStaticMarkup` markup, never
seen rendered. A temporary fixture route was written to render them and was
deleted again unused, because the preview tool cannot start the panel (`panel`
in `.claude/launch.json` carries a `url` with a path) and this sandbox cannot
bind a port. Also not verified against a live deployment: real WordPress
taxonomy responses, real multi-channel sends, and the featured-image upload
path beyond the gateway's own tests.

`publish-primitives.tsx` no longer restates the design system: its local
`Checkbox` and `Textarea` were swapped onto `@workspace/ui`'s `checkbox` (Base
UI, so `onCheckedChange` is its change API, not `onChange`) and a new
`packages/ui/src/components/textarea.tsx` that now sits beside `input.tsx` as
the shared multiline control. `packages/ui` had no textarea at all, which is why
that one had to be added there rather than imported. What is left in
`publish-primitives.tsx` is only behaviour — `ChipInput`, `SearchableCheckList`
and `Field`.

## Channel workspace goes full-bleed (2026-11-04, later)

The channel workspace now owns the whole content area under the breadcrumb bar.
`lib/page.tsx` gained `fullBleedSection(name)` (a `SectionHandle` with
`fullBleed: true`), and `routes/_panel/layout.tsx` reads it from `useMatches()`
and renders its `<Outlet />` either in the usual centered, padded page column
(`mx-auto max-w-6xl px-4 py-6`) or, for a full-bleed route, in a bare
`flex min-h-0 w-full flex-1 flex-col`. The switch is opt-in per route, so
dashboard, categories, WordPress and administrators are untouched.

`routes/_panel/channels.tsx` is the only route that opts in. Its page header
("کانال‌ها" + the description) and the surrounding `gap-4` are gone, and the
workspace's `bg-card rounded-xl border` wrapper is gone — the workspace is the
two panes directly, separated only by the channel list's `lg:border-e`. The
"کانال جدید" button moved from the removed header into the channel-list toolbar
(`ChannelListPane`), as a compact `size="sm"` button beside the search field.

Width/height contract: the workspace root is `flex min-h-0 w-full flex-1
flex-col overflow-hidden`; the panes row is `flex min-h-0 flex-1
overflow-hidden`, the channel list is `lg:w-[20rem]` (320px) and the messages
pane is `flex-1 min-w-0`. Both panes scroll their own regions, so `main` never
scrolls and there is no page gutter.

One `max-w` deliberately remains **inside** the messages pane:
`mx-auto ... max-w-3xl` on the message column in `channel-messages-pane.tsx`.
That is the readable column for bubbles (Telegram Desktop does the same), not a
page constraint — the workspace and both panes themselves have no `max-width`
and no centering. Bubble and media caps (`max-w-[min(100%,40rem)]`,
`max-w-[min(100%,32rem)]`) are the approved design and are unchanged.

VERIFIED here: `react-router typegen` + `tsc --noEmit` clean, all four test
runners green (21 + 19 + 57 + 59 = 156), `npm run build` in `web/apps/web`
succeeds. NOT verified in a browser: that the workspace actually meets the
breadcrumb bar with no gap, that both panes scroll independently, and the
mobile single-pane switch. This sandbox cannot bind a local port (`EPERM`) and
there is no authenticated backend to log into, so no route was rendered.

## Publish action becomes a bubble-attached pill (2026-11-04, later)

The "ارسال این پیام" action is no longer a bordered button in a column beside
the message row. It is now a pill — `variant="secondary"`, `h-7`,
`rounded-full`, `px-3`, `text-xs`, `shadow-none`, `ring-2 ring-background`, no
icon — that hangs off its own bubble's bottom edge at the physical right.

Composition in `components/channel-explorer/message-item.tsx`: a
`data-slot="message-bubble"` wrapper (`relative flex flex-col pb-4`) holds the
`Bubble` and, below it, a `flex justify-start` row whose child is the pill with
`relative z-10 -mt-3.5`. Under RTL `justify-start` is the physical right, and
the negative margin lifts the pill onto the bubble's bottom edge so it straddles
it (14px of a 28px control above, 14px below).

Two details are load-bearing:
- The pill is **in flow**, not absolutely placed, so a bubble shorter than the
  action simply grows to hold it. That is what keeps it from pushing past the
  message pane and creating horizontal overflow, and it means the action can
  never be clipped.
- `actionBand` (`pb-2`) is added to whichever of `BubbleContent` /
  `BubbleReactions` is last **only when a publish action is present**, giving
  the straddling control a clear band so it never covers text, media or the
  reaction chips, however narrow the bubble is.

Reactions stay at the physical bottom-left (`justify-end`) and the footer
(views · time) stays below the bubble, unchanged.

VERIFIED: `tsc --noEmit` clean and all four test runners green (21 + 19 + 60 +
59 = 159). The rendered markup was inspected directly (a throwaway SSR render)
to confirm the class and DOM contract. NOT verified in a browser: the visual
match against the shadcn "Yes, run it" reference, and the desktop/tablet/mobile
appearance — this sandbox cannot bind a local port and there is no
authenticated backend here to log into.

## Message timeline: bidirectional paging and scroll anchoring (2026-11-04, later)

The channel message history is now a real timeline rather than a one-way list
behind a button. Verified API facts first, because the paging is not what it
looks like: **page 1 is the newest edge**, every later request continues below a
per-channel **message-id** cursor held in the worker (`worker.Service.cursorFor`
/ `setCursor`), each page arrives **newest-first**, and `has_more` is
`len(msgs) >= limit` — a short page is the real end. So the page number is a
sequence, not an offset, and new Telegram arrivals cannot skip or duplicate
what is already walked. There is no total count for messages and no way to ask
for messages *newer* than an id.

New pure modules (both under `web/apps/web/app/lib/`, both tested):
- `message-timeline.ts` — `TimelineState` (messages, `page`, `hasOlder`,
  `isNewest`, `gap`, `stalled`, `walkingNewer`, per-direction loading and
  errors) plus `applyBatch` / `beginLoad` / `failLoad` / `retry` / `nextRequest`
  / `followNewest` / `showJumpToLatest`. `nextRequest(state, dir)` is the one
  place that refuses a second request for the same page, a request while one is
  in flight, and a request for history known to have ended.
- `scroll-anchor.ts` — `captureAnchor` / `restoreScrollTop` on a named message
  and its offset from the viewport top, so a picture finishing loading above the
  reader cannot move their place. `restoreScrollTop` answers null when the
  anchored message is gone rather than guessing.

`hooks/use-message-timeline.ts` fetches pages and owns the generation guard (a
channel change bumps it, so a stale answer can never be spliced into the next
channel) and one in-flight request at a time. It auto-continues **only** a
newer walk the reader started (`walkingNewer && gap`); nothing else fetches
upwards on its own.

`components/channel-explorer/channel-messages-pane.tsx` wires it: top and
bottom `IntersectionObserver` sentinels, rAF-coalesced near-bottom state that
only renders when the boolean flips, element-based anchor capture/restore
around a prepend plus a `ResizeObserver` that re-applies it while held, a
one-time scroll-to-bottom per channel after the first page renders, auto-follow
that stops when the reader scrolls away and resumes at the bottom, and a
floating `آخرین پیام‌ها` control. `hooks/use-infinite-messages.ts` is gone —
`explainMessagesError` and `MESSAGE_PAGE_SIZE` moved with it.

Known limits, deliberately not papered over: this codebase has **no** message
refresh or push, and polling was explicitly out of scope, so brand-new messages
are only picked up when the reader activates the jump control or reloads. The
newer walk closes a hole by re-walking from the newest edge (the API cannot ask
for "after id"), so filling a large gap costs several page requests. A chain
break in the older walk is reported as a hole (`gap`) with a recovery action
rather than drawn as continuous history.

VERIFIED: `tsc --noEmit` clean; `test-content` 21/21, `test-media` 19/19,
`test-panel` 97/97 (40 new timeline/anchor tests), `test-publish` 59/59;
`npm run build` succeeds. NOT verified in a browser: wheel/touch/keyboard
scrolling, long histories, and the visual smoothness of a prepend — this
sandbox cannot bind a local port and has no authenticated backend.

### Administrators management UX redesign (2026-11-04)

`web/apps/web/app/routes/_panel/administrators.tsx` now only lists accounts and
wires four dialogs; the dialogs live in `components/administrators/` and the
rules they share in `lib/admin-permissions.ts` and `lib/admin-users.ts`.
`components/password-input.tsx` is the one password field (eye toggle, `type`
button so it cannot submit) and `change-password-dialog.tsx` uses it too, so the
visibility logic exists once. `@workspace/ui` gained `components/switch.tsx`
over Base UI's Switch — there was none, and the account-status row needs one.

Create is `افزودن مدیر` with `نام کاربری`, `نام مستعار`, `رمز عبور` and no
status choice: `createUserBody` always posts `is_active: true`. Edit is
`ویرایش مدیر` with no password field at all (`updateUserBody` never carries
one) and the status on one `flex flex-nowrap items-center justify-between` row.
Password changing is its own dialog from the row menu, one field and no
confirmation; the API has no password route, so it PUTs `{"password": ...}` and
nothing else (`passwordBody`).

The permissions dialog is six tabs over **one** draft (`normalizeDraft` on load,
`serializeDraft` on save): `گزارش دسترسی‌ها` first and read-only, then
`دسته‌بندی‌ها`, `کانال‌ها`, `سایت‌های وردپرسی`, `مدیرها`, `عمومی`. The three
resource tabs share `PermissionScopeEditor` and reuse `CategoryMultiSelect` /
`MultiPicker`; `مدیرها` holds `users.view`/`users.manage`; `عمومی` holds only
`servers.view` as the product asked. `content.publish` is kept in the draft and
round-trips untouched but has **no editor** anywhere — see the report tab, which
lists it read-only. Switching tabs cannot lose a selection, Cancel discards, and
reopening reloads from the API.

VERIFIED: `make check` green end to end — gofmt, `go vet ./...`, `go test ./...`
(incl. a new `internal/gateway/permission_scope_test.go`), the panel version
tests, `web/` typecheck, all five web test suites (21+19+98+59+**49** new),
`docsgen --check` (72 pages unchanged, no route changed), and the build; the
panel was re-embedded into `internal/panel/files` (81 files) and `bin/telesync`
built. NOT verified in a browser: this sandbox denies local port binding
(`PermissionError: [Errno 1] Operation not permitted`), so no dialog was clicked
through — the dialog markup is covered by SSR tests over `renderToStaticMarkup`,
and the permission round trip by the Go API test. NOT verified: live Telegram or
WordPress behaviour, unchanged by this work.

`@workspace/ui`'s `FieldError` now renders whenever the caller renders it
(`match` on the primitive). It used to show a message only while the control was
*natively* invalid, so every server refusal the control knows nothing about — a
taken username, a refused URL — was computed, passed to `FieldError`, and
dropped on the floor. Every form in the panel already gates `FieldError` on its
own error, so this only makes intended messages visible.

## Links in the publishing dialog (2026-11-05)

The Telegram editor has three managed fields now: hashtags, mentions and
**links**. A link keeps its anchor text **in the body** where the message put
it — it is not lifted out like a hashtag, because the anchor text is a word of
the sentence. Only a link the user adds is appended, on its own line.

`lib/content.ts` `parse` reports `ParsedLink { anchorText, url }` from `url` and
`text_url` entities (or, with no entities, from the text's own URLs) without
removing them from `main`. `lib/publish-draft.ts` holds `LinkItem`, `locateLinks`
(matches anchors in listed order, so duplicate anchor texts are never confused),
`addLink`/`editLink`/`deleteLink`, and a `setTelegramText` that reconciles the
list with the body. `link-list.tsx` is the field's UI; rows commit on blur.

Outgoing entities come from the list: `url` when the anchor text is the URL,
`text_url` carrying `url` otherwise, and the source's spans and the lexer both
stand down over those spans. **No backend change was needed** — `rpc.Entity.URL`
and `inputEntity`'s `text_url` case already carry the target to Telegram.

The list is **shared by both platforms** and lives on `PublishDraftState.links`,
not on the Telegram draft: a link is a link of the message, and the post and the
article are two ways of sending it. Both bodies keep the anchor text;
`addLink`/`editLink`/`deleteLink` rewrite both, and `reconcileLinks` drops a link
only when **neither** body shows its words any more. `composeWordPressContent`
wraps each anchor in `<a href>` at request time and leaves the rest of the body
byte-for-byte as written, escaping only the link's own values. `LinkList` is
offered in both editors.

VERIFIED: `npm test` 311 green (content 26, media 19, panel 109, publish 108,
administrators 49), `npm run typecheck` clean. NOT verified: in a browser (the
preview tool cannot start the panel — `.claude/launch.json`'s `panel` entry has
a `url` with a path, and this sandbox cannot bind a port), and a live send with
links. Out of scope: links in the WordPress article body, which is HTML the user
writes separately.

## `telesync serve-webhook`, a receiver for the delivery engine (2026-11-08)

The webhook engine could only be tested against somebody else's server. It now
ships a receiver in the same binary: `telesync serve-webhook [--host <addr>]
[--port <n>] [--save-response] [--output-dir <dir>]`, an independent HTTP server
on its own port with exactly four paths and no dependency on the daemon, its
database, its Telegram clients or its configuration.

`internal/webhooktest` holds it: `config.go` reads four variables of its own
(`TELESYNC_TEST_WEBHOOK_HMAC_SECRET`, `_BEARER_TOKEN`, `_HEADER_NAME`,
`_HEADER_SECRET`) and refuses to start when any is missing or unusable;
`auth.go` verifies one mode per route; `echo.go` renders the request as it
arrived; `storage.go` writes one record per request; `server.go` wires the four
paths. `internal/cli/webhook.go` is the command.

**The path chooses the mode and the method chooses nothing.** All methods are
accepted on all four paths, HEAD returns the headers of the reply it would have
had with no body, anything else is 404, and a body over `DefaultMaxBodyBytes`
(2 MiB) is refused with 413 without being read. A request whose body was not
whole is never authenticated.

**The echo keeps everything**, including authentication headers — which is why
it binds `127.0.0.1` by default, warns when asked not to, keeps its records
`0700`/`0600`, and prints no credential and no body to the console. Valid JSON
is echoed both raw and parsed; anything not valid UTF-8 is base64 rather than
converted; a truncated body is counted, never half-echoed.

**Verified:** `gofmt -l`, `go vet ./...`, `go test ./...`, `go build`, and
`docsgen --check` (84 pages unchanged — this adds no gateway route). The sender
compatibility chain is a test, not a claim:
`internal/automation/webhooks/receiver_compat_test.go` runs the production
`Service.TestWebhook` and `Service.Handle` through the production transport into
a running receiver in all four modes and refuses a mismatched secret in each,
asserting the signed body comes back byte for byte. Run live against
`./bin/telesync serve-webhook` on 8091: the four endpoints, a computed Standard
Webhooks signature accepted and the same one refused after the body changed,
bearer and custom header valid and invalid, HEAD, 404, 413, 16 records written
to `temp/webhook-responses` with absolute paths printed, no secret or body in
the console, and SIGINT exiting 0 after `stopping`.
**Not verified:** a delivery driven by a live Telegram event; anything bound
beyond loopback (deliberately — this is a development tool).
**Pre-existing and unrelated:** `internal/worker`'s two Unix-socket tests fail
in this environment (path length at the default macOS temp directory; a
second-listener assertion at a shorter one). That package is untouched.

## `POST /messages` → `/messages/copy` (2026-11-09, implemented)

Handoff: `.agents/handoffs/telegram-messages-copy-route.md`, `status: DONE`,
baseline `887910f`. Prepared first (failing tests + plan), then implemented on
the user's instruction. The route is now `POST /api/v1/telegram/{key}/messages/copy`;
`POST /api/v1/telegram/{key}/messages` answers `404 not_found` on every method
and names the copy route. No alias, no redirect. The handler is
`Server.copyTelegramMessage`.

**Verified.** `publishing.Validate` refuses any request whose
`source.local_channel_id` or `source.message_id` is not positive
(`publishing/request.go:113`), so the route can only ever copy an existing
message — the rename is a correction, not a rebrand. `PublishTelegram` has
exactly two production callers: `gateway/publish.go:131` (this route) and
`publishing/automation.go:32` (the workflow engine, via
`workflows/publish.go:172`). Nothing in `publishing/` imports the gateway, so
the whole package stays live and untouched.

**Idempotency is path-independent, confirmed at the source.** `requestHash`
(`publishing/send.go:163`) is `sha256(json.Marshal(Request))`; the path is not
a field and `IdempotencyKey` is `json:"-"`. The operation key is
`clientKey+":"+destination.channel_id` (`publishing/telegram.go:77`). A stored
attempt therefore survives the move unchanged.

**Error codes, restated from source** — there is no `publish_source_missing`:
`publish_source_unavailable` (`CodePublishSourceMissing`),
`publish_source_forbidden`, `publish_destination_unavailable`,
`publish_destination_forbidden`, `publish_forbidden`,
`publish_permission_denied`, `publish_idempotency_conflict`,
`publish_media_too_large`. The last one **is** live on this route —
`publishing/send.go:59-62` and `publishing/errors.go:192-193` — contrary to the
task brief.

**Two gaps the task brief missed.** (1) `docs/api/examples/README.md:27` names
the old route; `TestIndexCoversEveryRoute` and `TestExamplesAreValidAndIndexed`
both fail without it. (2) `componentOf` (`internal/docsgen/render.go:297`)
selects the `publishing` paragraph by `HasSuffix(pattern, "/messages")`, which
also wrongly gives it to the read route `GET .../channels/{channel_id}/messages`
(visible in the committed page for that route). The correct predicate is
`"/messages/copy"`, which fixes both at once.

**Deleting the arm does not yield 404.** Without a dedicated arm, `POST /messages`
falls to the GET-only gate at `gateway.go:396` and answers `405 method_not_allowed`
with `Allow: GET`. The handoff specifies a `404 not_found` arm placed before that
gate, naming `/messages/copy` — the same shape `gateway.go:462-468` already uses
for a misspelled resource. The brief's option B (405 with `Allow: POST`) is not
implementable: RFC 9110 §15.5.6 requires `Allow` to list methods the resource
*does* support.

**Decisions recorded:** 404-with-hint for `/messages` on every verb; handler
renamed `publishTelegram` → `copyTelegramMessage` (forced coupling: docsgen
resolves that literal and writes an unresolved item if it drifts); panel Persian
copy left as «ارسال» (the operator's action is unchanged; the copy/send
distinction only becomes user-visible when a real send route lands).

**Verified this session:** `go test ./...` 30 packages ok,
`go run ./cmd/docsgen --check` up to date, `gofmt -l .` empty, `go vet ./...`
clean — all before the test changes. Red captured: 29 failing in
`internal/gateway` (7 new + 22 retargeted), all at `405`/`404`/`200` as recorded
in handoff §F. All 14 `TestWordPressPublish*` and all `TestBackgroundPublishing*`
green and unedited.
**Not verified:** live Telegram behavior. No handshake against a real client.

**Graph refreshed.** `graphify-out/` had been built at `45db19d` — 447 files and
~79k lines behind HEAD, missing the whole v0.0.3 automation series. Rebuilt
code-only (8803 nodes / 36886 edges / 236 communities, now at `887910f`) and
reclustered. The `--update` run without `--code-only` is blocked in this sandbox
for want of an LLM API key, so 212 doc/paper files keep their stale semantic
edges; code extraction is current.

**Implemented and validated.** `gofmt`, `go vet ./...`, `go build ./...`,
`go test ./...` (30 packages), `docsgen --check`, `go test ./internal/apidocs`,
`npm run typecheck`, `make web-test` (48 tests) and `make panel` all green.
Docsgen created `api-v1-telegram-key-messages-copy.md`, deleted the old
`api-v1-telegram-key-messages.md`, and — via the `componentOf` correction —
dropped the wrongly-attached `publishing` paragraph from the read route
`.../channels/{channel_id}/messages`. Unresolved items stayed at 23, which
confirms the handler rename and the docsgen handler map stayed coupled.

`TestLinkedRoutesExist` failed once and was right: the new prose in
`publishing.md` named the retired path in a code span, which that test reads as
a claim that a route exists. Reworded to describe the behaviour instead; the
test was not weakened and no route was added to `table.go` to satisfy it.

**⚠ Not this change: a voice-message feature landed in the same working tree
mid-implementation.** It is not attributed to this task and has zero overlap
with its source files, but it does edit `internal/automation/publishing/send.go`
(adds `sendKind`, `Duration`, `Waveform`), `rpc/rpc.go`, `worker/{send,worker}.go`,
`tgapi/{send,tgapi}.go`, `gotdclient/{channel,dialogs,send,updates}.go`, plus
four new `voice_test.go` files (~236 insertions, 11 files). It violates this
handoff's "must not change `internal/automation/publishing/**`" — but not
through this implementation, whose diff in that package is empty. The full
suite was measured with both present and is green. Nothing committed; HEAD is
still `887910f`. Separate the two diffs before committing.

## Voice messages published as documents (2026-11-09, fixed)

**Problem.** A real Telegram voice message republished to another channel
arrived as a generic downloadable file. Bytes crossed intact (~1.3 KB, ~5 s);
only the media semantics were lost. The destination document was even *named*
«پیام صوتی».

**Root cause — a chain, not one point.** Source classification was already
right: `gotdclient.documentType` returns `"voice"` on
`DocumentAttributeAudio.Voice` (`channel.go:84-97`). It died at
`publishing/buildSend` (`send.go:67-70`), which set `kind := "document"` for
anything that was not a photo; `sendFileName` then fell back to `Label`, giving
the outgoing file the name «پیام صوتی». And even had the kind survived,
`gotdclient/send.go:59-67` built `inputMediaUploadedDocument` with one attribute
— `documentAttributeFilename` — and **never wrote `documentAttributeAudio`**,
so the destination was told nothing about what the media was. `tgapi.SendMedia`
had only `Kind/Name/MimeType/Data` and no way to express it.

**Fix** (smallest correct change, shared pipeline, no schema/FFmpeg/new service):
`Duration` + `Waveform` added to `tgapi.Media`, `rpc.Media`, `tgapi.SendMedia`,
`rpc.SendMedia`; `SendMediaAudio` / `SendMediaVoice` kinds added. Read side:
new `gotdclient.MediaDuration` / `MediaWaveform` beside the existing
`MediaSize`, wired through `normalizeMessage`, the `updates.go` DTO and
`worker.messageDTO`. Pipeline: new `publishing.sendKind` maps the *source* type
(photo/voice/audio pass through, everything else stays `document` as before);
`sendFileName` returns `""` for a recording the sender never named. Destination:
new `gotdclient.sendMediaInput` writes
`documentAttributeAudio{voice, duration, waveform}` for `voice` and `audio`,
`ForceFile` **false** for both (true for `document`, unchanged), and a filename
attribute for `voice` only when the sender actually gave one.

Music stays `audio`, ordinary uploads stay `document`, photos stay photos,
video/sticker/gif outgoing shaping is untouched. Nothing is re-encoded.

**Verified:** `gofmt -l internal/ cmd/` empty, `go vet ./...` clean,
`go build ./...` ok, `go test ./...` all packages ok,
`go run ./cmd/docsgen --check` up to date, `go test ./internal/apidocs` ok,
`npm run typecheck` and `npm run build` in `web/` ok. Regression tests cover the
brief's A–J: source voice detection, music-not-voice, document-stays-document,
metadata round trip across `tgapi`/`rpc`/worker boundaries, the outgoing
MTProto request (decoded from a fake MTProto server via `tgtest`, asserting
`Voice`/`Duration`/`Waveform`/`ForceFile` — not a filename check), manual
publishing, workflow publishing (with AI-polished text), cross-connection
transfer, no-silent-downgrade, and other-media regression.

**Not verified:** live Telegram. No MTProto test credentials here, so no message
was sent to a real channel and the round play button was never observed. `cat`
goes through the same `gotdclient.SendMessage` as the official profile (one
adapter, one send path), so it is sent the same attribute; whether a backend
honours it is a destination property that cannot be established here. A refusal
is `publish_unsupported` and is never retried as a plain document.

**Docs:** `docs/api/manual/components/telegram.md` gains `duration` and
`waveform` rows (the generator does not know these fields) and a note that
`type` comes from the source attributes and from nothing else. Mirrored into
`internal/docsite/files` by `go run ./cmd/docsgen`.

**Handoff record:** `.agents/handoffs/telegram-voice-message-publishing.md`
(status DONE). Written as the session channel; `approval: not_requested`,
because no instruction named it — the user's mission brief directed the whole
task in one session, so prepare/handoff/implement was not used.

**Coordination.** A peer session was implementing `telegram-messages-copy-route`
in the same tree while this ran (moving the publish route to
`POST /messages/copy`); none of that work was touched.
`internal/gateway/publish_voice_test.go` resolves the live publish path at run
time (`/messages/copy`, falling back to `/messages`) so it is not coupled to
which address wins.

## Channel Explorer server filter (2026-11-11)

The category toolbar now has a compact server multi-select on the physical
right under RTL. Empty selection means all configured connections; repeated
`server` query params select a deduplicated union by `Channel.client_key`.
Server, category and search filters apply together to the client-loaded channel
inventory. The loader now follows all `/local/channels` pages instead of
stopping after 500 rows. Query changes preserve unrelated params and channel
links retain filter state; if the selected channel no longer matches the active
filters, its timeline is hidden and the route returns to the unselected
explorer state.

**Verified:** `npm run typecheck`, focused `node scripts/test-panel.mjs`
(150/150), `npm test` (all suites green, including 151 panel assertions), and
`npm run build` in `web/apps/web`.
**Not visually verified:** desktop/mobile RTL layout or live dropdown keyboard
interaction; no browser session was available. Handoff:
`.agents/handoffs/v0.0.4-channel-server-multiselect.md` (DONE).
