from __future__ import annotations

from datetime import date, datetime, timezone
import pytest

from bookkeeping_state.domain.bank import BankAccount, BankItem
from bookkeeping_state.domain.books import BookItem
from bookkeeping_state.domain.enums import Direction, SourceType
from bookkeeping_state.domain.reconciliations import (
    BankAllocation,
    BookAllocation,
    Reconciliation,
)
from bookkeeping_state.hydration import BookkeepingHydrator
from bookkeeping_state.payment_application import (
    PaymentApplicationBankItemView,
    PaymentApplicationObligationView,
    PaymentApplicationPlan,
    PaymentApplicationProposal,
    PaymentApplicationService,
    PaymentPostingIntent,
    TwoStageSettlementAndReconciliationPlan,
    build_payment_application_view,
    plan_two_stage_bookkeeping,
)
from bookkeeping_state.persistence.repository import BookkeepingSnapshot
from bookkeeping_state.reconciliation.models import (
    FeasibilityStatus,
    ReconciliationCandidate,
    ReconciliationHypothesis,
    ReconciliationPlan,
    ReconciliationResult,
)
from bookkeeping_state.reconciliation.service import ReconciliationService
from bookkeeping_state.reconciliation.validation import validate_candidate_allocations
from bookkeeping_state.reconciliation.view import (
    ReconciliationBookItemView,
    build_reconciliation_view,
)
from bookkeeping_state.state.queries import BookkeepingQueries
from bookkeeping_state.domain.commands import (
    CommandSource,
    CreateReconciliationCommand,
)
from bookkeeping_state.transitions.engine import TransitionEngine
from bookkeeping_state.transitions.result import RejectionCode, TransitionStatus
from bookkeeping_state_eval.tests import factories

from bookkeeping_state_eval.persistence.in_memory import InMemoryBookkeepingRepository
from unittest.mock import MagicMock

FIXED_TIME = datetime(2026, 1, 31, 12, 0, 0, tzinfo=timezone.utc)


def _build_snapshot_environment(
    *,
    bank_accounts: tuple[BankAccount, ...] | None = None,
    bank_items: tuple[BankItem, ...] = (),
    book_items: tuple[BookItem, ...] = (),
    reconciliations: tuple[Reconciliation, ...] = (),
) -> tuple[BookkeepingQueries, TransitionEngine]:
    if bank_accounts is None:
        accounts = (
            factories.account("acc-main", currency="MAD"),
            factories.account("account-1", currency="MAD"),
        )
    else:
        accounts = bank_accounts

    ctx = factories.context()
    snap = BookkeepingSnapshot(
        persistence_revision=1,
        context=ctx,
        bank_accounts=accounts,
        bank_items=bank_items,
        book_items=book_items,
        counterparties=(),
        documents=(),
        reconciliations=reconciliations,
        reconciliation_invalidations=(),
        routing_decisions=(),
        routing_invalidations=(),
        classifications=(),
        classification_invalidations=(),
    )
    repo = InMemoryBookkeepingRepository(initial_snapshots=[snap])
    hydrator = BookkeepingHydrator(
        repository=repo,
        clock=lambda: FIXED_TIME,
        session_id_factory=lambda: "test-session",
    )
    state = hydrator.hydrate(company_id=ctx.company_id, session_id="test-session")
    engine = TransitionEngine(repository=repo)
    return BookkeepingQueries(state), engine


def _build_snapshot_queries(
    *,
    bank_accounts: tuple[BankAccount, ...] | None = None,
    bank_items: tuple[BankItem, ...] = (),
    book_items: tuple[BookItem, ...] = (),
    reconciliations: tuple[Reconciliation, ...] = (),
) -> BookkeepingQueries:
    queries, _ = _build_snapshot_environment(
        bank_accounts=bank_accounts,
        bank_items=bank_items,
        book_items=book_items,
        reconciliations=reconciliations,
    )
    return queries


class TestReconciliationViewSourceTypeFiltering:
    """Cases 1, 2, 3: Reconciliation view strictly admits POSTED_BOOK_ITEM and rejects STAGING."""

    def test_reconciliation_view_excludes_staging_book_item(self) -> None:
        """Case 1: ReconciliationView filters out STAGING_BOOK_ITEM obligations."""
        b_item = factories.bank_item("bank-1", amount=500_000, direction=Direction.BANK_INFLOW)
        staging_item = factories.book_item(
            "invoice-1",
            amount=500_000,
            direction=Direction.BOOK_BANK_DEBIT,
            source_type=SourceType.STAGING_BOOK_ITEM,
        )

        queries = _build_snapshot_queries(
            bank_items=(b_item,),
            book_items=(staging_item,),
        )
        rec_view = build_reconciliation_view(queries)

        assert len(rec_view.bank_items) == 1
        assert len(rec_view.book_items) == 0  # Excluded!

    def test_reconciliation_view_includes_posted_book_item(self) -> None:
        """Case 2: ReconciliationView admits POSTED_BOOK_ITEM cash entries."""
        b_item = factories.bank_item("bank-1", amount=500_000, direction=Direction.BANK_INFLOW)
        posted_item = factories.posted_book_item(
            "tx-1",
            amount=500_000,
            direction=Direction.BOOK_BANK_DEBIT,
        )

        queries = _build_snapshot_queries(
            bank_items=(b_item,),
            book_items=(posted_item,),
        )
        rec_view = build_reconciliation_view(queries)

        assert len(rec_view.bank_items) == 1
        assert len(rec_view.book_items) == 1
        assert rec_view.book_items[0].book_item_id == "tx-1"
        assert rec_view.book_items[0].source_type == SourceType.POSTED_BOOK_ITEM

    def test_reconciliation_book_item_view_model_validator(self) -> None:
        """Case 3: ReconciliationBookItemView validator raises ValueError on STAGING_BOOK_ITEM."""
        with pytest.raises(ValueError, match="requires POSTED_BOOK_ITEM"):
            ReconciliationBookItemView(
                book_item_id="invoice-1",
                source_type=SourceType.STAGING_BOOK_ITEM,
                date=date(2026, 1, 15),
                original_amount_units="500000",
                remaining_amount_units="500000",
                direction=Direction.BOOK_BANK_DEBIT,
                currency="MAD",
            )


class TestReconciliationDefensiveGuards:
    """Cases 4, 5: Defensive guards in transition handler and candidate validation."""

    def test_transition_handler_rejects_staging_book_item(self) -> None:
        """Case 4: CreateReconciliationCommand against STAGING_BOOK_ITEM rejected with INVALID_BOOK_ITEM_SOURCE_TYPE."""
        b_item = factories.bank_item("bank-1", amount=500_000, direction=Direction.BANK_INFLOW)
        staging_item = factories.book_item(
            "invoice-1",
            amount=500_000,
            direction=Direction.BOOK_BANK_DEBIT,
            source_type=SourceType.STAGING_BOOK_ITEM,
        )

        queries, engine = _build_snapshot_environment(
            bank_items=(b_item,),
            book_items=(staging_item,),
        )

        cmd = CreateReconciliationCommand(
            command_id="cmd-1",
            session_id=queries.session_id,
            source=CommandSource.RECONCILIATION,
            expected_state_revision=queries.state_revision,
            reconciliation_id="rec-1",
            bank_allocations=(
                BankAllocation(bank_item_id="bank-1", amount_units="500000"),
            ),
            book_allocations=(
                BookAllocation(book_item_id="invoice-1", amount_units="500000"),
            ),
            issued_at=FIXED_TIME,
        )

        res = engine.apply(state=queries._state, command=cmd)
        assert res.status == TransitionStatus.REJECTED
        assert res.rejection is not None
        assert res.rejection.code == RejectionCode.INVALID_BOOK_ITEM_SOURCE_TYPE
        assert "STAGING_BOOK_ITEM" in res.rejection.message

    def test_candidate_validation_rejects_staging_book_item(self) -> None:
        """Case 5: validate_candidate_allocations returns INVALID_SOURCE_TYPE for non-posted items."""
        b_item = factories.bank_item("bank-1", amount=500_000, direction=Direction.BANK_INFLOW)
        posted_item = factories.posted_book_item("tx-1", amount=500_000, direction=Direction.BOOK_BANK_DEBIT)

        queries = _build_snapshot_queries(
            bank_items=(b_item,),
            book_items=(posted_item,),
        )
        rec_view = build_reconciliation_view(queries)

        bank_allocs = (BankAllocation(bank_item_id="bank-1", amount_units="500000"),)
        book_allocs = (BookAllocation(book_item_id="tx-1", amount_units="500000"),)

        # Valid posted view passes
        val_res = validate_candidate_allocations(rec_view, bank_allocs, book_allocs)
        assert val_res.is_feasible is True

        # When a non-existent item is allocated:
        fake_allocs = (BookAllocation(book_item_id="invoice-unknown", amount_units="500000"),)
        val_res2 = validate_candidate_allocations(rec_view, bank_allocs, fake_allocs)
        assert val_res2.is_feasible is False
        assert val_res2.status == FeasibilityStatus.UNKNOWN_ITEM

        # When a staging item is defensively presented to validation:
        mock_staging_view_item = MagicMock()
        mock_staging_view_item.book_item_id = "staging-1"
        mock_staging_view_item.source_type = SourceType.STAGING_BOOK_ITEM
        mock_view = MagicMock()
        mock_view.get_bank_item.return_value = rec_view.get_bank_item("bank-1")
        mock_view.get_book_item.return_value = mock_staging_view_item

        val_staging = validate_candidate_allocations(
            mock_view,
            bank_allocs,
            (BookAllocation(book_item_id="staging-1", amount_units="500000"),),
        )
        assert val_staging.is_feasible is False
        assert val_staging.status == FeasibilityStatus.INVALID_SOURCE_TYPE


class TestTwoStageSeparationScenarios:
    """Cases 6, 7, 8, 9, 10, 11: End-to-end accounting separation semantics."""

    def test_case_6_partial_payment_with_posted_cash_leg(self) -> None:
        """
        Case 6: Partial payment world:
        - Bank item: 400 (INFLOW)
        - Open invoice: 600 STAGING (BOOK_BANK_DEBIT)
        - Posted tx: 400 POSTED (BOOK_BANK_DEBIT)
        Expected:
        - Stage 2 reconciles Bank 400 <-> Posted tx 400.
        - Posted authority excludes Bank 400 from Stage 1.
        - Stage 1 has no bank items to allocate; open invoice remains 600 STAGING residual.
        """
        b_item = factories.bank_item(
            "bank-400",
            amount=400_000,
            direction=Direction.BANK_INFLOW,
            reference="REF-400",
        )
        inv_item = factories.book_item(
            "invoice-600",
            amount=600_000,
            direction=Direction.BOOK_BANK_DEBIT,
            source_type=SourceType.STAGING_BOOK_ITEM,
            reference="REF-600",
        )
        tx_item = factories.posted_book_item(
            "tx-400",
            amount=400_000,
            direction=Direction.BOOK_BANK_DEBIT,
            reference="REF-400",
        )

        queries = _build_snapshot_queries(
            bank_items=(b_item,),
            book_items=(inv_item, tx_item),
        )

        result = plan_two_stage_bookkeeping(queries)

        # Stage 2 results
        assert result.reconciliation_plan.has_reconciliations is True
        assert len(result.reconciliation_plan.commands) == 1
        cmd = result.reconciliation_plan.commands[0]
        assert cmd.bank_allocations[0].bank_item_id == "bank-400"
        assert cmd.book_allocations[0].book_item_id == "tx-400"

        # Authority gate
        assert result.posted_authority_bank_item_ids == ("bank-400",)

        # Stage 1 results: Bank-400 was excluded by posted authority
        assert result.payment_application_plan.has_proposals is False
        assert result.payment_application_plan.excluded_by_posted_authority_bank_item_ids == ("bank-400",)
        assert "invoice-600" in result.payment_application_plan.unmatched_obligation_ids

    def test_case_7_bank_before_posting_payment_application(self) -> None:
        """
        Case 7: Bank before posting:
        - Bank item: 1000 (INFLOW)
        - Open invoice: 1000 STAGING (BOOK_BANK_DEBIT)
        - No posted cash leg exists.
        Expected:
        - Stage 2 produces 0 reconciliations (posted view is empty).
        - Bank 1000 is NOT claimed by posted authority.
        - Stage 1 generates a PaymentPostingIntent proposal for 1000 against invoice.
        - Pure read-only: no mutations, state revision remains unchanged.
        """
        b_item = factories.bank_item("bank-1000", amount=1_000_000, direction=Direction.BANK_INFLOW)
        inv_item = factories.book_item(
            "invoice-1000",
            amount=1_000_000,
            direction=Direction.BOOK_BANK_DEBIT,
            source_type=SourceType.STAGING_BOOK_ITEM,
        )

        queries = _build_snapshot_queries(
            bank_items=(b_item,),
            book_items=(inv_item,),
        )

        result = plan_two_stage_bookkeeping(queries)

        # Stage 2: 0 reconciliations
        assert result.reconciliation_plan.has_reconciliations is False
        assert result.posted_authority_bank_item_ids == ()

        # Stage 1: proposes PaymentPostingIntent
        pay_plan = result.payment_application_plan
        assert pay_plan.has_proposals is True
        assert len(pay_plan.proposals) == 1
        prop = pay_plan.proposals[0]
        assert prop.bank_item_id == "bank-1000"
        assert prop.intent.total_amount_units == "1000000"
        assert len(prop.intent.obligation_allocations) == 1
        assert prop.intent.obligation_allocations[0].book_item_id == "invoice-1000"

        # Verification of read-only purity
        assert queries.state_revision == 0
        assert queries.derived.bank_remaining("bank-1000") == 1_000_000
        assert queries.derived.book_remaining("invoice-1000") == 1_000_000

    def test_case_8_multiple_posted_payments_independent(self) -> None:
        """Case 8: Multiple posted payments remain independent Stage 2 candidates."""
        b1 = factories.bank_item("bank-100", amount=100_000, direction=Direction.BANK_INFLOW, reference="REF-100")
        b2 = factories.bank_item("bank-200", amount=200_000, direction=Direction.BANK_INFLOW, reference="REF-200")
        tx1 = factories.posted_book_item("tx-100", amount=100_000, direction=Direction.BOOK_BANK_DEBIT, reference="REF-100")
        tx2 = factories.posted_book_item("tx-200", amount=200_000, direction=Direction.BOOK_BANK_DEBIT, reference="REF-200")

        queries = _build_snapshot_queries(
            bank_items=(b1, b2),
            book_items=(tx1, tx2),
        )

        result = plan_two_stage_bookkeeping(queries)

        assert len(result.reconciliation_plan.commands) == 2
        assert set(result.posted_authority_bank_item_ids) == {"bank-100", "bank-200"}
        assert result.payment_application_plan.has_proposals is False

    def test_case_9_one_payment_settling_multiple_invoices_in_stage_1(self) -> None:
        """
        Case 9: One bank payment settles multiple obligations in Stage 1 (1:N grouped settlement).
        - Bank item: 500 INFLOW.
        - Invoices: Inv-1 (200 STAGING), Inv-2 (300 STAGING).
        - No posted transactions.
        Expected: Stage 1 proposes a single PaymentPostingIntent allocating 200 to Inv-1 and 300 to Inv-2.
        """
        b = factories.bank_item("bank-500", amount=500_000, direction=Direction.BANK_INFLOW)
        inv1 = factories.book_item("inv-200", amount=200_000, direction=Direction.BOOK_BANK_DEBIT)
        inv2 = factories.book_item("inv-300", amount=300_000, direction=Direction.BOOK_BANK_DEBIT)

        queries = _build_snapshot_queries(
            bank_items=(b,),
            book_items=(inv1, inv2),
        )

        pay_svc = PaymentApplicationService()
        plan = pay_svc.plan(queries)

        assert plan.has_proposals is True
        assert len(plan.proposals) == 1
        prop = plan.proposals[0]
        assert prop.bank_item_id == "bank-500"
        assert prop.intent.total_amount_units == "500000"

        allocs = {a.book_item_id: a.amount_int for a in prop.intent.obligation_allocations}
        assert allocs == {"inv-200": 200_000, "inv-300": 300_000}

    def test_case_10_supplier_bill_outflow_symmetry(self) -> None:
        """Case 10: Supplier/bill outflow operates symmetrically with inflow/invoices."""
        b = factories.bank_item("bank-outflow", amount=750_000, direction=Direction.BANK_OUTFLOW)
        bill = factories.book_item(
            "bill-750",
            amount=750_000,
            direction=Direction.BOOK_BANK_CREDIT,
            source_type=SourceType.STAGING_BOOK_ITEM,
        )

        queries = _build_snapshot_queries(
            bank_items=(b,),
            book_items=(bill,),
        )

        pay_svc = PaymentApplicationService()
        plan = pay_svc.plan(queries)

        assert plan.has_proposals is True
        assert len(plan.proposals) == 1
        prop = plan.proposals[0]
        assert prop.bank_item_id == "bank-outflow"
        assert prop.intent.direction == Direction.BANK_OUTFLOW
        assert prop.intent.total_amount_units == "750000"
        assert prop.intent.obligation_allocations[0].book_item_id == "bill-750"

    def test_case_11_stage_1_pure_read_only_planning(self) -> None:
        """Case 11: Stage 1 does not mutate state or consume capacity."""
        b = factories.bank_item("bank-1", amount=100_000, direction=Direction.BANK_INFLOW)
        inv = factories.book_item("inv-1", amount=100_000, direction=Direction.BOOK_BANK_DEBIT)

        queries = _build_snapshot_queries(
            bank_items=(b,),
            book_items=(inv,),
        )

        initial_rev = queries.state_revision
        initial_bank_rem = queries.derived.bank_remaining("bank-1")
        initial_inv_rem = queries.derived.book_remaining("inv-1")

        pay_svc = PaymentApplicationService()
        plan = pay_svc.plan(queries)

        assert plan.has_proposals is True
        assert queries.state_revision == initial_rev
        assert queries.derived.bank_remaining("bank-1") == initial_bank_rem
        assert queries.derived.book_remaining("inv-1") == initial_inv_rem


class TestAuthorityGateEdgeCase:
    """
    Case 12: Authority-Gate Edge Case.
    Two bank lines both plausibly match one posted cash transaction.
    Global Stage 2 selects one and leaves the other unselected due to capacity conflict.
    The unselected bank line must NOT automatically fall through to Stage 1 and get applied to an invoice.
    """

    def test_authority_gate_two_competing_bank_lines_one_posted_tx(self) -> None:
        """
        Setup:
        - Posted tx = 400 (POSTED_BOOK_ITEM, BOOK_BANK_DEBIT)
        - Bank A = 400 (BANK_INFLOW)
        - Bank B = 400 (BANK_INFLOW)
        - Open invoice = 400 (STAGING_BOOK_ITEM, BOOK_BANK_DEBIT)

        Both Bank A and Bank B have SELECTABLE Stage-2 hypotheses against the same posted tx.
        Stage 2 optimizer selects one (e.g. Bank A).

        Expected:
        - BOTH Bank A and Bank B are in posted_authority_bank_item_ids because both participated
          in SELECTABLE Stage-2 hypotheses against posted book items.
        - Stage 2 selected_bank_item_ids is ("bank-A",) (or Bank B, whichever CP-SAT picked).
        - BOTH Bank A and Bank B are excluded from Stage 1.
        - Stage 1 plan produces 0 proposals.
        - The losing bank line (Bank B) remains unresolved / held for review; it is NOT converted
          into an invoice payment-application proposal!
        - Open invoice remains completely unallocated.
        """
        bank_a = factories.bank_item(
            "bank-A",
            amount=400_000,
            direction=Direction.BANK_INFLOW,
            date_val=date(2026, 1, 15),
            reference="REF-400",
        )
        bank_b = factories.bank_item(
            "bank-B",
            amount=400_000,
            direction=Direction.BANK_INFLOW,
            date_val=date(2026, 1, 15),
            reference="REF-400",
        )

        posted_tx = factories.posted_book_item(
            "tx-400",
            amount=400_000,
            direction=Direction.BOOK_BANK_DEBIT,
            date_val=date(2026, 1, 15),
            reference="REF-400",
        )

        open_invoice = factories.book_item(
            "invoice-400",
            amount=400_000,
            direction=Direction.BOOK_BANK_DEBIT,
            date_val=date(2026, 1, 15),
            reference="REF-400",
            source_type=SourceType.STAGING_BOOK_ITEM,
        )

        queries = _build_snapshot_queries(
            bank_items=(bank_a, bank_b),
            book_items=(posted_tx, open_invoice),
        )

        result = plan_two_stage_bookkeeping(queries)

        rec_plan = result.reconciliation_plan
        pay_plan = result.payment_application_plan

        # Stage 2 check
        assert rec_plan.has_reconciliations is True
        assert len(rec_plan.selected_bank_item_ids) == 1
        winner_bank_id = rec_plan.selected_bank_item_ids[0]
        loser_bank_id = "bank-B" if winner_bank_id == "bank-A" else "bank-A"

        # Posted Authority check:
        # BOTH bank lines must be in posted_authority_bank_item_ids!
        assert set(result.posted_authority_bank_item_ids) == {"bank-A", "bank-B"}
        assert set(rec_plan.posted_authority_bank_item_ids) == {"bank-A", "bank-B"}

        # Proof that Stage 1 strictly excluded BOTH bank lines
        assert set(pay_plan.excluded_by_posted_authority_bank_item_ids) == {"bank-A", "bank-B"}

        # Crucial invariant: The losing bank line was NOT applied to the open invoice!
        assert pay_plan.has_proposals is False
        assert len(pay_plan.proposals) == 0
        assert "invoice-400" in pay_plan.unmatched_obligation_ids


class TestFullBankConsumptionInvariant:
    """
    Case 13: Stage-1 Full Bank Consumption Invariant.
    A bank item cannot be partially executed in Stage 1 leaving a residual bank balance.
    Every executable Stage-1 payment application must fully consume 100% of the bank item.
    """

    def test_bank_1000_only_obligation_600_produces_no_proposals(self) -> None:
        """
        Bank = 1000, only obligation = 600.
        Must NOT produce an executable PaymentPostingIntent for 600 leaving 400 bank residual.
        """
        b = factories.bank_item("bank-1000", amount=1_000_000, direction=Direction.BANK_INFLOW)
        inv = factories.book_item("inv-600", amount=600_000, direction=Direction.BOOK_BANK_DEBIT)

        queries = _build_snapshot_queries(
            bank_items=(b,),
            book_items=(inv,),
        )

        pay_svc = PaymentApplicationService()
        plan = pay_svc.plan(queries)

        assert plan.has_proposals is False
        assert len(plan.proposals) == 0
        assert "bank-1000" in plan.unmatched_bank_item_ids
        assert "inv-600" in plan.unmatched_obligation_ids

    def test_bank_1000_with_two_obligations_600_and_400_produces_grouped_proposal(self) -> None:
        """
        Bank = 1000, obligations = 600 and 400.
        Produces an executable proposal because the entire 1000 is accounted for.
        """
        b = factories.bank_item("bank-1000", amount=1_000_000, direction=Direction.BANK_INFLOW)
        inv1 = factories.book_item("inv-600", amount=600_000, direction=Direction.BOOK_BANK_DEBIT)
        inv2 = factories.book_item("inv-400", amount=400_000, direction=Direction.BOOK_BANK_DEBIT)

        queries = _build_snapshot_queries(
            bank_items=(b,),
            book_items=(inv1, inv2),
        )

        pay_svc = PaymentApplicationService()
        plan = pay_svc.plan(queries)

        assert plan.has_proposals is True
        assert len(plan.proposals) == 1
        prop = plan.proposals[0]
        assert prop.bank_item_id == "bank-1000"
        assert prop.intent.total_amount_units == "1000000"
        alloc_map = {a.book_item_id: a.amount_int for a in prop.intent.obligation_allocations}
        assert alloc_map == {"inv-600": 600_000, "inv-400": 400_000}

    def test_optimizer_hard_invariant_rejects_candidate_with_bank_residual(self) -> None:
        """
        Even if a candidate allocating only 600 of a 1000 bank item is injected directly,
        the optimizer hard invariant rejects it and produces no proposal.
        """
        from bookkeeping_state.payment_application.models import (
            ObligationAllocation,
            PaymentApplicationCandidate,
        )
        from bookkeeping_state.payment_application.optimizer import optimize_payment_application
        from bookkeeping_state.payment_application.view import build_payment_application_view

        b = factories.bank_item("bank-1000", amount=1_000_000, direction=Direction.BANK_INFLOW)
        inv = factories.book_item("inv-600", amount=600_000, direction=Direction.BOOK_BANK_DEBIT)

        queries = _build_snapshot_queries(
            bank_items=(b,),
            book_items=(inv,),
        )
        view = build_payment_application_view(queries)

        # Injected candidate attempting to consume only 600 of 1000 bank line
        invalid_candidate = PaymentApplicationCandidate(
            candidate_id="cand-partial-bank-invalid",
            bank_item_id="bank-1000",
            obligation_allocations=(
                ObligationAllocation(
                    book_item_id="inv-600",
                    amount_units="600000",
                ),
            ),
            total_amount_units="600000",
            rationale="Attempted partial bank consumption",
        )

        plan = optimize_payment_application(
            view=view,
            candidates=[invalid_candidate],
        )

        # Hard invariant must have filtered out the candidate
        assert plan.has_proposals is False
        assert len(plan.proposals) == 0
        assert "bank-1000" in plan.unmatched_bank_item_ids

