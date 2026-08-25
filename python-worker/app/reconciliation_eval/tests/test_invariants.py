"""Property-based checks for CP-SAT reconciliation invariants."""

from datetime import date

from hypothesis import given, settings, strategies as st

from domain.bank import BankItem
from domain.base import Direction, SourceType
from domain.books import BookItem
from domain.hypothesis import BankAllocation, BookAllocation, ReconciliationHypothesis
from optimizer.cp_sat import solve_reconciliation
from optimizer.protocol import OptimizerRequest, SolverOptions


@settings(max_examples=50, deadline=None)
@given(
    bank_amounts=st.lists(st.integers(min_value=1, max_value=1_000), min_size=1, max_size=5),
    book_amounts=st.lists(st.integers(min_value=1, max_value=1_000), min_size=1, max_size=4),
)
def test_cp_sat_capacity_invariants(bank_amounts: list[int], book_amounts: list[int]):
    """An optimal result must never consume more than a book item's capacity."""
    bank_items = [
        BankItem(
            id=f"B{index}",
            date=date(2026, 8, 1),
            amount_units=str(amount),
            direction=Direction.BANK_OUTFLOW,
            currency="MAD",
            description="Generated bank movement",
        )
        for index, amount in enumerate(bank_amounts)
    ]
    book_items = [
        BookItem(
            id=f"J{index}",
            source_type=SourceType.POSTED_BOOK_ITEM,
            origin_period="2026-08",
            date=date(2026, 8, 1),
            remaining_amount_units=str(amount),
            direction=Direction.BOOK_BANK_CREDIT,
            currency="MAD",
        )
        for index, amount in enumerate(book_amounts)
    ]
    hypotheses = [
        ReconciliationHypothesis(
            id=f"H{bank_index}_{book_index}",
            utility=1_000 - bank_index - book_index,
            bank_allocations=[
                BankAllocation(bank_item_id=bank.id, amount_units=bank.amount_units)
            ],
            book_allocations=[
                BookAllocation(book_item_id=book.id, amount_units=bank.amount_units)
            ],
        )
        for bank_index, bank in enumerate(bank_items)
        for book_index, book in enumerate(book_items)
        if bank.amount_int <= book.remaining_amount_int
    ]
    request = OptimizerRequest(
        problem_id="property_capacity",
        currency="MAD",
        bank_items=bank_items,
        book_items=book_items,
        hypotheses=hypotheses,
        solver_options=SolverOptions(max_solve_seconds=1),
    )

    response = solve_reconciliation(request)

    if response.status == "OPTIMAL":
        consumption = {book.id: 0 for book in book_items}
        for selected in response.selected_hypotheses:
            for allocation in selected["book_allocations"]:
                consumption[allocation["book_item_id"]] += int(allocation["amount_units"])

        assert all(
            consumption[book.id] <= book.remaining_amount_int for book in book_items
        )
