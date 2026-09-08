from __future__ import annotations

from typing import Protocol, runtime_checkable

from bookkeeping_state_eval.dag.models import DagBatchPlan
from bookkeeping_state_eval.dag.view import DagView


@runtime_checkable
class AseClassifier(Protocol):
    """
    Protocol defining the contract for ASE classification of a bounded DagView.

    Consuming components (such as BookkeepingSession) depend strictly on this protocol,
    not on concrete simulator or transport implementations.
    """

    def classify_view(
        self,
        view: DagView,
        *,
        only_unclassified: bool = True,
    ) -> DagBatchPlan:
        """
        Classify eligible BookItems present in view.

        Returns a DagBatchPlan containing zero or more classification items and
        zero or more hold items.
        """
        ...
