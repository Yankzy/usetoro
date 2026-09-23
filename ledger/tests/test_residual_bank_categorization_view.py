from __future__ import annotations

from datetime import date, datetime, timezone
from decimal import Decimal
from typing import Any
from unittest.mock import patch
import uuid

from django.test import TestCase

from bookkeeping_state.bank_categorization.view import (
    ResidualBankCategorizationItem,
    ResidualBankCategorizationView,
    build_residual_bank_categorization_view,
)
from bookkeeping_state.domain.commands import (
    ApplyPaymentCommand,
    CommandSource,
    CreateReconciliationCommand,
)
from bookkeeping_state.domain.context import AccountingPolicy
from bookkeeping_state.domain.enums import Direction
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


class ResidualBankCategorizationViewTests(Stage1AccountingExecutionTestBase):
    """
    Focused test suite proving that `ResidualBankCategorizationView` correctly
    represents residual unmatched bank movements for semantic categorization:

    A. Untouched residual staged: BankItem appears in the view.
    B. Stage-1-consumed BankItem does not appear.
    C. Stage-2-fully-reconciled BankItem does not appear.
    D. Partially reconciled BankItem exposes only residual units.
    E. Direction/date/currency/description/reference are copied exactly from authoritative state.
    F. Ordering matches `residual_unmatched_bank_items()`.
    G. No plaid: IDs can appear (both via hydration and schema validation).
    H. No truth/eval/simulator fields exist in the view model.
    I. Building the view leaves state revision and fingerprint unchanged.
    """

    def test_a_untouched_residual_staged_bank_item_appears_in_view(self) -> None:
        """Test A: Untouched residual staged BankItem appears in the view with full amount."""
        stx = self._create_staged_movement(amount=Decimal("150.00"), dt=date(2026, 5, 1))
        state = self.hydrator.hydrate(company_id=str(self.entity.uuid), session_id="view-test-a")
        queries = BookkeepingQueries(state)

        view = build_residual_bank_categorization_view(queries)

        self.assertEqual(len(view.items), 1)
        self.assertEqual(view.item_count, 1)


        item = view.items[0]
        expected_id = f"staged:{stx.uuid}"
        self.assertEqual(item.bank_item_id, expected_id)
        self.assertEqual(item.residual_amount_units, 1_500_000)
        self.assertEqual(item.original_amount_units, 1_500_000)
        self.assertEqual(view.get_item(expected_id), item)

    def test_b_stage1_consumed_bank_item_does_not_appear(self) -> None:
        """Test B: Stage-1-consumed BankItem does not appear in the view."""
        stx_consumed = self._create_staged_movement(amount=Decimal("200.00"), dt=date(2026, 5, 1))
        self._create_approved_invoice(amount=Decimal("200.00"), dt=date(2026, 4, 15))
        stx_untouched = self._create_staged_movement(amount=Decimal("100.00"), dt=date(2026, 5, 2))

        state1 = self.hydrator.hydrate(company_id=str(self.entity.uuid), session_id="view-test-b-1")
        queries1 = BookkeepingQueries(state1)
        plan = plan_two_stage_bookkeeping(queries1)

        consumed_id = f"staged:{stx_consumed.uuid}"
        untouched_id = f"staged:{stx_untouched.uuid}"

        proposal = plan.payment_application_plan.proposals[0]
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
        self.engine.apply(state=state1, command=cmd)

        state2 = self.hydrator.hydrate(company_id=str(self.entity.uuid), session_id="view-test-b-2")
        queries2 = BookkeepingQueries(state2)

        view = build_residual_bank_categorization_view(queries2)
        view_ids = [item.bank_item_id for item in view.items]

        self.assertNotIn(consumed_id, view_ids)
        self.assertIn(untouched_id, view_ids)
        self.assertEqual(len(view.items), 1)

    def test_c_stage2_fully_reconciled_bank_item_does_not_appear(self) -> None:
        """Test C: Stage-2-fully-reconciled BankItem does not appear in the view."""
        stx_recon = self._create_staged_movement(amount=Decimal("300.00"), dt=date(2026, 5, 1))
        tx_cash = self._create_cash_tx(amount=Decimal("300.00"), is_debit=True, dt=date(2026, 5, 1))
        stx_untouched = self._create_staged_movement(amount=Decimal("75.00"), dt=date(2026, 5, 2))

        state = self.hydrator.hydrate(company_id=str(self.entity.uuid), session_id="view-test-c")

        recon_bank_id = f"staged:{stx_recon.uuid}"
        cash_book_id = f"tx:{tx_cash.uuid}"
        untouched_id = f"staged:{stx_untouched.uuid}"

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
        self.engine.apply(state=state, command=rec_cmd)

        fresh_state = self.hydrator.hydrate(company_id=str(self.entity.uuid), session_id="view-test-c-fresh")
        fresh_queries = BookkeepingQueries(fresh_state)

        view = build_residual_bank_categorization_view(fresh_queries)
        view_ids = [item.bank_item_id for item in view.items]

        self.assertNotIn(recon_bank_id, view_ids)
        self.assertIn(untouched_id, view_ids)
        self.assertEqual(len(view.items), 1)

    def test_d_partially_reconciled_bank_item_exposes_only_residual_units(self) -> None:
        """Test D: Partially reconciled BankItem exposes only residual amount units."""
        def _policy_with_partial_bank(*args: Any, **kwargs: Any) -> AccountingPolicy:
            kwargs["allow_partial_bank_reconciliation"] = True
            return AccountingPolicy(*args, **kwargs)

        with patch("bookkeeping_state.persistence.reader.AccountingPolicy", side_effect=_policy_with_partial_bank):
            stx_partial = self._create_staged_movement(amount=Decimal("500.00"), dt=date(2026, 5, 1))
            tx_cash = self._create_cash_tx(amount=Decimal("200.00"), is_debit=True, dt=date(2026, 5, 1))

            state = self.hydrator.hydrate(company_id=str(self.entity.uuid), session_id="view-test-d")
            bank_id = f"staged:{stx_partial.uuid}"
            cash_id = f"tx:{tx_cash.uuid}"

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

            fresh_state = self.hydrator.hydrate(company_id=str(self.entity.uuid), session_id="view-test-d-fresh")
            fresh_queries = BookkeepingQueries(fresh_state)

            view = build_residual_bank_categorization_view(fresh_queries)
            self.assertEqual(len(view.items), 1)

            item = view.items[0]
            self.assertEqual(item.bank_item_id, bank_id)
            self.assertEqual(item.residual_amount_units, 3_000_000)
            self.assertEqual(item.original_amount_units, 5_000_000)
            self.assertEqual(item.amount_units, 3_000_000)
            self.assertLess(item.residual_amount_units, item.original_amount_units)

    def test_e_metadata_fields_copied_exactly_from_authoritative_state(self) -> None:
        """Test E: direction, date, currency, description, reference, and account context copied exactly."""
        stx = self._create_staged_movement(
            amount=Decimal("250.75"),
            dt=date(2026, 6, 15),
            name="Wire Transfer - Client Retainer",
        )
        stx.fit_id = "WIRE-FIT-7788"
        stx.save(update_fields=["fit_id"])

        state = self.hydrator.hydrate(company_id=str(self.entity.uuid), session_id="view-test-e")
        queries = BookkeepingQueries(state)

        view = build_residual_bank_categorization_view(queries)
        self.assertEqual(len(view.items), 1)

        item = view.items[0]
        self.assertEqual(item.bank_item_id, f"staged:{stx.uuid}")
        self.assertEqual(item.bank_account_id, str(self.bank_account.uuid))
        self.assertEqual(item.bank_account_name, self.bank_account.name)
        self.assertEqual(item.date, date(2026, 6, 15))
        self.assertEqual(item.currency, "USD")
        self.assertEqual(item.direction, Direction.BANK_INFLOW)
        self.assertEqual(item.description, "Wire Transfer - Client Retainer")
        self.assertEqual(item.reference, "WIRE-FIT-7788")
        self.assertEqual(item.provenance_refs, (str(stx.uuid),))

    def test_f_ordering_matches_residual_unmatched_bank_items(self) -> None:
        """Test F: Item ordering strictly matches queries.residual_unmatched_bank_items()."""
        stx_d = self._create_staged_movement(amount=Decimal("10.00"), dt=date(2026, 5, 10), name="D")
        stx_a = self._create_staged_movement(amount=Decimal("20.00"), dt=date(2026, 5, 1), name="A")
        stx_c = self._create_staged_movement(amount=Decimal("30.00"), dt=date(2026, 5, 5), name="C")
        stx_b = self._create_staged_movement(amount=Decimal("40.00"), dt=date(2026, 5, 1), name="B")

        state = self.hydrator.hydrate(company_id=str(self.entity.uuid), session_id="view-order-session")
        queries = BookkeepingQueries(state)

        expected_query_pairs = queries.residual_unmatched_bank_items()
        view = build_residual_bank_categorization_view(queries)

        self.assertEqual(
            [item.bank_item_id for item in view.items],
            [pair[0].id for pair in expected_query_pairs],
        )

        dates = [item.date for item in view.items]
        self.assertEqual(dates, sorted(dates))

    def test_g_no_plaid_ids_can_appear(self) -> None:
        """Test G: No plaid: IDs can appear in the view, and validator rejects non-staged IDs."""
        self._create_plaid_movement(amount=Decimal("999.00"), dt=date(2026, 5, 1), name="Plaid Movement")
        stx = self._create_staged_movement(amount=Decimal("125.00"), dt=date(2026, 5, 1), name="Staged")

        state = self.hydrator.hydrate(company_id=str(self.entity.uuid), session_id="view-plaid-session")
        queries = BookkeepingQueries(state)

        view = build_residual_bank_categorization_view(queries)
        self.assertTrue(len(view.items) >= 1)

        for item in view.items:
            self.assertTrue(item.bank_item_id.startswith("staged:"))
            self.assertFalse(item.bank_item_id.startswith("plaid:"))

        # Also verify that ResidualBankCategorizationItem model validation strictly forbids plaid: IDs
        with self.assertRaises(ValueError) as ctx:
            ResidualBankCategorizationItem(
                bank_item_id=f"plaid:{uuid.uuid4()}",
                bank_account_id="ba-1",
                residual_amount_units=1000000,
                original_amount_units=1000000,
                direction=Direction.BANK_INFLOW,
                date=date(2026, 5, 1),
                currency="USD",
                description="Illegal Plaid Movement",
            )
        self.assertIn("staged:<uuid>", str(ctx.exception))

    def test_h_no_truth_eval_simulator_fields_exist_in_view_model(self) -> None:
        """Test H: View models do not include any truth/eval/simulator/target fields."""
        forbidden_substrings = [
            "truth",
            "eval",
            "simulator",
            "scenario",
            "target",
            "label",
            "account_code",
            "synthetic",
        ]

        for model_cls in (ResidualBankCategorizationItem, ResidualBankCategorizationView):
            field_names = list(model_cls.model_fields.keys())
            for field_name in field_names:
                for forbidden in forbidden_substrings:
                    self.assertNotIn(
                        forbidden,
                        field_name.lower(),
                        f"Forbidden substring {forbidden!r} found in {model_cls.__name__}.{field_name}",
                    )

    def test_i_building_view_leaves_state_revision_and_fingerprint_unchanged(self) -> None:
        """Test I: Building the view leaves state revision and state fingerprint unchanged."""
        self._create_staged_movement(amount=Decimal("100.00"), dt=date(2026, 5, 1))
        self._create_approved_invoice(amount=Decimal("100.00"), dt=date(2026, 4, 1))
        self._create_cash_tx(amount=Decimal("50.00"), is_debit=True, dt=date(2026, 5, 1))

        state = self.hydrator.hydrate(company_id=str(self.entity.uuid), session_id="view-fp-session")
        queries = BookkeepingQueries(state)

        rev_before = state.revision
        fp_before = state_fingerprint(state)

        view1 = build_residual_bank_categorization_view(queries)
        view2 = build_residual_bank_categorization_view(queries)

        self.assertEqual(len(view1.items), len(view2.items))

        rev_after = state.revision
        fp_after = state_fingerprint(state)

        self.assertEqual(rev_before, rev_after)
        self.assertEqual(fp_before, fp_after)
