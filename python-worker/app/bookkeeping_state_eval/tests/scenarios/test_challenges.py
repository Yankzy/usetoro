"""
Structural tests for BookkeepingState Challenge Worlds.

These tests assert structural integrity and interface compliance only.
They deliberately DO NOT assert that all adversarial challenges pass ExpectedTruth.
"""

from __future__ import annotations

import io
from contextlib import redirect_stdout

from bookkeeping_state_eval.hydration.hydrator import BookkeepingHydrator
from lab import BookkeepingLab
from bookkeeping_state_eval.scenarios.challenges import (
    CHALLENGES_MAP,
    get_challenge,
    list_challenges,
)
from bookkeeping_state_eval.scenarios.temporal import (
    TEMPORAL_CHALLENGES_MAP,
    TemporalChallengeRunner,
    get_temporal_challenge,
    list_temporal_challenges,
)
from bookkeeping_state_eval.scenarios.expected_truth import validate_expected_truth
from bookkeeping_state_eval.scenarios.runner import seed_scenario_repository
from bookkeeping_state_eval.state.queries import BookkeepingQueries


def test_challenge_definitions_load_and_have_unique_ids() -> None:
    challenges = list_challenges()
    assert len(challenges) == 7

    seen_ids: set[str] = set()
    for c in challenges:
        assert c.scenario_id not in seen_ids, f"Duplicate challenge ID: {c.scenario_id}"
        seen_ids.add(c.scenario_id)
        assert 1 <= c.difficulty <= 7
        assert len(c.tags) > 0
        assert c.name
        assert c.description
        assert c.scenario.context.company_id

    # Verify retrieval by name
    for sc_id in seen_ids:
        c = get_challenge(sc_id)
        assert c.scenario_id == sc_id


def test_challenge_artifact_identifiers_unique_within_scenarios() -> None:
    for c in list_challenges():
        sc = c.scenario
        bank_ids = [b.id for b in sc.bank_items]
        book_ids = [j.id for j in sc.book_items]
        acc_ids = [a.id for a in sc.bank_accounts]

        assert len(bank_ids) == len(set(bank_ids)), f"Duplicate BankItem ID in {c.scenario_id}"
        assert len(book_ids) == len(set(book_ids)), f"Duplicate BookItem ID in {c.scenario_id}"
        assert len(acc_ids) == len(set(acc_ids)), f"Duplicate BankAccount ID in {c.scenario_id}"


def test_challenge_initial_durable_seeding_and_hydration() -> None:
    for c in list_challenges():
        sc = c.scenario
        repo = seed_scenario_repository(sc)
        hydrator = BookkeepingHydrator(repository=repo)
        state = hydrator.hydrate(company_id=sc.context.company_id)

        try:
            assert not state.is_closed
            assert state.revision == 0
            assert state.persistence_revision == 1

            # Verify that validate_expected_truth evaluates without error
            queries = BookkeepingQueries(state)
            verdict = validate_expected_truth(queries, sc.expected_truth)
            assert isinstance(verdict.is_match, bool)
        finally:
            state.close()


def test_lab_challenge_switching_and_verification_command() -> None:
    lab = BookkeepingLab()
    try:
        buf_list = io.StringIO()
        with redirect_stdout(buf_list):
            lab.onecmd("challenges")
        out_list = buf_list.getvalue()
        assert "challenge_01_dense_collision" in out_list
        assert "challenge_05_partial_truth_vs_exact_decoy" in out_list

        # Switch to challenge 01
        buf_load = io.StringIO()
        with redirect_stdout(buf_load):
            lab.onecmd("challenge challenge_01_dense_collision")
        assert "Loaded challenge 'challenge_01_dense_collision'" in buf_load.getvalue()
        assert lab.active_challenge is not None
        assert lab.company_id == "challenge-01"

        # Verify initial state before run (will fail or mismatch expected truth)
        buf_ver = io.StringIO()
        with redirect_stdout(buf_ver):
            lab.onecmd("verify")
        assert "FAIL" in buf_ver.getvalue() or "PASS" in buf_ver.getvalue()

        # Run session and verify
        lab.onecmd("run")
        buf_ver_post = io.StringIO()
        with redirect_stdout(buf_ver_post):
            lab.onecmd("verify")
        assert "PASS" in buf_ver_post.getvalue()
    finally:
        if lab.state and not lab.state.is_closed:
            lab.state.close()


def test_temporal_challenge_definitions_load_and_have_unique_ids() -> None:
    challenges = list_temporal_challenges()
    assert len(challenges) == 8

    seen_ids: set[str] = set()
    for c in challenges:
        assert c.challenge_id not in seen_ids, f"Duplicate temporal challenge ID: {c.challenge_id}"
        seen_ids.add(c.challenge_id)
        assert 1 <= c.difficulty <= 7
        assert len(c.tags) > 0
        assert c.name
        assert c.description
        assert len(c.steps) > 0
        assert c.initial_scenario.context.company_id
        assert c.final_expected_truth is not None

    for sc_id in seen_ids:
        c = get_temporal_challenge(sc_id)
        assert c.challenge_id == sc_id

    # Legacy alias resolves without appearing in catalog
    assert "challenge_14_dirty_multi_session_month" not in seen_ids
    legacy_c = get_temporal_challenge("challenge_14_dirty_multi_session_month")
    assert legacy_c.challenge_id == "challenge_14_conservative"


def test_temporal_challenge_step_operation_ids_and_types_valid() -> None:
    for c in list_temporal_challenges():
        for step in c.steps:
            assert step.step_id
            assert step.operation is not None
            assert hasattr(step.operation, "op_type")


def test_temporal_challenge_runner_executes_and_fresh_hydration_per_session() -> None:
    runner = TemporalChallengeRunner()
    ch09 = get_temporal_challenge("challenge_09_bank_before_invoice")
    res = runner.run(ch09)

    assert len(res.step_summaries) == len(ch09.steps) + 1  # includes step 0
    # Step 0: Initial
    assert res.step_summaries[0].op_type == "INITIAL_WORLD"
    # Monotonicity check
    revisions = [s.persistence_revision for s in res.step_summaries]
    for i in range(1, len(revisions)):
        assert revisions[i] >= revisions[i - 1]


def test_lab_temporal_challenge_switching_step_and_timeline() -> None:
    lab = BookkeepingLab()
    try:
        buf_list = io.StringIO()
        with redirect_stdout(buf_list):
            lab.onecmd("temporal-challenges")
        out_list = buf_list.getvalue()
        assert "challenge_08_late_evidence_resolution" in out_list
        assert "challenge_14_conservative" in out_list
        assert "challenge_14_aggressive" in out_list
        assert "challenge_14_dirty_multi_session_month" not in out_list

        # Load temporal challenge 09
        buf_load = io.StringIO()
        with redirect_stdout(buf_load):
            lab.onecmd("temporal challenge_09_bank_before_invoice")
        assert "Loaded temporal challenge 'challenge_09_bank_before_invoice'" in buf_load.getvalue()
        assert lab.active_temporal_challenge is not None
        assert lab.temporal_step_index == 0

        # Timeline shows initial step 0
        buf_time = io.StringIO()
        with redirect_stdout(buf_time):
            lab.onecmd("timeline")
        assert "Step 0" in buf_time.getvalue()

        # Step 1
        buf_step1 = io.StringIO()
        with redirect_stdout(buf_step1):
            lab.onecmd("step")
        assert "Executed Step 1" in buf_step1.getvalue()
        assert lab.temporal_step_index == 1

        # Step 2
        buf_step2 = io.StringIO()
        with redirect_stdout(buf_step2):
            lab.onecmd("step")
        assert "Executed Step 2" in buf_step2.getvalue()
        assert lab.temporal_step_index == 2

        # Step 3
        buf_step3 = io.StringIO()
        with redirect_stdout(buf_step3):
            lab.onecmd("step")
        assert "Executed Step 3" in buf_step3.getvalue()
        assert lab.temporal_step_index == 3

        # Verify final truth
        buf_ver = io.StringIO()
        with redirect_stdout(buf_ver):
            lab.onecmd("verify")
        assert "PASS" in buf_ver.getvalue()
    finally:
        if lab.state and not lab.state.is_closed:
            lab.state.close()


def test_challenge_09_and_10_final_economic_truth_equivalence() -> None:
    runner = TemporalChallengeRunner()
    ch09 = get_temporal_challenge("challenge_09_bank_before_invoice")
    ch10 = get_temporal_challenge("challenge_10_invoice_before_bank")

    res09 = runner.run(ch09)
    res10 = runner.run(ch10)

    # Invariant: Arrival ordering alone must not alter final accounting truth
    # Reconciled allocations must be identical
    assert ch09.final_expected_truth.expected_pairwise_allocations == ch10.final_expected_truth.expected_pairwise_allocations
    assert res09.final_truth_verdict.is_match
    assert res10.final_truth_verdict.is_match

