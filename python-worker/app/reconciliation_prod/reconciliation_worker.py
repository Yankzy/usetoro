"""
Reconciliation NATS Worker.

This module listens for NATS messages on `worker.inbox.reconciliation`,
orchestrates the Semantic Engine and CP-SAT Optimizer pipeline, and responds
with the optimal proposed accounting state.
"""
import json
import logging
from typing import Dict, List
import nats
from nats.aio.msg import Msg
from openai import AsyncOpenAI
from reconciliation_prod.domain.bank import BankItem
from reconciliation_prod.domain.book import BookItem

from reconciliation_prod.reconciliation.cp_sat import solve_reconciliation
from reconciliation_prod.reconciliation.protocol import OptimizerRequest, SolverOptions
from app import nats_client

import os
logger = logging.getLogger(__name__)

_openai_client: AsyncOpenAI | None = None

def get_openai_client() -> AsyncOpenAI:
    global _openai_client
    if _openai_client is None:
        api_key = os.environ.get("OPENAI_API_KEY", "")
        _openai_client = AsyncOpenAI(api_key=api_key)
    return _openai_client

async def handle_reconciliation_request(msg: Msg):
    llm_client = get_openai_client()
    await handler(msg, llm_client)

async def handler(msg: Msg, llm_client: AsyncOpenAI):
    """
    Handles an incoming NATS message requesting a reconciliation solve.
    """
    print(f"\n🚀 [RECONCILIATION WORKER] Handler triggered! Received msg on '{msg.subject}' (Reply: {msg.reply}, Size: {len(msg.data)} bytes)", flush=True)
    try:
        envelope = json.loads(msg.data.decode())
        payload_str = envelope.get("payload", "{}")
        if isinstance(payload_str, str):
            payload = json.loads(payload_str)
        else:
            payload = payload_str

        account_id = payload.get("account_id", "UNKNOWN")
        currency = payload.get("currency", "MAD")
        bank_items = [BankItem(**b) for b in payload.get("bank_items", [])]
        book_items = [BookItem(**b) for b in payload.get("book_items", [])]
        evidence_text = payload.get("evidence_text", "")

        print(f"📦 [RECONCILIATION WORKER] Parsed payload for account '{account_id}'. Bank items: {len(bank_items)}, Book items: {len(book_items)}", flush=True)

        from reconciliation_prod.reconciliation.semantic_engine import run_semantic_engine
        print(f"🧠 [RECONCILIATION WORKER] Running Semantic Engine (Candidate Generation & LLM Scoring)...", flush=True)
        hypotheses = await run_semantic_engine(
            problem_id=f"RECON_{account_id}",
            currency=currency,
            bank_items=bank_items,
            book_items=book_items,
            scenario_evidence=evidence_text,
            llm_client=llm_client,
        )
        print(f"💡 [RECONCILIATION WORKER] Semantic Engine generated {len(hypotheses)} scored hypotheses", flush=True)

        # Phase 6: CP-SAT Lexicographic Reconciliation
        req = OptimizerRequest(
            problem_id=f"RECON_{account_id}",
            operation="OPTIMIZE",
            currency=currency,
            bank_items=bank_items,
            book_items=book_items,
            hypotheses=hypotheses,
            forced_hypothesis_ids=[],
            solver_options=SolverOptions()
        )

        print(f"⚙️ [RECONCILIATION WORKER] Solving with CP-SAT Optimizer...", flush=True)
        proposed_state = solve_reconciliation(req)
        print(f"✅ [RECONCILIATION WORKER] CP-SAT solve complete. Status: {proposed_state.status}, Formed {len(proposed_state.selected_hypotheses)} matches, {len(proposed_state.unresolved_bank_items)} unresolved.", flush=True)

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
                "payload": proposed_state.model_dump_json(exclude_none=True)
            }
            print(f"📤 [RECONCILIATION WORKER] Publishing result to reply subject: {reply_subject}", flush=True)
            await nats_client.publish(reply_subject, response_envelope)
            print(f"🎉 [RECONCILIATION WORKER] Response published successfully to {reply_subject}!", flush=True)

        if hasattr(msg, "ack") and callable(msg.ack):
            try:
                await msg.ack()
                print(f"📨 [RECONCILIATION WORKER] JetStream message acked", flush=True)
            except Exception:
                pass

    except Exception as e:
        print(f"❌ [RECONCILIATION WORKER] Error in handler: {e}", flush=True)
        import traceback
        traceback.print_exc()
        logger.error(f"Error handling reconciliation request: {e}")
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


async def start_reconciliation_worker(nc: nats.NATS, llm_client: AsyncOpenAI):
    """
    Starts the NATS subscription for the reconciliation worker.
    
    Args:
        nc (nats.NATS): The connected NATS client.
        llm_client (AsyncOpenAI): The OpenAI client instance to inject.
    """
    subject = "worker.inbox.reconciliation"
    durable_name = "worker-inbox-reconciliation-group"

    async def cb(msg: Msg):
        await handler(msg, llm_client)
    
    try:
        js = nc.jetstream()
        print(f"📡 [RECONCILIATION WORKER] Subscribing via JetStream to '{subject}' (durable: {durable_name})...", flush=True)
        await js.subscribe(subject, durable=durable_name, cb=cb, manual_ack=True)
        print(f"✅ [RECONCILIATION WORKER] Subscribed to JetStream subject '{subject}' with durable '{durable_name}'", flush=True)
        logger.info(f"Subscribed to JetStream subject {subject} with durable {durable_name}")
    except Exception as e:
        print(f"⚠️ [RECONCILIATION WORKER] JetStream subscription fallback to core NATS on '{subject}': {e}", flush=True)
        logger.warning(f"JetStream subscription fallback to core NATS on {subject}: {e}")
        await nc.subscribe(subject, cb=cb)
        print(f"✅ [RECONCILIATION WORKER] Subscribed to core NATS subject '{subject}'", flush=True)
        logger.info(f"Subscribed to core NATS subject {subject}")
