"""
NATS-Svix Bridge Consumer

This management command implements the atomic handoff between NATS JetStream (The Vault)
and Svix (The Cannon). It ensures banking-grade reliability with durable consumption
and only ACKs NATS after Svix confirms receipt.

Architecture:
- NATS JetStream: Source of truth with file-based persistence
- Svix: Delivery mechanism with isolated Redis queue
- Bridge: Ensures atomic handoff with failure recovery

Usage:
    python manage.py run_webhook_bridge
"""

import asyncio
import json
import logging
import signal
import sys
from typing import Optional

from django.core.management.base import BaseCommand
from django.conf import settings
from django.core.cache import cache  # Redis cache for idempotency

import nats
from nats.js import JetStreamContext
from nats.js.api import ConsumerConfig, DeliverPolicy, AckPolicy

from webhookks.utils import dispatch_webhook

logger = logging.getLogger(__name__)


class WebhookBridge:
    """
    The Bridge between NATS JetStream (Vault) and Svix (Cannon).
    
    Implements durable consumption with atomic handoff guarantees.
    """
    
    def __init__(self):
        self.nc: Optional[nats.NATS] = None
        self.js: Optional[JetStreamContext] = None
        self.running = False
        self.consumer_name = "webhook-bridge-consumer"
        self.stream_name = "TORO_INGEST"
        self.subject_filter = "toro.ingest.>"
        
    async def connect(self):
        """Connect to NATS JetStream cluster"""
        logger.info("🔌 Connecting to NATS cluster...")
        
        # Parse NATS URLs from settings
        nats_urls = settings.NATS_URL.split(',')
        logger.info(f"   NATS URLs: {nats_urls}")
        
        # Connect with reconnect options for resilience
        self.nc = await nats.connect(
            servers=nats_urls,
            reconnect_time_wait=2,
            max_reconnect_attempts=-1,  # Infinite reconnects
            name="webhook-bridge"
        )
        
        logger.info("✅ Connected to NATS")
        
        # Get JetStream context
        self.js = self.nc.jetstream()
        logger.info("✅ JetStream context acquired")
        
    async def ensure_stream(self):
        """Ensure the TORO_INGEST stream exists with proper configuration"""
        logger.info(f"🔍 Checking stream: {self.stream_name}")
        
        try:
            stream_info = await self.js.stream_info(self.stream_name)
            logger.info(f"✅ Stream exists: {self.stream_name}")
            logger.info(f"   Storage: {stream_info.config.storage}")
            logger.info(f"   Subjects: {stream_info.config.subjects}")
            logger.info(f"   Messages: {stream_info.state.messages}")
        except Exception as e:
            logger.warning(f"⚠️  Stream not found, will be created by producer: {e}")
    
    async def create_durable_consumer(self):
        """
        Create a durable consumer for reliable message processing.
        
        The consumer is durable, meaning it remembers its position even if
        the bridge crashes and restarts.
        """
        logger.info(f"🔧 Setting up durable consumer: {self.consumer_name}")
        
        consumer_config = ConsumerConfig(
            durable_name=self.consumer_name,
            deliver_policy=DeliverPolicy.ALL,  # Start from beginning if new
            ack_policy=AckPolicy.EXPLICIT,     # Manual ACK required
            ack_wait=30,                       # 30 seconds to ACK before redelivery
            max_deliver=5,                     # ⭐ DLQ: Max 5 delivery attempts (poison pill protection)
            filter_subject=self.subject_filter
        )
        
        try:
            await self.js.add_consumer(self.stream_name, consumer_config)
            logger.info(f"✅ Durable consumer created: {self.consumer_name}")
        except Exception as e:
            # Consumer might already exist
            logger.info(f"✅ Using existing consumer: {self.consumer_name}")
    
    async def process_message(self, msg):
        """
        Process a single message with atomic handoff guarantees + idempotency.
        
        Flow:
        1. Extract provider_event_id from payload
        2. Check Redis idempotency guard (dedup)
        3. If duplicate → ACK immediately and skip
        4. Parse message from NATS
        5. Send to Svix
        6. Set Redis idempotency key (24hr TTL)
        7. Only ACK NATS if Svix returns success
        
        If Svix is down or fails, DO NOT ACK. NATS will redeliver later.
        """
        try:
            # Parse the message payload
            data = json.loads(msg.data.decode())
            
            # Extract metadata from NATS headers
            headers = msg.headers or {}
            connection_id = headers.get('Connection-ID', ['unknown'])[0] if headers.get('Connection-ID') else 'unknown'
            event_id = headers.get('Event-ID', ['unknown'])[0] if headers.get('Event-ID') else 'unknown'
            source = headers.get('Source', ['unknown'])[0] if headers.get('Source') else 'unknown'
            user_id = headers.get('User-ID', ['unknown'])[0] if headers.get('User-ID') else 'unknown'
            
            # === IDEMPOTENCY GUARD (CRITICAL FOR AT-LEAST-ONCE DELIVERY) ===
            # Extract provider's native event ID (e.g., evt_xxx for Stripe)
            provider_event_id = self._extract_provider_event_id(data, source)
            
            if provider_event_id:
                dedup_key = f"processed_event:{source}:{provider_event_id}"
                
                # Check if we've already processed this event
                if cache.get(dedup_key):
                    logger.warning(
                        f"🔁 DUPLICATE DETECTED | "
                        f"source={source} | "
                        f"provider_event_id={provider_event_id} | "
                        f"event_id={event_id} | "
                        f"ACTION: ACKing without processing"
                    )
                    # ACK immediately to remove from NATS (already processed)
                    await msg.ack()
                    return
            else:
                logger.warning(
                    f"⚠️  Could not extract provider_event_id | "
                    f"source={source} | event_id={event_id}"
                )
            # === END IDEMPOTENCY CHECK ===
            
            logger.info(
                f"📨 Processing message | "
                f"subject={msg.subject} | "
                f"source={source} | "
                f"event_id={event_id} | "
                f"user_id={user_id} | "
                f"connection_id={connection_id}"
            )
            
            # Construct event type from subject
            event_type = self._construct_event_type(msg.subject, source)
            
            # THE CRITICAL HANDOFF: Send to Svix
            result = dispatch_webhook(
                user_id=user_id,
                event_type=event_type,
                payload=data.get('body', data)
            )
            
            # Check if Svix accepted the webhook
            if result.get('success'):
                logger.info(
                    f"✅ Svix accepted | "
                    f"message_id={result.get('message_id')} | "
                    f"event_id={event_id}"
                )
                
                # === SET IDEMPOTENCY KEY (24 HOUR TTL) ===
                if provider_event_id:
                    cache.set(dedup_key, True, timeout=86400)  # 24 hours
                    logger.info(f"🔒 Dedup key set: {dedup_key}")
                # === END IDEMPOTENCY SET ===
                
                # CRITICAL: Only ACK NATS after Svix confirms
                await msg.ack()
                logger.info(f"✅ NATS ACK | event_id={event_id}")
                
            else:
                # Svix failed - DO NOT ACK
                logger.error(
                    f"❌ Svix rejected | "
                    f"error={result.get('error')} | "
                    f"event_id={event_id} | "
                    f"ACTION: Will NOT ACK, NATS will redeliver"
                )
                # Do not call msg.ack() - let NATS redeliver
                
        except json.JSONDecodeError as e:
            logger.error(f"❌ Invalid JSON in message: {e}")
            # Bad data - ACK to avoid infinite loop (this is a permanent error)
            await msg.ack()
            # TODO: Publish to dead.letter subject
            
        except Exception as e:
            logger.error(f"❌ Unexpected error processing message: {e}", exc_info=True)
            # Don't ACK - let NATS redeliver (might be transient error)
    
    def _extract_provider_event_id(self, data: dict, source: str) -> Optional[str]:
        """
        Extract the provider's native event ID for idempotency.
        
        Examples:
        - Stripe: evt_1ABC123...
        - Plaid: plaid_event_123...
        - QuickBooks: qb_evt_456...
        """
        if source == 'stripe':
            # Stripe events have an 'id' field at the root
            return data.get('id')
        elif source == 'plaid':
            # Plaid notifications have a 'webhook_code' or 'item_id'
            return data.get('webhook_code') or data.get('item_id')
        elif source == 'quickbooks':
            # QuickBooks has eventNotifications with realmId + entities
            notifications = data.get('eventNotifications', [])
            if notifications:
                return notifications[0].get('realmId')
        
        # Fallback: try common patterns
        return data.get('id') or data.get('event_id') or data.get('eventId')
    
    def _extract_user_id(self, connection_id: str, data: dict) -> str:
        """
        Extract user_id from connection_id or message data.
        
        Implement your business logic here.
        """
        # Example: connection_id might be "user_{user_id}"
        if connection_id.startswith('user_'):
            return connection_id
        
        # Fallback: check data
        if 'user_id' in data:
            return str(data['user_id'])
        
        # Default fallback
        return "system"
    
    def _construct_event_type(self, subject: str, source: str) -> str:
        """
        Construct event type from NATS subject and source.
        
        Example: 
        - subject: "toro.ingest.stripe"
        - source: "stripe"
        - returns: "stripe.webhook"
        """
        # Extract the last part of the subject
        parts = subject.split('.')
        if len(parts) >= 3:
            provider = parts[2]
            return f"{provider}.webhook"
        
        return f"{source}.webhook"
    
    async def start_consuming(self):
        """Start consuming messages from NATS with durable subscription"""
        logger.info("🚀 Starting webhook bridge consumer...")
        
        self.running = True
        
        # Create pull subscription (more reliable than push)
        psub = await self.js.pull_subscribe(
            self.subject_filter,
            durable=self.consumer_name,
            stream=self.stream_name
        )
        
        logger.info(f"✅ Subscribed to: {self.subject_filter}")
        logger.info("🔄 Bridge is now running. Press Ctrl+C to stop.")
        
        # Main consumption loop
        while self.running:
            try:
                # Fetch messages in batches (up to 10)
                messages = await psub.fetch(batch=10, timeout=5)
                
                for msg in messages:
                    await self.process_message(msg)
                    
            except nats.errors.TimeoutError:
                # No messages available - normal, just continue
                await asyncio.sleep(1)
                continue
                
            except Exception as e:
                logger.error(f"❌ Error in consumption loop: {e}", exc_info=True)
                await asyncio.sleep(2)
                continue
    
    async def shutdown(self):
        """Graceful shutdown"""
        logger.info("🛑 Shutting down webhook bridge...")
        self.running = False
        
        if self.nc:
            await self.nc.drain()
            await self.nc.close()
        
        logger.info("✅ Bridge shut down successfully")


class Command(BaseCommand):
    help = 'Run the NATS-Svix webhook bridge consumer'

    def handle(self, *args, **options):
        """Django management command entry point"""
        self.stdout.write(self.style.SUCCESS('=' * 60))
        self.stdout.write(self.style.SUCCESS('  NATS-SVIX WEBHOOK BRIDGE'))
        self.stdout.write(self.style.SUCCESS('=' * 60))
        self.stdout.write('')
        
        # Create bridge instance
        bridge = WebhookBridge()
        
        # Set up signal handlers for graceful shutdown
        def signal_handler(sig, frame):
            self.stdout.write(self.style.WARNING('\n🛑 Received shutdown signal'))
            asyncio.create_task(bridge.shutdown())
        
        signal.signal(signal.SIGINT, signal_handler)
        signal.signal(signal.SIGTERM, signal_handler)
        
        # Run the bridge
        async def run():
            try:
                await bridge.connect()
                await bridge.ensure_stream()
                await bridge.create_durable_consumer()
                await bridge.start_consuming()
            except Exception as e:
                logger.error(f"❌ Fatal error: {e}", exc_info=True)
                self.stdout.write(self.style.ERROR(f'Fatal error: {e}'))
                sys.exit(1)
        
        # Run async event loop
        try:
            asyncio.run(run())
        except KeyboardInterrupt:
            self.stdout.write(self.style.WARNING('\n✅ Bridge stopped'))
