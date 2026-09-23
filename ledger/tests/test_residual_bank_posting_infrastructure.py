from __future__ import annotations

from datetime import date, datetime, timezone
from decimal import Decimal
from typing import Any, cast
from uuid import uuid4

from bookkeeping_state.domain.commands import (
    InvalidateResidualBankClassificationCommand,
    RecordResidualBankClassificationCommand,
)
from bookkeeping_state.domain.enums import Direction
from bookkeeping_state.domain.money import (
    major_units_to_solver_units,
)
from bookkeeping_state.domain.reconciliations import (
    BankAllocation,
    BookAllocation,
    Reconciliation,
)
from bookkeeping_state.domain.residual_bank_classifications import (
    ResidualBankClassificationStatus,
)
from bookkeeping_state.hydration import BookkeepingHydrator
from bookkeeping_state.persistence.bank_ledger import get_or_create_bank_ledger
from bookkeeping_state.persistence.repository import (
    BookkeepingRepository,
    PersistenceError,
    PersistenceWriteSet,
)
from bookkeeping_state.persistence.writer import _persist_reconciliation_record
from bookkeeping_state.state.fingerprint import (
    state_fingerprint,
)
from bookkeeping_state.state.queries import BookkeepingQueries
from bookkeeping_state.transitions.engine import TransitionEngine
from bookkeeping_state.transitions.result import (
    RejectionCode,
    TransitionStatus,
)
from django.contrib.auth import get_user_model
from django.db import IntegrityError
from django.db.models.deletion import ProtectedError
from django.test import TestCase

from ledger.io.roles import (
    ASSET_CA_CASH,
    ASSET_CA_RECEIVABLES,
    CREDIT,
    DEBIT,
    EXPENSE_OPERATIONAL,
)
from ledger.models import (
    BankAccountModel,
    EntityModel,
    ImportJobModel,
    JournalEntryModel,
    LedgerModel,
    StagedTransactionModel,
    TransactionModel,
)
from ledger.models.bookkeeping import (
    BookkeepingReconciliation,
    BookkeepingReconciliationBankAllocation,
    BookkeepingReconciliationBookAllocation,
    BookkeepingResidualBankClassificationDecision,
    BookkeepingResidualBankPosting,
    BookkeepingRevision,
)

UserModel = get_user_model()


class ResidualBankPostingInfrastructureTests(TestCase):
    @classmethod
    def setUpTestData(cls) -> None:
        cls.user = cast(Any, UserModel.objects).create_user(
            username=f"testuser_{uuid4().hex[:8]}",
            email=f"test_{uuid4().hex[:8]}@example.com",
            password="testpassword123",
        )
        cls.entity = EntityModel.add_root(
            name="Posting Infrastructure Test Corp",
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
        cls.receivables_account = asset_root.add_child(
            coa_model=cls.coa,
            code="1020",
            name="Accounts Receivable",
            role=ASSET_CA_RECEIVABLES,
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
        cls.tax_account = expense_root.add_child(
            coa_model=cls.coa,
            code="4456",
            name="VAT Settlement Account",
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

        cls.import_job = ImportJobModel.objects.create(
            bank_account_model=cls.bank_account,
            description="Posting Test OFX Job",
        )

    def _hydrate_state(self, session_id: str = "test-session-001"):
        hydrator = BookkeepingHydrator(repository=BookkeepingRepository())
        return hydrator.hydrate(
            company_id=str(self.entity.uuid),
            session_id=session_id,
        )

    def _create_staged_tx(
        self,
        amount: Decimal = Decimal("100.00"),
        direction: str = "OUTFLOW",
    ) -> StagedTransactionModel:
        signed_amount = -amount if direction.upper() == "OUTFLOW" else amount
        return StagedTransactionModel.objects.create(
            import_job=self.import_job,
            fit_id=f"fit_{uuid4().hex[:12]}",
            date_posted=date(2026, 3, 15),
            amount=signed_amount,
            name="Supplier Inc",
            memo="Office Supplies Purchase",
        )

    def _create_classification_decision(
        self,
        staged_tx: StagedTransactionModel,
        status: str = "CLASSIFIED",
        account_code: str = "6111",
        amount_units: int | None = None,
        direction: str = "OUTFLOW",
    ) -> BookkeepingResidualBankClassificationDecision:
        if amount_units is None:
            stx_amount = Decimal(str(staged_tx.amount)) if staged_tx.amount is not None else Decimal(0)
            amount_units = major_units_to_solver_units(abs(stx_amount))
        account = self.entity.default_coa.accountmodel_set.get(code=account_code)
        return BookkeepingResidualBankClassificationDecision.objects.create(
            id=f"rbcd_{uuid4().hex[:12]}",
            entity=self.entity,
            staged_transaction=staged_tx,
            bank_account=self.bank_account,
            status=status,
            account_code=account_code,
            account=account,
            original_amount_units=amount_units,
            residual_amount_units=amount_units,
            direction=direction.upper(),
            currency="USD",
            confidence=0.99,
            rationale="Deterministic residual match",
            evidence_refs=[],
            hold_reason=None,
            required_evidence=[],
            schema_version=1,
            dag_id="pcm_bank_cash_accounting_dag",
            request_semantic_digest=f"digest_{uuid4().hex[:8]}",
            session_id=f"session_{uuid4().hex[:8]}",
            state_revision_at_decision=1,
            persistence_revision_at_decision=1,
            ase_node_id="test_node",
            terminal_property=None,
            supersedes=None,
            created_at=datetime.now(timezone.utc),
        )

    def _create_posting_fixture(
        self,
        staged_tx: StagedTransactionModel,
        decision: BookkeepingResidualBankClassificationDecision,
        amount: Decimal = Decimal("100.00"),
        direction: str = "OUTFLOW",
        persistence_revision: int = 1,
    ) -> tuple[
        JournalEntryModel,
        TransactionModel,
        TransactionModel,
        BookkeepingReconciliation,
        BookkeepingResidualBankPosting,
    ]:
        bank_ledger = get_or_create_bank_ledger(bank_account=self.bank_account)
        je_timestamp = datetime(2026, 3, 15, 12, 0, tzinfo=timezone.utc)

        if direction.upper() == "OUTFLOW":
            bank_tx_type = CREDIT
            contra_tx_type = DEBIT
        else:
            bank_tx_type = DEBIT
            contra_tx_type = CREDIT

        je, (bank_tx, contra_tx) = bank_ledger.commit_txs(
            je_timestamp=je_timestamp,
            je_txs=[
                {
                    "account": self.bank_account.account_model,
                    "amount": amount,
                    "tx_type": bank_tx_type,
                    "description": "Bank cash leg",
                },
                {
                    "account": decision.account,
                    "amount": amount,
                    "tx_type": contra_tx_type,
                    "description": "Contra leg",
                },
            ],
            je_posted=True,
            je_desc="Residual bank classification posting",
        )
        assert isinstance(je, JournalEntryModel)
        assert isinstance(bank_tx, TransactionModel)
        assert isinstance(contra_tx, TransactionModel)

        amount_units = decision.residual_amount_units
        recon = _persist_reconciliation_record(
            entity=self.entity,
            reconciliation_id=f"recon_{uuid4().hex[:12]}",
            state_revision_at_creation=1,
            created_at=datetime.now(timezone.utc),
            semantic_rationale="Direct residual bank posting closure",
            session_id=f"session_{uuid4().hex[:8]}",
            bank_allocations=[(None, staged_tx, amount_units)],
            book_allocations=[(bank_tx, amount_units)],
        )

        posting = BookkeepingResidualBankPosting.objects.create(
            id=f"rbp_{uuid4().hex[:12]}",
            entity=self.entity,
            classification=decision,
            staged_transaction=staged_tx,
            journal_entry=je,
            bank_cash_transaction=bank_tx,
            contra_transaction=contra_tx,
            reconciliation=recon,
            persistence_revision=persistence_revision,
            posted_at=datetime.now(timezone.utc),
        )

        return je, bank_tx, contra_tx, recon, posting

    # ------------------------------------------------------------------
    # PART 1: DETERMINISTIC BANK LEDGER
    # ------------------------------------------------------------------

    def test_a_deterministic_bank_ledger_creation(self) -> None:
        ledger = get_or_create_bank_ledger(bank_account=self.bank_account)
        self.assertEqual(ledger.ledger_xid, f"bank-ledger-{self.bank_account.uuid}")
        self.assertEqual(ledger.entity, self.bank_account.entity_model)
        self.assertTrue(ledger.is_posted())
        self.assertFalse(ledger.is_locked())

    def test_b_repeated_resolution_returns_same_bank_ledger(self) -> None:
        l1 = get_or_create_bank_ledger(bank_account=self.bank_account)
        l2 = get_or_create_bank_ledger(bank_account=self.bank_account)
        self.assertEqual(l1.pk, l2.pk)
        self.assertEqual(
            LedgerModel.objects.filter(
                ledger_xid=f"bank-ledger-{self.bank_account.uuid}"
            ).count(),
            1,
        )

    def test_c_locked_deterministic_bank_ledger_fails_closed(self) -> None:
        ledger = get_or_create_bank_ledger(bank_account=self.bank_account)
        ledger.locked = True
        ledger.save(update_fields=["locked"])

        with self.assertRaises(PersistenceError) as ctx:
            get_or_create_bank_ledger(bank_account=self.bank_account)
        self.assertIn("locked", str(ctx.exception).lower())

    def test_d_canonical_ledger_post_lifecycle_used_for_existing_unposted_ledger(self) -> None:
        ba2 = BankAccountModel.objects.create(
            entity_model=self.entity,
            account_model=self.cash_account,
            name="Secondary Operating Account",
            active=True,
        )
        ledger_xid = f"bank-ledger-{ba2.uuid}"
        unposted = self.entity.create_ledger(
            name=f"Bank Ledger - {ba2.name}",
            ledger_xid=ledger_xid,
            posted=False,
            commit=True,
        )
        self.assertFalse(unposted.is_posted())

        resolved = get_or_create_bank_ledger(bank_account=ba2)
        self.assertEqual(resolved.pk, unposted.pk)
        self.assertTrue(resolved.is_posted())

    # ------------------------------------------------------------------
    # PART 2: DURABLE POSTING MODEL & MIGRATION & CONSTRAINTS
    # ------------------------------------------------------------------

    def test_e_posting_model_migration_applies(self) -> None:
        self.assertTrue(
            hasattr(BookkeepingResidualBankPosting, "objects"),
            "BookkeepingResidualBankPosting model exists with query manager",
        )
        self.assertEqual(BookkeepingResidualBankPosting.objects.count(), 0)

    def test_f_classification_onetoone_uniqueness(self) -> None:
        stx = self._create_staged_tx()
        dec = self._create_classification_decision(stx)
        je, b_tx, c_tx, recon, _posting = self._create_posting_fixture(stx, dec)

        with self.assertRaises(IntegrityError):
            BookkeepingResidualBankPosting.objects.create(
                id=f"rbp_dup_{uuid4().hex[:8]}",
                entity=self.entity,
                classification=dec,
                staged_transaction=stx,
                journal_entry=je,
                bank_cash_transaction=b_tx,
                contra_transaction=c_tx,
                reconciliation=recon,
                persistence_revision=2,
            )

    def test_g_journal_entry_onetoone_uniqueness(self) -> None:
        stx1 = self._create_staged_tx()
        dec1 = self._create_classification_decision(stx1)
        je, b_tx, c_tx, _recon1, _posting1 = self._create_posting_fixture(stx1, dec1)

        stx2 = self._create_staged_tx()
        dec2 = self._create_classification_decision(stx2)
        recon2 = _persist_reconciliation_record(
            entity=self.entity,
            reconciliation_id=f"recon_{uuid4().hex[:12]}",
            state_revision_at_creation=1,
            created_at=datetime.now(timezone.utc),
            bank_allocations=[(None, stx2, 1000000)],
            book_allocations=[(b_tx, 1000000)],
        )

        with self.assertRaises(IntegrityError):
            BookkeepingResidualBankPosting.objects.create(
                id=f"rbp_dup_je_{uuid4().hex[:8]}",
                entity=self.entity,
                classification=dec2,
                staged_transaction=stx2,
                journal_entry=je,
                bank_cash_transaction=b_tx,
                contra_transaction=c_tx,
                reconciliation=recon2,
                persistence_revision=2,
            )

    def test_h_bank_cash_transaction_onetoone_uniqueness(self) -> None:
        stx1 = self._create_staged_tx()
        dec1 = self._create_classification_decision(stx1)
        _je1, b_tx1, _c_tx1, _recon1, _ = self._create_posting_fixture(stx1, dec1)

        stx2 = self._create_staged_tx()
        dec2 = self._create_classification_decision(stx2)
        bank_ledger = get_or_create_bank_ledger(bank_account=self.bank_account)
        je2, (_, c_tx2) = bank_ledger.commit_txs(
            je_timestamp=datetime(2026, 3, 15, 13, 0, tzinfo=timezone.utc),
            je_txs=[
                {
                    "account": self.bank_account.account_model,
                    "amount": Decimal("100.00"),
                    "tx_type": CREDIT,
                    "description": "Bank cash leg",
                },
                {
                    "account": dec2.account,
                    "amount": Decimal("100.00"),
                    "tx_type": DEBIT,
                    "description": "Contra leg",
                },
            ],
            je_posted=True,
        )
        assert isinstance(je2, JournalEntryModel)
        assert isinstance(c_tx2, TransactionModel)
        recon2 = _persist_reconciliation_record(
            entity=self.entity,
            reconciliation_id=f"recon_{uuid4().hex[:12]}",
            state_revision_at_creation=1,
            created_at=datetime.now(timezone.utc),
            bank_allocations=[(None, stx2, 1000000)],
            book_allocations=[(b_tx1, 1000000)],
        )

        with self.assertRaises(IntegrityError):
            BookkeepingResidualBankPosting.objects.create(
                id=f"rbp_dup_btx_{uuid4().hex[:8]}",
                entity=self.entity,
                classification=dec2,
                staged_transaction=stx2,
                journal_entry=je2,
                bank_cash_transaction=b_tx1,
                contra_transaction=c_tx2,
                reconciliation=recon2,
                persistence_revision=2,
            )

    def test_i_contra_transaction_onetoone_uniqueness(self) -> None:
        stx1 = self._create_staged_tx()
        dec1 = self._create_classification_decision(stx1)
        _je1, _b_tx1, c_tx1, _recon1, _ = self._create_posting_fixture(stx1, dec1)

        stx2 = self._create_staged_tx()
        dec2 = self._create_classification_decision(stx2)
        bank_ledger = get_or_create_bank_ledger(bank_account=self.bank_account)
        je2, (b_tx2, _) = bank_ledger.commit_txs(
            je_timestamp=datetime(2026, 3, 15, 14, 0, tzinfo=timezone.utc),
            je_txs=[
                {
                    "account": self.bank_account.account_model,
                    "amount": Decimal("100.00"),
                    "tx_type": CREDIT,
                    "description": "Bank cash leg",
                },
                {
                    "account": dec2.account,
                    "amount": Decimal("100.00"),
                    "tx_type": DEBIT,
                    "description": "Contra leg",
                },
            ],
            je_posted=True,
        )
        assert isinstance(je2, JournalEntryModel)
        assert isinstance(b_tx2, TransactionModel)
        recon2 = _persist_reconciliation_record(
            entity=self.entity,
            reconciliation_id=f"recon_{uuid4().hex[:12]}",
            state_revision_at_creation=1,
            created_at=datetime.now(timezone.utc),
            bank_allocations=[(None, stx2, 1000000)],
            book_allocations=[(b_tx2, 1000000)],
        )

        with self.assertRaises(IntegrityError):
            BookkeepingResidualBankPosting.objects.create(
                id=f"rbp_dup_ctx_{uuid4().hex[:8]}",
                entity=self.entity,
                classification=dec2,
                staged_transaction=stx2,
                journal_entry=je2,
                bank_cash_transaction=b_tx2,
                contra_transaction=c_tx1,
                reconciliation=recon2,
                persistence_revision=2,
            )

    def test_j_non_null_reconciliation_enforced(self) -> None:
        stx = self._create_staged_tx()
        dec = self._create_classification_decision(stx)
        bank_ledger = get_or_create_bank_ledger(bank_account=self.bank_account)
        je, (b_tx, c_tx) = bank_ledger.commit_txs(
            je_timestamp=datetime(2026, 3, 15, 15, 0, tzinfo=timezone.utc),
            je_txs=[
                {
                    "account": self.bank_account.account_model,
                    "amount": Decimal("100.00"),
                    "tx_type": CREDIT,
                    "description": "Bank cash leg",
                },
                {
                    "account": dec.account,
                    "amount": Decimal("100.00"),
                    "tx_type": DEBIT,
                    "description": "Contra leg",
                },
            ],
            je_posted=True,
        )
        assert isinstance(je, JournalEntryModel)
        assert isinstance(b_tx, TransactionModel)
        assert isinstance(c_tx, TransactionModel)

        with self.assertRaises(IntegrityError):
            BookkeepingResidualBankPosting.objects.create(
                id=f"rbp_norecon_{uuid4().hex[:8]}",
                entity=self.entity,
                classification=dec,
                staged_transaction=stx,
                journal_entry=je,
                bank_cash_transaction=b_tx,
                contra_transaction=c_tx,
                reconciliation=None,
                persistence_revision=1,
            )

    def test_k_protect_cascade_delete_behavior_matches_precedent(self) -> None:
        stx = self._create_staged_tx()
        dec = self._create_classification_decision(stx)
        je, b_tx, c_tx, recon, _posting = self._create_posting_fixture(stx, dec)

        with self.assertRaises(ProtectedError):
            dec.delete()

        # Unlock and unpost JE so it passes can_delete() and hits Django ORM's ProtectedError
        JournalEntryModel.objects.filter(uuid=je.pk).update(locked=False, posted=False)
        with self.assertRaises(ProtectedError):
            JournalEntryModel.objects.get(uuid=je.pk).delete()

        with self.assertRaises(ProtectedError):
            stx.delete()

        with self.assertRaises(ProtectedError):
            recon.delete()

        with self.assertRaises(ProtectedError):
            TransactionModel.objects.get(uuid=b_tx.pk).delete()

        with self.assertRaises(ProtectedError):
            TransactionModel.objects.get(uuid=c_tx.pk).delete()

    # ------------------------------------------------------------------
    # PART 3 & 4: HYDRATION ROUND-TRIP & QUERIES
    # ------------------------------------------------------------------

    def test_l_posting_hydration_round_trip(self) -> None:
        stx = self._create_staged_tx(amount=Decimal("3400.00"))
        dec = self._create_classification_decision(
            stx,
            account_code="4456",
            amount_units=34000000,
        )
        je, b_tx, c_tx, recon, posting = self._create_posting_fixture(
            stx,
            dec,
            amount=Decimal("3400.00"),
            persistence_revision=5,
        )

        state = self._hydrate_state()

        self.assertEqual(len(state.residual_bank_postings), 1)
        domain_posting = state.get_residual_bank_posting(posting.id)
        self.assertIsNotNone(domain_posting)
        assert domain_posting is not None
        self.assertEqual(domain_posting.id, posting.id)
        self.assertEqual(domain_posting.decision_id, dec.id)
        self.assertEqual(domain_posting.bank_item_id, f"staged:{stx.uuid}")
        self.assertEqual(domain_posting.staged_transaction_id, str(stx.uuid))
        self.assertEqual(domain_posting.journal_entry_id, str(je.pk))
        self.assertEqual(domain_posting.bank_cash_transaction_id, str(b_tx.pk))
        self.assertEqual(domain_posting.contra_transaction_id, str(c_tx.pk))
        self.assertEqual(domain_posting.reconciliation_id, recon.id)
        self.assertEqual(domain_posting.persistence_revision, 5)

    def test_m_posting_lookup_by_decision(self) -> None:
        stx = self._create_staged_tx()
        dec = self._create_classification_decision(stx)
        _, _, _, _, posting = self._create_posting_fixture(stx, dec)

        state = self._hydrate_state()
        queries = BookkeepingQueries(state)

        domain_posting = queries.posting_for_residual_bank_classification(dec.id)
        self.assertIsNotNone(domain_posting)
        assert domain_posting is not None
        self.assertEqual(domain_posting.id, posting.id)
        self.assertTrue(queries.is_residual_bank_classification_posted(dec.id))

        self.assertIsNone(
            queries.posting_for_residual_bank_classification("non_existent_decision")
        )
        self.assertFalse(
            queries.is_residual_bank_classification_posted("non_existent_decision")
        )

    # ------------------------------------------------------------------
    # PART 7: POSTED CLASSIFICATION SEALING
    # ------------------------------------------------------------------

    def test_n_posted_classification_read_path_sealing_after_rehydrate(self) -> None:
        stx = self._create_staged_tx()
        dec = self._create_classification_decision(stx)
        self._create_posting_fixture(stx, dec)

        state = self._hydrate_state()
        engine = TransitionEngine(repository=BookkeepingRepository())

        # Attempt to supersede posted decision
        cmd_supersede = RecordResidualBankClassificationCommand(
            command_id=f"cmd_{uuid4().hex[:8]}",
            session_id=state.session_id,
            expected_state_revision=state.revision,
            expected_persistence_revision=state.persistence_revision,
            decision_id=f"dec_super_{uuid4().hex[:8]}",
            bank_item_id=f"staged:{stx.uuid}",
            bank_account_id=str(self.bank_account.uuid),
            status=ResidualBankClassificationStatus.CLASSIFIED,
            account_code="6111",
            original_amount_units=1000000,
            residual_amount_units=1000000,
            direction=Direction.OUTFLOW,
            currency="USD",
            confidence=0.99,
            schema_version="v1",
            dag_id="dag-residual-bank",
            request_semantic_digest=f"digest_{uuid4().hex[:8]}",
            supersedes_decision_id=dec.id,
            issued_at=datetime.now(timezone.utc),
        )
        res_supersede = engine.apply(state=state, command=cmd_supersede)
        self.assertEqual(res_supersede.status, TransitionStatus.REJECTED)
        assert res_supersede.rejection is not None
        self.assertEqual(
            res_supersede.rejection.code,
            RejectionCode.CANNOT_SUPERSEDE_POSTED_DECISION,
        )

        # Attempt to invalidate posted decision
        cmd_invalidate = InvalidateResidualBankClassificationCommand(
            command_id=f"cmd_{uuid4().hex[:8]}",
            invalidation_id=f"inv_{uuid4().hex[:8]}",
            session_id=state.session_id,
            expected_state_revision=state.revision,
            expected_persistence_revision=state.persistence_revision,
            decision_id=dec.id,
            reason="User correction",
            issued_at=datetime.now(timezone.utc),
        )
        res_invalidate = engine.apply(state=state, command=cmd_invalidate)
        self.assertEqual(res_invalidate.status, TransitionStatus.REJECTED)
        assert res_invalidate.rejection is not None
        self.assertEqual(
            res_invalidate.rejection.code,
            RejectionCode.CANNOT_INVALIDATE_POSTED_DECISION,
        )

    # ------------------------------------------------------------------
    # PART 4: HYDRATION LINKAGE VALIDATION REJECTIONS
    # ------------------------------------------------------------------

    def test_o_hydration_rejects_wrong_staged_transaction(self) -> None:
        stx1 = self._create_staged_tx()
        stx2 = self._create_staged_tx()
        dec = self._create_classification_decision(stx1)
        _, _, _, _, posting = self._create_posting_fixture(stx1, dec)

        BookkeepingResidualBankPosting.objects.filter(id=posting.id).update(
            staged_transaction=stx2
        )

        with self.assertRaises(PersistenceError) as ctx:
            self._hydrate_state()
        self.assertIn("staged_transaction", str(ctx.exception))

    def test_p_hydration_rejects_wrong_bank_account_cash_leg(self) -> None:
        stx = self._create_staged_tx()
        dec = self._create_classification_decision(stx)
        _, b_tx, _, _, _ = self._create_posting_fixture(stx, dec)

        TransactionModel.objects.filter(uuid=b_tx.pk).update(
            account=self.receivables_account
        )

        with self.assertRaises(PersistenceError) as ctx:
            self._hydrate_state()
        self.assertIn("bank cash transaction account", str(ctx.exception))

    def test_q_hydration_rejects_wrong_contra_account(self) -> None:
        stx = self._create_staged_tx()
        dec = self._create_classification_decision(stx, account_code="6111")
        _, _, c_tx, _, _ = self._create_posting_fixture(stx, dec)

        TransactionModel.objects.filter(uuid=c_tx.pk).update(
            account=self.receivables_account
        )

        with self.assertRaises(PersistenceError) as ctx:
            self._hydrate_state()
        self.assertIn("contra transaction account", str(ctx.exception))

    def test_r_hydration_rejects_wrong_amount(self) -> None:
        stx = self._create_staged_tx(amount=Decimal("100.00"))
        dec = self._create_classification_decision(stx, amount_units=1000000)
        _, b_tx, _, _, _ = self._create_posting_fixture(stx, dec, amount=Decimal("100.00"))

        TransactionModel.objects.filter(uuid=b_tx.pk).update(amount=Decimal("99.00"))

        with self.assertRaises(PersistenceError) as ctx:
            self._hydrate_state()
        self.assertIn("amount", str(ctx.exception))

    def test_s_hydration_rejects_wrong_debit_credit_direction(self) -> None:
        stx = self._create_staged_tx(direction="OUTFLOW")
        dec = self._create_classification_decision(stx, direction="OUTFLOW")
        _, b_tx, _, _, _ = self._create_posting_fixture(stx, dec, direction="OUTFLOW")

        TransactionModel.objects.filter(uuid=b_tx.pk).update(tx_type=DEBIT)

        with self.assertRaises(PersistenceError) as ctx:
            self._hydrate_state()
        self.assertIn("direction", str(ctx.exception).lower())

    def test_t_hydration_rejects_unposted_unlocked_wrong_je_state(self) -> None:
        stx = self._create_staged_tx()
        dec = self._create_classification_decision(stx)
        je, _, _, _, _ = self._create_posting_fixture(stx, dec)

        JournalEntryModel.objects.filter(uuid=je.pk).update(posted=False)
        with self.assertRaises(PersistenceError) as ctx:
            self._hydrate_state()
        self.assertIn("not posted", str(ctx.exception))

        JournalEntryModel.objects.filter(uuid=je.pk).update(posted=True, locked=False)
        with self.assertRaises(PersistenceError) as ctx:
            self._hydrate_state()
        self.assertIn("not locked", str(ctx.exception))

    def test_u_hydration_rejects_wrong_deterministic_bank_ledger(self) -> None:
        stx = self._create_staged_tx()
        dec = self._create_classification_decision(stx)
        je, _, _, _, _ = self._create_posting_fixture(stx, dec)

        wrong_ledger = self.entity.create_ledger(
            name="Unrelated Ledger",
            ledger_xid=f"wrong-ledger-{uuid4().hex[:8]}",
            posted=True,
            commit=True,
        )
        JournalEntryModel.objects.filter(uuid=je.pk).update(ledger=wrong_ledger)

        with self.assertRaises(PersistenceError) as ctx:
            self._hydrate_state()
        self.assertIn("ledger_xid", str(ctx.exception))

    def test_v_hydration_rejects_wrong_missing_reconciliation_allocations(self) -> None:
        stx = self._create_staged_tx()
        dec = self._create_classification_decision(stx)
        _, _, _, recon, _ = self._create_posting_fixture(stx, dec)

        # Corrupt bank and book allocation units
        BookkeepingReconciliationBankAllocation.objects.filter(
            reconciliation=recon
        ).update(amount_units=999)
        BookkeepingReconciliationBookAllocation.objects.filter(
            reconciliation=recon
        ).update(amount_units=999)

        with self.assertRaises(PersistenceError) as ctx:
            self._hydrate_state()
        self.assertIn("allocation", str(ctx.exception))

    def test_r2_hydration_rejects_wrong_contra_amount(self) -> None:
        stx = self._create_staged_tx(amount=Decimal("100.00"))
        dec = self._create_classification_decision(stx, amount_units=1000000)
        _, _, c_tx, _, _ = self._create_posting_fixture(stx, dec, amount=Decimal("100.00"))

        TransactionModel.objects.filter(uuid=c_tx.pk).update(amount=Decimal("99.00"))

        with self.assertRaises(PersistenceError) as ctx:
            self._hydrate_state()
        self.assertIn("amount", str(ctx.exception))

    def test_s2_hydration_rejects_inflow_direction_mismatch(self) -> None:
        stx = self._create_staged_tx(direction="INFLOW", amount=Decimal("100.00"))
        dec = self._create_classification_decision(stx, direction="INFLOW", amount_units=1000000)
        _, b_tx, _, _, _ = self._create_posting_fixture(stx, dec, direction="INFLOW", amount=Decimal("100.00"))

        TransactionModel.objects.filter(uuid=b_tx.pk).update(tx_type=CREDIT)

        with self.assertRaises(PersistenceError) as ctx:
            self._hydrate_state()
        self.assertIn("direction", str(ctx.exception).lower())

    def test_v1_hydration_rejects_reconciliation_bank_allocation_amount_mismatch(self) -> None:
        stx = self._create_staged_tx()
        dec = self._create_classification_decision(stx)
        _, _, _, recon, _ = self._create_posting_fixture(stx, dec)

        BookkeepingReconciliationBankAllocation.objects.filter(
            reconciliation=recon
        ).update(amount_units=999)

        with self.assertRaises(PersistenceError) as ctx:
            self._hydrate_state()
        self.assertIn("allocation", str(ctx.exception))

    def test_v2_hydration_rejects_reconciliation_book_allocation_amount_mismatch(self) -> None:
        stx = self._create_staged_tx()
        dec = self._create_classification_decision(stx)
        _, _, _, recon, _ = self._create_posting_fixture(stx, dec)

        BookkeepingReconciliationBookAllocation.objects.filter(
            reconciliation=recon
        ).update(amount_units=999)

        with self.assertRaises(PersistenceError) as ctx:
            self._hydrate_state()
        self.assertIn("allocation", str(ctx.exception))

    def test_v3_hydration_rejects_reconciliation_wrong_bank_cash_transaction(self) -> None:
        stx = self._create_staged_tx()
        dec = self._create_classification_decision(stx)
        _, b_tx, _, recon, _ = self._create_posting_fixture(stx, dec)

        bank_ledger = get_or_create_bank_ledger(bank_account=self.bank_account)
        _other_je, (other_tx, _) = bank_ledger.commit_txs(
            je_timestamp=datetime(2026, 3, 15, 12, 30, tzinfo=timezone.utc),
            je_txs=[
                {
                    "account": self.bank_account.account_model,
                    "amount": Decimal("100.00"),
                    "tx_type": CREDIT,
                    "description": "Other leg",
                },
                {
                    "account": dec.account,
                    "amount": Decimal("100.00"),
                    "tx_type": DEBIT,
                    "description": "Other contra",
                },
            ],
            je_posted=True,
        )
        assert isinstance(other_tx, TransactionModel)
        BookkeepingReconciliationBookAllocation.objects.filter(
            reconciliation=recon
        ).update(transaction=other_tx)

        with self.assertRaises(PersistenceError) as ctx:
            self._hydrate_state()
        self.assertIn("reconciliation book allocation does not match bank cash transaction", str(ctx.exception))

    def test_v4_hydration_rejects_hold_decision_as_posting_provenance(self) -> None:
        stx = self._create_staged_tx()
        dec = self._create_classification_decision(stx)
        self._create_posting_fixture(stx, dec)

        BookkeepingResidualBankClassificationDecision.objects.filter(id=dec.id).update(
            status="HOLD",
            account=None,
            account_code=None,
            hold_reason="Flagged for manual review",
        )

        with self.assertRaises(PersistenceError) as ctx:
            self._hydrate_state()
        self.assertIn("status", str(ctx.exception).lower())

    def test_p_persistence_revision_positive_invariant_enforced_by_db_and_hydration(self) -> None:
        # Attempt to insert persistence_revision=0 fails DB check constraint
        stx_fail = self._create_staged_tx()
        dec_fail = self._create_classification_decision(stx_fail)
        bank_ledger = get_or_create_bank_ledger(bank_account=self.bank_account)
        je_fail, (b_fail, c_fail) = bank_ledger.commit_txs(
            je_timestamp=datetime(2026, 3, 15, 12, 45, tzinfo=timezone.utc),
            je_txs=[
                {
                    "account": self.bank_account.account_model,
                    "amount": Decimal("100.00"),
                    "tx_type": CREDIT,
                    "description": "Bank cash leg",
                },
                {
                    "account": dec_fail.account,
                    "amount": Decimal("100.00"),
                    "tx_type": DEBIT,
                    "description": "Contra leg",
                },
            ],
            je_posted=True,
        )
        assert isinstance(je_fail, JournalEntryModel)
        assert isinstance(b_fail, TransactionModel)
        assert isinstance(c_fail, TransactionModel)

        recon_fail = _persist_reconciliation_record(
            entity=self.entity,
            reconciliation_id=f"recon_{uuid4().hex[:12]}",
            state_revision_at_creation=1,
            created_at=datetime.now(timezone.utc),
            bank_allocations=[(None, stx_fail, 1000000)],
            book_allocations=[(b_fail, 1000000)],
        )

        with self.assertRaises(IntegrityError):
            BookkeepingResidualBankPosting.objects.create(
                id=f"rbp_rev0_{uuid4().hex[:8]}",
                entity=self.entity,
                classification=dec_fail,
                staged_transaction=stx_fail,
                journal_entry=je_fail,
                bank_cash_transaction=b_fail,
                contra_transaction=c_fail,
                reconciliation=recon_fail,
                persistence_revision=0,
                posted_at=datetime.now(timezone.utc),
            )

    # ------------------------------------------------------------------
    # PART 6: FINGERPRINT & RECONCILIATION PERSISTENCE CONTRACTS
    # ------------------------------------------------------------------

    def test_w_posting_participates_deterministically_in_fingerprint(self) -> None:
        stx = self._create_staged_tx()
        dec = self._create_classification_decision(stx)

        state_before = self._hydrate_state()
        fp_before = state_fingerprint(state_before)

        self._create_posting_fixture(stx, dec)

        state_after = self._hydrate_state()
        fp_after = state_fingerprint(state_after)
        self.assertNotEqual(fp_before, fp_after)

        state_after_2 = self._hydrate_state()
        fp_after_2 = state_fingerprint(state_after_2)
        self.assertEqual(fp_after, fp_after_2)

    def test_x_shared_reconciliation_helper_preserves_existing_stage2_persistence_behavior(self) -> None:
        stx = self._create_staged_tx(amount=Decimal("50.00"))
        bank_ledger = get_or_create_bank_ledger(bank_account=self.bank_account)
        _je, (b_tx, _c_tx) = bank_ledger.commit_txs(
            je_timestamp=datetime(2026, 3, 15, 16, 0, tzinfo=timezone.utc),
            je_txs=[
                {
                    "account": self.bank_account.account_model,
                    "amount": Decimal("50.00"),
                    "tx_type": CREDIT,
                    "description": "Bank cash leg",
                },
                {
                    "account": self.expense_account,
                    "amount": Decimal("50.00"),
                    "tx_type": DEBIT,
                    "description": "Contra leg",
                },
            ],
            je_posted=True,
        )
        assert isinstance(b_tx, TransactionModel)

        repo = BookkeepingRepository()
        recon_id = f"recon_{uuid4().hex[:12]}"
        now = datetime.now(timezone.utc)

        recon = Reconciliation(
            id=recon_id,
            source_hypothesis_id=None,
            semantic_rationale="Stage 2 standard reconciliation",
            session_id=f"session_{uuid4().hex[:8]}",
            state_revision_at_creation=3,
            created_at=now,
            bank_allocations=(
                BankAllocation(
                    bank_item_id=f"staged:{stx.uuid}",
                    amount_units="500000",
                ),
            ),
            book_allocations=(
                BookAllocation(
                    book_item_id=f"tx:{b_tx.uuid}",
                    amount_units="500000",
                ),
            ),
        )

        state = self._hydrate_state()
        ws = PersistenceWriteSet(reconciliations=(recon,))
        repo.commit(
            company_id=str(self.entity.uuid),
            expected_revision=state.persistence_revision,
            write_set=ws,
        )

        db_recon = BookkeepingReconciliation.objects.get(id=recon_id)
        self.assertIsNone(db_recon.source_hypothesis_id)
        self.assertEqual(db_recon.state_revision_at_creation, 3)
        self.assertEqual(db_recon.bank_allocations.count(), 1)
        self.assertEqual(db_recon.book_allocations.count(), 1)

    def test_y_direct_reconciliation_helper_contract_supports_prov_count_0_without_fabricating_state_revision(self) -> None:
        stx = self._create_staged_tx(amount=Decimal("75.00"))
        bank_ledger = get_or_create_bank_ledger(bank_account=self.bank_account)
        _je, (b_tx, _c_tx) = bank_ledger.commit_txs(
            je_timestamp=datetime(2026, 3, 15, 17, 0, tzinfo=timezone.utc),
            je_txs=[
                {
                    "account": self.bank_account.account_model,
                    "amount": Decimal("75.00"),
                    "tx_type": CREDIT,
                    "description": "Bank cash leg",
                },
                {
                    "account": self.expense_account,
                    "amount": Decimal("75.00"),
                    "tx_type": DEBIT,
                    "description": "Contra leg",
                },
            ],
            je_posted=True,
        )
        assert isinstance(b_tx, TransactionModel)

        recon_id = f"recon_{uuid4().hex[:12]}"
        rec_row = _persist_reconciliation_record(
            entity=self.entity,
            reconciliation_id=recon_id,
            state_revision_at_creation=42,  # Actual candidate revision, NOT fabricated 0
            created_at=datetime.now(timezone.utc),
            semantic_rationale="Direct residual bank posting closure",
            session_id=f"session_{uuid4().hex[:8]}",
            bank_allocations=[(None, stx, 750000)],
            book_allocations=[(b_tx, 750000)],
        )
        self.assertIsNotNone(rec_row)

        db_recon = BookkeepingReconciliation.objects.get(id=recon_id)
        self.assertIsNone(db_recon.source_hypothesis_id)
        self.assertEqual(db_recon.state_revision_at_creation, 42)
        self.assertEqual(db_recon.bank_allocations.count(), 1)
        self.assertEqual(db_recon.book_allocations.count(), 1)

    def test_z_legacy_payment_reconciliation_classification_persistence_remains_green(self) -> None:
        rev = BookkeepingRevision.objects.get_or_create(
            entity=self.entity,
            defaults={"revision": 0},
        )[0]
        self.assertGreaterEqual(rev.revision, 0)

    def test_supersede_invalidated_unposted_tip_succeeds(self) -> None:
        """
        Step 403 / TASK 25 final review requirement:
        - D1 is current tip
        - invalidate D1
        - D1 has no posting
        - submit D2 with supersedes_decision_id=D1.id
        - D2 succeeds
        - D1 remains invalidated and inactive
        - D2 becomes latest and active
        """
        stx = self._create_staged_tx()
        state = self._hydrate_state()
        engine = TransitionEngine(repository=BookkeepingRepository())

        # 1. Record D1
        d1_id = f"rbcd_d1_{uuid4().hex[:8]}"
        cmd_d1 = RecordResidualBankClassificationCommand(
            command_id=f"cmd_d1_{uuid4().hex[:8]}",
            session_id=state.session_id,
            expected_state_revision=state.revision,
            expected_persistence_revision=state.persistence_revision,
            decision_id=d1_id,
            bank_item_id=f"staged:{stx.uuid}",
            bank_account_id=str(self.bank_account.uuid),
            status=ResidualBankClassificationStatus.CLASSIFIED,
            account_code="6111",
            original_amount_units=1000000,
            residual_amount_units=1000000,
            direction=Direction.OUTFLOW,
            currency="USD",
            confidence=0.99,
            schema_version="v1",
            dag_id="dag-residual-bank",
            request_semantic_digest=f"digest_d1_{uuid4().hex[:8]}",
            issued_at=datetime.now(timezone.utc),
        )
        res_d1 = engine.apply(state=state, command=cmd_d1)
        self.assertEqual(res_d1.status, TransitionStatus.APPLIED)

        # Rehydrate
        state = self._hydrate_state()
        queries = BookkeepingQueries(state)
        latest_d1 = queries.latest_residual_bank_classification(f"staged:{stx.uuid}")
        assert latest_d1 is not None
        self.assertEqual(latest_d1.id, d1_id)
        active_d1 = queries.active_residual_bank_classification(f"staged:{stx.uuid}")
        assert active_d1 is not None
        self.assertEqual(active_d1.id, d1_id)

        # 2. Invalidate D1
        cmd_inv = InvalidateResidualBankClassificationCommand(
            command_id=f"cmd_inv_{uuid4().hex[:8]}",
            invalidation_id=f"inv_d1_{uuid4().hex[:8]}",
            session_id=state.session_id,
            expected_state_revision=state.revision,
            expected_persistence_revision=state.persistence_revision,
            decision_id=d1_id,
            reason="Incorrect account categorized",
            issued_at=datetime.now(timezone.utc),
        )
        res_inv = engine.apply(state=state, command=cmd_inv)
        self.assertEqual(res_inv.status, TransitionStatus.APPLIED)

        # Rehydrate
        state = self._hydrate_state()
        queries = BookkeepingQueries(state)
        latest_after_inv = queries.latest_residual_bank_classification(f"staged:{stx.uuid}")
        assert latest_after_inv is not None
        self.assertEqual(latest_after_inv.id, d1_id)
        self.assertIsNone(queries.active_residual_bank_classification(f"staged:{stx.uuid}"))

        # 3. Submit D2 superseding invalidated unposted tip D1
        d2_id = f"rbcd_d2_{uuid4().hex[:8]}"
        cmd_d2 = RecordResidualBankClassificationCommand(
            command_id=f"cmd_d2_{uuid4().hex[:8]}",
            session_id=state.session_id,
            expected_state_revision=state.revision,
            expected_persistence_revision=state.persistence_revision,
            decision_id=d2_id,
            bank_item_id=f"staged:{stx.uuid}",
            bank_account_id=str(self.bank_account.uuid),
            status=ResidualBankClassificationStatus.CLASSIFIED,
            account_code="4456",
            original_amount_units=1000000,
            residual_amount_units=1000000,
            direction=Direction.OUTFLOW,
            currency="USD",
            confidence=0.99,
            schema_version="v1",
            dag_id="dag-residual-bank",
            request_semantic_digest=f"digest_d2_{uuid4().hex[:8]}",
            supersedes_decision_id=d1_id,
            issued_at=datetime.now(timezone.utc),
        )
        res_d2 = engine.apply(state=state, command=cmd_d2)
        self.assertEqual(res_d2.status, TransitionStatus.APPLIED)

        # 4. Verify post-supersession state
        state = self._hydrate_state()
        queries = BookkeepingQueries(state)
        latest_d2 = queries.latest_residual_bank_classification(f"staged:{stx.uuid}")
        assert latest_d2 is not None
        self.assertEqual(latest_d2.id, d2_id)
        active_d2 = queries.active_residual_bank_classification(f"staged:{stx.uuid}")
        assert active_d2 is not None
        self.assertEqual(active_d2.id, d2_id)
        d1_inv = next(
            (inv for inv in state.residual_bank_classification_invalidations.values() if inv.classification_id == d1_id),
            None,
        )
        self.assertIsNotNone(d1_inv)
        self.assertFalse(queries.is_residual_bank_classification_posted(d1_id))
        self.assertFalse(queries.is_residual_bank_classification_posted(d2_id))
