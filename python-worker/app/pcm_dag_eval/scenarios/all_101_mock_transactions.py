import json
from pathlib import Path
from typing import List, Dict

from app.pcm_dag_eval.domain.models import MockTransactionScenario

# The repository root is four levels above this module:
# pcm_dag_eval/scenarios -> pcm_dag_eval -> app -> python-worker -> root
REPO_ROOT = Path(__file__).resolve().parents[4]
FIXTURES_PATH = REPO_ROOT / "go" / "internal" / "erp" / "ase" / "fixtures" / "pcm_mock_transactions.json"

def load_all_scenarios() -> List[MockTransactionScenario]:
    if not FIXTURES_PATH.exists():
        raise FileNotFoundError(f"Mock transactions fixture not found at {FIXTURES_PATH}")
    
    with open(FIXTURES_PATH, "r") as f:
        data = json.load(f)
    
    scenarios = []
    for i, item in enumerate(data):
        if "transaction_id" not in item:
            item["transaction_id"] = f"mock_tx_{i}"
        scenarios.append(MockTransactionScenario(**item))
    return scenarios

def get_inflows_suite() -> List[MockTransactionScenario]:
    return [s for s in load_all_scenarios() if s.cash_direction == "INFLOW"]

def get_outflows_suite() -> List[MockTransactionScenario]:
    return [s for s in load_all_scenarios() if s.cash_direction == "OUTFLOW"]

def get_hold_ambiguity_suite() -> List[MockTransactionScenario]:
    return [s for s in load_all_scenarios() if "hold" in s.expected_child_node]

def get_tax_compliance_suite() -> List[MockTransactionScenario]:
    # Anything requiring statutory tax evaluation usually goes through specific nodes
    # For now, let's just use all scenarios, or filter by specific edge keys if needed.
    # The instructions say: "Inflows and Outflows requiring statutory tax evaluation"
    # Actually, we can define it based on expected paths later if needed, or just return all for now.
    return load_all_scenarios()
