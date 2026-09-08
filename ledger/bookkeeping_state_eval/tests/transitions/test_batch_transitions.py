from __future__ import annotations

from datetime import datetime, timezone
from unittest.mock import patch

import pytest

from bookkeeping_state.domain.commands import (
    CommandSource,
    CreateClassificationCommand,
    CreateReconciliationCommand,
    CreateRoutingDecisionCommand,
    InvalidateClassificationCommand,
    InvalidateReconciliationCommand,
    InvalidateRoutingDecisionCommand,
)
from bookkeeping_state.domain.classifications import (
    ClassificationSource,
)
from bookkeeping_state.domain.enums import Direction
from bookkeeping_state.domain.events import StateEventType
from bookkeeping_state.domain.hypotheses import (
    Eligibility,
    ReconciliationHypothesis,
)
from bookkeeping_state.domain.reconciliations import (
    BankAllocation,
    BookAllocation,
)
from bookkeeping_state.domain.routing import (
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
    state_fingerprint,
)
from bookkeeping_state.state.queries import (
    BookkeepingQueries,
)
from bookkeeping_state.transitions.batch import (
    TransitionBatch,
)
from bookkeeping_state.transitions.engine import (
    CommittedStateApplicationError,
    TransitionEngine,
)
from bookkeeping_state.transitions.result import (
    RejectionCode,
    TransitionStatus,
)
from tests.factories import (
    FIXED_TIME,
    account,
    bank_item,
    book_item,
    classification,
    context,
    reconciliation,
    routing,
    snapshot,
)


def _setup_world(
    *,
    books: tuple[str, ...] = ("book-1", "book-2", "book-3"),
    banks: tuple[str, ...] = ("bank-1", "bank-2", "bank-3"),
    accounts: tuple[str, ...] = ("account-1", "account-2"),
    policy_updates: dict | None = None,
):
    account_objs = tuple(account(acc_id) for acc_id in accounts)
    # Assign bank items across accounts
    bank_objs = []
    for i, b_id in enumerate(banks):
        acc = accounts[i % len(accounts)]
        bank_objs.append(bank_item(b_id, account_id=acc, amount=1_000_000))

    book_objs = tuple(book_item(b_id, amount=1_000_000) for b_id in books)

    snap_ctx = context(**policy_updates) if policy_updates else context()
    snap = BookkeepingSnapshot(
        persistence_revision=0,
        context=snap_ctx,
        bank_accounts=account_objs,
        bank_items=tuple(bank_objs),
        book_items=book_objs,
    )

    repo = InMemoryBookkeepingRepository(initial_snapshots=[snap])
    hydrator = BookkeepingHydrator(
        repository=repo,
        clock=lambda: FIXED_TIME,
        session_id_factory=lambda: "session-batch",
    )
    live = hydrator.hydrate(company_id="atlas")
    engine = TransitionEngine(repository=repo)
    return live, repo, engine, hydrator


def test_two_and_three_routing_siblings_apply_atomically() -> None:
    live, repo, engine, _ = _setup_world(
        books=("book-1", "book-2", "book-3", "book-4", "book-5"),
        banks=("bank-1", "bank-2", "bank-3", "bank-4", "bank-5"),
    )

    # 1. Two routing siblings
    cmd1 = CreateRoutingDecisionCommand(
        command_id="cmd-r1",
        expected_state_revision=0,
        source=CommandSource.ROUTING,
        session_id=live.session_id,
        issued_at=FIXED_TIME,
        routing_decision_id="route-1",
        book_item_id="book-1",
        bank_account_id="account-1",
        decision_source=RoutingDecisionSource.CP_SAT,
        utility=100,
        solver_run_id="run-1",
    )
    cmd2 = CreateRoutingDecisionCommand(
        command_id="cmd-r2",
        expected_state_revision=0,
        source=CommandSource.ROUTING,
        session_id=live.session_id,
        issued_at=FIXED_TIME,
        routing_decision_id="route-2",
        book_item_id="book-2",
        bank_account_id="account-2",
        decision_source=RoutingDecisionSource.CP_SAT,
        utility=200,
        solver_run_id="run-1",
    )
    batch_2 = TransitionBatch(
        batch_id="batch-2",
        session_id=live.session_id,
        expected_state_revision=0,
        commands=(cmd1, cmd2),
    )

    result_2 = engine.apply_batch(state=live, batch=batch_2)
    assert result_2.status is TransitionStatus.APPLIED
    assert result_2.is_applied
    assert live.revision == 1
    assert live.persistence_revision == 1
    assert repo.current_revision(company_id="atlas") == 1
    queries = BookkeepingQueries(live)
    assert queries.active_route("book-1").bank_account_id == "account-1"
    assert queries.active_route("book-2").bank_account_id == "account-2"

    # 2. Three routing siblings
    cmd3 = CreateRoutingDecisionCommand(
        command_id="cmd-r3",
        expected_state_revision=1,
        source=CommandSource.ROUTING,
        session_id=live.session_id,
        issued_at=FIXED_TIME,
        routing_decision_id="route-3",
        book_item_id="book-3",
        bank_account_id="account-1",
        decision_source=RoutingDecisionSource.CP_SAT,
        utility=300,
        solver_run_id="run-2",
    )
    cmd4 = CreateRoutingDecisionCommand(
        command_id="cmd-r4",
        expected_state_revision=1,
        source=CommandSource.ROUTING,
        session_id=live.session_id,
        issued_at=FIXED_TIME,
        routing_decision_id="route-4",
        book_item_id="book-4",
        bank_account_id="account-2",
        decision_source=RoutingDecisionSource.CP_SAT,
        utility=400,
        solver_run_id="run-2",
    )
    cmd5 = CreateRoutingDecisionCommand(
        command_id="cmd-r5",
        expected_state_revision=1,
        source=CommandSource.ROUTING,
        session_id=live.session_id,
        issued_at=FIXED_TIME,
        routing_decision_id="route-5",
        book_item_id="book-5",
        bank_account_id="account-1",
        decision_source=RoutingDecisionSource.CP_SAT,
        utility=500,
        solver_run_id="run-2",
    )
    batch_3 = TransitionBatch(
        batch_id="batch-3",
        session_id=live.session_id,
        expected_state_revision=1,
        commands=(cmd3, cmd4, cmd5),
    )

    result_3 = engine.apply_batch(state=live, batch=batch_3)
    assert result_3.status is TransitionStatus.APPLIED
    assert live.revision == 2
    assert live.persistence_revision == 2
    assert repo.current_revision(company_id="atlas") == 2
    assert len(live.routing_decisions) == 5


def test_one_invalid_sibling_rejects_entire_batch() -> None:
    live, repo, engine, _ = _setup_world()

    valid_cmd = CreateRoutingDecisionCommand(
        command_id="cmd-valid",
        expected_state_revision=0,
        source=CommandSource.ROUTING,
        session_id=live.session_id,
        issued_at=FIXED_TIME,
        routing_decision_id="route-valid",
        book_item_id="book-1",
        bank_account_id="account-1",
        decision_source=RoutingDecisionSource.CP_SAT,
        utility=100,
        solver_run_id="run-1",
    )
    invalid_cmd = CreateRoutingDecisionCommand(
        command_id="cmd-invalid",
        expected_state_revision=0,
        source=CommandSource.ROUTING,
        session_id=live.session_id,
        issued_at=FIXED_TIME,
        routing_decision_id="route-invalid",
        book_item_id="unknown-book",
        bank_account_id="account-1",
        decision_source=RoutingDecisionSource.CP_SAT,
        utility=100,
        solver_run_id="run-1",
    )
    batch = TransitionBatch(
        batch_id="batch-invalid",
        session_id=live.session_id,
        expected_state_revision=0,
        commands=(valid_cmd, invalid_cmd),
    )

    result = engine.apply_batch(state=live, batch=batch)
    assert result.status is TransitionStatus.REJECTED
    assert result.is_rejected
    assert result.rejection is not None
    assert result.rejection.code is RejectionCode.UNKNOWN_BOOK_ITEM
    assert result.rejection.failed_command_id == "cmd-invalid"
    assert live.revision == 0
    assert live.persistence_revision == 0
    assert repo.current_revision(company_id="atlas") == 0
    assert len(live.routing_decisions) == 0


def test_duplicate_active_routes_reject_all() -> None:
    live, repo, engine, _ = _setup_world()

    cmd1 = CreateRoutingDecisionCommand(
        command_id="cmd-r1",
        expected_state_revision=0,
        source=CommandSource.ROUTING,
        session_id=live.session_id,
        issued_at=FIXED_TIME,
        routing_decision_id="route-1",
        book_item_id="book-1",
        bank_account_id="account-1",
        decision_source=RoutingDecisionSource.CP_SAT,
        utility=100,
        solver_run_id="run-1",
    )
    cmd2 = CreateRoutingDecisionCommand(
        command_id="cmd-r2",
        expected_state_revision=0,
        source=CommandSource.ROUTING,
        session_id=live.session_id,
        issued_at=FIXED_TIME,
        routing_decision_id="route-2",
        book_item_id="book-1",
        bank_account_id="account-2",
        decision_source=RoutingDecisionSource.CP_SAT,
        utility=200,
        solver_run_id="run-1",
    )
    batch = TransitionBatch(
        batch_id="batch-dup-route",
        session_id=live.session_id,
        expected_state_revision=0,
        commands=(cmd1, cmd2),
    )

    result = engine.apply_batch(state=live, batch=batch)
    assert result.status is TransitionStatus.REJECTED
    assert result.rejection.code in (
        RejectionCode.MULTIPLE_ACTIVE_ROUTING_DECISIONS,
        RejectionCode.RESULTING_STATE_INVALID,
    )
    assert live.revision == 0
    assert len(live.routing_decisions) == 0


def test_duplicate_active_classifications_reject_all() -> None:
    live, repo, engine, _ = _setup_world()

    cmd1 = CreateClassificationCommand(
        command_id="cmd-c1",
        expected_state_revision=0,
        source=CommandSource.ASE_DAG,
        session_id=live.session_id,
        issued_at=FIXED_TIME,
        classification_id="class-1",
        book_item_id="book-1",
        account_code="6111",
        classification_source=ClassificationSource.ASE_DAG,
    )
    cmd2 = CreateClassificationCommand(
        command_id="cmd-c2",
        expected_state_revision=0,
        source=CommandSource.ASE_DAG,
        session_id=live.session_id,
        issued_at=FIXED_TIME,
        classification_id="class-2",
        book_item_id="book-1",
        account_code="6120",
        classification_source=ClassificationSource.ASE_DAG,
    )
    batch = TransitionBatch(
        batch_id="batch-dup-class",
        session_id=live.session_id,
        expected_state_revision=0,
        commands=(cmd1, cmd2),
    )

    result = engine.apply_batch(state=live, batch=batch)
    assert result.status is TransitionStatus.REJECTED
    assert result.rejection.code in (
        RejectionCode.MULTIPLE_ACTIVE_CLASSIFICATIONS,
        RejectionCode.RESULTING_STATE_INVALID,
    )
    assert live.revision == 0
    assert len(live.classifications) == 0


def test_reconciliation_combined_overallocation_rejects_all() -> None:
    # bank-1 has amount 1_000_000
    live, repo, engine, _ = _setup_world(
        policy_updates={
            "allow_partial_bank_reconciliation": True,
            "allow_partial_book_reconciliation": True,
        }
    )

    # Command 1 allocates 600_000 of bank-1
    cmd1 = CreateReconciliationCommand(
        command_id="cmd-rec-1",
        expected_state_revision=0,
        source=CommandSource.RECONCILIATION,
        session_id=live.session_id,
        issued_at=FIXED_TIME,
        reconciliation_id="rec-1",
        bank_allocations=(
            BankAllocation(bank_item_id="bank-1", amount_units="600000"),
        ),
        book_allocations=(
            BookAllocation(book_item_id="book-1", amount_units="600000"),
        ),
    )
    # Command 2 allocates 600_000 of bank-1 (combined = 1_200_000 > 1_000_000)
    cmd2 = CreateReconciliationCommand(
        command_id="cmd-rec-2",
        expected_state_revision=0,
        source=CommandSource.RECONCILIATION,
        session_id=live.session_id,
        issued_at=FIXED_TIME,
        reconciliation_id="rec-2",
        bank_allocations=(
            BankAllocation(bank_item_id="bank-1", amount_units="600000"),
        ),
        book_allocations=(
            BookAllocation(book_item_id="book-2", amount_units="600000"),
        ),
    )
    batch = TransitionBatch(
        batch_id="batch-overalloc",
        session_id=live.session_id,
        expected_state_revision=0,
        commands=(cmd1, cmd2),
    )

    result = engine.apply_batch(state=live, batch=batch)
    assert result.status is TransitionStatus.REJECTED
    assert result.rejection.code in (
        RejectionCode.CAPACITY_EXCEEDED,
        RejectionCode.RESULTING_STATE_INVALID,
    )
    assert live.revision == 0
    assert len(live.reconciliations) == 0


def test_route_and_reconciliation_contradiction_rejects_all() -> None:
    live, repo, engine, _ = _setup_world()

    # Sibling 1: route book-1 to account-1
    cmd_route = CreateRoutingDecisionCommand(
        command_id="cmd-route-1",
        expected_state_revision=0,
        source=CommandSource.ROUTING,
        session_id=live.session_id,
        issued_at=FIXED_TIME,
        routing_decision_id="route-1",
        book_item_id="book-1",
        bank_account_id="account-1",
        decision_source=RoutingDecisionSource.CP_SAT,
        utility=100,
        solver_run_id="run-1",
    )
    # Sibling 2: reconcile book-1 with bank-2 (which belongs to account-2!)
    cmd_rec = CreateReconciliationCommand(
        command_id="cmd-rec-1",
        expected_state_revision=0,
        source=CommandSource.RECONCILIATION,
        session_id=live.session_id,
        issued_at=FIXED_TIME,
        reconciliation_id="rec-1",
        bank_allocations=(
            BankAllocation(bank_item_id="bank-2", amount_units="1000000"),
        ),
        book_allocations=(
            BookAllocation(book_item_id="book-1", amount_units="1000000"),
        ),
    )
    batch = TransitionBatch(
        batch_id="batch-contradiction",
        session_id=live.session_id,
        expected_state_revision=0,
        commands=(cmd_route, cmd_rec),
    )

    result = engine.apply_batch(state=live, batch=batch)
    assert result.status is TransitionStatus.REJECTED
    assert result.rejection.code is RejectionCode.ROUTING_SUBJECT_MISMATCH
    assert live.revision == 0
    assert len(live.routing_decisions) == 0
    assert len(live.reconciliations) == 0


def test_duplicate_artifact_ids_reject_all() -> None:
    live, repo, engine, _ = _setup_world()

    # Sibling 1 and 2 both create routing decisions with the SAME routing_decision_id
    cmd1 = CreateRoutingDecisionCommand(
        command_id="cmd-r1",
        expected_state_revision=0,
        source=CommandSource.ROUTING,
        session_id=live.session_id,
        issued_at=FIXED_TIME,
        routing_decision_id="same-route-id",
        book_item_id="book-1",
        bank_account_id="account-1",
        decision_source=RoutingDecisionSource.CP_SAT,
        utility=100,
        solver_run_id="run-1",
    )
    cmd2 = CreateRoutingDecisionCommand(
        command_id="cmd-r2",
        expected_state_revision=0,
        source=CommandSource.ROUTING,
        session_id=live.session_id,
        issued_at=FIXED_TIME,
        routing_decision_id="same-route-id",
        book_item_id="book-2",
        bank_account_id="account-2",
        decision_source=RoutingDecisionSource.CP_SAT,
        utility=200,
        solver_run_id="run-1",
    )
    batch = TransitionBatch(
        batch_id="batch-dup-artifact",
        session_id=live.session_id,
        expected_state_revision=0,
        commands=(cmd1, cmd2),
    )

    result = engine.apply_batch(state=live, batch=batch)
    assert result.status is TransitionStatus.REJECTED
    assert result.rejection.code is RejectionCode.DUPLICATE_ARTIFACT
    assert live.revision == 0


def test_all_noop_batch() -> None:
    live, repo, engine, _ = _setup_world()

    # Pre-populate active route and classification
    r_res = engine.apply(
        state=live,
        command=CreateRoutingDecisionCommand(
            command_id="setup-route",
            expected_state_revision=0,
            source=CommandSource.ROUTING,
            session_id=live.session_id,
            issued_at=FIXED_TIME,
            routing_decision_id="route-1",
            book_item_id="book-1",
            bank_account_id="account-1",
            decision_source=RoutingDecisionSource.CP_SAT,
            utility=100,
            solver_run_id="run-1",
        ),
    )
    assert r_res.status is TransitionStatus.APPLIED
    assert live.revision == 1

    c_res = engine.apply(
        state=live,
        command=CreateClassificationCommand(
            command_id="setup-class",
            expected_state_revision=1,
            source=CommandSource.ASE_DAG,
            session_id=live.session_id,
            issued_at=FIXED_TIME,
            classification_id="class-1",
            book_item_id="book-1",
            account_code="6111",
            classification_source=ClassificationSource.ASE_DAG,
        ),
    )
    assert c_res.status is TransitionStatus.APPLIED
    assert live.revision == 2
    assert live.persistence_revision == 2

    # Now run an all-noop batch at revision 2 reasserting same route & classification
    noop1 = CreateRoutingDecisionCommand(
        command_id="noop-route",
        expected_state_revision=2,
        source=CommandSource.ROUTING,
        session_id=live.session_id,
        issued_at=FIXED_TIME,
        routing_decision_id="new-id-ignored",
        book_item_id="book-1",
        bank_account_id="account-1",
        decision_source=RoutingDecisionSource.CP_SAT,
        utility=100,
        solver_run_id="run-2",
    )
    noop2 = CreateClassificationCommand(
        command_id="noop-class",
        expected_state_revision=2,
        source=CommandSource.ASE_DAG,
        session_id=live.session_id,
        issued_at=FIXED_TIME,
        classification_id="new-class-ignored",
        book_item_id="book-1",
        account_code="6111",
        classification_source=ClassificationSource.ASE_DAG,
    )
    batch = TransitionBatch(
        batch_id="batch-noop",
        session_id=live.session_id,
        expected_state_revision=2,
        commands=(noop1, noop2),
    )

    result = engine.apply_batch(state=live, batch=batch)
    assert result.status is TransitionStatus.NOOP
    assert result.is_noop
    assert live.revision == 2
    assert live.persistence_revision == 2
    assert len(result.command_results) == 2


def test_mixed_noop_and_real_changes() -> None:
    live, repo, engine, _ = _setup_world()

    # Pre-populate active route for book-1
    engine.apply(
        state=live,
        command=CreateRoutingDecisionCommand(
            command_id="setup-route",
            expected_state_revision=0,
            source=CommandSource.ROUTING,
            session_id=live.session_id,
            issued_at=FIXED_TIME,
            routing_decision_id="route-1",
            book_item_id="book-1",
            bank_account_id="account-1",
            decision_source=RoutingDecisionSource.CP_SAT,
            utility=100,
            solver_run_id="run-1",
        ),
    )
    assert live.revision == 1

    # Mixed batch: noop reassertion of book-1 route + real route for book-2
    noop_cmd = CreateRoutingDecisionCommand(
        command_id="noop-route-1",
        expected_state_revision=1,
        source=CommandSource.ROUTING,
        session_id=live.session_id,
        issued_at=FIXED_TIME,
        routing_decision_id="ignored-route",
        book_item_id="book-1",
        bank_account_id="account-1",
        decision_source=RoutingDecisionSource.CP_SAT,
        utility=100,
        solver_run_id="run-2",
    )
    real_cmd = CreateRoutingDecisionCommand(
        command_id="real-route-2",
        expected_state_revision=1,
        source=CommandSource.ROUTING,
        session_id=live.session_id,
        issued_at=FIXED_TIME,
        routing_decision_id="route-2",
        book_item_id="book-2",
        bank_account_id="account-2",
        decision_source=RoutingDecisionSource.CP_SAT,
        utility=200,
        solver_run_id="run-2",
    )
    batch = TransitionBatch(
        batch_id="batch-mixed",
        session_id=live.session_id,
        expected_state_revision=1,
        commands=(noop_cmd, real_cmd),
    )

    result = engine.apply_batch(state=live, batch=batch)
    assert result.status is TransitionStatus.APPLIED
    assert live.revision == 2
    assert live.persistence_revision == 2
    queries = BookkeepingQueries(live)
    assert queries.active_route("book-1").bank_account_id == "account-1"
    assert queries.active_route("book-2").bank_account_id == "account-2"


def test_noop_whose_truth_is_undone_by_sibling_rejects() -> None:
    live, repo, engine, _ = _setup_world()

    # Pre-populate classification for book-1
    engine.apply(
        state=live,
        command=CreateClassificationCommand(
            command_id="setup-class",
            expected_state_revision=0,
            source=CommandSource.ASE_DAG,
            session_id=live.session_id,
            issued_at=FIXED_TIME,
            classification_id="class-1",
            book_item_id="book-1",
            account_code="6111",
            classification_source=ClassificationSource.ASE_DAG,
        ),
    )
    assert live.revision == 1

    # Sibling 1: no-op reasserting classification 6111
    noop_cmd = CreateClassificationCommand(
        command_id="noop-cmd",
        expected_state_revision=1,
        source=CommandSource.ASE_DAG,
        session_id=live.session_id,
        issued_at=FIXED_TIME,
        classification_id="ignored-id",
        book_item_id="book-1",
        account_code="6111",
        classification_source=ClassificationSource.ASE_DAG,
    )
    # Sibling 2: invalidates class-1
    invalidate_cmd = InvalidateClassificationCommand(
        command_id="invalidate-cmd",
        expected_state_revision=1,
        source=CommandSource.HUMAN,
        session_id=live.session_id,
        issued_at=FIXED_TIME,
        invalidation_id="inv-1",
        classification_id="class-1",
        reason="mistake",
    )
    batch = TransitionBatch(
        batch_id="batch-undone",
        session_id=live.session_id,
        expected_state_revision=1,
        commands=(noop_cmd, invalidate_cmd),
    )

    result = engine.apply_batch(state=live, batch=batch)
    assert result.status is TransitionStatus.REJECTED
    assert result.rejection.code is RejectionCode.RESULTING_STATE_INVALID
    assert result.rejection.failed_command_id == "noop-cmd"
    assert live.revision == 1


def test_stale_batch() -> None:
    live, repo, engine, _ = _setup_world()

    # Advance state to revision 1
    engine.apply(
        state=live,
        command=CreateRoutingDecisionCommand(
            command_id="setup-route",
            expected_state_revision=0,
            source=CommandSource.ROUTING,
            session_id=live.session_id,
            issued_at=FIXED_TIME,
            routing_decision_id="route-1",
            book_item_id="book-1",
            bank_account_id="account-1",
            decision_source=RoutingDecisionSource.CP_SAT,
            utility=100,
            solver_run_id="run-1",
        ),
    )
    assert live.revision == 1

    # Batch expects revision 0
    cmd = CreateRoutingDecisionCommand(
        command_id="stale-cmd",
        expected_state_revision=0,
        source=CommandSource.ROUTING,
        session_id=live.session_id,
        issued_at=FIXED_TIME,
        routing_decision_id="route-2",
        book_item_id="book-2",
        bank_account_id="account-1",
        decision_source=RoutingDecisionSource.CP_SAT,
        utility=100,
        solver_run_id="run-2",
    )
    batch = TransitionBatch(
        batch_id="batch-stale",
        session_id=live.session_id,
        expected_state_revision=0,
        commands=(cmd,),
    )

    result = engine.apply_batch(state=live, batch=batch)
    assert result.status is TransitionStatus.REJECTED
    assert result.rejection.code is RejectionCode.STATE_REVISION_CONFLICT
    assert live.revision == 1


def test_wrong_session() -> None:
    live, repo, engine, _ = _setup_world()

    cmd = CreateRoutingDecisionCommand(
        command_id="wrong-session-cmd",
        expected_state_revision=0,
        source=CommandSource.ROUTING,
        session_id="wrong-session",
        issued_at=FIXED_TIME,
        routing_decision_id="route-1",
        book_item_id="book-1",
        bank_account_id="account-1",
        decision_source=RoutingDecisionSource.CP_SAT,
        utility=100,
        solver_run_id="run-1",
    )
    batch = TransitionBatch(
        batch_id="batch-wrong-session",
        session_id="wrong-session",
        expected_state_revision=0,
        commands=(cmd,),
    )

    result = engine.apply_batch(state=live, batch=batch)
    assert result.status is TransitionStatus.REJECTED
    assert result.rejection.code is RejectionCode.SESSION_MISMATCH
    assert live.revision == 0


def test_hypothesis_invalidation_once() -> None:
    live, repo, engine, _ = _setup_world()

    # Pre-populate 2 hypotheses
    hyp1 = ReconciliationHypothesis(
        id="hyp-1",
        state_revision=0,
        bank_allocations=(
            BankAllocation(bank_item_id="bank-1", amount_units="1000000"),
        ),
        book_allocations=(
            BookAllocation(book_item_id="book-1", amount_units="1000000"),
        ),
        eligibility=Eligibility.SELECTABLE,
        utility=100,
        generated_at=FIXED_TIME,
    )
    hyp2 = ReconciliationHypothesis(
        id="hyp-2",
        state_revision=0,
        bank_allocations=(
            BankAllocation(bank_item_id="bank-2", amount_units="1000000"),
        ),
        book_allocations=(
            BookAllocation(book_item_id="book-2", amount_units="1000000"),
        ),
        eligibility=Eligibility.SELECTABLE,
        utility=100,
        generated_at=FIXED_TIME,
    )
    live._put_reconciliation_hypothesis(hyp1)
    live._put_reconciliation_hypothesis(hyp2)
    assert len(live.reconciliation_hypotheses) == 2

    # Batch of 2 routing commands
    cmd1 = CreateRoutingDecisionCommand(
        command_id="cmd-1",
        expected_state_revision=0,
        source=CommandSource.ROUTING,
        session_id=live.session_id,
        issued_at=FIXED_TIME,
        routing_decision_id="r-1",
        book_item_id="book-1",
        bank_account_id="account-1",
        decision_source=RoutingDecisionSource.CP_SAT,
        utility=100,
        solver_run_id="run-1",
    )
    cmd2 = CreateRoutingDecisionCommand(
        command_id="cmd-2",
        expected_state_revision=0,
        source=CommandSource.ROUTING,
        session_id=live.session_id,
        issued_at=FIXED_TIME,
        routing_decision_id="r-2",
        book_item_id="book-2",
        bank_account_id="account-2",
        decision_source=RoutingDecisionSource.CP_SAT,
        utility=100,
        solver_run_id="run-1",
    )
    batch = TransitionBatch(
        batch_id="batch-hyp",
        session_id=live.session_id,
        expected_state_revision=0,
        commands=(cmd1, cmd2),
    )

    result = engine.apply_batch(state=live, batch=batch)
    assert result.status is TransitionStatus.APPLIED
    assert len(live.reconciliation_hypotheses) == 0

    # Exactly ONE HYPOTHESES_INVALIDATED event
    hyp_events = [
        e for e in result.delta.events
        if e.event_type == StateEventType.HYPOTHESES_INVALIDATED
    ]
    assert len(hyp_events) == 1
    assert set(hyp_events[0].affected_artifact_ids) == {"hyp-1", "hyp-2"}


def test_all_events_same_resulting_revision() -> None:
    live, repo, engine, _ = _setup_world()

    cmd1 = CreateRoutingDecisionCommand(
        command_id="cmd-1",
        expected_state_revision=0,
        source=CommandSource.ROUTING,
        session_id=live.session_id,
        issued_at=FIXED_TIME,
        routing_decision_id="r-1",
        book_item_id="book-1",
        bank_account_id="account-1",
        decision_source=RoutingDecisionSource.CP_SAT,
        utility=100,
        solver_run_id="run-1",
    )
    cmd2 = CreateClassificationCommand(
        command_id="cmd-2",
        expected_state_revision=0,
        source=CommandSource.ASE_DAG,
        session_id=live.session_id,
        issued_at=FIXED_TIME,
        classification_id="c-1",
        book_item_id="book-2",
        account_code="6111",
        classification_source=ClassificationSource.ASE_DAG,
    )
    batch = TransitionBatch(
        batch_id="batch-events",
        session_id=live.session_id,
        expected_state_revision=0,
        commands=(cmd1, cmd2),
    )

    result = engine.apply_batch(state=live, batch=batch)
    assert result.status is TransitionStatus.APPLIED
    assert result.resulting_state_revision == 1
    assert len(result.delta.events) >= 2
    for event in result.delta.events:
        assert event.state_revision == 1


def test_per_command_event_provenance() -> None:
    live, repo, engine, _ = _setup_world()

    cmd1 = CreateRoutingDecisionCommand(
        command_id="cmd-source-1",
        expected_state_revision=0,
        source=CommandSource.ROUTING,
        session_id=live.session_id,
        issued_at=FIXED_TIME,
        routing_decision_id="r-1",
        book_item_id="book-1",
        bank_account_id="account-1",
        decision_source=RoutingDecisionSource.CP_SAT,
        utility=100,
        solver_run_id="run-1",
    )
    cmd2 = CreateClassificationCommand(
        command_id="cmd-source-2",
        expected_state_revision=0,
        source=CommandSource.ASE_DAG,
        session_id=live.session_id,
        issued_at=FIXED_TIME,
        classification_id="c-1",
        book_item_id="book-2",
        account_code="6111",
        classification_source=ClassificationSource.ASE_DAG,
    )
    batch = TransitionBatch(
        batch_id="batch-prov",
        session_id=live.session_id,
        expected_state_revision=0,
        commands=(cmd1, cmd2),
    )

    result = engine.apply_batch(state=live, batch=batch)
    assert result.status is TransitionStatus.APPLIED

    event1 = next(
        e for e in result.delta.events
        if e.event_type == StateEventType.ROUTING_DECISION_CREATED
    )
    assert event1.source_command_id == "cmd-source-1"

    event2 = next(
        e for e in result.delta.events
        if e.event_type == StateEventType.CLASSIFICATION_CREATED
    )
    assert event2.source_command_id == "cmd-source-2"


def test_one_persistence_commit_and_state_revision_increment() -> None:
    live, repo, engine, _ = _setup_world()

    cmd1 = CreateRoutingDecisionCommand(
        command_id="cmd-1",
        expected_state_revision=0,
        source=CommandSource.ROUTING,
        session_id=live.session_id,
        issued_at=FIXED_TIME,
        routing_decision_id="r-1",
        book_item_id="book-1",
        bank_account_id="account-1",
        decision_source=RoutingDecisionSource.CP_SAT,
        utility=100,
        solver_run_id="run-1",
    )
    cmd2 = CreateRoutingDecisionCommand(
        command_id="cmd-2",
        expected_state_revision=0,
        source=CommandSource.ROUTING,
        session_id=live.session_id,
        issued_at=FIXED_TIME,
        routing_decision_id="r-2",
        book_item_id="book-2",
        bank_account_id="account-2",
        decision_source=RoutingDecisionSource.CP_SAT,
        utility=200,
        solver_run_id="run-1",
    )
    batch = TransitionBatch(
        batch_id="batch-one-commit",
        session_id=live.session_id,
        expected_state_revision=0,
        commands=(cmd1, cmd2),
    )

    commit_count = 0
    original_commit = repo.commit

    def counted_commit(*args, **kwargs):
        nonlocal commit_count
        commit_count += 1
        return original_commit(*args, **kwargs)

    with patch.object(repo, "commit", side_effect=counted_commit):
        result = engine.apply_batch(state=live, batch=batch)

    assert result.status is TransitionStatus.APPLIED
    assert commit_count == 1
    assert live.revision == 1
    assert live.persistence_revision == 1
    assert repo.current_revision(company_id="atlas") == 1


def test_destroy_and_rehydrate_reconstructs_full_batch_result() -> None:
    live, repo, engine, hydrator = _setup_world()

    cmd1 = CreateRoutingDecisionCommand(
        command_id="cmd-1",
        expected_state_revision=0,
        source=CommandSource.ROUTING,
        session_id=live.session_id,
        issued_at=FIXED_TIME,
        routing_decision_id="r-1",
        book_item_id="book-1",
        bank_account_id="account-1",
        decision_source=RoutingDecisionSource.CP_SAT,
        utility=100,
        solver_run_id="run-1",
    )
    cmd2 = CreateClassificationCommand(
        command_id="cmd-2",
        expected_state_revision=0,
        source=CommandSource.ASE_DAG,
        session_id=live.session_id,
        issued_at=FIXED_TIME,
        classification_id="c-1",
        book_item_id="book-2",
        account_code="6111",
        classification_source=ClassificationSource.ASE_DAG,
    )
    batch = TransitionBatch(
        batch_id="batch-rehydrate",
        session_id=live.session_id,
        expected_state_revision=0,
        commands=(cmd1, cmd2),
    )

    result = engine.apply_batch(state=live, batch=batch)
    assert result.status is TransitionStatus.APPLIED

    fingerprint = state_fingerprint(live)
    live.close()

    # Rehydrate
    restored = hydrator.hydrate(company_id="atlas", session_id="rehydrated")
    assert restored.persistence_revision == 1
    assert restored.get_routing_decision("r-1") is not None
    assert restored.get_classification("c-1") is not None
    assert state_fingerprint(restored) == fingerprint


def test_repeated_hypothesis_promotion_rejects() -> None:
    live, repo, engine, _ = _setup_world()

    hyp = ReconciliationHypothesis(
        id="shared-hyp",
        state_revision=0,
        bank_allocations=(
            BankAllocation(bank_item_id="bank-1", amount_units="1000000"),
        ),
        book_allocations=(
            BookAllocation(book_item_id="book-1", amount_units="1000000"),
        ),
        eligibility=Eligibility.SELECTABLE,
        utility=100,
        generated_at=FIXED_TIME,
    )
    live._put_reconciliation_hypothesis(hyp)

    cmd1 = CreateReconciliationCommand(
        command_id="cmd-prom-1",
        expected_state_revision=0,
        source=CommandSource.RECONCILIATION,
        session_id=live.session_id,
        issued_at=FIXED_TIME,
        reconciliation_id="rec-1",
        bank_allocations=hyp.bank_allocations,
        book_allocations=hyp.book_allocations,
        source_hypothesis_id="shared-hyp",
    )
    cmd2 = CreateReconciliationCommand(
        command_id="cmd-prom-2",
        expected_state_revision=0,
        source=CommandSource.RECONCILIATION,
        session_id=live.session_id,
        issued_at=FIXED_TIME,
        reconciliation_id="rec-2",
        bank_allocations=hyp.bank_allocations,
        book_allocations=hyp.book_allocations,
        source_hypothesis_id="shared-hyp",
    )
    batch = TransitionBatch(
        batch_id="batch-dup-hyp",
        session_id=live.session_id,
        expected_state_revision=0,
        commands=(cmd1, cmd2),
    )

    result = engine.apply_batch(state=live, batch=batch)
    assert result.status is TransitionStatus.REJECTED
    assert result.rejection.code is RejectionCode.STALE_HYPOTHESIS
    assert live.revision == 0


def test_repeated_invalidation_rejects() -> None:
    live, repo, engine, _ = _setup_world()

    # Pre-populate route-1
    engine.apply(
        state=live,
        command=CreateRoutingDecisionCommand(
            command_id="setup-route",
            expected_state_revision=0,
            source=CommandSource.ROUTING,
            session_id=live.session_id,
            issued_at=FIXED_TIME,
            routing_decision_id="route-1",
            book_item_id="book-1",
            bank_account_id="account-1",
            decision_source=RoutingDecisionSource.CP_SAT,
            utility=100,
            solver_run_id="run-1",
        ),
    )
    assert live.revision == 1

    cmd1 = InvalidateRoutingDecisionCommand(
        command_id="inv-cmd-1",
        expected_state_revision=1,
        source=CommandSource.HUMAN,
        session_id=live.session_id,
        issued_at=FIXED_TIME,
        invalidation_id="inv-1",
        routing_decision_id="route-1",
        reason="correction-1",
    )
    cmd2 = InvalidateRoutingDecisionCommand(
        command_id="inv-cmd-2",
        expected_state_revision=1,
        source=CommandSource.HUMAN,
        session_id=live.session_id,
        issued_at=FIXED_TIME,
        invalidation_id="inv-2",
        routing_decision_id="route-1",
        reason="correction-2",
    )
    batch = TransitionBatch(
        batch_id="batch-dup-inv",
        session_id=live.session_id,
        expected_state_revision=1,
        commands=(cmd1, cmd2),
    )

    result = engine.apply_batch(state=live, batch=batch)
    assert result.status is TransitionStatus.REJECTED
    assert result.rejection.code is RejectionCode.RESULTING_STATE_INVALID
    assert live.revision == 1


def test_committed_state_application_error_semantics() -> None:
    live, repo, engine, _ = _setup_world()

    cmd = CreateRoutingDecisionCommand(
        command_id="cmd-fail-live",
        expected_state_revision=0,
        source=CommandSource.ROUTING,
        session_id=live.session_id,
        issued_at=FIXED_TIME,
        routing_decision_id="route-1",
        book_item_id="book-1",
        bank_account_id="account-1",
        decision_source=RoutingDecisionSource.CP_SAT,
        utility=100,
        solver_run_id="run-1",
    )
    batch = TransitionBatch(
        batch_id="batch-catastrophic",
        session_id=live.session_id,
        expected_state_revision=0,
        commands=(cmd,),
    )

    with patch.object(
        TransitionEngine,
        "_apply_delta",
        side_effect=RuntimeError("Hardware failure during live state delta"),
    ):
        with pytest.raises(CommittedStateApplicationError) as exc_info:
            engine.apply_batch(state=live, batch=batch)

    # Persistence did commit, but live state errored and was forcibly closed
    assert exc_info.value.commit is not None
    assert exc_info.value.__cause__ is not None
    assert repo.current_revision(company_id="atlas") == 1
    assert live.is_closed
