from __future__ import annotations

from dataclasses import dataclass
from enum import StrEnum

from bookkeeping_state.state.validation import StateValidationReport
from bookkeeping_state.transitions.batch import BatchTransitionResult


class FailureStage(StrEnum):
    ROUTING = "ROUTING"
    DAG = "DAG"
    PAYMENT_APPLICATION = "PAYMENT_APPLICATION"
    REHYDRATION = "REHYDRATION"
    LOOP_INVARIANT = "LOOP_INVARIANT"
    RECONCILIATION = "RECONCILIATION"
    RESIDUAL_CATEGORIZATION = "RESIDUAL_CATEGORIZATION"
    RESIDUAL_POSTING = "RESIDUAL_POSTING"
    VALIDATION = "VALIDATION"
    COMMITTED_STATE_APPLICATION = "COMMITTED_STATE_APPLICATION"


class SessionStageStatus(StrEnum):
    APPLIED = "APPLIED"
    SKIPPED = "SKIPPED"
    HELD = "HELD"
    INCOMPLETE = "INCOMPLETE"
    FAILED = "FAILED"
    REJECTED = "REJECTED"
    NOT_RUN = "NOT_RUN"
    CATASTROPHIC = "CATASTROPHIC"


@dataclass(frozen=True, slots=True)
class DagHoldSummary:
    """
    Detached, immutable session-level record of a BookItem held by ASE.

    Provides observability for unresolved items without persisting runtime
    DAG models or contaminating durable double-entry bookkeeping state.
    """

    book_item_id: str
    reason: str = "HOLD_INSUFFICIENT_EVIDENCE"
    rationale: str = ""
    ase_node_id: str = ""


@dataclass(frozen=True, slots=True)
class DagProviderIssueSummary:
    """
    Detached, immutable session-level record of a provider issue (e.g. missing output).

    Provides observability for provider-level omissions and irregularities
    without contaminating durable double-entry bookkeeping state.
    """

    book_item_id: str
    code: str = "MISSING_PROVIDER_OUTPUT"
    message: str = ""


@dataclass(frozen=True, slots=True)
class SessionBatchSummary:
    """
    Completely detached, immutable execution summary of a transition batch.

    Explicitly excludes TransitionBatch, BookkeepingCommand objects,
    ReconciliationHypothesis, live BookkeepingState, queries, or views,
    ensuring runtime-only reasoning artifacts never escape the session boundary.
    """

    status: str
    batch_id: str | None
    command_count: int
    command_ids: tuple[str, ...]
    command_types: tuple[str, ...]
    previous_state_revision: int
    resulting_state_revision: int | None
    previous_persistence_revision: int
    resulting_persistence_revision: int
    rejection_code: str | None = None
    rejection_message: str | None = None
    artifact_ids: tuple[str, ...] = ()

    @classmethod
    def from_batch_result(
        cls,
        batch_res: BatchTransitionResult,
    ) -> SessionBatchSummary:
        cmd_ids = tuple(cmd.command_id for cmd in batch_res.batch.commands)
        cmd_types = tuple(
            cmd.command_type.value
            if hasattr(cmd.command_type, "value")
            else str(cmd.command_type)
            for cmd in batch_res.batch.commands
        )

        artifact_ids: list[str] = []
        if batch_res.delta is not None:
            artifact_ids.extend(a.id for a in batch_res.delta.bank_accounts)
            artifact_ids.extend(i.id for i in batch_res.delta.bank_items)
            artifact_ids.extend(b.id for b in batch_res.delta.book_items)
            artifact_ids.extend(d.id for d in batch_res.delta.documents)
            artifact_ids.extend(r.id for r in batch_res.delta.routing_decisions)
            artifact_ids.extend(c.id for c in batch_res.delta.classifications)
            artifact_ids.extend(rec.id for rec in batch_res.delta.reconciliations)
        elif batch_res.rejection is not None and batch_res.rejection.artifact_ids:
            artifact_ids.extend(batch_res.rejection.artifact_ids)

        rejection_code = (
            batch_res.rejection.code.value
            if (batch_res.rejection and hasattr(batch_res.rejection.code, "value"))
            else (str(batch_res.rejection.code) if batch_res.rejection else None)
        )
        rejection_message = (
            batch_res.rejection.message if batch_res.rejection else None
        )

        status_str = str(getattr(batch_res.status, "value", batch_res.status))

        return cls(
            status=status_str,
            batch_id=batch_res.batch.batch_id,
            command_count=len(batch_res.batch.commands),
            command_ids=cmd_ids,
            command_types=cmd_types,
            previous_state_revision=batch_res.previous_state_revision,
            resulting_state_revision=batch_res.resulting_state_revision,
            previous_persistence_revision=batch_res.previous_persistence_revision,
            resulting_persistence_revision=batch_res.resulting_persistence_revision,
            rejection_code=rejection_code,
            rejection_message=rejection_message,
            artifact_ids=tuple(artifact_ids),
        )

    @classmethod
    def from_catastrophic_error(
        cls,
        *,
        batch_id: str | None,
        command_count: int,
        command_ids: tuple[str, ...],
        command_types: tuple[str, ...],
        previous_state_revision: int,
        previous_persistence_revision: int,
        committed_persistence_revision: int,
        cause_message: str,
    ) -> SessionBatchSummary:
        return cls(
            status="CATASTROPHIC",
            batch_id=batch_id,
            command_count=command_count,
            command_ids=command_ids,
            command_types=command_types,
            previous_state_revision=previous_state_revision,
            resulting_state_revision=None,
            previous_persistence_revision=previous_persistence_revision,
            resulting_persistence_revision=committed_persistence_revision,
            rejection_code="COMMITTED_STATE_APPLICATION_ERROR",
            rejection_message=cause_message,
            artifact_ids=(),
        )


@dataclass(frozen=True, slots=True)
class SessionStageResult:
    """
    Explicit outcome of a single session stage.
    """

    status: SessionStageStatus
    base_state_revision: int | None
    resulting_state_revision: int | None
    batch_summary: SessionBatchSummary | None = None
    holds: tuple[DagHoldSummary, ...] = ()
    provider_issues: tuple[DagProviderIssueSummary, ...] = ()

    @classmethod
    def applied(
        cls,
        *,
        base_state_revision: int,
        resulting_state_revision: int,
        batch_summary: SessionBatchSummary,
        holds: tuple[DagHoldSummary, ...] = (),
        provider_issues: tuple[DagProviderIssueSummary, ...] = (),
    ) -> SessionStageResult:
        return cls(
            status=SessionStageStatus.APPLIED,
            base_state_revision=base_state_revision,
            resulting_state_revision=resulting_state_revision,
            batch_summary=batch_summary,
            holds=holds,
            provider_issues=provider_issues,
        )

    @classmethod
    def skipped(
        cls,
        *,
        state_revision: int,
    ) -> SessionStageResult:
        return cls(
            status=SessionStageStatus.SKIPPED,
            base_state_revision=state_revision,
            resulting_state_revision=state_revision,
            batch_summary=None,
            holds=(),
            provider_issues=(),
        )

    @classmethod
    def held(
        cls,
        *,
        state_revision: int,
        holds: tuple[DagHoldSummary, ...] = (),
    ) -> SessionStageResult:
        return cls(
            status=SessionStageStatus.HELD,
            base_state_revision=state_revision,
            resulting_state_revision=state_revision,
            batch_summary=None,
            holds=holds,
            provider_issues=(),
        )

    @classmethod
    def incomplete(
        cls,
        *,
        state_revision: int,
        holds: tuple[DagHoldSummary, ...] = (),
        provider_issues: tuple[DagProviderIssueSummary, ...] = (),
    ) -> SessionStageResult:
        return cls(
            status=SessionStageStatus.INCOMPLETE,
            base_state_revision=state_revision,
            resulting_state_revision=state_revision,
            batch_summary=None,
            holds=holds,
            provider_issues=provider_issues,
        )

    @classmethod
    def failed(
        cls,
        *,
        base_state_revision: int,
        resulting_state_revision: int | None = None,
        holds: tuple[DagHoldSummary, ...] = (),
        provider_issues: tuple[DagProviderIssueSummary, ...] = (),
    ) -> SessionStageResult:
        return cls(
            status=SessionStageStatus.FAILED,
            base_state_revision=base_state_revision,
            resulting_state_revision=(
                resulting_state_revision
                if resulting_state_revision is not None
                else base_state_revision
            ),
            batch_summary=None,
            holds=holds,
            provider_issues=provider_issues,
        )

    @classmethod
    def rejected(
        cls,
        *,
        base_state_revision: int,
        resulting_state_revision: int,
        batch_summary: SessionBatchSummary | None = None,
        holds: tuple[DagHoldSummary, ...] = (),
        provider_issues: tuple[DagProviderIssueSummary, ...] = (),
    ) -> SessionStageResult:
        return cls(
            status=SessionStageStatus.REJECTED,
            base_state_revision=base_state_revision,
            resulting_state_revision=resulting_state_revision,
            batch_summary=batch_summary,
            holds=holds,
            provider_issues=provider_issues,
        )

    @classmethod
    def not_run(
        cls,
        *,
        state_revision: int | None = None,
    ) -> SessionStageResult:
        return cls(
            status=SessionStageStatus.NOT_RUN,
            base_state_revision=state_revision,
            resulting_state_revision=state_revision,
            batch_summary=None,
            holds=(),
            provider_issues=(),
        )

    @classmethod
    def catastrophic(
        cls,
        *,
        base_state_revision: int,
        batch_summary: SessionBatchSummary,
        holds: tuple[DagHoldSummary, ...] = (),
        provider_issues: tuple[DagProviderIssueSummary, ...] = (),
    ) -> SessionStageResult:
        return cls(
            status=SessionStageStatus.CATASTROPHIC,
            base_state_revision=base_state_revision,
            resulting_state_revision=None,
            batch_summary=batch_summary,
            holds=holds,
            provider_issues=provider_issues,
        )

    @property
    def is_applied(self) -> bool:
        return self.status == SessionStageStatus.APPLIED

    @property
    def is_skipped(self) -> bool:
        return self.status == SessionStageStatus.SKIPPED

    @property
    def is_held(self) -> bool:
        return self.status == SessionStageStatus.HELD

    @property
    def is_incomplete(self) -> bool:
        return self.status == SessionStageStatus.INCOMPLETE

    @property
    def is_failed(self) -> bool:
        return self.status == SessionStageStatus.FAILED

    @property
    def is_rejected(self) -> bool:
        return self.status == SessionStageStatus.REJECTED

    @property
    def is_not_run(self) -> bool:
        return self.status == SessionStageStatus.NOT_RUN

    @property
    def is_catastrophic(self) -> bool:
        return self.status == SessionStageStatus.CATASTROPHIC

    @property
    def hold_count(self) -> int:
        return len(self.holds)

    @property
    def provider_issue_count(self) -> int:
        return len(self.provider_issues)

    @property
    def has_provider_issues(self) -> bool:
        return bool(self.provider_issues)

    @property
    def command_count(self) -> int:
        return self.batch_summary.command_count if self.batch_summary is not None else 0

    @property
    def command_ids(self) -> tuple[str, ...]:
        return self.batch_summary.command_ids if self.batch_summary is not None else ()

    @property
    def command_types(self) -> tuple[str, ...]:
        return self.batch_summary.command_types if self.batch_summary is not None else ()

    @property
    def rejection_code(self) -> str | None:
        return self.batch_summary.rejection_code if self.batch_summary is not None else None

    @property
    def rejection_message(self) -> str | None:
        return self.batch_summary.rejection_message if self.batch_summary is not None else None

    @property
    def artifact_ids(self) -> tuple[str, ...]:
        return self.batch_summary.artifact_ids if self.batch_summary is not None else ()


@dataclass(frozen=True, slots=True)
class SessionResult:
    company_id: str
    session_id: str
    starting_persistence_revision: int
    final_persistence_revision: int
    final_local_state_revision: int | None
    last_consistent_local_state_revision: int | None = None

    routing_stage_result: SessionStageResult | None = None
    dag_stage_result: SessionStageResult | None = None
    payment_application_stage_result: SessionStageResult | None = None
    reconciliation_stage_result: SessionStageResult | None = None
    residual_stage_result: SessionStageResult | None = None

    executed_payment_application_ids: tuple[str, ...] = ()
    rehydration_count: int = 0

    residual_evaluation_count: int = 0
    residual_classified_count: int = 0
    residual_hold_count: int = 0
    residual_posting_count: int = 0
    residual_posting_noop_count: int = 0
    unresolved_residual_hold_count: int = 0
    executed_residual_posting_ids: tuple[str, ...] = ()

    final_validation_status: StateValidationReport | None = None

    artifact_fingerprint: str | None = None
    state_fingerprint: str | None = None

    is_success: bool = False
    failure_stage: FailureStage | None = None
    failure_reason: str | None = None

    @property
    def hold_count(self) -> int:
        if self.dag_stage_result is not None:
            return self.dag_stage_result.hold_count
        return 0

    @property
    def holds(self) -> tuple[DagHoldSummary, ...]:
        if self.dag_stage_result is not None:
            return self.dag_stage_result.holds
        return ()

    @property
    def provider_issue_count(self) -> int:
        if self.dag_stage_result is not None:
            return self.dag_stage_result.provider_issue_count
        return 0

    @property
    def provider_issues(self) -> tuple[DagProviderIssueSummary, ...]:
        if self.dag_stage_result is not None:
            return self.dag_stage_result.provider_issues
        return ()

    @property
    def is_provider_incomplete(self) -> bool:
        if self.dag_stage_result is not None:
            return self.dag_stage_result.has_provider_issues
        return False
