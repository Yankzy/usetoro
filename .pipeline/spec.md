# Spec: Stripe Integration Python Microservice

## OPEN QUESTIONS

None. All decisions resolved from codebase analysis.

---

## 1. Architecture Decisions

### 1.1 PostgreSQL Tables (NEW, not modifying existing tables)

The PRD references a generic "transactions" table, but the existing `toro_core.transactions` is for ERP data (QuickBooks transactions), not payment flows. The existing `toro_core.wallet_transactions` already tracks Stripe checkout sessions for wallet top-ups but is tightly coupled to the Micrion ledger system. We will create three new dedicated tables in the `toro_core` schema so the Python service owns its data and does not collide with Go backend tables.

### 1.2 NATS Subject Pattern

Follow the existing pattern from `go/internal/ingest/publisher.go`:
- Subject format: `{provider}.{event_type}` (e.g., `stripe.checkout.completed`)
- Messages are plain `nats.publish` (NOT JetStream) with JSON payload, consistent with how the Go `protocol` service publishes to `proof.*` subjects and `go/internal/api/slack_webhook.go` publishes.

### 1.3 Application Structure (Python Package)

The existing `python-worker/main.py` is a single-file NATS subscriber. We will restructure to a proper package layout with FastAPI.

```
python-worker/
  main.py                  # Uvicorn entrypoint, FastAPI app factory
  app/
    __init__.py
    config.py              # Settings from env vars
    database.py            # asyncpg pool + query functions
    nats_client.py         # NATS connection + publish helper
    stripe_service.py      # StripeClient wrapper for all Stripe API calls
    routes.py              # All FastAPI route handlers
    models.py              # Pydantic request/response models
    errors.py              # HTTP exception classes
  requirements.txt
  Dockerfile
```

### 1.4 Port

Expose FastAPI on port **8000** internally. Map to host port **8005** in docker-compose to avoid conflicts with existing services (gate:8080, ws:8081, graphql:8082, fignode:8083, protocol:9090).

### 1.5 Stripe SDK Version

Use `stripe` Python package v8+ (the modern API). Instantiate `stripe.StripeClient(os.environ["STRIPE_SECRET_KEY"])` as a module-level singleton -- NOT the legacy `stripe.api_key`.

---

## 2. Database Migrations

### File: `/Users/Yankz/programming/usetoro/sql/schema/009_stripe_integration.sql`

Create a new migration file **009**. This is the next sequential number after 008.

**Up migration:**

```sql
-- +goose Up
-- +goose StatementBegin

-- Stripe Checkout Sessions: tracks hosted payment page state
CREATE TABLE toro_core.stripe_checkout_sessions (
    id               UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    entity_id        UUID NOT NULL REFERENCES toro_core.entities(id) ON DELETE CASCADE,
    stripe_session_id TEXT NOT NULL UNIQUE,
    amount_cents     BIGINT NOT NULL,
    product_name     TEXT NOT NULL,
    status           TEXT NOT NULL DEFAULT 'PENDING',  -- PENDING, COMPLETED, EXPIRED
    metadata         JSONB DEFAULT '{}'::jsonb,
    created_at       TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at       TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_stripe_checkout_entity ON toro_core.stripe_checkout_sessions(entity_id);
CREATE INDEX idx_stripe_checkout_status ON toro_core.stripe_checkout_sessions(status);

-- Financial Connection Attempts: logs each bank link initiation
CREATE TABLE toro_core.financial_connection_attempts (
    id               UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    entity_id        UUID NOT NULL REFERENCES toro_core.entities(id) ON DELETE CASCADE,
    fc_session_id    TEXT NOT NULL,
    stripe_customer_id TEXT NOT NULL,
    status           TEXT NOT NULL DEFAULT 'INITIATED',  -- INITIATED, LINKED, FAILED
    created_at       TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at       TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_fc_attempts_entity ON toro_core.financial_connection_attempts(entity_id);
CREATE INDEX idx_fc_attempts_session ON toro_core.financial_connection_attempts(fc_session_id);

-- Linked Bank Accounts: stores accounts connected via Financial Connections
CREATE TABLE toro_core.linked_bank_accounts (
    id                 UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    entity_id          UUID NOT NULL REFERENCES toro_core.entities(id) ON DELETE CASCADE,
    fc_session_id      TEXT NOT NULL,
    stripe_account_id  TEXT NOT NULL UNIQUE,
    institution_name   TEXT NOT NULL,
    last4              TEXT,
    subcategory        TEXT,
    status             TEXT NOT NULL,
    metadata           JSONB DEFAULT '{}'::jsonb,
    created_at         TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at         TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_linked_accounts_entity ON toro_core.linked_bank_accounts(entity_id);
CREATE INDEX idx_linked_accounts_session ON toro_core.linked_bank_accounts(fc_session_id);

-- +goose StatementEnd
```

**Down migration:**

```sql
-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS toro_core.linked_bank_accounts;
DROP TABLE IF EXISTS toro_core.financial_connection_attempts;
DROP TABLE IF EXISTS toro_core.stripe_checkout_sessions;
-- +goose StatementEnd
```

### Design justification for NOT modifying `toro_core.transactions`

The existing `toro_core.transactions` table is reserved for ERP-sourced transaction data. It has a UNIQUE constraint on `(entity_id, external_id)`, RLS policies, and is queried by the Go backend's reporting layer. Adding nullable `status`, `session_id`, and `metadata` columns would:
1. Pollute ERP data with payment state
2. Force NULL columns on all existing and future ERP rows
3. Require Go backend changes to ignore those columns when inserting ERP transactions
4. Break the conceptual boundary between ERP data and payment flows

The existing `toro_core.wallet_transactions` already tracks Stripe sessions specifically for wallet top-ups but is designed around the Micrion ledger (see `micrion_amount` column, `wallet_id` FK). Our new tables are independent, dedicated to the generic checkout and financial connections use cases requested in the PRD.

---

## 3. Files to Create

### 3.1 `/Users/Yankz/programming/usetoro/python-worker/app/__init__.py`

Empty file. Marks the directory as a Python package.

### 3.2 `/Users/Yankz/programming/usetoro/python-worker/app/config.py`

Module-level singleton that reads environment variables at import time.

```python
import os

STRIPE_SECRET_KEY: str = os.environ["STRIPE_SECRET_KEY"]
STRIPE_WEBHOOK_SECRET: str = os.environ["STRIPE_WEBHOOK_SECRET"]
DATABASE_URL: str = os.environ["DATABASE_URL"]
NATS_URL: str = os.environ.get("NATS_URL", "nats://localhost:4222")
```

Edge cases: If any required env var is missing, `os.environ[...]` raises `KeyError` at startup, which causes uvicorn to fail fast before accepting traffic. This is intentional.

### 3.3 `/Users/Yankz/programming/usetoro/python-worker/app/models.py`

Pydantic v2 models for request/response validation.

**Request models:**

```python
from pydantic import BaseModel, Field, HttpUrl

class CreateCheckoutSessionRequest(BaseModel):
    user_id: str                          # maps to entity_id in DB
    amount_in_dollars: float = Field(gt=0)
    product_name: str
    success_url: HttpUrl
    cancel_url: HttpUrl

class CreateFCSessionRequest(BaseModel):
    user_id: str                          # maps to entity_id in DB
    stripe_customer_id: str = Field(min_length=1)

class SyncFCSessionRequest(BaseModel):
    session_id: str = Field(min_length=1) # Stripe FC session ID
    user_id: str                          # maps to entity_id in DB
```

**Response models:**

```python
class CheckoutSessionResponse(BaseModel):
    checkout_url: str

class FCSessionResponse(BaseModel):
    client_secret: str

class WebhookResponse(BaseModel):
    status: str

class BankAccountResponse(BaseModel):
    id: str
    stripe_account_id: str
    institution_name: str
    last4: str | None
    subcategory: str | None
    status: str
```

Edge cases: `amount_in_dollars` must be positive (>0). Validate with `Field(gt=0)`. `stripe_customer_id` must be non-empty. `success_url` and `cancel_url` must be valid HTTP(S) URLs (use `HttpUrl` type).

### 3.4 `/Users/Yankz/programming/usetoro/python-worker/app/database.py`

Provides an `asyncpg` connection pool and typed query functions.

**Module-level singleton:**

```python
import asyncpg

_pool: asyncpg.Pool | None = None

async def init_pool(dsn: str) -> None:
    """Create the connection pool. Called on FastAPI startup."""
    global _pool
    _pool = await asyncpg.create_pool(dsn=dsn, min_size=2, max_size=10)

async def close_pool() -> None:
    """Close the pool. Called on FastAPI shutdown."""
    global _pool
    if _pool:
        await _pool.close()

def get_pool() -> asyncpg.Pool:
    """Get the pool. Raises RuntimeError if not initialized."""
    if _pool is None:
        raise RuntimeError("Database pool not initialized")
    return _pool
```

**Query functions (top-level async functions, NOT methods, following the module-as-namespace pattern):**

```python
async def insert_checkout_session(
    entity_id: str,
    stripe_session_id: str,
    amount_cents: int,
    product_name: str,
    metadata: dict,
) -> None: ...

async def update_checkout_session_status(
    stripe_session_id: str,
    status: str,
) -> None: ...

async def insert_fc_attempt(
    entity_id: str,
    fc_session_id: str,
    stripe_customer_id: str,
) -> None: ...

async def upsert_linked_bank_account(
    entity_id: str,
    fc_session_id: str,
    stripe_account_id: str,
    institution_name: str,
    last4: str | None,
    subcategory: str | None,
    status: str,
) -> None: ...
```

`upsert_linked_bank_account` must use `INSERT ... ON CONFLICT (stripe_account_id) DO UPDATE SET ...`.

Edge cases:
- Pool not initialized: `get_pool()` raises `RuntimeError`, which FastAPI converts to 500.
- DB connection lost mid-query: `asyncpg` raises `ConnectionDoesNotExistError` or `InterfaceError`. These bubble up as unhandled exceptions, resulting in 500. The service should catch them in route handlers and log.
- Duplicate checkout session (same `stripe_session_id`): UNIQUE constraint on `stripe_session_id` causes `asyncpg.UniqueViolationError`. The route handler should catch this and return 409 Conflict.

### 3.5 `/Users/Yankz/programming/usetoro/python-worker/app/stripe_service.py`

Wraps all Stripe SDK calls. Module-level `StripeClient` singleton.

```python
import stripe
from stripe import StripeClient
from app.config import STRIPE_SECRET_KEY

stripe_client = StripeClient(STRIPE_SECRET_KEY)


async def create_checkout_session(
    amount_cents: int,
    product_name: str,
    success_url: str,
    cancel_url: str,
    user_id: str,
) -> dict:
    """
    Calls stripe_client.v1.checkout.sessions.create.
    Returns dict with at least {"id": str, "url": str}.
    Wraps stripe.StripeError -> raises StripeAPIError.
    Uses asyncio.to_thread() to avoid blocking the event loop.
    """
    ...


async def create_fc_session(
    stripe_customer_id: str,
) -> dict:
    """
    Calls stripe_client.v1.financial_connections.sessions.create.
    Returns dict with at least {"id": str, "client_secret": str}.
    Uses hardcoded permissions: ["balances", "ownership", "payment_method", "transactions"].
    Uses asyncio.to_thread() to avoid blocking the event loop.
    """
    ...


async def retrieve_fc_session(session_id: str) -> dict:
    """
    Calls stripe_client.v1.financial_connections.sessions.retrieve.
    Returns full session dict with accounts.data array.
    Uses asyncio.to_thread() to avoid blocking the event loop.
    """
    ...


async def verify_webhook_signature(
    raw_body: bytes,
    signature_header: str,
    webhook_secret: str,
) -> stripe.Event:
    """
    Calls stripe.Webhook.construct_event.
    Raises HTTPException(400) on SignatureVerificationError.
    This is CPU-bound (no network I/O), so no asyncio.to_thread() needed.
    """
    ...
```

Key constraints:
- All Stripe API calls are synchronous (the SDK's HTTP calls are blocking). Use `asyncio.to_thread()` to avoid blocking the event loop.
- Every function wraps `stripe.StripeError` and converts to `StripeAPIError` with appropriate status codes.
- The `create_checkout_session` function must set `metadata={"user_id": user_id}` on the Stripe session so the webhook can extract it.

Edge cases:
- Stripe API timeout: `stripe.APIConnectionError` is a subclass of `StripeError`. Caught generically, return 502 Bad Gateway.
- Invalid API key: `stripe.AuthenticationError`. Return 500 with message "Stripe authentication failed" (do NOT leak the key in logs).

### 3.6 `/Users/Yankz/programming/usetoro/python-worker/app/nats_client.py`

Module-level NATS connection. Simple publish-only (no subscriptions needed by this service).

```python
import json
import logging
import nats
from nats.aio.client import Client as NATS

_nc: NATS | None = None

async def connect(nats_url: str) -> None:
    """Connect to NATS. Called on FastAPI startup."""
    global _nc
    servers = nats_url.split(",")
    _nc = await nats.connect(servers=servers, name="stripe-worker")

async def close() -> None:
    """Drain and close. Called on FastAPI shutdown."""
    global _nc
    if _nc:
        await _nc.drain()

async def publish(subject: str, payload: dict) -> None:
    """
    Publish a JSON message to NATS.
    Logs and raises NATSPublishError on failure.
    """
    global _nc
    if _nc is None:
        raise NATSPublishError("NATS not connected")
    data = json.dumps(payload).encode()
    try:
        await _nc.publish(subject, data)
    except Exception as e:
        logging.error("Failed to publish to NATS", extra={"subject": subject, "error": str(e)})
        raise NATSPublishError(f"Failed to publish to {subject}: {e}")
```

Edge cases:
- NATS publish failure: `nats.errors.ConnectionClosedError`. Log the error, then raise `NATSPublishError` which the route handler converts to 500. This is the PRD's explicit requirement: "If the broker is unreachable, log heavily and return a 500 status."
- Message serialization: `json.dumps` on an un-serializable object raises `TypeError`. This is a programming error, not a runtime concern. The callers always pass plain dicts.

### 3.7 `/Users/Yankz/programming/usetoro/python-worker/app/routes.py`

All FastAPI route handlers. Uses `APIRouter`.

```python
from fastapi import APIRouter, Request, HTTPException

router = APIRouter(prefix="/v1")


@router.post("/payments/checkout/session", response_model=CheckoutSessionResponse)
async def create_checkout_session(req: CreateCheckoutSessionRequest) -> CheckoutSessionResponse:
    """
    Feature 1: Create Stripe Checkout Session.
    1. Convert dollars to cents (int(round(req.amount_in_dollars * 100)))
    2. Call stripe_service.create_checkout_session(...)
    3. INSERT into toro_core.stripe_checkout_sessions with status=PENDING
    4. Return {"checkout_url": session.url}
    """
    ...


@router.post("/financial-connections/session", response_model=FCSessionResponse)
async def create_fc_session(req: CreateFCSessionRequest) -> FCSessionResponse:
    """
    Feature 2a: Create Financial Connections session.
    1. Call stripe_service.create_fc_session(...)
    2. INSERT into toro_core.financial_connection_attempts with status=INITIATED
    3. Return {"client_secret": session.client_secret}
    """
    ...


@router.post("/financial-connections/sync", response_model=list[BankAccountResponse])
async def sync_fc_session(req: SyncFCSessionRequest) -> list[BankAccountResponse]:
    """
    Feature 2b: Sync linked accounts from completed FC session.
    1. Call stripe_service.retrieve_fc_session(...)
    2. Extract accounts from session["accounts"]["data"]
    3. For each account: upsert into toro_core.linked_bank_accounts
    4. Publish "stripe.financial_connections.linked" to NATS
    5. Return list of upserted bank accounts
    """
    ...


@router.post("/webhooks/stripe")
async def stripe_webhook(request: Request):
    """
    Feature 3: Stripe webhook listener.
    1. Read raw body with await request.body()
    2. Get stripe-signature header
    3. Verify with stripe_service.verify_webhook_signature(...)
    4. Route by event.type:
       - checkout.session.completed: update checkout session status to COMPLETED, publish stripe.checkout.completed
       - checkout.session.expired: update checkout session status to EXPIRED, publish stripe.checkout.expired
       - financial_connections.session.updated: publish stripe.financial_connections.session_updated
    5. Return {"status": "success"}
    """
    ...
```

**Detailed per-route behavior:**

**POST /v1/payments/checkout/session:**
- Dollar-to-cents: `amount_cents = int(round(amount_in_dollars * 100))`. Use `round()` to avoid floating-point truncation errors (e.g., 25.50 * 100 = 2549.9999... becomes 2550 with round).
- Catch `asyncpg.UniqueViolationError` on the INSERT and return 409 Conflict `{"detail": "Checkout session already exists"}`.
- On Stripe error, let the `StripeAPIError` from `stripe_service` propagate (caught by the global exception handler).

**POST /v1/financial-connections/session:**
- Catch `asyncpg.UniqueViolationError` on the INSERT and return 409 Conflict.
- The Stripe API call includes `permissions=["balances", "ownership", "payment_method", "transactions"]` as hardcoded (per the PRD).

**POST /v1/financial-connections/sync:**
- If `retrieve_fc_session` returns a session with no accounts (`len(session["accounts"]["data"]) == 0`), return an empty list `[]` with 200 OK. Do NOT publish NATS event.
- If the session retrieval fails with a Stripe 404, return 404 `{"detail": "Financial Connections session not found"}`.
- The NATS publish is fire-and-forget (no reply expected). If it fails, return 500.

**POST /v1/webhooks/stripe:**
- Signature verification failure: return 400 `{"detail": "Invalid signature"}`.
- Unknown event type: log a warning, return 200 `{"status": "ignored"}`. Do NOT 400; Stripe expects 200 for unhandled events so it doesn't retry.
- The `checkout.session.completed` handler extracts `user_id` from `event.data.object.metadata.user_id`. If missing, log an error and return 200 (don't fail the webhook; Stripe would retry forever).
- The `checkout.session.expired` handler updates status to `EXPIRED`. If the DB row doesn't exist (race condition or idempotency), log and return 200.
- The NATS publish payload for checkout events: `{"session_id": str, "user_id": str, "event_type": str}`.
- The NATS publish payload for FC session updated: `{"session_id": str, "accounts": [...], "status": str}`.

### 3.8 `/Users/Yankz/programming/usetoro/python-worker/app/errors.py`

Custom error classes:

```python
class StripeAPIError(Exception):
    """Wraps stripe.StripeError with HTTP status code."""
    def __init__(self, message: str, status_code: int = 502, code: str = "STRIPE_ERROR"):
        super().__init__(message)
        self.message = message
        self.status_code = status_code
        self.code = code


class NATSPublishError(Exception):
    """Raised when NATS publish fails."""
    def __init__(self, message: str):
        super().__init__(message)
        self.message = message
```

### 3.9 `/Users/Yankz/programming/usetoro/python-worker/main.py`

FastAPI application factory and uvicorn entrypoint.

```python
"""
Stripe Integration Microservice.

HTTP server: FastAPI (port 8000)
NATS client: publishes events to the ATS broker
Database: asyncpg pool to shared PostgreSQL

Startup:
  uvicorn main:app --host 0.0.0.0 --port 8000
"""

import logging
from contextlib import asynccontextmanager
from fastapi import FastAPI, Request
from fastapi.responses import JSONResponse

from app.config import DATABASE_URL, NATS_URL
from app.database import init_pool, close_pool
from app.nats_client import connect as nats_connect, close as nats_close
from app.routes import router
from app.errors import StripeAPIError, NATSPublishError


@asynccontextmanager
async def lifespan(app: FastAPI):
    # Startup
    logging.info("Connecting to database...")
    await init_pool(DATABASE_URL)
    logging.info("Connecting to NATS...")
    await nats_connect(NATS_URL)
    logging.info("Stripe Integration Microservice started")
    yield
    # Shutdown
    logging.info("Shutting down...")
    await close_pool()
    await nats_close()
    logging.info("Shutdown complete")


app = FastAPI(
    title="Stripe Integration Microservice",
    version="1.0.0",
    lifespan=lifespan,
)

app.include_router(router)


@app.exception_handler(StripeAPIError)
async def stripe_error_handler(request: Request, exc: StripeAPIError):
    return JSONResponse(
        status_code=exc.status_code,
        content={"detail": exc.message, "code": exc.code},
    )


@app.exception_handler(NATSPublishError)
async def nats_error_handler(request: Request, exc: NATSPublishError):
    return JSONResponse(
        status_code=500,
        content={"detail": "Event broker unavailable", "code": "NATS_ERROR"},
    )


@app.get("/health")
async def health():
    return {"status": "healthy"}
```

Edge cases:
- DB pool init fails: uvicorn will fail to start (exception in lifespan startup). Docker will restart the container. This is correct behavior.
- NATS connect fails: same as above.
- The `/health` endpoint should NOT check DB or NATS connectivity (keep it simple, Docker healthcheck uses it for liveness).

### 3.10 `/Users/Yankz/programming/usetoro/python-worker/requirements.txt`

Replace entirely:

```
fastapi>=0.115.0
uvicorn[standard]>=0.30.0
stripe>=8.0.0
asyncpg>=0.29.0
nats-py>=0.9.0
python-dotenv>=1.0.0
pydantic>=2.0.0
```

Remove `openai` (no longer needed). Keep `nats-py` and `python-dotenv`.

### 3.11 `/Users/Yankz/programming/usetoro/python-worker/Dockerfile`

Replace entirely:

```dockerfile
FROM python:3.12-slim

WORKDIR /app

# Install system dependencies for asyncpg (requires libpq-dev at build time)
RUN apt-get update && apt-get install -y --no-install-recommends \
    libpq-dev gcc \
    && rm -rf /var/lib/apt/lists/*

COPY python-worker/requirements.txt .
RUN pip install --no-cache-dir -r requirements.txt

COPY python-worker/ .

# Run uvicorn on port 8000
CMD ["uvicorn", "main:app", "--host", "0.0.0.0", "--port", "8000"]
```

Edge cases: `asyncpg` requires `libpq-dev` at install time and `libpq5` at runtime. The `python:3.12-slim` image already includes `libpq5` but not `libpq-dev`. We install `libpq-dev` + `gcc` for the pip install step, then the compiled binary links against the runtime `libpq5`.

---

## 4. Files to Modify

### 4.1 `/Users/Yankz/programming/usetoro/container/docker-compose.yml`

Replace the existing `python-worker` service block (lines 64-76) with:

```yaml
  stripe-worker:
    build:
      context: ../
      dockerfile: python-worker/Dockerfile
    restart: always
    ports:
      - "8005:8000"
    environment:
      STRIPE_SECRET_KEY: ${STRIPE_SECRET_KEY}
      STRIPE_WEBHOOK_SECRET: ${STRIPE_WEBHOOK_SECRET}
      DATABASE_URL: ${DATABASE_URL}
      NATS_URL: nats://nats-1:4222,nats://nats-2:4222,nats://nats-3:4222
      PGOPTIONS: "-c search_path=toro_core,shadow_erp,fignode,public"
    depends_on:
      db:
        condition: service_healthy
      nats-1:
        condition: service_healthy
    networks:
      - toro-net
```

Changes from the old `python-worker`:
- Renamed service from `python-worker` to `stripe-worker`
- Added `ports: "8005:8000"` to expose the HTTP API
- Added `STRIPE_SECRET_KEY`, `STRIPE_WEBHOOK_SECRET`, `DATABASE_URL`, `PGOPTIONS` env vars
- Added `db` as a `depends_on` dependency

### 4.2 `/Users/Yankz/programming/usetoro/.env`

Add these lines (user must fill in real values):

```
STRIPE_SECRET_KEY=sk_test_...
STRIPE_WEBHOOK_SECRET=whsec_...
```

### 4.3 `/Users/Yankz/programming/usetoro/container/nginx/nginx.conf`

Add a new location block for the Stripe webhook endpoint so external Stripe webhooks can reach the service through the nginx reverse proxy. This block must be placed **above** the `/api/v1/` location block because nginx uses the first matching prefix location.

Add after the `/api/v1/` block (after line 67) but before the `/api/` block (line 70):

```nginx
    # --- Stripe Webhook (direct pass-through to stripe-worker) ---
    location /v1/webhooks/stripe {
        set $upstream_stripe stripe-worker;
        proxy_pass http://$upstream_stripe:8000;
        proxy_set_header Host $host;
        proxy_set_header X-Real-IP $remote_addr;
        proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto $scheme;
    }
```

The `/v1/webhooks/stripe` path does NOT start with `/api/`, so it does not conflict with existing locations. It can be placed anywhere in the server block; placing it after the `/api/v1/` block is clean.

---

## 5. NOT in Scope (Explicitly Excluded)

- **No Go code changes.** The Go backend's `gate` service and its Stripe connector (`go/internal/connectors/stripe.go`) are left untouched. The Python microservice runs alongside Go services, not replacing them.
- **No frontend code.**
- **No Stripe Connect / marketplace features.**
- **No idempotency keys for Checkout Session creation.** The PRD does not mention them.
- **No RLS policies on the new tables.** The Go backend uses `SET LOCAL app.current_entity` for RLS, but the Python service does NOT set this context variable. The new tables do NOT have RLS enabled. If the Go backend needs to query these tables, it should use its own entity-scoped queries.
- **No JetStream consumer/subscriber in the Python service.** This service only publishes to NATS, it does not consume messages. The Go backend's existing `stripe.>` subject subscription in `go/internal/config/defaults.yml:56` can be extended to handle the new subjects.

---

## 6. Verification Steps

### 6.1 Migration

```bash
cd /Users/Yankz/programming/usetoro
make migrate
# or directly:
docker compose -f container/docker-compose.yml run --rm migrator
```

Verify tables exist:
```bash
docker compose -f container/docker-compose.yml exec db psql -U toro -d toro -c "\dt toro_core.stripe_*"
docker compose -f container/docker-compose.yml exec db psql -U toro -d toro -c "\dt toro_core.financial_*"
docker compose -f container/docker-compose.yml exec db psql -U toro -d toro -c "\dt toro_core.linked_*"
```

### 6.2 Service Startup

```bash
docker compose -f container/docker-compose.yml build stripe-worker
docker compose -f container/docker-compose.yml up stripe-worker
```

Check health:
```bash
curl http://localhost:8005/health
# Expected: {"status":"healthy"}
```

### 6.3 Feature 1: Checkout Session

```bash
curl -X POST http://localhost:8005/v1/payments/checkout/session \
  -H "Content-Type: application/json" \
  -d '{
    "user_id": "00000000-0000-0000-0000-000000000001",
    "amount_in_dollars": 25.50,
    "product_name": "Premium Plan",
    "success_url": "https://example.com/success",
    "cancel_url": "https://example.com/cancel"
  }'
# Expected: {"checkout_url": "https://checkout.stripe.com/c/pay/..."}

# Verify DB row:
docker compose -f container/docker-compose.yml exec db psql -U toro -d toro \
  -c "SELECT id, entity_id, stripe_session_id, amount_cents, status FROM toro_core.stripe_checkout_sessions;"
```

### 6.4 Feature 2a: Financial Connections Session

```bash
curl -X POST http://localhost:8005/v1/financial-connections/session \
  -H "Content-Type: application/json" \
  -d '{
    "user_id": "00000000-0000-0000-0000-000000000001",
    "stripe_customer_id": "cus_test123"
  }'
# Expected: {"client_secret": "fcsess_..."}
```

### 6.5 Feature 2b: Sync Financial Connections

Requires a real Stripe FC session ID from Stripe dashboard (cannot be fully tested with curl alone without a real linked account).

### 6.6 Feature 3: Webhook

Use Stripe CLI to forward test webhooks:
```bash
stripe listen --forward-to localhost:8005/v1/webhooks/stripe
stripe trigger checkout.session.completed
```

Check logs for:
- "Received webhook: checkout.session.completed"
- DB: status updated to COMPLETED
- NATS: message published to `stripe.checkout.completed`

Use a NATS subscriber to verify:
```bash
nats sub "stripe.>" -s nats://localhost:4222
```

### 6.7 Negative Cases

- Missing `STRIPE_SECRET_KEY` env var: container should crash-loop (FastAPI startup fails with KeyError).
- Invalid Stripe API key: curl should return 500 with `"code": "STRIPE_ERROR"`.
- Invalid webhook signature: curl should return 400 `"detail": "Invalid signature"`.
- Duplicate checkout session ID: curl should return 409 `"detail": "Checkout session already exists"`.
- NATS down: webhook endpoint should return 500 so Stripe retries.
