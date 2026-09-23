"""
Unit and integration tests for Go OCR Result -> Django Accounting Intake Bridge.
Verifies bank statements, supplier invoices, receipts, unsupported docs, idempotency, and BookkeepingState hydration.
"""

from datetime import date, datetime
from decimal import Decimal
import json
from uuid import uuid4

from django.db import connection, transaction
from django.test import TestCase

from unittest.mock import MagicMock, patch

from ledger.bookkeeping_state.domain.enums import Direction, BookkeepingRole
from ledger.bookkeeping_state.intake.service import (
    AccountingIntakeService,
    derive_staged_fit_id,
    dispatch_bookkeeping_trigger,
    should_trigger_bookkeeping,
)
from ledger.bookkeeping_state.persistence.reader import read_django_snapshot
from ledger.bookkeeping_state.session.runner import process_entity_triggers
from ledger.io.roles import (
    ASSET_CA_CASH,
    ASSET_CA_PREPAID,
    DEBIT,
    CREDIT,
    LIABILITY_CL_ACC_PAYABLE,
    LIABILITY_CL_DEFERRED_REVENUE,
)
from ledger.models.bank_account import BankAccountModel
from ledger.models.bill import BillModel
from ledger.models.bookkeeping import BookkeepingDocumentIntake, BookkeepingEntityTrigger
from ledger.models.data_import import ImportJobModel, StagedTransactionModel
from ledger.models.entity import EntityModel
from ledger.models.vendor import VendorModel


def ensure_toro_core_documents_table():
    with connection.cursor() as cursor:
        cursor.execute("""
            CREATE SCHEMA IF NOT EXISTS toro_core;
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
    ocr_status: str = "OCR_SUCCESS",
):
    ensure_toro_core_documents_table()
    with connection.cursor() as cursor:
        cursor.execute(
            """
            INSERT INTO toro_core.documents (
                id, session_id, document_type, file_name, mime_type, s3_url, sha256,
                ocr_status, raw_ocr_json, metadata, source_channel
            ) VALUES (
                %s, %s, %s, %s, %s, %s, %s, %s, %s, %s, %s
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
                ocr_status,
                json.dumps(raw_ocr_json),
                json.dumps(metadata),
                "EMAIL",
            ],
        )


class AccountingIntakeBridgeTests(TestCase):
    @classmethod
    def setUpTestData(cls):
        from django.contrib.auth import get_user_model

        ensure_toro_core_documents_table()

        cls.user = get_user_model().objects.create_user(
            username=f"accountant_{uuid4().hex[:8]}",
            email=f"accountant_{uuid4().hex[:8]}@example.com",
            password="securepassword123",
        )
        cls.entity = EntityModel.add_root(
            name="Atlas Trading SARL",
            admin=cls.user,
            currency="MAD",
            fy_start_month=1,
            accrual_method=False,
        )
        cls.coa = cls.entity.create_chart_of_accounts(
            coa_name="Atlas Primary CoA",
            assign_as_default=True,
            commit=True,
        )

        asset_root = cls.coa.accountmodel_set.get(code="01000000")
        cls.cash_account = asset_root.add_child(
            coa_model=cls.coa,
            code="5141",
            name="Banque Attijariwafa Cash",
            role=ASSET_CA_CASH,
            balance_type=DEBIT,
            active=True,
        )
        cls.prepaid_account = asset_root.add_child(
            coa_model=cls.coa,
            code="3491",
            name="Fournisseurs - Avances",
            role=ASSET_CA_PREPAID,
            balance_type=DEBIT,
            active=True,
        )

        liability_root = cls.coa.accountmodel_set.get(code="02000000")
        cls.ap_account = liability_root.add_child(
            coa_model=cls.coa,
            code="4411",
            name="Fournisseurs - Dettes",
            role=LIABILITY_CL_ACC_PAYABLE,
            balance_type=CREDIT,
            active=True,
        )
        cls.unearned_account = liability_root.add_child(
            coa_model=cls.coa,
            code="4421",
            name="Clients - Avances",
            role=LIABILITY_CL_DEFERRED_REVENUE,
            balance_type=CREDIT,
            active=True,
        )

        cls.bank_account = BankAccountModel.objects.create(
            name="Attijariwafa Checking MAD",
            entity_model=cls.entity,
            account_model=cls.cash_account,
            account_number="007810000012345678901234",
            metadata={
                "iban": "MA64007810000012345678901234",
                "rib": "007810000012345678901234",
                "bank_name": "ATTIJARIWAFA BANK",
            },
            active=True,
        )

        cls.vendor = VendorModel.objects.create(
            entity_model=cls.entity,
            vendor_name="Soc. Immobiliere Anfa",
            description="Office landlord",
        )

    def test_bank_statement_conversion_and_idempotency(self):
        doc_id = str(uuid4())
        session_id = str(uuid4())

        statement_ocr = {
            "doc_type": "bank_statement",
            "confidence": 0.99,
            "data": {
                "bank_name": "ATTIJARIWAFA BANK",
                "account_number": "007810000012345678901234",
                "rib": "007810000012345678901234",
                "iban": "MA64007810000012345678901234",
                "currency": "MAD",
                "statement_date": "31/07/2026",
                "transactions": {
                    "1": {
                        "date": "02/07/2026",
                        "value_date": "03/07/2026",
                        "description": "Loyer Juil. - Soc. Immobiliere Anfa",
                        "reference": "VIR-2026-07-001",
                        "amount": 12000,
                        "type": "debit",
                    },
                    "2": {
                        "date": "05/07/2026",
                        "description": "Virement Client ABC Construction",
                        "amount": 25000,
                        "type": "credit",
                    },
                    "3": {
                        "date": "08/07/2026",
                        "description": "Paiement CB Station Afriquia Oasis",
                        "amount": 850.50,
                        "type": "debit",
                    },
                },
            },
        }

        insert_mock_document(
            doc_id=doc_id,
            session_id=session_id,
            doc_type="BANK_STATEMENT",
            file_name="statement_july_2026.pdf",
            raw_ocr_json=statement_ocr,
            metadata={"entity_id": str(self.entity.uuid), "external_id": "msg-statement-001"},
        )

        event = {
            "document_id": doc_id,
            "entity_id": str(self.entity.uuid),
            "session_id": session_id,
            "source_message_id": "msg-statement-001",
            "file_name": "statement_july_2026.pdf",
            "status": "OCR_SUCCESS",
        }

        # First run: Successful conversion
        result = AccountingIntakeService.process_event(event)
        self.assertEqual(result.status, "PROCESSED")
        self.assertEqual(result.artifact_type, "IMPORT_JOB")
        self.assertEqual(result.row_count, 3)

        # Verify ImportJobModel
        import_job = ImportJobModel.objects.get(uuid=result.artifact_id)
        self.assertEqual(import_job.bank_account_model, self.bank_account)
        self.assertFalse(import_job.completed)

        # Verify StagedTransactionModel rows and invariants
        staged_rows = list(StagedTransactionModel.objects.filter(import_job=import_job).order_by("date_posted"))
        self.assertEqual(len(staged_rows), 3)

        for stx in staged_rows:
            self.assertIsNone(stx.parent, "parent must be null/top-level")
            self.assertIsNone(stx.transaction_model, "transaction_model must remain NULL")
            self.assertTrue(stx.fit_id.startswith("ocr:"))

        # Verify signed amounts: debit is negative, credit is positive
        loyer_row = [r for r in staged_rows if "Loyer" in r.name][0]
        self.assertEqual(loyer_row.amount, Decimal("-12000.00"))
        self.assertEqual(loyer_row.date_posted, date(2026, 7, 2))

        client_row = [r for r in staged_rows if "Virement" in r.name][0]
        self.assertEqual(client_row.amount, Decimal("25000.00"))
        self.assertEqual(client_row.date_posted, date(2026, 7, 5))

        cb_row = [r for r in staged_rows if "Afriquia" in r.name][0]
        self.assertEqual(cb_row.amount, Decimal("-850.50"))
        self.assertEqual(cb_row.date_posted, date(2026, 7, 8))

        # Replay duplicate: must be NOOP
        replay_result = AccountingIntakeService.process_event(event)
        self.assertEqual(replay_result.status, "NOOP")
        self.assertEqual(replay_result.reason, "ALREADY_PROCESSED")
        self.assertEqual(ImportJobModel.objects.filter(bank_account_model=self.bank_account).count(), 1)
        self.assertEqual(StagedTransactionModel.objects.filter(import_job=import_job).count(), 3)

    def test_bank_statement_legitimate_duplicate_rows_preserved(self):
        doc_id = str(uuid4())
        session_id = str(uuid4())

        statement_ocr = {
            "doc_type": "bank_statement",
            "data": {
                "iban": "MA64007810000012345678901234",
                "transactions": [
                    {
                        "date": "10/07/2026",
                        "description": "Retrait DAB Guichet Hassan II",
                        "amount": 1000,
                        "type": "debit",
                    },
                    {
                        "date": "10/07/2026",
                        "description": "Retrait DAB Guichet Hassan II",
                        "amount": 1000,
                        "type": "debit",
                    },
                ],
            },
        }

        insert_mock_document(
            doc_id=doc_id,
            session_id=session_id,
            doc_type="BANK_STATEMENT",
            file_name="statement_dups.pdf",
            raw_ocr_json=statement_ocr,
            metadata={"entity_id": str(self.entity.uuid)},
        )

        event = {"document_id": doc_id, "entity_id": str(self.entity.uuid)}
        result = AccountingIntakeService.process_event(event)
        self.assertEqual(result.status, "PROCESSED")
        self.assertEqual(result.row_count, 2)

        import_job = ImportJobModel.objects.get(uuid=result.artifact_id)
        rows = list(StagedTransactionModel.objects.filter(import_job=import_job))
        self.assertEqual(len(rows), 2)
        self.assertNotEqual(rows[0].fit_id, rows[1].fit_id, "Duplicate economic rows must have distinct fit_id via ordinal")

    def test_bank_account_resolution_unresolved_fails_closed(self):
        doc_id = str(uuid4())
        session_id = str(uuid4())

        statement_ocr = {
            "doc_type": "bank_statement",
            "data": {
                "iban": "FR7630006000011234567890189",  # Unknown IBAN
                "account_number": "9999999999999",
                "transactions": [{"date": "01/07/2026", "amount": 500, "type": "credit"}],
            },
        }

        insert_mock_document(
            doc_id=doc_id,
            session_id=session_id,
            doc_type="BANK_STATEMENT",
            file_name="unknown_bank.pdf",
            raw_ocr_json=statement_ocr,
            metadata={"entity_id": str(self.entity.uuid)},
        )

        event = {"document_id": doc_id, "entity_id": str(self.entity.uuid)}
        result = AccountingIntakeService.process_event(event)
        self.assertEqual(result.status, "NEEDS_REVIEW")
        self.assertEqual(result.error_code, "UNRESOLVED_BANK_ACCOUNT")

        # Zero staged transactions created
        self.assertEqual(StagedTransactionModel.objects.filter(fit_id__contains=doc_id).count(), 0)

        # Intake record marked NEEDS_REVIEW
        intake = BookkeepingDocumentIntake.objects.get(source_document_id=doc_id)
        self.assertEqual(intake.status, BookkeepingDocumentIntake.STATUS_NEEDS_REVIEW)
        self.assertEqual(intake.error_code, "UNRESOLVED_BANK_ACCOUNT")

    def test_bank_statement_atomic_rollback_on_invalid_row(self):
        doc_id = str(uuid4())
        session_id = str(uuid4())

        statement_ocr = {
            "doc_type": "bank_statement",
            "data": {
                "iban": "MA64007810000012345678901234",
                "transactions": [
                    {"date": "01/07/2026", "amount": 500, "type": "credit", "description": "Good row"},
                    {"date": "invalid-date", "amount": 600, "type": "debit", "description": "Bad row"},
                ],
            },
        }

        insert_mock_document(
            doc_id=doc_id,
            session_id=session_id,
            doc_type="BANK_STATEMENT",
            file_name="partial_fail.pdf",
            raw_ocr_json=statement_ocr,
            metadata={"entity_id": str(self.entity.uuid)},
        )

        event = {"document_id": doc_id, "entity_id": str(self.entity.uuid)}
        result = AccountingIntakeService.process_event(event)
        self.assertEqual(result.status, "NEEDS_REVIEW")
        self.assertEqual(result.error_code, "INVALID_TRANSACTION_DATE")

        # No partial rows created
        self.assertEqual(StagedTransactionModel.objects.filter(name="Good row").count(), 0)
        self.assertEqual(ImportJobModel.objects.filter(description__contains=doc_id).count(), 0)

    def test_supplier_invoice_creates_draft_bill(self):
        doc_id = str(uuid4())
        session_id = str(uuid4())

        invoice_ocr = {
            "doc_type": "invoice",
            "confidence": 0.98,
            "data": {
                "vendor_name": "Soc. Immobiliere Anfa",
                "invoice_number": "FA-2026-0789",
                "date": "15/07/2026",
                "total_mad": 14400.00,
                "ht_mad": 12000.00,
                "tva_mad": 2400.00,
            },
        }

        insert_mock_document(
            doc_id=doc_id,
            session_id=session_id,
            doc_type="INVOICE",
            file_name="supplier_invoice.pdf",
            raw_ocr_json=invoice_ocr,
            metadata={"entity_id": str(self.entity.uuid)},
        )

        event = {"document_id": doc_id, "entity_id": str(self.entity.uuid)}
        result = AccountingIntakeService.process_event(event)
        self.assertEqual(result.status, "PROCESSED")
        self.assertEqual(result.artifact_type, "BILL")

        bill = BillModel.objects.get(uuid=result.artifact_id)
        self.assertEqual(bill.bill_status, BillModel.BILL_STATUS_DRAFT, "Bill must be in DRAFT status only")
        self.assertEqual(bill.amount_due, Decimal("14400.00"))
        self.assertEqual(bill.vendor, self.vendor)
        self.assertEqual(bill.xref, "FA-2026-0789")
        self.assertEqual(bill.date_draft, date(2026, 7, 15))
        self.assertEqual(bill.cash_account, self.cash_account)
        self.assertEqual(bill.prepaid_account, self.prepaid_account)
        self.assertEqual(bill.unearned_account, self.ap_account)

    def test_supplier_invoice_unresolved_vendor_fails_closed(self):
        doc_id = str(uuid4())
        session_id = str(uuid4())

        invoice_ocr = {
            "doc_type": "invoice",
            "data": {
                "vendor_name": "Unknown Moroccan Supplier SARL",
                "invoice_number": "INV-9999",
                "date": "15/07/2026",
                "total_mad": 5000,
            },
        }

        insert_mock_document(
            doc_id=doc_id,
            session_id=session_id,
            doc_type="INVOICE",
            file_name="unknown_vendor.pdf",
            raw_ocr_json=invoice_ocr,
            metadata={"entity_id": str(self.entity.uuid)},
        )

        event = {"document_id": doc_id, "entity_id": str(self.entity.uuid)}
        result = AccountingIntakeService.process_event(event)
        self.assertEqual(result.status, "NEEDS_REVIEW")
        self.assertEqual(result.error_code, "UNRESOLVED_VENDOR")

        # Zero bills created
        self.assertEqual(BillModel.objects.filter(xref="INV-9999").count(), 0)

    def test_receipt_intake_requires_linking(self):
        doc_id = str(uuid4())
        session_id = str(uuid4())

        receipt_ocr = {
            "doc_type": "receipt",
            "confidence": 0.95,
            "data": {
                "vendor_name": "Station Afriquia Oasis",
                "date": "08/07/2026",
                "total_mad": 850.50,
                "payment_method": "Carte Bancaire",
            },
        }

        insert_mock_document(
            doc_id=doc_id,
            session_id=session_id,
            doc_type="RECEIPT",
            file_name="fuel_receipt.pdf",
            raw_ocr_json=receipt_ocr,
            metadata={"entity_id": str(self.entity.uuid)},
        )

        event = {"document_id": doc_id, "entity_id": str(self.entity.uuid)}
        result = AccountingIntakeService.process_event(event)
        self.assertEqual(result.status, "NEEDS_REVIEW")
        self.assertEqual(result.error_code, "RECEIPT_REQUIRES_LINKING")

        intake = BookkeepingDocumentIntake.objects.get(source_document_id=doc_id)
        self.assertEqual(intake.status, BookkeepingDocumentIntake.STATUS_NEEDS_REVIEW)
        self.assertEqual(intake.accounting_artifact_type, "SUPPORTING_DOCUMENT")
        self.assertEqual(intake.accounting_artifact_id, doc_id)

    def test_unsupported_doc_type_fails_closed(self):
        doc_id = str(uuid4())
        session_id = str(uuid4())

        insert_mock_document(
            doc_id=doc_id,
            session_id=session_id,
            doc_type="OTHER",
            file_name="contract_agreement.pdf",
            raw_ocr_json={"doc_type": "other", "data": {"title": "Partnership Agreement"}},
            metadata={"entity_id": str(self.entity.uuid)},
        )

        event = {"document_id": doc_id, "entity_id": str(self.entity.uuid)}
        result = AccountingIntakeService.process_event(event)
        self.assertEqual(result.status, "NEEDS_REVIEW")
        self.assertEqual(result.error_code, "UNSUPPORTED_DOCUMENT_TYPE")

    def test_bookkeeping_state_hydration_proof(self):
        doc_id = str(uuid4())
        session_id = str(uuid4())

        # 1. Ingest Bank Statement
        statement_ocr = {
            "doc_type": "bank_statement",
            "data": {
                "iban": "MA64007810000012345678901234",
                "transactions": [
                    {
                        "date": "02/07/2026",
                        "description": "Loyer Juil. Anfa",
                        "amount": 12000,
                        "type": "debit",
                    },
                    {
                        "date": "05/07/2026",
                        "description": "Paiement Client",
                        "amount": 25000,
                        "type": "credit",
                    },
                ],
            },
        }

        insert_mock_document(
            doc_id=doc_id,
            session_id=session_id,
            doc_type="BANK_STATEMENT",
            file_name="hydration_test_statement.pdf",
            raw_ocr_json=statement_ocr,
            metadata={"entity_id": str(self.entity.uuid)},
        )

        res = AccountingIntakeService.process_event({"document_id": doc_id, "entity_id": str(self.entity.uuid)})
        self.assertEqual(res.status, "PROCESSED")

        # 2. Ingest Supplier Invoice (creates DRAFT bill)
        inv_doc_id = str(uuid4())
        invoice_ocr = {
            "doc_type": "invoice",
            "data": {
                "vendor_name": "Soc. Immobiliere Anfa",
                "invoice_number": "BILL-STAGE1-PROOF",
                "date": "01/07/2026",
                "total_mad": 12000,
            },
        }
        insert_mock_document(
            doc_id=inv_doc_id,
            session_id=session_id,
            doc_type="INVOICE",
            file_name="bill_proof.pdf",
            raw_ocr_json=invoice_ocr,
            metadata={"entity_id": str(self.entity.uuid)},
        )
        res_inv = AccountingIntakeService.process_event({"document_id": inv_doc_id, "entity_id": str(self.entity.uuid)})
        self.assertEqual(res_inv.status, "PROCESSED")
        draft_bill_id = res_inv.artifact_id

        # 3. Prove Hydration: Bank statement appears as BankItems
        snapshot = read_django_snapshot(company_id=str(self.entity.uuid))
        bank_items = snapshot.bank_items
        self.assertEqual(len(bank_items), 2)

        loyer_item = [b for b in bank_items if "Loyer" in b.description][0]
        self.assertEqual(loyer_item.direction, Direction.BANK_OUTFLOW)
        self.assertEqual(loyer_item.amount_units, "120000000")  # 12000 * 10,000

        client_item = [b for b in bank_items if "Client" in b.description][0]
        self.assertEqual(client_item.direction, Direction.BANK_INFLOW)
        self.assertEqual(client_item.amount_units, "250000000")  # 25000 * 10,000

        # 4. Prove Hydration: DRAFT Bill is NOT visible in BookItems
        book_items = snapshot.book_items
        self.assertFalse(any("BILL-STAGE1-PROOF" in b.description or (b.reference == "BILL-STAGE1-PROOF") for b in book_items))

        # 5. Canonical Test Approval: Approve bill and rehydrate -> now visible as OPEN_PAYABLE!
        bill = BillModel.objects.get(uuid=draft_bill_id)
        bill.bill_status = BillModel.BILL_STATUS_APPROVED
        bill.date_approved = date(2026, 7, 1)
        bill.save(update_fields=["bill_status", "date_approved"])

        snapshot_approved = read_django_snapshot(company_id=str(self.entity.uuid))
        approved_book_items = snapshot_approved.book_items
        self.assertTrue(any(b.id == f"bill:{draft_bill_id}" for b in approved_book_items))
        approved_bill_item = [b for b in approved_book_items if b.id == f"bill:{draft_bill_id}"][0]
        self.assertEqual(approved_bill_item.bookkeeping_role, BookkeepingRole.OPEN_PAYABLE)
        self.assertEqual(approved_bill_item.amount_units, "120000000")

    @patch("ledger.bookkeeping_state.intake.service.dispatch_bookkeeping_trigger")
    def test_9a_processed_bank_statement_emits_trigger_after_commit(self, mock_dispatch):
        doc_id = str(uuid4())
        session_id = str(uuid4())
        statement_ocr = {
            "doc_type": "bank_statement",
            "data": {
                "iban": "MA64007810000012345678901234",
                "transactions": [{"date": "01/07/2026", "amount": 1000, "type": "credit", "description": "Deposit"}],
            },
        }
        insert_mock_document(
            doc_id=doc_id,
            session_id=session_id,
            doc_type="BANK_STATEMENT",
            file_name="statement_9a.pdf",
            raw_ocr_json=statement_ocr,
            metadata={"entity_id": str(self.entity.uuid)},
        )
        with self.captureOnCommitCallbacks(execute=True):
            res = AccountingIntakeService.process_event({"document_id": doc_id, "entity_id": str(self.entity.uuid)})
        self.assertEqual(res.status, "PROCESSED")
        self.assertTrue(should_trigger_bookkeeping(res))

        trigger = BookkeepingEntityTrigger.objects.get(entity=self.entity)
        self.assertTrue(trigger.pending)
        self.assertEqual(trigger.last_trigger_source, "accounting_intake")
        mock_dispatch.assert_called_once()
        call_payload = mock_dispatch.call_args[0][0]
        self.assertEqual(call_payload["entity_id"], str(self.entity.uuid))
        self.assertEqual(call_payload["source"], "accounting_intake")

    @patch("ledger.bookkeeping_state.intake.service.dispatch_bookkeeping_trigger")
    def test_9b_needs_review_emits_none(self, mock_dispatch):
        doc_id = str(uuid4())
        insert_mock_document(
            doc_id=doc_id,
            session_id=str(uuid4()),
            doc_type="RECEIPT",
            file_name="receipt_9b.pdf",
            raw_ocr_json={"doc_type": "receipt", "data": {"total": 100}},
            metadata={"entity_id": str(self.entity.uuid)},
        )
        res = AccountingIntakeService.process_event({"document_id": doc_id, "entity_id": str(self.entity.uuid)})
        self.assertEqual(res.status, "NEEDS_REVIEW")
        self.assertFalse(should_trigger_bookkeeping(res))
        mock_dispatch.assert_not_called()

    @patch("ledger.bookkeeping_state.intake.service.dispatch_bookkeeping_trigger")
    def test_9c_draft_bill_emits_none(self, mock_dispatch):
        doc_id = str(uuid4())
        invoice_ocr = {
            "doc_type": "invoice",
            "data": {
                "vendor_name": "Soc. Immobiliere Anfa",
                "invoice_number": "FA-9C",
                "date": "15/07/2026",
                "total_mad": 5000,
            },
        }
        insert_mock_document(
            doc_id=doc_id,
            session_id=str(uuid4()),
            doc_type="INVOICE",
            file_name="inv_9c.pdf",
            raw_ocr_json=invoice_ocr,
            metadata={"entity_id": str(self.entity.uuid)},
        )
        res = AccountingIntakeService.process_event({"document_id": doc_id, "entity_id": str(self.entity.uuid)})
        self.assertEqual(res.status, "PROCESSED")
        self.assertEqual(res.artifact_type, "BILL")
        self.assertFalse(should_trigger_bookkeeping(res))
        mock_dispatch.assert_not_called()

    @patch("ledger.bookkeeping_state.intake.service.dispatch_bookkeeping_trigger")
    def test_9d_duplicate_intake_noop_emits_none(self, mock_dispatch):
        doc_id = str(uuid4())
        statement_ocr = {
            "doc_type": "bank_statement",
            "data": {
                "iban": "MA64007810000012345678901234",
                "transactions": [{"date": "01/07/2026", "amount": 100, "type": "credit"}],
            },
        }
        insert_mock_document(
            doc_id=doc_id,
            session_id=str(uuid4()),
            doc_type="BANK_STATEMENT",
            file_name="stmt_9d.pdf",
            raw_ocr_json=statement_ocr,
            metadata={"entity_id": str(self.entity.uuid)},
        )
        res1 = AccountingIntakeService.process_event({"document_id": doc_id, "entity_id": str(self.entity.uuid)})
        self.assertEqual(res1.status, "PROCESSED")
        mock_dispatch.reset_mock()

        # Duplicate event
        res2 = AccountingIntakeService.process_event({"document_id": doc_id, "entity_id": str(self.entity.uuid)})
        self.assertEqual(res2.status, "NOOP")
        self.assertFalse(should_trigger_bookkeeping(res2))
        mock_dispatch.assert_not_called()

    def test_9e_duplicate_trigger_safe_and_idempotent(self):
        BookkeepingEntityTrigger.record_trigger(self.entity, "trig-1", "test")
        BookkeepingEntityTrigger.record_trigger(self.entity, "trig-1", "test")

        mock_service = MagicMock()
        mock_service.run_session.return_value = MagicMock(is_success=True)

        results = process_entity_triggers(str(self.entity.uuid), app_service=mock_service)
        self.assertEqual(len(results), 1)
        mock_service.run_session.assert_called_once()

        # Second run with no new triggers is clean NOOP
        results2 = process_entity_triggers(str(self.entity.uuid), app_service=mock_service)
        self.assertEqual(len(results2), 0)

    def test_9f_same_entity_burst_coalesced(self):
        # Simulate burst of 5 triggers recorded before worker execution
        for i in range(5):
            BookkeepingEntityTrigger.record_trigger(self.entity, f"burst-{i}", "test")

        mock_service = MagicMock()
        mock_service.run_session.return_value = MagicMock(is_success=True)

        results = process_entity_triggers(str(self.entity.uuid), app_service=mock_service)
        # 5 pending triggers coalesce into 1 run
        self.assertEqual(len(results), 1)
        self.assertEqual(mock_service.run_session.call_count, 1)

    def test_9g_different_entities_independent(self):
        from django.contrib.auth import get_user_model
        user2 = get_user_model().objects.create_user(
            username=f"accountant_{uuid4().hex[:8]}",
            email=f"accountant_{uuid4().hex[:8]}@example.com",
            password="securepassword123",
        )
        entity2 = EntityModel.add_root(
            name="Second Entity SARL",
            admin=user2,
            currency="MAD",
            fy_start_month=1,
            accrual_method=False,
        )

        BookkeepingEntityTrigger.record_trigger(self.entity, "t-e1", "test")
        BookkeepingEntityTrigger.record_trigger(entity2, "t-e2", "test")

        mock_service = MagicMock()
        mock_service.run_session.return_value = MagicMock(is_success=True)

        res1 = process_entity_triggers(str(self.entity.uuid), app_service=mock_service)
        self.assertEqual(len(res1), 1)
        self.assertEqual(mock_service.run_session.call_args[1]["company_id"], str(self.entity.uuid))

        # entity2 is independent and still pending
        trig2 = BookkeepingEntityTrigger.objects.get(entity=entity2)
        self.assertTrue(trig2.pending)

        res2 = process_entity_triggers(str(entity2.uuid), app_service=mock_service)
        self.assertEqual(len(res2), 1)
        self.assertEqual(mock_service.run_session.call_args[1]["company_id"], str(entity2.uuid))

    def test_9h_trigger_arriving_during_active_run_not_lost(self):
        BookkeepingEntityTrigger.record_trigger(self.entity, "initial-trig", "test")

        mock_service = MagicMock()
        call_count = 0

        def side_effect_run(company_id, session_id):
            nonlocal call_count
            call_count += 1
            if call_count == 1:
                # While first session is running, a new trigger arrives!
                BookkeepingEntityTrigger.record_trigger(self.entity, "interleaved-trig", "test")
            return MagicMock(is_success=True, session_id=session_id)

        mock_service.run_session.side_effect = side_effect_run

        results = process_entity_triggers(str(self.entity.uuid), app_service=mock_service)
        # Must execute subsequent run to process the interleaved trigger!
        self.assertEqual(len(results), 2)
        self.assertEqual(call_count, 2)
        trigger = BookkeepingEntityTrigger.objects.get(entity=self.entity)
        self.assertFalse(trigger.pending)
        self.assertFalse(trigger.running)

    def test_9i_session_failure_follows_error_convention(self):
        BookkeepingEntityTrigger.record_trigger(self.entity, "failing-trig", "test")

        mock_service = MagicMock()
        mock_service.run_session.side_effect = RuntimeError("Simulated operational failure")

        with self.assertRaises(RuntimeError):
            process_entity_triggers(str(self.entity.uuid), app_service=mock_service)

        # Running flag must be cleanly released in finally block
        trigger = BookkeepingEntityTrigger.objects.get(entity=self.entity)
        self.assertFalse(trigger.running)

    def test_9j_rolled_back_intake_emits_no_trigger(self):
        with patch("ledger.bookkeeping_state.intake.service.dispatch_bookkeeping_trigger") as mock_dispatch:
            with self.captureOnCommitCallbacks(execute=True):
                try:
                    with transaction.atomic():
                        # Register on_commit callback
                        transaction.on_commit(lambda: dispatch_bookkeeping_trigger({"test": "data"}))
                        raise ValueError("Simulated transaction failure")
                except ValueError:
                    pass
            mock_dispatch.assert_not_called()

    def test_9k_single_active_bank_account_no_identifier_fails_closed(self):
        # Entity has exactly 1 active bank account (self.bank_account)
        self.assertEqual(BankAccountModel.objects.filter(entity_model=self.entity, active=True).count(), 1)

        doc_id = str(uuid4())
        session_id = str(uuid4())
        # Statement has valid transactions but ZERO bank identifiers (no iban, no account_number, no rib)
        statement_ocr = {
            "doc_type": "bank_statement",
            "data": {
                "bank_name": "ATTIJARIWAFA BANK",
                "transactions": [{"date": "01/07/2026", "amount": 1000, "type": "credit", "description": "Mystery deposit"}],
            },
        }
        insert_mock_document(
            doc_id=doc_id,
            session_id=session_id,
            doc_type="BANK_STATEMENT",
            file_name="no_identifier.pdf",
            raw_ocr_json=statement_ocr,
            metadata={"entity_id": str(self.entity.uuid)},
        )
        res = AccountingIntakeService.process_event({"document_id": doc_id, "entity_id": str(self.entity.uuid)})
        self.assertEqual(res.status, "NEEDS_REVIEW")
        self.assertEqual(res.error_code, "UNRESOLVED_BANK_ACCOUNT")

        # Zero staged transactions created
        self.assertEqual(StagedTransactionModel.objects.filter(fit_id__contains=doc_id).count(), 0)
        self.assertEqual(ImportJobModel.objects.filter(description__contains=doc_id).count(), 0)

    @patch("ledger.bookkeeping_state.intake.service.dispatch_bookkeeping_trigger")
    def test_bill_approval_emits_trigger(self, mock_dispatch):
        doc_id = str(uuid4())
        invoice_ocr = {
            "doc_type": "invoice",
            "data": {
                "vendor_name": "Soc. Immobiliere Anfa",
                "invoice_number": "FA-APP-TEST",
                "date": "15/07/2026",
                "total_mad": 6000,
            },
        }
        insert_mock_document(
            doc_id=doc_id,
            session_id=str(uuid4()),
            doc_type="INVOICE",
            file_name="inv_app.pdf",
            raw_ocr_json=invoice_ocr,
            metadata={"entity_id": str(self.entity.uuid)},
        )
        res = AccountingIntakeService.process_event({"document_id": doc_id, "entity_id": str(self.entity.uuid)})
        self.assertEqual(res.status, "PROCESSED")
        bill_id = res.artifact_id
        bill = BillModel.objects.get(uuid=bill_id)
        self.assertEqual(bill.bill_status, BillModel.BILL_STATUS_DRAFT)
        mock_dispatch.assert_not_called()

        # Approve bill canonically (transition draft -> review -> approved)
        bill.bill_status = BillModel.BILL_STATUS_REVIEW
        bill.save()
        with self.captureOnCommitCallbacks(execute=True):
            bill.mark_as_approved(self.user, commit=True)

        trigger = BookkeepingEntityTrigger.objects.get(entity=self.entity)
        self.assertTrue(trigger.pending)
        self.assertEqual(trigger.last_trigger_source, "bill_approved")
        mock_dispatch.assert_called_once()
        payload = mock_dispatch.call_args[0][0]
        self.assertEqual(payload["entity_id"], str(self.entity.uuid))
        self.assertEqual(payload["source"], "bill_approved")


