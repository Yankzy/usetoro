from __future__ import annotations

from datetime import date, datetime, timezone
from decimal import Decimal
from typing import Any, Callable, Sequence
import uuid

from django.test import TestCase

from bookkeeping_state.bank_categorization.transport_models import (
    BANK_CATEGORIZATION_DAG_ID,
    BANK_CATEGORIZE_SCHEMA_VERSION,
    BankCategorizeResponseEnvelope,
    BankOutcomePayload,
)
from bookkeeping_state.bank_categorization.view import (
    ResidualBankCategorizationView,
)
from bookkeeping_state.dag.errors import AseTransportError
from bookkeeping_state.domain.commands import (
    InvalidateResidualBankClassificationCommand,
    PostResidualBankClassificationCommand,
    RecordResidualBankClassificationCommand,
)
from bookkeeping_state.domain.enums import Direction
from bookkeeping_state.domain.residual_bank_classifications import (
    ResidualBankClassificationStatus,
)
from bookkeeping_state.hydration.hydrator import BookkeepingHydrator
from bookkeeping_state.payment_application.service import PaymentApplicationService
from bookkeeping_state.reconciliation.service import ReconciliationService
from bookkeeping_state.routing.scorer import ZeroRoutingSemanticScoreProvider
from bookkeeping_state.routing.service import RoutingService
from bookkeeping_state.session.bookkeeping_session import BookkeepingSession
from bookkeeping_state.session.result import (
    FailureStage,
    SessionResult,
    SessionStageStatus,
)
from bookkeeping_state.state.queries import BookkeepingQueries
from bookkeeping_state.transitions.batch import (
    BatchTransitionResult,
    TransitionBatch,
)
from bookkeeping_state.transitions.engine import TransitionEngine
from bookkeeping_state.transitions.result import (
    RejectionCode,
    TransitionRejection,
    TransitionResult,
    TransitionStatus,
)
from ledger.models.bookkeeping import (
    BookkeepingResidualBankClassificationDecision,
    BookkeepingResidualBankClassificationInvalidation,
    BookkeepingResidualBankPosting,
)
from ledger.models.data_import import StagedTransactionModel
from ledger.tests.test_bookkeeping_session_stage1_integration import (
    StubAseClassifier,
)
from ledger.tests.test_stage1_accounting_execution import (
    Stage1AccountingExecutionTestBase,
)


class MockResidualBankCategorizer:
    """
    Flexible mock residual bank categorizer for testing session orchestration.
    Can be configured with deterministic outcomes or a callback.
    """

    def __init__(
        self,
        *,
        outcomes_map: dict[str, tuple[str, str | None, str | None]] | None = None,
        default_status: str = "CLASSIFIED",
        default_account_code: str = "5010",
        default_hold_reason: str | None = None,
        envelope_transformer: Callable[[BankCategorizeResponseEnvelope], BankCategorizeResponseEnvelope] | None = None,
        side_effect: Exception | None = None,
    ) -> None:
        self.outcomes_map = outcomes_map or {}
        self.default_status = default_status
        self.default_account_code = default_account_code
        self.default_hold_reason = default_hold_reason
        self.envelope_transformer = envelope_transformer
        self.side_effect = side_effect
        self.call_count = 0
        self.last_view: ResidualBankCategorizationView | None = None

    def categorize_view(
        self,
        view: ResidualBankCategorizationView,
    ) -> BankCategorizeResponseEnvelope:
        self.call_count += 1
        self.last_view = view

        if self.side_effect is not None:
            raise self.side_effect

        outcomes: list[BankOutcomePayload] = []
        for item in view.items:
            if item.bank_item_id in self.outcomes_map:
                status, acct, hold_reason = self.outcomes_map[item.bank_item_id]
            else:
                status = self.default_status
                acct = self.default_account_code if status == "CLASSIFIED" else None
                hold_reason = self.default_hold_reason or ("Hold reason" if status == "HOLD" else None)

            outcomes.append(
                BankOutcomePayload(
                    bank_item_id=item.bank_item_id,
                    status=status,
                    account_code=acct,
                    confidence=0.99 if status == "CLASSIFIED" else None,
                    rationale=f"Mock categorized as {status}",
                    required_evidence=(),
                    hold_reason=hold_reason,
                )
            )

        envelope = BankCategorizeResponseEnvelope(
            schema_version=BANK_CATEGORIZE_SCHEMA_VERSION,
            request_id=f"req-{uuid.uuid4().hex[:8]}",
            idempotency_key=f"idem-{uuid.uuid4().hex[:8]}",
            session_id=view.session_id,
            state_revision=view.state_revision,
            dag_id=BANK_CATEGORIZATION_DAG_ID,
            status="COMPLETED",
            outcomes=tuple(outcomes),
        )

        if self.envelope_transformer is not None:
            envelope = self.envelope_transformer(envelope)

        return envelope


class BookkeepingSessionResidualBankIntegrationTests(Stage1AccountingExecutionTestBase):
    """
    Comprehensive integration test suite for Task 27B:
    BookkeepingSession residual-bank semantic + posting path.
    Tests J through AL.
    """

    def setUp(self) -> None:
        super().setUp()
        self.routing_service = RoutingService(
            semantic_provider=ZeroRoutingSemanticScoreProvider()
        )
        self.dag_classifier = StubAseClassifier(
            default_account_code=self.revenue_account.code
        )
        self.reconciliation_service = ReconciliationService()
        self.payment_application_service = PaymentApplicationService()
        self.expense_account.active = True
        self.expense_account.save(update_fields=["active"])

    def _build_session(
        self,
        session_id: str,
        *,
        categorizer: Any = None,
        engine: Any = None,
        hydrator: Any = None,
    ) -> tuple[BookkeepingSession, Any]:
        h = hydrator or self.hydrator
        e = engine or self.engine
        state = h.hydrate(
            company_id=str(self.entity.uuid),
            session_id=session_id,
        )
        cat = categorizer or MockResidualBankCategorizer(default_account_code=self.expense_account.code)
        session = BookkeepingSession(
            state=state,
            engine=e,
            routing_service=self.routing_service,
            dag_classifier=self.dag_classifier,
            reconciliation_service=self.reconciliation_service,
            payment_application_service=self.payment_application_service,
            residual_bank_categorizer=cat,
            hydrator=h,
        )
        return session, state

    # ==================================================================
    # RESPONSE COMPLETENESS (Tests J, K, L, M)
    # ==================================================================

    def test_j_exact_response_item_set_accepted(self) -> None:
        """Test J: Exact response item set is accepted and persisted."""
        stx = self._create_staged_movement(amount=Decimal("150.00"), dt=date(2026, 4, 1))
        categorizer = MockResidualBankCategorizer(default_status="HOLD", default_hold_reason="Need info")

        session, state = self._build_session("session-j", categorizer=categorizer)
        res = session.run()

        self.assertTrue(res.is_success)
        self.assertEqual(res.residual_evaluation_count, 1)
        self.assertEqual(res.residual_hold_count, 1)
        self.assertEqual(res.unresolved_residual_hold_count, 1)
        self.assertEqual(categorizer.call_count, 1)

    def test_k_missing_response_outcome_fails_semantic_stage(self) -> None:
        """Test K: Missing response outcome fails semantic stage with zero decisions persisted."""
        stx1 = self._create_staged_movement(amount=Decimal("100.00"), dt=date(2026, 4, 1))
        stx2 = self._create_staged_movement(amount=Decimal("200.00"), dt=date(2026, 4, 2))

        # Transformer removes stx2 from response
        def drop_second(env: BankCategorizeResponseEnvelope) -> BankCategorizeResponseEnvelope:
            kept = tuple(o for o in env.outcomes if o.bank_item_id == f"staged:{stx1.uuid}")
            return BankCategorizeResponseEnvelope(
                schema_version=env.schema_version,
                request_id=env.request_id,
                idempotency_key=env.idempotency_key,
                session_id=env.session_id,
                state_revision=env.state_revision,
                dag_id=env.dag_id,
                status=env.status,
                outcomes=kept,
            )

        categorizer = MockResidualBankCategorizer(envelope_transformer=drop_second)
        session, state = self._build_session("session-k", categorizer=categorizer)
        res = session.run()

        self.assertFalse(res.is_success)
        self.assertEqual(res.failure_stage, FailureStage.RESIDUAL_CATEGORIZATION)
        self.assertIn("missing", str(res.failure_reason).lower())
        self.assertIn(f"staged:{stx2.uuid}", str(res.failure_reason))

        # Zero classification decisions persisted in DB
        self.assertEqual(BookkeepingResidualBankClassificationDecision.objects.filter(entity=self.entity).count(), 0)
        self.assertEqual(res.residual_posting_count, 0)

    def test_l_extra_response_outcome_fails_semantic_stage(self) -> None:
        """Test L: Extra response outcome fails semantic stage with zero decisions persisted."""
        stx1 = self._create_staged_movement(amount=Decimal("100.00"), dt=date(2026, 4, 1))
        extra_id = f"staged:{uuid.uuid4()}"

        def add_extra(env: BankCategorizeResponseEnvelope) -> BankCategorizeResponseEnvelope:
            extra_outcome = BankOutcomePayload(
                bank_item_id=extra_id,
                status="HOLD",
                hold_reason="Extra item",
            )
            return BankCategorizeResponseEnvelope(
                schema_version=env.schema_version,
                request_id=env.request_id,
                idempotency_key=env.idempotency_key,
                session_id=env.session_id,
                state_revision=env.state_revision,
                dag_id=env.dag_id,
                status=env.status,
                outcomes=env.outcomes + (extra_outcome,),
            )

        categorizer = MockResidualBankCategorizer(envelope_transformer=add_extra)
        session, state = self._build_session("session-l", categorizer=categorizer)
        res = session.run()

        self.assertFalse(res.is_success)
        self.assertEqual(res.failure_stage, FailureStage.RESIDUAL_CATEGORIZATION)
        self.assertIn("extra", str(res.failure_reason).lower())
        self.assertIn(extra_id, str(res.failure_reason))

        # Zero decisions persisted
        self.assertEqual(BookkeepingResidualBankClassificationDecision.objects.filter(entity=self.entity).count(), 0)

    def test_m_duplicate_outcome_rejected_by_envelope_validation(self) -> None:
        """Test M: Duplicate outcome ID in response envelope is rejected."""
        stx = self._create_staged_movement(amount=Decimal("100.00"), dt=date(2026, 4, 1))

        # Transformer duplicates outcome
        def duplicate_outcome(env: BankCategorizeResponseEnvelope) -> BankCategorizeResponseEnvelope:
            # Construct dictionary with duplicate outcomes and pass to from_dict
            env_dict = env.model_dump()
            env_dict["outcomes"] = list(env_dict["outcomes"]) + list(env_dict["outcomes"])
            # from_dict runs validation which catches duplicate outcome ID
            return BankCategorizeResponseEnvelope.from_dict(env_dict)

        categorizer = MockResidualBankCategorizer(envelope_transformer=duplicate_outcome)
        session, state = self._build_session("session-m", categorizer=categorizer)
        res = session.run()

        self.assertFalse(res.is_success)
        self.assertEqual(res.failure_stage, FailureStage.RESIDUAL_CATEGORIZATION)
        self.assertIn("duplicate", str(res.failure_reason).lower())
        self.assertEqual(BookkeepingResidualBankClassificationDecision.objects.filter(entity=self.entity).count(), 0)

    # ==================================================================
    # MULTI-OUTCOME TRANSITION BATCH CONTRACT (Tests N, O, P, Q)
    # ==================================================================

    def test_n_o_p_multi_outcome_transition_batch_and_revision_contract(self) -> None:
        """
        Tests N, O, P:
        N: 2 outcomes persist in one TransitionBatch.
        O: All sibling commands use same pre-batch R/P.
        P: All durable decisions receive R+1 / P+1.
        """
        stx1 = self._create_staged_movement(amount=Decimal("100.00"), dt=date(2026, 4, 1))
        stx2 = self._create_staged_movement(amount=Decimal("200.00"), dt=date(2026, 4, 2))

        categorizer = MockResidualBankCategorizer(
            default_status="HOLD",
            default_hold_reason="Hold test",
        )

        session, state = self._build_session("session-nop", categorizer=categorizer)
        pre_state_rev = state.revision
        pre_p_rev = state.persistence_revision

        res = session.run()

        self.assertTrue(res.is_success)
        self.assertEqual(res.residual_hold_count, 2)
        self.assertEqual(res.unresolved_residual_hold_count, 2)

        # Both decisions persisted in DB
        db_decisions = list(
            BookkeepingResidualBankClassificationDecision.objects.filter(entity=self.entity).order_by("created_at")
        )
        self.assertEqual(len(db_decisions), 2)

        # Both received R+1 and P+1 (Test P)
        for d in db_decisions:
            self.assertEqual(d.state_revision_at_decision, pre_state_rev + 1)
            self.assertEqual(d.persistence_revision_at_decision, pre_p_rev + 1)

    def test_q_one_invalid_outcome_entire_batch_rejects_zero_decisions(self) -> None:
        """Test Q: One invalid outcome in batch causes entire batch to reject (0 decisions persisted)."""
        stx1 = self._create_staged_movement(amount=Decimal("100.00"), dt=date(2026, 4, 1))
        stx2 = self._create_staged_movement(amount=Decimal("200.00"), dt=date(2026, 4, 2))

        # stx1 gets valid account, stx2 gets non-existent account code "9999"
        categorizer = MockResidualBankCategorizer(
            outcomes_map={
                f"staged:{stx1.uuid}": ("CLASSIFIED", self.expense_account.code, None),
                f"staged:{stx2.uuid}": ("CLASSIFIED", "9999", None),
            }
        )

        session, state = self._build_session("session-q", categorizer=categorizer)
        res = session.run()

        self.assertFalse(res.is_success)
        self.assertEqual(res.failure_stage, FailureStage.RESIDUAL_CATEGORIZATION)

        # Zero decisions persisted in DB
        self.assertEqual(BookkeepingResidualBankClassificationDecision.objects.filter(entity=self.entity).count(), 0)
        self.assertEqual(res.residual_posting_count, 0)

    # ==================================================================
    # SESSION / CRASH RECOVERY (Tests R, S, T, U, V, W)
    # ==================================================================

    def test_r_full_happy_path_classified_persisted_posted_rehydrated(self) -> None:
        """
        Test R: Full happy path:
        Stage 1 fixed point -> Stage 2 fixed point -> residual ASE ->
        CLASSIFIED persistence -> posting -> rehydrate -> final success.
        """
        stx = self._create_staged_movement(amount=Decimal("350.00"), dt=date(2026, 4, 5))
        categorizer = MockResidualBankCategorizer(
            default_status="CLASSIFIED",
            default_account_code=self.expense_account.code,
        )

        session, state = self._build_session("session-r", categorizer=categorizer)
        res = session.run()

        self.assertTrue(res.is_success, f"Session failed: stage={res.failure_stage}, reason={res.failure_reason}")
        self.assertIsNone(res.failure_stage)
        self.assertEqual(res.residual_evaluation_count, 1)
        self.assertEqual(res.residual_classified_count, 1)
        self.assertEqual(res.residual_posting_count, 1)
        self.assertEqual(res.unresolved_residual_hold_count, 0)
        self.assertEqual(len(res.executed_residual_posting_ids), 1)

        # Verify DB records
        posting_record = BookkeepingResidualBankPosting.objects.get(
            entity=self.entity,
            staged_transaction=stx,
        )
        self.assertIsNotNone(posting_record.journal_entry)
        self.assertTrue(posting_record.journal_entry.locked)

        # Hydrate fresh state to confirm economic residual is 0
        fresh_state = self.hydrator.hydrate(company_id=str(self.entity.uuid), session_id="verify-r")
        queries = BookkeepingQueries(fresh_state)
        self.assertEqual(queries.bank_remaining_units(f"staged:{stx.uuid}"), 0)
        self.assertEqual(len(queries.residual_unmatched_bank_items()), 0)

    def test_s_semantic_hold_completion_semantics(self) -> None:
        """Test S: Semantic HOLD persisted, 0 postings, session success, unresolved_hold_count=1."""
        stx = self._create_staged_movement(amount=Decimal("450.00"), dt=date(2026, 4, 5))
        categorizer = MockResidualBankCategorizer(
            default_status="HOLD",
            default_hold_reason="Missing receipt",
        )

        session, state = self._build_session("session-s", categorizer=categorizer)
        res = session.run()

        self.assertTrue(res.is_success)
        self.assertEqual(res.residual_evaluation_count, 1)
        self.assertEqual(res.residual_hold_count, 1)
        self.assertEqual(res.residual_posting_count, 0)
        self.assertEqual(res.unresolved_residual_hold_count, 1)
        self.assertEqual(BookkeepingResidualBankPosting.objects.filter(entity=self.entity).count(), 0)

    def test_t_restart_with_existing_unposted_classified_skips_ase_and_posts(self) -> None:
        """Test T: Restart with existing unposted CLASSIFIED decision: ASE is skipped, posting executes."""
        stx = self._create_staged_movement(amount=Decimal("-250.00"), dt=date(2026, 4, 5))

        # Manually create and commit CLASSIFIED decision (simulating process crash before posting)
        state_pre = self.hydrator.hydrate(company_id=str(self.entity.uuid), session_id="setup-t")
        record_cmd = RecordResidualBankClassificationCommand(
            command_id="cmd-setup-t",
            session_id=state_pre.session_id,
            expected_state_revision=state_pre.revision,
            expected_persistence_revision=state_pre.persistence_revision,
            decision_id=f"rbc:setup-t-{stx.uuid}",
            bank_item_id=f"staged:{stx.uuid}",
            bank_account_id=str(self.bank_account.uuid),
            status=ResidualBankClassificationStatus.CLASSIFIED,
            account_code=self.expense_account.code,
            confidence=0.99,
            rationale="Pre-existing classified",
            original_amount_units=250_0000,
            residual_amount_units=250_0000,
            direction=Direction.OUTFLOW,
            currency="USD",
            schema_version=BANK_CATEGORIZE_SCHEMA_VERSION,
            dag_id=BANK_CATEGORIZATION_DAG_ID,
            request_semantic_digest="digest-t",
            issued_at=datetime.now(timezone.utc),
        )
        apply_res = self.engine.apply(state=state_pre, command=record_cmd)
        self.assertTrue(apply_res.applied)

        # Now start session. Categorizer should NOT be called!
        categorizer = MockResidualBankCategorizer()
        session, state = self._build_session("session-t", categorizer=categorizer)
        res = session.run()

        self.assertTrue(res.is_success)
        self.assertEqual(categorizer.call_count, 0)  # ASE skipped!
        self.assertEqual(res.residual_evaluation_count, 0)
        self.assertEqual(res.residual_classified_count, 0)
        self.assertEqual(res.residual_posting_count, 1)  # Posted!

    def test_u_restart_with_existing_hold_skips_ase_and_posting(self) -> None:
        """Test U: Restart with existing HOLD: ASE skipped, posting skipped, no duplicate HOLD."""
        stx = self._create_staged_movement(amount=Decimal("-180.00"), dt=date(2026, 4, 5))

        state_pre = self.hydrator.hydrate(company_id=str(self.entity.uuid), session_id="setup-u")
        record_cmd = RecordResidualBankClassificationCommand(
            command_id="cmd-setup-u",
            session_id=state_pre.session_id,
            expected_state_revision=state_pre.revision,
            expected_persistence_revision=state_pre.persistence_revision,
            decision_id=f"rbc:setup-u-{stx.uuid}",
            bank_item_id=f"staged:{stx.uuid}",
            bank_account_id=str(self.bank_account.uuid),
            status=ResidualBankClassificationStatus.HOLD,
            hold_reason="Prior hold",
            original_amount_units=180_0000,
            residual_amount_units=180_0000,
            direction=Direction.OUTFLOW,
            currency="USD",
            schema_version=BANK_CATEGORIZE_SCHEMA_VERSION,
            dag_id=BANK_CATEGORIZATION_DAG_ID,
            request_semantic_digest="digest-u",
            issued_at=datetime.now(timezone.utc),
        )
        self.engine.apply(state=state_pre, command=record_cmd)

        initial_count = BookkeepingResidualBankClassificationDecision.objects.filter(entity=self.entity).count()
        self.assertEqual(initial_count, 1)

        categorizer = MockResidualBankCategorizer()
        session, state = self._build_session("session-u", categorizer=categorizer)
        res = session.run()

        self.assertTrue(res.is_success)
        self.assertEqual(categorizer.call_count, 0)
        self.assertEqual(res.residual_evaluation_count, 0)
        self.assertEqual(res.residual_posting_count, 0)
        self.assertEqual(res.unresolved_residual_hold_count, 1)

        # No duplicate decision created
        self.assertEqual(
            BookkeepingResidualBankClassificationDecision.objects.filter(entity=self.entity).count(),
            1,
        )

    def test_v_invalidated_tip_superseded_and_posted(self) -> None:
        """Test V: Invalidated unposted tip is evaluated, new decision supersedes it and posts."""
        stx = self._create_staged_movement(amount=Decimal("-220.00"), dt=date(2026, 4, 5))

        # 1. Create HOLD decision
        state_pre = self.hydrator.hydrate(company_id=str(self.entity.uuid), session_id="setup-v")
        dec_id = f"rbc:setup-v-{stx.uuid}"
        cmd1 = RecordResidualBankClassificationCommand(
            command_id="cmd-v-1",
            session_id=state_pre.session_id,
            expected_state_revision=state_pre.revision,
            expected_persistence_revision=state_pre.persistence_revision,
            decision_id=dec_id,
            bank_item_id=f"staged:{stx.uuid}",
            bank_account_id=str(self.bank_account.uuid),
            status=ResidualBankClassificationStatus.HOLD,
            hold_reason="Initial hold",
            original_amount_units=220_0000,
            residual_amount_units=220_0000,
            direction=Direction.OUTFLOW,
            currency="USD",
            schema_version=BANK_CATEGORIZE_SCHEMA_VERSION,
            dag_id=BANK_CATEGORIZATION_DAG_ID,
            request_semantic_digest="digest-v-1",
            issued_at=datetime.now(timezone.utc),
        )
        self.engine.apply(state=state_pre, command=cmd1)

        # 2. Invalidate decision via engine command with fresh rehydration
        state_inval = self.hydrator.hydrate(company_id=str(self.entity.uuid), session_id="setup-v-inv")
        inval_cmd = InvalidateResidualBankClassificationCommand(
            command_id="cmd-v-inv",
            session_id=state_inval.session_id,
            expected_state_revision=state_inval.revision,
            expected_persistence_revision=state_inval.persistence_revision,
            invalidation_id=f"inv-v-{uuid.uuid4().hex[:6]}",
            decision_id=dec_id,
            reason="User provided receipt",
            issued_at=datetime.now(timezone.utc),
        )
        inval_res = self.engine.apply(state=state_inval, command=inval_cmd)
        self.assertTrue(inval_res.applied)

        # 3. Now session runs: ASE is called, supersedes invalidated tip, posts CLASSIFIED
        categorizer = MockResidualBankCategorizer(
            default_status="CLASSIFIED",
            default_account_code=self.expense_account.code,
        )
        session, state = self._build_session("session-v", categorizer=categorizer)
        res = session.run()

        self.assertTrue(res.is_success)
        self.assertEqual(res.residual_evaluation_count, 1)
        self.assertEqual(res.residual_classified_count, 1)
        self.assertEqual(res.residual_posting_count, 1)

        # Check DB decision has supersedes_id set to dec_id
        new_dec = BookkeepingResidualBankClassificationDecision.objects.get(
            entity=self.entity,
            supersedes_id=dec_id,
        )
        self.assertEqual(new_dec.status, "CLASSIFIED")

    def test_w_mixed_batch_one_classified_one_hold(self) -> None:
        """Test W: Mixed batch (1 CLASSIFIED, 1 HOLD): both persist atomically, CLASSIFIED posts, HOLD remains."""
        stx1 = self._create_staged_movement(amount=Decimal("100.00"), dt=date(2026, 4, 1))
        stx2 = self._create_staged_movement(amount=Decimal("200.00"), dt=date(2026, 4, 2))

        categorizer = MockResidualBankCategorizer(
            outcomes_map={
                f"staged:{stx1.uuid}": ("CLASSIFIED", self.expense_account.code, None),
                f"staged:{stx2.uuid}": ("HOLD", None, "Awaiting documentation"),
            }
        )

        session, state = self._build_session("session-w", categorizer=categorizer)
        res = session.run()

        self.assertTrue(res.is_success)
        self.assertEqual(res.residual_evaluation_count, 2)
        self.assertEqual(res.residual_classified_count, 1)
        self.assertEqual(res.residual_hold_count, 1)
        self.assertEqual(res.residual_posting_count, 1)
        self.assertEqual(res.unresolved_residual_hold_count, 1)

        # Verify stx1 posted, stx2 not posted
        self.assertTrue(
            BookkeepingResidualBankPosting.objects.filter(
                entity=self.entity,
                staged_transaction=stx1,
            ).exists()
        )
        self.assertFalse(
            BookkeepingResidualBankPosting.objects.filter(
                entity=self.entity,
                staged_transaction=stx2,
            ).exists()
        )

    # ==================================================================
    # POSTING LOOP (Tests X, Y, Z, AA)
    # ==================================================================

    def test_x_two_unposted_classified_sequential_posting_with_rehydrate(self) -> None:
        """Test X: Two pre-existing CLASSIFIED residual decisions post sequentially with fresh rehydration."""
        stx1 = self._create_staged_movement(amount=Decimal("100.00"), dt=date(2026, 4, 1))
        stx2 = self._create_staged_movement(amount=Decimal("200.00"), dt=date(2026, 4, 2))

        categorizer = MockResidualBankCategorizer(
            default_status="CLASSIFIED",
            default_account_code=self.expense_account.code,
        )

        session, state = self._build_session("session-x", categorizer=categorizer)
        res = session.run()

        self.assertTrue(res.is_success)
        self.assertEqual(res.residual_posting_count, 2)
        self.assertEqual(res.rehydration_count, 2)
        self.assertEqual(len(res.executed_residual_posting_ids), 2)
        self.assertEqual(
            BookkeepingResidualBankPosting.objects.filter(entity=self.entity).count(),
            2,
        )

    def test_y_healthy_post_noop_progress_verification(self) -> None:
        """Test Y: Healthy NOOP proves posted + non-residual, increments noop count, terminates cleanly."""
        stx = self._create_staged_movement(amount=Decimal("150.00"), dt=date(2026, 4, 1))

        # First run posts the item
        session1, _ = self._build_session("session-y-1")
        res1 = session1.run()
        self.assertTrue(res1.is_success)
        self.assertEqual(res1.residual_posting_count, 1)

        # Now simulate a second engine apply returning NOOP when called
        class NoopPostingEngine:
            def __init__(self, real: Any) -> None:
                self.real = real

            @property
            def repository(self) -> Any:
                return self.real.repository

            def apply_batch(self, *args: Any, **kwargs: Any) -> Any:
                return self.real.apply_batch(*args, **kwargs)

            def apply(self, state: Any, command: Any) -> TransitionResult:
                if isinstance(command, PostResidualBankClassificationCommand):
                    return TransitionResult.noop_result(
                        command=command,
                        state_revision=state.revision,
                        persistence_revision=state.persistence_revision,
                    )
                return self.real.apply(state=state, command=command)

        session2, _ = self._build_session("session-y-2", engine=NoopPostingEngine(self.engine))
        res2 = session2.run()
        self.assertTrue(res2.is_success)
        # 0 postings, 0 residual candidates (item was already reconciled and non-residual)
        self.assertEqual(res2.residual_posting_count, 0)

    def test_z_unhealthy_post_noop_fails_loop_invariant(self) -> None:
        """Test Z: Unhealthy NOOP with unposted decision fails immediately with LOOP_INVARIANT."""
        stx = self._create_staged_movement(amount=Decimal("150.00"), dt=date(2026, 4, 1))

        class FakeNoopEngine:
            def __init__(self, real: Any) -> None:
                self.real = real

            @property
            def repository(self) -> Any:
                return self.real.repository

            def apply_batch(self, *args: Any, **kwargs: Any) -> Any:
                return self.real.apply_batch(*args, **kwargs)

            def apply(self, state: Any, command: Any) -> TransitionResult:
                if isinstance(command, PostResidualBankClassificationCommand):
                    return TransitionResult.noop_result(
                        command=command,
                        state_revision=state.revision,
                        persistence_revision=state.persistence_revision,
                    )
                return self.real.apply(state=state, command=command)

        session, state = self._build_session("session-z", engine=FakeNoopEngine(self.engine))
        res = session.run()

        self.assertFalse(res.is_success)
        self.assertEqual(res.failure_stage, FailureStage.LOOP_INVARIANT)
        self.assertIn("not durably posted", str(res.failure_reason))

    def test_aa_posted_decision_still_residual_fails_loop_invariant_case_e(self) -> None:
        """Test AA: Bank item still residual despite posted decision triggers Case E LOOP_INVARIANT."""
        stx = self._create_staged_movement(amount=Decimal("150.00"), dt=date(2026, 4, 1))

        # First run posts the residual cleanly
        categorizer = MockResidualBankCategorizer(
            default_status="CLASSIFIED",
            default_account_code=self.expense_account.code,
        )
        session1, _ = self._build_session("session-aa-1", categorizer=categorizer)
        res1 = session1.run()
        self.assertTrue(res1.is_success)

        # Corrupt state: invalidate the reconciliation so bank item appears residual again while decision remains posted:
        from ledger.models.bookkeeping import (
            BookkeepingReconciliation,
            BookkeepingReconciliationInvalidation,
        )
        rec = BookkeepingReconciliation.objects.filter(entity=self.entity).first()
        assert rec is not None
        BookkeepingReconciliationInvalidation.objects.create(
            id=f"rec_inv_{uuid.uuid4().hex[:8]}",
            reconciliation=rec,
            reason="Simulated reconciliation invalidation causing Case E corruption",
            session_id="corrupt-session",
            state_revision_at_invalidation=1,
            created_at=datetime.now(timezone.utc),
        )

        # Run session 2: must detect Case E corruption and fail with LOOP_INVARIANT!
        categorizer2 = MockResidualBankCategorizer()
        session2, _ = self._build_session("session-aa-2", categorizer=categorizer2)
        res2 = session2.run()

        self.assertFalse(res2.is_success)
        self.assertEqual(res2.failure_stage, FailureStage.LOOP_INVARIANT)
        self.assertIn("Case E", str(res2.failure_reason))
        self.assertEqual(categorizer2.call_count, 0)  # Zero ASE calls

    # ==================================================================
    # FAILURES (Tests AB, AC, AD, AE)
    # ==================================================================

    def test_ab_ase_transport_failure_preserves_stage1_2_truth(self) -> None:
        """Test AB: ASE transport failure returns RESIDUAL_CATEGORIZATION and preserves Stage 1/2 truth."""
        # Stage 1 applies payment against invoice
        stx_stage1 = self._create_staged_movement(amount=Decimal("500.00"), dt=date(2026, 4, 1))
        inv = self._create_approved_invoice(amount=Decimal("500.00"), dt=date(2026, 4, 1))

        # Staged item for residual path
        stx_res = self._create_staged_movement(amount=Decimal("100.00"), dt=date(2026, 4, 2))

        categorizer = MockResidualBankCategorizer(
            side_effect=AseTransportError("NATS connection timeout"),
        )

        session, state = self._build_session("session-ab", categorizer=categorizer)
        res = session.run()

        self.assertFalse(res.is_success)
        self.assertEqual(res.failure_stage, FailureStage.RESIDUAL_CATEGORIZATION)
        self.assertIn("NATS connection timeout", str(res.failure_reason))

        # Stage 1 truth IS preserved!
        inv.refresh_from_db()
        self.assertEqual(inv.amount_due - inv.amount_paid, Decimal("0.00"))
        self.assertEqual(len(res.executed_payment_application_ids), 1)

        # Zero residual decisions or postings persisted
        self.assertEqual(BookkeepingResidualBankClassificationDecision.objects.filter(entity=self.entity).count(), 0)
        self.assertEqual(BookkeepingResidualBankPosting.objects.filter(entity=self.entity).count(), 0)

    def test_ac_stage2_failure_prevents_residual_stage(self) -> None:
        """Test AC: Stage 2 failure prevents residual stage from running."""
        stx = self._create_staged_movement(amount=Decimal("300.00"), dt=date(2026, 4, 5))
        tx = self._create_cash_tx(amount=Decimal("300.00"), is_debit=True, dt=date(2026, 4, 5))
        tx.journal_entry.je_number = "REF-AC"
        tx.journal_entry.save(update_fields=["je_number"])
        stx.fit_id = "REF-AC"
        stx.save()

        # Another unmatched item
        stx_unmatched = self._create_staged_movement(amount=Decimal("100.00"), dt=date(2026, 4, 6))

        class FailingReconEngine:
            def __init__(self, real: Any) -> None:
                self.real = real

            @property
            def repository(self) -> Any:
                return self.real.repository

            def apply(self, *args: Any, **kwargs: Any) -> Any:
                return self.real.apply(*args, **kwargs)

            def apply_batch(self, state: Any, batch: Any) -> Any:
                # Fail any reconciliation batch
                if any(c.command_type.value == "CREATE_RECONCILIATION" for c in batch.commands):
                    return BatchTransitionResult.rejected(
                        batch=batch,
                        state=state,
                        code=RejectionCode.CAPACITY_EXCEEDED,
                        message="Simulated recon failure",
                    )
                return self.real.apply_batch(state=state, batch=batch)

        categorizer = MockResidualBankCategorizer()
        session, state = self._build_session(
            "session-ac",
            categorizer=categorizer,
            engine=FailingReconEngine(self.engine),
        )
        res = session.run()

        self.assertFalse(res.is_success)
        self.assertEqual(res.failure_stage, FailureStage.RECONCILIATION)
        self.assertEqual(categorizer.call_count, 0)  # Zero residual ASE calls!

    def test_ad_classification_batch_rejection_terminates_at_residual_categorization(self) -> None:
        """Test AD: Rejection of classification TransitionBatch terminates session without postings."""
        stx = self._create_staged_movement(amount=Decimal("150.00"), dt=date(2026, 4, 1))

        class FailingClassificationBatchEngine:
            def __init__(self, real: Any) -> None:
                self.real = real

            @property
            def repository(self) -> Any:
                return self.real.repository

            def apply(self, *args: Any, **kwargs: Any) -> Any:
                return self.real.apply(*args, **kwargs)

            def apply_batch(self, state: Any, batch: Any) -> Any:
                if any(c.command_type.value == "RECORD_RESIDUAL_BANK_CLASSIFICATION" for c in batch.commands):
                    return BatchTransitionResult.rejected(
                        batch=batch,
                        state=state,
                        code=RejectionCode.PERSISTENCE_REVISION_CONFLICT,
                        message="Simulated batch rejection",
                    )
                return self.real.apply_batch(state=state, batch=batch)

        session, state = self._build_session(
            "session-ad",
            engine=FailingClassificationBatchEngine(self.engine),
        )
        res = session.run()

        self.assertFalse(res.is_success)
        self.assertEqual(res.failure_stage, FailureStage.RESIDUAL_CATEGORIZATION)
        self.assertEqual(res.residual_posting_count, 0)

    def test_ae_posting_rejection_terminates_at_residual_posting(self) -> None:
        """Test AE: Rejection of PostResidualBankClassificationCommand terminates session at RESIDUAL_POSTING."""
        stx = self._create_staged_movement(amount=Decimal("150.00"), dt=date(2026, 4, 1))

        class FailingPostingEngine:
            def __init__(self, real: Any) -> None:
                self.real = real

            @property
            def repository(self) -> Any:
                return self.real.repository

            def apply_batch(self, *args: Any, **kwargs: Any) -> Any:
                return self.real.apply_batch(*args, **kwargs)

            def apply(self, state: Any, command: Any) -> TransitionResult:
                if isinstance(command, PostResidualBankClassificationCommand):
                    return TransitionResult.rejected_result(
                        command=command,
                        rejection=TransitionRejection(
                            code=RejectionCode.CAPACITY_EXCEEDED,
                            message="Posting capacity rejection test",
                        ),
                        state_revision=state.revision,
                        persistence_revision=state.persistence_revision,
                    )
                return self.real.apply(state=state, command=command)

        session, state = self._build_session(
            "session-ae",
            engine=FailingPostingEngine(self.engine),
        )
        res = session.run()

        self.assertFalse(res.is_success)
        self.assertEqual(res.failure_stage, FailureStage.RESIDUAL_POSTING)
        self.assertIn("Posting capacity rejection test", str(res.failure_reason))

    # ==================================================================
    # IDEMPOTENCY / RERUN (Tests AF through AK)
    # ==================================================================

    def test_af_through_ak_rerun_idempotency_no_duplicates(self) -> None:
        """
        Tests AF-AK: Complete idempotency audit.
        Rerunning session after full completion produces zero duplicate
        payments, reconciliations, classifications, or journal entries.
        """
        stx1 = self._create_staged_movement(amount=Decimal("400.00"), dt=date(2026, 4, 1), name="Stage 1 Item")
        inv = self._create_approved_invoice(amount=Decimal("400.00"), dt=date(2026, 4, 1))

        stx2 = self._create_staged_movement(amount=Decimal("200.00"), dt=date(2026, 4, 2), name="Residual Item")

        categorizer = MockResidualBankCategorizer(
            default_status="CLASSIFIED",
            default_account_code=self.expense_account.code,
        )

        # Run 1: Applies payment, classifies residual, posts residual
        session1, _ = self._build_session("session-idem-1", categorizer=categorizer)
        res1 = session1.run()
        self.assertTrue(res1.is_success)
        self.assertEqual(res1.residual_posting_count, 1)

        db_decisions_1 = BookkeepingResidualBankClassificationDecision.objects.filter(entity=self.entity).count()
        db_postings_1 = BookkeepingResidualBankPosting.objects.filter(entity=self.entity).count()
        from ledger.models import JournalEntryModel
        je_count_1 = JournalEntryModel.objects.filter(ledger__entity=self.entity).count()

        # Run 2: Re-run from fresh state
        categorizer2 = MockResidualBankCategorizer()
        session2, _ = self._build_session("session-idem-2", categorizer=categorizer2)
        res2 = session2.run()

        self.assertTrue(res2.is_success)
        self.assertEqual(categorizer2.call_count, 0)
        self.assertEqual(res2.residual_posting_count, 0)
        self.assertEqual(len(res2.executed_payment_application_ids), 0)

        db_decisions_2 = BookkeepingResidualBankClassificationDecision.objects.filter(entity=self.entity).count()
        db_postings_2 = BookkeepingResidualBankPosting.objects.filter(entity=self.entity).count()
        je_count_2 = JournalEntryModel.objects.filter(ledger__entity=self.entity).count()

        self.assertEqual(db_decisions_1, db_decisions_2)
        self.assertEqual(db_postings_1, db_postings_2)
        self.assertEqual(je_count_1, je_count_2)

    # ==================================================================
    # MIXED END-TO-END (Test AL)
    # ==================================================================

    def test_al_mixed_end_to_end_all_four_paths_in_one_session(self) -> None:
        """
        Test AL: One session containing:
        - 1 bank movement applied in Stage 1 against open invoice
        - 1 separate bank movement reconciled in Stage 2 against already-posted cash leg
        - 1 residual item CLASSIFIED and posted
        - 1 residual item HOLD
        Assert exact final durable truth and telemetry.
        """
        # 1. Stage 1 item ($300) pays invoice
        stx_s1 = self._create_staged_movement(amount=Decimal("300.00"), dt=date(2026, 4, 1), name="Stage 1 Bank")
        inv = self._create_approved_invoice(amount=Decimal("300.00"), dt=date(2026, 4, 1))

        # 2. Stage 2 item ($250) matches posted cash leg
        stx_s2 = self._create_staged_movement(amount=Decimal("250.00"), dt=date(2026, 4, 2), name="Stage 2 Bank")
        stx_s2.fit_id = "REF-AL-STAGE2"
        stx_s2.save()
        tx_s2 = self._create_cash_tx(amount=Decimal("250.00"), is_debit=True, dt=date(2026, 4, 2))
        tx_s2.journal_entry.je_number = "REF-AL-STAGE2"
        tx_s2.journal_entry.save(update_fields=["je_number"])

        # 3. Residual item 1 ($150) -> CLASSIFIED and posted
        stx_res_classified = self._create_staged_movement(
            amount=Decimal("150.00"), dt=date(2026, 4, 3), name="Residual Classified"
        )

        # 4. Residual item 2 ($75) -> HOLD
        stx_res_hold = self._create_staged_movement(
            amount=Decimal("75.00"), dt=date(2026, 4, 4), name="Residual Hold"
        )

        categorizer = MockResidualBankCategorizer(
            outcomes_map={
                f"staged:{stx_res_classified.uuid}": ("CLASSIFIED", self.expense_account.code, None),
                f"staged:{stx_res_hold.uuid}": ("HOLD", None, "Awaiting receipt"),
            }
        )

        session, state = self._build_session("session-al", categorizer=categorizer)
        res = session.run()

        # Assert full success
        self.assertTrue(res.is_success)
        self.assertIsNone(res.failure_stage)

        # Assert Stage 1 telemetry
        self.assertEqual(len(res.executed_payment_application_ids), 1)
        self.assertIsNotNone(res.payment_application_stage_result)
        assert res.payment_application_stage_result is not None
        self.assertEqual(res.payment_application_stage_result.status, SessionStageStatus.APPLIED)

        # Assert Stage 2 telemetry
        self.assertIsNotNone(res.reconciliation_stage_result)
        assert res.reconciliation_stage_result is not None
        self.assertIn(
            res.reconciliation_stage_result.status,
            {SessionStageStatus.APPLIED, SessionStageStatus.SKIPPED},
        )

        # Assert Residual semantic & posting telemetry
        self.assertEqual(res.residual_evaluation_count, 2)  # exactly 2 sent to ASE (neither s1 nor s2 leaked!)
        self.assertEqual(res.residual_classified_count, 1)
        self.assertEqual(res.residual_hold_count, 1)
        self.assertEqual(res.residual_posting_count, 1)
        self.assertEqual(res.unresolved_residual_hold_count, 1)
        self.assertEqual(len(res.executed_residual_posting_ids), 1)
        self.assertIsNotNone(res.residual_stage_result)
        assert res.residual_stage_result is not None
        self.assertEqual(res.residual_stage_result.status, SessionStageStatus.APPLIED)

        # Assert DB durable truth
        # Invoice is paid
        inv.refresh_from_db()
        self.assertEqual(inv.amount_due - inv.amount_paid, Decimal("0.00"))

        # Classified residual is posted
        self.assertTrue(
            BookkeepingResidualBankPosting.objects.filter(
                entity=self.entity,
                staged_transaction=stx_res_classified,
            ).exists()
        )

        # Hold residual remains unposted
        self.assertFalse(
            BookkeepingResidualBankPosting.objects.filter(
                entity=self.entity,
                staged_transaction=stx_res_hold,
            ).exists()
        )
        self.assertTrue(
            BookkeepingResidualBankClassificationDecision.objects.filter(
                entity=self.entity,
                staged_transaction=stx_res_hold,
                status="HOLD",
            ).exists()
        )
