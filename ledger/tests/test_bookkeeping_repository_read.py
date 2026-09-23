from __future__ import annotations

from datetime import date, datetime, timezone as dt_timezone
from decimal import Decimal

from django.contrib.auth import get_user_model
from django.test import TestCase

from bookkeeping_state.domain.enums import Direction, SourceType
from bookkeeping_state.hydration import BookkeepingHydrator
from bookkeeping_state.persistence.repository import (
    BookkeepingRepository,
    BookkeepingSnapshot,
    PersistenceError,
    PersistenceWriteSet,
)
from bookkeeping_state.state.validation import validate_state
from bookkeeping_state_eval.persistence.in_memory import InMemoryBookkeepingRepository
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
    PlaidItem,
    PlaidTransaction,
    StagedTransactionModel,
    TransactionModel,
    VendorModel,
)

UserModel = get_user_model()


class BookkeepingRepositoryReadIntegrationTests(TestCase):
    """
    Comprehensive regression test suite for production BookkeepingRepository reading from Django ledger models.
    """

    def setUp(self) -> None:
        super().setUp()

        # 1. Administrator User
        self.user = UserModel.objects.create_user(
            username="repo_test_admin",
            email="admin@testbookkeeping.com",
            password="secure_password_123",
        )

        # 2. Entity
        self.entity = EntityModel.add_root(
            name="Alpha Corp",
            admin=self.user,
            currency="USD",
            fy_start_month=1,
            accrual_method=False,
        )

        # 3. Chart of Accounts & GL Accounts
        self.coa = self.entity.create_chart_of_accounts(
            coa_name="Alpha Primary CoA",
            assign_as_default=True,
            commit=True,
        )
        asset_root = self.coa.accountmodel_set.get(code="01000000")

        self.cash_account = asset_root.add_child(
            coa_model=self.coa,
            code="1010",
            name="Main Operating Checking",
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

        self.accounts_payable_account = liability_root.add_child(
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

        # 4. Bank Accounts: 1 Manual, 1 Plaid
        self.manual_bank_account = BankAccountModel.objects.create(
            name="Manual Operating Bank",
            entity_model=self.entity,
            account_model=self.cash_account,
            connection_type="manual",
            account_number="CHK-9988",
            active=True,
        )

        self.plaid_item = PlaidItem.objects.create(
            user=self.user,
            item_id="plaid_item_test_123",
            access_token="access-sandbox-token",
            metadata={"institution": {"name": "First National Bank"}},
        )

        self.plaid_bank_account = BankAccountModel.objects.create(
            name="Plaid Payroll Bank",
            entity_model=self.entity,
            account_model=self.cash_account,
            connection_type="plaid",
            plaid_item=self.plaid_item,
            metadata={"account_id": "plaid_acc_chase_001"},
            account_number="CHK-7744",
            active=True,
        )

        # 5. Counterparties: Customer and Vendor
        self.customer = CustomerModel.objects.create(
            customer_name="Globex Corporation",
            customer_number="CUST-001",
            entity_model=self.entity,
        )

        self.vendor = VendorModel.objects.create(
            vendor_name="Initech Supplies",
            vendor_number="VEND-001",
            tax_id_number="TAX-998877",
            entity_model=self.entity,
        )

        # 6. Source Documents: Open Invoice and Open Bill
        # 6a. Invoice: 150.00 due, 50.00 paid -> 100.00 remaining
        inv = InvoiceModel(
            cash_account=self.cash_account,
            prepaid_account=self.receivables_account,
            unearned_account=self.unearned_revenue_account,
            accrue=False,
        )
        _, inv = inv.configure(entity_slug=self.entity, user_model=self.user)
        inv.amount_due = Decimal("150.00")
        inv.amount_paid = Decimal("50.00")
        inv.date_draft = date(2026, 1, 15)
        inv.invoice_status = InvoiceModel.INVOICE_STATUS_APPROVED
        inv.customer = self.customer
        inv.clean()
        inv.save()
        self.invoice = inv

        # 6b. Bill: 80.00 due, 0.00 paid -> 80.00 remaining
        bill = BillModel(
            cash_account=self.cash_account,
            prepaid_account=self.prepaid_account,
            unearned_account=self.accounts_payable_account,
            accrue=False,
        )
        _, bill = bill.configure(entity_slug=self.entity, user_model=self.user)
        bill.amount_due = Decimal("80.00")
        bill.amount_paid = Decimal("0.00")
        bill.date_draft = date(2026, 1, 18)
        bill.bill_status = BillModel.BILL_STATUS_APPROVED
        bill.vendor = self.vendor
        bill.clean()
        bill.save()
        self.bill = bill

        # 7. Standalone posted journal transaction on cash account (not invoice/bill)
        standalone_ledger = LedgerModel.objects.create(
            name="Operating Activities Ledger",
            entity=self.entity,
            posted=True,
        )
        journal_entry = JournalEntryModel.objects.create(
            ledger=standalone_ledger,
            description="Branch Cash Deposit",
            posted=False,
            timestamp=datetime(2026, 1, 20, 10, 0, 0, tzinfo=dt_timezone.utc),
        )
        self.standalone_tx = TransactionModel.objects.create(
            journal_entry=journal_entry,
            account=self.cash_account,
            amount=Decimal("25.00"),
            tx_type=DEBIT,
            reconciled=False,
            description="Counter Deposit",
        )
        TransactionModel.objects.create(
            journal_entry=journal_entry,
            account=self.revenue_account,
            amount=Decimal("25.00"),
            tx_type=CREDIT,
            reconciled=False,
            description="Operational Revenue Counterpart",
        )
        journal_entry.posted = True
        journal_entry.save(update_fields=["posted"], verify=False)

        # 8. Bank Transactions
        # 8a. Manual bank: StagedTransactionModel (inflow 100.00)
        self.import_job = ImportJobModel.objects.create(
            description="January Bank Statement",
            bank_account_model=self.manual_bank_account,
        )
        self.staged_tx = StagedTransactionModel.objects.create(
            import_job=self.import_job,
            amount=Decimal("100.00"),
            date_posted=date(2026, 1, 15),
            name="Incoming Wire Transfer",
            fit_id="WIRE-100-2026",
        )

        # 8b. Plaid bank: PlaidTransaction (outflow 45.00 in Plaid semantics)
        self.plaid_tx = PlaidTransaction.objects.create(
            plaid_item=self.plaid_item,
            account_id="plaid_acc_chase_001",
            transaction_id="plaid_tx_unique_999",
            amount=Decimal("45.00"),
            date=date(2026, 1, 19),
            name="Office Depot",
            merchant_name="Office Depot",
            iso_currency_code="USD",
            pending=False,
            is_current=True,
            is_removed=False,
        )

        self.repository = BookkeepingRepository()

    # ------------------------------------------------------------------
    # Canonical Identity & Lookup Tests
    # ------------------------------------------------------------------

    def test_canonical_company_identity_slug_vs_uuid(self) -> None:
        """
        Verify lookup by slug and by UUID produces identical canonical UUID company_id
        and identical snapshot contents.
        """
        snapshot_by_slug = self.repository.load_snapshot(company_id=self.entity.slug)
        snapshot_by_uuid = self.repository.load_snapshot(company_id=str(self.entity.uuid))

        self.assertEqual(snapshot_by_slug.context.company_id, str(self.entity.uuid))
        self.assertEqual(snapshot_by_uuid.context.company_id, str(self.entity.uuid))
        self.assertEqual(snapshot_by_slug, snapshot_by_uuid)
        self.assertEqual(snapshot_by_slug.context.base_currency, "USD")

    def test_unknown_company_id_fails_explicitly(self) -> None:
        """Verify invalid or missing entity raises PersistenceError."""
        with self.assertRaises(PersistenceError) as ctx:
            self.repository.load_snapshot(company_id="unknown-company-slug-xyz")
        self.assertIn("Entity not found", str(ctx.exception))

    # ------------------------------------------------------------------
    # Invoice & Bill Lifecycle and Cash Leg Tests
    # ------------------------------------------------------------------

    def test_approved_unpaid_invoice_produces_staging_obligation(self) -> None:
        """A. Approved unpaid invoice appears as STAGING_BOOK_ITEM."""
        inv = InvoiceModel(
            cash_account=self.cash_account,
            prepaid_account=self.receivables_account,
            unearned_account=self.unearned_revenue_account,
            accrue=False,
        )
        _, inv = inv.configure(entity_slug=self.entity, user_model=self.user)
        inv.amount_due = Decimal("200.00")
        inv.amount_paid = Decimal("0.00")
        inv.date_draft = date(2026, 2, 1)
        inv.invoice_status = InvoiceModel.INVOICE_STATUS_APPROVED
        inv.customer = self.customer
        inv.clean()
        inv.save()

        snapshot = self.repository.load_snapshot(company_id=str(self.entity.uuid))
        item = next(b for b in snapshot.book_items if b.id == f"invoice:{inv.uuid}")

        self.assertEqual(item.source_type, SourceType.STAGING_BOOK_ITEM)
        self.assertEqual(item.amount_units, "2000000")
        self.assertEqual(item.direction, Direction.BOOK_BANK_DEBIT)
        self.assertEqual(item.counterparty_id, str(self.customer.uuid))

    def test_paid_invoice_cash_je_created_bank_not_reconciled(self) -> None:
        """
        B. Paid invoice has no remaining staging capacity, but its posted cash JE
        leg DOES appear as a POSTED_BOOK_ITEM.
        """
        inv = InvoiceModel(
            cash_account=self.cash_account,
            prepaid_account=self.receivables_account,
            unearned_account=self.unearned_revenue_account,
            accrue=False,
        )
        _, inv = inv.configure(entity_slug=self.entity, user_model=self.user)
        inv.amount_due = Decimal("200.00")
        inv.amount_paid = Decimal("200.00")  # Fully paid
        inv.date_draft = date(2026, 2, 5)
        inv.invoice_status = InvoiceModel.INVOICE_STATUS_APPROVED
        inv.customer = self.customer
        inv.clean()
        inv.save()

        # Payment JE in invoice's ledger
        payment_je = JournalEntryModel.objects.create(
            ledger=inv.ledger,
            description=f"Full Payment for Invoice {inv.invoice_number}",
            posted=False,
            timestamp=datetime(2026, 2, 6, 12, 0, 0, tzinfo=dt_timezone.utc),
        )
        cash_tx = TransactionModel.objects.create(
            journal_entry=payment_je,
            account=self.cash_account,
            amount=Decimal("200.00"),
            tx_type=DEBIT,  # Debit Cash
            reconciled=False,
            description="Invoice Cash Payment Received",
        )
        TransactionModel.objects.create(
            journal_entry=payment_je,
            account=self.receivables_account,
            amount=Decimal("200.00"),
            tx_type=CREDIT,
            reconciled=False,
            description="A/R Cleared",
        )
        payment_je.posted = True
        payment_je.save(update_fields=["posted"], verify=False)

        snapshot = self.repository.load_snapshot(company_id=str(self.entity.uuid))

        # 1. Invoice staging item must NOT exist (remaining = 0)
        self.assertFalse(any(b.id == f"invoice:{inv.uuid}" for b in snapshot.book_items))

        # 2. Payment cash leg MUST exist as POSTED_BOOK_ITEM
        tx_item = next(b for b in snapshot.book_items if b.id == f"tx:{cash_tx.uuid}")
        self.assertEqual(tx_item.source_type, SourceType.POSTED_BOOK_ITEM)
        self.assertEqual(tx_item.amount_units, "2000000")
        self.assertEqual(tx_item.direction, Direction.BOOK_BANK_DEBIT)
        self.assertEqual(tx_item.counterparty_id, str(self.customer.uuid))
        self.assertIn(str(inv.uuid), tx_item.provenance_refs)

    def test_partial_invoice_payment(self) -> None:
        """
        C. Partial payment:
        - Invoice staging item reflects remaining obligation (125.00).
        - Posted cash transaction reflects the paid amount (75.00).
        - Total capacity is conserved (125 + 75 = 200).
        """
        inv = InvoiceModel(
            cash_account=self.cash_account,
            prepaid_account=self.receivables_account,
            unearned_account=self.unearned_revenue_account,
            accrue=False,
        )
        _, inv = inv.configure(entity_slug=self.entity, user_model=self.user)
        inv.amount_due = Decimal("200.00")
        inv.amount_paid = Decimal("75.00")  # Partial payment
        inv.date_draft = date(2026, 2, 7)
        inv.invoice_status = InvoiceModel.INVOICE_STATUS_APPROVED
        inv.customer = self.customer
        inv.clean()
        inv.save()

        # Payment JE for 75.00
        payment_je = JournalEntryModel.objects.create(
            ledger=inv.ledger,
            description=f"Partial Payment for Invoice {inv.invoice_number}",
            posted=False,
            timestamp=datetime(2026, 2, 8, 12, 0, 0, tzinfo=dt_timezone.utc),
        )
        cash_tx = TransactionModel.objects.create(
            journal_entry=payment_je,
            account=self.cash_account,
            amount=Decimal("75.00"),
            tx_type=DEBIT,
            reconciled=False,
            description="Partial Payment Leg",
        )
        TransactionModel.objects.create(
            journal_entry=payment_je,
            account=self.receivables_account,
            amount=Decimal("75.00"),
            tx_type=CREDIT,
            reconciled=False,
        )
        payment_je.posted = True
        payment_je.save(update_fields=["posted"], verify=False)

        snapshot = self.repository.load_snapshot(company_id=str(self.entity.uuid))

        # Staging item: remaining 125.00
        inv_item = next(b for b in snapshot.book_items if b.id == f"invoice:{inv.uuid}")
        self.assertEqual(inv_item.source_type, SourceType.STAGING_BOOK_ITEM)
        self.assertEqual(inv_item.amount_units, "1250000")

        # Posted cash leg: 75.00
        cash_item = next(b for b in snapshot.book_items if b.id == f"tx:{cash_tx.uuid}")
        self.assertEqual(cash_item.source_type, SourceType.POSTED_BOOK_ITEM)
        self.assertEqual(cash_item.amount_units, "750000")

    def test_multiple_payments_for_one_invoice(self) -> None:
        """
        D. Multiple payments for one invoice produce distinct POSTED_BOOK_ITEM lines.
        """
        inv = InvoiceModel(
            cash_account=self.cash_account,
            prepaid_account=self.receivables_account,
            unearned_account=self.unearned_revenue_account,
            accrue=False,
        )
        _, inv = inv.configure(entity_slug=self.entity, user_model=self.user)
        inv.amount_due = Decimal("200.00")
        inv.amount_paid = Decimal("110.00")  # 50 + 60
        inv.date_draft = date(2026, 2, 10)
        inv.invoice_status = InvoiceModel.INVOICE_STATUS_APPROVED
        inv.customer = self.customer
        inv.clean()
        inv.save()

        # Payment 1: 50.00
        je1 = JournalEntryModel.objects.create(
            ledger=inv.ledger,
            description="Installment 1",
            posted=False,
            timestamp=datetime(2026, 2, 11, 10, 0, 0, tzinfo=dt_timezone.utc),
        )
        tx1 = TransactionModel.objects.create(
            journal_entry=je1,
            account=self.cash_account,
            amount=Decimal("50.00"),
            tx_type=DEBIT,
            reconciled=False,
        )
        TransactionModel.objects.create(
            journal_entry=je1,
            account=self.receivables_account,
            amount=Decimal("50.00"),
            tx_type=CREDIT,
            reconciled=False,
        )
        je1.posted = True
        je1.save(update_fields=["posted"], verify=False)

        # Payment 2: 60.00
        je2 = JournalEntryModel.objects.create(
            ledger=inv.ledger,
            description="Installment 2",
            posted=False,
            timestamp=datetime(2026, 2, 12, 10, 0, 0, tzinfo=dt_timezone.utc),
        )
        tx2 = TransactionModel.objects.create(
            journal_entry=je2,
            account=self.cash_account,
            amount=Decimal("60.00"),
            tx_type=DEBIT,
            reconciled=False,
        )
        TransactionModel.objects.create(
            journal_entry=je2,
            account=self.receivables_account,
            amount=Decimal("60.00"),
            tx_type=CREDIT,
            reconciled=False,
        )
        je2.posted = True
        je2.save(update_fields=["posted"], verify=False)

        snapshot = self.repository.load_snapshot(company_id=str(self.entity.uuid))

        # Remaining invoice staging: 200 - 110 = 90.00
        inv_item = next(b for b in snapshot.book_items if b.id == f"invoice:{inv.uuid}")
        self.assertEqual(inv_item.amount_units, "900000")

        # Two distinct cash legs
        item1 = next(b for b in snapshot.book_items if b.id == f"tx:{tx1.uuid}")
        item2 = next(b for b in snapshot.book_items if b.id == f"tx:{tx2.uuid}")
        self.assertEqual(item1.amount_units, "500000")
        self.assertEqual(item2.amount_units, "600000")

    def test_bill_payment_symmetric_outflow_behavior(self) -> None:
        """
        E. Bill payment creates credit to Cash, projecting to Direction.BOOK_BANK_CREDIT.
        """
        bill = BillModel(
            cash_account=self.cash_account,
            prepaid_account=self.prepaid_account,
            unearned_account=self.accounts_payable_account,
            accrue=False,
        )
        _, bill = bill.configure(entity_slug=self.entity, user_model=self.user)
        bill.amount_due = Decimal("300.00")
        bill.amount_paid = Decimal("300.00")  # Fully paid
        bill.date_draft = date(2026, 2, 15)
        bill.bill_status = BillModel.BILL_STATUS_APPROVED
        bill.vendor = self.vendor
        bill.clean()
        bill.save()

        # Payment JE: Credit Cash
        payment_je = JournalEntryModel.objects.create(
            ledger=bill.ledger,
            description="Bill Payment",
            posted=False,
            timestamp=datetime(2026, 2, 16, 12, 0, 0, tzinfo=dt_timezone.utc),
        )
        cash_tx = TransactionModel.objects.create(
            journal_entry=payment_je,
            account=self.cash_account,
            amount=Decimal("300.00"),
            tx_type=CREDIT,  # Credit Cash = outflow
            reconciled=False,
            description="Disbursement to Vendor",
        )
        TransactionModel.objects.create(
            journal_entry=payment_je,
            account=self.accounts_payable_account,
            amount=Decimal("300.00"),
            tx_type=DEBIT,
            reconciled=False,
        )
        payment_je.posted = True
        payment_je.save(update_fields=["posted"], verify=False)

        snapshot = self.repository.load_snapshot(company_id=str(self.entity.uuid))

        # Bill staging item is excluded because remaining = 0
        self.assertFalse(any(b.id == f"bill:{bill.uuid}" for b in snapshot.book_items))

        # Cash leg appears as credit
        cash_item = next(b for b in snapshot.book_items if b.id == f"tx:{cash_tx.uuid}")
        self.assertEqual(cash_item.direction, Direction.BOOK_BANK_CREDIT)
        self.assertEqual(cash_item.amount_units, "3000000")
        self.assertEqual(cash_item.counterparty_id, str(self.vendor.uuid))
        self.assertIn(str(bill.uuid), cash_item.provenance_refs)

    def test_reconciled_cash_transaction_is_excluded(self) -> None:
        """
        F. Cash transactions marked reconciled=True in Django are excluded from open BookItems.
        """
        reconciled_ledger = LedgerModel.objects.create(
            name="Reconciled Activity Ledger",
            entity=self.entity,
            posted=True,
        )
        je = JournalEntryModel.objects.create(
            ledger=reconciled_ledger,
            description="Previously Reconciled Cash",
            posted=False,
            timestamp=datetime(2026, 2, 20, 10, 0, 0, tzinfo=dt_timezone.utc),
        )
        reconciled_tx = TransactionModel.objects.create(
            journal_entry=je,
            account=self.cash_account,
            amount=Decimal("500.00"),
            tx_type=DEBIT,
            reconciled=True,  # Reconciled!
            description="Reconciled Bank Movement",
        )
        TransactionModel.objects.create(
            journal_entry=je,
            account=self.revenue_account,
            amount=Decimal("500.00"),
            tx_type=CREDIT,
            reconciled=True,
        )
        je.posted = True
        je.save(update_fields=["posted"], verify=False)

        snapshot = self.repository.load_snapshot(company_id=str(self.entity.uuid))
        self.assertFalse(any(b.id == f"tx:{reconciled_tx.uuid}" for b in snapshot.book_items))

    # ------------------------------------------------------------------
    # Bank Sign Conventions & Scoping Tests
    # ------------------------------------------------------------------

    def test_plaid_transactions_excluded_from_snapshot_bank_items(self) -> None:
        """
        H. Plaid transactions are upstream staging records and MUST NOT hydrate into snapshot.bank_items.
        Direct normalization logic in bank_normalization.py remains unit-tested separately.
        """
        from bookkeeping_state.persistence.bank_normalization import normalize_plaid_movement

        ptx_outflow = PlaidTransaction.objects.create(
            plaid_item=self.plaid_item,
            account_id="plaid_acc_chase_001",
            transaction_id="tx_out_101",
            amount=Decimal("85.50"),  # positive = outflow in Plaid
            date=date(2026, 2, 21),
            name="Store Purchase",
            pending=False,
            is_current=True,
            is_removed=False,
        )
        ptx_inflow = PlaidTransaction.objects.create(
            plaid_item=self.plaid_item,
            account_id="plaid_acc_chase_001",
            transaction_id="tx_in_102",
            amount=Decimal("-120.00"),  # negative = inflow in Plaid
            date=date(2026, 2, 22),
            name="Refund Received",
            pending=False,
            is_current=True,
            is_removed=False,
        )

        # 1. Verify Plaid normalization utility itself
        abs_out, dir_out, curr_out = normalize_plaid_movement(ptx_outflow)
        self.assertEqual(dir_out, Direction.BANK_OUTFLOW)
        self.assertEqual(abs_out, Decimal("85.50"))

        abs_in, dir_in, curr_in = normalize_plaid_movement(ptx_inflow)
        self.assertEqual(dir_in, Direction.BANK_INFLOW)
        self.assertEqual(abs_in, Decimal("120.00"))

        # 2. Verify neither Plaid transaction appears in snapshot.bank_items
        snapshot = self.repository.load_snapshot(company_id=str(self.entity.uuid))
        self.assertFalse(any(b.id == f"plaid:{ptx_outflow.uuid}" for b in snapshot.bank_items))
        self.assertFalse(any(b.id == f"plaid:{ptx_inflow.uuid}" for b in snapshot.bank_items))
        self.assertFalse(any(b.id.startswith("plaid:") for b in snapshot.bank_items))

    def test_staged_direction_semantics_positive_inflow_negative_outflow(self) -> None:
        """
        I. Staged / statement sign conventions:
        - positive amount -> BANK_INFLOW
        - negative amount -> BANK_OUTFLOW
        """
        stx_inflow = StagedTransactionModel.objects.create(
            import_job=self.import_job,
            amount=Decimal("350.00"),  # positive = inflow
            date_posted=date(2026, 2, 23),
            name="Customer Wire Deposit",
            fit_id="FIT-IN-350",
        )
        stx_outflow = StagedTransactionModel.objects.create(
            import_job=self.import_job,
            amount=Decimal("-65.25"),  # negative = outflow
            date_posted=date(2026, 2, 24),
            name="ATM Cash Withdrawal",
            fit_id="FIT-OUT-65",
        )

        snapshot = self.repository.load_snapshot(company_id=str(self.entity.uuid))

        in_item = next(b for b in snapshot.bank_items if b.id == f"staged:{stx_inflow.uuid}")
        self.assertEqual(in_item.direction, Direction.BANK_INFLOW)
        self.assertEqual(in_item.amount_units, "3500000")

        out_item = next(b for b in snapshot.bank_items if b.id == f"staged:{stx_outflow.uuid}")
        self.assertEqual(out_item.direction, Direction.BANK_OUTFLOW)
        self.assertEqual(out_item.amount_units, "652500")

    def test_plaid_transactions_not_hydrated_across_multiple_accounts(self) -> None:
        """
        J. A single PlaidItem representing multiple accounts does not hydrate transactions
        into snapshot.bank_items.
        """
        savings_bank_account = BankAccountModel.objects.create(
            name="Plaid Savings Account",
            entity_model=self.entity,
            account_model=self.cash_account,
            connection_type="plaid",
            plaid_item=self.plaid_item,
            metadata={"account_id": "plaid_acc_chase_002"},
            active=True,
        )

        tx_checking = PlaidTransaction.objects.create(
            plaid_item=self.plaid_item,
            account_id="plaid_acc_chase_001",
            transaction_id="tx_chk_unique",
            amount=Decimal("50.00"),
            date=date(2026, 2, 25),
            name="Checking Movement",
            pending=False,
            is_current=True,
            is_removed=False,
        )
        tx_savings = PlaidTransaction.objects.create(
            plaid_item=self.plaid_item,
            account_id="plaid_acc_chase_002",
            transaction_id="tx_sav_unique",
            amount=Decimal("75.00"),
            date=date(2026, 2, 25),
            name="Savings Movement",
            pending=False,
            is_current=True,
            is_removed=False,
        )

        snapshot = self.repository.load_snapshot(company_id=str(self.entity.uuid))

        # No Plaid transactions enter bank_items
        self.assertFalse(any(b.id == f"plaid:{tx_checking.uuid}" for b in snapshot.bank_items))
        self.assertFalse(any(b.id == f"plaid:{tx_savings.uuid}" for b in snapshot.bank_items))
        self.assertFalse(any(b.id.startswith("plaid:") for b in snapshot.bank_items))

    def test_staged_multi_account_scoping_partitions_without_duplication(self) -> None:
        """
        J2. Authoritative StagedTransactionModel records across multiple bank accounts
        partition strictly by bank_account_id without duplication.
        """
        second_account = BankAccountModel.objects.create(
            name="Second Operating Account",
            entity_model=self.entity,
            account_model=self.cash_account,
            connection_type="manual",
            active=True,
        )
        second_import_job = ImportJobModel.objects.create(
            description="February Bank Statement",
            bank_account_model=second_account,
        )
        stx_second = StagedTransactionModel.objects.create(
            import_job=second_import_job,
            amount=Decimal("200.00"),
            date_posted=date(2026, 2, 25),
            name="Deposit in Second Account",
            fit_id="FIT-200-SEC",
        )

        snapshot = self.repository.load_snapshot(company_id=str(self.entity.uuid))

        item_first = next(b for b in snapshot.bank_items if b.id == f"staged:{self.staged_tx.uuid}")
        item_second = next(b for b in snapshot.bank_items if b.id == f"staged:{stx_second.uuid}")

        self.assertEqual(item_first.bank_account_id, str(self.manual_bank_account.uuid))
        self.assertEqual(item_second.bank_account_id, str(second_account.uuid))

        bank_item_ids = [bi.id for bi in snapshot.bank_items]
        self.assertEqual(len(bank_item_ids), len(set(bank_item_ids)))

    # ------------------------------------------------------------------
    # Counterparty Tax ID Mapping Tests
    # ------------------------------------------------------------------

    def test_vendor_tax_id_mapping(self) -> None:
        """
        K. VendorModel tax_id_number is mapped to Counterparty.tax_id.
        CustomerModel has no tax ID and remains None.
        """
        snapshot = self.repository.load_snapshot(company_id=str(self.entity.uuid))

        vend_cp = next(c for c in snapshot.counterparties if c.id == str(self.vendor.uuid))
        cust_cp = next(c for c in snapshot.counterparties if c.id == str(self.customer.uuid))

        self.assertEqual(vend_cp.tax_id, "TAX-998877")
        self.assertIsNone(cust_cp.tax_id)

    # ------------------------------------------------------------------
    # End-to-End Pipeline & Semantic Parity Tests
    # ------------------------------------------------------------------

    def test_hydrator_pipeline_validates_state(self) -> None:
        """
        Verify pure pipeline:
        Django ORM -> BookkeepingRepository -> BookkeepingSnapshot -> BookkeepingHydrator -> BookkeepingState
        """
        hydrator = BookkeepingHydrator(repository=self.repository)
        state = hydrator.hydrate(company_id=str(self.entity.uuid))

        self.assertEqual(state.context.company_id, str(self.entity.uuid))
        self.assertEqual(len(state.bank_accounts), 2)
        self.assertEqual(len(state.bank_items), 1)
        self.assertIn(f"staged:{self.staged_tx.uuid}", state.bank_items)
        self.assertNotIn(f"plaid:{self.plaid_tx.uuid}", state.bank_items)
        self.assertFalse(any(b_id.startswith("plaid:") for b_id in state.bank_items))
        self.assertEqual(len(state.book_items), 3)
        self.assertEqual(len(state.counterparties), 2)

        # Validate that state adheres to all domain invariants
        report = validate_state(state)
        self.assertTrue(report.is_valid, f"Validation errors: {report.errors}")
        self.assertEqual(len(report.errors), 0)

    def test_semantic_parity_with_in_memory_repository(self) -> None:
        """
        Verify that a state hydrated from the production BookkeepingRepository
        matches the state hydrated from InMemoryBookkeepingRepository seeded with the same snapshot.
        """
        prod_snapshot = self.repository.load_snapshot(company_id=str(self.entity.uuid))

        in_memory_repo = InMemoryBookkeepingRepository(initial_snapshots=[prod_snapshot])
        in_memory_snapshot = in_memory_repo.load_snapshot(company_id=str(self.entity.uuid))

        self.assertEqual(prod_snapshot, in_memory_snapshot)

        prod_state = BookkeepingHydrator(repository=self.repository).hydrate(company_id=str(self.entity.uuid))
        eval_state = BookkeepingHydrator(repository=in_memory_repo).hydrate(company_id=str(self.entity.uuid))

        self.assertEqual(prod_state.context.company_id, eval_state.context.company_id)
        self.assertEqual(prod_state.bank_accounts, eval_state.bank_accounts)
        self.assertEqual(prod_state.bank_items, eval_state.bank_items)
        self.assertEqual(prod_state.book_items, eval_state.book_items)
        self.assertEqual(prod_state.counterparties, eval_state.counterparties)

        prod_report = validate_state(prod_state)
        eval_report = validate_state(eval_state)
        self.assertEqual(prod_report.is_valid, eval_report.is_valid)
        self.assertEqual(prod_report.errors, eval_report.errors)

    def test_commit_supported_and_succeeds(self) -> None:
        """Verify write operations are now supported and empty write-set returns commit result."""
        write_set = PersistenceWriteSet()
        result = self.repository.commit(
            company_id=str(self.entity.uuid),
            expected_revision=0,
            write_set=write_set,
        )
        self.assertEqual(result.previous_revision, 0)
        self.assertEqual(result.new_revision, 0)

