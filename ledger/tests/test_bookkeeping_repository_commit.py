from __future__ import annotations

import concurrent.futures
from datetime import date, datetime, timezone as dt_timezone
from decimal import Decimal
import uuid

from django.contrib.auth import get_user_model
from django.db import connection
from django.test import TestCase, TransactionTestCase

from bookkeeping_state.domain.enums import (
    AllocationSupport,
    Direction,
    SemanticAdmissibility,
    SourceType,
)
from bookkeeping_state.domain.evidence import BookItemEvidenceType, EvidenceSource
from bookkeeping_state.domain.routing import (
    RoutingDecision,
    RoutingDecisionInvalidation,
    RoutingDecisionSource,
)
from bookkeeping_state.domain.classifications import (
    ClassificationDecision,
    ClassificationInvalidation,
    ClassificationSource,
)
from bookkeeping_state.domain.evidence import (
    BookItemEvidenceAssertion,
    BookItemEvidenceInvalidation,
)
from bookkeeping_state.domain.reconciliations import (
    BankAllocation,
    BookAllocation,
    Reconciliation,
    ReconciliationInvalidation,
)
from bookkeeping_state.hydration import BookkeepingHydrator
from bookkeeping_state.persistence.repository import (
    BookkeepingRepository,
    PersistenceConflictError,
    PersistenceError,
    PersistenceWriteSet,
)
from bookkeeping_state.state.queries import BookkeepingQueries
from ledger.io.roles import (
    ASSET_CA_CASH,
    ASSET_CA_PREPAID,
    ASSET_CA_RECEIVABLES,
    CREDIT,
    DEBIT,
    INCOME_OPERATIONAL,
    LIABILITY_CL_ACC_PAYABLE,
    LIABILITY_CL_DEFERRED_REVENUE,
)
from ledger.models import (
    AccountModel,
    BankAccountModel,
    BillModel,
    CustomerModel,
    EntityModel,
    ImportJobModel,
    InvoiceModel,
    JournalEntryModel,
    LedgerModel,
    StagedTransactionModel,
    TransactionModel,
    VendorModel,
)
from ledger.models.bookkeeping import (
    BookkeepingClassificationDecision,
    BookkeepingEvidenceAssertion,
    BookkeepingRevision,
    BookkeepingRoutingDecision,
)

UserModel = get_user_model()


class BookkeepingRepositoryCommitTests(TestCase):
    """
    Comprehensive tests proving production BookkeepingRepository.commit()
    for durable bookkeeping decision truth only.
    """

    def setUp(self) -> None:
        super().setUp()

        self.user = UserModel.objects.create_user(
            username="commit_admin",
            email="admin@committest.com",
            password="secure_password_123",
        )

        self.entity = EntityModel.add_root(
            name="Commit Testing Corp",
            admin=self.user,
            currency="USD",
            fy_start_month=1,
            accrual_method=False,
        )

        self.coa = self.entity.create_chart_of_accounts(
            coa_name="Commit CoA",
            assign_as_default=True,
            commit=True,
        )
        asset_root = self.coa.accountmodel_set.get(code="01000000")

        self.cash_account = asset_root.add_child(
            coa_model=self.coa,
            code="1010",
            name="Operating Checking",
            role=ASSET_CA_CASH,
            balance_type=DEBIT,
        )

        self.receivables_account = asset_root.add_child(
            coa_model=self.coa,
            code="1020",
            name="Accounts Receivable",
            role=ASSET_CA_RECEIVABLES,
            balance_type=DEBIT,
        )

        self.prepaid_account = asset_root.add_child(
            coa_model=self.coa,
            code="1030",
            name="Prepaid Expenses",
            role=ASSET_CA_PREPAID,
            balance_type=DEBIT,
        )

        liability_root = self.coa.accountmodel_set.get(code="02000000")
        self.unearned_revenue_account = liability_root.add_child(
            coa_model=self.coa,
            code="2010",
            name="Deferred Revenue",
            role=LIABILITY_CL_DEFERRED_REVENUE,
            balance_type=CREDIT,
        )

        self.payable_account = liability_root.add_child(
            coa_model=self.coa,
            code="2020",
            name="Accounts Payable",
            role=LIABILITY_CL_ACC_PAYABLE,
            balance_type=CREDIT,
        )

        income_root = self.coa.accountmodel_set.get(code="04000000")
        self.revenue_account = income_root.add_child(
            coa_model=self.coa,
            code="4010",
            name="Operational Revenue",
            role=INCOME_OPERATIONAL,
            balance_type=CREDIT,
        )

        self.bank_account = BankAccountModel.objects.create(
            name="Primary Checking",
            entity_model=self.entity,
            account_model=self.cash_account,
            connection_type="manual",
            account_number="CHK-1234",
            active=True,
        )

        self.import_job = ImportJobModel.objects.create(
            description="Commit Test Import Job",
            bank_account_model=self.bank_account,
        )

        self.customer = CustomerModel.objects.create(
            customer_name="Acme Corp",
            customer_number="CUST-101",
            entity_model=self.entity,
        )

        self.vendor = VendorModel.objects.create(
            vendor_name="Office Supplies Co",
            vendor_number="VEND-202",
            entity_model=self.entity,
        )

        self.ledger = LedgerModel.objects.create(
            name="General Ledger",
            entity=self.entity,
            posted=True,
        )

        self.repo = BookkeepingRepository()

    def _create_invoice(
        self,
        amount_due: Decimal,
        amount_paid: Decimal = Decimal("0.00"),
        dt: date = date(2026, 3, 1),
    ) -> InvoiceModel:
        inv = InvoiceModel(
            cash_account=self.cash_account,
            prepaid_account=self.receivables_account,
            unearned_account=self.unearned_revenue_account,
            accrue=False,
        )
        _, inv = inv.configure(entity_slug=self.entity, user_model=self.user)
        inv.amount_due = amount_due
        inv.amount_paid = amount_paid
        inv.date_draft = dt
        inv.invoice_status = InvoiceModel.INVOICE_STATUS_APPROVED
        inv.customer = self.customer
        inv.clean()
        inv.save()
        return inv

    def _create_bill(
        self,
        amount_due: Decimal,
        amount_paid: Decimal = Decimal("0.00"),
        dt: date = date(2026, 3, 1),
    ) -> BillModel:
        bill = BillModel(
            cash_account=self.cash_account,
            prepaid_account=self.prepaid_account,
            unearned_account=self.payable_account,
            accrue=False,
        )
        _, bill = bill.configure(entity_slug=self.entity, user_model=self.user)
        bill.amount_due = amount_due
        bill.amount_paid = amount_paid
        bill.date_draft = dt
        bill.bill_status = BillModel.BILL_STATUS_APPROVED
        bill.vendor = self.vendor
        bill.clean()
        bill.save()
        return bill

    def _create_staged_tx(
        self,
        amount: Decimal,
        dt: date = date(2026, 3, 10),
        name: str = "Bank Tx",
    ) -> StagedTransactionModel:
        return StagedTransactionModel.objects.create(
            import_job=self.import_job,
            amount=amount,
            date_posted=dt,
            name=name,
            fit_id=f"FIT-{uuid.uuid4().hex[:8]}",
        )

    def _create_cash_tx(
        self,
        amount: Decimal,
        is_debit: bool = True,
        dt: date = date(2026, 3, 10),
        desc: str = "Cash Tx",
        posted: bool = True,
        account: AccountModel | None = None,
    ) -> TransactionModel:
        je = JournalEntryModel.objects.create(
            ledger=self.ledger,
            description=desc,
            posted=False,
            timestamp=datetime(dt.year, dt.month, dt.day, 12, 0, 0, tzinfo=dt_timezone.utc),
        )
        tx = TransactionModel.objects.create(
            journal_entry=je,
            account=account or self.cash_account,
            amount=amount,
            tx_type=DEBIT if is_debit else CREDIT,
            reconciled=False,
            description=desc,
        )
        TransactionModel.objects.create(
            journal_entry=je,
            account=self.revenue_account,
            amount=amount,
            tx_type=CREDIT if is_debit else DEBIT,
            reconciled=False,
            description=f"{desc} Counterpart",
        )
        if posted:
            je.posted = True
            je.save(update_fields=["posted"], verify=False)
        return tx

    def _create_second_entity(self) -> tuple[EntityModel, InvoiceModel, BillModel, TransactionModel, BankAccountModel, StagedTransactionModel]:
        """Create an isolated second entity for cross-entity rejection tests."""
        entity2 = EntityModel.add_root(
            name="Second Entity Corp",
            admin=self.user,
            currency="USD",
            fy_start_month=1,
            accrual_method=False,
        )
        coa2 = entity2.create_chart_of_accounts(
            coa_name="Second CoA",
            assign_as_default=True,
            commit=True,
        )
        asset_root2 = coa2.accountmodel_set.get(code="01000000")
        cash_acc2 = asset_root2.add_child(
            coa_model=coa2,
            code="1010",
            name="Second Checking",
            role=ASSET_CA_CASH,
            balance_type=DEBIT,
        )
        recv_acc2 = asset_root2.add_child(
            coa_model=coa2,
            code="1020",
            name="Second Receivables",
            role=ASSET_CA_RECEIVABLES,
            balance_type=DEBIT,
        )
        prepaid_acc2 = asset_root2.add_child(
            coa_model=coa2,
            code="1030",
            name="Second Prepaid",
            role=ASSET_CA_PREPAID,
            balance_type=DEBIT,
        )
        liab_root2 = coa2.accountmodel_set.get(code="02000000")
        unearned_acc2 = liab_root2.add_child(
            coa_model=coa2,
            code="2010",
            name="Second Deferred",
            role=LIABILITY_CL_DEFERRED_REVENUE,
            balance_type=CREDIT,
        )
        payable_acc2 = liab_root2.add_child(
            coa_model=coa2,
            code="2020",
            name="Second Payable",
            role=LIABILITY_CL_ACC_PAYABLE,
            balance_type=CREDIT,
        )
        income_root2 = coa2.accountmodel_set.get(code="04000000")
        rev_acc2 = income_root2.add_child(
            coa_model=coa2,
            code="4010",
            name="Second Revenue",
            role=INCOME_OPERATIONAL,
            balance_type=CREDIT,
        )
        ba2 = BankAccountModel.objects.create(
            name="Second Bank Acc",
            entity_model=entity2,
            account_model=cash_acc2,
            connection_type="manual",
            account_number="CHK-9999",
            active=True,
        )
        job2 = ImportJobModel.objects.create(
            description="Second Import Job",
            bank_account_model=ba2,
        )
        stx2 = StagedTransactionModel.objects.create(
            import_job=job2,
            amount=Decimal("100.00"),
            date_posted=date(2026, 3, 10),
            name="Second Staged Tx",
            fit_id="FIT-SECOND-1",
        )
        cust2 = CustomerModel.objects.create(
            customer_name="Second Customer",
            customer_number="CUST-SECOND",
            entity_model=entity2,
        )
        vend2 = VendorModel.objects.create(
            vendor_name="Second Vendor",
            vendor_number="VEND-SECOND",
            entity_model=entity2,
        )
        ledger2 = LedgerModel.objects.create(
            name="Second Ledger",
            entity=entity2,
            posted=True,
        )
        inv2 = InvoiceModel(
            cash_account=cash_acc2,
            prepaid_account=recv_acc2,
            unearned_account=unearned_acc2,
            accrue=False,
        )
        _, inv2 = inv2.configure(entity_slug=entity2, user_model=self.user)
        inv2.amount_due = Decimal("200.00")
        inv2.date_draft = date(2026, 3, 1)
        inv2.invoice_status = InvoiceModel.INVOICE_STATUS_APPROVED
        inv2.customer = cust2
        inv2.clean()
        inv2.save()

        bill2 = BillModel(
            cash_account=cash_acc2,
            prepaid_account=prepaid_acc2,
            unearned_account=payable_acc2,
            accrue=False,
        )
        _, bill2 = bill2.configure(entity_slug=entity2, user_model=self.user)
        bill2.amount_due = Decimal("300.00")
        bill2.date_draft = date(2026, 3, 1)
        bill2.bill_status = BillModel.BILL_STATUS_APPROVED
        bill2.vendor = vend2
        bill2.clean()
        bill2.save()

        je2 = JournalEntryModel.objects.create(
            ledger=ledger2,
            description="Second Cash Tx",
            posted=False,
            timestamp=datetime(2026, 3, 10, 12, 0, 0, tzinfo=dt_timezone.utc),
        )
        tx2 = TransactionModel.objects.create(
            journal_entry=je2,
            account=cash_acc2,
            amount=Decimal("150.00"),
            tx_type=DEBIT,
            reconciled=False,
            description="Second Cash Tx",
        )
        TransactionModel.objects.create(
            journal_entry=je2,
            account=rev_acc2,
            amount=Decimal("150.00"),
            tx_type=CREDIT,
            reconciled=False,
            description="Second Counterpart",
        )
        je2.posted = True
        je2.save(update_fields=["posted"], verify=False)
        return entity2, inv2, bill2, tx2, ba2, stx2

    # ==================================================================
    # Test A: Empty Write Set
    # ==================================================================
    def test_empty_write_set_preserves_revision_and_no_row_created(self) -> None:
        """Empty write-set does NOT create revision row and does NOT advance revision."""
        # 1. No revision row in DB
        self.assertFalse(BookkeepingRevision.objects.filter(entity=self.entity).exists())

        empty_ws = PersistenceWriteSet()
        result = self.repo.commit(
            company_id=str(self.entity.uuid),
            expected_revision=0,
            write_set=empty_ws,
        )

        self.assertEqual(result.previous_revision, 0)
        self.assertEqual(result.new_revision, 0)
        self.assertEqual(result.write_set, empty_ws)
        # Crucial check: still no row created
        self.assertFalse(BookkeepingRevision.objects.filter(entity=self.entity).exists())

        # 2. Existing revision row: revision must match expected
        BookkeepingRevision.objects.create(entity=self.entity, revision=5)
        res2 = self.repo.commit(
            company_id=str(self.entity.uuid),
            expected_revision=5,
            write_set=empty_ws,
        )
        self.assertEqual(res2.previous_revision, 5)
        self.assertEqual(res2.new_revision, 5)
        self.assertEqual(BookkeepingRevision.objects.get(entity=self.entity).revision, 5)

        # 3. Existing revision mismatch raises PersistenceConflictError
        with self.assertRaises(PersistenceConflictError):
            self.repo.commit(
                company_id=str(self.entity.uuid),
                expected_revision=4,
                write_set=empty_ws,
            )

    # ==================================================================
    # Test B: First Commit
    # ==================================================================
    def test_first_commit_advances_revision_from_zero_to_one(self) -> None:
        """First non-empty commit with expected_revision=0 advances revision to 1."""
        inv = self._create_invoice(amount_due=Decimal("500.00"))
        now = datetime.now(dt_timezone.utc)

        rd = RoutingDecision(
            id="rd-first-commit",
            book_item_id=f"invoice:{inv.uuid}",
            bank_account_id=str(self.bank_account.uuid),
            source=RoutingDecisionSource.HUMAN,
            utility=1000,
            state_revision_at_creation=0,
            created_at=now,
        )
        ws = PersistenceWriteSet(routing_decisions=(rd,))

        result = self.repo.commit(
            company_id=str(self.entity.uuid),
            expected_revision=0,
            write_set=ws,
        )

        self.assertEqual(result.previous_revision, 0)
        self.assertEqual(result.new_revision, 1)

        rev_row = BookkeepingRevision.objects.get(entity=self.entity)
        self.assertEqual(rev_row.revision, 1)

        db_rd = BookkeepingRoutingDecision.objects.get(id="rd-first-commit")
        self.assertEqual(db_rd.invoice, inv)
        self.assertEqual(db_rd.bank_account, self.bank_account)

    # ==================================================================
    # Test C: Stale Revision Rejection
    # ==================================================================
    def test_stale_revision_rejects_and_writes_nothing(self) -> None:
        """Commit against stale revision raises PersistenceConflictError and writes nothing."""
        # Establish revision 1
        BookkeepingRevision.objects.create(entity=self.entity, revision=1)
        inv = self._create_invoice(amount_due=Decimal("500.00"))
        now = datetime.now(dt_timezone.utc)

        rd = RoutingDecision(
            id="rd-stale-write",
            book_item_id=f"invoice:{inv.uuid}",
            bank_account_id=str(self.bank_account.uuid),
            source=RoutingDecisionSource.HUMAN,
            utility=1000,
            state_revision_at_creation=0,
            created_at=now,
        )
        ws = PersistenceWriteSet(routing_decisions=(rd,))

        with self.assertRaises(PersistenceConflictError):
            self.repo.commit(
                company_id=str(self.entity.uuid),
                expected_revision=0,  # Stale! DB is 1
                write_set=ws,
            )

        # Ensure nothing was written and revision unchanged
        self.assertFalse(BookkeepingRoutingDecision.objects.filter(id="rd-stale-write").exists())
        self.assertEqual(BookkeepingRevision.objects.get(entity=self.entity).revision, 1)

    # ==================================================================
    # Test D: Batch Cohesion
    # ==================================================================
    def test_batch_cohesion_multiple_artifacts_advance_revision_once(self) -> None:
        """Multiple decision artifacts in one write_set persist atomically and advance revision once."""
        inv = self._create_invoice(amount_due=Decimal("500.00"))
        bill = self._create_bill(amount_due=Decimal("300.00"))
        now = datetime.now(dt_timezone.utc)

        rd = RoutingDecision(
            id="rd-batch-1",
            book_item_id=f"invoice:{inv.uuid}",
            bank_account_id=str(self.bank_account.uuid),
            source=RoutingDecisionSource.HUMAN,
            utility=1000,
            state_revision_at_creation=0,
            created_at=now,
        )
        cd = ClassificationDecision(
            id="cd-batch-1",
            book_item_id=f"bill:{bill.uuid}",
            account_code=self.revenue_account.code,
            source=ClassificationSource.HUMAN,
            confidence=1.0,
            state_revision_at_decision=0,
            created_at=now,
        )
        ea = BookItemEvidenceAssertion(
            id="ea-batch-1",
            book_item_id=f"invoice:{inv.uuid}",
            session_id="session-batch",
            evidence_type=BookItemEvidenceType.DESCRIPTION,
            value="Batch Invoice Desc",
            source=EvidenceSource.HUMAN_ASSERTION,
            state_revision_at_creation=0,
            created_at=now,
        )

        ws = PersistenceWriteSet(
            routing_decisions=(rd,),
            classifications=(cd,),
            book_item_evidence_assertions=(ea,),
        )

        result = self.repo.commit(
            company_id=str(self.entity.uuid),
            expected_revision=0,
            write_set=ws,
        )

        self.assertEqual(result.previous_revision, 0)
        self.assertEqual(result.new_revision, 1)
        self.assertEqual(BookkeepingRevision.objects.get(entity=self.entity).revision, 1)

        self.assertTrue(BookkeepingRoutingDecision.objects.filter(id="rd-batch-1").exists())
        self.assertTrue(BookkeepingClassificationDecision.objects.filter(id="cd-batch-1").exists())
        self.assertTrue(BookkeepingEvidenceAssertion.objects.filter(id="ea-batch-1").exists())

    # ==================================================================
    # Test E: Atomic Rollback
    # ==================================================================
    def test_atomic_rollback_on_failure(self) -> None:
        """Valid artifact + invalid artifact in same write_set rolls back completely."""
        inv = self._create_invoice(amount_due=Decimal("500.00"))
        now = datetime.now(dt_timezone.utc)

        rd_valid = RoutingDecision(
            id="rd-atomic-valid",
            book_item_id=f"invoice:{inv.uuid}",
            bank_account_id=str(self.bank_account.uuid),
            source=RoutingDecisionSource.HUMAN,
            utility=1000,
            state_revision_at_creation=0,
            created_at=now,
        )
        # Invalid classification: non-existent account code "9999"
        cd_invalid = ClassificationDecision(
            id="cd-atomic-invalid",
            book_item_id=f"invoice:{inv.uuid}",
            account_code="9999",
            source=ClassificationSource.HUMAN,
            confidence=1.0,
            state_revision_at_decision=0,
            created_at=now,
        )

        ws = PersistenceWriteSet(
            routing_decisions=(rd_valid,),
            classifications=(cd_invalid,),
        )

        with self.assertRaises(PersistenceError):
            self.repo.commit(
                company_id=str(self.entity.uuid),
                expected_revision=0,
                write_set=ws,
            )

        # Proves rollback: neither artifact exists and revision was not created
        self.assertFalse(BookkeepingRoutingDecision.objects.filter(id="rd-atomic-valid").exists())
        self.assertFalse(BookkeepingClassificationDecision.objects.filter(id="cd-atomic-invalid").exists())
        self.assertFalse(BookkeepingRevision.objects.filter(entity=self.entity).exists())

    # ==================================================================
    # Test F: Routing Round Trip
    # ==================================================================
    def test_routing_round_trip(self) -> None:
        """Commit -> close/reload -> exact RoutingDecision reconstructed."""
        inv = self._create_invoice(amount_due=Decimal("750.00"))
        now = datetime.now(dt_timezone.utc)

        rd = RoutingDecision(
            id="rd-round-trip-1",
            book_item_id=f"invoice:{inv.uuid}",
            bank_account_id=str(self.bank_account.uuid),
            source=RoutingDecisionSource.CP_SAT,
            utility=950,
            solver_run_id="solver-123",
            session_id="session-abc",
            state_revision_at_creation=0,
            created_at=now,
        )

        self.repo.commit(
            company_id=str(self.entity.uuid),
            expected_revision=0,
            write_set=PersistenceWriteSet(routing_decisions=(rd,)),
        )

        # Reload fresh snapshot
        snapshot = self.repo.load_snapshot(company_id=str(self.entity.uuid))
        self.assertEqual(snapshot.persistence_revision, 1)
        self.assertEqual(len(snapshot.routing_decisions), 1)

        loaded_rd = snapshot.routing_decisions[0]
        self.assertEqual(loaded_rd.id, rd.id)
        self.assertEqual(loaded_rd.book_item_id, rd.book_item_id)
        self.assertEqual(loaded_rd.bank_account_id, rd.bank_account_id)
        self.assertEqual(loaded_rd.source, rd.source)
        self.assertEqual(loaded_rd.utility, rd.utility)
        self.assertEqual(loaded_rd.solver_run_id, rd.solver_run_id)
        self.assertEqual(loaded_rd.session_id, rd.session_id)
        self.assertEqual(loaded_rd.state_revision_at_creation, rd.state_revision_at_creation)
        self.assertEqual(loaded_rd.created_at, rd.created_at)

    # ==================================================================
    # Test G: Classification Round Trip
    # ==================================================================
    def test_classification_round_trip(self) -> None:
        """Commit -> reload -> exact ClassificationDecision reconstructed."""
        bill = self._create_bill(amount_due=Decimal("1200.00"))
        now = datetime.now(dt_timezone.utc)

        cd = ClassificationDecision(
            id="cd-round-trip-1",
            book_item_id=f"bill:{bill.uuid}",
            account_code=self.revenue_account.code,
            source=ClassificationSource.ASE_DAG,
            confidence=0.92,
            rationale="Recurring SaaS subscription",
            evidence_refs=("doc-99", "doc-100"),
            session_id="session-xyz",
            dag_run_id="dag-777",
            ase_node_id="ase-888",
            state_revision_at_decision=0,
            created_at=now,
        )

        self.repo.commit(
            company_id=str(self.entity.uuid),
            expected_revision=0,
            write_set=PersistenceWriteSet(classifications=(cd,)),
        )

        snapshot = self.repo.load_snapshot(company_id=str(self.entity.uuid))
        self.assertEqual(snapshot.persistence_revision, 1)
        self.assertEqual(len(snapshot.classifications), 1)

        loaded_cd = snapshot.classifications[0]
        self.assertEqual(loaded_cd.id, cd.id)
        self.assertEqual(loaded_cd.book_item_id, cd.book_item_id)
        self.assertEqual(loaded_cd.account_code, cd.account_code)
        self.assertEqual(loaded_cd.source, cd.source)
        assert loaded_cd.confidence is not None and cd.confidence is not None
        self.assertAlmostEqual(loaded_cd.confidence, cd.confidence)
        self.assertEqual(loaded_cd.rationale, cd.rationale)
        self.assertEqual(loaded_cd.evidence_refs, cd.evidence_refs)
        self.assertEqual(loaded_cd.session_id, cd.session_id)
        self.assertEqual(loaded_cd.dag_run_id, cd.dag_run_id)
        self.assertEqual(loaded_cd.ase_node_id, cd.ase_node_id)
        self.assertEqual(loaded_cd.state_revision_at_decision, cd.state_revision_at_decision)
        self.assertEqual(loaded_cd.created_at, cd.created_at)

    # ==================================================================
    # Test H: Evidence Round Trip
    # ==================================================================
    def test_evidence_round_trip(self) -> None:
        """Commit -> reload -> exact evidence reconstructed."""
        tx = self._create_cash_tx(amount=Decimal("45.00"))
        now = datetime.now(dt_timezone.utc)

        ea = BookItemEvidenceAssertion(
            id="ea-round-trip-1",
            book_item_id=f"tx:{tx.uuid}",
            session_id="session-evidence",
            evidence_type=BookItemEvidenceType.DESCRIPTION,
            value="Office stationery and pens",
            document_ids=("doc-inv-1", "doc-inv-2"),
            source=EvidenceSource.DOCUMENT_EXTRACTION,
            confidence=0.98,
            reason="Extracted from receipt PDF",
            metadata={"merchant": "Staples", "tax": "3.50"},
            state_revision_at_creation=0,
            created_at=now,
        )

        self.repo.commit(
            company_id=str(self.entity.uuid),
            expected_revision=0,
            write_set=PersistenceWriteSet(book_item_evidence_assertions=(ea,)),
        )

        snapshot = self.repo.load_snapshot(company_id=str(self.entity.uuid))
        self.assertEqual(snapshot.persistence_revision, 1)
        self.assertEqual(len(snapshot.book_item_evidence_assertions), 1)

        loaded_ea = snapshot.book_item_evidence_assertions[0]
        self.assertEqual(loaded_ea.id, ea.id)
        self.assertEqual(loaded_ea.book_item_id, ea.book_item_id)
        self.assertEqual(loaded_ea.evidence_type, ea.evidence_type)
        self.assertEqual(loaded_ea.value, ea.value)
        self.assertEqual(loaded_ea.document_ids, ea.document_ids)
        self.assertEqual(loaded_ea.source, ea.source)
        self.assertAlmostEqual(loaded_ea.confidence, ea.confidence)
        self.assertEqual(loaded_ea.reason, ea.reason)
        self.assertEqual(loaded_ea.metadata, ea.metadata)
        self.assertEqual(loaded_ea.state_revision_at_creation, ea.state_revision_at_creation)
        self.assertEqual(loaded_ea.created_at, ea.created_at)

    # ==================================================================
    # Test I: Stage-2 Reconciliation Round Trip
    # ==================================================================
    def test_stage2_reconciliation_round_trip(self) -> None:
        """Commit -> reload -> exact Stage-2 allocations and provenance reconstructed."""
        tx = self._create_cash_tx(amount=Decimal("150.00"), is_debit=True)
        stx = self._create_staged_tx(amount=Decimal("150.00"))
        now = datetime.now(dt_timezone.utc)

        rec = Reconciliation(
            id="rec-round-trip-1",
            bank_allocations=(
                BankAllocation(
                    bank_item_id=f"staged:{stx.uuid}",
                    amount_units="1500000",
                ),
            ),
            book_allocations=(
                BookAllocation(
                    book_item_id=f"tx:{tx.uuid}",
                    amount_units="1500000",
                ),
            ),
            evidence_refs=("doc-rec-1", "doc-rec-2"),
            source_hypothesis_id="hypo-101",
            source_hypothesis_state_revision=0,
            source_hypothesis_utility=980,
            source_hypothesis_generated_at=now,
            source_hypothesis_admissibility=SemanticAdmissibility.SUPPORTED,
            source_hypothesis_allocation_support=AllocationSupport.EXPLICIT_EVIDENCE,
            semantic_rationale="Exact amount and date match with receipt evidence",
            session_id="session-rec",
            state_revision_at_creation=1,
            created_at=now,
        )

        self.repo.commit(
            company_id=str(self.entity.uuid),
            expected_revision=0,
            write_set=PersistenceWriteSet(reconciliations=(rec,)),
        )

        snapshot = self.repo.load_snapshot(company_id=str(self.entity.uuid))
        self.assertEqual(snapshot.persistence_revision, 1)
        self.assertEqual(len(snapshot.reconciliations), 1)

        loaded_rec = snapshot.reconciliations[0]
        self.assertEqual(loaded_rec.id, rec.id)
        self.assertEqual(len(loaded_rec.bank_allocations), 1)
        self.assertEqual(loaded_rec.bank_allocations[0].bank_item_id, f"staged:{stx.uuid}")
        self.assertEqual(loaded_rec.bank_allocations[0].amount_units, "1500000")

        self.assertEqual(len(loaded_rec.book_allocations), 1)
        self.assertEqual(loaded_rec.book_allocations[0].book_item_id, f"tx:{tx.uuid}")
        self.assertEqual(loaded_rec.book_allocations[0].amount_units, "1500000")

        self.assertEqual(loaded_rec.evidence_refs, rec.evidence_refs)
        self.assertEqual(loaded_rec.source_hypothesis_id, rec.source_hypothesis_id)
        self.assertEqual(loaded_rec.source_hypothesis_state_revision, rec.source_hypothesis_state_revision)
        self.assertEqual(loaded_rec.source_hypothesis_utility, rec.source_hypothesis_utility)
        self.assertEqual(loaded_rec.source_hypothesis_generated_at, rec.source_hypothesis_generated_at)
        self.assertEqual(loaded_rec.source_hypothesis_admissibility, rec.source_hypothesis_admissibility)
        self.assertEqual(loaded_rec.source_hypothesis_allocation_support, rec.source_hypothesis_allocation_support)
        self.assertEqual(loaded_rec.semantic_rationale, rec.semantic_rationale)
        self.assertEqual(loaded_rec.session_id, rec.session_id)
        self.assertEqual(loaded_rec.state_revision_at_creation, rec.state_revision_at_creation)
        self.assertEqual(loaded_rec.created_at, rec.created_at)

    # ==================================================================
    # Test J: Invalidation Round Trip
    # ==================================================================
    def test_invalidation_round_trip_updates_active_truth(self) -> None:
        """Commit invalidation -> reload -> derived active truth reflects invalidation."""
        inv = self._create_invoice(amount_due=Decimal("500.00"))
        tx = self._create_cash_tx(amount=Decimal("150.00"))
        stx = self._create_staged_tx(amount=Decimal("150.00"))
        now = datetime.now(dt_timezone.utc)

        # 1. Commit initial decisions
        rd = RoutingDecision(
            id="rd-to-invalidate",
            book_item_id=f"invoice:{inv.uuid}",
            bank_account_id=str(self.bank_account.uuid),
            source=RoutingDecisionSource.HUMAN,
            utility=1000,
            state_revision_at_creation=0,
            created_at=now,
        )
        cd = ClassificationDecision(
            id="cd-to-invalidate",
            book_item_id=f"invoice:{inv.uuid}",
            account_code=self.revenue_account.code,
            source=ClassificationSource.HUMAN,
            confidence=1.0,
            state_revision_at_decision=0,
            created_at=now,
        )
        ea = BookItemEvidenceAssertion(
            id="ea-to-invalidate",
            book_item_id=f"invoice:{inv.uuid}",
            session_id="session-inv",
            evidence_type=BookItemEvidenceType.DESCRIPTION,
            value="Initial Description",
            source=EvidenceSource.HUMAN_ASSERTION,
            state_revision_at_creation=0,
            created_at=now,
        )
        rec = Reconciliation(
            id="rec-to-invalidate",
            bank_allocations=(
                BankAllocation(
                    bank_item_id=f"staged:{stx.uuid}",
                    amount_units="1500000",
                ),
            ),
            book_allocations=(
                BookAllocation(
                    book_item_id=f"tx:{tx.uuid}",
                    amount_units="1500000",
                ),
            ),
            state_revision_at_creation=0,
            created_at=now,
        )

        self.repo.commit(
            company_id=str(self.entity.uuid),
            expected_revision=0,
            write_set=PersistenceWriteSet(
                routing_decisions=(rd,),
                classifications=(cd,),
                book_item_evidence_assertions=(ea,),
                reconciliations=(rec,),
            ),
        )

        # Verify active in hydrated state
        state_before = BookkeepingHydrator(repository=self.repo).hydrate(
            company_id=str(self.entity.uuid)
        )
        queries_before = BookkeepingQueries(state_before)
        self.assertIsNotNone(queries_before.active_route(f"invoice:{inv.uuid}"))
        self.assertIsNotNone(queries_before.active_classification(f"invoice:{inv.uuid}"))
        self.assertIsNotNone(queries_before.active_evidence_assertion(f"invoice:{inv.uuid}", BookItemEvidenceType.DESCRIPTION))
        self.assertEqual(len(queries_before.derived.active_reconciliations), 1)
        self.assertEqual(len(queries_before.reconciliations_for_bank_item(f"staged:{stx.uuid}")), 1)

        # 2. Commit invalidations at revision 1
        ri = RoutingDecisionInvalidation(
            id="ri-1",
            routing_decision_id="rd-to-invalidate",
            reason="Wrong routing bank account selected",
            state_revision_at_invalidation=1,
            created_at=now,
        )
        ci = ClassificationInvalidation(
            id="ci-1",
            classification_id="cd-to-invalidate",
            reason="Incorrect COA code",
            state_revision_at_invalidation=1,
            created_at=now,
        )
        ei = BookItemEvidenceInvalidation(
            id="ei-1",
            assertion_id="ea-to-invalidate",
            reason="Bad OCR extraction",
            session_id="session-inv",
            state_revision_at_invalidation=1,
            created_at=now,
        )
        rec_i = ReconciliationInvalidation(
            id="rec-i-1",
            reconciliation_id="rec-to-invalidate",
            reason="Unmatched by accountant",
            state_revision_at_invalidation=1,
            created_at=now,
        )

        self.repo.commit(
            company_id=str(self.entity.uuid),
            expected_revision=1,
            write_set=PersistenceWriteSet(
                routing_invalidations=(ri,),
                classification_invalidations=(ci,),
                book_item_evidence_invalidations=(ei,),
                reconciliation_invalidations=(rec_i,),
            ),
        )

        # 3. Re-hydrate and assert active truth reflects invalidation
        state_after = BookkeepingHydrator(repository=self.repo).hydrate(
            company_id=str(self.entity.uuid)
        )
        queries_after = BookkeepingQueries(state_after)
        self.assertIsNone(queries_after.active_route(f"invoice:{inv.uuid}"))
        self.assertIsNone(queries_after.active_classification(f"invoice:{inv.uuid}"))
        self.assertIsNone(queries_after.active_evidence_assertion(f"invoice:{inv.uuid}", BookItemEvidenceType.DESCRIPTION))
        # Active reconciliation is invalidated
        self.assertEqual(len(queries_after.derived.active_reconciliations), 0)
        self.assertEqual(len(queries_after.reconciliations_for_bank_item(f"staged:{stx.uuid}")), 0)
        self.assertEqual(len(state_after.reconciliation_invalidations), 1)

    # ==================================================================
    # Test K: Supersession / Non-Resurrection
    # ==================================================================
    def test_supersession_non_resurrection(self) -> None:
        """Invalidating newest decision does NOT resurrect historical superseded decision."""
        inv = self._create_invoice(amount_due=Decimal("500.00"))
        now = datetime.now(dt_timezone.utc)

        # Decision 1
        cd1 = ClassificationDecision(
            id="cd-orig",
            book_item_id=f"invoice:{inv.uuid}",
            account_code=self.revenue_account.code,
            source=ClassificationSource.DETERMINISTIC_RULE,
            confidence=0.80,
            state_revision_at_decision=0,
            created_at=now,
        )
        self.repo.commit(
            company_id=str(self.entity.uuid),
            expected_revision=0,
            write_set=PersistenceWriteSet(classifications=(cd1,)),
        )

        # Decision 2 supersedes Decision 1
        cd2 = ClassificationDecision(
            id="cd-superseding",
            book_item_id=f"invoice:{inv.uuid}",
            account_code=self.revenue_account.code,
            source=ClassificationSource.HUMAN,
            confidence=1.0,
            supersedes_classification_id="cd-orig",
            state_revision_at_decision=1,
            created_at=now,
        )
        self.repo.commit(
            company_id=str(self.entity.uuid),
            expected_revision=1,
            write_set=PersistenceWriteSet(classifications=(cd2,)),
        )

        # Invalidate Decision 2
        ci2 = ClassificationInvalidation(
            id="ci-superseding",
            classification_id="cd-superseding",
            reason="Incorrect manual override",
            state_revision_at_invalidation=2,
            created_at=now,
        )
        self.repo.commit(
            company_id=str(self.entity.uuid),
            expected_revision=2,
            write_set=PersistenceWriteSet(classification_invalidations=(ci2,)),
        )

        # Hydrate: cd1 must NOT be resurrected!
        state = BookkeepingHydrator(repository=self.repo).hydrate(
            company_id=str(self.entity.uuid)
        )
        queries = BookkeepingQueries(state)
        self.assertIsNone(queries.active_classification(f"invoice:{inv.uuid}"))

    # ==================================================================
    # Test L: Cross-Entity Source Rejection
    # ==================================================================
    def test_cross_entity_source_rejection(self) -> None:
        """Commit fails when attempting to reference another entity's resources."""
        entity2, inv2, bill2, tx2, ba2, stx2 = self._create_second_entity()
        inv1 = self._create_invoice(amount_due=Decimal("500.00"))
        now = datetime.now(dt_timezone.utc)

        # 1. Routing decision referencing entity2's invoice
        rd_bad_inv = RoutingDecision(
            id="rd-bad-inv",
            book_item_id=f"invoice:{inv2.uuid}",
            bank_account_id=str(self.bank_account.uuid),
            source=RoutingDecisionSource.HUMAN,
            utility=1000,
            state_revision_at_creation=0,
            created_at=now,
        )
        with self.assertRaises(PersistenceError):
            self.repo.commit(
                company_id=str(self.entity.uuid),
                expected_revision=0,
                write_set=PersistenceWriteSet(routing_decisions=(rd_bad_inv,)),
            )

        # 2. Routing decision referencing entity2's bank account
        rd_bad_ba = RoutingDecision(
            id="rd-bad-ba",
            book_item_id=f"invoice:{inv1.uuid}",
            bank_account_id=str(ba2.uuid),
            source=RoutingDecisionSource.HUMAN,
            utility=1000,
            state_revision_at_creation=0,
            created_at=now,
        )
        with self.assertRaises(PersistenceError):
            self.repo.commit(
                company_id=str(self.entity.uuid),
                expected_revision=0,
                write_set=PersistenceWriteSet(routing_decisions=(rd_bad_ba,)),
            )

        # 3. Classification referencing entity2's bill
        cd_bad_bill = ClassificationDecision(
            id="cd-bad-bill",
            book_item_id=f"bill:{bill2.uuid}",
            account_code=self.revenue_account.code,
            source=ClassificationSource.HUMAN,
            confidence=1.0,
            state_revision_at_decision=0,
            created_at=now,
        )
        with self.assertRaises(PersistenceError):
            self.repo.commit(
                company_id=str(self.entity.uuid),
                expected_revision=0,
                write_set=PersistenceWriteSet(classifications=(cd_bad_bill,)),
            )

        # 4. Evidence referencing entity2's transaction
        ea_bad_tx = BookItemEvidenceAssertion(
            id="ea-bad-tx",
            book_item_id=f"tx:{tx2.uuid}",
            session_id="session-cross",
            evidence_type=BookItemEvidenceType.DESCRIPTION,
            value="Cross entity desc",
            source=EvidenceSource.HUMAN_ASSERTION,
            state_revision_at_creation=0,
            created_at=now,
        )
        with self.assertRaises(PersistenceError):
            self.repo.commit(
                company_id=str(self.entity.uuid),
                expected_revision=0,
                write_set=PersistenceWriteSet(book_item_evidence_assertions=(ea_bad_tx,)),
            )

        # 5. Reconciliation referencing entity2's bank item
        tx1 = self._create_cash_tx(amount=Decimal("100.00"))
        rec_bad_bank = Reconciliation(
            id="rec-bad-bank",
            bank_allocations=(
                BankAllocation(
                    bank_item_id=f"staged:{stx2.uuid}",
                    amount_units="1000000",
                ),
            ),
            book_allocations=(
                BookAllocation(
                    book_item_id=f"tx:{tx1.uuid}",
                    amount_units="1000000",
                ),
            ),
            state_revision_at_creation=0,
            created_at=now,
        )
        with self.assertRaises(PersistenceError):
            self.repo.commit(
                company_id=str(self.entity.uuid),
                expected_revision=0,
                write_set=PersistenceWriteSet(reconciliations=(rec_bad_bank,)),
            )

    # ==================================================================
    # Test M: Stage-2 Non-POSTED Book Target Defense
    # ==================================================================
    def test_stage2_rejects_non_posted_book_source_defensively(self) -> None:
        """Stage-2 rejects invoice/bill and unposted or non-cash transactions defensively."""
        inv = self._create_invoice(amount_due=Decimal("100.00"))
        bill = self._create_bill(amount_due=Decimal("100.00"))
        stx = self._create_staged_tx(amount=Decimal("100.00"))
        now = datetime.now(dt_timezone.utc)

        # 1. invoice target rejected
        rec_inv = Reconciliation(
            id="rec-rej-inv",
            bank_allocations=(BankAllocation(bank_item_id=f"staged:{stx.uuid}", amount_units="1000000"),),
            book_allocations=(BookAllocation(book_item_id=f"invoice:{inv.uuid}", amount_units="1000000"),),
            state_revision_at_creation=0,
            created_at=now,
        )
        with self.assertRaises(PersistenceError):
            self.repo.commit(
                company_id=str(self.entity.uuid),
                expected_revision=0,
                write_set=PersistenceWriteSet(reconciliations=(rec_inv,)),
            )

        # 2. bill target rejected
        rec_bill = Reconciliation(
            id="rec-rej-bill",
            bank_allocations=(BankAllocation(bank_item_id=f"staged:{stx.uuid}", amount_units="1000000"),),
            book_allocations=(BookAllocation(book_item_id=f"bill:{bill.uuid}", amount_units="1000000"),),
            state_revision_at_creation=0,
            created_at=now,
        )
        with self.assertRaises(PersistenceError):
            self.repo.commit(
                company_id=str(self.entity.uuid),
                expected_revision=0,
                write_set=PersistenceWriteSet(reconciliations=(rec_bill,)),
            )

        # 3. Unposted transaction rejected
        unposted_tx = self._create_cash_tx(amount=Decimal("100.00"), posted=False)
        rec_unposted = Reconciliation(
            id="rec-rej-unposted",
            bank_allocations=(BankAllocation(bank_item_id=f"staged:{stx.uuid}", amount_units="1000000"),),
            book_allocations=(BookAllocation(book_item_id=f"tx:{unposted_tx.uuid}", amount_units="1000000"),),
            state_revision_at_creation=0,
            created_at=now,
        )
        with self.assertRaises(PersistenceError):
            self.repo.commit(
                company_id=str(self.entity.uuid),
                expected_revision=0,
                write_set=PersistenceWriteSet(reconciliations=(rec_unposted,)),
            )

        # 4. Non-cash transaction rejected (revenue account)
        non_cash_tx = self._create_cash_tx(
            amount=Decimal("100.00"),
            posted=True,
            account=self.revenue_account,
        )
        rec_non_cash = Reconciliation(
            id="rec-rej-non-cash",
            bank_allocations=(BankAllocation(bank_item_id=f"staged:{stx.uuid}", amount_units="1000000"),),
            book_allocations=(BookAllocation(book_item_id=f"tx:{non_cash_tx.uuid}", amount_units="1000000"),),
            state_revision_at_creation=0,
            created_at=now,
        )
        with self.assertRaises(PersistenceError):
            self.repo.commit(
                company_id=str(self.entity.uuid),
                expected_revision=0,
                write_set=PersistenceWriteSet(reconciliations=(rec_non_cash,)),
            )

    # ==================================================================
    # Test N: Canonical OCC Lookup by UUID and Slug
    # ==================================================================
    def test_slug_and_uuid_aliases_commit_to_same_occ_head(self) -> None:
        """UUID and slug lookups commit against the exact same canonical OCC head."""
        inv = self._create_invoice(amount_due=Decimal("500.00"))
        now = datetime.now(dt_timezone.utc)

        # Commit 1 by UUID
        rd1 = RoutingDecision(
            id="rd-by-uuid",
            book_item_id=f"invoice:{inv.uuid}",
            bank_account_id=str(self.bank_account.uuid),
            source=RoutingDecisionSource.HUMAN,
            utility=1000,
            state_revision_at_creation=0,
            created_at=now,
        )
        res1 = self.repo.commit(
            company_id=str(self.entity.uuid),
            expected_revision=0,
            write_set=PersistenceWriteSet(routing_decisions=(rd1,)),
        )
        self.assertEqual(res1.new_revision, 1)

        # Commit 2 by slug (expected revision 1)
        cd1 = ClassificationDecision(
            id="cd-by-slug",
            book_item_id=f"invoice:{inv.uuid}",
            account_code=self.revenue_account.code,
            source=ClassificationSource.HUMAN,
            confidence=1.0,
            state_revision_at_decision=1,
            created_at=now,
        )
        res2 = self.repo.commit(
            company_id=self.entity.slug,
            expected_revision=1,
            write_set=PersistenceWriteSet(classifications=(cd1,)),
        )
        self.assertEqual(res2.new_revision, 2)

        # Commit 3 by UUID with stale revision 1 -> must fail
        with self.assertRaises(PersistenceConflictError):
            self.repo.commit(
                company_id=str(self.entity.uuid),
                expected_revision=1,
                write_set=PersistenceWriteSet(),
            )

    # ==================================================================
    # Test O: Rejection of Read Projections in Write Set
    # ==================================================================
    def test_forbidden_read_projections_rejected(self) -> None:
        """PersistenceWriteSet cannot contain read projections."""
        from bookkeeping_state.domain.bank import BankItem

        bad_bi = BankItem(
            id="staged:00000000-0000-0000-0000-000000000001",
            bank_account_id=str(self.bank_account.uuid),
            source_type=SourceType.BANK_STATEMENT_LINE,
            date=date(2026, 3, 1),
            amount_units="100000",
            direction=Direction.BANK_INFLOW,
            currency="USD",
            description="Bad item",
        )
        ws = PersistenceWriteSet(bank_items=(bad_bi,))

        with self.assertRaises(PersistenceError) as ctx:
            self.repo.commit(
                company_id=str(self.entity.uuid),
                expected_revision=0,
                write_set=ws,
            )
        self.assertIn("cannot contain read projections", str(ctx.exception))


class BookkeepingConcurrencyTests(TransactionTestCase):
    """
    Real transactional concurrency regression.
    Proves that two concurrent first commits (expected_revision=0) cannot both succeed.
    """

    def setUp(self) -> None:
        super().setUp()
        self.user = UserModel.objects.create_user(
            username="concurrency_admin",
            email="admin@concurrency.com",
            password="secure_password_123",
        )
        self.entity = EntityModel.add_root(
            name="Concurrency Entity",
            admin=self.user,
            currency="USD",
            fy_start_month=1,
            accrual_method=False,
        )
        coa = self.entity.create_chart_of_accounts(
            coa_name="Conc CoA",
            assign_as_default=True,
            commit=True,
        )
        asset_root = coa.accountmodel_set.get(code="01000000")
        cash_account = asset_root.add_child(
            coa_model=coa,
            code="1010",
            name="Conc Cash",
            role=ASSET_CA_CASH,
            balance_type=DEBIT,
        )
        recv_account = asset_root.add_child(
            coa_model=coa,
            code="1020",
            name="Conc Receivables",
            role=ASSET_CA_RECEIVABLES,
            balance_type=DEBIT,
        )
        liab_root = coa.accountmodel_set.get(code="02000000")
        unearned_account = liab_root.add_child(
            coa_model=coa,
            code="2010",
            name="Conc Deferred",
            role=LIABILITY_CL_DEFERRED_REVENUE,
            balance_type=CREDIT,
        )
        self.bank_account = BankAccountModel.objects.create(
            name="Conc Checking",
            entity_model=self.entity,
            account_model=cash_account,
            connection_type="manual",
            account_number="CHK-CONC",
            active=True,
        )
        self.customer = CustomerModel.objects.create(
            customer_name="Conc Customer",
            customer_number="CUST-CONC",
            entity_model=self.entity,
        )

        inv = InvoiceModel(
            cash_account=cash_account,
            prepaid_account=recv_account,
            unearned_account=unearned_account,
            accrue=False,
        )
        _, inv = inv.configure(entity_slug=self.entity, user_model=self.user)
        inv.amount_due = Decimal("500.00")
        inv.date_draft = date(2026, 3, 1)
        inv.invoice_status = InvoiceModel.INVOICE_STATUS_APPROVED
        inv.customer = self.customer
        inv.clean()
        inv.save()
        self.invoice = inv
        self.repo = BookkeepingRepository()

    def test_concurrent_first_commits_serialize_via_occ_under_postgresql(self) -> None:
        """
        Two concurrent workers with expected_revision=0 against the same entity.
        Requires PostgreSQL row locking (SELECT FOR UPDATE).
        """
        self.assertEqual(
            connection.vendor,
            "postgresql",
            "PostgreSQL row-level locking (SELECT FOR UPDATE) required for multi-threaded OCC proof.",
        )

        now = datetime.now(dt_timezone.utc)
        rd1 = RoutingDecision(
            id="rd-conc-1",
            book_item_id=f"invoice:{self.invoice.uuid}",
            bank_account_id=str(self.bank_account.uuid),
            source=RoutingDecisionSource.HUMAN,
            utility=1000,
            state_revision_at_creation=0,
            created_at=now,
        )
        rd2 = RoutingDecision(
            id="rd-conc-2",
            book_item_id=f"invoice:{self.invoice.uuid}",
            bank_account_id=str(self.bank_account.uuid),
            source=RoutingDecisionSource.HUMAN,
            utility=900,
            state_revision_at_creation=0,
            created_at=now,
        )

        successes = []
        conflicts = []

        def worker(rd: RoutingDecision):
            connection.close()  # ensure separate DB connection in thread
            try:
                res = self.repo.commit(
                    company_id=str(self.entity.uuid),
                    expected_revision=0,
                    write_set=PersistenceWriteSet(routing_decisions=(rd,)),
                )
                successes.append(res)
            except PersistenceConflictError as e:
                conflicts.append(e)

        with concurrent.futures.ThreadPoolExecutor(max_workers=2) as executor:
            f1 = executor.submit(worker, rd1)
            f2 = executor.submit(worker, rd2)
            f1.result()
            f2.result()

        self.assertEqual(len(successes), 1)
        self.assertEqual(len(conflicts), 1)
        self.assertEqual(BookkeepingRevision.objects.get(entity=self.entity).revision, 1)
