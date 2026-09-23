from __future__ import annotations

import concurrent.futures
from datetime import date, datetime, timezone
from decimal import Decimal
from unittest.mock import patch
from uuid import uuid4

from bookkeeping_state.domain.commands import (
    InvalidateResidualBankClassificationCommand,
    PostResidualBankClassificationCommand,
    RecordResidualBankClassificationCommand,
)
from bookkeeping_state.domain.enums import Direction
from bookkeeping_state.domain.money import (
    amount_units_to_int,
    major_units_to_solver_units,
    solver_units_to_decimal,
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
from bookkeeping_state.state.queries import BookkeepingQueries
from bookkeeping_state.transitions.engine import TransitionEngine
from bookkeeping_state.transitions.result import (
    RejectionCode,
    TransitionStatus,
)
from django.contrib.auth import get_user_model
from django.db import connection, transaction
from django.test import TestCase, TransactionTestCase
from ledger.io.roles import (
    ASSET_CA_CASH,
    ASSET_CA_RECEIVABLES,
    CREDIT,
    DEBIT,
    EXPENSE_OPERATIONAL,
)
from ledger.models.bank_account import BankAccountModel
from ledger.models.bookkeeping import (
    BookkeepingPaymentApplication,
    BookkeepingPaymentApplicationAllocation,
    BookkeepingReconciliation,
    BookkeepingReconciliationBankAllocation,
    BookkeepingReconciliationBookAllocation,
    BookkeepingResidualBankClassificationDecision,
    BookkeepingResidualBankClassificationInvalidation,
    BookkeepingResidualBankPosting,
    BookkeepingRevision,
)
from ledger.models.data_import import ImportJobModel, StagedTransactionModel
from ledger.models.entity import EntityModel
from ledger.models.journal_entry import JournalEntryModel
from ledger.models.ledger import LedgerModel
from ledger.models.transactions import TransactionModel
from typing import Any, cast


UserModel = get_user_model()


class ResidualBankPostingExecutionTests(TestCase):
    """
    Focused test suite proving Task 26B residual bank accounting mutation execution.
    """

    @classmethod
    def setUpTestData(cls) -> None:
        cls.user = cast(Any, UserModel.objects).create_user(
            username=f"posting_user_{uuid4().hex[:8]}",
            email="posting_exec@test.com",
            password="testpassword123",
        )
        cls.entity = EntityModel.add_root(
            name="Posting Execution Test Corp",
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
        cls.inactive_account = expense_root.add_child(
            coa_model=cls.coa,
            code="6888",
            name="Inactive Expense",
            role=EXPENSE_OPERATIONAL,
            balance_type=DEBIT,
            active=False,
        )

        # Secondary CoA to test outside-default-CoA rejection
        cls.other_coa = cls.entity.create_chart_of_accounts(
            coa_name="Other CoA",
            assign_as_default=False,
            commit=True,
        )
        other_expense_root = cls.other_coa.accountmodel_set.get(code="06000000")
        cls.other_coa_account = other_expense_root.add_child(
            coa_model=cls.other_coa,
            code="6999",
            name="Foreign CoA Expense",
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
            description="Posting Exec OFX Job",
        )

        # Moroccan Entity for Test C
        cls.entity_mad = EntityModel.add_root(
            name="Societe Marocaine Test SARL",
            admin=cls.user,
            currency="MAD",
            fy_start_month=1,
            accrual_method=False,
        )
        cls.coa_mad = cls.entity_mad.create_chart_of_accounts(
            coa_name="Plan Comptable Marocain",
            assign_as_default=True,
            commit=True,
        )
        mad_asset_root = cls.coa_mad.accountmodel_set.get(code="01000000")
        cls.bank_account_mad_cash = mad_asset_root.add_child(
            coa_model=cls.coa_mad,
            code="5141",
            name="Banque (MAD)",
            role=ASSET_CA_CASH,
            balance_type=DEBIT,
            active=True,
        )
        mad_expense_root = cls.coa_mad.accountmodel_set.get(code="06000000")
        cls.vat_account_mad = mad_expense_root.add_child(
            coa_model=cls.coa_mad,
            code="4456",
            name="Etat - TVA Due",
            role=EXPENSE_OPERATIONAL,
            balance_type=DEBIT,
            active=True,
        )
        cls.bank_account_mad = BankAccountModel.objects.create(
            entity_model=cls.entity_mad,
            account_model=cls.bank_account_mad_cash,
            name="Compte Courant Attijariwafa",
            active=True,
        )
        cls.import_job_mad = ImportJobModel.objects.create(
            bank_account_model=cls.bank_account_mad,
            description="Attijariwafa Statement Import",
        )

    def setUp(self) -> None:
        BookkeepingPaymentApplication.objects.all().delete()
        self.repo = BookkeepingRepository()
        self.engine = TransitionEngine(repository=self.repo)

    def _hydrate_state(self, entity: EntityModel | None = None, session_id: str = "session-test-01"):
        target_entity = entity or self.entity
        hydrator = BookkeepingHydrator(repository=self.repo)
        return hydrator.hydrate(
            company_id=str(target_entity.uuid),
            session_id=session_id,
        )

    def _create_staged_tx(
        self,
        amount: Decimal = Decimal("100.00"),
        direction: str = "OUTFLOW",
        import_job: ImportJobModel | None = None,
        date_posted: date = date(2026, 3, 15),
    ) -> StagedTransactionModel:
        job = import_job or self.import_job
        signed_amount = -amount if direction.upper() == "OUTFLOW" else amount
        return StagedTransactionModel.objects.create(
            import_job=job,
            fit_id=f"fit_{uuid4().hex[:12]}",
            date_posted=date_posted,
            amount=signed_amount,
            name="Vendor Supplies Corp",
            memo="Monthly payment",
        )

    def _record_classified_decision(
        self,
        staged_tx: StagedTransactionModel,
        account_code: str = "6111",
        confidence: float = 0.99,
        status: ResidualBankClassificationStatus = ResidualBankClassificationStatus.CLASSIFIED,
        entity: EntityModel | None = None,
        bank_account: BankAccountModel | None = None,
        direction: Direction = Direction.OUTFLOW,
        currency: str = "USD",
        amount_units: int | None = None,
        residual_amount_units: int | None = None,
    ) -> tuple[str, int]:
        target_entity = entity or self.entity
        ba = bank_account or self.bank_account
        state = self._hydrate_state(entity=target_entity)

        stx_amount = Decimal(str(staged_tx.amount)) if staged_tx.amount is not None else Decimal(0)
        full_units = major_units_to_solver_units(abs(stx_amount))
        orig_units = amount_units if amount_units is not None else full_units
        res_units = residual_amount_units if residual_amount_units is not None else orig_units

        decision_id = f"dec_{uuid4().hex[:10]}"
        cmd = RecordResidualBankClassificationCommand(
            command_id=f"cmd-rec-{uuid4().hex[:8]}",
            session_id=state.session_id,
            expected_state_revision=state.revision,
            expected_persistence_revision=state.persistence_revision,
            decision_id=decision_id,
            bank_item_id=f"staged:{staged_tx.uuid}",
            bank_account_id=str(ba.uuid),
            status=status,
            account_code=account_code if status == ResidualBankClassificationStatus.CLASSIFIED else None,
            confidence=confidence,
            original_amount_units=orig_units,
            residual_amount_units=res_units,
            direction=direction,
            currency=currency,
            schema_version="v1",
            dag_id="pcm_bank_cash_accounting_dag",
            request_semantic_digest=f"digest_{uuid4().hex[:8]}",
            issued_at=datetime(2026, 3, 15, 12, 0, tzinfo=timezone.utc),
        )

        res = self.engine.apply(state=state, command=cmd)
        self.assertTrue(res.applied, f"Recording classification failed: {res.rejection}")
        return decision_id, state.persistence_revision

    # ------------------------------------------------------------------
    # Test A: Successful OUTFLOW posting (Dr contra / Cr bank)
    # ------------------------------------------------------------------
    def test_a_successful_outflow_posting(self) -> None:
        stx = self._create_staged_tx(amount=Decimal("150.00"), direction="OUTFLOW")
        dec_id, p_rev = self._record_classified_decision(
            staged_tx=stx,
            account_code="6111",
            direction=Direction.OUTFLOW,
        )

        state = self._hydrate_state()
        self.assertEqual(state.persistence_revision, p_rev)

        post_cmd = PostResidualBankClassificationCommand(
            command_id=f"cmd-post-{uuid4().hex[:8]}",
            session_id=state.session_id,
            expected_state_revision=state.revision,
            expected_persistence_revision=state.persistence_revision,
            decision_id=dec_id,
            issued_at=datetime(2026, 3, 15, 12, 5, tzinfo=timezone.utc),
        )

        res = self.engine.apply(state=state, command=post_cmd)
        self.assertEqual(res.status, TransitionStatus.APPLIED_REQUIRES_REHYDRATION)
        self.assertTrue(state.is_closed)

        # Invariants
        posting = BookkeepingResidualBankPosting.objects.get(classification_id=dec_id)
        self.assertEqual(posting.persistence_revision, p_rev + 1)
        self.assertEqual(posting.staged_transaction, stx)

        je = posting.journal_entry
        self.assertTrue(je.posted)
        self.assertTrue(je.locked)
        self.assertTrue(je.is_locked())
        self.assertEqual(je.origin, "residual_bank_classification")

        # Deterministic bank ledger
        bank_ledger = get_or_create_bank_ledger(bank_account=self.bank_account)
        self.assertEqual(je.ledger, bank_ledger)
        self.assertEqual(bank_ledger.ledger_xid, f"bank-ledger-{self.bank_account.uuid}")

        # Legs: Dr contra / Cr bank
        bank_tx = posting.bank_cash_transaction
        contra_tx = posting.contra_transaction
        self.assertEqual(bank_tx.account, self.bank_account.account_model)
        self.assertEqual(bank_tx.tx_type, CREDIT)
        self.assertEqual(Decimal(str(bank_tx.amount)), Decimal("150.00"))

        self.assertEqual(contra_tx.account, self.expense_account)
        self.assertEqual(contra_tx.tx_type, DEBIT)
        self.assertEqual(Decimal(str(contra_tx.amount)), Decimal("150.00"))

    # ------------------------------------------------------------------
    # Test B: Successful INFLOW posting (Dr bank / Cr contra)
    # ------------------------------------------------------------------
    def test_b_successful_inflow_posting(self) -> None:
        stx = self._create_staged_tx(amount=Decimal("250.00"), direction="INFLOW")
        dec_id, p_rev = self._record_classified_decision(
            staged_tx=stx,
            account_code="6111",
            direction=Direction.INFLOW,
        )

        state = self._hydrate_state()
        post_cmd = PostResidualBankClassificationCommand(
            command_id=f"cmd-post-{uuid4().hex[:8]}",
            session_id=state.session_id,
            expected_state_revision=state.revision,
            expected_persistence_revision=state.persistence_revision,
            decision_id=dec_id,
            issued_at=datetime(2026, 3, 15, 12, 5, tzinfo=timezone.utc),
        )

        res = self.engine.apply(state=state, command=post_cmd)
        self.assertEqual(res.status, TransitionStatus.APPLIED_REQUIRES_REHYDRATION)

        posting = BookkeepingResidualBankPosting.objects.get(classification_id=dec_id)
        bank_tx = posting.bank_cash_transaction
        contra_tx = posting.contra_transaction

        # Legs: Dr bank / Cr contra
        self.assertEqual(bank_tx.account, self.bank_account.account_model)
        self.assertEqual(bank_tx.tx_type, DEBIT)
        self.assertEqual(Decimal(str(bank_tx.amount)), Decimal("250.00"))

        self.assertEqual(contra_tx.account, self.expense_account)
        self.assertEqual(contra_tx.tx_type, CREDIT)
        self.assertEqual(Decimal(str(contra_tx.amount)), Decimal("250.00"))

    # ------------------------------------------------------------------
    # Test C: Moroccan VAT settlement (3,400 MAD, Dr 4456 / Cr 5141)
    # ------------------------------------------------------------------
    def test_c_moroccan_vat_settlement(self) -> None:
        stx_mad = self._create_staged_tx(
            amount=Decimal("3400.00"),
            direction="OUTFLOW",
            import_job=self.import_job_mad,
        )
        dec_id, p_rev = self._record_classified_decision(
            staged_tx=stx_mad,
            account_code="4456",
            direction=Direction.OUTFLOW,
            entity=self.entity_mad,
            bank_account=self.bank_account_mad,
            currency="MAD",
            amount_units=34000000,
        )

        state_mad = self._hydrate_state(entity=self.entity_mad)
        post_cmd = PostResidualBankClassificationCommand(
            command_id=f"cmd-post-mad-{uuid4().hex[:8]}",
            session_id=state_mad.session_id,
            expected_state_revision=state_mad.revision,
            expected_persistence_revision=state_mad.persistence_revision,
            decision_id=dec_id,
            issued_at=datetime(2026, 3, 15, 12, 5, tzinfo=timezone.utc),
        )

        res = self.engine.apply(state=state_mad, command=post_cmd)
        self.assertEqual(res.status, TransitionStatus.APPLIED_REQUIRES_REHYDRATION)

        posting = BookkeepingResidualBankPosting.objects.get(classification_id=dec_id)
        self.assertEqual(posting.entity, self.entity_mad)

        bank_tx = posting.bank_cash_transaction
        contra_tx = posting.contra_transaction

        # Cr 5141 (Bank) / Dr 4456 (VAT)
        self.assertEqual(bank_tx.account.code, "5141")
        self.assertEqual(bank_tx.tx_type, CREDIT)
        self.assertEqual(Decimal(str(bank_tx.amount)), Decimal("3400.00"))

        self.assertEqual(contra_tx.account.code, "4456")
        self.assertEqual(contra_tx.tx_type, DEBIT)
        self.assertEqual(Decimal(str(contra_tx.amount)), Decimal("3400.00"))

    # ------------------------------------------------------------------
    # Test D: Exact partial residual (original 100, Stage 2 has 40, post 60)
    # ------------------------------------------------------------------
    def test_d_exact_partial_residual_posting(self) -> None:
        from unittest.mock import patch
        from bookkeeping_state.domain.context import AccountingPolicy

        def _policy_with_partial_bank(*args: Any, **kwargs: Any) -> AccountingPolicy:
            kwargs["allow_partial_bank_reconciliation"] = True
            return AccountingPolicy(*args, **kwargs)

        with patch("bookkeeping_state.persistence.reader.AccountingPolicy", side_effect=_policy_with_partial_bank):
            stx = self._create_staged_tx(amount=Decimal("100.00"), direction="OUTFLOW")

            # Create prior Stage 2 reconciliation of 40.00 (400,000 units)
            bank_ledger = get_or_create_bank_ledger(bank_account=self.bank_account)
            je_prior, (prior_bank_tx, _) = bank_ledger.commit_txs(
                je_timestamp=date(2026, 3, 10),
                je_txs=[
                    {"account": self.cash_account, "amount": Decimal("40.00"), "tx_type": CREDIT, "description": "Prior partial"},
                    {"account": self.expense_account, "amount": Decimal("40.00"), "tx_type": DEBIT, "description": "Prior partial"},
                ],
                je_posted=True,
            )

            prior_recon = BookkeepingReconciliation.objects.create(
                id=f"rec_prior_{uuid4().hex[:8]}",
                entity=self.entity,
                state_revision_at_creation=0,
                created_at=datetime.now(timezone.utc),
            )
            BookkeepingReconciliationBankAllocation.objects.create(
                reconciliation=prior_recon,
                staged_transaction=stx,
                amount_units=400_000,
            )
            BookkeepingReconciliationBookAllocation.objects.create(
                reconciliation=prior_recon,
                transaction=prior_bank_tx,
                amount_units=400_000,
            )

            # Residual remaining is 600,000 units (60.00)
            dec_id, p_rev = self._record_classified_decision(
                staged_tx=stx,
                account_code="6111",
                direction=Direction.OUTFLOW,
                amount_units=1_000_000,
                residual_amount_units=600_000,
            )

            state = self._hydrate_state()
            post_cmd = PostResidualBankClassificationCommand(
                command_id=f"cmd-post-part-{uuid4().hex[:8]}",
                session_id=state.session_id,
                expected_state_revision=state.revision,
                expected_persistence_revision=state.persistence_revision,
                decision_id=dec_id,
                issued_at=datetime(2026, 3, 15, 12, 5, tzinfo=timezone.utc),
            )

            res = self.engine.apply(state=state, command=post_cmd)
            self.assertEqual(res.status, TransitionStatus.APPLIED_REQUIRES_REHYDRATION)

            posting = BookkeepingResidualBankPosting.objects.get(classification_id=dec_id)
            self.assertEqual(Decimal(str(posting.bank_cash_transaction.amount)), Decimal("60.00"))

            # Total bank allocated across reconciliations = 400,000 + 600,000 = 1,000,000 (100%)
            total_allocated = sum(
                a.amount_units
                for a in BookkeepingReconciliationBankAllocation.objects.filter(
                    staged_transaction=stx,
                    reconciliation__invalidations__isnull=True,
                )
            )
            self.assertEqual(total_allocated, 1_000_000)

    # ------------------------------------------------------------------
    # Test E: Direct reconciliation invariants (prov_count == 0)
    # ------------------------------------------------------------------
    def test_e_direct_reconciliation_invariants(self) -> None:
        stx = self._create_staged_tx(amount=Decimal("120.00"), direction="OUTFLOW")
        dec_id, _ = self._record_classified_decision(staged_tx=stx, account_code="6111")

        state = self._hydrate_state()
        post_cmd = PostResidualBankClassificationCommand(
            command_id=f"cmd-post-{uuid4().hex[:8]}",
            session_id=state.session_id,
            expected_state_revision=state.revision,
            expected_persistence_revision=state.persistence_revision,
            decision_id=dec_id,
            issued_at=datetime(2026, 3, 15, 12, 5, tzinfo=timezone.utc),
        )

        res = self.engine.apply(state=state, command=post_cmd)
        self.assertEqual(res.status, TransitionStatus.APPLIED_REQUIRES_REHYDRATION)

        posting = BookkeepingResidualBankPosting.objects.get(classification_id=dec_id)
        recon = posting.reconciliation

        self.assertIsNone(recon.source_hypothesis_id)
        self.assertIsNone(recon.source_hypothesis_state_revision)
        self.assertIsNone(recon.source_hypothesis_utility)
        self.assertIsNone(recon.source_hypothesis_generated_at)
        self.assertEqual(recon.state_revision_at_creation, 1)
        self.assertIn("Direct deterministic closure", recon.semantic_rationale)

        bank_allocs = list(recon.bank_allocations.all())
        book_allocs = list(recon.book_allocations.all())
        self.assertEqual(len(bank_allocs), 1)
        self.assertEqual(len(book_allocs), 1)

        self.assertEqual(bank_allocs[0].staged_transaction, stx)
        self.assertEqual(bank_allocs[0].amount_units, 1_200_000)
        self.assertEqual(book_allocs[0].transaction, posting.bank_cash_transaction)
        self.assertEqual(book_allocs[0].amount_units, 1_200_000)

    # ------------------------------------------------------------------
    # Test F: Posting provenance record links all components
    # ------------------------------------------------------------------
    def test_f_posting_provenance_linkage(self) -> None:
        stx = self._create_staged_tx(amount=Decimal("80.00"), direction="OUTFLOW")
        dec_id, p_rev = self._record_classified_decision(staged_tx=stx, account_code="6111")

        state = self._hydrate_state()
        post_cmd = PostResidualBankClassificationCommand(
            command_id=f"cmd-post-{uuid4().hex[:8]}",
            session_id=state.session_id,
            expected_state_revision=state.revision,
            expected_persistence_revision=state.persistence_revision,
            decision_id=dec_id,
            issued_at=datetime(2026, 3, 15, 12, 5, tzinfo=timezone.utc),
        )

        self.engine.apply(state=state, command=post_cmd)

        posting = BookkeepingResidualBankPosting.objects.get(classification_id=dec_id)
        self.assertEqual(posting.entity, self.entity)
        self.assertEqual(posting.classification_id, dec_id)
        self.assertEqual(posting.staged_transaction, stx)
        self.assertIsNotNone(posting.journal_entry)
        self.assertIsNotNone(posting.bank_cash_transaction)
        self.assertIsNotNone(posting.contra_transaction)
        self.assertIsNotNone(posting.reconciliation)
        self.assertEqual(posting.persistence_revision, p_rev + 1)
        self.assertEqual(
            BookkeepingRevision.objects.get(entity=self.entity).revision,
            posting.persistence_revision,
        )

    # ------------------------------------------------------------------
    # Test G: State closes on successful post
    # ------------------------------------------------------------------
    def test_g_state_closes_on_successful_post(self) -> None:
        stx = self._create_staged_tx(amount=Decimal("90.00"), direction="OUTFLOW")
        dec_id, _ = self._record_classified_decision(staged_tx=stx, account_code="6111")

        state = self._hydrate_state()
        post_cmd = PostResidualBankClassificationCommand(
            command_id=f"cmd-post-{uuid4().hex[:8]}",
            session_id=state.session_id,
            expected_state_revision=state.revision,
            expected_persistence_revision=state.persistence_revision,
            decision_id=dec_id,
            issued_at=datetime(2026, 3, 15, 12, 5, tzinfo=timezone.utc),
        )

        res = self.engine.apply(state=state, command=post_cmd)
        self.assertEqual(res.status, TransitionStatus.APPLIED_REQUIRES_REHYDRATION)
        self.assertTrue(state.is_closed)

    # ------------------------------------------------------------------
    # Test H: Fresh rehydrate reflects posted reality
    # ------------------------------------------------------------------
    def test_h_fresh_rehydrate_reflects_posted_reality(self) -> None:
        stx = self._create_staged_tx(amount=Decimal("75.00"), direction="OUTFLOW")
        dec_id, _ = self._record_classified_decision(staged_tx=stx, account_code="6111")

        state = self._hydrate_state()
        post_cmd = PostResidualBankClassificationCommand(
            command_id=f"cmd-post-{uuid4().hex[:8]}",
            session_id=state.session_id,
            expected_state_revision=state.revision,
            expected_persistence_revision=state.persistence_revision,
            decision_id=dec_id,
            issued_at=datetime(2026, 3, 15, 12, 5, tzinfo=timezone.utc),
        )
        self.engine.apply(state=state, command=post_cmd)

        # Fresh rehydrate
        fresh_state = self._hydrate_state()
        queries = BookkeepingQueries(fresh_state)

        # Decision is posted
        self.assertTrue(queries.is_residual_bank_classification_posted(dec_id))
        posting = queries.posting_for_residual_bank_classification(dec_id)
        self.assertIsNotNone(posting)

        # Bank movement is no longer residual
        residual_items = queries.residual_unmatched_bank_items()
        self.assertNotIn(f"staged:{stx.uuid}", [item.id for item, _ in residual_items])

        # New bank cash transaction is fully reconciled (remaining units == 0)
        db_posting = BookkeepingResidualBankPosting.objects.get(classification_id=dec_id)
        cash_tx_book_id = f"tx:{db_posting.bank_cash_transaction.uuid}"
        self.assertEqual(queries.book_remaining_units(cash_tx_book_id), 0)

    # ------------------------------------------------------------------
    # Test I: Exact replay after rehydrate returns NOOP before OCC check
    # ------------------------------------------------------------------
    def test_i_exact_replay_after_rehydrate_returns_noop_before_occ(self) -> None:
        stx = self._create_staged_tx(amount=Decimal("110.00"), direction="OUTFLOW")
        dec_id, _ = self._record_classified_decision(staged_tx=stx, account_code="6111")

        state = self._hydrate_state()
        post_cmd = PostResidualBankClassificationCommand(
            command_id=f"cmd-post-{uuid4().hex[:8]}",
            session_id=state.session_id,
            expected_state_revision=state.revision,
            expected_persistence_revision=state.persistence_revision,
            decision_id=dec_id,
            bank_item_id=f"staged:{stx.uuid}",
            bank_account_id=str(self.bank_account.uuid),
            residual_amount_units=1_100_000,
            direction=Direction.OUTFLOW,
            account_code="6111",
            issued_at=datetime(2026, 3, 15, 12, 5, tzinfo=timezone.utc),
        )
        res1 = self.engine.apply(state=state, command=post_cmd)
        self.assertEqual(res1.status, TransitionStatus.APPLIED_REQUIRES_REHYDRATION)

        fresh_state = self._hydrate_state()
        p_rev_after = fresh_state.persistence_revision

        # Replay command using old stale expected revisions
        replay_cmd = PostResidualBankClassificationCommand(
            command_id=f"cmd-post-replay-{uuid4().hex[:8]}",
            session_id=fresh_state.session_id,
            expected_state_revision=999,  # Stale state revision!
            expected_persistence_revision=0,  # Stale persistence revision!
            decision_id=dec_id,
            bank_item_id=f"staged:{stx.uuid}",
            bank_account_id=str(self.bank_account.uuid),
            residual_amount_units=1_100_000,
            direction=Direction.OUTFLOW,
            account_code="6111",
            issued_at=datetime(2026, 3, 15, 12, 10, tzinfo=timezone.utc),
        )

        res_replay = self.engine.apply(state=fresh_state, command=replay_cmd)
        self.assertEqual(res_replay.status, TransitionStatus.NOOP)
        self.assertTrue(res_replay.noop)

        # No duplicate rows created
        self.assertEqual(BookkeepingResidualBankPosting.objects.filter(classification_id=dec_id).count(), 1)
        self.assertEqual(
            BookkeepingRevision.objects.get(entity=self.entity).revision,
            p_rev_after,
        )

    # ------------------------------------------------------------------
    # Test J: Conflicting replay rejects with DECISION_ALREADY_POSTED
    # ------------------------------------------------------------------
    def test_j_conflicting_replay_rejects_already_posted(self) -> None:
        stx = self._create_staged_tx(amount=Decimal("130.00"), direction="OUTFLOW")
        dec_id, _ = self._record_classified_decision(staged_tx=stx, account_code="6111")

        state = self._hydrate_state()
        post_cmd = PostResidualBankClassificationCommand(
            command_id=f"cmd-post-{uuid4().hex[:8]}",
            session_id=state.session_id,
            expected_state_revision=state.revision,
            expected_persistence_revision=state.persistence_revision,
            decision_id=dec_id,
            issued_at=datetime(2026, 3, 15, 12, 5, tzinfo=timezone.utc),
        )
        self.engine.apply(state=state, command=post_cmd)

        fresh_state = self._hydrate_state()

        # Conflicting capability fields: wrong account_code
        conflict_cmd = PostResidualBankClassificationCommand(
            command_id=f"cmd-post-conflict-{uuid4().hex[:8]}",
            session_id=fresh_state.session_id,
            expected_state_revision=fresh_state.revision,
            expected_persistence_revision=fresh_state.persistence_revision,
            decision_id=dec_id,
            account_code="6222",  # Different from original 6111!
            issued_at=datetime(2026, 3, 15, 12, 10, tzinfo=timezone.utc),
        )

        res_conflict = self.engine.apply(state=fresh_state, command=conflict_cmd)
        self.assertEqual(res_conflict.status, TransitionStatus.REJECTED)
        assert res_conflict.rejection is not None
        self.assertEqual(res_conflict.rejection.code, RejectionCode.DECISION_ALREADY_POSTED)

    # ------------------------------------------------------------------
    # Test K: HOLD cannot post
    # ------------------------------------------------------------------
    def test_k_hold_cannot_post(self) -> None:
        stx = self._create_staged_tx(amount=Decimal("140.00"), direction="OUTFLOW")
        state = self._hydrate_state()

        dec_id = f"dec-hold-{uuid4().hex[:8]}"
        cmd = RecordResidualBankClassificationCommand(
            command_id=f"cmd-hold-{uuid4().hex[:8]}",
            session_id=state.session_id,
            expected_state_revision=state.revision,
            expected_persistence_revision=state.persistence_revision,
            decision_id=dec_id,
            bank_item_id=f"staged:{stx.uuid}",
            bank_account_id=str(self.bank_account.uuid),
            status=ResidualBankClassificationStatus.HOLD,
            hold_reason="Suspicious vendor name",
            confidence=0.5,
            original_amount_units=1_400_000,
            residual_amount_units=1_400_000,
            direction=Direction.OUTFLOW,
            currency="USD",
            schema_version="v1",
            dag_id="pcm_bank_cash_accounting_dag",
            request_semantic_digest="digest-hold",
            issued_at=datetime(2026, 3, 15, 12, 0, tzinfo=timezone.utc),
        )
        self.engine.apply(state=state, command=cmd)

        fresh_state = self._hydrate_state()
        post_cmd = PostResidualBankClassificationCommand(
            command_id=f"cmd-post-hold-{uuid4().hex[:8]}",
            session_id=fresh_state.session_id,
            expected_state_revision=fresh_state.revision,
            expected_persistence_revision=fresh_state.persistence_revision,
            decision_id=dec_id,
            issued_at=datetime(2026, 3, 15, 12, 5, tzinfo=timezone.utc),
        )

        res = self.engine.apply(state=fresh_state, command=post_cmd)
        self.assertEqual(res.status, TransitionStatus.REJECTED)
        assert res.rejection is not None
        self.assertEqual(res.rejection.code, RejectionCode.CANNOT_POST_NON_CLASSIFIED_DECISION)

    # ------------------------------------------------------------------
    # Test L: Invalidated decision cannot post
    # ------------------------------------------------------------------
    def test_l_invalidated_decision_cannot_post(self) -> None:
        stx = self._create_staged_tx(amount=Decimal("160.00"), direction="OUTFLOW")
        dec_id, _ = self._record_classified_decision(staged_tx=stx, account_code="6111")

        state = self._hydrate_state()
        inv_cmd = InvalidateResidualBankClassificationCommand(
            command_id=f"cmd-inv-{uuid4().hex[:8]}",
            session_id=state.session_id,
            expected_state_revision=state.revision,
            expected_persistence_revision=state.persistence_revision,
            decision_id=dec_id,
            invalidation_id=f"inv-{uuid4().hex[:8]}",
            reason="Incorrect tax category",
            issued_at=datetime(2026, 3, 15, 12, 5, tzinfo=timezone.utc),
        )
        self.engine.apply(state=state, command=inv_cmd)

        fresh_state = self._hydrate_state()
        post_cmd = PostResidualBankClassificationCommand(
            command_id=f"cmd-post-inv-{uuid4().hex[:8]}",
            session_id=fresh_state.session_id,
            expected_state_revision=fresh_state.revision,
            expected_persistence_revision=fresh_state.persistence_revision,
            decision_id=dec_id,
            issued_at=datetime(2026, 3, 15, 12, 10, tzinfo=timezone.utc),
        )

        res = self.engine.apply(state=fresh_state, command=post_cmd)
        self.assertEqual(res.status, TransitionStatus.REJECTED)
        assert res.rejection is not None
        self.assertEqual(res.rejection.code, RejectionCode.CANNOT_POST_INVALIDATED_DECISION)

    # ------------------------------------------------------------------
    # Test M: Superseded/non-tip decision cannot post
    # ------------------------------------------------------------------
    def test_m_superseded_non_tip_decision_cannot_post(self) -> None:
        stx = self._create_staged_tx(amount=Decimal("170.00"), direction="OUTFLOW")
        dec1_id, _ = self._record_classified_decision(staged_tx=stx, account_code="6111")

        state = self._hydrate_state()
        dec2_id = f"dec2-{uuid4().hex[:8]}"
        sup_cmd = RecordResidualBankClassificationCommand(
            command_id=f"cmd-sup-{uuid4().hex[:8]}",
            session_id=state.session_id,
            expected_state_revision=state.revision,
            expected_persistence_revision=state.persistence_revision,
            decision_id=dec2_id,
            supersedes_decision_id=dec1_id,
            bank_item_id=f"staged:{stx.uuid}",
            bank_account_id=str(self.bank_account.uuid),
            status=ResidualBankClassificationStatus.CLASSIFIED,
            account_code="6111",
            confidence=0.99,
            original_amount_units=1_700_000,
            residual_amount_units=1_700_000,
            direction=Direction.OUTFLOW,
            currency="USD",
            schema_version="v1",
            dag_id="pcm_bank_cash_accounting_dag",
            request_semantic_digest="digest-sup",
            issued_at=datetime(2026, 3, 15, 12, 5, tzinfo=timezone.utc),
        )
        self.engine.apply(state=state, command=sup_cmd)

        # Attempt to post dec1_id (the historical non-tip)
        fresh_state = self._hydrate_state()
        post_cmd = PostResidualBankClassificationCommand(
            command_id=f"cmd-post-old-{uuid4().hex[:8]}",
            session_id=fresh_state.session_id,
            expected_state_revision=fresh_state.revision,
            expected_persistence_revision=fresh_state.persistence_revision,
            decision_id=dec1_id,
            issued_at=datetime(2026, 3, 15, 12, 10, tzinfo=timezone.utc),
        )

        res = self.engine.apply(state=fresh_state, command=post_cmd)
        self.assertEqual(res.status, TransitionStatus.REJECTED)
        assert res.rejection is not None
        self.assertEqual(res.rejection.code, RejectionCode.CANNOT_POST_NON_TIP_DECISION)

    # ------------------------------------------------------------------
    # Test N: Posted decision cannot later invalidate
    # ------------------------------------------------------------------
    def test_n_posted_decision_cannot_later_invalidate(self) -> None:
        stx = self._create_staged_tx(amount=Decimal("180.00"), direction="OUTFLOW")
        dec_id, _ = self._record_classified_decision(staged_tx=stx, account_code="6111")

        state = self._hydrate_state()
        post_cmd = PostResidualBankClassificationCommand(
            command_id=f"cmd-post-{uuid4().hex[:8]}",
            session_id=state.session_id,
            expected_state_revision=state.revision,
            expected_persistence_revision=state.persistence_revision,
            decision_id=dec_id,
            issued_at=datetime(2026, 3, 15, 12, 5, tzinfo=timezone.utc),
        )
        self.engine.apply(state=state, command=post_cmd)

        fresh_state = self._hydrate_state()
        inv_cmd = InvalidateResidualBankClassificationCommand(
            command_id=f"cmd-inv-posted-{uuid4().hex[:8]}",
            session_id=fresh_state.session_id,
            expected_state_revision=fresh_state.revision,
            expected_persistence_revision=fresh_state.persistence_revision,
            decision_id=dec_id,
            invalidation_id=f"inv-{uuid4().hex[:8]}",
            reason="Attempting to invalidate posted decision",
            issued_at=datetime(2026, 3, 15, 12, 10, tzinfo=timezone.utc),
        )

        res = self.engine.apply(state=fresh_state, command=inv_cmd)
        self.assertEqual(res.status, TransitionStatus.REJECTED)
        assert res.rejection is not None
        self.assertEqual(res.rejection.code, RejectionCode.CANNOT_INVALIDATE_POSTED_DECISION)

    # ------------------------------------------------------------------
    # Test O: Posted decision cannot later supersede
    # ------------------------------------------------------------------
    def test_o_posted_decision_cannot_later_supersede(self) -> None:
        stx = self._create_staged_tx(amount=Decimal("190.00"), direction="OUTFLOW")
        dec_id, _ = self._record_classified_decision(staged_tx=stx, account_code="6111")

        state = self._hydrate_state()
        post_cmd = PostResidualBankClassificationCommand(
            command_id=f"cmd-post-{uuid4().hex[:8]}",
            session_id=state.session_id,
            expected_state_revision=state.revision,
            expected_persistence_revision=state.persistence_revision,
            decision_id=dec_id,
            issued_at=datetime(2026, 3, 15, 12, 5, tzinfo=timezone.utc),
        )
        self.engine.apply(state=state, command=post_cmd)

        fresh_state = self._hydrate_state()
        sup_cmd = RecordResidualBankClassificationCommand(
            command_id=f"cmd-sup-posted-{uuid4().hex[:8]}",
            session_id=fresh_state.session_id,
            expected_state_revision=fresh_state.revision,
            expected_persistence_revision=fresh_state.persistence_revision,
            decision_id=f"dec-new-{uuid4().hex[:8]}",
            supersedes_decision_id=dec_id,
            bank_item_id=f"staged:{stx.uuid}",
            bank_account_id=str(self.bank_account.uuid),
            status=ResidualBankClassificationStatus.CLASSIFIED,
            account_code="6111",
            confidence=0.99,
            original_amount_units=1_900_000,
            residual_amount_units=1_900_000,
            direction=Direction.OUTFLOW,
            currency="USD",
            schema_version="v1",
            dag_id="pcm_bank_cash_accounting_dag",
            request_semantic_digest="digest-sup-posted",
            issued_at=datetime(2026, 3, 15, 12, 10, tzinfo=timezone.utc),
        )

        res = self.engine.apply(state=fresh_state, command=sup_cmd)
        self.assertEqual(res.status, TransitionStatus.REJECTED)
        assert res.rejection is not None
        self.assertEqual(res.rejection.code, RejectionCode.CANNOT_SUPERSEDE_POSTED_DECISION)

    # ------------------------------------------------------------------
    # Test P: Invalidated UNPOSTED tip may still be superseded
    # ------------------------------------------------------------------
    def test_p_invalidated_unposted_tip_may_be_superseded(self) -> None:
        stx = self._create_staged_tx(amount=Decimal("200.00"), direction="OUTFLOW")
        dec1_id, _ = self._record_classified_decision(staged_tx=stx, account_code="6111")

        state = self._hydrate_state()
        inv_cmd = InvalidateResidualBankClassificationCommand(
            command_id=f"cmd-inv-{uuid4().hex[:8]}",
            session_id=state.session_id,
            expected_state_revision=state.revision,
            expected_persistence_revision=state.persistence_revision,
            decision_id=dec1_id,
            invalidation_id=f"inv-{uuid4().hex[:8]}",
            reason="Fixing account",
            issued_at=datetime(2026, 3, 15, 12, 5, tzinfo=timezone.utc),
        )
        self.engine.apply(state=state, command=inv_cmd)

        fresh_state = self._hydrate_state()
        dec2_id = f"dec2-{uuid4().hex[:8]}"
        sup_cmd = RecordResidualBankClassificationCommand(
            command_id=f"cmd-sup-inv-{uuid4().hex[:8]}",
            session_id=fresh_state.session_id,
            expected_state_revision=fresh_state.revision,
            expected_persistence_revision=fresh_state.persistence_revision,
            decision_id=dec2_id,
            supersedes_decision_id=dec1_id,
            bank_item_id=f"staged:{stx.uuid}",
            bank_account_id=str(self.bank_account.uuid),
            status=ResidualBankClassificationStatus.CLASSIFIED,
            account_code="6111",
            confidence=0.99,
            original_amount_units=2_000_000,
            residual_amount_units=2_000_000,
            direction=Direction.OUTFLOW,
            currency="USD",
            schema_version="v1",
            dag_id="pcm_bank_cash_accounting_dag",
            request_semantic_digest="digest-sup-inv",
            issued_at=datetime(2026, 3, 15, 12, 10, tzinfo=timezone.utc),
        )

        res = self.engine.apply(state=fresh_state, command=sup_cmd)
        self.assertEqual(res.status, TransitionStatus.APPLIED)

    # ------------------------------------------------------------------
    # Test Q: Stale state revision on UNPOSTED decision rejected
    # ------------------------------------------------------------------
    def test_q_stale_state_revision_on_unposted_decision_rejected(self) -> None:
        stx = self._create_staged_tx(amount=Decimal("210.00"), direction="OUTFLOW")
        dec_id, _ = self._record_classified_decision(staged_tx=stx, account_code="6111")

        state = self._hydrate_state()
        post_cmd = PostResidualBankClassificationCommand(
            command_id=f"cmd-post-stale-state-{uuid4().hex[:8]}",
            session_id=state.session_id,
            expected_state_revision=state.revision + 5,  # Stale state revision!
            expected_persistence_revision=state.persistence_revision,
            decision_id=dec_id,
            issued_at=datetime(2026, 3, 15, 12, 5, tzinfo=timezone.utc),
        )

        res = self.engine.apply(state=state, command=post_cmd)
        self.assertEqual(res.status, TransitionStatus.REJECTED)
        assert res.rejection is not None
        self.assertEqual(res.rejection.code, RejectionCode.STATE_REVISION_CONFLICT)

    # ------------------------------------------------------------------
    # Test R: Stale persistence revision on UNPOSTED decision rejected
    # ------------------------------------------------------------------
    def test_r_stale_persistence_revision_on_unposted_decision_rejected(self) -> None:
        stx = self._create_staged_tx(amount=Decimal("220.00"), direction="OUTFLOW")
        dec_id, _ = self._record_classified_decision(staged_tx=stx, account_code="6111")

        state = self._hydrate_state()
        post_cmd = PostResidualBankClassificationCommand(
            command_id=f"cmd-post-stale-p-{uuid4().hex[:8]}",
            session_id=state.session_id,
            expected_state_revision=state.revision,
            expected_persistence_revision=state.persistence_revision + 10,  # Stale persistence revision!
            decision_id=dec_id,
            issued_at=datetime(2026, 3, 15, 12, 5, tzinfo=timezone.utc),
        )

        res = self.engine.apply(state=state, command=post_cmd)
        self.assertEqual(res.status, TransitionStatus.REJECTED)
        assert res.rejection is not None
        self.assertEqual(res.rejection.code, RejectionCode.PERSISTENCE_REVISION_CONFLICT)

    # ------------------------------------------------------------------
    # Test S: Stage 1 consumed bank item rejected
    # ------------------------------------------------------------------
    def test_s_stage1_consumed_bank_item_rejected(self) -> None:
        stx = self._create_staged_tx(amount=Decimal("230.00"), direction="OUTFLOW")
        dec_id, _ = self._record_classified_decision(staged_tx=stx, account_code="6111")

        fresh_state = self._hydrate_state()

        # Simulate durable Stage 1 payment application consuming stx after hydration
        pa = BookkeepingPaymentApplication.objects.create(
            id=f"pa-{uuid4().hex[:8]}",
            entity=self.entity,
            staged_transaction=stx,
            total_amount_units=2_300_000,
            direction="OUTFLOW",
            currency="USD",
            session_id="session-pa",
            state_revision=1,
            created_at=datetime.now(timezone.utc),
        )
        try:
            post_cmd = PostResidualBankClassificationCommand(
                command_id=f"cmd-post-pa-{uuid4().hex[:8]}",
                session_id=fresh_state.session_id,
                expected_state_revision=fresh_state.revision,
                expected_persistence_revision=fresh_state.persistence_revision,
                decision_id=dec_id,
                issued_at=datetime(2026, 3, 15, 12, 5, tzinfo=timezone.utc),
            )

            res = self.engine.apply(state=fresh_state, command=post_cmd)
            self.assertEqual(res.status, TransitionStatus.REJECTED)
            assert res.rejection is not None
            self.assertEqual(res.rejection.code, RejectionCode.NOT_RESIDUAL_BANK_ITEM)
        finally:
            pa.delete()

    # ------------------------------------------------------------------
    # Test T: Stage 2 fully consumed bank item rejected
    # ------------------------------------------------------------------
    def test_t_stage2_fully_consumed_bank_item_rejected(self) -> None:
        stx = self._create_staged_tx(amount=Decimal("240.00"), direction="OUTFLOW")
        dec_id, _ = self._record_classified_decision(staged_tx=stx, account_code="6111")

        # Prior Stage 2 consumes 100% (240.00)
        bank_ledger = get_or_create_bank_ledger(bank_account=self.bank_account)
        je_prior, (b_tx, _) = bank_ledger.commit_txs(
            je_timestamp=date(2026, 3, 10),
            je_txs=[
                {"account": self.cash_account, "amount": Decimal("240.00"), "tx_type": CREDIT, "description": "Full prior"},
                {"account": self.expense_account, "amount": Decimal("240.00"), "tx_type": DEBIT, "description": "Full prior"},
            ],
            je_posted=True,
        )
        prior_recon = BookkeepingReconciliation.objects.create(
            id=f"rec_prior_full_{uuid4().hex[:8]}",
            entity=self.entity,
            state_revision_at_creation=0,
            created_at=datetime.now(timezone.utc),
        )
        BookkeepingReconciliationBankAllocation.objects.create(
            reconciliation=prior_recon,
            staged_transaction=stx,
            amount_units=2_400_000,
        )
        BookkeepingReconciliationBookAllocation.objects.create(
            reconciliation=prior_recon,
            transaction=b_tx,
            amount_units=2_400_000,
        )

        state = self._hydrate_state()
        post_cmd = PostResidualBankClassificationCommand(
            command_id=f"cmd-post-consumed-{uuid4().hex[:8]}",
            session_id=state.session_id,
            expected_state_revision=state.revision,
            expected_persistence_revision=state.persistence_revision,
            decision_id=dec_id,
            issued_at=datetime(2026, 3, 15, 12, 5, tzinfo=timezone.utc),
        )

        res = self.engine.apply(state=state, command=post_cmd)
        self.assertEqual(res.status, TransitionStatus.REJECTED)
        assert res.rejection is not None
        self.assertEqual(res.rejection.code, RejectionCode.NOT_RESIDUAL_BANK_ITEM)

    # ------------------------------------------------------------------
    # Test U: Partial residual mismatch rejected
    # ------------------------------------------------------------------
    def test_u_partial_residual_mismatch_rejected(self) -> None:
        stx = self._create_staged_tx(amount=Decimal("250.00"), direction="OUTFLOW")
        dec_id, _ = self._record_classified_decision(staged_tx=stx, account_code="6111")

        state = self._hydrate_state()

        # Command specifies conflicting residual amount units
        post_cmd = PostResidualBankClassificationCommand(
            command_id=f"cmd-post-mismatch-{uuid4().hex[:8]}",
            session_id=state.session_id,
            expected_state_revision=state.revision,
            expected_persistence_revision=state.persistence_revision,
            decision_id=dec_id,
            residual_amount_units=500_000,  # Decision has 1_000_000
            issued_at=datetime(2026, 3, 15, 12, 5, tzinfo=timezone.utc),
        )

        res = self.engine.apply(state=state, command=post_cmd)
        self.assertEqual(res.status, TransitionStatus.REJECTED)
        assert res.rejection is not None
        self.assertEqual(res.rejection.code, RejectionCode.RESIDUAL_AMOUNT_MISMATCH)

    # ------------------------------------------------------------------
    # Test V: Account outside default CoA / inactive rejected
    # ------------------------------------------------------------------
    def test_v_account_outside_default_coa_or_inactive_rejected(self) -> None:
        stx = self._create_staged_tx(amount=Decimal("100.00"), direction="OUTFLOW")
        dec_id, _ = self._record_classified_decision(staged_tx=stx, account_code="6111")

        fresh_state = self._hydrate_state()

        # 1. Inactive account rejection
        self.expense_account.active = False
        self.expense_account.save(update_fields=["active"])
        try:
            post_cmd = PostResidualBankClassificationCommand(
                command_id=f"cmd-post-inactive-{uuid4().hex[:8]}",
                session_id=fresh_state.session_id,
                expected_state_revision=fresh_state.revision,
                expected_persistence_revision=fresh_state.persistence_revision,
                decision_id=dec_id,
                issued_at=datetime(2026, 3, 15, 12, 5, tzinfo=timezone.utc),
            )
            res = self.engine.apply(state=fresh_state, command=post_cmd)
            self.assertEqual(res.status, TransitionStatus.REJECTED)
            assert res.rejection is not None
            self.assertEqual(res.rejection.code, RejectionCode.INACTIVE_ACCOUNT)
        finally:
            self.expense_account.active = True
            self.expense_account.save(update_fields=["active"])

        # 2. Account outside default CoA rejection
        self.expense_account.coa_model = self.other_coa
        self.expense_account.save(update_fields=["coa_model"])
        try:
            post_cmd_coa = PostResidualBankClassificationCommand(
                command_id=f"cmd-post-outside-coa-{uuid4().hex[:8]}",
                session_id=fresh_state.session_id,
                expected_state_revision=fresh_state.revision,
                expected_persistence_revision=fresh_state.persistence_revision,
                decision_id=dec_id,
                issued_at=datetime(2026, 3, 15, 12, 5, tzinfo=timezone.utc),
            )
            res_coa = self.engine.apply(state=fresh_state, command=post_cmd_coa)
            self.assertEqual(res_coa.status, TransitionStatus.REJECTED)
            assert res_coa.rejection is not None
            self.assertEqual(res_coa.rejection.code, RejectionCode.ACCOUNT_OUTSIDE_DEFAULT_COA)
        finally:
            self.expense_account.coa_model = self.coa
            self.expense_account.save(update_fields=["coa_model"])

    # ------------------------------------------------------------------
    # Test W: Deterministic bank ledger locked => fail, no accounting rows
    # ------------------------------------------------------------------
    def test_w_deterministic_bank_ledger_locked_fails_closed(self) -> None:
        stx = self._create_staged_tx(amount=Decimal("260.00"), direction="OUTFLOW")
        dec_id, _ = self._record_classified_decision(staged_tx=stx, account_code="6111")

        bank_ledger = get_or_create_bank_ledger(bank_account=self.bank_account)
        bank_ledger.locked = True
        bank_ledger.save(update_fields=["locked"])

        fresh_state = self._hydrate_state()
        post_cmd = PostResidualBankClassificationCommand(
            command_id=f"cmd-post-locked-{uuid4().hex[:8]}",
            session_id=fresh_state.session_id,
            expected_state_revision=fresh_state.revision,
            expected_persistence_revision=fresh_state.persistence_revision,
            decision_id=dec_id,
            issued_at=datetime(2026, 3, 15, 12, 5, tzinfo=timezone.utc),
        )

        res = self.engine.apply(state=fresh_state, command=post_cmd)
        self.assertEqual(res.status, TransitionStatus.REJECTED)
        assert res.rejection is not None
        self.assertEqual(res.rejection.code, RejectionCode.BANK_LEDGER_LOCKED)

        # Ensure no residual bank postings were created
        self.assertFalse(BookkeepingResidualBankPosting.objects.filter(classification_id=dec_id).exists())

    # ------------------------------------------------------------------
    # Test X: Closed accounting period => full atomic rollback
    # ------------------------------------------------------------------
    def test_x_closed_accounting_period_full_atomic_rollback(self) -> None:
        stx = self._create_staged_tx(
            amount=Decimal("270.00"),
            direction="OUTFLOW",
            date_posted=date(2026, 3, 15),
        )
        dec_id, _ = self._record_classified_decision(staged_tx=stx, account_code="6111")

        # Close accounting period after stx date
        self.entity.last_closing_date = date(2026, 3, 31)
        self.entity.save(update_fields=["last_closing_date"])

        fresh_state = self._hydrate_state()
        post_cmd = PostResidualBankClassificationCommand(
            command_id=f"cmd-post-closed-{uuid4().hex[:8]}",
            session_id=fresh_state.session_id,
            expected_state_revision=fresh_state.revision,
            expected_persistence_revision=fresh_state.persistence_revision,
            decision_id=dec_id,
            issued_at=datetime(2026, 3, 15, 12, 5, tzinfo=timezone.utc),
        )

        res = self.engine.apply(state=fresh_state, command=post_cmd)
        self.assertEqual(res.status, TransitionStatus.REJECTED)
        assert res.rejection is not None
        self.assertEqual(res.rejection.code, RejectionCode.CLOSED_ACCOUNTING_PERIOD)

        # Rollback: no JE, no posting, no reconciliation
        self.assertFalse(JournalEntryModel.objects.filter(origin="residual_bank_classification").exists())
        self.assertFalse(BookkeepingResidualBankPosting.objects.filter(classification_id=dec_id).exists())

    # ------------------------------------------------------------------
    # Test Y: Forced failure after commit_txs => full atomic rollback
    # ------------------------------------------------------------------
    def test_y_forced_failure_after_commit_txs_full_atomic_rollback(self) -> None:
        stx = self._create_staged_tx(amount=Decimal("280.00"), direction="OUTFLOW")
        dec_id, _ = self._record_classified_decision(staged_tx=stx, account_code="6111")

        state = self._hydrate_state()
        post_cmd = PostResidualBankClassificationCommand(
            command_id=f"cmd-post-fail1-{uuid4().hex[:8]}",
            session_id=state.session_id,
            expected_state_revision=state.revision,
            expected_persistence_revision=state.persistence_revision,
            decision_id=dec_id,
            issued_at=datetime(2026, 3, 15, 12, 5, tzinfo=timezone.utc),
        )

        with patch(
            "bookkeeping_state.persistence.writer._persist_reconciliation_record",
            side_effect=PersistenceError("Simulated failure after commit_txs"),
        ):
            res = self.engine.apply(state=state, command=post_cmd)

        self.assertEqual(res.status, TransitionStatus.REJECTED)
        self.assertFalse(BookkeepingResidualBankPosting.objects.filter(classification_id=dec_id).exists())
        self.assertFalse(JournalEntryModel.objects.filter(origin="residual_bank_classification").exists())

    # ------------------------------------------------------------------
    # Test Z: Forced failure after reconciliation => full atomic rollback
    # ------------------------------------------------------------------
    def test_z_forced_failure_after_reconciliation_full_atomic_rollback(self) -> None:
        stx = self._create_staged_tx(amount=Decimal("290.00"), direction="OUTFLOW")
        dec_id, _ = self._record_classified_decision(staged_tx=stx, account_code="6111")

        state = self._hydrate_state()
        post_cmd = PostResidualBankClassificationCommand(
            command_id=f"cmd-post-fail2-{uuid4().hex[:8]}",
            session_id=state.session_id,
            expected_state_revision=state.revision,
            expected_persistence_revision=state.persistence_revision,
            decision_id=dec_id,
            issued_at=datetime(2026, 3, 15, 12, 5, tzinfo=timezone.utc),
        )

        with patch.object(
            BookkeepingResidualBankPosting.objects,
            "create",
            side_effect=PersistenceError("Simulated failure after reconciliation"),
        ):
            res = self.engine.apply(state=state, command=post_cmd)

        self.assertEqual(res.status, TransitionStatus.REJECTED)
        self.assertFalse(BookkeepingResidualBankPosting.objects.filter(classification_id=dec_id).exists())
        self.assertFalse(JournalEntryModel.objects.filter(origin="residual_bank_classification").exists())
        self.assertFalse(
            BookkeepingReconciliation.objects.filter(
                semantic_rationale__contains=dec_id
            ).exists()
        )


class ResidualBankPostingConcurrencyTests(TransactionTestCase):
    """
    Multithreaded concurrency tests proving serialization and mutual exclusion
    under PostgreSQL row locking.
    """

    def setUp(self) -> None:
        self.user = cast(Any, UserModel.objects).create_user(
            username=f"conc_user_{uuid4().hex[:8]}",
            email="conc@test.com",
            password="testpassword123",
        )
        self.entity = EntityModel.add_root(
            name="Concurrency Test Corp",
            admin=self.user,
            currency="USD",
            fy_start_month=1,
            accrual_method=False,
        )
        self.coa = self.entity.create_chart_of_accounts(
            coa_name="Default CoA",
            assign_as_default=True,
            commit=True,
        )
        asset_root = self.coa.accountmodel_set.get(code="01000000")
        self.cash_account = asset_root.add_child(
            coa_model=self.coa,
            code="1010",
            name="Cash Account",
            role=ASSET_CA_CASH,
            balance_type=DEBIT,
            active=True,
        )
        expense_root = self.coa.accountmodel_set.get(code="06000000")
        self.expense_account = expense_root.add_child(
            coa_model=self.coa,
            code="6111",
            name="Office Supplies Expense",
            role=EXPENSE_OPERATIONAL,
            balance_type=DEBIT,
            active=True,
        )
        self.bank_account = BankAccountModel.objects.create(
            entity_model=self.entity,
            account_model=self.cash_account,
            name="Main Operating Account",
            active=True,
        )
        self.import_job = ImportJobModel.objects.create(
            bank_account_model=self.bank_account,
            description="Conc Import Job",
        )
        self.repo = BookkeepingRepository()
        self.engine = TransitionEngine(repository=self.repo)

    def _hydrate(self, session_id: str = "conc-sess"):
        hydrator = BookkeepingHydrator(repository=self.repo)
        return hydrator.hydrate(
            company_id=str(self.entity.uuid),
            session_id=session_id,
        )

    def _create_staged_tx(self, amount: Decimal = Decimal("100.00")) -> StagedTransactionModel:
        return StagedTransactionModel.objects.create(
            import_job=self.import_job,
            fit_id=f"fit_{uuid4().hex[:12]}",
            date_posted=date(2026, 3, 15),
            amount=-amount,
            name="Conc Vendor",
            memo="Conc payment",
        )

    # ------------------------------------------------------------------
    # Test AA: Concurrent Post D vs Invalidate D
    # ------------------------------------------------------------------
    def test_aa_concurrent_post_vs_invalidate_serializes(self) -> None:
        self.assertEqual(
            connection.vendor,
            "postgresql",
            "PostgreSQL row locking required for multi-threaded OCC proof.",
        )

        stx = self._create_staged_tx(amount=Decimal("300.00"))
        state = self._hydrate("setup-sess")
        dec_id = f"dec-aa-{uuid4().hex[:8]}"

        rec_cmd = RecordResidualBankClassificationCommand(
            command_id="cmd-rec-aa",
            session_id=state.session_id,
            expected_state_revision=state.revision,
            expected_persistence_revision=state.persistence_revision,
            decision_id=dec_id,
            bank_item_id=f"staged:{stx.uuid}",
            bank_account_id=str(self.bank_account.uuid),
            status=ResidualBankClassificationStatus.CLASSIFIED,
            account_code="6111",
            confidence=0.99,
            original_amount_units=3_000_000,
            residual_amount_units=3_000_000,
            direction=Direction.OUTFLOW,
            currency="USD",
            schema_version="v1",
            dag_id="pcm_bank_cash_accounting_dag",
            request_semantic_digest="digest-aa",
            issued_at=datetime(2026, 3, 15, 12, 0, tzinfo=timezone.utc),
        )
        self.engine.apply(state=state, command=rec_cmd)

        # Both workers prepared against same revision after decision
        state_post = self._hydrate("post-sess")
        state_inv = self._hydrate("inv-sess")

        post_cmd = PostResidualBankClassificationCommand(
            command_id="cmd-post-aa",
            session_id=state_post.session_id,
            expected_state_revision=state_post.revision,
            expected_persistence_revision=state_post.persistence_revision,
            decision_id=dec_id,
            issued_at=datetime(2026, 3, 15, 12, 5, tzinfo=timezone.utc),
        )

        inv_cmd = InvalidateResidualBankClassificationCommand(
            command_id="cmd-inv-aa",
            session_id=state_inv.session_id,
            expected_state_revision=state_inv.revision,
            expected_persistence_revision=state_inv.persistence_revision,
            decision_id=dec_id,
            invalidation_id=f"inv-aa-{uuid4().hex[:8]}",
            reason="Race invalidation",
            issued_at=datetime(2026, 3, 15, 12, 5, tzinfo=timezone.utc),
        )

        results = []

        def run_post():
            connection.close()
            res = self.engine.apply(state=state_post, command=post_cmd)
            results.append(("post", res))

        def run_inv():
            connection.close()
            res = self.engine.apply(state=state_inv, command=inv_cmd)
            results.append(("inv", res))

        with concurrent.futures.ThreadPoolExecutor(max_workers=2) as executor:
            f1 = executor.submit(run_post)
            f2 = executor.submit(run_inv)
            f1.result()
            f2.result()

        # Exactly one succeeded, one failed
        applied = [kind for kind, r in results if r.applied or r.applied_requires_rehydration]
        rejected = [kind for kind, r in results if r.rejected]
        self.assertEqual(len(applied), 1)
        self.assertEqual(len(rejected), 1)

        # Never both committed
        has_posting = BookkeepingResidualBankPosting.objects.filter(classification_id=dec_id).exists()
        has_invalidation = BookkeepingResidualBankClassificationInvalidation.objects.filter(
            classification_id=dec_id
        ).exists()
        self.assertFalse(has_posting and has_invalidation)

    # ------------------------------------------------------------------
    # Test AB: Concurrent Post D vs Supersede D
    # ------------------------------------------------------------------
    def test_ab_concurrent_post_vs_supersede_serializes(self) -> None:
        self.assertEqual(
            connection.vendor,
            "postgresql",
            "PostgreSQL row locking required for multi-threaded OCC proof.",
        )

        stx = self._create_staged_tx(amount=Decimal("350.00"))
        state = self._hydrate("setup-sess")
        dec_id = f"dec-ab-{uuid4().hex[:8]}"

        rec_cmd = RecordResidualBankClassificationCommand(
            command_id="cmd-rec-ab",
            session_id=state.session_id,
            expected_state_revision=state.revision,
            expected_persistence_revision=state.persistence_revision,
            decision_id=dec_id,
            bank_item_id=f"staged:{stx.uuid}",
            bank_account_id=str(self.bank_account.uuid),
            status=ResidualBankClassificationStatus.CLASSIFIED,
            account_code="6111",
            confidence=0.99,
            original_amount_units=3_500_000,
            residual_amount_units=3_500_000,
            direction=Direction.OUTFLOW,
            currency="USD",
            schema_version="v1",
            dag_id="pcm_bank_cash_accounting_dag",
            request_semantic_digest="digest-ab",
            issued_at=datetime(2026, 3, 15, 12, 0, tzinfo=timezone.utc),
        )
        self.engine.apply(state=state, command=rec_cmd)

        state_post = self._hydrate("post-sess")
        state_sup = self._hydrate("sup-sess")

        post_cmd = PostResidualBankClassificationCommand(
            command_id="cmd-post-ab",
            session_id=state_post.session_id,
            expected_state_revision=state_post.revision,
            expected_persistence_revision=state_post.persistence_revision,
            decision_id=dec_id,
            issued_at=datetime(2026, 3, 15, 12, 5, tzinfo=timezone.utc),
        )

        sup_cmd = RecordResidualBankClassificationCommand(
            command_id="cmd-sup-ab",
            session_id=state_sup.session_id,
            expected_state_revision=state_sup.revision,
            expected_persistence_revision=state_sup.persistence_revision,
            decision_id=f"dec-ab-sup-{uuid4().hex[:8]}",
            supersedes_decision_id=dec_id,
            bank_item_id=f"staged:{stx.uuid}",
            bank_account_id=str(self.bank_account.uuid),
            status=ResidualBankClassificationStatus.CLASSIFIED,
            account_code="6111",
            confidence=0.99,
            original_amount_units=3_500_000,
            residual_amount_units=3_500_000,
            direction=Direction.OUTFLOW,
            currency="USD",
            schema_version="v1",
            dag_id="pcm_bank_cash_accounting_dag",
            request_semantic_digest="digest-ab-sup",
            issued_at=datetime(2026, 3, 15, 12, 5, tzinfo=timezone.utc),
        )

        results = []

        def run_post():
            connection.close()
            res = self.engine.apply(state=state_post, command=post_cmd)
            results.append(("post", res))

        def run_sup():
            connection.close()
            res = self.engine.apply(state=state_sup, command=sup_cmd)
            results.append(("sup", res))

        with concurrent.futures.ThreadPoolExecutor(max_workers=2) as executor:
            f1 = executor.submit(run_post)
            f2 = executor.submit(run_sup)
            f1.result()
            f2.result()

        applied = [kind for kind, r in results if r.applied or r.applied_requires_rehydration]
        rejected = [kind for kind, r in results if r.rejected]
        self.assertEqual(len(applied), 1)
        self.assertEqual(len(rejected), 1)

    # ------------------------------------------------------------------
    # Test AC: Concurrent duplicate Post D
    # ------------------------------------------------------------------
    def test_ac_concurrent_duplicate_post_serializes(self) -> None:
        self.assertEqual(
            connection.vendor,
            "postgresql",
            "PostgreSQL row locking required for multi-threaded OCC proof.",
        )

        stx = self._create_staged_tx(amount=Decimal("400.00"))
        state = self._hydrate("setup-sess")
        dec_id = f"dec-ac-{uuid4().hex[:8]}"

        rec_cmd = RecordResidualBankClassificationCommand(
            command_id="cmd-rec-ac",
            session_id=state.session_id,
            expected_state_revision=state.revision,
            expected_persistence_revision=state.persistence_revision,
            decision_id=dec_id,
            bank_item_id=f"staged:{stx.uuid}",
            bank_account_id=str(self.bank_account.uuid),
            status=ResidualBankClassificationStatus.CLASSIFIED,
            account_code="6111",
            confidence=0.99,
            original_amount_units=4_000_000,
            residual_amount_units=4_000_000,
            direction=Direction.OUTFLOW,
            currency="USD",
            schema_version="v1",
            dag_id="pcm_bank_cash_accounting_dag",
            request_semantic_digest="digest-ac",
            issued_at=datetime(2026, 3, 15, 12, 0, tzinfo=timezone.utc),
        )
        self.engine.apply(state=state, command=rec_cmd)

        state1 = self._hydrate("post1-sess")
        state2 = self._hydrate("post2-sess")

        cmd1 = PostResidualBankClassificationCommand(
            command_id="cmd-post-ac-1",
            session_id=state1.session_id,
            expected_state_revision=state1.revision,
            expected_persistence_revision=state1.persistence_revision,
            decision_id=dec_id,
            issued_at=datetime(2026, 3, 15, 12, 5, tzinfo=timezone.utc),
        )

        cmd2 = PostResidualBankClassificationCommand(
            command_id="cmd-post-ac-2",
            session_id=state2.session_id,
            expected_state_revision=state2.revision,
            expected_persistence_revision=state2.persistence_revision,
            decision_id=dec_id,
            issued_at=datetime(2026, 3, 15, 12, 5, tzinfo=timezone.utc),
        )

        results = []

        def run_post(cmd, st):
            connection.close()
            res = self.engine.apply(state=st, command=cmd)
            results.append(res)

        with concurrent.futures.ThreadPoolExecutor(max_workers=2) as executor:
            f1 = executor.submit(run_post, cmd1, state1)
            f2 = executor.submit(run_post, cmd2, state2)
            f1.result()
            f2.result()

        # At most one committed the posting; exactly one posting exists
        self.assertEqual(BookkeepingResidualBankPosting.objects.filter(classification_id=dec_id).count(), 1)
        self.assertEqual(
            JournalEntryModel.objects.filter(
                origin="residual_bank_classification",
                residual_bank_posting__classification_id=dec_id,
            ).count(),
            1,
        )
