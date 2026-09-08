from __future__ import annotations

from dataclasses import dataclass
from datetime import date
from typing import Sequence

from bookkeeping_state.domain.books import BookItem
from bookkeeping_state.domain.enums import Direction
from bookkeeping_state.domain.money import amount_units_to_int
from bookkeeping_state.state.queries import BookkeepingQueries


@dataclass(frozen=True, slots=True)
class DagViewItem:
    """
    Bounded, read-only item presented to Go ASE DAG nodes or classifiers.
    """

    book_item_id: str
    date: date
    amount: int
    currency: str
    direction: Direction
    description: str | None = None
    counterparty_id: str | None = None
    counterparty_name: str | None = None
    reference: str | None = None
    active_bank_account_id: str | None = None
    existing_classification_id: str | None = None
    existing_account_code: str | None = None

    @property
    def is_classified(self) -> bool:
        return self.existing_classification_id is not None


@dataclass(frozen=True, slots=True)
class DagView:
    """
    Bounded projection of bookkeeping state exposed for DAG classification.
    """

    session_id: str
    state_revision: int
    items: tuple[DagViewItem, ...] = ()

    @property
    def unclassified_count(self) -> int:
        return sum(1 for item in self.items if not item.is_classified)

    @property
    def classified_count(self) -> int:
        return sum(1 for item in self.items if item.is_classified)

    def get_item(self, book_item_id: str) -> DagViewItem | None:
        for item in self.items:
            if item.book_item_id == book_item_id:
                return item
        return None


def build_dag_view(
    queries: BookkeepingQueries,
    *,
    include_already_classified: bool = False,
) -> DagView:
    """
    Construct a bounded DagView from current BookkeepingQueries.
    """
    if include_already_classified:
        book_items: Sequence[BookItem] = tuple(queries._state.book_items.values())
    else:
        book_items = queries.unclassified_book_items()

    view_items: list[DagViewItem] = []

    for book_item in book_items:
        route = queries.active_route(book_item.id)
        active_bank_account_id = route.bank_account_id if route else None

        existing_cls = queries.active_classification(book_item.id)
        existing_cls_id = existing_cls.id if existing_cls else None
        existing_acc = existing_cls.account_code if existing_cls else None

        cp = queries.effective_counterparty(book_item.id)
        cp_id = cp.id if cp else None
        cp_name = cp.name if cp else None

        view_items.append(
            DagViewItem(
                book_item_id=book_item.id,
                date=book_item.date,
                amount=amount_units_to_int(book_item.amount_units),
                currency=book_item.currency,
                direction=book_item.direction,
                description=queries.effective_description(book_item.id),
                counterparty_id=cp_id,
                counterparty_name=cp_name,
                reference=queries.effective_reference(book_item.id),
                active_bank_account_id=active_bank_account_id,
                existing_classification_id=existing_cls_id,
                existing_account_code=existing_acc,
            )
        )

    return DagView(
        session_id=queries.session_id,
        state_revision=queries.state_revision,
        items=tuple(view_items),
    )
