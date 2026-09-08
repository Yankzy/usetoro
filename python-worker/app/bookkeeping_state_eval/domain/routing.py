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


Identifier = Annotated[
    str,
    StringConstraints(
        strip_whitespace=True,
        min_length=1,
        max_length=255,
    ),
]


class RoutingDecisionSource(StrEnum):
    """
    Origin of an accepted routing decision.
    """

    CP_SAT = "CP_SAT"
    HUMAN = "HUMAN"
    DETERMINISTIC_RULE = "DETERMINISTIC_RULE"


class RoutingDecision(BaseModel):
    """
    Durable decision assigning a BookItem to a BankAccount.

    This artifact records routing truth after the routing stage has resolved
    which bank account owns the search/reconciliation space for the BookItem.

    It does NOT contain:

    - alternative candidate accounts
    - CP-SAT variables
    - temporary feasibility matrices
    - semantic scoring matrices

    Those remain runtime computation artifacts.

    A RoutingDecision is immutable. If routing is later corrected, the old
    decision is superseded or invalidated rather than mutated.
    """

    model_config = ConfigDict(
        frozen=True,
        extra="forbid",
    )

    id: Identifier

    book_item_id: Identifier

    bank_account_id: Identifier

    source: RoutingDecisionSource

    utility: int | None = Field(
        default=None,
        ge=0,
        le=1000,
        description=(
            "Optional routing semantic utility retained for provenance. "
            "It is not a probability and does not determine current truth "
            "after the decision has been accepted."
        ),
    )

    solver_run_id: Identifier | None = None

    supersedes_routing_decision_id: Identifier | None = Field(
        default=None,
        description=(
            "Previous routing decision replaced by this one, when routing "
            "is corrected without deleting historical truth."
        ),
    )

    session_id: Identifier | None = None

    state_revision_at_creation: int = Field(
        ...,
        ge=0,
    )

    created_at: datetime

    @field_validator("created_at")
    @classmethod
    def require_timezone_aware_datetime(
        cls,
        value: datetime,
    ) -> datetime:
        if value.tzinfo is None or value.utcoffset() is None:
            raise ValueError("created_at must be timezone-aware")

        return value

    @model_validator(mode="after")
    def validate_supersession(self) -> "RoutingDecision":
        if self.supersedes_routing_decision_id == self.id:
            raise ValueError(
                "A routing decision cannot supersede itself"
            )

        return self


class RoutingDecisionInvalidation(BaseModel):
    """
    Durable record removing an existing RoutingDecision from active
    bookkeeping truth.

    The original routing decision remains available for audit and
    historical state reconstruction.
    """

    model_config = ConfigDict(
        frozen=True,
        extra="forbid",
    )

    id: Identifier

    routing_decision_id: Identifier

    reason: str = Field(
        ...,
        min_length=1,
        max_length=4_000,
    )

    session_id: Identifier | None = None

    state_revision_at_invalidation: int = Field(
        ...,
        ge=0,
    )

    created_at: datetime

    @field_validator("created_at")
    @classmethod
    def require_timezone_aware_datetime(
        cls,
        value: datetime,
    ) -> datetime:
        if value.tzinfo is None or value.utcoffset() is None:
            raise ValueError("created_at must be timezone-aware")

        return value