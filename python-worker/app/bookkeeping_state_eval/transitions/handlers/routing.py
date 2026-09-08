from __future__ import annotations

from typing import TypeAlias

from bookkeeping_state_eval.domain.commands import (
    CreateRoutingDecisionCommand,
    InvalidateRoutingDecisionCommand,
)
from bookkeeping_state_eval.domain.routing import (
    RoutingDecision,
    RoutingDecisionInvalidation,
)
from bookkeeping_state_eval.persistence.repository import (
    PersistenceWriteSet,
)
from bookkeeping_state_eval.state.bookkeeping_state import (
    BookkeepingState,
)
from bookkeeping_state_eval.state.derived import (
    DerivedStateError,
    resolve_active_routing_decisions,
)
from bookkeeping_state_eval.transitions.result import (
    RejectionCode,
    TransitionRejection,
)


RoutingCommand = (
    CreateRoutingDecisionCommand
    | InvalidateRoutingDecisionCommand
)

RoutingHandlerResult: TypeAlias = (
    PersistenceWriteSet
    | TransitionRejection
)


def handle_routing_command(
    state: BookkeepingState,
    command: RoutingCommand,
) -> RoutingHandlerResult:
    """
    Validate a routing command and prepare its durable artifact mutation.

    This handler does NOT:

    - mutate BookkeepingState
    - persist anything
    - advance revisions
    - emit runtime events
    - call CP-SAT
    - call an LLM

    It answers only:

        "If this command were committed against the current state, which
        immutable routing artifact should be appended?"

    An empty PersistenceWriteSet means the command is semantically idempotent
    and requires no durable change.
    """

    if isinstance(
        command,
        CreateRoutingDecisionCommand,
    ):
        return _handle_create_routing_decision(
            state,
            command,
        )

    if isinstance(
        command,
        InvalidateRoutingDecisionCommand,
    ):
        return _handle_invalidate_routing_decision(
            state,
            command,
        )

    return TransitionRejection(
        code=RejectionCode.UNSUPPORTED_COMMAND,
        message=(
            "Routing handler received unsupported command "
            f"{type(command).__name__!r}"
        ),
    )


# ======================================================================
# Create routing decision
# ======================================================================


def _handle_create_routing_decision(
    state: BookkeepingState,
    command: CreateRoutingDecisionCommand,
) -> RoutingHandlerResult:
    book_item = state.get_book_item(
        command.book_item_id
    )

    if book_item is None:
        return TransitionRejection(
            code=RejectionCode.UNKNOWN_BOOK_ITEM,
            message=(
                f"Cannot create RoutingDecision "
                f"{command.routing_decision_id!r}: "
                f"BookItem {command.book_item_id!r} does not exist"
            ),
            artifact_ids=(
                command.book_item_id,
            ),
        )

    bank_account = state.get_bank_account(
        command.bank_account_id
    )

    if bank_account is None:
        return TransitionRejection(
            code=RejectionCode.UNKNOWN_BANK_ACCOUNT,
            message=(
                f"Cannot create RoutingDecision "
                f"{command.routing_decision_id!r}: "
                f"BankAccount {command.bank_account_id!r} "
                "does not exist"
            ),
            artifact_ids=(
                command.book_item_id,
                command.bank_account_id,
            ),
        )

    # ------------------------------------------------------------------
    # Currency compatibility
    # ------------------------------------------------------------------

    if (
        state.context.policy.require_exact_currency_match
        and book_item.currency != bank_account.currency
    ):
        return TransitionRejection(
            code=RejectionCode.CURRENCY_MISMATCH,
            message=(
                f"BookItem {book_item.id!r} currency "
                f"{book_item.currency!r} cannot be routed to "
                f"BankAccount {bank_account.id!r} currency "
                f"{bank_account.currency!r}"
            ),
            artifact_ids=(
                book_item.id,
                bank_account.id,
            ),
        )

    # ------------------------------------------------------------------
    # Validate explicit supersession target
    # ------------------------------------------------------------------

    superseded = None

    if command.supersedes_routing_decision_id is not None:
        superseded = state.get_routing_decision(
            command.supersedes_routing_decision_id
        )

        if superseded is None:
            return TransitionRejection(
                code=RejectionCode.UNKNOWN_ROUTING_DECISION,
                message=(
                    f"RoutingDecision "
                    f"{command.routing_decision_id!r} attempts "
                    f"to supersede unknown RoutingDecision "
                    f"{command.supersedes_routing_decision_id!r}"
                ),
                artifact_ids=(
                    command.routing_decision_id,
                    command.supersedes_routing_decision_id,
                ),
            )

        if superseded.book_item_id != command.book_item_id:
            return TransitionRejection(
                code=RejectionCode.ROUTING_SUBJECT_MISMATCH,
                message=(
                    f"RoutingDecision "
                    f"{command.routing_decision_id!r} for "
                    f"BookItem {command.book_item_id!r} cannot "
                    f"supersede RoutingDecision {superseded.id!r} "
                    f"for BookItem {superseded.book_item_id!r}"
                ),
                artifact_ids=(
                    command.routing_decision_id,
                    superseded.id,
                    command.book_item_id,
                    superseded.book_item_id,
                ),
            )

    # ------------------------------------------------------------------
    # Determine current routing truth
    # ------------------------------------------------------------------

    try:
        active_routes = resolve_active_routing_decisions(
            state
        )
    except DerivedStateError as exc:
        return TransitionRejection(
            code=(
                RejectionCode
                .MULTIPLE_ACTIVE_ROUTING_DECISIONS
            ),
            message=str(exc),
            artifact_ids=(
                command.book_item_id,
            ),
        )

    current = active_routes.get(
        command.book_item_id
    )

    # ------------------------------------------------------------------
    # Supersession semantics
    # ------------------------------------------------------------------

    if command.supersedes_routing_decision_id is not None:
        if current is None:
            return TransitionRejection(
                code=RejectionCode.ROUTING_SUBJECT_MISMATCH,
                message=(
                    f"RoutingDecision "
                    f"{command.routing_decision_id!r} attempts "
                    f"to supersede "
                    f"{command.supersedes_routing_decision_id!r}, "
                    f"but BookItem {command.book_item_id!r} "
                    "currently has no active routing decision"
                ),
                artifact_ids=(
                    command.book_item_id,
                    command.supersedes_routing_decision_id,
                ),
            )

        if (
            current.id
            != command.supersedes_routing_decision_id
        ):
            return TransitionRejection(
                code=RejectionCode.ROUTING_SUBJECT_MISMATCH,
                message=(
                    f"BookItem {command.book_item_id!r} is "
                    f"currently routed by RoutingDecision "
                    f"{current.id!r}; command attempted to "
                    f"supersede "
                    f"{command.supersedes_routing_decision_id!r}"
                ),
                artifact_ids=(
                    command.book_item_id,
                    current.id,
                    command.supersedes_routing_decision_id,
                ),
            )

    elif current is not None:
        # --------------------------------------------------------------
        # Reasserting exactly the same semantic route is a no-op.
        # --------------------------------------------------------------

        if current.bank_account_id == command.bank_account_id:
            return PersistenceWriteSet()

        # --------------------------------------------------------------
        # Changing routing truth requires explicit supersession.
        # --------------------------------------------------------------

        return TransitionRejection(
            code=(
                RejectionCode
                .MULTIPLE_ACTIVE_ROUTING_DECISIONS
            ),
            message=(
                f"BookItem {command.book_item_id!r} already has "
                f"active RoutingDecision {current.id!r} targeting "
                f"BankAccount {current.bank_account_id!r}. "
                "A different route must explicitly supersede the "
                "current decision."
            ),
            artifact_ids=(
                command.book_item_id,
                current.id,
                current.bank_account_id,
                command.bank_account_id,
            ),
        )

    # ------------------------------------------------------------------
    # Construct the immutable durable artifact
    # ------------------------------------------------------------------

    decision = RoutingDecision(
        id=command.routing_decision_id,
        book_item_id=command.book_item_id,
        bank_account_id=command.bank_account_id,
        source=command.decision_source,
        utility=command.utility,
        solver_run_id=command.solver_run_id,
        supersedes_routing_decision_id=(
            command.supersedes_routing_decision_id
        ),
        session_id=command.session_id,
        state_revision_at_creation=(
            state.revision + 1
        ),
        created_at=command.issued_at,
    )

    # ------------------------------------------------------------------
    # Immutable ID protection
    # ------------------------------------------------------------------

    existing = state.get_routing_decision(
        decision.id
    )

    if existing is not None:
        if existing == decision:
            return PersistenceWriteSet()

        return TransitionRejection(
            code=RejectionCode.DUPLICATE_ARTIFACT,
            message=(
                f"RoutingDecision ID {decision.id!r} already "
                "exists with different immutable content"
            ),
            artifact_ids=(
                decision.id,
            ),
        )

    return PersistenceWriteSet(
        routing_decisions=(
            decision,
        ),
    )


# ======================================================================
# Invalidate routing decision
# ======================================================================


def _handle_invalidate_routing_decision(
    state: BookkeepingState,
    command: InvalidateRoutingDecisionCommand,
) -> RoutingHandlerResult:
    target = state.get_routing_decision(
        command.routing_decision_id
    )

    if target is None:
        return TransitionRejection(
            code=RejectionCode.UNKNOWN_ROUTING_DECISION,
            message=(
                f"Cannot invalidate RoutingDecision "
                f"{command.routing_decision_id!r}: "
                "artifact does not exist"
            ),
            artifact_ids=(
                command.routing_decision_id,
            ),
        )

    # ------------------------------------------------------------------
    # If this routing decision is already invalidated, repeating the
    # semantic operation changes nothing.
    # ------------------------------------------------------------------

    existing_target_invalidations = tuple(
        invalidation
        for invalidation
        in state.routing_invalidations.values()
        if (
            invalidation.routing_decision_id
            == command.routing_decision_id
        )
    )

    if existing_target_invalidations:
        return PersistenceWriteSet()

    invalidation = RoutingDecisionInvalidation(
        id=command.invalidation_id,
        routing_decision_id=(
            command.routing_decision_id
        ),
        reason=command.reason,
        session_id=command.session_id,
        state_revision_at_invalidation=(
            state.revision + 1
        ),
        created_at=command.issued_at,
    )

    # ------------------------------------------------------------------
    # Immutable invalidation ID protection
    # ------------------------------------------------------------------

    existing = state.routing_invalidations.get(
        invalidation.id
    )

    if existing is not None:
        if existing == invalidation:
            return PersistenceWriteSet()

        return TransitionRejection(
            code=RejectionCode.DUPLICATE_ARTIFACT,
            message=(
                f"RoutingDecisionInvalidation ID "
                f"{invalidation.id!r} already exists with "
                "different immutable content"
            ),
            artifact_ids=(
                invalidation.id,
                target.id,
            ),
        )

    return PersistenceWriteSet(
        routing_invalidations=(
            invalidation,
        ),
    )