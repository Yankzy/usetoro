from __future__ import annotations

import logging
from datetime import datetime, timezone

from bookkeeping_state.bank_categorization.nats_bank_categorizer import (
    NatsAseBankCategorizer,
)
from bookkeeping_state.bank_categorization.protocol import (
    ResidualBankCategorizer,
)
from bookkeeping_state.bank_categorization.selection import (
    ResidualBankInvariantCorruptionError,
    get_unposted_classified_residual_decisions,
    residual_bank_items_needing_semantic_evaluation,
)
from bookkeeping_state.bank_categorization.transport_models import (
    compute_canonical_bank_payload_digest,
)
from bookkeeping_state.bank_categorization.view import (
    build_residual_bank_categorization_view,
)
from bookkeeping_state.dag.adapter import dag_plan_to_transition_batch
from bookkeeping_state.dag.protocol import AseClassifier
from bookkeeping_state.dag.view import build_dag_view
from bookkeeping_state.domain.commands import (
    ApplyPaymentCommand,
    PostResidualBankClassificationCommand,
    RecordResidualBankClassificationCommand,
)
from bookkeeping_state.domain.money import amount_units_to_int
from bookkeeping_state.domain.residual_bank_classifications import (
    ResidualBankClassificationStatus,
)
from bookkeeping_state.hydration.hydrator import BookkeepingHydrator
from bookkeeping_state.payment_application.coordinator import (
    TwoStageSettlementAndReconciliationPlan,
    plan_two_stage_bookkeeping,
)
from bookkeeping_state.payment_application.service import PaymentApplicationService
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
from bookkeeping_state.transitions.batch import (
    TransitionBatch,
    TransitionBatchError,
)
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
        payment_application_service: PaymentApplicationService | None = None,
        residual_bank_categorizer: ResidualBankCategorizer | None = None,
        hydrator: BookkeepingHydrator | None = None,
    ) -> None:
        self._state = state
        self._engine = engine
        self._routing_service = routing_service
        self._dag_classifier = dag_classifier
        self._reconciliation_service = reconciliation_service
        self._payment_application_service = (
            payment_application_service or PaymentApplicationService()
        )
        self._residual_bank_categorizer: ResidualBankCategorizer | None = (
            residual_bank_categorizer
        )
        self._hydrator = hydrator or BookkeepingHydrator(
            repository=self._engine.repository
        )

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
        payment_app_stage_res: SessionStageResult | None = None
        recon_stage_res: SessionStageResult | None = None
        residual_stage_res: SessionStageResult | None = None

        residual_evaluation_count: int = 0
        residual_classified_count: int = 0
        residual_hold_count: int = 0
        residual_posting_count: int = 0
        residual_posting_noop_count: int = 0
        unresolved_residual_hold_count: int = 0
        executed_residual_posting_ids: list[str] = []

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
            # 3. Stage-1 Payment Application Loop (Bounded Rehydrate/Replan)
            # -----------------------------------------------------------------
            starting_stage1_rev = self._state.revision
            starting_stage1_pers_rev = self._state.persistence_revision
            executed_bank_items: set[str] = set()
            executed_payment_application_ids: list[str] = []
            rehydration_count: int = 0
            payment_app_stage_res: SessionStageResult | None = None
            latest_two_stage_plan: TwoStageSettlementAndReconciliationPlan | None = None

            max_stage1_iterations = max(1, len(self._state.bank_items))
            iteration = 0

            while True:
                queries = BookkeepingQueries(self._state)
                two_stage_plan = plan_two_stage_bookkeeping(
                    queries=queries,
                    reconciliation_service=self._reconciliation_service,
                    payment_application_service=self._payment_application_service,
                    solver_run_id=f"{run_id_base}-s1-{iteration + 1}",
                    session_id=session_id,
                    issued_at=issued_at,
                )
                latest_two_stage_plan = two_stage_plan

                if not two_stage_plan.payment_application_plan.proposals:
                    # No executable Stage-1 proposals remain; proceed to Stage 2
                    break

                # Deterministic single-intent selection rule:
                # canonical order: bank_item_id, then total_amount_units, then sorted obligation book_item_ids
                sorted_proposals = sorted(
                    two_stage_plan.payment_application_plan.proposals,
                    key=lambda p: (
                        p.bank_item_id,
                        p.intent.total_amount_units,
                        tuple(
                            sorted(
                                a.book_item_id
                                for a in getattr(
                                    p.intent,
                                    "obligation_allocations",
                                    getattr(p.intent, "allocations", ()),
                                )
                            )
                        ),
                    ),
                )
                selected_proposal = sorted_proposals[0]

                # Loop safety guard 1: duplicate bank item check
                if selected_proposal.bank_item_id in executed_bank_items:
                    logger.error(
                        "Stage-1 loop invariant failure: duplicate bank item %s proposed",
                        selected_proposal.bank_item_id,
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
                        payment_app_res=payment_app_stage_res,
                        recon_res=SessionStageResult.not_run(state_revision=self._state.revision),
                        validation_report=None,
                        is_success=False,
                        failure_stage=FailureStage.LOOP_INVARIANT,
                        failure_reason=(
                            f"Duplicate bank item {selected_proposal.bank_item_id} "
                            "proposed in Stage-1 loop."
                        ),
                        executed_payment_application_ids=tuple(executed_payment_application_ids),
                        rehydration_count=rehydration_count,
                    )

                # Loop safety bound check
                if iteration >= max_stage1_iterations:
                    logger.error("Stage-1 loop reached safety iteration bound without converging")
                    return self._build_terminal_result(
                        company_id=company_id,
                        session_id=session_id,
                        starting_persistence_revision=starting_persistence_revision,
                        final_persistence_revision=self._state.persistence_revision,
                        final_local_state_revision=self._state.revision,
                        last_consistent_local_state_revision=last_consistent_local_revision,
                        routing_res=routing_stage_res,
                        dag_res=dag_stage_res,
                        payment_app_res=payment_app_stage_res,
                        recon_res=SessionStageResult.not_run(state_revision=self._state.revision),
                        validation_report=None,
                        is_success=False,
                        failure_stage=FailureStage.LOOP_INVARIANT,
                        failure_reason="Stage-1 loop reached safety iteration bound.",
                        executed_payment_application_ids=tuple(executed_payment_application_ids),
                        rehydration_count=rehydration_count,
                    )

                iteration += 1

                prev_p_rev = self._state.persistence_revision
                prev_fp = state_fingerprint(self._state)
                prev_local_rev = self._state.revision

                cap = two_stage_plan.get_capability(selected_proposal.bank_item_id)
                if cap is None:
                    logger.error(
                        "No capability minted for bank item %s",
                        selected_proposal.bank_item_id,
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
                        payment_app_res=payment_app_stage_res,
                        recon_res=SessionStageResult.not_run(state_revision=self._state.revision),
                        validation_report=None,
                        is_success=False,
                        failure_stage=FailureStage.PAYMENT_APPLICATION,
                        failure_reason=(
                            f"No capability minted for bank item {selected_proposal.bank_item_id}"
                        ),
                        executed_payment_application_ids=tuple(executed_payment_application_ids),
                        rehydration_count=rehydration_count,
                    )

                cmd = ApplyPaymentCommand.from_intent(
                    command_id=f"cmd:s1:{session_id}-{iteration}",
                    expected_state_revision=self._state.revision,
                    session_id=session_id,
                    issued_at=issued_at,
                    payment_application_id=f"payapp:{session_id}-{iteration}-{selected_proposal.bank_item_id.replace(':', '_')}",
                    plan_intent=selected_proposal.intent,
                    capability=cap,
                )

                apply_res = self._engine.apply(state=self._state, command=cmd)

                if apply_res.applied_requires_rehydration:
                    # Loop safety guard 2: monotone persistence revision advance
                    if apply_res.resulting_persistence_revision <= prev_p_rev:
                        logger.error("Persistence revision did not advance after Stage-1 commit")
                        return self._build_terminal_result(
                            company_id=company_id,
                            session_id=session_id,
                            starting_persistence_revision=starting_persistence_revision,
                            final_persistence_revision=apply_res.resulting_persistence_revision,
                            final_local_state_revision=None,
                            last_consistent_local_state_revision=prev_local_rev,
                            routing_res=routing_stage_res,
                            dag_res=dag_stage_res,
                            payment_app_res=payment_app_stage_res,
                            recon_res=SessionStageResult.not_run(state_revision=None),
                            validation_report=None,
                            is_success=False,
                            failure_stage=FailureStage.LOOP_INVARIANT,
                            failure_reason="Persistence revision did not advance after Stage-1 commit.",
                            executed_payment_application_ids=tuple(executed_payment_application_ids),
                            rehydration_count=rehydration_count,
                        )

                    executed_bank_items.add(selected_proposal.bank_item_id)
                    executed_payment_application_ids.append(cmd.payment_application_id)
                    rehydration_count += 1
                    current_persistence_revision = apply_res.resulting_persistence_revision

                    # Fresh post-commit rehydration
                    try:
                        self._state = self._hydrator.hydrate(
                            company_id=company_id,
                            session_id=session_id,
                        )
                    except Exception as exc:
                        logger.error(
                            "Rehydration failed after committed Stage-1 mutation: %s",
                            exc,
                        )
                        return self._build_catastrophic_result(
                            company_id=company_id,
                            session_id=session_id,
                            starting_persistence_revision=starting_persistence_revision,
                            final_persistence_revision=current_persistence_revision,
                            last_consistent_local_state_revision=None,
                            routing_res=routing_stage_res,
                            dag_res=dag_stage_res,
                            payment_app_res=payment_app_stage_res,
                            recon_res=SessionStageResult.not_run(state_revision=None),
                            failure_stage=FailureStage.REHYDRATION,
                            failure_reason=f"Rehydration failed after committed Stage-1 mutation: {exc}",
                            executed_payment_application_ids=tuple(executed_payment_application_ids),
                            rehydration_count=rehydration_count,
                        )

                    # Loop safety guard 3: fingerprint change verification
                    new_fp = state_fingerprint(self._state)
                    if new_fp == prev_fp:
                        logger.error("State fingerprint unchanged after Stage-1 rehydration")
                        return self._build_terminal_result(
                            company_id=company_id,
                            session_id=session_id,
                            starting_persistence_revision=starting_persistence_revision,
                            final_persistence_revision=self._state.persistence_revision,
                            final_local_state_revision=self._state.revision,
                            last_consistent_local_state_revision=self._state.revision,
                            routing_res=routing_stage_res,
                            dag_res=dag_stage_res,
                            payment_app_res=payment_app_stage_res,
                            recon_res=SessionStageResult.not_run(state_revision=self._state.revision),
                            validation_report=None,
                            is_success=False,
                            failure_stage=FailureStage.LOOP_INVARIANT,
                            failure_reason="State fingerprint unchanged after Stage-1 rehydration.",
                            executed_payment_application_ids=tuple(executed_payment_application_ids),
                            rehydration_count=rehydration_count,
                        )

                    last_consistent_local_revision = self._state.revision
                elif apply_res.rejected:
                    rej_msg = (
                        apply_res.rejection.message
                        if apply_res.rejection
                        else "Stage-1 payment application rejected"
                    )
                    logger.error("Stage-1 payment application rejected: %s", rej_msg)
                    payment_app_stage_res = SessionStageResult.rejected(
                        base_state_revision=prev_local_rev,
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
                        payment_app_res=payment_app_stage_res,
                        recon_res=SessionStageResult.not_run(state_revision=self._state.revision),
                        validation_report=None,
                        is_success=False,
                        failure_stage=FailureStage.PAYMENT_APPLICATION,
                        failure_reason=rej_msg,
                        executed_payment_application_ids=tuple(executed_payment_application_ids),
                        rehydration_count=rehydration_count,
                    )
                else:
                    return self._build_terminal_result(
                        company_id=company_id,
                        session_id=session_id,
                        starting_persistence_revision=starting_persistence_revision,
                        final_persistence_revision=self._state.persistence_revision,
                        final_local_state_revision=self._state.revision,
                        last_consistent_local_state_revision=last_consistent_local_revision,
                        routing_res=routing_stage_res,
                        dag_res=dag_stage_res,
                        payment_app_res=payment_app_stage_res,
                        recon_res=SessionStageResult.not_run(state_revision=self._state.revision),
                        validation_report=None,
                        is_success=False,
                        failure_stage=FailureStage.LOOP_INVARIANT,
                        failure_reason=f"Unexpected transition status {apply_res.status}",
                        executed_payment_application_ids=tuple(executed_payment_application_ids),
                        rehydration_count=rehydration_count,
                    )

            if not executed_payment_application_ids:
                payment_app_stage_res = SessionStageResult.skipped(
                    state_revision=self._state.revision
                )
            else:
                batch_summary = SessionBatchSummary(
                    status="APPLIED",
                    batch_id=None,
                    command_count=len(executed_payment_application_ids),
                    command_ids=tuple(executed_payment_application_ids),
                    command_types=tuple("APPLY_PAYMENT" for _ in executed_payment_application_ids),
                    previous_state_revision=starting_stage1_rev,
                    resulting_state_revision=self._state.revision,
                    previous_persistence_revision=starting_stage1_pers_rev,
                    resulting_persistence_revision=self._state.persistence_revision,
                    artifact_ids=tuple(executed_payment_application_ids),
                )
                payment_app_stage_res = SessionStageResult.applied(
                    base_state_revision=starting_stage1_rev,
                    resulting_state_revision=self._state.revision,
                    batch_summary=batch_summary,
                )

            # -----------------------------------------------------------------
            # 4. Final Stage-2 Bank Reconciliation Stage
            # -----------------------------------------------------------------
            base_rev = self._state.revision
            last_consistent_local_revision = base_rev
            recon_batch = None
            try:
                if latest_two_stage_plan is not None:
                    recon_plan = latest_two_stage_plan.reconciliation_plan
                else:
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
                            payment_app_res=payment_app_stage_res,
                            recon_res=recon_stage_res,
                            validation_report=None,
                            is_success=False,
                            failure_stage=FailureStage.RECONCILIATION,
                            failure_reason=(
                                batch_res.rejection.message
                                if batch_res.rejection
                                else "Unknown recon rejection"
                            ),
                            executed_payment_application_ids=tuple(executed_payment_application_ids),
                            rehydration_count=rehydration_count,
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
                    payment_app_res=payment_app_stage_res,
                    recon_res=recon_stage_res,
                    failure_reason=str(exc),
                    executed_payment_application_ids=tuple(executed_payment_application_ids),
                    rehydration_count=rehydration_count,
                )

            # -----------------------------------------------------------------
            # 4.5 Residual Bank Categorization & Authoritative Posting Phase
            # -----------------------------------------------------------------
            # A. Semantic Eligibility Selection & Case E Invariant Check
            queries_post_stage2 = BookkeepingQueries(self._state)
            try:
                residual_candidates = residual_bank_items_needing_semantic_evaluation(queries_post_stage2)
            except ResidualBankInvariantCorruptionError as exc:
                logger.error("Invariant corruption (Case E) detected in residual selection: %s", exc)
                return self._build_terminal_result(
                    company_id=company_id,
                    session_id=session_id,
                    starting_persistence_revision=starting_persistence_revision,
                    final_persistence_revision=self._state.persistence_revision,
                    final_local_state_revision=self._state.revision,
                    last_consistent_local_state_revision=last_consistent_local_revision,
                    routing_res=routing_stage_res,
                    dag_res=dag_stage_res,
                    payment_app_res=payment_app_stage_res,
                    recon_res=recon_stage_res,
                    residual_res=SessionStageResult.failed(
                        base_state_revision=self._state.revision,
                        resulting_state_revision=self._state.revision,
                    ),
                    validation_report=None,
                    is_success=False,
                    failure_stage=FailureStage.LOOP_INVARIANT,
                    failure_reason=str(exc),
                    executed_payment_application_ids=tuple(executed_payment_application_ids),
                    rehydration_count=rehydration_count,
                    residual_evaluation_count=residual_evaluation_count,
                    residual_classified_count=residual_classified_count,
                    residual_hold_count=residual_hold_count,
                    residual_posting_count=residual_posting_count,
                    residual_posting_noop_count=residual_posting_noop_count,
                    unresolved_residual_hold_count=unresolved_residual_hold_count,
                    executed_residual_posting_ids=tuple(executed_residual_posting_ids),
                )

            # B. Exactly ONE Bounded ASE Wave (if candidates exist)
            if residual_candidates:
                if self._residual_bank_categorizer is None:
                    logger.error(
                        "Residual candidates exist (%d items) but residual_bank_categorizer is not configured",
                        len(residual_candidates),
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
                        payment_app_res=payment_app_stage_res,
                        recon_res=recon_stage_res,
                        residual_res=SessionStageResult.failed(
                            base_state_revision=self._state.revision,
                            resulting_state_revision=self._state.revision,
                        ),
                        validation_report=None,
                        is_success=False,
                        failure_stage=FailureStage.RESIDUAL_CATEGORIZATION,
                        failure_reason="Residual bank categorizer is not configured but semantic candidates exist.",
                        executed_payment_application_ids=tuple(executed_payment_application_ids),
                        rehydration_count=rehydration_count,
                        residual_evaluation_count=len(residual_candidates),
                        residual_classified_count=residual_classified_count,
                        residual_hold_count=residual_hold_count,
                        residual_posting_count=residual_posting_count,
                        residual_posting_noop_count=residual_posting_noop_count,
                        unresolved_residual_hold_count=unresolved_residual_hold_count,
                        executed_residual_posting_ids=tuple(executed_residual_posting_ids),
                    )

                residual_evaluation_count = len(residual_candidates)
                candidate_pairs = tuple((c.bank_item, c.remaining_units) for c in residual_candidates)
                residual_view = build_residual_bank_categorization_view(
                    queries_post_stage2,
                    candidate_items=candidate_pairs,
                )

                try:
                    response = self._residual_bank_categorizer.categorize_view(residual_view)
                except Exception as exc:
                    logger.error("Residual bank categorization ASE invocation failed: %s", exc)
                    return self._build_terminal_result(
                        company_id=company_id,
                        session_id=session_id,
                        starting_persistence_revision=starting_persistence_revision,
                        final_persistence_revision=self._state.persistence_revision,
                        final_local_state_revision=self._state.revision,
                        last_consistent_local_state_revision=last_consistent_local_revision,
                        routing_res=routing_stage_res,
                        dag_res=dag_stage_res,
                        payment_app_res=payment_app_stage_res,
                        recon_res=recon_stage_res,
                        residual_res=SessionStageResult.failed(
                            base_state_revision=self._state.revision,
                            resulting_state_revision=self._state.revision,
                        ),
                        validation_report=None,
                        is_success=False,
                        failure_stage=FailureStage.RESIDUAL_CATEGORIZATION,
                        failure_reason=f"Residual bank categorization failed: {exc}",
                        executed_payment_application_ids=tuple(executed_payment_application_ids),
                        rehydration_count=rehydration_count,
                        residual_evaluation_count=residual_evaluation_count,
                        residual_classified_count=residual_classified_count,
                        residual_hold_count=residual_hold_count,
                        residual_posting_count=residual_posting_count,
                        residual_posting_noop_count=residual_posting_noop_count,
                        unresolved_residual_hold_count=unresolved_residual_hold_count,
                        executed_residual_posting_ids=tuple(executed_residual_posting_ids),
                    )

                # Whole-envelope response completeness validation
                requested_ids = {item.bank_item_id for item in residual_view.items}
                response_ids = {outcome.bank_item_id for outcome in response.outcomes}

                if requested_ids != response_ids:
                    missing = requested_ids - response_ids
                    extra = response_ids - requested_ids
                    error_msg = f"Incomplete ASE response envelope: missing {sorted(missing)}, extra {sorted(extra)}"
                    logger.error(error_msg)
                    return self._build_terminal_result(
                        company_id=company_id,
                        session_id=session_id,
                        starting_persistence_revision=starting_persistence_revision,
                        final_persistence_revision=self._state.persistence_revision,
                        final_local_state_revision=self._state.revision,
                        last_consistent_local_state_revision=last_consistent_local_revision,
                        routing_res=routing_stage_res,
                        dag_res=dag_stage_res,
                        payment_app_res=payment_app_stage_res,
                        recon_res=recon_stage_res,
                        residual_res=SessionStageResult.failed(
                            base_state_revision=self._state.revision,
                            resulting_state_revision=self._state.revision,
                        ),
                        validation_report=None,
                        is_success=False,
                        failure_stage=FailureStage.RESIDUAL_CATEGORIZATION,
                        failure_reason=error_msg,
                        executed_payment_application_ids=tuple(executed_payment_application_ids),
                        rehydration_count=rehydration_count,
                        residual_evaluation_count=residual_evaluation_count,
                        residual_classified_count=residual_classified_count,
                        residual_hold_count=residual_hold_count,
                        residual_posting_count=residual_posting_count,
                        residual_posting_noop_count=residual_posting_noop_count,
                        unresolved_residual_hold_count=unresolved_residual_hold_count,
                        executed_residual_posting_ids=tuple(executed_residual_posting_ids),
                    )

                # Map outcomes back to candidates and construct RecordResidualBankClassificationCommands
                candidate_by_bank_item_id = {c.bank_item.id: c for c in residual_candidates}
                commands: list[RecordResidualBankClassificationCommand] = []

                digest = compute_canonical_bank_payload_digest(
                    schema_version=response.schema_version,
                    dag_id=response.dag_id,
                    company_id=company_id,
                    session_id=session_id,
                    state_revision=residual_view.state_revision,
                    persistence_revision=residual_view.persistence_revision,
                    bank_items=residual_view.items,
                )

                for i, outcome in enumerate(response.outcomes, start=1):
                    cand = candidate_by_bank_item_id[outcome.bank_item_id]
                    bank_item = cand.bank_item
                    status = (
                        ResidualBankClassificationStatus.CLASSIFIED
                        if outcome.status == "CLASSIFIED"
                        else ResidualBankClassificationStatus.HOLD
                    )

                    cmd = RecordResidualBankClassificationCommand(
                        command_id=f"cmd:rec-res:{session_id}-{self._state.revision}-{i}",
                        expected_state_revision=self._state.revision,
                        expected_persistence_revision=self._state.persistence_revision,
                        session_id=session_id,
                        issued_at=issued_at,
                        decision_id=f"rbc:{session_id}-{bank_item.id.replace(':', '_')}",
                        bank_item_id=bank_item.id,
                        bank_account_id=bank_item.bank_account_id,
                        original_amount_units=amount_units_to_int(bank_item.amount_units),
                        residual_amount_units=cand.remaining_units,
                        direction=bank_item.direction,
                        currency=bank_item.currency,
                        request_semantic_digest=digest,
                        schema_version=response.schema_version,
                        dag_id=response.dag_id,
                        status=status,
                        account_code=outcome.account_code,
                        confidence=outcome.confidence,
                        rationale=outcome.rationale,
                        required_evidence=outcome.required_evidence,
                        hold_reason=outcome.hold_reason,
                        ase_node_id=outcome.ase_node_id,
                        terminal_property=outcome.terminal_property,
                        evidence_refs=outcome.evidence_refs,
                        supersedes_decision_id=cand.supersedes_decision_id,
                    )
                    commands.append(cmd)

                residual_batch = TransitionBatch(
                    batch_id=f"batch:rec-res:{session_id}-{self._state.revision}",
                    session_id=session_id,
                    expected_state_revision=self._state.revision,
                    commands=tuple(commands),
                )

                base_rev = self._state.revision
                try:
                    batch_res = self._engine.apply_batch(
                        state=self._state,
                        batch=residual_batch,
                    )
                    batch_summary = SessionBatchSummary.from_batch_result(batch_res)
                    if batch_res.is_applied:
                        residual_stage_res = SessionStageResult.applied(
                            base_state_revision=base_rev,
                            resulting_state_revision=self._state.revision,
                            batch_summary=batch_summary,
                        )
                        last_consistent_local_revision = self._state.revision
                        current_persistence_revision = self._state.persistence_revision
                        residual_classified_count = sum(
                            1 for c in commands if c.status == ResidualBankClassificationStatus.CLASSIFIED
                        )
                        residual_hold_count = sum(
                            1 for c in commands if c.status == ResidualBankClassificationStatus.HOLD
                        )
                    else:
                        residual_stage_res = SessionStageResult.rejected(
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
                            payment_app_res=payment_app_stage_res,
                            recon_res=recon_stage_res,
                            residual_res=residual_stage_res,
                            validation_report=None,
                            is_success=False,
                            failure_stage=FailureStage.RESIDUAL_CATEGORIZATION,
                            failure_reason=(
                                batch_res.rejection.message
                                if batch_res.rejection
                                else "Residual classification batch rejected"
                            ),
                            executed_payment_application_ids=tuple(executed_payment_application_ids),
                            rehydration_count=rehydration_count,
                            residual_evaluation_count=residual_evaluation_count,
                            residual_classified_count=residual_classified_count,
                            residual_hold_count=residual_hold_count,
                            residual_posting_count=residual_posting_count,
                            residual_posting_noop_count=residual_posting_noop_count,
                            unresolved_residual_hold_count=unresolved_residual_hold_count,
                            executed_residual_posting_ids=tuple(executed_residual_posting_ids),
                        )
                except CommittedStateApplicationError as exc:
                    logger.error(
                        "Catastrophic error applying committed state in residual classification: %s",
                        exc,
                    )
                    catastrophic_summary = SessionBatchSummary.from_catastrophic_error(
                        batch_id=residual_batch.batch_id,
                        command_count=len(residual_batch.commands),
                        command_ids=tuple(cmd.command_id for cmd in residual_batch.commands),
                        command_types=tuple(
                            cmd.command_type.value if hasattr(cmd.command_type, "value") else str(cmd.command_type)
                            for cmd in residual_batch.commands
                        ),
                        previous_state_revision=base_rev,
                        previous_persistence_revision=current_persistence_revision,
                        committed_persistence_revision=exc.commit.new_revision,
                        cause_message=str(exc),
                    )
                    residual_stage_res = SessionStageResult.catastrophic(
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
                        payment_app_res=payment_app_stage_res,
                        recon_res=recon_stage_res,
                        residual_res=residual_stage_res,
                        failure_stage=FailureStage.COMMITTED_STATE_APPLICATION,
                        failure_reason=str(exc),
                        executed_payment_application_ids=tuple(executed_payment_application_ids),
                        rehydration_count=rehydration_count,
                        residual_evaluation_count=residual_evaluation_count,
                        residual_classified_count=residual_classified_count,
                        residual_hold_count=residual_hold_count,
                        residual_posting_count=residual_posting_count,
                        residual_posting_noop_count=residual_posting_noop_count,
                        unresolved_residual_hold_count=unresolved_residual_hold_count,
                        executed_residual_posting_ids=tuple(executed_residual_posting_ids),
                    )
            else:
                residual_stage_res = SessionStageResult.skipped(
                    state_revision=self._state.revision
                )

            # C. Bounded Authoritative Posting Loop
            max_posting_iterations = max(1, len(self._state.bank_items))
            posting_iteration = 0

            while True:
                queries_posting = BookkeepingQueries(self._state)
                try:
                    pending_decisions = get_unposted_classified_residual_decisions(queries_posting)
                except ResidualBankInvariantCorruptionError as exc:
                    logger.error("Invariant corruption (Case E) detected in posting loop: %s", exc)
                    return self._build_terminal_result(
                        company_id=company_id,
                        session_id=session_id,
                        starting_persistence_revision=starting_persistence_revision,
                        final_persistence_revision=self._state.persistence_revision,
                        final_local_state_revision=self._state.revision,
                        last_consistent_local_state_revision=last_consistent_local_revision,
                        routing_res=routing_stage_res,
                        dag_res=dag_stage_res,
                        payment_app_res=payment_app_stage_res,
                        recon_res=recon_stage_res,
                        residual_res=residual_stage_res,
                        validation_report=None,
                        is_success=False,
                        failure_stage=FailureStage.LOOP_INVARIANT,
                        failure_reason=str(exc),
                        executed_payment_application_ids=tuple(executed_payment_application_ids),
                        rehydration_count=rehydration_count,
                        residual_evaluation_count=residual_evaluation_count,
                        residual_classified_count=residual_classified_count,
                        residual_hold_count=residual_hold_count,
                        residual_posting_count=residual_posting_count,
                        residual_posting_noop_count=residual_posting_noop_count,
                        unresolved_residual_hold_count=unresolved_residual_hold_count,
                        executed_residual_posting_ids=tuple(executed_residual_posting_ids),
                    )

                if not pending_decisions:
                    break

                if posting_iteration >= max_posting_iterations:
                    logger.error("Residual posting loop reached safety iteration bound without converging")
                    return self._build_terminal_result(
                        company_id=company_id,
                        session_id=session_id,
                        starting_persistence_revision=starting_persistence_revision,
                        final_persistence_revision=self._state.persistence_revision,
                        final_local_state_revision=self._state.revision,
                        last_consistent_local_state_revision=last_consistent_local_revision,
                        routing_res=routing_stage_res,
                        dag_res=dag_stage_res,
                        payment_app_res=payment_app_stage_res,
                        recon_res=recon_stage_res,
                        residual_res=residual_stage_res,
                        validation_report=None,
                        is_success=False,
                        failure_stage=FailureStage.LOOP_INVARIANT,
                        failure_reason="Residual posting loop reached safety iteration bound.",
                        executed_payment_application_ids=tuple(executed_payment_application_ids),
                        rehydration_count=rehydration_count,
                        residual_evaluation_count=residual_evaluation_count,
                        residual_classified_count=residual_classified_count,
                        residual_hold_count=residual_hold_count,
                        residual_posting_count=residual_posting_count,
                        residual_posting_noop_count=residual_posting_noop_count,
                        unresolved_residual_hold_count=unresolved_residual_hold_count,
                        executed_residual_posting_ids=tuple(executed_residual_posting_ids),
                    )

                posting_iteration += 1
                selected_decision = pending_decisions[0]

                prev_p_rev = self._state.persistence_revision
                prev_local_rev = self._state.revision
                prev_fp = state_fingerprint(self._state)

                post_cmd = PostResidualBankClassificationCommand(
                    command_id=f"cmd:post-res:{session_id}-{posting_iteration}",
                    expected_state_revision=self._state.revision,
                    expected_persistence_revision=self._state.persistence_revision,
                    session_id=session_id,
                    issued_at=issued_at,
                    decision_id=selected_decision.id,
                    bank_item_id=selected_decision.bank_item_id,
                    bank_account_id=selected_decision.bank_account_id,
                    residual_amount_units=selected_decision.residual_amount_units,
                    direction=selected_decision.direction,
                    account_code=selected_decision.account_code,
                )

                try:
                    apply_res = self._engine.apply(state=self._state, command=post_cmd)
                except CommittedStateApplicationError as exc:
                    logger.error("Catastrophic error applying committed state in residual posting: %s", exc)
                    return self._build_catastrophic_result(
                        company_id=company_id,
                        session_id=session_id,
                        starting_persistence_revision=starting_persistence_revision,
                        final_persistence_revision=exc.commit.new_revision,
                        last_consistent_local_state_revision=None,
                        routing_res=routing_stage_res,
                        dag_res=dag_stage_res,
                        payment_app_res=payment_app_stage_res,
                        recon_res=recon_stage_res,
                        residual_res=residual_stage_res,
                        failure_stage=FailureStage.COMMITTED_STATE_APPLICATION,
                        failure_reason=str(exc),
                        executed_payment_application_ids=tuple(executed_payment_application_ids),
                        rehydration_count=rehydration_count,
                        residual_evaluation_count=residual_evaluation_count,
                        residual_classified_count=residual_classified_count,
                        residual_hold_count=residual_hold_count,
                        residual_posting_count=residual_posting_count,
                        residual_posting_noop_count=residual_posting_noop_count,
                        unresolved_residual_hold_count=unresolved_residual_hold_count,
                        executed_residual_posting_ids=tuple(executed_residual_posting_ids),
                    )

                if apply_res.applied_requires_rehydration:
                    if apply_res.resulting_persistence_revision <= prev_p_rev:
                        logger.error("Persistence revision did not advance after residual posting commit")
                        return self._build_terminal_result(
                            company_id=company_id,
                            session_id=session_id,
                            starting_persistence_revision=starting_persistence_revision,
                            final_persistence_revision=apply_res.resulting_persistence_revision,
                            final_local_state_revision=None,
                            last_consistent_local_state_revision=prev_local_rev,
                            routing_res=routing_stage_res,
                            dag_res=dag_stage_res,
                            payment_app_res=payment_app_stage_res,
                            recon_res=recon_stage_res,
                            residual_res=residual_stage_res,
                            validation_report=None,
                            is_success=False,
                            failure_stage=FailureStage.LOOP_INVARIANT,
                            failure_reason="Persistence revision did not advance after residual posting commit.",
                            executed_payment_application_ids=tuple(executed_payment_application_ids),
                            rehydration_count=rehydration_count,
                            residual_evaluation_count=residual_evaluation_count,
                            residual_classified_count=residual_classified_count,
                            residual_hold_count=residual_hold_count,
                            residual_posting_count=residual_posting_count,
                            residual_posting_noop_count=residual_posting_noop_count,
                            unresolved_residual_hold_count=unresolved_residual_hold_count,
                            executed_residual_posting_ids=tuple(executed_residual_posting_ids),
                        )

                    executed_residual_posting_ids.append(post_cmd.command_id)
                    residual_posting_count += 1
                    rehydration_count += 1
                    current_persistence_revision = apply_res.resulting_persistence_revision

                    try:
                        self._state = self._hydrator.hydrate(
                            company_id=company_id,
                            session_id=session_id,
                        )
                    except Exception as exc:
                        logger.error("Rehydration failed after committed residual posting: %s", exc)
                        return self._build_catastrophic_result(
                            company_id=company_id,
                            session_id=session_id,
                            starting_persistence_revision=starting_persistence_revision,
                            final_persistence_revision=current_persistence_revision,
                            last_consistent_local_state_revision=None,
                            routing_res=routing_stage_res,
                            dag_res=dag_stage_res,
                            payment_app_res=payment_app_stage_res,
                            recon_res=recon_stage_res,
                            residual_res=residual_stage_res,
                            failure_stage=FailureStage.REHYDRATION,
                            failure_reason=f"Rehydration failed after committed residual posting: {exc}",
                            executed_payment_application_ids=tuple(executed_payment_application_ids),
                            rehydration_count=rehydration_count,
                            residual_evaluation_count=residual_evaluation_count,
                            residual_classified_count=residual_classified_count,
                            residual_hold_count=residual_hold_count,
                            residual_posting_count=residual_posting_count,
                            residual_posting_noop_count=residual_posting_noop_count,
                            unresolved_residual_hold_count=unresolved_residual_hold_count,
                            executed_residual_posting_ids=tuple(executed_residual_posting_ids),
                        )

                    new_fp = state_fingerprint(self._state)
                    if new_fp == prev_fp:
                        logger.error("State fingerprint unchanged after residual posting rehydration")
                        return self._build_terminal_result(
                            company_id=company_id,
                            session_id=session_id,
                            starting_persistence_revision=starting_persistence_revision,
                            final_persistence_revision=self._state.persistence_revision,
                            final_local_state_revision=self._state.revision,
                            last_consistent_local_state_revision=self._state.revision,
                            routing_res=routing_stage_res,
                            dag_res=dag_stage_res,
                            payment_app_res=payment_app_stage_res,
                            recon_res=recon_stage_res,
                            residual_res=residual_stage_res,
                            validation_report=None,
                            is_success=False,
                            failure_stage=FailureStage.LOOP_INVARIANT,
                            failure_reason="State fingerprint unchanged after residual posting rehydration.",
                            executed_payment_application_ids=tuple(executed_payment_application_ids),
                            rehydration_count=rehydration_count,
                            residual_evaluation_count=residual_evaluation_count,
                            residual_classified_count=residual_classified_count,
                            residual_hold_count=residual_hold_count,
                            residual_posting_count=residual_posting_count,
                            residual_posting_noop_count=residual_posting_noop_count,
                            unresolved_residual_hold_count=unresolved_residual_hold_count,
                            executed_residual_posting_ids=tuple(executed_residual_posting_ids),
                        )

                    last_consistent_local_revision = self._state.revision

                elif apply_res.noop:
                    check_queries = BookkeepingQueries(self._state)
                    if not check_queries.is_residual_bank_classification_posted(selected_decision.id):
                        logger.error("Residual posting command returned NOOP but decision remains unposted")
                        return self._build_terminal_result(
                            company_id=company_id,
                            session_id=session_id,
                            starting_persistence_revision=starting_persistence_revision,
                            final_persistence_revision=self._state.persistence_revision,
                            final_local_state_revision=self._state.revision,
                            last_consistent_local_state_revision=last_consistent_local_revision,
                            routing_res=routing_stage_res,
                            dag_res=dag_stage_res,
                            payment_app_res=payment_app_stage_res,
                            recon_res=recon_stage_res,
                            residual_res=residual_stage_res,
                            validation_report=None,
                            is_success=False,
                            failure_stage=FailureStage.LOOP_INVARIANT,
                            failure_reason=(
                                f"Posting command {post_cmd.command_id!r} returned NOOP, "
                                f"but decision {selected_decision.id!r} is not durably posted in state."
                            ),
                            executed_payment_application_ids=tuple(executed_payment_application_ids),
                            rehydration_count=rehydration_count,
                            residual_evaluation_count=residual_evaluation_count,
                            residual_classified_count=residual_classified_count,
                            residual_hold_count=residual_hold_count,
                            residual_posting_count=residual_posting_count,
                            residual_posting_noop_count=residual_posting_noop_count,
                            unresolved_residual_hold_count=unresolved_residual_hold_count,
                            executed_residual_posting_ids=tuple(executed_residual_posting_ids),
                        )

                    remaining_units = check_queries.bank_remaining_units(selected_decision.bank_item_id)
                    if remaining_units > 0:
                        logger.error("Case E corruption: decision posted but bank item remains residual")
                        return self._build_terminal_result(
                            company_id=company_id,
                            session_id=session_id,
                            starting_persistence_revision=starting_persistence_revision,
                            final_persistence_revision=self._state.persistence_revision,
                            final_local_state_revision=self._state.revision,
                            last_consistent_local_state_revision=last_consistent_local_revision,
                            routing_res=routing_stage_res,
                            dag_res=dag_stage_res,
                            payment_app_res=payment_app_stage_res,
                            recon_res=recon_stage_res,
                            residual_res=residual_stage_res,
                            validation_report=None,
                            is_success=False,
                            failure_stage=FailureStage.LOOP_INVARIANT,
                            failure_reason=(
                                f"Invariant corruption (Case E): Decision {selected_decision.id!r} is posted, "
                                f"but BankItem {selected_decision.bank_item_id!r} still has remaining units ({remaining_units})."
                            ),
                            executed_payment_application_ids=tuple(executed_payment_application_ids),
                            rehydration_count=rehydration_count,
                            residual_evaluation_count=residual_evaluation_count,
                            residual_classified_count=residual_classified_count,
                            residual_hold_count=residual_hold_count,
                            residual_posting_count=residual_posting_count,
                            residual_posting_noop_count=residual_posting_noop_count,
                            unresolved_residual_hold_count=unresolved_residual_hold_count,
                            executed_residual_posting_ids=tuple(executed_residual_posting_ids),
                        )

                    residual_posting_noop_count += 1
                    continue

                elif apply_res.rejected:
                    rej_msg = (
                        apply_res.rejection.message
                        if apply_res.rejection
                        else "Residual posting rejected"
                    )
                    logger.error("Residual posting rejected: %s", rej_msg)
                    return self._build_terminal_result(
                        company_id=company_id,
                        session_id=session_id,
                        starting_persistence_revision=starting_persistence_revision,
                        final_persistence_revision=self._state.persistence_revision,
                        final_local_state_revision=self._state.revision,
                        last_consistent_local_state_revision=last_consistent_local_revision,
                        routing_res=routing_stage_res,
                        dag_res=dag_stage_res,
                        payment_app_res=payment_app_stage_res,
                        recon_res=recon_stage_res,
                        residual_res=residual_stage_res,
                        validation_report=None,
                        is_success=False,
                        failure_stage=FailureStage.RESIDUAL_POSTING,
                        failure_reason=rej_msg,
                        executed_payment_application_ids=tuple(executed_payment_application_ids),
                        rehydration_count=rehydration_count,
                        residual_evaluation_count=residual_evaluation_count,
                        residual_classified_count=residual_classified_count,
                        residual_hold_count=residual_hold_count,
                        residual_posting_count=residual_posting_count,
                        residual_posting_noop_count=residual_posting_noop_count,
                        unresolved_residual_hold_count=unresolved_residual_hold_count,
                        executed_residual_posting_ids=tuple(executed_residual_posting_ids),
                    )
                else:
                    return self._build_terminal_result(
                        company_id=company_id,
                        session_id=session_id,
                        starting_persistence_revision=starting_persistence_revision,
                        final_persistence_revision=self._state.persistence_revision,
                        final_local_state_revision=self._state.revision,
                        last_consistent_local_state_revision=last_consistent_local_revision,
                        routing_res=routing_stage_res,
                        dag_res=dag_stage_res,
                        payment_app_res=payment_app_stage_res,
                        recon_res=recon_stage_res,
                        residual_res=residual_stage_res,
                        validation_report=None,
                        is_success=False,
                        failure_stage=FailureStage.LOOP_INVARIANT,
                        failure_reason=f"Unexpected transition status {apply_res.status}",
                        executed_payment_application_ids=tuple(executed_payment_application_ids),
                        rehydration_count=rehydration_count,
                        residual_evaluation_count=residual_evaluation_count,
                        residual_classified_count=residual_classified_count,
                        residual_hold_count=residual_hold_count,
                        residual_posting_count=residual_posting_count,
                        residual_posting_noop_count=residual_posting_noop_count,
                        unresolved_residual_hold_count=unresolved_residual_hold_count,
                        executed_residual_posting_ids=tuple(executed_residual_posting_ids),
                    )

            if residual_stage_res is None:
                if residual_posting_count > 0:
                    residual_stage_res = SessionStageResult.applied(
                        base_state_revision=self._state.revision,
                        resulting_state_revision=self._state.revision,
                        batch_summary=SessionBatchSummary(
                            status="APPLIED",
                            batch_id=None,
                            command_count=len(executed_residual_posting_ids),
                            command_ids=tuple(executed_residual_posting_ids),
                            command_types=tuple("POST_RESIDUAL_BANK_CLASSIFICATION" for _ in executed_residual_posting_ids),
                            previous_state_revision=self._state.revision,
                            resulting_state_revision=self._state.revision,
                            previous_persistence_revision=starting_persistence_revision,
                            resulting_persistence_revision=self._state.persistence_revision,
                            artifact_ids=tuple(executed_residual_posting_ids),
                        ),
                    )
                else:
                    residual_stage_res = SessionStageResult.skipped(
                        state_revision=self._state.revision
                    )

            # D. Count final unresolved active HOLD residuals and verify unposted decisions
            queries_final = BookkeepingQueries(self._state)
            unresolved_residual_hold_count = sum(
                1
                for bank_item, _ in queries_final.residual_unmatched_bank_items()
                if (
                    (act := queries_final.active_residual_bank_classification(bank_item.id)) is not None
                    and act.status == ResidualBankClassificationStatus.HOLD
                )
            )

            try:
                remaining_unposted = get_unposted_classified_residual_decisions(queries_final)
            except ResidualBankInvariantCorruptionError as exc:
                logger.error("Invariant corruption (Case E) detected in final validation: %s", exc)
                return self._build_terminal_result(
                    company_id=company_id,
                    session_id=session_id,
                    starting_persistence_revision=starting_persistence_revision,
                    final_persistence_revision=self._state.persistence_revision,
                    final_local_state_revision=self._state.revision,
                    last_consistent_local_state_revision=last_consistent_local_revision,
                    routing_res=routing_stage_res,
                    dag_res=dag_stage_res,
                    payment_app_res=payment_app_stage_res,
                    recon_res=recon_stage_res,
                    residual_res=residual_stage_res,
                    validation_report=None,
                    is_success=False,
                    failure_stage=FailureStage.LOOP_INVARIANT,
                    failure_reason=str(exc),
                    executed_payment_application_ids=tuple(executed_payment_application_ids),
                    rehydration_count=rehydration_count,
                    residual_evaluation_count=residual_evaluation_count,
                    residual_classified_count=residual_classified_count,
                    residual_hold_count=residual_hold_count,
                    residual_posting_count=residual_posting_count,
                    residual_posting_noop_count=residual_posting_noop_count,
                    unresolved_residual_hold_count=unresolved_residual_hold_count,
                    executed_residual_posting_ids=tuple(executed_residual_posting_ids),
                )

            if remaining_unposted:
                logger.error("Residual posting loop ended with remaining unposted classified decisions")
                return self._build_terminal_result(
                    company_id=company_id,
                    session_id=session_id,
                    starting_persistence_revision=starting_persistence_revision,
                    final_persistence_revision=self._state.persistence_revision,
                    final_local_state_revision=self._state.revision,
                    last_consistent_local_state_revision=last_consistent_local_revision,
                    routing_res=routing_stage_res,
                    dag_res=dag_stage_res,
                    payment_app_res=payment_app_stage_res,
                    recon_res=recon_stage_res,
                    residual_res=residual_stage_res,
                    validation_report=None,
                    is_success=False,
                    failure_stage=FailureStage.LOOP_INVARIANT,
                    failure_reason="Residual posting loop ended with remaining unposted classified decisions.",
                    executed_payment_application_ids=tuple(executed_payment_application_ids),
                    rehydration_count=rehydration_count,
                    residual_evaluation_count=residual_evaluation_count,
                    residual_classified_count=residual_classified_count,
                    residual_hold_count=residual_hold_count,
                    residual_posting_count=residual_posting_count,
                    residual_posting_noop_count=residual_posting_noop_count,
                    unresolved_residual_hold_count=unresolved_residual_hold_count,
                    executed_residual_posting_ids=tuple(executed_residual_posting_ids),
                )

            # -----------------------------------------------------------------
            # 5. Final Validation
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
                    payment_app_res=payment_app_stage_res,
                    recon_res=recon_stage_res,
                    residual_res=residual_stage_res,
                    validation_report=validation_report,
                    is_success=False,
                    failure_stage=FailureStage.VALIDATION,
                    failure_reason=failure_reason,
                    executed_payment_application_ids=tuple(executed_payment_application_ids),
                    rehydration_count=rehydration_count,
                    residual_evaluation_count=residual_evaluation_count,
                    residual_classified_count=residual_classified_count,
                    residual_hold_count=residual_hold_count,
                    residual_posting_count=residual_posting_count,
                    residual_posting_noop_count=residual_posting_noop_count,
                    unresolved_residual_hold_count=unresolved_residual_hold_count,
                    executed_residual_posting_ids=tuple(executed_residual_posting_ids),
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
                payment_app_res=payment_app_stage_res,
                recon_res=recon_stage_res,
                residual_res=residual_stage_res,
                validation_report=validation_report,
                is_success=True,
                failure_stage=None,
                failure_reason=None,
                executed_payment_application_ids=tuple(executed_payment_application_ids),
                rehydration_count=rehydration_count,
                residual_evaluation_count=residual_evaluation_count,
                residual_classified_count=residual_classified_count,
                residual_hold_count=residual_hold_count,
                residual_posting_count=residual_posting_count,
                residual_posting_noop_count=residual_posting_noop_count,
                unresolved_residual_hold_count=unresolved_residual_hold_count,
                executed_residual_posting_ids=tuple(executed_residual_posting_ids),
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
        final_local_state_revision: int | None,
        last_consistent_local_state_revision: int | None,
        routing_res: SessionStageResult | None,
        dag_res: SessionStageResult | None,
        recon_res: SessionStageResult | None,
        payment_app_res: SessionStageResult | None = None,
        residual_res: SessionStageResult | None = None,
        validation_report: StateValidationReport | None,
        is_success: bool,
        failure_stage: FailureStage | None,
        failure_reason: str | None,
        executed_payment_application_ids: tuple[str, ...] = (),
        rehydration_count: int = 0,
        residual_evaluation_count: int = 0,
        residual_classified_count: int = 0,
        residual_hold_count: int = 0,
        residual_posting_count: int = 0,
        residual_posting_noop_count: int = 0,
        unresolved_residual_hold_count: int = 0,
        executed_residual_posting_ids: tuple[str, ...] = (),
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

        effective_payment_app_res = (
            payment_app_res
            if payment_app_res is not None
            else SessionStageResult.not_run(state_revision=final_local_state_revision)
        )
        effective_residual_res = (
            residual_res
            if residual_res is not None
            else SessionStageResult.not_run(state_revision=final_local_state_revision)
        )

        return SessionResult(
            company_id=company_id,
            session_id=session_id,
            starting_persistence_revision=starting_persistence_revision,
            final_persistence_revision=final_persistence_revision,
            final_local_state_revision=final_local_state_revision,
            last_consistent_local_state_revision=last_consistent_local_state_revision,
            routing_stage_result=routing_res,
            dag_stage_result=dag_res,
            payment_application_stage_result=effective_payment_app_res,
            reconciliation_stage_result=recon_res,
            residual_stage_result=effective_residual_res,
            executed_payment_application_ids=executed_payment_application_ids,
            rehydration_count=rehydration_count,
            residual_evaluation_count=residual_evaluation_count,
            residual_classified_count=residual_classified_count,
            residual_hold_count=residual_hold_count,
            residual_posting_count=residual_posting_count,
            residual_posting_noop_count=residual_posting_noop_count,
            unresolved_residual_hold_count=unresolved_residual_hold_count,
            executed_residual_posting_ids=executed_residual_posting_ids,
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
        last_consistent_local_state_revision: int | None,
        routing_res: SessionStageResult | None,
        dag_res: SessionStageResult | None,
        recon_res: SessionStageResult | None,
        payment_app_res: SessionStageResult | None = None,
        residual_res: SessionStageResult | None = None,
        failure_stage: FailureStage = FailureStage.COMMITTED_STATE_APPLICATION,
        failure_reason: str,
        executed_payment_application_ids: tuple[str, ...] = (),
        rehydration_count: int = 0,
        residual_evaluation_count: int = 0,
        residual_classified_count: int = 0,
        residual_hold_count: int = 0,
        residual_posting_count: int = 0,
        residual_posting_noop_count: int = 0,
        unresolved_residual_hold_count: int = 0,
        executed_residual_posting_ids: tuple[str, ...] = (),
    ) -> SessionResult:
        """
        Build a detached SessionResult for catastrophic errors.

        CRITICAL: BookkeepingState is already closed and must
        NEVER be accessed again. No properties, queries, or fingerprints are read
        from the closed state.
        """
        effective_payment_app_res = (
            payment_app_res
            if payment_app_res is not None
            else SessionStageResult.not_run(state_revision=None)
        )
        effective_residual_res = (
            residual_res
            if residual_res is not None
            else SessionStageResult.not_run(state_revision=None)
        )
        return SessionResult(
            company_id=company_id,
            session_id=session_id,
            starting_persistence_revision=starting_persistence_revision,
            final_persistence_revision=final_persistence_revision,
            final_local_state_revision=None,
            last_consistent_local_state_revision=last_consistent_local_state_revision,
            routing_stage_result=routing_res,
            dag_stage_result=dag_res,
            payment_application_stage_result=effective_payment_app_res,
            reconciliation_stage_result=recon_res,
            residual_stage_result=effective_residual_res,
            executed_payment_application_ids=executed_payment_application_ids,
            rehydration_count=rehydration_count,
            residual_evaluation_count=residual_evaluation_count,
            residual_classified_count=residual_classified_count,
            residual_hold_count=residual_hold_count,
            residual_posting_count=residual_posting_count,
            residual_posting_noop_count=residual_posting_noop_count,
            unresolved_residual_hold_count=unresolved_residual_hold_count,
            executed_residual_posting_ids=executed_residual_posting_ids,
            final_validation_status=None,
            artifact_fingerprint=None,
            state_fingerprint=None,
            is_success=False,
            failure_stage=failure_stage,
            failure_reason=failure_reason,
        )
