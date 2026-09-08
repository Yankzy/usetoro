from __future__ import annotations

import ast
from dataclasses import FrozenInstanceError, is_dataclass, replace
from pathlib import Path
from unittest.mock import patch

import pytest

from bookkeeping_state_eval.domain.commands import CommandBase
from bookkeeping_state_eval.domain.hypotheses import ReconciliationHypothesis
from bookkeeping_state_eval.persistence.in_memory import InMemoryBookkeepingRepository
from bookkeeping_state_eval.persistence.repository import BookkeepingRepository
from bookkeeping_state_eval.scenarios.catalog import (
    SCENARIO_CATALOG,
    get_scenario,
)
from bookkeeping_state_eval.scenarios.expected_truth import (
    ExpectedReconciliation,
    ExpectedTruth,
)
from bookkeeping_state_eval.scenarios.models import (
    ScenarioDefinition,
    ScenarioResult,
)
from bookkeeping_state_eval.scenarios.runner import DEFAULT_CLOCK, ScenarioRunner
from bookkeeping_state_eval.session.result import SessionStageStatus
from bookkeeping_state_eval.state.bookkeeping_state import BookkeepingState
from bookkeeping_state_eval.state.validation import (
    StateValidationReport,
    ValidationCode,
    ValidationIssue,
    ValidationSeverity,
)
from bookkeeping_state_eval.transitions.batch import TransitionBatch


# ======================================================================
# 1. Catalog-Wide Execution Tests
# ======================================================================

@pytest.mark.parametrize(
    "scenario",
    SCENARIO_CATALOG,
    ids=lambda s: s.scenario_id,
)
def test_every_catalog_scenario_passes(scenario: ScenarioDefinition) -> None:
    """
    Every scenario in the catalog must execute and converge to its independently
    defined expected truth.
    """
    runner = ScenarioRunner()
    result = runner.run(scenario)

    assert result.is_pass, (
        f"Scenario {scenario.scenario_id} failed!\n"
        f"Reason: {result.failure_reason}\n"
        f"Mismatches:\n" + "\n".join(result.mismatches)
    )
    assert result.scenario_id == scenario.scenario_id
    assert result.expected_truth_verdict.is_match
    assert len(result.mismatches) == 0
    assert result.final_durable_fingerprint is not None
    assert result.final_persistence_revision >= 1


def test_catalog_loop_runner() -> None:
    """
    Explicit loop verifying all catalog scenarios pass sequentially.
    """
    runner = ScenarioRunner()
    assert len(SCENARIO_CATALOG) >= 16, f"Expected at least 16 scenarios, got {len(SCENARIO_CATALOG)}"
    for scenario in SCENARIO_CATALOG:
        result = runner.run(scenario)
        assert result.is_pass, (
            f"Scenario {scenario.scenario_id} failed: {result.failure_reason}"
        )


# ======================================================================
# 2. Rehydration and Lifecycle Tests
# ======================================================================

def test_runner_always_rehydrates_before_final_evaluation() -> None:
    """
    Ensure the runner evaluates against rehydrated state, and that the original
    live state S0 was closed/destroyed before truth validation.
    """
    scenario = get_scenario("scenario_a_exact_receipt")
    runner = ScenarioRunner()

    hydrated_states: list[BookkeepingState] = []
    from bookkeeping_state_eval.hydration.hydrator import BookkeepingHydrator

    orig_hydrate = BookkeepingHydrator.hydrate

    def spy_hydrate(self, *args, **kwargs):
        st = orig_hydrate(self, *args, **kwargs)
        hydrated_states.append(st)
        return st

    with patch.object(BookkeepingHydrator, "hydrate", side_effect=spy_hydrate, autospec=True):
        result = runner.run(scenario)

    assert result.is_pass
    # Exactly two states hydrated: S0 (for session) and S_rehydrated (for validation)
    assert len(hydrated_states) == 2
    s0, s_rehydrated = hydrated_states

    # S0 must have been destroyed by session run
    assert s0.is_closed, "S0 was not closed after BookkeepingSession run"
    # S_rehydrated was also closed after validation
    assert s_rehydrated.is_closed, "Rehydrated state was not closed after evaluation"


# ======================================================================
# 3. Detached & Immutable ScenarioResult Tests
# ======================================================================

def test_scenario_result_is_detached_and_immutable() -> None:
    """
    ScenarioResult must be frozen and must NOT leak live BookkeepingState,
    commands, hypotheses, TransitionBatch, or repository instances.
    """
    scenario = get_scenario("scenario_a_exact_receipt")
    runner = ScenarioRunner()
    result = runner.run(scenario)

    assert result.is_pass

    # Immutability
    with pytest.raises((FrozenInstanceError, AttributeError)):
        result.is_pass = False  # type: ignore[misc]

    # Verify no disallowed types anywhere in result structure
    forbidden_types = (
        BookkeepingState,
        CommandBase,
        ReconciliationHypothesis,
        TransitionBatch,
        BookkeepingRepository,
    )

    def scan_for_forbidden(obj, visited=None):
        if visited is None:
            visited = set()
        obj_id = id(obj)
        if obj_id in visited:
            return
        visited.add(obj_id)

        assert not isinstance(obj, forbidden_types), f"Found forbidden object {type(obj)} in ScenarioResult"

        if is_dataclass(obj):
            for field_name in obj.__dataclass_fields__:
                val = getattr(obj, field_name)
                scan_for_forbidden(val, visited)
        elif isinstance(obj, (list, tuple, set, frozenset)):
            for item in obj:
                scan_for_forbidden(item, visited)
        elif isinstance(obj, dict):
            for k, v in obj.items():
                scan_for_forbidden(k, visited)
                scan_for_forbidden(v, visited)

    scan_for_forbidden(result)


# ======================================================================
# 4. Independent Truth Failure Sensitivity Tests
# ======================================================================

def test_wrong_expected_truth_fails_even_when_session_succeeds() -> None:
    """
    A self-consistent and valid bookkeeping run must FAIL the scenario if its
    outcome does not match the independently declared expected truth.
    """
    scenario = get_scenario("scenario_a_exact_receipt")

    # Deliberately declare impossible expected remaining units (e.g. 50k instead of 0)
    corrupted_expected = ExpectedTruth(
        expected_routes={"book-1": "acc-main"},
        expected_classifications={"book-1": "3421"},
        expected_reconciliations=[
            ExpectedReconciliation.one_to_one("bank-1", "book-1", 100_000)
        ],
        expected_bank_remaining={"bank-1": 50_000},  # Wrong! Actual is 0
        expected_book_remaining={"book-1": 50_000},  # Wrong! Actual is 0
    )

    bad_scenario = ScenarioDefinition(
        scenario_id="scenario_a_corrupted_truth",
        name="Corrupted Truth Scenario",
        description="Expectation differs from actual reality.",
        context=scenario.context,
        bank_accounts=scenario.bank_accounts,
        bank_items=scenario.bank_items,
        book_items=scenario.book_items,
        expected_truth=corrupted_expected,
    )

    runner = ScenarioRunner()
    result = runner.run(bad_scenario)

    # Session itself succeeded, but scenario MUST fail
    assert result.session_result.is_success is True
    assert result.is_pass is False
    assert result.expected_truth_verdict.is_match is False
    assert len(result.mismatches) > 0
    assert any("expected remaining amount" in m for m in result.mismatches)


def test_wrong_expected_route_fails_scenario() -> None:
    """
    Wrong expected route fails the scenario.
    """
    scenario = get_scenario("scenario_a_exact_receipt")
    wrong_route_expected = ExpectedTruth(
        expected_routes={"book-1": "acc-wrong"},  # Wrong route!
        expected_classifications={"book-1": "3421"},
        expected_reconciliations=[
            ExpectedReconciliation.one_to_one("bank-1", "book-1", 100_000)
        ],
        expected_bank_remaining={"bank-1": 0},
        expected_book_remaining={"book-1": 0},
    )

    bad_scenario = ScenarioDefinition(
        scenario_id="scenario_a_wrong_route",
        name="Wrong Route Scenario",
        description="Declared route does not match.",
        context=scenario.context,
        bank_accounts=scenario.bank_accounts,
        bank_items=scenario.bank_items,
        book_items=scenario.book_items,
        expected_truth=wrong_route_expected,
    )

    runner = ScenarioRunner()
    result = runner.run(bad_scenario)

    assert result.session_result.is_success is True
    assert result.is_pass is False
    assert any("expected route" in m for m in result.mismatches)


def test_corrupted_closing_state_fails_scenario() -> None:
    """
    If the durable closing world fails validate_state, the scenario must fail
    even if the session run itself was technically successful.
    """
    scenario = get_scenario("scenario_a_exact_receipt")
    runner = ScenarioRunner()

    corrupted_report = StateValidationReport(
        state_revision=1,
        issues=(
            ValidationIssue(
                code=ValidationCode.RECONCILIATION_MONETARY_IMBALANCE,
                severity=ValidationSeverity.ERROR,
                message="Monetary conservation violated in closing state",
            ),
        ),
    )

    with patch(
        "bookkeeping_state_eval.scenarios.runner.validate_state",
        return_value=corrupted_report,
    ):
        result = runner.run(scenario)

    assert result.is_pass is False
    assert result.failure_reason is not None
    assert "Hydrated closing state is invalid" in result.failure_reason
    assert "RECONCILIATION_MONETARY_IMBALANCE" in result.failure_reason


# ======================================================================
# 5. Deterministic Repeatability & Idempotency Tests
# ======================================================================

def test_deterministic_repeatability() -> None:
    """
    Repeated runs from equivalent initial snapshots must produce the exact same
    closing truth.
    """
    scenario = get_scenario("scenario_a_exact_receipt")
    runner = ScenarioRunner()

    res1 = runner.run(scenario)
    res2 = runner.run(scenario)

    assert res1.is_pass
    assert res2.is_pass
    assert res1.final_persistence_revision == res2.final_persistence_revision
    assert res1.expected_truth_verdict == res2.expected_truth_verdict
    assert res1.mismatches == res2.mismatches == ()
    assert res1.final_durable_fingerprint is not None
    assert res2.final_durable_fingerprint is not None

    # If session IDs legitimately differ, canonical accounting truth matches
    res3 = runner.run(scenario, session_id="custom-run-3")
    assert res3.is_pass
    assert res3.expected_truth_verdict == res1.expected_truth_verdict
    assert res3.mismatches == ()


def test_second_session_idempotency() -> None:
    """
    Scenario P: Running a second BookkeepingSession on top of the durable state
    left by the first must produce zero new mutations and skip all stages.
    """
    scenario = get_scenario("scenario_p_second_session_idempotency")
    runner = ScenarioRunner()

    res1, res2 = runner.run_idempotent_session(scenario)

    assert res1.is_pass, f"First session failed: {res1.failure_reason}"
    assert res2.is_pass, f"Second session failed: {res2.failure_reason}"

    # Revisions and fingerprints must be identical
    assert res2.final_persistence_revision == res1.final_persistence_revision
    assert res2.final_durable_fingerprint == res1.final_durable_fingerprint

    # Second session must have skipped all stages because world is already converged
    assert res2.session_result.routing_stage_result is not None
    assert res2.session_result.routing_stage_result.status == SessionStageStatus.SKIPPED
    assert res2.session_result.dag_stage_result is not None
    assert res2.session_result.dag_stage_result.status == SessionStageStatus.SKIPPED
    assert res2.session_result.reconciliation_stage_result is not None
    assert res2.session_result.reconciliation_stage_result.status == SessionStageStatus.SKIPPED


# ======================================================================
# 6. Independence: No Imports from reconciliation_eval
# ======================================================================

def test_no_imports_from_reconciliation_eval() -> None:
    """
    Strict isolation rule: No file in bookkeeping_state_eval may import from
    the old historical reconciliation_eval package.
    """
    eval_root = Path(__file__).resolve().parent.parent.parent
    pkg_dir = eval_root if (eval_root / "domain").exists() else eval_root / "bookkeeping_state_eval"
    assert pkg_dir.exists(), f"Package dir not found: {pkg_dir}"

    for py_file in pkg_dir.rglob("*.py"):
        tree = ast.parse(py_file.read_text(encoding="utf-8"), filename=str(py_file))
        for node in ast.walk(tree):
            if isinstance(node, ast.Import):
                for alias in node.names:
                    assert "reconciliation_eval" not in alias.name, (
                        f"Forbidden import from reconciliation_eval found in {py_file}:{node.lineno}"
                    )
            elif isinstance(node, ast.ImportFrom):
                if node.module:
                    assert "reconciliation_eval" not in node.module, (
                        f"Forbidden import from reconciliation_eval found in {py_file}:{node.lineno}"
                    )


# ======================================================================
# 7. Oracle Independence & Exact-Set Validation Tests
# ======================================================================

def test_unspecified_routes_do_not_fail_when_runtime_has_routes() -> None:
    """
    When expected_routes is None (unspecified), the scenario must not fail
    merely because the runtime created active routes.
    """
    scenario_a = get_scenario("scenario_a_exact_receipt")
    # Omit expected_routes (defaults to None)
    unspecified_routes_truth = ExpectedTruth(
        expected_routes=None,
        expected_classifications=scenario_a.expected_truth.expected_classifications,
        expected_reconciliations=scenario_a.expected_truth.expected_reconciliations,
        expected_bank_remaining=scenario_a.expected_truth.expected_bank_remaining,
        expected_book_remaining=scenario_a.expected_truth.expected_book_remaining,
    )
    test_scenario = ScenarioDefinition(
        scenario_id="scenario_unspecified_routes",
        name="Unspecified Routes",
        description="Scenario does not assert routes.",
        context=scenario_a.context,
        bank_accounts=scenario_a.bank_accounts,
        bank_items=scenario_a.bank_items,
        book_items=scenario_a.book_items,
        expected_truth=unspecified_routes_truth,
    )
    res = ScenarioRunner().run(test_scenario)
    assert res.is_pass is True


def test_expected_routes_empty_fails_if_active_route_exists() -> None:
    """
    When expected_routes is {} (asserting exactly zero active routes),
    the presence of any active route in runtime must cause validation to fail.
    """
    scenario_a = get_scenario("scenario_a_exact_receipt")
    zero_routes_truth = ExpectedTruth(
        expected_routes={},  # Expect ZERO routes!
        expected_classifications=scenario_a.expected_truth.expected_classifications,
        expected_reconciliations=scenario_a.expected_truth.expected_reconciliations,
        expected_bank_remaining=scenario_a.expected_truth.expected_bank_remaining,
        expected_book_remaining=scenario_a.expected_truth.expected_book_remaining,
    )
    test_scenario = ScenarioDefinition(
        scenario_id="scenario_expect_zero_routes",
        name="Expect Zero Routes",
        description="Asserts no routes, but runtime routes book-1.",
        context=scenario_a.context,
        bank_accounts=scenario_a.bank_accounts,
        bank_items=scenario_a.bank_items,
        book_items=scenario_a.book_items,
        expected_truth=zero_routes_truth,
    )
    res = ScenarioRunner().run(test_scenario)
    assert res.is_pass is False
    assert any("unexpected active route for BookItem 'book-1'" in m for m in res.mismatches)


def test_expected_routes_fails_on_unexpected_extra_route() -> None:
    """
    When expected_routes declares a route for book-1, but book-2 also receives
    an active route, validation must fail due to the unexpected extra route.
    """
    scenario_g = get_scenario("scenario_g_duplicate_amounts_tie_breaking")
    # In Scenario G, book-1 and book-2 are both routed to acc-main by runtime.
    # Declare route only for book-1 to verify that undeclared book-2 triggers a failure.
    incomplete_routes_truth = ExpectedTruth(
        expected_routes={"book-1": "acc-main"},
        expected_reconciliations=scenario_g.expected_truth.expected_reconciliations,
        expected_bank_remaining=scenario_g.expected_truth.expected_bank_remaining,
        expected_book_remaining=scenario_g.expected_truth.expected_book_remaining,
    )
    test_scenario = ScenarioDefinition(
        scenario_id="scenario_extra_route",
        name="Extra Route",
        description="Asserts route only for book-1.",
        context=scenario_g.context,
        bank_accounts=scenario_g.bank_accounts,
        bank_items=scenario_g.bank_items,
        book_items=scenario_g.book_items,
        expected_truth=incomplete_routes_truth,
    )
    res = ScenarioRunner().run(test_scenario)
    assert res.is_pass is False
    assert any("unexpected active route for BookItem 'book-2'" in m for m in res.mismatches)


def test_unspecified_classifications_do_not_fail_when_runtime_classifies() -> None:
    """
    When expected_classifications is None (unspecified), validation must not
    fail merely because the runtime DAG classified items.
    """
    scenario_a = get_scenario("scenario_a_exact_receipt")
    unspecified_cls_truth = ExpectedTruth(
        expected_routes=scenario_a.expected_truth.expected_routes,
        expected_classifications=None,  # Unspecified
        expected_reconciliations=scenario_a.expected_truth.expected_reconciliations,
        expected_bank_remaining=scenario_a.expected_truth.expected_bank_remaining,
        expected_book_remaining=scenario_a.expected_truth.expected_book_remaining,
    )
    test_scenario = ScenarioDefinition(
        scenario_id="scenario_unspecified_classifications",
        name="Unspecified Classifications",
        description="Scenario does not assert classifications.",
        context=scenario_a.context,
        bank_accounts=scenario_a.bank_accounts,
        bank_items=scenario_a.bank_items,
        book_items=scenario_a.book_items,
        expected_truth=unspecified_cls_truth,
    )
    res = ScenarioRunner().run(test_scenario)
    assert res.is_pass is True


def test_expected_classifications_empty_fails_if_active_classification_exists() -> None:
    """
    When expected_classifications is {} (asserting zero classifications),
    any active classification in runtime must cause validation to fail.
    """
    scenario_a = get_scenario("scenario_a_exact_receipt")
    zero_cls_truth = ExpectedTruth(
        expected_routes=scenario_a.expected_truth.expected_routes,
        expected_classifications={},  # Expect ZERO classifications!
        expected_reconciliations=scenario_a.expected_truth.expected_reconciliations,
        expected_bank_remaining=scenario_a.expected_truth.expected_bank_remaining,
        expected_book_remaining=scenario_a.expected_truth.expected_book_remaining,
    )
    test_scenario = ScenarioDefinition(
        scenario_id="scenario_expect_zero_classifications",
        name="Expect Zero Classifications",
        description="Asserts no classifications, but runtime classifies book-1.",
        context=scenario_a.context,
        bank_accounts=scenario_a.bank_accounts,
        bank_items=scenario_a.bank_items,
        book_items=scenario_a.book_items,
        expected_truth=zero_cls_truth,
    )
    res = ScenarioRunner().run(test_scenario)
    assert res.is_pass is False
    assert any("unexpected active classification for BookItem 'book-1'" in m for m in res.mismatches)


def test_expected_classifications_fails_on_unexpected_extra_classification() -> None:
    """
    When expected_classifications declares a code for book-1, but book-2 also receives
    an active classification, validation must fail due to unexpected extra classification.
    """
    scenario_b = get_scenario("scenario_b_one_to_many")
    # Declare classification only for book-1; book-2 and book-3 are also classified by DAG
    incomplete_cls_truth = ExpectedTruth(
        expected_classifications={"book-1": "3421"},
        expected_reconciliations=scenario_b.expected_truth.expected_reconciliations,
        expected_bank_remaining=scenario_b.expected_truth.expected_bank_remaining,
        expected_book_remaining=scenario_b.expected_truth.expected_book_remaining,
    )
    test_scenario = ScenarioDefinition(
        scenario_id="scenario_extra_classification",
        name="Extra Classification",
        description="Asserts classification only for book-1.",
        context=scenario_b.context,
        bank_accounts=scenario_b.bank_accounts,
        bank_items=scenario_b.bank_items,
        book_items=scenario_b.book_items,
        expected_truth=incomplete_cls_truth,
    )
    res = ScenarioRunner().run(test_scenario)
    assert res.is_pass is False
    assert any("unexpected active classification for BookItem 'book-2'" in m for m in res.mismatches)
    assert any("unexpected active classification for BookItem 'book-3'" in m for m in res.mismatches)


def test_scenario_e_does_not_assert_classifier_fallback() -> None:
    """
    Scenario E must have expected_classifications=None, testing only genuine independent truth.
    """
    scenario_e = get_scenario("scenario_e_partial_bank_forbidden")
    assert scenario_e.expected_truth.expected_classifications is None
    assert scenario_e.expected_truth.expected_routes is None
    assert scenario_e.expected_truth.expected_reconciliations == ()

    res = ScenarioRunner().run(scenario_e)
    assert res.is_pass is True
    assert res.expected_truth_verdict.is_match is True


def test_scenario_c_uses_normal_partial_book_policy() -> None:
    """
    Scenario C must use the default policy (allow_partial_book_reconciliation=True).
    """
    scenario_c = get_scenario("scenario_c_many_to_one")
    assert scenario_c.context.policy.allow_partial_book_reconciliation is True


def test_scenario_c_passes_with_grouped_or_partial_reconciliation() -> None:
    """
    Scenario C validates semantic pairwise allocation truth regardless of whether
    the runtime produces one N:1 grouped reconciliation or three partial 1:1 reconciliations.
    """
    scenario_c = get_scenario("scenario_c_many_to_one")
    runner = ScenarioRunner()

    # 1. Standard execution: CP-SAT under default policy produces 3 partial 1:1 reconciliations
    res_runtime = runner.run(scenario_c)
    assert res_runtime.is_pass is True

    # 2. Rehydrate a state containing one N:1 grouped reconciliation instead
    from bookkeeping_state_eval.hydration.hydrator import BookkeepingHydrator
    from bookkeeping_state_eval.scenarios.catalog import (
        _classification,
        _reconciliation,
        _routing,
    )
    from bookkeeping_state_eval.scenarios.expected_truth import validate_expected_truth
    from bookkeeping_state_eval.state.queries import BookkeepingQueries

    rec_n1 = _reconciliation(
        "rec-n1-grouped",
        bank=(("bank-1", 20_000), ("bank-2", 30_000), ("bank-3", 50_000)),
        book=(("book-1", 100_000),),
    )
    r1 = _routing("route-1", book_item_id="book-1", bank_account_id="acc-main")
    c1 = _classification("cls-1", book_item_id="book-1", account_code="3421")

    snap = replace(
        scenario_c.to_initial_snapshot(),
        reconciliations=(rec_n1,),
        routing_decisions=(r1,),
        classifications=(c1,),
    )
    n1_repo = InMemoryBookkeepingRepository(initial_snapshots=[snap])
    hydrator = BookkeepingHydrator(
        repository=n1_repo,
        clock=lambda: scenario_c.clock_time or DEFAULT_CLOCK,
        session_id_factory=lambda: "eval-n1",
    )
    n1_state = hydrator.hydrate(company_id=scenario_c.context.company_id, session_id="eval-n1")
    try:
        queries = BookkeepingQueries(n1_state)
        verdict = validate_expected_truth(queries, scenario_c.expected_truth)
        assert verdict.is_match is True
        assert len(verdict.mismatches) == 0
    finally:
        n1_state.close()


def test_pairwise_allocation_validator_catches_all_discrepancies() -> None:
    """
    Test that ExpectedTruth.expected_pairwise_allocations fails on:
    - missing pair
    - unexpected extra pair
    - wrong allocation amount
    """
    from bookkeeping_state_eval.hydration.hydrator import BookkeepingHydrator
    from bookkeeping_state_eval.scenarios.catalog import _reconciliation
    from bookkeeping_state_eval.scenarios.expected_truth import validate_expected_truth
    from bookkeeping_state_eval.state.queries import BookkeepingQueries

    scenario_a = get_scenario("scenario_a_exact_receipt")
    rec = _reconciliation(
        "rec-1",
        bank=(("bank-1", 100_000),),
        book=(("book-1", 100_000),),
    )
    snap = replace(
        scenario_a.to_initial_snapshot(),
        reconciliations=(rec,),
    )
    repo = InMemoryBookkeepingRepository(initial_snapshots=[snap])
    hydrator = BookkeepingHydrator(
        repository=repo,
        clock=lambda: DEFAULT_CLOCK,
        session_id_factory=lambda: "eval-test",
    )
    state = hydrator.hydrate(company_id=scenario_a.context.company_id, session_id="eval-test")

    try:
        queries = BookkeepingQueries(state)

        # 1. Correct pairwise match
        good_truth = ExpectedTruth(
            expected_pairwise_allocations={("bank-1", "book-1"): 100_000}
        )
        assert validate_expected_truth(queries, good_truth).is_match is True

        # 2. Missing pair
        missing_pair_truth = ExpectedTruth(
            expected_pairwise_allocations={
                ("bank-1", "book-1"): 100_000,
                ("bank-2", "book-1"): 50_000,
            }
        )
        verdict = validate_expected_truth(queries, missing_pair_truth)
        assert verdict.is_match is False
        assert any(
            "missing expected pairwise allocation: Bank 'bank-2' -> Book 'book-1'" in m
            for m in verdict.mismatches
        )

        # 3. Unexpected extra pair
        unexpected_pair_truth = ExpectedTruth(
            expected_pairwise_allocations={}  # Expects no pairs
        )
        verdict = validate_expected_truth(queries, unexpected_pair_truth)
        assert verdict.is_match is False
        assert any(
            "unexpected extra pairwise allocation: Bank 'bank-1' -> Book 'book-1'" in m
            for m in verdict.mismatches
        )

        # 4. Wrong amount
        wrong_amt_truth = ExpectedTruth(
            expected_pairwise_allocations={("bank-1", "book-1"): 60_000}
        )
        verdict = validate_expected_truth(queries, wrong_amt_truth)
        assert verdict.is_match is False
        assert any(
            "pairwise allocation amount mismatch for ('bank-1', 'book-1')" in m
            for m in verdict.mismatches
        )

    finally:
        state.close()

