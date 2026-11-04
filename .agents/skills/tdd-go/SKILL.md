---
name: tdd-go
description: Write behavior-first Go tests using telesync's existing seams.
---

# TDD in Go

For a behavior change, write the test first, run it red for the intended
behavior (not merely compilation), implement the smallest change, then refactor
with tests green. A temporary `panic("not implemented")` body is acceptable
when an exported signature is needed to make a real red test.

Use external test packages where practical, table tests with `t.Run`,
`t.TempDir()`, and user-visible behavior assertions. Do not add a mocking
framework. The existing seam list is in `references/test-seams.md`.
