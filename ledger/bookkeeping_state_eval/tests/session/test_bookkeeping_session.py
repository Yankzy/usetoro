from __future__ import annotations

from dataclasses import FrozenInstanceError
from datetime import datetime, timezone
from unittest.mock import MagicMock, patch

import pytest

from bookkeeping_state_eval.dag.simulated_ase import SimulatedAseClassifier
from bookkeeping_state.domain.bank import BankItem
from bookkeeping_state.domain.books import BookItem
from bookkeeping_state.domain.commands import (
    CommandBase,
    CreateClassificationCommand,
    CreateReconciliationCommand,
    CreateRoutingDecisionCommand,
)
from bookkeeping_state.domain.enums import Eligibility
from bookkeeping_state.domain.hypotheses import ReconciliationHypothesis
from bookkeeping_state.domain.reconciliations import BankAllocation, BookAllocation
from bookkeeping_state.hydration.hydrator import BookkeepingHydrator
from bookkeeping_state_eval.persistence.in_memory import InMemoryBookkeepingRepository
from bookkeeping_state.persistence.repository import BookkeepingSnapshot
from bookkeeping_state.reconciliation.candidate_generation import (
    CandidateGenerator,
    DefaultReconciliationScorer,
)
from bookkeeping_state.reconciliation.service import ReconciliationService
from bookkeeping_state.routing.scorer import (
    RoutingProviderScore,
    RoutingSemanticScoreProvider,
    RoutingSemanticScoringRequest,
    RoutingSemanticScoringResponse,
)
from bookkeeping_state.routing.service import RoutingPlan, RoutingService
from bookkeeping_state.session.bookkeeping_session import BookkeepingSession
from bookkeeping_state.session.result import (
    FailureStage,
    SessionBatchSummary,
    SessionResult,
    SessionStageResult,
    SessionStageStatus,
)
from bookkeeping_state.state.bookkeeping_state import BookkeepingStateClosedError
from bookkeeping_state.state.queries import BookkeepingQueries
from bookkeeping_state.state.validation import (
    StateValidationReport,
    ValidationCode,
    ValidationIssue,
    ValidationSeverity,
)
from bookkeeping_state.transitions.batch import (
    BatchTransitionResult,
    TransitionBatch,
)
from bookkeeping_state.transitions.engine import (
    CommittedStateApplicationError,
    TransitionEngine,
)
from bookkeeping_state.transitions.result import RejectionCode
from tests import factories


class DummySemanticProvider(RoutingSemanticScoreProvider):
    def score(self, request: RoutingSemanticScoringRequest) -> RoutingSemanticScoringResponse:
        scores = []
        for candidate in request.candidates:
            scores.append(
                RoutingProviderScore(
                    book_item_id=candidate.book_item_id,
                    bank_account_id=candidate.bank_account_id,
                    score=1000,
                    rationale="Dummy perfect score",
                )
            )
        return RoutingSemanticScoringResponse(
            state_revision=request.state_revision,
            model_run_id="dummy-model",
            scores=tuple(scores),
        )


def _build_env(*, bank_items: tuple[BankItem, ...] = (), book_items: tuple[BookItem, ...] = ()):
    acc = factories.account("acc-main", currency="MAD")
    ctx = factories.context()
    snap = BookkeepingSnapshot(
        persistence_revision=1,
        context=ctx,
        bank_accounts=(acc,),
        bank_items=bank_items,
        book_items=book_items,
        documents=(),
        reconciliations=(),
        routing_decisions=(),
        classifications=(),
    )
    repo = InMemoryBookkeepingRepository(initial_snapshots=[snap])

    def hydrate_state(session_id="session-1"):
        hydrator = BookkeepingHydrator(
            repository=repo,
            clock=lambda: datetime(2026, 1, 31, 12, 0, 0, tzinfo=timezone.utc),
            session_id_factory=lambda: session_id,
        )
        return hydrator.hydrate(company_id=ctx.company_id, session_id=session_id)

    state = hydrate_state()
    engine = TransitionEngine(repository=repo)

    routing_service = RoutingService(semantic_provider=DummySemanticProvider())
    dag_classifier = SimulatedAseClassifier()
    reconciliation_service = ReconciliationService(
        candidate_generator=CandidateGenerator(),
        scorer=DefaultReconciliationScorer(),
    )

    return repo, engine, routing_service, dag_classifier, reconciliation_service, hydrate_state, state


def _find_objects_of_type(obj, target_types, visited=None):
    """Recursively search any object structure for instances of target_types."""
    if visited is None:
        visited = set()
    obj_id = id(obj)
    if obj_id in visited:
        return []
    visited.add(obj_id)

    found = []
    if isinstance(obj, target_types):
        found.append(obj)

    if hasattr(obj, "__dataclass_fields__"):
        for field in obj.__dataclass_fields__:
            val = getattr(obj, field)
            found.extend(_find_objects_of_type(val, target_types, visited))
    elif isinstance(obj, (list, tuple, set, frozenset)):
        for item in obj:
            found.extend(_find_objects_of_type(item, target_types, visited))
    elif isinstance(obj, dict):
        for k, v in obj.items():
            found.extend(_find_objects_of_type(k, target_types, visited))
            found.extend(_find_objects_of_type(v, target_types, visited))
    elif hasattr(obj, "__dict__"):
        for val in obj.__dict__.values():
            found.extend(_find_objects_of_type(val, target_types, visited))

    return found


# =============================================================================
# 1. Full Pipeline & Idempotency (Existing Test 1 updated)
# =============================================================================


def test_full_successful_session_and_idempotency():
    # 1. Setup multi-item scenario to produce multiple sibling decisions
    b1 = factories.bank_item("b1", account_id="acc-main", amount=100_000, reference="INV-100")
    j1 = factories.book_item("j1", amount=100_000, description="Client Alpha INV-100", reference="INV-100")

    b2 = factories.bank_item("b2", account_id="acc-main", amount=200_000, reference="INV-200")
    j2 = factories.book_item("j2", amount=200_000, description="Client Beta INV-200", reference="INV-200")

    repo, engine, rs, dc, recs, hydrate_state, state = _build_env(
        bank_items=(b1, b2), book_items=(j1, j2)
    )

    session = BookkeepingSession(
        state=state,
        engine=engine,
        routing_service=rs,
        dag_classifier=dc,
        reconciliation_service=recs,
    )

    # 2. Run session
    result = session.run()

    # 3. Assert full pipeline success
    assert result.is_success
    assert result.failure_stage is None

    # Assert state is closed on success
    assert state.is_closed
    assert not hasattr(result, "state")

    # Revisions progress correctly
    assert result.starting_persistence_revision == 1
    # 3 stages applied -> +3 revisions
    assert result.final_persistence_revision == 4
    assert result.final_local_state_revision == 3
    assert result.last_consistent_local_state_revision == 3

    # Final validation always runs on successful pipeline
    assert result.final_validation_status is not None
    assert result.final_validation_status.is_valid

    # Routing stage applied
    assert result.routing_stage_result is not None
    assert result.routing_stage_result.status == SessionStageStatus.APPLIED
    assert result.routing_stage_result.is_applied
    assert result.routing_stage_result.command_count == 2
    assert len(result.routing_stage_result.command_ids) == 2

    # DAG sees routing-produced state, DAG stage applied
    assert result.dag_stage_result is not None
    assert result.dag_stage_result.status == SessionStageStatus.APPLIED
    assert result.dag_stage_result.is_applied
    assert result.dag_stage_result.command_count == 2
    assert len(result.dag_stage_result.command_ids) == 2

    # Reconciliation sees routing + classification, Recon stage applied
    assert result.reconciliation_stage_result is not None
    assert result.reconciliation_stage_result.status == SessionStageStatus.APPLIED
    assert result.reconciliation_stage_result.is_applied
    assert result.reconciliation_stage_result.command_count == 2
    assert len(result.reconciliation_stage_result.command_ids) == 2

    # Fingerprints captured before close
    assert result.artifact_fingerprint is not None and result.artifact_fingerprint != ""
    assert result.state_fingerprint is not None and result.state_fingerprint != ""

    # 4. Rerun (idempotency, empty stages)
    # Rehydrate
    state2 = hydrate_state("session-2")
    assert state2.revision == 0
    assert state2.persistence_revision == 4

    session2 = BookkeepingSession(
        state=state2,
        engine=engine,
        routing_service=rs,
        dag_classifier=dc,
        reconciliation_service=recs,
    )
    result2 = session2.run()

    assert result2.is_success
    # Empty stages: local revision remains 0, persistence remains 4
    assert result2.starting_persistence_revision == 4
    assert result2.final_persistence_revision == 4
    assert result2.final_local_state_revision == 0

    assert result2.routing_stage_result.status == SessionStageStatus.SKIPPED
    assert result2.dag_stage_result.status == SessionStageStatus.SKIPPED
    assert result2.reconciliation_stage_result.status == SessionStageStatus.SKIPPED

    # State remains closed
    assert state2.is_closed

    # Durable results survive rehydration
    assert result2.artifact_fingerprint == result.artifact_fingerprint


# =============================================================================
# 2. Stage Rejection Tests (Existing Tests 2, 3, 4 updated)
# =============================================================================


def test_routing_rejection_stops_later_stages():
    b1 = factories.bank_item("b1", account_id="acc-main", amount=100_000)
    j1 = factories.book_item("j1", amount=100_000)

    repo, engine, rs, dc, recs, hydrate_state, state = _build_env(
        bank_items=(b1,), book_items=(j1,)
    )

    class SabotagedEngineRouting(TransitionEngine):
        def apply_batch(self, *, state, batch):
            if isinstance(batch.commands[0], CreateRoutingDecisionCommand):
                return BatchTransitionResult.rejected(
                    batch=batch,
                    state=state,
                    code=RejectionCode.UNKNOWN_BANK_ITEM,
                    message="Sabotaged Routing rejection",
                )
            return super().apply_batch(state=state, batch=batch)

    bad_engine = SabotagedEngineRouting(repository=repo)

    session = BookkeepingSession(
        state=state,
        engine=bad_engine,
        routing_service=rs,
        dag_classifier=dc,
        reconciliation_service=recs,
    )

    result = session.run()

    assert not result.is_success
    assert result.failure_stage == FailureStage.ROUTING
    assert result.routing_stage_result.status == SessionStageStatus.REJECTED
    assert result.routing_stage_result.rejection_code == RejectionCode.UNKNOWN_BANK_ITEM.value

    # Later stages explicitly NOT_RUN
    assert result.dag_stage_result.status == SessionStageStatus.NOT_RUN
    assert result.reconciliation_stage_result.status == SessionStageStatus.NOT_RUN

    # State closes on ordinary failure
    assert state.is_closed


def test_dag_rejection_preserves_committed_routing():
    b1 = factories.bank_item("b1", account_id="acc-main", amount=100_000)
    j1 = factories.book_item("j1", amount=100_000, description="Client Alpha INV-100")

    repo, engine, rs, dc, recs, hydrate_state, state = _build_env(
        bank_items=(b1,), book_items=(j1,)
    )

    class SabotagedEngineDag(TransitionEngine):
        def apply_batch(self, *, state, batch):
            if isinstance(batch.commands[0], CreateClassificationCommand):
                return BatchTransitionResult.rejected(
                    batch=batch,
                    state=state,
                    code=RejectionCode.UNKNOWN_BANK_ITEM,
                    message="Sabotaged DAG rejection",
                )
            return super().apply_batch(state=state, batch=batch)

    bad_engine = SabotagedEngineDag(repository=repo)

    session = BookkeepingSession(
        state=state,
        engine=bad_engine,
        routing_service=rs,
        dag_classifier=dc,
        reconciliation_service=recs,
    )

    result = session.run()

    assert not result.is_success
    assert result.failure_stage == FailureStage.DAG

    # Routing succeeded and was preserved
    assert result.starting_persistence_revision == 1
    assert result.final_persistence_revision == 2
    assert result.routing_stage_result.status == SessionStageStatus.APPLIED
    assert result.dag_stage_result.status == SessionStageStatus.REJECTED

    # Reconciliation never ran
    assert result.reconciliation_stage_result.status == SessionStageStatus.NOT_RUN

    # State closes on ordinary failure
    assert state.is_closed


def test_reconciliation_rejection_preserves_routing_and_classification():
    b1 = factories.bank_item("b1", account_id="acc-main", amount=100_000, reference="INV-100")
    j1 = factories.book_item("j1", amount=100_000, description="Client Alpha INV-100", reference="INV-100")

    repo, engine, rs, dc, recs, hydrate_state, state = _build_env(
        bank_items=(b1,), book_items=(j1,)
    )

    class SabotagedEngineRecon(TransitionEngine):
        def apply_batch(self, *, state, batch):
            if isinstance(batch.commands[0], CreateReconciliationCommand):
                return BatchTransitionResult.rejected(
                    batch=batch,
                    state=state,
                    code=RejectionCode.UNKNOWN_BANK_ITEM,
                    message="Sabotaged Recon rejection",
                )
            return super().apply_batch(state=state, batch=batch)

    bad_engine = SabotagedEngineRecon(repository=repo)

    session = BookkeepingSession(
        state=state,
        engine=bad_engine,
        routing_service=rs,
        dag_classifier=dc,
        reconciliation_service=recs,
    )

    result = session.run()

    assert not result.is_success
    assert result.failure_stage == FailureStage.RECONCILIATION

    # Routing and DAG succeeded and were preserved
    assert result.starting_persistence_revision == 1
    assert result.final_persistence_revision == 3
    assert result.routing_stage_result.status == SessionStageStatus.APPLIED
    assert result.dag_stage_result.status == SessionStageStatus.APPLIED
    assert result.reconciliation_stage_result.status == SessionStageStatus.REJECTED

    # State closes on ordinary failure
    assert state.is_closed


# =============================================================================
# 3. Stage Execution Models & Validation Tests
# =============================================================================


def test_all_empty_session():
    """
    When snapshot has no actionable items:
    - all three stages are SKIPPED
    - local state revision stays 0
    - persistence revision remains unchanged
    - state is closed
    """
    repo, engine, rs, dc, recs, hydrate_state, state = _build_env(
        bank_items=(), book_items=()
    )

    session = BookkeepingSession(
        state=state,
        engine=engine,
        routing_service=rs,
        dag_classifier=dc,
        reconciliation_service=recs,
    )

    result = session.run()

    assert result.is_success
    assert result.failure_stage is None

    assert result.routing_stage_result.status == SessionStageStatus.SKIPPED
    assert result.dag_stage_result.status == SessionStageStatus.SKIPPED
    assert result.reconciliation_stage_result.status == SessionStageStatus.SKIPPED

    assert result.routing_stage_result.batch_summary is None
    assert result.dag_stage_result.batch_summary is None
    assert result.reconciliation_stage_result.batch_summary is None

    assert result.starting_persistence_revision == 1
    assert result.final_persistence_revision == 1
    assert result.final_local_state_revision == 0
    assert result.last_consistent_local_state_revision == 0

    assert state.is_closed


def test_skipped_versus_not_run_distinction():
    """
    Ensure SKIPPED (stage ran and had 0 commands) and NOT_RUN (stage never ran
    because earlier stage failed) are strictly distinguishable.
    """
    b1 = factories.bank_item("b1", account_id="acc-main", amount=100_000)
    j1 = factories.book_item("j1", amount=100_000, description="Client Alpha INV-100")

    repo, engine, rs, dc, recs, hydrate_state, state = _build_env(
        bank_items=(b1,), book_items=(j1,)
    )

    # Force routing stage to have zero commands (SKIPPED)
    with patch.object(
        RoutingService,
        "run",
        return_value=MagicMock(commands=()),
    ):
        class SabotagedEngineDag(TransitionEngine):
            def apply_batch(self, *, state, batch):
                if isinstance(batch.commands[0], CreateClassificationCommand):
                    return BatchTransitionResult.rejected(
                        batch=batch,
                        state=state,
                        code=RejectionCode.UNKNOWN_BANK_ITEM,
                        message="Sabotaged DAG rejection",
                    )
                return super().apply_batch(state=state, batch=batch)

        bad_engine = SabotagedEngineDag(repository=repo)

        session = BookkeepingSession(
            state=state,
            engine=bad_engine,
            routing_service=rs,
            dag_classifier=dc,
            reconciliation_service=recs,
        )

        result = session.run()

    assert not result.is_success
    assert result.failure_stage == FailureStage.DAG

    # Routing stage ran with 0 commands -> SKIPPED
    assert result.routing_stage_result.status == SessionStageStatus.SKIPPED
    assert result.routing_stage_result.is_skipped
    assert not result.routing_stage_result.is_not_run

    # DAG stage ran and was rejected -> REJECTED
    assert result.dag_stage_result.status == SessionStageStatus.REJECTED
    assert result.dag_stage_result.is_rejected

    # Reconciliation stage never ran -> NOT_RUN
    assert result.reconciliation_stage_result.status == SessionStageStatus.NOT_RUN
    assert result.reconciliation_stage_result.is_not_run
    assert not result.reconciliation_stage_result.is_skipped


def test_catastrophic_post_commit_failure():
    """
    When TransitionEngine raises CommittedStateApplicationError during routing:
    - routing stage is explicitly CATASTROPHIC
    - later stages are NOT_RUN
    - SessionResult successfully returns
    - no BookkeepingStateClosedError escapes
    - final durable persistence revision comes from committed result
    - final_local_state_revision is None (NOT 0)
    - fingerprints are None
    - state remains closed
    """
    b1 = factories.bank_item("b1", account_id="acc-main", amount=100_000)
    j1 = factories.book_item("j1", amount=100_000)

    repo, engine, rs, dc, recs, hydrate_state, state = _build_env(
        bank_items=(b1,), book_items=(j1,)
    )

    session = BookkeepingSession(
        state=state,
        engine=engine,
        routing_service=rs,
        dag_classifier=dc,
        reconciliation_service=recs,
    )

    fault_msg = "Catastrophic hardware fault during delta application"
    with patch.object(
        TransitionEngine,
        "_apply_delta",
        side_effect=RuntimeError(fault_msg),
    ):
        result = session.run()

    assert not result.is_success
    assert result.failure_stage == FailureStage.COMMITTED_STATE_APPLICATION
    assert fault_msg in result.failure_reason

    # Persistence committed successfully before the crash
    assert result.starting_persistence_revision == 1
    assert result.final_persistence_revision == 2
    assert repo.current_revision(company_id=result.company_id) == 2

    # Local state diverged catastrophically
    assert result.final_local_state_revision is None
    assert result.last_consistent_local_state_revision == 0

    # Fingerprints are None
    assert result.state_fingerprint is None
    assert result.artifact_fingerprint is None

    # Routing stage resulted in catastrophic failure, later stages NOT_RUN
    assert result.routing_stage_result is not None
    assert result.routing_stage_result.status == SessionStageStatus.CATASTROPHIC
    assert result.routing_stage_result.is_catastrophic
    assert result.routing_stage_result.batch_summary.rejection_code == "COMMITTED_STATE_APPLICATION_ERROR"
    assert result.routing_stage_result.batch_summary.previous_state_revision == result.last_consistent_local_state_revision
    assert result.routing_stage_result.batch_summary.resulting_state_revision is None
    assert result.routing_stage_result.batch_summary.resulting_persistence_revision == 2

    assert result.dag_stage_result.status == SessionStageStatus.NOT_RUN
    assert result.reconciliation_stage_result.status == SessionStageStatus.NOT_RUN

    # State is closed and no property can be read from it
    assert state.is_closed
    with pytest.raises(BookkeepingStateClosedError):
        _ = state.revision
    with pytest.raises(BookkeepingStateClosedError):
        _ = state.context


def test_catastrophic_dag_stage():
    """
    Catastrophic DAG stage preserves routing commit, DAG is CATASTROPHIC,
    reconciliation is NOT_RUN.
    """
    b1 = factories.bank_item("b1", account_id="acc-main", amount=100_000)
    j1 = factories.book_item("j1", amount=100_000, description="Client Alpha INV-100")

    repo, engine, rs, dc, recs, hydrate_state, state = _build_env(
        bank_items=(b1,), book_items=(j1,)
    )

    orig_apply_delta = TransitionEngine._apply_delta
    call_count = 0
    fault_msg = "Catastrophic fault in DAG delta"

    def fail_on_second_delta(*, state, delta):
        nonlocal call_count
        call_count += 1
        if call_count == 2:
            raise RuntimeError(fault_msg)
        return orig_apply_delta(state=state, delta=delta)

    with patch.object(TransitionEngine, "_apply_delta", side_effect=fail_on_second_delta):
        session = BookkeepingSession(
            state=state,
            engine=engine,
            routing_service=rs,
            dag_classifier=dc,
            reconciliation_service=recs,
        )
        result = session.run()

    assert not result.is_success
    assert result.failure_stage == FailureStage.COMMITTED_STATE_APPLICATION

    # Routing succeeded and is preserved
    assert result.routing_stage_result.status == SessionStageStatus.APPLIED
    assert result.routing_stage_result.is_applied

    # DAG failed catastrophically
    assert result.dag_stage_result.status == SessionStageStatus.CATASTROPHIC
    assert result.dag_stage_result.is_catastrophic
    assert result.dag_stage_result.batch_summary.previous_state_revision == result.last_consistent_local_state_revision
    assert result.dag_stage_result.batch_summary.resulting_state_revision is None
    assert result.dag_stage_result.batch_summary.resulting_persistence_revision == 3

    # Reconciliation was NOT_RUN
    assert result.reconciliation_stage_result.status == SessionStageStatus.NOT_RUN

    # Revisions & State
    assert result.starting_persistence_revision == 1
    assert result.final_persistence_revision == 3
    assert result.final_local_state_revision is None
    assert result.last_consistent_local_state_revision == 1
    assert result.state_fingerprint is None
    assert result.artifact_fingerprint is None
    assert state.is_closed


def test_catastrophic_reconciliation_stage():
    """
    Catastrophic reconciliation stage preserves earlier routing and DAG commits,
    reconciliation is CATASTROPHIC.
    """
    b1 = factories.bank_item("b1", account_id="acc-main", amount=100_000, reference="INV-100")
    j1 = factories.book_item("j1", amount=100_000, description="Client Alpha INV-100", reference="INV-100")

    repo, engine, rs, dc, recs, hydrate_state, state = _build_env(
        bank_items=(b1,), book_items=(j1,)
    )

    orig_apply_delta = TransitionEngine._apply_delta
    call_count = 0
    fault_msg = "Catastrophic fault in reconciliation delta"

    def fail_on_third_delta(*, state, delta):
        nonlocal call_count
        call_count += 1
        if call_count == 3:
            raise RuntimeError(fault_msg)
        return orig_apply_delta(state=state, delta=delta)

    with patch.object(TransitionEngine, "_apply_delta", side_effect=fail_on_third_delta):
        session = BookkeepingSession(
            state=state,
            engine=engine,
            routing_service=rs,
            dag_classifier=dc,
            reconciliation_service=recs,
        )
        result = session.run()

    assert not result.is_success
    assert result.failure_stage == FailureStage.COMMITTED_STATE_APPLICATION

    # Routing and DAG succeeded
    assert result.routing_stage_result.status == SessionStageStatus.APPLIED
    assert result.dag_stage_result.status == SessionStageStatus.APPLIED

    # Reconciliation failed catastrophically
    assert result.reconciliation_stage_result.status == SessionStageStatus.CATASTROPHIC
    assert result.reconciliation_stage_result.is_catastrophic
    assert result.reconciliation_stage_result.batch_summary.previous_state_revision == result.last_consistent_local_state_revision
    assert result.reconciliation_stage_result.batch_summary.resulting_state_revision is None
    assert result.reconciliation_stage_result.batch_summary.resulting_persistence_revision == 4

    # Revisions & State
    assert result.starting_persistence_revision == 1
    assert result.final_persistence_revision == 4
    assert result.final_local_state_revision is None
    assert result.last_consistent_local_state_revision == 2
    assert result.state_fingerprint is None
    assert result.artifact_fingerprint is None
    assert state.is_closed


def test_catastrophic_paths_never_read_closed_state():
    """
    Verify none of the catastrophic result building paths read from closed BookkeepingState.
    """
    b1 = factories.bank_item("b1", account_id="acc-main", amount=100_000, reference="INV-100")
    j1 = factories.book_item("j1", amount=100_000, description="Client Alpha INV-100", reference="INV-100")

    for fail_stage_idx in (1, 2, 3):
        repo, engine, rs, dc, recs, hydrate_state, state = _build_env(
            bank_items=(b1,), book_items=(j1,)
        )

        orig_apply_delta = TransitionEngine._apply_delta
        call_count = 0

        def fail_on_target_delta(*, state, delta):
            nonlocal call_count
            call_count += 1
            if call_count == fail_stage_idx:
                raise RuntimeError("simulated fault")
            return orig_apply_delta(state=state, delta=delta)

        with patch.object(TransitionEngine, "_apply_delta", side_effect=fail_on_target_delta):
            session = BookkeepingSession(
                state=state,
                engine=engine,
                routing_service=rs,
                dag_classifier=dc,
                reconciliation_service=recs,
            )
            # Must return cleanly without BookkeepingStateClosedError escaping
            result = session.run()
            assert not result.is_success
            assert result.failure_stage == FailureStage.COMMITTED_STATE_APPLICATION
            assert state.is_closed


def test_state_closed_after_successful_session():
    """
    State is always forcibly closed after a successful session.
    """
    b1 = factories.bank_item("b1", account_id="acc-main", amount=100_000)
    j1 = factories.book_item("j1", amount=100_000, description="Client Alpha INV-100")

    repo, engine, rs, dc, recs, hydrate_state, state = _build_env(
        bank_items=(b1,), book_items=(j1,)
    )

    session = BookkeepingSession(
        state=state,
        engine=engine,
        routing_service=rs,
        dag_classifier=dc,
        reconciliation_service=recs,
    )

    result = session.run()
    assert result.is_success

    assert state.is_closed
    with pytest.raises(BookkeepingStateClosedError):
        _ = state.revision
    with pytest.raises(BookkeepingStateClosedError):
        _ = state.context
    with pytest.raises(BookkeepingStateClosedError):
        _ = state.book_items


def test_state_closed_after_ordinary_rejection():
    """
    State is always closed after an ordinary rejection failure.
    """
    b1 = factories.bank_item("b1", account_id="acc-main", amount=100_000)
    j1 = factories.book_item("j1", amount=100_000)

    repo, engine, rs, dc, recs, hydrate_state, state = _build_env(
        bank_items=(b1,), book_items=(j1,)
    )

    class SabotagedEngine(TransitionEngine):
        def apply_batch(self, *, state, batch):
            return BatchTransitionResult.rejected(
                batch=batch,
                state=state,
                code=RejectionCode.UNKNOWN_BANK_ITEM,
                message="Ordinary rejection",
            )

    session = BookkeepingSession(
        state=state,
        engine=SabotagedEngine(repository=repo),
        routing_service=rs,
        dag_classifier=dc,
        reconciliation_service=recs,
    )

    result = session.run()
    assert not result.is_success

    assert state.is_closed
    with pytest.raises(BookkeepingStateClosedError):
        _ = state.revision
    with pytest.raises(BookkeepingStateClosedError):
        _ = state.context


def test_final_validation_failure():
    """
    Final validation failure:
    - reports FailureStage.VALIDATION
    - already committed artifacts remain durable
    - state closes
    - no rollback occurs
    """
    b1 = factories.bank_item("b1", account_id="acc-main", amount=100_000, reference="INV-100")
    j1 = factories.book_item("j1", amount=100_000, description="Client Alpha INV-100", reference="INV-100")

    repo, engine, rs, dc, recs, hydrate_state, state = _build_env(
        bank_items=(b1,), book_items=(j1,)
    )

    session = BookkeepingSession(
        state=state,
        engine=engine,
        routing_service=rs,
        dag_classifier=dc,
        reconciliation_service=recs,
    )

    mock_report = StateValidationReport(
        state_revision=3,
        issues=(
            ValidationIssue(
                code=ValidationCode.UNKNOWN_BANK_ITEM,
                message="Simulated validation error",
                severity=ValidationSeverity.ERROR,
            ),
        ),
    )

    with patch(
        "bookkeeping_state.session.bookkeeping_session.validate_state",
        return_value=mock_report,
    ):
        result = session.run()

    assert not result.is_success
    assert result.failure_stage == FailureStage.VALIDATION
    assert "UNKNOWN_BANK_ITEM: Simulated validation error" in result.failure_reason

    # All prior durable commits remain authoritative
    assert result.starting_persistence_revision == 1
    assert result.final_persistence_revision == 4
    assert result.final_local_state_revision == 3
    assert repo.current_revision(company_id=result.company_id) == 4

    # State is closed
    assert state.is_closed


def test_fingerprints_captured_before_close():
    """
    Fingerprints exist on success and ordinary failure and are captured
    before state closes. On catastrophic failure, fingerprints are None.
    """
    b1 = factories.bank_item("b1", account_id="acc-main", amount=100_000)
    j1 = factories.book_item("j1", amount=100_000, description="Client Alpha INV-100")

    # 1. Success
    repo, engine, rs, dc, recs, hydrate_state, state = _build_env(
        bank_items=(b1,), book_items=(j1,)
    )
    res_success = BookkeepingSession(
        state=state,
        engine=engine,
        routing_service=rs,
        dag_classifier=dc,
        reconciliation_service=recs,
    ).run()
    assert res_success.state_fingerprint is not None and len(res_success.state_fingerprint) == 64
    assert res_success.artifact_fingerprint is not None and len(res_success.artifact_fingerprint) == 64

    # 2. Ordinary failure
    repo, engine, rs, dc, recs, hydrate_state, state2 = _build_env(
        bank_items=(b1,), book_items=(j1,)
    )
    class RejectingEngine(TransitionEngine):
        def apply_batch(self, *, state, batch):
            return BatchTransitionResult.rejected(
                batch=batch,
                state=state,
                code=RejectionCode.UNKNOWN_BANK_ITEM,
                message="Ordinary rejection",
            )
    res_fail = BookkeepingSession(
        state=state2,
        engine=RejectingEngine(repository=repo),
        routing_service=rs,
        dag_classifier=dc,
        reconciliation_service=recs,
    ).run()
    assert res_fail.state_fingerprint is not None and len(res_fail.state_fingerprint) == 64
    assert res_fail.artifact_fingerprint is not None and len(res_fail.artifact_fingerprint) == 64

    # 3. Catastrophic failure
    repo, engine, rs, dc, recs, hydrate_state, state3 = _build_env(
        bank_items=(b1,), book_items=(j1,)
    )
    with patch.object(
        TransitionEngine,
        "_apply_delta",
        side_effect=RuntimeError("simulated crash"),
    ):
        res_cat = BookkeepingSession(
            state=state3,
            engine=engine,
            routing_service=rs,
            dag_classifier=dc,
            reconciliation_service=recs,
        ).run()
    assert res_cat.state_fingerprint is None
    assert res_cat.artifact_fingerprint is None


def test_session_result_is_detached():
    """
    SessionResult must not contain BookkeepingState, BookkeepingQueries,
    repository/service objects, or state-backed mutable objects.
    """
    b1 = factories.bank_item("b1", account_id="acc-main", amount=100_000)
    j1 = factories.book_item("j1", amount=100_000, description="Client Alpha INV-100")

    repo, engine, rs, dc, recs, hydrate_state, state = _build_env(
        bank_items=(b1,), book_items=(j1,)
    )

    result = BookkeepingSession(
        state=state,
        engine=engine,
        routing_service=rs,
        dag_classifier=dc,
        reconciliation_service=recs,
    ).run()

    # Result is detached
    assert not hasattr(result, "state")
    assert not hasattr(result, "queries")
    assert not hasattr(result, "repository")
    assert not hasattr(result, "_state")

    # Result is immutable
    with pytest.raises(FrozenInstanceError):
        result.is_success = False

    with pytest.raises(FrozenInstanceError):
        result.routing_stage_result.status = SessionStageStatus.SKIPPED


def test_session_result_recursively_contains_no_hypotheses_batches_or_commands():
    """
    Prove that SessionResult recursively contains:
    - no ReconciliationHypothesis
    - no TransitionBatch
    - no TransitionCommand / BookkeepingCommand
    """
    b1 = factories.bank_item("b1", account_id="acc-main", amount=100_000)
    j1 = factories.book_item("j1", amount=100_000, description="Client Alpha INV-100")

    repo, engine, rs, dc, recs, hydrate_state, state = _build_env(
        bank_items=(b1,), book_items=(j1,)
    )

    # Put a hypothesis in state before running reconciliation
    hypo = ReconciliationHypothesis(
        id="hypo-leak-check",
        state_revision=state.revision,
        bank_allocations=(
            BankAllocation(bank_item_id="b1", amount_units="100000"),
        ),
        book_allocations=(
            BookAllocation(book_item_id="j1", amount_units="100000"),
        ),
        eligibility=Eligibility.SELECTABLE,
        utility=100,
        generated_at=datetime.now(timezone.utc),
    )
    state._put_reconciliation_hypothesis(hypo)

    session = BookkeepingSession(
        state=state,
        engine=engine,
        routing_service=rs,
        dag_classifier=dc,
        reconciliation_service=recs,
    )
    result = session.run()
    assert result.is_success

    # Strict recursive inspection of the entire SessionResult tree
    leaked_hypotheses = _find_objects_of_type(result, ReconciliationHypothesis)
    assert leaked_hypotheses == [], f"Found leaked ReconciliationHypothesis: {leaked_hypotheses}"

    leaked_batches = _find_objects_of_type(result, TransitionBatch)
    assert leaked_batches == [], f"Found leaked TransitionBatch: {leaked_batches}"

    leaked_commands = _find_objects_of_type(result, CommandBase)
    assert leaked_commands == [], f"Found leaked Command: {leaked_commands}"


def test_successful_reconciliation_session_exposes_useful_detached_audit_data():
    """
    Ensure the detached SessionBatchSummary provides useful audit data
    (command counts, ids, types, revisions, artifact ids) without retaining models.
    """
    b1 = factories.bank_item("b1", account_id="acc-main", amount=100_000, reference="INV-100")
    j1 = factories.book_item("j1", amount=100_000, description="Client Alpha INV-100", reference="INV-100")

    repo, engine, rs, dc, recs, hydrate_state, state = _build_env(
        bank_items=(b1,), book_items=(j1,)
    )

    session = BookkeepingSession(
        state=state,
        engine=engine,
        routing_service=rs,
        dag_classifier=dc,
        reconciliation_service=recs,
    )
    result = session.run()
    assert result.is_success

    recon_res = result.reconciliation_stage_result
    assert recon_res.status == SessionStageStatus.APPLIED
    assert recon_res.batch_summary is not None
    assert recon_res.command_count == 1
    assert len(recon_res.command_ids) == 1
    assert recon_res.command_types == ("CREATE_RECONCILIATION",)
    assert recon_res.batch_summary.batch_id is not None
    assert recon_res.batch_summary.previous_state_revision == 2
    assert recon_res.batch_summary.resulting_state_revision == 3
    assert recon_res.batch_summary.previous_persistence_revision == 3
    assert recon_res.batch_summary.resulting_persistence_revision == 4
    assert len(recon_res.artifact_ids) == 1


def test_runtime_hypotheses_do_not_survive_close_and_rehydrate():
    """
    Runtime hypotheses/events do not survive close + rehydrate AND cannot escape
    through SessionResult.
    """
    b1 = factories.bank_item("b1", account_id="acc-main", amount=100_000)
    j1 = factories.book_item("j1", amount=100_000, description="Client Alpha INV-100")

    repo, engine, rs, dc, recs, hydrate_state, state = _build_env(
        bank_items=(b1,), book_items=(j1,)
    )

    hypo = ReconciliationHypothesis(
        id="hypo-volatile-1",
        state_revision=state.revision,
        bank_allocations=(
            BankAllocation(bank_item_id="b1", amount_units="100000"),
        ),
        book_allocations=(
            BookAllocation(book_item_id="j1", amount_units="100000"),
        ),
        eligibility=Eligibility.SELECTABLE,
        utility=100,
        generated_at=datetime.now(timezone.utc),
    )
    state._put_reconciliation_hypothesis(hypo)
    assert state.get_reconciliation_hypothesis("hypo-volatile-1") is not None

    session = BookkeepingSession(
        state=state,
        engine=engine,
        routing_service=rs,
        dag_classifier=dc,
        reconciliation_service=recs,
    )
    result = session.run()
    assert result.is_success
    assert state.is_closed

    # Cannot escape through SessionResult
    assert _find_objects_of_type(result, ReconciliationHypothesis) == []

    # Does not survive rehydration
    state2 = hydrate_state("session-fresh")
    assert state2.get_reconciliation_hypothesis("hypo-volatile-1") is None


def test_second_session_revision_progression_and_counts():
    """
    Second session semantics:
    - first durable counts: routing decisions, classifications, reconciliations
    - second durable counts unchanged
    - all second-run stages explicitly SKIPPED
    - local revision remains 0 for freshly hydrated second runtime
    - persistence revision remains unchanged
    """
    b1 = factories.bank_item("b1", account_id="acc-main", amount=100_000, reference="INV-100")
    j1 = factories.book_item("j1", amount=100_000, description="Client Alpha INV-100", reference="INV-100")
    b2 = factories.bank_item("b2", account_id="acc-main", amount=200_000, reference="INV-200")
    j2 = factories.book_item("j2", amount=200_000, description="Client Beta INV-200", reference="INV-200")

    repo, engine, rs, dc, recs, hydrate_state, state1 = _build_env(
        bank_items=(b1, b2), book_items=(j1, j2)
    )

    # First session: local S0, durable P1 -> local S3, durable P4
    assert state1.revision == 0
    assert state1.persistence_revision == 1

    session1 = BookkeepingSession(
        state=state1,
        engine=engine,
        routing_service=rs,
        dag_classifier=dc,
        reconciliation_service=recs,
    )
    result1 = session1.run()
    assert result1.is_success
    assert result1.starting_persistence_revision == 1
    assert result1.final_persistence_revision == 4
    assert result1.final_local_state_revision == 3

    # Check first durable counts
    snap1 = repo.load_snapshot(company_id=result1.company_id)
    assert len(snap1.routing_decisions) == 2
    assert len(snap1.classifications) == 2
    assert len(snap1.reconciliations) == 2

    # Second session: freshly hydrated state starts local S0, durable P4
    state2 = hydrate_state("session-2")
    assert state2.revision == 0
    assert state2.persistence_revision == 4

    session2 = BookkeepingSession(
        state=state2,
        engine=engine,
        routing_service=rs,
        dag_classifier=dc,
        reconciliation_service=recs,
    )
    result2 = session2.run()

    # Second run assertions:
    assert result2.is_success
    assert result2.starting_persistence_revision == 4
    assert result2.final_persistence_revision == 4
    assert result2.final_local_state_revision == 0

    assert result2.routing_stage_result.status == SessionStageStatus.SKIPPED
    assert result2.dag_stage_result.status == SessionStageStatus.SKIPPED
    assert result2.reconciliation_stage_result.status == SessionStageStatus.SKIPPED

    # Durable counts unchanged
    snap2 = repo.load_snapshot(company_id=result1.company_id)
    assert len(snap2.routing_decisions) == 2
    assert len(snap2.classifications) == 2
    assert len(snap2.reconciliations) == 2


def test_fresh_query_view_sequencing_between_stages():
    """
    Between stages, fresh BookkeepingQueries / DagView are constructed at each
    new state revision, seeing truth committed by earlier stages.
    """
    b1 = factories.bank_item("b1", account_id="acc-main", amount=100_000)
    j1 = factories.book_item("j1", amount=100_000, description="Client Alpha INV-100")

    repo, engine, rs, dc, recs, hydrate_state, state = _build_env(
        bank_items=(b1,), book_items=(j1,)
    )

    stage_queries_revisions: list[int] = []

    orig_routing_run = RoutingService.run
    def spied_routing_run(self, queries, solver_run_id, issued_at):
        stage_queries_revisions.append(queries._state.revision)
        assert queries.active_route("j1") is None
        return orig_routing_run(self, queries=queries, solver_run_id=solver_run_id, issued_at=issued_at)

    orig_dag_classify = SimulatedAseClassifier.classify_view
    def spied_dag_classify(self, dag_view):
        # DagView was built from queries at revision 1, which has active route
        stage_queries_revisions.append(dag_view.state_revision)
        assert dag_view.items[0].active_bank_account_id == "acc-main"
        assert dag_view.items[0].existing_classification_id is None
        return orig_dag_classify(self, dag_view)

    orig_recon_plan = ReconciliationService.plan
    def spied_recon_plan(self, queries, solver_run_id, session_id, issued_at):
        # Recon was built from queries at revision 2, which has both route and classification
        stage_queries_revisions.append(queries._state.revision)
        assert queries.active_route("j1") is not None
        assert queries.active_classification("j1") is not None
        return orig_recon_plan(
            self,
            queries=queries,
            solver_run_id=solver_run_id,
            session_id=session_id,
            issued_at=issued_at,
        )

    with patch.object(RoutingService, "run", spied_routing_run), \
         patch.object(SimulatedAseClassifier, "classify_view", spied_dag_classify), \
         patch.object(ReconciliationService, "plan", spied_recon_plan):
        result = BookkeepingSession(
            state=state,
            engine=engine,
            routing_service=rs,
            dag_classifier=dc,
            reconciliation_service=recs,
        ).run()

    assert result.is_success
    assert stage_queries_revisions == [0, 1, 2]
