# Current state

**Updated:** 2026-11-05 (media downloads are no longer cut off by the request
budget or the write deadline, and a truncated transfer is now reported).

## Milestone

Telesync is a multi-client MTProto runtime with a supervised worker per client,
JSON HTTP API v1, SQLite channel/category/user/site inventory, permissions, and
an embedded `/admin` panel. The product path is acquire → manage → read → select
→ prepare → publish. Telegram and WordPress publishing is implemented, but not
live-verified; the exact product matrix remains in `PROJECT_BRIEF.md` §9.

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

From an unrestricted shell, build the binary/panel, configure and authorize the
custom destination client, run the daemon, create an administrator, then verify
health, channel reads, Telegram publishing, WordPress publishing, and idempotent
retry. Use real configured values; never invent endpoints or credentials.

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
filters never refetches the channel list. The list is read whole with `listAll`
(capped at 5 × 100) and search + category chips filter **client-side**, so
typing needs no request and no submit button.

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
