# Backend Core Context

## Purpose

This repository hosts the backend core for digital products delivered across WEB, MOBILE, IOT, partner, and future channels.

## Architectural Intent

- Build the platform as an API-first backend core with clear domain boundaries and channel-specific adapters.
- Keep business rules independent from transport concerns such as HTTP, gRPC, messaging, and device protocols.
- Favor a modular monolith first, with boundaries that can later be extracted into services without rewriting core domain logic.

## Primary Technology Direction

- Go is the primary implementation language for services, workers, and integration adapters.
- MariaDB is the transactional source of truth for strongly consistent business workflows.
- MongoDB is used for document-heavy use cases, read models, audit/event projections, and flexible aggregates.
- Redis is used for cache, rate limiting, coordination, ephemeral state, and short-lived performance accelerators.

## Quality Attributes

- Secure by default: zero-trust posture, least privilege, explicit authn/authz, secret hygiene, and auditability.
- Observable by default: structured logs, metrics, tracing, correlation IDs, and failure diagnostics.
- Resilient by default: retries with bounds, backpressure, idempotency, and graceful degradation.
- Ready for scale: multi-channel load patterns, asynchronous processing, and partition-aware data access.
- Change-friendly: reversible migrations, backward-compatible contracts, and isolated module ownership.

## Preferred Repository Shape

Use this as the baseline when implementation begins:

```text
cmd/            # Entrypoints
internal/       # Domain, application, platform, and module code
api/            # OpenAPI, protobuf, or channel contracts
deploy/         # Runtime and infrastructure assets
docs/           # ADRs, architecture notes, runbooks, and AI context
```

## Delivery Guardrails

- Start from acceptance criteria and tests before broad implementation.
- Model external contracts first, then application workflows, then persistence details.
- Separate channel concerns from business rules.
- Prefer explicit errors, versioned contracts, and small reversible changes.
- Any change touching persistence, auth, or public APIs must document risk, rollback, and validation.
