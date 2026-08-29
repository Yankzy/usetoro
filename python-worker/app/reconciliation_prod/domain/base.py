"""
Core types, constants, and enumerations for the reconciliation domain.

This module defines the fundamental scalar types and enumerations used 
across all domain objects in the reconciliation engine to ensure strict 
type safety and string format validation for monetary amounts and directions.
"""
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
    """
    Direction of money flow for both Bank and Book contexts.
    Ensures unambiguous categorization of cash movements.
    """
    BANK_INFLOW = "BANK_INFLOW"
    BANK_OUTFLOW = "BANK_OUTFLOW"
    BOOK_BANK_DEBIT = "BOOK_BANK_DEBIT"
    BOOK_BANK_CREDIT = "BOOK_BANK_CREDIT"
    INFLOW = "INFLOW"     # Used in simplified V1 examples
    OUTFLOW = "OUTFLOW"   # Used in simplified V1 examples

class SourceType(str, Enum):
    """
    The original origin of the entity, used to distinguish canonical sources.
    """
    BANK_STATEMENT_LINE = "BANK_STATEMENT_LINE"
    POSTED_BOOK_ITEM = "POSTED_BOOK_ITEM"
    OPENING_STATE_ITEM = "OPENING_STATE_ITEM"

class Eligibility(str, Enum):
    """
    Defines the role of a hypothesis within the optimizer.
    - SELECTABLE: The optimizer can select this hypothesis to resolve items.
    - COUNTERFACTUAL_ONLY: A known truth used solely to constrain other choices.
    """
    SELECTABLE = "SELECTABLE"
    COUNTERFACTUAL_ONLY = "COUNTERFACTUAL_ONLY"
