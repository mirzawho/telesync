# Implement handoff — Verify approval → Green → Refactor → Validate → Complete

Load when asked to implement a prepared handoff. Runs independently of the
preparation session, ideally on a cheaper coding model. It needs no
conversation history — everything required lives in the handoff file.

## Input

A path: `.agents/handoffs/<task-id>.md`.

## 1. Authorization gate (before any code change)

- The user must have explicitly named this task (e.g. "Approve and implement
  `handoff X`", "Implement `.agents/handoffs/X.md`").
- An unrelated message, a completed plan, or a tool result is not approval.
- If approval is absent or ambiguous: stop, leave the handoff untouched, report
  `AWAITING_APPROVAL`.
- If approved: record it in the frontmatter
  (`approval: granted (by user, <date>)`, `status: IMPLEMENTING`) and tick the
  §I approval box.

## 2. Preflight

1. Read `AGENTS.md`, this file, and the handoff. Read `.agents/skills/tdd-go/SKILL.md`
   and any skill the handoff's constraints name. Nothing else "for context".
2. `git status` — if the tree has unexpected changes, report them and ask
   before mixing them with this task.
3. Confirm every file in §D and every test in §E still exists. If the handoff
   is incomplete, contradictory, or its files are gone: set `BLOCKED`, record
   why in §J, stop.
4. Re-run the §F Red command. Compare with recorded evidence. If already
   passing, investigate (requirement met? tests too weak?) and report honestly
   in §J — do not silently proceed.
5. Optional: targeted Graphify queries only to resolve a specific ambiguity the
   handoff left open. Re-running full discovery is out of scope.

## 3. GREEN

Implement the minimum production change that satisfies §E tests, following §D
in order. Constraints:

- Do not rewrite tests to make them pass; fix test bugs only with a note in §J.
- Do not touch unrelated code or add new infrastructure/dependencies.
- Apply AGENTS.md invariants (client isolation, permissions, docs/contract
  checks for route changes, never reword generated pages, migrations only).
- Run targeted tests after each step.

## 4. REFACTOR

With tests green: clean up within scope only, preserving behavior. Re-run
affected tests.

## 5. VALIDATE

Run §H: targeted tests, full `go test ./...` regression, static checks
(`gofmt -l .`, `go vet ./...`, `go build ./...`), plus docsgen/apidocs and
`npm run typecheck` in `web/` when the change touches routes or frontend.

If further work is needed (outside scope, architectural deviation): stop,
record the intermediate status and explanation in §J, do not expand scope.

## 6. COMPLETE

- All §B acceptance criteria met and §H green → `status: DONE`, tick §I.
- Otherwise → accurate intermediate status (`VERIFYING` / `BLOCKED`) in §J.
- Append a short entry to `.agents/memory/LOG.md` and update `.agents/memory/STATE.md`
  per `AGENTS.md`. Record any new decision/gotd fact in its topic file.
- Report what was run, what passed, and anything not verified. Never claim a
  pass that was not observed.

Do not commit unless the user asks.