from __future__ import annotations

import importlib
from unittest.mock import patch
import pytest

from bookkeeping_state_eval.domain.commands import (
    CommandSource,
    CreateClassificationCommand,
    CreateRoutingDecisionCommand,
)
from bookkeeping_state_eval.domain.classifications import (
    ClassificationSource,
)
from bookkeeping_state_eval.domain.routing import (
    RoutingDecisionSource,
)
from bookkeeping_state_eval.hydration.hydrator import (
    BookkeepingHydrator,
)
from bookkeeping_state_eval.persistence.in_memory import (
    InMemoryBookkeepingRepository,
)
from bookkeeping_state_eval.persistence.repository import (
    BookkeepingSnapshot,
)
from bookkeeping_state_eval.state.bookkeeping_state import (
    BookkeepingStateClosedError,
)
from bookkeeping_state_eval.state.queries import (
    BookkeepingQueries,
)
from bookkeeping_state_eval.transitions.batch import (
    TransitionBatch,
)
from bookkeeping_state_eval.transitions.delta import (
    StateDelta,
)
from bookkeeping_state_eval.transitions.engine import (
    CommittedStateApplicationError,
    RepositoryContractError,
    TransitionEngine,
)
from bookkeeping_state_eval.transitions.result import (
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
)


def _setup_world(
    *,
    books: tuple[str, ...] = ("book-1", "book-2"),
    banks: tuple[str, ...] = ("bank-1", "bank-2"),
    accounts: tuple[str, ...] = ("account-1",),
):
    account_objs = tuple(account(acc_id) for acc_id in accounts)
    bank_objs = [
        bank_item(b_id, account_id=accounts[0], amount=1_000_000)
        for b_id in banks
    ]
    book_objs = tuple(book_item(b_id, amount=1_000_000) for b_id in books)

    snap = BookkeepingSnapshot(
        persistence_revision=0,
        context=context(),
        bank_accounts=account_objs,
        bank_items=tuple(bank_objs),
        book_items=book_objs,
    )

    repo = InMemoryBookkeepingRepository(initial_snapshots=[snap])
    hydrator = BookkeepingHydrator(
        repository=repo,
        clock=lambda: FIXED_TIME,
        session_id_factory=lambda: "session-post-commit",
    )
    live = hydrator.hydrate(company_id="atlas")
    engine = TransitionEngine(repository=repo)
    return live, repo, engine, hydrator


def _make_routing_cmd(live, book_id="book-1", cmd_id="cmd-r1", route_id="route-1"):
    return CreateRoutingDecisionCommand(
        command_id=cmd_id,
        expected_state_revision=live.revision,
        source=CommandSource.ROUTING,
        session_id=live.session_id,
        issued_at=FIXED_TIME,
        routing_decision_id=route_id,
        book_item_id=book_id,
        bank_account_id="account-1",
        decision_source=RoutingDecisionSource.CP_SAT,
        utility=100,
        solver_run_id="run-1",
    )


class TestBatchPartitionerRemoval:
    def test_batch_partitioner_is_deleted_and_unimportable(self):
        with pytest.raises(ModuleNotFoundError):
            importlib.import_module("bookkeeping_state_eval.transitions.partitioner")


class TestPreCommitPreparationFailures:
    def test_apply_batch_pre_commit_event_failure_causes_no_persistence_change(self):
        live, repo, engine, _ = _setup_world()
        cmd1 = _make_routing_cmd(live, "book-1", "cmd-r1", "route-1")
        cmd2 = _make_routing_cmd(live, "book-2", "cmd-r2", "route-2")
        batch = TransitionBatch(
            batch_id="batch-precommit-fail",
            session_id=live.session_id,
            expected_state_revision=live.revision,
            commands=(cmd1, cmd2),
        )

        with patch.object(
            TransitionEngine,
            "_build_batch_events",
            side_effect=ValueError("Event schema failure during pre-commit preparation"),
        ):
            with pytest.raises(ValueError, match="Event schema failure"):
                engine.apply_batch(state=live, batch=batch)

        # Invariant: Persistence was NOT touched, live state is NOT closed and NOT mutated
        assert repo.current_revision(company_id="atlas") == 0
        assert not live.is_closed
        assert live.revision == 0
        q = BookkeepingQueries(live)
        assert q.active_route("book-1") is None

    def test_apply_single_pre_commit_event_failure_causes_no_persistence_change(self):
        live, repo, engine, _ = _setup_world()
        cmd = _make_routing_cmd(live, "book-1", "cmd-r1", "route-1")

        with patch.object(
            TransitionEngine,
            "_build_events",
            side_effect=ValueError("Event construction error before commit"),
        ):
            with pytest.raises(ValueError, match="Event construction error"):
                engine.apply(state=live, command=cmd)

        assert repo.current_revision(company_id="atlas") == 0
        assert not live.is_closed
        assert live.revision == 0


class TestPostCommitFailureSemantics:
    def test_apply_batch_post_commit_delta_application_failure_closes_state(self):
        live, repo, engine, hydrator = _setup_world()
        cmd1 = _make_routing_cmd(live, "book-1", "cmd-r1", "route-1")
        cmd2 = _make_routing_cmd(live, "book-2", "cmd-r2", "route-2")
        batch = TransitionBatch(
            batch_id="batch-live-fail",
            session_id=live.session_id,
            expected_state_revision=live.revision,
            commands=(cmd1, cmd2),
        )

        original_err = RuntimeError("Hardware fault during in-memory artifact insertion")
        with patch.object(
            TransitionEngine,
            "_apply_delta",
            side_effect=original_err,
        ):
            with pytest.raises(CommittedStateApplicationError) as exc_info:
                engine.apply_batch(state=live, batch=batch)

        # 1. Catastrophic error was raised
        err = exc_info.value
        assert err.commit is not None
        assert err.cause is original_err
        assert err.__cause__ is original_err

        # 2. Persistence successfully committed
        assert repo.current_revision(company_id="atlas") == 1

        # 3. Live state was forcibly closed to prevent stale reads or further corruption
        assert live.is_closed
        with pytest.raises(BookkeepingStateClosedError):
            _ = live.book_items

        # 4. Attempting another transition rejects immediately due to state closure
        cmd3 = CreateRoutingDecisionCommand(
            command_id="cmd-r3",
            expected_state_revision=0,
            source=CommandSource.ROUTING,
            session_id="session-post-commit",
            issued_at=FIXED_TIME,
            routing_decision_id="route-3",
            book_item_id="book-1",
            bank_account_id="account-1",
            decision_source=RoutingDecisionSource.CP_SAT,
            utility=100,
            solver_run_id="run-1",
        )
        res = engine.apply(state=live, command=cmd3)
        assert res.rejection.code == RejectionCode.STATE_CLOSED

        # 5. Documented recovery path: Fresh hydration from repository reconstructs committed truth
        rehydrated = hydrator.hydrate(company_id="atlas")
        assert rehydrated.persistence_revision == 1
        q_rehydrated = BookkeepingQueries(rehydrated)
        assert q_rehydrated.active_route("book-1") is not None
        assert q_rehydrated.active_route("book-2") is not None

    def test_apply_single_post_commit_delta_application_failure_closes_state(self):
        live, repo, engine, hydrator = _setup_world()
        cmd = _make_routing_cmd(live, "book-1", "cmd-single-live-fail", "route-1")

        original_err = RuntimeError("Memory error in live delta application")
        with patch.object(
            TransitionEngine,
            "_apply_delta",
            side_effect=original_err,
        ):
            with pytest.raises(CommittedStateApplicationError) as exc_info:
                engine.apply(state=live, command=cmd)

        assert exc_info.value.cause is original_err
        assert exc_info.value.__cause__ is original_err
        assert repo.current_revision(company_id="atlas") == 1
        assert live.is_closed

        with pytest.raises(BookkeepingStateClosedError):
            _ = live.routing_decisions

        # Fresh hydration recovers committed state
        rehydrated = hydrator.hydrate(company_id="atlas")
        assert rehydrated.persistence_revision == 1
        assert BookkeepingQueries(rehydrated).active_route("book-1") is not None

    def test_apply_batch_post_commit_contract_assertion_failure_closes_state(self):
        live, repo, engine, hydrator = _setup_world()
        cmd = _make_routing_cmd(live, "book-1", "cmd-contract-fail", "route-1")
        batch = TransitionBatch(
            batch_id="batch-contract-fail",
            session_id=live.session_id,
            expected_state_revision=live.revision,
            commands=(cmd,),
        )

        contract_err = RepositoryContractError("Repository returned corrupt commit result")
        with patch.object(
            TransitionEngine,
            "_assert_commit_contract",
            side_effect=contract_err,
        ):
            with pytest.raises(CommittedStateApplicationError) as exc_info:
                engine.apply_batch(state=live, batch=batch)

        assert exc_info.value.cause is contract_err
        assert exc_info.value.__cause__ is contract_err
        assert repo.current_revision(company_id="atlas") == 1
        assert live.is_closed

    def test_apply_batch_post_commit_delta_construction_failure_closes_state(self):
        live, repo, engine, hydrator = _setup_world()
        cmd = _make_routing_cmd(live, "book-1", "cmd-delta-fail", "route-1")
        batch = TransitionBatch(
            batch_id="batch-delta-fail",
            session_id=live.session_id,
            expected_state_revision=live.revision,
            commands=(cmd,),
        )

        delta_err = ValueError("Invalid StateDelta parameters")
        with patch.object(
            StateDelta,
            "from_commit",
            side_effect=delta_err,
        ):
            with pytest.raises(CommittedStateApplicationError) as exc_info:
                engine.apply_batch(state=live, batch=batch)

        assert exc_info.value.cause is delta_err
        assert exc_info.value.__cause__ is delta_err
        assert repo.current_revision(company_id="atlas") == 1
        assert live.is_closed

    def test_apply_noop_contract_failure_closes_state(self):
        # Pre-populate classification
        cls1 = classification("cls-1", book_item_id="book-1", account_code="6111")
        account_obj = account("account-1")
        snap = BookkeepingSnapshot(
            persistence_revision=0,
            context=context(),
            bank_accounts=(account_obj,),
            bank_items=(bank_item("bank-1", account_id="account-1"),),
            book_items=(book_item("book-1"),),
            classifications=(cls1,),
        )
        repo = InMemoryBookkeepingRepository(initial_snapshots=[snap])
        hydrator = BookkeepingHydrator(
            repository=repo,
            clock=lambda: FIXED_TIME,
            session_id_factory=lambda: "session-noop-contract",
        )
        live = hydrator.hydrate(company_id="atlas")
        engine = TransitionEngine(repository=repo)

        # Idempotent classification command (semantic no-op)
        noop_cmd = CreateClassificationCommand(
            command_id="cmd-noop-1",
            expected_state_revision=0,
            session_id=live.session_id,
            issued_at=FIXED_TIME,
            source=CommandSource.ASE_DAG,
            classification_id="cls-1",
            book_item_id="book-1",
            account_code="6111",
            classification_source=ClassificationSource.DETERMINISTIC_RULE,
        )

        with patch.object(
            TransitionEngine,
            "_assert_noop_commit_contract",
            side_effect=RepositoryContractError("Corrupt noop commit"),
        ):
            with pytest.raises(CommittedStateApplicationError):
                engine.apply(state=live, command=noop_cmd)

        assert live.is_closed
