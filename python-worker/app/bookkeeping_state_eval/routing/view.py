"""
This file creates the bounded immutable projection that routing is allowed to see.

Routing never receives BookkeepingState itself.
"""

from __future__ import annotations

from datetime import date
from typing import Annotated

from pydantic import (
    BaseModel,
    ConfigDict,
    Field,
    StringConstraints,
    model_validator,
)

from bookkeeping_state_eval.domain.enums import Direction
from bookkeeping_state_eval.domain.money import (
    ResidualAmountUnits,
)
from bookkeeping_state_eval.state.queries import (
    BookkeepingQueries,
)


Identifier = Annotated[
    str,
    StringConstraints(
        min_length=1,
        strip_whitespace=True,
    ),
]

"""
That is an architectural boundary worth enforcing:

BookkeepingState
      |
      v
BookkeepingQueries
      |
      v
RoutingView
      |
      X
Routing cannot reach backward into BookkeepingState

Another deliberate choice here: fully reconciled items disappear from routing capacity. If a BankItem has zero remaining amount, routing should not even see it as selectable economic capacity.

Likewise, already-routed BookItems are excluded. Routing is solving unresolved routing, not repeatedly reconsidering accepted truth unless some explicit invalidation/supersession workflow reopens it.
"""

# ======================================================================
# Bank-side bounded projection
# ======================================================================


class RoutingBankItemView(BaseModel):
    """
    Minimal BankItem information routing is permitted to inspect.

    remaining_amount_units is derived from active reconciliations.

    The original persisted BankItem is never exposed to routing as mutable
    state.
    """

    model_config = ConfigDict(
        frozen=True,
        extra="forbid",
    )

    bank_item_id: Identifier
    bank_account_id: Identifier

    date: date

    remaining_amount_units: ResidualAmountUnits

    direction: Direction

    currency: str = Field(
        ...,
        min_length=3,
        max_length=3,
    )

    description: str | None = None
    reference: str | None = None

    @property
    def remaining_amount_int(self) -> int:
        return int(
            self.remaining_amount_units
        )


class RoutingBankAccountView(BaseModel):
    """
    One bank account plus currently available BankItems.

    Fully consumed BankItems are excluded before routing sees the view.
    """

    model_config = ConfigDict(
        frozen=True,
        extra="forbid",
    )

    bank_account_id: Identifier

    name: str = Field(
        ...,
        min_length=1,
    )

    currency: str = Field(
        ...,
        min_length=3,
        max_length=3,
    )

    institution_name: str | None = None

    bank_items: tuple[
        RoutingBankItemView,
        ...
    ] = ()

    @model_validator(mode="after")
    def validate_bank_items(
        self,
    ) -> "RoutingBankAccountView":
        seen: set[str] = set()

        for item in self.bank_items:
            if item.bank_account_id != self.bank_account_id:
                raise ValueError(
                    f"RoutingBankItemView {item.bank_item_id!r} "
                    f"belongs to {item.bank_account_id!r}, not "
                    f"{self.bank_account_id!r}"
                )

            if item.bank_item_id in seen:
                raise ValueError(
                    f"Duplicate BankItem {item.bank_item_id!r} "
                    "inside RoutingBankAccountView"
                )

            seen.add(
                item.bank_item_id
            )

        return self

    @property
    def available_units_int(self) -> int:
        return sum(
            item.remaining_amount_int
            for item in self.bank_items
        )


# ======================================================================
# Book-side bounded projection
# ======================================================================


class RoutingBookItemView(BaseModel):
    """
    Minimal unresolved BookItem information required by routing.

    Routing receives the remaining economic amount rather than assuming that
    the original BookItem amount is still fully available.

    Counterparty information is intentionally reduced to semantic hints.
    """

    model_config = ConfigDict(
        frozen=True,
        extra="forbid",
    )

    book_item_id: Identifier

    date: date

    remaining_amount_units: ResidualAmountUnits

    direction: Direction

    currency: str = Field(
        ...,
        min_length=3,
        max_length=3,
    )

    description: str | None = None
    reference: str | None = None

    counterparty_id: Identifier | None = None
    counterparty_name: str | None = None
    counterparty_aliases: tuple[str, ...] = ()

    evidence_document_ids: tuple[
        Identifier,
        ...
    ] = ()

    @model_validator(mode="after")
    def validate_book_item_view(
        self,
    ) -> "RoutingBookItemView":
        if self.remaining_amount_int <= 0:
            raise ValueError(
                "RoutingBookItemView must represent positive "
                "remaining capacity"
            )

        if len(set(self.counterparty_aliases)) != len(
            self.counterparty_aliases
        ):
            raise ValueError(
                "counterparty_aliases contains duplicates"
            )

        if len(set(self.evidence_document_ids)) != len(
            self.evidence_document_ids
        ):
            raise ValueError(
                "evidence_document_ids contains duplicates"
            )

        return self

    @property
    def remaining_amount_int(self) -> int:
        return int(
            self.remaining_amount_units
        )


# ======================================================================
# Complete bounded routing world
# ======================================================================


class RoutingView(BaseModel):
    """
    Immutable routing projection generated against one BookkeepingState
    revision.

    This is the ONLY bookkeeping world the routing subsystem should need.

    It deliberately excludes:

        ClassificationDecision history
        Reconciliation artifacts
        runtime hypotheses
        StateEvents
        mutable BookkeepingState
        full Documents
        unrelated company artifacts

    Routing receives only the information necessary to decide which bank
    account an unresolved BookItem plausibly belongs to.
    """

    model_config = ConfigDict(
        frozen=True,
        extra="forbid",
    )

    company_id: Identifier

    state_revision: int = Field(
        ...,
        ge=0,
    )

    persistence_revision: int = Field(
        ...,
        ge=0,
    )

    period_start: date
    period_end: date

    base_currency: str = Field(
        ...,
        min_length=3,
        max_length=3,
    )

    bank_accounts: tuple[
        RoutingBankAccountView,
        ...
    ] = ()

    book_items: tuple[
        RoutingBookItemView,
        ...
    ] = ()

    @model_validator(mode="after")
    def validate_view(
        self,
    ) -> "RoutingView":
        if self.period_end < self.period_start:
            raise ValueError(
                "RoutingView period_end cannot precede period_start"
            )

        account_ids = [
            account.bank_account_id
            for account in self.bank_accounts
        ]

        if len(set(account_ids)) != len(
            account_ids
        ):
            raise ValueError(
                "RoutingView contains duplicate BankAccounts"
            )

        book_item_ids = [
            item.book_item_id
            for item in self.book_items
        ]

        if len(set(book_item_ids)) != len(
            book_item_ids
        ):
            raise ValueError(
                "RoutingView contains duplicate BookItems"
            )

        return self

    def bank_account(
        self,
        bank_account_id: str,
    ) -> RoutingBankAccountView | None:
        for account in self.bank_accounts:
            if (
                account.bank_account_id
                == bank_account_id
            ):
                return account

        return None

    def book_item(
        self,
        book_item_id: str,
    ) -> RoutingBookItemView | None:
        for item in self.book_items:
            if item.book_item_id == book_item_id:
                return item

        return None


# ======================================================================
# Builder
# ======================================================================


def build_routing_view(
    queries: BookkeepingQueries,
) -> RoutingView:
    """
    Build a deterministic immutable projection of the current bookkeeping
    world for routing.

    Eligibility rules:

    BookItem:
        - positive remaining amount
        - no active routing decision

    BankItem:
        - positive remaining amount

    Routing itself will later determine:

        currency compatibility
        direction compatibility
        subset feasibility
        semantic preference
        global account assignment

    Therefore view construction contains no routing reasoning.
    """

    derived = queries.derived

    # ------------------------------------------------------------------
    # Bank accounts
    # ------------------------------------------------------------------

    bank_account_views: list[
        RoutingBankAccountView
    ] = []

    for bank_account in queries.bank_accounts():

        if bank_account is None:
            continue

        bank_item_views: list[
            RoutingBankItemView
        ] = []

        for bank_item in queries.bank_items_for_account(
            bank_account.id
        ):
            remaining = (
                derived.bank_remaining_units[
                    bank_item.id
                ]
            )

            if remaining <= 0:
                continue

            bank_item_views.append(
                RoutingBankItemView(
                    bank_item_id=bank_item.id,
                    bank_account_id=(
                        bank_item.bank_account_id
                    ),
                    date=bank_item.date,
                    remaining_amount_units=str(
                        remaining
                    ),
                    direction=bank_item.direction,
                    currency=bank_item.currency,
                    description=bank_item.description,
                    reference=bank_item.reference,
                )
            )

        bank_item_views.sort(
            key=lambda item: (
                item.date,
                item.bank_item_id,
            )
        )

        bank_account_views.append(
            RoutingBankAccountView(
                bank_account_id=bank_account.id,
                name=bank_account.name,
                currency=bank_account.currency,
                institution_name=(
                    bank_account.institution_name
                ),
                bank_items=tuple(
                    bank_item_views
                ),
            )
        )

    # ------------------------------------------------------------------
    # Unrouted BookItems
    # ------------------------------------------------------------------

    book_item_views: list[
        RoutingBookItemView
    ] = []

    for book_item in queries.unrouted_book_items():
        remaining = (
            derived.book_remaining_units[
                book_item.id
            ]
        )

        if remaining <= 0:
            continue

        counterparty = queries.effective_counterparty(
            book_item.id
        )
        counterparty_id = (
            counterparty.id if counterparty is not None else None
        )
        counterparty_name = (
            counterparty.name if counterparty is not None else None
        )
        counterparty_aliases = (
            tuple(sorted(counterparty.aliases))
            if counterparty is not None
            else ()
        )

        evidence = queries.effective_evidence_documents(
            book_item.id
        )

        book_item_views.append(
            RoutingBookItemView(
                book_item_id=book_item.id,
                date=book_item.date,
                remaining_amount_units=str(
                    remaining
                ),
                direction=book_item.direction,
                currency=book_item.currency,
                description=queries.effective_description(
                    book_item.id
                ),
                reference=queries.effective_reference(
                    book_item.id
                ),
                counterparty_id=counterparty_id,
                counterparty_name=counterparty_name,
                counterparty_aliases=counterparty_aliases,
                evidence_document_ids=tuple(
                    sorted(
                        document.id
                        for document in evidence
                    )
                ),
            )
        )

    book_item_views.sort(
        key=lambda item: (
            item.date,
            item.book_item_id,
        )
    )

    bank_account_views.sort(
        key=lambda account: (
            account.bank_account_id
        )
    )

    context = queries.context

    return RoutingView(
        company_id=context.company_id,
        state_revision=queries.state_revision,
        persistence_revision=(
            queries.persistence_revision
        ),
        period_start=context.period_start,
        period_end=context.period_end,
        base_currency=context.base_currency,
        bank_accounts=tuple(
            bank_account_views
        ),
        book_items=tuple(
            book_item_views
        ),
    )