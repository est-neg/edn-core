---
description: Data-platform guidance for MariaDB, MongoDB, Redis, schemas, indexes, migrations, cache strategy, and consistency decisions.
---
# Data Platform Rule

- MariaDB owns transactional consistency and authoritative business state.
- MongoDB is for document-centric aggregates and read-model style access.
- Redis is for cache, rate limiting, and ephemeral coordination, not durable truth.
- Every change must make index strategy, TTL or retention, rollback path, and operational impact explicit.
- Separate destructive cleanup from the first release that introduces a data change.