import os
import django
import sys

# Add project root to path
sys.path.append('/Users/Yankz/programming/usetoro')

os.environ.setdefault('DJANGO_SETTINGS_MODULE', 'config.settings')
django.setup()

import logging
logging.basicConfig(level=logging.INFO)

from django.conf import settings
import redis
import uuid
import json
import time
from webhookks.models import Connection, Event
from django.contrib.auth import get_user_model

User = get_user_model()

def run_verification():
    print("🚀 Starting Webhook v1 Verification")
    
    # 1. Setup Data
    user, _ = User.objects.get_or_create(email='verify@bot.com', defaults={'handle': 'verify_bot'})
    conn, _ = Connection.objects.get_or_create(
        user=user, 
        source_type='test_source', 
        defaults={'secret': 'whsec_test'}
    )
    print(f"✅ Setup Connection: {conn.id}")
    
    # 2. Insert into Redis (Simulating Go)
    r = redis.Redis.from_url(settings.REDIS_URL, decode_responses=True)
    stream_key = 'toro:ingest'
    
    event_id = str(uuid.uuid4())
    payload_data = {
        "id": event_id,
        "source": "test_source",
        "body": {"amount": 100, "currency": "usd"},
        "headers": {"Content-Type": "application/json"},
        "timestamp": time.time()
    }
    
    r.xadd(stream_key, {
        "payload": json.dumps(payload_data),
        "source": "test_source",
        "connection_id": str(conn.id)
    })
    print(f"✅ Simulating Go Ingest: Pushed event {event_id} to Redis")
    
    # 3. Trigger Consumer (Run one loop or mocked)
    # Ideally we run the management command in a separate process or thread, 
    # but here we can just import the Command class and run it for one iteration if we hacked it, 
    # or better: we just check if it appears in DB after we manually run the consumer in terminal?
    # No, let's try to run the logic directly or ask user to run consumer.
    # Actually, I can simulate the consumer processing logic here directly to verify the CODE changes work.
    
    from webhookks.management.commands.run_consumer import Command
    cmd = Command()
    
    # Fake the redis results to avoid blocking
    # Actually, we can just call process_message directly if we want to test the method logic
    # But checking the XREAD part is important. 
    
    print("⏳ Running Consumer Logic (Single Pass)...")
    # We will manually read from stream to verify we can pop it
    group_name = 'test_group_verifier'
    try:
        r.xgroup_create(stream_key, group_name, id='0', mkstream=True)
    except:
        pass
        
    entries = r.xreadgroup(group_name, 'worker_1', {stream_key: '>'}, count=10)
    
    if entries:
        for stream, messages in entries:
            for message_id, data in messages:
                print(f"✅ Consumer read message: {message_id}")
                cmd.process_message(r, stream_key, group_name, message_id, data)
    else:
        print("❌ Consumer failed to read ANY message from Redis")


    # 4. Verify DB Persistence
    try:
        event = Event.objects.get(id=event_id)
        print(f"✅ VERIFIED: Event persisted in DB!")
        print(f"   - Status: {event.status}")
        print(f"   - Body: {event.payload}")
        print(f"   - Connection: {event.connection.id}")
    except Event.DoesNotExist:
        print(f"❌ FAILED: Event not found in DB")
    
if __name__ == '__main__':
    run_verification()
