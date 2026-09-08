from __future__ import annotations

from enum import StrEnum
from typing import Annotated

from pydantic import BaseModel, ConfigDict, Field, StringConstraints


Identifier = Annotated[
    str,
    StringConstraints(strip_whitespace=True, min_length=1, max_length=255),
]


class CounterpartyType(StrEnum):
    CUSTOMER = "CUSTOMER"
    SUPPLIER = "SUPPLIER"
    EMPLOYEE = "EMPLOYEE"
    BANK = "BANK"
    TAX_AUTHORITY = "TAX_AUTHORITY"
    OTHER = "OTHER"


class Counterparty(BaseModel):
    """
    Durable identity artifact representing an external or internal economic party.
    """

    model_config = ConfigDict(
        frozen=True,
        extra="forbid",
    )

    id: Identifier
    name: str = Field(..., min_length=1, max_length=500)
    counterparty_type: CounterpartyType

    tax_id: str | None = Field(default=None, max_length=255)
    external_reference: str | None = Field(default=None, max_length=255)

    aliases: tuple[str, ...] = Field(default_factory=tuple)

    metadata: dict[str, str] = Field(default_factory=dict)