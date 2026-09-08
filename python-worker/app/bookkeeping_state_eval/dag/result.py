from __future__ import annotations

from dataclasses import dataclass

from bookkeeping_state_eval.dag.models import (
    DagBatchPlan,
    DagExecutionStatus,
)
from bookkeeping_state_eval.dag.view import DagView
from bookkeeping_state_eval.transitions.batch import (
    BatchTransitionResult,
    TransitionBatch,
)


@dataclass(frozen=True, slots=True)
class DagRunResult:
    """
    Result of a Go ASE DAG run or simulation through to batch transition.
    """

    dag_id: str
    run_id: str
    status: DagExecutionStatus
    view: DagView
    plan: DagBatchPlan
    batch: TransitionBatch | None = None
    transition_result: BatchTransitionResult | None = None
    error_message: str | None = None

    @property
    def is_applied(self) -> bool:
        return self.transition_result.is_applied if self.transition_result else False

    @property
    def is_noop(self) -> bool:
        return self.transition_result.is_noop if self.transition_result else False

    @property
    def is_rejected(self) -> bool:
        return self.transition_result.is_rejected if self.transition_result else False
