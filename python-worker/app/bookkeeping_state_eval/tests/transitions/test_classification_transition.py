from __future__ import annotations

from datetime import date, datetime, timezone

import pytest

from bookkeeping_state_eval.domain.books import BookItem
from bookkeeping_state_eval.domain.classifications import (
    ClassificationDecision,
    ClassificationSource,
)
from bookkeeping_state_eval.domain.commands import (
    CommandSource,
    CreateClassificationCommand,
    InvalidateClassificationCommand,
)
from bookkeeping_state_eval.domain.context import (
    AccountingPolicy,
    BookkeepingContext,
)
from bookkeeping_state_eval.domain.enums import Direction
from bookkeeping_state_eval.hydration.hydrator import (
    BookkeepingHydrator,
)
from bookkeeping_state_eval.persistence.in_memory import (
    InMemoryBookkeepingRepository,
)
from bookkeeping_state_eval.persistence.repository import (
    BookkeepingSnapshot,
)
from bookkeeping_state_eval.state.derived import (
    resolve_active_classifications,
)
from bookkeeping_state_eval.state.fingerprint import (
    artifact_fingerprint,
    state_fingerprint,
)
from bookkeeping_state_eval.transitions.engine import (
    TransitionEngine,
)
from bookkeeping_state_eval.transitions.result import (
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
        session_id_factory=lambda: "classification-session",
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
        session_id="classification-session",
    )


# ======================================================================
# Create
# ======================================================================


def test_create_classification_updates_live_and_durable_state(
    repository,
    hydrator,
    engine,
    state,
) -> None:
    """
    book-2 begins without an active classification.

    Accepting the command must:

        create one immutable ClassificationDecision
        advance state revision exactly once
        advance persistence revision exactly once
        expose the decision as current truth
        survive destroy-and-rehydrate
    """

    assert state.revision == 0
    assert state.persistence_revision == 5

    active_before = resolve_active_classifications(
        state
    )

    assert "book-2" not in active_before

    command = CreateClassificationCommand(
        command_id="cmd-classify-book-2",
        expected_state_revision=0,
        source=CommandSource.ASE_DAG,
        session_id=state.session_id,
        issued_at=FIXED_TIME,

        classification_id="classification-2",
        book_item_id="book-2",
        account_code="6125",

        classification_source=(
            ClassificationSource.ASE_DAG
        ),

        confidence=0.97,
        rationale="Office supplies purchase",

        dag_run_id="dag-run-2",
        ase_node_id="ase-node-2",
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

    assert result.previous_persistence_revision == 5
    assert result.resulting_persistence_revision == 6

    assert state.revision == 1
    assert state.persistence_revision == 6

    assert (
        repository.current_revision(
            company_id="atlas-distribution"
        )
        == 6
    )

    # ------------------------------------------------------------------
    # Durable artifact
    # ------------------------------------------------------------------

    classification = state.get_classification(
        "classification-2"
    )

    assert classification is not None

    assert classification.book_item_id == "book-2"
    assert classification.account_code == "6125"

    assert (
        classification.source
        == ClassificationSource.ASE_DAG
    )

    assert classification.confidence == 0.97
    assert (
        classification.rationale
        == "Office supplies purchase"
    )

    assert classification.dag_run_id == "dag-run-2"
    assert classification.ase_node_id == "ase-node-2"

    assert (
        classification.supersedes_classification_id
        is None
    )

    assert (
        classification.session_id
        == "classification-session"
    )

    assert (
        classification.state_revision_at_decision
        == 1
    )

    assert classification.created_at == FIXED_TIME

    # ------------------------------------------------------------------
    # Derived truth
    # ------------------------------------------------------------------

    active = resolve_active_classifications(
        state
    )

    assert (
        active["book-2"].id
        == "classification-2"
    )

    assert (
        active["book-2"].account_code
        == "6125"
    )

    # Existing book-1 truth remains independent.
    assert (
        active["book-1"].id
        == "classification-1"
    )

    # ------------------------------------------------------------------
    # Delta
    # ------------------------------------------------------------------

    assert result.delta.classifications == (
        classification,
    )

    assert (
        result.delta.classification_invalidations
        == ()
    )

    # ------------------------------------------------------------------
    # Rehydration
    # ------------------------------------------------------------------

    artifact_before = artifact_fingerprint(
        state
    )

    semantic_before = state_fingerprint(
        state
    )

    restored = hydrator.hydrate(
        company_id="atlas-distribution",
        session_id="rehydrated-classification-session",
    )

    try:
        # Fresh runtime incarnation.
        assert restored.revision == 0

        # Latest durable world.
        assert restored.persistence_revision == 6

        restored_classification = (
            restored.get_classification(
                "classification-2"
            )
        )

        assert (
            restored_classification
            == classification
        )

        restored_active = (
            resolve_active_classifications(
                restored
            )
        )

        assert (
            restored_active["book-2"].id
            == "classification-2"
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


def test_reasserting_same_account_code_is_noop(
    repository,
    engine,
    state,
) -> None:
    """
    book-1 is already classified as account 6111.

    Another ASE run reaching the same accounting conclusion must not create
    another durable artifact merely because confidence, rationale, or runtime
    provenance differ.
    """

    active_before = resolve_active_classifications(
        state
    )

    assert (
        active_before["book-1"].account_code
        == "6111"
    )

    artifact_before = artifact_fingerprint(
        state
    )

    semantic_before = state_fingerprint(
        state
    )

    command = CreateClassificationCommand(
        command_id="cmd-reassert-classification",
        expected_state_revision=0,
        source=CommandSource.ASE_DAG,
        session_id=state.session_id,
        issued_at=FIXED_TIME,

        classification_id="classification-reassertion",
        book_item_id="book-1",
        account_code="6111",

        classification_source=(
            ClassificationSource.ASE_DAG
        ),

        confidence=0.83,
        rationale=(
            "Different model reasoning, same accounting conclusion"
        ),

        dag_run_id="different-dag-run",
        ase_node_id="different-node",
    )

    result = engine.apply(
        state=state,
        command=command,
    )

    assert result.status == TransitionStatus.NOOP
    assert result.noop

    assert state.revision == 0
    assert state.persistence_revision == 5

    assert (
        repository.current_revision(
            company_id="atlas-distribution"
        )
        == 5
    )

    assert (
        state.get_classification(
            "classification-reassertion"
        )
        is None
    )

    assert len(state.classifications) == 1

    active_after = resolve_active_classifications(
        state
    )

    assert (
        active_after["book-1"].id
        == "classification-1"
    )

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


def test_explicit_supersession_changes_classification_without_rewriting_history(
    repository,
    hydrator,
    engine,
    state,
) -> None:
    """
    classification-1 currently says:

        book-1 = 6111

    A later accepted interpretation changes this to 6122.

    The original decision must remain durable history.
    """

    before = resolve_active_classifications(
        state
    )

    assert (
        before["book-1"].id
        == "classification-1"
    )

    assert (
        before["book-1"].account_code
        == "6111"
    )

    command = CreateClassificationCommand(
        command_id="cmd-supersede-classification",
        expected_state_revision=0,
        source=CommandSource.HUMAN,
        session_id=state.session_id,
        issued_at=FIXED_TIME,

        classification_id="classification-2",
        book_item_id="book-1",
        account_code="6122",

        classification_source=(
            ClassificationSource.HUMAN
        ),

        rationale=(
            "Accountant corrected original classification"
        ),

        supersedes_classification_id=(
            "classification-1"
        ),
    )

    result = engine.apply(
        state=state,
        command=command,
    )

    assert result.status == TransitionStatus.APPLIED

    assert state.revision == 1
    assert state.persistence_revision == 6

    # ------------------------------------------------------------------
    # Historical artifact remains untouched
    # ------------------------------------------------------------------

    old_classification = (
        state.get_classification(
            "classification-1"
        )
    )

    new_classification = (
        state.get_classification(
            "classification-2"
        )
    )

    assert old_classification is not None
    assert new_classification is not None

    assert old_classification.account_code == "6111"

    assert new_classification.account_code == "6122"

    assert (
        new_classification
        .supersedes_classification_id
        == "classification-1"
    )

    assert (
        new_classification.state_revision_at_decision
        == 1
    )

    # ------------------------------------------------------------------
    # Only the new interpretation is active
    # ------------------------------------------------------------------

    active = resolve_active_classifications(
        state
    )

    assert (
        active["book-1"].id
        == "classification-2"
    )

    assert (
        active["book-1"].account_code
        == "6122"
    )

    # ------------------------------------------------------------------
    # Durable after rehydration
    # ------------------------------------------------------------------

    restored = hydrator.hydrate(
        company_id="atlas-distribution",
        session_id="rehydrated-after-supersession",
    )

    try:
        assert (
            restored.get_classification(
                "classification-1"
            )
            is not None
        )

        assert (
            restored.get_classification(
                "classification-2"
            )
            is not None
        )

        restored_active = (
            resolve_active_classifications(
                restored
            )
        )

        assert (
            restored_active["book-1"].id
            == "classification-2"
        )

        assert (
            restored_active["book-1"].account_code
            == "6122"
        )

        assert (
            state_fingerprint(restored)
            == state_fingerprint(state)
        )

    finally:
        restored.close()


# ======================================================================
# Multiple accepted decisions in one live session
# ======================================================================


def test_each_applied_classification_transition_advances_revisions_once(
    repository,
    engine,
    state,
) -> None:
    first = CreateClassificationCommand(
        command_id="cmd-book-2-classification-v1",
        expected_state_revision=0,
        source=CommandSource.ASE_DAG,
        session_id=state.session_id,
        issued_at=FIXED_TIME,

        classification_id="classification-book-2-v1",
        book_item_id="book-2",
        account_code="6125",

        classification_source=(
            ClassificationSource.ASE_DAG
        ),

        confidence=0.95,
        dag_run_id="dag-v1",
        ase_node_id="node-v1",
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
    assert state.persistence_revision == 6

    first_decision = state.get_classification(
        "classification-book-2-v1"
    )

    assert first_decision is not None

    assert (
        first_decision.state_revision_at_decision
        == 1
    )

    second = CreateClassificationCommand(
        command_id="cmd-book-2-classification-v2",
        expected_state_revision=1,
        source=CommandSource.HUMAN,
        session_id=state.session_id,
        issued_at=FIXED_TIME,

        classification_id="classification-book-2-v2",
        book_item_id="book-2",
        account_code="6126",

        classification_source=(
            ClassificationSource.HUMAN
        ),

        rationale="Human correction",

        supersedes_classification_id=(
            "classification-book-2-v1"
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

    assert (
        second_result.previous_state_revision
        == 1
    )

    assert (
        second_result.resulting_state_revision
        == 2
    )

    assert (
        second_result.previous_persistence_revision
        == 6
    )

    assert (
        second_result.resulting_persistence_revision
        == 7
    )

    assert state.revision == 2
    assert state.persistence_revision == 7

    assert (
        repository.current_revision(
            company_id="atlas-distribution"
        )
        == 7
    )

    second_decision = state.get_classification(
        "classification-book-2-v2"
    )

    assert second_decision is not None

    assert (
        second_decision.state_revision_at_decision
        == 2
    )

    active = resolve_active_classifications(
        state
    )

    assert (
        active["book-2"].id
        == "classification-book-2-v2"
    )


# ======================================================================
# Invalidation
# ======================================================================


def test_invalidation_removes_active_classification_but_preserves_history(
    repository,
    hydrator,
    engine,
    state,
) -> None:
    assert (
        resolve_active_classifications(
            state
        )["book-1"].id
        == "classification-1"
    )

    command = InvalidateClassificationCommand(
        command_id="cmd-invalidate-classification-1",
        expected_state_revision=0,
        source=CommandSource.HUMAN,
        session_id=state.session_id,
        issued_at=FIXED_TIME,

        invalidation_id="classification-invalidation-1",
        classification_id="classification-1",
        reason="Classification was based on incorrect evidence",
    )

    result = engine.apply(
        state=state,
        command=command,
    )

    assert result.status == TransitionStatus.APPLIED

    assert state.revision == 1
    assert state.persistence_revision == 6

    # ------------------------------------------------------------------
    # Original classification remains durable
    # ------------------------------------------------------------------

    original = state.get_classification(
        "classification-1"
    )

    assert original is not None
    assert original.account_code == "6111"

    invalidation = (
        state.classification_invalidations.get(
            "classification-invalidation-1"
        )
    )

    assert invalidation is not None

    assert (
        invalidation.classification_id
        == "classification-1"
    )

    assert (
        invalidation.state_revision_at_invalidation
        == 1
    )

    # ------------------------------------------------------------------
    # Current truth disappears
    # ------------------------------------------------------------------

    active = resolve_active_classifications(
        state
    )

    assert "book-1" not in active

    # ------------------------------------------------------------------
    # Rehydrate
    # ------------------------------------------------------------------

    restored = hydrator.hydrate(
        company_id="atlas-distribution",
        session_id="rehydrated-after-invalidation",
    )

    try:
        assert (
            restored.get_classification(
                "classification-1"
            )
            is not None
        )

        assert (
            "classification-invalidation-1"
            in restored.classification_invalidations
        )

        restored_active = (
            resolve_active_classifications(
                restored
            )
        )

        assert (
            "book-1"
            not in restored_active
        )

        assert (
            state_fingerprint(restored)
            == state_fingerprint(state)
        )

    finally:
        restored.close()


def test_repeating_classification_invalidation_is_noop(
    repository,
    engine,
    state,
) -> None:
    first = InvalidateClassificationCommand(
        command_id="cmd-invalidate-first",
        expected_state_revision=0,
        source=CommandSource.HUMAN,
        session_id=state.session_id,
        issued_at=FIXED_TIME,

        invalidation_id="classification-invalidation-1",
        classification_id="classification-1",
        reason="Incorrect classification",
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
    assert state.persistence_revision == 6

    semantic_before = state_fingerprint(
        state
    )

    second = InvalidateClassificationCommand(
        command_id="cmd-invalidate-second",
        expected_state_revision=1,
        source=CommandSource.HUMAN,
        session_id=state.session_id,
        issued_at=FIXED_TIME,

        invalidation_id="classification-invalidation-2",
        classification_id="classification-1",
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

    assert state.revision == 1
    assert state.persistence_revision == 6

    assert (
        repository.current_revision(
            company_id="atlas-distribution"
        )
        == 6
    )

    assert (
        "classification-invalidation-2"
        not in state.classification_invalidations
    )

    assert (
        state_fingerprint(state)
        == semantic_before
    )


# ======================================================================
# Supersession + later invalidation semantics
# ======================================================================


def test_invalidating_superseding_classification_does_not_resurrect_old_truth(
    engine,
    state,
) -> None:
    """
    Critical historical semantics:

        C1 active
        C2 supersedes C1
        C2 invalidated

    Result:

        no active classification

    C1 does not magically become active again.
    """

    supersede = CreateClassificationCommand(
        command_id="cmd-create-c2",
        expected_state_revision=0,
        source=CommandSource.HUMAN,
        session_id=state.session_id,
        issued_at=FIXED_TIME,

        classification_id="classification-2",
        book_item_id="book-1",
        account_code="6122",

        classification_source=(
            ClassificationSource.HUMAN
        ),

        supersedes_classification_id=(
            "classification-1"
        ),
    )

    supersede_result = engine.apply(
        state=state,
        command=supersede,
    )

    assert (
        supersede_result.status
        == TransitionStatus.APPLIED
    )

    active_after_supersession = (
        resolve_active_classifications(
            state
        )
    )

    assert (
        active_after_supersession["book-1"].id
        == "classification-2"
    )

    invalidate = InvalidateClassificationCommand(
        command_id="cmd-invalidate-c2",
        expected_state_revision=1,
        source=CommandSource.HUMAN,
        session_id=state.session_id,
        issued_at=FIXED_TIME,

        invalidation_id="classification-2-invalidation",
        classification_id="classification-2",
        reason="Second interpretation also incorrect",
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
    assert state.persistence_revision == 7

    active_final = resolve_active_classifications(
        state
    )

    assert "book-1" not in active_final

    # Both decisions remain historical facts.
    assert (
        state.get_classification(
            "classification-1"
        )
        is not None
    )

    assert (
        state.get_classification(
            "classification-2"
        )
        is not None
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

        description="Office equipment supplier invoice",
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

        description="Office supplies purchase",
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

    return BookkeepingSnapshot(
        persistence_revision=5,

        context=context,

        book_items=(
            book_1,
            book_2,
        ),

        classifications=(
            classification_1,
        ),
    )