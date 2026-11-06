---
task_id: telesync-v003-automation
title: Telesync v0.0.3 — automation, webhooks and AI-powered publishing
status: DONE
created: 2026-11-05
updated: 2026-11-05
baseline_commit: d5ad3ed
approval: granted (by user, 2026-11-05 — the v0.0.3 directive directs implementation end to end and was confirmed as the approval)
---

# Telesync v0.0.3 — automation, webhooks and AI-powered publishing

## A. Metadata

See frontmatter. The repository's prepare/implement gate is satisfied by the
user's explicit directive for this release, confirmed in session on 2026-11-05.
No production change outside the ownership map in §D is authorized by this
handoff.

## B. Requirements

### Problem

Telesync v0.0.2 can fetch and publish channel content by hand, but nothing
reacts to new messages. There is no update listener (the only gotd update hook
is a deliberate no-op at `internal/profile/profile.go:180`), no queue, no
webhook, no workflow, no AI, and no persisted update state. Publishing is
reachable only through two HTTP handlers.

### Desired behavior

A channel publishes a message → Telesync receives it durably → optionally
polishes it with AI → publishes it to configured destinations. Webhooks run
alongside. Everything is background driven and survives restarts.

Testable statements:

- A new message in a managed source channel becomes exactly one stored event.
- A duplicate update, or the same message seen through a second account on the
  same network, produces no second automation run.
- The same channel and message ids on a different network are distinct events.
- Automation runs with no HTTP request and no browser session.
- A reconnect recovers missed messages, in order, without administrator action.
- At most one workflow per managed channel, enforced by the schema.
- AI failure blocks publication unless the workflow opted into fallback.
- Excluding images from AI input never excludes them from the destination.
- Webhooks carry media metadata and a URL, never media bytes.
- Every webhook request and response is recorded, redacted and inspectable.
- A message Telesync published never re-triggers automation.

### Acceptance criteria

See the release checklist in the v0.0.3 directive §41. It is authoritative and
reproduced in condensed form in §I.

### Out of scope

Scheduling, branching, node editors, user scripts, LLM tool calling, MCP,
approvals, moderation queues, automatic WordPress taxonomy creation, generated
images, transcription, video analysis, historical backfill before activation,
edit/delete triggers, and any new queue infrastructure.

## C. Code discovery

### Relevant files

| Path | Role today |
| --- | --- |
| `internal/channelstore/migrate.go` | ordered `[]migration`, currently versions 1–15 (1–7 pre-existing, 8–15 added by this release) |
| `internal/channelstore/store.go` | SQLite `Store`: WAL, `foreign_keys(1)`, `busy_timeout(5000)`, `MaxOpenConns(1)`, injectable `Options.Now` |
| `internal/channelstore/permission.go` | `GlobalPermission`, `Authorizer`, `Allowed`, `CanList` |
| `internal/appconfig/config.go` | JSON config loader `Load(path, lookup)`, `build()`, `raw*` decode layer |
| `internal/appconfig/automation.go` | **new** — `AutomationConfig`, `Client.NetworkKey()` |
| `internal/automation/automation.go` | **new** — shared contracts for all five workstreams |
| `internal/gateway/gateway.go` | hand-dispatched router, `envelope`, `Error`, `fail`, `pageMeta`, `parsePaging` |
| `internal/gateway/access.go` | `visible(authr, kind, action, localID)` — package private |
| `internal/gateway/publish.go` | the v0.0.2 publishing pipeline (unexported on `*Server`) |
| `internal/gateway/mediahttp.go` | range/streaming media responses |
| `internal/profile/profile.go` | gotd `telegram.Options`; the no-op `UpdateHandler` at :180 |
| `internal/gotdclient/manager.go` | per-client client lifecycle; `runClient` blocks after `setReady()` |
| `internal/gotdclient/dialogs.go` | `History`, `Message`, `normalizeMessage` |
| `internal/worker/worker.go` | `messageDTO`, synthesized `rpc.Media.ID` = `"%d:%d"` |
| `internal/rpc/rpc.go` | worker RPC contract, `Message`, `Media`, `SendRequest`, `MediaStream` |
| `internal/tgapi/tgapi.go` | domain `Message`, `Media`, `Reaction` |
| `internal/secrets/secrets.go` | AES-256-GCM `Encrypter`, `Box`, `Seal`, `Open` |
| `internal/wordpress/{client,dial,url}.go` | the SSRF-safe transport discipline to reuse |
| `internal/apidocs/table.go` | `gatewayRoutes` — the route table docsgen and its tests read |
| `web/apps/web/app/routes.ts` | React Router v8 route config |
| `web/apps/web/app/lib/nav.ts` | `navItems`, `visibleNavItems` |
| `web/apps/web/app/lib/api.ts` | `apiRequest`, `ApiError`, `PageMeta` |

### Symbols and contracts

- `channelstore.Open(path, Options) (*Store, error)` — migrations run on open.
- `channelstore.FormatTime`/`formatTime` — RFC3339Nano UTC for every timestamp.
- `visible(authr, kind, action, localID) bool` — deny by default, explicit deny
  wins, writes require view. Telegram peer ids must be resolved to
  `managed_channels.id` first.
- `gateway.Config.BasePath` is `/admin`; `s.fullPath(p) = BasePath + p`; route
  matching strips BasePath, path constants never include it.
- `rpc.Media.ID` is synthesized as `fmt.Sprintf("%d:%d", channelID, msgID)`. It
  is not a Telegram id.
- Media URL emitted today: `/telegram/{client_key}/channels/{channel_id}/
  messages/{message_id}/media`, relative to the API mount.

### Dependencies and call paths

- `cmd/telesync/runtime.go` → `runtime.Run` → `supervisor.Run` (worker
  processes) + `gateway.New` (HTTP). Automation must start and stop here.
- `gateway.publishTelegram` → `resolvePublishSource` → `buildSend` →
  `worker.SendMessage` → `gotdclient.SendMessage`. The extraction point is the
  line before `buildSend` (`publish.go:127`).
- `gateway.runWordPressPublish` does not use the actor at all and is already
  callable; only `credentialFor` needs an actor.

### Graphify findings

Graph built at `45db19d`, HEAD `d5ad3ed` (three commits behind; the delta is
publish-editor and media-serving changes, no structural rewrite). Used for
package and symbol orientation only; every load-bearing fact above was verified
against source.

### Constraints that apply

- `AGENTS.md` invariants: client isolation, permission checks through
  `visible()`, reads never write, honest `partial_failure`, docs generator for
  route changes, migrations only ever added.
- gotd stays at `v0.162.0`. Its `telegram/updates` package already provides
  `StateStorage`, `ChannelAccessHasher`, `UserAccessHasher`,
  `OnChannelTooLong`, `OnLoadUserStateFailed`, `OnLoadChannelStateFailed`.
- Never hardcode client names or keys. `client_key` is operator configuration.
- Never store or return secrets in plaintext; reuse `secrets.Encrypter`.
- Do not commit, push, tag or deploy without explicit authorization.

### Unknowns / risks

- `internal/docsgen/run.go:347` `permissionNames` filters with
  `^(users|channels|categories)\.(view|manage|create)$` and `loadProject` only
  parses `internal/gateway`, so `facts.json`'s `permissions` is `null` today.
  Fixing it is in scope because the new permissions must be documented.
- The emitted media URL omits `BasePath` while routing includes it. Webhook
  payloads must build `public_base_url + BasePath + rel`, not the raw relative
  string.
- `publish_operations` uniqueness is `(user_id, idempotency_key)`, so a
  background publish must run under a real user id.
- Grouped media (albums) is one event per message. v0.0.2's publisher sends one
  media per request, so an album publishes as one destination message per item.
  Documented as a limitation, not silently aggregated.

## D. Implementation plan

### Ownership map — one writer per file, no exceptions

| Owner | Files |
| --- | --- |
| Orchestrator | `internal/channelstore/migrate.go`, `internal/appconfig/*`, `internal/automation/automation.go`, `internal/channelstore/permission.go`, `internal/gateway/automation*.go`, `internal/gateway/gateway.go` (error codes + dispatch only), `cmd/telesync/runtime.go`, `internal/runtime/runtime.go`, `internal/apidocs/table.go`, `docs/api/manual/components/automation*.md`, `docs/*`, `.env.example`, `docs/telesync.example.json`, `PROJECT_BRIEF.md`, `.agents/memory/*` |
| Agent A | `internal/automation/{events,queue,recovery}/**`, `internal/channelstore/automation_*.go` |
| Agent B | `internal/automation/webhooks/**`, `internal/channelstore/webhook*.go` |
| Agent C | `internal/automation/{workflows,publishing}/**`, `internal/channelstore/workflow*.go`, `internal/gateway/publish.go` (extraction only) |
| Agent D | `internal/automation/ai/**` |
| Agent E | `web/apps/web/app/**`, `web/packages/ui/**` |

`migrate.go` is closed for the duration: all automation tables are already
created by migrations 8–15. If a schema change becomes necessary, request it
from the orchestrator; do not edit the file.

### Schema already applied (migrations 8–15)

- **8 `automation_events`** — `id` (deterministic, `evt_…`), `type`,
  `network_key`, `source_client_key`, `managed_channel_id` FK,
  `telegram_channel_id`, `message_id`, `grouped_id`, `occurred_at`,
  `received_at`, `payload`, `created_at`;
  `UNIQUE (type, network_key, telegram_channel_id, message_id)`. Plus
  `automation_event_sources (event_id, client_key, …)` PK — the second
  observer of a deduplicated message is kept here so an authorized media
  reader is never lost.
- **9 `automation_jobs`** — `id`, `event_id` FK, `job_type`
  (`webhook_delivery`|`workflow_run`), `target_id`, `status`
  (`pending`|`processing`|`retry_wait`|`succeeded`|`failed`|`cancelled`),
  `attempt_count`, `max_attempts`, `available_at`, `locked_by`, `locked_until`,
  `last_error_code`, `last_error_message`, timestamps;
  `UNIQUE (job_type, target_id, event_id)`; indexes on
  `(status, available_at)`, `(status, locked_until)`, `(job_type, target_id, id)`.
- **10 `automation_recovery_state`** — `automation_checkpoints (client_key,
  channel_id, last_message_id, updated_at)` PK, `automation_update_states`,
  `automation_channel_pts`, `automation_peer_hashes` (the durable backing for
  gotd's `updates.StateStorage` / hashers).
- **11 `webhooks`** — name, description, enabled, url, method
  (`POST`|`PUT`|`PATCH`), auth_mode (`hmac`|`bearer`|`header`|`none`),
  auth_header, `secret_version`/`secret_nonce`/`secret_ciphertext`/
  `secret_sealed_at` (AES-GCM, same shape as a WordPress credential),
  `timeout_ms`, `max_attempts`, `created_by` FK, timestamps;
  `CHECK (auth_mode <> 'header' OR length(trim(auth_header)) > 0)`. Plus
  `webhook_sources (webhook_id, managed_channel_id)` PK.
- **12 `webhook_deliveries`** — `webhook_id`, `event_id`,
  `UNIQUE (webhook_id, event_id)`, status
  (`pending`|`delivering`|`succeeded`|`failed`|`cancelled`), `attempt_count`,
  `next_attempt_at`, `last_http_status`, `last_error`, timestamps. Plus
  `webhook_attempts` with `UNIQUE (delivery_id, attempt_number)` and the full
  redacted, pre-truncated request/response record.
- **13 `workflows`** — `managed_channel_id` **UNIQUE** FK, `enabled`,
  `ai_enabled`, `ai_prompt`, `ai_send_images`, `ai_publish_on_failure`,
  `config_revision`, `created_by` FK, timestamps. Plus
  `workflow_destinations` with a `CHECK` that a telegram row names
  `client_key`+`channel_id` and no site, and a wordpress row names `site_id`
  and nothing else.
- **14 `workflow_runs`** — `UNIQUE (workflow_id, event_id)`, status
  (`queued`|`processing`|`published`|`published_with_fallback`|
  `partially_published`|`failed`|`cancelled`), `original_content`,
  `polished_content`, `ai_status`
  (`not_used`|`succeeded`|`failed`|`fallback_used`), `ai_fallback_used`,
  `ai_model`, ai error and token columns, `config_revision`, timestamps. Plus
  `workflow_publications` with
  `UNIQUE (workflow_run_id, destination_type, client_key, channel_id, site_id)`
  and remote ids.
- **15 `automation_outbound_messages`** —
  `UNIQUE (network_key, client_key, channel_id, remote_message_id)`, `origin`
  (`manual`|`workflow`), `origin_id`. This is the loop-prevention record.

### Shared contracts

`internal/automation/automation.go` already defines and compiles:
`EventType`/`EventMessageCreated`, `MessageEvent`, `MessagePayload`, `Job`,
`JobKind`, `JobStatus`, `JobError`, `JobHandler`, `JobHandlers`, `EventStore`,
`JobEnqueuer`, `WebhookSubscriptions`, `WorkflowLookup`, `OutboundMarker`,
`OutboundMessage`, `PolishInput`, `PolishResult`, `ImageInput`, `AIProcessor`,
`Destination`, `DestinationType`, `SourceRef`, `PublishRequest`,
`PublishResult`, `PublishOutcome`, `PublishingService`.

Extend your own package's types freely. To change this file, tell the
orchestrator first.

### Ordered steps

1. **A** — persistence for events/jobs/recovery state in
   `internal/channelstore/automation_*.go`, then `internal/automation/events`,
   `queue`, `recovery`.
2. **B** — `internal/channelstore/webhook*.go`, then signing, SSRF transport,
   payload envelope, delivery engine and history in `internal/automation/webhooks`.
3. **C** — extract the publishing service from `internal/gateway/publish.go`
   into `internal/automation/publishing`, then the workflow store, engine and
   loop prevention.
4. **D** — `internal/automation/ai`: Chat Completions client, default prompt,
   structured output validation, optional image input.
5. **E** — `web/` automation routes, forms, histories, navigation, tests.
6. **Orchestrator** — gateway routes and permissions, runtime wiring, config
   docs, API documentation, final verification.

### Must not change

- Migrations 1–7 and every v0.0.2 behaviour.
- `internal/rpc`'s wire contract, `internal/tgapi`'s domain types.
- The public shape of the manual publishing and WordPress routes.
- `docs/api/generated/**` (regenerate, never hand-edit).
- Client isolation rules in `internal/profile`.

## E. Test inventory

Each workstream writes its own tests before its implementation, per
`.agents/skills/tdd-go/SKILL.md`. The full inventory is the v0.0.3 directive
§35; the load-bearing set is:

| Area | Behaviours that must be covered |
| --- | --- |
| events | one event per message; duplicate update rejected; two accounts one event; different networks distinct; media metadata preserved; activation ignores history; recovery in order; restart keeps jobs; unrecoverable gap reported |
| queue | atomic claim; lease expiry and reclaim; shutdown; backoff; max attempts; cancellation; per-workflow ordering; concurrency across channels; uniqueness; no infinite retry |
| webhooks | CRUD; multi-source; payload; method; bearer; custom header; HMAC; per-attempt timestamp and signature; 2xx; 429; 5xx; permanent 4xx; timeout; request/response history; redaction; truncation; SSRF; redirect and rebinding; idempotent delivery |
| workflows | one per channel; duplicate refused; disabled does not publish; one run per event; multiple destinations; publisher reuse; media preserved; no re-publish; partial failure; loop prevention; distinct client keys; queued runs after config change |
| ai | disabled → source content; default prompt; custom prompt; output validation; invalid output fails; no publish when fallback off; publish original when on; reuse polished output on retry; images excluded by default; included when set; never removes destination media; timeout and token limits; injection cannot move destinations; no secret leakage |
| frontend | forms; auth mode switching; masked secrets; channel selection; delivery history; request/response tabs; retry; unique source; destinations; AI defaults; persistence; run history; RTL; responsive; permission-based nav |

## F. Red-phase evidence

The release is implemented continuously under the approval recorded in §A, so
Green runs alongside Red for each workstream rather than after a single frozen
Red snapshot. Each agent records its own first failing run for each behaviour
in its package's notes and reports it in §J. The foundation written before
dispatch (`internal/automation`, `internal/appconfig/automation.go`, migrations
8–15, three permission constants) compiles and leaves `go test ./internal/
channelstore ./internal/appconfig` green, which is the correct result for
schema and type additions that no behaviour depends on yet.

## G. Implementation instructions

1. Verify the approval in §A. If it is not there, stop and report
   `AWAITING_APPROVAL`.
2. Read `AGENTS.md`, this file, and `.agents/skills/tdd-go/SKILL.md`.
3. Work only inside your ownership rows in §D.
4. Write the failing test first for each behaviour, then the smallest change
   that makes it pass.
5. Do not run repository-wide `go test ./...` while other agents are working.
   Run your own packages; the orchestrator runs the full suite once at
   integration.

## H. Validation

The orchestrator runs, once, after integration:

- `gofmt -l .`
- `go vet ./...`
- `go test ./... -count=1`
- `go test -race ./internal/auth ./internal/channelstore ./internal/runtime -count=1`
- `go build ./cmd/telesync`
- `go run ./cmd/docsgen --check` and `go test ./internal/apidocs`
- `cd web && npm run typecheck`
- `cd web/apps/web && npm test`

## I. Progress

- [x] Discovery completed
- [x] Plan written
- [x] Tests written (§E)
- [x] Approved by user
- [x] Implementation started
- [x] Targeted tests passed
- [x] Regression tests passed
- [x] Validation complete → status `DONE`

## J. Final notes

Run at integration, on 2026-11-06, from the worktree root:

| Check | Result |
| --- | --- |
| `gofmt -l .` (excluding `bin/`) | clean |
| `go vet ./...` | clean |
| `go test ./... -count=1` | green except `internal/worker` (see below) |
| `go test -race ./internal/auth ./internal/channelstore ./internal/runtime` | green |
| `go build -o /tmp/telesync-bin ./cmd/telesync` | built |
| `go run ./cmd/docsgen --check` | "documentation is up to date" |
| `go test ./internal/apidocs` | green |
| `cd web && npm run typecheck` | 2 tasks successful |
| `cd web/apps/web && npm test` | 426 tests, 0 failures |

**`internal/worker` is the one failure and it is environmental, not a
regression.** `TestListenPreparesTheSocketPath` and `TestListenRemovesADeadSocket`
fail with `bind: invalid argument` because the socket path they build is 112
bytes and macOS's `sun_path` is 104. The package has no changes in this release
(`git status --short internal/worker/` is empty) and the file dates from the
initial commit.

**Deviations from the plan, all deliberate and recorded:**

- The orchestrator wrote migrations 8–15 up front rather than routing every
  schema request through one agent. One writer, no competing migrations — the
  intent of the ownership rule — with far less blocking.
- `created_by` is nullable. The operator token is a credential and not an
  account; a webhook or workflow made through it has no owner. It still cascades
  with its owner when there is one, and a workflow cannot be switched on without
  one, because publishing runs under that account's authority.
- The three route shapes that need an id only a real message can produce
  (`deliveries/{delivery_id}`, its `/retry`, and `runs/{run_id}`) carry `Skip`
  in the route table, so the generator documents them without inventing an
  exchange. The probes script only the network, as they already do for
  WordPress.
- `internal/docsgen`'s permission extraction was repaired: it scanned
  `internal/gateway` for constants declared in `internal/channelstore`, so
  `facts.json`'s `permissions` had been `null` and no permission was documented.

**Not verified, and not claimed:** nothing was sent to Telegram, WordPress, an
AI provider or a real webhook receiver; no browser session was available for the
panel; live reconnect recovery was not exercised.
