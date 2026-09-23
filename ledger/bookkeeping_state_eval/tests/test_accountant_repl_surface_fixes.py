from __future__ import annotations

import io
from contextlib import redirect_stdout
from datetime import date, datetime, timezone
from decimal import Decimal

import pytest
from bookkeeping_state.domain.bank import BankAccount, BankItem
from bookkeeping_state.domain.books import BookItem
from bookkeeping_state.domain.context import AccountingPolicy, BookkeepingContext
from bookkeeping_state.domain.enums import Direction, SourceType
from bookkeeping_state.domain.money import (
    format_money,
)
from bookkeeping_state.domain.reconciliations import (
    BankAllocation,
    BookAllocation,
    Reconciliation,
)
from bookkeeping_state.domain.residual_bank_classifications import (
    ResidualBankClassificationDecision,
    ResidualBankClassificationStatus,
    ResidualBankPosting,
)
from bookkeeping_state.operator.tools import execute_operator_tool
from bookkeeping_state.persistence.repository import (
    BookkeepingRepository,
    BookkeepingSnapshot,
    PersistenceCommitResult,
    PersistenceWriteSet,
)
from bookkeeping_state_eval.lab import BookkeepingLab
from bookkeeping_state_eval.operator.workbench import BookkeepingWorkbench

# -----------------------------------------------------------------------------
# 1. Money Formatting Tests
# -----------------------------------------------------------------------------


def test_money_formatting_canonical_cases() -> None:
    """Verify that raw AmountUnits (scale 10,000) render as human-readable currency."""
    assert format_money(78_500_000) == "7,850.00 MAD"
    assert format_money(1_500_000) == "150.00 MAD"
    assert format_money(34_000_000) == "3,400.00 MAD"
    assert format_money(100_000_000) == "10,000.00 MAD"


def test_money_formatting_types_and_edge_cases() -> None:
    """Verify format_money handles string units, Decimal, int, None, and custom currencies."""
    assert format_money("78500000", "MAD") == "7,850.00 MAD"
    assert format_money(Decimal("7850.00"), "MAD") == "7,850.00 MAD"
    assert format_money(0, "MAD") == "0.00 MAD"
    assert format_money(None, "MAD") == "0.00 MAD"
    assert format_money(1_500_000, "USD") == "150.00 USD"
    assert format_money(1_500_000, currency="") == "150.00"

    # Raw units must never equal the formatted output
    raw_str = f"{78_500_000:,} MAD"
    assert raw_str == "78,500,000 MAD"
    assert format_money(78_500_000) != raw_str


def _make_decision(
    *,
    decision_id: str,
    bank_item: BankItem,
    status: ResidualBankClassificationStatus,
    account_code: str | None = None,
    account_id: str | None = None,
    confidence: float | None = None,
    hold_reason: str | None = None,
    required_evidence: tuple[str, ...] = (),
    rationale: str = "Test rationale",
) -> ResidualBankClassificationDecision:
    now = datetime(2026, 9, 15, 12, 0, 0, tzinfo=timezone.utc)
    acc_id = account_id or (f"acc-{account_code}" if account_code else None)
    return ResidualBankClassificationDecision(
        id=decision_id,
        bank_item_id=bank_item.id,
        staged_transaction_id=f"staged:{bank_item.id}",
        bank_account_id=bank_item.bank_account_id,
        status=status,
        account_code=account_code,
        account_id=acc_id,
        original_amount_units=int(bank_item.amount_units),
        residual_amount_units=int(bank_item.amount_units),
        direction=bank_item.direction,
        currency=bank_item.currency,
        confidence=confidence if confidence is not None else (0.99 if status == ResidualBankClassificationStatus.CLASSIFIED else None),
        rationale=rationale,
        hold_reason=hold_reason,
        required_evidence=required_evidence,
        schema_version="bookkeeping.ase.bank_categorize.v1",
        dag_id="bank-cash-accounting-dag",
        request_semantic_digest="digest-1234567890",
        state_revision_at_decision=1,
        persistence_revision_at_decision=1,
        supersedes_decision_id=None,
        created_at=now,
    )


# -----------------------------------------------------------------------------
# Test Fixture: State with 4 Residual Decisions (3 CLASSIFIED, 1 HOLD)
# -----------------------------------------------------------------------------


@pytest.fixture
def production_like_workbench() -> BookkeepingWorkbench:
    """
    Construct an in-memory repository replicating the proven production state:
    - 7 bank items (6 reconciled, 1 residual item on HOLD for 7,850.00 MAD)
    - 4 residual bank classifications (3 CLASSIFIED, 1 HOLD)
    - 3 residual bank postings
    - 1 active residual HOLD
    """
    company_id = "test-company"
    bank_acc = BankAccount(id="acc-1", name="Operating MAD", currency="MAD")

    b_hold = BankItem(
        id="staged:bank-hold-1",
        bank_account_id="acc-1",
        date=date(2026, 9, 10),
        amount_units="78500000",  # 7,850.00 MAD
        direction=Direction.BANK_OUTFLOW,
        currency="MAD",
        description="VIREMENT EMIS AMBIGU",
        reference="VIR-AMBIG",
    )

    b_cls_1 = BankItem(
        id="staged:bank-cls-1",
        bank_account_id="acc-1",
        date=date(2026, 9, 11),
        amount_units="1500000",  # 150.00 MAD
        direction=Direction.BANK_OUTFLOW,
        currency="MAD",
        description="FRAIS BANCAIRES",
    )
    b_cls_2 = BankItem(
        id="staged:bank-cls-2",
        bank_account_id="acc-1",
        date=date(2026, 9, 12),
        amount_units="34000000",  # 3,400.00 MAD
        direction=Direction.BANK_OUTFLOW,
        currency="MAD",
        description="FOURNITURES BUREAU",
    )
    b_cls_3 = BankItem(
        id="staged:bank-cls-3",
        bank_account_id="acc-1",
        date=date(2026, 9, 13),
        amount_units="100000000",  # 10,000.00 MAD
        direction=Direction.BANK_OUTFLOW,
        currency="MAD",
        description="PRESTATION TECHNIQUE",
    )

    # 4 Residual decisions
    dec_hold = _make_decision(
        decision_id="res-dec-hold",
        bank_item=b_hold,
        status=ResidualBankClassificationStatus.HOLD,
        hold_reason="Ambiguous counterparty and missing contract documentation",
        required_evidence=("PROOF_OF_PAYMENT", "VENDOR_CONTRACT"),
        confidence=0.45,
        rationale="Insufficient evidence to distinguish between expense vs loan",
    )

    dec_cls_1 = _make_decision(
        decision_id="res-dec-cls-1",
        bank_item=b_cls_1,
        status=ResidualBankClassificationStatus.CLASSIFIED,
        account_code="6147",
        confidence=0.99,
        rationale="Bank fees pattern matched",
    )

    dec_cls_2 = _make_decision(
        decision_id="res-dec-cls-2",
        bank_item=b_cls_2,
        status=ResidualBankClassificationStatus.CLASSIFIED,
        account_code="6125",
        confidence=0.98,
        rationale="Office supplies pattern matched",
    )

    dec_cls_3 = _make_decision(
        decision_id="res-dec-cls-3",
        bank_item=b_cls_3,
        status=ResidualBankClassificationStatus.CLASSIFIED,
        account_code="6181",
        confidence=0.99,
        rationale="IT services pattern matched",
    )

    # 3 Residual postings for the 3 CLASSIFIED decisions
    now = datetime(2026, 9, 15, 12, 0, 0, tzinfo=timezone.utc)
    post_1 = ResidualBankPosting(
        id="post-1",
        decision_id=dec_cls_1.id,
        staged_transaction_id=f"staged:{b_cls_1.id}",
        bank_item_id=b_cls_1.id,
        journal_entry_id="je-101",
        bank_cash_transaction_id="tx-cash-1",
        contra_transaction_id="tx-contra-1",
        reconciliation_id="rec-post-1",
        persistence_revision=2,
        posted_at=now,
    )
    post_2 = ResidualBankPosting(
        id="post-2",
        decision_id=dec_cls_2.id,
        staged_transaction_id=f"staged:{b_cls_2.id}",
        bank_item_id=b_cls_2.id,
        journal_entry_id="je-102",
        bank_cash_transaction_id="tx-cash-2",
        contra_transaction_id="tx-contra-2",
        reconciliation_id="rec-post-2",
        persistence_revision=2,
        posted_at=now,
    )
    post_3 = ResidualBankPosting(
        id="post-3",
        decision_id=dec_cls_3.id,
        staged_transaction_id=f"staged:{b_cls_3.id}",
        bank_item_id=b_cls_3.id,
        journal_entry_id="je-103",
        bank_cash_transaction_id="tx-cash-3",
        contra_transaction_id="tx-contra-3",
        reconciliation_id="rec-post-3",
        persistence_revision=2,
        posted_at=now,
    )

    # 3 Contra Book items for the 3 reconciled residual postings
    book_1 = BookItem(
        id="book-contra-1",
        origin_period="2026-09",
        date=date(2026, 9, 11),
        amount_units="1500000",
        direction=Direction.BOOK_BANK_CREDIT,
        currency="MAD",
        description="Contra: FRAIS BANCAIRES",
        source_type=SourceType.POSTED_BOOK_ITEM,
    )
    book_2 = BookItem(
        id="book-contra-2",
        origin_period="2026-09",
        date=date(2026, 9, 12),
        amount_units="34000000",
        direction=Direction.BOOK_BANK_CREDIT,
        currency="MAD",
        description="Contra: FOURNITURES BUREAU",
        source_type=SourceType.POSTED_BOOK_ITEM,
    )
    book_3 = BookItem(
        id="book-contra-3",
        origin_period="2026-09",
        date=date(2026, 9, 13),
        amount_units="100000000",
        direction=Direction.BOOK_BANK_CREDIT,
        currency="MAD",
        description="Contra: PRESTATION TECHNIQUE",
        source_type=SourceType.POSTED_BOOK_ITEM,
    )

    rec_1 = Reconciliation(
        id="rec-post-1",
        bank_allocations=(BankAllocation(bank_item_id=b_cls_1.id, amount_units=b_cls_1.amount_units),),
        book_allocations=(BookAllocation(book_item_id=book_1.id, amount_units=book_1.amount_units),),
        state_revision_at_creation=1,
        created_at=now,
    )
    rec_2 = Reconciliation(
        id="rec-post-2",
        bank_allocations=(BankAllocation(bank_item_id=b_cls_2.id, amount_units=b_cls_2.amount_units),),
        book_allocations=(BookAllocation(book_item_id=book_2.id, amount_units=book_2.amount_units),),
        state_revision_at_creation=1,
        created_at=now,
    )
    rec_3 = Reconciliation(
        id="rec-post-3",
        bank_allocations=(BankAllocation(bank_item_id=b_cls_3.id, amount_units=b_cls_3.amount_units),),
        book_allocations=(BookAllocation(book_item_id=book_3.id, amount_units=book_3.amount_units),),
        state_revision_at_creation=1,
        created_at=now,
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
        bank_items=(b_hold, b_cls_1, b_cls_2, b_cls_3),
        book_items=(book_1, book_2, book_3),
        reconciliations=(rec_1, rec_2, rec_3),
        classifications=(),  # 0 legacy classifications
        residual_bank_classifications=(dec_hold, dec_cls_1, dec_cls_2, dec_cls_3),
        residual_bank_postings=(post_1, post_2, post_3),
    )

    class SingleSnapshotRepository(BookkeepingRepository):
        def __init__(self, snap: BookkeepingSnapshot) -> None:
            self._snap = snap

        def load_snapshot(self, *, company_id: str) -> BookkeepingSnapshot:
            return self._snap

        def commit(
            self,
            *,
            company_id: str,
            expected_revision: int,
            write_set: PersistenceWriteSet,
        ) -> PersistenceCommitResult:
            raise NotImplementedError()

    repo = SingleSnapshotRepository(snapshot)
    wb = BookkeepingWorkbench(
        repository=repo,
        company_id=company_id,
        semantic_provider="deterministic",
    )
    return wb


# -----------------------------------------------------------------------------
# 2. State Summary Distinguishes Legacy vs Residual & Active HOLD Count
# -----------------------------------------------------------------------------


def test_get_state_exposes_residual_decisions_and_holds(
    production_like_workbench: BookkeepingWorkbench,
) -> None:
    """Verify get_state exposes explicit legacy vs residual classification counts and active HOLD count."""
    st = execute_operator_tool(production_like_workbench, "get_state", {})
    assert st["status"] == "ok"
    res = st["result"]

    assert res["legacy_book_classifications_count"] == 0
    assert res["residual_bank_classifications_count"] == 4
    assert res["residual_classified_count"] == 3
    assert res["residual_hold_count"] == 1
    assert res["active_residual_holds_count"] == 1
    assert res["residual_bank_postings_count"] == 3
    assert res["unresolved_bank_items_count"] == 1


# -----------------------------------------------------------------------------
# 3. Operator Tools Expose 4 Residual Decisions, 3 Postings, 1 Active HOLD
# -----------------------------------------------------------------------------


def test_list_classifications_exposes_residual_bank_decisions(
    production_like_workbench: BookkeepingWorkbench,
) -> None:
    """Verify list_classifications returns both legacy and residual bank classifications with full metadata."""
    cls_res = execute_operator_tool(production_like_workbench, "list_classifications", {})
    assert cls_res["status"] == "ok"
    items = cls_res["result"]

    assert len(items) == 4
    for it in items:
        assert it["classification_type"] == "RESIDUAL_BANK"
        assert "amount_display" in it
        assert "remaining_amount" in it

    hold_item = next(it for it in items if it["status"] == "HOLD")
    assert hold_item["bank_item_id"] == "staged:bank-hold-1"
    assert hold_item["hold_reason"] == "Ambiguous counterparty and missing contract documentation"
    assert hold_item["amount_display"] == "7,850.00 MAD"
    assert hold_item["posting_exists"] is False
    assert "PROOF_OF_PAYMENT" in hold_item["required_evidence"]

    classified_items = [it for it in items if it["status"] == "CLASSIFIED"]
    assert len(classified_items) == 3
    for it in classified_items:
        assert it["posting_exists"] is True
        assert it["account_code"] in ("6147", "6125", "6181")


def test_get_holds_exposes_active_residual_hold(
    production_like_workbench: BookkeepingWorkbench,
) -> None:
    """Verify get_holds returns active residual bank classification HOLD decisions with reason, evidence, and formatted amount."""
    holds_res = execute_operator_tool(production_like_workbench, "get_holds", {})
    assert holds_res["status"] == "ok"
    holds = holds_res["result"]

    assert len(holds) == 1
    h = holds[0]
    assert h["hold_type"] == "RESIDUAL_BANK_HOLD"
    assert h["bank_item_id"] == "staged:bank-hold-1"
    assert h["hold_reason"] == "Ambiguous counterparty and missing contract documentation"
    assert h["amount_display"] == "7,850.00 MAD"
    assert "PROOF_OF_PAYMENT" in h["required_evidence"]
    assert "VENDOR_CONTRACT" in h["required_evidence"]


# -----------------------------------------------------------------------------
# 4. Tool-Level Grounding for "Classification Issues?"
# -----------------------------------------------------------------------------


def test_list_unresolved_grounds_classification_issues_in_active_hold(
    production_like_workbench: BookkeepingWorkbench,
) -> None:
    """
    Verify list_unresolved surfaces the unresolved bank item as HOLD with reason,
    evidence, and formatted currency amount, not merely generic 'UNMATCHED'.
    """
    unres_res = execute_operator_tool(production_like_workbench, "list_unresolved", {})
    assert unres_res["status"] == "ok"
    unres = unres_res["result"]

    assert unres["unreconciled_bank_items_count"] == 1
    assert unres["active_residual_holds_count"] == 1
    assert len(unres["active_residual_holds"]) == 1

    bank_item = unres["bank_items"][0]
    assert bank_item["bank_item_id"] == "staged:bank-hold-1"
    assert bank_item["is_hold"] is True
    assert bank_item["hold_reason"] == "Ambiguous counterparty and missing contract documentation"
    assert "HOLD:" in bank_item["reason"]
    assert bank_item["amount_display"] == "7,850.00 MAD"
    assert bank_item["remaining_amount_display"] == "7,850.00 MAD"
    assert "PROOF_OF_PAYMENT" in bank_item["required_evidence"]


# -----------------------------------------------------------------------------
# 5. REPL Commands Render Explicit Semantic Sections & Human-Readable Money
# -----------------------------------------------------------------------------


def test_lab_repl_commands_output(
    production_like_workbench: BookkeepingWorkbench,
) -> None:
    """Verify lab REPL commands print properly formatted money and explicit state sections."""
    lab = BookkeepingLab(
        scenario_name=None,
        operator=False,
    )
    lab.repository = production_like_workbench.repository
    lab.company_id = production_like_workbench.company_id
    lab.state = production_like_workbench.state
    lab.workbench = production_like_workbench
    try:
        # 1. state
        buf = io.StringIO()
        with redirect_stdout(buf):
            lab.do_state("")
        out_state = buf.getvalue()
        assert "Legacy book classifications: 0" in out_state
        assert "Residual bank classifications: 4" in out_state
        assert "Residual bank postings: 3" in out_state
        assert "Active residual HOLDs: 1" in out_state
        assert "Unresolved bank items: 1" in out_state

        # 2. bank
        buf = io.StringIO()
        with redirect_stdout(buf):
            lab.do_bank("")
        out_bank = buf.getvalue()
        assert "7,850.00 MAD" in out_bank
        assert "150.00 MAD" in out_bank
        assert "3,400.00 MAD" in out_bank
        assert "10,000.00 MAD" in out_bank
        assert "78,500,000" not in out_bank  # raw units never mislabeled as currency

        # 3. holds
        buf = io.StringIO()
        with redirect_stdout(buf):
            lab.do_holds("")
        out_holds = buf.getvalue()
        assert "Residual Bank Classification Holds (1):" in out_holds
        assert "7,850.00 MAD" in out_holds
        assert "Ambiguous counterparty and missing contract documentation" in out_holds
        assert "PROOF_OF_PAYMENT, VENDOR_CONTRACT" in out_holds

        # 4. residuals
        buf = io.StringIO()
        with redirect_stdout(buf):
            lab.do_residuals("")
        out_residuals = buf.getvalue()
        assert "Residual Decisions (4):" in out_residuals
        assert "[HOLD] Item staged:bank-hold-1 (7,850.00 MAD)" in out_residuals
        assert "NO POSTING (HOLD)" in out_residuals
        assert "[CLASSIFIED] Item staged:bank-cls-1 (150.00 MAD)" in out_residuals
        assert "POSTED (je-101)" in out_residuals

        # 5. unresolved
        buf = io.StringIO()
        with redirect_stdout(buf):
            lab.do_unresolved("")
        out_unres = buf.getvalue()
        assert "remaining 7,850.00 MAD / 7,850.00 MAD" in out_unres
        assert "HOLD: Ambiguous counterparty" in out_unres
        assert "required evidence: PROOF_OF_PAYMENT, VENDOR_CONTRACT" in out_unres

        # 6. classifications
        buf = io.StringIO()
        with redirect_stdout(buf):
            lab.do_classifications("")
        out_cls = buf.getvalue()
        assert "No active legacy book classifications" in out_cls
        assert "Found 4 residual bank classifications" in out_cls

    finally:
        lab.do_quit("")
