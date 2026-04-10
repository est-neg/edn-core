---
name: Go Backend Standards
description: "Use when implementing Go handlers, services, workers, repositories, adapters, or shared backend packages. Covers package boundaries, context propagation, error handling, testing, and observability."
applyTo: "**/*.go"
---
# Go Backend Standards

- Pass `context.Context` through every I/O or boundary-facing operation.
- Keep DTOs, transport contracts, and persistence models separate from domain types when responsibilities diverge.
- Use constructor injection and explicit interfaces at boundaries that need substitution, testing, or transport isolation.
- Return wrapped errors with actionable context; reserve panics for truly unrecoverable startup corruption.
- Keep concurrency explicit and bounded; prefer channels, worker pools, and context cancellation over ad-hoc goroutines.
- Add structured logging, metrics, and traces around external calls, background jobs, and high-risk workflows.
- Favor table-driven tests for units and contract-oriented integration tests for adapters.