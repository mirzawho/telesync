---
task_id: media-download-cut-short
title: A media download is cut off part-way and served as if it were whole
status: DONE
created: 2026-11-05
updated: 2026-11-05
baseline_commit: 45db19d
approval: granted (by user, 2026-11-05)
---

<!-- Status values: DISCOVERING | PLANNING | WRITING_TESTS | AWAITING_APPROVAL |
     APPROVED | IMPLEMENTING | VERIFYING | DONE | BLOCKED
     approval: not_requested | granted (by <who>, <date>) | blocked -->

# A media download is cut off part-way and served as if it were whole

## A. Metadata

See frontmatter. Update `status`, `updated`, and `approval` at every stage
transition. Never set `approval: granted` without an explicit user instruction
naming this task.

## B. Requirements

### Problem

`GET /api/v1/telegram/official/channels/{id}/messages/{id}/media` ends in
`curl: (18) transfer closed with 145091 bytes remaining to read`. That is not an
HTTP error: the response promised `Content-Length: N` and the connection closed
before N bytes arrived. The route announces the length before the first byte
(`internal/gateway/gateway.go:786-789`), so a transfer that stops early is not a
smaller file — it is a broken one.

Three defects make this happen, and two of them are proven rather than inferred.
Both were reproduced with standalone probes against Go 1.27.3 (see §F):

1. **The request budget covers the body read.** `serveTelegram` wraps every
   Telegram route in `context.WithTimeout(r.Context(), s.cfg.RequestTimeout)`
   (`gateway.go:322`), default **30s** (`internal/appconfig/config.go:34`), and
   that context reaches `http.NewRequestWithContext` inside `rpc.Client.Media`
   (`internal/rpc/rpc.go:1176`). In Go a client request's context governs
   reading the response **body**, not just the headers. Probe: a 20-byte
   response streamed over 600 ms under a 120 ms deadline delivered **2 bytes**
   and returned `context deadline exceeded`. This is the user's symptom.

2. **The public server's write deadline is never cleared.** The daemon's server
   runs `WriteTimeout: 120 * time.Second` (`internal/runtime/runtime.go:234`).
   The worker clears its own for media — `internal/rpc/rpc.go:910`,
   `SetWriteDeadline(time.Time{})`, with the comment "A long transfer must
   outlive the server's per-request write deadline" — and the gateway's `media`
   does not. Probe: same 20-byte response under a 100 ms `WriteTimeout`
   delivered **2 bytes** and failed the server write with `i/o timeout`; with the
   deadline cleared all 20 bytes arrived and the client read no error.

3. **A transfer that dies is invisible.** `_, _ = io.Copy(w, stream.Body)`
   discards the failure at both layers (`gateway.go:794`, `rpc.go:931`), and
   `mediaReader.Read` turns a 0-byte `upload.getFile` part into a clean `io.EOF`
   (`internal/gotdclient/download.go:154-155`) even when it has not reached the
   size Telegram reported. Probe: reading an attachment whose reported size is
   30 and whose bytes are 15 returned **15 bytes and no error**.

`Content-Length` comes from the size Telegram itself reports
(`internal/gotdclient/download.go:113-115` ← `MediaSize`, `largestPhoto` in
`internal/gotdclient/channel.go:289-349`), so the promise is not a gateway
invention; the bug is that nothing on the path keeps it.

**Corrected after live verification.** The three defects below are real and were
fixed, but they were not what cut these transfers. A fourth was: the part size
asked of Telegram. See §J — that is the one the owner's log named.

### Desired behavior

- A media download is no longer cut off by the request budget or by the server's
  write deadline. `RequestTimeout` bounds one upstream *call*, not a transfer.
- A transfer that still stops early — the upstream failed, Telegram sent fewer
  bytes than it reported, or a reconnect tore the client down mid-download — is
  reported through the gateway's `Logf` with the byte counts that say how far it
  got. It never looks like a complete download.
- A download is still cancelled when the caller goes away: no goroutine, socket
  or Telegram fetch outlives the request that started it.

### Acceptance criteria

- [x] A media body that takes longer than `gateway.Config.RequestTimeout` to
      arrive is delivered whole (`TestMediaDownloadOutlivesTheRequestTimeout`).
- [x] A media body that takes longer than the server's `WriteTimeout` to arrive
      is delivered whole (`TestMediaDownloadOutlivesTheServerWriteTimeout`).
- [x] A body that fails part-way is reported via `Config.Logf` naming `op=media`
      and the delivered/expected byte counts
      (`TestMediaDownloadReportsATruncatedStream`).
- [x] A body that simply ends early, with no error, is reported the same way
      (`TestMediaDownloadReportsAShortBodyThatDidNotFail`).
- [x] Reading an attachment Telegram reports larger than the bytes it serves
      fails instead of ending cleanly
      (`TestDownloadMediaFailsWhenTelegramSendsFewerBytesThanItReported`).
- [x] **Added after live verification:** every part asked of Telegram has a
      limit that is a 4 KiB multiple and no more than `mediaPart`, so an
      attachment whose size is not a chunk multiple still downloads whole
      (`TestDownloadMediaReadsAnAttachmentWhoseSizeIsNotAChunkMultiple`).
- [x] The publish paths keep the request-timeout bound: `publish.go:349` and
      `publish.go:763` call `stream.ReadAll()` and must not lose it.
- [x] The public HTTP contract is unchanged: same routes, statuses, headers and
      Range semantics; `go run ./cmd/docsgen --check` and
      `go test ./internal/apidocs` are clean.
- [x] Nothing buffers a whole attachment: the streaming shape of D63 is intact.
- [x] **Live verification by the owner (2026-11-05):** the originally failing
      download completes with no truncation.

### Out of scope

- A logging seam for `internal/rpc` (`NewHandler(backend Backend)` has none, and
  adding one is new infrastructure). The gateway's log already carries the byte
  counts that identify a truncated transfer; a multiple of 512 KiB points at a
  `mediaPart` boundary and therefore at the worker/Telegram side.
- Any change to the HTTP contract, to Range parsing
  (`internal/gateway/mediahttp.go`), or to `Accept-Ranges`/`Content-Disposition`.
- `avatar` and `custom_emoji`: both answer from a buffer whose length is the
  buffer (`gateway.go:812-821`), so they cannot be truncated.
- A configurable streaming budget. D63 records "a large transfer is legitimate";
  the bound is the caller's own patience plus `defer cancel()`.
- The pre-existing `internal/worker` socket-path test failures (see §J).

## C. Code discovery

Only the facts needed to implement. Cite paths/symbols; no source dumps.

### Relevant files

| Path | Role |
| --- | --- |
| `internal/gateway/gateway.go` | `serveTelegram` (:299) applies `RequestTimeout` at :322; `media()` (:720-795) parses Range, calls `worker.Media`, sets `Content-Length` at :786, flushes at :793, copies at :794 |
| `internal/gateway/channels.go` | `logf` (:534) — the existing log seam, backed by `gateway.Config.Logf` (:114-115, "Nil discards") |
| `internal/runtime/runtime.go` | the daemon's `http.Server` (:230-236): `WriteTimeout: 120s` |
| `internal/rpc/rpc.go` | `Client.Media` (:1157-1212) builds the streaming request at :1176; `writeMediaStream` (:908-932) clears its own write deadline at :910 and copies at :931; `stream` client has `ResponseHeaderTimeout` but no total timeout (:1081-1087) |
| `internal/gotdclient/download.go` | `DownloadMedia` (:49-121) resolves `end`/`Length`; `mediaReader.Read` (:136-163) fetches 512 KiB parts; `mediaFetcher.part` (:184-226) |
| `internal/gotdclient/channel.go` | `MediaSize` (:293-311), `largestPhoto` (:316-349) — the reported size the promise is built from |
| `internal/gateway/gateway_test.go` | `fakeWorker` (:28-), `newEnv` (:234), `registry` (:205) |
| `internal/gateway/media_delivery_test.go` | the 15 existing media-route tests that must stay green |
| `internal/gateway/media_stream_test.go` | **new** — the four gateway Red tests |
| `internal/gotdclient/media_stream_test.go` | `downloadServer`/`downloadServerReporting`, `startPanelServer`, `managerFor` |

### Symbols and contracts

- `gateway.Worker.Media(ctx, channelID int64, messageID, photoIndex int, slice rpc.MediaSlice) (rpc.MediaStream, error)`
  — three callers: `gateway.go:739` (public media route, streams),
  `publish.go:349` and `publish.go:763` (both `stream.ReadAll()` into a buffer).
- `rpc.MediaStream{MimeType, Filename, Size, Offset, Length, Body}` — `Length` is
  what the route announces as `Content-Length`; `Size` is the whole attachment.
- `gateway.Config{RequestTimeout, Logf, ...}` — `newEnv(t, registry, mutate ...func(*gateway.Config))`
  overrides both from a test.

### Dependencies and call paths

- `serveTelegram` → `context.WithTimeout(r.Context(), RequestTimeout)` → `r.WithContext(ctx)`
  → `routeTelegram` → `media` → `worker.Media(ctx,…)` → `rpc.Client.Media` →
  `http.NewRequestWithContext(ctx,…)` → **body read is governed by that ctx**.
- `media` → `w.Header().Set("Content-Length", stream.Length)` → `WriteHeader` →
  `Flush` → `io.Copy(w, stream.Body)`.
- Worker side: `rpc.handler` → `Service.Media` → `gotdclient.DownloadMedia` →
  `mediaReader` → `mediaFetcher.part` → `upload.getFile` (512 KiB `mediaPart`).

### Graphify findings

Graph refreshed with `graphify . --update --code-only` at `45db19d` (matches
HEAD): 6097 nodes, 24352 edges, 160 communities. Community 29 holds the worker
RPC/streaming symbols (`writeMediaStream`, `Client.Media`, `handler.ServeHTTP`);
community 58 holds `mediaReader`. The call chain above was confirmed against the
graph and then verified line by line in source — Graphify is the map, the source
is the truth.

**Limitation to carry forward:** the semantic pass over 183 non-code files could
not run (no LLM API key in this environment), so `--code-only` was used. Code
extraction is complete; doc/paper/image nodes in the graph are stale. Nothing in
this handoff depends on them.

### Constraints that apply

- **D63** (`.agents/memory/DECISIONS.md:1093`) — attachment bytes cross the
  worker boundary as a stream, never as JSON. It already records that the
  `rpc.Client`'s `stream` client "has no total timeout (a large transfer is
  legitimate), only `ResponseHeaderTimeout` on the shared transport **and the
  caller's context**". The defect is that the caller's context is a 30s request
  budget; the fix makes the media route's caller context say what D63 meant.
  Nothing may buffer a whole attachment; `MediaStream.ReadAll` stays the only
  bounded-buffer path and stays bounded by `MaxSendMediaBytes`.
- Behavior-first TDD per `.agents/skills/tdd-go/SKILL.md`; seams per
  `references/test-seams.md` (fake worker registry, `newEnv`, `t.TempDir()`, no
  mocking framework).
- Reads do not write. `requireChannelView` and client isolation stay untouched.
- No route/field/status/permission change → no `internal/apidocs/table.go` edit
  and no regenerated page. Run `docsgen --check` to prove it.
- Never reword generated pages; never edit an applied migration (none needed).

### Unknowns / risks

- **Which of the three defects cut the user's particular download is not
  established.** The server was not running during diagnosis, so the actual
  `Content-Length` and delivered byte count were never observed. 145091 bytes
  remaining does not reveal the total. All three are real; the first two are
  proven sufficient to produce the exact error, the third is proven to hide it.
- **Three more truncation producers were found in review and are deliberately
  NOT in scope.** Each ends a download early for a reason neither deadline
  causes, and none of them is fixed here — only made visible by §D step 3:
  - `internal/gotdclient/download.go:184-190` with
    `internal/gotdclient/manager.go:630-648` — `mediaFetcher.part` re-acquires
    `apiClient` for **every** 512 KiB part, and `apiClient` returns
    `tgapi.ErrNotConnected` while `detach()` has cleared the client during a
    reconnect. One reconnect window anywhere in a long download fails a later
    part. Rated **high** likelihood.
  - `internal/gotdclient/manager.go:196-237` — the watchdog ticks every
    `DefaultReconnect` (10s) and, when the state is not Ready, cancels the whole
    client generation, tearing down in-flight `upload.getFile` calls. Rated
    **medium**.
  - `internal/gotdclient/download.go:212-223` — `f.refreshed` spans the whole
    stream, so `FILE_REFERENCE_EXPIRED` is retried exactly **once per
    download**; a second expiry fails the read. Rated **medium**.
  Fixing these means reworking the gotd client's reconnect lifecycle and is its
  own task. §D step 4 covers the one producer that truncates with no error at
  all (the 0-byte `io.EOF`), which review rated **high** and called "the
  strongest suspect for a fast, reproducible `curl: (18)`".
- Removing the deadline from the transfer means nothing but the caller bounds
  it. The in-practice bounds are: `ResponseHeaderTimeout` (30s) on the setup, a
  caller that stops reading (its write fails, the handler returns, `defer
  cancel()` cancels the upstream fetch), and a worker socket that closes. A
  pathological worker that drips bytes forever would hold the connection until
  the caller gives up. Accepted, matching D63; a budget knob is out of scope.
  An alternative was proposed in review — a per-chunk stall deadline instead of
  clearing the write deadline — and rejected as speculative machinery with no
  existing pattern in the repo; `rpc.writeMediaStream` already clears it
  outright.
- `context.WithoutCancel` (Go 1.21+) is required; `go.mod` declares Go 1.27.3.
  The pattern is already used in this repo at
  `internal/gateway/local_channels.go:599`.

## D. Implementation plan

Ordered steps a smaller coding model can execute without re-deriving the
architecture.

1. **`internal/gateway/gateway.go` — `Server.media`**, give the upstream call a
   context a long transfer can outlive. Where `worker.Media` is called (today
   :739), derive a stream context first and register its cancel before the call:

   ```go
   // A download is a transfer, not a call: the request budget that answers a
   // listing must not end it mid-body. It is still tied to this handler, so a
   // caller that walks away stops the fetch.
   streamCtx, cancelStream := context.WithCancel(context.WithoutCancel(ctx))
   defer cancelStream()
   stream, err := worker.Media(streamCtx, channelID, messageID, 0, mediaSliceOf(wanted))
   ```

   `context.WithoutCancel` keeps the request-scoped values and drops the
   deadline; `WithCancel` re-ties the lifetime to the handler's return. The
   setup stays bounded: `rpc.Client`'s `stream` transport sets
   `ResponseHeaderTimeout` (`rpc.go:1082`), so a worker that never answers still
   fails within `RequestTimeout`.
   Do **not** touch `publish.go:349` or `publish.go:763` — those call
   `stream.ReadAll()` and must keep the request-timeout bound.

2. **`internal/gateway/gateway.go` — `Server.media`**, let the response outlive
   the server's write deadline. Immediately before `w.WriteHeader(status)` (today
   :789):

   ```go
   // A long transfer must outlive the server's per-request write deadline; the
   // worker clears its own for the same reason (rpc.writeMediaStream).
   _ = http.NewResponseController(w).SetWriteDeadline(time.Time{})
   ```

   `ResponseController.Flush()` does **not** extend the deadline — only
   `SetWriteDeadline` does. This is the step the probe proved sufficient to
   deliver all 20 bytes.

3. **`internal/gateway/gateway.go` — `Server.media`**, report a transfer that did
   not deliver what it promised. Replace `_, _ = io.Copy(w, stream.Body)` with a
   captured `n, err`, and when `err != nil` **or** `stream.Length > 0 && n != stream.Length`,
   call `s.logf` in the existing shape used at `channels.go:534`/`servers.go:125`:

   ```
   gateway: client=%s op=media request_id=%s channel=%d message=%d: truncated after %d of %d bytes: %v
   ```

   Use `err == nil` wording when there was no error (a body that merely ended).
   Do not change the status or the headers — they are already on the wire.

4. **`internal/gotdclient/download.go` — `mediaReader.Read`**, stop calling a
   short attachment the end of the attachment. Replace the unconditional
   `if len(part) == 0 { return 0, io.EOF }` (:154-155) with:
   - `end < 0` (unknown size): keep `io.EOF` — Telegram stopping is the only
     signal there is.
   - `end >= 0 && r.offset < r.end`: return an error, e.g.
     `fmt.Errorf("gotdclient: the attachment ended at %d of %d bytes", r.offset, r.end)`.

   `end` is already clamped to the reported size (`download.go:103-115`) and that
   size is what Telegram itself reports (`channel.go:289-349`), so a 0-byte part
   before `end` can only mean Telegram served less than it advertised. The error
   surfaces through `writeMediaStream`'s copy and then through step 3's log.

5. Run §H.

### Must not change

- The HTTP contract: routes, statuses, and the headers `Content-Length`,
  `Content-Range`, `Accept-Ranges`, `Content-Type`, `Content-Disposition`,
  `X-Content-Type-Options`, `Cache-Control`, plus `X-Telesync-*` on the worker
  side. `internal/gateway/mediahttp.go` (Range parsing, `unsatisfiableRange`,
  `contentDisposition`) is untouched.
- `gateway.Worker` / `rpc.Backend` signatures and `rpc.MediaStream`'s fields.
- The request-timeout bound on every route except the media body read.
- Permission checks (`requireChannelView`), client isolation, and the rule that
  reads do not write.
- The streaming shape of D63: no full-attachment buffer anywhere.

## E. Test inventory

| File | Test name | Purpose | Current result | Run command |
| --- | --- | --- | --- | --- |
| `internal/gateway/media_stream_test.go` | `TestMediaDownloadOutlivesTheRequestTimeout` | a body slower than `RequestTimeout` is delivered whole | FAIL | `go test ./internal/gateway -run TestMediaDownloadOutlivesTheRequestTimeout -count=1` |
| `internal/gateway/media_stream_test.go` | `TestMediaDownloadOutlivesTheServerWriteTimeout` | a body slower than the server's `WriteTimeout` is delivered whole over a real `http.Server` | FAIL | `go test ./internal/gateway -run TestMediaDownloadOutlivesTheServerWriteTimeout -count=1` |
| `internal/gateway/media_stream_test.go` | `TestMediaDownloadReportsATruncatedStream` | a body that fails part-way is logged with `op=media` and both byte counts | FAIL | `go test ./internal/gateway -run TestMediaDownloadReportsATruncatedStream -count=1` |
| `internal/gateway/media_stream_test.go` | `TestMediaDownloadReportsAShortBodyThatDidNotFail` | a body that merely ends early is logged the same way | FAIL | `go test ./internal/gateway -run TestMediaDownloadReportsAShortBodyThatDidNotFail -count=1` |
| `internal/gotdclient/media_stream_test.go` | `TestDownloadMediaFailsWhenTelegramSendsFewerBytesThanItReported` | an attachment shorter than its reported size fails the read instead of ending cleanly | FAIL | `go test ./internal/gotdclient -run TestDownloadMediaFailsWhenTelegramSendsFewerBytesThanItReported -count=1` |
| `internal/gotdclient/media_stream_test.go` | `TestDownloadMediaStreamsAnUnknownSizeAttachmentToEnd` | **guard, already passing** — an attachment with no reported size still ends cleanly, which is the branch §D step 4 changed | PASS | `go test ./internal/gotdclient -run TestDownloadMediaStreamsAnUnknownSizeAttachmentToEnd -count=1` |

Test-only helpers added (no production code touched):
- `fakeWorker.mediaBody func(ctx context.Context) io.ReadCloser` in
  `internal/gateway/gateway_test.go`, used in place of `mediaBytes`. It receives
  the context the gateway passed, so a test can model a body that stops when that
  context is done — which is exactly how the real worker's HTTP body behaves.
  `fakeWorker.Media` now takes that context instead of `_`.
- `pacedBody` and `brokenBody` in `internal/gateway/media_stream_test.go`.
- `downloadServerReporting(t, docBytes, reportedSize, attrs)` in
  `internal/gotdclient/media_stream_test.go`; `downloadServer` now delegates to
  it with `int64(len(docBytes))`, so every existing test is unchanged in
  behaviour.

Must stay green: the 15 tests in `internal/gateway/media_delivery_test.go`, the
media/descriptor tests in `internal/gateway/gateway_test.go`, and the six
`TestDownloadMedia…` tests in `internal/gotdclient/media_stream_test.go`.

## F. Red-phase evidence

- Command: `go test ./internal/gateway -run TestMediaDownload -count=1`
- Exit code: `1`
- Failure excerpt:
  ```
  --- FAIL: TestMediaDownloadOutlivesTheRequestTimeout (0.05s)
      media_stream_test.go:103: body = "0", want "0123456789abcdefghij": the request timeout cut the download short
  --- FAIL: TestMediaDownloadOutlivesTheServerWriteTimeout (0.06s)
      media_stream_test.go:145: read body: unexpected EOF: the write deadline cut the transfer
  --- FAIL: TestMediaDownloadReportsATruncatedStream (0.00s)
      media_stream_test.go:184: the truncation does not name the operation; log:
  --- FAIL: TestMediaDownloadReportsAShortBodyThatDidNotFail (0.00s)
      media_stream_test.go:219: a short body was served as if it were whole; log:
  ```
- Command: `go test ./internal/gotdclient -run TestDownloadMediaFailsWhenTelegramSendsFewerBytesThanItReported -count=1`
- Exit code: `1`
- Failure excerpt:
  ```
  --- FAIL: TestDownloadMediaFailsWhenTelegramSendsFewerBytesThanItReported (0.70s)
      media_stream_test.go:381: reading a short attachment returned 15 of 30 bytes and no error:
      it must fail instead of ending cleanly
  ```
- Why each failure is a genuine Red:
  - `…OutlivesTheRequestTimeout` — 1 of 20 bytes arrived; asserts delivery of a
    transfer that outlives the request budget. Implementation absent.
  - `…OutlivesTheServerWriteTimeout` — the client read ended in `unexpected EOF`;
    asserts delivery past the write deadline. Implementation absent. This is the
    exact signature of `curl: (18) transfer closed with N bytes remaining`.
  - `…ReportsATruncatedStream` — the body and `Content-Length` assertions passed
    (4 of 10 bytes delivered, `Content-Length: 10`), and only the log assertion
    failed: the truncation happens today and is reported by nobody.
  - `…ReportsAShortBodyThatDidNotFail` — same, for the no-error case.
  - `…FewerBytesThanItReported` — 15 of 30 bytes returned with `err == nil`;
    asserts that a short attachment fails the read.
- Environment/unrelated failures: none in these commands. The two
  `internal/worker` socket-path failures in §J are pre-existing and unrelated.

### Supporting probes (not tests; run outside the repo in `/tmp`)

Both mechanisms were reproduced directly before any test was written, on Go
1.27.3, against a real `http.Server`:

- Request-context deadline: 20-byte body streamed over 600 ms, 120 ms request
  deadline → `body read: n=2 err=context deadline exceeded elapsed=121ms`.
- Write deadline: 20-byte body streamed over 1.2 s, 100 ms `WriteTimeout` →
  `delivered=2 read_err=unexpected EOF` and `server write err: i/o timeout`;
  with `SetWriteDeadline(time.Time{})` → `delivered=20 read_err=<nil>`.

## G. Implementation instructions

1. Verify you are authorized: the user explicitly named this handoff as
   approved. If not, stop and report `AWAITING_APPROVAL`.
2. Read `AGENTS.md`, `.agents/workflows/implement-handoff.md`, this file, and
   `.agents/skills/tdd-go/SKILL.md`. Do not re-run repository-wide discovery.
3. Confirm the baseline commit still exists and the working tree is sane;
   re-run the Red command in §F and compare with the recorded evidence.
4. Execute §D in order. Do not rewrite §E tests except to fix genuine test
   bugs, and say so in §J if you do.
5. Run §H validation. Record results in §I and §J.

## H. Validation

- Targeted: `go test ./internal/gateway -run 'TestMedia' -count=1` and
  `go test ./internal/gotdclient -run 'TestDownloadMedia' -count=1`
- Regression: `go test ./...`
- Static: `gofmt -l .`, `go vet ./...`, `go build ./...`
- Contract (prove the API did not move): `go run ./cmd/docsgen --check` and
  `go test ./internal/apidocs`
- Frontend: not touched; `npm run typecheck` in `web/` only if it is.
- Done when: all acceptance criteria in §B are checked, targeted and regression
  tests pass, static checks are clean, and §J records the actual commands run
  with their outcomes.

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

**Live verification found the actual cause, and it was not the deadlines.**
The owner rebuilt and reran the failing download. The new log line did its job
and named a fourth defect — the one that was really cutting these transfers:

```
gateway: client=official op=media ... message=78999: truncated after 0 of 145091 bytes: ... unexpected EOF
gateway: client=official op=media ... message=79000: truncated after 1572864 of 1756960 bytes: ... unexpected EOF
```

Every number fits one rule: **`upload.getFile` is served only in parts whose
`limit` is a multiple of 4 KiB**, and `mediaReader.Read` asked for "what is
left" — almost never one. 145091, 114566, 138858 and 184453 are none of them
multiples of 4096, and each of those four downloads delivered **zero** bytes. The
1756960-byte one delivered three whole 512 KiB parts (1572864) and died on the
tail, where the remainder was 184096 — again not a multiple — and 184096 is
exactly the byte count `curl` reported as remaining. Five for five. The owner's
original report ("145091 bytes remaining") was the same case: a 145091-byte
attachment that delivered nothing.

gotd's own downloader confirms the shape: `telegram/downloader` fixes
`defaultPartSize = 512 * 1024`, sets `precise: true`, and `reader.next` always
asks for `r.partSize` — never the remainder — learning the end from a short
chunk. `precise` relaxes an arbitrary **offset** (a byte range needs one); it
does not make an unaligned **limit** servable.

**Second fix (same task, same handoff).** `internal/gotdclient/download.go`:
`mediaReader.Read` now asks for a limit that is always a multiple of
`chunkAlign` (4 KiB) and never more than `mediaPart`, instead of the exact
remainder, and drops the bytes a part carries past the slice rather than asking
for a size Telegram will refuse. `mediaPart`'s comment claimed `precise` relaxed
the limit; that was the mistaken assumption behind the bug and is corrected.

Test change recorded honestly: `TestDownloadMediaServesAByteRange` asserted the
adapter "only ever asks for the bytes it needs" (`g[0]+g[1] > 4+6`). That
property is unachievable — Telegram will not serve a part smaller than 4 KiB, so
reading six bytes must ask for one. The assertion is replaced by "never asks for
more than one part", which is the line between the granularity Telegram imposes
and dragging the attachment across the wire. The `downloadServer` test double
also now refuses an unaligned or oversized limit (`LIMIT_INVALID`), which is what
made this visible at all; every other test passes against it unchanged.

**Red for the second fix.** Against the strict test double and unmodified
production code:
```
--- FAIL: TestDownloadMediaStreamsTheWholeAttachment     read stream: rpc error code 400: LIMIT_INVALID
--- FAIL: TestDownloadMediaServesAByteRange              read stream: rpc error code 400: LIMIT_INVALID
--- FAIL: TestDownloadMediaStreamsBeyondOneChunk         read stream: rpc error code 400: LIMIT_INVALID
--- FAIL: TestDownloadMediaServesATailRange              read stream: rpc error code 400: LIMIT_INVALID
--- FAIL: TestDownloadMediaReadsAnAttachmentWhoseSizeIsNotAChunkMultiple (both subtests)
```
The four pre-existing tests went red only because the test double was corrected
to model Telegram; they are green again after the fix.

**Implementation results (2026-11-05).** §D executed in order; all four steps
landed exactly as written. Commands run and their outcomes:

| Command | Outcome |
| --- | --- |
| `go test ./internal/gateway -run TestMediaDownload -count=1` | Red before → **ok** after |
| `go test ./internal/gotdclient -run TestDownloadMedia -count=1` | Red before → **ok** after (all seven, including the six pre-existing) |
| `go test ./internal/gateway -count=1` | **ok** (11.3s) |
| `go test ./... -count=1` | **ok** everywhere except the two pre-existing `internal/worker` socket failures below |
| `gofmt -l .` | clean |
| `go vet ./...` | clean |
| `go build ./...` | clean |
| `go run ./cmd/docsgen --check` | "documentation is up to date" — created 0, updated 0, stale 0 |
| `go test ./internal/apidocs -count=1` | **ok** |

Production diff is 2 files and ~30 lines: `internal/gateway/gateway.go`
(stream context, write deadline, truncated-transfer log) and
`internal/gotdclient/download.go` (a 0-byte part before `end` is an error).
No contract change: `docsgen --check` proves the API did not move.

**One test assertion was tightened during Green.** `TestMediaDownloadReportsATruncatedStream`
first asserted only that the log contains `"4"` and `"10"` — trivially
satisfied by unrelated numbers in the same line (`message=24`). It now asserts
`"4 of 10 bytes"`, which is what the test is actually about. Stricter, not
weaker; the Red evidence in §F still holds and was re-run before implementing.

**One guard test was added at review's suggestion.**
`TestDownloadMediaStreamsAnUnknownSizeAttachmentToEnd` was **already passing**
before and after — it is not a Red and was never counted as one. It guards the
exact branch §D step 4 touched: with no reported size there is no length to be
short of, and Telegram stopping must remain a clean `io.EOF` rather than the
error the fix introduces for the sized case.

**An independent test review reproduced the Red suite from a pristine baseline.**
It rebuilt `git archive HEAD` into a scratch directory, copied in only the test
files, and ran them: all five named above are red against unmodified production
code, with the same names, files and assertions this handoff records. Its matrix
added nothing else.

**Trap the reviewer named, worth keeping.** `r.Context()` inside `media` is not
the raw request context — `serveTelegram` already did `r = r.WithContext(ctx)`
at `gateway.go:324`, so it carries the same 30s deadline. `context.WithoutCancel(ctx)`
on the parameter is the correct way to strip it; using `r.Context()` as a
shortcut would reintroduce the bug. `requireChannelView` deliberately stays on
the bounded context, so a permission lookup can never become an unbounded call.

**Further truncation producers found in review, deliberately not fixed.** Beyond
the three in §C, review named two more. Both end a download early and are now at
least *visible* in the new log line, but neither is changed here:
`internal/gotdclient/download.go:203-224` turns per-part Telegram failures
(`FLOOD_WAIT` included — a 60 MB attachment is ~120 `upload.getFile` calls, a
plausible flood target) into read errors; and `internal/rpc/rpc.go:1078-1087`
shares one transport between the JSON and streaming clients, so
`ResponseHeaderTimeout` still bounds the media request's **headers** at
`RequestTimeout` — which is desired, and must not be confused with the body
bound that was removed.

**Adversarial review corrections (folding in before Green).** An independent
review of the diagnosis corrected two statements and confirmed the rest:

- Go arms the write deadline in `conn.readRequest`, so `WriteTimeout: 120s`
  covers **handler time including the `worker.Media` setup**, not only the body
  write. That widens §B defect 2 rather than weakening it — and it still loses
  to the 30s request budget, so defect 1 remains the primary suspect.
- `internal/gateway` does already call `http.NewResponseController(w).Flush()`
  (`gateway.go:793`); what it never calls is `SetWriteDeadline`. `Flush` does
  **not** extend the deadline. The worker's clearing call at `rpc.go:910` sits
  before `WriteHeader` and is correct, because nothing re-arms the deadline
  after `readRequest` — placing §D step 2 there is right.
- The review confirmed the context mechanism from Go 1.27.3 source
  (`transport.go` builds the request ctx with `WithCancelCause`;
  `persistConn.readLoop` watches `treq.ctx.Done()` while the caller reads the
  body), and confirmed that after `WriteHeader` no code on the streaming path
  calls `s.logf` at all — `cfg.Logf` is wired to `log.Printf`/`os.Stderr` in
  `cmd/telesync/runtime.go` and simply never reached.

**Pre-existing, unrelated baseline failures.** `go test ./...` at `45db19d`
passes everything except two tests in `internal/worker`:
`TestListenPreparesTheSocketPath` and `TestListenRemovesADeadSocket`, both
`bind: invalid argument` on a unix socket under `$TMPDIR` on macOS — the path
exceeds the `sockaddr_un` limit. Environmental, not a regression; do not chase
it and do not report it as one.

**Working tree at handoff.** Three files belong to this task:
`internal/gateway/gateway_test.go` (modified),
`internal/gotdclient/media_stream_test.go` (modified),
`internal/gateway/media_stream_test.go` (new). The tree also carries panel build
assets under `internal/panel/files/` that were already dirty before this task
started, and `graphify-out/` refresh output from the mandated discovery step.
Neither is part of this change; leave them alone.

**Not verified.** The user's own download was never reproduced — the daemon was
not running during diagnosis. Human verification is planned: rerun the original
`curl` and confirm the transfer completes. If it still truncates, the new log
line is the next evidence to read; a byte count that is a multiple of 524288
(`mediaPart`) points at a part boundary and therefore at the worker/Telegram
side rather than at either deadline.
