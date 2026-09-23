"""
Tests for Bookkeeping Decision and Provenance Models
-----------------------------------------------------
Verifies database-level constraints, check constraints, foreign key protections,
and uniqueness rules for the 13 durable bookkeeping persistence models.
"""

from datetime import date, datetime, timezone
from decimal import Decimal
from uuid import uuid4

from django.db import IntegrityError, models, transaction
from django.test import TestCase

from ledger.io.roles import (
    ASSET_CA_CASH,
    ASSET_CA_PREPAID,
    ASSET_CA_RECEIVABLES,
    CREDIT,
    DEBIT,
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
    PlaidItem,
    PlaidTransaction,
    StagedTransactionModel,
    TransactionModel,
    VendorModel,
)
from ledger.models.bookkeeping import (
    BookkeepingClassificationDecision,
    BookkeepingClassificationInvalidation,
    BookkeepingEvidenceAssertion,
    BookkeepingEvidenceInvalidation,
    BookkeepingPaymentApplication,
    BookkeepingPaymentApplicationAllocation,
    BookkeepingReconciliation,
    BookkeepingReconciliationBankAllocation,
    BookkeepingReconciliationBookAllocation,
    BookkeepingReconciliationInvalidation,
    BookkeepingRevision,
    BookkeepingRoutingDecision,
    BookkeepingRoutingInvalidation,
)


class BookkeepingModelConstraintTests(TestCase):
    @classmethod
    def setUpTestData(cls) -> None:
        from django.contrib.auth import get_user_model
        cls.user = get_user_model().objects.create_user(
            username=f"testuser_{uuid4().hex[:8]}",
            email=f"testuser_{uuid4().hex[:8]}@example.com",
            password="testpassword123",
        )
        cls.entity = EntityModel.add_root(
            name="Bookkeeping Test Corp",
            admin=cls.user,
            currency="USD",
            fy_start_month=1,
            accrual_method=False,
        )
        cls.coa = cls.entity.create_chart_of_accounts(
            coa_name="Bookkeeping Test CoA",
            assign_as_default=True,
            commit=True,
        )
        asset_root = cls.coa.accountmodel_set.get(code="01000000")

        cls.cash_account = asset_root.add_child(
            coa_model=cls.coa,
            code="1010",
            name="Operating Cash",
            role=ASSET_CA_CASH,
            balance_type=DEBIT,
        )
        cls.receivable_account = asset_root.add_child(
            coa_model=cls.coa,
            code="1020",
            name="Accounts Receivable",
            role=ASSET_CA_RECEIVABLES,
            balance_type=DEBIT,
        )
        cls.prepaid_account = asset_root.add_child(
            coa_model=cls.coa,
            code="1030",
            name="Prepaid Expenses",
            role=ASSET_CA_PREPAID,
            balance_type=DEBIT,
        )

        liability_root = cls.coa.accountmodel_set.get(code="02000000")
        cls.unearned_revenue_account = liability_root.add_child(
            coa_model=cls.coa,
            code="2010",
            name="Deferred Revenue",
            role=LIABILITY_CL_DEFERRED_REVENUE,
            balance_type=CREDIT,
        )
        cls.accounts_payable_account = liability_root.add_child(
            coa_model=cls.coa,
            code="2020",
            name="Accounts Payable",
            role=LIABILITY_CL_ACC_PAYABLE,
            balance_type=CREDIT,
        )

        cls.bank_account = BankAccountModel.objects.create(
            entity_model=cls.entity,
            account_model=cls.cash_account,
            name="Main Checking",
            connection_type="manual",
            account_number="CHK-001",
            active=True,
        )

        cls.customer = CustomerModel.objects.create(
            entity_model=cls.entity,
            customer_name="Acme Corp",
            customer_number="CUST-001",
        )
        cls.vendor = VendorModel.objects.create(
            entity_model=cls.entity,
            vendor_name="Global Supplies",
            vendor_number="VEND-001",
        )

        inv = InvoiceModel(
            cash_account=cls.cash_account,
            prepaid_account=cls.receivable_account,
            unearned_account=cls.unearned_revenue_account,
            accrue=False,
        )
        _, inv = inv.configure(entity_slug=cls.entity, user_model=cls.user)
        inv.amount_due = Decimal("1000.00")
        inv.amount_paid = Decimal("0.00")
        inv.date_draft = date(2026, 1, 15)
        inv.invoice_status = InvoiceModel.INVOICE_STATUS_APPROVED
        inv.customer = cls.customer
        inv.clean()
        inv.save()
        cls.invoice = inv

        bill = BillModel(
            cash_account=cls.cash_account,
            prepaid_account=cls.prepaid_account,
            unearned_account=cls.accounts_payable_account,
            accrue=False,
        )
        _, bill = bill.configure(entity_slug=cls.entity, user_model=cls.user)
        bill.amount_due = Decimal("500.00")
        bill.amount_paid = Decimal("0.00")
        bill.date_draft = date(2026, 1, 15)
        bill.bill_status = BillModel.BILL_STATUS_APPROVED
        bill.vendor = cls.vendor
        bill.clean()
        bill.save()
        cls.bill = bill

        cls.standalone_ledger = LedgerModel.objects.create(
            name="Bookkeeping Test Ledger",
            entity=cls.entity,
            posted=True,
        )
        cls.journal_entry = JournalEntryModel.objects.create(
            ledger=cls.standalone_ledger,
            timestamp=datetime(2026, 1, 15, 12, 0, 0, tzinfo=timezone.utc),
            description="Test payment journal entry",
            posted=False,
        )
        cls.transaction = TransactionModel.objects.create(
            journal_entry=cls.journal_entry,
            account=cls.cash_account,
            amount=Decimal("1000.00"),
            tx_type=TransactionModel.DEBIT,
        )
        cls.journal_entry.posted = True
        cls.journal_entry.save(update_fields=["posted"], verify=False)

        cls.plaid_item = PlaidItem.objects.create(
            user=cls.user,
            item_id=f"item_{uuid4().hex[:8]}",
            access_token=f"token_{uuid4().hex[:8]}",
        )
        cls.plaid_tx = PlaidTransaction.objects.create(
            plaid_item=cls.plaid_item,
            transaction_id=f"tx_{uuid4().hex[:8]}",
            account_id="acc_123",
            amount=Decimal("1000.00"),
            date=date(2026, 1, 15),
            name="Plaid Deposit",
            iso_currency_code="USD",
            pending=False,
            is_current=True,
            is_removed=False,
        )

        cls.import_job = ImportJobModel.objects.create(
            description="Import Job",
            bank_account_model=cls.bank_account,
        )
        cls.staged_tx = StagedTransactionModel.objects.create(
            import_job=cls.import_job,
            amount=Decimal("1000.00"),
            date_posted=date(2026, 1, 15),
            name="Staged Deposit",
            fit_id=f"FIT-{uuid4().hex[:8]}",
        )

    # ------------------------------------------------------------------
    # 1. BookkeepingRevision One-to-One
    # ------------------------------------------------------------------
    def test_bookkeeping_revision_one_to_one_per_entity(self) -> None:
        rev1 = BookkeepingRevision.objects.create(entity=self.entity, revision=0)
        self.assertEqual(rev1.revision, 0)

        with self.assertRaises(IntegrityError):
            BookkeepingRevision.objects.create(entity=self.entity, revision=1)

    # ------------------------------------------------------------------
    # 2. Supersession Self-FK is Protected
    # ------------------------------------------------------------------
    def test_routing_supersession_self_fk_protect(self) -> None:
        d1 = BookkeepingRoutingDecision.objects.create(
            id="route-1",
            entity=self.entity,
            invoice=self.invoice,
            bank_account=self.bank_account,
            source="ENGINE",
            utility=1000,
            state_revision_at_creation=1,
            created_at=datetime.now(timezone.utc),
        )
        d2 = BookkeepingRoutingDecision.objects.create(
            id="route-2",
            entity=self.entity,
            invoice=self.invoice,
            bank_account=self.bank_account,
            source="USER",
            utility=1000,
            supersedes=d1,
            state_revision_at_creation=2,
            created_at=datetime.now(timezone.utc),
        )

        with self.assertRaises(models.ProtectedError):
            d1.delete()

    # ------------------------------------------------------------------
    # 3. Invalidation Target Deletion is Protected
    # ------------------------------------------------------------------
    def test_invalidation_target_deletion_protected(self) -> None:
        d1 = BookkeepingRoutingDecision.objects.create(
            id="route-inv-test",
            entity=self.entity,
            invoice=self.invoice,
            bank_account=self.bank_account,
            source="ENGINE",
            state_revision_at_creation=1,
            created_at=datetime.now(timezone.utc),
        )
        BookkeepingRoutingInvalidation.objects.create(
            id="route-inv-1",
            routing_decision=d1,
            reason="Wrong account routed",
            state_revision_at_invalidation=2,
            created_at=datetime.now(timezone.utc),
        )

        with self.assertRaises(models.ProtectedError):
            d1.delete()

    # ------------------------------------------------------------------
    # 4. Routing / Classification / Evidence Target XOR Constraints
    # ------------------------------------------------------------------
    def test_routing_target_xor_constraint(self) -> None:
        # Both invoice and bill: fails
        with self.assertRaises(IntegrityError):
            with transaction.atomic():
                BookkeepingRoutingDecision.objects.create(
                    id="route-fail-both",
                    entity=self.entity,
                    invoice=self.invoice,
                    bill=self.bill,
                    bank_account=self.bank_account,
                    source="ENGINE",
                    state_revision_at_creation=1,
                    created_at=datetime.now(timezone.utc),
                )

        # Invoice and transaction: fails
        with self.assertRaises(IntegrityError):
            with transaction.atomic():
                BookkeepingRoutingDecision.objects.create(
                    id="route-fail-inv-tx",
                    entity=self.entity,
                    invoice=self.invoice,
                    transaction=self.transaction,
                    bank_account=self.bank_account,
                    source="ENGINE",
                    state_revision_at_creation=1,
                    created_at=datetime.now(timezone.utc),
                )

        # Bill and transaction: fails
        with self.assertRaises(IntegrityError):
            with transaction.atomic():
                BookkeepingRoutingDecision.objects.create(
                    id="route-fail-bill-tx",
                    entity=self.entity,
                    bill=self.bill,
                    transaction=self.transaction,
                    bank_account=self.bank_account,
                    source="ENGINE",
                    state_revision_at_creation=1,
                    created_at=datetime.now(timezone.utc),
                )

        # Neither: fails
        with self.assertRaises(IntegrityError):
            with transaction.atomic():
                BookkeepingRoutingDecision.objects.create(
                    id="route-fail-neither",
                    entity=self.entity,
                    bank_account=self.bank_account,
                    source="ENGINE",
                    state_revision_at_creation=1,
                    created_at=datetime.now(timezone.utc),
                )

        # Exactly one (bill): succeeds
        route_ok = BookkeepingRoutingDecision.objects.create(
            id="route-ok-bill",
            entity=self.entity,
            bill=self.bill,
            bank_account=self.bank_account,
            source="ENGINE",
            state_revision_at_creation=1,
            created_at=datetime.now(timezone.utc),
        )
        self.assertIsNotNone(route_ok.pk)

        # Exactly one (transaction): succeeds
        route_tx_ok = BookkeepingRoutingDecision.objects.create(
            id="route-ok-tx",
            entity=self.entity,
            transaction=self.transaction,
            bank_account=self.bank_account,
            source="ENGINE",
            state_revision_at_creation=1,
            created_at=datetime.now(timezone.utc),
        )
        self.assertIsNotNone(route_tx_ok.pk)

    def test_classification_target_xor_constraint(self) -> None:
        with self.assertRaises(IntegrityError):
            with transaction.atomic():
                BookkeepingClassificationDecision.objects.create(
                    id="cls-fail-neither",
                    entity=self.entity,
                    account_code="6111",
                    source="RULE",
                    state_revision_at_decision=1,
                    created_at=datetime.now(timezone.utc),
                )

        cls_ok = BookkeepingClassificationDecision.objects.create(
            id="cls-ok-tx",
            entity=self.entity,
            transaction=self.transaction,
            account_code="6111",
            source="RULE",
            state_revision_at_decision=1,
            created_at=datetime.now(timezone.utc),
        )
        self.assertIsNotNone(cls_ok.pk)

    def test_evidence_target_xor_constraint(self) -> None:
        with self.assertRaises(IntegrityError):
            with transaction.atomic():
                BookkeepingEvidenceAssertion.objects.create(
                    id="ev-fail-both",
                    entity=self.entity,
                    invoice=self.invoice,
                    transaction=self.transaction,
                    evidence_type="COUNTERPARTY",
                    source="USER",
                    state_revision_at_creation=1,
                    created_at=datetime.now(timezone.utc),
                )

        ev_ok = BookkeepingEvidenceAssertion.objects.create(
            id="ev-ok-inv",
            entity=self.entity,
            invoice=self.invoice,
            evidence_type="COUNTERPARTY",
            value=str(self.customer.uuid),
            source="USER",
            state_revision_at_creation=1,
            created_at=datetime.now(timezone.utc),
        )
        self.assertIsNotNone(ev_ok.pk)

    # ------------------------------------------------------------------
    # 5. Stage 2 Bank Allocation Target XOR Constraint
    # ------------------------------------------------------------------
    def test_stage2_bank_allocation_target_xor(self) -> None:
        rec = BookkeepingReconciliation.objects.create(
            id="rec-1",
            entity=self.entity,
            state_revision_at_creation=1,
            created_at=datetime.now(timezone.utc),
        )

        with self.assertRaises(IntegrityError):
            with transaction.atomic():
                BookkeepingReconciliationBankAllocation.objects.create(
                    reconciliation=rec,
                    plaid_transaction=None,
                    staged_transaction=None,
                    amount_units=10000000,
                )

        alloc_ok = BookkeepingReconciliationBankAllocation.objects.create(
            reconciliation=rec,
            plaid_transaction=self.plaid_tx,
            amount_units=10000000,
        )
        self.assertIsNotNone(alloc_ok.pk)

    # ------------------------------------------------------------------
    # 6. Reconciliation Book Allocation Points to Transaction & Protects It
    # ------------------------------------------------------------------
    def test_reconciliation_book_allocation_points_to_transaction_and_protects(self) -> None:
        rec = BookkeepingReconciliation.objects.create(
            id="rec-book-test",
            entity=self.entity,
            state_revision_at_creation=1,
            created_at=datetime.now(timezone.utc),
        )
        alloc = BookkeepingReconciliationBookAllocation.objects.create(
            reconciliation=rec,
            transaction=self.transaction,
            amount_units=10000000,
        )
        self.assertIsNotNone(alloc.pk)

        # Deleting transaction is blocked by PROTECT
        with self.assertRaises(models.ProtectedError):
            self.transaction.delete()

        # Duplicate allocation of same transaction in same reconciliation fails uniqueness
        with self.assertRaises(IntegrityError):
            with transaction.atomic():
                BookkeepingReconciliationBookAllocation.objects.create(
                    reconciliation=rec,
                    transaction=self.transaction,
                    amount_units=5000000,
                )

    # ------------------------------------------------------------------
    # 7. AmountUnits Rejects <= 0
    # ------------------------------------------------------------------
    def test_amount_units_rejects_non_positive(self) -> None:
        rec = BookkeepingReconciliation.objects.create(
            id="rec-amt-test",
            entity=self.entity,
            state_revision_at_creation=1,
            created_at=datetime.now(timezone.utc),
        )
        with self.assertRaises(IntegrityError):
            with transaction.atomic():
                BookkeepingReconciliationBookAllocation.objects.create(
                    reconciliation=rec,
                    transaction=self.transaction,
                    amount_units=0,
                )

    # ------------------------------------------------------------------
    # 8. Stage 1 Payment Application Models
    # ------------------------------------------------------------------
    def test_stage1_payment_application_and_allocation_integrity(self) -> None:
        # Bank source XOR
        with self.assertRaises(IntegrityError):
            with transaction.atomic():
                BookkeepingPaymentApplication.objects.create(
                    id="pay-fail",
                    entity=self.entity,
                    total_amount_units=10000000,
                    direction="BANK_INFLOW",
                    currency="USD",
                    state_revision=1,
                    created_at=datetime.now(timezone.utc),
                )

        app = BookkeepingPaymentApplication.objects.create(
            id="pay-ok-1",
            entity=self.entity,
            plaid_transaction=self.plaid_tx,
            total_amount_units=10000000,
            direction="BANK_INFLOW",
            currency="USD",
            state_revision=1,
            created_at=datetime.now(timezone.utc),
        )
        self.assertIsNotNone(app.pk)

        # Uniqueness: same Plaid transaction cannot be executed twice
        with self.assertRaises(IntegrityError):
            with transaction.atomic():
                BookkeepingPaymentApplication.objects.create(
                    id="pay-duplicate-bank",
                    entity=self.entity,
                    plaid_transaction=self.plaid_tx,
                    total_amount_units=10000000,
                    direction="BANK_INFLOW",
                    currency="USD",
                    state_revision=1,
                    created_at=datetime.now(timezone.utc),
                )

        # Allocation: targets invoice, links to real cash TransactionModel
        alloc = BookkeepingPaymentApplicationAllocation.objects.create(
            payment_application=app,
            invoice=self.invoice,
            amount_units=10000000,
            cash_transaction=self.transaction,
        )
        self.assertIsNotNone(alloc.pk)
        self.assertEqual(alloc.cash_transaction.journal_entry, self.journal_entry)

        # PROTECT on cash_transaction
        with self.assertRaises(models.ProtectedError):
            self.transaction.delete()

        # Obligation target XOR: both invoice and bill fails
        with self.assertRaises(IntegrityError):
            with transaction.atomic():
                BookkeepingPaymentApplicationAllocation.objects.create(
                    payment_application=app,
                    invoice=self.invoice,
                    bill=self.bill,
                    amount_units=5000000,
                    cash_transaction=self.transaction,
                )

        # Obligation target XOR: neither invoice nor bill fails
        with self.assertRaises(IntegrityError):
            with transaction.atomic():
                BookkeepingPaymentApplicationAllocation.objects.create(
                    payment_application=app,
                    amount_units=5000000,
                    cash_transaction=self.transaction,
                )

        # Bill allocation succeeds
        alloc_bill = BookkeepingPaymentApplicationAllocation.objects.create(
            payment_application=app,
            bill=self.bill,
            amount_units=5000000,
            cash_transaction=self.transaction,
        )
        self.assertIsNotNone(alloc_bill.pk)

    # ------------------------------------------------------------------
    # 9. Multi-Obligation Provenance with Exact Amounts
    # ------------------------------------------------------------------
    def test_stage1_multi_obligation_exact_amounts_provenance(self) -> None:
        """
        Tests 1 BankItem -> N Obligations with separate cash transactions and exact amounts:
        Bank 1000 -> Invoice (600, T1) + Bill (400, T2).
        """
        # Create second journal entry and cash transaction
        je2 = JournalEntryModel.objects.create(
            ledger=self.standalone_ledger,
            timestamp=datetime(2026, 1, 15, 12, 0, 0, tzinfo=timezone.utc),
            description="Test payment journal entry 2",
            posted=False,
        )
        tx2 = TransactionModel.objects.create(
            journal_entry=je2,
            account=self.cash_account,
            amount=Decimal("400.00"),
            tx_type=TransactionModel.CREDIT,
        )
        je2.posted = True
        je2.save(update_fields=["posted"], verify=False)

        app = BookkeepingPaymentApplication.objects.create(
            id="pay-multi-1",
            entity=self.entity,
            staged_transaction=self.staged_tx,
            total_amount_units=10000000,
            direction="BANK_INFLOW",
            currency="USD",
            state_revision=1,
            created_at=datetime.now(timezone.utc),
        )

        alloc1 = BookkeepingPaymentApplicationAllocation.objects.create(
            payment_application=app,
            invoice=self.invoice,
            amount_units=6000000,
            cash_transaction=self.transaction,
        )
        alloc2 = BookkeepingPaymentApplicationAllocation.objects.create(
            payment_application=app,
            bill=self.bill,
            amount_units=4000000,
            cash_transaction=tx2,
        )

        allocations = list(app.allocations.order_by("amount_units"))
        self.assertEqual(len(allocations), 2)
        self.assertEqual(allocations[0].amount_units, 4000000)
        self.assertEqual(allocations[0].bill, self.bill)
        self.assertEqual(allocations[0].cash_transaction, tx2)
        self.assertEqual(allocations[1].amount_units, 6000000)
        self.assertEqual(allocations[1].invoice, self.invoice)
        self.assertEqual(allocations[1].cash_transaction, self.transaction)

        total_alloc = sum(a.amount_units for a in allocations)
        self.assertEqual(total_alloc, app.total_amount_units)
