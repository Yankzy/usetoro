from __future__ import annotations

import asyncio
import json
import logging
from collections.abc import Sequence
from typing import Any

from openai import AsyncOpenAI
from pydantic import BaseModel, Field

from bookkeeping_state_eval.domain.enums import (
    AllocationSupport,
    SemanticAdmissibility,
)
from bookkeeping_state_eval.llm.client import (
    get_model_name,
    get_openai_client,
    run_coro_sync,
)
from bookkeeping_state_eval.reconciliation.allocation_analysis import (
    evaluate_allocation_support,
    has_explicit_allocation_evidence_for_leg,
)
from bookkeeping_state_eval.reconciliation.candidate_generation import (
    PairwiseSemanticAssessment,
    ReconciliationCandidate,
    ReconciliationSemanticAssessment,
    ReconciliationSemanticScoreProvider,
    decompose_candidate_allocations,
)
from bookkeeping_state_eval.reconciliation.models import (
    CounterpartyRelation,
    PairwiseSemanticObservation,
    ReferenceRelation,
)
from bookkeeping_state_eval.reconciliation.view import (
    ReconciliationView,
)

logger = logging.getLogger(__name__)


class ReconciliationSemanticScoringError(RuntimeError):
    """Raised when reconciliation semantic scoring violates its contract."""


RECONCILIATION_SEMANTIC_SYSTEM_PROMPT = """You are an expert accounting reconciliation reasoner operating inside a constrained reconciliation system.
Your job is to evaluate semantic relationships between bank movements and book-side accounting objects (invoices, bills, journal entries) based on textual evidence, descriptions, references, dates, and counterparty metadata.

You are NOT responsible for calculating amount sums, capacity, subset uniqueness, currency feasibility, or solving the global allocation. Those have been deterministically verified.
You are evaluating PRE-CALCULATED, MATHEMATICALLY FEASIBLE candidate pairs.

RULES:
1. Evaluate EACH provided (BankItem, BookItem) pair independently based strictly on semantic and documentary evidence.
2. For each assessment, bank_item_id MUST be the exact BankItem ID and book_item_id MUST be the exact BookItem ID of the provided pair. Never invent items or IDs not provided in the input, and do not put a BookItem ID into bank_item_id.
3. For EACH pair, assign a semantic preference score (0 to 1000):
   - 900-1000: Strong evidence (e.g. exact or clear reference match, explicit counterparty match, confirmed invoice settlement).
   - 600-890: Plausible connection (e.g. matching counterparty entity or clear business context, standard payment pattern).
   - 100-590: Weak connection / ambiguous (e.g. matching amounts but no identifying counterparty or reference).
   - 0: Contradicted or completely implausible.
4. Determine identity_admissibility:
   - SUPPORTED: Affirmative identity evidence connects the bank item and book item (matching counterparty, matching reference, or explicit narration).
   - CONTRADICTED: Explicit conflicting economic identities (e.g. contradictory counterparty entities).
   - INSUFFICIENT_EVIDENCE: Neither supported nor contradicted (e.g. matching amount alone without identity evidence).
5. Report semantic observations:
   - counterparty_relation: MATCH (same counterparty/entity), POSSIBLE_ALIAS (likely alias/subsidiary), CONTRADICTED (contradictory entity), or NONE (no counterparty data).
   - reference_relation: MATCH (matching reference or narration reference), CONTRADICTED (positive evidence of conflicting reference numbers), or NONE (no reference data or different reference namespaces).
     IMPORTANT: Do NOT mark reference_relation as CONTRADICTED simply because references are absent or formatted differently across namespaces (e.g. internal batch ID vs customer invoice ID). Use CONTRADICTED ONLY if the text explicitly states payment for a different incompatible invoice (e.g. bank text explicitly says payment for INV-200 while book item is INV-100).
   - matched_reference: the specific reference string found in common or narration, if any.
   - partial_payment_language: true if narration contains explicit partial payment terminology (e.g. 'acompte', 'partial', 'installment', 'deposit'), false otherwise.
   - batch_or_remittance_reference: batch or remittance slip token if present.
   - evidence_tags: list of short tags (e.g. "ref:INV-01", "cp:Acme", "lang:partial").
   - semantic_rationale: concise explanation of your reasoning.

Respond ONLY with a JSON object matching this schema:
{
  "pair_assessments": [
    {
      "bank_item_id": "string",
      "book_item_id": "string",
      "score": integer,
      "identity_admissibility": "SUPPORTED" | "INSUFFICIENT_EVIDENCE" | "CONTRADICTED",
      "counterparty_relation": "MATCH" | "POSSIBLE_ALIAS" | "CONTRADICTED" | "NONE",
      "reference_relation": "MATCH" | "CONTRADICTED" | "NONE",
      "matched_reference": "string or null",
      "partial_payment_language": boolean,
      "batch_or_remittance_reference": "string or null",
      "evidence_tags": ["string"],
      "semantic_rationale": "string"
    }
  ]
}
"""


class LlmPairwiseAssessment(BaseModel):
    bank_item_id: str
    book_item_id: str
    score: int = Field(..., ge=0, le=1000)
    identity_admissibility: str
    counterparty_relation: str
    reference_relation: str
    matched_reference: str | None = None
    partial_payment_language: bool = False
    batch_or_remittance_reference: str | None = None
    evidence_tags: list[str] = Field(default_factory=list)
    semantic_rationale: str


class LlmReconciliationScoreResponse(BaseModel):
    pair_assessments: list[LlmPairwiseAssessment]


class LlmReconciliationSemanticScoreProvider:
    """
    Real LLM reconciliation semantic scoring provider.
    Evaluates pairwise relationships between BankItems and BookItems with packaging invariance,
    translating LLM observations into provider-neutral PairwiseSemanticObservation records.
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

    async def _score_pairs_async(
        self,
        unique_pairs: list[tuple[str, str]],
        view: ReconciliationView,
    ) -> dict[tuple[str, str], PairwiseSemanticObservation]:
        """
        Query LLM to score unique (bank_item_id, book_item_id) pairs in a packaging-blind manner.
        """
        if not unique_pairs:
            return {}

        pairs_context: list[str] = []
        for b_id, j_id in unique_pairs:
            b = view.get_bank_item(b_id)
            j = view.get_book_item(j_id)
            if b is None:
                raise ReconciliationSemanticScoringError(
                    f"BankItem {b_id!r} not found in view"
                )
            if j is None:
                raise ReconciliationSemanticScoringError(
                    f"BookItem {j_id!r} not found in view"
                )

            pairs_context.append(
                f"- PAIR: BankItem {b.bank_item_id} <-> BookItem {j.book_item_id}\n"
                f"  Bank: date={b.date}, rem_amt={b.remaining_amount_units} {b.currency}, dir={b.direction.value}, desc={b.description or 'None'}, ref={b.reference or 'None'}\n"
                f"  Book: date={j.date}, rem_amt={j.remaining_amount_units} {j.currency}, dir={j.direction.value}, desc={j.description or 'None'}, ref={j.reference or 'None'}, cp={j.counterparty_name or 'None'}, docs={list(j.evidence_document_ids)}"
            )

        user_payload = (
            f"RECONCILIATION ECONOMIC PAIRS TO EVALUATE:\n\n"
            + "\n\n".join(pairs_context)
            + "\n\nProvide structured assessments for each pair."
        )

        client = self._get_client()
        last_err: Exception | None = None

        for attempt in range(1, self._max_retries + 1):
            try:
                response = await client.responses.create(
                    model=self._model_name,
                    service_tier="fast",
                    input=[
                        {"role": "system", "content": RECONCILIATION_SEMANTIC_SYSTEM_PROMPT},
                        {"role": "user", "content": user_payload},
                    ],
                    text={
                        "format": {
                            "type": "json_schema",
                            "name": "reconciliation_score_response",
                            "schema": LlmReconciliationScoreResponse.model_json_schema(),
                            "strict": False,
                        }
                    },
                )

                content = response.output_text
                if not content:
                    raise ReconciliationSemanticScoringError(
                        "LLM returned empty content for reconciliation pair scoring"
                    )

                raw_json = json.loads(content)
                parsed = LlmReconciliationScoreResponse.model_validate(raw_json)

                # Validation & error handling
                expected_pairs_set = set(unique_pairs)
                returned_pairs_set: set[tuple[str, str]] = set()

                observations: dict[tuple[str, str], PairwiseSemanticObservation] = {}

                for item in parsed.pair_assessments:
                    pair = (item.bank_item_id, item.book_item_id)
                    if pair in returned_pairs_set:
                        raise ReconciliationSemanticScoringError(
                            f"LLM returned duplicate assessment for pair {pair!r}"
                        )
                    if pair not in expected_pairs_set:
                        raise ReconciliationSemanticScoringError(
                            f"LLM proposed unknown pair {pair!r} not in requested feasible set"
                        )
                    returned_pairs_set.add(pair)

                    if not (0 <= item.score <= 1000):
                        raise ReconciliationSemanticScoringError(
                            f"LLM returned out-of-bounds score {item.score} for pair {pair!r}"
                        )

                    try:
                        admissibility = SemanticAdmissibility(item.identity_admissibility)
                    except ValueError as exc:
                        raise ReconciliationSemanticScoringError(
                            f"Invalid identity admissibility {item.identity_admissibility!r} for pair {pair!r}"
                        ) from exc

                    try:
                        cp_rel = CounterpartyRelation(item.counterparty_relation)
                    except ValueError:
                        cp_rel = CounterpartyRelation.NONE

                    try:
                        ref_rel = ReferenceRelation(item.reference_relation)
                    except ValueError:
                        ref_rel = ReferenceRelation.NONE

                    obs = PairwiseSemanticObservation(
                        bank_item_id=item.bank_item_id,
                        book_item_id=item.book_item_id,
                        identity_admissibility=admissibility,
                        semantic_score=item.score,
                        counterparty_relation=cp_rel,
                        reference_relation=ref_rel,
                        matched_reference=item.matched_reference,
                        partial_payment_language=item.partial_payment_language,
                        batch_or_remittance_reference=item.batch_or_remittance_reference,
                        evidence_tags=tuple(item.evidence_tags),
                        rationale=item.semantic_rationale,
                    )
                    observations[pair] = obs

                missing_pairs = expected_pairs_set - returned_pairs_set
                if missing_pairs:
                    raise ReconciliationSemanticScoringError(
                        f"LLM omitted assessments for pairs: {sorted(missing_pairs)!r}"
                    )

                return observations

            except Exception as exc:
                last_err = exc
                logger.warning(
                    f"Attempt {attempt}/{self._max_retries} failed scoring reconciliation pairs: {exc}"
                )
                if attempt < self._max_retries:
                    await asyncio.sleep(0.5 * (2 ** (attempt - 1)))

        raise ReconciliationSemanticScoringError(
            f"Failed to score reconciliation pairs after {self._max_retries} attempts: {last_err}"
        ) from last_err

    def score_candidates(
        self,
        candidates: Sequence[ReconciliationCandidate],
        view: ReconciliationView,
    ) -> Sequence[ReconciliationSemanticAssessment]:
        """
        Evaluate candidate assessments by scoring unique economic pairs packaging-blindly.
        """
        if not candidates:
            return ()

        # 1. Collect unique (bank_item_id, book_item_id) pairs across all candidates
        unique_pairs_set: set[tuple[str, str]] = set()
        for cand in candidates:
            for b_id, j_id, _ in decompose_candidate_allocations(cand):
                unique_pairs_set.add((b_id, j_id))

        unique_pairs = sorted(unique_pairs_set)

        # 2. Score all unique pairs via async LLM call
        pairwise_observations = run_coro_sync(
            self._score_pairs_async(unique_pairs, view)
        )

        # 3. Assemble candidate assessments from pairwise observations
        assessments: list[ReconciliationSemanticAssessment] = []

        for cand in sorted(candidates, key=lambda c: c.candidate_id):
            pairs = decompose_candidate_allocations(cand)
            pairwise_assessments: list[PairwiseSemanticAssessment] = []

            # Evaluate allocation support using provider-neutral observations
            alloc_support, alloc_rationale = evaluate_allocation_support(
                cand, view, pairwise_observations=pairwise_observations
            )

            pair_summaries: list[str] = []
            exact_semantic_value = 0

            for b_id, j_id, amt in pairs:
                obs = pairwise_observations.get((b_id, j_id))
                if obs is None:
                    raise ReconciliationSemanticScoringError(
                        f"Missing pairwise semantic observation for ({b_id}, {j_id})"
                    )

                j_item = view.get_book_item(j_id)
                doc_refs = j_item.evidence_document_ids if j_item else ()

                b_item = view.get_bank_item(b_id)
                p_leg_explicit = has_explicit_allocation_evidence_for_leg(
                    b_item, j_item, semantic_observation=obs
                )
                p_alloc_support = (
                    AllocationSupport.EXPLICIT_EVIDENCE
                    if p_leg_explicit
                    else alloc_support
                )

                p_assessment = PairwiseSemanticAssessment(
                    bank_item_id=b_id,
                    book_item_id=j_id,
                    allocated_amount_units=str(amt),
                    score=obs.semantic_score,
                    admissibility=obs.identity_admissibility,
                    allocation_support=p_alloc_support,
                    evidence_refs=doc_refs,
                    evidence_tags=obs.evidence_tags,
                    rationale=obs.rationale or "neutral:no_evidence",
                )
                pairwise_assessments.append(p_assessment)

                p_val = obs.semantic_score * amt
                exact_semantic_value += p_val

                pair_summaries.append(
                    f"{b_id}->{j_id}:{obs.semantic_score} [{obs.identity_admissibility.value}; {p_alloc_support.value}; {obs.rationale}]"
                )

            # Candidate identity admissibility
            if any(p.admissibility == SemanticAdmissibility.CONTRADICTED for p in pairwise_assessments):
                cand_admissibility = SemanticAdmissibility.CONTRADICTED
            elif all(p.admissibility == SemanticAdmissibility.SUPPORTED for p in pairwise_assessments):
                cand_admissibility = SemanticAdmissibility.SUPPORTED
            else:
                cand_admissibility = SemanticAdmissibility.INSUFFICIENT_EVIDENCE

            # Candidate-level utility score (floor average for telemetry) & exact integer semantic value (for optimizer)
            total_amount = cand.total_amount_int
            if (
                cand_admissibility != SemanticAdmissibility.SUPPORTED
                or alloc_support in (AllocationSupport.CONTRADICTED, AllocationSupport.INSUFFICIENT_EVIDENCE)
            ):
                cand_score = 0
                exact_semantic_value = 0
                rationale_str = (
                    f"Inadmissible: identity={cand_admissibility.value}, "
                    f"allocation={alloc_support.value} ({alloc_rationale}; pairs: {'; '.join(pair_summaries)})"
                )
            else:
                cand_score = (
                    max(0, min(1000, exact_semantic_value // total_amount))
                    if total_amount > 0
                    else 0
                )
                if len(pairs) == 1:
                    p_reasons_str = pairwise_assessments[0].rationale
                    rationale_str = f"Semantic score {cand_score} ({p_reasons_str}) [allocation: {alloc_support.value}]"
                else:
                    rationale_str = (
                        f"Semantic score {cand_score} (exact weighted value {exact_semantic_value} / {total_amount}; "
                        f"pairs: {'; '.join(pair_summaries)}) [allocation: {alloc_support.value}]"
                    )

            assessments.append(
                ReconciliationSemanticAssessment(
                    candidate_id=cand.candidate_id,
                    semantic_score=cand_score,
                    admissibility=cand_admissibility,
                    allocation_support=alloc_support,
                    evidence_refs=cand.evidence_refs,
                    rationale=rationale_str,
                    semantic_value=exact_semantic_value,
                    pairwise_assessments=tuple(pairwise_assessments),
                )
            )

        return tuple(assessments)
