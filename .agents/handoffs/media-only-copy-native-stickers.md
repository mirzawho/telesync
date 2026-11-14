---
task_id: media-only-copy-native-stickers
title: Resolve media-only Telegram copies and preserve native stickers
status: BLOCKED
created: 2026-11-14
updated: 2026-11-14
baseline_commit: 375dbce302273d6414fb2b82d5c34819ca64ac82
approval: granted (by user, 2026-11-14)
---

# Resolve media-only Telegram copies and preserve native stickers

## A. Metadata

See frontmatter. This handoff follows `.agents/workflows/prepare-change.md`.
Approved implementation is present in the dirty worktree; unrelated account
work remains interleaved and must be preserved.

## B. Requirements

### Problem

`preserve_source` currently skips retrieval whenever the source's `rpc.Media.Type`
is not in `mediaHasBytes`. `other` is not in that set. `buildSend` then returns
a request with no media; with empty text, validation still permits the request
because the mode is `preserve_source`, and the publisher calls `SendMessage`
with an empty body. A fake destination currently accepts that and the pipeline
reports success without copying the attachment. This is a verified code path,
not a log-derived claim about request `2461939f08ff8800`.

Separately, source classification recognizes `DocumentAttributeSticker` in
`gotdclient.documentType`, but publication's `sendKind` maps all unlisted kinds
to `document`; `sendMediaInput` has no sticker case and refuses kind `sticker`.
The sticker's native semantic attribute therefore does not reach the destination.

### Desired behavior

- For `preserve_source`, use authoritative source MTProto message metadata rather
  than deciding attachment existence from stale `other`/zero-size cache fields.
- Retrieve a supported attachment without a caller-supplied `attachment_id`.
- Permit empty caption with valid media; refuse empty text when no usable media
  exists. Never send an empty fallback message.
- Preserve Photo, Video, Voice, Audio, Document and Album behavior.
- Preserve static WebP, TGS and WebM sticker semantics end-to-end, with
  destination-compatible metadata; unsupported native transport is explicit.
- Propagate typed stage errors, correlate safe diagnostics, retain idempotent
  retry/unknown-outcome semantics and never report a failed or unverified
  publication as success.

### Acceptance criteria

- [ ] Regression reproduces media-only source metadata `other`, `size=0`, empty
  text, omitted `attachment_id`; valid source bytes are published with returned
  destination message ID, or a precise typed source-media error is returned.
- [ ] No empty text fallback is sent if media cannot be resolved or read.
- [ ] Sticker classification and metadata reach actual outgoing MTProto
  constructors; no silent document/photo downgrade.
- [ ] Custom emoji is not silently classified as an ordinary sticker.
- [ ] Source and destination connections remain isolated.
- [ ] Native destination result is verified or publishing returns an explicit
  unsupported/outcome error.
- [ ] Existing media, albums, manual publishing, workflow publishing, edit,
  delete and permission behavior remains intact.
- [ ] API documentation and generated API checks match any public error changes.
- [ ] Live Cat verification is reported only if authorized credentials and a
  non-production test destination are available.

### Out of scope

- Automatic sticker-pack creation/install, conversion, renaming, MIME-only
  classification, hidden retry/fallback, database reset/migration of history,
  unrelated WordPress changes, broad UI redesign, deploy/commit/push.

## C. Code discovery

### Relevant files

| Path | Role |
| --- | --- |
| `internal/automation/publishing/resolve.go` | Resolves managed source row, source worker and live message through `Worker.Message`. |
| `internal/automation/publishing/send.go` | `buildSend` decides if media is copied, downloads through source worker, maps source type to outgoing kind, and creates stable random IDs. |
| `internal/automation/publishing/request.go` | Request validation permits empty text for `preserve_source`; `attachment_id` is optional. |
| `internal/automation/publishing/errors.go` | Existing public error taxonomy and worker failure mappings. |
| `internal/rpc/rpc.go` | Shared `rpc.Media` metadata and `rpc.SendMedia`-related transfer contract; current `Media` has no sticker-specific fields. |
| `internal/gotdclient/channel.go` | `MediaType`/`documentType` classify raw MTProto document attributes. |
| `internal/gotdclient/send.go` | `sendMediaInput` constructs uploaded Photo/Video/Voice/Audio/Document; sticker kind currently reaches unsupported default. |
| `internal/tgapi/send.go` | Library-independent send DTO currently has no sticker kind constant or sticker metadata. |
| `internal/automation/publishing/voice_test.go` | Shared publishing pipeline, source/destination seams and existing voice/album tests. |
| `internal/gotdclient/send_media_internal_test.go` | Tests actual gotd outgoing media constructor and attributes. |
| `docs/api/manual/components/publishing.md` | Existing publishing failure-stage contract. |
| `.agents/memory/STATE.md`, `.agents/memory/DECISIONS.md` | Existing pipeline constraints and media/idempotency decisions. |

### Symbols and contracts

- `(*Service).resolvePublishSource` fetches the source message using the source
  connection and returns that worker on `sourceMessage`.
- `(*Service).buildSend` only calls `source.worker.Media` when
  `mediaHasBytes(source.message.Media.Type)` returns true. Current `other`
  classification returns false, so the media stream is not attempted.
- `sendKind` maps `photo`, `video`, `round_video`, `voice` and `audio`; all
  remaining media currently map to `document`.
- `gotdclient.MediaType` calls `documentType` on `tg.Document`; the latter
  recognizes `DocumentAttributeSticker`, but does not explicitly distinguish
  `DocumentAttributeCustomEmoji`.
- `sendMediaInput` returns `ErrSendUnsupported` for `"sticker"` today.
- `RequestMedia.AttachmentID` is optional and `namedAttachment` only compares a
  provided opaque ID against the resolved source media ID.
- Publishing idempotency is already durable through `PublishRecords`; stable
  request-derived `RandomID` is used, and unknown outcomes are not retried.

### Dependencies and call paths

- HTTP/manual and workflow → shared `publishing.Service` → `resolvePublishSource`
  (`Worker.Message` on source) → `buildSend` (`Worker.Media` on source) →
  `Worker.SendMessage` on destination → gotd `sendMediaInput` →
  `messages.sendMedia`.
- Worker `rpc.Media` and send DTOs are serialized across worker RPC; sticker
  fields must be added and verified across every boundary rather than retained
  only in gotd code.
- Result handling currently accepts a successful worker result that may lack a
  message object; determine what readback is required to prove native sticker
  semantics without broadening non-sticker behavior.

### Graphify findings

- Graphify query identified `sourceMessage` / `resolvePublishSource`,
  `buildSend`, `sendKind`, `sendMediaInput`, `rpc.Media`, and the related
  publishing and gotd test files.
- Stored graph reports 11,333 nodes and zero edges; call relationships are
  unavailable. The source is authoritative.
- Graph snapshot staleness relative to the current dirty tree is unknown; the
  working tree contains extensive user modifications.

### Constraints that apply

- `.agents/workflows/implement-handoff.md` permits production changes only after
  explicit approval naming this handoff.
- Use behavior-first Go tests; preserve custom backend isolation, permissions,
  safe diagnostics, and existing idempotency.
- No generated API page hand edits; run docs generator and API docs checks when
  public contract changes.
- Do not overwrite/revert existing changes. Working tree has numerous modified
  files, including `STATE.md`, `LOG.md`, publishing/RPC/worker code, API docs,
  and generated panel bundles.

### Unknowns / risks

- No request-ID logs were available. The exact stage for the reported deployed
  request is therefore not confirmed from logs.
- The supplied source message 55 was not fetched in this environment; why its
  production MTProto object reached `other` is unknown.
- `size=0` is not inspected by current `buildSend`; it is not the branch
  excluding retrieval. Its role in the reported HTTP response is unverified.
- A live `Worker.Message` fetch is already performed for each publish. It is
  unknown whether it yields the same incomplete metadata or whether an
  existing dedicated refresh/resolve API exists.
- Native standalone sticker publishing to the Cat server may require a
  destination-supported mechanism beyond `InputMediaUploadedDocument` with
  `DocumentAttributeSticker`. This must be validated against installed gotd
  version and authorized destination behavior; do not assume or create packs.
- Source sticker attributes expose an input sticker set that is server-bound;
  destination-safe representation and result readback remain to be verified.
- Live official/Cat credentials and an authorized test destination are not
  available/verified here.
- Existing uncommitted changes may overlap every likely implementation file.

## D. Implementation plan

1. **`internal/automation/publishing/send.go` + pipeline tests** — Resolve
   source media from the live message rather than using incomplete cached type
   as proof of no attachment. Define explicit behavior for unsupported/no-media
   sources; never send captionless text alone. Validate non-empty downloaded
   bytes when the source media requires them. Keep source-worker downloads and
   destination-worker sends.
2. **`internal/gotdclient/channel.go` + classification tests** — Add real
   MTProto-shaped cases for sticker attribute precedence (including formats),
   generic WebP/document/video, and explicit custom emoji handling. Preserve
   existing voice/audio/video priority.
3. **`internal/rpc/rpc.go`, `internal/tgapi/send.go`, worker/RPC serialization
   files and publishing DTO construction** — Add only required semantic sticker
   fields (emoji/alt, dimensions, animation/video/mask, safe set representation)
   and test JSON/multipart propagation. Never carry source-server access hashes
   into destination requests.
4. **`internal/gotdclient/send.go` + wire-shape tests** — Implement only a
   destination-supported native sticker send path for supported formats;
   preserve original bytes/MIME/attributes, do not force-file or downgrade, and
   do not create/install packs implicitly. Return a typed unsupported error when
   the protocol/destination cannot produce native sticker semantics.
5. **Result and RPC error mapping (`internal/automation/publishing`,
   `internal/gateway`, worker/RPC)** — Map known missing/unavailable/unsupported,
   download/upload/forbidden/unavailable/timeout/ambiguous failures to stable
   public error codes/statuses; retain public envelope and request correlation.
   Add stage-safe logs with no tokens, text, access hashes or file bytes.
6. **`docs/api/manual/components/publishing.md` and generator outputs** — Update
   public error contract only if new codes/behavior are introduced; run docsgen
   and apidocs checks rather than hand-editing generated files.
7. **Validation** — Run focused publishing/gotd/RPC tests; then required Go
   tests, vet/build, docs checks and web checks only if frontend changes. Run
   authorized live Cat checks only when test credentials are actually present.

### Must not change

- Do not change channel permissions, WordPress, album semantics unless required
  for shared DTO compatibility, editing/deleting behavior, endpoint/key
  isolation, or existing idempotency semantics.
- Do not auto-create sticker packs, silently downgrade stickers, infer sticker
  kind from filename/MIME, or report a file upload as native sticker success.
- Do not touch or regenerate unrelated user modifications without review.

## E. Test inventory

| File | Test name | Purpose | Current result | Run command |
| --- | --- | --- | --- | --- |
| `internal/automation/publishing/voice_test.go` | media-only `other`, zero-size media, zero-byte media, captionless sticker, ambiguous sticker retry regressions | Unknown `other` fails typed without sending; supported zero-size media does not need attachment ID; empty bytes fail; sticker DTO and idempotency semantics preserved. | PASS (focused publishing package; see §H) | `go test ./internal/automation/publishing -count=1` |
| `internal/gotdclient/send_media_internal_test.go`, `internal/gotdclient/media_sticker_test.go`, `internal/gotdclient/media_voice_test.go` | sticker classification, formats, attributes, destination confirmation | Covers WebP/TGS/WebM, generic media/custom emoji distinction, mask metadata, and unknown outcome without native result. | Focused tests passed before destination-readback enhancement; current package test compilation blocked by unrelated untracked account tests. Production build passes. | `go test ./internal/gotdclient -run 'TestSticker' -count=1` |

## F. Red-phase evidence

- Command: `go test ./internal/automation/publishing -run '^TestMediaOnlyOtherMetadataNeverPublishesAnEmptyMessage$' -count=1`
- Exit code: `1`
- Failure excerpt:
  ```
  TestMediaOnlyOtherMetadataNeverPublishesAnEmptyMessage
  a retrievable media-only source must publish its attachment;
  sends = [{ChannelID:154 Text: Entities:[] Media:<nil> ...}]
  ```
- Command: `go test ./internal/gotdclient -run '^TestStickerUsesNativeStickerAttribute$' -count=1`
- Exit code: `1`
- Failure excerpt:
  ```
  TestStickerUsesNativeStickerAttribute
  sendMediaInput: tgapi: this content cannot be sent to the destination:
  media kind "sticker"
  ```
- Why each failure is a genuine Red:
  - Media-only test exercises a retrievable source stream yet observes the
    current publisher omit media solely because its message DTO says `other`.
    The fake currently reports successful empty send; the test stops before
    accepting that result.
  - Sticker test checks the actual gotd constructor contract, not a filename;
    current outgoing constructor rejects `"sticker"` before native attributes
    could be created.
- These were the initial Red failures before implementation.

## G. Implementation instructions

1. Verify authorization: the user must explicitly approve this handoff by name.
2. Read `AGENTS.md`, `.agents/workflows/implement-handoff.md`, this file, and
   `.agents/skills/tdd-go/SKILL.md`; preserve the dirty working tree.
3. Re-run both Red commands and compare evidence.
4. Execute §D in order, confirm exact gotd version/protocol support, and update
   tests only for genuine test defects.
5. Run §H and record actual validation results in §I/§J.

## H. Validation

- Targeted Red commands above; rerun during implementation.
- Focused regressions: `go test ./internal/automation/publishing ./internal/gotdclient ./internal/rpc -count=1`
- Full: `go test ./... -count=1`, `go vet ./...`, `go build -o ./bin/telesync ./cmd/telesync`
- Formatting: `gofmt -l .`
- API docs if contract changes: `go run ./cmd/docsgen --check` and
  `go test ./internal/apidocs -count=1`
- Web checks only if frontend code changes: `npm run typecheck` in `web/`.
- Live Telegram/Cat verification: only authorized and non-production test
  channel; inspect returned `tg.Message.Media` as a native sticker, not upload
  success.

## I. Progress

- [x] Discovery completed
- [x] Plan written
- [x] Tests written (§E)
- [x] Red verified (§F)
- [x] Approved by user
- [x] Implementation started
- [x] Publishing regressions passed
- [ ] Latest gotd destination-readback tests passed (blocked by unrelated package test compilation)
- [ ] Full validation complete → status `DONE`

## J. Final notes

Implementation summary: live source messages are resolved through the source
worker; supported captionless media can be copied despite zero cached size and
without `attachment_id`; unknown media and zero-byte streams fail before send.
Sticker semantics (format, emoji, animated/video, mask and mask coordinates)
are classified, carried over RPC, and used to build native destination
attributes. Destination results without a native sticker confirmation are
reported as outcome unknown. No source sticker-set identity is transferred.

Verified: `go build ./...`, `go run ./cmd/docsgen --check`,
`go test ./internal/apidocs -count=1`, `git diff --check`, focused publishing
tests, and web media tests passed at the recorded points. The latest publishing
package passed. Full Go tests and vet fail on unrelated account work in the
dirty tree. Current gotd package tests cannot compile because untracked
`internal/gotdclient/account_auth_test.go` refers to missing
`NewAccountAuthFactory` and `AccountAuthFactoryConfig`; preserve that work.
The latest web typecheck is likewise blocked by account-login UI type errors.
Live Telegram/Cat verification is unavailable; protocol fakes are not live
verification. Exact deployed logs and source message 55 subtype remain
unverified. Keep the handoff blocked until these validation blockers are
resolved or explicitly accepted by the user.
