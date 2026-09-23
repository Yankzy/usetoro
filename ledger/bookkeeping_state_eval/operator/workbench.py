from __future__ import annotations

from datetime import date as dt_date, datetime, timezone
import logging
from typing import Any, Callable, Sequence

from bookkeeping_state.dag.protocol import AseClassifier
from bookkeeping_state.domain.bank import BankAccount, BankItem
from bookkeeping_state.domain.books import BookItem
from bookkeeping_state.domain.context import AccountingPolicy, BookkeepingContext
from bookkeeping_state.domain.enums import Direction, SourceType
from bookkeeping_state.hydration.hydrator import BookkeepingHydrator
from bookkeeping_state.llm.factory import (
    create_reconciliation_semantic_provider,
    create_routing_semantic_provider,
)
from bookkeeping_state.operator.workbench import (
    BookkeepingWorkbench as ProductionBookkeepingWorkbench,
    WorkbenchSessionResult,
    _extract_delta_ids,
    _parse_amount,
)
from bookkeeping_state.persistence.repository import (
    BookkeepingRepository,
    BookkeepingSnapshot,
)
from bookkeeping_state.reconciliation.models import (
    ReconciliationPlan,
    ReconciliationResult,
)
from bookkeeping_state.reconciliation.service import ReconciliationService
from bookkeeping_state.state.bookkeeping_state import BookkeepingState
from bookkeeping_state_eval.dag.simulated_ase import SimulatedAseClassifier
from bookkeeping_state_eval.persistence.in_memory import InMemoryBookkeepingRepository
from bookkeeping_state_eval.scenarios.catalog import SCENARIOS_MAP
from bookkeeping_state_eval.scenarios.challenges import CHALLENGES_MAP, ChallengeDefinition
from bookkeeping_state_eval.scenarios.models import ScenarioDefinition
from bookkeeping_state_eval.scenarios.temporal import (
    TEMPORAL_CHALLENGES_MAP,
    TemporalChallengeDefinition,
)

logger = logging.getLogger(__name__)


def create_demo_repository() -> tuple[BookkeepingRepository, str]:
    """Create demo repository with 5 bank items and 5 book items."""
    company_id = "demo-company"
    bank_acc = BankAccount(id="acc-main", name="BMCE Operating MAD", currency="MAD")
    book_items = (
        BookItem(
            id="book-salary",
            source_type=SourceType.POSTED_BOOK_ITEM,
            origin_period="2026-09",
            date=dt_date(2026, 9, 1),
            amount_units="40000",
            direction=Direction.BOOK_BANK_CREDIT,
            currency="MAD",
            description="Salary September payroll",
            reference="SAL-SEP",
        ),
        BookItem(
            id="book-aws",
            source_type=SourceType.POSTED_BOOK_ITEM,
            origin_period="2026-09",
            date=dt_date(2026, 9, 2),
            amount_units="1200",
            direction=Direction.BOOK_BANK_CREDIT,
            currency="MAD",
            description="AWS Cloud hosting services",
            reference="AWS-INV-1",
        ),
        BookItem(
            id="book-rent",
            source_type=SourceType.POSTED_BOOK_ITEM,
            origin_period="2026-09",
            date=dt_date(2026, 9, 3),
            amount_units="12000",
            direction=Direction.BOOK_BANK_CREDIT,
            currency="MAD",
            description="Office rent Casablanca",
            reference="RENT-SEP",
        ),
        BookItem(
            id="book-supplier",
            source_type=SourceType.POSTED_BOOK_ITEM,
            origin_period="2026-09",
            date=dt_date(2026, 9, 4),
            amount_units="18000",
            direction=Direction.BOOK_BANK_CREDIT,
            currency="MAD",
            description="Supplier Alpha invoice payment",
            reference="SUP-ALPHA",
        ),
        BookItem(
            id="book-mystery",
            source_type=SourceType.POSTED_BOOK_ITEM,
            origin_period="2026-09",
            date=dt_date(2026, 9, 5),
            amount_units="7500",
            direction=Direction.BOOK_BANK_CREDIT,
            currency="MAD",
            description="VIREMENT 828192",
            reference="VIR-828192",
        ),
    )

    bank_items = (
        BankItem(
            id="bank-salary",
            bank_account_id="acc-main",
            date=dt_date(2026, 9, 1),
            amount_units="40000",
            direction=Direction.BANK_OUTFLOW,
            currency="MAD",
            description="VRT SALAIRES SEPTEMBRE",
            reference="SAL-SEP",
        ),
        BankItem(
            id="bank-aws",
            bank_account_id="acc-main",
            date=dt_date(2026, 9, 2),
            amount_units="1200",
            direction=Direction.BANK_OUTFLOW,
            currency="MAD",
            description="PRLV AMAZON WEB SERVICES",
            reference="AWS-INV-1",
        ),
        BankItem(
            id="bank-rent",
            bank_account_id="acc-main",
            date=dt_date(2026, 9, 3),
            amount_units="12000",
            direction=Direction.BANK_OUTFLOW,
            currency="MAD",
            description="LOYER BUREAU SEPTEMBRE",
            reference="RENT-SEP",
        ),
        BankItem(
            id="bank-supplier",
            bank_account_id="acc-main",
            date=dt_date(2026, 9, 4),
            amount_units="18000",
            direction=Direction.BANK_OUTFLOW,
            currency="MAD",
            description="FACTURE ALPHA SARL",
            reference="SUP-ALPHA",
        ),
        BankItem(
            id="bank-mystery",
            bank_account_id="acc-main",
            date=dt_date(2026, 9, 5),
            amount_units="8200",
            direction=Direction.BANK_OUTFLOW,
            currency="MAD",
            description="VIREMENT EMIS 828192",
            reference="VIR-DIFF",
        ),
    )

    snapshot = BookkeepingSnapshot(
        persistence_revision=1,
        context=BookkeepingContext(
            company_id=company_id,
            period_start=dt_date(2026, 9, 1),
            period_end=dt_date(2026, 9, 30),
            base_currency="MAD",
            policy=AccountingPolicy(chart_of_accounts_id="pcge"),
        ),
        bank_accounts=(bank_acc,),
        bank_items=bank_items,
        book_items=book_items,
    )
    repo = InMemoryBookkeepingRepository(initial_snapshots=[snapshot])
    return repo, company_id


def seed_scenario_repository(scenario: ScenarioDefinition) -> BookkeepingRepository:
    """Seed a fresh InMemoryBookkeepingRepository with scenario's initial snapshot."""
    snapshot = scenario.to_initial_snapshot()
    return InMemoryBookkeepingRepository(initial_snapshots=[snapshot])


# Register default factory with production workbench
ProductionBookkeepingWorkbench._default_repository_factory = staticmethod(create_demo_repository)
ProductionBookkeepingWorkbench._default_dag_classifier_factory = staticmethod(SimulatedAseClassifier)


class BookkeepingWorkbench(ProductionBookkeepingWorkbench):
    """
    Evaluation subclass of BookkeepingWorkbench.
    Adds scenario catalogs, adversarial challenge seeding, temporal stepping,
    and automatic in-memory demo repository provisioning.
    """

    def __init__(
        self,
        *,
        scenario_name: str | None = None,
        semantic_provider: str = "deterministic",
        repository: BookkeepingRepository | None = None,
        company_id: str | None = None,
        company_name: str | None = None,
        debug: bool = False,
    ) -> None:
        self.debug = debug
        self.semantic_provider = semantic_provider
        self.company_name = company_name
        self.session_counter = 0
        self.last_result: WorkbenchSessionResult | None = None
        self.last_reconciliation_result: ReconciliationResult | None = None
        self.last_recon_plan: ReconciliationPlan | None = None
        self.active_scenario: ScenarioDefinition | None = None
        self.active_challenge: ChallengeDefinition | None = None
        self.active_temporal_challenge: TemporalChallengeDefinition | None = None
        self.active_scenario_name: str | None = scenario_name
        self.clock_time: datetime | None = None
        self.temporal_step_index: int = 0
        self.temporal_step_history: list[Any] = []

        if repository is not None and company_id is not None:
            self.repository = repository
            self.company_id = company_id
            self.routing_semantic_provider = None
            self.dag_classifier = None
            self.reconciliation_service = None
            self.hydrator = BookkeepingHydrator(repository=self.repository)
            self.session_counter += 1
            self.state: BookkeepingState | None = self.hydrator.hydrate(
                company_id=self.company_id,
                session_id=f"workbench-inspection-{self.session_counter}",
            )
        elif scenario_name and scenario_name in SCENARIOS_MAP:
            self.load_scenario(scenario_name)
        else:
            self.repository, self.company_id = create_demo_repository()
            self._configure_services()
            self.hydrator = BookkeepingHydrator(repository=self.repository)
            self.session_counter += 1
            self.state = self.hydrator.hydrate(
                company_id=self.company_id,
                session_id=f"workbench-inspection-{self.session_counter}",
            )

    def _update_repository_snapshot(self, new_snap: BookkeepingSnapshot) -> None:
        """Update snapshot in in-memory evaluation repository."""
        self.repository = InMemoryBookkeepingRepository(initial_snapshots=[new_snap])

    def _get_dag_classifier(self) -> AseClassifier:
        """Hook to obtain an ASE classifier when running session."""
        if self.dag_classifier is not None:
            return self.dag_classifier
        return SimulatedAseClassifier()

    # ------------------------------------------------------------------
    # Scenario & Challenge Loading
    # ------------------------------------------------------------------

    def load_demo(self) -> None:
        """Reset workbench to default demo repository."""
        self.close_state()
        self.repository, self.company_id = create_demo_repository()
        self._configure_services()
        self.hydrator = BookkeepingHydrator(repository=self.repository)
        self.session_counter += 1
        self.state = self.hydrator.hydrate(
            company_id=self.company_id,
            session_id=f"workbench-inspection-{self.session_counter}",
        )
        self.last_result = None
        self.last_reconciliation_result = None
        self.last_recon_plan = None
        self.active_scenario = None
        self.active_scenario_name = None
        self.active_challenge = None
        self.active_temporal_challenge = None
        self.temporal_step_index = 0
        self.temporal_step_history = []

    def load_scenario(self, scenario_or_name: str | ScenarioDefinition) -> None:
        """Seed and load a scenario from catalog."""
        if isinstance(scenario_or_name, str):
            if scenario_or_name in SCENARIOS_MAP:
                scenario = SCENARIOS_MAP[scenario_or_name]
            elif scenario_or_name in CHALLENGES_MAP:
                scenario = CHALLENGES_MAP[scenario_or_name].scenario
            elif scenario_or_name in TEMPORAL_CHALLENGES_MAP:
                scenario = TEMPORAL_CHALLENGES_MAP[scenario_or_name].initial_scenario
            else:
                raise KeyError(f"Unknown scenario or challenge: {scenario_or_name}")
            self.active_scenario_name = scenario_or_name
        else:
            scenario = scenario_or_name
            self.active_scenario_name = getattr(scenario, "scenario_id", None)

        self.repository = seed_scenario_repository(scenario)
        self.company_id = scenario.context.company_id
        if self.semantic_provider == "llm":
            self.routing_semantic_provider = create_routing_semantic_provider("llm")
            self.reconciliation_service = ReconciliationService(
                scorer=create_reconciliation_semantic_provider("llm")
            )
        else:
            self.routing_semantic_provider = scenario.routing_semantic_provider
            self.reconciliation_service = scenario.reconciliation_service
        self.dag_classifier = scenario.dag_classifier
        self.clock_time = scenario.clock_time

        scenario_clock = self.clock_time
        self.hydrator = BookkeepingHydrator(
            repository=self.repository,
            clock=(lambda: scenario_clock) if scenario_clock is not None else None,
        )
        self.close_state()
        self.session_counter += 1
        self.state = self.hydrator.hydrate(
            company_id=self.company_id,
            session_id=f"workbench-inspection-{self.session_counter}",
        )
        self.last_result = None
        self.last_reconciliation_result = None
        self.last_recon_plan = None
        self.active_scenario = scenario
        self.active_challenge = None
        self.active_temporal_challenge = None

    def load_challenge(self, challenge_id: str) -> None:
        """Seed and load an adversarial challenge."""
        if challenge_id in CHALLENGES_MAP:
            challenge = CHALLENGES_MAP[challenge_id]
            scenario = challenge.scenario
            self.load_scenario(scenario)
            self.active_challenge = challenge
        elif challenge_id in TEMPORAL_CHALLENGES_MAP:
            self.load_temporal_challenge(challenge_id)
        else:
            raise KeyError(f"Challenge '{challenge_id}' not found in challenges or temporal challenges.")

    def load_temporal_challenge(self, challenge_id: str) -> None:
        """Seed and load a temporal challenge."""
        challenge = TEMPORAL_CHALLENGES_MAP[challenge_id]
        scenario = challenge.initial_scenario
        self.load_scenario(scenario)
        self.active_temporal_challenge = challenge
        self.temporal_step_index = 0
