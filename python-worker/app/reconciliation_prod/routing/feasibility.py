import itertools
from typing import Dict, List, Set
from pydantic import BaseModel, Field

from reconciliation_prod.domain.bank import BankItem
from reconciliation_prod.domain.book import BookItem
from reconciliation_prod.reconciliation.validation import DIRECTION_COMPATIBILITY_MAP


class AccountFeasibility(BaseModel):
    """The binary feasibility output F_{i,a} for a specific account."""
    account_id: str
    is_feasible: bool
    plausible_bank_item_ids: List[str] = Field(default_factory=list)


class RoutingScore(BaseModel):
    """The semantic utility score assigned to a feasible account."""
    account_id: str
    utility_score: int = Field(ge=0, le=1000)
    semantic_rationale: str


class InvoiceRoutingNode(BaseModel):
    """The complete routing matrix row for a single invoice i."""
    book_item_id: str
    amount_units: int
    direction: str
    counterparty_id: str | None = None
    reference: str | None = None
    feasibility_matrix: List[AccountFeasibility] = Field(default_factory=list)
    semantic_scores: List[RoutingScore] = Field(default_factory=list)


def _can_form_subset_sum(
    target: int,
    candidate_amounts: List[int]
) -> bool:
    """
    Standard DP Subset-Sum solver.
    Returns True if any non-empty subset of candidate_amounts sums to target.
    """
    if target <= 0 or not candidate_amounts:
        return False

    dp = {0}
    for amt in candidate_amounts:
        if amt > target:
            continue
        new_sums = {s + amt for s in dp if s + amt <= target}
        dp.update(new_sums)
        if target in dp:
            return True

    return False



def _find_plausible_bank_ids(
    book_item: BookItem,
    bank_items: List[BankItem],
    max_combo_size: int = 3
) -> List[str]:
    """
    Finds all bank item IDs in a specific account that could mathematically 
    contribute to fulfilling the book item's remaining capacity.
    
    Handles:
      1. Direct 1-to-1 matches or partial bank payments (Bank <= Book Target).
      2. Grouped bank movements where sum(Bank_Items) == Book Target.
      3. Consolidated bank transfers where Bank > Book Target (One-to-Many potential).
    """
    target = book_item.remaining_amount_int
    compatible_bank_items: List[BankItem] = [
        b for b in bank_items 
        if (b.direction, book_item.direction) in DIRECTION_COMPATIBILITY_MAP
    ]

    if not compatible_bank_items:
        return []

    plausible_ids: Set[str] = set()

    # 1. Direct 1-to-1 matches, partial payments, or consolidated transfers
    for b in compatible_bank_items:
        b_amt = b.amount_int
        
        # Case A: Bank line is equal to or smaller than book target (1-to-1 or partial)
        if b_amt <= target:
            plausible_ids.add(b.id)
            
        # Case B: Bank line is larger than book target (Consolidated payment covering multiple items)
        elif b_amt > target:
            plausible_ids.add(b.id)

    # 2. Grouped matches (Multiple Bank items sum up to 1 Book item)
    # Filter for bank items <= target to test subset sums without exploding search space
    valid_amounts = [b.amount_int for b in compatible_bank_items if b.amount_int <= target]
    
    if len(valid_amounts) > 1 and _can_form_subset_sum(target, valid_amounts):
        # Identify exact combinations that form the sum
        for r in range(2, min(max_combo_size + 1, len(compatible_bank_items) + 1)):
            for combo in itertools.combinations(compatible_bank_items, r):
                if sum(item.amount_int for item in combo) == target:
                    for item in combo:
                        plausible_ids.add(item.id)

    return sorted(list(plausible_ids))

def build_feasibility_matrix(
    book_items: List[BookItem],
    accounts_bank_items: Dict[str, List[BankItem]]
) -> List[InvoiceRoutingNode]:
    """
    Phase 1 Core Function:
    Calculates the feasibility matrix F_{i,a} across all accounts for every book item.
    
    :param book_items: List of canonical book items (e.g., invoices).
    :param accounts_bank_items: Dictionary mapping account_id -> List[BankItem].
    :return: List of InvoiceRoutingNode objects containing F_{i,a}.
    """
    routing_nodes: List[InvoiceRoutingNode] = []

    for book in book_items:
        node = InvoiceRoutingNode(
            book_item_id=book.id,
            amount_units=book.remaining_amount_int,
            direction=book.direction.value if hasattr(book.direction, "value") else str(book.direction),
            counterparty_id=book.counterparty_id,
            reference=book.reference,
            feasibility_matrix=[]
        )

        for account_id, bank_items in accounts_bank_items.items():
            plausible_ids = _find_plausible_bank_ids(book, bank_items)
            is_feasible = len(plausible_ids) > 0

            node.feasibility_matrix.append(
                AccountFeasibility(
                    account_id=account_id,
                    is_feasible=is_feasible,
                    plausible_bank_item_ids=plausible_ids
                )
            )

        routing_nodes.append(node)

    return routing_nodes
