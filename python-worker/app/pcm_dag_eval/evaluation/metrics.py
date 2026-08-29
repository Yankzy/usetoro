from typing import Dict, Any, List
from app.pcm_dag_eval.domain.models import LightweightExecutionReport, GroundTruthDAGPath

def calculate_metrics(report: LightweightExecutionReport, truth: GroundTruthDAGPath) -> Dict[str, Any]:
    metrics = {
        "EdgeClassificationAccuracy": 0.0,
        "Top2Recall": 0.0,
        "ConfidenceCalibration": 0.0,
        "GuardrailComplianceRate": 0.0,
        "NodePathExactMatch": 0.0,
        "StepTransitionValidity": 0.0,
        "TerminalStateAccuracy": 0.0,
        "HoldPrecisionRecall": {"precision": 0.0, "recall": 0.0, "f1": 0.0},
        "FailureTaxonomy": "SUCCESS"
    }
    
    # 1. Model Performance
    is_correct_edge = report.selected_edge == report.edge_key
    metrics["EdgeClassificationAccuracy"] = 1.0 if is_correct_edge else 0.0
    
    candidates = report.candidates or []
    if candidates:
        top_2_values = [c.value for c in candidates[:2]]
        metrics["Top2Recall"] = 1.0 if report.edge_key in top_2_values else 0.0
        
        # Guardrail Compliance
        conf_sum = sum(c.confidence for c in candidates)
        has_two_cands = len(candidates) >= 2
        # auto-advance guardrail: conf >= 0.98 if not hold
        top_cand = candidates[0]
        valid_auto_advance = True
        if not top_cand.value.startswith("HOLD_") and top_cand.confidence < 0.98:
            valid_auto_advance = False
            
        prob_sum_valid = abs(conf_sum - 1.0) < 0.01
        has_reasoning = all(c.reasoning for c in candidates)
        
        if has_two_cands and prob_sum_valid and has_reasoning and valid_auto_advance:
            metrics["GuardrailComplianceRate"] = 1.0
            
        metrics["ConfidenceCalibration"] = top_cand.confidence
        
    # 2. DAG Execution & Topology
    executed_nodes = [step.dag_node_id for step in report.execution_steps]
    # In the DAG, the dag_node_id in execution_steps corresponds to the DAGNodeID
    
    if executed_nodes == truth.expected_nodes:
        metrics["NodePathExactMatch"] = 1.0
        
    if report.final_state == truth.expected_final_state:
        metrics["TerminalStateAccuracy"] = 1.0
        
    # Simplified Transition Validity
    metrics["StepTransitionValidity"] = 1.0 if executed_nodes else 0.0
    
    # Hold metrics
    is_expected_hold = truth.expected_final_state.startswith("HOLD_")
    is_actual_hold = report.final_state.startswith("HOLD_")
    
    if is_expected_hold and is_actual_hold:
        metrics["HoldPrecisionRecall"]["precision"] = 1.0
        metrics["HoldPrecisionRecall"]["recall"] = 1.0
        metrics["HoldPrecisionRecall"]["f1"] = 1.0
    elif is_expected_hold and not is_actual_hold:
        metrics["HoldPrecisionRecall"]["precision"] = 0.0
        metrics["HoldPrecisionRecall"]["recall"] = 0.0
    elif not is_expected_hold and is_actual_hold:
        metrics["HoldPrecisionRecall"]["precision"] = 0.0
        metrics["HoldPrecisionRecall"]["recall"] = 0.0
    else:
        metrics["HoldPrecisionRecall"]["precision"] = 1.0
        metrics["HoldPrecisionRecall"]["recall"] = 1.0
        metrics["HoldPrecisionRecall"]["f1"] = 1.0

    # 3. Failure Taxonomy
    if not is_correct_edge:
        metrics["FailureTaxonomy"] = "MODEL_WRONG_EDGE"
    elif candidates and not valid_auto_advance:
        metrics["FailureTaxonomy"] = "GUARDRAIL_CONFIDENCE_BELOW_THRESHOLD"
    elif candidates and not prob_sum_valid:
        metrics["FailureTaxonomy"] = "GUARDRAIL_PROBABILITY_SUM_INVALID"
    elif not metrics["NodePathExactMatch"]:
        metrics["FailureTaxonomy"] = "DAG_NODE_PATH_DIVERGENCE"
    elif is_actual_hold and not is_expected_hold:
        metrics["FailureTaxonomy"] = "INCORRECT_HOLD_SIGNAL"
        
    return metrics
