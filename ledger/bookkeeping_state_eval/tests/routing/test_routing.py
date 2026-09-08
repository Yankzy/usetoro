from __future__ import annotations

from datetime import date

import pytest
from pydantic import ValidationError

from bookkeeping_state.domain.classifications import ClassificationSource
from bookkeeping_state.domain.commands import CommandSource, CreateClassificationCommand, InvalidateRoutingDecisionCommand
from bookkeeping_state.domain.routing import RoutingDecisionSource
from bookkeeping_state.domain.enums import Direction
from bookkeeping_state.domain.events import StateEvent, StateEventType
from bookkeeping_state.domain.hypotheses import ReconciliationHypothesis
from bookkeeping_state.domain.reconciliations import BankAllocation, BookAllocation
from bookkeeping_state.hydration.hydrator import BookkeepingHydrator
from bookkeeping_state_eval.persistence.in_memory import InMemoryBookkeepingRepository
from bookkeeping_state.routing.feasibility import (
    build_capacity_buckets,
    build_feasibility_matrix,
    directions_are_compatible,
    evaluate_feasibility,
    find_exact_subset_witness,
)
from bookkeeping_state.routing.models import (
    RoutingAssignment,
    RoutingFeasibilityMatrix,
    RoutingFeasibilityStatus,
    RoutingResult,
    RoutingSemanticScoreMatrix,
    RoutingUnassignedReason,
    UnassignedRoutingItem,
)
from bookkeeping_state.routing.optimizer import optimize_routing
from bookkeeping_state.routing.scorer import (
    RoutingProviderScore,
    RoutingSemanticScoringError,
    RoutingSemanticScoringResponse,
    StaticRoutingSemanticScoreProvider,
    ZeroRoutingSemanticScoreProvider,
    build_semantic_candidates,
    score_feasible_pairs,
)
from bookkeeping_state.routing.service import RoutingService
from bookkeeping_state.routing.view import (
    RoutingBankAccountView,
    RoutingBankItemView,
    RoutingBookItemView,
    RoutingView,
    build_routing_view,
)
from bookkeeping_state.state.fingerprint import artifact_fingerprint, state_fingerprint
from bookkeeping_state.state.queries import BookkeepingQueries
from bookkeeping_state.transitions.engine import TransitionEngine
from bookkeeping_state.transitions.result import RejectionCode, TransitionStatus
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
    state,
)


def _bank(id: str, amount: int, *, day: int = 15, account_id: str = "account-a", currency: str = "MAD", direction: Direction = Direction.BANK_OUTFLOW) -> RoutingBankItemView:
    return RoutingBankItemView(bank_item_id=id, bank_account_id=account_id, date=date(2026, 1, day), remaining_amount_units=str(amount), direction=direction, currency=currency, description=id)


def _book(id: str, amount: int, *, day: int = 15, currency: str = "MAD", direction: Direction = Direction.BOOK_BANK_CREDIT) -> RoutingBookItemView:
    return RoutingBookItemView(book_item_id=id, date=date(2026, 1, day), remaining_amount_units=str(amount), direction=direction, currency=currency, description=id)


def _account_view(id: str = "account-a", *items: RoutingBankItemView, currency: str = "MAD") -> RoutingBankAccountView:
    normalized_items = tuple(
        item.model_copy(update={"bank_account_id": id})
        for item in items
    )
    return RoutingBankAccountView(bank_account_id=id, name=id, currency=currency, institution_name="Bank", bank_items=normalized_items)


def _view(*, accounts: tuple[RoutingBankAccountView, ...] = (), books: tuple[RoutingBookItemView, ...] = (), revision: int = 0) -> RoutingView:
    return RoutingView(company_id="atlas", state_revision=revision, persistence_revision=9, period_start=date(2026, 1, 1), period_end=date(2026, 1, 31), base_currency="MAD", bank_accounts=accounts, book_items=books)


def _optimized(view: RoutingView, scores: dict[tuple[str, str], int] | None = None):
    feasibility = build_feasibility_matrix(view)
    semantic = score_feasible_pairs(view=view, feasibility=feasibility, provider=StaticRoutingSemanticScoreProvider(scores=scores or {}))
    return optimize_routing(view=view, feasibility=feasibility, semantic_scores=semantic, solver_run_id="solver-1")


def test_routing_view_is_bounded_ordered_immutable_and_uses_residual_capacity() -> None:
    cp = counterparty("supplier").model_copy(update={"aliases": ("Atlas", "Supplier")})
    live = state(
        persistence_revision=12,
        bank_accounts=(account("account-b"), account("account-a")),
        bank_items=(
            bank_item("bank-full", amount=100, account_id="account-a"),
            bank_item("bank-partial", amount=150, account_id="account-a"),
            bank_item("bank-other", amount=80, account_id="account-b"),
        ),
        book_items=(
            book_item("book-full", amount=100, counterparty_id="supplier"),
            book_item("book-partial", amount=150, counterparty_id="supplier", provenance_refs=("doc-1",)),
            book_item("book-routed", amount=80),
            book_item("book-open", amount=80),
        ),
        documents=(document(),), counterparties=(cp,),
        routing_decisions=(routing(book_item_id="book-routed", account_id="account-b"),),
        classifications=(classification(book_item_id="book-open"),),
        reconciliations=(
            reconciliation("rec-full", bank=(("bank-full", 100),), book=(("book-full", 100),)),
            reconciliation("rec-partial", bank=(("bank-partial", 50),), book=(("book-partial", 50),)),
        ),
    )
    queries = BookkeepingQueries(live)
    before = state_fingerprint(live)
    view = build_routing_view(queries)

    assert (view.company_id, view.state_revision, view.persistence_revision, view.base_currency) == ("atlas", 0, 12, "MAD")
    assert tuple(item.bank_account_id for item in view.bank_accounts) == ("account-a", "account-b")
    assert tuple(item.bank_item_id for item in view.bank_account("account-a").bank_items) == ("bank-partial",)
    assert view.bank_account("account-a").bank_items[0].remaining_amount_int == 100
    assert tuple(item.book_item_id for item in view.book_items) == ("book-open", "book-partial")
    partial = view.book_item("book-partial")
    assert partial.remaining_amount_int == 100
    assert partial.counterparty_name == "supplier" and partial.counterparty_aliases == ("Atlas", "Supplier")
    assert partial.evidence_document_ids == ("doc-1",)
    assert not hasattr(view, "classifications") and not hasattr(view, "reconciliations") and not hasattr(view, "events")
    assert state_fingerprint(live) == before
    with pytest.raises(ValidationError):
        view.state_revision = 9


def test_routing_view_revision_is_an_immutable_snapshot_after_state_changes() -> None:
    repository = InMemoryBookkeepingRepository(initial_snapshots=[snapshot(bank_accounts=(account(),), book_items=(book_item(),))])
    live = BookkeepingHydrator(repository=repository, clock=lambda: FIXED_TIME).hydrate(company_id="atlas", session_id="routing-view")
    old = build_routing_view(BookkeepingQueries(live))
    result = TransitionEngine(repository=repository).apply(
        state=live,
        command=CreateClassificationCommand(command_id="class", expected_state_revision=0, source=CommandSource.ASE_DAG, session_id=live.session_id, issued_at=FIXED_TIME, classification_id="class-1", book_item_id="book-1", account_code="6111", classification_source=ClassificationSource.ASE_DAG),
    )
    assert result.status is TransitionStatus.APPLIED
    assert old.state_revision == 0
    assert build_routing_view(BookkeepingQueries(live)).state_revision == 1


@pytest.mark.parametrize(
    ("bank_direction", "book_direction", "expected"),
    [
        (Direction.BANK_OUTFLOW, Direction.BOOK_BANK_CREDIT, True),
        (Direction.BANK_INFLOW, Direction.BOOK_BANK_DEBIT, True),
        (Direction.OUTFLOW, Direction.OUTFLOW, True),
        (Direction.INFLOW, Direction.INFLOW, True),
        (Direction.BANK_OUTFLOW, Direction.BOOK_BANK_DEBIT, False),
        (Direction.BANK_INFLOW, Direction.BOOK_BANK_CREDIT, False),
    ],
)
def test_direction_compatibility_is_a_hard_rule(bank_direction, book_direction, expected) -> None:
    assert directions_are_compatible(bank_direction=bank_direction, book_direction=book_direction) is expected


def test_exact_subset_witness_is_exact_deterministic_and_never_reuses_items() -> None:
    single = (_bank("b1", 100),)
    multi = (_bank("b70", 70), _bank("b80", 80), _bank("b20", 20))
    assert find_exact_subset_witness(bank_items=single, target_units=100) == ("b1",)
    assert find_exact_subset_witness(bank_items=multi, target_units=100) == ("b20", "b80")
    assert find_exact_subset_witness(bank_items=(_bank("b70", 70), _bank("b80", 80)), target_units=100) is None
    assert find_exact_subset_witness(bank_items=(_bank("b150", 150), _bank("b60", 60), _bank("b40", 40)), target_units=100) == ("b40", "b60")
    assert find_exact_subset_witness(bank_items=(), target_units=100) is None
    assert find_exact_subset_witness(bank_items=tuple(reversed(multi)), target_units=100) == ("b20", "b80")
    assert find_exact_subset_witness(bank_items=(_bank("only", 60),), target_units=120) is None
    with pytest.raises(ValueError, match="positive"):
        find_exact_subset_witness(bank_items=single, target_units=0)


def test_feasibility_matrix_reports_every_hard_status_and_exact_witnesses() -> None:
    book = _book("book", 100)
    feasible = evaluate_feasibility(book_item=book, bank_account=_account_view("a", _bank("b", 100)))
    assert feasible.status is RoutingFeasibilityStatus.FEASIBLE and feasible.witness_bank_item_ids == ("b",) and feasible.witness_total_units == "100"
    assert evaluate_feasibility(book_item=book, bank_account=_account_view("empty")).status is RoutingFeasibilityStatus.NO_BANK_ITEMS
    assert evaluate_feasibility(book_item=book, bank_account=_account_view("usd", _bank("usd-item", 100, account_id="usd", currency="USD"), currency="USD")).status is RoutingFeasibilityStatus.CURRENCY_MISMATCH
    assert evaluate_feasibility(book_item=book, bank_account=_account_view("in", _bank("in-item", 100, account_id="in", direction=Direction.BANK_INFLOW))).status is RoutingFeasibilityStatus.DIRECTION_MISMATCH
    no_subset = evaluate_feasibility(book_item=book, bank_account=_account_view("subset", _bank("x", 70, account_id="subset"), _bank("y", 80, account_id="subset")))
    assert no_subset.status is RoutingFeasibilityStatus.NO_EXACT_SUBSET and no_subset.compatible_bank_item_ids == ("x", "y") and not no_subset.witness_bank_item_ids
    view = _view(accounts=(_account_view("b", _bank("b1", 100, account_id="b")), _account_view("a", _bank("a1", 100, account_id="a"))), books=(_book("book-b", 100), _book("book-a", 100)), revision=4)
    matrix = build_feasibility_matrix(view)
    assert matrix.state_revision == 4
    assert [(entry.book_item_id, entry.bank_account_id) for entry in matrix.entries] == [("book-a", "a"), ("book-a", "b"), ("book-b", "a"), ("book-b", "b")]
    assert len({(entry.book_item_id, entry.bank_account_id) for entry in matrix.entries}) == 4


def test_capacity_buckets_use_remaining_compatible_items_but_do_not_claim_exact_feasibility() -> None:
    account_view = _account_view("a", _bank("out-70", 70), _bank("out-80", 80), _bank("in-100", 100, direction=Direction.BANK_INFLOW))
    view = _view(accounts=(account_view,), books=(_book("credit", 100), _book("debit", 100, direction=Direction.BOOK_BANK_DEBIT)))
    buckets = {(bucket.bank_account_id, bucket.book_direction): bucket for bucket in build_capacity_buckets(view)}
    assert buckets[("a", Direction.BOOK_BANK_CREDIT)].available_int == 150
    assert buckets[("a", Direction.BOOK_BANK_CREDIT)].bank_item_ids == ("out-70", "out-80")
    assert buckets[("a", Direction.BOOK_BANK_DEBIT)].available_int == 100
    assert build_feasibility_matrix(view).get(book_item_id="credit", bank_account_id="a").status is RoutingFeasibilityStatus.NO_EXACT_SUBSET


def test_semantic_candidates_are_bounded_feasible_only_and_preserve_witnesses() -> None:
    account_view = _account_view("a", _bank("far", 10, day=1), _bank("w80", 80, day=3), _bank("w20", 20, day=4), _bank("near", 10, day=14))
    book = _book("book", 100, day=15)
    view = _view(accounts=(account_view, _account_view("none")), books=(book,))
    feasibility = build_feasibility_matrix(view)
    candidates = build_semantic_candidates(view=view, feasibility=feasibility, max_bank_evidence_items=3)
    assert len(candidates) == 1
    candidate = candidates[0]
    assert candidate.book_item_id == "book" and candidate.bank_account_name == "a" and candidate.institution_name == "Bank"
    assert [item.bank_item_id for item in candidate.bank_evidence] == ["w20", "w80", "near"]
    assert {item.bank_item_id for item in candidate.bank_evidence if item.is_feasibility_witness} == {"w80", "w20"}
    assert sum(item.remaining_amount_int for item in candidate.bank_evidence if item.is_feasibility_witness) == 100
    assert all(item.bank_item_id != "far" for item in candidate.bank_evidence)
    witness_only = build_semantic_candidates(view=view, feasibility=feasibility, max_bank_evidence_items=1)[0]
    assert {item.bank_item_id for item in witness_only.bank_evidence} == {"w80", "w20"}
    with pytest.raises(ValueError, match="positive"):
        build_semantic_candidates(view=view, feasibility=feasibility, max_bank_evidence_items=0)


def test_semantic_scorer_fails_closed_and_zero_provider_remains_valid() -> None:
    view = _view(accounts=(_account_view("a", _bank("b", 100)),), books=(_book("book", 100),), revision=2)
    feasibility = build_feasibility_matrix(view)
    seen = []

    class Provider:
        def score(self, request):
            seen.extend(request.candidates)
            return RoutingSemanticScoringResponse(state_revision=request.state_revision, model_run_id="fake", scores=(RoutingProviderScore(book_item_id="book", bank_account_id="a", score=777, rationale="yes"),))

    scores = score_feasible_pairs(view=view, feasibility=feasibility, provider=Provider())
    assert len(seen) == 1 and scores.state_revision == 2 and scores.scores[0].score == 777 and scores.scores[0].rationale == "yes" and scores.scores[0].model_run_id == "fake"
    assert score_feasible_pairs(view=view, feasibility=feasibility, provider=ZeroRoutingSemanticScoreProvider()).scores[0].score == 0

    class MustNotRun:
        def score(self, request):
            raise AssertionError("semantic scoring must not run without feasible pairs")
    empty_feasibility = build_feasibility_matrix(_view(accounts=(_account_view("empty"),), books=(_book("none", 100),)))
    assert score_feasible_pairs(view=_view(accounts=(_account_view("empty"),), books=(_book("none", 100),)), feasibility=empty_feasibility, provider=MustNotRun()).scores == ()

    class Missing:
        def score(self, request): return RoutingSemanticScoringResponse(state_revision=request.state_revision, model_run_id="bad", scores=())
    class Extra:
        def score(self, request): return RoutingSemanticScoringResponse(state_revision=request.state_revision, model_run_id="bad", scores=(RoutingProviderScore(book_item_id="other", bank_account_id="a", score=1),))
    class Stale:
        def score(self, request): return RoutingSemanticScoringResponse(state_revision=request.state_revision + 1, model_run_id="bad", scores=(RoutingProviderScore(book_item_id="book", bank_account_id="a", score=1),))
    for provider in (Missing(), Extra(), Stale()):
        with pytest.raises(RoutingSemanticScoringError):
            score_feasible_pairs(view=view, feasibility=feasibility, provider=provider)
    with pytest.raises(ValidationError, match="duplicate"):
        RoutingSemanticScoringResponse(state_revision=2, model_run_id="bad", scores=(RoutingProviderScore(book_item_id="book", bank_account_id="a", score=1), RoutingProviderScore(book_item_id="book", bank_account_id="a", score=2)))


def test_optimizer_enforces_global_bank_exclusivity_and_exact_witnesses() -> None:
    conflict = _view(accounts=(_account_view("a", _bank("bank-1", 100)),), books=(_book("book-x", 100), _book("book-y", 100)))
    result = _optimized(conflict)
    assert len(result.assignments) == 1 and len(result.unassigned) == 1
    assert result.unassigned[0].reason is RoutingUnassignedReason.GLOBAL_CAPACITY_CONFLICT
    used = [bank_id for assignment in result.assignments for bank_id in assignment.feasibility_witness_bank_item_ids]
    assert used == ["bank-1"]

    exact = _view(accounts=(_account_view("a", _bank("b1", 70), _bank("b2", 80), _bank("b3", 20)),), books=(_book("book", 100),))
    assignment = _optimized(exact).assignments[0]
    amounts = {"b1": 70, "b2": 80, "b3": 20}
    assert sum(amounts[item_id] for item_id in assignment.feasibility_witness_bank_item_ids) == assignment.amount_int == 100


def test_optimizer_prioritizes_money_then_semantics_and_breaks_ties_deterministically() -> None:
    money = _view(accounts=(_account_view("a", _bank("bank", 100)),), books=(_book("large", 100), _book("small", 60)))
    result = _optimized(money, {("large", "a"): 1, ("small", "a"): 1000})
    assert result.assignment_for("large") is not None and result.assignment_for("small") is None

    semantic = _view(accounts=(_account_view("a", _bank("a-bank", 100)), _account_view("b", _bank("b-bank", 100, account_id="b"))), books=(_book("book", 100),))
    assert _optimized(semantic, {("book", "a"): 5, ("book", "b"): 9}).assignments[0].bank_account_id == "b"

    tied = _view(accounts=(_account_view("b", _bank("b-bank", 100, account_id="b")), _account_view("a", _bank("a-bank", 100))), books=(_book("book", 100),))
    outputs = {_optimized(tied).model_dump_json() for _ in range(4)}
    assert len(outputs) == 1
    assert _optimized(tied).assignments[0].bank_account_id == "a"


def test_routing_result_model_rejects_invalid_assignment_worlds() -> None:
    assignment = RoutingAssignment(book_item_id="book", bank_account_id="a", amount_units="100", semantic_score=4, feasibility_witness_bank_item_ids=("bank",))
    unassigned = UnassignedRoutingItem(book_item_id="book", amount_units="100", reason=RoutingUnassignedReason.NO_FEASIBLE_ACCOUNT)
    valid = dict(state_revision=0, solver_run_id="solver", objective_routed_units="100", objective_semantic_utility=4)
    with pytest.raises(ValidationError, match="more than once"):
        RoutingResult(**valid, assignments=(assignment, assignment))
    with pytest.raises(ValidationError, match="both assigned"):
        RoutingResult(**valid, assignments=(assignment,), unassigned=(unassigned,))
    with pytest.raises(ValidationError, match="objective_routed"):
        RoutingResult(**{**valid, "objective_routed_units": "99"}, assignments=(assignment,))
    with pytest.raises(ValidationError, match="duplicates"):
        RoutingAssignment(book_item_id="book", bank_account_id="a", amount_units="100", semantic_score=4, feasibility_witness_bank_item_ids=("bank", "bank"))
    with pytest.raises(ValidationError, match="requires deterministic"):
        RoutingAssignment(book_item_id="book", bank_account_id="a", amount_units="100", semantic_score=4)
    with pytest.raises(ValidationError, match="duplicate unassigned"):
        RoutingResult(state_revision=0, solver_run_id="solver", unassigned=(unassigned, unassigned))


def _service_world(*, books: tuple = ("book-1",), banks: tuple = ("bank-1",)):
    repository = InMemoryBookkeepingRepository(initial_snapshots=[snapshot(bank_accounts=(account(),), bank_items=tuple(bank_item(bank_id, amount=100) for bank_id in banks), book_items=tuple(book_item(book_id, amount=100) for book_id in books))])
    live = BookkeepingHydrator(repository=repository, clock=lambda: FIXED_TIME).hydrate(company_id="atlas", session_id="routing-service")
    return live, repository


def test_routing_service_is_read_only_and_plan_commands_cross_transition_boundary() -> None:
    live, repository = _service_world()
    queries = BookkeepingQueries(live)
    service = RoutingService(semantic_provider=StaticRoutingSemanticScoreProvider(scores={("book-1", "account-1"): 321}), max_bank_evidence_items=2)
    before = (artifact_fingerprint(live), state_fingerprint(live), live.revision, live.persistence_revision, len(live.events), dict(live.reconciliation_hypotheses), repository.current_revision(company_id="atlas"))
    plan = service.run(queries=queries, solver_run_id="run-1", issued_at=FIXED_TIME)
    assert before == (artifact_fingerprint(live), state_fingerprint(live), live.revision, live.persistence_revision, len(live.events), dict(live.reconciliation_hypotheses), repository.current_revision(company_id="atlas"))
    assert plan.view.state_revision == plan.feasibility.state_revision == plan.semantic_scores.state_revision == plan.result.state_revision == 0
    assert len(plan.commands) == len(plan.result.assignments) == 1
    command = plan.commands[0]
    assignment = plan.result.assignments[0]
    assert (command.book_item_id, command.bank_account_id, command.expected_state_revision, command.solver_run_id, command.utility, command.supersedes_routing_decision_id) == (assignment.book_item_id, assignment.bank_account_id, 0, "run-1", assignment.semantic_score, None)
    assert command.command_id == "routing-command:run-1:book-1" and command.routing_decision_id == "routing-decision:run-1:book-1"
    assert command.source is CommandSource.ROUTING and command.decision_source is RoutingDecisionSource.CP_SAT
    applied = TransitionEngine(repository=repository).apply(state=live, command=command)
    assert applied.status is TransitionStatus.APPLIED and BookkeepingQueries(live).active_route("book-1").id == command.routing_decision_id


def test_stale_routing_plan_is_rejected_and_accepted_route_rehydrates() -> None:
    live, repository = _service_world()
    hydrator = BookkeepingHydrator(repository=repository, clock=lambda: FIXED_TIME)
    plan = RoutingService(semantic_provider=ZeroRoutingSemanticScoreProvider()).run(queries=BookkeepingQueries(live), solver_run_id="stale", issued_at=FIXED_TIME)
    intervening = TransitionEngine(repository=repository).apply(state=live, command=CreateClassificationCommand(command_id="intervening", expected_state_revision=0, source=CommandSource.ASE_DAG, session_id=live.session_id, issued_at=FIXED_TIME, classification_id="class", book_item_id="book-1", account_code="6111", classification_source=ClassificationSource.ASE_DAG))
    assert intervening.status is TransitionStatus.APPLIED
    stale = TransitionEngine(repository=repository).apply(state=live, command=plan.commands[0])
    assert stale.status is TransitionStatus.REJECTED and stale.rejection.code is RejectionCode.STATE_REVISION_CONFLICT
    assert live.get_routing_decision("routing-decision:stale:book-1") is None

    refreshed = RoutingService(semantic_provider=ZeroRoutingSemanticScoreProvider()).run(queries=BookkeepingQueries(live), solver_run_id="fresh", issued_at=FIXED_TIME)
    accepted = TransitionEngine(repository=repository).apply(state=live, command=refreshed.commands[0])
    assert accepted.status is TransitionStatus.APPLIED
    fingerprint = state_fingerprint(live)
    live.close()
    restored = hydrator.hydrate(company_id="atlas", session_id="rehydrated")
    assert restored.revision == 0 and restored.persistence_revision == 2
    assert BookkeepingQueries(restored).active_route("book-1").id == "routing-decision:fresh:book-1"
    assert state_fingerprint(restored) == fingerprint


def test_multi_assignment_plan_applies_atomically_via_transition_batch() -> None:
    live, repository = _service_world(books=("book-1", "book-2"), banks=("bank-1", "bank-2"))
    plan = RoutingService(semantic_provider=ZeroRoutingSemanticScoreProvider()).run(queries=BookkeepingQueries(live), solver_run_id="batch", issued_at=FIXED_TIME)
    assert len(plan.commands) == 2 and {command.expected_state_revision for command in plan.commands} == {0}
    engine = TransitionEngine(repository=repository)
    batch = plan.to_batch()
    result = engine.apply_batch(state=live, batch=batch)
    assert result.status is TransitionStatus.APPLIED
    assert result.is_applied
    assert live.revision == 1
    assert live.persistence_revision == 1
    assert len(live.routing_decisions) == 2
    queries = BookkeepingQueries(live)
    assert queries.active_route("book-1") is not None
    assert queries.active_route("book-2") is not None
    hydrator = BookkeepingHydrator(repository=repository, clock=lambda: FIXED_TIME)
    fingerprint = state_fingerprint(live)
    live.close()
    restored = hydrator.hydrate(company_id="atlas", session_id="rehydrated")
    assert restored.persistence_revision == 1
    assert len(restored.routing_decisions) == 2
    assert state_fingerprint(restored) == fingerprint


def test_accepted_route_is_hidden_until_explicit_invalidation_reopens_it() -> None:
    live, repository = _service_world()
    engine = TransitionEngine(repository=repository)
    plan = RoutingService(semantic_provider=ZeroRoutingSemanticScoreProvider()).run(queries=BookkeepingQueries(live), solver_run_id="reopen", issued_at=FIXED_TIME)
    command = plan.commands[0]
    assert engine.apply(state=live, command=command).status is TransitionStatus.APPLIED
    assert build_routing_view(BookkeepingQueries(live)).book_items == ()
    invalidation = engine.apply(
        state=live,
        command=InvalidateRoutingDecisionCommand(command_id="invalidate-route", expected_state_revision=1, source=CommandSource.HUMAN, session_id=live.session_id, issued_at=FIXED_TIME, invalidation_id="invalidate-route", routing_decision_id=command.routing_decision_id, reason="reconsider"),
    )
    assert invalidation.status is TransitionStatus.APPLIED
    assert tuple(item.book_item_id for item in build_routing_view(BookkeepingQueries(live)).book_items) == ("book-1",)
