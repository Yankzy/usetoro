from __future__ import annotations

from collections.abc import Iterable
from threading import RLock

from bookkeeping_state.domain.commands import (
    AssertBookItemEvidenceCommand,
    BookkeepingCommand,
    CreateClassificationCommand,
    CreateReconciliationCommand,
    CreateRoutingDecisionCommand,
    InvalidateBookItemEvidenceCommand,
    InvalidateClassificationCommand,
    InvalidateReconciliationCommand,
    InvalidateRoutingDecisionCommand,
)
from bookkeeping_state.domain.context import RuntimeContext
from bookkeeping_state.domain.events import (
    StateEvent,
    StateEventType,
)
from bookkeeping_state.persistence.repository import (
    BookkeepingRepository,
    PersistenceCommitResult,
    PersistenceConflictError,
    PersistenceDuplicateError,
    PersistenceError,
    PersistenceWriteSet,
)
from bookkeeping_state.state.bookkeeping_state import (
    BookkeepingState,
    BookkeepingStateClosedError,
    DuplicateArtifactError,
    StateRevisionConflictError,
)
from bookkeeping_state.state.queries import (
    BookkeepingQueries,
)
from bookkeeping_state.state.validation import (
    ValidationCode,
    validate_state,
)
from bookkeeping_state.transitions.batch import (
    BatchTransitionRejection,
    BatchTransitionResult,
    TransitionBatch,
)
from bookkeeping_state.transitions.delta import (
    StateDelta,
)
from bookkeeping_state.transitions.handlers.classification import (
    handle_classification_command,
)
from bookkeeping_state.transitions.handlers.evidence import (
    handle_evidence_command,
)
from bookkeeping_state.transitions.handlers.reconciliation import (
    handle_reconciliation_command,
)
from bookkeeping_state.transitions.handlers.routing import (
    handle_routing_command,
)
from bookkeeping_state.transitions.result import (
    RejectionCode,
    TransitionRejection,
    TransitionResult,
    TransitionStatus,
)


class TransitionEngineError(RuntimeError):
    """Base error for transition-engine failures."""


class CommittedStateApplicationError(TransitionEngineError):
    """
    Catastrophic consistency error.

    Persistence committed successfully, but the corresponding mutation could
    not be applied or synchronized to the live BookkeepingState.

    The live BookkeepingState is forcibly closed to prevent stale reads or
    corrupted mutations. The caller must discard the live state and rehydrate
    from durable repository persistence.
    """

    def __init__(
        self,
        *,
        commit: PersistenceCommitResult,
        cause: Exception,
    ) -> None:
        self.commit = commit
        self.cause = cause

        super().__init__(
            "Durable persistence committed successfully (revision "
            f"{commit.new_revision}), but live BookkeepingState synchronization "
            f"failed: {cause}. The live state has been closed. "
            "The repository is authoritative and the state must be rehydrated."
        )


class RepositoryContractError(TransitionEngineError):
    """
    Raised when a BookkeepingRepository violates the contract required by the
    TransitionEngine.
    """


class TransitionEngine:
    """
    The only public write boundary for BookkeepingState.

    High-level lifecycle:

        command / batch
          |
          v
        revision checks
          |
          v
        domain handler(s)
          |
          v
        cross-sibling safety checks
          |
          v
        preview candidate state
          |
          v
        deterministic whole-state validation
          |
          v
        atomic repository commit
          |
          v
        StateDelta
          |
          v
        mutate live BookkeepingState
          |
          v
        TransitionResult / BatchTransitionResult

    No routing model, ASE agent, reconciliation optimizer, or caller is allowed
    to mutate bookkeeping truth directly.
    """

    __slots__ = (
        "_repository",
        "_lock",
    )

    def __init__(
        self,
        *,
        repository: BookkeepingRepository,
    ) -> None:
        self._repository = repository
        self._lock = RLock()

    # ==================================================================
    # Single-command transition
    # ==================================================================

    def apply(
        self,
        *,
        state: BookkeepingState,
        command: BookkeepingCommand,
    ) -> TransitionResult:
        """
        Attempt exactly one bookkeeping state transition.

        Ordinary invalid commands are represented by TransitionResult.REJECTED.

        The following invariant is fundamental:

            rejected command
                means
            state unchanged AND persistence unchanged

        Once persistence has committed, failures are therefore exceptional and
        cannot be represented as ordinary rejections.
        """

        with self._lock:
            # Runtime ownership
            if state.is_closed:
                return self._reject(
                    state=state,
                    command=command,
                    code=RejectionCode.STATE_CLOSED,
                    message=(
                        "Cannot apply command to closed "
                        "BookkeepingState"
                    ),
                )

            if command.session_id != state.session_id:
                return self._reject(
                    state=state,
                    command=command,
                    code=RejectionCode.SESSION_MISMATCH,
                    message=(
                        f"Command {command.command_id!r} belongs "
                        f"to session {command.session_id!r}, but "
                        f"BookkeepingState belongs to session "
                        f"{state.session_id!r}"
                    ),
                    artifact_ids=(
                        command.command_id,
                    ),
                )

            # In-memory optimistic concurrency
            if (
                command.expected_state_revision
                != state.revision
            ):
                return self._reject(
                    state=state,
                    command=command,
                    code=(
                        RejectionCode
                        .STATE_REVISION_CONFLICT
                    ),
                    message=(
                        f"Command {command.command_id!r} was "
                        f"prepared against state revision "
                        f"{command.expected_state_revision}, "
                        f"but current revision is "
                        f"{state.revision}"
                    ),
                    artifact_ids=(
                        command.command_id,
                    ),
                )

            # Domain-specific deterministic validation
            handler_result = self._dispatch(
                state=state,
                command=command,
            )

            if isinstance(
                handler_result,
                TransitionRejection,
            ):
                return TransitionResult.rejected_result(
                    command=command,
                    rejection=handler_result,
                    state_revision=state.revision,
                    persistence_revision=(
                        state.persistence_revision
                    ),
                )

            write_set = handler_result

            # Semantic no-op
            if write_set.is_empty:
                commit_res, commit_err = self._commit_noop(state=state)
                if commit_err is not None:
                    code, msg = commit_err
                    return self._reject(
                        state=state,
                        command=command,
                        code=code,
                        message=msg,
                        artifact_ids=(command.command_id,),
                    )

                return TransitionResult.noop_result(
                    command=command,
                    state_revision=state.revision,
                    persistence_revision=(
                        state.persistence_revision
                    ),
                )

            # Pre-commit resulting-state validation
            preview = self._build_preview_state(
                state=state,
                write_set=write_set,
            )

            try:
                report = validate_state(preview)
            finally:
                preview.close()

            if not report.is_valid:
                message = "; ".join(
                    f"{issue.code.value}: "
                    f"{issue.message}"
                    for issue in report.errors
                )

                return self._reject(
                    state=state,
                    command=command,
                    code=(
                        RejectionCode
                        .RESULTING_STATE_INVALID
                    ),
                    message=(
                        "Command would produce invalid "
                        f"BookkeepingState: {message}"
                    ),
                    artifact_ids=(
                        command.command_id,
                    ),
                )

            # Pre-commit preparation of runtime effects & events
            hypothesis_ids = tuple(
                sorted(
                    state.reconciliation_hypotheses
                )
            )

            events = self._build_events(
                state=state,
                command=command,
                write_set=write_set,
                invalidated_hypothesis_ids=hypothesis_ids,
            )

            # Durable commit
            previous_state_revision = state.revision
            previous_persistence_revision = (
                state.persistence_revision
            )

            commit, commit_err = self._commit_write_set(
                state=state,
                write_set=write_set,
                expected_revision=previous_persistence_revision,
            )
            if commit_err is not None:
                code, msg = commit_err
                return self._reject(
                    state=state,
                    command=command,
                    code=code,
                    message=msg,
                    artifact_ids=(command.command_id,),
                )

            # Post-commit live state synchronization (atomic critical section)
            delta = self._synchronize_committed_state(
                state=state,
                commit=commit,
                events=events,
                previous_state_revision=previous_state_revision,
                clear_all_hypotheses=bool(hypothesis_ids),
            )

            return TransitionResult.applied_result(
                command=command,
                delta=delta,
            )

    # ==================================================================
    # Atomic batch transition
    # ==================================================================

    def apply_batch(
        self,
        *,
        state: BookkeepingState,
        batch: TransitionBatch,
    ) -> BatchTransitionResult:
        """
        Attempt an atomic sibling batch transition.

        Commands independently derived from one unchanged base state S7 must be
        able to commit together as one transition:

            S7 + {A, B, C} = S8

        not:

            S7 -> S8 -> S9 -> S10

        Every sibling command is dispatched against the SAME unchanged base
        BookkeepingState. If any handler rejects, the entire batch is rejected
        with zero mutation.
        """

        with self._lock:
            # 1. Runtime ownership & revision checks
            if state.is_closed:
                return BatchTransitionResult.rejected(
                    batch=batch,
                    state=state,
                    code=RejectionCode.STATE_CLOSED,
                    message="Cannot apply batch to closed BookkeepingState",
                )

            if batch.session_id != state.session_id:
                return BatchTransitionResult.rejected(
                    batch=batch,
                    state=state,
                    code=RejectionCode.SESSION_MISMATCH,
                    message=(
                        f"Batch {batch.batch_id!r} belongs to session "
                        f"{batch.session_id!r}, but BookkeepingState belongs to "
                        f"session {state.session_id!r}"
                    ),
                    artifact_ids=(batch.batch_id,),
                )

            if batch.expected_state_revision != state.revision:
                return BatchTransitionResult.rejected(
                    batch=batch,
                    state=state,
                    code=RejectionCode.STATE_REVISION_CONFLICT,
                    message=(
                        f"Batch {batch.batch_id!r} was prepared against state "
                        f"revision {batch.expected_state_revision}, but current "
                        f"revision is {state.revision}"
                    ),
                    artifact_ids=(batch.batch_id,),
                )

            # 2. Sibling dispatch against SAME unchanged base BookkeepingState
            dispatched: list[
                tuple[BookkeepingCommand, PersistenceWriteSet]
            ] = []
            command_results: list[TransitionResult] = []

            for command in batch.commands:
                handler_result = self._dispatch(
                    state=state,
                    command=command,
                )

                if isinstance(handler_result, TransitionRejection):
                    rejection_res = TransitionResult.rejected_result(
                        command=command,
                        rejection=handler_result,
                        state_revision=state.revision,
                        persistence_revision=state.persistence_revision,
                    )
                    return BatchTransitionResult.rejected(
                        batch=batch,
                        state=state,
                        code=handler_result.code,
                        message=handler_result.message,
                        failed_command_id=command.command_id,
                        artifact_ids=handler_result.artifact_ids,
                        command_results=(rejection_res,),
                    )

                dispatched.append((command, handler_result))

            # 3. Cross-sibling semantic safety checks
            cross_rejection = self._check_cross_sibling_conflicts(
                state=state,
                batch=batch,
                dispatched=dispatched,
            )
            if cross_rejection is not None:
                return BatchTransitionResult.rejected(
                    batch=batch,
                    state=state,
                    code=cross_rejection.code,
                    message=cross_rejection.message,
                    failed_command_id=cross_rejection.failed_command_id,
                    artifact_ids=cross_rejection.artifact_ids,
                )

            # 4. Check if all sibling commands are semantic no-ops
            all_noop = all(ws.is_empty for _, ws in dispatched)

            if all_noop:
                commit, commit_err = self._commit_noop(state=state)
                if commit_err is not None:
                    code, msg = commit_err
                    return BatchTransitionResult.rejected(
                        batch=batch,
                        state=state,
                        code=code,
                        message=msg,
                        artifact_ids=(batch.batch_id,),
                    )

                noop_results = tuple(
                    TransitionResult.noop_result(
                        command=cmd,
                        state_revision=state.revision,
                        persistence_revision=state.persistence_revision,
                    )
                    for cmd, _ in dispatched
                )

                return BatchTransitionResult.noop(
                    batch=batch,
                    state_revision=state.revision,
                    persistence_revision=state.persistence_revision,
                    command_results=noop_results,
                )

            # 5. Merge non-empty sibling write sets
            non_empty_write_sets = [
                ws for _, ws in dispatched if not ws.is_empty
            ]
            merged_write_set = self._merge_write_sets(non_empty_write_sets)

            # 6. Build ONE candidate resulting state at base_state_revision + 1
            preview = self._build_preview_state(
                state=state,
                write_set=merged_write_set,
            )

            try:
                # 7. Whole-state validation on that combined candidate
                report = validate_state(preview)

                if not report.is_valid:
                    # Check if error is capacity overflow or multiple active decisions
                    code = RejectionCode.RESULTING_STATE_INVALID
                    for err in report.errors:
                        if err.code in (
                            ValidationCode.BANK_CAPACITY_OVERFLOW,
                            ValidationCode.BOOK_CAPACITY_OVERFLOW,
                        ):
                            code = RejectionCode.CAPACITY_EXCEEDED
                            break
                        if err.code == ValidationCode.MULTIPLE_ACTIVE_DECISIONS:
                            code = (
                                RejectionCode.MULTIPLE_ACTIVE_ROUTING_DECISIONS
                            )
                            break

                    message = "; ".join(
                        f"{issue.code.value}: {issue.message}"
                        for issue in report.errors
                    )
                    return BatchTransitionResult.rejected(
                        batch=batch,
                        state=state,
                        code=code,
                        message=(
                            "Batch would produce invalid BookkeepingState: "
                            f"{message}"
                        ),
                        artifact_ids=(batch.batch_id,),
                    )

                # 8. Verify no-op siblings postconditions still hold in candidate state
                for cmd, ws in dispatched:
                    if ws.is_empty:
                        holds = self._verify_command_postcondition(
                            candidate=preview,
                            command=cmd,
                        )
                        if not holds:
                            conflicting_cmd = None
                            for other_cmd, other_ws in dispatched:
                                if other_cmd.command_id != cmd.command_id and not other_ws.is_empty:
                                    cmd_book = getattr(cmd, "book_item_id", None)
                                    other_book = getattr(other_cmd, "book_item_id", None)
                                    cmd_rec = getattr(cmd, "reconciliation_id", None)
                                    other_rec = getattr(other_cmd, "reconciliation_id", None)
                                    if (cmd_book is not None and cmd_book == other_book) or (
                                        cmd_rec is not None and cmd_rec == other_rec
                                    ):
                                        conflicting_cmd = other_cmd
                                        break

                            cause_desc = (
                                f"sibling command {conflicting_cmd.command_id!r}"
                                if conflicting_cmd
                                else "another command in the batch"
                            )
                            art_ids = (
                                (cmd.command_id, conflicting_cmd.command_id)
                                if conflicting_cmd
                                else (cmd.command_id,)
                            )
                            return BatchTransitionResult.rejected(
                                batch=batch,
                                state=state,
                                code=RejectionCode.RESULTING_STATE_INVALID,
                                message=(
                                    f"No-op sibling command {cmd.command_id!r} "
                                    f"truth was undone by {cause_desc}"
                                ),
                                failed_command_id=cmd.command_id,
                                artifact_ids=art_ids,
                            )

                # 9. Verify candidate active routing vs reconciliation consistency
                routing_rejection = self._verify_preview_routing_reconciliation(
                    preview=preview,
                    batch=batch,
                )
                if routing_rejection is not None:
                    return BatchTransitionResult.rejected(
                        batch=batch,
                        state=state,
                        code=routing_rejection.code,
                        message=routing_rejection.message,
                        failed_command_id=routing_rejection.failed_command_id,
                        artifact_ids=routing_rejection.artifact_ids,
                    )

            finally:
                preview.close()

            # 10. Pre-commit preparation of runtime effects & batch events
            previous_state_revision = state.revision
            previous_persistence_revision = state.persistence_revision

            hypothesis_ids = tuple(
                sorted(state.reconciliation_hypotheses)
            )

            events = self._build_batch_events(
                state=state,
                batch=batch,
                dispatched=dispatched,
                invalidated_hypothesis_ids=hypothesis_ids,
            )

            # 11. Durable commit - Make ONE repository commit
            commit, commit_err = self._commit_write_set(
                state=state,
                write_set=merged_write_set,
                expected_revision=previous_persistence_revision,
            )
            if commit_err is not None:
                code, msg = commit_err
                return BatchTransitionResult.rejected(
                    batch=batch,
                    state=state,
                    code=code,
                    message=msg,
                    artifact_ids=(batch.batch_id,),
                )

            # 12. Post-commit live state synchronization (atomic critical section)
            delta = self._synchronize_committed_state(
                state=state,
                commit=commit,
                events=events,
                previous_state_revision=previous_state_revision,
                clear_all_hypotheses=bool(hypothesis_ids),
            )

            # Build command results for provenance
            for cmd, ws in dispatched:
                if ws.is_empty:
                    command_results.append(
                        TransitionResult.noop_result(
                            command=cmd,
                            state_revision=previous_state_revision,
                            persistence_revision=previous_persistence_revision,
                        )
                    )
                else:
                    command_results.append(
                        TransitionResult.applied_result(
                            command=cmd,
                            delta=delta,
                        )
                    )

            return BatchTransitionResult.applied(
                batch=batch,
                previous_state_revision=previous_state_revision,
                previous_persistence_revision=previous_persistence_revision,
                delta=delta,
                command_results=tuple(command_results),
            )

    # ==================================================================
    # Cross-sibling validation
    # ==================================================================

    @staticmethod
    def _check_cross_sibling_conflicts(
        *,
        state: BookkeepingState,
        batch: TransitionBatch,
        dispatched: list[
            tuple[BookkeepingCommand, PersistenceWriteSet]
        ],
    ) -> BatchTransitionRejection | None:
        """
        Validate cross-sibling invariants prior to preview construction.
        """

        # A. Reject duplicate durable artifact IDs across sibling commands
        seen_artifact_ids: dict[str, str] = {}
        for command, write_set in dispatched:
            for artifact in (
                write_set.bank_accounts
                + write_set.bank_items
                + write_set.book_items
                + write_set.documents
                + write_set.counterparties
                + write_set.routing_decisions
                + write_set.routing_invalidations
                + write_set.classifications
                + write_set.classification_invalidations
                + write_set.reconciliations
                + write_set.reconciliation_invalidations
            ):
                if artifact.id in seen_artifact_ids:
                    first_cmd_id = seen_artifact_ids[artifact.id]
                    return BatchTransitionRejection(
                        code=RejectionCode.DUPLICATE_ARTIFACT,
                        message=(
                            f"Sibling commands {first_cmd_id!r} and "
                            f"{command.command_id!r} both produce artifact ID "
                            f"{artifact.id!r}"
                        ),
                        failed_command_id=command.command_id,
                        artifact_ids=(artifact.id,),
                    )
                seen_artifact_ids[artifact.id] = command.command_id

        # B. Repeated hypothesis promotion
        promoted_hypotheses: dict[str, str] = {}
        for command, _ in dispatched:
            if isinstance(command, CreateReconciliationCommand):
                hyp_id = command.source_hypothesis_id
                if hyp_id is not None:
                    if hyp_id in promoted_hypotheses:
                        first_cmd = promoted_hypotheses[hyp_id]
                        return BatchTransitionRejection(
                            code=RejectionCode.STALE_HYPOTHESIS,
                            message=(
                                f"Reconciliation hypothesis {hyp_id!r} was "
                                f"promoted by multiple sibling commands "
                                f"({first_cmd!r} and {command.command_id!r})"
                            ),
                            failed_command_id=command.command_id,
                            artifact_ids=(hyp_id,),
                        )
                    promoted_hypotheses[hyp_id] = command.command_id

        # C. Repeated invalidation conflicts
        invalidated_routing: dict[str, str] = {}
        invalidated_classification: dict[str, str] = {}
        invalidated_reconciliation: dict[str, str] = {}

        for command, _ in dispatched:
            if isinstance(command, InvalidateRoutingDecisionCommand):
                target_id = command.routing_decision_id
                if target_id in invalidated_routing:
                    return BatchTransitionRejection(
                        code=RejectionCode.RESULTING_STATE_INVALID,
                        message=(
                            f"Multiple sibling commands invalidate RoutingDecision "
                            f"{target_id!r}"
                        ),
                        failed_command_id=command.command_id,
                        artifact_ids=(target_id,),
                    )
                invalidated_routing[target_id] = command.command_id

            elif isinstance(command, InvalidateClassificationCommand):
                target_id = command.classification_id
                if target_id in invalidated_classification:
                    return BatchTransitionRejection(
                        code=RejectionCode.RESULTING_STATE_INVALID,
                        message=(
                            f"Multiple sibling commands invalidate ClassificationDecision "
                            f"{target_id!r}"
                        ),
                        failed_command_id=command.command_id,
                        artifact_ids=(target_id,),
                    )
                invalidated_classification[target_id] = command.command_id

            elif isinstance(command, InvalidateReconciliationCommand):
                target_id = command.reconciliation_id
                if target_id in invalidated_reconciliation:
                    return BatchTransitionRejection(
                        code=RejectionCode.RESULTING_STATE_INVALID,
                        message=(
                            f"Multiple sibling commands invalidate Reconciliation "
                            f"{target_id!r}"
                        ),
                        failed_command_id=command.command_id,
                        artifact_ids=(target_id,),
                    )
                invalidated_reconciliation[target_id] = command.command_id

        # D. Duplicate active routes across commands for the same book item
        routed_book_items: dict[str, str] = {}
        for command, write_set in dispatched:
            if isinstance(command, CreateRoutingDecisionCommand) and not write_set.is_empty:
                book_id = command.book_item_id
                if book_id in routed_book_items:
                    return BatchTransitionRejection(
                        code=RejectionCode.MULTIPLE_ACTIVE_ROUTING_DECISIONS,
                        message=(
                            f"Multiple sibling commands create active routing "
                            f"decisions for BookItem {book_id!r}"
                        ),
                        failed_command_id=command.command_id,
                        artifact_ids=(book_id,),
                    )
                routed_book_items[book_id] = command.command_id

        # E. Duplicate active classifications across commands for the same book item
        classified_book_items: dict[str, str] = {}
        for command, write_set in dispatched:
            if isinstance(command, CreateClassificationCommand) and not write_set.is_empty:
                book_id = command.book_item_id
                if book_id in classified_book_items:
                    return BatchTransitionRejection(
                        code=RejectionCode.MULTIPLE_ACTIVE_CLASSIFICATIONS,
                        message=(
                            f"Multiple sibling commands create active classifications "
                            f"for BookItem {book_id!r}"
                        ),
                        failed_command_id=command.command_id,
                        artifact_ids=(book_id,),
                    )
                classified_book_items[book_id] = command.command_id

        # F. Early Capacity Aggregation across sibling reconciliations
        from bookkeeping_state.state.derived import build_derived_state

        try:
            derived = build_derived_state(state)
            total_bank_alloc: dict[str, int] = {}
            total_book_alloc: dict[str, int] = {}

            for command, write_set in dispatched:
                if isinstance(command, CreateReconciliationCommand) and not write_set.is_empty:
                    for b_alloc in command.bank_allocations:
                        total_bank_alloc[b_alloc.bank_item_id] = (
                            total_bank_alloc.get(b_alloc.bank_item_id, 0)
                            + b_alloc.amount_int
                        )
                        remaining = derived.bank_remaining_units.get(
                            b_alloc.bank_item_id, 0
                        )
                        if total_bank_alloc[b_alloc.bank_item_id] > remaining:
                            return BatchTransitionRejection(
                                code=RejectionCode.CAPACITY_EXCEEDED,
                                message=(
                                    f"Combined sibling reconciliations request "
                                    f"{total_bank_alloc[b_alloc.bank_item_id]} units "
                                    f"from BankItem {b_alloc.bank_item_id!r}, "
                                    f"exceeding remaining capacity {remaining}"
                                ),
                                failed_command_id=command.command_id,
                                artifact_ids=(
                                    command.reconciliation_id,
                                    b_alloc.bank_item_id,
                                ),
                            )

                    for bk_alloc in command.book_allocations:
                        total_book_alloc[bk_alloc.book_item_id] = (
                            total_book_alloc.get(bk_alloc.book_item_id, 0)
                            + bk_alloc.amount_int
                        )
                        remaining = derived.book_remaining_units.get(
                            bk_alloc.book_item_id, 0
                        )
                        if total_book_alloc[bk_alloc.book_item_id] > remaining:
                            return BatchTransitionRejection(
                                code=RejectionCode.CAPACITY_EXCEEDED,
                                message=(
                                    f"Combined sibling reconciliations request "
                                    f"{total_book_alloc[bk_alloc.book_item_id]} units "
                                    f"from BookItem {bk_alloc.book_item_id!r}, "
                                    f"exceeding remaining capacity {remaining}"
                                ),
                                failed_command_id=command.command_id,
                                artifact_ids=(
                                    command.reconciliation_id,
                                    bk_alloc.book_item_id,
                                ),
                            )
        except Exception:
            pass

        # G. Active routing vs reconciliation contradiction across siblings (bidirectional)
        for command, write_set in dispatched:
            if isinstance(command, CreateRoutingDecisionCommand) and not write_set.is_empty:
                # Forward: route command checked against reconciliations in state
                for rec in state.reconciliations.values():
                    if rec.id in state.reconciliation_invalidations:
                        continue
                    if any(a.book_item_id == command.book_item_id for a in rec.book_allocations):
                        for b_alloc in rec.bank_allocations:
                            bank_item = state.get_bank_item(b_alloc.bank_item_id)
                            if bank_item and bank_item.bank_account_id != command.bank_account_id:
                                return BatchTransitionRejection(
                                    code=RejectionCode.ROUTING_SUBJECT_MISMATCH,
                                    message=(
                                        f"BookItem {command.book_item_id!r} is being routed "
                                        f"to BankAccount {command.bank_account_id!r}, but existing "
                                        f"Reconciliation {rec.id!r} references BankAccount "
                                        f"{bank_item.bank_account_id!r}"
                                    ),
                                    failed_command_id=command.command_id,
                                    artifact_ids=(
                                        command.routing_decision_id,
                                        command.book_item_id,
                                        bank_item.bank_account_id,
                                    ),
                                )

            if isinstance(command, CreateReconciliationCommand) and not write_set.is_empty:
                bank_items = {
                    bi.id: bi
                    for bi in state.bank_items.values()
                }
                for allocation in command.book_allocations:
                    book_id = allocation.book_item_id
                    # Find active route: from sibling routing command or state
                    target_account = None
                    if book_id in routed_book_items:
                        # Find the command that routed it
                        for c, ws in dispatched:
                            if (
                                isinstance(c, CreateRoutingDecisionCommand)
                                and c.book_item_id == book_id
                                and not ws.is_empty
                            ):
                                target_account = c.bank_account_id
                                break
                    else:
                        active_route = BookkeepingQueries(state).active_route(
                            book_id
                        )
                        if active_route is not None:
                            target_account = active_route.bank_account_id

                    if target_account is not None:
                        for b_alloc in command.bank_allocations:
                            bank_item = bank_items.get(b_alloc.bank_item_id)
                            if (
                                bank_item is not None
                                and bank_item.bank_account_id != target_account
                            ):
                                return BatchTransitionRejection(
                                    code=RejectionCode.ROUTING_SUBJECT_MISMATCH,
                                    message=(
                                        f"BookItem {book_id!r} is routed to "
                                        f"BankAccount {target_account!r}, but "
                                        f"Reconciliation {command.reconciliation_id!r} "
                                        f"references BankAccount "
                                        f"{bank_item.bank_account_id!r}"
                                    ),
                                    failed_command_id=command.command_id,
                                    artifact_ids=(
                                        command.reconciliation_id,
                                        book_id,
                                        target_account,
                                    ),
                                )

        return None

    # ==================================================================
    # No-op postcondition verification
    # ==================================================================

    @staticmethod
    def _verify_command_postcondition(
        *,
        candidate: BookkeepingState,
        command: BookkeepingCommand,
    ) -> bool:
        """
        Verify that a semantic no-op command's intended postcondition still
        holds in the candidate preview state.
        """
        queries = BookkeepingQueries(candidate)

        if isinstance(command, CreateRoutingDecisionCommand):
            active_route = queries.active_route(command.book_item_id)
            return (
                active_route is not None
                and active_route.bank_account_id == command.bank_account_id
            )

        if isinstance(command, InvalidateRoutingDecisionCommand):
            return (
                command.routing_decision_id in candidate.routing_invalidations
                or candidate.get_routing_decision(
                    command.routing_decision_id
                )
                is None
            )

        if isinstance(command, CreateClassificationCommand):
            active_class = queries.active_classification(command.book_item_id)
            return (
                active_class is not None
                and active_class.account_code == command.account_code
            )

        if isinstance(command, InvalidateClassificationCommand):
            return (
                command.classification_id
                in candidate.classification_invalidations
                or candidate.get_classification(command.classification_id)
                is None
            )

        if isinstance(command, CreateReconciliationCommand):
            rec = candidate.get_reconciliation(command.reconciliation_id)
            return (
                rec is not None
                and rec.id not in candidate.reconciliation_invalidations
            )

        if isinstance(command, InvalidateReconciliationCommand):
            return (
                command.reconciliation_id
                in candidate.reconciliation_invalidations
            )

        return True

    @staticmethod
    def _verify_preview_routing_reconciliation(
        *,
        preview: BookkeepingState,
        batch: TransitionBatch,
    ) -> BatchTransitionRejection | None:
        """
        Verify routing vs reconciliation consistency in the candidate preview.
        """
        queries = BookkeepingQueries(preview)

        for reconciliation in preview.reconciliations.values():
            if reconciliation.id in preview.reconciliation_invalidations:
                continue

            bank_account_ids = {
                preview.bank_items[alloc.bank_item_id].bank_account_id
                for alloc in reconciliation.bank_allocations
                if alloc.bank_item_id in preview.bank_items
            }

            for alloc in reconciliation.book_allocations:
                active_route = queries.active_route(alloc.book_item_id)
                if active_route is None:
                    continue

                if bank_account_ids != {active_route.bank_account_id}:
                    return BatchTransitionRejection(
                        code=RejectionCode.ROUTING_SUBJECT_MISMATCH,
                        message=(
                            f"BookItem {alloc.book_item_id!r} is actively routed "
                            f"to BankAccount {active_route.bank_account_id!r}, "
                            f"but Reconciliation {reconciliation.id!r} references "
                            f"BankAccounts {sorted(bank_account_ids)!r}"
                        ),
                        artifact_ids=(
                            reconciliation.id,
                            alloc.book_item_id,
                            active_route.id,
                            *sorted(bank_account_ids),
                        ),
                    )

        return None

    # ==================================================================
    # Dispatch
    # ==================================================================

    @staticmethod
    def _dispatch(
        *,
        state: BookkeepingState,
        command: BookkeepingCommand,
    ) -> PersistenceWriteSet | TransitionRejection:
        if isinstance(
            command,
            (
                CreateRoutingDecisionCommand,
                InvalidateRoutingDecisionCommand,
            ),
        ):
            return handle_routing_command(
                state,
                command,
            )

        if isinstance(
            command,
            (
                CreateClassificationCommand,
                InvalidateClassificationCommand,
            ),
        ):
            return handle_classification_command(
                state,
                command,
            )

        if isinstance(
            command,
            (
                CreateReconciliationCommand,
                InvalidateReconciliationCommand,
            ),
        ):
            return handle_reconciliation_command(
                state,
                command,
            )

        if isinstance(
            command,
            (
                AssertBookItemEvidenceCommand,
                InvalidateBookItemEvidenceCommand,
            ),
        ):
            return handle_evidence_command(
                state,
                command,
            )

        return TransitionRejection(
            code=RejectionCode.UNSUPPORTED_COMMAND,
            message=(
                "TransitionEngine does not support command "
                f"{type(command).__name__!r}"
            ),
            artifact_ids=(
                command.command_id,
            ),
        )

    # ==================================================================
    # Write set merge & Preview
    # ==================================================================

    @staticmethod
    def _merge_write_sets(
        write_sets: Iterable[PersistenceWriteSet],
    ) -> PersistenceWriteSet:
        ws_list = list(write_sets)
        return PersistenceWriteSet(
            bank_accounts=tuple(
                a for ws in ws_list for a in ws.bank_accounts
            ),
            bank_items=tuple(
                a for ws in ws_list for a in ws.bank_items
            ),
            book_items=tuple(
                a for ws in ws_list for a in ws.book_items
            ),
            documents=tuple(
                a for ws in ws_list for a in ws.documents
            ),
            counterparties=tuple(
                a for ws in ws_list for a in ws.counterparties
            ),
            routing_decisions=tuple(
                a for ws in ws_list for a in ws.routing_decisions
            ),
            routing_invalidations=tuple(
                a for ws in ws_list for a in ws.routing_invalidations
            ),
            classifications=tuple(
                a for ws in ws_list for a in ws.classifications
            ),
            classification_invalidations=tuple(
                a for ws in ws_list for a in ws.classification_invalidations
            ),
            reconciliations=tuple(
                a for ws in ws_list for a in ws.reconciliations
            ),
            reconciliation_invalidations=tuple(
                a for ws in ws_list for a in ws.reconciliation_invalidations
            ),
        )

    @staticmethod
    def _build_preview_state(
        *,
        state: BookkeepingState,
        write_set: PersistenceWriteSet,
    ) -> BookkeepingState:
        """
        Construct the exact authoritative world expected after commit.

        Runtime hypotheses/events are deliberately not copied because they do
        not participate in durable state validity.
        """

        preview_runtime: RuntimeContext = (
            state.runtime.model_copy(
                update={
                    "state_revision": (
                        state.revision + 1
                    ),
                    "persistence_revision": (
                        state.persistence_revision + 1
                    ),
                }
            )
        )

        return BookkeepingState(
            context=state.context,
            runtime=preview_runtime,

            bank_accounts=(
                tuple(state.bank_accounts.values())
                + write_set.bank_accounts
            ),

            bank_items=(
                tuple(state.bank_items.values())
                + write_set.bank_items
            ),

            book_items=(
                tuple(state.book_items.values())
                + write_set.book_items
            ),

            documents=(
                tuple(state.documents.values())
                + write_set.documents
            ),

            counterparties=(
                tuple(state.counterparties.values())
                + write_set.counterparties
            ),

            routing_decisions=(
                tuple(
                    state.routing_decisions.values()
                )
                + write_set.routing_decisions
            ),

            routing_invalidations=(
                tuple(
                    state.routing_invalidations.values()
                )
                + write_set.routing_invalidations
            ),

            classifications=(
                tuple(
                    state.classifications.values()
                )
                + write_set.classifications
            ),

            classification_invalidations=(
                tuple(
                    state
                    .classification_invalidations
                    .values()
                )
                + write_set.classification_invalidations
            ),

            reconciliations=(
                tuple(
                    state.reconciliations.values()
                )
                + write_set.reconciliations
            ),

            reconciliation_invalidations=(
                tuple(
                    state
                    .reconciliation_invalidations
                    .values()
                )
                + write_set.reconciliation_invalidations
            ),
        )

    # ==================================================================
    # Repository commit helpers
    # ==================================================================

    def _commit_noop(
        self,
        *,
        state: BookkeepingState,
    ) -> tuple[
        PersistenceCommitResult | None,
        tuple[RejectionCode, str] | None,
    ]:
        try:
            commit = self._repository.commit(
                company_id=state.context.company_id,
                expected_revision=state.persistence_revision,
                write_set=PersistenceWriteSet(),
            )
        except PersistenceConflictError as exc:
            return None, (
                RejectionCode.PERSISTENCE_REVISION_CONFLICT,
                str(exc),
            )
        except PersistenceError as exc:
            return None, (
                RejectionCode.PERSISTENCE_FAILURE,
                str(exc),
            )

        try:
            self._assert_noop_commit_contract(
                state=state,
                commit=commit,
            )
        except Exception as exc:
            try:
                state.close()
            except Exception:
                pass
            raise CommittedStateApplicationError(
                commit=commit,
                cause=exc,
            ) from exc

        return commit, None

    def _commit_write_set(
        self,
        *,
        state: BookkeepingState,
        write_set: PersistenceWriteSet,
        expected_revision: int,
    ) -> tuple[
        PersistenceCommitResult | None,
        tuple[RejectionCode, str] | None,
    ]:
        try:
            commit = self._repository.commit(
                company_id=state.context.company_id,
                expected_revision=expected_revision,
                write_set=write_set,
            )
        except PersistenceConflictError as exc:
            return None, (
                RejectionCode.PERSISTENCE_REVISION_CONFLICT,
                str(exc),
            )
        except PersistenceDuplicateError as exc:
            return None, (
                RejectionCode.DUPLICATE_ARTIFACT,
                str(exc),
            )
        except PersistenceError as exc:
            return None, (
                RejectionCode.PERSISTENCE_FAILURE,
                str(exc),
            )

        return commit, None

    # ==================================================================
    # Live application & post-commit synchronization
    # ==================================================================

    def _synchronize_committed_state(
        self,
        *,
        state: BookkeepingState,
        commit: PersistenceCommitResult,
        events: tuple[StateEvent, ...],
        previous_state_revision: int,
        clear_all_hypotheses: bool,
    ) -> StateDelta:
        """
        Synchronize a successfully committed durable mutation to the live state.

        If any error occurs during commit contract assertion, StateDelta construction,
        or in-memory state application:
            1. The live BookkeepingState is forcibly closed to prevent stale reads
               or corrupted mutations.
            2. CommittedStateApplicationError is raised, preserving the original
               exception as __cause__.
        """
        try:
            self._assert_commit_contract(
                expected_previous_revision=state.persistence_revision,
                commit=commit,
            )

            delta = StateDelta.from_commit(
                previous_state_revision=previous_state_revision,
                commit=commit,
                events=events,
                clear_all_hypotheses=clear_all_hypotheses,
            )

            self._apply_delta(
                state=state,
                delta=delta,
            )
            return delta
        except Exception as exc:
            try:
                state.close()
            except Exception:
                pass
            raise CommittedStateApplicationError(
                commit=commit,
                cause=exc,
            ) from exc

    @staticmethod
    def _apply_delta(
        *,
        state: BookkeepingState,
        delta: StateDelta,
    ) -> None:
        """
        Apply a successfully committed durable mutation to the live state.

        This method intentionally uses BookkeepingState's internal insertion
        API. No caller outside the transition layer should use these methods.
        """

        if (
            state.revision
            != delta.previous_state_revision
        ):
            raise StateRevisionConflictError(
                "Live state changed between persistence "
                "commit and delta application: "
                f"expected="
                f"{delta.previous_state_revision}, "
                f"actual={state.revision}"
            )

        if (
            state.persistence_revision
            != delta.previous_persistence_revision
        ):
            raise StateRevisionConflictError(
                "Live persistence revision changed between "
                "commit and delta application: "
                f"expected="
                f"{delta.previous_persistence_revision}, "
                f"actual={state.persistence_revision}"
            )

        # --------------------------------------------------------------
        # Apply durable artifacts
        # --------------------------------------------------------------

        for artifact in delta.bank_accounts:
            state._insert_bank_account(artifact)

        for artifact in delta.bank_items:
            state._insert_bank_item(artifact)

        for artifact in delta.book_items:
            state._insert_book_item(artifact)

        for artifact in delta.documents:
            state._insert_document(artifact)

        for artifact in delta.counterparties:
            state._insert_counterparty(artifact)

        for artifact in delta.routing_decisions:
            state._insert_routing_decision(
                artifact
            )

        for artifact in delta.routing_invalidations:
            state._insert_routing_invalidation(
                artifact
            )

        for artifact in delta.classifications:
            state._insert_classification(
                artifact
            )

        for artifact in (
            delta.classification_invalidations
        ):
            state._insert_classification_invalidation(
                artifact
            )

        for artifact in delta.reconciliations:
            state._insert_reconciliation(
                artifact
            )

        for artifact in (
            delta.reconciliation_invalidations
        ):
            state._insert_reconciliation_invalidation(
                artifact
            )

        for artifact in delta.book_item_evidence_assertions:
            state._insert_book_item_evidence_assertion(
                artifact
            )

        for artifact in (
            delta.book_item_evidence_invalidations
        ):
            state._insert_book_item_evidence_invalidation(
                artifact
            )

        # --------------------------------------------------------------
        # Runtime hypothesis invalidation
        # --------------------------------------------------------------

        if delta.clear_all_hypotheses:
            state._clear_reconciliation_hypotheses()

        else:
            for hypothesis_id in (
                delta.invalidated_hypothesis_ids
            ):
                state._drop_reconciliation_hypothesis(
                    hypothesis_id
                )

        # --------------------------------------------------------------
        # Revision moves only after all artifact insertion succeeds.
        # --------------------------------------------------------------

        resulting_revision = (
            state._advance_revision(
                expected_previous_revision=(
                    delta.previous_state_revision
                ),
                new_persistence_revision=(
                    delta.new_persistence_revision
                ),
            )
        )

        if (
            resulting_revision
            != delta.new_state_revision
        ):
            raise RepositoryContractError(
                "StateDelta revision does not match "
                "BookkeepingState revision after application"
            )

        # --------------------------------------------------------------
        # Runtime events belong to the new revision.
        # --------------------------------------------------------------

        for event in delta.events:
            state._append_event(event)

    # ==================================================================
    # Events
    # ==================================================================

    @classmethod
    def _build_events(
        cls,
        *,
        state: BookkeepingState,
        command: BookkeepingCommand,
        write_set: PersistenceWriteSet,
        invalidated_hypothesis_ids: tuple[str, ...],
    ) -> tuple[StateEvent, ...]:
        new_revision = state.revision + 1
        events = cls._build_events_for_command(
            new_revision=new_revision,
            command=command,
            write_set=write_set,
        )

        if invalidated_hypothesis_ids:
            events.append(
                StateEvent(
                    id=f"{command.command_id}:event:{len(events) + 1}",
                    event_type=StateEventType.HYPOTHESES_INVALIDATED,
                    state_revision=new_revision,
                    affected_artifact_ids=invalidated_hypothesis_ids,
                    source_command_id=command.command_id,
                    occurred_at=command.issued_at,
                )
            )

        return tuple(events)

    @classmethod
    def _build_batch_events(
        cls,
        *,
        state: BookkeepingState,
        batch: TransitionBatch,
        dispatched: list[
            tuple[BookkeepingCommand, PersistenceWriteSet]
        ],
        invalidated_hypothesis_ids: tuple[str, ...],
    ) -> tuple[StateEvent, ...]:
        new_revision = state.revision + 1
        events: list[StateEvent] = []

        # Emit events for each sibling command preserving source command attribution
        for command, write_set in dispatched:
            cmd_events = cls._build_events_for_command(
                new_revision=new_revision,
                command=command,
                write_set=write_set,
            )
            events.extend(cmd_events)

        # Invalidate runtime reconciliation hypotheses exactly once for the batch
        if invalidated_hypothesis_ids:
            first_cmd = batch.commands[0]
            events.append(
                StateEvent(
                    id=f"{batch.batch_id}:event:hypotheses",
                    event_type=StateEventType.HYPOTHESES_INVALIDATED,
                    state_revision=new_revision,
                    affected_artifact_ids=invalidated_hypothesis_ids,
                    source_command_id=first_cmd.command_id,
                    occurred_at=first_cmd.issued_at,
                )
            )

        return tuple(events)

    @staticmethod
    def _build_events_for_command(
        *,
        new_revision: int,
        command: BookkeepingCommand,
        write_set: PersistenceWriteSet,
    ) -> list[StateEvent]:
        events: list[StateEvent] = []
        event_index = 0

        def append_event(
            event_type: StateEventType,
            artifact_ids: tuple[str, ...],
        ) -> None:
            nonlocal event_index
            event_index += 1
            events.append(
                StateEvent(
                    id=(
                        f"{command.command_id}:"
                        f"event:{event_index}"
                    ),
                    event_type=event_type,
                    state_revision=new_revision,
                    affected_artifact_ids=artifact_ids,
                    source_command_id=command.command_id,
                    occurred_at=command.issued_at,
                )
            )

        # Routing
        for decision in write_set.routing_decisions:
            if decision.supersedes_routing_decision_id is not None:
                append_event(
                    StateEventType.ROUTING_DECISION_SUPERSEDED,
                    (
                        decision.id,
                        decision.supersedes_routing_decision_id,
                    ),
                )
            else:
                append_event(
                    StateEventType.ROUTING_DECISION_CREATED,
                    (
                        decision.id,
                    ),
                )

        for invalidation in write_set.routing_invalidations:
            append_event(
                StateEventType.ROUTING_DECISION_INVALIDATED,
                (
                    invalidation.id,
                    invalidation.routing_decision_id,
                ),
            )

        # Classification
        for classification in write_set.classifications:
            if classification.supersedes_classification_id is not None:
                append_event(
                    StateEventType.CLASSIFICATION_SUPERSEDED,
                    (
                        classification.id,
                        classification.supersedes_classification_id,
                    ),
                )
            else:
                append_event(
                    StateEventType.CLASSIFICATION_CREATED,
                    (
                        classification.id,
                    ),
                )

        for invalidation in write_set.classification_invalidations:
            append_event(
                StateEventType.CLASSIFICATION_INVALIDATED,
                (
                    invalidation.id,
                    invalidation.classification_id,
                ),
            )

        # Reconciliation
        for reconciliation in write_set.reconciliations:
            append_event(
                StateEventType.RECONCILIATION_CREATED,
                (
                    reconciliation.id,
                ),
            )

        for invalidation in write_set.reconciliation_invalidations:
            append_event(
                StateEventType.RECONCILIATION_INVALIDATED,
                (
                    invalidation.id,
                    invalidation.reconciliation_id,
                ),
            )

        # Evidence
        for assertion in write_set.book_item_evidence_assertions:
            if assertion.supersedes_assertion_id is not None:
                append_event(
                    StateEventType.BOOK_ITEM_EVIDENCE_SUPERSEDED,
                    (
                        assertion.id,
                        assertion.supersedes_assertion_id,
                    ),
                )
            else:
                append_event(
                    StateEventType.BOOK_ITEM_EVIDENCE_ASSERTED,
                    (
                        assertion.id,
                    ),
                )

        for invalidation in write_set.book_item_evidence_invalidations:
            append_event(
                StateEventType.BOOK_ITEM_EVIDENCE_INVALIDATED,
                (
                    invalidation.id,
                    invalidation.assertion_id,
                ),
            )

        return events

    # ==================================================================
    # Repository contract checks
    # ==================================================================

    @staticmethod
    def _assert_commit_contract(
        *,
        expected_previous_revision: int,
        commit: PersistenceCommitResult,
    ) -> None:
        if (
            commit.previous_revision
            != expected_previous_revision
        ):
            raise RepositoryContractError(
                "Repository returned incorrect previous "
                "revision: "
                f"expected={expected_previous_revision}, "
                f"actual={commit.previous_revision}"
            )

        if (
            commit.new_revision
            != commit.previous_revision + 1
        ):
            raise RepositoryContractError(
                "Non-empty repository commit must advance "
                "persistence revision exactly once: "
                f"previous={commit.previous_revision}, "
                f"new={commit.new_revision}"
            )

        if commit.write_set.is_empty:
            raise RepositoryContractError(
                "Non-empty transition received empty "
                "PersistenceCommitResult.write_set"
            )

    @staticmethod
    def _assert_noop_commit_contract(
        *,
        state: BookkeepingState,
        commit: PersistenceCommitResult,
    ) -> None:
        if not commit.write_set.is_empty:
            raise RepositoryContractError(
                "Repository returned non-empty write set "
                "for no-op commit"
            )

        if (
            commit.previous_revision
            != state.persistence_revision
        ):
            raise RepositoryContractError(
                "No-op commit returned unexpected previous "
                "persistence revision"
            )

        if (
            commit.new_revision
            != state.persistence_revision
        ):
            raise RepositoryContractError(
                "No-op repository commit must not advance "
                "persistence revision"
            )

    # ==================================================================
    # Rejection helper
    # ==================================================================

    @staticmethod
    def _reject(
        *,
        state: BookkeepingState,
        command: BookkeepingCommand,
        code: RejectionCode,
        message: str,
        artifact_ids: tuple[str, ...] = (),
    ) -> TransitionResult:
        state_rev = 0 if state.is_closed else state.revision
        persistence_rev = (
            0 if state.is_closed else state.persistence_revision
        )
        return TransitionResult.rejected_result(
            command=command,
            rejection=TransitionRejection(
                code=code,
                message=message,
                artifact_ids=artifact_ids,
            ),
            state_revision=state_rev,
            persistence_revision=persistence_rev,
        )