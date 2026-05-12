# InfinitePay Webhook Relay Architecture Specification

## 1. Document Purpose

This document is the complete architecture specification for the InfinitePay webhook relay slice in EDN Core development environments.

It exists to support implementation and architecture review before code changes expand across services, runtime configuration, and provider cutover. It makes the following explicit:

- the current integration problem and why a relay is required
- the approved target topology for public ingress and private financial reconciliation
- service boundaries, dependency direction, and trust boundaries
- the exact public and internal HTTP contracts
- data ownership, idempotency, and reconciliation authority
- Cloud Run, IAM, Secret Manager, and deployment responsibilities
- observability, failure handling, retry policy checkpoints, and rollback guidance

This specification is normative for the InfinitePay webhook relay slice. When code, deployment assets, or follow-up notes diverge from it, this document is the reference to review first.

## 2. Problem Statement

InfinitePay cannot call the current internal-ingress core service directly.

Today, the code paths in `cmd/app/main.go` and `cmd/payments-api/main.go` mount the public InfinitePay webhook route directly on the payments/core runtime, and `internal/payments/handler.go` reads the request body and delegates to `internal/payments/webhook.go`. That shape couples internet-facing provider ingress with the financial authority that verifies payments, deduplicates deliveries, and updates orders.

The approved target architecture separates those concerns:

`InfinitePay -> edn-webhook-dev -> edn-core-dev -> MongoDB/Redis/payment state`

In that target state:

- `edn-webhook-dev` is the only public ingress for InfinitePay webhook delivery
- `edn-webhook-dev` is a minimal relay only
- `edn-core-dev` remains the only financial authority for reconciliation
- MongoDB and Redis remain owned by the core payment flow, not by the relay

The change is required to make InfinitePay ingress reachable without moving financial decision-making out of the core service.

## 3. Scope

### 3.1 In Scope

- a new public Cloud Run relay service named `edn-webhook-dev`
- a new internal core route `POST /internal/payments/providers/infinitepay/webhook-reconcile`
- moving the public secret-path webhook edge off the financial authority runtime
- preserving the inbound request body byte-for-byte from relay edge to core reconciliation handler
- Cloud Run service-to-service authentication for relay-to-core invocation
- continued reuse of the existing payment reconciliation logic centered in `internal/payments/webhook.go`, with mandatory core hardening so the provider-verified transaction ID returned by `payment_check` is the canonical transaction identity for reconciliation, webhook payload `transaction_id` is treated only as a lookup hint and consistency check, and any persisted canonical `transaction_nsu` for the same order or invoice remains a hard integrity gate
- Secret Manager and configuration separation between relay-edge secrets and core provider credentials
- observability, failure handling, acceptance criteria, implementation sequencing, and rollback guidance

### 3.2 Out Of Scope

- redesign of checkout creation, plan listing, or order status APIs
- generic payments platform rewrites unrelated to webhook ingress and reconciliation
- moving payment status rules, order transitions, or provider verification logic into the relay
- adding MongoDB, Redis, or any durable business store to the relay service
- changing the public frontend contract described in `docs/frontend-integration.md`, except where cutover notes reference the webhook topology
- redesign of subscription activation or outbox workflows beyond what existing reconciliation already triggers

## 4. Non-Goals

This change must not become a broader payments rewrite.

The following are explicit non-goals:

- `edn-webhook-dev` must not decide payment status
- `edn-webhook-dev` must not update orders or payments
- `edn-webhook-dev` must not query MongoDB or Redis
- `edn-webhook-dev` must not call InfinitePay `payment_check`
- `edn-webhook-dev` must not semantically parse or interpret `order_nsu`, `transaction_id`, `transaction_nsu`, or `invoice_slug`
- `edn-webhook-dev` must not log the raw webhook payload or sensitive provider values
- the new internal core route must not be exposed as a public internet-facing provider endpoint

## 5. Assumptions And Invariants

The implementation defined by this document assumes the following:

- InfinitePay continues to deliver webhooks as HTTP `POST` requests with `Content-Type: application/json`.
- The public webhook URL contains an unguessable secret path segment.
- That secret path exists only on the public relay edge.
- In v1, if InfinitePay provides no signed webhook verification or documented source-IP allow-list, the internet-to-relay edge relies on that unguessable secret path plus the compensating controls defined in Section 16.3.
- Cloud Run service-to-service authentication is available and `run.invoker` can be granted to the `edn-webhook-dev` service account on `edn-core-dev`.
- A defense-in-depth internal token may exist, but it is not the primary authorization control.
- The real InfinitePay API token remains configured only in core because `internal/payments/infinitepay_adapter.go` uses it for `POST /payment_check` during authoritative verification.
- The status vocabulary in `internal/payments/types.go` remains the canonical payment and order status model.
- Existing reconciliation behavior in `internal/payments/webhook.go` is the canonical financial flow and must be reused where possible, but only after mandatory core hardening implements the canonical transaction identity model in Section 17.4 and preserves order not found, amount mismatch, and transaction identity mismatch as hard integrity gates.
- This change introduces no new system of record. MongoDB and Redis remain owned by core payment modules.

The following invariants are mandatory:

- provider payloads are never the financial source of truth
- only the core service may verify provider state and mutate payment or order state
- webhook payload `transaction_id` is only a lookup hint and consistency check and is never canonical truth
- the canonical transaction identity for reconciliation is the provider-verified transaction ID returned by `payment_check`; where the current core model stores that value as `transaction_nsu`, that persisted `transaction_nsu` is the canonical provider transaction identity
- if the webhook includes `transaction_id` and provider verification returns a different transaction ID, core must return `422 Unprocessable Entity` and perform no payment or order mutation
- if core already has a persisted canonical `transaction_nsu` for the same order or invoice, the verified provider transaction ID must match it or core must return `422 Unprocessable Entity` and perform no payment or order mutation
- if no canonical transaction identity is yet persisted for the same order or invoice, the verified provider transaction ID becomes the canonical value to persist
- relay forwarding must preserve the raw request body byte-for-byte
- relay logging must exclude the secret path, raw payload, and sensitive identifiers, and platform-managed request logging or tracing that would capture request URLs containing the secret path must be disabled, redacted, excluded from export or retention, or otherwise prevented from exposing the secret path
- relay provider-facing responses must be synthesized from a fixed public-safe allow-list and must not propagate core response bodies or headers
- malformed or unusable payloads, order not found, amount mismatch, and transaction identity mismatch must return deterministic non-retry-inducing `422 Unprocessable Entity` responses at both the core internal route and the provider-facing relay boundary
- lock acquisition timeout or concurrent reconcile conflict is a temporary retry-inducing failure that must return internal `5xx` and use the approved transition mapping of provider-facing `400 Bad Request`
- temporary downstream/core/provider verification failures use an approved transition mapping of `400 Bad Request` at the public relay to induce provider retry until lower-environment validation confirms or changes the policy before production cutover
- the internal reconcile route MUST NOT be mounted on any Cloud Run surface that also serves unauthenticated public traffic
- amount mismatch and transaction identity mismatch are hard integrity gates that MUST prevent payment or order mutation and MUST NOT map to `pending_review` or any other intermediate payment or order status in this webhook-reconcile path
- if InfinitePay later provides signed webhook verification or a documented source-IP allow-list, the relay must adopt one of those origin-authentication controls before or during production hardening
- if the platform cannot guarantee that the secret path is not exposed through platform-managed request logging or tracing, the path-secret design is not approved for production cutover until an alternative secret transport or equivalent control is adopted

## 6. Architectural Principles

### 6.1 Public Edge Is Minimal

`edn-webhook-dev` exists only to receive a public provider callback and forward it safely to the private authority. It must remain a thin ingress adapter.

### 6.2 Financial Authority Stays In Core

`edn-core-dev` remains the only authority allowed to:

- parse minimum provider identifiers for reconciliation
- deduplicate deliveries
- call InfinitePay `payment_check`
- validate amount and transaction identity
- update payments, orders, caches, and outbox state

### 6.3 Secrets Belong To Their Boundary

The secret path belongs to the public edge only. The InfinitePay API token belongs to the core only. These secrets serve different purposes and must not be merged.

### 6.4 Idempotency And Replay Safety Are Core Concerns

The relay is intentionally stateless. Durable replay safety, duplicate detection, and authoritative state transitions remain inside the core flow already implemented around `internal/payments/webhook.go`.

### 6.5 Channel Differences Stay In Adapters

Provider ingress differences belong in the relay adapter or HTTP boundary. Financial rules must remain in the core payment module.

## 7. TOGAF-Style Baseline Architecture

### 7.1 Baseline State

The baseline implementation shape is:

- public webhook ingress is mounted directly on the main payments/core runtimes in `cmd/app/main.go` and `cmd/payments-api/main.go`
- the public path is `/v1/webhooks/infinitepay/<secret>`
- `internal/payments/handler.go` performs edge validation and then calls `WebhookService.Handle`
- `internal/payments/webhook.go` performs minimum JSON parsing, Redis and MongoDB deduplication, order lookup, provider verification, payment upsert, order update, cache invalidation, and outbox enqueueing
- `internal/payments/infinitepay_adapter.go` performs `POST /payment_check` with the real InfinitePay API token
- configuration is currently loaded through `internal/platform/config/config.go`

### 7.2 Baseline Constraints

The baseline shape has the following constraints:

- the public provider ingress is coupled to the financial authority runtime
- the secret-path public webhook surface exists on the same runtime that owns database and provider credentials
- provider reachability and internal-ingress requirements are in tension
- deployment and security controls are broader than needed for a minimal provider ingress adapter

### 7.3 Baseline Risks

- a larger internet-exposed surface than required for the webhook use case
- less explicit separation between public edge validation and financial reconciliation
- harder least-privilege configuration because the same service mixes public ingress with core authority

## 8. TOGAF-Style Target Architecture

### 8.1 Target Topology

The approved target topology is:

```text
InfinitePay
  -> edn-webhook-dev (public Cloud Run, thin relay only)
  -> edn-core-dev (authenticated/private financial authority)
  -> MongoDB / Redis / payment state
  -> InfinitePay payment_check from core only
```

### 8.2 Target Responsibilities

`edn-webhook-dev` owns:

- public internet ingress for InfinitePay webhooks
- exact secret-path routing
- method validation
- content-type validation
- max body byte enforcement
- raw-byte forwarding to core
- safe metadata logging and request correlation

`edn-webhook-dev` does not own:

- JSON semantic parsing
- MongoDB or Redis access
- payment verification
- deduplication authority
- payment or order mutation
- financial status interpretation

`edn-core-dev` owns:

- authenticated internal webhook reconciliation endpoint
- reuse of the existing webhook reconciliation logic
- provider verification through `payment_check`
- duplicate delivery safety
- amount validation
- transaction identity validation
- authoritative payment and order mutation
- Redis cache and dedup behavior
- outbox and downstream financial side effects

### 8.3 Target Invariants

- The secret path must not appear on the core route.
- The internal route must not require the provider secret path.
- The relay must forward the exact bytes received, not a parsed or reserialized JSON document.
- The core must verify the transaction with InfinitePay before mutating business state.
- The core must use the real InfinitePay API token for `payment_check`.
- The relay must have no direct access to MongoDB, Redis, or provider verification credentials.
- The internal reconcile route MUST NOT be mounted on any Cloud Run surface that also serves unauthenticated public traffic.
- If the current `edn-core-dev` runtime still serves public routes on an unauthenticated Cloud Run surface, a deployment split is mandatory before provider cutover.
- The relay must discard inbound `Authorization`, `X-EDN-*`, `Forwarded`, `X-Forwarded-*`, and hop-by-hop headers and regenerate only its allow-listed outbound headers.

## 9. TOGAF-Style Transition Architecture

### 9.1 Transition Goal

The transition must introduce the relay and internal route without duplicating financial rules, without changing durable state ownership, and without creating a period where two public endpoints are both considered the active provider contract.

### 9.2 Transition Shape

The required transition is:

1. if the current `edn-core-dev` runtime still serves unauthenticated public routes, split deployment so the reconcile route lands on an authenticated-only Cloud Run surface before any provider cutover
2. add the new core internal reconciliation endpoint on that authenticated-only surface first
3. deploy the relay service without cutting provider traffic yet
4. grant `run.invoker` from the relay service account to the core service
5. configure the real InfinitePay token in core and confirm provider verification works through the internal path
6. validate in lower environments that success and duplicate-safe success return prompt `200` responses, deterministic local edge rejections remain `404`/`405`/`413`/`415`, malformed or unusable payloads and order not found return `422`, amount mismatch and transaction identity mismatch return `422` with no payment or order mutation, lock acquisition or concurrent reconcile conflict returns internal `5xx` and provider-facing `400`, and temporary downstream/core/provider verification failures use the approved transition `400` mapping
7. cut InfinitePay webhook delivery over to the relay URL
8. remove or disable the legacy direct public core webhook mounts in `cmd/app/main.go` and `cmd/payments-api/main.go` after cutover validation

### 9.3 Transition Rule

During transition, if a temporary rollback-only legacy public route is retained, it must use a separate fallback path and be explicitly marked transitional. The target-state secret path belongs only to the relay edge, and the internal reconcile route MUST NOT share a Cloud Run surface with any unauthenticated public traffic.

## 10. C4-Style Context View

### 10.1 System Context

Actors and systems for this slice are:

- InfinitePay as the external payment provider and webhook sender
- `edn-webhook-dev` as the only public webhook ingress service
- `edn-core-dev` as the internal financial authority for reconciliation
- MongoDB as the current durable payment/order state store already used by the payments slice
- Redis as the current cache, lock, and fast-path dedup store already used by the payments slice
- operations and backend maintainers as owners of Cloud Run, IAM, configuration, and cutover

### 10.2 Context Interaction Summary

- InfinitePay sends a webhook to `edn-webhook-dev`
- `edn-webhook-dev` validates only edge concerns and forwards opaque bytes to `edn-core-dev`
- `edn-core-dev` authenticates the relay caller and reconciles the payment
- `edn-core-dev` verifies with InfinitePay through `payment_check`
- `edn-core-dev` updates MongoDB and Redis-backed state as required

## 11. C4-Style Container View

### 11.1 Public Container: `edn-webhook-dev`

Runtime characteristics:

- Cloud Run service
- Cloud Run service with `maxScale` capped at 2
- public ingress
- source-aware throttling keyed by normalized caller source, with a minimum policy that no single source may hold more than 8 in-flight forward slots per instance or exceed 120 requests per rolling minute per instance; excess requests are shed locally before any core call
- application-level throttling or backpressure with a maximum of 32 in-flight forward calls per instance and no unbounded buffering
- short bounded forward timeouts of 3 seconds
- sanitized provider-facing responses only, with no core response body or header pass-through
- no durable business store
- no provider verification token
- no MongoDB dependency
- no Redis dependency

### 11.2 Private Authority Container: `edn-core-dev`

Runtime characteristics:

- authenticated-only Cloud Run service or other authenticated internal deployment surface for the core runtime
- protected by Cloud Run service-to-service authentication for the reconcile path
- must not share a Cloud Run surface with unauthenticated public routes
- owns provider verification credentials
- owns payment/order reconciliation logic
- owns MongoDB and Redis integration already used by the payments module

### 11.3 Supporting Containers

- InfinitePay API for `payment_check`
- MongoDB for payments, orders, webhook events, subscriptions, and outbox state already handled by core repositories
- Redis for fast-path dedup, locking, and status cache invalidation already handled by the core flow

## 12. C4-Style Component View

### 12.1 Relay Components

Recommended implementation structure:

- `cmd/webhook-relay/main.go` as the Cloud Run entrypoint
- a small internal package such as `internal/webhookrelay` for HTTP handling and forward-client logic

Relay components are:

- `RelayHandler`: validates method, exact secret path, exact media type allow-listing, and max bytes
- `ForwardClient`: sends the unchanged request body to the core internal route with Cloud Run identity
- `ResponseMapper`: maps local and core outcomes to the fixed public-safe provider response codes, headers, and minimal bodies
- `RelayLogger`: emits only safe metadata and correlation fields

### 12.2 Core Components

Core components are:

- internal reconcile HTTP handler mounted on `/internal/payments/providers/infinitepay/webhook-reconcile`
- canonical webhook reconciliation service centered in `internal/payments/webhook.go`
- provider adapter in `internal/payments/infinitepay_adapter.go`
- repositories and stores for orders, payments, webhook events, outbox, Redis locks, and dedup state

### 12.3 Reuse Rule

The preferred implementation is to make the new internal reconcile handler delegate directly to the existing webhook reconciliation service. A mandatory hardening/refactor must implement the canonical transaction identity model defined in Section 17.4: the provider-verified transaction ID returned by `payment_check` is canonical, webhook payload `transaction_id` is only a lookup hint and consistency check, and any mismatch against the verified or already-persisted canonical transaction identity remains a hard integrity gate. Amount mismatch and transaction identity mismatch must stay hard integrity gates before simple reuse is considered sufficient. If a refactor is needed, it must extract a reusable core application method. It must not copy financial rules into the relay.

## 13. Service Boundaries And Responsibilities

### 13.1 `edn-webhook-dev`

`edn-webhook-dev` must:

- expose only `POST /v1/webhooks/infinitepay/{secretPath}` for this integration
- mount the exact secret path string and avoid path-parameter enumeration patterns
- use a single opaque secret-path segment with at least 128 bits of entropy
- enforce `POST` only
- enforce `Content-Type` by parsing the media type and accepting only `application/json` with no parameters or a single case-insensitive `charset=utf-8` parameter
- enforce a bounded request-body size
- read the request body exactly once
- apply the minimum source-aware throttling and backpressure controls defined in Section 16.3, including no more than 32 in-flight forward calls per instance, no single normalized source consuming more than 8 of those slots, and no single normalized source exceeding 120 requests per rolling minute per instance
- use a short bounded timeout of 3 seconds for the forward call to core
- forward the raw bytes unchanged to core
- discard inbound `Authorization`, `X-EDN-*`, `Forwarded`, `X-Forwarded-*`, and hop-by-hop headers before forwarding
- regenerate only the allow-listed outbound headers required for correlation and content typing
- synthesize only the public-safe response codes, headers, and minimal bodies defined in this specification
- never propagate core response headers or response bodies to InfinitePay
- avoid logging the raw payload, secret path, `order_nsu`, `transaction_id`, `transaction_nsu`, `invoice_slug`, provider tokens, or internal tokens
- return only the approved public-safe response mapping and the validated production retry policy

`edn-webhook-dev` must not:

- call `json.Unmarshal` or `json.Marshal` on the provider payload
- inspect business identifiers for decision-making
- query or mutate MongoDB or Redis
- own deduplication or reconciliation state
- call InfinitePay `payment_check`

### 13.2 `edn-core-dev`

`edn-core-dev` must:

- expose `POST /internal/payments/providers/infinitepay/webhook-reconcile`
- mount that route only on an authenticated-only Cloud Run surface that serves no unauthenticated public traffic
- require authenticated invocation from the relay service account through Cloud Run service-to-service auth
- optionally validate a defense-in-depth internal token if configured
- enforce bounded request-body size and safe raw-body reading before passing the forwarded raw body into the canonical reconciliation flow
- parse only the minimum identifiers required for reconciliation inside the core flow and treat webhook payload `transaction_id` only as a lookup hint and consistency check
- verify the payment with InfinitePay `payment_check`
- deduplicate duplicate deliveries
- return internal `422 Unprocessable Entity` with no payment or order mutation when no authoritative order or invoice can be resolved from the usable lookup hints
- validate amount exactly against order state and, on mismatch, return internal `422 Unprocessable Entity` with no payment or order mutation
- treat the provider-verified transaction ID returned by `payment_check` as the canonical transaction identity for reconciliation
- if webhook payload `transaction_id` is present and differs from the provider-verified transaction ID, return internal `422 Unprocessable Entity` with no payment or order mutation
- if a canonical `transaction_nsu` is already persisted for the same order or invoice, require the verified provider transaction ID to match it or return internal `422 Unprocessable Entity` with no payment or order mutation
- if no canonical transaction identity is yet persisted for the same order or invoice, persist the verified provider transaction ID as the canonical `transaction_nsu` or equivalent stored provider transaction identity before downstream mutation
- classify lock acquisition timeout or concurrent reconcile conflict as a temporary reconciliation failure that returns internal `5xx` and performs no duplicate side effect
- treat amount mismatch and transaction identity mismatch as integrity-gate failures that MUST NOT map to `pending_review`; this requirement supersedes any legacy core behavior for this webhook-reconcile path
- update payment and order state only after verification

### 13.3 Data And Event Boundaries

- MongoDB collections remain owned by the core runtime and repositories already used in the payments flow.
- Redis remains an acceleration and coordination dependency of the core flow only.
- Any future event publication remains owned by core outbox logic, not by the relay.

## 14. Workflow Specification

### 14.1 Public Ingress Flow

1. InfinitePay sends `POST /v1/webhooks/infinitepay/{secretPath}` to `edn-webhook-dev`.
2. The relay confirms the method is `POST`.
3. The relay parses the `Content-Type` media type and accepts only `application/json` with no parameters or a single case-insensitive `charset=utf-8` parameter.
4. The relay confirms the body does not exceed the configured byte limit.
5. The relay reads the body as opaque bytes.
6. The relay applies source-aware throttling and backpressure before any core call.
7. The relay discards inbound authentication, forwarding, and hop-by-hop headers and prepares only its allow-listed outbound headers.
8. The relay obtains an ID token for the core audience using its Cloud Run service identity.
9. The relay forwards the unchanged bytes to `POST /internal/payments/providers/infinitepay/webhook-reconcile` on `edn-core-dev`.
10. The relay synthesizes and returns only the fixed public-safe response code, headers, and minimal body defined by this specification.

### 14.2 Internal Reconciliation Flow

1. Cloud Run authenticates the relay caller before the request reaches core business logic.
2. The core internal handler enforces a bounded body reader and reads the forwarded raw body safely.
3. The handler delegates to the canonical reconciliation service centered in `internal/payments/webhook.go`.
4. The core extracts only the minimum provider identifiers required to reconcile the event and treats webhook payload `transaction_id` only as a lookup hint and consistency check when present.
5. The core runs duplicate detection using the current Redis and MongoDB strategy, with MongoDB-backed dedup remaining the fallback when Redis is unavailable.
6. The core loads the authoritative order or invoice candidate. If no authoritative order can be resolved from the usable lookup hints, reconciliation stops with internal `422 Unprocessable Entity` and no payment or order mutation.
7. The core verifies the payment with InfinitePay `payment_check` using the real API token configured in core.
8. The provider-verified transaction ID returned by `payment_check` is the canonical transaction identity for reconciliation. Where the current core model stores that value as `transaction_nsu`, that persisted `transaction_nsu` is the canonical provider transaction identity.
9. The core validates that the verified payment amount matches the order amount.
10. If the webhook payload includes `transaction_id`, it must match the provider-verified transaction ID or reconciliation stops with internal `422 Unprocessable Entity` and no payment or order mutation.
11. If core already has a persisted canonical `transaction_nsu` for the same order or invoice, the verified provider transaction ID must match it or reconciliation stops with internal `422 Unprocessable Entity` and no payment or order mutation.
12. If no canonical transaction identity is yet persisted for the same order or invoice, the verified provider transaction ID becomes the canonical value to persist before downstream mutation.
13. Any order-not-found, amount-mismatch, or transaction-identity-mismatch condition stops reconciliation, returns internal `422 Unprocessable Entity`, and prevents any payment or order mutation.
14. If exclusive lock acquisition fails or a concurrent reconcile conflict is detected, the failing attempt returns internal `5xx`, performs no duplicate side effect, and relies on the approved public `400` transition mapping.
15. The core upserts payment state, updates order state, invalidates cache, applies lock behavior, and records webhook processing.
16. If the payment is approved and valid, the core continues the existing outbox/event behavior exactly once.

Legacy-behavior note:

Any current relay-path behavior that maps amount mismatch or transaction identity mismatch to `pending_review` is superseded by this specification and is non-compliant for production cutover.

### 14.3 Financial Rule Ownership

The relay is not allowed to decide any of the following:

- whether a payload is a duplicate
- whether a payment is approved, rejected, pending, refunded, or chargeback
- whether an order should transition to `paid`, `failed`, or another status

For this webhook-reconcile path, order not found, amount mismatch, and transaction identity mismatch are not discretionary status decisions. Core MUST return `422 Unprocessable Entity`, MUST NOT mutate payment or order state, and MUST NOT translate any of those conditions into `pending_review`. This requirement supersedes any legacy core behavior and the required hardening or refactor is not complete until that behavior is enforced.

All remaining payment-status decisions remain owned by the core payment flow and status types already defined in `internal/payments/types.go`.

## 15. API Contracts

### 15.1 Public Relay Contract

Endpoint:

- `POST /v1/webhooks/infinitepay/{secretPath}`

Request rules:

- method must be `POST`
- path must match the exact configured secret path
- `Content-Type` must be parsed as a media type and accepted only when the media type is exactly `application/json` with no parameters or a single case-insensitive `charset=utf-8` parameter
- body must not exceed the configured maximum bytes
- body is treated as opaque raw bytes

Public relay response rules:

| Condition | Response | Public body | Notes |
| --- | --- | --- | --- |
| secret path mismatch | `404 Not Found` | `{"error":"not_found"}` | must not leak the expected secret path |
| method is not `POST` | `405 Method Not Allowed` | `{"error":"method_not_allowed"}` | no core call |
| content-type allow-list validation fails | `415 Unsupported Media Type` | `{"error":"unsupported_media_type"}` | no core call |
| body exceeds max bytes | `413 Payload Too Large` | `{"error":"payload_too_large"}` | no core call |
| relay sheds the request due to source-aware throttling or backpressure | `400 Bad Request` | `{"error":"bad_request"}` | temporary local protection response under the approved transition policy; no core call |
| core processed successfully | `200 OK` | `{"status":"ok"}` | prompt synchronous success |
| core reports known duplicate | `200 OK` | `{"status":"ok"}` | duplicate-safe success uses the same minimal public body |
| core reports malformed or unusable payload | `422 Unprocessable Entity` | `{"error":"unprocessable_entity"}` | deterministic core validation outcome |
| core reports order not found | `422 Unprocessable Entity` | `{"error":"unprocessable_entity"}` | deterministic unusable payload with no payment or order mutation |
| core reports amount mismatch | `422 Unprocessable Entity` | `{"error":"unprocessable_entity"}` | deterministic integrity-gate failure with no payment or order mutation |
| core reports transaction identity mismatch | `422 Unprocessable Entity` | `{"error":"unprocessable_entity"}` | deterministic integrity-gate failure; includes webhook payload `transaction_id` mismatch against the provider-verified transaction ID or mismatch against an already-persisted canonical transaction identity |
| core reports lock acquisition timeout or concurrent reconcile conflict | `400 Bad Request` | `{"error":"bad_request"}` | approved transition retry-inducing mapping for temporary exclusive-ownership failure; no false success is allowed |
| relay cannot forward to core, core auth fails, or core or provider verification fails temporarily | `400 Bad Request` | `{"error":"bad_request"}` | approved transition retry-inducing mapping; public response stays sanitized and does not expose internal details |

Public response sanitization rule:

The relay MUST synthesize the provider-facing response and MUST NOT forward any core response header or response body to InfinitePay. The only application-controlled provider-facing headers allowed are `Content-Type: application/json` and optional `X-EDN-Relay-Request-ID`. Platform-generated transport headers such as `Date` or `Content-Length` are not part of the application contract. The only application-controlled provider-facing bodies allowed are the fixed minimal JSON bodies listed in the table above. Internal validation, authentication, authorization, and provider-verification errors MUST be translated only through those public-safe responses.

Implementation rule:

The relay must forward the exact `[]byte` read from the inbound request. It must not reserialize JSON, normalize whitespace, rewrite field order, or synthesize provider identifiers.

Forwarded-header rule:

The relay MUST discard inbound `Authorization`, any inbound `X-EDN-*`, `Forwarded`, `X-Forwarded-*`, and hop-by-hop headers such as `Connection`, `Proxy-Connection`, `Keep-Alive`, `TE`, `Trailer`, `Transfer-Encoding`, and `Upgrade`. It MUST then emit only its own allow-listed outbound headers to core.

### 15.2 Internal Core Contract

Endpoint:

- `POST /internal/payments/providers/infinitepay/webhook-reconcile`

Authentication rules:

- primary control is Cloud Run service-to-service authentication
- only the `edn-webhook-dev` service account is granted `run.invoker` on the core target
- the relay sends a Google-signed identity token for the core audience
- an optional internal token header may be required as a secondary control, but it is not the primary authorization mechanism

Authentication-denial evidence rules:

- when Cloud Run IAM denies the invocation before application code runs, the denial is evidenced by Cloud Audit Logs or equivalent platform audit logs rather than application logs
- if an optional defense-in-depth internal token is enabled and fails after IAM admission, the core application must emit the audit/security log required by this specification

Internal request rules:

- method must be `POST`
- body is the unchanged raw payload received from InfinitePay
- `Content-Type` must be forwarded unchanged from the public request after successful relay allow-list validation
- the internal route must not require or expose the public secret path
- the internal route must enforce a bounded request-body size and safe raw-body reading
- the internal route MUST NOT be mounted on any Cloud Run surface that also serves unauthenticated public traffic

Required forwarded header allow-list:

- `Content-Type`
- `X-EDN-Relay-Request-ID`
- `X-EDN-Relay-Received-At`
- optional defense-in-depth token header when enabled

Internal response rules:

| Condition | Response | Notes |
| --- | --- | --- |
| authenticated processing success | `200 OK` | synchronous reconcile completed |
| authenticated known duplicate | `200 OK` | duplicate is idempotent success |
| malformed or unusable payload | `422 Unprocessable Entity` | deterministic validation failure with no payment or order mutation |
| order not found | `422 Unprocessable Entity` | deterministic unusable payload with no payment or order mutation |
| amount mismatch | `422 Unprocessable Entity` | deterministic integrity-gate failure with no payment or order mutation and no `pending_review` |
| transaction identity mismatch | `422 Unprocessable Entity` | deterministic integrity-gate failure; includes mismatch between webhook payload `transaction_id`, the provider-verified transaction ID, and any already-persisted canonical `transaction_nsu` |
| authentication or authorization failure | `401` or `403` | when Cloud Run IAM rejects before application code runs, the evidence is Cloud Audit Logs or equivalent platform audit logs; if an optional internal token fails after IAM admission, the core application must emit an audit log; relay maps this to provider-facing `400 Bad Request` without exposing auth details |
| lock acquisition timeout or concurrent reconcile conflict | `503 Service Unavailable` | temporary retry-inducing failure with no duplicate side effect; relay maps this to provider-facing `400 Bad Request` under the approved transition policy |
| temporary core or provider verification failure | `5xx` | relay maps this to provider-facing `400 Bad Request` during the approved transition behavior |

### 15.3 Public Documentation Rule

The internal reconcile route must not be published in the public OpenAPI surface. The public docs route and public API contract remain separate concerns from this internal webhook boundary.

## 16. Security Model

### 16.1 Trust Boundaries

Trust boundaries are:

- internet to public relay
- relay to authenticated core route
- core to InfinitePay verification API
- core to MongoDB and Redis

### 16.2 Required Controls

- untrusted external payloads are accepted only at the relay edge
- secret path checking occurs only at the public relay edge
- the relay service account receives only the permissions required to invoke core and read its own secrets
- the relay has no MongoDB secret, no Redis secret, and no InfinitePay API token
- the core has the real InfinitePay API token because it must call `payment_check`
- the core route requires authenticated invocation through Cloud Run IAM
- the core route MUST NOT be mounted on any Cloud Run surface that also serves unauthenticated public traffic
- the public relay must enforce the compensating controls in Section 16.3, including `maxScale` 2, source-aware throttling, a maximum of 32 in-flight forward calls per instance, no unbounded buffering, a downstream timeout of 3 seconds, and the alert thresholds defined in Section 19.4
- the relay must discard inbound `Authorization`, `X-EDN-*`, `Forwarded`, `X-Forwarded-*`, and hop-by-hop headers before regenerating only its allow-listed outbound headers
- the relay must synthesize provider-facing responses from the fixed allow-list defined in Section 15.1 and must not proxy any core response headers or response body
- the internal route must enforce bounded request-body size and safe raw-body reading
- Cloud Run IAM-denied internal invocations are evidenced through Cloud Audit Logs or equivalent platform audit logs because the request is rejected before application code runs
- if an optional internal token is enabled and fails after IAM admission, the core application must create an audit/security log record without logging request bodies or secrets
- raw payload and sensitive values must not be written to relay logs
- the secret path must not appear in logs, metrics labels, trace attributes, or error bodies
- platform-managed request logging or tracing that would capture request URLs containing the secret path must be disabled, redacted, excluded from export or retention, or otherwise prevented from exposing the secret path
- if the platform cannot guarantee that control, the path-secret design is not approved for production cutover until an alternative secret transport or equivalent control is adopted

### 16.3 Public Edge Origin Limitation And Compensating Controls

For v1, if InfinitePay does not provide a signed webhook verification mechanism or a documented source-IP allow-list, the internet-to-relay edge relies on an unguessable secret path plus compensating controls. That limitation is accepted only with all of the following minimum controls in place:

- the secret path is a single opaque segment with at least 128 bits of entropy
- planned secret-path rotation uses a bounded overlap window that does not exceed 15 minutes
- suspected secret leakage uses an immediate revocation and provider-cutover runbook that bypasses any planned overlap window
- the relay Cloud Run service is deployed with `maxScale` capped at 2
- source-aware throttling keys on normalized caller source derived from platform networking metadata and, at minimum, prevents one source from holding more than 8 in-flight forward slots per instance or exceeding 120 requests per rolling minute per instance
- the relay enforces a maximum of 32 in-flight forward calls per instance with no unbounded buffering
- the relay enforces a 3-second forward timeout to core
- suspicious-burst alerting from Section 19.4 is configured and actionable

If InfinitePay later provides signed webhook verification or a documented source-IP allow-list, the relay must adopt one of those origin-authentication controls before or during production hardening and record the resulting control change before production promotion.

### 16.4 Optional Defense In Depth

If an internal token is retained, it must be treated as a secondary control only:

- stored in Secret Manager
- validated in constant time
- never logged
- never treated as a substitute for Cloud Run service-to-service authentication
- audited at the application layer only when the token check fails after IAM admission

### 16.5 Secret Rotation And Revocation

- the public secret path must be stored and rotated through Secret Manager-backed configuration
- planned public secret-path rotation must support a bounded overlap window that does not exceed 15 minutes before the prior path is revoked
- suspected secret leakage must use an immediate revocation and provider-cutover runbook that bypasses the overlap window and replaces the compromised path without waiting for normal rotation cadence
- any optional defense-in-depth internal token must also have documented rotation and revocation steps
- operations must maintain a runbook for rotation, revocation, compromise response, and coordinated redeploy or config refresh for these controls

## 17. Data Ownership, Idempotency, And Consistency

### 17.1 Relay Data Ownership

`edn-webhook-dev` owns no durable business data. It must not create a MongoDB collection, Redis keyspace, or filesystem-backed spool for webhook reconciliation.

### 17.2 Core Data Ownership

The authoritative payment flow remains in core and continues using the existing stores and repositories referenced by the payments module. Existing payment-related state already handled by the core includes:

- orders
- payments
- `webhook_events`
- subscriptions
- `outbox_events`
- Redis-based dedup and locking state

### 17.3 Idempotency Rules

- the relay is stateless and not authoritative for deduplication
- the core must continue to deduplicate by transaction identity and event hash
- Redis may accelerate deduplication and locking, but MongoDB-backed webhook-event records remain the fallback duplicate check when Redis is unavailable
- duplicate delivery must remain a successful idempotent outcome
- authoritative reconciliation must remain replay-safe under provider retries and relay retries
- lock behavior must remain core-owned and must not permit duplicate downstream side effects
- outbox persistence and downstream publish behavior must remain coupled to the authoritative core reconciliation path so duplicate deliveries do not create duplicate publishes

### 17.4 Verification Rules

The core must not trust the webhook payload as final truth. Before updating business state, the core must:

- verify the payment through InfinitePay `payment_check`
- treat the provider-verified transaction ID returned by `payment_check` as the canonical transaction identity for reconciliation; where current core persistence names that field `transaction_nsu`, that stored `transaction_nsu` is the canonical provider transaction identity
- treat webhook payload `transaction_id` only as a lookup hint and consistency check and never as the source of truth
- if the webhook includes `transaction_id` and the verified provider transaction ID is different, return internal `422 Unprocessable Entity`, return provider-facing `422 Unprocessable Entity`, and perform no payment or order mutation
- if core already has a persisted canonical `transaction_nsu` for the same order or invoice, require the verified provider transaction ID to match it or return internal `422 Unprocessable Entity`, return provider-facing `422 Unprocessable Entity`, and perform no payment or order mutation
- if no canonical transaction identity is yet persisted for the same order or invoice, persist the verified provider transaction ID as the canonical `transaction_nsu` or equivalent stored provider transaction identity before downstream mutation
- confirm the verified amount matches the authoritative order amount, with any amount mismatch treated as a hard integrity gate that blocks payment and order mutation
- confirm the verified transaction identity remains consistent with the intended transaction identity and any already-persisted canonical transaction identity, with any mismatch treated as a hard integrity gate that blocks payment and order mutation

If current core code needs refactoring to make that validation explicit and reusable, the refactor is mandatory, belongs in core, and must be completed before simple reuse of the existing webhook flow is considered sufficient.

### 17.5 Operational Data Note

- no new collections are introduced in the first relay release
- existing core ownership of `webhook_events` and `outbox_events` remains unchanged
- no TTL change is introduced in the first relay release
- retention and index decisions from the existing payments slice remain in force unless separately approved

## 18. Cloud Run, IAM, And Deployment Model

### 18.1 Relay Service Deployment

`edn-webhook-dev` must be deployed as a separate Cloud Run service.

Required characteristics:

- public ingress enabled
- `maxScale` capped at 2
- dedicated service account
- mandatory source-aware throttling keyed by normalized caller source with a minimum policy of at most 8 in-flight forward slots per source per instance and at most 120 requests per rolling minute per source per instance
- mandatory application-level throttling or backpressure with a maximum of 32 in-flight forward calls per instance and no unbounded buffering
- short bounded timeout of 3 seconds for the forward call to core
- no MongoDB or Redis runtime dependency
- no InfinitePay API token secret mounted
- platform-managed request logging or tracing that would capture request URLs containing the secret path disabled, redacted, excluded from export or retention, or otherwise prevented from exposing the secret path; otherwise the path-secret design is not approved for production cutover
- configuration limited to edge secret path, core target URL or audience, timeout, and max body bytes

### 18.2 Core Service Deployment

`edn-core-dev` is the financial authority target for the relay hop.

Required characteristics for the reconcile path:

- authenticated invocation required
- the reconcile route MUST NOT be mounted on any Cloud Run surface that also serves unauthenticated public traffic
- if the current `edn-core-dev` runtime still serves public routes on an unauthenticated surface, a deployment split is mandatory before provider cutover
- `run.invoker` granted to the relay service account
- real InfinitePay API token configured in core
- MongoDB and Redis configuration remain in core only

### 18.3 Cloud Build And Runtime Assets

`cloudbuild.development.yaml` currently builds and deploys `cmd/app`, `cmd/subscription-worker`, and `cmd/payments-api`. The target state must add a relay build and deploy step for the new public service.

Deployment assets must also include:

- Secret Manager wiring for the relay secret path
- Secret Manager wiring for any optional internal relay token
- Secret Manager wiring for the real InfinitePay API token in core
- IAM binding that grants `roles/run.invoker` from the relay service account to the core target
- runbooks for rotation and revocation of the public secret path and any optional defense-in-depth token

### 18.4 Configuration Separation

`internal/platform/config/config.go` currently carries payment settings such as `WebhookSecretPath`, `InternalVerifyAuthToken`, `RequestBodyMaxBytes`, and `InfinitePay.APIToken`.

Target-state configuration must keep these concerns separated by runtime:

- relay runtime loads the secret path and forwarding configuration only
- core runtime loads the real InfinitePay API token and provider base URL
- the relay must not load or require the InfinitePay API token

## 19. Observability

### 19.1 Logging

Relay logs must be structured and safe. They may include:

- request correlation ID
- service name
- HTTP status class
- request size
- content-type classification
- forward latency
- core response class

Relay logs must not include:

- raw payload
- secret path
- order identifiers
- transaction identifiers
- invoice identifiers
- provider tokens
- internal tokens

Application-level log redaction is not sufficient on its own. Platform-managed request logging and tracing that would capture request URLs containing the secret path MUST be disabled, redacted, excluded from export or retention, or otherwise prevented from exposing the secret path. If the platform cannot guarantee that control, the path-secret design is not approved for production cutover until an alternative secret transport or equivalent control is adopted.

Core logs may continue to emit operational payment information, but they must also avoid raw payload logging and secret leakage. When Cloud Run IAM denies invocation before application code runs, the evidence is Cloud Audit Logs or equivalent platform audit logs with caller identity, route, and denial outcome. If an optional internal token is enabled and fails after IAM admission, the core application must emit the audit/security log entry with caller identity metadata, request correlation, route, and denial outcome without logging body bytes or secrets.

### 19.2 Metrics

Required metrics:

- relay request count by outcome
- relay local rejection count by `404`/`405`/`413`/`415`
- relay source-aware throttle count
- relay backpressure or shed-load count
- relay forward latency
- relay forward timeout count
- relay upstream-to-core failure count
- relay provider-facing `400` temporary-failure count
- relay suspicious spike indicators for request volume, rejection volume, and source-aware throttle activation
- core reconcile success count
- core duplicate webhook count
- core provider verification latency
- core provider verification error count
- core order-not-found count
- core amount mismatch count
- core transaction-identity-mismatch count
- core lock-acquisition-or-concurrent-conflict count

### 19.4 Alerting

Mandatory alerts must exist for:

- at least 20 invalid path, method, or content-type rejections within 5 minutes
- at least 10 source-aware throttle activations within 1 minute
- at least 5 provider-facing temporary-failure `400` responses within 5 minutes
- sustained relay backpressure or shed-load for 1 minute or more
- denied internal invocations on the core reconcile route

### 19.3 Tracing And Correlation

The relay should forward a correlation identifier to core so one provider delivery can be traced across both services without exposing the secret path or raw payload.

## 20. Failure Handling

### 20.1 Edge Validation And Protection Failures

The relay handles the following locally and does not call core:

- invalid secret path
- invalid method
- invalid content-type
- oversized request body
- source-aware throttling or backpressure shed due to suspicious burst or capacity protection

When the relay sheds a request for source-aware throttling or backpressure, it must still use the approved temporary provider-facing `400 Bad Request` mapping from Section 15.1. The relay must not emit a false `200 OK` for any request it did not forward and reconcile successfully.

### 20.2 Downstream Core Failures

When the relay cannot reach core, or when core returns a temporary failure, the approved transition implementation must return `400 Bad Request` to InfinitePay in order to induce provider retry according to current stakeholder guidance until lower-environment validation confirms or changes that policy.

That provider-facing `400 Bad Request` response MUST use only the fixed public-safe headers and minimal body defined in Section 15.1. The relay MUST NOT forward core response headers, response bodies, or internal auth details to the provider.

The implementation must not assume that returning `200` after a failed downstream call is safe. If the core did not reconcile the event, a false success could permanently lose the webhook.

Lock acquisition timeout or concurrent reconcile conflict is classified as one of those temporary failures. Core must return internal `5xx` for that condition, and the relay must surface the approved provider-facing `400 Bad Request` transition mapping rather than a false success.

### 20.3 Duplicate Delivery

Duplicate delivery is a success case, not an error case. Core deduplication remains authoritative and the relay must surface duplicate-safe success once core confirms it.

### 20.4 Provider Verification Failure

If `payment_check` fails because the provider is unavailable or returns a temporary error, core should classify that as a temporary reconciliation failure. The relay response to InfinitePay then follows the approved transition `400` mapping until lower-environment validation confirms or changes the production policy.

### 20.5 Deterministic Core Validation Failures

Deterministic malformed or unusable payloads MUST return internal `422 Unprocessable Entity` and provider-facing `422 Unprocessable Entity`. Order not found MUST also return internal `422 Unprocessable Entity` and provider-facing `422 Unprocessable Entity` with no payment or order mutation. Amount mismatch and transaction identity mismatch, including webhook payload `transaction_id` mismatch against the provider-verified transaction ID and mismatch against an already-persisted canonical transaction identity, MUST also return internal `422 Unprocessable Entity` and provider-facing `422 Unprocessable Entity`, MUST NOT mutate payment or order state, and MUST NOT be treated as `pending_review` or as temporary retry-inducing conditions.

## 21. Retry Semantics Checkpoint

Before production cutover is finalized, the team must confirm InfinitePay retry and timeout behavior for:

- `2xx` responses
- `400` responses used by the approved transition failure mapping
- `422` responses for deterministic malformed or unusable payloads, order not found, amount mismatch, and transaction identity mismatch
- `4xx` responses
- `5xx` responses
- connection timeout or connection reset
- delayed responses near the provider timeout threshold

This checkpoint is mandatory because the approved transition `400` mapping is an implementation decision that still requires empirical lower-environment validation before production.

Normative rule:

- implementation must use the approved transition mapping of provider-facing `400 Bad Request` for temporary downstream/core/provider verification failures until lower-environment validation confirms or changes it
- implementation must use the approved transition mapping of provider-facing `400 Bad Request` for lock acquisition timeout or concurrent reconcile conflict until lower-environment validation confirms or changes it
- implementation must use provider-facing `422 Unprocessable Entity` for deterministic malformed or unusable payloads, order not found, amount mismatch, and transaction identity mismatch, with no payment or order mutation for the integrity-gate failures
- production promotion must not occur until timeout and retry behavior are validated, the final production policy is recorded, and any required policy change is reflected in this specification

## 22. Well-Architected Tradeoffs

### 22.1 Operational Excellence

The relay separates a narrow public edge concern from the core financial flow. That improves operational reasoning, deployment blast radius, and incident triage because ingress failures and reconciliation failures become distinguishable.

### 22.2 Security

The target shape reduces secret and datastore exposure at the public edge. Least privilege is stronger because the relay does not need database credentials or provider verification credentials.

### 22.3 Reliability

Reliability improves because authoritative deduplication and verification remain in one core flow instead of being split across services. The reliability gating item is lower-environment validation of the approved transition `400` mapping and timeout budget before production promotion.

### 22.4 Performance Efficiency

The relay adds one network hop, but the hop is small and avoids loading database or provider verification concerns into the public edge. This is an acceptable trade for clearer boundaries and safer public exposure.

### 22.5 Cost

The relay adds another Cloud Run service, but it is intentionally thin, stateless, and inexpensive compared to duplicating or widening the core surface. The added cost is justified by tighter security and cleaner operational separation.

## 23. Zachman-Style Completeness Check

| Dimension | Specification Answer |
| --- | --- |
| Data | raw webhook body is opaque at the relay; orders, payments, webhook events, outbox state, and Redis dedup remain core-owned |
| Process | receive, edge-validate, forward, authenticate, reconcile, verify with `payment_check`, dedupe, update payment/order, publish downstream effects |
| Location | internet to public Cloud Run relay, then authenticated Cloud Run core, then core-owned MongoDB, Redis, and InfinitePay API |
| People | InfinitePay sends events; backend engineers implement; platform operators manage Cloud Run, IAM, and secrets; finance/business operators depend on core-authoritative status |
| Timing | webhook delivery is synchronous from relay to core; retries are provider-driven; dedup must tolerate duplicate deliveries across time; cutover is staged |
| Motivation | make InfinitePay ingress reachable without moving financial authority out of core and without widening the public blast radius of sensitive payment logic |

## 24. Acceptance Criteria

Implementation is complete only when all of the following are true:

- `edn-webhook-dev` exists as a separate public Cloud Run service for InfinitePay ingress
- the relay exposes `POST /v1/webhooks/infinitepay/{secretPath}` and nothing in the relay decides payment state
- the secret path exists only on the relay edge in the target state
- `edn-core-dev` exposes `POST /internal/payments/providers/infinitepay/webhook-reconcile`
- the internal reconcile route is not externally invokable in the deployed environment and that boundary is proven by deployed-environment negative invocation checks plus Cloud Audit Logs or equivalent platform audit-log evidence of IAM denial
- the core reconcile route is protected by Cloud Run service-to-service authentication with `run.invoker` granted to the relay service account
- the core reconcile route is not mounted on any Cloud Run surface that also serves unauthenticated public traffic, and any required deployment split is complete before cutover
- the real InfinitePay API token is configured in core and is used for `payment_check`
- the relay preserves the raw request body byte-for-byte and does not reserialize JSON
- failing-first tests prove the relay strips inbound `Authorization`, any inbound `X-EDN-*`, `Forwarded`, any inbound `X-Forwarded-*`, and hop-by-hop headers such as `Connection`, `Proxy-Connection`, `Keep-Alive`, `TE`, `Trailer`, `Transfer-Encoding`, and `Upgrade` before forwarding to core, and forwards only the internal header allow-list defined in Section 15.2
- failing-first tests prove the relay returns to InfinitePay only the provider-facing header allow-list defined in Section 15.1, only the fixed minimal public-safe JSON body mapped for the outcome, and never propagates any upstream core response header or response body
- the relay uses a single opaque secret-path segment with at least 128 bits of entropy, maintains a bounded rotation overlap window, and has an immediate revocation and cutover runbook for suspected secret leakage
- the relay is deployed with `maxScale` capped at 2 and enforces source-aware throttling, a maximum of 32 in-flight forward calls per instance, a 3-second forward timeout, and the alert thresholds defined in Section 19.4
- the relay does not query MongoDB or Redis and does not log raw payload or sensitive values
- if optional internal token validation is enabled, IAM-admitted token failures are application-audited, while IAM-denied calls are evidenced in Cloud Audit Logs or equivalent platform audit logs
- core reuses the existing webhook reconciliation logic from `internal/payments/webhook.go` only after the mandatory hardening/refactor that implements the canonical transaction identity model from Section 17.4
- core remains the only source of truth for deduplication, canonical transaction identity persistence, amount validation, transaction validation, lock behavior, outbox persistence, and payment or order mutation
- malformed or unusable payloads, order not found, amount mismatch, and transaction identity mismatch return deterministic `422 Unprocessable Entity` responses at both the internal core route and the provider-facing relay boundary as defined in this specification
- amount mismatch and transaction identity mismatch are enforced as mandatory integrity gates before any payment or order mutation and never map to `pending_review`
- lock acquisition timeout or concurrent reconcile conflict is classified as temporary internal `5xx` and provider-facing `400 Bad Request` under the approved transition policy
- no new collections are introduced, ownership of `webhook_events` and `outbox_events` remains in core, and no TTL change is introduced in the first relay release
- retention and index decisions from the existing payments slice remain in force unless separately approved
- the approved transition `400` mapping for temporary failures is implemented, and lower-environment validation records the final timeout and retry behavior before production cutover
- platform-managed request logging or tracing does not expose the secret path, or production cutover is blocked until an alternative secret transport or equivalent control is adopted
- if InfinitePay later provides signed webhook verification or a documented source-IP allow-list, one of those origin-authentication controls is adopted before or during production hardening
- the legacy direct public core webhook mounts in `cmd/app/main.go` and `cmd/payments-api/main.go` are removed or disabled after cutover

## 25. Implementation Slices

Implementation order is fixed for this change:

### 25.1 Slice 1: Create `edn-webhook-dev` Minimal Service

- add a new relay runtime under `cmd/`
- add a small internal relay package for request validation and forwarding
- implement exact secret-path routing, method check, media-type parsing that accepts only `application/json` with no parameters or a single `charset=utf-8` parameter, max-body enforcement, and forbidden-header stripping
- configure relay deployment with `maxScale` capped at 2
- add source-aware throttling keyed by normalized caller source with a minimum policy of at most 8 in-flight forward slots per source per instance and at most 120 requests per rolling minute per source per instance
- add application-level throttling or backpressure with a maximum of 32 in-flight forward calls per instance, no unbounded buffering, and a 3-second bounded forward timeout
- implement raw-byte forwarding with no JSON semantic parsing
- implement the fixed public-safe provider response mapping and prevent any core response body or header pass-through

### 25.2 Slice 2: Create Internal Core Reconciliation Endpoint On An Authenticated-Only Surface

- add `POST /internal/payments/providers/infinitepay/webhook-reconcile`
- keep the route internal, authenticated, and off any Cloud Run surface that serves unauthenticated public traffic
- complete any required deployment split before provider cutover
- ensure the handler enforces bounded raw-body reading and delegates to the canonical core flow

### 25.3 Slice 3: Reuse Existing Core Webhook Logic

- reuse `internal/payments/webhook.go` wherever possible
- add the mandatory hardening/refactor that implements the canonical transaction identity model from Section 17.4, including webhook `transaction_id` hint handling and persisted canonical `transaction_nsu` enforcement
- keep amount mismatch and transaction identity mismatch as hard integrity gates before mutation
- if refactoring is required, extract a reusable core method rather than duplicating logic
- keep all financial rules in core

### 25.4 Slice 4: Configure IAM `run.invoker` For Relay To Core

- create or confirm the relay service account
- grant that service account `roles/run.invoker` on the core target
- configure relay audience and identity-token generation correctly

### 25.5 Slice 5: Configure The Real InfinitePay Token In Core

- confirm Secret Manager wiring for the real provider API token in core
- confirm the core provider adapter can call `payment_check`
- confirm the relay has no access to that token

### 25.6 Slice 6: Implement The Approved Transition Retry Policy And Validate It

- implement provider-facing `400 Bad Request` for temporary downstream/core/provider verification failures during the transition release
- implement provider-facing `400 Bad Request` for lock acquisition timeout or concurrent reconcile conflict during the transition release
- implement provider-facing `422 Unprocessable Entity` for deterministic malformed or unusable payloads, order not found, amount mismatch, and transaction identity mismatch
- document actual InfinitePay retry and timeout behavior from lower-environment validation
- update the production policy if validation disproves the approved transition mapping
- do not treat this slice as optional

### 25.7 Slice 7: Add Tests

Required tests and validation scenarios are:

- raw body preserved byte-for-byte across relay forwarding
- invalid secret path is rejected locally without forwarding to core
- invalid method is rejected locally without forwarding to core
- invalid content-type is rejected locally without forwarding to core
- invalid `Content-Type` parameters outside the allow-list are rejected locally without forwarding to core
- oversized body is rejected locally without forwarding to core
- source-aware throttling sheds requests locally before any core call and emits the expected metrics and alerts
- Cloud Run IAM-denied internal authentication failure is evidenced in Cloud Audit Logs or equivalent platform audit logs without body or secret leakage
- if optional internal token validation is enabled, token failure after IAM admission is application-audited without body or secret leakage
- failing-first tests prove the relay strips inbound `Authorization`, any inbound `X-EDN-*`, `Forwarded`, any inbound `X-Forwarded-*`, and hop-by-hop headers such as `Connection`, `Proxy-Connection`, `Keep-Alive`, `TE`, `Trailer`, `Transfer-Encoding`, and `Upgrade` before forwarding, and emits only the Section 15.2 internal header allow-list to core
- failing-first tests prove the relay returns only the Section 15.1 provider-facing header allow-list and mapped minimal public-safe JSON body to InfinitePay, and never propagates any upstream core response header or response body
- duplicate delivery returns duplicate-safe success
- core unavailable returns the provider-facing retry-inducing `400` mapping and never a false `200`
- confirmed payment follows the successful reconciliation path
- order not found returns internal `422` and provider-facing `422` and performs no payment or order mutation
- amount mismatch returns internal `422` and provider-facing `422`, performs no payment or order mutation, and never produces `pending_review`
- webhook payload `transaction_id` mismatch against the provider-verified transaction ID returns internal `422` and provider-facing `422`, performs no payment or order mutation, and never produces `pending_review`
- already-persisted canonical `transaction_nsu` mismatch against the verified provider transaction ID returns internal `422` and provider-facing `422`, performs no payment or order mutation
- when no canonical transaction identity is stored yet, the verified provider transaction ID becomes the persisted canonical transaction identity
- lock acquisition timeout or concurrent reconcile conflict returns internal `5xx`, provider-facing `400`, and no duplicate side effect
- the deployed environment proves an external caller cannot invoke the internal reconcile route, and the denial is evidenced in Cloud Audit Logs or equivalent platform audit logs
- Redis fallback and MongoDB-backed dedupe preserve duplicate safety when Redis is unavailable
- lock behavior prevents duplicate reconciliation side effects
- exactly-once downstream publish and outbox persistence remain intact under duplicate deliveries and retries
- platform-managed request logging and tracing are verified not to expose the secret path before production cutover
- secret-path rotation overlap and immediate revocation runbooks are validated operationally before production cutover
- lower-environment validation finalizes timeout and retry behavior before production cutover

### 25.8 Slice 8: Retire Legacy Public Core Webhook Mounts

- remove or disable the direct public webhook mounts in `cmd/app/main.go` and `cmd/payments-api/main.go` after cutover validation
- confirm no target-state provider traffic depends on those legacy public core mounts

## 26. Rollback And Transition Guidance

### 26.1 Rollback Characteristics

This change introduces no new durable data store and no required data migration. Rollback is primarily a deployment, IAM, and provider-routing exercise.

### 26.2 Cutover Guidance

- complete any mandatory deployment split before provider cutover so the internal route is not mounted on an unauthenticated public Cloud Run surface
- deploy and validate the internal core route before provider cutover
- deploy and validate the relay before provider cutover
- confirm IAM and token configuration before pointing InfinitePay to the relay URL
- confirm the v1 edge compensating controls are live before production promotion: at least 128 bits of secret-path entropy, `maxScale` 2, source-aware throttling, 32 in-flight cap, 3-second forward timeout, suspicious-burst alerting, bounded rotation overlap, and immediate revocation runbook
- confirm platform-managed request logging and tracing do not expose the secret path before production promotion, or block cutover until an alternative secret transport or equivalent control is in place
- validate the approved transition `400` mapping, `422` handling, and timeout behavior in lower environments before production promotion
- cut over the provider webhook URL only after internal end-to-end validation succeeds

### 26.3 Rollback Guidance

If rollback is needed during the transition window:

- revert provider webhook routing first
- then roll back relay traffic or deployment
- keep the core reconciliation logic as the single financial authority throughout

If a temporary legacy direct-core fallback is retained during transition, it must be removed after the governance checkpoint is complete so the target-state secret path exists only at the relay edge. Post-cutover, the legacy public webhook mounts in `cmd/app/main.go` and `cmd/payments-api/main.go` must be removed or disabled.

## 27. Open Questions

- Should the optional defense-in-depth internal token be retained, or is Cloud Run IAM sufficient for the first release?
- Does the core raw-payload persistence format need a later audit-oriented refinement beyond the first relay release while still preserving exact-byte forwarding and current data ownership?

## 28. Governance Checkpoints

The following checkpoints are required before production-style promotion of this architecture:

### 28.1 Baseline Checkpoint

- confirm the current public webhook mounts in `cmd/app/main.go` and `cmd/payments-api/main.go`
- confirm the current financial reconciliation logic in `internal/payments/webhook.go`

### 28.2 Target Architecture Checkpoint

- approve the relay-only public ingress model
- approve the core-only financial authority model
- approve the internal route name and trust boundary

### 28.3 Security Checkpoint

- approve secret separation between relay path secret and core provider token
- approve Cloud Run IAM and service-account least privilege
- approve the v1 public-edge compensating controls, including at least 128 bits of secret-path entropy, `maxScale` 2, source-aware throttling, 32 in-flight cap, 3-second forward timeout, suspicious-burst alerting, bounded overlap rotation, and immediate revocation runbook
- approve the IAM-denial evidence model in Cloud Audit Logs or equivalent platform audit logs and the application-audit requirement only for optional token failure after IAM admission
- approve logging redaction rules and the platform-managed control that prevents secret-path exposure in request logging or tracing

### 28.4 Provider Contract Checkpoint

- confirm lower-environment timeout and retry behavior, including the approved transition `400` mapping for downstream failures and lock conflicts and the `422` handling for order not found and transaction identity mismatches
- record any production policy adjustment before production promotion

### 28.5 Deployment Readiness Checkpoint

- confirm `cloudbuild.development.yaml` changes for relay build and deploy
- confirm Secret Manager configuration
- confirm IAM binding and end-to-end authenticated invocation
- confirm any required deployment split so the internal reconcile route is not mounted on an unauthenticated public Cloud Run surface

### 28.6 Post-Cutover Checkpoint

- confirm successful end-to-end webhook processing through relay and core
- confirm duplicate delivery is safe
- confirm no raw payload or secret leakage in logs
- confirm an external caller cannot invoke the internal reconcile route and that IAM-denied attempts are evidenced in Cloud Audit Logs or equivalent platform audit logs
- confirm legacy public webhook mounts in `cmd/app/main.go` and `cmd/payments-api/main.go` are removed or disabled
