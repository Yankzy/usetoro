"""
Accounting intake service bridging Go perception OCR results into Django accounting authority.
"""

from __future__ import annotations

from dataclasses import dataclass
from datetime import date, datetime
from decimal import Decimal
import hashlib
import logging
import json
import os
from typing import Any
from uuid import UUID, uuid4

from django.db import transaction
from django.dispatch import receiver
from django.utils import timezone

from ledger.io import ASSET_CA_CASH, ASSET_CA_PREPAID, LIABILITY_CL_ACC_PAYABLE
from ledger.models.bank_account import BankAccountModel
from ledger.models.bill import BillModel
from ledger.models.bookkeeping import BookkeepingDocumentIntake, BookkeepingEntityTrigger
from ledger.models.data_import import ImportJobModel, StagedTransactionModel
from ledger.models.entity import EntityModel
from ledger.models.signals import bill_status_approved
from ledger.models.vendor import VendorModel
from ledger.models import AccountModel
from ledger.bookkeeping_state.intake.repository import DocumentSourceRecord, DocumentSourceRepository

logger = logging.getLogger(__name__)


@dataclass(frozen=True)
class IntakeResult:
    status: str
    artifact_type: str = ""
    artifact_id: str = ""
    error_code: str = ""
    error_detail: str = ""
    row_count: int = 0
    reason: str = ""


def derive_staged_fit_id(
    document_id: str,
    date_str: str,
    value_date_str: str | None,
    signed_amount: Decimal,
    reference: str | None,
    description: str,
    occurrence_ordinal: int,
) -> str:
    """
    Derives a deterministic, crash-consistent fit_id from document ID and statement line content.
    Incorporates occurrence_ordinal to handle legitimate identical economic rows within one statement.
    """
    norm_desc = " ".join((description or "").lower().split())
    norm_ref = (reference or "").strip().lower()
    raw = f"{document_id}:{date_str}:{value_date_str or ''}:{signed_amount:.2f}:{norm_ref}:{norm_desc}:{occurrence_ordinal}"
    digest = hashlib.sha256(raw.encode("utf-8")).hexdigest()[:24]
    return f"ocr:{digest}"


def parse_date(date_raw: Any) -> date | None:
    if not date_raw:
        return None
    if isinstance(date_raw, date):
        return date_raw
    if isinstance(date_raw, datetime):
        return date_raw.date()
    val = str(date_raw).strip()
    for fmt in ("%d/%m/%Y", "%Y-%m-%d", "%d-%m-%Y", "%m/%d/%Y", "%d.%m.%Y"):
        try:
            return datetime.strptime(val, fmt).date()
        except ValueError:
            continue
    try:
        from dateutil import parser
        return parser.parse(val).date()
    except Exception:
        return None


def resolve_bank_account(entity: EntityModel, ocr_data: dict[str, Any]) -> tuple[BankAccountModel | None, str | None]:
    """
    Resolves BankAccountModel strictly within the already-resolved entity.
    Only active bank accounts are eligible.
    Priority:
    1. Normalized IBAN exact match
    2. Normalized Account Number / RIB exact match

    Fails closed into UNRESOLVED_BANK_ACCOUNT if zero match or no explicit identifier present.
    Fails closed into AMBIGUOUS_BANK_ACCOUNT if multiple accounts match.
    Never selects an account simply because the entity has one active bank account.
    """
    iban_raw = str(ocr_data.get("iban") or "").replace(" ", "").upper()
    acc_num_raw = str(ocr_data.get("account_number") or "").replace(" ", "").upper()
    rib_raw = str(ocr_data.get("rib") or "").replace(" ", "").upper()

    all_accounts = list(BankAccountModel.objects.filter(entity_model=entity, active=True))
    if not all_accounts:
        return None, "NO_BANK_ACCOUNTS_FOR_ENTITY"

    num_candidates = [c for c in [acc_num_raw, rib_raw] if c]

    # Statement identity must deterministically resolve an account via explicit identifier
    if not iban_raw and not num_candidates:
        return None, "UNRESOLVED_BANK_ACCOUNT"

    # Priority 1: IBAN match
    if iban_raw:
        matches = [
            ba for ba in all_accounts
            if (ba.account_number and ba.account_number.replace(" ", "").upper() == iban_raw)
            or (isinstance(ba.metadata, dict) and str(ba.metadata.get("iban", "")).replace(" ", "").upper() == iban_raw)
        ]
        if len(matches) == 1:
            return matches[0], None
        elif len(matches) > 1:
            return None, "AMBIGUOUS_BANK_ACCOUNT"

    # Priority 2: Account Number / RIB match
    if num_candidates:
        matches = [
            ba for ba in all_accounts
            if any(
                (ba.account_number and ba.account_number.replace(" ", "").upper() == cand)
                or (isinstance(ba.metadata, dict) and str(ba.metadata.get("account_number", "")).replace(" ", "").upper() == cand)
                or (isinstance(ba.metadata, dict) and str(ba.metadata.get("rib", "")).replace(" ", "").upper() == cand)
                or (isinstance(ba.metadata, dict) and str(ba.metadata.get("external_reference", "")).replace(" ", "").upper() == cand)
                for cand in num_candidates
            )
        ]
        if len(matches) == 1:
            return matches[0], None
        elif len(matches) > 1:
            return None, "AMBIGUOUS_BANK_ACCOUNT"

    return None, "UNRESOLVED_BANK_ACCOUNT"


def should_trigger_bookkeeping(result: IntakeResult) -> bool:
    """
    Returns True ONLY if accounting intake created state immediately visible to BookkeepingState.
    - IMPORT_JOB with row_count > 0: True (creates StagedTransactionModel bank items)
    - BILL (in DRAFT status): False (hydration excludes draft bills)
    - NEEDS_REVIEW (receipts, unresolved, unsupported): False
    - NOOP: False
    - FAILED: False
    """
    if result.status == "PROCESSED" and result.artifact_type == "IMPORT_JOB" and (result.row_count or 0) > 0:
        return True
    return False


def dispatch_bookkeeping_trigger(payload: dict[str, Any], nats_url: str | None = None) -> None:
    """
    Publishes an entity bookkeeping trigger event to NATS JetStream.
    Subject: events.bookkeeping.trigger
    Safe execution: logs error on transport failure without raising or rolling back database changes.
    """
    subject = "events.bookkeeping.trigger"
    url = nats_url or os.getenv("NATS_URL", "nats://127.0.0.1:4222")
    payload_bytes = json.dumps(payload).encode("utf-8")

    async def _async_publish() -> None:
        import nats
        servers = [s.strip() for s in url.split(",") if s.strip()]
        nc = await nats.connect(
            servers=servers,
            name="bookkeeping-trigger-publisher",
            connect_timeout=0.5,
            max_reconnect_attempts=0,
        )
        try:
            js = nc.jetstream()
            await js.publish(subject, payload_bytes)
        finally:
            await nc.close()

    try:
        from bookkeeping_state.llm.client import run_coro_sync
        run_coro_sync(_async_publish())
        logger.info("Published bookkeeping trigger event", extra={"subject": subject, "payload": payload})
    except Exception as e:
        logger.error("Failed to publish bookkeeping trigger to NATS (accounting truth remains intact)",
                     extra={"error": str(e), "payload": payload})


@receiver(bill_status_approved)
def on_bill_status_approved(sender: Any, instance: Any, **kwargs: Any) -> None:
    """
    Canonical Bill approval hook: when a bill transitions to APPROVED, emit the entity bookkeeping trigger.
    """
    if not instance or getattr(instance, "bill_status", None) != BillModel.BILL_STATUS_APPROVED:
        return
    ledger = getattr(instance, "ledger", None)
    if not ledger or not getattr(ledger, "entity", None):
        return
    entity = ledger.entity
    trigger_id = str(uuid4())
    trigger_payload = {
        "entity_id": str(entity.uuid),
        "trigger_id": trigger_id,
        "source": "bill_approved",
        "source_document_id": str(instance.uuid),
        "timestamp": timezone.now().isoformat(),
    }
    BookkeepingEntityTrigger.record_trigger(
        entity=entity,
        trigger_id=trigger_id,
        source="bill_approved",
    )
    transaction.on_commit(
        lambda: dispatch_bookkeeping_trigger(trigger_payload)
    )


class AccountingIntakeService:
    """
    Service for validating and converting perception documents into Django accounting authority.
    """

    @classmethod
    def _maybe_dispatch_review_notification(cls, intake: BookkeepingDocumentIntake) -> None:
        from bookkeeping_state.notifications.service import (
            REVIEW_NOTIFICATION_ERROR_CODES,
            notify_document_needs_review,
        )
        if (
            intake.status == BookkeepingDocumentIntake.STATUS_NEEDS_REVIEW
            and intake.error_code in REVIEW_NOTIFICATION_ERROR_CODES
        ):
            i_id = str(intake.id)
            transaction.on_commit(lambda id_=i_id: notify_document_needs_review(id_))

    @classmethod
    def process_event(cls, event: dict[str, Any]) -> IntakeResult:
        doc_id_raw = event.get("document_id")
        entity_id_raw = event.get("entity_id")

        if not doc_id_raw or not entity_id_raw:
            logger.error("AccountingIntakeService: event missing document_id or entity_id", extra={"event": event})
            return IntakeResult(status="FAILED", error_code="MISSING_ENVELOPE_FIELDS")

        try:
            doc_uuid = UUID(str(doc_id_raw))
            entity_uuid = UUID(str(entity_id_raw))
        except (ValueError, TypeError) as e:
            logger.error("AccountingIntakeService: invalid UUID in event", extra={"error": str(e)})
            return IntakeResult(status="FAILED", error_code="INVALID_UUID_ENVELOPE")

        # 1. Resolve source document from PostgreSQL toro_core.documents
        doc_record = DocumentSourceRepository.get_document(doc_uuid)
        if not doc_record:
            logger.error("AccountingIntakeService: document not found in toro_core.documents", extra={"doc_id": str(doc_uuid)})
            return IntakeResult(status="FAILED", error_code="DOCUMENT_NOT_FOUND")

        # 2. Verify entity_id matches document metadata
        meta_entity_id = doc_record.metadata.get("entity_id")
        if meta_entity_id and str(meta_entity_id) != str(entity_uuid):
            logger.error("AccountingIntakeService: entity mismatch between event and document metadata",
                         extra={"event_entity": str(entity_uuid), "doc_entity": meta_entity_id})
            return IntakeResult(status="FAILED", error_code="ENTITY_MISMATCH")

        # 3. Verify OCR status is successful
        if doc_record.ocr_status not in ("OCR_SUCCESS", "EMBEDDINGS_SUCCESS", "PROCESSED"):
            logger.info("AccountingIntakeService: document ocr_status is not successful",
                        extra={"doc_id": str(doc_uuid), "ocr_status": doc_record.ocr_status})
            return IntakeResult(status="FAILED", error_code="OCR_NOT_SUCCESSFUL")

        # 4. Resolve Django EntityModel
        try:
            entity = EntityModel.objects.get(uuid=entity_uuid)
        except EntityModel.DoesNotExist:
            logger.error("AccountingIntakeService: EntityModel does not exist", extra={"entity_id": str(entity_uuid)})
            return IntakeResult(status="FAILED", error_code="ENTITY_NOT_FOUND")

        # 5. Extract doc_type and ocr data payload
        raw_ocr = doc_record.raw_ocr_json or {}
        doc_type = (raw_ocr.get("doc_type") or doc_record.document_type or "other").lower().strip()
        ocr_data = raw_ocr.get("data")
        if not isinstance(ocr_data, dict):
            ocr_data = {}

        session_id = event.get("session_id") or doc_record.session_id or ""
        source_message_id = event.get("source_message_id") or doc_record.metadata.get("external_id") or ""

        # 6. Idempotency claim
        with transaction.atomic():
            intake, created = BookkeepingDocumentIntake.objects.select_for_update().get_or_create(
                source_document_id=doc_record.id,
                defaults={
                    "entity": entity,
                    "source_session_id": session_id,
                    "source_message_id": source_message_id,
                    "doc_type": doc_type,
                    "status": BookkeepingDocumentIntake.STATUS_PENDING,
                    "metadata": {
                        "file_name": doc_record.file_name,
                        "s3_url": doc_record.s3_url,
                    },
                },
            )

            if not created and intake.status in (
                BookkeepingDocumentIntake.STATUS_PROCESSED,
                BookkeepingDocumentIntake.STATUS_NEEDS_REVIEW,
                BookkeepingDocumentIntake.STATUS_FAILED,
            ):
                logger.info("AccountingIntakeService: duplicate event already terminal, returning NOOP",
                            extra={"doc_id": str(doc_uuid), "status": intake.status})
                return IntakeResult(
                    status="NOOP",
                    reason="ALREADY_PROCESSED",
                    artifact_type=intake.accounting_artifact_type,
                    artifact_id=intake.accounting_artifact_id,
                )

        # 7. Route conversion by doc_type
        if doc_type == "bank_statement":
            return cls.convert_bank_statement(intake, entity, doc_record, ocr_data)
        elif doc_type in ("invoice", "bill"):
            return cls.convert_supplier_invoice(intake, entity, doc_record, ocr_data)
        elif doc_type == "receipt":
            return cls.convert_receipt(intake, entity, doc_record, ocr_data)
        else:
            return cls.convert_other(intake, entity, doc_record, ocr_data)

    @classmethod
    def convert_bank_statement(
        cls,
        intake: BookkeepingDocumentIntake,
        entity: EntityModel,
        doc_record: DocumentSourceRecord,
        ocr_data: dict[str, Any],
    ) -> IntakeResult:
        # 1. Resolve BankAccountModel
        bank_account, err_code = resolve_bank_account(entity, ocr_data)
        if not bank_account:
            intake.status = BookkeepingDocumentIntake.STATUS_NEEDS_REVIEW
            intake.error_code = err_code or "UNRESOLVED_BANK_ACCOUNT"
            intake.error_detail = (
                f"Could not deterministically match bank account for IBAN='{ocr_data.get('iban')}', "
                f"Account='{ocr_data.get('account_number')}', RIB='{ocr_data.get('rib')}'"
            )
            intake.processed_at = timezone.now()
            intake.save(update_fields=["status", "error_code", "error_detail", "processed_at"])
            cls._maybe_dispatch_review_notification(intake)
            return IntakeResult(status="NEEDS_REVIEW", error_code=intake.error_code, error_detail=intake.error_detail)

        # 2. Extract transaction list
        raw_txs = ocr_data.get("transactions") or []
        if isinstance(raw_txs, dict):
            txs_list = [raw_txs[k] for k in sorted(raw_txs.keys(), key=lambda x: int(x) if x.isdigit() else x)]
        elif isinstance(raw_txs, list):
            txs_list = raw_txs
        else:
            intake.status = BookkeepingDocumentIntake.STATUS_NEEDS_REVIEW
            intake.error_code = "MALFORMED_TRANSACTION_LIST"
            intake.error_detail = "transactions field is neither object nor array"
            intake.processed_at = timezone.now()
            intake.save(update_fields=["status", "error_code", "error_detail", "processed_at"])
            return IntakeResult(status="NEEDS_REVIEW", error_code="MALFORMED_TRANSACTION_LIST")

        if not txs_list:
            intake.status = BookkeepingDocumentIntake.STATUS_NEEDS_REVIEW
            intake.error_code = "EMPTY_STATEMENT_TRANSACTIONS"
            intake.error_detail = "No transactions found in statement OCR output"
            intake.processed_at = timezone.now()
            intake.save(update_fields=["status", "error_code", "error_detail", "processed_at"])
            return IntakeResult(status="NEEDS_REVIEW", error_code="EMPTY_STATEMENT_TRANSACTIONS")

        # 3. Validate all rows before committing anything (All-or-nothing atomicity)
        validated_rows = []
        seen_signatures: dict[tuple[str, str, str, str], int] = {}

        for idx, tx in enumerate(txs_list):
            if not isinstance(tx, dict):
                return cls._fail_statement_review(intake, "INVALID_TRANSACTION_ROW", f"Row {idx} is not an object")

            parsed_d = parse_date(tx.get("date"))
            if not parsed_d:
                return cls._fail_statement_review(
                    intake, "INVALID_TRANSACTION_DATE", f"Row {idx} has unparseable date: {tx.get('date')}"
                )

            raw_amt = tx.get("amount")
            if raw_amt is None:
                return cls._fail_statement_review(intake, "MISSING_TRANSACTION_AMOUNT", f"Row {idx} is missing amount")
            try:
                amt_dec = Decimal(str(raw_amt))
            except Exception:
                return cls._fail_statement_review(
                    intake, "INVALID_TRANSACTION_AMOUNT", f"Row {idx} amount is invalid: {raw_amt}"
                )

            if amt_dec == Decimal(0):
                return cls._fail_statement_review(intake, "ZERO_TRANSACTION_AMOUNT", f"Row {idx} has 0 amount")

            tx_type = str(tx.get("type") or "").strip().lower()
            if tx_type not in ("credit", "debit"):
                return cls._fail_statement_review(
                    intake, "INVALID_TRANSACTION_TYPE", f"Row {idx} type '{tx_type}' not debit/credit"
                )

            # Polarity: credit is inflow > 0, debit is outflow < 0
            signed_amount = abs(amt_dec) if tx_type == "credit" else -abs(amt_dec)

            desc = str(tx.get("description") or "").strip()
            ref = str(tx.get("reference") or "").strip() if tx.get("reference") else None
            v_date = str(tx.get("value_date") or "").strip() if tx.get("value_date") else None

            sig = (parsed_d.isoformat(), str(signed_amount), desc.lower(), (ref or "").lower())
            ord_val = seen_signatures.get(sig, 0)
            seen_signatures[sig] = ord_val + 1

            fit_id = derive_staged_fit_id(
                document_id=str(doc_record.id),
                date_str=parsed_d.isoformat(),
                value_date_str=v_date,
                signed_amount=signed_amount,
                reference=ref,
                description=desc,
                occurrence_ordinal=ord_val,
            )

            validated_rows.append({
                "date_posted": parsed_d,
                "amount": signed_amount,
                "name": (desc[:200] if desc else "Bank Transaction"),
                "memo": (f"Ref: {ref}"[:200] if ref else ""),
                "fit_id": fit_id,
            })

        # 4. Atomic creation of ImportJobModel + StagedTransactionModel rows
        with transaction.atomic():
            import_desc = f"Bank Statement {doc_record.file_name} ({ocr_data.get('statement_date') or doc_record.id})"[:200]
            import_job = ImportJobModel.objects.create(
                description=import_desc,
                bank_account_model=bank_account,
                completed=False,
            )
            import_job.configure(commit=True)

            staged_objs = [
                StagedTransactionModel(
                    import_job=import_job,
                    date_posted=r["date_posted"],
                    amount=r["amount"],
                    name=r["name"],
                    memo=r["memo"],
                    fit_id=r["fit_id"],
                    parent=None,
                    transaction_model=None,
                )
                for r in validated_rows
            ]
            StagedTransactionModel.objects.bulk_create(staged_objs)

            intake.status = BookkeepingDocumentIntake.STATUS_PROCESSED
            intake.accounting_artifact_type = "IMPORT_JOB"
            intake.accounting_artifact_id = str(import_job.uuid)
            intake.error_code = ""
            intake.error_detail = ""
            intake.processed_at = timezone.now()
            intake.save(update_fields=[
                "status",
                "accounting_artifact_type",
                "accounting_artifact_id",
                "error_code",
                "error_detail",
                "processed_at",
            ])

            # Record durable trigger and register post-commit NATS dispatch
            trigger_id = str(uuid4())
            trigger_payload = {
                "entity_id": str(entity.uuid),
                "trigger_id": trigger_id,
                "source": "accounting_intake",
                "source_document_id": str(doc_record.id),
                "timestamp": timezone.now().isoformat(),
            }
            BookkeepingEntityTrigger.record_trigger(
                entity=entity,
                trigger_id=trigger_id,
                source="accounting_intake",
            )
            transaction.on_commit(
                lambda: dispatch_bookkeeping_trigger(trigger_payload)
            )

        return IntakeResult(
            status="PROCESSED",
            artifact_type="IMPORT_JOB",
            artifact_id=str(import_job.uuid),
            row_count=len(staged_objs),
        )

    @classmethod
    def _fail_statement_review(cls, intake: BookkeepingDocumentIntake, error_code: str, error_detail: str) -> IntakeResult:
        intake.status = BookkeepingDocumentIntake.STATUS_NEEDS_REVIEW
        intake.error_code = error_code
        intake.error_detail = error_detail
        intake.processed_at = timezone.now()
        intake.save(update_fields=["status", "error_code", "error_detail", "processed_at"])
        cls._maybe_dispatch_review_notification(intake)
        return IntakeResult(status="NEEDS_REVIEW", error_code=error_code, error_detail=error_detail)

    @classmethod
    def convert_supplier_invoice(
        cls,
        intake: BookkeepingDocumentIntake,
        entity: EntityModel,
        doc_record: DocumentSourceRecord,
        ocr_data: dict[str, Any],
    ) -> IntakeResult:
        # 1. Deterministic Vendor resolution
        vendor_name_raw = str(ocr_data.get("vendor_name") or "").strip()
        if not vendor_name_raw:
            intake.status = BookkeepingDocumentIntake.STATUS_NEEDS_REVIEW
            intake.error_code = "MISSING_VENDOR_NAME"
            intake.error_detail = "Invoice OCR extraction is missing vendor_name"
            intake.processed_at = timezone.now()
            intake.save(update_fields=["status", "error_code", "error_detail", "processed_at"])
            return IntakeResult(status="NEEDS_REVIEW", error_code="MISSING_VENDOR_NAME")

        vendors = list(VendorModel.objects.filter(entity_model=entity, vendor_name__iexact=vendor_name_raw))
        if len(vendors) == 0:
            intake.status = BookkeepingDocumentIntake.STATUS_NEEDS_REVIEW
            intake.error_code = "UNRESOLVED_VENDOR"
            intake.error_detail = f"No matching vendor found for '{vendor_name_raw}'"
            intake.processed_at = timezone.now()
            intake.save(update_fields=["status", "error_code", "error_detail", "processed_at"])
            cls._maybe_dispatch_review_notification(intake)
            return IntakeResult(status="NEEDS_REVIEW", error_code="UNRESOLVED_VENDOR", error_detail=intake.error_detail)
        elif len(vendors) > 1:
            intake.status = BookkeepingDocumentIntake.STATUS_NEEDS_REVIEW
            intake.error_code = "AMBIGUOUS_VENDOR"
            intake.error_detail = f"Multiple matching vendors for '{vendor_name_raw}': {[v.vendor_name for v in vendors]}"
            intake.processed_at = timezone.now()
            intake.save(update_fields=["status", "error_code", "error_detail", "processed_at"])
            cls._maybe_dispatch_review_notification(intake)
            return IntakeResult(status="NEEDS_REVIEW", error_code="AMBIGUOUS_VENDOR", error_detail=intake.error_detail)

        matched_vendor = vendors[0]

        # 2. Account resolution from Entity default COA
        account_qs = None
        if entity.default_coa_id:
            try:
                account_qs = entity.get_default_coa_accounts(active=False)
            except Exception:
                account_qs = None
        if account_qs is None or not account_qs.exists():
            account_qs = AccountModel.objects.filter(coa_model__entity=entity)

        cash_acc = (
            account_qs.filter(role=ASSET_CA_CASH, active=True).first()
            or account_qs.filter(role=ASSET_CA_CASH).first()
        )
        prepaid_acc = (
            account_qs.filter(role=ASSET_CA_PREPAID, active=True).first()
            or account_qs.filter(role=ASSET_CA_PREPAID).first()
        )
        unearned_acc = (
            account_qs.filter(role=LIABILITY_CL_ACC_PAYABLE, active=True).first()
            or account_qs.filter(role=LIABILITY_CL_ACC_PAYABLE).first()
        )

        if not cash_acc or not prepaid_acc or not unearned_acc:
            intake.status = BookkeepingDocumentIntake.STATUS_NEEDS_REVIEW
            intake.error_code = "UNRESOLVED_BILL_ACCOUNTS"
            intake.error_detail = (
                f"Entity '{entity.name}' is missing required default accounts: "
                f"cash={bool(cash_acc)}, prepaid={bool(prepaid_acc)}, payable={bool(unearned_acc)}"
            )
            intake.processed_at = timezone.now()
            intake.save(update_fields=["status", "error_code", "error_detail", "processed_at"])
            return IntakeResult(status="NEEDS_REVIEW", error_code="UNRESOLVED_BILL_ACCOUNTS", error_detail=intake.error_detail)

        # 3. Parse invoice date & amount
        inv_date = parse_date(ocr_data.get("date")) or timezone.now().date()
        raw_total = ocr_data.get("total_mad") or ocr_data.get("total") or ocr_data.get("amount") or 0
        try:
            total_amt = Decimal(str(raw_total))
        except Exception:
            total_amt = Decimal("0.00")

        inv_num = str(ocr_data.get("invoice_number") or "").strip()

        try:
            bill = BillModel(
                vendor=matched_vendor,
                amount_due=total_amt,
                amount_paid=Decimal("0.00"),
                date_draft=inv_date,
                xref=inv_num[:20] if inv_num else None,
                cash_account=cash_acc,
                prepaid_account=prepaid_acc,
                unearned_account=unearned_acc,
                additional_info={
                    "source_document_id": str(doc_record.id),
                    "invoice_number": inv_num,
                    "vendor_name": matched_vendor.vendor_name,
                    "ht_mad": str(ocr_data.get("ht_mad") or ""),
                    "tva_mad": str(ocr_data.get("tva_mad") or ""),
                },
            )
            _, bill = bill.configure(
                entity_slug=entity,
                date_draft=inv_date,
                commit=False,
            )
            bill.bill_status = BillModel.BILL_STATUS_DRAFT
            bill.clean()
            bill.save()

            intake.status = BookkeepingDocumentIntake.STATUS_PROCESSED
            intake.accounting_artifact_type = "BILL"
            intake.accounting_artifact_id = str(bill.uuid)
            intake.error_code = ""
            intake.error_detail = ""
            intake.processed_at = timezone.now()
            intake.save(update_fields=[
                "status",
                "accounting_artifact_type",
                "accounting_artifact_id",
                "error_code",
                "error_detail",
                "processed_at",
            ])
        except Exception as e:
            intake.status = BookkeepingDocumentIntake.STATUS_FAILED
            intake.error_code = "BILL_CREATION_FAILED"
            intake.error_detail = str(e)
            intake.processed_at = timezone.now()
            intake.save(update_fields=["status", "error_code", "error_detail", "processed_at"])
            return IntakeResult(status="FAILED", error_code="BILL_CREATION_FAILED", error_detail=str(e))

        return IntakeResult(status="PROCESSED", artifact_type="BILL", artifact_id=str(bill.uuid))

    @classmethod
    def convert_receipt(
        cls,
        intake: BookkeepingDocumentIntake,
        entity: EntityModel,
        doc_record: DocumentSourceRecord,
        ocr_data: dict[str, Any],
    ) -> IntakeResult:
        intake.status = BookkeepingDocumentIntake.STATUS_NEEDS_REVIEW
        intake.error_code = "RECEIPT_REQUIRES_LINKING"
        intake.accounting_artifact_type = "SUPPORTING_DOCUMENT"
        intake.accounting_artifact_id = str(doc_record.id)
        intake.error_detail = (
            f"Receipt requires manual linking to bank item. "
            f"Vendor: '{ocr_data.get('vendor_name')}', Total: '{ocr_data.get('total_mad') or ocr_data.get('total')}', "
            f"Date: '{ocr_data.get('date')}', Payment Method: '{ocr_data.get('payment_method')}'"
        )
        intake.processed_at = timezone.now()
        intake.save(update_fields=[
            "status",
            "error_code",
            "accounting_artifact_type",
            "accounting_artifact_id",
            "error_detail",
            "processed_at",
        ])
        cls._maybe_dispatch_review_notification(intake)
        return IntakeResult(status="NEEDS_REVIEW", error_code="RECEIPT_REQUIRES_LINKING", error_detail=intake.error_detail)

    @classmethod
    def convert_other(
        cls,
        intake: BookkeepingDocumentIntake,
        entity: EntityModel,
        doc_record: DocumentSourceRecord,
        ocr_data: dict[str, Any],
    ) -> IntakeResult:
        intake.status = BookkeepingDocumentIntake.STATUS_NEEDS_REVIEW
        intake.error_code = "UNSUPPORTED_DOCUMENT_TYPE"
        intake.error_detail = f"Document type '{intake.doc_type}' is not currently supported for automated accounting intake"
        intake.processed_at = timezone.now()
        intake.save(update_fields=["status", "error_code", "error_detail", "processed_at"])
        cls._maybe_dispatch_review_notification(intake)
        return IntakeResult(status="NEEDS_REVIEW", error_code="UNSUPPORTED_DOCUMENT_TYPE", error_detail=intake.error_detail)
