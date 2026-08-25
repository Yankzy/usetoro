import logging
from typing import Dict, List
from pydantic import BaseModel, Field

from reconciliation_prod.domain.bank import BankItem
from reconciliation_prod.domain.book import BookItem
from reconciliation_prod.routing.feasibility import build_feasibility_matrix
from reconciliation_prod.routing.scorer import score_all_invoices
from reconciliation_prod.routing.optimizer import optimize_global_routing, GlobalRoutingState
import openai

logger = logging.getLogger(__name__)


async def run_routing_pipeline(
    book_items: List[BookItem],
    accounts_bank_items: Dict[str, List[BankItem]],
    account_metadata: Dict[str, str],
    evidence_text: str,
    llm_client: openai.AsyncOpenAI,
    model_name: str = "gpt-5.4-mini",
) -> GlobalRoutingState:
    """
    Executes the Global Routing Pipeline:
      1. Phase 1: Build mathematical feasibility matrix (F_{i,a})
      2. Phase 2: Score feasible routing choices using LLM (S_{i,a})
      3. Phase 3: Optimize global account routing via CP-SAT
    """
    logger.info("--- Starting Phase 1: Building Mathematical Feasibility Matrix ---")
    feasibility_nodes = build_feasibility_matrix(book_items, accounts_bank_items)

    logger.info("--- Starting Phase 2: Scoring Feasible Account Routings ---")
    scored_nodes = await score_all_invoices(
        routing_nodes=feasibility_nodes,
        account_metadata=account_metadata,
        evidence_text=evidence_text,
        llm_client=llm_client,
        model_name=model_name
    )

    logger.info("--- Starting Phase 3: Optimizing Global Account Assignments ---")
    routing_state = optimize_global_routing(scored_nodes, accounts_bank_items)

    return routing_state