# Backend Core Guidelines

This repository defines the backend core for digital products consumed by WEB, MOBILE, IOT, and partner channels.

## Architecture

- Treat the system as a Go-first backend core with explicit module boundaries and channel adapters.
- Prefer `cmd/` for entrypoints, `internal/` for domain and application logic, `api/` for contracts, and `docs/` for architecture notes and runbooks.
- Keep business rules independent from transport and persistence implementation details.
- Use MariaDB for transactional consistency, MongoDB for document or read-model workloads, and Redis for cache and coordination concerns.
- Favor contract-first APIs, idempotent workflows, asynchronous integration through outbox or event publication, and backward-compatible changes.

## Delivery

- Start from acceptance criteria and tests before broad implementation.
- Keep changes small, reversible, and observable.
- Document architecture-impacting decisions in Markdown under `docs/`.

## Security

- Validate input at every trust boundary.
- Require authentication, authorization, auditability, and rate-limiting for external-facing capabilities.
- Never hardcode secrets or trust channel payloads without validation.

## AI Context

- Use [../docs/ai/backend-core-context.md](../docs/ai/backend-core-context.md) as the default system context.
- Use [../docs/ai/agent-orchestration.md](../docs/ai/agent-orchestration.md) for role selection, model policy, and handoff flow.