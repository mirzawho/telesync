---
name: gotd-mtproto-connectivity
description: Build and debug gotd v0.162.0 profiles, auth, sessions, and handshake tests.
---

# gotd/td connectivity

Load when adding a client profile, changing `telegram.Options`, debugging a
handshake/authorization failure, or changing session persistence. The pinned
dependency is `github.com/gotd/td v0.162.0`; inspect that cached version, not
`main`.

## Workflow

1. Inspect `internal/profile/profile.go`, `internal/gotdclient/manager.go`,
   `internal/login`, and the matching gotd source.
2. Keep official gotd defaults zeroed and custom routing explicit. A no-op
   update handler is intentional.
3. Use `Run` readiness for transport/MTProto handshake, `Self` or auth status
   for authorization, and the project session store for durable sessions.
4. Test through `tgtest` and `net.Pipe` or the existing interfaces; do not add a
   mocking framework.
5. Run focused tests and record new verified facts in
   `.agents/memory/GOTD_NOTES.md`.

Important distinctions: `Run` may return nil on context cancellation; a 401 is
not an authorized session; session storage must create secure directories and
persist atomically. Detailed v0.162.0 facts are in `references/verified-behavior.md`.
