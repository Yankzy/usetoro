from __future__ import annotations

from collections.abc import Callable
from datetime import datetime, timezone
import json
import logging
import os
from typing import Any
import uuid

from bookkeeping_state.bank_categorization.transport_models import (
    BANK_CATEGORIZATION_DAG_ID,
    BANK_CATEGORIZE_SCHEMA_VERSION,
    DEFAULT_BANK_CATEGORIZER_NATS_SUBJECT,
    BankCategorizeRequestEnvelope,
    BankCategorizeResponseEnvelope,
    BankOutcomePayload,
    ProviderIssuePayload,
    compute_canonical_bank_payload_digest,
)
from bookkeeping_state.bank_categorization.view import ResidualBankCategorizationView
from bookkeeping_state.dag.errors import (
    AseExecutionFailedError,
    AseExecutionTimeoutError,
    AseTransportError,
)
from bookkeeping_state.llm.client import run_coro_sync

logger = logging.getLogger(__name__)


async def _async_nats_bank_request(
    nats_url: str,
    subject: str,
    payload_bytes: bytes,
    timeout_seconds: float,
) -> bytes:
    import time
    import nats

    servers = [s.strip() for s in nats_url.split(",") if s.strip()]
    nc = await nats.connect(servers=servers, name="residual-bank-eval-client")
    try:
        inbox = nc.new_inbox()
        sub = await nc.subscribe(inbox)
        await nc.publish(subject, payload_bytes, reply=inbox)
        start_t = time.monotonic()
        while True:
            remaining = timeout_seconds - (time.monotonic() - start_t)
            if remaining <= 0:
                raise TimeoutError(f"NATS bank request to {subject} timed out")
            msg = await sub.next_msg(timeout=remaining)
            try:
                data = json.loads(msg.data.decode("utf-8"))
                if isinstance(data, dict) and "stream" in data and "seq" in data and "schema_version" not in data:
                    continue
            except Exception:
                pass
            return msg.data
    finally:
        await nc.drain()


class NatsAseBankCategorizer:
    """
    Production wire client for residual bank categorization over NATS / Go ASE.
    Sends schema bookkeeping.ase.bank_categorize.v1 requests to
    worker.inbox.bookkeeping_ase_bank_categorizer.
    """

    def __init__(
        self,
        *,
        nats_url: str | None = None,
        subject: str = DEFAULT_BANK_CATEGORIZER_NATS_SUBJECT,
        timeout_seconds: float = 60.0,
        transport: Callable[[str, bytes, float], bytes] | None = None,
    ) -> None:
        self.nats_url = nats_url or os.environ.get("NATS_URL", "nats://localhost:4224")
        self.subject = subject
        self.timeout_seconds = timeout_seconds
        self._transport = transport

    def check_readiness(self) -> None:
        """
        Verify transport connectivity / readiness before session execution.
        Raises AseTransportError or AseExecutionFailedError if not ready.
        """
        if self._transport is not None:
            if hasattr(self._transport, "check_readiness") and callable(self._transport.check_readiness):
                self._transport.check_readiness()
            return

        try:
            import nats

            async def _ping_nats() -> None:
                servers = [s.strip() for s in self.nats_url.split(",") if s.strip()]
                nc = await nats.connect(
                    servers=servers,
                    name="residual-bank-readiness-check",
                    connect_timeout=min(self.timeout_seconds, 2.0),
                )
                try:
                    pass
                finally:
                    await nc.close()

            run_coro_sync(_ping_nats())
        except Exception as exc:
            if isinstance(exc, (AseExecutionFailedError, AseTransportError)):
                raise
            raise AseTransportError(
                f"Residual bank categorizer NATS preflight failed on {self.nats_url}: {exc}"
            ) from exc

    def categorize_view(
        self,
        view: ResidualBankCategorizationView,
    ) -> BankCategorizeResponseEnvelope:
        """
        Execute residual bank categorization against Go ASE for all items in view.
        """
        if view.item_count == 0:
            return BankCategorizeResponseEnvelope(
                schema_version=BANK_CATEGORIZE_SCHEMA_VERSION,
                request_id=f"bank-req:{view.session_id}:{view.state_revision}:empty",
                idempotency_key=f"idem:{view.session_id}:{view.state_revision}:empty",
                session_id=view.session_id,
                state_revision=view.state_revision,
                dag_id=BANK_CATEGORIZATION_DAG_ID,
                status="COMPLETED",
                outcomes=(),
            )

        now_iso = datetime.now(timezone.utc).isoformat()
        digest = compute_canonical_bank_payload_digest(
            schema_version=BANK_CATEGORIZE_SCHEMA_VERSION,
            dag_id=BANK_CATEGORIZATION_DAG_ID,
            company_id=view.company_id,
            session_id=view.session_id,
            state_revision=view.state_revision,
            persistence_revision=view.persistence_revision,
            bank_items=view.items,
        )
        idempotency_key = f"bank-idem:{view.session_id}:{view.state_revision}:{digest[:16]}"
        request_id = f"bank-req:{view.session_id}:{view.state_revision}:{uuid.uuid4().hex[:8]}"

        envelope = BankCategorizeRequestEnvelope.from_view(
            view,
            request_id=request_id,
            idempotency_key=idempotency_key,
            dag_id=BANK_CATEGORIZATION_DAG_ID,
            requested_at=now_iso,
        )

        payload_bytes = json.dumps(envelope.to_dict()).encode("utf-8")

        # Execute over transport
        if self._transport is not None:
            raw_resp = self._transport(self.subject, payload_bytes, self.timeout_seconds)
        else:
            try:
                raw_resp = run_coro_sync(
                    _async_nats_bank_request(
                        self.nats_url,
                        self.subject,
                        payload_bytes,
                        self.timeout_seconds,
                    )
                )
            except Exception as exc:
                logger.error("NATS bank categorization request failed: %s", exc)
                raise AseTransportError(f"NATS communication failed on {self.subject}: {exc}") from exc

        try:
            resp_dict = json.loads(raw_resp.decode("utf-8"))
        except Exception as exc:
            raise AseExecutionFailedError(f"Malformed JSON response from bank categorizer: {exc}") from exc

        if resp_dict.get("status") == "ERROR":
            issues = resp_dict.get("provider_issues", [])
            raise AseExecutionFailedError(f"Bank categorizer returned ERROR status: {issues}")

        return BankCategorizeResponseEnvelope.from_dict(resp_dict)
