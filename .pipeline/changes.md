# Changes Summary: Stripe Integration Microservice

## Files Created (12)

1. **`sql/schema/009_stripe_integration.sql`** — Goose migration with 3 new tables in `toro_core`: `stripe_checkout_sessions` (tracks payment page state), `financial_connection_attempts` (logs bank link initiation), `linked_bank_accounts` (stores connected accounts via Financial Connections). All tables use UUID PKs, reference `toro_core.entities`, and include appropriate indexes.

2. **`python-worker/app/__init__.py`** — Empty package marker.

3. **`python-worker/app/config.py`** — Environment variable singletons: `STRIPE_SECRET_KEY`, `STRIPE_WEBHOOK_SECRET`, `DATABASE_URL`, `NATS_URL`. Fail-fast on missing required vars.

4. **`python-worker/app/models.py`** — Pydantic v2 request/response models with validation (`HttpUrl` for URLs, `Field(gt=0)` for amount, `Field(min_length=1)` for required strings).

5. **`python-worker/app/database.py`** — asyncpg connection pool singleton + 4 query functions: `insert_checkout_session`, `update_checkout_session_status`, `insert_fc_attempt`, `upsert_linked_bank_account` (uses `INSERT ... ON CONFLICT DO UPDATE`).

6. **`python-worker/app/stripe_service.py`** — Stripe SDK v8+ wrapper using `stripe.StripeClient`. All API calls via `asyncio.to_thread()`. Functions: `create_checkout_session`, `create_fc_session`, `retrieve_fc_session`, `verify_webhook_signature`. Wraps `stripe.StripeError` → `StripeAPIError`.

7. **`python-worker/app/nats_client.py`** — NATS publish-only client. Module-level connection singleton. `connect`, `close`, `publish` functions. Publishes JSON payloads, logs and raises `NATSPublishError` on failure.

8. **`python-worker/app/routes.py`** — FastAPI `APIRouter` with 4 endpoints:
   - `POST /v1/payments/checkout/session` — creates Stripe Checkout, INSERTs PENDING row
   - `POST /v1/financial-connections/session` — creates FC session, logs INITIATED attempt
   - `POST /v1/financial-connections/sync` — retrieves session, upserts accounts, publishes NATS event
   - `POST /v1/webhooks/stripe` — verifies signature, routes `checkout.session.completed`, `checkout.session.expired`, `financial_connections.session.updated`

9. **`python-worker/app/errors.py`** — `StripeAPIError` (with HTTP status code) and `NATSPublishError`.

10. **`python-worker/main.py`** — FastAPI app factory with lifespan (init/close DB pool + NATS), global exception handlers for `StripeAPIError` and `NATSPublishError`, and `/health` endpoint.

11. **`python-worker/requirements.txt`** — Replaced: removed `openai`, added `fastapi`, `uvicorn[standard]`, `stripe>=8.0.0`, `asyncpg`, `pydantic>=2.0.0`. Kept `nats-py`, `python-dotenv`.

12. **`python-worker/Dockerfile`** — Replaced: installs `libpq-dev` + `gcc` for asyncpg build, runs `uvicorn main:app --host 0.0.0.0 --port 8000`.

## Files Modified (3)

13. **`container/docker-compose.yml`** — Renamed `python-worker` service to `stripe-worker`. Added port `8005:8000`, env vars (`STRIPE_SECRET_KEY`, `STRIPE_WEBHOOK_SECRET`, `DATABASE_URL`, `PGOPTIONS`), and `db` health dependency.

14. **`container/.env`** — Added `STRIPE_SECRET_KEY` and `STRIPE_WEBHOOK_SECRET` with placeholder values.

15. **`container/nginx/nginx.conf`** — Added `/v1/webhooks/stripe` location block proxying to `stripe-worker:8000`.

## What Changed

The python-worker was converted from a simple NATS subscriber (mock OCR) into a full Stripe integration microservice running FastAPI on port 8000. It connects to the shared PostgreSQL database (same as Go backend) and publishes state-change events to NATS. The service sits behind nginx for webhook ingress and communicates internally over the `toro-net` Docker network.
