from __future__ import annotations

import json
import logging
import os
import urllib.error
import urllib.request
from decimal import Decimal
from typing import Any, Callable
from uuid import UUID

from django.conf import settings
from django.db import connection, transaction
from django.utils import timezone

from bookkeeping_state.domain.money import solver_units_to_decimal
from ledger.models.bookkeeping import (
    BookkeepingDocumentIntake,
    BookkeepingNotificationDelivery,
    BookkeepingResidualBankClassificationDecision,
)
from ledger.models.entity import EntityModel

logger = logging.getLogger(__name__)

# The 6 review error codes requiring unresolved item notification
REVIEW_NOTIFICATION_ERROR_CODES = {
    "RECEIPT_REQUIRES_LINKING",
    "UNRESOLVED_VENDOR",
    "AMBIGUOUS_VENDOR",
    "UNRESOLVED_BANK_ACCOUNT",
    "AMBIGUOUS_BANK_ACCOUNT",
    "UNSUPPORTED_DOCUMENT_TYPE",
}

SAFE_ACTION_MAPPING = {
    "RECEIPT_REQUIRES_LINKING": "Please link this receipt manually to the corresponding card/bank transaction or bill in Toro.",
    "UNRESOLVED_VENDOR": "The vendor on this invoice could not be matched. Please verify or create the vendor profile in Toro.",
    "AMBIGUOUS_VENDOR": "Multiple matching vendors were identified. Please confirm the correct vendor in Toro.",
    "UNRESOLVED_BANK_ACCOUNT": "The bank account on this statement could not be identified by IBAN or RIB. Please verify your registered bank accounts.",
    "AMBIGUOUS_BANK_ACCOUNT": "Multiple bank accounts match the identifier on this statement. Please verify the account in Toro.",
    "UNSUPPORTED_DOCUMENT_TYPE": "The document format or type is not currently supported for automated extraction.",
}

# Optional sender override for test isolation
_POSTMARK_SENDER_OVERRIDE: Callable[[str, str, str, str | None], tuple[bool, str]] | None = None


def set_postmark_sender_override(
    override: Callable[[str, str, str, str | None], tuple[bool, str]] | None,
) -> None:
    global _POSTMARK_SENDER_OVERRIDE
    _POSTMARK_SENDER_OVERRIDE = override


def resolve_canonical_recipient(entity: EntityModel) -> str | None:
    """
    Resolves the canonical recipient email address for an entity.

    Precedence:
    1. Direct entity owner/admin/accountant in toro_core.users matching entity.uuid
    2. Parent organization owner/admin/accountant in toro_core.users matching parent_id in toro_core.entities
    3. Django EntityModel.admin.email
    4. EntityModel managers email
    """
    entity_uuid = getattr(entity, "uuid", None) or getattr(entity, "id", None)
    if entity_uuid:
        # Check direct entity users (wrapped in atomic savepoint to protect outer transaction)
        try:
            with transaction.atomic():
                with connection.cursor() as cur:
                    cur.execute(
                        """
                        SELECT email, role FROM toro_core.users
                        WHERE entity_id = %s AND is_active = true
                        ORDER BY CASE
                            WHEN role = 'owner' THEN 1
                            WHEN role = 'admin' THEN 2
                            WHEN role = 'accountant' THEN 3
                            ELSE 4
                        END
                        LIMIT 1;
                        """,
                        [str(entity_uuid)],
                    )
                    row = cur.fetchone()
                    if row and row[0]:
                        return row[0].strip().lower()
        except Exception as e:
            logger.debug("Failed querying direct toro_core.users: %s", e)

        # Check parent entity users (client entities linked to an org)
        try:
            with transaction.atomic():
                with connection.cursor() as cur:
                    cur.execute(
                        """
                        SELECT u.email, u.role FROM toro_core.users u
                        JOIN toro_core.entities e ON e.parent_id = u.entity_id
                        WHERE e.id = %s AND u.is_active = true
                        ORDER BY CASE
                            WHEN u.role = 'owner' THEN 1
                            WHEN u.role = 'admin' THEN 2
                            WHEN u.role = 'accountant' THEN 3
                            ELSE 4
                        END
                        LIMIT 1;
                        """,
                        [str(entity_uuid)],
                    )
                    row = cur.fetchone()
                    if row and row[0]:
                        return row[0].strip().lower()
        except Exception as e:
            logger.debug("Failed querying parent toro_core.users: %s", e)


    # Fallback to EntityModel admin
    admin = getattr(entity, "admin", None)
    if admin and getattr(admin, "email", None):
        return admin.email.strip().lower()

    # Fallback to EntityModel managers
    managers = getattr(entity, "managers", None)
    if managers and hasattr(managers, "all"):
        first_manager = managers.filter(email__isnull=False).exclude(email="").first()
        if first_manager and first_manager.email:
            return first_manager.email.strip().lower()

    return None


def send_postmark_email(
    to: str,
    subject: str,
    text_body: str,
    reply_to: str | None = None,
) -> tuple[bool, str]:
    """
    Sends a transactional email via the Postmark Outbound API.
    Returns (success, message_id_or_error_string).
    """
    if _POSTMARK_SENDER_OVERRIDE is not None:
        return _POSTMARK_SENDER_OVERRIDE(to, subject, text_body, reply_to)

    token = (
        os.environ.get("POSTMARK_TRANSACTIONAL_SERVER_TOKEN")
        or os.environ.get("POSTMARK_SERVER_TOKEN")
        or getattr(settings, "POSTMARK_SERVER_TOKEN", "")
    )
    sender = (
        os.environ.get("POSTMARK_SENDER_SIGNATURE")
        or getattr(settings, "POSTMARK_SENDER_SIGNATURE", "")
        or "do-not-reply@usetoro.io"
    )

    if not token:
        logger.warning(
            "send_postmark_email: Postmark server token not configured, recording delivery without HTTP dispatch"
        )
        # In development / test environments without Postmark credentials, treat as mock delivery
        return True, "mock-postmark-token-not-configured"

    payload: dict[str, Any] = {
        "From": sender,
        "To": to,
        "Subject": subject,
        "TextBody": text_body,
        "MessageStream": "outbound",
    }
    if reply_to:
        payload["ReplyTo"] = reply_to

    data = json.dumps(payload).encode("utf-8")
    req = urllib.request.Request(
        "https://api.postmarkapp.com/email",
        data=data,
        headers={
            "Accept": "application/json",
            "Content-Type": "application/json",
            "X-Postmark-Server-Token": token,
        },
        method="POST",
    )

    try:
        with urllib.request.urlopen(req, timeout=10) as resp:
            body = resp.read().decode("utf-8")
            res_json = json.loads(body)
            msg_id = res_json.get("MessageID", "")
            logger.info("Postmark email sent successfully", extra={"to": to, "msg_id": msg_id})
            return True, str(msg_id)
    except urllib.error.HTTPError as e:
        err_body = e.read().decode("utf-8") if e.fp else str(e)
        logger.error("Postmark API HTTP error: %s - %s", e.code, err_body)
        return False, f"HTTP {e.code}: {err_body}"
    except Exception as e:
        logger.error("Failed sending email via Postmark: %s", e)
        return False, str(e)


def notify_residual_hold(decision_id: str) -> bool:
    """
    Dispatches a one-time notification email for a new residual bank classification decision with status HOLD.
    Durable exactly-once delivery keyed by ('residual_hold', decision_id).
    """
    try:
        decision = (
            BookkeepingResidualBankClassificationDecision.objects.filter(id=decision_id)
            .select_related("entity", "staged_transaction", "bank_account")
            .first()
        )
        if not decision or decision.status != BookkeepingResidualBankClassificationDecision.StatusChoices.HOLD:
            return False

        # Idempotency check: exactly one notification per unresolved event
        existing = BookkeepingNotificationDelivery.objects.filter(
            event_type=BookkeepingNotificationDelivery.EVENT_TYPE_RESIDUAL_HOLD,
            event_id=decision_id,
        ).first()
        if existing and existing.status == BookkeepingNotificationDelivery.STATUS_SENT:
            logger.info("Residual HOLD notification already sent for decision %s", decision_id)
            return False

        entity = decision.entity
        recipient = resolve_canonical_recipient(entity)
        if not recipient:
            logger.warning("No canonical recipient resolved for entity %s on HOLD decision %s", entity.slug, decision_id)
            BookkeepingNotificationDelivery.objects.update_or_create(
                event_type=BookkeepingNotificationDelivery.EVENT_TYPE_RESIDUAL_HOLD,
                event_id=decision_id,
                defaults={
                    "entity": entity,
                    "recipient": "unresolved@usetoro.io",
                    "subject": "Toro needs information for a transaction",
                    "body": "No canonical recipient found",
                    "status": BookkeepingNotificationDelivery.STATUS_SKIPPED,
                    "error_message": "No canonical recipient found",
                },
            )
            return False

        stx = decision.staged_transaction
        date_str = str(getattr(stx, "date_posted", None) or getattr(stx, "date", None) or "N/A")
        amount_units = decision.original_amount_units or (decision.residual_amount_units or 0)
        amount_dec = solver_units_to_decimal(amount_units) if amount_units else (stx.amount if stx else Decimal(0))
        currency = decision.currency or getattr(stx, "currency", None) or "MAD"
        formatted_amount = f"{abs(amount_dec):,.2f}"
        description = (getattr(stx, "name", None) or getattr(stx, "description", None) or getattr(stx, "memo", None) or "N/A") if stx else "N/A"
        hold_reason = decision.hold_reason or decision.rationale or "Needs manual accountant review"
        evidence_req = ", ".join(decision.required_evidence) if decision.required_evidence else ""


        subject = "Toro needs information for a transaction"
        body_lines = [
            f"We could not confidently classify a {formatted_amount} {currency} bank transaction dated {date_str}.",
            "",
            f"Company: {entity.name}",
            f"Date: {date_str}",
            f"Amount: {formatted_amount} {currency}",
            f"Description: {description}",
            f"Reason: {hold_reason}",
        ]
        if evidence_req:
            body_lines.append(f"Required Evidence: {evidence_req}")
        body_lines.extend([
            "",
            "Please review it in Toro or reply with supporting information.",
        ])
        body = "\n".join(body_lines)

        inbound_domain = os.environ.get("POSTMARK_INBOUND_EMAIL_DOMAIN", "inbound.usetoro.io")
        reply_to = f"accounting@{entity.slug}.{inbound_domain}"

        success, result = send_postmark_email(
            to=recipient,
            subject=subject,
            text_body=body,
            reply_to=reply_to,
        )

        if success:
            BookkeepingNotificationDelivery.objects.update_or_create(
                event_type=BookkeepingNotificationDelivery.EVENT_TYPE_RESIDUAL_HOLD,
                event_id=decision_id,
                defaults={
                    "entity": entity,
                    "recipient": recipient,
                    "subject": subject,
                    "body": body,
                    "postmark_message_id": result,
                    "status": BookkeepingNotificationDelivery.STATUS_SENT,
                    "error_message": "",
                },
            )
            return True
        else:
            BookkeepingNotificationDelivery.objects.update_or_create(
                event_type=BookkeepingNotificationDelivery.EVENT_TYPE_RESIDUAL_HOLD,
                event_id=decision_id,
                defaults={
                    "entity": entity,
                    "recipient": recipient,
                    "subject": subject,
                    "body": body,
                    "status": BookkeepingNotificationDelivery.STATUS_FAILED,
                    "error_message": result,
                },
            )
            return False
    except Exception as e:
        logger.error("Unexpected error in notify_residual_hold: %s", e)
        return False


def notify_document_needs_review(intake_id: str) -> bool:
    """
    Dispatches a one-time notification email for a BookkeepingDocumentIntake transitioning to NEEDS_REVIEW
    for one of the 6 targeted review error codes.
    Durable exactly-once delivery keyed by ('document_needs_review', intake_id).
    """
    try:
        intake = (
            BookkeepingDocumentIntake.objects.filter(id=intake_id)
            .select_related("entity")
            .first()
        )
        if not intake or intake.status != BookkeepingDocumentIntake.STATUS_NEEDS_REVIEW:
            return False

        if intake.error_code not in REVIEW_NOTIFICATION_ERROR_CODES:
            logger.debug(
                "Document intake %s error_code %s not in notification set, skipping",
                intake_id,
                intake.error_code,
            )
            return False

        # Idempotency check
        existing = BookkeepingNotificationDelivery.objects.filter(
            event_type=BookkeepingNotificationDelivery.EVENT_TYPE_DOCUMENT_NEEDS_REVIEW,
            event_id=intake_id,
        ).first()
        if existing and existing.status == BookkeepingNotificationDelivery.STATUS_SENT:
            logger.info("Document review notification already sent for intake %s", intake_id)
            return False

        entity = intake.entity
        recipient = resolve_canonical_recipient(entity)
        if not recipient:
            logger.warning("No canonical recipient resolved for entity %s on intake %s", entity.slug, intake_id)
            BookkeepingNotificationDelivery.objects.update_or_create(
                event_type=BookkeepingNotificationDelivery.EVENT_TYPE_DOCUMENT_NEEDS_REVIEW,
                event_id=intake_id,
                defaults={
                    "entity": entity,
                    "recipient": "unresolved@usetoro.io",
                    "subject": "Toro needs review for a document",
                    "body": "No canonical recipient found",
                    "status": BookkeepingNotificationDelivery.STATUS_SKIPPED,
                    "error_message": "No canonical recipient found",
                },
            )
            return False

        meta = intake.metadata or {}
        filename = meta.get("file_name") or meta.get("name") or intake.doc_type or "Document"
        safe_action = SAFE_ACTION_MAPPING.get(
            intake.error_code,
            "Please review this document in Toro or reply with supporting information.",
        )
        error_explanation = intake.error_detail or "Requires manual accountant verification"

        subject = "Toro needs review for a document"
        body_lines = [
            "A newly received document requires your review before it can be processed into accounting records.",
            "",
            f"Company: {entity.name}",
            f"Filename: {filename}",
            f"Review Reason: {intake.error_code} ({error_explanation})",
            f"Safe Next Action: {safe_action}",
            "",
            "Please review this document in Toro or reply with supporting information.",
        ]
        body = "\n".join(body_lines)

        inbound_domain = os.environ.get("POSTMARK_INBOUND_EMAIL_DOMAIN", "inbound.usetoro.io")
        reply_to = f"accounting@{entity.slug}.{inbound_domain}"

        success, result = send_postmark_email(
            to=recipient,
            subject=subject,
            text_body=body,
            reply_to=reply_to,
        )

        if success:
            BookkeepingNotificationDelivery.objects.update_or_create(
                event_type=BookkeepingNotificationDelivery.EVENT_TYPE_DOCUMENT_NEEDS_REVIEW,
                event_id=intake_id,
                defaults={
                    "entity": entity,
                    "recipient": recipient,
                    "subject": subject,
                    "body": body,
                    "postmark_message_id": result,
                    "status": BookkeepingNotificationDelivery.STATUS_SENT,
                    "error_message": "",
                },
            )
            return True
        else:
            BookkeepingNotificationDelivery.objects.update_or_create(
                event_type=BookkeepingNotificationDelivery.EVENT_TYPE_DOCUMENT_NEEDS_REVIEW,
                event_id=intake_id,
                defaults={
                    "entity": entity,
                    "recipient": recipient,
                    "subject": subject,
                    "body": body,
                    "status": BookkeepingNotificationDelivery.STATUS_FAILED,
                    "error_message": result,
                },
            )
            return False
    except Exception as e:
        logger.error("Unexpected error in notify_document_needs_review: %s", e)
        return False


# ======================================================================
# Signal Receivers (Post-Save & Post-Commit Safety)
# ======================================================================

from django.db.models.signals import post_save
from django.dispatch import receiver


@receiver(post_save, sender=BookkeepingResidualBankClassificationDecision)
def on_residual_classification_post_save(
    sender: Any,
    instance: BookkeepingResidualBankClassificationDecision,
    created: bool,
    **kwargs: Any,
) -> None:
    """
    When a BookkeepingResidualBankClassificationDecision is saved with status HOLD,
    schedule a one-time notification dispatch after transaction commits.
    """
    if instance and getattr(instance, "status", None) == BookkeepingResidualBankClassificationDecision.StatusChoices.HOLD:
        dec_id = str(instance.id)
        transaction.on_commit(lambda d_id=dec_id: notify_residual_hold(d_id))


@receiver(post_save, sender=BookkeepingDocumentIntake)
def on_document_intake_post_save(
    sender: Any,
    instance: BookkeepingDocumentIntake,
    **kwargs: Any,
) -> None:
    """
    When a BookkeepingDocumentIntake transitions to NEEDS_REVIEW for one of the 6 targeted error codes,
    schedule a one-time notification dispatch after transaction commits.
    """
    if (
        instance
        and getattr(instance, "status", None) == BookkeepingDocumentIntake.STATUS_NEEDS_REVIEW
        and getattr(instance, "error_code", None) in REVIEW_NOTIFICATION_ERROR_CODES
    ):
        intake_id = str(instance.id)
        transaction.on_commit(lambda i_id=intake_id: notify_document_needs_review(i_id))

