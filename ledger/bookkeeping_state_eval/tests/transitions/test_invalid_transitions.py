from __future__ import annotations

from datetime import date, datetime, timezone

import pytest

from bookkeeping_state.domain.bank import (
    BankAccount,
    BankItem,
)
from bookkeeping_state.domain.books import BookItem
from bookkeeping_state.domain.classifications import (
    ClassificationDecision,
    ClassificationSource,
)
from bookkeeping_state.domain.commands import (
    CommandSource,
    CreateClassificationCommand,
    CreateReconciliationCommand,
    CreateRoutingDecisionCommand,
)
from bookkeeping_state.domain.context import (
    AccountingPolicy,
    BookkeepingContext,
)
from bookkeeping_state.domain.enums import Direction
from bookkeeping_state.domain.reconciliations import (
    BankAllocation,
    BookAllocation,
    Reconciliation,
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
from bookkeeping_state.state.fingerprint import (
    artifact_fingerprint,
    state_fingerprint,
)
from bookkeeping_state.transitions.engine import (
    TransitionEngine,
)
from bookkeeping_state.transitions.result import (
    RejectionCode,
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
        session_id_factory=lambda: "session-test",
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
    )


# ======================================================================
# Core rejection invariant
# ======================================================================


def test_unknown_book_item_rejection_changes_nothing(
    repository,
    engine,
    state,
) -> None:
    command = CreateRoutingDecisionCommand(
        command_id="cmd-route-unknown",
        expected_state_revision=state.revision,
        source=CommandSource.ROUTING,
        session_id=state.session_id,
        issued_at=FIXED_TIME,

        routing_decision_id="route-unknown",
        book_item_id="book-does-not-exist",
        bank_account_id="bank-account-1",
        decision_source=RoutingDecisionSource.CP_SAT,
        utility=900,
        solver_run_id="solver-1",
    )

    result = _apply_and_assert_rejected_without_mutation(
        repository=repository,
        engine=engine,
        state=state,
        command=command,
    )

    assert result.rejection is not None
    assert (
        result.rejection.code
        == RejectionCode.UNKNOWN_BOOK_ITEM
    )


def test_stale_state_revision_rejection_changes_nothing(
    repository,
    engine,
    state,
) -> None:
    command = CreateClassificationCommand(
        command_id="cmd-stale-state",
        expected_state_revision=999,
        source=CommandSource.ASE_DAG,
        session_id=state.session_id,
        issued_at=FIXED_TIME,

        classification_id="classification-new",
        book_item_id="book-2",
        account_code="6111",
        classification_source=(
            ClassificationSource.ASE_DAG
        ),
        confidence=0.99,
    )

    result = _apply_and_assert_rejected_without_mutation(
        repository=repository,
        engine=engine,
        state=state,
        command=command,
    )

    assert result.rejection is not None
    assert (
        result.rejection.code
        == RejectionCode.STATE_REVISION_CONFLICT
    )


def test_wrong_session_rejection_changes_nothing(
    repository,
    engine,
    state,
) -> None:
    command = CreateClassificationCommand(
        command_id="cmd-wrong-session",
        expected_state_revision=state.revision,
        source=CommandSource.ASE_DAG,
        session_id="another-session",
        issued_at=FIXED_TIME,

        classification_id="classification-new",
        book_item_id="book-2",
        account_code="6111",
        classification_source=(
            ClassificationSource.ASE_DAG
        ),
        confidence=0.99,
    )

    result = _apply_and_assert_rejected_without_mutation(
        repository=repository,
        engine=engine,
        state=state,
        command=command,
    )

    assert result.rejection is not None
    assert (
        result.rejection.code
        == RejectionCode.SESSION_MISMATCH
    )


# ======================================================================
# Routing truth
# ======================================================================


def test_different_route_requires_explicit_supersession(
    repository,
    engine,
    state,
) -> None:
    """
    book-1 already has:

        route-1 -> bank-account-1

    A second active route cannot silently appear.
    """

    command = CreateRoutingDecisionCommand(
        command_id="cmd-conflicting-route",
        expected_state_revision=state.revision,
        source=CommandSource.ROUTING,
        session_id=state.session_id,
        issued_at=FIXED_TIME,

        routing_decision_id="route-2",
        book_item_id="book-1",
        bank_account_id="bank-account-2",
        decision_source=RoutingDecisionSource.CP_SAT,
        utility=950,
        solver_run_id="solver-2",

        supersedes_routing_decision_id=None,
    )

    result = _apply_and_assert_rejected_without_mutation(
        repository=repository,
        engine=engine,
        state=state,
        command=command,
    )

    assert result.rejection is not None
    assert (
        result.rejection.code
        == RejectionCode.MULTIPLE_ACTIVE_ROUTING_DECISIONS
    )


def test_route_cannot_supersede_decision_for_another_book_item(
    repository,
    engine,
    state,
) -> None:
    command = CreateRoutingDecisionCommand(
        command_id="cmd-bad-route-supersession",
        expected_state_revision=state.revision,
        source=CommandSource.ROUTING,
        session_id=state.session_id,
        issued_at=FIXED_TIME,

        routing_decision_id="route-new",
        book_item_id="book-2",
        bank_account_id="bank-account-1",
        decision_source=RoutingDecisionSource.HUMAN,

        # route-1 belongs to book-1.
        supersedes_routing_decision_id="route-1",
    )

    result = _apply_and_assert_rejected_without_mutation(
        repository=repository,
        engine=engine,
        state=state,
        command=command,
    )

    assert result.rejection is not None
    assert (
        result.rejection.code
        == RejectionCode.ROUTING_SUBJECT_MISMATCH
    )


# ======================================================================
# Classification truth
# ======================================================================


def test_different_classification_requires_explicit_supersession(
    repository,
    engine,
    state,
) -> None:
    """
    book-1 is already classified as 6111.

    A different classification must explicitly supersede classification-1.
    """

    command = CreateClassificationCommand(
        command_id="cmd-conflicting-classification",
        expected_state_revision=state.revision,
        source=CommandSource.ASE_DAG,
        session_id=state.session_id,
        issued_at=FIXED_TIME,

        classification_id="classification-2",
        book_item_id="book-1",
        account_code="6122",
        classification_source=(
            ClassificationSource.ASE_DAG
        ),
        confidence=0.96,

        supersedes_classification_id=None,
    )

    result = _apply_and_assert_rejected_without_mutation(
        repository=repository,
        engine=engine,
        state=state,
        command=command,
    )

    assert result.rejection is not None
    assert (
        result.rejection.code
        == RejectionCode.MULTIPLE_ACTIVE_CLASSIFICATIONS
    )


def test_classification_cannot_supersede_another_items_decision(
    repository,
    engine,
    state,
) -> None:
    command = CreateClassificationCommand(
        command_id="cmd-bad-classification-supersession",
        expected_state_revision=state.revision,
        source=CommandSource.ASE_DAG,
        session_id=state.session_id,
        issued_at=FIXED_TIME,

        classification_id="classification-new",
        book_item_id="book-2",
        account_code="6111",
        classification_source=(
            ClassificationSource.ASE_DAG
        ),

        # classification-1 belongs to book-1.
        supersedes_classification_id=(
            "classification-1"
        ),
    )

    result = _apply_and_assert_rejected_without_mutation(
        repository=repository,
        engine=engine,
        state=state,
        command=command,
    )

    assert result.rejection is not None
    assert (
        result.rejection.code
        == RejectionCode.CLASSIFICATION_SUBJECT_MISMATCH
    )


# ======================================================================
# Reconciliation truth
# ======================================================================


def test_reconciliation_cannot_exceed_remaining_bank_capacity(
    repository,
    engine,
    state,
) -> None:
    """
    bank-2 is already fully consumed by reconciliation-1.

    A second distinct reconciliation cannot spend the same bank amount again.
    """

    command = CreateReconciliationCommand(
        command_id="cmd-overallocate-bank",
        expected_state_revision=state.revision,
        source=CommandSource.RECONCILIATION,
        session_id=state.session_id,
        issued_at=FIXED_TIME,

        reconciliation_id="reconciliation-overflow",

        bank_allocations=(
            BankAllocation(
                bank_item_id="bank-2",
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

    result = _apply_and_assert_rejected_without_mutation(
        repository=repository,
        engine=engine,
        state=state,
        command=command,
    )

    assert result.rejection is not None
    assert (
        result.rejection.code
        == RejectionCode.CAPACITY_EXCEEDED
    )


def test_reconciliation_must_conserve_money(
    repository,
    engine,
    state,
) -> None:
    command = CreateReconciliationCommand(
        command_id="cmd-unbalanced-reconciliation",
        expected_state_revision=state.revision,
        source=CommandSource.RECONCILIATION,
        session_id=state.session_id,
        issued_at=FIXED_TIME,

        reconciliation_id="reconciliation-unbalanced",

        bank_allocations=(
            BankAllocation(
                bank_item_id="bank-1",
                amount_units="1000000",
            ),
        ),

        book_allocations=(
            BookAllocation(
                book_item_id="book-2",
                amount_units="900000",
            ),
        ),
    )

    result = _apply_and_assert_rejected_without_mutation(
        repository=repository,
        engine=engine,
        state=state,
        command=command,
    )

    assert result.rejection is not None
    assert (
        result.rejection.code
        == RejectionCode.MONETARY_IMBALANCE
    )


def test_missing_runtime_hypothesis_is_rejected_as_stale(
    repository,
    engine,
    state,
) -> None:
    command = CreateReconciliationCommand(
        command_id="cmd-stale-hypothesis",
        expected_state_revision=state.revision,
        source=CommandSource.RECONCILIATION,
        session_id=state.session_id,
        issued_at=FIXED_TIME,

        reconciliation_id="reconciliation-from-stale-hypothesis",

        bank_allocations=(
            BankAllocation(
                bank_item_id="bank-1",
                amount_units="1000000",
            ),
        ),

        book_allocations=(
            BookAllocation(
                book_item_id="book-2",
                amount_units="1000000",
            ),
        ),

        source_hypothesis_id="hypothesis-does-not-exist",
    )

    result = _apply_and_assert_rejected_without_mutation(
        repository=repository,
        engine=engine,
        state=state,
        command=command,
    )

    assert result.rejection is not None
    assert (
        result.rejection.code
        == RejectionCode.STALE_HYPOTHESIS
    )


# ======================================================================
# Durable optimistic concurrency
# ======================================================================


def test_stale_hydrated_session_cannot_commit_over_newer_persistence(
    repository,
    hydrator,
    engine,
) -> None:
    """
    Two sessions hydrate persistence revision 7.

    Session A commits first, moving durable reality to revision 8.

    Session B still has a perfectly valid local state revision 0, but its
    persistence revision is stale and therefore it must not commit.
    """

    state_a = hydrator.hydrate(
        company_id="atlas-distribution",
        session_id="session-a",
    )

    state_b = hydrator.hydrate(
        company_id="atlas-distribution",
        session_id="session-b",
    )

    assert state_a.persistence_revision == 7
    assert state_b.persistence_revision == 7

    first_command = CreateClassificationCommand(
        command_id="cmd-session-a",
        expected_state_revision=0,
        source=CommandSource.ASE_DAG,
        session_id="session-a",
        issued_at=FIXED_TIME,

        classification_id="classification-session-a",
        book_item_id="book-2",
        account_code="6125",
        classification_source=(
            ClassificationSource.ASE_DAG
        ),
        confidence=0.98,
    )

    first_result = engine.apply(
        state=state_a,
        command=first_command,
    )

    assert first_result.status == TransitionStatus.APPLIED
    assert state_a.revision == 1
    assert state_a.persistence_revision == 8
    assert (
        repository.current_revision(
            company_id="atlas-distribution"
        )
        == 8
    )

    state_b_artifact_before = artifact_fingerprint(
        state_b
    )

    state_b_semantic_before = state_fingerprint(
        state_b
    )

    second_command = CreateRoutingDecisionCommand(
        command_id="cmd-session-b",
        expected_state_revision=0,
        source=CommandSource.ROUTING,
        session_id="session-b",
        issued_at=FIXED_TIME,

        routing_decision_id="route-session-b",
        book_item_id="book-2",
        bank_account_id="bank-account-1",
        decision_source=RoutingDecisionSource.CP_SAT,
        utility=800,
    )

    second_result = engine.apply(
        state=state_b,
        command=second_command,
    )

    assert second_result.status == TransitionStatus.REJECTED
    assert second_result.rejection is not None
    assert (
        second_result.rejection.code
        == RejectionCode.PERSISTENCE_REVISION_CONFLICT
    )

    # The stale live state itself was not mutated.
    assert state_b.revision == 0
    assert state_b.persistence_revision == 7

    assert (
        artifact_fingerprint(state_b)
        == state_b_artifact_before
    )

    assert (
        state_fingerprint(state_b)
        == state_b_semantic_before
    )

    # Session B's failed command also did not advance durable reality.
    assert (
        repository.current_revision(
            company_id="atlas-distribution"
        )
        == 8
    )


# ======================================================================
# Helpers
# ======================================================================


def _apply_and_assert_rejected_without_mutation(
    *,
    repository: InMemoryBookkeepingRepository,
    engine: TransitionEngine,
    state,
    command,
):
    """
    Central eval invariant:

        ordinary REJECTED transition
            means
        exactly zero observable bookkeeping mutation.
    """

    state_revision_before = state.revision

    persistence_revision_before = (
        repository.current_revision(
            company_id=state.context.company_id
        )
    )

    live_persistence_revision_before = (
        state.persistence_revision
    )

    artifact_before = artifact_fingerprint(
        state
    )

    semantic_before = state_fingerprint(
        state
    )

    event_count_before = len(
        state.events
    )

    hypothesis_ids_before = tuple(
        sorted(
            state.reconciliation_hypotheses
        )
    )

    result = engine.apply(
        state=state,
        command=command,
    )

    assert result.status == TransitionStatus.REJECTED
    assert result.rejected

    # --------------------------------------------------------------
    # Live state unchanged
    # --------------------------------------------------------------

    assert state.revision == state_revision_before

    assert (
        state.persistence_revision
        == live_persistence_revision_before
    )

    assert (
        artifact_fingerprint(state)
        == artifact_before
    )

    assert (
        state_fingerprint(state)
        == semantic_before
    )

    assert len(state.events) == event_count_before

    assert tuple(
        sorted(
            state.reconciliation_hypotheses
        )
    ) == hypothesis_ids_before

    # --------------------------------------------------------------
    # Durable persistence unchanged
    # --------------------------------------------------------------

    assert (
        repository.current_revision(
            company_id=state.context.company_id
        )
        == persistence_revision_before
    )

    # --------------------------------------------------------------
    # Result itself tells the same story
    # --------------------------------------------------------------

    assert (
        result.previous_state_revision
        == state_revision_before
    )

    assert (
        result.resulting_state_revision
        == state_revision_before
    )

    assert (
        result.previous_persistence_revision
        == live_persistence_revision_before
    )

    assert (
        result.resulting_persistence_revision
        == live_persistence_revision_before
    )

    return result


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

    bank_1 = BankItem(
        id="bank-1",
        bank_account_id="bank-account-1",
        date=date(2026, 1, 12),
        amount_units="1000000",
        direction=Direction.BANK_OUTFLOW,
        currency="MAD",
        description="Supplier payment one",
    )

    bank_2 = BankItem(
        id="bank-2",
        bank_account_id="bank-account-1",
        date=date(2026, 1, 18),
        amount_units="1000000",
        direction=Direction.BANK_OUTFLOW,
        currency="MAD",
        description="Supplier payment two",
    )

    book_1 = BookItem(
        id="book-1",
        origin_period="2026-01",
        date=date(2026, 1, 12),
        amount_units="1000000",
        direction=Direction.BOOK_BANK_CREDIT,
        currency="MAD",
        description="Supplier invoice one",
    )

    book_2 = BookItem(
        id="book-2",
        origin_period="2026-01",
        date=date(2026, 1, 18),
        amount_units="1000000",
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

    classification_1 = ClassificationDecision(
        id="classification-1",
        book_item_id="book-1",
        account_code="6111",
        source=ClassificationSource.ASE_DAG,
        confidence=0.99,
        rationale="Seed classification",
        session_id="seed-session",
        dag_run_id="seed-dag-run",
        ase_node_id="seed-node",
        state_revision_at_decision=0,
        created_at=FIXED_TIME,
    )

    reconciliation_1 = Reconciliation(
        id="reconciliation-1",

        bank_allocations=(
            BankAllocation(
                bank_item_id="bank-2",
                amount_units="1000000",
            ),
        ),

        book_allocations=(
            BookAllocation(
                book_item_id="book-2",
                amount_units="1000000",
            ),
        ),

        session_id="seed-session",
        state_revision_at_creation=0,
        created_at=FIXED_TIME,
    )

    return BookkeepingSnapshot(
        persistence_revision=7,
        context=context,

        bank_accounts=(
            bank_account_1,
            bank_account_2,
        ),

        bank_items=(
            bank_1,
            bank_2,
        ),

        book_items=(
            book_1,
            book_2,
        ),

        routing_decisions=(
            route_1,
        ),

        classifications=(
            classification_1,
        ),

        reconciliations=(
            reconciliation_1,
        ),
    )