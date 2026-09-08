from __future__ import annotations

from collections.abc import Mapping
from datetime import date
from typing import Annotated, Protocol, runtime_checkable

from pydantic import (
    BaseModel,
    ConfigDict,
    Field,
    StringConstraints,
    model_validator,
)

from bookkeeping_state_eval.domain.enums import Direction
from bookkeeping_state_eval.domain.money import (
    AmountUnits,
    ResidualAmountUnits,
    amount_units_to_int,
)
from bookkeeping_state_eval.routing.models import (
    RoutingFeasibilityMatrix,
    RoutingSemanticScore,
    RoutingSemanticScoreMatrix,
)
from bookkeeping_state_eval.routing.view import (
    RoutingBankItemView,
    RoutingBookItemView,
    RoutingView,
)


Identifier = Annotated[
    str,
    StringConstraints(
        min_length=1,
        strip_whitespace=True,
    ),
]

"""
The control structure is now deliberately asymmetric:

deterministic feasibility
        |
        | only feasible pairs
        v
semantic scorer
        |
        | score 0..1000
        v
global optimizer

The semantic provider cannot create a candidate.

If deterministic code says:

BOOK-7 x BANK-B = NO_EXACT_SUBSET

there is literally no request sent asking the model to score BANK-B.

So even if an LLM would confidently say:

BANK-B looks perfect
score = 1000

that hypothesis cannot enter the system.
"""

class RoutingSemanticScoringError(RuntimeError):
    """Raised when semantic scoring violates the routing contract."""


# ======================================================================
# Bounded semantic request
# ======================================================================


class RoutingSemanticBankEvidence(BaseModel):
    """
    One bank-side semantic clue supplied to the semantic scorer.

    This is bounded runtime context, not bookkeeping truth.
    """

    model_config = ConfigDict(
        frozen=True,
        extra="forbid",
    )

    bank_item_id: Identifier

    date: date

    remaining_amount_units: ResidualAmountUnits

    direction: Direction

    description: str | None = None
    reference: str | None = None

    is_feasibility_witness: bool = False

    @property
    def remaining_amount_int(self) -> int:
        return int(
            self.remaining_amount_units
        )


class RoutingSemanticCandidate(BaseModel):
    """
    One deterministically feasible BookItem/BankAccount pair presented to a
    semantic provider.

    The provider's job is only:

        "Among mathematically valid accounts, how semantically plausible is
        this account for this BookItem?"

    The provider cannot invent new accounts or override feasibility.
    """

    model_config = ConfigDict(
        frozen=True,
        extra="forbid",
    )

    book_item_id: Identifier
    bank_account_id: Identifier

    target_amount_units: AmountUnits

    book_date: date
    book_direction: Direction
    currency: str = Field(
        ...,
        min_length=3,
        max_length=3,
    )

    book_description: str | None = None
    book_reference: str | None = None

    counterparty_name: str | None = None
    counterparty_aliases: tuple[str, ...] = ()

    evidence_document_ids: tuple[
        Identifier,
        ...
    ] = ()

    bank_account_name: str = Field(
        ...,
        min_length=1,
    )

    institution_name: str | None = None

    bank_evidence: tuple[
        RoutingSemanticBankEvidence,
        ...
    ]

    @model_validator(mode="after")
    def validate_candidate(
        self,
    ) -> "RoutingSemanticCandidate":
        if not self.bank_evidence:
            raise ValueError(
                "RoutingSemanticCandidate requires bank evidence"
            )

        evidence_ids = [
            evidence.bank_item_id
            for evidence in self.bank_evidence
        ]

        if len(set(evidence_ids)) != len(
            evidence_ids
        ):
            raise ValueError(
                "RoutingSemanticCandidate contains duplicate BankItems"
            )

        witness = tuple(
            evidence
            for evidence in self.bank_evidence
            if evidence.is_feasibility_witness
        )

        if not witness:
            raise ValueError(
                "RoutingSemanticCandidate requires deterministic "
                "feasibility witness evidence"
            )

        witness_total = sum(
            evidence.remaining_amount_int
            for evidence in witness
        )

        if witness_total != self.target_amount_int:
            raise ValueError(
                "Feasibility witness evidence does not exactly equal "
                "the BookItem target amount"
            )

        return self

    @property
    def target_amount_int(self) -> int:
        return amount_units_to_int(
            self.target_amount_units
        )


class RoutingSemanticScoringRequest(BaseModel):
    """
    Complete bounded request sent to one semantic scoring invocation.
    """

    model_config = ConfigDict(
        frozen=True,
        extra="forbid",
    )

    state_revision: int = Field(
        ...,
        ge=0,
    )

    candidates: tuple[
        RoutingSemanticCandidate,
        ...
    ]

    @model_validator(mode="after")
    def validate_unique_candidates(
        self,
    ) -> "RoutingSemanticScoringRequest":
        seen: set[
            tuple[str, str]
        ] = set()

        for candidate in self.candidates:
            pair = (
                candidate.book_item_id,
                candidate.bank_account_id,
            )

            if pair in seen:
                raise ValueError(
                    "Duplicate semantic candidate "
                    f"{pair!r}"
                )

            seen.add(pair)

        return self


# ======================================================================
# Provider contract
# ======================================================================


class RoutingProviderScore(BaseModel):
    """
    Raw score returned by a semantic provider.

    Score is integer 0..1000 so the optimizer never needs floating point.
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


class RoutingSemanticScoringResponse(BaseModel):
    """
    Raw provider response before deterministic validation.
    """

    model_config = ConfigDict(
        frozen=True,
        extra="forbid",
    )

    state_revision: int = Field(
        ...,
        ge=0,
    )

    model_run_id: Identifier

    scores: tuple[
        RoutingProviderScore,
        ...
    ]

    @model_validator(mode="after")
    def validate_unique_scores(
        self,
    ) -> "RoutingSemanticScoringResponse":
        seen: set[
            tuple[str, str]
        ] = set()

        for score in self.scores:
            pair = (
                score.book_item_id,
                score.bank_account_id,
            )

            if pair in seen:
                raise ValueError(
                    "Semantic provider returned duplicate score "
                    f"for pair {pair!r}"
                )

            seen.add(pair)

        return self


@runtime_checkable
class RoutingSemanticScoreProvider(Protocol):
    """
    Provider boundary for semantic account scoring.

    Production may implement this with an LLM.

    Evals may implement it deterministically.

    Routing orchestration depends only on this contract.
    """

    def score(
        self,
        request: RoutingSemanticScoringRequest,
    ) -> RoutingSemanticScoringResponse:
        ...


# ======================================================================
# Public scorer
# ======================================================================


def score_feasible_pairs(
    *,
    view: RoutingView,
    feasibility: RoutingFeasibilityMatrix,
    provider: RoutingSemanticScoreProvider,
    max_bank_evidence_items: int = 20,
) -> RoutingSemanticScoreMatrix:
    """
    Score every and only deterministically feasible routing pair.

    The function fails closed if the provider:

        scores an infeasible pair
        omits a feasible pair
        adds a new pair
        returns duplicate pairs
        returns a result for another state revision

    Semantic output therefore cannot expand the deterministic feasible set.
    """

    if (
        feasibility.state_revision
        != view.state_revision
    ):
        raise RoutingSemanticScoringError(
            "Routing feasibility matrix and RoutingView belong to "
            "different state revisions"
        )

    candidates = build_semantic_candidates(
        view=view,
        feasibility=feasibility,
        max_bank_evidence_items=(
            max_bank_evidence_items
        ),
    )

    if not candidates:
        return RoutingSemanticScoreMatrix(
            state_revision=view.state_revision,
            scores=(),
        )

    request = RoutingSemanticScoringRequest(
        state_revision=view.state_revision,
        candidates=candidates,
    )

    response = provider.score(
        request
    )

    if response.state_revision != view.state_revision:
        raise RoutingSemanticScoringError(
            "Semantic provider returned scores for stale or unrelated "
            f"state revision {response.state_revision}; "
            f"expected {view.state_revision}"
        )

    expected_pairs = {
        (
            candidate.book_item_id,
            candidate.bank_account_id,
        )
        for candidate in candidates
    }

    returned_pairs = {
        (
            score.book_item_id,
            score.bank_account_id,
        )
        for score in response.scores
    }

    missing = (
        expected_pairs
        - returned_pairs
    )

    extra = (
        returned_pairs
        - expected_pairs
    )

    if missing:
        raise RoutingSemanticScoringError(
            "Semantic provider omitted feasible routing pairs: "
            f"{sorted(missing)!r}"
        )

    if extra:
        raise RoutingSemanticScoringError(
            "Semantic provider attempted to score pairs outside the "
            "deterministic feasible set: "
            f"{sorted(extra)!r}"
        )

    scores = tuple(
        RoutingSemanticScore(
            book_item_id=score.book_item_id,
            bank_account_id=score.bank_account_id,
            score=score.score,
            rationale=score.rationale,
            model_run_id=response.model_run_id,
        )
        for score in sorted(
            response.scores,
            key=lambda value: (
                value.book_item_id,
                value.bank_account_id,
            ),
        )
    )

    return RoutingSemanticScoreMatrix(
        state_revision=view.state_revision,
        scores=scores,
    )


# ======================================================================
# Candidate builder
# ======================================================================


def build_semantic_candidates(
    *,
    view: RoutingView,
    feasibility: RoutingFeasibilityMatrix,
    max_bank_evidence_items: int = 20,
) -> tuple[RoutingSemanticCandidate, ...]:
    """
    Construct bounded semantic context for feasible pairs only.

    Witness BankItems are always included.

    Additional compatible BankItems are selected by proximity to the BookItem
    date until max_bank_evidence_items is reached.

    If an exact witness itself contains more items than the configured limit,
    all witness items are retained because removing one would destroy the
    deterministic explanation of feasibility.
    """

    if max_bank_evidence_items <= 0:
        raise ValueError(
            "max_bank_evidence_items must be positive"
        )

    if (
        feasibility.state_revision
        != view.state_revision
    ):
        raise RoutingSemanticScoringError(
            "Cannot build semantic candidates from mismatched revisions"
        )

    candidates: list[
        RoutingSemanticCandidate
    ] = []

    for entry in feasibility.entries:
        if not entry.is_feasible:
            continue

        book_item = view.book_item(
            entry.book_item_id
        )

        if book_item is None:
            raise RoutingSemanticScoringError(
                f"Feasibility references missing BookItem "
                f"{entry.book_item_id!r}"
            )

        bank_account = view.bank_account(
            entry.bank_account_id
        )

        if bank_account is None:
            raise RoutingSemanticScoringError(
                f"Feasibility references missing BankAccount "
                f"{entry.bank_account_id!r}"
            )

        bank_items_by_id = {
            item.bank_item_id: item
            for item in bank_account.bank_items
        }

        witness_ids = set(
            entry.witness_bank_item_ids
        )

        compatible_items: list[
            RoutingBankItemView
        ] = []

        for bank_item_id in (
            entry.compatible_bank_item_ids
        ):
            item = bank_items_by_id.get(
                bank_item_id
            )

            if item is None:
                raise RoutingSemanticScoringError(
                    f"Feasibility references BankItem "
                    f"{bank_item_id!r} not present in account "
                    f"{bank_account.bank_account_id!r}"
                )

            compatible_items.append(
                item
            )

        selected = _select_bank_evidence(
            book_item=book_item,
            compatible_items=compatible_items,
            witness_ids=witness_ids,
            max_items=max_bank_evidence_items,
        )

        candidates.append(
            RoutingSemanticCandidate(
                book_item_id=book_item.book_item_id,
                bank_account_id=(
                    bank_account.bank_account_id
                ),

                target_amount_units=(
                    book_item.remaining_amount_units
                ),

                book_date=book_item.date,
                book_direction=book_item.direction,
                currency=book_item.currency,

                book_description=(
                    book_item.description
                ),

                book_reference=(
                    book_item.reference
                ),

                counterparty_name=(
                    book_item.counterparty_name
                ),

                counterparty_aliases=(
                    book_item.counterparty_aliases
                ),

                evidence_document_ids=(
                    book_item.evidence_document_ids
                ),

                bank_account_name=(
                    bank_account.name
                ),

                institution_name=(
                    bank_account.institution_name
                ),

                bank_evidence=tuple(
                    RoutingSemanticBankEvidence(
                        bank_item_id=item.bank_item_id,
                        date=item.date,
                        remaining_amount_units=(
                            item.remaining_amount_units
                        ),
                        direction=item.direction,
                        description=item.description,
                        reference=item.reference,
                        is_feasibility_witness=(
                            item.bank_item_id
                            in witness_ids
                        ),
                    )
                    for item in selected
                ),
            )
        )

    candidates.sort(
        key=lambda candidate: (
            candidate.book_item_id,
            candidate.bank_account_id,
        )
    )

    return tuple(candidates)


# ======================================================================
# Deterministic providers for eval/fallback
# ======================================================================


class StaticRoutingSemanticScoreProvider:
    """
    Deterministic provider useful for tests, evals, and simulated semantic
    scoring.

    Any pair absent from `scores` receives default_score.
    """

    __slots__ = (
        "_scores",
        "_default_score",
        "_model_run_id",
    )

    def __init__(
        self,
        *,
        scores: Mapping[
            tuple[str, str],
            int,
        ],
        default_score: int = 0,
        model_run_id: str = "static-routing-scorer",
    ) -> None:
        if not 0 <= default_score <= 1000:
            raise ValueError(
                "default_score must be between 0 and 1000"
            )

        if not model_run_id:
            raise ValueError(
                "model_run_id cannot be empty"
            )

        normalized: dict[
            tuple[str, str],
            int,
        ] = {}

        for pair, score in scores.items():
            if len(pair) != 2:
                raise ValueError(
                    "Static routing score key must be "
                    "(book_item_id, bank_account_id)"
                )

            if not 0 <= score <= 1000:
                raise ValueError(
                    f"Static score for {pair!r} must be between "
                    "0 and 1000"
                )

            normalized[pair] = score

        self._scores = normalized
        self._default_score = default_score
        self._model_run_id = model_run_id

    def score(
        self,
        request: RoutingSemanticScoringRequest,
    ) -> RoutingSemanticScoringResponse:
        scores = tuple(
            RoutingProviderScore(
                book_item_id=candidate.book_item_id,
                bank_account_id=candidate.bank_account_id,

                score=self._scores.get(
                    (
                        candidate.book_item_id,
                        candidate.bank_account_id,
                    ),
                    self._default_score,
                ),

                rationale=(
                    "Deterministic static routing score"
                ),
            )
            for candidate in request.candidates
        )

        return RoutingSemanticScoringResponse(
            state_revision=request.state_revision,
            model_run_id=self._model_run_id,
            scores=scores,
        )


class ZeroRoutingSemanticScoreProvider(
    StaticRoutingSemanticScoreProvider
):
    """
    Semantic-neutral fallback.

    The optimizer will then rely entirely on hard feasibility and its
    deterministic primary objectives.
    """

    def __init__(self) -> None:
        super().__init__(
            scores={},
            default_score=0,
            model_run_id="zero-routing-scorer",
        )


# ======================================================================
# Helpers
# ======================================================================


def _select_bank_evidence(
    *,
    book_item: RoutingBookItemView,
    compatible_items: list[
        RoutingBankItemView
    ],
    witness_ids: set[str],
    max_items: int,
) -> tuple[RoutingBankItemView, ...]:
    items_by_id = {
        item.bank_item_id: item
        for item in compatible_items
    }

    missing_witness = (
        witness_ids
        - set(items_by_id)
    )

    if missing_witness:
        raise RoutingSemanticScoringError(
            "Feasibility witness contains BankItems outside the "
            "compatible set: "
            f"{sorted(missing_witness)!r}"
        )

    # Witness always comes first and is always retained.
    witness = sorted(
        (
            items_by_id[item_id]
            for item_id in witness_ids
        ),
        key=lambda item: (
            abs(
                (
                    item.date
                    - book_item.date
                ).days
            ),
            item.date,
            item.bank_item_id,
        ),
    )

    non_witness = sorted(
        (
            item
            for item in compatible_items
            if item.bank_item_id not in witness_ids
        ),
        key=lambda item: (
            abs(
                (
                    item.date
                    - book_item.date
                ).days
            ),
            item.date,
            item.bank_item_id,
        ),
    )

    effective_limit = max(
        max_items,
        len(witness),
    )

    selected = (
        witness
        + non_witness[
            : max(
                0,
                effective_limit
                - len(witness),
            )
        ]
    )

    return tuple(selected)