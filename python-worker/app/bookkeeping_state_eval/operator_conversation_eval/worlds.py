"""
Pristine world construction and independent state snapshotting
for the Bookkeeping Operator Conversational Evaluation Harness.
"""

from __future__ import annotations

import copy
from datetime import date, datetime, timezone
from typing import Any

from bookkeeping_state_eval.domain.bank import BankAccount, BankItem
from bookkeeping_state_eval.domain.books import BookItem
from bookkeeping_state_eval.domain.context import AccountingPolicy, BookkeepingContext
from bookkeeping_state_eval.domain.counterparties import Counterparty, CounterpartyType
from bookkeeping_state_eval.domain.enums import Direction, SourceType
from bookkeeping_state_eval.domain.reconciliations import (
    BankAllocation,
    BookAllocation,
    Reconciliation,
)
from bookkeeping_state_eval.state.queries import BookkeepingQueries
from bookkeeping_state_eval.state.fingerprint import state_fingerprint
from bookkeeping_state_eval.hydration.hydrator import BookkeepingHydrator
from bookkeeping_state_eval.operator.workbench import (
    BookkeepingWorkbench,
    create_demo_repository,
)
from bookkeeping_state_eval.persistence.in_memory import (
    InMemoryBookkeepingRepository,
)
from bookkeeping_state_eval.persistence.repository import (
    BookkeepingSnapshot,
    PersistenceWriteSet,
)
from bookkeeping_state_eval.reconciliation.service import ReconciliationService
from bookkeeping_state_eval.scenarios.catalog import SCENARIOS_MAP
from bookkeeping_state_eval.scenarios.temporal import TEMPORAL_CHALLENGES_MAP

from .models import StateSnapshot


def capture_state_snapshot(workbench: BookkeepingWorkbench) -> StateSnapshot:
    """
    Independently rehydrates company state from durable persistence to capture
    an immutable, unpolluted StateSnapshot without relying on workbench memory.
    """
    hydrator = BookkeepingHydrator(repository=workbench.repository)
    state = hydrator.hydrate(
        company_id=workbench.company_id,
        session_id="eval-independent-snapshot",
    )
    queries = BookkeepingQueries(state)

    remaining_bank: dict[str, int] = {}
    for b_id in state.bank_items:
        remaining_bank[b_id] = queries.bank_remaining_units(b_id)

    remaining_book: dict[str, int] = {}
    for j_id in state.book_items:
        remaining_book[j_id] = queries.book_remaining_units(j_id)

    unresolved_bank = tuple(b_id for b_id, rem in remaining_bank.items() if rem > 0)
    unresolved_book = tuple(j_id for j_id, rem in remaining_book.items() if rem > 0)

    policy_dict = copy.deepcopy(getattr(state.context.policy, "__dict__", {}))

    # Safely gather hold and issue counts from recent session if available
    holds_count = (
        len(workbench.last_result.dag_stage_result.holds)
        if workbench.last_result and workbench.last_result.dag_stage_result
        else 0
    )
    provider_issues_count = (
        len(workbench.last_result.reconciliation_stage_result.provider_issues)
        if workbench.last_result and workbench.last_result.reconciliation_stage_result
        else 0
    )

    active_recons = queries.derived.active_reconciliations
    snapshot = StateSnapshot(
        persistence_revision=state.persistence_revision,
        state_fingerprint=state_fingerprint(state),
        policy=policy_dict,
        bank_items_count=len(state.bank_items),
        book_items_count=len(state.book_items),
        unresolved_bank_ids=unresolved_bank,
        unresolved_book_ids=unresolved_book,
        reconciliations_count=len(active_recons),
        reconciliation_ids=tuple(active_recons.keys()),
        routes_count=len(state.routing_decisions),
        classifications_count=len(state.classifications),
        evidence_count=len(state.book_item_evidence_assertions),
        holds_count=holds_count,
        provider_issues_count=provider_issues_count,
        remaining_bank_units=remaining_bank,
        remaining_book_units=remaining_book,
        total_unreconciled_bank_units=sum(remaining_bank.values()),
        total_unreconciled_book_units=sum(remaining_book.values()),
    )
    state.close()
    return snapshot


# -----------------------------------------------------------------------------
# Canonical World Builders
# -----------------------------------------------------------------------------


def _resolve_provider(mode: str) -> str:
    return "llm" if mode == "full" else "deterministic"


def build_c01_world(mode: str) -> BookkeepingWorkbench:
    """
    C01: Vague inspection world with an unambiguous Delta entity and items.
    """
    company_id = "company-c01"
    bank_acc = BankAccount(id="acc-main", name="Main Operations", currency="MAD")
    cp_delta = Counterparty(
        id="cp-delta",
        name="Delta Services",
        counterparty_type=CounterpartyType.CUSTOMER,
        aliases=("Delta Maroc",),
    )
    b_item = BankItem(
        id="bank-delta-01",
        bank_account_id="acc-main",
        date=date(2026, 9, 1),
        amount_units="250000000",  # 25,000.00 MAD
        direction=Direction.BANK_INFLOW,
        currency="MAD",
        description="VIREMENT RECU DELTA SERVICES",
        reference="VIR-DELTA-901",
    )
    j_item = BookItem(
        id="book-delta-01",
        source_type=SourceType.POSTED_BOOK_ITEM,
        origin_period="2026-08",
        date=date(2026, 8, 28),
        amount_units="250000000",
        direction=Direction.BOOK_BANK_DEBIT,
        currency="MAD",
        description="FACTURE PRESTATION DELTA SERVICES",
        counterparty_id="cp-delta",
        reference="INV-DELTA-101",
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
        bank_items=(b_item,),
        book_items=(j_item,),
        counterparties=(cp_delta,),
    )
    repo = InMemoryBookkeepingRepository(initial_snapshots=[snapshot])
    return BookkeepingWorkbench(
        repository=repo,
        company_id=company_id,
        semantic_provider=_resolve_provider(mode),
    )


def build_c02_world(mode: str) -> BookkeepingWorkbench:
    """
    C02: Incomplete mutation world with registered Beta counterparty.
    """
    company_id = "company-c02"
    bank_acc = BankAccount(id="acc-main", name="Operating Account", currency="MAD")
    cp_beta = Counterparty(
        id="cp-beta",
        name="Beta SARL",
        counterparty_type=CounterpartyType.CUSTOMER,
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
        counterparties=(cp_beta,),
    )
    repo = InMemoryBookkeepingRepository(initial_snapshots=[snapshot])
    return BookkeepingWorkbench(
        repository=repo,
        company_id=company_id,
        semantic_provider=_resolve_provider(mode),
    )


def build_c03_world(mode: str) -> BookkeepingWorkbench:
    """
    C03: Multi-turn mutation completion and correction world.
    """
    return build_c02_world(mode)


def build_c04_world(mode: str) -> BookkeepingWorkbench:
    """
    C04: Pronoun/referent continuity world.
    Runs challenge_14_conservative once so split payment is held as a review candidate.
    """
    wb = BookkeepingWorkbench(semantic_provider=_resolve_provider(mode))
    wb.load_temporal_challenge("challenge_14_conservative")
    wb.run_bookkeeping()
    return wb


def build_c05_world(mode: str) -> BookkeepingWorkbench:
    """
    C05: Hypothetical policy question world.
    Loads challenge_14_conservative and runs once; unique inference is held.
    """
    wb = BookkeepingWorkbench(semantic_provider=_resolve_provider(mode))
    wb.load_temporal_challenge("challenge_14_conservative")
    wb.run_bookkeeping()
    return wb


def build_c06_world(mode: str) -> BookkeepingWorkbench:
    """
    C06: Elliptical action after hypothetical.
    """
    wb = BookkeepingWorkbench(semantic_provider=_resolve_provider(mode))
    wb.load_temporal_challenge("challenge_14_conservative")
    wb.run_bookkeeping()
    return wb


def build_c07_world(mode: str) -> BookkeepingWorkbench:
    """
    C07: Explicit action chain world (Enable it and rerun).
    Starts unrun so that enabling unique inference and rerunning executes
    reconciliations and increments persistence revision.
    """
    wb = BookkeepingWorkbench(semantic_provider=_resolve_provider(mode))
    wb.load_temporal_challenge("challenge_14_conservative")
    return wb


def build_c08_world(mode: str) -> BookkeepingWorkbench:
    """
    C08: Stale-state trap world.
    Has 2 unresolved bank items initially.
    """
    company_id = "company-c08"
    bank_acc = BankAccount(id="acc-main", name="Main Account", currency="MAD")
    b1 = BankItem(
        id="bank-1",
        bank_account_id="acc-main",
        date=date(2026, 9, 1),
        amount_units="100000000",
        direction=Direction.BANK_INFLOW,
        currency="MAD",
        description="PAYMENT 1",
    )
    b2 = BankItem(
        id="bank-2",
        bank_account_id="acc-main",
        date=date(2026, 9, 2),
        amount_units="200000000",
        direction=Direction.BANK_INFLOW,
        currency="MAD",
        description="PAYMENT 2",
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
        bank_items=(b1, b2),
    )
    repo = InMemoryBookkeepingRepository(initial_snapshots=[snapshot])
    return BookkeepingWorkbench(
        repository=repo,
        company_id=company_id,
        semantic_provider=_resolve_provider(mode),
    )


def inject_c08_bank_item(workbench: BookkeepingWorkbench) -> None:
    """
    C08 inter-turn event: injects a new bank item directly into durable persistence,
    bypassing the model's conversational memory. Closes inspection state so next
    tool call hydrates fresh durable truth.
    """
    new_item = BankItem(
        id="bank-3-injected",
        bank_account_id="acc-main",
        date=date(2026, 9, 3),
        amount_units="300000000",
        direction=Direction.BANK_INFLOW,
        currency="MAD",
        description="INJECTED DIRECT FEED PAYMENT",
    )
    snap = workbench.repository.load_snapshot(company_id=workbench.company_id)
    write_set = PersistenceWriteSet(bank_items=(new_item,))
    workbench.repository.commit(
        company_id=workbench.company_id,
        expected_revision=snap.persistence_revision,
        write_set=write_set,
    )
    workbench.close_state()


def build_c09_world(mode: str) -> BookkeepingWorkbench:
    """
    C09: Multi-intent execution world. Starts unprocessed from challenge_14_conservative.
    """
    wb = BookkeepingWorkbench(semantic_provider=_resolve_provider(mode))
    wb.load_temporal_challenge("challenge_14_conservative")
    return wb


def build_c10_world(mode: str) -> BookkeepingWorkbench:
    """
    C10: Negative instruction world. Runs scenario_b so active reconciliation exists.
    """
    wb = BookkeepingWorkbench(semantic_provider=_resolve_provider(mode))
    scenario = SCENARIOS_MAP["scenario_b_one_to_many"]
    wb.load_scenario(scenario)
    wb.run_bookkeeping()
    return wb


def build_c11_world(mode: str) -> BookkeepingWorkbench:
    """
    C11: Explicit invalidation world with active reconciliation.
    """
    wb = BookkeepingWorkbench(semantic_provider=_resolve_provider(mode))
    scenario = SCENARIOS_MAP["scenario_b_one_to_many"]
    wb.load_scenario(scenario)
    wb.run_bookkeeping()
    return wb


def build_c12_world(mode: str) -> BookkeepingWorkbench:
    """
    C12: Unknown entity world. Clean world without any Acme counterparty or items.
    """
    company_id = "company-c12"
    bank_acc = BankAccount(id="acc-main", name="Main Account", currency="MAD")
    cp_atlas = Counterparty(id="cp-atlas", name="Atlas Corp", counterparty_type=CounterpartyType.CUSTOMER)
    b1 = BankItem(
        id="bank-atlas",
        bank_account_id="acc-main",
        date=date(2026, 9, 1),
        amount_units="100000000",
        direction=Direction.BANK_INFLOW,
        currency="MAD",
        description="PAYMENT ATLAS CORP",
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
        bank_items=(b1,),
        counterparties=(cp_atlas,),
    )
    repo = InMemoryBookkeepingRepository(initial_snapshots=[snapshot])
    return BookkeepingWorkbench(
        repository=repo,
        company_id=company_id,
        semantic_provider=_resolve_provider(mode),
    )


def build_c13_world(mode: str) -> BookkeepingWorkbench:
    """
    C13: Ambiguous entity world.
    Contains two distinct Beta counterparties with invoices and active reconciliations:
    - Beta Industries SARL (30,000 MAD, inv-beta-ind, bank-beta-ind, rec-beta-ind)
    - Beta Distribution SARL (40,000 MAD, inv-beta-dist, bank-beta-dist, rec-beta-dist)
    """
    company_id = "company-c13"
    bank_acc = BankAccount(id="acc-main", name="Main Account", currency="MAD")
    cp_beta1 = Counterparty(id="cp-beta-ind", name="Beta Industries SARL", counterparty_type=CounterpartyType.CUSTOMER)
    cp_beta2 = Counterparty(id="cp-beta-dist", name="Beta Distribution SARL", counterparty_type=CounterpartyType.CUSTOMER)

    b1 = BankItem(
        id="bank-beta-ind",
        bank_account_id="acc-main",
        date=date(2026, 9, 2),
        amount_units="300000000",
        direction=Direction.BANK_INFLOW,
        currency="MAD",
        description="VIR RECU CLIENT BETA IND",
    )
    b2 = BankItem(
        id="bank-beta-dist",
        bank_account_id="acc-main",
        date=date(2026, 9, 3),
        amount_units="400000000",
        direction=Direction.BANK_INFLOW,
        currency="MAD",
        description="VIR RECU CLIENT BETA DIST",
    )

    j1 = BookItem(
        id="inv-beta-ind",
        source_type=SourceType.POSTED_BOOK_ITEM,
        origin_period="2026-09",
        date=date(2026, 9, 2),
        amount_units="300000000",
        direction=Direction.BOOK_BANK_DEBIT,
        currency="MAD",
        description="FACTURE BETA INDUSTRIES SARL",
        counterparty_id="cp-beta-ind",
    )
    j2 = BookItem(
        id="inv-beta-dist",
        source_type=SourceType.POSTED_BOOK_ITEM,
        origin_period="2026-09",
        date=date(2026, 9, 3),
        amount_units="400000000",
        direction=Direction.BOOK_BANK_DEBIT,
        currency="MAD",
        description="FACTURE BETA DISTRIBUTION SARL",
        counterparty_id="cp-beta-dist",
    )

    rec1 = Reconciliation(
        id="rec-beta-ind",
        bank_allocations=(BankAllocation(bank_item_id="bank-beta-ind", amount_units="300000000"),),
        book_allocations=(BookAllocation(book_item_id="inv-beta-ind", amount_units="300000000"),),
        state_revision_at_creation=1,
        created_at=datetime(2026, 9, 2, 10, 0, tzinfo=timezone.utc),
    )
    rec2 = Reconciliation(
        id="rec-beta-dist",
        bank_allocations=(BankAllocation(bank_item_id="bank-beta-dist", amount_units="400000000"),),
        book_allocations=(BookAllocation(book_item_id="inv-beta-dist", amount_units="400000000"),),
        state_revision_at_creation=1,
        created_at=datetime(2026, 9, 3, 10, 0, tzinfo=timezone.utc),
    )

    snapshot = BookkeepingSnapshot(
        persistence_revision=2,
        context=BookkeepingContext(
            company_id=company_id,
            period_start=date(2026, 9, 1),
            period_end=date(2026, 9, 30),
            base_currency="MAD",
            policy=AccountingPolicy(chart_of_accounts_id="pcge"),
        ),
        bank_accounts=(bank_acc,),
        bank_items=(b1, b2),
        book_items=(j1, j2),
        counterparties=(cp_beta1, cp_beta2),
        reconciliations=(rec1, rec2),
    )
    repo = InMemoryBookkeepingRepository(initial_snapshots=[snapshot])
    return BookkeepingWorkbench(
        repository=repo,
        company_id=company_id,
        semantic_provider=_resolve_provider(mode),
    )


def build_c14_world(mode: str) -> BookkeepingWorkbench:
    """
    C14: Provider / Runtime failure injection world.
    Configures a ReconciliationService subclass that raises an error during plan().
    """
    wb = BookkeepingWorkbench(semantic_provider=_resolve_provider(mode))
    scenario = SCENARIOS_MAP["scenario_b_one_to_many"]
    wb.load_scenario(scenario)

    class FailingReconciliationService(ReconciliationService):
        def plan(self, *args: Any, **kwargs: Any) -> Any:
            raise RuntimeError("Simulated solver crash: CP-SAT solver OOM / timeout")

    wb.reconciliation_service = FailingReconciliationService()
    return wb


def build_c15_world(mode: str) -> BookkeepingWorkbench:
    """
    C15: Durable explanation after rehydration world.
    Runs scenario_b to commit reconciliations to durable persistence,
    then rehydrates afresh so that ephemeral runtime planning artifacts are dropped.
    """
    wb = BookkeepingWorkbench(semantic_provider=_resolve_provider(mode))
    scenario = SCENARIOS_MAP["scenario_b_one_to_many"]
    wb.load_scenario(scenario)
    wb.run_bookkeeping()

    # Re-hydrate fresh inspection state from durable repository
    wb.last_result = None
    wb.last_recon_plan = None
    wb.last_reconciliation_result = None
    wb.close_state()
    wb.get_or_hydrate_state()
    return wb
