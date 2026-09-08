from __future__ import annotations

from datetime import datetime
from enum import StrEnum
from typing import Annotated, Any, Literal

from pydantic import (
    BaseModel,
    ConfigDict,
    Field,
    field_validator,
)

from bookkeeping_state_eval.domain.classifications import (
    ClassificationSource,
)
from bookkeeping_state_eval.domain.enums import (
    AllocationSupport,
    SemanticAdmissibility,
)
from bookkeeping_state_eval.domain.hypotheses import (
    ReconciliationHypothesis,
)
from bookkeeping_state_eval.domain.reconciliations import (
    BankAllocation,
    BookAllocation,
)
from bookkeeping_state_eval.domain.routing import (
    RoutingDecisionSource,
)


class CommandSource(StrEnum):
    """
    Runtime subsystem that issued a command.

    This is distinct from the provenance source recorded on a durable
    bookkeeping decision artifact.
    """

    ROUTING = "ROUTING"
    ASE_DAG = "ASE_DAG"
    RECONCILIATION = "RECONCILIATION"
    HUMAN = "HUMAN"
    SYSTEM = "SYSTEM"


from bookkeeping_state_eval.domain.evidence import (
    BookItemEvidenceType,
    EvidenceSource,
)


class CommandType(StrEnum):
    CREATE_ROUTING_DECISION = "CREATE_ROUTING_DECISION"
    INVALIDATE_ROUTING_DECISION = "INVALIDATE_ROUTING_DECISION"

    CREATE_CLASSIFICATION = "CREATE_CLASSIFICATION"
    INVALIDATE_CLASSIFICATION = "INVALIDATE_CLASSIFICATION"

    CREATE_RECONCILIATION = "CREATE_RECONCILIATION"
    INVALIDATE_RECONCILIATION = "INVALIDATE_RECONCILIATION"

    ASSERT_BOOK_ITEM_EVIDENCE = "ASSERT_BOOK_ITEM_EVIDENCE"
    INVALIDATE_BOOK_ITEM_EVIDENCE = "INVALIDATE_BOOK_ITEM_EVIDENCE"


class CommandBase(BaseModel):
    """
    Base contract for every requested BookkeepingState transition.

    expected_state_revision provides optimistic concurrency against the live
    in-memory BookkeepingState.

    Persistence has its own independent revision check at commit time.
    """

    model_config = ConfigDict(
        frozen=True,
        extra="forbid",
    )

    command_id: str = Field(..., min_length=1)

    expected_state_revision: int = Field(
        ...,
        ge=0,
    )

    source: CommandSource

    session_id: str = Field(
        ...,
        min_length=1,
    )

    issued_at: datetime

    @field_validator("issued_at")
    @classmethod
    def require_timezone_aware_issued_at(
        cls,
        value: datetime,
    ) -> datetime:
        if value.tzinfo is None or value.utcoffset() is None:
            raise ValueError(
                "issued_at must be timezone-aware"
            )

        return value


# ======================================================================
# Routing
# ======================================================================


class CreateRoutingDecisionCommand(CommandBase):
    """
    Request creation of one immutable RoutingDecision.

    If supersedes_routing_decision_id is supplied, the new decision replaces
    that historical decision for the same BookItem.

    The old artifact itself remains untouched.
    """

    command_type: Literal[
        CommandType.CREATE_ROUTING_DECISION
    ] = CommandType.CREATE_ROUTING_DECISION

    routing_decision_id: str = Field(
        ...,
        min_length=1,
    )

    book_item_id: str = Field(
        ...,
        min_length=1,
    )

    bank_account_id: str = Field(
        ...,
        min_length=1,
    )

    decision_source: RoutingDecisionSource

    utility: int | None = Field(
        default=None,
        ge=0,
        le=1000,
    )

    solver_run_id: str | None = None

    supersedes_routing_decision_id: str | None = None

    @field_validator(
        "solver_run_id",
        "supersedes_routing_decision_id",
    )
    @classmethod
    def reject_empty_optional_identifiers(
        cls,
        value: str | None,
    ) -> str | None:
        if value == "":
            raise ValueError(
                "Optional identifier cannot be empty"
            )

        return value


class InvalidateRoutingDecisionCommand(CommandBase):
    """
    Request creation of a durable RoutingDecisionInvalidation artifact.

    Invalidation never deletes the original RoutingDecision.
    """

    command_type: Literal[
        CommandType.INVALIDATE_ROUTING_DECISION
    ] = CommandType.INVALIDATE_ROUTING_DECISION

    invalidation_id: str = Field(
        ...,
        min_length=1,
    )

    routing_decision_id: str = Field(
        ...,
        min_length=1,
    )

    reason: str = Field(
        ...,
        min_length=1,
    )


# ======================================================================
# Classification
# ======================================================================


class CreateClassificationCommand(CommandBase):
    """
    Request creation of one immutable ClassificationDecision.

    ASE interpretation becomes bookkeeping truth only after this command is
    accepted by the TransitionEngine.
    """

    command_type: Literal[
        CommandType.CREATE_CLASSIFICATION
    ] = CommandType.CREATE_CLASSIFICATION

    classification_id: str = Field(
        ...,
        min_length=1,
    )

    book_item_id: str = Field(
        ...,
        min_length=1,
    )

    account_code: str = Field(
        ...,
        min_length=1,
    )

    classification_source: ClassificationSource

    confidence: float | None = Field(
        default=None,
        ge=0.0,
        le=1.0,
    )

    rationale: str | None = None

    evidence_refs: tuple[str, ...] = ()

    supersedes_classification_id: str | None = None

    dag_run_id: str | None = None
    ase_node_id: str | None = None

    @field_validator(
        "supersedes_classification_id",
        "dag_run_id",
        "ase_node_id",
    )
    @classmethod
    def reject_empty_optional_identifiers(
        cls,
        value: str | None,
    ) -> str | None:
        if value == "":
            raise ValueError(
                "Optional identifier cannot be empty"
            )

        return value


class InvalidateClassificationCommand(CommandBase):
    """
    Request creation of a ClassificationInvalidation artifact.
    """

    command_type: Literal[
        CommandType.INVALIDATE_CLASSIFICATION
    ] = CommandType.INVALIDATE_CLASSIFICATION

    invalidation_id: str = Field(
        ...,
        min_length=1,
    )

    classification_id: str = Field(
        ...,
        min_length=1,
    )

    reason: str = Field(
        ...,
        min_length=1,
    )


# ======================================================================
# Reconciliation
# ======================================================================


class CreateReconciliationCommand(CommandBase):
    """
    Request creation of one immutable Reconciliation artifact.

    The model intentionally permits commands that may be economically invalid.

    For example:

        allocation exceeds remaining capacity
        currencies differ
        directions are incompatible
        hypothesis is stale

    Those commands must remain representable so the TransitionEngine can reject
    them deterministically and prove that state remained unchanged.
    """

    command_type: Literal[
        CommandType.CREATE_RECONCILIATION
    ] = CommandType.CREATE_RECONCILIATION

    reconciliation_id: str = Field(
        ...,
        min_length=1,
    )

    bank_allocations: tuple[
        BankAllocation,
        ...
    ] = Field(
        ...,
        min_length=1,
    )

    book_allocations: tuple[
        BookAllocation,
        ...
    ] = Field(
        ...,
        min_length=1,
    )

    source_hypothesis_id: str | None = None
    source_hypothesis: ReconciliationHypothesis | None = None
    source_hypothesis_admissibility: SemanticAdmissibility | None = None
    source_hypothesis_allocation_support: AllocationSupport | None = None

    evidence_refs: tuple[str, ...] = ()

    semantic_rationale: str | None = None

    @field_validator("source_hypothesis_id")
    @classmethod
    def reject_empty_hypothesis_id(
        cls,
        value: str | None,
    ) -> str | None:
        if value == "":
            raise ValueError(
                "source_hypothesis_id cannot be empty"
            )

        return value


class InvalidateReconciliationCommand(CommandBase):
    """
    Request creation of a ReconciliationInvalidation artifact.
    """

    command_type: Literal[
        CommandType.INVALIDATE_RECONCILIATION
    ] = CommandType.INVALIDATE_RECONCILIATION

    invalidation_id: str = Field(
        ...,
        min_length=1,
    )

    reconciliation_id: str = Field(
        ...,
        min_length=1,
    )

    reason: str = Field(
        ...,
        min_length=1,
    )


# ======================================================================
# Evidence
# ======================================================================


class AssertBookItemEvidenceCommand(CommandBase):
    """
    Request creation of one immutable BookItemEvidenceAssertion.

    If supersedes_assertion_id is supplied, the new assertion replaces
    that historical assertion for the same BookItem and same evidence_type.
    """

    command_type: Literal[
        CommandType.ASSERT_BOOK_ITEM_EVIDENCE
    ] = CommandType.ASSERT_BOOK_ITEM_EVIDENCE

    assertion_id: str = Field(
        ...,
        min_length=1,
    )

    book_item_id: str = Field(
        ...,
        min_length=1,
    )

    evidence_type: BookItemEvidenceType

    value: str = Field(
        ...,
        min_length=1,
        max_length=2000,
    )

    document_ids: tuple[str, ...] = Field(
        default_factory=tuple,
    )

    evidence_source: EvidenceSource = EvidenceSource.HUMAN_ASSERTION

    source_document_id: str | None = None

    confidence: float = Field(
        default=1.0,
        ge=0.0,
        le=1.0,
    )

    reason: str | None = None

    metadata: dict[str, Any] = Field(
        default_factory=dict,
    )

    supersedes_assertion_id: str | None = None


class InvalidateBookItemEvidenceCommand(CommandBase):
    """
    Request creation of a BookItemEvidenceInvalidation artifact.
    """

    command_type: Literal[
        CommandType.INVALIDATE_BOOK_ITEM_EVIDENCE
    ] = CommandType.INVALIDATE_BOOK_ITEM_EVIDENCE

    invalidation_id: str = Field(
        ...,
        min_length=1,
    )

    assertion_id: str = Field(
        ...,
        min_length=1,
    )

    reason: str = Field(
        ...,
        min_length=1,
    )


BookkeepingCommand = Annotated[
    (
        CreateRoutingDecisionCommand
        | InvalidateRoutingDecisionCommand
        | CreateClassificationCommand
        | InvalidateClassificationCommand
        | CreateReconciliationCommand
        | InvalidateReconciliationCommand
        | AssertBookItemEvidenceCommand
        | InvalidateBookItemEvidenceCommand
    ),
    Field(discriminator="command_type"),
]