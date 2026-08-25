import logging
from typing import List, Optional
from pydantic import BaseModel
from openai import AsyncOpenAI
import os

from reconciliation_prod.domain.bank import BankItem
from reconciliation_prod.domain.book import BookItem
from reconciliation_prod.domain.hypothesis import ReconciliationHypothesis
from reconciliation_prod.reconciliation.candidate_generation import generate_plausible_candidates

logger = logging.getLogger(__name__)

SYSTEM_PROMPT = """You are an accounting reconciliation reasoner operating inside a constrained reconciliation system. Your job is to determine plausible relationships between bank movements and canonical book-side accounting objects using the evidence supplied to you. 

You are not responsible for exact global allocation arithmetic or searching for amount matches. We have provided a list of PRE-CALCULATED PLAUSIBLE CANDIDATES. Your job is to evaluate these candidates against the EVIDENCE and generate explicit reconciliation hypotheses.

Rules:
1. The global optimizer will test whether your hypotheses coexist globally. Treat its mathematical constraints as authoritative.
2. Unresolved is a valid outcome. Do NOT create hypotheses for unresolved bank movements; simply omit them.
3. Never invent a canonical object that does not exist in the supplied state.
4. Utility values (0-1000) are relative preference rankings. Assign higher utilities (900+) to candidates with strong evidence (e.g., exact reference matches) and lower utilities (500-700) to plausible but ambiguous candidates (e.g., matching amounts without references).
5. CRITICAL: If the pre-calculated list shows multiple overlapping candidates for the same bank movement, you MUST generate a SEPARATE hypothesis for EACH plausible match in your output. Assign them appropriate utilities based on evidence, and let the optimizer mathematically resolve the overlap.
"""

class ScoredHypothesesResponse(BaseModel):
    hypotheses: List[ReconciliationHypothesis]

async def run_semantic_engine(
    problem_id: str,
    currency: str,
    bank_items: List[BankItem],
    book_items: List[BookItem],
    scenario_evidence: str,
    llm_client: AsyncOpenAI,
    model_name: str = "gpt-5.4-mini"
) -> List[ReconciliationHypothesis]:
    """
    Executes a single-shot LLM reasoning to score candidates semantically.
    Returns a list of scored hypotheses.
    """
    plausible_candidates = generate_plausible_candidates(bank_items, book_items)

    context = (
        f"SCENARIO: {problem_id}\n"
        f"CURRENCY: {currency}\n\n"
        f"BANK ITEMS:\n{[b.model_dump_json() for b in bank_items]}\n\n"
        f"BOOK ITEMS:\n{[j.model_dump_json() for j in book_items]}\n\n"
        f"EVIDENCE:\n{scenario_evidence}\n\n"
        f"--- PRE-CALCULATED PLAUSIBLE CANDIDATES ---\n"
        f"The following mathematically valid combinations were found. "
        f"Use the evidence to SCORE them (assigning utility) and generate hypotheses for them. "
        f"If a bank item appears in multiple overlapping candidates, generate a hypothesis for EACH one.\n"
        f"{plausible_candidates}\n\n"
        f"Please output a JSON object conforming to the schema."
    )

    messages = [
        {"role": "system", "content": SYSTEM_PROMPT},
        {"role": "user", "content": context}
    ]

    logger.info(f"Invoking LLM Semantic Engine for {problem_id}")
    
    response = await llm_client.beta.chat.completions.parse(
        model=model_name,
        messages=messages,
        response_format=ScoredHypothesesResponse,
    )

    parsed_response = response.choices[0].message.parsed
    if parsed_response is None:
        logger.error("LLM failed to parse response into expected schema.")
        return []
        
    return parsed_response.hypotheses
