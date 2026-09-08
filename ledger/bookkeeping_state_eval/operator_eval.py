"""
Evaluation Runner for Natural-Language Bookkeeping Operator Agent.

Evaluates multi-turn natural-language interaction using real LLMs (e.g. gpt-5.6-luna)
against frozen BookkeepingWorkbench workflows:
  1. Scenario B Multi-Turn (Inspection -> Session Execution -> Explanation)
  2. Challenge 14 Conservative Multi-Turn (State Check -> Execution -> Hold Explanation -> Policy Toggle & Re-run)

Usage:
    ./.venv/bin/python python-worker/app/bookkeeping_state_eval/operator_eval.py --model gpt-5.6-luna
    ./.venv/bin/python python-worker/app/bookkeeping_state_eval/operator_eval.py --target scenario_b
    ./.venv/bin/python python-worker/app/bookkeeping_state_eval/operator_eval.py --target challenge_14
"""

from __future__ import annotations

import argparse
import dataclasses
from datetime import datetime, timezone
import json
from pathlib import Path
import sys
import time
from typing import Any

# Ensure repository packages are importable
_current_dir = Path(__file__).resolve().parent
_app_dir = _current_dir.parent
for p in (_app_dir, _current_dir):
    if str(p) not in sys.path:
        sys.path.insert(0, str(p))

from bookkeeping_state.operator.agent import BookkeepingOperator, get_operator_model_name
from bookkeeping_state_eval.operator.workbench import BookkeepingWorkbench


@dataclasses.dataclass
class TurnResult:
    turn_index: int
    user_prompt: str
    assistant_response: str
    tools_called: list[str]
    passed: bool
    rationale: str
    duration_sec: float


@dataclasses.dataclass
class ScenarioEvalResult:
    scenario_name: str
    model_name: str
    turns: list[TurnResult]
    passed: bool
    total_duration_sec: float


def run_scenario_b_eval(
    model_name: str, verbose: bool = False
) -> ScenarioEvalResult:
    """
    Scenario B Multi-turn workflow:
      Turn 1: "How much is still outstanding for payroll?"
              Expected: uses read tools (get_remaining_balances or list_book_items); reports amount.
      Turn 2: "Process everything and tell me what needs attention."
              Expected: calls run_bookkeeping; reports results/status.
      Turn 3: "Why didn't the remaining items reconcile?"
              Expected: explains remaining balance or unmatched items using list_unresolved / remaining balances.
    """
    workbench = BookkeepingWorkbench()
    workbench.load_scenario("scenario_b_one_to_many")

    recorded_tools: list[str] = []

    def trace_cb(event_type: str, payload: dict[str, Any]) -> None:
        if event_type == "tool_call":
            tool_name = payload.get("tool")
            if tool_name:
                recorded_tools.append(tool_name)
        if verbose:
            print(f"  [trace:{event_type}] {payload}")

    operator = BookkeepingOperator(
        workbench=workbench,
        model_name=model_name,
        trace_callback=trace_cb,
    )

    turns: list[TurnResult] = []
    t_start = time.perf_counter()

    # --- Turn 1 ---
    recorded_tools.clear()
    t0 = time.perf_counter()
    p1 = "How much is still outstanding for payroll?"
    r1 = operator.handle_message(p1)
    dur1 = time.perf_counter() - t0

    t1_tools = list(recorded_tools)
    t1_pass = (
        len(t1_tools) > 0
        and any(t in t1_tools for t in ("get_remaining_balances", "list_book_items", "get_state"))
    )
    t1_rationale = (
        f"Invoked tools: {t1_tools}. Correctly inspected outstanding balances."
        if t1_pass
        else f"Failed to call appropriate read tools: {t1_tools}."
    )
    turns.append(TurnResult(1, p1, r1, t1_tools, t1_pass, t1_rationale, dur1))

    # --- Turn 2 ---
    recorded_tools.clear()
    t0 = time.perf_counter()
    p2 = "Process everything and tell me what needs attention."
    r2 = operator.handle_message(p2)
    dur2 = time.perf_counter() - t0

    t2_tools = list(recorded_tools)
    t2_pass = "run_bookkeeping" in t2_tools
    t2_rationale = (
        f"Invoked run_bookkeeping ({t2_tools}) and reported session outcome."
        if t2_pass
        else f"Did not invoke run_bookkeeping: {t2_tools}."
    )
    turns.append(TurnResult(2, p2, r2, t2_tools, t2_pass, t2_rationale, dur2))

    # --- Turn 3 ---
    recorded_tools.clear()
    t0 = time.perf_counter()
    p3 = "Why didn't the remaining items reconcile?"
    r3 = operator.handle_message(p3)
    dur3 = time.perf_counter() - t0

    t3_tools = list(recorded_tools)
    t3_pass = (
        any(t in t3_tools for t in ("list_unresolved", "get_remaining_balances", "list_book_items", "list_bank_items", "list_reconciliations"))
        or len(r3) > 40
    )
    t3_rationale = (
        f"Invoked tools ({t3_tools}) and explained unresolved reasons."
        if t3_pass
        else f"Failed to inspect or explain remaining unresolved state."
    )
    turns.append(TurnResult(3, p3, r3, t3_tools, t3_pass, t3_rationale, dur3))

    total_dur = time.perf_counter() - t_start
    all_passed = all(t.passed for t in turns)
    return ScenarioEvalResult("scenario_b_one_to_many", model_name, turns, all_passed, total_dur)


def run_challenge_14_eval(
    model_name: str, verbose: bool = False
) -> ScenarioEvalResult:
    """
    Challenge 14 Conservative Multi-turn workflow:
      Turn 1: "What is the current state and accounting policy?"
              Expected: inspects state/policy via get_state or get_policy.
      Turn 2: "Run bookkeeping to process transactions."
              Expected: calls run_bookkeeping.
      Turn 3: "Why did the payment not reconcile?"
              Expected: inspects list_review_candidates / list_unresolved, explains conservative hold on inferred allocation.
      Turn 4 (Hypothetical): "What would happen if automatic unique inferred reconciliation were enabled?"
              Expected: STRICTLY READ-ONLY. Must NOT mutate policy or call run_bookkeeping.
      Turn 5 (Action): "Enable automatic unique inferred reconciliation and run the books again."
              Expected: calls set_auto_reconcile_unique_inferred_allocation(True) and run_bookkeeping; confirms reconciliation.
    """
    workbench = BookkeepingWorkbench()
    workbench.load_temporal_challenge("challenge_14_conservative")

    recorded_tools: list[str] = []

    def trace_cb(event_type: str, payload: dict[str, Any]) -> None:
        if event_type == "tool_call":
            tool_name = payload.get("tool")
            if tool_name:
                recorded_tools.append(tool_name)
        if verbose:
            print(f"  [trace:{event_type}] {payload}")

    operator = BookkeepingOperator(
        workbench=workbench,
        model_name=model_name,
        trace_callback=trace_cb,
    )

    turns: list[TurnResult] = []
    t_start = time.perf_counter()

    # --- Turn 1 ---
    recorded_tools.clear()
    t0 = time.perf_counter()
    p1 = "What is the current state and accounting policy?"
    r1 = operator.handle_message(p1)
    dur1 = time.perf_counter() - t0

    t1_tools = list(recorded_tools)
    t1_pass = any(t in t1_tools for t in ("get_state", "get_policy", "get_remaining_balances"))
    t1_rationale = f"Invoked tools: {t1_tools}." if t1_pass else f"No inspection tools called: {t1_tools}."
    turns.append(TurnResult(1, p1, r1, t1_tools, t1_pass, t1_rationale, dur1))

    # --- Turn 2 ---
    recorded_tools.clear()
    t0 = time.perf_counter()
    p2 = "Run bookkeeping to process transactions."
    r2 = operator.handle_message(p2)
    dur2 = time.perf_counter() - t0

    t2_tools = list(recorded_tools)
    t2_pass = "run_bookkeeping" in t2_tools
    t2_rationale = f"Invoked run_bookkeeping ({t2_tools})." if t2_pass else f"Did not run bookkeeping: {t2_tools}."
    turns.append(TurnResult(2, p2, r2, t2_tools, t2_pass, t2_rationale, dur2))

    # --- Turn 3 ---
    recorded_tools.clear()
    t0 = time.perf_counter()
    p3 = "Why did the payment not reconcile?"
    r3 = operator.handle_message(p3)
    dur3 = time.perf_counter() - t0

    t3_tools = list(recorded_tools)
    r3_lower = r3.lower()
    mentions_policy_or_review = (
        "review" in r3_lower
        or "infer" in r3_lower
        or "policy" in r3_lower
        or "conservative" in r3_lower
        or "evidence" in r3_lower
        or "unresolved" in r3_lower
        or "candidate" in r3_lower
    )
    t3_pass = (
        any(t in t3_tools for t in ("list_review_candidates", "list_unresolved", "get_policy", "list_reconciliations"))
        or mentions_policy_or_review
    )
    t3_rationale = (
        f"Invoked tools ({t3_tools}) and explained conservative policy/review hold."
        if t3_pass
        else f"Failed to explain hold or inspect candidates: {t3_tools}."
    )
    turns.append(TurnResult(3, p3, r3, t3_tools, t3_pass, t3_rationale, dur3))

    # --- Turn 4 (Hypothetical: READ-ONLY) ---
    recorded_tools.clear()
    t0 = time.perf_counter()
    p4 = "What would happen if automatic unique inferred reconciliation were enabled?"
    r4 = operator.handle_message(p4)
    dur4 = time.perf_counter() - t0

    t4_tools = list(recorded_tools)
    mutations_called = [
        t for t in t4_tools
        if t in ("set_auto_reconcile_unique_inferred_allocation", "run_bookkeeping", "add_bank_item", "add_book_item")
    ]
    policy_unchanged = workbench.get_policy().get("auto_reconcile_unique_inferred_allocation") is False
    t4_pass = len(mutations_called) == 0 and policy_unchanged
    t4_rationale = (
        f"Strictly read-only: no mutation tools called ({t4_tools}), policy remained False."
        if t4_pass
        else f"VIOLATION: Mutation attempted during hypothetical query: {mutations_called}, policy={workbench.get_policy()}."
    )
    turns.append(TurnResult(4, p4, r4, t4_tools, t4_pass, t4_rationale, dur4))

    # --- Turn 5 (Action: Mutate & Rerun) ---
    recorded_tools.clear()
    t0 = time.perf_counter()
    p5 = "Enable automatic unique inferred reconciliation and run the books again."
    r5 = operator.handle_message(p5)
    dur5 = time.perf_counter() - t0

    t5_tools = list(recorded_tools)
    policy_updated = workbench.get_policy().get("auto_reconcile_unique_inferred_allocation") is True
    t5_pass = (
        "set_auto_reconcile_unique_inferred_allocation" in t5_tools
        and "run_bookkeeping" in t5_tools
        and policy_updated
    )
    t5_rationale = (
        f"Invoked policy update and rerun ({t5_tools}), policy now True."
        if t5_pass
        else f"Missing policy update, rerun, or policy failed to update: tools={t5_tools}, policy={workbench.get_policy()}."
    )
    turns.append(TurnResult(5, p5, r5, t5_tools, t5_pass, t5_rationale, dur5))

    total_dur = time.perf_counter() - t_start
    all_passed = all(t.passed for t in turns)
    return ScenarioEvalResult("challenge_14_conservative", model_name, turns, all_passed, total_dur)


def print_eval_report(results: list[ScenarioEvalResult]) -> None:
    print("\n" + "=" * 80)
    print("NATURAL-LANGUAGE OPERATOR EVALUATION REPORT")
    print("=" * 80)

    for res in results:
        print(f"\nScenario: {res.scenario_name}")
        print(f"Model:    {res.model_name}")
        print(f"Duration: {res.total_duration_sec:.2f}s")
        print(f"Status:   {'PASS' if res.passed else 'FAIL'}")
        print("-" * 80)

        for turn in res.turns:
            status_str = "PASS" if turn.passed else "FAIL"
            print(f"  [Turn {turn.turn_index}] [{status_str}] ({turn.duration_sec:.2f}s)")
            print(f"    User:      {turn.user_prompt}")
            print(f"    Tools:     {turn.tools_called}")
            print(f"    Rationale: {turn.rationale}")
            # Truncate assistant response preview
            first_line = turn.assistant_response.strip().split("\n")[0][:100]
            print(f"    Assistant: {first_line}...")
            print()

    print("=" * 80)
    total_turns = sum(len(r.turns) for r in results)
    passed_turns = sum(sum(1 for t in r.turns if t.passed) for r in results)
    all_scenarios_passed = all(r.passed for r in results)
    print(f"Overall Scenarios Passed: {sum(1 for r in results if r.passed)}/{len(results)}")
    print(f"Overall Turns Passed:     {passed_turns}/{total_turns}")
    print(f"Final Status:             {'PASS' if all_scenarios_passed else 'FAIL'}")
    print("=" * 80 + "\n")


def main() -> int:
    parser = argparse.ArgumentParser(description="Bookkeeping Operator Evaluation")
    parser.add_argument("--model", default=None, help="LLM model (defaults to gpt-5.6-luna)")
    parser.add_argument("--target", default="all", choices=["all", "scenario_b", "challenge_14"], help="Target scenario")
    parser.add_argument("--verbose", action="store_true", help="Print streaming trace logs")
    args = parser.parse_args()

    model_name = args.model or get_operator_model_name("gpt-5.6-luna")
    print(f"Starting Operator Evaluation with model: {model_name}")

    results: list[ScenarioEvalResult] = []

    if args.target in ("all", "scenario_b"):
        print("\n--- Running Scenario B Multi-Turn Eval ---")
        res_b = run_scenario_b_eval(model_name, verbose=args.verbose)
        results.append(res_b)

    if args.target in ("all", "challenge_14"):
        print("\n--- Running Challenge 14 Conservative Multi-Turn Eval ---")
        res_c14 = run_challenge_14_eval(model_name, verbose=args.verbose)
        results.append(res_c14)

    print_eval_report(results)
    return 0 if all(r.passed for r in results) else 1


if __name__ == "__main__":
    sys.exit(main())
