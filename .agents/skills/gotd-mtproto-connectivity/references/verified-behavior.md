# Verified gotd v0.162.0 behavior

- `telegram.Options.setDefaults` supplies `dcs.DefaultResolver()`, DC 2 and
  `dcs.Prod()` when fields are zero; MTProto supplies bundled keys when
  `PublicKeys` is empty. That is why official leaves those fields untouched.
- `Client.Run(ctx, f)` calls `f` only after primary readiness, including
  `initConnection` and `help.getConfig`; cancellation can produce a nil return.
- `Client.Self(ctx)` uses `users.getUsers(InputUserSelf)` and returns 401 for an
  unauthorized session. `Auth().Status` maps 401 to `Authorized:false`.
- The auth flow handles code and 2FA; refusing the password returns
  `auth.ErrPasswordNotProvided`.
- `session.Loader` persists auth state through `session.Storage`; gotd's
  `FileStorage` alone does not create directories or write atomically, so use
  `internal/sessionstore`.
- `tgtest.Server` supports the handshake over a `net.Pipe`; this is the project's
  no-socket integration seam.

For resolver call sites and migration/media/CDN behavior, read the custom
routing skill's `references/dial-paths.md`.

## Failure stages

| Symptom | Stage | Check |
| --- | --- | --- |
| TCP refused or timed out | TCP | Host/port, firewall, backend availability |
| Transport handshake failure | Transport | Configured transport; backend may require Obfuscated2 |
| Key fingerprint not found | Key exchange | Configured RSA public key matches the backend |
| `initConnection` timeout | Session init | Backend implements `help.getConfig` |
| `AUTH_KEY_UNREGISTERED` | Authorization | Session revoked; reauthorize rather than retrying indefinitely |
| Invalid phone/code | Authorization | Input or backend-specific auth requirements |
| `ErrPasswordNotProvided` | 2FA | Interactive auth must provide the password |

`AUTH_KEY_UNREGISTERED`, `SESSION_EXPIRED`, `AUTH_KEY_DUPLICATED`, and
`exchange.ErrKeyFingerprintNotFound` are permanent in gotd; it stops reconnecting
instead of looping. Do not add bot tokens or incoming-update processing; reads
must remain separate from the explicit publishing operation.
