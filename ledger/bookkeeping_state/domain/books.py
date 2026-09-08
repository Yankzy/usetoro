from __future__ import annotations

from datetime import date
from typing import Annotated

from pydantic import (
    BaseModel,
    ConfigDict,
    Field,
    StringConstraints,
    field_validator,
    model_validator,
)

from .enums import Direction, SourceType
from .money import AmountUnits, amount_units_to_int


Identifier = Annotated[
    str,
    StringConstraints(strip_whitespace=True, min_length=1, max_length=255),
]

CurrencyCode = Annotated[
    str,
    StringConstraints(
        strip_whitespace=True,
        min_length=3,
        max_length=3,
        pattern=r"^[A-Za-z]{3}$",
    ),
]

AccountingPeriod = Annotated[
    str,
    StringConstraints(
        pattern=r"^[0-9]{4}-(0[1-9]|1[0-2])$",
    ),
]


class BookItem(BaseModel):
    """
    Canonical durable book-side economic artifact.

    A BookItem is an economic item that can eventually participate in routing,
    categorization, journal construction, and reconciliation.

    Crucially, this model stores the ORIGINAL amount, not the remaining
    reconciliation amount.

    Remaining reconciliation capacity is derived by BookkeepingState from:

        original amount
        minus
        active reconciliation allocations

    This prevents derived reconciliation state from becoming duplicated
    authoritative truth.
    """

    model_config = ConfigDict(
        frozen=True,
        extra="forbid",
        validate_assignment=True,
    )

    id: Identifier

    source_type: SourceType = Field(
        default=SourceType.STAGING_BOOK_ITEM,
    )

    origin_period: AccountingPeriod = Field(
        ...,
        description=(
            "Accounting period associated with the item. This may intentionally "
            "differ from the calendar month of the underlying economic event."
        ),
    )

    date: date

    amount_units: AmountUnits = Field(
        ...,
        description=(
            "Original absolute book-side amount in exact solver units. "
            "This value does not decrease when reconciliations are created."
        ),
    )

    direction: Direction

    currency: CurrencyCode

    description: str | None = Field(
        default=None,
        max_length=2_000,
        description=(
            "Human-readable description of the economic event when available."
        ),
    )

    counterparty_id: Identifier | None = None

    reference: str | None = Field(
        default=None,
        max_length=500,
    )

    provenance_refs: tuple[Identifier, ...] = Field(
        default_factory=tuple,
        description=(
            "IDs of durable source/evidence artifacts from which this item "
            "originated, such as invoices, receipts, cheques, or opening-state "
            "records."
        ),
    )

    @field_validator("currency")
    @classmethod
    def normalize_currency(cls, value: str) -> str:
        return value.upper()

    @model_validator(mode="after")
    def validate_book_semantics(self) -> "BookItem":
        valid_source_types = {
            SourceType.STAGING_BOOK_ITEM,
            SourceType.POSTED_BOOK_ITEM,
            SourceType.OPENING_STATE_ITEM,
        }

        if self.source_type not in valid_source_types:
            raise ValueError(
                f"BookItem {self.id!r} cannot use source_type "
                f"{self.source_type!r}"
            )

        valid_directions = {
            Direction.BOOK_BANK_DEBIT,
            Direction.BOOK_BANK_CREDIT,
            Direction.INFLOW,
            Direction.OUTFLOW,
        }

        if self.direction not in valid_directions:
            raise ValueError(
                f"BookItem {self.id!r} cannot use bank-side direction "
                f"{self.direction!r}"
            )

        return self

    @property
    def amount_int(self) -> int:
        """
        Original exact amount.

        Do not interpret this as remaining reconciliation capacity.
        """
        return amount_units_to_int(self.amount_units)