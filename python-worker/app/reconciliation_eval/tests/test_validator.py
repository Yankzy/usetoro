"""Tests for reconciliation invariants."""

from datetime import date

from domain.bank import BankItem
from domain.base import Direction, SourceType
from domain.books import BookItem
from domain.hypothesis import BankAllocation, BookAllocation
from domain.patch import ProposedMatchGroup, ProposedState
from validation.deterministic import validate_proposal


def make_bank(amount: str = "100") -> BankItem:
    return BankItem(
        id="B1",
        date=date(2026, 8, 1),
        amount_units=amount,
        direction=Direction.BANK_OUTFLOW,
        currency="MAD",
        description="Test bank movement",
    )


def make_book(amount: str = "100") -> BookItem:
    return BookItem(
        id="J1",
        source_type=SourceType.POSTED_BOOK_ITEM,
        origin_period="2026-08",
        date=date(2026, 8, 1),
        remaining_amount_units=amount,
        direction=Direction.BOOK_BANK_CREDIT,
        currency="MAD",
    )


def make_match(bank_id: str, bank_amount: str, book_amount: str) -> ProposedMatchGroup:
    return ProposedMatchGroup(
        group_id="M1",
        bank_allocations=[BankAllocation(bank_item_id=bank_id, amount_units=bank_amount)],
        book_allocations=[BookAllocation(book_item_id="J1", amount_units=book_amount)],
    )


def test_hallucinated_bank_id():
    proposal = ProposedState(matches=[make_match("B404", "100", "100")])

    is_valid, errors = validate_proposal([make_bank()], [make_book()], proposal)

    assert not is_valid
    assert any(error.startswith("HALLUCINATED_BANK_ID") for error in errors)


def test_bank_exclusivity_violation():
    proposal = ProposedState(
        matches=[make_match("B1", "100", "100")],
        unresolved_bank_ids=["B1"],
    )

    is_valid, errors = validate_proposal([make_bank()], [make_book()], proposal)

    assert not is_valid
    assert any(error.startswith("BANK_EXCLUSIVITY_VIOLATION") for error in errors)


def test_partial_bank_consumption():
    proposal = ProposedState(matches=[make_match("B1", "50", "50")])

    is_valid, errors = validate_proposal([make_bank()], [make_book()], proposal)

    assert not is_valid
    assert any(error.startswith("PARTIAL_BANK_CONSUMPTION") for error in errors)


def test_value_conservation_violation():
    proposal = ProposedState(matches=[make_match("B1", "100", "90")])

    is_valid, errors = validate_proposal([make_bank()], [make_book()], proposal)

    assert not is_valid
    assert any(error.startswith("MONETARY_IMBALANCE") for error in errors)


def test_book_capacity_overflow():
    proposal = ProposedState(matches=[make_match("B1", "120", "120")])

    is_valid, errors = validate_proposal([make_bank("120")], [make_book("100")], proposal)

    assert not is_valid
    assert any(error.startswith("BOOK_CAPACITY_OVERFLOW") for error in errors)
