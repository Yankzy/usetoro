from __future__ import annotations

from datetime import date, datetime, timezone
import pytest

from bookkeeping_state_eval.dag.adapter import (
    dag_outputs_to_transition_batch,
    dag_plan_to_transition_batch,
)
from bookkeeping_state_eval.dag.adversarial import AdversarialAseClassifier
from bookkeeping_state_eval.dag.models import DagBatchPlan, DagClassificationItem, DagHoldItem
from bookkeeping_state_eval.dag.protocol import AseClassifier
from bookkeeping_state_eval.dag.simulated_ase import DEFAULT_RULES, SimulatedAseClassifier
from bookkeeping_state_eval.dag.view import build_dag_view
from bookkeeping_state_eval.domain.bank import BankAccount, BankItem
from bookkeeping_state_eval.domain.books import BookItem
from bookkeeping_state_eval.domain.classifications import ClassificationSource
from bookkeeping_state_eval.domain.commands import CommandSource
from bookkeeping_state_eval.domain.enums import Direction
from bookkeeping_state_eval.hydration.hydrator import BookkeepingHydrator
from bookkeeping_state_eval.persistence.in_memory import InMemoryBookkeepingRepository
from bookkeeping_state_eval.persistence.repository import BookkeepingSnapshot
from bookkeeping_state_eval.reconciliation.candidate_generation import (
    CandidateGenerator,
    DefaultReconciliationScorer,
)
from bookkeeping_state_eval.reconciliation.service import ReconciliationService
from bookkeeping_state_eval.routing.scorer import (
    RoutingProviderScore,
    RoutingSemanticScoreProvider,
    RoutingSemanticScoringRequest,
    RoutingSemanticScoringResponse,
)
from bookkeeping_state_eval.routing.service import RoutingService
from bookkeeping_state_eval.session.bookkeeping_session import BookkeepingSession
from bookkeeping_state_eval.session.result import (
    DagProviderIssueSummary,
    FailureStage,
    SessionStageStatus,
)
from bookkeeping_state_eval.state.queries import BookkeepingQueries
from bookkeeping_state_eval.transitions.batch import TransitionBatchError
from bookkeeping_state_eval.transitions.engine import TransitionEngine
from tests import factories


class _TrivialRoutingScorer(RoutingSemanticScoreProvider):
    def score(self, request: RoutingSemanticScoringRequest) -> RoutingSemanticScoringResponse:
        return RoutingSemanticScoringResponse(
            state_revision=request.state_revision,
            model_run_id="trivial-scorer",
            scores=tuple(
                RoutingProviderScore(
                    book_item_id=c.book_item_id,
                    bank_account_id=c.bank_account_id,
                    score=1000,
                    rationale="auto-routed",
                )
                for c in request.candidates
            ),
        )


def _setup_session_env(
    *,
    books: tuple[BookItem, ...] = (),
    bank_items: tuple[BankItem, ...] = (),
    routing_decisions: tuple = (),
    dag_classifier: AseClassifier | None = None,
):
    acc = factories.account("acc-main", currency="MAD")
    ctx = factories.context()
    snap = BookkeepingSnapshot(
        persistence_revision=1,
        context=ctx,
        bank_accounts=(acc,),
        bank_items=bank_items,
        book_items=books,
        documents=(),
        reconciliations=(),
        routing_decisions=routing_decisions,
        classifications=(),
    )
    repo = InMemoryBookkeepingRepository(initial_snapshots=[snap])

    def hydrate_state(session_id: str = "session-test"):
        hydrator = BookkeepingHydrator(
            repository=repo,
            clock=lambda: datetime(2026, 1, 31, 12, 0, 0, tzinfo=timezone.utc),
            session_id_factory=lambda: session_id,
        )
        return hydrator.hydrate(company_id=ctx.company_id, session_id=session_id)

    state = hydrate_state()
    engine = TransitionEngine(repository=repo)
    routing_service = RoutingService(semantic_provider=_TrivialRoutingScorer())
    classifier = dag_classifier or SimulatedAseClassifier()
    reconciliation_service = ReconciliationService(
        candidate_generator=CandidateGenerator(),
        scorer=DefaultReconciliationScorer(),
    )

    return repo, engine, routing_service, classifier, reconciliation_service, hydrate_state, state


class TestAseContract:
    def test_protocol_conformance(self):
        """SimulatedAseClassifier and AdversarialAseClassifier satisfy AseClassifier Protocol."""
        sim = SimulatedAseClassifier()
        adv = AdversarialAseClassifier()
        assert isinstance(sim, AseClassifier)
        assert isinstance(adv, AseClassifier)

    def test_known_payroll_rule_classifies_6171(self):
        item = factories.book_item("b1", description="Monthly salary payment Jan 2026")
        repo, _, _, classifier, _, _, state = _setup_session_env(books=(item,))
        view = build_dag_view(BookkeepingQueries(state))

        plan = classifier.classify_view(view)
        assert len(plan.items) == 1
        assert len(plan.hold_items) == 0
        assert plan.items[0].book_item_id == "b1"
        assert plan.items[0].account_code == "6171"
        assert plan.items[0].ase_node_id == "payroll_node"

    def test_known_rent_rule_classifies_6131(self):
        item = factories.book_item("b1", description="Loyer bureau Q1 2026")
        repo, _, _, classifier, _, _, state = _setup_session_env(books=(item,))
        view = build_dag_view(BookkeepingQueries(state))

        plan = classifier.classify_view(view)
        assert len(plan.items) == 1
        assert len(plan.hold_items) == 0
        assert plan.items[0].account_code == "6131"
        assert plan.items[0].ase_node_id == "rent_node"

    def test_known_customer_rule_classifies_3421(self):
        item = factories.book_item("b1", description="Client Alpha INV-100 payment")
        repo, _, _, classifier, _, _, state = _setup_session_env(books=(item,))
        view = build_dag_view(BookkeepingQueries(state))

        plan = classifier.classify_view(view)
        assert len(plan.items) == 1
        assert len(plan.hold_items) == 0
        assert plan.items[0].account_code == "3421"
        assert plan.items[0].ase_node_id == "customer_receipt_node"

    def test_unknown_text_produces_hold_no_classification(self):
        item = factories.book_item("b1", description="TRANSFER 839291")
        repo, _, _, classifier, _, _, state = _setup_session_env(books=(item,))
        view = build_dag_view(BookkeepingQueries(state))

        plan = classifier.classify_view(view)
        assert len(plan.items) == 0
        assert len(plan.hold_items) == 1
        assert plan.hold_items[0].book_item_id == "b1"
        assert plan.hold_items[0].reason == "HOLD_INSUFFICIENT_EVIDENCE"
        assert "No deterministic semantic rule matched" in plan.hold_items[0].rationale

        # DAG items is empty, adapter rejects creating an empty TransitionBatch
        with pytest.raises(TransitionBatchError, match="Cannot create TransitionBatch from empty DAG items"):
            dag_plan_to_transition_batch(plan)

    def test_direction_alone_does_not_produce_6111_or_7111(self):
        """Direction without semantic match MUST produce HOLD, never 6111 or 7111."""
        credit_item = factories.book_item(
            "b-credit",
            direction=Direction.BOOK_BANK_CREDIT,
            description="MISC INFLOW 999",
        )
        debit_item = factories.book_item(
            "b-debit",
            direction=Direction.BOOK_BANK_DEBIT,
            description="MISC OUTFLOW 888",
        )

        repo, _, _, classifier, _, _, state = _setup_session_env(books=(credit_item, debit_item))
        view = build_dag_view(BookkeepingQueries(state))

        plan = classifier.classify_view(view)
        assert len(plan.items) == 0
        assert len(plan.hold_items) == 2

        held_ids = {h.book_item_id for h in plan.hold_items}
        assert "b-credit" in held_ids
        assert "b-debit" in held_ids

    def test_hold_does_not_advance_state_revision(self):
        """A session where ASE returns HOLD for all items skips DAG stage and leaves revision unchanged."""
        item = factories.book_item("b1", description="TRANSFER 839291")
        b1 = factories.bank_item("bank-1", account_id="acc-main", amount=int(item.amount_units))

        repo, engine, routing_service, classifier, rec_service, hydrate, state = _setup_session_env(
            books=(item,), bank_items=(b1,)
        )

        session = BookkeepingSession(
            state=state,
            engine=engine,
            routing_service=routing_service,
            dag_classifier=classifier,
            reconciliation_service=rec_service,
        )
        result = session.run()

        assert result.is_success
        # Routing: S0 -> S1 (revision 1)
        assert result.routing_stage_result.status == SessionStageStatus.APPLIED
        assert result.routing_stage_result.resulting_state_revision == 1

        # DAG: HOLD emitted -> zero commands -> stage HELD at revision 1
        assert result.dag_stage_result.status == SessionStageStatus.HELD
        assert result.dag_stage_result.is_held
        assert result.dag_stage_result.base_state_revision == 1
        assert result.dag_stage_result.resulting_state_revision == 1
        assert result.hold_count == 1
        assert result.holds[0].book_item_id == "b1"
        assert result.holds[0].reason == "HOLD_INSUFFICIENT_EVIDENCE"

        # State queries show no active classification
        rehydrated = hydrate("session-check")
        q = BookkeepingQueries(rehydrated)
        assert q.active_classification("b1") is None
        assert len(q.unclassified_book_items()) == 1

    def test_mixed_batch_commits_only_valid_classification_siblings(self):
        """Mixed batch: some CLASSIFIED, some HOLD. Commits only valid classification commands."""
        b1 = factories.book_item("b1", description="Salary payment")  # 6171
        b2 = factories.book_item("b2", description="TRANSFER 839291")  # HOLD
        b3 = factories.book_item("b3", description="Fournisseur Acme bill")  # 4411

        repo, engine, rs, classifier, recs, hydrate, state = _setup_session_env(books=(b1, b2, b3))
        view = build_dag_view(BookkeepingQueries(state))

        plan = classifier.classify_view(view)
        assert len(plan.items) == 2
        assert len(plan.hold_items) == 1
        assert {it.book_item_id for it in plan.items} == {"b1", "b3"}
        assert plan.hold_items[0].book_item_id == "b2"

        batch = dag_plan_to_transition_batch(plan, view=view)
        assert len(batch.commands) == 2
        assert {cmd.book_item_id for cmd in batch.commands} == {"b1", "b3"}

        res = engine.apply_batch(state=state, batch=batch)
        assert res.is_applied

        q = BookkeepingQueries(state)
        assert q.active_classification("b1").account_code == "6171"
        assert q.active_classification("b3").account_code == "4411"
        assert q.active_classification("b2") is None
        assert [it.id for it in q.unclassified_book_items()] == ["b2"]

    def test_stale_ase_result_rejected(self):
        """Adversarial stale revision in DagBatchPlan is rejected as REJECTED and produces no mutation."""
        b1 = factories.book_item("b1", description="Salary payment")
        adv = AdversarialAseClassifier(stale_state_revision=42)

        repo, engine, rs, _, recs, hydrate, state = _setup_session_env(
            books=(b1,), dag_classifier=adv
        )
        session = BookkeepingSession(
            state=state,
            engine=engine,
            routing_service=rs,
            dag_classifier=adv,
            reconciliation_service=recs,
        )
        result = session.run()

        assert not result.is_success
        assert result.failure_stage == FailureStage.DAG
        assert result.dag_stage_result.status == SessionStageStatus.REJECTED
        assert result.dag_stage_result.is_rejected
        assert not result.dag_stage_result.is_failed
        assert "Stale DAG batch revision 42" in (result.failure_reason or "")
        assert state.is_closed
        state_check = hydrate("check-stale")
        assert state_check.persistence_revision == 1  # zero accounting mutations committed
        assert BookkeepingQueries(state_check).active_classification("b1") is None

    def test_duplicate_book_item_interpretation_rejected(self):
        """Adversarial duplicate BookItem result is rejected by adapter and marks session REJECTED."""
        b1 = factories.book_item("b1", description="Salary payment")
        adv = AdversarialAseClassifier(duplicate_item_id="b1")

        repo, engine, rs, _, recs, hydrate, state = _setup_session_env(
            books=(b1,), dag_classifier=adv
        )
        view = build_dag_view(BookkeepingQueries(state))
        plan = adv.classify_view(view)

        with pytest.raises(TransitionBatchError, match="multiple classifications for book_item 'b1'"):
            dag_plan_to_transition_batch(plan, view=view)

        session = BookkeepingSession(
            state=state,
            engine=engine,
            routing_service=rs,
            dag_classifier=adv,
            reconciliation_service=recs,
        )
        result = session.run()
        assert not result.is_success
        assert result.failure_stage == FailureStage.DAG
        assert result.dag_stage_result.status == SessionStageStatus.REJECTED
        assert result.dag_stage_result.is_rejected
        assert not result.dag_stage_result.is_failed

    def test_unknown_book_item_interpretation_rejected(self):
        """Adversarial unknown BookItem ID not present in DagView is rejected."""
        b1 = factories.book_item("b1", description="Salary payment")
        adv = AdversarialAseClassifier(unknown_item_id="ghost-item")

        repo, engine, rs, _, recs, hydrate, state = _setup_session_env(
            books=(b1,), dag_classifier=adv
        )
        view = build_dag_view(BookkeepingQueries(state))
        plan = adv.classify_view(view)

        with pytest.raises(TransitionBatchError, match="unknown book_item_id 'ghost-item' not in DagView"):
            dag_plan_to_transition_batch(plan, view=view)

    def test_empty_account_code_rejected(self):
        """Malformed/empty account code is rejected by adapter and marks session REJECTED."""
        b1 = factories.book_item("b1", description="Salary payment")
        adv = AdversarialAseClassifier(malformed_account_codes={"b1": "  "})

        repo, engine, rs, _, recs, hydrate, state = _setup_session_env(
            books=(b1,), dag_classifier=adv
        )
        view = build_dag_view(BookkeepingQueries(state))
        plan = adv.classify_view(view)

        with pytest.raises(TransitionBatchError, match="invalid or empty account_code"):
            dag_plan_to_transition_batch(plan, view=view)

        session = BookkeepingSession(
            state=state,
            engine=engine,
            routing_service=rs,
            dag_classifier=adv,
            reconciliation_service=recs,
        )
        result = session.run()
        assert not result.is_success
        assert result.failure_stage == FailureStage.DAG
        assert result.dag_stage_result.status == SessionStageStatus.REJECTED
        assert result.dag_stage_result.is_rejected
        assert not result.dag_stage_result.is_failed

    def test_provider_failure_creates_no_accounting_truth(self):
        """Provider crash produces explicit FAILED status (not REJECTED) with no state mutation."""
        b1 = factories.book_item("b1", description="Salary payment")
        adv = AdversarialAseClassifier(raise_exception=ConnectionError("ASE NATS cluster down"))

        repo, engine, rs, _, recs, hydrate, state = _setup_session_env(
            books=(b1,), dag_classifier=adv
        )
        session = BookkeepingSession(
            state=state,
            engine=engine,
            routing_service=rs,
            dag_classifier=adv,
            reconciliation_service=recs,
        )
        result = session.run()

        assert not result.is_success
        assert result.failure_stage == FailureStage.DAG
        assert result.dag_stage_result.status == SessionStageStatus.FAILED
        assert result.dag_stage_result.is_failed
        assert not result.dag_stage_result.is_rejected
        assert "ASE NATS cluster down" in (result.failure_reason or "")
        assert state.is_closed
        state_check = hydrate("check-failure")
        assert BookkeepingQueries(state_check).active_classification("b1") is None

    def test_second_session_reconsiders_previously_hold_book_item(self):
        """BookItem previously in HOLD can be classified in a subsequent session once semantic evidence is provided."""
        b1_initial = factories.book_item("b1", description="TRANSFER 839291")
        bank1 = factories.bank_item("bank-1", account_id="acc-main", amount=int(b1_initial.amount_units))

        repo, engine, rs, classifier, recs, hydrate, state1 = _setup_session_env(
            books=(b1_initial,), bank_items=(bank1,)
        )

        # Session 1: b1 has no semantic evidence -> HOLD
        session1 = BookkeepingSession(
            state=state1,
            engine=engine,
            routing_service=rs,
            dag_classifier=classifier,
            reconciliation_service=recs,
        )
        result1 = session1.run()
        assert result1.is_success
        assert result1.dag_stage_result.status == SessionStageStatus.HELD
        assert result1.dag_stage_result.is_held
        assert result1.hold_count == 1

        # Verify b1 is unclassified in durable persistence
        state_between = hydrate("session-between")
        assert BookkeepingQueries(state_between).active_classification("b1") is None

        # Now simulate arrival of semantic evidence (e.g. updated rules recognizing transfer reference)
        enriched_classifier = SimulatedAseClassifier(
            rules=DEFAULT_RULES + (
                (r"(?i)\b839291\b", "6181", 0.95, "matched_reference_node"),
            )
        )

        state2 = hydrate("session-2")
        session2 = BookkeepingSession(
            state=state2,
            engine=engine,
            routing_service=rs,
            dag_classifier=enriched_classifier,
            reconciliation_service=recs,
        )
        result2 = session2.run()
        assert result2.is_success
        # In session 2, routing is skipped (already routed in session 1), DAG applies 6181
        assert result2.dag_stage_result.status == SessionStageStatus.APPLIED
        assert result2.dag_stage_result.command_count == 1

        state_final = hydrate("session-final")
        q_final = BookkeepingQueries(state_final)
        cls = q_final.active_classification("b1")
        assert cls is not None
        assert cls.account_code == "6181"
        assert cls.ase_node_id == "matched_reference_node"

    def test_hold_state_is_detached_and_runtime_disposable(self):
        """DagHoldItem is detached runtime data; it does not alter domain state models or serialization."""
        hold = DagHoldItem(
            book_item_id="b1",
            reason="HOLD_INSUFFICIENT_EVIDENCE",
            rationale="No matched pattern",
            ase_node_id="hold_node",
        )
        plan = DagBatchPlan(
            plan_id="plan-1",
            session_id="session-1",
            expected_state_revision=0,
            items=(),
            hold_items=(hold,),
        )
        assert len(plan.items) == 0
        assert len(plan.hold_items) == 1
        with pytest.raises(TransitionBatchError, match="Cannot create TransitionBatch from empty DAG items"):
            dag_plan_to_transition_batch(plan)

    def test_neutral_factory_book_item_does_not_accidentally_classify(self):
        """Factory default description is None (semantically neutral) and must produce HOLD."""
        item = factories.book_item("b-neutral")
        assert item.description is None

        repo, _, _, classifier, _, _, state = _setup_session_env(books=(item,))
        view = build_dag_view(BookkeepingQueries(state))

        plan = classifier.classify_view(view)
        assert len(plan.items) == 0
        assert len(plan.hold_items) == 1
        assert plan.hold_items[0].book_item_id == "b-neutral"
        assert plan.hold_items[0].reason == "HOLD_INSUFFICIENT_EVIDENCE"

    def test_no_eligible_dag_work_produces_skipped(self):
        """When no eligible unclassified BookItems exist, DAG stage is explicitly SKIPPED with 0 holds."""
        b1 = factories.bank_item("bank-1", account_id="acc-main", amount=1000)
        repo, engine, routing_service, classifier, rec_service, hydrate, state = _setup_session_env(
            books=(), bank_items=(b1,)
        )

        session = BookkeepingSession(
            state=state,
            engine=engine,
            routing_service=routing_service,
            dag_classifier=classifier,
            reconciliation_service=rec_service,
        )
        result = session.run()

        assert result.is_success
        assert result.dag_stage_result.status == SessionStageStatus.SKIPPED
        assert result.dag_stage_result.is_skipped
        assert result.dag_stage_result.hold_count == 0
        assert result.hold_count == 0
        assert result.holds == ()

    def test_all_eligible_items_hold_produces_held_without_revision_advance(self):
        """When all eligible items are HOLD, stage is HELD and neither local nor persistence revision advances."""
        item1 = factories.book_item("b1", description="UNKNOWN OUTFLOW 123")
        item2 = factories.book_item("b2", description="TRANSFER 987654")
        r1 = factories.routing("r1", book_item_id="b1", account_id="acc-main")
        r2 = factories.routing("r2", book_item_id="b2", account_id="acc-main")

        repo, engine, routing_service, classifier, rec_service, hydrate, state = _setup_session_env(
            books=(item1, item2), routing_decisions=(r1, r2)
        )

        session = BookkeepingSession(
            state=state,
            engine=engine,
            routing_service=routing_service,
            dag_classifier=classifier,
            reconciliation_service=rec_service,
        )
        result = session.run()
        assert result.is_success
        assert result.dag_stage_result.status == SessionStageStatus.HELD
        assert result.dag_stage_result.is_held
        # Local state revision does not advance during DAG stage:
        assert result.dag_stage_result.base_state_revision == 0
        assert result.dag_stage_result.resulting_state_revision == 0
        assert result.dag_stage_result.batch_summary is None
        assert result.hold_count == 2
        # Neither local nor persistence revision advanced at all:
        assert result.final_local_state_revision == 0
        assert result.starting_persistence_revision == 1
        assert result.final_persistence_revision == 1

    def test_mixed_classified_and_hold_produces_applied_with_detached_hold_summaries(self):
        """Mixed classified + hold items commit valid classifications, set stage APPLIED, and retain detached hold summaries."""
        b_class = factories.book_item("b-class", description="Loyer bureau mars 2026")  # classifies to 6131
        b_hold = factories.book_item("b-hold", description="NONDESCRIPT ENTRY 404")  # hold
        bank = factories.bank_item("bank-1", account_id="acc-main", amount=int(b_class.amount_units))

        repo, engine, routing_service, classifier, rec_service, hydrate, state = _setup_session_env(
            books=(b_class, b_hold), bank_items=(bank,)
        )

        session = BookkeepingSession(
            state=state,
            engine=engine,
            routing_service=routing_service,
            dag_classifier=classifier,
            reconciliation_service=rec_service,
        )
        result = session.run()

        assert result.is_success
        assert result.dag_stage_result.status == SessionStageStatus.APPLIED
        assert result.dag_stage_result.is_applied
        assert result.dag_stage_result.command_count == 1
        assert result.hold_count == 1
        assert result.holds[0].book_item_id == "b-hold"
        assert result.holds[0].reason == "HOLD_INSUFFICIENT_EVIDENCE"

        # Durable state has active classification for b-class, but none for b-hold
        state_final = hydrate("check-mixed")
        q = BookkeepingQueries(state_final)
        assert q.active_classification("b-class") is not None
        assert q.active_classification("b-class").account_code == "6131"
        assert q.active_classification("b-hold") is None
        assert [it.id for it in q.unclassified_book_items()] == ["b-hold"]

    def test_hold_summaries_survive_closed_state_but_do_not_persist(self):
        """Hold summaries remain accessible on SessionResult after state is closed, but do not exist in rehydrated state."""
        b_hold = factories.book_item("b-hold", description="WIRE TRANSFER 001")
        repo, engine, rs, classifier, recs, hydrate, state = _setup_session_env(books=(b_hold,))

        session = BookkeepingSession(
            state=state,
            engine=engine,
            routing_service=rs,
            dag_classifier=classifier,
            reconciliation_service=recs,
        )
        result = session.run()

        # State is closed after session.run()
        assert state.is_closed

        # Result holds survive cleanly and are completely detached
        assert result.hold_count == 1
        hold = result.holds[0]
        assert hold.book_item_id == "b-hold"
        assert hold.reason == "HOLD_INSUFFICIENT_EVIDENCE"
        # Confirm DagHoldSummary does not retain complex state/provider objects
        assert not hasattr(hold, "state")
        assert not hasattr(hold, "view")
        assert not hasattr(hold, "plan")
        assert not hasattr(hold, "provider")

        # In durable rehydrated state, no hold artifact or classification exists
        fresh_state = hydrate("fresh-after-hold")
        q = BookkeepingQueries(fresh_state)
        assert q.active_classification("b-hold") is None
        assert len(fresh_state.classifications) == 0

    def test_missing_provider_output_produces_incomplete_not_held(self):
        """When ASE drops an item without valid semantic HOLD or classification, status is INCOMPLETE, not HELD."""
        b1 = factories.book_item("b1", description="Client Alpha INV-100")

        class DroppingClassifier(AseClassifier):
            def classify_view(self, view):
                return DagBatchPlan(
                    plan_id="plan-drop",
                    session_id="session-test",
                    expected_state_revision=view.state_revision,
                    items=(),
                    hold_items=(),
                )

        repo, engine, rs, _, recs, hydrate, state = _setup_session_env(
            books=(b1,), dag_classifier=DroppingClassifier()
        )
        session = BookkeepingSession(
            state=state,
            engine=engine,
            routing_service=rs,
            dag_classifier=DroppingClassifier(),
            reconciliation_service=recs,
        )
        result = session.run()

        assert result.is_success
        assert result.dag_stage_result.status == SessionStageStatus.INCOMPLETE
        assert result.dag_stage_result.is_incomplete
        assert not result.dag_stage_result.is_held
        assert not result.dag_stage_result.is_failed
        assert not result.dag_stage_result.is_rejected

        assert result.hold_count == 0
        assert result.holds == ()
        assert result.provider_issue_count == 1
        assert result.is_provider_incomplete
        issue = result.provider_issues[0]
        assert issue.book_item_id == "b1"
        assert issue.code == "MISSING_PROVIDER_OUTPUT"
        assert "No semantic determination returned" in issue.message

    def test_mixed_classified_and_missing_output_produces_applied_with_provider_incomplete_metadata(self):
        """Mixed classified + missing output commits valid classifications, sets APPLIED, and retains provider issues."""
        b1 = factories.book_item("b1", description="Client Alpha INV-100")
        b2 = factories.book_item("b2", description="Forgotten item")

        class IncompleteClassifier(AseClassifier):
            def classify_view(self, view):
                return DagBatchPlan(
                    plan_id="plan-inc",
                    session_id="session-test",
                    expected_state_revision=view.state_revision,
                    items=(
                        DagClassificationItem(
                            book_item_id="b1",
                            account_code="3421",
                            confidence=0.95,
                            ase_node_id="customer_node",
                        ),
                    ),
                    hold_items=(),
                )

        incomplete = IncompleteClassifier()
        repo, engine, rs, _, recs, hydrate, state = _setup_session_env(
            books=(b1, b2), dag_classifier=incomplete
        )
        session = BookkeepingSession(
            state=state,
            engine=engine,
            routing_service=rs,
            dag_classifier=incomplete,
            reconciliation_service=recs,
        )
        result = session.run()

        assert result.is_success
        assert result.dag_stage_result.status == SessionStageStatus.APPLIED
        assert result.dag_stage_result.is_applied
        assert result.dag_stage_result.command_count == 1
        assert result.hold_count == 0
        assert result.holds == ()
        assert result.provider_issue_count == 1
        assert result.is_provider_incomplete
        issue = result.provider_issues[0]
        assert issue.book_item_id == "b2"
        assert issue.code == "MISSING_PROVIDER_OUTPUT"

        # Durable state has active classification for b1, but b2 remains unclassified
        state_check = hydrate("check-missing")
        q = BookkeepingQueries(state_check)
        assert q.active_classification("b1") is not None
        assert q.active_classification("b1").account_code == "3421"
        assert q.active_classification("b2") is None
        assert [it.id for it in q.unclassified_book_items()] == ["b2"]

    def test_second_session_can_retry_both_held_and_missing_output_items(self):
        """A subsequent session can retry and classify items that were previously HELD or had MISSING_PROVIDER_OUTPUT."""
        b_hold = factories.book_item("b-hold", description="TRANSFER 839291")
        b_missing = factories.book_item("b-missing", description="UNKNOWN 999")

        # Session 1: b_hold is evaluated to HOLD, b_missing is omitted
        class PartialClassifier(AseClassifier):
            def classify_view(self, view):
                return DagBatchPlan(
                    plan_id="plan-p1",
                    session_id="session-1",
                    expected_state_revision=view.state_revision,
                    items=(),
                    hold_items=(
                        DagHoldItem(
                            book_item_id="b-hold",
                            reason="HOLD_INSUFFICIENT_EVIDENCE",
                            rationale="No matched pattern",
                            ase_node_id="hold_node",
                        ),
                    ),
                )

        repo, engine, rs, _, recs, hydrate, state1 = _setup_session_env(
            books=(b_hold, b_missing), dag_classifier=PartialClassifier()
        )
        session1 = BookkeepingSession(
            state=state1,
            engine=engine,
            routing_service=rs,
            dag_classifier=PartialClassifier(),
            reconciliation_service=recs,
        )
        result1 = session1.run()
        assert result1.is_success
        assert result1.dag_stage_result.status == SessionStageStatus.INCOMPLETE
        assert result1.hold_count == 1
        assert result1.provider_issue_count == 1

        # Both remain unclassified in durable state
        state_between = hydrate("between-sessions")
        q_between = BookkeepingQueries(state_between)
        assert q_between.active_classification("b-hold") is None
        assert q_between.active_classification("b-missing") is None
        assert set(q_between.unclassified_book_items()) == {b_hold, b_missing}

        # Session 2: enriched classifier classifies both items
        class CompleteClassifier(AseClassifier):
            def classify_view(self, view):
                return DagBatchPlan(
                    plan_id="plan-p2",
                    session_id="session-2",
                    expected_state_revision=view.state_revision,
                    items=(
                        DagClassificationItem(
                            book_item_id="b-hold",
                            account_code="6181",
                            confidence=0.9,
                            ase_node_id="transfer_node",
                        ),
                        DagClassificationItem(
                            book_item_id="b-missing",
                            account_code="6131",
                            confidence=0.9,
                            ase_node_id="rent_node",
                        ),
                    ),
                    hold_items=(),
                )

        state2 = hydrate("session-2")
        session2 = BookkeepingSession(
            state=state2,
            engine=engine,
            routing_service=rs,
            dag_classifier=CompleteClassifier(),
            reconciliation_service=recs,
        )
        result2 = session2.run()
        assert result2.is_success
        assert result2.dag_stage_result.status == SessionStageStatus.APPLIED
        assert result2.dag_stage_result.command_count == 2
        assert result2.hold_count == 0
        assert result2.provider_issue_count == 0
        assert not result2.is_provider_incomplete

        state_final = hydrate("final-session")
        q_final = BookkeepingQueries(state_final)
        assert q_final.active_classification("b-hold").account_code == "6181"
        assert q_final.active_classification("b-missing").account_code == "6131"
        assert len(q_final.unclassified_book_items()) == 0

    def test_neither_hold_nor_provider_issues_survive_as_durable_accounting_artifacts(self):
        """Neither DagHoldSummary nor DagProviderIssueSummary pollute durable state after rehydrate."""
        b_hold = factories.book_item("b-hold", description="TRANSFER 111")
        b_missing = factories.book_item("b-missing", description="UNKNOWN 222")

        class MixedIssueClassifier(AseClassifier):
            def classify_view(self, view):
                return DagBatchPlan(
                    plan_id="plan-issue",
                    session_id="session-1",
                    expected_state_revision=view.state_revision,
                    items=(),
                    hold_items=(
                        DagHoldItem(
                            book_item_id="b-hold",
                            reason="HOLD_INSUFFICIENT_EVIDENCE",
                            rationale="Needs verification",
                            ase_node_id="hold_node",
                        ),
                    ),
                )

        repo, engine, rs, _, recs, hydrate, state = _setup_session_env(
            books=(b_hold, b_missing), dag_classifier=MixedIssueClassifier()
        )
        session = BookkeepingSession(
            state=state,
            engine=engine,
            routing_service=rs,
            dag_classifier=MixedIssueClassifier(),
            reconciliation_service=recs,
        )
        result = session.run()

        assert state.is_closed
        # Detached summaries exist on SessionResult
        assert result.hold_count == 1
        assert result.provider_issue_count == 1

        # Rehydrate completely fresh state
        fresh_state = hydrate("fresh-check")
        assert len(fresh_state.classifications) == 0
        q = BookkeepingQueries(fresh_state)
        assert q.active_classification("b-hold") is None
        assert q.active_classification("b-missing") is None
        assert not hasattr(fresh_state, "holds")
        assert not hasattr(fresh_state, "provider_issues")
