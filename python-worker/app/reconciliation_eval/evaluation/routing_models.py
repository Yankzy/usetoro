from typing import Dict, List, Set

from pydantic import BaseModel, Field


class RoutingGroundTruth(BaseModel):
    """Ground truth for multi-account routing assignments."""

    # Mapping of BookItem ID to the expected Bank Account ID
    expected_assignments: Dict[str, str]
    # Set of BookItem IDs that should NOT be routable to any account
    expected_unroutable: Set[str] = Field(default_factory=set)


class RoutingMetrics(BaseModel):
    """Evaluation metrics for the routing phase."""

    routing_accuracy: float  # correct_assignments / total_routable
    partition_leakage_rate: float  # incorrect_assignments / total_routable
    unroutable_precision: float  # correct_unroutable / total_expected_unroutable
