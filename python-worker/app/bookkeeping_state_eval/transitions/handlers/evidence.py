from __future__ import annotations

from typing import TypeAlias

from bookkeeping_state_eval.domain.commands import (
    AssertBookItemEvidenceCommand,
    InvalidateBookItemEvidenceCommand,
)
from bookkeeping_state_eval.domain.evidence import (
    BookItemEvidenceAssertion,
    BookItemEvidenceInvalidation,
    BookItemEvidenceType,
)
from bookkeeping_state_eval.persistence.repository import (
    PersistenceWriteSet,
)
from bookkeeping_state_eval.state.bookkeeping_state import (
    BookkeepingState,
)
from bookkeeping_state_eval.state.derived import (
    DerivedStateError,
    resolve_active_evidence_assertions,
)
from bookkeeping_state_eval.transitions.result import (
    RejectionCode,
    TransitionRejection,
)

EvidenceCommand = (
    AssertBookItemEvidenceCommand
    | InvalidateBookItemEvidenceCommand
)

EvidenceHandlerResult: TypeAlias = (
    PersistenceWriteSet
    | TransitionRejection
)


def handle_evidence_command(
    state: BookkeepingState,
    command: EvidenceCommand,
) -> EvidenceHandlerResult:
    """
    Validate an evidence command and prepare its durable artifact mutation.

    This handler does NOT:
    - mutate BookkeepingState
    - persist anything
    - advance revisions
    - emit runtime events

    An empty PersistenceWriteSet means the command is semantically idempotent.
    """
    if isinstance(command, AssertBookItemEvidenceCommand):
        return _handle_assert_book_item_evidence(state, command)

    if isinstance(command, InvalidateBookItemEvidenceCommand):
        return _handle_invalidate_book_item_evidence(state, command)

    return TransitionRejection(
        code=RejectionCode.UNSUPPORTED_COMMAND,
        message=(
            "Evidence handler received unsupported command "
            f"{type(command).__name__!r}"
        ),
    )


def _handle_assert_book_item_evidence(
    state: BookkeepingState,
    command: AssertBookItemEvidenceCommand,
) -> EvidenceHandlerResult:
    book_item = state.get_book_item(command.book_item_id)
    if book_item is None:
        return TransitionRejection(
            code=RejectionCode.UNKNOWN_BOOK_ITEM,
            message=(
                f"Cannot assert evidence for unknown BookItem "
                f"{command.book_item_id!r}"
            ),
            artifact_ids=(command.book_item_id,),
        )

    # Referential checks
    if (
        command.evidence_type == BookItemEvidenceType.COUNTERPARTY
        and command.value is not None
    ):
        if state.get_counterparty(command.value) is None:
            return TransitionRejection(
                code=RejectionCode.UNKNOWN_COUNTERPARTY,
                message=(
                    f"Evidence assertion references unknown Counterparty "
                    f"{command.value!r}"
                ),
                artifact_ids=(command.book_item_id, command.value),
            )

    if (
        command.evidence_type == BookItemEvidenceType.DOCUMENT_LINK
        and command.value is not None
    ):
        if state.get_document(command.value) is None:
            return TransitionRejection(
                code=RejectionCode.UNKNOWN_DOCUMENT,
                message=(
                    f"Evidence assertion references unknown Document "
                    f"{command.value!r}"
                ),
                artifact_ids=(command.book_item_id, command.value),
            )

    for doc_id in command.document_ids:
        if state.get_document(doc_id) is None:
            return TransitionRejection(
                code=RejectionCode.UNKNOWN_DOCUMENT,
                message=(
                    f"Evidence assertion references unknown Document {doc_id!r}"
                ),
                artifact_ids=(command.book_item_id, doc_id),
            )

    # Explicit supersession validation
    if command.supersedes_assertion_id is not None:
        superseded = state.get_book_item_evidence_assertion(
            command.supersedes_assertion_id
        )
        if superseded is None:
            return TransitionRejection(
                code=RejectionCode.UNKNOWN_EVIDENCE_ASSERTION,
                message=(
                    f"BookItemEvidenceAssertion {command.assertion_id!r} attempts "
                    f"to supersede unknown assertion {command.supersedes_assertion_id!r}"
                ),
                artifact_ids=(
                    command.assertion_id,
                    command.supersedes_assertion_id,
                ),
            )

        if superseded.book_item_id != command.book_item_id:
            return TransitionRejection(
                code=RejectionCode.EVIDENCE_ASSERTION_SUBJECT_MISMATCH,
                message=(
                    f"BookItemEvidenceAssertion {command.assertion_id!r} for "
                    f"BookItem {command.book_item_id!r} cannot supersede "
                    f"assertion {superseded.id!r} for BookItem {superseded.book_item_id!r}"
                ),
                artifact_ids=(
                    command.assertion_id,
                    superseded.id,
                ),
            )

        if superseded.evidence_type != command.evidence_type:
            return TransitionRejection(
                code=RejectionCode.EVIDENCE_ASSERTION_TYPE_MISMATCH,
                message=(
                    f"BookItemEvidenceAssertion {command.assertion_id!r} of type "
                    f"{command.evidence_type.value!r} cannot supersede assertion "
                    f"{superseded.id!r} of type {superseded.evidence_type.value!r}"
                ),
                artifact_ids=(
                    command.assertion_id,
                    superseded.id,
                ),
            )

    # Determine current active truth for this (book_item, evidence_type) dimension
    try:
        active_assertions = resolve_active_evidence_assertions(state)
    except DerivedStateError as exc:
        return TransitionRejection(
            code=RejectionCode.MULTIPLE_ACTIVE_EVIDENCE_ASSERTIONS,
            message=str(exc),
            artifact_ids=(command.book_item_id,),
        )

    current = active_assertions.get(
        (command.book_item_id, command.evidence_type)
    )

    if command.supersedes_assertion_id is not None:
        if current is None:
            return TransitionRejection(
                code=RejectionCode.EVIDENCE_ASSERTION_SUBJECT_MISMATCH,
                message=(
                    f"BookItemEvidenceAssertion {command.assertion_id!r} attempts "
                    f"to supersede {command.supersedes_assertion_id!r}, but "
                    f"BookItem {command.book_item_id!r} currently has no active "
                    f"assertion for type {command.evidence_type.value!r}"
                ),
                artifact_ids=(
                    command.assertion_id,
                    command.supersedes_assertion_id,
                ),
            )
        if current.id != command.supersedes_assertion_id:
            return TransitionRejection(
                code=RejectionCode.EVIDENCE_ASSERTION_SUBJECT_MISMATCH,
                message=(
                    f"BookItem {command.book_item_id!r} currently has active assertion "
                    f"{current.id!r} for type {command.evidence_type.value!r}; "
                    f"command attempted to supersede {command.supersedes_assertion_id!r}"
                ),
                artifact_ids=(
                    command.assertion_id,
                    current.id,
                    command.supersedes_assertion_id,
                ),
            )
    elif current is not None:
        # Idempotent reassertion of identical semantic state
        if (
            current.value == command.value
            and tuple(current.document_ids) == tuple(command.document_ids)
            and current.source == command.source
        ):
            return PersistenceWriteSet()

        return TransitionRejection(
            code=RejectionCode.MULTIPLE_ACTIVE_EVIDENCE_ASSERTIONS,
            message=(
                f"BookItem {command.book_item_id!r} already has active assertion "
                f"{current.id!r} for dimension {command.evidence_type.value!r}. "
                "A different assertion must explicitly supersede the current assertion."
            ),
            artifact_ids=(
                command.book_item_id,
                current.id,
            ),
        )

    assertion = BookItemEvidenceAssertion(
        id=command.assertion_id,
        book_item_id=command.book_item_id,
        evidence_type=command.evidence_type,
        value=command.value,
        document_ids=tuple(command.document_ids),
        source=command.evidence_source,
        supersedes_assertion_id=command.supersedes_assertion_id,
        reason=command.reason,
        confidence=command.confidence,
        metadata=dict(command.metadata),
        session_id=command.session_id,
        state_revision_at_creation=state.revision + 1,
        created_at=command.issued_at,
    )

    existing = state.get_book_item_evidence_assertion(assertion.id)
    if existing is not None:
        if existing == assertion:
            return PersistenceWriteSet()
        return TransitionRejection(
            code=RejectionCode.DUPLICATE_ARTIFACT,
            message=(
                f"BookItemEvidenceAssertion ID {assertion.id!r} already "
                "exists with different immutable content"
            ),
            artifact_ids=(assertion.id,),
        )

    return PersistenceWriteSet(
        book_item_evidence_assertions=(assertion,),
    )


def _handle_invalidate_book_item_evidence(
    state: BookkeepingState,
    command: InvalidateBookItemEvidenceCommand,
) -> EvidenceHandlerResult:
    target = state.get_book_item_evidence_assertion(command.assertion_id)
    if target is None:
        return TransitionRejection(
            code=RejectionCode.UNKNOWN_EVIDENCE_ASSERTION,
            message=(
                f"Cannot invalidate BookItemEvidenceAssertion "
                f"{command.assertion_id!r}: artifact does not exist"
            ),
            artifact_ids=(command.assertion_id,),
        )

    # Check if already invalidated
    for inv in state.book_item_evidence_invalidations.values():
        if inv.assertion_id == command.assertion_id:
            return PersistenceWriteSet()

    invalidation = BookItemEvidenceInvalidation(
        id=command.invalidation_id,
        assertion_id=command.assertion_id,
        reason=command.reason,
        session_id=command.session_id,
        state_revision_at_invalidation=state.revision + 1,
        created_at=command.issued_at,
    )

    existing = state.get_book_item_evidence_invalidation(invalidation.id)
    if existing is not None:
        if existing == invalidation:
            return PersistenceWriteSet()
        return TransitionRejection(
            code=RejectionCode.DUPLICATE_ARTIFACT,
            message=(
                f"BookItemEvidenceInvalidation ID {invalidation.id!r} already "
                "exists with different immutable content"
            ),
            artifact_ids=(invalidation.id, target.id),
        )

    return PersistenceWriteSet(
        book_item_evidence_invalidations=(invalidation,),
    )
