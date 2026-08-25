from typing import List, Dict, Set, Optional
from pydantic import BaseModel, Field

class ExpectedMatchGroup(BaseModel):
    """Represents a perfectly mapped relationship in ground truth."""
    bank_ids: Set[str]
    book_ids: Set[str]
    
    def matches(self, other_bank_ids: Set[str], other_book_ids: Set[str]) -> bool:
        return self.bank_ids == other_bank_ids and self.book_ids == other_book_ids

class GroundTruth(BaseModel):
    """The authoritative hidden truth for a scenario."""
    matches: List[ExpectedMatchGroup]
    unresolved_bank_ids: Set[str]
    book_residuals: Dict[str, int] = Field(default_factory=dict)