# Complete Architecture Specification: Catalog, Packages, Plans, Organizations, Tenants, Checkout, Payments, and Subscriptions

## 1. Document Purpose

This document is the complete architecture specification for the commercial platform slice of EDN Core.

It exists to support architecture review before expanding implementation and to make the following explicit:

- business scope and non-goals
- module boundaries and dependency direction
- authoritative data ownership in MongoDB
- public, worker, and administrative trust boundaries
- request, payment, webhook, and subscription workflows
- idempotency, replay, and partial-failure behavior
- Cloud Run deployment topology
- operational, security, and rollback constraints

This specification is normative for the commercial platform slice. When code, plans, or future docs diverge from it, this specification should be treated as the reference to review first.

## 2. Problem Statement

The original checkout flow sold a flattened `Plan` document and created orders with only lightweight plan references. That model is not sufficient for the required business shape because the platform must support:

- reusable atomic products and services
- versioned packages composed from those items
- versioned commercial plans that reuse packages
- organization and tenant isolation
- durable order lineage from plan to package to item
- strong idempotency for checkout and webhook processing
- asynchronous subscription activation and event publication
- operation on Cloud Run using MongoDB Atlas as the only durable business store

The target state is a MongoDB-first commercial architecture that preserves historical correctness, isolates tenants, and behaves safely under retries, duplicates, and partial failures.

## 3. Scope

### In Scope

- organizations and tenants
- catalog items, packages, and versioned plans
- public plan listing and checkout creation
- order persistence and status tracking
- payment verification and webhook processing
- subscription activation from approved payments
- outbox-based domain event publication
- Cloud Run topology for public and background workloads

### Out Of Scope

- invoice rendering beyond provider references
- ERP or accounting integration
- CRM campaign orchestration
- admin UI design
- advanced entitlement delivery workflows for every product type
- multi-region active-active behavior

## 4. Architectural Principles

### 4.1 MongoDB Is The Only Durable Source Of Truth

MongoDB Atlas stores all authoritative commercial state for this slice:

- organizations
- tenants
- catalog items
- packages
- versioned plans
- orders
- payments
- subscriptions
- webhook events
- idempotency keys
- outbox events

Redis may accelerate reads, dedupe fast paths, locks, and coordination, but it never owns durable business truth.

### 4.2 Tenant Isolation Is Explicit

Commercial collections are scoped by `organization_id` and `tenant_id` whenever applicable. Unique indexes for tenant-scoped business concepts must include `tenant_id`.

### 4.3 Historical Correctness Beats Live Joins

Orders and subscriptions must remain auditable even after plans or packages evolve. For that reason, the system stores both:

- lightweight references for navigation and analytics
- immutable commercial snapshots for historical truth

### 4.4 Public Writes Must Be Idempotent

Every externally retryable write must have a durable idempotency model. Public checkout creation requires `Idempotency-Key`, and webhook processing must be replay-safe and transaction-aware.

### 4.5 Public HTTP And Background Processing Are Different Boundaries

The public HTTP runtime must not embed long-lived background workers. Public traffic, subscriber processing, and outbox dispatch scale differently, fail differently, and require different security postures.

### 4.6 Provider Payloads Are Not Truth

No provider callback may directly promote business state without server-side verification against the provider adapter.

## 5. System Context

### External Actors

- public frontend for plan browsing and checkout initiation
- InfinitePay as checkout and payment provider
- Google Cloud Pub/Sub as internal async transport for approved payment events
- operations and future admin clients for catalog and tenancy management

### Internal Runtimes

- public HTTP service on Cloud Run
- internal worker service on Cloud Run
- MongoDB Atlas
- Redis

## 6. High-Level Runtime Topology

### Public HTTP Service

Primary runtime:

- current entrypoint: `cmd/app`
- Cloud Run services: `edn-core-dev`, `edn-core-prd`

Responsibilities:

- public leads endpoint
- public plan listing
- public checkout session creation
- order status polling
- provider webhook ingress
- docs and health endpoints

This service is the only runtime exposed to unauthenticated internet traffic.

### Internal Worker Service

Primary runtime:

- current entrypoint: `cmd/subscription-worker`
- Cloud Run services: `edn-core-dev-worker`, `edn-core-prd-worker`

Responsibilities:

- consume `payment.approved` from Pub/Sub
- activate subscriptions from approved payments
- dispatch unpublished outbox events
- expose only internal health endpoints for Cloud Run readiness and liveness

This service must remain non-public and internal ingress only.

### Supporting Infrastructure

- MongoDB Atlas: durable commercial state
- Redis: locks, fast-path dedupe, cache
- Pub/Sub: internal async choreography after payment approval

## 7. Module Boundaries And Dependency Direction

### 7.1 `internal/organizations`

Owns:

- organization identity
- commercial ownership root
- organization activation state

Must not depend on:

- checkout
- payments
- subscriptions

### 7.2 `internal/tenants`

Owns:

- tenant identity within an organization
- tenant activation state
- enabled channels per tenant

Depends on:

- organization identifiers as foreign business references

Must not depend on:

- checkout
- payments

### 7.3 `internal/catalog`

Owns:

- atomic commercial items
- item type and delivery mode
- item activation state

### 7.4 `internal/packages`

Owns:

- versioned package composition
- item inclusion and quantity snapshots for package publishing

Depends on:

- catalog item references

### 7.5 `internal/plans`

Owns:

- sellable commercial offers
- package references and package snapshots
- pricing, cycle, and channel scope
- validity window

Depends on:

- published packages

### 7.6 `internal/checkout`

Owns:

- public order creation path
- durable order persistence
- checkout provider initiation
- request validation and order status retrieval

Depends on:

- published plan resolution
- durable idempotency
- Redis lock/cache abstractions

### 7.7 `internal/idempotency`

Owns:

- authoritative idempotency records
- replay metadata
- partial failure recovery snapshot for retriable writes

Must remain generic and not embed provider-specific logic.

### 7.8 `internal/payments`

Owns:

- payment verification against provider
- webhook processing
- order status transitions after payment outcomes
- outbox publication for approved payments
- subscription activation worker logic

Depends on:

- checkout orders
- subscriptions
- provider adapter
- Redis lock and dedupe fast path
- Pub/Sub publisher and consumer adapters

### 7.9 `internal/subscriptions`

Logical responsibility even if some behavior currently lives in `internal/payments`:

- create pending subscriptions
- activate subscriptions from approved orders
- manage lifecycle changes over time

The activation flow must derive commercial truth from the order snapshot, not from a mutable live plan lookup.

## 8. Public, Internal, And Future Surfaces

### Public Surface

- `GET /v1/plans`
- `POST /v1/checkout/sessions`
- `GET /v1/orders/{orderNSU}/status`
- provider webhook endpoint under secret path

### Internal Surface

- worker readiness and liveness endpoints
- Pub/Sub subscription consumption
- outbox polling and publication

### Administrative Surface

Target state only, not fully implemented in this slice:

- organization creation and activation
- tenant management
- catalog item management
- package publishing
- plan publishing

Administrative APIs must be authenticated and authorized server-side and must not share the same unauthenticated boundary as the public checkout service.

## 9. Domain Model

### Organization

Administrative and billing root. A single organization owns many tenants.

### Tenant

Operational isolation unit under an organization. Catalog, packages, plans, orders, and subscriptions belong to a tenant.

### Catalog Item

Atomic sellable or fulfillable unit, either product or service.

### Package

Versioned composition of catalog items. Packages are not directly sold to the public; they are reused by plans.

### Plan

Versioned sellable offer. Each published plan references one package version and carries commercial configuration such as price, billing cycle, and channel scope.

### Order

Authoritative checkout record. Orders capture customer data, commercial lineage, and provider references.

### Payment

Authoritative provider transaction record verified server-side.

### Subscription

Lifecycle record derived from a paid order. Activation is asynchronous and keyed by the origin order.

### Idempotency Key

Durable record for retry-safe public writes. Stores request hash and replay metadata.

### Webhook Event

Durable raw inbound provider callback record used for replay safety and audit.

### Outbox Event

Durable unpublished domain event for at-least-once asynchronous publication.

## 10. Authoritative Data Ownership And Collections

### 10.1 `organizations`

Purpose:

- root administrative identity

Required fields:

```text
org_uuid
slug
name
billing_email
active
created_at
updated_at
```

Indexes:

- unique `org_uuid`
- unique `slug`

### 10.2 `tenants`

Purpose:

- organization-scoped tenant isolation

Required fields:

```text
tenant_uuid
organization_id
slug
name
channels[]
active
created_at
updated_at
```

Indexes:

- unique `tenant_uuid`
- unique `organization_id + slug`
- compound `organization_id + active`

### 10.3 `catalog_items`

Purpose:

- tenant-scoped atomic products and services

Required fields:

```text
item_uuid
organization_id
tenant_id
type
slug
name
description
delivery_mode
active
metadata
created_at
updated_at
```

Indexes:

- unique `item_uuid`
- unique `tenant_id + slug`
- compound `tenant_id + type + active`

### 10.4 `packages`

Purpose:

- versioned package composition

Required fields:

```text
package_uuid
organization_id
tenant_id
slug
version
name
status
items[]
created_at
updated_at
```

Package items must include enough snapshot data to survive future catalog mutations.

Indexes:

- unique `package_uuid`
- unique `tenant_id + slug + version`
- compound `tenant_id + slug + status`

### 10.5 `versioned_plans`

Purpose:

- sellable versioned plans isolated from the legacy `plans` collection

Required fields:

```text
plan_uuid
organization_id
tenant_id
slug
version
package_ref
package_snapshot
name
billing_cycle
price_cents
currency
max_installments
channel
active
valid_from
valid_until
created_at
updated_at
```

Indexes:

- unique `plan_uuid`
- unique `tenant_id + slug + billing_cycle + version`
- compound `tenant_id + channel + active + billing_cycle`
- partial `tenant_id + slug + billing_cycle` where `active = true`

Migration note: `BootstrapTenancyStorage` automatically removes the legacy index
`idx_versioned_plans_tenant_slug_version_unique` (which lacked `billing_cycle`)
before creating the new indexes. No manual step is required in staging or production.

Commercial plan names and prices still require commercial confirmation before promotion to production.

### 10.6 `orders`

Purpose:

- authoritative record of checkout attempts that became business orders

Required fields in the current target shape:

```text
id
order_nsu
organization_id
tenant_id
plan_id
plan_slug
plan_version
plan_source
billing_cycle
amount_cents
currency
customer_name
customer_email
customer_phone
customer_document
status
provider
provider_checkout_url
invoice_slug
receipt_url
created_at
updated_at
```

Target extension for full commercial lineage:

- `plan_ref`
- `package_ref`
- `item_refs[]`
- `commercial_snapshot`

Indexes:

- unique `order_nsu`
- `customer_email`
- `status`
- `created_at`
- `tenant_id + status + created_at`

### 10.7 `payments`

Purpose:

- authoritative verified provider transaction state

Required fields:

```text
id
order_nsu
transaction_nsu
invoice_slug
amount_cents
paid_amount_cents
installments
capture_method
receipt_url
status
raw_payload
created_at
updated_at
```

Indexes:

- unique `transaction_nsu`
- `order_nsu`
- `status`

### 10.8 `subscriptions`

Purpose:

- customer lifecycle after approved payment

Required fields:

```text
id
customer_email
plan_id
plan_slug
status
origin_order_nsu
starts_at
ends_at
created_at
updated_at
```

Target extension:

- order snapshot reference or embedded commercial snapshot where needed for richer fulfillment

Indexes:

- `customer_email`
- `plan_slug`
- `status`
- `origin_order_nsu`

### 10.9 `webhook_events`

Purpose:

- durable audit and dedupe record of inbound provider callbacks

Required fields:

```text
id
provider
event_hash
order_nsu
transaction_nsu
status
raw_payload
received_at
processed_at
```

Indexes:

- unique `event_hash`
- `order_nsu`
- `transaction_nsu`
- `received_at`

The target business invariant is durable dedupe by event hash and transaction identity, even if transaction dedupe is implemented through stateful lookup rather than a second unique index.

### 10.10 `idempotency_keys`

Purpose:

- authoritative retry state for public writes

Required fields:

```text
operation
organization_id
tenant_id
idempotency_key
request_hash
resource_type
resource_id
resource_status
resource_url
external_ref
status
expirable
expires_at
created_at
updated_at
```

Indexes:

- unique `tenant_id + operation + idempotency_key`
- TTL on `expires_at` only for `expirable = true`

### 10.11 `outbox_events`

Purpose:

- durable unpublished domain event storage

Required fields:

```text
id
event_type
payload
published
created_at
published_at
```

Indexes:

- `published + created_at`

## 11. Data Lifecycle Rules

### Versioning Rules

- package composition changes create a new package version
- plan price, cycle, channel, or linked package changes create a new plan version
- historical versions remain immutable once referenced by an order

### Active Vs Historical Data

- live selling uses active and valid plans only
- orders and subscriptions retain historical truth independent of live plan state

### TTL Rules

- only expirable idempotency records participate in TTL cleanup
- payments, orders, subscriptions, webhook events, and outbox events are not auto-expired by TTL in this release

## 12. Public Contract Specification

### 12.1 `GET /v1/plans`

Purpose:

- list available plans for sale

Query parameters:

- `organization_slug` optional but required together with `tenant_slug` for tenant-aware catalog
- `tenant_slug` optional but required together with `organization_slug`
- `channel` optional, defaults to `web` in runtime normalization

Behavior:

- if organization and tenant slugs are present, resolve organization and tenant, then query `versioned_plans`
- if no scope is present, use legacy public path
- only return active and currently sellable plans

### 12.2 `POST /v1/checkout/sessions`

Purpose:

- create an order and provider checkout session

Headers:

- `Idempotency-Key` required

Request body:

- `organization_slug` optional but paired with `tenant_slug`
- `tenant_slug` optional but paired with `organization_slug`
- `channel` optional, normalized to `web` when omitted
- `plan_slug` required
- `billing_cycle` required
- `customer` required

Behavior:

- validate request fields
- resolve tenant scope
- resolve sellable plan from published catalog
- reserve durable idempotency key before provider call
- create order
- call provider to create checkout
- persist provider URL and status
- commit replay snapshot into idempotency record

### 12.3 `GET /v1/orders/{orderNSU}/status`

Purpose:

- expose current order and subscription state to the frontend

Behavior:

- validate NSU format
- read authoritative order state
- optionally include subscription status if present

### 12.4 Webhook Endpoint

Purpose:

- receive provider callbacks without exposing enumeratable path structure

Behavior:

- accept only JSON payloads
- read raw body once
- compute event hash
- perform fast-path dedupe in Redis
- perform durable dedupe and recovery in MongoDB
- verify payment against provider
- persist payment, order transitions, and outbox event

## 13. Critical Workflow Specifications

### 13.1 Tenant-Aware Plan Listing

Sequence:

1. client calls `GET /v1/plans`
2. service normalizes `channel`
3. if tenant scope is present, resolve organization by slug
4. resolve tenant by organization and tenant slug
5. list active sellable plans for tenant and channel
6. return flattened public response

Failure handling:

- invalid scope returns `422`
- unknown organization or tenant returns `404`

### 13.2 Checkout Creation

Sequence:

1. client sends checkout request with `Idempotency-Key`
2. service validates payload and customer fields
3. service resolves organization and tenant scope if present
4. service resolves sellable plan from current published catalog
5. service computes canonical request hash
6. service reserves `idempotency_keys` record with pending status
7. service creates order with authoritative price from plan
8. service acquires distributed order lock in Redis
9. service calls InfinitePay to create checkout session
10. service persists provider URL and transitions order to `checkout_created`
11. service commits idempotency snapshot with `resource_id`, `resource_status`, `resource_url`, and external provider reference
12. service returns checkout URL and order NSU

Idempotency rules:

- same key plus same request hash returns same business result
- same key plus different request hash returns conflict
- duplicate order creation must collapse into replay path

Partial failure recovery rules:

- if provider call succeeds but local order persistence partially fails, the committed idempotency snapshot becomes the replay authority
- a repeated request with the same key must return the same checkout URL instead of creating a second provider checkout
- best-effort repair updates the order from the idempotency snapshot when possible

### 13.3 Webhook Processing

Sequence:

1. receive raw webhook body
2. compute `event_hash`
3. parse minimum transaction and order identifiers
4. check Redis fast-path dedupe
5. acquire Redis order lock
6. inspect MongoDB webhook history for `transaction_nsu`
7. if a processed event already exists for the transaction, return success without replaying side effects
8. if an unprocessed event exists, recover and continue processing against the existing durable record
9. verify payment with provider server-side
10. persist or reuse webhook event record
11. upsert verified payment record
12. transition order status based on verified payment outcome and amount match
13. insert `payment.approved` into outbox when appropriate
14. invalidate cached order status
15. mark webhook event as processed

Rules:

- provider payload status alone is never authoritative
- duplicate webhook deliveries must be safe
- out-of-order webhook deliveries must not corrupt business state

### 13.4 Outbox Dispatch

Sequence:

1. worker polls unpublished outbox events ordered by `created_at`
2. worker publishes supported event types to Pub/Sub
3. worker marks events as published only after successful publish acknowledgment

Semantics:

- at-least-once publication
- consumers must remain idempotent

### 13.5 Subscription Activation

Sequence:

1. worker consumes `payment.approved` event
2. worker checks whether subscription is already active for the origin order
3. worker loads order by NSU
4. worker derives billing period from order billing cycle
5. worker creates subscription if missing
6. worker activates subscription by `origin_order_nsu`
7. worker invalidates cached order status

Rules:

- activation must be idempotent
- activation must not depend on mutable live plan lookup
- the order is the activation source of truth

## 14. Idempotency And Replay Specification

### Checkout Idempotency

Authority:

- MongoDB `idempotency_keys`

Fast path:

- none required for correctness

State model:

- `pending`
- `committed`
- `failed`

Replay metadata:

- `resource_id`
- `resource_status`
- `resource_url`
- `external_ref`

### Webhook Dedupe

Authority:

- MongoDB `webhook_events`

Fast path:

- Redis dedupe hints by transaction and event hash

Durable behavior:

- dedupe by processed transaction state
- recover from unprocessed durable event when possible
- avoid replaying side effects for already processed transaction deliveries

## 15. Security Specification

### 15.1 Trust Boundaries

- public internet to HTTP service
- provider webhook to HTTP service
- internal worker to MongoDB, Redis, and Pub/Sub
- administrative clients to future protected endpoints

### 15.2 Input Validation

All external input is untrusted, including:

- frontend checkout payloads
- query parameters for tenant scope
- provider webhook payloads

Required validation includes:

- slug allow-list patterns
- email normalization
- CPF normalization and verification
- phone normalization to E.164 rules supported by the current validator
- bounded body size

### 15.3 Data Protection

The following must not be exposed in logs or error surfaces:

- MongoDB URI
- provider API token
- Redis password
- raw customer PII where unnecessary
- raw sensitive webhook payloads in normal info logs

### 15.4 Authorization

Public plan listing and checkout creation remain unauthenticated by design.

Administrative and partner surfaces must require:

- strong authentication
- server-side authorization
- least privilege service accounts

### 15.5 Replay And Abuse Protection

- checkout requires `Idempotency-Key`
- webhook processing requires durable dedupe and provider verification
- Redis lock and dedupe are supportive only, not authoritative

## 16. Cloud Run Deployment Specification

### Public Service

Characteristics:

- unauthenticated internet ingress allowed
- serves HTTP traffic only
- no embedded long-running background loops

### Worker Service

Characteristics:

- no unauthenticated public ingress
- internal ingress only
- low concurrency for predictable message handling
- long-lived Pub/Sub receive loop
- recurring outbox dispatch loop

### Image Build Strategy

Single Dockerfile with parameterized build target:

- `./cmd/app` for public HTTP service
- `./cmd/subscription-worker` for internal worker

### Configuration And Secrets

Non-sensitive configuration in environment files:

- HTTP settings
- collection names
- database name
- logging config
- Pub/Sub resource names

Sensitive values via Secret Manager:

- MongoDB URI
- leads auth token
- Redis password
- provider tokens
- webhook secret path

## 17. Operational Specification

### Health Checks

Public and worker runtimes expose:

- `/livez`
- `/readyz`

Readiness requirement:

- MongoDB `Ping` must succeed

### Logging

Structured logging is required around:

- provider calls
- checkout creation failures
- webhook processing outcomes
- worker consumption failures
- outbox dispatch failures

### Observability Gaps To Close

Future recommended additions:

- metrics for checkout replay rate
- metrics for webhook duplicate rate
- metrics for outbox lag
- alerting on worker subscription failures
- trace correlation from checkout to payment to activation

## 18. Failure Modes And Recovery Rules

### Provider Success, Local Persistence Failure

Handled by:

- durable replay snapshot in `idempotency_keys`
- retry with same `Idempotency-Key`

### Duplicate Webhooks

Handled by:

- Redis hint layer
- MongoDB durable transaction-aware dedupe

### Worker Restart

Handled by:

- Pub/Sub redelivery
- idempotent activation flow
- unpublished outbox records remaining durable in MongoDB

### Service Rollback

Handled by:

- Cloud Run revision rollback
- no destructive schema cleanup in the same functional release

## 19. Current Implementation Status Versus Target State

### Implemented In This Slice

- organization and tenant foundations
- versioned plan repository and runtime resolution by slug
- public checkout requiring `Idempotency-Key`
- durable idempotency replay snapshot
- public HTTP and worker separation in Cloud Run topology
- webhook transaction-aware durable dedupe behavior
- subscription activation based on order data instead of mutable live plan lookup

### Still Target-State Or Next-Step Concerns

- richer order lineage with explicit `package_ref`, `item_refs`, and full `commercial_snapshot`
- fully separated administrative API surface
- stronger transaction usage where multi-document atomicity is justified and available
- richer observability and alerting
- retention policy decisions for historical webhook and payment data

## 20. Risks And Tradeoffs

### MongoDB-Only Tradeoff

Using MongoDB as the only durable store reduces cross-database complexity, but puts more pressure on document design, index discipline, and careful handling of multi-document consistency.

### Separate Worker Service Tradeoff

Splitting public HTTP from background processing improves security and operational behavior, but introduces one more Cloud Run service and deployment artifact.

### Snapshot Tradeoff

Embedding snapshots increases storage footprint, but is required for historical correctness and auditability.

### Legacy Fallback Tradeoff

Keeping legacy plan fallback preserves backward compatibility during migration, but temporarily increases code path complexity until full catalog migration is complete.

## 21. Review Checklist

Use this specification to review the architecture against the following questions:

- Are organization and tenant boundaries explicit in every relevant business collection?
- Is any public write missing durable idempotency?
- Can any provider callback mutate state without provider verification?
- Can public HTTP scaling accidentally multiply background workers?
- Do order and subscription records remain historically correct after plan evolution?
- Are trust boundaries separated between public, internal, and future administrative surfaces?
- Do indexes support the hot paths described here?
- Is there any place where Redis is acting like durable truth?

## 22. Recommended Analysis Outcome

If this specification is approved, the next architecture-controlled steps should be:

1. freeze the data contract for order lineage and commercial snapshot fields
2. confirm retention and archival policy for webhook and payment history
3. define the protected administrative API surface as a separate architecture document
4. run end-to-end validation in development Cloud Run for public service and worker service together
