from typing import List, Tuple, Dict
from reconciliation_prod.domain.bank import BankItem
from reconciliation_prod.domain.book import BookItem
from reconciliation_prod.domain.state import ProposedState
from reconciliation_prod.reconciliation.validation import DIRECTION_COMPATIBILITY_MAP

def validate_proposal(
    original_bank_items: List[BankItem],
    original_book_items: List[BookItem],
    proposal: ProposedState
) -> Tuple[bool, List[str]]:
    """
    Independently verifies all accounting invariants of the final proposed state.
    Returns (True, []) if perfectly valid.
    Returns (False, [error_messages]) if any invariant is violated.
    """
    errors = []
    
    # 1. Fast Lookups & Canonical Existence (Section 16 / 42)
    bank_map = {b.id: b for b in original_bank_items}
    book_map = {j.id: j for j in original_book_items}
    
    # Check for hallucinated or missing IDs
    proposed_bank_ids = set()
    for match in proposal.matches:
        for b_alloc in match.bank_allocations:
            if b_alloc.bank_item_id not in bank_map:
                errors.append(f"HALLUCINATED_BANK_ID: {b_alloc.bank_item_id} in match {match.group_id}")
            proposed_bank_ids.add(b_alloc.bank_item_id)
            
    proposed_book_ids = set()
    for match in proposal.matches:
        for j_alloc in match.book_allocations:
            if j_alloc.book_item_id not in book_map:
                errors.append(f"HALLUCINATED_BOOK_ID: {j_alloc.book_item_id} in match {match.group_id}")
            proposed_book_ids.add(j_alloc.book_item_id)
            
    for u_id in proposal.unresolved_bank_ids:
        if u_id not in bank_map:
            errors.append(f"HALLUCINATED_UNRESOLVED_BANK_ID: {u_id}")

    if errors:
        return False, errors  # Stop early if referential integrity is broken

    # 2. Bank Exclusivity & Complete Coverage (Sections 7, 11, 42)
    all_original_bank_ids = set(bank_map.keys())
    unresolved_ids = set(proposal.unresolved_bank_ids)
    
    # Check overlap (a bank item cannot be both matched and unresolved)
    overlap = proposed_bank_ids.intersection(unresolved_ids)
    if overlap:
        errors.append(f"BANK_EXCLUSIVITY_VIOLATION: Items both matched and unresolved: {overlap}")
        
    # Check missing (every bank item must have exactly one disposition)
    missing = all_original_bank_ids - (proposed_bank_ids.union(unresolved_ids))
    if missing:
        errors.append(f"MISSING_BANK_DISPOSITION: Items neither matched nor unresolved: {missing}")

    # Track capacities and consumptions
    book_consumptions: Dict[str, int] = {j.id: 0 for j in original_book_items}
    bank_match_count: Dict[str, int] = {b.id: 0 for b in original_bank_items}

    # 3. Match-level Invariants
    for match in proposal.matches:
        match_bank_sum = 0
        match_book_sum = 0
        match_currencies = set()
        
        # Validate Bank Allocations
        for b_alloc in match.bank_allocations:
            b_id = b_alloc.bank_item_id
            bank_item = bank_map[b_id]
            
            match_bank_sum += b_alloc.amount_int
            bank_match_count[b_id] += 1
            match_currencies.add(bank_item.currency)
            
            # Complete consumption rule (Section 7)
            if b_alloc.amount_int != bank_item.amount_int:
                errors.append(f"PARTIAL_BANK_CONSUMPTION: Match {match.group_id}, Bank {b_id} allocated {b_alloc.amount_int} but requires {bank_item.amount_int}")

        # Validate Book Allocations
        for j_alloc in match.book_allocations:
            j_id = j_alloc.book_item_id
            book_item = book_map[j_id]
            
            match_book_sum += j_alloc.amount_int
            book_consumptions[j_id] += j_alloc.amount_int
            match_currencies.add(book_item.currency)

            # Direction Compatibility (Section 15, 42)
            for b_alloc in match.bank_allocations:
                bank_item = bank_map[b_alloc.bank_item_id]
                pair = (bank_item.direction, book_item.direction)
                if pair not in DIRECTION_COMPATIBILITY_MAP:
                    errors.append(f"DIRECTION_INCOMPATIBILITY: Match {match.group_id}, {pair}")

        # Monetary Conservation (Section 9, 42)
        if match_bank_sum != match_book_sum:
            errors.append(f"MONETARY_IMBALANCE: Match {match.group_id} Bank Sum = {match_bank_sum} != Book Sum = {match_book_sum}")
            
        # Currency Compatibility (Section 14, 42)
        if len(match_currencies) > 1:
            errors.append(f"CURRENCY_MISMATCH: Match {match.group_id} mixes currencies {match_currencies}")

    # 4. Bank Multiple Consumption Check
    multiple_banks = [b_id for b_id, count in bank_match_count.items() if count > 1]
    if multiple_banks:
        errors.append(f"BANK_DOUBLE_CONSUMPTION: {multiple_banks}")

    # 5. Book Capacity Invariant (Section 12, 42)
    for j_id, consumed in book_consumptions.items():
        capacity = book_map[j_id].remaining_amount_int
        if consumed > capacity:
            errors.append(f"BOOK_CAPACITY_OVERFLOW: Item {j_id} capacity {capacity} but consumed {consumed}")

    # 6. LLM Claimed Residuals Check (Section 42 - optional strictness)
    for residual_obj in proposal.expected_book_residuals:
        j_id = residual_obj.book_item_id
        claimed_residual_str = residual_obj.residual_amount_units
        if j_id in book_map:
            actual_residual = book_map[j_id].remaining_amount_int - book_consumptions.get(j_id, 0)
            claimed_residual = int(claimed_residual_str)
            if actual_residual != claimed_residual:
                errors.append(f"RESIDUAL_MISMATCH: Item {j_id} LLM claims {claimed_residual} but math yields {actual_residual}")

    is_valid = len(errors) == 0
    return is_valid, errors