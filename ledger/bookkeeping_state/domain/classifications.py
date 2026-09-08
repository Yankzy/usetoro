from __future__ import annotations

from datetime import datetime
from enum import StrEnum
from typing import Annotated

from pydantic import (
    BaseModel,
    ConfigDict,
    Field,
    StringConstraints,
    model_validator,
)


Identifier = Annotated[
    str,
    StringConstraints(strip_whitespace=True, min_length=1, max_length=255),
]

AccountCode = Annotated[
    str,
    StringConstraints(strip_whitespace=True, min_length=1, max_length=64),
]


class ClassificationSource(StrEnum):
    ASE_DAG = "ASE_DAG"
    HUMAN = "HUMAN"
    DETERMINISTIC_RULE = "DETERMINISTIC_RULE"


class ClassificationDecision(BaseModel):
    """
    Durable accepted categorization of a BookItem.

    The BookItem itself remains unchanged. A correction creates another
    ClassificationDecision that supersedes the previous decision.

    Whether this decision is currently active is derived by BookkeepingState.
    """

    model_config = ConfigDict(
        frozen=True,
        extra="forbid",
    )

    id: Identifier
    book_item_id: Identifier

    account_code: AccountCode

    source: ClassificationSource

    confidence: float | None = Field(
        default=None,
        ge=0.0,
        le=1.0,
        description=(
            "Confidence reported by the decision producer. "
            "It is provenance metadata, not bookkeeping truth."
        ),
    )

    rationale: str | None = Field(
        default=None,
        max_length=4_000,
    )

    evidence_refs: tuple[Identifier, ...] = Field(default_factory=tuple)

    supersedes_classification_id: Identifier | None = Field(
        default=None,
        description=(
            "Previous durable classification replaced by this decision. "
            "The previous artifact is never rewritten or deleted."
        ),
    )

    session_id: Identifier | None = None
    dag_run_id: Identifier | None = None
    ase_node_id: Identifier | None = None

    state_revision_at_decision: int = Field(..., ge=0)

    created_at: datetime

    @model_validator(mode="after")
    def validate_supersession(self) -> "ClassificationDecision":
        if self.supersedes_classification_id == self.id:
            raise ValueError("A classification cannot supersede itself")

        return self


class ClassificationInvalidation(BaseModel):
    """
    Durable record that removes a ClassificationDecision from active truth
    without deleting or mutating the original decision.
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

    created_at: datetime