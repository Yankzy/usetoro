from __future__ import annotations

from dataclasses import dataclass, field
from datetime import datetime
from typing import Sequence

from bookkeeping_state.dag.protocol import AseClassifier
from bookkeeping_state.domain.bank import BankAccount, BankItem
from bookkeeping_state.domain.books import BookItem
from bookkeeping_state.domain.classifications import (
    ClassificationDecision,
    ClassificationInvalidation,
)
from bookkeeping_state.domain.context import BookkeepingContext
from bookkeeping_state.domain.counterparties import Counterparty
from bookkeeping_state.domain.documents import Document
from bookkeeping_state.domain.reconciliations import (
    Reconciliation,
    ReconciliationInvalidation,
)
from bookkeeping_state.domain.routing import (
    RoutingDecision,
    RoutingDecisionInvalidation,
)
from bookkeeping_state.persistence.repository import BookkeepingSnapshot
from bookkeeping_state.reconciliation.service import ReconciliationService
from bookkeeping_state.routing.scorer import RoutingSemanticScoreProvider
from bookkeeping_state_eval.scenarios.expected_truth import (
    ExpectedTruth,
    ExpectedTruthVerdict,
)
from bookkeeping_state.session.result import SessionResult


@dataclass(frozen=True, slots=True)
class ScenarioDefinition:
    """
    Immutable scenario definition describing an initial durable world and its
    independently expected closing truth.

    Does not contain BookkeepingState.
    """

    scenario_id: str
    name: str
    description: str
    context: BookkeepingContext
    bank_accounts: tuple[BankAccount, ...]
    bank_items: tuple[BankItem, ...]
    book_items: tuple[BookItem, ...]
    documents: tuple[Document, ...] = ()
    counterparties: tuple[Counterparty, ...] = ()
    routing_decisions: tuple[RoutingDecision, ...] = ()
    routing_invalidations: tuple[RoutingDecisionInvalidation, ...] = ()
    classifications: tuple[ClassificationDecision, ...] = ()
    classification_invalidations: tuple[ClassificationInvalidation, ...] = ()
    reconciliations: tuple[Reconciliation, ...] = ()
    reconciliation_invalidations: tuple[ReconciliationInvalidation, ...] = ()

    routing_semantic_provider: RoutingSemanticScoreProvider | None = None
    dag_classifier: AseClassifier | None = None
    reconciliation_service: ReconciliationService | None = None
    clock_time: datetime | None = None

    expected_truth: ExpectedTruth = field(default_factory=ExpectedTruth)

    def to_initial_snapshot(
        self,
        persistence_revision: int = 1,
    ) -> BookkeepingSnapshot:
        """
        Produce a durable BookkeepingSnapshot to seed persistence.
        """
        return BookkeepingSnapshot(
            persistence_revision=persistence_revision,
            context=self.context,
            bank_accounts=self.bank_accounts,
            bank_items=self.bank_items,
            book_items=self.book_items,
            documents=self.documents,
            counterparties=self.counterparties,
            routing_decisions=self.routing_decisions,
            routing_invalidations=self.routing_invalidations,
            classifications=self.classifications,
            classification_invalidations=self.classification_invalidations,
            reconciliations=self.reconciliations,
            reconciliation_invalidations=self.reconciliation_invalidations,
        )


@dataclass(frozen=True, slots=True)
class ScenarioResult:
    """
    Immutable detached result of one scenario evaluation run.

    Strict boundary: does not expose BookkeepingState, commands, hypotheses,
    TransitionBatch, services, or repository instances.
    """

    scenario_id: str
    is_pass: bool
    expected_truth_verdict: ExpectedTruthVerdict
    mismatches: tuple[str, ...]
    session_result: SessionResult
    final_persistence_revision: int
    final_durable_fingerprint: str | None
    failure_reason: str | None = None
