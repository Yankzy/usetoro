"""
Execution engine for the Bookkeeping Operator Conversational Evaluation Harness.
Supports Mode A (Operator Isolation) and Mode B (Full Toro).
"""

from __future__ import annotations

import json
import time
from typing import Any, Callable
from openai import AsyncOpenAI

from bookkeeping_state_eval.operator.agent import BookkeepingOperator
from bookkeeping_state_eval.operator.tools import execute_operator_tool

from .models import (
    CaseResult,
    ConversationCase,
    FailureCategory,
    SuiteResult,
    ToolCallRecord,
    TurnEnvelope,
)
from .validators import MUTATION_TOOLS, validate_turn_envelope
from .worlds import capture_state_snapshot


class ConversationRunner:
    """
    Executes conversational evaluation cases against pristine BookkeepingWorkbench
    environments and evaluates turn-by-turn correctness.
    """

    def __init__(
        self,
        operator_model: str = "gpt-5.6-luna",
        semantic_model: str = "gpt-5.6-luna",
        client: AsyncOpenAI | None = None,
        verbose: bool = False,
        trace_logger: Callable[[str], None] | None = None,
    ) -> None:
        self.operator_model = operator_model
        self.semantic_model = semantic_model
        self.client = client
        self.verbose = verbose
        self.trace_logger = trace_logger or print

    def run_case(self, case: ConversationCase, mode: str = "operator-only") -> CaseResult:
        """
        Execute an individual conversation case in a pristine world.
        """
        t0_case = time.perf_counter()

        # Construct a completely fresh, unpolluted world
        workbench = case.world_factory(mode)

        recorded_tools: list[dict[str, Any]] = []

        def trace_cb(event_type: str, payload: dict[str, Any]) -> None:
            if event_type == "tool_call":
                recorded_tools.append({
                    "tool": payload.get("tool"),
                    "arguments": payload.get("arguments"),
                    "call_id": payload.get("call_id"),
                    "status": "pending",
                })
            elif event_type == "tool_result":
                call_id = payload.get("call_id")
                status = payload.get("status", "ok")
                for r in reversed(recorded_tools):
                    if r.get("call_id") == call_id:
                        r["status"] = status
                        break
            if self.verbose:
                self.trace_logger(f"  [trace:{event_type}] {payload}")

        operator = BookkeepingOperator(
            workbench=workbench,
            model_name=self.operator_model,
            client=self.client,
            trace_callback=trace_cb,
        )

        turns: list[TurnEnvelope] = []
        prev_envelope: TurnEnvelope | None = None

        for turn_idx, turn_def in enumerate(case.turns):
            recorded_tools.clear()
            before_snapshot = capture_state_snapshot(workbench)

            t0_turn = time.perf_counter()
            response = operator.handle_message(turn_def.user_message)
            turn_duration = time.perf_counter() - t0_turn

            after_snapshot = capture_state_snapshot(workbench)

            # Build ToolCallRecords
            tool_records: list[ToolCallRecord] = []
            for item in recorded_tools:
                tool_name = item.get("tool") or ""
                raw_args = item.get("arguments") or "{}"
                if isinstance(raw_args, dict):
                    args_dict = raw_args
                    raw_str = json.dumps(raw_args)
                else:
                    raw_str = str(raw_args)
                    try:
                        args_dict = json.loads(raw_str)
                    except Exception:
                        args_dict = {}

                tool_records.append(
                    ToolCallRecord(
                        tool=tool_name,
                        arguments=args_dict,
                        raw_arguments=raw_str,
                        status=item.get("status", "ok"),
                        call_id=item.get("call_id", ""),
                        is_mutation=(tool_name in MUTATION_TOOLS),
                        round_idx=0,
                    )
                )

            envelope = TurnEnvelope(
                case_id=case.id,
                turn_index=turn_idx + 1,
                user_message=turn_def.user_message,
                before=before_snapshot,
                tool_activity=tool_records,
                after=after_snapshot,
                response=response,
                verdict=None,  # Populated below
                duration_seconds=turn_duration,
            )

            envelope.verdict = validate_turn_envelope(
                envelope,
                turn_def,
                previous_envelope=prev_envelope,
            )
            turns.append(envelope)
            prev_envelope = envelope

            # Execute inter-turn hook if defined (e.g. C08 stale state injection)
            if turn_def.inter_turn_hook is not None:
                turn_def.inter_turn_hook(workbench)

        case_duration = time.perf_counter() - t0_case
        case_passed = all(t.verdict.passed for t in turns)
        all_failures: list[FailureCategory] = []
        for t in turns:
            for f in t.verdict.failures:
                if f.category not in all_failures:
                    all_failures.append(f.category)

        return CaseResult(
            case_id=case.id,
            description=case.description,
            mode=mode,
            turns=turns,
            passed=case_passed,
            failure_categories=all_failures,
            total_duration_seconds=case_duration,
        )

    def run_suite(
        self,
        cases: list[ConversationCase],
        mode: str = "operator-only",
        repeat: int = 1,
    ) -> SuiteResult:
        """
        Execute a collection of cases, repeating each case N times in pristine worlds.
        """
        t0_suite = time.perf_counter()
        results: list[CaseResult] = []

        unauthorized_count = 0
        state_mismatch_count = 0
        stale_state_count = 0
        clarification_count = 0
        loop_exhaustion_count = 0

        for rep in range(repeat):
            for case in cases:
                if mode not in case.supported_modes:
                    continue
                res = self.run_case(case, mode=mode)
                results.append(res)

                for cat in res.failure_categories:
                    if cat == FailureCategory.UNAUTHORIZED_MUTATION:
                        unauthorized_count += 1
                    elif cat in (FailureCategory.STATE_TRANSITION_MISMATCH, FailureCategory.DURABLE_TRUTH_MISMATCH):
                        state_mismatch_count += 1
                    elif cat == FailureCategory.STALE_STATE_ANSWER:
                        stale_state_count += 1
                    elif cat == FailureCategory.MISSING_CLARIFICATION:
                        clarification_count += 1
                    elif cat == FailureCategory.TOOL_LOOP_EXHAUSTED:
                        loop_exhaustion_count += 1

        total_cases = len(results)
        passed_cases = sum(1 for r in results if r.passed)
        duration = time.perf_counter() - t0_suite

        return SuiteResult(
            mode=mode,
            operator_model=self.operator_model,
            semantic_model=self.semantic_model,
            case_results=results,
            total_cases=total_cases,
            passed_cases=passed_cases,
            hard_invariant_passed=passed_cases,
            unauthorized_mutations_count=unauthorized_count,
            state_mismatch_count=state_mismatch_count,
            stale_state_failures_count=stale_state_count,
            clarification_failures_count=clarification_count,
            loop_exhaustion_count=loop_exhaustion_count,
            duration_seconds=duration,
        )
