from __future__ import annotations

from bookkeeping_state.domain.classifications import ClassificationSource
from bookkeeping_state.domain.commands import (
    CommandSource,
    CreateClassificationCommand,
    CreateReconciliationCommand,
    CreateRoutingDecisionCommand,
)
from bookkeeping_state.domain.reconciliations import BankAllocation, BookAllocation
from bookkeeping_state.domain.routing import RoutingDecisionSource
from bookkeeping_state.hydration.hydrator import BookkeepingHydrator
from bookkeeping_state_eval.persistence.in_memory import InMemoryBookkeepingRepository
from bookkeeping_state.state.fingerprint import state_fingerprint
from bookkeeping_state.state.queries import BookkeepingQueries
from bookkeeping_state.state.validation import validate_state
from bookkeeping_state.transitions.engine import TransitionEngine
from bookkeeping_state.transitions.result import TransitionStatus
from tests.factories import (
    FIXED_TIME,
    account,
    bank_item,
    book_item,
    counterparty,
    document,
    snapshot,
)


def test_full_pipeline_admits_truth_only_through_atomic_transitions() -> None:
    repository = InMemoryBookkeepingRepository(
        initial_snapshots=[
            snapshot(
                bank_accounts=(account(), account("account-2")),
                bank_items=(
                    bank_item("bank-supplier", amount=1_000_000, provenance_refs=("doc-supplier",)),
                    bank_item("bank-office", amount=300_000, provenance_refs=("doc-office",)),
                ),
                book_items=(
                    book_item("book-supplier", amount=1_300_000, counterparty_id="supplier", provenance_refs=("doc-supplier",)),
                    book_item("book-office", amount=300_000, counterparty_id="supplier", provenance_refs=("doc-office",)),
                ),
                documents=(document("doc-supplier"), document("doc-office")),
                counterparties=(counterparty("supplier"),),
            )
        ]
    )
    state = BookkeepingHydrator(repository=repository, clock=lambda: FIXED_TIME).hydrate(
        company_id="atlas", session_id="pipeline-session"
    )
    engine = TransitionEngine(repository=repository)
    before = state_fingerprint(state)

    def apply(command):
        result = engine.apply(state=state, command=command)
        assert result.status is TransitionStatus.APPLIED

    # Routing proposals are only made true through the engine.
    apply(CreateRoutingDecisionCommand(
        command_id="route-supplier", expected_state_revision=0, source=CommandSource.ROUTING,
        session_id=state.session_id, issued_at=FIXED_TIME, routing_decision_id="route-supplier",
        book_item_id="book-supplier", bank_account_id="account-1",
        decision_source=RoutingDecisionSource.DETERMINISTIC_RULE,
    ))
    apply(CreateRoutingDecisionCommand(
        command_id="route-office", expected_state_revision=1, source=CommandSource.ROUTING,
        session_id=state.session_id, issued_at=FIXED_TIME, routing_decision_id="route-office",
        book_item_id="book-office", bank_account_id="account-1",
        decision_source=RoutingDecisionSource.DETERMINISTIC_RULE,
    ))
    # Classification has a later expected revision and cannot mutate a BookItem.
    apply(CreateClassificationCommand(
        command_id="class-supplier", expected_state_revision=2, source=CommandSource.ASE_DAG,
        session_id=state.session_id, issued_at=FIXED_TIME, classification_id="class-supplier",
        book_item_id="book-supplier", account_code="4411", classification_source=ClassificationSource.ASE_DAG,
        evidence_refs=("doc-supplier",),
    ))
    apply(CreateClassificationCommand(
        command_id="class-office", expected_state_revision=3, source=CommandSource.ASE_DAG,
        session_id=state.session_id, issued_at=FIXED_TIME, classification_id="class-office",
        book_item_id="book-office", account_code="6125", classification_source=ClassificationSource.ASE_DAG,
        evidence_refs=("doc-office",),
    ))
    # Reconciliation consumes residual capacity in exact integer units.
    apply(CreateReconciliationCommand(
        command_id="reconcile-supplier", expected_state_revision=4, source=CommandSource.RECONCILIATION,
        session_id=state.session_id, issued_at=FIXED_TIME, reconciliation_id="rec-supplier",
        bank_allocations=(BankAllocation(bank_item_id="bank-supplier", amount_units="1000000"),),
        book_allocations=(BookAllocation(book_item_id="book-supplier", amount_units="1000000"),),
        evidence_refs=("doc-supplier",),
    ))
    apply(CreateReconciliationCommand(
        command_id="reconcile-office", expected_state_revision=5, source=CommandSource.RECONCILIATION,
        session_id=state.session_id, issued_at=FIXED_TIME, reconciliation_id="rec-office",
        bank_allocations=(BankAllocation(bank_item_id="bank-office", amount_units="300000"),),
        book_allocations=(BookAllocation(book_item_id="book-office", amount_units="300000"),),
        evidence_refs=("doc-office",),
    ))

    queries = BookkeepingQueries(state)
    assert state.revision == repository.current_revision(company_id="atlas") == 6
    assert state_fingerprint(state) != before
    assert queries.book_remaining_units("book-supplier") == 300_000
    assert queries.book_remaining_units("book-office") == 0
    assert tuple(item.id for item in queries.partially_reconciled_book_items()) == ("book-supplier",)
    assert tuple(item.id for item in queries.fully_reconciled_bank_items()) == ("bank-office", "bank-supplier")
    assert queries.active_route("book-supplier").id == "route-supplier"
    assert queries.active_classification("book-office").id == "class-office"
    assert queries.related_to("book-supplier")
    assert validate_state(state).is_valid
