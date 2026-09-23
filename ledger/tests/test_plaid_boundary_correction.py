from datetime import date, datetime, timezone
from decimal import Decimal
import subprocess
import sys
import uuid

from bookkeeping_state.domain.commands import ApplyPaymentCommand
from bookkeeping_state.domain.enums import (
    AllocationSupport,
    Direction,
    SemanticAdmissibility,
)
from bookkeeping_state.domain.reconciliations import (
    BankAllocation,
    BookAllocation,
    Reconciliation,
)
from bookkeeping_state.payment_application.coordinator import plan_two_stage_bookkeeping
from bookkeeping_state.persistence.writer import PersistenceWriteSet
from bookkeeping_state.state.queries import BookkeepingQueries
from bookkeeping_state.transitions.result import TransitionStatus
from ledger.models.bookkeeping import BookkeepingPaymentApplication
from ledger.models.data_import import StagedTransactionModel
from ledger.models.plaid import PlaidTransaction
from ledger.tests.test_stage1_accounting_execution import Stage1AccountingExecutionTestBase


class PlaidBoundaryCorrectionTests(Stage1AccountingExecutionTestBase):
    """
    Focused test suite proving PlaidTransaction is completely excluded from
    the BookkeepingState / BankItem production boundary, while authoritative
    StagedTransactionModel records hydrate and support Stage 1 and Stage 2 workflows.
    """

    def setUp(self) -> None:
        super().setUp()

        # Upstream Plaid rows (ingestion concern)
        self.plaid_tx_outflow = self._create_plaid_movement(
            amount=Decimal("125.00"),
            dt=date(2026, 4, 10),
            name="Software Vendor SaaS",
        )
        self.plaid_tx_inflow = self._create_plaid_movement(
            amount=Decimal("-500.00"),
            dt=date(2026, 4, 11),
            name="Client Wire Transfer",
        )

        # Authoritative Staged transactions
        self.staged_tx_1 = self._create_staged_movement(
            amount=Decimal("300.00"),
            dt=date(2026, 4, 12),
            name="Customer Wire Deposit",
        )
        self.staged_tx_2 = self._create_staged_movement(
            amount=Decimal("-45.50"),
            dt=date(2026, 4, 13),
            name="Office Supplies Purchase",
        )

    def test_1_production_hydration_creates_no_bank_item_from_plaid_transaction(self) -> None:
        """1. Production hydration creates NO BankItem from PlaidTransaction."""
        snapshot = self.repo.load_snapshot(company_id=str(self.entity.uuid))

        plaid_item_ids = [b.id for b in snapshot.bank_items if b.id.startswith("plaid:")]
        self.assertEqual(len(plaid_item_ids), 0)
        self.assertFalse(any(b.id == f"plaid:{self.plaid_tx_outflow.uuid}" for b in snapshot.bank_items))
        self.assertFalse(any(b.id == f"plaid:{self.plaid_tx_inflow.uuid}" for b in snapshot.bank_items))

    def test_2_plaid_rows_can_exist_without_appearing_in_bookkeeping_state_bank_items(self) -> None:
        """2. Plaid rows can exist in DB without appearing in BookkeepingState.bank_items."""
        self.assertGreaterEqual(PlaidTransaction.objects.filter(plaid_item=self.plaid_item).count(), 2)

        state = self.hydrator.hydrate(company_id=str(self.entity.uuid))

        self.assertNotIn(f"plaid:{self.plaid_tx_outflow.uuid}", state.bank_items)
        self.assertNotIn(f"plaid:{self.plaid_tx_inflow.uuid}", state.bank_items)
        self.assertFalse(any(b_id.startswith("plaid:") for b_id in state.bank_items))

    def test_3_authoritative_bank_side_model_does_hydrate_into_bank_item(self) -> None:
        """3. The authoritative bank-side model (StagedTransactionModel) DOES hydrate into BankItem."""
        state = self.hydrator.hydrate(company_id=str(self.entity.uuid))

        staged_id_1 = f"staged:{self.staged_tx_1.uuid}"
        staged_id_2 = f"staged:{self.staged_tx_2.uuid}"

        self.assertIn(staged_id_1, state.bank_items)
        self.assertIn(staged_id_2, state.bank_items)

        item1 = state.bank_items[staged_id_1]
        self.assertEqual(item1.id, staged_id_1)
        self.assertEqual(item1.bank_account_id, str(self.bank_account.uuid))
        self.assertEqual(item1.direction, Direction.BANK_INFLOW)
        self.assertEqual(item1.amount_units, "3000000")
        self.assertEqual(item1.date, date(2026, 4, 12))
        self.assertEqual(item1.description, "Customer Wire Deposit")
        self.assertEqual(item1.provenance_refs, (str(self.staged_tx_1.uuid),))

        item2 = state.bank_items[staged_id_2]
        self.assertEqual(item2.id, staged_id_2)
        self.assertEqual(item2.bank_account_id, str(self.bank_account.uuid))
        self.assertEqual(item2.direction, Direction.BANK_OUTFLOW)
        self.assertEqual(item2.amount_units, "455000")
        self.assertEqual(item2.date, date(2026, 4, 13))
        self.assertEqual(item2.description, "Office Supplies Purchase")
        self.assertEqual(item2.provenance_refs, (str(self.staged_tx_2.uuid),))

    def test_4_stage1_still_works_with_authoritative_bank_items(self) -> None:
        """4. Stage 1 executes successfully with authoritative BankItems."""
        inv = self._create_approved_invoice(amount=Decimal("300.00"), dt=date(2026, 4, 1))

        state = self.hydrator.hydrate(company_id=str(self.entity.uuid), session_id="session-stage1-auth")
        queries = BookkeepingQueries(state)

        two_stage_plan = plan_two_stage_bookkeeping(queries)
        staged_id = f"staged:{self.staged_tx_1.uuid}"

        proposals = [p for p in two_stage_plan.payment_application_plan.proposals if p.bank_item_id == staged_id]
        self.assertEqual(len(proposals), 1)
        proposal = proposals[0]

        capability = two_stage_plan.get_capability(staged_id)
        self.assertIsNotNone(capability)

        pay_app_id = f"payapp-{uuid.uuid4().hex[:6]}"
        cmd = ApplyPaymentCommand.from_intent(
            command_id=f"cmd-{uuid.uuid4().hex[:6]}",
            expected_state_revision=state.revision,
            session_id=state.session_id,
            issued_at=datetime(2026, 4, 12, 12, 0, tzinfo=timezone.utc),
            payment_application_id=pay_app_id,
            plan_intent=proposal.intent,
            capability=capability,
        )

        res = self.engine.apply(state=state, command=cmd)
        self.assertEqual(res.status, TransitionStatus.APPLIED_REQUIRES_REHYDRATION)
        self.assertTrue(state.is_closed)

        durable_app = BookkeepingPaymentApplication.objects.get(id=pay_app_id)
        self.assertEqual(durable_app.total_amount_units, 300_0000)
        self.assertEqual(durable_app.staged_transaction, self.staged_tx_1)
        self.assertIsNone(durable_app.plaid_transaction)

    def test_5_stage2_still_works_with_authoritative_bank_items(self) -> None:
        """5. Stage 2 executes successfully with authoritative BankItems."""
        cash_tx = self._create_cash_tx(amount=Decimal("300.00"), is_debit=True, dt=date(2026, 4, 12))

        staged_id = f"staged:{self.staged_tx_1.uuid}"
        now = datetime.now(timezone.utc)

        rec = Reconciliation(
            id=f"rec-{uuid.uuid4().hex[:6]}",
            bank_allocations=(
                BankAllocation(
                    bank_item_id=staged_id,
                    amount_units="3000000",
                ),
            ),
            book_allocations=(
                BookAllocation(
                    book_item_id=f"tx:{cash_tx.uuid}",
                    amount_units="3000000",
                ),
            ),
            evidence_refs=(),
            source_hypothesis_id="hypo-staged-01",
            source_hypothesis_state_revision=0,
            source_hypothesis_utility=950,
            source_hypothesis_generated_at=now,
            source_hypothesis_admissibility=SemanticAdmissibility.SUPPORTED,
            source_hypothesis_allocation_support=AllocationSupport.EXPLICIT_EVIDENCE,
            semantic_rationale="Authoritative staged match",
            session_id="session-stage2",
            state_revision_at_creation=1,
            created_at=now,
        )

        commit_res = self.repo.commit(
            company_id=str(self.entity.uuid),
            expected_revision=0,
            write_set=PersistenceWriteSet(reconciliations=(rec,)),
        )
        self.assertEqual(commit_res.new_revision, 1)

        snapshot = self.repo.load_snapshot(company_id=str(self.entity.uuid))
        self.assertEqual(len(snapshot.reconciliations), 1)
        self.assertEqual(snapshot.reconciliations[0].bank_allocations[0].bank_item_id, staged_id)

    def test_6_no_plaid_ids_appear_in_production_bank_item_queries(self) -> None:
        """6. No plaid:<uuid> IDs appear in production BankItem queries."""
        state = self.hydrator.hydrate(company_id=str(self.entity.uuid))
        queries = BookkeepingQueries(state)

        # 1. Unreconciled bank items query
        unreconciled = queries.unreconciled_bank_items()
        self.assertTrue(len(unreconciled) > 0)
        for bi in unreconciled:
            self.assertFalse(
                bi.id.startswith("plaid:"),
                f"Found illegal Plaid BankItem ID '{bi.id}' in unreconciled_bank_items()"
            )
            self.assertTrue(bi.id.startswith("staged:"))

        # 2. Bank items for account query
        for ba in state.bank_accounts.values():
            ba_items = queries.bank_items_for_account(ba.id)
            for bi in ba_items:
                self.assertFalse(
                    bi.id.startswith("plaid:"),
                    f"Found illegal Plaid BankItem ID '{bi.id}' in bank_items_for_account()"
                )
                self.assertTrue(bi.id.startswith("staged:"))

        # 3. Direct bank_item lookup by Plaid ID returns None
        self.assertIsNone(queries.bank_item(f"plaid:{self.plaid_tx_outflow.uuid}"))
        self.assertIsNone(queries.bank_item(f"plaid:{self.plaid_tx_inflow.uuid}"))

    def test_7_no_django_migration_is_created(self) -> None:
        """7. No Django migration is created."""
        result = subprocess.run(
            [sys.executable, "ledger/manage.py", "makemigrations", "--dry-run", "--check"],
            capture_output=True,
            text=True,
        )
        self.assertEqual(
            result.returncode,
            0,
            f"Expected no migration changes required, but makemigrations failed:\n{result.stdout}\n{result.stderr}"
        )
        self.assertIn("No changes detected", result.stdout)
