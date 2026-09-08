from __future__ import annotations

import asyncio
import json
import logging
from collections import defaultdict
from typing import Any

from openai import AsyncOpenAI
from pydantic import BaseModel, Field

from bookkeeping_state_eval.llm.client import (
    get_model_name,
    get_openai_client,
    run_coro_sync,
)
from bookkeeping_state_eval.routing.scorer import (
    RoutingProviderScore,
    RoutingSemanticCandidate,
    RoutingSemanticScoreProvider,
    RoutingSemanticScoringError,
    RoutingSemanticScoringRequest,
    RoutingSemanticScoringResponse,
)

logger = logging.getLogger(__name__)

# Donor system prompt ported from reconciliation_eval/routing/prompt.py
ROUTING_SYSTEM_PROMPT = """You are an expert accounting router. Your job is to determine which bank account a book item (invoice/bill) belongs to based on semantic evidence, references, counterparty metadata, and account descriptions.

RULES:
1. You will be provided a book item and a list of MATHEMATICALLY FEASIBLE bank accounts.
2. Do NOT evaluate or invent accounts that are not in the provided feasible list.
3. For EACH feasible account, assign a utility score S_{i,a} from 0 to 1000:
   - 900-1000: Strong evidence (e.g., explicit reference match, explicit account name match, known dedicated supplier account).
   - 600-890: Plausible match (e.g., standard operating expense likely to come from this account type).
   - 100-590: Low confidence / Weak connection.
   - 0: Completely implausible based on evidence.
4. Provide a brief semantic rationale for each assigned score.

Respond ONLY with a JSON object matching this schema:
{
  "routing_scores": [
    {
      "account_id": "string",
      "utility_score": integer,
      "semantic_rationale": "string"
    }
  ]
}
"""


class RoutingScore(BaseModel):
    account_id: str
    utility_score: int = Field(..., ge=0, le=1000)
    semantic_rationale: str


class RoutingScoreResponse(BaseModel):
    routing_scores: list[RoutingScore]


class LlmRoutingSemanticScoreProvider:
    """
    Real LLM routing semantic scoring provider.
    Ports and adapts the donor implementation from reconciliation_eval/routing/scorer.py
    into the bookkeeping_state_eval.routing.scorer.RoutingSemanticScoreProvider protocol.
    """

    def __init__(
        self,
        *,
        llm_client: AsyncOpenAI | None = None,
        model_name: str | None = None,
        max_retries: int = 3,
    ) -> None:
        self._llm_client = llm_client
        self._model_name = model_name or get_model_name("gpt-5.6-luna")
        self._max_retries = max_retries

    @property
    def model_name(self) -> str:
        return self._model_name

    def _get_client(self) -> AsyncOpenAI:
        if self._llm_client is None:
            self._llm_client = get_openai_client()
        return self._llm_client

    async def _score_book_item_async(
        self,
        book_item_id: str,
        candidates: list[RoutingSemanticCandidate],
    ) -> list[RoutingProviderScore]:
        """
        Score all feasible accounts for one book item.
        """
        feasible_accounts = [c.bank_account_id for c in candidates]

        if not feasible_accounts:
            return []

        # Singleton feasibility optimization:
        # Preserve donor behavior (avoid unnecessary LLM latency for singleton feasibility),
        # but mark telemetry clearly as deterministic singleton feasibility, not LLM reasoning.
        if len(feasible_accounts) == 1:
            return [
                RoutingProviderScore(
                    book_item_id=book_item_id,
                    bank_account_id=feasible_accounts[0],
                    score=1000,
                    rationale="Deterministic singleton feasibility: sole mathematically feasible account.",
                )
            ]

        # Multi-account evaluation: construct context following donor scorer.py
        first = candidates[0]
        accounts_context: list[str] = []
        for c in candidates:
            witness_items = [
                f"{ev.bank_item_id} ({ev.remaining_amount_units} {ev.direction.value})"
                for ev in c.bank_evidence
                if ev.is_feasibility_witness
            ]
            accounts_context.append(
                f"- Account ID: {c.bank_account_id}\n"
                f"  Account Name: {c.bank_account_name}\n"
                f"  Institution: {c.institution_name or 'None'}\n"
                f"  Feasible Bank Item Matches: {', '.join(witness_items)}"
            )

        evidence_lines: list[str] = []
        if first.book_description:
            evidence_lines.append(f"Description: {first.book_description}")
        if first.book_reference:
            evidence_lines.append(f"Reference: {first.book_reference}")
        if first.counterparty_name:
            evidence_lines.append(f"Counterparty: {first.counterparty_name}")
        if first.counterparty_aliases:
            evidence_lines.append(f"Aliases: {', '.join(first.counterparty_aliases)}")
        if first.evidence_document_ids:
            evidence_lines.append(
                f"Evidence Documents: {', '.join(first.evidence_document_ids)}"
            )
        evidence_text = "\n".join(evidence_lines) if evidence_lines else "None provided"

        user_payload = (
            f"BOOK ITEM TO ROUTE:\n"
            f"ID: {book_item_id}\n"
            f"Amount Units: {first.target_amount_units} {first.currency}\n"
            f"Date: {first.book_date}\n"
            f"Direction: {first.book_direction.value}\n"
            f"Counterparty: {first.counterparty_name or 'Unknown'}\n"
            f"Reference: {first.book_reference or 'None'}\n\n"
            f"EVIDENCE:\n{evidence_text}\n\n"
            f"FEASIBLE BANK ACCOUNTS TO SCORE:\n" + "\n".join(accounts_context)
        )

        client = self._get_client()
        last_err: Exception | None = None

        for attempt in range(1, self._max_retries + 1):
            try:
                response = await client.chat.completions.create(
                    model=self._model_name,
                    service_tier="fast",
                    messages=[
                        {"role": "system", "content": ROUTING_SYSTEM_PROMPT},
                        {"role": "user", "content": user_payload},
                    ],
                    response_format={"type": "json_object"},
                )

                content = response.choices[0].message.content
                if not content:
                    raise RoutingSemanticScoringError(
                        f"LLM returned empty content for routing book item {book_item_id!r}"
                    )

                raw_json = json.loads(content)
                parsed = RoutingScoreResponse.model_validate(raw_json)

                # Validate strict adherence to feasible accounts
                returned_accounts = [item.account_id for item in parsed.routing_scores]
                if len(set(returned_accounts)) != len(returned_accounts):
                    raise RoutingSemanticScoringError(
                        f"LLM returned duplicate account scores for book item {book_item_id!r}: {returned_accounts}"
                    )

                unknown_accounts = set(returned_accounts) - set(feasible_accounts)
                if unknown_accounts:
                    raise RoutingSemanticScoringError(
                        f"LLM proposed unknown/infeasible accounts for {book_item_id!r}: {unknown_accounts}"
                    )

                missing_accounts = set(feasible_accounts) - set(returned_accounts)
                if missing_accounts:
                    raise RoutingSemanticScoringError(
                        f"LLM omitted feasible accounts for {book_item_id!r}: {missing_accounts}"
                    )

                scores_by_account = {
                    item.account_id: item for item in parsed.routing_scores
                }

                provider_scores = []
                for acc_id in feasible_accounts:
                    item = scores_by_account[acc_id]
                    if not (0 <= item.utility_score <= 1000):
                        raise RoutingSemanticScoringError(
                            f"LLM returned out-of-bounds score {item.utility_score} for account {acc_id!r}"
                        )
                    provider_scores.append(
                        RoutingProviderScore(
                            book_item_id=book_item_id,
                            bank_account_id=acc_id,
                            score=item.utility_score,
                            rationale=item.semantic_rationale,
                        )
                    )

                return provider_scores

            except Exception as exc:
                last_err = exc
                logger.warning(
                    f"Attempt {attempt}/{self._max_retries} failed scoring routing for {book_item_id}: {exc}"
                )
                if attempt < self._max_retries:
                    await asyncio.sleep(0.5 * (2 ** (attempt - 1)))

        raise RoutingSemanticScoringError(
            f"Failed to score routing for book item {book_item_id} after {self._max_retries} attempts: {last_err}"
        ) from last_err

    async def _score_async(
        self,
        request: RoutingSemanticScoringRequest,
    ) -> RoutingSemanticScoringResponse:
        by_book_item: dict[str, list[RoutingSemanticCandidate]] = defaultdict(list)
        for cand in request.candidates:
            by_book_item[cand.book_item_id].append(cand)

        # Concurrently score all book items
        tasks = [
            self._score_book_item_async(book_item_id, candidates)
            for book_item_id, candidates in by_book_item.items()
        ]

        results = await asyncio.gather(*tasks)

        all_scores: list[RoutingProviderScore] = []
        for scores_list in results:
            all_scores.extend(scores_list)

        # Deterministically order by (book_item_id, bank_account_id)
        all_scores.sort(key=lambda s: (s.book_item_id, s.bank_account_id))

        return RoutingSemanticScoringResponse(
            state_revision=request.state_revision,
            model_run_id=self._model_name,
            scores=tuple(all_scores),
        )

    def score(
        self,
        request: RoutingSemanticScoringRequest,
    ) -> RoutingSemanticScoringResponse:
        """
        Synchronous provider protocol method.
        Safely bridges to the async OpenAI call.
        """
        return run_coro_sync(self._score_async(request))
