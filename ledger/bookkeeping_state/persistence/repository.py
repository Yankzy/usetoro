from __future__ import annotations

from dataclasses import dataclass


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
from bookkeeping_state.domain.payment_application import (
    ExecutedPaymentApplication,
)
from bookkeeping_state.domain.reconciliations import (
    Reconciliation,
    ReconciliationInvalidation,
)
from bookkeeping_state.domain.residual_bank_classifications import (
    ResidualBankClassificationDecision,
    ResidualBankClassificationInvalidation,
    ResidualBankPosting,
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
    historical_book_item_ids: tuple[str, ...] = ()

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

    executed_payment_applications: tuple[
        ExecutedPaymentApplication,
        ...
    ] = ()

    residual_bank_classifications: tuple[
        ResidualBankClassificationDecision,
        ...
    ] = ()
    residual_bank_classification_invalidations: tuple[
        ResidualBankClassificationInvalidation,
        ...
    ] = ()
    residual_bank_postings: tuple[
        ResidualBankPosting,
        ...
    ] = ()



from datetime import date
from bookkeeping_state.domain.money import AmountUnits


@dataclass(frozen=True, slots=True)
class PaymentApplicationObligationAllocationInstruction:
    book_item_id: str
    amount_units: AmountUnits


@dataclass(frozen=True, slots=True)
class ApplyPaymentInstruction:
    payment_application_id: str
    bank_item_id: str
    total_amount_units: AmountUnits
    payment_date: date
    allocations: tuple[PaymentApplicationObligationAllocationInstruction, ...]
    session_id: str | None = None


@dataclass(frozen=True, slots=True)
class PostResidualBankClassificationInstruction:
    decision_id: str
    command_id: str
    session_id: str | None = None
    state_revision_at_creation: int = 1


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

    payment_applications_to_execute: tuple[
        ApplyPaymentInstruction,
        ...
    ] = ()

    residual_bank_classifications: tuple[
        ResidualBankClassificationDecision,
        ...
    ] = ()
    residual_bank_classification_invalidations: tuple[
        ResidualBankClassificationInvalidation,
        ...
    ] = ()
    residual_bank_postings: tuple[
        ResidualBankPosting,
        ...
    ] = ()
    residual_bank_postings_to_execute: tuple[
        PostResidualBankClassificationInstruction,
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
                self.payment_applications_to_execute,
                self.residual_bank_classifications,
                self.residual_bank_classification_invalidations,
                self.residual_bank_postings,
                self.residual_bank_postings_to_execute,
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


class BookkeepingRepository:
    """
    Persistence boundary used by BookkeepingState.

    Production implementation reads from the Django ledger models.
    """

    def load_snapshot(
        self,
        *,
        company_id: str,
    ) -> BookkeepingSnapshot:
        """
        Load one internally consistent durable snapshot from the Django ledger models.
        """
        from bookkeeping_state.persistence.reader import read_django_snapshot

        return read_django_snapshot(company_id=company_id)

    def commit(
        self,
        *,
        company_id: str,
        expected_revision: int,
        write_set: PersistenceWriteSet,
    ) -> PersistenceCommitResult:
        """
        Atomically append durable decision artifacts to Django storage.
        """
        from bookkeeping_state.persistence.writer import commit_django_write_set

        return commit_django_write_set(
            company_id=company_id,
            expected_revision=expected_revision,
            write_set=write_set,
        )


