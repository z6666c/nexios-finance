# Nexios Finance (نيكسيوس فاينانس)

A Saudi fintech liquidity-aggregation prototype: a Go backend implementing
a Saga-orchestrated, double-entry ledgered payment/transfer engine, plus
two static single-page prototype apps (a user app and an admin dashboard)
that talk to it.

This repository is written to be **genuinely runnable on a fresh, local
server**: `go build ./...` succeeds on a plain `git clone` with zero
network access (see "Zero dependencies" below), and
`docker compose up --build` in `deployments/` brings up a complete,
browser-reachable stack - database, API, and the two frontend apps served
together - with no manual setup.

## Live prototypes

- User app (mobile simulator): https://z6666c.github.io/nexios-finance/web/user-app.html
- Admin dashboard: https://z6666c.github.io/nexios-finance/web/admin-app.html

These are the same two files under `web/`, hosted for convenience; the
"Running locally" section below is how to run the actual backend behind
them.

## Zero dependencies

`go.mod` declares no `require` entries at all. That's deliberate, not an
oversight: this project intentionally avoids every third-party Go module
so a checkout can be built and run on a server with no access to
`proxy.golang.org` - a common restriction on a locked-down local/on-prem
box, and the exact situation that used to break this repo (see
`internal/uuid`'s and `internal/pgwire`'s package docs for the two places
this mattered: a dependency-free UUID implementation, and a pure-Go
PostgreSQL wire-protocol client written from scratch to replace
`github.com/jackc/pgx`/`lib/pq`). Both are small, focused, and only
implement the subset of behavior this codebase actually needs - they are
not meant as general-purpose replacements for those libraries.

## Running locally

**Zero setup (in-memory storage, data lost on restart):**

```
go run ./cmd/orchestrator
```

Starts the API on `:8080` with an in-memory account store, seeded with a
demo user, nine linked bank accounts/investment portfolios, and four other
Nexios users to transfer to - the same data the frontend prototypes show.
No database, no config file, no environment variables required.

**With durable Postgres-backed storage:**

```
export NEXIOS_LEDGER_DRIVER=postgres
export NEXIOS_LEDGER_DSN="postgres://nexios:nexios@localhost:5432/nexios?sslmode=disable"
go run ./cmd/orchestrator
```

On startup this connects to Postgres, applies the embedded schema
migration (`internal/schema/migrations/0001_init.sql`), and seeds the same
demo data as above if the `users` table is empty - safe to run against an
already-seeded database, and safe to restart repeatedly. Postgres must be
configured for MD5 (or cleartext) password auth, not the SCRAM-SHA-256
default in Postgres 16+ - `internal/pgwire`'s doc comment explains why;
`docker compose` (below) sets this automatically via
`POSTGRES_HOST_AUTH_METHOD=md5`.

**Full stack via Docker Compose (recommended for a real deployment):**

```
cd deployments
docker compose up --build
```

This builds the orchestrator image, starts Postgres, and starts nginx
serving `web/user-app.html` / `web/admin-app.html` with `/v1/*` and
`/healthz` reverse-proxied through to the orchestrator - all three
reachable as one system:

- http://localhost:8082/ - user app (served by nginx)
- http://localhost:8082/admin-app.html - admin dashboard
- http://localhost:8081/v1/... - direct API access (also reachable at
  http://localhost:8082/v1/... through nginx)

Set `NEXIOS_AUTH_API_KEY` before starting to require a bearer token on
every `/v1/*` request (see "Security" below); left unset, the API runs
open, which is only appropriate on a trusted local network.

## HTTP API

All amounts are integers in minor currency units (halalas), never floats.

| Method | Path                        | Purpose                                              |
|--------|-----------------------------|-------------------------------------------------------|
| GET    | `/healthz`                  | Liveness check (never requires auth)                   |
| GET    | `/v1/users`                 | List Nexios users (for the P2P recipient picker)       |
| GET    | `/v1/accounts?user_id=`     | A user's linked accounts/portfolios + total liquidity  |
| GET    | `/v1/transfers?user_id=`    | A user's transfer history                              |
| POST   | `/v1/transfers/own`         | Transfer between two of the user's own accounts        |
| POST   | `/v1/transfers/p2p`         | Transfer to another Nexios user                        |
| POST   | `/v1/transfers/distribute`  | Distribute an incoming amount across several accounts  |
| POST   | `/v1/payments/authorize`    | Run the Saga-orchestrated multi-bank payment demo       |

Every `POST` requires an `idempotency_key` field in its JSON body; a
repeated key is not currently deduplicated at the transfer level (see
"What needs work" below) but is enforced at the ledger-entry level (see
`internal/accounts/postgres_repository.go`'s `ledger_entries.idempotency_key`
unique constraint).

## Architecture

**`internal/accounts`** is the newer half of the system: linked bank
accounts and investment portfolios, Nexios account numbers, and the three
transfer types the user app exposes. Every transfer - own-account, P2P, or
a multi-account distribution - is written as one atomic SQL transaction
(`PostgresStore.ExecuteTransfer`) that updates account balances, writes a
*balanced* double-entry ledger batch, records a human-readable transfer,
and enqueues an outbox event together or not at all. A "distribute"
transfer's offsetting ledger leg is an internal clearing account
(`accounts.ClearingAccountID`) - the same well-known-suspense-account
pattern `internal/ledger` already used for its own settlements - since
crediting several of a user's accounts from one incoming amount needs a
debit somewhere to keep the ledger balanced, and that money's source is
external to the two accounts it moves between.

**`internal/ledger` / `internal/orchestrator` / `internal/openbanking` /
`internal/domain` / `internal/tracing`** are the original Saga-orchestrated
payment engine behind `POST /v1/payments/authorize`, implementing:

- **Ports & Adapters**: the `ProviderResolver`, `AssetProvider`, and
  `Repository` interfaces.
- **Saga orchestration**: `orchestrator/executor.go` and `rollback.go` run
  each leg of a payment plan and automatically compensate (roll back) the
  legs that already settled if any leg fails.
- **Outbox pattern**: `ledger/outbox.go` decouples "the ledger write
  committed" from "an event about it was published."
- **Idempotency**: enforced per transaction and per ledger entry.
- **Circuit breaker**: isolates a failing provider in
  `orchestrator/circuitbreaker.go`.
- **Double-entry ledger**: balanced batches with duplicate prevention.
- **Simplified distributed tracing**: `internal/tracing` (a request-ID
  propagated through context and logs, not full OpenTelemetry).

This part of the system still uses `ledger.NewInMemoryRepository()` /
`NewInMemoryOutbox()` (see "What needs work" below) - its
Postgres-backed persistence was intentionally *not* wired up in this
round of work, to avoid coupling its schema to `internal/accounts`' before
either has a real caller that needs them to cross-reference each other.

**`internal/pgwire`** is the dependency-free PostgreSQL client described
above, registered as the `database/sql` driver named `"pgwire"`.

**`internal/config`** loads `config/config.yaml` (a small hand-parsed
`key: value` format - no YAML library, same zero-dependency reasoning) with
`NEXIOS_*` environment variable overrides, so `docker-compose.yml` can
inject the database DSN and an API key without editing the checked-in file.

**`internal/schema`** embeds and applies `internal/schema/migrations/*.sql`
at startup via `go:embed`, so there's no separate migration tool to run.

Three pieces of the original Saga engine remain explicitly unfinished,
documented rather than hidden:

- JWS signing for FAPI 1.0 Advanced (`openbanking/jws.go`) is an HMAC
  placeholder, not PS256 backed by an HSM.
- ISO 20022 `pacs.008` support (`openbanking/iso20022.go`) is a simplified
  structure, not validated against the official schema.
- gRPC via `proto/liquidity.proto` is documented as the target contract,
  but the server runs HTTP/JSON because generating real gRPC code needs
  `protoc`, which this build deliberately does not require.

## Security

`internal/api`'s bearer-token check (`NEXIOS_AUTH_API_KEY`) is a
deliberately simple starting point - a single static shared secret, not a
real identity provider, per-user credentials, or scoped permissions. It's
enough to keep an API off the open internet if a deployment is
accidentally exposed, but it is **not** a substitute for real AuthN/AuthZ
(OAuth2/OIDC, per-user tokens, RBAC) before this handles real money for
real users - see "What needs work" below.

## What needs work before production

Generate real gRPC code from `proto/liquidity.proto` via `protoc`. Wire
`internal/ledger`'s repositories to Postgres (currently in-memory only;
`internal/accounts` already has a working Postgres implementation to model
this on). Replace `HMACSigner` with a real JWS signer backed by an HSM.
Validate `Pacs008Message` against the official ISO 20022 XSD before using
it against a real instant payment rail. Move secrets to Vault or KMS
instead of environment variables/config files. Add real AuthN/AuthZ
(OAuth2/OIDC, per-user credentials, RBAC) in place of the single static API
key, plus internal service-to-service mTLS. Add request-level idempotency
deduplication at the HTTP layer (currently only enforced at the ledger
entry). Add network isolation for PCI-DSS scoping around card-data
services. Replace the simplified tracing package with full OpenTelemetry.
Add TLS support to `internal/pgwire` (it currently only speaks plaintext
Postgres wire protocol - fine inside a private Docker network / VPC, not
for a database reachable over the open internet). Add comprehensive unit
and integration tests - this round of work was verified with manual
smoke tests against a real local Postgres instance and real HTTP requests,
not a committed automated test suite.
