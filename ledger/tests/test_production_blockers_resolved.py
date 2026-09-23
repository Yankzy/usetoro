from __future__ import annotations

from datetime import date, datetime, timezone
from decimal import Decimal
from typing import Any
from unittest.mock import MagicMock, patch
import uuid

from django.test import TestCase

from bookkeeping_state.bank_categorization.nats_bank_categorizer import (
    NatsAseBankCategorizer,
)
from bookkeeping_state.bank_categorization.protocol import (
    MissingResidualBankCategorizerError,
    ResidualBankCategorizer,
)
from bookkeeping_state.bank_categorization.transport_models import (
    DEFAULT_BANK_CATEGORIZER_NATS_SUBJECT,
)
from bookkeeping_state.dag.errors import AseTransportError
from bookkeeping_state.dag.nats_book_categorizer import (
    DEFAULT_BOOK_CATEGORIZER_NATS_SUBJECT,
    NatsAseBookCategorizer,
)
from bookkeeping_state.domain.commands import (
    CommandSource,
    InvalidateReconciliationCommand,
    InvalidateResidualBankClassificationCommand,
    PostResidualBankClassificationCommand,
    RecordResidualBankClassificationCommand,
)
from bookkeeping_state.domain.enums import Direction
from bookkeeping_state.domain.residual_bank_classifications import (
    ResidualBankClassificationStatus,
)
from bookkeeping_state.hydration.hydrator import BookkeepingHydrator
from bookkeeping_state.payment_application.service import PaymentApplicationService
from bookkeeping_state.persistence.repository import (
    BookkeepingRepository,
    PersistenceError,
    PersistenceWriteSet,
)
from bookkeeping_state.reconciliation.service import ReconciliationService
from bookkeeping_state.routing.scorer import ZeroRoutingSemanticScoreProvider
from bookkeeping_state.routing.service import RoutingService
from bookkeeping_state.session.bookkeeping_session import BookkeepingSession
from bookkeeping_state.session.factory import (
    create_production_session,
    create_production_residual_bank_categorizer,
)
from bookkeeping_state.session.result import (
    FailureStage,
    SessionStageStatus,
)
from bookkeeping_state.session.service import (
    BookkeepingApplicationService,
    create_production_bookkeeping_application_service,
    run_bookkeeping_session,
)
from bookkeeping_state.transitions.batch import TransitionBatch
from bookkeeping_state.transitions.result import (
    RejectionCode,
    TransitionStatus,
)
from ledger.models.bookkeeping import (
    BookkeepingReconciliation,
    BookkeepingReconciliationBankAllocation,
    BookkeepingReconciliationBookAllocation,
    BookkeepingReconciliationInvalidation,
    BookkeepingResidualBankClassificationDecision,
    BookkeepingResidualBankPosting,
    BookkeepingRevision,
)
from ledger.models.journal_entry import JournalEntryModel
from ledger.tests.test_bookkeeping_session_stage1_integration import (
    StubAseClassifier,
)
from ledger.tests.test_bookkeeping_session_residual_bank_integration import (
    MockResidualBankCategorizer,
)
from ledger.tests.test_stage1_accounting_execution import (
    Stage1AccountingExecutionTestBase,
)


class ProductionBlockersResolvedTests(Stage1AccountingExecutionTestBase):
    """
    Focused test suite proving resolution of:
    Blocker 1: Posting-owned reconciliations are sealed against invalidation.
    Blocker 2: Explicit production residual categorizer wiring, configuration, and preflight.
    """

    def setUp(self) -> None:
        super().setUp()
        self.repository = BookkeepingRepository()
        self.routing_service = RoutingService(
            semantic_provider=ZeroRoutingSemanticScoreProvider()
        )
        self.dag_classifier = StubAseClassifier(
            default_account_code=self.revenue_account.code
        )
        self.reconciliation_service = ReconciliationService()
        self.payment_application_service = PaymentApplicationService()
        self.expense_account.active = True
        self.expense_account.save(update_fields=["active"])

    def _setup_posted_residual(
        self,
        amount: Decimal = Decimal("250.00"),
    ) -> tuple[str, BookkeepingResidualBankPosting, BookkeepingReconciliation]:
        """
        Execute one full residual session resulting in an authoritative posting
        and its direct reconciliation.
        """
        stx = self._create_staged_movement(
            amount=amount,
            dt=date(2026, 4, 5),
            name="Software subscription",
        )
        session_id = f"sess-{uuid.uuid4().hex[:8]}"
        state = self.hydrator.hydrate(
            company_id=str(self.entity.uuid),
            session_id=session_id,
        )
        categorizer = MockResidualBankCategorizer(
            default_account_code=self.expense_account.code,
            default_status="CLASSIFIED",
        )
        session = BookkeepingSession(
            state=state,
            engine=self.engine,
            routing_service=self.routing_service,
            dag_classifier=self.dag_classifier,
            residual_bank_categorizer=categorizer,
            reconciliation_service=self.reconciliation_service,
            payment_application_service=self.payment_application_service,
            hydrator=self.hydrator,
        )
        res = session.run()
        self.assertTrue(res.is_success, f"Setup session failed: {res.failure_reason}")
        self.assertEqual(res.residual_posting_count, 1)

        posting_row = BookkeepingResidualBankPosting.objects.get(
            staged_transaction=stx
        )
        recon_row = posting_row.reconciliation
        return session_id, posting_row, recon_row

    # =========================================================================
    # BLOCKER 1 TESTS: SEAL POSTING-OWNED RECONCILIATIONS
    # =========================================================================

    def test_a_handler_rejects_invalidating_posting_owned_reconciliation(self) -> None:
        """
        A. Transition handler rejects InvalidateReconciliationCommand when
        target reconciliation is owned by a ResidualBankPosting.
        """
        session_id, posting_row, recon_row = self._setup_posted_residual()

        state = self.hydrator.hydrate(
            company_id=str(self.entity.uuid),
            session_id=f"sess-inv-{uuid.uuid4().hex[:8]}",
        )
        inv_cmd = InvalidateReconciliationCommand(
            command_id="cmd:inv-recon-1",
            expected_state_revision=state.revision,
            source=CommandSource.HUMAN,
            session_id=state.session_id,
            issued_at=datetime.now(timezone.utc),
            invalidation_id=f"inv:{uuid.uuid4().hex[:8]}",
            reconciliation_id=recon_row.id,
            reason="Operator manual invalidation attempt",
        )

        res = self.engine.apply(state=state, command=inv_cmd)
        self.assertEqual(res.status, TransitionStatus.REJECTED)
        self.assertIsNotNone(res.rejection)
        self.assertEqual(
            res.rejection.code,
            RejectionCode.CANNOT_INVALIDATE_POSTING_RECONCILIATION,
        )
        self.assertIn("authoritative residual-bank posting", res.rejection.message)
        self.assertIn("formal posting reversal", res.rejection.message)

    def test_b_rejection_code_is_exact_and_stable(self) -> None:
        """
        B. Machine-readable rejection code is CANNOT_INVALIDATE_POSTING_RECONCILIATION.
        """
        self.assertEqual(
            RejectionCode.CANNOT_INVALIDATE_POSTING_RECONCILIATION.value,
            "CANNOT_INVALIDATE_POSTING_RECONCILIATION",
        )

    def test_c_db_writer_independently_rejects_posting_owned_reconciliation_invalidation(self) -> None:
        """
        C. DB writer independently rejects posting-owned reconciliation invalidation
        even if handler or state-level check is bypassed.
        """
        _, posting_row, recon_row = self._setup_posted_residual()

        from bookkeeping_state.domain.reconciliations import ReconciliationInvalidation

        invalidation_artifact = ReconciliationInvalidation(
            id=f"inv-bypass-{uuid.uuid4().hex[:8]}",
            reconciliation_id=recon_row.id,
            reason="Bypassed handler test",
            session_id="sess-bypass",
            state_revision_at_invalidation=1,
            created_at=datetime.now(timezone.utc),
        )
        write_set = PersistenceWriteSet(
            reconciliation_invalidations=(invalidation_artifact,)
        )

        current_p_rev = BookkeepingRevision.objects.get(entity=self.entity).revision
        with self.assertRaises(PersistenceError) as cm:
            self.repository.commit(
                company_id=str(self.entity.uuid),
                expected_revision=current_p_rev,
                write_set=write_set,
            )

        self.assertIn("CANNOT_INVALIDATE_POSTING_RECONCILIATION", str(cm.exception))
        self.assertIn("owned by an authoritative residual bank posting", str(cm.exception))

    def test_d_posting_row_je_recon_revision_remain_unchanged_after_rejection(self) -> None:
        """
        D. Posting row, JE, reconciliation, and revision remain unchanged after rejection.
        """
        session_id, posting_row, recon_row = self._setup_posted_residual()

        initial_p_rev = BookkeepingRevision.objects.get(entity=self.entity).revision
        je_row = posting_row.journal_entry

        state = self.hydrator.hydrate(
            company_id=str(self.entity.uuid),
            session_id=f"sess-inv-{uuid.uuid4().hex[:8]}",
        )
        inv_cmd = InvalidateReconciliationCommand(
            command_id="cmd:inv-recon-d",
            expected_state_revision=state.revision,
            source=CommandSource.HUMAN,
            session_id=state.session_id,
            issued_at=datetime.now(timezone.utc),
            invalidation_id=f"inv-d:{uuid.uuid4().hex[:8]}",
            reconciliation_id=recon_row.id,
            reason="Attempt invalidation",
        )
        res = self.engine.apply(state=state, command=inv_cmd)
        self.assertEqual(res.status, TransitionStatus.REJECTED)

        # Verify DB rows are intact
        posting_row.refresh_from_db()
        recon_row.refresh_from_db()
        je_row.refresh_from_db()
        final_p_rev = BookkeepingRevision.objects.get(entity=self.entity).revision

        self.assertEqual(initial_p_rev, final_p_rev)
        self.assertTrue(je_row.posted)
        self.assertTrue(je_row.locked)
        self.assertFalse(
            BookkeepingReconciliationInvalidation.objects.filter(
                reconciliation=recon_row
            ).exists()
        )

    def test_e_next_session_does_not_enter_case_e(self) -> None:
        """
        E. Because invalidation was rejected, the next session runs cleanly
        and does NOT enter Case E invariant corruption.
        """
        session_id, posting_row, recon_row = self._setup_posted_residual()

        # Attempt to invalidate and verify rejection
        state = self.hydrator.hydrate(
            company_id=str(self.entity.uuid),
            session_id=f"sess-inv-{uuid.uuid4().hex[:8]}",
        )
        inv_cmd = InvalidateReconciliationCommand(
            command_id="cmd:inv-recon-e",
            expected_state_revision=state.revision,
            source=CommandSource.HUMAN,
            session_id=state.session_id,
            issued_at=datetime.now(timezone.utc),
            invalidation_id=f"inv-e:{uuid.uuid4().hex[:8]}",
            reconciliation_id=recon_row.id,
            reason="Attempt invalidation",
        )
        self.engine.apply(state=state, command=inv_cmd)

        # Run next session
        next_session_id = f"sess-next-{uuid.uuid4().hex[:8]}"
        state_next = self.hydrator.hydrate(
            company_id=str(self.entity.uuid),
            session_id=next_session_id,
        )
        categorizer = MockResidualBankCategorizer(
            default_account_code=self.expense_account.code
        )
        next_session = BookkeepingSession(
            state=state_next,
            engine=self.engine,
            routing_service=self.routing_service,
            dag_classifier=self.dag_classifier,
            residual_bank_categorizer=categorizer,
            reconciliation_service=self.reconciliation_service,
            payment_application_service=self.payment_application_service,
            hydrator=self.hydrator,
        )
        res_next = next_session.run()
        self.assertTrue(res_next.is_success)
        self.assertNotEqual(res_next.failure_stage, FailureStage.LOOP_INVARIANT)
        # Bank item was already posted; zero residual work remaining
        self.assertEqual(res_next.residual_evaluation_count, 0)
        self.assertEqual(res_next.residual_posting_count, 0)

    def test_f_ordinary_non_posting_reconciliation_invalidation_still_succeeds(self) -> None:
        """
        F. Ordinary non-posting Stage 2 reconciliation invalidation continues to work.
        """
        cash_tx = self._create_cash_tx(
            amount=Decimal("100.00"),
            is_debit=True,
            dt=date(2026, 4, 5),
        )
        stx = self._create_staged_movement(
            amount=Decimal("100.00"),
            dt=date(2026, 4, 5),
            name="Manual bank match",
        )
        recon_id = f"rec-ord-{uuid.uuid4().hex[:8]}"
        recon_row = BookkeepingReconciliation.objects.create(
            id=recon_id,
            entity=self.entity,
            state_revision_at_creation=1,
            created_at=datetime.now(timezone.utc),
        )
        BookkeepingReconciliationBankAllocation.objects.create(
            reconciliation=recon_row,
            staged_transaction=stx,
            amount_units=1000000,
        )
        BookkeepingReconciliationBookAllocation.objects.create(
            reconciliation=recon_row,
            transaction=cash_tx,
            amount_units=1000000,
        )

        state2 = self.hydrator.hydrate(
            company_id=str(self.entity.uuid),
            session_id="sess-ordinary-inv",
        )
        inv_cmd = InvalidateReconciliationCommand(
            command_id="cmd:inv-ord",
            expected_state_revision=state2.revision,
            source=CommandSource.HUMAN,
            session_id=state2.session_id,
            issued_at=datetime.now(timezone.utc),
            invalidation_id=f"inv-ord-{uuid.uuid4().hex[:8]}",
            reconciliation_id=recon_id,
            reason="Legitimate operator correction of ordinary reconciliation",
        )
        res_inv = self.engine.apply(state=state2, command=inv_cmd)
        self.assertEqual(res_inv.status, TransitionStatus.APPLIED)
        self.assertTrue(
            BookkeepingReconciliationInvalidation.objects.filter(
                reconciliation_id=recon_id
            ).exists()
        )

    def test_g_posted_decision_remains_sealed(self) -> None:
        """
        G. Posted decision remains sealed against invalidation.
        """
        _, posting_row, _ = self._setup_posted_residual()
        state = self.hydrator.hydrate(
            company_id=str(self.entity.uuid),
            session_id="sess-seal-check",
        )
        cmd = InvalidateResidualBankClassificationCommand(
            command_id="cmd:inv-dec-posted",
            expected_state_revision=state.revision,
            expected_persistence_revision=state.persistence_revision,
            session_id=state.session_id,
            issued_at=datetime.now(timezone.utc),
            invalidation_id="inv:dec-posted",
            decision_id=posting_row.classification_id,
            reason="Attempt to invalidate posted decision",
        )
        res = self.engine.apply(state=state, command=cmd)
        self.assertEqual(res.status, TransitionStatus.REJECTED)
        self.assertEqual(
            res.rejection.code,
            RejectionCode.CANNOT_INVALIDATE_POSTED_DECISION,
        )

    # =========================================================================
    # BLOCKER 2 TESTS: EXPLICIT PRODUCTION RESIDUAL CATEGORIZER WIRING
    # =========================================================================

    def test_i_bookkeeping_application_service_passes_configured_categorizer(self) -> None:
        """
        I. BookkeepingApplicationService stores and forwards the configured residual categorizer.
        """
        custom_categorizer = MockResidualBankCategorizer(
            default_account_code=self.expense_account.code
        )
        app_service = BookkeepingApplicationService(
            repository=self.repository,
            hydrator=self.hydrator,
            transition_engine=self.engine,
            dag_classifier=self.dag_classifier,
            residual_bank_categorizer=custom_categorizer,
            reconciliation_service=self.reconciliation_service,
            payment_application_service=self.payment_application_service,
        )
        self.assertIs(app_service.residual_bank_categorizer, custom_categorizer)

        stx = self._create_staged_movement(
            amount=Decimal("150.00"),
            dt=date(2026, 4, 5),
            name="Service check",
        )
        res = app_service.run_session(
            company_id=str(self.entity.uuid),
            session_id=f"sess-svc-{uuid.uuid4().hex[:8]}",
        )
        self.assertTrue(res.is_success)
        self.assertEqual(custom_categorizer.call_count, 1)

    def test_j_production_application_service_factory_creates_real_nats_bank_categorizer(self) -> None:
        """
        J. create_production_bookkeeping_application_service creates real NatsAseBankCategorizer.
        """
        app_service = create_production_bookkeeping_application_service(
            nats_url="nats://prod-nats:4222",
            timeout_seconds=15.0,
        )
        self.assertIsInstance(
            app_service.residual_bank_categorizer,
            NatsAseBankCategorizer,
        )
        categorizer = app_service.residual_bank_categorizer
        self.assertEqual(categorizer.nats_url, "nats://prod-nats:4222")
        self.assertEqual(categorizer.timeout_seconds, 15.0)
        self.assertEqual(
            categorizer.subject,
            DEFAULT_BANK_CATEGORIZER_NATS_SUBJECT,
        )

    def test_k_custom_nats_url_propagates_to_both_dag_and_residual(self) -> None:
        """
        K. A configured custom nats_url is used by BOTH DAG and residual categorizers.
        """
        custom_url = "nats://clustered-nats:4222"
        app_service = create_production_bookkeeping_application_service(
            nats_url=custom_url,
            timeout_seconds=7.5,
        )
        self.assertEqual(app_service.dag_classifier.nats_url, custom_url)
        self.assertEqual(
            app_service.residual_bank_categorizer.nats_url,
            custom_url,
        )

    def test_l_production_session_factory_injects_residual_categorizer_explicitly(self) -> None:
        """
        L. create_production_session explicitly wires residual categorizer using supplied nats_url.
        """
        custom_url = "nats://custom-session-nats:4222"
        session = create_production_session(
            company_id=str(self.entity.uuid),
            session_id="sess-fact-test",
            nats_url=custom_url,
            timeout_seconds=5.0,
        )
        self.assertIsInstance(
            session._residual_bank_categorizer,
            NatsAseBankCategorizer,
        )
        self.assertEqual(
            session._residual_bank_categorizer.nats_url,
            custom_url,
        )
        self.assertEqual(
            session._residual_bank_categorizer.timeout_seconds,
            5.0,
        )

    def test_m_residual_categorizer_readiness_is_checked_in_service(self) -> None:
        """
        M. residual categorizer readiness check is executed during application service preflight.
        """
        mock_cat = MagicMock()
        mock_cat.check_readiness = MagicMock()
        mock_cat.categorize_view = MagicMock()

        app_service = BookkeepingApplicationService(
            repository=self.repository,
            hydrator=self.hydrator,
            transition_engine=self.engine,
            dag_classifier=self.dag_classifier,
            residual_bank_categorizer=mock_cat,
            reconciliation_service=self.reconciliation_service,
            payment_application_service=self.payment_application_service,
        )
        app_service.run_session(
            company_id=str(self.entity.uuid),
            session_id="sess-readiness-ok",
        )
        mock_cat.check_readiness.assert_called_once()

    def test_n_readiness_failure_aborts_before_bookkeeping_mutations(self) -> None:
        """
        N. Preflight readiness failure aborts execution before hydration or mutations.
        """
        mock_cat = MagicMock()
        mock_cat.check_readiness.side_effect = AseTransportError("NATS cluster unreachable")

        app_service = BookkeepingApplicationService(
            repository=self.repository,
            hydrator=self.hydrator,
            transition_engine=self.engine,
            dag_classifier=self.dag_classifier,
            residual_bank_categorizer=mock_cat,
            reconciliation_service=self.reconciliation_service,
            payment_application_service=self.payment_application_service,
        )
        with self.assertRaises(AseTransportError) as cm:
            app_service.run_session(
                company_id=str(self.entity.uuid),
                session_id="sess-readiness-fail",
            )
        self.assertIn("NATS cluster unreachable", str(cm.exception))

    def test_o_missing_categorizer_with_candidates_fails_residual_categorization(self) -> None:
        """
        O. Missing categorizer when semantic candidates exist => explicit
        RESIDUAL_CATEGORIZATION failure stage, never silent success.
        """
        stx = self._create_staged_movement(
            amount=Decimal("300.00"),
            dt=date(2026, 4, 5),
            name="Unclassified residual",
        )
        state = self.hydrator.hydrate(
            company_id=str(self.entity.uuid),
            session_id="sess-no-cat-with-cand",
        )
        # Explicitly pass residual_bank_categorizer=None
        session = BookkeepingSession(
            state=state,
            engine=self.engine,
            routing_service=self.routing_service,
            dag_classifier=self.dag_classifier,
            residual_bank_categorizer=None,
            reconciliation_service=self.reconciliation_service,
            payment_application_service=self.payment_application_service,
            hydrator=self.hydrator,
        )
        res = session.run()
        self.assertFalse(res.is_success)
        self.assertEqual(res.failure_stage, FailureStage.RESIDUAL_CATEGORIZATION)
        self.assertIn("not configured", res.failure_reason)
        # Bank item remains unclassified and unposted
        self.assertEqual(res.residual_classified_count, 0)
        self.assertEqual(res.residual_posting_count, 0)

    def test_p_missing_categorizer_with_zero_candidates_completes_normally(self) -> None:
        """
        P. Missing categorizer when zero semantic candidates exist => session completes normally.
        """
        state = self.hydrator.hydrate(
            company_id=str(self.entity.uuid),
            session_id="sess-no-cat-no-cand",
        )
        # No staged transactions created; candidate count is 0
        session = BookkeepingSession(
            state=state,
            engine=self.engine,
            routing_service=self.routing_service,
            dag_classifier=self.dag_classifier,
            residual_bank_categorizer=None,
            reconciliation_service=self.reconciliation_service,
            payment_application_service=self.payment_application_service,
            hydrator=self.hydrator,
        )
        res = session.run()
        self.assertTrue(res.is_success)
        self.assertEqual(res.residual_evaluation_count, 0)
        self.assertEqual(res.residual_posting_count, 0)

    def test_q_custom_mock_categorizer_injection_in_tests_preserved(self) -> None:
        """
        Q. Custom test mock categorizers continue to function without requiring real NATS.
        """
        stx = self._create_staged_movement(
            amount=Decimal("75.00"),
            dt=date(2026, 4, 5),
            name="Mock test",
        )
        mock_cat = MockResidualBankCategorizer(
            default_account_code=self.expense_account.code,
            default_status="HOLD",
            default_hold_reason="Waiting for invoice",
        )
        state = self.hydrator.hydrate(
            company_id=str(self.entity.uuid),
            session_id="sess-custom-mock",
        )
        session = BookkeepingSession(
            state=state,
            engine=self.engine,
            routing_service=self.routing_service,
            dag_classifier=self.dag_classifier,
            residual_bank_categorizer=mock_cat,
            reconciliation_service=self.reconciliation_service,
            payment_application_service=self.payment_application_service,
            hydrator=self.hydrator,
        )
        res = session.run()
        self.assertTrue(res.is_success)
        self.assertEqual(mock_cat.call_count, 1)
        self.assertEqual(res.residual_hold_count, 1)
        self.assertEqual(res.residual_posting_count, 0)
