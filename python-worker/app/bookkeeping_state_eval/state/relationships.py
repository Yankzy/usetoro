from __future__ import annotations

from collections import defaultdict
from enum import StrEnum
from types import MappingProxyType
from typing import Mapping

from pydantic import BaseModel, ConfigDict

from bookkeeping_state_eval.state.bookkeeping_state import BookkeepingState
from bookkeeping_state_eval.state.derived import (
    BookkeepingDerivedState,
    build_derived_state,
)


class RelationshipType(StrEnum):
    """
    Relationship kinds deterministically derived from authoritative
    bookkeeping artifacts.

    Relationships are runtime projections only.

    They are never persisted as an independent source of bookkeeping truth.
    """

    BANK_ITEM_IN_ACCOUNT = "BANK_ITEM_IN_ACCOUNT"

    BOOK_ITEM_COUNTERPARTY = "BOOK_ITEM_COUNTERPARTY"

    BOOK_ITEM_DOCUMENT = "BOOK_ITEM_DOCUMENT"
    BANK_ITEM_DOCUMENT = "BANK_ITEM_DOCUMENT"

    BOOK_ITEM_ROUTED_TO_BANK_ACCOUNT = (
        "BOOK_ITEM_ROUTED_TO_BANK_ACCOUNT"
    )

    BOOK_ITEM_CLASSIFIED_AS = "BOOK_ITEM_CLASSIFIED_AS"

    BANK_ITEM_RECONCILED_WITH_BOOK_ITEM = (
        "BANK_ITEM_RECONCILED_WITH_BOOK_ITEM"
    )

    CLASSIFICATION_SUPPORTED_BY_DOCUMENT = (
        "CLASSIFICATION_SUPPORTED_BY_DOCUMENT"
    )

    RECONCILIATION_SUPPORTED_BY_DOCUMENT = (
        "RECONCILIATION_SUPPORTED_BY_DOCUMENT"
    )


class Relationship(BaseModel):
    """
    One edge in the hydrated bookkeeping relationship topology.

    source_artifact_id identifies the durable artifact whose existence caused
    this relationship to exist.

    Example:

        BOOK-17 --routed_to--> BANK-ACCOUNT-A

    may be caused by:

        source_artifact_id = ROUTING-DECISION-4

    This makes every derived relationship explainable.
    """

    model_config = ConfigDict(
        frozen=True,
        extra="forbid",
    )

    relationship_type: RelationshipType

    source_id: str
    target_id: str

    source_artifact_id: str


class RelationshipIndex:
    """
    Read-only relationship topology derived from one BookkeepingState revision.

    The implementation deliberately uses ordinary Python collections.

    If later evals prove that graph algorithms are required, the same
    relationship semantics can be projected into a dedicated graph engine
    without changing authoritative bookkeeping artifacts.
    """

    __slots__ = (
        "_state_revision",
        "_relationships",
        "_outgoing",
        "_incoming",
        "_by_type",
    )

    def __init__(
        self,
        *,
        state_revision: int,
        relationships: tuple[Relationship, ...],
    ) -> None:
        self._state_revision = state_revision
        self._relationships = relationships

        outgoing: dict[str, list[Relationship]] = defaultdict(list)
        incoming: dict[str, list[Relationship]] = defaultdict(list)
        by_type: dict[
            RelationshipType,
            list[Relationship],
        ] = defaultdict(list)

        for relationship in relationships:
            outgoing[relationship.source_id].append(relationship)
            incoming[relationship.target_id].append(relationship)

            by_type[
                relationship.relationship_type
            ].append(relationship)

        self._outgoing = {
            artifact_id: tuple(values)
            for artifact_id, values in outgoing.items()
        }

        self._incoming = {
            artifact_id: tuple(values)
            for artifact_id, values in incoming.items()
        }

        self._by_type = {
            relationship_type: tuple(values)
            for relationship_type, values in by_type.items()
        }

    @property
    def state_revision(self) -> int:
        return self._state_revision

    @property
    def all(self) -> tuple[Relationship, ...]:
        return self._relationships

    @property
    def outgoing(
        self,
    ) -> Mapping[str, tuple[Relationship, ...]]:
        return MappingProxyType(self._outgoing)

    @property
    def incoming(
        self,
    ) -> Mapping[str, tuple[Relationship, ...]]:
        return MappingProxyType(self._incoming)

    @property
    def by_type(
        self,
    ) -> Mapping[
        RelationshipType,
        tuple[Relationship, ...],
    ]:
        return MappingProxyType(self._by_type)

    def outgoing_from(
        self,
        artifact_id: str,
        *,
        relationship_type: RelationshipType | None = None,
    ) -> tuple[Relationship, ...]:
        relationships = self._outgoing.get(
            artifact_id,
            (),
        )

        if relationship_type is None:
            return relationships

        return tuple(
            relationship
            for relationship in relationships
            if relationship.relationship_type == relationship_type
        )

    def incoming_to(
        self,
        artifact_id: str,
        *,
        relationship_type: RelationshipType | None = None,
    ) -> tuple[Relationship, ...]:
        relationships = self._incoming.get(
            artifact_id,
            (),
        )

        if relationship_type is None:
            return relationships

        return tuple(
            relationship
            for relationship in relationships
            if relationship.relationship_type == relationship_type
        )

    def related_to(
        self,
        artifact_id: str,
    ) -> tuple[Relationship, ...]:
        """
        Return every first-order relationship touching an artifact.
        """

        return (
            self._outgoing.get(artifact_id, ())
            + self._incoming.get(artifact_id, ())
        )

    def has_relationship(
        self,
        *,
        relationship_type: RelationshipType,
        source_id: str,
        target_id: str,
    ) -> bool:
        """
        Efficiently check whether a particular relationship exists.
        """

        return any(
            relationship.target_id == target_id
            and relationship.relationship_type == relationship_type
            for relationship in self._outgoing.get(source_id, ())
        )


def build_relationship_index(
    state: BookkeepingState,
    *,
    derived: BookkeepingDerivedState | None = None,
) -> RelationshipIndex:
    """
    Deterministically construct the current relationship topology.

    No hypotheses are generated here.

    Every relationship must be explainable from an authoritative artifact
    already present in BookkeepingState.
    """

    if derived is None:
        derived = build_derived_state(state)

    relationships: list[Relationship] = []

    _add_bank_account_relationships(
        state,
        relationships,
    )

    _add_book_item_relationships(
        state,
        relationships,
    )

    _add_bank_item_document_relationships(
        state,
        relationships,
    )

    _add_routing_relationships(
        state,
        derived,
        relationships,
    )

    _add_classification_relationships(
        state,
        derived,
        relationships,
    )

    _add_reconciliation_relationships(
        state,
        derived,
        relationships,
    )

    relationships.sort(
        key=lambda relationship: (
            relationship.relationship_type.value,
            relationship.source_id,
            relationship.target_id,
            relationship.source_artifact_id,
        )
    )

    _assert_unique_relationships(relationships)

    return RelationshipIndex(
        state_revision=state.revision,
        relationships=tuple(relationships),
    )


def _add_bank_account_relationships(
    state: BookkeepingState,
    relationships: list[Relationship],
) -> None:
    for bank_item in state.bank_items.values():
        relationships.append(
            Relationship(
                relationship_type=(
                    RelationshipType.BANK_ITEM_IN_ACCOUNT
                ),
                source_id=bank_item.id,
                target_id=bank_item.bank_account_id,
                source_artifact_id=bank_item.id,
            )
        )


def _add_book_item_relationships(
    state: BookkeepingState,
    relationships: list[Relationship],
) -> None:
    for book_item in state.book_items.values():
        if book_item.counterparty_id is not None:
            relationships.append(
                Relationship(
                    relationship_type=(
                        RelationshipType.BOOK_ITEM_COUNTERPARTY
                    ),
                    source_id=book_item.id,
                    target_id=book_item.counterparty_id,
                    source_artifact_id=book_item.id,
                )
            )

        for provenance_ref in book_item.provenance_refs:
            if provenance_ref not in state.documents:
                continue

            relationships.append(
                Relationship(
                    relationship_type=(
                        RelationshipType.BOOK_ITEM_DOCUMENT
                    ),
                    source_id=book_item.id,
                    target_id=provenance_ref,
                    source_artifact_id=book_item.id,
                )
            )


def _add_bank_item_document_relationships(
    state: BookkeepingState,
    relationships: list[Relationship],
) -> None:
    for bank_item in state.bank_items.values():
        for provenance_ref in bank_item.provenance_refs:
            if provenance_ref not in state.documents:
                continue

            relationships.append(
                Relationship(
                    relationship_type=(
                        RelationshipType.BANK_ITEM_DOCUMENT
                    ),
                    source_id=bank_item.id,
                    target_id=provenance_ref,
                    source_artifact_id=bank_item.id,
                )
            )


def _add_routing_relationships(
    state: BookkeepingState,
    derived: BookkeepingDerivedState,
    relationships: list[Relationship],
) -> None:
    del state

    for decision in (
        derived.active_routing_by_book_item.values()
    ):
        relationships.append(
            Relationship(
                relationship_type=(
                    RelationshipType
                    .BOOK_ITEM_ROUTED_TO_BANK_ACCOUNT
                ),
                source_id=decision.book_item_id,
                target_id=decision.bank_account_id,
                source_artifact_id=decision.id,
            )
        )


def _add_classification_relationships(
    state: BookkeepingState,
    derived: BookkeepingDerivedState,
    relationships: list[Relationship],
) -> None:
    for classification in (
        derived.active_classification_by_book_item.values()
    ):
        relationships.append(
            Relationship(
                relationship_type=(
                    RelationshipType.BOOK_ITEM_CLASSIFIED_AS
                ),
                source_id=classification.book_item_id,
                target_id=classification.account_code,
                source_artifact_id=classification.id,
            )
        )

        for evidence_ref in classification.evidence_refs:
            if evidence_ref not in state.documents:
                continue

            relationships.append(
                Relationship(
                    relationship_type=(
                        RelationshipType
                        .CLASSIFICATION_SUPPORTED_BY_DOCUMENT
                    ),
                    source_id=classification.id,
                    target_id=evidence_ref,
                    source_artifact_id=classification.id,
                )
            )


def _add_reconciliation_relationships(
    state: BookkeepingState,
    derived: BookkeepingDerivedState,
    relationships: list[Relationship],
) -> None:
    for reconciliation in (
        derived.active_reconciliations.values()
    ):
        for bank_allocation in reconciliation.bank_allocations:
            for book_allocation in reconciliation.book_allocations:
                relationships.append(
                    Relationship(
                        relationship_type=(
                            RelationshipType
                            .BANK_ITEM_RECONCILED_WITH_BOOK_ITEM
                        ),
                        source_id=bank_allocation.bank_item_id,
                        target_id=book_allocation.book_item_id,
                        source_artifact_id=reconciliation.id,
                    )
                )

        for evidence_ref in reconciliation.evidence_refs:
            if evidence_ref not in state.documents:
                continue

            relationships.append(
                Relationship(
                    relationship_type=(
                        RelationshipType
                        .RECONCILIATION_SUPPORTED_BY_DOCUMENT
                    ),
                    source_id=reconciliation.id,
                    target_id=evidence_ref,
                    source_artifact_id=reconciliation.id,
                )
            )


def _assert_unique_relationships(
    relationships: list[Relationship],
) -> None:
    """
    Detect accidental duplicate relationship construction.

    Two different durable reconciliation artifacts may legitimately establish
    the same BankItem-to-BookItem relationship, so source_artifact_id is part
    of relationship identity.
    """

    seen: set[
        tuple[
            RelationshipType,
            str,
            str,
            str,
        ]
    ] = set()

    for relationship in relationships:
        identity = (
            relationship.relationship_type,
            relationship.source_id,
            relationship.target_id,
            relationship.source_artifact_id,
        )

        if identity in seen:
            raise ValueError(
                "Duplicate derived relationship generated: "
                f"{identity!r}"
            )

        seen.add(identity)