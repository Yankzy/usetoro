import json
import logging
from typing import Dict, List
import nats
from nats.aio.msg import Msg
from openai import AsyncOpenAI
from reconciliation_prod.domain.bank import BankItem
from reconciliation_prod.domain.book import BookItem
from reconciliation_prod.reconciliation.semantic_engine import run_semantic_engine
from reconciliation_prod.reconciliation.cp_sat import solve_reconciliation
from reconciliation_prod.reconciliation.protocol import OptimizerRequest, SolverOptions

logger = logging.getLogger(__name__)

async def handle_reconciliation_request(msg: Msg, llm_client: AsyncOpenAI):
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

        logger.info(f"Received reconciliation request for account {account_id}")

        # Phase 4 & 5: Semantic scoring
        hypotheses = await run_semantic_engine(
            problem_id=f"RECON_{account_id}",
            currency=currency,
            bank_items=bank_items,
            book_items=book_items,
            scenario_evidence=evidence_text,
            llm_client=llm_client,
        )

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

        proposed_state = solve_reconciliation(req)

        if msg.reply:
            response_envelope = {
                "type": "INFORM",
                "payload": proposed_state.model_dump_json(exclude_none=True)
            }
            await msg.respond(json.dumps(response_envelope).encode())

        if msg._reply:
            await msg.ack()

    except Exception as e:
        logger.error(f"Error handling reconciliation request: {e}")
        if msg.reply:
            error_env = {"type": "FAILURE", "payload": json.dumps({"error": str(e)})}
            await msg.respond(json.dumps(error_env).encode())


async def start_reconciliation_worker(nc: nats.NATS, llm_client: AsyncOpenAI):
    subject = "worker.inbox.reconciliation"
    async def cb(msg: Msg):
        await handle_reconciliation_request(msg, llm_client)
    
    await nc.subscribe(subject, cb=cb)
    logger.info(f"Started reconciliation worker on {subject}")
