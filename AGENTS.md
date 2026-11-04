# telesync — agent guide

`telesync` is a Telegram content-management and distribution daemon: supervised
isolated MTProto workers behind an HTTP API, SQLite inventory, and embedded
Persian admin panel. The workflow is **acquire → manage → read → select →
prepare → publish** to Telegram or WordPress. Product intent/status is in
`PROJECT_BRIEF.md`; the compact current snapshot is `.agents/memory/STATE.md`.

## Architecture

```
browser/scripts → gateway + supervisor + SQLite → worker RPC → MTProto
```

`cmd/telesync` wires `runtime`; `gateway` owns HTTP/authz, `rpc` the worker
contract, `worker` the per-client service, `gotdclient` the only gotd adapter,
and `channelstore` SQLite. `web/` is the frontend and `internal/panel` embeds
it; the root `panel/` is not the product.

## Mandatory investigation workflow

Every agent must use Graphify before exploring source files or making a plan.
This is required for both implementation tasks and repository questions:

1. Inspect `graphify-out/` and use the existing graph when available; otherwise
   build/update the graph with the Graphify skill.
2. Call `graph_stats` first. Then inspect the relevant packages, important
   symbols, interfaces, dependencies, communities, and function-call
   relationships with Graphify.
3. Check the relevant repository context (`PROJECT_BRIEF.md`, `.agents/memory/`,
   decisions, API docs, skills, tests, and current working-tree changes).
4. Separate verified facts from requirements, proposals, and unknowns. Record
   the relevant findings and risks before planning.
5. Only after these checks, write a concise implementation plan. Then work in
   small, behavior-first steps, preserving existing patterns and seams.

Do not begin by browsing or editing source files, guessing architecture, or
planning from filenames alone. If Graphify is unavailable or its graph is
stale/incomplete, report that limitation, repair or update the graph when
possible, and clearly label any conclusions that remain unverified.

## Development lifecycle (mandatory for source changes)

Every feature, fix, or behavior-changing refactor follows one of two workflows:

- **Prepare** — `.agents/workflows/prepare-change.md`: Discover (Graphify
  first) → Plan → write failing tests (Red) → write a handoff under
  `.agents/handoffs/` from `_TEMPLATE.md` → **stop**. The preparation agent
  never edits production code.
- **Implement** — `.agents/workflows/implement-handoff.md`: only on explicit
  user approval naming the handoff; Green → Refactor → validate → record
  results in the handoff.

Handoffs are the only channel between sessions and models: a preparation
session's conversation is never required. Never mark a handoff approved without
an instruction that names it.

## Always preserve

- Use behavior-first TDD; load `.agents/skills/tdd-go/SKILL.md` for the exact
  loop and test seams.
- Keep clients isolated: official leaves gotd DC/resolver/key options at zero;
  custom uses only configured endpoint, DC and PEM keys. Never silently fall
  back between clients, endpoints, CDN or keys; unknown client = 404, unavailable
  worker = 503.
- Never leak or invent secrets, phone numbers, access hashes, credentials,
  tokens, backend values, permissions, or results. Mask secrets/phones with the
  existing helpers.
- Keep application permissions separate from Telegram capabilities. Resolve
  Telegram ids to local `managed_channels.id` before permission checks. Every
  channel/category/site route uses `gateway.visible()`: deny by default, explicit
  deny wins, and writes require view.
- Reads do not write, mark messages read, or publish. Telegram/WordPress and
  SQLite are not one transaction; report `partial_failure` honestly.
- Route/contract changes require the docs generator and `internal/apidocs` check;
  load `.agents/skills/api-documentation/SKILL.md`. Never hand-edit generated
  pages. Schema changes require a new migration, never edits to an applied one.
- Keep WordPress HTTPS/public-host/resolve-once/no-redirect/no-proxy guards and
  backend-only AES-GCM credentials. Do not hardcode client names or keys.

## Work and verification

For nontrivial changes, trace workflow/problem/owner/routes/permissions/status
and decisions before designing; reuse patterns and name unknowns or owner
decisions instead of guessing. Reports distinguish verified fact, requirement,
proposal and unknown; claims distinguish tests/build, handshake, authorization
and live verification. A sandbox skip is not a pass.

Useful checks (run the smallest relevant set): `gofmt -l .`, `go vet ./...`,
`go test ./...`, `go build ./...`, docsgen `--check`, apidocs tests, and
`npm run typecheck` in `web/`.

## Load on demand

Product/status: `PROJECT_BRIEF.md`; current state: `.agents/memory/STATE.md`;
architecture: `.agents/memory/ARCHITECTURE.md`; decisions:
`.agents/memory/DECISIONS.md`; gotd facts: `.agents/memory/GOTD_NOTES.md`;
API: `docs/api/README.md`; routing and gotd workflows are in their skills.

Before stopping, update `STATE.md` with changed/verified/unverified facts and
append `LOG.md`; record new decisions or gotd facts in their topic files and
update the product status matrix only when status changed.
