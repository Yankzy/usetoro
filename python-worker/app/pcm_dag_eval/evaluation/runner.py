import asyncio
import json
import logging
import uuid
from datetime import datetime, UTC
from pathlib import Path
from typing import List, Dict, Any

from nats.aio.client import Client as NATS
from pydantic import ValidationError

from app.pcm_dag_eval.domain.models import MockTransactionScenario, LightweightExecutionReport
from app.pcm_dag_eval.evaluation.ground_truth import resolve_ground_truth_path
from app.pcm_dag_eval.evaluation.metrics import calculate_metrics

logging.basicConfig(level=logging.INFO, format='%(asctime)s - %(levelname)s - %(message)s')
logger = logging.getLogger(__name__)

async def run_single_eval(
    nc: NATS,
    scenario: MockTransactionScenario,
    run_id: str,
    timestamp: str
) -> dict:
    
    # 1. Resolve Ground Truth
    truth = resolve_ground_truth_path(scenario)
    
    logger.info(f"--- Starting Run {run_id} | EdgeKey: {scenario.edge_key} ---")
    
    # 2. Build Request Envelope
    eval_did = f"did:toro:agent:eval_runner_{run_id}"
    req_body = {
        "config": {
            "dag_name": "pcm_bank_cash_accounting_dag",
            "domain_tool": "pcm_cash_accounting"
        },
        "input": {
            "mock_transaction": scenario.model_dump(),
            "timeout_seconds": 60
        }
    }
    
    envelope = {
        "id": str(uuid.uuid4()),
        "src": eval_did,
        "dst": "did:toro:worker:ase-lightweight-bridge",
        "perf": "REQUEST",
        "body": req_body
    }
    
    # 3. Setup Reply Subscription
    reply_subject = f"agents.{eval_did}.inbox"
    sub = await nc.subscribe(reply_subject)
    
    # 4. Send Request
    target_subject = "worker.inbox.ase_lightweight_bridge"
    await nc.publish(target_subject, json.dumps(envelope).encode())
    
    start_time = datetime.now(UTC)
    
    # 5. Wait for Response
    try:
        msg = await sub.next_msg(timeout=65.0)
        resp_env = json.loads(msg.data.decode())
        
        if resp_env.get("perf", "").lower() == "inform":
            report_data = resp_env.get("body", {})
            try:
                report = LightweightExecutionReport(**report_data)
                agent_error = None
            except ValidationError as e:
                logger.error(f"Failed to validate response report: {e}")
                report = None
                agent_error = str(e)
        else:
            report = None
            agent_error = f"Unexpected performative: {resp_env.get('perf')} - {resp_env.get('body')}"
            
    except asyncio.TimeoutError:
        logger.error(f"Timeout waiting for Go ASE Bridge response for {scenario.edge_key}")
        report = None
        agent_error = "EXECUTION_TIMEOUT"
    finally:
        await sub.unsubscribe()
        
    latency = (datetime.now(UTC) - start_time).total_seconds()
    
    # 6. Calculate Metrics
    if report:
        metrics = calculate_metrics(report, truth)
    else:
        metrics = {
            "FailureTaxonomy": agent_error if agent_error == "EXECUTION_TIMEOUT" else "BRIDGE_ERROR"
        }
        
    # 7. Compile Artifact
    artifact = {
        "run_id": run_id,
        "timestamp": timestamp,
        "edge_key": scenario.edge_key,
        "latency_seconds": latency,
        "agent_error": agent_error,
        "ground_truth": truth.model_dump(),
        "report": report.model_dump() if report else None,
        "metrics": metrics
    }
    
    return artifact

async def run_scenario_evaluation(
    scenarios: List[MockTransactionScenario],
    scenario_name: str,
    runs_dir: Path = Path("logs/runs"),
) -> List[dict]:
    
    runs_dir.mkdir(parents=True, exist_ok=True)
    timestamp = datetime.now(UTC).isoformat()
    
    # Connect to NATS
    nc = NATS()
    import os
    nats_url = os.environ.get("NATS_URL", "nats://localhost:4222")
    try:
        await nc.connect(nats_url)
    except Exception as e:
        logger.error(f"Failed to connect to NATS at {nats_url}: {e}")
        raise
        
    all_artifacts = []
    
    try:
        total = len(scenarios)
        for i, scenario in enumerate(scenarios):
            logger.info(f"\n{'=' * 40}\nScenario {i + 1}/{total}: {scenario.edge_key}\n{'=' * 40}")
            run_id = str(uuid.uuid4())
            artifact = await run_single_eval(nc, scenario, run_id, timestamp)
            all_artifacts.append(artifact)
    finally:
        await nc.close()
        
    report_path = runs_dir / f"eval_report_{scenario_name}.json"
    with open(report_path, "w") as f:
        json.dump(all_artifacts, f, indent=2, default=str)
        
    logger.info(f"Saved complete evaluation report to {report_path}")
    print("\n--- QUICK SUMMARY ---")
    for art in all_artifacts:
        metrics = art.get('metrics', {})
        print(
            f"EdgeKey: {art['edge_key'][:30]:<30} | "
            f"Exact Path: {metrics.get('NodePathExactMatch', 0.0)} | "
            f"Accuracy: {metrics.get('EdgeClassificationAccuracy', 0.0)} | "
            f"FailureTaxonomy: {metrics.get('FailureTaxonomy')}"
        )
    return all_artifacts
