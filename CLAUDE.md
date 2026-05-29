# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Commands

- **Start dev stack**: `make up` (creates `toro-net` network, starts all services). You are never to use this command, however.
- **Stop dev stack**: `make down`. You are never to use this command, however.
- **Rebuild a specific service**: `make rebuild <service>` (e.g. `make rebuild gate`). You are never to use this command, however.
- **Run all tests**: `make test` — runs `cd go && GOWORK=off go test -mod=vendor ./...`
- **Regenerate sqlc + migrate**: `make sqlc` (generates Go code from SQL queries, then runs Goose migrations). You are never to use this command, however.
- **Run migrations only**: `make migrate`. You are never to use this command, however.
- **Vendor dependencies**: `make vndr` — runs `go work vendor`, `go mod tidy`, `go mod vendor` for both `go/` and `tap/` modules. You are never to use this command, however.
- **View service logs**: `make logs` (all services) or `make getlogs` (prompts for service name)
- **Database shell**: `make psql` (prompts for DB user)

## Architecture

Toro is a financial intelligence platform using a **"Thick Go, Thin Python"** architecture. All services communicate via **NATS JetStream** (3-node cluster). PostgreSQL (TimescaleDB) is the unified data store. Nginx reverse-proxies external traffic.

### Service Map

| Service | Role |
|---------|------|
| `gate` (8080) | Webhook ingress — receives Stripe/Plaid/QBO webhooks, publishes to NATS |
| `graphql` (8082) | GraphQL API (gqlgen) — user-facing queries and mutations |
| `ws` (8081) | WebSocket server — real-time client communication |
| `protocol` | Agent protocol engine — state machines, workflow orchestration, LLM calls |
| `sync` | Background ERP sync — QBO token refresh, Plaid transaction polling |
| `cdc-worker` | Change Data Capture — PostgreSQL logical replication → NATS |
| `fignode` (8083) | Financial intelligence layer — accounting analytics |
| `python-worker` | Python NATS subscriber — OCR, pandas-based data processing |
| `migrator` | Goose migration runner (runs once on startup) |
| `nginx` (80) | Reverse proxy |

### Repository Layout

```
go/                  — Main Go module (github.com/Yankzy/usetoro)
  cmd/               — Service entrypoints (gate, graphql, ws, sync, cdc-worker, protocol, fignode)
  internal/
    api/             — REST handlers, router, server, webhook verifiers
    auth/            — Ed25519 JWT, Argon2id, Redis token blacklist
    config/          — Viper-based config (defaults.yml + env overrides)
    connectors/      — ERP adapter interfaces (QBO client)
    database/        — sqlc-generated DB code (DO NOT EDIT — regenerated from sql/queries/)
    domain/          — Shared domain types
    erp/             — ERP adapters (Plaid, QuickBooks) and rule engine
    infra/vector/    — Pinecone vector DB client
    ingest/          — Webhook ingestion and NATS publishing
    queue/           — NATS client wrapper
    resilience/      — Circuit breakers, rate limiters
    services/        — Business logic (accounting, AI, cleanup, fignode, onboarding)
    store/           — Crypto, secrets, wallet ledger, QBO state
    workers/         — NATS JetStream background workers (event-driven registry pattern)
    wshandler/       — WebSocket hub, client management, message routing
tap/                 — Toro Agent Protocol (separate Go module)
  pkg/               — agent, contract, identity (DIDs), transport, redux, micrion, etc.
  agents/            — Agent implementations (classification, OCR, reconciliation, etc.)
  workflows/         — YAML-defined workflow blueprints (bookkeeping, categorization, CSV cleanup)
sql/schema/          — Goose migration files (sequential: 001, 002, ...)
sql/queries/         — sqlc-annotated SQL query definitions
container/           — Dockerfiles and docker-compose configs
docs/                — Architecture docs, pipeline designs, protocol specs
```

### Data Flow

1. External webhooks → `gate` → publish to NATS stream
2. Workers (in `protocol` service) consume NATS, process via LLM calls, write to Postgres
3. `graphql` serves processed data to clients
4. `sync` handles background ERP data fetching (QBO, Plaid)
5. `cdc-worker` streams Postgres changes back to NATS for real-time reactivity

### Multi-Tenancy

Hierarchical entity tree: `Apex CPA → Sub CPA → Client`. Row-Level Security via `SET LOCAL app.current_entity` in transactions. Use `ExecTx(ctx, entityID, fn)` to scope queries. Always filter by `authorized_entity_ids` in tenant-scoped SQL.

## Key Conventions

- **sqlc**: Write SQL in `sql/queries/*.sql` with `-- name: FuncName :one|:many|:exec` annotations. Run `make sqlc` to regenerate. Never edit `go/internal/database/*.sql.go`.
- **Migrations**: Goose sequential files in `sql/schema/`. Always include up + down.
- **Workers**: Implement the `workers.Worker` interface (`Init`, `Subscriptions`, `Handle`). Self-register via `init()` with `RegisterFactory`. The `Manager` handles ACK/NAK, panic recovery, and graceful shutdown.
- **Error wrapping**: Always `fmt.Errorf("context: %w", err)`. Return generic messages to users, log real errors.
- **Dependency injection**: Constructors receive interfaces, return concrete structs.
- **Logging**: `log/slog` with structured key-value pairs. Create child loggers with `h.Logger.With("request_id", id)`.
- **HTTP**: Go 1.22+ routing (`"POST /webhooks/{provider}/{conn_id}"`). Extract path values with `r.PathValue(...)`. Apply `http.MaxBytesReader` before body reads.
- **Resilience**: Circuit breakers (`sony/gobreaker`) for external calls. Rate limiters per-connection. Exponential backoff for NATS retries.
- **Never edit generated files**: `go/internal/database/*.sql.go`, `go/graph/generated.go`, `go/graph/model/models_gen.go`.
