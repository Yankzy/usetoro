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
    amount_units represents exact 4-decimal integer AmountUnits (scale 10,000).
    """

    book_item_id: str
    date: date
    amount_units: int
    currency: str
    direction: Direction
    description: str | None = None
    counterparty_id: str | None = None
    counterparty_name: str | None = None
    reference: str | None = None
    active_bank_account_id: str | None = None
    existing_classification_id: str | None = None
    existing_account_code: str | None = None
    evidence_refs: tuple[str, ...] = ()
    safe_evidence_summaries: tuple[str, ...] = ()
    source_artifact_kind: str | None = None
    bookkeeping_role: str | None = None

    def __init__(
        self,
        book_item_id: str,
        date: date,
        amount_units: int | None = None,
        currency: str = "",
        direction: Direction = Direction.OUTFLOW,
        description: str | None = None,
        counterparty_id: str | None = None,
        counterparty_name: str | None = None,
        reference: str | None = None,
        active_bank_account_id: str | None = None,
        existing_classification_id: str | None = None,
        existing_account_code: str | None = None,
        evidence_refs: tuple[str, ...] = (),
        safe_evidence_summaries: tuple[str, ...] = (),
        source_artifact_kind: str | None = None,
        bookkeeping_role: str | None = None,
        *,
        amount: int | None = None,
    ) -> None:
        resolved_amount = (
            amount_units
            if amount_units is not None
            else (amount if amount is not None else 0)
        )
        object.__setattr__(self, "book_item_id", book_item_id)
        object.__setattr__(self, "date", date)
        object.__setattr__(self, "amount_units", resolved_amount)
        object.__setattr__(self, "currency", currency)
        object.__setattr__(self, "direction", direction)
        object.__setattr__(self, "description", description)
        object.__setattr__(self, "counterparty_id", counterparty_id)
        object.__setattr__(self, "counterparty_name", counterparty_name)
        object.__setattr__(self, "reference", reference)
        object.__setattr__(self, "active_bank_account_id", active_bank_account_id)
        object.__setattr__(
            self, "existing_classification_id", existing_classification_id
        )
        object.__setattr__(self, "existing_account_code", existing_account_code)
        object.__setattr__(self, "evidence_refs", tuple(evidence_refs))
        object.__setattr__(
            self, "safe_evidence_summaries", tuple(safe_evidence_summaries)
        )
        object.__setattr__(self, "source_artifact_kind", source_artifact_kind)
        object.__setattr__(self, "bookkeeping_role", bookkeeping_role)

    @property
    def amount(self) -> int:
        return self.amount_units

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
    company_id: str = ""
    persistence_revision: int = 0

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

        evidence_docs = queries.effective_evidence_documents(book_item.id)
        evidence_refs = tuple(d.id for d in evidence_docs)
        safe_summaries = tuple(
            f"Doc:{d.id}:{getattr(d.document_type, 'value', str(d.document_type))}"
            for d in evidence_docs
        )

        kind_str = (
            book_item.source_artifact_kind.value
            if hasattr(book_item.source_artifact_kind, "value")
            else (str(book_item.source_artifact_kind) if book_item.source_artifact_kind else None)
        )
        role_str = (
            book_item.bookkeeping_role.value
            if hasattr(book_item.bookkeeping_role, "value")
            else (str(book_item.bookkeeping_role) if book_item.bookkeeping_role else None)
        )

        view_items.append(
            DagViewItem(
                book_item_id=book_item.id,
                date=book_item.date,
                amount_units=amount_units_to_int(book_item.amount_units),
                currency=book_item.currency,
                direction=book_item.direction,
                description=queries.effective_description(book_item.id),
                counterparty_id=cp_id,
                counterparty_name=cp_name,
                reference=queries.effective_reference(book_item.id),
                active_bank_account_id=active_bank_account_id,
                existing_classification_id=existing_cls_id,
                existing_account_code=existing_acc,
                evidence_refs=evidence_refs,
                safe_evidence_summaries=safe_summaries,
                source_artifact_kind=kind_str,
                bookkeeping_role=role_str,
            )
        )

    return DagView(
        session_id=queries.session_id,
        state_revision=queries.state_revision,
        items=tuple(view_items),
        company_id=queries.company_id,
        persistence_revision=queries.persistence_revision,
    )


# ----------------------------------------------------------------------
# Residual Bank Categorization View (Bank-side movements)
# ----------------------------------------------------------------------
from bookkeeping_state.bank_categorization.view import (
    ResidualBankCategorizationItem,
    ResidualBankCategorizationView,
    build_residual_bank_categorization_view,
)


