---
description: Security guidance for authentication, authorization, secrets, trust boundaries, input validation, and backend hardening.
---
# Security Rule

- Treat every external input, device signal, and partner callback as untrusted.
- Enforce authn and authz at server-side trust boundaries.
- Avoid leaking secrets or sensitive data through logs, errors, caches, or events.
- Require least privilege, replay protection, and rate limiting where relevant.
- Block insecure defaults, undocumented privileged behavior, and hardcoded credentials.