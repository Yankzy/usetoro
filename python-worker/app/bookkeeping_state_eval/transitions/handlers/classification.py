from __future__ import annotations

from typing import TypeAlias

from bookkeeping_state_eval.domain.classifications import (
    ClassificationDecision,
    ClassificationInvalidation,
)
from bookkeeping_state_eval.domain.commands import (
    CreateClassificationCommand,
    InvalidateClassificationCommand,
)
from bookkeeping_state_eval.persistence.repository import (
    PersistenceWriteSet,
)
from bookkeeping_state_eval.state.bookkeeping_state import (
    BookkeepingState,
)
from bookkeeping_state_eval.state.derived import (
    DerivedStateError,
    resolve_active_classifications,
)
from bookkeeping_state_eval.transitions.result import (
    RejectionCode,
    TransitionRejection,
)


ClassificationCommand = (
    CreateClassificationCommand
    | InvalidateClassificationCommand
)

ClassificationHandlerResult: TypeAlias = (
    PersistenceWriteSet
    | TransitionRejection
)


def handle_classification_command(
    state: BookkeepingState,
    command: ClassificationCommand,
) -> ClassificationHandlerResult:
    """
    Validate a classification command and prepare immutable durable artifacts.

    This handler does NOT:

    - mutate BookkeepingState
    - persist artifacts
    - advance revisions
    - invoke ASE
    - perform account inference
    - generate hypotheses

    ASE, a human, or a deterministic rule may propose an interpretation.
    This handler decides whether that interpretation can legally become
    bookkeeping truth.
    """

    if isinstance(
        command,
        CreateClassificationCommand,
    ):
        return _handle_create_classification(
            state,
            command,
        )

    if isinstance(
        command,
        InvalidateClassificationCommand,
    ):
        return _handle_invalidate_classification(
            state,
            command,
        )

    return TransitionRejection(
        code=RejectionCode.UNSUPPORTED_COMMAND,
        message=(
            "Classification handler received unsupported command "
            f"{type(command).__name__!r}"
        ),
    )


# ======================================================================
# Create classification
# ======================================================================


def _handle_create_classification(
    state: BookkeepingState,
    command: CreateClassificationCommand,
) -> ClassificationHandlerResult:
    book_item = state.get_book_item(
        command.book_item_id
    )

    if book_item is None:
        return TransitionRejection(
            code=RejectionCode.UNKNOWN_BOOK_ITEM,
            message=(
                f"Cannot create ClassificationDecision "
                f"{command.classification_id!r}: "
                f"BookItem {command.book_item_id!r} does not exist"
            ),
            artifact_ids=(
                command.book_item_id,
            ),
        )

    # ------------------------------------------------------------------
    # Evidence must already exist as durable Documents.
    # ------------------------------------------------------------------

    unknown_evidence = tuple(
        sorted(
            evidence_ref
            for evidence_ref in command.evidence_refs
            if state.get_document(evidence_ref) is None
        )
    )

    if unknown_evidence:
        return TransitionRejection(
            code=RejectionCode.UNKNOWN_DOCUMENT,
            message=(
                f"ClassificationDecision "
                f"{command.classification_id!r} references "
                f"unknown evidence Documents "
                f"{unknown_evidence!r}"
            ),
            artifact_ids=(
                command.classification_id,
                *unknown_evidence,
            ),
        )

    # ------------------------------------------------------------------
    # Validate explicit supersession target.
    # ------------------------------------------------------------------

    superseded = None

    if command.supersedes_classification_id is not None:
        superseded = state.get_classification(
            command.supersedes_classification_id
        )

        if superseded is None:
            return TransitionRejection(
                code=RejectionCode.UNKNOWN_CLASSIFICATION,
                message=(
                    f"ClassificationDecision "
                    f"{command.classification_id!r} attempts "
                    f"to supersede unknown ClassificationDecision "
                    f"{command.supersedes_classification_id!r}"
                ),
                artifact_ids=(
                    command.classification_id,
                    command.supersedes_classification_id,
                ),
            )

        if superseded.book_item_id != command.book_item_id:
            return TransitionRejection(
                code=(
                    RejectionCode
                    .CLASSIFICATION_SUBJECT_MISMATCH
                ),
                message=(
                    f"ClassificationDecision "
                    f"{command.classification_id!r} for "
                    f"BookItem {command.book_item_id!r} cannot "
                    f"supersede ClassificationDecision "
                    f"{superseded.id!r} for "
                    f"BookItem {superseded.book_item_id!r}"
                ),
                artifact_ids=(
                    command.classification_id,
                    superseded.id,
                    command.book_item_id,
                    superseded.book_item_id,
                ),
            )

    # ------------------------------------------------------------------
    # Determine current classification truth.
    # ------------------------------------------------------------------

    try:
        active_classifications = (
            resolve_active_classifications(
                state
            )
        )
    except DerivedStateError as exc:
        return TransitionRejection(
            code=(
                RejectionCode
                .MULTIPLE_ACTIVE_CLASSIFICATIONS
            ),
            message=str(exc),
            artifact_ids=(
                command.book_item_id,
            ),
        )

    current = active_classifications.get(
        command.book_item_id
    )

    # ------------------------------------------------------------------
    # Supersession semantics.
    # ------------------------------------------------------------------

    if command.supersedes_classification_id is not None:
        if current is None:
            return TransitionRejection(
                code=(
                    RejectionCode
                    .CLASSIFICATION_SUBJECT_MISMATCH
                ),
                message=(
                    f"ClassificationDecision "
                    f"{command.classification_id!r} attempts "
                    f"to supersede "
                    f"{command.supersedes_classification_id!r}, "
                    f"but BookItem {command.book_item_id!r} "
                    "currently has no active classification"
                ),
                artifact_ids=(
                    command.book_item_id,
                    command.supersedes_classification_id,
                ),
            )

        if (
            current.id
            != command.supersedes_classification_id
        ):
            return TransitionRejection(
                code=(
                    RejectionCode
                    .CLASSIFICATION_SUBJECT_MISMATCH
                ),
                message=(
                    f"BookItem {command.book_item_id!r} is "
                    f"currently classified by "
                    f"ClassificationDecision {current.id!r}; "
                    f"command attempted to supersede "
                    f"{command.supersedes_classification_id!r}"
                ),
                artifact_ids=(
                    command.book_item_id,
                    current.id,
                    command.supersedes_classification_id,
                ),
            )

    elif current is not None:
        # --------------------------------------------------------------
        # Reasserting the same bookkeeping classification is a no-op.
        #
        # We intentionally compare the economic conclusion, not confidence,
        # rationale, or ASE provenance.
        # --------------------------------------------------------------

        if current.account_code == command.account_code:
            return PersistenceWriteSet()

        # --------------------------------------------------------------
        # Changing classification truth requires explicit supersession.
        # --------------------------------------------------------------

        return TransitionRejection(
            code=(
                RejectionCode
                .MULTIPLE_ACTIVE_CLASSIFICATIONS
            ),
            message=(
                f"BookItem {command.book_item_id!r} already has "
                f"active ClassificationDecision {current.id!r} "
                f"with account code {current.account_code!r}. "
                "A different classification must explicitly "
                "supersede the current decision."
            ),
            artifact_ids=(
                command.book_item_id,
                current.id,
            ),
        )

    # ------------------------------------------------------------------
    # Construct immutable durable artifact.
    # ------------------------------------------------------------------

    classification = ClassificationDecision(
        id=command.classification_id,
        book_item_id=command.book_item_id,
        account_code=command.account_code,
        source=command.classification_source,
        confidence=command.confidence,
        rationale=command.rationale,
        evidence_refs=command.evidence_refs,
        supersedes_classification_id=(
            command.supersedes_classification_id
        ),
        session_id=command.session_id,
        dag_run_id=command.dag_run_id,
        ase_node_id=command.ase_node_id,
        state_revision_at_decision=(
            state.revision + 1
        ),
        created_at=command.issued_at,
    )

    # ------------------------------------------------------------------
    # Immutable ID protection.
    # ------------------------------------------------------------------

    existing = state.get_classification(
        classification.id
    )

    if existing is not None:
        if existing == classification:
            return PersistenceWriteSet()

        return TransitionRejection(
            code=RejectionCode.DUPLICATE_ARTIFACT,
            message=(
                f"ClassificationDecision ID "
                f"{classification.id!r} already exists with "
                "different immutable content"
            ),
            artifact_ids=(
                classification.id,
            ),
        )

    return PersistenceWriteSet(
        classifications=(
            classification,
        ),
    )


# ======================================================================
# Invalidate classification
# ======================================================================


def _handle_invalidate_classification(
    state: BookkeepingState,
    command: InvalidateClassificationCommand,
) -> ClassificationHandlerResult:
    target = state.get_classification(
        command.classification_id
    )

    if target is None:
        return TransitionRejection(
            code=RejectionCode.UNKNOWN_CLASSIFICATION,
            message=(
                f"Cannot invalidate ClassificationDecision "
                f"{command.classification_id!r}: "
                "artifact does not exist"
            ),
            artifact_ids=(
                command.classification_id,
            ),
        )

    # ------------------------------------------------------------------
    # Repeating the same semantic invalidation is a no-op.
    # ------------------------------------------------------------------

    existing_target_invalidations = tuple(
        invalidation
        for invalidation
        in state.classification_invalidations.values()
        if (
            invalidation.classification_id
            == command.classification_id
        )
    )

    if existing_target_invalidations:
        return PersistenceWriteSet()

    invalidation = ClassificationInvalidation(
        id=command.invalidation_id,
        classification_id=command.classification_id,
        reason=command.reason,
        session_id=command.session_id,
        state_revision_at_invalidation=(
            state.revision + 1
        ),
        created_at=command.issued_at,
    )

    # ------------------------------------------------------------------
    # Immutable invalidation ID protection.
    # ------------------------------------------------------------------

    existing = state.classification_invalidations.get(
        invalidation.id
    )

    if existing is not None:
        if existing == invalidation:
            return PersistenceWriteSet()

        return TransitionRejection(
            code=RejectionCode.DUPLICATE_ARTIFACT,
            message=(
                f"ClassificationInvalidation ID "
                f"{invalidation.id!r} already exists with "
                "different immutable content"
            ),
            artifact_ids=(
                invalidation.id,
                target.id,
            ),
        )

    return PersistenceWriteSet(
        classification_invalidations=(
            invalidation,
        ),
    )