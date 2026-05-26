# vil-core

Backend core for digital products delivered across WEB, MOBILE, IOT, and partner channels.

## Quick Start

```bash
# Set up local environment (required — contains secrets, never committed)
cp configs/local-development.example .env.local
# edit .env.local and fill in the REPLACE_ME placeholders

# Run locally
make run

# Test
make test

# Build binary
make build
```

Server starts on `:8080` by default. Override with `VIL_HTTP_ADDR=:9090`.

## Health Endpoints

| Endpoint      | Purpose         |
| ------------- | --------------- |
| `GET /livez`  | Liveness probe  |
| `GET /readyz` | Readiness probe |

## Layout

```text
cmd/api/            Entry point
internal/
  platform/
    config/         Config loader (YAML + env)
    logger/         Structured logger (zap)
    health/         Liveness and readiness handlers
    httpserver/     HTTP server factory and middleware
api/openapi/        OpenAPI / protobuf contracts
configs/            Sample config files
deploy/docker/      Dockerfile
docs/
  ai/               AI context and agent orchestration docs
  adr/              Architecture Decision Records
```

## Environment Variables

All variables use the `VIL_` prefix. Nested keys use `_` as separator.

| Variable        | Default  | Description                 |
| --------------- | -------- | --------------------------- |
| `VIL_HTTP_ADDR` | `:8080`  | Listen address              |
| `VIL_LOG_LEVEL` | `info`   | debug / info / warn / error |
| `VIL_LOG_JSON`  | `true`   | Structured JSON logging     |

## AI Agent Team

See [docs/ai/agent-orchestration.md](docs/ai/agent-orchestration.md).

## Cloud Run Bootstrap

Non-sensitive Cloud Run deploy inputs live in [deploy/env/cloudrun.development.yaml](deploy/env/cloudrun.development.yaml) and [deploy/env/cloudrun.production.yaml](deploy/env/cloudrun.production.yaml).

Cloud Build pipelines live in [cloudbuild.development.yaml](cloudbuild.development.yaml) and [cloudbuild.yaml](cloudbuild.yaml).

Deployment and MongoDB bootstrap analysis live in [docs/cloud-run-bootstrap.md](docs/cloud-run-bootstrap.md).

Lead form and API protection strategy live in [docs/lead-protection-strategy.md](docs/lead-protection-strategy.md).
