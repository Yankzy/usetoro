from __future__ import annotations

from datetime import date, datetime, timezone
from decimal import Decimal
from typing import Any, Sequence
import os
import uuid

from django.contrib.auth import get_user_model
from django.test import TestCase

from unittest.mock import patch

from bookkeeping_state.domain.commands import (
    ApplyPaymentCommand,
    CommandSource,
    CreateReconciliationCommand,
    CreateRoutingDecisionCommand,
    PaymentApplicationObligationAllocation,
    RoutingDecisionSource,
    Stage1ExecutionCapability,
    get_stage1_capability_secret,
    mint_stage1_capability,
    verify_stage1_capability,
)
from bookkeeping_state.domain.enums import Direction, SourceType
from bookkeeping_state.domain.money import major_units_to_amount_units
from bookkeeping_state.domain.reconciliations import (
    BankAllocation,
    BookAllocation,
)
from bookkeeping_state.hydration.hydrator import BookkeepingHydrator
from bookkeeping_state.payment_application.coordinator import (
    plan_two_stage_bookkeeping,
)
from bookkeeping_state.persistence.repository import BookkeepingRepository
from bookkeeping_state.state.fingerprint import state_fingerprint
from bookkeeping_state.state.queries import BookkeepingQueries
from bookkeeping_state.transitions.batch import TransitionBatch
from bookkeeping_state.transitions.engine import TransitionEngine
from bookkeeping_state.transitions.result import RejectionCode, TransitionStatus
from ledger.io import (
    ASSET_CA_CASH,
    ASSET_CA_PREPAID,
    ASSET_CA_RECEIVABLES,
    EXPENSE_OPERATIONAL,
    INCOME_OPERATIONAL,
    LIABILITY_CL_ACC_PAYABLE,
    LIABILITY_CL_DEFERRED_REVENUE,
)
from ledger.io.roles import DEBIT, CREDIT
from ledger.models.bank_account import BankAccountModel
from ledger.models.bill import BillModel
from ledger.models.bookkeeping import (
    BookkeepingPaymentApplication,
    BookkeepingPaymentApplicationAllocation,
    BookkeepingReconciliation,
    BookkeepingReconciliationBankAllocation,
    BookkeepingReconciliationBookAllocation,
    BookkeepingRevision,
)
from ledger.models.customer import CustomerModel
from ledger.models.data_import import ImportJobModel, StagedTransactionModel
from ledger.models.entity import EntityModel
from ledger.models.invoice import InvoiceModel
from ledger.models.items import ItemModel, ItemTransactionModel, UnitOfMeasureModel
from ledger.models.journal_entry import JournalEntryModel
from ledger.models.ledger import LedgerModel
from ledger.models.plaid import PlaidItem, PlaidTransaction
from ledger.models.transactions import TransactionModel
from ledger.models.vendor import VendorModel

UserModel: Any = get_user_model()


class Stage1AccountingExecutionTestBase(TestCase):
    def setUp(self):
        super().setUp()
        self.user = UserModel.objects.create_user(
            username=f"testuser_{uuid.uuid4().hex[:8]}",
            email="test@example.com",
            password="password123",
        )

        self.entity = EntityModel.add_root(
            name="Stage1 Corp",
            admin=self.user,
            currency="USD",
            fy_start_month=1,
            accrual_method=False,
        )

        self.coa = self.entity.create_chart_of_accounts(
            coa_name="Stage1 CoA",
            assign_as_default=True,
            commit=True,
        )
        asset_root = self.coa.accountmodel_set.get(code="01000000")

        self.cash_account_default = asset_root.add_child(
            coa_model=self.coa,
            code="1010",
            name="Default Cash",
            role=ASSET_CA_CASH,
            balance_type=DEBIT,
        )

        self.cash_account_target = asset_root.add_child(
            coa_model=self.coa,
            code="1015",
            name="Target Bank Cash",
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
            name="Sales Revenue",
            role=INCOME_OPERATIONAL,
            balance_type=CREDIT,
        )

        expense_root = self.coa.accountmodel_set.get(code="05000000")
        self.expense_account = expense_root.add_child(
            coa_model=self.coa,
            code="5010",
            name="Operating Expense",
            role=EXPENSE_OPERATIONAL,
            balance_type=DEBIT,
        )

        self.bank_account = BankAccountModel.objects.create(
            name="Primary Checking",
            entity_model=self.entity,
            account_model=self.cash_account_target,
            connection_type="manual",
            account_number="CHK-9999",
            active=True,
        )

        self.import_job = ImportJobModel.objects.create(
            description="Stage1 Import Job",
            bank_account_model=self.bank_account,
        )

        self.plaid_item = PlaidItem.objects.create(
            user=self.user,
            access_token="access-sandbox-123",
            item_id=f"plaid_item_{uuid.uuid4().hex[:8]}",
        )
        self.plaid_bank_account = BankAccountModel.objects.create(
            name="Plaid Checking",
            entity_model=self.entity,
            account_model=self.cash_account_target,
            connection_type="plaid",
            plaid_item=self.plaid_item,
            account_number="PLD-8888",
            active=True,
        )

        self.customer = CustomerModel.objects.create(
            customer_name="Alpha Client",
            customer_number="CUST-ALPHA",
            entity_model=self.entity,
        )

        self.vendor = VendorModel.objects.create(
            vendor_name="Beta Supplier",
            vendor_number="VEND-BETA",
            entity_model=self.entity,
        )

        self.uom = UnitOfMeasureModel.objects.create(
            name="Unit",
            unit_abbr=f"ea_{uuid.uuid4().hex[:4]}",
            entity=self.entity,
        )

        self.service_item = ItemModel.objects.create(
            name="Consulting Service",
            sku=f"SRV-{uuid.uuid4().hex[:4]}",
            entity=self.entity,
            uom=self.uom,
            item_role=ItemModel.ITEM_ROLE_SERVICE,
            item_type=ItemModel.ITEM_TYPE_LABOR,
            earnings_account=self.revenue_account,
            cogs_account=self.expense_account,
            for_inventory=False,
            is_product_or_service=True,
        )

        self.expense_item = ItemModel.objects.create(
            name="Office Supplies",
            sku=f"EXP-{uuid.uuid4().hex[:4]}",
            entity=self.entity,
            uom=self.uom,
            item_role=ItemModel.ITEM_ROLE_EXPENSE,
            item_type=ItemModel.ITEM_TYPE_OTHER,
            expense_account=self.expense_account,
            for_inventory=False,
            is_product_or_service=False,
        )

        self.repo = BookkeepingRepository()
        self.hydrator = BookkeepingHydrator(repository=self.repo)
        self.engine = TransitionEngine(repository=self.repo)

    def _create_approved_invoice(
        self,
        amount: Decimal,
        dt: date = date(2026, 4, 1),
    ) -> InvoiceModel:
        inv: Any = InvoiceModel(
            cash_account=self.cash_account_default,
            prepaid_account=self.receivables_account,
            unearned_account=self.unearned_revenue_account,
            accrue=False,
        )
        _, inv = inv.configure(entity_slug=self.entity, user_model=self.user)
        inv.date_draft = dt
        inv.customer = self.customer
        inv.save()

        itm = ItemTransactionModel(
            invoice_model=inv,
            item_model=self.service_item,
            quantity=1.0,
            unit_cost=float(amount),
            total_amount=amount,
        )
        itm.full_clean()
        itm.save()

        inv.update_amount_due()
        inv.invoice_status = InvoiceModel.INVOICE_STATUS_APPROVED
        inv.date_approved = dt
        inv.clean()
        inv.save()
        return inv

    def _create_approved_bill(
        self,
        amount: Decimal,
        dt: date = date(2026, 4, 1),
    ) -> BillModel:
        bill: Any = BillModel(
            cash_account=self.cash_account_default,
            prepaid_account=self.prepaid_account,
            unearned_account=self.payable_account,
            accrue=False,
        )
        _, bill = bill.configure(entity_slug=self.entity, user_model=self.user)
        bill.date_draft = dt
        bill.vendor = self.vendor
        bill.save()

        itm = ItemTransactionModel(
            bill_model=bill,
            item_model=self.expense_item,
            quantity=1.0,
            unit_cost=float(amount),
            total_amount=amount,
        )
        itm.full_clean()
        itm.save()

        bill.update_amount_due()
        bill.bill_status = BillModel.BILL_STATUS_APPROVED
        bill.date_approved = dt
        bill.clean()
        bill.save()
        return bill

    def _create_staged_movement(
        self,
        amount: Decimal,
        dt: date = date(2026, 4, 5),
        name: str = "Bank Movement",
    ) -> StagedTransactionModel:
        return StagedTransactionModel.objects.create(
            import_job=self.import_job,
            amount=amount,
            date_posted=dt,
            name=name,
        )

    def _create_cash_tx(
        self,
        amount: Decimal,
        is_debit: bool = True,
        dt: date = date(2026, 4, 1),
    ) -> TransactionModel:
        ledger = LedgerModel.objects.create(
            name=f"Ledger-{uuid.uuid4().hex[:6]}",
            entity=self.entity,
            posted=True,
        )
        je = JournalEntryModel.objects.create(
            ledger=ledger,
            timestamp=datetime(dt.year, dt.month, dt.day, 12, 0, tzinfo=timezone.utc),
            posted=False,
        )
        tx = TransactionModel.objects.create(
            journal_entry=je,
            account=self.cash_account_target,
            amount=amount,
            tx_type=TransactionModel.DEBIT if is_debit else TransactionModel.CREDIT,
        )
        TransactionModel.objects.create(
            journal_entry=je,
            account=self.revenue_account,
            amount=amount,
            tx_type=TransactionModel.CREDIT if is_debit else TransactionModel.DEBIT,
        )
        je.posted = True
        je.save(update_fields=["posted"], verify=False)
        return tx

    def _create_plaid_movement(
        self,
        amount: Decimal,
        dt: date = date(2026, 4, 5),
        name: str = "Plaid Movement",
    ) -> PlaidTransaction:
        return PlaidTransaction.objects.create(
            plaid_item=self.plaid_item,
            account_id="plaid_acc_001",
            transaction_id=f"tx_{uuid.uuid4().hex[:8]}",
            amount=amount,
            date=dt,
            name=name,
            pending=False,
            is_current=True,
            is_removed=False,
        )

    def _mint_valid_capability(
        self,
        *,
        state: Any,
        bank_item_id: str,
        bank_account_id: str | None = None,
        direction: Direction | None = None,
        currency: str = "USD",
        total_amount_units: int,
        payment_date: date | None = None,
        allocations: Sequence[Any],
        secret_key: bytes | None = None,
    ) -> Stage1ExecutionCapability:
        ba_id = bank_account_id or str(self.bank_account.uuid)
        b_item = state.bank_items.get(bank_item_id)
        p_date = payment_date or (b_item.date if b_item else date(2026, 4, 5))
        d_val = direction or (b_item.direction if b_item else Direction.BANK_INFLOW)
        return mint_stage1_capability(
            bank_item_id=bank_item_id,
            bank_account_id=ba_id,
            direction=d_val,
            currency=currency,
            total_amount_units=total_amount_units,
            payment_date=p_date,
            allocations=allocations,
            session_id=state.session_id,
            base_state_revision=state.revision,
            base_state_fingerprint=state_fingerprint(state),
            secret_key=secret_key,
        )

    def _create_approved_invoice_multi_line(
        self,
        amounts: list[Decimal],
        dt: date = date(2026, 4, 1),
    ) -> InvoiceModel:
        inv: Any = InvoiceModel(
            cash_account=self.cash_account_default,
            prepaid_account=self.receivables_account,
            unearned_account=self.unearned_revenue_account,
            accrue=False,
        )
        _, inv = inv.configure(entity_slug=self.entity, user_model=self.user)
        inv.date_draft = dt
        inv.customer = self.customer
        inv.save()

        for amt in amounts:
            itm = ItemTransactionModel(
                invoice_model=inv,
                item_model=self.service_item,
                quantity=1.0,
                unit_cost=float(amt),
                total_amount=amt,
            )
            itm.full_clean()
            itm.save()

        inv.update_amount_due()
        inv.invoice_status = InvoiceModel.INVOICE_STATUS_APPROVED
        inv.date_approved = dt
        inv.clean()
        inv.save()
        return inv

    def _create_approved_bill_multi_line(
        self,
        amounts: list[Decimal],
        dt: date = date(2026, 4, 1),
    ) -> BillModel:
        bill: Any = BillModel(
            cash_account=self.cash_account_default,
            prepaid_account=self.prepaid_account,
            unearned_account=self.payable_account,
            accrue=False,
        )
        _, bill = bill.configure(entity_slug=self.entity, user_model=self.user)
        bill.date_draft = dt
        bill.vendor = self.vendor
        bill.save()

        for amt in amounts:
            itm = ItemTransactionModel(
                bill_model=bill,
                item_model=self.expense_item,
                quantity=1.0,
                unit_cost=float(amt),
                total_amount=amt,
            )
            itm.full_clean()
            itm.save()

        bill.update_amount_due()
        bill.bill_status = BillModel.BILL_STATUS_APPROVED
        bill.date_approved = dt
        bill.clean()
        bill.save()
        return bill


class TestStage1AccountingExecutionScenarios(Stage1AccountingExecutionTestBase):

    def test_scenario_a_successful_invoice_payment_execution(self):
        """
        Scenario A:
        - Invoice $500.00
        - Bank inflow $500.00
        - Plan two-stage -> capability minted
        - ApplyPaymentCommand applied
        - Status is APPLIED_REQUIRES_REHYDRATION
        - Live state closed
        - Cash transaction posted with target cash account
        - Rehydration proves:
          - Invoice residual cleared
          - Cash leg is POSTED_BOOK_ITEM
          - ExecutedPaymentApplication present
          - Bank item excluded from Stage 1
          - Exact Stage 2 candidate generated matching bank item to posted cash leg
        """
        inv = self._create_approved_invoice(amount=Decimal("500.00"), dt=date(2026, 4, 1))
        stx = self._create_staged_movement(amount=Decimal("500.00"), dt=date(2026, 4, 5))

        state = self.hydrator.hydrate(company_id=str(self.entity.uuid), session_id="session-a")
        queries = BookkeepingQueries(state)

        from bookkeeping_state.payment_application.coordinator import plan_two_stage_bookkeeping
        two_stage_plan = plan_two_stage_bookkeeping(queries)

        self.assertEqual(len(two_stage_plan.payment_application_plan.proposals), 1)
        proposal = two_stage_plan.payment_application_plan.proposals[0]
        bank_item_id = f"staged:{stx.uuid}"
        self.assertEqual(proposal.bank_item_id, bank_item_id)

        capability = two_stage_plan.get_capability(bank_item_id)
        self.assertIsNotNone(capability)
        assert capability is not None

        cmd = ApplyPaymentCommand.from_intent(
            command_id=f"cmd-{uuid.uuid4().hex[:6]}",
            expected_state_revision=state.revision,
            session_id=state.session_id,
            issued_at=datetime(2026, 4, 5, 12, 0, tzinfo=timezone.utc),
            payment_application_id=f"payapp-{uuid.uuid4().hex[:6]}",
            plan_intent=proposal.intent,
            capability=capability,
        )

        res = self.engine.apply(state=state, command=cmd)
        self.assertEqual(res.status, TransitionStatus.APPLIED_REQUIRES_REHYDRATION)
        self.assertTrue(res.applied_requires_rehydration)
        self.assertTrue(state.is_closed)

        # Verify durable rows
        durable_app = BookkeepingPaymentApplication.objects.get(id=cmd.payment_application_id)
        self.assertEqual(durable_app.total_amount_units, 500_0000)
        self.assertEqual(durable_app.staged_transaction, stx)
        self.assertEqual(durable_app.direction, "BANK_INFLOW")

        alloc = BookkeepingPaymentApplicationAllocation.objects.get(payment_application=durable_app)
        self.assertEqual(alloc.invoice, inv)
        self.assertEqual(alloc.amount_units, 500_0000)
        self.assertIsNotNone(alloc.cash_transaction)

        cash_tx = alloc.cash_transaction
        self.assertEqual(cash_tx.account_id, self.cash_account_target.pk)
        self.assertEqual(cash_tx.amount, Decimal("500.00"))
        self.assertTrue(cash_tx.journal_entry.posted)

        # Rehydrate and verify Stage 1 and Stage 2
        fresh_state = self.hydrator.hydrate(company_id=str(self.entity.uuid), session_id="session-a2")
        fresh_queries = BookkeepingQueries(fresh_state)

        # Invoice should no longer be an open staging book item
        inv_book_id = f"invoice:{inv.uuid}"
        self.assertNotIn(inv_book_id, fresh_state.book_items)

        # Cash transaction should be a POSTED_BOOK_ITEM
        cash_tx_book_id = f"tx:{cash_tx.uuid}"
        self.assertIn(cash_tx_book_id, fresh_state.book_items)
        posted_book_item = fresh_state.book_items[cash_tx_book_id]
        self.assertEqual(posted_book_item.source_type, SourceType.POSTED_BOOK_ITEM)
        self.assertEqual(posted_book_item.direction, Direction.BOOK_BANK_DEBIT)

        # Executed payment application should be hydrated
        self.assertEqual(len(fresh_state.executed_payment_applications), 1)

        # Second two-stage planning:
        fresh_plan = plan_two_stage_bookkeeping(fresh_queries)
        # Stage 1 produces 0 proposals because bank item is already executed / claimed
        self.assertEqual(len(fresh_plan.payment_application_plan.proposals), 0)
        # Stage 2 generates exact hypothesis linking bank item to posted cash leg!
        self.assertIn(bank_item_id, fresh_plan.posted_authority_bank_item_ids)
        self.assertEqual(len(fresh_plan.reconciliation_plan.hypotheses), 1)
        hyp = fresh_plan.reconciliation_plan.hypotheses[0]
        self.assertIn(bank_item_id, [ba.bank_item_id for ba in hyp.bank_allocations])
        self.assertIn(cash_tx_book_id, [ba.book_item_id for ba in hyp.book_allocations])

    def test_scenario_b_successful_bill_payment_execution(self):
        """
        Scenario B:
        - Bill $300.00
        - Bank outflow $300.00 (staged tx amount = -300.00)
        - Plan two-stage -> capability minted
        - ApplyPaymentCommand applied
        - Status is APPLIED_REQUIRES_REHYDRATION
        - Live state closed
        - Cash transaction posted with credit on target cash account
        - Rehydration proves:
          - Bill residual cleared
          - Cash leg is POSTED_BOOK_ITEM with BOOK_BANK_CREDIT
          - ExecutedPaymentApplication present with BANK_OUTFLOW
          - Bank item excluded from Stage 1
          - Stage 2 hypothesis generated linking bank item to posted cash leg
        """
        bill = self._create_approved_bill(amount=Decimal("300.00"), dt=date(2026, 4, 1))
        stx = self._create_staged_movement(amount=Decimal("-300.00"), dt=date(2026, 4, 5))

        state = self.hydrator.hydrate(company_id=str(self.entity.uuid), session_id="session-b")
        queries = BookkeepingQueries(state)

        two_stage_plan = plan_two_stage_bookkeeping(queries)
        self.assertEqual(len(two_stage_plan.payment_application_plan.proposals), 1)
        proposal = two_stage_plan.payment_application_plan.proposals[0]
        bank_item_id = f"staged:{stx.uuid}"
        self.assertEqual(proposal.bank_item_id, bank_item_id)

        capability = two_stage_plan.get_capability(bank_item_id)
        self.assertIsNotNone(capability)
        assert capability is not None

        cmd = ApplyPaymentCommand.from_intent(
            command_id=f"cmd-{uuid.uuid4().hex[:6]}",
            expected_state_revision=state.revision,
            session_id=state.session_id,
            issued_at=datetime(2026, 4, 5, 12, 0, tzinfo=timezone.utc),
            payment_application_id=f"payapp-{uuid.uuid4().hex[:6]}",
            plan_intent=proposal.intent,
            capability=capability,
        )

        res = self.engine.apply(state=state, command=cmd)
        self.assertEqual(res.status, TransitionStatus.APPLIED_REQUIRES_REHYDRATION)
        self.assertTrue(res.applied_requires_rehydration)
        self.assertTrue(state.is_closed)

        # Verify durable rows
        durable_app = BookkeepingPaymentApplication.objects.get(id=cmd.payment_application_id)
        self.assertEqual(durable_app.total_amount_units, 300_0000)
        self.assertEqual(durable_app.staged_transaction, stx)
        self.assertEqual(durable_app.direction, "BANK_OUTFLOW")

        alloc = BookkeepingPaymentApplicationAllocation.objects.get(payment_application=durable_app)
        self.assertEqual(alloc.bill, bill)
        self.assertEqual(alloc.amount_units, 300_0000)
        self.assertIsNotNone(alloc.cash_transaction)

        cash_tx = alloc.cash_transaction
        self.assertEqual(cash_tx.account_id, self.cash_account_target.pk)
        self.assertEqual(cash_tx.amount, Decimal("300.00"))
        self.assertTrue(cash_tx.journal_entry.posted)

        # Rehydrate and verify Stage 1 and Stage 2
        fresh_state = self.hydrator.hydrate(company_id=str(self.entity.uuid), session_id="session-b2")
        fresh_queries = BookkeepingQueries(fresh_state)

        # Bill should no longer be an open staging book item
        bill_book_id = f"bill:{bill.uuid}"
        self.assertNotIn(bill_book_id, fresh_state.book_items)

        # Cash transaction should be a POSTED_BOOK_ITEM with BOOK_BANK_CREDIT
        cash_tx_book_id = f"tx:{cash_tx.uuid}"
        self.assertIn(cash_tx_book_id, fresh_state.book_items)
        posted_book_item = fresh_state.book_items[cash_tx_book_id]
        self.assertEqual(posted_book_item.source_type, SourceType.POSTED_BOOK_ITEM)
        self.assertEqual(posted_book_item.direction, Direction.BOOK_BANK_CREDIT)

        # Executed payment application should be hydrated
        self.assertEqual(len(fresh_state.executed_payment_applications), 1)

        # Second two-stage planning:
        fresh_plan = plan_two_stage_bookkeeping(fresh_queries)
        self.assertEqual(len(fresh_plan.payment_application_plan.proposals), 0)
        self.assertIn(bank_item_id, fresh_plan.posted_authority_bank_item_ids)
        self.assertEqual(len(fresh_plan.reconciliation_plan.hypotheses), 1)
        hyp = fresh_plan.reconciliation_plan.hypotheses[0]
        self.assertIn(bank_item_id, [ba.bank_item_id for ba in hyp.bank_allocations])
        self.assertIn(cash_tx_book_id, [ba.book_item_id for ba in hyp.book_allocations])

    def test_scenario_c_exact_cash_gl_account_override_used(self):
        """
        Scenario C:
        - Invoice configured with default cash account (1010)
        - Bank account configured with target cash account (1015)
        - When executed, payment posts strictly to 1015
        - Invoice in DB is NOT persistently modified
        """
        inv = self._create_approved_invoice(amount=Decimal("250.00"), dt=date(2026, 4, 1))
        self.assertEqual(inv.cash_account, self.cash_account_default)

        stx = self._create_staged_movement(amount=Decimal("250.00"), dt=date(2026, 4, 5))
        state = self.hydrator.hydrate(company_id=str(self.entity.uuid), session_id="session-c")
        plan = plan_two_stage_bookkeeping(BookkeepingQueries(state))
        proposal = plan.payment_application_plan.proposals[0]
        cap = plan.get_capability(f"staged:{stx.uuid}")
        self.assertIsNotNone(cap)
        assert cap is not None

        cmd = ApplyPaymentCommand.from_intent(
            command_id=f"cmd-{uuid.uuid4().hex[:6]}",
            expected_state_revision=state.revision,
            session_id=state.session_id,
            issued_at=datetime(2026, 4, 5, 12, 0, tzinfo=timezone.utc),
            payment_application_id=f"payapp-{uuid.uuid4().hex[:6]}",
            plan_intent=proposal.intent,
            capability=cap,
        )

        res = self.engine.apply(state=state, command=cmd)
        self.assertEqual(res.status, TransitionStatus.APPLIED_REQUIRES_REHYDRATION)

        alloc = BookkeepingPaymentApplicationAllocation.objects.get(
            payment_application_id=cmd.payment_application_id
        )
        assert alloc.cash_transaction is not None
        self.assertEqual(alloc.cash_transaction.account_id, self.cash_account_target.pk)

        # Confirm non-destructive override on InvoiceModel
        inv.refresh_from_db()
        self.assertEqual(inv.cash_account, self.cash_account_default)

    def test_scenario_d_multi_obligation_split_payment(self):
        """
        Scenario D:
        - Single bank inflow of $500.00
        - Pays 2 invoices: $300.00 and $200.00
        - Produces 1 BookkeepingPaymentApplication with 2 allocations
        - Advances revision exactly once
        - Clears both invoices upon rehydration 
        """
        inv1 = self._create_approved_invoice(amount=Decimal("300.00"), dt=date(2026, 4, 1))
        inv2 = self._create_approved_invoice(amount=Decimal("200.00"), dt=date(2026, 4, 2))
        stx = self._create_staged_movement(amount=Decimal("500.00"), dt=date(2026, 4, 5))

        state = self.hydrator.hydrate(company_id=str(self.entity.uuid), session_id="session-d")
        bank_item_id = f"staged:{stx.uuid}"
        inv1_item_id = f"invoice:{inv1.uuid}"
        inv2_item_id = f"invoice:{inv2.uuid}"

        allocations = (
            PaymentApplicationObligationAllocation(
                book_item_id=inv1_item_id,
                amount_units=300_0000,
            ),
            PaymentApplicationObligationAllocation(
                book_item_id=inv2_item_id,
                amount_units=200_0000,
            ),
        )

        cap = self._mint_valid_capability(
            state=state,
            bank_item_id=bank_item_id,
            bank_account_id=str(self.bank_account.uuid),
            direction=Direction.BANK_INFLOW,
            currency="USD",
            total_amount_units=500_0000,
            payment_date=stx.date_posted,
            allocations=allocations,
        )

        cmd = ApplyPaymentCommand(
            command_id=f"cmd-{uuid.uuid4().hex[:6]}",
            expected_state_revision=state.revision,
            session_id=state.session_id,
            issued_at=datetime(2026, 4, 5, 12, 0, tzinfo=timezone.utc),
            payment_application_id=f"payapp-{uuid.uuid4().hex[:6]}",
            bank_item_id=bank_item_id,
            bank_account_id=str(self.bank_account.uuid),
            direction=Direction.BANK_INFLOW,
            payment_date=stx.date_posted,
            total_amount_units=500_0000,
            currency="USD",
            allocations=allocations,
            capability=cap,
        )

        res = self.engine.apply(state=state, command=cmd)
        self.assertEqual(res.status, TransitionStatus.APPLIED_REQUIRES_REHYDRATION)

        durable_app: Any = BookkeepingPaymentApplication.objects.get(id=cmd.payment_application_id)
        self.assertEqual(durable_app.total_amount_units, 500_0000)
        allocs = list(durable_app.allocations.order_by("amount_units"))
        self.assertEqual(len(allocs), 2)
        self.assertEqual(allocs[0].invoice, inv2)
        self.assertEqual(allocs[0].amount_units, 200_0000)
        self.assertEqual(allocs[1].invoice, inv1)
        self.assertEqual(allocs[1].amount_units, 300_0000)

        # Rehydrate
        fresh_state = self.hydrator.hydrate(company_id=str(self.entity.uuid), session_id="session-d2")
        self.assertNotIn(inv1_item_id, fresh_state.book_items)
        self.assertNotIn(inv2_item_id, fresh_state.book_items)
        self.assertEqual(len(fresh_state.executed_payment_applications), 1)
        hydrated_app = list(fresh_state.executed_payment_applications.values())[0]
        self.assertEqual(len(hydrated_app.allocations), 2)

    def test_scenario_e_capability_validation_failure(self):
        """
        Scenario E:
        - Tampered token or mismatched state fingerprint
        - Rejected with INVALID_STAGE1_CAPABILITY
        - State remains open and unchanged
        """
        inv = self._create_approved_invoice(amount=Decimal("100.00"))
        stx = self._create_staged_movement(amount=Decimal("100.00"))

        state = self.hydrator.hydrate(company_id=str(self.entity.uuid), session_id="session-e")
        bank_item_id = f"staged:{stx.uuid}"

        forged_cap = Stage1ExecutionCapability(
            token="forged_token_0000000000000000",
            intent_digest="forged_digest_0000000000000000",
            bank_item_id=bank_item_id,
            session_id=state.session_id,
            base_state_revision=state.revision,
            base_state_fingerprint="forged_fingerprint",
        )

        cmd = ApplyPaymentCommand(
            command_id=f"cmd-{uuid.uuid4().hex[:6]}",
            expected_state_revision=state.revision,
            session_id=state.session_id,
            issued_at=datetime(2026, 4, 5, 12, 0, tzinfo=timezone.utc),
            payment_application_id=f"payapp-{uuid.uuid4().hex[:6]}",
            bank_item_id=bank_item_id,
            bank_account_id=str(self.bank_account.uuid),
            direction=Direction.BANK_INFLOW,
            payment_date=stx.date_posted,
            total_amount_units=100_0000,
            currency="USD",
            allocations=(
                PaymentApplicationObligationAllocation(
                    book_item_id=f"invoice:{inv.uuid}",
                    amount_units=100_0000,
                ),
            ),
            capability=forged_cap,
        )

        res = self.engine.apply(state=state, command=cmd)
        self.assertEqual(res.status, TransitionStatus.REJECTED)
        self.assertIsNotNone(res.rejection)
        assert res.rejection is not None
        self.assertEqual(res.rejection.code, RejectionCode.INVALID_STAGE1_CAPABILITY)
        self.assertFalse(state.is_closed)

    def test_scenario_f_active_stage2_bank_allocation_capacity_conflict(self):
        """
        Scenario F:
        - Bank item already allocated in active Stage-2 reconciliation
        - Apply payment rejected with ACTIVE_STAGE2_ALLOCATION_EXISTS
        """
        inv = self._create_approved_invoice(amount=Decimal("100.00"))
        stx = self._create_staged_movement(amount=Decimal("100.00"))

        rec = BookkeepingReconciliation.objects.create(
            id=f"rec-{uuid.uuid4().hex[:8]}",
            entity=self.entity,
            state_revision_at_creation=0,
            created_at=datetime.now(timezone.utc),
        )
        BookkeepingReconciliationBankAllocation.objects.create(
            reconciliation=rec,
            staged_transaction=stx,
            amount_units=100_0000,
        )
        tx = self._create_cash_tx(amount=Decimal("100.00"), is_debit=True)
        BookkeepingReconciliationBookAllocation.objects.create(
            reconciliation=rec,
            transaction=tx,
            amount_units=100_0000,
        )

        state = self.hydrator.hydrate(company_id=str(self.entity.uuid), session_id="session-f")
        bank_item_id = f"staged:{stx.uuid}"

        allocations = (
            PaymentApplicationObligationAllocation(
                book_item_id=f"invoice:{inv.uuid}",
                amount_units=100_0000,
            ),
        )

        cap = self._mint_valid_capability(
            state=state,
            bank_item_id=bank_item_id,
            bank_account_id=str(self.bank_account.uuid),
            direction=Direction.BANK_INFLOW,
            currency="USD",
            total_amount_units=100_0000,
            payment_date=stx.date_posted,
            allocations=allocations,
        )

        cmd = ApplyPaymentCommand(
            command_id=f"cmd-{uuid.uuid4().hex[:6]}",
            expected_state_revision=state.revision,
            session_id=state.session_id,
            issued_at=datetime(2026, 4, 5, 12, 0, tzinfo=timezone.utc),
            payment_application_id=f"payapp-{uuid.uuid4().hex[:6]}",
            bank_item_id=bank_item_id,
            bank_account_id=str(self.bank_account.uuid),
            direction=Direction.BANK_INFLOW,
            payment_date=stx.date_posted,
            total_amount_units=100_0000,
            currency="USD",
            allocations=allocations,
            capability=cap,
        )

        res = self.engine.apply(state=state, command=cmd)
        self.assertEqual(res.status, TransitionStatus.REJECTED)
        self.assertIsNotNone(res.rejection)
        assert res.rejection is not None
        self.assertEqual(res.rejection.code, RejectionCode.ACTIVE_STAGE2_ALLOCATION_EXISTS)

    def test_scenario_g_obligation_capacity_exceeded(self):
        """
        Scenario G:
        - Invoice has capacity 100.00
        - Bank movement is 150.00
        - Allocation is 150.00 (exceeds invoice capacity)
        - Rejected with CAPACITY_EXCEEDED
        """
        inv = self._create_approved_invoice(amount=Decimal("100.00"))
        stx = self._create_staged_movement(amount=Decimal("150.00"))

        state = self.hydrator.hydrate(company_id=str(self.entity.uuid), session_id="session-g")
        bank_item_id = f"staged:{stx.uuid}"

        allocations = (
            PaymentApplicationObligationAllocation(
                book_item_id=f"invoice:{inv.uuid}",
                amount_units=150_0000,
            ),
        )

        cap = self._mint_valid_capability(
            state=state,
            bank_item_id=bank_item_id,
            bank_account_id=str(self.bank_account.uuid),
            direction=Direction.BANK_INFLOW,
            currency="USD",
            total_amount_units=150_0000,
            payment_date=stx.date_posted,
            allocations=allocations,
        )

        cmd = ApplyPaymentCommand(
            command_id=f"cmd-{uuid.uuid4().hex[:6]}",
            expected_state_revision=state.revision,
            session_id=state.session_id,
            issued_at=datetime(2026, 4, 5, 12, 0, tzinfo=timezone.utc),
            payment_application_id=f"payapp-{uuid.uuid4().hex[:6]}",
            bank_item_id=bank_item_id,
            bank_account_id=str(self.bank_account.uuid),
            direction=Direction.BANK_INFLOW,
            payment_date=stx.date_posted,
            total_amount_units=150_0000,
            currency="USD",
            allocations=allocations,
            capability=cap,
        )

        res = self.engine.apply(state=state, command=cmd)
        self.assertEqual(res.status, TransitionStatus.REJECTED)
        self.assertIsNotNone(res.rejection)
        assert res.rejection is not None
        self.assertEqual(res.rejection.code, RejectionCode.CAPACITY_EXCEEDED)

    def test_scenario_h_currency_mismatch(self):
        """
        Scenario H:
        - Command specifies mismatched currency (e.g. EUR vs USD)
        - Rejected with CURRENCY_MISMATCH
        """
        inv = self._create_approved_invoice(amount=Decimal("100.00"))
        stx = self._create_staged_movement(amount=Decimal("100.00"))

        state = self.hydrator.hydrate(company_id=str(self.entity.uuid), session_id="session-h")
        bank_item_id = f"staged:{stx.uuid}"

        allocations = (
            PaymentApplicationObligationAllocation(
                book_item_id=f"invoice:{inv.uuid}",
                amount_units=100_0000,
            ),
        )

        cap = self._mint_valid_capability(
            state=state,
            bank_item_id=bank_item_id,
            bank_account_id=str(self.bank_account.uuid),
            direction=Direction.BANK_INFLOW,
            currency="EUR",
            total_amount_units=100_0000,
            payment_date=stx.date_posted,
            allocations=allocations,
        )

        cmd = ApplyPaymentCommand(
            command_id=f"cmd-{uuid.uuid4().hex[:6]}",
            expected_state_revision=state.revision,
            session_id=state.session_id,
            issued_at=datetime(2026, 4, 5, 12, 0, tzinfo=timezone.utc),
            payment_application_id=f"payapp-{uuid.uuid4().hex[:6]}",
            bank_item_id=bank_item_id,
            bank_account_id=str(self.bank_account.uuid),
            direction=Direction.BANK_INFLOW,
            payment_date=stx.date_posted,
            total_amount_units=100_0000,
            currency="EUR",
            allocations=allocations,
            capability=cap,
        )

        res = self.engine.apply(state=state, command=cmd)
        self.assertEqual(res.status, TransitionStatus.REJECTED)
        self.assertIsNotNone(res.rejection)
        assert res.rejection is not None
        self.assertEqual(res.rejection.code, RejectionCode.CURRENCY_MISMATCH)

    def test_scenario_i_direction_mismatch(self):
        """
        Scenario I:
        - Attempting to apply bank outflow to an Invoice (receivable)
        - Rejected with DIRECTION_MISMATCH
        """
        inv = self._create_approved_invoice(amount=Decimal("100.00"))
        stx = self._create_staged_movement(amount=Decimal("-100.00"))

        state = self.hydrator.hydrate(company_id=str(self.entity.uuid), session_id="session-i")
        bank_item_id = f"staged:{stx.uuid}"

        allocations = (
            PaymentApplicationObligationAllocation(
                book_item_id=f"invoice:{inv.uuid}",
                amount_units=100_0000,
            ),
        )

        cap = self._mint_valid_capability(
            state=state,
            bank_item_id=bank_item_id,
            bank_account_id=str(self.bank_account.uuid),
            direction=Direction.BANK_OUTFLOW,
            currency="USD",
            total_amount_units=100_0000,
            payment_date=stx.date_posted,
            allocations=allocations,
        )

        cmd = ApplyPaymentCommand(
            command_id=f"cmd-{uuid.uuid4().hex[:6]}",
            expected_state_revision=state.revision,
            session_id=state.session_id,
            issued_at=datetime(2026, 4, 5, 12, 0, tzinfo=timezone.utc),
            payment_application_id=f"payapp-{uuid.uuid4().hex[:6]}",
            bank_item_id=bank_item_id,
            bank_account_id=str(self.bank_account.uuid),
            direction=Direction.BANK_OUTFLOW,
            payment_date=stx.date_posted,
            total_amount_units=100_0000,
            currency="USD",
            allocations=allocations,
            capability=cap,
        )

        res = self.engine.apply(state=state, command=cmd)
        self.assertEqual(res.status, TransitionStatus.REJECTED)
        self.assertIsNotNone(res.rejection)
        assert res.rejection is not None
        self.assertEqual(res.rejection.code, RejectionCode.DIRECTION_MISMATCH)

    def test_scenario_j_obligation_type_invalid(self):
        """
        Scenario J:
        - Obligation targeting an unknown book item or non-staging book item
        - Rejected with UNKNOWN_BOOK_ITEM
        """
        stx = self._create_staged_movement(amount=Decimal("100.00"))
        state = self.hydrator.hydrate(company_id=str(self.entity.uuid), session_id="session-j")
        bank_item_id = f"staged:{stx.uuid}"

        allocations = (
            PaymentApplicationObligationAllocation(
                book_item_id="invoice:non-existent-uuid",
                amount_units=100_0000,
            ),
        )

        cap = self._mint_valid_capability(
            state=state,
            bank_item_id=bank_item_id,
            bank_account_id=str(self.bank_account.uuid),
            direction=Direction.BANK_INFLOW,
            currency="USD",
            total_amount_units=100_0000,
            payment_date=stx.date_posted,
            allocations=allocations,
        )

        cmd = ApplyPaymentCommand(
            command_id=f"cmd-{uuid.uuid4().hex[:6]}",
            expected_state_revision=state.revision,
            session_id=state.session_id,
            issued_at=datetime(2026, 4, 5, 12, 0, tzinfo=timezone.utc),
            payment_application_id=f"payapp-{uuid.uuid4().hex[:6]}",
            bank_item_id=bank_item_id,
            bank_account_id=str(self.bank_account.uuid),
            direction=Direction.BANK_INFLOW,
            payment_date=stx.date_posted,
            total_amount_units=100_0000,
            currency="USD",
            allocations=allocations,
            capability=cap,
        )

        res = self.engine.apply(state=state, command=cmd)
        self.assertEqual(res.status, TransitionStatus.REJECTED)
        self.assertIsNotNone(res.rejection)
        assert res.rejection is not None
        self.assertEqual(res.rejection.code, RejectionCode.UNKNOWN_BOOK_ITEM)

    def test_scenario_k_in_memory_state_revision_conflict(self):
        """
        Scenario K:
        - Command prepared against wrong state revision
        - Rejected with STATE_REVISION_CONFLICT
        """
        inv = self._create_approved_invoice(amount=Decimal("100.00"))
        stx = self._create_staged_movement(amount=Decimal("100.00"))
        state = self.hydrator.hydrate(company_id=str(self.entity.uuid), session_id="session-k")
        bank_item_id = f"staged:{stx.uuid}"

        allocations = (
            PaymentApplicationObligationAllocation(
                book_item_id=f"invoice:{inv.uuid}",
                amount_units=100_0000,
            ),
        )

        cap = self._mint_valid_capability(
            state=state,
            bank_item_id=bank_item_id,
            bank_account_id=str(self.bank_account.uuid),
            direction=Direction.BANK_INFLOW,
            currency="USD",
            total_amount_units=100_0000,
            payment_date=stx.date_posted,
            allocations=allocations,
        )

        cmd = ApplyPaymentCommand(
            command_id=f"cmd-{uuid.uuid4().hex[:6]}",
            expected_state_revision=state.revision + 5,
            session_id=state.session_id,
            issued_at=datetime(2026, 4, 5, 12, 0, tzinfo=timezone.utc),
            payment_application_id=f"payapp-{uuid.uuid4().hex[:6]}",
            bank_item_id=bank_item_id,
            bank_account_id=str(self.bank_account.uuid),
            direction=Direction.BANK_INFLOW,
            payment_date=stx.date_posted,
            total_amount_units=100_0000,
            currency="USD",
            allocations=allocations,
            capability=cap,
        )

        res = self.engine.apply(state=state, command=cmd)
        self.assertEqual(res.status, TransitionStatus.REJECTED)
        self.assertIsNotNone(res.rejection)
        assert res.rejection is not None
        self.assertEqual(res.rejection.code, RejectionCode.STATE_REVISION_CONFLICT)

    def test_scenario_l_durable_occ_revision_mismatch(self):
        """
        Scenario L:
        - Durable revision is incremented concurrently in the database
        - ApplyPaymentCommand execution fails OCC
        - Rejected with PERSISTENCE_REVISION_CONFLICT
        """
        inv = self._create_approved_invoice(amount=Decimal("100.00"))
        stx = self._create_staged_movement(amount=Decimal("100.00"))
        state = self.hydrator.hydrate(company_id=str(self.entity.uuid), session_id="session-l")
        bank_item_id = f"staged:{stx.uuid}"

        allocations = (
            PaymentApplicationObligationAllocation(
                book_item_id=f"invoice:{inv.uuid}",
                amount_units=100_0000,
            ),
        )

        cap = self._mint_valid_capability(
            state=state,
            bank_item_id=bank_item_id,
            bank_account_id=str(self.bank_account.uuid),
            direction=Direction.BANK_INFLOW,
            currency="USD",
            total_amount_units=100_0000,
            payment_date=stx.date_posted,
            allocations=allocations,
        )

        cmd = ApplyPaymentCommand(
            command_id=f"cmd-{uuid.uuid4().hex[:6]}",
            expected_state_revision=state.revision,
            session_id=state.session_id,
            issued_at=datetime(2026, 4, 5, 12, 0, tzinfo=timezone.utc),
            payment_application_id=f"payapp-{uuid.uuid4().hex[:6]}",
            bank_item_id=bank_item_id,
            bank_account_id=str(self.bank_account.uuid),
            direction=Direction.BANK_INFLOW,
            payment_date=stx.date_posted,
            total_amount_units=100_0000,
            currency="USD",
            allocations=allocations,
            capability=cap,
        )

        BookkeepingRevision.objects.update_or_create(
            entity=self.entity,
            defaults={"revision": 99},
        )

        res = self.engine.apply(state=state, command=cmd)
        self.assertEqual(res.status, TransitionStatus.REJECTED)
        self.assertIsNotNone(res.rejection)
        assert res.rejection is not None
        self.assertEqual(res.rejection.code, RejectionCode.PERSISTENCE_REVISION_CONFLICT)

    def test_scenario_m_duplicate_bank_item_execution_rejection(self):
        """
        Scenario M:
        - Executing payment on a bank item that was already executed
        - Rejected with DUPLICATE_ARTIFACT
        """
        inv1 = self._create_approved_invoice(amount=Decimal("100.00"))
        inv2 = self._create_approved_invoice(amount=Decimal("100.00"))
        stx = self._create_staged_movement(amount=Decimal("100.00"))

        state = self.hydrator.hydrate(company_id=str(self.entity.uuid), session_id="session-m1")
        plan = plan_two_stage_bookkeeping(BookkeepingQueries(state))
        proposal = plan.payment_application_plan.proposals[0]
        cap = plan.get_capability(f"staged:{stx.uuid}")
        self.assertIsNotNone(cap)
        assert cap is not None

        cmd1 = ApplyPaymentCommand.from_intent(
            command_id=f"cmd-{uuid.uuid4().hex[:6]}",
            expected_state_revision=state.revision,
            session_id=state.session_id,
            issued_at=datetime(2026, 4, 5, 12, 0, tzinfo=timezone.utc),
            payment_application_id=f"payapp-{uuid.uuid4().hex[:6]}",
            plan_intent=proposal.intent,
            capability=cap,
        )
        res1 = self.engine.apply(state=state, command=cmd1)
        self.assertEqual(res1.status, TransitionStatus.APPLIED_REQUIRES_REHYDRATION)

        state2 = self.hydrator.hydrate(company_id=str(self.entity.uuid), session_id="session-m2")
        bank_item_id = f"staged:{stx.uuid}"

        allocations2 = (
            PaymentApplicationObligationAllocation(
                book_item_id=f"invoice:{inv2.uuid}",
                amount_units=100_0000,
            ),
        )

        cap2 = self._mint_valid_capability(
            state=state2,
            bank_item_id=bank_item_id,
            bank_account_id=str(self.bank_account.uuid),
            direction=Direction.BANK_INFLOW,
            currency="USD",
            total_amount_units=100_0000,
            payment_date=stx.date_posted,
            allocations=allocations2,
        )
        cmd2 = ApplyPaymentCommand(
            command_id=f"cmd-{uuid.uuid4().hex[:6]}",
            expected_state_revision=state2.revision,
            session_id=state2.session_id,
            issued_at=datetime(2026, 4, 5, 12, 0, tzinfo=timezone.utc),
            payment_application_id=f"payapp-{uuid.uuid4().hex[:6]}",
            bank_item_id=bank_item_id,
            bank_account_id=str(self.bank_account.uuid),
            direction=Direction.BANK_INFLOW,
            payment_date=stx.date_posted,
            total_amount_units=100_0000,
            currency="USD",
            allocations=allocations2,
            capability=cap2,
        )
        res2 = self.engine.apply(state=state2, command=cmd2)
        self.assertEqual(res2.status, TransitionStatus.REJECTED)
        self.assertIsNotNone(res2.rejection)
        assert res2.rejection is not None
        self.assertEqual(res2.rejection.code, RejectionCode.DUPLICATE_ARTIFACT)

    def test_scenario_n_duplicate_payment_application_id_rejection(self):
        """
        Scenario N:
        - Attempting to reuse an existing payment_application_id
        - Rejected with DUPLICATE_ARTIFACT
        """
        inv1 = self._create_approved_invoice(amount=Decimal("100.00"))
        inv2 = self._create_approved_invoice(amount=Decimal("100.00"))
        stx1 = self._create_staged_movement(amount=Decimal("100.00"))
        stx2 = self._create_staged_movement(amount=Decimal("100.00"))

        fixed_app_id = f"payapp-dup-{uuid.uuid4().hex[:6]}"

        state = self.hydrator.hydrate(company_id=str(self.entity.uuid), session_id="session-n1")
        plan = plan_two_stage_bookkeeping(BookkeepingQueries(state))
        prop1 = next(
            p for p in plan.payment_application_plan.proposals
            if p.bank_item_id == f"staged:{stx1.uuid}"
        )
        cap1 = plan.get_capability(prop1.bank_item_id)
        self.assertIsNotNone(cap1)
        assert cap1 is not None

        cmd1 = ApplyPaymentCommand.from_intent(
            command_id=f"cmd-{uuid.uuid4().hex[:6]}",
            expected_state_revision=state.revision,
            session_id=state.session_id,
            issued_at=datetime(2026, 4, 5, 12, 0, tzinfo=timezone.utc),
            payment_application_id=fixed_app_id,
            plan_intent=prop1.intent,
            capability=cap1,
        )
        res1 = self.engine.apply(state=state, command=cmd1)
        self.assertEqual(res1.status, TransitionStatus.APPLIED_REQUIRES_REHYDRATION)

        state2 = self.hydrator.hydrate(company_id=str(self.entity.uuid), session_id="session-n2")

        allocations2 = (
            PaymentApplicationObligationAllocation(
                book_item_id=f"invoice:{inv2.uuid}",
                amount_units=100_0000,
            ),
        )

        cap2 = self._mint_valid_capability(
            state=state2,
            bank_item_id=f"staged:{stx2.uuid}",
            bank_account_id=str(self.bank_account.uuid),
            direction=Direction.BANK_INFLOW,
            currency="USD",
            total_amount_units=100_0000,
            payment_date=stx2.date_posted,
            allocations=allocations2,
        )

        cmd2 = ApplyPaymentCommand(
            command_id=f"cmd-{uuid.uuid4().hex[:6]}",
            expected_state_revision=state2.revision,
            session_id=state2.session_id,
            issued_at=datetime(2026, 4, 5, 12, 0, tzinfo=timezone.utc),
            payment_application_id=fixed_app_id,
            bank_item_id=f"staged:{stx2.uuid}",
            bank_account_id=str(self.bank_account.uuid),
            direction=Direction.BANK_INFLOW,
            payment_date=stx2.date_posted,
            total_amount_units=100_0000,
            currency="USD",
            allocations=allocations2,
            capability=cap2,
        )
        res2 = self.engine.apply(state=state2, command=cmd2)
        self.assertEqual(res2.status, TransitionStatus.REJECTED)
        self.assertIsNotNone(res2.rejection)
        assert res2.rejection is not None
        self.assertEqual(res2.rejection.code, RejectionCode.DUPLICATE_ARTIFACT)

    def test_scenario_o_transaction_rollback_on_kernel_failure(self):
        """
        Scenario O:
        - When accounting kernel fails during make_payment_with_result
        - Entire transaction rolls back atomically
        - No BookkeepingPaymentApplication rows created
        - No cash TransactionModel rows created
        - Revision unchanged
        """
        inv = self._create_approved_invoice(amount=Decimal("100.00"))
        stx = self._create_staged_movement(amount=Decimal("100.00"))

        state = self.hydrator.hydrate(company_id=str(self.entity.uuid), session_id="session-o")
        bank_item_id = f"staged:{stx.uuid}"

        allocations = (
            PaymentApplicationObligationAllocation(
                book_item_id=f"invoice:{inv.uuid}",
                amount_units=100_0000,
            ),
        )

        cap = self._mint_valid_capability(
            state=state,
            bank_item_id=bank_item_id,
            bank_account_id=str(self.bank_account.uuid),
            direction=Direction.BANK_INFLOW,
            currency="USD",
            total_amount_units=100_0000,
            payment_date=stx.date_posted,
            allocations=allocations,
        )

        cmd = ApplyPaymentCommand(
            command_id=f"cmd-{uuid.uuid4().hex[:6]}",
            expected_state_revision=state.revision,
            session_id=state.session_id,
            issued_at=datetime(2026, 4, 5, 12, 0, tzinfo=timezone.utc),
            payment_application_id=f"payapp-{uuid.uuid4().hex[:6]}",
            bank_item_id=bank_item_id,
            bank_account_id=str(self.bank_account.uuid),
            direction=Direction.BANK_INFLOW,
            payment_date=stx.date_posted,
            total_amount_units=100_0000,
            currency="USD",
            allocations=allocations,
            capability=cap,
        )

        initial_app_count = BookkeepingPaymentApplication.objects.count()
        initial_cash_tx_count = TransactionModel.objects.filter(account=self.cash_account_target).count()
        initial_rev_obj = BookkeepingRevision.objects.filter(entity=self.entity).first()
        initial_rev = initial_rev_obj.revision if initial_rev_obj else 0

        with patch.object(InvoiceModel, "make_payment_with_result", side_effect=RuntimeError("Kernel explosion")):
            res = self.engine.apply(state=state, command=cmd)

        self.assertEqual(res.status, TransitionStatus.REJECTED)
        self.assertEqual(BookkeepingPaymentApplication.objects.count(), initial_app_count)
        self.assertEqual(TransactionModel.objects.filter(account=self.cash_account_target).count(), initial_cash_tx_count)
        current_rev_obj = BookkeepingRevision.objects.filter(entity=self.entity).first()
        current_rev = current_rev_obj.revision if current_rev_obj else 0
        self.assertEqual(current_rev, initial_rev)

    def test_scenario_p_non_destructive_cash_account_override(self):
        """
        Scenario P:
        - Confirms both InvoiceModel and BillModel cash_account fields
        - are completely untouched in DB after make_payment_with_result
        """
        inv = self._create_approved_invoice(amount=Decimal("100.00"))
        bill = self._create_approved_bill(amount=Decimal("100.00"))

        self.assertEqual(inv.cash_account, self.cash_account_default)
        self.assertEqual(bill.cash_account, self.cash_account_default)

        res_inv = inv.make_payment_with_result(
            payment_amount=Decimal("100.00"),
            payment_date=date(2026, 4, 5),
            cash_account_override=self.cash_account_target,
            commit=True,
        )
        self.assertIsNotNone(res_inv)
        assert res_inv is not None and res_inv.cash_transaction is not None
        inv.refresh_from_db()
        self.assertEqual(inv.cash_account, self.cash_account_default)
        self.assertEqual(res_inv.cash_transaction.account_id, self.cash_account_target.pk)

        res_bill = bill.make_payment_with_result(
            payment_amount=Decimal("100.00"),
            payment_date=date(2026, 4, 5),
            cash_account_override=self.cash_account_target,
            commit=True,
        )
        self.assertIsNotNone(res_bill)
        assert res_bill is not None and res_bill.cash_transaction is not None
        bill.refresh_from_db()
        self.assertEqual(bill.cash_account, self.cash_account_default)
        self.assertEqual(res_bill.cash_transaction.account_id, self.cash_account_target.pk)

    def test_scenario_q_single_command_batch_support(self):
        """
        Scenario Q:
        - Single-command TransitionBatch containing ApplyPaymentCommand
        - Successfully processed and returns APPLIED_REQUIRES_REHYDRATION
        """
        inv = self._create_approved_invoice(amount=Decimal("100.00"))
        stx = self._create_staged_movement(amount=Decimal("100.00"))

        state = self.hydrator.hydrate(company_id=str(self.entity.uuid), session_id="session-q")
        bank_item_id = f"staged:{stx.uuid}"

        allocations = (
            PaymentApplicationObligationAllocation(
                book_item_id=f"invoice:{inv.uuid}",
                amount_units=100_0000,
            ),
        )

        cap = self._mint_valid_capability(
            state=state,
            bank_item_id=bank_item_id,
            bank_account_id=str(self.bank_account.uuid),
            direction=Direction.BANK_INFLOW,
            currency="USD",
            total_amount_units=100_0000,
            payment_date=stx.date_posted,
            allocations=allocations,
        )

        cmd = ApplyPaymentCommand(
            command_id=f"cmd-{uuid.uuid4().hex[:6]}",
            expected_state_revision=state.revision,
            session_id=state.session_id,
            issued_at=datetime(2026, 4, 5, 12, 0, tzinfo=timezone.utc),
            payment_application_id=f"payapp-{uuid.uuid4().hex[:6]}",
            bank_item_id=bank_item_id,
            bank_account_id=str(self.bank_account.uuid),
            direction=Direction.BANK_INFLOW,
            payment_date=stx.date_posted,
            total_amount_units=100_0000,
            currency="USD",
            allocations=allocations,
            capability=cap,
        )

        batch = TransitionBatch(
            batch_id=f"batch-{uuid.uuid4().hex[:6]}",
            session_id=state.session_id,
            expected_state_revision=state.revision,
            commands=(cmd,),
        )
        batch_res = self.engine.apply_batch(state=state, batch=batch)
        self.assertEqual(batch_res.status, TransitionStatus.APPLIED_REQUIRES_REHYDRATION)
        self.assertTrue(batch_res.is_applied_requires_rehydration)
        self.assertTrue(state.is_closed)

    def test_scenario_r_mixed_sibling_batch_rejection(self):
        """
        Scenario R:
        - Mixed batch containing ApplyPaymentCommand and another command
        - Rejected with UNSUPPORTED_COMMAND
        """
        inv = self._create_approved_invoice(amount=Decimal("100.00"))
        stx = self._create_staged_movement(amount=Decimal("100.00"))

        state = self.hydrator.hydrate(company_id=str(self.entity.uuid), session_id="session-r")
        bank_item_id = f"staged:{stx.uuid}"

        allocations = (
            PaymentApplicationObligationAllocation(
                book_item_id=f"invoice:{inv.uuid}",
                amount_units=100_0000,
            ),
        )

        cap = self._mint_valid_capability(
            state=state,
            bank_item_id=bank_item_id,
            bank_account_id=str(self.bank_account.uuid),
            direction=Direction.BANK_INFLOW,
            currency="USD",
            total_amount_units=100_0000,
            payment_date=stx.date_posted,
            allocations=allocations,
        )

        cmd1 = ApplyPaymentCommand(
            command_id=f"cmd-{uuid.uuid4().hex[:6]}",
            expected_state_revision=state.revision,
            session_id=state.session_id,
            issued_at=datetime(2026, 4, 5, 12, 0, tzinfo=timezone.utc),
            payment_application_id=f"payapp-{uuid.uuid4().hex[:6]}",
            bank_item_id=bank_item_id,
            bank_account_id=str(self.bank_account.uuid),
            direction=Direction.BANK_INFLOW,
            payment_date=stx.date_posted,
            total_amount_units=100_0000,
            currency="USD",
            allocations=allocations,
            capability=cap,
        )
        cmd2 = CreateRoutingDecisionCommand(
            command_id=f"cmd-route-{uuid.uuid4().hex[:6]}",
            expected_state_revision=state.revision,
            source=CommandSource.ROUTING,
            session_id=state.session_id,
            issued_at=datetime(2026, 4, 5, 12, 0, tzinfo=timezone.utc),
            routing_decision_id=f"route-{uuid.uuid4().hex[:6]}",
            book_item_id=f"invoice:{inv.uuid}",
            bank_account_id=str(self.bank_account.uuid),
            decision_source=RoutingDecisionSource.HUMAN,
        )

        batch = TransitionBatch(
            batch_id=f"batch-{uuid.uuid4().hex[:6]}",
            session_id=state.session_id,
            expected_state_revision=state.revision,
            commands=(cmd1, cmd2),
        )
        batch_res = self.engine.apply_batch(state=state, batch=batch)
        self.assertEqual(batch_res.status, TransitionStatus.REJECTED)
        self.assertIsNotNone(batch_res.rejection)
        assert batch_res.rejection is not None
        self.assertEqual(batch_res.rejection.code, RejectionCode.UNSUPPORTED_COMMAND)
        self.assertFalse(state.is_closed)

    def test_scenario_s_closed_state_rejection(self):
        """
        Scenario S:
        - Applying command to an already closed BookkeepingState
        - Rejected with STATE_CLOSED
        """
        inv = self._create_approved_invoice(amount=Decimal("100.00"))
        stx = self._create_staged_movement(amount=Decimal("100.00"))

        state = self.hydrator.hydrate(company_id=str(self.entity.uuid), session_id="session-s")
        bank_item_id = f"staged:{stx.uuid}"

        allocations = (
            PaymentApplicationObligationAllocation(
                book_item_id=f"invoice:{inv.uuid}",
                amount_units=100_0000,
            ),
        )

        cap = self._mint_valid_capability(
            state=state,
            bank_item_id=bank_item_id,
            bank_account_id=str(self.bank_account.uuid),
            direction=Direction.BANK_INFLOW,
            currency="USD",
            total_amount_units=100_0000,
            payment_date=stx.date_posted,
            allocations=allocations,
        )

        cmd = ApplyPaymentCommand(
            command_id=f"cmd-{uuid.uuid4().hex[:6]}",
            expected_state_revision=state.revision,
            session_id=state.session_id,
            issued_at=datetime(2026, 4, 5, 12, 0, tzinfo=timezone.utc),
            payment_application_id=f"payapp-{uuid.uuid4().hex[:6]}",
            bank_item_id=bank_item_id,
            bank_account_id=str(self.bank_account.uuid),
            direction=Direction.BANK_INFLOW,
            payment_date=stx.date_posted,
            total_amount_units=100_0000,
            currency="USD",
            allocations=allocations,
            capability=cap,
        )

        state.close()
        res = self.engine.apply(state=state, command=cmd)
        self.assertEqual(res.status, TransitionStatus.REJECTED)
        self.assertIsNotNone(res.rejection)
        assert res.rejection is not None
        self.assertEqual(res.rejection.code, RejectionCode.STATE_CLOSED)

    def test_scenario_t_full_round_trip_end_to_end(self):
        """
        Scenario T:
        - Complete end-to-end accounting loop:
          1. Approved invoice + Staged bank movement
          2. Stage 1 planning -> proposal & capability
          3. Stage 1 execution -> payment posted & applied
          4. Rehydration -> Stage 2 planning -> exact hypothesis matching cash leg to bank item
          5. Stage 2 execution -> CreateReconciliationCommand applied
          6. Final rehydration -> all remaining units are zero, fully balanced and reconciled!
        """
        inv = self._create_approved_invoice(amount=Decimal("450.00"), dt=date(2026, 4, 1))
        stx = self._create_staged_movement(amount=Decimal("450.00"), dt=date(2026, 4, 5))

        # 1. Hydrate state 1
        state1 = self.hydrator.hydrate(company_id=str(self.entity.uuid), session_id="session-t1")
        plan1 = plan_two_stage_bookkeeping(BookkeepingQueries(state1))
        proposal = plan1.payment_application_plan.proposals[0]
        bank_item_id = f"staged:{stx.uuid}"
        cap = plan1.get_capability(bank_item_id)
        self.assertIsNotNone(cap)
        assert cap is not None

        # 2. Apply Stage 1 payment
        cmd1 = ApplyPaymentCommand.from_intent(
            command_id=f"cmd-{uuid.uuid4().hex[:6]}",
            expected_state_revision=state1.revision,
            session_id=state1.session_id,
            issued_at=datetime(2026, 4, 5, 12, 0, tzinfo=timezone.utc),
            payment_application_id=f"payapp-{uuid.uuid4().hex[:6]}",
            plan_intent=proposal.intent,
            capability=cap,
        )
        res1 = self.engine.apply(state=state1, command=cmd1)
        self.assertEqual(res1.status, TransitionStatus.APPLIED_REQUIRES_REHYDRATION)

        # 3. Hydrate state 2
        state2 = self.hydrator.hydrate(company_id=str(self.entity.uuid), session_id="session-t2")
        queries2 = BookkeepingQueries(state2)
        plan2 = plan_two_stage_bookkeeping(queries2)

        # Verify Stage 1 has 0 proposals, and Stage 2 has 1 hypothesis
        self.assertEqual(len(plan2.payment_application_plan.proposals), 0)
        self.assertEqual(len(plan2.reconciliation_plan.hypotheses), 1)
        hyp = plan2.reconciliation_plan.hypotheses[0]

        # 4. Apply Stage 2 reconciliation
        rec_cmd = CreateReconciliationCommand(
            command_id=f"cmd-rec-{uuid.uuid4().hex[:6]}",
            expected_state_revision=state2.revision,
            source=CommandSource.RECONCILIATION,
            session_id=state2.session_id,
            issued_at=datetime(2026, 4, 5, 12, 0, tzinfo=timezone.utc),
            reconciliation_id=f"rec-{uuid.uuid4().hex[:6]}",
            bank_allocations=hyp.bank_allocations,
            book_allocations=hyp.book_allocations,
            source_hypothesis_id=hyp.id,
            source_hypothesis=hyp,
        )
        res2 = self.engine.apply(state=state2, command=rec_cmd)
        self.assertEqual(res2.status, TransitionStatus.APPLIED)

        # 5. Hydrate final state 3
        state3 = self.hydrator.hydrate(company_id=str(self.entity.uuid), session_id="session-t3")
        queries3 = BookkeepingQueries(state3)

        # Remaining capacity on bank item must be 0
        self.assertEqual(queries3.bank_remaining_units(bank_item_id), 0)
        # Remaining capacity on posted cash book item must be 0
        cash_book_id = hyp.book_allocations[0].book_item_id
        self.assertEqual(queries3.book_remaining_units(cash_book_id), 0)
        # No unallocated balances remain
        self.assertEqual(len(state3.reconciliations), 1)
        self.assertEqual(len(state3.executed_payment_applications), 1)

    def test_tampering_a_changed_obligation_id(self):
        """
        Tampering Test A:
        - Capability minted for inv1
        - Command attempts to apply to inv2
        - Rejected with INVALID_STAGE1_CAPABILITY
        """
        inv1 = self._create_approved_invoice(amount=Decimal("100.00"))
        inv2 = self._create_approved_invoice(amount=Decimal("100.00"))
        stx = self._create_staged_movement(amount=Decimal("100.00"))

        state = self.hydrator.hydrate(company_id=str(self.entity.uuid), session_id="session-tamper-a")
        bank_item_id = f"staged:{stx.uuid}"

        allocs_mint = (
            PaymentApplicationObligationAllocation(
                book_item_id=f"invoice:{inv1.uuid}",
                amount_units=100_0000,
            ),
        )
        cap = self._mint_valid_capability(
            state=state,
            bank_item_id=bank_item_id,
            bank_account_id=str(self.bank_account.uuid),
            direction=Direction.BANK_INFLOW,
            currency="USD",
            total_amount_units=100_0000,
            payment_date=stx.date_posted,
            allocations=allocs_mint,
        )

        allocs_tampered = (
            PaymentApplicationObligationAllocation(
                book_item_id=f"invoice:{inv2.uuid}",
                amount_units=100_0000,
            ),
        )
        cmd = ApplyPaymentCommand(
            command_id=f"cmd-{uuid.uuid4().hex[:6]}",
            expected_state_revision=state.revision,
            session_id=state.session_id,
            issued_at=datetime(2026, 4, 5, 12, 0, tzinfo=timezone.utc),
            payment_application_id=f"payapp-{uuid.uuid4().hex[:6]}",
            bank_item_id=bank_item_id,
            bank_account_id=str(self.bank_account.uuid),
            direction=Direction.BANK_INFLOW,
            payment_date=stx.date_posted,
            total_amount_units=100_0000,
            currency="USD",
            allocations=allocs_tampered,
            capability=cap,
        )

        res = self.engine.apply(state=state, command=cmd)
        self.assertEqual(res.status, TransitionStatus.REJECTED)
        self.assertIsNotNone(res.rejection)
        assert res.rejection is not None
        self.assertEqual(res.rejection.code, RejectionCode.INVALID_STAGE1_CAPABILITY)
        self.assertFalse(state.is_closed)

    def test_tampering_b_changed_allocation_amount(self):
        """
        Tampering Test B:
        - Capability minted for amount 500.00
        - Command specifies altered allocation amount 400.00
        - Rejected with INVALID_STAGE1_CAPABILITY
        """
        inv = self._create_approved_invoice(amount=Decimal("500.00"))
        stx = self._create_staged_movement(amount=Decimal("500.00"))

        state = self.hydrator.hydrate(company_id=str(self.entity.uuid), session_id="session-tamper-b")
        bank_item_id = f"staged:{stx.uuid}"

        allocs_mint = (
            PaymentApplicationObligationAllocation(
                book_item_id=f"invoice:{inv.uuid}",
                amount_units=500_0000,
            ),
        )
        cap = self._mint_valid_capability(
            state=state,
            bank_item_id=bank_item_id,
            bank_account_id=str(self.bank_account.uuid),
            direction=Direction.BANK_INFLOW,
            currency="USD",
            total_amount_units=500_0000,
            payment_date=stx.date_posted,
            allocations=allocs_mint,
        )

        allocs_tampered = (
            PaymentApplicationObligationAllocation(
                book_item_id=f"invoice:{inv.uuid}",
                amount_units=400_0000,
            ),
        )
        cmd = ApplyPaymentCommand(
            command_id=f"cmd-{uuid.uuid4().hex[:6]}",
            expected_state_revision=state.revision,
            session_id=state.session_id,
            issued_at=datetime(2026, 4, 5, 12, 0, tzinfo=timezone.utc),
            payment_application_id=f"payapp-{uuid.uuid4().hex[:6]}",
            bank_item_id=bank_item_id,
            bank_account_id=str(self.bank_account.uuid),
            direction=Direction.BANK_INFLOW,
            payment_date=stx.date_posted,
            total_amount_units=400_0000,
            currency="USD",
            allocations=allocs_tampered,
            capability=cap,
        )

        res = self.engine.apply(state=state, command=cmd)
        self.assertEqual(res.status, TransitionStatus.REJECTED)
        self.assertIsNotNone(res.rejection)
        assert res.rejection is not None
        self.assertEqual(res.rejection.code, RejectionCode.INVALID_STAGE1_CAPABILITY)

    def test_tampering_c_changed_bank_account_id(self):
        """
        Tampering Test C:
        - Capability minted for bank_account_id
        - Command specifies tampered bank_account_id
        - Rejected with INVALID_STAGE1_CAPABILITY
        """
        inv = self._create_approved_invoice(amount=Decimal("100.00"))
        stx = self._create_staged_movement(amount=Decimal("100.00"))

        state = self.hydrator.hydrate(company_id=str(self.entity.uuid), session_id="session-tamper-c")
        bank_item_id = f"staged:{stx.uuid}"

        allocs = (
            PaymentApplicationObligationAllocation(
                book_item_id=f"invoice:{inv.uuid}",
                amount_units=100_0000,
            ),
        )
        cap = self._mint_valid_capability(
            state=state,
            bank_item_id=bank_item_id,
            bank_account_id=str(self.bank_account.uuid),
            direction=Direction.BANK_INFLOW,
            currency="USD",
            total_amount_units=100_0000,
            payment_date=stx.date_posted,
            allocations=allocs,
        )

        cmd = ApplyPaymentCommand(
            command_id=f"cmd-{uuid.uuid4().hex[:6]}",
            expected_state_revision=state.revision,
            session_id=state.session_id,
            issued_at=datetime(2026, 4, 5, 12, 0, tzinfo=timezone.utc),
            payment_application_id=f"payapp-{uuid.uuid4().hex[:6]}",
            bank_item_id=bank_item_id,
            bank_account_id=f"tampered-acc-{uuid.uuid4().hex[:6]}",
            direction=Direction.BANK_INFLOW,
            payment_date=stx.date_posted,
            total_amount_units=100_0000,
            currency="USD",
            allocations=allocs,
            capability=cap,
        )

        res = self.engine.apply(state=state, command=cmd)
        self.assertEqual(res.status, TransitionStatus.REJECTED)
        self.assertIsNotNone(res.rejection)
        assert res.rejection is not None
        self.assertEqual(res.rejection.code, RejectionCode.INVALID_STAGE1_CAPABILITY)

    def test_tampering_d_changed_payment_date(self):
        """
        Tampering Test D:
        - Capability minted for payment_date 2026-04-05
        - Command specifies tampered payment_date 2026-04-06
        - Rejected with INVALID_STAGE1_CAPABILITY
        """
        inv = self._create_approved_invoice(amount=Decimal("100.00"))
        stx = self._create_staged_movement(amount=Decimal("100.00"), dt=date(2026, 4, 5))

        state = self.hydrator.hydrate(company_id=str(self.entity.uuid), session_id="session-tamper-d")
        bank_item_id = f"staged:{stx.uuid}"

        allocs = (
            PaymentApplicationObligationAllocation(
                book_item_id=f"invoice:{inv.uuid}",
                amount_units=100_0000,
            ),
        )
        cap = self._mint_valid_capability(
            state=state,
            bank_item_id=bank_item_id,
            bank_account_id=str(self.bank_account.uuid),
            direction=Direction.BANK_INFLOW,
            currency="USD",
            total_amount_units=100_0000,
            payment_date=date(2026, 4, 5),
            allocations=allocs,
        )

        cmd = ApplyPaymentCommand(
            command_id=f"cmd-{uuid.uuid4().hex[:6]}",
            expected_state_revision=state.revision,
            session_id=state.session_id,
            issued_at=datetime(2026, 4, 5, 12, 0, tzinfo=timezone.utc),
            payment_application_id=f"payapp-{uuid.uuid4().hex[:6]}",
            bank_item_id=bank_item_id,
            bank_account_id=str(self.bank_account.uuid),
            direction=Direction.BANK_INFLOW,
            payment_date=date(2026, 4, 6),
            total_amount_units=100_0000,
            currency="USD",
            allocations=allocs,
            capability=cap,
        )

        res = self.engine.apply(state=state, command=cmd)
        self.assertEqual(res.status, TransitionStatus.REJECTED)
        self.assertIsNotNone(res.rejection)
        assert res.rejection is not None
        self.assertEqual(res.rejection.code, RejectionCode.INVALID_STAGE1_CAPABILITY)

    def test_tampering_e_changed_direction_or_currency(self):
        """
        Tampering Test E:
        - Capability minted for BANK_INFLOW and USD
        - Command specifies BANK_OUTFLOW or EUR
        - Both rejected with INVALID_STAGE1_CAPABILITY
        """
        inv = self._create_approved_invoice(amount=Decimal("100.00"))
        stx = self._create_staged_movement(amount=Decimal("100.00"))

        state = self.hydrator.hydrate(company_id=str(self.entity.uuid), session_id="session-tamper-e")
        bank_item_id = f"staged:{stx.uuid}"

        allocs = (
            PaymentApplicationObligationAllocation(
                book_item_id=f"invoice:{inv.uuid}",
                amount_units=100_0000,
            ),
        )
        cap = self._mint_valid_capability(
            state=state,
            bank_item_id=bank_item_id,
            bank_account_id=str(self.bank_account.uuid),
            direction=Direction.BANK_INFLOW,
            currency="USD",
            total_amount_units=100_0000,
            payment_date=stx.date_posted,
            allocations=allocs,
        )

        # 1. Tampered direction
        cmd_dir = ApplyPaymentCommand(
            command_id=f"cmd-{uuid.uuid4().hex[:6]}",
            expected_state_revision=state.revision,
            session_id=state.session_id,
            issued_at=datetime(2026, 4, 5, 12, 0, tzinfo=timezone.utc),
            payment_application_id=f"payapp-{uuid.uuid4().hex[:6]}",
            bank_item_id=bank_item_id,
            bank_account_id=str(self.bank_account.uuid),
            direction=Direction.BANK_OUTFLOW,
            payment_date=stx.date_posted,
            total_amount_units=100_0000,
            currency="USD",
            allocations=allocs,
            capability=cap,
        )
        res_dir = self.engine.apply(state=state, command=cmd_dir)
        self.assertEqual(res_dir.status, TransitionStatus.REJECTED)
        self.assertIsNotNone(res_dir.rejection)
        assert res_dir.rejection is not None
        self.assertEqual(res_dir.rejection.code, RejectionCode.INVALID_STAGE1_CAPABILITY)

        # 2. Tampered currency
        cmd_curr = ApplyPaymentCommand(
            command_id=f"cmd-{uuid.uuid4().hex[:6]}",
            expected_state_revision=state.revision,
            session_id=state.session_id,
            issued_at=datetime(2026, 4, 5, 12, 0, tzinfo=timezone.utc),
            payment_application_id=f"payapp-{uuid.uuid4().hex[:6]}",
            bank_item_id=bank_item_id,
            bank_account_id=str(self.bank_account.uuid),
            direction=Direction.BANK_INFLOW,
            payment_date=stx.date_posted,
            total_amount_units=100_0000,
            currency="EUR",
            allocations=allocs,
            capability=cap,
        )
        res_curr = self.engine.apply(state=state, command=cmd_curr)
        self.assertEqual(res_curr.status, TransitionStatus.REJECTED)
        self.assertIsNotNone(res_curr.rejection)
        assert res_curr.rejection is not None
        self.assertEqual(res_curr.rejection.code, RejectionCode.INVALID_STAGE1_CAPABILITY)

    def test_secret_management_part_a_explicit_injected_secret(self):
        """
        Part A - Test A: Explicit injected test secret works and takes highest priority.
        """
        explicit_secret = b"explicit-secret-for-test-32bytes!"
        self.assertEqual(get_stage1_capability_secret(secret_key=explicit_secret), explicit_secret)

        # Even if environment is set, explicit argument wins
        with patch.dict(os.environ, {"STAGE1_CAPABILITY_SECRET": "env-secret"}):
            self.assertEqual(get_stage1_capability_secret(secret_key=explicit_secret), explicit_secret)

    def test_secret_management_part_b_env_and_settings_secret(self):
        """
        Part A - Test B: Environment variable and Django settings capability secrets work.
        """
        with patch.dict(os.environ, {"STAGE1_CAPABILITY_SECRET": "env-override-secret-xyz"}):
            self.assertEqual(get_stage1_capability_secret(), b"env-override-secret-xyz")

        with patch.dict(os.environ, {"STAGE1_CAPABILITY_SECRET": ""}):
            from django.conf import settings
            with patch.object(settings, "STAGE1_CAPABILITY_SECRET", "settings-secret-abc"):
                self.assertEqual(get_stage1_capability_secret(), b"settings-secret-abc")

    def test_secret_management_part_c_hkdf_derivation_from_django_secret_key(self):
        """
        Part A - Test C: HKDF derivation from Django SECRET_KEY works deterministically.
        """
        from django.conf import settings
        import hashlib

        with patch.dict(os.environ, {"STAGE1_CAPABILITY_SECRET": ""}):
            with patch.object(settings, "STAGE1_CAPABILITY_SECRET", ""):
                with patch.object(settings, "SECRET_KEY", "test-django-secret-key-123456789"):
                    derived = get_stage1_capability_secret()
                    from bookkeeping_state.domain.commands import hkdf_sha256
                    expected_hkdf = hkdf_sha256(
                        b"test-django-secret-key-123456789",
                        salt=b"stage1-capability-salt",
                        info=b"stage1-execution-capability-signing-key",
                        length=32,
                    )
                    self.assertEqual(derived, expected_hkdf)
                    self.assertEqual(len(derived), 32)

    def test_secret_management_part_d_no_secret_source_fails_closed(self):
        """
        Part A - Test D: When no secret source is configured, system FAILS CLOSED with RuntimeError.
        No deterministic fallback seed is ever used.
        """
        from django.conf import settings

        with patch.dict(os.environ, {"STAGE1_CAPABILITY_SECRET": ""}):
            with patch.object(settings, "STAGE1_CAPABILITY_SECRET", ""):
                with patch.object(settings, "SECRET_KEY", ""):
                    with self.assertRaises(RuntimeError) as ctx:
                        get_stage1_capability_secret()
                    self.assertIn("not configured", str(ctx.exception))

                    # Capability minting must also fail closed
                    with self.assertRaises(RuntimeError) as ctx_mint:
                        mint_stage1_capability(
                            bank_item_id="staged:123",
                            bank_account_id="acc:123",
                            direction=Direction.BANK_INFLOW,
                            currency="USD",
                            total_amount_units=100_0000,
                            payment_date=date(2026, 4, 5),
                            allocations=(),
                            session_id="session-fail-closed",
                            base_state_revision=0,
                            base_state_fingerprint="fp",
                        )
                    self.assertIn("not configured", str(ctx_mint.exception))

    def test_secret_management_part_e_two_different_secrets_cannot_verify(self):
        """
        Part A - Test E: Two different secrets cannot verify each other's capability.
        """
        secret_a = b"secret-key-alpha-32bytes-padding!"
        secret_b = b"secret-key-bravo-32bytes-padding!"

        inv = self._create_approved_invoice(amount=Decimal("100.00"))
        stx = self._create_staged_movement(amount=Decimal("100.00"))
        state = self.hydrator.hydrate(company_id=str(self.entity.uuid), session_id="session-sec-e")
        bank_item_id = f"staged:{stx.uuid}"

        allocs = (
            PaymentApplicationObligationAllocation(
                book_item_id=f"invoice:{inv.uuid}",
                amount_units=100_0000,
            ),
        )

        cap_a = mint_stage1_capability(
            bank_item_id=bank_item_id,
            bank_account_id=str(self.bank_account.uuid),
            direction=Direction.BANK_INFLOW,
            currency="USD",
            total_amount_units=100_0000,
            payment_date=stx.date_posted,
            allocations=allocs,
            session_id=state.session_id,
            base_state_revision=state.revision,
            base_state_fingerprint=state_fingerprint(state),
            secret_key=secret_a,
        )

        # secret_a verifies successfully
        self.assertTrue(
            verify_stage1_capability(
                capability=cap_a,
                expected_bank_item_id=bank_item_id,
                expected_bank_account_id=str(self.bank_account.uuid),
                expected_direction=Direction.BANK_INFLOW,
                expected_currency="USD",
                expected_total_amount_units=100_0000,
                expected_payment_date=stx.date_posted,
                expected_allocations=allocs,
                expected_session_id=state.session_id,
                expected_state_revision=state.revision,
                expected_state_fingerprint=state_fingerprint(state),
                secret_key=secret_a,
            )
        )

        # secret_b fails verification
        self.assertFalse(
            verify_stage1_capability(
                capability=cap_a,
                expected_bank_item_id=bank_item_id,
                expected_bank_account_id=str(self.bank_account.uuid),
                expected_direction=Direction.BANK_INFLOW,
                expected_currency="USD",
                expected_total_amount_units=100_0000,
                expected_payment_date=stx.date_posted,
                expected_allocations=allocs,
                expected_session_id=state.session_id,
                expected_state_revision=state.revision,
                expected_state_fingerprint=state_fingerprint(state),
                secret_key=secret_b,
            )
        )

    def test_payment_date_exact_propagation_regression(self):
        """
        Verify deterministic payment date propagation from BankItem.date (2026-08-15)
        through intent, command, and accounting kernel (JournalEntry.timestamp.date() == 2026-08-15).
        """
        movement_date = date(2026, 8, 15)
        inv = self._create_approved_invoice(amount=Decimal("750.00"), dt=date(2026, 8, 1))
        stx = self._create_staged_movement(amount=Decimal("750.00"), dt=movement_date)

        state = self.hydrator.hydrate(company_id=str(self.entity.uuid), session_id="session-date-prop")
        queries = BookkeepingQueries(state)
        plan = plan_two_stage_bookkeeping(queries)
        self.assertEqual(len(plan.payment_application_plan.proposals), 1)
        proposal = plan.payment_application_plan.proposals[0]

        # Verify date on intent
        self.assertEqual(proposal.intent.payment_date, movement_date)

        cap = plan.get_capability(proposal.bank_item_id)
        assert cap is not None

        cmd = ApplyPaymentCommand.from_intent(
            command_id=f"cmd-{uuid.uuid4().hex[:6]}",
            expected_state_revision=state.revision,
            session_id=state.session_id,
            issued_at=datetime(2026, 9, 9, 12, 0, tzinfo=timezone.utc),  # Execution timestamp is September
            payment_application_id=f"payapp-{uuid.uuid4().hex[:6]}",
            plan_intent=proposal.intent,
            capability=cap,
        )
        self.assertEqual(cmd.payment_date, movement_date)

        res = self.engine.apply(state=state, command=cmd)
        self.assertEqual(res.status, TransitionStatus.APPLIED_REQUIRES_REHYDRATION)

        alloc = BookkeepingPaymentApplicationAllocation.objects.get(payment_application_id=cmd.payment_application_id)
        assert alloc.cash_transaction is not None
        je = alloc.cash_transaction.journal_entry
        self.assertEqual(je.timestamp.date(), movement_date)

    def test_cash_account_non_destructive_end_to_end_regression(self):
        """
        End-to-end regression: InvoiceModel and BillModel cash_account fields remain
        untouched across full Stage-1 engine execution, while posted cash legs target bank cash account.
        """
        # Invoice test
        inv = self._create_approved_invoice(amount=Decimal("200.00"), dt=date(2026, 4, 1))
        stx_inv = self._create_staged_movement(amount=Decimal("200.00"), dt=date(2026, 4, 5))

        state1 = self.hydrator.hydrate(company_id=str(self.entity.uuid), session_id="session-non-destruct-inv")
        plan1 = plan_two_stage_bookkeeping(BookkeepingQueries(state1))
        prop1 = plan1.payment_application_plan.proposals[0]
        cap1 = plan1.get_capability(prop1.bank_item_id)
        assert cap1 is not None

        cmd1 = ApplyPaymentCommand.from_intent(
            command_id=f"cmd-{uuid.uuid4().hex[:6]}",
            expected_state_revision=state1.revision,
            session_id=state1.session_id,
            issued_at=datetime(2026, 4, 5, 12, 0, tzinfo=timezone.utc),
            payment_application_id=f"payapp-{uuid.uuid4().hex[:6]}",
            plan_intent=prop1.intent,
            capability=cap1,
        )
        res1 = self.engine.apply(state=state1, command=cmd1)
        self.assertEqual(res1.status, TransitionStatus.APPLIED_REQUIRES_REHYDRATION)

        inv.refresh_from_db()
        self.assertEqual(inv.cash_account, self.cash_account_default)
        alloc1 = BookkeepingPaymentApplicationAllocation.objects.get(payment_application_id=cmd1.payment_application_id)
        assert alloc1.cash_transaction is not None
        self.assertEqual(alloc1.cash_transaction.account_id, self.cash_account_target.pk)

        # Bill test
        bill = self._create_approved_bill(amount=Decimal("150.00"), dt=date(2026, 4, 1))
        stx_bill = self._create_staged_movement(amount=Decimal("-150.00"), dt=date(2026, 4, 5))

        state2 = self.hydrator.hydrate(company_id=str(self.entity.uuid), session_id="session-non-destruct-bill")
        plan2 = plan_two_stage_bookkeeping(BookkeepingQueries(state2))
        prop2 = plan2.payment_application_plan.proposals[0]
        cap2 = plan2.get_capability(prop2.bank_item_id)
        assert cap2 is not None

        cmd2 = ApplyPaymentCommand.from_intent(
            command_id=f"cmd-{uuid.uuid4().hex[:6]}",
            expected_state_revision=state2.revision,
            session_id=state2.session_id,
            issued_at=datetime(2026, 4, 5, 12, 0, tzinfo=timezone.utc),
            payment_application_id=f"payapp-{uuid.uuid4().hex[:6]}",
            plan_intent=prop2.intent,
            capability=cap2,
        )
        res2 = self.engine.apply(state=state2, command=cmd2)
        self.assertEqual(res2.status, TransitionStatus.APPLIED_REQUIRES_REHYDRATION)

        bill.refresh_from_db()
        self.assertEqual(bill.cash_account, self.cash_account_default)
        alloc2 = BookkeepingPaymentApplicationAllocation.objects.get(payment_application_id=cmd2.payment_application_id)
        assert alloc2.cash_transaction is not None
        self.assertEqual(alloc2.cash_transaction.account_id, self.cash_account_target.pk)

    def test_multi_line_invoice_one_cash_tx_cardinality_proof(self):
        """
        Cardinality proof: Paying an approved invoice with 3 line items creates
        EXACTLY ONE cash TransactionModel line.
        """
        amounts = [Decimal("100.00"), Decimal("200.00"), Decimal("300.00")]
        inv = self._create_approved_invoice_multi_line(amounts=amounts, dt=date(2026, 4, 1))
        self.assertEqual(ItemTransactionModel.objects.filter(invoice_model=inv).count(), 3)
        self.assertEqual(inv.amount_due, Decimal("600.00"))

        stx = self._create_staged_movement(amount=Decimal("600.00"), dt=date(2026, 4, 5))
        state = self.hydrator.hydrate(company_id=str(self.entity.uuid), session_id="session-ml-inv")
        plan = plan_two_stage_bookkeeping(BookkeepingQueries(state))
        proposal = plan.payment_application_plan.proposals[0]
        cap = plan.get_capability(proposal.bank_item_id)
        assert cap is not None

        cmd = ApplyPaymentCommand.from_intent(
            command_id=f"cmd-{uuid.uuid4().hex[:6]}",
            expected_state_revision=state.revision,
            session_id=state.session_id,
            issued_at=datetime(2026, 4, 5, 12, 0, tzinfo=timezone.utc),
            payment_application_id=f"payapp-{uuid.uuid4().hex[:6]}",
            plan_intent=proposal.intent,
            capability=cap,
        )
        res = self.engine.apply(state=state, command=cmd)
        self.assertEqual(res.status, TransitionStatus.APPLIED_REQUIRES_REHYDRATION)

        durable_app: Any = BookkeepingPaymentApplication.objects.get(id=cmd.payment_application_id)
        self.assertEqual(durable_app.allocations.count(), 1)
        alloc = durable_app.allocations.first()
        assert alloc.cash_transaction is not None

        # Verify exactly one cash TransactionModel row exists in the journal entry
        je = alloc.cash_transaction.journal_entry
        cash_txs = list(je.transactionmodel_set.filter(account=self.cash_account_target))
        self.assertEqual(len(cash_txs), 1)
        self.assertEqual(cash_txs[0].amount, Decimal("600.00"))
        self.assertEqual(cash_txs[0].tx_type, TransactionModel.DEBIT)

    def test_multi_line_bill_one_cash_tx_cardinality_proof(self):
        """
        Cardinality proof: Paying an approved bill with 3 line items creates
        EXACTLY ONE cash TransactionModel line.
        """
        amounts = [Decimal("70.00"), Decimal("130.00"), Decimal("200.00")]
        bill = self._create_approved_bill_multi_line(amounts=amounts, dt=date(2026, 4, 1))
        self.assertEqual(ItemTransactionModel.objects.filter(bill_model=bill).count(), 3)
        self.assertEqual(bill.amount_due, Decimal("400.00"))

        stx = self._create_staged_movement(amount=Decimal("-400.00"), dt=date(2026, 4, 5))
        state = self.hydrator.hydrate(company_id=str(self.entity.uuid), session_id="session-ml-bill")
        plan = plan_two_stage_bookkeeping(BookkeepingQueries(state))
        proposal = plan.payment_application_plan.proposals[0]
        cap = plan.get_capability(proposal.bank_item_id)
        assert cap is not None

        cmd = ApplyPaymentCommand.from_intent(
            command_id=f"cmd-{uuid.uuid4().hex[:6]}",
            expected_state_revision=state.revision,
            session_id=state.session_id,
            issued_at=datetime(2026, 4, 5, 12, 0, tzinfo=timezone.utc),
            payment_application_id=f"payapp-{uuid.uuid4().hex[:6]}",
            plan_intent=proposal.intent,
            capability=cap,
        )
        res = self.engine.apply(state=state, command=cmd)
        self.assertEqual(res.status, TransitionStatus.APPLIED_REQUIRES_REHYDRATION)

        durable_app: Any = BookkeepingPaymentApplication.objects.get(id=cmd.payment_application_id)
        self.assertEqual(durable_app.allocations.count(), 1)
        alloc = durable_app.allocations.first()
        assert alloc.cash_transaction is not None

        # Verify exactly one cash TransactionModel row exists in the journal entry
        je = alloc.cash_transaction.journal_entry
        cash_txs = list(je.transactionmodel_set.filter(account=self.cash_account_target))
        self.assertEqual(len(cash_txs), 1)
        self.assertEqual(cash_txs[0].amount, Decimal("400.00"))
        self.assertEqual(cash_txs[0].tx_type, TransactionModel.CREDIT)

    def test_active_stage2_partial_allocation_defense(self):
        """
        Hardened defense: Even if a bank movement has partial active Stage-2 allocation
        (e.g., movement $1000, Stage-2 reconciliation allocating $400), Stage-1 MUST reject
        any ApplyPaymentCommand on that bank item with ACTIVE_STAGE2_ALLOCATION_EXISTS.
        """
        inv = self._create_approved_invoice(amount=Decimal("600.00"))
        stx = self._create_staged_movement(amount=Decimal("1000.00"))

        rec = BookkeepingReconciliation.objects.create(
            id=f"rec-{uuid.uuid4().hex[:8]}",
            entity=self.entity,
            state_revision_at_creation=0,
            created_at=datetime.now(timezone.utc),
        )
        BookkeepingReconciliationBankAllocation.objects.create(
            reconciliation=rec,
            staged_transaction=stx,
            amount_units=400_0000,  # Partial allocation of 400 out of 1000
        )
        tx = self._create_cash_tx(amount=Decimal("400.00"), is_debit=True)
        BookkeepingReconciliationBookAllocation.objects.create(
            reconciliation=rec,
            transaction=tx,
            amount_units=400_0000,
        )

        state = self.hydrator.hydrate(company_id=str(self.entity.uuid), session_id="session-partial-s2", require_valid=False)
        bank_item_id = f"staged:{stx.uuid}"

        allocations = (
            PaymentApplicationObligationAllocation(
                book_item_id=f"invoice:{inv.uuid}",
                amount_units=600_0000,
            ),
        )

        cap = self._mint_valid_capability(
            state=state,
            bank_item_id=bank_item_id,
            bank_account_id=str(self.bank_account.uuid),
            direction=Direction.BANK_INFLOW,
            currency="USD",
            total_amount_units=600_0000,
            payment_date=stx.date_posted,
            allocations=allocations,
        )

        cmd = ApplyPaymentCommand(
            command_id=f"cmd-{uuid.uuid4().hex[:6]}",
            expected_state_revision=state.revision,
            session_id=state.session_id,
            issued_at=datetime(2026, 4, 5, 12, 0, tzinfo=timezone.utc),
            payment_application_id=f"payapp-{uuid.uuid4().hex[:6]}",
            bank_item_id=bank_item_id,
            bank_account_id=str(self.bank_account.uuid),
            direction=Direction.BANK_INFLOW,
            payment_date=stx.date_posted,
            total_amount_units=600_0000,
            currency="USD",
            allocations=allocations,
            capability=cap,
        )

        res = self.engine.apply(state=state, command=cmd)
        self.assertEqual(res.status, TransitionStatus.REJECTED)
        self.assertIsNotNone(res.rejection)
        assert res.rejection is not None
        self.assertEqual(res.rejection.code, RejectionCode.ACTIVE_STAGE2_ALLOCATION_EXISTS)
        self.assertEqual(state.revision, 0)
        self.assertFalse(state.is_closed)

    def test_plaid_movement_excluded_from_state_bank_items_inflow(self):
        """
        PlaidTransaction rows are upstream staging and must NOT hydrate into BookkeepingState.bank_items.
        """
        plaid_tx = self._create_plaid_movement(amount=Decimal("-250.00"), dt=date(2026, 4, 5))
        state = self.hydrator.hydrate(company_id=str(self.entity.uuid), session_id="session-norm-p-in")
        bank_item_id = f"plaid:{plaid_tx.uuid}"

        self.assertNotIn(bank_item_id, state.bank_items)
        self.assertFalse(any(b_id.startswith("plaid:") for b_id in state.bank_items))

    def test_plaid_movement_excluded_from_state_bank_items_outflow(self):
        """
        PlaidTransaction rows are upstream staging and must NOT hydrate into BookkeepingState.bank_items.
        """
        plaid_tx = self._create_plaid_movement(amount=Decimal("350.00"), dt=date(2026, 4, 5))
        state = self.hydrator.hydrate(company_id=str(self.entity.uuid), session_id="session-norm-p-out")
        bank_item_id = f"plaid:{plaid_tx.uuid}"

        self.assertNotIn(bank_item_id, state.bank_items)
        self.assertFalse(any(b_id.startswith("plaid:") for b_id in state.bank_items))

    def test_sign_normalization_staged_inflow(self):
        """
        Staged sign normalization: Positive Staged amount (+400.00) is BANK_INFLOW of 400.00.
        """
        stx = self._create_staged_movement(amount=Decimal("400.00"), dt=date(2026, 4, 5))
        state = self.hydrator.hydrate(company_id=str(self.entity.uuid), session_id="session-norm-s-in")
        bank_item_id = f"staged:{stx.uuid}"

        self.assertIn(bank_item_id, state.bank_items)
        item = state.bank_items[bank_item_id]
        self.assertEqual(item.direction, Direction.BANK_INFLOW)
        self.assertEqual(int(item.amount_units), 400_0000)

    def test_sign_normalization_staged_outflow(self):
        """
        Staged sign normalization: Negative Staged amount (-450.00) is BANK_OUTFLOW of 450.00.
        """
        stx = self._create_staged_movement(amount=Decimal("-450.00"), dt=date(2026, 4, 5))
        state = self.hydrator.hydrate(company_id=str(self.entity.uuid), session_id="session-norm-s-out")
        bank_item_id = f"staged:{stx.uuid}"

        self.assertIn(bank_item_id, state.bank_items)
        item = state.bank_items[bank_item_id]
        self.assertEqual(item.direction, Direction.BANK_OUTFLOW)
        self.assertEqual(int(item.amount_units), 450_0000)


