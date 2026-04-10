---
description: Backend architecture guidance for module boundaries, APIs, events, workflow design, and multi-channel backend capabilities in this repository.
---
# Backend Architecture Rule

- Keep business rules independent from transport and persistence adapters.
- Model channel differences in adapters, not inside core workflows.
- Preserve explicit module boundaries, dependency direction, and contract ownership.
- Prefer idempotent write paths, reliable event publication, and backward-compatible interfaces.
- Write down assumptions and tradeoffs before expanding a change across modules.