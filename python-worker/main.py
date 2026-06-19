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
from app.errors import StripeAPIError, NATSPublishError
from app.nats_client import connect as nats_connect, close as nats_close
from app.routes import router

logging.basicConfig(level=logging.INFO, format="%(asctime)s [%(levelname)s] %(name)s: %(message)s")


@asynccontextmanager
async def lifespan(app: FastAPI):
    logging.info("Connecting to database...")
    await init_pool(DATABASE_URL)
    logging.info("Connecting to NATS...")
    await nats_connect(NATS_URL)
    logging.info("Stripe Integration Microservice started")
    yield
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
