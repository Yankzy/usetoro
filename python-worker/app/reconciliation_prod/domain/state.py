from typing import List
from pydantic import BaseModel, Field
from reconciliation_prod.domain.base import ResidualAmountUnits
from reconciliation_prod.domain.hypothesis import BankAllocation, BookAllocation

class ExpectedResidual(BaseModel):
    book_item_id: str
    residual_amount_units: ResidualAmountUnits

class ProposedMatchGroup(BaseModel):
    """Represents one final chosen reconciliation relationship."""
    group_id: str
    bank_allocations: List[BankAllocation] = Field(..., min_length=1)
    book_allocations: List[BookAllocation] = Field(..., min_length=1)

class ProposedState(BaseModel):
    """The final state produced by the LLM (conceptually via JSON Patch)."""
    matches: List[ProposedMatchGroup] = Field(default_factory=list)
    unresolved_bank_ids: List[str] = Field(default_factory=list)
    expected_book_residuals: List[ExpectedResidual] = Field(default_factory=list)
