from __future__ import annotations

from datetime import datetime
from enum import StrEnum
from typing import Annotated, Any, Sequence

from pydantic import (
    BaseModel,
    ConfigDict,
    Field,
    StringConstraints,
    model_validator,
)

from bookkeeping_state.domain.commands import CreateReconciliationCommand
from bookkeeping_state.domain.enums import (
    AllocationSupport,
    Eligibility,
    SemanticAdmissibility,
)
from bookkeeping_state.domain.hypotheses import ReconciliationHypothesis
from bookkeeping_state.domain.money import (
    AmountUnits,
    ResidualAmountUnits,
    amount_units_to_int,
)
from bookkeeping_state.domain.reconciliations import (
    BankAllocation,
    BookAllocation,
)
from bookkeeping_state.transitions.batch import (
    TransitionBatch,
    TransitionBatchError,
)

Identifier = Annotated[
    str,
    StringConstraints(
        min_length=1,
        strip_whitespace=True,
    ),
]


class CandidateType(StrEnum):
    ONE_TO_ONE_EXACT = "ONE_TO_ONE_EXACT"
    ONE_TO_ONE_PARTIAL_BOOK = "ONE_TO_ONE_PARTIAL_BOOK"
    ONE_TO_ONE_PARTIAL_BANK = "ONE_TO_ONE_PARTIAL_BANK"
    ONE_TO_MANY = "ONE_TO_MANY"
    MANY_TO_ONE = "MANY_TO_ONE"
    MANY_TO_MANY = "MANY_TO_MANY"


class FeasibilityStatus(StrEnum):
    FEASIBLE = "FEASIBLE"
    MONETARY_IMBALANCE = "MONETARY_IMBALANCE"
    UNKNOWN_ITEM = "UNKNOWN_ITEM"
    DUPLICATE_ALLOCATION = "DUPLICATE_ALLOCATION"
    CAPACITY_EXCEEDED = "CAPACITY_EXCEEDED"
    PARTIAL_BANK_FORBIDDEN = "PARTIAL_BANK_FORBIDDEN"
    PARTIAL_BOOK_FORBIDDEN = "PARTIAL_BOOK_FORBIDDEN"
    CURRENCY_MISMATCH = "CURRENCY_MISMATCH"
    DIRECTION_MISMATCH = "DIRECTION_MISMATCH"
    ROUTING_CONTRADICTION = "ROUTING_CONTRADICTION"
    DATE_WINDOW_EXCEEDED = "DATE_WINDOW_EXCEEDED"


class UnresolvedReason(StrEnum):
    """
    Reason why a BankItem remained unresolved after global reconciliation planning.
    """

    NO_VALID_CANDIDATES = "NO_VALID_CANDIDATES"
    NO_SEMANTICALLY_ADMISSIBLE_CANDIDATES = "NO_SEMANTICALLY_ADMISSIBLE_CANDIDATES"
    NO_MUTUALLY_COMPATIBLE_MATCH = "NO_MUTUALLY_COMPATIBLE_MATCH"
    ALLOCATION_REQUIRES_REVIEW = "ALLOCATION_REQUIRES_REVIEW"


class CandidateFeasibilityResult(BaseModel):
    """
    Deterministic explanation of candidate feasibility.
    """

    model_config = ConfigDict(
        frozen=True,
        extra="forbid",
    )

    status: FeasibilityStatus
    message: str | None = None

    @property
    def is_feasible(self) -> bool:
        return self.status == FeasibilityStatus.FEASIBLE


class ReconciliationCandidate(BaseModel):
    """
    Mathematically feasible candidate reconciliation produced by candidate generation.

    This candidate has passed all hard deterministic constraints, but has not yet
    been semantically scored or promoted to a ReconciliationHypothesis.
    """

    model_config = ConfigDict(
        frozen=True,
        extra="forbid",
    )

    candidate_id: Identifier
    candidate_type: CandidateType

    bank_allocations: tuple[BankAllocation, ...] = Field(..., min_length=1)
    book_allocations: tuple[BookAllocation, ...] = Field(..., min_length=1)

    total_amount_units: AmountUnits
    evidence_refs: tuple[Identifier, ...] = ()
    rationale: str | None = None

    @property
    def total_amount_int(self) -> int:
        return amount_units_to_int(self.total_amount_units)

    @property
    def bank_item_ids(self) -> tuple[str, ...]:
        return tuple(a.bank_item_id for a in self.bank_allocations)

    @property
    def book_item_ids(self) -> tuple[str, ...]:
        return tuple(a.book_item_id for a in self.book_allocations)

    @model_validator(mode="after")
    def validate_candidate(self) -> "ReconciliationCandidate":
        bank_total = sum(a.amount_int for a in self.bank_allocations)
        book_total = sum(a.amount_int for a in self.book_allocations)

        if bank_total != book_total:
            raise ValueError(
                f"Candidate monetary imbalance: bank={bank_total} != book={book_total}"
            )

        if bank_total != self.total_amount_int:
            raise ValueError(
                f"Candidate total_amount_units mismatch: expected {bank_total}, got {self.total_amount_int}"
            )

        return self

    def to_hypothesis(
        self,
        *,
        state_revision: int,
        utility: int | None = None,
        semantic_score: int | None = None,
        semantic_value: int | None = None,
        semantic_rationale: str | None = None,
        admissibility: SemanticAdmissibility = SemanticAdmissibility.SUPPORTED,
        allocation_support: AllocationSupport = AllocationSupport.EXPLICIT_EVIDENCE,
        evidence_refs: Sequence[Identifier] | None = None,
        eligibility: Eligibility = Eligibility.SELECTABLE,
        generated_at: datetime | None = None,
    ) -> ReconciliationHypothesis:
        from datetime import timezone

        resolved_utility = (
            utility
            if utility is not None
            else (semantic_score if semantic_score is not None else 0)
        )
        resolved_evidence = (
            tuple(evidence_refs)
            if evidence_refs is not None
            else self.evidence_refs
        )
        resolved_value = (
            semantic_value
            if semantic_value is not None
            else (resolved_utility * self.total_amount_int)
        )

        return ReconciliationHypothesis(
            id=f"hyp:{self.candidate_id}",
            eligibility=eligibility,
            admissibility=admissibility,
            allocation_support=allocation_support,
            utility=resolved_utility,
            semantic_value=resolved_value,
            bank_allocations=self.bank_allocations,
            book_allocations=self.book_allocations,
            evidence_refs=resolved_evidence,
            semantic_rationale=semantic_rationale or self.rationale,
            state_revision=state_revision,
            generated_at=generated_at or datetime.now(timezone.utc),
        )


class UnresolvedBankItem(BaseModel):
    """
    BankItem that remained unresolved after global reconciliation optimization.
    """

    model_config = ConfigDict(
        frozen=True,
        extra="forbid",
    )

    bank_item_id: Identifier
    remaining_amount_units: ResidualAmountUnits
    reason: str = UnresolvedReason.NO_MUTUALLY_COMPATIBLE_MATCH

    @property
    def remaining_amount_int(self) -> int:
        return int(self.remaining_amount_units)


class BookResidual(BaseModel):
    """
    Residual amount tracking for a BookItem affected by selected reconciliations.
    """

    model_config = ConfigDict(
        frozen=True,
        extra="forbid",
    )

    book_item_id: Identifier
    starting_remaining_units: ResidualAmountUnits
    consumed_units: ResidualAmountUnits
    ending_remaining_units: ResidualAmountUnits

    @property
    def starting_remaining_int(self) -> int:
        return int(self.starting_remaining_units)

    @property
    def consumed_int(self) -> int:
        return int(self.consumed_units)

    @property
    def ending_remaining_int(self) -> int:
        return int(self.ending_remaining_units)


class ReconciliationResult(BaseModel):
    """
    Complete reconciliation proposal output from the global optimizer.

    This is runtime-only optimization output and does not mutate BookkeepingState.
    """

    model_config = ConfigDict(
        frozen=True,
        extra="forbid",
    )

    state_revision: int = Field(..., ge=0)
    solver_run_id: Identifier

    selected_hypotheses: tuple[ReconciliationHypothesis, ...] = ()
    review_hypotheses: tuple[ReconciliationHypothesis, ...] = ()
    unresolved_bank_items: tuple[UnresolvedBankItem, ...] = ()
    book_residuals: tuple[BookResidual, ...] = ()

    objective_reconciled_units: ResidualAmountUnits = "0"
    objective_amount_weighted_semantic_value: int = Field(default=0, ge=0)
    objective_items_cleared: int = Field(default=0, ge=0)

    @property
    def reconciled_units_int(self) -> int:
        return int(self.objective_reconciled_units)

    @property
    def average_semantic_score(self) -> int:
        """
        Integer average semantic-preference score per unit of reconciled money, in [0, 1000].
        Computed as: objective_amount_weighted_semantic_value // reconciled_units_int.
        Returns 0 if reconciled_units_int == 0.
        """
        reconciled = self.reconciled_units_int
        if reconciled <= 0:
            return 0
        return self.objective_amount_weighted_semantic_value // reconciled

    @model_validator(mode="after")
    def validate_result(self) -> "ReconciliationResult":
        # Check no duplicate hypotheses
        hyp_ids = [h.id for h in self.selected_hypotheses]
        if len(set(hyp_ids)) != len(hyp_ids):
            raise ValueError("ReconciliationResult contains duplicate selected hypotheses")

        # Bank items must not be selected multiple times
        selected_bank_ids: list[str] = []
        for h in self.selected_hypotheses:
            for b_alloc in h.bank_allocations:
                selected_bank_ids.append(b_alloc.bank_item_id)
        if len(set(selected_bank_ids)) != len(selected_bank_ids):
            raise ValueError("ReconciliationResult contains duplicate bank item allocations across hypotheses")

        calc_reconciled = sum(h.total_bank_allocation_int for h in self.selected_hypotheses)
        if calc_reconciled != int(self.objective_reconciled_units):
            raise ValueError(
                f"objective_reconciled_units mismatch: calculated {calc_reconciled} != declared {self.objective_reconciled_units}"
            )

        calc_weighted_value = sum(
            h.exact_semantic_value for h in self.selected_hypotheses
        )
        if calc_weighted_value != self.objective_amount_weighted_semantic_value:
            raise ValueError(
                f"objective_amount_weighted_semantic_value mismatch: calculated {calc_weighted_value} != declared {self.objective_amount_weighted_semantic_value}"
            )

        return self


class ReconciliationPlan(BaseModel):
    """
    Complete runtime plan produced by ReconciliationService.

    Preserves exact base state_revision and adapts to ONE atomic TransitionBatch.
    """

    model_config = ConfigDict(
        frozen=True,
        extra="forbid",
    )

    state_revision: int = Field(..., ge=0)
    solver_run_id: Identifier

    result: ReconciliationResult
    hypotheses: tuple[ReconciliationHypothesis, ...] = ()
    commands: tuple[CreateReconciliationCommand, ...] = ()

    @model_validator(mode="after")
    def validate_plan(self) -> "ReconciliationPlan":
        if self.result.state_revision != self.state_revision:
            raise ValueError(
                f"ReconciliationPlan state_revision ({self.state_revision}) != result state_revision ({self.result.state_revision})"
            )

        if len(self.commands) != len(self.result.selected_hypotheses):
            raise ValueError(
                f"ReconciliationPlan commands count ({len(self.commands)}) != selected hypotheses ({len(self.result.selected_hypotheses)})"
            )

        for cmd in self.commands:
            if cmd.expected_state_revision != self.state_revision:
                raise ValueError(
                    f"Command {cmd.command_id} expected_state_revision ({cmd.expected_state_revision}) != plan state_revision ({self.state_revision})"
                )

        return self

    @property
    def has_reconciliations(self) -> bool:
        return bool(self.commands)

    def to_batch(
        self,
        *,
        batch_id: str | None = None,
    ) -> TransitionBatch:
        """
        Adapt this reconciliation plan into an atomic TransitionBatch.
        """
        if not self.commands:
            raise TransitionBatchError(
                "Cannot create TransitionBatch from ReconciliationPlan with no commands"
            )

        resolved_batch_id = batch_id or f"batch:reconciliation:{self.solver_run_id}"
        return TransitionBatch(
            batch_id=resolved_batch_id,
            session_id=self.commands[0].session_id,
            expected_state_revision=self.state_revision,
            commands=self.commands,
        )


def reconciliation_plan_to_transition_batch(
    plan: ReconciliationPlan,
    *,
    batch_id: str | None = None,
) -> TransitionBatch:
    return plan.to_batch(batch_id=batch_id)


class CounterpartyRelation(StrEnum):
    MATCH = "MATCH"
    POSSIBLE_ALIAS = "POSSIBLE_ALIAS"
    CONTRADICTED = "CONTRADICTED"
    NONE = "NONE"


class ReferenceRelation(StrEnum):
    MATCH = "MATCH"
    CONTRADICTED = "CONTRADICTED"
    NONE = "NONE"


class PairwiseSemanticObservation(BaseModel):
    """
    Provider-neutral semantic observation for one material (BankItem, BookItem) pair.
    Carries semantic facts discovered by a semantic provider (e.g. LLM, Go ASE)
    without imposing LLM transport models on deterministic allocation analysis.
    """

    model_config = ConfigDict(
        frozen=True,
        extra="forbid",
    )

    bank_item_id: Identifier
    book_item_id: Identifier
    identity_admissibility: SemanticAdmissibility = SemanticAdmissibility.SUPPORTED
    semantic_score: int = Field(..., ge=0, le=1000)
    counterparty_relation: CounterpartyRelation = CounterpartyRelation.NONE
    reference_relation: ReferenceRelation = ReferenceRelation.NONE
    matched_reference: str | None = None
    partial_payment_language: bool = False
    batch_or_remittance_reference: str | None = None
    evidence_document_ids: tuple[Identifier, ...] = ()
    evidence_tags: tuple[str, ...] = ()
    rationale: str | None = None
