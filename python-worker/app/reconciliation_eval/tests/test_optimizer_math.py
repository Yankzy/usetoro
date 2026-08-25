import pytest
from datetime import date
from domain.base import Direction, SourceType
from domain.bank import BankItem
from domain.books import BookItem
from domain.hypothesis import ReconciliationHypothesis, BankAllocation, BookAllocation
from optimizer.protocol import OptimizerRequest
from optimizer.cp_sat import solve_reconciliation

# ---------------------------------------------------------------------------
# Test Data Helpers
# ---------------------------------------------------------------------------

def make_bank(b_id: str, amount: str) -> BankItem:
    return BankItem(
        id=b_id,
        date=date(2026, 8, 22),
        amount_units=amount,
        direction=Direction.OUTFLOW,
        currency="MAD",
        description=f"Test Bank {b_id}"
    )

def make_book(j_id: str, amount: str) -> BookItem:
    return BookItem(
        id=j_id,
        source_type=SourceType.POSTED_BOOK_ITEM,
        origin_period="2026-08",
        date=date(2026, 8, 20),
        remaining_amount_units=amount,
        direction=Direction.OUTFLOW,
        currency="MAD"
    )

def make_hyp(h_id: str, utility: int, bank_allocs: dict, book_allocs: dict) -> ReconciliationHypothesis:
    banks = [BankAllocation(bank_item_id=k, amount_units=str(v)) for k, v in bank_allocs.items()]
    books = [BookAllocation(book_item_id=k, amount_units=str(v)) for k, v in book_allocs.items()]
    return ReconciliationHypothesis(
        id=h_id,
        utility=utility,
        bank_allocations=banks,
        book_allocations=books
    )

# ---------------------------------------------------------------------------
# Section 63 Required Tests
# ---------------------------------------------------------------------------

def test_exact_one_to_one():
    """B1 = 10,000, A1 = 10,000 -> H1 must be selectable."""
    req = OptimizerRequest(
        problem_id="test_1",
        currency="MAD",
        bank_items=[make_bank("B1", "10000")],
        book_items=[make_book("A1", "10000")],
        hypotheses=[make_hyp("H1", 800, {"B1": 10000}, {"A1": 10000})]
    )
    
    res = solve_reconciliation(req)
    assert res.status == "OPTIMAL"
    assert len(res.selected_hypotheses) == 1
    assert res.selected_hypotheses[0]["hypothesis_id"] == "H1"
    assert res.objective_value == 100000990800


def test_internal_imbalance():
    """B1 = 10,000, A1 = 9,999 -> Hypothesis rejected before solving."""
    req = OptimizerRequest(
        problem_id="test_2",
        currency="MAD",
        bank_items=[make_bank("B1", "10000")],
        book_items=[make_book("A1", "10000")],
        hypotheses=[make_hyp("H1", 800, {"B1": 10000}, {"A1": 9999})]
    )
    
    res = solve_reconciliation(req)
    assert res.status == "INVALID_INPUT"
    assert any("IMBALANCED_HYPOTHESIS" in d for d in res.diagnostics)


def test_double_bank_allocation():
    """Two hypotheses both consume B1. Both cannot be selected."""
    req = OptimizerRequest(
        problem_id="test_3",
        currency="MAD",
        bank_items=[make_bank("B1", "10000")],
        book_items=[make_book("A1", "10000"), make_book("A2", "10000")],
        hypotheses=[
            make_hyp("H1", 900, {"B1": 10000}, {"A1": 10000}),
            make_hyp("H2", 800, {"B1": 10000}, {"A2": 10000})
        ]
    )
    
    res = solve_reconciliation(req)
    assert res.status == "OPTIMAL"
    assert len(res.selected_hypotheses) == 1
    # H1 has higher utility (900 > 800), so it wins the single bank item B1
    assert res.selected_hypotheses[0]["hypothesis_id"] == "H1"
    assert res.objective_value == 100000990900


def test_book_capacity_overflow():
    """A1 = 10,000. H1 consumes 6,000. H2 consumes 5,000. Both cannot be selected."""
    req = OptimizerRequest(
        problem_id="test_4",
        currency="MAD",
        bank_items=[make_bank("B1", "6000"), make_bank("B2", "5000")],
        book_items=[make_book("A1", "10000")],
        hypotheses=[
            make_hyp("H1", 900, {"B1": 6000}, {"A1": 6000}),
            make_hyp("H2", 800, {"B2": 5000}, {"A1": 5000})
        ]
    )
    
    res = solve_reconciliation(req)
    assert res.status == "OPTIMAL"
    assert len(res.selected_hypotheses) == 1
    # Selecting both would consume 11,000 > 10,000. H1 is chosen.
    assert res.selected_hypotheses[0]["hypothesis_id"] == "H1"
    
    # Check that B2 was left unresolved
    unresolved_ids = [u["bank_item_id"] for u in res.unresolved_bank_items]
    assert "B2" in unresolved_ids


def test_partial_book_settlement():
    """A1 = 20,000. H1 consumes 8,000. Residual must equal 12,000."""
    req = OptimizerRequest(
        problem_id="test_5",
        currency="MAD",
        bank_items=[make_bank("B1", "8000")],
        book_items=[make_book("A1", "20000")],
        hypotheses=[make_hyp("H1", 700, {"B1": 8000}, {"A1": 8000})]
    )
    
    res = solve_reconciliation(req)
    assert res.status == "OPTIMAL"
    assert len(res.book_residuals) == 1
    
    residual = res.book_residuals[0]
    assert residual["book_item_id"] == "A1"
    assert residual["consumed_amount_units"] == "8000"
    assert residual["remaining_amount_units"] == "12000"


def test_global_assignment_trap():
    """
    B1 = 10,000, B2 = 10,000
    A1 = 10,000, A2 = 10,000
    
    H1: B1 -> A1 (utility 750)
    H2: B1 -> A2 (utility 650)
    H3: B2 -> A1 (utility 980)
    
    Locally, B1 prefers H1 (750 > 650).
    But if B1 takes H1, A1 is consumed, preventing B2 from taking H3 (980).
    Globally optimal is H2 + H3 = 1630.
    """
    req = OptimizerRequest(
        problem_id="test_6_global_trap",
        currency="MAD",
        bank_items=[make_bank("B1", "10000"), make_bank("B2", "10000")],
        book_items=[make_book("A1", "10000"), make_book("A2", "10000")],
        hypotheses=[
            make_hyp("H1", 750, {"B1": 10000}, {"A1": 10000}),
            make_hyp("H2", 650, {"B1": 10000}, {"A2": 10000}),
            make_hyp("H3", 980, {"B2": 10000}, {"A1": 10000})
        ]
    )
    
    res = solve_reconciliation(req)
    assert res.status == "OPTIMAL"
    
    selected_ids = {h["hypothesis_id"] for h in res.selected_hypotheses}
    assert "H2" in selected_ids
    assert "H3" in selected_ids
    assert "H1" not in selected_ids
    assert res.objective_value == 200001981630


def test_unresolved_bank_item():
    """A bank line with no hypothesis must result in u_b = 1."""
    req = OptimizerRequest(
        problem_id="test_7",
        currency="MAD",
        bank_items=[make_bank("B1", "10000"), make_bank("B2", "5000")],
        book_items=[make_book("A1", "10000")],
        hypotheses=[make_hyp("H1", 800, {"B1": 10000}, {"A1": 10000})]
    )
    
    res = solve_reconciliation(req)
    assert res.status == "OPTIMAL"
    
    # B1 should be matched, B2 unresolved
    assert len(res.selected_hypotheses) == 1
    assert len(res.unresolved_bank_items) == 1
    
    unresolved = res.unresolved_bank_items[0]
    assert unresolved["bank_item_id"] == "B2"
    assert unresolved["reason"] == "NO_CANDIDATES"