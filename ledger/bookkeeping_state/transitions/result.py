from __future__ import annotations

from dataclasses import dataclass
from enum import StrEnum

from bookkeeping_state.domain.commands import BookkeepingCommand
from bookkeeping_state.transitions.delta import StateDelta


class TransitionStatus(StrEnum):
    APPLIED = "APPLIED"
    REJECTED = "REJECTED"
    NOOP = "NOOP"


class RejectionCode(StrEnum):
    """
    Stable machine-readable reasons a command can be rejected.

    These are intentionally independent from human-readable messages.
    """

    STATE_CLOSED = "STATE_CLOSED"

    STATE_REVISION_CONFLICT = "STATE_REVISION_CONFLICT"
    PERSISTENCE_REVISION_CONFLICT = (
        "PERSISTENCE_REVISION_CONFLICT"
    )

    UNSUPPORTED_COMMAND = "UNSUPPORTED_COMMAND"

    DUPLICATE_ARTIFACT = "DUPLICATE_ARTIFACT"

    UNKNOWN_BOOK_ITEM = "UNKNOWN_BOOK_ITEM"
    UNKNOWN_BANK_ITEM = "UNKNOWN_BANK_ITEM"
    UNKNOWN_BANK_ACCOUNT = "UNKNOWN_BANK_ACCOUNT"

    UNKNOWN_ROUTING_DECISION = "UNKNOWN_ROUTING_DECISION"
    ROUTING_SUBJECT_MISMATCH = "ROUTING_SUBJECT_MISMATCH"

    UNKNOWN_CLASSIFICATION = "UNKNOWN_CLASSIFICATION"
    CLASSIFICATION_SUBJECT_MISMATCH = (
        "CLASSIFICATION_SUBJECT_MISMATCH"
    )

    UNKNOWN_RECONCILIATION = "UNKNOWN_RECONCILIATION"

    UNKNOWN_DOCUMENT = "UNKNOWN_DOCUMENT"

    STALE_HYPOTHESIS = "STALE_HYPOTHESIS"
    INVALID_HYPOTHESIS_PROVENANCE = (
        "INVALID_HYPOTHESIS_PROVENANCE"
    )

    INVALID_ALLOCATION = "INVALID_ALLOCATION"
    MONETARY_IMBALANCE = "MONETARY_IMBALANCE"
    CAPACITY_EXCEEDED = "CAPACITY_EXCEEDED"
    CURRENCY_MISMATCH = "CURRENCY_MISMATCH"
    DIRECTION_MISMATCH = "DIRECTION_MISMATCH"

    PARTIAL_BANK_RECONCILIATION_FORBIDDEN = (
        "PARTIAL_BANK_RECONCILIATION_FORBIDDEN"
    )

    PARTIAL_BOOK_RECONCILIATION_FORBIDDEN = (
        "PARTIAL_BOOK_RECONCILIATION_FORBIDDEN"
    )

    MULTIPLE_ACTIVE_ROUTING_DECISIONS = (
        "MULTIPLE_ACTIVE_ROUTING_DECISIONS"
    )

    MULTIPLE_ACTIVE_CLASSIFICATIONS = (
        "MULTIPLE_ACTIVE_CLASSIFICATIONS"
    )

    UNKNOWN_COUNTERPARTY = "UNKNOWN_COUNTERPARTY"
    UNKNOWN_EVIDENCE_ASSERTION = "UNKNOWN_EVIDENCE_ASSERTION"
    EVIDENCE_ASSERTION_SUBJECT_MISMATCH = (
        "EVIDENCE_ASSERTION_SUBJECT_MISMATCH"
    )
    EVIDENCE_ASSERTION_TYPE_MISMATCH = (
        "EVIDENCE_ASSERTION_TYPE_MISMATCH"
    )
    MULTIPLE_ACTIVE_EVIDENCE_ASSERTIONS = (
        "MULTIPLE_ACTIVE_EVIDENCE_ASSERTIONS"
    )

    RESULTING_STATE_INVALID = "RESULTING_STATE_INVALID"

    PERSISTENCE_FAILURE = "PERSISTENCE_FAILURE"

    SESSION_MISMATCH = "SESSION_MISMATCH"


@dataclass(frozen=True, slots=True)
class TransitionRejection:
    """
    Structured explanation for why a command was not applied.
    """

    code: RejectionCode
    message: str

    artifact_ids: tuple[str, ...] = ()

    def __post_init__(self) -> None:
        if not self.message:
            raise ValueError(
                "TransitionRejection.message cannot be empty"
            )

        if len(set(self.artifact_ids)) != len(
            self.artifact_ids
        ):
            raise ValueError(
                "TransitionRejection.artifact_ids contains duplicates"
            )


@dataclass(frozen=True, slots=True)
class TransitionResult:
    """
    Public result returned by the TransitionEngine.

    Invalid commands are represented as values rather than ordinary control
    flow exceptions.

    This is important for the eval because we want to prove:

        rejected command
        state fingerprint unchanged
        persistence revision unchanged
    """

    status: TransitionStatus

    command: BookkeepingCommand

    previous_state_revision: int
    resulting_state_revision: int

    previous_persistence_revision: int
    resulting_persistence_revision: int

    delta: StateDelta | None = None
    rejection: TransitionRejection | None = None

    def __post_init__(self) -> None:
        if self.previous_state_revision < 0:
            raise ValueError(
                "previous_state_revision cannot be negative"
            )

        if self.resulting_state_revision < 0:
            raise ValueError(
                "resulting_state_revision cannot be negative"
            )

        if self.previous_persistence_revision < 0:
            raise ValueError(
                "previous_persistence_revision cannot be negative"
            )

        if self.resulting_persistence_revision < 0:
            raise ValueError(
                "resulting_persistence_revision cannot be negative"
            )

        if self.status == TransitionStatus.APPLIED:
            if self.delta is None:
                raise ValueError(
                    "APPLIED TransitionResult requires a StateDelta"
                )

            if self.rejection is not None:
                raise ValueError(
                    "APPLIED TransitionResult cannot contain a rejection"
                )

            if self.delta.is_noop:
                raise ValueError(
                    "APPLIED TransitionResult cannot contain a no-op delta"
                )

        elif self.status == TransitionStatus.REJECTED:
            if self.rejection is None:
                raise ValueError(
                    "REJECTED TransitionResult requires a rejection"
                )

            if self.delta is not None:
                raise ValueError(
                    "REJECTED TransitionResult cannot contain a StateDelta"
                )

            if (
                self.resulting_state_revision
                != self.previous_state_revision
            ):
                raise ValueError(
                    "Rejected transition must leave state revision unchanged"
                )

            if (
                self.resulting_persistence_revision
                != self.previous_persistence_revision
            ):
                raise ValueError(
                    "Rejected transition must leave persistence revision "
                    "unchanged"
                )

        elif self.status == TransitionStatus.NOOP:
            if self.rejection is not None:
                raise ValueError(
                    "NOOP TransitionResult cannot contain a rejection"
                )

            if self.delta is not None and not self.delta.is_noop:
                raise ValueError(
                    "NOOP TransitionResult can only contain a no-op delta"
                )

            if (
                self.resulting_state_revision
                != self.previous_state_revision
            ):
                raise ValueError(
                    "No-op transition must leave state revision unchanged"
                )

            if (
                self.resulting_persistence_revision
                != self.previous_persistence_revision
            ):
                raise ValueError(
                    "No-op transition must leave persistence revision "
                    "unchanged"
                )

    @property
    def applied(self) -> bool:
        return self.status == TransitionStatus.APPLIED

    @property
    def rejected(self) -> bool:
        return self.status == TransitionStatus.REJECTED

    @property
    def noop(self) -> bool:
        return self.status == TransitionStatus.NOOP

    @classmethod
    def applied_result(
        cls,
        *,
        command: BookkeepingCommand,
        delta: StateDelta,
    ) -> "TransitionResult":
        if delta.is_noop:
            raise ValueError(
                "Use noop_result() for a no-op StateDelta"
            )

        return cls(
            status=TransitionStatus.APPLIED,
            command=command,

            previous_state_revision=(
                delta.previous_state_revision
            ),
            resulting_state_revision=(
                delta.new_state_revision
            ),

            previous_persistence_revision=(
                delta.previous_persistence_revision
            ),
            resulting_persistence_revision=(
                delta.new_persistence_revision
            ),

            delta=delta,
        )

    @classmethod
    def rejected_result(
        cls,
        *,
        command: BookkeepingCommand,
        rejection: TransitionRejection,
        state_revision: int,
        persistence_revision: int,
    ) -> "TransitionResult":
        return cls(
            status=TransitionStatus.REJECTED,
            command=command,

            previous_state_revision=state_revision,
            resulting_state_revision=state_revision,

            previous_persistence_revision=(
                persistence_revision
            ),
            resulting_persistence_revision=(
                persistence_revision
            ),

            rejection=rejection,
        )

    @classmethod
    def noop_result(
        cls,
        *,
        command: BookkeepingCommand,
        state_revision: int,
        persistence_revision: int,
        delta: StateDelta | None = None,
    ) -> "TransitionResult":
        return cls(
            status=TransitionStatus.NOOP,
            command=command,

            previous_state_revision=state_revision,
            resulting_state_revision=state_revision,

            previous_persistence_revision=(
                persistence_revision
            ),
            resulting_persistence_revision=(
                persistence_revision
            ),

            delta=delta,
        )