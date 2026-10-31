---
name: custom-backend-routing
description: Keep the custom MTProto profile on its configured endpoint and key through config refreshes, reconnects, media and CDN paths — and prove it with tests.
---

# Strict custom-backend routing

Use this skill whenever you touch the custom profile's endpoint, key, resolver,
transport, or datacenter handling — and whenever you are asked "is it really
talking to my backend?".

## The problem in one paragraph

Setting `telegram.Options.DCList` to your backend is **not** enough. In
gotd/td v0.162.0, `manager.Conn.init` sends `invokeWithLayer(initConnection(
help.getConfig))` and stores the returned `tg.Config`; `Client.onSession` then
calls `c.cfg.Store(cfg)` and `c.dcList()` starts returning
`cfg.DCOptions`, i.e. **whatever addresses the server reported**. A resolver that
reads the list it is given can therefore be steered anywhere after the first
handshake. `internal/routing` ignores that list and rebuilds a one-entry list
from configuration on every dial.

## Files to inspect

- `internal/routing/routing.go` — the resolver; `check`, `listFor`, CDN refusal.
- `internal/profile/profile.go` — `buildCustom`, `customDCList`, `Options`.
- `internal/keys/keys.go` — PEM loading, no fallback to bundled keys.
- `internal/config/config.go` — `server.routing`, `server.transport`,
  `server.obfuscated`.
- gotd: `telegram/conn_builder.go` (`primaryDC`, `dcList`),
  `telegram/session.go` (`onSession`, `saveSession`),
  `telegram/invoke.go` (`*_MIGRATE` handling),
  `telegram/pool.go` (`MediaOnly`, `CDN`), `telegram/dcs/plain.go`.

## Every path that can dial (v0.162.0, verified)

| Path | Resolver method | Behaviour here |
| --- | --- | --- |
| Primary connection, reconnects | `Primary(ctx, dc, list)` | configured endpoint, foreign DC refused under `single-dc` |
| `FILE_MIGRATE`/`STATS_MIGRATE` sub-connections | `Primary(ctx, targetDC, list)` via `invokeSub` | refused unless `map-all` |
| `FLOOD`/`PHONE_MIGRATE`/`NETWORK_MIGRATE` | `invokeMigrate` → `MigrateTo` → restart | refused unless `map-all` |
| `MediaOnly` pool | `MediaOnly(ctx, dc, list)` | configured endpoint |
| CDN download | `CDN(ctx, dc, list)` | always an error |

There is no other dial site: grep for `\.Primary(`, `\.MediaOnly(`, `\.CDN(`
in `telegram/` to re-verify after an upgrade.

## Why `single-dc` is the default

Auth keys are bound to a datacenter. Mapping DC 4 onto a backend that only
implements DC 2 produces a connection the server cannot decrypt correctly, and
obscures the real failure. Refusing with `ErrForeignDC` names the requested DC,
the configured DC and the endpoint, which is actionable.

`map-all` exists for backends that genuinely implement all DC IDs. It is opt-in.

## Workflow for changes

1. Add or adjust a test in `internal/routing/routing_test.go` using the injected
   `Options.Dial` recorder. Tests must assert on the dialed `addr` strings, not
   on internal fields.
2. Implement the minimum change.
3. `go test ./internal/routing ./internal/profile ./internal/gotdclient`.
4. If the change affects real dialing, extend
   `internal/gotdclient/e2e_test.go`: the in-memory `tgtest` server reports a
   datacenter address in `help.getConfig`, and the test asserts every dial went
   to the configured endpoint anyway.
5. Update `.agents/memory/GOTD_NOTES.md` and, if the policy changed,
   `DECISIONS.md`.

## Pitfalls

- Never fall back to `dcs.Prod()` or `mtproto`'s vendored keys when custom
  configuration is missing. Return `ErrNotConfigured`/`ErrEmpty`/`ErrInvalid`.
- Never resolve `help.getConfig` addresses and "just check" them — a DNS lookup
  is harmless, a dial is not. `routing.VerifyConfig` only reports.
- A session file saved for another DC must be rejected
  (`profile.FromClient` (the `custom` branch)), not silently reused: the stored auth key belongs to
  that DC.
- `CDN` must error, not return a Telegram CDN connection.
- Obfuscation changes only the transport wrapper, not the destination. It does
  not make `map-all` safe.

## Completion checklist

- [ ] Red test written for the new routing rule.
- [ ] All resolver methods covered: `Primary`, `MediaOnly`, `CDN`.
- [ ] Test asserts the dialed address, not just an option field.
- [ ] In-memory e2e still shows no dial to any unexpected address.
- [ ] Memory updated with anything newly verified.
