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
            source = data.get('source', 'unknown')
            connection_id = data.get('connection_id')

            if not payload_json:
                logger.warning(f"Missing payload in message {message_id}")
                r.xack(stream_key, group_name, message_id)
                return

            payload = json.loads(payload_json)
            # If source wasn't top-level in Redis, try payload (fallback)
            if source == 'unknown':
                source = payload.get('source', 'unknown')
            
            event_uuid = payload.get('id', 'unknown')

            logger.info(f"📥 Processing Event [{source}] ID: {event_uuid}")

            # 1. PERSIST RAW EVENT
            connection = None
            if connection_id:
                try:
                    from webhookks.models import Connection, Event
                    connection = Connection.objects.get(id=connection_id)
                except Connection.DoesNotExist:
                    logger.error(f"❌ Connection {connection_id} not found for event {event_uuid}")
                    # Decide: Ack and drop? Or keep for manual inspection? 
                    # For now, drop/ack to avoid blocking.
                    r.xack(stream_key, group_name, message_id)
                    return
                except Exception as e:
                    logger.error(f"Error fetching connection: {e}")
                    # DB error, don't ack, let it retry
                    return
            else:
                 logger.warning(f"No connection_id provided for event {event_uuid}")
                 # For v1 strictness, maybe drop. For dev, we might tolerate.
                 # Let's drop if invalid.
                 r.xack(stream_key, group_name, message_id)
                 return

            # Save Raw Event
            try:
                event = Event.objects.create(
                    id=event_uuid, # Use the UUID from Go if possible, or let Django gen valid one if format differs. 
                    # Go generates standard UUID string. Django UUIDField expects UUID object or valid string.
                    connection=connection,
                    source=source,
                    payload=payload.get('body'), # Go 'WebhookPayload' has 'Body' (original payload)
                    headers=payload.get('headers'),
                    status='raw'
                )
                logger.info(f"💾 Persisted raw event {event.id}")
            except Exception as e:
                logger.error(f"❌ Failed to persist raw event: {e}")
                # Don't Ack, retry
                return

            # 2. DYNAMIC SCHEMA VALIDATION
            try:
                # payload['body'] is expected to be a dict if json, or we need to handle non-json body?
                # Go sends json.RawMessage which unmarshals to whatever it is. 
                # If it's a JSON webhook, 'body' in 'payload' dict is the data.
                body_data = payload.get('body')
                if isinstance(body_data, (bytes, str)):
                     # If it came as string/bytes, try to parse
                     try:
                        body_data = json.loads(body_data)
                     except:
                        pass # Keep as is if not json
                
                if isinstance(body_data, dict):
                    validated_data = SchemaValidationService.validate_payload(source, body_data)
                    event.status = 'validated'
                    event.save()
                    logger.info(f"✅ Schema Validated for {source}")
                else:
                    logger.warning(f"Skipping validation for non-dict body: {type(body_data)}")
                    
            except ValidationError as e:
                logger.error(f"❌ Schema Validation Failed for {source}: {e.json()}")
                event.status = 'validation_failed'
                event.save()
                # We still Ack because we saved the raw event and marked it failed.
            
            # 3. Ack the message from Stream
            r.xack(stream_key, group_name, message_id)
            logger.info(f"✅ Acked Event {event_uuid}")

        except json.JSONDecodeError:
            logger.error(f"Failed to decode JSON for message {message_id}")
            r.xack(stream_key, group_name, message_id) 
        except Exception as e:
            logger.error(f"Error processing message {message_id}: {e}")
            # Do NOT Ack here for general errors
