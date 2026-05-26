---
name: Backend Architecture
description: "Use when defining modules, APIs, service boundaries, events, workflows, or multi-channel architecture for the backend core. Covers DDD bounded contexts, TOGAF-style governance and transition planning, Zachman completeness checks, Well-Architected cloud tradeoffs, C4 communication, contracts, idempotency, and evolvability."
---
# Backend Architecture Rules

- Model channel differences in adapters, not inside core business rules.
- Define module boundaries around capabilities, ownership, and consistency needs.
- Use DDD to name bounded contexts, aggregates, domain services, and integration seams when business rules are deep or cross-module.
- For platform or enterprise-shaping changes, describe baseline architecture, target architecture, transition architecture, and governance checkpoints in a TOGAF-like structure.
- Use Zachman as a review lens for broader changes: verify the design has explicit coverage for data, process, location, people, timing, and motivation.
- Keep external contracts backward compatible unless the task explicitly approves a breaking change.
- Design write paths for idempotency, retries, auditability, and partial-failure handling.
- Evaluate Cloud Run, data flow, and integration topologies with Well-Architected tradeoffs called out explicitly: operational excellence, security, reliability, performance efficiency, and cost.
- Explain architecture using C4-style views in the level that fits the audience; at minimum, make context, container, and component boundaries understandable in text.
- Prefer asynchronous integration for cross-module side effects; use outbox-style reliability when events must not be lost.
- Document assumptions, invariants, and non-functional constraints before implementation expands across modules.
- When a feature touches API shape, data ownership, and background processing at once, produce an architecture note before coding.