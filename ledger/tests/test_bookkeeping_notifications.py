from __future__ import annotations

import uuid
from decimal import Decimal
from typing import Any, cast

from django.contrib.auth import get_user_model
from django.db import connection, transaction
from django.test import TestCase
from django.utils import timezone

from bookkeeping_state.notifications.service import (
    REVIEW_NOTIFICATION_ERROR_CODES,
    SAFE_ACTION_MAPPING,
    notify_document_needs_review,
    notify_residual_hold,
    resolve_canonical_recipient,
    set_postmark_sender_override,
)
from ledger.models.bank_account import BankAccountModel
from ledger.models.bookkeeping import (
    BookkeepingDocumentIntake,
    BookkeepingNotificationDelivery,
    BookkeepingResidualBankClassificationDecision,
)
from ledger.models.data_import import ImportJobModel, StagedTransactionModel
from ledger.models.entity import EntityModel
from ledger.models.ledger import LedgerModel

UserModel = get_user_model()


class BookkeepingNotificationTests(TestCase):
    @classmethod
    def setUpClass(cls):
        super().setUpClass()
        with connection.cursor() as cur:
            cur.execute("""
                CREATE SCHEMA IF NOT EXISTS toro_core;
                CREATE TABLE IF NOT EXISTS toro_core.entities (
                    id UUID PRIMARY KEY,
                    parent_id UUID,
                    name VARCHAR,
                    entity_type VARCHAR,
                    plan_tier VARCHAR,
                    status VARCHAR,
                    created_at TIMESTAMPTZ DEFAULT NOW(),
                    updated_at TIMESTAMPTZ DEFAULT NOW()
                );
                CREATE TABLE IF NOT EXISTS toro_core.users (
                    id UUID PRIMARY KEY,
                    entity_id UUID,
                    email VARCHAR UNIQUE,
                    password_hash VARCHAR,
                    full_name VARCHAR,
                    role VARCHAR,
                    is_active BOOLEAN DEFAULT TRUE,
                    created_at TIMESTAMPTZ DEFAULT NOW(),
                    updated_at TIMESTAMPTZ DEFAULT NOW()
                );
            """)

    def setUp(self):
        super().setUp()
        self.sent_emails: list[dict[str, Any]] = []

        def mock_sender(to: str, subject: str, text_body: str, reply_to: str | None = None) -> tuple[bool, str]:
            msg_id = f"pm-msg-{uuid.uuid4()}"
            self.sent_emails.append({
                "to": to,
                "subject": subject,
                "text_body": text_body,
                "reply_to": reply_to,
                "msg_id": msg_id,
            })
            return True, msg_id

        set_postmark_sender_override(mock_sender)

        self.admin_user = cast(Any, UserModel.objects).create_user(
            username=f"admin_{uuid.uuid4().hex[:8]}",
            email="admin@testentity.com",
            password="password",
        )
        self.entity = EntityModel.add_root(
            name="Test Notification Entity SARL",
            admin=self.admin_user,
            currency="MAD",
            fy_start_month=1,
            accrual_method=False,
        )
        self.coa = self.entity.create_chart_of_accounts(
            coa_name="Test Standard CoA",
            assign_as_default=True,
            commit=True,
        )

        self.ledger = LedgerModel.objects.create(
            name="Test General Ledger",
            entity=self.entity,
        )
        self.bank_account = BankAccountModel.objects.create(
            name="Attijariwafa Bank",
            entity_model=self.entity,
            account_number="007780000123456789012345",
            active=True,
            metadata={"iban": "MA64007780000123456789012345"},
        )
        self.import_job = ImportJobModel.objects.create(
            bank_account_model=self.bank_account,
            ledger_model=self.ledger,
            description="Test Import Job",
        )
        self.stx = StagedTransactionModel.objects.create(
            import_job=self.import_job,
            date_posted=timezone.now().date(),
            amount=Decimal("-7850.00"),
            name="Virement Inconnu Ref #998877",
            memo="Ref: 998877",
            fit_id=f"fit-{uuid.uuid4().hex[:8]}",
        )

    def tearDown(self):
        set_postmark_sender_override(None)
        super().tearDown()

    def _create_hold_decision(self, dec_id: str, **kwargs: Any) -> BookkeepingResidualBankClassificationDecision:
        defaults = {
            "entity": self.entity,
            "staged_transaction": self.stx,
            "bank_account": self.bank_account,
            "status": BookkeepingResidualBankClassificationDecision.StatusChoices.HOLD,
            "original_amount_units": 78500000,
            "residual_amount_units": 78500000,
            "direction": "OUTFLOW",
            "currency": "MAD",
            "confidence": 0.45,
            "rationale": "Unmatched payment requiring contract evidence",
            "hold_reason": "Contract or supplier invoice missing for bank movement",
            "required_evidence": ["supplier_invoice", "signed_contract"],
            "schema_version": "bookkeeping.ase.bank_categorize.v1",
            "dag_id": "bookkeeping_bank_categorization_v1",
            "request_semantic_digest": f"digest-{uuid.uuid4().hex[:8]}",
            "state_revision_at_decision": 1,
            "persistence_revision_at_decision": 1,
            "created_at": timezone.now(),
        }
        defaults.update(kwargs)
        return BookkeepingResidualBankClassificationDecision.objects.create(
            id=dec_id,
            **defaults,
        )

    def test_canonical_recipient_resolution(self):
        # 1. Fallback to admin if no toro_core.users
        recipient = resolve_canonical_recipient(self.entity)
        self.assertEqual(recipient, "admin@testentity.com")

        # 2. If toro_core.users has an owner for this entity, prefer it
        with connection.cursor() as cur:
            cur.execute(
                """
                INSERT INTO toro_core.entities (id, name, entity_type, status)
                VALUES (%s, %s, 'client', 'active')
                ON CONFLICT (id) DO NOTHING;
                """,
                [str(self.entity.uuid), self.entity.name],
            )
            cur.execute(
                """
                INSERT INTO toro_core.users (id, entity_id, email, password_hash, role, is_active)
                VALUES (%s, %s, %s, 'hash', 'owner', true)
                ON CONFLICT (email) DO UPDATE SET entity_id = EXCLUDED.entity_id, role = EXCLUDED.role;
                """,
                [str(uuid.uuid4()), str(self.entity.uuid), "owner@canonical.com"],
            )

        resolved_owner = resolve_canonical_recipient(self.entity)
        self.assertEqual(resolved_owner, "owner@canonical.com")

    def test_new_hold_sends_one_notification_after_commit(self):
        dec_id = f"dec-hold-{uuid.uuid4().hex[:8]}"
        with self.captureOnCommitCallbacks(execute=True):
            dec = self._create_hold_decision(dec_id)

        self.assertEqual(len(self.sent_emails), 1)
        sent = self.sent_emails[0]
        self.assertEqual(sent["to"], "admin@testentity.com")
        self.assertEqual(sent["subject"], "Toro needs information for a transaction")
        self.assertIn("7,850.00 MAD", sent["text_body"])
        self.assertIn("Contract or supplier invoice missing", sent["text_body"])
        self.assertIn("Virement Inconnu", sent["text_body"])
        self.assertEqual(sent["reply_to"], f"accounting@{self.entity.slug}.inbound.usetoro.io")

        # Verify durable delivery record
        delivery = BookkeepingNotificationDelivery.objects.filter(
            event_type=BookkeepingNotificationDelivery.EVENT_TYPE_RESIDUAL_HOLD,
            event_id=dec.id,
        ).first()
        self.assertIsNotNone(delivery)
        assert delivery is not None
        self.assertEqual(delivery.status, BookkeepingNotificationDelivery.STATUS_SENT)
        self.assertEqual(delivery.recipient, "admin@testentity.com")
        self.assertTrue(delivery.postmark_message_id.startswith("pm-msg-"))

    def test_rollback_emits_no_notification(self):
        dec_id = f"dec-rollback-{uuid.uuid4().hex[:8]}"
        try:
            with transaction.atomic():
                self._create_hold_decision(dec_id, hold_reason="Rollback test")
                raise RuntimeError("Intentional rollback")
        except RuntimeError:
            pass

        self.assertEqual(len(self.sent_emails), 0)
        self.assertEqual(
            BookkeepingNotificationDelivery.objects.filter(event_type="residual_hold", event_id=dec_id).count(),
            0,
        )

    def test_duplicate_replayed_hold_no_duplicate_notification(self):
        dec_id = f"dec-dup-{uuid.uuid4().hex[:8]}"
        with self.captureOnCommitCallbacks(execute=True):
            dec = self._create_hold_decision(dec_id, hold_reason="Duplicate test")

        self.assertEqual(len(self.sent_emails), 1)

        # Attempt to notify again for the same decision
        result = notify_residual_hold(dec.id)
        self.assertFalse(result)
        # Email count must still be exactly 1
        self.assertEqual(len(self.sent_emails), 1)
        self.assertEqual(
            BookkeepingNotificationDelivery.objects.filter(
                event_type=BookkeepingNotificationDelivery.EVENT_TYPE_RESIDUAL_HOLD,
                event_id=dec.id,
            ).count(),
            1,
        )

    def test_needs_review_intake_sends_one_notification_for_all_6_codes(self):
        for code in REVIEW_NOTIFICATION_ERROR_CODES:
            self.sent_emails.clear()
            doc_id = uuid.uuid4()
            with self.captureOnCommitCallbacks(execute=True):
                intake = BookkeepingDocumentIntake.objects.create(
                    entity=self.entity,
                    source_document_id=doc_id,
                    doc_type="bank_statement" if "BANK" in code else "invoice",
                    status=BookkeepingDocumentIntake.STATUS_NEEDS_REVIEW,
                    error_code=code,
                    error_detail=f"Verification required for {code}",
                    metadata={"file_name": f"{code.lower()}_sample.pdf"},
                )

            self.assertEqual(len(self.sent_emails), 1, f"Expected 1 email for code {code}")
            sent = self.sent_emails[0]
            self.assertEqual(sent["to"], "admin@testentity.com")
            self.assertEqual(sent["subject"], "Toro needs review for a document")
            self.assertIn(code, sent["text_body"])
            self.assertIn(SAFE_ACTION_MAPPING[code], sent["text_body"])
            self.assertIn(f"{code.lower()}_sample.pdf", sent["text_body"])

            delivery = BookkeepingNotificationDelivery.objects.filter(
                event_type=BookkeepingNotificationDelivery.EVENT_TYPE_DOCUMENT_NEEDS_REVIEW,
                event_id=str(intake.id),
            ).first()
            self.assertIsNotNone(delivery)
            assert delivery is not None
            self.assertEqual(delivery.status, BookkeepingNotificationDelivery.STATUS_SENT)

    def test_duplicate_intake_no_duplicate_notification(self):
        doc_id = uuid.uuid4()
        with self.captureOnCommitCallbacks(execute=True):
            intake = BookkeepingDocumentIntake.objects.create(
                entity=self.entity,
                source_document_id=doc_id,
                doc_type="receipt",
                status=BookkeepingDocumentIntake.STATUS_NEEDS_REVIEW,
                error_code="RECEIPT_REQUIRES_LINKING",
                error_detail="Receipt needs manual link",
                metadata={"file_name": "receipt_123.jpg"},
            )

        self.assertEqual(len(self.sent_emails), 1)

        result = notify_document_needs_review(str(intake.id))
        self.assertFalse(result)
        self.assertEqual(len(self.sent_emails), 1)
        self.assertEqual(
            BookkeepingNotificationDelivery.objects.filter(
                event_type=BookkeepingNotificationDelivery.EVENT_TYPE_DOCUMENT_NEEDS_REVIEW,
                event_id=str(intake.id),
            ).count(),
            1,
        )

    def test_postmark_failure_does_not_affect_accounting_state(self):
        def failing_sender(to: str, subject: str, text_body: str, reply_to: str | None = None) -> tuple[bool, str]:
            return False, "HTTP 500: Internal Server Error from Postmark"

        set_postmark_sender_override(failing_sender)

        dec_id = f"dec-fail-{uuid.uuid4().hex[:8]}"
        with self.captureOnCommitCallbacks(execute=True):
            dec = self._create_hold_decision(dec_id, hold_reason="Payment failure test")

        persisted_dec = BookkeepingResidualBankClassificationDecision.objects.filter(id=dec_id).first()
        self.assertIsNotNone(persisted_dec)
        assert persisted_dec is not None
        self.assertEqual(persisted_dec.status, "HOLD")

        delivery = BookkeepingNotificationDelivery.objects.filter(
            event_type=BookkeepingNotificationDelivery.EVENT_TYPE_RESIDUAL_HOLD,
            event_id=dec_id,
        ).first()
        self.assertIsNotNone(delivery)
        assert delivery is not None
        self.assertEqual(delivery.status, BookkeepingNotificationDelivery.STATUS_FAILED)
        self.assertIn("HTTP 500", delivery.error_message)

    def test_retry_eventually_sends_once(self):
        def failing_sender(to: str, subject: str, text_body: str, reply_to: str | None = None) -> tuple[bool, str]:
            return False, "HTTP 503: Service Unavailable"

        set_postmark_sender_override(failing_sender)

        dec_id = f"dec-retry-{uuid.uuid4().hex[:8]}"
        with self.captureOnCommitCallbacks(execute=True):
            self._create_hold_decision(dec_id, hold_reason="Service retry test")

        delivery = BookkeepingNotificationDelivery.objects.get(
            event_type=BookkeepingNotificationDelivery.EVENT_TYPE_RESIDUAL_HOLD,
            event_id=dec_id,
        )
        self.assertEqual(delivery.status, BookkeepingNotificationDelivery.STATUS_FAILED)

        def successful_sender(to: str, subject: str, text_body: str, reply_to: str | None = None) -> tuple[bool, str]:
            return True, "pm-msg-retry-success-123"

        set_postmark_sender_override(successful_sender)

        retry_result = notify_residual_hold(dec_id)
        self.assertTrue(retry_result)

        delivery.refresh_from_db()
        self.assertEqual(delivery.status, BookkeepingNotificationDelivery.STATUS_SENT)
        self.assertEqual(delivery.postmark_message_id, "pm-msg-retry-success-123")

        third_attempt = notify_residual_hold(dec_id)
        self.assertFalse(third_attempt)
