from __future__ import annotations

from collections import defaultdict
from datetime import date
from types import MappingProxyType
from typing import Any, Callable, Mapping, TypeVar

from bookkeeping_state.domain.bank import BankItem
from bookkeeping_state.domain.books import BookItem
from bookkeeping_state.domain.documents import Document, DocumentType
from bookkeeping_state.state.bookkeeping_state import BookkeepingState


class BookkeepingIndexes:
    """
    Disposable lookup indexes built from authoritative BookkeepingState artifacts.

    These indexes exist only to make state navigation efficient.

    They contain no bookkeeping truth that cannot be reconstructed from the
    authoritative artifacts.

    Every index records the BookkeepingState revision it was built from so
    callers can detect stale projections.
    """

    __slots__ = (
        "_state_revision",
        "_bank_items_by_account",
        "_bank_items_by_date",
        "_book_items_by_date",
        "_book_items_by_counterparty",
        "_documents_by_type",
        "_documents_by_source_reference",
        "_artifacts_by_provenance_ref",
    )

    def __init__(
        self,
        *,
        state_revision: int,
        bank_items_by_account: dict[str, tuple[BankItem, ...]],
        bank_items_by_date: dict[date, tuple[BankItem, ...]],
        book_items_by_date: dict[date, tuple[BookItem, ...]],
        book_items_by_counterparty: dict[str, tuple[BookItem, ...]],
        documents_by_type: dict[DocumentType, tuple[Document, ...]],
        documents_by_source_reference: dict[str, Document],
        artifacts_by_provenance_ref: dict[str, tuple[str, ...]],
    ) -> None:
        self._state_revision = state_revision

        self._bank_items_by_account = bank_items_by_account
        self._bank_items_by_date = bank_items_by_date

        self._book_items_by_date = book_items_by_date
        self._book_items_by_counterparty = book_items_by_counterparty

        self._documents_by_type = documents_by_type
        self._documents_by_source_reference = documents_by_source_reference

        self._artifacts_by_provenance_ref = artifacts_by_provenance_ref

    @property
    def state_revision(self) -> int:
        return self._state_revision

    # ------------------------------------------------------------------
    # Bank indexes
    # ------------------------------------------------------------------

    @property
    def bank_items_by_account(
        self,
    ) -> Mapping[str, tuple[BankItem, ...]]:
        return MappingProxyType(self._bank_items_by_account)

    @property
    def bank_items_by_date(
        self,
    ) -> Mapping[date, tuple[BankItem, ...]]:
        return MappingProxyType(self._bank_items_by_date)

    def bank_items_for_account(
        self,
        bank_account_id: str,
    ) -> tuple[BankItem, ...]:
        return self._bank_items_by_account.get(
            bank_account_id,
            (),
        )

    def bank_items_on(
        self,
        transaction_date: date,
    ) -> tuple[BankItem, ...]:
        return self._bank_items_by_date.get(
            transaction_date,
            (),
        )

    # ------------------------------------------------------------------
    # Book indexes
    # ------------------------------------------------------------------

    @property
    def book_items_by_date(
        self,
    ) -> Mapping[date, tuple[BookItem, ...]]:
        return MappingProxyType(self._book_items_by_date)

    @property
    def book_items_by_counterparty(
        self,
    ) -> Mapping[str, tuple[BookItem, ...]]:
        return MappingProxyType(
            self._book_items_by_counterparty
        )

    def book_items_on(
        self,
        transaction_date: date,
    ) -> tuple[BookItem, ...]:
        return self._book_items_by_date.get(
            transaction_date,
            (),
        )

    def book_items_for_counterparty(
        self,
        counterparty_id: str,
    ) -> tuple[BookItem, ...]:
        return self._book_items_by_counterparty.get(
            counterparty_id,
            (),
        )

    # ------------------------------------------------------------------
    # Document indexes
    # ------------------------------------------------------------------

    @property
    def documents_by_type(
        self,
    ) -> Mapping[DocumentType, tuple[Document, ...]]:
        return MappingProxyType(self._documents_by_type)

    @property
    def documents_by_source_reference(
        self,
    ) -> Mapping[str, Document]:
        return MappingProxyType(
            self._documents_by_source_reference
        )

    def documents_of_type(
        self,
        document_type: DocumentType,
    ) -> tuple[Document, ...]:
        return self._documents_by_type.get(
            document_type,
            (),
        )

    def document_for_source_reference(
        self,
        source_reference: str,
    ) -> Document | None:
        return self._documents_by_source_reference.get(
            source_reference
        )

    # ------------------------------------------------------------------
    # Provenance reverse index
    # ------------------------------------------------------------------

    @property
    def artifacts_by_provenance_ref(
        self,
    ) -> Mapping[str, tuple[str, ...]]:
        """
        Reverse lookup:

            provenance/document ID
                ->
            artifact IDs derived from or supported by it
        """
        return MappingProxyType(
            self._artifacts_by_provenance_ref
        )

    def artifacts_for_provenance(
        self,
        provenance_ref: str,
    ) -> tuple[str, ...]:
        return self._artifacts_by_provenance_ref.get(
            provenance_ref,
            (),
        )


def build_indexes(
    state: BookkeepingState,
) -> BookkeepingIndexes:
    """
    Deterministically build disposable lookup indexes for one state revision.

    No bookkeeping conclusions are generated here.

    This function only reorganizes already-existing authoritative artifacts for
    efficient runtime access.
    """

    bank_items_by_account: dict[
        str,
        list[BankItem],
    ] = defaultdict(list)

    bank_items_by_date: dict[
        date,
        list[BankItem],
    ] = defaultdict(list)

    book_items_by_date: dict[
        date,
        list[BookItem],
    ] = defaultdict(list)

    book_items_by_counterparty: dict[
        str,
        list[BookItem],
    ] = defaultdict(list)

    documents_by_type: dict[
        DocumentType,
        list[Document],
    ] = defaultdict(list)

    documents_by_source_reference: dict[
        str,
        Document,
    ] = {}

    artifacts_by_provenance_ref: dict[
        str,
        list[str],
    ] = defaultdict(list)

    # ------------------------------------------------------------------
    # Bank items
    # ------------------------------------------------------------------

    for bank_item in state.bank_items.values():
        bank_items_by_account[
            bank_item.bank_account_id
        ].append(bank_item)

        bank_items_by_date[
            bank_item.date
        ].append(bank_item)

        for provenance_ref in bank_item.provenance_refs:
            artifacts_by_provenance_ref[
                provenance_ref
            ].append(bank_item.id)

    # ------------------------------------------------------------------
    # Book items
    # ------------------------------------------------------------------

    for book_item in state.book_items.values():
        book_items_by_date[
            book_item.date
        ].append(book_item)

        if book_item.counterparty_id is not None:
            book_items_by_counterparty[
                book_item.counterparty_id
            ].append(book_item)

        for provenance_ref in book_item.provenance_refs:
            artifacts_by_provenance_ref[
                provenance_ref
            ].append(book_item.id)

    # ------------------------------------------------------------------
    # Documents
    # ------------------------------------------------------------------

    for document in state.documents.values():
        documents_by_type[
            document.document_type
        ].append(document)

        if document.source_reference is not None:
            existing = documents_by_source_reference.get(
                document.source_reference
            )

            if existing is not None and existing.id != document.id:
                raise ValueError(
                    "Duplicate Document source_reference "
                    f"{document.source_reference!r}: "
                    f"{existing.id!r} and {document.id!r}"
                )

            documents_by_source_reference[
                document.source_reference
            ] = document

    # ------------------------------------------------------------------
    # Decision-artifact evidence
    # ------------------------------------------------------------------

    for classification in state.classifications.values():
        for evidence_ref in classification.evidence_refs:
            artifacts_by_provenance_ref[
                evidence_ref
            ].append(classification.id)

    for reconciliation in state.reconciliations.values():
        for evidence_ref in reconciliation.evidence_refs:
            artifacts_by_provenance_ref[
                evidence_ref
            ].append(reconciliation.id)

    return BookkeepingIndexes(
        state_revision=state.revision,

        bank_items_by_account=_freeze_grouped_index(
            bank_items_by_account,
            sort_key=_bank_item_sort_key,
        ),

        bank_items_by_date=_freeze_grouped_index(
            bank_items_by_date,
            sort_key=_bank_item_sort_key,
        ),

        book_items_by_date=_freeze_grouped_index(
            book_items_by_date,
            sort_key=_book_item_sort_key,
        ),

        book_items_by_counterparty=_freeze_grouped_index(
            book_items_by_counterparty,
            sort_key=_book_item_sort_key,
        ),

        documents_by_type=_freeze_grouped_index(
            documents_by_type,
            sort_key=_document_sort_key,
        ),

        documents_by_source_reference=dict(
            sorted(
                documents_by_source_reference.items(),
                key=lambda item: item[0],
            )
        ),

        artifacts_by_provenance_ref={
            provenance_ref: tuple(
                sorted(set(artifact_ids))
            )
            for provenance_ref, artifact_ids in sorted(
                artifacts_by_provenance_ref.items(),
                key=lambda item: item[0],
            )
        },
    )


def _bank_item_sort_key(
    item: BankItem,
) -> tuple[date, str]:
    return (
        item.date,
        item.id,
    )


def _book_item_sort_key(
    item: BookItem,
) -> tuple[date, str]:
    return (
        item.date,
        item.id,
    )


def _document_sort_key(
    document: Document,
) -> tuple[str, str]:
    return (
        document.received_at.isoformat(),
        document.id,
    )


_K = TypeVar("_K")
_V = TypeVar("_V")


def _freeze_grouped_index(
    grouped: Mapping[_K, list[_V]],
    *,
    sort_key: Callable[[_V], Any],
) -> dict[_K, tuple[_V, ...]]:
    """
    Convert a mutable defaultdict/list index into deterministic tuple-backed
    storage.

    This helper is intentionally internal. BookkeepingIndexes exposes only
    read-only views.
    """
    return {
        key: tuple(
            sorted(
                values,
                key=sort_key,
            )
        )
        for key, values in grouped.items()
    }