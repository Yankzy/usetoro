from __future__ import annotations

from collections import defaultdict
import math
from typing import Final, Sequence

from ortools.sat.python import cp_model

from bookkeeping_state.domain.enums import (
    AllocationSupport,
    Eligibility,
    SemanticAdmissibility,
)
from bookkeeping_state.domain.hypotheses import ReconciliationHypothesis
from bookkeeping_state.reconciliation.models import (
    BookResidual,
    ReconciliationResult,
    UnresolvedBankItem,
    UnresolvedReason,
)
from bookkeeping_state.reconciliation.validation import (
    validate_candidate_allocations,
)
from bookkeeping_state.reconciliation.view import ReconciliationView

CP_SAT_INT64_MAX: Final[int] = (1 << 62) - 1


class ReconciliationOptimizationError(RuntimeError):
    """Raised when the reconciliation optimization problem fails or cannot be solved safely."""


def _determine_unresolved_reason(
    bank_item_id: str,
    view: ReconciliationView,
    hypotheses: Sequence[ReconciliationHypothesis],
) -> UnresolvedReason:
    """
    Distinguish between NO_VALID_CANDIDATES, NO_SEMANTICALLY_ADMISSIBLE_CANDIDATES,
    ALLOCATION_REQUIRES_REVIEW, and NO_MUTUALLY_COMPATIBLE_MATCH.
    """
    b_hyps = [
        h for h in hypotheses
        if any(ba.bank_item_id == bank_item_id for ba in h.bank_allocations)
    ]
    feasible_b_hyps = [
        h for h in b_hyps
        if validate_candidate_allocations(view, h.bank_allocations, h.book_allocations).is_feasible
    ]
    if not feasible_b_hyps:
        return UnresolvedReason.NO_VALID_CANDIDATES
    supported_feasible = [
        h for h in feasible_b_hyps
        if h.admissibility == SemanticAdmissibility.SUPPORTED
    ]
    if not supported_feasible:
        return UnresolvedReason.NO_SEMANTICALLY_ADMISSIBLE_CANDIDATES
    if not any(h.eligibility == Eligibility.SELECTABLE for h in supported_feasible):
        if any(h.allocation_support == AllocationSupport.UNIQUE_INFERENCE for h in supported_feasible):
            return UnresolvedReason.ALLOCATION_REQUIRES_REVIEW
    return UnresolvedReason.NO_MUTUALLY_COMPATIBLE_MATCH


def optimize_reconciliation(
    *,
    view: ReconciliationView,
    hypotheses: Sequence[ReconciliationHypothesis],
    solver_run_id: str,
    max_solve_seconds: float = 10.0,
    forced_hypothesis_ids: Sequence[str] = (),
) -> ReconciliationResult:
    """
    Globally optimize reconciliation selection using CP-SAT.

    Enforces:
    - Exclusion of stale, ineligible, or counterfactual hypotheses
    - Semantic admissibility gate (only SUPPORTED hypotheses can be selected)
    - BankItem exclusivity (each bank line matched at most once)
    - BookItem remaining capacity (sum of book allocations <= remaining amount)
    - Forced hypothesis support

    Lexicographic objective hierarchy:
    1. Maximize total monetary amount reconciled
    2. Maximize amount-weighted semantic value
    3. Maximize unique fully-cleared items (bank items + book items)
    4. Deterministic tie-break on hypothesis ID
    """
    # ------------------------------------------------------------------
    # 1. Filter out stale, non-selectable, inadmissible, or counterfactual hypotheses
    # ------------------------------------------------------------------
    valid_hypotheses: list[ReconciliationHypothesis] = []
    forced_set = set(forced_hypothesis_ids)

    for h in hypotheses:
        # Check revision
        if h.state_revision != view.state_revision:
            continue
        # Check eligibility and admissibility
        if h.eligibility != Eligibility.SELECTABLE:
            continue
        if h.admissibility != SemanticAdmissibility.SUPPORTED:
            continue
        # Deterministic feasibility check against current view
        res = validate_candidate_allocations(
            view,
            h.bank_allocations,
            h.book_allocations,
        )
        if not res.is_feasible:
            continue

        valid_hypotheses.append(h)

    # If any forced hypothesis is invalid, optimization must fail
    valid_hyp_ids = {h.id for h in valid_hypotheses}
    missing_forced = forced_set - valid_hyp_ids
    if missing_forced:
        raise ReconciliationOptimizationError(
            f"Forced hypothesis IDs cannot be satisfied: {sorted(missing_forced)}"
        )

    # If no valid hypotheses, return empty result
    if not valid_hypotheses:
        unresolved = tuple(
            UnresolvedBankItem(
                bank_item_id=b.bank_item_id,
                remaining_amount_units=b.remaining_amount_units,
                reason=_determine_unresolved_reason(b.bank_item_id, view, hypotheses),
            )
            for b in view.bank_items
        )
        review_hypotheses = tuple(
            sorted(
                (
                    h for h in hypotheses
                    if h.state_revision == view.state_revision
                    and h.eligibility == Eligibility.COUNTERFACTUAL_ONLY
                    and h.admissibility == SemanticAdmissibility.SUPPORTED
                ),
                key=lambda h: h.id,
            )
        )
        return ReconciliationResult(
            state_revision=view.state_revision,
            solver_run_id=solver_run_id,
            selected_hypotheses=(),
            review_hypotheses=review_hypotheses,
            unresolved_bank_items=unresolved,
            book_residuals=(),
            objective_reconciled_units="0",
            objective_amount_weighted_semantic_value=0,
            objective_items_cleared=0,
            posted_authority_bank_item_ids=(),
        )

    # ------------------------------------------------------------------
    # 2. Build CP-SAT Model
    # ------------------------------------------------------------------
    model = cp_model.CpModel()
    x_vars: dict[str, cp_model.IntVar] = {}

    for h in valid_hypotheses:
        x_vars[h.id] = model.new_bool_var(f"x_{h.id}")

    # Forced constraints
    for f_id in forced_set:
        model.Add(x_vars[f_id] == 1)

    # Bank item exclusivity constraint:
    # Each bank item is matched at most once across all selected hypotheses
    bank_to_hyps: dict[str, list[tuple[str, int]]] = defaultdict(list)
    for h in valid_hypotheses:
        for b_alloc in h.bank_allocations:
            bank_to_hyps[b_alloc.bank_item_id].append((h.id, b_alloc.amount_int))

    for bank_id, allocations in bank_to_hyps.items():
        bank_item = view.get_bank_item(bank_id)
        if bank_item is None:
            continue
        model.Add(sum(x_vars[h_id] for h_id, _ in allocations) <= 1)

    # Book item capacity constraint:
    # Total consumption across selected hypotheses cannot exceed remaining capacity
    book_to_hyps: dict[str, list[tuple[str, int]]] = defaultdict(list)
    for h in valid_hypotheses:
        for j_alloc in h.book_allocations:
            book_to_hyps[j_alloc.book_item_id].append((h.id, j_alloc.amount_int))

    for book_id, allocations in book_to_hyps.items():
        book_item = view.get_book_item(book_id)
        if book_item is None:
            continue
        capacity = book_item.remaining_amount_int
        consumption_expr = sum(amount * x_vars[h_id] for h_id, amount in allocations)
        model.Add(consumption_expr <= capacity)

    # ------------------------------------------------------------------
    # Unique Fully-Cleared Items variables and exact linkage
    # ------------------------------------------------------------------
    bank_cleared_vars: dict[str, cp_model.IntVar] = {}
    for b in view.bank_items:
        b_var = model.new_bool_var(f"bank_cleared_{b.bank_item_id}")
        bank_cleared_vars[b.bank_item_id] = b_var
        b_allocs = bank_to_hyps.get(b.bank_item_id, [])
        r_b = b.remaining_amount_int
        if not b_allocs:
            model.Add(b_var == 0)
        else:
            bank_alloc_total = sum(amount * x_vars[h_id] for h_id, amount in b_allocs)
            model.Add(bank_alloc_total == r_b).OnlyEnforceIf(b_var)
            model.Add(bank_alloc_total <= r_b - 1).OnlyEnforceIf(b_var.Not())

    book_cleared_vars: dict[str, cp_model.IntVar] = {}
    for j in view.book_items:
        j_var = model.new_bool_var(f"book_cleared_{j.book_item_id}")
        book_cleared_vars[j.book_item_id] = j_var
        j_allocs = book_to_hyps.get(j.book_item_id, [])
        r_j = j.remaining_amount_int
        if not j_allocs:
            model.Add(j_var == 0)
        else:
            book_alloc_total = sum(amount * x_vars[h_id] for h_id, amount in j_allocs)
            model.Add(book_alloc_total == r_j).OnlyEnforceIf(j_var)
            model.Add(book_alloc_total <= r_j - 1).OnlyEnforceIf(j_var.Not())

    # ------------------------------------------------------------------
    # 3. Lexicographic Hierarchical Solve
    # ------------------------------------------------------------------
    def _create_solver() -> cp_model.CpSolver:
        s = cp_model.CpSolver()
        s.parameters.max_time_in_seconds = max_solve_seconds
        s.parameters.num_workers = 1  # Deterministic execution
        return s

    # Phase 1: Maximize total reconciled money
    money_expr = sum(
        h.total_bank_allocation_int * x_vars[h.id] for h in valid_hypotheses
    )
    model.Maximize(money_expr)
    solver = _create_solver()
    status = solver.Solve(model)

    if status not in (cp_model.OPTIMAL, cp_model.FEASIBLE):
        raise ReconciliationOptimizationError(
            f"CP-SAT solver failed at Phase 1 (money): status={status}"
        )
    max_money = int(solver.Value(money_expr))
    model.Add(money_expr == max_money)

    # Phase 2: Maximize amount-weighted semantic value
    # Consumes exact_semantic_value to guarantee exact packaging invariance
    # between grouped hypotheses and fragmented partial hypotheses.
    raw_weighted_terms = [
        (h.id, h.exact_semantic_value)
        for h in valid_hypotheses
    ]
    max_unscaled_sum = sum(val for _, val in raw_weighted_terms)

    scale_divisor = 1
    if max_unscaled_sum > CP_SAT_INT64_MAX:
        all_vals = [val for _, val in raw_weighted_terms if val > 0]
        scale_divisor = math.gcd(*all_vals) if all_vals else 1
        if (max_unscaled_sum // scale_divisor) > CP_SAT_INT64_MAX:
            raise ReconciliationOptimizationError(
                f"Amount-weighted semantic objective exceeds CP-SAT int64 capacity: "
                f"{max_unscaled_sum} with gcd {scale_divisor}"
            )

    utility_expr = sum(
        (val // scale_divisor) * x_vars[h_id]
        for h_id, val in raw_weighted_terms
    )
    model.Maximize(utility_expr)
    solver = _create_solver()
    status = solver.Solve(model)

    if status not in (cp_model.OPTIMAL, cp_model.FEASIBLE):
        raise ReconciliationOptimizationError(
            f"CP-SAT solver failed at Phase 2 (utility): status={status}"
        )
    max_scaled_utility = int(solver.Value(utility_expr))
    model.Add(utility_expr == max_scaled_utility)

    # Phase 3: Maximize unique fully-cleared items (bank items + book items)
    items_expr = sum(bank_cleared_vars.values()) + sum(book_cleared_vars.values())
    model.Maximize(items_expr)
    solver = _create_solver()
    status = solver.Solve(model)

    if status not in (cp_model.OPTIMAL, cp_model.FEASIBLE):
        raise ReconciliationOptimizationError(
            f"CP-SAT solver failed at Phase 3 (items): status={status}"
        )
    max_items = int(solver.Value(items_expr))
    model.Add(items_expr == max_items)

    # Phase 4: Deterministic tie-break
    sorted_hyp_ids = sorted(valid_hyp_ids)
    for h_id in sorted_hyp_ids:
        var = x_vars[h_id]
        model.Maximize(var)
        solver = _create_solver()
        status = solver.Solve(model)
        if status in (cp_model.OPTIMAL, cp_model.FEASIBLE):
            val = int(solver.Value(var))
            model.Add(var == val)

    # ------------------------------------------------------------------
    # 4. Extract Solution
    # ------------------------------------------------------------------
    selected_hypotheses: list[ReconciliationHypothesis] = []
    matched_bank_ids: set[str] = set()
    book_consumption: dict[str, int] = defaultdict(int)

    for h in valid_hypotheses:
        if solver.Value(x_vars[h.id]) == 1:
            selected_hypotheses.append(h)
            for b_alloc in h.bank_allocations:
                matched_bank_ids.add(b_alloc.bank_item_id)
            for j_alloc in h.book_allocations:
                book_consumption[j_alloc.book_item_id] += j_alloc.amount_int

    total_amount_weighted_value = sum(
        h.exact_semantic_value
        for h in selected_hypotheses
    )

    # Unresolved bank items
    unresolved_bank_items: list[UnresolvedBankItem] = []
    for b in view.bank_items:
        if b.bank_item_id not in matched_bank_ids:
            unresolved_bank_items.append(
                UnresolvedBankItem(
                    bank_item_id=b.bank_item_id,
                    remaining_amount_units=b.remaining_amount_units,
                    reason=_determine_unresolved_reason(b.bank_item_id, view, hypotheses),
                )
            )

    # Book residuals
    book_residuals: list[BookResidual] = []
    for book_id, consumed in sorted(book_consumption.items()):
        book_item = view.get_book_item(book_id)
        if book_item is None:
            continue
        starting = book_item.remaining_amount_int
        ending = starting - consumed
        book_residuals.append(
            BookResidual(
                book_item_id=book_id,
                starting_remaining_units=str(starting),
                consumed_units=str(consumed),
                ending_remaining_units=str(ending),
            )
        )

    # Sort selected hypotheses deterministically by id
    selected_hypotheses.sort(key=lambda h: h.id)

    # Counterfactual / review hypotheses (supported but non-selectable, e.g. requiring human review)
    review_hypotheses = tuple(
        sorted(
            (
                h for h in hypotheses
                if h.state_revision == view.state_revision
                and h.eligibility == Eligibility.COUNTERFACTUAL_ONLY
                and h.admissibility == SemanticAdmissibility.SUPPORTED
            ),
            key=lambda h: h.id,
        )
    )

    posted_authority_bank_ids = tuple(
        sorted(
            {
                b_alloc.bank_item_id
                for h in valid_hypotheses
                for b_alloc in h.bank_allocations
            }
        )
    )

    return ReconciliationResult(
        state_revision=view.state_revision,
        solver_run_id=solver_run_id,
        selected_hypotheses=tuple(selected_hypotheses),
        review_hypotheses=review_hypotheses,
        unresolved_bank_items=tuple(unresolved_bank_items),
        book_residuals=tuple(book_residuals),
        objective_reconciled_units=str(max_money),
        objective_amount_weighted_semantic_value=total_amount_weighted_value,
        objective_items_cleared=max_items,
        posted_authority_bank_item_ids=posted_authority_bank_ids,
    )
