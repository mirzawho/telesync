# Prepare change — Discover → Plan → Red → Handoff → Stop

Load when asked to prepare a feature, bug fix, or behavior-changing refactor for
later implementation. Run this with a strong reasoning model. It never edits
production code.

## Stage 1 — DISCOVER (Graphify first)

1. Read `AGENTS.md` (already loaded) and confirm the task's intended behavior.
   If behavior is unclear, ask before continuing.
2. Call `graph_stats` and confirm the graph matches HEAD. If stale, refresh it
   per `.agents/skills/graphify/SKILL.md`, then continue.
3. Use targeted Graphify queries (`query_graph`, `shortest_path`, `get_node`,
   `get_neighbors`) to locate packages, symbols, interfaces, callers and
   related tests. Keep `token_budget` small (≤1500) and narrow `context_filter`
   when output truncates. Never load `graph.json`/`graph.html` into context.
4. Verify every load-bearing fact by reading only the source files Graphify
   pointed at. Graphify is a map, source is the truth; if it lacks the answer,
   use targeted `rg`/`gopls` searches and note the limitation.
5. Check `.agents/memory/` context relevant to the task (STATE, DECISIONS,
   topic notes), the matching `.agents/skills/`, existing tests, and the
   current `git status`.

Output: a short evidence-based discovery summary (relevant files, symbols,
call paths, existing tests, constraints, risks) — later §C of the handoff.
Separate verified facts from assumptions; label unknowns.

## Stage 2 — PLAN

Write, before any test: problem, expected behavior, acceptance criteria, what
must be preserved, files to change and in what order, contracts/signatures if
new, edge cases, test strategy, regression risks, validation commands.

Rules: smallest implementation that works; reuse existing patterns and
dependencies; no speculative architecture; no assumptions marked as facts.

## Stage 3 — RED (tests first)

Choose existing test files where they fit, else create the natural ones using
the project's patterns (`.agents/skills/tdd-go/SKILL.md`). Write real tests for
success, failure and boundary cases, asserting user-visible behavior.

- Run only the new tests against unchanged production code and capture the
  result.
- A genuine Red means compilation succeeds and assertions fail on missing
  behavior. Missing APIs may fail compile only if §F documents the exact
  contract needed.
- Never weaken assertions, add placeholders, or touch production code
  (test-only helpers are fine). Tests that already pass must be reported as
  already-passing and investigated — never manufactured into a Red.
- Environment/infrastructure failures → status `BLOCKED`, not Red.

## Stage 4 — HANDOFF (mandatory stop)

Create `.agents/handoffs/<task-id>.md` from `.agents/handoffs/_TEMPLATE.md`.
Fill every section with the Stage 1–3 outputs; set
`status: AWAITING_APPROVAL`, `approval: not_requested`; check §I boxes that
are genuinely done.

Then STOP. Do not write production code, do not start Green, do not continue
because the plan looks complete. Report the handoff path and status, and end
the session. Implementation begins only on explicit user approval that names
this handoff (see `.agents/workflows/implement-handoff.md`).