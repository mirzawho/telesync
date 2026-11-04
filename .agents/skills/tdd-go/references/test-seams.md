# Telesync test seams

| Area | Seam |
| --- | --- |
| config | `Load(lookup func(string) string)` |
| routing | `routing.Options.Dial` |
| login | `login.Flow` |
| CLI | `cli.Deps{Lookup, Stdout, Stderr, Runtime}` |
| sessionstore | real files under `t.TempDir()` |
| gotdclient | in-memory `tgtest.Server` over `net.Pipe` |
| supervisor | injected launcher, readiness prober and no-sleep clock |
| worker | narrow `worker.Telegram` interface |
| gateway | fake worker registry; real store/tokens for permission tests |

These seams are preferred over global state, network credentials, sockets, or a
new mocking library.

## Authorization and credentials

- `newEnv` in `internal/gateway/gateway_test.go` represents the operator token,
  which has every permission. Use `newAccountEnv` from
  `authz_test_helpers_test.go` for account permission behavior; it uses a real
  `channelstore.Store` and real tokens so each request re-reads current grants.
- Category contract tests use `env.grantedCategory` or
  `grantCategoryOnCreate`; see `categories_test.go` and `authz_access_test.go`.
- Create test accounts with `auth.HashPassword`; verify through
  `auth.VerifyPassword`. Never store or assert against a plaintext password.
