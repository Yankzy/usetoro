from datetime import date
from typing import Optional
from pydantic import BaseModel, Field
from .base import Direction, SourceType, AmountUnits

class BankItem(BaseModel):
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
        return int(self.amount_units)