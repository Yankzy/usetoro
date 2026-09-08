from __future__ import annotations

from bookkeeping_state_eval.domain.bank import BankAccount, BankItem
from bookkeeping_state_eval.domain.books import BookItem
from bookkeeping_state_eval.domain.classifications import ClassificationDecision
from bookkeeping_state_eval.domain.counterparties import Counterparty
from bookkeeping_state_eval.domain.documents import Document
from bookkeeping_state_eval.domain.evidence import (
    BookItemEvidenceAssertion,
    BookItemEvidenceType,
)
from bookkeeping_state_eval.domain.reconciliations import Reconciliation
from bookkeeping_state_eval.domain.routing import RoutingDecision
from bookkeeping_state_eval.state.bookkeeping_state import BookkeepingState
from bookkeeping_state_eval.state.derived import (
    BookkeepingDerivedState,
    build_derived_state,
)
from bookkeeping_state_eval.state.indexes import (
    BookkeepingIndexes,
    build_indexes,
)
from bookkeeping_state_eval.state.relationships import (
    Relationship,
    RelationshipIndex,
    RelationshipType,
    build_relationship_index,
)


class BookkeepingQueries:
    """
    Controlled read interface over one live BookkeepingState.

    Deterministic workers, routing adapters, DAG view builders, and
    reconciliation view builders should use this interface rather than
    manually navigating BookkeepingState internals.

    Derived state, indexes, and relationship topology are cached for the
    current state revision and rebuilt automatically after a transition.

    No query mutates bookkeeping truth.
    """

    __slots__ = (
        "_state",
        "_projection_revision",
        "_derived",
        "_indexes",
        "_relationships",
    )

    def __init__(
        self,
        state: BookkeepingState,
    ) -> None:
        self._state = state

        self._projection_revision: int | None = None
        self._derived: BookkeepingDerivedState | None = None
        self._indexes: BookkeepingIndexes | None = None
        self._relationships: RelationshipIndex | None = None

    # ------------------------------------------------------------------
    # Runtime
    # ------------------------------------------------------------------

    @property
    def session_id(self) -> str:
        return self._state.session_id

    @property
    def state_revision(self) -> int:
        return self._state.revision

    @property
    def derived(self) -> BookkeepingDerivedState:
        self._refresh_if_required()
        assert self._derived is not None
        return self._derived

    @property
    def indexes(self) -> BookkeepingIndexes:
        self._refresh_if_required()
        assert self._indexes is not None
        return self._indexes

    @property
    def relationships(self) -> RelationshipIndex:
        self._refresh_if_required()
        assert self._relationships is not None
        return self._relationships

    @property
    def context(self):
        return self._state.context


    @property
    def persistence_revision(self) -> int:
        return self._state.persistence_revision


    def bank_accounts(
        self,
    ) -> tuple[BankAccount, ...]:
        return tuple(
            self._state.bank_accounts[
                account_id
            ]
            for account_id in sorted(
                self._state.bank_accounts
            )
        )

    def refresh(self) -> None:
        """
        Force reconstruction of every disposable projection.
        """
        derived = build_derived_state(self._state)

        indexes = build_indexes(self._state)

        relationships = build_relationship_index(
            self._state,
            derived=derived,
        )

        revision = self._state.revision

        if indexes.state_revision != revision:
            raise RuntimeError(
                "BookkeepingIndexes were constructed against the wrong "
                f"state revision: indexes={indexes.state_revision}, "
                f"state={revision}"
            )

        if relationships.state_revision != revision:
            raise RuntimeError(
                "RelationshipIndex was constructed against the wrong "
                f"state revision: relationships={relationships.state_revision}, "
                f"state={revision}"
            )

        self._derived = derived
        self._indexes = indexes
        self._relationships = relationships
        self._projection_revision = revision

    # ------------------------------------------------------------------
    # Exact artifact lookup
    # ------------------------------------------------------------------

    def bank_account(
        self,
        bank_account_id: str,
    ) -> BankAccount | None:
        return self._state.get_bank_account(bank_account_id)

    def bank_item(
        self,
        bank_item_id: str,
    ) -> BankItem | None:
        return self._state.get_bank_item(bank_item_id)

    def book_item(
        self,
        book_item_id: str,
    ) -> BookItem | None:
        return self._state.get_book_item(book_item_id)

    def document(
        self,
        document_id: str,
    ) -> Document | None:
        return self._state.get_document(document_id)

    def counterparty(
        self,
        counterparty_id: str,
    ) -> Counterparty | None:
        return self._state.get_counterparty(counterparty_id)

    # ------------------------------------------------------------------
    # Indexed lookup
    # ------------------------------------------------------------------

    def bank_items_for_account(
        self,
        bank_account_id: str,
    ) -> tuple[BankItem, ...]:
        return self.indexes.bank_items_for_account(
            bank_account_id
        )

    def book_items_for_counterparty(
        self,
        counterparty_id: str,
    ) -> tuple[BookItem, ...]:
        return self.indexes.book_items_for_counterparty(
            counterparty_id
        )

    # ------------------------------------------------------------------
    # Routing
    # ------------------------------------------------------------------

    def active_route(
        self,
        book_item_id: str,
    ) -> RoutingDecision | None:
        return self.derived.active_routing_by_book_item.get(
            book_item_id
        )

    def unrouted_book_items(
        self,
    ) -> tuple[BookItem, ...]:
        return tuple(
            self._state.book_items[book_item_id]
            for book_item_id
            in self.derived.book_items_without_active_route
        )

    # ------------------------------------------------------------------
    # Classification
    # ------------------------------------------------------------------

    def active_classification(
        self,
        book_item_id: str,
    ) -> ClassificationDecision | None:
        return (
            self.derived
            .active_classification_by_book_item
            .get(book_item_id)
        )

    def unclassified_book_items(
        self,
    ) -> tuple[BookItem, ...]:
        return tuple(
            self._state.book_items[book_item_id]
            for book_item_id
            in self.derived.book_items_without_active_classification
        )

    # ------------------------------------------------------------------
    # Reconciliation arithmetic
    # ------------------------------------------------------------------

    def bank_allocated_units(
        self,
        bank_item_id: str,
    ) -> int:
        self._require_bank_item(bank_item_id)

        return self.derived.bank_allocated_units.get(
            bank_item_id,
            0,
        )

    def bank_remaining_units(
        self,
        bank_item_id: str,
    ) -> int:
        self._require_bank_item(bank_item_id)

        return self.derived.bank_remaining(
            bank_item_id
        )

    def book_allocated_units(
        self,
        book_item_id: str,
    ) -> int:
        self._require_book_item(book_item_id)

        return self.derived.book_allocated_units.get(
            book_item_id,
            0,
        )

    def book_remaining_units(
        self,
        book_item_id: str,
    ) -> int:
        self._require_book_item(book_item_id)

        return self.derived.book_remaining(
            book_item_id
        )

    def unreconciled_bank_items(
        self,
    ) -> tuple[BankItem, ...]:
        return tuple(
            self._state.bank_items[bank_item_id]
            for bank_item_id
            in self.derived.unreconciled_bank_item_ids
        )

    def partially_reconciled_bank_items(
        self,
    ) -> tuple[BankItem, ...]:
        return tuple(
            self._state.bank_items[bank_item_id]
            for bank_item_id
            in self.derived.partially_reconciled_bank_item_ids
        )

    def fully_reconciled_bank_items(
        self,
    ) -> tuple[BankItem, ...]:
        return tuple(
            self._state.bank_items[bank_item_id]
            for bank_item_id
            in self.derived.fully_reconciled_bank_item_ids
        )

    def unreconciled_book_items(
        self,
    ) -> tuple[BookItem, ...]:
        return tuple(
            self._state.book_items[book_item_id]
            for book_item_id
            in self.derived.unreconciled_book_item_ids
        )

    def partially_reconciled_book_items(
        self,
    ) -> tuple[BookItem, ...]:
        return tuple(
            self._state.book_items[book_item_id]
            for book_item_id
            in self.derived.partially_reconciled_book_item_ids
        )

    def fully_reconciled_book_items(
        self,
    ) -> tuple[BookItem, ...]:
        return tuple(
            self._state.book_items[book_item_id]
            for book_item_id
            in self.derived.fully_reconciled_book_item_ids
        )

    # ------------------------------------------------------------------
    # Reconciliation relationships
    # ------------------------------------------------------------------

    def reconciliations_for_bank_item(
        self,
        bank_item_id: str,
    ) -> tuple[Reconciliation, ...]:
        self._require_bank_item(bank_item_id)

        matches = [
            reconciliation
            for reconciliation
            in self.derived.active_reconciliations.values()
            if any(
                allocation.bank_item_id == bank_item_id
                for allocation
                in reconciliation.bank_allocations
            )
        ]

        return tuple(
            sorted(
                matches,
                key=lambda reconciliation: reconciliation.id,
            )
        )

    def reconciliations_for_book_item(
        self,
        book_item_id: str,
    ) -> tuple[Reconciliation, ...]:
        self._require_book_item(book_item_id)

        matches = [
            reconciliation
            for reconciliation
            in self.derived.active_reconciliations.values()
            if any(
                allocation.book_item_id == book_item_id
                for allocation
                in reconciliation.book_allocations
            )
        ]

        return tuple(
            sorted(
                matches,
                key=lambda reconciliation: reconciliation.id,
            )
        )

    # ------------------------------------------------------------------
    # Evidence
    # ------------------------------------------------------------------

    def evidence_for(
        self,
        artifact_id: str,
    ) -> tuple[Document, ...]:
        """
        Return durable Document artifacts supporting or originating the
        requested artifact.

        This works for:

        - BankItems
        - BookItems
        - ClassificationDecisions
        - Reconciliations
        """

        document_ids: set[str] = set()

        for relationship in self.relationships.outgoing_from(
            artifact_id
        ):
            if relationship.relationship_type in {
                RelationshipType.BANK_ITEM_DOCUMENT,
                RelationshipType.BOOK_ITEM_DOCUMENT,
                RelationshipType.CLASSIFICATION_SUPPORTED_BY_DOCUMENT,
                RelationshipType.RECONCILIATION_SUPPORTED_BY_DOCUMENT,
            }:
                document_ids.add(
                    relationship.target_id
                )

        return tuple(
            self._state.documents[document_id]
            for document_id in sorted(document_ids)
            if document_id in self._state.documents
        )

    def active_evidence_assertion(
        self,
        book_item_id: str,
        evidence_type: BookItemEvidenceType,
    ) -> BookItemEvidenceAssertion | None:
        return self.derived.active_evidence_by_book_item_and_type.get(
            (book_item_id, evidence_type)
        )

    def evidence_assertions_for_book_item(
        self,
        book_item_id: str,
    ) -> tuple[BookItemEvidenceAssertion, ...]:
        return tuple(
            assertion
            for assertion in self._state.book_item_evidence_assertions.values()
            if assertion.book_item_id == book_item_id
        )

    def effective_counterparty(
        self,
        book_item_id: str,
    ) -> Counterparty | None:
        book_item = self._require_book_item(book_item_id)
        assertion = self.active_evidence_assertion(
            book_item_id, BookItemEvidenceType.COUNTERPARTY
        )
        cp_id = (
            assertion.value
            if assertion is not None
            else book_item.counterparty_id
        )
        if cp_id is None:
            return None
        return self._state.get_counterparty(cp_id)

    def effective_counterparty_id(
        self,
        book_item_id: str,
    ) -> str | None:
        book_item = self._require_book_item(book_item_id)
        assertion = self.active_evidence_assertion(
            book_item_id, BookItemEvidenceType.COUNTERPARTY
        )
        return (
            assertion.value
            if assertion is not None
            else book_item.counterparty_id
        )

    def effective_reference(
        self,
        book_item_id: str,
    ) -> str | None:
        book_item = self._require_book_item(book_item_id)
        assertion = self.active_evidence_assertion(
            book_item_id, BookItemEvidenceType.REFERENCE
        )
        return (
            assertion.value
            if assertion is not None
            else book_item.reference
        )

    def effective_description(
        self,
        book_item_id: str,
    ) -> str:
        book_item = self._require_book_item(book_item_id)
        assertion = self.active_evidence_assertion(
            book_item_id, BookItemEvidenceType.DESCRIPTION
        )
        return (
            assertion.value
            if assertion is not None and assertion.value is not None
            else book_item.description
        )

    def effective_evidence_documents(
        self,
        book_item_id: str,
    ) -> tuple[Document, ...]:
        self._require_book_item(book_item_id)
        docs_by_id: dict[str, Document] = {d.id: d for d in self.evidence_for(book_item_id)}
        doc_link_assertion = self.active_evidence_assertion(
            book_item_id, BookItemEvidenceType.DOCUMENT_LINK
        )
        if doc_link_assertion is not None and doc_link_assertion.value is not None:
            doc = self._state.get_document(doc_link_assertion.value)
            if doc is not None:
                docs_by_id[doc.id] = doc
        for assertion in self.derived.active_evidence_by_book_item_and_type.values():
            if assertion.book_item_id == book_item_id:
                for doc_id in assertion.document_ids:
                    doc = self._state.get_document(doc_id)
                    if doc is not None:
                        docs_by_id[doc.id] = doc
        return tuple(docs_by_id[doc_id] for doc_id in sorted(docs_by_id))


    # ------------------------------------------------------------------
    # Relationship navigation
    # ------------------------------------------------------------------

    def related_to(
        self,
        artifact_id: str,
    ) -> tuple[Relationship, ...]:
        return self.relationships.related_to(
            artifact_id
        )

    # ------------------------------------------------------------------
    # Internal helpers
    # ------------------------------------------------------------------

    def _refresh_if_required(self) -> None:
        current_revision = self._state.revision

        if (
            self._projection_revision != current_revision
            or self._derived is None
            or self._indexes is None
            or self._relationships is None
        ):
            self.refresh()

    def _require_bank_item(
        self,
        bank_item_id: str,
    ) -> BankItem:
        item = self._state.get_bank_item(
            bank_item_id
        )

        if item is None:
            raise KeyError(
                f"Unknown BankItem {bank_item_id!r}"
            )

        return item

    def _require_book_item(
        self,
        book_item_id: str,
    ) -> BookItem:
        item = self._state.get_book_item(
            book_item_id
        )

        if item is None:
            raise KeyError(
                f"Unknown BookItem {book_item_id!r}"
            )

        return item