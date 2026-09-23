from __future__ import annotations

from datetime import date
from typing import Annotated, Sequence

from pydantic import (
    BaseModel,
    ConfigDict,
    Field,
    StringConstraints,
    field_validator,
    model_validator,
)

from bookkeeping_state.domain.bank import BankItem
from bookkeeping_state.domain.enums import Direction
from bookkeeping_state.state.queries import BookkeepingQueries

Identifier = Annotated[
    str,
    StringConstraints(
        min_length=1,
        strip_whitespace=True,
    ),
]


class ResidualBankCategorizationItem(BaseModel):
    """
    Immutable bounded BankItem projection for semantic account categorization.

    Represents an authoritative bank movement that was neither consumed by Stage 1
    payment application nor fully reconciled in Stage 2.
    """

    model_config = ConfigDict(
        frozen=True,
        extra="forbid",
    )

    bank_item_id: Identifier
    bank_account_id: Identifier
    residual_amount_units: int = Field(
        ...,
        gt=0,
        description="Current residual unmatched amount in exact solver units (10,000 units = 1.0000 currency unit).",
    )
    original_amount_units: int = Field(
        ...,
        gt=0,
        description="Original observed bank statement movement amount in exact solver units.",
    )
    direction: Direction
    date: date
    currency: str = Field(..., min_length=3, max_length=3)
    description: str = Field(
        ...,
        min_length=1,
        description="Raw or normalized bank statement description.",
    )
    reference: str | None = Field(
        default=None,
        description="Extracted payment/bank reference if available.",
    )
    provenance_refs: tuple[Identifier, ...] = Field(
        default_factory=tuple,
        description="Authoritative source artifact IDs establishing this observation.",
    )
    bank_account_name: str | None = Field(
        default=None,
        description="Optional human-readable name of the bank account.",
    )
    institution_name: str | None = Field(
        default=None,
        description="Optional bank/institution name (e.g. Chase, Attijariwafa).",
    )
    counterparty_name: str | None = Field(
        default=None,
        description="Optional extracted or resolved counterparty name if available.",
    )

    @property
    def amount_units(self) -> int:
        """Alias for residual_amount_units for compatibility with downstream consumers."""
        return self.residual_amount_units

    @property
    def residual_amount_int(self) -> int:
        return self.residual_amount_units

    @property
    def original_amount_int(self) -> int:
        return self.original_amount_units

    @field_validator("currency")
    @classmethod
    def normalize_currency(cls, value: str) -> str:
        return value.upper()

    @model_validator(mode="after")
    def validate_production_bank_item(self) -> "ResidualBankCategorizationItem":
        if not self.bank_item_id.startswith("staged:"):
            raise ValueError(
                f"ResidualBankCategorizationItem requires authoritative 'staged:<uuid>' ID, got {self.bank_item_id!r}"
            )
        if self.residual_amount_units <= 0:
            raise ValueError(
                f"ResidualBankCategorizationItem requires positive residual_amount_units, got {self.residual_amount_units}"
            )
        if self.residual_amount_units > self.original_amount_units:
            raise ValueError(
                f"ResidualBankCategorizationItem residual ({self.residual_amount_units}) exceeds original ({self.original_amount_units})"
            )
        return self


class ResidualBankCategorizationView(BaseModel):
    """
    Immutable bounded view representing residual unmatched bank movements
    ready for semantic account categorization.
    """

    model_config = ConfigDict(
        frozen=True,
        extra="forbid",
    )

    session_id: Identifier
    state_revision: int = Field(..., ge=0)
    company_id: Identifier
    persistence_revision: int = Field(default=0, ge=0)
    items: tuple[ResidualBankCategorizationItem, ...] = ()

    @property
    def bank_items(self) -> tuple[ResidualBankCategorizationItem, ...]:
        return self.items

    @property
    def item_count(self) -> int:
        return len(self.items)

    def get_item(self, bank_item_id: str) -> ResidualBankCategorizationItem | None:
        for item in self.items:
            if item.bank_item_id == bank_item_id:
                return item
        return None


def build_residual_bank_categorization_view(
    queries: BookkeepingQueries,
    *,
    candidate_items: Sequence[tuple[BankItem, int]] | None = None,
) -> ResidualBankCategorizationView:
    """
    Construct an immutable bounded ResidualBankCategorizationView.

    Sources candidate bank movements from:
        candidate_items if explicitly supplied, otherwise queries.residual_unmatched_bank_items()

    Preserves deterministic ordering and excludes any
    Stage-1-consumed or Stage-2-reconciled items.
    """
    source_items = (
        candidate_items
        if candidate_items is not None
        else queries.residual_unmatched_bank_items()
    )
    items: list[ResidualBankCategorizationItem] = []

    for bank_item, remaining_units in source_items:
        ba = queries.bank_account(bank_item.bank_account_id)
        ba_name = ba.name if ba is not None else None
        inst_name = ba.institution_name if ba is not None else None

        items.append(
            ResidualBankCategorizationItem(
                bank_item_id=bank_item.id,
                bank_account_id=bank_item.bank_account_id,
                residual_amount_units=remaining_units,
                original_amount_units=bank_item.amount_int,
                direction=bank_item.direction,
                date=bank_item.date,
                currency=bank_item.currency,
                description=bank_item.description,
                reference=bank_item.reference,
                provenance_refs=bank_item.provenance_refs,
                bank_account_name=ba_name,
                institution_name=inst_name,
                counterparty_name=None,
            )
        )

    return ResidualBankCategorizationView(
        session_id=queries.session_id,
        state_revision=queries.state_revision,
        company_id=queries.company_id,
        persistence_revision=queries.persistence_revision,
        items=tuple(items),
    )
