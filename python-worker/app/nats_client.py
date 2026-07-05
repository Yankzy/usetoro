import json
import logging

import nats
from nats.aio.client import Client as NATS

from app.errors import NATSPublishError

_nc: NATS | None = None
logger = logging.getLogger(__name__)


async def connect(nats_url: str) -> None:
    global _nc
    servers = nats_url.split(",")
    _nc = await nats.connect(servers=servers, name="stripe-worker")


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

async def subscribe_jetstream(subject: str, durable_name: str, callback) -> None:
    global _nc
    if _nc is None:
        raise NATSPublishError("NATS not connected")
    
    js = _nc.jetstream()
    try:
        await js.subscribe(
            subject,
            durable=durable_name,
            cb=callback,
            manual_ack=True
        )
        logger.info(f"Subscribed to JetStream subject {subject} with durable {durable_name}")
    except Exception as e:
        logger.error("Failed to subscribe to JetStream", extra={"subject": subject, "error": str(e)})
        raise e
