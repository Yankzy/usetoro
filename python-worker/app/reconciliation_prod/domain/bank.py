"""
Domain definitions for Bank-side items.

This module provides the `BankItem` class which represents a single, indivisible 
movement of cash on a bank statement (e.g., a deposit or withdrawal).
"""
from datetime import date
from typing import Optional
from pydantic import BaseModel, Field
from .base import Direction, SourceType, AmountUnits

class BankItem(BaseModel):
    """
    Represents a canonical bank statement line item.
    
    A BankItem must always be fully consumed (100% matched) or left completely 
    unresolved. It cannot be partially reconciled according to the engine's rules.
    """
    id: str = Field(..., description="Unique canonical identifier for the bank movement.")
    source_type: SourceType = Field(default=SourceType.BANK_STATEMENT_LINE)
    date: date
    amount_units: AmountUnits = Field(..., description="Absolute reconcilable amount in solver units.")
    direction: Direction
    currency: str = Field(..., min_length=3, max_length=3)
    description: str = Field(..., description="Raw bank statement description.")
    reference: Optional[str] = Field(default=None, description="Extracted reference if available.")

    @property
    def amount_int(self) -> int:
        """
        Integer representation of the amount for solver arithmetic.
        
        Returns:
            int: The amount in integer solver units.
        """
        return int(self.amount_units)