from __future__ import annotations

from collections import defaultdict
from datetime import datetime, timezone
import math
from typing import Sequence
import uuid

from ortools.sat.python import cp_model

from bookkeeping_state.payment_application.candidate_generation import (
    score_payment_pair,
)
from bookkeeping_state.payment_application.models import (
    PaymentApplicationCandidate,
    PaymentApplicationPlan,
    PaymentApplicationProposal,
    PaymentPostingIntent,
)
from bookkeeping_state.payment_application.view import (
    PaymentApplicationView,
)


def score_candidate(
    candidate: PaymentApplicationCandidate,
    view: PaymentApplicationView,
) -> tuple[int, int, str]:
    """
    Score a payment application candidate across its allocated obligations.

    Returns: (average_semantic_score, total_semantic_value, rationale)
    """
    bank = view.get_bank_item(candidate.bank_item_id)
    if bank is None:
        return 0, 0, "Bank item not in view"

    total_value = 0
    rationales: list[str] = []

    for alloc in candidate.obligation_allocations:
        oblig = view.get_obligation(alloc.book_item_id)
        if oblig is None:
            continue
        p_score, p_reasons = score_payment_pair(bank, oblig)
        total_value += p_score * alloc.amount_int
        rationales.append(f"{oblig.book_item_id}:{p_score} [{', '.join(p_reasons)}]")

    total_amt = candidate.total_amount_int
    avg_score = (total_value // total_amt) if total_amt > 0 else 0
    rat_str = f"Avg score {avg_score} (value {total_value}/{total_amt}; {'; '.join(rationales)})"
    return avg_score, total_value, rat_str


def optimize_payment_application(
    *,
    view: PaymentApplicationView,
    candidates: Sequence[PaymentApplicationCandidate],
    issued_at: datetime | None = None,
    max_solve_seconds: float = 10.0,
) -> PaymentApplicationPlan:
    """
    Globally optimize Stage 1 payment application candidates using CP-SAT.

    Guarantees that the resulting PaymentApplicationPlan is strictly non-conflicting:
    - Each BankItem is allocated at most once.
    - Each Obligation's remaining capacity is respected (sum of allocations <= remaining).
    - Multi-obligation allocations and partial settlements are coordinated globally.
    - Objectives:
      1. Maximize total monetary settlement amount.
      2. Maximize amount-weighted semantic value.
      3. Deterministic tie-break.
    """
    resolved_issued_at = issued_at or datetime.now(timezone.utc)

    if not candidates or not view.bank_items or not view.obligations:
        return PaymentApplicationPlan(
            state_revision=view.state_revision,
            session_id=view.session_id,
            proposals=(),
            unmatched_bank_item_ids=tuple(sorted(b.bank_item_id for b in view.bank_items)),
            unmatched_obligation_ids=tuple(sorted(o.book_item_id for o in view.obligations)),
        )

    # Filter and validate candidates: enforce hard invariant that Stage-1 payment application
    # must fully consume the bank item's remaining capacity.
    valid_candidates: list[PaymentApplicationCandidate] = []
    for cand in candidates:
        bank = view.get_bank_item(cand.bank_item_id)
        if bank is None:
            continue
        if cand.total_amount_int != bank.remaining_amount_int:
            # Stage 1 requires full bank consumption
            continue
        valid_candidates.append(cand)

    if not valid_candidates:
        return PaymentApplicationPlan(
            state_revision=view.state_revision,
            session_id=view.session_id,
            proposals=(),
            unmatched_bank_item_ids=tuple(sorted(b.bank_item_id for b in view.bank_items)),
            unmatched_obligation_ids=tuple(sorted(o.book_item_id for o in view.obligations)),
        )

    # Score candidates and build proposals
    scored_candidates: list[tuple[PaymentApplicationCandidate, int, int, str]] = []
    for cand in valid_candidates:
        avg_score, val, rat = score_candidate(cand, view)
        scored_candidates.append((cand, avg_score, val, rat))

    model = cp_model.CpModel()
    x_vars: dict[str, cp_model.IntVar] = {}

    for cand, _, _, _ in scored_candidates:
        x_vars[cand.candidate_id] = model.new_bool_var(f"x_{cand.candidate_id}")

    # 1. Bank line exclusivity: each bank line used at most once
    bank_to_cands: dict[str, list[str]] = defaultdict(list)
    for cand, _, _, _ in scored_candidates:
        bank_to_cands[cand.bank_item_id].append(cand.candidate_id)

    for bank_id, cand_ids in bank_to_cands.items():
        model.add(sum(x_vars[cid] for cid in cand_ids) <= 1)

    # 2. Obligation capacity bounds: total allocation across candidates <= remaining
    oblig_to_allocs: dict[str, list[tuple[str, int]]] = defaultdict(list)
    for cand, _, _, _ in scored_candidates:
        for alloc in cand.obligation_allocations:
            oblig_to_allocs[alloc.book_item_id].append((cand.candidate_id, alloc.amount_int))

    for oblig_id, alloc_list in oblig_to_allocs.items():
        oblig = view.get_obligation(oblig_id)
        if oblig is None:
            continue
        model.add(
            sum(amt * x_vars[cid] for cid, amt in alloc_list) <= oblig.remaining_amount_int
        )

    # ------------------------------------------------------------------
    # Hierarchical Solve
    # ------------------------------------------------------------------
    def _create_solver() -> cp_model.CpSolver:
        s = cp_model.CpSolver()
        s.parameters.max_time_in_seconds = max_solve_seconds
        s.parameters.num_workers = 1
        return s

    # Phase 1: Maximize total monetary settlement
    money_expr = sum(
        cand.total_amount_int * x_vars[cand.candidate_id]
        for cand, _, _, _ in scored_candidates
    )
    model.maximize(money_expr)
    solver = _create_solver()
    status = solver.solve(model)
    if status not in (cp_model.OPTIMAL, cp_model.FEASIBLE):
        max_money = 0
    else:
        max_money = int(solver.value(money_expr))
    model.add(money_expr == max_money)

    # Phase 2: Maximize amount-weighted semantic value
    utility_expr = sum(
        val * x_vars[cand.candidate_id]
        for cand, _, val, _ in scored_candidates
    )
    model.maximize(utility_expr)
    solver = _create_solver()
    status = solver.solve(model)
    if status in (cp_model.OPTIMAL, cp_model.FEASIBLE):
        max_utility = int(solver.value(utility_expr))
        model.add(utility_expr == max_utility)

    # Phase 3: Deterministic tie-break on candidate ID
    for cand, _, _, _ in sorted(scored_candidates, key=lambda t: t[0].candidate_id):
        var = x_vars[cand.candidate_id]
        model.maximize(var)
        solver = _create_solver()
        status = solver.solve(model)
        if status in (cp_model.OPTIMAL, cp_model.FEASIBLE):
            chosen = int(solver.value(var))
            model.add(var == chosen)

    # Collect selected proposals
    selected_proposals: list[PaymentApplicationProposal] = []
    for cand, avg_score, val, rat in scored_candidates:
        if solver.value(x_vars[cand.candidate_id]) == 1:
            bank = view.get_bank_item(cand.bank_item_id)
            if bank is None:
                continue

            intent = PaymentPostingIntent(
                intent_id=f"intent:{uuid.uuid4().hex[:12]}",
                company_id=view.company_id,
                bank_item_id=bank.bank_item_id,
                bank_account_id=bank.bank_account_id,
                direction=bank.direction,
                currency=bank.currency,
                total_amount_units=cand.total_amount_units,
                payment_date=bank.date,
                obligation_allocations=cand.obligation_allocations,
                evidence_refs=cand.evidence_refs,
                semantic_rationale=rat,
                expected_state_revision=view.state_revision,
                issued_at=resolved_issued_at,
            )

            proposal = PaymentApplicationProposal(
                proposal_id=f"prop:{uuid.uuid4().hex[:12]}",
                candidate=cand,
                semantic_score=avg_score,
                semantic_value=val,
                intent=intent,
            )
            selected_proposals.append(proposal)

    selected_proposals.sort(key=lambda p: p.candidate.candidate_id)

    used_banks = {p.bank_item_id for p in selected_proposals}
    unmatched_banks = tuple(sorted(b.bank_item_id for b in view.bank_items if b.bank_item_id not in used_banks))

    allocated_obligs = {
        alloc.book_item_id
        for p in selected_proposals
        for alloc in p.candidate.obligation_allocations
    }
    unmatched_obligs = tuple(sorted(o.book_item_id for o in view.obligations if o.book_item_id not in allocated_obligs))

    return PaymentApplicationPlan(
        state_revision=view.state_revision,
        session_id=view.session_id,
        proposals=tuple(selected_proposals),
        unmatched_bank_item_ids=unmatched_banks,
        unmatched_obligation_ids=unmatched_obligs,
    )
