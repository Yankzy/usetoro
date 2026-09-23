from __future__ import annotations

from datetime import date, datetime, timezone
from decimal import Decimal
from typing import Any, cast
from unittest.mock import patch
from uuid import uuid4

from bookkeeping_state.domain.classifications import ClassificationSource
from bookkeeping_state.domain.commands import (
    CommandSource,
    CreateClassificationCommand,
    CreateRoutingDecisionCommand,
    InvalidateResidualBankClassificationCommand,
    RecordResidualBankClassificationCommand,
)
from bookkeeping_state.domain.enums import Direction
from bookkeeping_state.domain.events import StateEventType
from bookkeeping_state.domain.residual_bank_classifications import (
    ResidualBankClassificationStatus,
)
from bookkeeping_state.domain.routing import RoutingDecisionSource
from bookkeeping_state.hydration import BookkeepingHydrator
from bookkeeping_state.persistence.repository import BookkeepingRepository
from bookkeeping_state.state.queries import BookkeepingQueries
from bookkeeping_state.transitions.engine import (
    CommittedStateApplicationError,
    TransitionEngine,
)
from bookkeeping_state.transitions.result import (
    RejectionCode,
    TransitionStatus,
)
from django.contrib.auth import get_user_model
from django.test import TestCase

from ledger.io.roles import (
    ASSET_CA_CASH,
    ASSET_CA_RECEIVABLES,
    CREDIT,
    DEBIT,
    EXPENSE_OPERATIONAL,
    LIABILITY_CL_DEFERRED_REVENUE,
)
from ledger.models import (
    BankAccountModel,
    CustomerModel,
    EntityModel,
    ImportJobModel,
    InvoiceModel,
    JournalEntryModel,
    LedgerModel,
    StagedTransactionModel,
    TransactionModel,
)
from ledger.models.bookkeeping import (
    BookkeepingPaymentApplication,
    BookkeepingPaymentApplicationAllocation,
    BookkeepingReconciliation,
    BookkeepingReconciliationBankAllocation,
    BookkeepingReconciliationBookAllocation,
    BookkeepingResidualBankClassificationDecision,
    BookkeepingResidualBankClassificationInvalidation,
)

UserModel = get_user_model()


class ResidualBankClassificationTransitionTests(TestCase):
    @classmethod
    def setUpTestData(cls) -> None:
        cls.user = cast(Any, UserModel.objects).create_user(
            username=f"testuser_{uuid4().hex[:8]}",
            email=f"test_{uuid4().hex[:8]}@example.com",
            password="testpassword123",
        )
        cls.entity = EntityModel.add_root(
            name="Residual Transition Test Corp",
            admin=cls.user,
            currency="USD",
            fy_start_month=1,
            accrual_method=False,
        )
        cls.coa = cls.entity.create_chart_of_accounts(
            coa_name="Default CoA",
            assign_as_default=True,
            commit=True,
        )
        asset_root = cls.coa.accountmodel_set.get(code="01000000")
        cls.cash_account = asset_root.add_child(
            coa_model=cls.coa,
            code="1010",
            name="Cash Account",
            role=ASSET_CA_CASH,
            balance_type=DEBIT,
            active=True,
        )
        cls.receivables_account = asset_root.add_child(
            coa_model=cls.coa,
            code="1020",
            name="Accounts Receivable",
            role=ASSET_CA_RECEIVABLES,
            balance_type=DEBIT,
            active=True,
        )

        liability_root = cls.coa.accountmodel_set.get(code="02000000")
        cls.unearned_revenue_account = liability_root.add_child(
            coa_model=cls.coa,
            code="2010",
            name="Deferred Revenue",
            role=LIABILITY_CL_DEFERRED_REVENUE,
            balance_type=CREDIT,
            active=True,
        )

        expense_root = cls.coa.accountmodel_set.get(code="06000000")
        cls.expense_account = expense_root.add_child(
            coa_model=cls.coa,
            code="6111",
            name="Office Supplies Expense",
            role=EXPENSE_OPERATIONAL,
            balance_type=DEBIT,
            active=True,
        )

        cls.expense_account_2 = expense_root.add_child(
            coa_model=cls.coa,
            code="6222",
            name="Advertising Expense",
            role=EXPENSE_OPERATIONAL,
            balance_type=DEBIT,
            active=True,
        )

        cls.inactive_expense_account = expense_root.add_child(
            coa_model=cls.coa,
            code="6888",
            name="Inactive Expense",
            role=EXPENSE_OPERATIONAL,
            balance_type=DEBIT,
            active=False,
        )

        # Secondary CoA (non-default)
        cls.secondary_coa = cls.entity.create_chart_of_accounts(
            coa_name="Secondary CoA",
            assign_as_default=False,
            commit=True,
        )
        sec_root = cls.secondary_coa.accountmodel_set.get(code="06000000")
        cls.foreign_account = sec_root.add_child(
            coa_model=cls.secondary_coa,
            code="6999",
            name="Foreign Expense",
            role=EXPENSE_OPERATIONAL,
            balance_type=DEBIT,
            active=True,
        )

        cls.bank_account = BankAccountModel.objects.create(
            entity_model=cls.entity,
            account_model=cls.cash_account,
            name="Main Operating Account",
            active=True,
        )

        cls.customer = CustomerModel.objects.create(
            customer_name="Acme Corp",
            customer_number="CUST-101",
            entity_model=cls.entity,
        )

        cls.import_job = ImportJobModel.objects.create(
            bank_account_model=cls.bank_account,
            description="OFX Import Job",
        )

        cls.staged_tx = StagedTransactionModel.objects.create(
            import_job=cls.import_job,
            fit_id="fit-tx-001",
            date_posted=date(2026, 8, 1),
            amount=Decimal("-150.00"),
            name="Office Depot #123",
            memo="Stationery purchase",
        )

        cls.staged_tx2 = StagedTransactionModel.objects.create(
            import_job=cls.import_job,
            fit_id="fit-tx-002",
            date_posted=date(2026, 8, 2),
            amount=Decimal("-300.00"),
            name="Online Ad Campaign",
        )

    def setUp(self) -> None:
        self.repository = BookkeepingRepository()
        self.hydrator = BookkeepingHydrator(repository=self.repository)
        self.engine = TransitionEngine(repository=self.repository)

    def _hydrate_state(self, session_id: str = "test-session-001"):
        return self.hydrator.hydrate(
            company_id=str(self.entity.uuid),
            session_id=session_id,
        )

    # ------------------------------------------------------------------
    # Test A: Successful CLASSIFIED root recording
    # ------------------------------------------------------------------
    def test_a_successful_classified_root_recording(self) -> None:
        state = self._hydrate_state()
        cmd = RecordResidualBankClassificationCommand(
            command_id="cmd-a-1",
            session_id=state.session_id,
            expected_state_revision=state.revision,
            expected_persistence_revision=state.persistence_revision,
            decision_id="dec-a-1",
            bank_item_id=f"staged:{self.staged_tx.uuid}",
            bank_account_id=str(self.bank_account.uuid),
            status=ResidualBankClassificationStatus.CLASSIFIED,
            account_code="6111",
            confidence=0.99,
            rationale="Office Depot supplies purchase",
            original_amount_units=1500000,
            residual_amount_units=1500000,
            direction=Direction.OUTFLOW,
            currency="USD",
            schema_version="v1",
            dag_id="dag-residual-bank",
            request_semantic_digest="digest-a-1",
            issued_at=datetime(2026, 8, 15, 12, 0, 0, tzinfo=timezone.utc),
        )

        res = self.engine.apply(state=state, command=cmd)
        self.assertTrue(res.applied)
        self.assertEqual(res.status, TransitionStatus.APPLIED)
        self.assertEqual(state.revision, 1)
        self.assertEqual(state.persistence_revision, 1)

        # Verify durable DB row
        db_dec = BookkeepingResidualBankClassificationDecision.objects.get(id="dec-a-1")
        self.assertEqual(db_dec.status, "CLASSIFIED")
        self.assertEqual(db_dec.account_code, "6111")
        self.assertEqual(getattr(db_dec, "account_id", None), self.expense_account.pk)
        self.assertAlmostEqual(float(db_dec.confidence or 0.0), 0.99)
        self.assertIsNone(db_dec.supersedes)

        # Verify state queries
        queries = BookkeepingQueries(state)
        latest = queries.latest_residual_bank_classification(f"staged:{self.staged_tx.uuid}")
        active = queries.active_residual_bank_classification(f"staged:{self.staged_tx.uuid}")
        self.assertIsNotNone(latest)
        self.assertIsNotNone(active)
        assert latest is not None
        assert active is not None
        self.assertEqual(latest.id, "dec-a-1")
        self.assertEqual(active.id, "dec-a-1")

        # Verify event
        events = state.events
        self.assertTrue(any(e.event_type == StateEventType.RESIDUAL_BANK_CLASSIFICATION_CREATED for e in events))

    # ------------------------------------------------------------------
    # Test B: Successful HOLD root recording
    # ------------------------------------------------------------------
    def test_b_successful_hold_root_recording(self) -> None:
        state = self._hydrate_state()
        cmd = RecordResidualBankClassificationCommand(
            command_id="cmd-b-1",
            session_id=state.session_id,
            expected_state_revision=state.revision,
            expected_persistence_revision=state.persistence_revision,
            decision_id="dec-b-1",
            bank_item_id=f"staged:{self.staged_tx2.uuid}",
            bank_account_id=str(self.bank_account.uuid),
            status=ResidualBankClassificationStatus.HOLD,
            hold_reason="Missing vendor tax ID and contract",
            required_evidence=("DOCUMENT_LINK",),
            confidence=0.85,
            original_amount_units=3000000,
            residual_amount_units=3000000,
            direction=Direction.OUTFLOW,
            currency="USD",
            schema_version="v1",
            dag_id="dag-residual-bank",
            request_semantic_digest="digest-b-1",
            issued_at=datetime(2026, 8, 15, 12, 0, 0, tzinfo=timezone.utc),
        )

        res = self.engine.apply(state=state, command=cmd)
        self.assertTrue(res.applied)

        db_dec = BookkeepingResidualBankClassificationDecision.objects.get(id="dec-b-1")
        self.assertEqual(db_dec.status, "HOLD")
        self.assertIsNone(db_dec.account)
        self.assertIsNone(db_dec.account_code)
        self.assertEqual(db_dec.hold_reason, "Missing vendor tax ID and contract")
        self.assertEqual(db_dec.required_evidence, ["DOCUMENT_LINK"])

        queries = BookkeepingQueries(state)
        active = queries.active_residual_bank_classification(f"staged:{self.staged_tx2.uuid}")
        self.assertIsNotNone(active)
        assert active is not None
        self.assertEqual(active.status, ResidualBankClassificationStatus.HOLD)

    # ------------------------------------------------------------------
    # Test C: Account outside default_coa rejected
    # ------------------------------------------------------------------
    def test_c_account_outside_default_coa_rejected(self) -> None:
        state = self._hydrate_state()
        cmd = RecordResidualBankClassificationCommand(
            command_id="cmd-c-1",
            session_id=state.session_id,
            expected_state_revision=state.revision,
            expected_persistence_revision=state.persistence_revision,
            decision_id="dec-c-1",
            bank_item_id=f"staged:{self.staged_tx.uuid}",
            bank_account_id=str(self.bank_account.uuid),
            status=ResidualBankClassificationStatus.CLASSIFIED,
            account_code="6999",  # on secondary_coa, not default_coa
            confidence=0.99,
            original_amount_units=1500000,
            residual_amount_units=1500000,
            direction=Direction.OUTFLOW,
            currency="USD",
            schema_version="v1",
            dag_id="dag-residual-bank",
            request_semantic_digest="digest-c-1",
            issued_at=datetime(2026, 8, 15, 12, 0, 0, tzinfo=timezone.utc),
        )

        res = self.engine.apply(state=state, command=cmd)
        self.assertTrue(res.rejected)
        assert res.rejection is not None
        self.assertEqual(res.rejection.code, RejectionCode.ACCOUNT_OUTSIDE_DEFAULT_COA)
        self.assertEqual(state.revision, 0)
        self.assertFalse(BookkeepingResidualBankClassificationDecision.objects.filter(id="dec-c-1").exists())

    # ------------------------------------------------------------------
    # Test D: Inactive account rejected
    # ------------------------------------------------------------------
    def test_d_inactive_account_rejected(self) -> None:
        state = self._hydrate_state()
        cmd = RecordResidualBankClassificationCommand(
            command_id="cmd-d-1",
            session_id=state.session_id,
            expected_state_revision=state.revision,
            expected_persistence_revision=state.persistence_revision,
            decision_id="dec-d-1",
            bank_item_id=f"staged:{self.staged_tx.uuid}",
            bank_account_id=str(self.bank_account.uuid),
            status=ResidualBankClassificationStatus.CLASSIFIED,
            account_code="6888",  # inactive on default_coa
            confidence=0.99,
            original_amount_units=1500000,
            residual_amount_units=1500000,
            direction=Direction.OUTFLOW,
            currency="USD",
            schema_version="v1",
            dag_id="dag-residual-bank",
            request_semantic_digest="digest-d-1",
            issued_at=datetime(2026, 8, 15, 12, 0, 0, tzinfo=timezone.utc),
        )

        res = self.engine.apply(state=state, command=cmd)
        self.assertTrue(res.rejected)
        assert res.rejection is not None
        self.assertEqual(res.rejection.code, RejectionCode.INACTIVE_ACCOUNT)

    # ------------------------------------------------------------------
    # Test E: CLASSIFIED confidence < 0.98 rejected
    # ------------------------------------------------------------------
    def test_e_confidence_below_threshold_rejected(self) -> None:
        state = self._hydrate_state()
        cmd = RecordResidualBankClassificationCommand(
            command_id="cmd-e-1",
            session_id=state.session_id,
            expected_state_revision=state.revision,
            expected_persistence_revision=state.persistence_revision,
            decision_id="dec-e-1",
            bank_item_id=f"staged:{self.staged_tx.uuid}",
            bank_account_id=str(self.bank_account.uuid),
            status=ResidualBankClassificationStatus.CLASSIFIED,
            account_code="6111",
            confidence=0.979,  # strictly below 0.98
            original_amount_units=1500000,
            residual_amount_units=1500000,
            direction=Direction.OUTFLOW,
            currency="USD",
            schema_version="v1",
            dag_id="dag-residual-bank",
            request_semantic_digest="digest-e-1",
            issued_at=datetime(2026, 8, 15, 12, 0, 0, tzinfo=timezone.utc),
        )

        res = self.engine.apply(state=state, command=cmd)
        self.assertTrue(res.rejected)
        assert res.rejection is not None
        self.assertEqual(res.rejection.code, RejectionCode.CONFIDENCE_BELOW_THRESHOLD)

    # ------------------------------------------------------------------
    # Test F: Bank item consumed by Stage 1 rejected
    # ------------------------------------------------------------------
    def test_f_bank_item_consumed_by_stage1_rejected(self) -> None:
        # Create executed payment application in DB
        pa = BookkeepingPaymentApplication.objects.create(
            id="pa-f-1",
            entity=self.entity,
            staged_transaction=self.staged_tx,
            total_amount_units=1500000,
            direction="OUTFLOW",
            currency="USD",
            session_id="session-prior",
            state_revision=0,
            created_at=datetime.now(timezone.utc),
        )
        ledger = LedgerModel.objects.create(
            name=f"Ledger-f-{uuid4().hex[:6]}",
            entity=self.entity,
            posted=True,
        )
        je = JournalEntryModel.objects.create(
            ledger=ledger,
            timestamp=datetime(2026, 8, 1, 12, 0, tzinfo=timezone.utc),
            posted=False,
        )
        tx = TransactionModel.objects.create(
            journal_entry=je,
            account=self.cash_account,
            amount=Decimal("150.00"),
            tx_type=DEBIT,
        )
        inv = InvoiceModel(
            cash_account=self.cash_account,
            prepaid_account=self.receivables_account,
            unearned_account=self.unearned_revenue_account,
            accrue=False,
        )
        _, inv = inv.configure(entity_slug=self.entity, user_model=self.user)
        inv.amount_due = Decimal("150.00")
        inv.amount_paid = Decimal("150.00")
        inv.date_draft = date(2026, 8, 1)
        inv.invoice_status = InvoiceModel.INVOICE_STATUS_APPROVED
        inv.customer = self.customer
        inv.clean()
        inv.save()

        BookkeepingPaymentApplicationAllocation.objects.create(
            payment_application=pa,
            invoice=inv,
            amount_units=1500000,
            cash_transaction=tx,
        )

        state = self._hydrate_state()
        queries = BookkeepingQueries(state)
        self.assertIn(f"staged:{self.staged_tx.uuid}", queries.executed_stage1_bank_item_ids())

        cmd = RecordResidualBankClassificationCommand(
            command_id="cmd-f-1",
            session_id=state.session_id,
            expected_state_revision=state.revision,
            expected_persistence_revision=state.persistence_revision,
            decision_id="dec-f-1",
            bank_item_id=f"staged:{self.staged_tx.uuid}",
            bank_account_id=str(self.bank_account.uuid),
            status=ResidualBankClassificationStatus.CLASSIFIED,
            account_code="6111",
            confidence=0.99,
            original_amount_units=1500000,
            residual_amount_units=1500000,
            direction=Direction.OUTFLOW,
            currency="USD",
            schema_version="v1",
            dag_id="dag-residual-bank",
            request_semantic_digest="digest-f-1",
            issued_at=datetime(2026, 8, 15, 12, 0, 0, tzinfo=timezone.utc),
        )

        res = self.engine.apply(state=state, command=cmd)
        self.assertTrue(res.rejected)
        assert res.rejection is not None
        self.assertEqual(res.rejection.code, RejectionCode.NOT_RESIDUAL_BANK_ITEM)

    # ------------------------------------------------------------------
    # Test G: Bank item fully reconciled by Stage 2 rejected
    # ------------------------------------------------------------------
    def test_g_bank_item_fully_reconciled_by_stage2_rejected(self) -> None:
        rec = BookkeepingReconciliation.objects.create(
            id="rec-g-1",
            entity=self.entity,
            state_revision_at_creation=0,
            created_at=datetime.now(timezone.utc),
        )
        BookkeepingReconciliationBankAllocation.objects.create(
            reconciliation=rec,
            staged_transaction=self.staged_tx,
            amount_units=1500000,
        )
        ledger = LedgerModel.objects.create(
            name=f"Ledger-g-{uuid4().hex[:6]}",
            entity=self.entity,
            posted=True,
        )
        je = JournalEntryModel.objects.create(
            ledger=ledger,
            timestamp=datetime(2026, 8, 1, 12, 0, tzinfo=timezone.utc),
            posted=False,
        )
        tx = TransactionModel.objects.create(
            journal_entry=je,
            account=self.cash_account,
            amount=Decimal("150.00"),
            tx_type=CREDIT,
        )
        je.posted = True
        je.save(update_fields=["posted"], verify=False)
        BookkeepingReconciliationBookAllocation.objects.create(
            reconciliation=rec,
            transaction=tx,
            amount_units=1500000,
        )

        state = self._hydrate_state()
        queries = BookkeepingQueries(state)
        self.assertEqual(queries.bank_remaining_units(f"staged:{self.staged_tx.uuid}"), 0)

        cmd = RecordResidualBankClassificationCommand(
            command_id="cmd-g-1",
            session_id=state.session_id,
            expected_state_revision=state.revision,
            expected_persistence_revision=state.persistence_revision,
            decision_id="dec-g-1",
            bank_item_id=f"staged:{self.staged_tx.uuid}",
            bank_account_id=str(self.bank_account.uuid),
            status=ResidualBankClassificationStatus.CLASSIFIED,
            account_code="6111",
            confidence=0.99,
            original_amount_units=1500000,
            residual_amount_units=1500000,
            direction=Direction.OUTFLOW,
            currency="USD",
            schema_version="v1",
            dag_id="dag-residual-bank",
            request_semantic_digest="digest-g-1",
            issued_at=datetime(2026, 8, 15, 12, 0, 0, tzinfo=timezone.utc),
        )

        res = self.engine.apply(state=state, command=cmd)
        self.assertTrue(res.rejected)
        assert res.rejection is not None
        self.assertEqual(res.rejection.code, RejectionCode.NOT_RESIDUAL_BANK_ITEM)

    # ------------------------------------------------------------------
    # Test H: Partial residual amount mismatch rejected
    # ------------------------------------------------------------------
    def test_h_partial_residual_amount_mismatch_rejected(self) -> None:
        state = self._hydrate_state()
        cmd = RecordResidualBankClassificationCommand(
            command_id="cmd-h-1",
            session_id=state.session_id,
            expected_state_revision=state.revision,
            expected_persistence_revision=state.persistence_revision,
            decision_id="dec-h-1",
            bank_item_id=f"staged:{self.staged_tx.uuid}",
            bank_account_id=str(self.bank_account.uuid),
            status=ResidualBankClassificationStatus.CLASSIFIED,
            account_code="6111",
            confidence=0.99,
            original_amount_units=1500000,
            residual_amount_units=1000000,  # mismatch with actual remaining 1500000
            direction=Direction.OUTFLOW,
            currency="USD",
            schema_version="v1",
            dag_id="dag-residual-bank",
            request_semantic_digest="digest-h-1",
            issued_at=datetime(2026, 8, 15, 12, 0, 0, tzinfo=timezone.utc),
        )

        res = self.engine.apply(state=state, command=cmd)
        self.assertTrue(res.rejected)
        assert res.rejection is not None
        self.assertEqual(res.rejection.code, RejectionCode.RESIDUAL_AMOUNT_MISMATCH)

    # ------------------------------------------------------------------
    # Test I: Bank account mismatch rejected
    # ------------------------------------------------------------------
    def test_i_bank_account_mismatch_rejected(self) -> None:
        ba2 = BankAccountModel.objects.create(
            entity_model=self.entity,
            account_model=self.cash_account,
            name="Secondary Account",
            active=True,
        )

        state = self._hydrate_state()
        cmd = RecordResidualBankClassificationCommand(
            command_id="cmd-i-1",
            session_id=state.session_id,
            expected_state_revision=state.revision,
            expected_persistence_revision=state.persistence_revision,
            decision_id="dec-i-1",
            bank_item_id=f"staged:{self.staged_tx.uuid}",
            bank_account_id=str(ba2.uuid),  # mismatch with staged_tx bank account
            status=ResidualBankClassificationStatus.CLASSIFIED,
            account_code="6111",
            confidence=0.99,
            original_amount_units=1500000,
            residual_amount_units=1500000,
            direction=Direction.OUTFLOW,
            currency="USD",
            schema_version="v1",
            dag_id="dag-residual-bank",
            request_semantic_digest="digest-i-1",
            issued_at=datetime(2026, 8, 15, 12, 0, 0, tzinfo=timezone.utc),
        )

        res = self.engine.apply(state=state, command=cmd)
        self.assertTrue(res.rejected)
        assert res.rejection is not None
        self.assertEqual(res.rejection.code, RejectionCode.BANK_ACCOUNT_MISMATCH)

    # ------------------------------------------------------------------
    # Test J: Original amount/direction/currency mismatch rejected
    # ------------------------------------------------------------------
    def test_j_economic_snapshot_mismatch_rejected(self) -> None:
        state = self._hydrate_state()

        # Direction mismatch
        cmd_dir = RecordResidualBankClassificationCommand(
            command_id="cmd-j-1",
            session_id=state.session_id,
            expected_state_revision=state.revision,
            expected_persistence_revision=state.persistence_revision,
            decision_id="dec-j-1",
            bank_item_id=f"staged:{self.staged_tx.uuid}",
            bank_account_id=str(self.bank_account.uuid),
            status=ResidualBankClassificationStatus.CLASSIFIED,
            account_code="6111",
            confidence=0.99,
            original_amount_units=1500000,
            residual_amount_units=1500000,
            direction=Direction.INFLOW,  # mismatch with OUTFLOW
            currency="USD",
            schema_version="v1",
            dag_id="dag-residual-bank",
            request_semantic_digest="digest-j-1",
            issued_at=datetime(2026, 8, 15, 12, 0, 0, tzinfo=timezone.utc),
        )
        res_dir = self.engine.apply(state=state, command=cmd_dir)
        self.assertTrue(res_dir.rejected)
        assert res_dir.rejection is not None
        self.assertEqual(res_dir.rejection.code, RejectionCode.ECONOMIC_INPUT_MISMATCH)

        # Currency mismatch
        cmd_curr = RecordResidualBankClassificationCommand(
            command_id="cmd-j-2",
            session_id=state.session_id,
            expected_state_revision=state.revision,
            expected_persistence_revision=state.persistence_revision,
            decision_id="dec-j-2",
            bank_item_id=f"staged:{self.staged_tx.uuid}",
            bank_account_id=str(self.bank_account.uuid),
            status=ResidualBankClassificationStatus.CLASSIFIED,
            account_code="6111",
            confidence=0.99,
            original_amount_units=1500000,
            residual_amount_units=1500000,
            direction=Direction.OUTFLOW,
            currency="EUR",  # mismatch with USD
            schema_version="v1",
            dag_id="dag-residual-bank",
            request_semantic_digest="digest-j-2",
            issued_at=datetime(2026, 8, 15, 12, 0, 0, tzinfo=timezone.utc),
        )
        res_curr = self.engine.apply(state=state, command=cmd_curr)
        self.assertTrue(res_curr.rejected)
        assert res_curr.rejection is not None
        self.assertEqual(res_curr.rejection.code, RejectionCode.ECONOMIC_INPUT_MISMATCH)

    # ------------------------------------------------------------------
    # Test K: Stale expected_state_revision rejected
    # ------------------------------------------------------------------
    def test_k_stale_expected_state_revision_rejected(self) -> None:
        state = self._hydrate_state()
        cmd = RecordResidualBankClassificationCommand(
            command_id="cmd-k-1",
            session_id=state.session_id,
            expected_state_revision=999,  # stale
            expected_persistence_revision=state.persistence_revision,
            decision_id="dec-k-1",
            bank_item_id=f"staged:{self.staged_tx.uuid}",
            bank_account_id=str(self.bank_account.uuid),
            status=ResidualBankClassificationStatus.CLASSIFIED,
            account_code="6111",
            confidence=0.99,
            original_amount_units=1500000,
            residual_amount_units=1500000,
            direction=Direction.OUTFLOW,
            currency="USD",
            schema_version="v1",
            dag_id="dag-residual-bank",
            request_semantic_digest="digest-k-1",
            issued_at=datetime(2026, 8, 15, 12, 0, 0, tzinfo=timezone.utc),
        )

        res = self.engine.apply(state=state, command=cmd)
        self.assertTrue(res.rejected)
        assert res.rejection is not None
        self.assertEqual(res.rejection.code, RejectionCode.STATE_REVISION_CONFLICT)

    # ------------------------------------------------------------------
    # Test L: Stale expected_persistence_revision rejected
    # ------------------------------------------------------------------
    def test_l_stale_expected_persistence_revision_rejected(self) -> None:
        state = self._hydrate_state()
        cmd = RecordResidualBankClassificationCommand(
            command_id="cmd-l-1",
            session_id=state.session_id,
            expected_state_revision=state.revision,
            expected_persistence_revision=999,  # stale
            decision_id="dec-l-1",
            bank_item_id=f"staged:{self.staged_tx.uuid}",
            bank_account_id=str(self.bank_account.uuid),
            status=ResidualBankClassificationStatus.CLASSIFIED,
            account_code="6111",
            confidence=0.99,
            original_amount_units=1500000,
            residual_amount_units=1500000,
            direction=Direction.OUTFLOW,
            currency="USD",
            schema_version="v1",
            dag_id="dag-residual-bank",
            request_semantic_digest="digest-l-1",
            issued_at=datetime(2026, 8, 15, 12, 0, 0, tzinfo=timezone.utc),
        )

        res = self.engine.apply(state=state, command=cmd)
        self.assertTrue(res.rejected)
        assert res.rejection is not None
        self.assertEqual(res.rejection.code, RejectionCode.PERSISTENCE_REVISION_CONFLICT)

    # ------------------------------------------------------------------
    # Test M: Exact replay => no-op, no extra row, no revision bump
    # ------------------------------------------------------------------
    def test_m_exact_replay_is_noop(self) -> None:
        state = self._hydrate_state()
        cmd = RecordResidualBankClassificationCommand(
            command_id="cmd-m-1",
            session_id=state.session_id,
            expected_state_revision=0,
            expected_persistence_revision=0,
            decision_id="dec-m-1",
            bank_item_id=f"staged:{self.staged_tx.uuid}",
            bank_account_id=str(self.bank_account.uuid),
            status=ResidualBankClassificationStatus.CLASSIFIED,
            account_code="6111",
            confidence=0.99,
            original_amount_units=1500000,
            residual_amount_units=1500000,
            direction=Direction.OUTFLOW,
            currency="USD",
            schema_version="v1",
            dag_id="dag-residual-bank",
            request_semantic_digest="digest-m-1",
            issued_at=datetime(2026, 8, 15, 12, 0, 0, tzinfo=timezone.utc),
        )

        res1 = self.engine.apply(state=state, command=cmd)
        self.assertTrue(res1.applied)
        self.assertEqual(state.revision, 1)
        self.assertEqual(state.persistence_revision, 1)
        self.assertEqual(BookkeepingResidualBankClassificationDecision.objects.count(), 1)

        # Exact replay with updated expected revisions
        replay_cmd = RecordResidualBankClassificationCommand(
            command_id="cmd-m-replay",
            session_id=state.session_id,
            expected_state_revision=1,
            expected_persistence_revision=1,
            decision_id="dec-m-1",
            bank_item_id=f"staged:{self.staged_tx.uuid}",
            bank_account_id=str(self.bank_account.uuid),
            status=ResidualBankClassificationStatus.CLASSIFIED,
            account_code="6111",
            confidence=0.99,
            original_amount_units=1500000,
            residual_amount_units=1500000,
            direction=Direction.OUTFLOW,
            currency="USD",
            schema_version="v1",
            dag_id="dag-residual-bank",
            request_semantic_digest="digest-m-1",
            issued_at=datetime(2026, 8, 15, 12, 5, 0, tzinfo=timezone.utc),
        )

        res2 = self.engine.apply(state=state, command=replay_cmd)
        self.assertTrue(res2.noop)
        self.assertEqual(res2.status, TransitionStatus.NOOP)
        self.assertEqual(state.revision, 1)
        self.assertEqual(state.persistence_revision, 1)
        self.assertEqual(BookkeepingResidualBankClassificationDecision.objects.count(), 1)

    # ------------------------------------------------------------------
    # Test N: Same digest but different outcome => requires supersession
    # ------------------------------------------------------------------
    def test_n_same_digest_different_outcome_requires_supersession(self) -> None:
        state = self._hydrate_state()
        cmd1 = RecordResidualBankClassificationCommand(
            command_id="cmd-n-1",
            session_id=state.session_id,
            expected_state_revision=0,
            expected_persistence_revision=0,
            decision_id="dec-n-1",
            bank_item_id=f"staged:{self.staged_tx.uuid}",
            bank_account_id=str(self.bank_account.uuid),
            status=ResidualBankClassificationStatus.HOLD,
            hold_reason="Need verification",
            original_amount_units=1500000,
            residual_amount_units=1500000,
            direction=Direction.OUTFLOW,
            currency="USD",
            schema_version="v1",
            dag_id="dag-residual-bank",
            request_semantic_digest="digest-same-n",
            issued_at=datetime(2026, 8, 15, 12, 0, 0, tzinfo=timezone.utc),
        )
        res1 = self.engine.apply(state=state, command=cmd1)
        self.assertTrue(res1.applied)

        # Same digest, but outcome changed to CLASSIFIED without supersession link
        cmd2 = RecordResidualBankClassificationCommand(
            command_id="cmd-n-2",
            session_id=state.session_id,
            expected_state_revision=1,
            expected_persistence_revision=1,
            decision_id="dec-n-2",
            bank_item_id=f"staged:{self.staged_tx.uuid}",
            bank_account_id=str(self.bank_account.uuid),
            status=ResidualBankClassificationStatus.CLASSIFIED,
            account_code="6111",
            confidence=0.99,
            original_amount_units=1500000,
            residual_amount_units=1500000,
            direction=Direction.OUTFLOW,
            currency="USD",
            schema_version="v1",
            dag_id="dag-residual-bank",
            request_semantic_digest="digest-same-n",
            supersedes_decision_id=None,  # missing explicit supersession
            issued_at=datetime(2026, 8, 15, 12, 5, 0, tzinfo=timezone.utc),
        )
        res2 = self.engine.apply(state=state, command=cmd2)
        self.assertTrue(res2.rejected)
        assert res2.rejection is not None
        self.assertEqual(res2.rejection.code, RejectionCode.BRANCHING_HISTORY_FORBIDDEN)

    # ------------------------------------------------------------------
    # Test O: Superseding latest active HOLD -> CLASSIFIED succeeds
    # ------------------------------------------------------------------
    def test_o_superseding_hold_to_classified(self) -> None:
        state = self._hydrate_state()
        cmd1 = RecordResidualBankClassificationCommand(
            command_id="cmd-o-1",
            session_id=state.session_id,
            expected_state_revision=0,
            expected_persistence_revision=0,
            decision_id="dec-o-1",
            bank_item_id=f"staged:{self.staged_tx.uuid}",
            bank_account_id=str(self.bank_account.uuid),
            status=ResidualBankClassificationStatus.HOLD,
            hold_reason="Reviewing invoice",
            original_amount_units=1500000,
            residual_amount_units=1500000,
            direction=Direction.OUTFLOW,
            currency="USD",
            schema_version="v1",
            dag_id="dag-residual-bank",
            request_semantic_digest="digest-o-1",
            issued_at=datetime(2026, 8, 15, 12, 0, 0, tzinfo=timezone.utc),
        )
        self.assertTrue(self.engine.apply(state=state, command=cmd1).applied)

        cmd2 = RecordResidualBankClassificationCommand(
            command_id="cmd-o-2",
            session_id=state.session_id,
            expected_state_revision=1,
            expected_persistence_revision=1,
            decision_id="dec-o-2",
            bank_item_id=f"staged:{self.staged_tx.uuid}",
            bank_account_id=str(self.bank_account.uuid),
            status=ResidualBankClassificationStatus.CLASSIFIED,
            account_code="6111",
            confidence=0.99,
            original_amount_units=1500000,
            residual_amount_units=1500000,
            direction=Direction.OUTFLOW,
            currency="USD",
            schema_version="v1",
            dag_id="dag-residual-bank",
            request_semantic_digest="digest-o-2",
            supersedes_decision_id="dec-o-1",
            issued_at=datetime(2026, 8, 15, 12, 10, 0, tzinfo=timezone.utc),
        )
        res2 = self.engine.apply(state=state, command=cmd2)
        self.assertTrue(res2.applied)

        db_dec2 = BookkeepingResidualBankClassificationDecision.objects.get(id="dec-o-2")
        self.assertEqual(db_dec2.supersedes_id, "dec-o-1")

        queries = BookkeepingQueries(state)
        latest_o = queries.latest_residual_bank_classification(f"staged:{self.staged_tx.uuid}")
        active_o = queries.active_residual_bank_classification(f"staged:{self.staged_tx.uuid}")
        assert latest_o is not None
        assert active_o is not None
        self.assertEqual(latest_o.id, "dec-o-2")
        self.assertEqual(active_o.id, "dec-o-2")

        events = state.events
        self.assertTrue(any(e.event_type == StateEventType.RESIDUAL_BANK_CLASSIFICATION_SUPERSEDED for e in events))

    # ------------------------------------------------------------------
    # Test P: Superseding latest CLASSIFIED -> new CLASSIFIED succeeds
    # ------------------------------------------------------------------
    def test_p_superseding_classified_to_new_classified(self) -> None:
        state = self._hydrate_state()
        cmd1 = RecordResidualBankClassificationCommand(
            command_id="cmd-p-1",
            session_id=state.session_id,
            expected_state_revision=0,
            expected_persistence_revision=0,
            decision_id="dec-p-1",
            bank_item_id=f"staged:{self.staged_tx.uuid}",
            bank_account_id=str(self.bank_account.uuid),
            status=ResidualBankClassificationStatus.CLASSIFIED,
            account_code="6111",
            confidence=0.99,
            original_amount_units=1500000,
            residual_amount_units=1500000,
            direction=Direction.OUTFLOW,
            currency="USD",
            schema_version="v1",
            dag_id="dag-residual-bank",
            request_semantic_digest="digest-p-1",
            issued_at=datetime(2026, 8, 15, 12, 0, 0, tzinfo=timezone.utc),
        )
        self.assertTrue(self.engine.apply(state=state, command=cmd1).applied)

        cmd2 = RecordResidualBankClassificationCommand(
            command_id="cmd-p-2",
            session_id=state.session_id,
            expected_state_revision=1,
            expected_persistence_revision=1,
            decision_id="dec-p-2",
            bank_item_id=f"staged:{self.staged_tx.uuid}",
            bank_account_id=str(self.bank_account.uuid),
            status=ResidualBankClassificationStatus.CLASSIFIED,
            account_code="6222",  # new account
            confidence=0.99,
            original_amount_units=1500000,
            residual_amount_units=1500000,
            direction=Direction.OUTFLOW,
            currency="USD",
            schema_version="v1",
            dag_id="dag-residual-bank",
            request_semantic_digest="digest-p-2",
            supersedes_decision_id="dec-p-1",
            issued_at=datetime(2026, 8, 15, 12, 10, 0, tzinfo=timezone.utc),
        )
        res2 = self.engine.apply(state=state, command=cmd2)
        self.assertTrue(res2.applied)

        queries = BookkeepingQueries(state)
        active = queries.active_residual_bank_classification(f"staged:{self.staged_tx.uuid}")
        assert active is not None
        self.assertEqual(active.id, "dec-p-2")
        self.assertEqual(active.account_code, "6222")

    # ------------------------------------------------------------------
    # Test Q: Successor after invalidated tip succeeds
    # ------------------------------------------------------------------
    def test_q_successor_after_invalidated_tip(self) -> None:
        state = self._hydrate_state()
        cmd1 = RecordResidualBankClassificationCommand(
            command_id="cmd-q-1",
            session_id=state.session_id,
            expected_state_revision=0,
            expected_persistence_revision=0,
            decision_id="dec-q-1",
            bank_item_id=f"staged:{self.staged_tx.uuid}",
            bank_account_id=str(self.bank_account.uuid),
            status=ResidualBankClassificationStatus.CLASSIFIED,
            account_code="6111",
            confidence=0.99,
            original_amount_units=1500000,
            residual_amount_units=1500000,
            direction=Direction.OUTFLOW,
            currency="USD",
            schema_version="v1",
            dag_id="dag-residual-bank",
            request_semantic_digest="digest-q-1",
            issued_at=datetime(2026, 8, 15, 12, 0, 0, tzinfo=timezone.utc),
        )
        self.assertTrue(self.engine.apply(state=state, command=cmd1).applied)

        # Invalidate dec-q-1
        inval_cmd = InvalidateResidualBankClassificationCommand(
            command_id="cmd-q-inv",
            session_id=state.session_id,
            expected_state_revision=1,
            expected_persistence_revision=1,
            invalidation_id="inv-q-1",
            decision_id="dec-q-1",
            reason="Wrong category attributed by model",
            issued_at=datetime(2026, 8, 15, 12, 5, 0, tzinfo=timezone.utc),
        )
        self.assertTrue(self.engine.apply(state=state, command=inval_cmd).applied)

        queries = BookkeepingQueries(state)
        self.assertIsNone(queries.active_residual_bank_classification(f"staged:{self.staged_tx.uuid}"))
        latest_q = queries.latest_residual_bank_classification(f"staged:{self.staged_tx.uuid}")
        assert latest_q is not None
        self.assertEqual(latest_q.id, "dec-q-1")

        # Now append successor dec-q-2 superseding dec-q-1
        cmd2 = RecordResidualBankClassificationCommand(
            command_id="cmd-q-2",
            session_id=state.session_id,
            expected_state_revision=2,
            expected_persistence_revision=2,
            decision_id="dec-q-2",
            bank_item_id=f"staged:{self.staged_tx.uuid}",
            bank_account_id=str(self.bank_account.uuid),
            status=ResidualBankClassificationStatus.CLASSIFIED,
            account_code="6222",
            confidence=0.99,
            original_amount_units=1500000,
            residual_amount_units=1500000,
            direction=Direction.OUTFLOW,
            currency="USD",
            schema_version="v1",
            dag_id="dag-residual-bank",
            request_semantic_digest="digest-q-2",
            supersedes_decision_id="dec-q-1",
            issued_at=datetime(2026, 8, 15, 12, 10, 0, tzinfo=timezone.utc),
        )
        res2 = self.engine.apply(state=state, command=cmd2)
        self.assertTrue(res2.applied)

        queries = BookkeepingQueries(state)
        active = queries.active_residual_bank_classification(f"staged:{self.staged_tx.uuid}")
        self.assertIsNotNone(active)
        assert active is not None
        self.assertEqual(active.id, "dec-q-2")
        self.assertEqual(active.account_code, "6222")

    # ------------------------------------------------------------------
    # Test R: Appending to non-tip historical decision rejected
    # ------------------------------------------------------------------
    def test_r_appending_to_non_tip_historical_decision_rejected(self) -> None:
        state = self._hydrate_state()
        cmd1 = RecordResidualBankClassificationCommand(
            command_id="cmd-r-1",
            session_id=state.session_id,
            expected_state_revision=0,
            expected_persistence_revision=0,
            decision_id="dec-r-1",
            bank_item_id=f"staged:{self.staged_tx.uuid}",
            bank_account_id=str(self.bank_account.uuid),
            status=ResidualBankClassificationStatus.CLASSIFIED,
            account_code="6111",
            confidence=0.99,
            original_amount_units=1500000,
            residual_amount_units=1500000,
            direction=Direction.OUTFLOW,
            currency="USD",
            schema_version="v1",
            dag_id="dag-residual-bank",
            request_semantic_digest="digest-r-1",
            issued_at=datetime(2026, 8, 15, 12, 0, 0, tzinfo=timezone.utc),
        )
        self.assertTrue(self.engine.apply(state=state, command=cmd1).applied)

        cmd2 = RecordResidualBankClassificationCommand(
            command_id="cmd-r-2",
            session_id=state.session_id,
            expected_state_revision=1,
            expected_persistence_revision=1,
            decision_id="dec-r-2",
            bank_item_id=f"staged:{self.staged_tx.uuid}",
            bank_account_id=str(self.bank_account.uuid),
            status=ResidualBankClassificationStatus.CLASSIFIED,
            account_code="6222",
            confidence=0.99,
            original_amount_units=1500000,
            residual_amount_units=1500000,
            direction=Direction.OUTFLOW,
            currency="USD",
            schema_version="v1",
            dag_id="dag-residual-bank",
            request_semantic_digest="digest-r-2",
            supersedes_decision_id="dec-r-1",
            issued_at=datetime(2026, 8, 15, 12, 10, 0, tzinfo=timezone.utc),
        )
        self.assertTrue(self.engine.apply(state=state, command=cmd2).applied)

        # Now try to supersede dec-r-1 (which is non-tip, because dec-r-2 is latest)
        cmd3 = RecordResidualBankClassificationCommand(
            command_id="cmd-r-3",
            session_id=state.session_id,
            expected_state_revision=2,
            expected_persistence_revision=2,
            decision_id="dec-r-3",
            bank_item_id=f"staged:{self.staged_tx.uuid}",
            bank_account_id=str(self.bank_account.uuid),
            status=ResidualBankClassificationStatus.CLASSIFIED,
            account_code="6111",
            confidence=0.99,
            original_amount_units=1500000,
            residual_amount_units=1500000,
            direction=Direction.OUTFLOW,
            currency="USD",
            schema_version="v1",
            dag_id="dag-residual-bank",
            request_semantic_digest="digest-r-3",
            supersedes_decision_id="dec-r-1",
            issued_at=datetime(2026, 8, 15, 12, 20, 0, tzinfo=timezone.utc),
        )
        res3 = self.engine.apply(state=state, command=cmd3)
        self.assertTrue(res3.rejected)
        assert res3.rejection is not None
        self.assertEqual(res3.rejection.code, RejectionCode.BRANCHING_HISTORY_FORBIDDEN)

    # ------------------------------------------------------------------
    # Test S: Concurrent/branching race safely rejected
    # ------------------------------------------------------------------
    def test_s_concurrent_branching_race_rejected(self) -> None:
        state = self._hydrate_state()
        cmd1 = RecordResidualBankClassificationCommand(
            command_id="cmd-s-1",
            session_id=state.session_id,
            expected_state_revision=0,
            expected_persistence_revision=0,
            decision_id="dec-s-1",
            bank_item_id=f"staged:{self.staged_tx.uuid}",
            bank_account_id=str(self.bank_account.uuid),
            status=ResidualBankClassificationStatus.HOLD,
            hold_reason="Hold 1",
            original_amount_units=1500000,
            residual_amount_units=1500000,
            direction=Direction.OUTFLOW,
            currency="USD",
            schema_version="v1",
            dag_id="dag-residual-bank",
            request_semantic_digest="digest-s-1",
            issued_at=datetime(2026, 8, 15, 12, 0, 0, tzinfo=timezone.utc),
        )
        self.assertTrue(self.engine.apply(state=state, command=cmd1).applied)

        # Sibling branch prepared against revision 0
        stale_cmd = RecordResidualBankClassificationCommand(
            command_id="cmd-s-stale",
            session_id=state.session_id,
            expected_state_revision=0,  # stale
            expected_persistence_revision=0,
            decision_id="dec-s-stale",
            bank_item_id=f"staged:{self.staged_tx.uuid}",
            bank_account_id=str(self.bank_account.uuid),
            status=ResidualBankClassificationStatus.HOLD,
            hold_reason="Hold 2",
            original_amount_units=1500000,
            residual_amount_units=1500000,
            direction=Direction.OUTFLOW,
            currency="USD",
            schema_version="v1",
            dag_id="dag-residual-bank",
            request_semantic_digest="digest-s-stale",
            issued_at=datetime(2026, 8, 15, 12, 5, 0, tzinfo=timezone.utc),
        )
        res_stale = self.engine.apply(state=state, command=stale_cmd)
        self.assertTrue(res_stale.rejected)
        assert res_stale.rejection is not None
        self.assertEqual(res_stale.rejection.code, RejectionCode.STATE_REVISION_CONFLICT)

    # ------------------------------------------------------------------
    # Test T: Successful invalidation of current tip
    # ------------------------------------------------------------------
    def test_t_successful_invalidation_of_current_tip(self) -> None:
        state = self._hydrate_state()
        cmd = RecordResidualBankClassificationCommand(
            command_id="cmd-t-1",
            session_id=state.session_id,
            expected_state_revision=0,
            expected_persistence_revision=0,
            decision_id="dec-t-1",
            bank_item_id=f"staged:{self.staged_tx.uuid}",
            bank_account_id=str(self.bank_account.uuid),
            status=ResidualBankClassificationStatus.CLASSIFIED,
            account_code="6111",
            confidence=0.99,
            original_amount_units=1500000,
            residual_amount_units=1500000,
            direction=Direction.OUTFLOW,
            currency="USD",
            schema_version="v1",
            dag_id="dag-residual-bank",
            request_semantic_digest="digest-t-1",
            issued_at=datetime(2026, 8, 15, 12, 0, 0, tzinfo=timezone.utc),
        )
        self.assertTrue(self.engine.apply(state=state, command=cmd).applied)

        inval_cmd = InvalidateResidualBankClassificationCommand(
            command_id="cmd-t-inv",
            session_id=state.session_id,
            expected_state_revision=1,
            expected_persistence_revision=1,
            invalidation_id="inv-t-1",
            decision_id="dec-t-1",
            reason="Auditor determined incorrect expense category",
            issued_at=datetime(2026, 8, 15, 12, 5, 0, tzinfo=timezone.utc),
        )
        res = self.engine.apply(state=state, command=inval_cmd)
        self.assertTrue(res.applied)
        self.assertEqual(state.revision, 2)
        self.assertEqual(state.persistence_revision, 2)

        db_inv = BookkeepingResidualBankClassificationInvalidation.objects.get(id="inv-t-1")
        self.assertEqual(db_inv.classification_id, "dec-t-1")
        self.assertEqual(db_inv.reason, "Auditor determined incorrect expense category")

        queries = BookkeepingQueries(state)
        self.assertIsNone(queries.active_residual_bank_classification(f"staged:{self.staged_tx.uuid}"))
        latest_t = queries.latest_residual_bank_classification(f"staged:{self.staged_tx.uuid}")
        assert latest_t is not None
        self.assertEqual(latest_t.id, "dec-t-1")

        events = state.events
        self.assertTrue(any(e.event_type == StateEventType.RESIDUAL_BANK_CLASSIFICATION_INVALIDATED for e in events))

    # ------------------------------------------------------------------
    # Test U: Invalidating old non-tip rejected
    # ------------------------------------------------------------------
    def test_u_invalidating_old_non_tip_rejected(self) -> None:
        state = self._hydrate_state()
        cmd1 = RecordResidualBankClassificationCommand(
            command_id="cmd-u-1",
            session_id=state.session_id,
            expected_state_revision=0,
            expected_persistence_revision=0,
            decision_id="dec-u-1",
            bank_item_id=f"staged:{self.staged_tx.uuid}",
            bank_account_id=str(self.bank_account.uuid),
            status=ResidualBankClassificationStatus.CLASSIFIED,
            account_code="6111",
            confidence=0.99,
            original_amount_units=1500000,
            residual_amount_units=1500000,
            direction=Direction.OUTFLOW,
            currency="USD",
            schema_version="v1",
            dag_id="dag-residual-bank",
            request_semantic_digest="digest-u-1",
            issued_at=datetime(2026, 8, 15, 12, 0, 0, tzinfo=timezone.utc),
        )
        self.assertTrue(self.engine.apply(state=state, command=cmd1).applied)

        cmd2 = RecordResidualBankClassificationCommand(
            command_id="cmd-u-2",
            session_id=state.session_id,
            expected_state_revision=1,
            expected_persistence_revision=1,
            decision_id="dec-u-2",
            bank_item_id=f"staged:{self.staged_tx.uuid}",
            bank_account_id=str(self.bank_account.uuid),
            status=ResidualBankClassificationStatus.CLASSIFIED,
            account_code="6222",
            confidence=0.99,
            original_amount_units=1500000,
            residual_amount_units=1500000,
            direction=Direction.OUTFLOW,
            currency="USD",
            schema_version="v1",
            dag_id="dag-residual-bank",
            request_semantic_digest="digest-u-2",
            supersedes_decision_id="dec-u-1",
            issued_at=datetime(2026, 8, 15, 12, 5, 0, tzinfo=timezone.utc),
        )
        self.assertTrue(self.engine.apply(state=state, command=cmd2).applied)

        # Attempt to invalidate dec-u-1 (non-tip)
        inval_cmd = InvalidateResidualBankClassificationCommand(
            command_id="cmd-u-inv",
            session_id=state.session_id,
            expected_state_revision=2,
            expected_persistence_revision=2,
            invalidation_id="inv-u-1",
            decision_id="dec-u-1",
            reason="Invalidate old root",
            issued_at=datetime(2026, 8, 15, 12, 10, 0, tzinfo=timezone.utc),
        )
        res = self.engine.apply(state=state, command=inval_cmd)
        self.assertTrue(res.rejected)
        assert res.rejection is not None
        self.assertEqual(res.rejection.code, RejectionCode.CANNOT_INVALIDATE_NON_TIP)

    # ------------------------------------------------------------------
    # Test V: Duplicate invalidation exact replay no-op
    # ------------------------------------------------------------------
    def test_v_duplicate_invalidation_exact_replay_noop(self) -> None:
        state = self._hydrate_state()
        cmd = RecordResidualBankClassificationCommand(
            command_id="cmd-v-1",
            session_id=state.session_id,
            expected_state_revision=0,
            expected_persistence_revision=0,
            decision_id="dec-v-1",
            bank_item_id=f"staged:{self.staged_tx.uuid}",
            bank_account_id=str(self.bank_account.uuid),
            status=ResidualBankClassificationStatus.CLASSIFIED,
            account_code="6111",
            confidence=0.99,
            original_amount_units=1500000,
            residual_amount_units=1500000,
            direction=Direction.OUTFLOW,
            currency="USD",
            schema_version="v1",
            dag_id="dag-residual-bank",
            request_semantic_digest="digest-v-1",
            issued_at=datetime(2026, 8, 15, 12, 0, 0, tzinfo=timezone.utc),
        )
        self.assertTrue(self.engine.apply(state=state, command=cmd).applied)

        inval_cmd1 = InvalidateResidualBankClassificationCommand(
            command_id="cmd-v-inv1",
            session_id=state.session_id,
            expected_state_revision=1,
            expected_persistence_revision=1,
            invalidation_id="inv-v-1",
            decision_id="dec-v-1",
            reason="Auditor review",
            issued_at=datetime(2026, 8, 15, 12, 5, 0, tzinfo=timezone.utc),
        )
        res1 = self.engine.apply(state=state, command=inval_cmd1)
        self.assertTrue(res1.applied)
        self.assertEqual(BookkeepingResidualBankClassificationInvalidation.objects.count(), 1)

        # Duplicate invalidation with exact same reason
        inval_cmd2 = InvalidateResidualBankClassificationCommand(
            command_id="cmd-v-inv2",
            session_id=state.session_id,
            expected_state_revision=2,
            expected_persistence_revision=2,
            invalidation_id="inv-v-1",
            decision_id="dec-v-1",
            reason="Auditor review",
            issued_at=datetime(2026, 8, 15, 12, 10, 0, tzinfo=timezone.utc),
        )
        res2 = self.engine.apply(state=state, command=inval_cmd2)
        self.assertTrue(res2.noop)
        self.assertEqual(res2.status, TransitionStatus.NOOP)
        self.assertEqual(state.revision, 2)
        self.assertEqual(BookkeepingResidualBankClassificationInvalidation.objects.count(), 1)

    # ------------------------------------------------------------------
    # Test W: Invalidated decision never becomes active again
    # ------------------------------------------------------------------
    def test_w_invalidated_decision_never_becomes_active_again(self) -> None:
        state = self._hydrate_state()
        cmd = RecordResidualBankClassificationCommand(
            command_id="cmd-w-1",
            session_id=state.session_id,
            expected_state_revision=0,
            expected_persistence_revision=0,
            decision_id="dec-w-1",
            bank_item_id=f"staged:{self.staged_tx.uuid}",
            bank_account_id=str(self.bank_account.uuid),
            status=ResidualBankClassificationStatus.CLASSIFIED,
            account_code="6111",
            confidence=0.99,
            original_amount_units=1500000,
            residual_amount_units=1500000,
            direction=Direction.OUTFLOW,
            currency="USD",
            schema_version="v1",
            dag_id="dag-residual-bank",
            request_semantic_digest="digest-w-1",
            issued_at=datetime(2026, 8, 15, 12, 0, 0, tzinfo=timezone.utc),
        )
        self.assertTrue(self.engine.apply(state=state, command=cmd).applied)

        inval_cmd = InvalidateResidualBankClassificationCommand(
            command_id="cmd-w-inv",
            session_id=state.session_id,
            expected_state_revision=1,
            expected_persistence_revision=1,
            invalidation_id="inv-w-1",
            decision_id="dec-w-1",
            reason="Retired",
            issued_at=datetime(2026, 8, 15, 12, 5, 0, tzinfo=timezone.utc),
        )
        self.assertTrue(self.engine.apply(state=state, command=inval_cmd).applied)

        # Hydrate a fresh state
        fresh_state = self._hydrate_state(session_id="fresh-session-w")
        try:
            queries = BookkeepingQueries(fresh_state)
            self.assertIsNone(queries.active_residual_bank_classification(f"staged:{self.staged_tx.uuid}"))
            latest = queries.latest_residual_bank_classification(f"staged:{self.staged_tx.uuid}")
            self.assertIsNotNone(latest)
            assert latest is not None
            self.assertEqual(latest.id, "dec-w-1")
        finally:
            fresh_state.close()

    # ------------------------------------------------------------------
    # Test X: Candidate-state validation occurs before persistence
    # ------------------------------------------------------------------
    def test_x_candidate_state_validation_occurs_before_persistence(self) -> None:
        state = self._hydrate_state()
        cmd = RecordResidualBankClassificationCommand(
            command_id="cmd-x-1",
            session_id=state.session_id,
            expected_state_revision=0,
            expected_persistence_revision=0,
            decision_id="dec-x-1",
            bank_item_id=f"staged:{self.staged_tx.uuid}",
            bank_account_id=str(self.bank_account.uuid),
            status=ResidualBankClassificationStatus.CLASSIFIED,
            account_code="6111",
            confidence=0.99,
            original_amount_units=1500000,
            residual_amount_units=1500000,
            direction=Direction.OUTFLOW,
            currency="USD",
            schema_version="v1",
            dag_id="dag-residual-bank",
            request_semantic_digest="digest-x-1",
            issued_at=datetime(2026, 8, 15, 12, 0, 0, tzinfo=timezone.utc),
        )

        from bookkeeping_state.state.validation import (
            StateValidationReport,
            ValidationCode,
            ValidationIssue,
        )

        # Mock validate_state to simulate resulting-state validation failure
        bad_report = StateValidationReport(
            state_revision=1,
            issues=(
                ValidationIssue(
                    code=ValidationCode.UNKNOWN_BANK_ITEM,
                    message="Simulated validation error",
                ),
            ),
        )

        with patch("bookkeeping_state.transitions.engine.validate_state", return_value=bad_report):
            res = self.engine.apply(state=state, command=cmd)
            self.assertTrue(res.rejected)
            assert res.rejection is not None
            self.assertEqual(res.rejection.code, RejectionCode.RESULTING_STATE_INVALID)
            # Confirm no DB row created
            self.assertFalse(BookkeepingResidualBankClassificationDecision.objects.filter(id="dec-x-1").exists())

    # ------------------------------------------------------------------
    # Test Y: Post-commit live-state failure closes state and requires rehydrate
    # ------------------------------------------------------------------
    def test_y_post_commit_live_state_failure_closes_state(self) -> None:
        state = self._hydrate_state()
        cmd = RecordResidualBankClassificationCommand(
            command_id="cmd-y-1",
            session_id=state.session_id,
            expected_state_revision=0,
            expected_persistence_revision=0,
            decision_id="dec-y-1",
            bank_item_id=f"staged:{self.staged_tx.uuid}",
            bank_account_id=str(self.bank_account.uuid),
            status=ResidualBankClassificationStatus.CLASSIFIED,
            account_code="6111",
            confidence=0.99,
            original_amount_units=1500000,
            residual_amount_units=1500000,
            direction=Direction.OUTFLOW,
            currency="USD",
            schema_version="v1",
            dag_id="dag-residual-bank",
            request_semantic_digest="digest-y-1",
            issued_at=datetime(2026, 8, 15, 12, 0, 0, tzinfo=timezone.utc),
        )

        with (
            patch.object(TransitionEngine, "_apply_delta", side_effect=RuntimeError("Simulated memory sync crash")),
            self.assertRaises(CommittedStateApplicationError),
        ):
            self.engine.apply(state=state, command=cmd)

        self.assertTrue(state.is_closed)

    # ------------------------------------------------------------------
    # Test Z: Legacy classification/routing/reconciliation transitions unaffected
    # ------------------------------------------------------------------
    def test_z_legacy_transitions_unaffected(self) -> None:
        inv = InvoiceModel(
            cash_account=self.cash_account,
            prepaid_account=self.receivables_account,
            unearned_account=self.unearned_revenue_account,
            accrue=False,
        )
        _, inv = inv.configure(entity_slug=self.entity, user_model=self.user)
        inv.amount_due = Decimal("250.00")
        inv.amount_paid = Decimal("0.00")
        inv.date_draft = date(2026, 8, 1)
        inv.invoice_status = InvoiceModel.INVOICE_STATUS_APPROVED
        inv.customer = self.customer
        inv.clean()
        inv.save()

        state = self._hydrate_state()

        # Legacy routing command
        route_cmd = CreateRoutingDecisionCommand(
            command_id="cmd-z-route",
            session_id=state.session_id,
            expected_state_revision=state.revision,
            source=CommandSource.ROUTING,
            decision_source=RoutingDecisionSource.CP_SAT,
            book_item_id=f"invoice:{inv.uuid}",
            bank_account_id=str(self.bank_account.uuid),
            routing_decision_id="route-z-1",
            utility=950,
            issued_at=datetime(2026, 8, 15, 12, 0, 0, tzinfo=timezone.utc),
        )
        res_route = self.engine.apply(state=state, command=route_cmd)
        self.assertTrue(res_route.applied)

        # Legacy classification command
        class_cmd = CreateClassificationCommand(
            command_id="cmd-z-class",
            session_id=state.session_id,
            expected_state_revision=state.revision,
            source=CommandSource.ASE_DAG,
            classification_source=ClassificationSource.ASE_DAG,
            book_item_id=f"invoice:{inv.uuid}",
            account_code="6111",
            classification_id="class-z-1",
            confidence=0.99,
            issued_at=datetime(2026, 8, 15, 12, 5, 0, tzinfo=timezone.utc),
        )
        res_class = self.engine.apply(state=state, command=class_cmd)
        self.assertTrue(res_class.applied)
