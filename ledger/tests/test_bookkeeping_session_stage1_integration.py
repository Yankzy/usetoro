from __future__ import annotations

from dataclasses import replace
from datetime import date, datetime, timezone
from decimal import Decimal
from typing import Any
from unittest.mock import MagicMock, patch
import uuid

from django.test import TestCase

from bookkeeping_state.dag.models import (
    DagBatchPlan,
    DagClassificationItem,
    DagHoldItem,
)
from bookkeeping_state.dag.view import DagView
from bookkeeping_state.domain.commands import (
    ApplyPaymentCommand,
    CommandSource,
    CreateRoutingDecisionCommand,
    RoutingDecisionSource,
    Stage1ExecutionCapability,
    mint_stage1_capability,
    verify_stage1_capability,
)
from bookkeeping_state.domain.enums import Direction
from bookkeeping_state.hydration.hydrator import BookkeepingHydrator
from bookkeeping_state.payment_application.coordinator import (
    plan_two_stage_bookkeeping,
)
from bookkeeping_state.payment_application.models import (
    PaymentApplicationPlan,
    PaymentPostingIntent,
)
from bookkeeping_state.payment_application.service import PaymentApplicationService
from bookkeeping_state.persistence.repository import BookkeepingRepository
from bookkeeping_state.reconciliation.service import ReconciliationService
from bookkeeping_state.routing.scorer import ZeroRoutingSemanticScoreProvider
from bookkeeping_state.routing.service import RoutingService
from bookkeeping_state.session.bookkeeping_session import BookkeepingSession
from bookkeeping_state.session.result import (
    FailureStage,
    SessionStageStatus,
)
from bookkeeping_state.state.fingerprint import state_fingerprint
from bookkeeping_state.state.queries import BookkeepingQueries
from bookkeeping_state.transitions.engine import TransitionEngine
from bookkeeping_state.transitions.result import (
    RejectionCode,
    TransitionRejection,
    TransitionResult,
    TransitionStatus,
)
from ledger.models.bookkeeping import BookkeepingPaymentApplication
from ledger.models.invoice import InvoiceModel
from ledger.tests.test_stage1_accounting_execution import (
    Stage1AccountingExecutionTestBase,
)


class StubAseClassifier:
    """Deterministic in-memory ASE classifier for test sessions."""

    def __init__(self, default_account_code: str = "4010") -> None:
        self.default_account_code = default_account_code

    def classify_view(
        self,
        view: DagView,
        *,
        only_unclassified: bool = True,
    ) -> DagBatchPlan:
        items = tuple(
            DagClassificationItem(
                book_item_id=v.book_item_id,
                account_code=self.default_account_code,
                confidence=1.0,
                rationale="Classified by StubAseClassifier",
            )
            for v in view.items
            if not (only_unclassified and v.is_classified)
        )
        return DagBatchPlan(
            plan_id=f"plan-{uuid.uuid4().hex[:6]}",
            session_id=view.session_id,
            expected_state_revision=view.state_revision,
            items=items,
            hold_items=(),
            dag_run_id=f"dag-run-{uuid.uuid4().hex[:6]}",
        )


from bookkeeping_state.bank_categorization.transport_models import (
    BankCategorizeResponseEnvelope,
    BankOutcomePayload,
)
from bookkeeping_state.bank_categorization.view import (
    ResidualBankCategorizationView,
)


class StubResidualBankCategorizer:
    """Deterministic in-memory residual bank categorizer for test sessions."""

    def __init__(self, default_status: str = "HOLD") -> None:
        self.default_status = default_status

    def categorize_view(
        self,
        view: ResidualBankCategorizationView,
    ) -> BankCategorizeResponseEnvelope:
        outcomes = tuple(
            BankOutcomePayload(
                bank_item_id=item.bank_item_id,
                status=self.default_status,
                account_code=None,
                confidence=1.0,
                rationale="Classified by StubResidualBankCategorizer",
                required_evidence=(),
                hold_reason="Test hold",
            )
            for item in view.items
        )
        return BankCategorizeResponseEnvelope(
            schema_version="bookkeeping.ase.bank_categorize.v1",
            request_id=f"req-{uuid.uuid4().hex[:6]}",
            idempotency_key=f"idem-{uuid.uuid4().hex[:6]}",
            session_id=view.session_id,
            state_revision=view.state_revision,
            dag_id="bank-cash-accounting-dag",
            status="COMPLETED",
            outcomes=outcomes,
        )


class BookkeepingSessionStage1IntegrationTests(Stage1AccountingExecutionTestBase):
    """
    Production BookkeepingSession integration test suite verifying the frozen
    Two-Stage accounting lifecycle:
        Routing -> DAG -> Bounded Rehydrate/Replan Stage 1 -> Stage 2 -> Validation
    """

    def setUp(self) -> None:
        super().setUp()
        self.routing_service = RoutingService(
            semantic_provider=ZeroRoutingSemanticScoreProvider()
        )
        self.dag_classifier = StubAseClassifier(
            default_account_code=self.revenue_account.code
        )
        self.residual_bank_categorizer = StubResidualBankCategorizer()
        self.reconciliation_service = ReconciliationService()
        self.payment_application_service = PaymentApplicationService()

    def _build_session(
        self,
        session_id: str,
        *,
        engine: Any = None,
        hydrator: Any = None,
    ) -> tuple[BookkeepingSession, Any]:
        h = hydrator or self.hydrator
        e = engine or self.engine
        state = h.hydrate(
            company_id=str(self.entity.uuid),
            session_id=session_id,
        )
        session = BookkeepingSession(
            state=state,
            engine=e,
            routing_service=self.routing_service,
            dag_classifier=self.dag_classifier,
            reconciliation_service=self.reconciliation_service,
            payment_application_service=self.payment_application_service,
            residual_bank_categorizer=self.residual_bank_categorizer,
            hydrator=h,
        )
        return session, state

    # ==================================================================
    # Test A: No Stage-1 needed
    # ==================================================================
    def test_scenario_a_no_stage1_needed_posted_authority_wins(self) -> None:
        """
        Scenario A:
        - Posted cash movement already exists in DB, matching bank movement.
        - Open invoice also exists in DB.
        - Stage-2 posted authority claims bank movement.
        - Stage 1 is skipped (0 payments executed, 0 rehydrations).
        - Stage 2 reconciles bank movement to posted cash leg.
        """
        stx = self._create_staged_movement(amount=Decimal("500.00"), dt=date(2026, 4, 5))
        stx.fit_id = "REF-POSTED-A"
        stx.save()

        tx = self._create_cash_tx(amount=Decimal("500.00"), is_debit=True, dt=date(2026, 4, 5))
        tx.journal_entry.je_number = "REF-POSTED-A"
        tx.journal_entry.save(update_fields=["je_number"])

        inv = self._create_approved_invoice(amount=Decimal("500.00"), dt=date(2026, 4, 1))

        session, state = self._build_session("session-test-a")
        res = session.run()

        self.assertTrue(res.is_success)
        self.assertIsNone(res.failure_stage)
        self.assertTrue(state.is_closed)

        # Stage 1 was skipped because posted authority claimed the bank item
        self.assertIsNotNone(res.payment_application_stage_result)
        self.assertEqual(res.payment_application_stage_result.status, SessionStageStatus.SKIPPED)
        self.assertEqual(len(res.executed_payment_application_ids), 0)
        self.assertEqual(res.rehydration_count, 0)

        # Stage 2 applied reconciliation
        self.assertIsNotNone(res.reconciliation_stage_result)
        self.assertEqual(res.reconciliation_stage_result.status, SessionStageStatus.APPLIED)

        # Invoice remains unpaid because bank movement was claimed by posted cash tx
        inv.refresh_from_db()
        self.assertEqual(inv.amount_due, Decimal("500.00"))

    # ==================================================================
    # Test B: One bank-before-posting payment
    # ==================================================================
    def test_scenario_b_one_bank_before_posting_payment(self) -> None:
        """
        Scenario B:
        - Bank inflow $500.00 + approved invoice $500.00.
        - No posted cash tx exists.
        - Stage 1 executes once, commits cash leg, advances revision, and rehydrates.
        - Stage 2 fresh pass reconciles bank item to the newly posted cash leg.
        - Final bank and invoice capacity are both 0.
        """
        stx = self._create_staged_movement(amount=Decimal("500.00"), dt=date(2026, 4, 5))
        inv = self._create_approved_invoice(amount=Decimal("500.00"), dt=date(2026, 4, 1))

        session, state = self._build_session("session-test-b")
        res = session.run()

        self.assertTrue(res.is_success)
        self.assertIsNone(res.failure_stage)
        self.assertTrue(state.is_closed)

        # Stage 1 executed exactly once and rehydrated
        self.assertEqual(len(res.executed_payment_application_ids), 1)
        self.assertEqual(res.rehydration_count, 1)
        self.assertIsNotNone(res.payment_application_stage_result)
        self.assertEqual(res.payment_application_stage_result.status, SessionStageStatus.APPLIED)

        # Stage 2 reconciled the fresh cash leg
        self.assertIsNotNone(res.reconciliation_stage_result)
        self.assertEqual(res.reconciliation_stage_result.status, SessionStageStatus.APPLIED)

        # Verify DB truth: invoice is fully paid
        inv.refresh_from_db()
        self.assertEqual(inv.amount_due - inv.amount_paid, Decimal("0.00"))
        self.assertEqual(inv.amount_paid, Decimal("500.00"))

        # Rehydrate and verify remaining capacity in state is 0
        fresh_state = self.hydrator.hydrate(company_id=str(self.entity.uuid), session_id="verify-b")
        q = BookkeepingQueries(fresh_state)
        self.assertEqual(q.bank_remaining_units(f"staged:{stx.uuid}"), 0)
        self.assertIsNone(fresh_state.get_book_item(f"invoice:{inv.uuid}"))

    # ==================================================================
    # Test C: Two independent bank-before-posting payments
    # ==================================================================
    def test_scenario_c_two_independent_payments_rehydrate_replan(self) -> None:
        """
        Scenario C:
        - 2 bank movements and 2 approved invoices.
        - Cycle 1 executes first payment -> rehydrates -> revision advances.
        - Cycle 2 executes second payment -> rehydrates -> revision advances.
        - Cycle 3 finds 0 proposals -> breaks to Stage 2.
        - Stage 2 reconciles both posted cash legs.
        """
        stx1 = self._create_staged_movement(amount=Decimal("300.00"), dt=date(2026, 4, 2))
        inv1 = self._create_approved_invoice(amount=Decimal("300.00"), dt=date(2026, 4, 1))

        stx2 = self._create_staged_movement(amount=Decimal("400.00"), dt=date(2026, 4, 4))
        inv2 = self._create_approved_invoice(amount=Decimal("400.00"), dt=date(2026, 4, 3))

        session, state = self._build_session("session-test-c")
        res = session.run()

        self.assertTrue(res.is_success)
        self.assertIsNone(res.failure_stage)
        self.assertTrue(state.is_closed)

        # 2 distinct Stage-1 payments executed across 2 rehydrations
        self.assertEqual(len(res.executed_payment_application_ids), 2)
        self.assertEqual(res.rehydration_count, 2)
        self.assertEqual(res.payment_application_stage_result.status, SessionStageStatus.APPLIED)

        # Both invoices paid in DB
        inv1.refresh_from_db()
        inv2.refresh_from_db()
        self.assertEqual(inv1.amount_due - inv1.amount_paid, Decimal("0.00"))
        self.assertEqual(inv2.amount_due - inv2.amount_paid, Decimal("0.00"))
        self.assertEqual(inv1.amount_paid, Decimal("300.00"))
        self.assertEqual(inv2.amount_paid, Decimal("400.00"))

        # Reconciliations committed
        self.assertEqual(res.reconciliation_stage_result.status, SessionStageStatus.APPLIED)

    # ==================================================================
    # Test D: Partial obligation settlement
    # ==================================================================
    def test_scenario_d_partial_obligation_settlement(self) -> None:
        """
        Scenario D:
        - Bank movement $400.00, Invoice $1000.00.
        - Stage 1 executes $400.00 payment application.
        - Residual on invoice is $600.00.
        - Stage 2 reconciles the $400 cash leg.
        """
        stx = self._create_staged_movement(amount=Decimal("400.00"), dt=date(2026, 4, 5))
        inv = self._create_approved_invoice(amount=Decimal("1000.00"), dt=date(2026, 4, 1))

        session, state = self._build_session("session-test-d")
        res = session.run()

        self.assertTrue(res.is_success)
        self.assertEqual(len(res.executed_payment_application_ids), 1)
        self.assertEqual(res.rehydration_count, 1)

        # DB invoice has $600.00 residual
        inv.refresh_from_db()
        self.assertEqual(inv.amount_due - inv.amount_paid, Decimal("600.00"))

        # Stage 2 reconciled bank item completely
        fresh_state = self.hydrator.hydrate(company_id=str(self.entity.uuid), session_id="verify-d")
        q = BookkeepingQueries(fresh_state)
        self.assertEqual(q.bank_remaining_units(f"staged:{stx.uuid}"), 0)
        self.assertEqual(q.book_remaining_units(f"invoice:{inv.uuid}"), 600_0000)

    # ==================================================================
    # Test E: Multi-obligation payment
    # ==================================================================
    def test_scenario_e_multi_obligation_payment(self) -> None:
        """
        Scenario E:
        - One bank movement of $1000.00 pays two invoices: $600.00 and $400.00.
        - Exactly one Stage-1 payment application executed.
        - Both invoices cleared.
        - Stage 2 reconciles.
        """
        stx = self._create_staged_movement(amount=Decimal("1000.00"), dt=date(2026, 4, 5))
        inv1 = self._create_approved_invoice(amount=Decimal("600.00"), dt=date(2026, 4, 1))
        inv2 = self._create_approved_invoice(amount=Decimal("400.00"), dt=date(2026, 4, 2))

        session, state = self._build_session("session-test-e")
        res = session.run()

        self.assertTrue(res.is_success)
        self.assertEqual(len(res.executed_payment_application_ids), 1)
        self.assertEqual(res.rehydration_count, 1)

        inv1.refresh_from_db()
        inv2.refresh_from_db()
        self.assertEqual(inv1.amount_due - inv1.amount_paid, Decimal("0.00"))
        self.assertEqual(inv2.amount_due - inv2.amount_paid, Decimal("0.00"))
        self.assertEqual(inv1.amount_paid, Decimal("600.00"))
        self.assertEqual(inv2.amount_paid, Decimal("400.00"))

    # ==================================================================
    # Test F: Posted-authority competition
    # ==================================================================
    def test_scenario_f_posted_authority_competition(self) -> None:
        """
        Scenario F:
        - 2 bank movements ($500 each) compete for ONE posted cash tx ($500).
        - Open invoice of $500 also exists.
        - Both bank items are claimed by posted authority.
        - Zero Stage-1 payments executed for the open invoice.
        """
        stx1 = self._create_staged_movement(amount=Decimal("500.00"), dt=date(2026, 4, 5), name="Bank 1")
        stx1.fit_id = "REF-COMPETE"
        stx1.save()

        stx2 = self._create_staged_movement(amount=Decimal("500.00"), dt=date(2026, 4, 5), name="Bank 2")
        stx2.fit_id = "REF-COMPETE"
        stx2.save()

        tx = self._create_cash_tx(amount=Decimal("500.00"), is_debit=True, dt=date(2026, 4, 5))
        tx.journal_entry.je_number = "REF-COMPETE"
        tx.journal_entry.save(update_fields=["je_number"])

        inv = self._create_approved_invoice(amount=Decimal("500.00"), dt=date(2026, 4, 1))

        session, state = self._build_session("session-test-f")
        res = session.run()

        self.assertTrue(res.is_success)
        # Stage 1 executed 0 times
        self.assertEqual(len(res.executed_payment_application_ids), 0)
        self.assertEqual(res.rehydration_count, 0)
        self.assertEqual(res.payment_application_stage_result.status, SessionStageStatus.SKIPPED)

        # Invoice remains unpaid
        inv.refresh_from_db()
        self.assertEqual(inv.amount_due, Decimal("500.00"))

    # ==================================================================
    # Test G: Failure after one committed Stage-1 cycle
    # ==================================================================
    def test_scenario_g_failure_after_one_committed_cycle_preserves_truth(self) -> None:
        """
        Scenario G:
        - 2 bank movements and 2 invoices.
        - First payment commits to DB.
        - Second payment is rejected by engine.
        - First payment remains durable; session reports PAYMENT_APPLICATION failure.
        - No rollback of prior committed stages.
        """
        stx1 = self._create_staged_movement(amount=Decimal("200.00"), dt=date(2026, 4, 2))
        inv1 = self._create_approved_invoice(amount=Decimal("200.00"), dt=date(2026, 4, 1))

        stx2 = self._create_staged_movement(amount=Decimal("300.00"), dt=date(2026, 4, 4))
        inv2 = self._create_approved_invoice(amount=Decimal("300.00"), dt=date(2026, 4, 3))

        class FailingSecondEngine:
            def __init__(self, real: Any) -> None:
                self.real = real
                self.apply_call_count = 0

            @property
            def repository(self) -> Any:
                return self.real.repository

            def apply_batch(self, *args: Any, **kwargs: Any) -> Any:
                return self.real.apply_batch(*args, **kwargs)

            def apply(self, state: Any, command: Any) -> TransitionResult:
                if isinstance(command, ApplyPaymentCommand):
                    self.apply_call_count += 1
                    if self.apply_call_count == 2:
                        return TransitionResult.rejected_result(
                            command=command,
                            rejection=TransitionRejection(
                                code=RejectionCode.CAPACITY_EXCEEDED,
                                message="Simulated rejection on second payment",
                            ),
                            state_revision=state.revision,
                            persistence_revision=state.persistence_revision,
                        )
                return self.real.apply(state=state, command=command)

        session, state = self._build_session(
            "session-test-g",
            engine=FailingSecondEngine(self.engine),
        )
        res = session.run()

        self.assertFalse(res.is_success)
        self.assertEqual(res.failure_stage, FailureStage.PAYMENT_APPLICATION)
        self.assertIn("Simulated rejection on second payment", str(res.failure_reason))

        # Exactly 1 payment executed and committed
        self.assertEqual(len(res.executed_payment_application_ids), 1)
        self.assertEqual(res.rehydration_count, 1)

        # Prior committed payment remains durable!
        inv1.refresh_from_db()
        inv2.refresh_from_db()
        paid_invs = [inv for inv in (inv1, inv2) if inv.amount_due - inv.amount_paid == Decimal("0.00")]
        unpaid_invs = [inv for inv in (inv1, inv2) if inv.amount_due - inv.amount_paid > Decimal("0.00")]
        self.assertEqual(len(paid_invs), 1)
        self.assertEqual(len(unpaid_invs), 1)
        self.assertEqual(paid_invs[0].amount_paid, paid_invs[0].amount_due)
        self.assertEqual(unpaid_invs[0].amount_paid, Decimal("0.00"))

    # ==================================================================
    # Test H: Rehydration failure after successful Stage-1 commit
    # ==================================================================
    def test_scenario_h_rehydration_failure_yields_catastrophic_result(self) -> None:
        """
        Scenario H:
        - Bank movement $500, invoice $500.
        - Payment commits to DB.
        - Hydrator fails during post-commit rehydration.
        - Session returns catastrophic result with FailureStage.REHYDRATION.
        - Committed payment remains in DB.
        """
        stx = self._create_staged_movement(amount=Decimal("500.00"), dt=date(2026, 4, 5))
        inv = self._create_approved_invoice(amount=Decimal("500.00"), dt=date(2026, 4, 1))

        class FailingSecondHydrator:
            def __init__(self, real: Any) -> None:
                self.real = real
                self.hydrate_count = 0

            def hydrate(self, *args: Any, **kwargs: Any) -> Any:
                self.hydrate_count += 1
                if self.hydrate_count == 2:  # 1st is initial session hydration, 2nd is post-commit
                    raise RuntimeError("Simulated DB connection failure on rehydration")
                return self.real.hydrate(*args, **kwargs)

        session, state = self._build_session(
            "session-test-h",
            hydrator=FailingSecondHydrator(self.hydrator),
        )
        res = session.run()

        self.assertFalse(res.is_success)
        self.assertEqual(res.failure_stage, FailureStage.REHYDRATION)
        self.assertIn("Simulated DB connection failure", str(res.failure_reason))

        # Committed payment remains in DB!
        inv.refresh_from_db()
        self.assertEqual(inv.amount_due - inv.amount_paid, Decimal("0.00"))
        self.assertEqual(inv.amount_paid, Decimal("500.00"))
        self.assertEqual(len(res.executed_payment_application_ids), 1)

    # ==================================================================
    # Test I: No-progress / loop guard
    # ==================================================================
    def test_scenario_i_duplicate_bank_item_loop_guard(self) -> None:
        """
        Scenario I:
        - Buggy planner repeatedly proposes the same bank item after it was already executed.
        - Loop guard catches duplicate bank item and aborts with LOOP_INVARIANT.
        """
        stx = self._create_staged_movement(amount=Decimal("500.00"), dt=date(2026, 4, 5))
        inv = self._create_approved_invoice(amount=Decimal("500.00"), dt=date(2026, 4, 1))

        real_plan = plan_two_stage_bookkeeping
        plan_count = 0
        executed_prop = None

        def looping_plan(*args: Any, **kwargs: Any) -> Any:
            nonlocal plan_count, executed_prop
            plan_count += 1
            plan = real_plan(*args, **kwargs)
            if plan_count == 1 and plan.payment_application_plan.proposals:
                executed_prop = plan.payment_application_plan.proposals[0]
            elif plan_count == 2 and executed_prop is not None:
                # Maliciously reinject the executed bank item in iteration 2
                fake_plan = plan.payment_application_plan.model_copy(
                    update={"proposals": (executed_prop,)}
                )
                return replace(plan, payment_application_plan=fake_plan)
            return plan

        with patch(
            "bookkeeping_state.session.bookkeeping_session.plan_two_stage_bookkeeping",
            side_effect=looping_plan,
        ):
            session, state = self._build_session("session-test-i")
            res = session.run()

        self.assertFalse(res.is_success)
        self.assertEqual(res.failure_stage, FailureStage.LOOP_INVARIANT)
        self.assertIn("duplicate bank item", str(res.failure_reason).lower())

    # ==================================================================
    # Test J: Capability refresh
    # ==================================================================
    def test_scenario_j_capability_refresh_across_revisions(self) -> None:
        """
        Scenario J:
        - Capability minted against state revision N fails verification against state revision N+1.
        - Proves capabilities cannot be reused across rehydration cycles.
        """
        inv = self._create_approved_invoice(amount=Decimal("100.00"))
        stx = self._create_staged_movement(amount=Decimal("100.00"))

        state = self.hydrator.hydrate(
            company_id=str(self.entity.uuid),
            session_id="session-cap-j",
        )
        plan1 = plan_two_stage_bookkeeping(BookkeepingQueries(state))
        cap1 = plan1.get_capability(f"staged:{stx.uuid}")
        self.assertIsNotNone(cap1)
        assert cap1 is not None

        proposal = plan1.payment_application_plan.proposals[0]

        # Verify against current revision (N) succeeds
        self.assertTrue(
            verify_stage1_capability(
                capability=cap1,
                expected_bank_item_id=proposal.intent.bank_item_id,
                expected_bank_account_id=proposal.intent.bank_account_id,
                expected_direction=proposal.intent.direction,
                expected_currency=proposal.intent.currency,
                expected_total_amount_units=proposal.intent.total_amount_units,
                expected_payment_date=proposal.intent.payment_date,
                expected_allocations=proposal.intent.obligation_allocations,
                expected_session_id=state.session_id,
                expected_state_revision=state.revision,
                expected_state_fingerprint=state_fingerprint(state),
            )
        )

        # Mutate state to advance revision to N+1
        cmd = CreateRoutingDecisionCommand(
            command_id=f"cmd-route-{uuid.uuid4().hex[:6]}",
            expected_state_revision=state.revision,
            source=CommandSource.SYSTEM,
            session_id=state.session_id,
            issued_at=datetime.now(timezone.utc),
            routing_decision_id=f"route-{uuid.uuid4().hex[:6]}",
            book_item_id=f"invoice:{inv.uuid}",
            bank_account_id=str(self.bank_account.uuid),
            decision_source=RoutingDecisionSource.DETERMINISTIC_RULE,
            utility=100,
        )
        res = self.engine.apply(state=state, command=cmd)
        self.assertTrue(res.applied)
        self.assertEqual(state.revision, 1)

        # Capability from revision N now FAILS verification against revision N+1
        self.assertFalse(
            verify_stage1_capability(
                capability=cap1,
                expected_bank_item_id=proposal.intent.bank_item_id,
                expected_bank_account_id=proposal.intent.bank_account_id,
                expected_direction=proposal.intent.direction,
                expected_currency=proposal.intent.currency,
                expected_total_amount_units=proposal.intent.total_amount_units,
                expected_payment_date=proposal.intent.payment_date,
                expected_allocations=proposal.intent.obligation_allocations,
                expected_session_id=state.session_id,
                expected_state_revision=state.revision,
                expected_state_fingerprint=state_fingerprint(state),
            )
        )

    # ==================================================================
    # Test K: Full lifecycle end-to-end
    # ==================================================================
    def test_scenario_k_full_lifecycle_end_to_end(self) -> None:
        """
        Scenario K:
        - Full lifecycle: Route -> Classify -> Stage-1 Payment(s) -> Rehydrate(s) -> Stage-2 -> Detached final result.
        - State is closed on completion.
        - All stages applied and final validation valid.
        """
        # Unrouted and unclassified invoice
        inv = self._create_approved_invoice(amount=Decimal("500.00"), dt=date(2026, 4, 1))
        stx = self._create_staged_movement(amount=Decimal("500.00"), dt=date(2026, 4, 5))

        session, state = self._build_session("session-test-k")
        res = session.run()

        self.assertTrue(res.is_success)
        self.assertIsNone(res.failure_stage)
        self.assertTrue(state.is_closed)

        # All 4 stages ran and applied
        self.assertIsNotNone(res.routing_stage_result)
        self.assertTrue(res.routing_stage_result.is_applied or res.routing_stage_result.is_skipped)

        self.assertIsNotNone(res.dag_stage_result)
        self.assertTrue(res.dag_stage_result.is_applied or res.dag_stage_result.is_skipped)

        self.assertIsNotNone(res.payment_application_stage_result)
        self.assertTrue(res.payment_application_stage_result.is_applied)

        self.assertIsNotNone(res.reconciliation_stage_result)
        self.assertTrue(res.reconciliation_stage_result.is_applied)

        self.assertIsNotNone(res.final_validation_status)
        self.assertTrue(res.final_validation_status.is_valid)

        # Detached result has fingerprints and revisions
        self.assertIsNotNone(res.artifact_fingerprint)
        self.assertIsNotNone(res.state_fingerprint)
        self.assertGreater(res.final_persistence_revision, res.starting_persistence_revision)
