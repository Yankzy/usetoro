from __future__ import annotations

from datetime import datetime
from enum import StrEnum
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

Identifier = Annotated[
    str,
    StringConstraints(strip_whitespace=True, min_length=1, max_length=255),
]

AccountCode = Annotated[
    str,
    StringConstraints(strip_whitespace=True, min_length=1, max_length=64),
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


class ResidualBankClassificationStatus(StrEnum):
    CLASSIFIED = "CLASSIFIED"
    HOLD = "HOLD"


class ResidualBankClassificationDecision(BaseModel):
    """
    Immutable domain representation of an accepted residual bank classification decision.

    Derived from durable persistence (BookkeepingResidualBankClassificationDecision)
    or constructed during transition execution.
    """

    model_config = ConfigDict(
        frozen=True,
        extra="forbid",
    )

    id: Identifier
    bank_item_id: Identifier
    staged_transaction_id: Identifier
    bank_account_id: Identifier

    status: ResidualBankClassificationStatus
    account_code: AccountCode | None = None
    account_id: Identifier | None = None

    original_amount_units: int = Field(..., gt=0)
    residual_amount_units: int = Field(..., gt=0)
    direction: Direction
    currency: CurrencyCode

    confidence: float | None = Field(default=None, ge=0.0, le=1.0)
    rationale: str | None = Field(default=None, max_length=4_000)
    evidence_refs: tuple[Identifier, ...] = Field(default_factory=tuple)
    hold_reason: str | None = Field(default=None, max_length=4_000)
    required_evidence: tuple[str, ...] = Field(default_factory=tuple)

    schema_version: str
    dag_id: str
    request_semantic_digest: str
    session_id: Identifier | None = None
    state_revision_at_decision: int = Field(..., ge=0)
    persistence_revision_at_decision: int = Field(..., ge=0)
    ase_node_id: Identifier | None = None
    terminal_property: Identifier | None = None

    supersedes_decision_id: Identifier | None = None
    created_at: datetime

    @field_validator("direction", mode="before")
    @classmethod
    def normalize_direction(cls, value: object) -> Direction:
        val = str(value).strip().upper()
        if val in ("BANK_OUTFLOW", "OUTFLOW"):
            return Direction.OUTFLOW
        if val in ("BANK_INFLOW", "INFLOW"):
            return Direction.INFLOW
        return Direction(val)

    @field_validator("currency")
    @classmethod
    def normalize_currency(cls, value: str) -> str:
        return value.upper()

    @model_validator(mode="after")
    def validate_invariants(self) -> "ResidualBankClassificationDecision":
        if self.residual_amount_units > self.original_amount_units:
            raise ValueError(
                f"residual_amount_units ({self.residual_amount_units}) exceeds "
                f"original_amount_units ({self.original_amount_units})"
            )

        if self.supersedes_decision_id == self.id:
            raise ValueError("A decision cannot supersede itself")

        if self.status == ResidualBankClassificationStatus.CLASSIFIED:
            if not self.account_code:
                raise ValueError("CLASSIFIED status requires account_code")
            if not self.account_id:
                raise ValueError("CLASSIFIED status requires account_id")
            if self.hold_reason is not None and self.hold_reason.strip():
                raise ValueError("CLASSIFIED status must not specify hold_reason")
            if self.confidence is None:
                raise ValueError("CLASSIFIED status requires confidence")
        elif self.status == ResidualBankClassificationStatus.HOLD:
            if self.account_code is not None:
                raise ValueError("HOLD status must not specify account_code")
            if self.account_id is not None:
                raise ValueError("HOLD status must not specify account_id")
            if not self.hold_reason or not self.hold_reason.strip():
                raise ValueError("HOLD status requires hold_reason")

        return self


class ResidualBankClassificationInvalidation(BaseModel):
    """
    Immutable domain representation of an accepted invalidation of a
    prior residual bank classification decision.
    """

    model_config = ConfigDict(
        frozen=True,
        extra="forbid",
    )

    id: Identifier
    classification_id: Identifier
    reason: str = Field(..., min_length=1, max_length=4_000)
    session_id: Identifier | None = None
    state_revision_at_invalidation: int = Field(..., ge=0)
    persistence_revision_at_invalidation: int = Field(..., ge=0)
    created_at: datetime


class ResidualBankPosting(BaseModel):
    """
    Immutable domain representation of an authoritative ledger posting provenance record
    for a residual bank classification decision.
    """

    model_config = ConfigDict(
        frozen=True,
        extra="forbid",
    )

    id: Identifier
    decision_id: Identifier
    staged_transaction_id: Identifier
    bank_item_id: Identifier
    journal_entry_id: Identifier
    bank_cash_transaction_id: Identifier
    contra_transaction_id: Identifier
    reconciliation_id: Identifier
    persistence_revision: int = Field(..., ge=1)
    posted_at: datetime
