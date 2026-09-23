"""
Django management command to run the Python Accounting Intake NATS JetStream worker.

Usage:
    python manage.py run_accounting_intake_worker
"""

from __future__ import annotations

import asyncio
import json
import logging
import os
import signal
import sys
from typing import Any

from django.core.management.base import BaseCommand
import nats
from nats.aio.client import Client as NATS
from nats.js.api import ConsumerConfig, DeliverPolicy

from ledger.bookkeeping_state.intake.service import AccountingIntakeService

logger = logging.getLogger("accounting_intake_worker")
logging.basicConfig(level=logging.INFO, format="%(asctime)s [%(levelname)s] %(name)s: %(message)s")


class Command(BaseCommand):
    help = "Runs the NATS JetStream worker for processing Go OCR output into Django accounting authority."

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
            default="worker.inbox.python.accounting_intake",
            help="NATS subject to subscribe to",
        )
        parser.add_argument(
            "--queue-group",
            dest="queue_group",
            default="accounting_intake_group",
            help="Durable queue group name",
        )

    def handle(self, *args, **options):
        nats_url = options["nats_url"]
        subject = options["subject"]
        queue_group = options["queue_group"]

        self.stdout.write(f"Starting Accounting Intake Worker on subject: {subject} (NATS: {nats_url})")

        try:
            asyncio.run(self._run_worker(nats_url, subject, queue_group))
        except (KeyboardInterrupt, SystemExit):
            self.stdout.write("Accounting Intake Worker stopped.")

    async def _run_worker(self, nats_url: str, subject: str, queue_group: str) -> None:
        servers = [s.strip() for s in nats_url.split(",") if s.strip()]
        nc = await nats.connect(servers=servers, name="django-accounting-intake-worker")
        js = nc.jetstream()

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

            try:
                result = await asyncio.to_thread(AccountingIntakeService.process_event, data)
                logger.info("Processed accounting intake event", extra={"result": result, "doc_id": data.get("document_id")})
                await msg.ack()
            except Exception as e:
                logger.error("Error executing accounting intake service", extra={"error": str(e), "doc_id": data.get("document_id")})
                await msg.nak()

        logger.info("Subscribing to %s with queue group %s...", subject, queue_group)
        sub = await js.subscribe(
            subject=subject,
            queue=queue_group,
            durable=queue_group,
            cb=msg_handler,
            manual_ack=True,
        )

        logger.info("Accounting Intake Worker is active and listening for events.")
        await shutdown_event.wait()

        logger.info("Draining subscription and closing connection...")
        await sub.unsubscribe()
        await nc.drain()
        logger.info("Worker cleanly stopped.")
