from __future__ import annotations

from enum import StrEnum
from typing import Annotated

from pydantic import (
    BaseModel,
    ConfigDict,
    Field,
    StringConstraints,
    model_validator,
)

from bookkeeping_state.domain.enums import Direction
from bookkeeping_state.domain.money import (
    AmountUnits,
    ResidualAmountUnits,
    amount_units_to_int,
)


Identifier = Annotated[
    str,
    StringConstraints(
        min_length=1,
        strip_whitespace=True,
    ),
]

"""
The separation is now:

RoutingFeasibility
    hard deterministic evidence

RoutingSemanticScore
    soft semantic evidence

RoutingAssignment
    optimizer proposal

RoutingResult
    complete proposed routing world

Critically:

RoutingResult != RoutingDecision

RoutingResult is disposable computation.

Only later:

RoutingAssignment
    |
    v
CreateRoutingDecisionCommand
    |
    v
TransitionEngine
    |
    v
RoutingDecision

does routing become bookkeeping truth.

I also deliberately included a feasibility witness. If routing says:

BOOK-17 belongs to BANK-ACCOUNT-A

we can ask deterministic code:

Why was A even considered feasible?

and get something like:

BOOK-17 = 1,500,000 units

BANK-A witness:
    BANK-4 = 1,000,000
    BANK-9 =   500,000

sum = 1,500,000

The witness is not a reconciliation claim. It only proves that the routing candidate is mathematically plausible.
"""

# ======================================================================
# Capacity
# ======================================================================


class RoutingCapacityBucket(BaseModel):
    """
    Bank capacity available to routing for one account and one compatible
    BookItem direction.

    This is runtime-derived data.

    It is NOT persisted bookkeeping truth.

    Example:

        bank-account-1
        BOOK_BANK_CREDIT
        available = 3,500,000 units

    means that, from the current bounded routing view, that account exposes
    3,500,000 solver units of bank-side capacity compatible with BookItems
    whose bank posting direction is BOOK_BANK_CREDIT.
    """

    model_config = ConfigDict(
        frozen=True,
        extra="forbid",
    )

    bank_account_id: Identifier

    book_direction: Direction

    available_units: ResidualAmountUnits

    bank_item_ids: tuple[Identifier, ...] = ()

    @model_validator(mode="after")
    def validate_capacity_bucket(
        self,
    ) -> "RoutingCapacityBucket":
        if len(set(self.bank_item_ids)) != len(
            self.bank_item_ids
        ):
            raise ValueError(
                "RoutingCapacityBucket.bank_item_ids contains duplicates"
            )

        return self

    @property
    def available_int(self) -> int:
        return int(self.available_units)


# ======================================================================
# Feasibility
# ======================================================================


class RoutingFeasibilityStatus(StrEnum):
    """
    Deterministic explanation for whether a BookItem can plausibly belong to
    one bank account.

    FEASIBLE means deterministic constraints found at least one exact bank-side
    witness for the BookItem amount.

    Semantic similarity is deliberately NOT represented here.
    """

    FEASIBLE = "FEASIBLE"

    NO_BANK_ITEMS = "NO_BANK_ITEMS"

    CURRENCY_MISMATCH = "CURRENCY_MISMATCH"

    DIRECTION_MISMATCH = "DIRECTION_MISMATCH"

    NO_EXACT_SUBSET = "NO_EXACT_SUBSET"


class RoutingFeasibility(BaseModel):
    """
    Feasibility evidence for one:

        BookItem x BankAccount

    pair.

    When feasible, witness_bank_item_ids contains one deterministic exact
    subset demonstrating that this account can explain the BookItem amount.

    This witness is diagnostic evidence only. It does NOT reserve those
    BankItems and it does NOT itself create a reconciliation.
    """

    model_config = ConfigDict(
        frozen=True,
        extra="forbid",
    )

    book_item_id: Identifier
    bank_account_id: Identifier

    status: RoutingFeasibilityStatus

    target_amount_units: AmountUnits

    compatible_bank_item_ids: tuple[
        Identifier,
        ...
    ] = ()

    witness_bank_item_ids: tuple[
        Identifier,
        ...
    ] = ()

    witness_total_units: ResidualAmountUnits = "0"

    @model_validator(mode="after")
    def validate_feasibility(
        self,
    ) -> "RoutingFeasibility":
        if len(set(self.compatible_bank_item_ids)) != len(
            self.compatible_bank_item_ids
        ):
            raise ValueError(
                "compatible_bank_item_ids contains duplicates"
            )

        if len(set(self.witness_bank_item_ids)) != len(
            self.witness_bank_item_ids
        ):
            raise ValueError(
                "witness_bank_item_ids contains duplicates"
            )

        compatible = set(
            self.compatible_bank_item_ids
        )

        witness = set(
            self.witness_bank_item_ids
        )

        if not witness.issubset(compatible):
            raise ValueError(
                "Every witness BankItem must also appear in "
                "compatible_bank_item_ids"
            )

        target = amount_units_to_int(
            self.target_amount_units
        )

        witness_total = int(
            self.witness_total_units
        )

        if (
            self.status
            == RoutingFeasibilityStatus.FEASIBLE
        ):
            if not self.witness_bank_item_ids:
                raise ValueError(
                    "FEASIBLE routing entry requires a witness subset"
                )

            if witness_total != target:
                raise ValueError(
                    "FEASIBLE routing witness total must exactly equal "
                    "the BookItem target amount"
                )

        else:
            if self.witness_bank_item_ids:
                raise ValueError(
                    "Infeasible routing entry cannot contain a witness subset"
                )

            if witness_total != 0:
                raise ValueError(
                    "Infeasible routing entry must have witness_total_units=0"
                )

        return self

    @property
    def is_feasible(self) -> bool:
        return (
            self.status
            == RoutingFeasibilityStatus.FEASIBLE
        )

    @property
    def target_amount_int(self) -> int:
        return amount_units_to_int(
            self.target_amount_units
        )


class RoutingFeasibilityMatrix(BaseModel):
    """
    Complete deterministic feasibility matrix for one RoutingView revision.

    There may be at most one entry for each:

        BookItem x BankAccount

    pair.
    """

    model_config = ConfigDict(
        frozen=True,
        extra="forbid",
    )

    state_revision: int = Field(
        ...,
        ge=0,
    )

    entries: tuple[
        RoutingFeasibility,
        ...
    ] = ()

    @model_validator(mode="after")
    def validate_unique_pairs(
        self,
    ) -> "RoutingFeasibilityMatrix":
        seen: set[
            tuple[str, str]
        ] = set()

        for entry in self.entries:
            key = (
                entry.book_item_id,
                entry.bank_account_id,
            )

            if key in seen:
                raise ValueError(
                    "RoutingFeasibilityMatrix contains duplicate "
                    f"BookItem/BankAccount pair {key!r}"
                )

            seen.add(key)

        return self

    def get(
        self,
        *,
        book_item_id: str,
        bank_account_id: str,
    ) -> RoutingFeasibility | None:
        for entry in self.entries:
            if (
                entry.book_item_id
                == book_item_id
                and entry.bank_account_id
                == bank_account_id
            ):
                return entry

        return None

    def feasible_accounts_for(
        self,
        book_item_id: str,
    ) -> tuple[str, ...]:
        return tuple(
            sorted(
                entry.bank_account_id
                for entry in self.entries
                if (
                    entry.book_item_id
                    == book_item_id
                    and entry.is_feasible
                )
            )
        )


# ======================================================================
# Semantic scoring
# ======================================================================


class RoutingSemanticScore(BaseModel):
    """
    Semantic preference for one feasible BookItem/BankAccount pair.

    Score range is intentionally integer 0..1000 so downstream CP-SAT can use
    it directly without floating-point conversion.

    Semantic score is soft evidence.

    It can rank feasible possibilities but can never override hard feasibility.
    """

    model_config = ConfigDict(
        frozen=True,
        extra="forbid",
    )

    book_item_id: Identifier
    bank_account_id: Identifier

    score: int = Field(
        ...,
        ge=0,
        le=1000,
    )

    rationale: str | None = None

    model_run_id: Identifier | None = None


class RoutingSemanticScoreMatrix(BaseModel):
    """
    Semantic scores generated against one exact BookkeepingState revision.
    """

    model_config = ConfigDict(
        frozen=True,
        extra="forbid",
    )

    state_revision: int = Field(
        ...,
        ge=0,
    )

    scores: tuple[
        RoutingSemanticScore,
        ...
    ] = ()

    @model_validator(mode="after")
    def validate_unique_pairs(
        self,
    ) -> "RoutingSemanticScoreMatrix":
        seen: set[
            tuple[str, str]
        ] = set()

        for score in self.scores:
            key = (
                score.book_item_id,
                score.bank_account_id,
            )

            if key in seen:
                raise ValueError(
                    "RoutingSemanticScoreMatrix contains duplicate "
                    f"BookItem/BankAccount pair {key!r}"
                )

            seen.add(key)

        return self

    def get(
        self,
        *,
        book_item_id: str,
        bank_account_id: str,
    ) -> RoutingSemanticScore | None:
        for score in self.scores:
            if (
                score.book_item_id
                == book_item_id
                and score.bank_account_id
                == bank_account_id
            ):
                return score

        return None

    def score_for(
        self,
        *,
        book_item_id: str,
        bank_account_id: str,
        default: int = 0,
    ) -> int:
        score = self.get(
            book_item_id=book_item_id,
            bank_account_id=bank_account_id,
        )

        if score is None:
            return default

        return score.score


# ======================================================================
# Optimization output
# ======================================================================


class RoutingAssignment(BaseModel):
    """
    One routing decision proposed by the optimizer.

    This is NOT yet a durable RoutingDecision.

    It becomes bookkeeping truth only after translation into a
    CreateRoutingDecisionCommand and acceptance by TransitionEngine.
    """

    model_config = ConfigDict(
        frozen=True,
        extra="forbid",
    )

    book_item_id: Identifier
    bank_account_id: Identifier

    amount_units: AmountUnits

    semantic_score: int = Field(
        ...,
        ge=0,
        le=1000,
    )

    feasibility_witness_bank_item_ids: tuple[
        Identifier,
        ...
    ] = ()

    @model_validator(mode="after")
    def validate_assignment(
        self,
    ) -> "RoutingAssignment":
        if len(
            set(
                self.feasibility_witness_bank_item_ids
            )
        ) != len(
            self.feasibility_witness_bank_item_ids
        ):
            raise ValueError(
                "feasibility_witness_bank_item_ids contains duplicates"
            )

        if not self.feasibility_witness_bank_item_ids:
            raise ValueError(
                "RoutingAssignment requires deterministic feasibility evidence"
            )

        return self

    @property
    def amount_int(self) -> int:
        return amount_units_to_int(
            self.amount_units
        )


class RoutingUnassignedReason(StrEnum):
    """
    Why a BookItem was left without a routing proposal.
    """

    NO_FEASIBLE_ACCOUNT = "NO_FEASIBLE_ACCOUNT"

    GLOBAL_CAPACITY_CONFLICT = (
        "GLOBAL_CAPACITY_CONFLICT"
    )

    OPTIMIZER_EXCLUDED = "OPTIMIZER_EXCLUDED"


class UnassignedRoutingItem(BaseModel):
    """
    BookItem considered by routing but not assigned to a bank account.
    """

    model_config = ConfigDict(
        frozen=True,
        extra="forbid",
    )

    book_item_id: Identifier

    amount_units: AmountUnits

    reason: RoutingUnassignedReason

    detail: str | None = None

    @property
    def amount_int(self) -> int:
        return amount_units_to_int(
            self.amount_units
        )


class RoutingResult(BaseModel):
    """
    Complete routing proposal generated against one exact state revision.

    RoutingResult is runtime-only.

    It does not mutate BookkeepingState and is not persisted directly.

    Its assignments are later translated into commands carrying:

        expected_state_revision = state_revision
    """

    model_config = ConfigDict(
        frozen=True,
        extra="forbid",
    )

    state_revision: int = Field(
        ...,
        ge=0,
    )

    solver_run_id: Identifier

    assignments: tuple[
        RoutingAssignment,
        ...
    ] = ()

    unassigned: tuple[
        UnassignedRoutingItem,
        ...
    ] = ()

    objective_routed_units: ResidualAmountUnits = "0"

    objective_semantic_utility: int = Field(
        default=0,
        ge=0,
    )

    @model_validator(mode="after")
    def validate_result(
        self,
    ) -> "RoutingResult":
        assigned_ids = [
            assignment.book_item_id
            for assignment in self.assignments
        ]

        if len(set(assigned_ids)) != len(
            assigned_ids
        ):
            raise ValueError(
                "RoutingResult assigns one BookItem more than once"
            )

        unassigned_ids = [
            item.book_item_id
            for item in self.unassigned
        ]

        if len(set(unassigned_ids)) != len(
            unassigned_ids
        ):
            raise ValueError(
                "RoutingResult contains duplicate unassigned BookItems"
            )

        overlap = (
            set(assigned_ids)
            & set(unassigned_ids)
        )

        if overlap:
            raise ValueError(
                "BookItem cannot be both assigned and unassigned: "
                f"{sorted(overlap)!r}"
            )

        calculated_routed_units = sum(
            assignment.amount_int
            for assignment in self.assignments
        )

        if calculated_routed_units != int(
            self.objective_routed_units
        ):
            raise ValueError(
                "objective_routed_units does not equal the total amount "
                "of RoutingAssignments"
            )

        calculated_semantic_utility = sum(
            assignment.semantic_score
            for assignment in self.assignments
        )

        if (
            calculated_semantic_utility
            != self.objective_semantic_utility
        ):
            raise ValueError(
                "objective_semantic_utility does not equal the sum of "
                "assignment semantic scores"
            )

        return self

    @property
    def routed_units_int(self) -> int:
        return int(
            self.objective_routed_units
        )

    def assignment_for(
        self,
        book_item_id: str,
    ) -> RoutingAssignment | None:
        for assignment in self.assignments:
            if (
                assignment.book_item_id
                == book_item_id
            ):
                return assignment

        return None