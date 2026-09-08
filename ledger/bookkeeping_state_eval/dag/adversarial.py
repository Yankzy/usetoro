from __future__ import annotations

from typing import Sequence

from bookkeeping_state.dag.models import (
    DagBatchPlan,
    DagClassificationItem,
    DagHoldItem,
)
from bookkeeping_state.dag.protocol import AseClassifier
from bookkeeping_state_eval.dag.simulated_ase import SimulatedAseClassifier
from bookkeeping_state.dag.view import DagView


class AdversarialAseClassifier(AseClassifier):
    """
    Deterministic test classifier implementing AseClassifier with configurable fault injection.

    Allows tests to inject:
    - provider exceptions (network/model failure)
    - force HOLD for specific BookItem IDs
    - stale expected_state_revision
    - duplicate BookItem results
    - unknown BookItem IDs not present in DagView
    - malformed account codes (e.g. empty strings)
    - omitted BookItems (missing responses)
    """

    def __init__(
        self,
        *,
        base_classifier: AseClassifier | None = None,
        force_hold_item_ids: Sequence[str] | set[str] | None = None,
        stale_state_revision: int | None = None,
        duplicate_item_id: str | None = None,
        unknown_item_id: str | None = None,
        malformed_account_codes: dict[str, str] | None = None,
        raise_exception: Exception | None = None,
        omit_item_ids: Sequence[str] | set[str] | None = None,
    ) -> None:
        self._base = base_classifier or SimulatedAseClassifier()
        self._force_hold = set(force_hold_item_ids) if force_hold_item_ids else set()
        self._stale_state_revision = stale_state_revision
        self._duplicate_item_id = duplicate_item_id
        self._unknown_item_id = unknown_item_id
        self._malformed_account_codes = malformed_account_codes or {}
        self._raise_exception = raise_exception
        self._omit_item_ids = set(omit_item_ids) if omit_item_ids else set()

    def classify_view(
        self,
        view: DagView,
        *,
        only_unclassified: bool = True,
    ) -> DagBatchPlan:
        if self._raise_exception is not None:
            raise self._raise_exception

        base_plan = self._base.classify_view(view, only_unclassified=only_unclassified)

        items = list(base_plan.items)
        hold_items = list(base_plan.hold_items)

        # 1. Force HOLD for selected items
        if self._force_hold:
            new_items = []
            for item in items:
                if item.book_item_id in self._force_hold:
                    hold_items.append(
                        DagHoldItem(
                            book_item_id=item.book_item_id,
                            reason="FORCE_HOLD_ADVERSARIAL",
                            rationale="Injected adversarial HOLD",
                            ase_node_id="adversarial_node",
                        )
                    )
                else:
                    new_items.append(item)
            items = new_items

        # 2. Omit items (simulate missing response)
        if self._omit_item_ids:
            items = [it for it in items if it.book_item_id not in self._omit_item_ids]
            hold_items = [h for h in hold_items if h.book_item_id not in self._omit_item_ids]

        # 3. Malformed account codes
        if self._malformed_account_codes:
            new_items = []
            for item in items:
                if item.book_item_id in self._malformed_account_codes:
                    bad_code = self._malformed_account_codes[item.book_item_id]
                    new_items.append(
                        DagClassificationItem(
                            book_item_id=item.book_item_id,
                            account_code=bad_code,
                            confidence=item.confidence,
                            classification_source=item.classification_source,
                            rationale=item.rationale,
                            evidence_refs=item.evidence_refs,
                            supersedes_classification_id=item.supersedes_classification_id,
                            ase_node_id=item.ase_node_id,
                        )
                    )
                else:
                    new_items.append(item)
            items = new_items

        # 4. Inject duplicate item
        if self._duplicate_item_id is not None:
            dup_candidates = [it for it in items if it.book_item_id == self._duplicate_item_id]
            if dup_candidates:
                items.append(dup_candidates[0])
            else:
                items.append(
                    DagClassificationItem(
                        book_item_id=self._duplicate_item_id,
                        account_code="6111",
                        confidence=0.5,
                        ase_node_id="adversarial_dup",
                    )
                )

        # 5. Inject unknown item ID
        if self._unknown_item_id is not None:
            items.append(
                DagClassificationItem(
                    book_item_id=self._unknown_item_id,
                    account_code="6111",
                    confidence=0.5,
                    ase_node_id="adversarial_unknown",
                )
            )

        # 6. Revision: stale revision
        rev = (
            self._stale_state_revision
            if self._stale_state_revision is not None
            else base_plan.expected_state_revision
        )

        return DagBatchPlan(
            plan_id=base_plan.plan_id,
            session_id=base_plan.session_id,
            expected_state_revision=rev,
            items=tuple(items),
            hold_items=tuple(hold_items),
            dag_id=base_plan.dag_id,
            dag_run_id=base_plan.dag_run_id,
        )
