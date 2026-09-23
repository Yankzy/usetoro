"""
BookkeepingState Temporal Challenge Worlds (Challenges 08-14).

Multi-session, trajectory-based falsification experiments designed to stress-test:
- Late evidence resolution and reconsideration
- Arrival ordering invariance (bank-before-invoice vs invoice-before-bank)
- Duplicate bank import exclusivity
- Reconciliation invalidation and capacity release
- Economic truth migration under replacement evidence
- Complex multi-session month-end convergence

These are falsification experiments, NOT regression scenarios.
They are kept separate from catalog.py.
"""

from __future__ import annotations

from dataclasses import dataclass, field, replace
from datetime import date, datetime, timezone
from enum import StrEnum
from typing import Any, Callable, Sequence

from bookkeeping_state_eval.dag.simulated_ase import SimulatedAseClassifier
from bookkeeping_state.domain.bank import BankAccount, BankItem
from bookkeeping_state.domain.books import BookItem as _DomainBookItem


def BookItem(*args: Any, **kwargs: Any) -> _DomainBookItem:
    if "source_type" not in kwargs:
        kwargs["source_type"] = SourceType.POSTED_BOOK_ITEM
    return _DomainBookItem(*args, **kwargs)

from bookkeeping_state.domain.commands import (
    CommandSource,
    InvalidateReconciliationCommand,
)
from bookkeeping_state.domain.context import AccountingPolicy, BookkeepingContext
from bookkeeping_state.domain.counterparties import (
    Counterparty,
    CounterpartyType,
)
from bookkeeping_state.domain.documents import Document
from bookkeeping_state.domain.enums import Direction, SourceType
from bookkeeping_state.domain.evidence import (
    BookItemEvidenceAssertion,
    BookItemEvidenceInvalidation,
    BookItemEvidenceType,
    EvidenceSource,
)
from bookkeeping_state.domain.reconciliations import (
    BankAllocation,
    BookAllocation,
    Reconciliation,
    ReconciliationInvalidation,
)
from bookkeeping_state.hydration.hydrator import BookkeepingHydrator
from bookkeeping_state_eval.persistence.in_memory import InMemoryBookkeepingRepository
from bookkeeping_state.persistence.repository import (
    BookkeepingRepository,
    BookkeepingSnapshot,
    PersistenceWriteSet,
)
from bookkeeping_state.reconciliation.service import ReconciliationService
from bookkeeping_state.routing.scorer import ZeroRoutingSemanticScoreProvider
from bookkeeping_state.routing.service import RoutingService
from bookkeeping_state_eval.scenarios.expected_truth import (
    ExpectedReconciliation,
    ExpectedTruth,
    ExpectedTruthVerdict,
    validate_expected_truth,
)
from bookkeeping_state_eval.scenarios.models import ScenarioDefinition
from bookkeeping_state_eval.scenarios.runner import seed_scenario_repository
from bookkeeping_state.session.bookkeeping_session import BookkeepingSession
from bookkeeping_state.session.result import SessionResult
from bookkeeping_state.state.fingerprint import (
    artifact_fingerprint,
    state_fingerprint,
)
from bookkeeping_state.state.queries import BookkeepingQueries
from bookkeeping_state.state.validation import validate_state
from bookkeeping_state.transitions.engine import TransitionEngine

DEFAULT_DATE = date(2026, 1, 15)
DEFAULT_CLOCK = datetime(2026, 1, 31, 12, 0, 0, tzinfo=timezone.utc)


# ======================================================================
# Trajectory Abstraction
# ======================================================================


class TemporalOpType(StrEnum):
    ADD_BANK_ITEMS = "ADD_BANK_ITEMS"
    ADD_BOOK_ITEMS = "ADD_BOOK_ITEMS"
    ADD_COUNTERPARTIES = "ADD_COUNTERPARTIES"
    ADD_DOCUMENTS = "ADD_DOCUMENTS"
    ADD_BOOK_ITEM_EVIDENCE = "ADD_BOOK_ITEM_EVIDENCE"
    INVALIDATE_BOOK_ITEM_EVIDENCE = "INVALIDATE_BOOK_ITEM_EVIDENCE"
    INVALIDATE_RECONCILIATION = "INVALIDATE_RECONCILIATION"
    RUN_SESSION = "RUN_SESSION"
    VERIFY = "VERIFY"


@dataclass(frozen=True, slots=True)
class AddBankItemsOp:
    bank_items: tuple[BankItem, ...]
    op_type: TemporalOpType = TemporalOpType.ADD_BANK_ITEMS
    description: str = "Add BankItems"


@dataclass(frozen=True, slots=True)
class AddBookItemsOp:
    book_items: tuple[BookItem, ...]
    op_type: TemporalOpType = TemporalOpType.ADD_BOOK_ITEMS
    description: str = "Add BookItems"


@dataclass(frozen=True, slots=True)
class AddCounterpartiesOp:
    counterparties: tuple[Counterparty, ...]
    op_type: TemporalOpType = TemporalOpType.ADD_COUNTERPARTIES
    description: str = "Add Counterparties"


@dataclass(frozen=True, slots=True)
class AddDocumentsOp:
    documents: tuple[Document, ...]
    op_type: TemporalOpType = TemporalOpType.ADD_DOCUMENTS
    description: str = "Add Documents"


@dataclass(frozen=True, slots=True)
class AddBookItemEvidenceOp:
    assertions: tuple[BookItemEvidenceAssertion, ...]
    op_type: TemporalOpType = TemporalOpType.ADD_BOOK_ITEM_EVIDENCE
    description: str = "Add BookItemEvidenceAssertions"


@dataclass(frozen=True, slots=True)
class InvalidateBookItemEvidenceOp:
    invalidation_id: str
    assertion_id: str
    reason: str = "Explicit evidence invalidation"
    op_type: TemporalOpType = TemporalOpType.INVALIDATE_BOOK_ITEM_EVIDENCE
    description: str = "Invalidate BookItemEvidenceAssertion"


@dataclass(frozen=True, slots=True)
class InvalidateReconciliationOp:
    reconciliation_id: str | None = None
    bank_item_id: str | None = None
    book_item_id: str | None = None
    reason: str = "Explicit invalidation"
    op_type: TemporalOpType = TemporalOpType.INVALIDATE_RECONCILIATION
    description: str = "Invalidate Reconciliation"


@dataclass(frozen=True, slots=True)
class RunSessionOp:
    session_id_suffix: str | None = None
    op_type: TemporalOpType = TemporalOpType.RUN_SESSION
    description: str = "Run BookkeepingSession"


@dataclass(frozen=True, slots=True)
class VerifyOp:
    expected_truth: ExpectedTruth
    op_type: TemporalOpType = TemporalOpType.VERIFY
    description: str = "Verify Checkpoint"


TemporalOperation = (
    AddBankItemsOp
    | AddBookItemsOp
    | AddCounterpartiesOp
    | AddDocumentsOp
    | AddBookItemEvidenceOp
    | InvalidateBookItemEvidenceOp
    | InvalidateReconciliationOp
    | RunSessionOp
    | VerifyOp
)


@dataclass(frozen=True, slots=True)
class TemporalStep:
    """
    One step in a temporal challenge trajectory.
    """

    step_id: str
    operation: TemporalOperation
    expected_checkpoint: ExpectedTruth | None = None
    description: str = ""


@dataclass(frozen=True, slots=True)
class TemporalChallengeDefinition:
    """
    Trajectory challenge definition representing a multi-session, multi-mutation world.
    """

    challenge_id: str
    name: str
    difficulty: int
    tags: tuple[str, ...]
    initial_scenario: ScenarioDefinition
    steps: tuple[TemporalStep, ...]
    final_expected_truth: ExpectedTruth

    @property
    def description(self) -> str:
        return self.initial_scenario.description


@dataclass(frozen=True, slots=True)
class StepSummary:
    """
    Detached summary of one completed step in a temporal challenge.
    """

    step_index: int
    step_id: str
    op_type: str
    persistence_revision: int
    summary: str
    truth_match: bool | None = None
    mismatches: tuple[str, ...] = ()


@dataclass(frozen=True, slots=True)
class TemporalChallengeResult:
    """
    Detached, immutable outcome of executing a full temporal challenge.
    """

    challenge_id: str
    is_pass: bool
    final_persistence_revision: int
    final_durable_fingerprint: str
    step_summaries: tuple[StepSummary, ...]
    final_truth_verdict: ExpectedTruthVerdict
    failure_messages: tuple[str, ...]


# ======================================================================
# Trajectory Execution Runner
# ======================================================================


class TemporalChallengeRunner:
    """
    Deterministic trajectory runner for BookkeepingState temporal challenges.

    Invariants enforced after every step:
    1. Local state_revision starts at S0 on fresh hydration.
    2. Persistence revision never decreases.
    3. State passes independent validate_state.
    4. Fingerprint is deterministic for identical durable state.
    5. Active allocations never exceed bank/book capacity.
    6. No stale runtime hypothesis survives across session boundaries.
    7. Inactive reconciliations consume zero capacity.
    """

    def __init__(
        self,
        *,
        clock_factory: Callable[[], datetime] | None = None,
    ) -> None:
        self._clock_factory = clock_factory or (lambda: DEFAULT_CLOCK)

    def run(
        self,
        challenge: TemporalChallengeDefinition,
        *,
        repository: BookkeepingRepository | None = None,
    ) -> TemporalChallengeResult:
        repo = repository or seed_scenario_repository(challenge.initial_scenario)
        company_id = challenge.initial_scenario.context.company_id
        clock_time = challenge.initial_scenario.clock_time or self._clock_factory()

        step_summaries: list[StepSummary] = []
        failure_messages: list[str] = []
        last_pers_rev = repo.load_snapshot(company_id=company_id).persistence_revision

        # Initial baseline summary (Step 0)
        step_summaries.append(
            StepSummary(
                step_index=0,
                step_id="step-0-initial",
                op_type="INITIAL_WORLD",
                persistence_revision=last_pers_rev,
                summary=f"Initial durable world hydrated at P{last_pers_rev}",
                truth_match=True,
                mismatches=(),
            )
        )

        session_counter = 0

        for idx, step in enumerate(challenge.steps, start=1):
            op = step.operation

            # ----------------------------------------------------------
            # 1. Execute Typed Operation
            # ----------------------------------------------------------
            if isinstance(op, AddBankItemsOp):
                snap = repo.load_snapshot(company_id=company_id)
                ws = PersistenceWriteSet(bank_items=op.bank_items)
                commit_res = repo.commit(
                    company_id=company_id,
                    expected_revision=snap.persistence_revision,
                    write_set=ws,
                )
                summary_text = f"Added {len(op.bank_items)} BankItem(s)"

            elif isinstance(op, AddBookItemsOp):
                snap = repo.load_snapshot(company_id=company_id)
                ws = PersistenceWriteSet(book_items=op.book_items)
                commit_res = repo.commit(
                    company_id=company_id,
                    expected_revision=snap.persistence_revision,
                    write_set=ws,
                )
                summary_text = f"Added {len(op.book_items)} BookItem(s)"

            elif isinstance(op, AddCounterpartiesOp):
                snap = repo.load_snapshot(company_id=company_id)
                ws = PersistenceWriteSet(counterparties=op.counterparties)
                commit_res = repo.commit(
                    company_id=company_id,
                    expected_revision=snap.persistence_revision,
                    write_set=ws,
                )
                summary_text = f"Added {len(op.counterparties)} Counterparty(ies)"

            elif isinstance(op, AddDocumentsOp):
                snap = repo.load_snapshot(company_id=company_id)
                ws = PersistenceWriteSet(documents=op.documents)
                commit_res = repo.commit(
                    company_id=company_id,
                    expected_revision=snap.persistence_revision,
                    write_set=ws,
                )
                summary_text = f"Added {len(op.documents)} Document(s)"

            elif isinstance(op, AddBookItemEvidenceOp):
                snap = repo.load_snapshot(company_id=company_id)
                ws = PersistenceWriteSet(
                    book_item_evidence_assertions=op.assertions
                )
                commit_res = repo.commit(
                    company_id=company_id,
                    expected_revision=snap.persistence_revision,
                    write_set=ws,
                )
                summary_text = f"Added {len(op.assertions)} BookItemEvidenceAssertion(s)"

            elif isinstance(op, InvalidateBookItemEvidenceOp):
                snap = repo.load_snapshot(company_id=company_id)
                inv = BookItemEvidenceInvalidation(
                    id=op.invalidation_id,
                    assertion_id=op.assertion_id,
                    reason=op.reason,
                    session_id=f"inval-session-{idx}",
                    state_revision_at_invalidation=0,
                    created_at=clock_time,
                )
                ws = PersistenceWriteSet(
                    book_item_evidence_invalidations=(inv,)
                )
                commit_res = repo.commit(
                    company_id=company_id,
                    expected_revision=snap.persistence_revision,
                    write_set=ws,
                )
                summary_text = f"Invalidated BookItemEvidenceAssertion {op.assertion_id}"

            elif isinstance(op, InvalidateReconciliationOp):
                # Hydrate temporary local state to resolve target reconciliation
                temp_hydrator = BookkeepingHydrator(
                    repository=repo,
                    clock=lambda: clock_time,
                    session_id_factory=lambda: f"inval-session-{idx}",
                )
                temp_state = temp_hydrator.hydrate(company_id=company_id)
                target_recon_id = op.reconciliation_id

                if target_recon_id is None:
                    queries = BookkeepingQueries(temp_state)
                    if op.bank_item_id is not None:
                        active_recons = queries.reconciliations_for_bank_item(
                            op.bank_item_id
                        )
                        if active_recons:
                            target_recon_id = active_recons[0].id
                    elif op.book_item_id is not None:
                        active_recons = queries.reconciliations_for_book_item(
                            op.book_item_id
                        )
                        if active_recons:
                            target_recon_id = active_recons[0].id

                if target_recon_id is None:
                    temp_state.close()
                    raise ValueError(
                        f"Could not resolve reconciliation to invalidate in step {step.step_id}"
                    )

                cmd = InvalidateReconciliationCommand(
                    command_id=f"cmd-inval-{idx}",
                    expected_state_revision=temp_state.revision,
                    invalidation_id=f"inval-{idx}-{target_recon_id}",
                    reconciliation_id=target_recon_id,
                    reason=op.reason,
                    session_id=temp_state.session_id,
                    source=CommandSource.HUMAN,
                    issued_at=clock_time,
                )
                engine = TransitionEngine(repository=repo)
                t_res = engine.apply(command=cmd, state=temp_state)
                if not t_res.applied:
                    rej_code = t_res.rejection.code if t_res.rejection else "UNKNOWN"
                    rej_msg = t_res.rejection.message if t_res.rejection else "Unknown rejection"
                    temp_state.close()
                    raise RuntimeError(
                        f"Failed to invalidate reconciliation {target_recon_id}: "
                        f"{rej_code}: {rej_msg}"
                    )
                temp_state.close()
                summary_text = f"Invalidated reconciliation {target_recon_id}"

            elif isinstance(op, RunSessionOp):
                session_counter += 1
                sess_id = f"session-{challenge.challenge_id}-{session_counter}"
                if op.session_id_suffix:
                    sess_id += f"-{op.session_id_suffix}"

                # Strict lifecycle: hydrate fresh local S0 -> run -> persist -> close
                hydrator = BookkeepingHydrator(
                    repository=repo,
                    clock=lambda: clock_time,
                    session_id_factory=lambda: sess_id,
                )
                live_state = hydrator.hydrate(
                    company_id=company_id,
                    session_id=sess_id,
                )
                assert live_state.revision == 0, (
                    f"Invariant violated: local state_revision was S{live_state.revision}, "
                    "expected S0 on fresh hydration"
                )

                engine = TransitionEngine(repository=repo)
                routing_svc = RoutingService(
                    semantic_provider=challenge.initial_scenario.routing_semantic_provider
                    or ZeroRoutingSemanticScoreProvider()
                )
                dag_cls = (
                    challenge.initial_scenario.dag_classifier or SimulatedAseClassifier()
                )
                recon_svc = (
                    challenge.initial_scenario.reconciliation_service
                    or ReconciliationService()
                )

                session = BookkeepingSession(
                    state=live_state,
                    engine=engine,
                    routing_service=routing_svc,
                    dag_classifier=dag_cls,
                    reconciliation_service=recon_svc,
                )
                s_res = session.run()
                assert live_state.is_closed, "Live state was not closed by session termination"

                if not s_res.is_success:
                    failure_messages.append(
                        f"Step {step.step_id} session failure: {s_res.failure_stage}: {s_res.failure_reason}"
                    )

                recons_created = (
                    s_res.reconciliation_stage_result.command_count
                    if s_res.reconciliation_stage_result
                    else 0
                )
                summary_text = f"Session {session_counter} executed -> {recons_created} reconciliation(s) created"

            elif isinstance(op, VerifyOp):
                summary_text = "Checkpoint verification"

            # ----------------------------------------------------------
            # 2. Rehydrate Fresh and Enforce Invariants
            # ----------------------------------------------------------
            check_hydrator = BookkeepingHydrator(
                repository=repo,
                clock=lambda: clock_time,
                session_id_factory=lambda: f"check-hydrator-{idx}",
            )
            rehydrated_state = check_hydrator.hydrate(
                company_id=company_id,
                session_id=f"check-{idx}",
            )

            try:
                # Invariant 1: S0 initial revision
                assert rehydrated_state.revision == 0

                # Invariant 2: Persistence revision monotonicity
                curr_pers_rev = rehydrated_state.persistence_revision
                assert curr_pers_rev >= last_pers_rev, (
                    f"Invariant violated: persistence revision decreased from "
                    f"P{last_pers_rev} to P{curr_pers_rev}"
                )
                last_pers_rev = curr_pers_rev

                # Invariant 3: Independent state validation
                val_rep = validate_state(rehydrated_state)
                if not val_rep.is_valid:
                    val_errs = [f"{e.code}: {e.message}" for e in val_rep.errors]
                    failure_messages.append(
                        f"Step {step.step_id} closing state invalid: {'; '.join(val_errs)}"
                    )

                # Invariant 4: No stale hypotheses survived
                assert len(rehydrated_state.reconciliation_hypotheses) == 0, (
                    "Stale runtime hypotheses survived rehydration"
                )

                # Invariant 5: Capacity non-negativity
                queries = BookkeepingQueries(rehydrated_state)
                for b_acc in queries.bank_accounts():
                    for b_item in queries.bank_items_for_account(b_acc.id):
                        assert queries.derived.bank_remaining(b_item.id) >= 0
                for j_item in rehydrated_state.book_items.values():
                    assert queries.derived.book_remaining(j_item.id) >= 0

                # Invariant 6: Deterministic fingerprint
                fp1 = artifact_fingerprint(rehydrated_state)
                rehydrated_state_2 = check_hydrator.hydrate(
                    company_id=company_id,
                    session_id=f"check-fp-{idx}",
                )
                try:
                    fp2 = artifact_fingerprint(rehydrated_state_2)
                    assert fp1 == fp2, "Durable fingerprint is non-deterministic"
                finally:
                    rehydrated_state_2.close()

                # Step Checkpoint Validation (if requested)
                step_truth_match: bool | None = None
                step_mismatches: tuple[str, ...] = ()
                expected_to_check = step.expected_checkpoint or (
                    op.expected_truth if isinstance(op, VerifyOp) else None
                )
                if expected_to_check is not None:
                    verdict = validate_expected_truth(queries, expected_to_check)
                    step_truth_match = verdict.is_match
                    step_mismatches = verdict.mismatches
                    if not verdict.is_match:
                        failure_messages.append(
                            f"Step {step.step_id} checkpoint mismatch:\n"
                            + "\n".join(verdict.mismatches)
                        )

            finally:
                rehydrated_state.close()

            step_summaries.append(
                StepSummary(
                    step_index=idx,
                    step_id=step.step_id,
                    op_type=op.op_type.value,
                    persistence_revision=curr_pers_rev,
                    summary=summary_text,
                    truth_match=step_truth_match,
                    mismatches=step_mismatches,
                )
            )

        # --------------------------------------------------------------
        # 3. Final Rehydration & Expected Truth Validation
        # --------------------------------------------------------------
        final_hydrator = BookkeepingHydrator(
            repository=repo,
            clock=lambda: clock_time,
            session_id_factory=lambda: f"final-eval-{challenge.challenge_id}",
        )
        final_state = final_hydrator.hydrate(
            company_id=company_id,
            session_id=f"final-{challenge.challenge_id}",
        )
        try:
            final_queries = BookkeepingQueries(final_state)
            final_verdict = validate_expected_truth(
                final_queries, challenge.final_expected_truth
            )
            final_fp = artifact_fingerprint(final_state)
            final_p_rev = final_state.persistence_revision
            if not final_verdict.is_match:
                failure_messages.append(
                    "Final expected truth mismatch:\n"
                    + "\n".join(final_verdict.mismatches)
                )
        finally:
            final_state.close()

        is_pass = len(failure_messages) == 0 and final_verdict.is_match

        return TemporalChallengeResult(
            challenge_id=challenge.challenge_id,
            is_pass=is_pass,
            final_persistence_revision=final_p_rev,
            final_durable_fingerprint=final_fp,
            step_summaries=tuple(step_summaries),
            final_truth_verdict=final_verdict,
            failure_messages=tuple(failure_messages),
        )


# ======================================================================
# Challenge 08: Late Evidence Resolution
# ======================================================================


def _build_challenge_08() -> TemporalChallengeDefinition:
    ctx = BookkeepingContext(
        company_id="challenge-08",
        period_start=date(2026, 1, 1),
        period_end=date(2026, 1, 31),
        base_currency="MAD",
        policy=AccountingPolicy(
            chart_of_accounts_id="pcge",
            reconciliation_date_window_days=45,
            require_exact_currency_match=True,
            allow_partial_book_reconciliation=True,
            allow_partial_bank_reconciliation=False,
        ),
    )
    acc = BankAccount(id="acc-main", name="BMCE MAD", currency="MAD")

    # Initial: 25k bank and 25k book with no shared references and unknown CP
    b_item = BankItem(
        id="bank-a",
        bank_account_id="acc-main",
        date=DEFAULT_DATE,
        amount_units="25000",
        direction=Direction.BANK_INFLOW,
        currency="MAD",
        description="Virement Client Atlas INV-81818",
        reference="INV-81818",
    )
    j_item = BookItem(
        id="book-a",
        origin_period="2026-01",
        date=DEFAULT_DATE,
        amount_units="25000",
        direction=Direction.BOOK_BANK_DEBIT,
        currency="MAD",
        description="Ecriture encaissement 25000",
        reference=None,
        counterparty_id=None,
    )

    sc = ScenarioDefinition(
        scenario_id="challenge_08_late_evidence_resolution",
        name="Late Evidence Resolution",
        description=(
            "Session 1 abstains due to INSUFFICIENT_EVIDENCE. "
            "Durable counterparty and reference evidence arrives via BookItemEvidenceAssertion in Step 2. "
            "Session 2 reconciles fully, proving past abstention does not block reconsideration."
        ),
        context=ctx,
        bank_accounts=(acc,),
        bank_items=(b_item,),
        book_items=(j_item,),
        expected_truth=ExpectedTruth(),
    )

    steps = (
        # Step 1: Session 1 runs without evidence -> 0 reconciliations
        TemporalStep(
            step_id="step-1-session-1-abstains",
            operation=RunSessionOp(),
            expected_checkpoint=ExpectedTruth(
                expected_pairwise_allocations={},
                expected_bank_remaining={"bank-a": 25_000},
                expected_book_remaining={"book-a": 25_000},
                expected_unreconciled_bank_items=["bank-a"],
                expected_unreconciled_book_items=["book-a"],
            ),
            description="Session 1: Bank & Book remain unresolved due to lack of identity evidence",
        ),
        # Step 2: Durable counterparty arrives in storage
        TemporalStep(
            step_id="step-2-durable-counterparty-arrives",
            operation=AddCounterpartiesOp(
                counterparties=(
                    Counterparty(
                        id="cp-atlas",
                        name="Client Atlas",
                        counterparty_type=CounterpartyType.CUSTOMER,
                    ),
                )
            ),
            description="Introduce Counterparty('cp-atlas', 'Client Atlas') into durable storage",
        ),
        # Step 3: Durable evidence assertions attach counterparty and reference to book-a
        TemporalStep(
            step_id="step-3-durable-evidence-assertion",
            operation=AddBookItemEvidenceOp(
                assertions=(
                    BookItemEvidenceAssertion(
                        id="ev-book-a-cp",
                        book_item_id="book-a",
                        evidence_type=BookItemEvidenceType.COUNTERPARTY,
                        value="cp-atlas",
                        source=EvidenceSource.MANUAL,
                        session_id="temporal-seed",
                        state_revision_at_creation=1,
                        created_at=DEFAULT_CLOCK,
                    ),
                    BookItemEvidenceAssertion(
                        id="ev-book-a-ref",
                        book_item_id="book-a",
                        evidence_type=BookItemEvidenceType.REFERENCE,
                        value="INV-81818",
                        source=EvidenceSource.MANUAL,
                        session_id="temporal-seed",
                        state_revision_at_creation=1,
                        created_at=DEFAULT_CLOCK,
                    ),
                )
            ),
            description="Attach COUNTERPARTY and REFERENCE evidence assertions to book-a",
        ),
        # Step 4: Session 2 runs with new evidence -> Full reconciliation
        TemporalStep(
            step_id="step-4-session-2-reconciles",
            operation=RunSessionOp(),
            expected_checkpoint=ExpectedTruth(
                expected_pairwise_allocations={("bank-a", "book-a"): 25_000},
                expected_bank_remaining={"bank-a": 0},
                expected_book_remaining={"book-a": 0},
                expected_fully_reconciled_bank_items=["bank-a"],
                expected_fully_reconciled_book_items=["book-a"],
            ),
            description="Session 2: Reconciles bank-a to book-a after evidence arrival",
        ),
    )

    final_truth = ExpectedTruth(
        expected_pairwise_allocations={("bank-a", "book-a"): 25_000},
        expected_bank_remaining={"bank-a": 0},
        expected_book_remaining={"book-a": 0},
        expected_fully_reconciled_bank_items=["bank-a"],
        expected_fully_reconciled_book_items=["book-a"],
        expected_unreconciled_bank_items=[],
        expected_unreconciled_book_items=[],
    )

    return TemporalChallengeDefinition(
        challenge_id="challenge_08_late_evidence_resolution",
        name="Late Evidence Resolution",
        difficulty=3,
        tags=("temporal", "evidence", "reconsideration", "admissibility"),
        initial_scenario=sc,
        steps=steps,
        final_expected_truth=final_truth,
    )


# ======================================================================
# Challenge 09: Bank Before Invoice
# ======================================================================


def _build_challenge_09() -> TemporalChallengeDefinition:
    ctx = BookkeepingContext(
        company_id="challenge-09",
        period_start=date(2026, 1, 1),
        period_end=date(2026, 1, 31),
        base_currency="MAD",
        policy=AccountingPolicy(
            chart_of_accounts_id="pcge",
            reconciliation_date_window_days=45,
            require_exact_currency_match=True,
            allow_partial_book_reconciliation=True,
            allow_partial_bank_reconciliation=False,
        ),
    )
    acc = BankAccount(id="acc-main", name="BMCE MAD", currency="MAD")

    b_item = BankItem(
        id="bank-alpha",
        bank_account_id="acc-main",
        date=DEFAULT_DATE,
        amount_units="75000",
        direction=Direction.BANK_INFLOW,
        currency="MAD",
        description="Virement Client Alpha INV-75",
        reference="INV-75",
    )

    sc = ScenarioDefinition(
        scenario_id="challenge_09_bank_before_invoice",
        name="Bank Before Invoice",
        description=(
            "Bank transaction arrives first without matching book entry. "
            "Invoice arrives later. System reconciles fully in Session 2."
        ),
        context=ctx,
        bank_accounts=(acc,),
        bank_items=(b_item,),
        book_items=(),
        expected_truth=ExpectedTruth(),
    )

    book_alpha = BookItem(
        id="book-alpha",
        origin_period="2026-01",
        date=DEFAULT_DATE,
        amount_units="75000",
        direction=Direction.BOOK_BANK_DEBIT,
        currency="MAD",
        description="Facture Client Alpha INV-75",
        reference="INV-75",
    )

    steps = (
        TemporalStep(
            step_id="step-1-bank-unresolved",
            operation=RunSessionOp(),
            expected_checkpoint=ExpectedTruth(
                expected_pairwise_allocations={},
                expected_bank_remaining={"bank-alpha": 75_000},
                expected_unreconciled_bank_items=["bank-alpha"],
            ),
            description="Session 1: BankItem arrives first, remains unresolved",
        ),
        TemporalStep(
            step_id="step-2-invoice-arrives",
            operation=AddBookItemsOp(book_items=(book_alpha,)),
            description="BookItem book-alpha arrives into durable storage",
        ),
        TemporalStep(
            step_id="step-3-reconciles",
            operation=RunSessionOp(),
            expected_checkpoint=ExpectedTruth(
                expected_pairwise_allocations={("bank-alpha", "book-alpha"): 75_000},
                expected_bank_remaining={"bank-alpha": 0},
                expected_book_remaining={"book-alpha": 0},
                expected_fully_reconciled_bank_items=["bank-alpha"],
                expected_fully_reconciled_book_items=["book-alpha"],
            ),
            description="Session 2: Reconciles bank-alpha to book-alpha",
        ),
    )

    final_truth = ExpectedTruth(
        expected_pairwise_allocations={("bank-alpha", "book-alpha"): 75_000},
        expected_bank_remaining={"bank-alpha": 0},
        expected_book_remaining={"book-alpha": 0},
        expected_fully_reconciled_bank_items=["bank-alpha"],
        expected_fully_reconciled_book_items=["book-alpha"],
        expected_unreconciled_bank_items=[],
        expected_unreconciled_book_items=[],
    )

    return TemporalChallengeDefinition(
        challenge_id="challenge_09_bank_before_invoice",
        name="Bank Before Invoice",
        difficulty=2,
        tags=("temporal", "arrival-order", "invariance"),
        initial_scenario=sc,
        steps=steps,
        final_expected_truth=final_truth,
    )


# ======================================================================
# Challenge 10: Invoice Before Bank
# ======================================================================


def _build_challenge_10() -> TemporalChallengeDefinition:
    ctx = BookkeepingContext(
        company_id="challenge-10",
        period_start=date(2026, 1, 1),
        period_end=date(2026, 1, 31),
        base_currency="MAD",
        policy=AccountingPolicy(
            chart_of_accounts_id="pcge",
            reconciliation_date_window_days=45,
            require_exact_currency_match=True,
            allow_partial_book_reconciliation=True,
            allow_partial_bank_reconciliation=False,
        ),
    )
    acc = BankAccount(id="acc-main", name="BMCE MAD", currency="MAD")

    book_alpha = BookItem(
        id="book-alpha",
        origin_period="2026-01",
        date=DEFAULT_DATE,
        amount_units="75000",
        direction=Direction.BOOK_BANK_DEBIT,
        currency="MAD",
        description="Facture Client Alpha INV-75",
        reference="INV-75",
    )

    sc = ScenarioDefinition(
        scenario_id="challenge_10_invoice_before_bank",
        name="Invoice Before Bank",
        description=(
            "Invoice arrives first without matching bank item. "
            "Bank transaction arrives later. System reconciles fully in Session 2. "
            "Final economic truth must strictly match Challenge 09."
        ),
        context=ctx,
        bank_accounts=(acc,),
        bank_items=(),
        book_items=(book_alpha,),
        expected_truth=ExpectedTruth(),
    )

    bank_alpha = BankItem(
        id="bank-alpha",
        bank_account_id="acc-main",
        date=DEFAULT_DATE,
        amount_units="75000",
        direction=Direction.BANK_INFLOW,
        currency="MAD",
        description="Virement Client Alpha INV-75",
        reference="INV-75",
    )

    steps = (
        TemporalStep(
            step_id="step-1-invoice-unresolved",
            operation=RunSessionOp(),
            expected_checkpoint=ExpectedTruth(
                expected_pairwise_allocations={},
                expected_book_remaining={"book-alpha": 75_000},
                expected_unreconciled_book_items=["book-alpha"],
            ),
            description="Session 1: BookItem arrives first, remains unresolved",
        ),
        TemporalStep(
            step_id="step-2-bank-arrives",
            operation=AddBankItemsOp(bank_items=(bank_alpha,)),
            description="BankItem bank-alpha arrives into durable storage",
        ),
        TemporalStep(
            step_id="step-3-reconciles",
            operation=RunSessionOp(),
            expected_checkpoint=ExpectedTruth(
                expected_pairwise_allocations={("bank-alpha", "book-alpha"): 75_000},
                expected_bank_remaining={"bank-alpha": 0},
                expected_book_remaining={"book-alpha": 0},
                expected_fully_reconciled_bank_items=["bank-alpha"],
                expected_fully_reconciled_book_items=["book-alpha"],
            ),
            description="Session 2: Reconciles bank-alpha to book-alpha",
        ),
    )

    final_truth = ExpectedTruth(
        expected_pairwise_allocations={("bank-alpha", "book-alpha"): 75_000},
        expected_bank_remaining={"bank-alpha": 0},
        expected_book_remaining={"book-alpha": 0},
        expected_fully_reconciled_bank_items=["bank-alpha"],
        expected_fully_reconciled_book_items=["book-alpha"],
        expected_unreconciled_bank_items=[],
        expected_unreconciled_book_items=[],
    )

    return TemporalChallengeDefinition(
        challenge_id="challenge_10_invoice_before_bank",
        name="Invoice Before Bank",
        difficulty=2,
        tags=("temporal", "arrival-order", "invariance"),
        initial_scenario=sc,
        steps=steps,
        final_expected_truth=final_truth,
    )


# ======================================================================
# Challenge 11: Duplicate Economic Bank Import
# ======================================================================


def _build_challenge_11() -> TemporalChallengeDefinition:
    ctx = BookkeepingContext(
        company_id="challenge-11",
        period_start=date(2026, 1, 1),
        period_end=date(2026, 1, 31),
        base_currency="MAD",
        policy=AccountingPolicy(
            chart_of_accounts_id="pcge",
            reconciliation_date_window_days=45,
            require_exact_currency_match=True,
            allow_partial_book_reconciliation=True,
            allow_partial_bank_reconciliation=False,
        ),
    )
    acc = BankAccount(id="acc-main", name="BMCE MAD", currency="MAD")

    j_item = BookItem(
        id="book-alpha",
        origin_period="2026-01",
        date=DEFAULT_DATE,
        amount_units="50000",
        direction=Direction.BOOK_BANK_DEBIT,
        currency="MAD",
        description="Facture INV-500",
        reference="INV-500",
    )
    b_item_a = BankItem(
        id="bank-import-a",
        bank_account_id="acc-main",
        date=DEFAULT_DATE,
        amount_units="50000",
        direction=Direction.BANK_INFLOW,
        currency="MAD",
        description="Virement INV-500 Import A",
        reference="INV-500",
    )
    b_item_b = BankItem(
        id="bank-import-b",
        bank_account_id="acc-main",
        date=DEFAULT_DATE,
        amount_units="50000",
        direction=Direction.BANK_INFLOW,
        currency="MAD",
        description="Virement INV-500 Import B",
        reference="INV-500",
    )

    sc = ScenarioDefinition(
        scenario_id="challenge_11_duplicate_economic_bank_import",
        name="Duplicate Economic Bank Import",
        description=(
            "Two identical bank transactions compete for one 50,000 book item. "
            "Capacity limit strictly permits only one bank item to reconcile. "
            "The other remains unresolved."
        ),
        context=ctx,
        bank_accounts=(acc,),
        bank_items=(b_item_a, b_item_b),
        book_items=(j_item,),
        expected_truth=ExpectedTruth(),
    )

    steps = (
        TemporalStep(
            step_id="step-1-session-exclusivity",
            operation=RunSessionOp(),
            description="Run session: exactly one bank item reconciles to book-alpha",
        ),
    )

    # In Phase 4 tie-break, bank-import-a precedes bank-import-b lexicographically
    final_truth = ExpectedTruth(
        expected_pairwise_allocations={("bank-import-a", "book-alpha"): 50_000},
        expected_bank_remaining={"bank-import-a": 0, "bank-import-b": 50_000},
        expected_book_remaining={"book-alpha": 0},
        expected_fully_reconciled_bank_items=["bank-import-a"],
        expected_fully_reconciled_book_items=["book-alpha"],
        expected_unreconciled_bank_items=["bank-import-b"],
        expected_unreconciled_book_items=[],
    )

    return TemporalChallengeDefinition(
        challenge_id="challenge_11_duplicate_economic_bank_import",
        name="Duplicate Economic Bank Import",
        difficulty=3,
        tags=("temporal", "exclusivity", "tie-break", "capacity"),
        initial_scenario=sc,
        steps=steps,
        final_expected_truth=final_truth,
    )


# ======================================================================
# Challenge 12: Invalidate and Rebuild
# ======================================================================


def _build_challenge_12() -> TemporalChallengeDefinition:
    ctx = BookkeepingContext(
        company_id="challenge-12",
        period_start=date(2026, 1, 1),
        period_end=date(2026, 1, 31),
        base_currency="MAD",
        policy=AccountingPolicy(
            chart_of_accounts_id="pcge",
            reconciliation_date_window_days=45,
            require_exact_currency_match=True,
            allow_partial_book_reconciliation=True,
            allow_partial_bank_reconciliation=False,
        ),
    )
    acc = BankAccount(id="acc-main", name="BMCE MAD", currency="MAD")

    b_item = BankItem(
        id="bank-a",
        bank_account_id="acc-main",
        date=DEFAULT_DATE,
        amount_units="60000",
        direction=Direction.BANK_INFLOW,
        currency="MAD",
        description="Virement Client 60 INV-60",
        reference="INV-60",
    )
    j_item = BookItem(
        id="book-a",
        origin_period="2026-01",
        date=DEFAULT_DATE,
        amount_units="60000",
        direction=Direction.BOOK_BANK_DEBIT,
        currency="MAD",
        description="Facture Client 60 INV-60",
        reference="INV-60",
    )

    sc = ScenarioDefinition(
        scenario_id="challenge_12_invalidate_and_rebuild",
        name="Invalidate and Rebuild",
        description=(
            "Session 1 reconciles bank-a to book-a. "
            "Reconciliation is explicitly invalidated, restoring full capacity. "
            "Session 2 rebuilds the reconciliation with fresh hypothesis provenance."
        ),
        context=ctx,
        bank_accounts=(acc,),
        bank_items=(b_item,),
        book_items=(j_item,),
        expected_truth=ExpectedTruth(),
    )

    steps = (
        TemporalStep(
            step_id="step-1-initial-reconciliation",
            operation=RunSessionOp(),
            expected_checkpoint=ExpectedTruth(
                expected_pairwise_allocations={("bank-a", "book-a"): 60_000},
                expected_bank_remaining={"bank-a": 0},
                expected_book_remaining={"book-a": 0},
            ),
            description="Session 1: Reconcile bank-a to book-a",
        ),
        TemporalStep(
            step_id="step-2-invalidate-reconciliation",
            operation=InvalidateReconciliationOp(
                bank_item_id="bank-a",
                reason="Auditor requested invalidation",
            ),
            expected_checkpoint=ExpectedTruth(
                expected_pairwise_allocations={},
                expected_bank_remaining={"bank-a": 60_000},
                expected_book_remaining={"book-a": 60_000},
                expected_unreconciled_bank_items=["bank-a"],
                expected_unreconciled_book_items=["book-a"],
            ),
            description="Invalidate initial reconciliation, releasing capacity",
        ),
        TemporalStep(
            step_id="step-3-rebuild-reconciliation",
            operation=RunSessionOp(),
            expected_checkpoint=ExpectedTruth(
                expected_pairwise_allocations={("bank-a", "book-a"): 60_000},
                expected_bank_remaining={"bank-a": 0},
                expected_book_remaining={"book-a": 0},
                expected_fully_reconciled_bank_items=["bank-a"],
                expected_fully_reconciled_book_items=["book-a"],
            ),
            description="Session 2: Rebuilds new active reconciliation with fresh ID & provenance",
        ),
    )

    final_truth = ExpectedTruth(
        expected_pairwise_allocations={("bank-a", "book-a"): 60_000},
        expected_bank_remaining={"bank-a": 0},
        expected_book_remaining={"book-a": 0},
        expected_fully_reconciled_bank_items=["bank-a"],
        expected_fully_reconciled_book_items=["book-a"],
        expected_unreconciled_bank_items=[],
        expected_unreconciled_book_items=[],
    )

    return TemporalChallengeDefinition(
        challenge_id="challenge_12_invalidate_and_rebuild",
        name="Invalidate and Rebuild",
        difficulty=4,
        tags=("temporal", "invalidation", "capacity-release", "rebuild"),
        initial_scenario=sc,
        steps=steps,
        final_expected_truth=final_truth,
    )


# ======================================================================
# Challenge 13: Correction Migrates Economic Truth
# ======================================================================


def _build_challenge_13() -> TemporalChallengeDefinition:
    ctx = BookkeepingContext(
        company_id="challenge-13",
        period_start=date(2026, 1, 1),
        period_end=date(2026, 1, 31),
        base_currency="MAD",
        policy=AccountingPolicy(
            chart_of_accounts_id="pcge",
            reconciliation_date_window_days=45,
            require_exact_currency_match=True,
            allow_partial_book_reconciliation=True,
            allow_partial_bank_reconciliation=False,
        ),
    )
    acc = BankAccount(id="acc-main", name="BMCE MAD", currency="MAD")

    b_item = BankItem(
        id="bank",
        bank_account_id="acc-main",
        date=DEFAULT_DATE,
        amount_units="100000",
        direction=Direction.BANK_INFLOW,
        currency="MAD",
        description="Virement INV-100",
        reference="INV-100",
    )
    # book-alpha initial matching reference INV-100
    j_alpha = BookItem(
        id="book-alpha",
        origin_period="2026-01",
        date=DEFAULT_DATE,
        amount_units="100000",
        direction=Direction.BOOK_BANK_DEBIT,
        currency="MAD",
        description="Facture INV-100",
        reference="INV-100",
    )
    # book-beta initial reference INV-200
    j_beta = BookItem(
        id="book-beta",
        origin_period="2026-01",
        date=DEFAULT_DATE,
        amount_units="100000",
        direction=Direction.BOOK_BANK_DEBIT,
        currency="MAD",
        description="Facture INV-200",
        reference="INV-200",
        counterparty_id=None,
    )

    sc = ScenarioDefinition(
        scenario_id="challenge_13_correction_migrates_economic_truth",
        name="Correction Migrates Economic Truth",
        description=(
            "Session 1 reconciles bank to book-alpha (matching reference INV-100). "
            "Alpha reconciliation is invalidated. "
            "Durable evidence assertions arrive correcting book-alpha reference to INV-300 and book-beta reference to INV-100. "
            "Session 2 reallocates bank to book-beta. "
            "Historical Alpha reconciliation remains inactive without resurrection."
        ),
        context=ctx,
        bank_accounts=(acc,),
        bank_items=(b_item,),
        book_items=(j_alpha, j_beta),
        expected_truth=ExpectedTruth(),
    )

    steps = (
        TemporalStep(
            step_id="step-1-alpha-reconciled",
            operation=RunSessionOp(),
            expected_checkpoint=ExpectedTruth(
                expected_pairwise_allocations={("bank", "book-alpha"): 100_000},
                expected_bank_remaining={"bank": 0},
                expected_book_remaining={"book-alpha": 0, "book-beta": 100_000},
                expected_fully_reconciled_bank_items=["bank"],
                expected_fully_reconciled_book_items=["book-alpha"],
                expected_unreconciled_book_items=["book-beta"],
            ),
            description="Session 1: Initial reference matches book-alpha",
        ),
        TemporalStep(
            step_id="step-2-invalidate-alpha",
            operation=InvalidateReconciliationOp(
                bank_item_id="bank",
                reason="Alpha allocation was incorrect",
            ),
            expected_checkpoint=ExpectedTruth(
                expected_pairwise_allocations={},
                expected_bank_remaining={"bank": 100_000},
                expected_book_remaining={"book-alpha": 100_000, "book-beta": 100_000},
                expected_unreconciled_bank_items=["bank"],
                expected_unreconciled_book_items=["book-alpha", "book-beta"],
            ),
            description="Invalidate Alpha reconciliation",
        ),
        TemporalStep(
            step_id="step-3-new-evidence-assertions",
            operation=AddBookItemEvidenceOp(
                assertions=(
                    BookItemEvidenceAssertion(
                        id="ev-alpha-ref-correction",
                        book_item_id="book-alpha",
                        evidence_type=BookItemEvidenceType.REFERENCE,
                        value="INV-300",
                        source=EvidenceSource.MANUAL,
                        session_id="temporal-seed",
                        state_revision_at_creation=1,
                        created_at=DEFAULT_CLOCK,
                    ),
                    BookItemEvidenceAssertion(
                        id="ev-beta-ref-correction",
                        book_item_id="book-beta",
                        evidence_type=BookItemEvidenceType.REFERENCE,
                        value="INV-100",
                        source=EvidenceSource.MANUAL,
                        session_id="temporal-seed",
                        state_revision_at_creation=1,
                        created_at=DEFAULT_CLOCK,
                    ),
                )
            ),
            description="Correct book-alpha reference to INV-300 and book-beta reference to INV-100 via assertions",
        ),
        TemporalStep(
            step_id="step-4-beta-reconciled",
            operation=RunSessionOp(),
            expected_checkpoint=ExpectedTruth(
                expected_pairwise_allocations={("bank", "book-beta"): 100_000},
                expected_bank_remaining={"bank": 0},
                expected_book_remaining={"book-alpha": 100_000, "book-beta": 0},
                expected_fully_reconciled_bank_items=["bank"],
                expected_fully_reconciled_book_items=["book-beta"],
                expected_unreconciled_book_items=["book-alpha"],
            ),
            description="Session 2: Beta selected following corrected evidence reference INV-100",
        ),
    )

    final_truth = ExpectedTruth(
        expected_pairwise_allocations={("bank", "book-beta"): 100_000},
        expected_bank_remaining={"bank": 0},
        expected_book_remaining={"book-alpha": 100_000, "book-beta": 0},
        expected_fully_reconciled_bank_items=["bank"],
        expected_fully_reconciled_book_items=["book-beta"],
        expected_unreconciled_book_items=["book-alpha"],
        expected_unreconciled_bank_items=[],
    )

    return TemporalChallengeDefinition(
        challenge_id="challenge_13_correction_migrates_economic_truth",
        name="Correction Migrates Economic Truth",
        difficulty=5,
        tags=("temporal", "correction", "semantic-priority", "non-resurrection"),
        initial_scenario=sc,
        steps=steps,
        final_expected_truth=final_truth,
    )


# ======================================================================
# Challenge 14: Dirty Multi-Session Month
# ======================================================================


def _build_challenge_14(policy_mode: str = "conservative") -> TemporalChallengeDefinition:
    is_conservative = policy_mode == "conservative"
    ctx = BookkeepingContext(
        company_id=f"challenge-14-{policy_mode}",
        period_start=date(2026, 1, 1),
        period_end=date(2026, 1, 31),
        base_currency="MAD",
        policy=AccountingPolicy(
            chart_of_accounts_id="pcge",
            reconciliation_date_window_days=45,
            require_exact_currency_match=True,
            allow_partial_book_reconciliation=True,
            allow_partial_bank_reconciliation=False,
            auto_reconcile_unique_inferred_allocation=not is_conservative,
        ),
    )
    acc_main = BankAccount(id="acc-main", name="BMCE MAD", currency="MAD")
    acc_eur = BankAccount(id="acc-eur", name="BMCE EUR", currency="EUR")

    # Initial Book Items (Session 1)
    j_salary = BookItem(
        id="book-salary",
        origin_period="2026-01",
        date=date(2026, 1, 5),
        amount_units="40000",
        direction=Direction.BOOK_BANK_CREDIT,
        currency="MAD",
        description="Salaires Janvier",
        reference="SAL-JAN",
    )
    j_rent = BookItem(
        id="book-rent",
        origin_period="2026-01",
        date=date(2026, 1, 6),
        amount_units="15000",
        direction=Direction.BOOK_BANK_CREDIT,
        currency="MAD",
        description="Loyer Bureau",
        reference="RENT-JAN",
    )
    j_aws = BookItem(
        id="book-aws",
        origin_period="2026-01",
        date=date(2026, 1, 7),
        amount_units="2500",
        direction=Direction.BOOK_BANK_CREDIT,
        currency="MAD",
        description="Amazon Web Services",
        reference="AWS-001",
    )
    j_cust_large = BookItem(
        id="book-cust-large",
        origin_period="2026-01",
        date=date(2026, 1, 8),
        amount_units="80000",
        direction=Direction.BOOK_BANK_DEBIT,
        currency="MAD",
        description="Facture Client Atlas BATCH-1",
        reference="BATCH-1",
    )
    j_sup_bill1 = BookItem(
        id="book-sup-bill1",
        origin_period="2026-01",
        date=date(2026, 1, 10),
        amount_units="20000",
        direction=Direction.BOOK_BANK_CREDIT,
        currency="MAD",
        description="Fournisseur Delta Facture 1",
        reference="DELTA-1",
    )
    j_sup_bill2 = BookItem(
        id="book-sup-bill2",
        origin_period="2026-01",
        date=date(2026, 1, 10),
        amount_units="30000",
        direction=Direction.BOOK_BANK_CREDIT,
        currency="MAD",
        description="Fournisseur Delta Facture 2",
        reference="DELTA-2",
    )

    # Initial Bank Items (Session 1)
    b_salary = BankItem(
        id="bank-salary",
        bank_account_id="acc-main",
        date=date(2026, 1, 5),
        amount_units="40000",
        direction=Direction.BANK_OUTFLOW,
        currency="MAD",
        description="VRT Salaires Janvier",
        reference="SAL-JAN",
    )
    b_rent = BankItem(
        id="bank-rent",
        bank_account_id="acc-main",
        date=date(2026, 1, 6),
        amount_units="15000",
        direction=Direction.BANK_OUTFLOW,
        currency="MAD",
        description="PRLV Loyer Janvier",
        reference="RENT-JAN",
    )
    b_aws = BankItem(
        id="bank-aws",
        bank_account_id="acc-main",
        date=date(2026, 1, 7),
        amount_units="2500",
        direction=Direction.BANK_OUTFLOW,
        currency="MAD",
        description="PRLV Amazon AWS",
        reference="AWS-001",
    )
    b_cust_part1 = BankItem(
        id="bank-cust-part1",
        bank_account_id="acc-main",
        date=date(2026, 1, 8),
        amount_units="50000",
        direction=Direction.BANK_INFLOW,
        currency="MAD",
        description="VRT Client Atlas Acompte BATCH-1",
        reference="BATCH-1",
    )

    sc = ScenarioDefinition(
        scenario_id=f"challenge_14_dirty_multi_session_month_{policy_mode}",
        name=f"Dirty Multi-Session Month ({policy_mode.capitalize()})",
        description=(
            "Complete 6-session trajectory encompassing salary, rent, cloud, supplier split, "
            "customer partial & residual completion, multi-currency (EUR/MAD), duplicate-amount ties, "
            "unmatched items, invalidation/re-correction, and historical audit invariants."
        ),
        context=ctx,
        bank_accounts=(acc_main, acc_eur),
        bank_items=(b_salary, b_rent, b_aws, b_cust_part1),
        book_items=(j_salary, j_rent, j_aws, j_cust_large, j_sup_bill1, j_sup_bill2),
        expected_truth=ExpectedTruth(),
    )

    # Future arrivals for later steps
    b_sup_pay = BankItem(
        id="bank-sup-split-pay",
        bank_account_id="acc-main",
        date=date(2026, 1, 12),
        amount_units="50000",
        direction=Direction.BANK_OUTFLOW,
        currency="MAD",
        description="VRT Fournisseur Delta BATCH-DELTA",
        reference="BATCH-DELTA",
    )
    b_unmatched = BankItem(
        id="bank-mystery",
        bank_account_id="acc-main",
        date=date(2026, 1, 14),
        amount_units="7200",
        direction=Direction.BANK_INFLOW,
        currency="MAD",
        description="VIREMENT MYSTERE SANS REF",
        reference=None,
    )
    b_eur = BankItem(
        id="bank-eur-export",
        bank_account_id="acc-eur",
        date=date(2026, 1, 15),
        amount_units="12000",
        direction=Direction.BANK_INFLOW,
        currency="EUR",
        description="VRT Export Client Paris EUR-99",
        reference="EUR-99",
    )
    b_cust_part2 = BankItem(
        id="bank-cust-part2",
        bank_account_id="acc-main",
        date=date(2026, 1, 18),
        amount_units="30000",
        direction=Direction.BANK_INFLOW,
        currency="MAD",
        description="VRT Client Atlas Solde BATCH-1",
        reference="BATCH-1",
    )
    j_eur = BookItem(
        id="book-eur-export",
        origin_period="2026-01",
        date=date(2026, 1, 15),
        amount_units="12000",
        direction=Direction.BOOK_BANK_DEBIT,
        currency="EUR",
        description="Facture Export Paris EUR-99",
        reference="EUR-99",
    )
    j_unmatched = BookItem(
        id="book-mystery",
        origin_period="2026-01",
        date=date(2026, 1, 20),
        amount_units="18500",
        direction=Direction.BOOK_BANK_CREDIT,
        currency="MAD",
        description="Ecriture Diverse Non Rapprochée",
        reference=None,
    )

    steps = (
        # Step 1: Session 1
        TemporalStep(
            step_id="step-1-session-1-initial-matches",
            operation=RunSessionOp(),
            expected_checkpoint=ExpectedTruth(
                expected_pairwise_allocations={
                    ("bank-salary", "book-salary"): 40_000,
                    ("bank-rent", "book-rent"): 15_000,
                    ("bank-aws", "book-aws"): 2_500,
                    ("bank-cust-part1", "book-cust-large"): 50_000,
                },
                expected_bank_remaining={
                    "bank-salary": 0,
                    "bank-rent": 0,
                    "bank-aws": 0,
                    "bank-cust-part1": 0,
                },
                expected_book_remaining={
                    "book-salary": 0,
                    "book-rent": 0,
                    "book-aws": 0,
                    "book-cust-large": 30_000,
                    "book-sup-bill1": 20_000,
                    "book-sup-bill2": 30_000,
                },
            ),
            description="Session 1: Initial reconciliations (salary, rent, aws, partial customer 50k)",
        ),
        # Step 2: Session 2 Bank arrivals
        TemporalStep(
            step_id="step-2-add-bank-items",
            operation=AddBankItemsOp(
                bank_items=(b_sup_pay, b_unmatched, b_eur)
            ),
            description="Step 2: Add bank-sup-split-pay (50k), bank-mystery (7.2k), bank-eur (12k EUR)",
        ),
        TemporalStep(
            step_id="step-3-session-2-grouped-supplier",
            operation=RunSessionOp(),
            description="Session 2: Process supplier payment & hold mystery bank",
        ),
        # Step 4: Step 3 Book arrivals (EUR book, residual completion bank, unmatched book)
        TemporalStep(
            step_id="step-4-add-book-items-and-residual-bank",
            operation=AddBookItemsOp(book_items=(j_eur, j_unmatched)),
            description="Step 4: Add book-eur-export and book-mystery",
        ),
        TemporalStep(
            step_id="step-5-add-residual-customer-bank",
            operation=AddBankItemsOp(bank_items=(b_cust_part2,)),
            description="Step 5: Add bank-cust-part2 (30k) completing 80k customer invoice",
        ),
        TemporalStep(
            step_id="step-6-session-3-residual-and-eur",
            operation=RunSessionOp(),
            description="Session 3: Completes customer invoice (30k) and reconciles EUR export",
        ),
        # Step 7: Session 4 Invalidate AWS reconciliation
        TemporalStep(
            step_id="step-7-invalidate-aws",
            operation=InvalidateReconciliationOp(
                bank_item_id="bank-aws",
                reason="AWS receipt disputed",
            ),
            description="Step 7: Invalidate AWS reconciliation",
        ),
        # Step 8: Session 5 Re-reconcile AWS after clarification
        TemporalStep(
            step_id="step-8-session-5-rebuild-aws",
            operation=RunSessionOp(),
            description="Session 5: Reconcile AWS anew",
        ),
        # Step 9: Final month-end verification
        TemporalStep(
            step_id="step-9-session-6-final-convergence",
            operation=RunSessionOp(),
            description="Session 6: Final month-end convergence",
        ),
    )

    if is_conservative:
        final_truth = ExpectedTruth(
            expected_pairwise_allocations={
                ("bank-salary", "book-salary"): 40_000,
                ("bank-rent", "book-rent"): 15_000,
                ("bank-aws", "book-aws"): 2_500,
                ("bank-cust-part1", "book-cust-large"): 50_000,
                ("bank-cust-part2", "book-cust-large"): 30_000,
                ("bank-eur-export", "book-eur-export"): 12_000,
            },
            expected_bank_remaining={
                "bank-salary": 0,
                "bank-rent": 0,
                "bank-aws": 0,
                "bank-cust-part1": 0,
                "bank-cust-part2": 0,
                "bank-eur-export": 0,
                "bank-mystery": 7_200,
                "bank-sup-split-pay": 50_000,
            },
            expected_book_remaining={
                "book-salary": 0,
                "book-rent": 0,
                "book-aws": 0,
                "book-cust-large": 0,
                "book-eur-export": 0,
                "book-sup-bill1": 20_000,
                "book-sup-bill2": 30_000,
                "book-mystery": 18_500,
            },
            expected_fully_reconciled_bank_items=[
                "bank-salary",
                "bank-rent",
                "bank-aws",
                "bank-cust-part1",
                "bank-cust-part2",
                "bank-eur-export",
            ],
            expected_fully_reconciled_book_items=[
                "book-salary",
                "book-rent",
                "book-aws",
                "book-cust-large",
                "book-eur-export",
            ],
            expected_unreconciled_bank_items=[
                "bank-mystery",
                "bank-sup-split-pay",
            ],
            expected_unreconciled_book_items=[
                "book-mystery",
                "book-sup-bill1",
                "book-sup-bill2",
            ],
        )
    else:
        final_truth = ExpectedTruth(
            expected_pairwise_allocations={
                ("bank-salary", "book-salary"): 40_000,
                ("bank-rent", "book-rent"): 15_000,
                ("bank-aws", "book-aws"): 2_500,
                ("bank-cust-part1", "book-cust-large"): 50_000,
                ("bank-cust-part2", "book-cust-large"): 30_000,
                ("bank-eur-export", "book-eur-export"): 12_000,
                ("bank-sup-split-pay", "book-sup-bill1"): 20_000,
                ("bank-sup-split-pay", "book-sup-bill2"): 30_000,
            },
            expected_bank_remaining={
                "bank-salary": 0,
                "bank-rent": 0,
                "bank-aws": 0,
                "bank-cust-part1": 0,
                "bank-cust-part2": 0,
                "bank-eur-export": 0,
                "bank-mystery": 7_200,
                "bank-sup-split-pay": 0,
            },
            expected_book_remaining={
                "book-salary": 0,
                "book-rent": 0,
                "book-aws": 0,
                "book-cust-large": 0,
                "book-eur-export": 0,
                "book-sup-bill1": 0,
                "book-sup-bill2": 0,
                "book-mystery": 18_500,
            },
            expected_fully_reconciled_bank_items=[
                "bank-salary",
                "bank-rent",
                "bank-aws",
                "bank-cust-part1",
                "bank-cust-part2",
                "bank-eur-export",
                "bank-sup-split-pay",
            ],
            expected_fully_reconciled_book_items=[
                "book-salary",
                "book-rent",
                "book-aws",
                "book-cust-large",
                "book-eur-export",
                "book-sup-bill1",
                "book-sup-bill2",
            ],
            expected_unreconciled_bank_items=["bank-mystery"],
            expected_unreconciled_book_items=["book-mystery"],
        )

    cid = f"challenge_14_{policy_mode}"
    name = f"Dirty Multi-Session Month ({policy_mode.capitalize()})"

    return TemporalChallengeDefinition(
        challenge_id=cid,
        name=name,
        difficulty=7,
        tags=("temporal", "month-end", "multi-session", "convergence", "multi-currency"),
        initial_scenario=sc,
        steps=steps,
        final_expected_truth=final_truth,
    )


# ======================================================================
# Registry
# ======================================================================

c14_conservative = _build_challenge_14(policy_mode="conservative")
c14_aggressive = _build_challenge_14(policy_mode="aggressive")

TEMPORAL_CHALLENGES_MAP: dict[str, TemporalChallengeDefinition] = {
    c.challenge_id: c
    for c in (
        _build_challenge_08(),
        _build_challenge_09(),
        _build_challenge_10(),
        _build_challenge_11(),
        _build_challenge_12(),
        _build_challenge_13(),
        c14_conservative,
        c14_aggressive,
    )
}

TEMPORAL_CHALLENGES_CATALOG: tuple[TemporalChallengeDefinition, ...] = tuple(
    TEMPORAL_CHALLENGES_MAP.values()
)

# Aliases for backwards compatibility (not included in catalog or listing)
TEMPORAL_CHALLENGE_ALIASES: dict[str, str] = {
    "challenge_14_dirty_multi_session_month": "challenge_14_conservative",
}


def list_temporal_challenges() -> tuple[TemporalChallengeDefinition, ...]:
    """Return all defined temporal challenges ordered by difficulty."""
    return tuple(sorted(TEMPORAL_CHALLENGES_CATALOG, key=lambda c: c.difficulty))


def get_temporal_challenge(name: str) -> TemporalChallengeDefinition:
    """Retrieve one temporal challenge by challenge_id or legacy alias."""
    canonical_name = TEMPORAL_CHALLENGE_ALIASES.get(name, name)
    try:
        return TEMPORAL_CHALLENGES_MAP[canonical_name]
    except KeyError as exc:
        raise KeyError(
            f"Unknown temporal challenge {name!r}. Available: {sorted(TEMPORAL_CHALLENGES_MAP)}"
        ) from exc
