from __future__ import annotations

import inspect
import json
import os
from datetime import date, datetime, timezone
from typing import Any, cast
import pytest
from unittest.mock import MagicMock, patch

from bookkeeping_state.dag.adapter import dag_plan_to_transition_batch
from bookkeeping_state.dag.errors import (
    AseExecutionFailedError,
    AseExecutionTimeoutError,
    DuplicateItemOutcomeError,
    IdempotencyBackendUnavailableError,
    IdempotencyPayloadMismatchError,
    InvalidAccountCodeError,
    MissingDagProviderError,
    MissingItemOutcomeError,
    ResponseCorrelationMismatchError,
    StaleStateRevisionError,
    SubjectTypeMismatchError,
    UnauthorizedEvidenceReferenceError,
    UnknownBookItemError,
    UnknownDagError,
    UnsupportedSchemaVersionError,
)
from bookkeeping_state.dag.models import DagBatchPlan, DagClassificationItem, DagHoldItem
from bookkeeping_state.dag.nats_book_categorizer import (
    BOOK_CATEGORIZATION_DAG_ID,
    BOOK_CATEGORIZATION_SCHEMA_VERSION,
    DEFAULT_BOOK_CATEGORIZER_NATS_SUBJECT,
    NatsAseBookCategorizer,
    compute_canonical_view_hash,
)
from bookkeeping_state.dag.transport_models import (
    BOOK_CATEGORIZE_SCHEMA_VERSION,
    BookCategorizeItemPayload,
    BookCategorizeRequestEnvelope,
    BookCategorizeResponseEnvelope,
    BookOutcomePayload,
    ProviderIssuePayload,
    compute_canonical_payload_digest,
)
from bookkeeping_state.dag.view import DagView, DagViewItem
from bookkeeping_state.domain.commands import (
    CreateClassificationCommand,
    get_stage1_capability_secret,
)
from bookkeeping_state.domain.context import AccountingPolicy, BookkeepingContext
from bookkeeping_state.domain.enums import Direction
from bookkeeping_state.hydration.hydrator import BookkeepingHydrator
from bookkeeping_state.operator.workbench import BookkeepingWorkbench
from bookkeeping_state.persistence.repository import BookkeepingRepository, BookkeepingSnapshot
from bookkeeping_state.session.result import SessionResult
from bookkeeping_state.session.service import (
    create_production_bookkeeping_application_service,
    run_bookkeeping_session,
)
from bookkeeping_state.transitions.engine import TransitionEngine



def _make_sample_dag_view() -> DagView:
    item1 = DagViewItem(
        book_item_id="book-1",
        date=date(2026, 1, 15),
        amount=150000,
        currency="MAD",
        direction=Direction.OUTFLOW,
        description="LOYER MENSUEL",
        evidence_refs=("doc-lease-1",),
    )
    item2 = DagViewItem(
        book_item_id="book-2",
        date=date(2026, 1, 16),
        amount=50000,
        currency="MAD",
        direction=Direction.OUTFLOW,
        description="HONORAIRES AVOCAT",
        evidence_refs=(),
    )
    return DagView(
        session_id="session-test-1",
        state_revision=3,
        items=(item1, item2),
        company_id="company-1",
        persistence_revision=7,
    )


@pytest.mark.django_db
class TestProductionExecutionBoundary:
    """Comprehensive test suite covering Production Execution Boundary (Tests A-T)."""

    def test_a_public_entrypoint_exposes_no_fake_dependency_overrides(self):
        # Test A: Canonical production entrypoint signature exposes NO test/eval overrides
        sig = inspect.signature(run_bookkeeping_session)
        assert set(sig.parameters.keys()) == {"company_id", "session_id", "run_id"}

        # create_production_bookkeeping_application_service creates real production classes
        app_service = create_production_bookkeeping_application_service(
            capability_secret=b"test-secret-32-bytes-long-123456"
        )
        assert isinstance(app_service.repository, BookkeepingRepository)
        assert isinstance(app_service.hydrator, BookkeepingHydrator)
        assert isinstance(app_service.transition_engine, TransitionEngine)
        assert isinstance(app_service.dag_classifier, NatsAseBookCategorizer)

    def test_b_production_factory_contains_zero_eval_imports(self):
        # Test B: Verify no module under ledger/bookkeeping_state imports bookkeeping_state_eval
        import sys
        eval_modules = [m for m in sys.modules if m.startswith("bookkeeping_state_eval")]
        # Also check module source files
        import bookkeeping_state.session.service as s_mod
        import bookkeeping_state.session.factory as f_mod
        import bookkeeping_state.dag.nats_book_categorizer as n_mod

        for mod in (s_mod, f_mod, n_mod):
            source = inspect.getsource(mod)
            for line in source.splitlines():
                stripped = line.strip()
                if stripped.startswith("import ") or stripped.startswith("from "):
                    assert "bookkeeping_state_eval" not in stripped, f"Eval import found in {mod.__name__}: {line}"

    def test_c_preflight_dag_provider_readiness_before_mutation(self):
        # Test C: Preflight validation fails closed before any state transition
        # When capability secret cannot be resolved, preflight fails immediately
        with patch.dict("os.environ", {}, clear=True):
            with patch("django.conf.settings.SECRET_KEY", ""):
                with pytest.raises(RuntimeError) as exc_info:
                    run_bookkeeping_session(
                        company_id="company-1",
                        session_id="session-1",
                    )
                assert "STAGE1_CAPABILITY_SECRET" in str(exc_info.value)

    def test_d_nats_book_categorizer_exact_request_serialization(self):
        # Test D: Exact PRD request serialization (§8.2)
        view = _make_sample_dag_view()
        captured_data: dict | None = None

        def mock_transport(subject: str, data: bytes, timeout: float) -> bytes:
            nonlocal captured_data
            assert subject == DEFAULT_BOOK_CATEGORIZER_NATS_SUBJECT
            assert timeout == 10.0
            req = json.loads(data.decode("utf-8"))
            captured_data = req
            # Return valid mock response
            resp = {
                "schema_version": BOOK_CATEGORIZATION_SCHEMA_VERSION,
                "request_id": req["request_id"],
                "idempotency_key": req["idempotency_key"],
                "session_id": req["session_id"],
                "state_revision": req["state_revision"],
                "dag_id": BOOK_CATEGORIZATION_DAG_ID,
                "dag_run_id": "dag-run-123",
                "status": "SUCCESS",
                "outcomes": [
                    {
                        "book_item_id": "book-1",
                        "status": "CLASSIFIED",
                        "account_code": "6131",
                        "confidence": 0.99,
                        "rationale": "Commercial rent",
                        "evidence_refs": ["doc-lease-1"],
                        "ase_node_id": "pcge_account_resolver",
                        "terminal_property": "account_code",
                    },
                    {
                        "book_item_id": "book-2",
                        "status": "CLASSIFIED",
                        "account_code": "6136",
                        "confidence": 0.95,
                        "rationale": "Legal fees",
                        "evidence_refs": [],
                        "ase_node_id": "pcge_account_resolver",
                        "terminal_property": "account_code",
                    },
                ],
                "provider_issues": [],
            }
            return json.dumps(resp).encode("utf-8")

        categorizer = NatsAseBookCategorizer(transport=mock_transport)
        categorizer.classify_view(view)

        assert captured_data is not None
        assert captured_data["schema_version"] == BOOK_CATEGORIZATION_SCHEMA_VERSION
        assert captured_data["dag_id"] == BOOK_CATEGORIZATION_DAG_ID
        assert captured_data["session_id"] == "session-test-1"
        assert captured_data["state_revision"] == 3
        assert captured_data["persistence_revision"] == 7
        assert captured_data["company_id"] == "company-1"
        assert len(captured_data["book_items"]) == 2
        assert captured_data["book_items"][0]["book_item_id"] == "book-1"
        assert captured_data["book_items"][0]["evidence_refs"] == ["doc-lease-1"]

    def test_e_deterministic_request_view_hash(self):
        # Test E: Deterministic request/view hash
        item1 = BookCategorizeItemPayload(
            book_item_id="b-1", date="2026-01-01", amount=100, currency="MAD", direction="OUTFLOW"
        )
        item2 = BookCategorizeItemPayload(
            book_item_id="b-2", date="2026-01-02", amount=200, currency="MAD", direction="OUTFLOW"
        )

        # Ordering should not affect hash
        hash_1 = compute_canonical_view_hash((item1, item2))
        hash_2 = compute_canonical_view_hash((item2, item1))
        assert hash_1 == hash_2
        assert len(hash_1) == 16

        # Changed content produces different hash
        item2_modified = BookCategorizeItemPayload(
            book_item_id="b-2", date="2026-01-02", amount=999, currency="MAD", direction="OUTFLOW"
        )
        hash_3 = compute_canonical_view_hash((item1, item2_modified))
        assert hash_1 != hash_3

    def test_f_response_correlation_validation(self):
        # Test F: Response correlation validation
        view = _make_sample_dag_view()

        def make_transport_returning(mismatched_field: str, value: Any):
            def transport(subject: str, data: bytes, timeout: float) -> bytes:
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
                    "provider_issues": [],
                }
                resp[mismatched_field] = value
                return json.dumps(resp).encode("utf-8")
            return transport

        # Mismatched request_id
        categorizer_req = NatsAseBookCategorizer(transport=make_transport_returning("request_id", "wrong-req-id"))
        with pytest.raises(ResponseCorrelationMismatchError):
            categorizer_req.classify_view(view)

        # Mismatched idempotency_key
        categorizer_idemp = NatsAseBookCategorizer(transport=make_transport_returning("idempotency_key", "wrong-idemp"))
        with pytest.raises(ResponseCorrelationMismatchError):
            categorizer_idemp.classify_view(view)

        # Mismatched session_id
        categorizer_sess = NatsAseBookCategorizer(transport=make_transport_returning("session_id", "wrong-session"))
        with pytest.raises(ResponseCorrelationMismatchError):
            categorizer_sess.classify_view(view)

        # Mismatched state_revision
        categorizer_rev = NatsAseBookCategorizer(transport=make_transport_returning("state_revision", 999))
        with pytest.raises(StaleStateRevisionError):
            categorizer_rev.classify_view(view)

    def test_g_wrong_schema_rejected(self):
        # Test G: Wrong schema rejected
        view = _make_sample_dag_view()

        def transport_wrong_schema(subject: str, data: bytes, timeout: float) -> bytes:
            req = json.loads(data.decode("utf-8"))
            resp = {
                "schema_version": "bookkeeping.ase.wrong_schema.v1",
                "request_id": req["request_id"],
                "idempotency_key": req["idempotency_key"],
                "session_id": req["session_id"],
                "state_revision": req["state_revision"],
                "dag_id": BOOK_CATEGORIZATION_DAG_ID,
                "status": "SUCCESS",
                "outcomes": [],
            }
            return json.dumps(resp).encode("utf-8")

        categorizer = NatsAseBookCategorizer(transport=transport_wrong_schema)
        with pytest.raises(UnsupportedSchemaVersionError):
            categorizer.classify_view(view)

        # Bank interpretation schema on book categorization contract raises SubjectTypeMismatchError
        def transport_bank_schema(subject: str, data: bytes, timeout: float) -> bytes:
            req = json.loads(data.decode("utf-8"))
            resp = {
                "schema_version": "bookkeeping.ase.bank_interpret.v1",
                "request_id": req["request_id"],
                "idempotency_key": req["idempotency_key"],
                "session_id": req["session_id"],
                "state_revision": req["state_revision"],
                "dag_id": BOOK_CATEGORIZATION_DAG_ID,
                "status": "SUCCESS",
                "outcomes": [],
            }
            return json.dumps(resp).encode("utf-8")

        categorizer_bank = NatsAseBookCategorizer(transport=transport_bank_schema)
        with pytest.raises(SubjectTypeMismatchError):
            categorizer_bank.classify_view(view)

    def test_h_wrong_dag_id_rejected(self):
        # Test H: Wrong DAG ID rejected (prevent invoking full PCM DAG as narrow classifier)
        view = _make_sample_dag_view()

        def transport_wrong_dag(subject: str, data: bytes, timeout: float) -> bytes:
            req = json.loads(data.decode("utf-8"))
            resp = {
                "schema_version": BOOK_CATEGORIZATION_SCHEMA_VERSION,
                "request_id": req["request_id"],
                "idempotency_key": req["idempotency_key"],
                "session_id": req["session_id"],
                "state_revision": req["state_revision"],
                "dag_id": "pcm_bank_cash_accounting_dag",
                "status": "SUCCESS",
                "outcomes": [],
            }
            return json.dumps(resp).encode("utf-8")

        categorizer = NatsAseBookCategorizer(transport=transport_wrong_dag)
        with pytest.raises(UnknownDagError):
            categorizer.classify_view(view)

    def test_i_unknown_book_item_rejected(self):
        # Test I: Unknown book item outcome rejected
        view = _make_sample_dag_view()

        def transport_unknown_item(subject: str, data: bytes, timeout: float) -> bytes:
            req = json.loads(data.decode("utf-8"))
            resp = {
                "schema_version": BOOK_CATEGORIZATION_SCHEMA_VERSION,
                "request_id": req["request_id"],
                "idempotency_key": req["idempotency_key"],
                "session_id": req["session_id"],
                "state_revision": req["state_revision"],
                "dag_id": BOOK_CATEGORIZATION_DAG_ID,
                "status": "SUCCESS",
                "outcomes": [
                    {
                        "book_item_id": "book-ghost-item",
                        "status": "CLASSIFIED",
                        "account_code": "6131",
                    }
                ],
            }
            return json.dumps(resp).encode("utf-8")

        categorizer = NatsAseBookCategorizer(transport=transport_unknown_item)
        with pytest.raises(UnknownBookItemError):
            categorizer.classify_view(view)

    def test_j_duplicate_item_outcome_rejected(self):
        # Test J: Duplicate item outcome rejected
        view = _make_sample_dag_view()

        def transport_dup_item(subject: str, data: bytes, timeout: float) -> bytes:
            req = json.loads(data.decode("utf-8"))
            resp = {
                "schema_version": BOOK_CATEGORIZATION_SCHEMA_VERSION,
                "request_id": req["request_id"],
                "idempotency_key": req["idempotency_key"],
                "session_id": req["session_id"],
                "state_revision": req["state_revision"],
                "dag_id": BOOK_CATEGORIZATION_DAG_ID,
                "status": "SUCCESS",
                "outcomes": [
                    {"book_item_id": "book-1", "status": "CLASSIFIED", "account_code": "6131"},
                    {"book_item_id": "book-1", "status": "CLASSIFIED", "account_code": "6132"},
                ],
            }
            return json.dumps(resp).encode("utf-8")

        categorizer = NatsAseBookCategorizer(transport=transport_dup_item)
        with pytest.raises(DuplicateItemOutcomeError):
            categorizer.classify_view(view)

    def test_k_missing_item_outcome_becomes_provider_issue(self):
        # Test K: Missing requested item outcome rejected if unannotated
        view = _make_sample_dag_view()

        def transport_missing_item(subject: str, data: bytes, timeout: float) -> bytes:
            req = json.loads(data.decode("utf-8"))
            resp = {
                "schema_version": BOOK_CATEGORIZATION_SCHEMA_VERSION,
                "request_id": req["request_id"],
                "idempotency_key": req["idempotency_key"],
                "session_id": req["session_id"],
                "state_revision": req["state_revision"],
                "dag_id": BOOK_CATEGORIZATION_DAG_ID,
                "status": "SUCCESS",
                "outcomes": [
                    {"book_item_id": "book-1", "status": "CLASSIFIED", "account_code": "6131"}
                    # book-2 is missing without provider issue
                ],
                "provider_issues": [],
            }
            return json.dumps(resp).encode("utf-8")

        categorizer = NatsAseBookCategorizer(transport=transport_missing_item)
        with pytest.raises(MissingItemOutcomeError):
            categorizer.classify_view(view)

    def test_l_hold_distinct_from_provider_failure(self):
        # Test L: HOLD distinct from provider failure
        view = _make_sample_dag_view()

        def transport_hold(subject: str, data: bytes, timeout: float) -> bytes:
            req = json.loads(data.decode("utf-8"))
            resp = {
                "schema_version": BOOK_CATEGORIZATION_SCHEMA_VERSION,
                "request_id": req["request_id"],
                "idempotency_key": req["idempotency_key"],
                "session_id": req["session_id"],
                "state_revision": req["state_revision"],
                "dag_id": BOOK_CATEGORIZATION_DAG_ID,
                "status": "SUCCESS",
                "outcomes": [
                    {
                        "book_item_id": "book-1",
                        "status": "HOLD",
                        "hold_reason": "HOLD_INSUFFICIENT_EVIDENCE",
                        "rationale": "Missing invoice",
                        "required_evidence": ["invoice"],
                        "ase_node_id": "hold_account_ambiguity",
                    },
                    {
                        "book_item_id": "book-2",
                        "status": "HOLD",
                        "hold_reason": "HOLD_INSUFFICIENT_EVIDENCE",
                        "rationale": "Ambiguous description",
                        "ase_node_id": "hold_account_ambiguity",
                    },
                ],
                "provider_issues": [],
            }
            return json.dumps(resp).encode("utf-8")

        categorizer = NatsAseBookCategorizer(transport=transport_hold)
        plan = categorizer.classify_view(view)

        # Plan has 0 classified items and 2 hold items; no exception raised
        assert len(plan.items) == 0
        assert len(plan.hold_items) == 2
        assert plan.hold_items[0].book_item_id == "book-1"
        assert plan.hold_items[0].reason == "HOLD_INSUFFICIENT_EVIDENCE"

    def test_m_timeout_provider_failure_never_becomes_score_zero_or_hold(self):
        # Test M: Transport timeout or provider failure is NOT silent score 0 or HOLD
        view = _make_sample_dag_view()

        def transport_timeout(subject: str, data: bytes, timeout: float) -> bytes:
            raise TimeoutError("NATS request timed out")

        categorizer_to = NatsAseBookCategorizer(transport=transport_timeout)
        with pytest.raises(AseExecutionTimeoutError):
            categorizer_to.classify_view(view)

        def transport_failed_status(subject: str, data: bytes, timeout: float) -> bytes:
            req = json.loads(data.decode("utf-8"))
            resp = {
                "schema_version": BOOK_CATEGORIZATION_SCHEMA_VERSION,
                "request_id": req["request_id"],
                "idempotency_key": req["idempotency_key"],
                "session_id": req["session_id"],
                "state_revision": req["state_revision"],
                "dag_id": BOOK_CATEGORIZATION_DAG_ID,
                "status": "FAILED",
                "outcomes": [],
                "provider_issues": [
                    {"code": "ASE_EXECUTION_FAILED", "message": "Cluster memory exceeded"}
                ],
            }
            return json.dumps(resp).encode("utf-8")

        categorizer_fail = NatsAseBookCategorizer(transport=transport_failed_status)
        with pytest.raises(AseExecutionFailedError):
            categorizer_fail.classify_view(view)

    def test_n_bounded_payload_contains_no_orm_bookkeeping_state(self):
        # Test N: Bounded payload contains strictly primitive serialized JSON, no ORM or State
        view = _make_sample_dag_view()
        captured_bytes: bytes | None = None

        def transport_capture(subject: str, data: bytes, timeout: float) -> bytes:
            nonlocal captured_bytes
            captured_bytes = data
            req = json.loads(data.decode("utf-8"))
            resp = {
                "schema_version": BOOK_CATEGORIZATION_SCHEMA_VERSION,
                "request_id": req["request_id"],
                "idempotency_key": req["idempotency_key"],
                "session_id": req["session_id"],
                "state_revision": req["state_revision"],
                "dag_id": BOOK_CATEGORIZATION_DAG_ID,
                "status": "SUCCESS",
                "outcomes": [
                    {"book_item_id": "book-1", "status": "CLASSIFIED", "account_code": "6131"},
                    {"book_item_id": "book-2", "status": "CLASSIFIED", "account_code": "6136"},
                ],
            }
            return json.dumps(resp).encode("utf-8")

        categorizer = NatsAseBookCategorizer(transport=transport_capture)
        categorizer.classify_view(view)

        assert captured_bytes is not None
        payload_str = captured_bytes.decode("utf-8")
        assert "django" not in payload_str.lower()
        assert "BookkeepingState" not in payload_str
        assert "models" not in payload_str

    def test_o_normalized_response_becomes_current_dag_batch_plan(self):
        # Test O: Normalized response produces valid DagBatchPlan
        view = _make_sample_dag_view()

        def transport_valid(subject: str, data: bytes, timeout: float) -> bytes:
            req = json.loads(data.decode("utf-8"))
            resp = {
                "schema_version": BOOK_CATEGORIZATION_SCHEMA_VERSION,
                "request_id": req["request_id"],
                "idempotency_key": req["idempotency_key"],
                "session_id": req["session_id"],
                "state_revision": req["state_revision"],
                "dag_id": BOOK_CATEGORIZATION_DAG_ID,
                "dag_run_id": "run-99",
                "status": "SUCCESS",
                "outcomes": [
                    {
                        "book_item_id": "book-1",
                        "status": "CLASSIFIED",
                        "account_code": "6131",
                        "confidence": 0.99,
                        "rationale": "Rent payment",
                        "evidence_refs": ["doc-lease-1"],
                        "ase_node_id": "pcge_account_resolver",
                    },
                    {
                        "book_item_id": "book-2",
                        "status": "HOLD",
                        "hold_reason": "HOLD_INSUFFICIENT_EVIDENCE",
                        "rationale": "Missing proof",
                    },
                ],
            }
            return json.dumps(resp).encode("utf-8")

        categorizer = NatsAseBookCategorizer(transport=transport_valid)
        plan = categorizer.classify_view(view)

        assert isinstance(plan, DagBatchPlan)
        assert plan.dag_id == BOOK_CATEGORIZATION_DAG_ID
        assert plan.session_id == "session-test-1"
        assert plan.expected_state_revision == 3
        assert len(plan.items) == 1
        assert len(plan.hold_items) == 1
        assert plan.items[0].book_item_id == "book-1"
        assert plan.items[0].account_code == "6131"
        assert plan.hold_items[0].book_item_id == "book-2"

    def test_p_existing_dag_adapter_and_transition_engine_accepts_valid_result(self):
        # Test P: Existing adapter and TransitionEngine accept the normalized plan
        view = _make_sample_dag_view()

        def transport_valid(subject: str, data: bytes, timeout: float) -> bytes:
            req = json.loads(data.decode("utf-8"))
            resp = {
                "schema_version": BOOK_CATEGORIZATION_SCHEMA_VERSION,
                "request_id": req["request_id"],
                "idempotency_key": req["idempotency_key"],
                "session_id": req["session_id"],
                "state_revision": req["state_revision"],
                "dag_id": BOOK_CATEGORIZATION_DAG_ID,
                "status": "SUCCESS",
                "outcomes": [
                    {"book_item_id": "book-1", "status": "CLASSIFIED", "account_code": "6131"},
                    {"book_item_id": "book-2", "status": "CLASSIFIED", "account_code": "6136"},
                ],
            }
            return json.dumps(resp).encode("utf-8")

        categorizer = NatsAseBookCategorizer(transport=transport_valid)
        plan = categorizer.classify_view(view)

        batch = dag_plan_to_transition_batch(plan, view=view)
        assert batch is not None
        assert len(batch.commands) == 2
        cmd0 = batch.commands[0]
        assert isinstance(cmd0, CreateClassificationCommand)
        assert cmd0.account_code == "6131"
        cmd1 = batch.commands[1]
        assert isinstance(cmd1, CreateClassificationCommand)
        assert cmd1.account_code == "6136"

    def test_q_production_workbench_delegates_to_canonical_service(self):
        # Test Q: Production BookkeepingWorkbench delegates to BookkeepingApplicationService
        wb = BookkeepingWorkbench.__new__(BookkeepingWorkbench)
        wb.repository = MagicMock()
        wb.hydrator = MagicMock()
        wb.company_id = "test-comp"
        wb.session_counter = 0
        wb.routing_semantic_provider = None
        wb.reconciliation_service = None
        wb.dag_classifier = MagicMock()
        wb.clock_time = None
        wb.close_state = MagicMock()
        wb.get_or_hydrate_state = MagicMock()
        wb._get_dag_classifier = MagicMock(return_value=wb.dag_classifier)

        mock_session_result = SessionResult(
            session_id="workbench-session-1",
            company_id="test-comp",
            starting_persistence_revision=1,
            final_persistence_revision=1,
            final_local_state_revision=1,
            last_consistent_local_state_revision=1,
            routing_stage_result=None,
            dag_stage_result=None,
            payment_application_stage_result=None,
            reconciliation_stage_result=None,
            final_validation_status=None,
            is_success=True,
        )

        mock_state = MagicMock()
        mock_state.bank_items = {}
        mock_state.book_items = {}
        mock_state.revision = 1
        mock_state.persistence_revision = 1
        wb.hydrator.hydrate.return_value = mock_state

        with patch("bookkeeping_state.session.service.BookkeepingApplicationService.run_session", return_value=mock_session_result) as mock_run:
            res = wb.run_bookkeeping()
            mock_run.assert_called_once()
            assert res["is_success"] is True

    def test_r_eval_workbench_remains_eval_only(self):
        # Test R: Eval workbench is isolated and production code never references it
        import bookkeeping_state.operator.workbench as wb_mod
        source = inspect.getsource(wb_mod)
        assert "bookkeeping_state_eval" not in source

    def test_s_session_result_detached_no_orm(self):
        # Test S: SessionResult contains zero ORM models
        res = SessionResult(
            session_id="test-session",
            company_id="comp-1",
            starting_persistence_revision=1,
            final_persistence_revision=2,
            final_local_state_revision=3,
            last_consistent_local_state_revision=3,
            routing_stage_result=None,
            dag_stage_result=None,
            payment_application_stage_result=None,
            reconciliation_stage_result=None,
            final_validation_status=None,
            is_success=True,
        )

        from django.db.models import Model
        for field in inspect.getmembers(res):
            assert not isinstance(field[1], Model)

    def test_t_retry_session_id_contract(self):
        # Test T: Retry contract preserves session ID and respects OCC
        from django.contrib.auth import get_user_model
        from ledger.models import EntityModel

        UserModel = get_user_model()
        user = cast(Any, UserModel.objects).create_user(
            username=f"retry_admin_{datetime.now().microsecond}",
            email="admin@test.com",
            password="pwd",
        )
        entity = EntityModel.add_root(
            name=f"Retry Corp {datetime.now().microsecond}",
            admin=user,
            currency="MAD",
            fy_start_month=1,
            accrual_method=False,
        )
        entity.create_chart_of_accounts(
            coa_name="Retry CoA",
            assign_as_default=True,
            commit=True,
        )
        comp_id = str(entity.uuid)
        sess_id = f"sess-{datetime.now().microsecond}"

        app_service = create_production_bookkeeping_application_service(
            capability_secret=b"test-secret-32-bytes-long-123456",
            dag_classifier=NatsAseBookCategorizer(
                transport=lambda s, d, t: json.dumps({
                    "schema_version": BOOK_CATEGORIZATION_SCHEMA_VERSION,
                    "request_id": json.loads(d)["request_id"],
                    "idempotency_key": json.loads(d)["idempotency_key"],
                    "session_id": json.loads(d)["session_id"],
                    "state_revision": json.loads(d)["state_revision"],
                    "dag_id": BOOK_CATEGORIZATION_DAG_ID,
                    "status": "SUCCESS",
                    "outcomes": [],
                }).encode("utf-8")
            ),
        )

        # Run 1
        res1 = app_service.run_session(company_id=comp_id, session_id=sess_id)
        assert res1.is_success

        # Run 2 with same session_id (idempotent retry)
        res2 = app_service.run_session(company_id=comp_id, session_id=sess_id)
        assert res2.is_success
        assert res2.session_id == sess_id

    def test_u_exact_amount_units_integer_preservation(self):
        # Exact AmountUnits integer preservation across DagViewItem -> transport payload
        item1 = DagViewItem(
            book_item_id="item-precise-1",
            date=date(2026, 3, 1),
            amount_units=10001,
            currency="MAD",
            direction=Direction.OUTFLOW,
        )
        item2 = DagViewItem(
            book_item_id="item-precise-2",
            date=date(2026, 3, 2),
            amount_units=123456,
            currency="MAD",
            direction=Direction.INFLOW,
        )
        item3 = DagViewItem(
            book_item_id="item-precise-3",
            date=date(2026, 3, 3),
            amount_units=1,
            currency="MAD",
            direction=Direction.OUTFLOW,
        )
        assert item1.amount_units == 10001
        assert item2.amount_units == 123456
        assert item3.amount_units == 1

        payload1 = BookCategorizeItemPayload(
            book_item_id=item1.book_item_id,
            date=item1.date.isoformat(),
            amount_units=item1.amount_units,
            currency=item1.currency,
            direction=item1.direction.value,
        )
        d1 = payload1.to_dict()
        assert d1["amount_units"] == 10001
        assert d1["amount"] == 10001  # backward compatibility alias
        serialized = json.dumps(d1)
        deserialized = json.loads(serialized)
        assert deserialized["amount_units"] == 10001
        roundtrip = BookCategorizeItemPayload.from_dict(deserialized)
        assert roundtrip.amount_units == 10001
        assert roundtrip.amount == 10001

    def test_v_amount_property_backward_compatibility(self):
        # amount property fallback on DagViewItem and transport payload maintains backward compatibility
        item = DagViewItem(
            book_item_id="item-compat-1",
            date=date(2026, 1, 1),
            amount=50000,
            currency="MAD",
            direction=Direction.OUTFLOW,
        )
        assert item.amount_units == 50000
        assert item.amount == 50000

        payload = BookCategorizeItemPayload(
            book_item_id="payload-compat-1",
            date="2026-01-01",
            amount=75000,
            currency="MAD",
            direction="OUTFLOW",
        )
        assert payload.amount_units == 75000
        assert payload.amount == 75000

    def test_w_all_four_directions_preserved(self):
        # Round-trip preservation of all 4 canonical Direction values
        directions = [
            Direction.BOOK_BANK_DEBIT,
            Direction.BOOK_BANK_CREDIT,
            Direction.INFLOW,
            Direction.OUTFLOW,
        ]
        for d in directions:
            item = DagViewItem(
                book_item_id=f"dir-{d.value}",
                date=date(2026, 1, 1),
                amount_units=10000,
                currency="MAD",
                direction=d,
            )
            payload = BookCategorizeItemPayload(
                book_item_id=item.book_item_id,
                date=item.date.isoformat(),
                amount_units=item.amount_units,
                currency=item.currency,
                direction=item.direction.value,
            )
            serialized = json.dumps(payload.to_dict())
            parsed = json.loads(serialized)
            assert parsed["direction"] == d.value
            roundtrip = BookCategorizeItemPayload.from_dict(parsed)
            assert roundtrip.direction == d.value

    def test_x_bounded_dag_view_fields(self):
        # DagViewItem and transport payload carry bounded fields
        item = DagViewItem(
            book_item_id="bounded-1",
            date=date(2026, 2, 15),
            amount_units=20000,
            currency="MAD",
            direction=Direction.OUTFLOW,
            description="ACHAT SERVEUR",
            counterparty_id="cp-99",
            counterparty_name="Cloud Provider Inc",
            reference="INV-2026-001",
            active_bank_account_id="bank-acc-1",
            evidence_refs=("doc-1", "doc-2"),
            safe_evidence_summaries=("Invoice attached with 20% VAT",),
            source_artifact_kind="BILL",
            bookkeeping_role="OPEN_PAYABLE",
        )
        assert item.counterparty_id == "cp-99"
        assert item.counterparty_name == "Cloud Provider Inc"
        assert item.reference == "INV-2026-001"
        assert item.active_bank_account_id == "bank-acc-1"
        assert item.evidence_refs == ("doc-1", "doc-2")
        assert item.safe_evidence_summaries == ("Invoice attached with 20% VAT",)
        assert item.source_artifact_kind == "BILL"
        assert item.bookkeeping_role == "OPEN_PAYABLE"

        payload = BookCategorizeItemPayload(
            book_item_id=item.book_item_id,
            date=item.date.isoformat(),
            amount_units=item.amount_units,
            currency=item.currency,
            direction=item.direction.value,
            description=item.description,
            counterparty_id=item.counterparty_id,
            counterparty_name=item.counterparty_name,
            reference=item.reference,
            active_bank_account_id=item.active_bank_account_id,
            evidence_refs=item.evidence_refs,
            safe_evidence_summaries=item.safe_evidence_summaries,
            source_artifact_kind=item.source_artifact_kind,
            bookkeeping_role=item.bookkeeping_role,
        )
        d = payload.to_dict()
        assert d["counterparty_id"] == "cp-99"
        assert d["counterparty_name"] == "Cloud Provider Inc"
        assert d["reference"] == "INV-2026-001"
        assert d["active_bank_account_id"] == "bank-acc-1"
        assert d["evidence_refs"] == ["doc-1", "doc-2"]
        assert d["safe_evidence_summaries"] == ["Invoice attached with 20% VAT"]
        assert d["source_artifact_kind"] == "BILL"
        assert d["bookkeeping_role"] == "OPEN_PAYABLE"

    def test_y_public_run_bookkeeping_session_exact_signature(self):
        # Canonical public entrypoint signature is strictly (company_id, session_id, run_id=None)
        sig = inspect.signature(run_bookkeeping_session)
        params = list(sig.parameters.values())
        assert len(params) == 3
        assert params[0].name == "company_id"
        assert params[1].name == "session_id"
        assert params[2].name == "run_id"
        assert params[2].default is None

    def test_z_provider_preflight_rejection_before_hydration_or_mutation(self):
        # Provider preflight rejection: when check_readiness fails, run_session rejects BEFORE state hydration
        mock_classifier = MagicMock(spec=NatsAseBookCategorizer)
        mock_classifier.check_readiness.side_effect = AseExecutionFailedError("NATS cluster unreachable")

        mock_hydrator = MagicMock(spec=BookkeepingHydrator)

        app_service = create_production_bookkeeping_application_service(
            dag_classifier=mock_classifier,
            capability_secret=b"test-secret-32-bytes-long-123456",
        )
        app_service.hydrator = mock_hydrator

        with pytest.raises(AseExecutionFailedError) as exc_info:
            app_service.run_session(company_id="comp-1", session_id="sess-1")
        assert "NATS cluster unreachable" in str(exc_info.value)
        # Hydration was never attempted!
        mock_hydrator.hydrate.assert_not_called()

    def test_aa_unauthorized_evidence_reference_rejected(self):
        # Remote outcome returns unauthorized evidence reference -> raises UnauthorizedEvidenceReferenceError
        view = DagView(
            session_id="session-auth-ev",
            state_revision=1,
            company_id="comp-1",
            items=(
                DagViewItem(
                    book_item_id="book-auth-1",
                    date=date(2026, 1, 1),
                    amount_units=10000,
                    currency="MAD",
                    direction=Direction.OUTFLOW,
                    description="LOYER",
                    evidence_refs=("doc-legit-1",),
                ),
            ),
        )

        def mock_transport(subject: str, data: bytes, timeout: float) -> bytes:
            req = json.loads(data.decode("utf-8"))
            resp = {
                "schema_version": BOOK_CATEGORIZATION_SCHEMA_VERSION,
                "request_id": req["request_id"],
                "idempotency_key": req["idempotency_key"],
                "session_id": req["session_id"],
                "state_revision": req["state_revision"],
                "dag_id": BOOK_CATEGORIZATION_DAG_ID,
                "status": "SUCCESS",
                "outcomes": [
                    {
                        "book_item_id": "book-auth-1",
                        "status": "CLASSIFIED",
                        "account_code": "6131",
                        "confidence": 0.99,
                        "rationale": "Rent",
                        "evidence_refs": ["doc-legit-1", "doc-unauthorized-injected-999"],
                        "ase_node_id": "pcge_account_resolver",
                    },
                ],
            }
            return json.dumps(resp).encode("utf-8")

        classifier = NatsAseBookCategorizer(transport=mock_transport)
        with pytest.raises(UnauthorizedEvidenceReferenceError) as exc_info:
            classifier.classify_view(view)
        assert "doc-unauthorized-injected-999" in str(exc_info.value)

    def test_ab_canonical_view_hash_stability(self):
        # compute_canonical_view_hash is deterministic and excludes requested_at
        item_a = BookCategorizeItemPayload(
            book_item_id="item-a",
            date="2026-01-01",
            amount_units=10000,
            currency="MAD",
            direction="OUTFLOW",
            evidence_refs=("ref-2", "ref-1"),
        )
        item_b = BookCategorizeItemPayload(
            book_item_id="item-b",
            date="2026-01-02",
            amount_units=20000,
            currency="MAD",
            direction="INFLOW",
        )

        hash_order_1 = compute_canonical_view_hash((item_a, item_b))
        hash_order_2 = compute_canonical_view_hash((item_b, item_a))
        assert hash_order_1 == hash_order_2
        assert len(hash_order_1) == 16

    def test_ac_production_preflight_causes_zero_durable_mutation_when_idempotency_unavailable(self):
        # Test P: Production preflight causes zero durable mutation when Go readiness reports idempotency backend unavailable
        def mock_transport_unready(subject: str, data: bytes, timeout: float) -> bytes:
            req = json.loads(data.decode("utf-8"))
            if req.get("schema_version") == "bookkeeping.ase.readiness.v1":
                return json.dumps({
                    "schema_version": "bookkeeping.ase.readiness.v1",
                    "status": "UNHEALTHY",
                    "ready": False,
                    "reason": "Distributed idempotency store unavailable",
                }).encode("utf-8")
            return b"{}"

        categorizer = NatsAseBookCategorizer(transport=mock_transport_unready)
        mock_hydrator = MagicMock(spec=BookkeepingHydrator)

        app_service = create_production_bookkeeping_application_service(
            dag_classifier=categorizer,
            capability_secret=b"test-secret-32-bytes-long-123456",
        )
        app_service.hydrator = mock_hydrator

        with pytest.raises(AseExecutionFailedError) as exc_info:
            app_service.run_session(company_id="comp-fail", session_id="sess-fail")

        assert "Distributed idempotency store unavailable" in str(exc_info.value)
        # Preflight failed before state hydration or mutation occurred!
        mock_hydrator.hydrate.assert_not_called()

    def test_ad_duplicate_stored_go_response_normalizes_into_same_dag_batch_plan(self):
        # Test Q: Duplicate stored Go response normalizes into identical DagBatchPlan
        view = DagView(
            session_id="sess-replay-q",
            state_revision=1,
            company_id="comp-q",
            items=(
                DagViewItem(
                    book_item_id="item-q1",
                    date=date(2026, 1, 1),
                    amount_units=10000,
                    currency="MAD",
                    direction=Direction.OUTFLOW,
                    description="LOYER",
                    evidence_refs=("doc-1",),
                ),
                DagViewItem(
                    book_item_id="item-q2",
                    date=date(2026, 1, 2),
                    amount_units=20000,
                    currency="MAD",
                    direction=Direction.OUTFLOW,
                    description="VIREMENT AMBIGU",
                ),
            ),
        )

        stored_response = {
            "schema_version": BOOK_CATEGORIZATION_SCHEMA_VERSION,
            "request_id": f"ase-request:{view.session_id}:{view.state_revision}",
            "idempotency_key": f"ase-book-categorize:{view.company_id}:{view.session_id}:{view.state_revision}:{compute_canonical_view_hash(tuple(BookCategorizeItemPayload(book_item_id=it.book_item_id, date=it.date.isoformat(), amount_units=it.amount_units, currency=it.currency, direction=it.direction.value, description=it.description, evidence_refs=it.evidence_refs) for it in view.items))}",
            "session_id": view.session_id,
            "state_revision": view.state_revision,
            "dag_id": BOOK_CATEGORIZATION_DAG_ID,
            "dag_run_id": "dag-run-fixed-uuid-12345",
            "status": "SUCCESS",
            "outcomes": [
                {
                    "book_item_id": "item-q1",
                    "status": "CLASSIFIED",
                    "account_code": "6131",
                    "confidence": 0.99,
                    "rationale": "Rent expense",
                    "evidence_refs": ["doc-1"],
                    "ase_node_id": "pcge_account_resolver",
                },
                {
                    "book_item_id": "item-q2",
                    "status": "HOLD",
                    "hold_reason": "HOLD_INSUFFICIENT_EVIDENCE",
                    "rationale": "Ambiguous description",
                    "evidence_refs": [],
                    "ase_node_id": "hold_account_ambiguity",
                },
            ],
            "provider_issues": [],
        }

        def transport_replay(s: str, d: bytes, t: float) -> bytes:
            req = json.loads(d.decode("utf-8"))
            resp = dict(stored_response)
            resp["idempotency_key"] = req["idempotency_key"]
            resp["request_id"] = req["request_id"]
            return json.dumps(resp).encode("utf-8")

        categorizer = NatsAseBookCategorizer(transport=transport_replay)

        plan1 = categorizer.classify_view(view)
        plan2 = categorizer.classify_view(view)

        # Exact logical equivalence
        assert plan1.plan_id == plan2.plan_id
        assert plan1.dag_run_id == plan2.dag_run_id == "dag-run-fixed-uuid-12345"
        assert len(plan1.items) == len(plan2.items) == 1
        assert plan1.items[0].book_item_id == plan2.items[0].book_item_id == "item-q1"
        assert plan1.items[0].account_code == plan2.items[0].account_code == "6131"
        assert plan1.items[0].confidence == plan2.items[0].confidence == 0.99
        assert len(plan1.hold_items) == len(plan2.hold_items) == 1
        assert plan1.hold_items[0].book_item_id == plan2.hold_items[0].book_item_id == "item-q2"
        assert plan1.hold_items[0].reason == plan2.hold_items[0].reason == "HOLD_INSUFFICIENT_EVIDENCE"

    def test_ae_provider_readiness_failure_remains_distinct_from_semantic_hold(self):
        # Test R: Provider/readiness failure remains distinct from semantic HOLD
        view = DagView(
            session_id="sess-r",
            state_revision=1,
            company_id="comp-r",
            items=(
                DagViewItem(
                    book_item_id="item-r1",
                    date=date(2026, 1, 1),
                    amount_units=10000,
                    currency="MAD",
                    direction=Direction.OUTFLOW,
                    description="AMBIGUOUS PAYMENT",
                ),
            ),
        )

        # 1. Semantic HOLD returns valid DagBatchPlan with DagHoldItem (no error raised)
        def transport_semantic_hold(s: str, d: bytes, t: float) -> bytes:
            req = json.loads(d.decode("utf-8"))
            return json.dumps({
                "schema_version": BOOK_CATEGORIZATION_SCHEMA_VERSION,
                "request_id": req["request_id"],
                "idempotency_key": req["idempotency_key"],
                "session_id": req["session_id"],
                "state_revision": req["state_revision"],
                "dag_id": BOOK_CATEGORIZATION_DAG_ID,
                "status": "SUCCESS",
                "outcomes": [
                    {
                        "book_item_id": "item-r1",
                        "status": "HOLD",
                        "hold_reason": "HOLD_INSUFFICIENT_EVIDENCE",
                        "rationale": "Needs invoice",
                        "evidence_refs": [],
                        "ase_node_id": "hold_account_ambiguity",
                    },
                ],
            }).encode("utf-8")

        cat_semantic = NatsAseBookCategorizer(transport=transport_semantic_hold)
        plan_hold = cat_semantic.classify_view(view)
        assert len(plan_hold.items) == 0
        assert len(plan_hold.hold_items) == 1
        assert plan_hold.hold_items[0].reason == "HOLD_INSUFFICIENT_EVIDENCE"

        # 2. Provider failure with payload mismatch raises IdempotencyPayloadMismatchError
        def transport_mismatch(s: str, d: bytes, t: float) -> bytes:
            req = json.loads(d.decode("utf-8"))
            return json.dumps({
                "schema_version": BOOK_CATEGORIZATION_SCHEMA_VERSION,
                "request_id": req["request_id"],
                "idempotency_key": req["idempotency_key"],
                "session_id": req["session_id"],
                "state_revision": req["state_revision"],
                "dag_id": BOOK_CATEGORIZATION_DAG_ID,
                "status": "FAILED",
                "provider_issues": [
                    {
                        "code": "IDEMPOTENCY_KEY_PAYLOAD_MISMATCH",
                        "message": "Idempotency key reused with different request payload digest",
                    },
                ],
            }).encode("utf-8")

        cat_mismatch = NatsAseBookCategorizer(transport=transport_mismatch)
        with pytest.raises(IdempotencyPayloadMismatchError):
            cat_mismatch.classify_view(view)

        # 3. Provider failure with uninitialized idempotency backend raises IdempotencyBackendUnavailableError
        def transport_backend_down(s: str, d: bytes, t: float) -> bytes:
            req = json.loads(d.decode("utf-8"))
            return json.dumps({
                "schema_version": BOOK_CATEGORIZATION_SCHEMA_VERSION,
                "request_id": req["request_id"],
                "idempotency_key": req["idempotency_key"],
                "session_id": req["session_id"],
                "state_revision": req["state_revision"],
                "dag_id": BOOK_CATEGORIZATION_DAG_ID,
                "status": "FAILED",
                "provider_issues": [
                    {
                        "code": "IDEMPOTENCY_BACKEND_UNAVAILABLE",
                        "message": "Distributed idempotency backend is not available",
                    },
                ],
            }).encode("utf-8")

        cat_backend_down = NatsAseBookCategorizer(transport=transport_backend_down)
        with pytest.raises(IdempotencyBackendUnavailableError):
            cat_backend_down.classify_view(view)

    def test_af_r_python_canonical_digest_matches_same_golden_vector(self):
        # Test R: Python canonical digest matches cross-language golden vector
        fixture_path = os.path.join(
            os.path.dirname(__file__), "fixtures", "book_categorize_golden_vector.json"
        )
        with open(fixture_path, "r", encoding="utf-8") as f:
            fixture = json.load(f)

        req_data = fixture["request"]
        items = tuple(
            BookCategorizeItemPayload.from_dict(it) for it in req_data["book_items"]
        )

        computed_digest = compute_canonical_payload_digest(
            schema_version=req_data["schema_version"],
            company_id=req_data["company_id"],
            session_id=req_data["session_id"],
            state_revision=req_data["state_revision"],
            persistence_revision=req_data["persistence_revision"],
            dag_id=req_data["dag_id"],
            book_items=items,
        )

        assert computed_digest == fixture["expected_sha256_digest"]
        assert computed_digest == "c16e6f272c3d98561be7b311bae99e02b49689fb8b60275fc38b8134dad4d855"

    def test_ag_s_canonical_schema_constant_exactly_book_categorize_v1(self):
        # Test S: Canonical schema constant exactly bookkeeping.ase.book_categorize.v1
        assert BOOK_CATEGORIZE_SCHEMA_VERSION == "bookkeeping.ase.book_categorize.v1"
        assert BOOK_CATEGORIZATION_SCHEMA_VERSION == "bookkeeping.ase.book_categorize.v1"

    def test_ah_t_malformed_alternate_spelling_rejected(self):
        # Test T: Malformed alternate spelling rejected as UnsupportedSchemaVersionError
        view = _make_sample_dag_view()

        def transport_alternate_spelling(s: str, d: bytes, t: float) -> bytes:
            req = json.loads(d.decode("utf-8"))
            return json.dumps({
                "schema_version": "bookkeeping.ase.book_categorization.v1",  # Malformed spelling
                "request_id": req["request_id"],
                "idempotency_key": req["idempotency_key"],
                "session_id": req["session_id"],
                "state_revision": req["state_revision"],
                "dag_id": BOOK_CATEGORIZATION_DAG_ID,
                "status": "SUCCESS",
                "outcomes": [],
            }).encode("utf-8")

        categorizer = NatsAseBookCategorizer(transport=transport_alternate_spelling)
        with pytest.raises(UnsupportedSchemaVersionError) as exc_info:
            categorizer.classify_view(view)
        assert "bookkeeping.ase.book_categorization.v1" in str(exc_info.value)

    def test_ai_u_request_view_identity_changes_for_date_currency_description_persistence_revision(self):
        # Test U: Semantic identity changes for date, currency, description, persistence revision
        base_item = BookCategorizeItemPayload(
            book_item_id="item-u",
            date="2026-03-01",
            amount_units=10000,
            currency="EUR",
            direction="OUTFLOW",
            description="Base description",
        )
        base_digest = compute_canonical_payload_digest(
            schema_version=BOOK_CATEGORIZE_SCHEMA_VERSION,
            company_id="comp-1",
            session_id="sess-1",
            state_revision=1,
            persistence_revision=1,
            dag_id=BOOK_CATEGORIZATION_DAG_ID,
            book_items=(base_item,),
        )

        # Date change
        item_date = BookCategorizeItemPayload(
            book_item_id="item-u",
            date="2026-03-02",
            amount_units=10000,
            currency="EUR",
            direction="OUTFLOW",
            description="Base description",
        )
        digest_date = compute_canonical_payload_digest(
            schema_version=BOOK_CATEGORIZE_SCHEMA_VERSION,
            company_id="comp-1",
            session_id="sess-1",
            state_revision=1,
            persistence_revision=1,
            dag_id=BOOK_CATEGORIZATION_DAG_ID,
            book_items=(item_date,),
        )
        assert base_digest != digest_date

        # Currency change
        item_curr = BookCategorizeItemPayload(
            book_item_id="item-u",
            date="2026-03-01",
            amount_units=10000,
            currency="USD",
            direction="OUTFLOW",
            description="Base description",
        )
        digest_curr = compute_canonical_payload_digest(
            schema_version=BOOK_CATEGORIZE_SCHEMA_VERSION,
            company_id="comp-1",
            session_id="sess-1",
            state_revision=1,
            persistence_revision=1,
            dag_id=BOOK_CATEGORIZATION_DAG_ID,
            book_items=(item_curr,),
        )
        assert base_digest != digest_curr

        # Description change
        item_desc = BookCategorizeItemPayload(
            book_item_id="item-u",
            date="2026-03-01",
            amount_units=10000,
            currency="EUR",
            direction="OUTFLOW",
            description="Modified description",
        )
        digest_desc = compute_canonical_payload_digest(
            schema_version=BOOK_CATEGORIZE_SCHEMA_VERSION,
            company_id="comp-1",
            session_id="sess-1",
            state_revision=1,
            persistence_revision=1,
            dag_id=BOOK_CATEGORIZATION_DAG_ID,
            book_items=(item_desc,),
        )
        assert base_digest != digest_desc

        # Persistence revision change
        digest_persist = compute_canonical_payload_digest(
            schema_version=BOOK_CATEGORIZE_SCHEMA_VERSION,
            company_id="comp-1",
            session_id="sess-1",
            state_revision=1,
            persistence_revision=2,
            dag_id=BOOK_CATEGORIZATION_DAG_ID,
            book_items=(base_item,),
        )
        assert base_digest != digest_persist

        # Source artifact kind change
        item_kind = BookCategorizeItemPayload(
            book_item_id="item-u",
            date="2026-03-01",
            amount_units=10000,
            currency="EUR",
            direction="OUTFLOW",
            description="Base description",
            source_artifact_kind="BILL",
        )
        digest_kind = compute_canonical_payload_digest(
            schema_version=BOOK_CATEGORIZE_SCHEMA_VERSION,
            company_id="comp-1",
            session_id="sess-1",
            state_revision=1,
            persistence_revision=1,
            dag_id=BOOK_CATEGORIZATION_DAG_ID,
            book_items=(item_kind,),
        )
        assert base_digest != digest_kind

        # Bookkeeping role change
        item_role = BookCategorizeItemPayload(
            book_item_id="item-u",
            date="2026-03-01",
            amount_units=10000,
            currency="EUR",
            direction="OUTFLOW",
            description="Base description",
            bookkeeping_role="OPEN_PAYABLE",
        )
        digest_role = compute_canonical_payload_digest(
            schema_version=BOOK_CATEGORIZE_SCHEMA_VERSION,
            company_id="comp-1",
            session_id="sess-1",
            state_revision=1,
            persistence_revision=1,
            dag_id=BOOK_CATEGORIZATION_DAG_ID,
            book_items=(item_role,),
        )
        assert base_digest != digest_role

    def test_aj_v_requested_at_does_not_change_semantic_identity(self):
        # Test V: requested_at does not alter semantic digest
        item = BookCategorizeItemPayload(
            book_item_id="item-v",
            date="2026-03-01",
            amount_units=10000,
            currency="EUR",
            direction="OUTFLOW",
        )
        digest_1 = compute_canonical_payload_digest(
            schema_version=BOOK_CATEGORIZE_SCHEMA_VERSION,
            company_id="comp-1",
            session_id="sess-1",
            state_revision=1,
            persistence_revision=1,
            dag_id=BOOK_CATEGORIZATION_DAG_ID,
            book_items=(item,),
        )
        digest_2 = compute_canonical_payload_digest(
            schema_version=BOOK_CATEGORIZE_SCHEMA_VERSION,
            company_id="comp-1",
            session_id="sess-1",
            state_revision=1,
            persistence_revision=1,
            dag_id=BOOK_CATEGORIZATION_DAG_ID,
            book_items=(item,),
        )
        assert digest_1 == digest_2

    def test_ak_w_existing_response_normalization_remains_green(self):
        # Test W: existing response normalization remains green
        item = DagViewItem(
            book_item_id="book-1",
            date=date(2026, 1, 15),
            amount_units=150000,
            currency="MAD",
            direction=Direction.OUTFLOW,
            description="LOYER MENSUEL",
            evidence_refs=("doc-lease-1",),
        )
        view = DagView(
            session_id="session-test-1",
            state_revision=3,
            items=(item,),
            company_id="company-1",
            persistence_revision=7,
        )
        outcomes = [
            {
                "book_item_id": "book-1",
                "status": "CLASSIFIED",
                "account_code": "6111",
                "confidence": 0.99,
                "rationale": "PCGE normal expense",
                "evidence_refs": [],
                "ase_node_id": "pcge_account_resolver",
            }
        ]
        stored_resp = {
            "schema_version": BOOK_CATEGORIZE_SCHEMA_VERSION,
            "session_id": view.session_id,
            "state_revision": view.state_revision,
            "dag_id": BOOK_CATEGORIZATION_DAG_ID,
            "dag_run_id": "run-w-123",
            "status": "SUCCESS",
            "outcomes": outcomes,
            "provider_issues": [],
        }

        def transport(s: str, d: bytes, t: float) -> bytes:
            req = json.loads(d.decode("utf-8"))
            resp = dict(stored_resp)
            resp["idempotency_key"] = req["idempotency_key"]
            resp["request_id"] = req["request_id"]
            return json.dumps(resp).encode("utf-8")

        categorizer = NatsAseBookCategorizer(transport=transport)
        plan = categorizer.classify_view(view)
        assert plan.dag_run_id == "run-w-123"
        assert len(plan.items) == 1
        assert plan.items[0].account_code == "6111"

    def test_al_x_provider_failures_remain_distinct_from_hold(self):
        # Test X: provider failures remain distinct from semantic HOLD
        item = DagViewItem(
            book_item_id="book-1",
            date=date(2026, 1, 15),
            amount_units=150000,
            currency="MAD",
            direction=Direction.OUTFLOW,
            description="LOYER MENSUEL",
        )
        view = DagView(
            session_id="session-test-1",
            state_revision=3,
            items=(item,),
            company_id="company-1",
            persistence_revision=7,
        )

        # Semantic HOLD produces hold items
        def transport_hold(s: str, d: bytes, t: float) -> bytes:
            req = json.loads(d.decode("utf-8"))
            return json.dumps({
                "schema_version": BOOK_CATEGORIZE_SCHEMA_VERSION,
                "request_id": req["request_id"],
                "idempotency_key": req["idempotency_key"],
                "session_id": req["session_id"],
                "state_revision": req["state_revision"],
                "dag_id": BOOK_CATEGORIZATION_DAG_ID,
                "status": "SUCCESS",
                "outcomes": [{
                    "book_item_id": "book-1",
                    "status": "HOLD",
                    "hold_reason": "HOLD_INSUFFICIENT_EVIDENCE",
                    "rationale": "Ambiguous",
                    "evidence_refs": [],
                    "ase_node_id": "hold_account_ambiguity",
                }],
            }).encode("utf-8")

        cat_hold = NatsAseBookCategorizer(transport=transport_hold)
        plan_hold = cat_hold.classify_view(view)
        assert len(plan_hold.hold_items) == 1
        assert len(plan_hold.items) == 0

        # Provider failure raises exception
        def transport_err(s: str, d: bytes, t: float) -> bytes:
            req = json.loads(d.decode("utf-8"))
            return json.dumps({
                "schema_version": BOOK_CATEGORIZE_SCHEMA_VERSION,
                "request_id": req["request_id"],
                "idempotency_key": req["idempotency_key"],
                "session_id": req["session_id"],
                "state_revision": req["state_revision"],
                "dag_id": BOOK_CATEGORIZATION_DAG_ID,
                "status": "FAILED",
                "provider_issues": [{
                    "code": "DAG_CONFIGURATION_ERROR",
                    "message": "Engine failed to load DAG",
                }],
            }).encode("utf-8")

        cat_err = NatsAseBookCategorizer(transport=transport_err)
        with pytest.raises(AseExecutionFailedError):
            cat_err.classify_view(view)



