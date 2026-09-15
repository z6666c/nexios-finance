# Nexios Finance

Unified liquidity engine backend (Go) plus user-facing and admin UI prototypes for automatically covering payments across a user's connected bank accounts and cards.

## Live prototypes

User app (mobile simulator): https://claude.ai/artifact/XkwH3omEHJ5qyCioegScHk

Admin dashboard: https://claude.ai/artifact/6yRDuQMxUBNQmVTjZpMoc3

## Running locally

Start the HTTP dev server with: go run ./cmd/orchestrator

This starts a server on port 8080 exposing POST /v1/payments/authorize, which accepts a JSON body with user_id, amount_minor_units, currency, merchant_id, and idempotency_key.

## Architecture patterns implemented

Ports and Adapters is implemented via the ProviderResolver, AssetProvider, and Repository interfaces. Saga orchestration is implemented in orchestrator/executor.go and rollback.go. The Outbox pattern is implemented as an in-memory development version in ledger/outbox.go. Idempotency keys are enforced per transaction and per leg in executor.go. A circuit breaker isolates failing providers in orchestrator/circuitbreaker.go. Simplified distributed tracing lives in internal/tracing. mTLS with banks is implemented in openbanking/client.go.

Three items are documented but not production ready. JWS signing for FAPI 1.0 Advanced in openbanking/jws.go is an HMAC placeholder, not PS256 backed by an HSM. ISO 20022 pacs.008 support in openbanking/iso20022.go is a simplified structure, not validated against the official schema. gRPC via proto/liquidity.proto is documented as the target contract, but the server currently runs HTTP and JSON because generating real gRPC code requires running protoc locally.

## What is complete

Data models use integer minor units instead of float64. The planner builds a split plan by priority and safety threshold. The executor runs Saga steps in parallel with idempotency and a per-provider circuit breaker. Rollback automatically compensates when any leg fails. The ledger keeps balanced double-entry bookkeeping with duplicate prevention and outbox events.

## What needs work before production

Generate real gRPC code from proto/liquidity.proto via protoc. Replace the in-memory ledger and outbox repositories with real CockroachDB-backed implementations. Replace HMACSigner with a real JWS signer backed by an HSM. Validate Pacs008Message against the official ISO 20022 XSD before using it against a real instant payment rail. Move secrets to Vault or KMS instead of config files. Add an AuthN and AuthZ layer plus internal service-to-service mTLS. Add network isolation for PCI-DSS scoping around card-data services. Replace the simplified tracing package with full OpenTelemetry. Add comprehensive unit and integration tests.
