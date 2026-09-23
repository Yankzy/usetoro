"""
Coalesced BookkeepingSession Runner.
Provides durable, per-entity concurrency control, burst coalescing, and non-loss of triggers.
"""

from __future__ import annotations

import logging
from typing import Any
from uuid import uuid4

from django.db import transaction
from django.utils import timezone

from bookkeeping_state.session.result import SessionResult
from bookkeeping_state.session.service import (
    BookkeepingApplicationService,
    run_bookkeeping_session,
)
from ledger.models.bookkeeping import BookkeepingEntityTrigger

logger = logging.getLogger("bookkeeping_session_runner")


def process_entity_triggers(
    entity_id: str,
    app_service: BookkeepingApplicationService | None = None,
    session_id_override: str | None = None,
) -> list[SessionResult]:
    """
    Executes production BookkeepingSession for an entity with durable coalescing.

    Guarantees:
    - Bursts of triggers for the same entity coalesce into a single execution.
    - If another session is already running for this entity, the trigger remains pending
      and will be executed by the active runner when it finishes.
    - If a trigger arrives WHILE a session is executing, a subsequent session is guaranteed to run.
    - Different entities execute independently.
    - Duplicate triggers or reruns with zero new work are healthy and non-destructive.
    """
    results: list[SessionResult] = []

    while True:
        with transaction.atomic():
            trigger_rec = (
                BookkeepingEntityTrigger.objects.select_for_update()
                .filter(entity__uuid=entity_id)
                .first()
            )
            if not trigger_rec or not trigger_rec.pending:
                logger.debug("No pending triggers for entity %s", entity_id)
                return results

            if trigger_rec.running:
                logger.info(
                    "Entity %s already has an active session running; trigger will be processed upon completion",
                    entity_id,
                )
                return results

            # Claim execution: mark running, clear pending
            trigger_rec.running = True
            trigger_rec.pending = False
            trigger_rec.save(update_fields=["running", "pending", "updated_at"])

        session_id = session_id_override or f"session:{entity_id}:{uuid4()}"
        res = None
        try:
            logger.info("Executing BookkeepingSession for entity %s (session_id=%s)", entity_id, session_id)
            if app_service:
                res = app_service.run_session(company_id=entity_id, session_id=session_id)
            else:
                res = run_bookkeeping_session(company_id=entity_id, session_id=session_id)
            results.append(res)
        finally:
            with transaction.atomic():
                trigger_rec = (
                    BookkeepingEntityTrigger.objects.select_for_update()
                    .get(entity__uuid=entity_id)
                )
                trigger_rec.running = False
                trigger_rec.last_run_at = timezone.now()
                trigger_rec.last_run_session_id = session_id
                has_subsequent_pending = trigger_rec.pending
                trigger_rec.save(update_fields=["running", "last_run_at", "last_run_session_id", "updated_at"])

        if not has_subsequent_pending:
            break

        # A trigger arrived while the session was running! Clear override and loop to run subsequent session
        session_id_override = None

    return results
