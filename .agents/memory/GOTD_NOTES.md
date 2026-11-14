# gotd/td notes

## Version

`github.com/gotd/td v0.162.0` (pinned in `go.mod`). Module cache path used for
reading source:
`$(go env GOMODCACHE)/github.com/gotd/td@v0.162.0`.

The library's own `go.mod` declares `go 1.25.0`; this project declares
`go 1.27.3`.

## Source paths inspected

`telegram/options.go`, `telegram/client.go`, `telegram/connect.go`,
`telegram/session.go`, `telegram/invoke.go`, `telegram/pool.go`,
`telegram/sub_conns.go`, `telegram/conn_builder.go`, `telegram/cfg.go`,
`telegram/migrate_to_dc.go`, `telegram/auth/{auth,flow,status,self,user}.go`,
`telegram/dcs/{list,plain,find,prod,resolver,resolver_notjs}.go`,
`telegram/internal/manager/{conn,create}.go`, `crypto/public_keys.go`,
`mtproto/{options,connect}.go`, `exchange/key.go`, `session/session.go`,
`session/storage_file.go`, `transport/{protocol,listener,connection}.go`,
`tgtest/{server,handle,middleware,dispatcher,send,exchange}.go`.

## Verified facts (read in source, v0.162.0)

1. `telegram.Options.setDefaults`:
   `Resolver = dcs.DefaultResolver()`, `DC = 2` if zero, `DCList = dcs.Prod()`
   if zero. `mtproto.Options.setDefaults` installs vendored keys when
   `PublicKeys` is empty. Leaving fields zero **is** the official path.
2. `dcs.Prod()` contains real Telegram IPv4/IPv6 addresses and websocket
   domains; `dcs.Plain` dials `net.JoinHostPort(IPAddress, Port)` from the list
   it is given.
3. `manager.Conn.init` calls `invokeWithLayer(initConnection(help.getConfig))`
   and stores the result. `telegram.Client.onSession` stores it into `c.cfg`.
   `Client.dcList()` then returns `cfg.DCOptions`, so **server-reported
   addresses replace the configured list** — the reason the custom resolver
   ignores its argument.
4. Resolver call sites (grep `.Primary(`, `.MediaOnly(`, `.CDN(`): only
   `conn_builder.go:primaryDC` → `Primary`, `pool.go` `MediaOnly` and `CDN`.
   `invokeSub` and `invokeMigrate` both use `primaryDC`, so they go through
   `Primary`.
5. Migration: `invokeDirect` handles any error type ending in `_MIGRATE`;
   `FILE_MIGRATE`/`STATS_MIGRATE` go to `invokeSub(targetDC)` (a new `Primary`
   dial to another DC); everything else calls `invokeMigrate` →
   `MigrateTo`/`Migrate` → restart with the new DC.
6. `exchange.Client(rsaPublicKeys)` selects the key by fingerprint; a mismatch
   yields `exchange.ErrKeyFingerprintNotFound`, treated as permanent by
   `isPermanentError` (no reconnect loop).
7. `Client.Run(ctx, f)`: calls `f` only after the primary connection's `Ready`
   channel fires (post-`initConnection`/`getConfig`). Returns `nil` when the
   internal group ends with `context.Canceled` — callers must check `ctx.Err()`.
8. `Client.Self(ctx)` = `users.getUsers([InputUserSelf])`; 401 for unauthorized.
9. `auth.Client.Status(ctx)` converts 401 into `Authorized:false, err=nil`.
   `auth.NewFlow(...).Run` handles code + 2FA, returning
   `auth.ErrPasswordNotProvided` when the authenticator refuses to supply one.
10. `session.Loader` stores `session.Data` as JSON `{Version:1, Data:{...}}`;
    `session.FileStorage` writes 0600 but creates no directory and is not atomic.
11. `tgtest.Server` supports the full handshake in-process (`exchange.Server`,
    `initConnection` unwrapping via `tgtest.UnpackInvoke`, `SendVector`,
    `SendErr`). It can serve a `transport.Listener` wrapped around any
    `net.Listener`, which is how the e2e test uses `net.Pipe`.

## Hypotheses (not verified against a real server)

- A typical custom MTProto backend implements `help.getConfig` and returns its
  own `DCOptions`. telesync does not depend on that being true, but a backend
  that omits `help.getConfig` would fail `initConnection` and surface as
  `initConnection`/`invoke` errors.
- Some backends require Obfuscated2 on the transport; `server.obfuscated`
  exists for that but has not been exercised.
- Whether the backend accepts the operator-supplied the client's own app_id/hash rather
  than fixed values is unknown; both are configurable.

## Test findings

- The in-memory e2e test (`internal/gotdclient/e2e_test.go`) completes a real
  key exchange, `initConnection`, `help.getConfig` and `users.getUsers(self)`
  over `net.Pipe`, using a `routing.Resolver` with an injected dialer. It also
  asserts that the persisted session file exists with an auth key.
- `telegram.Client.Run` returning `nil` on cancellation was confirmed by a
  failing test (`TestContextCancellationPropagates`) before the adapter wrapped
  it.

## Verified live against the custom backend (2026-10-23)

Target: `62.220.127.238:30001`, DC 2, intermediate transport, single RSA key.

1. `Options.UpdateHandler == nil` makes gotd set `NoUpdates=true`; every request
   is then wrapped in `tg.InvokeWithoutUpdatesRequest`. The backend answers
   `400 CONNECTION_NOT_INITED` for the wrapped `initConnection`. With a no-op
   handler installed, `invokeWithLayer(initConnection(help.getConfig))` succeeds
   immediately (`this_dc=2`, one DC option). **This is a real, reproducible
   backend behaviour, not a hypothesis.**
2. A bare `help.getConfig` (no `initConnection`) also succeeds on this backend,
   and a bare `initConnection` (without `invokeWithLayer`) fails — so the backend
   requires the `invokeWithLayer` wrapper specifically.
3. `help.getNearestDc` returns `Country=IR ThisDC=2 NearestDC=2`.
4. `users.getFullUser(self)` returns `401 AUTH_KEY_UNREGISTERED` on a fresh
   session, as expected before login.
5. Auth-key generation against this backend took ~11 s (official Telegram ~12.6 s
   in the same environment), so a `-timeout` below ~30 s is too aggressive.
6. `telegram.Client` persisted both sessions (`official.json`, `custom.json`) with
   an auth key before the probe ran.

## Search and join (verified 2026-10-25)

- **A scripted `messages.searchGlobal` answer cannot be observed by the client
  in `tgtest`.** The base dispatcher serves the client's initial
  `invokeWithLayer(initConnection)` correctly, but once that request is answered,
  every later `searchGlobal` on the same connection dies with
  `waitSession: context deadline exceeded` (the connection's `gotConfig` is
  never signalled for a request sent after `init` completes) or is reported as
  `SEARCH_TOO_COMPLEX` when the handler sends an error. Removing the init
  wrapper does not help (`initConnection` must not carry `msg_id 0`).
  `contacts.resolveUsername`, `channels.getMessages`, `channels.joinChannel`,
  `messages.importChatInvite`, `channels.getFullChannel` and `upload.getFile`
  are unaffected, which is why only the text-search merge path has no
  end-to-end scripted test. Guarded instead: a real 5 s deadline around the
  public search, and a direct unit test of the merge step
  (`tgapi.DedupeChannels`).
- `messages.searchGlobal` requires `OffsetPeer` (a nil value panics on encode);
  use `&tg.InputPeerEmpty{}` for the first page.
- `messages.importChatInvite` and `channels.joinChannel` return
  `messages.chatInviteJoinResultBox` wrapping
  `messages.chatInviteJoinResultOk{updates}` — a plain `*tg.Updates` will not
  decode.
- A channel the account has left answers with `channel.Left == true`, even when
  it also appears in the dialog snapshot. `Channel.Left` is the reliable
  pre-join membership signal; `channels.joinChannel` on an already-joined
  channel answers `USER_ALREADY_PARTICIPANT`, so the adapter checks `Left`
  first and skips the call.
- A pending join request is the documented `INVITE_REQUEST_SENT` (400); match
  it by `tgerr.Is`, never by searching message text.
- A numeric channel id is addressable only with its access hash. `channels.
  getChannels` and `contacts.resolveUsername` both return it; `channels.
  getFullChannel` fails with `CHANNEL_PRIVATE` for a channel the account has
  not joined, so enrichment is best-effort.
- Chat avatars download through
  `InputPeerPhotoFileLocation{Big: true, Peer: InputPeerChannel}` via
  `downloader.NewDownloader()`; the bytes are JPEG.

## `channels.leaveChannel` (2026-10-26) — not verified in tgtest

`tg.ChannelsLeaveChannelRequest` carries one `InputChannel`. In
`gotdclient.Manager.LeaveChannel` it is sent through `api.ChannelsLeaveChannel`
(from the `tg.Client` obtained by `Manager.apiClient`), which is the same
client `ChannelsLeaveChannel`-style calls use in `JoinChannel`, so the
transport, the layer wrapper and the session are identical to the verified
join path. `USER_NOT_PARTICIPANT` is mapped to `tgapi.ErrNotAMember`;
`CHANNEL_INVALID`/`CHANNEL_PRIVATE`/`PEER_ID_INVALID` go through
`ClassifyError` → `ErrChannelNotFound`.

Not verified: driving `channels.leaveChannel` through the in-memory
`tgtest.Server`. Every attempt (after `Dialogs`, after `History`, with a
`tg.Updates{}` reply and with a raw `updates#79a8a16e` reply) never reached the
dispatcher handler; the client's ack arrived and it waited for
`"Acknowledged, waiting for result"` forever. A control `channels.joinChannel`
call from `LeaveChannel` failed the same way while `History` right after it
succeeded, so the framing is not specific to `leaveChannel`; it is the
invoke-with-updates path answer routing in tgtest. The worker-level
`Service.Leave` is covered with a fake adapter (`internal/worker/worker_test.go`)
and the gateway with a fake worker. Live verification must be done from an
unrestricted shell against a real account, not claimed from unit tests.

## Attachment download: `upload.getFile` offsets (2026-11-03)

`downloader.NewDownloader().Download(api, loc).Stream(ctx, w)` (what
`gotdclient` used before) has no offset: it only streams from byte 0, so it
cannot serve an HTTP range and it buffers through `limitedWriter` with a 32 MiB
cap. `internal/gotdclient/download.go` now calls `api.UploadGetFile` directly
with `Offset`, `Limit` and `Precise(true)`, in 512 KiB parts, which is exactly
what gotd's `telegram/downloader/master.go` does internally.

- `req.SetCDNSupported(false)` matches gotd's `master{allowCDN: false}` default,
  so no CDN redirect is ever taken — required by the MTProto isolation rule
  (`never silently fall back … CDN`).
- `req.SetPrecise(true)` is what lets an offset and a limit be any size, which
  an arbitrary byte range needs. Without it both must be 4 KiB multiples.
- `tg.PhotoSizeProgressive.Sizes` is a ladder of JPEG *prefix* sizes; the last
  entry is the whole file. `tg.PhotoSize.Size` is the byte length of that
  thumbnail. `MediaSize` now reports the byte size of the largest thumbnail, so
  photos have a size like documents do and ranges on them can be answered.
- `FILE_REFERENCE_EXPIRED` / `FILE_REFERENCE_INVALID` on `getFile` is recovered
  by re-reading the message with `channels.getMessages` for a fresh
  `file_reference` and retrying **once** (the second stale reference is a real
  error). Verified in tgtest:
  `TestDownloadMediaRefreshesAnExpiredFileReference`.
- The empty result of a `getFile` past EOF is a zero-length `upload.file`, which
  the streaming reader treats as EOF.

Verified in tgtest against a fake MTProto server (`net.Pipe`, no network), not
against live Telegram: whole-file download, byte ranges, a 1.2 MB file fetched in
three parts, a suffix range, an empty slice past EOF, and the file-reference
refresh.

## Voice and audio documents (2026-11-09)

- A Telegram voice message is a *document*. What makes it a voice message is one
  flag on one attribute: `tg.DocumentAttributeAudio.Voice`, which
  `SetFlags()` encodes as **bit 10** of `flags`. `EncodeBare` calls `SetFlags()`
  itself, so setting the struct field is enough to get the right wire form.
- `tg.DocumentAttributeAudio` also carries `Duration` (seconds, an `int` — never
  a byte count), `Title`, `Performer` and `Waveform []byte` (bitpacked 5-bit
  amplitude values; `SetFlags()` sets bit 2 when it is non-nil).
- `tg.InputMediaUploadedDocument.ForceFile` is `force_file:flags.4?true` — "Force
  the media file to be uploaded as document". **It must be false for both voice
  and audio**, or the recording arrives as a downloadable file whatever the
  attributes say.
- Telegram's own voice messages carry **no** `documentAttributeFilename`. Adding
  one turns a recording into a file that happens to have a name.
- The MIME type decides nothing. `audio/ogg` is what a voice recording is stored
  as and also what an ordinary OGG file is stored as; a file called `voice.ogg`
  is not necessarily a voice message. Only the attribute tells them apart.
- `github.com/gotd/td v0.162.0` does ship a voice-aware builder —
  `telegram/message`'s `AudioDocumentBuilder` with `.Voice()`,
  `.DurationSeconds()`, `.Waveform()`, and `DefaultVoiceMIME = "audio/ogg"`
  (`telegram/message/audio.go`, `mime.go`). This project builds raw
  `tg.InputMediaUploadedDocument` in `internal/gotdclient/send.go` instead; the
  raw construction now produces the same shape that builder would.
- Verified in tgtest against a fake MTProto server: the decoded
  `MessagesSendMediaRequest` arriving at the "destination" carries
  `documentAttributeAudio{voice=true, duration, waveform}` and `ForceFile=false`
  (`TestSendAVoiceMessageCarriesTheVoiceAttribute`). Not verified against live
  Telegram.

## Account-auth session handoff (2026-11-14)

- The account-auth adapter keeps one `telegram.Client` alive across separate
  phone-code/2FA HTTP operations. The account's `AccountBox` is supplied as
  `Options.SessionStorage`; the phone-code hash is stored separately with
  account/key-version-bound encryption context.
- On successful authorization, closing the temporary `Client.Run` completes
  before the permanent account Runner opens the same AccountBox. A
  `tgtest.Server` integration test verifies that the permanent gotd client can
  load the saved session and reports the same authorized Telegram user after
  this close boundary.
- Verified against gotd v0.162.0's fake server only; live Telegram/Cat login,
  delivery and handoff remain unverified.
