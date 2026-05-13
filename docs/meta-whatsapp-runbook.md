# Meta WhatsApp Phase 1 — Cloud Run Runbook

Operational reference for `edn-meta-whatsapp-api` (internal intake) and
`edn-meta-whatsapp-webhook` (public relay).

---

## Services

| Environment | Service name | Surface | Purpose |
| --- | --- | --- | --- |
| dev | `edn-meta-whatsapp-api-dev` | internal, no-allow-unauthenticated | Durable receipt persistence + dedupe |
| dev | `edn-meta-whatsapp-webhook-dev` | public, no-invoker-iam-check | Meta webhook edge (verify + relay) |
| prd | `edn-meta-whatsapp-api-prd` | internal, no-allow-unauthenticated | Durable receipt persistence + dedupe |
| prd | `edn-meta-whatsapp-webhook-prd` | public, no-invoker-iam-check | Meta webhook edge (verify + relay) |

Region: dev → `us-central1`, prd → `southamerica-east1`.

---

## Required Secrets (Secret Manager)

| Secret name (prd) | Env var | Notes |
| --- | --- | --- |
| `edn-core-prd-mongodb-uri` | `VIL_MONGODB_URI` | API service only |
| `edn-core-prd-whatsapp-meta-verify-token` | `VIL_WHATSAPP_META_VERIFY_TOKEN` | webhook service only |
| `edn-core-prd-whatsapp-meta-app-secret` | `VIL_WHATSAPP_META_APP_SECRET` | webhook service only |
| `edn-core-prd-whatsapp-meta-internal-verify-auth-token` | `VIL_WHATSAPP_META_INTERNAL_VERIFY_AUTH_TOKEN` | both services |

Replace `prd` with `dev` for the development equivalents.

The webhook service also receives `VIL_WHATSAPP_META_INTERNAL_INTAKE_URL` and
`VIL_WHATSAPP_META_INTERNAL_AUDIENCE` as non-secret env vars derived at deploy time
from the API service URL (see `cloudbuild.yaml`).

---

## Deploy / Cutover Order

1. Deploy `edn-meta-whatsapp-api-{env}` first (internal service must exist before URL derivation).
2. Grant the webhook SA `roles/run.invoker` on the API service (binding must exist before public relay starts receiving traffic).
3. Deploy `edn-meta-whatsapp-webhook-{env}` (picks up intake URL from the just-deployed API service).
4. Register the public webhook URL with Meta in the App Dashboard.

Both services are deployed and the IAM grant is applied automatically on every
`cloudbuild.yaml` / `cloudbuild.development.yaml` run in this order.

---

## IAM Grant

The public webhook service account must have `roles/run.invoker` on the internal API service:

```bash
# prd
gcloud run services add-iam-policy-binding edn-meta-whatsapp-api-prd \
  --region southamerica-east1 \
  --member serviceAccount:edn-whatsapp-webhook-prd@funcionario-online-493412.iam.gserviceaccount.com \
  --role roles/run.invoker

# dev
gcloud run services add-iam-policy-binding edn-meta-whatsapp-api-dev \
  --region us-central1 \
  --member serviceAccount:edn-whatsapp-webhook-dev@funcionario-online-493412.iam.gserviceaccount.com \
  --role roles/run.invoker
```

---

## Smoke Checks

### GET verify (Meta webhook handshake)

```bash
WEBHOOK_HOST=https://<edn-meta-whatsapp-webhook-{env}>
VERIFY_TOKEN=<verify_token_value>

curl -s -o /dev/null -w "%{http_code}" \
  "${WEBHOOK_HOST}/v1/webhooks/meta/whatsapp?hub.mode=subscribe&hub.verify_token=${VERIFY_TOKEN}&hub.challenge=test123"
# Expected: 200 with body: test123
```

Wrong token must return 403:

```bash
curl -s -o /dev/null -w "%{http_code}" \
  "${WEBHOOK_HOST}/v1/webhooks/meta/whatsapp?hub.mode=subscribe&hub.verify_token=wrong&hub.challenge=test123"
# Expected: 403
```

### POST notification (signed payload)

```bash
APP_SECRET=<app_secret_value>
PAYLOAD='{"object":"whatsapp_business_account","entry":[]}'
SIG=$(printf '%s' "${PAYLOAD}" | openssl dgst -sha256 -hmac "${APP_SECRET}" | awk '{print "sha256="$2}')

curl -s -o /dev/null -w "%{http_code}" \
  -X POST "${WEBHOOK_HOST}/v1/webhooks/meta/whatsapp" \
  -H "Content-Type: application/json" \
  -H "X-Hub-Signature-256: ${SIG}" \
  -d "${PAYLOAD}"
# Expected: 200 (ignored — no messages in payload)
```

Missing or wrong signature must return 400 (absent/malformed) or 403 (HMAC mismatch):

```bash
curl -s -o /dev/null -w "%{http_code}" \
  -X POST "${WEBHOOK_HOST}/v1/webhooks/meta/whatsapp" \
  -H "Content-Type: application/json" \
  -d "${PAYLOAD}"
# Expected: 400 (no signature header)
```

---

## Status Code Reference

| Scenario | Public response |
| --- | --- |
| GET — valid verify_token | 200 + challenge literal |
| GET — wrong token | 403 |
| POST — signature header absent or malformed | 400 |
| POST — HMAC mismatch | 403 |
| POST — wrong Content-Type | 415 |
| POST — body too large | 413 |
| POST — accepted (messages written) | 200 `{"status":"ok"}` |
| POST — duplicate (already seen, skipped) | 200 `{"status":"ok"}` |
| POST — no messages field (ignored) | 200 `{"status":"ok"}` |
| POST — invalid payload (empty phone_number_id or message.id) | 400 `{"error":"bad_request"}` |
| POST — MongoDB unavailable | 503 `{"error":"service_unavailable"}` |
