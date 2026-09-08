from __future__ import annotations

from datetime import date
from typing import Annotated

from pydantic import (
    BaseModel,
    ConfigDict,
    Field,
    StringConstraints,
    model_validator,
)

from bookkeeping_state.domain.enums import Direction
from bookkeeping_state.domain.money import (
    AmountUnits,
    ResidualAmountUnits,
    amount_units_to_int,
)
from bookkeeping_state.state.queries import BookkeepingQueries

Identifier = Annotated[
    str,
    StringConstraints(
        min_length=1,
        strip_whitespace=True,
    ),
]


class ReconciliationBankItemView(BaseModel):
    """
    Minimal BankItem projection exposed to the reconciliation subsystem.

    remaining_amount_units is derived dynamically from active reconciliations.
    Fully consumed items are excluded prior to view creation.
    """

    model_config = ConfigDict(
        frozen=True,
        extra="forbid",
    )

    bank_item_id: Identifier
    bank_account_id: Identifier
    date: date
    original_amount_units: AmountUnits
    remaining_amount_units: ResidualAmountUnits
    direction: Direction
    currency: str = Field(..., min_length=3, max_length=3)
    description: str | None = None
    reference: str | None = None

    @property
    def original_amount_int(self) -> int:
        return amount_units_to_int(self.original_amount_units)

    @property
    def remaining_amount_int(self) -> int:
        return int(self.remaining_amount_units)

    @model_validator(mode="after")
    def validate_bank_view(self) -> "ReconciliationBankItemView":
        if self.remaining_amount_int <= 0:
            raise ValueError(
                f"ReconciliationBankItemView requires positive remaining amount, got {self.remaining_amount_int}"
            )
        if self.remaining_amount_int > self.original_amount_int:
            raise ValueError(
                f"ReconciliationBankItemView remaining ({self.remaining_amount_int}) exceeds original ({self.original_amount_int})"
            )
        return self


class ReconciliationBookItemView(BaseModel):
    """
    Minimal BookItem projection exposed to the reconciliation subsystem.

    Contains derived remaining amount, and any active routing constraint.
    """

    model_config = ConfigDict(
        frozen=True,
        extra="forbid",
    )

    book_item_id: Identifier
    date: date
    original_amount_units: AmountUnits
    remaining_amount_units: ResidualAmountUnits
    direction: Direction
    currency: str = Field(..., min_length=3, max_length=3)
    routed_bank_account_id: Identifier | None = None
    description: str | None = None
    reference: str | None = None
    counterparty_id: Identifier | None = None
    counterparty_name: str | None = None
    evidence_document_ids: tuple[Identifier, ...] = ()

    @property
    def original_amount_int(self) -> int:
        return amount_units_to_int(self.original_amount_units)

    @property
    def remaining_amount_int(self) -> int:
        return int(self.remaining_amount_units)

    @model_validator(mode="after")
    def validate_book_view(self) -> "ReconciliationBookItemView":
        if self.remaining_amount_int <= 0:
            raise ValueError(
                f"ReconciliationBookItemView requires positive remaining amount, got {self.remaining_amount_int}"
            )
        if self.remaining_amount_int > self.original_amount_int:
            raise ValueError(
                f"ReconciliationBookItemView remaining ({self.remaining_amount_int}) exceeds original ({self.original_amount_int})"
            )
        return self


class ReconciliationViewConfig(BaseModel):
    """
    Configuration parameters for reconciliation feasibility and candidate generation.
    """

    model_config = ConfigDict(
        frozen=True,
        extra="forbid",
    )

    allow_partial_bank: bool = False
    allow_partial_book: bool = True
    require_exact_currency_match: bool = True
    max_date_distance_days: int | None = None
    auto_reconcile_unique_inferred_allocation: bool = True


class ReconciliationView(BaseModel):
    """
    Immutable bounded projection of BookkeepingState for reconciliation.

    Reconciliation algorithms operate solely on this view and cannot mutate state.
    """

    model_config = ConfigDict(
        frozen=True,
        extra="forbid",
    )

    state_revision: int = Field(..., ge=0)
    session_id: Identifier
    bank_items: tuple[ReconciliationBankItemView, ...] = ()
    book_items: tuple[ReconciliationBookItemView, ...] = ()
    config: ReconciliationViewConfig = Field(default_factory=ReconciliationViewConfig)

    def get_bank_item(self, bank_item_id: str) -> ReconciliationBankItemView | None:
        for item in self.bank_items:
            if item.bank_item_id == bank_item_id:
                return item
        return None

    def get_book_item(self, book_item_id: str) -> ReconciliationBookItemView | None:
        for item in self.book_items:
            if item.book_item_id == book_item_id:
                return item
        return None

    def bank_items_for_account(
        self, bank_account_id: str
    ) -> tuple[ReconciliationBankItemView, ...]:
        return tuple(
            item for item in self.bank_items if item.bank_account_id == bank_account_id
        )

    @property
    def total_bank_remaining_int(self) -> int:
        return sum(item.remaining_amount_int for item in self.bank_items)

    @property
    def total_book_remaining_int(self) -> int:
        return sum(item.remaining_amount_int for item in self.book_items)


def build_reconciliation_view(
    queries: BookkeepingQueries,
    *,
    config: ReconciliationViewConfig | None = None,
) -> ReconciliationView:
    """
    Construct the bounded immutable ReconciliationView from BookkeepingQueries.

    Filters out any BankItems or BookItems that are already fully consumed (remaining <= 0).
    Captures active routing decisions for each BookItem.
    """
    derived = queries.derived

    # Resolve configuration from accounting policy if not explicitly overridden
    if config is None:
        policy = queries.context.policy
        resolved_config = ReconciliationViewConfig(
            allow_partial_bank=policy.allow_partial_bank_reconciliation,
            allow_partial_book=policy.allow_partial_book_reconciliation,
            require_exact_currency_match=policy.require_exact_currency_match,
            max_date_distance_days=policy.reconciliation_date_window_days,
            auto_reconcile_unique_inferred_allocation=policy.auto_reconcile_unique_inferred_allocation,
        )
    else:
        resolved_config = config

    # Bank items: collected across all bank accounts
    bank_views: list[ReconciliationBankItemView] = []
    for bank_account in queries.bank_accounts():
        for bank_item in queries.bank_items_for_account(bank_account.id):
            remaining = derived.bank_remaining(bank_item.id)
            if remaining <= 0:
                continue

            bank_views.append(
                ReconciliationBankItemView(
                    bank_item_id=bank_item.id,
                    bank_account_id=bank_item.bank_account_id,
                    date=bank_item.date,
                    original_amount_units=bank_item.amount_units,
                    remaining_amount_units=str(remaining),
                    direction=bank_item.direction,
                    currency=bank_item.currency,
                    description=bank_item.description,
                    reference=bank_item.reference,
                )
            )

    # Book items: collected across state, capturing routing and counterparties
    book_views: list[ReconciliationBookItemView] = []
    for book_item in sorted(queries._state.book_items.values(), key=lambda b: (b.date, b.id)):
        remaining = derived.book_remaining(book_item.id)
        if remaining <= 0:
            continue

        active_route = queries.active_route(book_item.id)
        routed_account_id = (
            active_route.bank_account_id if active_route is not None else None
        )

        cp = queries.effective_counterparty(book_item.id)
        cp_id = cp.id if cp is not None else None
        cp_name = cp.name if cp is not None else None
        evidence_docs = tuple(
            doc.id for doc in queries.effective_evidence_documents(book_item.id)
        )

        book_views.append(
            ReconciliationBookItemView(
                book_item_id=book_item.id,
                date=book_item.date,
                original_amount_units=book_item.amount_units,
                remaining_amount_units=str(remaining),
                direction=book_item.direction,
                currency=book_item.currency,
                routed_bank_account_id=routed_account_id,
                description=queries.effective_description(book_item.id),
                reference=queries.effective_reference(book_item.id),
                counterparty_id=cp_id,
                counterparty_name=cp_name,
                evidence_document_ids=evidence_docs,
            )
        )

    bank_views.sort(key=lambda item: (item.date, item.bank_item_id))
    book_views.sort(key=lambda item: (item.date, item.book_item_id))

    return ReconciliationView(
        state_revision=queries.state_revision,
        session_id=queries.session_id,
        bank_items=tuple(bank_views),
        book_items=tuple(book_views),
        config=resolved_config,
    )
