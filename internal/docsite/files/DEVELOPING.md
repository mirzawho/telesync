<!-- title: Maintaining this documentation -->
# Maintaining this documentation

The API reference is generated from the source and a replay of the examples,
then served as HTML by the daemon at `GET /docs`. This page is for whoever
changes the API next.

## Generate

```bash
go run ./cmd/docsgen            # write docs/api/generated, compose the pages
go run ./cmd/docsgen --check    # write nothing; exit 1 if the committed pages
                                # are out of date (for CI)
```

or, with make:

```bash
make docs          # go run ./cmd/docsgen
make docs-check    # go run ./cmd/docsgen --check
```

The generator runs offline: no network, no credentials, no model. Two runs with
no source change produce the same bytes. The same pass is covered by
`go test ./internal/apidocs`, which additionally checks that every route and
method is documented, that every link and anchor resolves, and that every
example runs against the gateway.

## How the documentation reaches `GET /docs`

The binary carries it. The generator copies `docs/api` into
`internal/docsite/files`, which the `internal/docsite` package embeds, and
`make build` runs the generator before it compiles. Nothing is configured and
nothing is looked up on disk: build, start the daemon, open `/docs`.

- `make build` (or `make release`) runs `go run ./cmd/docsgen` first, so the
  embedded copy always matches the source being built.
- `api.docs_dir` overrides it with a tree on disk, relative to the
  configuration file's directory or absolute, and that directory must exist.
  `telesync config validate` prints `api.docs_dir=(embedded)` or the path.
- A configuration inside a checkout with no `api.docs_dir` serves the
  checkout's `docs/api`; otherwise it serves the embedded copy.
- The embedded copy is read at build time: after regenerating, rebuild and
  restart. A tree named by `api.docs_dir` is read once at startup.

`internal/docsite/files` is a build artifact. Do not edit it; the generator
mirrors `docs/api` into it on every run and `go run ./cmd/docsgen --check`
fails if the committed copy is stale.

## What is generated and what is not

| Location | Owner | Rewritten by the generator? |
| --- | --- | --- |
| `docs/api/generated/` | generator | yes, in full; do not edit by hand |
| `docs/api/generated/facts.json` | generator | the raw facts every page is rendered from |
| `docs/api/README.md` | hand-written | copied as a page; its generated sections are rewritten |
| `docs/api/examples/README.md`, `examples/api.http` | hand-written | copied as pages; the examples are executed |
| `docs/api/manual/` | hand-written | copied into `docs/api` at the same path |
| `docs/api/manual/components/` | hand-written paragraphs | inserted into the group and endpoint pages |

A hand-written page starts with `<!-- title: … -->` (the archive under
`docs/api/manual/legacy/` starts with `<!-- archived: … -->`). A component starts
with `<!-- component: … -->`. A page without either marker is not published by
`GET /docs`, and the generator lists it in `unresolved.md`.

## When the extraction is not enough

The generator documents what the code and the probe establish: routes, methods,
handlers, `writeJSON` values and statuses, request types as JSON Schema, and the
credential and permission conditions on the way to each handler. It does not
explain meaning, limits or partial failures. Put that in a component:

1. Edit `docs/api/manual/components/<topic>.md`, or add a new one.
2. Name it in the group page's `<!-- components: … -->` list if it is a new
   topic.
3. Run the generator and the check.

Everything the generator could not establish is listed in
`docs/api/generated/unresolved.md` with the evidence it had; work through that
list rather than assuming.
