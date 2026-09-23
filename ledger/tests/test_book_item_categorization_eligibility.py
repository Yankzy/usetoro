from __future__ import annotations

from datetime import date, datetime, timezone
from decimal import Decimal
import io
from typing import Any
import uuid

from django.core.management import call_command
from django.test import TestCase

from bookkeeping_state.dag.view import build_dag_view
from bookkeeping_state.domain.books import BookItem
from bookkeeping_state.domain.classifications import (
    ClassificationDecision,
    ClassificationSource,
)
from bookkeeping_state.domain.enums import (
    BookkeepingRole,
    Direction,
    SourceArtifactKind,
    SourceType,
)
from bookkeeping_state.reconciliation.service import ReconciliationService
from bookkeeping_state.routing.scorer import ZeroRoutingSemanticScoreProvider
from bookkeeping_state.routing.service import RoutingService
from bookkeeping_state.session.bookkeeping_session import BookkeepingSession
from bookkeeping_state.session.result import SessionStageStatus
from bookkeeping_state.state.queries import BookkeepingQueries
from bookkeeping_state.payment_application.service import PaymentApplicationService
from ledger.tests.test_bookkeeping_session_stage1_integration import StubAseClassifier
from ledger.tests.test_stage1_accounting_execution import (
    Stage1AccountingExecutionTestBase,
)


class BookItemCategorizationEligibilityTests(Stage1AccountingExecutionTestBase):
    """
    Focused test suite proving that authoritative production BookItems
    (open invoices, open bills, and posted cash movements) are strictly excluded
    from semantic account categorization DAG view, while ensuring:
    - classification history remains readable
    - build_dag_view is empty for production BookItems
    - BookkeepingSession skips categorization cleanly and runs Stage 1 and Stage 2
    - no bank item categorization was introduced
    - no migrations were generated
    """

    def setUp(self) -> None:
        super().setUp()
        self.routing_service = RoutingService(
            semantic_provider=ZeroRoutingSemanticScoreProvider()
        )
        self.dag_classifier = StubAseClassifier(
            default_account_code=self.revenue_account.code
        )
        self.reconciliation_service = ReconciliationService()
        self.payment_application_service = PaymentApplicationService()

    def _build_session(self, session_id: str) -> tuple[BookkeepingSession, Any]:
        state = self.hydrator.hydrate(
            company_id=str(self.entity.uuid),
            session_id=session_id,
        )
        session = BookkeepingSession(
            state=state,
            engine=self.engine,
            routing_service=self.routing_service,
            dag_classifier=self.dag_classifier,
            reconciliation_service=self.reconciliation_service,
            payment_application_service=self.payment_application_service,
            hydrator=self.hydrator,
        )
        return session, state

    def test_a_open_invoice_excluded_from_unclassified_book_items(self) -> None:
        """Test A: Open Invoice BookItem is excluded from unclassified_book_items()."""
        inv = self._create_approved_invoice(amount=Decimal("250.00"), dt=date(2026, 4, 1))
        state = self.hydrator.hydrate(
            company_id=str(self.entity.uuid),
            session_id="session-test-inv",
        )
        queries = BookkeepingQueries(state)
        inv_item_id = f"invoice:{inv.uuid}"

        self.assertIn(inv_item_id, state.book_items)
        inv_item = state.book_items[inv_item_id]
        self.assertEqual(inv_item.source_artifact_kind, SourceArtifactKind.INVOICE)
        self.assertEqual(inv_item.bookkeeping_role, BookkeepingRole.OPEN_RECEIVABLE)
        self.assertFalse(inv_item.is_categorization_eligible)
        self.assertFalse(queries.is_categorization_eligible(inv_item_id))

        unclassified = queries.unclassified_book_items()
        self.assertNotIn(inv_item, unclassified)
        self.assertEqual(len(unclassified), 0)

    def test_b_open_bill_excluded_from_unclassified_book_items(self) -> None:
        """Test B: Open Bill BookItem is excluded from unclassified_book_items()."""
        bill = self._create_approved_bill(amount=Decimal("150.00"), dt=date(2026, 4, 1))
        state = self.hydrator.hydrate(
            company_id=str(self.entity.uuid),
            session_id="session-test-bill",
        )
        queries = BookkeepingQueries(state)
        bill_item_id = f"bill:{bill.uuid}"

        self.assertIn(bill_item_id, state.book_items)
        bill_item = state.book_items[bill_item_id]
        self.assertEqual(bill_item.source_artifact_kind, SourceArtifactKind.BILL)
        self.assertEqual(bill_item.bookkeeping_role, BookkeepingRole.OPEN_PAYABLE)
        self.assertFalse(bill_item.is_categorization_eligible)
        self.assertFalse(queries.is_categorization_eligible(bill_item_id))

        unclassified = queries.unclassified_book_items()
        self.assertNotIn(bill_item, unclassified)
        self.assertEqual(len(unclassified), 0)

    def test_c_posted_cash_tx_excluded_from_unclassified_book_items(self) -> None:
        """Test C: Posted cash Transaction BookItem is excluded from unclassified_book_items()."""
        tx = self._create_cash_tx(amount=Decimal("300.00"), is_debit=True, dt=date(2026, 4, 1))
        state = self.hydrator.hydrate(
            company_id=str(self.entity.uuid),
            session_id="session-test-tx",
        )
        queries = BookkeepingQueries(state)
        tx_item_id = f"tx:{tx.uuid}"

        self.assertIn(tx_item_id, state.book_items)
        tx_item = state.book_items[tx_item_id]
        self.assertEqual(tx_item.source_artifact_kind, SourceArtifactKind.TRANSACTION)
        self.assertEqual(tx_item.bookkeeping_role, BookkeepingRole.POSTED_CASH_MOVEMENT)
        self.assertEqual(tx_item.source_type, SourceType.POSTED_BOOK_ITEM)
        self.assertFalse(tx_item.is_categorization_eligible)
        self.assertFalse(queries.is_categorization_eligible(tx_item_id))

        unclassified = queries.unclassified_book_items()
        self.assertNotIn(tx_item, unclassified)
        self.assertEqual(len(unclassified), 0)

    def test_d_existing_durable_classification_history_remains_readable(self) -> None:
        """Test D: Existing durable classification history remains readable and unchanged."""
        inv = self._create_approved_invoice(amount=Decimal("100.00"), dt=date(2026, 4, 1))
        state = self.hydrator.hydrate(
            company_id=str(self.entity.uuid),
            session_id="session-test-history",
        )
        inv_item_id = f"invoice:{inv.uuid}"

        # Simulate prior historical classification decision attached to state
        decision = ClassificationDecision(
            id="dec-test-1",
            book_item_id=inv_item_id,
            account_code="4010",
            source=ClassificationSource.ASE_DAG,
            confidence=1.0,
            rationale="Legacy classification",
            state_revision_at_decision=state.revision,
            created_at=datetime.now(timezone.utc),
        )
        state._classifications[decision.id] = decision
        queries = BookkeepingQueries(state)

        # Ensure history is preserved and readable through queries
        active = queries.active_classification(inv_item_id)
        self.assertIsNotNone(active)
        self.assertEqual(active.account_code, "4010")
        self.assertEqual(active.rationale, "Legacy classification")

        all_decisions = queries.classifications_for_book_item(inv_item_id)
        self.assertEqual(len(all_decisions), 1)
        self.assertEqual(all_decisions[0].id, "dec-test-1")

    def test_e_build_dag_view_empty_with_production_book_items(self) -> None:
        """Test E: build_dag_view() is empty when state contains only production BookItem types."""
        self._create_approved_invoice(amount=Decimal("200.00"), dt=date(2026, 4, 1))
        self._create_approved_bill(amount=Decimal("100.00"), dt=date(2026, 4, 1))
        self._create_cash_tx(amount=Decimal("50.00"), is_debit=True, dt=date(2026, 4, 1))

        state = self.hydrator.hydrate(
            company_id=str(self.entity.uuid),
            session_id="session-test-dag-empty",
        )
        queries = BookkeepingQueries(state)
        # All 3 items exist in state
        self.assertEqual(len(state.book_items), 3)

        dag_view = build_dag_view(queries)
        self.assertEqual(len(dag_view.items), 0)
        self.assertEqual(dag_view.items, ())

    def test_f_session_continues_into_stage1_and_stage2_when_categorization_has_zero_eligible(self) -> None:
        """Test F: BookkeepingSession continues into Stage 1 and Stage 2 when categorization has no eligible items."""
        # 1 bank item matching an open invoice (Stage 1 flow)
        stx = self._create_staged_movement(amount=Decimal("500.00"), dt=date(2026, 4, 5))
        stx.fit_id = "REF-STAGE1-F"
        stx.save()

        inv = self._create_approved_invoice(amount=Decimal("500.00"), dt=date(2026, 4, 1))

        # 1 bank item matching a posted cash tx (Stage 2 flow)
        stx2 = self._create_staged_movement(amount=Decimal("300.00"), dt=date(2026, 4, 6))
        stx2.fit_id = "REF-STAGE2-F"
        stx2.save()

        tx2 = self._create_cash_tx(amount=Decimal("300.00"), is_debit=True, dt=date(2026, 4, 6))
        tx2.journal_entry.je_number = "REF-STAGE2-F"
        tx2.journal_entry.save(update_fields=["je_number"])

        session, state = self._build_session("session-test-f")
        res = session.run()

        self.assertTrue(res.is_success)
        self.assertIsNone(res.failure_stage)

        # DAG stage was SKIPPED because 0 eligible items were presented
        self.assertIsNotNone(res.dag_stage_result)
        self.assertEqual(res.dag_stage_result.status, SessionStageStatus.SKIPPED)

        # Stage 1 executed the invoice payment
        self.assertIsNotNone(res.payment_application_stage_result)
        self.assertEqual(res.payment_application_stage_result.status, SessionStageStatus.APPLIED)
        self.assertEqual(len(res.executed_payment_application_ids), 1)

        # Stage 2 executed reconciliation
        self.assertIsNotNone(res.reconciliation_stage_result)
        self.assertIn(
            res.reconciliation_stage_result.status,
            {SessionStageStatus.APPLIED, SessionStageStatus.SKIPPED},
        )
        self.assertGreaterEqual(res.reconciliation_stage_result.command_count, 1)

    def test_g_no_direct_bank_item_categorization_introduced(self) -> None:
        """Test G: No direct BankItem categorization was introduced."""
        stx = self._create_staged_movement(amount=Decimal("123.00"), dt=date(2026, 4, 5))
        state = self.hydrator.hydrate(
            company_id=str(self.entity.uuid),
            session_id="session-test-g",
        )
        queries = BookkeepingQueries(state)

        # Bank items exist in state.bank_items
        self.assertGreater(len(state.bank_items), 0)

        # Bank items NEVER appear in unclassified_book_items
        unclassified = queries.unclassified_book_items()
        bank_ids = set(state.bank_items.keys())
        for item in unclassified:
            self.assertNotIn(item.id, bank_ids)
            self.assertIsInstance(item, BookItem)

        # build_dag_view does not contain bank items
        dag_view = build_dag_view(queries)
        for view_item in dag_view.items:
            self.assertNotIn(view_item.book_item_id, bank_ids)

    def test_h_no_django_migrations(self) -> None:
        """Test H: No Django migrations were introduced or required."""
        out = io.StringIO()
        call_command("makemigrations", "--check", "--dry-run", stdout=out)
        self.assertNotIn("Migrations for", out.getvalue())
