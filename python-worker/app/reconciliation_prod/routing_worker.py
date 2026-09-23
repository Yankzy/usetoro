import json
import logging
import os
from typing import Any, Dict, List, Optional
import nats
from nats.aio.client import Client as NATS
from nats.aio.msg import Msg
from openai import AsyncOpenAI

from app import nats_client
from reconciliation_prod.domain.bank import BankItem
from reconciliation_prod.domain.book import BookItem
from reconciliation_prod.routing.engine import run_routing_pipeline

logger = logging.getLogger(__name__)

_openai_client: AsyncOpenAI | None = None

def get_openai_client() -> AsyncOpenAI:
    global _openai_client
    if _openai_client is None:
        api_key = os.environ.get("OPENAI_API_KEY", "")
        _openai_client = AsyncOpenAI(api_key=api_key)
    return _openai_client

async def handle_routing_request(msg: Msg):
    llm_client = get_openai_client()
    await handler(msg, llm_client)

async def handler(msg: Msg, llm_client: AsyncOpenAI):
    print(f"\n🚀 [ROUTING WORKER] Handler triggered! Received msg on '{msg.subject}' (Reply: {msg.reply}, Size: {len(msg.data)} bytes)", flush=True)
    envelope: Optional[Dict[str, Any]] = None
    try:
        parsed = json.loads(msg.data.decode())
        envelope = parsed if isinstance(parsed, dict) else {}
        payload_str = envelope.get("payload", "{}")
        if isinstance(payload_str, str):
            payload = json.loads(payload_str)
        else:
            payload = payload_str

        # Assuming the payload has "book_items" and "accounts_bank_items", "account_metadata", "evidence_text"
        book_items = [BookItem(**b) for b in payload.get("book_items", [])]
        
        accounts_bank_items_raw = payload.get("accounts_bank_items", {})
        accounts_bank_items = {
            acc: [BankItem(**b) for b in items] 
            for acc, items in accounts_bank_items_raw.items()
        }

        account_metadata = payload.get("account_metadata", {})
        evidence_text = payload.get("evidence_text", "")

        print(f"📦 [ROUTING WORKER] Parsed routing request: {len(book_items)} book items across {len(accounts_bank_items)} accounts", flush=True)
        logger.info(f"Received routing request. Books: {len(book_items)}, Accounts: {len(accounts_bank_items)}")

        # Execute
        routing_state = await run_routing_pipeline(
            book_items=book_items,
            accounts_bank_items=accounts_bank_items,
            account_metadata=account_metadata,
            evidence_text=evidence_text,
            llm_client=llm_client,
            model_name="gpt-4o"
        )

        response_payload = routing_state.model_dump(exclude_none=True)
        
        # Resolve target reply topic (matching ocr.py pattern)
        reply_subject = (
            (envelope.get("final_destination_subject") if isinstance(envelope, dict) else None)
            or (envelope.get("reply_subject") if isinstance(envelope, dict) else None)
            or (envelope.get("reply_to") if isinstance(envelope, dict) else None)
            or (payload.get("final_destination_subject") if isinstance(payload, dict) else None)
            or (payload.get("reply_subject") if isinstance(payload, dict) else None)
            or (payload.get("reply_to") if isinstance(payload, dict) else None)
            or (msg.headers.get("Nats-Reply-To") if hasattr(msg, "headers") and msg.headers else None)
            or (msg.reply if msg.reply and not msg.reply.startswith("$JS.ACK.") else None)
        )

        if reply_subject:
            response_envelope = {
                "type": "INFORM",
                "payload": json.dumps(response_payload)
            }
            print(f"📤 [ROUTING WORKER] Publishing result to reply subject: {reply_subject}", flush=True)
            await nats_client.publish(reply_subject, response_envelope)
            print(f"🎉 [ROUTING WORKER] Response published to {reply_subject}!", flush=True)

        # If it's a JetStream msg, ack it
        if hasattr(msg, "ack") and callable(msg.ack):
            try:
                await msg.ack()
            except Exception:
                pass

    except Exception as e:
        print(f"❌ [ROUTING WORKER] Error in handler: {e}", flush=True)
        import traceback
        traceback.print_exc()
        logger.error(f"Error handling routing request: {e}")
        try:
            target = (
                (envelope.get("final_destination_subject") if isinstance(envelope, dict) else None)
                or (envelope.get("reply_subject") if isinstance(envelope, dict) else None)
                or (envelope.get("reply_to") if isinstance(envelope, dict) else None)
                or (msg.reply if msg.reply and not msg.reply.startswith("$JS.ACK.") else None)
            )
            if target:
                error_env = {"type": "FAILURE", "payload": json.dumps({"error": str(e)})}
                await nats_client.publish(target, error_env)
        except Exception:
            pass

async def start_routing_worker(nc: NATS, llm_client: AsyncOpenAI):
    subject = "worker.inbox.routing"
    durable_name = "worker-inbox-routing-group"

    async def cb(msg: Msg):
        await handler(msg, llm_client)
    
    try:
        js = nc.jetstream()
        print(f"📡 [ROUTING WORKER] Subscribing via JetStream to '{subject}' (durable: {durable_name})...", flush=True)
        await js.subscribe(subject, durable=durable_name, cb=cb, manual_ack=True)
        print(f"✅ [ROUTING WORKER] Subscribed to JetStream subject '{subject}' with durable '{durable_name}'", flush=True)
        logger.info(f"Subscribed to JetStream subject {subject} with durable {durable_name}")
    except Exception as e:
        print(f"⚠️ [ROUTING WORKER] JetStream subscription fallback to core NATS on '{subject}': {e}", flush=True)
        logger.warning(f"JetStream subscription fallback to core NATS on {subject}: {e}")
        await nc.subscribe(subject, cb=cb)
        print(f"✅ [ROUTING WORKER] Subscribed to core NATS subject '{subject}'", flush=True)
        logger.info(f"Subscribed to core NATS subject {subject}")
