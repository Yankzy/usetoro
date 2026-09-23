"""
Django management command to run the BookkeepingSession NATS JetStream runner worker.

Usage:
    python manage.py run_bookkeeping_session_worker
"""

from __future__ import annotations

import asyncio
import json
import logging
import os
import signal
from typing import Any

from django.core.management.base import BaseCommand
import nats
from nats.aio.client import Client as NATS
import nats.js.errors

from ledger.bookkeeping_state.session.runner import process_entity_triggers

logger = logging.getLogger("bookkeeping_session_worker")
logging.basicConfig(level=logging.INFO, format="%(asctime)s [%(levelname)s] %(name)s: %(message)s")


class Command(BaseCommand):
    help = "Runs the NATS JetStream worker for executing canonical BookkeepingSessions upon intake triggers."

    def add_arguments(self, parser):
        parser.add_argument(
            "--nats-url",
            dest="nats_url",
            default=os.getenv("NATS_URL", "nats://127.0.0.1:4222"),
            help="NATS server URL(s) comma-separated",
        )
        parser.add_argument(
            "--subject",
            dest="subject",
            default="events.bookkeeping.trigger",
            help="NATS subject to subscribe to",
        )
        parser.add_argument(
            "--queue-group",
            dest="queue_group",
            default="bookkeeping_session_runner_group",
            help="Durable queue group name",
        )

    def handle(self, *args, **options):
        logging.basicConfig(level=logging.INFO)
        nats_url = options["nats_url"]
        subject = options["subject"]
        queue_group = options["queue_group"]

        self.stdout.write(f"Starting BookkeepingSession Worker on subject: {subject} (NATS: {nats_url})")

        try:
            asyncio.run(self._run_worker(nats_url, subject, queue_group))
        except (KeyboardInterrupt, SystemExit):
            self.stdout.write("BookkeepingSession Worker stopped.")

    async def _run_worker(self, nats_url: str, subject: str, queue_group: str) -> None:
        servers = [s.strip() for s in nats_url.split(",") if s.strip()]

        # Connect with retry resilience
        nc = None
        for attempt in range(1, 16):
            try:
                nc = await nats.connect(
                    servers=servers,
                    name="django-bookkeeping-session-worker",
                    connect_timeout=5,
                    max_reconnect_attempts=60,
                    reconnect_time_wait=2,
                )
                logger.info("Connected to NATS servers: %s", servers)
                break
            except Exception as e:
                if attempt == 15:
                    logger.error("Failed to connect to NATS after 15 attempts: %s", e)
                    raise
                logger.warning("NATS connection pending (%s), retrying in 2s...", e)
                await asyncio.sleep(2)

        if not nc:
            raise RuntimeError("Failed to obtain NATS connection")

        js = nc.jetstream()
        jsm = nc.jsm()

        shutdown_event = asyncio.Event()

        def _signal_handler():
            logger.info("Received shutdown signal, draining NATS connection...")
            shutdown_event.set()

        loop = asyncio.get_running_loop()
        for sig in (signal.SIGINT, signal.SIGTERM):
            try:
                loop.add_signal_handler(sig, _signal_handler)
            except NotImplementedError:
                pass

        async def msg_handler(msg):
            meta = None
            try:
                meta = await msg.metadata()
                if meta and meta.num_delivered > 3:
                    logger.error("Poison pill detected on subject %s, terminating message", msg.subject)
                    await msg.term()
                    return
            except Exception:
                pass

            try:
                data = json.loads(msg.data.decode("utf-8"))
            except Exception as e:
                logger.error("Failed to parse JSON payload", extra={"error": str(e)})
                await msg.term()
                return

            entity_id = data.get("entity_id")
            if not entity_id:
                logger.error("Trigger payload missing entity_id, terminating", extra={"data": data})
                await msg.term()
                return

            try:
                results = await asyncio.to_thread(process_entity_triggers, entity_id=str(entity_id))
                logger.info("Executed coalesced session runs for entity %s", entity_id, extra={"runs_count": len(results)})
                await msg.ack()
            except Exception as e:
                logger.error("Error executing bookkeeping session worker for entity %s", entity_id, extra={"error": str(e)})
                await msg.nak()

        # Ensure JetStream stream exists and subscribe with retries
        sub = None
        for attempt in range(1, 16):
            try:
                # 1. Ensure stream exists covering this subject
                try:
                    stream_name = await jsm.find_stream_name_by_subject(subject)
                    logger.info("Found stream '%s' covering subject '%s'", stream_name, subject)
                except nats.js.errors.NotFoundError:
                    stream_name = "BOOKKEEPING_EVENTS"
                    # events.bookkeeping.> encompasses events.bookkeeping.trigger and all sub-subjects.
                    # Listing both causes NATS ServerError 10052 (overlapping subjects).
                    stream_subjects = ["events.bookkeeping.>"]
                    if not subject.startswith("events.bookkeeping."):
                        stream_subjects.append(subject)
                    try:
                        await jsm.add_stream(
                            name=stream_name,
                            subjects=stream_subjects,
                        )
                        logger.info("Created JetStream stream '%s' for subjects %s", stream_name, stream_subjects)
                    except Exception as e:
                        logger.warning("Stream creation for %s: %s", stream_name, e)

                logger.info("Subscribing to %s with queue group %s (attempt %d/15)...", subject, queue_group, attempt)
                sub = await js.subscribe(
                    subject=subject,
                    queue=queue_group,
                    durable=queue_group,
                    cb=msg_handler,
                    manual_ack=True,
                )
                break
            except Exception as e:
                if attempt == 15:
                    logger.error("Failed to subscribe to %s after 15 attempts: %s", subject, e)
                    raise
                logger.warning("Subscription to %s pending (%s), retrying in 2s...", subject, e)
                await asyncio.sleep(2)

        logger.info("BookkeepingSession Worker is active and listening for events.")
        await shutdown_event.wait()

        logger.info("Draining subscription and closing connection...")
        if sub:
            await sub.unsubscribe()
        await nc.drain()
        logger.info("BookkeepingSession Worker cleanly stopped.")
