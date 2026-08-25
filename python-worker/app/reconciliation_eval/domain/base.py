from enum import Enum
from typing import Annotated
from pydantic import Field, StringConstraints

# Amounts are strings containing only digits, representing integers > 0.
# 10,000 solver units = 1.0000 MAD.
AmountUnits = Annotated[
    str, 
    StringConstraints(pattern=r"^[1-9][0-9]*$")
]

# A residual can legitimately be zero when a book item is fully consumed.
ResidualAmountUnits = Annotated[
    str,
    StringConstraints(pattern=r"^(0|[1-9][0-9]*)$"),
]

class Direction(str, Enum):
    BANK_INFLOW = "BANK_INFLOW"
    BANK_OUTFLOW = "BANK_OUTFLOW"
    BOOK_BANK_DEBIT = "BOOK_BANK_DEBIT"
    BOOK_BANK_CREDIT = "BOOK_BANK_CREDIT"
    INFLOW = "INFLOW"     # Used in simplified V1 examples
    OUTFLOW = "OUTFLOW"   # Used in simplified V1 examples

class SourceType(str, Enum):
    BANK_STATEMENT_LINE = "BANK_STATEMENT_LINE"
    POSTED_BOOK_ITEM = "POSTED_BOOK_ITEM"
    OPENING_STATE_ITEM = "OPENING_STATE_ITEM"

class Eligibility(str, Enum):
    SELECTABLE = "SELECTABLE"
    COUNTERFACTUAL_ONLY = "COUNTERFACTUAL_ONLY"
