from __future__ import annotations

import logging
from collections.abc import Callable
from datetime import datetime
from typing import Any

from bookkeeping_state.bank_categorization.nats_bank_categorizer import (
    NatsAseBankCategorizer,
)
from bookkeeping_state.bank_categorization.protocol import (
    MissingResidualBankCategorizerError,
    ResidualBankCategorizer,
)
from bookkeeping_state.bank_categorization.transport_models import (
    DEFAULT_BANK_CATEGORIZER_NATS_SUBJECT,
)
from bookkeeping_state.dag.errors import MissingDagProviderError
from bookkeeping_state.dag.nats_book_categorizer import (
    DEFAULT_BOOK_CATEGORIZER_NATS_SUBJECT,
    NatsAseBookCategorizer,
)
from bookkeeping_state.dag.protocol import AseClassifier
from bookkeeping_state.domain.commands import get_stage1_capability_secret
from bookkeeping_state.hydration.hydrator import BookkeepingHydrator
from bookkeeping_state.llm.client import get_semantic_provider_mode
from bookkeeping_state.llm.factory import (
    create_reconciliation_semantic_provider,
    create_routing_semantic_provider,
)
from bookkeeping_state.payment_application.service import PaymentApplicationService
from bookkeeping_state.persistence.repository import BookkeepingRepository

from bookkeeping_state.reconciliation.service import ReconciliationService
from bookkeeping_state.routing.scorer import (
    RoutingSemanticScoreProvider,
    ZeroRoutingSemanticScoreProvider,
)
from bookkeeping_state.routing.service import RoutingService
from bookkeeping_state.session.bookkeeping_session import BookkeepingSession
from bookkeeping_state.session.result import SessionResult
from bookkeeping_state.transitions.engine import TransitionEngine

logger = logging.getLogger(__name__)


class BookkeepingApplicationService:
    """
    Internal application service orchestrating BookkeepingSession runs.
    Accepts explicit dependency injection for testing and local inspection.
    """

    def __init__(
        self,
        *,
        repository: BookkeepingRepository,
        hydrator: BookkeepingHydrator,
        transition_engine: TransitionEngine,
        dag_classifier: AseClassifier,
        residual_bank_categorizer: ResidualBankCategorizer | None = None,
        routing_semantic_provider: RoutingSemanticScoreProvider | None = None,
        reconciliation_service: ReconciliationService | None = None,
        payment_application_service: PaymentApplicationService | None = None,
    ) -> None:
        self.repository = repository
        self.hydrator = hydrator
        self.transition_engine = transition_engine
        self.dag_classifier = dag_classifier
        self.residual_bank_categorizer = residual_bank_categorizer
        self.routing_semantic_provider = routing_semantic_provider
        self.reconciliation_service = reconciliation_service
        self.payment_application_service = payment_application_service

    def run_session(
        self,
        company_id: str,
        session_id: str,
        run_id: str | None = None,
        *,
        clock: Callable[[], datetime] | None = None,
        seed: int | None = None,
    ) -> SessionResult:
        if not company_id or not company_id.strip():
            raise ValueError("company_id is required")
        if not session_id or not session_id.strip():
            raise ValueError("session_id is required")

        # DAG Provider Preflight: Ensure classifier is present and ready before hydration
        if self.dag_classifier is None:
            raise MissingDagProviderError("No DAG classifier configured")
        if hasattr(self.dag_classifier, "check_readiness") and callable(
            self.dag_classifier.check_readiness
        ):
            self.dag_classifier.check_readiness()

        # Residual Bank Categorizer Preflight: If configured, verify readiness
        if hasattr(self.residual_bank_categorizer, "check_readiness") and callable(
            self.residual_bank_categorizer.check_readiness
        ):
            self.residual_bank_categorizer.check_readiness()

        session_state = self.hydrator.hydrate(
            company_id=company_id,
            session_id=session_id,
        )

        routing_service = RoutingService(
            semantic_provider=self.routing_semantic_provider
            or ZeroRoutingSemanticScoreProvider()
        )

        session = BookkeepingSession(
            state=session_state,
            engine=self.transition_engine,
            routing_service=routing_service,
            dag_classifier=self.dag_classifier,
            residual_bank_categorizer=self.residual_bank_categorizer,
            reconciliation_service=self.reconciliation_service or ReconciliationService(),
            payment_application_service=self.payment_application_service,
            hydrator=self.hydrator,
        )

        return session.run()


def create_production_bookkeeping_application_service(
    *,
    semantic_provider_mode: str | None = None,
    nats_url: str | None = None,
    nats_subject: str | None = None,
    nats_bank_subject: str | None = None,
    timeout_seconds: float = 10.0,
    capability_secret: bytes | None = None,
    dag_classifier: AseClassifier | None = None,
    residual_bank_categorizer: ResidualBankCategorizer | None = None,
) -> BookkeepingApplicationService:
    """
    Production-only factory creating BookkeepingApplicationService with authoritative
    Django persistence, real Stage-1 payment application, and real NATS book categorizer.
    STRICTLY ZERO imports from bookkeeping_state_eval.
    """
    repository = BookkeepingRepository()
    hydrator = BookkeepingHydrator(repository=repository)
    transition_engine = TransitionEngine(repository=repository)

    payment_app_service = PaymentApplicationService()

    resolved_mode = semantic_provider_mode or get_semantic_provider_mode()
    routing_provider = create_routing_semantic_provider(mode=resolved_mode)
    recon_scorer = create_reconciliation_semantic_provider(mode=resolved_mode)
    reconciliation_service = ReconciliationService(scorer=recon_scorer)

    if dag_classifier is None:
        dag_classifier = NatsAseBookCategorizer(
            nats_url=nats_url,
            subject=nats_subject or DEFAULT_BOOK_CATEGORIZER_NATS_SUBJECT,
            timeout_seconds=timeout_seconds,
        )

    if residual_bank_categorizer is None:
        residual_bank_categorizer = NatsAseBankCategorizer(
            nats_url=nats_url,
            subject=nats_bank_subject or DEFAULT_BANK_CATEGORIZER_NATS_SUBJECT,
            timeout_seconds=timeout_seconds,
        )

    return BookkeepingApplicationService(
        repository=repository,
        hydrator=hydrator,
        transition_engine=transition_engine,
        dag_classifier=dag_classifier,
        residual_bank_categorizer=residual_bank_categorizer,
        routing_semantic_provider=routing_provider,
        reconciliation_service=reconciliation_service,
        payment_application_service=payment_app_service,
    )


def run_bookkeeping_session(
    company_id: str,
    session_id: str,
    run_id: str | None = None,
) -> SessionResult:
    """
    Canonical production entrypoint for executing a BookkeepingSession.
    Does NOT accept test overrides, clocks, seeds, capability secrets, or provider injection.
    Production configuration is derived from environment and secret store.

    Performs preflight checks on dependencies before any durable mutation begins.
    """
    if not company_id or not company_id.strip():
        raise ValueError("company_id is required")
    if not session_id or not session_id.strip():
        raise ValueError("session_id is required")

    # Dependency Preflight: Fail before routing or any durable mutation if misconfigured
    # 1. Capability secret preflight
    get_stage1_capability_secret()

    # 2. Semantic provider preflight
    mode = get_semantic_provider_mode()
    if mode == "llm":
        from bookkeeping_state.llm.client import _read_openai_api_key

        _read_openai_api_key()

    app_service = create_production_bookkeeping_application_service()

    # 3. DAG Classifier Preflight: Verify readiness before any session hydration/mutation
    if app_service.dag_classifier is None:
        raise MissingDagProviderError("No DAG classifier configured")
    if hasattr(app_service.dag_classifier, "check_readiness") and callable(
        app_service.dag_classifier.check_readiness
    ):
        app_service.dag_classifier.check_readiness()

    # 4. Residual Bank Categorizer Preflight: Verify readiness before any session hydration/mutation
    if app_service.residual_bank_categorizer is None:
        raise MissingResidualBankCategorizerError("No residual bank categorizer configured")
    if hasattr(app_service.residual_bank_categorizer, "check_readiness") and callable(
        app_service.residual_bank_categorizer.check_readiness
    ):
        app_service.residual_bank_categorizer.check_readiness()

    return app_service.run_session(
        company_id=company_id,
        session_id=session_id,
        run_id=run_id,
    )
