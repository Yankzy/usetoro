from __future__ import annotations

from datetime import date, datetime, timezone
from decimal import Decimal
import uuid

from django.test import TestCase

from bookkeeping_state.domain.bank import BankItem
from bookkeeping_state.domain.commands import (
    ApplyPaymentCommand,
    CommandSource,
    CreateReconciliationCommand,
)
from bookkeeping_state.domain.reconciliations import (
    BankAllocation,
    BookAllocation,
)
from bookkeeping_state.payment_application.coordinator import (
    plan_two_stage_bookkeeping,
)
from bookkeeping_state.state.fingerprint import state_fingerprint
from bookkeeping_state.state.queries import BookkeepingQueries
from bookkeeping_state.transitions.result import TransitionStatus
from ledger.tests.test_stage1_accounting_execution import (
    Stage1AccountingExecutionTestBase,
)


class ResidualUnmatchedBankItemsTests(Stage1AccountingExecutionTestBase):
    """
    Focused test suite proving that `residual_unmatched_bank_items()` on
    `BookkeepingQueries` correctly identifies and returns residual unmatched BankItems:

    A. Untouched staged: BankItem is returned with full remaining amount.
    B. Stage-1-consumed BankItem is excluded.
    C. Stage-2-fully-reconciled BankItem is excluded.
    D. Partial reconciliation returns only the residual amount.
    E. Mixed state returns exactly the correct residual set.
    F. Ordering is deterministic (item.date, item.id).
    G. No plaid: IDs can appear.
    H. State revision and fingerprint are unchanged after calling the query.
    """

    def test_a_untouched_staged_bank_item_returned_with_full_amount(self) -> None:
        """Test A: Untouched staged BankItem is returned with full remaining amount."""
        stx = self._create_staged_movement(amount=Decimal("150.00"), dt=date(2026, 5, 1))
        state = self.hydrator.hydrate(
            company_id=str(self.entity.uuid),
            session_id="session-test-a",
        )
        queries = BookkeepingQueries(state)
        residuals = queries.residual_unmatched_bank_items()

        self.assertEqual(len(residuals), 1)
        item, remaining_units = residuals[0]

        expected_id = f"staged:{stx.uuid}"
        self.assertEqual(item.id, expected_id)
        self.assertEqual(remaining_units, 1_500_000)
        self.assertEqual(remaining_units, item.amount_int)

    def test_b_stage1_consumed_bank_item_is_excluded(self) -> None:
        """Test B: Stage-1-consumed BankItem is excluded from residual unmatched items."""
        # 1. Create a staged movement matching an open invoice
        stx_consumed = self._create_staged_movement(amount=Decimal("200.00"), dt=date(2026, 5, 1))
        self._create_approved_invoice(amount=Decimal("200.00"), dt=date(2026, 4, 15))

        # 2. Also create an untouched staged movement
        stx_untouched = self._create_staged_movement(amount=Decimal("100.00"), dt=date(2026, 5, 2))

        # 3. Hydrate state 1 and execute Stage 1 payment application
        state1 = self.hydrator.hydrate(company_id=str(self.entity.uuid), session_id="session-test-b-1")
        queries1 = BookkeepingQueries(state1)
        plan = plan_two_stage_bookkeeping(queries1)

        consumed_id = f"staged:{stx_consumed.uuid}"
        untouched_id = f"staged:{stx_untouched.uuid}"

        self.assertEqual(len(plan.payment_application_plan.proposals), 1)
        proposal = plan.payment_application_plan.proposals[0]
        self.assertEqual(proposal.bank_item_id, consumed_id)

        capability = plan.get_capability(consumed_id)
        self.assertIsNotNone(capability)
        assert capability is not None

        cmd = ApplyPaymentCommand.from_intent(
            command_id=f"cmd-b-{uuid.uuid4().hex[:6]}",
            expected_state_revision=state1.revision,
            session_id=state1.session_id,
            issued_at=datetime(2026, 5, 1, 12, 0, tzinfo=timezone.utc),
            payment_application_id=f"payapp-b-{uuid.uuid4().hex[:6]}",
            plan_intent=proposal.intent,
            capability=capability,
        )
        res = self.engine.apply(state=state1, command=cmd)
        self.assertEqual(res.status, TransitionStatus.APPLIED_REQUIRES_REHYDRATION)

        # 4. Rehydrate state 2 after Stage 1 execution
        state2 = self.hydrator.hydrate(company_id=str(self.entity.uuid), session_id="session-test-b-2")
        queries2 = BookkeepingQueries(state2)

        residuals = queries2.residual_unmatched_bank_items()
        residual_ids = [item.id for item, _ in residuals]

        self.assertNotIn(consumed_id, residual_ids)
        self.assertIn(untouched_id, residual_ids)
        self.assertEqual(len(residuals), 1)
        self.assertEqual(residuals[0][1], 1_000_000)

    def test_c_stage2_fully_reconciled_bank_item_is_excluded(self) -> None:
        """Test C: Stage-2-fully-reconciled BankItem is excluded from residual unmatched items."""
        # 1. Create a staged movement and a posted cash transaction of identical amount ($300.00)
        stx_recon = self._create_staged_movement(amount=Decimal("300.00"), dt=date(2026, 5, 1))
        tx_cash = self._create_cash_tx(amount=Decimal("300.00"), is_debit=True, dt=date(2026, 5, 1))

        # 2. Also create an untouched staged movement
        stx_untouched = self._create_staged_movement(amount=Decimal("75.00"), dt=date(2026, 5, 2))

        # 3. Hydrate state
        state = self.hydrator.hydrate(company_id=str(self.entity.uuid), session_id="session-test-c")
        queries = BookkeepingQueries(state)

        recon_bank_id = f"staged:{stx_recon.uuid}"
        cash_book_id = f"tx:{tx_cash.uuid}"
        untouched_id = f"staged:{stx_untouched.uuid}"

        # 4. Reconcile in Stage 2 (100% of 3,000,000 units)
        rec_cmd = CreateReconciliationCommand(
            command_id=f"cmd-c-{uuid.uuid4().hex[:6]}",
            expected_state_revision=state.revision,
            source=CommandSource.RECONCILIATION,
            session_id=state.session_id,
            issued_at=datetime(2026, 5, 1, 12, 0, tzinfo=timezone.utc),
            reconciliation_id=f"rec-c-{uuid.uuid4().hex[:6]}",
            bank_allocations=(
                BankAllocation(bank_item_id=recon_bank_id, amount_units="3000000"),
            ),
            book_allocations=(
                BookAllocation(book_item_id=cash_book_id, amount_units="3000000"),
            ),
        )
        res = self.engine.apply(state=state, command=rec_cmd)
        self.assertEqual(res.status, TransitionStatus.APPLIED)

        # 5. Hydrate fresh state and check residual query
        fresh_state = self.hydrator.hydrate(company_id=str(self.entity.uuid), session_id="session-test-c-fresh")
        fresh_queries = BookkeepingQueries(fresh_state)

        residuals = fresh_queries.residual_unmatched_bank_items()
        residual_ids = [item.id for item, _ in residuals]

        self.assertNotIn(recon_bank_id, residual_ids)
        self.assertIn(untouched_id, residual_ids)
        self.assertEqual(len(residuals), 1)
        self.assertEqual(residuals[0][1], 750_000)

    def test_d_partial_reconciliation_returns_only_residual_amount(self) -> None:
        """Test D: Partial reconciliation returns only the residual amount, when supported in test state."""
        from unittest.mock import patch
        from bookkeeping_state.domain.context import AccountingPolicy

        def _policy_with_partial_bank(*args: Any, **kwargs: Any) -> AccountingPolicy:
            kwargs["allow_partial_bank_reconciliation"] = True
            return AccountingPolicy(*args, **kwargs)

        with patch("bookkeeping_state.persistence.reader.AccountingPolicy", side_effect=_policy_with_partial_bank):
            # 1. Bank movement of $500.00 (5,000,000 units), cash transaction of $200.00 (2,000,000 units)
            stx_partial = self._create_staged_movement(amount=Decimal("500.00"), dt=date(2026, 5, 1))
            tx_cash = self._create_cash_tx(amount=Decimal("200.00"), is_debit=True, dt=date(2026, 5, 1))

            state = self.hydrator.hydrate(company_id=str(self.entity.uuid), session_id="session-test-d")
            bank_id = f"staged:{stx_partial.uuid}"
            cash_id = f"tx:{tx_cash.uuid}"

            # 2. Reconcile 2,000,000 units
            rec_cmd = CreateReconciliationCommand(
                command_id=f"cmd-d-{uuid.uuid4().hex[:6]}",
                expected_state_revision=state.revision,
                source=CommandSource.RECONCILIATION,
                session_id=state.session_id,
                issued_at=datetime(2026, 5, 1, 12, 0, tzinfo=timezone.utc),
                reconciliation_id=f"rec-d-{uuid.uuid4().hex[:6]}",
                bank_allocations=(
                    BankAllocation(bank_item_id=bank_id, amount_units="2000000"),
                ),
                book_allocations=(
                    BookAllocation(book_item_id=cash_id, amount_units="2000000"),
                ),
            )
            res = self.engine.apply(state=state, command=rec_cmd)
            self.assertEqual(res.status, TransitionStatus.APPLIED)

            fresh_state = self.hydrator.hydrate(company_id=str(self.entity.uuid), session_id="session-test-d-fresh")
            fresh_queries = BookkeepingQueries(fresh_state)

            residuals = fresh_queries.residual_unmatched_bank_items()
            self.assertEqual(len(residuals), 1)

            item, remaining_units = residuals[0]
            self.assertEqual(item.id, bank_id)
            # Residual must be exactly 5,000,000 - 2,000,000 = 3,000,000 units ($300.00)
            self.assertEqual(remaining_units, 3_000_000)
            self.assertLess(remaining_units, item.amount_int)
            self.assertEqual(item.amount_int, 5_000_000)

    def test_e_mixed_state_returns_exactly_correct_residual_set(self) -> None:
        """Test E: Mixed state returns exactly the correct residual set and amounts."""
        from unittest.mock import patch
        from bookkeeping_state.domain.context import AccountingPolicy

        def _policy_with_partial_bank(*args: Any, **kwargs: Any) -> AccountingPolicy:
            kwargs["allow_partial_bank_reconciliation"] = True
            return AccountingPolicy(*args, **kwargs)

        with patch("bookkeeping_state.persistence.reader.AccountingPolicy", side_effect=_policy_with_partial_bank):
            # 1. Staged untouched 1: $100.00 -> remaining 1,000,000
            stx_u1 = self._create_staged_movement(amount=Decimal("100.00"), dt=date(2026, 5, 1), name="Untouched 1")
            # 2. Staged consumed by Stage 1: $200.00 -> excluded
            stx_s1 = self._create_staged_movement(amount=Decimal("200.00"), dt=date(2026, 5, 2), name="Stage 1 Consumed")
            self._create_approved_invoice(amount=Decimal("200.00"), dt=date(2026, 4, 1))
            # 3. Staged fully reconciled: $300.00 -> excluded
            stx_s2_full = self._create_staged_movement(amount=Decimal("300.00"), dt=date(2026, 5, 3), name="Recon Full")
            tx_cash_full = self._create_cash_tx(amount=Decimal("300.00"), is_debit=True, dt=date(2026, 5, 3))
            # 4. Staged partially reconciled: $500.00, $150.00 allocated -> remaining 3,500,000
            stx_s2_part = self._create_staged_movement(amount=Decimal("500.00"), dt=date(2026, 5, 4), name="Recon Partial")
            tx_cash_part = self._create_cash_tx(amount=Decimal("150.00"), is_debit=True, dt=date(2026, 5, 4))
            # 5. Staged untouched 2: $50.00 -> remaining 500,000
            stx_u2 = self._create_staged_movement(amount=Decimal("50.00"), dt=date(2026, 5, 5), name="Untouched 2")

            # Execute Stage 1
            state1 = self.hydrator.hydrate(company_id=str(self.entity.uuid), session_id="mixed-s1")
            queries1 = BookkeepingQueries(state1)
            plan1 = plan_two_stage_bookkeeping(queries1)
            s1_id = f"staged:{stx_s1.uuid}"
            proposal = next(p for p in plan1.payment_application_plan.proposals if p.bank_item_id == s1_id)
            capability = plan1.get_capability(s1_id)
            self.assertIsNotNone(capability)
            cmd_s1 = ApplyPaymentCommand.from_intent(
                command_id=f"cmd-m1-{uuid.uuid4().hex[:6]}",
                expected_state_revision=state1.revision,
                session_id=state1.session_id,
                issued_at=datetime(2026, 5, 2, 12, 0, tzinfo=timezone.utc),
                payment_application_id=f"payapp-m1-{uuid.uuid4().hex[:6]}",
                plan_intent=proposal.intent,
                capability=capability,
            )
            self.engine.apply(state=state1, command=cmd_s1)

            # Execute Stage 2 reconciliations
            state2 = self.hydrator.hydrate(company_id=str(self.entity.uuid), session_id="mixed-s2")
            # Fully reconcile stx_s2_full
            cmd_rec_full = CreateReconciliationCommand(
                command_id=f"cmd-mf-{uuid.uuid4().hex[:6]}",
                expected_state_revision=state2.revision,
                source=CommandSource.RECONCILIATION,
                session_id=state2.session_id,
                issued_at=datetime(2026, 5, 3, 12, 0, tzinfo=timezone.utc),
                reconciliation_id=f"rec-mf-{uuid.uuid4().hex[:6]}",
                bank_allocations=(
                    BankAllocation(bank_item_id=f"staged:{stx_s2_full.uuid}", amount_units="3000000"),
                ),
                book_allocations=(
                    BookAllocation(book_item_id=f"tx:{tx_cash_full.uuid}", amount_units="3000000"),
                ),
            )
            self.engine.apply(state=state2, command=cmd_rec_full)

            # Partially reconcile stx_s2_part (1,500,000 out of 5,000,000)
            state2_after = self.hydrator.hydrate(company_id=str(self.entity.uuid), session_id="mixed-s2-part")
            cmd_rec_part = CreateReconciliationCommand(
                command_id=f"cmd-mp-{uuid.uuid4().hex[:6]}",
                expected_state_revision=state2_after.revision,
                source=CommandSource.RECONCILIATION,
                session_id=state2_after.session_id,
                issued_at=datetime(2026, 5, 4, 12, 0, tzinfo=timezone.utc),
                reconciliation_id=f"rec-mp-{uuid.uuid4().hex[:6]}",
                bank_allocations=(
                    BankAllocation(bank_item_id=f"staged:{stx_s2_part.uuid}", amount_units="1500000"),
                ),
                book_allocations=(
                    BookAllocation(book_item_id=f"tx:{tx_cash_part.uuid}", amount_units="1500000"),
                ),
            )
            res_part = self.engine.apply(state=state2_after, command=cmd_rec_part)
            self.assertEqual(res_part.status, TransitionStatus.APPLIED)

            # Hydrate final state and inspect residual items
            final_state = self.hydrator.hydrate(company_id=str(self.entity.uuid), session_id="mixed-final")
            final_queries = BookkeepingQueries(final_state)

            residuals = final_queries.residual_unmatched_bank_items()
            residual_dict = {item.id: remaining for item, remaining in residuals}

            # Expected residual items:
            # u1: 1,000,000
            # part: 3,500,000
            # u2: 500,000
            expected = {
                f"staged:{stx_u1.uuid}": 1_000_000,
                f"staged:{stx_s2_part.uuid}": 3_500_000,
                f"staged:{stx_u2.uuid}": 500_000,
            }
            self.assertEqual(residual_dict, expected)
            self.assertEqual(len(residuals), 3)

            # Confirm Stage 1 and Stage 2 fully reconciled items are strictly absent
            self.assertNotIn(f"staged:{stx_s1.uuid}", residual_dict)

    def test_f_ordering_is_deterministic(self) -> None:
        """Test F: Residual unmatched items are sorted deterministically by (date, id)."""
        stx_d = self._create_staged_movement(amount=Decimal("10.00"), dt=date(2026, 5, 10), name="D")
        stx_a = self._create_staged_movement(amount=Decimal("20.00"), dt=date(2026, 5, 1), name="A")
        stx_c = self._create_staged_movement(amount=Decimal("30.00"), dt=date(2026, 5, 5), name="C")
        stx_b = self._create_staged_movement(amount=Decimal("40.00"), dt=date(2026, 5, 1), name="B")

        state = self.hydrator.hydrate(company_id=str(self.entity.uuid), session_id="order-session")
        queries = BookkeepingQueries(state)

        residuals1 = queries.residual_unmatched_bank_items()
        residuals2 = queries.residual_unmatched_bank_items()

        # Deterministic stability across calls
        self.assertEqual(residuals1, residuals2)

        # Check ordering matches (date, id)
        pairs = [(item.date, item.id) for item, _ in residuals1]
        self.assertEqual(pairs, sorted(pairs))

        # Check earliest date items come first
        self.assertEqual(residuals1[0][0].date, date(2026, 5, 1))
        self.assertEqual(residuals1[1][0].date, date(2026, 5, 1))
        self.assertEqual(residuals1[2][0].date, date(2026, 5, 5))
        self.assertEqual(residuals1[3][0].date, date(2026, 5, 10))

    def test_g_no_plaid_ids_can_appear(self) -> None:
        """Test G: No plaid: IDs can appear in residual unmatched items."""
        # 1. Create a Plaid movement (upstream staging only)
        self._create_plaid_movement(amount=Decimal("999.00"), dt=date(2026, 5, 1), name="Plaid Ingestion")
        # 2. Create authoritative staged movements
        stx = self._create_staged_movement(amount=Decimal("150.00"), dt=date(2026, 5, 1), name="Authoritative Staged")

        state = self.hydrator.hydrate(company_id=str(self.entity.uuid), session_id="plaid-test-session")
        queries = BookkeepingQueries(state)

        residuals = queries.residual_unmatched_bank_items()
        self.assertTrue(len(residuals) >= 1)

        for item, remaining in residuals:
            self.assertTrue(
                item.id.startswith("staged:"),
                f"Expected authoritative staged ID but got: {item.id}",
            )
            self.assertFalse(
                item.id.startswith("plaid:"),
                f"Plaid ID must NEVER appear in residual items: {item.id}",
            )

    def test_h_state_revision_and_fingerprint_unchanged_after_calling_query(self) -> None:
        """Test H: State revision and state fingerprint are unchanged after calling query."""
        self._create_staged_movement(amount=Decimal("100.00"), dt=date(2026, 5, 1))
        self._create_approved_invoice(amount=Decimal("100.00"), dt=date(2026, 4, 1))
        self._create_cash_tx(amount=Decimal("50.00"), is_debit=True, dt=date(2026, 5, 1))

        state = self.hydrator.hydrate(company_id=str(self.entity.uuid), session_id="fingerprint-test-session")
        queries = BookkeepingQueries(state)

        rev_before = state.revision
        fp_before = state_fingerprint(state)

        # Call the read-only query multiple times
        res1 = queries.residual_unmatched_bank_items()
        res2 = queries.residual_unmatched_bank_items()
        self.assertEqual(res1, res2)

        rev_after = state.revision
        fp_after = state_fingerprint(state)

        self.assertEqual(rev_before, rev_after)
        self.assertEqual(fp_before, fp_after)
