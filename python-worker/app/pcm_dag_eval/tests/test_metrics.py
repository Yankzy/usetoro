from app.pcm_dag_eval.domain.models import LightweightExecutionReport, NodeExecutionStep, ProbabilityCandidate
from app.pcm_dag_eval.evaluation.metrics import calculate_metrics
from app.pcm_dag_eval.evaluation.ground_truth import GroundTruthDAGPath
import datetime

def test_calculate_metrics_perfect_match():
    truth = GroundTruthDAGPath(
        expected_nodes=["direction_router", "bank_transaction_classifier_inflow", "terminal"],
        expected_edges=["INFLOW", "EDGE_X"],
        expected_final_state="READY_FOR_SYNC"
    )
    
    report = LightweightExecutionReport(
        transaction_id="tx_1",
        cash_direction="INFLOW",
        raw_description="test",
        raw_amount="100.0",
        currency="MAD",
        duration=1000,
        edge_key="EDGE_X",
        selected_edge="EDGE_X",
        candidates=[
            ProbabilityCandidate(candidate="EDGE_X", probability=0.99, reasoning="Good"),
            ProbabilityCandidate(candidate="EDGE_Y", probability=0.01, reasoning="Bad")
        ],
        execution_steps=[
            NodeExecutionStep(dag_node_id="direction_router", kind="router", timestamp=datetime.datetime.now(datetime.UTC)),
            NodeExecutionStep(dag_node_id="bank_transaction_classifier_inflow", kind="classifier", timestamp=datetime.datetime.now(datetime.UTC)),
            NodeExecutionStep(dag_node_id="terminal", kind="terminal", timestamp=datetime.datetime.now(datetime.UTC)),
        ],
        final_state="READY_FOR_SYNC",
        duration_ms=50
    )
    
    metrics = calculate_metrics(report, truth)
    assert metrics["EdgeClassificationAccuracy"] == 1.0
    assert metrics["Top2Recall"] == 1.0
    assert metrics["GuardrailComplianceRate"] == 1.0
    assert metrics["NodePathExactMatch"] == 1.0
    assert metrics["TerminalStateAccuracy"] == 1.0
    assert metrics["FailureTaxonomy"] == "SUCCESS"

def test_calculate_metrics_wrong_edge():
    truth = GroundTruthDAGPath(
        expected_nodes=["direction_router", "bank_transaction_classifier_inflow", "terminal"],
        expected_edges=["INFLOW", "EDGE_X"],
        expected_final_state="READY_FOR_SYNC"
    )
    
    report = LightweightExecutionReport(
        transaction_id="tx_2",
        cash_direction="INFLOW",
        raw_description="test",
        raw_amount="100.0",
        currency="MAD",
        duration=1000,
        edge_key="EDGE_X",
        selected_edge="EDGE_Y",
        candidates=[
            ProbabilityCandidate(candidate="EDGE_Y", probability=0.99, reasoning="Good"),
            ProbabilityCandidate(candidate="EDGE_X", probability=0.01, reasoning="Bad")
        ],
        execution_steps=[
            NodeExecutionStep(dag_node_id="direction_router", kind="router", timestamp=datetime.datetime.now(datetime.UTC)),
            NodeExecutionStep(dag_node_id="bank_transaction_classifier_inflow", kind="classifier", timestamp=datetime.datetime.now(datetime.UTC)),
            NodeExecutionStep(dag_node_id="terminal", kind="terminal", timestamp=datetime.datetime.now(datetime.UTC)),
        ],
        final_state="READY_FOR_SYNC",
        duration_ms=50
    )
    
    metrics = calculate_metrics(report, truth)
    assert metrics["EdgeClassificationAccuracy"] == 0.0
    assert metrics["Top2Recall"] == 1.0
    assert metrics["FailureTaxonomy"] == "MODEL_WRONG_EDGE"
