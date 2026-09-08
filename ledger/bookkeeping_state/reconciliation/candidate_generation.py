from __future__ import annotations

from itertools import combinations
import re
from typing import Protocol, Sequence, runtime_checkable

from pydantic import BaseModel, ConfigDict, Field

from bookkeeping_state.domain.enums import (
    AllocationSupport,
    Eligibility,
    SemanticAdmissibility,
)
from bookkeeping_state.domain.hypotheses import (
    Identifier,
    ReconciliationHypothesis,
)
from bookkeeping_state.domain.money import (
    AmountUnits,
    amount_units_to_int,
)
from bookkeeping_state.domain.reconciliations import (
    BankAllocation,
    BookAllocation,
)
from bookkeeping_state.reconciliation.allocation_analysis import (
    evaluate_allocation_support,
    has_explicit_allocation_evidence_for_leg,
)
from bookkeeping_state.reconciliation.models import (
    CandidateType,
    ReconciliationCandidate,
)
from bookkeeping_state.reconciliation.validation import (
    validate_candidate_allocations,
)
from bookkeeping_state.reconciliation.view import (
    ReconciliationBankItemView,
    ReconciliationBookItemView,
    ReconciliationView,
)


class CandidateGenerator:
    """
    Deterministic candidate generation for bank-to-book reconciliation.

    Generates only candidates that satisfy hard deterministic feasibility constraints.
    Supports:
    - 1:1 Exact matches
    - 1:1 Partial book matches (if permitted by policy)
    - 1:1 Partial bank matches (if permitted by policy)
    - 1:N Grouped matches (1 bank item to multiple book items)
    - N:1 Grouped matches (multiple bank items to 1 book item)
    """

    def __init__(
        self,
        *,
        max_grouped_subset_size: int = 4,
        max_grouped_candidates_per_target: int = 20,
    ) -> None:
        self.max_grouped_subset_size = max_grouped_subset_size
        self.max_grouped_candidates_per_target = max_grouped_candidates_per_target

    def generate_candidates(
        self,
        view: ReconciliationView,
    ) -> tuple[ReconciliationCandidate, ...]:
        candidates: list[ReconciliationCandidate] = []
        candidate_idx = 1

        # ------------------------------------------------------------------
        # 1. 1:1 Exact Matches
        # ------------------------------------------------------------------
        for bank in view.bank_items:
            for book in view.book_items:
                if bank.remaining_amount_int == book.remaining_amount_int:
                    bank_alloc = (
                        BankAllocation(
                            bank_item_id=bank.bank_item_id,
                            amount_units=bank.remaining_amount_units,
                        ),
                    )
                    book_alloc = (
                        BookAllocation(
                            book_item_id=book.book_item_id,
                            amount_units=book.remaining_amount_units,
                        ),
                    )
                    res = validate_candidate_allocations(view, bank_alloc, book_alloc)
                    if res.is_feasible:
                        candidates.append(
                            ReconciliationCandidate(
                                candidate_id=f"cand-{candidate_idx}",
                                candidate_type=CandidateType.ONE_TO_ONE_EXACT,
                                bank_allocations=bank_alloc,
                                book_allocations=book_alloc,
                                total_amount_units=bank.remaining_amount_units,
                                rationale=f"Exact 1:1 match: Bank {bank.bank_item_id} -> Book {book.book_item_id}",
                            )
                        )
                        candidate_idx += 1

        # ------------------------------------------------------------------
        # 2. 1:1 Partial Book Matches (Bank < Book)
        # ------------------------------------------------------------------
        if view.config.allow_partial_book:
            for bank in view.bank_items:
                for book in view.book_items:
                    if bank.remaining_amount_int < book.remaining_amount_int:
                        bank_alloc = (
                            BankAllocation(
                                bank_item_id=bank.bank_item_id,
                                amount_units=bank.remaining_amount_units,
                            ),
                        )
                        book_alloc = (
                            BookAllocation(
                                book_item_id=book.book_item_id,
                                amount_units=bank.remaining_amount_units,
                            ),
                        )
                        res = validate_candidate_allocations(view, bank_alloc, book_alloc)
                        if res.is_feasible:
                            candidates.append(
                                ReconciliationCandidate(
                                    candidate_id=f"cand-{candidate_idx}",
                                    candidate_type=CandidateType.ONE_TO_ONE_PARTIAL_BOOK,
                                    bank_allocations=bank_alloc,
                                    book_allocations=book_alloc,
                                    total_amount_units=bank.remaining_amount_units,
                                    rationale=(
                                        f"Partial book match: Bank {bank.bank_item_id} consumes "
                                        f"{bank.remaining_amount_int} of Book {book.book_item_id}"
                                    ),
                                )
                            )
                            candidate_idx += 1

        # ------------------------------------------------------------------
        # 3. 1:1 Partial Bank Matches (Book < Bank)
        # ------------------------------------------------------------------
        if view.config.allow_partial_bank:
            for bank in view.bank_items:
                for book in view.book_items:
                    if book.remaining_amount_int < bank.remaining_amount_int:
                        bank_alloc = (
                            BankAllocation(
                                bank_item_id=bank.bank_item_id,
                                amount_units=book.remaining_amount_units,
                            ),
                        )
                        book_alloc = (
                            BookAllocation(
                                book_item_id=book.book_item_id,
                                amount_units=book.remaining_amount_units,
                            ),
                        )
                        res = validate_candidate_allocations(view, bank_alloc, book_alloc)
                        if res.is_feasible:
                            candidates.append(
                                ReconciliationCandidate(
                                    candidate_id=f"cand-{candidate_idx}",
                                    candidate_type=CandidateType.ONE_TO_ONE_PARTIAL_BANK,
                                    bank_allocations=bank_alloc,
                                    book_allocations=book_alloc,
                                    total_amount_units=book.remaining_amount_units,
                                    rationale=(
                                        f"Partial bank match: Book {book.book_item_id} consumes "
                                        f"{book.remaining_amount_int} of Bank {bank.bank_item_id}"
                                    ),
                                )
                            )
                            candidate_idx += 1

        # ------------------------------------------------------------------
        # 4. 1:N Grouped Matches (1 Bank -> Multiple Books)
        # ------------------------------------------------------------------
        for bank in view.bank_items:
            # Filter compatible books preliminarily to avoid massive combinations
            compatible_books = [
                book
                for book in view.book_items
                if book.remaining_amount_int < bank.remaining_amount_int
                and (
                    book.routed_bank_account_id is None
                    or book.routed_bank_account_id == bank.bank_account_id
                )
                and book.currency == bank.currency
            ]

            found_count = 0
            for r in range(2, min(len(compatible_books) + 1, self.max_grouped_subset_size + 1)):
                if found_count >= self.max_grouped_candidates_per_target:
                    break
                for book_combo in combinations(compatible_books, r):
                    if sum(b.remaining_amount_int for b in book_combo) == bank.remaining_amount_int:
                        bank_alloc = (
                            BankAllocation(
                                bank_item_id=bank.bank_item_id,
                                amount_units=bank.remaining_amount_units,
                            ),
                        )
                        book_alloc = tuple(
                            BookAllocation(
                                book_item_id=b.book_item_id,
                                amount_units=b.remaining_amount_units,
                            )
                            for b in book_combo
                        )
                        res = validate_candidate_allocations(view, bank_alloc, book_alloc)
                        if res.is_feasible:
                            candidates.append(
                                ReconciliationCandidate(
                                    candidate_id=f"cand-{candidate_idx}",
                                    candidate_type=CandidateType.ONE_TO_MANY,
                                    bank_allocations=bank_alloc,
                                    book_allocations=book_alloc,
                                    total_amount_units=bank.remaining_amount_units,
                                    rationale=(
                                        f"1:N Grouped: Bank {bank.bank_item_id} matches "
                                        f"{len(book_combo)} Books {[b.book_item_id for b in book_combo]}"
                                    ),
                                )
                            )
                            candidate_idx += 1
                            found_count += 1
                            if found_count >= self.max_grouped_candidates_per_target:
                                break

        # ------------------------------------------------------------------
        # 5. N:1 Grouped Matches (Multiple Banks -> 1 Book)
        # ------------------------------------------------------------------
        for book in view.book_items:
            compatible_banks = [
                bank
                for bank in view.bank_items
                if bank.remaining_amount_int < book.remaining_amount_int
                and (
                    book.routed_bank_account_id is None
                    or book.routed_bank_account_id == bank.bank_account_id
                )
                and bank.currency == book.currency
            ]

            found_count = 0
            for r in range(2, min(len(compatible_banks) + 1, self.max_grouped_subset_size + 1)):
                if found_count >= self.max_grouped_candidates_per_target:
                    break
                for bank_combo in combinations(compatible_banks, r):
                    if sum(b.remaining_amount_int for b in bank_combo) == book.remaining_amount_int:
                        bank_alloc = tuple(
                            BankAllocation(
                                bank_item_id=b.bank_item_id,
                                amount_units=b.remaining_amount_units,
                            )
                            for b in bank_combo
                        )
                        book_alloc = (
                            BookAllocation(
                                book_item_id=book.book_item_id,
                                amount_units=book.remaining_amount_units,
                            ),
                        )
                        res = validate_candidate_allocations(view, bank_alloc, book_alloc)
                        if res.is_feasible:
                            candidates.append(
                                ReconciliationCandidate(
                                    candidate_id=f"cand-{candidate_idx}",
                                    candidate_type=CandidateType.MANY_TO_ONE,
                                    bank_allocations=bank_alloc,
                                    book_allocations=book_alloc,
                                    total_amount_units=book.remaining_amount_units,
                                    rationale=(
                                        f"N:1 Grouped: {len(bank_combo)} Banks "
                                        f"{[b.bank_item_id for b in bank_combo]} match Book {book.book_item_id}"
                                    ),
                                )
                            )
                            candidate_idx += 1
                            found_count += 1
                            if found_count >= self.max_grouped_candidates_per_target:
                                break

        return tuple(candidates)


from bookkeeping_state.domain.enums import (
    Eligibility,
    SemanticAdmissibility,
)


class PairwiseSemanticAssessment(BaseModel):
    """
    Immutable semantic assessment for an individual (bank_item, book_item) allocation.
    """

    model_config = ConfigDict(
        frozen=True,
        extra="forbid",
    )

    bank_item_id: Identifier
    book_item_id: Identifier
    allocated_amount_units: AmountUnits
    score: int = Field(..., ge=0, le=1000)
    admissibility: SemanticAdmissibility = SemanticAdmissibility.SUPPORTED
    allocation_support: AllocationSupport = AllocationSupport.EXPLICIT_EVIDENCE
    evidence_refs: tuple[Identifier, ...] = ()
    evidence_tags: tuple[str, ...] = ()
    rationale: str | None = None

    @property
    def allocated_amount_int(self) -> int:
        return amount_units_to_int(self.allocated_amount_units)

    @property
    def semantic_value(self) -> int:
        return self.score * self.allocated_amount_int


class ReconciliationSemanticAssessment(BaseModel):
    """
    Immutable semantic assessment for a feasible reconciliation candidate.
    """

    model_config = ConfigDict(
        frozen=True,
        extra="forbid",
    )

    candidate_id: Identifier
    semantic_score: int = Field(..., ge=0, le=1000)
    admissibility: SemanticAdmissibility = SemanticAdmissibility.SUPPORTED
    allocation_support: AllocationSupport = AllocationSupport.EXPLICIT_EVIDENCE
    evidence_refs: tuple[Identifier, ...] = ()
    rationale: str | None = None
    semantic_value: int | None = None
    pairwise_assessments: tuple[PairwiseSemanticAssessment, ...] = ()


# ======================================================================
# Production ASE Boundary Contract
# ======================================================================
# ASE / Provider responsibility:
#     Evaluate semantic evidence for already-feasible candidates.
#     Outputs globally comparable cardinal semantic-preference scores in [0, 1000]
#     per unit of reconciled money.
#     The optimizer treats score differences consistently across feasible candidates.
#     It is:
#       - not a probability
#       - not raw LLM confidence
#       - not a claim that 800 is literally twice as correct as 400
#       - required to be comparable across candidate types and items
#       - required to be packaging invariant
#       - independent of hard feasibility
#
#     The production provider SHOULD be empirically calibrated against labelled
#     reconciliation outcomes/evals before treating score magnitudes as strong
#     quantitative evidence.
#
# Optimizer responsibility:
#     Choose globally compatible economic allocation that maximizes:
#       Phase 1: total reconciled money
#       Phase 2: unique fully-cleared items
#       Phase 3: amount-weighted semantic value
#
# Hard deterministic code:
#     Defines feasibility, capacity, currency, direction, routing, and date constraints.
#
# The semantic provider MUST NOT:
#     - Invent candidates
#     - Bypass hard feasibility
#     - Mutate BookkeepingState
#     - Encode packaging topology as semantic evidence
#
# The score consumed by the optimizer must be a cardinal preference score,
# ensuring that candidate packaging alone does not affect selection.
# ======================================================================


@runtime_checkable
class ReconciliationSemanticScoreProvider(Protocol):
    """
    Provider boundary for semantic reconciliation candidate scoring.

    Analogous to RoutingSemanticScoreProvider.
    Orchestration depends on this protocol rather than concrete scoring implementations.
    No network calls.
    """

    def score_candidates(
        self,
        candidates: Sequence[ReconciliationCandidate],
        view: ReconciliationView,
    ) -> Sequence[ReconciliationSemanticAssessment]:
        ...


def decompose_candidate_allocations(
    candidate: ReconciliationCandidate,
) -> tuple[tuple[str, str, int], ...]:
    """
    Decompose candidate into pairwise (bank_item_id, book_item_id, allocated_amount_int).

    Guarantees:
    1. sum(allocated_amount_int for _, _, amount in pairs) == candidate.total_amount_int
    2. Decomposes 1:1, 1:N, and N:1 candidate structures into their exact component pairs.
    """
    if len(candidate.bank_allocations) == 1 and len(candidate.book_allocations) == 1:
        b = candidate.bank_allocations[0]
        j = candidate.book_allocations[0]
        return ((b.bank_item_id, j.book_item_id, b.amount_int),)
    elif len(candidate.bank_allocations) == 1 and len(candidate.book_allocations) > 1:
        b = candidate.bank_allocations[0]
        return tuple(
            (b.bank_item_id, j.book_item_id, j.amount_int)
            for j in candidate.book_allocations
        )
    elif len(candidate.bank_allocations) > 1 and len(candidate.book_allocations) == 1:
        j = candidate.book_allocations[0]
        return tuple(
            (b.bank_item_id, j.book_item_id, b.amount_int)
            for b in candidate.bank_allocations
        )
    else:
        # General case fallback (e.g. M:N)
        pairs: list[tuple[str, str, int]] = []
        b_rem = [b.amount_int for b in candidate.bank_allocations]
        j_rem = [j.amount_int for j in candidate.book_allocations]
        b_idx = 0
        j_idx = 0
        while b_idx < len(b_rem) and j_idx < len(j_rem):
            alloc = min(b_rem[b_idx], j_rem[j_idx])
            if alloc > 0:
                pairs.append(
                    (
                        candidate.bank_allocations[b_idx].bank_item_id,
                        candidate.book_allocations[j_idx].book_item_id,
                        alloc,
                    )
                )
                b_rem[b_idx] -= alloc
                j_rem[j_idx] -= alloc
            if b_rem[b_idx] == 0:
                b_idx += 1
            if j_rem[j_idx] == 0:
                j_idx += 1
        return tuple(pairs)


TRANSACTION_WORDS = {
    "payment", "payments", "invoice", "invoices", "bill", "bills",
    "facture", "factures", "reglement", "reglements", "transfer", "transfers",
    "acompte", "acomptes", "solde", "soldes", "virement", "virements",
    "encaissement", "encaissements", "settlement", "settlements", "purchase", "purchases",
    "deposit", "deposits", "receivable", "receivables", "claim", "claims",
    "divers", "multi", "partial", "split", "batch", "order", "unrelated", "receipt", "receipts",
}


def _normalize_counterparty_str(s: str) -> str:
    s = s.strip().lower()
    for prefix in ("client ", "customer ", "fournisseur ", "supplier ", "vendor "):
        if s.startswith(prefix):
            s = s[len(prefix):].strip()
    return s


def _extract_counterparty(desc: str | None, cp_name: str | None = None) -> str | None:
    if cp_name and cp_name.strip():
        norm = _normalize_counterparty_str(cp_name)
        if norm and norm not in TRANSACTION_WORDS and not norm.isdigit() and len(norm) > 1:
            return norm
    if not desc:
        return None
    desc_lower = desc.strip().lower()
    m = re.search(
        r"\b(?:client|customer|fournisseur|supplier|vendor)"
        r"(?:\s+(?:payment|payments|invoice|invoices|bill|bills|facture|factures|reglement|reglements|transfer|transfers|virement|virements|encaissement|encaissements|settlement|settlements))?"
        r"\s+([a-z0-9_\-]+)\b",
        desc_lower,
    )
    if m:
        word = m.group(1).lower()
        if word not in TRANSACTION_WORDS and not word.isdigit() and len(word) > 1:
            return word
    for keyword in ("amazon", "aws", "salaires", "salary", "payroll", "loyer", "rent", "office supplies", "papeterie"):
        if keyword in desc_lower:
            return keyword
    return None


def _normalize_cp_entity(name: str | None) -> str | None:
    if not name:
        return None
    name = name.strip().lower()
    if name in ("amazon", "aws"):
        return "aws"
    if name in ("salaires", "salary", "payroll", "remuneration"):
        return "payroll"
    if name in ("loyer", "rent", "location", "bail"):
        return "rent"
    if name in ("office supplies", "papeterie", "fournitures"):
        return "office_supplies"
    return name


def score_reconciliation_pair(
    bank_item: ReconciliationBankItemView | None,
    book_item: ReconciliationBookItemView | None,
) -> tuple[int, tuple[str, ...], tuple[str, ...], SemanticAdmissibility]:
    """
    Score pairwise economic evidence between a BankItem and BookItem, and determine admissibility.

    Semantic evidence rules:
    - Reference correspondence: +200 if non-empty references match exactly (case-insensitive)
    - Counterparty correspondence: +100 if counterparty matches (case-insensitive)
    - Temporal proximity: +50 if same day (0 days diff); +25 if within 3 calendar days (<= 3 days diff)

    Admissibility:
    - SUPPORTED: Affirmative identity evidence present (matching reference or matching counterparty)
      without explicit conflicting counterparty evidence.
    - CONTRADICTED: Explicit conflicting references (both have non-empty structured references that differ)
      or conflicting counterparty entities.
    - INSUFFICIENT_EVIDENCE: Neither supported nor contradicted (e.g. same amount/date without identity evidence).

    Zero structural bonuses, zero arbitrary baselines.
    A non-supported pair returns score 0.
    """
    if bank_item is None or book_item is None:
        return 0, (), ("missing_item:0",), SemanticAdmissibility.INSUFFICIENT_EVIDENCE

    # 1. Reference correspondence
    b_ref = bank_item.reference.strip().lower() if bank_item.reference else ""
    j_ref = book_item.reference.strip().lower() if book_item.reference else ""

    has_matching_ref = bool(b_ref and j_ref and b_ref == j_ref)
    has_conflicting_ref = bool(b_ref and j_ref and b_ref != j_ref)

    # 2. Counterparty correspondence
    b_cp = _extract_counterparty(bank_item.description)
    j_cp = _extract_counterparty(book_item.description, book_item.counterparty_name)

    b_norm = _normalize_cp_entity(b_cp)
    j_norm = _normalize_cp_entity(j_cp)

    has_matching_cp = False
    has_conflicting_cp = False

    if b_norm and j_norm:
        if b_norm == j_norm:
            has_matching_cp = True
        else:
            has_conflicting_cp = True
    elif j_norm and bank_item.description and j_norm in bank_item.description.lower():
        has_matching_cp = True
    elif b_norm and book_item.description and b_norm in book_item.description.lower():
        has_matching_cp = True
    elif book_item.counterparty_name and bank_item.description:
        raw_cp = book_item.counterparty_name.strip().lower()
        if raw_cp and raw_cp in bank_item.description.lower():
            has_matching_cp = True

    # 3. Admissibility determination
    if has_conflicting_cp:
        admissibility = SemanticAdmissibility.CONTRADICTED
    elif has_matching_ref or has_matching_cp:
        admissibility = SemanticAdmissibility.SUPPORTED
    elif has_conflicting_ref:
        admissibility = SemanticAdmissibility.CONTRADICTED
    else:
        admissibility = SemanticAdmissibility.INSUFFICIENT_EVIDENCE

    # 4. Scoring calculation
    score = 0
    reasons: list[str] = []
    evidence_tags: list[str] = []

    if has_matching_ref:
        score += 200
        reasons.append("ref_match:+200")
        evidence_tags.append(f"ref:{bank_item.reference.strip()}")

    if has_matching_cp:
        score += 100
        reasons.append("cp_match:+100")
        tag_val = j_cp or (book_item.counterparty_name.strip() if book_item.counterparty_name else "cp")
        evidence_tags.append(f"cp:{tag_val}")

    days = abs((bank_item.date - book_item.date).days)
    if days == 0:
        score += 50
        reasons.append("same_day:+50")
        evidence_tags.append("date:same_day")
    elif days <= 3:
        score += 25
        reasons.append("within_3d:+25")
        evidence_tags.append("date:within_3d")

    if admissibility != SemanticAdmissibility.SUPPORTED:
        score = 0
        reasons.append(f"inadmissible:{admissibility.value}")

    clamped = max(0, min(1000, score))
    return clamped, tuple(evidence_tags), tuple(reasons), admissibility


class DefaultReconciliationScorer:
    """
    Deterministic eval/reference semantic scoring provider with hand-authored evidence weights.

    Implements ReconciliationSemanticScoreProvider.
    Evaluates pairwise semantic evidence across economic record relationships:
    - Reference correspondence (+200)
    - Counterparty correspondence (+100)
    - Temporal proximity (+50 same day, +25 within 3 days)

    NOTE: This is an eval/reference provider using hand-authored evidence weights, NOT
    a production-calibrated scorer. Production providers should be empirically calibrated
    against labelled reconciliation outcomes/evals before treating score magnitudes as
    strong quantitative evidence.

    Contains NO structural candidate shape bonuses and NO arbitrary baselines.
    """

    def score_candidate(
        self,
        candidate: ReconciliationCandidate,
        view: ReconciliationView,
    ) -> ReconciliationSemanticAssessment:
        pairs = decompose_candidate_allocations(candidate)
        pairwise_assessments: list[PairwiseSemanticAssessment] = []
        total_semantic_value = 0
        pair_summaries: list[str] = []

        # Evaluate allocation support
        alloc_support, alloc_rationale = evaluate_allocation_support(candidate, view)

        for b_id, j_id, amt in pairs:
            b_item = view.get_bank_item(b_id)
            j_item = view.get_book_item(j_id)
            p_score, p_tags, p_reasons, p_admissibility = score_reconciliation_pair(b_item, j_item)

            p_val = p_score * amt
            total_semantic_value += p_val

            doc_refs = j_item.evidence_document_ids if j_item else ()
            p_leg_explicit = has_explicit_allocation_evidence_for_leg(b_item, j_item)
            p_alloc_support = (
                AllocationSupport.EXPLICIT_EVIDENCE
                if p_leg_explicit
                else alloc_support
            )
            p_assessment = PairwiseSemanticAssessment(
                bank_item_id=b_id,
                book_item_id=j_id,
                allocated_amount_units=str(amt),
                score=p_score,
                admissibility=p_admissibility,
                allocation_support=p_alloc_support,
                evidence_refs=doc_refs,
                evidence_tags=p_tags,
                rationale=", ".join(p_reasons) if p_reasons else "neutral:no_evidence",
            )
            pairwise_assessments.append(p_assessment)
            pair_summaries.append(
                f"{b_id}->{j_id}:{p_score} [{p_admissibility.value}; {p_alloc_support.value}; {', '.join(p_reasons) if p_reasons else 'no_evidence'}]"
            )

        if any(p.admissibility == SemanticAdmissibility.CONTRADICTED for p in pairwise_assessments):
            cand_admissibility = SemanticAdmissibility.CONTRADICTED
        elif all(p.admissibility == SemanticAdmissibility.SUPPORTED for p in pairwise_assessments):
            cand_admissibility = SemanticAdmissibility.SUPPORTED
        else:
            cand_admissibility = SemanticAdmissibility.INSUFFICIENT_EVIDENCE

        # If identity is contradicted/insufficient OR allocation is contradicted/insufficient
        if cand_admissibility != SemanticAdmissibility.SUPPORTED or alloc_support in (
            AllocationSupport.CONTRADICTED,
            AllocationSupport.INSUFFICIENT_EVIDENCE,
        ):
            semantic_score = 0
            total_semantic_value = 0
            rationale_str = (
                f"Inadmissible: identity={cand_admissibility.value}, "
                f"allocation={alloc_support.value} ({alloc_rationale}; pairs: {'; '.join(pair_summaries)})"
            )
        else:
            total_amount = candidate.total_amount_int
            if total_amount > 0:
                semantic_score = max(0, min(1000, total_semantic_value // total_amount))
            else:
                semantic_score = 0

            if len(pairs) == 1:
                p_reasons_str = pairwise_assessments[0].rationale
                rationale_str = f"Semantic score {semantic_score} ({p_reasons_str}) [allocation: {alloc_support.value}]"
            else:
                rationale_str = (
                    f"Semantic score {semantic_score} (weighted value {total_semantic_value} / {total_amount}; "
                    f"pairs: {'; '.join(pair_summaries)}) [allocation: {alloc_support.value}]"
                )

        return ReconciliationSemanticAssessment(
            candidate_id=candidate.candidate_id,
            semantic_score=semantic_score,
            admissibility=cand_admissibility,
            allocation_support=alloc_support,
            evidence_refs=candidate.evidence_refs,
            rationale=rationale_str,
            semantic_value=total_semantic_value,
            pairwise_assessments=tuple(pairwise_assessments),
        )

    def score_candidates(
        self,
        candidates: Sequence[ReconciliationCandidate],
        view: ReconciliationView,
    ) -> tuple[ReconciliationSemanticAssessment, ...]:
        return tuple(self.score_candidate(c, view) for c in candidates)

    def score(
        self,
        candidate: ReconciliationCandidate,
        view: ReconciliationView,
    ) -> tuple[int, str]:
        """Convenience method for backwards compatibility."""
        assessment = self.score_candidate(candidate, view)
        return assessment.semantic_score, assessment.rationale or ""

    def score_candidates_to_hypotheses(
        self,
        candidates: Sequence[ReconciliationCandidate],
        view: ReconciliationView,
    ) -> tuple[ReconciliationHypothesis, ...]:
        """Convenience helper to produce runtime hypotheses directly."""
        hypotheses: list[ReconciliationHypothesis] = []
        for cand in candidates:
            assessment = self.score_candidate(cand, view)
            if assessment.admissibility != SemanticAdmissibility.SUPPORTED or assessment.allocation_support in (
                AllocationSupport.CONTRADICTED,
                AllocationSupport.INSUFFICIENT_EVIDENCE,
            ):
                eligibility = Eligibility.INELIGIBLE
            elif assessment.allocation_support == AllocationSupport.EXPLICIT_EVIDENCE:
                eligibility = Eligibility.SELECTABLE
            elif assessment.allocation_support == AllocationSupport.UNIQUE_INFERENCE:
                if view.config.auto_reconcile_unique_inferred_allocation:
                    eligibility = Eligibility.SELECTABLE
                else:
                    eligibility = Eligibility.COUNTERFACTUAL_ONLY
            else:
                eligibility = Eligibility.INELIGIBLE

            hyp = cand.to_hypothesis(
                state_revision=view.state_revision,
                utility=assessment.semantic_score,
                semantic_score=assessment.semantic_score,
                semantic_value=assessment.semantic_value,
                eligibility=eligibility,
                admissibility=assessment.admissibility,
                allocation_support=assessment.allocation_support,
                evidence_refs=assessment.evidence_refs,
                semantic_rationale=assessment.rationale,
            )
            hypotheses.append(hyp)
        return tuple(hypotheses)
