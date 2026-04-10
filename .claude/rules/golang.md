---
description: Go backend coding guidance for handlers, services, workers, repositories, and adapters in this repository.
paths:
  - "**/*.go"
---
# Go Backend Rule

- Pass `context.Context` through boundary and I/O operations.
- Keep transport DTOs, domain types, and persistence models separate when their responsibilities differ.
- Use constructor injection and explicit interfaces at substitutable boundaries.
- Return wrapped errors with useful context and keep concurrency bounded and observable.
- Add tests and instrumentation with the implementation instead of after it.