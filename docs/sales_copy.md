# Prompt: CTO Infra Sales Copy Blueprint (Code-Backed Only)

You are writing outbound sales copy for CTOs.

Hard rule: every claim must be grounded in already-implemented code. If a detail is not in the evidence list below, do not mention it.

## Objective
Generate infra-focused copy that positions UseToro as a reliability-first event ingestion and data movement layer.

## Audience
- CTO / VP Engineering
- Platform, Data, SRE, and Infrastructure leaders

## Evidence Pack (Implemented In Code)
Use only these claims.

1) Edge ingestion and webhook handling (Go API gateway)
- Generic endpoint: `POST /webhooks/{provider}/{conn_id}`.
- Provider-specific verification registry supports Stripe and HMAC providers (QBO).
- Connection/webhook secret is fetched from store before verification.
- Rate limiting and failed-auth attempt blocking are enforced per connection.
- Request IDs are propagated (`X-Request-ID`) for traceability.
- Health endpoints: `/health/live` and `/health/ready`.
- Readiness explicitly checks DB + NATS connectivity.
Code refs:
- `go/internal/api/router.go`
- `go/internal/api/handler.go`
- `go/internal/api/verifier.go`
- `go/internal/api/stripe_verifier.go`
- `go/internal/api/verifier_hmac.go`

2) Durable messaging path (NATS JetStream)
- Webhook events are published to JetStream subjects via a Go publisher.
- Publisher has a circuit breaker to protect downstream during failure spikes.
- Gateway startup ensures streams exist from config and waits for stream readiness.
- Sync publish path uses JetStream pub-ack semantics.
Code refs:
- `go/internal/ingest/publisher.go`
- `go/internal/queue/client.go`
- `go/cmd/gate/main.go`

3) Retry and dead-letter strategy
- On publish failure, async exponential retry runs (5 attempts over ~2 minutes).
- If retries exhaust, event is persisted in Redis dead-letter keys + sorted index.
Code refs:
- `go/internal/api/retry.go`
- `go/cmd/gate/main.go`

4) CDC pipeline (Postgres WAL -> JetStream)
- Dedicated worker tails Postgres logical replication using `pgoutput`.
- Uses named replication slot (`toro_nats_slot`) and publication (`toro_ledger_pub`).
- Decodes INSERT/UPDATE/DELETE into normalized event envelope.
- Publishes synchronously to JetStream subject pattern `ledger.<table>.<action>`.
- Uses `Nats-Msg-Id` with Postgres LSN for deduplication semantics.
- If publish fails, worker returns error (does not advance checkpoint), protecting durability.
Code refs:
- `go/cmd/cdc-worker/main.go`
- `go/internal/cdc/replicator.go`
- `go/internal/cdc/decoder.go`
- `go/internal/cdc/publisher.go`

5) Auth and multi-tenant enforcement primitives
- Ed25519 JWT verification done locally in services.
- Optional Redis blacklist check for token revocation.
- Optional DB user status check during token verification.
- RBAC middleware resolves descendant entity scope and caches in Redis.
Code refs:
- `go/internal/auth/middleware.go`
- `go/internal/auth/claims.go`
- `go/cmd/gate/main.go`
- `go/cmd/ws/main.go`

6) Realtime delivery plane
- WebSocket hub supports room-scoped broadcasting (`entity_id` rooms).
- WS service uses Redis, Postgres, and NATS initialization checks.
Code refs:
- `go/internal/wshandler/hub.go`
- `go/internal/wshandler/handler.go`
- `go/cmd/ws/main.go`

## Architectural Philosophy To Communicate
- Reliability over convenience.
- Fail isolated, recover fast.
- Verify and gate at the edge before publish.
- Use durable logs/streams for decoupling.
- Keep identity verification local to reduce auth-path latency.
- Build observable flows (request IDs, health/readiness, explicit retries).

## Hard Exclusions
Do not mention or imply:
- Accounting
- Fignode
- Virtual backoffice
- Any feature not listed in Evidence Pack

## Tone
- Technical, direct, and infrastructure-literate.
- No fluff, no generic AI claims, no vague “platform magic”.
- Use concrete failure-mode language (retry, backoff, circuit breaker, durability, replay, readiness).

## Output Format
Return exactly 3 variants:
1. Cold outbound email (120-180 words)
2. LinkedIn DM (60-90 words)
3. 30-second founder pitch track

Each variant must include:
- Subject/opener
- Main body
- CTA for a 20-minute technical architecture call

## Validation Gate (Must Pass)
Before finalizing each variant, silently check:
- Every technical claim maps to at least one file in Evidence Pack.
- No banned topics appear.
- No speculative roadmap language.
