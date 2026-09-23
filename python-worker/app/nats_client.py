import asyncio
import json
import logging

import nats
from nats.aio.client import Client as NATS

from app.errors import NATSPublishError

_nc: NATS | None = None
logger = logging.getLogger(__name__)


def get_nc() -> NATS | None:
    global _nc
    return _nc


async def connect(nats_url: str, max_retries: int = 10, retry_delay: float = 2.0) -> None:
    global _nc
    servers = nats_url.split(",")
    for attempt in range(1, max_retries + 1):
        try:
            logger.info(f"Connecting to NATS (attempt {attempt}/{max_retries})...")
            _nc = await nats.connect(
                servers=servers,
                name="stripe-worker",
                connect_timeout=5,
                max_reconnect_attempts=60,
                reconnect_time_wait=2,
            )
            logger.info("Successfully connected to NATS")
            return
        except Exception as e:
            if attempt == max_retries:
                logger.error(f"Failed to connect to NATS after {max_retries} attempts: {e}")
                raise
            logger.warning(f"NATS connection pending ({e}), retrying in {retry_delay}s...")
            await asyncio.sleep(retry_delay)


async def close() -> None:
    global _nc
    if _nc:
        await _nc.drain()


async def publish(subject: str, payload: dict) -> None:
    global _nc
    if _nc is None:
        raise NATSPublishError("NATS not connected")
    data = json.dumps(payload).encode()
    try:
        await _nc.publish(subject, data)
    except Exception as e:
        logger.error("Failed to publish to NATS", extra={"subject": subject, "error": str(e)})
        raise NATSPublishError(f"Failed to publish to {subject}: {e}") from e

async def subscribe(subject: str, callback) -> None:
    global _nc
    if _nc is None:
        raise NATSPublishError("NATS not connected")
    try:
        await _nc.subscribe(subject, cb=callback)
        logger.info(f"Subscribed to core NATS subject {subject}")
    except Exception as e:
        logger.error("Failed to subscribe to NATS subject", extra={"subject": subject, "error": str(e)})
        raise e

async def subscribe_jetstream(subject: str, durable_name: str, callback, max_retries: int = 5, retry_delay: float = 2.0) -> None:
    global _nc
    if _nc is None:
        raise NATSPublishError("NATS not connected")
    
    js = _nc.jetstream()
    for attempt in range(1, max_retries + 1):
        try:
            await js.subscribe(
                subject,
                durable=durable_name,
                cb=callback,
                manual_ack=True
            )
            logger.info(f"Subscribed to JetStream subject {subject} with durable {durable_name}")
            return
        except Exception as e:
            if attempt == max_retries:
                logger.error("Failed to subscribe to JetStream", extra={"subject": subject, "error": str(e)})
                raise e
            logger.warning(f"JetStream subscription to {subject} pending ({e}), retrying in {retry_delay}s...")
            await asyncio.sleep(retry_delay)

