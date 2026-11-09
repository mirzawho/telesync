---
task_id: telegram-messages-copy-route
title: Move the Telegram publish route to POST /messages/copy and retire /messages
status: DONE
created: 2026-11-09
updated: 2026-11-09
baseline_commit: 887910fdc9535f456905874a6cedb3068ae7d143
approval: granted (by user, 2026-11-09 — "این رو انجام بده و تموم بکن" after the
  AGENTS.md approval requirement and this task id were stated in the preceding
  message; D1 confirmed by "متد قدیمی هم ۴۰۴ بشه")
---

<!-- Status values: DISCOVERING | PLANNING | WRITING_TESTS | AWAITING_APPROVAL |
     APPROVED | IMPLEMENTING | VERIFYING | DONE | BLOCKED
     approval: not_requested | granted (by <who>, <date>) | blocked -->

# Move the Telegram publish route to `POST /messages/copy` and retire `/messages`

## A. Metadata

See frontmatter. Baseline `887910f` (`feat: add a demo Kubernetes manifest for
telesync`), working tree at preparation time contains only this task's test
changes plus a refreshed `graphify-out/`. No production code was edited.

## B. Requirements

### Problem

`POST /api/v1/telegram/{key}/messages` is named "send a message" and behaves as
"a copy an existing message". Verified at `publishing/request.go:113-132`:
`Validate` refuses any request whose `source.local_channel_id` or
`source.message_id` is not positive, so the route can never send anything that
did not already exist as a message on a source channel. The body has no way to
carry a file, and the handler reads the source message before it sends
(`resolvePublishSource` → `buildSend`, `publishing/send.go`).

The contract name therefore promises a capability the route does not have, and
leaves no room to add a real send (free text now, uploads later) beside it.

### Desired behavior

Testable statements, each pinned by a test in §E:

1. `POST /api/v1/telegram/{key}/messages/copy` performs exactly what
   `POST /api/v1/telegram/{key}/messages` performs today — same body, same
   response, same error codes, same idempotency behaviour.
2. `POST /api/v1/telegram/{key}/messages` (and every other method at that path)
   answers `404 not_found`, names `/messages/copy` in its message, and reaches
   no worker. No redirect, no alias, no compatibility shim.
3. The route is reachable under the panel's `/admin` mount exactly as at the
   root.
4. `content.publish` is still required; the refusal is still
   `403 publish_forbidden`.
5. An `Idempotency-Key` recorded before the move replays after it: the same key
   and the same body answer `Idempotent-Replay: true` and post nothing.
6. `GET /messages/copy` answers `405 method_not_allowed` with `Allow: POST`.

### Acceptance criteria

- [x] `POST /api/v1/telegram/{key}/messages/copy` returns `200` and copies the
      source message (text, and the attachment when `media.mode` is
      `preserve_source`)
- [x] `POST /api/v1/telegram/{key}/messages` → `404 not_found`, message names
      `/messages/copy`, `worker.callCount() == 0`
- [x] `GET /api/v1/telegram/{key}/messages` → `404 not_found` (not `405`)
- [x] `GET /api/v1/telegram/{key}/messages/copy` → `405 method_not_allowed`,
      `Allow: POST`
- [x] `POST /admin/api/v1/telegram/{key}/messages/copy` → `200`
- [x] actor without `content.publish` → `403 publish_forbidden`, no worker call
- [x] `publish_source_unavailable`, `publish_source_forbidden`,
      `publish_destination_unavailable`, `publish_destination_forbidden`,
      `publish_permission_denied`, `publish_idempotency_conflict`,
      `publish_media_too_large` are unchanged
- [x] same `Idempotency-Key` + same body on the new path →
      `Idempotent-Replay: true`, `worker.sendCalls == 1`
- [x] all 14 `TestWordPressPublish*` pass with no edit
- [x] all `TestBackgroundPublishing*` in
      `internal/automation/publishing/automation_test.go` pass with no edit
- [x] `go run ./cmd/docsgen --check` passes
- [x] `go test ./internal/apidocs` passes
- [x] `npm run typecheck` in `web/` passes
- [x] `go test ./...`, `gofmt -l .`, `go vet ./...`, `go build ./...` pass
- [x] the panel bundle is rebuilt (`make panel`) and the refreshed
      `internal/panel/files/assets/*` is committed

### Out of scope

- **Any change to `internal/automation/publishing`.** `PublishTelegram`,
  `Request`, `RequestSource`, `RequestDestination`, `RequestContent`,
  `RequestMedia`, `RequestOptions`, `TelegramResult`, `Validate`,
  `resolvePublishSource`, `resolvePublishDestination`, `buildSend`,
  `requestHash` and `sourceRequestHash` all stay exactly as they are. See
  §C "Dependencies and call paths" for why each is still live.
- Renaming `publishing.PublishTelegram` to anything copy-shaped. Only the HTTP
  layer moves.
- A `POST /messages/send` route, free-text sending, or file upload. `/messages`
  is deliberately left free for that work.
- Any compatibility shim, alias, `301`/`308` or JSON redirect for the old path.
- Changing the permission from `content.publish`.
- WordPress publishing behaviour.
- CLI/`cmd` changes.
- `TASKS.md`, `.agents/memory/STATE-history-2026.md`, `.claude/worktrees/*`.
- Panel Persian wording (see §C "Decisions", D4).

## C. Code discovery

### Relevant files

| Path | Role today |
| --- | --- |
| `internal/gateway/gateway.go:376-385` | routes `resource == "messages" && len(segments) == 2` to `publishTelegram`, after a POST-only guard |
| `internal/gateway/gateway.go:396-400` | the GET-only gate every remaining resource passes through |
| `internal/gateway/gateway.go:462-468` | near-miss resources get `404 not_found` naming the canonical routes |
| `internal/gateway/publish.go:110-142` | `publishTelegram`: permission → decode → `Validate` → `Idempotency-Key` → `PublishTelegram` |
| `internal/gateway/publish.go:145-174` | `publishWordPress` — untouched |
| `internal/apidocs/table.go:54` | the route table entry `apidocs` checks the gateway against |
| `internal/docsgen/run.go:403` | maps the pattern to handler `publishTelegram` |
| `internal/docsgen/render.go:295-301` | `componentOf` decides which manual paragraphs a page carries |
| `internal/automation/publishing/request.go` | `Request` and `Validate` — the copy shape |
| `internal/automation/publishing/telegram.go:46` | `PublishTelegram` — the pipeline both callers share |
| `internal/automation/publishing/automation.go:24-32` | the automation caller |
| `internal/automation/workflows/publish.go:172` | the workflow engine's call into `Publisher.Publish` |
| `internal/gateway/publish_test.go`, `publish_media_test.go` | the HTTP-level suite (26 path literals) |
| `web/apps/web/app/components/publish-dialog.tsx:521` | the panel's only HTTP caller |
| `web/apps/web/app/lib/publish-draft.ts:784-836` | `TelegramPublishBody` / `buildTelegramPayloads` — body shape, unchanged |

### Symbols and contracts

- `(*Server).publishTelegram(ctx, w, r, key string, worker Worker, requestID string)`
  — `publish.go:111`. Order is load-bearing and must be preserved:
  `actorOf` → `a.auth.Global(channelstore.PermissionContentPublish)` →
  `decodeJSONBodyLimited(w, r, &req, maxPublishBody)` → `publishing.Validate(req)`
  → `req.IdempotencyKey = r.Header.Get("Idempotency-Key")` →
  `s.publisher().PublishTelegram(ctx, actorOfPublish(a), key, req)` → on
  `result.Replayed` set `Idempotent-Replay: true` → `200` with
  `envelope{Data, Meta}`.
- `maxPublishBody = 256 << 10` (`publish.go:23`) — unchanged.
- `publishing.Request.IdempotencyKey string \`json:"-"\`` (`request.go:76`) —
  excluded from the fingerprint by design.
- `publishing.Error{Status, Code, Message}` (`errors.go:48`) — field order is
  load-bearing (the gateway builds it with unkeyed literals via a type alias).
- Error code constants, restated in two places with identical strings:
  `gateway.go:166-191` and `publishing/errors.go:15-38`. The **values** are:

  | Constant | Value |
  | --- | --- |
  | `CodePublishForbidden` | `publish_forbidden` |
  | `CodePublishSourceMissing` | `publish_source_unavailable` |
  | `CodePublishSourceDenied` | `publish_source_forbidden` |
  | `CodePublishDestMissing` | `publish_destination_unavailable` |
  | `CodePublishDestDenied` | `publish_destination_forbidden` |
  | `CodePublishDenied` | `publish_permission_denied` |
  | `CodePublishMediaTooLarge` | `publish_media_too_large` |
  | `CodePublishConflict` | `publish_idempotency_conflict` |

  There is **no** `publish_source_missing`. The gateway's `publishErrorOf`
  (`publish.go:44`) copies `publishing.Error.Code` through verbatim, so no code
  value changes when the path does.

### Dependencies and call paths

`PublishTelegram` has exactly two production callers — verified by grep over
`**/*.go` excluding `.claude/worktrees/`:

- `internal/gateway/publish.go:131` — the HTTP route being moved.
- `internal/automation/publishing/automation.go:32` —
  `(*Service).Publish(ctx, automation.PublishRequest)`, reached from
  `internal/automation/workflows/publish.go:172` via `h.Publisher.Publish(...)`.

So the whole `publishing` package stays live for background workflows. Nothing
in `publishing/` imports the gateway (`errors.go:11-14` states this
deliberately), so moving the route cannot break the automation path.

Idempotency survives the move, verified at the source:

- `requestHash(req)` (`send.go:163-170`) is `sha256(json.Marshal(req))[:16]`.
  `Request` holds `source`/`destination`/`content`/`media`/`options` only; the
  path is not a field and `IdempotencyKey` is `json:"-"`. The fingerprint cannot
  change when the path does.
- The operation key is `clientKey+":"+req.Destination.ChannelID`
  (`telegram.go:77`) — also path-independent.
- `sourceRequestHash` (`send.go:175-182`) is the WordPress fingerprint and is
  untouched.

Graphify findings — graph rebuilt to HEAD `887910f` during this preparation
(8803 nodes / 36886 edges / 236 communities; it had been built at `45db19d`,
447 files behind, which is why the v0.0.3 automation call paths were missing
from it). `.publishTelegram()` resolves to `internal/gateway/publish.go:L111`,
a method on `Server`, calling `Validate` (L125), `.publisher()` + `actorOfPublish`
(L131), `.publishFailure` + `publishErrorOf` (L133), `decodeJSONBodyLimited`
(L121). Community labels in this graph are not meaningful for this change; the
facts above were confirmed by reading the source Graphify pointed at.

### Verified facts that differ from the task brief

These are corrections to the input prompt, each confirmed by reading the source.

1. **`docs/api/examples/README.md:27` names the old route and was not in the
   brief.** Row `| POST | /api/v1/telegram/{key}/messages | — | below |`.
   `internal/apidocs/docs_test.go` enforces this file both ways:
   `TestIndexCoversEveryRoute` requires every table route to appear in it, and
   `TestExamplesAreValidAndIndexed` requires every index row to have a matching
   example. Leaving it stale fails `go test ./internal/apidocs` twice over.
2. **`componentOf` (`internal/docsgen/render.go:297`) is wrong in two ways, not
   one.** `strings.HasSuffix(pattern, "/messages")` today gives
   `{"telegram", "publishing"}` to `POST /api/v1/telegram/{key}/messages`
   **and** to `GET /api/v1/telegram/{key}/channels/{channel_id}/messages` — a
   read-only listing route. Confirmed in the committed page
   `docs/api/generated/telegram/api-v1-telegram-key-channels-channel-id-messages.md`,
   which carries `**Documentation:** [publishing](...)`. Separately it would
   give only `{"telegram"}` to `/messages/copy`. The correct predicate is
   `strings.HasSuffix(pattern, "/messages/copy")`, which fixes both: there is no
   way to select "the publishing route" by a `/messages` suffix without also
   matching the read route. This is the only correct form of the fix, so it is
   in §D, not treated as optional.
3. **Deleting the route arm does not produce a 404.** After removal,
   `POST /messages` falls to the GET-only gate at `gateway.go:396` and answers
   `405 method_not_allowed` with `Allow: GET` — a claim that GET works at a path
   where GET 404s. A dedicated arm before that gate is required. Verified by the
   Red run: `TestOldPublishRouteIsGone` currently sees `200`, and the retargeted
   tests see `405 "Only GET is supported."` at `/messages/copy`, which is that
   same gate.
4. **`publish_media_too_large` is live on this route**, contrary to the brief.
   It is produced in two places on the copy path: `publishing/send.go:59-62`
   (the source attachment read exceeds the limit) and
   `publishing/errors.go:192-193` (`classifySend` when the worker reports
   `rpc.CodeMediaTooLarge`). It is not returned by the handler directly, but a
   caller of this route can receive it.
5. **The WordPress suite is 14 tests, not 3.** The brief named the three in
   `publish_media_test.go` (lines 357, 387, 413); there are 14 `TestWordPressPublish*`
   across `publish_test.go` and `publish_media_test.go`. All 14 pass today and
   none touches an HTTP path, so all 14 must stay green unedited.
6. **`docs/api/README.md` does not name the route** (brief question 3). It only
   describes the `/api/v1/telegram/{key}/...` namespace at lines 19, 25, 67. No
   change needed.

### Decisions

Recorded here because they were open in the brief. The implementation must
follow them; changing one is a new decision for the user.

**D1 — `/messages` answers `404 not_found` with a hint, for every method.**
The brief's hard constraint is 404 ("no shim, no alias, no redirect; the old
route must give 404 not_found"), while its §5 suggested option B (405 with
`Allow: POST`). Option B is not implementable as specified: RFC 9110 §15.5.6
requires the `Allow` header to list the methods the target resource *does*
support. An `Allow: POST` beside a refusal of POST is self-contradictory, and a
client that honours `Allow` retries POST and loops. What option B was reaching
for — telling the caller where the route went — is what this gateway already
does for a misspelled resource (`gateway.go:462-468`, pinned by
`TestMisspelledResourceExplainsTheCanonicalRoute` at `gateway_test.go:943`):
`404 not_found` plus a message naming the canonical path. So: 404, with the
message naming `/messages/copy`, for GET/POST/DELETE alike. This satisfies the
hard constraint and option B's actual intent.

**D2 — rename the handler to `copyTelegramMessage`.** The brief asked; the cost
is one string in `internal/docsgen/run.go:403`. `handlerNamed(proj, h.name)`
resolves that literal against real gateway functions and, when it does not
resolve, writes `The handler `publishTelegram` was not found in the gateway
package.` into the generated docs as an unresolved item — so the coupling is
loud, not silent. Leaving `publishTelegram` (next to `publishWordPress`) to
serve `/messages/copy` would keep the exact confusion the change exists to
remove. The doc comment at `publish.go:110` changes with it. **Scope limit:** no
rename inside `internal/automation/publishing`.

**D3 — `componentOf` predicate becomes `"/messages/copy"`.** See correction 2.

**D4 — panel Persian wording is left alone.** Brief question 4 asked. Verified:
`publish-dialog.tsx` uses «ارسال» throughout — `«ارسال پیام»` (DialogTitle,
:412), `«پیام با موفقیت به N کانال ارسال شد.»` (:366), `«ارسال ناموفق بود.»`
(:379, :383, :527), `«این پیام هنوز آمادهٔ ارسال نیست.»` (:349),
`«بستن بدون ارسال»` (:455), `«متنی که ویرایش کرده‌اید ارسال نیست.»` (:454).
These describe the *operator's* action — send this post to these channels —
which has not changed. `/copy` is a fact about the request body (it must name a
`source`), not about what the operator does. The distinction only becomes
user-visible when a real send route exists. Leave the copy; flag it for that
follow-up.

### Constraints that apply

- Behaviour-first TDD, `.agents/skills/tdd-go/SKILL.md`.
- Route/contract changes require `internal/apidocs/table.go`, `go run
  ./cmd/docsgen`, `go run ./cmd/docsgen --check`, `go test ./internal/apidocs`,
  and gaps documented in `docs/api/manual/components/`. Generated pages are
  never hand-edited.
- `gateway.visible()` and the resource-permission rules are untouched: this
  route's only authorization is the global `content.publish` check
  (`publish.go:115`), which is not a resource route.
- Never leak or invent secrets, phone numbers, access hashes, credentials,
  tokens, or results.
- Reads do not write. This route is a write and stays one.

### Unknowns / risks

- **Panel bundle file names are content-hashed.** `make panel` will rename
  `channel-messages-main-*.js` and rewrite `manifest-*.js`. Expected, but the
  whole `internal/panel/files/assets/` delta must be committed, not just the
  file that contains the path. Verified the old literal is baked into
  `internal/panel/files/assets/channel-messages-main-BLaX3XW9.js` as
  `telegram/${encodeURIComponent(i)}/messages`.
- **`docsgen --check` does not re-embed.** `cmd/docsgen/main.go:66-71` returns
  before `docsgen.Embed` in check mode, so `internal/docsite/files/` is only
  refreshed by a non-check run. `go run ./cmd/docsgen` must actually be run,
  not just `--check`.
- **Unresolved items in generated docs are already at 23** (`docsgen --check`
  reports `unresolved items: 23 (docs/api/generated/unresolved.md)`). If the
  handler rename is done without updating `run.go:403`, this count rises to 24
  and names the missing handler. Treat a rise as a failure.
- **`TestPublishNeedsACredential` and `TestPublishRefusesAnUnknownDestinationConnection`
  pass both before and after**, for a reason worth knowing: both are answered in
  `serveTelegram` before routing (401 on no token, `404 unknown_client` on an
  unknown key). They are not evidence the route moved.
- Not verified here: live Telegram behavior. Everything above is tests, build
  and generator output. No handshake against a real client was performed.

## D. Implementation plan

Ordered so each step leaves the tree compiling. Steps 1–3 are the behavior;
4–7 are the contract and its documentation.

1. **`internal/gateway/gateway.go`** — replace the arm at `:376-385`. Contract:
   two arms, both before the GET-only gate at `:396`:
   ```go
   // /messages is no longer a route; copying a message lives at /messages/copy.
   // Name it here rather than let the GET-only gate below answer 405 for a path
   // that supports no method at all.
   if resource == "messages" && len(segments) == 2 {
       s.fail(w, http.StatusNotFound, CodeNotFound,
           "Unknown resource "+strconv.Quote("messages")+". "+
               "Use /api/v1/telegram/"+key+"/messages/copy to copy a message and "+
               "/api/v1/telegram/"+key+"/channels/{channel_id}/messages to read them.",
           requestID)
       return
   }
   if resource == "messages" && len(segments) == 3 && segments[2] == "copy" {
       if r.Method != http.MethodPost {
           w.Header().Set("Allow", http.MethodPost)
           s.fail(w, http.StatusMethodNotAllowed, CodeMethodNotAllowed,
               "Copying a message changes state and must be sent as POST.", requestID)
           return
       }
       s.copyTelegramMessage(ctx, w, r, key, worker, requestID)
       return
   }
   ```
   Behavior: `/messages` → `404 not_found` for every method, message names
   `/messages/copy`; `/messages/copy` → POST only. `messages` stays in the
   near-miss list at `:462` — it still catches `/messages/{anything-else}`.
   Do not reorder the GET-only gate.

2. **`internal/gateway/publish.go`** — rename `publishTelegram` →
   `copyTelegramMessage` and rewrite the doc comment on `:110` to name
   `POST /api/v1/telegram/{client_key}/messages/copy` and say it copies an
   existing message. The body of the function is not otherwise touched: the
   permission check, `decodeJSONBodyLimited`, `Validate`, `Idempotency-Key`,
   `PublishTelegram`, `Idempotent-Replay` and the `envelope` answer are already
   exactly right.

3. **`internal/docsgen/run.go:403`** — key and handler:
   `"/api/v1/telegram/{key}/messages/copy": {http.MethodPost: h("copyTelegramMessage")}`.
   This must land with step 2; `TestEveryRouteHasAHandler` and the unresolved
   list both fail if the two drift.

4. **`internal/docsgen/render.go:297`** — `strings.HasSuffix(pattern, "/messages")`
   → `strings.HasSuffix(pattern, "/messages/copy")`. Do not widen the match.
   Expected effect on generated pages: `/messages/copy` gains the `publishing`
   paragraph; `GET .../channels/{channel_id}/messages` **loses** it (correction
   2). Both are correct.

5. **`internal/apidocs/table.go:54`** —
   `{Pattern: "/api/v1/telegram/{key}/messages/copy", Methods: []string{http.MethodPost}, Probe: telegramKey}`.
   `Probe: telegramKey` is required or the probe reaches an unknown client.

6. **Hand-written docs.** Each is a path string only:
   - `docs/api/examples/api.http:82` — `POST /api/v1/telegram/official/messages`
     → `.../messages/copy`. The request body is unchanged.
   - `docs/api/examples/README.md:27` — the index row (correction 1).
   - `docs/api/manual/components/publishing.md:28` — the route in prose, and
     add a sentence that a copy names an existing source message and that a
     free-text send is not this route. Keep it short; this paragraph is the
     generator's `publishing` component.
   - `PROJECT_BRIEF.md:210`, `:334`, `:409`.
   - `docs/VERIFYING-MEDIA-PUBLISH.md:303`, `:370`, `:404`, `:468`, `:508` —
     the curl commands.
   Do **not** edit anything under `docs/api/generated/` or
   `internal/docsite/files/` by hand.

7. **Panel.** `web/apps/web/app/components/publish-dialog.tsx:521` —
   `` `/telegram/${encodeURIComponent(clientKey)}/messages` `` →
   `` `/telegram/${encodeURIComponent(clientKey)}/messages/copy` ``.
   `web/apps/web/app/lib/publish-draft.ts` is **not** touched: `TelegramPublishBody`
   and `buildTelegramPayloads` already match `publishing.Request` exactly.
   Then `make panel` and commit the whole `internal/panel/files/assets/` delta.

8. **Regenerate and validate**, in this order:
   ```bash
   go run ./cmd/docsgen
   go run ./cmd/docsgen --check
   go test ./internal/apidocs
   ```
   `go run ./cmd/docsgen` must run before `--check`, and must be the non-check
   form so `internal/docsite/files/` is re-embedded (see risks).

### Must not change

- `internal/automation/publishing/**` — every symbol listed under "Out of scope".
- `publishing.Error`'s field order (`errors.go:46-47` is explicit that the
  gateway builds it with unkeyed literals).
- The error-code **values** in `gateway.go:166-191` and `publishing/errors.go:15-38`.
- The handler's step order in `publish.go`.
- `maxPublishBody`, `PublishStore`, `publisher()`, `actorOfPublish`,
  `publishingRegistry`, `localChannelsOf`.
- `publishWordPress` (`publish.go:145`) and the whole WordPress path.
- `gateway.go:396` (the GET-only gate) and the near-miss list at `:462`.
- `api/v1/telegram/{key}/channels/{channel_id}/messages*` — the read routes.
- `web/apps/web/app/lib/publish-draft.ts`.
- `.agents/memory/STATE-history-*.md`, `TASKS.md`, `.claude/worktrees/**`.

## E. Test inventory

New file `internal/gateway/publish_route_test.go` (package `gateway_test`),
already written and currently failing:

| File | Test name | Purpose | Current result | Run command |
| --- | --- | --- | --- | --- |
| `internal/gateway/publish_route_test.go` | `TestCopyRouteAcceptsPost` | `POST .../messages/copy` → `200`, one worker send, envelope names connection + channel | FAIL | `go test ./internal/gateway -run TestCopyRouteAcceptsPost -count=1` |
| `internal/gateway/publish_route_test.go` | `TestCopyRouteRequiresContentPublish` | actor without `content.publish` → `403 publish_forbidden`, no worker call | FAIL | `go test ./internal/gateway -run TestCopyRouteRequiresContentPublish -count=1` |
| `internal/gateway/publish_route_test.go` | `TestCopyRouteConsumesSourceMessage` | a copy names a source; a sourceless body → `400 invalid_input`, nothing sent | FAIL | `go test ./internal/gateway -run TestCopyRouteConsumesSourceMessage -count=1` |
| `internal/gateway/publish_route_test.go` | `TestCopyRouteIsVisibleUnderAdminMount` | `POST /admin/api/v1/telegram/official/messages/copy` → `200` with `BasePath: "/admin"` | FAIL | `go test ./internal/gateway -run TestCopyRouteIsVisibleUnderAdminMount -count=1` |
| `internal/gateway/publish_route_test.go` | `TestIdempotencySurvivesRouteRename` | same key + same body → `Idempotent-Replay: true`, `sendCalls == 1`; first send reports no replay | FAIL | `go test ./internal/gateway -run TestIdempotencySurvivesRouteRename -count=1` |
| `internal/gateway/publish_route_test.go` | `TestCopyRouteAnswersMethodNotAllowed` | `GET .../messages/copy` → `405`, `Allow: POST`, `method_not_allowed` | FAIL | `go test ./internal/gateway -run TestCopyRouteAnswersMethodNotAllowed -count=1` |
| `internal/gateway/publish_route_test.go` | `TestOldPublishRouteIsGone` | `POST`/`GET`/`DELETE` `/messages` → `404 not_found`, message names `/messages/copy`, `callCount() == 0` | FAIL | `go test ./internal/gateway -run TestOldPublishRouteIsGone -count=1` |

Retargeted in place (path literal only, assertions untouched):

| File | Tests | Count | Current result | Run command |
| --- | --- | --- | --- | --- |
| `internal/gateway/publish_test.go` | `TestPublishNeedsThePublishingPermission`, `TestPublishSendsThePreparedTextThroughTheNamedConnection`, `TestPublishRefusesASourceTheCallerCannotRead`, `TestPublishRefusesASourceMessageThatIsNotThere`, `TestPublishRefusesADestinationTheAccountCannotPostTo`, `TestPublishRefusesADestinationTheCallerCannotSee`, `TestPublishReplaysAConfirmedResultInsteadOfSendingAgain`, `TestPublishRefusesAnIdempotencyKeyUsedForADifferentRequest`, `TestPublishRejectsAnUnknownBodyField`, `TestPublishReadsTheSourceAttachmentAtTheLargestSize`, `TestPublishRefusesAnAttachmentOfAnotherMessage` | 11 | FAIL | `go test ./internal/gateway -run TestPublish -count=1` |
| `internal/gateway/publish_media_test.go` | `TestPublishReadsSourceMediaThroughTheSourceConnectionNotTheDestination`, `TestPublishReadsTheSameSourceMediaWhoeverTheDestinationIs`, `TestPublishRefusesToSendTextAloneWhenTheSourceAttachmentCannotBeRead`, `TestPublishRefusesWhenTheSourceConnectionIsUnavailable`, `TestPublishReportsADestinationFailureSeparatelyFromASourceReadFailure`, `TestPublishOmitsMediaOnlyWhenTheSourceMessageHasNone`, `TestPublishHonoursAnExplicitDetachWithoutReadingTheSourceMedia`, `TestPublishClassifiesASendFailureTheSameHoweverTheWorkerReportedIt`, `TestPublishNamesAFailedMediaSendAsADestinationFailure`, `TestPublishNeverUsesTheDestinationConnectionToResolveAMediaID`, `TestPublishLogsTheSourceConnectionWhenTheAttachmentCannotBeRead` | 11 | FAIL | `go test ./internal/gateway -run TestPublish -count=1` |

Deliberately **not** edited, and green today — the regression set:

| File | Tests | Current result | Run command |
| --- | --- | --- | --- |
| `internal/gateway/publish_test.go`, `publish_media_test.go` | all 14 `TestWordPressPublish*` | PASS | `go test ./internal/gateway -run TestWordPressPublish -count=1` |
| `internal/automation/publishing/automation_test.go` | `TestBackgroundPublishing*` | PASS | `go test ./internal/automation/publishing -count=1` |
| `internal/gateway/gateway_test.go` | `TestMisspelledResourceExplainsTheCanonicalRoute`, `TestUnknownResourceStillGetsAPlain404` | PASS | `go test ./internal/gateway -run 'TestMisspelled|TestUnknownResource' -count=1` |
| `internal/apidocs/*_test.go` | the whole package | PASS | `go test ./internal/apidocs -count=1` |
| `internal/gateway/publish_test.go` | `TestPublishNeedsACredential`, `TestPublishRefusesAnUnknownDestinationConnection` | PASS (both before and after — answered pre-routing) | `go test ./internal/gateway -run 'TestPublishNeedsACredential|TestPublishRefusesAnUnknownDestinationConnection' -count=1` |

## F. Red-phase evidence

- Command: `go test ./internal/gateway -run 'TestCopyRoute|TestIdempotencySurvivesRouteRename|TestOldPublishRouteIsGone' -count=1 -v`
- Exit code: `1` (expected: the copy route is not routed yet and the old one is
  still served)
- Failure excerpt:
  ```
  --- FAIL: TestCopyRouteAcceptsPost (0.03s)
      publish_route_test.go:32: status = 405, want 200
  --- FAIL: TestCopyRouteRequiresContentPublish (0.05s)
      publish_route_test.go:53: status = 405, want 403
  --- FAIL: TestCopyRouteConsumesSourceMessage (0.04s)
      publish_route_test.go:73: status = 405, want 200
  --- FAIL: TestCopyRouteIsVisibleUnderAdminMount (0.04s)
      publish_route_test.go:110: status = 405, want 200 under the /admin mount
  --- FAIL: TestIdempotencySurvivesRouteRename (0.04s)
      publish_route_test.go:129: first status = 405, want 200
  --- FAIL: TestCopyRouteAnswersMethodNotAllowed (0.02s)
      publish_route_test.go:157: GET status = 404, want 405
  --- FAIL: TestOldPublishRouteIsGone (0.03s)
      publish_route_test.go:185: POST /api/v1/telegram/official/messages: status = 200, want 404
  ```
- Whole publish set: `go test ./internal/gateway -run 'TestPublish|TestCopyRoute|TestIdempotencySurvivesRouteRename|TestOldPublishRouteIsGone' -count=1`
  → **29 FAIL**, 2 PASS.

Why each failure is a genuine Red:

- `TestCopyRouteAcceptsPost`, `TestCopyRouteRequiresContentPublish`,
  `TestCopyRouteConsumesSourceMessage`, `TestCopyRouteIsVisibleUnderAdminMount`,
  `TestIdempotencySurvivesRouteRename` — all answer `405 "Only GET is
  supported."`, which is the GET-only gate at `gateway.go:396` catching
  `resource == "messages"` with `len(segments) == 3`. The behavior is absent
  because the route is not registered; production code is unchanged.
- `TestCopyRouteAnswersMethodNotAllowed` — `GET .../messages/copy` reaches the
  near-miss case at `gateway.go:462` and answers `404`; the route's own method
  rule does not exist yet.
- `TestOldPublishRouteIsGone` — the old route still answers `200`. This is the
  deletion itself, unimplemented.
- The 22 retargeted tests — same `405` from the same gate: the behavior exists,
  at the old address only.

Already-passing and investigated (not manufactured into a Red):

- `TestPublishNeedsACredential` and `TestPublishRefusesAnUnknownDestinationConnection`
  pass at the new path. Both are refused in `serveTelegram` before routing:
  no token → `401`, unknown client key → `404 unknown_client`
  (`gateway.go:343-363`). Their assertions hold at any path by construction, so
  retargeting them is harmless and they are not evidence of anything about the
  route. Not weakened.
- All 14 `TestWordPressPublish*` pass — they drive `publishing.PublishWordPress`
  directly and never touch an HTTP path.
- All `TestBackgroundPublishing*` pass — same reason, via
  `publishing/automation.go`.

Environment/unrelated failures: none. Baseline before this preparation was
clean: `go test ./...` 30 packages ok, `go run ./cmd/docsgen --check`
"documentation is up to date", `gofmt -l .` empty, `go vet ./...` clean.

## G. Implementation instructions

1. Verify you are authorized: the user explicitly named this handoff as
   approved. If not, stop and report `AWAITING_APPROVAL`.
2. Read `AGENTS.md`, `.agents/workflows/implement-handoff.md`, this file, and
   `.agents/skills/tdd-go/SKILL.md`. Do not re-run repository-wide discovery —
   §C is current as of `887910f` with the graph rebuilt to match.
3. Confirm the baseline commit still exists and the working tree is sane;
   re-run the Red command in §F and compare with the recorded evidence. The
   expected Red is 29 failing tests in `internal/gateway`, all at
   `405`/`404`/`200` as recorded.
4. Execute §D in order. Do not rewrite §E tests except to fix genuine test bugs,
   and say so in §J if you do.
5. Run §H validation. Record results in §I and §J.
6. Note the corrections in §C "Verified facts that differ from the task brief"
   — especially `docs/api/examples/README.md:27` and the `componentOf`
   predicate. A reviewer will look for both.

## H. Validation

Run in this order; the docsgen pair must precede the apidocs tests.

```bash
gofmt -l .
go vet ./...
go build ./...
go run ./cmd/docsgen
go run ./cmd/docsgen --check
go test ./internal/apidocs -count=1
go test ./internal/gateway -count=1
go test ./internal/automation/publishing -count=1
go test ./... -count=1
cd web && npm run typecheck
make panel
```

Targeted: `go test ./internal/gateway -run 'TestCopyRoute|TestIdempotencySurvivesRouteRename|TestOldPublishRouteIsGone|TestPublish|TestWordPressPublish' -count=1`

Regression that must stay green with no test edit: `TestWordPressPublish*`,
`TestBackgroundPublishing*`, `TestMisspelledResourceExplainsTheCanonicalRoute`,
`TestUnknownResourceStillGetsAPlain404`, `internal/apidocs`.

Static/generator: `gofmt -l .` empty, `go vet ./...` clean, `go build ./...`
clean, `go run ./cmd/docsgen --check` prints "documentation is up to date" and
the unresolved count does not rise above 23.

Done when: every acceptance criterion in §B is checked, targeted and regression
tests pass, static checks are clean, `internal/panel/files/assets/` is rebuilt
and staged, and §J records the actual commands run with their outcomes.

## I. Progress

- [x] Discovery completed
- [x] Plan written
- [x] Tests written (§E)
- [x] Red verified (§F)
- [x] Approved by user
- [x] Implementation started
- [x] Targeted tests passed
- [x] Regression tests passed
- [x] Validation complete → status `DONE`

## J. Final notes

Implemented 2026-11-09, all of §D in order. Nothing in `internal/automation/publishing`
was changed by this work — see the concurrent-work warning below.

### Commands actually run

| Command | Outcome |
| --- | --- |
| `gofmt -l .` | clean |
| `go vet ./...` | clean |
| `go build ./...` | clean |
| `go run ./cmd/docsgen` | `created 1, updated 3, unchanged 80, stale 1`; unresolved items **23** (unchanged from baseline) |
| `go run ./cmd/docsgen --check` | "documentation is up to date" |
| `go test ./internal/apidocs -count=1` | ok |
| `go test ./internal/gateway -count=1` | ok (15.2s) |
| `go test ./internal/automation/publishing -count=1` | ok |
| `go test ./... -count=1` | ok, all 30 packages |
| `npm run typecheck` (in `web/`) | 2 tasks successful |
| `make web-test` | 48 pass, 0 fail |
| `make panel` | 135 files embedded at `internal/panel/files` |

Generated delta from `go run ./cmd/docsgen`: `+ generated/telegram/api-v1-telegram-key-messages-copy.md`,
`~ generated/facts.json`, `~ generated/telegram.md`,
`~ generated/telegram/api-v1-telegram-key-channels-channel-id-messages.md`,
`- generated/telegram/api-v1-telegram-key-messages.md`. The last two are the
`componentOf` correction taking effect: the old page is gone, and the **read**
route's page lost the `publishing` paragraph it had been carrying wrongly.

### Evidence split on the error codes (AGENTS.md: distinguish test from construction)

Pinned by a passing test on this route: `publish_forbidden`,
`publish_source_forbidden`, `publish_source_unavailable`,
`publish_permission_denied`, `publish_destination_forbidden`,
`publish_idempotency_conflict`, `publish_source_media_unavailable`.

Unchanged **by construction, not re-proven by a test**: `publish_destination_unavailable`
and `publish_media_too_large`. Their producing code (`publishing/resolve.go`,
`publishing/send.go`, `publishing/errors.go`) is outside this change's edit set.
A live Telegram handshake was not run, so no code is claimed verified against a
real client.

### One test bit back, and was right

`TestLinkedRoutesExist` (`internal/apidocs/docs_test.go:192`) failed on the
first attempt at the `publishing.md` paragraph:

```
manual/components/publishing.md: /api/v1/telegram/{key}/messages matches no route
```

The prose named the retired path in a code span, which the test reads as a claim
that a route exists. The wording was changed to describe the behaviour ("A
request to the same path with the `copy` segment left off answers
`404 not_found`") rather than the path. The test was **not** weakened, and no
route was added to `internal/apidocs/table.go` to satisfy it.

### WARNING — concurrent work in this working tree that is not part of this change

While this was being implemented, a **voice-message** feature appeared in the
same working tree. It is not this task's work and is not attributed to it. It
touches `internal/automation/publishing/send.go` (adds `sendKind`, `Duration`,
`Waveform`), `internal/automation/publishing/automation_test.go`,
`internal/rpc/rpc.go`, `internal/worker/{send,worker}.go`,
`internal/tgapi/{send,tgapi}.go`, `internal/gotdclient/{channel,dialogs,send,updates}.go`,
and adds `internal/automation/publishing/voice_test.go`,
`internal/gateway/publish_voice_test.go`, `internal/gotdclient/media_voice_test.go`,
`internal/rpc/voice_test.go`. ~236 insertions across 11 modified files.

Two consequences:

1. This handoff's "**Must not change**: `internal/automation/publishing/**`" is
   now violated — **not by this implementation**, which has zero diff in that
   package. A reviewer must not read those edits as part of the route rename.
2. The full `go test ./...` result above was measured **with** that work present
   and is green. Neither change set overlaps the other's source files. Nothing
   was committed; HEAD is still `887910f`.

The reviewer should separate the two diffs before committing.
