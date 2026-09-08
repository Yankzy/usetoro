from __future__ import annotations

import pytest

from bookkeeping_state.domain.commands import (
    CommandSource,
    CreateClassificationCommand,
    CreateRoutingDecisionCommand,
)
from bookkeeping_state.domain.classifications import ClassificationSource
from bookkeeping_state.domain.routing import RoutingDecisionSource
from bookkeeping_state.hydration.hydrator import BookkeepingHydrator
from bookkeeping_state_eval.persistence.in_memory import InMemoryBookkeepingRepository
from bookkeeping_state.state.queries import BookkeepingQueries
from bookkeeping_state.transitions.engine import TransitionEngine
from bookkeeping_state.transitions.result import TransitionStatus
from tests.factories import (
    FIXED_TIME,
    account,
    bank_item,
    book_item,
    classification,
    counterparty,
    document,
    reconciliation,
    routing,
    snapshot,
)


def _live_state():
    repository = InMemoryBookkeepingRepository(
        initial_snapshots=[
            snapshot(
                bank_accounts=(account(),),
                bank_items=(bank_item(provenance_refs=("doc-1",)),),
                book_items=(book_item(counterparty_id="cp-1", provenance_refs=("doc-1",)),),
                documents=(document(),),
                counterparties=(counterparty("cp-1"),),
                routing_decisions=(routing(),),
                classifications=(classification(evidence_refs=("doc-1",)),),
                reconciliations=(reconciliation(evidence_refs=("doc-1",)),),
            )
        ]
    )
    state = BookkeepingHydrator(
        repository=repository, clock=lambda: FIXED_TIME
    ).hydrate(company_id="atlas", session_id="query-session")
    return state, repository


def test_queries_expose_public_artifacts_indexes_derived_values_and_evidence() -> None:
    live, _ = _live_state()
    queries = BookkeepingQueries(live)

    assert queries.bank_account("account-1") == live.bank_accounts["account-1"]
    assert queries.bank_item("missing") is None
    assert queries.book_item("book-1") == live.book_items["book-1"]
    assert queries.counterparty("cp-1") == live.counterparties["cp-1"]
    assert queries.bank_items_for_account("account-1") == (live.bank_items["bank-1"],)
    assert queries.book_items_for_counterparty("cp-1") == (live.book_items["book-1"],)
    assert queries.active_route("book-1").id == "route-1"
    assert queries.active_classification("book-1").id == "classification-1"
    assert queries.bank_allocated_units("bank-1") == 1_000_000
    assert queries.book_remaining_units("book-1") == 0
    assert queries.fully_reconciled_bank_items() == (live.bank_items["bank-1"],)
    assert queries.fully_reconciled_book_items() == (live.book_items["book-1"],)
    assert tuple(item.id for item in queries.reconciliations_for_bank_item("bank-1")) == ("reconciliation-1",)
    assert tuple(item.id for item in queries.reconciliations_for_book_item("book-1")) == ("reconciliation-1",)
    assert queries.evidence_for("book-1") == (live.documents["doc-1"],)
    assert queries.evidence_for("classification-1") == (live.documents["doc-1"],)
    assert queries.evidence_for("reconciliation-1") == (live.documents["doc-1"],)
    assert queries.evidence_for("missing") == ()
    assert queries.related_to("book-1")
    with pytest.raises(KeyError):
        queries.bank_remaining_units("missing")


def test_query_projection_is_rebuilt_after_accepted_state_change() -> None:
    repository = InMemoryBookkeepingRepository(
        initial_snapshots=[snapshot(bank_accounts=(account(),), book_items=(book_item(),))]
    )
    live = BookkeepingHydrator(repository=repository, clock=lambda: FIXED_TIME).hydrate(
        company_id="atlas", session_id="query-session"
    )
    queries = BookkeepingQueries(live)
    cached_before = queries.derived
    assert queries.relationships.state_revision == 0

    result = TransitionEngine(repository=repository).apply(
        state=live,
        command=CreateRoutingDecisionCommand(
            command_id="route-command",
            expected_state_revision=0,
            source=CommandSource.ROUTING,
            session_id="query-session",
            issued_at=FIXED_TIME,
            routing_decision_id="route-1",
            book_item_id="book-1",
            bank_account_id="account-1",
            decision_source=RoutingDecisionSource.DETERMINISTIC_RULE,
        ),
    )

    assert result.status is TransitionStatus.APPLIED
    assert queries.active_route("book-1").id == "route-1"
    assert queries.indexes.state_revision == 1
    assert queries.relationships.state_revision == 1
    assert queries.derived is not cached_before


def test_queries_refresh_classification_without_reusing_s0_projection() -> None:
    repository = InMemoryBookkeepingRepository(
        initial_snapshots=[snapshot(book_items=(book_item(),))]
    )
    live = BookkeepingHydrator(repository=repository, clock=lambda: FIXED_TIME).hydrate(
        company_id="atlas", session_id="query-session"
    )
    queries = BookkeepingQueries(live)
    assert queries.active_classification("book-1") is None

    result = TransitionEngine(repository=repository).apply(
        state=live,
        command=CreateClassificationCommand(
            command_id="classification-command",
            expected_state_revision=0,
            source=CommandSource.ASE_DAG,
            session_id="query-session",
            issued_at=FIXED_TIME,
            classification_id="classification-1",
            book_item_id="book-1",
            account_code="6111",
            classification_source=ClassificationSource.ASE_DAG,
        ),
    )

    assert result.status is TransitionStatus.APPLIED
    assert queries.active_classification("book-1").account_code == "6111"
