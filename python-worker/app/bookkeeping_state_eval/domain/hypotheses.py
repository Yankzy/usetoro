from __future__ import annotations

from datetime import datetime
from typing import Annotated, Any

from pydantic import (
    BaseModel,
    ConfigDict,
    Field,
    StringConstraints,
    model_validator,
)

from .enums import AllocationSupport, Eligibility, SemanticAdmissibility
from .reconciliations import BankAllocation, BookAllocation


Identifier = Annotated[
    str,
    StringConstraints(strip_whitespace=True, min_length=1, max_length=255),
]


class ReconciliationHypothesis(BaseModel):
    """
    Runtime-only proposed reconciliation relationship.

    This is NOT bookkeeping truth and is never required for state
    reconstruction.

    A hypothesis represents a semantically evaluated candidate relationship
    that may later be promoted into a durable Reconciliation artifact.

    The model intentionally does not enforce reconciliation conservation or
    cross-artifact validity. Those are transition/optimizer concerns.

    This allows the system to represent and subsequently reject malformed,
    stale, or otherwise invalid hypotheses through deterministic validation.
    """

    model_config = ConfigDict(
        frozen=True,
        extra="forbid",
    )

    id: Identifier

    eligibility: Eligibility = Eligibility.SELECTABLE

    admissibility: SemanticAdmissibility = SemanticAdmissibility.SUPPORTED

    allocation_support: AllocationSupport = AllocationSupport.EXPLICIT_EVIDENCE

    utility: int = Field(
        ...,
        ge=0,
        le=1000,
        description=(
            "Globally comparable cardinal semantic-preference score per unit of "
            "reconciled money, in range [0, 1000]. The optimizer treats score differences "
            "consistently across feasible candidates. It is not a probability, not raw LLM "
            "confidence, and not a claim that 800 is literally twice as correct as 400. "
            "Scores are required to be comparable across candidate types (1:1, 1:N, N:1) "
            "and across different BankItems and BookItems, while remaining packaging invariant. "
            "Hard feasibility is never encoded in this score. The production provider should "
            "be empirically calibrated against labelled reconciliation outcomes/evals "
            "before treating score magnitudes as strong quantitative evidence."
        ),
    )

    semantic_value: int | None = Field(
        default=None,
        ge=0,
        description=(
            "Exact unscaled amount-weighted semantic value: "
            "sum(pair_semantic_score * pair_allocated_amount). "
            "When set, the optimizer consumes this integer directly to guarantee exact "
            "packaging invariance between grouped and fragmented candidate representations. "
            "If None, falls back to utility * total_bank_allocation_int."
        ),
    )

    @model_validator(mode="before")
    @classmethod
    def _resolve_semantic_score_alias(cls, data: Any) -> Any:
        if isinstance(data, dict):
            if "utility" not in data and "semantic_score" in data:
                data["utility"] = data["semantic_score"]
        return data

    bank_allocations: tuple[BankAllocation, ...] = Field(
        ...,
        min_length=1,
    )

    book_allocations: tuple[BookAllocation, ...] = Field(
        ...,
        min_length=1,
    )

    evidence_refs: tuple[Identifier, ...] = Field(default_factory=tuple)

    semantic_rationale: str | None = Field(
        default=None,
        max_length=4_000,
    )

    state_revision: int = Field(
        ...,
        ge=0,
        description=(
            "BookkeepingState revision against which this hypothesis was "
            "generated. Promotion must fail if relevant state has become stale."
        ),
    )

    generated_at: datetime

    @property
    def total_bank_allocation_int(self) -> int:
        return sum(
            allocation.amount_int
            for allocation in self.bank_allocations
        )

    @property
    def total_book_allocation_int(self) -> int:
        return sum(
            allocation.amount_int
            for allocation in self.book_allocations
        )

    @property
    def is_balanced(self) -> bool:
        """
        Convenience diagnostic only.

        A false result does not make construction of the hypothesis fail.
        Deterministic validation decides whether the hypothesis may be promoted.
        """
        return (
            self.total_bank_allocation_int
            == self.total_book_allocation_int
        )

    @property
    def semantic_score(self) -> int:
        """
        Globally comparable cardinal semantic-preference score per unit of reconciled money in [0, 1000].
        The optimizer treats score differences consistently across feasible candidates.
        """
        return self.utility

    @property
    def exact_semantic_value(self) -> int:
        """
        Exact unscaled amount-weighted semantic value for global optimization.
        Guarantees that packaging alone cannot alter the total Phase 3 objective value.
        """
        if self.semantic_value is not None:
            return self.semantic_value
        return self.utility * self.total_bank_allocation_int