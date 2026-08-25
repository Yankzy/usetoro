import json
from typing import Dict, List
from pydantic import BaseModel, Field
import openai

from routing.feasibility import InvoiceRoutingNode, RoutingScore
from routing.prompt import ROUTING_SYSTEM_PROMPT


class RoutingScoreResponse(BaseModel):
    routing_scores: List[RoutingScore]


async def score_invoice_routing(
    node: InvoiceRoutingNode,
    account_metadata: Dict[str, str],  # account_id -> account description/metadata
    evidence_text: str,
    llm_client: openai.AsyncOpenAI,
    model_name: str = "gpt-5.4-mini"
) -> InvoiceRoutingNode:
    """
    Phase 2 Core Function:
    Filters to feasible accounts only, queries the LLM for semantic scores S_{i,a},
    and populates node.semantic_scores.
    """
    # 1. Filter to mathematically feasible accounts only
    feasible_accounts = [f for f in node.feasibility_matrix if f.is_feasible]

    # Edge Case: No feasible accounts exist
    if not feasible_accounts:
        node.semantic_scores = []
        return node

    # Edge Case: Only 1 feasible account exists (Default 1000 score, bypass LLM call to save latency)
    if len(feasible_accounts) == 1:
        single_acc = feasible_accounts[0].account_id
        node.semantic_scores = [
            RoutingScore(
                account_id=single_acc,
                utility_score=1000,
                semantic_rationale="Single mathematically feasible account available."
            )
        ]
        return node

    # 2. Construct the prompt context for multi-account semantic evaluation
    feasible_context = []
    for f in feasible_accounts:
        acc_desc = account_metadata.get(f.account_id, "No metadata provided")
        feasible_context.append(
            f"- Account ID: {f.account_id}\n"
            f"  Metadata: {acc_desc}\n"
            f"  Feasible Bank Item Matches: {f.plausible_bank_item_ids}"
        )

    user_payload = (
        f"BOOK ITEM TO ROUTE:\n"
        f"ID: {node.book_item_id}\n"
        f"Amount Units: {node.amount_units}\n"
        f"Counterparty: {node.counterparty_id or 'Unknown'}\n"
        f"Reference: {node.reference or 'None'}\n\n"
        f"EVIDENCE:\n{evidence_text}\n\n"
        f"FEASIBLE BANK ACCOUNTS TO SCORE:\n" + "\n".join(feasible_context)
    )

    # 3. Call the LLM
    response = await llm_client.chat.completions.create(
        model=model_name,
        messages=[
            {"role": "system", "content": ROUTING_SYSTEM_PROMPT},
            {"role": "user", "content": user_payload}
        ],
        response_format={"type": "json_object"}
    )

    # 4. Parse and assign semantic scores
    message_content = response.choices[0].message.content
    if message_content is None:
        raise ValueError("LLM response did not contain message content")
    raw_json = json.loads(message_content)
    parsed = RoutingScoreResponse.model_validate(raw_json)
    node.semantic_scores = parsed.routing_scores

    return node


async def score_all_invoices(
    routing_nodes: List[InvoiceRoutingNode],
    account_metadata: Dict[str, str],
    evidence_text: str,
    llm_client: openai.AsyncOpenAI,
    model_name: str = "gpt-5.4-mini"
) -> List[InvoiceRoutingNode]:
    """
    Executes Phase 2 scoring sequentially or concurrently for a batch of nodes.
    """
    scored_nodes = []
    for node in routing_nodes:
        scored_node = await score_invoice_routing(
            node=node,
            account_metadata=account_metadata,
            evidence_text=evidence_text,
            llm_client=llm_client,
            model_name=model_name
        )
        scored_nodes.append(scored_node)

    return scored_nodes
