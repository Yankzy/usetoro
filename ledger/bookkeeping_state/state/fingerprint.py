from __future__ import annotations

import hashlib
import json
from collections.abc import Mapping
from typing import Any, Final

from pydantic import BaseModel

from bookkeeping_state.state.bookkeeping_state import BookkeepingState
from bookkeeping_state.state.derived import (
    BookkeepingDerivedState,
    build_derived_state,
)
from bookkeeping_state.state.relationships import (
    RelationshipIndex,
    build_relationship_index,
)


FINGERPRINT_SCHEMA_VERSION: Final[int] = 1


class FingerprintError(RuntimeError):
    """Raised when a deterministic BookkeepingState fingerprint cannot be built."""


def canonical_artifact_projection(
    state: BookkeepingState,
) -> dict[str, Any]:
    """
    Produce the deterministic durable-artifact projection of BookkeepingState.

    This projection contains everything required to represent persisted
    bookkeeping reality but deliberately excludes:

    - RuntimeContext
    - session_id as runtime identity
    - hydrated_at
    - current in-memory state revision
    - reconciliation hypotheses
    - runtime StateEvents
    - indexes
    - caches

    Durable artifacts may themselves contain provenance fields such as
    session_id or state_revision_at_creation. Those are part of the durable
    artifact and therefore remain in the projection.

    This projection can be generated even before derived-state equivalence is
    considered.
    """

    return {
        "fingerprint_schema_version": FINGERPRINT_SCHEMA_VERSION,
        "context": _model_projection(state.context),
        "artifacts": {
            "bank_accounts": _artifact_collection_projection(
                state.bank_accounts
            ),
            "bank_items": _artifact_collection_projection(
                state.bank_items
            ),
            "book_items": _artifact_collection_projection(
                state.book_items
            ),
            "documents": _artifact_collection_projection(
                state.documents
            ),
            "counterparties": _artifact_collection_projection(
                state.counterparties
            ),
            "routing_decisions": _artifact_collection_projection(
                state.routing_decisions
            ),
            "routing_invalidations": _artifact_collection_projection(
                state.routing_invalidations
            ),
            "classifications": _artifact_collection_projection(
                state.classifications
            ),
            "classification_invalidations": (
                _artifact_collection_projection(
                    state.classification_invalidations
                )
            ),
            "reconciliations": _artifact_collection_projection(
                state.reconciliations
            ),
            "reconciliation_invalidations": (
                _artifact_collection_projection(
                    state.reconciliation_invalidations
                )
            ),
            "executed_payment_applications": (
                _artifact_collection_projection(
                    getattr(state, "executed_payment_applications", {})
                )
            ),
            "book_item_evidence_assertions": (
                _artifact_collection_projection(
                    getattr(state, "book_item_evidence_assertions", {})
                )
            ),
            "book_item_evidence_invalidations": (
                _artifact_collection_projection(
                    getattr(state, "book_item_evidence_invalidations", {})
                )
            ),
            "residual_bank_classifications": (
                _artifact_collection_projection(
                    getattr(state, "residual_bank_classifications", {})
                )
            ),
            "residual_bank_classification_invalidations": (
                _artifact_collection_projection(
                    getattr(state, "residual_bank_classification_invalidations", {})
                )
            ),
            "residual_bank_postings": (
                _artifact_collection_projection(
                    getattr(state, "residual_bank_postings", {})
                )
            ),
        },
    }


def canonical_state_projection(
    state: BookkeepingState,
) -> dict[str, Any]:
    """
    Produce the canonical semantic projection of the hydrated bookkeeping world.

    This is stronger than canonical_artifact_projection().

    It includes:

    1. durable authoritative artifacts
    2. current active decision state
    3. reconciliation residual arithmetic
    4. current relationship topology

    It still excludes all session-only runtime machinery.

    Therefore two BookkeepingState instances are semantically equivalent when
    this projection is identical, even if they were hydrated at different
    times or belong to different runtime sessions.
    """

    derived = build_derived_state(state)

    relationships = build_relationship_index(
        state,
        derived=derived,
    )

    projection = canonical_artifact_projection(state)

    projection["derived"] = _derived_projection(derived)

    projection["relationships"] = _relationship_projection(
        relationships
    )

    return projection


def artifact_fingerprint(
    state: BookkeepingState,
) -> str:
    """
    SHA-256 fingerprint of durable bookkeeping artifacts and BookkeepingContext.

    Useful when we want to compare persisted reality independently from derived
    runtime semantics.
    """

    return fingerprint_projection(
        canonical_artifact_projection(state)
    )


def state_fingerprint(
    state: BookkeepingState,
) -> str:
    """
    SHA-256 fingerprint of the complete canonical bookkeeping-state projection.

    Intended eval:

        before = state_fingerprint(state)

        state.close()

        restored = hydrate(...)

        after = state_fingerprint(restored)

        assert before == after
    """

    return fingerprint_projection(
        canonical_state_projection(state)
    )


def fingerprint_projection(
    projection: Mapping[str, Any],
) -> str:
    """
    Produce a deterministic SHA-256 hash for an already-built projection.
    """

    canonical_bytes = canonical_json_bytes(projection)

    return hashlib.sha256(canonical_bytes).hexdigest()


def canonical_json_bytes(
    value: Any,
) -> bytes:
    """
    Deterministically serialize supported projection data.

    JSON object keys are sorted and unnecessary whitespace is removed.
    UTF-8 is used explicitly.

    allow_nan=False prevents non-standard floating-point values from silently
    entering a supposedly deterministic bookkeeping fingerprint.
    """

    try:
        canonical = json.dumps(
            value,
            sort_keys=True,
            separators=(",", ":"),
            ensure_ascii=False,
            allow_nan=False,
        )
    except (TypeError, ValueError) as exc:
        raise FingerprintError(
            "BookkeepingState projection contains a value that cannot be "
            "deterministically serialized"
        ) from exc

    return canonical.encode("utf-8")


def states_are_equivalent(
    left: BookkeepingState,
    right: BookkeepingState,
) -> bool:
    """
    Compare two independently hydrated BookkeepingState instances.

    Runtime session identity is intentionally ignored.
    """

    return state_fingerprint(left) == state_fingerprint(right)


def _artifact_collection_projection(
    artifacts: Mapping[str, BaseModel],
) -> list[dict[str, Any]]:
    """
    Convert an ID-indexed artifact collection into a deterministic ordered list.

    We sort explicitly by canonical artifact ID rather than relying on insertion
    order from persistence or hydration.
    """

    return [
        _model_projection(artifact)
        for _, artifact in sorted(
            artifacts.items(),
            key=lambda item: item[0],
        )
    ]


def _model_projection(
    model: BaseModel,
) -> dict[str, Any]:
    """
    Convert a Pydantic model into JSON-compatible canonical data.

    mode="json" normalizes values such as:
    - datetime
    - date
    - StrEnum
    - tuples
    """

    return model.model_dump(
        mode="json",
        exclude_none=False,
        by_alias=True,
    )


def _derived_projection(
    derived: BookkeepingDerivedState,
) -> dict[str, Any]:
    """
    Reduce disposable derived state to economically meaningful deterministic
    values.
    """

    return {
        "active_routing_by_book_item": {
            book_item_id: decision.id
            for book_item_id, decision in sorted(
                derived.active_routing_by_book_item.items(),
                key=lambda item: item[0],
            )
        },
        "active_classification_by_book_item": {
            book_item_id: classification.id
            for book_item_id, classification in sorted(
                derived.active_classification_by_book_item.items(),
                key=lambda item: item[0],
            )
        },
        "active_residual_bank_classification_by_bank_item": {
            bank_item_id: decision.id
            for bank_item_id, decision in sorted(
                derived.active_residual_bank_classification_by_bank_item.items(),
                key=lambda item: item[0],
            )
        },
        "active_reconciliation_ids": sorted(
            derived.active_reconciliations
        ),
        "bank_allocated_units": _sorted_integer_mapping(
            derived.bank_allocated_units
        ),
        "book_allocated_units": _sorted_integer_mapping(
            derived.book_allocated_units
        ),
        "bank_remaining_units": _sorted_integer_mapping(
            derived.bank_remaining_units
        ),
        "book_remaining_units": _sorted_integer_mapping(
            derived.book_remaining_units
        ),
        "fully_reconciled_bank_item_ids": list(
            derived.fully_reconciled_bank_item_ids
        ),
        "partially_reconciled_bank_item_ids": list(
            derived.partially_reconciled_bank_item_ids
        ),
        "unreconciled_bank_item_ids": list(
            derived.unreconciled_bank_item_ids
        ),
        "fully_reconciled_book_item_ids": list(
            derived.fully_reconciled_book_item_ids
        ),
        "partially_reconciled_book_item_ids": list(
            derived.partially_reconciled_book_item_ids
        ),
        "unreconciled_book_item_ids": list(
            derived.unreconciled_book_item_ids
        ),
        "book_items_without_active_route": list(
            derived.book_items_without_active_route
        ),
        "book_items_without_active_classification": list(
            derived.book_items_without_active_classification
        ),
        "overallocated_bank_item_ids": list(
            derived.overallocated_bank_item_ids
        ),
        "overallocated_book_item_ids": list(
            derived.overallocated_book_item_ids
        ),
    }


def _relationship_projection(
    relationships: RelationshipIndex,
) -> list[dict[str, str]]:
    """
    Serialize the reconstructed relationship topology.

    RelationshipIndex already establishes deterministic ordering, but sorting
    again here makes the fingerprint function independent of that implementation
    detail.
    """

    ordered = sorted(
        relationships.all,
        key=lambda relationship: (
            relationship.relationship_type.value,
            relationship.source_id,
            relationship.target_id,
            relationship.source_artifact_id,
        ),
    )

    return [
        {
            "relationship_type": relationship.relationship_type.value,
            "source_id": relationship.source_id,
            "target_id": relationship.target_id,
            "source_artifact_id": relationship.source_artifact_id,
        }
        for relationship in ordered
    ]


def _sorted_integer_mapping(
    values: Mapping[str, int],
) -> dict[str, int]:
    return {
        key: int(value)
        for key, value in sorted(
            values.items(),
            key=lambda item: item[0],
        )
    }