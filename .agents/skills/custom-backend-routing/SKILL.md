---
name: custom-backend-routing
description: Preserve custom MTProto endpoint/key isolation across every dial path and prove it with tests.
---

# Custom-backend routing

Load when changing `internal/profile`, `routing`, `keys`, media/CDN behavior,
reconnects, or custom endpoint/key handling, or when checking whether traffic
can reach official Telegram.

The custom resolver must rebuild its configured endpoint on every dial and must
ignore server-reported `help.getConfig` addresses. Official and custom profiles
are isolated: no fallback to official hosts, CDN, or bundled RSA keys. Preserve
the configured single-DC/map-all policy and separate sessions/credentials.

## Workflow

1. Read `internal/routing/routing.go`, `internal/profile/profile.go`,
   `internal/keys/keys.go`, and the relevant gotd v0.162.0 source.
2. Test the resolver with its injected dial seam; cover primary, migration,
   media-only, CDN refusal, reconnect and key-selection paths as applicable.
3. Run the focused package tests, then `go test ./...` when practical.
4. Record newly verified dependency behavior in `.agents/memory/GOTD_NOTES.md`
   and binding policy changes in `DECISIONS.md`.

The complete dial-path table is in `references/dial-paths.md`.
