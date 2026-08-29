from typing import List, Optional, Any, Dict
from pydantic import BaseModel, Field
from datetime import datetime

class MockTransactionScenario(BaseModel):
    edge_key: str
    parent_node: str
    cash_direction: str
    expected_child_node: str
    raw_description: str
    raw_amount: str
    currency: str
    counterparty_name: str
    ice_number: str
    notes: str

class ProbabilityCandidate(BaseModel):
    value: str
    confidence: float
    reasoning: Optional[str] = None

class NodeExecutionStep(BaseModel):
    dag_node_id: str
    kind: str
    selected_edge: Optional[str] = None
    property_key: Optional[str] = None
    candidates: Optional[List[ProbabilityCandidate]] = None
    timestamp: datetime = Field(default_factory=datetime.utcnow)
    execution_duration_ms: Optional[float] = None

class GroundTruthDAGPath(BaseModel):
    expected_nodes: List[str]
    expected_edges: List[str]
    expected_final_state: str

class LightweightExecutionReport(BaseModel):
    transaction_id: str
    edge_key: str
    cash_direction: str
    raw_description: str
    raw_amount: str
    currency: str
    final_state: str
    hold_reason: Optional[str] = None
    candidates: Optional[List[ProbabilityCandidate]] = None
    selected_edge: Optional[str] = None
    target_child_node: Optional[str] = None
    expected_child: Optional[str] = None
    matched_expected: bool = False
    execution_steps: List[NodeExecutionStep] = []
    duration: float  # Note: Go's time.Duration unmarshals to integer nanoseconds in Go JSON, but we might receive it differently. Usually NATS Go JSON duration goes as ns.
    
class ExecutionTraceRequest(BaseModel):
    transaction: MockTransactionScenario
