import asyncio
import json
import logging
import hashlib
import uuid
from datetime import UTC, datetime
from pathlib import Path
from typing import Any, Dict, List

from domain.bank import BankItem
from domain.books import BookItem
from domain.patch import ProposedState
from evaluation.ground_truth import GroundTruth
from evaluation.metrics import calculate_metrics
from validation.deterministic import validate_proposal
from pydantic import BaseModel
from agent.reconciliation_agent import run_reconciliation_agent

logging.basicConfig(level=logging.INFO, format='%(asctime)s - %(levelname)s - %(message)s')
logger = logging.getLogger(__name__)

def compute_hash(data: Any) -> str:
    """Computes a SHA-256 hash of a JSON-serializable object for reproducibility (Section 60)."""
    if isinstance(data, BaseModel):
        data_str = data.model_dump_json()
    else:
        data_str = json.dumps(data, sort_keys=True, default=str)
    return hashlib.sha256(data_str.encode('utf-8')).hexdigest()

async def run_single_eval(
    scenario_id: str,
    currency: str,
    bank_items: List[BankItem],
    book_items: List[BookItem],
    evidence: str,
    truth: GroundTruth,
    use_optimizer: bool,
    model_name: str = "gpt-4o"
) -> dict:
    """Executes a single evaluation run (either Config A or Config B)."""
    
    run_id = str(uuid.uuid4())
    timestamp = datetime.now(UTC).isoformat()
    
    logger.info(f"--- Starting Run {run_id} | Scenario: {scenario_id} | Optimizer: {use_optimizer} ---")
    
    # 1. Hashes for tracking
    scenario_hash = compute_hash({"bank": [b.model_dump() for b in bank_items], "book": [j.model_dump() for j in book_items]})
    truth_hash = compute_hash(truth.model_dump())
    
    # 2. Execute the LLM Agent
    # Config A sends no tool definition; Config B permits optimizer calls.
    max_calls = 5 if use_optimizer else 0
    
    start_time = datetime.now(UTC)
    try:
        proposal, plausible_candidates, llm_proposed_hypotheses = await run_reconciliation_agent(
            problem_id=scenario_id,
            currency=currency,
            bank_items=bank_items,
            book_items=book_items,
            scenario_evidence=evidence,
            model_name=model_name,
            max_optimizer_calls=max_calls
        )
        agent_error = None
    except Exception as e:
        logger.error(f"Agent execution failed: {e}")
        proposal = ProposedState() # Empty fallback
        plausible_candidates = ""
        llm_proposed_hypotheses = []
        agent_error = str(e)
        
    latency = (datetime.now(UTC) - start_time).total_seconds()

    # 3. Deterministic Validation (Section 42)
    is_valid, validation_errors = validate_proposal(bank_items, book_items, proposal)
    
    # 4. Ground Truth Metrics Evaluation (Sections 51-54)
    metrics = calculate_metrics(proposal, truth)
    
    # Add validation failure to failure taxonomy (Section 59)
    if not is_valid:
        metrics["failure_taxonomy"] = "DETERMINISTIC_VALIDATION_FAILURE"
    elif metrics["FRR"] > 0:
        metrics["failure_taxonomy"] = "LLM_FALSE_HYPOTHESIS"
    elif metrics["HoldAccuracy"] < 1.0:
        metrics["failure_taxonomy"] = "INCORRECT_HOLD"
    else:
        metrics["failure_taxonomy"] = "SUCCESS"

    # 5. Compile Artifact (Section 60)
    artifact = {
        "run_id": run_id,
        "timestamp": timestamp,
        "scenario_id": scenario_id,
        "configuration": "B_OPTIMIZER" if use_optimizer else "A_LLM_ONLY",
        "scenario_hash": scenario_hash,
        "truth_hash": truth_hash,
        "llm_provider": "openai",
        "model_name": model_name,
        "latency_seconds": latency,
        "agent_error": agent_error,
        "pre_calculated_candidates": plausible_candidates,
        "llm_proposed_hypotheses": llm_proposed_hypotheses,
        "final_patch": proposal.model_dump(),
        "deterministic_validation": {
            "is_valid": is_valid,
            "errors": validation_errors
        },
        "eval_metrics": metrics
    }
    
    return artifact

async def run_scenario_evaluation(
    *,
    scenario_id: str,
    currency: str,
    bank_items: List[BankItem],
    book_items: List[BookItem],
    evidence: str,
    truth: GroundTruth,
    runs: int = 2,
    model_name: str = "gpt-5.6-sol",
    runs_dir: Path = Path("logs/runs"),
) -> List[dict]:
    """Evaluate one scenario with and without the optimizer and save its report."""
    if runs < 1:
        raise ValueError("runs must be at least 1")

    runs_dir.mkdir(parents=True, exist_ok=True)
    all_artifacts = []
    try:
        for i in range(runs):
            logger.info(f"\n{'=' * 40}\nIteration {i + 1}/{runs}\n{'=' * 40}")

            artifact_a = await run_single_eval(
                scenario_id, currency, bank_items, book_items, evidence, truth,
                use_optimizer=False, model_name=model_name,
            )
            all_artifacts.append(artifact_a)

            artifact_b = await run_single_eval(
                scenario_id, currency, bank_items, book_items, evidence, truth,
                use_optimizer=True, model_name=model_name,
            )
            all_artifacts.append(artifact_b)
    except Exception as e:
        logger.error(f"Evaluation loop failed: {e}")
        raise

    report_path = runs_dir / f"eval_report_{scenario_id}.json"

    with open(report_path, "w") as f:
        json.dump(all_artifacts, f, indent=2)

    logger.info(f"Saved complete evaluation report to {report_path}")
    print("\n--- QUICK SUMMARY ---")
    for art in all_artifacts:
        print(
            f"Run: {art['run_id'][:8]} | Config: {art['configuration']} | "
            f"Valid: {art['deterministic_validation']['is_valid']} | "
            f"Exact Match: {art['eval_metrics']['GLOBAL_STATE_EXACT']} | "
            f"Recall: {art['eval_metrics']['Recall']:.2f} | "
            f"FRR: {art['eval_metrics']['FRR']:.2f}"
        )
    return all_artifacts

if __name__ == "__main__":
    raise SystemExit(
        "Run a scenario-specific module instead, e.g. "
        "python -m scenarios.atlas_station.runner"
    )
