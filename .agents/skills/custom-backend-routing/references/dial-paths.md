# Custom resolver dial paths

Verified against gotd/td v0.162.0:

| Path | Gotd entry | Required behavior |
| --- | --- | --- |
| Primary and reconnect | `Resolver.Primary(ctx, dc, list)` | configured endpoint; foreign DC refused under `single-dc` |
| `FILE_MIGRATE` / `STATS_MIGRATE` | `Primary` through `invokeSub` | refused unless `map-all` permits it |
| `FLOOD` / `PHONE_MIGRATE` / `NETWORK_MIGRATE` | `MigrateTo` and restart | same configured policy; no official fallback |
| Media-only pool | `Resolver.MediaOnly` | configured endpoint |
| CDN download | `Resolver.CDN` | always an error for the single-endpoint backend |

`initConnection(help.getConfig)` replaces gotd's stored DC list with the
server-reported list. The custom resolver must ignore that argument and rebuild
from configuration. Grep `\.Primary(`, `\.MediaOnly(` and `\.CDN(` when auditing
new gotd versions for additional dial sites.

## Policy and tests

- `single-dc` is the default: auth keys belong to a DC, and mapping a foreign DC
  to a backend that does not implement it hides the real failure. `map-all` is
  opt-in for backends that implement every configured DC.
- Missing or invalid custom endpoint/key configuration is an error
  (`ErrNotConfigured`, `ErrEmpty`, or `ErrInvalid`); never fall back to
  `dcs.Prod()` or gotd's bundled RSA keys. `CDN` must fail. Obfuscation changes
  transport, not the destination.
- A session from another DC must be rejected. `routing.VerifyConfig` reports
  configuration; it must not resolve or dial server-reported addresses.
- Test with the injected `Options.Dial` recorder and assert the actual address.
  For changes to dialing, extend `internal/gotdclient/e2e_test.go` to prove the
  in-memory server's advertised addresses are never used.
