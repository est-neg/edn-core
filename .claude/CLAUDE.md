# Claude Workspace Guidance

Use this workspace with Claude models for implementation-heavy backend work.

## Model Policy

- Default implementation model: Claude Sonnet 4.6.
- Escalation model: Claude Opus 4.6 when the task stays blocked after meaningful attempts, when QA repeats the same correction, or when the fix spans multiple modules or data stores and the first path is not stabilizing.

## Backend Core Defaults

- This repository is a Go-first backend core for WEB, MOBILE, IOT, and partner channels.
- Keep channel adapters outside business rules.
- Use MariaDB for transactional truth, MongoDB for document/read-model workloads, and Redis for cache and coordination.
- Prefer contract-first APIs, idempotent workflows, reversible changes, and explicit observability.

## Delivery Expectations

- Start from acceptance criteria and tests whenever possible.
- Keep changes small, explicit, and reviewable.
- Treat security, data ownership, and operational safety as first-class constraints.

## Shared Context

- See `docs/ai/backend-core-context.md` for platform scope.
- See `docs/ai/agent-orchestration.md` for role boundaries, handoff flow, and model escalation rules.
