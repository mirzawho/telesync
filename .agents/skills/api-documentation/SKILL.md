---
name: api-documentation
description: Keep docs/api synchronized with gateway routes; generator workflow and checks.
---

# API documentation

Load this skill before changing an HTTP route, method, request/response type,
status, permission, limit, pagination rule, or its API documentation.

## Procedure

1. Change the code and behavior tests first; use the TDD skill for the test loop.
2. Register every route in `internal/apidocs/table.go`.
3. Run `go run ./cmd/docsgen`; it owns `docs/api/generated/` and the composed
   pages. Do not hand-rewrite generated files.
4. Put meaning, limits, partial-failure behavior, and retry guidance the
   generator cannot infer in `docs/api/manual/components/`. Add new components
   to the group page marker.
5. Resolve every entry in `docs/api/generated/unresolved.md` from source facts;
   never guess a missing permission, body, or response.
6. Run `go run ./cmd/docsgen --check` and `go test ./internal/apidocs`.
7. If the served docs are affected, inspect `/docs` or the docs gateway tests.

The generator checks route coverage, links, anchors, examples, schemas, and the
embedded `/docs` tree. Read `docs/api/README.md` for the public contract and
`.claude/rules/api-documentation.md` for the path-scoped invariant. For
add/change/remove route steps and generator limits, read
`references/route-changes.md`; for the served docs and file ownership, see
`docs/api/DEVELOPING.md`.
