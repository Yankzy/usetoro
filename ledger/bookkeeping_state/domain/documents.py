from __future__ import annotations

from datetime import datetime
from enum import StrEnum
from typing import Annotated

from pydantic import BaseModel, ConfigDict, Field, StringConstraints


Identifier = Annotated[
    str,
    StringConstraints(strip_whitespace=True, min_length=1, max_length=255),
]


class DocumentType(StrEnum):
    INVOICE = "INVOICE"
    RECEIPT = "RECEIPT"
    BANK_STATEMENT = "BANK_STATEMENT"
    CREDIT_NOTE = "CREDIT_NOTE"
    DEBIT_NOTE = "DEBIT_NOTE"
    PURCHASE_ORDER = "PURCHASE_ORDER"
    DELIVERY_NOTE = "DELIVERY_NOTE"
    PAYROLL_DOCUMENT = "PAYROLL_DOCUMENT"
    OTHER = "OTHER"


class DocumentStatus(StrEnum):
    RECEIVED = "RECEIVED"
    EXTRACTED = "EXTRACTED"
    VALIDATED = "VALIDATED"
    REJECTED = "REJECTED"


class Document(BaseModel):
    """
    Durable evidence artifact.

    The document stores what physically/logically exists.
    Interpretations extracted from it should become separate artifacts.
    """

    model_config = ConfigDict(
        frozen=True,
        extra="forbid",
    )

    id: Identifier
    document_type: DocumentType
    status: DocumentStatus

    filename: str | None = Field(default=None, max_length=500)
    mime_type: str | None = Field(default=None, max_length=255)

    source_reference: str | None = Field(
        default=None,
        max_length=1000,
        description="External storage/object identifier.",
    )

    content_hash: str | None = Field(
        default=None,
        max_length=128,
    )

    received_at: datetime

    extracted_text: str | None = None

    metadata: dict[str, str] = Field(default_factory=dict)