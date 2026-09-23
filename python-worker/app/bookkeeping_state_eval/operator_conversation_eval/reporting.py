"""
Reporting and diagnostic output formatting for the Bookkeeping Operator
Conversational Evaluation Harness.
"""

from __future__ import annotations

import sys
from typing import TextIO

from .models import CaseResult, SuiteResult, TurnEnvelope


def format_verbose_turn(envelope: TurnEnvelope) -> str:
    """Format an auditable, compact representation of a single turn."""
    lines: list[str] = [
        f"\n  --- CASE {envelope.case_id} TURN {envelope.turn_index} ---",
        f"  User:     {envelope.user_message}",
        f"  Before:   P{envelope.before.persistence_revision} | "
        f"Bank: {envelope.before.bank_items_count} | "
        f"Book: {envelope.before.book_items_count} | "
        f"Unresolved: {len(envelope.before.unresolved_bank_ids)}B/{len(envelope.before.unresolved_book_ids)}J | "
        f"Recons: {envelope.before.reconciliations_count} | "
        f"Policy UniqueInferred={envelope.before.policy.get('auto_reconcile_unique_inferred_allocation')}",
    ]

    tools_called = [t.tool for t in envelope.tool_activity]
    lines.append(f"  Tools:    {tools_called if tools_called else 'None'}")
    for t in envelope.tool_activity:
        status_flag = "" if t.status == "ok" else f" [{t.status.upper()}]"
        lines.append(f"    * {t.tool}({t.raw_arguments}){status_flag}")

    lines.extend([
        f"  After:    P{envelope.after.persistence_revision} | "
        f"Bank: {envelope.after.bank_items_count} | "
        f"Book: {envelope.after.book_items_count} | "
        f"Unresolved: {len(envelope.after.unresolved_bank_ids)}B/{len(envelope.after.unresolved_book_ids)}J | "
        f"Recons: {envelope.after.reconciliations_count} | "
        f"Policy UniqueInferred={envelope.after.policy.get('auto_reconcile_unique_inferred_allocation')}",
        f"  Response: {envelope.response[:200] + ('...' if len(envelope.response) > 200 else '')}",
        f"  Verdict:  overall={'PASS' if envelope.verdict.passed else 'FAIL'} | "
        f"action={'PASS' if envelope.verdict.action_legality else 'FAIL'} | "
        f"state={'PASS' if envelope.verdict.state_transition else 'FAIL'} | "
        f"grounding={'PASS' if envelope.verdict.grounding else 'FAIL'}",
    ])

    if envelope.verdict.failures:
        lines.append("  Failures:")
        for f in envelope.verdict.failures:
            lines.append(f"    ! [{f.category.value}] {f.message}")

    return "\n".join(lines)


def print_suite_report(
    suite_result: SuiteResult,
    out: TextIO = sys.stdout,
    verbose: bool = False,
) -> None:
    """Print complete diagnostic and summary report of conversational evaluation."""
    out.write("\n" + "=" * 60 + "\n")
    out.write("CONVERSATION GAUNTLET EVALUATION REPORT\n")
    out.write("=" * 60 + "\n")
    out.write(f"Mode:              {suite_result.mode}\n")
    out.write(f"Operator Model:    {suite_result.operator_model}\n")
    out.write(f"Semantic Model:    {suite_result.semantic_model}\n")
    out.write(f"Duration:          {suite_result.duration_seconds:.2f}s\n")
    out.write("-" * 60 + "\n\n")

    for case_res in suite_result.case_results:
        status_str = "PASS" if case_res.passed else "FAIL"
        out.write(f"{case_res.case_id:4s} {case_res.description[:40]:40s} {status_str}\n")
        if not case_res.passed:
            for cat in case_res.failure_categories:
                out.write(f"     ! {cat.value}\n")

        if verbose:
            for envelope in case_res.turns:
                out.write(format_verbose_turn(envelope) + "\n")

    out.write("\n" + "=" * 60 + "\n")
    out.write("SUMMARY DIAGNOSTICS\n")
    out.write("=" * 60 + "\n")
    out.write(f"Hard Invariants:            {suite_result.passed_cases} / {suite_result.total_cases}\n")
    out.write(f"Unauthorized Mutations:     {suite_result.unauthorized_mutations_count}\n")
    out.write(f"State Transition Mismatches:{suite_result.state_mismatch_count}\n")
    out.write(f"Stale-State Failures:       {suite_result.stale_state_failures_count}\n")
    out.write(f"Clarification Failures:     {suite_result.clarification_failures_count}\n")
    out.write(f"Tool-Loop Exhaustion:       {suite_result.loop_exhaustion_count}\n")
    out.write(f"Overall Status:             {'PASS' if suite_result.passed_cases == suite_result.total_cases else 'FAIL'}\n")
    out.write("=" * 60 + "\n\n")
