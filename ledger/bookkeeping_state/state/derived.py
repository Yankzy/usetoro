from __future__ import annotations

from collections import defaultdict
from dataclasses import dataclass
from types import MappingProxyType
from typing import Mapping, TypeVar

from bookkeeping_state.domain.classifications import ClassificationDecision
from bookkeeping_state.domain.evidence import (
    BookItemEvidenceAssertion,
    BookItemEvidenceType,
)
from bookkeeping_state.domain.reconciliations import Reconciliation
from bookkeeping_state.domain.routing import RoutingDecision
from bookkeeping_state.state.bookkeeping_state import BookkeepingState

"""The important thing this gives us

We can now ask a hydrated state:

What classification is currently active?

Where is this BookItem currently routed?

Which reconciliations currently count?

How much of BOOK-17 remains unreconciled?

How much of BANK-4 remains?

Which items are partially reconciled?

Which items have never been routed?

Which items have never been classified?

Has persisted reality somehow overallocated an item?

without storing any of those answers.

For example:

BookItem amount = 100,000 units

REC-1 allocation = 30,000
REC-2 allocation = 20,000

BookkeepingState derives:

allocated = 50,000
remaining = 50,000
status = partially reconciled

Destroy the state and none of that needs to survive. The two reconciliation artifacts are sufficient to calculate it again.

One deliberate semantic decision here is also important:

Supersession does not automatically resurrect old truth.

If classification C2 superseded C1, subsequently invalidating C2 means there is currently no active classification. C1 does not silently return. Re-establishing that interpretation requires another explicit decision artifact.

That keeps history causal rather than magical.
"""

class DerivedStateError(RuntimeError):
    """
    Raised when authoritative artifacts cannot be reduced into one coherent
    current bookkeeping interpretation.

    Example:
        two non-invalidated, non-superseded classifications are simultaneously
        active for the same BookItem.
    """


@dataclass(frozen=True, slots=True)
class BookkeepingDerivedState:
    """
    Disposable facts computed from authoritative BookkeepingState artifacts.

    None of the values in this object need to be persisted. Given the same
    authoritative artifacts, this object must be reconstructable deterministically.
    """

    # ------------------------------------------------------------------
    # Current accepted decision state
    # ------------------------------------------------------------------

    active_routing_by_book_item: Mapping[str, RoutingDecision]
    active_classification_by_book_item: Mapping[str, ClassificationDecision]
    active_reconciliations: Mapping[str, Reconciliation]
    active_evidence_by_book_item_and_type: Mapping[
        tuple[str, BookItemEvidenceType], BookItemEvidenceAssertion
    ]

    # ------------------------------------------------------------------
    # Reconciliation arithmetic
    # ------------------------------------------------------------------

    bank_allocated_units: Mapping[str, int]
    book_allocated_units: Mapping[str, int]

    bank_remaining_units: Mapping[str, int]
    book_remaining_units: Mapping[str, int]

    # ------------------------------------------------------------------
    # Useful bookkeeping projections
    # ------------------------------------------------------------------

    fully_reconciled_bank_item_ids: tuple[str, ...]
    partially_reconciled_bank_item_ids: tuple[str, ...]
    unreconciled_bank_item_ids: tuple[str, ...]

    fully_reconciled_book_item_ids: tuple[str, ...]
    partially_reconciled_book_item_ids: tuple[str, ...]
    unreconciled_book_item_ids: tuple[str, ...]

    book_items_without_active_route: tuple[str, ...]
    book_items_without_active_classification: tuple[str, ...]

    # ------------------------------------------------------------------
    # Consistency diagnostics
    #
    # These are derived observations. validation.py will decide whether they
    # make the state invalid.
    # ------------------------------------------------------------------

    overallocated_bank_item_ids: tuple[str, ...]
    overallocated_book_item_ids: tuple[str, ...]

    def bank_remaining(self, bank_item_id: str) -> int:
        try:
            return self.bank_remaining_units[bank_item_id]
        except KeyError as exc:
            raise KeyError(
                f"Unknown BankItem {bank_item_id!r}"
            ) from exc

    def book_remaining(self, book_item_id: str) -> int:
        try:
            return self.book_remaining_units[book_item_id]
        except KeyError as exc:
            raise KeyError(
                f"Unknown BookItem {book_item_id!r}"
            ) from exc

    def is_bank_item_fully_reconciled(self, bank_item_id: str) -> bool:
        return self.bank_remaining(bank_item_id) == 0

    def is_book_item_fully_reconciled(self, book_item_id: str) -> bool:
        return self.book_remaining(book_item_id) == 0


def build_derived_state(
    state: BookkeepingState,
) -> BookkeepingDerivedState:
    """
    Compute the current bookkeeping interpretation from authoritative artifacts.

    This function:

    - performs no LLM reasoning
    - generates no hypotheses
    - persists nothing
    - mutates nothing
    - performs no routing or reconciliation search

    It only answers:

        "Given the artifacts currently accepted as truth, what is true now?"
    """

    active_routing = resolve_active_routing_decisions(state)
    active_classifications = resolve_active_classifications(state)
    active_reconciliations = resolve_active_reconciliations(state)
    active_evidence = resolve_active_evidence_assertions(state)

    bank_allocated: dict[str, int] = defaultdict(int)
    book_allocated: dict[str, int] = defaultdict(int)

    for reconciliation in active_reconciliations.values():
        for allocation in reconciliation.bank_allocations:
            bank_allocated[allocation.bank_item_id] += allocation.amount_int

        for allocation in reconciliation.book_allocations:
            book_allocated[allocation.book_item_id] += allocation.amount_int

    bank_remaining: dict[str, int] = {}
    book_remaining: dict[str, int] = {}

    fully_reconciled_bank: list[str] = []
    partially_reconciled_bank: list[str] = []
    unreconciled_bank: list[str] = []
    overallocated_bank: list[str] = []

    for bank_item_id, bank_item in state.bank_items.items():
        consumed = bank_allocated.get(bank_item_id, 0)
        remaining = bank_item.amount_int - consumed

        bank_remaining[bank_item_id] = remaining

        if remaining < 0:
            overallocated_bank.append(bank_item_id)
        elif remaining == 0:
            fully_reconciled_bank.append(bank_item_id)
        elif consumed > 0:
            partially_reconciled_bank.append(bank_item_id)
        else:
            unreconciled_bank.append(bank_item_id)

    fully_reconciled_book: list[str] = []
    partially_reconciled_book: list[str] = []
    unreconciled_book: list[str] = []
    overallocated_book: list[str] = []

    for book_item_id, book_item in state.book_items.items():
        consumed = book_allocated.get(book_item_id, 0)
        remaining = book_item.amount_int - consumed

        book_remaining[book_item_id] = remaining

        if remaining < 0:
            overallocated_book.append(book_item_id)
        elif remaining == 0:
            fully_reconciled_book.append(book_item_id)
        elif consumed > 0:
            partially_reconciled_book.append(book_item_id)
        else:
            unreconciled_book.append(book_item_id)

    book_items_without_route = sorted(
        book_item_id
        for book_item_id in state.book_items
        if book_item_id not in active_routing
    )

    book_items_without_classification = sorted(
        book_item_id
        for book_item_id in state.book_items
        if book_item_id not in active_classifications
    )

    return BookkeepingDerivedState(
        active_routing_by_book_item=_readonly(active_routing),
        active_classification_by_book_item=_readonly(
            active_classifications
        ),
        active_reconciliations=_readonly(active_reconciliations),
        active_evidence_by_book_item_and_type=_readonly(active_evidence),

        bank_allocated_units=_readonly(dict(bank_allocated)),
        book_allocated_units=_readonly(dict(book_allocated)),

        bank_remaining_units=_readonly(bank_remaining),
        book_remaining_units=_readonly(book_remaining),

        fully_reconciled_bank_item_ids=tuple(
            sorted(fully_reconciled_bank)
        ),
        partially_reconciled_bank_item_ids=tuple(
            sorted(partially_reconciled_bank)
        ),
        unreconciled_bank_item_ids=tuple(
            sorted(unreconciled_bank)
        ),

        fully_reconciled_book_item_ids=tuple(
            sorted(fully_reconciled_book)
        ),
        partially_reconciled_book_item_ids=tuple(
            sorted(partially_reconciled_book)
        ),
        unreconciled_book_item_ids=tuple(
            sorted(unreconciled_book)
        ),

        book_items_without_active_route=tuple(
            book_items_without_route
        ),
        book_items_without_active_classification=tuple(
            book_items_without_classification
        ),

        overallocated_bank_item_ids=tuple(
            sorted(overallocated_bank)
        ),
        overallocated_book_item_ids=tuple(
            sorted(overallocated_book)
        ),
    )


def resolve_active_routing_decisions(
    state: BookkeepingState,
) -> dict[str, RoutingDecision]:
    """
    Resolve exactly one current RoutingDecision per BookItem.

    Supersession semantics are intentionally non-resurrecting.

    Example:

        RD-1 active
        RD-2 supersedes RD-1
        RD-2 later invalidated

    RD-1 does NOT automatically become active again.

    Historical truth has already recorded that RD-1 was replaced. Restoring an
    earlier routing decision requires an explicit new decision artifact.
    """

    invalidated_ids = {
        invalidation.routing_decision_id
        for invalidation in state.routing_invalidations.values()
    }

    superseded_ids = {
        decision.supersedes_routing_decision_id
        for decision in state.routing_decisions.values()
        if decision.supersedes_routing_decision_id is not None
    }

    active_candidates = [
        decision
        for decision in state.routing_decisions.values()
        if decision.id not in invalidated_ids
        and decision.id not in superseded_ids
    ]

    by_book_item: dict[str, RoutingDecision] = {}

    for decision in sorted(
        active_candidates,
        key=lambda item: item.id,
    ):
        existing = by_book_item.get(decision.book_item_id)

        if existing is not None:
            raise DerivedStateError(
                "Bookkeeping state contains multiple active routing "
                f"decisions for BookItem {decision.book_item_id!r}: "
                f"{existing.id!r} and {decision.id!r}"
            )

        by_book_item[decision.book_item_id] = decision

    return by_book_item


def resolve_active_classifications(
    state: BookkeepingState,
) -> dict[str, ClassificationDecision]:
    """
    Resolve exactly one current ClassificationDecision per BookItem.

    Superseded decisions never become active again merely because a later
    decision is invalidated.
    """

    invalidated_ids = {
        invalidation.classification_id
        for invalidation in state.classification_invalidations.values()
    }

    superseded_ids = {
        classification.supersedes_classification_id
        for classification in state.classifications.values()
        if classification.supersedes_classification_id is not None
    }

    active_candidates = [
        classification
        for classification in state.classifications.values()
        if classification.id not in invalidated_ids
        and classification.id not in superseded_ids
    ]

    by_book_item: dict[str, ClassificationDecision] = {}

    for classification in sorted(
        active_candidates,
        key=lambda item: item.id,
    ):
        existing = by_book_item.get(classification.book_item_id)

        if existing is not None:
            raise DerivedStateError(
                "Bookkeeping state contains multiple active classifications "
                f"for BookItem {classification.book_item_id!r}: "
                f"{existing.id!r} and {classification.id!r}"
            )

        by_book_item[classification.book_item_id] = classification

    return by_book_item


def resolve_active_reconciliations(
    state: BookkeepingState,
) -> dict[str, Reconciliation]:
    """
    Resolve all currently active Reconciliation artifacts.

    Reconciliations are independently active or invalidated. Allocation
    conflicts and capacity violations are handled by state validation.
    """

    invalidated_ids = {
        invalidation.reconciliation_id
        for invalidation in state.reconciliation_invalidations.values()
    }

    return {
        reconciliation.id: reconciliation
        for reconciliation in sorted(
            state.reconciliations.values(),
            key=lambda item: item.id,
        )
        if reconciliation.id not in invalidated_ids
    }


def resolve_active_evidence_assertions(
    state: BookkeepingState,
) -> dict[tuple[str, BookItemEvidenceType], BookItemEvidenceAssertion]:
    """
    Resolve exactly one current BookItemEvidenceAssertion per (book_item_id, evidence_type).

    Dimension-isolated: an assertion on REFERENCE does not supersede or invalidate
    an assertion on COUNTERPARTY.

    Supersession semantics are intentionally non-resurrecting.
    """
    invalidated_ids = {
        invalidation.assertion_id
        for invalidation in state.book_item_evidence_invalidations.values()
    }

    superseded_ids = {
        assertion.supersedes_assertion_id
        for assertion in state.book_item_evidence_assertions.values()
        if assertion.supersedes_assertion_id is not None
    }

    active_candidates = [
        assertion
        for assertion in state.book_item_evidence_assertions.values()
        if assertion.id not in invalidated_ids
        and assertion.id not in superseded_ids
    ]

    by_dimension: dict[
        tuple[str, BookItemEvidenceType], BookItemEvidenceAssertion
    ] = {}

    for assertion in sorted(
        active_candidates,
        key=lambda item: item.id,
    ):
        key = (assertion.book_item_id, assertion.evidence_type)
        existing = by_dimension.get(key)

        if existing is not None:
            raise DerivedStateError(
                "Bookkeeping state contains multiple active evidence assertions "
                f"for BookItem {assertion.book_item_id!r} and type {assertion.evidence_type.value!r}: "
                f"{existing.id!r} and {assertion.id!r}"
            )

        by_dimension[key] = assertion

    return by_dimension


KeyT = TypeVar("KeyT")
ValueT = TypeVar("ValueT")


def _readonly(value: dict[KeyT, ValueT]) -> Mapping[KeyT, ValueT]:
    """
    Return a read-only view over a freshly-created derived dictionary.

    The backing dictionary is never exposed elsewhere.
    """
    return MappingProxyType(value)

