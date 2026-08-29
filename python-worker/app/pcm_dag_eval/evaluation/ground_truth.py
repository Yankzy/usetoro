import yaml
from pathlib import Path
from typing import List, Dict, Any, Tuple

from app.pcm_dag_eval.domain.models import MockTransactionScenario, GroundTruthDAGPath

REPO_ROOT = Path(__file__).resolve().parents[4]
DAG_PATH = REPO_ROOT / "go" / "internal" / "erp" / "ase" / "dags" / "pcm_bank_cash_accounting_dag.yml"

def _load_dag() -> Dict[str, Any]:
    if not DAG_PATH.exists():
        raise FileNotFoundError(f"DAG definition not found at {DAG_PATH}")
    with open(DAG_PATH, "r") as f:
        return yaml.safe_load(f)["dag"]

_DAG = _load_dag()

def resolve_ground_truth_path(scenario: MockTransactionScenario) -> GroundTruthDAGPath:
    """
    Computes the expected sequence of nodes from the direction router to the terminal node
    based on the scenario's properties and the static DAG definition.
    """
    current_node_id = _DAG["entry_node"]
    path_nodes = [current_node_id]
    path_edges = []
    
    # 1. Direction Router
    direction = scenario.cash_direction
    node_config = _DAG["nodes"][current_node_id]
    
    # The direction router expects INFLOW/OUTFLOW
    next_node_id = node_config["children"].get(direction, node_config.get("default_child"))
    path_edges.append(direction)
    current_node_id = next_node_id
    path_nodes.append(current_node_id)
    
    # 2. Classifier
    node_config = _DAG["nodes"][current_node_id]
    if scenario.edge_key.startswith("HOLD_"):
        # If it's a hold scenario, the classifier routes directly to the hold terminal
        next_node_id = node_config["children"].get(scenario.edge_key, node_config.get("default_child"))
        path_edges.append(scenario.edge_key)
        current_node_id = next_node_id
        path_nodes.append(current_node_id)
        
        # Terminal node reached
        return GroundTruthDAGPath(
            expected_nodes=path_nodes,
            expected_edges=path_edges,
            expected_final_state=scenario.edge_key
        )
    
    # Happy path classifier
    next_node_id = node_config["children"].get(scenario.edge_key, node_config.get("default_child"))
    path_edges.append(scenario.edge_key)
    current_node_id = next_node_id
    path_nodes.append(current_node_id)
    
    # 3. Traverse the rest of the DAG until terminal node
    while True:
        node_config = _DAG["nodes"][current_node_id]
        if node_config["kind"] == "terminal":
            break
            
        target_value = ""
        # Match the mock logic in ase_lightweight_bridge_worker.go
        if current_node_id == "compliance_router":
            target_value = "NO_SPECIAL_COMPLIANCE_REQUIRED"
            if scenario.edge_key == "FOREIGN_VENDOR_SERVICES_PAYMENT":
                target_value = "FOREIGN_PAYMENT_TAX_EVALUATION_REQUIRED"
            elif scenario.edge_key == "DOMESTIC_RENTAL_LEASE_PAYMENT":
                target_value = "RENT_RAS_EVALUATION_REQUIRED"
            elif scenario.edge_key == "BANK_FEE_COMMISSION":
                target_value = "BANKING_VAT_EVALUATION_REQUIRED"
            elif scenario.edge_key == "SUPPLIER_INVOICE_PAYMENT_4411":
                target_value = "INPUT_VAT_EVALUATION_REQUIRED"
        else:
            for edge_key in node_config["children"].keys():
                if not edge_key.startswith("HOLD_"):
                    target_value = edge_key
                    break
            if not target_value:
                target_value = list(node_config["children"].keys())[0]
                
        next_node_id = node_config["children"].get(target_value, node_config.get("default_child"))
        path_edges.append(target_value)
        current_node_id = next_node_id
        path_nodes.append(current_node_id)

    # For a terminal node, the final state is usually the terminal node's name or hold_state_signal
    terminal_node_config = _DAG["nodes"][current_node_id]
    final_state = terminal_node_config.get("hold_state_signal", "READY_FOR_SYNC")
    if current_node_id == "bank_cash_complete":
        final_state = terminal_node_config.get("execution_parameters", {}).get("close_status", "POSTED_AND_RECONCILED")
    
    return GroundTruthDAGPath(
        expected_nodes=path_nodes,
        expected_edges=path_edges,
        expected_final_state=final_state
    )
