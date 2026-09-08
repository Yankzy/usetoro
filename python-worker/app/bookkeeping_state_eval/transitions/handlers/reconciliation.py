from __future__ import annotations

from typing import TypeAlias

from bookkeeping_state_eval.domain.commands import (
    CreateReconciliationCommand,
    InvalidateReconciliationCommand,
)
from bookkeeping_state_eval.domain.enums import (
    AllocationSupport,
    Eligibility,
    SemanticAdmissibility,
)
from bookkeeping_state_eval.domain.hypotheses import (
    ReconciliationHypothesis,
)
from bookkeeping_state_eval.domain.reconciliations import (
    BankAllocation,
    BookAllocation,
    Reconciliation,
    ReconciliationInvalidation,
)
from bookkeeping_state_eval.persistence.repository import (
    PersistenceWriteSet,
)
from bookkeeping_state_eval.state.bookkeeping_state import (
    BookkeepingState,
)
from bookkeeping_state_eval.state.derived import (
    DerivedStateError,
    build_derived_state,
)
from bookkeeping_state_eval.state.validation import (
    DIRECTION_COMPATIBILITY_MAP,
)
from bookkeeping_state_eval.transitions.result import (
    RejectionCode,
    TransitionRejection,
)


ReconciliationCommand = (
    CreateReconciliationCommand
    | InvalidateReconciliationCommand
)

ReconciliationHandlerResult: TypeAlias = (
    PersistenceWriteSet
    | TransitionRejection
)


def handle_reconciliation_command(
    state: BookkeepingState,
    command: ReconciliationCommand,
) -> ReconciliationHandlerResult:
    """
    Validate a reconciliation command and prepare its immutable durable artifact.

    This is the deterministic gate between:

        proposed reconciliation
            and
        accepted bookkeeping truth

    The handler performs no optimization, semantic reasoning, persistence, or
    state mutation.
    """

    if isinstance(
        command,
        CreateReconciliationCommand,
    ):
        return _handle_create_reconciliation(
            state,
            command,
        )

    if isinstance(
        command,
        InvalidateReconciliationCommand,
    ):
        return _handle_invalidate_reconciliation(
            state,
            command,
        )

    return TransitionRejection(
        code=RejectionCode.UNSUPPORTED_COMMAND,
        message=(
            "Reconciliation handler received unsupported command "
            f"{type(command).__name__!r}"
        ),
    )


# ======================================================================
# Create reconciliation
# ======================================================================


def _handle_create_reconciliation(
    state: BookkeepingState,
    command: CreateReconciliationCommand,
) -> ReconciliationHandlerResult:
    # ------------------------------------------------------------------
    # Allocation shape
    # ------------------------------------------------------------------

    duplicate_bank_ids = _duplicates(
        allocation.bank_item_id
        for allocation in command.bank_allocations
    )

    if duplicate_bank_ids:
        return TransitionRejection(
            code=RejectionCode.INVALID_ALLOCATION,
            message=(
                f"Reconciliation {command.reconciliation_id!r} "
                "contains duplicate BankItem allocations "
                f"{duplicate_bank_ids!r}"
            ),
            artifact_ids=(
                command.reconciliation_id,
                *duplicate_bank_ids,
            ),
        )

    duplicate_book_ids = _duplicates(
        allocation.book_item_id
        for allocation in command.book_allocations
    )

    if duplicate_book_ids:
        return TransitionRejection(
            code=RejectionCode.INVALID_ALLOCATION,
            message=(
                f"Reconciliation {command.reconciliation_id!r} "
                "contains duplicate BookItem allocations "
                f"{duplicate_book_ids!r}"
            ),
            artifact_ids=(
                command.reconciliation_id,
                *duplicate_book_ids,
            ),
        )

    # ------------------------------------------------------------------
    # Monetary conservation
    # ------------------------------------------------------------------

    bank_total = sum(
        allocation.amount_int
        for allocation in command.bank_allocations
    )

    book_total = sum(
        allocation.amount_int
        for allocation in command.book_allocations
    )

    if bank_total != book_total:
        return TransitionRejection(
            code=RejectionCode.MONETARY_IMBALANCE,
            message=(
                f"Reconciliation {command.reconciliation_id!r} "
                f"allocates {bank_total} bank units but "
                f"{book_total} book units"
            ),
            artifact_ids=(
                command.reconciliation_id,
            ),
        )

    # ------------------------------------------------------------------
    # Resolve all referenced authoritative items.
    # ------------------------------------------------------------------

    bank_items = {}

    for allocation in command.bank_allocations:
        bank_item = state.get_bank_item(
            allocation.bank_item_id
        )

        if bank_item is None:
            return TransitionRejection(
                code=RejectionCode.UNKNOWN_BANK_ITEM,
                message=(
                    f"Reconciliation "
                    f"{command.reconciliation_id!r} references "
                    f"unknown BankItem "
                    f"{allocation.bank_item_id!r}"
                ),
                artifact_ids=(
                    command.reconciliation_id,
                    allocation.bank_item_id,
                ),
            )

        bank_items[bank_item.id] = bank_item

    book_items = {}

    for allocation in command.book_allocations:
        book_item = state.get_book_item(
            allocation.book_item_id
        )

        if book_item is None:
            return TransitionRejection(
                code=RejectionCode.UNKNOWN_BOOK_ITEM,
                message=(
                    f"Reconciliation "
                    f"{command.reconciliation_id!r} references "
                    f"unknown BookItem "
                    f"{allocation.book_item_id!r}"
                ),
                artifact_ids=(
                    command.reconciliation_id,
                    allocation.book_item_id,
                ),
            )

        book_items[book_item.id] = book_item

    # ------------------------------------------------------------------
    # Runtime hypothesis provenance
    # ------------------------------------------------------------------

    hypothesis_rejection, validated_hypothesis = _validate_source_hypothesis(
        state,
        command,
    )

    if hypothesis_rejection is not None:
        return hypothesis_rejection

    # ------------------------------------------------------------------
    # Evidence integrity
    # ------------------------------------------------------------------

    effective_evidence = (
        validated_hypothesis.evidence_refs
        if validated_hypothesis is not None
        else command.evidence_refs
    )

    unknown_evidence = tuple(
        sorted(
            evidence_ref
            for evidence_ref in effective_evidence
            if state.get_document(evidence_ref) is None
        )
    )

    if unknown_evidence:
        return TransitionRejection(
            code=RejectionCode.UNKNOWN_DOCUMENT,
            message=(
                f"Reconciliation {command.reconciliation_id!r} "
                "references unknown evidence Documents "
                f"{unknown_evidence!r}"
            ),
            artifact_ids=(
                command.reconciliation_id,
                *unknown_evidence,
            ),
        )

    # ------------------------------------------------------------------
    # Currency consistency
    # ------------------------------------------------------------------

    if state.context.policy.require_exact_currency_match:
        currencies = {
            bank_item.currency
            for bank_item in bank_items.values()
        } | {
            book_item.currency
            for book_item in book_items.values()
        }

        if len(currencies) > 1:
            return TransitionRejection(
                code=RejectionCode.CURRENCY_MISMATCH,
                message=(
                    f"Reconciliation "
                    f"{command.reconciliation_id!r} mixes "
                    f"currencies {sorted(currencies)!r}"
                ),
                artifact_ids=(
                    command.reconciliation_id,
                    *sorted(bank_items),
                    *sorted(book_items),
                ),
            )

    # ------------------------------------------------------------------
    # Accounting direction compatibility
    # ------------------------------------------------------------------

    for bank_item in bank_items.values():
        for book_item in book_items.values():
            pair = (
                bank_item.direction,
                book_item.direction,
            )

            if pair not in DIRECTION_COMPATIBILITY_MAP:
                return TransitionRejection(
                    code=RejectionCode.DIRECTION_MISMATCH,
                    message=(
                        f"Reconciliation "
                        f"{command.reconciliation_id!r} cannot "
                        f"match BankItem {bank_item.id!r} "
                        f"direction {bank_item.direction.value!r} "
                        f"with BookItem {book_item.id!r} "
                        f"direction {book_item.direction.value!r}"
                    ),
                    artifact_ids=(
                        command.reconciliation_id,
                        bank_item.id,
                        book_item.id,
                    ),
                )

    # ------------------------------------------------------------------
    # Resolve current accepted reconciliation state.
    # ------------------------------------------------------------------

    try:
        derived = build_derived_state(state)
    except DerivedStateError as exc:
        return TransitionRejection(
            code=RejectionCode.RESULTING_STATE_INVALID,
            message=(
                "Cannot create reconciliation because current "
                f"BookkeepingState is internally ambiguous: {exc}"
            ),
            artifact_ids=(
                command.reconciliation_id,
            ),
        )

    # ------------------------------------------------------------------
    # Semantic idempotency
    #
    # If an active reconciliation already represents exactly this allocation
    # relation, another artifact would add no bookkeeping truth.
    # ------------------------------------------------------------------

    requested_bank_signature = _bank_allocation_signature(
        command.bank_allocations
    )

    requested_book_signature = _book_allocation_signature(
        command.book_allocations
    )

    for existing in derived.active_reconciliations.values():
        if (
            _bank_allocation_signature(
                existing.bank_allocations
            )
            == requested_bank_signature
            and _book_allocation_signature(
                existing.book_allocations
            )
            == requested_book_signature
        ):
            return PersistenceWriteSet()

    # ------------------------------------------------------------------
    # Routing consistency
    #
    # Routing is not required for every possible reconciliation path, but once
    # a BookItem has an accepted route, reconciliation may not contradict it.
    # ------------------------------------------------------------------

    bank_account_ids = {
        bank_item.bank_account_id
        for bank_item in bank_items.values()
    }

    for book_item in book_items.values():
        active_route = (
            derived.active_routing_by_book_item.get(
                book_item.id
            )
        )

        if active_route is None:
            continue

        if bank_account_ids != {
            active_route.bank_account_id
        }:
            return TransitionRejection(
                code=RejectionCode.ROUTING_SUBJECT_MISMATCH,
                message=(
                    f"BookItem {book_item.id!r} is actively "
                    f"routed to BankAccount "
                    f"{active_route.bank_account_id!r}, but "
                    f"Reconciliation "
                    f"{command.reconciliation_id!r} references "
                    f"BankAccounts "
                    f"{sorted(bank_account_ids)!r}"
                ),
                artifact_ids=(
                    command.reconciliation_id,
                    book_item.id,
                    active_route.id,
                    *sorted(bank_account_ids),
                ),
            )

    # ------------------------------------------------------------------
    # Remaining-capacity protection
    # ------------------------------------------------------------------

    for allocation in command.bank_allocations:
        remaining = derived.bank_remaining_units[
            allocation.bank_item_id
        ]

        if remaining < 0:
            return TransitionRejection(
                code=RejectionCode.RESULTING_STATE_INVALID,
                message=(
                    f"BankItem {allocation.bank_item_id!r} "
                    "is already overallocated in current state"
                ),
                artifact_ids=(
                    command.reconciliation_id,
                    allocation.bank_item_id,
                ),
            )

        if allocation.amount_int > remaining:
            return TransitionRejection(
                code=RejectionCode.CAPACITY_EXCEEDED,
                message=(
                    f"Reconciliation "
                    f"{command.reconciliation_id!r} requests "
                    f"{allocation.amount_int} units from "
                    f"BankItem {allocation.bank_item_id!r}, "
                    f"but only {remaining} units remain"
                ),
                artifact_ids=(
                    command.reconciliation_id,
                    allocation.bank_item_id,
                ),
            )

    for allocation in command.book_allocations:
        remaining = derived.book_remaining_units[
            allocation.book_item_id
        ]

        if remaining < 0:
            return TransitionRejection(
                code=RejectionCode.RESULTING_STATE_INVALID,
                message=(
                    f"BookItem {allocation.book_item_id!r} "
                    "is already overallocated in current state"
                ),
                artifact_ids=(
                    command.reconciliation_id,
                    allocation.book_item_id,
                ),
            )

        if allocation.amount_int > remaining:
            return TransitionRejection(
                code=RejectionCode.CAPACITY_EXCEEDED,
                message=(
                    f"Reconciliation "
                    f"{command.reconciliation_id!r} requests "
                    f"{allocation.amount_int} units from "
                    f"BookItem {allocation.book_item_id!r}, "
                    f"but only {remaining} units remain"
                ),
                artifact_ids=(
                    command.reconciliation_id,
                    allocation.book_item_id,
                ),
            )

    # ------------------------------------------------------------------
    # Partial-allocation policies
    # ------------------------------------------------------------------

    if not state.context.policy.allow_partial_bank_reconciliation:
        for allocation in command.bank_allocations:
            bank_item = bank_items[
                allocation.bank_item_id
            ]

            if allocation.amount_int != bank_item.amount_int:
                return TransitionRejection(
                    code=(
                        RejectionCode
                        .PARTIAL_BANK_RECONCILIATION_FORBIDDEN
                    ),
                    message=(
                        f"BankItem {bank_item.id!r} has "
                        f"authoritative amount "
                        f"{bank_item.amount_int}, but "
                        f"Reconciliation "
                        f"{command.reconciliation_id!r} "
                        f"allocates only "
                        f"{allocation.amount_int} units"
                    ),
                    artifact_ids=(
                        command.reconciliation_id,
                        bank_item.id,
                    ),
                )

    if not state.context.policy.allow_partial_book_reconciliation:
        for allocation in command.book_allocations:
            book_item = book_items[
                allocation.book_item_id
            ]

            if allocation.amount_int != book_item.amount_int:
                return TransitionRejection(
                    code=(
                        RejectionCode
                        .PARTIAL_BOOK_RECONCILIATION_FORBIDDEN
                    ),
                    message=(
                        f"BookItem {book_item.id!r} has "
                        f"authoritative amount "
                        f"{book_item.amount_int}, but "
                        f"Reconciliation "
                        f"{command.reconciliation_id!r} "
                        f"allocates only "
                        f"{allocation.amount_int} units"
                    ),
                    artifact_ids=(
                        command.reconciliation_id,
                        book_item.id,
                    ),
                )

    # ------------------------------------------------------------------
    # Construct immutable durable reconciliation.
    #
    # Reconciliation's domain model performs another local conservation check.
    # That duplication is deliberate defensive validation at the artifact
    # boundary.
    # ------------------------------------------------------------------

    reconciliation = Reconciliation(
        id=command.reconciliation_id,
        bank_allocations=command.bank_allocations,
        book_allocations=command.book_allocations,
        evidence_refs=(
            validated_hypothesis.evidence_refs
            if validated_hypothesis is not None
            else command.evidence_refs
        ),
        source_hypothesis_id=(
            validated_hypothesis.id
            if validated_hypothesis is not None
            else command.source_hypothesis_id
        ),
        source_hypothesis_state_revision=(
            validated_hypothesis.state_revision
            if validated_hypothesis is not None
            else None
        ),
        source_hypothesis_utility=(
            validated_hypothesis.utility
            if validated_hypothesis is not None
            else None
        ),
        source_hypothesis_generated_at=(
            validated_hypothesis.generated_at
            if validated_hypothesis is not None
            else None
        ),
        source_hypothesis_admissibility=(
            validated_hypothesis.admissibility
            if validated_hypothesis is not None
            else (
                command.source_hypothesis_admissibility
                if command.source_hypothesis_admissibility is not None
                else (
                    SemanticAdmissibility.SUPPORTED
                    if command.source_hypothesis_id is not None
                    else None
                )
            )
        ),
        source_hypothesis_allocation_support=(
            validated_hypothesis.allocation_support
            if validated_hypothesis is not None
            else (
                command.source_hypothesis_allocation_support
                if command.source_hypothesis_allocation_support is not None
                else (
                    AllocationSupport.EXPLICIT_EVIDENCE
                    if command.source_hypothesis_id is not None
                    else None
                )
            )
        ),
        semantic_rationale=(
            validated_hypothesis.semantic_rationale
            if validated_hypothesis is not None
            else command.semantic_rationale
        ),
        session_id=command.session_id,
        state_revision_at_creation=(
            state.revision + 1
        ),
        created_at=command.issued_at,
    )

    # ------------------------------------------------------------------
    # Immutable artifact ID protection
    # ------------------------------------------------------------------

    existing = state.get_reconciliation(
        reconciliation.id
    )

    if existing is not None:
        if existing == reconciliation:
            return PersistenceWriteSet()

        return TransitionRejection(
            code=RejectionCode.DUPLICATE_ARTIFACT,
            message=(
                f"Reconciliation ID {reconciliation.id!r} "
                "already exists with different immutable content"
            ),
            artifact_ids=(
                reconciliation.id,
            ),
        )

    return PersistenceWriteSet(
        reconciliations=(
            reconciliation,
        ),
    )


# ======================================================================
# Invalidate reconciliation
# ======================================================================


def _handle_invalidate_reconciliation(
    state: BookkeepingState,
    command: InvalidateReconciliationCommand,
) -> ReconciliationHandlerResult:
    target = state.get_reconciliation(
        command.reconciliation_id
    )

    if target is None:
        return TransitionRejection(
            code=RejectionCode.UNKNOWN_RECONCILIATION,
            message=(
                f"Cannot invalidate Reconciliation "
                f"{command.reconciliation_id!r}: "
                "artifact does not exist"
            ),
            artifact_ids=(
                command.reconciliation_id,
            ),
        )

    existing_target_invalidations = tuple(
        invalidation
        for invalidation
        in state.reconciliation_invalidations.values()
        if (
            invalidation.reconciliation_id
            == command.reconciliation_id
        )
    )

    if existing_target_invalidations:
        return PersistenceWriteSet()

    invalidation = ReconciliationInvalidation(
        id=command.invalidation_id,
        reconciliation_id=command.reconciliation_id,
        reason=command.reason,
        session_id=command.session_id,
        state_revision_at_invalidation=(
            state.revision + 1
        ),
        created_at=command.issued_at,
    )

    existing = state.reconciliation_invalidations.get(
        invalidation.id
    )

    if existing is not None:
        if existing == invalidation:
            return PersistenceWriteSet()

        return TransitionRejection(
            code=RejectionCode.DUPLICATE_ARTIFACT,
            message=(
                f"ReconciliationInvalidation ID "
                f"{invalidation.id!r} already exists with "
                "different immutable content"
            ),
            artifact_ids=(
                invalidation.id,
                target.id,
            ),
        )

    return PersistenceWriteSet(
        reconciliation_invalidations=(
            invalidation,
        ),
    )


# ======================================================================
# Hypothesis validation
# ======================================================================


def _validate_source_hypothesis(
    state: BookkeepingState,
    command: CreateReconciliationCommand,
) -> tuple[TransitionRejection | None, ReconciliationHypothesis | None]:
    if (
        command.source_hypothesis_admissibility is not None
        and command.source_hypothesis_admissibility != SemanticAdmissibility.SUPPORTED
    ):
        return (
            TransitionRejection(
                code=RejectionCode.INVALID_HYPOTHESIS_PROVENANCE,
                message=(
                    f"Command specifies source_hypothesis_admissibility {command.source_hypothesis_admissibility.value!r}, "
                    "but only SUPPORTED can be accepted as durable reconciliation truth"
                ),
                artifact_ids=(
                    command.reconciliation_id,
                ),
            ),
            None,
        )

    if (
        command.source_hypothesis_allocation_support is not None
        and command.source_hypothesis_allocation_support
        in (
            AllocationSupport.INSUFFICIENT_EVIDENCE,
            AllocationSupport.CONTRADICTED,
        )
    ):
        return (
            TransitionRejection(
                code=RejectionCode.INVALID_HYPOTHESIS_PROVENANCE,
                message=(
                    f"Command specifies source_hypothesis_allocation_support {command.source_hypothesis_allocation_support.value!r}, "
                    "but only EXPLICIT_EVIDENCE or UNIQUE_INFERENCE can be accepted as durable reconciliation truth"
                ),
                artifact_ids=(
                    command.reconciliation_id,
                ),
            ),
            None,
        )

    hypothesis_id = command.source_hypothesis_id
    embedded_hyp = command.source_hypothesis

    if hypothesis_id is None and embedded_hyp is None:
        validated_hypothesis = None
    elif embedded_hyp is not None:
        if hypothesis_id != embedded_hyp.id:
            return (
                TransitionRejection(
                    code=RejectionCode.INVALID_HYPOTHESIS_PROVENANCE,
                    message=(
                        f"Reconciliation {command.reconciliation_id!r} "
                        f"specifies source_hypothesis_id {hypothesis_id!r}, "
                        f"which does not match embedded hypothesis id {embedded_hyp.id!r}"
                    ),
                    artifact_ids=(
                        command.reconciliation_id,
                        embedded_hyp.id,
                    ),
                ),
                None,
            )

        if embedded_hyp.state_revision != command.expected_state_revision:
            return (
                TransitionRejection(
                    code=RejectionCode.STALE_HYPOTHESIS,
                    message=(
                        f"Embedded hypothesis {embedded_hyp.id!r} "
                        f"was generated against state revision {embedded_hyp.state_revision}, "
                        f"but command expected_state_revision is {command.expected_state_revision}"
                    ),
                    artifact_ids=(
                        command.reconciliation_id,
                        embedded_hyp.id,
                    ),
                ),
                None,
            )

        if embedded_hyp.state_revision != state.revision:
            return (
                TransitionRejection(
                    code=RejectionCode.STALE_HYPOTHESIS,
                    message=(
                        f"Embedded hypothesis {embedded_hyp.id!r} "
                        f"was generated against state revision {embedded_hyp.state_revision}, "
                        f"but current live revision is {state.revision}"
                    ),
                    artifact_ids=(
                        command.reconciliation_id,
                        embedded_hyp.id,
                    ),
                ),
                None,
            )

        if embedded_hyp.eligibility != Eligibility.SELECTABLE:
            return (
                TransitionRejection(
                    code=RejectionCode.INVALID_ALLOCATION,
                    message=(
                        f"Reconciliation hypothesis {embedded_hyp.id!r} "
                        f"has eligibility {embedded_hyp.eligibility.value!r} "
                        "and cannot be selected as bookkeeping truth"
                    ),
                    artifact_ids=(
                        command.reconciliation_id,
                        embedded_hyp.id,
                    ),
                ),
                None,
            )

        if embedded_hyp.admissibility != SemanticAdmissibility.SUPPORTED:
            return (
                TransitionRejection(
                    code=RejectionCode.INVALID_ALLOCATION,
                    message=(
                        f"Reconciliation hypothesis {embedded_hyp.id!r} "
                        f"has admissibility {embedded_hyp.admissibility.value!r} "
                        "and cannot be selected as bookkeeping truth; only SUPPORTED is admissible"
                    ),
                    artifact_ids=(
                        command.reconciliation_id,
                        embedded_hyp.id,
                    ),
                ),
                None,
            )

        if (
            command.source_hypothesis_admissibility is not None
            and command.source_hypothesis_admissibility != SemanticAdmissibility.SUPPORTED
        ):
            return (
                TransitionRejection(
                    code=RejectionCode.INVALID_ALLOCATION,
                    message=(
                        f"Command specifies source_hypothesis_admissibility {command.source_hypothesis_admissibility.value!r}, "
                        "but only SUPPORTED can be accepted as durable reconciliation truth"
                    ),
                    artifact_ids=(
                        command.reconciliation_id,
                        embedded_hyp.id,
                    ),
                ),
                None,
            )

        if embedded_hyp.allocation_support in (
            AllocationSupport.INSUFFICIENT_EVIDENCE,
            AllocationSupport.CONTRADICTED,
        ):
            return (
                TransitionRejection(
                    code=RejectionCode.INVALID_HYPOTHESIS_PROVENANCE,
                    message=(
                        f"Embedded hypothesis {embedded_hyp.id!r} has allocation_support "
                        f"{embedded_hyp.allocation_support.value!r}; "
                        "only EXPLICIT_EVIDENCE or UNIQUE_INFERENCE hypotheses can be promoted"
                    ),
                    artifact_ids=(
                        command.reconciliation_id,
                        embedded_hyp.id,
                    ),
                ),
                None,
            )

        if (
            command.source_hypothesis_allocation_support is not None
            and command.source_hypothesis_allocation_support != embedded_hyp.allocation_support
        ):
            return (
                TransitionRejection(
                    code=RejectionCode.INVALID_ALLOCATION,
                    message=(
                        f"Command specifies source_hypothesis_allocation_support {command.source_hypothesis_allocation_support.value!r}, "
                        f"which disagrees with embedded hypothesis allocation_support {embedded_hyp.allocation_support.value!r}"
                    ),
                    artifact_ids=(
                        command.reconciliation_id,
                        embedded_hyp.id,
                    ),
                ),
                None,
            )

        if (
            _bank_allocation_signature(embedded_hyp.bank_allocations)
            != _bank_allocation_signature(command.bank_allocations)
            or _book_allocation_signature(embedded_hyp.book_allocations)
            != _book_allocation_signature(command.book_allocations)
        ):
            return (
                TransitionRejection(
                    code=RejectionCode.INVALID_ALLOCATION,
                    message=(
                        f"Reconciliation {command.reconciliation_id!r} "
                        f"claims provenance from hypothesis {embedded_hyp.id!r}, "
                        "but its allocations differ from the hypothesis"
                    ),
                    artifact_ids=(
                        command.reconciliation_id,
                        embedded_hyp.id,
                    ),
                ),
                None,
            )

        if (
            command.evidence_refs
            and command.evidence_refs != embedded_hyp.evidence_refs
        ):
            return (
                TransitionRejection(
                    code=RejectionCode.INVALID_HYPOTHESIS_PROVENANCE,
                    message=(
                        f"Reconciliation {command.reconciliation_id!r} "
                        f"evidence_refs {command.evidence_refs!r} differ "
                        f"from hypothesis evidence_refs {embedded_hyp.evidence_refs!r}"
                    ),
                    artifact_ids=(
                        command.reconciliation_id,
                        embedded_hyp.id,
                    ),
                ),
                None,
            )

        if (
            command.semantic_rationale is not None
            and command.semantic_rationale != embedded_hyp.semantic_rationale
        ):
            return (
                TransitionRejection(
                    code=RejectionCode.INVALID_HYPOTHESIS_PROVENANCE,
                    message=(
                        f"Reconciliation {command.reconciliation_id!r} "
                        f"semantic_rationale {command.semantic_rationale!r} differs "
                        f"from hypothesis semantic_rationale {embedded_hyp.semantic_rationale!r}"
                    ),
                    artifact_ids=(
                        command.reconciliation_id,
                        embedded_hyp.id,
                    ),
                ),
                None,
            )

        # Agreement check with state hypothesis if present
        state_hyp = state.get_reconciliation_hypothesis(embedded_hyp.id)
        if state_hyp is not None and state_hyp != embedded_hyp:
            return (
                TransitionRejection(
                    code=RejectionCode.INVALID_HYPOTHESIS_PROVENANCE,
                    message=(
                        f"Reconciliation {command.reconciliation_id!r} embedded hypothesis "
                        f"{embedded_hyp.id!r} disagrees with state-stored hypothesis"
                    ),
                    artifact_ids=(
                        command.reconciliation_id,
                        embedded_hyp.id,
                    ),
                ),
                None,
            )

        validated_hypothesis = embedded_hyp
    else:
        # Embedded is absent, but hypothesis_id is present -> resolve from state
        assert hypothesis_id is not None
        hypothesis = state.get_reconciliation_hypothesis(hypothesis_id)
        if hypothesis is None:
            return (
                TransitionRejection(
                    code=RejectionCode.STALE_HYPOTHESIS,
                    message=(
                        f"Reconciliation {command.reconciliation_id!r} "
                        f"references runtime hypothesis {hypothesis_id!r}, "
                        "but that hypothesis is no longer present in the current BookkeepingState"
                    ),
                    artifact_ids=(
                        command.reconciliation_id,
                        hypothesis_id,
                    ),
                ),
                None,
            )

        if hypothesis.state_revision != command.expected_state_revision:
            return (
                TransitionRejection(
                    code=RejectionCode.STALE_HYPOTHESIS,
                    message=(
                        f"Reconciliation hypothesis {hypothesis.id!r} "
                        f"was generated against state revision {hypothesis.state_revision}, "
                        f"but command expected_state_revision is {command.expected_state_revision}"
                    ),
                    artifact_ids=(
                        command.reconciliation_id,
                        hypothesis.id,
                    ),
                ),
                None,
            )

        if hypothesis.state_revision != state.revision:
            return (
                TransitionRejection(
                    code=RejectionCode.STALE_HYPOTHESIS,
                    message=(
                        f"Reconciliation hypothesis {hypothesis.id!r} "
                        f"was generated against state revision {hypothesis.state_revision}, "
                        f"but current live revision is {state.revision}"
                    ),
                    artifact_ids=(
                        command.reconciliation_id,
                        hypothesis.id,
                    ),
                ),
                None,
            )

        if hypothesis.eligibility != Eligibility.SELECTABLE:
            return (
                TransitionRejection(
                    code=RejectionCode.INVALID_ALLOCATION,
                    message=(
                        f"Reconciliation hypothesis {hypothesis.id!r} "
                        f"has eligibility {hypothesis.eligibility.value!r} "
                        "and cannot be selected as bookkeeping truth"
                    ),
                    artifact_ids=(
                        command.reconciliation_id,
                        hypothesis.id,
                    ),
                ),
                None,
            )

        if hypothesis.admissibility != SemanticAdmissibility.SUPPORTED:
            return (
                TransitionRejection(
                    code=RejectionCode.INVALID_ALLOCATION,
                    message=(
                        f"Reconciliation hypothesis {hypothesis.id!r} "
                        f"has admissibility {hypothesis.admissibility.value!r} "
                        "and cannot be selected as bookkeeping truth; only SUPPORTED is admissible"
                    ),
                    artifact_ids=(
                        command.reconciliation_id,
                        hypothesis.id,
                    ),
                ),
                None,
            )

        if (
            command.source_hypothesis_admissibility is not None
            and command.source_hypothesis_admissibility != hypothesis.admissibility
        ):
            return (
                TransitionRejection(
                    code=RejectionCode.INVALID_ALLOCATION,
                    message=(
                        f"Command specifies source_hypothesis_admissibility {command.source_hypothesis_admissibility.value!r}, "
                        f"which disagrees with state-stored hypothesis admissibility {hypothesis.admissibility.value!r}"
                    ),
                    artifact_ids=(
                        command.reconciliation_id,
                        hypothesis.id,
                    ),
                ),
                None,
            )

        if hypothesis.allocation_support in (
            AllocationSupport.INSUFFICIENT_EVIDENCE,
            AllocationSupport.CONTRADICTED,
        ):
            return (
                TransitionRejection(
                    code=RejectionCode.INVALID_HYPOTHESIS_PROVENANCE,
                    message=(
                        f"Reconciliation hypothesis {hypothesis.id!r} has allocation_support "
                        f"{hypothesis.allocation_support.value!r}; "
                        "only EXPLICIT_EVIDENCE or UNIQUE_INFERENCE hypotheses can be promoted"
                    ),
                    artifact_ids=(
                        command.reconciliation_id,
                        hypothesis.id,
                    ),
                ),
                None,
            )

        if (
            command.source_hypothesis_allocation_support is not None
            and command.source_hypothesis_allocation_support != hypothesis.allocation_support
        ):
            return (
                TransitionRejection(
                    code=RejectionCode.INVALID_ALLOCATION,
                    message=(
                        f"Command specifies source_hypothesis_allocation_support {command.source_hypothesis_allocation_support.value!r}, "
                        f"which disagrees with state-stored hypothesis allocation_support {hypothesis.allocation_support.value!r}"
                    ),
                    artifact_ids=(
                        command.reconciliation_id,
                        hypothesis.id,
                    ),
                ),
                None,
            )

        if (
            _bank_allocation_signature(hypothesis.bank_allocations)
            != _bank_allocation_signature(command.bank_allocations)
            or _book_allocation_signature(hypothesis.book_allocations)
            != _book_allocation_signature(command.book_allocations)
        ):
            return (
                TransitionRejection(
                    code=RejectionCode.INVALID_ALLOCATION,
                    message=(
                        f"Reconciliation {command.reconciliation_id!r} "
                        f"claims provenance from hypothesis {hypothesis.id!r}, "
                        "but its allocations differ from the hypothesis"
                    ),
                    artifact_ids=(
                        command.reconciliation_id,
                        hypothesis.id,
                    ),
                ),
                None,
            )

        if (
            command.evidence_refs
            and command.evidence_refs != hypothesis.evidence_refs
        ):
            return (
                TransitionRejection(
                    code=RejectionCode.INVALID_HYPOTHESIS_PROVENANCE,
                    message=(
                        f"Reconciliation {command.reconciliation_id!r} "
                        f"evidence_refs {command.evidence_refs!r} differ "
                        f"from hypothesis evidence_refs {hypothesis.evidence_refs!r}"
                    ),
                    artifact_ids=(
                        command.reconciliation_id,
                        hypothesis.id,
                    ),
                ),
                None,
            )

        if (
            command.semantic_rationale is not None
            and command.semantic_rationale != hypothesis.semantic_rationale
        ):
            return (
                TransitionRejection(
                    code=RejectionCode.INVALID_HYPOTHESIS_PROVENANCE,
                    message=(
                        f"Reconciliation {command.reconciliation_id!r} "
                        f"semantic_rationale {command.semantic_rationale!r} differs "
                        f"from hypothesis semantic_rationale {hypothesis.semantic_rationale!r}"
                    ),
                    artifact_ids=(
                        command.reconciliation_id,
                        hypothesis.id,
                    ),
                ),
                None,
            )

        validated_hypothesis = hypothesis

    effective_allocation_support = (
        validated_hypothesis.allocation_support
        if validated_hypothesis is not None
        else command.source_hypothesis_allocation_support
    )
    if (
        effective_allocation_support == AllocationSupport.UNIQUE_INFERENCE
        and not state.context.policy.auto_reconcile_unique_inferred_allocation
    ):
        return (
            TransitionRejection(
                code=RejectionCode.INVALID_ALLOCATION,
                message=(
                    f"Reconciliation {command.reconciliation_id!r} has allocation_support "
                    f"{AllocationSupport.UNIQUE_INFERENCE.value!r}, but current accounting policy "
                    "does not permit automatic reconciliation of unique inferred allocations without explicit evidence"
                ),
                artifact_ids=(
                    command.reconciliation_id,
                    *([validated_hypothesis.id] if validated_hypothesis is not None else ()),
                ),
            ),
            None,
        )

    return None, validated_hypothesis


# ======================================================================
# Helpers
# ======================================================================


def _bank_allocation_signature(
    allocations: tuple[BankAllocation, ...],
) -> tuple[tuple[str, int], ...]:
    return tuple(
        sorted(
            (
                allocation.bank_item_id,
                allocation.amount_int,
            )
            for allocation in allocations
        )
    )


def _book_allocation_signature(
    allocations: tuple[BookAllocation, ...],
) -> tuple[tuple[str, int], ...]:
    return tuple(
        sorted(
            (
                allocation.book_item_id,
                allocation.amount_int,
            )
            for allocation in allocations
        )
    )


def _duplicates(
    values,
) -> tuple[str, ...]:
    seen: set[str] = set()
    duplicates: set[str] = set()

    for value in values:
        if value in seen:
            duplicates.add(value)
        else:
            seen.add(value)

    return tuple(sorted(duplicates))


"""
This gives us a very strong boundary.

A reconciliation hypothesis can say:

BANK-7 matches BOOK-3
utility = 947

but none of that matters until the transition layer proves:

hypothesis still belongs to this state revision
allocations exactly match the selected hypothesis
items actually exist
money balances exactly
remaining capacity exists
currencies match
directions are compatible
partial-allocation policy permits it
existing routing is not contradicted
evidence references are valid

Only then does:

Reconciliation(...)

become durable bookkeeping truth.

There is another important consequence of the revision check. Suppose reconciliation generates 500 hypotheses against S8, then any accepted transition changes the world to S9. Those old hypotheses must never be selected blindly afterward.

The engine will therefore clear the runtime hypothesis pool after every applied state-changing command.
"""