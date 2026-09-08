from __future__ import annotations

from datetime import timedelta

from bookkeeping_state_eval.domain.classifications import ClassificationSource
from bookkeeping_state_eval.domain.commands import (
    CommandSource,
    CreateClassificationCommand,
    CreateReconciliationCommand,
    CreateRoutingDecisionCommand,
    InvalidateClassificationCommand,
    InvalidateReconciliationCommand,
)
from bookkeeping_state_eval.domain.hypotheses import ReconciliationHypothesis
from bookkeeping_state_eval.domain.reconciliations import BankAllocation, BookAllocation
from bookkeeping_state_eval.domain.routing import RoutingDecisionSource
from bookkeeping_state_eval.hydration.hydrator import BookkeepingHydrator
from bookkeeping_state_eval.persistence.in_memory import InMemoryBookkeepingRepository
from bookkeeping_state_eval.state.fingerprint import artifact_fingerprint, state_fingerprint
from bookkeeping_state_eval.state.queries import BookkeepingQueries
from bookkeeping_state_eval.transitions.engine import TransitionEngine
from bookkeeping_state_eval.transitions.result import TransitionStatus
from tests.factories import FIXED_TIME, account, bank_item, book_item, snapshot


def test_destroy_and_rehydrate_reconstructs_truth_but_not_runtime_state() -> None:
    repository = InMemoryBookkeepingRepository(
        initial_snapshots=[snapshot(bank_accounts=(account(),), bank_items=(bank_item(),), book_items=(book_item(),))]
    )
    times = iter((FIXED_TIME, FIXED_TIME + timedelta(hours=3)))
    hydrator = BookkeepingHydrator(repository=repository, clock=lambda: next(times))
    first = hydrator.hydrate(company_id="atlas", session_id="first-session")
    engine = TransitionEngine(repository=repository)

    def apply(command):
        result = engine.apply(state=first, command=command)
        assert result.status is TransitionStatus.APPLIED

    apply(CreateRoutingDecisionCommand(
        command_id="route-1", expected_state_revision=0, source=CommandSource.ROUTING,
        session_id=first.session_id, issued_at=FIXED_TIME, routing_decision_id="route-1",
        book_item_id="book-1", bank_account_id="account-1", decision_source=RoutingDecisionSource.DETERMINISTIC_RULE,
    ))
    apply(CreateRoutingDecisionCommand(
        command_id="route-2", expected_state_revision=1, source=CommandSource.ROUTING,
        session_id=first.session_id, issued_at=FIXED_TIME, routing_decision_id="route-2",
        book_item_id="book-1", bank_account_id="account-1", decision_source=RoutingDecisionSource.HUMAN,
        supersedes_routing_decision_id="route-1",
    ))
    apply(CreateClassificationCommand(
        command_id="class-1", expected_state_revision=2, source=CommandSource.ASE_DAG,
        session_id=first.session_id, issued_at=FIXED_TIME, classification_id="class-1",
        book_item_id="book-1", account_code="6111", classification_source=ClassificationSource.ASE_DAG,
    ))
    apply(CreateClassificationCommand(
        command_id="class-2", expected_state_revision=3, source=CommandSource.ASE_DAG,
        session_id=first.session_id, issued_at=FIXED_TIME, classification_id="class-2",
        book_item_id="book-1", account_code="6125", classification_source=ClassificationSource.ASE_DAG,
        supersedes_classification_id="class-1",
    ))
    apply(InvalidateClassificationCommand(
        command_id="invalidate-class-2", expected_state_revision=4, source=CommandSource.HUMAN,
        session_id=first.session_id, issued_at=FIXED_TIME, invalidation_id="invalidate-class-2",
        classification_id="class-2", reason="wrong account",
    ))
    apply(CreateReconciliationCommand(
        command_id="rec-1", expected_state_revision=5, source=CommandSource.RECONCILIATION,
        session_id=first.session_id, issued_at=FIXED_TIME, reconciliation_id="rec-1",
        bank_allocations=(BankAllocation(bank_item_id="bank-1", amount_units="1000000"),),
        book_allocations=(BookAllocation(book_item_id="book-1", amount_units="1000000"),),
    ))
    apply(InvalidateReconciliationCommand(
        command_id="invalidate-rec-1", expected_state_revision=6, source=CommandSource.HUMAN,
        session_id=first.session_id, issued_at=FIXED_TIME, invalidation_id="invalidate-rec-1",
        reconciliation_id="rec-1", reason="replace allocation",
    ))
    apply(CreateReconciliationCommand(
        command_id="rec-2", expected_state_revision=7, source=CommandSource.RECONCILIATION,
        session_id=first.session_id, issued_at=FIXED_TIME, reconciliation_id="rec-2",
        bank_allocations=(BankAllocation(bank_item_id="bank-1", amount_units="1000000"),),
        book_allocations=(BookAllocation(book_item_id="book-1", amount_units="1000000"),),
    ))
    first._put_reconciliation_hypothesis(ReconciliationHypothesis(
        id="runtime-only", utility=1,
        bank_allocations=(BankAllocation(bank_item_id="bank-1", amount_units="1"),),
        book_allocations=(BookAllocation(book_item_id="book-1", amount_units="1"),),
        state_revision=8, generated_at=FIXED_TIME,
    ))
    before_artifacts = artifact_fingerprint(first)
    before_semantics = state_fingerprint(first)
    before_queries = BookkeepingQueries(first)
    expected_remaining = before_queries.book_remaining_units("book-1")
    expected_route = before_queries.active_route("book-1").id
    assert first.events and first.reconciliation_hypotheses
    first.close()

    restored = hydrator.hydrate(company_id="atlas", session_id="second-session")
    restored_queries = BookkeepingQueries(restored)
    assert restored.revision == 0
    assert restored.persistence_revision == 8
    assert restored.session_id != "first-session"
    assert restored.runtime.hydrated_at != FIXED_TIME
    assert artifact_fingerprint(restored) == before_artifacts
    assert state_fingerprint(restored) == before_semantics
    assert restored_queries.book_remaining_units("book-1") == expected_remaining == 0
    assert restored_queries.active_route("book-1").id == expected_route == "route-2"
    assert restored_queries.active_classification("book-1") is None  # invalidating C2 does not resurrect C1.
    assert tuple(item.id for item in restored_queries.reconciliations_for_bank_item("bank-1")) == ("rec-2",)
    assert restored.events == ()
    assert restored.reconciliation_hypotheses == {}
