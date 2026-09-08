from __future__ import annotations

from dataclasses import dataclass

from bookkeeping_state_eval.domain.bank import BankAccount, BankItem
from bookkeeping_state_eval.domain.books import BookItem
from bookkeeping_state_eval.domain.classifications import (
    ClassificationDecision,
    ClassificationInvalidation,
)
from bookkeeping_state_eval.domain.counterparties import Counterparty
from bookkeeping_state_eval.domain.documents import Document
from bookkeeping_state_eval.domain.events import StateEvent
from bookkeeping_state_eval.domain.evidence import (
    BookItemEvidenceAssertion,
    BookItemEvidenceInvalidation,
)
from bookkeeping_state_eval.domain.reconciliations import (
    Reconciliation,
    ReconciliationInvalidation,
)
from bookkeeping_state_eval.domain.routing import (
    RoutingDecision,
    RoutingDecisionInvalidation,
)
from bookkeeping_state_eval.persistence.repository import (
    PersistenceCommitResult,
    PersistenceWriteSet,
)


@dataclass(frozen=True, slots=True)
class StateDelta:
    """
    Exact mutation applied to one live BookkeepingState after persistence has
    successfully committed.

    A StateDelta is runtime machinery, not durable bookkeeping truth.

    Durable truth is represented by the immutable artifacts contained inside
    the delta.

    Revision semantics:

        previous_state_revision
            revision of the live in-memory state before application

        new_state_revision
            revision after application

        previous_persistence_revision
            durable revision against which the command committed

        new_persistence_revision
            durable revision returned by persistence after commit
    """

    previous_state_revision: int
    new_state_revision: int

    previous_persistence_revision: int
    new_persistence_revision: int

    bank_accounts: tuple[BankAccount, ...] = ()
    bank_items: tuple[BankItem, ...] = ()
    book_items: tuple[BookItem, ...] = ()

    documents: tuple[Document, ...] = ()
    counterparties: tuple[Counterparty, ...] = ()

    routing_decisions: tuple[RoutingDecision, ...] = ()
    routing_invalidations: tuple[
        RoutingDecisionInvalidation,
        ...
    ] = ()

    classifications: tuple[ClassificationDecision, ...] = ()
    classification_invalidations: tuple[
        ClassificationInvalidation,
        ...
    ] = ()

    reconciliations: tuple[Reconciliation, ...] = ()
    reconciliation_invalidations: tuple[
        ReconciliationInvalidation,
        ...
    ] = ()

    book_item_evidence_assertions: tuple[
        BookItemEvidenceAssertion,
        ...
    ] = ()
    book_item_evidence_invalidations: tuple[
        BookItemEvidenceInvalidation,
        ...
    ] = ()

    events: tuple[StateEvent, ...] = ()

    invalidated_hypothesis_ids: tuple[str, ...] = ()

    clear_all_hypotheses: bool = False

    def __post_init__(self) -> None:
        if self.previous_state_revision < 0:
            raise ValueError(
                "previous_state_revision cannot be negative"
            )

        if self.new_state_revision < 0:
            raise ValueError(
                "new_state_revision cannot be negative"
            )

        if self.previous_persistence_revision < 0:
            raise ValueError(
                "previous_persistence_revision cannot be negative"
            )

        if self.new_persistence_revision < 0:
            raise ValueError(
                "new_persistence_revision cannot be negative"
            )

        if self.is_noop:
            if self.new_state_revision != self.previous_state_revision:
                raise ValueError(
                    "No-op StateDelta cannot advance state revision"
                )
        else:
            if (
                self.new_state_revision
                != self.previous_state_revision + 1
            ):
                raise ValueError(
                    "Non-empty StateDelta must advance state revision "
                    "exactly once"
                )

        if (
            self.new_persistence_revision
            < self.previous_persistence_revision
        ):
            raise ValueError(
                "Persistence revision cannot move backwards"
            )

        if (
            self.invalidated_hypothesis_ids
            and self.clear_all_hypotheses
        ):
            raise ValueError(
                "StateDelta cannot request both selective hypothesis "
                "invalidation and clear_all_hypotheses"
            )

        if len(set(self.invalidated_hypothesis_ids)) != len(
            self.invalidated_hypothesis_ids
        ):
            raise ValueError(
                "invalidated_hypothesis_ids contains duplicates"
            )

    @property
    def is_noop(self) -> bool:
        """
        True when applying this delta changes neither durable nor runtime
        bookkeeping state.
        """

        return not any(
            (
                self.bank_accounts,
                self.bank_items,
                self.book_items,
                self.documents,
                self.counterparties,
                self.routing_decisions,
                self.routing_invalidations,
                self.classifications,
                self.classification_invalidations,
                self.reconciliations,
                self.reconciliation_invalidations,
                self.book_item_evidence_assertions,
                self.book_item_evidence_invalidations,
                self.events,
                self.invalidated_hypothesis_ids,
                self.clear_all_hypotheses,
            )
        )

    @property
    def contains_durable_changes(self) -> bool:
        return not self.write_set.is_empty

    @property
    def write_set(self) -> PersistenceWriteSet:
        """
        Reconstruct the durable portion of this delta.
        """

        return PersistenceWriteSet(
            bank_accounts=self.bank_accounts,
            bank_items=self.bank_items,
            book_items=self.book_items,
            documents=self.documents,
            counterparties=self.counterparties,
            routing_decisions=self.routing_decisions,
            routing_invalidations=self.routing_invalidations,
            classifications=self.classifications,
            classification_invalidations=(
                self.classification_invalidations
            ),
            reconciliations=self.reconciliations,
            reconciliation_invalidations=(
                self.reconciliation_invalidations
            ),
            book_item_evidence_assertions=(
                self.book_item_evidence_assertions
            ),
            book_item_evidence_invalidations=(
                self.book_item_evidence_invalidations
            ),
        )

    @classmethod
    def from_commit(
        cls,
        *,
        previous_state_revision: int,
        commit: PersistenceCommitResult,
        events: tuple[StateEvent, ...] = (),
        invalidated_hypothesis_ids: tuple[str, ...] = (),
        clear_all_hypotheses: bool = False,
    ) -> "StateDelta":
        """
        Construct the live-state mutation corresponding to a successful
        persistence commit.

        A non-empty durable commit causes exactly one in-memory state revision.
        """

        has_runtime_changes = bool(
            events
            or invalidated_hypothesis_ids
            or clear_all_hypotheses
        )

        has_durable_changes = not commit.write_set.is_empty

        should_advance_state = (
            has_durable_changes or has_runtime_changes
        )

        new_state_revision = (
            previous_state_revision + 1
            if should_advance_state
            else previous_state_revision
        )

        write_set = commit.write_set

        return cls(
            previous_state_revision=previous_state_revision,
            new_state_revision=new_state_revision,
            previous_persistence_revision=(
                commit.previous_revision
            ),
            new_persistence_revision=commit.new_revision,

            bank_accounts=write_set.bank_accounts,
            bank_items=write_set.bank_items,
            book_items=write_set.book_items,

            documents=write_set.documents,
            counterparties=write_set.counterparties,

            routing_decisions=write_set.routing_decisions,
            routing_invalidations=(
                write_set.routing_invalidations
            ),

            classifications=write_set.classifications,
            classification_invalidations=(
                write_set.classification_invalidations
            ),

            reconciliations=write_set.reconciliations,
            reconciliation_invalidations=(
                write_set.reconciliation_invalidations
            ),

            book_item_evidence_assertions=(
                write_set.book_item_evidence_assertions
            ),
            book_item_evidence_invalidations=(
                write_set.book_item_evidence_invalidations
            ),

            events=events,

            invalidated_hypothesis_ids=(
                invalidated_hypothesis_ids
            ),

            clear_all_hypotheses=clear_all_hypotheses,
        )