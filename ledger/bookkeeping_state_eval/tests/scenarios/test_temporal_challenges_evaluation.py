"""
Evaluation script to execute all Temporal Challenges (08 through 14)
and print their execution timelines, outcomes, and failure diagnostics.
"""

from __future__ import annotations

import pytest

from bookkeeping_state_eval.scenarios.temporal import (
    TemporalChallengeRunner,
    list_temporal_challenges,
)


def test_evaluate_all_temporal_challenges() -> None:
    runner = TemporalChallengeRunner()
    challenges = list_temporal_challenges()

    print("\n" + "=" * 70)
    print("        TEMPORAL CHALLENGES EVALUATION (CHALLENGES 08 - 14)        ")
    print("=" * 70)

    results = []
    for c in challenges:
        print(f"\nRunning {c.challenge_id} [{c.name}] (difficulty: {c.difficulty})...")
        res = runner.run(c)
        status = "PASS" if res.is_pass else "FAIL"
        results.append((c.challenge_id, c.name, status, res))

        print(f"Status: {status} (Final P{res.final_persistence_revision})")
        print("Trajectory Timeline:")
        for s in res.step_summaries:
            cp_str = f" [{'PASS' if s.truth_match else 'FAIL'}]" if s.truth_match is not None else ""
            print(f"   Step {s.step_index:<2}  P{s.persistence_revision:<2}  {s.op_type:<26} {s.summary}{cp_str}")
        
        if not res.is_pass:
            print("Failure messages:")
            for msg in res.failure_messages:
                print(f"   - {msg}")

    print("\n" + "=" * 70)
    print("SUMMARY MATRIX:")
    print(f"{'Challenge ID':<45} {'Difficulty':<12} {'Status':<8} {'Final P'}")
    print("-" * 70)
    for cid, name, status, res in results:
        print(f"{cid:<45} {res.final_persistence_revision:<12} {status:<8} P{res.final_persistence_revision}")
    print("=" * 70 + "\n")
