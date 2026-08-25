import json
import logging
from typing import Dict, List
import nats
from nats.aio.msg import Msg
from openai import AsyncOpenAI
from reconciliation_prod.domain.bank import BankItem
from reconciliation_prod.domain.book import BookItem
from reconciliation_prod.routing.engine import run_routing_pipeline

logger = logging.getLogger(__name__)

async def handle_routing_request(msg: Msg, llm_client: AsyncOpenAI):
    try:
        # FIPA Envelope parsing
        envelope = json.loads(msg.data.decode())
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
        
        if msg.reply:
            # Wrap in envelope if needed, but simple response first
            response_envelope = {
                "type": "INFORM",
                "payload": json.dumps(response_payload)
            }
            await msg.respond(json.dumps(response_envelope).encode())

        # If it's a JetStream msg, ack it
        if msg._reply:
            await msg.ack()

    except Exception as e:
        logger.error(f"Error handling routing request: {e}")
        if msg.reply:
            error_env = {"type": "FAILURE", "payload": json.dumps({"error": str(e)})}
            await msg.respond(json.dumps(error_env).encode())

async def start_routing_worker(nc: nats.NATS, llm_client: AsyncOpenAI):
    subject = "worker.inbox.routing"
    async def cb(msg: Msg):
        await handle_routing_request(msg, llm_client)
    
    await nc.subscribe(subject, cb=cb)
    logger.info(f"Started routing worker on {subject}")
