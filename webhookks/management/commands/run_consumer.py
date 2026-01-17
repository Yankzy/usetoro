import logging
import time
import json
import redis
from django.conf import settings
from django.core.management.base import BaseCommand
from pydantic import ValidationError
from webhookks.services import SchemaValidationService

logger = logging.getLogger(__name__)

class Command(BaseCommand):
    help = 'Consumes webhook events from Redis Stream and processes them'

    def handle(self, *args, **options):
        # Connect to Redis
        r = redis.Redis.from_url(settings.REDIS_URL, decode_responses=True)
        stream_key = 'toro:ingest'
        group_name = 'django_processor_group'
        consumer_name = 'worker_1' # In prod, this could be hostname + pid

        # 1. Create Consumer Group (if not exists)
        try:
            r.xgroup_create(stream_key, group_name, id='0', mkstream=True)
            logger.info(f"Created consumer group: {group_name}")
        except redis.exceptions.ResponseError as e:
            if "BUSYGROUP" in str(e):
                logger.info(f"Consumer group {group_name} already exists.")
            else:
                raise e

        logger.info(f"🚀 Started Webhook Consumer: {consumer_name}")

        while True:
            try:
                # 2. Read from Stream
                # BLOCK=2000 means wait 2 seconds for new messages
                # > means read messages never delivered to this consumer
                streams = {stream_key: '>'}
                results = r.xreadgroup(group_name, consumer_name, streams, count=10, block=2000)

                if not results:
                    continue

                for stream, messages in results:
                    for message_id, data in messages:
                        self.process_message(r, stream_key, group_name, message_id, data)

            except Exception as e:
                logger.error(f"Consumer Error: {e}")
                time.sleep(1) # Prevent tight loop on error

    def process_message(self, r, stream_key, group_name, message_id, data):
        try:
            payload_json = data.get('payload')
            if not payload_json:
                logger.warning(f"Missing payload in message {message_id}")
                r.xack(stream_key, group_name, message_id)
                return

            payload = json.loads(payload_json)
            source = payload.get('source', 'unknown') # Assuming source is inside payload, or passed as metadata
            # Note: In real production, source usually comes from URL path or separate metadata field in Redis 
            # But based on the previous simple consumer, we extract it.
            # If extracting from payload is unreliable, we should look at 'source' key in Redis data if we stored it there.
            # Checking previous file content...
            # The Go example showed: "source": source, "payload": body.
            # So 'source' might be a top-level field in Redis data, NOT inside the JSON body.
            
            # Let's check 'data' dictionary from Redis.
            redis_source = data.get('source')
            if redis_source:
                 source = redis_source
            
            event_id = payload.get('id', 'unknown')

            logger.info(f"📥 Processing Event [{source}] ID: {event_id}")

            # --- DYNAMIC SCHEMA VALIDATION ---
            try:
                validated_data = SchemaValidationService.validate_payload(source, payload)
                logger.info(f"✅ Schema Validated for {source}")
                
                # TODO: Save to Postgres (Implementation pending)
                
            except ValidationError as e:
                logger.error(f"❌ Schema Validation Failed for {source}: {e.json()}")
                # TODO: Send to AI Repair Queue
                # For now, we ack it so we don't loop forever, but log it as error.
            
            # ---------------------------------

            # Ack the message so it's not redelivered
            r.xack(stream_key, group_name, message_id)
            logger.info(f"✅ Acked Event {event_id}")

        except json.JSONDecodeError:
            logger.error(f"Failed to decode JSON for message {message_id}")
            r.xack(stream_key, group_name, message_id) # Ack bad data to skip it
        except Exception as e:
            logger.error(f"Error processing message {message_id}: {e}")
            # Do NOT Ack here, so it can be retried or moved to DLQ later
