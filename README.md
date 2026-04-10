# vil-core

Backend core for digital products delivered across WEB, MOBILE, IOT, and partner channels.

## Quick Start

```bash
# Copy sample config (optional — defaults apply without a file)
cp configs/config.example.yaml config.yaml

# Run locally
make run

# Test
make test

# Build binary
make build
```

Server starts on `:8080` by default. Override with `VIL_HTTP_ADDR=:9090`.

## Health Endpoints

| Endpoint  | Purpose       |
|-----------|---------------|
| `GET /livez`  | Liveness probe   |
| `GET /readyz` | Readiness probe  |

## Layout

```
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

| Variable               | Default | Description             |
|------------------------|---------|-------------------------|
| `VIL_HTTP_ADDR`        | `:8080` | Listen address          |
| `VIL_LOG_LEVEL`        | `info`  | debug / info / warn / error |
| `VIL_LOG_JSON`         | `true`  | Structured JSON logging |

## AI Agent Team

See [docs/ai/agent-orchestration.md](docs/ai/agent-orchestration.md).
