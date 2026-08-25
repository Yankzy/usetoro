from typing import List, Optional
from pydantic import BaseModel, Field
from .base import Eligibility, AmountUnits

class BankAllocation(BaseModel):
    bank_item_id: str
    amount_units: AmountUnits

    @property
    def amount_int(self) -> int:
        return int(self.amount_units)

class BookAllocation(BaseModel):
    book_item_id: str
    amount_units: AmountUnits

    @property
    def amount_int(self) -> int:
        return int(self.amount_units)

class ReconciliationHypothesis(BaseModel):
    id: str
    eligibility: Eligibility = Field(default=Eligibility.SELECTABLE)
    utility: int = Field(
        ..., 
        ge=0, le=1000, 
        description="Relative preference score, not a probability."
    )
    bank_allocations: List[BankAllocation] = Field(..., min_length=1)
    book_allocations: List[BookAllocation] = Field(..., min_length=1)
    evidence_refs: Optional[List[str]] = Field(default_factory=list)
    semantic_rationale: Optional[str] = Field(
        default=None, 
        description="LLM's reasoning for proposing this match."
    )

    @property
    def total_bank_allocation_int(self) -> int:
        return sum(alloc.amount_int for alloc in self.bank_allocations)
        
    @property
    def total_book_allocation_int(self) -> int:
        return sum(alloc.amount_int for alloc in self.book_allocations)