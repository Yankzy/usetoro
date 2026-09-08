"""
Deterministic allocation-support analysis for reconciliation candidates.

Separates the economic identity claim (SemanticAdmissibility) from the
obligation allocation claim (AllocationSupport).

Evaluates:
- Contradiction (conflicting references or identity contradiction)
- Explicit allocation evidence (direct reference correspondence, narration token match, shared batch token)
- Deterministic global unique inference (sole compatible obligation or unique subset sum over all open items for that identity)
"""

from __future__ import annotations

from itertools import combinations
import re
from typing import Mapping, Sequence

from bookkeeping_state.domain.enums import (
    AllocationSupport,
    Direction,
    SemanticAdmissibility,
)
from bookkeeping_state.domain.reconciliations import (
    BankAllocation,
    BookAllocation,
)
from bookkeeping_state.reconciliation.models import (
    CounterpartyRelation,
    PairwiseSemanticObservation,
    ReconciliationCandidate,
    ReferenceRelation,
)
from bookkeeping_state.reconciliation.validation import (
    validate_candidate_allocations,
)
from bookkeeping_state.reconciliation.view import (
    ReconciliationBankItemView,
    ReconciliationBookItemView,
    ReconciliationView,
)


def has_explicit_allocation_evidence_for_leg(
    bank_item: ReconciliationBankItemView | None,
    book_item: ReconciliationBookItemView | None,
    semantic_observation: PairwiseSemanticObservation | None = None,
) -> bool:
    """
    Determine whether direct, explicit allocation evidence binds this bank item
    specifically to this book item.

    Recognizes:
    1. Provider-neutral semantic observation of reference match or batch/remittance reference
    2. Exact reference correspondence (case-insensitive, non-empty)
    3. Substring reference in narration (word-bounded reference match)
    4. Shared structured batch token (same explicit token across legs)
    """
    if bank_item is None or book_item is None:
        return False

    if semantic_observation is not None:
        # Constraint 2: Reference contradiction requires positive incompatibility
        if semantic_observation.reference_relation == ReferenceRelation.CONTRADICTED:
            return False

        if (
            semantic_observation.reference_relation == ReferenceRelation.MATCH
            or semantic_observation.matched_reference
            or semantic_observation.batch_or_remittance_reference
        ):
            return True

    b_ref = bank_item.reference.strip().lower() if bank_item.reference else ""
    j_ref = book_item.reference.strip().lower() if book_item.reference else ""

    # 1. Exact non-empty reference match
    if b_ref and j_ref and b_ref == j_ref:
        return True

    # 2. Book reference token appears as an explicit word in bank narration
    if j_ref and bank_item.description:
        desc_lower = bank_item.description.lower()
        if re.search(r"\b" + re.escape(j_ref) + r"\b", desc_lower):
            return True

    # 3. Bank reference token appears as an explicit word in book narration
    if b_ref and book_item.description:
        desc_lower = book_item.description.lower()
        if re.search(r"\b" + re.escape(b_ref) + r"\b", desc_lower):
            return True

    return False


def get_compatible_open_book_items(
    bank_item: ReconciliationBankItemView,
    view: ReconciliationView,
    pairwise_observations: Mapping[tuple[str, str], PairwiseSemanticObservation] | None = None,
) -> tuple[ReconciliationBookItemView, ...]:
    """
    Return all open BookItems in view that have identity support with this bank item
    and satisfy hard feasibility constraints (currency, direction, account routing, date window).
    """
    from bookkeeping_state.reconciliation.candidate_generation import (
        score_reconciliation_pair,
    )

    compatible: list[ReconciliationBookItemView] = []
    for book in view.book_items:
        if book.remaining_amount_int <= 0:
            continue

        # Direction compatibility
        if bank_item.direction == Direction.BANK_INFLOW and book.direction != Direction.BOOK_BANK_DEBIT:
            continue
        if bank_item.direction == Direction.BANK_OUTFLOW and book.direction != Direction.BOOK_BANK_CREDIT:
            continue

        # Currency match
        if view.config.require_exact_currency_match and bank_item.currency != book.currency:
            continue

        # Account routing
        if book.routed_bank_account_id is not None and book.routed_bank_account_id != bank_item.bank_account_id:
            continue

        # Date window
        if view.config.max_date_distance_days is not None:
            if abs((bank_item.date - book.date).days) > view.config.max_date_distance_days:
                continue

        # Identity support check
        if pairwise_observations and (bank_item.bank_item_id, book.book_item_id) in pairwise_observations:
            obs = pairwise_observations[(bank_item.bank_item_id, book.book_item_id)]
            admissibility = obs.identity_admissibility
        else:
            _, _, _, admissibility = score_reconciliation_pair(bank_item, book)

        if admissibility != SemanticAdmissibility.SUPPORTED:
            continue

        compatible.append(book)

    return tuple(compatible)


def get_compatible_open_bank_items(
    book_item: ReconciliationBookItemView,
    view: ReconciliationView,
    pairwise_observations: Mapping[tuple[str, str], PairwiseSemanticObservation] | None = None,
) -> tuple[ReconciliationBankItemView, ...]:
    """
    Return all open BankItems in view that have identity support with this book item
    and satisfy hard feasibility constraints (currency, direction, account routing, date window).
    """
    from bookkeeping_state.reconciliation.candidate_generation import (
        score_reconciliation_pair,
    )

    compatible: list[ReconciliationBankItemView] = []
    for bank in view.bank_items:
        if bank.remaining_amount_int <= 0:
            continue

        # Direction compatibility
        if bank.direction == Direction.BANK_INFLOW and book_item.direction != Direction.BOOK_BANK_DEBIT:
            continue
        if bank.direction == Direction.BANK_OUTFLOW and book_item.direction != Direction.BOOK_BANK_CREDIT:
            continue

        # Currency match
        if view.config.require_exact_currency_match and bank.currency != book_item.currency:
            continue

        # Account routing
        if book_item.routed_bank_account_id is not None and book_item.routed_bank_account_id != bank.bank_account_id:
            continue

        # Date window
        if view.config.max_date_distance_days is not None:
            if abs((bank.date - book_item.date).days) > view.config.max_date_distance_days:
                continue

        # Identity support check
        if pairwise_observations and (bank.bank_item_id, book_item.book_item_id) in pairwise_observations:
            obs = pairwise_observations[(bank.bank_item_id, book_item.book_item_id)]
            admissibility = obs.identity_admissibility
        else:
            _, _, _, admissibility = score_reconciliation_pair(bank, book_item)

        if admissibility != SemanticAdmissibility.SUPPORTED:
            continue

        compatible.append(bank)

    return tuple(compatible)


def find_matching_subsets_book(
    target_amount: int,
    candidate_items: Sequence[ReconciliationBookItemView],
    max_subset_size: int = 6,
) -> list[tuple[str, ...]]:
    """
    Find all subsets of candidate_items whose residual amounts sum exactly to target_amount.
    """
    matching_subsets: list[tuple[str, ...]] = []
    n = len(candidate_items)
    limit = min(n, max_subset_size)

    for r in range(1, limit + 1):
        for combo in combinations(candidate_items, r):
            if sum(item.remaining_amount_int for item in combo) == target_amount:
                matching_subsets.append(tuple(item.book_item_id for item in combo))

    return matching_subsets


def find_matching_subsets_bank(
    target_amount: int,
    candidate_items: Sequence[ReconciliationBankItemView],
    max_subset_size: int = 6,
) -> list[tuple[str, ...]]:
    """
    Find all subsets of candidate_items whose residual amounts sum exactly to target_amount.
    """
    matching_subsets: list[tuple[str, ...]] = []
    n = len(candidate_items)
    limit = min(n, max_subset_size)

    for r in range(1, limit + 1):
        for combo in combinations(candidate_items, r):
            if sum(item.remaining_amount_int for item in combo) == target_amount:
                matching_subsets.append(tuple(item.bank_item_id for item in combo))

    return matching_subsets


def evaluate_allocation_support(
    candidate: ReconciliationCandidate,
    view: ReconciliationView,
    pairwise_observations: Mapping[tuple[str, str], PairwiseSemanticObservation] | None = None,
) -> tuple[AllocationSupport, str]:
    """
    Evaluate allocation-specific evidence for a feasible reconciliation candidate.

    Returns (AllocationSupport, rationale).
    """
    from bookkeeping_state.reconciliation.candidate_generation import (
        decompose_candidate_allocations,
        score_reconciliation_pair,
    )

    pairs = decompose_candidate_allocations(candidate)
    if not pairs:
        return AllocationSupport.INSUFFICIENT_EVIDENCE, "No allocations in candidate"

    # 1. Contradiction and Identity Prerequisite check
    for b_id, j_id, _ in pairs:
        b = view.get_bank_item(b_id)
        j = view.get_book_item(j_id)
        if pairwise_observations and (b_id, j_id) in pairwise_observations:
            obs = pairwise_observations[(b_id, j_id)]
            admissibility = obs.identity_admissibility
            if (
                admissibility == SemanticAdmissibility.CONTRADICTED
                or obs.counterparty_relation == CounterpartyRelation.CONTRADICTED
                or obs.reference_relation == ReferenceRelation.CONTRADICTED
            ):
                return AllocationSupport.CONTRADICTED, f"Pair {b_id}->{j_id} is contradicted"
            if admissibility != SemanticAdmissibility.SUPPORTED:
                return (
                    AllocationSupport.INSUFFICIENT_EVIDENCE,
                    f"Pair {b_id}->{j_id} lacks identity support ({admissibility.value})",
                )
        else:
            _, _, _, admissibility = score_reconciliation_pair(b, j)
            if admissibility == SemanticAdmissibility.CONTRADICTED:
                return AllocationSupport.CONTRADICTED, f"Pair {b_id}->{j_id} is contradicted"
            if admissibility != SemanticAdmissibility.SUPPORTED:
                return (
                    AllocationSupport.INSUFFICIENT_EVIDENCE,
                    f"Pair {b_id}->{j_id} lacks identity support ({admissibility.value})",
                )

    # 2. Explicit Allocation Evidence Check
    # For grouped candidates, every material leg must have explicit evidence.
    all_legs_explicit = True
    for b_id, j_id, _ in pairs:
        b = view.get_bank_item(b_id)
        j = view.get_book_item(j_id)
        obs = pairwise_observations.get((b_id, j_id)) if pairwise_observations else None
        if not has_explicit_allocation_evidence_for_leg(b, j, semantic_observation=obs):
            all_legs_explicit = False
            break

    if all_legs_explicit:
        return AllocationSupport.EXPLICIT_EVIDENCE, "Explicit allocation evidence on all legs"

    # 3. Deterministic Global Unique Inference Check
    # Evaluate whether state analysis proves this is the unique feasible allocation
    # over all open items for that economic identity.
    bank_ids = candidate.bank_item_ids
    book_ids = candidate.book_item_ids
    total_amount = candidate.total_amount_int

    # Case A: 1:N Grouped (1 bank item -> multiple book items)
    if len(bank_ids) == 1 and len(book_ids) > 1:
        b = view.get_bank_item(bank_ids[0])
        if b is None:
            return AllocationSupport.INSUFFICIENT_EVIDENCE, "Bank item not in view"

        compatible_books = get_compatible_open_book_items(b, view, pairwise_observations=pairwise_observations)
        matching_subsets = find_matching_subsets_book(total_amount, compatible_books)

        cand_subset = tuple(sorted(book_ids))
        matching_sorted = [tuple(sorted(sub)) for sub in matching_subsets]

        if len(matching_sorted) == 1 and matching_sorted[0] == cand_subset:
            return (
                AllocationSupport.UNIQUE_INFERENCE,
                f"Unique feasible BookItem subset summing to {total_amount}",
            )
        elif len(matching_sorted) > 1:
            return (
                AllocationSupport.INSUFFICIENT_EVIDENCE,
                f"Ambiguous: {len(matching_sorted)} feasible BookItem subsets sum to {total_amount}",
            )
        else:
            return (
                AllocationSupport.INSUFFICIENT_EVIDENCE,
                f"No matching subsets of remaining capacity sum to {total_amount}",
            )

    # Case B: N:1 Grouped (multiple bank items -> 1 book item)
    elif len(bank_ids) > 1 and len(book_ids) == 1:
        j = view.get_book_item(book_ids[0])
        if j is None:
            return AllocationSupport.INSUFFICIENT_EVIDENCE, "Book item not in view"

        compatible_banks = get_compatible_open_bank_items(j, view, pairwise_observations=pairwise_observations)
        matching_subsets = find_matching_subsets_bank(total_amount, compatible_banks)

        cand_subset = tuple(sorted(bank_ids))
        matching_sorted = [tuple(sorted(sub)) for sub in matching_subsets]

        if len(matching_sorted) == 1 and matching_sorted[0] == cand_subset:
            return (
                AllocationSupport.UNIQUE_INFERENCE,
                f"Unique feasible BankItem subset summing to {total_amount}",
            )
        elif len(matching_sorted) > 1:
            return (
                AllocationSupport.INSUFFICIENT_EVIDENCE,
                f"Ambiguous: {len(matching_sorted)} feasible BankItem subsets sum to {total_amount}",
            )
        else:
            return (
                AllocationSupport.INSUFFICIENT_EVIDENCE,
                f"No matching subsets of remaining capacity sum to {total_amount}",
            )

    # Case C: 1:1 Candidate
    elif len(bank_ids) == 1 and len(book_ids) == 1:
        b = view.get_bank_item(bank_ids[0])
        j = view.get_book_item(book_ids[0])
        if b is None or j is None:
            return AllocationSupport.INSUFFICIENT_EVIDENCE, "Items not in view"

        # C.1: 1:1 Exact (r(b) == r(j))
        if b.remaining_amount_int == j.remaining_amount_int:
            compatible_books = get_compatible_open_book_items(b, view, pairwise_observations=pairwise_observations)
            matching_book_subsets = find_matching_subsets_book(total_amount, compatible_books)

            compatible_banks = get_compatible_open_bank_items(j, view, pairwise_observations=pairwise_observations)
            matching_bank_subsets = find_matching_subsets_bank(total_amount, compatible_banks)

            # Is there any other book subset or bank subset that equals this amount?
            cand_book_subset = (j.book_item_id,)
            cand_bank_subset = (b.bank_item_id,)

            book_unique = (
                len(matching_book_subsets) == 1
                and matching_book_subsets[0] == cand_book_subset
            )
            bank_unique = (
                len(matching_bank_subsets) == 1
                and matching_bank_subsets[0] == cand_bank_subset
            )

            if book_unique and bank_unique:
                return (
                    AllocationSupport.UNIQUE_INFERENCE,
                    f"Sole compatible exact obligation for this economic identity ({total_amount})",
                )
            else:
                reasons: list[str] = []
                if not book_unique:
                    reasons.append(f"{len(matching_book_subsets)} book subsets match amount")
                if not bank_unique:
                    reasons.append(f"{len(matching_bank_subsets)} bank subsets match amount")
                return (
                    AllocationSupport.INSUFFICIENT_EVIDENCE,
                    f"Ambiguous exact allocation: {'; '.join(reasons)}",
                )

        # C.2: 1:1 Partial Book (r(b) < r(j))
        elif b.remaining_amount_int < j.remaining_amount_int:
            if not view.config.allow_partial_book:
                return (
                    AllocationSupport.INSUFFICIENT_EVIDENCE,
                    "Partial book reconciliation forbidden by policy",
                )

            compatible_books = get_compatible_open_book_items(b, view, pairwise_observations=pairwise_observations)
            # A partial candidate is UNIQUE_INFERENCE only if j is the sole compatible
            # open obligation for this economic identity under current constraints.
            if len(compatible_books) == 1 and compatible_books[0].book_item_id == j.book_item_id:
                return (
                    AllocationSupport.UNIQUE_INFERENCE,
                    f"Sole compatible open obligation capable of absorbing partial payment {total_amount}",
                )
            else:
                return (
                    AllocationSupport.INSUFFICIENT_EVIDENCE,
                    f"Ambiguous partial allocation: {len(compatible_books)} open obligations exist for this identity",
                )

        # C.3: 1:1 Partial Bank (r(j) < r(b))
        elif j.remaining_amount_int < b.remaining_amount_int:
            if not view.config.allow_partial_bank:
                return (
                    AllocationSupport.INSUFFICIENT_EVIDENCE,
                    "Partial bank reconciliation forbidden by policy",
                )

            compatible_banks = get_compatible_open_bank_items(j, view, pairwise_observations=pairwise_observations)
            if len(compatible_banks) == 1 and compatible_banks[0].bank_item_id == b.bank_item_id:
                return (
                    AllocationSupport.UNIQUE_INFERENCE,
                    f"Sole compatible open bank item capable of partial absorption {total_amount}",
                )
            else:
                return (
                    AllocationSupport.INSUFFICIENT_EVIDENCE,
                    f"Ambiguous partial allocation: {len(compatible_banks)} bank items exist for this identity",
                )

    # General / fallback
    return AllocationSupport.INSUFFICIENT_EVIDENCE, "Complex multi-leg allocation without explicit evidence"
