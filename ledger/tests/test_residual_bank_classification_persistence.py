from __future__ import annotations

from datetime import date, datetime, timezone
from decimal import Decimal
from uuid import uuid4

from django.contrib.auth import get_user_model
from django.db import IntegrityError, transaction
from django.test import TestCase

from bookkeeping_state.domain.enums import Direction
from bookkeeping_state.domain.residual_bank_classifications import (
    ResidualBankClassificationDecision,
    ResidualBankClassificationInvalidation,
    ResidualBankClassificationStatus,
)
from bookkeeping_state.hydration import BookkeepingHydrator
from bookkeeping_state.persistence.reader import read_django_snapshot
from bookkeeping_state.persistence.repository import (
    BookkeepingRepository,
    PersistenceError,
)
from bookkeeping_state.state.bookkeeping_state import BookkeepingState
from bookkeeping_state.state.derived import DerivedStateError
from bookkeeping_state.state.fingerprint import state_fingerprint
from bookkeeping_state.state.queries import BookkeepingQueries
from bookkeeping_state.state.validation import validate_state
from ledger.io.roles import (
    ASSET_CA_CASH,
    DEBIT,
    EXPENSE_OPERATIONAL,
)
from ledger.models import (
    AccountModel,
    BankAccountModel,
    EntityModel,
    ImportJobModel,
    JournalEntryModel,
    LedgerModel,
    StagedTransactionModel,
    TransactionModel,
)
from ledger.models.bookkeeping import (
    BookkeepingClassificationDecision,
    BookkeepingResidualBankClassificationDecision,
    BookkeepingResidualBankClassificationInvalidation,
)

UserModel = get_user_model()


class ResidualBankClassificationPersistenceTests(TestCase):
    @classmethod
    def setUpTestData(cls) -> None:
        cls.user = UserModel.objects.create_user(
            username=f"testuser_{uuid4().hex[:8]}",
            email=f"test_{uuid4().hex[:8]}@example.com",
            password="testpassword123",
        )
        cls.entity = EntityModel.add_root(
            name="Residual Classification Test Corp",
            admin=cls.user,
            currency="USD",
            fy_start_month=1,
            accrual_method=False,
        )
        cls.coa = cls.entity.create_chart_of_accounts(
            coa_name="Default CoA",
            assign_as_default=True,
            commit=True,
        )
        asset_root = cls.coa.accountmodel_set.get(code="01000000")
        cls.cash_account = asset_root.add_child(
            coa_model=cls.coa,
            code="1010",
            name="Cash Account",
            role=ASSET_CA_CASH,
            balance_type=DEBIT,
            active=True,
        )

        expense_root = cls.coa.accountmodel_set.get(code="06000000")
        cls.expense_account = expense_root.add_child(
            coa_model=cls.coa,
            code="6111",
            name="Office Supplies Expense",
            role=EXPENSE_OPERATIONAL,
            balance_type=DEBIT,
            active=True,
        )

        cls.bank_account = BankAccountModel.objects.create(
            entity_model=cls.entity,
            account_model=cls.cash_account,
            name="Main Operating Account",
            active=True,
        )

        cls.standalone_ledger = LedgerModel.objects.create(
            name="Residual Classification Test Ledger",
            entity=cls.entity,
            posted=True,
        )
        cls.journal_entry = JournalEntryModel.objects.create(
            ledger=cls.standalone_ledger,
            timestamp=datetime(2026, 8, 1, 12, 0, 0, tzinfo=timezone.utc),
            description="Test payment journal entry",
            posted=False,
        )
        cls.transaction = TransactionModel.objects.create(
            journal_entry=cls.journal_entry,
            account=cls.cash_account,
            amount=Decimal("150.00"),
            tx_type=TransactionModel.DEBIT,
        )
        cls.journal_entry.posted = True
        cls.journal_entry.save(update_fields=["posted"], verify=False)

        cls.import_job = ImportJobModel.objects.create(
            bank_account_model=cls.bank_account,
            description="OFX Import Job",
        )

        cls.staged_tx = StagedTransactionModel.objects.create(
            import_job=cls.import_job,
            fit_id="fit-tx-001",
            date_posted=date(2026, 8, 1),
            amount=Decimal("-150.00"),
            name="Office Depot #123",
            memo="Stationery purchase",
        )

        cls.staged_tx2 = StagedTransactionModel.objects.create(
            import_job=cls.import_job,
            fit_id="fit-tx-002",
            date_posted=date(2026, 8, 2),
            amount=Decimal("500.00"),
            name="Client Wire Transfer",
        )

    # ------------------------------------------------------------------
    # Test A: Migration applied and model accessible
    # ------------------------------------------------------------------
    def test_a_migration_applied(self) -> None:
        count = BookkeepingResidualBankClassificationDecision.objects.count()
        self.assertEqual(count, 0)
        inv_count = BookkeepingResidualBankClassificationInvalidation.objects.count()
        self.assertEqual(inv_count, 0)

    # ------------------------------------------------------------------
    # Test B: CLASSIFIED DB check constraints
    # ------------------------------------------------------------------
    def test_b_classified_db_constraints(self) -> None:
        # Missing account / account_code fails
        with self.assertRaises(IntegrityError):
            with transaction.atomic():
                BookkeepingResidualBankClassificationDecision.objects.create(
                    id="dec-b-fail-no-acc",
                    entity=self.entity,
                    staged_transaction=self.staged_tx,
                    bank_account=self.bank_account,
                    status="CLASSIFIED",
                    account=None,
                    account_code=None,
                    original_amount_units=1500000,
                    residual_amount_units=1500000,
                    direction="OUTFLOW",
                    currency="USD",
                    confidence=0.99,
                    schema_version="bookkeeping.ase.bank_categorize.v1",
                    dag_id="bookkeeping_bank_categorization_v1",
                    request_semantic_digest="digest-b1",
                    state_revision_at_decision=1,
                    persistence_revision_at_decision=1,
                    created_at=datetime.now(timezone.utc),
                )

        # CLASSIFIED with hold_reason fails
        with self.assertRaises(IntegrityError):
            with transaction.atomic():
                BookkeepingResidualBankClassificationDecision.objects.create(
                    id="dec-b-fail-hold-reason",
                    entity=self.entity,
                    staged_transaction=self.staged_tx,
                    bank_account=self.bank_account,
                    status="CLASSIFIED",
                    account=self.expense_account,
                    account_code="6111",
                    hold_reason="Should not have hold reason",
                    original_amount_units=1500000,
                    residual_amount_units=1500000,
                    direction="OUTFLOW",
                    currency="USD",
                    confidence=0.99,
                    schema_version="bookkeeping.ase.bank_categorize.v1",
                    dag_id="bookkeeping_bank_categorization_v1",
                    request_semantic_digest="digest-b2",
                    state_revision_at_decision=1,
                    persistence_revision_at_decision=1,
                    created_at=datetime.now(timezone.utc),
                )

        # CLASSIFIED with null confidence fails
        with self.assertRaises(IntegrityError):
            with transaction.atomic():
                BookkeepingResidualBankClassificationDecision.objects.create(
                    id="dec-b-fail-null-conf",
                    entity=self.entity,
                    staged_transaction=self.staged_tx,
                    bank_account=self.bank_account,
                    status="CLASSIFIED",
                    account=self.expense_account,
                    account_code="6111",
                    confidence=None,
                    original_amount_units=1500000,
                    residual_amount_units=1500000,
                    direction="OUTFLOW",
                    currency="USD",
                    schema_version="bookkeeping.ase.bank_categorize.v1",
                    dag_id="bookkeeping_bank_categorization_v1",
                    request_semantic_digest="digest-b3",
                    state_revision_at_decision=1,
                    persistence_revision_at_decision=1,
                    created_at=datetime.now(timezone.utc),
                )

        # CLASSIFIED with confidence > 1.0 fails
        with self.assertRaises(IntegrityError):
            with transaction.atomic():
                BookkeepingResidualBankClassificationDecision.objects.create(
                    id="dec-b-fail-conf-high",
                    entity=self.entity,
                    staged_transaction=self.staged_tx,
                    bank_account=self.bank_account,
                    status="CLASSIFIED",
                    account=self.expense_account,
                    account_code="6111",
                    confidence=1.05,
                    original_amount_units=1500000,
                    residual_amount_units=1500000,
                    direction="OUTFLOW",
                    currency="USD",
                    schema_version="bookkeeping.ase.bank_categorize.v1",
                    dag_id="bookkeeping_bank_categorization_v1",
                    request_semantic_digest="digest-b4",
                    state_revision_at_decision=1,
                    persistence_revision_at_decision=1,
                    created_at=datetime.now(timezone.utc),
                )

        # Valid CLASSIFIED decision succeeds
        ok_dec = BookkeepingResidualBankClassificationDecision.objects.create(
            id="dec-b-ok",
            entity=self.entity,
            staged_transaction=self.staged_tx,
            bank_account=self.bank_account,
            status="CLASSIFIED",
            account=self.expense_account,
            account_code="6111",
            confidence=0.99,
            rationale="Standard office supplies purchase",
            evidence_refs=["ref-1"],
            original_amount_units=1500000,
            residual_amount_units=1500000,
            direction="OUTFLOW",
            currency="USD",
            schema_version="bookkeeping.ase.bank_categorize.v1",
            dag_id="bookkeeping_bank_categorization_v1",
            request_semantic_digest="digest-b-ok",
            state_revision_at_decision=1,
            persistence_revision_at_decision=1,
            created_at=datetime.now(timezone.utc),
        )
        self.assertIsNotNone(ok_dec.pk)
        ok_dec.delete()

    # ------------------------------------------------------------------
    # Test C: HOLD DB check constraints
    # ------------------------------------------------------------------
    def test_c_hold_db_constraints(self) -> None:
        # HOLD with account / account_code fails
        with self.assertRaises(IntegrityError):
            with transaction.atomic():
                BookkeepingResidualBankClassificationDecision.objects.create(
                    id="dec-c-fail-acc",
                    entity=self.entity,
                    staged_transaction=self.staged_tx,
                    bank_account=self.bank_account,
                    status="HOLD",
                    account=self.expense_account,
                    account_code="6111",
                    hold_reason="Unclear counterparty",
                    original_amount_units=1500000,
                    residual_amount_units=1500000,
                    direction="OUTFLOW",
                    currency="USD",
                    schema_version="bookkeeping.ase.bank_categorize.v1",
                    dag_id="bookkeeping_bank_categorization_v1",
                    request_semantic_digest="digest-c1",
                    state_revision_at_decision=1,
                    persistence_revision_at_decision=1,
                    created_at=datetime.now(timezone.utc),
                )

        # HOLD without hold_reason fails
        with self.assertRaises(IntegrityError):
            with transaction.atomic():
                BookkeepingResidualBankClassificationDecision.objects.create(
                    id="dec-c-fail-no-hold-reason",
                    entity=self.entity,
                    staged_transaction=self.staged_tx,
                    bank_account=self.bank_account,
                    status="HOLD",
                    hold_reason=None,
                    original_amount_units=1500000,
                    residual_amount_units=1500000,
                    direction="OUTFLOW",
                    currency="USD",
                    schema_version="bookkeeping.ase.bank_categorize.v1",
                    dag_id="bookkeeping_bank_categorization_v1",
                    request_semantic_digest="digest-c2",
                    state_revision_at_decision=1,
                    persistence_revision_at_decision=1,
                    created_at=datetime.now(timezone.utc),
                )

        # HOLD with null confidence succeeds
        hold_null_conf = BookkeepingResidualBankClassificationDecision.objects.create(
            id="dec-c-hold-null-conf",
            entity=self.entity,
            staged_transaction=self.staged_tx,
            bank_account=self.bank_account,
            status="HOLD",
            hold_reason="Missing receipt documentation",
            confidence=None,
            required_evidence=["RECEIPT_DOCUMENT"],
            original_amount_units=1500000,
            residual_amount_units=1500000,
            direction="OUTFLOW",
            currency="USD",
            schema_version="bookkeeping.ase.bank_categorize.v1",
            dag_id="bookkeeping_bank_categorization_v1",
            request_semantic_digest="digest-c3",
            state_revision_at_decision=1,
            persistence_revision_at_decision=1,
            created_at=datetime.now(timezone.utc),
        )
        self.assertIsNotNone(hold_null_conf.pk)
        hold_null_conf.delete()

        # HOLD with valid confidence succeeds
        hold_valid_conf = BookkeepingResidualBankClassificationDecision.objects.create(
            id="dec-c-hold-valid-conf",
            entity=self.entity,
            staged_transaction=self.staged_tx,
            bank_account=self.bank_account,
            status="HOLD",
            hold_reason="Borderline confidence score",
            confidence=0.85,
            original_amount_units=1500000,
            residual_amount_units=1500000,
            direction="OUTFLOW",
            currency="USD",
            schema_version="bookkeeping.ase.bank_categorize.v1",
            dag_id="bookkeeping_bank_categorization_v1",
            request_semantic_digest="digest-c4",
            state_revision_at_decision=1,
            persistence_revision_at_decision=1,
            created_at=datetime.now(timezone.utc),
        )
        self.assertIsNotNone(hold_valid_conf.pk)
        hold_valid_conf.delete()

    # ------------------------------------------------------------------
    # Test D: Amount check constraints (residual <= original, positive)
    # ------------------------------------------------------------------
    def test_d_amount_constraints(self) -> None:
        # original_amount_units <= 0 fails
        with self.assertRaises(IntegrityError):
            with transaction.atomic():
                BookkeepingResidualBankClassificationDecision.objects.create(
                    id="dec-d-fail-orig-zero",
                    entity=self.entity,
                    staged_transaction=self.staged_tx,
                    bank_account=self.bank_account,
                    status="HOLD",
                    hold_reason="Testing amounts",
                    original_amount_units=0,
                    residual_amount_units=0,
                    direction="OUTFLOW",
                    currency="USD",
                    schema_version="v1",
                    dag_id="dag1",
                    request_semantic_digest="digest-d1",
                    state_revision_at_decision=1,
                    persistence_revision_at_decision=1,
                    created_at=datetime.now(timezone.utc),
                )

        # residual > original fails
        with self.assertRaises(IntegrityError):
            with transaction.atomic():
                BookkeepingResidualBankClassificationDecision.objects.create(
                    id="dec-d-fail-res-gt-orig",
                    entity=self.entity,
                    staged_transaction=self.staged_tx,
                    bank_account=self.bank_account,
                    status="HOLD",
                    hold_reason="Residual exceeds original",
                    original_amount_units=1000000,
                    residual_amount_units=1500000,
                    direction="OUTFLOW",
                    currency="USD",
                    schema_version="v1",
                    dag_id="dag1",
                    request_semantic_digest="digest-d2",
                    state_revision_at_decision=1,
                    persistence_revision_at_decision=1,
                    created_at=datetime.now(timezone.utc),
                )

    # ------------------------------------------------------------------
    # Test E: At most one root per staged transaction
    # ------------------------------------------------------------------
    def test_e_at_most_one_root_per_staged_transaction(self) -> None:
        root1 = BookkeepingResidualBankClassificationDecision.objects.create(
            id="dec-e-root1",
            entity=self.entity,
            staged_transaction=self.staged_tx,
            bank_account=self.bank_account,
            status="HOLD",
            hold_reason="Root 1",
            original_amount_units=1500000,
            residual_amount_units=1500000,
            direction="OUTFLOW",
            currency="USD",
            schema_version="v1",
            dag_id="dag1",
            request_semantic_digest="digest-e1",
            state_revision_at_decision=1,
            persistence_revision_at_decision=1,
            created_at=datetime.now(timezone.utc),
        )
        self.assertIsNotNone(root1.pk)

        # Second root for same staged_transaction fails uniq_res_cls_root
        with self.assertRaises(IntegrityError):
            with transaction.atomic():
                BookkeepingResidualBankClassificationDecision.objects.create(
                    id="dec-e-root2-conflict",
                    entity=self.entity,
                    staged_transaction=self.staged_tx,
                    bank_account=self.bank_account,
                    status="HOLD",
                    hold_reason="Root 2 conflicting",
                    original_amount_units=1500000,
                    residual_amount_units=1500000,
                    direction="OUTFLOW",
                    currency="USD",
                    schema_version="v1",
                    dag_id="dag1",
                    request_semantic_digest="digest-e2",
                    state_revision_at_decision=1,
                    persistence_revision_at_decision=1,
                    created_at=datetime.now(timezone.utc),
                )

        # Root on a different staged_transaction succeeds
        root_tx2 = BookkeepingResidualBankClassificationDecision.objects.create(
            id="dec-e-root-tx2",
            entity=self.entity,
            staged_transaction=self.staged_tx2,
            bank_account=self.bank_account,
            status="HOLD",
            hold_reason="Root for tx2",
            original_amount_units=5000000,
            residual_amount_units=5000000,
            direction="INFLOW",
            currency="USD",
            schema_version="v1",
            dag_id="dag1",
            request_semantic_digest="digest-e3",
            state_revision_at_decision=1,
            persistence_revision_at_decision=1,
            created_at=datetime.now(timezone.utc),
        )
        self.assertIsNotNone(root_tx2.pk)

        root1.delete()
        root_tx2.delete()

    # ------------------------------------------------------------------
    # Test F: One successor per predecessor / branch prevention
    # ------------------------------------------------------------------
    def test_f_one_successor_per_predecessor_branch_prevention(self) -> None:
        root = BookkeepingResidualBankClassificationDecision.objects.create(
            id="dec-f-root",
            entity=self.entity,
            staged_transaction=self.staged_tx,
            bank_account=self.bank_account,
            status="HOLD",
            hold_reason="Initial hold",
            original_amount_units=1500000,
            residual_amount_units=1500000,
            direction="OUTFLOW",
            currency="USD",
            schema_version="v1",
            dag_id="dag1",
            request_semantic_digest="digest-f1",
            state_revision_at_decision=1,
            persistence_revision_at_decision=1,
            created_at=datetime.now(timezone.utc),
        )

        successor1 = BookkeepingResidualBankClassificationDecision.objects.create(
            id="dec-f-succ1",
            entity=self.entity,
            staged_transaction=self.staged_tx,
            bank_account=self.bank_account,
            status="CLASSIFIED",
            account=self.expense_account,
            account_code="6111",
            confidence=0.99,
            supersedes=root,
            original_amount_units=1500000,
            residual_amount_units=1500000,
            direction="OUTFLOW",
            currency="USD",
            schema_version="v1",
            dag_id="dag1",
            request_semantic_digest="digest-f2",
            state_revision_at_decision=2,
            persistence_revision_at_decision=2,
            created_at=datetime.now(timezone.utc),
        )
        self.assertIsNotNone(successor1.pk)

        # Second successor to the same root fails OneToOneField unique constraint
        with self.assertRaises(IntegrityError):
            with transaction.atomic():
                BookkeepingResidualBankClassificationDecision.objects.create(
                    id="dec-f-succ2-branching",
                    entity=self.entity,
                    staged_transaction=self.staged_tx,
                    bank_account=self.bank_account,
                    status="HOLD",
                    hold_reason="Attempted branch",
                    supersedes=root,
                    original_amount_units=1500000,
                    residual_amount_units=1500000,
                    direction="OUTFLOW",
                    currency="USD",
                    schema_version="v1",
                    dag_id="dag1",
                    request_semantic_digest="digest-f3",
                    state_revision_at_decision=3,
                    persistence_revision_at_decision=3,
                    created_at=datetime.now(timezone.utc),
                )

        successor1.delete()
        root.delete()

    # ------------------------------------------------------------------
    # Test G: One invalidation per decision
    # ------------------------------------------------------------------
    def test_g_one_invalidation_per_decision(self) -> None:
        dec = BookkeepingResidualBankClassificationDecision.objects.create(
            id="dec-g-root",
            entity=self.entity,
            staged_transaction=self.staged_tx,
            bank_account=self.bank_account,
            status="HOLD",
            hold_reason="Testing invalidation",
            original_amount_units=1500000,
            residual_amount_units=1500000,
            direction="OUTFLOW",
            currency="USD",
            schema_version="v1",
            dag_id="dag1",
            request_semantic_digest="digest-g",
            state_revision_at_decision=1,
            persistence_revision_at_decision=1,
            created_at=datetime.now(timezone.utc),
        )

        inv1 = BookkeepingResidualBankClassificationInvalidation.objects.create(
            id="inv-g-1",
            classification=dec,
            reason="Retiring decision",
            state_revision_at_invalidation=2,
            persistence_revision_at_invalidation=2,
            created_at=datetime.now(timezone.utc),
        )
        self.assertIsNotNone(inv1.pk)

        # Second invalidation of same decision fails OneToOneField
        with self.assertRaises(IntegrityError):
            with transaction.atomic():
                BookkeepingResidualBankClassificationInvalidation.objects.create(
                    id="inv-g-2-duplicate",
                    classification=dec,
                    reason="Duplicate invalidation attempt",
                    state_revision_at_invalidation=3,
                    persistence_revision_at_invalidation=3,
                    created_at=datetime.now(timezone.utc),
                )

        inv1.delete()
        dec.delete()

    # ------------------------------------------------------------------
    # Test H: Multiple decisions with identical request_semantic_digest are allowed across linear history
    # ------------------------------------------------------------------
    def test_h_duplicate_request_semantic_digest_allowed_in_linear_history(self) -> None:
        shared_digest = "a" * 64
        d1 = BookkeepingResidualBankClassificationDecision.objects.create(
            id="dec-h-root",
            entity=self.entity,
            staged_transaction=self.staged_tx,
            bank_account=self.bank_account,
            status="HOLD",
            hold_reason="Initial hold",
            original_amount_units=1500000,
            residual_amount_units=1500000,
            direction="OUTFLOW",
            currency="USD",
            schema_version="v1",
            dag_id="dag1",
            request_semantic_digest=shared_digest,
            state_revision_at_decision=1,
            persistence_revision_at_decision=1,
            created_at=datetime.now(timezone.utc),
        )

        # Re-evaluating with same request_semantic_digest after invalidation or model upgrade
        d2 = BookkeepingResidualBankClassificationDecision.objects.create(
            id="dec-h-succ",
            entity=self.entity,
            staged_transaction=self.staged_tx,
            bank_account=self.bank_account,
            status="CLASSIFIED",
            account=self.expense_account,
            account_code="6111",
            confidence=0.99,
            supersedes=d1,
            original_amount_units=1500000,
            residual_amount_units=1500000,
            direction="OUTFLOW",
            currency="USD",
            schema_version="v1",
            dag_id="dag1",
            request_semantic_digest=shared_digest,
            state_revision_at_decision=2,
            persistence_revision_at_decision=2,
            created_at=datetime.now(timezone.utc),
        )
        self.assertIsNotNone(d2.pk)
        d2.delete()
        d1.delete()

    # ------------------------------------------------------------------
    # Test I: Hydration round-trip (Django -> Snapshot -> State)
    # ------------------------------------------------------------------
    def test_i_hydration_round_trip(self) -> None:
        d1 = BookkeepingResidualBankClassificationDecision.objects.create(
            id="dec-i-root",
            entity=self.entity,
            staged_transaction=self.staged_tx,
            bank_account=self.bank_account,
            status="HOLD",
            hold_reason="Need vendor receipt",
            required_evidence=["RECEIPT"],
            original_amount_units=1500000,
            residual_amount_units=1500000,
            direction="OUTFLOW",
            currency="USD",
            schema_version="v1",
            dag_id="dag1",
            request_semantic_digest="digest-i1",
            state_revision_at_decision=1,
            persistence_revision_at_decision=1,
            created_at=datetime.now(timezone.utc),
        )
        d2 = BookkeepingResidualBankClassificationDecision.objects.create(
            id="dec-i-succ",
            entity=self.entity,
            staged_transaction=self.staged_tx,
            bank_account=self.bank_account,
            status="CLASSIFIED",
            account=self.expense_account,
            account_code="6111",
            confidence=0.99,
            rationale="Office supplies categorized",
            evidence_refs=["ref-receipt-1"],
            supersedes=d1,
            original_amount_units=1500000,
            residual_amount_units=1500000,
            direction="OUTFLOW",
            currency="USD",
            schema_version="v1",
            dag_id="dag1",
            request_semantic_digest="digest-i2",
            state_revision_at_decision=2,
            persistence_revision_at_decision=2,
            created_at=datetime.now(timezone.utc),
        )

        inv = BookkeepingResidualBankClassificationInvalidation.objects.create(
            id="inv-i-root",
            classification=d1,
            reason="Superseded by classified decision",
            state_revision_at_invalidation=2,
            persistence_revision_at_invalidation=2,
            created_at=datetime.now(timezone.utc),
        )

        snapshot = read_django_snapshot(company_id=str(self.entity.uuid))
        self.assertEqual(len(snapshot.residual_bank_classifications), 2)
        self.assertEqual(len(snapshot.residual_bank_classification_invalidations), 1)

        hydrator = BookkeepingHydrator(repository=BookkeepingRepository())
        state = hydrator.hydrate(company_id=str(self.entity.uuid))
        try:
            self.assertEqual(len(state.residual_bank_classifications), 2)
            self.assertEqual(len(state.residual_bank_classification_invalidations), 1)
            hydrated_d2 = state.get_residual_bank_classification(d2.id)
            self.assertIsNotNone(hydrated_d2)
            assert hydrated_d2 is not None
            self.assertEqual(hydrated_d2.account_code, "6111")
            self.assertEqual(hydrated_d2.confidence, 0.99)
            self.assertEqual(hydrated_d2.supersedes_decision_id, d1.id)
        finally:
            state.close()

        inv.delete()
        d2.delete()
        d1.delete()

    # ------------------------------------------------------------------
    # Test J: Active decision resolves to non-invalidated chain tip
    # ------------------------------------------------------------------
    def test_j_active_decision_resolves_to_non_invalidated_chain_tip(self) -> None:
        d1 = BookkeepingResidualBankClassificationDecision.objects.create(
            id="dec-j-1",
            entity=self.entity,
            staged_transaction=self.staged_tx,
            bank_account=self.bank_account,
            status="HOLD",
            hold_reason="Initial hold",
            original_amount_units=1500000,
            residual_amount_units=1500000,
            direction="OUTFLOW",
            currency="USD",
            schema_version="v1",
            dag_id="dag1",
            request_semantic_digest="digest-j1",
            state_revision_at_decision=1,
            persistence_revision_at_decision=1,
            created_at=datetime.now(timezone.utc),
        )
        d2 = BookkeepingResidualBankClassificationDecision.objects.create(
            id="dec-j-2",
            entity=self.entity,
            staged_transaction=self.staged_tx,
            bank_account=self.bank_account,
            status="CLASSIFIED",
            account=self.expense_account,
            account_code="6111",
            confidence=0.99,
            supersedes=d1,
            original_amount_units=1500000,
            residual_amount_units=1500000,
            direction="OUTFLOW",
            currency="USD",
            schema_version="v1",
            dag_id="dag1",
            request_semantic_digest="digest-j2",
            state_revision_at_decision=2,
            persistence_revision_at_decision=2,
            created_at=datetime.now(timezone.utc),
        )

        hydrator = BookkeepingHydrator(repository=BookkeepingRepository())
        state = hydrator.hydrate(company_id=str(self.entity.uuid))
        try:
            queries = BookkeepingQueries(state)
            bank_item_id = f"staged:{self.staged_tx.uuid}"

            history = queries.residual_bank_classification_history(bank_item_id)
            self.assertEqual(len(history), 2)
            self.assertEqual(history[0].id, d1.id)
            self.assertEqual(history[1].id, d2.id)

            latest = queries.latest_residual_bank_classification(bank_item_id)
            self.assertIsNotNone(latest)
            assert latest is not None
            self.assertEqual(latest.id, d2.id)

            active = queries.active_residual_bank_classification(bank_item_id)
            self.assertIsNotNone(active)
            assert active is not None
            self.assertEqual(active.id, d2.id)
            self.assertEqual(active.status, ResidualBankClassificationStatus.CLASSIFIED)
        finally:
            state.close()

        d2.delete()
        d1.delete()

    # ------------------------------------------------------------------
    # Test K: Invalidated tip yields no active decision
    # ------------------------------------------------------------------
    def test_k_invalidated_tip_yields_no_active_decision(self) -> None:
        d1 = BookkeepingResidualBankClassificationDecision.objects.create(
            id="dec-k-1",
            entity=self.entity,
            staged_transaction=self.staged_tx,
            bank_account=self.bank_account,
            status="HOLD",
            hold_reason="Initial hold",
            original_amount_units=1500000,
            residual_amount_units=1500000,
            direction="OUTFLOW",
            currency="USD",
            schema_version="v1",
            dag_id="dag1",
            request_semantic_digest="digest-k1",
            state_revision_at_decision=1,
            persistence_revision_at_decision=1,
            created_at=datetime.now(timezone.utc),
        )
        d2 = BookkeepingResidualBankClassificationDecision.objects.create(
            id="dec-k-2",
            entity=self.entity,
            staged_transaction=self.staged_tx,
            bank_account=self.bank_account,
            status="CLASSIFIED",
            account=self.expense_account,
            account_code="6111",
            confidence=0.99,
            supersedes=d1,
            original_amount_units=1500000,
            residual_amount_units=1500000,
            direction="OUTFLOW",
            currency="USD",
            schema_version="v1",
            dag_id="dag1",
            request_semantic_digest="digest-k2",
            state_revision_at_decision=2,
            persistence_revision_at_decision=2,
            created_at=datetime.now(timezone.utc),
        )
        inv = BookkeepingResidualBankClassificationInvalidation.objects.create(
            id="inv-k-2",
            classification=d2,
            reason="Retiring D2 because counterparty disputed invoice",
            state_revision_at_invalidation=3,
            persistence_revision_at_invalidation=3,
            created_at=datetime.now(timezone.utc),
        )

        hydrator = BookkeepingHydrator(repository=BookkeepingRepository())
        state = hydrator.hydrate(company_id=str(self.entity.uuid))
        try:
            queries = BookkeepingQueries(state)
            bank_item_id = f"staged:{self.staged_tx.uuid}"

            # Invalidated tip -> no active decision
            active = queries.active_residual_bank_classification(bank_item_id)
            self.assertIsNone(active)

            # Latest is still D2
            latest = queries.latest_residual_bank_classification(bank_item_id)
            self.assertIsNotNone(latest)
            assert latest is not None
            self.assertEqual(latest.id, d2.id)

            # D1 does NOT resurrect
            history = queries.residual_bank_classification_history(bank_item_id)
            self.assertEqual(len(history), 2)
        finally:
            state.close()

        inv.delete()
        d2.delete()
        d1.delete()

    # ------------------------------------------------------------------
    # Test L: Successor after invalidated tip becomes new active decision
    # ------------------------------------------------------------------
    def test_l_successor_after_invalidated_tip_becomes_new_active_decision(self) -> None:
        d1 = BookkeepingResidualBankClassificationDecision.objects.create(
            id="dec-l-1",
            entity=self.entity,
            staged_transaction=self.staged_tx,
            bank_account=self.bank_account,
            status="HOLD",
            hold_reason="Initial hold",
            original_amount_units=1500000,
            residual_amount_units=1500000,
            direction="OUTFLOW",
            currency="USD",
            schema_version="v1",
            dag_id="dag1",
            request_semantic_digest="digest-l1",
            state_revision_at_decision=1,
            persistence_revision_at_decision=1,
            created_at=datetime.now(timezone.utc),
        )
        d2 = BookkeepingResidualBankClassificationDecision.objects.create(
            id="dec-l-2",
            entity=self.entity,
            staged_transaction=self.staged_tx,
            bank_account=self.bank_account,
            status="HOLD",
            hold_reason="Second hold",
            supersedes=d1,
            original_amount_units=1500000,
            residual_amount_units=1500000,
            direction="OUTFLOW",
            currency="USD",
            schema_version="v1",
            dag_id="dag1",
            request_semantic_digest="digest-l2",
            state_revision_at_decision=2,
            persistence_revision_at_decision=2,
            created_at=datetime.now(timezone.utc),
        )
        inv = BookkeepingResidualBankClassificationInvalidation.objects.create(
            id="inv-l-2",
            classification=d2,
            reason="Retiring D2",
            state_revision_at_invalidation=3,
            persistence_revision_at_invalidation=3,
            created_at=datetime.now(timezone.utc),
        )
        # D3 appended after invalidated D2
        d3 = BookkeepingResidualBankClassificationDecision.objects.create(
            id="dec-l-3",
            entity=self.entity,
            staged_transaction=self.staged_tx,
            bank_account=self.bank_account,
            status="CLASSIFIED",
            account=self.expense_account,
            account_code="6111",
            confidence=0.99,
            supersedes=d2,
            original_amount_units=1500000,
            residual_amount_units=1500000,
            direction="OUTFLOW",
            currency="USD",
            schema_version="v1",
            dag_id="dag1",
            request_semantic_digest="digest-l3",
            state_revision_at_decision=4,
            persistence_revision_at_decision=4,
            created_at=datetime.now(timezone.utc),
        )

        hydrator = BookkeepingHydrator(repository=BookkeepingRepository())
        state = hydrator.hydrate(company_id=str(self.entity.uuid))
        try:
            queries = BookkeepingQueries(state)
            bank_item_id = f"staged:{self.staged_tx.uuid}"

            active = queries.active_residual_bank_classification(bank_item_id)
            self.assertIsNotNone(active)
            assert active is not None
            self.assertEqual(active.id, d3.id)
            self.assertEqual(active.status, ResidualBankClassificationStatus.CLASSIFIED)

            latest = queries.latest_residual_bank_classification(bank_item_id)
            self.assertIsNotNone(latest)
            assert latest is not None
            self.assertEqual(latest.id, d3.id)

            history = queries.residual_bank_classification_history(bank_item_id)
            self.assertEqual(len(history), 3)
            self.assertEqual([h.id for h in history], [d1.id, d2.id, d3.id])
        finally:
            state.close()

        d3.delete()
        inv.delete()
        d2.delete()
        d1.delete()

    # ------------------------------------------------------------------
    # Test M: Malformed branching / multiple roots detected by derived state
    # ------------------------------------------------------------------
    def test_m_malformed_history_detected_by_derived_validation(self) -> None:
        bank_item_id = f"staged:{self.staged_tx.uuid}"
        now = datetime.now(timezone.utc)

        # 1. Multiple roots for same item
        root_a = ResidualBankClassificationDecision(
            id="dec-m-root-a",
            bank_item_id=bank_item_id,
            staged_transaction_id=str(self.staged_tx.uuid),
            bank_account_id=str(self.bank_account.uuid),
            status=ResidualBankClassificationStatus.HOLD,
            hold_reason="Root A",
            original_amount_units=1500000,
            residual_amount_units=1500000,
            direction=Direction.OUTFLOW,
            currency="USD",
            schema_version="v1",
            dag_id="dag1",
            request_semantic_digest="digest-m1",
            state_revision_at_decision=1,
            persistence_revision_at_decision=1,
            created_at=now,
        )
        root_b = ResidualBankClassificationDecision(
            id="dec-m-root-b",
            bank_item_id=bank_item_id,
            staged_transaction_id=str(self.staged_tx.uuid),
            bank_account_id=str(self.bank_account.uuid),
            status=ResidualBankClassificationStatus.HOLD,
            hold_reason="Root B",
            original_amount_units=1500000,
            residual_amount_units=1500000,
            direction=Direction.OUTFLOW,
            currency="USD",
            schema_version="v1",
            dag_id="dag1",
            request_semantic_digest="digest-m2",
            state_revision_at_decision=1,
            persistence_revision_at_decision=1,
            created_at=now,
        )

        hydrator = BookkeepingHydrator(repository=BookkeepingRepository())
        state = hydrator.hydrate(company_id=str(self.entity.uuid))
        try:
            # Injecting multiple roots into state
            state._residual_bank_classifications = {
                root_a.id: root_a,
                root_b.id: root_b,
            }
            queries = BookkeepingQueries(state)
            with self.assertRaises(DerivedStateError) as ctx:
                _ = queries.derived
            self.assertIn("Multiple history roots found", str(ctx.exception))
        finally:
            state.close()

    # ------------------------------------------------------------------
    # Test N: State fingerprint is deterministic and reflects residual classifications
    # ------------------------------------------------------------------
    def test_n_state_fingerprint_deterministic_and_reflects_residual_classifications(self) -> None:
        hydrator = BookkeepingHydrator(repository=BookkeepingRepository())
        state_empty = hydrator.hydrate(company_id=str(self.entity.uuid))
        fp_empty = state_fingerprint(state_empty)
        state_empty.close()

        # Add a decision
        d = BookkeepingResidualBankClassificationDecision.objects.create(
            id="dec-n-1",
            entity=self.entity,
            staged_transaction=self.staged_tx,
            bank_account=self.bank_account,
            status="HOLD",
            hold_reason="Testing fingerprint",
            original_amount_units=1500000,
            residual_amount_units=1500000,
            direction="OUTFLOW",
            currency="USD",
            schema_version="v1",
            dag_id="dag1",
            request_semantic_digest="digest-n",
            state_revision_at_decision=1,
            persistence_revision_at_decision=1,
            created_at=datetime.now(timezone.utc),
        )

        state_with_d = hydrator.hydrate(company_id=str(self.entity.uuid))
        fp_with_d = state_fingerprint(state_with_d)
        state_with_d.close()

        self.assertNotEqual(fp_empty, fp_with_d)

        # Deterministic: re-hydrating gives same fingerprint
        state_with_d_2 = hydrator.hydrate(company_id=str(self.entity.uuid))
        fp_with_d_2 = state_fingerprint(state_with_d_2)
        state_with_d_2.close()

        self.assertEqual(fp_with_d, fp_with_d_2)

        d.delete()

    # ------------------------------------------------------------------
    # Test O: Legacy ClassificationDecision behavior remains unchanged
    # ------------------------------------------------------------------
    def test_o_legacy_classification_unaffected(self) -> None:
        legacy = BookkeepingClassificationDecision.objects.create(
            id="legacy-cls-1",
            entity=self.entity,
            transaction=self.transaction,
            account_code="6111",
            account=self.expense_account,
            source="RULE",
            confidence=1.0,
            state_revision_at_decision=1,
            created_at=datetime.now(timezone.utc),
        )
        self.assertIsNotNone(legacy.pk)
        legacy.delete()

    # ------------------------------------------------------------------
    # Test P: No Plaid / invoice / bill / posted-transaction target accepted
    # ------------------------------------------------------------------
    def test_p_strictly_targets_staged_transaction(self) -> None:
        model_fields = [f.name for f in BookkeepingResidualBankClassificationDecision._meta.get_fields()]
        self.assertIn("staged_transaction", model_fields)
        self.assertNotIn("plaid_transaction", model_fields)
        self.assertNotIn("invoice", model_fields)
        self.assertNotIn("bill", model_fields)
        self.assertNotIn("transaction", model_fields)

    # ------------------------------------------------------------------
    # Test Q: Default-CoA membership & snapshot code validation
    # ------------------------------------------------------------------
    def test_q_default_coa_membership_and_code_validation(self) -> None:
        # Create an account on a DIFFERENT non-default chart of accounts
        second_coa = self.entity.create_chart_of_accounts(
            coa_name="Secondary CoA",
            assign_as_default=False,
            commit=True,
        )
        sec_root = second_coa.accountmodel_set.get(code="06000000")
        foreign_expense = sec_root.add_child(
            coa_model=second_coa,
            code="6999",
            name="Foreign Expense",
            role=EXPENSE_OPERATIONAL,
            balance_type=DEBIT,
            active=True,
        )

        dec_foreign_coa = BookkeepingResidualBankClassificationDecision.objects.create(
            id="dec-q-foreign-coa",
            entity=self.entity,
            staged_transaction=self.staged_tx,
            bank_account=self.bank_account,
            status="CLASSIFIED",
            account=foreign_expense,
            account_code="6999",
            confidence=0.99,
            original_amount_units=1500000,
            residual_amount_units=1500000,
            direction="OUTFLOW",
            currency="USD",
            schema_version="v1",
            dag_id="dag1",
            request_semantic_digest="digest-q1",
            state_revision_at_decision=1,
            persistence_revision_at_decision=1,
            created_at=datetime.now(timezone.utc),
        )

        with self.assertRaises(PersistenceError) as ctx:
            read_django_snapshot(company_id=str(self.entity.uuid))
        self.assertIn("not belonging to entity default_coa", str(ctx.exception))

        dec_foreign_coa.delete()

        # Inactive account on default CoA fails
        expense_root = self.coa.accountmodel_set.get(code="06000000")
        inactive_expense = expense_root.add_child(
            coa_model=self.coa,
            code="6888",
            name="Inactive Expense",
            role=EXPENSE_OPERATIONAL,
            balance_type=DEBIT,
            active=False,
        )
        dec_inactive = BookkeepingResidualBankClassificationDecision.objects.create(
            id="dec-q-inactive",
            entity=self.entity,
            staged_transaction=self.staged_tx,
            bank_account=self.bank_account,
            status="CLASSIFIED",
            account=inactive_expense,
            account_code="6888",
            confidence=0.99,
            original_amount_units=1500000,
            residual_amount_units=1500000,
            direction="OUTFLOW",
            currency="USD",
            schema_version="v1",
            dag_id="dag1",
            request_semantic_digest="digest-q-inact",
            state_revision_at_decision=1,
            persistence_revision_at_decision=1,
            created_at=datetime.now(timezone.utc),
        )
        with self.assertRaises(PersistenceError) as ctx:
            read_django_snapshot(company_id=str(self.entity.uuid))
        self.assertIn("references inactive account", str(ctx.exception))

        dec_inactive.delete()
        inactive_expense.delete()

        # Code mismatch: decision account_code != account.code
        dec_code_mismatch = BookkeepingResidualBankClassificationDecision.objects.create(
            id="dec-q-code-mismatch",
            entity=self.entity,
            staged_transaction=self.staged_tx,
            bank_account=self.bank_account,
            status="CLASSIFIED",
            account=self.expense_account,
            account_code="WRONG_CODE",
            confidence=0.99,
            original_amount_units=1500000,
            residual_amount_units=1500000,
            direction="OUTFLOW",
            currency="USD",
            schema_version="v1",
            dag_id="dag1",
            request_semantic_digest="digest-q2",
            state_revision_at_decision=1,
            persistence_revision_at_decision=1,
            created_at=datetime.now(timezone.utc),
        )

        with self.assertRaises(PersistenceError) as ctx:
            read_django_snapshot(company_id=str(self.entity.uuid))
        self.assertIn("does not match linked AccountModel code", str(ctx.exception))

        dec_code_mismatch.delete()

    # ------------------------------------------------------------------
    # Test R: Strict bank_account equality between decision and staged transaction
    # ------------------------------------------------------------------
    def test_r_strict_bank_account_equality(self) -> None:
        # Create a second bank account
        other_cash = self.coa.accountmodel_set.get(code="01000000").add_child(
            coa_model=self.coa,
            code="1011",
            name="Secondary Cash",
            role=ASSET_CA_CASH,
            balance_type=DEBIT,
            active=True,
        )
        second_ba = BankAccountModel.objects.create(
            entity_model=self.entity,
            account_model=other_cash,
            name="Secondary Account",
            active=True,
        )

        # Decision references second_ba, but staged_tx belongs to self.bank_account
        dec_ba_mismatch = BookkeepingResidualBankClassificationDecision.objects.create(
            id="dec-r-mismatch",
            entity=self.entity,
            staged_transaction=self.staged_tx,
            bank_account=second_ba,
            status="HOLD",
            hold_reason="Testing mismatch",
            original_amount_units=1500000,
            residual_amount_units=1500000,
            direction="OUTFLOW",
            currency="USD",
            schema_version="v1",
            dag_id="dag1",
            request_semantic_digest="digest-r",
            state_revision_at_decision=1,
            persistence_revision_at_decision=1,
            created_at=datetime.now(timezone.utc),
        )

        with self.assertRaises(PersistenceError) as ctx:
            read_django_snapshot(company_id=str(self.entity.uuid))
        self.assertIn("does not match staged transaction's import job bank account", str(ctx.exception))

        dec_ba_mismatch.delete()

    # ------------------------------------------------------------------
    # Test S: Direction structural validation
    # ------------------------------------------------------------------
    def test_s_direction_structural_validation(self) -> None:
        # Arbitrary direction fails DB check constraint
        with self.assertRaises(IntegrityError):
            with transaction.atomic():
                BookkeepingResidualBankClassificationDecision.objects.create(
                    id="dec-s-fail-dir",
                    entity=self.entity,
                    staged_transaction=self.staged_tx,
                    bank_account=self.bank_account,
                    status="HOLD",
                    hold_reason="Testing direction",
                    original_amount_units=1500000,
                    residual_amount_units=1500000,
                    direction="ARBITRARY_DIRECTION",
                    currency="USD",
                    schema_version="v1",
                    dag_id="dag1",
                    request_semantic_digest="digest-s",
                    state_revision_at_decision=1,
                    persistence_revision_at_decision=1,
                    created_at=datetime.now(timezone.utc),
                )

        # INFLOW succeeds
        dec_in = BookkeepingResidualBankClassificationDecision.objects.create(
            id="dec-s-inflow",
            entity=self.entity,
            staged_transaction=self.staged_tx,
            bank_account=self.bank_account,
            status="HOLD",
            hold_reason="Testing inflow",
            original_amount_units=1500000,
            residual_amount_units=1500000,
            direction="INFLOW",
            currency="USD",
            schema_version="v1",
            dag_id="dag1",
            request_semantic_digest="digest-s1",
            state_revision_at_decision=1,
            persistence_revision_at_decision=1,
            created_at=datetime.now(timezone.utc),
        )
        self.assertIsNotNone(dec_in.pk)
        dec_in.delete()
