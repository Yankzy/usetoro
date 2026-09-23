from __future__ import annotations

from datetime import date, datetime, timezone
from decimal import Decimal
import uuid

from django.test import TestCase

from bookkeeping_state.dag.models import (
    DagBatchPlan,
    DagClassificationItem,
)
from bookkeeping_state.dag.view import DagView
from bookkeeping_state.domain.commands import (
    ApplyPaymentCommand,
    CommandSource,
    CreateRoutingDecisionCommand,
    PaymentApplicationObligationAllocation,
    RoutingDecisionSource,
    mint_stage1_capability,
)
from bookkeeping_state.domain.enums import Direction
from bookkeeping_state.domain.evidence import BookItemEvidenceType, EvidenceSource
from bookkeeping_state.hydration.hydrator import BookkeepingHydrator
from bookkeeping_state.payment_application.coordinator import plan_two_stage_bookkeeping
from bookkeeping_state.payment_application.service import PaymentApplicationService
from bookkeeping_state.persistence.repository import (
    BookkeepingRepository,
    PersistenceError,
)
from bookkeeping_state.reconciliation.service import ReconciliationService
from bookkeeping_state.routing.scorer import ZeroRoutingSemanticScoreProvider
from bookkeeping_state.routing.service import RoutingService
from bookkeeping_state.session.bookkeeping_session import BookkeepingSession
from bookkeeping_state.state.fingerprint import (
    canonical_artifact_projection,
    state_fingerprint,
)
from bookkeeping_state.state.queries import BookkeepingQueries
from bookkeeping_state.transitions.engine import TransitionEngine
from bookkeeping_state.transitions.result import TransitionStatus
from ledger.models.bookkeeping import (
    BookkeepingClassificationDecision,
    BookkeepingClassificationInvalidation,
    BookkeepingEvidenceAssertion,
    BookkeepingEvidenceInvalidation,
    BookkeepingRoutingDecision,
    BookkeepingRoutingInvalidation,
)
from ledger.models.entity import EntityModel
from ledger.models.invoice import InvoiceModel
from ledger.tests.test_stage1_accounting_execution import (
    Stage1AccountingExecutionTestBase,
)


class StubAseClassifier:
    """Deterministic classifier for lifecycle tests."""

    def __init__(self, default_account_code: str = "4010") -> None:
        self.default_account_code = default_account_code

    def classify_view(
        self,
        view: DagView,
        *,
        only_unclassified: bool = True,
    ) -> DagBatchPlan:
        items = tuple(
            DagClassificationItem(
                book_item_id=v.book_item_id,
                account_code=self.default_account_code,
                confidence=1.0,
                rationale="Classified by StubAseClassifier",
            )
            for v in view.items
            if not (only_unclassified and v.is_classified)
        )
        return DagBatchPlan(
            plan_id=f"plan-{uuid.uuid4().hex[:6]}",
            session_id=view.session_id,
            expected_state_revision=view.state_revision,
            items=items,
            hold_items=(),
            dag_run_id=f"dag-run-{uuid.uuid4().hex[:6]}",
        )


class DurableHistoryLifecycleTests(Stage1AccountingExecutionTestBase):
    """
    Focused regression test suite verifying the frozen durable-history invariant:
    accepted durable bookkeeping truth must survive close -> fresh hydration even
    when target obligations clear and leave the active book_items projection.
    """

    def setUp(self) -> None:
        super().setUp()
        self.repository = BookkeepingRepository()
        self.hydrator = BookkeepingHydrator(repository=self.repository)

    def _create_routing_decision(self, **kwargs: Any) -> BookkeepingRoutingDecision:
        defaults = {
            "id": f"rd-{uuid.uuid4().hex[:12]}",
            "created_at": datetime.now(timezone.utc),
            "utility": 1000,
        }
        defaults.update(kwargs)
        return BookkeepingRoutingDecision.objects.create(**defaults)

    def _create_classification_decision(self, **kwargs: Any) -> BookkeepingClassificationDecision:
        defaults = {
            "id": f"cd-{uuid.uuid4().hex[:12]}",
            "created_at": datetime.now(timezone.utc),
            "confidence": 1.0,
        }
        defaults.update(kwargs)
        return BookkeepingClassificationDecision.objects.create(**defaults)

    def _create_evidence_assertion(self, **kwargs: Any) -> BookkeepingEvidenceAssertion:
        defaults = {
            "id": f"ea-{uuid.uuid4().hex[:12]}",
            "created_at": datetime.now(timezone.utc),
            "confidence": 1.0,
        }
        defaults.update(kwargs)
        return BookkeepingEvidenceAssertion.objects.create(**defaults)

    def _create_evidence_invalidation(self, **kwargs: Any) -> BookkeepingEvidenceInvalidation:
        defaults = {
            "id": f"ei-{uuid.uuid4().hex[:12]}",
            "created_at": datetime.now(timezone.utc),
        }
        defaults.update(kwargs)
        return BookkeepingEvidenceInvalidation.objects.create(**defaults)

    def _execute_payment(
        self,
        *,
        staged_tx: StagedTransactionModel,
        invoices: list[InvoiceModel] | None = None,
        bills: list[BillModel] | None = None,
        payment_amount: Decimal,
        payment_date: date,
        session_id: str,
    ) -> None:
        state = self.hydrator.hydrate(company_id=str(self.entity.uuid), session_id=session_id)
        try:
            bank_item_id = f"staged:{staged_tx.uuid}"
            direction = Direction.BANK_INFLOW if staged_tx.amount > 0 else Direction.BANK_OUTFLOW
            amount_units = int(abs(payment_amount) * 10000)

            allocations_list = []
            if invoices:
                for inv in invoices:
                    allocations_list.append(
                        PaymentApplicationObligationAllocation(
                            book_item_id=f"invoice:{inv.uuid}",
                            amount_units=amount_units,
                        )
                    )
            elif bills:
                for bill in bills:
                    allocations_list.append(
                        PaymentApplicationObligationAllocation(
                            book_item_id=f"bill:{bill.uuid}",
                            amount_units=amount_units,
                        )
                    )
            allocations = tuple(allocations_list)

            cap = self._mint_valid_capability(
                state=state,
                bank_item_id=bank_item_id,
                bank_account_id=str(self.bank_account.uuid),
                direction=direction,
                currency="USD",
                total_amount_units=amount_units,
                payment_date=payment_date,
                allocations=allocations,
            )

            cmd = ApplyPaymentCommand(
                command_id=f"cmd-{uuid.uuid4().hex[:6]}",
                expected_state_revision=state.revision,
                session_id=state.session_id,
                issued_at=datetime.now(timezone.utc),
                payment_application_id=f"payapp-{uuid.uuid4().hex[:6]}",
                bank_item_id=bank_item_id,
                bank_account_id=str(self.bank_account.uuid),
                direction=direction,
                payment_date=payment_date,
                total_amount_units=amount_units,
                currency="USD",
                allocations=allocations,
                capability=cap,
            )

            res = self.engine.apply(state=state, command=cmd)
            if res.status != TransitionStatus.APPLIED_REQUIRES_REHYDRATION:
                raise RuntimeError(f"Payment execution failed: {res.rejection}")
        finally:
            state.close()

    # ------------------------------------------------------------------
    # Test A: Fully paid invoice retains routing history
    # ------------------------------------------------------------------
    def test_a_fully_paid_invoice_retains_routing_history(self) -> None:
        """
        A. Fully paid invoice retains routing history:
        - route invoice
        - Stage 1 pays invoice fully
        - fresh hydration
        - invoice absent from active open-obligation projection
        - RoutingDecision still present in state history
        - active_route(invoice_id) is None or lifecycle-inactive according to query contract
        """
        stx = self._create_staged_movement(amount=Decimal("500.00"), dt=date(2026, 4, 5))
        inv = self._create_approved_invoice(amount=Decimal("500.00"), dt=date(2026, 4, 1))
        inv_item_id = f"invoice:{inv.uuid}"

        # Persist a routing decision for the open invoice
        rd_row = self._create_routing_decision(
            entity=self.entity,
            invoice=inv,
            bank_account=self.bank_account,
            source=RoutingDecisionSource.HUMAN.value,
            session_id="test-session-a",
            state_revision_at_creation=0,
        )

        # Initial hydration: invoice is active, route is active
        state_before = self.hydrator.hydrate(company_id=str(self.entity.uuid))
        queries_before = BookkeepingQueries(state_before)
        self.assertIsNotNone(state_before.get_book_item(inv_item_id))
        self.assertIsNotNone(queries_before.active_route(inv_item_id))
        self.assertEqual(queries_before.active_route(inv_item_id).id, rd_row.id)
        state_before.close()

        # Stage 1 pays invoice fully
        self._execute_payment(
            staged_tx=stx,
            invoices=[inv],
            payment_amount=Decimal("500.00"),
            payment_date=date(2026, 4, 5),
            session_id="test-session-a",
        )
        inv.refresh_from_db()
        self.assertEqual(inv.amount_paid, Decimal("500.00"))

        # Fresh hydration after payment
        state_after = self.hydrator.hydrate(company_id=str(self.entity.uuid))
        try:
            queries_after = BookkeepingQueries(state_after)

            # 1. Invoice is absent from active book_items projection
            self.assertIsNone(state_after.get_book_item(inv_item_id))
            self.assertNotIn(inv_item_id, state_after.book_items)

            # 2. RoutingDecision is still present in durable state history
            self.assertIn(rd_row.id, state_after.routing_decisions)
            rd_durable = state_after.routing_decisions[rd_row.id]
            self.assertEqual(rd_durable.book_item_id, inv_item_id)

            # 3. active_route(invoice_id) is None (lifecycle-inactive)
            self.assertIsNone(queries_after.active_route(inv_item_id))

            # 4. Historical query finds it
            historical_routes = queries_after.routing_decisions_for_book_item(inv_item_id)
            self.assertEqual(len(historical_routes), 1)
            self.assertEqual(historical_routes[0].id, rd_row.id)
        finally:
            state_after.close()

    # ------------------------------------------------------------------
    # Test B: Fully paid invoice retains classification history
    # ------------------------------------------------------------------
    def test_b_fully_paid_invoice_retains_classification_history(self) -> None:
        """
        B. Fully paid invoice retains classification history.
        """
        stx = self._create_staged_movement(amount=Decimal("300.00"), dt=date(2026, 4, 5))
        inv = self._create_approved_invoice(amount=Decimal("300.00"), dt=date(2026, 4, 1))
        inv_item_id = f"invoice:{inv.uuid}"

        # Persist classification decision
        cd_row = self._create_classification_decision(
            entity=self.entity,
            invoice=inv,
            account=self.revenue_account,
            account_code=self.revenue_account.code,
            source="DETERMINISTIC_RULE",
            session_id="test-session-b",
            state_revision_at_decision=0,
        )

        # Stage 1 pays invoice fully
        self._execute_payment(
            staged_tx=stx,
            invoices=[inv],
            payment_amount=Decimal("300.00"),
            payment_date=date(2026, 4, 5),
            session_id="test-session-b",
        )

        # Fresh hydration
        state = self.hydrator.hydrate(company_id=str(self.entity.uuid))
        try:
            queries = BookkeepingQueries(state)

            # Invoice absent from active book items
            self.assertIsNone(state.get_book_item(inv_item_id))

            # Classification preserved in durable history
            self.assertIn(cd_row.id, state.classifications)
            self.assertEqual(state.classifications[cd_row.id].book_item_id, inv_item_id)

            # active_classification returns None
            self.assertIsNone(queries.active_classification(inv_item_id))

            # Historical query finds it
            historical = queries.classifications_for_book_item(inv_item_id)
            self.assertEqual(len(historical), 1)
            self.assertEqual(historical[0].id, cd_row.id)
        finally:
            state.close()

    # ------------------------------------------------------------------
    # Test C: Fully paid invoice retains evidence assertion & invalidation history
    # ------------------------------------------------------------------
    def test_c_fully_paid_invoice_retains_evidence_and_invalidation_history(self) -> None:
        """
        C. Fully paid invoice retains evidence assertion + invalidation/supersession history.
        """
        stx = self._create_staged_movement(amount=Decimal("400.00"), dt=date(2026, 4, 5))
        inv = self._create_approved_invoice(amount=Decimal("400.00"), dt=date(2026, 4, 1))
        inv_item_id = f"invoice:{inv.uuid}"

        # E1
        ea1 = self._create_evidence_assertion(
            entity=self.entity,
            invoice=inv,
            evidence_type=BookItemEvidenceType.REFERENCE.value,
            value="PO-9999",
            source=EvidenceSource.DOCUMENT_EXTRACTION.value,
            confidence=0.8,
            session_id="test-session-c",
            state_revision_at_creation=0,
        )

        # E2 supersedes E1
        ea2 = self._create_evidence_assertion(
            entity=self.entity,
            invoice=inv,
            evidence_type=BookItemEvidenceType.REFERENCE.value,
            value="PO-10000",
            source=EvidenceSource.HUMAN_ASSERTION.value,
            supersedes=ea1,
            session_id="test-session-c",
            state_revision_at_creation=1,
        )

        # Invalidate E2
        ei = self._create_evidence_invalidation(
            assertion=ea2,
            reason="Entered in error",
            session_id="test-session-c",
            state_revision_at_invalidation=2,
        )

        # Stage 1 pays invoice fully
        self._execute_payment(
            staged_tx=stx,
            invoices=[inv],
            payment_amount=Decimal("400.00"),
            payment_date=date(2026, 4, 5),
            session_id="test-session-c",
        )

        # Fresh hydration
        state = self.hydrator.hydrate(company_id=str(self.entity.uuid))
        try:
            queries = BookkeepingQueries(state)

            self.assertIsNone(state.get_book_item(inv_item_id))

            # Both assertions and invalidation are present in durable history
            self.assertIn(ea1.id, state.book_item_evidence_assertions)
            self.assertIn(ea2.id, state.book_item_evidence_assertions)
            self.assertIn(ei.id, state.book_item_evidence_invalidations)

            # active_evidence_assertion is None
            self.assertIsNone(
                queries.active_evidence_assertion(inv_item_id, BookItemEvidenceType.REFERENCE)
            )

            # Historical queries find both
            assertions = queries.evidence_assertions_for_book_item(inv_item_id)
            self.assertEqual(len(assertions), 2)
        finally:
            state.close()

    # ------------------------------------------------------------------
    # Test D: Fully paid bill symmetric behavior
    # ------------------------------------------------------------------
    def test_d_fully_paid_bill_symmetric_behavior(self) -> None:
        """
        D. Fully paid bill symmetric behavior.
        """
        stx = self._create_staged_movement(amount=Decimal("-700.00"), dt=date(2026, 4, 5))
        bill = self._create_approved_bill(amount=Decimal("700.00"), dt=date(2026, 4, 1))
        bill_item_id = f"bill:{bill.uuid}"

        rd_row = self._create_routing_decision(
            entity=self.entity,
            bill=bill,
            bank_account=self.bank_account,
            source=RoutingDecisionSource.CP_SAT.value,
            session_id="test-session-d",
            state_revision_at_creation=0,
        )
        cd_row = self._create_classification_decision(
            entity=self.entity,
            bill=bill,
            account=self.expense_account,
            account_code=self.expense_account.code,
            source="DETERMINISTIC_RULE",
            session_id="test-session-d",
            state_revision_at_decision=0,
        )

        # Stage 1 pays bill fully
        self._execute_payment(
            staged_tx=stx,
            bills=[bill],
            payment_amount=Decimal("700.00"),
            payment_date=date(2026, 4, 5),
            session_id="test-session-d",
        )

        state = self.hydrator.hydrate(company_id=str(self.entity.uuid))
        try:
            queries = BookkeepingQueries(state)

            self.assertIsNone(state.get_book_item(bill_item_id))
            self.assertIn(rd_row.id, state.routing_decisions)
            self.assertIn(cd_row.id, state.classifications)

            self.assertIsNone(queries.active_route(bill_item_id))
            self.assertIsNone(queries.active_classification(bill_item_id))

            self.assertEqual(len(queries.routing_decisions_for_book_item(bill_item_id)), 1)
            self.assertEqual(len(queries.classifications_for_book_item(bill_item_id)), 1)
        finally:
            state.close()

    # ------------------------------------------------------------------
    # Test E: Historical supersession non-resurrection
    # ------------------------------------------------------------------
    def test_e_historical_supersession_non_resurrection(self) -> None:
        """
        E. Historical supersession non-resurrection:
        - D1
        - D2 supersedes D1
        - target later leaves active projection
        - fresh hydration retains D1 + D2
        - no active route/classification is resurrected
        """
        stx = self._create_staged_movement(amount=Decimal("500.00"), dt=date(2026, 4, 5))
        inv = self._create_approved_invoice(amount=Decimal("500.00"), dt=date(2026, 4, 1))
        inv_item_id = f"invoice:{inv.uuid}"

        # D1
        d1 = self._create_routing_decision(
            entity=self.entity,
            invoice=inv,
            bank_account=self.bank_account,
            source=RoutingDecisionSource.DETERMINISTIC_RULE.value,
            session_id="test-session-e",
            state_revision_at_creation=0,
        )

        # D2 supersedes D1
        d2 = self._create_routing_decision(
            entity=self.entity,
            invoice=inv,
            bank_account=self.bank_account,
            source=RoutingDecisionSource.HUMAN.value,
            supersedes=d1,
            session_id="test-session-e",
            state_revision_at_creation=1,
        )

        # Stage 1 pays invoice fully
        self._execute_payment(
            staged_tx=stx,
            invoices=[inv],
            payment_amount=Decimal("500.00"),
            payment_date=date(2026, 4, 5),
            session_id="test-session-e",
        )

        state = self.hydrator.hydrate(company_id=str(self.entity.uuid))
        try:
            queries = BookkeepingQueries(state)

            # Both D1 and D2 retained in durable history
            self.assertIn(d1.id, state.routing_decisions)
            self.assertIn(d2.id, state.routing_decisions)

            # Neither is active; D1 is not resurrected
            self.assertIsNone(queries.active_route(inv_item_id))
        finally:
            state.close()

    # ------------------------------------------------------------------
    # Test F: Fingerprint history preservation
    # ------------------------------------------------------------------
    def test_f_fingerprint_history_preservation(self) -> None:
        """
        F. Fingerprint history preservation:
        - before payment fingerprint includes accepted decision history
        - after payment + fresh hydration history remains represented
        - fingerprint change attributable to new accounting/payment truth, not reader-side artifact deletion
        """
        stx = self._create_staged_movement(amount=Decimal("500.00"), dt=date(2026, 4, 5))
        inv = self._create_approved_invoice(amount=Decimal("500.00"), dt=date(2026, 4, 1))
        inv_item_id = f"invoice:{inv.uuid}"

        rd_row = self._create_routing_decision(
            entity=self.entity,
            invoice=inv,
            bank_account=self.bank_account,
            source=RoutingDecisionSource.HUMAN.value,
            session_id="test-session-f",
            state_revision_at_creation=0,
        )
        cd_row = self._create_classification_decision(
            entity=self.entity,
            invoice=inv,
            account=self.revenue_account,
            account_code=self.revenue_account.code,
            source="DETERMINISTIC_RULE",
            session_id="test-session-f",
            state_revision_at_decision=0,
        )

        state_before = self.hydrator.hydrate(company_id=str(self.entity.uuid))
        fp_before = state_fingerprint(state_before)
        proj_before = canonical_artifact_projection(state_before)
        state_before.close()

        # Verify decision artifacts are in projection
        rd_ids_before = {a["id"] for a in proj_before["artifacts"]["routing_decisions"]}
        cd_ids_before = {a["id"] for a in proj_before["artifacts"]["classifications"]}
        self.assertIn(rd_row.id, rd_ids_before)
        self.assertIn(cd_row.id, cd_ids_before)

        # Stage 1 pays invoice fully
        self._execute_payment(
            staged_tx=stx,
            invoices=[inv],
            payment_amount=Decimal("500.00"),
            payment_date=date(2026, 4, 5),
            session_id="test-session-f",
        )

        state_after = self.hydrator.hydrate(company_id=str(self.entity.uuid))
        try:
            fp_after = state_fingerprint(state_after)
            proj_after = canonical_artifact_projection(state_after)

            # Fingerprint changed because accounting reality changed
            self.assertNotEqual(fp_before, fp_after)

            # But historical routing and classification decisions REMAIN in artifacts projection!
            rd_ids_after = {a["id"] for a in proj_after["artifacts"]["routing_decisions"]}
            cd_ids_after = {a["id"] for a in proj_after["artifacts"]["classifications"]}
            self.assertIn(rd_row.id, rd_ids_after)
            self.assertIn(cd_row.id, cd_ids_after)

            # New executed payment application is present
            self.assertEqual(len(proj_after["artifacts"]["executed_payment_applications"]), 1)
        finally:
            state_after.close()

    # ------------------------------------------------------------------
    # Test G: Session integration still passes without reader pruning
    # ------------------------------------------------------------------
    def test_g_session_integration_still_passes_without_reader_pruning(self) -> None:
        """
        G. Session integration still passes without reader pruning:
        Full production session: Route -> Classify -> Stage 1 execution -> rehydrate -> Stage 2 -> Detached result.
        """
        stx = self._create_staged_movement(amount=Decimal("500.00"), dt=date(2026, 4, 5))
        inv = self._create_approved_invoice(amount=Decimal("500.00"), dt=date(2026, 4, 1))

        # Prior classification decision is present in durable history
        self._create_classification_decision(
            entity=self.entity,
            invoice=inv,
            account=self.revenue_account,
            account_code=self.revenue_account.code,
            source="DETERMINISTIC_RULE",
            session_id="test-session-g-prior",
            state_revision_at_decision=0,
        )

        state_init = self.hydrator.hydrate(company_id=str(self.entity.uuid), session_id="test-session-g")
        engine = TransitionEngine(
            repository=self.repository,
        )
        routing_service = RoutingService(semantic_provider=ZeroRoutingSemanticScoreProvider())
        dag_classifier = StubAseClassifier(default_account_code=self.revenue_account.code)
        payment_service = PaymentApplicationService()
        recon_service = ReconciliationService()

        session = BookkeepingSession(
            state=state_init,
            engine=engine,
            hydrator=self.hydrator,
            routing_service=routing_service,
            dag_classifier=dag_classifier,
            payment_application_service=payment_service,
            reconciliation_service=recon_service,
        )

        res = session.run()
        self.assertTrue(res.is_success)
        self.assertEqual(len(res.executed_payment_application_ids), 1)

        # Fresh hydration reveals all intermediate history is retained
        state = self.hydrator.hydrate(company_id=str(self.entity.uuid))
        try:
            self.assertGreaterEqual(len(state.routing_decisions), 1)
            self.assertGreaterEqual(len(state.classifications), 1)
            self.assertEqual(len(state.executed_payment_applications), 1)
        finally:
            state.close()

    # ------------------------------------------------------------------
    # Test H: Malformed durable reference fails explicitly
    # ------------------------------------------------------------------
    def test_h_malformed_durable_reference_fails_explicitly(self) -> None:
        """
        H. Malformed durable reference:
        - if a durable row points to a source row that was actually deleted/impossible,
          hydration fails explicitly rather than silently dropping it.
        """
        # Create a second entity
        other_entity = EntityModel.add_root(
            name="Other Corp",
            admin=self.user,
            currency="USD",
            fy_start_month=1,
            accrual_method=False,
        )
        other_coa = other_entity.create_chart_of_accounts(
            coa_name="Other CoA",
            assign_as_default=True,
            commit=True,
        )
        other_asset = other_coa.accountmodel_set.get(code="01000000")
        from ledger.io.roles import ASSET_CA_CASH
        from ledger.io.roles import DEBIT
        other_cash = other_asset.add_child(
            coa_model=other_coa,
            code="1010",
            name="Other Cash",
            role=ASSET_CA_CASH,
            balance_type=DEBIT,
        )
        from ledger.models.bank_account import BankAccountModel
        other_bank_account = BankAccountModel.objects.create(
            name="Foreign Account",
            entity_model=other_entity,
            account_model=other_cash,
            connection_type="manual",
        )

        inv = self._create_approved_invoice(amount=Decimal("200.00"), dt=date(2026, 4, 1))

        self._create_routing_decision(
            entity=self.entity,
            invoice=inv,
            bank_account=other_bank_account,
            source=RoutingDecisionSource.HUMAN.value,
            session_id="test-session-h",
            state_revision_at_creation=0,
        )

        # Hydration must raise PersistenceError, NOT silently drop it!
        with self.assertRaises(PersistenceError):
            self.hydrator.hydrate(company_id=str(self.entity.uuid))
