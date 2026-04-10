---
name: Backend Architecture
description: "Use when defining modules, APIs, service boundaries, events, workflows, or multi-channel architecture for the backend core. Covers bounded contexts, contracts, idempotency, and evolvability."
---
# Backend Architecture Rules

- Model channel differences in adapters, not inside core business rules.
- Define module boundaries around capabilities, ownership, and consistency needs.
- Keep external contracts backward compatible unless the task explicitly approves a breaking change.
- Design write paths for idempotency, retries, auditability, and partial-failure handling.
- Prefer asynchronous integration for cross-module side effects; use outbox-style reliability when events must not be lost.
- Document assumptions, invariants, and non-functional constraints before implementation expands across modules.
- When a feature touches API shape, data ownership, and background processing at once, produce an architecture note before coding.