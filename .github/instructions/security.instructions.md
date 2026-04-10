---
name: Security Review Rules
description: "Use when reviewing authentication, authorization, secrets, input validation, network boundaries, data protection, or application hardening for the backend core."
---
# Security Review Rules

- Treat every external payload, device signal, and partner callback as untrusted input.
- Enforce authentication and authorization at explicit trust boundaries; never rely on client-side role claims alone.
- Minimize sensitive data exposure in logs, errors, events, and cache entries.
- Require least-privilege access for services, queues, database users, and operational tooling.
- Prefer allow-lists, explicit validation, and bounded parsers over permissive acceptance.
- Verify replay protection, idempotency, and rate limits for public or device-facing endpoints.
- Block changes that introduce hardcoded secrets, insecure defaults, or undocumented privileged behavior.