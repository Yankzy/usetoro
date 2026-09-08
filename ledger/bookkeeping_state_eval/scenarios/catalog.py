from __future__ import annotations

from datetime import date, datetime, timezone

from bookkeeping_state.domain.bank import BankAccount, BankItem
from bookkeeping_state.domain.books import BookItem
from bookkeeping_state.domain.classifications import (
    ClassificationDecision,
    ClassificationInvalidation,
    ClassificationSource,
)
from bookkeeping_state.domain.context import (
    AccountingPolicy,
    BookkeepingContext,
)
from bookkeeping_state.domain.enums import Direction
from bookkeeping_state.domain.reconciliations import (
    BankAllocation,
    BookAllocation,
    Reconciliation,
    ReconciliationInvalidation,
)
from bookkeeping_state.domain.routing import (
    RoutingDecision,
    RoutingDecisionInvalidation,
    RoutingDecisionSource,
)
from bookkeeping_state.routing.scorer import (
    RoutingProviderScore,
    RoutingSemanticCandidate,
    RoutingSemanticScoreProvider,
    RoutingSemanticScoringRequest,
    RoutingSemanticScoringResponse,
)
from bookkeeping_state_eval.scenarios.expected_truth import (
    ExpectedReconciliation,
    ExpectedTruth,
)
from bookkeeping_state_eval.scenarios.models import ScenarioDefinition

DEFAULT_DATE = date(2026, 1, 15)
DEFAULT_CLOCK = datetime(2026, 1, 31, 12, 0, 0, tzinfo=timezone.utc)


def _default_context(
    company_id: str = "atlas",
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


def _reconciliation_invalidation(
    id: str,
    *,
    reconciliation_id: str,
    reason: str = "Invalidated",
) -> ReconciliationInvalidation:
    return ReconciliationInvalidation(
        id=id,
        reconciliation_id=reconciliation_id,
        reason=reason,
        state_revision_at_invalidation=0,
        created_at=DEFAULT_CLOCK,
    )


def _classification(
    id: str,
    *,
    book_item_id: str,
    account_code: str,
    supersedes: str | None = None,
    source: ClassificationSource = ClassificationSource.DETERMINISTIC_RULE,
) -> ClassificationDecision:
    return ClassificationDecision(
        id=id,
        book_item_id=book_item_id,
        account_code=account_code,
        source=source,
        supersedes_classification_id=supersedes,
        state_revision_at_decision=0,
        created_at=DEFAULT_CLOCK,
    )


def _classification_invalidation(
    id: str,
    *,
    classification_id: str,
    reason: str = "Invalidated",
) -> ClassificationInvalidation:
    return ClassificationInvalidation(
        id=id,
        classification_id=classification_id,
        reason=reason,
        state_revision_at_invalidation=0,
        created_at=DEFAULT_CLOCK,
    )


def _routing(
    id: str,
    *,
    book_item_id: str,
    bank_account_id: str,
    supersedes: str | None = None,
    source: RoutingDecisionSource = RoutingDecisionSource.HUMAN,
) -> RoutingDecision:
    return RoutingDecision(
        id=id,
        book_item_id=book_item_id,
        bank_account_id=bank_account_id,
        source=source,
        supersedes_routing_decision_id=supersedes,
        state_revision_at_creation=0,
        created_at=DEFAULT_CLOCK,
    )


# ======================================================================
# Scenario A: Simple exact customer receipt
# ======================================================================
def _build_scenario_a() -> ScenarioDefinition:
    ctx = _default_context()
    acc = BankAccount(id="acc-main", name="Main Account", currency="MAD")
    b_item = _bank_item(
        "bank-1",
        amount=100_000,
        direction=Direction.BANK_INFLOW,
        description="Client Alpha encaissement",
        reference="INV-100",
    )
    j_item = _book_item(
        "book-1",
        amount=100_000,
        direction=Direction.BOOK_BANK_DEBIT,
        description="Client Alpha invoice",
        reference="INV-100",
    )

    expected = ExpectedTruth(
        expected_routes={"book-1": "acc-main"},
        expected_classifications={"book-1": "3421"},
        expected_reconciliations=[
            ExpectedReconciliation.one_to_one("bank-1", "book-1", 100_000)
        ],
        expected_bank_remaining={"bank-1": 0},
        expected_book_remaining={"book-1": 0},
        expected_fully_reconciled_bank_items=["bank-1"],
        expected_fully_reconciled_book_items=["book-1"],
        expected_unreconciled_bank_items=[],
        expected_unreconciled_book_items=[],
        expected_artifact_counts={
            "reconciliations": 1,
            "routing_decisions": 1,
            "classifications": 1,
        },
    )

    return ScenarioDefinition(
        scenario_id="scenario_a_exact_receipt",
        name="Simple Exact Customer Receipt",
        description="Single incoming BankItem exactly matching customer BookItem.",
        context=ctx,
        bank_accounts=(acc,),
        bank_items=(b_item,),
        book_items=(j_item,),
        expected_truth=expected,
    )


# ======================================================================
# Scenario B: Customer pays three invoices in one transfer (1:N)
# ======================================================================
def _build_scenario_b() -> ScenarioDefinition:
    ctx = _default_context()
    acc = BankAccount(id="acc-main", name="Main Account", currency="MAD")
    b_item = _bank_item(
        "bank-1",
        amount=100_000,
        direction=Direction.BANK_INFLOW,
        description="Client Beta multi-invoice transfer",
    )
    j_1 = _book_item(
        "book-1",
        amount=50_000,
        direction=Direction.BOOK_BANK_DEBIT,
        description="Client Beta invoice 1",
    )
    j_2 = _book_item(
        "book-2",
        amount=30_000,
        direction=Direction.BOOK_BANK_DEBIT,
        description="Client Beta invoice 2",
    )
    j_3 = _book_item(
        "book-3",
        amount=20_000,
        direction=Direction.BOOK_BANK_DEBIT,
        description="Client Beta invoice 3",
    )

    expected = ExpectedTruth(
        expected_classifications={
            "book-1": "3421",
            "book-2": "3421",
            "book-3": "3421",
        },
        expected_reconciliations=[
            ExpectedReconciliation.one_to_many(
                "bank-1",
                {"book-1": 50_000, "book-2": 30_000, "book-3": 20_000},
            )
        ],
        expected_bank_remaining={"bank-1": 0},
        expected_book_remaining={"book-1": 0, "book-2": 0, "book-3": 0},
        expected_fully_reconciled_bank_items=["bank-1"],
        expected_fully_reconciled_book_items=["book-1", "book-2", "book-3"],
        expected_unreconciled_bank_items=[],
        expected_unreconciled_book_items=[],
        expected_artifact_counts={"reconciliations": 1},
    )

    return ScenarioDefinition(
        scenario_id="scenario_b_one_to_many",
        name="Customer Pays Three Invoices in One Transfer",
        description="1 incoming BankItem clears 3 BookItems via 1:N grouped reconciliation.",
        context=ctx,
        bank_accounts=(acc,),
        bank_items=(b_item,),
        book_items=(j_1, j_2, j_3),
        expected_truth=expected,
    )


# ======================================================================
# Scenario C: Three customer receipts settle one aggregated book entry (N:1)
# ======================================================================
def _build_scenario_c() -> ScenarioDefinition:
    ctx = _default_context()
    acc = BankAccount(id="acc-main", name="Main Account", currency="MAD")
    b_1 = _bank_item("bank-1", amount=20_000, description="Deposit 1", reference="GAMMA-BATCH")
    b_2 = _bank_item("bank-2", amount=30_000, description="Deposit 2", reference="GAMMA-BATCH")
    b_3 = _bank_item("bank-3", amount=50_000, description="Deposit 3", reference="GAMMA-BATCH")
    j_item = _book_item(
        "book-1",
        amount=100_000,
        direction=Direction.BOOK_BANK_DEBIT,
        description="Client Gamma batch invoice",
        reference="GAMMA-BATCH",
    )

    expected = ExpectedTruth(
        expected_routes={"book-1": "acc-main"},
        expected_classifications={"book-1": "3421"},
        expected_pairwise_allocations={
            ("bank-1", "book-1"): 20_000,
            ("bank-2", "book-1"): 30_000,
            ("bank-3", "book-1"): 50_000,
        },
        expected_bank_remaining={"bank-1": 0, "bank-2": 0, "bank-3": 0},
        expected_book_remaining={"book-1": 0},
        expected_fully_reconciled_bank_items=["bank-1", "bank-2", "bank-3"],
        expected_fully_reconciled_book_items=["book-1"],
        expected_unreconciled_bank_items=[],
        expected_unreconciled_book_items=[],
    )

    return ScenarioDefinition(
        scenario_id="scenario_c_many_to_one",
        name="Three Receipts Settle One Aggregated Book Entry",
        description="3 BankItems settle 1 BookItem via grouped or partial reconciliation.",
        context=ctx,
        bank_accounts=(acc,),
        bank_items=(b_1, b_2, b_3),
        book_items=(j_item,),
        expected_truth=expected,
    )


# ======================================================================
# Scenario D: Partial customer receipt
# ======================================================================
def _build_scenario_d() -> ScenarioDefinition:
    ctx = _default_context(allow_partial_book_reconciliation=True)
    acc = BankAccount(id="acc-main", name="Main Account", currency="MAD")
    b_item = _bank_item(
        "bank-1",
        amount=40_000,
        direction=Direction.BANK_INFLOW,
        description="Client Delta partial payment",
    )
    j_item = _book_item(
        "book-1",
        amount=100_000,
        direction=Direction.BOOK_BANK_DEBIT,
        description="Client Delta full invoice",
    )

    expected = ExpectedTruth(
        expected_classifications={"book-1": "3421"},
        expected_reconciliations=[
            ExpectedReconciliation.one_to_one("bank-1", "book-1", 40_000)
        ],
        expected_bank_remaining={"bank-1": 0},
        expected_book_remaining={"book-1": 60_000},
        expected_fully_reconciled_bank_items=["bank-1"],
        expected_partially_reconciled_book_items=["book-1"],
        expected_fully_reconciled_book_items=[],
        expected_unreconciled_bank_items=[],
        expected_unreconciled_book_items=[],
        expected_artifact_counts={"reconciliations": 1},
    )

    return ScenarioDefinition(
        scenario_id="scenario_d_partial_customer_receipt",
        name="Partial Customer Receipt",
        description="BankItem (40k) is smaller than BookItem (100k); leaves 60k residual.",
        context=ctx,
        bank_accounts=(acc,),
        bank_items=(b_item,),
        book_items=(j_item,),
        expected_truth=expected,
    )


# ======================================================================
# Scenario E: Partial bank consumption forbidden
# ======================================================================
def _build_scenario_e() -> ScenarioDefinition:
    ctx = _default_context(allow_partial_bank_reconciliation=False)
    acc = BankAccount(id="acc-main", name="Main Account", currency="MAD")
    b_item = _bank_item(
        "bank-1",
        amount=100_000,
        direction=Direction.BANK_INFLOW,
        description="Large payment deposit",
    )
    j_item = _book_item(
        "book-1",
        amount=40_000,
        direction=Direction.BOOK_BANK_DEBIT,
        description="Small invoice",
    )

    expected = ExpectedTruth(
        expected_reconciliations=(),
        expected_bank_remaining={"bank-1": 100_000},
        expected_book_remaining={"book-1": 40_000},
        expected_unreconciled_bank_items=["bank-1"],
        expected_unreconciled_book_items=["book-1"],
        expected_fully_reconciled_bank_items=[],
        expected_fully_reconciled_book_items=[],
        expected_artifact_counts={"reconciliations": 0},
    )

    return ScenarioDefinition(
        scenario_id="scenario_e_partial_bank_forbidden",
        name="Partial Bank Consumption Forbidden",
        description="BankItem (100k) cannot be partially split to match BookItem (40k); remains unresolved.",
        context=ctx,
        bank_accounts=(acc,),
        bank_items=(b_item,),
        book_items=(j_item,),
        expected_truth=expected,
    )


# ======================================================================
# Scenario F: Two bank accounts with routing ambiguity
# ======================================================================
class _AccountASemanticScorer(RoutingSemanticScoreProvider):
    def score(
        self,
        request: RoutingSemanticScoringRequest,
    ) -> RoutingSemanticScoringResponse:
        scores = []
        for cand in request.candidates:
            val = 900 if cand.bank_account_id == "acc-a" else 100
            scores.append(
                RoutingProviderScore(
                    book_item_id=cand.book_item_id,
                    bank_account_id=cand.bank_account_id,
                    score=val,
                    rationale="Prefers account A",
                )
            )
        return RoutingSemanticScoringResponse(
            state_revision=request.state_revision,
            model_run_id="scorer-pref-a",
            scores=tuple(scores),
        )


def _build_scenario_f() -> ScenarioDefinition:
    ctx = _default_context()
    acc_a = BankAccount(id="acc-a", name="Bank Account A", currency="MAD")
    acc_b = BankAccount(id="acc-b", name="Bank Account B", currency="MAD")
    b_a = _bank_item(
        "bank-a",
        bank_account_id="acc-a",
        amount=50_000,
        description="Deposit at Branch A",
        reference="REC-BRANCH-A",
    )
    b_b = _bank_item(
        "bank-b",
        bank_account_id="acc-b",
        amount=50_000,
        description="Deposit at Branch B",
        reference="REC-BRANCH-B",
    )
    j_item = _book_item(
        "book-1",
        amount=50_000,
        description="Customer Receipt",
        reference="REC-BRANCH-A",
    )

    expected = ExpectedTruth(
        expected_routes={"book-1": "acc-a"},
        expected_classifications={"book-1": "3421"},
        expected_reconciliations=[
            ExpectedReconciliation.one_to_one("bank-a", "book-1", 50_000)
        ],
        expected_bank_remaining={"bank-a": 0, "bank-b": 50_000},
        expected_book_remaining={"book-1": 0},
        expected_fully_reconciled_bank_items=["bank-a"],
        expected_unreconciled_bank_items=["bank-b"],
        expected_fully_reconciled_book_items=["book-1"],
        expected_unreconciled_book_items=[],
        expected_artifact_counts={"reconciliations": 1, "routing_decisions": 1},
    )

    return ScenarioDefinition(
        scenario_id="scenario_f_routing_ambiguity",
        name="Two Bank Accounts with Routing Ambiguity",
        description="Both accounts feasible; semantic routing selects acc-a, constraining reconciliation to bank-a.",
        context=ctx,
        bank_accounts=(acc_a, acc_b),
        bank_items=(b_a, b_b),
        book_items=(j_item,),
        routing_semantic_provider=_AccountASemanticScorer(),
        expected_truth=expected,
    )


# ======================================================================
# Scenario G: Same amount repeated multiple times
# ======================================================================
def _build_scenario_g() -> ScenarioDefinition:
    ctx = _default_context()
    acc = BankAccount(id="acc-main", name="Main Account", currency="MAD")
    b_1 = _bank_item(
        "bank-1",
        amount=30_000,
        date_val=date(2026, 1, 10),
        description="Client payment 1",
        reference="INV-001",
    )
    b_2 = _bank_item(
        "bank-2",
        amount=30_000,
        date_val=date(2026, 1, 20),
        description="Client payment 2",
        reference="INV-002",
    )
    j_1 = _book_item(
        "book-1",
        amount=30_000,
        date_val=date(2026, 1, 10),
        description="Customer invoice 1",
        reference="INV-001",
    )
    j_2 = _book_item(
        "book-2",
        amount=30_000,
        date_val=date(2026, 1, 20),
        description="Customer invoice 2",
        reference="INV-002",
    )

    expected = ExpectedTruth(
        expected_reconciliations=[
            ExpectedReconciliation.one_to_one("bank-1", "book-1", 30_000),
            ExpectedReconciliation.one_to_one("bank-2", "book-2", 30_000),
        ],
        expected_bank_remaining={"bank-1": 0, "bank-2": 0},
        expected_book_remaining={"book-1": 0, "book-2": 0},
        expected_fully_reconciled_bank_items=["bank-1", "bank-2"],
        expected_fully_reconciled_book_items=["book-1", "book-2"],
        expected_unreconciled_bank_items=[],
        expected_unreconciled_book_items=[],
        expected_artifact_counts={"reconciliations": 2},
    )

    return ScenarioDefinition(
        scenario_id="scenario_g_duplicate_amounts_tie_breaking",
        name="Same Amount Repeated Multiple Times",
        description="Multiple BankItems & BookItems of 30k paired deterministically via references without double-consumption.",
        context=ctx,
        bank_accounts=(acc,),
        bank_items=(b_1, b_2),
        book_items=(j_1, j_2),
        expected_truth=expected,
    )


# ======================================================================
# Scenario H: Supplier payment covering multiple bills
# ======================================================================
def _build_scenario_h() -> ScenarioDefinition:
    ctx = _default_context()
    acc = BankAccount(id="acc-main", name="Main Account", currency="MAD")
    b_item = _bank_item(
        "bank-1",
        amount=75_000,
        direction=Direction.BANK_OUTFLOW,
        description="Fournisseur Gamma reglement",
    )
    j_1 = _book_item(
        "book-1",
        amount=45_000,
        direction=Direction.BOOK_BANK_CREDIT,
        description="Fournisseur Gamma bill 1",
    )
    j_2 = _book_item(
        "book-2",
        amount=30_000,
        direction=Direction.BOOK_BANK_CREDIT,
        description="Fournisseur Gamma bill 2",
    )

    expected = ExpectedTruth(
        expected_classifications={"book-1": "4411", "book-2": "4411"},
        expected_reconciliations=[
            ExpectedReconciliation.one_to_many(
                "bank-1",
                {"book-1": 45_000, "book-2": 30_000},
            )
        ],
        expected_bank_remaining={"bank-1": 0},
        expected_book_remaining={"book-1": 0, "book-2": 0},
        expected_fully_reconciled_bank_items=["bank-1"],
        expected_fully_reconciled_book_items=["book-1", "book-2"],
        expected_unreconciled_bank_items=[],
        expected_unreconciled_book_items=[],
        expected_artifact_counts={"reconciliations": 1},
    )

    return ScenarioDefinition(
        scenario_id="scenario_h_supplier_payment_multiple_bills",
        name="Supplier Payment Covering Multiple Bills",
        description="Outgoing BANK_OUTFLOW settling multiple BOOK_BANK_CREDIT bills with correct direction mapping.",
        context=ctx,
        bank_accounts=(acc,),
        bank_items=(b_item,),
        book_items=(j_1, j_2),
        expected_truth=expected,
    )


# ======================================================================
# Scenario I: Bank transaction with no valid book match
# ======================================================================
def _build_scenario_i() -> ScenarioDefinition:
    ctx = _default_context()
    acc = BankAccount(id="acc-main", name="Main Account", currency="MAD")
    b_item = _bank_item(
        "bank-1",
        amount=88_000,
        direction=Direction.BANK_INFLOW,
        description="Unknown wire transfer",
    )
    j_item = _book_item(
        "book-1",
        amount=35_000,
        direction=Direction.BOOK_BANK_DEBIT,
        description="Unrelated customer invoice",
    )

    expected = ExpectedTruth(
        expected_reconciliations=(),
        expected_bank_remaining={"bank-1": 88_000},
        expected_book_remaining={"book-1": 35_000},
        expected_unreconciled_bank_items=["bank-1"],
        expected_unreconciled_book_items=["book-1"],
        expected_fully_reconciled_bank_items=[],
        expected_fully_reconciled_book_items=[],
        expected_artifact_counts={"reconciliations": 0},
    )

    return ScenarioDefinition(
        scenario_id="scenario_i_unmatched_bank_transaction",
        name="Bank Transaction with No Valid Book Match",
        description="BankItem cannot match book entry; remains unresolved with zero fake reconciliations.",
        context=ctx,
        bank_accounts=(acc,),
        bank_items=(b_item,),
        book_items=(j_item,),
        expected_truth=expected,
    )


# ======================================================================
# Scenario J: Book entry with no corresponding bank activity
# ======================================================================
def _build_scenario_j() -> ScenarioDefinition:
    ctx = _default_context()
    acc = BankAccount(id="acc-main", name="Main Account", currency="MAD")
    j_item = _book_item(
        "book-1",
        amount=50_000,
        direction=Direction.BOOK_BANK_DEBIT,
        description="Customer receivable invoice",
    )

    expected = ExpectedTruth(
        expected_routes={"book-1": None},
        expected_classifications={"book-1": "3421"},
        expected_reconciliations=(),
        expected_book_remaining={"book-1": 50_000},
        expected_unreconciled_book_items=["book-1"],
        expected_fully_reconciled_book_items=[],
        expected_artifact_counts={"reconciliations": 0, "routing_decisions": 0},
    )

    return ScenarioDefinition(
        scenario_id="scenario_j_unmatched_book_entry",
        name="Book Entry with No Corresponding Bank Activity",
        description="BookItem has no bank activity; remains unresolved and unrouted.",
        context=ctx,
        bank_accounts=(acc,),
        bank_items=(),
        book_items=(j_item,),
        expected_truth=expected,
    )


# ======================================================================
# Scenario K: Currency mismatch
# ======================================================================
def _build_scenario_k() -> ScenarioDefinition:
    ctx = _default_context()
    acc = BankAccount(id="acc-eur", name="EUR Account", currency="EUR")
    b_item = _bank_item(
        "bank-1",
        bank_account_id="acc-eur",
        amount=10_000,
        currency="EUR",
        description="EUR Receipt",
    )
    j_item = _book_item(
        "book-1",
        amount=10_000,
        currency="MAD",
        description="MAD Customer invoice",
    )

    expected = ExpectedTruth(
        expected_routes={"book-1": None},
        expected_reconciliations=(),
        expected_bank_remaining={"bank-1": 10_000},
        expected_book_remaining={"book-1": 10_000},
        expected_unreconciled_bank_items=["bank-1"],
        expected_unreconciled_book_items=["book-1"],
        expected_fully_reconciled_bank_items=[],
        expected_fully_reconciled_book_items=[],
        expected_artifact_counts={"reconciliations": 0},
    )

    return ScenarioDefinition(
        scenario_id="scenario_k_currency_mismatch",
        name="Currency Mismatch",
        description="Same monetary quantity (10k) but incompatible currencies (EUR vs MAD); reconciliation forbidden.",
        context=ctx,
        bank_accounts=(acc,),
        bank_items=(b_item,),
        book_items=(j_item,),
        expected_truth=expected,
    )


# ======================================================================
# Scenario L: Date-window rejection
# ======================================================================
def _build_scenario_l() -> ScenarioDefinition:
    # 45 days window configured
    ctx = _default_context(reconciliation_date_window_days=45)
    acc = BankAccount(id="acc-main", name="Main Account", currency="MAD")
    b_item = _bank_item(
        "bank-1",
        amount=50_000,
        date_val=date(2026, 1, 1),
        description="Payment in January",
    )
    j_item = _book_item(
        "book-1",
        amount=50_000,
        date_val=date(2026, 4, 15),  # 104 days apart > 45 days window
        origin_period="2026-04",
        description="Invoice dated April",
    )

    expected = ExpectedTruth(
        expected_reconciliations=(),
        expected_bank_remaining={"bank-1": 50_000},
        expected_book_remaining={"book-1": 50_000},
        expected_unreconciled_bank_items=["bank-1"],
        expected_unreconciled_book_items=["book-1"],
        expected_fully_reconciled_bank_items=[],
        expected_fully_reconciled_book_items=[],
        expected_artifact_counts={"reconciliations": 0},
    )

    return ScenarioDefinition(
        scenario_id="scenario_l_date_window_rejection",
        name="Date-Window Rejection",
        description="Transactions separated by 104 days exceed the 45-day window; must remain unreconciled.",
        context=ctx,
        bank_accounts=(acc,),
        bank_items=(b_item,),
        book_items=(j_item,),
        expected_truth=expected,
    )


# ======================================================================
# Scenario M: Pre-existing partial reconciliation
# ======================================================================
def _build_scenario_m() -> ScenarioDefinition:
    ctx = _default_context()
    acc = BankAccount(id="acc-main", name="Main Account", currency="MAD")
    b_1 = _bank_item(
        "bank-1",
        amount=40_000,
        date_val=date(2026, 1, 10),
        description="Deposit 1",
        reference="INV-BETA",
    )
    b_2 = _bank_item(
        "bank-2",
        amount=60_000,
        date_val=date(2026, 1, 15),
        description="Deposit 2",
        reference="INV-BETA",
    )
    j_1 = _book_item(
        "book-1",
        amount=100_000,
        date_val=date(2026, 1, 10),
        description="Client Beta invoice",
        reference="INV-BETA",
    )
    # Existing durable reconciliation: bank-1 (40k) -> book-1 (40k)
    prior_rec = _reconciliation(
        "rec-prior-1",
        bank=(("bank-1", 40_000),),
        book=(("book-1", 40_000),),
    )

    expected = ExpectedTruth(
        expected_routes={"book-1": "acc-main"},
        expected_classifications={"book-1": "3421"},
        expected_reconciliations=[
            ExpectedReconciliation.one_to_one("bank-1", "book-1", 40_000),
            ExpectedReconciliation.one_to_one("bank-2", "book-1", 60_000),
        ],
        expected_bank_remaining={"bank-1": 0, "bank-2": 0},
        expected_book_remaining={"book-1": 0},
        expected_fully_reconciled_bank_items=["bank-1", "bank-2"],
        expected_fully_reconciled_book_items=["book-1"],
        expected_unreconciled_bank_items=[],
        expected_unreconciled_book_items=[],
        expected_artifact_counts={"reconciliations": 2},
    )

    return ScenarioDefinition(
        scenario_id="scenario_m_preexisting_partial_reconciliation",
        name="Pre-existing Partial Reconciliation",
        description="Repository starts with 40k reconciled; session reconciles residual 60k using bank-2.",
        context=ctx,
        bank_accounts=(acc,),
        bank_items=(b_1, b_2),
        book_items=(j_1,),
        reconciliations=(prior_rec,),
        expected_truth=expected,
    )


# ======================================================================
# Scenario N: Invalidation releases capacity
# ======================================================================
def _build_scenario_n() -> ScenarioDefinition:
    ctx = _default_context()
    acc = BankAccount(id="acc-main", name="Main Account", currency="MAD")
    b_1 = _bank_item(
        "bank-1",
        amount=50_000,
        description="Client payment",
        reference="INV-RIGHT",
    )
    j_wrong = _book_item(
        "book-wrong",
        amount=50_000,
        description="Wrong invoice",
        reference="INV-WRONG",
    )
    j_right = _book_item(
        "book-right",
        amount=50_000,
        description="Right invoice",
        reference="INV-RIGHT",
    )
    # Stale reconciliation that was previously invalidated
    rec_bad = _reconciliation(
        "rec-bad",
        bank=(("bank-1", 50_000),),
        book=(("book-wrong", 50_000),),
    )
    inv_bad = _reconciliation_invalidation(
        "inv-bad",
        reconciliation_id="rec-bad",
        reason="Allocated to wrong invoice",
    )

    expected = ExpectedTruth(
        expected_reconciliations=[
            ExpectedReconciliation.one_to_one("bank-1", "book-right", 50_000)
        ],
        expected_bank_remaining={"bank-1": 0},
        expected_book_remaining={"book-right": 0, "book-wrong": 50_000},
        expected_fully_reconciled_bank_items=["bank-1"],
        expected_fully_reconciled_book_items=["book-right"],
        expected_unreconciled_book_items=["book-wrong"],
        expected_artifact_counts={
            "reconciliations": 2,
            "reconciliation_invalidations": 1,
        },
    )

    return ScenarioDefinition(
        scenario_id="scenario_n_invalidation_releases_capacity",
        name="Invalidation Releases Capacity",
        description="Prior reconciliation was invalidated; capacity released and session matches bank-1 to book-right.",
        context=ctx,
        bank_accounts=(acc,),
        bank_items=(b_1,),
        book_items=(j_wrong, j_right),
        reconciliations=(rec_bad,),
        reconciliation_invalidations=(inv_bad,),
        expected_truth=expected,
    )


# ======================================================================
# Scenario O: Supersession non-resurrection
# ======================================================================
def _build_scenario_o() -> ScenarioDefinition:
    ctx = _default_context()
    acc = BankAccount(id="acc-main", name="Main Account", currency="MAD")
    b_1 = _bank_item(
        "bank-1",
        amount=50_000,
        direction=Direction.BANK_OUTFLOW,
        description="Office supplies purchase",
    )
    j_1 = _book_item(
        "book-1",
        amount=50_000,
        direction=Direction.BOOK_BANK_CREDIT,
        description="Office supplies papeterie",
    )
    # Historical chain: C1 superseded by C2, C2 subsequently invalidated
    c1 = _classification("cls-1", book_item_id="book-1", account_code="6111")
    c2 = _classification(
        "cls-2",
        book_item_id="book-1",
        account_code="6125",
        source=ClassificationSource.HUMAN,
        supersedes="cls-1",
    )
    inv_c2 = _classification_invalidation(
        "inv-cls-2",
        classification_id="cls-2",
        reason="Mistaken override",
    )

    expected = ExpectedTruth(
        # DAG classifies freshly into 6125 based on 'papeterie' regex; C1 is NOT silently resurrected
        expected_classifications={"book-1": "6125"},
        expected_reconciliations=[
            ExpectedReconciliation.one_to_one("bank-1", "book-1", 50_000)
        ],
        expected_bank_remaining={"bank-1": 0},
        expected_book_remaining={"book-1": 0},
        expected_fully_reconciled_bank_items=["bank-1"],
        expected_fully_reconciled_book_items=["book-1"],
        expected_artifact_counts={
            "classifications": 3,
            "classification_invalidations": 1,
            "reconciliations": 1,
        },
    )

    return ScenarioDefinition(
        scenario_id="scenario_o_supersession_non_resurrection",
        name="Supersession Non-Resurrection",
        description="Superseded C1 is not resurrected when C2 is invalidated; DAG cleanly re-evaluates truth.",
        context=ctx,
        bank_accounts=(acc,),
        bank_items=(b_1,),
        book_items=(j_1,),
        classifications=(c1, c2),
        classification_invalidations=(inv_c2,),
        expected_truth=expected,
    )


# ======================================================================
# Scenario P: Second-session idempotency
# ======================================================================
def _build_scenario_p() -> ScenarioDefinition:
    base_scen = _build_scenario_a()
    return ScenarioDefinition(
        scenario_id="scenario_p_second_session_idempotency",
        name="Second-Session Idempotency",
        description="Session executed twice on same repository; second run skips all stages and produces zero mutations.",
        context=base_scen.context,
        bank_accounts=base_scen.bank_accounts,
        bank_items=base_scen.bank_items,
        book_items=base_scen.book_items,
        expected_truth=base_scen.expected_truth,
    )


# ======================================================================
# Adversarial Scenario Q: Global CP-SAT optimizer competition
# ======================================================================
def _build_scenario_q() -> ScenarioDefinition:
    ctx = _default_context()
    acc = BankAccount(id="acc-main", name="Main Account", currency="MAD")
    b_1 = _bank_item(
        "bank-1",
        amount=100_000,
        direction=Direction.BANK_INFLOW,
        description="Customer transfer",
        reference="GROUP-REF",
    )
    j_single = _book_item(
        "book-single",
        amount=100_000,
        direction=Direction.BOOK_BANK_DEBIT,
        description="Single invoice",
    )
    j_group_1 = _book_item(
        "book-group-1",
        amount=60_000,
        direction=Direction.BOOK_BANK_DEBIT,
        description="Grouped invoice 1",
        reference="GROUP-REF",
    )
    j_group_2 = _book_item(
        "book-group-2",
        amount=40_000,
        direction=Direction.BOOK_BANK_DEBIT,
        description="Grouped invoice 2",
        reference="GROUP-REF",
    )

    expected = ExpectedTruth(
        expected_reconciliations=[
            ExpectedReconciliation.one_to_many(
                "bank-1",
                {"book-group-1": 60_000, "book-group-2": 40_000},
            )
        ],
        expected_bank_remaining={"bank-1": 0},
        expected_book_remaining={
            "book-group-1": 0,
            "book-group-2": 0,
            "book-single": 100_000,
        },
        expected_fully_reconciled_bank_items=["bank-1"],
        expected_fully_reconciled_book_items=["book-group-1", "book-group-2"],
        expected_unreconciled_book_items=["book-single"],
        expected_artifact_counts={"reconciliations": 1},
    )

    return ScenarioDefinition(
        scenario_id="scenario_q_optimizer_competition",
        name="Global Optimizer Competition",
        description="1:1 and 1:N compete for bank-1; global optimizer selects 1:N to maximize cleared items and utility.",
        context=ctx,
        bank_accounts=(acc,),
        bank_items=(b_1,),
        book_items=(j_single, j_group_1, j_group_2),
        expected_truth=expected,
    )


# ======================================================================
# Adversarial Scenario R: Pre-routed account contradiction
# ======================================================================
def _build_scenario_r() -> ScenarioDefinition:
    ctx = _default_context()
    acc_1 = BankAccount(id="acc-1", name="Account 1", currency="MAD")
    acc_2 = BankAccount(id="acc-2", name="Account 2", currency="MAD")
    b_item = _bank_item(
        "bank-2",
        bank_account_id="acc-2",
        amount=50_000,
        description="Deposit in acc-2",
    )
    j_item = _book_item(
        "book-1",
        amount=50_000,
        description="Invoice pre-routed to acc-1",
    )
    route_to_1 = _routing(
        "route-1",
        book_item_id="book-1",
        bank_account_id="acc-1",
    )

    expected = ExpectedTruth(
        expected_routes={"book-1": "acc-1"},
        expected_reconciliations=(),
        expected_bank_remaining={"bank-2": 50_000},
        expected_book_remaining={"book-1": 50_000},
        expected_unreconciled_bank_items=["bank-2"],
        expected_unreconciled_book_items=["book-1"],
        expected_fully_reconciled_bank_items=[],
        expected_fully_reconciled_book_items=[],
        expected_artifact_counts={"reconciliations": 0, "routing_decisions": 1},
    )

    return ScenarioDefinition(
        scenario_id="scenario_r_routing_contradiction_blocks_reconciliation",
        name="Routing Contradiction Blocks Reconciliation",
        description="BookItem routed to acc-1 cannot reconcile against BankItem in acc-2; remains unresolved.",
        context=ctx,
        bank_accounts=(acc_1, acc_2),
        bank_items=(b_item,),
        book_items=(j_item,),
        routing_decisions=(route_to_1,),
        expected_truth=expected,
    )


# ======================================================================
# Adversarial Scenario S: Superseded route coexistence
# ======================================================================
def _build_scenario_s() -> ScenarioDefinition:
    ctx = _default_context()
    acc_old = BankAccount(id="acc-old", name="Old Account", currency="MAD")
    acc_new = BankAccount(id="acc-new", name="New Account", currency="MAD")
    b_item = _bank_item(
        "bank-1",
        bank_account_id="acc-new",
        amount=50_000,
        description="Deposit in acc-new",
        reference="REC-NEW",
    )
    j_item = _book_item(
        "book-1",
        amount=50_000,
        description="Customer Receipt",
        reference="REC-NEW",
    )
    r_old = _routing(
        "route-old",
        book_item_id="book-1",
        bank_account_id="acc-old",
        source=RoutingDecisionSource.CP_SAT,
    )
    r_new = _routing(
        "route-new",
        book_item_id="book-1",
        bank_account_id="acc-new",
        supersedes="route-old",
        source=RoutingDecisionSource.HUMAN,
    )

    expected = ExpectedTruth(
        expected_routes={"book-1": "acc-new"},
        expected_reconciliations=[
            ExpectedReconciliation.one_to_one("bank-1", "book-1", 50_000)
        ],
        expected_bank_remaining={"bank-1": 0},
        expected_book_remaining={"book-1": 0},
        expected_fully_reconciled_bank_items=["bank-1"],
        expected_fully_reconciled_book_items=["book-1"],
        expected_artifact_counts={"reconciliations": 1, "routing_decisions": 2},
    )

    return ScenarioDefinition(
        scenario_id="scenario_s_superseded_route_coexistence",
        name="Superseded Route Coexistence",
        description="Historical superseded route coexists cleanly; active route to acc-new allows reconciliation.",
        context=ctx,
        bank_accounts=(acc_old, acc_new),
        bank_items=(b_item,),
        book_items=(j_item,),
        routing_decisions=(r_old, r_new),
        expected_truth=expected,
    )


# ======================================================================
# Scenario Catalog Registry
# ======================================================================
SCENARIOS_MAP: dict[str, ScenarioDefinition] = {
    s.scenario_id: s
    for s in (
        _build_scenario_a(),
        _build_scenario_b(),
        _build_scenario_c(),
        _build_scenario_d(),
        _build_scenario_e(),
        _build_scenario_f(),
        _build_scenario_g(),
        _build_scenario_h(),
        _build_scenario_i(),
        _build_scenario_j(),
        _build_scenario_k(),
        _build_scenario_l(),
        _build_scenario_m(),
        _build_scenario_n(),
        _build_scenario_o(),
        _build_scenario_p(),
        _build_scenario_q(),
        _build_scenario_r(),
        _build_scenario_s(),
    )
}

SCENARIO_CATALOG: tuple[ScenarioDefinition, ...] = tuple(SCENARIOS_MAP.values())


def get_scenario(scenario_id: str) -> ScenarioDefinition:
    try:
        return SCENARIOS_MAP[scenario_id]
    except KeyError as exc:
        raise KeyError(
            f"Unknown scenario ID {scenario_id!r}. Available: {sorted(SCENARIOS_MAP)}"
        ) from exc
