---
description: Backend architecture guidance for module boundaries, APIs, events, workflow design, and multi-channel backend capabilities in this repository.
---
# Backend Architecture Rule

- Keep business rules independent from transport and persistence adapters.
- Model channel differences in adapters, not inside core workflows.
- Use DDD language for bounded contexts, aggregates, and integration seams when business logic is non-trivial.
- For larger architecture changes, describe baseline, target, transition, and governance checkpoints in a TOGAF-like progression.
- Use Zachman as a completeness check across data, process, runtime location, actors, timing, and motivation.
- Preserve explicit module boundaries, dependency direction, and contract ownership.
- Make Well-Architected tradeoffs explicit for Cloud Run and platform decisions: operational excellence, security, reliability, performance efficiency, and cost.
- Communicate designs with C4-style context, container, and component views when explaining architecture to different audiences.
- Prefer idempotent write paths, reliable event publication, and backward-compatible interfaces.
- Write down assumptions and tradeoffs before expanding a change across modules.