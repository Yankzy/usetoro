"""
BookkeepingState Challenge Worlds.

Falsification experiments designed to stress-test the runtime against edge cases,
dense collisions, residual completions, currency constraints, and adversarial
lexicographic optimizer incentives.

These are NOT regression scenarios and are kept separate from catalog.py.
A challenge returning ExpectedTruth FAIL is a legitimate falsification result.
"""

from __future__ import annotations

from dataclasses import dataclass
from datetime import date, datetime, timezone
from typing import Mapping

from bookkeeping_state_eval.domain.bank import BankAccount, BankItem
from bookkeeping_state_eval.domain.books import BookItem
from bookkeeping_state_eval.domain.context import AccountingPolicy, BookkeepingContext
from bookkeeping_state_eval.domain.enums import Direction
from bookkeeping_state_eval.domain.reconciliations import (
    BankAllocation,
    BookAllocation,
    Reconciliation,
)
from bookkeeping_state_eval.scenarios.expected_truth import (
    ExpectedReconciliation,
    ExpectedTruth,
)
from bookkeeping_state_eval.scenarios.models import ScenarioDefinition

DEFAULT_DATE = date(2026, 1, 15)
DEFAULT_CLOCK = datetime(2026, 1, 31, 12, 0, 0, tzinfo=timezone.utc)


@dataclass(frozen=True, slots=True)
class ChallengeDefinition:
    """
    Metadata-wrapped scenario definition representing one falsification challenge world.
    """

    scenario: ScenarioDefinition
    difficulty: int
    tags: tuple[str, ...]

    @property
    def scenario_id(self) -> str:
        return self.scenario.scenario_id

    @property
    def name(self) -> str:
        return self.scenario.name

    @property
    def description(self) -> str:
        return self.scenario.description


def _default_context(
    company_id: str = "challenge-company",
    currency: str = "MAD",
    **policy_overrides: object,
) -> BookkeepingContext:
    policy_kwargs = {
        "chart_of_accounts_id": "pcge",
        "reconciliation_date_window_days": 45,
        "require_exact_currency_match": True,
        "allow_partial_book_reconciliation": True,
        "allow_partial_bank_reconciliation": False,
    }
    policy_kwargs.update(policy_overrides)
    return BookkeepingContext(
        company_id=company_id,
        period_start=date(2026, 1, 1),
        period_end=date(2026, 1, 31),
        base_currency=currency,
        policy=AccountingPolicy(**policy_kwargs),  # type: ignore[arg-type]
    )


def _bank_item(
    id: str,
    *,
    amount: int,
    bank_account_id: str = "acc-main",
    direction: Direction = Direction.BANK_INFLOW,
    currency: str = "MAD",
    date_val: date = DEFAULT_DATE,
    description: str | None = None,
    reference: str | None = None,
) -> BankItem:
    return BankItem(
        id=id,
        bank_account_id=bank_account_id,
        date=date_val,
        amount_units=str(amount),
        direction=direction,
        currency=currency,
        description=description or id,
        reference=reference,
    )


def _book_item(
    id: str,
    *,
    amount: int,
    direction: Direction = Direction.BOOK_BANK_DEBIT,
    currency: str = "MAD",
    date_val: date = DEFAULT_DATE,
    description: str | None = None,
    reference: str | None = None,
    origin_period: str = "2026-01",
) -> BookItem:
    return BookItem(
        id=id,
        origin_period=origin_period,
        date=date_val,
        amount_units=str(amount),
        direction=direction,
        currency=currency,
        description=description or id,
        reference=reference,
    )


def _reconciliation(
    id: str,
    *,
    bank: tuple[tuple[str, int], ...] = (),
    book: tuple[tuple[str, int], ...] = (),
) -> Reconciliation:
    return Reconciliation(
        id=id,
        bank_allocations=tuple(
            BankAllocation(bank_item_id=b_id, amount_units=str(amt))
            for b_id, amt in bank
        ),
        book_allocations=tuple(
            BookAllocation(book_item_id=j_id, amount_units=str(amt))
            for j_id, amt in book
        ),
        state_revision_at_creation=0,
        created_at=DEFAULT_CLOCK,
    )


# ======================================================================
# Challenge 01: Dense Collision
# ======================================================================
def _build_challenge_01() -> ChallengeDefinition:
    ctx = _default_context(company_id="challenge-01")
    acc = BankAccount(id="acc-main", name="BMCE Operating MAD", currency="MAD")

    # 4 BookItems with identical amounts (10,000 MAD) and close dates
    book_items = (
        _book_item(
            "book-alpha",
            amount=10_000,
            date_val=date(2026, 1, 15),
            direction=Direction.BOOK_BANK_DEBIT,
            description="Client Alpha encaissement REF-ALPHA",
            reference="REF-ALPHA",
        ),
        _book_item(
            "book-beta",
            amount=10_000,
            date_val=date(2026, 1, 15),
            direction=Direction.BOOK_BANK_DEBIT,
            description="Client Beta encaissement REF-BETA",
            reference="REF-BETA",
        ),
        _book_item(
            "book-gamma",
            amount=10_000,
            date_val=date(2026, 1, 16),
            direction=Direction.BOOK_BANK_DEBIT,
            description="Client Gamma encaissement REF-GAMMA",
            reference="REF-GAMMA",
        ),
        _book_item(
            "book-delta",
            amount=10_000,
            date_val=date(2026, 1, 16),
            direction=Direction.BOOK_BANK_DEBIT,
            description="Client Delta encaissement REF-DELTA",
            reference="REF-DELTA",
        ),
    )

    # 4 BankItems with matching amounts, distinct references
    bank_items = (
        _bank_item(
            "bank-alpha",
            amount=10_000,
            date_val=date(2026, 1, 15),
            direction=Direction.BANK_INFLOW,
            description="VIR CLIENT ALPHA REF-ALPHA",
            reference="REF-ALPHA",
        ),
        _bank_item(
            "bank-beta",
            amount=10_000,
            date_val=date(2026, 1, 15),
            direction=Direction.BANK_INFLOW,
            description="VIR CLIENT BETA REF-BETA",
            reference="REF-BETA",
        ),
        _bank_item(
            "bank-gamma",
            amount=10_000,
            date_val=date(2026, 1, 16),
            direction=Direction.BANK_INFLOW,
            description="VIR CLIENT GAMMA REF-GAMMA",
            reference="REF-GAMMA",
        ),
        _bank_item(
            "bank-delta",
            amount=10_000,
            date_val=date(2026, 1, 16),
            direction=Direction.BANK_INFLOW,
            description="VIR CLIENT DELTA REF-DELTA",
            reference="REF-DELTA",
        ),
    )

    expected = ExpectedTruth(
        expected_routes={
            "book-alpha": "acc-main",
            "book-beta": "acc-main",
            "book-gamma": "acc-main",
            "book-delta": "acc-main",
        },
        expected_classifications={
            "book-alpha": "3421",
            "book-beta": "3421",
            "book-gamma": "3421",
            "book-delta": "3421",
        },
        expected_pairwise_allocations={
            ("bank-alpha", "book-alpha"): 10_000,
            ("bank-beta", "book-beta"): 10_000,
            ("bank-gamma", "book-gamma"): 10_000,
            ("bank-delta", "book-delta"): 10_000,
        },
        expected_bank_remaining={
            "bank-alpha": 0,
            "bank-beta": 0,
            "bank-gamma": 0,
            "bank-delta": 0,
        },
        expected_book_remaining={
            "book-alpha": 0,
            "book-beta": 0,
            "book-gamma": 0,
            "book-delta": 0,
        },
        expected_fully_reconciled_bank_items=[
            "bank-alpha",
            "bank-beta",
            "bank-gamma",
            "bank-delta",
        ],
        expected_fully_reconciled_book_items=[
            "book-alpha",
            "book-beta",
            "book-gamma",
            "book-delta",
        ],
        expected_unreconciled_bank_items=[],
        expected_unreconciled_book_items=[],
    )

    sc = ScenarioDefinition(
        scenario_id="challenge_01_dense_collision",
        name="Dense Collision",
        description="Four identical-amount customer receipts with close dates disambiguated by counterparty and reference.",
        context=ctx,
        bank_accounts=(acc,),
        bank_items=bank_items,
        book_items=book_items,
        expected_truth=expected,
    )
    return ChallengeDefinition(
        scenario=sc,
        difficulty=1,
        tags=("baseline", "ambiguity"),
    )


# ======================================================================
# Challenge 02: Residual Completion
# ======================================================================
def _build_challenge_02() -> ChallengeDefinition:
    ctx = _default_context(
        company_id="challenge-02",
        allow_partial_book_reconciliation=True,
    )
    acc = BankAccount(id="acc-main", name="Main Account", currency="MAD")

    # book-main was 100,000 MAD, already partially reconciled for 40,000 MAD
    book_main = _book_item(
        "book-main",
        amount=100_000,
        date_val=date(2026, 1, 10),
        direction=Direction.BOOK_BANK_DEBIT,
        description="Client Atlas invoice INV-100",
        reference="INV-100",
    )
    book_decoy = _book_item(
        "book-decoy",
        amount=60_000,
        date_val=date(2026, 1, 20),
        direction=Direction.BOOK_BANK_DEBIT,
        description="Client Zebra invoice INV-999",
        reference="INV-999",
    )

    bank_old = _bank_item(
        "bank-old",
        amount=40_000,
        date_val=date(2026, 1, 10),
        direction=Direction.BANK_INFLOW,
        description="VIR CLIENT ATLAS ACOMPTE INV-100",
        reference="INV-100",
    )
    bank_new = _bank_item(
        "bank-new",
        amount=60_000,
        date_val=date(2026, 1, 20),
        direction=Direction.BANK_INFLOW,
        description="VIR CLIENT ATLAS SOLDE INV-100",
        reference="INV-100",
    )

    historical_recon = _reconciliation(
        "recon-old",
        bank=(("bank-old", 40_000),),
        book=(("book-main", 40_000),),
    )

    expected = ExpectedTruth(
        expected_pairwise_allocations={
            ("bank-old", "book-main"): 40_000,
            ("bank-new", "book-main"): 60_000,
        },
        expected_bank_remaining={
            "bank-old": 0,
            "bank-new": 0,
        },
        expected_book_remaining={
            "book-main": 0,
            "book-decoy": 60_000,
        },
        expected_fully_reconciled_bank_items=["bank-old", "bank-new"],
        expected_fully_reconciled_book_items=["book-main"],
        expected_unreconciled_book_items=["book-decoy"],
        expected_unreconciled_bank_items=[],
    )

    sc = ScenarioDefinition(
        scenario_id="challenge_02_residual_completion",
        name="Residual Completion",
        description="Pre-existing partial reconciliation where a 60k transaction completes the 100k main item instead of binding to a 60k decoy.",
        context=ctx,
        bank_accounts=(acc,),
        bank_items=(bank_old, bank_new),
        book_items=(book_main, book_decoy),
        reconciliations=(historical_recon,),
        expected_truth=expected,
    )
    return ChallengeDefinition(
        scenario=sc,
        difficulty=2,
        tags=("historical-state", "ambiguity"),
    )


# ======================================================================
# Challenge 03: Account + Currency Minefield
# ======================================================================
def _build_challenge_03() -> ChallengeDefinition:
    ctx = _default_context(
        company_id="challenge-03",
        currency="MAD",
        require_exact_currency_match=True,
    )
    acc_mad_main = BankAccount(id="acc-mad-main", name="Main Account MAD", currency="MAD")
    acc_mad_sec = BankAccount(id="acc-mad-secondary", name="Secondary Account MAD", currency="MAD")
    acc_eur_main = BankAccount(id="acc-eur-main", name="EUR Account", currency="EUR")

    bank_items = (
        _bank_item(
            "bank-eur-1",
            bank_account_id="acc-eur-main",
            amount=5_000,
            currency="EUR",
            direction=Direction.BANK_INFLOW,
            description="VIR CLIENT OMEGA EUR",
            reference="INV-OMEGA",
        ),
        _bank_item(
            "bank-mad-main",
            bank_account_id="acc-mad-main",
            amount=15_000,
            currency="MAD",
            direction=Direction.BANK_OUTFLOW,
            description="LOYER BUREAU SIEGE",
            reference="LOY-MAIN",
        ),
        _bank_item(
            "bank-mad-sec",
            bank_account_id="acc-mad-secondary",
            amount=15_000,
            currency="MAD",
            direction=Direction.BANK_OUTFLOW,
            description="FOURNISSEUR MATERIEL SECONDARY",
            reference="FOUR-SEC",
        ),
    )

    book_items = (
        _book_item(
            "book-eur-1",
            amount=5_000,
            currency="EUR",
            direction=Direction.BOOK_BANK_DEBIT,
            description="Client Omega EUR invoice",
            reference="INV-OMEGA",
        ),
        _book_item(
            "book-mad-decoy",
            amount=5_000,
            currency="MAD",
            direction=Direction.BOOK_BANK_DEBIT,
            description="Client Omega payment in MAD",
            reference="INV-OMEGA",
        ),
        _book_item(
            "book-mad-main",
            amount=15_000,
            currency="MAD",
            direction=Direction.BOOK_BANK_CREDIT,
            description="Loyer bureau siege",
            reference="LOY-MAIN",
        ),
        _book_item(
            "book-mad-sec",
            amount=15_000,
            currency="MAD",
            direction=Direction.BOOK_BANK_CREDIT,
            description="Fournisseur materiel secondary",
            reference="FOUR-SEC",
        ),
    )

    expected = ExpectedTruth(
        expected_routes={
            "book-eur-1": "acc-eur-main",
            "book-mad-main": "acc-mad-main",
            "book-mad-sec": "acc-mad-secondary",
        },
        expected_pairwise_allocations={
            ("bank-eur-1", "book-eur-1"): 5_000,
            ("bank-mad-main", "book-mad-main"): 15_000,
            ("bank-mad-sec", "book-mad-sec"): 15_000,
        },
        expected_bank_remaining={
            "bank-eur-1": 0,
            "bank-mad-main": 0,
            "bank-mad-sec": 0,
        },
        expected_book_remaining={
            "book-eur-1": 0,
            "book-mad-main": 0,
            "book-mad-sec": 0,
            "book-mad-decoy": 5_000,
        },
        expected_fully_reconciled_bank_items=[
            "bank-eur-1",
            "bank-mad-main",
            "bank-mad-sec",
        ],
        expected_fully_reconciled_book_items=[
            "book-eur-1",
            "book-mad-main",
            "book-mad-sec",
        ],
        expected_unreconciled_book_items=["book-mad-decoy"],
        expected_unreconciled_bank_items=[],
    )

    sc = ScenarioDefinition(
        scenario_id="challenge_03_account_currency_minefield",
        name="Account and Currency Minefield",
        description="Multi-account and multi-currency scenario where hard currency feasibility and account routing prevent alluring cross-currency matches.",
        context=ctx,
        bank_accounts=(acc_mad_main, acc_mad_sec, acc_eur_main),
        bank_items=bank_items,
        book_items=book_items,
        expected_truth=expected,
    )
    return ChallengeDefinition(
        scenario=sc,
        difficulty=4,
        tags=("routing", "baseline"),
    )


# ======================================================================
# Challenge 04: Duplicate Reference Collision
# ======================================================================
def _build_challenge_04() -> ChallengeDefinition:
    ctx = _default_context(company_id="challenge-04")
    acc = BankAccount(id="acc-main", name="Main Account", currency="MAD")

    book_items = (
        _book_item(
            "book-alpha",
            amount=25_000,
            direction=Direction.BOOK_BANK_DEBIT,
            description="Client Alpha settlement INV-77",
            reference="INV-77",
        ),
        _book_item(
            "book-beta",
            amount=25_000,
            direction=Direction.BOOK_BANK_DEBIT,
            description="Client Beta settlement INV-77",
            reference="INV-77",
        ),
    )

    bank_items = (
        _bank_item(
            "bank-alpha",
            amount=25_000,
            direction=Direction.BANK_INFLOW,
            description="VIR CLIENT ALPHA INV-77",
            reference="INV-77",
        ),
        _bank_item(
            "bank-beta",
            amount=25_000,
            direction=Direction.BANK_INFLOW,
            description="VIR CLIENT BETA INV-77",
            reference="INV-77",
        ),
    )

    expected = ExpectedTruth(
        expected_pairwise_allocations={
            ("bank-alpha", "book-alpha"): 25_000,
            ("bank-beta", "book-beta"): 25_000,
        },
        expected_bank_remaining={"bank-alpha": 0, "bank-beta": 0},
        expected_book_remaining={"book-alpha": 0, "book-beta": 0},
        expected_fully_reconciled_bank_items=["bank-alpha", "bank-beta"],
        expected_fully_reconciled_book_items=["book-alpha", "book-beta"],
        expected_unreconciled_bank_items=[],
        expected_unreconciled_book_items=[],
    )

    sc = ScenarioDefinition(
        scenario_id="challenge_04_duplicate_reference_collision",
        name="Duplicate Reference Collision",
        description="Identical reference (INV-77) and identical amount (25,000) across two counterparties disambiguated strictly by counterparty semantic evidence.",
        context=ctx,
        bank_accounts=(acc,),
        bank_items=bank_items,
        book_items=book_items,
        expected_truth=expected,
    )
    return ChallengeDefinition(
        scenario=sc,
        difficulty=3,
        tags=("ambiguity", "baseline"),
    )


# ======================================================================
# Challenge 05: Correct Partial vs Wrong Exact
# ======================================================================
def _build_challenge_05() -> ChallengeDefinition:
    ctx = _default_context(
        company_id="challenge-05",
        allow_partial_book_reconciliation=True,
    )
    acc = BankAccount(id="acc-main", name="Main Account", currency="MAD")

    # bank payment of 95k strongly referencing Alpha INV-100
    bank_payment = _bank_item(
        "bank-payment",
        amount=95_000,
        direction=Direction.BANK_INFLOW,
        description="Client Alpha acompte partiel INV-100",
        reference="INV-100",
    )

    # Correct BookItem: 100k Alpha INV-100 (partially cleared)
    book_alpha = _book_item(
        "book-alpha",
        amount=100_000,
        direction=Direction.BOOK_BANK_DEBIT,
        description="Client Alpha facture INV-100",
        reference="INV-100",
    )

    # Decoy BookItem: 95k Beta (exact numerical match, but zero semantic match)
    book_beta = _book_item(
        "book-beta",
        amount=95_000,
        direction=Direction.BOOK_BANK_DEBIT,
        description="Client Beta unrelated invoice INV-999",
        reference="INV-999",
    )

    # Independent accounting truth: payment belongs to Alpha INV-100
    expected = ExpectedTruth(
        expected_pairwise_allocations={
            ("bank-payment", "book-alpha"): 95_000,
        },
        expected_bank_remaining={"bank-payment": 0},
        expected_book_remaining={
            "book-alpha": 5_000,
            "book-beta": 95_000,
        },
        expected_fully_reconciled_bank_items=["bank-payment"],
        expected_partially_reconciled_book_items=["book-alpha"],
        expected_unreconciled_book_items=["book-beta"],
        expected_fully_reconciled_book_items=[],
    )

    sc = ScenarioDefinition(
        scenario_id="challenge_05_partial_truth_vs_exact_decoy",
        name="Correct Partial vs Wrong Exact",
        description="Adversarial challenge testing whether Phase 2 unique items cleared forces selection of an exact-amount decoy over a semantically superior partial settlement.",
        context=ctx,
        bank_accounts=(acc,),
        bank_items=(bank_payment,),
        book_items=(book_alpha, book_beta),
        expected_truth=expected,
    )
    return ChallengeDefinition(
        scenario=sc,
        difficulty=5,
        tags=("optimizer-adversarial",),
    )


# ======================================================================
# Challenge 06: Correct Aggregate vs Exact Decoys
# ======================================================================
def _build_challenge_06() -> ChallengeDefinition:
    ctx = _default_context(company_id="challenge-06")
    acc = BankAccount(id="acc-main", name="Main Account", currency="MAD")

    # Two bank items (40k + 60k) with strong Client Gamma / BATCH-900 evidence
    bank_items = (
        _bank_item(
            "bank-40",
            amount=40_000,
            direction=Direction.BANK_INFLOW,
            description="Client Gamma BATCH-900 split 1",
            reference="BATCH-900",
        ),
        _bank_item(
            "bank-60",
            amount=60_000,
            direction=Direction.BANK_INFLOW,
            description="Client Gamma BATCH-900 split 2",
            reference="BATCH-900",
        ),
    )

    # Correct BookItem: 100k Client Gamma BATCH-900
    book_gamma = _book_item(
        "book-gamma",
        amount=100_000,
        direction=Direction.BOOK_BANK_DEBIT,
        description="Client Gamma invoice BATCH-900",
        reference="BATCH-900",
    )

    # Decoy BookItems: 40k and 60k unrelated items (would clear 4 items instead of 3 in Phase 2)
    book_decoy_40 = _book_item(
        "book-decoy-40",
        amount=40_000,
        direction=Direction.BOOK_BANK_DEBIT,
        description="Client X unrelated invoice",
        reference="OTHER-40",
    )
    book_decoy_60 = _book_item(
        "book-decoy-60",
        amount=60_000,
        direction=Direction.BOOK_BANK_DEBIT,
        description="Client Y unrelated invoice",
        reference="OTHER-60",
    )

    expected = ExpectedTruth(
        expected_pairwise_allocations={
            ("bank-40", "book-gamma"): 40_000,
            ("bank-60", "book-gamma"): 60_000,
        },
        expected_bank_remaining={"bank-40": 0, "bank-60": 0},
        expected_book_remaining={
            "book-gamma": 0,
            "book-decoy-40": 40_000,
            "book-decoy-60": 60_000,
        },
        expected_fully_reconciled_bank_items=["bank-40", "bank-60"],
        expected_fully_reconciled_book_items=["book-gamma"],
        expected_unreconciled_book_items=["book-decoy-40", "book-decoy-60"],
        expected_unreconciled_bank_items=[],
    )

    sc = ScenarioDefinition(
        scenario_id="challenge_06_aggregate_truth_vs_fragment_decoys",
        name="Correct Aggregate vs Exact Decoys",
        description="Adversarial challenge testing whether Phase 2 unique clearance prioritizes clearing four 1:1 items over one 2:1 aggregate with overwhelming semantic evidence.",
        context=ctx,
        bank_accounts=(acc,),
        bank_items=bank_items,
        book_items=(book_gamma, book_decoy_40, book_decoy_60),
        expected_truth=expected,
    )
    return ChallengeDefinition(
        scenario=sc,
        difficulty=6,
        tags=("optimizer-adversarial",),
    )


# ======================================================================
# Challenge 07: Dirty Month-End
# ======================================================================
def _build_challenge_07() -> ChallengeDefinition:
    ctx = _default_context(
        company_id="challenge-07",
        allow_partial_book_reconciliation=True,
    )
    acc_main = BankAccount(id="acc-main", name="Main Account MAD", currency="MAD")
    acc_eur = BankAccount(id="acc-eur", name="Operations EUR", currency="EUR")

    # 12 BookItems
    book_items = (
        # A. Payroll exact (6171)
        _book_item(
            "book-payroll",
            amount=45_000,
            direction=Direction.BOOK_BANK_CREDIT,
            description="Salary January payroll",
            reference="SAL-JAN",
        ),
        # B. Office rent exact (6131)
        _book_item(
            "book-rent",
            amount=12_500,
            direction=Direction.BOOK_BANK_CREDIT,
            description="Office rent January",
            reference="RENT-JAN",
        ),
        # C. AWS / software exact (6181)
        _book_item(
            "book-aws",
            amount=2_400,
            direction=Direction.BOOK_BANK_CREDIT,
            description="AWS Cloud hosting services",
            reference="AWS-JAN",
        ),
        # D. Supplier payment split across 2 bank transactions (4411)
        _book_item(
            "book-supplier",
            amount=30_000,
            direction=Direction.BOOK_BANK_CREDIT,
            description="Supplier Atlas equipment invoice",
            reference="SUP-300",
        ),
        # E. Customer invoice settled by 3 aggregated receipts (3421)
        _book_item(
            "book-client-bulk",
            amount=50_000,
            direction=Direction.BOOK_BANK_DEBIT,
            description="Client Sommet project bill",
            reference="SOM-500",
        ),
        # F. Duplicate-amount pair distinguished by semantic evidence
        _book_item(
            "book-dup-a",
            amount=8_000,
            direction=Direction.BOOK_BANK_DEBIT,
            description="Client Blue project settlement",
            reference="BLUE-1",
        ),
        _book_item(
            "book-dup-b",
            amount=8_000,
            direction=Direction.BOOK_BANK_DEBIT,
            description="Client Red project settlement",
            reference="RED-1",
        ),
        # G. Pre-existing partial reconciliation (8k already cleared, 12k remaining)
        _book_item(
            "book-preexist",
            amount=20_000,
            direction=Direction.BOOK_BANK_DEBIT,
            description="Client Zenith invoice",
            reference="ZEN-200",
        ),
        # H. Unmatched BookItem
        _book_item(
            "book-unmatched",
            amount=14_000,
            direction=Direction.BOOK_BANK_DEBIT,
            description="Client Horizon pending claim",
            reference="HOR-14",
        ),
        # I. Semantically ambiguous BookItem (should HOLD in DAG)
        _book_item(
            "book-ambiguous",
            amount=9_900,
            direction=Direction.BOOK_BANK_CREDIT,
            description="VIREMENT DIVERS 994827",
            reference="VIR-994827",
        ),
        # J. EUR account operation
        _book_item(
            "book-eur-fee",
            amount=500,
            currency="EUR",
            direction=Direction.BOOK_BANK_CREDIT,
            description="Bank maintenance fee EUR",
            reference="EUR-FEE",
        ),
        # K. Currency decoy in MAD
        _book_item(
            "book-mad-decoy",
            amount=500,
            currency="MAD",
            direction=Direction.BOOK_BANK_CREDIT,
            description="Decoy bank maintenance fee in MAD",
            reference="EUR-FEE",
        ),
    )

    # 14 BankItems
    bank_items = (
        _bank_item(
            "bank-payroll",
            amount=45_000,
            direction=Direction.BANK_OUTFLOW,
            description="VRT SALAIRES JANVIER",
            reference="SAL-JAN",
        ),
        _bank_item(
            "bank-rent",
            amount=12_500,
            direction=Direction.BANK_OUTFLOW,
            description="LOYER BUREAU JANVIER",
            reference="RENT-JAN",
        ),
        _bank_item(
            "bank-aws",
            amount=2_400,
            direction=Direction.BANK_OUTFLOW,
            description="PRLV AMAZON WEB SERVICES",
            reference="AWS-JAN",
        ),
        _bank_item(
            "bank-supplier-1",
            amount=10_000,
            direction=Direction.BANK_OUTFLOW,
            description="REGLEMENT FOURNISSEUR ATLAS PART 1",
            reference="SUP-300",
        ),
        _bank_item(
            "bank-supplier-2",
            amount=20_000,
            direction=Direction.BANK_OUTFLOW,
            description="REGLEMENT FOURNISSEUR ATLAS PART 2",
            reference="SUP-300",
        ),
        _bank_item(
            "bank-client-bulk-1",
            amount=15_000,
            direction=Direction.BANK_INFLOW,
            description="VIR SOMMET ACOMPTE 1",
            reference="SOM-500",
        ),
        _bank_item(
            "bank-client-bulk-2",
            amount=20_000,
            direction=Direction.BANK_INFLOW,
            description="VIR SOMMET ACOMPTE 2",
            reference="SOM-500",
        ),
        _bank_item(
            "bank-client-bulk-3",
            amount=15_000,
            direction=Direction.BANK_INFLOW,
            description="VIR SOMMET SOLDE 3",
            reference="SOM-500",
        ),
        _bank_item(
            "bank-dup-a",
            amount=8_000,
            direction=Direction.BANK_INFLOW,
            description="VIR CLIENT BLUE",
            reference="BLUE-1",
        ),
        _bank_item(
            "bank-dup-b",
            amount=8_000,
            direction=Direction.BANK_INFLOW,
            description="VIR CLIENT RED",
            reference="RED-1",
        ),
        _bank_item(
            "bank-preexist-old",
            amount=8_000,
            direction=Direction.BANK_INFLOW,
            description="VIR ZENITH ACOMPTE",
            reference="ZEN-200",
        ),
        _bank_item(
            "bank-preexist-new",
            amount=12_000,
            direction=Direction.BANK_INFLOW,
            description="VIR ZENITH SOLDE",
            reference="ZEN-200",
        ),
        _bank_item(
            "bank-unmatched",
            amount=3_500,
            direction=Direction.BANK_INFLOW,
            description="Depot especes agence non identifie",
            reference="CASH-99",
        ),
        _bank_item(
            "bank-eur-fee",
            bank_account_id="acc-eur",
            amount=500,
            currency="EUR",
            direction=Direction.BANK_OUTFLOW,
            description="FRAIS TENUE COMPTE EUR",
            reference="EUR-FEE",
        ),
    )

    # Historical reconciliation for Zenith
    hist_recon = _reconciliation(
        "recon-preexist-old",
        bank=(("bank-preexist-old", 8_000),),
        book=(("book-preexist", 8_000),),
    )

    expected = ExpectedTruth(
        expected_pairwise_allocations={
            ("bank-payroll", "book-payroll"): 45_000,
            ("bank-rent", "book-rent"): 12_500,
            ("bank-aws", "book-aws"): 2_400,
            ("bank-supplier-1", "book-supplier"): 10_000,
            ("bank-supplier-2", "book-supplier"): 20_000,
            ("bank-client-bulk-1", "book-client-bulk"): 15_000,
            ("bank-client-bulk-2", "book-client-bulk"): 20_000,
            ("bank-client-bulk-3", "book-client-bulk"): 15_000,
            ("bank-dup-a", "book-dup-a"): 8_000,
            ("bank-dup-b", "book-dup-b"): 8_000,
            ("bank-preexist-old", "book-preexist"): 8_000,
            ("bank-preexist-new", "book-preexist"): 12_000,
            ("bank-eur-fee", "book-eur-fee"): 500,
        },
        expected_bank_remaining={
            "bank-payroll": 0,
            "bank-rent": 0,
            "bank-aws": 0,
            "bank-supplier-1": 0,
            "bank-supplier-2": 0,
            "bank-client-bulk-1": 0,
            "bank-client-bulk-2": 0,
            "bank-client-bulk-3": 0,
            "bank-dup-a": 0,
            "bank-dup-b": 0,
            "bank-preexist-old": 0,
            "bank-preexist-new": 0,
            "bank-eur-fee": 0,
            "bank-unmatched": 3_500,
        },
        expected_book_remaining={
            "book-payroll": 0,
            "book-rent": 0,
            "book-aws": 0,
            "book-supplier": 0,
            "book-client-bulk": 0,
            "book-dup-a": 0,
            "book-dup-b": 0,
            "book-preexist": 0,
            "book-eur-fee": 0,
            "book-unmatched": 14_000,
            "book-ambiguous": 9_900,
            "book-mad-decoy": 500,
        },
        expected_fully_reconciled_bank_items=[
            "bank-payroll",
            "bank-rent",
            "bank-aws",
            "bank-supplier-1",
            "bank-supplier-2",
            "bank-client-bulk-1",
            "bank-client-bulk-2",
            "bank-client-bulk-3",
            "bank-dup-a",
            "bank-dup-b",
            "bank-preexist-old",
            "bank-preexist-new",
            "bank-eur-fee",
        ],
        expected_fully_reconciled_book_items=[
            "book-payroll",
            "book-rent",
            "book-aws",
            "book-supplier",
            "book-client-bulk",
            "book-dup-a",
            "book-dup-b",
            "book-preexist",
            "book-eur-fee",
        ],
        expected_unreconciled_bank_items=["bank-unmatched"],
        expected_unreconciled_book_items=[
            "book-unmatched",
            "book-ambiguous",
            "book-mad-decoy",
        ],
    )

    sc = ScenarioDefinition(
        scenario_id="challenge_07_dirty_month_end",
        name="Dirty Month-End",
        description="Realistic month-end company containing 12 book items, 14 bank items, split supplier settlements, 3-way aggregated customer receipts, duplicate-amount pairs, pre-existing partials, unmatched items, ambiguous HOLD, and currency boundaries.",
        context=ctx,
        bank_accounts=(acc_main, acc_eur),
        bank_items=bank_items,
        book_items=book_items,
        reconciliations=(hist_recon,),
        expected_truth=expected,
    )
    return ChallengeDefinition(
        scenario=sc,
        difficulty=7,
        tags=("month-end", "ambiguity", "historical-state", "routing"),
    )


# ======================================================================
# Challenge Registry
# ======================================================================

CHALLENGES_MAP: dict[str, ChallengeDefinition] = {
    c.scenario_id: c
    for c in (
        _build_challenge_01(),
        _build_challenge_02(),
        _build_challenge_03(),
        _build_challenge_04(),
        _build_challenge_05(),
        _build_challenge_06(),
        _build_challenge_07(),
    )
}

CHALLENGES_CATALOG: tuple[ChallengeDefinition, ...] = tuple(
    CHALLENGES_MAP.values()
)


def list_challenges() -> tuple[ChallengeDefinition, ...]:
    """Return all defined challenge worlds ordered by difficulty."""
    return tuple(sorted(CHALLENGES_CATALOG, key=lambda c: c.difficulty))


def get_challenge(name: str) -> ChallengeDefinition:
    """Retrieve one challenge world by its scenario_id."""
    try:
        return CHALLENGES_MAP[name]
    except KeyError as exc:
        raise KeyError(
            f"Unknown challenge {name!r}. Available: {sorted(CHALLENGES_MAP)}"
        ) from exc
