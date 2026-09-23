"""
BookkeepingState Developer Evaluation Lab.

A thin interactive CLI playground for inspecting, executing, closing,
and rehydrating BookkeepingState through its validated runtime architecture.

Usage:
    ./.venv/bin/python ledger/bookkeeping_state_eval/lab.py
    ./.venv/bin/python ledger/bookkeeping_state_eval/lab.py --scenario scenario_c_many_to_one
    ./.venv/bin/python ledger/bookkeeping_state_eval/lab.py --production-state
"""

from __future__ import annotations

import argparse
import cmd
from collections.abc import Callable
import dataclasses
from datetime import date, datetime, timezone
from pathlib import Path
import re
import sys
from typing import Any
import uuid

# Ensure repository packages are importable regardless of launch directory
_current_dir = Path(__file__).resolve().parent
_app_dir = _current_dir.parent
_repo_root = _app_dir.parent
for p in (_repo_root, _app_dir, _current_dir):
    if str(p) not in sys.path:
        sys.path.insert(0, str(p))

from bookkeeping_state.reconciliation.models import (
    ReconciliationPlan,
    ReconciliationResult,
    UnresolvedReason,
)
from bookkeeping_state_eval.scenarios.expected_truth import ExpectedTruth


DEFAULT_CLOCK = datetime(2026, 1, 31, 12, 0, 0, tzinfo=timezone.utc)

from bookkeeping_state.dag.protocol import AseClassifier
from bookkeeping_state_eval.dag.simulated_ase import SimulatedAseClassifier
from bookkeeping_state.domain.bank import BankAccount, BankItem
from bookkeeping_state.domain.books import BookItem
from bookkeeping_state.domain.commands import (
    AssertBookItemEvidenceCommand,
    BookkeepingCommand,
    CommandSource,
    InvalidateBookItemEvidenceCommand,
    InvalidateReconciliationCommand,
)
from bookkeeping_state.domain.context import AccountingPolicy, BookkeepingContext
from bookkeeping_state.domain.counterparties import Counterparty, CounterpartyType
from bookkeeping_state.domain.enums import (
    AllocationSupport,
    Direction,
    Eligibility,
    SemanticAdmissibility,
    SourceType,
)
from bookkeeping_state.domain.hypotheses import ReconciliationHypothesis
from bookkeeping_state.domain.evidence import (
    BookItemEvidenceAssertion,
    BookItemEvidenceInvalidation,
    BookItemEvidenceType,
    EvidenceSource,
)
from bookkeeping_state.domain.money import (
    format_money,
    major_units_to_solver_units,
    solver_units_to_decimal,
)
from bookkeeping_state.domain.residual_bank_classifications import (
    ResidualBankClassificationStatus,
)
from bookkeeping_state.hydration.hydrator import BookkeepingHydrator
from bookkeeping_state_eval.persistence.in_memory import InMemoryBookkeepingRepository
from bookkeeping_state.persistence.repository import (
    BookkeepingRepository,
    BookkeepingSnapshot,
    PersistenceWriteSet,
)
from bookkeeping_state.reconciliation.service import ReconciliationService
from bookkeeping_state.routing.scorer import (
    RoutingSemanticScoreProvider,
    ZeroRoutingSemanticScoreProvider,
)
from bookkeeping_state.llm import (
    create_reconciliation_semantic_provider,
    create_routing_semantic_provider,
)
from bookkeeping_state.routing.service import RoutingService
from bookkeeping_state_eval.scenarios.catalog import (
    SCENARIOS_MAP,
    get_scenario,
)
from bookkeeping_state_eval.scenarios.challenges import (
    CHALLENGES_MAP,
    ChallengeDefinition,
    get_challenge,
    list_challenges,
)
from bookkeeping_state_eval.scenarios.temporal import (
    TEMPORAL_CHALLENGES_MAP,
    TemporalChallengeDefinition,
    TemporalOpType,
    TemporalStep,
    get_temporal_challenge,
    list_temporal_challenges,
    AddBankItemsOp,
    AddBookItemsOp,
    AddCounterpartiesOp,
    AddDocumentsOp,
    AddBookItemEvidenceOp,
    InvalidateBookItemEvidenceOp,
    InvalidateReconciliationOp,
    RunSessionOp,
    VerifyOp,
    StepSummary,
)
from bookkeeping_state.operator.agent import BookkeepingOperator
from bookkeeping_state_eval.operator.workbench import BookkeepingWorkbench
from bookkeeping_state.operator.workbench import WorkbenchSessionResult
from bookkeeping_state.domain.evidence import (
    BookItemEvidenceAssertion,
    BookItemEvidenceInvalidation,
)
from bookkeeping_state_eval.scenarios.expected_truth import validate_expected_truth
from bookkeeping_state_eval.scenarios.models import ScenarioDefinition
from bookkeeping_state_eval.scenarios.runner import seed_scenario_repository
from bookkeeping_state.session.bookkeeping_session import BookkeepingSession
from bookkeeping_state.session.result import SessionResult
from bookkeeping_state.state.bookkeeping_state import BookkeepingState
from bookkeeping_state.state.fingerprint import (
    artifact_fingerprint,
    state_fingerprint,
)
from bookkeeping_state.state.queries import BookkeepingQueries
from bookkeeping_state.transitions.engine import TransitionEngine


def _ensure_django_ready() -> None:
    import os
    import django
    os.environ.setdefault("DJANGO_SETTINGS_MODULE", "config.settings")
    django.setup()


def _resolve_production_company_info(company_id_or_slug: str) -> tuple[str, str, str | None]:
    _ensure_django_ready()
    from ledger.models.entity import EntityModel
    entity = None
    try:
        entity = EntityModel.objects.filter(slug=company_id_or_slug).first()
        if not entity:
            import uuid
            entity = EntityModel.objects.filter(uuid=uuid.UUID(company_id_or_slug)).first()
    except Exception:
        entity = None
    if entity:
        return str(entity.uuid), entity.name, entity.slug
    return company_id_or_slug, company_id_or_slug, None


def _resolve_production_company_uuid(company_id_or_slug: str) -> str:
    uuid_str, _, _ = _resolve_production_company_info(company_id_or_slug)
    return uuid_str


def create_demo_repository() -> tuple[BookkeepingRepository, str]:
    """
    Seed a tiny in-memory demo company with explicit semantic evidence
    and one ambiguous item intentionally placed on HOLD.
    """
    company_id = "demo-company"
    bank_acc = BankAccount(id="acc-main", name="BMCE Operating MAD", currency="MAD")

    book_items = (
        BookItem(
            id="book-salary",
            origin_period="2026-09",
            date=date(2026, 9, 1),
            amount_units="40000",
            direction=Direction.BOOK_BANK_CREDIT,
            currency="MAD",
            description="Salary September payroll",
            reference="SAL-SEP",
            source_type=SourceType.POSTED_BOOK_ITEM,
        ),
        BookItem(
            id="book-aws",
            origin_period="2026-09",
            date=date(2026, 9, 2),
            amount_units="1200",
            direction=Direction.BOOK_BANK_CREDIT,
            currency="MAD",
            description="AWS Cloud hosting services",
            reference="AWS-INV-1",
            source_type=SourceType.POSTED_BOOK_ITEM,
        ),
        BookItem(
            id="book-rent",
            origin_period="2026-09",
            date=date(2026, 9, 3),
            amount_units="12000",
            direction=Direction.BOOK_BANK_CREDIT,
            currency="MAD",
            description="Office rent Casablanca",
            reference="RENT-SEP",
            source_type=SourceType.POSTED_BOOK_ITEM,
        ),
        BookItem(
            id="book-supplier",
            origin_period="2026-09",
            date=date(2026, 9, 4),
            amount_units="18000",
            direction=Direction.BOOK_BANK_CREDIT,
            currency="MAD",
            description="Supplier Alpha invoice payment",
            reference="SUP-ALPHA",
            source_type=SourceType.POSTED_BOOK_ITEM,
        ),
        BookItem(
            id="book-mystery",
            origin_period="2026-09",
            date=date(2026, 9, 5),
            amount_units="7500",
            direction=Direction.BOOK_BANK_CREDIT,
            currency="MAD",
            description="VIREMENT 828192",
            reference="VIR-828192",
            source_type=SourceType.POSTED_BOOK_ITEM,
        ),
    )

    bank_items = (
        BankItem(
            id="bank-salary",
            bank_account_id="acc-main",
            date=date(2026, 9, 1),
            amount_units="40000",
            direction=Direction.BANK_OUTFLOW,
            currency="MAD",
            description="VRT SALAIRES SEPTEMBRE",
            reference="SAL-SEP",
        ),
        BankItem(
            id="bank-aws",
            bank_account_id="acc-main",
            date=date(2026, 9, 2),
            amount_units="1200",
            direction=Direction.BANK_OUTFLOW,
            currency="MAD",
            description="PRLV AMAZON WEB SERVICES",
            reference="AWS-INV-1",
        ),
        BankItem(
            id="bank-rent",
            bank_account_id="acc-main",
            date=date(2026, 9, 3),
            amount_units="12000",
            direction=Direction.BANK_OUTFLOW,
            currency="MAD",
            description="LOYER BUREAU SEPTEMBRE",
            reference="RENT-SEP",
        ),
        BankItem(
            id="bank-supplier",
            bank_account_id="acc-main",
            date=date(2026, 9, 4),
            amount_units="18000",
            direction=Direction.BANK_OUTFLOW,
            currency="MAD",
            description="FACTURE ALPHA SARL",
            reference="SUP-ALPHA",
        ),
        BankItem(
            id="bank-mystery",
            bank_account_id="acc-main",
            date=date(2026, 9, 5),
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
            period_start=date(2026, 9, 1),
            period_end=date(2026, 9, 30),
            base_currency="MAD",
            policy=AccountingPolicy(chart_of_accounts_id="pcge"),
        ),
        bank_accounts=(bank_acc,),
        bank_items=bank_items,
        book_items=book_items,
    )
    repo = InMemoryBookkeepingRepository(initial_snapshots=[snapshot])
    return repo, company_id


def _parse_amount(raw_val: str) -> str:
    raw = raw_val.strip().replace(",", "").replace("_", "")
    if re.match(r"^[1-9][0-9]*$", raw):
        return raw
    # Try converting as major unit decimal
    try:
        val_int = major_units_to_solver_units(raw)
        if val_int > 0:
            return str(val_int)
    except Exception:
        pass
    raise ValueError(
        f"Invalid monetary amount: {raw_val!r}. "
        "Enter positive solver units (e.g. 50000) or decimal (e.g. 5.0)."
    )


@dataclasses.dataclass(frozen=True)
class LabSessionResult(WorkbenchSessionResult):
    """
    Detached SessionResult enriched with reconciliation plan telemetry for lab debugging.
    """

    reconciliation_result: ReconciliationResult | None = None
    reconciliation_plan: ReconciliationPlan | None = None

    @classmethod
    def from_session_result(
        cls,
        res: SessionResult,
        reconciliation_result: ReconciliationResult | None = None,
        reconciliation_plan: ReconciliationPlan | None = None,
    ) -> LabSessionResult:
        kwargs = {
            f.name: getattr(res, f.name) for f in dataclasses.fields(SessionResult)
        }
        return cls(
            **kwargs,
            reconciliation_result=reconciliation_result,
            reconciliation_plan=reconciliation_plan,
        )

    @property
    def review_hypotheses(self) -> tuple[ReconciliationHypothesis, ...]:
        if self.reconciliation_result is not None:
            return self.reconciliation_result.review_hypotheses
        return ()


class BookkeepingLab(cmd.Cmd):
    """
    Developer interactive command loop for BookkeepingState.
    """

    prompt = "(lab) > "

    def __init__(
        self,
        *,
        scenario_name: str | None = None,
        debug: bool = False,
        semantic_provider: str = "deterministic",
        operator: bool = True,
        operator_model: str = "gpt-5.6-luna",
        production_state: bool = False,
        company_id: str | None = None,
    ) -> None:
        super().__init__()
        self.debug = debug
        self.semantic_provider = semantic_provider
        self.operator_enabled = operator
        self.operator_model = operator_model or "gpt-5.6-luna"
        self.production_state = production_state
        self.company_name: str | None = None
        self.company_slug: str | None = None
        if production_state:
            _ensure_django_ready()
            raw_target = company_id or "toro-synthetic-bookkeeping"
            uuid_str, name, slug = _resolve_production_company_info(raw_target)
            self.target_company_id = uuid_str
            self.company_name = name
            self.company_slug = slug
            self.workbench = BookkeepingWorkbench(
                repository=BookkeepingRepository(),
                company_id=self.target_company_id,
                company_name=name,
                semantic_provider=semantic_provider,
                debug=debug,
            )
        else:
            self.target_company_id = company_id or "demo-company"
            self.company_name = self.target_company_id
            self.workbench = BookkeepingWorkbench(
                scenario_name=scenario_name,
                semantic_provider=semantic_provider,
                debug=debug,
            )
        self.operator: BookkeepingOperator | None = None
        if self.operator_enabled:
            self.operator = BookkeepingOperator(
                workbench=self.workbench,
                model_name=self.operator_model,
            )
        self.session_counter = 0
        self.last_result: WorkbenchSessionResult | None = None
        self.last_reconciliation_result: ReconciliationResult | None = None
        self.last_recon_plan: ReconciliationPlan | None = None
        self.repository: BookkeepingRepository
        self.company_id: str
        self.routing_semantic_provider: RoutingSemanticScoreProvider | None = None
        self.dag_classifier: AseClassifier | None = None
        self.reconciliation_service: ReconciliationService | None = None
        self.clock_time: datetime | None = None
        self.hydrator: BookkeepingHydrator
        self.state: BookkeepingState | None = None
        self.active_scenario: ScenarioDefinition | None = None
        self.active_scenario_name: str | None = scenario_name
        self.active_challenge: ChallengeDefinition | None = None
        self.active_temporal_challenge: TemporalChallengeDefinition | None = None
        self.temporal_step_index: int = 0
        self.temporal_step_history: list[StepSummary] = []

        if production_state:
            self._load_production_state_internal(self.target_company_id)
        elif scenario_name:
            self._load_scenario_internal(scenario_name)
        else:
            self._load_demo_internal()

    @property
    def company_display_name(self) -> str:
        name = getattr(self, "company_name", None)
        slug = getattr(self, "company_slug", None)
        if name:
            if slug and slug.lower() != name.lower():
                return f"{name} ({slug})"
            return name
        cid = getattr(self, "company_id", None) or getattr(self, "target_company_id", None)
        if cid:
            try:
                _ensure_django_ready()
                from ledger.models.entity import EntityModel
                import uuid
                e = None
                try:
                    e = EntityModel.objects.filter(uuid=uuid.UUID(cid)).first()
                except Exception:
                    pass
                if not e:
                    e = EntityModel.objects.filter(slug=cid).first()
                if e:
                    if e.name and e.slug and e.name.lower() != e.slug.lower():
                        return f"{e.name} ({e.slug})"
                    return e.name or e.slug or cid
            except Exception:
                pass
            return cid
        return "Unknown Company"

    def _sync_to_workbench(self) -> None:
        if not hasattr(self, "workbench"):
            return
        self.workbench.state = self.state
        self.workbench.repository = self.repository
        self.workbench.company_id = self.company_id
        self.workbench.company_name = getattr(self, "company_name", None)
        self.workbench.session_counter = self.session_counter
        self.workbench.last_result = self.last_result
        self.workbench.last_reconciliation_result = self.last_reconciliation_result
        self.workbench.last_recon_plan = self.last_recon_plan
        self.workbench.active_scenario = self.active_scenario
        self.workbench.active_scenario_name = self.active_scenario_name
        self.workbench.active_challenge = self.active_challenge
        self.workbench.active_temporal_challenge = self.active_temporal_challenge
        self.workbench.temporal_step_index = self.temporal_step_index
        self.workbench.temporal_step_history = self.temporal_step_history

    def _sync_from_workbench(self) -> None:
        if not hasattr(self, "workbench"):
            return
        self.state = self.workbench.state
        self.repository = self.workbench.repository
        self.company_id = self.workbench.company_id
        if getattr(self.workbench, "company_name", None):
            self.company_name = self.workbench.company_name
        self.session_counter = self.workbench.session_counter
        self.last_result = self.workbench.last_result
        self.last_reconciliation_result = self.workbench.last_reconciliation_result
        self.last_recon_plan = self.workbench.last_recon_plan
        self.active_scenario = self.workbench.active_scenario
        self.active_scenario_name = self.workbench.active_scenario_name
        self.active_challenge = self.workbench.active_challenge
        self.active_temporal_challenge = self.workbench.active_temporal_challenge
        self.temporal_step_index = self.workbench.temporal_step_index
        self.temporal_step_history = self.workbench.temporal_step_history

    def clock(self) -> datetime:
        return self.clock_time or DEFAULT_CLOCK

    # ------------------------------------------------------------------
    # Internal Loading Helpers
    # ------------------------------------------------------------------

    def _load_production_state_internal(self, company_id: str) -> None:
        if self.state is not None and not self.state.is_closed:
            self.state.close()
            self.state = None

        resolved_company_id, name, slug = _resolve_production_company_info(company_id)
        self.repository = BookkeepingRepository()
        self.company_id = resolved_company_id
        self.company_name = name
        self.company_slug = slug
        if self.semantic_provider == "llm":
            self.routing_semantic_provider = create_routing_semantic_provider("llm")
            self.reconciliation_service = ReconciliationService(
                scorer=create_reconciliation_semantic_provider("llm")
            )
        else:
            self.routing_semantic_provider = None
            self.reconciliation_service = None
        self.dag_classifier = None
        self.clock_time = None

        self.hydrator = BookkeepingHydrator(repository=self.repository)
        self.session_counter += 1
        self.state = self.hydrator.hydrate(
            company_id=self.company_id,
            session_id=f"lab-production-inspection-{self.session_counter}",
        )
        self.last_result = None
        self.last_reconciliation_result = None
        self.last_recon_plan = None
        self.active_scenario = None
        self.active_scenario_name = f"production:{company_id}"
        self.active_challenge = None
        self.active_temporal_challenge = None
        self._sync_to_workbench()

    def _load_demo_internal(self) -> None:
        if self.state is not None and not self.state.is_closed:
            self.state.close()
            self.state = None

        self.repository, self.company_id = create_demo_repository()
        if self.semantic_provider == "llm":
            self.routing_semantic_provider = create_routing_semantic_provider("llm")
            self.reconciliation_service = ReconciliationService(
                scorer=create_reconciliation_semantic_provider("llm")
            )
        else:
            self.routing_semantic_provider = None
            self.reconciliation_service = None
        self.dag_classifier = None
        self.clock_time = None

        self.hydrator = BookkeepingHydrator(repository=self.repository)
        self.session_counter += 1
        self.state = self.hydrator.hydrate(
            company_id=self.company_id,
            session_id=f"lab-inspection-{self.session_counter}",
        )
        self.last_result = None
        self.last_reconciliation_result = None
        self.last_recon_plan = None
        self.active_scenario = None
        self.active_scenario_name = None
        self.active_challenge = None
        self.active_temporal_challenge = None
        self._sync_to_workbench()

    def _load_scenario_internal(
        self,
        scenario_or_name: str | ScenarioDefinition,
    ) -> None:
        if self.state is not None and not self.state.is_closed:
            self.state.close()
            self.state = None

        if isinstance(scenario_or_name, str):
            scenario = get_scenario(scenario_or_name)
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
        self.session_counter += 1
        self.state = self.hydrator.hydrate(
            company_id=self.company_id,
            session_id=f"lab-inspection-{self.session_counter}",
        )
        self.last_result = None
        self.last_reconciliation_result = None
        self.last_recon_plan = None
        self.active_scenario = scenario
        self.active_challenge = None
        self.active_temporal_challenge = None
        self._sync_to_workbench()

    # ------------------------------------------------------------------
    # Mutation & State Lifecycle Boundaries
    # ------------------------------------------------------------------

    def _prompt(self, prompt_text: str, default: str | None = None) -> str:
        default_display = f" [{default}]" if default is not None else ""
        msg = f"{prompt_text}{default_display}: "
        if hasattr(self, "stdin") and self.stdin is not None and self.stdin is not sys.stdin:
            line = self.stdin.readline()
            if not line:
                return default if default is not None else ""
            val = line.strip()
            return val if val else (default if default is not None else "")
        try:
            val = input(msg).strip()
        except EOFError:
            return default if default is not None else ""
        return val if val else (default if default is not None else "")

    def _get_or_hydrate_state(self) -> BookkeepingState:
        if self.state is None or self.state.is_closed:
            self.session_counter += 1
            self.state = self.hydrator.hydrate(
                company_id=self.company_id,
                session_id=f"lab-inspection-{self.session_counter}",
            )
        return self.state

    def _commit_source_writeset(self, write_set: PersistenceWriteSet, desc: str) -> None:
        """
        Commit exogenous source artifacts directly through the authoritative
        repository boundary, closing the old inspection state and hydrating fresh S0.
        """
        if self.state is not None and not self.state.is_closed:
            self.state.close()
            self.state = None

        snap = self.repository.load_snapshot(company_id=self.company_id)
        prev_p = snap.persistence_revision
        self.repository.commit(
            company_id=self.company_id,
            expected_revision=prev_p,
            write_set=write_set,
        )

        self.session_counter += 1
        self.state = self.hydrator.hydrate(
            company_id=self.company_id,
            session_id=f"lab-inspection-{self.session_counter}",
        )
        new_p = self.state.persistence_revision

        print(f"Added {desc}.")
        print(f"Persistence: P{prev_p} -> P{new_p}")
        print(f"Fresh inspection state: S{self.state.revision}")
        self._sync_to_workbench()

    def _apply_transition_command(
        self,
        command_factory: Callable[[BookkeepingState], BookkeepingCommand],
        desc: str,
    ) -> bool:
        """
        Apply an accounting assertion or invalidation command through the
        authoritative TransitionEngine write boundary.
        """
        if self.state is not None and not self.state.is_closed:
            self.state.close()
            self.state = None

        snap = self.repository.load_snapshot(company_id=self.company_id)
        prev_p = snap.persistence_revision

        self.session_counter += 1
        mutation_state = self.hydrator.hydrate(
            company_id=self.company_id,
            session_id=f"lab-mutation-{self.session_counter}",
        )

        cmd = command_factory(mutation_state)
        engine = TransitionEngine(repository=self.repository)
        t_res = engine.apply(state=mutation_state, command=cmd)

        if not mutation_state.is_closed:
            mutation_state.close()

        if not t_res.applied:
            rej_code = t_res.rejection.code if t_res.rejection else "UNKNOWN"
            rej_msg = t_res.rejection.message if t_res.rejection else "Unknown rejection"
            print(f"Transition rejected ({rej_code}): {rej_msg}")
            self.session_counter += 1
            self.state = self.hydrator.hydrate(
                company_id=self.company_id,
                session_id=f"lab-inspection-{self.session_counter}",
            )
            return False

        self.session_counter += 1
        self.state = self.hydrator.hydrate(
            company_id=self.company_id,
            session_id=f"lab-inspection-{self.session_counter}",
        )
        new_p = self.state.persistence_revision

        print(f"Applied {desc}.")
        print(f"Persistence: P{prev_p} -> P{new_p}")
        print(f"Fresh inspection state: S{self.state.revision}")
        self._sync_to_workbench()
        return True

    # ------------------------------------------------------------------
    # Command Dispatch & Aliasing
    # ------------------------------------------------------------------

    def onecmd(self, line: str) -> bool:
        cmd_name, sep, arg = line.strip().partition(" ")
        # Map hyphenated commands to Python-compliant method identifiers
        if cmd_name == "provider-issues":
            line = "provider_issues" + (sep + arg if sep else "")
        elif cmd_name == "run-routing":
            line = "run_routing" + (sep + arg if sep else "")
        elif cmd_name == "run-dag":
            line = "run_dag" + (sep + arg if sep else "")
        elif cmd_name == "run-reconciliation":
            line = "run_reconciliation" + (sep + arg if sep else "")
        elif cmd_name == "add-evidence":
            line = "add_evidence" + (sep + arg if sep else "")
        elif cmd_name == "temporal-challenges":
            line = "temporal_challenges" + (sep + arg if sep else "")
        elif cmd_name == "add-bank":
            line = "add_bank" + (sep + arg if sep else "")
        elif cmd_name == "add-book":
            line = "add_book" + (sep + arg if sep else "")
        elif cmd_name == "add-counterparty":
            line = "add_counterparty" + (sep + arg if sep else "")
        elif cmd_name == "assert-evidence":
            line = "assert_evidence" + (sep + arg if sep else "")
        elif cmd_name == "invalidate-evidence":
            line = "invalidate_evidence" + (sep + arg if sep else "")
        elif cmd_name == "invalidate-reconciliation":
            line = "invalidate_reconciliation" + (sep + arg if sep else "")
        elif cmd_name == "set-policy":
            line = "set_policy" + (sep + arg if sep else "")
        elif cmd_name == "clear-chat":
            line = "clear_chat" + (sep + arg if sep else "")
        elif cmd_name == "operator-trace":
            line = "operator_trace" + (sep + arg if sep else "")

        try:
            return super().onecmd(line)
        except Exception as exc:
            if self.debug:
                import traceback
                traceback.print_exc()
            else:
                print(f"Error ({type(exc).__name__}): {exc}")
            return False

    def emptyline(self) -> bool:
        return False

    # ------------------------------------------------------------------
    # Inspection Commands
    # ------------------------------------------------------------------

    def do_state(self, arg: str) -> None:
        """Show compact state summary."""
        if self.state is None or self.state.is_closed:
            pers_rev = self.repository.load_snapshot(
                company_id=self.company_id
            ).persistence_revision
            print(f"Company: {self.company_display_name}")
            print("Session: none (state is closed)")
            print("Local revision: closed")
            print(f"Persistence revision: P{pers_rev}")
            print()
            last_holds = self.last_result.hold_count if self.last_result else 0
            last_issues = (
                self.last_result.provider_issue_count if self.last_result else 0
            )
            print(f"Last DAG holds: {last_holds}")
            print(f"Last provider issues: {last_issues}")
            print("\nState is closed. Use `rehydrate`.")
            return

        queries = BookkeepingQueries(self.state)
        active_routes = len(queries.derived.active_routing_by_book_item)
        active_classifications = len(
            queries.derived.active_classification_by_book_item
        )
        active_reconciliations = len(queries.derived.active_reconciliations)

        unresolved_bank = sum(
            1
            for item in self.state.bank_items.values()
            if queries.bank_remaining_units(item.id) > 0
        )
        unresolved_book = sum(
            1
            for item in self.state.book_items.values()
            if queries.book_remaining_units(item.id) > 0
        )

        last_holds = self.last_result.hold_count if self.last_result else 0
        last_issues = (
            self.last_result.provider_issue_count if self.last_result else 0
        )

        print(f"Company: {self.company_display_name}")
        print(f"Session: {self.state.session_id}")
        print(f"Local revision: S{self.state.revision}")
        print(f"Persistence revision: P{self.state.persistence_revision}")
        print()
        print(f"Bank items: {len(self.state.bank_items)}")
        print(f"Book items: {len(self.state.book_items)}")
        print()
        active_residual_holds = sum(
            1
            for dec in self.state.residual_bank_classifications.values()
            if dec.status == ResidualBankClassificationStatus.HOLD
        )
        print(f"Active routes: {active_routes}")
        print(f"Legacy book classifications: {active_classifications}")
        print(f"Residual bank classifications: {len(self.state.residual_bank_classifications)}")
        print(f"Residual bank postings: {len(self.state.residual_bank_postings)}")
        print(f"Active residual HOLDs: {active_residual_holds}")
        print(f"Active reconciliations: {active_reconciliations}")
        print(f"Executed payment applications: {len(self.state.executed_payment_applications)}")
        print()
        print(f"Unresolved bank items: {unresolved_bank}")
        print(f"Unresolved book items: {unresolved_book}")
        print()
        print(f"Last DAG holds: {last_holds}")
        print(f"Last provider issues: {last_issues}")

    def do_bank(self, arg: str) -> None:
        """List BankItems."""
        if self.state is None or self.state.is_closed:
            print("State is closed. Use `rehydrate`.")
            return

        queries = BookkeepingQueries(self.state)
        print("BANK ITEMS\n")
        for item in sorted(self.state.bank_items.values(), key=lambda x: x.id):
            rem = queries.bank_remaining_units(item.id)
            desc = f'"{item.description}"' if item.description else '""'
            print(f"{item.id}")
            print(f"  {format_money(item.amount_units, item.currency)}")
            print(f"  {desc}")
            print(f"  remaining: {format_money(rem, item.currency)}")
            print()

    def do_book(self, arg: str) -> None:
        """List BookItems."""
        if self.state is None or self.state.is_closed:
            print("State is closed. Use `rehydrate`.")
            return

        queries = BookkeepingQueries(self.state)
        print("BOOK ITEMS\n")
        for item in sorted(self.state.book_items.values(), key=lambda x: x.id):
            classification = queries.active_classification(item.id)
            class_str = (
                classification.account_code if classification else "none"
            )
            rem = queries.book_remaining_units(item.id)
            desc = f'"{item.description}"' if item.description else '""'
            print(f"{item.id}")
            print(f"  {format_money(item.amount_units, item.currency)}")
            print(f"  {desc}")
            print(f"  classification: {class_str}")
            print(f"  remaining: {format_money(rem, item.currency)}")
            print()

    def do_classifications(self, arg: str) -> None:
        """List active classifications."""
        if self.state is None or self.state.is_closed:
            print("State is closed. Use `rehydrate`.")
            return

        queries = BookkeepingQueries(self.state)
        active = queries.derived.active_classification_by_book_item
        if not active:
            print("No active legacy book classifications.")
            if self.state.residual_bank_classifications:
                print(
                    f"Found {len(self.state.residual_bank_classifications)} residual bank classifications "
                    "(use `residuals` to inspect)."
                )
            return

        print("ACTIVE LEGACY BOOK CLASSIFICATIONS\n")
        for book_item_id, decision in sorted(active.items()):
            source_str = (
                decision.source.value
                if hasattr(decision.source, "value")
                else str(decision.source)
            )
            conf_str = (
                f", confidence: {decision.confidence:.2f}"
                if decision.confidence is not None
                else ""
            )
            print(
                f"{book_item_id} -> {decision.account_code} "
                f"(source: {source_str}{conf_str})"
            )
        if self.state.residual_bank_classifications:
            print(
                f"\n(Found {len(self.state.residual_bank_classifications)} residual bank classifications; "
                "use `residuals` to inspect)"
            )

    def do_routes(self, arg: str) -> None:
        """List active routes."""
        if self.state is None or self.state.is_closed:
            print("State is closed. Use `rehydrate`.")
            return

        queries = BookkeepingQueries(self.state)
        active = queries.derived.active_routing_by_book_item
        if not active:
            print("No active routes.")
            return

        print("ACTIVE ROUTES\n")
        for book_item_id, route in sorted(active.items()):
            print(f"{book_item_id} -> {route.bank_account_id}")

    def do_reconciliations(self, arg: str) -> None:
        """List active reconciliations."""
        if self.state is None or self.state.is_closed:
            print("State is closed. Use `rehydrate`.")
            return

        queries = BookkeepingQueries(self.state)
        active = queries.derived.active_reconciliations.values()
        if not active:
            print("No active reconciliations.")
            return

        print("ACTIVE RECONCILIATIONS\n")
        for recon in sorted(active, key=lambda r: r.id):
            print(f"{recon.id}")
            bank_parts = [
                f"{a.bank_item_id} ({format_money(a.amount_units)})"
                for a in recon.bank_allocations
            ]
            book_parts = [
                f"{a.book_item_id} ({format_money(a.amount_units)})"
                for a in recon.book_allocations
            ]
            print(f"  bank: {', '.join(bank_parts)}")
            print(f"  book: {', '.join(book_parts)}")
            if recon.source_hypothesis_utility is not None:
                print(f"  score: {recon.source_hypothesis_utility}")
            print()

    def do_residuals(self, arg: str) -> None:
        """List residual bank classifications and postings in current state."""
        state = self._get_or_hydrate_state()
        if not state.residual_bank_classifications:
            print("No residual bank classifications in current state.")
            return

        print(f"Residual Decisions ({len(state.residual_bank_classifications)}):")
        for dec_id, dec in sorted(state.residual_bank_classifications.items(), key=lambda x: x[1].bank_item_id):
            b_item = state.bank_items.get(dec.bank_item_id)
            desc = b_item.description if b_item else "Unknown"
            curr = dec.currency or (b_item.currency if b_item else "MAD")
            amt_formatted = format_money(dec.residual_amount_units, curr)
            posting = state.get_residual_bank_posting_for_decision(dec.id)
            posting_info = f"POSTED ({posting.journal_entry_id})" if posting else "NO POSTING (HOLD)"
            conf_str = f"{dec.confidence:.2f}" if dec.confidence is not None else "N/A"
            print(f"  * [{dec.status.value}] Item {dec.bank_item_id} ({amt_formatted}): {desc[:40]}")
            print(f"    Account: {dec.account_code or 'None'} | Conf: {conf_str} | Status: {posting_info}")
            if dec.hold_reason:
                print(f"    Hold Reason: {dec.hold_reason}")
            if dec.required_evidence:
                print(f"    Required Evidence: {', '.join(dec.required_evidence)}")
            if dec.rationale:
                print(f"    Rationale: {dec.rationale}")

    def do_remaining(self, arg: str) -> None:
        """Show remaining amounts for BankItems and BookItems."""
        if self.state is None or self.state.is_closed:
            print("State is closed. Use `rehydrate`.")
            return

        queries = BookkeepingQueries(self.state)
        print("REMAINING AMOUNTS\n")
        print("Bank items:")
        for item in sorted(self.state.bank_items.values(), key=lambda x: x.id):
            rem = queries.bank_remaining_units(item.id)
            print(
                f"  {item.id}: {format_money(rem, item.currency)} "
                f"(original: {format_money(item.amount_units, item.currency)})"
            )
        print()
        print("Book items:")
        for item in sorted(self.state.book_items.values(), key=lambda x: x.id):
            rem = queries.book_remaining_units(item.id)
            print(
                f"  {item.id}: {format_money(rem, item.currency)} "
                f"(original: {format_money(item.amount_units, item.currency)})"
            )

    def do_holds(self, arg: str) -> None:
        """Show active holds across durable residual bank classifications and latest session run."""
        state = self._get_or_hydrate_state()
        residual_holds = [
            dec for dec in state.residual_bank_classifications.values()
            if dec.status == ResidualBankClassificationStatus.HOLD
        ] if state else []

        dag_holds = getattr(self.last_result, "holds", ()) if self.last_result else ()

        if not residual_holds and not dag_holds:
            print("No active holds in current state or latest session.")
            return

        print("ACTIVE HOLDS\n")
        if residual_holds:
            print(f"Residual Bank Classification Holds ({len(residual_holds)}):")
            for rh in sorted(residual_holds, key=lambda x: x.bank_item_id):
                b_item = state.bank_items.get(rh.bank_item_id) if state else None
                desc = b_item.description if b_item else "Unknown"
                curr = rh.currency or (b_item.currency if b_item else "MAD")
                amt_str = format_money(rh.residual_amount_units, curr)
                print(f"  * Item {rh.bank_item_id} ({amt_str}): {desc}")
                print(f"    Hold Reason: {rh.hold_reason}")
                if rh.required_evidence:
                    print(f"    Required Evidence: {', '.join(rh.required_evidence)}")
                if rh.rationale:
                    print(f"    Rationale: {rh.rationale}")
            print()

        if dag_holds:
            print(f"Legacy DAG Book Item Holds ({len(dag_holds)}):")
            for hold in dag_holds:
                print(f"  * {hold.book_item_id}")
                print(f"    Reason: {hold.reason}")
                if getattr(hold, "rationale", None):
                    print(f"    Rationale: {hold.rationale}")
            print()

    def do_provider_issues(self, arg: str) -> None:
        """Show detached provider issue summaries from the most recent session."""
        if self.last_result is None:
            print("No session has been run yet.")
            return

        if not self.last_result.provider_issues:
            print("No provider issues in last session.")
            return

        print("LAST SESSION PROVIDER ISSUES\n")
        for issue in self.last_result.provider_issues:
            print(f"{issue.book_item_id}")
            print(f"  {issue.code}")
            if issue.message:
                print(f"  {issue.message}")
            print()

    # ------------------------------------------------------------------
    # Lifecycle & Execution Commands
    # ------------------------------------------------------------------

    def do_run(self, arg: str) -> None:
        """Execute one full BookkeepingSession using SimulatedAseClassifier."""
        # 1. Close current inspection state if open
        if self.state is not None and not self.state.is_closed:
            self.state.close()
            self.state = None

        # 2. Execute BookkeepingSession against durable repository
        self.session_counter += 1
        session_id = f"lab-session-{self.session_counter}"

        if getattr(self, "production_state", False):
            from bookkeeping_state.session.service import (
                create_production_bookkeeping_application_service,
            )
            app_service = create_production_bookkeeping_application_service()
            result = app_service.run_session(
                company_id=self.company_id,
                session_id=session_id,
            )
            self.state = self.hydrator.hydrate(
                company_id=self.company_id,
                session_id=f"lab-inspection-{self.session_counter}",
            )
            self._sync_to_workbench()
            print(
                f"Production session finished: success={result.is_success}, "
                f"P{result.starting_persistence_revision} -> P{result.final_persistence_revision}"
            )
            return

        session_state = self.hydrator.hydrate(
            company_id=self.company_id,
            session_id=session_id,
        )

        engine = TransitionEngine(repository=self.repository)
        routing_service = RoutingService(
            semantic_provider=self.routing_semantic_provider
            or ZeroRoutingSemanticScoreProvider()
        )
        dag_classifier = self.dag_classifier or SimulatedAseClassifier()
        base_reconciliation_service = (
            self.reconciliation_service or ReconciliationService()
        )

        captured_plan: ReconciliationPlan | None = None
        orig_plan = base_reconciliation_service.plan

        def intercepted_plan(*args: Any, **kwargs: Any) -> ReconciliationPlan:
            nonlocal captured_plan
            captured_plan = orig_plan(*args, **kwargs)
            return captured_plan

        class InterceptingReconciliationService(ReconciliationService):
            def plan(self, *args: Any, **kwargs: Any) -> ReconciliationPlan:
                return intercepted_plan(*args, **kwargs)

        reconciliation_service = InterceptingReconciliationService(
            candidate_generator=base_reconciliation_service.candidate_generator,
            scorer=base_reconciliation_service.scorer,
        )

        session = BookkeepingSession(
            state=session_state,
            engine=engine,
            routing_service=routing_service,
            dag_classifier=dag_classifier,
            reconciliation_service=reconciliation_service,
        )

        result = session.run()

        # 3. Retain detached SessionResult only
        self.last_recon_plan = captured_plan
        self.last_reconciliation_result = captured_plan.result if captured_plan else None
        self.last_result = LabSessionResult.from_session_result(
            result,
            reconciliation_result=self.last_reconciliation_result,
            reconciliation_plan=self.last_recon_plan,
        )

        # Invariant: session state must be closed
        assert session_state.is_closed, "Session did not close its live state."

        # 4. Hydrate a NEW inspection BookkeepingState from resulting durable artifacts
        self.state = self.hydrator.hydrate(
            company_id=self.company_id,
            session_id=f"lab-inspection-{self.session_counter}",
        )

        # 5. Print stage summaries
        routing_status = (
            result.routing_stage_result.status.value
            if result.routing_stage_result
            else "NOT_RUN"
        )
        dag_status = (
            result.dag_stage_result.status.value
            if result.dag_stage_result
            else "NOT_RUN"
        )
        classified_count = (
            result.dag_stage_result.command_count
            if result.dag_stage_result
            else 0
        )
        recon_status = (
            result.reconciliation_stage_result.status.value
            if result.reconciliation_stage_result
            else "NOT_RUN"
        )

        print(f"routing: {routing_status}")
        print(f"dag: {dag_status}")
        print(f"  classified: {classified_count}")
        print(f"  holds: {result.hold_count}")
        print(f"reconciliation: {recon_status}")
        print()
        print(f"final persistence revision: P{result.final_persistence_revision}")
        self._sync_to_workbench()

    def do_close(self, arg: str) -> None:
        """Close current hydrated BookkeepingState."""
        if self.state is None or self.state.is_closed:
            print("State is already closed.")
        else:
            self.state.close()
            print("Inspection state closed. Use `rehydrate` to reopen.")
        self._sync_to_workbench()

    def do_rehydrate(self, arg: str) -> None:
        """Create a fresh BookkeepingState from durable artifacts."""
        if self.state is not None and not self.state.is_closed:
            self.state.close()

        self.session_counter += 1
        self.state = self.hydrator.hydrate(
            company_id=self.company_id,
            session_id=f"lab-inspection-{self.session_counter}",
        )
        self._sync_to_workbench()
        print(
            f"Hydrated fresh state S{self.state.revision} from "
            f"persistence revision P{self.state.persistence_revision}."
        )

    def do_load_production(self, arg: str) -> None:
        """
        Load live BookkeepingState from production PostgreSQL database.

        Usage:
            load_production [company_id]
        """
        target = arg.strip() or getattr(self, "company_id", "toro-synthetic-bookkeeping")
        print(f"Loading production BookkeepingState for company '{target}' from PostgreSQL...")
        try:
            self.production_state = True
            self._load_production_state_internal(target)
            print(f"Successfully hydrated production state for '{self.company_id}'.")
            if self.state:
                print(f"  Persistence Revision: P{self.state.persistence_revision}")
                print(f"  Bank Items: {len(self.state.bank_items)}")
                print(f"  Book Items: {len(self.state.book_items)}")
                print(f"  Reconciliations: {len(self.state.reconciliations)}")
                print(f"  Residual Decisions: {len(self.state.residual_bank_classifications)}")
                print(f"  Residual Postings: {len(self.state.residual_bank_postings)}")
        except Exception as exc:
            print(f"Failed to load production state: {exc}")
            if self.debug:
                import traceback
                traceback.print_exc()

    def do_scenario(self, arg: str) -> None:
        """Replace lab contents with one existing catalog scenario and hydrate it."""
        name = arg.strip()
        if not name:
            print("Available scenarios:")
            for sc_name in sorted(SCENARIOS_MAP.keys()):
                print(f"  {sc_name}")
            return

        if name not in SCENARIOS_MAP:
            print(f"Unknown scenario: {name}")
            print(
                "Use `scenario` without arguments to list available scenarios."
            )
            return

        self._load_scenario_internal(name)
        self.active_challenge = None
        assert self.state is not None
        print(
            f"Loaded scenario '{name}' for company '{self.company_id}'. "
            f"Initial state S{self.state.revision} hydrated."
        )

    def do_challenges(self, arg: str) -> None:
        """List available challenge worlds with difficulty and short description."""
        print("AVAILABLE CHALLENGE WORLDS\n")
        for c in list_challenges():
            tags_str = f" [{', '.join(c.tags)}]" if c.tags else ""
            print(f"{c.scenario_id} (difficulty: {c.difficulty}){tags_str}")
            print(f"  {c.name}: {c.description}")
            print()

    def do_challenge(self, arg: str) -> None:
        """Seed INITIAL durable world for selected challenge and hydrate fresh state."""
        name = arg.strip()
        if not name:
            self.do_challenges("")
            return

        if name not in CHALLENGES_MAP:
            print(f"Unknown challenge: {name}")
            print("Use `challenges` to list available challenge worlds.")
            return

        challenge = get_challenge(name)
        self._load_scenario_internal(challenge.scenario)
        self.active_challenge = challenge
        assert self.state is not None
        print(
            f"Loaded challenge '{name}' (difficulty: {challenge.difficulty}). "
            f"Initial state S{self.state.revision} hydrated."
        )

    def do_temporal_challenges(self, arg: str) -> None:
        """List available temporal challenge worlds (Challenges 08-14)."""
        print("AVAILABLE TEMPORAL CHALLENGES\n")
        for c in list_temporal_challenges():
            tags_str = f" [{', '.join(c.tags)}]" if c.tags else ""
            print(f"{c.challenge_id} (difficulty: {c.difficulty}){tags_str}")
            print(f"  {c.name}: {c.description}")
            print(f"  steps: {len(c.steps)}")
            print()

    def do_temporal(self, arg: str) -> None:
        """Load initial durable world for a temporal challenge: temporal <name>."""
        name = arg.strip()
        if not name:
            self.do_temporal_challenges("")
            return

        try:
            challenge = get_temporal_challenge(name)
        except KeyError:
            print(f"Unknown temporal challenge: {name}")
            print("Use `temporal-challenges` to list available temporal challenges.")
            return
        if self.state is not None and not self.state.is_closed:
            self.state.close()
            self.state = None

        self.repository = seed_scenario_repository(challenge.initial_scenario)
        self.company_id = challenge.initial_scenario.context.company_id
        self.active_temporal_challenge = challenge
        self.active_challenge = None
        self.active_scenario = None
        self.temporal_step_index = 0
        self.temporal_step_history = []
        self.clock_time = challenge.initial_scenario.clock_time or DEFAULT_CLOCK

        self.hydrator = BookkeepingHydrator(
            repository=self.repository,
            clock=lambda: self.clock_time or DEFAULT_CLOCK,
            session_id_factory=lambda: f"lab-session-{self.session_counter}",
        )
        self.session_counter += 1
        self.state = self.hydrator.hydrate(
            company_id=self.company_id,
            session_id=f"session-{self.session_counter}",
        )

        pers_rev = self.state.persistence_revision
        self.temporal_step_history.append(
            StepSummary(
                step_index=0,
                step_id="step-0-initial",
                op_type="INITIAL_WORLD",
                persistence_revision=pers_rev,
                summary=f"Initial durable world hydrated at P{pers_rev}",
                truth_match=True,
                mismatches=(),
            )
        )
        print(
            f"Loaded temporal challenge '{name}' ({challenge.name}). "
            f"Initial state S{self.state.revision} P{pers_rev} hydrated. "
            f"{len(challenge.steps)} steps remaining."
        )

    def do_step(self, arg: str) -> None:
        """Execute exactly the next step in the active temporal challenge."""
        if self.active_temporal_challenge is None:
            print("No temporal challenge is currently loaded. Use `temporal <name>`.")
            return

        total_steps = len(self.active_temporal_challenge.steps)
        if self.temporal_step_index >= total_steps:
            print(f"All {total_steps} steps of '{self.active_temporal_challenge.challenge_id}' have already been executed.")
            return

        step = self.active_temporal_challenge.steps[self.temporal_step_index]
        op = step.operation

        if self.state is not None and not self.state.is_closed:
            self.state.close()
            self.state = None

        clock_now = self.clock_time or DEFAULT_CLOCK
        step_idx = self.temporal_step_index + 1
        summary_text = ""

        if isinstance(op, AddBankItemsOp):
            snap = self.repository.load_snapshot(company_id=self.company_id)
            ws = PersistenceWriteSet(bank_items=op.bank_items)
            self.repository.commit(
                company_id=self.company_id,
                expected_revision=snap.persistence_revision,
                write_set=ws,
            )
            summary_text = f"Added {len(op.bank_items)} BankItem(s)"

        elif isinstance(op, AddBookItemsOp):
            snap = self.repository.load_snapshot(company_id=self.company_id)
            ws = PersistenceWriteSet(book_items=op.book_items)
            self.repository.commit(
                company_id=self.company_id,
                expected_revision=snap.persistence_revision,
                write_set=ws,
            )
            summary_text = f"Added {len(op.book_items)} BookItem(s)"

        elif isinstance(op, AddCounterpartiesOp):
            snap = self.repository.load_snapshot(company_id=self.company_id)
            ws = PersistenceWriteSet(counterparties=op.counterparties)
            self.repository.commit(
                company_id=self.company_id,
                expected_revision=snap.persistence_revision,
                write_set=ws,
            )
            summary_text = f"Added {len(op.counterparties)} Counterparty(ies)"

        elif isinstance(op, AddDocumentsOp):
            snap = self.repository.load_snapshot(company_id=self.company_id)
            ws = PersistenceWriteSet(documents=op.documents)
            self.repository.commit(
                company_id=self.company_id,
                expected_revision=snap.persistence_revision,
                write_set=ws,
            )
            summary_text = f"Added {len(op.documents)} Document(s)"

        elif isinstance(op, AddBookItemEvidenceOp):
            snap = self.repository.load_snapshot(company_id=self.company_id)
            ws = PersistenceWriteSet(
                book_item_evidence_assertions=op.assertions
            )
            self.repository.commit(
                company_id=self.company_id,
                expected_revision=snap.persistence_revision,
                write_set=ws,
            )
            summary_text = f"Added {len(op.assertions)} BookItemEvidenceAssertion(s)"

        elif isinstance(op, InvalidateBookItemEvidenceOp):
            snap = self.repository.load_snapshot(company_id=self.company_id)
            inv = BookItemEvidenceInvalidation(
                id=op.invalidation_id,
                assertion_id=op.assertion_id,
                reason=op.reason,
                session_id=f"lab-inval-session-{step_idx}",
                state_revision_at_invalidation=0,
                created_at=clock_now,
            )
            ws = PersistenceWriteSet(
                book_item_evidence_invalidations=(inv,)
            )
            self.repository.commit(
                company_id=self.company_id,
                expected_revision=snap.persistence_revision,
                write_set=ws,
            )
            summary_text = f"Invalidated BookItemEvidenceAssertion {op.assertion_id}"

        elif isinstance(op, InvalidateReconciliationOp):
            temp_hydrator = BookkeepingHydrator(
                repository=self.repository,
                clock=lambda: clock_now,
                session_id_factory=lambda: f"lab-inval-session-{step_idx}",
            )
            temp_state = temp_hydrator.hydrate(company_id=self.company_id)
            target_recon_id = op.reconciliation_id

            if target_recon_id is None:
                queries = BookkeepingQueries(temp_state)
                if op.bank_item_id is not None:
                    active_recons = queries.reconciliations_for_bank_item(op.bank_item_id)
                    if active_recons:
                        target_recon_id = active_recons[0].id
                elif op.book_item_id is not None:
                    active_recons = queries.reconciliations_for_book_item(op.book_item_id)
                    if active_recons:
                        target_recon_id = active_recons[0].id

            if target_recon_id is None:
                temp_state.close()
                print(f"Error: Could not find active reconciliation to invalidate in step {step.step_id}")
                return

            cmd = InvalidateReconciliationCommand(
                command_id=f"cmd-lab-inval-{step_idx}",
                expected_state_revision=temp_state.revision,
                invalidation_id=f"inval-lab-{step_idx}-{target_recon_id}",
                reconciliation_id=target_recon_id,
                reason=op.reason,
                session_id=temp_state.session_id,
                source=CommandSource.HUMAN,
                issued_at=self.clock(),
            )
            engine = TransitionEngine(repository=self.repository)
            t_res = engine.apply(command=cmd, state=temp_state)
            temp_state.close()
            if not t_res.applied:
                rej_code = t_res.rejection.code if t_res.rejection else "UNKNOWN"
                rej_msg = t_res.rejection.message if t_res.rejection else "Unknown rejection"
                print(f"Invalidation rejected: {rej_code}: {rej_msg}")
                return
            summary_text = f"Invalidated reconciliation {target_recon_id}"

        elif isinstance(op, RunSessionOp):
            self.session_counter += 1
            sess_id = f"session-temporal-{self.session_counter}"
            live_state = self.hydrator.hydrate(company_id=self.company_id, session_id=sess_id)
            engine = TransitionEngine(repository=self.repository)
            routing_svc = RoutingService(
                semantic_provider=self.active_temporal_challenge.initial_scenario.routing_semantic_provider
                or ZeroRoutingSemanticScoreProvider()
            )
            dag_cls = self.active_temporal_challenge.initial_scenario.dag_classifier or SimulatedAseClassifier()
            recon_svc = self.active_temporal_challenge.initial_scenario.reconciliation_service or ReconciliationService()

            session = BookkeepingSession(
                state=live_state,
                engine=engine,
                routing_service=routing_svc,
                dag_classifier=dag_cls,
                reconciliation_service=recon_svc,
            )
            s_res = session.run()
            self.last_result = LabSessionResult.from_session_result(s_res)
            recons_count = (
                s_res.reconciliation_stage_result.command_count
                if s_res.reconciliation_stage_result
                else 0
            )
            summary_text = f"Session executed -> {recons_count} reconciliation(s) created"

        elif isinstance(op, VerifyOp):
            summary_text = "Checkpoint verified"

        # Rehydrate fresh state
        self.state = self.hydrator.hydrate(
            company_id=self.company_id,
            session_id=f"session-step-{step_idx}",
        )
        pers_rev = self.state.persistence_revision

        # Check step checkpoint if defined
        truth_match: bool | None = None
        mismatches: tuple[str, ...] = ()
        expected_to_check = step.expected_checkpoint or (op.expected_truth if isinstance(op, VerifyOp) else None)
        if expected_to_check is not None:
            queries = BookkeepingQueries(self.state)
            verdict = validate_expected_truth(queries, expected_to_check)
            truth_match = verdict.is_match
            mismatches = verdict.mismatches

        step_summary = StepSummary(
            step_index=step_idx,
            step_id=step.step_id,
            op_type=op.op_type.value,
            persistence_revision=pers_rev,
            summary=summary_text,
            truth_match=truth_match,
            mismatches=mismatches,
        )
        self.temporal_step_history.append(step_summary)
        self.temporal_step_index += 1

        print(f"Executed Step {step_idx}/{total_steps} [{op.op_type.value}]: {step.description or summary_text}")
        print(f"  Result: {summary_text} (P{pers_rev})")
        if truth_match is not None:
            status_str = "PASS" if truth_match else "FAIL"
            print(f"  Checkpoint: {status_str}")

    def do_timeline(self, arg: str) -> None:
        """Display completed steps and persistence revisions for active temporal challenge."""
        if not self.temporal_step_history:
            print("No temporal trajectory in progress.")
            return

        print("TEMPORAL TRAJECTORY TIMELINE")
        print("--------------------------------------------------")
        for s in self.temporal_step_history:
            checkpoint_str = ""
            if s.truth_match is not None:
                checkpoint_str = f" [{'PASS' if s.truth_match else 'FAIL'}]"
            print(f"Step {s.step_index:<2}  P{s.persistence_revision:<2}  {s.op_type:<24} {s.summary}{checkpoint_str}")
        print("--------------------------------------------------")
        if self.active_temporal_challenge:
            rem = len(self.active_temporal_challenge.steps) - self.temporal_step_index
            print(f"Completed {self.temporal_step_index}/{len(self.active_temporal_challenge.steps)} steps ({rem} remaining).")

    def do_verify(self, arg: str) -> None:
        """Run independent ExpectedTruth validator against current state: verify."""
        target_truth: ExpectedTruth | None = None
        if self.active_temporal_challenge is not None:
            curr_idx = self.temporal_step_index
            if curr_idx > 0 and curr_idx <= len(self.active_temporal_challenge.steps):
                step = self.active_temporal_challenge.steps[curr_idx - 1]
                if step.expected_checkpoint is not None:
                    target_truth = step.expected_checkpoint
            if target_truth is None:
                target_truth = self.active_temporal_challenge.final_expected_truth
        elif self.active_challenge is not None:
            target_truth = self.active_challenge.scenario.expected_truth
        elif self.active_scenario is not None:
            target_truth = self.active_scenario.expected_truth

        if target_truth is None:
            print("No challenge or scenario is currently loaded to verify.")
            return

        if self.state is None or self.state.is_closed:
            print("State is closed. Use `rehydrate` before verifying.")
            return

        queries = BookkeepingQueries(self.state)
        verdict = validate_expected_truth(queries, target_truth)
        if verdict.is_match:
            print("PASS")
        else:
            print("FAIL\n")
            for mismatch in verdict.mismatches:
                print(f"  - {mismatch}")

    def do_fingerprint(self, arg: str) -> None:
        """Show state fingerprint if exposed."""
        if self.state is None or self.state.is_closed:
            print("State is closed. Use `rehydrate`.")
            return

        print("Durable artifact fingerprint:")
        print(f"  {artifact_fingerprint(self.state)}")
        print("Canonical semantic state fingerprint:")
        print(f"  {state_fingerprint(self.state)}")

    def do_revisions(self, arg: str) -> None:
        """Show local state_revision and persistence_revision."""
        if self.state is not None and not self.state.is_closed:
            local_rev = f"S{self.state.revision}"
            pers_rev = f"P{self.state.persistence_revision}"
        else:
            local_rev = "closed"
            pers_rev = (
                f"P{self.repository.load_snapshot(company_id=self.company_id).persistence_revision}"
            )

        print(f"local state_revision: {local_rev}")
        print(f"persistence_revision: {pers_rev}")

    # ------------------------------------------------------------------
    # Omitted Stage Orchestration & Mutation Commands
    # ------------------------------------------------------------------

    def do_run_routing(self, arg: str) -> None:
        """Inspect/run routing stage if existing public APIs allow this cleanly."""
        print(
            "Command unavailable because this subsystem has no safe public "
            "stage API outside BookkeepingSession orchestration."
        )

    def do_run_dag(self, arg: str) -> None:
        """Inspect/run DAG classification stage if existing public APIs allow this cleanly."""
        print(
            "Command unavailable because this subsystem has no safe public "
            "stage API outside BookkeepingSession orchestration."
        )

    def do_run_reconciliation(self, arg: str) -> None:
        """Inspect/run reconciliation stage if existing public APIs allow this cleanly."""
        print(
            "Command unavailable because this subsystem has no safe public "
            "stage API outside BookkeepingSession orchestration."
        )

    def do_add_evidence(self, arg: str) -> None:
        """Append/update durable input artifact."""
        print(
            "Command unavailable: durable artifacts are append-only and cannot "
            "be mutated in place via existing repository APIs."
        )

    # ------------------------------------------------------------------
    # Exogenous Source Mutation Commands
    # ------------------------------------------------------------------

    def add_bank_item(self, bank_item: BankItem) -> None:
        """Programmatic helper to insert a BankItem through authoritative repo boundary."""
        self._commit_source_writeset(
            PersistenceWriteSet(bank_items=(bank_item,)),
            f"BankItem {bank_item.id}",
        )

    def do_add_bank(self, arg: str) -> None:
        """Interactive wizard to add a new BankItem: add-bank."""
        state = self._get_or_hydrate_state()
        existing_count = len(state.bank_items)
        default_id = f"bank-{existing_count + 1}"
        default_account = (
            next(iter(state.bank_accounts.keys()))
            if state.bank_accounts
            else "acc-main"
        )
        account_obj = state.bank_accounts.get(default_account)
        default_currency = account_obj.currency if account_obj else state.context.base_currency
        default_date = (self.clock_time.date() if self.clock_time else date(2026, 9, 1)).isoformat()

        item_id = self._prompt("BankItem ID", default=default_id)
        account_id = self._prompt("Bank account ID", default=default_account)
        amount_raw = self._prompt("Amount (solver units or decimal, e.g. 50000 or 5.0)")
        amount_units = _parse_amount(amount_raw)
        currency = self._prompt("Currency", default=default_currency).upper()
        direction_str = self._prompt(
            "Direction (BANK_OUTFLOW / BANK_INFLOW)",
            default=Direction.BANK_OUTFLOW.value,
        ).upper()
        direction = Direction(direction_str)
        date_str = self._prompt("Date (YYYY-MM-DD)", default=default_date)
        item_date = date.fromisoformat(date_str)
        description = self._prompt("Description", default=f"Statement entry {item_id}")
        reference = self._prompt("Reference (optional)", default="") or None

        bank_item = BankItem(
            id=item_id,
            bank_account_id=account_id,
            date=item_date,
            amount_units=amount_units,
            direction=direction,
            currency=currency,
            description=description,
            reference=reference,
        )
        self.add_bank_item(bank_item)

    def add_book_item(self, book_item: BookItem) -> None:
        """Programmatic helper to insert a BookItem through authoritative repo boundary."""
        self._commit_source_writeset(
            PersistenceWriteSet(book_items=(book_item,)),
            f"BookItem {book_item.id}",
        )

    def do_add_book(self, arg: str) -> None:
        """Interactive wizard to add a new BookItem: add-book."""
        state = self._get_or_hydrate_state()
        existing_count = len(state.book_items)
        default_id = f"book-{existing_count + 1}"
        default_currency = state.context.base_currency
        default_date = (self.clock_time.date() if self.clock_time else date(2026, 9, 1)).isoformat()

        item_id = self._prompt("BookItem ID", default=default_id)
        amount_raw = self._prompt("Amount (solver units or decimal, e.g. 50000 or 5.0)")
        amount_units = _parse_amount(amount_raw)
        currency = self._prompt("Currency", default=default_currency).upper()
        direction_str = self._prompt(
            "Direction (BOOK_BANK_CREDIT / BOOK_BANK_DEBIT)",
            default=Direction.BOOK_BANK_CREDIT.value,
        ).upper()
        direction = Direction(direction_str)
        date_str = self._prompt("Date (YYYY-MM-DD)", default=default_date)
        item_date = date.fromisoformat(date_str)
        default_period = item_date.strftime("%Y-%m")
        origin_period = self._prompt("Origin accounting period (YYYY-MM)", default=default_period)
        description = self._prompt("Description (optional)", default=f"Ledger item {item_id}") or None
        reference = self._prompt("Reference (optional)", default="") or None
        counterparty_id = self._prompt("Counterparty ID (optional)", default="") or None

        book_item = BookItem(
            id=item_id,
            origin_period=origin_period,
            date=item_date,
            amount_units=amount_units,
            direction=direction,
            currency=currency,
            description=description,
            reference=reference,
            counterparty_id=counterparty_id,
        )
        self.add_book_item(book_item)

    def add_counterparty_item(self, counterparty: Counterparty) -> None:
        """Programmatic helper to insert a Counterparty through authoritative repo boundary."""
        self._commit_source_writeset(
            PersistenceWriteSet(counterparties=(counterparty,)),
            f"Counterparty {counterparty.id}",
        )

    def do_add_counterparty(self, arg: str) -> None:
        """Interactive wizard to add a new Counterparty: add-counterparty."""
        default_id = f"cp-{self.session_counter + 1}"
        cp_id = self._prompt("Counterparty ID", default=default_id)
        name = self._prompt("Name", default=f"Counterparty {cp_id}")
        type_str = self._prompt(
            "Type (CUSTOMER / SUPPLIER / EMPLOYEE / BANK / TAX_AUTHORITY / OTHER)",
            default=CounterpartyType.OTHER.value,
        ).upper()
        cp_type = CounterpartyType(type_str)
        tax_id = self._prompt("Tax ID (optional)", default="") or None
        external_ref = self._prompt("External reference (optional)", default="") or None

        cp = Counterparty(
            id=cp_id,
            name=name,
            counterparty_type=cp_type,
            tax_id=tax_id,
            external_reference=external_ref,
        )
        self.add_counterparty_item(cp)

    # ------------------------------------------------------------------
    # Accounting Assertions & Corrections Commands
    # ------------------------------------------------------------------

    def assert_book_item_evidence(
        self,
        *,
        book_item_id: str,
        evidence_type: BookItemEvidenceType,
        value: str,
        evidence_source: EvidenceSource = EvidenceSource.HUMAN_ASSERTION,
        source_document_id: str | None = None,
        supersedes_assertion_id: str | None = None,
    ) -> bool:
        """Programmatic helper to assert evidence through TransitionEngine."""
        self.session_counter += 1
        clock_now = self.clock_time or datetime.now(timezone.utc)
        assertion_id = f"ev-{self.session_counter}-{uuid.uuid4().hex[:6]}"

        def make_cmd(mutation_state: BookkeepingState) -> AssertBookItemEvidenceCommand:
            return AssertBookItemEvidenceCommand(
                command_id=f"cmd-assert-ev-{self.session_counter}",
                expected_state_revision=mutation_state.revision,
                assertion_id=assertion_id,
                book_item_id=book_item_id,
                evidence_type=evidence_type,
                value=value,
                document_ids=(source_document_id,) if source_document_id else (),
                evidence_source=evidence_source,
                source_document_id=source_document_id,
                supersedes_assertion_id=supersedes_assertion_id,
                session_id=mutation_state.session_id,
                source=CommandSource.HUMAN,
                issued_at=clock_now,
            )

        return self._apply_transition_command(
            make_cmd,
            f"BookItemEvidenceAssertion {assertion_id} on {book_item_id}",
        )

    def do_assert_evidence(self, arg: str) -> None:
        """Interactive wizard to assert evidence for a BookItem: assert-evidence <book_item_id>."""
        book_item_id = arg.strip()
        if not book_item_id:
            book_item_id = self._prompt("BookItem ID")
        if not book_item_id:
            print("BookItem ID is required.")
            return

        state = self._get_or_hydrate_state()
        if book_item_id not in state.book_items:
            print(f"BookItem {book_item_id!r} not found in state.")
            return

        type_str = self._prompt(
            "Evidence type (COUNTERPARTY / REFERENCE / DESCRIPTION / DOCUMENT_LINK)",
            default=BookItemEvidenceType.COUNTERPARTY.value,
        ).upper()
        evidence_type = BookItemEvidenceType(type_str)
        value = self._prompt("Typed evidentiary value")
        source_str = self._prompt(
            "Evidence source (HUMAN_ASSERTION / DOCUMENT_EXTRACTION / ENTITY_RESOLUTION / MANUAL / SYSTEM)",
            default=EvidenceSource.HUMAN_ASSERTION.value,
        ).upper()
        evidence_source = EvidenceSource(source_str)
        source_doc = self._prompt("Source document ID (optional)", default="") or None
        supersedes_id = self._prompt("Supersedes assertion ID (optional)", default="") or None

        self.assert_book_item_evidence(
            book_item_id=book_item_id,
            evidence_type=evidence_type,
            value=value,
            evidence_source=evidence_source,
            source_document_id=source_doc,
            supersedes_assertion_id=supersedes_id,
        )

    def invalidate_evidence_assertion(
        self,
        assertion_id: str,
        reason: str = "Invalidated via developer workbench",
    ) -> bool:
        """Programmatic helper to invalidate an evidence assertion through TransitionEngine."""
        self.session_counter += 1
        clock_now = self.clock_time or datetime.now(timezone.utc)
        invalidation_id = f"inv-ev-{self.session_counter}-{uuid.uuid4().hex[:6]}"

        def make_cmd(mutation_state: BookkeepingState) -> InvalidateBookItemEvidenceCommand:
            return InvalidateBookItemEvidenceCommand(
                command_id=f"cmd-inval-ev-{self.session_counter}",
                expected_state_revision=mutation_state.revision,
                invalidation_id=invalidation_id,
                assertion_id=assertion_id,
                reason=reason,
                session_id=mutation_state.session_id,
                source=CommandSource.HUMAN,
                issued_at=clock_now,
            )

        return self._apply_transition_command(
            make_cmd,
            f"invalidation of assertion {assertion_id}",
        )

    def do_invalidate_evidence(self, arg: str) -> None:
        """Invalidate a BookItemEvidenceAssertion: invalidate-evidence <assertion_id>."""
        assertion_id = arg.strip()
        if not assertion_id:
            assertion_id = self._prompt("Assertion ID to invalidate")
        if not assertion_id:
            print("Assertion ID is required.")
            return

        reason = self._prompt("Reason for invalidation", default="Invalidated via developer workbench")
        self.invalidate_evidence_assertion(assertion_id, reason=reason)

    def invalidate_reconciliation_item(
        self,
        reconciliation_id: str,
        reason: str = "Invalidated via developer workbench",
    ) -> bool:
        """Programmatic helper to invalidate a reconciliation through TransitionEngine."""
        self.session_counter += 1
        clock_now = self.clock_time or datetime.now(timezone.utc)
        invalidation_id = f"inv-rec-{self.session_counter}-{uuid.uuid4().hex[:6]}"

        def make_cmd(mutation_state: BookkeepingState) -> InvalidateReconciliationCommand:
            return InvalidateReconciliationCommand(
                command_id=f"cmd-inval-rec-{self.session_counter}",
                expected_state_revision=mutation_state.revision,
                invalidation_id=invalidation_id,
                reconciliation_id=reconciliation_id,
                reason=reason,
                session_id=mutation_state.session_id,
                source=CommandSource.HUMAN,
                issued_at=clock_now,
            )

        return self._apply_transition_command(
            make_cmd,
            f"invalidation of reconciliation {reconciliation_id}",
        )

    def do_invalidate_reconciliation(self, arg: str) -> None:
        """Invalidate an active reconciliation: invalidate-reconciliation <reconciliation_id>."""
        rec_id = arg.strip()
        if not rec_id:
            rec_id = self._prompt("Reconciliation ID to invalidate")
        if not rec_id:
            print("Reconciliation ID is required.")
            return

        reason = self._prompt("Reason for invalidation", default="Invalidated via developer workbench")
        self.invalidate_reconciliation_item(rec_id, reason=reason)

    # ------------------------------------------------------------------
    # Policy & Reset Commands
    # ------------------------------------------------------------------

    def do_policy(self, arg: str) -> None:
        """Print current AccountingPolicy."""
        if self.state is None or self.state.is_closed:
            snap = self.repository.load_snapshot(company_id=self.company_id)
            policy = snap.context.policy
        else:
            policy = self.state.context.policy

        print("ACCOUNTING POLICY\n")
        print(f"  chart_of_accounts_id:                       {policy.chart_of_accounts_id}")
        print(f"  allow_partial_bank_reconciliation:          {policy.allow_partial_bank_reconciliation}")
        print(f"  allow_partial_book_reconciliation:          {policy.allow_partial_book_reconciliation}")
        print(f"  auto_reconcile_unique_inferred_allocation:  {policy.auto_reconcile_unique_inferred_allocation}")
        print(f"  reconciliation_date_window_days:            {policy.reconciliation_date_window_days}")
        print(f"  require_exact_currency_match:               {policy.require_exact_currency_match}")

    def do_set_policy(self, arg: str) -> None:
        """Update policy: set-policy auto_reconcile_unique_inferred_allocation true|false."""
        parts = arg.strip().split()
        if len(parts) != 2 or parts[0] != "auto_reconcile_unique_inferred_allocation":
            print("Usage: set-policy auto_reconcile_unique_inferred_allocation true|false")
            return

        val_str = parts[1].lower()
        if val_str in ("true", "1", "yes", "on"):
            target_val = True
        elif val_str in ("false", "0", "no", "off"):
            target_val = False
        else:
            print("Value must be true or false.")
            return

        if self.state is not None and not self.state.is_closed:
            self.state.close()
            self.state = None

        snap = self.repository.load_snapshot(company_id=self.company_id)
        current_policy = snap.context.policy
        if current_policy.auto_reconcile_unique_inferred_allocation == target_val:
            print(f"Policy auto_reconcile_unique_inferred_allocation is already {target_val}.")
            self.state = self.hydrator.hydrate(
                company_id=self.company_id,
                session_id=f"lab-inspection-{self.session_counter}",
            )
            return

        new_policy = current_policy.model_copy(
            update={"auto_reconcile_unique_inferred_allocation": target_val}
        )
        new_context = snap.context.model_copy(update={"policy": new_policy})
        new_snap = BookkeepingSnapshot(
            persistence_revision=snap.persistence_revision,
            context=new_context,
            bank_accounts=snap.bank_accounts,
            bank_items=snap.bank_items,
            book_items=snap.book_items,
            documents=snap.documents,
            counterparties=snap.counterparties,
            routing_decisions=snap.routing_decisions,
            routing_invalidations=snap.routing_invalidations,
            classifications=snap.classifications,
            classification_invalidations=snap.classification_invalidations,
            reconciliations=snap.reconciliations,
            reconciliation_invalidations=snap.reconciliation_invalidations,
            book_item_evidence_assertions=snap.book_item_evidence_assertions,
            book_item_evidence_invalidations=snap.book_item_evidence_invalidations,
        )

        self.repository = InMemoryBookkeepingRepository(initial_snapshots=[new_snap])
        scenario_clock = self.clock_time
        self.hydrator = BookkeepingHydrator(
            repository=self.repository,
            clock=(lambda: scenario_clock) if scenario_clock is not None else None,
        )
        self.session_counter += 1
        self.state = self.hydrator.hydrate(
            company_id=self.company_id,
            session_id=f"lab-inspection-{self.session_counter}",
        )

        print(f"Updated policy auto_reconcile_unique_inferred_allocation = {target_val}.")
        print(f"Persistence revision: P{self.state.persistence_revision}")
        print(f"Fresh inspection state: S{self.state.revision}")

    def do_reset(self, arg: str) -> None:
        """Discard mutated lab repository and restore original scenario/demo definition."""
        if self.state is not None and not self.state.is_closed:
            self.state.close()
            self.state = None

        if self.active_temporal_challenge is not None:
            self.do_temporal(self.active_temporal_challenge.challenge_id)
        elif self.active_challenge is not None:
            self.do_challenge(self.active_challenge.scenario_id)
        elif self.active_scenario_name is not None:
            self._load_scenario_internal(self.active_scenario_name)
        else:
            self._load_demo_internal()

        self.last_result = None
        self.last_reconciliation_result = None
        self.last_recon_plan = None

        print("Reset complete.")
        if self.state is not None:
            print(f"Persistence revision: P{self.state.persistence_revision}")
            print(f"Local revision: S{self.state.revision}")

    # ------------------------------------------------------------------
    # Observability & Inspection Commands
    # ------------------------------------------------------------------

    def do_unresolved(self, arg: str) -> None:
        """Show unresolved BankItems and BookItems after latest run/current durable world."""
        if self.state is None or self.state.is_closed:
            print("State is closed. Use `rehydrate`.")
            return

        queries = BookkeepingQueries(self.state)
        unresolved_bank = [
            b for b in sorted(self.state.bank_items.values(), key=lambda x: x.id)
            if queries.bank_remaining_units(b.id) > 0
        ]
        unresolved_book = [
            b for b in sorted(self.state.book_items.values(), key=lambda x: x.id)
            if queries.book_remaining_units(b.id) > 0
        ]

        print("UNRESOLVED ITEMS\n")
        print("Bank items:")
        if not unresolved_bank:
            print("  (None)")
        else:
            unres_map = {}
            if self.last_reconciliation_result:
                for ub in self.last_reconciliation_result.unresolved_bank_items:
                    unres_map[ub.bank_item_id] = ub.reason

            hypotheses_by_bank: dict[str, list[ReconciliationHypothesis]] = {}
            if self.last_recon_plan and self.last_recon_plan.hypotheses:
                for h in self.last_recon_plan.hypotheses:
                    for ba in h.bank_allocations:
                        hypotheses_by_bank.setdefault(ba.bank_item_id, []).append(h)

            for b in unresolved_bank:
                rem = queries.bank_remaining_units(b.id)
                res_dec = queries.derived.active_residual_bank_classification_by_bank_item.get(b.id)
                if res_dec is not None and res_dec.status == ResidualBankClassificationStatus.HOLD:
                    reason = f"HOLD: {res_dec.hold_reason}"
                else:
                    reason = unres_map.get(b.id, "UNMATCHED")

                print(f"  {b.id}: remaining {format_money(rem, b.currency)} / {format_money(b.amount_units, b.currency)}")
                print(f"    reason: {reason}")
                if res_dec is not None and res_dec.status == ResidualBankClassificationStatus.HOLD:
                    if res_dec.required_evidence:
                        print(f"    required evidence: {', '.join(res_dec.required_evidence)}")
                    if res_dec.rationale:
                        print(f"    rationale: {res_dec.rationale}")
                hyps = hypotheses_by_bank.get(b.id, [])
                if hyps:
                    print(f"    hard candidate count: {len(hyps)}")
                    for h in hyps:
                        req_review = (
                            "YES"
                            if h.eligibility == Eligibility.COUNTERFACTUAL_ONLY
                            or (
                                h.allocation_support == AllocationSupport.UNIQUE_INFERENCE
                                and not self.state.context.policy.auto_reconcile_unique_inferred_allocation
                            )
                            else "NO"
                        )
                        print(
                            f"      hypothesis {h.id}: identity={h.admissibility.value}, "
                            f"allocation={h.allocation_support.value}, "
                            f"eligibility={h.eligibility.value}, "
                            f"review_required={req_review}"
                        )
                print()

        print("Book items:")
        if not unresolved_book:
            print("  (None)")
        else:
            holds_map = {}
            if self.last_result and self.last_result.holds:
                for h in self.last_result.holds:
                    holds_map[h.book_item_id] = h

            hypotheses_by_book: dict[str, list[ReconciliationHypothesis]] = {}
            if self.last_recon_plan and self.last_recon_plan.hypotheses:
                for h in self.last_recon_plan.hypotheses:
                    for ba in h.book_allocations:
                        hypotheses_by_book.setdefault(ba.book_item_id, []).append(h)

            for b in unresolved_book:
                rem = queries.book_remaining_units(b.id)
                print(f"  {b.id}: remaining {format_money(rem, b.currency)} / {format_money(b.amount_units, b.currency)}")
                if b.id in holds_map:
                    h = holds_map[b.id]
                    print(f"    hold reason: {h.reason}")
                    if h.rationale:
                        print(f"    hold rationale: {h.rationale}")
                hyps = hypotheses_by_book.get(b.id, [])
                if hyps:
                    print(f"    hard candidate count: {len(hyps)}")
                    for h in hyps:
                        print(
                            f"      hypothesis {h.id}: identity={h.admissibility.value}, "
                            f"allocation={h.allocation_support.value}, "
                            f"eligibility={h.eligibility.value}"
                        )
                print()

    def do_review(self, arg: str) -> None:
        """Inspect review-only hypotheses from the detached last session result."""
        review_hyps = ()
        if self.last_reconciliation_result:
            review_hyps = self.last_reconciliation_result.review_hypotheses
        elif self.last_result is not None:
            review_hyps = self.last_result.review_hypotheses

        if not review_hyps:
            print("No review-only hypotheses from the last session.")
            return

        print("REVIEW-ONLY HYPOTHESES\n")
        for hyp in review_hyps:
            bank_ids = [a.bank_item_id for a in hyp.bank_allocations]
            book_ids = [a.book_item_id for a in hyp.book_allocations]
            bank_allocs = ", ".join(
                f"{a.bank_item_id} ({format_money(a.amount_units)})" for a in hyp.bank_allocations
            )
            book_allocs = ", ".join(
                f"{a.book_item_id} ({format_money(a.amount_units)})" for a in hyp.book_allocations
            )
            print(f"Hypothesis: {hyp.id}")
            print(f"  BankItems: {bank_ids}")
            print(f"  BookItems: {book_ids}")
            print(f"  Allocations:")
            print(f"    Bank: {bank_allocs}")
            print(f"    Book: {book_allocs}")
            print(f"  Identity support:   {hyp.admissibility.value}")
            print(f"  Allocation support: {hyp.allocation_support.value}")
            print(f"  Semantic score:     {hyp.utility}")
            print(f"  Semantic value:     {hyp.exact_semantic_value:,}")
            print(f"  Eligibility:        {hyp.eligibility.value}")
            if hyp.semantic_rationale:
                print(f"  Rationale:          {hyp.semantic_rationale}")

            policy_reason = "Policy prevents automatic reconciliation"
            if (
                hyp.allocation_support == AllocationSupport.UNIQUE_INFERENCE
                and self.state is not None
                and not self.state.context.policy.auto_reconcile_unique_inferred_allocation
            ):
                policy_reason = (
                    "Policy auto_reconcile_unique_inferred_allocation is False "
                    "(requires manual review for UNIQUE_INFERENCE)"
                )
            print(f"  Policy reason:      {policy_reason}")
            print()

    def do_history(self, arg: str) -> None:
        """Show durable artifact counts/history."""
        if self.state is None or self.state.is_closed:
            snap = self.repository.load_snapshot(company_id=self.company_id)
            routing_count = len(snap.routing_decisions)
            routing_inval_count = len(snap.routing_invalidations)
            class_count = len(snap.classifications)
            class_inval_count = len(snap.classification_invalidations)
            res_class_count = len(snap.residual_bank_classifications)
            res_inval_count = len(snap.residual_bank_classification_invalidations)
            res_posting_count = len(snap.residual_bank_postings)
            recon_count = len(snap.reconciliations)
            recon_inval_count = len(snap.reconciliation_invalidations)
            assertion_count = len(snap.book_item_evidence_assertions)
            assertion_inval_count = len(snap.book_item_evidence_invalidations)
        else:
            routing_count = len(self.state.routing_decisions)
            routing_inval_count = len(self.state.routing_invalidations)
            class_count = len(self.state.classifications)
            class_inval_count = len(self.state.classification_invalidations)
            res_class_count = len(self.state.residual_bank_classifications)
            res_inval_count = len(self.state.residual_bank_classification_invalidations)
            res_posting_count = len(self.state.residual_bank_postings)
            recon_count = len(self.state.reconciliations)
            recon_inval_count = len(self.state.reconciliation_invalidations)
            assertion_count = len(self.state.book_item_evidence_assertions)
            assertion_inval_count = len(self.state.book_item_evidence_invalidations)

        print("DURABLE ARTIFACT HISTORY\n")
        print(f"  Routing decisions:              {routing_count} (invalidations: {routing_inval_count})")
        print(f"  Legacy book classifications:    {class_count} (invalidations: {class_inval_count})")
        print(f"  Residual bank classifications:  {res_class_count} (invalidations: {res_inval_count})")
        print(f"  Residual bank postings:         {res_posting_count}")
        print(f"  Reconciliations:                {recon_count} (invalidations: {recon_inval_count})")
        print(f"  Evidence assertions:            {assertion_count} (invalidations: {assertion_inval_count})")

    def do_evidence(self, arg: str) -> None:
        """Inspect durable and effective evidence for a BookItem: evidence <book_item_id>."""
        book_item_id = arg.strip()
        if not book_item_id:
            print("Usage: evidence <book_item_id>")
            return

        state = self._get_or_hydrate_state()
        book_item = state.get_book_item(book_item_id)
        if book_item is None:
            print(f"BookItem {book_item_id!r} not found in current state.")
            return

        queries = BookkeepingQueries(state)

        print(f"RAW BOOK ITEM: {book_item.id}")
        print(f"  raw counterparty: {book_item.counterparty_id or 'None'}")
        print(f"  raw reference:    {book_item.reference or 'None'}")
        print(f"  raw description:  {book_item.description or 'None'}")
        print(f"  provenance refs:  {list(book_item.provenance_refs) if book_item.provenance_refs else 'None'}")

        print(f"\nEFFECTIVE SEMANTICS:")
        eff_cp = queries.effective_counterparty(book_item_id)
        eff_cp_str = f"{eff_cp.name} ({eff_cp.id})" if eff_cp else "None"
        print(f"  effective counterparty: {eff_cp_str}")
        print(f"  effective reference:    {queries.effective_reference(book_item_id) or 'None'}")
        print(f"  effective description:  {queries.effective_description(book_item_id) or 'None'}")
        eff_docs = queries.effective_evidence_documents(book_item_id)
        eff_doc_ids = [d.id for d in eff_docs]
        print(f"  evidence documents:     {eff_doc_ids if eff_doc_ids else 'None'}")

        print(f"\nASSERTION HISTORY:")
        assertions = queries.evidence_assertions_for_book_item(book_item_id)
        if not assertions:
            print("  (No evidence assertions recorded)")
        else:
            invalidated_ids = {
                inv.assertion_id
                for inv in state.book_item_evidence_invalidations.values()
            }
            superseded_ids = {
                a.supersedes_assertion_id
                for a in state.book_item_evidence_assertions.values()
                if a.supersedes_assertion_id is not None
            }

            for a in sorted(assertions, key=lambda x: (x.created_at, x.id)):
                if a.id in invalidated_ids:
                    status = "INVALIDATED"
                elif a.id in superseded_ids:
                    status = "SUPERSEDED"
                else:
                    status = "ACTIVE"
                print(f"  ID:         {a.id}")
                print(f"    type:       {a.evidence_type.value}")
                print(f"    value:      {a.value!r}")
                print(f"    source:     {a.source.value}")
                print(f"    supersedes: {a.supersedes_assertion_id or 'None'}")
                print(f"    status:     {status}")

    # ------------------------------------------------------------------
    # Utilities & Exit
    # ------------------------------------------------------------------

    def do_debug(self, arg: str) -> None:
        """Toggle debug mode: debug on / debug off."""
        mode = arg.strip().lower()
        if mode in ("on", "1", "true"):
            self.debug = True
            print("Debug mode ON.")
        elif mode in ("off", "0", "false"):
            self.debug = False
            print("Debug mode OFF.")
        else:
            self.debug = not self.debug
            print(f"Debug mode {'ON' if self.debug else 'OFF'}.")

    def default(self, line: str) -> None:
        """Fallback handler for natural language input or unrecognized commands."""
        line = line.strip()
        if not line:
            return
        if self.operator_enabled and self.operator is not None:
            try:
                self._sync_to_workbench()
                print("\nToro Operator is thinking...")
                reply = self.operator.handle_message(line)
                self._sync_from_workbench()
                print(f"\nToro:\n{reply}\n")
            except Exception as exc:
                if self.debug:
                    import traceback
                    traceback.print_exc()
                print(f"\nOperator error: {exc}\n")
        else:
            print(
                f"*** Unknown command: '{line}'. Type 'help' for available commands, "
                "or run with --operator (or type 'operator on') for natural-language assistance."
            )

    def do_operator(self, arg: str) -> None:
        """Send a natural-language query to the Toro Bookkeeping Operator."""
        if not self.operator:
            self.operator = BookkeepingOperator(
                workbench=self.workbench,
                model_name=self.operator_model,
            )
            self.operator_enabled = True
        self.default(arg)

    def do_clear_chat(self, arg: str) -> None:
        """Clear conversational history of the Bookkeeping Operator."""
        if self.operator:
            self.operator.clear_chat()
            print("Operator conversational memory cleared.")
        else:
            print("Operator is not active.")

    def do_operator_trace(self, arg: str) -> None:
        """Toggle detailed tool call tracing for the Bookkeeping Operator."""
        if not self.operator:
            self.operator = BookkeepingOperator(
                workbench=self.workbench,
                model_name=self.operator_model,
            )
            self.operator_enabled = True
        self.operator.trace_enabled = not self.operator.trace_enabled
        status = "ON" if self.operator.trace_enabled else "OFF"
        print(f"Operator detailed tracing is now {status}.")

    def do_quit(self, arg: str) -> bool:
        """Exit the lab."""
        if self.state is not None and not self.state.is_closed:
            self.state.close()
            self.state = None
        return True

    def do_exit(self, arg: str) -> bool:
        """Exit the lab."""
        return self.do_quit(arg)

    def do_EOF(self, arg: str) -> bool:
        """Exit on EOF (Ctrl+D)."""
        print()
        return self.do_quit(arg)


def main() -> None:
    parser = argparse.ArgumentParser(description="BookkeepingState Developer Lab")
    parser.add_argument(
        "--scenario",
        type=str,
        default=None,
        help="Scenario name to load from catalog (e.g. scenario_c_many_to_one)",
    )
    parser.add_argument(
        "--debug",
        action="store_true",
        help="Enable full tracebacks on exceptions",
    )
    parser.add_argument(
        "--semantic-provider",
        choices=["deterministic", "llm"],
        default="deterministic",
        help="Semantic reasoning provider ('deterministic' or 'llm')",
    )
    parser.add_argument(
        "--run",
        action="store_true",
        help="Execute one session run immediately and exit",
    )
    parser.add_argument(
        "--operator",
        action=argparse.BooleanOptionalAction,
        default=True,
        help="Enable natural-language Operator LLM assistant (default: enabled; use --no-operator to disable)",
    )
    parser.add_argument(
        "--operator-model",
        type=str,
        default="gpt-5.6-luna",
        help="Model name for the Operator LLM (default: gpt-5.6-luna)",
    )
    parser.add_argument(
        "--production-state",
        action="store_true",
        help="Hydrate BookkeepingState directly from production PostgreSQL database",
    )
    parser.add_argument(
        "--company-id",
        type=str,
        default="toro-synthetic-bookkeeping",
        help="Company ID or slug to hydrate from production database (default: toro-synthetic-bookkeeping)",
    )
    args = parser.parse_args()

    if args.scenario and args.scenario not in SCENARIOS_MAP:
        print(f"Unknown scenario: {args.scenario}")
        print("Available scenarios:")
        for sc_name in sorted(SCENARIOS_MAP.keys()):
            print(f"  {sc_name}")
        sys.exit(1)

    lab = BookkeepingLab(
        scenario_name=args.scenario,
        debug=args.debug,
        semantic_provider=args.semantic_provider,
        operator=args.operator,
        operator_model=args.operator_model,
        production_state=args.production_state,
        company_id=args.company_id,
    )
    provider_info = (
        "LLM (model: gpt-5.6-luna)"
        if args.semantic_provider == "llm"
        else "DETERMINISTIC (offline heuristic)"
    )
    print("==================================================")
    print("       BookkeepingState Developer Lab (REPL)      ")
    print("==================================================")
    print("Type 'help' for available commands or 'quit' to exit.")
    if args.production_state:
        print(f"Active mode: PRODUCTION STATE ({lab.company_display_name})")
    elif args.scenario:
        print(f"Active scenario: {args.scenario} ({lab.company_display_name})")
    else:
        print(f"Active scenario: default demo ({lab.company_display_name})")
    print(f"Semantic provider: {provider_info}")
    if lab.operator_enabled:
        op_model = lab.operator_model or "gpt-5.6-luna"
        print(f"Operator: ENABLED ({op_model}) - ask natural language questions directly.")
    else:
        print("Operator: DISABLED - explicit commands only.")
    print()

    if args.run:
        lab.do_run("")
        if lab.state is not None and not lab.state.is_closed:
            lab.state.close()
        return

    try:
        lab.cmdloop()
    except KeyboardInterrupt:
        print("\nInterrupted. Exiting lab.")
        if lab.state is not None and not lab.state.is_closed:
            lab.state.close()


if __name__ == "__main__":
    main()
