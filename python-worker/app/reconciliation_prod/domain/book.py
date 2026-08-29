"""
Domain definitions for Book-side items.

This module provides the `BookItem` class which represents a canonical accounting 
ledger entry (e.g., an invoice, a bill, or a journal entry line).
"""
from datetime import date
from typing import List, Optional
from pydantic import BaseModel, Field
from .base import Direction, SourceType, AmountUnits

class BookItem(BaseModel):
    """
    Represents a canonical accounting book item.
    
    Unlike BankItems, BookItems can be partially consumed across multiple matches 
    (e.g., a single large invoice paid across two separate bank transfers).
    The engine enforces that the sum of allocations does not exceed `remaining_amount_units`.
    """
    id: str
    source_type: SourceType
    origin_period: str = Field(..., description="YYYY-MM representing when the item was recorded.")
    date: date
    remaining_amount_units: AmountUnits = Field(..., description="Remaining amount available for reconciliation.")
    direction: Direction
    currency: str = Field(..., min_length=3, max_length=3)
    counterparty_id: Optional[str] = None
    reference: Optional[str] = None
    provenance_refs: List[str] = Field(
        default_factory=list, 
        description="IDs of related evidence objects like invoices or cheques."
    )

    @property
    def remaining_amount_int(self) -> int:
        """
        Integer representation of the remaining amount for solver arithmetic.
        
        Returns:
            int: The capacity limit in integer solver units.
        """
        return int(self.remaining_amount_units)