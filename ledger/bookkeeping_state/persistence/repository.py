from __future__ import annotations

from dataclasses import dataclass
from typing import Protocol, runtime_checkable

from bookkeeping_state.domain.bank import BankAccount, BankItem
from bookkeeping_state.domain.books import BookItem
from bookkeeping_state.domain.classifications import (
    ClassificationDecision,
    ClassificationInvalidation,
)
from bookkeeping_state.domain.context import BookkeepingContext
from bookkeeping_state.domain.counterparties import Counterparty
from bookkeeping_state.domain.documents import Document
from bookkeeping_state.domain.evidence import (
    BookItemEvidenceAssertion,
    BookItemEvidenceInvalidation,
)
from bookkeeping_state.domain.reconciliations import (
    Reconciliation,
    ReconciliationInvalidation,
)
from bookkeeping_state.domain.routing import (
    RoutingDecision,
    RoutingDecisionInvalidation,
)

"""
This establishes a useful separation:

BookkeepingRepository
        |
        | load_snapshot()
        v
BookkeepingSnapshot
        |
        | hydrate
        v
BookkeepingState

And on mutation:

TransitionEngine
      |
      | PersistenceWriteSet
      v
BookkeepingRepository.commit()
      |
      | atomic durable commit
      v
PersistenceCommitResult
      |
      | StateDelta
      v
BookkeepingState

The repository has no methods like update_classification() or mark_reconciled(). Those would push bookkeeping semantics into persistence.

Persistence only knows immutable artifacts and atomic commits.
"""

class PersistenceError(RuntimeError):
    """Base error for durable bookkeeping persistence failures."""


class PersistenceConflictError(PersistenceError):
    """
    Raised when a write was prepared against an older persistence revision.

    This is the durable-storage equivalent of optimistic concurrency control.
    """


class PersistenceDuplicateError(PersistenceError):
    """
    Raised when an immutable artifact ID already exists with different content.
    """


@dataclass(frozen=True, slots=True)
class BookkeepingSnapshot:
    """
    Immutable read snapshot used to hydrate BookkeepingState.

    This is NOT BookkeepingState.

    It is simply a consistent representation of durable artifacts at one
    persistence revision.
    """

    persistence_revision: int

    context: BookkeepingContext

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


@dataclass(frozen=True, slots=True)
class PersistenceWriteSet:
    """
    Atomic append-only mutation request against durable bookkeeping storage.

    Domain artifacts are immutable, so persistence does not update artifact
    contents in place.

    Corrections happen by appending new decision, supersession, or invalidation
    artifacts.

    The repository must either commit the entire write set or commit nothing.
    """

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

    @property
    def is_empty(self) -> bool:
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
            )
        )


@dataclass(frozen=True, slots=True)
class PersistenceCommitResult:
    """
    Result of one successful atomic persistence commit.
    """

    previous_revision: int
    new_revision: int

    write_set: PersistenceWriteSet


@runtime_checkable
class BookkeepingRepository(Protocol):
    """
    Persistence boundary used by BookkeepingState.

    Production may implement this with PostgreSQL/AlloyDB.

    The eval will implement the same contract entirely in memory.

    BookkeepingState must not know which implementation is behind this
    interface.
    """

    def load_snapshot(
        self,
        *,
        company_id: str,
    ) -> BookkeepingSnapshot:
        """
        Load one internally consistent durable snapshot.

        All returned artifacts must correspond to the same
        persistence_revision.
        """

    def commit(
        self,
        *,
        company_id: str,
        expected_revision: int,
        write_set: PersistenceWriteSet,
    ) -> PersistenceCommitResult:
        """
        Atomically append durable artifacts.

        Requirements:

        1. Current persistence revision must equal expected_revision.
        2. Every artifact ID is immutable.
        3. Reinserting exactly the same artifact may be treated idempotently.
        4. Same ID with different contents must fail.
        5. The write set is all-or-nothing.
        6. A successful non-empty commit advances persistence revision exactly
           once.
        7. A failed commit changes nothing.
        """
