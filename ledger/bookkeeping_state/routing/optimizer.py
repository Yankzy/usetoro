from __future__ import annotations

from collections import defaultdict
from dataclasses import dataclass

from ortools.sat.python import cp_model

from bookkeeping_state.routing.models import (
    RoutingAssignment,
    RoutingFeasibility,
    RoutingFeasibilityMatrix,
    RoutingResult,
    RoutingSemanticScoreMatrix,
    RoutingUnassignedReason,
    UnassignedRoutingItem,
)
from bookkeeping_state.routing.view import (
    RoutingBankItemView,
    RoutingBookItemView,
    RoutingView,
)

"""
This is stronger than the routing optimizer we initially described.

Consider:

BANK ACCOUNT A

BANK-1 = 100
BANK-2 = 50

BOOK-X = 100
BOOK-Y = 100

Locally:

BOOK-X x A = feasible
    witness BANK-1

BOOK-Y x A = feasible
    witness BANK-1

A naive matrix-level optimizer might say:

X -> A
Y -> A

because both pairs individually passed feasibility.

Our global optimizer introduces actual bank-capacity variables:

x[X,A,BANK-1]
x[Y,A,BANK-1]

constraint:

x[X,A,BANK-1]
+
x[Y,A,BANK-1]
<= 1

So the same BankItem cannot mathematically justify two routing decisions.

This does not mean routing has reconciled the transactions. The BankItem witness remains runtime proof of account plausibility only. Reconciliation later independently establishes actual durable matches.

The objective hierarchy is now:

1. ROUTE THE MOST MONEY POSSIBLE

2. among equally complete solutions,
   choose the highest semantic utility

3. among mathematically and semantically identical solutions,
   choose deterministically

So semantic preference can decide between two valid worlds, but it can never sacrifice hard feasibility or routed monetary coverage.
"""

class RoutingOptimizationError(RuntimeError):
    """Raised when the routing optimization problem cannot be solved safely."""


@dataclass(frozen=True, slots=True)
class _PairVariable:
    """
    Internal CP-SAT representation of one feasible:

        BookItem x BankAccount

    assignment.
    """

    book_item_id: str
    bank_account_id: str

    feasibility: RoutingFeasibility

    selected: cp_model.IntVar


@dataclass(frozen=True, slots=True)
class _BankUseVariable:
    """
    Internal CP-SAT representation of:

        this BankItem is consumed as feasibility capacity
        for this BookItem/account assignment.

    These variables are optimizer machinery only.

    They do NOT become Reconciliation allocations.
    """

    book_item_id: str
    bank_account_id: str
    bank_item_id: str

    selected: cp_model.IntVar


# ======================================================================
# Public API
# ======================================================================


def optimize_routing(
    *,
    view: RoutingView,
    feasibility: RoutingFeasibilityMatrix,
    semantic_scores: RoutingSemanticScoreMatrix,
    solver_run_id: str,
) -> RoutingResult:
    """
    Select the globally compatible set of routing assignments.

    Hard constraints:

        - only deterministically feasible pairs may be selected
        - each BookItem routes to at most one BankAccount
        - each BankItem may support at most one selected BookItem
        - selected BankItems must exactly sum to the BookItem remaining amount

    Lexicographic objective:

        1. maximize routed monetary units
        2. maximize semantic utility
        3. deterministically break remaining ties

    This is stronger than independently choosing the best account for each
    BookItem.

    Example:

        BOOK-A -> ACCOUNT-1 is feasible
        BOOK-B -> ACCOUNT-1 is feasible

    but both local feasibility witnesses require the same BankItem.

    Independent routing would incorrectly select both.

    This optimizer cannot.
    """

    if not solver_run_id:
        raise ValueError(
            "solver_run_id cannot be empty"
        )

    _validate_inputs(
        view=view,
        feasibility=feasibility,
        semantic_scores=semantic_scores,
    )

    if not view.book_items:
        return RoutingResult(
            state_revision=view.state_revision,
            solver_run_id=solver_run_id,
            assignments=(),
            unassigned=(),
            objective_routed_units="0",
            objective_semantic_utility=0,
        )

    model = cp_model.CpModel()

    book_items = {
        item.book_item_id: item
        for item in view.book_items
    }

    bank_items = _index_bank_items(
        view
    )

    # ------------------------------------------------------------------
    # Feasible pair variables
    #
    # y[i,a] = 1
    #     BookItem i is routed to BankAccount a.
    # ------------------------------------------------------------------

    pair_variables: dict[
        tuple[str, str],
        _PairVariable,
    ] = {}

    # ------------------------------------------------------------------
    # Bank witness variables
    #
    # x[i,a,b] = 1
    #     BankItem b is allocated by the optimizer as mathematical witness
    #     capacity supporting routing i to account a.
    #
    # Again: this is NOT a durable reconciliation allocation.
    # ------------------------------------------------------------------

    bank_use_variables: dict[
        tuple[str, str, str],
        _BankUseVariable,
    ] = {}

    feasible_entries = tuple(
        sorted(
            (
                entry
                for entry in feasibility.entries
                if entry.is_feasible
            ),
            key=lambda entry: (
                entry.book_item_id,
                entry.bank_account_id,
            ),
        )
    )

    for entry in feasible_entries:
        book_item = book_items.get(
            entry.book_item_id
        )

        if book_item is None:
            raise RoutingOptimizationError(
                f"Feasibility references unknown BookItem "
                f"{entry.book_item_id!r}"
            )

        pair_key = (
            entry.book_item_id,
            entry.bank_account_id,
        )

        pair_selected = model.NewBoolVar(
            _safe_variable_name(
                "route",
                *pair_key,
            )
        )

        pair_variables[pair_key] = (
            _PairVariable(
                book_item_id=entry.book_item_id,
                bank_account_id=(
                    entry.bank_account_id
                ),
                feasibility=entry,
                selected=pair_selected,
            )
        )

        compatible_use_vars: list[
            tuple[int, cp_model.IntVar]
        ] = []

        for bank_item_id in (
            entry.compatible_bank_item_ids
        ):
            bank_item = bank_items.get(
                bank_item_id
            )

            if bank_item is None:
                raise RoutingOptimizationError(
                    f"Feasibility pair {pair_key!r} references "
                    f"unknown BankItem {bank_item_id!r}"
                )

            if (
                bank_item.bank_account_id
                != entry.bank_account_id
            ):
                raise RoutingOptimizationError(
                    f"BankItem {bank_item_id!r} belongs to "
                    f"{bank_item.bank_account_id!r}, but feasibility "
                    f"claims it belongs to "
                    f"{entry.bank_account_id!r}"
                )

            use_selected = model.NewBoolVar(
                _safe_variable_name(
                    "use",
                    entry.book_item_id,
                    entry.bank_account_id,
                    bank_item_id,
                )
            )

            bank_use_variables[
                (
                    entry.book_item_id,
                    entry.bank_account_id,
                    bank_item_id,
                )
            ] = _BankUseVariable(
                book_item_id=entry.book_item_id,
                bank_account_id=(
                    entry.bank_account_id
                ),
                bank_item_id=bank_item_id,
                selected=use_selected,
            )

            # A BankItem cannot support an assignment that isn't selected.
            model.Add(
                use_selected
                <= pair_selected
            )

            compatible_use_vars.append(
                (
                    bank_item.remaining_amount_int,
                    use_selected,
                )
            )

        if not compatible_use_vars:
            raise RoutingOptimizationError(
                f"Feasible pair {pair_key!r} contains no compatible "
                "BankItems"
            )

        # --------------------------------------------------------------
        # Exact bank-side witness.
        #
        # pair_selected = 1:
        #
        #     selected BankItems must sum exactly to BookItem amount
        #
        # pair_selected = 0:
        #
        #     all BankItem use variables collapse to zero.
        # --------------------------------------------------------------

        model.Add(
            sum(
                amount * variable
                for amount, variable
                in compatible_use_vars
            )
            == (
                book_item.remaining_amount_int
                * pair_selected
            )
        )

    # ==================================================================
    # Each BookItem can route to at most one BankAccount.
    # ==================================================================

    pair_variables_by_book: dict[
        str,
        list[cp_model.IntVar],
    ] = defaultdict(list)

    for pair in pair_variables.values():
        pair_variables_by_book[
            pair.book_item_id
        ].append(
            pair.selected
        )

    for variables in (
        pair_variables_by_book.values()
    ):
        model.Add(
            sum(variables) <= 1
        )

    # ==================================================================
    # Each BankItem is globally exclusive.
    #
    # This is what prevents two locally feasible BookItems from reusing the
    # same bank-side capacity.
    # ==================================================================

    use_variables_by_bank_item: dict[
        str,
        list[cp_model.IntVar],
    ] = defaultdict(list)

    for use in bank_use_variables.values():
        use_variables_by_bank_item[
            use.bank_item_id
        ].append(
            use.selected
        )

    for variables in (
        use_variables_by_bank_item.values()
    ):
        model.Add(
            sum(variables) <= 1
        )

    # ==================================================================
    # Objective 1: maximize routed money
    # ==================================================================

    routed_units_expression = sum(
        (
            book_items[
                pair.book_item_id
            ].remaining_amount_int
            * pair.selected
        )
        for pair in pair_variables.values()
    )

    model.Maximize(
        routed_units_expression
    )

    solver = _new_solver()

    status = solver.Solve(
        model
    )

    _require_optimal_or_feasible(
        status=status,
        phase="routed-money optimization",
    )

    maximum_routed_units = int(
        solver.Value(
            routed_units_expression
        )
    )

    model.Add(
        routed_units_expression
        == maximum_routed_units
    )

    # ==================================================================
    # Objective 2: maximize semantic utility while preserving money optimum
    # ==================================================================

    semantic_expression = sum(
        (
            semantic_scores.score_for(
                book_item_id=(
                    pair.book_item_id
                ),
                bank_account_id=(
                    pair.bank_account_id
                ),
                default=0,
            )
            * pair.selected
        )
        for pair in pair_variables.values()
    )

    model.Maximize(
        semantic_expression
    )

    solver = _new_solver()

    status = solver.Solve(
        model
    )

    _require_optimal_or_feasible(
        status=status,
        phase="semantic-utility optimization",
    )

    maximum_semantic_utility = int(
        solver.Value(
            semantic_expression
        )
    )

    model.Add(
        semantic_expression
        == maximum_semantic_utility
    )

    # ==================================================================
    # Objective 3: deterministic pair selection
    #
    # Multiple mathematically and semantically identical global solutions may
    # remain.
    #
    # Fix pair decisions lexicographically so RoutingResult does not depend on
    # incidental CP-SAT search order.
    # ==================================================================

    ordered_pair_keys = tuple(
        sorted(
            pair_variables
        )
    )

    for pair_key in ordered_pair_keys:
        variable = (
            pair_variables[
                pair_key
            ].selected
        )

        model.Maximize(
            variable
        )

        solver = _new_solver()

        status = solver.Solve(
            model
        )

        _require_optimal_or_feasible(
            status=status,
            phase=(
                "deterministic routing-pair "
                f"tie-break {pair_key!r}"
            ),
        )

        chosen = int(
            solver.Value(
                variable
            )
        )

        model.Add(
            variable == chosen
        )

    # ==================================================================
    # Objective 4: deterministic bank witness selection
    #
    # A selected account may itself have multiple exact BankItem subsets.
    # Fix those choices as well.
    # ==================================================================

    ordered_use_keys = tuple(
        sorted(
            bank_use_variables
        )
    )

    for use_key in ordered_use_keys:
        variable = (
            bank_use_variables[
                use_key
            ].selected
        )

        model.Maximize(
            variable
        )

        solver = _new_solver()

        status = solver.Solve(
            model
        )

        _require_optimal_or_feasible(
            status=status,
            phase=(
                "deterministic bank-witness "
                f"tie-break {use_key!r}"
            ),
        )

        chosen = int(
            solver.Value(
                variable
            )
        )

        model.Add(
            variable == chosen
        )

    # ==================================================================
    # Final solve after all lexicographic decisions are fixed.
    # ==================================================================

    model.Maximize(
        0
    )

    solver = _new_solver()

    status = solver.Solve(
        model
    )

    _require_optimal_or_feasible(
        status=status,
        phase="final routing solution",
    )

    return _build_result(
        view=view,
        feasibility=feasibility,
        semantic_scores=semantic_scores,
        solver_run_id=solver_run_id,
        pair_variables=pair_variables,
        bank_use_variables=bank_use_variables,
        solver=solver,
        maximum_routed_units=(
            maximum_routed_units
        ),
        maximum_semantic_utility=(
            maximum_semantic_utility
        ),
    )


# ======================================================================
# Result construction
# ======================================================================


def _build_result(
    *,
    view: RoutingView,
    feasibility: RoutingFeasibilityMatrix,
    semantic_scores: RoutingSemanticScoreMatrix,
    solver_run_id: str,
    pair_variables: dict[
        tuple[str, str],
        _PairVariable,
    ],
    bank_use_variables: dict[
        tuple[str, str, str],
        _BankUseVariable,
    ],
    solver: cp_model.CpSolver,
    maximum_routed_units: int,
    maximum_semantic_utility: int,
) -> RoutingResult:
    book_items = {
        item.book_item_id: item
        for item in view.book_items
    }

    assignments: list[
        RoutingAssignment
    ] = []

    assigned_book_ids: set[
        str
    ] = set()

    for pair_key in sorted(
        pair_variables
    ):
        pair = pair_variables[
            pair_key
        ]

        if solver.Value(
            pair.selected
        ) != 1:
            continue

        book_item = book_items[
            pair.book_item_id
        ]

        witness_ids = tuple(
            sorted(
                use.bank_item_id
                for use_key, use
                in bank_use_variables.items()
                if (
                    use.book_item_id
                    == pair.book_item_id
                    and use.bank_account_id
                    == pair.bank_account_id
                    and solver.Value(
                        use.selected
                    )
                    == 1
                )
            )
        )

        if not witness_ids:
            raise RoutingOptimizationError(
                f"Selected routing pair {pair_key!r} "
                "has no global BankItem witness"
            )

        assignments.append(
            RoutingAssignment(
                book_item_id=(
                    pair.book_item_id
                ),

                bank_account_id=(
                    pair.bank_account_id
                ),

                amount_units=(
                    book_item.remaining_amount_units
                ),

                semantic_score=(
                    semantic_scores.score_for(
                        book_item_id=(
                            pair.book_item_id
                        ),
                        bank_account_id=(
                            pair.bank_account_id
                        ),
                        default=0,
                    )
                ),

                feasibility_witness_bank_item_ids=(
                    witness_ids
                ),
            )
        )

        assigned_book_ids.add(
            pair.book_item_id
        )

    assignments.sort(
        key=lambda assignment: (
            assignment.book_item_id,
            assignment.bank_account_id,
        )
    )

    # ------------------------------------------------------------------
    # Explain every unresolved BookItem that was not selected.
    # ------------------------------------------------------------------

    unassigned: list[
        UnassignedRoutingItem
    ] = []

    for book_item in sorted(
        view.book_items,
        key=lambda item: (
            item.book_item_id
        ),
    ):
        if (
            book_item.book_item_id
            in assigned_book_ids
        ):
            continue

        feasible_accounts = (
            feasibility.feasible_accounts_for(
                book_item.book_item_id
            )
        )

        if not feasible_accounts:
            reason = (
                RoutingUnassignedReason
                .NO_FEASIBLE_ACCOUNT
            )

            detail = (
                "No bank account has an exact deterministic "
                "bank-side feasibility witness."
            )

        else:
            reason = (
                RoutingUnassignedReason
                .GLOBAL_CAPACITY_CONFLICT
            )

            detail = (
                "At least one account is locally feasible, but no "
                "globally compatible solution can assign this "
                "BookItem without reusing bank-side capacity while "
                "preserving the higher-priority routing objective."
            )

        unassigned.append(
            UnassignedRoutingItem(
                book_item_id=(
                    book_item.book_item_id
                ),

                amount_units=(
                    book_item.remaining_amount_units
                ),

                reason=reason,

                detail=detail,
            )
        )

    result = RoutingResult(
        state_revision=view.state_revision,

        solver_run_id=solver_run_id,

        assignments=tuple(
            assignments
        ),

        unassigned=tuple(
            unassigned
        ),

        objective_routed_units=str(
            maximum_routed_units
        ),

        objective_semantic_utility=(
            maximum_semantic_utility
        ),
    )

    # ------------------------------------------------------------------
    # Defensive consistency check.
    # ------------------------------------------------------------------

    actual_routed = sum(
        assignment.amount_int
        for assignment in result.assignments
    )

    if actual_routed != maximum_routed_units:
        raise RoutingOptimizationError(
            "RoutingResult does not reproduce the CP-SAT routed-money "
            "objective"
        )

    actual_semantic = sum(
        assignment.semantic_score
        for assignment in result.assignments
    )

    if actual_semantic != maximum_semantic_utility:
        raise RoutingOptimizationError(
            "RoutingResult does not reproduce the CP-SAT semantic "
            "objective"
        )

    return result


# ======================================================================
# Input validation
# ======================================================================


def _validate_inputs(
    *,
    view: RoutingView,
    feasibility: RoutingFeasibilityMatrix,
    semantic_scores: RoutingSemanticScoreMatrix,
) -> None:
    if (
        feasibility.state_revision
        != view.state_revision
    ):
        raise RoutingOptimizationError(
            "RoutingView and RoutingFeasibilityMatrix belong to "
            "different state revisions"
        )

    if (
        semantic_scores.state_revision
        != view.state_revision
    ):
        raise RoutingOptimizationError(
            "RoutingView and RoutingSemanticScoreMatrix belong to "
            "different state revisions"
        )

    feasible_pairs = {
        (
            entry.book_item_id,
            entry.bank_account_id,
        )
        for entry in feasibility.entries
        if entry.is_feasible
    }

    score_pairs = {
        (
            score.book_item_id,
            score.bank_account_id,
        )
        for score in semantic_scores.scores
    }

    extra_scores = (
        score_pairs
        - feasible_pairs
    )

    if extra_scores:
        raise RoutingOptimizationError(
            "Semantic scores exist outside deterministic feasible set: "
            f"{sorted(extra_scores)!r}"
        )

    missing_scores = (
        feasible_pairs
        - score_pairs
    )

    if missing_scores:
        raise RoutingOptimizationError(
            "Deterministically feasible pairs are missing semantic "
            f"scores: {sorted(missing_scores)!r}"
        )


# ======================================================================
# View indexing
# ======================================================================


def _index_bank_items(
    view: RoutingView,
) -> dict[str, RoutingBankItemView]:
    items: dict[
        str,
        RoutingBankItemView,
    ] = {}

    for account in view.bank_accounts:
        for item in account.bank_items:
            existing = items.get(
                item.bank_item_id
            )

            if existing is not None:
                raise RoutingOptimizationError(
                    f"BankItem {item.bank_item_id!r} appears in "
                    "multiple RoutingBankAccountViews"
                )

            items[
                item.bank_item_id
            ] = item

    return items


# ======================================================================
# Solver
# ======================================================================


def _new_solver() -> cp_model.CpSolver:
    """
    Single-worker deterministic CP-SAT configuration.

    We intentionally avoid a time limit in the eval because returning an
    arbitrary incumbent would weaken the meaning of the routing artifact.

    Production can later introduce an explicit optimization budget and
    distinguish OPTIMAL from bounded FEASIBLE results if required.
    """

    solver = cp_model.CpSolver()

    solver.parameters.num_search_workers = 1
    solver.parameters.random_seed = 0

    return solver


def _require_optimal_or_feasible(
    *,
    status: int,
    phase: str,
) -> None:
    if status in {
        cp_model.OPTIMAL,
        cp_model.FEASIBLE,
    }:
        return

    if status == cp_model.INFEASIBLE:
        raise RoutingOptimizationError(
            f"Routing optimization became infeasible during {phase}"
        )

    if status == cp_model.MODEL_INVALID:
        raise RoutingOptimizationError(
            f"CP-SAT reported invalid routing model during {phase}"
        )

    raise RoutingOptimizationError(
        f"Routing optimization returned unsupported status "
        f"{status!r} during {phase}"
    )


# ======================================================================
# Misc
# ======================================================================


def _safe_variable_name(
    prefix: str,
    *parts: str,
) -> str:
    """
    Produce readable CP-SAT variable names without relying on artifact IDs
    containing solver-friendly characters.
    """

    normalized = []

    for part in parts:
        safe = "".join(
            character
            if (
                character.isalnum()
                or character == "_"
            )
            else "_"
            for character in part
        )

        normalized.append(
            safe
        )

    return (
        prefix
        + "__"
        + "__".join(
            normalized
        )
    )