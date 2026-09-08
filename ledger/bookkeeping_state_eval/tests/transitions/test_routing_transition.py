from __future__ import annotations

from datetime import date, datetime, timezone

import pytest

from bookkeeping_state.domain.bank import BankAccount
from bookkeeping_state.domain.books import BookItem
from bookkeeping_state.domain.commands import (
    CommandSource,
    CreateRoutingDecisionCommand,
    InvalidateRoutingDecisionCommand,
)
from bookkeeping_state.domain.context import (
    AccountingPolicy,
    BookkeepingContext,
)
from bookkeeping_state.domain.enums import Direction
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
    resolve_active_routing_decisions,
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
        session_id_factory=lambda: "routing-session",
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
        session_id="routing-session",
    )


# ======================================================================
# Create
# ======================================================================


def test_create_routing_decision_updates_live_and_durable_state(
    repository,
    hydrator,
    engine,
    state,
) -> None:
    """
    book-2 begins without an active route.

    Accepting a routing command must:

        persist exactly one RoutingDecision
        advance live state revision exactly once
        advance persistence revision exactly once
        expose the new decision as current routing truth
        survive destroy-and-rehydrate
    """

    assert state.revision == 0
    assert state.persistence_revision == 3

    assert (
        resolve_active_routing_decisions(state)
        .get("book-2")
        is None
    )

    command = CreateRoutingDecisionCommand(
        command_id="cmd-create-route-2",
        expected_state_revision=0,
        source=CommandSource.ROUTING,
        session_id=state.session_id,
        issued_at=FIXED_TIME,

        routing_decision_id="route-2",
        book_item_id="book-2",
        bank_account_id="bank-account-1",

        decision_source=RoutingDecisionSource.CP_SAT,
        utility=940,
        solver_run_id="routing-solver-run-2",
    )

    result = engine.apply(
        state=state,
        command=command,
    )

    assert result.status == TransitionStatus.APPLIED
    assert result.applied
    assert result.delta is not None

    # ------------------------------------------------------------------
    # Revision semantics
    # ------------------------------------------------------------------

    assert result.previous_state_revision == 0
    assert result.resulting_state_revision == 1

    assert result.previous_persistence_revision == 3
    assert result.resulting_persistence_revision == 4

    assert state.revision == 1
    assert state.persistence_revision == 4

    assert (
        repository.current_revision(
            company_id="atlas-distribution"
        )
        == 4
    )

    # ------------------------------------------------------------------
    # Exact durable artifact
    # ------------------------------------------------------------------

    decision = state.get_routing_decision(
        "route-2"
    )

    assert decision is not None

    assert decision.book_item_id == "book-2"
    assert decision.bank_account_id == "bank-account-1"
    assert decision.source == RoutingDecisionSource.CP_SAT
    assert decision.utility == 940
    assert decision.solver_run_id == "routing-solver-run-2"

    assert (
        decision.supersedes_routing_decision_id
        is None
    )

    assert decision.session_id == "routing-session"
    assert decision.state_revision_at_creation == 1
    assert decision.created_at == FIXED_TIME

    # ------------------------------------------------------------------
    # Derived truth
    # ------------------------------------------------------------------

    active = resolve_active_routing_decisions(
        state
    )

    assert active["book-2"].id == "route-2"

    # Existing route remains independently active for book-1.
    assert active["book-1"].id == "route-1"

    # ------------------------------------------------------------------
    # Delta tells the same story
    # ------------------------------------------------------------------

    assert result.delta.routing_decisions == (
        decision,
    )

    assert result.delta.routing_invalidations == ()

    # ------------------------------------------------------------------
    # Rehydrate entirely from persistence
    # ------------------------------------------------------------------

    live_artifact_fingerprint = artifact_fingerprint(
        state
    )

    live_semantic_fingerprint = state_fingerprint(
        state
    )

    restored = hydrator.hydrate(
        company_id="atlas-distribution",
        session_id="rehydrated-routing-session",
    )

    try:
        # Fresh in-memory incarnation.
        assert restored.revision == 0

        # But born from the latest durable world.
        assert restored.persistence_revision == 4

        restored_route = (
            restored.get_routing_decision(
                "route-2"
            )
        )

        assert restored_route == decision

        restored_active = (
            resolve_active_routing_decisions(
                restored
            )
        )

        assert (
            restored_active["book-2"].id
            == "route-2"
        )

        assert (
            artifact_fingerprint(restored)
            == live_artifact_fingerprint
        )

        assert (
            state_fingerprint(restored)
            == live_semantic_fingerprint
        )

    finally:
        restored.close()


# ======================================================================
# Semantic idempotency
# ======================================================================


def test_reasserting_same_route_is_noop(
    repository,
    engine,
    state,
) -> None:
    """
    book-1 is already actively routed to bank-account-1.

    Another routing invocation reaching the same economic conclusion must not
    create another durable decision merely because it has another artifact ID,
    solver run, or utility.
    """

    active_before = resolve_active_routing_decisions(
        state
    )

    assert active_before["book-1"].id == "route-1"

    artifact_before = artifact_fingerprint(
        state
    )

    semantic_before = state_fingerprint(
        state
    )

    command = CreateRoutingDecisionCommand(
        command_id="cmd-reassert-route-1",
        expected_state_revision=0,
        source=CommandSource.ROUTING,
        session_id=state.session_id,
        issued_at=FIXED_TIME,

        routing_decision_id="route-reassertion",
        book_item_id="book-1",
        bank_account_id="bank-account-1",

        decision_source=RoutingDecisionSource.CP_SAT,
        utility=999,
        solver_run_id="another-solver-run",
    )

    result = engine.apply(
        state=state,
        command=command,
    )

    assert result.status == TransitionStatus.NOOP
    assert result.noop

    # ------------------------------------------------------------------
    # Nothing advanced
    # ------------------------------------------------------------------

    assert state.revision == 0
    assert state.persistence_revision == 3

    assert (
        repository.current_revision(
            company_id="atlas-distribution"
        )
        == 3
    )

    # ------------------------------------------------------------------
    # No redundant decision artifact was created
    # ------------------------------------------------------------------

    assert (
        state.get_routing_decision(
            "route-reassertion"
        )
        is None
    )

    assert (
        len(state.routing_decisions)
        == 1
    )

    active_after = resolve_active_routing_decisions(
        state
    )

    assert active_after["book-1"].id == "route-1"

    assert (
        artifact_fingerprint(state)
        == artifact_before
    )

    assert (
        state_fingerprint(state)
        == semantic_before
    )


# ======================================================================
# Supersession
# ======================================================================


def test_explicit_supersession_replaces_current_route_without_rewriting_history(
    repository,
    hydrator,
    engine,
    state,
) -> None:
    """
    route-1 currently routes book-1 to bank-account-1.

    We intentionally change routing truth to bank-account-2.

    route-1 must remain durably present, while route-2 becomes the single
    current interpretation.
    """

    before = resolve_active_routing_decisions(
        state
    )

    assert before["book-1"].id == "route-1"
    assert (
        before["book-1"].bank_account_id
        == "bank-account-1"
    )

    command = CreateRoutingDecisionCommand(
        command_id="cmd-supersede-route-1",
        expected_state_revision=0,
        source=CommandSource.ROUTING,
        session_id=state.session_id,
        issued_at=FIXED_TIME,

        routing_decision_id="route-2",
        book_item_id="book-1",
        bank_account_id="bank-account-2",

        decision_source=RoutingDecisionSource.HUMAN,

        supersedes_routing_decision_id="route-1",
    )

    result = engine.apply(
        state=state,
        command=command,
    )

    assert result.status == TransitionStatus.APPLIED

    assert state.revision == 1
    assert state.persistence_revision == 4

    # ------------------------------------------------------------------
    # History remains append-only
    # ------------------------------------------------------------------

    old_decision = state.get_routing_decision(
        "route-1"
    )

    new_decision = state.get_routing_decision(
        "route-2"
    )

    assert old_decision is not None
    assert new_decision is not None

    assert old_decision.bank_account_id == "bank-account-1"

    assert new_decision.bank_account_id == "bank-account-2"

    assert (
        new_decision.supersedes_routing_decision_id
        == "route-1"
    )

    assert new_decision.state_revision_at_creation == 1

    # ------------------------------------------------------------------
    # Current truth resolves to only the new decision
    # ------------------------------------------------------------------

    active = resolve_active_routing_decisions(
        state
    )

    assert active["book-1"].id == "route-2"

    assert (
        active["book-1"].bank_account_id
        == "bank-account-2"
    )

    # ------------------------------------------------------------------
    # Durable semantics survive hydration
    # ------------------------------------------------------------------

    restored = hydrator.hydrate(
        company_id="atlas-distribution",
        session_id="rehydrated-session",
    )

    try:
        assert (
            restored.get_routing_decision(
                "route-1"
            )
            is not None
        )

        assert (
            restored.get_routing_decision(
                "route-2"
            )
            is not None
        )

        restored_active = (
            resolve_active_routing_decisions(
                restored
            )
        )

        assert (
            restored_active["book-1"].id
            == "route-2"
        )

        assert (
            restored_active["book-1"]
            .bank_account_id
            == "bank-account-2"
        )

        assert (
            state_fingerprint(restored)
            == state_fingerprint(state)
        )

    finally:
        restored.close()


# ======================================================================
# Multiple transitions in one live session
# ======================================================================


def test_each_applied_routing_transition_advances_both_revisions_once(
    repository,
    engine,
    state,
) -> None:
    """
    Prove state_revision and persistence_revision evolve independently but in
    lockstep for successful durable transitions within one session.
    """

    first = CreateRoutingDecisionCommand(
        command_id="cmd-route-book-2-first",
        expected_state_revision=0,
        source=CommandSource.ROUTING,
        session_id=state.session_id,
        issued_at=FIXED_TIME,

        routing_decision_id="route-book-2-v1",
        book_item_id="book-2",
        bank_account_id="bank-account-1",

        decision_source=RoutingDecisionSource.CP_SAT,
        utility=900,
        solver_run_id="solver-v1",
    )

    first_result = engine.apply(
        state=state,
        command=first,
    )

    assert first_result.status == TransitionStatus.APPLIED

    assert state.revision == 1
    assert state.persistence_revision == 4

    first_decision = state.get_routing_decision(
        "route-book-2-v1"
    )

    assert first_decision is not None
    assert first_decision.state_revision_at_creation == 1

    second = CreateRoutingDecisionCommand(
        command_id="cmd-route-book-2-second",
        expected_state_revision=1,
        source=CommandSource.ROUTING,
        session_id=state.session_id,
        issued_at=FIXED_TIME,

        routing_decision_id="route-book-2-v2",
        book_item_id="book-2",
        bank_account_id="bank-account-2",

        decision_source=RoutingDecisionSource.HUMAN,

        supersedes_routing_decision_id=(
            "route-book-2-v1"
        ),
    )

    second_result = engine.apply(
        state=state,
        command=second,
    )

    assert second_result.status == TransitionStatus.APPLIED

    assert second_result.previous_state_revision == 1
    assert second_result.resulting_state_revision == 2

    assert second_result.previous_persistence_revision == 4
    assert second_result.resulting_persistence_revision == 5

    assert state.revision == 2
    assert state.persistence_revision == 5

    assert (
        repository.current_revision(
            company_id="atlas-distribution"
        )
        == 5
    )

    second_decision = state.get_routing_decision(
        "route-book-2-v2"
    )

    assert second_decision is not None
    assert second_decision.state_revision_at_creation == 2

    active = resolve_active_routing_decisions(
        state
    )

    assert (
        active["book-2"].id
        == "route-book-2-v2"
    )


# ======================================================================
# Invalidation
# ======================================================================


def test_invalidation_removes_route_from_current_truth_but_preserves_history(
    repository,
    hydrator,
    engine,
    state,
) -> None:
    """
    Invalidation appends a new artifact.

    It must not delete or modify the original RoutingDecision.
    """

    assert (
        resolve_active_routing_decisions(
            state
        )["book-1"].id
        == "route-1"
    )

    command = InvalidateRoutingDecisionCommand(
        command_id="cmd-invalidate-route-1",
        expected_state_revision=0,
        source=CommandSource.HUMAN,
        session_id=state.session_id,
        issued_at=FIXED_TIME,

        invalidation_id="route-invalidation-1",
        routing_decision_id="route-1",
        reason="Routing decision was incorrect",
    )

    result = engine.apply(
        state=state,
        command=command,
    )

    assert result.status == TransitionStatus.APPLIED

    assert state.revision == 1
    assert state.persistence_revision == 4

    # Original historical artifact remains.
    original = state.get_routing_decision(
        "route-1"
    )

    assert original is not None

    invalidation = (
        state.routing_invalidations.get(
            "route-invalidation-1"
        )
    )

    assert invalidation is not None

    assert (
        invalidation.routing_decision_id
        == "route-1"
    )

    assert (
        invalidation.state_revision_at_invalidation
        == 1
    )

    # There is now no active routing truth for book-1.
    active = resolve_active_routing_decisions(
        state
    )

    assert "book-1" not in active

    # ------------------------------------------------------------------
    # Rehydrate and prove invalidation is durable
    # ------------------------------------------------------------------

    restored = hydrator.hydrate(
        company_id="atlas-distribution",
        session_id="rehydrated-after-invalidation",
    )

    try:
        assert (
            restored.get_routing_decision(
                "route-1"
            )
            is not None
        )

        assert (
            "route-invalidation-1"
            in restored.routing_invalidations
        )

        restored_active = (
            resolve_active_routing_decisions(
                restored
            )
        )

        assert "book-1" not in restored_active

        assert (
            state_fingerprint(restored)
            == state_fingerprint(state)
        )

    finally:
        restored.close()


def test_repeating_routing_invalidation_is_noop(
    repository,
    engine,
    state,
) -> None:
    first = InvalidateRoutingDecisionCommand(
        command_id="cmd-invalidate-first",
        expected_state_revision=0,
        source=CommandSource.HUMAN,
        session_id=state.session_id,
        issued_at=FIXED_TIME,

        invalidation_id="route-invalidation-1",
        routing_decision_id="route-1",
        reason="Incorrect route",
    )

    first_result = engine.apply(
        state=state,
        command=first,
    )

    assert first_result.status == TransitionStatus.APPLIED

    assert state.revision == 1
    assert state.persistence_revision == 4

    fingerprint_before = state_fingerprint(
        state
    )

    second = InvalidateRoutingDecisionCommand(
        command_id="cmd-invalidate-second",
        expected_state_revision=1,
        source=CommandSource.HUMAN,
        session_id=state.session_id,
        issued_at=FIXED_TIME,

        invalidation_id="route-invalidation-2",
        routing_decision_id="route-1",
        reason="Repeated invalidation",
    )

    second_result = engine.apply(
        state=state,
        command=second,
    )

    assert second_result.status == TransitionStatus.NOOP

    assert state.revision == 1
    assert state.persistence_revision == 4

    assert (
        repository.current_revision(
            company_id="atlas-distribution"
        )
        == 4
    )

    assert (
        "route-invalidation-2"
        not in state.routing_invalidations
    )

    assert (
        state_fingerprint(state)
        == fingerprint_before
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

    book_1 = BookItem(
        id="book-1",
        origin_period="2026-01",
        date=date(
            2026,
            1,
            12,
        ),
        amount_units="1000000",
        direction=Direction.BOOK_BANK_CREDIT,
        currency="MAD",
        description="Supplier invoice one",
    )

    book_2 = BookItem(
        id="book-2",
        origin_period="2026-01",
        date=date(
            2026,
            1,
            18,
        ),
        amount_units="2000000",
        direction=Direction.BOOK_BANK_CREDIT,
        currency="MAD",
        description="Supplier invoice two",
    )

    route_1 = RoutingDecision(
        id="route-1",
        book_item_id="book-1",
        bank_account_id="bank-account-1",

        source=RoutingDecisionSource.CP_SAT,
        utility=950,
        solver_run_id="seed-routing-run",

        session_id="seed-session",
        state_revision_at_creation=0,
        created_at=FIXED_TIME,
    )

    return BookkeepingSnapshot(
        persistence_revision=3,
        context=context,

        bank_accounts=(
            bank_account_1,
            bank_account_2,
        ),

        book_items=(
            book_1,
            book_2,
        ),

        routing_decisions=(
            route_1,
        ),
    )