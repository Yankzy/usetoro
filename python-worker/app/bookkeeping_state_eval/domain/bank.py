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


class BankAccount(BaseModel):
    """
    Canonical durable representation of one company bank account.

    The account does not contain transactions. BankItem references the account
    through bank_account_id.

    Routing views can later group BankItems by bank_account_id to recreate the
    accounts_bank_items structure already proven by the routing eval.
    """

    model_config = ConfigDict(
        frozen=True,
        extra="forbid",
        validate_assignment=True,
    )

    id: Identifier
    name: Identifier
    currency: CurrencyCode
    institution_name: str | None = Field(
        default=None,
        max_length=255,
    )
    external_reference: str | None = Field(
        default=None,
        max_length=255,
        description=(
            "Optional masked IBAN/account number or external banking-system "
            "identifier. It must not be treated as the canonical identity."
        ),
    )

    @field_validator("currency")
    @classmethod
    def normalize_currency(cls, value: str) -> str:
        return value.upper()


class BankItem(BaseModel):
    """
    Canonical durable bank-side economic artifact.

    A BankItem represents an observed bank movement. It is authoritative input
    to BookkeepingState and must not contain reconciliation conclusions,
    classification conclusions, candidate matches, or derived residual state.

    Corrections to the interpretation of a BankItem are represented by separate
    decision artifacts rather than by mutating the observed bank movement.
    """

    model_config = ConfigDict(
        frozen=True,
        extra="forbid",
        validate_assignment=True,
    )

    id: Identifier

    bank_account_id: Identifier = Field(
        ...,
        description="Canonical ID of the BankAccount containing this movement.",
    )

    source_type: SourceType = Field(
        default=SourceType.BANK_STATEMENT_LINE,
    )

    date: date

    amount_units: AmountUnits = Field(
        ...,
        description=(
            "Absolute observed amount represented in exact solver units. "
            "10,000 units = 1.0000 major currency unit."
        ),
    )

    direction: Direction

    currency: CurrencyCode

    description: str = Field(
        ...,
        min_length=1,
        description="Raw or normalized bank statement description.",
    )

    reference: str | None = Field(
        default=None,
        max_length=500,
        description="Extracted bank/payment reference when available.",
    )

    provenance_refs: tuple[Identifier, ...] = Field(
        default_factory=tuple,
        description=(
            "Durable IDs of source artifacts establishing this observation, "
            "for example the originating bank statement/document."
        ),
    )

    @field_validator("currency")
    @classmethod
    def normalize_currency(cls, value: str) -> str:
        return value.upper()

    @model_validator(mode="after")
    def validate_bank_semantics(self) -> "BankItem":
        valid_directions = {
            Direction.BANK_INFLOW,
            Direction.BANK_OUTFLOW,
            Direction.INFLOW,
            Direction.OUTFLOW,
        }

        if self.direction not in valid_directions:
            raise ValueError(
                f"BankItem {self.id!r} cannot use book-side direction "
                f"{self.direction!r}"
            )

        if self.source_type != SourceType.BANK_STATEMENT_LINE:
            raise ValueError(
                f"BankItem {self.id!r} must have source_type "
                f"{SourceType.BANK_STATEMENT_LINE.value!r}"
            )

        return self

    @property
    def amount_int(self) -> int:
        """
        Exact integer amount for deterministic arithmetic and CP-SAT.
        """
        return amount_units_to_int(self.amount_units)