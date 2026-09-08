from __future__ import annotations

from dataclasses import dataclass
from datetime import datetime

from bookkeeping_state_eval.domain.commands import (
    CommandSource,
    CreateRoutingDecisionCommand,
)
from bookkeeping_state_eval.domain.routing import (
    RoutingDecisionSource,
)
from bookkeeping_state_eval.routing.feasibility import (
    build_feasibility_matrix,
)
from bookkeeping_state_eval.routing.models import (
    RoutingFeasibilityMatrix,
    RoutingResult,
    RoutingSemanticScoreMatrix,
)
from bookkeeping_state_eval.routing.optimizer import (
    optimize_routing,
)
from bookkeeping_state_eval.routing.scorer import (
    RoutingSemanticScoreProvider,
    score_feasible_pairs,
)
from bookkeeping_state_eval.routing.view import (
    RoutingView,
    build_routing_view,
)
from bookkeeping_state_eval.state.queries import (
    BookkeepingQueries,
)
from bookkeeping_state_eval.transitions.batch import (
    TransitionBatch,
    TransitionBatchError,
)


class RoutingServiceError(RuntimeError):
    """Raised when the routing pipeline violates its internal contract."""


@dataclass(frozen=True, slots=True)
class RoutingPlan:
    """
    Complete runtime artifact produced by one routing run.

    Nothing in this object is durable bookkeeping truth.

    It records the full reasoning pipeline:

        bounded RoutingView
        deterministic feasibility
        semantic scores
        global optimization result
        proposed transition commands

    Only the commands may later cross TransitionEngine and become durable
    RoutingDecision artifacts.
    """

    view: RoutingView

    feasibility: RoutingFeasibilityMatrix

    semantic_scores: RoutingSemanticScoreMatrix

    result: RoutingResult

    commands: tuple[
        CreateRoutingDecisionCommand,
        ...
    ]

    def __post_init__(self) -> None:
        revision = self.view.state_revision

        if self.feasibility.state_revision != revision:
            raise ValueError(
                "RoutingPlan feasibility revision differs from RoutingView"
            )

        if self.semantic_scores.state_revision != revision:
            raise ValueError(
                "RoutingPlan semantic-score revision differs from RoutingView"
            )

        if self.result.state_revision != revision:
            raise ValueError(
                "RoutingPlan result revision differs from RoutingView"
            )

        assignment_pairs = {
            (
                assignment.book_item_id,
                assignment.bank_account_id,
            )
            for assignment in self.result.assignments
        }

        command_pairs = {
            (
                command.book_item_id,
                command.bank_account_id,
            )
            for command in self.commands
        }

        if assignment_pairs != command_pairs:
            raise ValueError(
                "RoutingPlan commands do not exactly represent "
                "RoutingResult assignments"
            )

        if len(self.commands) != len(
            self.result.assignments
        ):
            raise ValueError(
                "RoutingPlan must contain exactly one command per assignment"
            )

        for command in self.commands:
            if command.expected_state_revision != revision:
                raise ValueError(
                    "Routing command was generated against incorrect "
                    "state revision"
                )

            if command.solver_run_id != self.result.solver_run_id:
                raise ValueError(
                    "Routing command solver_run_id differs from RoutingResult"
                )

    @property
    def state_revision(self) -> int:
        return self.view.state_revision

    @property
    def solver_run_id(self) -> str:
        return self.result.solver_run_id

    @property
    def has_assignments(self) -> bool:
        return bool(
            self.result.assignments
        )

    def to_batch(
        self,
        *,
        batch_id: str | None = None,
    ) -> TransitionBatch:
        """
        Adapt this routing plan into an atomic TransitionBatch.
        """
        if not self.commands:
            raise TransitionBatchError(
                "Cannot create TransitionBatch from RoutingPlan with no commands"
            )
        resolved_batch_id = (
            batch_id
            or f"routing-batch:{self.solver_run_id}"
        )
        return TransitionBatch(
            batch_id=resolved_batch_id,
            session_id=self.commands[0].session_id,
            expected_state_revision=self.state_revision,
            commands=self.commands,
        )


def routing_plan_to_transition_batch(
    plan: RoutingPlan,
    *,
    batch_id: str | None = None,
) -> TransitionBatch:
    """
    Adapter function converting a RoutingPlan into an atomic TransitionBatch.
    """
    return plan.to_batch(batch_id=batch_id)


class RoutingService:
    """
    Pure routing orchestrator.

    This service is intentionally unable to mutate BookkeepingState.

    Pipeline:

        BookkeepingQueries
            |
            v
        RoutingView
            |
            v
        deterministic feasibility
            |
            v
        semantic scoring
            |
            v
        CP-SAT global optimization
            |
            v
        RoutingResult
            |
            v
        CreateRoutingDecisionCommands

    The resulting commands must be submitted separately to TransitionEngine.
    """

    __slots__ = (
        "_semantic_provider",
        "_max_bank_evidence_items",
    )

    def __init__(
        self,
        *,
        semantic_provider: RoutingSemanticScoreProvider,
        max_bank_evidence_items: int = 20,
    ) -> None:
        if max_bank_evidence_items <= 0:
            raise ValueError(
                "max_bank_evidence_items must be positive"
            )

        self._semantic_provider = semantic_provider
        self._max_bank_evidence_items = (
            max_bank_evidence_items
        )

    def run(
        self,
        *,
        queries: BookkeepingQueries,
        solver_run_id: str,
        issued_at: datetime,
    ) -> RoutingPlan:
        """
        Execute one complete routing computation.

        The BookkeepingState may change after this method returns.

        That is safe because every generated command carries:

            expected_state_revision = RoutingView.state_revision

        TransitionEngine will reject stale output rather than applying a result
        computed against an older bookkeeping world.
        """

        if not solver_run_id:
            raise ValueError(
                "solver_run_id cannot be empty"
            )

        if (
            issued_at.tzinfo is None
            or issued_at.utcoffset() is None
        ):
            raise ValueError(
                "issued_at must be timezone-aware"
            )

        # --------------------------------------------------------------
        # 1. Freeze the bookkeeping world visible to this routing run.
        # --------------------------------------------------------------

        view = build_routing_view(
            queries
        )

        # --------------------------------------------------------------
        # 2. Hard mathematical possibility space.
        # --------------------------------------------------------------

        feasibility = build_feasibility_matrix(
            view
        )

        # --------------------------------------------------------------
        # 3. Semantic judgment only inside deterministic feasibility.
        # --------------------------------------------------------------

        semantic_scores = score_feasible_pairs(
            view=view,
            feasibility=feasibility,
            provider=self._semantic_provider,
            max_bank_evidence_items=(
                self._max_bank_evidence_items
            ),
        )

        # --------------------------------------------------------------
        # 4. Globally compatible routing solution.
        # --------------------------------------------------------------

        result = optimize_routing(
            view=view,
            feasibility=feasibility,
            semantic_scores=semantic_scores,
            solver_run_id=solver_run_id,
        )

        # --------------------------------------------------------------
        # 5. Translate proposals into commands.
        #
        # Still no mutation.
        # --------------------------------------------------------------

        commands = build_routing_commands(
            result=result,
            session_id=queries.session_id,
            issued_at=issued_at,
        )

        return RoutingPlan(
            view=view,
            feasibility=feasibility,
            semantic_scores=semantic_scores,
            result=result,
            commands=commands,
        )


def build_routing_commands(
    *,
    result: RoutingResult,
    session_id: str,
    issued_at: datetime,
) -> tuple[
    CreateRoutingDecisionCommand,
    ...
]:
    """
    Translate RoutingAssignments into transition commands.

    This function is deterministic.

    IDs derive from the solver run and BookItem so that retries of the same
    routing computation produce identical command/artifact identities.

    RoutingService only processes currently unrouted BookItems, therefore these
    commands create new routing truth and do not implicitly supersede existing
    RoutingDecision artifacts.
    """

    if not session_id:
        raise ValueError(
            "session_id cannot be empty"
        )

    if (
        issued_at.tzinfo is None
        or issued_at.utcoffset() is None
    ):
        raise ValueError(
            "issued_at must be timezone-aware"
        )

    commands: list[
        CreateRoutingDecisionCommand
    ] = []

    for assignment in sorted(
        result.assignments,
        key=lambda value: (
            value.book_item_id,
            value.bank_account_id,
        ),
    ):
        routing_decision_id = (
            _routing_decision_id(
                solver_run_id=result.solver_run_id,
                book_item_id=(
                    assignment.book_item_id
                ),
            )
        )

        command_id = (
            _routing_command_id(
                solver_run_id=result.solver_run_id,
                book_item_id=(
                    assignment.book_item_id
                ),
            )
        )

        commands.append(
            CreateRoutingDecisionCommand(
                command_id=command_id,

                expected_state_revision=(
                    result.state_revision
                ),

                source=CommandSource.ROUTING,

                session_id=session_id,

                issued_at=issued_at,

                routing_decision_id=(
                    routing_decision_id
                ),

                book_item_id=(
                    assignment.book_item_id
                ),

                bank_account_id=(
                    assignment.bank_account_id
                ),

                decision_source=(
                    RoutingDecisionSource.CP_SAT
                ),

                utility=(
                    assignment.semantic_score
                ),

                solver_run_id=(
                    result.solver_run_id
                ),

                supersedes_routing_decision_id=None,
            )
        )

    return tuple(
        commands
    )


def _routing_decision_id(
    *,
    solver_run_id: str,
    book_item_id: str,
) -> str:
    return (
        f"routing-decision:"
        f"{solver_run_id}:"
        f"{book_item_id}"
    )


def _routing_command_id(
    *,
    solver_run_id: str,
    book_item_id: str,
) -> str:
    return (
        f"routing-command:"
        f"{solver_run_id}:"
        f"{book_item_id}"
    )