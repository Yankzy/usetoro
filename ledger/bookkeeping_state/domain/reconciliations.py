from __future__ import annotations

from datetime import datetime
from typing import Annotated

from pydantic import (
    BaseModel,
    ConfigDict,
    Field,
    StringConstraints,
    field_validator,
    model_validator,
)

from .enums import AllocationSupport, SemanticAdmissibility
from .money import AmountUnits, amount_units_to_int
from typing import Any


Identifier = Annotated[
    str,
    StringConstraints(strip_whitespace=True, min_length=1, max_length=255),
]


class BankAllocation(BaseModel):
    """
    Exact amount of a BankItem consumed by a reconciliation relationship.
    """

    model_config = ConfigDict(
        frozen=True,
        extra="forbid",
    )

    bank_item_id: Identifier
    amount_units: AmountUnits

    @property
    def amount_int(self) -> int:
        return amount_units_to_int(self.amount_units)


class BookAllocation(BaseModel):
    """
    Exact amount of a BookItem consumed by a reconciliation relationship.
    """

    model_config = ConfigDict(
        frozen=True,
        extra="forbid",
    )

    book_item_id: Identifier
    amount_units: AmountUnits

    @property
    def amount_int(self) -> int:
        return amount_units_to_int(self.amount_units)


class ReconciliationHypothesisProvenance(BaseModel):
    """
    Compact immutable durable record of the runtime hypothesis promoted into this Reconciliation.
    """

    model_config = ConfigDict(
        frozen=True,
        extra="forbid",
    )

    hypothesis_id: Identifier
    state_revision: int = Field(..., ge=0)
    utility: int = Field(..., ge=0, le=1000)
    admissibility: SemanticAdmissibility = SemanticAdmissibility.SUPPORTED
    allocation_support: AllocationSupport = AllocationSupport.EXPLICIT_EVIDENCE
    generated_at: datetime

    @field_validator("generated_at")
    @classmethod
    def validate_generated_at_tz(cls, value: datetime) -> datetime:
        if value.tzinfo is None or value.utcoffset() is None:
            raise ValueError("generated_at must be timezone-aware")
        return value

    @property
    def semantic_score(self) -> int:
        return self.utility


class Reconciliation(BaseModel):
    """
    Durable accepted reconciliation relationship.

    This artifact records the relationship that became bookkeeping truth.

    It supports:
        1:1
        1:N
        N:1
        N:M

    It does NOT store mutable residual amounts. Residual book capacity is
    derived by BookkeepingState from active reconciliation allocations.
    """

    model_config = ConfigDict(
        frozen=True,
        extra="forbid",
    )

    id: Identifier

    bank_allocations: tuple[BankAllocation, ...] = Field(..., min_length=1)
    book_allocations: tuple[BookAllocation, ...] = Field(..., min_length=1)

    evidence_refs: tuple[Identifier, ...] = Field(default_factory=tuple)

    source_hypothesis_id: Identifier | None = Field(
        default=None,
        description=(
            "Runtime reconciliation hypothesis from which this durable "
            "relationship was promoted, when applicable."
        ),
    )

    source_hypothesis_state_revision: int | None = Field(
        default=None,
        ge=0,
        description="State revision against which the source hypothesis was generated.",
    )

    source_hypothesis_utility: int | None = Field(
        default=None,
        ge=0,
        le=1000,
        description="Semantic preference score of the source hypothesis.",
    )

    source_hypothesis_generated_at: datetime | None = Field(
        default=None,
        description="Timestamp when the source hypothesis was generated.",
    )

    source_hypothesis_admissibility: SemanticAdmissibility | None = Field(
        default=None,
        description="Admissibility status of the source hypothesis.",
    )

    source_hypothesis_allocation_support: AllocationSupport | None = Field(
        default=None,
        description="Allocation support basis of the source hypothesis.",
    )

    semantic_rationale: str | None = Field(
        default=None,
        max_length=4_000,
    )

    session_id: Identifier | None = None

    state_revision_at_creation: int = Field(..., ge=0)

    created_at: datetime

    @field_validator("created_at")
    @classmethod
    def validate_created_at_tz(cls, value: datetime) -> datetime:
        if value.tzinfo is None or value.utcoffset() is None:
            raise ValueError("created_at must be timezone-aware")
        return value

    @field_validator("source_hypothesis_generated_at")
    @classmethod
    def validate_source_hyp_generated_at_tz(
        cls, value: datetime | None
    ) -> datetime | None:
        if value is None:
            return None
        if value.tzinfo is None or value.utcoffset() is None:
            raise ValueError(
                "source_hypothesis_generated_at must be timezone-aware"
            )
        return value

    @property
    def hypothesis_provenance(self) -> ReconciliationHypothesisProvenance | None:
        if self.source_hypothesis_id is None:
            return None
        return ReconciliationHypothesisProvenance(
            hypothesis_id=self.source_hypothesis_id,
            state_revision=self.source_hypothesis_state_revision,  # type: ignore[arg-type]
            utility=self.source_hypothesis_utility,  # type: ignore[arg-type]
            admissibility=self.source_hypothesis_admissibility or SemanticAdmissibility.SUPPORTED,
            allocation_support=self.source_hypothesis_allocation_support or AllocationSupport.EXPLICIT_EVIDENCE,
            generated_at=self.source_hypothesis_generated_at,  # type: ignore[arg-type]
        )

    @property
    def source_hypothesis_semantic_score(self) -> int | None:
        return self.source_hypothesis_utility

    @property
    def total_bank_allocation_int(self) -> int:
        return sum(allocation.amount_int for allocation in self.bank_allocations)

    @property
    def total_book_allocation_int(self) -> int:
        return sum(allocation.amount_int for allocation in self.book_allocations)

    @model_validator(mode="before")
    @classmethod
    def _default_source_admissibility(cls, data: Any) -> Any:
        if isinstance(data, dict):
            if (
                data.get("source_hypothesis_id") is not None
                and data.get("source_hypothesis_admissibility") is None
            ):
                data["source_hypothesis_admissibility"] = SemanticAdmissibility.SUPPORTED
        return data

    @model_validator(mode="after")
    def validate_reconciliation(self) -> "Reconciliation":
        bank_ids = [
            allocation.bank_item_id
            for allocation in self.bank_allocations
        ]
        book_ids = [
            allocation.book_item_id
            for allocation in self.book_allocations
        ]

        if len(bank_ids) != len(set(bank_ids)):
            raise ValueError(
                "A reconciliation cannot contain duplicate BankItem allocations"
            )

        if len(book_ids) != len(set(book_ids)):
            raise ValueError(
                "A reconciliation cannot contain duplicate BookItem allocations"
            )

        if self.total_bank_allocation_int != self.total_book_allocation_int:
            raise ValueError(
                "Reconciliation must conserve monetary value: "
                f"bank={self.total_bank_allocation_int}, "
                f"book={self.total_book_allocation_int}"
            )

        provenance_values = (
            self.source_hypothesis_id,
            self.source_hypothesis_state_revision,
            self.source_hypothesis_utility,
            self.source_hypothesis_generated_at,
        )
        present_count = sum(1 for v in provenance_values if v is not None)
        if present_count not in (0, 4):
            raise ValueError(
                "Reconciliation hypothesis provenance fields must be either all present or all absent: "
                f"source_hypothesis_id={self.source_hypothesis_id!r}, "
                f"source_hypothesis_state_revision={self.source_hypothesis_state_revision!r}, "
                f"source_hypothesis_utility={self.source_hypothesis_utility!r}, "
                f"source_hypothesis_generated_at={self.source_hypothesis_generated_at!r}"
            )

        if present_count == 4:
            if (
                self.source_hypothesis_state_revision + 1  # type: ignore[operator]
                != self.state_revision_at_creation
            ):
                raise ValueError(
                    f"source_hypothesis_state_revision + 1 "
                    f"({self.source_hypothesis_state_revision + 1}) "  # type: ignore[operator]
                    f"must equal state_revision_at_creation ({self.state_revision_at_creation})"
                )

        return self


class ReconciliationInvalidation(BaseModel):
    """
    Durable record invalidating a previously accepted reconciliation.

    The original Reconciliation remains permanently available for audit and
    historical reconstruction.
    """

    model_config = ConfigDict(
        frozen=True,
        extra="forbid",
    )

    id: Identifier
    reconciliation_id: Identifier

    reason: str = Field(..., min_length=1, max_length=4_000)

    session_id: Identifier | None = None

    state_revision_at_invalidation: int = Field(..., ge=0)

    created_at: datetime

    @field_validator("created_at")
    @classmethod
    def validate_invalidation_created_at_tz(cls, value: datetime) -> datetime:
        if value.tzinfo is None or value.utcoffset() is None:
            raise ValueError("created_at must be timezone-aware")
        return value