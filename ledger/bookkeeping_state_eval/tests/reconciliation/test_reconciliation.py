from __future__ import annotations

from datetime import date, datetime, timezone
import pytest
from pydantic import ValidationError

from bookkeeping_state.domain.bank import BankAccount, BankItem
from bookkeeping_state.domain.books import BookItem
from bookkeeping_state.domain.commands import (
    CommandSource,
    CreateReconciliationCommand,
    InvalidateReconciliationCommand,
)
from bookkeeping_state.domain.context import (
    AccountingPolicy,
    BookkeepingContext,
)
from bookkeeping_state.domain.counterparties import (
    Counterparty,
    CounterpartyType,
)
from bookkeeping_state.domain.enums import (
    Direction,
    Eligibility,
    SemanticAdmissibility,
    SourceType,
)
from bookkeeping_state.reconciliation.candidate_generation import (
    score_reconciliation_pair,
)
from bookkeeping_state.domain.hypotheses import ReconciliationHypothesis
from bookkeeping_state.domain.reconciliations import (
    BankAllocation,
    BookAllocation,
    Reconciliation,
    ReconciliationHypothesisProvenance,
)
from bookkeeping_state.hydration.hydrator import (
    BookkeepingHydrator,
    InvalidHydratedStateError,
)
from bookkeeping_state_eval.persistence.in_memory import (
    InMemoryBookkeepingRepository,
)
from bookkeeping_state.persistence.repository import BookkeepingSnapshot
from bookkeeping_state.reconciliation import (
    CandidateGenerator,
    CandidateType,
    DefaultReconciliationScorer,
    FeasibilityStatus,
    ReconciliationCandidate,
    ReconciliationPlan,
    ReconciliationResult,
    ReconciliationService,
    ReconciliationView,
    ReconciliationViewConfig,
    build_reconciliation_view,
    optimize_reconciliation,
    reconciliation_plan_to_transition_batch,
    validate_candidate_allocations,
)
from bookkeeping_state.state.derived import build_derived_state
from bookkeeping_state.state.fingerprint import (
    artifact_fingerprint,
    state_fingerprint,
)
from bookkeeping_state.state.queries import BookkeepingQueries
from bookkeeping_state.state.validation import (
    ValidationCode,
    validate_state,
)
from bookkeeping_state.transitions.batch import TransitionBatch
from bookkeeping_state.transitions.engine import TransitionEngine
from bookkeeping_state.transitions.result import (
    RejectionCode,
    TransitionStatus,
)
from tests import factories

FIXED_TIME = datetime(2026, 1, 31, 12, 0, 0, tzinfo=timezone.utc)


def _build_test_environment(
    *,
    bank_accounts: tuple[BankAccount, ...] | None = None,
    bank_items: tuple[BankItem, ...] = (),
    book_items: tuple[BookItem, ...] = (),
    counterparties: tuple[Counterparty, ...] = (),
    reconciliations: tuple[Reconciliation, ...] = (),
    routing_decisions=(),
    documents: tuple = (),
    policy_updates: dict | None = None,
    default_posted: bool = True,
):
    if bank_accounts is None:
        acc_main = factories.account("acc-main", currency="MAD")
        acc_1 = factories.account("account-1", currency="MAD")
        accounts = (acc_main, acc_1)
    else:
        accounts = bank_accounts

    ctx = factories.context(**(policy_updates or {}))

    # In Stage-2 bank reconciliation tests, book items represent posted bank ledger entries
    # unless default_posted=False is explicitly specified (e.g. to test staging rejection).
    if default_posted:
        resolved_book_items = tuple(
            b.model_copy(update={"source_type": SourceType.POSTED_BOOK_ITEM})
            if b.source_type == SourceType.STAGING_BOOK_ITEM
            else b
            for b in book_items
        )
    else:
        resolved_book_items = book_items

    snap = BookkeepingSnapshot(
        persistence_revision=1,
        context=ctx,
        bank_accounts=accounts,
        bank_items=bank_items,
        book_items=resolved_book_items,
        counterparties=counterparties,
        documents=documents,
        reconciliations=reconciliations,
        routing_decisions=routing_decisions,
    )

    repo = InMemoryBookkeepingRepository(initial_snapshots=[snap])
    hydrator = BookkeepingHydrator(
        repository=repo,
        clock=lambda: FIXED_TIME,
        session_id_factory=lambda: "test-rec-session",
    )
    state = hydrator.hydrate(company_id=ctx.company_id, session_id="test-rec-session")
    engine = TransitionEngine(repository=repo)
    return repo, hydrator, state, engine


# ======================================================================
# 1. Bounded ReconciliationView & Immutability
# ======================================================================


def test_bounded_reconciliation_view_and_immutability():
    b1 = factories.bank_item("b1", account_id="acc-main", amount=1_000_000)
    j1 = factories.book_item("j1", amount=1_000_000)
    _, _, state, _ = _build_test_environment(bank_items=(b1,), book_items=(j1,))

    queries = BookkeepingQueries(state)
    view = build_reconciliation_view(queries)

    assert isinstance(view, ReconciliationView)
    assert view.state_revision == 0
    assert len(view.bank_items) == 1
    assert len(view.book_items) == 1
    assert view.bank_items[0].bank_item_id == "b1"
    assert view.book_items[0].book_item_id == "j1"

    # Verify immutability: models are frozen
    with pytest.raises(Exception):
        view.bank_items[0].remaining_amount_units = "500"  # type: ignore


# ======================================================================
# 2. Remaining amounts used correctly & fully consumed items excluded
# ======================================================================


def test_remaining_amounts_used_correctly_and_fully_consumed_excluded():
    b1 = factories.bank_item("b1", account_id="acc-main", amount=1_000_000)
    b2 = factories.bank_item("b2", account_id="acc-main", amount=500_000)
    j1 = factories.book_item("j1", amount=1_000_000)
    j2 = factories.book_item("j2", amount=500_000)

    # Active reconciliation consumes all of b2 and j2, and 400_000 of b1 and j1
    r1 = factories.reconciliation(
        "r1",
        bank=(("b2", 500_000),),
        book=(("j2", 500_000),),
    )
    r2 = factories.reconciliation(
        "r2",
        bank=(("b1", 400_000),),
        book=(("j1", 400_000),),
    )

    _, _, state, _ = _build_test_environment(
        bank_items=(b1, b2),
        book_items=(j1, j2),
        reconciliations=(r1, r2),
        policy_updates={"allow_partial_bank_reconciliation": True},
    )

    queries = BookkeepingQueries(state)
    view = build_reconciliation_view(queries)

    # b2 and j2 are fully consumed -> MUST NOT appear in view
    assert view.get_bank_item("b2") is None
    assert view.get_book_item("j2") is None

    # b1 and j1 have remaining amounts derived from active reconciliations
    b1_view = view.get_bank_item("b1")
    j1_view = view.get_book_item("j1")

    assert b1_view is not None
    assert b1_view.remaining_amount_int == 600_000
    assert b1_view.original_amount_int == 1_000_000

    assert j1_view is not None
    assert j1_view.remaining_amount_int == 600_000
    assert j1_view.original_amount_int == 1_000_000


# ======================================================================
# 3. Deterministic candidate generation & exact money matching
# ======================================================================


def test_deterministic_candidate_generation_and_exact_money_matching():
    b1 = factories.bank_item("b1", account_id="acc-main", amount=1_000_000)
    j1 = factories.book_item("j1", amount=1_000_000)
    b2 = factories.bank_item("b2", account_id="acc-main", amount=300_000)
    j2 = factories.book_item("j2", amount=800_000)

    _, _, state, _ = _build_test_environment(
        bank_items=(b1, b2),
        book_items=(j1, j2),
    )

    queries = BookkeepingQueries(state)
    view = build_reconciliation_view(queries)
    generator = CandidateGenerator()
    candidates = generator.generate_candidates(view)

    # Must contain exact 1:1 match for b1 <-> j1
    exact_matches = [
        c for c in candidates if c.candidate_type == CandidateType.ONE_TO_ONE_EXACT
    ]
    assert len(exact_matches) >= 1
    exact = exact_matches[0]
    assert exact.total_amount_int == 1_000_000
    assert exact.bank_allocations[0].bank_item_id == "b1"
    assert exact.book_allocations[0].book_item_id == "j1"


# ======================================================================
# 4. Partial book reconciliation
# ======================================================================


def test_partial_book_reconciliation():
    b1 = factories.bank_item("b1", account_id="acc-main", amount=400_000)
    j1 = factories.book_item("j1", amount=1_000_000)

    _, _, state, _ = _build_test_environment(
        bank_items=(b1,),
        book_items=(j1,),
        policy_updates={"allow_partial_book_reconciliation": True},
    )

    queries = BookkeepingQueries(state)
    view = build_reconciliation_view(queries)
    generator = CandidateGenerator()
    candidates = generator.generate_candidates(view)

    partial_matches = [
        c
        for c in candidates
        if c.candidate_type == CandidateType.ONE_TO_ONE_PARTIAL_BOOK
    ]
    assert len(partial_matches) == 1
    cand = partial_matches[0]
    assert cand.total_amount_int == 400_000
    assert cand.bank_allocations[0].amount_int == 400_000
    assert cand.book_allocations[0].amount_int == 400_000


# ======================================================================
# 5. Partial bank policy enforcement
# ======================================================================


def test_partial_bank_policy_forbidden_and_allowed():
    b1 = factories.bank_item("b1", account_id="acc-main", amount=1_000_000)
    j1 = factories.book_item("j1", amount=400_000)

    # Case A: Forbidden by default policy
    _, _, state_a, _ = _build_test_environment(
        bank_items=(b1,),
        book_items=(j1,),
        policy_updates={"allow_partial_bank_reconciliation": False},
    )
    view_a = build_reconciliation_view(BookkeepingQueries(state_a))

    # Allocation of only 400_000 from 1_000_000 bank item
    bank_alloc = (BankAllocation(bank_item_id="b1", amount_units="400000"),)
    book_alloc = (BookAllocation(book_item_id="j1", amount_units="400000"),)
    res_a = validate_candidate_allocations(view_a, bank_alloc, book_alloc)
    assert res_a.status == FeasibilityStatus.PARTIAL_BANK_FORBIDDEN

    # Case B: Allowed by policy
    _, _, state_b, _ = _build_test_environment(
        bank_items=(b1,),
        book_items=(j1,),
        policy_updates={"allow_partial_bank_reconciliation": True},
    )
    view_b = build_reconciliation_view(BookkeepingQueries(state_b))
    res_b = validate_candidate_allocations(view_b, bank_alloc, book_alloc)
    assert res_b.is_feasible


# ======================================================================
# 6. Currency mismatch rejection
# ======================================================================


def test_currency_mismatch_rejection():
    acc_usd = factories.account("acc-usd", currency="USD")
    b1 = factories.bank_item("b1", account_id="acc-usd", amount=1_000_000, currency="USD")
    j1 = factories.book_item("j1", amount=1_000_000, currency="EUR")

    _, _, state, _ = _build_test_environment(
        bank_accounts=(acc_usd,),
        bank_items=(b1,),
        book_items=(j1,),
        policy_updates={"require_exact_currency_match": True},
    )
    view = build_reconciliation_view(BookkeepingQueries(state))

    bank_alloc = (BankAllocation(bank_item_id="b1", amount_units="1000000"),)
    book_alloc = (BookAllocation(book_item_id="j1", amount_units="1000000"),)
    res = validate_candidate_allocations(view, bank_alloc, book_alloc)
    assert res.status == FeasibilityStatus.CURRENCY_MISMATCH


# ======================================================================
# 7. Direction mismatch rejection
# ======================================================================


def test_direction_mismatch_rejection():
    # Incompatible accounting directions: BANK_OUTFLOW is compatible with BOOK_BANK_CREDIT, NOT BOOK_BANK_DEBIT
    b1 = factories.bank_item("b1", account_id="acc-main", direction=Direction.BANK_OUTFLOW)
    j1 = factories.book_item("j1", direction=Direction.BOOK_BANK_DEBIT)

    _, _, state, _ = _build_test_environment(bank_items=(b1,), book_items=(j1,))
    view = build_reconciliation_view(BookkeepingQueries(state))

    bank_alloc = (BankAllocation(bank_item_id="b1", amount_units="1000000"),)
    book_alloc = (BookAllocation(book_item_id="j1", amount_units="1000000"),)
    res = validate_candidate_allocations(view, bank_alloc, book_alloc)
    assert res.status == FeasibilityStatus.DIRECTION_MISMATCH


# ======================================================================
# 8. Routing contradiction rejection
# ======================================================================


def test_routing_contradiction_rejection():
    # Account 1 & Account 2
    acc2 = factories.account("acc-other", currency="MAD")
    b_other = factories.bank_item("b-other", account_id="acc-other", amount=1_000_000)
    j1 = factories.posted_book_item("j1", amount=1_000_000)

    # J1 is actively routed to acc-main
    r_decision = factories.routing("r-j1", book_item_id="j1", account_id="acc-main")

    repo = InMemoryBookkeepingRepository(initial_snapshots=[
        BookkeepingSnapshot(
            persistence_revision=1,
            context=factories.context(),
            bank_accounts=(factories.account("acc-main"), acc2),
            bank_items=(b_other,),
            book_items=(j1,),
            routing_decisions=(r_decision,),
        )
    ])
    hydrator = BookkeepingHydrator(repository=repo, clock=lambda: FIXED_TIME)
    state = hydrator.hydrate(company_id="atlas", session_id="test")

    view = build_reconciliation_view(BookkeepingQueries(state))
    assert view.get_book_item("j1").routed_bank_account_id == "acc-main"

    # Candidate attempting to reconcile j1 with b-other from acc-other must be rejected
    bank_alloc = (BankAllocation(bank_item_id="b-other", amount_units="1000000"),)
    book_alloc = (BookAllocation(book_item_id="j1", amount_units="1000000"),)
    res = validate_candidate_allocations(view, bank_alloc, book_alloc)
    assert res.status == FeasibilityStatus.ROUTING_CONTRADICTION


# ======================================================================
# 9. Date-window filtering
# ======================================================================


def test_date_window_filtering():
    b1 = BankItem(
        id="b1",
        bank_account_id="acc-main",
        date=date(2026, 1, 1),
        amount_units="1000000",
        direction=Direction.BANK_OUTFLOW,
        currency="MAD",
        description="b1",
    )
    j1 = BookItem(
        id="j1",
        origin_period="2026-01",
        date=date(2026, 1, 25),  # 24 days distance
        amount_units="1000000",
        direction=Direction.BOOK_BANK_CREDIT,
        currency="MAD",
        description="j1",
    )

    _, _, state, _ = _build_test_environment(bank_items=(b1,), book_items=(j1,))
    view = build_reconciliation_view(
        BookkeepingQueries(state),
        config=ReconciliationViewConfig(max_date_distance_days=7),
    )

    bank_alloc = (BankAllocation(bank_item_id="b1", amount_units="1000000"),)
    book_alloc = (BookAllocation(book_item_id="j1", amount_units="1000000"),)
    res = validate_candidate_allocations(view, bank_alloc, book_alloc)
    assert res.status == FeasibilityStatus.DATE_WINDOW_EXCEEDED


# ======================================================================
# 10. Runtime hypothesis state_revision & stale hypothesis rejection
# ======================================================================


def test_runtime_hypothesis_state_revision_and_stale_rejection():
    b1 = factories.bank_item("b1", account_id="acc-main", amount=1_000_000)
    j1 = factories.book_item("j1", amount=1_000_000)
    _, _, state, _ = _build_test_environment(bank_items=(b1,), book_items=(j1,))

    view = build_reconciliation_view(BookkeepingQueries(state))
    assert view.state_revision == 0

    hyp = ReconciliationHypothesis(
        id="hyp-stale",
        eligibility=Eligibility.SELECTABLE,
        utility=900,
        bank_allocations=(BankAllocation(bank_item_id="b1", amount_units="1000000"),),
        book_allocations=(BookAllocation(book_item_id="j1", amount_units="1000000"),),
        state_revision=999,  # Stale revision
        generated_at=FIXED_TIME,
    )

    result = optimize_reconciliation(
        view=view,
        hypotheses=(hyp,),
        solver_run_id="test-run",
    )
    # Stale hypothesis must be ignored by optimizer
    assert len(result.selected_hypotheses) == 0
    assert len(result.unresolved_bank_items) == 1


# ======================================================================
# 11. Counterfactual/non-selectable hypothesis rejection
# ======================================================================


def test_counterfactual_and_non_selectable_hypothesis_rejection():
    b1 = factories.bank_item("b1", account_id="acc-main", amount=1_000_000)
    j1 = factories.book_item("j1", amount=1_000_000)
    _, _, state, _ = _build_test_environment(bank_items=(b1,), book_items=(j1,))
    view = build_reconciliation_view(BookkeepingQueries(state))

    hyp = ReconciliationHypothesis(
        id="hyp-counterfactual",
        eligibility=Eligibility.COUNTERFACTUAL_ONLY,
        utility=1000,
        bank_allocations=(BankAllocation(bank_item_id="b1", amount_units="1000000"),),
        book_allocations=(BookAllocation(book_item_id="j1", amount_units="1000000"),),
        state_revision=view.state_revision,
        generated_at=FIXED_TIME,
    )

    result = optimize_reconciliation(
        view=view,
        hypotheses=(hyp,),
        solver_run_id="test-run",
    )
    assert len(result.selected_hypotheses) == 0


# ======================================================================
# 12. Semantic score cannot override hard feasibility
# ======================================================================


def test_semantic_score_cannot_override_hard_feasibility():
    acc_usd = factories.account("acc-usd", currency="USD")
    b1 = factories.bank_item("b1", account_id="acc-usd", currency="USD", amount=1_000_000)
    j1 = factories.book_item("j1", currency="EUR", amount=1_000_000)
    _, _, state, _ = _build_test_environment(
        bank_accounts=(acc_usd,),
        bank_items=(b1,),
        book_items=(j1,),
    )
    view = build_reconciliation_view(BookkeepingQueries(state))

    # Even with utility=1000, hard feasibility failure prevents selection
    hyp = ReconciliationHypothesis(
        id="hyp-invalid-currency",
        eligibility=Eligibility.SELECTABLE,
        utility=1000,
        bank_allocations=(BankAllocation(bank_item_id="b1", amount_units="1000000"),),
        book_allocations=(BookAllocation(book_item_id="j1", amount_units="1000000"),),
        state_revision=view.state_revision,
        generated_at=FIXED_TIME,
    )

    result = optimize_reconciliation(
        view=view,
        hypotheses=(hyp,),
        solver_run_id="test-run",
    )
    assert len(result.selected_hypotheses) == 0


# ======================================================================
# 13. Global BankItem exclusivity & BookItem capacity
# ======================================================================


def test_global_bank_item_exclusivity_and_book_item_capacity():
    b1 = factories.bank_item("b1", account_id="acc-main", amount=1_000_000)
    j1 = factories.book_item("j1", amount=1_000_000)
    j2 = factories.book_item("j2", amount=1_000_000)
    _, _, state, _ = _build_test_environment(
        bank_items=(b1,),
        book_items=(j1, j2),
    )
    view = build_reconciliation_view(BookkeepingQueries(state))

    # Two competing hypotheses for b1
    h1 = ReconciliationHypothesis(
        id="hyp-1",
        eligibility=Eligibility.SELECTABLE,
        utility=500,
        bank_allocations=(BankAllocation(bank_item_id="b1", amount_units="1000000"),),
        book_allocations=(BookAllocation(book_item_id="j1", amount_units="1000000"),),
        state_revision=view.state_revision,
        generated_at=FIXED_TIME,
    )
    h2 = ReconciliationHypothesis(
        id="hyp-2",
        eligibility=Eligibility.SELECTABLE,
        utility=600,
        bank_allocations=(BankAllocation(bank_item_id="b1", amount_units="1000000"),),
        book_allocations=(BookAllocation(book_item_id="j2", amount_units="1000000"),),
        state_revision=view.state_revision,
        generated_at=FIXED_TIME,
    )

    result = optimize_reconciliation(
        view=view,
        hypotheses=(h1, h2),
        solver_run_id="test-run",
    )
    # Exclusivity: exactly one can be chosen, never both
    assert len(result.selected_hypotheses) == 1
    assert result.selected_hypotheses[0].id == "hyp-2"  # chosen due to higher utility


# ======================================================================
# 14. Optimizer maximizes money before semantic utility
# ======================================================================


def test_optimizer_maximizes_money_before_semantic_utility():
    # b_big = 1_000_000, b_small = 400_000
    # j1 = 1_000_000, j2 = 400_000
    b_big = factories.bank_item("b_big", amount=1_000_000)
    b_small = factories.bank_item("b_small", amount=400_000)
    j_shared = factories.book_item("j_shared", amount=1_000_000)

    _, _, state, _ = _build_test_environment(
        bank_items=(b_big, b_small),
        book_items=(j_shared,),
    )
    view = build_reconciliation_view(BookkeepingQueries(state))

    # H_big reconciles 1_000_000 with low utility (100)
    h_big = ReconciliationHypothesis(
        id="hyp-big",
        eligibility=Eligibility.SELECTABLE,
        utility=100,
        bank_allocations=(BankAllocation(bank_item_id="b_big", amount_units="1000000"),),
        book_allocations=(BookAllocation(book_item_id="j_shared", amount_units="1000000"),),
        state_revision=view.state_revision,
        generated_at=FIXED_TIME,
    )

    # H_small reconciles 400_000 with high utility (1000)
    h_small = ReconciliationHypothesis(
        id="hyp-small",
        eligibility=Eligibility.SELECTABLE,
        utility=1000,
        bank_allocations=(BankAllocation(bank_item_id="b_small", amount_units="400000"),),
        book_allocations=(BookAllocation(book_item_id="j_shared", amount_units="400000"),),
        state_revision=view.state_revision,
        generated_at=FIXED_TIME,
    )

    result = optimize_reconciliation(
        view=view,
        hypotheses=(h_big, h_small),
        solver_run_id="test-run",
    )
    # Big money wins over high utility
    assert len(result.selected_hypotheses) == 1
    assert result.selected_hypotheses[0].id == "hyp-big"
    assert result.reconciled_units_int == 1_000_000


# ======================================================================
# 15. Equal-money semantic tie-break & deterministic result
# ======================================================================


def test_equal_money_semantic_tie_break_and_deterministic_result():
    b1 = factories.bank_item("b1", amount=1_000_000)
    j1 = factories.book_item("j1", amount=1_000_000)
    j2 = factories.book_item("j2", amount=1_000_000)
    _, _, state, _ = _build_test_environment(
        bank_items=(b1,),
        book_items=(j1, j2),
    )
    view = build_reconciliation_view(BookkeepingQueries(state))

    h1 = ReconciliationHypothesis(
        id="hyp-1",
        eligibility=Eligibility.SELECTABLE,
        utility=400,
        bank_allocations=(BankAllocation(bank_item_id="b1", amount_units="1000000"),),
        book_allocations=(BookAllocation(book_item_id="j1", amount_units="1000000"),),
        state_revision=view.state_revision,
        generated_at=FIXED_TIME,
    )
    h2 = ReconciliationHypothesis(
        id="hyp-2",
        eligibility=Eligibility.SELECTABLE,
        utility=800,
        bank_allocations=(BankAllocation(bank_item_id="b1", amount_units="1000000"),),
        book_allocations=(BookAllocation(book_item_id="j2", amount_units="1000000"),),
        state_revision=view.state_revision,
        generated_at=FIXED_TIME,
    )

    # Tie-break selects hyp-2 due to higher utility
    res1 = optimize_reconciliation(view=view, hypotheses=(h1, h2), solver_run_id="run-1")
    res2 = optimize_reconciliation(view=view, hypotheses=(h2, h1), solver_run_id="run-2")

    assert res1.selected_hypotheses[0].id == "hyp-2"
    assert res2.selected_hypotheses[0].id == "hyp-2"


# ======================================================================
# 16. Plan generation does not mutate state
# ======================================================================


def test_plan_generation_does_not_mutate_state():
    b1 = factories.bank_item("b1", amount=1_000_000, reference="INV-1")
    j1 = factories.book_item("j1", amount=1_000_000, reference="INV-1")
    _, _, state, _ = _build_test_environment(bank_items=(b1,), book_items=(j1,))

    fp_before = state_fingerprint(state)
    rev_before = state.revision
    prev_before = state.persistence_revision

    service = ReconciliationService()
    queries = BookkeepingQueries(state)
    plan = service.plan(queries)

    assert plan.has_reconciliations
    assert state_fingerprint(state) == fp_before
    assert state.revision == rev_before
    assert state.persistence_revision == prev_before


# ======================================================================
# 17. ReconciliationPlan -> one TransitionBatch & atomic apply
# ======================================================================


def test_reconciliation_plan_to_one_batch_and_atomic_apply():
    b1 = factories.bank_item("b1", amount=1_000_000, reference="INV-1")
    j1 = factories.book_item("j1", amount=1_000_000, reference="INV-1")
    b2 = factories.bank_item("b2", amount=500_000, reference="INV-2")
    j2 = factories.book_item("j2", amount=500_000, reference="INV-2")

    _, _, state, engine = _build_test_environment(
        bank_items=(b1, b2),
        book_items=(j1, j2),
    )

    queries = BookkeepingQueries(state)
    service = ReconciliationService()
    plan = service.plan(queries)

    assert plan.state_revision == 0
    assert len(plan.commands) == 2

    batch = plan.to_batch()
    assert batch.expected_state_revision == 0
    assert len(batch.commands) == 2
    for cmd in batch.commands:
        assert cmd.expected_state_revision == 0

    result = engine.apply_batch(state=state, batch=batch)
    assert result.status == TransitionStatus.APPLIED
    assert len(result.command_results) == 2
    assert state.revision == 1


# ======================================================================
# 18. All selected reconciliation artifacts share one creation revision
# ======================================================================


def test_all_selected_reconciliations_share_one_creation_revision():
    b1 = factories.bank_item("b1", amount=1_000_000, reference="INV-1")
    j1 = factories.book_item("j1", amount=1_000_000, reference="INV-1")
    b2 = factories.bank_item("b2", amount=500_000, reference="INV-2")
    j2 = factories.book_item("j2", amount=500_000, reference="INV-2")

    _, _, state, engine = _build_test_environment(
        bank_items=(b1, b2),
        book_items=(j1, j2),
    )

    service = ReconciliationService()
    plan = service.plan(BookkeepingQueries(state))
    batch = plan.to_batch()
    res = engine.apply_batch(state=state, batch=batch)

    assert res.status == TransitionStatus.APPLIED
    assert state.revision == 1

    # Both reconciliation artifacts in state must have state_revision_at_creation == 1
    recs = tuple(state.reconciliations.values())
    assert len(recs) == 2
    for r in recs:
        assert r.state_revision_at_creation == 1


# ======================================================================
# 19. State revision and persistence revision advance once
# ======================================================================


def test_state_revision_and_persistence_revision_advance_once():
    b1 = factories.bank_item("b1", amount=1_000_000, reference="INV-1")
    j1 = factories.book_item("j1", amount=1_000_000, reference="INV-1")
    b2 = factories.bank_item("b2", amount=500_000, reference="INV-2")
    j2 = factories.book_item("j2", amount=500_000, reference="INV-2")

    repo, _, state, engine = _build_test_environment(
        bank_items=(b1, b2),
        book_items=(j1, j2),
    )

    start_state_rev = state.revision
    start_persist_rev = state.persistence_revision

    service = ReconciliationService()
    plan = service.plan(BookkeepingQueries(state))
    batch = plan.to_batch()
    engine.apply_batch(state=state, batch=batch)

    assert state.revision == start_state_rev + 1
    assert state.persistence_revision == start_persist_rev + 1
    assert repo.current_revision(company_id=state.context.company_id) == start_persist_rev + 1


# ======================================================================
# 20. Invalidation releases capacity & rehydration reconstructs truth
# ======================================================================


def test_invalidation_releases_capacity_and_rehydrates():
    b1 = factories.bank_item("b1", amount=1_000_000, reference="INV-1")
    j1 = factories.book_item("j1", amount=1_000_000, reference="INV-1")

    repo, hydrator, state, engine = _build_test_environment(
        bank_items=(b1,),
        book_items=(j1,),
    )

    # 1. Reconcile
    service = ReconciliationService()
    plan = service.plan(BookkeepingQueries(state))
    engine.apply_batch(state=state, batch=plan.to_batch())

    derived = build_derived_state(state)
    assert derived.bank_remaining("b1") == 0
    assert derived.book_remaining("j1") == 0

    # 2. Invalidate
    rec = tuple(state.reconciliations.values())[0]
    inv_cmd = InvalidateReconciliationCommand(
        command_id="cmd-inv-1",
        expected_state_revision=state.revision,
        source=CommandSource.RECONCILIATION,
        session_id=state.session_id,
        issued_at=FIXED_TIME,
        invalidation_id="inv-1",
        reconciliation_id=rec.id,
        reason="erroneous match",
    )
    res = engine.apply(state=state, command=inv_cmd)
    assert res.status == TransitionStatus.APPLIED

    # Derived state now releases capacity back
    derived_after = build_derived_state(state)
    assert derived_after.bank_remaining("b1") == 1_000_000
    assert derived_after.book_remaining("j1") == 1_000_000

    # 3. Destroy live state and rehydrate
    fresh_state = hydrator.hydrate(company_id=state.context.company_id, session_id="fresh-session")
    fresh_derived = build_derived_state(fresh_state)
    assert fresh_derived.bank_remaining("b1") == 1_000_000
    assert fresh_derived.book_remaining("j1") == 1_000_000


# ======================================================================
# 21. Hypotheses invalidated once
# ======================================================================


def test_hypotheses_invalidated_once():
    b1 = factories.bank_item("b1", amount=1_000_000)
    j1 = factories.book_item("j1", amount=1_000_000)

    repo, hydrator, state, engine = _build_test_environment(
        bank_items=(b1,),
        book_items=(j1,),
    )

    hyp = ReconciliationHypothesis(
        id="hyp-to-invalidate",
        eligibility=Eligibility.SELECTABLE,
        utility=900,
        bank_allocations=(BankAllocation(bank_item_id="b1", amount_units="1000000"),),
        book_allocations=(BookAllocation(book_item_id="j1", amount_units="1000000"),),
        state_revision=state.revision,
        generated_at=FIXED_TIME,
    )
    state._put_reconciliation_hypothesis(hyp)
    assert len(state.reconciliation_hypotheses) == 1

    service = ReconciliationService()
    plan = service.plan(BookkeepingQueries(state), additional_hypotheses=(hyp,))
    res = engine.apply_batch(state=state, batch=plan.to_batch())

    assert res.status == TransitionStatus.APPLIED
    # Hypotheses pool in live state must be cleared
    assert len(state.reconciliation_hypotheses) == 0

    # Ensure delta recorded that hypotheses were cleared
    assert res.delta.clear_all_hypotheses is True


# ======================================================================
# 22. Hypothesis-provenance correction tests
# ======================================================================


def test_service_plan_read_only_and_embeds_hypothesis_provenance():
    b1 = factories.bank_item("b1", amount=1_000_000, reference="INV-1")
    j1 = factories.book_item("j1", amount=1_000_000, reference="INV-1")

    repo, hydrator, state, engine = _build_test_environment(
        bank_items=(b1,),
        book_items=(j1,),
    )

    fp_before = state_fingerprint(state)
    assert state.revision == 0
    assert len(state.reconciliation_hypotheses) == 0

    # 1. Normal service.plan() must remain completely read-only
    service = ReconciliationService()
    plan = service.plan(BookkeepingQueries(state))

    fp_after = state_fingerprint(state)
    assert fp_before == fp_after, "service.plan() must not mutate BookkeepingState"
    assert state.revision == 0
    assert len(state.reconciliation_hypotheses) == 0, "service.plan() must never inject hypotheses into state"

    # 2. Selected hypothesis evidence is embedded in command
    assert len(plan.commands) == 1
    cmd = plan.commands[0]
    assert cmd.source_hypothesis_id is not None
    assert cmd.source_hypothesis is not None
    assert cmd.source_hypothesis.id == cmd.source_hypothesis_id
    assert cmd.source_hypothesis.eligibility == Eligibility.SELECTABLE
    assert cmd.source_hypothesis.state_revision == state.revision

    # 3. TransitionBatch remains reconciliation-agnostic
    batch = plan.to_batch()
    assert not hasattr(batch, "hypotheses")
    assert not hasattr(batch, "reconciliation_hypotheses")
    assert hasattr(batch, "commands")

    # 4. Normal plan applies without pre-registering hypotheses in state
    res = engine.apply_batch(state=state, batch=batch)
    assert res.status == TransitionStatus.APPLIED

    # 5. Durable Reconciliation retains compact accepted-hypothesis provenance
    rec = tuple(state.reconciliations.values())[0]
    assert rec.source_hypothesis_id == cmd.source_hypothesis.id
    assert rec.source_hypothesis_state_revision == cmd.source_hypothesis.state_revision
    assert rec.source_hypothesis_utility == cmd.source_hypothesis.utility
    assert rec.source_hypothesis_generated_at == cmd.source_hypothesis.generated_at
    assert rec.hypothesis_provenance is not None
    assert rec.hypothesis_provenance.hypothesis_id == cmd.source_hypothesis.id
    assert rec.hypothesis_provenance.state_revision == cmd.source_hypothesis.state_revision
    assert rec.hypothesis_provenance.utility == cmd.source_hypothesis.utility
    assert rec.hypothesis_provenance.generated_at == cmd.source_hypothesis.generated_at

    # 6. Destroy / rehydrate preserves accepted hypothesis provenance
    company_id = state.context.company_id
    state.close()
    fresh_state = hydrator.hydrate(company_id=company_id)
    rehydrated_rec = tuple(fresh_state.reconciliations.values())[0]
    assert rehydrated_rec.source_hypothesis_id == cmd.source_hypothesis.id
    assert rehydrated_rec.source_hypothesis_state_revision == cmd.source_hypothesis.state_revision
    assert rehydrated_rec.source_hypothesis_utility == cmd.source_hypothesis.utility
    assert rehydrated_rec.source_hypothesis_generated_at == cmd.source_hypothesis.generated_at
    assert rehydrated_rec.hypothesis_provenance is not None

    # 7. Runtime hypothesis objects themselves are not persisted
    assert len(fresh_state.reconciliation_hypotheses) == 0
    snapshot = repo.load_snapshot(company_id=company_id)
    assert not hasattr(snapshot, "reconciliation_hypotheses")


def test_allocation_tampering_after_scoring_rejects():
    b1 = factories.bank_item("b1", amount=1_000_000)
    j1 = factories.book_item("j1", amount=1_000_000)

    repo, hydrator, state, engine = _build_test_environment(
        bank_items=(b1,),
        book_items=(j1,),
    )

    hyp = ReconciliationHypothesis(
        id="hyp-tamper",
        state_revision=state.revision,
        bank_allocations=(BankAllocation(bank_item_id="b1", amount_units="1000000"),),
        book_allocations=(BookAllocation(book_item_id="j1", amount_units="1000000"),),
        eligibility=Eligibility.SELECTABLE,
        utility=900,
        generated_at=FIXED_TIME,
    )

    # Command tampers with allocation amount compared to embedded hypothesis
    cmd = CreateReconciliationCommand(
        command_id="cmd-tamper",
        expected_state_revision=state.revision,
        source=CommandSource.RECONCILIATION,
        session_id=state.session_id,
        issued_at=FIXED_TIME,
        reconciliation_id="rec-tamper",
        bank_allocations=(BankAllocation(bank_item_id="b1", amount_units="500000"),),
        book_allocations=(BookAllocation(book_item_id="j1", amount_units="500000"),),
        source_hypothesis_id=hyp.id,
        source_hypothesis=hyp,
    )

    res = engine.apply(state=state, command=cmd)
    assert res.status == TransitionStatus.REJECTED
    assert res.rejection.code == RejectionCode.INVALID_ALLOCATION


def test_stale_embedded_hypothesis_rejects():
    b1 = factories.bank_item("b1", amount=1_000_000)
    j1 = factories.book_item("j1", amount=1_000_000)

    repo, hydrator, state, engine = _build_test_environment(
        bank_items=(b1,),
        book_items=(j1,),
    )

    # Hypothesis generated against stale revision 99
    hyp = ReconciliationHypothesis(
        id="hyp-stale",
        state_revision=99,
        bank_allocations=(BankAllocation(bank_item_id="b1", amount_units="1000000"),),
        book_allocations=(BookAllocation(book_item_id="j1", amount_units="1000000"),),
        eligibility=Eligibility.SELECTABLE,
        utility=900,
        generated_at=FIXED_TIME,
    )

    cmd = CreateReconciliationCommand(
        command_id="cmd-stale",
        expected_state_revision=state.revision,
        source=CommandSource.RECONCILIATION,
        session_id=state.session_id,
        issued_at=FIXED_TIME,
        reconciliation_id="rec-stale",
        bank_allocations=hyp.bank_allocations,
        book_allocations=hyp.book_allocations,
        source_hypothesis_id=hyp.id,
        source_hypothesis=hyp,
    )

    res = engine.apply(state=state, command=cmd)
    assert res.status == TransitionStatus.REJECTED
    assert res.rejection.code == RejectionCode.STALE_HYPOTHESIS


def test_counterfactual_only_embedded_hypothesis_rejects():
    b1 = factories.bank_item("b1", amount=1_000_000)
    j1 = factories.book_item("j1", amount=1_000_000)

    repo, hydrator, state, engine = _build_test_environment(
        bank_items=(b1,),
        book_items=(j1,),
    )

    hyp = ReconciliationHypothesis(
        id="hyp-counterfactual",
        state_revision=state.revision,
        bank_allocations=(BankAllocation(bank_item_id="b1", amount_units="1000000"),),
        book_allocations=(BookAllocation(book_item_id="j1", amount_units="1000000"),),
        eligibility=Eligibility.COUNTERFACTUAL_ONLY,
        utility=500,
        generated_at=FIXED_TIME,
    )

    cmd = CreateReconciliationCommand(
        command_id="cmd-cf",
        expected_state_revision=state.revision,
        source=CommandSource.RECONCILIATION,
        session_id=state.session_id,
        issued_at=FIXED_TIME,
        reconciliation_id="rec-cf",
        bank_allocations=hyp.bank_allocations,
        book_allocations=hyp.book_allocations,
        source_hypothesis_id=hyp.id,
        source_hypothesis=hyp,
    )

    res = engine.apply(state=state, command=cmd)
    assert res.status == TransitionStatus.REJECTED
    assert res.rejection.code == RejectionCode.INVALID_ALLOCATION


def test_mismatched_hypothesis_id_rejects():
    b1 = factories.bank_item("b1", amount=1_000_000)
    j1 = factories.book_item("j1", amount=1_000_000)

    repo, hydrator, state, engine = _build_test_environment(
        bank_items=(b1,),
        book_items=(j1,),
    )

    hyp = ReconciliationHypothesis(
        id="hyp-actual",
        state_revision=state.revision,
        bank_allocations=(BankAllocation(bank_item_id="b1", amount_units="1000000"),),
        book_allocations=(BookAllocation(book_item_id="j1", amount_units="1000000"),),
        eligibility=Eligibility.SELECTABLE,
        utility=900,
        generated_at=FIXED_TIME,
    )

    cmd = CreateReconciliationCommand(
        command_id="cmd-mismatch",
        expected_state_revision=state.revision,
        source=CommandSource.RECONCILIATION,
        session_id=state.session_id,
        issued_at=FIXED_TIME,
        reconciliation_id="rec-mismatch",
        bank_allocations=hyp.bank_allocations,
        book_allocations=hyp.book_allocations,
        source_hypothesis_id="hyp-different",
        source_hypothesis=hyp,
    )

    res = engine.apply(state=state, command=cmd)
    assert res.status == TransitionStatus.REJECTED
    assert res.rejection.code == RejectionCode.INVALID_HYPOTHESIS_PROVENANCE


def test_interactive_state_stored_hypothesis_path_works():
    b1 = factories.bank_item("b1", amount=1_000_000)
    j1 = factories.book_item("j1", amount=1_000_000)

    repo, hydrator, state, engine = _build_test_environment(
        bank_items=(b1,),
        book_items=(j1,),
    )

    hyp = ReconciliationHypothesis(
        id="hyp-interactive",
        state_revision=state.revision,
        bank_allocations=(BankAllocation(bank_item_id="b1", amount_units="1000000"),),
        book_allocations=(BookAllocation(book_item_id="j1", amount_units="1000000"),),
        eligibility=Eligibility.SELECTABLE,
        utility=850,
        generated_at=FIXED_TIME,
    )
    # Stored in state interactively
    state._put_reconciliation_hypothesis(hyp)

    # Command has source_hypothesis_id but source_hypothesis is None
    cmd = CreateReconciliationCommand(
        command_id="cmd-interactive",
        expected_state_revision=state.revision,
        source=CommandSource.RECONCILIATION,
        session_id=state.session_id,
        issued_at=FIXED_TIME,
        reconciliation_id="rec-interactive",
        bank_allocations=hyp.bank_allocations,
        book_allocations=hyp.book_allocations,
        source_hypothesis_id=hyp.id,
        source_hypothesis=None,
    )

    res = engine.apply(state=state, command=cmd)
    assert res.status == TransitionStatus.APPLIED

    rec = tuple(state.reconciliations.values())[0]
    assert rec.source_hypothesis_id == hyp.id
    assert rec.source_hypothesis_state_revision == hyp.state_revision
    assert rec.source_hypothesis_utility == hyp.utility
    assert rec.source_hypothesis_generated_at == hyp.generated_at
    assert rec.hypothesis_provenance is not None


def test_embedded_and_state_stored_hypothesis_disagreement_rejects():
    b1 = factories.bank_item("b1", amount=1_000_000)
    j1 = factories.book_item("j1", amount=1_000_000)

    repo, hydrator, state, engine = _build_test_environment(
        bank_items=(b1,),
        book_items=(j1,),
    )

    hyp_state = ReconciliationHypothesis(
        id="hyp-shared",
        state_revision=state.revision,
        bank_allocations=(BankAllocation(bank_item_id="b1", amount_units="1000000"),),
        book_allocations=(BookAllocation(book_item_id="j1", amount_units="1000000"),),
        eligibility=Eligibility.SELECTABLE,
        utility=800,
        generated_at=FIXED_TIME,
    )
    state._put_reconciliation_hypothesis(hyp_state)

    # Embedded hypothesis with same ID but different utility / content
    hyp_embedded = ReconciliationHypothesis(
        id="hyp-shared",
        state_revision=state.revision,
        bank_allocations=(BankAllocation(bank_item_id="b1", amount_units="1000000"),),
        book_allocations=(BookAllocation(book_item_id="j1", amount_units="1000000"),),
        eligibility=Eligibility.SELECTABLE,
        utility=999,  # disagrees with state
        generated_at=FIXED_TIME,
    )

    cmd = CreateReconciliationCommand(
        command_id="cmd-disagree",
        expected_state_revision=state.revision,
        source=CommandSource.RECONCILIATION,
        session_id=state.session_id,
        issued_at=FIXED_TIME,
        reconciliation_id="rec-disagree",
        bank_allocations=hyp_embedded.bank_allocations,
        book_allocations=hyp_embedded.book_allocations,
        source_hypothesis_id=hyp_embedded.id,
        source_hypothesis=hyp_embedded,
    )

    res = engine.apply(state=state, command=cmd)
    assert res.status == TransitionStatus.REJECTED
    assert res.rejection.code == RejectionCode.INVALID_HYPOTHESIS_PROVENANCE


def test_reconciliation_command_without_hypothesis_remains_supported():
    b1 = factories.bank_item("b1", amount=1_000_000)
    j1 = factories.book_item("j1", amount=1_000_000)

    repo, hydrator, state, engine = _build_test_environment(
        bank_items=(b1,),
        book_items=(j1,),
    )

    cmd = CreateReconciliationCommand(
        command_id="cmd-human",
        expected_state_revision=state.revision,
        source=CommandSource.HUMAN,
        session_id=state.session_id,
        issued_at=FIXED_TIME,
        reconciliation_id="rec-human",
        bank_allocations=(BankAllocation(bank_item_id="b1", amount_units="1000000"),),
        book_allocations=(BookAllocation(book_item_id="j1", amount_units="1000000"),),
        source_hypothesis_id=None,
        source_hypothesis=None,
    )

    res = engine.apply(state=state, command=cmd)
    assert res.status == TransitionStatus.APPLIED

    rec = tuple(state.reconciliations.values())[0]
    assert rec.source_hypothesis_id is None
    assert rec.source_hypothesis_state_revision is None
    assert rec.source_hypothesis_utility is None
    assert rec.source_hypothesis_generated_at is None
    assert rec.hypothesis_provenance is None


def test_partial_hypothesis_provenance_rejected_at_domain_level():
    # Only 1 field present
    with pytest.raises(ValidationError, match="either all present or all absent"):
        Reconciliation(
            id="rec-partial-1",
            bank_allocations=(BankAllocation(bank_item_id="b1", amount_units="1000000"),),
            book_allocations=(BookAllocation(book_item_id="j1", amount_units="1000000"),),
            source_hypothesis_id="hyp-1",
            state_revision_at_creation=1,
            created_at=FIXED_TIME,
        )

    # 2 fields present
    with pytest.raises(ValidationError, match="either all present or all absent"):
        Reconciliation(
            id="rec-partial-2",
            bank_allocations=(BankAllocation(bank_item_id="b1", amount_units="1000000"),),
            book_allocations=(BookAllocation(book_item_id="j1", amount_units="1000000"),),
            source_hypothesis_id="hyp-1",
            source_hypothesis_utility=950,
            state_revision_at_creation=1,
            created_at=FIXED_TIME,
        )

    # 3 fields present
    with pytest.raises(ValidationError, match="either all present or all absent"):
        Reconciliation(
            id="rec-partial-3",
            bank_allocations=(BankAllocation(bank_item_id="b1", amount_units="1000000"),),
            book_allocations=(BookAllocation(book_item_id="j1", amount_units="1000000"),),
            source_hypothesis_id="hyp-1",
            source_hypothesis_state_revision=0,
            source_hypothesis_utility=950,
            source_hypothesis_generated_at=None,
            state_revision_at_creation=1,
            created_at=FIXED_TIME,
        )


def test_provenance_state_revision_plus_one_enforced_at_domain_level():
    # Mismatched revision: state_revision_at_creation != source_hypothesis_state_revision + 1
    with pytest.raises(ValidationError, match="must equal state_revision_at_creation"):
        Reconciliation(
            id="rec-rev-mismatch",
            bank_allocations=(BankAllocation(bank_item_id="b1", amount_units="1000000"),),
            book_allocations=(BookAllocation(book_item_id="j1", amount_units="1000000"),),
            source_hypothesis_id="hyp-1",
            source_hypothesis_state_revision=0,
            source_hypothesis_utility=900,
            source_hypothesis_generated_at=FIXED_TIME,
            state_revision_at_creation=5,  # Expected 0 + 1 = 1
            created_at=FIXED_TIME,
        )

    # Valid: 0 + 1 == 1
    valid_rec = Reconciliation(
        id="rec-rev-valid",
        bank_allocations=(BankAllocation(bank_item_id="b1", amount_units="1000000"),),
        book_allocations=(BookAllocation(book_item_id="j1", amount_units="1000000"),),
        source_hypothesis_id="hyp-1",
        source_hypothesis_state_revision=0,
        source_hypothesis_utility=900,
        source_hypothesis_generated_at=FIXED_TIME,
        state_revision_at_creation=1,
        created_at=FIXED_TIME,
    )
    assert valid_rec.source_hypothesis_state_revision + 1 == valid_rec.state_revision_at_creation


def test_timezone_aware_generated_at_enforced():
    naive_dt = datetime(2026, 1, 31, 12, 0, 0)

    with pytest.raises(ValidationError, match="timezone-aware"):
        Reconciliation(
            id="rec-naive-gen",
            bank_allocations=(BankAllocation(bank_item_id="b1", amount_units="1000000"),),
            book_allocations=(BookAllocation(book_item_id="j1", amount_units="1000000"),),
            source_hypothesis_id="hyp-1",
            source_hypothesis_state_revision=0,
            source_hypothesis_utility=900,
            source_hypothesis_generated_at=naive_dt,
            state_revision_at_creation=1,
            created_at=FIXED_TIME,
        )

    with pytest.raises(ValidationError, match="timezone-aware"):
        ReconciliationHypothesisProvenance(
            hypothesis_id="hyp-1",
            state_revision=0,
            utility=900,
            generated_at=naive_dt,
        )


def test_command_evidence_and_rationale_tampering_rejects():
    b1 = factories.bank_item("b1", amount=1_000_000)
    j1 = factories.book_item("j1", amount=1_000_000)
    doc_orig = factories.document("doc-original")
    doc_tamp = factories.document("doc-tampered")

    repo, hydrator, state, engine = _build_test_environment(
        bank_items=(b1,),
        book_items=(j1,),
        documents=(doc_orig, doc_tamp),
    )

    hyp = ReconciliationHypothesis(
        id="hyp-ev-rat",
        state_revision=state.revision,
        bank_allocations=(BankAllocation(bank_item_id="b1", amount_units="1000000"),),
        book_allocations=(BookAllocation(book_item_id="j1", amount_units="1000000"),),
        evidence_refs=("doc-original",),
        semantic_rationale="original explanation",
        eligibility=Eligibility.SELECTABLE,
        utility=900,
        generated_at=FIXED_TIME,
    )

    # 1. Evidence tampering rejects
    cmd_tamper_ev = CreateReconciliationCommand(
        command_id="cmd-tamper-ev",
        expected_state_revision=state.revision,
        source=CommandSource.RECONCILIATION,
        session_id=state.session_id,
        issued_at=FIXED_TIME,
        reconciliation_id="rec-tamper-ev",
        bank_allocations=hyp.bank_allocations,
        book_allocations=hyp.book_allocations,
        evidence_refs=("doc-tampered",),
        semantic_rationale=hyp.semantic_rationale,
        source_hypothesis_id=hyp.id,
        source_hypothesis=hyp,
    )
    res = engine.apply(state=state, command=cmd_tamper_ev)
    assert res.status == TransitionStatus.REJECTED
    assert res.rejection.code == RejectionCode.INVALID_HYPOTHESIS_PROVENANCE

    # 2. Rationale tampering rejects
    cmd_tamper_rat = CreateReconciliationCommand(
        command_id="cmd-tamper-rat",
        expected_state_revision=state.revision,
        source=CommandSource.RECONCILIATION,
        session_id=state.session_id,
        issued_at=FIXED_TIME,
        reconciliation_id="rec-tamper-rat",
        bank_allocations=hyp.bank_allocations,
        book_allocations=hyp.book_allocations,
        evidence_refs=hyp.evidence_refs,
        semantic_rationale="tampered rationale",
        source_hypothesis_id=hyp.id,
        source_hypothesis=hyp,
    )
    res = engine.apply(state=state, command=cmd_tamper_rat)
    assert res.status == TransitionStatus.REJECTED
    assert res.rejection.code == RejectionCode.INVALID_HYPOTHESIS_PROVENANCE

    # 3. Omitting evidence and rationale copies them from validated hypothesis
    cmd_omit = CreateReconciliationCommand(
        command_id="cmd-omit",
        expected_state_revision=state.revision,
        source=CommandSource.RECONCILIATION,
        session_id=state.session_id,
        issued_at=FIXED_TIME,
        reconciliation_id="rec-omit",
        bank_allocations=hyp.bank_allocations,
        book_allocations=hyp.book_allocations,
        evidence_refs=(),
        semantic_rationale=None,
        source_hypothesis_id=hyp.id,
        source_hypothesis=hyp,
    )
    res = engine.apply(state=state, command=cmd_omit)
    assert res.status == TransitionStatus.APPLIED
    rec = state.get_reconciliation("rec-omit")
    assert rec.evidence_refs == ("doc-original",)
    assert rec.semantic_rationale == "original explanation"


def test_fingerprint_includes_durable_hypothesis_provenance():
    b1 = factories.bank_item("b1", amount=1_000_000)
    j1 = factories.book_item("j1", amount=1_000_000)

    # State with provenance
    _, _, state1, _ = _build_test_environment(
        bank_items=(b1,),
        book_items=(j1,),
        reconciliations=(
            Reconciliation(
                id="rec-1",
                bank_allocations=(BankAllocation(bank_item_id="b1", amount_units="1000000"),),
                book_allocations=(BookAllocation(book_item_id="j1", amount_units="1000000"),),
                source_hypothesis_id="hyp-A",
                source_hypothesis_state_revision=0,
                source_hypothesis_utility=900,
                source_hypothesis_generated_at=FIXED_TIME,
                state_revision_at_creation=1,
                created_at=FIXED_TIME,
            ),
        ),
    )

    # State with different hypothesis utility
    _, _, state2, _ = _build_test_environment(
        bank_items=(b1,),
        book_items=(j1,),
        reconciliations=(
            Reconciliation(
                id="rec-1",
                bank_allocations=(BankAllocation(bank_item_id="b1", amount_units="1000000"),),
                book_allocations=(BookAllocation(book_item_id="j1", amount_units="1000000"),),
                source_hypothesis_id="hyp-A",
                source_hypothesis_state_revision=0,
                source_hypothesis_utility=400,  # different utility
                source_hypothesis_generated_at=FIXED_TIME,
                state_revision_at_creation=1,
                created_at=FIXED_TIME,
            ),
        ),
    )

    # State with no hypothesis provenance
    _, _, state3, _ = _build_test_environment(
        bank_items=(b1,),
        book_items=(j1,),
        reconciliations=(
            Reconciliation(
                id="rec-1",
                bank_allocations=(BankAllocation(bank_item_id="b1", amount_units="1000000"),),
                book_allocations=(BookAllocation(book_item_id="j1", amount_units="1000000"),),
                state_revision_at_creation=1,
                created_at=FIXED_TIME,
            ),
        ),
    )

    fp1_art = artifact_fingerprint(state1)
    fp2_art = artifact_fingerprint(state2)
    fp3_art = artifact_fingerprint(state3)

    assert fp1_art != fp2_art
    assert fp1_art != fp3_art
    assert fp2_art != fp3_art

    fp1_state = state_fingerprint(state1)
    fp2_state = state_fingerprint(state2)
    fp3_state = state_fingerprint(state3)

    assert fp1_state != fp2_state
    assert fp1_state != fp3_state
    assert fp2_state != fp3_state


def test_hydration_rejects_malformed_persisted_provenance():
    b1 = factories.bank_item("b1", amount=1_000_000)
    j1 = factories.book_item("j1", amount=1_000_000)

    repo, hydrator, state, engine = _build_test_environment(
        bank_items=(b1,),
        book_items=(j1,),
    )

    # 1. Direct validation of state with mismatched provenance revision catches it
    corrupted_rec = Reconciliation(
        id="rec-corrupt",
        bank_allocations=(BankAllocation(bank_item_id="b1", amount_units="1000000"),),
        book_allocations=(BookAllocation(book_item_id="j1", amount_units="1000000"),),
        source_hypothesis_id="hyp-1",
        source_hypothesis_state_revision=0,
        source_hypothesis_utility=900,
        source_hypothesis_generated_at=FIXED_TIME,
        state_revision_at_creation=1,
        created_at=FIXED_TIME,
    )
    # Bypass frozen Pydantic model validator via object.__setattr__ to simulate post-deserialization corruption
    object.__setattr__(corrupted_rec, "state_revision_at_creation", 5)

    # Build unvalidated state using internal store
    corrupted_state = hydrator.hydrate(company_id=state.context.company_id, require_valid=False)
    corrupted_state._reconciliations["rec-corrupt"] = corrupted_rec
    report = validate_state(corrupted_state)
    assert not report.is_valid
    assert any(
        issue.code == ValidationCode.INVALID_HYPOTHESIS_PROVENANCE
        for issue in report.errors
    )

    # 2. Hydration rejects snapshot containing malformed provenance
    repo._companies[state.context.company_id].reconciliations["rec-corrupt"] = corrupted_rec

    with pytest.raises(InvalidHydratedStateError) as exc_info:
        hydrator.hydrate(company_id=state.context.company_id)

    assert any(
        issue.code == ValidationCode.INVALID_HYPOTHESIS_PROVENANCE
        for issue in exc_info.value.report.errors
    )


# ======================================================================
# 35. Optimizer Objective Corrections: Unique Cleared Items & Amount-Weighted Semantic Utility
# ======================================================================


def test_shared_item_counted_once_in_items_cleared_objective():
    """
    Test A: A shared item appearing in multiple selected hypotheses is counted
    exactly once in Phase 2 (unique fully-cleared items).
    Scenario C style: 3 bank items (20k, 30k, 50k) clear 1 book item (100k).
    Resulting cleared items count must be 4, NOT 6.
    """
    b1 = factories.bank_item("b1", amount=200_000)
    b2 = factories.bank_item("b2", amount=300_000)
    b3 = factories.bank_item("b3", amount=500_000)
    j1 = factories.book_item("j1", amount=1_000_000)

    _, _, state, _ = _build_test_environment(
        bank_items=(b1, b2, b3),
        book_items=(j1,),
        policy_updates={"allow_partial_book_reconciliation": True},
    )
    view = build_reconciliation_view(BookkeepingQueries(state))

    h1 = ReconciliationHypothesis(
        id="hyp-part-1",
        eligibility=Eligibility.SELECTABLE,
        utility=550,
        bank_allocations=(BankAllocation(bank_item_id="b1", amount_units="200000"),),
        book_allocations=(BookAllocation(book_item_id="j1", amount_units="200000"),),
        state_revision=view.state_revision,
        generated_at=FIXED_TIME,
    )
    h2 = ReconciliationHypothesis(
        id="hyp-part-2",
        eligibility=Eligibility.SELECTABLE,
        utility=550,
        bank_allocations=(BankAllocation(bank_item_id="b2", amount_units="300000"),),
        book_allocations=(BookAllocation(book_item_id="j1", amount_units="300000"),),
        state_revision=view.state_revision,
        generated_at=FIXED_TIME,
    )
    h3 = ReconciliationHypothesis(
        id="hyp-part-3",
        eligibility=Eligibility.SELECTABLE,
        utility=550,
        bank_allocations=(BankAllocation(bank_item_id="b3", amount_units="500000"),),
        book_allocations=(BookAllocation(book_item_id="j1", amount_units="500000"),),
        state_revision=view.state_revision,
        generated_at=FIXED_TIME,
    )

    result = optimize_reconciliation(
        view=view,
        hypotheses=(h1, h2, h3),
        solver_run_id="test-shared-items",
    )
    assert len(result.selected_hypotheses) == 3
    assert result.reconciled_units_int == 1_000_000
    # Exactly 4 unique items cleared (b1, b2, b3, j1), NOT 6 endpoints
    assert result.objective_items_cleared == 4


def test_partial_item_receives_zero_cleared_credit():
    """
    Test B: An item that is only partially consumed receives zero credit
    in the Phase 2 items-cleared objective.
    bank1 (200k) consumes only 200k of book1 (1_000_000).
    bank1 is fully cleared (credit 1), but book1 still has 800k remaining (credit 0).
    Total items cleared must be 1, NOT 2.
    """
    b1 = factories.bank_item("b1", amount=200_000)
    j1 = factories.book_item("j1", amount=1_000_000)

    _, _, state, _ = _build_test_environment(
        bank_items=(b1,),
        book_items=(j1,),
        policy_updates={"allow_partial_book_reconciliation": True},
    )
    view = build_reconciliation_view(BookkeepingQueries(state))

    h1 = ReconciliationHypothesis(
        id="hyp-part",
        eligibility=Eligibility.SELECTABLE,
        utility=550,
        bank_allocations=(BankAllocation(bank_item_id="b1", amount_units="200000"),),
        book_allocations=(BookAllocation(book_item_id="j1", amount_units="200000"),),
        state_revision=view.state_revision,
        generated_at=FIXED_TIME,
    )

    result = optimize_reconciliation(
        view=view,
        hypotheses=(h1,),
        solver_run_id="test-partial-credit",
    )
    assert len(result.selected_hypotheses) == 1
    assert result.reconciled_units_int == 200_000
    # Only bank1 is cleared; book1 has residual 800k and contributes 0
    assert result.objective_items_cleared == 1


def test_existing_residual_capacity_clearing():
    """
    Test C: An item with pre-existing partial consumption is evaluated against
    current remaining capacity in ReconciliationView, not original amount.
    Original book1 = 1_000_000, 400_000 already reconciled, remaining = 600_000.
    Allocating 600_000 fully clears book1 and receives cleared-item credit.
    """
    b_old = factories.bank_item("b_old", amount=400_000)
    b_new = factories.bank_item("b_new", amount=600_000)
    j1 = factories.book_item("j1", amount=1_000_000)

    prior_rec = Reconciliation(
        id="rec-prior",
        bank_allocations=(BankAllocation(bank_item_id="b_old", amount_units="400000"),),
        book_allocations=(BookAllocation(book_item_id="j1", amount_units="400000"),),
        source_hypothesis_id="hyp-prior",
        source_hypothesis_state_revision=0,
        source_hypothesis_utility=900,
        source_hypothesis_generated_at=FIXED_TIME,
        state_revision_at_creation=1,
        created_at=FIXED_TIME,
    )

    _, _, state, _ = _build_test_environment(
        bank_items=(b_old, b_new),
        book_items=(j1,),
        reconciliations=(prior_rec,),
        policy_updates={"allow_partial_book_reconciliation": True},
    )
    queries = BookkeepingQueries(state)
    view = build_reconciliation_view(queries)

    # b_old is fully consumed (0 remaining) and excluded from view
    assert view.get_bank_item("b_old") is None
    # j1 has current remaining = 600_000
    j1_view = view.get_book_item("j1")
    assert j1_view is not None
    assert j1_view.remaining_amount_int == 600_000

    h_new = ReconciliationHypothesis(
        id="hyp-new",
        eligibility=Eligibility.SELECTABLE,
        utility=600,
        bank_allocations=(BankAllocation(bank_item_id="b_new", amount_units="600000"),),
        book_allocations=(BookAllocation(book_item_id="j1", amount_units="600000"),),
        state_revision=view.state_revision,
        generated_at=FIXED_TIME,
    )

    result = optimize_reconciliation(
        view=view,
        hypotheses=(h_new,),
        solver_run_id="test-residual-cleared",
    )
    assert len(result.selected_hypotheses) == 1
    # Both b_new (600k) and j1 (remaining 600k) are fully cleared
    assert result.objective_items_cleared == 2


def test_decomposition_invariant_semantic_value():
    """
    Test D: Two feasible solutions encoding the exact same economic amount (1_000_000)
    with the same per-unit utility (500):
    - Solution 1: 1 grouped hypothesis (amount 1_000_000, utility 500)
    - Solution 2: 2 fragmented hypotheses (amounts 400_000 + 600_000, utility 500 each)
    Both must produce identical amount-weighted semantic value (500_000_000).
    """
    b1 = factories.bank_item("b1", amount=400_000)
    b2 = factories.bank_item("b2", amount=600_000)
    j1 = factories.book_item("j1", amount=1_000_000)

    _, _, state, _ = _build_test_environment(
        bank_items=(b1, b2),
        book_items=(j1,),
        policy_updates={"allow_partial_book_reconciliation": True},
    )
    view = build_reconciliation_view(BookkeepingQueries(state))

    h_grouped = ReconciliationHypothesis(
        id="hyp-grouped",
        eligibility=Eligibility.SELECTABLE,
        utility=500,
        bank_allocations=(
            BankAllocation(bank_item_id="b1", amount_units="400000"),
            BankAllocation(bank_item_id="b2", amount_units="600000"),
        ),
        book_allocations=(BookAllocation(book_item_id="j1", amount_units="1000000"),),
        state_revision=view.state_revision,
        generated_at=FIXED_TIME,
    )

    h_frag1 = ReconciliationHypothesis(
        id="hyp-frag-1",
        eligibility=Eligibility.SELECTABLE,
        utility=500,
        bank_allocations=(BankAllocation(bank_item_id="b1", amount_units="400000"),),
        book_allocations=(BookAllocation(book_item_id="j1", amount_units="400000"),),
        state_revision=view.state_revision,
        generated_at=FIXED_TIME,
    )
    h_frag2 = ReconciliationHypothesis(
        id="hyp-frag-2",
        eligibility=Eligibility.SELECTABLE,
        utility=500,
        bank_allocations=(BankAllocation(bank_item_id="b2", amount_units="600000"),),
        book_allocations=(BookAllocation(book_item_id="j1", amount_units="600000"),),
        state_revision=view.state_revision,
        generated_at=FIXED_TIME,
    )

    res_grouped = optimize_reconciliation(
        view=view,
        hypotheses=(h_grouped,),
        solver_run_id="run-grouped",
    )
    res_fragmented = optimize_reconciliation(
        view=view,
        hypotheses=(h_frag1, h_frag2),
        solver_run_id="run-fragmented",
    )

    # 500 * 1_000_000 == 500 * 400_000 + 500 * 600_000 == 500_000_000
    assert res_grouped.objective_amount_weighted_semantic_value == 500_000_000
    assert res_fragmented.objective_amount_weighted_semantic_value == 500_000_000
    assert res_grouped.objective_items_cleared == res_fragmented.objective_items_cleared == 3


def test_amount_weighted_utility_counterexample_selects_correct_economic_truth():
    """
    Test E: Economic-truth counterexample proving that amount-weighted utility
    prevents candidate decomposition from overriding genuine semantic evidence.

    World:
        B1 = 500_000, B2 = 500_000
        K1 = 1_000_000 (correct aggregate target with strong reference evidence)
        K2 = 1_000_000 (competing target with no reference evidence)

    Solution A (Grouped B1+B2 -> K1):
        utility = 700 (strong evidence)
        money = 1_000_000, cleared = 3 items (B1, B2, K1)
        amount-weighted semantic value = 700 * 1_000_000 = 700_000_000

    Solution B (Fragmented B1 -> K2, B2 -> K2):
        each utility = 550
        money = 1_000_000, cleared = 3 items (B1, B2, K2)
        old raw utility sum = 550 + 550 = 1100 > 700 (would incorrectly win under raw sum!)
        amount-weighted semantic value = 550 * 500_000 + 550 * 500_000 = 550_000_000

    Under the corrected amount-weighted objective, Solution A (700M) defeats
    Solution B (550M), selecting the genuine economic counterparty K1.
    """
    b1 = factories.bank_item("b1", amount=500_000)
    b2 = factories.bank_item("b2", amount=500_000)
    k1 = factories.book_item("k1", amount=1_000_000)
    k2 = factories.book_item("k2", amount=1_000_000)

    _, _, state, _ = _build_test_environment(
        bank_items=(b1, b2),
        book_items=(k1, k2),
        policy_updates={"allow_partial_book_reconciliation": True},
    )
    view = build_reconciliation_view(BookkeepingQueries(state))

    h_grouped_k1 = ReconciliationHypothesis(
        id="hyp-grouped-k1",
        eligibility=Eligibility.SELECTABLE,
        utility=700,
        bank_allocations=(
            BankAllocation(bank_item_id="b1", amount_units="500000"),
            BankAllocation(bank_item_id="b2", amount_units="500000"),
        ),
        book_allocations=(BookAllocation(book_item_id="k1", amount_units="1000000"),),
        state_revision=view.state_revision,
        generated_at=FIXED_TIME,
    )

    h_partial_b1_k2 = ReconciliationHypothesis(
        id="hyp-part-b1-k2",
        eligibility=Eligibility.SELECTABLE,
        utility=550,
        bank_allocations=(BankAllocation(bank_item_id="b1", amount_units="500000"),),
        book_allocations=(BookAllocation(book_item_id="k2", amount_units="500000"),),
        state_revision=view.state_revision,
        generated_at=FIXED_TIME,
    )
    h_partial_b2_k2 = ReconciliationHypothesis(
        id="hyp-part-b2-k2",
        eligibility=Eligibility.SELECTABLE,
        utility=550,
        bank_allocations=(BankAllocation(bank_item_id="b2", amount_units="500000"),),
        book_allocations=(BookAllocation(book_item_id="k2", amount_units="500000"),),
        state_revision=view.state_revision,
        generated_at=FIXED_TIME,
    )

    result = optimize_reconciliation(
        view=view,
        hypotheses=(h_grouped_k1, h_partial_b1_k2, h_partial_b2_k2),
        solver_run_id="test-counterexample",
    )

    assert len(result.selected_hypotheses) == 1
    assert result.selected_hypotheses[0].id == "hyp-grouped-k1"
    assert result.objective_amount_weighted_semantic_value == 700_000_000
    assert result.objective_items_cleared == 3


def test_scenario_q_optimizer_competition_clears_more_unique_items():
    """
    Test F: Scenario Q still chooses 1:N grouped reconciliation over 1:1
    because 1:N clears 3 unique items whereas 1:1 clears 2 unique items.
    """
    from bookkeeping_state_eval.scenarios.catalog import get_scenario
    from bookkeeping_state_eval.scenarios.runner import ScenarioRunner

    scenario_q = get_scenario("scenario_q_optimizer_competition")
    runner = ScenarioRunner()
    result = runner.run(scenario_q)
    assert result.is_pass is True


def test_optimizer_phase3_gcd_scaling_prevents_int64_overflow():
    """
    Test integer overflow safety in Phase 3.
    If unscaled utility * amount sum would exceed CP-SAT's int64 safe bound ((1 << 62) - 1),
    the optimizer safely computes the exact common GCD of hypothesis amounts and scales down
    without loss of precision or relative ordering.
    """
    large_amount = 5_000_000_000_000_000  # 5 * 10^15 units
    b1 = factories.bank_item("b1", amount=large_amount)
    j1 = factories.book_item("j1", amount=large_amount)
    j2 = factories.book_item("j2", amount=large_amount)

    _, _, state, _ = _build_test_environment(
        bank_items=(b1,),
        book_items=(j1, j2),
    )
    view = build_reconciliation_view(BookkeepingQueries(state))

    h1 = ReconciliationHypothesis(
        id="hyp-large-1",
        eligibility=Eligibility.SELECTABLE,
        utility=600,
        bank_allocations=(BankAllocation(bank_item_id="b1", amount_units=str(large_amount)),),
        book_allocations=(BookAllocation(book_item_id="j1", amount_units=str(large_amount)),),
        state_revision=view.state_revision,
        generated_at=FIXED_TIME,
    )
    h2 = ReconciliationHypothesis(
        id="hyp-large-2",
        eligibility=Eligibility.SELECTABLE,
        utility=900,
        bank_allocations=(BankAllocation(bank_item_id="b1", amount_units=str(large_amount)),),
        book_allocations=(BookAllocation(book_item_id="j2", amount_units=str(large_amount)),),
        state_revision=view.state_revision,
        generated_at=FIXED_TIME,
    )

    # 900 * 5 * 10^15 = 4.5 * 10^18, sum = 7.5 * 10^18 > (1 << 62) - 1 (4.61 * 10^18)
    result = optimize_reconciliation(
        view=view,
        hypotheses=(h1, h2),
        solver_run_id="test-gcd-scaling",
    )
    assert len(result.selected_hypotheses) == 1
    assert result.selected_hypotheses[0].id == "hyp-large-2"
    assert result.objective_amount_weighted_semantic_value == 900 * large_amount
    assert result.objective_items_cleared == 2


# ======================================================================
# 25. Semantic Scoring Contract Corrections (Section 8 Regression Tests)
# ======================================================================


def test_regression_a_candidate_shape_alone_cannot_change_semantic_score():
    """
    Test A: Candidate shape alone cannot change semantic score.
    Same evidence: exact 1:1, partial 1:1 book, partial 1:1 bank, 1:N grouped,
    and N:1 grouped representations must not receive arbitrary structural
    bonuses (+250, +150, +100) or baselines (+400).
    When underlying evidence is identical (e.g. matching ref + same-day date), all receive
    the exact same semantic score (250).
    """
    common_date = date(2026, 1, 15)
    b_exact = factories.bank_item("b-exact", amount=100_000, reference="INV-A")
    j_exact = factories.book_item("j-exact", amount=100_000, reference="INV-A")

    b_part_bk = factories.bank_item("b-part-bk", amount=40_000, reference="INV-A")
    j_part_bk = factories.book_item("j-part-bk", amount=100_000, reference="INV-A")

    b_part_bnk = factories.bank_item("b-part-bnk", amount=100_000, reference="INV-A")
    j_part_bnk = factories.book_item("j-part-bnk", amount=40_000, reference="INV-A")

    b_1n = factories.bank_item("b-1n", amount=100_000, reference="INV-A")
    j_1n_a = factories.book_item("j-1n-a", amount=60_000, reference="INV-A")
    j_1n_b = factories.book_item("j-1n-b", amount=40_000, reference="INV-A")

    b_n1_a = factories.bank_item("b-n1-a", amount=60_000, reference="INV-A")
    b_n1_b = factories.bank_item("b-n1-b", amount=40_000, reference="INV-A")
    j_n1 = factories.book_item("j-n1", amount=100_000, reference="INV-A")

    _, _, state, _ = _build_test_environment(
        bank_items=(b_exact, b_part_bk, b_part_bnk, b_1n, b_n1_a, b_n1_b),
        book_items=(j_exact, j_part_bk, j_part_bnk, j_1n_a, j_1n_b, j_n1),
    )
    view = build_reconciliation_view(BookkeepingQueries(state))
    scorer = DefaultReconciliationScorer()

    c_exact = ReconciliationCandidate(
        candidate_id="c-exact",
        candidate_type=CandidateType.ONE_TO_ONE_EXACT,
        bank_allocations=(BankAllocation(bank_item_id="b-exact", amount_units="100000"),),
        book_allocations=(BookAllocation(book_item_id="j-exact", amount_units="100000"),),
        total_amount_units="100000",
    )
    c_part_bk = ReconciliationCandidate(
        candidate_id="c-part-bk",
        candidate_type=CandidateType.ONE_TO_ONE_PARTIAL_BOOK,
        bank_allocations=(BankAllocation(bank_item_id="b-part-bk", amount_units="40000"),),
        book_allocations=(BookAllocation(book_item_id="j-part-bk", amount_units="40000"),),
        total_amount_units="40000",
    )
    c_part_bnk = ReconciliationCandidate(
        candidate_id="c-part-bnk",
        candidate_type=CandidateType.ONE_TO_ONE_PARTIAL_BANK,
        bank_allocations=(BankAllocation(bank_item_id="b-part-bnk", amount_units="40000"),),
        book_allocations=(BookAllocation(book_item_id="j-part-bnk", amount_units="40000"),),
        total_amount_units="40000",
    )
    c_1n = ReconciliationCandidate(
        candidate_id="c-1n",
        candidate_type=CandidateType.ONE_TO_MANY,
        bank_allocations=(BankAllocation(bank_item_id="b-1n", amount_units="100000"),),
        book_allocations=(
            BookAllocation(book_item_id="j-1n-a", amount_units="60000"),
            BookAllocation(book_item_id="j-1n-b", amount_units="40000"),
        ),
        total_amount_units="100000",
    )
    c_n1 = ReconciliationCandidate(
        candidate_id="c-n1",
        candidate_type=CandidateType.MANY_TO_ONE,
        bank_allocations=(
            BankAllocation(bank_item_id="b-n1-a", amount_units="60000"),
            BankAllocation(bank_item_id="b-n1-b", amount_units="40000"),
        ),
        book_allocations=(BookAllocation(book_item_id="j-n1", amount_units="100000"),),
        total_amount_units="100000",
    )

    a_exact = scorer.score_candidate(c_exact, view)
    a_part_bk = scorer.score_candidate(c_part_bk, view)
    a_part_bnk = scorer.score_candidate(c_part_bnk, view)
    a_1n = scorer.score_candidate(c_1n, view)
    a_n1 = scorer.score_candidate(c_n1, view)

    # Under identical economic evidence (matching ref +200, same day date +50), all shapes have score 250
    assert a_exact.semantic_score == 250
    assert a_part_bk.semantic_score == 250
    assert a_part_bnk.semantic_score == 250
    assert a_1n.semantic_score == 250
    assert a_n1.semantic_score == 250


def test_regression_b_grouped_candidates_receive_temporal_evidence():
    """
    Test B: Grouped candidates receive temporal evidence.
    N:1 and 1:N candidates evaluate temporal proximity over their component pairs
    rather than receiving 0 date bonus because of their grouped shape.
    """
    b1 = BankItem(
        id="b1",
        bank_account_id="account-1",
        date=date(2026, 1, 15),
        amount_units="60000",
        direction=Direction.BANK_OUTFLOW,
        currency="MAD",
        description="b1",
        reference="BATCH-1",
    )
    b2 = BankItem(
        id="b2",
        bank_account_id="account-1",
        date=date(2026, 1, 17),  # 2 days after book item (<= 3d -> +25)
        amount_units="40000",
        direction=Direction.BANK_OUTFLOW,
        currency="MAD",
        description="b2",
        reference="BATCH-1",
    )
    j1 = BookItem(
        id="j1",
        origin_period="2026-01",
        date=date(2026, 1, 15),
        amount_units="100000",
        direction=Direction.BOOK_BANK_CREDIT,
        currency="MAD",
        description="j1",
        reference="BATCH-1",
    )

    _, _, state, _ = _build_test_environment(
        bank_items=(b1, b2),
        book_items=(j1,),
    )
    view = build_reconciliation_view(BookkeepingQueries(state))
    scorer = DefaultReconciliationScorer()

    c_n1 = ReconciliationCandidate(
        candidate_id="c-n1",
        candidate_type=CandidateType.MANY_TO_ONE,
        bank_allocations=(
            BankAllocation(bank_item_id="b1", amount_units="60000"),
            BankAllocation(bank_item_id="b2", amount_units="40000"),
        ),
        book_allocations=(BookAllocation(book_item_id="j1", amount_units="100000"),),
        total_amount_units="100000",
    )

    assessment = scorer.score_candidate(c_n1, view)
    # Pair 1: ref_match +200, same day +50 -> 250 * 60_000 = 15_000_000
    # Pair 2: ref_match +200, within 3d +25 -> 225 * 40_000 = 9_000_000
    # Total semantic value = 24_000_000; average score = 24_000_000 // 100_000 = 240
    assert assessment.semantic_value == 24_000_000
    assert assessment.semantic_score == 240
    assert len(assessment.pairwise_assessments) == 2
    assert assessment.pairwise_assessments[0].score == 250
    assert "date:same_day" in assessment.pairwise_assessments[0].evidence_tags
    assert assessment.pairwise_assessments[1].score == 225
    assert "date:within_3d" in assessment.pairwise_assessments[1].evidence_tags


def test_regression_c_packaging_invariance():
    """
    Test C: Packaging invariance.
    Same pairwise allocation + same pairwise evidence represented as:
        one N:1 grouped hypothesis
    vs
        multiple partial 1:1 hypotheses
    must produce identical total Phase-3 semantic value down to the exact integer.
    """
    b1 = BankItem(
        id="b1",
        bank_account_id="account-1",
        date=date(2026, 1, 15),
        amount_units="30000",
        direction=Direction.BANK_OUTFLOW,
        currency="MAD",
        description="b1",
        reference="INV-MATCH",
    )
    b2 = BankItem(
        id="b2",
        bank_account_id="account-1",
        date=date(2026, 1, 16),
        amount_units="70000",
        direction=Direction.BANK_OUTFLOW,
        currency="MAD",
        description="Payment to VendorAcme",
    )
    cp_acme = Counterparty(
        id="cp-acme",
        name="VendorAcme",
        counterparty_type=CounterpartyType.SUPPLIER,
    )
    j1 = BookItem(
        id="j1",
        origin_period="2026-01",
        date=date(2026, 1, 15),
        amount_units="100000",
        direction=Direction.BOOK_BANK_CREDIT,
        currency="MAD",
        description="j1",
        reference="INV-MATCH",
        counterparty_id="cp-acme",
    )

    _, _, state, _ = _build_test_environment(
        bank_items=(b1, b2),
        book_items=(j1,),
        counterparties=(cp_acme,),
    )
    view = build_reconciliation_view(
        BookkeepingQueries(state),
        config=ReconciliationViewConfig(allow_partial_book=True),
    )
    scorer = DefaultReconciliationScorer()

    # Representation 1: One N:1 grouped hypothesis
    c_grouped = ReconciliationCandidate(
        candidate_id="c-grouped",
        candidate_type=CandidateType.MANY_TO_ONE,
        bank_allocations=(
            BankAllocation(bank_item_id="b1", amount_units="30000"),
            BankAllocation(bank_item_id="b2", amount_units="70000"),
        ),
        book_allocations=(BookAllocation(book_item_id="j1", amount_units="100000"),),
        total_amount_units="100000",
    )
    a_grouped = scorer.score_candidate(c_grouped, view)
    # Pair 1: ref match (+200) + same day (+50) = 250 * 30_000 = 7_500_000
    # Pair 2: cp match (+100) + within 3d (+25) = 125 * 70_000 = 8_750_000
    # Total = 16_250_000
    assert a_grouped.semantic_value == 16_250_000

    hyp_grouped = c_grouped.to_hypothesis(
        state_revision=view.state_revision,
        semantic_score=a_grouped.semantic_score,
        semantic_value=a_grouped.semantic_value,
    )
    res_grouped = optimize_reconciliation(
        view=view, hypotheses=(hyp_grouped,), solver_run_id="run-c-grouped"
    )
    assert res_grouped.objective_amount_weighted_semantic_value == 16_250_000

    # Representation 2: Two partial 1:1 hypotheses
    c_part1 = ReconciliationCandidate(
        candidate_id="c-part1",
        candidate_type=CandidateType.ONE_TO_ONE_PARTIAL_BOOK,
        bank_allocations=(BankAllocation(bank_item_id="b1", amount_units="30000"),),
        book_allocations=(BookAllocation(book_item_id="j1", amount_units="30000"),),
        total_amount_units="30000",
    )
    c_part2 = ReconciliationCandidate(
        candidate_id="c-part2",
        candidate_type=CandidateType.ONE_TO_ONE_PARTIAL_BOOK,
        bank_allocations=(BankAllocation(bank_item_id="b2", amount_units="70000"),),
        book_allocations=(BookAllocation(book_item_id="j1", amount_units="70000"),),
        total_amount_units="70000",
    )
    a_part1 = scorer.score_candidate(c_part1, view)
    a_part2 = scorer.score_candidate(c_part2, view)
    assert a_part1.semantic_value == 7_500_000
    assert a_part2.semantic_value == 8_750_000

    hyp_part1 = c_part1.to_hypothesis(
        state_revision=view.state_revision,
        semantic_score=a_part1.semantic_score,
        semantic_value=a_part1.semantic_value,
    )
    hyp_part2 = c_part2.to_hypothesis(
        state_revision=view.state_revision,
        semantic_score=a_part2.semantic_score,
        semantic_value=a_part2.semantic_value,
    )
    res_partial = optimize_reconciliation(
        view=view, hypotheses=(hyp_part1, hyp_part2), solver_run_id="run-c-partial"
    )
    assert res_partial.objective_amount_weighted_semantic_value == 16_250_000

    # Total Phase-3 semantic value is exactly equal between grouped and fragmented
    assert (
        res_grouped.objective_amount_weighted_semantic_value
        == res_partial.objective_amount_weighted_semantic_value
    )


def test_regression_d_no_evidence_feasible_candidates_tie_semantically():
    """
    Test D: No-evidence feasible candidates tie semantically rather than receiving
    candidate-type preference (+250 vs +150 vs +100).
    """
    # 20 days apart -> 0 temporal evidence, no ref, no counterparty
    b1 = BankItem(
        id="b1",
        bank_account_id="account-1",
        date=date(2026, 1, 1),
        amount_units="100000",
        direction=Direction.BANK_OUTFLOW,
        currency="MAD",
        description="b1",
    )
    j1 = BookItem(
        id="j1",
        origin_period="2026-01",
        date=date(2026, 1, 25),
        amount_units="100000",
        direction=Direction.BOOK_BANK_CREDIT,
        currency="MAD",
        description="j1",
    )
    b2a = BankItem(
        id="b2a",
        bank_account_id="account-1",
        date=date(2026, 1, 1),
        amount_units="50000",
        direction=Direction.BANK_OUTFLOW,
        currency="MAD",
        description="b2a",
    )
    b2b = BankItem(
        id="b2b",
        bank_account_id="account-1",
        date=date(2026, 1, 1),
        amount_units="50000",
        direction=Direction.BANK_OUTFLOW,
        currency="MAD",
        description="b2b",
    )
    j2 = BookItem(
        id="j2",
        origin_period="2026-01",
        date=date(2026, 1, 25),
        amount_units="100000",
        direction=Direction.BOOK_BANK_CREDIT,
        currency="MAD",
        description="j2",
    )

    _, _, state, _ = _build_test_environment(
        bank_items=(b1, b2a, b2b),
        book_items=(j1, j2),
    )
    view = build_reconciliation_view(BookkeepingQueries(state))
    scorer = DefaultReconciliationScorer()

    c_1_1 = ReconciliationCandidate(
        candidate_id="c-1-1",
        candidate_type=CandidateType.ONE_TO_ONE_EXACT,
        bank_allocations=(BankAllocation(bank_item_id="b1", amount_units="100000"),),
        book_allocations=(BookAllocation(book_item_id="j1", amount_units="100000"),),
        total_amount_units="100000",
    )
    c_n_1 = ReconciliationCandidate(
        candidate_id="c-n-1",
        candidate_type=CandidateType.MANY_TO_ONE,
        bank_allocations=(
            BankAllocation(bank_item_id="b2a", amount_units="50000"),
            BankAllocation(bank_item_id="b2b", amount_units="50000"),
        ),
        book_allocations=(BookAllocation(book_item_id="j2", amount_units="100000"),),
        total_amount_units="100000",
    )

    a_1_1 = scorer.score_candidate(c_1_1, view)
    a_n_1 = scorer.score_candidate(c_n_1, view)

    # Both have 0 evidence and neutral score 0
    assert a_1_1.semantic_score == 0
    assert a_n_1.semantic_score == 0
    assert a_1_1.semantic_value == 0
    assert a_n_1.semantic_value == 0


def test_regression_e_strong_reference_evidence_beats_weak_or_no_evidence():
    """
    Test E: Strong reference evidence beats weak/no evidence regardless of candidate
    shape when Phase 1 and Phase 2 tie.
    """
    b1 = BankItem(
        id="b1",
        bank_account_id="account-1",
        date=date(2026, 1, 1),
        amount_units="100000",
        direction=Direction.BANK_OUTFLOW,
        currency="MAD",
        description="Bank item 1",
        reference="TARGET-REF",
    )
    b2 = BankItem(
        id="b2",
        bank_account_id="account-1",
        date=date(2026, 1, 1),
        amount_units="100000",
        direction=Direction.BANK_OUTFLOW,
        currency="MAD",
        description="Bank item 2 without ref",
    )
    j1 = BookItem(
        id="j1",
        origin_period="2026-01",
        date=date(2026, 1, 20),
        amount_units="100000",
        direction=Direction.BOOK_BANK_CREDIT,
        currency="MAD",
        description="Book item 1",
        reference="TARGET-REF",
    )

    _, _, state, _ = _build_test_environment(
        bank_items=(b1, b2),
        book_items=(j1,),
    )
    view = build_reconciliation_view(BookkeepingQueries(state))
    scorer = DefaultReconciliationScorer()

    c_strong = ReconciliationCandidate(
        candidate_id="c-strong",
        candidate_type=CandidateType.ONE_TO_ONE_EXACT,
        bank_allocations=(BankAllocation(bank_item_id="b1", amount_units="100000"),),
        book_allocations=(BookAllocation(book_item_id="j1", amount_units="100000"),),
        total_amount_units="100000",
    )
    c_weak = ReconciliationCandidate(
        candidate_id="c-weak",
        candidate_type=CandidateType.ONE_TO_ONE_EXACT,
        bank_allocations=(BankAllocation(bank_item_id="b2", amount_units="100000"),),
        book_allocations=(BookAllocation(book_item_id="j1", amount_units="100000"),),
        total_amount_units="100000",
    )

    h_strong = scorer.score_candidate(c_strong, view)
    h_weak = scorer.score_candidate(c_weak, view)
    assert h_strong.semantic_score == 200  # ref_match
    assert h_weak.semantic_score == 0

    hyp_strong = c_strong.to_hypothesis(
        state_revision=view.state_revision,
        semantic_score=h_strong.semantic_score,
        semantic_value=h_strong.semantic_value,
    )
    hyp_weak = c_weak.to_hypothesis(
        state_revision=view.state_revision,
        semantic_score=h_weak.semantic_score,
        semantic_value=h_weak.semantic_value,
    )

    result = optimize_reconciliation(
        view=view, hypotheses=(hyp_strong, hyp_weak), solver_run_id="run-e"
    )
    assert len(result.selected_hypotheses) == 1
    assert result.selected_hypotheses[0].id == hyp_strong.id


def test_regression_f_strong_counterparty_evidence_beats_weak_evidence():
    """
    Test F: Strong counterparty evidence behaves similarly:
    candidate with counterparty match beats candidate without counterparty match under tied Phase 1 & 2.
    """
    b1 = BankItem(
        id="b1",
        bank_account_id="account-1",
        date=date(2026, 1, 1),
        amount_units="100000",
        direction=Direction.BANK_OUTFLOW,
        currency="MAD",
        description="Payment to AlphaCorp",
    )
    b2 = BankItem(
        id="b2",
        bank_account_id="account-1",
        date=date(2026, 1, 1),
        amount_units="100000",
        direction=Direction.BANK_OUTFLOW,
        currency="MAD",
        description="Generic withdrawal",
    )
    cp_alpha = Counterparty(
        id="cp-alpha",
        name="AlphaCorp",
        counterparty_type=CounterpartyType.SUPPLIER,
    )
    j1 = BookItem(
        id="j1",
        origin_period="2026-01",
        date=date(2026, 1, 20),
        amount_units="100000",
        direction=Direction.BOOK_BANK_CREDIT,
        currency="MAD",
        description="Invoice from AlphaCorp",
        counterparty_id="cp-alpha",
    )

    _, _, state, _ = _build_test_environment(
        bank_items=(b1, b2),
        book_items=(j1,),
        counterparties=(cp_alpha,),
    )
    view = build_reconciliation_view(BookkeepingQueries(state))
    scorer = DefaultReconciliationScorer()

    c1 = ReconciliationCandidate(
        candidate_id="c1",
        candidate_type=CandidateType.ONE_TO_ONE_EXACT,
        bank_allocations=(BankAllocation(bank_item_id="b1", amount_units="100000"),),
        book_allocations=(BookAllocation(book_item_id="j1", amount_units="100000"),),
        total_amount_units="100000",
    )
    c2 = ReconciliationCandidate(
        candidate_id="c2",
        candidate_type=CandidateType.ONE_TO_ONE_EXACT,
        bank_allocations=(BankAllocation(bank_item_id="b2", amount_units="100000"),),
        book_allocations=(BookAllocation(book_item_id="j1", amount_units="100000"),),
        total_amount_units="100000",
    )

    a1 = scorer.score_candidate(c1, view)
    a2 = scorer.score_candidate(c2, view)
    assert a1.semantic_score == 100  # cp_match
    assert a2.semantic_score == 0

    hyp1 = c1.to_hypothesis(
        state_revision=view.state_revision,
        semantic_score=a1.semantic_score,
        semantic_value=a1.semantic_value,
    )
    hyp2 = c2.to_hypothesis(
        state_revision=view.state_revision,
        semantic_score=a2.semantic_score,
        semantic_value=a2.semantic_value,
    )

    result = optimize_reconciliation(
        view=view, hypotheses=(hyp1, hyp2), solver_run_id="run-f"
    )
    assert len(result.selected_hypotheses) == 1
    assert result.selected_hypotheses[0].id == hyp1.id


def test_regression_g_structural_shape_alone_cannot_redirect_money_alpha_beta():
    """
    Test G: Structural shape alone cannot redirect money to a different counterparty.
    Uses the concrete Alpha/Beta counterexample from the audit and proves that,
    with equal underlying evidence, topology alone cannot make Beta win.
    """
    common_date = date(2026, 1, 15)
    # Bank items for Alpha
    b_alpha_1 = BankItem(
        id="b-alpha-1",
        bank_account_id="account-1",
        date=common_date,
        amount_units="10000",
        direction=Direction.BANK_OUTFLOW,
        currency="MAD",
        description="Payment Alpha 1",
    )
    b_alpha_2 = BankItem(
        id="b-alpha-2",
        bank_account_id="account-1",
        date=common_date,
        amount_units="10000",
        direction=Direction.BANK_OUTFLOW,
        currency="MAD",
        description="Payment Alpha 2",
    )
    cp_alpha = Counterparty(
        id="cp-alpha",
        name="Alpha",
        counterparty_type=CounterpartyType.SUPPLIER,
    )
    cp_beta = Counterparty(
        id="cp-beta",
        name="Beta",
        counterparty_type=CounterpartyType.SUPPLIER,
    )
    # Book item for Alpha (grouped N:1)
    j_alpha = BookItem(
        id="j-alpha",
        origin_period="2026-01",
        date=common_date,
        amount_units="20000",
        direction=Direction.BOOK_BANK_CREDIT,
        currency="MAD",
        description="Invoice Alpha",
        counterparty_id="cp-alpha",
    )
    # Book item for Beta (competing for b-alpha-1 as 1:1)
    j_beta = BookItem(
        id="j-beta",
        origin_period="2026-01",
        date=common_date,
        amount_units="10000",
        direction=Direction.BOOK_BANK_CREDIT,
        currency="MAD",
        description="Invoice Beta",
        counterparty_id="cp-beta",
    )

    _, _, state, _ = _build_test_environment(
        bank_items=(b_alpha_1, b_alpha_2),
        book_items=(j_alpha, j_beta),
        counterparties=(cp_alpha, cp_beta),
    )
    view = build_reconciliation_view(BookkeepingQueries(state))
    scorer = DefaultReconciliationScorer()

    c_alpha_n1 = ReconciliationCandidate(
        candidate_id="c-alpha-n1",
        candidate_type=CandidateType.MANY_TO_ONE,
        bank_allocations=(
            BankAllocation(bank_item_id="b-alpha-1", amount_units="10000"),
            BankAllocation(bank_item_id="b-alpha-2", amount_units="10000"),
        ),
        book_allocations=(BookAllocation(book_item_id="j-alpha", amount_units="20000"),),
        total_amount_units="20000",
    )
    c_beta_11 = ReconciliationCandidate(
        candidate_id="c-beta-11",
        candidate_type=CandidateType.ONE_TO_ONE_EXACT,
        bank_allocations=(BankAllocation(bank_item_id="b-alpha-1", amount_units="10000"),),
        book_allocations=(BookAllocation(book_item_id="j-beta", amount_units="10000"),),
        total_amount_units="10000",
    )

    a_alpha = scorer.score_candidate(c_alpha_n1, view)
    a_beta = scorer.score_candidate(c_beta_11, view)

    # Alpha: cp_match (+100) + same_day (+50) = 150 (SUPPORTED)
    # Beta: no cp_match, no ref -> INSUFFICIENT_EVIDENCE -> score 0
    assert a_alpha.semantic_score == 150
    assert a_alpha.admissibility == SemanticAdmissibility.SUPPORTED
    assert a_beta.semantic_score == 0
    assert a_beta.admissibility == SemanticAdmissibility.INSUFFICIENT_EVIDENCE

    hyp_alpha = c_alpha_n1.to_hypothesis(
        state_revision=view.state_revision,
        semantic_score=a_alpha.semantic_score,
        semantic_value=a_alpha.semantic_value,
        admissibility=a_alpha.admissibility,
        eligibility=Eligibility.SELECTABLE,
    )
    hyp_beta = c_beta_11.to_hypothesis(
        state_revision=view.state_revision,
        semantic_score=a_beta.semantic_score,
        semantic_value=a_beta.semantic_value,
        admissibility=a_beta.admissibility,
        eligibility=Eligibility.INELIGIBLE,
    )

    result = optimize_reconciliation(
        view=view, hypotheses=(hyp_alpha, hyp_beta), solver_run_id="run-g"
    )
    assert len(result.selected_hypotheses) == 1
    assert result.selected_hypotheses[0].id == hyp_alpha.id


def test_regression_h_scenario_c_pairwise_truth_still_passes():
    """
    Test H: Existing Scenario C pairwise truth still passes with corrected scoring.
    """
    from bookkeeping_state_eval.scenarios.catalog import get_scenario
    from bookkeeping_state_eval.scenarios.runner import ScenarioRunner

    scenario_c = get_scenario("scenario_c_many_to_one")
    runner = ScenarioRunner()
    result = runner.run(scenario_c)
    assert result.is_pass is True


def test_regression_i_scenario_q_passes_due_to_semantic_value_priority():
    """
    Test I: Scenario Q passes because Phase 2 amount-weighted semantic value resolves
    the competition in favor of 1:N grouped match (which has affirmative matching references)
    over 1:1 decoy (which lacks reference evidence), before Phase 3 items cleared is reached.
    """
    from bookkeeping_state_eval.scenarios.catalog import get_scenario
    from bookkeeping_state_eval.scenarios.runner import ScenarioRunner

    scenario_q = get_scenario("scenario_q_optimizer_competition")
    runner = ScenarioRunner()
    result = runner.run(scenario_q)
    assert result.is_pass is True
    assert result.expected_truth_verdict.is_match is True

    # Verify directly with the optimizer that 1:N wins due to Phase 2 semantic value
    b1 = scenario_q.bank_items[0]
    j_single = [j for j in scenario_q.book_items if j.id == "book-single"][0]
    j_group_1 = [j for j in scenario_q.book_items if j.id == "book-group-1"][0]
    j_group_2 = [j for j in scenario_q.book_items if j.id == "book-group-2"][0]

    _, _, state, _ = _build_test_environment(
        bank_items=(b1,),
        book_items=(j_single, j_group_1, j_group_2),
    )
    view = build_reconciliation_view(BookkeepingQueries(state))
    scorer = DefaultReconciliationScorer()

    c_1_1 = ReconciliationCandidate(
        candidate_id="c-1-1",
        candidate_type=CandidateType.ONE_TO_ONE_EXACT,
        bank_allocations=(BankAllocation(bank_item_id=b1.id, amount_units="100000"),),
        book_allocations=(BookAllocation(book_item_id=j_single.id, amount_units="100000"),),
        total_amount_units="100000",
    )
    c_1_n = ReconciliationCandidate(
        candidate_id="c-1-n",
        candidate_type=CandidateType.ONE_TO_MANY,
        bank_allocations=(BankAllocation(bank_item_id=b1.id, amount_units="100000"),),
        book_allocations=(
            BookAllocation(book_item_id=j_group_1.id, amount_units="60000"),
            BookAllocation(book_item_id=j_group_2.id, amount_units="40000"),
        ),
        total_amount_units="100000",
    )
    a_1_1 = scorer.score_candidate(c_1_1, view)
    a_1_n = scorer.score_candidate(c_1_n, view)
    assert a_1_n.admissibility == SemanticAdmissibility.SUPPORTED
    assert a_1_n.semantic_score > 0
    assert a_1_1.admissibility == SemanticAdmissibility.INSUFFICIENT_EVIDENCE
    assert a_1_1.semantic_score == 0

    hyp_1_1 = c_1_1.to_hypothesis(
        state_revision=view.state_revision,
        semantic_score=a_1_1.semantic_score,
        semantic_value=a_1_1.semantic_value,
        admissibility=a_1_1.admissibility,
        eligibility=Eligibility.SELECTABLE,  # Even if forced selectable
    )
    hyp_1_n = c_1_n.to_hypothesis(
        state_revision=view.state_revision,
        semantic_score=a_1_n.semantic_score,
        semantic_value=a_1_n.semantic_value,
        admissibility=a_1_n.admissibility,
        eligibility=Eligibility.SELECTABLE,
    )
    opt_res = optimize_reconciliation(
        view=view,
        hypotheses=(hyp_1_1, hyp_1_n),
        solver_run_id="run-q-opt",
    )
    assert opt_res.objective_amount_weighted_semantic_value == a_1_n.semantic_value
    assert len(opt_res.selected_hypotheses) == 1
    assert opt_res.selected_hypotheses[0].id == hyp_1_n.id


def test_regression_j_provenance_and_rehydration_preserve_semantic_score():
    """
    Test J: Provenance and rehydration tests still pass and durably record the semantic
    score and admissibility used in selection.
    """
    b1 = factories.bank_item("b1", amount=500_000, reference="INV-500")
    j1 = factories.book_item("j1", amount=500_000, reference="INV-500")

    repo, hydrator, state, engine = _build_test_environment(
        bank_items=(b1,),
        book_items=(j1,),
    )

    service = ReconciliationService()
    plan = service.plan(BookkeepingQueries(state))
    engine.apply_batch(state=state, batch=plan.to_batch())

    rec = tuple(state.reconciliations.values())[0]
    # Scored with ref_match (+200) + same_day (+50) = 250
    assert rec.source_hypothesis_utility == 250
    assert rec.source_hypothesis_semantic_score == 250
    assert rec.source_hypothesis_admissibility == SemanticAdmissibility.SUPPORTED
    assert rec.hypothesis_provenance is not None
    assert rec.hypothesis_provenance.utility == 250
    assert rec.hypothesis_provenance.semantic_score == 250
    assert rec.hypothesis_provenance.admissibility == SemanticAdmissibility.SUPPORTED

    # Rehydrate
    fresh_state = hydrator.hydrate(company_id=state.context.company_id)
    rehydrated_rec = tuple(fresh_state.reconciliations.values())[0]
    assert rehydrated_rec.source_hypothesis_utility == 250
    assert rehydrated_rec.source_hypothesis_semantic_score == 250
    assert rehydrated_rec.source_hypothesis_admissibility == SemanticAdmissibility.SUPPORTED
    assert rehydrated_rec.hypothesis_provenance is not None
    assert rehydrated_rec.hypothesis_provenance.utility == 250
    assert rehydrated_rec.hypothesis_provenance.semantic_score == 250
    assert rehydrated_rec.hypothesis_provenance.admissibility == SemanticAdmissibility.SUPPORTED


def test_semantic_score_contract_specification():
    """
    Assert the semantic-score contract specification:
    1. utility field contract docstring specifies cardinal semantic preference, not probability,
       not raw LLM confidence, and not a claim that 800 is literally twice as correct as 400.
    2. DefaultReconciliationScorer is documented as an eval/reference provider with hand-authored weights.
    3. semantic_score property returns utility.
    4. exact_semantic_value returns exact sum if provided, or utility * total_bank_allocation_int.
    """
    desc = ReconciliationHypothesis.model_fields["utility"].description or ""
    assert "Globally comparable cardinal semantic-preference score" in desc
    assert "not a probability" in desc
    assert "not raw LLM confidence" in desc
    assert "not a claim that 800 is literally twice as correct as 400" in desc
    assert "packaging invariant" in desc
    assert "empirically calibrated" in desc

    scorer_doc = DefaultReconciliationScorer.__doc__ or ""
    assert "eval/reference" in scorer_doc
    assert "hand-authored evidence weights" in scorer_doc
    assert "NOT" in scorer_doc and "production-calibrated" in scorer_doc


# ======================================================================
# 26. Semantic Admissibility & Gate Tests (A through L)
# ======================================================================


def test_semantic_admissibility_a_exact_amount_same_day_no_identity_evidence():
    """
    Test A: exact amount + same day with no identity evidence => INSUFFICIENT_EVIDENCE
    """
    b = factories.bank_item("b1", amount=100_000, description="Unknown deposit", date_val=date(2026, 1, 15))
    j = factories.book_item("j1", amount=100_000, description="Unknown receipt", date_val=date(2026, 1, 15))
    _, _, state, _ = _build_test_environment(bank_items=(b,), book_items=(j,))
    view = build_reconciliation_view(BookkeepingQueries(state))

    b_view = view.get_bank_item("b1")
    j_view = view.get_book_item("j1")
    score, tags, reasons, admissibility = score_reconciliation_pair(b_view, j_view)
    assert admissibility == SemanticAdmissibility.INSUFFICIENT_EVIDENCE
    assert score == 0


def test_semantic_admissibility_b_matching_reference():
    """
    Test B: matching reference => SUPPORTED
    """
    b = factories.bank_item("b1", amount=100_000, reference="INV-42")
    j = factories.book_item("j1", amount=100_000, reference="INV-42")
    _, _, state, _ = _build_test_environment(bank_items=(b,), book_items=(j,))
    view = build_reconciliation_view(BookkeepingQueries(state))

    b_view = view.get_bank_item("b1")
    j_view = view.get_book_item("j1")
    score, tags, reasons, admissibility = score_reconciliation_pair(b_view, j_view)
    assert admissibility == SemanticAdmissibility.SUPPORTED
    assert score >= 200
    assert "ref:INV-42" in tags


def test_semantic_admissibility_c_verified_counterparty_correspondence():
    """
    Test C: verified counterparty correspondence => SUPPORTED
    """
    cp = Counterparty(id="cp-aws", name="AWS", counterparty_type=CounterpartyType.SUPPLIER)
    b = factories.bank_item("b1", amount=10_000, description="PRLV AMAZON WEB SERVICES")
    j = factories.book_item("j1", amount=10_000, description="AWS Cloud services", counterparty_id="cp-aws")
    _, _, state, _ = _build_test_environment(bank_items=(b,), book_items=(j,), counterparties=(cp,))
    view = build_reconciliation_view(BookkeepingQueries(state))

    b_view = view.get_bank_item("b1")
    j_view = view.get_book_item("j1")
    score, tags, reasons, admissibility = score_reconciliation_pair(b_view, j_view)
    assert admissibility == SemanticAdmissibility.SUPPORTED
    assert score >= 100


def test_semantic_admissibility_d_contradictory_identity_evidence():
    """
    Test D: explicit contradictory identity evidence => CONTRADICTED
    """
    cp_a = Counterparty(id="cp-a", name="Alpha", counterparty_type=CounterpartyType.SUPPLIER)
    cp_b = Counterparty(id="cp-b", name="Beta", counterparty_type=CounterpartyType.SUPPLIER)
    b = factories.bank_item("b1", amount=10_000, description="Payment to Alpha", reference="REF-ALPHA")
    j = factories.book_item("j1", amount=10_000, description="Bill from Beta", reference="REF-BETA", counterparty_id="cp-b")
    _, _, state, _ = _build_test_environment(bank_items=(b,), book_items=(j,), counterparties=(cp_a, cp_b))
    view = build_reconciliation_view(BookkeepingQueries(state))

    b_view = view.get_bank_item("b1")
    j_view = view.get_book_item("j1")
    score, tags, reasons, admissibility = score_reconciliation_pair(b_view, j_view)
    assert admissibility == SemanticAdmissibility.CONTRADICTED
    assert score == 0


def test_semantic_admissibility_e_date_proximity_cannot_alone_establish_supported():
    """
    Test E: date proximity affects semantic score but cannot alone establish SUPPORTED
    """
    b = factories.bank_item("b1", amount=10_000, date_val=date(2026, 1, 15), description="Generic bank")
    j = factories.book_item("j1", amount=10_000, date_val=date(2026, 1, 15), description="Generic book")
    _, _, state, _ = _build_test_environment(bank_items=(b,), book_items=(j,))
    view = build_reconciliation_view(BookkeepingQueries(state))
    scorer = DefaultReconciliationScorer()

    c = ReconciliationCandidate(
        candidate_id="c1",
        candidate_type=CandidateType.ONE_TO_ONE_EXACT,
        bank_allocations=(BankAllocation(bank_item_id="b1", amount_units="10000"),),
        book_allocations=(BookAllocation(book_item_id="j1", amount_units="10000"),),
        total_amount_units="10000",
    )
    assessment = scorer.score_candidate(c, view)
    assert assessment.admissibility == SemanticAdmissibility.INSUFFICIENT_EVIDENCE
    assert assessment.semantic_score == 0
    assert assessment.semantic_value == 0


def test_semantic_admissibility_f_one_contradicted_pair_makes_candidate_contradicted():
    """
    Test F: candidate with one CONTRADICTED pair => candidate CONTRADICTED
    """
    b1 = factories.bank_item("b1", amount=100_000, reference="BATCH-1", description="Client Gamma")
    j1 = factories.book_item("j1", amount=60_000, reference="BATCH-1", description="Client Gamma")  # SUPPORTED
    j2 = factories.book_item("j2", amount=40_000, reference="OTHER-REF", description="Client Zeta")  # CONTRADICTED
    _, _, state, _ = _build_test_environment(bank_items=(b1,), book_items=(j1, j2))
    view = build_reconciliation_view(BookkeepingQueries(state))
    scorer = DefaultReconciliationScorer()

    c = ReconciliationCandidate(
        candidate_id="c1",
        candidate_type=CandidateType.ONE_TO_MANY,
        bank_allocations=(BankAllocation(bank_item_id="b1", amount_units="100000"),),
        book_allocations=(
            BookAllocation(book_item_id="j1", amount_units="60000"),
            BookAllocation(book_item_id="j2", amount_units="40000"),
        ),
        total_amount_units="100000",
    )
    assessment = scorer.score_candidate(c, view)
    assert assessment.admissibility == SemanticAdmissibility.CONTRADICTED
    assert assessment.semantic_score == 0
    assert assessment.semantic_value == 0


def test_semantic_admissibility_g_candidate_with_supported_and_insufficient_pairs_is_insufficient():
    """
    Test G: candidate with supported + insufficient pairs => candidate INSUFFICIENT_EVIDENCE
    """
    b1 = factories.bank_item("b1", amount=100_000, reference="BATCH-1", description="Client Gamma")
    j1 = factories.book_item("j1", amount=60_000, reference="BATCH-1", description="Client Gamma")  # SUPPORTED
    j2 = factories.book_item("j2", amount=40_000, description="Generic deposit")  # INSUFFICIENT
    _, _, state, _ = _build_test_environment(bank_items=(b1,), book_items=(j1, j2))
    view = build_reconciliation_view(BookkeepingQueries(state))
    scorer = DefaultReconciliationScorer()

    c = ReconciliationCandidate(
        candidate_id="c1",
        candidate_type=CandidateType.ONE_TO_MANY,
        bank_allocations=(BankAllocation(bank_item_id="b1", amount_units="100000"),),
        book_allocations=(
            BookAllocation(book_item_id="j1", amount_units="60000"),
            BookAllocation(book_item_id="j2", amount_units="40000"),
        ),
        total_amount_units="100000",
    )
    assessment = scorer.score_candidate(c, view)
    assert assessment.admissibility == SemanticAdmissibility.INSUFFICIENT_EVIDENCE
    assert assessment.semantic_score == 0
    assert assessment.semantic_value == 0


def test_semantic_admissibility_h_all_supported_pairs_is_supported():
    """
    Test H: all supported pairs => candidate SUPPORTED
    """
    b1 = factories.bank_item("b1", amount=100_000, reference="BATCH-1", description="Client Gamma")
    j1 = factories.book_item("j1", amount=60_000, reference="BATCH-1", description="Client Gamma")
    j2 = factories.book_item("j2", amount=40_000, reference="BATCH-1", description="Client Gamma")
    _, _, state, _ = _build_test_environment(bank_items=(b1,), book_items=(j1, j2))
    view = build_reconciliation_view(BookkeepingQueries(state))
    scorer = DefaultReconciliationScorer()

    c = ReconciliationCandidate(
        candidate_id="c1",
        candidate_type=CandidateType.ONE_TO_MANY,
        bank_allocations=(BankAllocation(bank_item_id="b1", amount_units="100000"),),
        book_allocations=(
            BookAllocation(book_item_id="j1", amount_units="60000"),
            BookAllocation(book_item_id="j2", amount_units="40000"),
        ),
        total_amount_units="100000",
    )
    assessment = scorer.score_candidate(c, view)
    assert assessment.admissibility == SemanticAdmissibility.SUPPORTED
    assert assessment.semantic_score > 0
    assert assessment.semantic_value > 0


def test_semantic_admissibility_i_packaging_equivalent_grouped_fragmented():
    """
    Test I: packaging-equivalent grouped/fragmented representations produce equivalent
    pairwise admissibility behavior
    """
    b1 = factories.bank_item("b1", amount=60_000, reference="SUP-1", description="Fournisseur Atlas")
    b2 = factories.bank_item("b2", amount=40_000, reference="SUP-1", description="Fournisseur Atlas")
    j = factories.book_item("j1", amount=100_000, reference="SUP-1", description="Fournisseur Atlas")

    _, _, state, _ = _build_test_environment(bank_items=(b1, b2), book_items=(j,))
    view = build_reconciliation_view(BookkeepingQueries(state))
    scorer = DefaultReconciliationScorer()

    c_grouped = ReconciliationCandidate(
        candidate_id="c-grp",
        candidate_type=CandidateType.MANY_TO_ONE,
        bank_allocations=(
            BankAllocation(bank_item_id="b1", amount_units="60000"),
            BankAllocation(bank_item_id="b2", amount_units="40000"),
        ),
        book_allocations=(BookAllocation(book_item_id="j1", amount_units="100000"),),
        total_amount_units="100000",
    )
    c_part1 = ReconciliationCandidate(
        candidate_id="c-p1",
        candidate_type=CandidateType.ONE_TO_ONE_PARTIAL_BOOK,
        bank_allocations=(BankAllocation(bank_item_id="b1", amount_units="60000"),),
        book_allocations=(BookAllocation(book_item_id="j1", amount_units="60000"),),
        total_amount_units="60000",
    )
    c_part2 = ReconciliationCandidate(
        candidate_id="c-p2",
        candidate_type=CandidateType.ONE_TO_ONE_PARTIAL_BOOK,
        bank_allocations=(BankAllocation(bank_item_id="b2", amount_units="40000"),),
        book_allocations=(BookAllocation(book_item_id="j1", amount_units="40000"),),
        total_amount_units="40000",
    )

    a_grp = scorer.score_candidate(c_grouped, view)
    a_p1 = scorer.score_candidate(c_part1, view)
    a_p2 = scorer.score_candidate(c_part2, view)

    assert a_grp.admissibility == SemanticAdmissibility.SUPPORTED
    assert a_p1.admissibility == SemanticAdmissibility.SUPPORTED
    assert a_p2.admissibility == SemanticAdmissibility.SUPPORTED
    assert a_grp.semantic_value == a_p1.semantic_value + a_p2.semantic_value


def test_semantic_admissibility_j_hard_candidate_exists_no_supported_hypotheses():
    """
    Test J: hard candidate exists but no supported hypotheses
        => NO_SEMANTICALLY_ADMISSIBLE_CANDIDATES
    """
    from bookkeeping_state.reconciliation.models import UnresolvedReason

    b = factories.bank_item("b1", amount=50_000, description="Unmatched bank item")
    j = factories.book_item("j1", amount=50_000, description="Unmatched book item")
    _, _, state, _ = _build_test_environment(bank_items=(b,), book_items=(j,))

    service = ReconciliationService()
    plan = service.plan(BookkeepingQueries(state))

    assert len(plan.commands) == 0
    unres_bank = plan.result.unresolved_bank_items
    assert len(unres_bank) == 1
    assert unres_bank[0].bank_item_id == "b1"
    assert unres_bank[0].reason == UnresolvedReason.NO_SEMANTICALLY_ADMISSIBLE_CANDIDATES


def test_semantic_admissibility_k_supported_candidate_exists_loses_globally():
    """
    Test K: supported candidate exists but loses globally
        => NO_MUTUALLY_COMPATIBLE_MATCH
    """
    from bookkeeping_state.reconciliation.models import UnresolvedReason

    b_winner = factories.bank_item("b-win", amount=100_000, reference="MATCH-1", date_val=date(2026, 1, 15))
    b_loser = factories.bank_item("b-lose", amount=100_000, reference="MATCH-1", date_val=date(2026, 1, 20))  # lower date proximity
    j = factories.book_item("j1", amount=100_000, reference="MATCH-1", date_val=date(2026, 1, 15))

    _, _, state, _ = _build_test_environment(bank_items=(b_winner, b_loser), book_items=(j,))
    service = ReconciliationService()
    plan = service.plan(BookkeepingQueries(state))

    assert len(plan.commands) == 1
    unres_bank = plan.result.unresolved_bank_items
    assert len(unres_bank) == 1
    assert unres_bank[0].bank_item_id == "b-lose"
    assert unres_bank[0].reason == UnresolvedReason.NO_MUTUALLY_COMPATIBLE_MATCH


def test_semantic_admissibility_l_accepted_provenance_must_be_supported():
    """
    Test L: accepted durable reconciliation provenance can only originate from
    SUPPORTED hypothesis. Transitions reject non-SUPPORTED commands, and
    state validation rejects non-SUPPORTED reconciliations.
    """
    b = factories.bank_item("b1", amount=10_000, reference="INV-1")
    j = factories.book_item("j1", amount=10_000, reference="INV-1")
    _, _, state, engine = _build_test_environment(bank_items=(b,), book_items=(j,))

    cmd = CreateReconciliationCommand(
        command_id="cmd-1",
        expected_state_revision=state.revision,
        source=CommandSource.RECONCILIATION,
        session_id=state.session_id,
        issued_at=FIXED_TIME,
        reconciliation_id="rec-1",
        bank_allocations=(BankAllocation(bank_item_id="b1", amount_units="10000"),),
        book_allocations=(BookAllocation(book_item_id="j1", amount_units="10000"),),
        source_hypothesis_id="hyp-1",
        source_hypothesis_admissibility=SemanticAdmissibility.INSUFFICIENT_EVIDENCE,
    )
    batch = TransitionBatch(
        batch_id="batch-1",
        session_id=state.session_id,
        expected_state_revision=state.revision,
        commands=(cmd,),
    )
    res = engine.apply_batch(state=state, batch=batch)
    assert res.status == TransitionStatus.REJECTED
    assert res.rejection is not None
    assert res.rejection.code == RejectionCode.INVALID_HYPOTHESIS_PROVENANCE


