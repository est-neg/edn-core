---
name: Data Platform Rules
description: "Use when designing schemas, migrations, indexes, cache strategy, TTL policies, data lifecycle, or consistency behavior for MariaDB, MongoDB, Redis, and related persistence decisions."
---
# Data Platform Rules

- MariaDB owns transactional consistency, foreign-keyed workflows, and authoritative business state.
- MongoDB is for document-centric aggregates, flexible read models, and projection-heavy access patterns.
- Redis is never the source of truth for durable business state.
- Every data change must define indexes, lifecycle, rollback strategy, and operational impact.
- Make migrations reversible whenever possible and separate destructive cleanup from the initial functional release.
- Declare cache invalidation strategy and TTL explicitly; do not create permanent caches by accident.
- Review hot paths for contention, cardinality growth, and multi-tenant isolation before finalizing data design.