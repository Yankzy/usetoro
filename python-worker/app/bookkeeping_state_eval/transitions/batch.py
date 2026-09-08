from __future__ import annotations

from dataclasses import dataclass
from typing import Sequence

from bookkeeping_state_eval.domain.commands import (
    BookkeepingCommand,
)
from bookkeeping_state_eval.state.bookkeeping_state import (
    BookkeepingState,
)
from bookkeeping_state_eval.transitions.delta import (
    StateDelta,
)
from bookkeeping_state_eval.transitions.result import (
    RejectionCode,
    TransitionResult,
    TransitionStatus,
)


class TransitionBatchError(ValueError):
    """Raised when an atomic sibling batch is structurally invalid."""


@dataclass(
    frozen=True,
    slots=True,
)
class TransitionBatch:
    """
    A set of sibling bookkeeping commands produced against one exact state.

    All commands:

        - belong to the same runtime session
        - were computed against the same state revision
        - are intended to become true atomically

    A TransitionBatch is NOT a sequential mini-program.

    Given:

        base state = S7

        command A expects S7
        command B expects S7
        command C expects S7

    successful application means:

        S7 + {A, B, C} = S8

    NOT:

        S7 + A = S8
        S8 + B = S9
        S9 + C = S10

    Therefore commands inside a batch must not depend on mutations produced
    earlier within that same batch.
    """

    batch_id: str

    session_id: str

    expected_state_revision: int

    commands: tuple[
        BookkeepingCommand,
        ...
    ]

    def __post_init__(
        self,
    ) -> None:
        if not self.batch_id.strip():
            raise TransitionBatchError(
                "batch_id cannot be empty"
            )

        if not self.session_id.strip():
            raise TransitionBatchError(
                "session_id cannot be empty"
            )

        if self.expected_state_revision < 0:
            raise TransitionBatchError(
                "expected_state_revision cannot be negative"
            )

        if not self.commands:
            raise TransitionBatchError(
                "TransitionBatch must contain at least one command"
            )

        command_ids: set[str] = set()

        for command in self.commands:
            if command.command_id in command_ids:
                raise TransitionBatchError(
                    "TransitionBatch contains duplicate command_id "
                    f"{command.command_id!r}"
                )

            command_ids.add(
                command.command_id
            )

            if (
                command.session_id
                != self.session_id
            ):
                raise TransitionBatchError(
                    f"Command {command.command_id!r} belongs to "
                    f"session {command.session_id!r}; batch belongs to "
                    f"{self.session_id!r}"
                )

            if (
                command.expected_state_revision
                != self.expected_state_revision
            ):
                raise TransitionBatchError(
                    f"Command {command.command_id!r} expects state revision "
                    f"{command.expected_state_revision}; batch expects "
                    f"{self.expected_state_revision}"
                )

    @property
    def command_ids(
        self,
    ) -> tuple[str, ...]:
        return tuple(
            command.command_id
            for command in self.commands
        )

    @property
    def size(
        self,
    ) -> int:
        return len(
            self.commands
        )

    @classmethod
    def from_commands(
        cls,
        commands: Sequence[BookkeepingCommand],
        *,
        batch_id: str | None = None,
    ) -> "TransitionBatch":
        if not commands:
            raise TransitionBatchError(
                "TransitionBatch must contain at least one command"
            )

        first = commands[0]
        resolved_batch_id = (
            batch_id
            or f"batch:{first.session_id}:{first.expected_state_revision}"
        )
        return cls(
            batch_id=resolved_batch_id,
            session_id=first.session_id,
            expected_state_revision=first.expected_state_revision,
            commands=tuple(commands),
        )


@dataclass(
    frozen=True,
    slots=True,
)
class BatchTransitionRejection:
    """
    Structured rejection for one entire atomic batch.

    failed_command_id identifies the sibling command that first failed
    command-level validation when applicable.

    Importantly, even if only one sibling is invalid, the entire batch is
    rejected.
    """

    code: RejectionCode

    message: str

    failed_command_id: str | None = None

    artifact_ids: tuple[str, ...] = ()

    def __post_init__(
        self,
    ) -> None:
        if not self.message.strip():
            raise ValueError(
                "BatchTransitionRejection.message cannot be empty"
            )

        if len(set(self.artifact_ids)) != len(
            self.artifact_ids
        ):
            raise ValueError(
                "BatchTransitionRejection.artifact_ids contains duplicates"
            )


@dataclass(
    frozen=True,
    slots=True,
)
class BatchTransitionResult:
    """
    Result of attempting one TransitionBatch.

    Exactly three semantic outcomes exist:

        APPLIED
            combined durable world committed atomically

        NOOP
            every command was semantically idempotent

        REJECTED
            nothing changed

    A successful non-noop batch advances:

        state_revision exactly once
        persistence_revision exactly once
    """

    batch: TransitionBatch

    status: TransitionStatus | str

    previous_state_revision: int
    resulting_state_revision: int

    previous_persistence_revision: int
    resulting_persistence_revision: int

    delta: StateDelta | None = None

    rejection: BatchTransitionRejection | None = None

    command_results: tuple[
        TransitionResult,
        ...
    ] = ()

    def __post_init__(
        self,
    ) -> None:
        allowed = {
            TransitionStatus.APPLIED,
            TransitionStatus.NOOP,
            TransitionStatus.REJECTED,
            "APPLIED",
            "NOOP",
            "REJECTED",
        }

        if self.status not in allowed:
            raise ValueError(
                f"Unsupported batch transition status {self.status!r}"
            )

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

        if self.is_applied:
            if self.delta is None:
                raise ValueError(
                    "APPLIED batch requires StateDelta"
                )

            if self.rejection is not None:
                raise ValueError(
                    "APPLIED batch cannot contain rejection"
                )

            if (
                self.resulting_state_revision
                != self.previous_state_revision + 1
            ):
                raise ValueError(
                    "APPLIED batch must advance state revision exactly once"
                )

            if (
                self.resulting_persistence_revision
                != self.previous_persistence_revision + 1
            ):
                raise ValueError(
                    "APPLIED batch must advance persistence revision "
                    "exactly once"
                )

            return

        if self.is_noop:
            if self.delta is not None:
                raise ValueError(
                    "NOOP batch cannot contain StateDelta"
                )

            if self.rejection is not None:
                raise ValueError(
                    "NOOP batch cannot contain rejection"
                )

            self._require_unchanged_revisions()

            return

        if self.is_rejected:
            if self.delta is not None:
                raise ValueError(
                    "REJECTED batch cannot contain StateDelta"
                )

            if self.rejection is None:
                raise ValueError(
                    "REJECTED batch requires rejection"
                )

            self._require_unchanged_revisions()

    def _require_unchanged_revisions(
        self,
    ) -> None:
        if (
            self.previous_state_revision
            != self.resulting_state_revision
        ):
            raise ValueError(
                f"{self.status} batch cannot change state revision"
            )

        if (
            self.previous_persistence_revision
            != self.resulting_persistence_revision
        ):
            raise ValueError(
                f"{self.status} batch cannot change persistence revision"
            )

    @property
    def is_applied(
        self,
    ) -> bool:
        return (
            self.status == TransitionStatus.APPLIED
            or self.status == "APPLIED"
        )

    @property
    def is_noop(
        self,
    ) -> bool:
        return (
            self.status == TransitionStatus.NOOP
            or self.status == "NOOP"
        )

    @property
    def is_rejected(
        self,
    ) -> bool:
        return (
            self.status == TransitionStatus.REJECTED
            or self.status == "REJECTED"
        )

    @property
    def applied(
        self,
    ) -> bool:
        return self.is_applied

    @property
    def noop(
        self,
    ) -> bool:
        return self.is_noop

    @property
    def rejected(
        self,
    ) -> bool:
        return self.is_rejected

    def get_command_result(
        self,
        command_id: str,
    ) -> TransitionResult | None:
        for res in self.command_results:
            if res.command.command_id == command_id:
                return res
        return None

    @classmethod
    def applied(
        cls,
        *,
        batch: TransitionBatch,
        previous_state_revision: int,
        previous_persistence_revision: int,
        delta: StateDelta,
        command_results: tuple[TransitionResult, ...] = (),
    ) -> "BatchTransitionResult":
        return cls(
            batch=batch,
            status=TransitionStatus.APPLIED,

            previous_state_revision=(
                previous_state_revision
            ),

            resulting_state_revision=(
                previous_state_revision + 1
            ),

            previous_persistence_revision=(
                previous_persistence_revision
            ),

            resulting_persistence_revision=(
                previous_persistence_revision + 1
            ),

            delta=delta,

            rejection=None,

            command_results=command_results,
        )

    @classmethod
    def noop(
        cls,
        *,
        batch: TransitionBatch,
        state_revision: int,
        persistence_revision: int,
        command_results: tuple[TransitionResult, ...] = (),
    ) -> "BatchTransitionResult":
        return cls(
            batch=batch,
            status=TransitionStatus.NOOP,

            previous_state_revision=(
                state_revision
            ),

            resulting_state_revision=(
                state_revision
            ),

            previous_persistence_revision=(
                persistence_revision
            ),

            resulting_persistence_revision=(
                persistence_revision
            ),

            delta=None,

            rejection=None,

            command_results=command_results,
        )

    @classmethod
    def rejected(
        cls,
        *,
        batch: TransitionBatch,
        state: BookkeepingState,
        code: RejectionCode,
        message: str,
        failed_command_id: str | None = None,
        artifact_ids: tuple[str, ...] = (),
        command_results: tuple[TransitionResult, ...] = (),
    ) -> "BatchTransitionResult":
        state_rev = 0 if state.is_closed else state.revision
        persistence_rev = 0 if state.is_closed else state.persistence_revision
        return cls(
            batch=batch,
            status=TransitionStatus.REJECTED,

            previous_state_revision=state_rev,
            resulting_state_revision=state_rev,

            previous_persistence_revision=persistence_rev,
            resulting_persistence_revision=persistence_rev,

            delta=None,

            rejection=BatchTransitionRejection(
                code=code,
                message=message,
                failed_command_id=(
                    failed_command_id
                ),
                artifact_ids=artifact_ids,
            ),

            command_results=command_results,
        )