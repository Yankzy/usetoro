import asyncio
import json
import logging
import os
import nats
from nats.errors import TimeoutError
from django.conf import settings
from django.core.management.base import BaseCommand
from asgiref.sync import sync_to_async
from pydantic import ValidationError
from webhookks.services import SchemaValidationService
from webhookks.models import Connection, Event

logger = logging.getLogger(__name__)

class Command(BaseCommand):
    help = 'Consumes webhook events from NATS Jetstream and processes them'

    def handle(self, *args, **options):
        try:
            asyncio.run(self.run_consumer())
        except KeyboardInterrupt:
            self.stdout.write(self.style.SUCCESS("Consumer stopped by user"))
        except Exception as e:
            self.stdout.write(self.style.ERROR(f"Consumer crashed: {e}"))

    async def run_consumer(self):
        # 1. Connect to NATS Cluster
        nats_urls = os.getenv("NATS_URL", "nats://localhost:4222").split(",")
        logger.info(f"Connecting to NATS: {nats_urls}")
        
        nc = await nats.connect(servers=nats_urls)
        js = nc.jetstream()

        # 2. Ensure Consumer Exists (Idempotent)
        stream_name = "TORO_INGEST"
        durable_name = "django_processor"
        subject_filter = "toro.ingest.>"

        try:
            # We assume stream 'TORO_INGEST' is created by Go producer.
            # If not, add_consumer might fail with Stream Not Found.
            await js.add_consumer(stream_name, {
                "durable_name": durable_name,
                "deliver_policy": "all",
                "ack_policy": "explicit",
                "filter_subject": subject_filter,
                "replay_policy": "instant",
            })
            logger.info(f"✅ Consumer '{durable_name}' ready on '{stream_name}'")
        except Exception as e:
            # It might fail if stream doesn't exist yet (Go app hasn't started)
            # We'll retry or proceed and fail at subscribe
            logger.warning(f"⚠️ Consumer check failed (Stream might not exist yet): {e}")

        # 3. Pull Subscription
        # We use pull_subscribe for worker scaling control
        psub = await js.pull_subscribe(subject_filter, durable_name)
        logger.info(f"🚀 Started Webhook Consumer (NATS) for {subject_filter}")

        while True:
            try:
                # Fetch batch of 10 messages, wait up to 2s
                msgs = await psub.fetch(10, timeout=2)
                
                for msg in msgs:
                    try:
                        await self.process_message(msg)
                        await msg.ack()
                    except Exception as e:
                        logger.error(f"❌ Failed to process message {msg.subject}: {e}")
                        # If we don't Ack, NATS will redeliver after AckWait (default 30s)
                        # We could also msg.nak() to trigger immediate redelivery with backoff 
                        # but let's stick to simple Ack-on-success.
                        
            except TimeoutError:
                # No messages in queue, loop again
                continue
            except Exception as e:
                logger.error(f"Consumer loop critical error: {e}")
                await asyncio.sleep(2) # Prevent busy loop on network errors

    async def process_message(self, msg):
        data_json = msg.data.decode()
        try:
            data = json.loads(data_json)
        except json.JSONDecodeError:
            logger.error(f"Invalid JSON in message {msg.subject}")
            return # Ack (skip) invalid JSON

        # Extract Metadata
        # Headers preferred, fallback to payload keys
        connection_id = msg.header.get("Connection-ID") or data.get("connection_id")
        source = msg.header.get("Source") or data.get("source") or "unknown"
        event_uuid = msg.header.get("Event-ID") or data.get("id") or "unknown"
        
        raw_body = data.get("body")
        headers_payload = data.get("headers")

        logger.info(f"📥 Processing Event [{source}] ID: {event_uuid}")

        # Run DB Logic in Sync Thread
        await self.process_db_logic(connection_id, source, event_uuid, raw_body, headers_payload)

    @sync_to_async
    def process_db_logic(self, connection_id, source, event_uuid, raw_body, headers_payload):
        # 1. Validate Connection
        connection = None
        if connection_id:
            try:
                connection = Connection.objects.get(id=connection_id)
            except Connection.DoesNotExist:
                logger.error(f"❌ Connection {connection_id} not found for event {event_uuid}")
                # We return (Ack) because retrying won't fix 'Not Found'
                return
            except Exception as e:
                logger.error(f"DB Error fetching connection: {e}")
                raise e # Raise to trigger Redelivery (No Ack)
        else:
             logger.warning(f"No connection_id for event {event_uuid}")
             return

        # 2. Persist Raw Event
        try:
            event = Event.objects.create(
                id=event_uuid,
                connection=connection,
                source=source,
                payload=raw_body,
                headers=headers_payload,
                status='raw'
            )
            logger.info(f"💾 Persisted raw event {event.id}")
        except Exception as e:
            logger.error(f"❌ Failed to persist raw event: {e}")
            raise e # Retry

        # 3. Dynamic Schema Validation
        try:
            # Handle different body types (str/bytes vs dict)
            body_data = raw_body
            if isinstance(body_data, (bytes, str)):
                try:
                    body_data = json.loads(body_data)
                except:
                    pass 
            
            if isinstance(body_data, dict):
                SchemaValidationService.validate_payload(source, body_data)
                event.status = 'validated'
                event.save()
                logger.info(f"✅ Schema Validated for {source}")
            else:
                logger.warning(f"Skipping validation for non-dict body: {type(body_data)}")

        except ValidationError as e:
            logger.error(f"❌ Schema Validation Failed for {source}: {e.json()}")
            event.status = 'validation_failed'
            event.save()
        except Exception as e:
             logger.error(f"Error during validation logic: {e}")
             # We saved raw event, so we might not want to retry indefinitely if it's a logic bug.
             # But for DB errors we should.
