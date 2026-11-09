---
task_id: telegram-voice-message-publishing
title: Keep a recorded voice message a voice message when it is published
status: DONE
created: 2026-11-09
updated: 2026-11-09
baseline_commit: 887910fdc9535f456905874a6cedb3068ae7d143
approval: not_requested
---

<!-- Status values: DISCOVERING | PLANNING | WRITING_TESTS | AWAITING_APPROVAL |
     APPROVED | IMPLEMENTING | VERIFYING | DONE | BLOCKED
     approval: not_requested | granted (by <who>, <date>) | blocked -->

# Keep a recorded voice message a voice message when it is published

## A. Metadata

See frontmatter. `approval` stays `not_requested`: no instruction named this
handoff. The work was done end to end in one session on the user's explicit
mission brief, which directed discovery, failing tests, implementation and
verification together — so the usual prepare/handoff/implement split was not
used. This record is written as the channel between sessions so the discovery
and the reasoning are not lost with the conversation.

Baseline `887910f` (`feat: add a demo Kubernetes manifest for telesync`). The
tree also held another session's in-flight work on
`telegram-messages-copy-route` (route move to `POST /messages/copy`); none of it
was touched. See §G.

## B. Requirements

### Problem

A real Telegram voice message republished to another channel arrived as a
generic downloadable file instead of a playable voice message. The bytes
crossed intact (≈1.3 KB, ≈5 s); only the media semantics were lost.

### Desired behavior

1. A source attachment whose document carries `documentAttributeAudio.voice` is
   recognised as `voice`, from that flag and from nothing else.
2. A music file (`voice=false`) stays `audio`; an ordinary upload stays
   `document`; a photo stays a photo.
3. Voice classification, duration, waveform and MIME type survive every
   internal boundary: `tgapi` DTO, worker RPC JSON, the multipart send request.
4. The outgoing `inputMediaUploadedDocument` carries
   `documentAttributeAudio{voice=true, duration, waveform}` and is not sent as a
   file.
5. A recording the sender never named carries no `documentAttributeFilename`.
6. The caller is never asked for a media kind or an `is_voice` flag; the server
   reads it from the source message.
7. Manual publishing (`ارسال این پیام` / the API with `media.mode =
   preserve_source`), background Workflow publishing and the existing publishing
   API all share the one pipeline and all behave identically.
8. Source bytes are read on the source connection and uploaded on the
   destination's; nothing about the source's storage is reused.
9. A destination that refuses the voice send is a failure. Publishing never
   answers a refusal by sending the recording again as a plain document.
10. Nothing is re-encoded; no FFmpeg; no database change.

### Acceptance criteria

- [x] Real source voice messages are detected from the MTProto attribute.
- [x] Voice and regular audio are distinguished by source metadata.
- [x] Voice classification survives `tgapi` / `rpc` / worker boundaries.
- [x] Duration preserved; waveform preserved when present, never invented.
- [x] Original bytes preserved; MIME type preserved.
- [x] Outgoing MTProto media carries the voice attribute.
- [x] Manual publishing, Workflow publishing and the publishing API all preserve it.
- [x] Cross-connection transfer reads source, uploads destination.
- [x] Unsupported destination produces an explicit error; no silent downgrade.
- [x] Normal audio, documents, photos and videos unaffected.
- [x] Regression tests pass; `go build ./...` passes; `web` typecheck passes.
- [x] No duplicate publications (idempotency path re-run under one key).
- [ ] Destination verified in a real Telegram-compatible client — **not
      possible**: no live MTProto credentials in this environment. See §F.

### Out of scope

Frontend rendering and labels, webhooks, workflow configuration forms, AI
prompts, the queue, database schema, gotd/td itself, media transcoding, video /
sticker / gif outgoing shaping (unchanged), WordPress publishing.

## C. Investigation

Graphify first: `graph_stats` at HEAD `887910f` (8803 nodes / 36886 edges / 236
communities, 93% extracted). Traced the pipeline named in the brief through
`query_graph` plus targeted greps for `DocumentAttributeAudio`, `Voice`,
`UploadedDocument`, `preserve_source`, `media_type`.

Verified facts, each read in source:

| Stage | Location | Finding |
| --- | --- | --- |
| Source decode / classification | `internal/gotdclient/channel.go:84-97` | **correct already** — `documentType` returns `"voice"` on `DocumentAttributeAudio.Voice` |
| Source normalization | `internal/gotdclient/dialogs.go:52-72` | dropped duration and waveform; `tgapi.Media` had nowhere to put them |
| Library-independent DTO | `internal/tgapi/tgapi.go:62-75` | no `Duration`, no `Waveform` |
| Wire DTO | `internal/rpc/rpc.go:202-216` | same |
| Worker DTO | `internal/worker/worker.go:434-443`, `internal/gotdclient/updates.go:224` | same |
| **Outgoing kind (root cause 1)** | `internal/automation/publishing/send.go:67-70` | `kind := "document"` for anything that is not a photo — a recording collapsed to a document here |
| **Outgoing name (root cause 2)** | `internal/automation/publishing/send.go:120-131` | fell back to `Label` = «پیام صوتی», so the destination document was *named* «پیام صوتی» — the "Label" in the product owner's screenshot |
| Send contract | `internal/tgapi/send.go:29-44` | `SendMedia` had only `Kind/Name/MimeType/Data`; `Kind` was `photo` or `document` |
| Worker wire → adapter | `internal/worker/send.go:42-52` | copied four fields only |
| **Outgoing MTProto (root cause 3)** | `internal/gotdclient/send.go:59-67` | `inputMediaUploadedDocument` got one attribute, `documentAttributeFilename`, and `ForceFile` set for documents. **No `documentAttributeAudio` was ever written** — this is where the destination learned nothing about what the media was |

So the loss was not one point but a chain: the classification survived the read
side and died at `buildSend`, and even had it survived, `send.go` had no way to
express it.

### Gotd facts (verified against the installed module)

- `github.com/gotd/td v0.162.0`.
- `tg.DocumentAttributeAudio` (`tg/tl_document_attribute_gen.go:1036`):
  `Voice bool`, `Duration int`, `Title`, `Performer`, `Waveform []byte`.
  `SetFlags()` sets **bit 10** for `Voice`, bit 2 for a present `Waveform`;
  `EncodeBare` calls `SetFlags()` itself, so setting the struct field is enough.
- `tg.InputMediaUploadedDocument` (`tg/tl_input_media_gen.go:1237`):
  `ForceFile bool` is `force_file:flags.4?true` — "Force the media file to be
  uploaded as document". **It must be false for voice and audio.**
- The project constructs raw MTProto objects (`api.MessagesSendMedia`), so the
  raw route was kept rather than switching to `telegram/message`. That helper
  does exist and is voice-aware — `telegram/message/audio.go` has
  `AudioDocumentBuilder.Voice()/DurationSeconds()/Waveform()` and
  `DefaultVoiceMIME = "audio/ogg"` — and its shape is what this adapter now
  produces. Recorded in `.agents/memory/GOTD_NOTES.md`.

## D. Implementation

Smallest correct change, in the shared pipeline. No new subsystem, no new RPC
service, no schema change, no new error system.

**Contract (additive, optional fields):**
- `tgapi.Media` + `Duration int`, `Waveform []byte`.
- `tgapi.SendMedia` + `Duration`, `Waveform`; kinds `SendMediaAudio`,
  `SendMediaVoice`.
- `rpc.Media` + `duration,omitempty`, `waveform,omitempty`;
  `rpc.SendMedia` + the same.

**Read side:** `gotdclient.MediaDuration` / `MediaWaveform` /
`mediaAudioAttribute` (new, beside the existing `MediaSize` / `MediaFileName`),
wired into `normalizeMessage`, the `updates.go` DTO and `worker.messageDTO`.

**Pipeline:** `publishing.sendKind` maps the *source* type — `photo`/`voice`/
`audio` pass through, everything else stays `document` as before.
`publishing.sendFileName` returns `""` for a voice recording the sender never
named (the Label fallback produced the «پیام صوتی» filename artifact).

**Destination:** `gotdclient.sendMediaInput` (new) builds
`inputMediaUploadedDocument` with
`documentAttributeAudio{voice, duration, waveform}` for `voice` and `audio`
(`voice` only when the source said so), `ForceFile` **false** for both, and
`ForceFile` exactly as before for `document`/`""`. A filename attribute is added
for `audio` as before, and for `voice` **only** when the sender named the file.

`worker.sendMediaFromWire` carries the two new fields. Nothing else moved.

## E. Tests

Written before the production change. Red captured first
(`go test ./internal/automation/publishing/`):

```
--- FAIL: TestManualPublishingKeepsARecordedVoiceMessageVoice
    voice_test.go:90: kind = "document", want voice — a recorded voice message
    must not go out as "document"
--- FAIL: TestTheCallerIsNeverAskedToSayTheAttachmentIsVoice
--- FAIL: TestWorkflowPublishingKeepsARecordedVoiceMessageVoice
--- FAIL: TestACrossConnectionTransferReadsSourceAndUploadsVoiceOntoTheDestination
--- FAIL: TestAVoiceSendThatIsRefusedIsAFailureNotADocument
--- FAIL: TestOnlyARecordingIsSentAsVoice/a_recorded_voice_message_is_voice
--- FAIL: TestOnlyARecordingIsSentAsVoice/a_music_file_stays_audio
```

`internal/gotdclient` and `internal/worker` additionally failed to build at red
time, on `Duration`/`Waveform` being absent from `tgapi.Media` / `rpc.SendMedia`
— the contract tests for §D could not compile until the fields existed. Stated
rather than hidden: the behavioural red above is the evidence the bug
reproduced.

Brief ↔ test map: A `TestMessageARecordedVoiceKeepsItsVoiceSemantics`;
B `TestMessageAnAudioFileWithoutTheVoiceFlagStaysAudio`;
C `TestMessageAnOrdinaryDocumentStaysADocument`;
D `TestASendCarriesTheVoiceMetadataAcrossTheRPCBoundary`,
`TestAMediaDescriptorKeepsItsVoiceSemanticsOverJSON`,
`TestServiceSendMessageCarriesTheVoiceSemanticsToTheAdapter`;
E `TestSendAVoiceMessageCarriesTheVoiceAttribute` (real fake MTProto server via
`tgtest`, asserting the decoded `MessagesSendMediaRequest`);
F `TestManualPublishingKeepsARecordedVoiceMessageVoice`,
`TestPublishRouteKeepsARecordedVoiceMessageVoice`;
G `TestWorkflowPublishingKeepsARecordedVoiceMessageVoice`;
H `TestACrossConnectionTransferReadsSourceAndUploadsVoiceOntoTheDestination`,
`TestPublishRouteSendsVoiceOntoTheDestinationConnection`;
I `TestAVoiceSendThatIsRefusedIsAFailureNotADocument`;
J `TestOnlyARecordingIsSentAsVoice`, `TestSendMusicCarriesAnAudioAttributeWithoutTheVoiceFlag`,
`TestSendADocumentIsSentAsAFile`, `TestSendAPhotoIsSentAsAPhoto`,
`TestPublishRouteLeavesOtherMediaAlone`.

## F. Verification

**Tests / build (verified):** `gofmt -l internal/ cmd/` empty; `go vet ./...`
clean; `go build ./...` ok; `go test ./...` — every package ok, including the
voice suites in `gotdclient`, `automation/publishing`, `rpc`, `worker` and
`gateway`. `go run ./cmd/docsgen --check` "documentation is up to date";
`go test ./internal/apidocs` ok. `npm run typecheck` and `npm run build` in
`web/` ok.

**Request-level integration (verified):** the outgoing
`MessagesSendMediaRequest` is decoded from the wire in
`TestSendAVoiceMessageCarriesTheVoiceAttribute` and its
`documentAttributeAudio.Voice` is asserted true, `Duration` 5, `Waveform` the
source's own, `ForceFile` false. That is the destination media request, not a
filename check.

**Live destination (NOT verified):** no MTProto test credentials are available
in this environment, so no message was sent to a real channel and the round play
button was never seen. `cat` is reached through the same
`gotdclient.SendMessage` as the official profile — `internal/gotdclient` is the
only gotd adapter and the send path is shared — so the request that reaches
`cat` carries the same attribute. Whether a particular backend honours it is a
destination-server property that cannot be established here. If it rejects the
send, the publication fails with `publish_unsupported` and nothing is sent as a
document; it is never silently downgraded.

## G. Coordination

A peer session was implementing `telegram-messages-copy-route` in this same
working tree while this work ran, moving the publish route to
`POST /api/v1/telegram/{key}/messages/copy` (`internal/gateway/gateway.go`,
`publish.go`, `apidocs/table.go`, `docsgen/*`, `docsite/*`, the generated
pages). None of it was touched, edited or reverted here.

`internal/gateway/publish_voice_test.go` therefore resolves the live publish
path at run time (`/messages/copy`, falling back to `/messages`) and says so in
a comment: the behaviour under test is what the shared publisher does with the
attachment, which is the same behind either name. If the rename settles on one
address, that helper can be collapsed to a constant.

The gateway route is the one place a future change could re-break this: the
voice semantics live in `publishing.buildSend` and `gotdclient.sendMediaInput`,
not in any route.
