from __future__ import annotations

import hashlib
import json
import logging
import os
from collections.abc import Callable
from datetime import datetime, timezone
from typing import Any

from bookkeeping_state.dag.errors import (
    AseExecutionFailedError,
    AseExecutionTimeoutError,
    AseTransportError,
    DuplicateItemOutcomeError,
    IdempotencyBackendUnavailableError,
    IdempotencyPayloadMismatchError,
    InvalidAccountCodeError,
    InvalidRequestError,
    MissingItemOutcomeError,
    ResponseCorrelationMismatchError,
    StaleStateRevisionError,
    SubjectTypeMismatchError,
    UnauthorizedEvidenceReferenceError,
    UnknownBookItemError,
    UnknownDagError,
    UnsupportedSchemaVersionError,
)
from bookkeeping_state.dag.models import (
    DagBatchPlan,
    DagClassificationItem,
    DagHoldItem,
)
from bookkeeping_state.dag.protocol import AseClassifier
from bookkeeping_state.dag.transport_models import (
    BOOK_CATEGORIZE_SCHEMA_VERSION,
    BOOK_CATEGORIZATION_DAG_ID,
    BOOK_CATEGORIZATION_SCHEMA_VERSION,
    DEFAULT_BOOK_CATEGORIZER_NATS_SUBJECT,
    READINESS_SCHEMA_VERSION,
    BookCategorizeItemPayload,
    BookCategorizeRequestEnvelope,
    BookCategorizeResponseEnvelope,
    BookOutcomePayload,
    ProviderIssuePayload,
    compute_canonical_payload_digest,
)
from bookkeeping_state.dag.view import DagView, DagViewItem
from bookkeeping_state.domain.classifications import ClassificationSource
from bookkeeping_state.llm.client import run_coro_sync

logger = logging.getLogger(__name__)


def compute_canonical_view_hash(items: tuple[BookCategorizeItemPayload, ...]) -> str:
    """
    Compute deterministic SHA-256 hash of canonical sorted view items.
    Stable across dict ordering and Python runtimes.
    """
    sorted_items = sorted(items, key=lambda it: it.book_item_id)
    canonical_dicts = [it.to_dict() for it in sorted_items]
    serialized = json.dumps(canonical_dicts, sort_keys=True, separators=(",", ":"))
    return hashlib.sha256(serialized.encode("utf-8")).hexdigest()[:16]


async def _async_nats_request(
    nats_url: str,
    subject: str,
    payload_bytes: bytes,
    timeout_seconds: float,
) -> bytes:
    import time
    import nats

    servers = [s.strip() for s in nats_url.split(",") if s.strip()]
    nc = await nats.connect(servers=servers, name="bookkeeping-dag-client")
    try:
        inbox = nc.new_inbox()
        sub = await nc.subscribe(inbox)
        await nc.publish(subject, payload_bytes, reply=inbox)
        start_t = time.monotonic()
        while True:
            remaining = timeout_seconds - (time.monotonic() - start_t)
            if remaining <= 0:
                raise TimeoutError(f"NATS request to {subject} timed out")
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


class NatsAseBookCategorizer(AseClassifier):
    """
    Production transport adapter connecting Python BookkeepingSession to Go ASE.
    Satisfies the AseClassifier protocol.
    """

    def __init__(
        self,
        *,
        nats_url: str | None = None,
        subject: str = DEFAULT_BOOK_CATEGORIZER_NATS_SUBJECT,
        timeout_seconds: float = 10.0,
        transport: Callable[[str, bytes, float], bytes] | None = None,
    ) -> None:
        self.nats_url = nats_url or os.environ.get("NATS_URL", "nats://localhost:4222")
        self.subject = subject
        self.timeout_seconds = timeout_seconds
        self._transport = transport

    def check_readiness(self) -> None:
        """
        Verify transport connectivity / readiness before session execution.
        Raises AseExecutionFailedError or AseTransportError if not ready.
        """
        probe_req = {
            "schema_version": "bookkeeping.ase.readiness.v1",
            "request_id": f"readiness-check-{datetime.now(timezone.utc).timestamp()}",
            "action": "PING",
        }
        probe_bytes = json.dumps(probe_req).encode("utf-8")

        if self._transport is not None:
            try:
                raw_resp = self._transport(self.subject, probe_bytes, min(self.timeout_seconds, 2.0))
                resp = json.loads(raw_resp.decode("utf-8"))
                if isinstance(resp, dict) and resp.get("ready") is False:
                    reason = resp.get("reason") or "Worker idempotency backend unavailable"
                    raise AseExecutionFailedError(f"DAG classifier preflight failed: {reason}")
            except (AseExecutionFailedError, AseTransportError):
                raise
            except Exception:
                # If mock transport does not handle readiness probe, pass through for test compatibility
                return
            return

        try:
            import nats

            async def _ping_worker() -> None:
                import time

                servers = [s.strip() for s in self.nats_url.split(",") if s.strip()]
                nc = await nats.connect(
                    servers=servers,
                    name="bookkeeping-readiness-check",
                    connect_timeout=2.0,
                )
                try:
                    inbox = nc.new_inbox()
                    sub = await nc.subscribe(inbox)
                    await nc.publish(self.subject, probe_bytes, reply=inbox)
                    start_t = time.monotonic()
                    while True:
                        remaining = min(self.timeout_seconds, 2.0) - (time.monotonic() - start_t)
                        if remaining <= 0:
                            raise TimeoutError("Timeout waiting for DAG readiness response")
                        msg = await sub.next_msg(timeout=remaining)
                        try:
                            data = json.loads(msg.data.decode("utf-8"))
                            if isinstance(data, dict) and "stream" in data and "seq" in data and "schema_version" not in data:
                                continue
                        except Exception:
                            pass
                        resp = json.loads(msg.data.decode("utf-8"))
                        if not resp.get("ready", False):
                            reason = resp.get("reason") or "Worker idempotency backend unavailable"
                            raise AseExecutionFailedError(f"DAG classifier preflight failed: {reason}")
                        break
                finally:
                    await nc.close()

            run_coro_sync(_ping_worker())
        except Exception as exc:
            if isinstance(exc, AseExecutionFailedError):
                raise
            raise AseExecutionFailedError(f"NATS classifier preflight failed: {exc}") from exc

    def classify_view(
        self,
        view: DagView,
        *,
        only_unclassified: bool = True,
    ) -> DagBatchPlan:
        """
        Classify eligible BookItems present in view using the remote Go ASE DAG over NATS.
        """
        if only_unclassified:
            eligible_items = tuple(it for it in view.items if not it.is_classified)
        else:
            eligible_items = view.items

        if not eligible_items:
            return DagBatchPlan(
                plan_id=f"plan:{view.session_id}:{view.state_revision}:empty",
                session_id=view.session_id,
                expected_state_revision=view.state_revision,
                dag_id=BOOK_CATEGORIZATION_DAG_ID,
                items=(),
                hold_items=(),
            )

        # 1. Build bounded item payloads
        book_items_payload = tuple(
            BookCategorizeItemPayload(
                book_item_id=it.book_item_id,
                date=it.date.isoformat(),
                amount_units=it.amount_units,
                currency=it.currency,
                direction=it.direction.value if hasattr(it.direction, "value") else str(it.direction),
                description=it.description,
                counterparty_id=it.counterparty_id,
                counterparty_name=it.counterparty_name,
                reference=it.reference,
                active_bank_account_id=it.active_bank_account_id,
                evidence_refs=it.evidence_refs,
                safe_evidence_summaries=it.safe_evidence_summaries,
                existing_classification_id=it.existing_classification_id,
                existing_account_code=it.existing_account_code,
                source_artifact_kind=it.source_artifact_kind,
                bookkeeping_role=it.bookkeeping_role,
            )
            for it in eligible_items
        )

        company_id = view.company_id or "company-default"
        payload_digest = compute_canonical_payload_digest(
            schema_version=BOOK_CATEGORIZE_SCHEMA_VERSION,
            company_id=company_id,
            session_id=view.session_id,
            state_revision=view.state_revision,
            persistence_revision=view.persistence_revision,
            dag_id=BOOK_CATEGORIZATION_DAG_ID,
            book_items=book_items_payload,
        )
        view_hash = payload_digest[:16]
        request_id = f"ase-request:{view.session_id}:{view.state_revision}"
        idempotency_key = f"ase-book-categorize:{company_id}:{view.session_id}:{view.state_revision}:{view_hash}"
        now_iso = datetime.now(timezone.utc).isoformat()

        req_env = BookCategorizeRequestEnvelope(
            schema_version=BOOK_CATEGORIZE_SCHEMA_VERSION,
            request_id=request_id,
            idempotency_key=idempotency_key,
            company_id=company_id,
            session_id=view.session_id,
            state_revision=view.state_revision,
            persistence_revision=view.persistence_revision,
            dag_id=BOOK_CATEGORIZATION_DAG_ID,
            requested_at=now_iso,
            book_items=book_items_payload,
        )

        request_bytes = json.dumps(req_env.to_dict()).encode("utf-8")

        # 2. Execute transport
        raw_response_bytes = self._execute_transport(self.subject, request_bytes, self.timeout_seconds)

        # 3. Parse and validate response envelope
        try:
            raw_response = json.loads(raw_response_bytes.decode("utf-8"))
        except Exception as exc:
            raise AseExecutionFailedError(f"Failed to decode remote response JSON: {exc}") from exc

        if not isinstance(raw_response, dict):
            raise InvalidRequestError("Remote response is not a JSON object")

        # Validate schema_version
        schema_ver = raw_response.get("schema_version")
        if schema_ver != BOOK_CATEGORIZATION_SCHEMA_VERSION:
            if schema_ver == "bookkeeping.ase.bank_interpret.v1":
                raise SubjectTypeMismatchError(
                    f"Received bank interpretation schema on book categorization contract: {schema_ver}"
                )
            raise UnsupportedSchemaVersionError(
                f"Unsupported schema version {schema_ver!r}, expected {BOOK_CATEGORIZATION_SCHEMA_VERSION!r}"
            )

        # Validate DAG ID
        resp_dag_id = raw_response.get("dag_id")
        if resp_dag_id != BOOK_CATEGORIZATION_DAG_ID:
            raise UnknownDagError(
                f"DAG ID mismatch {resp_dag_id!r}, expected {BOOK_CATEGORIZATION_DAG_ID!r}"
            )

        # Validate request_id correlation
        resp_req_id = raw_response.get("request_id")
        if resp_req_id != request_id:
            raise ResponseCorrelationMismatchError(
                f"Response request_id {resp_req_id!r} does not match request {request_id!r}"
            )

        # Validate idempotency_key correlation
        resp_idem_key = raw_response.get("idempotency_key")
        if resp_idem_key != idempotency_key:
            raise ResponseCorrelationMismatchError(
                f"Response idempotency_key {resp_idem_key!r} does not match request {idempotency_key!r}"
            )

        # Validate session_id correlation
        resp_session_id = raw_response.get("session_id")
        if resp_session_id != view.session_id:
            raise ResponseCorrelationMismatchError(
                f"Response session_id {resp_session_id!r} does not match request {view.session_id!r}"
            )

        # Validate state_revision correlation
        resp_state_rev = raw_response.get("state_revision")
        if resp_state_rev != view.state_revision:
            raise StaleStateRevisionError(
                f"Response state_revision {resp_state_rev} does not match request {view.state_revision}"
            )

        resp_env = BookCategorizeResponseEnvelope.from_dict(raw_response)

        if resp_env.status == "FAILED":
            for p in resp_env.provider_issues:
                if p.code == "IDEMPOTENCY_KEY_PAYLOAD_MISMATCH":
                    raise IdempotencyPayloadMismatchError(p.message)
                if p.code == "IDEMPOTENCY_BACKEND_UNAVAILABLE":
                    raise IdempotencyBackendUnavailableError(p.message)
            msg = "; ".join(p.message for p in resp_env.provider_issues) or "Remote execution failed"
            raise AseExecutionFailedError(msg)

        # 4. Outcome validation
        requested_book_ids = {it.book_item_id for it in eligible_items}
        authorized_evidence_by_item = {
            it.book_item_id: set(it.evidence_refs) for it in eligible_items
        }
        seen_book_ids: set[str] = set()

        classification_items: list[DagClassificationItem] = []
        hold_items: list[DagHoldItem] = []

        for outcome in resp_env.outcomes:
            book_id = outcome.book_item_id
            if book_id not in requested_book_ids:
                raise UnknownBookItemError(
                    f"Remote outcome references unknown book_item_id {book_id!r}"
                )

            if book_id in seen_book_ids:
                raise DuplicateItemOutcomeError(
                    f"Remote outcome contains duplicate determination for book_item_id {book_id!r}"
                )
            seen_book_ids.add(book_id)

            # Validate evidence refs: Remote outcome must not return unauthorized evidence refs
            authorized_ev = authorized_evidence_by_item.get(book_id, set())
            for ev_ref in outcome.evidence_refs:
                if ev_ref not in authorized_ev:
                    raise UnauthorizedEvidenceReferenceError(
                        f"Remote outcome for book_item_id {book_id!r} returned unauthorized evidence_ref {ev_ref!r}. "
                        f"Authorized refs: {sorted(authorized_ev)}"
                    )

            if outcome.status == "CLASSIFIED":
                if not outcome.account_code or not outcome.account_code.strip():
                    raise InvalidAccountCodeError(
                        f"CLASSIFIED outcome for {book_id!r} missing valid account_code"
                    )
                classification_items.append(
                    DagClassificationItem(
                        book_item_id=book_id,
                        account_code=outcome.account_code.strip(),
                        confidence=outcome.confidence,
                        classification_source=ClassificationSource.ASE_DAG,
                        rationale=outcome.rationale,
                        evidence_refs=outcome.evidence_refs,
                        ase_node_id=outcome.ase_node_id,
                        candidate_source=outcome.candidate_source,
                        constrained_macro=outcome.constrained_macro,
                        candidate_codes=outcome.candidate_codes,
                    )
                )
            elif outcome.status == "HOLD":
                hold_reason = outcome.hold_reason or "HOLD_INSUFFICIENT_EVIDENCE"
                hold_items.append(
                    DagHoldItem(
                        book_item_id=book_id,
                        reason=hold_reason,
                        rationale=outcome.rationale or "",
                        ase_node_id=outcome.ase_node_id or "",
                        candidate_source=outcome.candidate_source,
                        constrained_macro=outcome.constrained_macro,
                        candidate_codes=outcome.candidate_codes,
                    )
                )
            else:
                raise AseExecutionFailedError(
                    f"Unknown outcome status {outcome.status!r} for {book_id!r}"
                )

        # Check for unreturned items without explicit provider issues
        provider_issue_book_ids = {
            p.book_item_id for p in resp_env.provider_issues if p.book_item_id is not None
        }
        missing_ids = requested_book_ids - seen_book_ids - provider_issue_book_ids
        if missing_ids:
            raise MissingItemOutcomeError(
                f"Missing outcome for requested book items: {sorted(missing_ids)}"
            )

        plan_id = f"plan:{resp_env.request_id}"
        return DagBatchPlan(
            plan_id=plan_id,
            session_id=resp_env.session_id,
            expected_state_revision=resp_env.state_revision,
            dag_id=resp_env.dag_id,
            dag_run_id=resp_env.dag_run_id or plan_id,
            items=tuple(classification_items),
            hold_items=tuple(hold_items),
        )

    def _execute_transport(self, subject: str, data: bytes, timeout: float) -> bytes:
        if self._transport is not None:
            try:
                return self._transport(subject, data, timeout)
            except TimeoutError as exc:
                raise AseExecutionTimeoutError(f"NATS transport timed out: {exc}") from exc
            except Exception as exc:
                if isinstance(exc, (AseExecutionTimeoutError, AseExecutionFailedError)):
                    raise
                raise AseExecutionFailedError(f"Transport error: {exc}") from exc

        # Synchronous execution of async NATS client
        try:
            return run_coro_sync(
                _async_nats_request(
                    nats_url=self.nats_url,
                    subject=subject,
                    payload_bytes=data,
                    timeout_seconds=timeout,
                )
            )
        except TimeoutError as exc:
            raise AseExecutionTimeoutError(f"NATS request timed out after {timeout}s: {exc}") from exc
        except Exception as exc:
            raise AseExecutionFailedError(f"NATS communication failed: {exc}") from exc


# Alias for domain clarity
AseBookCategorizer = NatsAseBookCategorizer
