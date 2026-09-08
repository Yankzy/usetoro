from __future__ import annotations

from bookkeeping_state.state.derived import build_derived_state
from tests.factories import (
    account,
    bank_item,
    book_item,
    classification,
    classification_invalidation,
    reconciliation,
    reconciliation_invalidation,
    routing,
    routing_invalidation,
    state,
)


def test_derived_state_resolves_active_artifacts_and_exact_residuals() -> None:
    """Residual capacity is rebuilt from immutable reconciliation artifacts."""
    original_book = book_item("book-1", amount=1_500_000)
    live = state(
        bank_accounts=(account(),),
        bank_items=(bank_item("bank-1"), bank_item("bank-2", amount=300_000)),
        book_items=(original_book, book_item("book-2", amount=300_000)),
        routing_decisions=(routing(),),
        classifications=(classification(),),
        reconciliations=(
            reconciliation("rec-1", bank=(("bank-1", 1_000_000),), book=(("book-1", 1_000_000),)),
            reconciliation("rec-2", bank=(("bank-2", 300_000),), book=(("book-1", 300_000),)),
        ),
    )

    derived = build_derived_state(live)

    assert derived.active_routing_by_book_item["book-1"].id == "route-1"
    assert derived.active_classification_by_book_item["book-1"].id == "classification-1"
    assert set(derived.active_reconciliations) == {"rec-1", "rec-2"}
    assert derived.bank_allocated_units == {"bank-1": 1_000_000, "bank-2": 300_000}
    assert derived.book_allocated_units == {"book-1": 1_300_000}
    assert derived.book_remaining("book-1") == 200_000
    assert derived.fully_reconciled_bank_item_ids == ("bank-1", "bank-2")
    assert derived.partially_reconciled_book_item_ids == ("book-1",)
    assert derived.unreconciled_book_item_ids == ("book-2",)
    assert derived.book_items_without_active_route == ("book-2",)
    assert derived.book_items_without_active_classification == ("book-2",)
    # The authoritative observation never carries a mutable remaining amount.
    assert original_book.amount_int == 1_500_000


def test_supersession_and_invalidation_are_non_resurrecting() -> None:
    live = state(
        bank_accounts=(account(),),
        book_items=(book_item(),),
        routing_decisions=(routing("route-1"), routing("route-2", supersedes="route-1")),
        routing_invalidations=(routing_invalidation(target="route-2"),),
        classifications=(
            classification("class-1"),
            classification("class-2", supersedes="class-1"),
        ),
        classification_invalidations=(classification_invalidation(target="class-2"),),
        reconciliations=(reconciliation(),),
        reconciliation_invalidations=(reconciliation_invalidation(),),
    )

    derived = build_derived_state(live)

    assert "book-1" not in derived.active_routing_by_book_item
    assert "book-1" not in derived.active_classification_by_book_item
    assert derived.active_reconciliations == {}
    assert derived.book_items_without_active_route == ("book-1",)
    assert derived.book_items_without_active_classification == ("book-1",)


def test_derived_calculation_is_order_independent_and_does_not_mutate_artifacts() -> None:
    artifacts = dict(
        bank_accounts=(account(),),
        bank_items=(bank_item("bank-1"), bank_item("bank-2", amount=300_000)),
        book_items=(book_item("book-1", amount=1_300_000),),
        reconciliations=(
            reconciliation("rec-a", bank=(("bank-1", 1_000_000),), book=(("book-1", 1_000_000),)),
            reconciliation("rec-b", bank=(("bank-2", 300_000),), book=(("book-1", 300_000),)),
        ),
    )
    first = state(**artifacts)
    second = state(
        bank_accounts=tuple(reversed(artifacts["bank_accounts"])),
        bank_items=tuple(reversed(artifacts["bank_items"])),
        book_items=artifacts["book_items"],
        reconciliations=tuple(reversed(artifacts["reconciliations"])),
    )

    before = tuple(first.reconciliations.values())
    assert build_derived_state(first) == build_derived_state(second)
    assert tuple(first.reconciliations.values()) == before


def test_overallocation_is_a_derived_diagnostic_not_a_mutation() -> None:
    live = state(
        bank_accounts=(account(),),
        bank_items=(bank_item(),),
        book_items=(book_item(),),
        reconciliations=(
            reconciliation(
                bank=(("bank-1", 2_000_000),),
                book=(("book-1", 2_000_000),),
            ),
        ),
    )

    derived = build_derived_state(live)

    assert derived.bank_remaining("bank-1") == -1_000_000
    assert derived.book_remaining("book-1") == -1_000_000
    assert derived.overallocated_bank_item_ids == ("bank-1",)
    assert derived.overallocated_book_item_ids == ("book-1",)
    assert live.bank_items["bank-1"].amount_int == 1_000_000
