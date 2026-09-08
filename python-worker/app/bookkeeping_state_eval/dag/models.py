from __future__ import annotations

from dataclasses import dataclass, field
from datetime import datetime, timezone
from enum import StrEnum
from typing import Sequence

from bookkeeping_state_eval.domain.classifications import ClassificationSource
from bookkeeping_state_eval.domain.commands import (
    CommandSource,
    CreateClassificationCommand,
)
from bookkeeping_state_eval.transitions.batch import TransitionBatch


class DagExecutionStatus(StrEnum):
    SUCCESS = "SUCCESS"
    PARTIAL_SUCCESS = "PARTIAL_SUCCESS"
    FAILED = "FAILED"
    SKIPPED = "SKIPPED"


@dataclass(frozen=True, slots=True)
class DagClassificationItem:
    """
    Classification proposed by a Go ASE DAG execution or deterministic simulator.
    """

    book_item_id: str
    account_code: str
    confidence: float | None = None
    classification_source: ClassificationSource = ClassificationSource.ASE_DAG
    rationale: str | None = None
    evidence_refs: tuple[str, ...] = ()
    supersedes_classification_id: str | None = None
    classification_id: str | None = None
    ase_node_id: str | None = None


@dataclass(frozen=True, slots=True)
class DagHoldItem:
    """
    Detached runtime record representing an unresolved BookItem under evaluation.

    Emitted when ASE has insufficient evidence to produce a deterministic PCGE
    classification. This never produces a CreateClassificationCommand or mutates
    durable accounting state.
    """

    book_item_id: str
    reason: str = "HOLD_INSUFFICIENT_EVIDENCE"
    rationale: str = ""
    ase_node_id: str = ""


@dataclass(frozen=True, slots=True)
class DagBatchPlan:
    """
    Plan aggregating classification outputs from a Go ASE DAG run.
    """

    plan_id: str
    session_id: str
    expected_state_revision: int
    items: tuple[DagClassificationItem, ...] = ()
    hold_items: tuple[DagHoldItem, ...] = ()
    dag_id: str = "pcm_bank_cash_accounting_dag"
    dag_run_id: str | None = None

    def to_batch(self, issued_at: datetime | None = None) -> TransitionBatch:
        """
        Convert this DAG plan into an atomic TransitionBatch.
        """
        now = issued_at or datetime.now(timezone.utc)
        commands: list[CreateClassificationCommand] = []

        for idx, item in enumerate(self.items):
            cls_id = item.classification_id or f"cls:{item.book_item_id}:{self.plan_id}:{idx}"
            cmd_id = f"cmd:cls:{item.book_item_id}:{self.plan_id}:{idx}"
            commands.append(
                CreateClassificationCommand(
                    command_id=cmd_id,
                    expected_state_revision=self.expected_state_revision,
                    source=CommandSource.ASE_DAG,
                    session_id=self.session_id,
                    issued_at=now,
                    classification_id=cls_id,
                    book_item_id=item.book_item_id,
                    account_code=item.account_code,
                    classification_source=item.classification_source,
                    confidence=item.confidence,
                    rationale=item.rationale,
                    evidence_refs=item.evidence_refs,
                    supersedes_classification_id=item.supersedes_classification_id,
                    dag_run_id=self.dag_run_id or self.plan_id,
                    ase_node_id=item.ase_node_id,
                )
            )

        return TransitionBatch(
            batch_id=f"batch:dag:{self.plan_id}",
            session_id=self.session_id,
            expected_state_revision=self.expected_state_revision,
            commands=tuple(commands),
        )
