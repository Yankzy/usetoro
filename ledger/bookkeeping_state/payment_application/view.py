from __future__ import annotations

from datetime import date
from typing import Annotated, Sequence

from pydantic import (
    BaseModel,
    ConfigDict,
    Field,
    StringConstraints,
    model_validator,
)

from bookkeeping_state.domain.enums import Direction, SourceType
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


class PaymentApplicationBankItemView(BaseModel):
    """
    Minimal BankItem projection exposed to Stage 1 payment application.

    Independent from reconciliation view models.
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
    def validate_bank_view(self) -> "PaymentApplicationBankItemView":
        if self.remaining_amount_int <= 0:
            raise ValueError(
                f"PaymentApplicationBankItemView requires positive remaining amount, got {self.remaining_amount_int}"
            )
        if self.remaining_amount_int > self.original_amount_int:
            raise ValueError(
                f"PaymentApplicationBankItemView remaining ({self.remaining_amount_int}) exceeds original ({self.original_amount_int})"
            )
        return self


class PaymentApplicationObligationView(BaseModel):
    """
    Minimal open-obligation (STAGING_BOOK_ITEM) projection exposed to Stage 1.

    Represents invoice or bill settlement capacity. Strictly forbids non-staging items.
    """

    model_config = ConfigDict(
        frozen=True,
        extra="forbid",
    )

    book_item_id: Identifier
    source_type: SourceType
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
    def validate_obligation_view(self) -> "PaymentApplicationObligationView":
        if self.source_type != SourceType.STAGING_BOOK_ITEM:
            raise ValueError(
                f"PaymentApplicationObligationView requires STAGING_BOOK_ITEM, got {self.source_type.value!r}"
            )
        if self.remaining_amount_int <= 0:
            raise ValueError(
                f"PaymentApplicationObligationView requires positive remaining amount, got {self.remaining_amount_int}"
            )
        if self.remaining_amount_int > self.original_amount_int:
            raise ValueError(
                f"PaymentApplicationObligationView remaining ({self.remaining_amount_int}) exceeds original ({self.original_amount_int})"
            )
        return self


class PaymentApplicationView(BaseModel):
    """
    Immutable bounded view for Stage 1 Payment Application.
    """

    model_config = ConfigDict(
        frozen=True,
        extra="forbid",
    )

    state_revision: int = Field(..., ge=0)
    session_id: Identifier
    company_id: Identifier
    bank_items: tuple[PaymentApplicationBankItemView, ...] = ()
    obligations: tuple[PaymentApplicationObligationView, ...] = ()

    def get_bank_item(self, bank_item_id: str) -> PaymentApplicationBankItemView | None:
        for item in self.bank_items:
            if item.bank_item_id == bank_item_id:
                return item
        return None

    def get_obligation(self, book_item_id: str) -> PaymentApplicationObligationView | None:
        for item in self.obligations:
            if item.book_item_id == book_item_id:
                return item
        return None


def build_payment_application_view(
    queries: BookkeepingQueries,
    *,
    excluded_bank_item_ids: Sequence[str] = (),
) -> PaymentApplicationView:
    """
    Construct the Stage 1 PaymentApplicationView from BookkeepingQueries.

    Filters out:
    - Bank items whose ID is in excluded_bank_item_ids (e.g. claimed by Stage-2 posted-book authority)
    - Bank items that are fully consumed (remaining <= 0)
    - Book items that are NOT SourceType.STAGING_BOOK_ITEM (posted cash legs are strictly excluded)
    - Book items that are fully consumed (remaining <= 0)
    """
    derived = queries.derived
    executed_bank_ids = set(queries.executed_stage1_bank_item_ids())
    excluded_bank_set = set(excluded_bank_item_ids) | executed_bank_ids

    # 1. Bank items: eligible bank movements not claimed by posted authority
    bank_views: list[PaymentApplicationBankItemView] = []
    for bank_account in queries.bank_accounts():
        for bank_item in queries.bank_items_for_account(bank_account.id):
            if bank_item.id in excluded_bank_set:
                continue

            remaining = derived.bank_remaining(bank_item.id)
            if remaining <= 0:
                continue

            bank_views.append(
                PaymentApplicationBankItemView(
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

    # 2. Obligations: strictly STAGING_BOOK_ITEM open obligations
    obligation_views: list[PaymentApplicationObligationView] = []
    for book_item in sorted(queries._state.book_items.values(), key=lambda b: (b.date, b.id)):
        if book_item.source_type != SourceType.STAGING_BOOK_ITEM:
            continue
        if not book_item.id.lower().startswith(("invoice", "inv", "bill")):
            continue

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

        obligation_views.append(
            PaymentApplicationObligationView(
                book_item_id=book_item.id,
                source_type=book_item.source_type,
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
    obligation_views.sort(key=lambda item: (item.date, item.book_item_id))

    return PaymentApplicationView(
        state_revision=queries.state_revision,
        session_id=queries.session_id,
        company_id=queries.company_id,
        bank_items=tuple(bank_views),
        obligations=tuple(obligation_views),
    )
