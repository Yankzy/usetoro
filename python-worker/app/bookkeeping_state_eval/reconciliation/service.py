from __future__ import annotations

from datetime import datetime, timezone
from typing import Sequence
import uuid

from bookkeeping_state_eval.domain.commands import (
    CommandSource,
    CreateReconciliationCommand,
)
from bookkeeping_state_eval.domain.enums import (
    AllocationSupport,
    Eligibility,
    SemanticAdmissibility,
)
from bookkeeping_state_eval.domain.hypotheses import ReconciliationHypothesis
from bookkeeping_state_eval.reconciliation.candidate_generation import (
    CandidateGenerator,
    DefaultReconciliationScorer,
    ReconciliationSemanticScoreProvider,
)
from bookkeeping_state_eval.reconciliation.models import (
    ReconciliationPlan,
    reconciliation_plan_to_transition_batch,
)
from bookkeeping_state_eval.reconciliation.optimizer import (
    optimize_reconciliation,
)
from bookkeeping_state_eval.reconciliation.view import (
    ReconciliationViewConfig,
    build_reconciliation_view,
)
from bookkeeping_state_eval.state.queries import BookkeepingQueries


class ReconciliationServiceError(RuntimeError):
    """Raised when reconciliation service violates its internal contract."""


class ReconciliationService:
    """
    Pure reconciliation planning orchestrator.

    Pipeline:
        BookkeepingQueries
            |
            v
        ReconciliationView
            |
            v
        deterministic candidate generation
            |
            v
        runtime ReconciliationHypotheses
            |
            v
        semantic scoring / utility
            |
            v
        global CP-SAT optimizer
            |
            v
        ReconciliationPlan
            |
            v
        one TransitionBatch -> apply_batch()

    This service never mutates BookkeepingState directly.
    """

    def __init__(
        self,
        *,
        candidate_generator: CandidateGenerator | None = None,
        scorer: ReconciliationSemanticScoreProvider | None = None,
    ) -> None:
        self.candidate_generator = candidate_generator or CandidateGenerator()
        self.scorer = scorer or DefaultReconciliationScorer()

    def plan(
        self,
        queries: BookkeepingQueries,
        *,
        solver_run_id: str | None = None,
        session_id: str | None = None,
        issued_at: datetime | None = None,
        view_config: ReconciliationViewConfig | None = None,
        additional_hypotheses: Sequence[ReconciliationHypothesis] = (),
        max_solve_seconds: float = 10.0,
        forced_hypothesis_ids: Sequence[str] = (),
    ) -> ReconciliationPlan:
        """
        Execute the end-to-end reconciliation planning pipeline.

        Returns a ReconciliationPlan rooted at the queries' exact state revision.
        """
        run_id = solver_run_id or f"rec-run-{uuid.uuid4().hex[:8]}"
        resolved_session = session_id or queries.session_id
        resolved_issued_at = issued_at or datetime.now(timezone.utc)

        # 1. Bounded immutable projection
        view = build_reconciliation_view(queries, config=view_config)

        # 2. Deterministic candidate generation
        candidates = self.candidate_generator.generate_candidates(view)

        # 3. Semantic scoring to runtime hypotheses via provider protocol
        assessments = self.scorer.score_candidates(candidates, view)
        assessment_by_id = {a.candidate_id: a for a in assessments}

        generated_hypotheses: list[ReconciliationHypothesis] = []
        for cand in candidates:
            assessment = assessment_by_id.get(cand.candidate_id)
            if assessment is None:
                raise ReconciliationServiceError(
                    f"Incomplete semantic assessment: candidate {cand.candidate_id!r} has no assessment"
                )

            if assessment.admissibility != SemanticAdmissibility.SUPPORTED:
                eligibility = Eligibility.INELIGIBLE
            elif assessment.allocation_support == AllocationSupport.EXPLICIT_EVIDENCE:
                eligibility = Eligibility.SELECTABLE
            elif assessment.allocation_support == AllocationSupport.UNIQUE_INFERENCE:
                eligibility = (
                    Eligibility.SELECTABLE
                    if view.config.auto_reconcile_unique_inferred_allocation
                    else Eligibility.COUNTERFACTUAL_ONLY
                )
            else:
                eligibility = Eligibility.INELIGIBLE

            hyp = cand.to_hypothesis(
                state_revision=view.state_revision,
                utility=assessment.semantic_score,
                semantic_score=assessment.semantic_score,
                semantic_value=assessment.semantic_value,
                eligibility=eligibility,
                admissibility=assessment.admissibility,
                allocation_support=assessment.allocation_support,
                evidence_refs=assessment.evidence_refs,
                semantic_rationale=assessment.rationale,
            )
            generated_hypotheses.append(hyp)

        # Combine generated hypotheses with any caller-provided hypotheses
        all_hypotheses_map: dict[str, ReconciliationHypothesis] = {}
        for h in generated_hypotheses:
            all_hypotheses_map[h.id] = h
        for h in additional_hypotheses:
            all_hypotheses_map[h.id] = h

        all_hypotheses = tuple(
            all_hypotheses_map[k] for k in sorted(all_hypotheses_map)
        )

        # 4. Global CP-SAT optimization
        result = optimize_reconciliation(
            view=view,
            hypotheses=all_hypotheses,
            solver_run_id=run_id,
            max_solve_seconds=max_solve_seconds,
            forced_hypothesis_ids=forced_hypothesis_ids,
        )

        # 5. Translate selected hypotheses into transition commands
        commands: list[CreateReconciliationCommand] = []
        for idx, hyp in enumerate(result.selected_hypotheses):
            rec_id = f"rec:{run_id}:{idx + 1}"
            cmd_id = f"cmd:{rec_id}"
            commands.append(
                CreateReconciliationCommand(
                    command_id=cmd_id,
                    expected_state_revision=view.state_revision,
                    source=CommandSource.RECONCILIATION,
                    session_id=resolved_session,
                    issued_at=resolved_issued_at,
                    reconciliation_id=rec_id,
                    bank_allocations=hyp.bank_allocations,
                    book_allocations=hyp.book_allocations,
                    source_hypothesis_id=hyp.id,
                    source_hypothesis=hyp,
                    source_hypothesis_admissibility=hyp.admissibility,
                    source_hypothesis_allocation_support=hyp.allocation_support,
                    evidence_refs=hyp.evidence_refs,
                    semantic_rationale=hyp.semantic_rationale,
                )
            )

        return ReconciliationPlan(
            state_revision=view.state_revision,
            solver_run_id=run_id,
            result=result,
            hypotheses=all_hypotheses,
            commands=tuple(commands),
        )
