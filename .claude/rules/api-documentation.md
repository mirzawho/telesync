---
paths:
  - "internal/gateway/**"
  - "internal/rpc/**"
  - "internal/apidocs/**"
  - "internal/docsgen/**"
  - "cmd/docsgen/**"
  - "docs/api/**"
---

# API contract and docs

When a route, method, request/response field, status, permission, limit, or
pagination rule changes, update `internal/apidocs/table.go`, run
`go run ./cmd/docsgen`, document generator gaps in
`docs/api/manual/components/`, and run `go run ./cmd/docsgen --check` plus
`go test ./internal/apidocs`. Generated pages are not hand-edited.

Every resource route must apply `gateway.visible()` (deny by default, explicit
deny wins, writes require view). Resolve Telegram peer ids to local ids before
permission checks. Read routes must not write or publish.
