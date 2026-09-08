from __future__ import annotations

from typing import Sequence

from bookkeeping_state_eval.domain.reconciliations import (
    BankAllocation,
    BookAllocation,
)
from bookkeeping_state_eval.reconciliation.models import (
    CandidateFeasibilityResult,
    FeasibilityStatus,
)
from bookkeeping_state_eval.reconciliation.view import ReconciliationView
from bookkeeping_state_eval.state.validation import DIRECTION_COMPATIBILITY_MAP


def validate_candidate_allocations(
    view: ReconciliationView,
    bank_allocations: Sequence[BankAllocation],
    book_allocations: Sequence[BookAllocation],
    *,
    max_date_distance_days: int | None = None,
) -> CandidateFeasibilityResult:
    """
    Deterministically validate a set of bank and book allocations against the bounded view.

    Enforces all hard accounting and policy invariants:
    - Monetary balance
    - Item presence in view (non-zero remaining)
    - No duplicate item references in the candidate
    - Capacity boundaries (cannot exceed remaining amounts)
    - Partial-allocation policies (bank & book)
    - Currency uniformity
    - Direction compatibility
    - Routing consistency (routed BookItem cannot reconcile against a different BankAccount)
    - Date-window bounds (if specified)
    """
    if not bank_allocations or not book_allocations:
        return CandidateFeasibilityResult(
            status=FeasibilityStatus.MONETARY_IMBALANCE,
            message="Reconciliation allocations cannot be empty",
        )

    # 1. Allocation shapes (no duplicates)
    bank_ids = [a.bank_item_id for a in bank_allocations]
    if len(set(bank_ids)) != len(bank_ids):
        return CandidateFeasibilityResult(
            status=FeasibilityStatus.DUPLICATE_ALLOCATION,
            message=f"Duplicate BankItem IDs in candidate: {bank_ids}",
        )

    book_ids = [a.book_item_id for a in book_allocations]
    if len(set(book_ids)) != len(book_ids):
        return CandidateFeasibilityResult(
            status=FeasibilityStatus.DUPLICATE_ALLOCATION,
            message=f"Duplicate BookItem IDs in candidate: {book_ids}",
        )

    # 2. Monetary conservation
    bank_total = sum(a.amount_int for a in bank_allocations)
    book_total = sum(a.amount_int for a in book_allocations)
    if bank_total <= 0 or book_total <= 0:
        return CandidateFeasibilityResult(
            status=FeasibilityStatus.MONETARY_IMBALANCE,
            message="Reconciliation allocations must have positive monetary values",
        )
    if bank_total != book_total:
        return CandidateFeasibilityResult(
            status=FeasibilityStatus.MONETARY_IMBALANCE,
            message=f"Bank sum ({bank_total}) != Book sum ({book_total})",
        )

    # 3. Item presence in view
    bank_items = {}
    for a in bank_allocations:
        b_item = view.get_bank_item(a.bank_item_id)
        if b_item is None:
            return CandidateFeasibilityResult(
                status=FeasibilityStatus.UNKNOWN_ITEM,
                message=f"BankItem {a.bank_item_id!r} is unknown or fully consumed in current view",
            )
        bank_items[b_item.bank_item_id] = b_item

    book_items = {}
    for a in book_allocations:
        j_item = view.get_book_item(a.book_item_id)
        if j_item is None:
            return CandidateFeasibilityResult(
                status=FeasibilityStatus.UNKNOWN_ITEM,
                message=f"BookItem {a.book_item_id!r} is unknown or fully consumed in current view",
            )
        book_items[j_item.book_item_id] = j_item

    # 4. Capacity bounds
    for a in bank_allocations:
        b_item = bank_items[a.bank_item_id]
        if a.amount_int > b_item.remaining_amount_int:
            return CandidateFeasibilityResult(
                status=FeasibilityStatus.CAPACITY_EXCEEDED,
                message=(
                    f"BankItem {a.bank_item_id} allocated {a.amount_int} "
                    f"exceeds remaining capacity {b_item.remaining_amount_int}"
                ),
            )

    for a in book_allocations:
        j_item = book_items[a.book_item_id]
        if a.amount_int > j_item.remaining_amount_int:
            return CandidateFeasibilityResult(
                status=FeasibilityStatus.CAPACITY_EXCEEDED,
                message=(
                    f"BookItem {a.book_item_id} allocated {a.amount_int} "
                    f"exceeds remaining capacity {j_item.remaining_amount_int}"
                ),
            )

    # 5. Partial policies
    if not view.config.allow_partial_bank:
        for a in bank_allocations:
            b_item = bank_items[a.bank_item_id]
            if a.amount_int != b_item.original_amount_int:
                return CandidateFeasibilityResult(
                    status=FeasibilityStatus.PARTIAL_BANK_FORBIDDEN,
                    message=(
                        f"BankItem {a.bank_item_id} allocated {a.amount_int} "
                        f"but original amount is {b_item.original_amount_int}"
                    ),
                )

    if not view.config.allow_partial_book:
        for a in book_allocations:
            j_item = book_items[a.book_item_id]
            if a.amount_int != j_item.original_amount_int:
                return CandidateFeasibilityResult(
                    status=FeasibilityStatus.PARTIAL_BOOK_FORBIDDEN,
                    message=(
                        f"BookItem {a.book_item_id} allocated {a.amount_int} "
                        f"but original amount is {j_item.original_amount_int}"
                    ),
                )

    # 6. Currency uniformity
    if view.config.require_exact_currency_match:
        currencies = {b.currency for b in bank_items.values()} | {
            j.currency for j in book_items.values()
        }
        if len(currencies) > 1:
            return CandidateFeasibilityResult(
                status=FeasibilityStatus.CURRENCY_MISMATCH,
                message=f"Reconciliation mixes currencies: {sorted(currencies)}",
            )

    # 7. Direction compatibility
    for b_item in bank_items.values():
        for j_item in book_items.values():
            pair = (b_item.direction, j_item.direction)
            if pair not in DIRECTION_COMPATIBILITY_MAP:
                return CandidateFeasibilityResult(
                    status=FeasibilityStatus.DIRECTION_MISMATCH,
                    message=(
                        f"Direction mismatch: BankItem {b_item.bank_item_id} ({b_item.direction}) "
                        f"incompatible with BookItem {j_item.book_item_id} ({j_item.direction})"
                    ),
                )

    # 8. Routing consistency
    bank_account_ids = {b.bank_account_id for b in bank_items.values()}
    for j_item in book_items.values():
        if j_item.routed_bank_account_id is not None:
            if bank_account_ids != {j_item.routed_bank_account_id}:
                return CandidateFeasibilityResult(
                    status=FeasibilityStatus.ROUTING_CONTRADICTION,
                    message=(
                        f"BookItem {j_item.book_item_id} is routed to bank account "
                        f"{j_item.routed_bank_account_id!r}, but candidate references "
                        f"bank account(s) {sorted(bank_account_ids)!r}"
                    ),
                )

    # 9. Date window
    effective_window = (
        max_date_distance_days
        if max_date_distance_days is not None
        else view.config.max_date_distance_days
    )
    if effective_window is not None:
        for b_item in bank_items.values():
            for j_item in book_items.values():
                distance = abs((b_item.date - j_item.date).days)
                if distance > effective_window:
                    return CandidateFeasibilityResult(
                        status=FeasibilityStatus.DATE_WINDOW_EXCEEDED,
                        message=(
                            f"Date distance ({distance} days) between BankItem {b_item.bank_item_id} "
                            f"and BookItem {j_item.book_item_id} exceeds limit {effective_window} days"
                        ),
                    )

    return CandidateFeasibilityResult(status=FeasibilityStatus.FEASIBLE)
