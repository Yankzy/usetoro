from __future__ import annotations

from bookkeeping_state.dag.view import build_dag_view
from bookkeeping_state.domain.counterparties import Counterparty
from bookkeeping_state.domain.enums import Direction
from bookkeeping_state.domain.routing import RoutingDecisionSource
from bookkeeping_state.hydration.hydrator import BookkeepingHydrator
from bookkeeping_state_eval.persistence.in_memory import (
    InMemoryBookkeepingRepository,
)
from bookkeeping_state.persistence.repository import (
    BookkeepingSnapshot,
)
from bookkeeping_state.state.queries import BookkeepingQueries
from tests.factories import (
    FIXED_TIME,
    account,
    bank_item,
    book_item,
    classification,
    context,
    routing,
)


def _setup_world(
    *,
    books: tuple = (),
    classifications: tuple = (),
    routings: tuple = (),
    counterparties: tuple = (),
):
    account_obj = account("acc-1")
    snap = BookkeepingSnapshot(
        persistence_revision=0,
        context=context(),
        bank_accounts=(account_obj,),
        bank_items=(bank_item("bank-1", account_id="acc-1"),),
        book_items=books,
        classifications=classifications,
        routing_decisions=routings,
        counterparties=counterparties,
    )
    repo = InMemoryBookkeepingRepository(initial_snapshots=[snap])
    hydrator = BookkeepingHydrator(
        repository=repo,
        clock=lambda: FIXED_TIME,
        session_id_factory=lambda: "session-dag-view",
    )
    live = hydrator.hydrate(company_id="atlas")
    queries = BookkeepingQueries(live)
    return live, queries


class TestDagViews:
    def test_build_dag_view_unclassified_items(self):
        from datetime import date
        from bookkeeping_state.domain.books import BookItem

        b1 = BookItem(
            id="book-1",
            origin_period="2026-01",
            date=date(2026, 1, 15),
            amount_units="500000",
            direction=Direction.BOOK_BANK_CREDIT,
            currency="MAD",
            description="Office Supplies",
            counterparty_id="cp-1",
        )
        b2 = BookItem(
            id="book-2",
            origin_period="2026-01",
            date=date(2026, 1, 15),
            amount_units="1200000",
            direction=Direction.BOOK_BANK_DEBIT,
            currency="MAD",
            description="Monthly Rent",
        )
        from bookkeeping_state.domain.counterparties import CounterpartyType

        cp1 = Counterparty(id="cp-1", name="Staples Morocco", counterparty_type=CounterpartyType.SUPPLIER)

        route_b1 = routing("route-1", book_item_id="book-1", account_id="acc-1")

        live, queries = _setup_world(
            books=(b1, b2),
            counterparties=(cp1,),
            routings=(route_b1,),
        )

        view = build_dag_view(queries)
        assert view.session_id == live.session_id
        assert view.state_revision == 0
        assert len(view.items) == 2
        assert view.unclassified_count == 2
        assert view.classified_count == 0

        item1 = view.get_item("book-1")
        assert item1 is not None
        assert item1.amount == 500_000
        assert item1.description == "Office Supplies"
        assert item1.counterparty_id == "cp-1"
        assert item1.counterparty_name == "Staples Morocco"
        assert item1.active_bank_account_id == "acc-1"
        assert not item1.is_classified

        item2 = view.get_item("book-2")
        assert item2 is not None
        assert item2.amount == 1_200_000
        assert item2.active_bank_account_id is None
        assert not item2.is_classified

    def test_build_dag_view_excludes_already_classified_by_default(self):
        b1 = book_item("book-1", amount=500_000)
        b2 = book_item("book-2", amount=800_000)
        cls_b1 = classification("cls-1", book_item_id="book-1", account_code="6125")

        live, queries = _setup_world(
            books=(b1, b2),
            classifications=(cls_b1,),
        )

        # Default excludes classified
        view = build_dag_view(queries)
        assert len(view.items) == 1
        assert view.items[0].book_item_id == "book-2"
        assert view.unclassified_count == 1
        assert view.classified_count == 0

        # Including classified
        view_all = build_dag_view(queries, include_already_classified=True)
        assert len(view_all.items) == 2
        assert view_all.unclassified_count == 1
        assert view_all.classified_count == 1

        classified_item = view_all.get_item("book-1")
        assert classified_item is not None
        assert classified_item.is_classified
        assert classified_item.existing_classification_id == "cls-1"
        assert classified_item.existing_account_code == "6125"
