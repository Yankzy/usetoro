from __future__ import annotations

from dataclasses import dataclass, field
from threading import RLock
from typing import Iterable, TypeVar

from pydantic import BaseModel

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
from bookkeeping_state.persistence.repository import (
    BookkeepingRepository,
    BookkeepingSnapshot,
    PersistenceCommitResult,
    PersistenceConflictError,
    PersistenceDuplicateError,
    PersistenceError,
    PersistenceWriteSet,
)


ArtifactT = TypeVar("ArtifactT", bound=BaseModel)


@dataclass(slots=True)
class _CompanyStore:
    """
    Internal durable representation for one company.

    This is deliberately NOT BookkeepingState.

    It models what a transactional database would persist.
    """

    context: BookkeepingContext
    revision: int = 0

    bank_accounts: dict[str, BankAccount] = field(default_factory=dict)
    bank_items: dict[str, BankItem] = field(default_factory=dict)
    book_items: dict[str, BookItem] = field(default_factory=dict)

    documents: dict[str, Document] = field(default_factory=dict)
    counterparties: dict[str, Counterparty] = field(default_factory=dict)

    routing_decisions: dict[str, RoutingDecision] = field(default_factory=dict)
    routing_invalidations: dict[
        str,
        RoutingDecisionInvalidation,
    ] = field(default_factory=dict)

    classifications: dict[
        str,
        ClassificationDecision,
    ] = field(default_factory=dict)

    classification_invalidations: dict[
        str,
        ClassificationInvalidation,
    ] = field(default_factory=dict)

    reconciliations: dict[
        str,
        Reconciliation,
    ] = field(default_factory=dict)

    reconciliation_invalidations: dict[
        str,
        ReconciliationInvalidation,
    ] = field(default_factory=dict)

    book_item_evidence_assertions: dict[
        str,
        BookItemEvidenceAssertion,
    ] = field(default_factory=dict)
    book_item_evidence_invalidations: dict[
        str,
        BookItemEvidenceInvalidation,
    ] = field(default_factory=dict)


class InMemoryBookkeepingRepository(BookkeepingRepository):
    """
    Thread-safe transactional persistence implementation for the eval.

    Its purpose is to reproduce the semantics we expect from production
    PostgreSQL/AlloyDB persistence:

        - consistent reads
        - immutable artifact identity
        - atomic writes
        - optimistic concurrency
        - deterministic snapshots
        - no partial commits

    It must not contain bookkeeping reasoning.
    """

    __slots__ = (
        "_companies",
        "_lock",
    )

    def __init__(
        self,
        *,
        initial_snapshots: Iterable[BookkeepingSnapshot] = (),
    ) -> None:
        self._companies: dict[str, _CompanyStore] = {}
        self._lock = RLock()

        for snapshot in initial_snapshots:
            self.seed_snapshot(snapshot)

    # ------------------------------------------------------------------
    # Eval setup
    # ------------------------------------------------------------------

    def seed_snapshot(
        self,
        snapshot: BookkeepingSnapshot,
    ) -> None:
        """
        Seed one company's durable world.

        This method exists only to construct eval fixtures. It is not part of
        the BookkeepingRepository production contract.

        A company may only be seeded once.
        """

        company_id = snapshot.context.company_id

        with self._lock:
            if company_id in self._companies:
                raise PersistenceError(
                    f"Company {company_id!r} is already seeded"
                )

            store = _CompanyStore(
                context=snapshot.context,
                revision=snapshot.persistence_revision,
            )

            store.bank_accounts = _build_unique_collection(
                snapshot.bank_accounts,
                artifact_name="BankAccount",
            )

            store.bank_items = _build_unique_collection(
                snapshot.bank_items,
                artifact_name="BankItem",
            )

            store.book_items = _build_unique_collection(
                snapshot.book_items,
                artifact_name="BookItem",
            )

            store.documents = _build_unique_collection(
                snapshot.documents,
                artifact_name="Document",
            )

            store.counterparties = _build_unique_collection(
                snapshot.counterparties,
                artifact_name="Counterparty",
            )

            store.routing_decisions = _build_unique_collection(
                snapshot.routing_decisions,
                artifact_name="RoutingDecision",
            )

            store.routing_invalidations = _build_unique_collection(
                snapshot.routing_invalidations,
                artifact_name="RoutingDecisionInvalidation",
            )

            store.classifications = _build_unique_collection(
                snapshot.classifications,
                artifact_name="ClassificationDecision",
            )

            store.classification_invalidations = _build_unique_collection(
                snapshot.classification_invalidations,
                artifact_name="ClassificationInvalidation",
            )

            store.reconciliations = _build_unique_collection(
                snapshot.reconciliations,
                artifact_name="Reconciliation",
            )

            store.reconciliation_invalidations = _build_unique_collection(
                snapshot.reconciliation_invalidations,
                artifact_name="ReconciliationInvalidation",
            )

            store.book_item_evidence_assertions = _build_unique_collection(
                snapshot.book_item_evidence_assertions,
                artifact_name="BookItemEvidenceAssertion",
            )

            store.book_item_evidence_invalidations = _build_unique_collection(
                snapshot.book_item_evidence_invalidations,
                artifact_name="BookItemEvidenceInvalidation",
            )

            self._companies[company_id] = store

    # ------------------------------------------------------------------
    # Repository contract
    # ------------------------------------------------------------------

    def load_snapshot(
        self,
        *,
        company_id: str,
    ) -> BookkeepingSnapshot:
        """
        Return one consistent immutable snapshot.

        The lock ensures we cannot observe half of a concurrent commit.
        """

        with self._lock:
            store = self._require_company(company_id)

            return BookkeepingSnapshot(
                persistence_revision=store.revision,
                context=store.context,

                bank_accounts=_sorted_artifacts(
                    store.bank_accounts
                ),

                bank_items=_sorted_artifacts(
                    store.bank_items
                ),

                book_items=_sorted_artifacts(
                    store.book_items
                ),

                documents=_sorted_artifacts(
                    store.documents
                ),

                counterparties=_sorted_artifacts(
                    store.counterparties
                ),

                routing_decisions=_sorted_artifacts(
                    store.routing_decisions
                ),

                routing_invalidations=_sorted_artifacts(
                    store.routing_invalidations
                ),

                classifications=_sorted_artifacts(
                    store.classifications
                ),

                classification_invalidations=_sorted_artifacts(
                    store.classification_invalidations
                ),

                reconciliations=_sorted_artifacts(
                    store.reconciliations
                ),

                reconciliation_invalidations=_sorted_artifacts(
                    store.reconciliation_invalidations
                ),

                book_item_evidence_assertions=_sorted_artifacts(
                    store.book_item_evidence_assertions
                ),

                book_item_evidence_invalidations=_sorted_artifacts(
                    store.book_item_evidence_invalidations
                ),
            )

    def commit(
        self,
        *,
        company_id: str,
        expected_revision: int,
        write_set: PersistenceWriteSet,
    ) -> PersistenceCommitResult:
        """
        Atomically append a set of immutable bookkeeping artifacts.

        The implementation uses copy-on-write under a lock:

        1. verify optimistic concurrency
        2. copy every affected durable collection
        3. apply and validate the entire write set to the copies
        4. swap all collections only if every operation succeeds
        5. advance persistence revision exactly once

        Therefore an exception cannot leave persistence partially mutated.
        """

        with self._lock:
            store = self._require_company(company_id)

            if store.revision != expected_revision:
                raise PersistenceConflictError(
                    "Persistence revision conflict for "
                    f"company {company_id!r}: "
                    f"expected={expected_revision}, "
                    f"actual={store.revision}"
                )

            previous_revision = store.revision

            if write_set.is_empty:
                return PersistenceCommitResult(
                    previous_revision=previous_revision,
                    new_revision=previous_revision,
                    write_set=write_set,
                )

            # ----------------------------------------------------------
            # Copy-on-write transactional workspace
            # ----------------------------------------------------------

            bank_accounts = dict(store.bank_accounts)
            bank_items = dict(store.bank_items)
            book_items = dict(store.book_items)

            documents = dict(store.documents)
            counterparties = dict(store.counterparties)

            routing_decisions = dict(
                store.routing_decisions
            )

            routing_invalidations = dict(
                store.routing_invalidations
            )

            classifications = dict(
                store.classifications
            )

            classification_invalidations = dict(
                store.classification_invalidations
            )

            reconciliations = dict(
                store.reconciliations
            )

            reconciliation_invalidations = dict(
                store.reconciliation_invalidations
            )

            book_item_evidence_assertions = dict(
                store.book_item_evidence_assertions
            )

            book_item_evidence_invalidations = dict(
                store.book_item_evidence_invalidations
            )

            # ----------------------------------------------------------
            # Apply entire write set to temporary copies
            # ----------------------------------------------------------

            _append_artifacts(
                bank_accounts,
                write_set.bank_accounts,
                artifact_name="BankAccount",
            )

            _append_artifacts(
                bank_items,
                write_set.bank_items,
                artifact_name="BankItem",
            )

            _append_artifacts(
                book_items,
                write_set.book_items,
                artifact_name="BookItem",
            )

            _append_artifacts(
                documents,
                write_set.documents,
                artifact_name="Document",
            )

            _append_artifacts(
                counterparties,
                write_set.counterparties,
                artifact_name="Counterparty",
            )

            _append_artifacts(
                routing_decisions,
                write_set.routing_decisions,
                artifact_name="RoutingDecision",
            )

            _append_artifacts(
                routing_invalidations,
                write_set.routing_invalidations,
                artifact_name="RoutingDecisionInvalidation",
            )

            _append_artifacts(
                classifications,
                write_set.classifications,
                artifact_name="ClassificationDecision",
            )

            _append_artifacts(
                classification_invalidations,
                write_set.classification_invalidations,
                artifact_name="ClassificationInvalidation",
            )

            _append_artifacts(
                reconciliations,
                write_set.reconciliations,
                artifact_name="Reconciliation",
            )

            _append_artifacts(
                reconciliation_invalidations,
                write_set.reconciliation_invalidations,
                artifact_name="ReconciliationInvalidation",
            )

            _append_artifacts(
                book_item_evidence_assertions,
                write_set.book_item_evidence_assertions,
                artifact_name="BookItemEvidenceAssertion",
            )

            _append_artifacts(
                book_item_evidence_invalidations,
                write_set.book_item_evidence_invalidations,
                artifact_name="BookItemEvidenceInvalidation",
            )

            # ----------------------------------------------------------
            # Commit atomically
            # ----------------------------------------------------------

            new_revision = previous_revision + 1

            store.bank_accounts = bank_accounts
            store.bank_items = bank_items
            store.book_items = book_items

            store.documents = documents
            store.counterparties = counterparties

            store.routing_decisions = routing_decisions
            store.routing_invalidations = routing_invalidations

            store.classifications = classifications
            store.classification_invalidations = (
                classification_invalidations
            )

            store.reconciliations = reconciliations
            store.reconciliation_invalidations = (
                reconciliation_invalidations
            )

            store.book_item_evidence_assertions = (
                book_item_evidence_assertions
            )
            store.book_item_evidence_invalidations = (
                book_item_evidence_invalidations
            )

            store.revision = new_revision

            return PersistenceCommitResult(
                previous_revision=previous_revision,
                new_revision=new_revision,
                write_set=write_set,
            )

    # ------------------------------------------------------------------
    # Eval diagnostics
    # ------------------------------------------------------------------

    def current_revision(
        self,
        *,
        company_id: str,
    ) -> int:
        """
        Inspect durable revision without loading the full snapshot.

        Eval/debug helper only.
        """

        with self._lock:
            return self._require_company(
                company_id
            ).revision

    def has_company(
        self,
        *,
        company_id: str,
    ) -> bool:
        with self._lock:
            return company_id in self._companies

    # ------------------------------------------------------------------
    # Internal
    # ------------------------------------------------------------------

    def _require_company(
        self,
        company_id: str,
    ) -> _CompanyStore:
        store = self._companies.get(company_id)

        if store is None:
            raise PersistenceError(
                f"Unknown company {company_id!r}"
            )

        return store


def _build_unique_collection(
    artifacts: Iterable[ArtifactT],
    *,
    artifact_name: str,
) -> dict[str, ArtifactT]:
    collection: dict[str, ArtifactT] = {}

    for artifact in artifacts:
        artifact_id = _artifact_id(
            artifact,
            artifact_name=artifact_name,
        )

        existing = collection.get(artifact_id)

        if existing is not None:
            if existing == artifact:
                raise PersistenceDuplicateError(
                    f"Duplicate {artifact_name} {artifact_id!r} "
                    "appears more than once in the seed snapshot"
                )

            raise PersistenceDuplicateError(
                f"Conflicting {artifact_name} values share ID "
                f"{artifact_id!r}"
            )

        collection[artifact_id] = artifact

    return collection


def _append_artifacts(
    collection: dict[str, ArtifactT],
    artifacts: tuple[ArtifactT, ...],
    *,
    artifact_name: str,
) -> None:
    """
    Append immutable artifacts to a transactional collection.

    Exact reinsertion is idempotent.

    Same ID with different content is forbidden.
    """

    seen_in_write_set: dict[str, ArtifactT] = {}

    for artifact in artifacts:
        artifact_id = _artifact_id(
            artifact,
            artifact_name=artifact_name,
        )

        previous_in_write_set = seen_in_write_set.get(
            artifact_id
        )

        if previous_in_write_set is not None:
            if previous_in_write_set == artifact:
                continue

            raise PersistenceDuplicateError(
                f"Write set contains conflicting "
                f"{artifact_name} artifacts with ID "
                f"{artifact_id!r}"
            )

        seen_in_write_set[artifact_id] = artifact

        existing = collection.get(artifact_id)

        if existing is None:
            collection[artifact_id] = artifact
            continue

        if existing == artifact:
            # Safe retry / replay of exactly the same immutable fact.
            continue

        raise PersistenceDuplicateError(
            f"{artifact_name} {artifact_id!r} already exists "
            "with different immutable content"
        )


def _artifact_id(
    artifact: BaseModel,
    *,
    artifact_name: str,
) -> str:
    artifact_id = getattr(
        artifact,
        "id",
        None,
    )

    if not isinstance(artifact_id, str) or not artifact_id:
        raise PersistenceError(
            f"{artifact_name} must expose a non-empty string 'id'"
        )

    return artifact_id


def _sorted_artifacts(
    artifacts: dict[str, ArtifactT],
) -> tuple[ArtifactT, ...]:
    """
    Persistence retrieval order must never affect hydrated state.
    """

    return tuple(
        artifact
        for _, artifact in sorted(
            artifacts.items(),
            key=lambda item: item[0],
        )
    )

"""
We now have a genuine durable/runtime separation:

InMemoryBookkeepingRepository
    persistent for duration of eval
            |
            | load_snapshot
            v
    BookkeepingSnapshot
            |
            | hydrate
            v
    BookkeepingState
            |
        RAM only

And importantly, a failed multi-artifact write cannot leave something like:

Classification persisted
Reconciliation failed
State half changed

The copied collections are discarded if any artifact conflicts before the final swap.
"""