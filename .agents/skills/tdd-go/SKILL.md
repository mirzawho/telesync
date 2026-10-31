---
name: tdd-go
description: How tests are written and run in telesync — red/green/refactor with Go, test seams used here, and the exact commands.
---

# TDD in Go for telesync

## The loop

1. **Red.** Write the test for the next behaviour. Run it. It must fail because
   the behaviour is missing — not because the package does not compile, and not
   because of the environment.
   - If the package has no implementation yet, add the exported API with bodies
     that `panic("not implemented")`. That produces a real red test with the
     intended signature, and proves the test itself is well formed.
2. **Green.** Implement the minimum that satisfies the test.
3. **Refactor.** Only with tests green. Re-run after each meaningful edit.

The `panic("not implemented")` stub trick is what was used for `internal/config`
and `internal/keys`; their red runs are recorded in `.agents/memory/LOG.md`.

## Test seams used in this repository

| Package | Seam | Why |
| --- | --- | --- |
| `config` | `Load(lookup func(string) string)` | no `os.Setenv` races, table-driven cases |
| `routing` | `routing.Options.Dial dcs.DialFunc` | assert the dialed `addr` without opening sockets |
| `check` | `check.Factory` / `check.Session` interfaces | drive every state without a network |
| `login` | `login.Flow` interface | script status/auth outcomes |
| `cli` | `cli.Deps{Lookup, Stdout, Stderr, Runtime}` | full command tests, including exit codes |
| `sessionstore` | real files in `t.TempDir()` | permissions and atomicity are the point |
| `gotdclient` | in-memory `tgtest.Server` over `net.Pipe` | real handshake, no sockets, no credentials |
| `supervisor` | injected `Launcher`, `ReadyProber` and a no-sleep clock | restart/backoff/shutdown without spawning processes |
| `worker` | narrow `worker.Telegram` interface | health, paging and cache behaviour without gotd |
| `gateway` | `gateway.Registry` of fake workers | routing, auth and error mapping |
| `gateway` (permissions) | a real `channelstore.Store` + real `auth.Tokens` | login, per-resource grants, revocation on the next request |

Do not add a mocking framework. The interfaces above are the whole surface.

## Conventions

- External test packages (`package config_test`) so tests use only the public API.
- Table tests with `t.Run(name, ...)` for validation matrices.
- `t.TempDir()` everywhere; never write under the repository.
- Assert on messages/behaviour users see, not on private struct fields.
- One behaviour per test; the test name states the behaviour.
- No `time.Sleep` to "wait for" a connection. Use the ready callback or a
  context with a deadline.
- Tests must pass with no environment variables, no network and no credentials.

## Commands

```bash
gofmt -l .                     # must print nothing
go vet ./...
go test ./...                  # full suite
go test ./internal/routing -run TestPrimary -v
# the in-memory end-to-end test needs no environment at all:
go test ./internal/runtime -run TestDaemonToWorker -v
```

If outbound network or Docker is blocked in your sandbox, say so in the final
report instead of weakening a test.

## Pitfalls seen in this repo

- A test that fails to compile is not a red test. Stub first.
- `telegram.Client.Run` returns `nil` on cancellation; the adapter compensates.
  Tests for cancellation must assert on `ctx.Err()`, not on `nil`.
- `t.TempDir()` per subtest, not per test, when permission assertions run.
- Do not test `docker compose` from a unit test; it is a manual/live step.
- Tests that bind a socket skip where the sandbox denies binding. A skip is not
  a pass: report it as unverified.

## Completion checklist

- [ ] Every new behaviour has a test that was seen failing.
- [ ] `go test ./...` green, `go vet` clean, `gofmt -l .` empty.
- [ ] No test needs credentials, network, or a writable `/data`.
- [ ] Red-phase observations recorded in `.agents/memory/LOG.md`.

## Testing authentication and authorization

- The gateway tests build a real `channelstore.Store` in `t.TempDir()` and the
  real `auth.Tokens`, so a login, a token and a permission change are exercised
  end to end (`internal/gateway/authz_test_helpers_test.go`). Use those helpers
  rather than inventing a fake user store: a fake would not prove that
  permissions are re-read from the database on every request.
- `newEnv` (routing, validation and paging tests) runs as the operator token: a
  caller holding every permission, so those tests keep testing what they were
  written for. A test that is *about* authorization must start from
  `newAccountEnv` and an account with the permissions it means to grant.
- The categories contract tests use `env.grantedCategory` / `grantCategoryOnCreate`;
  the authorization rules live in `perms_test.go`, `users_test.go`,
  `authz_access_test.go` and `auth_test.go`.
- Passwords in tests are hashed with `auth.HashPassword` at creation; compare
  with `auth.VerifyPassword`, never with a stored plaintext.
