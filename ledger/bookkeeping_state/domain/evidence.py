from __future__ import annotations

from datetime import datetime
from enum import StrEnum
from typing import Annotated, Any

from pydantic import (
    BaseModel,
    ConfigDict,
    Field,
    StringConstraints,
    field_validator,
)

Identifier = Annotated[
    str,
    StringConstraints(strip_whitespace=True, min_length=1, max_length=255),
]


class BookItemEvidenceType(StrEnum):
    """
    Independent evidentiary dimensions that can be asserted about a BookItem.
    """

    COUNTERPARTY = "COUNTERPARTY"
    REFERENCE = "REFERENCE"
    DESCRIPTION = "DESCRIPTION"
    DOCUMENT_LINK = "DOCUMENT_LINK"


class EvidenceSource(StrEnum):
    """
    Subsystem or provenance source that produced the evidentiary assertion.
    """

    DOCUMENT_EXTRACTION = "DOCUMENT_EXTRACTION"
    ENTITY_RESOLUTION = "ENTITY_RESOLUTION"
    HUMAN_ASSERTION = "HUMAN_ASSERTION"
    MANUAL = "MANUAL"
    SYSTEM = "SYSTEM"


class BookItemEvidenceAssertion(BaseModel):
    """
    Immutable durable evidentiary assertion about one BookItem on one dimension.

    Raw BookItems remain unchanged. Bounded views consume the effective
    interpretation derived from active assertions.
    """

    model_config = ConfigDict(
        frozen=True,
        extra="forbid",
        validate_assignment=True,
    )

    id: Identifier
    book_item_id: Identifier
    session_id: Identifier

    evidence_type: BookItemEvidenceType

    value: str = Field(
        ...,
        min_length=1,
        max_length=2000,
        description="The asserted effective value (e.g. counterparty ID, reference token, description, or document ID).",
    )

    document_ids: tuple[Identifier, ...] = Field(
        default_factory=tuple,
        description="Optional supporting durable Document artifact IDs.",
    )

    source: EvidenceSource = EvidenceSource.HUMAN_ASSERTION

    source_document_id: Identifier | None = Field(
        default=None,
        description="Optional durable Document artifact from which this evidence was extracted.",
    )

    confidence: float = Field(
        default=1.0,
        ge=0.0,
        le=1.0,
        description="Confidence score between 0.0 and 1.0.",
    )

    reason: str | None = None

    metadata: dict[str, Any] = Field(default_factory=dict)

    state_revision_at_creation: int = Field(default=0, ge=0)

    created_at: datetime = Field(
        ...,
        description="Timezone-aware timestamp when this assertion was committed.",
    )

    supersedes_assertion_id: Identifier | None = Field(
        default=None,
        description="ID of an earlier assertion on the same BookItem and same evidence_type that this assertion supersedes.",
    )

    @field_validator("created_at")
    @classmethod
    def require_timezone_aware_created_at(
        cls,
        value: datetime,
    ) -> datetime:
        if value.tzinfo is None or value.utcoffset() is None:
            raise ValueError("created_at must be timezone-aware")
        return value


class BookItemEvidenceInvalidation(BaseModel):
    """
    Durable append-only invalidation of an earlier BookItemEvidenceAssertion.

    Never deletes historical assertions.
    """

    model_config = ConfigDict(
        frozen=True,
        extra="forbid",
        validate_assignment=True,
    )

    id: Identifier
    assertion_id: Identifier
    reason: str = Field(..., min_length=1, max_length=1000)
    session_id: Identifier

    source: EvidenceSource = EvidenceSource.HUMAN_ASSERTION

    state_revision_at_invalidation: int = Field(default=0, ge=0)

    created_at: datetime = Field(
        ...,
        description="Timezone-aware timestamp when this invalidation was recorded.",
    )

    @property
    def invalidation_id(self) -> str:
        return self.id

    @field_validator("created_at")
    @classmethod
    def require_timezone_aware_created_at(
        cls,
        value: datetime,
    ) -> datetime:
        if value.tzinfo is None or value.utcoffset() is None:
            raise ValueError("created_at must be timezone-aware")
        return value

