"""
Standalone Evaluation Runner for LLM Semantic Reasoning vs Deterministic Baselines.

Evaluates 6 Catalog Scenarios and 5 Adversarial Challenges against frozen ExpectedTruth.

Usage:
    ./.venv/bin/python python-worker/app/bookkeeping_state_eval/llm_eval.py --mode deterministic
    ./.venv/bin/python python-worker/app/bookkeeping_state_eval/llm_eval.py --mode llm --model gpt-5.6-luna
    ./.venv/bin/python python-worker/app/bookkeeping_state_eval/llm_eval.py --mode both
    ./.venv/bin/python python-worker/app/bookkeeping_state_eval/llm_eval.py --target scenario_b_one_to_many --mode llm
"""

from __future__ import annotations

import argparse
import dataclasses
from datetime import datetime, timezone
from pathlib import Path
import sys
import time

# Ensure repository packages are importable
_current_dir = Path(__file__).resolve().parent
_app_dir = _current_dir.parent
for p in (_app_dir, _current_dir):
    if str(p) not in sys.path:
        sys.path.insert(0, str(p))

from bookkeeping_state_eval.llm.client import get_model_name
from bookkeeping_state_eval.llm.factory import (
    create_reconciliation_semantic_provider,
    create_routing_semantic_provider,
)
from bookkeeping_state_eval.reconciliation.service import ReconciliationService
from bookkeeping_state_eval.scenarios.catalog import get_scenario
from bookkeeping_state_eval.scenarios.challenges import get_challenge
from bookkeeping_state_eval.scenarios.models import ScenarioDefinition, ScenarioResult
from bookkeeping_state_eval.scenarios.runner import ScenarioRunner

# Canonical evaluation suite targets
EVAL_SCENARIOS = (
    "scenario_a_exact_receipt",
    "scenario_b_one_to_many",
    "scenario_c_many_to_one",
    "scenario_d_partial_customer_receipt",
    "scenario_f_routing_ambiguity",
    "scenario_q_optimizer_competition",
)

EVAL_CHALLENGES = (
    "challenge_01_dense_collision",
    "challenge_04_duplicate_reference_collision",
    "challenge_05_partial_truth_vs_exact_decoy",
    "challenge_06_aggregate_truth_vs_fragment_decoys",
    "challenge_07_dirty_month_end",
)


@dataclasses.dataclass(frozen=True)
class EvalRunRecord:
    target_id: str
    target_type: str  # "SCENARIO" or "CHALLENGE"
    mode: str  # "DETERMINISTIC" or "LLM"
    model_name: str
    is_pass: bool
    reconciliations_expected: int  # Expected total active reconciliations in closing state
    reconciliations_actual: int  # Actual total active reconciliations in closing state
    elapsed_seconds: float
    mismatches: tuple[str, ...] = ()
    error_message: str | None = None


def _count_expected_reconciliations(scenario: ScenarioDefinition) -> int:
    exp = scenario.expected_truth
    if exp.expected_reconciliations is not None:
        return len(exp.expected_reconciliations)
    if exp.expected_pairwise_allocations is not None:
        return len(exp.expected_pairwise_allocations)
    if exp.expected_artifact_counts and "reconciliations" in exp.expected_artifact_counts:
        return exp.expected_artifact_counts["reconciliations"]
    return 0


def run_single_eval(
    scenario: ScenarioDefinition,
    target_id: str,
    target_type: str,
    mode: str,
    model_name: str,
) -> EvalRunRecord:
    """Execute one target under the specified mode and return structured metrics."""
    start_time = time.perf_counter()
    exp_recons = _count_expected_reconciliations(scenario)
    try:
        if mode.lower() == "llm":
            routing_prov = create_routing_semantic_provider("llm", model_name=model_name)
            recon_prov = create_reconciliation_semantic_provider("llm", model_name=model_name)
            recon_svc = ReconciliationService(scorer=recon_prov)
            used_model = model_name
        else:
            routing_prov = create_routing_semantic_provider("deterministic")
            recon_prov = create_reconciliation_semantic_provider("deterministic")
            recon_svc = ReconciliationService(scorer=recon_prov)
            used_model = "heuristic"

        runner = ScenarioRunner()
        result: ScenarioResult = runner.run(
            scenario,
            routing_semantic_provider=routing_prov,
            reconciliation_service=recon_svc,
        )

        elapsed = time.perf_counter() - start_time
        recon_stage = result.session_result.reconciliation_stage_result
        # Total active reconciliations in closing state = initial pre-existing + newly created session reconciliations
        initial_recons = len(scenario.reconciliations)
        session_created = recon_stage.command_count if recon_stage else 0
        act_recons = initial_recons + session_created

        return EvalRunRecord(
            target_id=target_id,
            target_type=target_type,
            mode=mode.upper(),
            model_name=used_model,
            is_pass=result.is_pass,
            reconciliations_expected=exp_recons,
            reconciliations_actual=act_recons,
            elapsed_seconds=elapsed,
            mismatches=tuple(result.mismatches),
            error_message=result.failure_reason,
        )

    except Exception as exc:
        elapsed = time.perf_counter() - start_time
        return EvalRunRecord(
            target_id=target_id,
            target_type=target_type,
            mode=mode.upper(),
            model_name=model_name if mode.lower() == "llm" else "heuristic",
            is_pass=False,
            reconciliations_expected=exp_recons,
            reconciliations_actual=0,
            elapsed_seconds=elapsed,
            mismatches=(),
            error_message=f"Execution exception: {exc}",
        )


def print_summary_table(records: list[EvalRunRecord]) -> None:
    """Format and print an evaluation results table."""
    print("\n" + "=" * 90)
    print(f"{'TARGET':<42} | {'MODE':<6} | {'STATUS':<6} | {'EXP/ACT':<8} | {'TIME':<7} | {'MODEL'}")
    print("-" * 90)

    pass_count = 0
    fail_count = 0

    for r in records:
        status_str = "PASS" if r.is_pass else "FAIL"
        if r.is_pass:
            pass_count += 1
        else:
            fail_count += 1

        exp_act = f"{r.reconciliations_expected}/{r.reconciliations_actual}"
        time_str = f"{r.elapsed_seconds:.2f}s"
        print(f"{r.target_id:<42} | {r.mode:<6} | {status_str:<6} | {exp_act:<8} | {time_str:<7} | {r.model_name}")

    print("=" * 90)
    print(f"Total: {len(records)} | Passed: {pass_count} | Failed: {fail_count}")
    print("=" * 90 + "\n")

    # Print failures in detail
    failures = [r for r in records if not r.is_pass]
    if failures:
        print("DETAILED FAILURE REPORTS:")
        print("-" * 50)
        for f in failures:
            print(f"[{f.mode}] {f.target_id} ({f.target_type}):")
            if f.error_message:
                print(f"  Error: {f.error_message}")
            if f.mismatches:
                print("  Mismatches:")
                for m in f.mismatches:
                    print(f"    - {m}")
            print()


def main() -> None:
    parser = argparse.ArgumentParser(description="Bookkeeping Evaluation Runner (LLM vs Deterministic)")
    parser.add_argument(
        "--mode",
        choices=["deterministic", "llm", "both"],
        default="deterministic",
        help="Evaluation mode (deterministic, llm, or both)",
    )
    parser.add_argument(
        "--model",
        type=str,
        default="gpt-5.6-luna",
        help="LLM model name to use when mode=llm or mode=both (default: gpt-5.6-luna)",
    )
    parser.add_argument(
        "--target",
        type=str,
        default=None,
        help="Run a specific scenario or challenge by ID",
    )
    parser.add_argument(
        "--scenarios-only",
        action="store_true",
        help="Run only catalog scenarios",
    )
    parser.add_argument(
        "--challenges-only",
        action="store_true",
        help="Run only challenge worlds",
    )

    args = parser.parse_args()
    model_name = get_model_name(args.model)

    modes_to_run: list[str] = []
    if args.mode in ("deterministic", "both"):
        modes_to_run.append("deterministic")
    if args.mode in ("llm", "both"):
        modes_to_run.append("llm")

    targets: list[tuple[str, str, ScenarioDefinition]] = []

    if args.target:
        # Resolve target from catalog or challenges
        target_found = False
        try:
            sc = get_scenario(args.target)
            targets.append((args.target, "SCENARIO", sc))
            target_found = True
        except KeyError:
            pass

        if not target_found:
            try:
                ch = get_challenge(args.target)
                targets.append((args.target, "CHALLENGE", ch.scenario))
                target_found = True
            except KeyError:
                pass

        if not target_found:
            print(f"Error: Target {args.target!r} not found in catalog or challenges.")
            sys.exit(1)
    else:
        if not args.challenges_only:
            for sc_id in EVAL_SCENARIOS:
                targets.append((sc_id, "SCENARIO", get_scenario(sc_id)))
        if not args.scenarios_only:
            for ch_id in EVAL_CHALLENGES:
                ch = get_challenge(ch_id)
                targets.append((ch_id, "CHALLENGE", ch.scenario))

    print("==================================================")
    print("   BookkeepingState LLM Evaluation Benchmark      ")
    print("==================================================")
    print(f"Modes: {', '.join(m.upper() for m in modes_to_run)}")
    if "llm" in modes_to_run:
        print(f"LLM Model: {model_name}")
    print(f"Targets ({len(targets)}):")
    for t_id, t_type, _ in targets:
        print(f"  - [{t_type}] {t_id}")
    print()

    all_records: list[EvalRunRecord] = []

    for mode in modes_to_run:
        print(f"\n--- Starting {mode.upper()} Evaluation Pass ---")
        for t_id, t_type, sc_def in targets:
            print(f"Running {t_id} ({mode.upper()})...", end="", flush=True)
            record = run_single_eval(
                sc_def,
                target_id=t_id,
                target_type=t_type,
                mode=mode,
                model_name=model_name,
            )
            res_str = "PASS" if record.is_pass else "FAIL"
            print(f" [{res_str}] ({record.elapsed_seconds:.2f}s)")
            all_records.append(record)

    print_summary_table(all_records)

    # If any deterministic tests failed, exit with 1
    det_failures = [r for r in all_records if r.mode == "DETERMINISTIC" and not r.is_pass]
    if det_failures:
        sys.exit(1)


if __name__ == "__main__":
    main()
