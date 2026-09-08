from __future__ import annotations

from collections.abc import Callable
from datetime import datetime, timezone
from uuid import uuid4

from bookkeeping_state_eval.domain.context import RuntimeContext
from bookkeeping_state_eval.persistence.repository import (
    BookkeepingRepository,
    BookkeepingSnapshot,
)
from bookkeeping_state_eval.state.bookkeeping_state import BookkeepingState
from bookkeeping_state_eval.state.validation import (
    StateValidationReport,
    validate_state,
)


class HydrationError(RuntimeError):
    """Base error for BookkeepingState hydration failures."""


class SnapshotCompanyMismatchError(HydrationError):
    """
    Raised when persistence returns a snapshot belonging to a different company.
    """


class InvalidHydratedStateError(HydrationError):
    """
    Raised when durable artifacts hydrate successfully but do not describe a
    valid bookkeeping world.
    """

    def __init__(
        self,
        report: StateValidationReport,
    ) -> None:
        self.report = report

        summary = "; ".join(
            f"{issue.code.value}: {issue.message}"
            for issue in report.errors
        )

        super().__init__(
            "Hydrated BookkeepingState failed validation"
            + (f": {summary}" if summary else "")
        )


class BookkeepingHydrator:
    """
    Constructs disposable BookkeepingState instances from durable persistence.

    Hydration performs only reconstruction.

    It must NOT:

    - route transactions
    - classify transactions
    - generate reconciliation candidates
    - invoke an LLM
    - create new bookkeeping decisions
    - modify persistence

    Given the same durable snapshot, hydration must reconstruct the same
    bookkeeping world regardless of session identity or hydration time.
    """

    __slots__ = (
        "_repository",
        "_clock",
        "_session_id_factory",
    )

    def __init__(
        self,
        *,
        repository: BookkeepingRepository,
        clock: Callable[[], datetime] | None = None,
        session_id_factory: Callable[[], str] | None = None,
    ) -> None:
        self._repository = repository

        self._clock = clock or _utc_now
        self._session_id_factory = (
            session_id_factory
            or (lambda: str(uuid4()))
        )

    def hydrate(
        self,
        *,
        company_id: str,
        session_id: str | None = None,
        require_valid: bool = True,
    ) -> BookkeepingState:
        """
        Hydrate one fresh BookkeepingState.

        Lifecycle:

            persistence
                -> consistent BookkeepingSnapshot
                -> immutable domain artifacts
                -> BookkeepingState revision 0
                -> deterministic validation
                -> return live state

        Args:
            company_id:
                Company whose bookkeeping world should be reconstructed.

            session_id:
                Optional explicit runtime session identity.

                Tests may supply one for deterministic assertions.
                Production callers may omit it.

            require_valid:
                When True, invalid durable reality prevents the state from
                becoming operational.

                When False, the caller receives the invalid state for
                diagnostics/evaluation.

        Returns:
            A new live BookkeepingState instance.

        Raises:
            SnapshotCompanyMismatchError:
                Persistence returned data for the wrong company.

            InvalidHydratedStateError:
                require_valid=True and state invariants fail.
        """

        snapshot = self._repository.load_snapshot(
            company_id=company_id,
        )

        self._validate_snapshot_identity(
            requested_company_id=company_id,
            snapshot=snapshot,
        )

        resolved_session_id = (
            session_id
            if session_id is not None
            else self._session_id_factory()
        )

        if not resolved_session_id:
            raise HydrationError(
                "Bookkeeping session_id cannot be empty"
            )

        hydrated_at = self._clock()

        if (
            hydrated_at.tzinfo is None
            or hydrated_at.utcoffset() is None
        ):
            raise HydrationError(
                "Hydrator clock must return a timezone-aware datetime"
            )

        runtime = RuntimeContext(
            session_id=resolved_session_id,
            state_revision=0,
            persistence_revision=snapshot.persistence_revision,
            hydrated_at=hydrated_at,
        )

        state = BookkeepingState(
            context=snapshot.context,
            runtime=runtime,

            bank_accounts=snapshot.bank_accounts,
            bank_items=snapshot.bank_items,
            book_items=snapshot.book_items,

            documents=snapshot.documents,
            counterparties=snapshot.counterparties,

            routing_decisions=snapshot.routing_decisions,
            routing_invalidations=snapshot.routing_invalidations,

            classifications=snapshot.classifications,
            classification_invalidations=(
                snapshot.classification_invalidations
            ),

            reconciliations=snapshot.reconciliations,
            reconciliation_invalidations=(
                snapshot.reconciliation_invalidations
            ),
            book_item_evidence_assertions=(
                snapshot.book_item_evidence_assertions
            ),
            book_item_evidence_invalidations=(
                snapshot.book_item_evidence_invalidations
            ),
        )

        report = validate_state(state)

        if require_valid and not report.is_valid:
            state.close()
            raise InvalidHydratedStateError(report)

        return state

    def validate_snapshot(
        self,
        *,
        company_id: str,
    ) -> StateValidationReport:
        """
        Hydrate persistence strictly for diagnostic validation.

        The temporary state is always destroyed before returning.

        This is useful in evals where we want to inspect whether a persisted
        artifact set is valid without opening an operational bookkeeping
        session.
        """

        state = self.hydrate(
            company_id=company_id,
            session_id="validation-session",
            require_valid=False,
        )

        try:
            return validate_state(state)
        finally:
            state.close()

    @staticmethod
    def _validate_snapshot_identity(
        *,
        requested_company_id: str,
        snapshot: BookkeepingSnapshot,
    ) -> None:
        actual_company_id = snapshot.context.company_id

        if actual_company_id != requested_company_id:
            raise SnapshotCompanyMismatchError(
                "Persistence returned snapshot for the wrong company: "
                f"requested={requested_company_id!r}, "
                f"actual={actual_company_id!r}"
            )


def _utc_now() -> datetime:
    return datetime.now(timezone.utc)