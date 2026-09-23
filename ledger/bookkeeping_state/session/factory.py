from __future__ import annotations

from bookkeeping_state.bank_categorization.nats_bank_categorizer import (
    NatsAseBankCategorizer,
)
from bookkeeping_state.bank_categorization.protocol import (
    ResidualBankCategorizer,
)
from bookkeeping_state.bank_categorization.transport_models import (
    DEFAULT_BANK_CATEGORIZER_NATS_SUBJECT,
)
from bookkeeping_state.dag.nats_book_categorizer import (
    DEFAULT_BOOK_CATEGORIZER_NATS_SUBJECT,
    NatsAseBookCategorizer,
)
from bookkeeping_state.dag.protocol import AseClassifier
from bookkeeping_state.hydration.hydrator import BookkeepingHydrator
from bookkeeping_state.llm.client import get_semantic_provider_mode
from bookkeeping_state.llm.factory import (
    create_reconciliation_semantic_provider,
    create_routing_semantic_provider,
)
from bookkeeping_state.payment_application.service import PaymentApplicationService
from bookkeeping_state.persistence.repository import BookkeepingRepository

from bookkeeping_state.reconciliation.service import ReconciliationService
from bookkeeping_state.routing.service import RoutingService
from bookkeeping_state.session.bookkeeping_session import BookkeepingSession
from bookkeeping_state.transitions.engine import TransitionEngine


def create_production_dag_classifier(
    *,
    nats_url: str | None = None,
    subject: str = DEFAULT_BOOK_CATEGORIZER_NATS_SUBJECT,
    timeout_seconds: float = 10.0,
) -> AseClassifier:
    """
    Construct the production NatsAseBookCategorizer.
    """
    return NatsAseBookCategorizer(
        nats_url=nats_url,
        subject=subject,
        timeout_seconds=timeout_seconds,
    )


def create_production_residual_bank_categorizer(
    *,
    nats_url: str | None = None,
    subject: str = DEFAULT_BANK_CATEGORIZER_NATS_SUBJECT,
    timeout_seconds: float = 10.0,
) -> ResidualBankCategorizer:
    """
    Construct the production NatsAseBankCategorizer.
    """
    return NatsAseBankCategorizer(
        nats_url=nats_url,
        subject=subject,
        timeout_seconds=timeout_seconds,
    )


def create_production_session(
    company_id: str,
    session_id: str,
    *,
    run_id: str | None = None,
    nats_url: str | None = None,
    nats_subject: str = DEFAULT_BOOK_CATEGORIZER_NATS_SUBJECT,
    nats_bank_subject: str = DEFAULT_BANK_CATEGORIZER_NATS_SUBJECT,
    timeout_seconds: float = 10.0,
    capability_secret: bytes | None = None,
    semantic_provider_mode: str | None = None,
    dag_classifier: AseClassifier | None = None,
    residual_bank_categorizer: ResidualBankCategorizer | None = None,
) -> BookkeepingSession:
    """
    Construct a BookkeepingSession fully wired with production dependencies.
    STRICTLY ZERO imports from bookkeeping_state_eval.
    """
    repository = BookkeepingRepository()
    hydrator = BookkeepingHydrator(repository=repository)
    transition_engine = TransitionEngine(repository=repository)

    payment_app_service = PaymentApplicationService()

    resolved_mode = semantic_provider_mode or get_semantic_provider_mode()
    routing_provider = create_routing_semantic_provider(mode=resolved_mode)
    routing_service = RoutingService(semantic_provider=routing_provider)
    recon_scorer = create_reconciliation_semantic_provider(mode=resolved_mode)
    reconciliation_service = ReconciliationService(scorer=recon_scorer)

    if dag_classifier is None:
        dag_classifier = create_production_dag_classifier(
            nats_url=nats_url,
            subject=nats_subject,
            timeout_seconds=timeout_seconds,
        )

    if residual_bank_categorizer is None:
        residual_bank_categorizer = create_production_residual_bank_categorizer(
            nats_url=nats_url,
            subject=nats_bank_subject,
            timeout_seconds=timeout_seconds,
        )

    state = hydrator.hydrate(company_id=company_id, session_id=session_id)

    return BookkeepingSession(
        state=state,
        engine=transition_engine,
        routing_service=routing_service,
        dag_classifier=dag_classifier,
        residual_bank_categorizer=residual_bank_categorizer,
        reconciliation_service=reconciliation_service,
        payment_application_service=payment_app_service,
        hydrator=hydrator,
    )
