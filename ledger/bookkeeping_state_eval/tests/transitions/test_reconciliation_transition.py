from __future__ import annotations

from datetime import date, datetime, timezone

import pytest

from bookkeeping_state.domain.bank import (
    BankAccount,
    BankItem,
)
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
from bookkeeping_state.domain.enums import (
    Direction,
    Eligibility,
)
from bookkeeping_state.domain.events import StateEventType
from bookkeeping_state.domain.hypotheses import (
    ReconciliationHypothesis,
)
from bookkeeping_state.domain.reconciliations import (
    BankAllocation,
    BookAllocation,
)
from bookkeeping_state.domain.routing import (
    RoutingDecision,
    RoutingDecisionSource,
)
from bookkeeping_state.hydration.hydrator import (
    BookkeepingHydrator,
)
from bookkeeping_state_eval.persistence.in_memory import (
    InMemoryBookkeepingRepository,
)
from bookkeeping_state.persistence.repository import (
    BookkeepingSnapshot,
)
from bookkeeping_state.state.derived import (
    build_derived_state,
)
from bookkeeping_state.state.fingerprint import (
    artifact_fingerprint,
    state_fingerprint,
)
from bookkeeping_state.transitions.engine import (
    TransitionEngine,
)
from bookkeeping_state.transitions.result import (
    TransitionStatus,
)


FIXED_TIME = datetime(
    2026,
    1,
    31,
    12,
    0,
    0,
    tzinfo=timezone.utc,
)


@pytest.fixture
def repository() -> InMemoryBookkeepingRepository:
    return InMemoryBookkeepingRepository(
        initial_snapshots=[
            _baseline_snapshot(),
        ]
    )


@pytest.fixture
def hydrator(
    repository: InMemoryBookkeepingRepository,
) -> BookkeepingHydrator:
    return BookkeepingHydrator(
        repository=repository,
        clock=lambda: FIXED_TIME,
        session_id_factory=lambda: "reconciliation-session",
    )


@pytest.fixture
def engine(
    repository: InMemoryBookkeepingRepository,
) -> TransitionEngine:
    return TransitionEngine(
        repository=repository,
    )


@pytest.fixture
def state(
    hydrator: BookkeepingHydrator,
):
    return hydrator.hydrate(
        company_id="atlas-distribution",
        session_id="reconciliation-session",
    )


# ======================================================================
# Create
# ======================================================================


def test_create_reconciliation_updates_live_and_durable_state(
    repository,
    hydrator,
    engine,
    state,
) -> None:
    """
    bank-1 = 100.0000 MAD
    book-1 = 150.0000 MAD

    Partial BookItem reconciliation is permitted.

    After accepting 100.0000 MAD:

        bank-1 remaining = 0
        book-1 remaining = 50.0000 MAD
    """

    assert state.revision == 0
    assert state.persistence_revision == 11

    derived_before = build_derived_state(
        state
    )

    assert (
        derived_before.bank_remaining_units[
            "bank-1"
        ]
        == 1_000_000
    )

    assert (
        derived_before.book_remaining_units[
            "book-1"
        ]
        == 1_500_000
    )

    command = CreateReconciliationCommand(
        command_id="cmd-reconcile-1",
        expected_state_revision=0,
        source=CommandSource.RECONCILIATION,
        session_id=state.session_id,
        issued_at=FIXED_TIME,

        reconciliation_id="reconciliation-1",

        bank_allocations=(
            BankAllocation(
                bank_item_id="bank-1",
                amount_units="1000000",
            ),
        ),

        book_allocations=(
            BookAllocation(
                book_item_id="book-1",
                amount_units="1000000",
            ),
        ),

        semantic_rationale=(
            "Bank payment matches supplier payable"
        ),
    )

    result = engine.apply(
        state=state,
        command=command,
    )

    assert result.status == TransitionStatus.APPLIED
    assert result.applied
    assert result.delta is not None

    # ------------------------------------------------------------------
    # Revisions
    # ------------------------------------------------------------------

    assert result.previous_state_revision == 0
    assert result.resulting_state_revision == 1

    assert result.previous_persistence_revision == 11
    assert result.resulting_persistence_revision == 12

    assert state.revision == 1
    assert state.persistence_revision == 12

    assert (
        repository.current_revision(
            company_id="atlas-distribution"
        )
        == 12
    )

    # ------------------------------------------------------------------
    # Durable artifact
    # ------------------------------------------------------------------

    reconciliation = state.get_reconciliation(
        "reconciliation-1"
    )

    assert reconciliation is not None

    assert reconciliation.bank_allocations == (
        BankAllocation(
            bank_item_id="bank-1",
            amount_units="1000000",
        ),
    )

    assert reconciliation.book_allocations == (
        BookAllocation(
            book_item_id="book-1",
            amount_units="1000000",
        ),
    )

    assert (
        reconciliation.semantic_rationale
        == "Bank payment matches supplier payable"
    )

    assert reconciliation.session_id == state.session_id

    assert (
        reconciliation.state_revision_at_creation
        == 1
    )

    assert reconciliation.created_at == FIXED_TIME

    # ------------------------------------------------------------------
    # Derived economic truth
    # ------------------------------------------------------------------

    derived = build_derived_state(
        state
    )

    assert (
        derived.active_reconciliations[
            "reconciliation-1"
        ]
        == reconciliation
    )

    assert (
        derived.bank_allocated_units[
            "bank-1"
        ]
        == 1_000_000
    )

    assert (
        derived.bank_remaining_units[
            "bank-1"
        ]
        == 0
    )

    assert (
        derived.book_allocated_units[
            "book-1"
        ]
        == 1_000_000
    )

    assert (
        derived.book_remaining_units[
            "book-1"
        ]
        == 500_000
    )

    assert (
        "bank-1"
        in derived.fully_reconciled_bank_item_ids
    )

    assert (
        "book-1"
        in derived.partially_reconciled_book_item_ids
    )

    # ------------------------------------------------------------------
    # Delta
    # ------------------------------------------------------------------

    assert result.delta.reconciliations == (
        reconciliation,
    )

    assert (
        result.delta.reconciliation_invalidations
        == ()
    )

    # ------------------------------------------------------------------
    # Destroy and reconstruct from persistence
    # ------------------------------------------------------------------

    artifact_before = artifact_fingerprint(
        state
    )

    semantic_before = state_fingerprint(
        state
    )

    restored = hydrator.hydrate(
        company_id="atlas-distribution",
        session_id="rehydrated-reconciliation-session",
    )

    try:
        assert restored.revision == 0
        assert restored.persistence_revision == 12

        restored_reconciliation = (
            restored.get_reconciliation(
                "reconciliation-1"
            )
        )

        assert (
            restored_reconciliation
            == reconciliation
        )

        restored_derived = build_derived_state(
            restored
        )

        assert (
            restored_derived.bank_remaining_units[
                "bank-1"
            ]
            == 0
        )

        assert (
            restored_derived.book_remaining_units[
                "book-1"
            ]
            == 500_000
        )

        assert (
            artifact_fingerprint(restored)
            == artifact_before
        )

        assert (
            state_fingerprint(restored)
            == semantic_before
        )

    finally:
        restored.close()


# ======================================================================
# Semantic idempotency
# ======================================================================


def test_reasserting_same_reconciliation_is_noop(
    repository,
    engine,
    state,
) -> None:
    first = CreateReconciliationCommand(
        command_id="cmd-reconcile-first",
        expected_state_revision=0,
        source=CommandSource.RECONCILIATION,
        session_id=state.session_id,
        issued_at=FIXED_TIME,

        reconciliation_id="reconciliation-1",

        bank_allocations=(
            BankAllocation(
                bank_item_id="bank-1",
                amount_units="1000000",
            ),
        ),

        book_allocations=(
            BookAllocation(
                book_item_id="book-1",
                amount_units="1000000",
            ),
        ),
    )

    first_result = engine.apply(
        state=state,
        command=first,
    )

    assert (
        first_result.status
        == TransitionStatus.APPLIED
    )

    assert state.revision == 1
    assert state.persistence_revision == 12

    artifact_before = artifact_fingerprint(
        state
    )

    semantic_before = state_fingerprint(
        state
    )

    second = CreateReconciliationCommand(
        command_id="cmd-reconcile-again",
        expected_state_revision=1,
        source=CommandSource.RECONCILIATION,
        session_id=state.session_id,
        issued_at=FIXED_TIME,

        reconciliation_id="reconciliation-duplicate",

        bank_allocations=(
            BankAllocation(
                bank_item_id="bank-1",
                amount_units="1000000",
            ),
        ),

        book_allocations=(
            BookAllocation(
                book_item_id="book-1",
                amount_units="1000000",
            ),
        ),

        semantic_rationale=(
            "Same economic match proposed again"
        ),
    )

    second_result = engine.apply(
        state=state,
        command=second,
    )

    assert (
        second_result.status
        == TransitionStatus.NOOP
    )

    assert state.revision == 1
    assert state.persistence_revision == 12

    assert (
        repository.current_revision(
            company_id="atlas-distribution"
        )
        == 12
    )

    assert (
        state.get_reconciliation(
            "reconciliation-duplicate"
        )
        is None
    )

    assert len(state.reconciliations) == 1

    assert (
        artifact_fingerprint(state)
        == artifact_before
    )

    assert (
        state_fingerprint(state)
        == semantic_before
    )


# ======================================================================
# Partial BookItem reconciliation
# ======================================================================


def test_multiple_bank_items_can_clear_one_book_item_incrementally(
    engine,
    state,
) -> None:
    """
    book-1 has authoritative amount 150.0000 MAD.

    bank-1 contributes 100.0000 MAD.
    bank-3 contributes 50.0000 MAD.

    Remaining amount must be derived from active Reconciliation artifacts,
    never persisted as mutable BookItem state.
    """

    first = CreateReconciliationCommand(
        command_id="cmd-partial-book-first",
        expected_state_revision=0,
        source=CommandSource.RECONCILIATION,
        session_id=state.session_id,
        issued_at=FIXED_TIME,

        reconciliation_id="reconciliation-partial-1",

        bank_allocations=(
            BankAllocation(
                bank_item_id="bank-1",
                amount_units="1000000",
            ),
        ),

        book_allocations=(
            BookAllocation(
                book_item_id="book-1",
                amount_units="1000000",
            ),
        ),
    )

    first_result = engine.apply(
        state=state,
        command=first,
    )

    assert (
        first_result.status
        == TransitionStatus.APPLIED
    )

    after_first = build_derived_state(
        state
    )

    assert (
        after_first.book_remaining_units[
            "book-1"
        ]
        == 500_000
    )

    assert (
        "book-1"
        in after_first.partially_reconciled_book_item_ids
    )

    second = CreateReconciliationCommand(
        command_id="cmd-partial-book-second",
        expected_state_revision=1,
        source=CommandSource.RECONCILIATION,
        session_id=state.session_id,
        issued_at=FIXED_TIME,

        reconciliation_id="reconciliation-partial-2",

        bank_allocations=(
            BankAllocation(
                bank_item_id="bank-3",
                amount_units="500000",
            ),
        ),

        book_allocations=(
            BookAllocation(
                book_item_id="book-1",
                amount_units="500000",
            ),
        ),
    )

    second_result = engine.apply(
        state=state,
        command=second,
    )

    assert (
        second_result.status
        == TransitionStatus.APPLIED
    )

    assert state.revision == 2
    assert state.persistence_revision == 13

    after_second = build_derived_state(
        state
    )

    assert (
        after_second.book_allocated_units[
            "book-1"
        ]
        == 1_500_000
    )

    assert (
        after_second.book_remaining_units[
            "book-1"
        ]
        == 0
    )

    assert (
        "book-1"
        in after_second.fully_reconciled_book_item_ids
    )

    assert (
        "book-1"
        not in after_second.partially_reconciled_book_item_ids
    )

    # Original amount never changed.
    assert (
        state.get_book_item(
            "book-1"
        ).amount_int
        == 1_500_000
    )


# ======================================================================
# Runtime hypothesis
# ======================================================================


def test_selected_hypothesis_becomes_durable_reconciliation_and_pool_is_cleared(
    engine,
    state,
) -> None:
    """
    Runtime hypotheses are disposable.

    The selected hypothesis may justify a durable Reconciliation, but once the
    state advances all hypotheses generated against the previous revision are
    invalid.
    """

    selected = ReconciliationHypothesis(
        id="hypothesis-selected",

        eligibility=Eligibility.SELECTABLE,

        utility=980,

        bank_allocations=(
            BankAllocation(
                bank_item_id="bank-2",
                amount_units="700000",
            ),
        ),

        book_allocations=(
            BookAllocation(
                book_item_id="book-2",
                amount_units="700000",
            ),
        ),

        semantic_rationale=(
            "Exact supplier-payment semantic match"
        ),

        state_revision=0,

        generated_at=FIXED_TIME,
    )

    unrelated = ReconciliationHypothesis(
        id="hypothesis-unrelated",

        eligibility=Eligibility.SELECTABLE,

        utility=700,

        bank_allocations=(
            BankAllocation(
                bank_item_id="bank-1",
                amount_units="1000000",
            ),
        ),

        book_allocations=(
            BookAllocation(
                book_item_id="book-1",
                amount_units="1000000",
            ),
        ),

        state_revision=0,

        generated_at=FIXED_TIME,
    )

    state._put_reconciliation_hypothesis(
        selected
    )

    state._put_reconciliation_hypothesis(
        unrelated
    )

    assert (
        state.get_reconciliation_hypothesis(
            "hypothesis-selected"
        )
        == selected
    )

    assert len(
        state.reconciliation_hypotheses
    ) == 2

    command = CreateReconciliationCommand(
        command_id="cmd-select-hypothesis",
        expected_state_revision=0,
        source=CommandSource.RECONCILIATION,
        session_id=state.session_id,
        issued_at=FIXED_TIME,

        reconciliation_id=(
            "reconciliation-from-hypothesis"
        ),

        bank_allocations=(
            BankAllocation(
                bank_item_id="bank-2",
                amount_units="700000",
            ),
        ),

        book_allocations=(
            BookAllocation(
                book_item_id="book-2",
                amount_units="700000",
            ),
        ),

        source_hypothesis_id=(
            "hypothesis-selected"
        ),

        semantic_rationale=(
            selected.semantic_rationale
        ),
    )

    result = engine.apply(
        state=state,
        command=command,
    )

    assert result.status == TransitionStatus.APPLIED

    reconciliation = state.get_reconciliation(
        "reconciliation-from-hypothesis"
    )

    assert reconciliation is not None

    assert (
        reconciliation.source_hypothesis_id
        == "hypothesis-selected"
    )

    # ------------------------------------------------------------------
    # Runtime hypotheses disappear after state changes.
    # ------------------------------------------------------------------

    assert len(
        state.reconciliation_hypotheses
    ) == 0

    assert (
        state.get_reconciliation_hypothesis(
            "hypothesis-selected"
        )
        is None
    )

    assert (
        state.get_reconciliation_hypothesis(
            "hypothesis-unrelated"
        )
        is None
    )

    assert result.delta is not None
    assert result.delta.clear_all_hypotheses

    # ------------------------------------------------------------------
    # Runtime event explains why they disappeared.
    # ------------------------------------------------------------------

    hypothesis_events = tuple(
        event
        for event in state.events
        if (
            event.event_type
            == StateEventType.HYPOTHESES_INVALIDATED
        )
    )

    assert len(hypothesis_events) == 1

    assert set(
        hypothesis_events[0].affected_artifact_ids
    ) == {
        "hypothesis-selected",
        "hypothesis-unrelated",
    }


# ======================================================================
# Invalidation
# ======================================================================


def test_invalidation_releases_reconciliation_capacity_without_deleting_history(
    repository,
    hydrator,
    engine,
    state,
) -> None:
    create = CreateReconciliationCommand(
        command_id="cmd-create-before-invalidation",
        expected_state_revision=0,
        source=CommandSource.RECONCILIATION,
        session_id=state.session_id,
        issued_at=FIXED_TIME,

        reconciliation_id="reconciliation-1",

        bank_allocations=(
            BankAllocation(
                bank_item_id="bank-1",
                amount_units="1000000",
            ),
        ),

        book_allocations=(
            BookAllocation(
                book_item_id="book-1",
                amount_units="1000000",
            ),
        ),
    )

    create_result = engine.apply(
        state=state,
        command=create,
    )

    assert (
        create_result.status
        == TransitionStatus.APPLIED
    )

    allocated = build_derived_state(
        state
    )

    assert (
        allocated.bank_remaining_units[
            "bank-1"
        ]
        == 0
    )

    assert (
        allocated.book_remaining_units[
            "book-1"
        ]
        == 500_000
    )

    invalidate = InvalidateReconciliationCommand(
        command_id="cmd-invalidate-reconciliation",
        expected_state_revision=1,
        source=CommandSource.HUMAN,
        session_id=state.session_id,
        issued_at=FIXED_TIME,

        invalidation_id="reconciliation-invalidation-1",

        reconciliation_id="reconciliation-1",

        reason="Bank match was incorrect",
    )

    invalidation_result = engine.apply(
        state=state,
        command=invalidate,
    )

    assert (
        invalidation_result.status
        == TransitionStatus.APPLIED
    )

    assert state.revision == 2
    assert state.persistence_revision == 13

    # ------------------------------------------------------------------
    # Original decision remains historical
    # ------------------------------------------------------------------

    assert (
        state.get_reconciliation(
            "reconciliation-1"
        )
        is not None
    )

    invalidation = (
        state.reconciliation_invalidations.get(
            "reconciliation-invalidation-1"
        )
    )

    assert invalidation is not None

    assert (
        invalidation.reconciliation_id
        == "reconciliation-1"
    )

    # ------------------------------------------------------------------
    # But it no longer consumes economic capacity
    # ------------------------------------------------------------------

    released = build_derived_state(
        state
    )

    assert (
        "reconciliation-1"
        not in released.active_reconciliations
    )

    assert (
        released.bank_remaining_units[
            "bank-1"
        ]
        == 1_000_000
    )

    assert (
        released.book_remaining_units[
            "book-1"
        ]
        == 1_500_000
    )

    # ------------------------------------------------------------------
    # Rehydrate same result
    # ------------------------------------------------------------------

    semantic_before = state_fingerprint(
        state
    )

    restored = hydrator.hydrate(
        company_id="atlas-distribution",
        session_id="rehydrated-after-reconciliation-invalidation",
    )

    try:
        assert (
            restored.get_reconciliation(
                "reconciliation-1"
            )
            is not None
        )

        restored_derived = build_derived_state(
            restored
        )

        assert (
            "reconciliation-1"
            not in restored_derived.active_reconciliations
        )

        assert (
            restored_derived.bank_remaining_units[
                "bank-1"
            ]
            == 1_000_000
        )

        assert (
            restored_derived.book_remaining_units[
                "book-1"
            ]
            == 1_500_000
        )

        assert (
            state_fingerprint(restored)
            == semantic_before
        )

    finally:
        restored.close()


def test_repeating_reconciliation_invalidation_is_noop(
    repository,
    engine,
    state,
) -> None:
    create = CreateReconciliationCommand(
        command_id="cmd-create-reconciliation",
        expected_state_revision=0,
        source=CommandSource.RECONCILIATION,
        session_id=state.session_id,
        issued_at=FIXED_TIME,

        reconciliation_id="reconciliation-1",

        bank_allocations=(
            BankAllocation(
                bank_item_id="bank-1",
                amount_units="1000000",
            ),
        ),

        book_allocations=(
            BookAllocation(
                book_item_id="book-1",
                amount_units="1000000",
            ),
        ),
    )

    assert (
        engine.apply(
            state=state,
            command=create,
        ).status
        == TransitionStatus.APPLIED
    )

    first = InvalidateReconciliationCommand(
        command_id="cmd-invalidate-first",
        expected_state_revision=1,
        source=CommandSource.HUMAN,
        session_id=state.session_id,
        issued_at=FIXED_TIME,

        invalidation_id="reconciliation-invalidation-1",
        reconciliation_id="reconciliation-1",
        reason="Incorrect match",
    )

    assert (
        engine.apply(
            state=state,
            command=first,
        ).status
        == TransitionStatus.APPLIED
    )

    assert state.revision == 2
    assert state.persistence_revision == 13

    semantic_before = state_fingerprint(
        state
    )

    second = InvalidateReconciliationCommand(
        command_id="cmd-invalidate-second",
        expected_state_revision=2,
        source=CommandSource.HUMAN,
        session_id=state.session_id,
        issued_at=FIXED_TIME,

        invalidation_id="reconciliation-invalidation-2",
        reconciliation_id="reconciliation-1",
        reason="Repeated invalidation",
    )

    second_result = engine.apply(
        state=state,
        command=second,
    )

    assert (
        second_result.status
        == TransitionStatus.NOOP
    )

    assert state.revision == 2
    assert state.persistence_revision == 13

    assert (
        repository.current_revision(
            company_id="atlas-distribution"
        )
        == 13
    )

    assert (
        "reconciliation-invalidation-2"
        not in state.reconciliation_invalidations
    )

    assert (
        state_fingerprint(state)
        == semantic_before
    )


# ======================================================================
# Scenario
# ======================================================================


def _baseline_snapshot() -> BookkeepingSnapshot:
    context = BookkeepingContext(
        company_id="atlas-distribution",

        period_start=date(
            2026,
            1,
            1,
        ),

        period_end=date(
            2026,
            1,
            31,
        ),

        base_currency="MAD",

        policy=AccountingPolicy(
            chart_of_accounts_id="morocco-pcge",

            reconciliation_date_window_days=45,

            require_exact_currency_match=True,

            allow_partial_book_reconciliation=True,

            allow_partial_bank_reconciliation=False,
        ),
    )

    bank_account_1 = BankAccount(
        id="bank-account-1",
        name="Operating Account",
        currency="MAD",
        institution_name="Atlas Bank",
    )

    bank_account_2 = BankAccount(
        id="bank-account-2",
        name="Secondary Account",
        currency="MAD",
        institution_name="Atlas Bank",
    )

    # ------------------------------------------------------------------
    # Bank side
    # ------------------------------------------------------------------

    bank_1 = BankItem(
        id="bank-1",
        bank_account_id="bank-account-1",

        date=date(
            2026,
            1,
            12,
        ),

        amount_units="1000000",

        direction=Direction.BANK_OUTFLOW,

        currency="MAD",

        description="Supplier payment 100 MAD",
    )

    bank_2 = BankItem(
        id="bank-2",
        bank_account_id="bank-account-1",

        date=date(
            2026,
            1,
            18,
        ),

        amount_units="700000",

        direction=Direction.BANK_OUTFLOW,

        currency="MAD",

        description="Office supplies payment 70 MAD",
    )

    bank_3 = BankItem(
        id="bank-3",
        bank_account_id="bank-account-1",

        date=date(
            2026,
            1,
            20,
        ),

        amount_units="500000",

        direction=Direction.BANK_OUTFLOW,

        currency="MAD",

        description="Final supplier payment 50 MAD",
    )

    # This account exists so routing consistency can be evaluated elsewhere.
    bank_4 = BankItem(
        id="bank-4",
        bank_account_id="bank-account-2",

        date=date(
            2026,
            1,
            22,
        ),

        amount_units="400000",

        direction=Direction.BANK_OUTFLOW,

        currency="MAD",

        description="Secondary account payment",
    )

    # ------------------------------------------------------------------
    # Book side
    # ------------------------------------------------------------------

    book_1 = BookItem(
        id="book-1",
        origin_period="2026-01",

        date=date(
            2026,
            1,
            12,
        ),

        amount_units="1500000",

        direction=Direction.BOOK_BANK_CREDIT,

        currency="MAD",

        description="Supplier payable 150 MAD",
    )

    book_2 = BookItem(
        id="book-2",
        origin_period="2026-01",

        date=date(
            2026,
            1,
            18,
        ),

        amount_units="700000",

        direction=Direction.BOOK_BANK_CREDIT,

        currency="MAD",

        description="Office supplies payable 70 MAD",
    )

    # ------------------------------------------------------------------
    # Existing routing truth
    # ------------------------------------------------------------------

    route_1 = RoutingDecision(
        id="route-book-1",

        book_item_id="book-1",

        bank_account_id="bank-account-1",

        source=RoutingDecisionSource.CP_SAT,

        utility=970,

        solver_run_id="seed-routing-run",

        session_id="seed-session",

        state_revision_at_creation=0,

        created_at=FIXED_TIME,
    )

    return BookkeepingSnapshot(
        persistence_revision=11,

        context=context,

        bank_accounts=(
            bank_account_1,
            bank_account_2,
        ),

        bank_items=(
            bank_1,
            bank_2,
            bank_3,
            bank_4,
        ),

        book_items=(
            book_1,
            book_2,
        ),

        routing_decisions=(
            route_1,
        ),
    )