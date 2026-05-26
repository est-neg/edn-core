# InfinitePay Webhook Relay — Operations Runbook

This runbook covers cutover, secret rotation, revocation, and rollback for the
`edn-webhook-dev` relay and the `edn-core-payments-dev` internal reconcile surface.

## 1. Service Overview

| Service | Binary | Cloud Run ingress | Purpose |
| --- | --- | --- | --- |
| `edn-core-dev` | `cmd/app` | public / unauthenticated | checkout, plans, leads, docs |
| `edn-core-payments-dev` | `cmd/payments-api` | internal / authenticated | webhook reconciliation |
| `edn-webhook-dev` | `cmd/webhook-relay` | public / unauthenticated | thin relay for InfinitePay |
| `edn-core-dev-worker` | `cmd/subscription-worker` | internal | outbox dispatch |

The relay is the **only** internet-facing webhook ingress. The reconcile route lives
exclusively on `edn-core-payments-dev` (internal, no public traffic).

## 2. Pre-Cutover Checklist

Before updating the InfinitePay webhook delivery URL:

- [ ] `edn-core-payments-dev` is deployed with `--no-allow-unauthenticated --ingress internal`
- [ ] `edn-webhook-dev` service account has `roles/run.invoker` on `edn-core-payments-dev`
- [ ] `RELAY_SECRET_PATH` is loaded from Secret Manager (not env-vars-file)
- [ ] `RELAY_CORE_URL` points at the `edn-core-payments-dev` reconcile endpoint
- [ ] `RELAY_CORE_INTERNAL_TOKEN` matches `VIL_PAYMENTS_INTERNAL_VERIFY_AUTH_TOKEN` on core
- [ ] Smoke test: `POST /internal/payments/providers/infinitepay/webhook-reconcile` from the relay service returns 200 or 422 (not 401/403)
- [ ] Smoke test: `POST /v1/webhooks/infinitepay/<secret>` via `edn-webhook-dev` with a synthetic payload returns a relay-synthesized response (200 or 422), no core body leaks
- [ ] Platform request logging for `edn-webhook-dev` is configured to exclude or redact the webhook path to avoid secret exposure

## 3. Provider Cutover

1. Log in to the InfinitePay merchant dashboard.
2. Change the webhook delivery URL from the old `edn-core-dev` path to:
   `https://edn-webhook-dev-<hash>-uc.a.run.app/v1/webhooks/infinitepay/<RELAY_SECRET_PATH>`
3. Trigger a test delivery from the dashboard and confirm a `200 OK` response in relay logs.
4. Monitor `edn-core-payments-dev` logs for any unexpected 4xx or 5xx from `Handle`.
5. Do **not** remove the legacy `edn-core-dev` webhook mount until cutover is validated.

## 4. Post-Cutover Validation

- Confirm live webhook events arrive at `edn-webhook-dev` and are reconciled by `edn-core-payments-dev`.
- Verify `edn-core-dev` receives no new webhook traffic on the retired path.
- After 24 hours without legacy traffic, confirm and proceed with legacy mount removal (already done in code; ensure redeploy of `edn-core-dev`).

## 5. Secret Rotation (RELAY_SECRET_PATH)

Secret rotation uses the optional dual-path overlap window (≤ 15 minutes).

### Step-by-step

1. Generate a new high-entropy secret: `openssl rand -hex 32`.
2. Add the new value to Secret Manager under a new version.
3. Update `RELAY_NEXT_SECRET_PATH` on `edn-webhook-dev` to the new secret value (via `--set-secrets` or Cloud Run update). Do **not** change `RELAY_SECRET_PATH` yet.
4. Deploy `edn-webhook-dev` — both old and new secret paths are now accepted.
5. Update the InfinitePay webhook URL to use the new secret path.
6. Confirm test deliveries arrive successfully on the new path.
7. Within 15 minutes, remove `RELAY_NEXT_SECRET_PATH` and set `RELAY_SECRET_PATH` to the new value. Deploy again.
8. The old secret path is now invalid. Confirm InfinitePay is delivering only to the new path.

### Revocation (emergency)

If the secret path is believed to be compromised:

1. Immediately set `RELAY_SECRET_PATH` to a new random secret (step 1 above).
2. Remove `RELAY_NEXT_SECRET_PATH` (do not allow overlap for a compromised secret).
3. Deploy `edn-webhook-dev` — the old path is immediately invalid.
4. Update InfinitePay webhook URL to the new secret.
5. File an incident report noting the source and scope of the exposure.
6. If platform request logging captured the old path, request log purge or redaction per your data-handling policy.

## 6. Internal Token Rotation (RELAY_CORE_INTERNAL_TOKEN / VIL_PAYMENTS_INTERNAL_VERIFY_AUTH_TOKEN)

The internal token is a secondary defense-in-depth control. IAM is the primary auth.

1. Generate a new token: `openssl rand -hex 32`.
2. Update the Secret Manager secret versions for both:
   - `_PAYMENTS_INTERNAL_VERIFY_AUTH_TOKEN_SECRET` (used by both relay and core)
3. Deploy `edn-webhook-dev` with the new `RELAY_CORE_INTERNAL_TOKEN` version.
4. Deploy `edn-core-payments-dev` with the new `VIL_PAYMENTS_INTERNAL_VERIFY_AUTH_TOKEN` version.
5. Deploy relay first, then core, in quick succession to minimize the window where they have mismatched tokens. During that window, core will return 401 for relay requests, which the relay maps to `400 Bad Request` for InfinitePay, inducing retry — no events are lost.

## 7. Rollback

### Rollback relay to previous revision

```sh
gcloud run services update-traffic edn-webhook-dev \
  --region REGION \
  --to-revisions PREVIOUS_REVISION=100
```

### Rollback payments-api core to previous revision

```sh
gcloud run services update-traffic edn-core-payments-dev \
  --region REGION \
  --to-revisions PREVIOUS_REVISION=100
```

### Emergency: re-enable legacy core webhook mount

If the relay becomes unavailable and InfinitePay cannot deliver:

1. Temporarily re-mount the webhook route on `edn-core-dev` (revert the route removal in `cmd/app/main.go`).
2. Ensure `VIL_PAYMENTS_WEBHOOK_SECRET_PATH` is still configured in `edn-core-dev` secrets.
3. Update InfinitePay webhook URL back to the `edn-core-dev` path.
4. This is a transitional fallback only — remove it again once the relay is restored.

## 8. Observability

| Signal | What to watch |
| --- | --- |
| Relay `relay request completed` log | `core_status`, `relay_status`, `elapsed` |
| Relay `relay forward error` log | count and error type |
| Core `webhook reconcile failed` log | unexpected error count |
| Cloud Run request count | `edn-webhook-dev` vs `edn-core-payments-dev` ratio should be ~1:1 |
| Core `Handle` 422 rate | spikes indicate payload changes or integrity gate hits |
| Core `Handle` 503 rate | lock contention or downstream provider issues |

## 9. Key Invariants (never violate)

- `edn-core-payments-dev` must never have `--allow-unauthenticated`
- `edn-webhook-dev` must never query MongoDB, Redis, or InfinitePay `payment_check`
- The relay secret path must never appear in logs, metrics labels, or trace attributes
- Amount mismatch and transaction identity mismatch must never result in payment or order mutation — they must return 422 from core and 422 from the relay
