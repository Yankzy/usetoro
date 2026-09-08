from __future__ import annotations

from collections.abc import Iterable, Mapping
from types import MappingProxyType
from typing import TypeVar

from bookkeeping_state.domain.bank import BankAccount, BankItem
from bookkeeping_state.domain.books import BookItem
from bookkeeping_state.domain.classifications import (
    ClassificationDecision,
    ClassificationInvalidation,
)
from bookkeeping_state.domain.context import (
    BookkeepingContext,
    RuntimeContext,
)
from bookkeeping_state.domain.counterparties import Counterparty
from bookkeeping_state.domain.documents import Document
from bookkeeping_state.domain.events import StateEvent
from bookkeeping_state.domain.evidence import (
    BookItemEvidenceAssertion,
    BookItemEvidenceInvalidation,
)
from bookkeeping_state.domain.hypotheses import ReconciliationHypothesis
from bookkeeping_state.domain.reconciliations import (
    Reconciliation,
    ReconciliationInvalidation,
)
from bookkeeping_state.domain.routing import (
    RoutingDecision,
    RoutingDecisionInvalidation,
)


class BookkeepingStateError(RuntimeError):
    """Base error for BookkeepingState runtime failures."""


class DuplicateArtifactError(BookkeepingStateError):
    """Raised when an artifact with the same identity is inserted twice."""


class BookkeepingStateClosedError(BookkeepingStateError):
    """Raised when code attempts to interact with a destroyed state."""


class StateRevisionConflictError(BookkeepingStateError):
    """Raised when a transition targets a stale BookkeepingState revision."""


ArtifactT = TypeVar("ArtifactT")


class BookkeepingState:
    """
    Temporary in-memory representation of one company's bookkeeping world.

    BookkeepingState is NOT persistence.

    It is hydrated from durable artifacts, used while a bookkeeping session
    executes, incrementally updated as durable bookkeeping truth changes, and
    destroyed when the session ends.

    Authoritative bookkeeping artifacts remain immutable domain objects.

    The container itself is mutable because an active session evolves through:

        S0 -> S1 -> S2 -> ... -> Sn

    Mutation is deliberately exposed only through package-internal methods.
    The transition engine will become the public write path.

    Runtime-only reconciliation hypotheses and state events live here as well,
    but they are not required to reconstruct bookkeeping truth.
    """

    __slots__ = (
        "_context",
        "_runtime",
        "_bank_accounts",
        "_bank_items",
        "_book_items",
        "_documents",
        "_counterparties",
        "_routing_decisions",
        "_routing_invalidations",
        "_classifications",
        "_classification_invalidations",
        "_reconciliations",
        "_reconciliation_invalidations",
        "_book_item_evidence_assertions",
        "_book_item_evidence_invalidations",
        "_reconciliation_hypotheses",
        "_events",
        "_closed",
    )

    def __init__(
        self,
        *,
        context: BookkeepingContext,
        runtime: RuntimeContext,
        bank_accounts: Iterable[BankAccount] = (),
        bank_items: Iterable[BankItem] = (),
        book_items: Iterable[BookItem] = (),
        documents: Iterable[Document] = (),
        counterparties: Iterable[Counterparty] = (),
        routing_decisions: Iterable[RoutingDecision] = (),
        routing_invalidations: Iterable[RoutingDecisionInvalidation] = (),
        classifications: Iterable[ClassificationDecision] = (),
        classification_invalidations: Iterable[
            ClassificationInvalidation
        ] = (),
        reconciliations: Iterable[Reconciliation] = (),
        reconciliation_invalidations: Iterable[
            ReconciliationInvalidation
        ] = (),
        book_item_evidence_assertions: Iterable[
            BookItemEvidenceAssertion
        ] = (),
        book_item_evidence_invalidations: Iterable[
            BookItemEvidenceInvalidation
        ] = (),
    ) -> None:
        self._context = context
        self._runtime = runtime

        self._bank_accounts = self._index_unique(
            bank_accounts,
            artifact_name="BankAccount",
        )
        self._bank_items = self._index_unique(
            bank_items,
            artifact_name="BankItem",
        )
        self._book_items = self._index_unique(
            book_items,
            artifact_name="BookItem",
        )
        self._documents = self._index_unique(
            documents,
            artifact_name="Document",
        )
        self._counterparties = self._index_unique(
            counterparties,
            artifact_name="Counterparty",
        )

        self._routing_decisions = self._index_unique(
            routing_decisions,
            artifact_name="RoutingDecision",
        )
        self._routing_invalidations = self._index_unique(
            routing_invalidations,
            artifact_name="RoutingDecisionInvalidation",
        )

        self._classifications = self._index_unique(
            classifications,
            artifact_name="ClassificationDecision",
        )
        self._classification_invalidations = self._index_unique(
            classification_invalidations,
            artifact_name="ClassificationInvalidation",
        )

        self._reconciliations = self._index_unique(
            reconciliations,
            artifact_name="Reconciliation",
        )
        self._reconciliation_invalidations = self._index_unique(
            reconciliation_invalidations,
            artifact_name="ReconciliationInvalidation",
        )

        self._book_item_evidence_assertions = self._index_unique(
            book_item_evidence_assertions,
            artifact_name="BookItemEvidenceAssertion",
        )
        self._book_item_evidence_invalidations = self._index_unique(
            book_item_evidence_invalidations,
            artifact_name="BookItemEvidenceInvalidation",
        )

        self._reconciliation_hypotheses: dict[
            str,
            ReconciliationHypothesis,
        ] = {}

        self._events: list[StateEvent] = []

        self._closed = False

    # ------------------------------------------------------------------
    # Identity / lifecycle
    # ------------------------------------------------------------------

    @property
    def context(self) -> BookkeepingContext:
        self._ensure_open()
        return self._context

    @property
    def runtime(self) -> RuntimeContext:
        self._ensure_open()
        return self._runtime

    @property
    def revision(self) -> int:
        self._ensure_open()
        return self._runtime.state_revision

    @property
    def session_id(self) -> str:
        self._ensure_open()
        return self._runtime.session_id

    @property
    def is_closed(self) -> bool:
        return self._closed
    
    @property
    def persistence_revision(self) -> int:
        return self._runtime.persistence_revision
    
    @property
    def source_revisions(self) -> Mapping[str, int]:
        return self._runtime.source_revisions

    def close(self) -> None:
        """
        Destroy this hydrated BookkeepingState instance.

        Closing state does not alter durable bookkeeping truth.

        References held by this object are released so the runtime state can be
        garbage-collected. Any subsequent attempt to inspect or mutate the
        state raises BookkeepingStateClosedError.

        A new bookkeeping session must hydrate a fresh BookkeepingState.
        """
        if self._closed:
            return

        self._bank_accounts.clear()
        self._bank_items.clear()
        self._book_items.clear()
        self._documents.clear()
        self._counterparties.clear()

        self._routing_decisions.clear()
        self._routing_invalidations.clear()

        self._classifications.clear()
        self._classification_invalidations.clear()

        self._reconciliations.clear()
        self._reconciliation_invalidations.clear()

        self._book_item_evidence_assertions.clear()
        self._book_item_evidence_invalidations.clear()

        self._reconciliation_hypotheses.clear()
        self._events.clear()

        self._closed = True

    # ------------------------------------------------------------------
    # Authoritative artifact collections
    # ------------------------------------------------------------------

    @property
    def bank_accounts(self) -> Mapping[str, BankAccount]:
        self._ensure_open()
        return MappingProxyType(self._bank_accounts)

    @property
    def bank_items(self) -> Mapping[str, BankItem]:
        self._ensure_open()
        return MappingProxyType(self._bank_items)

    @property
    def book_items(self) -> Mapping[str, BookItem]:
        self._ensure_open()
        return MappingProxyType(self._book_items)

    @property
    def documents(self) -> Mapping[str, Document]:
        self._ensure_open()
        return MappingProxyType(self._documents)

    @property
    def counterparties(self) -> Mapping[str, Counterparty]:
        self._ensure_open()
        return MappingProxyType(self._counterparties)

    @property
    def routing_decisions(self) -> Mapping[str, RoutingDecision]:
        self._ensure_open()
        return MappingProxyType(self._routing_decisions)

    @property
    def routing_invalidations(
        self,
    ) -> Mapping[str, RoutingDecisionInvalidation]:
        self._ensure_open()
        return MappingProxyType(self._routing_invalidations)

    @property
    def classifications(self) -> Mapping[str, ClassificationDecision]:
        self._ensure_open()
        return MappingProxyType(self._classifications)

    @property
    def classification_invalidations(
        self,
    ) -> Mapping[str, ClassificationInvalidation]:
        self._ensure_open()
        return MappingProxyType(self._classification_invalidations)

    @property
    def reconciliations(self) -> Mapping[str, Reconciliation]:
        self._ensure_open()
        return MappingProxyType(self._reconciliations)

    @property
    def reconciliation_invalidations(
        self,
    ) -> Mapping[str, ReconciliationInvalidation]:
        self._ensure_open()
        return MappingProxyType(self._reconciliation_invalidations)

    @property
    def book_item_evidence_assertions(
        self,
    ) -> Mapping[str, BookItemEvidenceAssertion]:
        self._ensure_open()
        return MappingProxyType(self._book_item_evidence_assertions)

    @property
    def book_item_evidence_invalidations(
        self,
    ) -> Mapping[str, BookItemEvidenceInvalidation]:
        self._ensure_open()
        return MappingProxyType(self._book_item_evidence_invalidations)

    # ------------------------------------------------------------------
    # Runtime-only state
    # ------------------------------------------------------------------

    @property
    def reconciliation_hypotheses(
        self,
    ) -> Mapping[str, ReconciliationHypothesis]:
        self._ensure_open()
        return MappingProxyType(self._reconciliation_hypotheses)

    @property
    def events(self) -> tuple[StateEvent, ...]:
        self._ensure_open()
        return tuple(self._events)

    # ------------------------------------------------------------------
    # Exact artifact lookups
    # ------------------------------------------------------------------

    def get_bank_account(self, artifact_id: str) -> BankAccount | None:
        self._ensure_open()
        return self._bank_accounts.get(artifact_id)

    def get_bank_item(self, artifact_id: str) -> BankItem | None:
        self._ensure_open()
        return self._bank_items.get(artifact_id)

    def get_book_item(self, artifact_id: str) -> BookItem | None:
        self._ensure_open()
        return self._book_items.get(artifact_id)

    def get_document(self, artifact_id: str) -> Document | None:
        self._ensure_open()
        return self._documents.get(artifact_id)

    def get_counterparty(self, artifact_id: str) -> Counterparty | None:
        self._ensure_open()
        return self._counterparties.get(artifact_id)

    def get_routing_decision(
        self,
        artifact_id: str,
    ) -> RoutingDecision | None:
        self._ensure_open()
        return self._routing_decisions.get(artifact_id)

    def get_classification(
        self,
        artifact_id: str,
    ) -> ClassificationDecision | None:
        self._ensure_open()
        return self._classifications.get(artifact_id)

    def get_reconciliation(
        self,
        artifact_id: str,
    ) -> Reconciliation | None:
        self._ensure_open()
        return self._reconciliations.get(artifact_id)

    def get_book_item_evidence_assertion(
        self,
        artifact_id: str,
    ) -> BookItemEvidenceAssertion | None:
        self._ensure_open()
        return self._book_item_evidence_assertions.get(artifact_id)

    def get_book_item_evidence_invalidation(
        self,
        artifact_id: str,
    ) -> BookItemEvidenceInvalidation | None:
        self._ensure_open()
        return self._book_item_evidence_invalidations.get(artifact_id)

    def get_reconciliation_hypothesis(
        self,
        hypothesis_id: str,
    ) -> ReconciliationHypothesis | None:
        self._ensure_open()
        return self._reconciliation_hypotheses.get(hypothesis_id)

    # ------------------------------------------------------------------
    # Internal authoritative mutation surface
    #
    # These methods are intentionally prefixed with "_".
    # Application code must eventually mutate state through TransitionEngine.
    # ------------------------------------------------------------------

    def _insert_bank_account(self, artifact: BankAccount) -> None:
        self._ensure_open()
        self._insert_unique(
            self._bank_accounts,
            artifact,
            artifact_name="BankAccount",
        )

    def _insert_bank_item(self, artifact: BankItem) -> None:
        self._ensure_open()
        self._insert_unique(
            self._bank_items,
            artifact,
            artifact_name="BankItem",
        )

    def _insert_book_item(self, artifact: BookItem) -> None:
        self._ensure_open()
        self._insert_unique(
            self._book_items,
            artifact,
            artifact_name="BookItem",
        )

    def _insert_document(self, artifact: Document) -> None:
        self._ensure_open()
        self._insert_unique(
            self._documents,
            artifact,
            artifact_name="Document",
        )

    def _insert_counterparty(self, artifact: Counterparty) -> None:
        self._ensure_open()
        self._insert_unique(
            self._counterparties,
            artifact,
            artifact_name="Counterparty",
        )

    def _insert_routing_decision(
        self,
        artifact: RoutingDecision,
    ) -> None:
        self._ensure_open()
        self._insert_unique(
            self._routing_decisions,
            artifact,
            artifact_name="RoutingDecision",
        )

    def _insert_routing_invalidation(
        self,
        artifact: RoutingDecisionInvalidation,
    ) -> None:
        self._ensure_open()
        self._insert_unique(
            self._routing_invalidations,
            artifact,
            artifact_name="RoutingDecisionInvalidation",
        )

    def _insert_classification(
        self,
        artifact: ClassificationDecision,
    ) -> None:
        self._ensure_open()
        self._insert_unique(
            self._classifications,
            artifact,
            artifact_name="ClassificationDecision",
        )

    def _insert_classification_invalidation(
        self,
        artifact: ClassificationInvalidation,
    ) -> None:
        self._ensure_open()
        self._insert_unique(
            self._classification_invalidations,
            artifact,
            artifact_name="ClassificationInvalidation",
        )

    def _insert_reconciliation(
        self,
        artifact: Reconciliation,
    ) -> None:
        self._ensure_open()
        self._insert_unique(
            self._reconciliations,
            artifact,
            artifact_name="Reconciliation",
        )

    def _insert_reconciliation_invalidation(
        self,
        artifact: ReconciliationInvalidation,
    ) -> None:
        self._ensure_open()
        self._insert_unique(
            self._reconciliation_invalidations,
            artifact,
            artifact_name="ReconciliationInvalidation",
        )

    def _insert_book_item_evidence_assertion(
        self,
        artifact: BookItemEvidenceAssertion,
    ) -> None:
        self._ensure_open()
        self._insert_unique(
            self._book_item_evidence_assertions,
            artifact,
            artifact_name="BookItemEvidenceAssertion",
        )

    def _insert_book_item_evidence_invalidation(
        self,
        artifact: BookItemEvidenceInvalidation,
    ) -> None:
        self._ensure_open()
        self._insert_unique(
            self._book_item_evidence_invalidations,
            artifact,
            artifact_name="BookItemEvidenceInvalidation",
        )

    # ------------------------------------------------------------------
    # Runtime hypothesis mutation
    # ------------------------------------------------------------------

    def _put_reconciliation_hypothesis(
        self,
        hypothesis: ReconciliationHypothesis,
    ) -> None:
        self._ensure_open()

        existing = self._reconciliation_hypotheses.get(hypothesis.id)

        if existing is not None and existing != hypothesis:
            raise DuplicateArtifactError(
                "ReconciliationHypothesis "
                f"{hypothesis.id!r} already exists with different content"
            )

        self._reconciliation_hypotheses[hypothesis.id] = hypothesis

    def _drop_reconciliation_hypothesis(
        self,
        hypothesis_id: str,
    ) -> ReconciliationHypothesis | None:
        self._ensure_open()
        return self._reconciliation_hypotheses.pop(
            hypothesis_id,
            None,
        )

    def _clear_reconciliation_hypotheses(self) -> None:
        self._ensure_open()
        self._reconciliation_hypotheses.clear()

    # ------------------------------------------------------------------
    # Runtime event mutation
    # ------------------------------------------------------------------

    def _append_event(self, event: StateEvent) -> None:
        self._ensure_open()

        if event.state_revision != self.revision:
            raise StateRevisionConflictError(
                "StateEvent revision does not match current "
                f"BookkeepingState revision: "
                f"event={event.state_revision}, state={self.revision}"
            )

        self._events.append(event)

    # ------------------------------------------------------------------
    # Revision management
    # ------------------------------------------------------------------

    def _assert_revision(self, expected_revision: int) -> None:
        self._ensure_open()

        if self.revision != expected_revision:
            raise StateRevisionConflictError(
                "BookkeepingState revision conflict: "
                f"expected={expected_revision}, actual={self.revision}"
            )

            
    def _advance_revision(
        self,
        *,
        expected_previous_revision: int,
        new_persistence_revision: int,
    ) -> int:
        """
        Atomically advance the runtime interpretation of this state after a
        successful durable commit.

        This is an internal transition-engine operation.
        """

        self._ensure_open()
        self._assert_revision(
            expected_previous_revision
        )

        if new_persistence_revision < self.persistence_revision:
            raise ValueError(
                "Persistence revision cannot move backwards"
            )

        new_state_revision = (
            expected_previous_revision + 1
        )

        self._runtime = self._runtime.model_copy(
            update={
                "state_revision": new_state_revision,
                "persistence_revision": (
                    new_persistence_revision
                ),
            }
        )

        return new_state_revision
    # ------------------------------------------------------------------
    # Helpers
    # ------------------------------------------------------------------

    @staticmethod
    def _index_unique(
        artifacts: Iterable[ArtifactT],
        *,
        artifact_name: str,
    ) -> dict[str, ArtifactT]:
        indexed: dict[str, ArtifactT] = {}

        for artifact in artifacts:
            artifact_id = getattr(artifact, "id", None)

            if not isinstance(artifact_id, str) or not artifact_id:
                raise BookkeepingStateError(
                    f"{artifact_name} must expose a non-empty string 'id'"
                )

            if artifact_id in indexed:
                raise DuplicateArtifactError(
                    f"Duplicate {artifact_name} ID: {artifact_id!r}"
                )

            indexed[artifact_id] = artifact

        return indexed

    @staticmethod
    def _insert_unique(
        collection: dict[str, ArtifactT],
        artifact: ArtifactT,
        *,
        artifact_name: str,
    ) -> None:
        artifact_id = getattr(artifact, "id", None)

        if not isinstance(artifact_id, str) or not artifact_id:
            raise BookkeepingStateError(
                f"{artifact_name} must expose a non-empty string 'id'"
            )

        existing = collection.get(artifact_id)

        if existing is not None:
            if existing == artifact:
                # Idempotent reinsertion of exactly the same durable artifact.
                return

            raise DuplicateArtifactError(
                f"{artifact_name} {artifact_id!r} already exists "
                "with different content"
            )

        collection[artifact_id] = artifact

    def _ensure_open(self) -> None:
        if self._closed:
            raise BookkeepingStateClosedError(
                "BookkeepingState has been destroyed. "
                "Hydrate a new state before continuing."
            )