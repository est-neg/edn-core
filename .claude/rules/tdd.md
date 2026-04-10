---
description: TDD and QA guidance for backend validation, regression coverage, and failing-test-first delivery.
paths:
  - "**/*_test.go"
---
# TDD Rule

- Start from a failing test that captures the required behavior or regression.
- Name tests by behavior and keep them deterministic.
- Cover happy path, validation, authorization, idempotency, and failure handling for critical workflows.
- Do not approve behavior based only on reasoning when executable evidence is expected.