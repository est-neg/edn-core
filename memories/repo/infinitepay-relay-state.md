# InfinitePay Webhook Relay — Implementation State

## Architecture (target state — implemented)

- `cmd/app` (edn-core-dev, public): checkout, plans, leads, docs — NO webhook routes
- `cmd/payments-api` (edn-core-payments-dev, internal-only): reconcile route only
- `cmd/webhook-relay` (edn-webhook-dev, public): thin relay, no business logic

## Key invariants

- `HandleWebhookReconcile` is mounted ONLY on `cmd/payments-api` (internal surface)
- Legacy public webhook route removed from both `cmd/app` and `cmd/payments-api`
- Relay uses `mime.ParseMediaType` for strict CT: `application/json` or `application/json; charset=utf-8` only
- Relay response bodies match spec: `not_found`, `method_not_allowed`, `unsupported_media_type`, `payload_too_large`, `bad_request`, `{"status":"ok"}`, `unprocessable_entity`
- `Handler.webhook` field is `webhookRunner` interface (allows isolated handler tests)

## Files changed in this session

- `internal/webhookrelay/handler.go` — mime CT validation, spec-correct bodies
- `internal/webhookrelay/handler_test.go` — updated assertions, strict CT + header allow-list tests
- `internal/payments/handler.go` — `webhookRunner` interface, `context` import added
- `internal/payments/handler_reconcile_test.go` — new: 12 focused reconcile handler tests
- `cmd/webhook-relay/main.go` — new entrypoint
- `cmd/app/main.go` — webhook routes removed, WebhookSecretPath validation removed
- `cmd/payments-api/main.go` — legacy webhook route removed, WebhookSecretPath validation removed
- `cloudbuild.development.yaml` — relay+payments-api build/deploy/IAM steps added
- `deploy/env/cloudrun.relay.development.yaml` — new relay env file
- `configs/local-development.example` — relay section added
- `docs/infinitepay-webhook-relay-runbook.md` — new cutover/rotation/rollback runbook

## Remaining environment-only validations

### Verified / resolved

- `_RELAY_IMAGE`, `_PAYMENTS_API_IMAGE`, `_RELAY_SERVICE_ACCOUNT` are defined in the development pipeline substitutions block.
- `RELAY_CORE_URL` / `RELAY_CORE_AUDIENCE` are no longer injected via `--update-env-vars`; they are merged into a generated temporary env-vars YAML file during the relay deploy step.
- `allUsers` IAM blocked by org policy; public services now use `--no-invoker-iam-check` instead of `--allow-unauthenticated`.
- Relay runtime SA `edn-webhook-dev@funcionario-online-493412.iam.gserviceaccount.com` exists.
- Deployer SA `edn-core-dev@funcionario-online-493412.iam.gserviceaccount.com` has `roles/iam.serviceAccountUser` on the relay SA.
- Relay SA has `roles/secretmanager.secretAccessor` on exactly the two relay secrets (`edn-core-dev-payments-webhook-secret-path`, `edn-core-dev-payments-internal-verify-auth-token`).

### Still pending (ops actions)

- Platform request logging for `edn-webhook-dev` must be configured to redact the webhook path.
- InfinitePay provider URL cutover to the relay endpoint (ops, not code).
- End-to-end smoke validation: live InfinitePay event → relay → payments-api reconcile → correct outcome.
