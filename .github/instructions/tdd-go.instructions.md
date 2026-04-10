---
name: Go TDD Rules
description: "Use when writing or reviewing Go tests, defining acceptance criteria, validating regressions, or driving implementation through TDD for backend modules."
applyTo: "**/*_test.go"
---
# Go TDD Rules

- Start with a failing test that captures business behavior or a regression, then implement the smallest passing change.
- Name tests by externally visible behavior, not by private implementation details.
- Cover happy path, validation, authorization, idempotency, and failure handling for every critical workflow.
- Keep tests deterministic; prefer fakes, fixtures, and controlled clocks over sleeps and race-prone timing.
- When integration coverage is needed, validate real contracts at the boundary instead of duplicating unit internals.
- Reject approvals that rely on reasoning alone when executable evidence is expected.