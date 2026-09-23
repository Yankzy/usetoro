from __future__ import annotations

from datetime import date, datetime, timezone as dt_timezone
from decimal import Decimal
import uuid

from django.contrib.auth import get_user_model
from django.test import TestCase

from bookkeeping_state.domain.enums import (
    AllocationSupport,
    Direction,
    SemanticAdmissibility,
    SourceType,
)
from bookkeeping_state.domain.evidence import BookItemEvidenceType, EvidenceSource
from bookkeeping_state.domain.payment_application import (
    ExecutedPaymentAllocation,
    ExecutedPaymentApplication,
)
from bookkeeping_state.domain.routing import RoutingDecisionSource
from bookkeeping_state.domain.classifications import ClassificationSource
from bookkeeping_state.hydration import BookkeepingHydrator
from bookkeeping_state.payment_application.view import build_payment_application_view
from bookkeeping_state.persistence.reader import read_django_snapshot
from bookkeeping_state.persistence.repository import (
    BookkeepingRepository,
    PersistenceError,
)
from bookkeeping_state.reconciliation.candidate_generation import (
    CandidateGenerator,
    DefaultReconciliationScorer,
)
from bookkeeping_state.reconciliation.view import build_reconciliation_view
from bookkeeping_state.state.queries import BookkeepingQueries
from ledger.io.roles import (
    ASSET_CA_CASH,
    ASSET_CA_RECEIVABLES,
    CREDIT,
    DEBIT,
    INCOME_OPERATIONAL,
    LIABILITY_CL_DEFERRED_REVENUE,
)
from ledger.models import (
    AccountModel,
    BankAccountModel,
    BillModel,
    CustomerModel,
    EntityModel,
    ImportJobModel,
    InvoiceModel,
    JournalEntryModel,
    LedgerModel,
    PlaidItem,
    PlaidTransaction,
    StagedTransactionModel,
    TransactionModel,
    VendorModel,
)
from ledger.models.bookkeeping import (
    BookkeepingClassificationDecision,
    BookkeepingClassificationInvalidation,
    BookkeepingEvidenceAssertion,
    BookkeepingEvidenceInvalidation,
    BookkeepingPaymentApplication,
    BookkeepingPaymentApplicationAllocation,
    BookkeepingReconciliation,
    BookkeepingReconciliationBankAllocation,
    BookkeepingReconciliationBookAllocation,
    BookkeepingReconciliationInvalidation,
    BookkeepingRevision,
    BookkeepingRoutingDecision,
    BookkeepingRoutingInvalidation,
)

UserModel = get_user_model()


class DurableBookkeepingHydrationTests(TestCase):
    """
    Comprehensive test suite verifying hydration of all 13 durable Django
    bookkeeping models into detached domain artifacts and BookkeepingSnapshot/State.
    """

    def setUp(self) -> None:
        super().setUp()

        self.user = UserModel.objects.create_user(
            username="hydration_admin",
            email="admin@hydrationtest.com",
            password="secure_password_123",
        )

        self.entity = EntityModel.add_root(
            name="Hydration Testing Corp",
            admin=self.user,
            currency="USD",
            fy_start_month=1,
            accrual_method=False,
        )

        self.coa = self.entity.create_chart_of_accounts(
            coa_name="Hydration CoA",
            assign_as_default=True,
            commit=True,
        )
        asset_root = self.coa.accountmodel_set.get(code="01000000")

        self.cash_account = asset_root.add_child(
            coa_model=self.coa,
            code="1010",
            name="Operating Checking",
            role=ASSET_CA_CASH,
            balance_type=DEBIT,
        )

        self.receivables_account = asset_root.add_child(
            coa_model=self.coa,
            code="1020",
            name="Accounts Receivable",
            role=ASSET_CA_RECEIVABLES,
            balance_type=DEBIT,
        )

        liability_root = self.coa.accountmodel_set.get(code="02000000")
        self.unearned_revenue_account = liability_root.add_child(
            coa_model=self.coa,
            code="2010",
            name="Deferred Revenue",
            role=LIABILITY_CL_DEFERRED_REVENUE,
            balance_type=CREDIT,
        )

        income_root = self.coa.accountmodel_set.get(code="04000000")
        self.revenue_account = income_root.add_child(
            coa_model=self.coa,
            code="4010",
            name="Operational Revenue",
            role=INCOME_OPERATIONAL,
            balance_type=CREDIT,
        )

        self.bank_account = BankAccountModel.objects.create(
            name="Primary Checking",
            entity_model=self.entity,
            account_model=self.cash_account,
            connection_type="manual",
            account_number="CHK-1234",
            active=True,
        )

        self.import_job = ImportJobModel.objects.create(
            description="Hydration Test Import Job",
            bank_account_model=self.bank_account,
        )

        self.customer = CustomerModel.objects.create(
            customer_name="Acme Corp",
            customer_number="CUST-101",
            entity_model=self.entity,
        )

        self.ledger = LedgerModel.objects.create(
            name="General Ledger",
            entity=self.entity,
            posted=True,
        )

    def _create_invoice(
        self,
        amount_due: Decimal,
        amount_paid: Decimal = Decimal("0.00"),
        dt: date = date(2026, 3, 1),
    ) -> InvoiceModel:
        inv = InvoiceModel(
            cash_account=self.cash_account,
            prepaid_account=self.receivables_account,
            unearned_account=self.unearned_revenue_account,
            accrue=False,
        )
        _, inv = inv.configure(entity_slug=self.entity, user_model=self.user)
        inv.amount_due = amount_due
        inv.amount_paid = amount_paid
        inv.date_draft = dt
        inv.invoice_status = InvoiceModel.INVOICE_STATUS_APPROVED
        inv.customer = self.customer
        inv.clean()
        inv.save()
        return inv

    def _create_staged_tx(
        self,
        amount: Decimal,
        dt: date = date(2026, 3, 10),
        name: str = "Bank Tx",
    ) -> StagedTransactionModel:
        return StagedTransactionModel.objects.create(
            import_job=self.import_job,
            amount=amount,
            date_posted=dt,
            name=name,
            fit_id=f"FIT-{uuid.uuid4().hex[:8]}",
        )

    def _create_cash_tx(
        self,
        amount: Decimal,
        is_debit: bool = True,
        dt: date = date(2026, 3, 10),
        desc: str = "Cash Tx",
    ) -> TransactionModel:
        je = JournalEntryModel.objects.create(
            ledger=self.ledger,
            description=desc,
            posted=False,
            timestamp=datetime(dt.year, dt.month, dt.day, 12, 0, 0, tzinfo=dt_timezone.utc),
        )
        tx = TransactionModel.objects.create(
            journal_entry=je,
            account=self.cash_account,
            amount=amount,
            tx_type=DEBIT if is_debit else CREDIT,
            reconciled=False,
            description=desc,
        )
        TransactionModel.objects.create(
            journal_entry=je,
            account=self.revenue_account,
            amount=amount,
            tx_type=CREDIT if is_debit else DEBIT,
            reconciled=False,
            description=f"{desc} Counterpart",
        )
        je.posted = True
        je.save(update_fields=["posted"], verify=False)
        return tx

    def test_revision_hydration_zero_vs_persisted(self) -> None:
        """Test persistence_revision is 0 when no row exists, and N when row exists."""
        # 1. No row -> 0
        snap_0 = read_django_snapshot(company_id=str(self.entity.uuid))
        self.assertEqual(snap_0.persistence_revision, 0)

        # 2. Row exists -> N
        BookkeepingRevision.objects.create(entity=self.entity, revision=7)
        snap_7 = read_django_snapshot(company_id=str(self.entity.uuid))
        self.assertEqual(snap_7.persistence_revision, 7)

    def test_routing_decision_supersession_and_invalidation_hydration(self) -> None:
        """Test routing decisions, target mapping, supersession, and invalidation."""
        inv = self._create_invoice(amount_due=Decimal("500.00"))

        now = datetime.now(dt_timezone.utc)
        rd1 = BookkeepingRoutingDecision.objects.create(
            id="route:001",
            entity=self.entity,
            invoice=inv,
            bank_account=self.bank_account,
            source=RoutingDecisionSource.CP_SAT.value,
            utility=950,
            solver_run_id="solver-run-1",
            session_id="session-1",
            state_revision_at_creation=0,
            created_at=now,
        )

        rd2 = BookkeepingRoutingDecision.objects.create(
            id="route:002",
            entity=self.entity,
            invoice=inv,
            bank_account=self.bank_account,
            source=RoutingDecisionSource.HUMAN.value,
            utility=1000,
            supersedes=rd1,
            session_id="session-2",
            state_revision_at_creation=1,
            created_at=now,
        )

        BookkeepingRoutingInvalidation.objects.create(
            id="route_inv:001",
            routing_decision=rd2,
            reason="Corrected routing error",
            session_id="session-3",
            state_revision_at_invalidation=2,
            created_at=now,
        )

        snapshot = read_django_snapshot(company_id=str(self.entity.uuid))
        self.assertEqual(len(snapshot.routing_decisions), 2)
        self.assertEqual(len(snapshot.routing_invalidations), 1)

        r1 = next(r for r in snapshot.routing_decisions if r.id == "route:001")
        r2 = next(r for r in snapshot.routing_decisions if r.id == "route:002")
        self.assertEqual(r1.book_item_id, f"invoice:{inv.uuid}")
        self.assertEqual(r1.bank_account_id, str(self.bank_account.uuid))
        self.assertEqual(r1.source, RoutingDecisionSource.CP_SAT)
        self.assertEqual(r2.supersedes_routing_decision_id, "route:001")

        inv_art = snapshot.routing_invalidations[0]
        self.assertEqual(inv_art.routing_decision_id, "route:002")
        self.assertEqual(inv_art.reason, "Corrected routing error")

    def test_classification_decision_and_account_mismatch_validation(self) -> None:
        """Test classification hydration and verify account_code mismatch raises PersistenceError."""
        inv = self._create_invoice(amount_due=Decimal("300.00"))

        now = datetime.now(dt_timezone.utc)
        cd = BookkeepingClassificationDecision.objects.create(
            id="class:001",
            entity=self.entity,
            invoice=inv,
            account_code="4010",
            account=self.revenue_account,
            source=ClassificationSource.ASE_DAG.value,
            confidence=0.98,
            rationale="Operational income match",
            evidence_refs=["doc:123"],
            state_revision_at_decision=0,
            created_at=now,
        )

        BookkeepingClassificationInvalidation.objects.create(
            id="class_inv:001",
            classification=cd,
            reason="Wrong category",
            state_revision_at_invalidation=1,
            created_at=now,
        )

        snapshot = read_django_snapshot(company_id=str(self.entity.uuid))
        self.assertEqual(len(snapshot.classifications), 1)
        self.assertEqual(len(snapshot.classification_invalidations), 1)
        self.assertEqual(snapshot.classifications[0].account_code, "4010")
        self.assertEqual(snapshot.classifications[0].book_item_id, f"invoice:{inv.uuid}")

        # Now induce mismatch: linked AccountModel has code "4010", but account_code is "1010"
        cd.account_code = "1010"
        cd.save()
        with self.assertRaises(PersistenceError) as ctx:
            read_django_snapshot(company_id=str(self.entity.uuid))
        self.assertIn("does not match linked AccountModel code", str(ctx.exception))

    def test_evidence_assertion_hydration(self) -> None:
        """Test evidence assertion and invalidation hydration."""
        inv = self._create_invoice(amount_due=Decimal("250.00"))

        now = datetime.now(dt_timezone.utc)
        ea = BookkeepingEvidenceAssertion.objects.create(
            id="ev:001",
            entity=self.entity,
            invoice=inv,
            evidence_type=BookItemEvidenceType.REFERENCE.value,
            value="INV-2026-99",
            document_ids=["doc:9988"],
            source=EvidenceSource.DOCUMENT_EXTRACTION.value,
            confidence=0.99,
            reason="Extracted from PDF",
            metadata={"ocr_engine": "tesseract"},
            state_revision_at_creation=0,
            created_at=now,
        )

        BookkeepingEvidenceInvalidation.objects.create(
            id="ev_inv:001",
            assertion=ea,
            reason="Typo in reference",
            state_revision_at_invalidation=1,
            created_at=now,
        )

        snapshot = read_django_snapshot(company_id=str(self.entity.uuid))
        self.assertEqual(len(snapshot.book_item_evidence_assertions), 1)
        self.assertEqual(len(snapshot.book_item_evidence_invalidations), 1)

        ev_art = snapshot.book_item_evidence_assertions[0]
        self.assertEqual(ev_art.book_item_id, f"invoice:{inv.uuid}")
        self.assertEqual(ev_art.value, "INV-2026-99")
        self.assertEqual(ev_art.document_ids, ("doc:9988",))
        self.assertEqual(ev_art.source, EvidenceSource.DOCUMENT_EXTRACTION)

    def test_stage1_payment_application_provenance_hydration(self) -> None:
        """Test Stage-1 payment application and allocation hydration with exact AmountUnits."""
        # 1. Create StagedTransactionModel (Bank payment 1000.00)
        staged = self._create_staged_tx(amount=Decimal("1000.00"), name="Wire from Client 1000")

        # 2. Create Invoices A (600) and B (400)
        inv_a = self._create_invoice(amount_due=Decimal("600.00"), amount_paid=Decimal("600.00"))
        inv_b = self._create_invoice(amount_due=Decimal("400.00"), amount_paid=Decimal("400.00"))

        # 3. Create cash TransactionModel legs (T1 600, T2 400)
        now = datetime.now(dt_timezone.utc)
        tx1 = self._create_cash_tx(amount=Decimal("600.00"), desc="Payment A")
        tx2 = self._create_cash_tx(amount=Decimal("400.00"), desc="Payment B")

        # 4. Create durable BookkeepingPaymentApplication (10,000,000 units = 1000.00)
        pa = BookkeepingPaymentApplication.objects.create(
            id="payapp:001",
            entity=self.entity,
            staged_transaction=staged,
            total_amount_units=10_000_000,
            direction=Direction.BANK_INFLOW.value,
            currency="USD",
            session_id="session-pay-1",
            state_revision=1,
            created_at=now,
        )

        BookkeepingPaymentApplicationAllocation.objects.create(
            payment_application=pa,
            invoice=inv_a,
            amount_units=6_000_000,
            cash_transaction=tx1,
        )
        BookkeepingPaymentApplicationAllocation.objects.create(
            payment_application=pa,
            invoice=inv_b,
            amount_units=4_000_000,
            cash_transaction=tx2,
        )

        # 5. Hydrate snapshot
        snapshot = read_django_snapshot(company_id=str(self.entity.uuid))
        self.assertEqual(len(snapshot.executed_payment_applications), 1)

        exec_app = snapshot.executed_payment_applications[0]
        self.assertEqual(exec_app.id, "payapp:001")
        self.assertEqual(exec_app.bank_item_id, f"staged:{staged.uuid}")
        self.assertEqual(exec_app.total_amount_units, "10000000")
        self.assertEqual(len(exec_app.allocations), 2)

        alloc1 = exec_app.allocations[0]
        alloc2 = exec_app.allocations[1]
        self.assertEqual(alloc1.obligation_book_item_id, f"invoice:{inv_a.uuid}")
        self.assertEqual(alloc1.cash_transaction_book_item_id, f"tx:{tx1.uuid}")
        self.assertEqual(alloc1.amount_units, "6000000")

        self.assertEqual(alloc2.obligation_book_item_id, f"invoice:{inv_b.uuid}")
        self.assertEqual(alloc2.cash_transaction_book_item_id, f"tx:{tx2.uuid}")
        self.assertEqual(alloc2.amount_units, "4000000")

    def test_executed_stage1_bank_item_excluded_from_stage1_view(self) -> None:
        """Prove that a bank item used in executed Stage 1 is excluded from future Stage 1 views."""
        staged = self._create_staged_tx(amount=Decimal("1000.00"), name="Wire from Client 1000")
        inv = self._create_invoice(amount_due=Decimal("1000.00"), amount_paid=Decimal("1000.00"))
        tx = self._create_cash_tx(amount=Decimal("1000.00"), desc="Payment Full")
        now = datetime.now(dt_timezone.utc)
        pa = BookkeepingPaymentApplication.objects.create(
            id="payapp:full",
            entity=self.entity,
            staged_transaction=staged,
            total_amount_units=10_000_000,
            direction=Direction.BANK_INFLOW.value,
            currency="USD",
            state_revision=1,
            created_at=now,
        )
        BookkeepingPaymentApplicationAllocation.objects.create(
            payment_application=pa,
            invoice=inv,
            amount_units=10_000_000,
            cash_transaction=tx,
        )

        hydrator = BookkeepingHydrator(repository=BookkeepingRepositoryStub(str(self.entity.uuid)))
        state = hydrator.hydrate(company_id=str(self.entity.uuid))
        queries = BookkeepingQueries(state)

        # Query Stage-1 view
        stage1_view = build_payment_application_view(queries)
        bank_ids = [b.bank_item_id for b in stage1_view.bank_items]
        self.assertNotIn(f"staged:{staged.uuid}", bank_ids)
        self.assertIn(f"staged:{staged.uuid}", queries.executed_stage1_bank_item_ids())

    def test_stage1_structured_provenance_produces_exact_stage2_candidate_and_support(self) -> None:
        """Prove that structured Stage-1 provenance produces exact Stage-2 1:N candidate with SUPPORTED status."""
        staged = self._create_staged_tx(amount=Decimal("1000.00"), name="Generic Uninformative Bank Narration")
        inv_a = self._create_invoice(amount_due=Decimal("600.00"), amount_paid=Decimal("600.00"))
        inv_b = self._create_invoice(amount_due=Decimal("400.00"), amount_paid=Decimal("400.00"))
        tx1 = self._create_cash_tx(amount=Decimal("600.00"), desc="Payment A")
        tx2 = self._create_cash_tx(amount=Decimal("400.00"), desc="Payment B")
        now = datetime.now(dt_timezone.utc)
        pa = BookkeepingPaymentApplication.objects.create(
            id="payapp:split",
            entity=self.entity,
            staged_transaction=staged,
            total_amount_units=10_000_000,
            direction=Direction.BANK_INFLOW.value,
            currency="USD",
            state_revision=1,
            created_at=now,
        )
        BookkeepingPaymentApplicationAllocation.objects.create(
            payment_application=pa,
            invoice=inv_a,
            amount_units=6_000_000,
            cash_transaction=tx1,
        )
        BookkeepingPaymentApplicationAllocation.objects.create(
            payment_application=pa,
            invoice=inv_b,
            amount_units=4_000_000,
            cash_transaction=tx2,
        )

        hydrator = BookkeepingHydrator(repository=BookkeepingRepositoryStub(str(self.entity.uuid)))
        state = hydrator.hydrate(company_id=str(self.entity.uuid))
        queries = BookkeepingQueries(state)

        # Stage 2 view
        rec_view = build_reconciliation_view(queries)
        cand_gen = CandidateGenerator()
        candidates = cand_gen.generate_candidates(rec_view)

        # Verify candidate matching bank to {tx1, tx2} exists
        b_id = f"staged:{staged.uuid}"
        prov_cand = next(
            (c for c in candidates if c.bank_allocations[0].bank_item_id == b_id and len(c.book_allocations) == 2),
            None,
        )
        self.assertIsNotNone(prov_cand, "Expected structured Stage-1 provenance candidate to be generated")
        assert prov_cand is not None

        # Verify scoring
        scorer = DefaultReconciliationScorer()
        scored = scorer.score_candidate(prov_cand, rec_view)
        self.assertEqual(scored.admissibility, SemanticAdmissibility.SUPPORTED)
        self.assertEqual(scored.allocation_support, AllocationSupport.EXPLICIT_EVIDENCE)

    def test_stage2_reconciliation_and_invalidation_survives_fresh_hydration(self) -> None:
        """Test accepted reconciliation and subsequent invalidation survive roundtrip hydration."""
        staged = self._create_staged_tx(amount=Decimal("500.00"), name="Reconciled Bank Leg")
        tx = self._create_cash_tx(amount=Decimal("500.00"), desc="Reconciled Cash Leg")
        now = datetime.now(dt_timezone.utc)

        rec = BookkeepingReconciliation.objects.create(
            id="rec:001",
            entity=self.entity,
            state_revision_at_creation=1,
            created_at=now,
        )
        BookkeepingReconciliationBankAllocation.objects.create(
            reconciliation=rec,
            staged_transaction=staged,
            amount_units=5_000_000,
        )
        BookkeepingReconciliationBookAllocation.objects.create(
            reconciliation=rec,
            transaction=tx,
            amount_units=5_000_000,
        )

        snapshot = read_django_snapshot(company_id=str(self.entity.uuid))
        self.assertEqual(len(snapshot.reconciliations), 1)
        r = snapshot.reconciliations[0]
        self.assertEqual(r.bank_allocations[0].bank_item_id, f"staged:{staged.uuid}")
        self.assertEqual(r.book_allocations[0].book_item_id, f"tx:{tx.uuid}")
        self.assertEqual(r.total_bank_allocation_int, 5_000_000)

        # Invalidate reconciliation
        BookkeepingReconciliationInvalidation.objects.create(
            id="rec_inv:001",
            reconciliation=rec,
            reason="Accidental match",
            state_revision_at_invalidation=2,
            created_at=now,
        )
        snapshot_inv = read_django_snapshot(company_id=str(self.entity.uuid))
        self.assertEqual(len(snapshot_inv.reconciliation_invalidations), 1)

    def test_malformed_durable_provenance_fails_explicitly(self) -> None:
        """Test that data integrity violations in durable models raise PersistenceError during hydration."""
        staged = self._create_staged_tx(amount=Decimal("1000.00"))
        inv = self._create_invoice(amount_due=Decimal("1000.00"), amount_paid=Decimal("1000.00"))
        tx = self._create_cash_tx(amount=Decimal("1000.00"))
        now = datetime.now(dt_timezone.utc)

        # Case 1: Amount mismatch between bank source and application total
        pa = BookkeepingPaymentApplication.objects.create(
            id="payapp:bad_amt",
            entity=self.entity,
            staged_transaction=staged,
            total_amount_units=5_000_000,  # 500.00 vs bank 1000.00
            direction=Direction.BANK_INFLOW.value,
            currency="USD",
            state_revision=1,
            created_at=now,
        )
        BookkeepingPaymentApplicationAllocation.objects.create(
            payment_application=pa,
            invoice=inv,
            amount_units=5_000_000,
            cash_transaction=tx,
        )

        with self.assertRaises(PersistenceError) as ctx:
            read_django_snapshot(company_id=str(self.entity.uuid))
        self.assertIn("does not match bank source original amount", str(ctx.exception))

        # Fix amount, but break allocation sum
        pa.total_amount_units = 10_000_000
        pa.save()
        with self.assertRaises(PersistenceError) as ctx:
            read_django_snapshot(company_id=str(self.entity.uuid))
        self.assertIn("allocations sum (5000000) != total (10000000)", str(ctx.exception))

    def test_slug_vs_uuid_lookup_produces_identical_company_identity(self) -> None:
        """Test that company_id lookup by UUID or slug produces canonical identical company identity."""
        snap_uuid = read_django_snapshot(company_id=str(self.entity.uuid))
        snap_slug = read_django_snapshot(company_id=str(self.entity.slug))

        self.assertEqual(snap_uuid.context.company_id, str(self.entity.uuid))
        self.assertEqual(snap_slug.context.company_id, str(self.entity.uuid))

    def test_detached_read_boundary_no_orm_leakage(self) -> None:
        """Test that BookkeepingSnapshot contains zero Django model instances or QuerySets."""
        snapshot = read_django_snapshot(company_id=str(self.entity.uuid))
        from django.db.models import Model, QuerySet

        for attr in (
            "bank_accounts",
            "bank_items",
            "book_items",
            "counterparties",
            "routing_decisions",
            "routing_invalidations",
            "classifications",
            "classification_invalidations",
            "reconciliations",
            "reconciliation_invalidations",
            "book_item_evidence_assertions",
            "book_item_evidence_invalidations",
            "executed_payment_applications",
        ):
            val = getattr(snapshot, attr)
            self.assertNotIsInstance(val, QuerySet)
            for item in val:
                self.assertNotIsInstance(item, Model)


class BookkeepingRepositoryStub(BookkeepingRepository):
    """Minimal repository wrapper around read_django_snapshot for testing BookkeepingHydrator."""

    def __init__(self, company_id: str) -> None:
        self._company_id = company_id

    def load_snapshot(self, *, company_id: str):
        return read_django_snapshot(company_id=company_id)

    def commit(self, *, company_id: str, expected_revision: int, write_set):
        raise NotImplementedError("commit() is not implemented yet.")
