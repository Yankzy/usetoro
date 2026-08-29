"""
Stripe Integration Microservice.

HTTP server: FastAPI (port 8000)
NATS client: publishes events to the ATS broker
Database: asyncpg pool to shared PostgreSQL

Startup:
  uvicorn main:app --host 0.0.0.0 --port 8000
"""

import sys
import os
# Ensure app/ directory is on sys.path for submodule imports (e.g. reconciliation_prod)
_app_dir = os.path.join(os.path.dirname(__file__), "app")
if _app_dir not in sys.path:
    sys.path.insert(0, _app_dir)

import logging
from contextlib import asynccontextmanager

from fastapi import FastAPI, Request
from fastapi.responses import JSONResponse
from openai import AsyncOpenAI

from app.config import DATABASE_URL, NATS_URL
from app.database import init_pool, close_pool
from app.errors import StripeAPIError, NATSPublishError
from app.nats_client import connect as nats_connect, close as nats_close, subscribe_jetstream, subscribe as nats_subscribe, get_nc
from app.marketing import handlers
from app.openai.ocr import handle_ocr_request
from app.reconciliation_prod.reconciliation_worker import handle_reconciliation_request
from app.reconciliation_prod.routing_worker import handle_routing_request
from app.routes import router

logging.basicConfig(level=logging.INFO, format="%(asctime)s [%(levelname)s] %(name)s: %(message)s")


@asynccontextmanager
async def lifespan(app: FastAPI):
    logging.info("Connecting to database...")
    await init_pool(DATABASE_URL)
    logging.info("Connecting to NATS...")
    await nats_connect(NATS_URL)
    
    # Register Marketing DAG NATS subscribers
    await subscribe_jetstream("worker.inbox.marketing.ingest", "marketing_ingest_group", handlers.handle_ingest_request)
    await subscribe_jetstream("worker.inbox.marketing.analyze", "marketing_analyze_group", handlers.handle_analyze_request)
    await subscribe_jetstream("worker.inbox.marketing.discover", "marketing_discover_group", handlers.handle_discover_request)
    await subscribe_jetstream("worker.inbox.marketing.verify", "marketing_verify_group", handlers.handle_verify_request)

    # Register Reconciliation & Routing NATS subscribers
    await subscribe_jetstream("worker.inbox.reconciliation", "reconciliation_group", handle_reconciliation_request)
    await subscribe_jetstream("worker.inbox.routing", "routing_group", handle_routing_request)

    logging.info("Stripe Integration Microservice started")
    yield
    print("🛑 [PYTHON-WORKER] Shutting down...", flush=True)
    logging.info("Shutting down...")
    await close_pool()
    await nats_close()
    print("🛑 [PYTHON-WORKER] Shutdown complete", flush=True)
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
