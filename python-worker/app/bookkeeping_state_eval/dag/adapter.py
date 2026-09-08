from __future__ import annotations

from datetime import datetime, timezone
from typing import Sequence

from bookkeeping_state_eval.dag.models import (
    DagBatchPlan,
    DagClassificationItem,
)
from bookkeeping_state_eval.dag.view import DagView
from bookkeeping_state_eval.domain.commands import (
    CommandSource,
    CreateClassificationCommand,
)
from bookkeeping_state_eval.transitions.batch import (
    TransitionBatch,
    TransitionBatchError,
)


def dag_plan_to_transition_batch(
    plan: DagBatchPlan,
    *,
    view: DagView | None = None,
    issued_at: datetime | None = None,
) -> TransitionBatch:
    """
    Convert a DagBatchPlan into an atomic TransitionBatch.
    """
    return dag_outputs_to_transition_batch(
        items=plan.items,
        session_id=plan.session_id,
        expected_state_revision=plan.expected_state_revision,
        batch_id=f"batch:dag:{plan.plan_id}",
        dag_run_id=plan.dag_run_id or plan.plan_id,
        issued_at=issued_at,
        view=view,
    )


def dag_outputs_to_transition_batch(
    *,
    items: Sequence[DagClassificationItem],
    session_id: str,
    expected_state_revision: int,
    batch_id: str,
    dag_run_id: str | None = None,
    issued_at: datetime | None = None,
    view: DagView | None = None,
) -> TransitionBatch:
    """
    Transform raw DAG classification outputs into an atomic TransitionBatch.

    Validates that:
    - items is not empty
    - no two sibling outputs target the same book_item_id without explicit supersession
    - account_code is non-empty and non-blank
    - if view is provided:
        - expected_state_revision matches view.state_revision (rejecting stale result revisions)
        - every output references a book_item_id present in the DagView (rejecting unknown items)
    """
    if not items:
        raise TransitionBatchError("Cannot create TransitionBatch from empty DAG items")

    if view is not None:
        if expected_state_revision != view.state_revision:
            raise TransitionBatchError(
                f"Stale DAG batch revision {expected_state_revision}, expected {view.state_revision}"
            )
        valid_book_ids = {v.book_item_id for v in view.items}
    else:
        valid_book_ids = None

    now = issued_at or datetime.now(timezone.utc)
    commands: list[CreateClassificationCommand] = []
    seen_books: set[str] = set()

    for idx, item in enumerate(items):
        if not item.account_code or not item.account_code.strip():
            raise TransitionBatchError(
                f"DAG output contains invalid or empty account_code for {item.book_item_id!r}"
            )

        if valid_book_ids is not None and item.book_item_id not in valid_book_ids:
            raise TransitionBatchError(
                f"DAG output references unknown book_item_id {item.book_item_id!r} not in DagView"
            )

        if item.book_item_id in seen_books:
            raise TransitionBatchError(
                f"DAG outputs contain multiple classifications for book_item {item.book_item_id!r}"
            )
        seen_books.add(item.book_item_id)

        cls_id = item.classification_id or f"cls:{item.book_item_id}:{batch_id}:{idx}"
        cmd_id = f"cmd:cls:{item.book_item_id}:{batch_id}:{idx}"

        commands.append(
            CreateClassificationCommand(
                command_id=cmd_id,
                expected_state_revision=expected_state_revision,
                source=CommandSource.ASE_DAG,
                session_id=session_id,
                issued_at=now,
                classification_id=cls_id,
                book_item_id=item.book_item_id,
                account_code=item.account_code,
                classification_source=item.classification_source,
                confidence=item.confidence,
                rationale=item.rationale,
                evidence_refs=item.evidence_refs,
                supersedes_classification_id=item.supersedes_classification_id,
                dag_run_id=dag_run_id,
                ase_node_id=item.ase_node_id,
            )
        )

    return TransitionBatch(
        batch_id=batch_id,
        session_id=session_id,
        expected_state_revision=expected_state_revision,
        commands=tuple(commands),
    )
