import logging
from typing import Dict, List
from pydantic import BaseModel, Field

from domain.bank import BankItem
from domain.books import BookItem
from agent.reconciliation_agent import run_reconciliation_agent
from domain.patch import ProposedState
from routing.feasibility import build_feasibility_matrix
from routing.scorer import score_all_invoices
from routing.optimizer import optimize_global_routing, GlobalRoutingState
import openai

logger = logging.getLogger(__name__)


class AccountReconciliationResult(BaseModel):
    """The reconciliation result for a single isolated bank account."""
    account_id: str
    proposed_state: ProposedState
    routed_book_item_ids: List[str] = Field(default_factory=list)



class MultiAccountReconciliationResponse(BaseModel):
    """The consolidated response across all bank accounts."""
    routing_state: GlobalRoutingState
    account_results: List[AccountReconciliationResult] = Field(default_factory=list)
    unrouted_book_items: List[BookItem] = Field(default_factory=list)


async def run_multi_account_pipeline(
    book_items: List[BookItem],
    accounts_bank_items: Dict[str, List[BankItem]],
    account_metadata: Dict[str, str],
    evidence_text: str,
    llm_client: openai.AsyncOpenAI,
    currency: str = "MAD",
    model_name: str = "gpt-5.4-mini",
    use_optimizer: bool = True
) -> MultiAccountReconciliationResponse:
    """
    Phase 4 Orchestrator:
    Executes the entire end-to-end Two-Stage Solver Pipeline:
      1. Phase 1: Build mathematical feasibility matrix (F_{i,a})
      2. Phase 2: Score feasible routing choices using LLM (S_{i,a})
      3. Phase 3: Optimize global account routing via CP-SAT
      4. Phase 4: Partition data and run isolated reconciliation engines per account
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

    # Map book items by ID for fast partitioning
    book_lookup = {b.id: b for b in book_items}
    
    # Group routed book items by their assigned account
    partitioned_books: Dict[str, List[BookItem]] = {acc_id: [] for acc_id in accounts_bank_items.keys()}
    
    for assignment in routing_state.assignments:
        book_obj = book_lookup.get(assignment.book_item_id)
        if book_obj:
            partitioned_books[assignment.assigned_account_id].append(book_obj)

    # Track unrouted book items
    unrouted_books = [book_lookup[b_id] for b_id in routing_state.unroutable_book_ids if b_id in book_lookup]

    logger.info("--- Starting Phase 4: Executing Isolated Account Reconciliations ---")
    account_results = []

    for acc_id, bank_items in accounts_bank_items.items():
        routed_books = partitioned_books.get(acc_id, [])
        
        logger.info(
            f"Running reconciliation for Account '{acc_id}' | "
            f"Bank Items: {len(bank_items)} | Routed Book Items: {len(routed_books)}"
        )

        if not bank_items:
            logger.warning(f"Account '{acc_id}' has no bank items. Skipping.")
            continue

        # Execute the core engine on the isolated partition
        proposed_state = await run_reconciliation_agent(
            problem_id=f"RECON_{acc_id}",
            currency=currency,
            bank_items=bank_items,
            book_items=routed_books,
            scenario_evidence=evidence_text,
            model_name=model_name
        )

        account_results.append(
            AccountReconciliationResult(
                account_id=acc_id,
                proposed_state=proposed_state,
                routed_book_item_ids=[b.id for b in routed_books]
            )
        )

    return MultiAccountReconciliationResponse(
        routing_state=routing_state,
        account_results=account_results,
        unrouted_book_items=unrouted_books
    )