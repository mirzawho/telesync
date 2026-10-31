---
name: api-documentation
description: How docs/api/ is kept in step with the HTTP API — run the generator instead of rewriting pages, what it extracts and what a person must write, how to remove documentation for a deleted endpoint, and the checks that prove the pages and the /docs site match the code.
---

# Maintaining `docs/api/`

`docs/api/` is the reference for the public HTTP API. Most of it is **generated**
from the source by `go run ./cmd/docsgen`; the prose a generator cannot derive
lives in `docs/api/manual/components/`. The daemon serves the same tree as HTML
at `GET /docs`.

## The loop

1. **Code and tests first.** Change the API and its tests as the task requires
   (see [../tdd-go/SKILL.md](../tdd-go/SKILL.md)).
2. **Register a new route in the table.** `internal/apidocs/table.go`
   (`apidocs.Routes`) is the one place that names routes; the generator's
   checks and the coverage probe both read it.
3. **Run the generator.** `go run ./cmd/docsgen`. It rewrites
   `docs/api/generated/`, composes the hand-written pages, and prints what it
   created, updated, left unchanged and removed.
4. **Write what it cannot extract**, in `docs/api/manual/components/<topic>.md`:
   meaning, limits, partial failures, retry advice. Add the component to the
   group pages' `<!-- components: … -->` list if it is a new topic.
5. **Work through `docs/api/generated/unresolved.md`.** It lists every route
   where the generator could not name the response, the permission check or the
   request body, with the evidence it had. Either make the source clearer or
   document the gap by hand; do not guess.
6. **Check.** `go run ./cmd/docsgen --check` must pass and
   `go test ./internal/apidocs` must be green.
7. **Look at the site** when the change is visible there: `GET /docs` on a
   running daemon (it is public), or the tests in
   `internal/gateway/docs_test.go`.

**Do not rewrite the documentation with a model on every task.** The generator
is deterministic; a hand-rewrite of generated pages is reverted by the next run
and hides what actually changed.

## When to do this

Load this skill before changing code if the task touches:

- a route, method or path (`internal/apidocs/table.go`, `gateway.ServeHTTP`);
- a request or response type or its fields (`internal/gateway/*.go`,
  `internal/rpc`);
- validation, authentication, permissions (`gateway/auth.go`, `access.go`,
  `channelstore/permission.go`);
- status codes or error mapping (`gateway.classify`, `rpc.statusForCode`);
- pagination, filtering, caching, or configuration that changes API behaviour
  (`api.request_timeout`, `api.auth_token_env`, `api.jwt.*`, `api.docs_dir`,
  `runtime.avatar_ttl`).

Also load it when asked to write, audit or update the API documentation.

## What the generator extracts

| Fact | Source |
| --- | --- |
| routes and methods | `internal/apidocs/table.go`, proven against the gateway |
| handler, file | the table the generator keeps in `run.go` |
| success body and status | the handler's `writeJSON` call, read from the AST |
| request body type | the decode call in the handler or in `ServeHTTP`'s branch |
| request schema | the Go struct: JSON name, Go type, `omitempty`, pointers, nesting |
| credential and permission conditions | `Global`, `Allowed`, `CanList`, `HasAny`, `requireAuth` calls on the way to the handler, plus the package functions they call |
| observed responses | the route replayed as anonymous, operator, limited user and administrator with an invalid path value — no request in that pass can change state |
| example responses | `docs/api/examples/api.http`, replayed in order |
| global permissions | the constants the gateway's store defines |

What it cannot see: prose, meaning, limits, partial failures, behaviour behind
interface dispatch, live Telegram answers, and the body of a route it cannot
name. Those go in components, or in `unresolved.md` until someone decides.

## Map of `docs/api/`

| Path | Owner |
| --- | --- |
| `generated/` (pages, `facts.json`, `unresolved.md`) | generator; never edited by hand |
| `README.md` | hand-written, with generated endpoint sections |
| `telegram.md`, `local.md` | generated group pages with component sections |
| `telegram/`, `local/`, `identity/`, `schemas/` | generator |
| `examples/api.http`, `examples/README.md` | hand-written; the examples are executed |
| `manual/components/` | hand-written paragraphs |
| `manual/legacy/` | archive, published as it stands, not generated |
| `DEVELOPING.md` | this workflow for a human reader |

## Adding, changing and removing

### A new route

1. Add it to `apidocs.Routes` (with `Probe` values if its variables need a
   known client or document).
2. Add it to the generator's route→handler table in `internal/docsgen/run.go`.
3. Add an example request and index row in `examples/`.
4. Run the generator; it creates the pattern page, the group row and the
   schemas.
5. Write the paragraph it needs, if any.

### A changed route

Run the generator and read its diff: the pages, the schemas and `facts.json`
change together. Update the component that explains the behaviour. A new error
code belongs in the errors component; a new permission in the authentication
component.

### A removed route

1. Remove it from `apidocs.Routes` and from the generator's table.
2. Remove its example and index row.
3. Run the generator: its page, its group row and its schema disappear.
4. Search for the path and repair what remains:
   `grep -rn "/api/v1/<removed>" docs/api`.
5. Re-run the check. Never leave a removed endpoint documented as available; do
   not add a "previously" section unless the repository keeps an API history
   (it does not).

## The checks

```bash
go run ./cmd/docsgen --check     # the committed pages match a fresh run
go test ./internal/apidocs       # routes, links, anchors, examples, offline guard
go test ./internal/gateway       # the documentation handler (/docs)
gofmt -l . && go vet ./... && go test ./...
```

`go test ./internal/apidocs` proves: every route and method is documented and
every documented route is served; every backtick path, relative link and
`#anchor` resolves; every example is well formed, indexed and runs; the
generator mentions no network, shell or model; deleting a route from the table
is detected.

### What the checks cannot see

- A **GET** on an unserved path answers `404`, indistinguishable from a `404` for
  data the probe cannot see, so the live walk detects a missing table entry only
  through another method.
- The suite checks paths, methods and examples, not prose.
- Examples run against a gateway over an in-memory store: they prove routing and
  validation, not what live Telegram answers.

## Docs and code are one task

**An API implementation task is not complete until the generator has run, the
check passes, and the prose that changed says what it needs to say — unless the
task explicitly says not to touch documentation.** If it does, follow it and say
in the final report which pages are now stale.

## Never invent

No field, status, code, header, default, permission or endpoint the code does
not produce; no credential, token or host from a real deployment in an example.
If behaviour cannot be verified, say so on the page and in the report.

## Final report

List the pages created, updated and removed (the generator prints them), the
unresolved items that remain, the commands run and their real results.
