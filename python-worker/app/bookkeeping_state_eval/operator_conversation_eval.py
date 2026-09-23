#!/usr/bin/env python3
"""
Standalone CLI entry point for the Bookkeeping Operator Conversational Evaluation Harness.

Usage:
  python operator_conversation_eval.py --mode operator-only --case C01 --verbose
  python operator_conversation_eval.py --mode operator-only --all
  python operator_conversation_eval.py --mode both --all --repeat 2
"""

from __future__ import annotations

import argparse
import sys
from pathlib import Path

# Add python-worker/app to sys.path
_eval_dir = Path(__file__).resolve().parent
_app_dir = _eval_dir.parent
for p in (_app_dir, _eval_dir):
    p_str = str(p)
    if p_str not in sys.path:
        sys.path.insert(0, p_str)

from bookkeeping_state_eval.operator_conversation_eval import (
    CANONICAL_CASES,
    ConversationRunner,
    get_canonical_cases,
    get_case,
    print_suite_report,
)


def main() -> int:
    parser = argparse.ArgumentParser(
        description="Bookkeeping Operator Conversational Evaluation Harness"
    )
    parser.add_argument(
        "--mode",
        choices=["operator-only", "full", "both"],
        default="operator-only",
        help="Evaluation mode: operator-only (deterministic semantics) vs full (real LLM semantics) vs both",
    )
    parser.add_argument(
        "--case",
        type=str,
        default=None,
        help="Specific case ID or comma-separated IDs (e.g. C01 or C01,C02,C03)",
    )
    parser.add_argument(
        "--all",
        action="store_true",
        help="Run all 15 canonical conversation cases",
    )
    parser.add_argument(
        "--repeat",
        type=int,
        default=1,
        help="Number of repetitions per case in pristine reconstructed worlds (default: 1)",
    )
    parser.add_argument(
        "--operator-model",
        type=str,
        default="gpt-5.6-luna",
        help="Model for outer Bookkeeping Operator (default: gpt-5.6-luna)",
    )
    parser.add_argument(
        "--semantic-model",
        type=str,
        default="gpt-5.6-luna",
        help="Model for inner semantic routing and reconciliation (default: gpt-5.6-luna)",
    )
    parser.add_argument(
        "--verbose",
        action="store_true",
        help="Output detailed per-turn envelopes, traces, and verdicts",
    )

    args = parser.parse_args()

    if args.case:
        case_ids = [c.strip().upper() for c in args.case.split(",") if c.strip()]
        selected_cases = [get_case(cid) for cid in case_ids]
    else:
        selected_cases = get_canonical_cases()

    modes_to_run = ["operator-only", "full"] if args.mode == "both" else [args.mode]

    runner = ConversationRunner(
        operator_model=args.operator_model,
        semantic_model=args.semantic_model,
        verbose=args.verbose,
    )

    all_suites_passed = True

    for mode in modes_to_run:
        suite_res = runner.run_suite(
            cases=selected_cases,
            mode=mode,
            repeat=args.repeat,
        )
        print_suite_report(suite_res, verbose=args.verbose)
        if suite_res.passed_cases < suite_res.total_cases:
            all_suites_passed = False

    return 0 if all_suites_passed else 1


if __name__ == "__main__":
    sys.exit(main())
