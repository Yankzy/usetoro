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
)


Identifier = Annotated[
    str,
    StringConstraints(strip_whitespace=True, min_length=1, max_length=255),
]


class StateEventType(StrEnum):
    """
    Session-local notifications produced after BookkeepingState changes.

    These events are not bookkeeping truth. They tell runtime components
    what changed and where to look in the authoritative state.
    """

    ROUTING_DECISION_CREATED = "ROUTING_DECISION_CREATED"

    CLASSIFICATION_CREATED = "CLASSIFICATION_CREATED"
    CLASSIFICATION_SUPERSEDED = "CLASSIFICATION_SUPERSEDED"
    CLASSIFICATION_INVALIDATED = "CLASSIFICATION_INVALIDATED"

    RECONCILIATION_CREATED = "RECONCILIATION_CREATED"
    RECONCILIATION_INVALIDATED = "RECONCILIATION_INVALIDATED"

    HYPOTHESES_INVALIDATED = "HYPOTHESES_INVALIDATED"

    DERIVED_STATE_INVALIDATED = "DERIVED_STATE_INVALIDATED"
    DERIVED_STATE_REFRESHED = "DERIVED_STATE_REFRESHED"

    ROUTING_DECISION_SUPERSEDED = (
        "ROUTING_DECISION_SUPERSEDED"
    )

    ROUTING_DECISION_INVALIDATED = (
        "ROUTING_DECISION_INVALIDATED"
    )

    BOOK_ITEM_EVIDENCE_ASSERTED = "BOOK_ITEM_EVIDENCE_ASSERTED"
    BOOK_ITEM_EVIDENCE_SUPERSEDED = "BOOK_ITEM_EVIDENCE_SUPERSEDED"
    BOOK_ITEM_EVIDENCE_INVALIDATED = "BOOK_ITEM_EVIDENCE_INVALIDATED"

    RESIDUAL_BANK_CLASSIFICATION_CREATED = "RESIDUAL_BANK_CLASSIFICATION_CREATED"
    RESIDUAL_BANK_CLASSIFICATION_SUPERSEDED = "RESIDUAL_BANK_CLASSIFICATION_SUPERSEDED"
    RESIDUAL_BANK_CLASSIFICATION_INVALIDATED = "RESIDUAL_BANK_CLASSIFICATION_INVALIDATED"


class StateEvent(BaseModel):
    """
    Ephemeral notification emitted by the live BookkeepingState runtime.

    Example:

        RECONCILIATION_CREATED
        affected_artifact_ids = ("REC-001", "BT-004", "BOOK-009")

    Consumers must query BookkeepingState for the resulting truth rather
    than treating this event as authoritative data.
    """

    model_config = ConfigDict(
        frozen=True,
        extra="forbid",
    )

    id: Identifier

    event_type: StateEventType

    state_revision: int = Field(..., ge=0)

    affected_artifact_ids: tuple[Identifier, ...] = Field(
        default_factory=tuple,
    )

    source_command_id: Identifier | None = None

    occurred_at: datetime

    @field_validator("occurred_at")
    @classmethod
    def require_timezone_aware_datetime(
        cls,
        value: datetime,
    ) -> datetime:
        if value.tzinfo is None or value.utcoffset() is None:
            raise ValueError("occurred_at must be timezone-aware")

        return value