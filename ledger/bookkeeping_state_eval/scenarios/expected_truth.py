from __future__ import annotations

from collections import defaultdict
from dataclasses import dataclass, field
from types import MappingProxyType
from typing import Iterable, Mapping, Sequence

from bookkeeping_state.domain.reconciliations import Reconciliation
from bookkeeping_state.state.queries import BookkeepingQueries


@dataclass(frozen=True, slots=True)
class ExpectedReconciliation:
    """
    Independently authored expectation of one active reconciliation allocation.

    Canonicalized by canonical_key() for order-independent, artifact-ID-independent
    semantic comparison.
    """

    bank_allocations: Mapping[str, int]
    book_allocations: Mapping[str, int]

    def canonical_key(
        self,
    ) -> tuple[tuple[tuple[str, int], ...], tuple[tuple[str, int], ...]]:
        sorted_bank = tuple(sorted(self.bank_allocations.items()))
        sorted_book = tuple(sorted(self.book_allocations.items()))
        return (sorted_bank, sorted_book)

    @classmethod
    def one_to_one(
        cls,
        bank_item_id: str,
        book_item_id: str,
        amount: int,
    ) -> ExpectedReconciliation:
        return cls(
            bank_allocations={bank_item_id: amount},
            book_allocations={book_item_id: amount},
        )

    @classmethod
    def one_to_many(
        cls,
        bank_item_id: str,
        book_allocations: Mapping[str, int],
    ) -> ExpectedReconciliation:
        total = sum(book_allocations.values())
        return cls(
            bank_allocations={bank_item_id: total},
            book_allocations=dict(book_allocations),
        )

    @classmethod
    def many_to_one(
        cls,
        bank_allocations: Mapping[str, int],
        book_item_id: str,
    ) -> ExpectedReconciliation:
        total = sum(bank_allocations.values())
        return cls(
            bank_allocations=dict(bank_allocations),
            book_allocations={book_item_id: total},
        )


def extract_actual_pairwise_allocations(
    reconciliations: Iterable[Reconciliation],
) -> dict[tuple[str, str], int]:
    """
    Canonicalize active Reconciliation allocations across artifacts into a pairwise allocation matrix:
        (bank_item_id, book_item_id) -> total_allocated_amount
    """
    pairwise: dict[tuple[str, str], int] = defaultdict(int)
    for r in reconciliations:
        if len(r.bank_allocations) == 1:
            bank_id = r.bank_allocations[0].bank_item_id
            for book_alloc in r.book_allocations:
                pairwise[(bank_id, book_alloc.book_item_id)] += book_alloc.amount_int
        elif len(r.book_allocations) == 1:
            book_id = r.book_allocations[0].book_item_id
            for bank_alloc in r.bank_allocations:
                pairwise[(bank_alloc.bank_item_id, book_id)] += bank_alloc.amount_int
        else:
            raise ValueError(
                f"Reconciliation '{r.id}' has multiple bank and book allocations (N:M) "
                "and cannot be unambiguously decomposed into pairwise allocations"
            )
    return dict(pairwise)


@dataclass(frozen=True, slots=True)
class ExpectedTruth:
    """
    Independently authored expectation of closing bookkeeping truth.

    This expectation is completely detached from the runtime implementation.
    It contains no BookkeepingState, TransitionBatch, or hypothesis objects.

    Optionality semantics:
        None: dimension is unspecified for this scenario (validation skipped)
        {}: dimension is explicitly asserted to have zero active items
        mapping / sequence: exact declared active items and no unexpected extras
    """

    expected_routes: Mapping[str, str | None] | None = None
    expected_classifications: Mapping[str, str | None] | None = None
    expected_pairwise_allocations: Mapping[tuple[str, str], int] | None = None
    expected_reconciliations: tuple[ExpectedReconciliation, ...] | None = None
    expected_bank_remaining: Mapping[str, int] = field(
        default_factory=lambda: MappingProxyType({})
    )
    expected_book_remaining: Mapping[str, int] = field(
        default_factory=lambda: MappingProxyType({})
    )
    expected_fully_reconciled_bank_items: frozenset[str] | None = None
    expected_partially_reconciled_bank_items: frozenset[str] | None = None
    expected_unreconciled_bank_items: frozenset[str] | None = None
    expected_fully_reconciled_book_items: frozenset[str] | None = None
    expected_partially_reconciled_book_items: frozenset[str] | None = None
    expected_unreconciled_book_items: frozenset[str] | None = None
    expected_artifact_counts: Mapping[str, int] | None = None

    def __init__(
        self,
        *,
        expected_routes: Mapping[str, str | None] | None = None,
        expected_classifications: Mapping[str, str | None] | None = None,
        expected_pairwise_allocations: (
            Mapping[tuple[str, str], int | str] | None
        ) = None,
        expected_reconciliations: Sequence[ExpectedReconciliation] | None = None,
        expected_bank_remaining: Mapping[str, int] | None = None,
        expected_book_remaining: Mapping[str, int] | None = None,
        expected_fully_reconciled_bank_items: Iterable[str] | None = None,
        expected_partially_reconciled_bank_items: Iterable[str] | None = None,
        expected_unreconciled_bank_items: Iterable[str] | None = None,
        expected_fully_reconciled_book_items: Iterable[str] | None = None,
        expected_partially_reconciled_book_items: Iterable[str] | None = None,
        expected_unreconciled_book_items: Iterable[str] | None = None,
        expected_artifact_counts: Mapping[str, int] | None = None,
    ) -> None:
        object.__setattr__(
            self,
            "expected_routes",
            MappingProxyType(dict(expected_routes))
            if expected_routes is not None
            else None,
        )
        object.__setattr__(
            self,
            "expected_classifications",
            MappingProxyType(dict(expected_classifications))
            if expected_classifications is not None
            else None,
        )
        object.__setattr__(
            self,
            "expected_pairwise_allocations",
            MappingProxyType(
                {pair: int(amt) for pair, amt in expected_pairwise_allocations.items()}
            )
            if expected_pairwise_allocations is not None
            else None,
        )
        object.__setattr__(
            self,
            "expected_reconciliations",
            tuple(expected_reconciliations)
            if expected_reconciliations is not None
            else None,
        )
        object.__setattr__(
            self,
            "expected_bank_remaining",
            MappingProxyType(dict(expected_bank_remaining))
            if expected_bank_remaining is not None
            else MappingProxyType({}),
        )
        object.__setattr__(
            self,
            "expected_book_remaining",
            MappingProxyType(dict(expected_book_remaining))
            if expected_book_remaining is not None
            else MappingProxyType({}),
        )
        object.__setattr__(
            self,
            "expected_fully_reconciled_bank_items",
            frozenset(expected_fully_reconciled_bank_items)
            if expected_fully_reconciled_bank_items is not None
            else None,
        )
        object.__setattr__(
            self,
            "expected_partially_reconciled_bank_items",
            frozenset(expected_partially_reconciled_bank_items)
            if expected_partially_reconciled_bank_items is not None
            else None,
        )
        object.__setattr__(
            self,
            "expected_unreconciled_bank_items",
            frozenset(expected_unreconciled_bank_items)
            if expected_unreconciled_bank_items is not None
            else None,
        )
        object.__setattr__(
            self,
            "expected_fully_reconciled_book_items",
            frozenset(expected_fully_reconciled_book_items)
            if expected_fully_reconciled_book_items is not None
            else None,
        )
        object.__setattr__(
            self,
            "expected_partially_reconciled_book_items",
            frozenset(expected_partially_reconciled_book_items)
            if expected_partially_reconciled_book_items is not None
            else None,
        )
        object.__setattr__(
            self,
            "expected_unreconciled_book_items",
            frozenset(expected_unreconciled_book_items)
            if expected_unreconciled_book_items is not None
            else None,
        )
        object.__setattr__(
            self,
            "expected_artifact_counts",
            MappingProxyType(dict(expected_artifact_counts))
            if expected_artifact_counts is not None
            else None,
        )


@dataclass(frozen=True, slots=True)
class ExpectedTruthVerdict:
    """
    Result of comparing rehydrated closing truth against ExpectedTruth.
    """

    is_match: bool
    mismatches: tuple[str, ...]

    @classmethod
    def pass_verdict(cls) -> ExpectedTruthVerdict:
        return cls(is_match=True, mismatches=())

    @classmethod
    def fail_verdict(cls, mismatches: Sequence[str]) -> ExpectedTruthVerdict:
        return cls(is_match=False, mismatches=tuple(mismatches))


def validate_expected_truth(
    queries: BookkeepingQueries,
    expected: ExpectedTruth,
) -> ExpectedTruthVerdict:
    """
    Validate rehydrated durable bookkeeping state against independent expected truth.

    Compares semantic accounting facts (canonical routing, classifications,
    reconciliations, residuals, and item status sets), not raw artifact ordering.
    """
    mismatches: list[str] = []

    # 1. Active routing per BookItem (exact-set validation when specified)
    if expected.expected_routes is not None:
        actual_routes = queries.derived.active_routing_by_book_item
        actual_active_routes = {
            book_id: route.bank_account_id
            for book_id, route in actual_routes.items()
        }
        expected_active_routes = {
            book_id: acc_id
            for book_id, acc_id in expected.expected_routes.items()
            if acc_id is not None
        }
        expected_no_routes = {
            book_id
            for book_id, acc_id in expected.expected_routes.items()
            if acc_id is None
        }

        # Check explicitly unrouted items
        for book_id in sorted(expected_no_routes):
            if book_id in actual_active_routes:
                mismatches.append(
                    f"expected no active route for BookItem '{book_id}', "
                    f"actual route: '{actual_active_routes[book_id]}'"
                )

        # Check declared active routes
        for book_id, exp_acc_id in sorted(expected_active_routes.items()):
            act_acc_id = actual_active_routes.get(book_id)
            if act_acc_id != exp_acc_id:
                mismatches.append(
                    f"expected route:\n    {book_id} -> {exp_acc_id}\n"
                    f"actual:\n    {book_id} -> {act_acc_id}"
                )

        # Check unexpected extra active routes
        unexpected_routed = (
            set(actual_active_routes.keys()) - set(expected_active_routes.keys())
        )
        for book_id in sorted(unexpected_routed):
            if book_id not in expected_no_routes:
                mismatches.append(
                    f"unexpected active route for BookItem '{book_id}': "
                    f"routes to '{actual_active_routes[book_id]}'"
                )

    # 2. Active classification per BookItem (exact-set validation when specified)
    if expected.expected_classifications is not None:
        actual_classifications = queries.derived.active_classification_by_book_item
        actual_active_classifications = {
            book_id: cls.account_code
            for book_id, cls in actual_classifications.items()
        }
        expected_active_cls = {
            book_id: code
            for book_id, code in expected.expected_classifications.items()
            if code is not None
        }
        expected_no_cls = {
            book_id
            for book_id, code in expected.expected_classifications.items()
            if code is None
        }

        # Check explicitly unclassified items
        for book_id in sorted(expected_no_cls):
            if book_id in actual_active_classifications:
                mismatches.append(
                    f"expected no active classification for BookItem '{book_id}', "
                    f"actual classification: '{actual_active_classifications[book_id]}'"
                )

        # Check declared active classifications
        for book_id, exp_code in sorted(expected_active_cls.items()):
            act_code = actual_active_classifications.get(book_id)
            if act_code != exp_code:
                mismatches.append(
                    f"expected classification:\n    {book_id} -> {exp_code}\n"
                    f"actual:\n    {book_id} -> {act_code}"
                )

        # Check unexpected extra active classifications
        unexpected_classified = (
            set(actual_active_classifications.keys()) - set(expected_active_cls.keys())
        )
        for book_id in sorted(unexpected_classified):
            if book_id not in expected_no_cls:
                mismatches.append(
                    f"unexpected active classification for BookItem '{book_id}': "
                    f"classified to '{actual_active_classifications[book_id]}'"
                )

    # 3. BankItem remaining amounts
    for bank_id, expected_amt in expected.expected_bank_remaining.items():
        try:
            actual_amt = queries.bank_remaining_units(bank_id)
        except KeyError:
            mismatches.append(
                f"expected remaining amount for BankItem '{bank_id}': {expected_amt}, "
                "actual: BankItem does not exist in state"
            )
            continue
        if actual_amt != expected_amt:
            mismatches.append(
                f"expected remaining amount:\n    {bank_id} = {expected_amt}\n"
                f"actual:\n    {bank_id} = {actual_amt}"
            )

    # 4. BookItem remaining amounts
    for book_id, expected_amt in expected.expected_book_remaining.items():
        try:
            actual_amt = queries.book_remaining_units(book_id)
        except KeyError:
            mismatches.append(
                f"expected remaining amount for BookItem '{book_id}': {expected_amt}, "
                "actual: BookItem does not exist in state"
            )
            continue
        if actual_amt != expected_amt:
            mismatches.append(
                f"expected remaining amount:\n    {book_id} = {expected_amt}\n"
                f"actual:\n    {book_id} = {actual_amt}"
            )

    # 5. Semantic pairwise allocations
    if expected.expected_pairwise_allocations is not None:
        actual_pairwise = extract_actual_pairwise_allocations(
            queries.derived.active_reconciliations.values()
        )
        expected_pairwise = dict(expected.expected_pairwise_allocations)

        missing_pairs = set(expected_pairwise.keys()) - set(actual_pairwise.keys())
        for pair in sorted(missing_pairs):
            mismatches.append(
                f"missing expected pairwise allocation: Bank '{pair[0]}' -> Book '{pair[1]}' "
                f"(expected amount {expected_pairwise[pair]})"
            )

        for pair in sorted(set(expected_pairwise.keys()) & set(actual_pairwise.keys())):
            if actual_pairwise[pair] != expected_pairwise[pair]:
                mismatches.append(
                    f"pairwise allocation amount mismatch for {pair}:\n"
                    f"  expected: {expected_pairwise[pair]}\n"
                    f"  actual: {actual_pairwise[pair]}"
                )

        unexpected_pairs = set(actual_pairwise.keys()) - set(expected_pairwise.keys())
        for pair in sorted(unexpected_pairs):
            mismatches.append(
                f"unexpected extra pairwise allocation: Bank '{pair[0]}' -> Book '{pair[1]}' "
                f"(actual amount {actual_pairwise[pair]})"
            )

    # 6. Active reconciliations (canonical allocation comparison)
    if expected.expected_reconciliations is not None:
        actual_reconciliations = queries.derived.active_reconciliations.values()
        actual_canonical_set = {
            _reconciliation_to_canonical_key(r) for r in actual_reconciliations
        }
        expected_canonical_set = {
            r.canonical_key() for r in expected.expected_reconciliations
        }

        if actual_canonical_set != expected_canonical_set:
            missing = expected_canonical_set - actual_canonical_set
            unexpected = actual_canonical_set - expected_canonical_set
            msg_parts = ["reconciliation allocation mismatch:"]
            if missing:
                msg_parts.append(f"  missing expected allocations: {sorted(missing)}")
            if unexpected:
                msg_parts.append(f"  unexpected actual allocations: {sorted(unexpected)}")
            mismatches.append("\n".join(msg_parts))

    # 6. Reconciled / partially reconciled / unreconciled status sets
    derived = queries.derived
    if expected.expected_fully_reconciled_bank_items is not None:
        actual = set(derived.fully_reconciled_bank_item_ids)
        if actual != set(expected.expected_fully_reconciled_bank_items):
            mismatches.append(
                f"expected fully reconciled bank items: {sorted(expected.expected_fully_reconciled_bank_items)}, "
                f"actual: {sorted(actual)}"
            )

    if expected.expected_partially_reconciled_bank_items is not None:
        actual = set(derived.partially_reconciled_bank_item_ids)
        if actual != set(expected.expected_partially_reconciled_bank_items):
            mismatches.append(
                f"expected partially reconciled bank items: {sorted(expected.expected_partially_reconciled_bank_items)}, "
                f"actual: {sorted(actual)}"
            )

    if expected.expected_unreconciled_bank_items is not None:
        actual = set(derived.unreconciled_bank_item_ids)
        if actual != set(expected.expected_unreconciled_bank_items):
            mismatches.append(
                f"expected unreconciled bank items: {sorted(expected.expected_unreconciled_bank_items)}, "
                f"actual: {sorted(actual)}"
            )

    if expected.expected_fully_reconciled_book_items is not None:
        actual = set(derived.fully_reconciled_book_item_ids)
        if actual != set(expected.expected_fully_reconciled_book_items):
            mismatches.append(
                f"expected fully reconciled book items: {sorted(expected.expected_fully_reconciled_book_items)}, "
                f"actual: {sorted(actual)}"
            )

    if expected.expected_partially_reconciled_book_items is not None:
        actual = set(derived.partially_reconciled_book_item_ids)
        if actual != set(expected.expected_partially_reconciled_book_items):
            mismatches.append(
                f"expected partially reconciled book items: {sorted(expected.expected_partially_reconciled_book_items)}, "
                f"actual: {sorted(actual)}"
            )

    if expected.expected_unreconciled_book_items is not None:
        actual = set(derived.unreconciled_book_item_ids)
        if actual != set(expected.expected_unreconciled_book_items):
            mismatches.append(
                f"expected unreconciled book items: {sorted(expected.expected_unreconciled_book_items)}, "
                f"actual: {sorted(actual)}"
            )

    # 7. Durable artifact counts
    if expected.expected_artifact_counts is not None:
        state = queries._state
        artifact_count_map = {
            "reconciliations": len(state.reconciliations),
            "reconciliation_invalidations": len(state.reconciliation_invalidations),
            "routing_decisions": len(state.routing_decisions),
            "routing_invalidations": len(state.routing_invalidations),
            "classifications": len(state.classifications),
            "classification_invalidations": len(state.classification_invalidations),
        }
        for artifact_key, expected_count in expected.expected_artifact_counts.items():
            actual_count = artifact_count_map.get(artifact_key)
            if actual_count is None:
                mismatches.append(f"unknown artifact count key '{artifact_key}'")
            elif actual_count != expected_count:
                mismatches.append(
                    f"expected artifact count for '{artifact_key}': {expected_count}, "
                    f"actual: {actual_count}"
                )

    if mismatches:
        return ExpectedTruthVerdict.fail_verdict(mismatches)
    return ExpectedTruthVerdict.pass_verdict()


def _reconciliation_to_canonical_key(
    reconciliation: Reconciliation,
) -> tuple[tuple[tuple[str, int], ...], tuple[tuple[str, int], ...]]:
    bank_allocs = tuple(
        sorted(
            (alloc.bank_item_id, alloc.amount_int)
            for alloc in reconciliation.bank_allocations
        )
    )
    book_allocs = tuple(
        sorted(
            (alloc.book_item_id, alloc.amount_int)
            for alloc in reconciliation.book_allocations
        )
    )
    return (bank_allocs, book_allocs)
