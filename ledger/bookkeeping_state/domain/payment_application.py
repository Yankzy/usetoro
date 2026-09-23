from __future__ import annotations

from datetime import datetime
from typing import Annotated

from pydantic import (
    BaseModel,
    ConfigDict,
    Field,
    StringConstraints,
    field_validator,
    model_validator,
)

from bookkeeping_state.domain.enums import Direction
from bookkeeping_state.domain.money import (
    AmountUnits,
    amount_units_to_int,
)

Identifier = Annotated[
    str,
    StringConstraints(
        min_length=1,
        max_length=255,
        strip_whitespace=True,
    ),
]


class ExecutedPaymentAllocation(BaseModel):
    """
    Immutable detached allocation leg of an accepted Stage-1 payment application.

    Links the settled obligation (Invoice or Bill) to the resulting real posted
    cash TransactionModel created in the accounting kernel.
    """

    model_config = ConfigDict(
        frozen=True,
        extra="forbid",
    )

    obligation_book_item_id: Identifier
    cash_transaction_book_item_id: Identifier
    amount_units: AmountUnits

    @property
    def amount_int(self) -> int:
        return amount_units_to_int(self.amount_units)


class ExecutedPaymentApplication(BaseModel):
    """
    Immutable detached record of an accepted Stage-1 payment application.

    Records the durable provenance connecting an external BankItem to the
    resulting accounting settlement and cash transaction legs.
    """

    model_config = ConfigDict(
        frozen=True,
        extra="forbid",
    )

    id: Identifier
    company_id: Identifier
    bank_item_id: Identifier
    direction: Direction
    currency: str = Field(..., min_length=3, max_length=3)
    total_amount_units: AmountUnits
    allocations: tuple[ExecutedPaymentAllocation, ...] = Field(..., min_length=1)

    session_id: Identifier | None = None
    state_revision_at_creation: int = Field(..., ge=0)
    created_at: datetime

    @field_validator("currency")
    @classmethod
    def normalize_currency(cls, value: str) -> str:
        return value.upper()

    @field_validator("created_at")
    @classmethod
    def require_timezone_aware_created_at(cls, value: datetime) -> datetime:
        if value.tzinfo is None or value.utcoffset() is None:
            raise ValueError("created_at must be timezone-aware")
        return value

    @property
    def total_amount_int(self) -> int:
        return amount_units_to_int(self.total_amount_units)

    @model_validator(mode="after")
    def validate_allocations_sum(self) -> "ExecutedPaymentApplication":
        alloc_sum = sum(alloc.amount_int for alloc in self.allocations)
        if alloc_sum != self.total_amount_int:
            raise ValueError(
                f"ExecutedPaymentApplication total ({self.total_amount_int}) "
                f"!= sum of allocations ({alloc_sum})"
            )
        return self
