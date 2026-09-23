from __future__ import annotations

import json
import os
from pathlib import Path
import sys
from typing import Any

# Ensure ledger directory and repo root are in sys.path
ledger_dir = Path(__file__).resolve().parents[2]
repo_root = Path(__file__).resolve().parents[3]
if str(ledger_dir) not in sys.path:
    sys.path.insert(0, str(ledger_dir))
if str(repo_root) not in sys.path:
    sys.path.insert(0, str(repo_root))

os.environ.setdefault("DJANGO_SETTINGS_MODULE", "config.settings")
import django
django.setup()

from bookkeeping_state_eval.dag.parity_company_setup import (
    CANDIDATE_SOURCE_DJANGO_LEDGER_DEFAULT_COA,
    PARITY_SEED_ACCOUNTS,
)
from bookkeeping_state_eval.dag.residual_bank_corpus import RESIDUAL_BANK_CORPUS_SPECS
from bookkeeping_state_eval.dag.residual_bank_models import (
    ResidualBankCaseResult,
    ResidualBankObservation,
    compute_residual_bank_metrics,
    evaluate_residual_bank_case,
)
from bookkeeping_state_eval.dag.residual_bank_runner import (
    write_residual_bank_results_json,
    write_residual_bank_summary_md,
)
from bookkeeping_state_eval.dag.residual_bank_truth_manifest import (
    build_residual_bank_truth_manifest,
    write_residual_bank_truth_manifest_json,
)


def recompute_clean_eval_reports(
    clean_results_json_path: Path | str,
    clean_summary_md_path: Path | str,
    truth_manifest_json_path: Path | str,
) -> dict[str, Any]:
    """
    Deterministically recomputes clean residual-bank evaluation metrics and reports
    directly from stored model execution outcomes against the authoritative truth manifest.

    Guarantees:
    1. Zero model reruns needed (uses stored observations from clean run).
    2. Metrics recomputed strictly by compute_residual_bank_metrics(), never hardcoded.
    3. Truth manifest updated with audited account codes and rationales.
    4. Historical split-brain artifacts remain completely untouched.
    """
    # 1. Regenerate truth manifest from updated RESIDUAL_BANK_CORPUS_SPECS
    write_residual_bank_truth_manifest_json(truth_manifest_json_path)

    # 2. Load stored clean run results
    clean_json = Path(clean_results_json_path)
    with open(clean_json, "r", encoding="utf-8") as f:
        data = json.load(f)

    execution_mode = data.get("execution_mode", "REAL_MODEL_DOMAIN_TOOLS")
    model_name = data.get("model_name", "gpt-5.4-mini")
    database_target = data.get("database_target", "127.0.0.1:5435/toro")
    entity_slug = data.get("entity_slug", "eval-residual-bank")
    entity_uuid = data.get("entity_uuid", "79519ce6-d528-4312-83a5-5f35ade32ad5")
    replicate_stability = data.get("replicate_stability", {})

    raw_coa_codes = {acc["code"] for acc in PARITY_SEED_ACCOUNTS}
    specs_by_id = {s.case_id: s for s in RESIDUAL_BANK_CORPUS_SPECS}

    # 3. Re-evaluate each case using stored observations and updated truth specs
    new_results: list[ResidualBankCaseResult] = []
    for r in data["results"]:
        case_id = r["case_id"]
        spec = specs_by_id[case_id]
        obs = ResidualBankObservation.from_dict(r["obs"])

        res = evaluate_residual_bank_case(
            case_id=spec.case_id,
            bank_item_id=r["bank_item_id"],
            category=spec.category,
            direction=spec.direction,
            expected_terminal_type=spec.expected_terminal_type,
            expected_account_code=spec.expected_account_code,
            expected_macro_family=spec.expected_macro_family,
            expected_hold_reason=spec.expected_hold_reason,
            risk_class=spec.risk_class,
            rationale=spec.rationale,
            obs=obs,
            raw_coa_codes=raw_coa_codes,
        )
        new_results.append(res)

    # 4. Compute metrics strictly by code
    new_metrics = compute_residual_bank_metrics(new_results)

    # 5. Write updated clean results and summary
    cand_source = new_results[0].obs.candidate_source or CANDIDATE_SOURCE_DJANGO_LEDGER_DEFAULT_COA
    write_residual_bank_results_json(
        clean_json,
        new_results,
        new_metrics,
        replicate_stability,
        execution_mode=execution_mode,
        model_name=model_name,
        database_target=database_target,
        entity_slug=entity_slug,
        entity_uuid=entity_uuid,
        account_count=len(raw_coa_codes),
    )

    write_residual_bank_summary_md(
        clean_summary_md_path,
        new_metrics,
        new_results,
        replicate_stability,
        execution_mode=execution_mode,
        model_name=model_name,
        candidate_source=cand_source,
        database_target=database_target,
        entity_slug=entity_slug,
        entity_uuid=entity_uuid,
        account_count=len(raw_coa_codes),
    )

    return new_metrics


if __name__ == "__main__":
    reports_dir = ledger_dir / "bookkeeping_state_eval" / "reports"
    metrics = recompute_clean_eval_reports(
        clean_results_json_path=reports_dir / "residual_bank_real_model_clean_results.json",
        clean_summary_md_path=reports_dir / "residual_bank_real_model_clean_summary.md",
        truth_manifest_json_path=reports_dir / "residual_bank_truth_manifest.json",
    )
    print("Clean evaluation reports successfully recomputed by code:")
    print(f"Total items: {metrics['total_items']}")
    print(f"Candidate Recall: {metrics['candidate_recall']}")
    print(f"Conditional Exact Accuracy: {metrics['conditional_exact_accuracy']}")
    print(f"Overall Exact Accuracy: {metrics['overall_exact_code_accuracy']}")
    print(f"Macro Family Accuracy: {metrics['macro_family_accuracy']}")
    print(f"Terminal Accuracy: {metrics['terminal_accuracy']}")
    print(f"Failure counts: {metrics['failure_counts']}")
