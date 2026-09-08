from __future__ import annotations

import pytest

from bookkeeping_state.state.relationships import (
    Relationship,
    RelationshipType,
    _assert_unique_relationships,
    build_relationship_index,
)
from tests.factories import (
    account,
    bank_item,
    book_item,
    classification,
    classification_invalidation,
    counterparty,
    document,
    reconciliation,
    reconciliation_invalidation,
    routing,
    routing_invalidation,
    state,
)


def test_relationship_index_reconstructs_current_topology_and_navigation() -> None:
    live = state(
        revision=7,
        bank_accounts=(account(),),
        bank_items=(bank_item(provenance_refs=("doc-1",)),),
        book_items=(book_item(counterparty_id="cp-1", provenance_refs=("doc-1",)),),
        documents=(document(),),
        counterparties=(counterparty("cp-1"),),
        routing_decisions=(routing(),),
        classifications=(classification(evidence_refs=("doc-1",)),),
        reconciliations=(reconciliation(evidence_refs=("doc-1",)),),
    )
    index = build_relationship_index(live)

    assert index.state_revision == 7
    assert index.has_relationship(
        relationship_type=RelationshipType.BANK_ITEM_IN_ACCOUNT,
        source_id="bank-1",
        target_id="account-1",
    )
    assert index.has_relationship(
        relationship_type=RelationshipType.BOOK_ITEM_COUNTERPARTY,
        source_id="book-1",
        target_id="cp-1",
    )
    assert index.has_relationship(
        relationship_type=RelationshipType.BOOK_ITEM_DOCUMENT,
        source_id="book-1",
        target_id="doc-1",
    )
    assert index.has_relationship(
        relationship_type=RelationshipType.BANK_ITEM_DOCUMENT,
        source_id="bank-1",
        target_id="doc-1",
    )
    assert index.has_relationship(
        relationship_type=RelationshipType.BOOK_ITEM_ROUTED_TO_BANK_ACCOUNT,
        source_id="book-1",
        target_id="account-1",
    )
    assert index.has_relationship(
        relationship_type=RelationshipType.BOOK_ITEM_CLASSIFIED_AS,
        source_id="book-1",
        target_id="6111",
    )
    assert index.has_relationship(
        relationship_type=RelationshipType.BANK_ITEM_RECONCILED_WITH_BOOK_ITEM,
        source_id="bank-1",
        target_id="book-1",
    )
    assert index.has_relationship(
        relationship_type=RelationshipType.CLASSIFICATION_SUPPORTED_BY_DOCUMENT,
        source_id="classification-1",
        target_id="doc-1",
    )
    assert index.has_relationship(
        relationship_type=RelationshipType.RECONCILIATION_SUPPORTED_BY_DOCUMENT,
        source_id="reconciliation-1",
        target_id="doc-1",
    )
    assert index.outgoing_from("book-1")
    assert index.incoming_to("doc-1")
    assert set(index.related_to("book-1")) == set(index.outgoing_from("book-1")) | set(index.incoming_to("book-1"))


def test_current_relationships_exclude_superseded_and_invalidated_decisions() -> None:
    live = state(
        bank_accounts=(account(),),
        bank_items=(bank_item(),),
        book_items=(book_item(),),
        routing_decisions=(routing("route-1"), routing("route-2", supersedes="route-1")),
        routing_invalidations=(routing_invalidation(target="route-2"),),
        classifications=(classification("class-1"), classification("class-2", supersedes="class-1")),
        classification_invalidations=(classification_invalidation(target="class-2"),),
        reconciliations=(reconciliation(),),
        reconciliation_invalidations=(reconciliation_invalidation(),),
    )
    index = build_relationship_index(live)
    current_types = {relationship.relationship_type for relationship in index.all}

    assert RelationshipType.BOOK_ITEM_ROUTED_TO_BANK_ACCOUNT not in current_types
    assert RelationshipType.BOOK_ITEM_CLASSIFIED_AS not in current_types
    assert RelationshipType.BANK_ITEM_RECONCILED_WITH_BOOK_ITEM not in current_types


def test_relationship_order_is_deterministic_and_duplicate_edges_are_detected() -> None:
    live = state(
        bank_accounts=(account("account-2"), account("account-1")),
        bank_items=(bank_item("bank-2", account_id="account-2"), bank_item("bank-1")),
        book_items=(book_item("book-2"), book_item("book-1")),
    )
    index = build_relationship_index(live)
    ordering = [
        (edge.relationship_type.value, edge.source_id, edge.target_id, edge.source_artifact_id)
        for edge in index.all
    ]
    assert ordering == sorted(ordering)

    duplicate = Relationship(
        relationship_type=RelationshipType.BANK_ITEM_IN_ACCOUNT,
        source_id="bank-1",
        target_id="account-1",
        source_artifact_id="bank-1",
    )
    with pytest.raises(ValueError, match="Duplicate derived relationship"):
        _assert_unique_relationships([duplicate, duplicate])
