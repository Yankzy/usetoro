"""
Comprehensive unit test suite for the Natural-Language Bookkeeping Operator.

Covers:
- (A) All 14 read workbench tools
- (B) Single-runtime session execution tool (run_bookkeeping)
- (C) All 7 fail-closed mutation tools
- (D) Unknown tool handling
- (E) Malformed JSON and invalid arguments handling
- (F) Missing mutation arguments validation
- (G) Fail-closed post-commit hydration failure semantics
- (H) Rejection handling & non-advancement of revision
- (I) Loop bounding (max_rounds guard)
- (J) Multi-turn conversational memory & clear_chat
- (K) Workbench security & isolation boundary
- (L) REPL lab.py integration (--operator flag, default dispatch, clear-chat, operator-trace)
"""

from __future__ import annotations

import io
import json
from contextlib import redirect_stdout
from typing import Any, Generator
from unittest.mock import AsyncMock, MagicMock, patch

import pytest

from bookkeeping_state.hydration.hydrator import BookkeepingHydrator
from bookkeeping_state_eval.lab import BookkeepingLab
from bookkeeping_state.operator.agent import BookkeepingOperator
from bookkeeping_state.operator.tools import (
    OPERATOR_TOOLS,
    execute_operator_tool,
)
from bookkeeping_state_eval.operator.workbench import (
    BookkeepingWorkbench,
)


@pytest.fixture
def workbench() -> Generator[BookkeepingWorkbench, None, None]:
    wb = BookkeepingWorkbench(semantic_provider="deterministic")
    yield wb
    wb.close_state()


# -----------------------------------------------------------------------------
# (A) Read Tools Tests
# -----------------------------------------------------------------------------


def test_operator_read_tools(workbench: BookkeepingWorkbench) -> None:
    """Verify all 14 read tools execute cleanly and return typed JSON results."""
    # 1. get_state
    res = execute_operator_tool(workbench, "get_state", {})
    assert res["status"] == "ok"
    assert res["result"]["is_closed"] is False
    assert res["result"]["company_id"] == "demo-company"
    assert res["result"]["bank_items_count"] == 5
    assert res["result"]["book_items_count"] == 5

    # 2. list_bank_items
    res = execute_operator_tool(workbench, "list_bank_items", {})
    assert res["status"] == "ok"
    assert len(res["result"]) == 5
    assert any(b["id"] == "bank-aws" for b in res["result"])

    # Filtered list_bank_items
    res = execute_operator_tool(workbench, "list_bank_items", {"status": "UNRECONCILED"})
    assert res["status"] == "ok"
    assert len(res["result"]) == 5

    # 3. list_book_items
    res = execute_operator_tool(workbench, "list_book_items", {})
    assert res["status"] == "ok"
    assert len(res["result"]) == 5

    # 4. get_remaining_balances
    res = execute_operator_tool(workbench, "get_remaining_balances", {})
    assert res["status"] == "ok"
    assert "total_unreconciled_bank_units" in res["result"]
    assert "total_unreconciled_book_units" in res["result"]
    assert len(res["result"]["bank_items"]) == 5

    # 5. list_unresolved
    res = execute_operator_tool(workbench, "list_unresolved", {})
    assert res["status"] == "ok"
    assert res["result"]["unreconciled_bank_items_count"] == 5
    assert res["result"]["unreconciled_book_items_count"] == 5

    # 6. list_review_candidates (initially empty prior to run)
    res = execute_operator_tool(workbench, "list_review_candidates", {})
    assert res["status"] == "ok"
    assert isinstance(res["result"], list)

    # 7. list_reconciliations
    res = execute_operator_tool(workbench, "list_reconciliations", {})
    assert res["status"] == "ok"
    assert isinstance(res["result"], list)

    # 8. list_routes
    res = execute_operator_tool(workbench, "list_routes", {})
    assert res["status"] == "ok"
    assert isinstance(res["result"], list)

    # 9. list_classifications
    res = execute_operator_tool(workbench, "list_classifications", {})
    assert res["status"] == "ok"
    assert isinstance(res["result"], list)

    # 10. get_evidence
    res = execute_operator_tool(workbench, "get_evidence", {})
    assert res["status"] == "ok"
    assert isinstance(res["result"], list)

    # 11. get_history
    res = execute_operator_tool(workbench, "get_history", {})
    assert res["status"] == "ok"
    assert res["result"]["company_id"] == "demo-company"
    assert res["result"]["persistence_revision"] == 1

    # 12. get_policy
    res = execute_operator_tool(workbench, "get_policy", {})
    assert res["status"] == "ok"
    assert "auto_reconcile_unique_inferred_allocation" in res["result"]

    # 13. get_holds
    res = execute_operator_tool(workbench, "get_holds", {})
    assert res["status"] == "ok"
    assert isinstance(res["result"], list)

    # 14. get_provider_issues
    res = execute_operator_tool(workbench, "get_provider_issues", {})
    assert res["status"] == "ok"
    assert isinstance(res["result"], list)


# -----------------------------------------------------------------------------
# (B) Execution Tool (run_bookkeeping) Tests
# -----------------------------------------------------------------------------


def test_operator_run_bookkeeping(workbench: BookkeepingWorkbench) -> None:
    """Verify run_bookkeeping executes single runtime session and captures plan/stages."""
    res = execute_operator_tool(workbench, "run_bookkeeping", {})
    assert res["status"] == "ok"
    result = res["result"]
    assert result["is_success"] is True
    assert result["routing"]["status"] == "APPLIED"
    assert result["dag"]["status"] == "APPLIED"
    assert result["reconciliation"]["status"] == "APPLIED"

    # Captured plan contains reconciliations and candidate review hypotheses
    assert "review_candidates_count" in result
    assert result["final_persistence_revision"] > result["starting_persistence_revision"]

    # Invariant: fresh inspection state S0
    st_res = execute_operator_tool(workbench, "get_state", {})
    assert st_res["result"]["local_state_revision"] == 0
    assert st_res["result"]["persistence_revision"] == result["final_persistence_revision"]


# -----------------------------------------------------------------------------
# (C) Mutation Tools Tests
# -----------------------------------------------------------------------------


def test_operator_add_bank_item(workbench: BookkeepingWorkbench) -> None:
    """Verify add_bank_item appends transaction and advances persistence revision."""
    init_state = workbench.get_state()
    prev_p = init_state["persistence_revision"]

    res = execute_operator_tool(
        workbench,
        "add_bank_item",
        {
            "bank_item_id": "bank_new_01",
            "amount": "5000.00",
            "currency": "MAD",
            "date": "2026-09-10",
            "counterparty": "Client Delta",
            "description": "Payment Delta",
        },
    )
    assert res["status"] == "ok"
    assert res["result"]["applied"] is True
    assert res["result"]["persistence_revision"] == prev_p + 1

    # Verify item is visible in inspection state
    items = workbench.list_bank_items()
    assert any(b["id"] == "bank_new_01" for b in items)


def test_operator_add_book_item(workbench: BookkeepingWorkbench) -> None:
    """Verify add_book_item appends invoice and advances persistence revision."""
    init_state = workbench.get_state()
    prev_p = init_state["persistence_revision"]

    res = execute_operator_tool(
        workbench,
        "add_book_item",
        {
            "book_item_id": "inv_new_01",
            "amount": "5000.00",
            "currency": "MAD",
            "date": "2026-09-08",
            "item_type": "INVOICE",
            "counterparty": "Client Delta",
            "reference": "INV-DELTA-1",
        },
    )
    assert res["status"] == "ok"
    assert res["result"]["applied"] is True
    assert res["result"]["persistence_revision"] == prev_p + 1

    # Verify item is visible in inspection state
    items = workbench.list_book_items()
    assert any(b["id"] == "inv_new_01" for b in items)


def test_operator_add_counterparty(workbench: BookkeepingWorkbench) -> None:
    """Verify add_counterparty registers entity with aliases."""
    res = execute_operator_tool(
        workbench,
        "add_counterparty",
        {
            "counterparty_id": "cp_delta",
            "name": "Delta Corporation",
            "counterparty_type": "CUSTOMER",
            "aliases": ["Delta Corp", "Delta Maroc"],
        },
    )
    assert res["status"] == "ok"
    assert res["result"]["applied"] is True


def test_operator_assert_and_invalidate_evidence(workbench: BookkeepingWorkbench) -> None:
    """Verify asserting and invalidating evidence through TransitionEngine."""
    # Assert evidence
    res_assert = execute_operator_tool(
        workbench,
        "assert_book_item_evidence",
        {
            "evidence_id": "ev_email_101",
            "book_item_id": "book-aws",
            "evidence_type": "EMAIL_THREAD",
            "raw_text": "Invoice AWS paid via card",
            "confidence": 0.95,
        },
    )
    assert res_assert["status"] == "ok"
    assert res_assert["result"]["applied"] is True

    # Verify evidence is visible
    evs = workbench.get_evidence(book_item_id="book-aws")
    assert any(e["assertion_id"] == "ev_email_101" for e in evs)

    # Invalidate evidence
    res_inval = execute_operator_tool(
        workbench,
        "invalidate_evidence",
        {
            "evidence_id": "ev_email_101",
            "reason": "Superseded by corrected bank memo",
        },
    )
    assert res_inval["status"] == "ok"
    assert res_inval["result"]["applied"] is True

    # Verify evidence is marked invalid or removed from active truth
    evs_after = workbench.get_evidence(book_item_id="book-aws")
    assert not any(e["assertion_id"] == "ev_email_101" and e.get("is_valid") for e in evs_after)


def test_operator_invalidate_reconciliation(workbench: BookkeepingWorkbench) -> None:
    """Verify invalidating reconciliation releases capacity and records transition."""
    # Run bookkeeping to produce reconciliations
    workbench.run_bookkeeping()
    recons = workbench.list_reconciliations()
    assert len(recons) > 0
    rec_to_cancel = recons[0]["reconciliation_id"]

    # Invalidate
    res = execute_operator_tool(
        workbench,
        "invalidate_reconciliation",
        {
            "reconciliation_id": rec_to_cancel,
            "reason": "Duplicate bank feed error",
        },
    )
    assert res["status"] == "ok"
    assert res["result"]["applied"] is True

    # Verify reconciliation is no longer active
    recons_after = workbench.list_reconciliations()
    assert not any(r["reconciliation_id"] == rec_to_cancel for r in recons_after)


def test_operator_set_policy(workbench: BookkeepingWorkbench) -> None:
    """Verify updating policy auto_reconcile flag."""
    res = execute_operator_tool(
        workbench,
        "set_auto_reconcile_unique_inferred_allocation",
        {"enabled": True},
    )
    assert res["status"] == "ok"
    assert res["result"]["applied"] is True

    pol = workbench.get_policy()
    assert pol["auto_reconcile_unique_inferred_allocation"] is True


# -----------------------------------------------------------------------------
# (D), (E), (F) Error Handling Tests
# -----------------------------------------------------------------------------


def test_operator_unknown_tool(workbench: BookkeepingWorkbench) -> None:
    """Verify unknown tool rejection returns clean error dict."""
    res = execute_operator_tool(workbench, "non_existent_tool", {})
    assert res["status"] == "error"
    assert "Unknown tool" in res["error"]
    assert res["tool"] == "non_existent_tool"


def test_operator_malformed_arguments(workbench: BookkeepingWorkbench) -> None:
    """Verify malformed arguments return clean error dict without uncaught exception."""
    res = execute_operator_tool(workbench, "list_bank_items", "{not-valid-json}")
    assert res["status"] == "error"
    assert "Malformed JSON" in res["error"]

    res_list = execute_operator_tool(workbench, "list_bank_items", "[1, 2, 3]")
    assert res["status"] == "error"
    assert "must be a JSON object" in res_list["error"]


def test_operator_missing_required_mutation_fields(workbench: BookkeepingWorkbench) -> None:
    """Verify missing required mutation arguments returns clean error dict."""
    res = execute_operator_tool(workbench, "add_bank_item", {"bank_item_id": "bank_x"})
    assert res["status"] == "error"
    assert "KeyError" in res["error"] or "amount" in res["error"]


# -----------------------------------------------------------------------------
# (G) Fail-Closed Post-Commit Hydration Failure Semantics
# -----------------------------------------------------------------------------


def test_operator_fail_closed_post_commit_hydration(workbench: BookkeepingWorkbench) -> None:
    """
    Verify that if commit succeeds but post-commit hydration fails:
    1. Inspection state is closed (self.state = None)
    2. Stale state is never resurrected
    3. Error dictionary reports catastrophic hydration failure
    """
    # Force hydrator to fail during post-commit hydration
    with patch.object(BookkeepingHydrator, "hydrate", side_effect=RuntimeError("Hydration crash")):
        res = execute_operator_tool(
            workbench,
            "add_bank_item",
            {
                "bank_item_id": "txn_crash_01",
                "amount": "100.00",
                "currency": "MAD",
                "date": "2026-09-01",
            },
        )
        assert res["status"] == "ok"
        result = res["result"]
        assert result["success"] is False
        assert result["state_closed"] is True
        assert "Catastrophic error: post-commit state hydration failed" in result["error"]
        assert workbench.state is None


# -----------------------------------------------------------------------------
# (H) Rejection Handling Tests
# -----------------------------------------------------------------------------


def test_operator_rejection_handling(workbench: BookkeepingWorkbench) -> None:
    """Verify transition rejection does not advance persistence revision."""
    init_state = workbench.get_state()
    prev_p = init_state["persistence_revision"]

    # Invalidate a non-existent reconciliation
    res = execute_operator_tool(
        workbench,
        "invalidate_reconciliation",
        {
            "reconciliation_id": "rec_does_not_exist",
            "reason": "Test non-existent",
        },
    )
    assert res["status"] == "ok"
    assert res["result"]["applied"] is False
    assert "rejection_code" in res["result"]

    # Invariant: persistence revision was not advanced
    curr_state = workbench.get_state()
    assert curr_state["persistence_revision"] == prev_p


# -----------------------------------------------------------------------------
# (I) Loop Bounding Tests
# -----------------------------------------------------------------------------


def test_operator_loop_bounding(workbench: BookkeepingWorkbench) -> None:
    """Verify operator loop terminates at max_rounds if model emits infinite tool calls."""
    # Mock an LLM response that perpetually emits tool calls
    mock_tool_call = MagicMock()
    mock_tool_call.type = "function_call"
    mock_tool_call.name = "get_state"
    mock_tool_call.arguments = "{}"
    mock_tool_call.call_id = "call_perpetual"
    mock_tool_call.model_dump.return_value = {
        "type": "function_call",
        "name": "get_state",
        "arguments": "{}",
        "call_id": "call_perpetual",
    }

    mock_response = MagicMock()
    mock_response.output = [mock_tool_call]
    mock_response.output_text = ""

    mock_client = AsyncMock()
    mock_client.responses.create.return_value = mock_response

    operator = BookkeepingOperator(
        workbench=workbench,
        client=mock_client,
        max_rounds=3,  # Set small max for test speed
    )

    reply = operator.handle_message("Check the state endlessly")
    assert "Operator paused: reached maximum tool reasoning limit" in reply
    assert mock_client.responses.create.call_count == 3


# -----------------------------------------------------------------------------
# (J) Multi-turn Conversational Memory & clear_chat Tests
# -----------------------------------------------------------------------------


def test_operator_conversational_memory_and_clear_chat(workbench: BookkeepingWorkbench) -> None:
    """Verify multi-turn memory accumulation and clear_chat behavior."""
    mock_response_1 = MagicMock()
    mock_response_1.output = []
    mock_response_1.output_text = "The remaining balance for payroll is 40000 MAD."

    mock_response_2 = MagicMock()
    mock_response_2.output = []
    mock_response_2.output_text = "It has not been matched because no bank item has cleared it yet."

    mock_client = AsyncMock()
    mock_client.responses.create.side_effect = [mock_response_1, mock_response_2]

    operator = BookkeepingOperator(workbench=workbench, client=mock_client)

    # Turn 1
    reply_1 = operator.handle_message("How much is remaining for payroll?")
    assert "40000 MAD" in reply_1
    assert len(operator.history) == 2
    assert operator.history[0] == {"role": "user", "content": "How much is remaining for payroll?"}
    assert operator.history[1] == {"role": "assistant", "content": "The remaining balance for payroll is 40000 MAD."}

    # Turn 2 (with pronoun context)
    reply_2 = operator.handle_message("Why has it not been matched?")
    assert "cleared it yet" in reply_2
    assert len(operator.history) == 4

    # Verify second call included prior history
    second_call_input = mock_client.responses.create.call_args_list[1].kwargs["input"]
    assert len(second_call_input) == 3
    assert second_call_input[0]["role"] == "user"
    assert second_call_input[1]["role"] == "assistant"
    assert second_call_input[2]["content"] == "Why has it not been matched?"

    # Clear chat
    operator.clear_chat()
    assert len(operator.history) == 0


# -----------------------------------------------------------------------------
# (K) Workbench Security and Isolation Boundary
# -----------------------------------------------------------------------------


def test_operator_security_boundary(workbench: BookkeepingWorkbench) -> None:
    """
    Verify operator tool definitions provide no escape hatches:
    - No shell execution
    - No filesystem access
    - No raw SQL / Python execution
    - Exactly 22 tools exposed
    """
    assert len(OPERATOR_TOOLS) == 22
    tool_names = {t["name"] for t in OPERATOR_TOOLS}

    forbidden_patterns = ["bash", "shell", "eval", "exec", "sql", "file", "disk", "python", "subprocess"]
    for name in tool_names:
        for forbidden in forbidden_patterns:
            assert forbidden not in name.lower(), f"Tool {name} contains forbidden keyword {forbidden}"

    # Verify all tools have descriptions and parameter specs
    for t in OPERATOR_TOOLS:
        assert t["type"] == "function"
        assert t["description"]
        assert "parameters" in t


# -----------------------------------------------------------------------------
# (L) REPL Lab Integration Tests
# -----------------------------------------------------------------------------


def test_lab_operator_integration_disabled_by_default() -> None:
    """Verify that without --operator, natural language falls back to informative message."""
    lab = BookkeepingLab(operator=False)
    try:
        buf = io.StringIO()
        with redirect_stdout(buf):
            lab.onecmd("why didn't Delta reconcile?")

        output = buf.getvalue()
        assert "Unknown command" in output
        assert "--operator" in output
    finally:
        lab.do_quit("")


def test_lab_operator_integration_enabled_and_trace_toggle() -> None:
    """Verify that with operator enabled, natural language routes to operator."""
    lab = BookkeepingLab(operator=True)
    try:
        assert lab.operator is not None
        assert lab.operator_enabled is True

        # Mock operator reply
        lab.operator.handle_message = MagicMock(return_value="Delta did not reconcile because it is held for review.")

        buf = io.StringIO()
        with redirect_stdout(buf):
            lab.onecmd("why didn't Delta reconcile?")

        output = buf.getvalue()
        assert "Toro Operator is thinking..." in output
        assert "Toro:\nDelta did not reconcile because it is held for review." in output

        # Test operator-trace toggle
        buf_trace = io.StringIO()
        with redirect_stdout(buf_trace):
            lab.onecmd("operator-trace")
        assert "Operator detailed tracing is now ON" in buf_trace.getvalue()

        # Test clear-chat command
        lab.operator.history.append({"role": "user", "content": "hello"})
        buf_clear = io.StringIO()
        with redirect_stdout(buf_clear):
            lab.onecmd("clear-chat")
        assert "Operator conversational memory cleared" in buf_clear.getvalue()
        assert len(lab.operator.history) == 0

    finally:
        lab.do_quit("")


# -----------------------------------------------------------------------------
# (M) Mutation Intent Rule & Hypothetical Query Tests (A, B, C)
# -----------------------------------------------------------------------------


def test_hypothetical_query_does_not_mutate_state_or_policy(workbench: BookkeepingWorkbench) -> None:
    """
    Test A: 'What would happen if X were enabled?'
    Hypothetical questions must be strictly read-only and must NOT invoke
    mutation tools (set_auto_reconcile_unique_inferred_allocation, run_bookkeeping, etc.).
    """
    workbench.load_challenge("challenge_14_conservative")
    init_state = workbench.get_state()
    init_policy = workbench.get_policy()
    prev_persistence = init_state["persistence_revision"]
    assert init_policy.get("auto_reconcile_unique_inferred_allocation") is False

    # Simulate model following the prompt rule: it calls read tools (get_policy, list_review_candidates)
    mock_tool_1 = MagicMock()
    mock_tool_1.type = "function_call"
    mock_tool_1.name = "get_policy"
    mock_tool_1.arguments = "{}"
    mock_tool_1.call_id = "call_p1"
    mock_tool_1.model_dump.return_value = {
        "type": "function_call",
        "name": "get_policy",
        "arguments": "{}",
        "call_id": "call_p1",
    }

    mock_tool_2 = MagicMock()
    mock_tool_2.type = "function_call"
    mock_tool_2.name = "list_review_candidates"
    mock_tool_2.arguments = "{}"
    mock_tool_2.call_id = "call_rc2"
    mock_tool_2.model_dump.return_value = {
        "type": "function_call",
        "name": "list_review_candidates",
        "arguments": "{}",
        "call_id": "call_rc2",
    }

    mock_resp_1 = MagicMock()
    mock_resp_1.output = [mock_tool_1, mock_tool_2]
    mock_resp_1.output_text = ""

    mock_resp_2 = MagicMock()
    mock_resp_2.output = []
    mock_resp_2.output_text = (
        "If automatic unique inferred reconciliation were enabled, the candidate with "
        "UNIQUE_INFERENCE allocation support would become eligible for automatic reconciliation on the next run."
    )

    mock_client = AsyncMock()
    mock_client.responses.create.side_effect = [mock_resp_1, mock_resp_2]

    recorded_tools = []

    def trace_cb(ev: str, payload: dict[str, Any]) -> None:
        if ev == "tool_call":
            recorded_tools.append(payload.get("tool"))

    operator = BookkeepingOperator(
        workbench=workbench,
        client=mock_client,
        trace_callback=trace_cb,
    )

    reply = operator.handle_message("What would happen if automatic unique inferred reconciliation were enabled?")

    # Verify no mutation tools called
    assert "set_auto_reconcile_unique_inferred_allocation" not in recorded_tools
    assert "run_bookkeeping" not in recorded_tools
    assert recorded_tools == ["get_policy", "list_review_candidates"]

    # Verify state and policy did NOT change
    current_state = workbench.get_state()
    current_policy = workbench.get_policy()
    assert current_state["persistence_revision"] == prev_persistence
    assert current_policy.get("auto_reconcile_unique_inferred_allocation") is False
    assert "If automatic unique inferred reconciliation were enabled" in reply


def test_explicit_enable_calls_mutation_tool_only(workbench: BookkeepingWorkbench) -> None:
    """
    Test B: 'Enable X'
    Explicit action to enable policy permits calling set_auto_reconcile_unique_inferred_allocation,
    but must NOT execute run_bookkeeping unless requested.
    """
    workbench.load_challenge("challenge_14_conservative")
    init_policy = workbench.get_policy()
    assert init_policy.get("auto_reconcile_unique_inferred_allocation") is False

    mock_tool = MagicMock()
    mock_tool.type = "function_call"
    mock_tool.name = "set_auto_reconcile_unique_inferred_allocation"
    mock_tool.arguments = json.dumps({"enabled": True})
    mock_tool.call_id = "call_set_pol"
    mock_tool.model_dump.return_value = {
        "type": "function_call",
        "name": "set_auto_reconcile_unique_inferred_allocation",
        "arguments": json.dumps({"enabled": True}),
        "call_id": "call_set_pol",
    }

    mock_resp_1 = MagicMock()
    mock_resp_1.output = [mock_tool]
    mock_resp_1.output_text = ""

    mock_resp_2 = MagicMock()
    mock_resp_2.output = []
    mock_resp_2.output_text = "I have enabled automatic unique inferred reconciliation in the accounting policy."

    mock_client = AsyncMock()
    mock_client.responses.create.side_effect = [mock_resp_1, mock_resp_2]

    recorded_tools = []

    def trace_cb(ev: str, payload: dict[str, Any]) -> None:
        if ev == "tool_call":
            recorded_tools.append(payload.get("tool"))

    operator = BookkeepingOperator(
        workbench=workbench,
        client=mock_client,
        trace_callback=trace_cb,
    )

    reply = operator.handle_message("Enable automatic unique inferred reconciliation.")

    # Verify mutation tool called, but run_bookkeeping NOT called
    assert "set_auto_reconcile_unique_inferred_allocation" in recorded_tools
    assert "run_bookkeeping" not in recorded_tools

    # Verify policy was indeed mutated in workbench
    updated_policy = workbench.get_policy()
    assert updated_policy.get("auto_reconcile_unique_inferred_allocation") is True


def test_explicit_enable_and_run_calls_mutation_and_run(workbench: BookkeepingWorkbench) -> None:
    """
    Test C: 'Enable X and run the books again'
    Explicit action to enable policy AND run executes both set_auto_reconcile_unique_inferred_allocation
    and run_bookkeeping.
    """
    workbench.load_challenge("challenge_14_conservative")
    init_state = workbench.get_state()
    prev_persistence = init_state["persistence_revision"]

    mock_tool_1 = MagicMock()
    mock_tool_1.type = "function_call"
    mock_tool_1.name = "set_auto_reconcile_unique_inferred_allocation"
    mock_tool_1.arguments = json.dumps({"enabled": True})
    mock_tool_1.call_id = "call_set_pol"
    mock_tool_1.model_dump.return_value = {
        "type": "function_call",
        "name": "set_auto_reconcile_unique_inferred_allocation",
        "arguments": json.dumps({"enabled": True}),
        "call_id": "call_set_pol",
    }

    mock_tool_2 = MagicMock()
    mock_tool_2.type = "function_call"
    mock_tool_2.name = "run_bookkeeping"
    mock_tool_2.arguments = "{}"
    mock_tool_2.call_id = "call_run_b"
    mock_tool_2.model_dump.return_value = {
        "type": "function_call",
        "name": "run_bookkeeping",
        "arguments": "{}",
        "call_id": "call_run_b",
    }

    mock_resp_1 = MagicMock()
    mock_resp_1.output = [mock_tool_1]
    mock_resp_1.output_text = ""

    mock_resp_2 = MagicMock()
    mock_resp_2.output = [mock_tool_2]
    mock_resp_2.output_text = ""

    mock_resp_3 = MagicMock()
    mock_resp_3.output = []
    mock_resp_3.output_text = "Enabled auto reconcile policy and reran bookkeeping. All items are now reconciled."

    mock_client = AsyncMock()
    mock_client.responses.create.side_effect = [mock_resp_1, mock_resp_2, mock_resp_3]

    recorded_tools = []

    def trace_cb(ev: str, payload: dict[str, Any]) -> None:
        if ev == "tool_call":
            recorded_tools.append(payload.get("tool"))

    operator = BookkeepingOperator(
        workbench=workbench,
        client=mock_client,
        trace_callback=trace_cb,
    )

    reply = operator.handle_message("Enable automatic unique inferred reconciliation and run the books again.")

    # Verify both tools called
    assert "set_auto_reconcile_unique_inferred_allocation" in recorded_tools
    assert "run_bookkeeping" in recorded_tools

    # Verify policy changed and persistence advanced
    updated_policy = workbench.get_policy()
    updated_state = workbench.get_state()
    assert updated_policy.get("auto_reconcile_unique_inferred_allocation") is True
    assert updated_state["persistence_revision"] > prev_persistence

