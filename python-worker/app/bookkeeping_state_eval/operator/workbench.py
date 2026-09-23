from __future__ import annotations

import dataclasses
from datetime import date as dt_date, datetime, timezone
import logging
from typing import Any, Callable, Sequence
import uuid

from bookkeeping_state_eval.dag.protocol import AseClassifier
from bookkeeping_state_eval.dag.simulated_ase import SimulatedAseClassifier
from bookkeeping_state_eval.domain.bank import BankAccount, BankItem
from bookkeeping_state_eval.domain.books import BookItem
from bookkeeping_state_eval.domain.commands import (
    AssertBookItemEvidenceCommand,
    BookkeepingCommand,
    CommandSource,
    InvalidateBookItemEvidenceCommand,
    InvalidateReconciliationCommand,
)
from bookkeeping_state_eval.domain.context import AccountingPolicy, BookkeepingContext
from bookkeeping_state_eval.domain.counterparties import Counterparty, CounterpartyType
from bookkeeping_state_eval.domain.enums import (
    AllocationSupport,
    Direction,
    Eligibility,
)
from bookkeeping_state_eval.domain.evidence import (
    BookItemEvidenceAssertion,
    BookItemEvidenceType,
    EvidenceSource,
)
from bookkeeping_state_eval.domain.hypotheses import ReconciliationHypothesis
from bookkeeping_state_eval.domain.money import (
    major_units_to_solver_units,
    solver_units_to_decimal,
)
from bookkeeping_state_eval.hydration.hydrator import BookkeepingHydrator
from bookkeeping_state_eval.llm.factory import (
    create_reconciliation_semantic_provider,
    create_routing_semantic_provider,
)
from bookkeeping_state_eval.persistence.in_memory import InMemoryBookkeepingRepository
from bookkeeping_state_eval.persistence.repository import (
    BookkeepingRepository,
    BookkeepingSnapshot,
    PersistenceWriteSet,
)
from bookkeeping_state_eval.reconciliation.models import (
    ReconciliationPlan,
    ReconciliationResult,
)
from bookkeeping_state_eval.reconciliation.service import ReconciliationService
from bookkeeping_state_eval.routing.scorer import (
    RoutingSemanticScoreProvider,
    ZeroRoutingSemanticScoreProvider,
)
from bookkeeping_state_eval.routing.service import RoutingService
from bookkeeping_state_eval.scenarios.catalog import SCENARIOS_MAP
from bookkeeping_state_eval.scenarios.challenges import CHALLENGES_MAP, ChallengeDefinition
from bookkeeping_state_eval.scenarios.models import ScenarioDefinition
from bookkeeping_state_eval.scenarios.temporal import (
    TEMPORAL_CHALLENGES_MAP,
    TemporalChallengeDefinition,
)
from bookkeeping_state_eval.session.bookkeeping_session import BookkeepingSession
from bookkeeping_state_eval.session.result import SessionResult
from bookkeeping_state_eval.state.bookkeeping_state import BookkeepingState
from bookkeeping_state_eval.state.queries import BookkeepingQueries
from bookkeeping_state_eval.transitions.engine import TransitionEngine

logger = logging.getLogger(__name__)
DEFAULT_CLOCK = datetime(2026, 1, 31, 12, 0, 0, tzinfo=timezone.utc)


def create_demo_repository() -> tuple[BookkeepingRepository, str]:
    """Create demo repository with 5 bank items and 5 book items."""
    company_id = "demo-company"
    bank_acc = BankAccount(id="acc-main", name="BMCE Operating MAD", currency="MAD")
    book_items = (
        BookItem(
            id="book-salary",
            origin_period="2026-09",
            date=dt_date(2026, 9, 1),
            amount_units="40000",
            direction=Direction.BOOK_BANK_CREDIT,
            currency="MAD",
            description="Salary September payroll",
            reference="SAL-SEP",
        ),
        BookItem(
            id="book-aws",
            origin_period="2026-09",
            date=dt_date(2026, 9, 2),
            amount_units="1200",
            direction=Direction.BOOK_BANK_CREDIT,
            currency="MAD",
            description="AWS Cloud hosting services",
            reference="AWS-INV-1",
        ),
        BookItem(
            id="book-rent",
            origin_period="2026-09",
            date=dt_date(2026, 9, 3),
            amount_units="12000",
            direction=Direction.BOOK_BANK_CREDIT,
            currency="MAD",
            description="Office rent Casablanca",
            reference="RENT-SEP",
        ),
        BookItem(
            id="book-supplier",
            origin_period="2026-09",
            date=dt_date(2026, 9, 4),
            amount_units="18000",
            direction=Direction.BOOK_BANK_CREDIT,
            currency="MAD",
            description="Supplier Alpha invoice payment",
            reference="SUP-ALPHA",
        ),
        BookItem(
            id="book-mystery",
            origin_period="2026-09",
            date=dt_date(2026, 9, 5),
            amount_units="7500",
            direction=Direction.BOOK_BANK_CREDIT,
            currency="MAD",
            description="VIREMENT 828192",
            reference="VIR-828192",
        ),
    )

    bank_items = (
        BankItem(
            id="bank-salary",
            bank_account_id="acc-main",
            date=dt_date(2026, 9, 1),
            amount_units="40000",
            direction=Direction.BANK_OUTFLOW,
            currency="MAD",
            description="VRT SALAIRES SEPTEMBRE",
            reference="SAL-SEP",
        ),
        BankItem(
            id="bank-aws",
            bank_account_id="acc-main",
            date=dt_date(2026, 9, 2),
            amount_units="1200",
            direction=Direction.BANK_OUTFLOW,
            currency="MAD",
            description="PRLV AMAZON WEB SERVICES",
            reference="AWS-INV-1",
        ),
        BankItem(
            id="bank-rent",
            bank_account_id="acc-main",
            date=dt_date(2026, 9, 3),
            amount_units="12000",
            direction=Direction.BANK_OUTFLOW,
            currency="MAD",
            description="LOYER BUREAU SEPTEMBRE",
            reference="RENT-SEP",
        ),
        BankItem(
            id="bank-supplier",
            bank_account_id="acc-main",
            date=dt_date(2026, 9, 4),
            amount_units="18000",
            direction=Direction.BANK_OUTFLOW,
            currency="MAD",
            description="FACTURE ALPHA SARL",
            reference="SUP-ALPHA",
        ),
        BankItem(
            id="bank-mystery",
            bank_account_id="acc-main",
            date=dt_date(2026, 9, 5),
            amount_units="8200",
            direction=Direction.BANK_OUTFLOW,
            currency="MAD",
            description="VIREMENT EMIS 828192",
            reference="VIR-DIFF",
        ),
    )

    snapshot = BookkeepingSnapshot(
        persistence_revision=1,
        context=BookkeepingContext(
            company_id=company_id,
            period_start=dt_date(2026, 9, 1),
            period_end=dt_date(2026, 9, 30),
            base_currency="MAD",
            policy=AccountingPolicy(chart_of_accounts_id="pcge"),
        ),
        bank_accounts=(bank_acc,),
        bank_items=bank_items,
        book_items=book_items,
    )
    repo = InMemoryBookkeepingRepository(initial_snapshots=[snapshot])
    return repo, company_id


def seed_scenario_repository(scenario: ScenarioDefinition) -> BookkeepingRepository:
    """Seed a fresh InMemoryBookkeepingRepository with scenario's initial snapshot."""
    snapshot = scenario.to_initial_snapshot()
    return InMemoryBookkeepingRepository(initial_snapshots=[snapshot])


@dataclasses.dataclass(frozen=True)
class WorkbenchSessionResult(SessionResult):
    """
    Detached SessionResult enriched with reconciliation plan telemetry.
    """

    reconciliation_result: ReconciliationResult | None = None
    reconciliation_plan: ReconciliationPlan | None = None

    @classmethod
    def from_session_result(
        cls,
        res: SessionResult,
        reconciliation_result: ReconciliationResult | None = None,
        reconciliation_plan: ReconciliationPlan | None = None,
    ) -> WorkbenchSessionResult:
        kwargs = {
            f.name: getattr(res, f.name) for f in dataclasses.fields(SessionResult)
        }
        return cls(
            **kwargs,
            reconciliation_result=reconciliation_result,
            reconciliation_plan=reconciliation_plan,
        )

    @property
    def review_hypotheses(self) -> tuple[ReconciliationHypothesis, ...]:
        if self.reconciliation_result is not None:
            return self.reconciliation_result.review_hypotheses
        return ()


def _extract_delta_ids(delta: Any) -> list[str]:
    if not delta:
        return []
    ids: list[str] = []
    for items in (
        delta.bank_accounts,
        delta.bank_items,
        delta.book_items,
        delta.documents,
        delta.counterparties,
        delta.routing_decisions,
        delta.routing_invalidations,
        delta.classifications,
        delta.classification_invalidations,
        delta.reconciliations,
        delta.reconciliation_invalidations,
        delta.book_item_evidence_assertions,
        delta.book_item_evidence_invalidations,
    ):
        for it in items:
            for attr in ("id", "assertion_id", "invalidation_id", "reconciliation_id", "decision_id", "book_item_id"):
                val = getattr(it, attr, None)
                if val and isinstance(val, str):
                    ids.append(val)
                    break
    return ids


class BookkeepingWorkbench:
    """
    Authoritative typed application layer for BookkeepingState operations.

    Executes all bookkeeping operations, enforces single-runtime and fail-closed
    mutation invariants, and returns detached read-safe data without exposing
    live state or private runtime engines to callers.
    """

    def __init__(
        self,
        *,
        scenario_name: str | None = None,
        semantic_provider: str = "deterministic",
        repository: BookkeepingRepository | None = None,
        company_id: str | None = None,
        debug: bool = False,
    ) -> None:
        self.debug = debug
        self.semantic_provider = semantic_provider
        self.session_counter = 0
        self.last_result: WorkbenchSessionResult | None = None
        self.last_reconciliation_result: ReconciliationResult | None = None
        self.last_recon_plan: ReconciliationPlan | None = None
        self.active_scenario: ScenarioDefinition | None = None
        self.active_challenge: ChallengeDefinition | None = None
        self.active_temporal_challenge: TemporalChallengeDefinition | None = None
        self.clock_time: datetime | None = None

        if repository is not None and company_id is not None:
            self.repository = repository
            self.company_id = company_id
            self.routing_semantic_provider = None
            self.dag_classifier = None
            self.reconciliation_service = None
            self.hydrator = BookkeepingHydrator(repository=self.repository)
            self.session_counter += 1
            self.state: BookkeepingState | None = self.hydrator.hydrate(
                company_id=self.company_id,
                session_id=f"workbench-inspection-{self.session_counter}",
            )
        elif scenario_name and scenario_name in SCENARIOS_MAP:
            self.load_scenario(scenario_name)
        else:
            self.repository, self.company_id = create_demo_repository()
            self._configure_services()
            self.hydrator = BookkeepingHydrator(repository=self.repository)
            self.session_counter += 1
            self.state = self.hydrator.hydrate(
                company_id=self.company_id,
                session_id=f"workbench-inspection-{self.session_counter}",
            )

    def _configure_services(self) -> None:
        if self.semantic_provider == "llm":
            self.routing_semantic_provider = create_routing_semantic_provider("llm")
            self.reconciliation_service = ReconciliationService(
                scorer=create_reconciliation_semantic_provider("llm")
            )
        else:
            self.routing_semantic_provider = None
            self.reconciliation_service = None
        self.dag_classifier = None

    def get_or_hydrate_state(self) -> BookkeepingState | None:
        """Return active inspection state, rehydrating if closed."""
        if self.state is None or self.state.is_closed:
            try:
                self.session_counter += 1
                self.state = self.hydrator.hydrate(
                    company_id=self.company_id,
                    session_id=f"workbench-inspection-{self.session_counter}",
                )
            except Exception as exc:
                logger.error(f"Failed to hydrate state: {exc}")
                self.state = None
        return self.state

    def close_state(self) -> None:
        """Explicitly close current inspection state."""
        if self.state is not None and not self.state.is_closed:
            self.state.close()
        self.state = None

    def rehydrate(self) -> dict[str, Any]:
        """Explicitly reopen inspection state from durable persistence."""
        self.close_state()
        self.session_counter += 1
        try:
            self.state = self.hydrator.hydrate(
                company_id=self.company_id,
                session_id=f"workbench-inspection-{self.session_counter}",
            )
            return {
                "success": True,
                "local_state_revision": self.state.revision,
                "persistence_revision": self.state.persistence_revision,
                "company_id": self.company_id,
            }
        except Exception as exc:
            self.state = None
            return {
                "success": False,
                "error": f"Rehydration failed: {exc}",
            }

    # ------------------------------------------------------------------
    # Mutation Fail-Closed Lifecycle
    # ------------------------------------------------------------------

    def _commit_source_writeset(
        self, write_set: PersistenceWriteSet, desc: str
    ) -> dict[str, Any]:
        """
        Commit exogenous source artifacts directly through authoritative repository.
        Fails closed: inspection state is closed immediately before mutation.
        """
        self.close_state()
        prev_p: int | None = None

        try:
            snap = self.repository.load_snapshot(company_id=self.company_id)
            prev_p_val: int = snap.persistence_revision
            prev_p = prev_p_val
            self.repository.commit(
                company_id=self.company_id,
                expected_revision=prev_p_val,
                write_set=write_set,
            )
        except Exception as exc:
            # Mutation failed before or during commit. Do not report success.
            # Restore usability by attempting fresh hydration from unchanged persistence.
            self.get_or_hydrate_state()
            return {
                "success": False,
                "error": f"Source mutation failed: {exc}",
                "persistence_revision_before": prev_p,
                "persistence_revision_after": prev_p,
            }

        new_p = (prev_p if prev_p is not None else 0) + 1
        # Attempt fresh post-commit hydration
        try:
            self.session_counter += 1
            self.state = self.hydrator.hydrate(
                company_id=self.company_id,
                session_id=f"workbench-inspection-{self.session_counter}",
            )
            local_rev = self.state.revision
        except Exception as exc:
            # Post-commit hydration failed! Fail closed: NEVER restore or reuse stale state.
            self.state = None
            return {
                "success": False,
                "applied": True,
                "state_closed": True,
                "catastrophic": True,
                "error": f"Catastrophic error: post-commit state hydration failed: {exc}",
                "persistence_revision_before": prev_p,
                "persistence_revision_after": new_p,
                "persistence_revision": new_p,
                "local_state_revision": None,
                "message": f"Added {desc} to durable persistence, but workbench inspection state is closed due to hydration error.",
            }

        return {
            "success": True,
            "applied": True,
            "persistence_revision": new_p,
            "persistence_revision_before": prev_p,
            "persistence_revision_after": new_p,
            "local_state_revision": local_rev,
            "message": f"Successfully added {desc}.",
        }

    def _apply_transition_command(
        self,
        command_factory: Callable[[BookkeepingState], BookkeepingCommand],
        desc: str,
    ) -> dict[str, Any]:
        """
        Apply an accounting assertion or invalidation through TransitionEngine.
        Fails closed: inspection state is closed immediately before mutation.
        """
        self.close_state()
        prev_p: int | None = None
        mutation_state: BookkeepingState | None = None

        try:
            snap = self.repository.load_snapshot(company_id=self.company_id)
            prev_p = snap.persistence_revision

            self.session_counter += 1
            mutation_state = self.hydrator.hydrate(
                company_id=self.company_id,
                session_id=f"workbench-mutation-{self.session_counter}",
            )
            cmd = command_factory(mutation_state)
            engine = TransitionEngine(repository=self.repository)
            t_res = engine.apply(state=mutation_state, command=cmd)
        except Exception as exc:
            # Pre-commit failure: restore inspection from unchanged persistence
            self.get_or_hydrate_state()
            return {
                "success": False,
                "applied": False,
                "error": f"Transition execution error: {exc}",
                "persistence_revision_before": prev_p,
                "persistence_revision_after": prev_p,
            }
        finally:
            if mutation_state is not None and not mutation_state.is_closed:
                mutation_state.close()

        if not t_res.applied:
            rej_code = t_res.rejection.code if t_res.rejection else "UNKNOWN"
            rej_msg = t_res.rejection.message if t_res.rejection else "Unknown rejection"
            # Pre-commit rejection: persistence revision did not advance
            self.get_or_hydrate_state()
            return {
                "success": False,
                "applied": False,
                "rejection_code": rej_code,
                "rejection_message": rej_msg,
                "error": f"Transition rejected ({rej_code}): {rej_msg}",
                "persistence_revision_before": prev_p,
                "persistence_revision_after": prev_p,
            }

        new_p = (prev_p if prev_p is not None else 0) + 1
        # Commit succeeded; attempt post-commit hydration
        try:
            self.session_counter += 1
            self.state = self.hydrator.hydrate(
                company_id=self.company_id,
                session_id=f"workbench-inspection-{self.session_counter}",
            )
            local_rev = self.state.revision
        except Exception as exc:
            # Post-commit hydration failed! Fail closed: NEVER restore or reuse stale state.
            self.state = None
            return {
                "success": False,
                "applied": True,
                "state_closed": True,
                "catastrophic": True,
                "error": f"Catastrophic error: post-commit state hydration failed: {exc}",
                "persistence_revision_before": prev_p,
                "persistence_revision_after": new_p,
                "persistence_revision": new_p,
                "local_state_revision": None,
                "message": f"Applied {desc}, but workbench inspection state is closed due to hydration error.",
            }

        return {
            "success": True,
            "applied": True,
            "persistence_revision": new_p,
            "persistence_revision_before": prev_p,
            "persistence_revision_after": new_p,
            "local_state_revision": local_rev,
            "message": f"Successfully applied {desc}.",
            "affected_artifact_ids": _extract_delta_ids(t_res.delta),
        }

    # ------------------------------------------------------------------
    # Execution: Single BookkeepingSession Invariant
    # ------------------------------------------------------------------

    def run_bookkeeping(self) -> dict[str, Any]:
        """
        Execute one complete BookkeepingSession (Routing -> DAG -> Reconciliation).
        Single runtime invariant: calls BookkeepingSession exactly once and
        aggregates detached/current inspection results around it.
        """
        self.close_state()
        self.session_counter += 1
        session_id = f"workbench-session-{self.session_counter}"

        session_state = self.hydrator.hydrate(
            company_id=self.company_id,
            session_id=session_id,
        )

        engine = TransitionEngine(repository=self.repository)
        routing_service = RoutingService(
            semantic_provider=self.routing_semantic_provider
            or ZeroRoutingSemanticScoreProvider()
        )
        dag_classifier = self.dag_classifier or SimulatedAseClassifier()
        base_reconciliation_service = (
            self.reconciliation_service or ReconciliationService()
        )

        captured_plan: ReconciliationPlan | None = None
        orig_plan = base_reconciliation_service.plan

        def intercepted_plan(*args: Any, **kwargs: Any) -> ReconciliationPlan:
            nonlocal captured_plan
            captured_plan = orig_plan(*args, **kwargs)
            return captured_plan

        class InterceptingReconciliationService(ReconciliationService):
            def plan(self, *args: Any, **kwargs: Any) -> ReconciliationPlan:
                return intercepted_plan(*args, **kwargs)

        reconciliation_service = InterceptingReconciliationService(
            candidate_generator=base_reconciliation_service.candidate_generator,
            scorer=base_reconciliation_service.scorer,
        )

        session = BookkeepingSession(
            state=session_state,
            engine=engine,
            routing_service=routing_service,
            dag_classifier=dag_classifier,
            reconciliation_service=reconciliation_service,
        )

        result = session.run()

        # Invariant: session state must be closed
        assert session_state.is_closed, "Session did not close its live state."

        self.last_recon_plan = captured_plan
        self.last_reconciliation_result = captured_plan.result if captured_plan else None
        self.last_result = WorkbenchSessionResult.from_session_result(
            result,
            reconciliation_result=self.last_reconciliation_result,
            reconciliation_plan=self.last_recon_plan,
        )

        # Hydrate fresh inspection state from resulting persistence
        self.session_counter += 1
        self.state = self.hydrator.hydrate(
            company_id=self.company_id,
            session_id=f"workbench-inspection-{self.session_counter}",
        )

        queries = BookkeepingQueries(self.state)
        unresolved_bank = [
            b.id for b in self.state.bank_items.values()
            if queries.bank_remaining_units(b.id) > 0
        ]
        unresolved_book = [
            b.id for b in self.state.book_items.values()
            if queries.book_remaining_units(b.id) > 0
        ]

        review_count = len(self.last_result.review_hypotheses)

        routing_status = (
            result.routing_stage_result.status.value
            if result.routing_stage_result
            else "NOT_RUN"
        )
        routing_applied = (
            result.routing_stage_result.command_count
            if result.routing_stage_result
            else 0
        )

        dag_status = (
            result.dag_stage_result.status.value
            if result.dag_stage_result
            else "NOT_RUN"
        )
        classified_count = (
            result.dag_stage_result.command_count
            if result.dag_stage_result
            else 0
        )
        hold_count = result.dag_stage_result.hold_count if result.dag_stage_result else 0
        holds_summary = [
            {"book_item_id": h.book_item_id, "reason": h.reason}
            for h in (result.dag_stage_result.holds if result.dag_stage_result else ())
        ]

        recon_status = (
            result.reconciliation_stage_result.status.value
            if result.reconciliation_stage_result
            else "NOT_RUN"
        )
        reconciliations_applied = (
            result.reconciliation_stage_result.command_count
            if result.reconciliation_stage_result
            else 0
        )

        provider_issue_count = (
            result.dag_stage_result.provider_issue_count
            if result.dag_stage_result
            else 0
        )

        return {
            "session_id": result.session_id,
            "is_success": result.is_success,
            "starting_persistence_revision": result.starting_persistence_revision,
            "final_persistence_revision": result.final_persistence_revision,
            "routing": {
                "status": routing_status,
                "applied_count": routing_applied,
            },
            "dag": {
                "status": dag_status,
                "classified_count": classified_count,
                "hold_count": hold_count,
                "holds": holds_summary,
            },
            "reconciliation": {
                "status": recon_status,
                "applied_count": reconciliations_applied,
            },
            "unresolved_bank_count": len(unresolved_bank),
            "unresolved_book_count": len(unresolved_book),
            "review_candidates_count": review_count,
            "provider_issues_count": provider_issue_count,
            "failure_stage": result.failure_stage.value if result.failure_stage else None,
            "failure_reason": result.failure_reason,
        }

    # ------------------------------------------------------------------
    # Read Tools (Detached / Safe Dictionaries)
    # ------------------------------------------------------------------

    def get_state(self) -> dict[str, Any]:
        """Return compact summary of active company and bookkeeping state."""
        if self.state is None or self.state.is_closed:
            pers_rev = self.repository.load_snapshot(
                company_id=self.company_id
            ).persistence_revision
            return {
                "company_id": self.company_id,
                "is_closed": True,
                "local_state_revision": "closed",
                "persistence_revision": pers_rev,
                "error": "State is closed. Use rehydrate before querying state.",
            }

        queries = BookkeepingQueries(self.state)
        unres_bank = sum(1 for b in self.state.bank_items.values() if queries.bank_remaining_units(b.id) > 0)
        unres_book = sum(1 for b in self.state.book_items.values() if queries.book_remaining_units(b.id) > 0)

        return {
            "company_id": self.company_id,
            "session_id": self.state.session_id,
            "is_closed": False,
            "local_state_revision": self.state.revision,
            "persistence_revision": self.state.persistence_revision,
            "bank_items_count": len(self.state.bank_items),
            "book_items_count": len(self.state.book_items),
            "active_routes_count": len(self.state.routing_decisions),
            "active_classifications_count": len(self.state.classifications),
            "active_reconciliations_count": len(queries.derived.active_reconciliations),
            "unresolved_bank_items_count": unres_bank,
            "unresolved_book_items_count": unres_book,
            "last_dag_holds_count": self.last_result.hold_count if self.last_result else 0,
            "last_provider_issues_count": self.last_result.provider_issue_count if self.last_result else 0,
        }

    def list_bank_items(self, status: str | None = None) -> list[dict[str, Any]]:
        """List bank items with status (RECONCILED, PARTIALLY_RECONCILED, UNRECONCILED)."""
        st = self.get_or_hydrate_state()
        if st is None:
            return []

        queries = BookkeepingQueries(st)
        results = []
        for b in sorted(st.bank_items.values(), key=lambda x: x.id):
            orig = int(b.amount_units)
            rem = queries.bank_remaining_units(b.id)
            orig_dec = solver_units_to_decimal(orig)
            rem_dec = solver_units_to_decimal(rem)
            if rem == 0:
                item_status = "RECONCILED"
            elif rem < orig:
                item_status = "PARTIALLY_RECONCILED"
            else:
                item_status = "UNRECONCILED"

            if status and status.upper() != "ALL":
                if status.upper() in ("UNRESOLVED", "OPEN") and item_status == "RECONCILED":
                    continue
                if status.upper() == "RECONCILED" and item_status != "RECONCILED":
                    continue

            results.append({
                "id": b.id,
                "bank_item_id": b.id,
                "bank_account_id": b.bank_account_id,
                "date": str(b.date),
                "original_amount": f"{orig_dec:.2f}",
                "remaining_amount": f"{rem_dec:.2f}",
                "original_amount_units": str(orig),
                "remaining_amount_units": str(rem),
                "amount_display": f"{orig_dec:,.2f} {b.currency}",
                "remaining_amount_display": f"{rem_dec:,.2f} {b.currency}",
                "currency": b.currency,
                "direction": b.direction.value,
                "description": b.description,
                "reference": b.reference,
                "status": item_status,
            })
        return results

    def list_book_items(self, status: str | None = None) -> list[dict[str, Any]]:
        """List book items with routing, classification, and status."""
        st = self.get_or_hydrate_state()
        if st is None:
            return []

        queries = BookkeepingQueries(st)
        results = []
        for b in sorted(st.book_items.values(), key=lambda x: x.id):
            orig = int(b.amount_units)
            rem = queries.book_remaining_units(b.id)
            orig_dec = solver_units_to_decimal(orig)
            rem_dec = solver_units_to_decimal(rem)
            if rem == 0:
                item_status = "RECONCILED"
            elif rem < orig:
                item_status = "PARTIALLY_RECONCILED"
            else:
                item_status = "UNRECONCILED"

            if status and status.upper() != "ALL":
                if status.upper() in ("UNRESOLVED", "OPEN") and item_status == "RECONCILED":
                    continue
                if status.upper() == "RECONCILED" and item_status != "RECONCILED":
                    continue

            route = st.routing_decisions.get(b.id)
            cls_dec = st.classifications.get(b.id)

            results.append({
                "id": b.id,
                "book_item_id": b.id,
                "origin_period": b.origin_period,
                "date": str(b.date),
                "original_amount": f"{orig_dec:.2f}",
                "remaining_amount": f"{rem_dec:.2f}",
                "original_amount_units": str(orig),
                "remaining_amount_units": str(rem),
                "amount_display": f"{orig_dec:,.2f} {b.currency}",
                "remaining_amount_display": f"{rem_dec:,.2f} {b.currency}",
                "currency": b.currency,
                "direction": b.direction.value,
                "description": b.description,
                "reference": b.reference,
                "status": item_status,
                "active_route_account_id": route.bank_account_id if route else None,
                "active_classification_account_code": cls_dec.account_code if cls_dec else None,
            })
        return results

    def get_remaining_balances(self) -> dict[str, Any]:
        """Itemized and aggregate remaining amounts for bank and book items."""
        st = self.get_or_hydrate_state()
        if st is None:
            return {"error": "State closed"}

        queries = BookkeepingQueries(st)
        bank_rem = {}
        book_rem = {}
        total_bank = 0
        total_book = 0

        for b in sorted(st.bank_items.values(), key=lambda x: x.id):
            rem = queries.bank_remaining_units(b.id)
            bank_rem[b.id] = rem
            total_bank += rem

        for b in sorted(st.book_items.values(), key=lambda x: x.id):
            rem = queries.book_remaining_units(b.id)
            book_rem[b.id] = rem
            total_book += rem

        total_bank_dec = solver_units_to_decimal(total_bank)
        total_book_dec = solver_units_to_decimal(total_book)
        base_curr = st.context.base_currency if st and st.context else "MAD"

        return {
            "total_unreconciled_bank_units": total_bank,
            "total_unreconciled_book_units": total_book,
            "total_unresolved_bank_units": total_bank,
            "total_unresolved_book_units": total_book,
            "total_unresolved_bank_amount": f"{total_bank_dec:.2f}",
            "total_unresolved_book_amount": f"{total_book_dec:.2f}",
            "total_unresolved_bank_display": f"{total_bank_dec:,.2f} {base_curr}",
            "total_unresolved_book_display": f"{total_book_dec:,.2f} {base_curr}",
            "bank_remaining_amounts": {k: f"{solver_units_to_decimal(v):.2f}" for k, v in bank_rem.items()},
            "book_remaining_amounts": {k: f"{solver_units_to_decimal(v):.2f}" for k, v in book_rem.items()},
            "bank_items": bank_rem,
            "book_items": book_rem,
            "bank_remaining": bank_rem,
            "book_remaining": book_rem,
        }

    def list_unresolved(self) -> dict[str, Any]:
        """Returns unresolved items, reasons, and review requirements."""
        st = self.get_or_hydrate_state()
        if st is None:
            return {"bank_items": [], "book_items": [], "review_required": []}

        queries = BookkeepingQueries(st)
        unres_map = {}
        if self.last_reconciliation_result:
            for ub in self.last_reconciliation_result.unresolved_bank_items:
                unres_map[ub.bank_item_id] = ub.reason

        hyps_by_bank: dict[str, list[ReconciliationHypothesis]] = {}
        if self.last_recon_plan and self.last_recon_plan.hypotheses:
            for h in self.last_recon_plan.hypotheses:
                for ba in h.bank_allocations:
                    hyps_by_bank.setdefault(ba.bank_item_id, []).append(h)

        unresolved_bank = []
        for b in sorted(st.bank_items.values(), key=lambda x: x.id):
            rem = queries.bank_remaining_units(b.id)
            if rem > 0:
                hyps = hyps_by_bank.get(b.id, [])
                orig_dec = solver_units_to_decimal(int(b.amount_units))
                rem_dec = solver_units_to_decimal(rem)
                unresolved_bank.append({
                    "bank_item_id": b.id,
                    "currency": b.currency,
                    "original_amount": f"{orig_dec:.2f}",
                    "remaining_amount": f"{rem_dec:.2f}",
                    "amount_display": f"{orig_dec:,.2f} {b.currency}",
                    "remaining_amount_display": f"{rem_dec:,.2f} {b.currency}",
                    "original_amount_units": str(int(b.amount_units)),
                    "remaining_amount_units": str(rem),
                    "description": b.description,
                    "reference": b.reference,
                    "reason": unres_map.get(b.id, "UNMATCHED"),
                    "candidate_hypotheses_count": len(hyps),
                })

        unresolved_book = []
        for b in sorted(st.book_items.values(), key=lambda x: x.id):
            rem = queries.book_remaining_units(b.id)
            if rem > 0:
                orig_dec = solver_units_to_decimal(int(b.amount_units))
                rem_dec = solver_units_to_decimal(rem)
                unresolved_book.append({
                    "book_item_id": b.id,
                    "currency": b.currency,
                    "original_amount": f"{orig_dec:.2f}",
                    "remaining_amount": f"{rem_dec:.2f}",
                    "amount_display": f"{orig_dec:,.2f} {b.currency}",
                    "remaining_amount_display": f"{rem_dec:,.2f} {b.currency}",
                    "original_amount_units": str(int(b.amount_units)),
                    "remaining_amount_units": str(rem),
                    "description": b.description,
                    "reference": b.reference,
                })

        review_required = self.list_review_candidates()

        return {
            "unreconciled_bank_items_count": len(unresolved_bank),
            "unreconciled_book_items_count": len(unresolved_book),
            "bank_items": unresolved_bank,
            "book_items": unresolved_book,
            "review_required": review_required,
        }

    def list_review_candidates(self) -> list[dict[str, Any]]:
        """Review-only hypotheses from the detached last session result."""
        review_hyps = ()
        if self.last_reconciliation_result:
            review_hyps = self.last_reconciliation_result.review_hypotheses
        elif self.last_result and hasattr(self.last_result, "review_hypotheses"):
            review_hyps = self.last_result.review_hypotheses

        results = []
        policy_flag = (
            self.state.context.policy.auto_reconcile_unique_inferred_allocation
            if self.state
            else False
        )

        for hyp in review_hyps:
            policy_reason = "Policy prevents automatic reconciliation"
            if (
                hyp.allocation_support == AllocationSupport.UNIQUE_INFERENCE
                and not policy_flag
            ):
                policy_reason = (
                    "Policy auto_reconcile_unique_inferred_allocation is False "
                    "(requires manual review for UNIQUE_INFERENCE)"
                )

            results.append({
                "hypothesis_id": hyp.id,
                "bank_allocations": [
                    {
                        "bank_item_id": a.bank_item_id,
                        "amount": f"{solver_units_to_decimal(int(a.amount_units)):.2f}",
                        "amount_units": str(a.amount_units),
                    }
                    for a in hyp.bank_allocations
                ],
                "book_allocations": [
                    {
                        "book_item_id": a.book_item_id,
                        "amount": f"{solver_units_to_decimal(int(a.amount_units)):.2f}",
                        "amount_units": str(a.amount_units),
                    }
                    for a in hyp.book_allocations
                ],
                "identity_admissibility": hyp.admissibility.value,
                "allocation_support": hyp.allocation_support.value,
                "semantic_score": hyp.utility,
                "exact_semantic_value": hyp.exact_semantic_value,
                "eligibility": hyp.eligibility.value,
                "rationale": hyp.semantic_rationale,
                "policy_reason": policy_reason,
            })
        return results

    def list_reconciliations(
        self,
        bank_item_id: str | None = None,
        book_item_id: str | None = None,
    ) -> list[dict[str, Any]]:
        """List active durable reconciliations with allocations and rationale."""
        st = self.get_or_hydrate_state()
        if st is None:
            return []

        queries = BookkeepingQueries(st)
        active_recons = queries.derived.active_reconciliations.values()

        results = []
        for r in sorted(active_recons, key=lambda x: x.id):
            if bank_item_id and not any(a.bank_item_id == bank_item_id for a in r.bank_allocations):
                continue
            if book_item_id and not any(a.book_item_id == book_item_id for a in r.book_allocations):
                continue
            results.append({
                "reconciliation_id": r.id,
                "bank_allocations": [
                    {
                        "bank_item_id": a.bank_item_id,
                        "amount": f"{solver_units_to_decimal(int(a.amount_units)):.2f}",
                        "amount_units": str(a.amount_units),
                    }
                    for a in r.bank_allocations
                ],
                "book_allocations": [
                    {
                        "book_item_id": a.book_item_id,
                        "amount": f"{solver_units_to_decimal(int(a.amount_units)):.2f}",
                        "amount_units": str(a.amount_units),
                    }
                    for a in r.book_allocations
                ],
                "semantic_score": r.source_hypothesis_utility,
                "evidence_refs": list(r.evidence_refs),
            })
        return results

    def list_routes(self, book_item_id: str | None = None) -> list[dict[str, Any]]:
        """List active durable routing decisions per book item."""
        st = self.get_or_hydrate_state()
        if st is None:
            return []

        queries = BookkeepingQueries(st)
        active_routes = queries.derived.active_routing_by_book_item.values()

        results = []
        for r in sorted(active_routes, key=lambda x: x.book_item_id):
            if book_item_id and r.book_item_id != book_item_id:
                continue
            src_val = r.source.value if hasattr(r.source, "value") else str(r.source)
            results.append({
                "book_item_id": r.book_item_id,
                "bank_account_id": r.bank_account_id,
                "utility": r.utility,
                "confidence": (r.utility / 1000.0) if r.utility is not None else 1.0,
                "source": src_val,
            })
        return results

    def list_classifications(self, book_item_id: str | None = None) -> list[dict[str, Any]]:
        """List active durable account classifications per book item."""
        st = self.get_or_hydrate_state()
        if st is None:
            return []

        queries = BookkeepingQueries(st)
        active_cls = queries.derived.active_classification_by_book_item.values()

        results = []
        for c in sorted(active_cls, key=lambda x: x.book_item_id):
            if book_item_id and c.book_item_id != book_item_id:
                continue
            results.append({
                "book_item_id": c.book_item_id,
                "account_code": c.account_code,
                "confidence": c.confidence,
                "source": c.source,
                "rationale": c.rationale,
            })
        return results

    def get_evidence(self, book_item_id: str | None = None) -> list[dict[str, Any]]:
        """List durable evidence assertions."""
        st = self.get_or_hydrate_state()
        if st is None:
            return []

        invalidated_ids = {
            inv.assertion_id
            for inv in st.book_item_evidence_invalidations.values()
        }
        results = []
        for a in sorted(st.book_item_evidence_assertions.values(), key=lambda x: x.id):
            if book_item_id and a.book_item_id != book_item_id:
                continue
            is_valid = a.id not in invalidated_ids
            results.append({
                "assertion_id": a.id,
                "evidence_id": a.id,
                "book_item_id": a.book_item_id,
                "assertion_type": a.evidence_type.value,
                "evidence_type": a.evidence_type.value,
                "value": a.value,
                "confidence": a.confidence,
                "source": a.source.value,
                "is_valid": is_valid,
            })
        return results

    def get_history(self) -> dict[str, Any]:
        """
        Return history and durable artifact counts from repository snapshot.
        Single-runtime: uses existing durable artifacts/events only.
        """
        snap = self.repository.load_snapshot(company_id=self.company_id)
        return {
            "company_id": self.company_id,
            "persistence_revision": snap.persistence_revision,
            "bank_accounts_count": len(snap.bank_accounts),
            "bank_items_count": len(snap.bank_items),
            "book_items_count": len(snap.book_items),
            "routing_decisions_count": len(snap.routing_decisions),
            "classifications_count": len(snap.classifications),
            "reconciliations_count": len(snap.reconciliations),
            "evidence_assertions_count": len(snap.book_item_evidence_assertions),
        }

    def get_policy(self) -> dict[str, Any]:
        """Return active accounting policy."""
        st = self.get_or_hydrate_state()
        if st is None:
            return {}
        p = st.context.policy
        return {
            "chart_of_accounts_id": p.chart_of_accounts_id,
            "auto_reconcile_unique_inferred_allocation": p.auto_reconcile_unique_inferred_allocation,
        }

    def get_holds(self) -> list[dict[str, Any]]:
        """Return holds from latest session run."""
        if not self.last_result:
            return []
        return [
            {
                "book_item_id": h.book_item_id,
                "reason": h.reason,
                "rationale": getattr(h, "rationale", ""),
                "ase_node_id": getattr(h, "ase_node_id", ""),
            }
            for h in getattr(self.last_result, "holds", ())
        ]

    def get_provider_issues(self) -> list[dict[str, Any]]:
        """Return provider issues from latest session run."""
        if not self.last_result:
            return []
        return [
            {
                "book_item_id": pi.book_item_id,
                "code": pi.code,
                "message": pi.message,
            }
            for pi in self.last_result.provider_issues
        ]

    # ------------------------------------------------------------------
    # Mutation Tools (Typed / Fail-Closed)
    # ------------------------------------------------------------------

    def add_bank_item(
        self,
        *,
        bank_item_id: str,
        amount: str | int | None = None,
        amount_units: str | int | None = None,
        currency: str = "USD",
        date: str | None = None,
        date_str: str | None = None,
        bank_account_id: str | None = None,
        direction: str | None = None,
        counterparty: str | None = None,
        description: str = "",
        reference: str | None = None,
        **kwargs: Any,
    ) -> dict[str, Any]:
        """Authoritatively insert an exogenous BankItem into persistence."""
        try:
            raw_amt = amount if amount is not None else amount_units
            if raw_amt is None:
                return {"success": False, "applied": False, "error": "amount is required"}
            parsed_amount = _parse_amount(str(raw_amt))
            raw_date = date_str or date or "2026-09-01"
            parsed_date = dt_date.fromisoformat(raw_date) if isinstance(raw_date, str) else raw_date
            snap = self.repository.load_snapshot(company_id=self.company_id)
            acc_id = bank_account_id or (list(snap.bank_accounts)[0].id if snap.bank_accounts else "acc-main")
            if direction:
                dir_enum = (
                    Direction(direction)
                    if direction in [d.value for d in Direction]
                    else Direction.BANK_OUTFLOW
                )
            else:
                dir_enum = Direction.BANK_OUTFLOW

            desc = description or (f"Payment from {counterparty}" if counterparty else f"Bank item {bank_item_id}")
            item = BankItem(
                id=bank_item_id,
                bank_account_id=acc_id,
                amount_units=parsed_amount,
                currency=currency.upper(),
                direction=dir_enum,
                date=parsed_date,
                description=desc,
                reference=reference or None,
            )
        except Exception as exc:
            return {"success": False, "applied": False, "error": f"Invalid bank item arguments: {exc}"}

        ws = PersistenceWriteSet(bank_items=(item,))
        return self._commit_source_writeset(ws, f"BankItem '{bank_item_id}'")

    def add_book_item(
        self,
        *,
        book_item_id: str,
        amount: str | int | None = None,
        amount_units: str | int | None = None,
        currency: str = "USD",
        date: str | None = None,
        date_str: str | None = None,
        item_type: str = "INVOICE",
        direction: str | None = None,
        origin_period: str | None = None,
        counterparty: str | None = None,
        description: str = "",
        reference: str | None = None,
        **kwargs: Any,
    ) -> dict[str, Any]:
        """Authoritatively insert an exogenous BookItem into persistence."""
        try:
            raw_amt = amount if amount is not None else amount_units
            if raw_amt is None:
                return {"success": False, "applied": False, "error": "amount is required"}
            parsed_amount = _parse_amount(str(raw_amt))
            raw_date = date_str or date or "2026-09-01"
            parsed_date = dt_date.fromisoformat(raw_date) if isinstance(raw_date, str) else raw_date
            period = origin_period or (parsed_date.strftime("%Y-%m") if hasattr(parsed_date, "strftime") else str(parsed_date)[:7])
            if direction:
                dir_enum = (
                    Direction(direction)
                    if direction in [d.value for d in Direction]
                    else Direction.BOOK_BANK_CREDIT
                )
            else:
                dir_enum = Direction.BOOK_BANK_CREDIT

            desc = description or (f"Invoice for {counterparty}" if counterparty else f"Book item {book_item_id}")
            item = BookItem(
                id=book_item_id,
                origin_period=period,
                amount_units=parsed_amount,
                currency=currency.upper(),
                direction=dir_enum,
                date=parsed_date,
                description=desc,
                reference=reference or None,
            )
        except Exception as exc:
            return {"success": False, "applied": False, "error": f"Invalid book item arguments: {exc}"}

        ws = PersistenceWriteSet(book_items=(item,))
        return self._commit_source_writeset(ws, f"BookItem '{book_item_id}'")

    def add_counterparty(
        self,
        *,
        counterparty_id: str,
        name: str,
        counterparty_type: str = "CUSTOMER",
        aliases: list[str] | None = None,
        tax_id: str | None = None,
        external_reference: str | None = None,
        **kwargs: Any,
    ) -> dict[str, Any]:
        """Authoritatively insert an exogenous Counterparty record."""
        try:
            type_str = counterparty_type.upper()
            if "CUST" in type_str:
                cp_type = CounterpartyType.CUSTOMER
            elif "SUPP" in type_str or "VEND" in type_str:
                cp_type = CounterpartyType.SUPPLIER
            elif "EMP" in type_str:
                cp_type = CounterpartyType.EMPLOYEE
            elif "BANK" in type_str:
                cp_type = CounterpartyType.BANK
            elif "TAX" in type_str:
                cp_type = CounterpartyType.TAX_AUTHORITY
            else:
                cp_type = CounterpartyType.OTHER

            cp = Counterparty(
                id=counterparty_id,
                name=name,
                counterparty_type=cp_type,
                tax_id=tax_id or None,
                external_reference=external_reference or (aliases[0] if aliases else None),
            )
        except Exception as exc:
            return {"success": False, "applied": False, "error": f"Invalid counterparty arguments: {exc}"}

        ws = PersistenceWriteSet(counterparties=(cp,))
        return self._commit_source_writeset(ws, f"Counterparty '{name}' ({counterparty_id})")

    def assert_book_item_evidence(
        self,
        *,
        book_item_id: str,
        evidence_id: str | None = None,
        assertion_id: str | None = None,
        evidence_type: str = "DOCUMENT_LINK",
        assertion_type: str | None = None,
        raw_text: str | None = None,
        value: str | None = None,
        confidence: float = 1.0,
        tags: list[str] | None = None,
        source: str = "OPERATOR",
        **kwargs: Any,
    ) -> dict[str, Any]:
        """Append a new BookItemEvidenceAssertion through TransitionEngine."""
        eff_type_str = (assertion_type or evidence_type).upper()
        if "COUNTERPARTY" in eff_type_str:
            ev_type = BookItemEvidenceType.COUNTERPARTY
        elif "REF" in eff_type_str:
            ev_type = BookItemEvidenceType.REFERENCE
        elif "DOC" in eff_type_str:
            ev_type = BookItemEvidenceType.DOCUMENT_LINK
        else:
            ev_type = BookItemEvidenceType.DESCRIPTION

        eff_value = raw_text or value or "Evidence assertion"
        target_id = evidence_id or assertion_id or f"ev-{book_item_id}-{uuid.uuid4().hex[:6]}"
        self.session_counter += 1
        clock_now = self.clock_time or datetime.now(timezone.utc)

        def make_cmd(mutation_state: BookkeepingState) -> AssertBookItemEvidenceCommand:
            valid_doc_ids = tuple(t for t in (tags or []) if mutation_state.get_document(t) is not None)
            nonlocal ev_type
            if ev_type == BookItemEvidenceType.DOCUMENT_LINK and mutation_state.get_document(eff_value) is None:
                actual_ev_type = BookItemEvidenceType.DESCRIPTION
            else:
                actual_ev_type = ev_type

            return AssertBookItemEvidenceCommand(
                command_id=f"cmd-assert-ev-{self.session_counter}",
                expected_state_revision=mutation_state.revision,
                assertion_id=target_id,
                book_item_id=book_item_id,
                evidence_type=actual_ev_type,
                value=eff_value,
                document_ids=valid_doc_ids,
                evidence_source=EvidenceSource.HUMAN_ASSERTION,
                source_document_id=None,
                supersedes_assertion_id=None,
                session_id=mutation_state.session_id,
                source=CommandSource.HUMAN,
                issued_at=clock_now,
            )

        return self._apply_transition_command(
            make_cmd, f"BookItemEvidenceAssertion '{target_id}' on '{book_item_id}'"
        )

    def invalidate_evidence(
        self,
        evidence_id: str | None = None,
        assertion_id: str | None = None,
        reason: str = "",
    ) -> dict[str, Any]:
        """Invalidate an evidence assertion through TransitionEngine command."""
        target_id = evidence_id or assertion_id
        if not target_id:
            return {"applied": False, "rejection_code": "MISSING_ID", "rejection_message": "evidence_id is required"}

        self.session_counter += 1
        clock_now = self.clock_time or datetime.now(timezone.utc)
        invalidation_id = f"inv-ev-{self.session_counter}-{uuid.uuid4().hex[:6]}"

        def make_cmd(mutation_state: BookkeepingState) -> InvalidateBookItemEvidenceCommand:
            return InvalidateBookItemEvidenceCommand(
                command_id=f"cmd-inval-ev-{self.session_counter}",
                expected_state_revision=mutation_state.revision,
                invalidation_id=invalidation_id,
                assertion_id=target_id,
                reason=reason or "Invalidated by user/operator",
                session_id=mutation_state.session_id,
                source=CommandSource.HUMAN,
                issued_at=clock_now,
            )

        return self._apply_transition_command(
            make_cmd, f"evidence invalidation for '{target_id}'"
        )

    def invalidate_reconciliation(
        self,
        reconciliation_id: str,
        reason: str = "",
    ) -> dict[str, Any]:
        """Invalidate an active reconciliation, releasing capacity."""
        if not reconciliation_id:
            return {"applied": False, "rejection_code": "MISSING_ID", "rejection_message": "reconciliation_id is required"}

        self.session_counter += 1
        clock_now = self.clock_time or datetime.now(timezone.utc)
        invalidation_id = f"inv-rec-{self.session_counter}-{uuid.uuid4().hex[:6]}"

        def make_cmd(mutation_state: BookkeepingState) -> InvalidateReconciliationCommand:
            return InvalidateReconciliationCommand(
                command_id=f"cmd-inval-rec-{self.session_counter}",
                expected_state_revision=mutation_state.revision,
                invalidation_id=invalidation_id,
                reconciliation_id=reconciliation_id,
                reason=reason or "Invalidated by user/operator",
                session_id=mutation_state.session_id,
                source=CommandSource.HUMAN,
                issued_at=clock_now,
            )

        return self._apply_transition_command(
            make_cmd, f"reconciliation invalidation for '{reconciliation_id}'"
        )

    def set_auto_reconcile_unique_inferred_allocation(
        self,
        *,
        enabled: bool,
    ) -> dict[str, Any]:
        """Update accounting policy flag auto_reconcile_unique_inferred_allocation."""
        self.close_state()
        snap = self.repository.load_snapshot(company_id=self.company_id)
        current_policy = snap.context.policy
        new_policy = current_policy.model_copy(
            update={"auto_reconcile_unique_inferred_allocation": enabled}
        )
        new_context = snap.context.model_copy(update={"policy": new_policy})
        new_snap = BookkeepingSnapshot(
            persistence_revision=snap.persistence_revision,
            context=new_context,
            bank_accounts=snap.bank_accounts,
            bank_items=snap.bank_items,
            book_items=snap.book_items,
            documents=snap.documents,
            counterparties=snap.counterparties,
            routing_decisions=snap.routing_decisions,
            routing_invalidations=snap.routing_invalidations,
            classifications=snap.classifications,
            classification_invalidations=snap.classification_invalidations,
            reconciliations=snap.reconciliations,
            reconciliation_invalidations=snap.reconciliation_invalidations,
            book_item_evidence_assertions=snap.book_item_evidence_assertions,
            book_item_evidence_invalidations=snap.book_item_evidence_invalidations,
        )
        self.repository = InMemoryBookkeepingRepository(initial_snapshots=[new_snap])
        scenario_clock = self.clock_time
        self.hydrator = BookkeepingHydrator(
            repository=self.repository,
            clock=(lambda: scenario_clock) if scenario_clock is not None else None,
        )
        self.session_counter += 1
        self.state = self.hydrator.hydrate(
            company_id=self.company_id,
            session_id=f"workbench-inspection-{self.session_counter}",
        )
        return {
            "success": True,
            "applied": True,
            "persistence_revision": self.state.persistence_revision,
            "policy": {
                "auto_reconcile_unique_inferred_allocation": enabled,
            },
            "message": f"Updated policy auto_reconcile_unique_inferred_allocation = {enabled}.",
        }

    # ------------------------------------------------------------------
    # Scenario & Challenge Loading
    # ------------------------------------------------------------------

    def load_demo(self) -> None:
        """Reset workbench to default demo repository."""
        self.close_state()
        self.repository, self.company_id = create_demo_repository()
        self._configure_services()
        self.hydrator = BookkeepingHydrator(repository=self.repository)
        self.session_counter += 1
        self.state = self.hydrator.hydrate(
            company_id=self.company_id,
            session_id=f"workbench-inspection-{self.session_counter}",
        )
        self.last_result = None
        self.last_reconciliation_result = None
        self.last_recon_plan = None
        self.active_scenario = None
        self.active_scenario_name = None
        self.active_challenge = None
        self.active_temporal_challenge = None
        self.temporal_step_index = 0
        self.temporal_step_history = []

    def load_scenario(self, scenario_or_name: str | ScenarioDefinition) -> None:
        """Seed and load a scenario from catalog."""
        if isinstance(scenario_or_name, str):
            if scenario_or_name in SCENARIOS_MAP:
                scenario = SCENARIOS_MAP[scenario_or_name]
            elif scenario_or_name in CHALLENGES_MAP:
                scenario = CHALLENGES_MAP[scenario_or_name].scenario
            elif scenario_or_name in TEMPORAL_CHALLENGES_MAP:
                scenario = TEMPORAL_CHALLENGES_MAP[scenario_or_name].initial_scenario
            else:
                raise KeyError(f"Unknown scenario or challenge: {scenario_or_name}")
            self.active_scenario_name = scenario_or_name
        else:
            scenario = scenario_or_name
            self.active_scenario_name = getattr(scenario, "scenario_id", None)

        self.repository = seed_scenario_repository(scenario)
        self.company_id = scenario.context.company_id
        if self.semantic_provider == "llm":
            self.routing_semantic_provider = create_routing_semantic_provider("llm")
            self.reconciliation_service = ReconciliationService(
                scorer=create_reconciliation_semantic_provider("llm")
            )
        else:
            self.routing_semantic_provider = scenario.routing_semantic_provider
            self.reconciliation_service = scenario.reconciliation_service
        self.dag_classifier = scenario.dag_classifier
        self.clock_time = scenario.clock_time

        scenario_clock = self.clock_time
        self.hydrator = BookkeepingHydrator(
            repository=self.repository,
            clock=(lambda: scenario_clock) if scenario_clock is not None else None,
        )
        self.close_state()
        self.session_counter += 1
        self.state = self.hydrator.hydrate(
            company_id=self.company_id,
            session_id=f"workbench-inspection-{self.session_counter}",
        )
        self.last_result = None
        self.last_reconciliation_result = None
        self.last_recon_plan = None
        self.active_scenario = scenario
        self.active_challenge = None
        self.active_temporal_challenge = None

    def load_challenge(self, challenge_id: str) -> None:
        """Seed and load an adversarial challenge."""
        if challenge_id in CHALLENGES_MAP:
            challenge = CHALLENGES_MAP[challenge_id]
            scenario = challenge.scenario
            self.load_scenario(scenario)
            self.active_challenge = challenge
        elif challenge_id in TEMPORAL_CHALLENGES_MAP:
            self.load_temporal_challenge(challenge_id)
        else:
            raise KeyError(f"Challenge '{challenge_id}' not found in challenges or temporal challenges.")

    def load_temporal_challenge(self, challenge_id: str) -> None:
        """Seed and load a temporal challenge."""
        challenge = TEMPORAL_CHALLENGES_MAP[challenge_id]
        scenario = challenge.initial_scenario
        self.load_scenario(scenario)
        self.active_temporal_challenge = challenge
        self.temporal_step_index = 0


def _parse_amount(raw_val: str) -> str:
    """Helper converting monetary amount string to solver units."""
    raw = str(raw_val).strip().replace(",", "").replace("_", "")
    if raw.isdigit() and int(raw) > 0:
        return raw
    try:
        val_int = major_units_to_solver_units(raw)
        if val_int > 0:
            return str(val_int)
    except Exception:
        pass
    raise ValueError(
        f"Invalid monetary amount: {raw_val!r}. "
        "Provide positive solver units (e.g. 50000) or decimal (e.g. 5.0)."
    )
