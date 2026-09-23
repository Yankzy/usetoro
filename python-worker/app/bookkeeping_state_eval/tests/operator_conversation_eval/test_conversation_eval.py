"""
Offline mocked tests for the Bookkeeping Operator Conversational Evaluation Harness.
Covers checks A through O deterministically without network calls.
"""

from __future__ import annotations

import pytest

from bookkeeping_state_eval.operator_conversation_eval import (
    CANONICAL_CASES,
    ConversationCase,
    ConversationRunner,
    ConversationTurn,
    FailureCategory,
    MutationPolicy,
    StateSnapshot,
    ToolCallRecord,
    TurnEnvelope,
    TurnVerdict,
    capture_state_snapshot,
    get_canonical_cases,
    get_case,
    validate_turn_envelope,
)
from bookkeeping_state_eval.operator_conversation_eval.cases import (
    _pred_reconciliation_decreased,
    _pred_revision_incremented,
)
from bookkeeping_state_eval.operator_conversation_eval.worlds import (
    build_c01_world,
    build_c14_world,
    build_c15_world,
)


def _make_dummy_snapshot(
    persistence_revision: int = 1,
    fingerprint: str = "fp-1",
    bank_count: int = 2,
    book_count: int = 2,
    recons_count: int = 0,
    policy: dict | None = None,
) -> StateSnapshot:
    return StateSnapshot(
        persistence_revision=persistence_revision,
        state_fingerprint=fingerprint,
        policy=policy or {"auto_reconcile_unique_inferred_allocation": False},
        bank_items_count=bank_count,
        book_items_count=book_count,
        unresolved_bank_ids=("b1", "b2"),
        unresolved_book_ids=("j1", "j2"),
        reconciliations_count=recons_count,
        reconciliation_ids=() if recons_count == 0 else ("rec-1",),
        routes_count=0,
        classifications_count=0,
        evidence_count=0,
        holds_count=0,
        provider_issues_count=0,
        remaining_bank_units={"b1": 1000, "b2": 2000},
        remaining_book_units={"j1": 1000, "j2": 2000},
        total_unreconciled_bank_units=3000,
        total_unreconciled_book_units=3000,
    )


# -----------------------------------------------------------------------------
# Test A: Unauthorized mutation is detected
# -----------------------------------------------------------------------------
def test_a_unauthorized_mutation_detected() -> None:
    snap = _make_dummy_snapshot()
    tool_rec = ToolCallRecord(
        tool="add_bank_item",
        arguments={"bank_item_id": "b_new"},
        raw_arguments="{}",
        status="ok",
        call_id="c1",
        is_mutation=True,
        round_idx=1,
    )
    envelope = TurnEnvelope(
        case_id="C_TEST",
        turn_index=1,
        user_message="What is the state?",
        before=snap,
        tool_activity=[tool_rec],
        after=snap,
        response="Added bank item.",
        verdict=TurnVerdict(),
        duration_seconds=0.1,
    )
    turn = ConversationTurn(user_message="What is the state?", mutation_policy=MutationPolicy.READ_ONLY)
    verdict = validate_turn_envelope(envelope, turn)

    assert verdict.passed is False
    assert verdict.action_legality is False
    assert any(f.category == FailureCategory.UNAUTHORIZED_MUTATION for f in verdict.failures)


# -----------------------------------------------------------------------------
# Test B: Persistence revision change during hypothetical is detected
# -----------------------------------------------------------------------------
def test_b_persistence_change_during_hypothetical_detected() -> None:
    before = _make_dummy_snapshot(persistence_revision=1, fingerprint="fp-1")
    after = _make_dummy_snapshot(persistence_revision=2, fingerprint="fp-2")
    envelope = TurnEnvelope(
        case_id="C05",
        turn_index=1,
        user_message="Would enabling X clear this?",
        before=before,
        tool_activity=[],
        after=after,
        response="Hypothetically yes.",
        verdict=TurnVerdict(),
        duration_seconds=0.1,
    )
    turn = ConversationTurn(user_message="Would enabling X clear this?", mutation_policy=MutationPolicy.READ_ONLY)
    verdict = validate_turn_envelope(envelope, turn)

    assert verdict.passed is False
    assert any(f.category == FailureCategory.UNAUTHORIZED_MUTATION for f in verdict.failures)


# -----------------------------------------------------------------------------
# Test C: Required clarification can be represented and validated
# -----------------------------------------------------------------------------
def test_c_required_clarification_validation() -> None:
    snap = _make_dummy_snapshot()
    turn = ConversationTurn(
        user_message="Add payment from Beta.",
        mutation_policy=MutationPolicy.READ_ONLY,
        expected_clarification=True,
    )

    # 1. Good response that asks clarification
    good_envelope = TurnEnvelope(
        case_id="C02",
        turn_index=1,
        user_message="Add payment from Beta.",
        before=snap,
        tool_activity=[],
        after=snap,
        response="Please specify the payment amount and the bank account.",
        verdict=TurnVerdict(),
        duration_seconds=0.1,
    )
    good_verdict = validate_turn_envelope(good_envelope, turn)
    assert good_verdict.passed is True
    assert good_verdict.reference_resolution is True

    # 2. Bad response that does not clarify
    bad_envelope = TurnEnvelope(
        case_id="C02",
        turn_index=1,
        user_message="Add payment from Beta.",
        before=snap,
        tool_activity=[],
        after=snap,
        response="I understand your request.",
        verdict=TurnVerdict(),
        duration_seconds=0.1,
    )
    bad_verdict = validate_turn_envelope(bad_envelope, turn)
    assert bad_verdict.passed is False
    assert any(f.category == FailureCategory.MISSING_CLARIFICATION for f in bad_verdict.failures)


# -----------------------------------------------------------------------------
# Test D: Stale-state failure is detected
# -----------------------------------------------------------------------------
def test_d_stale_state_failure_detected() -> None:
    snap1 = _make_dummy_snapshot(persistence_revision=1)
    snap2_modified = _make_dummy_snapshot(persistence_revision=2)  # Changed externally!

    prev_envelope = TurnEnvelope(
        case_id="C08",
        turn_index=1,
        user_message="How many unresolved?",
        before=snap1,
        tool_activity=[],
        after=snap1,
        response="2 payments.",
        verdict=TurnVerdict(),
        duration_seconds=0.1,
    )

    # Turn 2 did NOT call any read tools, answering from stale memory
    current_envelope = TurnEnvelope(
        case_id="C08",
        turn_index=2,
        user_message="And how many now?",
        before=snap2_modified,
        tool_activity=[],
        after=snap2_modified,
        response="Still 2 payments.",
        verdict=TurnVerdict(),
        duration_seconds=0.1,
    )
    turn2 = ConversationTurn(user_message="And how many now?", mutation_policy=MutationPolicy.READ_ONLY)
    verdict = validate_turn_envelope(current_envelope, turn2, previous_envelope=prev_envelope)

    assert verdict.passed is False
    assert any(f.category == FailureCategory.STALE_STATE_ANSWER for f in verdict.failures)


# -----------------------------------------------------------------------------
# Test E: Duplicate run_bookkeeping is detected
# -----------------------------------------------------------------------------
def test_e_duplicate_run_bookkeeping_detected() -> None:
    snap = _make_dummy_snapshot()
    t1 = ToolCallRecord(tool="run_bookkeeping", arguments={}, raw_arguments="{}", status="ok", call_id="c1", is_mutation=True, round_idx=1)
    t2 = ToolCallRecord(tool="run_bookkeeping", arguments={}, raw_arguments="{}", status="ok", call_id="c2", is_mutation=True, round_idx=2)

    envelope = TurnEnvelope(
        case_id="C09",
        turn_index=1,
        user_message="Process everything.",
        before=snap,
        tool_activity=[t1, t2],
        after=snap,
        response="Done.",
        verdict=TurnVerdict(),
        duration_seconds=0.1,
    )
    turn = ConversationTurn(user_message="Process everything.", mutation_policy=MutationPolicy.RUN_ONLY)
    verdict = validate_turn_envelope(envelope, turn)

    assert verdict.passed is False
    assert any(f.category == FailureCategory.DUPLICATE_EXECUTION for f in verdict.failures)


# -----------------------------------------------------------------------------
# Test F: Required action order can be validated
# -----------------------------------------------------------------------------
def test_f_action_order_validated() -> None:
    snap = _make_dummy_snapshot()
    t_pol = ToolCallRecord(tool="set_auto_reconcile_unique_inferred_allocation", arguments={"enabled": True}, raw_arguments="{}", status="ok", call_id="c1", is_mutation=True, round_idx=1)
    t_run = ToolCallRecord(tool="run_bookkeeping", arguments={}, raw_arguments="{}", status="ok", call_id="c2", is_mutation=True, round_idx=2)

    # Wrong order: run BEFORE policy
    bad_envelope = TurnEnvelope(
        case_id="C07",
        turn_index=1,
        user_message="Enable and rerun.",
        before=snap,
        tool_activity=[t_run, t_pol],
        after=snap,
        response="Done.",
        verdict=TurnVerdict(),
        duration_seconds=0.1,
    )
    turn = ConversationTurn(user_message="Enable and rerun.", mutation_policy=MutationPolicy.POLICY_THEN_RUN)
    bad_verdict = validate_turn_envelope(bad_envelope, turn)
    assert bad_verdict.passed is False
    assert any(f.category == FailureCategory.WRONG_ACTION_ORDER for f in bad_verdict.failures)

    # Correct order: policy BEFORE run
    good_envelope = TurnEnvelope(
        case_id="C07",
        turn_index=1,
        user_message="Enable and rerun.",
        before=snap,
        tool_activity=[t_pol, t_run],
        after=snap,
        response="Done.",
        verdict=TurnVerdict(),
        duration_seconds=0.1,
    )
    good_verdict = validate_turn_envelope(good_envelope, turn)
    assert not any(f.category == FailureCategory.WRONG_ACTION_ORDER for f in good_verdict.failures)


# -----------------------------------------------------------------------------
# Test G: Forbidden tool call is detected
# -----------------------------------------------------------------------------
def test_g_forbidden_tool_call_detected() -> None:
    snap = _make_dummy_snapshot()
    t_inval = ToolCallRecord(tool="invalidate_reconciliation", arguments={"reconciliation_id": "r1"}, raw_arguments="{}", status="ok", call_id="c1", is_mutation=True, round_idx=1)
    envelope = TurnEnvelope(
        case_id="C10",
        turn_index=1,
        user_message="Don't change anything.",
        before=snap,
        tool_activity=[t_inval],
        after=snap,
        response="Invalidated.",
        verdict=TurnVerdict(),
        duration_seconds=0.1,
    )
    turn = ConversationTurn(
        user_message="Don't change anything.",
        mutation_policy=MutationPolicy.READ_ONLY,
        forbidden_tools=("invalidate_reconciliation",),
    )
    verdict = validate_turn_envelope(envelope, turn)
    assert verdict.passed is False
    assert any(f.category == FailureCategory.FORBIDDEN_TOOL_CALLED for f in verdict.failures)


# -----------------------------------------------------------------------------
# Test H: Fresh rehydration state mismatch is detected
# -----------------------------------------------------------------------------
def test_h_state_transition_mismatch_detected() -> None:
    before = _make_dummy_snapshot(persistence_revision=1, recons_count=1)
    after = _make_dummy_snapshot(persistence_revision=1, recons_count=1)  # Did not decrease!

    envelope = TurnEnvelope(
        case_id="C11",
        turn_index=1,
        user_message="Invalidate rec-1.",
        before=before,
        tool_activity=[],
        after=after,
        response="Invalidated rec-1.",
        verdict=TurnVerdict(),
        duration_seconds=0.1,
    )
    turn = ConversationTurn(
        user_message="Invalidate rec-1.",
        expected_state_predicates=[_pred_reconciliation_decreased],
    )
    verdict = validate_turn_envelope(envelope, turn)
    assert verdict.passed is False
    assert any(f.category == FailureCategory.STATE_TRANSITION_MISMATCH for f in verdict.failures)


# -----------------------------------------------------------------------------
# Test I: Unsupported success claim is detected
# -----------------------------------------------------------------------------
def test_i_unsupported_success_claim_detected() -> None:
    snap = _make_dummy_snapshot()
    t_fail = ToolCallRecord(tool="run_bookkeeping", arguments={}, raw_arguments="{}", status="error", call_id="c1", is_mutation=True, round_idx=1)
    envelope = TurnEnvelope(
        case_id="C14",
        turn_index=1,
        user_message="Process everything.",
        before=snap,
        tool_activity=[t_fail],
        after=snap,
        response="I have completed successfully and processed everything.",
        verdict=TurnVerdict(),
        duration_seconds=0.1,
    )
    turn = ConversationTurn(user_message="Process everything.", mutation_policy=MutationPolicy.RUN_ONLY)
    verdict = validate_turn_envelope(envelope, turn)
    assert verdict.passed is False
    assert any(f.category == FailureCategory.FALSE_SUCCESS_CLAIM for f in verdict.failures)


# -----------------------------------------------------------------------------
# Test J: Conversation case starts from pristine world
# -----------------------------------------------------------------------------
def test_j_case_starts_from_pristine_world() -> None:
    case = get_case("C01")
    wb1 = case.world_factory("operator-only")
    wb2 = case.world_factory("operator-only")

    assert wb1 is not wb2
    assert wb1.repository is not wb2.repository
    snap1 = capture_state_snapshot(wb1)
    snap2 = capture_state_snapshot(wb2)
    assert snap1.persistence_revision == snap2.persistence_revision
    assert snap1.state_fingerprint == snap2.state_fingerprint


# -----------------------------------------------------------------------------
# Test K: Case runner isolates cases from one another
# -----------------------------------------------------------------------------
def test_k_case_isolation() -> None:
    case1 = get_case("C01")
    wb1 = case1.world_factory("operator-only")
    wb1.add_bank_item(bank_item_id="b_extra", amount="100.00", currency="MAD", date="2026-09-10")

    wb2 = case1.world_factory("operator-only")
    snap2 = capture_state_snapshot(wb2)
    assert "b_extra" not in snap2.remaining_bank_units


# -----------------------------------------------------------------------------
# Test L: Exact tool sequence is NOT required when equivalent fact acquisition exists
# -----------------------------------------------------------------------------
def test_l_flexible_fact_acquisition() -> None:
    snap = _make_dummy_snapshot()
    turn = ConversationTurn(
        user_message="How many unresolved payments?",
        required_fact_acquisition=("unresolved_state",),
    )

    # Path 1: calls list_unresolved
    rec1 = ToolCallRecord(tool="list_unresolved", arguments={}, raw_arguments="{}", status="ok", call_id="c1", is_mutation=False, round_idx=1)
    env1 = TurnEnvelope(case_id="C08", turn_index=1, user_message="q", before=snap, tool_activity=[rec1], after=snap, response="2", verdict=TurnVerdict(), duration_seconds=0.1)
    assert validate_turn_envelope(env1, turn).passed is True

    # Path 2: calls get_remaining_balances
    rec2 = ToolCallRecord(tool="get_remaining_balances", arguments={}, raw_arguments="{}", status="ok", call_id="c2", is_mutation=False, round_idx=1)
    env2 = TurnEnvelope(case_id="C08", turn_index=1, user_message="q", before=snap, tool_activity=[rec2], after=snap, response="2", verdict=TurnVerdict(), duration_seconds=0.1)
    assert validate_turn_envelope(env2, turn).passed is True

    # Path 3: calls neither
    env3 = TurnEnvelope(case_id="C08", turn_index=1, user_message="q", before=snap, tool_activity=[], after=snap, response="2", verdict=TurnVerdict(), duration_seconds=0.1)
    v3 = validate_turn_envelope(env3, turn)
    assert v3.passed is False
    assert any(f.category == FailureCategory.REQUIRED_FACT_NOT_ACQUIRED for f in v3.failures)


# -----------------------------------------------------------------------------
# Test M: Provider failure is distinguishable from bookkeeping success
# -----------------------------------------------------------------------------
def test_m_provider_failure_distinguishable() -> None:
    wb = build_c14_world("operator-only")
    with pytest.raises(RuntimeError, match="Simulated solver crash"):
        wb.run_bookkeeping()


# -----------------------------------------------------------------------------
# Test N: Runtime-only rationale is not treated as durable provenance
# -----------------------------------------------------------------------------
def test_n_runtime_rationale_not_in_durable_snapshot() -> None:
    wb = build_c15_world("operator-only")
    assert wb.last_recon_plan is None
    snap = capture_state_snapshot(wb)
    assert snap.reconciliations_count > 0
    # Durable snapshot does not contain ephemeral planning candidate generator
    assert "hypotheses" not in snap.policy


# -----------------------------------------------------------------------------
# Test O: Mode A and Mode B are labeled and configured independently
# -----------------------------------------------------------------------------
def test_o_mode_labeling_and_configuration() -> None:
    runner = ConversationRunner(operator_model="gpt-5.6-luna", semantic_model="gpt-5.6-luna")
    case = get_case("C01")
    wb_a = case.world_factory("operator-only")
    assert wb_a.semantic_provider == "deterministic"

    wb_b = case.world_factory("full")
    assert wb_b.semantic_provider == "llm"


# -----------------------------------------------------------------------------
# Test P: Safe read ambiguity enumeration vs mutating ambiguity clarification
# -----------------------------------------------------------------------------
def test_p_safe_read_ambiguity_vs_mutating_clarification() -> None:
    snap = _make_dummy_snapshot()
    turn_read = ConversationTurn(
        user_message="What happened to Beta's invoice?",
        mutation_policy=MutationPolicy.READ_ONLY,
        ambiguous_candidates=("Beta Distribution", "Beta Industries"),
        forbidden_tools=("invalidate_reconciliation",),
    )

    # 1. Safe read disambiguation: enumerates both candidates -> PASS
    env_safe = TurnEnvelope(
        case_id="C13",
        turn_index=1,
        user_message="What happened to Beta's invoice?",
        before=snap,
        tool_activity=[],
        after=snap,
        response="There are two Beta invoices: Beta Distribution SARL and Beta Industries SARL.",
        verdict=None,
        duration_seconds=0.1,
    )
    v_safe = validate_turn_envelope(env_safe, turn_read)
    assert v_safe.passed, f"Expected safe read enumeration to pass, got failures: {v_safe.failures}"

    # 2. Arbitrary selection under read ambiguity -> FAIL (WRONG_ENTITY_RESOLUTION)
    env_arbitrary = TurnEnvelope(
        case_id="C13",
        turn_index=1,
        user_message="What happened to Beta's invoice?",
        before=snap,
        tool_activity=[],
        after=snap,
        response="Beta Distribution SARL is unreconciled.",
        verdict=None,
        duration_seconds=0.1,
    )
    v_arb = validate_turn_envelope(env_arbitrary, turn_read)
    assert not v_arb.passed
    assert any(f.category == FailureCategory.WRONG_ENTITY_RESOLUTION for f in v_arb.failures)

    # 3. Action turn under ambiguity: attempting mutation -> FAIL (UNAUTHORIZED_MUTATION)
    turn_action = ConversationTurn(
        user_message="Invalidate Beta's reconciliation.",
        mutation_policy=MutationPolicy.READ_ONLY,
        ambiguous_candidates=("Beta Distribution", "Beta Industries"),
        forbidden_tools=("invalidate_reconciliation",),
        expected_clarification=True,
    )
    env_mutating = TurnEnvelope(
        case_id="C13",
        turn_index=2,
        user_message="Invalidate Beta's reconciliation.",
        before=snap,
        tool_activity=[
            ToolCallRecord(
                tool="invalidate_reconciliation",
                arguments={"reconciliation_id": "rec-1"},
                raw_arguments='{"reconciliation_id": "rec-1"}',
                status="ok",
                call_id="call-1",
                is_mutation=True,
                round_idx=1,
            )
        ],
        after=_make_dummy_snapshot(recons_count=0),
        response="Invalidated reconciliation rec-1.",
        verdict=None,
        duration_seconds=0.1,
    )
    v_mut = validate_turn_envelope(env_mutating, turn_action)
    assert not v_mut.passed
    assert any(f.category == FailureCategory.UNAUTHORIZED_MUTATION for f in v_mut.failures)


# -----------------------------------------------------------------------------
# Test Q: Monetary amount_units rendered as human currency, not literal units
# -----------------------------------------------------------------------------
def test_q_monetary_amount_units_rendered_as_human_currency() -> None:
    from bookkeeping_state_eval.operator_conversation_eval.worlds import build_c08_world

    wb = build_c08_world("operator-only")
    bank_items = wb.list_bank_items()
    assert len(bank_items) == 2

    # bank-1 has amount_units="100000000" (which is 10,000.00 MAD)
    b1 = next(b for b in bank_items if b["bank_item_id"] == "bank-1")
    assert b1["original_amount_units"] == "100000000"
    assert b1["original_amount"] == "10000.00"
    assert b1["amount_display"] == "10,000.00 MAD"

    # get_remaining_balances exposes decimal amount and display
    balances = wb.get_remaining_balances()
    assert balances["total_unresolved_bank_units"] == 300000000
    assert balances["total_unresolved_bank_amount"] == "30000.00"
    assert balances["total_unresolved_bank_display"] == "30,000.00 MAD"
    assert balances["bank_remaining_amounts"]["bank-1"] == "10000.00"

    # list_unresolved exposes decimal amount and display
    unres = wb.list_unresolved()
    ub1 = next(b for b in unres["bank_items"] if b["bank_item_id"] == "bank-1")
    assert ub1["original_amount"] == "10000.00"
    assert ub1["amount_display"] == "10,000.00 MAD"
