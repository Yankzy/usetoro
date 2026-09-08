from __future__ import annotations

import re
from typing import Callable, Sequence

from bookkeeping_state_eval.dag.models import (
    DagBatchPlan,
    DagClassificationItem,
    DagHoldItem,
)
from bookkeeping_state_eval.dag.protocol import AseClassifier
from bookkeeping_state_eval.dag.view import DagView, DagViewItem
from bookkeeping_state_eval.domain.classifications import ClassificationSource


# Deterministic pattern matching rules simulating Go ASE DAG classification nodes
DEFAULT_RULES: tuple[tuple[str, str, float, str], ...] = (
    (r"(?i)\b(payroll|salary|salaire|remuneration)\b", "6171", 0.99, "payroll_node"),
    (r"(?i)\b(rent|loyer|bail|location)\b", "6131", 0.99, "rent_node"),
    (r"(?i)\b(software|aws|saas|google|cloud|hosting)\b", "6181", 0.99, "it_software_node"),
    (r"(?i)\b(office|fourniture|supplies|papeterie)\b", "6125", 0.99, "office_supplies_node"),
    (r"(?i)\b(client|customer|virement client|encaissement)\b", "3421", 0.98, "customer_receipt_node"),
    (r"(?i)\b(fournisseur|vendor|supplier|reglement)\b", "4411", 0.98, "supplier_payment_node"),
    (r"(?i)\b(tax|dgi|tgr|impot|tva)\b", "4455", 0.98, "tax_node"),
)


class SimulatedAseClassifier(AseClassifier):
    """
    Deterministic simulator replicating Go ASE DAG execution against a DagView.

    Implements the AseClassifier protocol. When no deterministic semantic rule matches,
    it returns a detached DagHoldItem (HOLD_INSUFFICIENT_EVIDENCE) and emits no
    classification command.
    """

    def __init__(
        self,
        *,
        rules: Sequence[tuple[str, str, float, str]] = DEFAULT_RULES,
        plan_id_factory: Callable[[], str] | None = None,
    ) -> None:
        self._rules = [
            (re.compile(pattern), code, conf, node)
            for pattern, code, conf, node in rules
        ]
        self._plan_id_factory = plan_id_factory or (lambda: "sim-ase-run")

    def classify_view(
        self,
        view: DagView,
        *,
        only_unclassified: bool = True,
    ) -> DagBatchPlan:
        """
        Evaluate DAG rules against all eligible items in the view.
        """
        plan_id = self._plan_id_factory()
        items: list[DagClassificationItem] = []
        hold_items: list[DagHoldItem] = []

        for item in view.items:
            if only_unclassified and item.is_classified:
                continue

            outcome = self._classify_item(item, plan_id=plan_id)
            if isinstance(outcome, DagClassificationItem):
                items.append(outcome)
            elif isinstance(outcome, DagHoldItem):
                hold_items.append(outcome)

        return DagBatchPlan(
            plan_id=plan_id,
            session_id=view.session_id,
            expected_state_revision=view.state_revision,
            items=tuple(items),
            hold_items=tuple(hold_items),
            dag_id="pcm_bank_cash_accounting_dag",
            dag_run_id=plan_id,
        )

    def _classify_item(
        self,
        item: DagViewItem,
        *,
        plan_id: str,
    ) -> DagClassificationItem | DagHoldItem:
        text_to_match = f"{item.description or ''} {item.counterparty_name or ''} {item.reference or ''}"

        for regex, code, conf, node_id in self._rules:
            if regex.search(text_to_match):
                return DagClassificationItem(
                    book_item_id=item.book_item_id,
                    account_code=code,
                    confidence=conf,
                    classification_source=ClassificationSource.ASE_DAG,
                    rationale=f"Matched simulated ASE rule {node_id} on {item.book_item_id}",
                    supersedes_classification_id=item.existing_classification_id,
                    ase_node_id=node_id,
                )

        # No deterministic semantic rule matched -> HOLD (insufficient evidence)
        return DagHoldItem(
            book_item_id=item.book_item_id,
            reason="HOLD_INSUFFICIENT_EVIDENCE",
            rationale=f"No deterministic semantic rule matched for {item.book_item_id}",
            ase_node_id="hold_node",
        )
