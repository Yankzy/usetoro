from __future__ import annotations

from datetime import timedelta

from bookkeeping_state_eval.domain.events import StateEvent, StateEventType
from bookkeeping_state_eval.domain.hypotheses import ReconciliationHypothesis
from bookkeeping_state_eval.domain.reconciliations import BankAllocation, BookAllocation
from bookkeeping_state_eval.hydration.hydrator import BookkeepingHydrator
from bookkeeping_state_eval.persistence.in_memory import InMemoryBookkeepingRepository
from bookkeeping_state_eval.state.fingerprint import (
    artifact_fingerprint,
    canonical_json_bytes,
    canonical_state_projection,
    state_fingerprint,
    states_are_equivalent,
)
from tests.factories import (
    FIXED_TIME,
    account,
    bank_item,
    book_item,
    reconciliation,
    routing,
    routing_invalidation,
    snapshot,
    state,
)


def test_fingerprints_ignore_runtime_only_identity_and_are_order_independent() -> None:
    artifacts = dict(
        bank_accounts=(account("account-2"), account("account-1")),
        bank_items=(bank_item("bank-2", account_id="account-2"), bank_item("bank-1")),
        book_items=(book_item("book-2"), book_item("book-1")),
    )
    first = state(**artifacts, session_id="session-a", revision=0)
    second = state(
        bank_accounts=tuple(reversed(artifacts["bank_accounts"])),
        bank_items=tuple(reversed(artifacts["bank_items"])),
        book_items=tuple(reversed(artifacts["book_items"])),
        session_id="session-b",
        revision=99,
    )

    assert artifact_fingerprint(first) == artifact_fingerprint(second)
    assert state_fingerprint(first) == state_fingerprint(second)
    assert states_are_equivalent(first, second)
    assert not states_are_equivalent(first, state(bank_accounts=(account(),)))
    assert canonical_json_bytes({"b": 1, "a": [2]}) == canonical_json_bytes({"a": [2], "b": 1})


def test_runtime_hypotheses_and_events_do_not_change_semantic_fingerprint() -> None:
    live = state(bank_accounts=(account(),), bank_items=(bank_item(),), book_items=(book_item(),))
    before = state_fingerprint(live)
    live._put_reconciliation_hypothesis(
        ReconciliationHypothesis(
            id="hypothesis-1",
            utility=1,
            bank_allocations=(BankAllocation(bank_item_id="bank-1", amount_units="1000000"),),
            book_allocations=(BookAllocation(book_item_id="book-1", amount_units="1000000"),),
            state_revision=0,
            generated_at=FIXED_TIME,
        )
    )
    live._append_event(
        StateEvent(
            id="event-1",
            event_type=StateEventType.DERIVED_STATE_REFRESHED,
            state_revision=0,
            occurred_at=FIXED_TIME,
        )
    )

    assert state_fingerprint(live) == before


def test_durable_artifacts_supersession_and_invalidation_change_fingerprints() -> None:
    base = state(bank_accounts=(account(),), book_items=(book_item(),))
    routed = state(bank_accounts=(account(),), book_items=(book_item(),), routing_decisions=(routing(),))
    superseded = state(
        bank_accounts=(account(),),
        book_items=(book_item(),),
        routing_decisions=(routing("route-1"), routing("route-2", supersedes="route-1")),
    )
    invalidated = state(
        bank_accounts=(account(),),
        book_items=(book_item(),),
        routing_decisions=(routing(),),
        routing_invalidations=(routing_invalidation(),),
    )

    assert artifact_fingerprint(base) != artifact_fingerprint(routed)
    assert state_fingerprint(base) != state_fingerprint(routed)
    assert state_fingerprint(routed) != state_fingerprint(superseded)
    assert state_fingerprint(routed) != state_fingerprint(invalidated)

    residual_before = state(bank_accounts=(account(),), bank_items=(bank_item(),), book_items=(book_item(),))
    residual_after = state(
        bank_accounts=(account(),),
        bank_items=(bank_item(),),
        book_items=(book_item(),),
        reconciliations=(reconciliation(),),
    )
    assert state_fingerprint(residual_before) != state_fingerprint(residual_after)


def test_destroy_and_rehydrate_preserve_durable_and_semantic_fingerprints() -> None:
    durable = snapshot(
        bank_accounts=(account(),),
        bank_items=(bank_item(),),
        book_items=(book_item(amount=1_500_000),),
        routing_decisions=(routing(),),
        reconciliations=(reconciliation(book=(("book-1", 1_000_000),)),),
    )
    repository = InMemoryBookkeepingRepository(initial_snapshots=[durable])
    clock_values = iter((FIXED_TIME, FIXED_TIME + timedelta(hours=1)))
    hydrator = BookkeepingHydrator(repository=repository, clock=lambda: next(clock_values))
    first = hydrator.hydrate(company_id="atlas", session_id="session-a")
    expected_artifacts = artifact_fingerprint(first)
    expected_state = state_fingerprint(first)
    projection = canonical_state_projection(first)
    first.close()

    restored = hydrator.hydrate(company_id="atlas", session_id="session-b")
    assert restored.revision == 0
    assert restored.runtime.hydrated_at != FIXED_TIME
    assert artifact_fingerprint(restored) == expected_artifacts
    assert state_fingerprint(restored) == expected_state
    assert canonical_state_projection(restored) == projection
    assert states_are_equivalent(restored, restored)
