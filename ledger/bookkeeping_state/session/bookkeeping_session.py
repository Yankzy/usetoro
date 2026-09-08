from __future__ import annotations

import logging
from datetime import datetime, timezone

from bookkeeping_state.dag.adapter import dag_plan_to_transition_batch
from bookkeeping_state.dag.protocol import AseClassifier
from bookkeeping_state.dag.view import build_dag_view
from bookkeeping_state.reconciliation.models import (
    reconciliation_plan_to_transition_batch,
)
from bookkeeping_state.reconciliation.service import ReconciliationService
from bookkeeping_state.routing.service import (
    RoutingService,
    routing_plan_to_transition_batch,
)
from bookkeeping_state.session.result import (
    DagHoldSummary,
    DagProviderIssueSummary,
    FailureStage,
    SessionBatchSummary,
    SessionResult,
    SessionStageResult,
    SessionStageStatus,
)
from bookkeeping_state.state.bookkeeping_state import BookkeepingState
from bookkeeping_state.state.fingerprint import (
    artifact_fingerprint,
    state_fingerprint,
)
from bookkeeping_state.state.queries import BookkeepingQueries
from bookkeeping_state.state.validation import (
    StateValidationReport,
    validate_state,
)
from bookkeeping_state.transitions.batch import TransitionBatchError
from bookkeeping_state.transitions.engine import (
    CommittedStateApplicationError,
    TransitionEngine,
)


logger = logging.getLogger(__name__)


class BookkeepingSession:
    """
    Orchestrates the full lifecycle of a Bookkeeping evaluation run.

    Coordinates sequential execution of:
    1. Routing
    2. DAG Classification
    3. Reconciliation

    Ensures that truth remains consistent, cleanly handles partial failures
    by preserving durable history, and strictly manages the live state lifecycle.
    """

    def __init__(
        self,
        *,
        state: BookkeepingState,
        engine: TransitionEngine,
        routing_service: RoutingService,
        dag_classifier: AseClassifier,
        reconciliation_service: ReconciliationService,
    ) -> None:
        self._state = state
        self._engine = engine
        self._routing_service = routing_service
        self._dag_classifier = dag_classifier
        self._reconciliation_service = reconciliation_service

    def run(self) -> SessionResult:
        """
        Execute the full bookkeeping session.

        The provided BookkeepingState is always closed when this method returns,
        meaning it can no longer be mutated or queried. The SessionResult
        contains final state fingerprints and revision markers for validation.
        """
        company_id = self._state.context.company_id
        session_id = self._state.session_id
        starting_persistence_revision = self._state.persistence_revision

        last_consistent_local_revision: int = self._state.revision
        current_persistence_revision: int = starting_persistence_revision

        routing_stage_res: SessionStageResult | None = None
        dag_stage_res: SessionStageResult | None = None
        recon_stage_res: SessionStageResult | None = None

        issued_at = datetime.now(timezone.utc)
        run_id_base = session_id

        try:
            # -----------------------------------------------------------------
            # 1. Routing Stage
            # -----------------------------------------------------------------
            base_rev = self._state.revision
            last_consistent_local_revision = base_rev
            routing_batch = None
            try:
                routing_queries = BookkeepingQueries(self._state)
                routing_plan = self._routing_service.run(
                    queries=routing_queries,
                    solver_run_id=f"{run_id_base}-routing",
                    issued_at=issued_at,
                )

                if not routing_plan.commands:
                    # zero-command stage: SKIPPED
                    routing_stage_res = SessionStageResult.skipped(
                        state_revision=base_rev
                    )
                else:
                    routing_batch = routing_plan_to_transition_batch(routing_plan)
                    batch_res = self._engine.apply_batch(
                        state=self._state,
                        batch=routing_batch,
                    )
                    batch_summary = SessionBatchSummary.from_batch_result(batch_res)
                    if batch_res.is_applied:
                        routing_stage_res = SessionStageResult.applied(
                            base_state_revision=base_rev,
                            resulting_state_revision=self._state.revision,
                            batch_summary=batch_summary,
                        )
                        last_consistent_local_revision = self._state.revision
                        current_persistence_revision = self._state.persistence_revision
                    else:
                        # ordinary rejected batch: REJECTED
                        routing_stage_res = SessionStageResult.rejected(
                            base_state_revision=base_rev,
                            resulting_state_revision=self._state.revision,
                            batch_summary=batch_summary,
                        )
                        return self._build_terminal_result(
                            company_id=company_id,
                            session_id=session_id,
                            starting_persistence_revision=starting_persistence_revision,
                            final_persistence_revision=self._state.persistence_revision,
                            final_local_state_revision=self._state.revision,
                            last_consistent_local_state_revision=last_consistent_local_revision,
                            routing_res=routing_stage_res,
                            dag_res=SessionStageResult.not_run(
                                state_revision=self._state.revision
                            ),
                            recon_res=SessionStageResult.not_run(
                                state_revision=self._state.revision
                            ),
                            validation_report=None,
                            is_success=False,
                            failure_stage=FailureStage.ROUTING,
                            failure_reason=(
                                batch_res.rejection.message
                                if batch_res.rejection
                                else "Unknown routing rejection"
                            ),
                        )
            except CommittedStateApplicationError as exc:
                logger.error(
                    "Catastrophic error applying committed state in routing: %s",
                    exc,
                )
                catastrophic_summary = SessionBatchSummary.from_catastrophic_error(
                    batch_id=routing_batch.batch_id if routing_batch else None,
                    command_count=len(routing_batch.commands) if routing_batch else 0,
                    command_ids=tuple(cmd.command_id for cmd in routing_batch.commands) if routing_batch else (),
                    command_types=tuple(
                        cmd.command_type.value if hasattr(cmd.command_type, "value") else str(cmd.command_type)
                        for cmd in routing_batch.commands
                    ) if routing_batch else (),
                    previous_state_revision=base_rev,
                    previous_persistence_revision=current_persistence_revision,
                    committed_persistence_revision=exc.commit.new_revision,
                    cause_message=str(exc),
                )
                routing_stage_res = SessionStageResult.catastrophic(
                    base_state_revision=base_rev,
                    batch_summary=catastrophic_summary,
                )
                return self._build_catastrophic_result(
                    company_id=company_id,
                    session_id=session_id,
                    starting_persistence_revision=starting_persistence_revision,
                    final_persistence_revision=exc.commit.new_revision,
                    last_consistent_local_state_revision=last_consistent_local_revision,
                    routing_res=routing_stage_res,
                    dag_res=SessionStageResult.not_run(state_revision=None),
                    recon_res=SessionStageResult.not_run(state_revision=None),
                    failure_reason=str(exc),
                )

            # -----------------------------------------------------------------
            # 2. DAG Stage
            # -----------------------------------------------------------------
            base_rev = self._state.revision
            last_consistent_local_revision = base_rev
            dag_batch = None
            try:
                dag_queries = BookkeepingQueries(self._state)
                dag_view = build_dag_view(dag_queries)
                if not dag_view.items:
                    # A. No eligible items presented to ASE: SKIPPED
                    dag_stage_res = SessionStageResult.skipped(
                        state_revision=base_rev
                    )
                else:
                    try:
                        dag_plan = self._dag_classifier.classify_view(dag_view)
                    except Exception as exc:
                        logger.error("DAG provider invocation failed: %s", exc)
                        dag_stage_res = SessionStageResult.failed(
                            base_state_revision=base_rev,
                            resulting_state_revision=base_rev,
                        )
                        return self._build_terminal_result(
                            company_id=company_id,
                            session_id=session_id,
                            starting_persistence_revision=starting_persistence_revision,
                            final_persistence_revision=self._state.persistence_revision,
                            final_local_state_revision=self._state.revision,
                            last_consistent_local_state_revision=last_consistent_local_revision,
                            routing_res=routing_stage_res,
                            dag_res=dag_stage_res,
                            recon_res=SessionStageResult.not_run(
                                state_revision=self._state.revision
                            ),
                            validation_report=None,
                            is_success=False,
                            failure_stage=FailureStage.DAG,
                            failure_reason=str(exc),
                        )

                    eval_book_ids = {it.book_item_id for it in dag_plan.items} | {
                        h.book_item_id for h in dag_plan.hold_items
                    }
                    provider_issues = tuple(
                        DagProviderIssueSummary(
                            book_item_id=v.book_item_id,
                            code="MISSING_PROVIDER_OUTPUT",
                            message="No semantic determination returned by ASE",
                        )
                        for v in dag_view.items
                        if v.book_item_id not in eval_book_ids
                    )
                    hold_summaries = tuple(
                        DagHoldSummary(
                            book_item_id=h.book_item_id,
                            reason=h.reason,
                            rationale=h.rationale,
                            ase_node_id=h.ase_node_id,
                        )
                        for h in dag_plan.hold_items
                    )

                    if not dag_plan.items:
                        if provider_issues:
                            # Expected provider outputs were missing without a valid semantic HOLD: INCOMPLETE
                            dag_stage_res = SessionStageResult.incomplete(
                                state_revision=base_rev,
                                holds=hold_summaries,
                                provider_issues=provider_issues,
                            )
                        else:
                            # B. Eligible items evaluated, but all were semantic HOLD: HELD
                            dag_stage_res = SessionStageResult.held(
                                state_revision=base_rev,
                                holds=hold_summaries,
                            )
                    else:
                        # C. Mixed classifications + HOLD / provider issues: APPLIED if batch commits
                        try:
                            dag_batch = dag_plan_to_transition_batch(
                                dag_plan,
                                view=dag_view,
                                issued_at=issued_at,
                            )
                        except TransitionBatchError as exc:
                            logger.error("DAG plan validation rejected: %s", exc)
                            dag_stage_res = SessionStageResult.rejected(
                                base_state_revision=base_rev,
                                resulting_state_revision=self._state.revision,
                                batch_summary=None,
                                holds=hold_summaries,
                                provider_issues=provider_issues,
                            )
                            return self._build_terminal_result(
                                company_id=company_id,
                                session_id=session_id,
                                starting_persistence_revision=starting_persistence_revision,
                                final_persistence_revision=self._state.persistence_revision,
                                final_local_state_revision=self._state.revision,
                                last_consistent_local_state_revision=last_consistent_local_revision,
                                routing_res=routing_stage_res,
                                dag_res=dag_stage_res,
                                recon_res=SessionStageResult.not_run(
                                    state_revision=self._state.revision
                                ),
                                validation_report=None,
                                is_success=False,
                                failure_stage=FailureStage.DAG,
                                failure_reason=str(exc),
                            )

                        batch_res = self._engine.apply_batch(
                            state=self._state,
                            batch=dag_batch,
                        )
                        batch_summary = SessionBatchSummary.from_batch_result(batch_res)
                        if batch_res.is_applied:
                            dag_stage_res = SessionStageResult.applied(
                                base_state_revision=base_rev,
                                resulting_state_revision=self._state.revision,
                                batch_summary=batch_summary,
                                holds=hold_summaries,
                                provider_issues=provider_issues,
                            )
                            last_consistent_local_revision = self._state.revision
                            current_persistence_revision = self._state.persistence_revision
                        else:
                            # ordinary rejected batch: REJECTED
                            dag_stage_res = SessionStageResult.rejected(
                                base_state_revision=base_rev,
                                resulting_state_revision=self._state.revision,
                                batch_summary=batch_summary,
                                holds=hold_summaries,
                                provider_issues=provider_issues,
                            )
                            return self._build_terminal_result(
                                company_id=company_id,
                                session_id=session_id,
                                starting_persistence_revision=starting_persistence_revision,
                                final_persistence_revision=self._state.persistence_revision,
                                final_local_state_revision=self._state.revision,
                                last_consistent_local_state_revision=last_consistent_local_revision,
                                routing_res=routing_stage_res,
                                dag_res=dag_stage_res,
                                recon_res=SessionStageResult.not_run(
                                    state_revision=self._state.revision
                                ),
                                validation_report=None,
                                is_success=False,
                                failure_stage=FailureStage.DAG,
                                failure_reason=(
                                    batch_res.rejection.message
                                    if batch_res.rejection
                                    else "Unknown DAG rejection"
                                ),
                            )
            except CommittedStateApplicationError as exc:
                logger.error(
                    "Catastrophic error applying committed state in DAG: %s",
                    exc,
                )
                catastrophic_summary = SessionBatchSummary.from_catastrophic_error(
                    batch_id=dag_batch.batch_id if dag_batch else None,
                    command_count=len(dag_batch.commands) if dag_batch else 0,
                    command_ids=tuple(cmd.command_id for cmd in dag_batch.commands) if dag_batch else (),
                    command_types=tuple(
                        cmd.command_type.value if hasattr(cmd.command_type, "value") else str(cmd.command_type)
                        for cmd in dag_batch.commands
                    ) if dag_batch else (),
                    previous_state_revision=base_rev,
                    previous_persistence_revision=current_persistence_revision,
                    committed_persistence_revision=exc.commit.new_revision,
                    cause_message=str(exc),
                )
                dag_stage_res = SessionStageResult.catastrophic(
                    base_state_revision=base_rev,
                    batch_summary=catastrophic_summary,
                )
                return self._build_catastrophic_result(
                    company_id=company_id,
                    session_id=session_id,
                    starting_persistence_revision=starting_persistence_revision,
                    final_persistence_revision=exc.commit.new_revision,
                    last_consistent_local_state_revision=last_consistent_local_revision,
                    routing_res=routing_stage_res,
                    dag_res=dag_stage_res,
                    recon_res=SessionStageResult.not_run(state_revision=None),
                    failure_reason=str(exc),
                )
            except TransitionBatchError as exc:
                logger.error("DAG stage validation rejected: %s", exc)
                dag_stage_res = SessionStageResult.rejected(
                    base_state_revision=base_rev,
                    resulting_state_revision=self._state.revision,
                    batch_summary=None,
                )
                return self._build_terminal_result(
                    company_id=company_id,
                    session_id=session_id,
                    starting_persistence_revision=starting_persistence_revision,
                    final_persistence_revision=self._state.persistence_revision,
                    final_local_state_revision=self._state.revision,
                    last_consistent_local_state_revision=last_consistent_local_revision,
                    routing_res=routing_stage_res,
                    dag_res=dag_stage_res,
                    recon_res=SessionStageResult.not_run(
                        state_revision=self._state.revision
                    ),
                    validation_report=None,
                    is_success=False,
                    failure_stage=FailureStage.DAG,
                    failure_reason=str(exc),
                )
            except Exception as exc:
                logger.error("DAG stage execution failed: %s", exc)
                dag_stage_res = SessionStageResult.failed(
                    base_state_revision=base_rev,
                    resulting_state_revision=self._state.revision,
                )
                return self._build_terminal_result(
                    company_id=company_id,
                    session_id=session_id,
                    starting_persistence_revision=starting_persistence_revision,
                    final_persistence_revision=self._state.persistence_revision,
                    final_local_state_revision=self._state.revision,
                    last_consistent_local_state_revision=last_consistent_local_revision,
                    routing_res=routing_stage_res,
                    dag_res=dag_stage_res,
                    recon_res=SessionStageResult.not_run(
                        state_revision=self._state.revision
                    ),
                    validation_report=None,
                    is_success=False,
                    failure_stage=FailureStage.DAG,
                    failure_reason=str(exc),
                )

            # -----------------------------------------------------------------
            # 3. Reconciliation Stage
            # -----------------------------------------------------------------
            base_rev = self._state.revision
            last_consistent_local_revision = base_rev
            recon_batch = None
            try:
                recon_queries = BookkeepingQueries(self._state)
                recon_plan = self._reconciliation_service.plan(
                    queries=recon_queries,
                    solver_run_id=f"{run_id_base}-recon",
                    session_id=session_id,
                    issued_at=issued_at,
                )

                if not recon_plan.commands:
                    # zero-command stage: SKIPPED
                    recon_stage_res = SessionStageResult.skipped(
                        state_revision=base_rev
                    )
                else:
                    recon_batch = reconciliation_plan_to_transition_batch(
                        recon_plan,
                        batch_id=f"batch:recon:{recon_plan.solver_run_id}",
                    )
                    batch_res = self._engine.apply_batch(
                        state=self._state,
                        batch=recon_batch,
                    )
                    batch_summary = SessionBatchSummary.from_batch_result(batch_res)
                    if batch_res.is_applied:
                        recon_stage_res = SessionStageResult.applied(
                            base_state_revision=base_rev,
                            resulting_state_revision=self._state.revision,
                            batch_summary=batch_summary,
                        )
                        last_consistent_local_revision = self._state.revision
                        current_persistence_revision = self._state.persistence_revision
                    else:
                        # ordinary rejected batch: REJECTED
                        recon_stage_res = SessionStageResult.rejected(
                            base_state_revision=base_rev,
                            resulting_state_revision=self._state.revision,
                            batch_summary=batch_summary,
                        )
                        return self._build_terminal_result(
                            company_id=company_id,
                            session_id=session_id,
                            starting_persistence_revision=starting_persistence_revision,
                            final_persistence_revision=self._state.persistence_revision,
                            final_local_state_revision=self._state.revision,
                            last_consistent_local_state_revision=last_consistent_local_revision,
                            routing_res=routing_stage_res,
                            dag_res=dag_stage_res,
                            recon_res=recon_stage_res,
                            validation_report=None,
                            is_success=False,
                            failure_stage=FailureStage.RECONCILIATION,
                            failure_reason=(
                                batch_res.rejection.message
                                if batch_res.rejection
                                else "Unknown recon rejection"
                            ),
                        )
            except CommittedStateApplicationError as exc:
                logger.error(
                    "Catastrophic error applying committed state in reconciliation: %s",
                    exc,
                )
                catastrophic_summary = SessionBatchSummary.from_catastrophic_error(
                    batch_id=recon_batch.batch_id if recon_batch else None,
                    command_count=len(recon_batch.commands) if recon_batch else 0,
                    command_ids=tuple(cmd.command_id for cmd in recon_batch.commands) if recon_batch else (),
                    command_types=tuple(
                        cmd.command_type.value if hasattr(cmd.command_type, "value") else str(cmd.command_type)
                        for cmd in recon_batch.commands
                    ) if recon_batch else (),
                    previous_state_revision=base_rev,
                    previous_persistence_revision=current_persistence_revision,
                    committed_persistence_revision=exc.commit.new_revision,
                    cause_message=str(exc),
                )
                recon_stage_res = SessionStageResult.catastrophic(
                    base_state_revision=base_rev,
                    batch_summary=catastrophic_summary,
                )
                return self._build_catastrophic_result(
                    company_id=company_id,
                    session_id=session_id,
                    starting_persistence_revision=starting_persistence_revision,
                    final_persistence_revision=exc.commit.new_revision,
                    last_consistent_local_state_revision=last_consistent_local_revision,
                    routing_res=routing_stage_res,
                    dag_res=dag_stage_res,
                    recon_res=recon_stage_res,
                    failure_reason=str(exc),
                )

            # -----------------------------------------------------------------
            # 4. Final Validation
            # -----------------------------------------------------------------
            validation_report = validate_state(self._state)
            if not validation_report.is_valid:
                messages = [
                    f"{issue.code.value}: {issue.message}"
                    for issue in validation_report.errors
                ]
                failure_reason = "; ".join(messages)
                return self._build_terminal_result(
                    company_id=company_id,
                    session_id=session_id,
                    starting_persistence_revision=starting_persistence_revision,
                    final_persistence_revision=self._state.persistence_revision,
                    final_local_state_revision=self._state.revision,
                    last_consistent_local_state_revision=self._state.revision,
                    routing_res=routing_stage_res,
                    dag_res=dag_stage_res,
                    recon_res=recon_stage_res,
                    validation_report=validation_report,
                    is_success=False,
                    failure_stage=FailureStage.VALIDATION,
                    failure_reason=failure_reason,
                )

            return self._build_terminal_result(
                company_id=company_id,
                session_id=session_id,
                starting_persistence_revision=starting_persistence_revision,
                final_persistence_revision=self._state.persistence_revision,
                final_local_state_revision=self._state.revision,
                last_consistent_local_state_revision=self._state.revision,
                routing_res=routing_stage_res,
                dag_res=dag_stage_res,
                recon_res=recon_stage_res,
                validation_report=validation_report,
                is_success=True,
                failure_stage=None,
                failure_reason=None,
            )

        finally:
            if not self._state.is_closed:
                self._state.close()

    def _build_terminal_result(
        self,
        *,
        company_id: str,
        session_id: str,
        starting_persistence_revision: int,
        final_persistence_revision: int,
        final_local_state_revision: int,
        last_consistent_local_state_revision: int,
        routing_res: SessionStageResult | None,
        dag_res: SessionStageResult | None,
        recon_res: SessionStageResult | None,
        validation_report: StateValidationReport | None,
        is_success: bool,
        failure_stage: FailureStage | None,
        failure_reason: str | None,
    ) -> SessionResult:
        """
        Build a detached SessionResult for successful runs or ordinary domain failures.
        Captures fingerprints and revisions before closing the live state.
        """
        art_fp: str | None = None
        st_fp: str | None = None

        if not self._state.is_closed:
            try:
                art_fp = artifact_fingerprint(self._state)
                st_fp = state_fingerprint(self._state)
            except Exception:
                pass
            self._state.close()

        return SessionResult(
            company_id=company_id,
            session_id=session_id,
            starting_persistence_revision=starting_persistence_revision,
            final_persistence_revision=final_persistence_revision,
            final_local_state_revision=final_local_state_revision,
            last_consistent_local_state_revision=last_consistent_local_state_revision,
            routing_stage_result=routing_res,
            dag_stage_result=dag_res,
            reconciliation_stage_result=recon_res,
            final_validation_status=validation_report,
            artifact_fingerprint=art_fp,
            state_fingerprint=st_fp,
            is_success=is_success,
            failure_stage=failure_stage,
            failure_reason=failure_reason,
        )

    def _build_catastrophic_result(
        self,
        *,
        company_id: str,
        session_id: str,
        starting_persistence_revision: int,
        final_persistence_revision: int,
        last_consistent_local_state_revision: int,
        routing_res: SessionStageResult | None,
        dag_res: SessionStageResult | None,
        recon_res: SessionStageResult | None,
        failure_reason: str,
    ) -> SessionResult:
        """
        Build a detached SessionResult for catastrophic CommittedStateApplicationError.

        CRITICAL: BookkeepingState is already closed by TransitionEngine and must
        NEVER be accessed again. No properties, queries, or fingerprints are read
        from the closed state.
        """
        return SessionResult(
            company_id=company_id,
            session_id=session_id,
            starting_persistence_revision=starting_persistence_revision,
            final_persistence_revision=final_persistence_revision,
            final_local_state_revision=None,
            last_consistent_local_state_revision=last_consistent_local_state_revision,
            routing_stage_result=routing_res,
            dag_stage_result=dag_res,
            reconciliation_stage_result=recon_res,
            final_validation_status=None,
            artifact_fingerprint=None,
            state_fingerprint=None,
            is_success=False,
            failure_stage=FailureStage.COMMITTED_STATE_APPLICATION,
            failure_reason=failure_reason,
        )
