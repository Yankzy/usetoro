from __future__ import annotations

from datetime import datetime, timezone
from typing import Callable

from bookkeeping_state_eval.dag.simulated_ase import SimulatedAseClassifier
from bookkeeping_state_eval.hydration.hydrator import BookkeepingHydrator
from bookkeeping_state_eval.persistence.in_memory import InMemoryBookkeepingRepository
from bookkeeping_state_eval.persistence.repository import BookkeepingRepository
from bookkeeping_state_eval.reconciliation.service import ReconciliationService
from bookkeeping_state_eval.routing.scorer import ZeroRoutingSemanticScoreProvider
from bookkeeping_state_eval.routing.service import RoutingService
from bookkeeping_state_eval.scenarios.expected_truth import (
    ExpectedTruthVerdict,
    validate_expected_truth,
)
from bookkeeping_state_eval.scenarios.models import (
    ScenarioDefinition,
    ScenarioResult,
)
from bookkeeping_state_eval.session.bookkeeping_session import BookkeepingSession
from bookkeeping_state_eval.state.fingerprint import artifact_fingerprint
from bookkeeping_state_eval.state.queries import BookkeepingQueries
from bookkeeping_state_eval.state.validation import validate_state
from bookkeeping_state_eval.transitions.engine import TransitionEngine

DEFAULT_CLOCK = datetime(2026, 1, 31, 12, 0, 0, tzinfo=timezone.utc)


def seed_scenario_repository(
    scenario: ScenarioDefinition,
    *,
    repository_factory: Callable[..., BookkeepingRepository] | None = None,
) -> BookkeepingRepository:
    """
    Seed a repository with the initial durable snapshot of a scenario.
    """
    snapshot = scenario.to_initial_snapshot()
    if repository_factory is not None:
        return repository_factory([snapshot])
    return InMemoryBookkeepingRepository(initial_snapshots=[snapshot])


class ScenarioRunner:
    """
    Deterministic execution runner for bookkeeping evaluation scenarios.

    Lifecycle:
        ScenarioDefinition
            |
            v
        seed repository
            |
            v
        hydrate fresh runtime S0
            |
            v
        run BookkeepingSession (destroys live S0)
            |
            v
        rehydrate completely fresh from durable persistence
            |
            v
        independently validate closing truth & state invariants
            |
            v
        ScenarioResult (detached & immutable)
    """

    def __init__(
        self,
        *,
        clock_factory: Callable[[], datetime] | None = None,
    ) -> None:
        self._clock_factory = clock_factory or (lambda: DEFAULT_CLOCK)

    def run(
        self,
        scenario: ScenarioDefinition,
        *,
        session_id: str | None = None,
        repository: BookkeepingRepository | None = None,
    ) -> ScenarioResult:
        """
        Execute one scenario from its initial durable snapshot to closing truth.
        """
        repo = repository or seed_scenario_repository(scenario)
        clock_time = scenario.clock_time or self._clock_factory()
        session_run_id = session_id or f"session-{scenario.scenario_id}"

        # 1. Hydrate fresh initial runtime state S0
        hydrator = BookkeepingHydrator(
            repository=repo,
            clock=lambda: clock_time,
            session_id_factory=lambda: session_run_id,
        )
        live_state = hydrator.hydrate(
            company_id=scenario.context.company_id,
            session_id=session_run_id,
        )

        # 2. Configure services
        engine = TransitionEngine(repository=repo)
        routing_service = RoutingService(
            semantic_provider=scenario.routing_semantic_provider
            or ZeroRoutingSemanticScoreProvider()
        )
        dag_classifier = scenario.dag_classifier or SimulatedAseClassifier()
        recon_service = scenario.reconciliation_service or ReconciliationService()

        # 3. Execute BookkeepingSession
        session = BookkeepingSession(
            state=live_state,
            engine=engine,
            routing_service=routing_service,
            dag_classifier=dag_classifier,
            reconciliation_service=recon_service,
        )
        session_result = session.run()

        # Invariant check: live state must be completely closed/destroyed
        assert live_state.is_closed, "Live state was not closed by session termination"

        # 4. Rehydrate fresh state from durable persistence
        rehydrated_id = f"{session_run_id}-rehydrated"
        rehydrated_state = hydrator.hydrate(
            company_id=scenario.context.company_id,
            session_id=rehydrated_id,
        )

        try:
            # 5. Validate durable state consistency
            validation_report = validate_state(rehydrated_state)

            # 6. Validate independently authored expected truth
            queries = BookkeepingQueries(rehydrated_state)
            truth_verdict = validate_expected_truth(queries, scenario.expected_truth)

            # 7. Record durable fingerprint
            final_durable_fp = artifact_fingerprint(rehydrated_state)
            final_persistence_rev = rehydrated_state.persistence_revision

        finally:
            rehydrated_state.close()

        # 8. Evaluate overall verdict and distinguish failure modes
        failure_messages: list[str] = []
        if not session_result.is_success:
            failure_messages.append(
                f"Session failed at stage {session_result.failure_stage}: "
                f"{session_result.failure_reason}"
            )

        if not validation_report.is_valid:
            validation_errs = [
                f"{issue.code}: {issue.message}" for issue in validation_report.errors
            ]
            failure_messages.append(
                f"Hydrated closing state is invalid: {'; '.join(validation_errs)}"
            )

        if not truth_verdict.is_match:
            failure_messages.append(
                "Expected truth mismatch:\n" + "\n".join(truth_verdict.mismatches)
            )

        is_pass = (
            session_result.is_success
            and validation_report.is_valid
            and truth_verdict.is_match
        )
        failure_reason = (
            "\n".join(failure_messages) if failure_messages else None
        )

        return ScenarioResult(
            scenario_id=scenario.scenario_id,
            is_pass=is_pass,
            expected_truth_verdict=truth_verdict,
            mismatches=truth_verdict.mismatches,
            session_result=session_result,
            final_persistence_revision=final_persistence_rev,
            final_durable_fingerprint=final_durable_fp,
            failure_reason=failure_reason,
        )

    def run_idempotent_session(
        self,
        scenario: ScenarioDefinition,
    ) -> tuple[ScenarioResult, ScenarioResult]:
        """
        Run a scenario through two sequential sessions to verify idempotency.

        The second session starts fresh S0 from the durable repository left by the
        first session. It must produce zero new mutations and identical closing truth.
        """
        repo = seed_scenario_repository(scenario)

        # First run
        result_1 = self.run(
            scenario,
            session_id=f"session-1-{scenario.scenario_id}",
            repository=repo,
        )

        # Second run on same repository
        result_2 = self.run(
            scenario,
            session_id=f"session-2-{scenario.scenario_id}",
            repository=repo,
        )

        return result_1, result_2
