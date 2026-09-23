from __future__ import annotations

from dataclasses import dataclass
from datetime import date
from typing import Sequence

from bookkeeping_state.domain.bank import BankItem
from bookkeeping_state.domain.residual_bank_classifications import (
    ResidualBankClassificationDecision,
    ResidualBankClassificationStatus,
)
from bookkeeping_state.state.queries import BookkeepingQueries


class ResidualBankInvariantCorruptionError(Exception):
    """
    Raised when an economically residual BankItem has a durably posted classification.

    In a healthy accounting state, posting a residual classification creates a
    balancing cash movement and directly reconciles the BankItem for its residual
    units, driving remaining_units to 0. A BankItem that remains economically
    residual despite having a posted classification represents ledger corruption.
    """


@dataclass(frozen=True, slots=True)
class ResidualSemanticEvaluationCandidate:
    """
    Candidate bank movement needing semantic account evaluation by Go ASE.

    Contains the BankItem, its current residual units, and the ID of any
    invalidated decision tip that must be superseded by the new decision.
    """

    bank_item: BankItem
    remaining_units: int
    supersedes_decision_id: str | None = None


def residual_bank_items_needing_semantic_evaluation(
    queries: BookkeepingQueries,
) -> tuple[ResidualSemanticEvaluationCandidate, ...]:
    """
    Return residual BankItems that require semantic evaluation by Go ASE.

    Evaluates every current economic residual BankItem against its durable history:
    - CASE A: No residual classification history -> eligible (supersedes_decision_id=None).
    - CASE B: Active HOLD exists -> NOT eligible (leave unresolved).
    - CASE C: Active unposted CLASSIFIED exists -> NOT eligible (posting work only).
    - CASE D: Latest tip is invalidated and there is no active decision -> eligible
              (supersedes_decision_id=latest.id).
    - CASE E: Economically residual AND relevant latest/current decision has durable
              posting provenance -> INVARIANT CORRUPTION (fails loudly).
    - CASE F: Non-residual items are not returned by residual_unmatched_bank_items().
    """
    candidates: list[ResidualSemanticEvaluationCandidate] = []

    for bank_item, remaining_units in queries.residual_unmatched_bank_items():
        latest = queries.latest_residual_bank_classification(bank_item.id)
        if latest is not None and queries.is_residual_bank_classification_posted(latest.id):
            raise ResidualBankInvariantCorruptionError(
                f"Invariant corruption (Case E): BankItem {bank_item.id!r} has positive "
                f"remaining units ({remaining_units}) despite posted decision {latest.id!r}."
            )

        active = queries.active_residual_bank_classification(bank_item.id)
        if active is not None:
            # Case B (active HOLD) or Case C (active CLASSIFIED)
            continue

        if latest is not None:
            # Case D: Latest tip was invalidated and no active decision exists
            candidates.append(
                ResidualSemanticEvaluationCandidate(
                    bank_item=bank_item,
                    remaining_units=remaining_units,
                    supersedes_decision_id=latest.id,
                )
            )
        else:
            # Case A: Fresh residual item with no classification history
            candidates.append(
                ResidualSemanticEvaluationCandidate(
                    bank_item=bank_item,
                    remaining_units=remaining_units,
                    supersedes_decision_id=None,
                )
            )

    return tuple(candidates)


def get_unposted_classified_residual_decisions(
    queries: BookkeepingQueries,
) -> tuple[ResidualBankClassificationDecision, ...]:
    """
    Return active, unposted CLASSIFIED decisions for current economic residuals.

    Strict semantics:
    1. Iterates strictly over queries.residual_unmatched_bank_items().
    2. If any residual item has a posted decision in history, raises
       ResidualBankInvariantCorruptionError (Case E).
    3. Selects the active chain tip if and only if:
       - active.status == ResidualBankClassificationStatus.CLASSIFIED
       - not queries.is_residual_bank_classification_posted(active.id)
    4. Orders deterministically by (bank_item.date, bank_item.id, active.id).
    """
    unposted: list[ResidualBankClassificationDecision] = []

    for bank_item, remaining_units in queries.residual_unmatched_bank_items():
        latest = queries.latest_residual_bank_classification(bank_item.id)
        if latest is not None and queries.is_residual_bank_classification_posted(latest.id):
            raise ResidualBankInvariantCorruptionError(
                f"Invariant corruption (Case E): BankItem {bank_item.id!r} has positive "
                f"remaining units ({remaining_units}) despite posted decision {latest.id!r}."
            )

        active = queries.active_residual_bank_classification(bank_item.id)
        if active is not None and active.status == ResidualBankClassificationStatus.CLASSIFIED:
            assert not queries.is_residual_bank_classification_posted(active.id)
            unposted.append(active)

    def _sort_key(d: ResidualBankClassificationDecision) -> tuple[date, str, str]:
        bi = queries.bank_item(d.bank_item_id)
        d_date = bi.date if bi is not None else date.min
        return (d_date, d.bank_item_id, d.id)

    unposted.sort(key=_sort_key)
    return tuple(unposted)
