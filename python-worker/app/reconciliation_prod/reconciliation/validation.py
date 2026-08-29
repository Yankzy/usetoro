"""
Deterministic Pre-solve Validation.

This module validates the `OptimizerRequest` before it is passed to the CP-SAT
engine, ensuring that hypotheses do not violate structural accounting invariants.
"""
from .protocol import OptimizerRequest, OptimizerResponse
from reconciliation_prod.domain.base import Direction

# Section 15: Explicit compatibility mapping
# Adjust depending on the precise direction enums used by the core systems.
DIRECTION_COMPATIBILITY_MAP = {
    (Direction.OUTFLOW, Direction.OUTFLOW),
    (Direction.INFLOW, Direction.INFLOW),
    (Direction.BANK_OUTFLOW, Direction.BOOK_BANK_CREDIT),
    (Direction.BANK_INFLOW, Direction.BOOK_BANK_DEBIT)
}

def validate_optimizer_request(req: OptimizerRequest) -> OptimizerResponse:
    """
    Performs deterministic pre-solve validation to verify hypothesis legality.
    
    Checks for:
    - Unique Canonical IDs
    - Unknown Object References (Referential Integrity)
    - Internal Monetary Conservation (Bank amounts == Book amounts)
    - Currency Compatibility
    - Direction Compatibility (Using DIRECTION_COMPATIBILITY_MAP)
    - Complete Bank Consumption (No partial bank matching allowed)
    - Pre-solve Book Capacity limits
    
    Args:
        req (OptimizerRequest): The incoming request payload.
        
    Returns:
        OptimizerResponse: An INVALID_INPUT response containing a list of 
                           diagnostics if validation fails. Otherwise, returns
                           a FEASIBLE pseudo-status to proceed.
    """
    diagnostics = []

    # 1. Unique Canonical IDs
    bank_ids = [b.id for b in req.bank_items]
    book_ids = [j.id for j in req.book_items]
    hyp_ids = [h.id for h in req.hypotheses]
    
    if len(bank_ids) != len(set(bank_ids)):
        diagnostics.append("DUPLICATE_BANK_IDS")
    if len(book_ids) != len(set(book_ids)):
        diagnostics.append("DUPLICATE_BOOK_IDS")
    if len(hyp_ids) != len(set(hyp_ids)):
        diagnostics.append("DUPLICATE_HYPOTHESIS_IDS")

    # Fast lookups
    bank_map = {b.id: b for b in req.bank_items}
    book_map = {j.id: j for j in req.book_items}

    # 2. Hypothesis Validations
    for hyp in req.hypotheses:
        # A. Canonical Reference Existence (Section 16)
        missing_banks = [a.bank_item_id for a in hyp.bank_allocations if a.bank_item_id not in bank_map]
        missing_books = [a.book_item_id for a in hyp.book_allocations if a.book_item_id not in book_map]
        
        if missing_banks or missing_books:
            diagnostics.append(f"UNKNOWN_OBJECT_REFERENCE in hypothesis {hyp.id}: Banks={missing_banks}, Books={missing_books}")
            continue # Skip further checks for this hypothesis to avoid KeyErrors

        # B. Internal Monetary Conservation (Section 9)
        if hyp.total_bank_allocation_int != hyp.total_book_allocation_int:
            diagnostics.append(f"IMBALANCED_HYPOTHESIS in {hyp.id}: Bank={hyp.total_bank_allocation_int}, Book={hyp.total_book_allocation_int}")

        # Extract referenced objects for deeper checks
        ref_banks = [bank_map[a.bank_item_id] for a in hyp.bank_allocations]
        ref_books = [book_map[a.book_item_id] for a in hyp.book_allocations]

        # C. Currency Compatibility (Section 14)
        currencies = {item.currency for item in ref_banks + ref_books}
        if len(currencies) > 1:
            diagnostics.append(f"CURRENCY_MISMATCH in hypothesis {hyp.id}: {currencies}")

        # D. Direction Compatibility (Section 15)
        for bank_item in ref_banks:
            for book_item in ref_books:
                pair = (bank_item.direction, book_item.direction)
                if pair not in DIRECTION_COMPATIBILITY_MAP:
                    diagnostics.append(f"DIRECTION_INCOMPATIBILITY in {hyp.id}: {pair}")

        # E. Bank Complete Consumption (Section 7)
        for alloc in hyp.bank_allocations:
            bank_item = bank_map[alloc.bank_item_id]
            if alloc.amount_int != bank_item.amount_int:
                diagnostics.append(f"PARTIAL_BANK_CONSUMPTION in {hyp.id}: Bank {bank_item.id} capacity {bank_item.amount_int} != alloc {alloc.amount_int}")

        # F. Book Capacity per Hypothesis (Section 12 - Pre-solve subset)
        for alloc in hyp.book_allocations:
            book_item = book_map[alloc.book_item_id]
            if alloc.amount_int > book_item.remaining_amount_int:
                diagnostics.append(f"BOOK_CAPACITY_OVERFLOW in {hyp.id}: Book {book_item.id} capacity {book_item.remaining_amount_int} < alloc {alloc.amount_int}")

    # 3. Forced Constraints Validation (Section 22)
    for f_id in req.forced_hypothesis_ids:
        if f_id not in set(hyp_ids):
            diagnostics.append(f"UNKNOWN_FORCED_HYPOTHESIS: {f_id}")

    if diagnostics:
        return OptimizerResponse(
            status="INVALID_INPUT",
            diagnostics=diagnostics
        )

    # Return a pseudo-status to indicate validation passed
    return OptimizerResponse(status="FEASIBLE", diagnostics=["VALIDATION_PASSED"])