from datetime import date
from typing import List, Optional
from pydantic import BaseModel, Field
from .base import Direction, SourceType, AmountUnits

class BookItem(BaseModel):
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
        return int(self.remaining_amount_units)