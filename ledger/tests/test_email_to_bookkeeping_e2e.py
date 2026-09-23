"""
End-to-End Integration Test: Email / Document Ingestion to Production Bookkeeping.

Simulates the complete production lifecycle:
1. Inbound Postmark bank statement document intake via Go OCR -> Django AccountingIntakeService.
2. Ingestion produces ImportJobModel + StagedTransactionModel rows with transaction_model=None.
3. Successful intake emits events.bookkeeping.trigger on commit, recorded in BookkeepingEntityTrigger.
4. Coalesced session runner claims execution and runs BookkeepingSession with wire-level NATS categorizers.
5. All 4 controlled transactions are verified:
   - Case 1: Inflow 10,000 MAD applied to open approved invoice (INV-SYNTH-2026-001) in Stage 1.
   - Case 3: Inflow 2,500 MAD reconciled with pre-posted cash GL movement in Stage 2.
   - Case 4: Outflow 3,400 MAD classified to PCGE 4456 (DGI TVA) and posted to GL in Residual stage.
   - Case 7: Outflow 7,850 MAD classified as HOLD with ZERO residual postings.
6. Final state passes authoritative validate_state (0 violations).
7. Re-triggering execution is safe and idempotent (0 duplicate postings).
8. Accountant REPL loader (BookkeepingWorkbench) successfully hydrates live persisted state.
"""

from __future__ import annotations

from datetime import date, datetime, timezone as dt_timezone
from decimal import Decimal
import json
from unittest.mock import patch
from uuid import uuid4

from django.contrib.auth import get_user_model
from django.db import connection, transaction
from django.test import TestCase

from bookkeeping_state.dag.nats_book_categorizer import NatsAseBookCategorizer
from bookkeeping_state.dag.transport_models import (
    BOOK_CATEGORIZATION_DAG_ID,
    BOOK_CATEGORIZATION_SCHEMA_VERSION,
)
from bookkeeping_state.bank_categorization.nats_bank_categorizer import (
    NatsAseBankCategorizer,
)
from bookkeeping_state.bank_categorization import (
    BANK_CATEGORIZATION_DAG_ID,
    BANK_CATEGORIZE_SCHEMA_VERSION,
)
from bookkeeping_state.hydration.hydrator import BookkeepingHydrator
from bookkeeping_state.operator.workbench import BookkeepingWorkbench
from bookkeeping_state.persistence.repository import BookkeepingRepository
from bookkeeping_state.session.service import (
    create_production_bookkeeping_application_service,
)
from bookkeeping_state.state.queries import BookkeepingQueries
from bookkeeping_state.state.validation import validate_state
from ledger.bookkeeping_state.intake.service import AccountingIntakeService
from ledger.bookkeeping_state.session.runner import process_entity_triggers
from ledger.io.roles import (
    ASSET_CA_CASH,
    ASSET_CA_PREPAID,
    ASSET_CA_RECEIVABLES,
    CREDIT,
    DEBIT,
    EXPENSE_OPERATIONAL,
    INCOME_OPERATIONAL,
    LIABILITY_CL_ACC_PAYABLE,
    LIABILITY_CL_DEFERRED_REVENUE,
    LIABILITY_CL_TAXES_PAYABLE,
)
from ledger.models.accounts import AccountModel
from ledger.models.bank_account import BankAccountModel
from ledger.models.bookkeeping import (
    BookkeepingEntityTrigger,
    BookkeepingPaymentApplication,
    BookkeepingReconciliation,
    BookkeepingReconciliationBookAllocation,
    BookkeepingResidualBankClassificationDecision,
    BookkeepingResidualBankPosting,
)
from ledger.models.customer import CustomerModel
from ledger.models.data_import import ImportJobModel, StagedTransactionModel
from ledger.models.entity import EntityModel
from ledger.models.invoice import InvoiceModel
from ledger.models.items import ItemModel, ItemTransactionModel, UnitOfMeasureModel
from ledger.models.journal_entry import JournalEntryModel
from ledger.models.ledger import LedgerModel
from ledger.models.transactions import TransactionModel


def ensure_toro_core_tables():
    with connection.cursor() as cursor:
        cursor.execute("CREATE SCHEMA IF NOT EXISTS toro_core;")
        cursor.execute("""
            CREATE TABLE IF NOT EXISTS toro_core.documents (
                id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
                session_id TEXT NOT NULL,
                document_type TEXT NOT NULL,
                file_name TEXT NOT NULL,
                mime_type TEXT NOT NULL,
                s3_url TEXT NOT NULL,
                sha256 TEXT,
                ocr_status TEXT NOT NULL DEFAULT 'PENDING',
                raw_ocr_json JSONB NOT NULL DEFAULT '{}',
                extracted_text TEXT,
                sender_email TEXT,
                source_channel TEXT NOT NULL DEFAULT 'EMAIL',
                metadata JSONB NOT NULL DEFAULT '{}',
                processed_at TIMESTAMPTZ,
                created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
                updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
            );
        """)


def insert_mock_document(
    doc_id: str,
    session_id: str,
    doc_type: str,
    file_name: str,
    raw_ocr_json: dict,
    metadata: dict,
):
    ensure_toro_core_tables()
    with connection.cursor() as cursor:
        cursor.execute(
            """
            INSERT INTO toro_core.documents (
                id, session_id, document_type, file_name, mime_type, s3_url, sha256,
                ocr_status, raw_ocr_json, metadata, source_channel
            ) VALUES (
                %s, %s, %s, %s, %s, %s, %s, 'OCR_SUCCESS', %s, %s, 'EMAIL'
            ) ON CONFLICT (id) DO UPDATE SET
                raw_ocr_json = EXCLUDED.raw_ocr_json,
                ocr_status = EXCLUDED.ocr_status,
                metadata = EXCLUDED.metadata;
        """,
            [
                doc_id,
                session_id,
                doc_type.upper(),
                file_name,
                "application/pdf",
                f"s3://bucket/{file_name}",
                "dummy_sha256",
                json.dumps(raw_ocr_json),
                json.dumps(metadata),
            ],
        )


class EmailToBookkeepingE2ETest(TestCase):
    """
    Exhaustive production-grade E2E test verifying:
    Postmark Email Ingestion -> Go OCR JSON -> Django Accounting Intake -> Entity Trigger
    -> Coalesced BookkeepingSession -> Moroccan PCGE Hydration -> State Validation -> REPL Proof.
    """

    databases = {"default"}

    @classmethod
    def setUpTestData(cls):
        ensure_toro_core_tables()

        UserModel = get_user_model()
        cls.user = UserModel.objects.create_user(
            username=f"accountant_e2e_{uuid4().hex[:8]}",
            email="accountant_e2e@fignode.com",
            password="testpassword123",
        )

        # 1. Authoritative Moroccan PCGE Entity
        cls.entity = EntityModel.add_root(
            name="Atlas Trading Production SARL",
            admin=cls.user,
            currency="MAD",
            fy_start_month=1,
            accrual_method=False,
        )

        cls.coa = cls.entity.create_chart_of_accounts(
            coa_name="Moroccan PCGE Standard CoA",
            assign_as_default=True,
            commit=True,
        )

        asset_root = cls.coa.accountmodel_set.get(code="01000000")
        liability_root = cls.coa.accountmodel_set.get(code="02000000")
        expense_root = cls.coa.accountmodel_set.get(code="05000000")
        income_root = cls.coa.accountmodel_set.get(code="04000000")

        # Moroccan Accounts
        cls.acc_5141 = asset_root.add_child(
            coa_model=cls.coa,
            code="5141",
            name="Banque Attijariwafa Cash",
            role=ASSET_CA_CASH,
            balance_type=DEBIT,
            active=True,
        )
        cls.acc_3421 = asset_root.add_child(
            coa_model=cls.coa,
            code="3421",
            name="Clients",
            role=ASSET_CA_RECEIVABLES,
            balance_type=DEBIT,
            active=True,
        )
        cls.acc_3491 = asset_root.add_child(
            coa_model=cls.coa,
            code="3491",
            name="Fournisseurs - Avances",
            role=ASSET_CA_PREPAID,
            balance_type=DEBIT,
            active=True,
        )
        cls.acc_4411 = liability_root.add_child(
            coa_model=cls.coa,
            code="4411",
            name="Fournisseurs",
            role=LIABILITY_CL_ACC_PAYABLE,
            balance_type=CREDIT,
            active=True,
        )
        cls.acc_4456 = liability_root.add_child(
            coa_model=cls.coa,
            code="4456",
            name="État, TVA due",
            role=LIABILITY_CL_TAXES_PAYABLE,
            balance_type=CREDIT,
            active=True,
        )
        cls.acc_4491 = liability_root.add_child(
            coa_model=cls.coa,
            code="4491",
            name="Clients - Avances et acomptes reçus",
            role=LIABILITY_CL_DEFERRED_REVENUE,
            balance_type=CREDIT,
            active=True,
        )
        cls.acc_6125 = expense_root.add_child(
            coa_model=cls.coa,
            code="6125",
            name="Achats de fournitures",
            role=EXPENSE_OPERATIONAL,
            balance_type=DEBIT,
            active=True,
        )
        cls.acc_7111 = income_root.add_child(
            coa_model=cls.coa,
            code="7111",
            name="Ventes de marchandises",
            role=INCOME_OPERATIONAL,
            balance_type=CREDIT,
            active=True,
        )

        cls.ledger, _ = LedgerModel.objects.get_or_create(
            entity=cls.entity,
            name="Atlas General Ledger",
            defaults={"posted": True},
        )

        # Dedicated Bank Account
        cls.bank_account = BankAccountModel.objects.create(
            name="Attijariwafa Principal MAD",
            entity_model=cls.entity,
            account_model=cls.acc_5141,
            account_number="007780000123456789012345",
            routing_number="007780",
            metadata={
                "bank_name": "Attijariwafa Bank",
                "iban": "MA64007780000123456789012345",
                "account_number": "007780000123456789012345",
            },
            active=True,
        )

        # Supporting entities for Case 1 (Approved Invoice)
        cls.customer = CustomerModel.objects.create(
            entity_model=cls.entity,
            customer_name="Atlas Client SARL",
            customer_number="CLI-ATLAS-01",
        )
        cls.uom = UnitOfMeasureModel.objects.create(
            entity=cls.entity,
            name="Unit",
            unit_abbr="U",
        )
        cls.service_item = ItemModel.objects.create(
            entity=cls.entity,
            name="Prestation de Conseil",
            sku="SRV-CONSULTING-MAD",
            uom=cls.uom,
            item_role=ItemModel.ITEM_ROLE_SERVICE,
            item_type=ItemModel.ITEM_TYPE_LABOR,
            earnings_account=cls.acc_7111,
            cogs_account=cls.acc_6125,
            for_inventory=False,
            is_product_or_service=True,
        )

        # 2. Case 1 Setup: Approved Customer Invoice (10,000 MAD)
        inv = InvoiceModel(
            cash_account=cls.acc_5141,
            prepaid_account=cls.acc_3421,
            unearned_account=cls.acc_4491,
            accrue=False,
        )
        inv.invoice_number = "INV-SYNTH-2026-001"
        _, inv = inv.configure(entity_slug=cls.entity, user_model=cls.user, commit_ledger=True)
        inv.date_draft = date(2026, 4, 5)
        inv.customer = cls.customer
        inv.save()

        itm = ItemTransactionModel(
            invoice_model=inv,
            item_model=cls.service_item,
            quantity=1.0,
            unit_cost=10000.0,
            total_amount=Decimal("10000.00"),
        )
        itm.full_clean()
        itm.save()

        inv.update_amount_due()
        inv.invoice_status = InvoiceModel.INVOICE_STATUS_APPROVED
        inv.date_approved = date(2026, 4, 5)
        inv.clean()
        inv.save()

        # 3. Case 3 Setup: Pre-posted cash GL journal entry (2,500 MAD)
        je = JournalEntryModel.objects.create(
            ledger=cls.ledger,
            je_number="FIT-SYNTH-CASE3-CASHGL",
            description="Encaissement direct en agence bancaire - Vente comptoir",
            posted=False,
            timestamp=datetime(2026, 4, 12, 12, 0, 0, tzinfo=dt_timezone.utc),
        )
        TransactionModel.objects.create(
            journal_entry=je,
            account=cls.acc_5141,
            amount=Decimal("2500.00"),
            tx_type=DEBIT,
            reconciled=False,
            description="Encaissement direct agence",
        )
        TransactionModel.objects.create(
            journal_entry=je,
            account=cls.acc_7111,
            amount=Decimal("2500.00"),
            tx_type=CREDIT,
            reconciled=False,
            description="Ventes comptoir agence",
        )
        je.posted = True
        je.save(update_fields=["posted", "je_number"], verify=False)

    @patch("ledger.bookkeeping_state.intake.service.dispatch_bookkeeping_trigger")
    def test_e2e_email_intake_to_bookkeeping_lifecycle(self, mock_dispatch):
        """
        Executes the full chain:
        Postmark Ingestion -> OCR Result -> Accounting Intake -> Entity Trigger
        -> Coalesced Session Runner -> Stage 1, Stage 2, Residual Bank Postings
        -> State Validation -> Idempotent Rerun -> REPL Workbench Proof.
        """
        doc_id = str(uuid4())
        session_id = f"e2e-session-{uuid4().hex[:8]}"

        # Simulated Postmark Inbound Bank Statement with 4 controlled transactions
        statement_ocr = {
            "doc_type": "bank_statement",
            "data": {
                "bank_name": "Attijariwafa Bank",
                "account_number": "007780000123456789012345",
                "iban": "MA64007780000123456789012345",
                "period_start": "01/04/2026",
                "period_end": "30/04/2026",
                "transactions": [
                    # Case 1: Inflow matching approved customer invoice
                    {
                        "date": "05/04/2026",
                        "description": "VIREMENT CLIENT ATLAS SARL REF INV-SYNTH-2026-001",
                        "amount": 10000.00,
                        "type": "credit",
                        "reference": "FIT-SYNTH-CASE1-INVOICE",
                    },
                    # Case 3: Inflow matching pre-posted cash GL entry
                    {
                        "date": "12/04/2026",
                        "description": "DEPOT ESPECES COMPTOIR REF CASH-GL-2500",
                        "amount": 2500.00,
                        "type": "credit",
                        "reference": "FIT-SYNTH-CASE3-CASHGL",
                    },
                    # Case 4: Outflow residual payment to DGI (TVA)
                    {
                        "date": "18/04/2026",
                        "description": "TELEPAIEMENT DIRECTION GENERALE DES IMPOTS TVA DGI REF 202604",
                        "amount": 3400.00,
                        "type": "debit",
                        "reference": "FIT-SYNTH-CASE4-DGIVAT",
                    },
                    # Case 7: Outflow residual ambiguous payment (HOLD)
                    {
                        "date": "24/04/2026",
                        "description": "VIREMENT DIVERS NON IDENTIFIE X77821",
                        "amount": 7850.00,
                        "type": "debit",
                        "reference": "FIT-SYNTH-CASE7-HOLD",
                    },
                ],
            },
        }

        insert_mock_document(
            doc_id=doc_id,
            session_id=session_id,
            doc_type="BANK_STATEMENT",
            file_name="releve_avril_2026.pdf",
            raw_ocr_json=statement_ocr,
            metadata={"entity_id": str(self.entity.uuid)},
        )

        # ------------------------------------------------------------------
        # Step 1: Execute Accounting Intake within transaction commit wrapper
        # ------------------------------------------------------------------
        with self.captureOnCommitCallbacks(execute=True):
            intake_res = AccountingIntakeService.process_event(
                {"document_id": doc_id, "entity_id": str(self.entity.uuid)}
            )

        self.assertEqual(intake_res.status, "PROCESSED")
        self.assertEqual(intake_res.artifact_type, "IMPORT_JOB")

        import_job = ImportJobModel.objects.get(uuid=intake_res.artifact_id)
        self.assertEqual(import_job.bank_account_model, self.bank_account)

        staged_txs = StagedTransactionModel.objects.filter(import_job=import_job).order_by("date_posted")
        self.assertEqual(staged_txs.count(), 4)

        # Invariant: transaction_model remains NULL
        for stx in staged_txs:
            self.assertIsNone(stx.transaction_model)

        # Invariant: Entity trigger is created and marked pending
        trigger = BookkeepingEntityTrigger.objects.get(entity=self.entity)
        self.assertTrue(trigger.pending)
        self.assertEqual(trigger.last_trigger_source, "accounting_intake")
        self.assertFalse(trigger.running)
        mock_dispatch.assert_called_once()
        self.assertEqual(mock_dispatch.call_args[0][0]["entity_id"], str(self.entity.uuid))
        self.assertEqual(mock_dispatch.call_args[0][0]["source"], "accounting_intake")

        # ------------------------------------------------------------------
        # Step 2: Configure Production Application Service with Wire Transports
        # ------------------------------------------------------------------
        def mock_book_transport(subject: str, data: bytes, timeout: float) -> bytes:
            req = json.loads(data.decode("utf-8"))
            resp = {
                "schema_version": BOOK_CATEGORIZATION_SCHEMA_VERSION,
                "request_id": req["request_id"],
                "idempotency_key": req["idempotency_key"],
                "session_id": req["session_id"],
                "state_revision": req["state_revision"],
                "dag_id": BOOK_CATEGORIZATION_DAG_ID,
                "status": "SUCCESS",
                "outcomes": [],
            }
            return json.dumps(resp).encode("utf-8")

        def mock_bank_transport(subject: str, data: bytes, timeout: float) -> bytes:
            req = json.loads(data.decode("utf-8"))
            outcomes = []
            bank_items = req.get("bank_items") or req.get("items") or []
            for item in bank_items:
                desc = item.get("description", "")
                if "DGI" in desc or "TVA" in desc:
                    outcomes.append({
                        "bank_item_id": item["bank_item_id"],
                        "status": "CLASSIFIED",
                        "account_code": "4456",
                        "confidence": 0.99,
                        "rationale": "DGI TVA telepayment",
                    })
                elif "NON IDENTIFIE" in desc or "X77821" in desc:
                    outcomes.append({
                        "bank_item_id": item["bank_item_id"],
                        "status": "HOLD",
                        "hold_reason": "Ambiguous payee unidentified bank transfer",
                    })
                else:
                    outcomes.append({
                        "bank_item_id": item["bank_item_id"],
                        "status": "HOLD",
                        "hold_reason": "Unclassified residual movement",
                    })
            resp = {
                "schema_version": BANK_CATEGORIZE_SCHEMA_VERSION,
                "request_id": req["request_id"],
                "idempotency_key": req["idempotency_key"],
                "session_id": req["session_id"],
                "state_revision": req["state_revision"],
                "dag_id": BANK_CATEGORIZATION_DAG_ID,
                "status": "COMPLETED",
                "outcomes": outcomes,
            }
            return json.dumps(resp).encode("utf-8")

        app_service = create_production_bookkeeping_application_service(
            dag_classifier=NatsAseBookCategorizer(transport=mock_book_transport),
            residual_bank_categorizer=NatsAseBankCategorizer(transport=mock_bank_transport),
        )

        # ------------------------------------------------------------------
        # Step 3: Run Coalesced Session Runner
        # ------------------------------------------------------------------
        results = process_entity_triggers(
            str(self.entity.uuid),
            app_service=app_service,
            session_id_override=f"session-{uuid4().hex[:8]}",
        )

        self.assertEqual(len(results), 1)
        res = results[0]
        self.assertTrue(res.is_success, f"Session failed at {res.failure_stage}: {res.failure_reason}")

        trigger.refresh_from_db()
        self.assertFalse(trigger.pending)
        self.assertFalse(trigger.running)
        self.assertIsNotNone(trigger.last_run_at)
        self.assertEqual(trigger.last_run_session_id, res.session_id)

        # ------------------------------------------------------------------
        # Step 4: Rehydrate Final State and Assert Authoritative Accounting Invariants
        # ------------------------------------------------------------------
        repo = BookkeepingRepository()
        hydrator = BookkeepingHydrator(repository=repo)
        final_state = hydrator.hydrate(company_id=str(self.entity.uuid), session_id=res.session_id)

        # Authoritative validator must report 0 violations
        val_report = validate_state(final_state)
        self.assertTrue(val_report.is_valid, f"Validation errors: {val_report.errors}")

        queries = BookkeepingQueries(final_state)

        c1_item = next(b for b in final_state.bank_items.values() if "INV-SYNTH-2026-001" in b.description)
        self.assertIsNotNone(c1_item)
        self.assertIn(str(c1_item.id), queries.executed_stage1_bank_item_ids())

        # --- Case 1: Stage 1 Payment Application on Invoice INV-SYNTH-2026-001 ---
        inv = InvoiceModel.objects.get(ledger__entity=self.entity, invoice_number="INV-SYNTH-2026-001")
        self.assertEqual(inv.amount_paid, Decimal("10000.00"))
        self.assertEqual(BookkeepingPaymentApplication.objects.filter(entity=self.entity).count(), 1)

        # --- Case 3: Stage 2 Direct Cash GL Reconciliation (2,500 MAD) ---
        c3_item = next(b for b in final_state.bank_items.values() if "CASH-GL-2500" in b.description)
        self.assertIsNotNone(c3_item)
        self.assertIn(str(c3_item.id), queries.executed_stage2_reconciliation_bank_item_ids())

        self.assertTrue(
            BookkeepingReconciliationBookAllocation.objects.filter(
                transaction__journal_entry__je_number="FIT-SYNTH-CASE3-CASHGL"
            ).exists()
        )

        # --- Case 4: Residual Bank Classification to 4456 (DGI TVA) + GL Posting ---
        dec_4 = [
            d for d in final_state.residual_bank_classifications.values()
            if d.account_code == "4456"
        ]
        self.assertEqual(len(dec_4), 1)
        self.assertEqual(dec_4[0].status.value, "CLASSIFIED")

        posting_4 = final_state.get_residual_bank_posting_for_decision(dec_4[0].id)
        self.assertIsNotNone(posting_4)
        je_4 = JournalEntryModel.objects.get(uuid=posting_4.journal_entry_id)
        self.assertTrue(je_4.posted)
        self.assertEqual(je_4.transactionmodel_set.count(), 2)

        dr_leg = je_4.transactionmodel_set.get(tx_type=DEBIT)
        self.assertEqual(dr_leg.account.code, "4456")
        self.assertEqual(dr_leg.amount, Decimal("3400.00"))

        cr_leg = je_4.transactionmodel_set.get(tx_type=CREDIT)
        self.assertEqual(cr_leg.account.code, "5141")
        self.assertEqual(cr_leg.amount, Decimal("3400.00"))

        # --- Case 7: Residual Bank Classification HOLD + 0 Postings ---
        dec_7 = [
            d for d in final_state.residual_bank_classifications.values()
            if d.status.value == "HOLD"
        ]
        self.assertEqual(len(dec_7), 1)
        self.assertEqual(dec_7[0].status.value, "HOLD")
        self.assertIn("Ambiguous", dec_7[0].hold_reason or "")

        posting_7 = final_state.get_residual_bank_posting_for_decision(dec_7[0].id)
        self.assertIsNone(posting_7, "HOLD decision must NEVER produce a residual posting!")

        # ------------------------------------------------------------------
        # Step 5: Verify Idempotent Rerun (Zero Duplicate Postings)
        # ------------------------------------------------------------------
        trigger.pending = True
        trigger.save(update_fields=["pending"])

        rerun_results = process_entity_triggers(
            str(self.entity.uuid),
            app_service=app_service,
            session_id_override=f"session-rerun-{uuid4().hex[:8]}",
        )
        self.assertEqual(len(rerun_results), 1)
        self.assertTrue(rerun_results[0].is_success)

        # Verify no duplicate journal entries or decisions were posted
        self.assertEqual(
            BookkeepingResidualBankClassificationDecision.objects.filter(entity=self.entity).count(),
            2,
        )
        self.assertEqual(
            BookkeepingResidualBankPosting.objects.filter(entity=self.entity).count(),
            1,
        )
        rehydrated_state = hydrator.hydrate(company_id=str(self.entity.uuid), session_id=rerun_results[0].session_id)
        self.assertTrue(validate_state(rehydrated_state).is_valid)

        # ------------------------------------------------------------------
        # Step 6: Accountant REPL Loader Verification (BookkeepingWorkbench)
        # ------------------------------------------------------------------
        workbench = BookkeepingWorkbench(
            repository=repo,
            company_id=str(self.entity.uuid),
        )
        self.assertIsNotNone(workbench.state)
        self.assertFalse(workbench.state.is_closed)
        self.assertEqual(len(workbench.state.bank_items), 4)
        self.assertEqual(workbench.company_id, str(self.entity.uuid))
