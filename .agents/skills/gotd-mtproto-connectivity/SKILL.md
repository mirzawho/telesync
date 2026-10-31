---
name: gotd-mtproto-connectivity
description: Build, debug and verify gotd/td MTProto client connections in telesync — options, Run lifecycle, auth flow, session persistence, and diagnosing handshake failures.
---

# gotd/td MTProto connectivity

Use this skill when adding a client profile, changing connection options,
debugging a failing handshake/authorization, or changing session handling.

Pinned dependency: **github.com/gotd/td v0.162.0**. Read that version's source in
`$(go env GOMODCACHE)/github.com/gotd/td@v0.162.0`, not `main`.

## Files to inspect first

- `internal/profile/profile.go` — where `telegram.Options` is built.
- `internal/gotdclient/client.go` — `Run`, `Verify`, `Login`.
- `internal/check/check.go` — what "connected"/"authorized" mean.
- gotd: `telegram/options.go`, `telegram/connect.go`, `telegram/self.go`,
  `telegram/auth/{flow,status,user}.go`, `session/storage_file.go`.

## Verified facts about v0.162.0 (checked in source)

- `telegram.Options.setDefaults` fills `Resolver` with `dcs.DefaultResolver()`,
  `DC` with 2, `DCList` with `dcs.Prod()`. **Leaving them zero is how the
  official profile stays official.**
- `Client.Run(ctx, f)` calls `f` only after the primary connection is ready
  (after `initConnection` + `help.getConfig`). Reaching `f` therefore proves
  transport handshake, key exchange and session init.
- `Run` returns `nil` when the group ends with `context.Canceled`. Wrap it (see
  `session.Run`) if the caller must distinguish cancellation from success.
- `Client.Self(ctx)` is `users.getUsers([InputUserSelf])`; it returns a 401 error
  for unauthorized sessions. That is the cheapest authorized probe available.
- `client.Auth().Status(ctx)` maps 401 to `Authorized: false` without an error.
- `auth.NewFlow(auth.CodeOnly(phone, codeAuth), auth.SendCodeOptions{}).Run(ctx,
  client.Auth())` handles code + 2FA. It returns `auth.ErrPasswordNotProvided`
  when 2FA is required and the authenticator refuses to supply a password.
- `session.Loader` persists `session.Data` (DC, auth key, salt, config subset)
  through any `session.Storage`. `FileStorage` writes mode 0600 but does not
  create directories or write atomically — `internal/sessionstore` does both.
- `telegram.Options.SessionStorage` is the only supported hook for persistence;
  storing a session in memory silently loses authorization across runs.

## Workflow

1. Write a failing test first (`internal/profile` for options, `internal/check`
   for lifecycle). Run it and confirm the failure is behavioural.
2. Change the smallest thing in `profile.Spec.Options` or the adapter.
3. `go test ./internal/profile ./internal/check ./internal/gotdclient`.
4. If the change affects an in-memory handshake, extend
   `internal/gotdclient/e2e_test.go` and confirm it still passes.
5. Record verified findings in `.agents/memory/GOTD_NOTES.md`.

## Diagnosing failures by stage

| Symptom | Stage | Usual cause |
| --- | --- | --- |
| `dial tcp ... connection refused/timeout` | TCP | wrong host/port, firewall, backend down |
| `transport handshake` | transport | wrong `server.transport`, backend needs Obfuscated2 |
| `key fingerprint not found` | key exchange | RSA public key does not match the server's |
| `initConnection` timeout | session init | server does not implement `help.getConfig` |
| `AUTH_KEY_UNREGISTERED` | auth | session revoked; delete the session file and log in |
| `PHONE_CODE_INVALID` / `PHONE_NUMBER_INVALID` | auth | wrong phone/code, or backend auth differs |
| `ErrPasswordNotProvided` | auth | 2FA enabled; run `login` interactively |

`AUTH_KEY_UNREGISTERED`, `SESSION_EXPIRED`, `AUTH_KEY_DUPLICATED` and
`exchange.ErrKeyFingerprintNotFound` are treated as permanent by gotd: it stops
reconnecting instead of looping.

## Pitfalls

- Do not set `Resolver`/`DCList`/`PublicKeys` "just in case" on the official
  profile — that silently changes which server you talk to.
- Do not put phone numbers, codes or passwords in `telegram.Options.Device`
  strings or log lines.
- Do not treat `Run` returning as success; it also returns on cancellation.
- Do not swap the no-op `UpdateHandler` for one that processes updates, and do
  not add bot tokens. Incoming-update processing and bots are not product
  features.
- Today the adapter performs no send/forward/copy/edit/delete call. When
  publishing is implemented it is a deliberate, tested adapter operation on a
  **destination** connection (`PROJECT_BRIEF.md` §5, `DECISIONS.md` D55–D56) —
  never a side effect of a read path.

## Completion checklist

- [ ] Test written first and observed failing.
- [ ] `go test ./...` passes, including `internal/gotdclient` e2e.
- [ ] `gofmt -l .` and `go vet ./...` clean.
- [ ] No secret appears in output or errors.
- [ ] `.agents/memory/GOTD_NOTES.md` updated if behaviour was newly verified.
