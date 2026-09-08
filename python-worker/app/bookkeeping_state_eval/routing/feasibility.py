from __future__ import annotations

from collections.abc import Iterable

from bookkeeping_state_eval.domain.enums import Direction
from bookkeeping_state_eval.routing.models import (
    RoutingCapacityBucket,
    RoutingFeasibility,
    RoutingFeasibilityMatrix,
    RoutingFeasibilityStatus,
)
from bookkeeping_state_eval.routing.view import (
    RoutingBankAccountView,
    RoutingBankItemView,
    RoutingBookItemView,
    RoutingView,
)

"""
The distinction between capacity and feasibility matters a lot.

Suppose an account contains:

70 MAD
80 MAD

and the BookItem is:

100 MAD

Naive routing might say:

available = 150
100 <= 150
therefore feasible

Wrong.

Our feasibility layer says:

70
80
70 + 80 = 150

no exact subset = 100

NO_EXACT_SUBSET

But if the account contains:

70
80
20

then:

80 + 20 = 100

FEASIBLE
witness = [BANK-80, BANK-20]

That witness gives us something valuable later when semantic scoring says an account "looks right." We already know the account is mathematically capable of explaining the amount.

One thing we should eventually centralize is the direction compatibility table, because reconciliation validation currently has the same accounting rule. It belongs at the domain layer rather than being independently defined by routing and reconciliation. We don't need to interrupt routing implementation for that yet.
"""


# ======================================================================
# Direction semantics
# ======================================================================


_DIRECTION_COMPATIBILITY: frozenset[
    tuple[Direction, Direction]
] = frozenset(
    {
        (
            Direction.BANK_OUTFLOW,
            Direction.BOOK_BANK_CREDIT,
        ),
        (
            Direction.BANK_INFLOW,
            Direction.BOOK_BANK_DEBIT,
        ),
        (
            Direction.OUTFLOW,
            Direction.OUTFLOW,
        ),
        (
            Direction.INFLOW,
            Direction.INFLOW,
        ),
    }
)


def directions_are_compatible(
    *,
    bank_direction: Direction,
    book_direction: Direction,
) -> bool:
    """
    Return whether bank-side and book-side directions are economically
    compatible.

    This is a hard deterministic routing constraint.

    Semantic scoring must never override it.
    """

    return (
        bank_direction,
        book_direction,
    ) in _DIRECTION_COMPATIBILITY


# ======================================================================
# Public API
# ======================================================================


def build_feasibility_matrix(
    view: RoutingView,
) -> RoutingFeasibilityMatrix:
    """
    Build the complete deterministic BookItem x BankAccount feasibility matrix.

    For every unresolved BookItem and every visible bank account, answer:

        Can this account mathematically explain the BookItem amount?

    Feasibility requires:

        1. bank-side capacity exists
        2. currency compatibility
        3. direction compatibility
        4. at least one exact subset of remaining BankItem amounts

    No semantic model is called here.

    No assignments are reserved here.

    A witness subset proves only local plausibility. Different BookItems may
    temporarily have overlapping witnesses. The global optimizer is responsible
    for resolving competition between them.
    """

    entries: list[RoutingFeasibility] = []

    for book_item in sorted(
        view.book_items,
        key=_book_item_sort_key,
    ):
        for bank_account in sorted(
            view.bank_accounts,
            key=_bank_account_sort_key,
        ):
            entries.append(
                evaluate_feasibility(
                    book_item=book_item,
                    bank_account=bank_account,
                )
            )

    return RoutingFeasibilityMatrix(
        state_revision=view.state_revision,
        entries=tuple(entries),
    )


def evaluate_feasibility(
    *,
    book_item: RoutingBookItemView,
    bank_account: RoutingBankAccountView,
) -> RoutingFeasibility:
    """
    Evaluate one BookItem x BankAccount pair.

    This function is pure and deterministic.
    """

    target = book_item.remaining_amount_int

    # ------------------------------------------------------------------
    # No bank-side capacity
    # ------------------------------------------------------------------

    if not bank_account.bank_items:
        return _infeasible(
            book_item=book_item,
            bank_account=bank_account,
            status=RoutingFeasibilityStatus.NO_BANK_ITEMS,
        )

    # ------------------------------------------------------------------
    # Account-level currency
    # ------------------------------------------------------------------

    if bank_account.currency != book_item.currency:
        return _infeasible(
            book_item=book_item,
            bank_account=bank_account,
            status=RoutingFeasibilityStatus.CURRENCY_MISMATCH,
        )

    # ------------------------------------------------------------------
    # Bank-item currency
    #
    # Normally hydration validation guarantees these agree with their account,
    # but routing remains defensive and uses only compatible items.
    # ------------------------------------------------------------------

    currency_compatible = tuple(
        item
        for item in bank_account.bank_items
        if item.currency == book_item.currency
    )

    if not currency_compatible:
        return _infeasible(
            book_item=book_item,
            bank_account=bank_account,
            status=RoutingFeasibilityStatus.CURRENCY_MISMATCH,
        )

    # ------------------------------------------------------------------
    # Direction
    # ------------------------------------------------------------------

    direction_compatible = tuple(
        sorted(
            (
                item
                for item in currency_compatible
                if directions_are_compatible(
                    bank_direction=item.direction,
                    book_direction=book_item.direction,
                )
            ),
            key=_bank_item_sort_key,
        )
    )

    if not direction_compatible:
        return _infeasible(
            book_item=book_item,
            bank_account=bank_account,
            status=RoutingFeasibilityStatus.DIRECTION_MISMATCH,
        )

    compatible_ids = tuple(
        item.bank_item_id
        for item in direction_compatible
    )

    # ------------------------------------------------------------------
    # Exact subset witness
    # ------------------------------------------------------------------

    witness_ids = find_exact_subset_witness(
        bank_items=direction_compatible,
        target_units=target,
    )

    if witness_ids is None:
        return RoutingFeasibility(
            book_item_id=book_item.book_item_id,
            bank_account_id=bank_account.bank_account_id,

            status=(
                RoutingFeasibilityStatus.NO_EXACT_SUBSET
            ),

            target_amount_units=str(target),

            compatible_bank_item_ids=compatible_ids,

            witness_bank_item_ids=(),

            witness_total_units="0",
        )

    return RoutingFeasibility(
        book_item_id=book_item.book_item_id,
        bank_account_id=bank_account.bank_account_id,

        status=RoutingFeasibilityStatus.FEASIBLE,

        target_amount_units=str(target),

        compatible_bank_item_ids=compatible_ids,

        witness_bank_item_ids=witness_ids,

        witness_total_units=str(target),
    )


def build_capacity_buckets(
    view: RoutingView,
) -> tuple[RoutingCapacityBucket, ...]:
    """
    Produce deterministic aggregate bank capacity by:

        BankAccount x BookItem direction

    These buckets are useful to the global routing optimizer as cheap capacity
    bounds.

    They are NOT substitutes for exact subset feasibility.

    Example:

        account total capacity = 150
        BookItem target = 100

    does not prove feasibility when available BankItems are:

        70 + 80

    Aggregate capacity says 150 exists.
    Exact subset feasibility correctly says 100 cannot be formed.
    """

    book_directions = tuple(
        sorted(
            {
                item.direction
                for item in view.book_items
            },
            key=lambda direction: direction.value,
        )
    )

    buckets: list[RoutingCapacityBucket] = []

    for account in sorted(
        view.bank_accounts,
        key=_bank_account_sort_key,
    ):
        for book_direction in book_directions:
            compatible = tuple(
                sorted(
                    (
                        bank_item
                        for bank_item in account.bank_items
                        if directions_are_compatible(
                            bank_direction=bank_item.direction,
                            book_direction=book_direction,
                        )
                    ),
                    key=_bank_item_sort_key,
                )
            )

            available = sum(
                item.remaining_amount_int
                for item in compatible
            )

            buckets.append(
                RoutingCapacityBucket(
                    bank_account_id=account.bank_account_id,
                    book_direction=book_direction,
                    available_units=str(available),
                    bank_item_ids=tuple(
                        item.bank_item_id
                        for item in compatible
                    ),
                )
            )

    return tuple(buckets)


# ======================================================================
# Exact subset solver
# ======================================================================


def find_exact_subset_witness(
    *,
    bank_items: Iterable[RoutingBankItemView],
    target_units: int,
) -> tuple[str, ...] | None:
    """
    Find one deterministic subset of BankItems whose remaining amounts sum
    exactly to target_units.

    This is a bounded subset-sum dynamic program.

    Important properties:

        exact integer arithmetic
        each BankItem can be used at most once
        no floating-point arithmetic
        deterministic witness selection
        input ordering does not change the result

    Returns:

        tuple of BankItem IDs
            when an exact subset exists

        None
            when no exact subset exists

    Complexity depends on the number of reachable sums up to target_units.
    This is suitable for the eval and mirrors the exact-feasibility role from
    the earlier routing architecture.

    If production workloads later make this frontier too large, the contract
    can remain unchanged while the implementation is replaced by CP-SAT or a
    specialized subset solver.
    """

    if target_units <= 0:
        raise ValueError(
            "target_units must be positive"
        )

    ordered_items = tuple(
        sorted(
            bank_items,
            key=_bank_item_sort_key,
        )
    )

    if not ordered_items:
        return None

    # ------------------------------------------------------------------
    # predecessor[reachable_sum] =
    #     (previous_sum, bank_item_id)
    #
    # Sum zero is the root and has no predecessor.
    # ------------------------------------------------------------------

    predecessor: dict[
        int,
        tuple[int, str] | None,
    ] = {
        0: None,
    }

    for item in ordered_items:
        amount = item.remaining_amount_int

        if amount <= 0:
            continue

        # An individual item larger than the target can never participate in
        # a positive exact subset for this target.
        if amount > target_units:
            continue

        # Snapshot existing sums so this BankItem cannot be consumed twice
        # during the same iteration.
        existing_sums = tuple(
            sorted(
                predecessor.keys(),
                reverse=True,
            )
        )

        additions: dict[
            int,
            tuple[int, str],
        ] = {}

        for current_sum in existing_sums:
            new_sum = (
                current_sum
                + amount
            )

            if new_sum > target_units:
                continue

            if (
                new_sum in predecessor
                or new_sum in additions
            ):
                # Keep the first deterministically discovered path.
                continue

            additions[new_sum] = (
                current_sum,
                item.bank_item_id,
            )

        predecessor.update(
            additions
        )

        if target_units in predecessor:
            return _reconstruct_witness(
                predecessor=predecessor,
                target_units=target_units,
            )

    return None


# ======================================================================
# Helpers
# ======================================================================


def _reconstruct_witness(
    *,
    predecessor: dict[
        int,
        tuple[int, str] | None,
    ],
    target_units: int,
) -> tuple[str, ...]:
    """
    Reconstruct the BankItem IDs that produced target_units.
    """

    witness: list[str] = []

    current_sum = target_units

    while current_sum != 0:
        edge = predecessor.get(
            current_sum
        )

        if edge is None:
            raise RuntimeError(
                "Subset-sum predecessor chain is incomplete"
            )

        previous_sum, bank_item_id = edge

        witness.append(
            bank_item_id
        )

        if previous_sum >= current_sum:
            raise RuntimeError(
                "Subset-sum predecessor chain did not decrease"
            )

        current_sum = previous_sum

    witness.reverse()

    return tuple(witness)


def _infeasible(
    *,
    book_item: RoutingBookItemView,
    bank_account: RoutingBankAccountView,
    status: RoutingFeasibilityStatus,
) -> RoutingFeasibility:
    if status == RoutingFeasibilityStatus.FEASIBLE:
        raise ValueError(
            "_infeasible cannot create FEASIBLE result"
        )

    return RoutingFeasibility(
        book_item_id=book_item.book_item_id,
        bank_account_id=bank_account.bank_account_id,

        status=status,

        target_amount_units=str(
            book_item.remaining_amount_int
        ),

        compatible_bank_item_ids=(),

        witness_bank_item_ids=(),

        witness_total_units="0",
    )


def _bank_item_sort_key(
    item: RoutingBankItemView,
) -> tuple:
    return (
        item.date,
        item.bank_item_id,
    )


def _book_item_sort_key(
    item: RoutingBookItemView,
) -> tuple:
    return (
        item.date,
        item.book_item_id,
    )


def _bank_account_sort_key(
    account: RoutingBankAccountView,
) -> str:
    return account.bank_account_id
