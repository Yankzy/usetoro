import os
import django
import redis
import json
import logging

import sys
from pathlib import Path

# Add project root to sys.path
PROJ_ROOT = Path(__file__).resolve().parent.parent
sys.path.append(str(PROJ_ROOT))

# Setup Django Environment
os.environ.setdefault('DJANGO_SETTINGS_MODULE', 'config.settings')
django.setup()

from webhookks.models import WebhookSchema
from django.conf import settings

logging.basicConfig(level=logging.INFO)
logger = logging.getLogger(__name__)

def seed_and_test():
    # 1. Create a Schema in DB
    source = "stripe_charge_succeeded"
    schema_def = {
        "id": {"type": "str", "required": True},
        "amount": {"type": "int", "required": True},
        "currency": {"type": "str", "required": True}
    }
    
    schema, created = WebhookSchema.objects.update_or_create(
        source=source,
        defaults={"schema_definition": schema_def, "version": 1}
    )
    logger.info(f"✅ Schema prepared for {source} (Created: {created})")

    # 2. Push a VALID event to Redis
    r = redis.Redis.from_url(settings.REDIS_URL, decode_responses=True)
    stream_key = 'toro:ingest'
    
    valid_payload = {
        "id": "evt_test_123",
        "amount": 5000,
        "currency": "usd"
    }
    
    # Simulate internal structure where source is separate or inside payload
    # Consumer logic checks data.get('source') first
    message_data = {
        "source": source,
        "payload": json.dumps(valid_payload)
    }
    
    msg_id = r.xadd(stream_key, message_data)
    logger.info(f"🚀 Pushed VALID event to Redis Stream: {msg_id}")

    # 3. Push an INVALID event to Redis
    invalid_payload = {
        "id": "evt_invalid",
        "currency": "usd"
        # Missing 'amount'
    }
    
    message_data_invalid = {
        "source": source,
        "payload": json.dumps(invalid_payload)
    }
    
    msg_id_invalid = r.xadd(stream_key, message_data_invalid)
    logger.info(f"🚀 Pushed INVALID event to Redis Stream: {msg_id_invalid}")

if __name__ == "__main__":
    seed_and_test()
