from __future__ import annotations

from datetime import date, datetime
from typing import Annotated

from pydantic import (
    BaseModel,
    ConfigDict,
    Field,
    StringConstraints,
    field_validator,
    model_validator,
)


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


class AccountingPolicy(BaseModel):
    """
    Stable bookkeeping policy required to interpret the hydrated world.

    This eval starts deliberately small. Policy fields should only be added
    when an actual bookkeeping primitive requires them.
    """

    model_config = ConfigDict(
        frozen=True,
        extra="forbid",
    )

    chart_of_accounts_id: Identifier

    reconciliation_date_window_days: int = Field(
        default=45,
        ge=0,
        le=366,
    )

    require_exact_currency_match: bool = True

    allow_partial_book_reconciliation: bool = True

    allow_partial_bank_reconciliation: bool = False
    auto_reconcile_unique_inferred_allocation: bool = True


class BookkeepingContext(BaseModel):
    """
    Defines the exact bookkeeping world represented by one hydrated
    BookkeepingState.

    This is runtime context, not the state itself.
    """

    model_config = ConfigDict(
        frozen=True,
        extra="forbid",
    )

    company_id: Identifier

    period_start: date
    period_end: date

    base_currency: CurrencyCode

    policy: AccountingPolicy

    @field_validator("base_currency")
    @classmethod
    def normalize_currency(cls, value: str) -> str:
        return value.upper()

    @model_validator(mode="after")
    def validate_period(self) -> "BookkeepingContext":
        if self.period_end < self.period_start:
            raise ValueError(
                "period_end cannot be earlier than period_start"
            )

        return self


class RuntimeContext(BaseModel):
    """
    Identity and lifecycle metadata for one active BookkeepingState instance.

    Destroying the state destroys this context as well.
    state_revision:
        changes as the live BookkeepingState transitions
    persistence_revision:
        changes when durable artifact storage commits
    """
    model_config = ConfigDict(
        frozen=True,
        extra="forbid",
    )

    session_id: Identifier

    state_revision: int = Field(default=0, ge=0)

    persistence_revision: int = Field(
        ...,
        ge=0,
        description=(
            "Revision of durable bookkeeping artifacts from which this "
            "BookkeepingState was hydrated or last synchronized."
        ),
    )

    hydrated_at: datetime

    @field_validator("hydrated_at")
    @classmethod
    def require_timezone_aware_datetime(
        cls,
        value: datetime,
    ) -> datetime:
        if value.tzinfo is None or value.utcoffset() is None:
            raise ValueError("hydrated_at must be timezone-aware")

        return value