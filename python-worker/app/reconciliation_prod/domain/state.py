"""
Domain definitions for the final output state.

This module defines the structures that represent the finalized result of the 
reconciliation process, including the accepted matches and unresolved items.
"""
from typing import List
from pydantic import BaseModel, Field
from reconciliation_prod.domain.base import ResidualAmountUnits
from reconciliation_prod.domain.hypothesis import BankAllocation, BookAllocation

class ExpectedResidual(BaseModel):
    """
    Represents the remaining unallocated balance of a BookItem after all matches.
    """
    book_item_id: str
    residual_amount_units: ResidualAmountUnits

class ProposedMatchGroup(BaseModel):
    """
    Represents one final, mathematically verified and selected reconciliation relationship.
    
    This is essentially an accepted ReconciliationHypothesis minus the metadata (utility, etc).
    """
    group_id: str
    bank_allocations: List[BankAllocation] = Field(..., min_length=1)
    book_allocations: List[BookAllocation] = Field(..., min_length=1)

class ProposedState(BaseModel):
    """
    The final global state produced by the reconciliation worker.
    
    This object guarantees referential integrity, bank exclusivity, book capacity bounds,
    and internal monetary conservation for all its contents.
    """
    matches: List[ProposedMatchGroup] = Field(default_factory=list)
    unresolved_bank_ids: List[str] = Field(default_factory=list)
    expected_book_residuals: List[ExpectedResidual] = Field(default_factory=list)
