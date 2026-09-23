from __future__ import annotations

from datetime import date, datetime
from typing import Annotated, Sequence

from pydantic import (
    BaseModel,
    ConfigDict,
    Field,
    StringConstraints,
    field_validator,
    model_validator,
)

from bookkeeping_state.domain.enums import Direction
from bookkeeping_state.domain.money import (
    AmountUnits,
    amount_units_to_int,
)

Identifier = Annotated[
    str,
    StringConstraints(
        min_length=1,
        strip_whitespace=True,
    ),
]


class ObligationAllocation(BaseModel):
    """
    Exact allocation of value against an open obligation (STAGING_BOOK_ITEM).
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


class PaymentPostingIntent(BaseModel):
    """
    Typed accounting intent describing the payment journal entry that should later
    be executed and posted through the Django ledger.

    This represents a planned settlement action, NOT an already-posted or already-applied state.
    """

    model_config = ConfigDict(
        frozen=True,
        extra="forbid",
    )

    intent_id: Identifier
    company_id: Identifier
    bank_item_id: Identifier
    bank_account_id: Identifier
    direction: Direction
    currency: str = Field(..., min_length=3, max_length=3)
    total_amount_units: AmountUnits
    payment_date: date
    obligation_allocations: tuple[ObligationAllocation, ...] = Field(..., min_length=1)

    evidence_refs: tuple[Identifier, ...] = ()
    semantic_rationale: str | None = None
    expected_state_revision: int = Field(..., ge=0)
    issued_at: datetime

    @field_validator("currency")
    @classmethod
    def normalize_currency(cls, value: str) -> str:
        return value.upper()

    @field_validator("issued_at")
    @classmethod
    def validate_issued_at_tz(cls, value: datetime) -> datetime:
        if value.tzinfo is None or value.utcoffset() is None:
            raise ValueError("issued_at must be timezone-aware")
        return value

    @property
    def total_amount_int(self) -> int:
        return amount_units_to_int(self.total_amount_units)

    @model_validator(mode="after")
    def validate_allocations_sum(self) -> "PaymentPostingIntent":
        alloc_sum = sum(a.amount_int for a in self.obligation_allocations)
        if alloc_sum != self.total_amount_int:
            raise ValueError(
                f"PaymentPostingIntent total ({self.total_amount_int}) "
                f"!= sum of allocations ({alloc_sum})"
            )
        return self


class PaymentApplicationCandidate(BaseModel):
    """
    Proposed match between an unresolved bank movement and one or more open obligations.
    """

    model_config = ConfigDict(
        frozen=True,
        extra="forbid",
    )

    candidate_id: Identifier
    bank_item_id: Identifier
    obligation_allocations: tuple[ObligationAllocation, ...] = Field(..., min_length=1)
    total_amount_units: AmountUnits
    evidence_refs: tuple[Identifier, ...] = ()
    rationale: str | None = None

    @property
    def total_amount_int(self) -> int:
        return amount_units_to_int(self.total_amount_units)

    @property
    def obligation_ids(self) -> tuple[str, ...]:
        return tuple(a.book_item_id for a in self.obligation_allocations)

    @model_validator(mode="after")
    def validate_amounts(self) -> "PaymentApplicationCandidate":
        alloc_sum = sum(a.amount_int for a in self.obligation_allocations)
        if alloc_sum != self.total_amount_int:
            raise ValueError(
                f"Candidate total ({self.total_amount_int}) != sum of allocations ({alloc_sum})"
            )
        return self


class PaymentApplicationProposal(BaseModel):
    """
    Evaluated, scored payment application proposal containing a candidate match
    and the corresponding PaymentPostingIntent.
    """

    model_config = ConfigDict(
        frozen=True,
        extra="forbid",
    )

    proposal_id: Identifier
    candidate: PaymentApplicationCandidate
    semantic_score: int = Field(..., ge=0, le=1000)
    semantic_value: int = Field(default=0, ge=0)
    intent: PaymentPostingIntent

    @property
    def bank_item_id(self) -> str:
        return self.candidate.bank_item_id


class PaymentApplicationPlan(BaseModel):
    """
    Globally non-conflicting payment application plan for a snapshot revision.

    This is a purely read-only plan. It does NOT mutate BookkeepingState, does NOT
    decrement obligation capacity, does NOT consume bank items, and does NOT advance revisions.
    """

    model_config = ConfigDict(
        frozen=True,
        extra="forbid",
    )

    state_revision: int = Field(..., ge=0)
    session_id: Identifier
    proposals: tuple[PaymentApplicationProposal, ...] = ()
    excluded_by_posted_authority_bank_item_ids: tuple[Identifier, ...] = ()
    unmatched_bank_item_ids: tuple[Identifier, ...] = ()
    unmatched_obligation_ids: tuple[Identifier, ...] = ()

    @property
    def has_proposals(self) -> bool:
        return bool(self.proposals)

    @property
    def proposed_bank_item_ids(self) -> tuple[str, ...]:
        return tuple(sorted({p.bank_item_id for p in self.proposals}))

    @property
    def total_proposed_amount_int(self) -> int:
        return sum(p.intent.total_amount_int for p in self.proposals)

    @model_validator(mode="after")
    def validate_non_conflicting(self) -> "PaymentApplicationPlan":
        # Check no duplicate bank items across proposals
        bank_ids = [p.bank_item_id for p in self.proposals]
        if len(set(bank_ids)) != len(bank_ids):
            raise ValueError("PaymentApplicationPlan contains duplicate bank items across proposals")

        # Check no overlap with excluded bank items
        excluded_set = set(self.excluded_by_posted_authority_bank_item_ids)
        overlap = set(bank_ids) & excluded_set
        if overlap:
            raise ValueError(
                f"PaymentApplicationPlan proposals overlap with excluded_by_posted_authority bank items: {sorted(overlap)}"
            )

        return self
