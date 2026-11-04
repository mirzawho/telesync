---
task_id: <kebab-case-id>
title: <one-line task title>
status: DISCOVERING
created: <YYYY-MM-DD>
updated: <YYYY-MM-DD>
baseline_commit: <sha of baseline the plan was written against>
approval: not_requested
---

<!-- Status values: DISCOVERING | PLANNING | WRITING_TESTS | AWAITING_APPROVAL |
     APPROVED | IMPLEMENTING | VERIFYING | DONE | BLOCKED
     approval: not_requested | granted (by <who>, <date>) | blocked -->

# <Task title>

## A. Metadata

See frontmatter. Update `status`, `updated`, and `approval` at every stage
transition. Never set `approval: granted` without an explicit user instruction
naming this task.

## B. Requirements

### Problem

<What is wrong or missing, with evidence.>

### Desired behavior

<Testable statements of what must be true when done.>

### Acceptance criteria

- [ ] <specific, checkable criterion>
- [ ] <specific, checkable criterion>

### Out of scope

- <things explicitly not being changed>

## C. Code discovery

Only the facts needed to implement. Cite paths/symbols; no source dumps.

### Relevant files

| Path | Role |
| --- | --- |
| `internal/...` | <what it does today> |

### Symbols and contracts

- `<Symbol>()` — <signature/behavior relevant to this task>

### Dependencies and call paths

- <caller> → <callee> → <side effect>

### Graphify findings

- <community/package + the relationship that matters>. Graph built at
  `<commit>`.

### Constraints that apply

- <from AGENTS.md / skills / existing tests that must stay green>

### Unknowns / risks

- <open questions; nothing invented to fill a gap>

## D. Implementation plan

Ordered steps a smaller coding model can execute without re-deriving the
architecture.

1. **<file>** — <function/method to add or change>, contract: `<signature>`,
   behavior: <required effect and error handling>.
2. **<file>** — <next step, why it comes after step 1>.
3. …

### Must not change

- <files, exported APIs, or behaviors that must stay as they are>

## E. Test inventory

| File | Test name | Purpose | Current result | Run command |
| --- | --- | --- | --- | --- |
| `pkg/x/y_test.go` | `TestFoo...` | <behavior asserted> | FAIL | `go test ./pkg/x -run TestFoo -count=1` |

## F. Red-phase evidence

- Command: `go test ./pkg/x -run TestFoo -count=1`
- Exit code: `1` (expected: these tests target behavior not yet implemented)
- Failure excerpt:
  ```
  <2-5 lines, enough to show expected vs actual>
  ```
- Why each failure is a genuine Red:
  - `TestFoo...` — <asserts new behavior; implementation absent>
- Environment/unrelated failures: <none, or listed as BLOCKED>

## G. Implementation instructions

1. Verify you are authorized: the user explicitly named this handoff as
   approved. If not, stop and report `AWAITING_APPROVAL`.
2. Read `AGENTS.md`, `.agents/workflows/implement-handoff.md`, this file, and
   `.agents/skills/tdd-go/SKILL.md`. Do not re-run repository-wide discovery.
3. Confirm the baseline commit still exists and the working tree is sane;
   re-run the Red command in §F and compare with the recorded evidence.
4. Execute §D in order. Do not rewrite §E tests except to fix genuine test
   bugs, and say so in §J if you do.
5. Run §H validation. Record results in §I and §J.

## H. Validation

- Targeted: `go test ./... -run '<pattern>' -count=1`
- Regression: `go test ./...` (and `npm run typecheck` in `web/` if touched)
- Static: `gofmt -l .`, `go vet ./...`, `go build ./...`; docsgen `--check`
  and apidocs tests if routes/contracts changed
- Done when: all acceptance criteria in §B are checked, targeted and
  regression tests pass, static checks are clean, and §J records the actual
  commands run with their outcomes.

## I. Progress

- [ ] Discovery completed
- [ ] Plan written
- [ ] Tests written (§E)
- [x] Red verified (§F)
- [ ] Approved by user
- [ ] Implementation started
- [ ] Targeted tests passed
- [ ] Regression tests passed
- [ ] Validation complete → status `DONE`

## J. Final notes

<For the implementation agent: deviations, blockers, what was actually run and
what passed. Keep short.>