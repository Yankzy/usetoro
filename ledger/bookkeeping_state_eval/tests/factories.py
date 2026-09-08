from __future__ import annotations

from datetime import date, datetime, timezone

from bookkeeping_state.domain.bank import BankAccount, BankItem
from bookkeeping_state.domain.books import BookItem
from bookkeeping_state.domain.classifications import (
    ClassificationDecision,
    ClassificationInvalidation,
    ClassificationSource,
)
from bookkeeping_state.domain.context import (
    AccountingPolicy,
    BookkeepingContext,
    RuntimeContext,
)
from bookkeeping_state.domain.counterparties import (
    Counterparty,
    CounterpartyType,
)
from bookkeeping_state.domain.documents import (
    Document,
    DocumentStatus,
    DocumentType,
)
from bookkeeping_state.domain.enums import Direction
from bookkeeping_state.domain.reconciliations import (
    BankAllocation,
    BookAllocation,
    Reconciliation,
    ReconciliationInvalidation,
)
from bookkeeping_state.domain.routing import (
    RoutingDecision,
    RoutingDecisionInvalidation,
    RoutingDecisionSource,
)
from bookkeeping_state.persistence.repository import BookkeepingSnapshot
from bookkeeping_state.state.bookkeeping_state import BookkeepingState


FIXED_TIME = datetime(2026, 1, 31, 12, tzinfo=timezone.utc)


def context(**policy_updates: object) -> BookkeepingContext:
    return BookkeepingContext(
        company_id="atlas",
        period_start=date(2026, 1, 1),
        period_end=date(2026, 1, 31),
        base_currency="MAD",
        policy=AccountingPolicy(
            chart_of_accounts_id="pcge",
            **policy_updates,
        ),
    )


def account(id: str = "account-1", *, currency: str = "MAD") -> BankAccount:
    return BankAccount(id=id, name=id, currency=currency)


def bank_item(
    id: str = "bank-1",
    *,
    account_id: str = "account-1",
    amount: int = 1_000_000,
    currency: str = "MAD",
    direction: Direction = Direction.BANK_OUTFLOW,
    provenance_refs: tuple[str, ...] = (),
    reference: str | None = None,
    description: str | None = None,
    date_val: date = date(2026, 1, 15),
) -> BankItem:
    return BankItem(
        id=id,
        bank_account_id=account_id,
        date=date_val,
        amount_units=str(amount),
        direction=direction,
        currency=currency,
        description=description or id,
        reference=reference,
        provenance_refs=provenance_refs,
    )


def book_item(
    id: str = "book-1",
    *,
    amount: int = 1_000_000,
    currency: str = "MAD",
    direction: Direction = Direction.BOOK_BANK_CREDIT,
    counterparty_id: str | None = None,
    provenance_refs: tuple[str, ...] = (),
    description: str | None = None,
    reference: str | None = None,
    date_val: date = date(2026, 1, 15),
) -> BookItem:
    return BookItem(
        id=id,
        origin_period="2026-01",
        date=date_val,
        amount_units=str(amount),
        direction=direction,
        currency=currency,
        description=description,
        reference=reference,
        counterparty_id=counterparty_id,
        provenance_refs=provenance_refs,
    )


def document(id: str = "doc-1") -> Document:
    return Document(
        id=id,
        document_type=DocumentType.INVOICE,
        status=DocumentStatus.VALIDATED,
        source_reference=f"source-{id}",
        received_at=FIXED_TIME,
    )


def counterparty(id: str = "counterparty-1") -> Counterparty:
    return Counterparty(
        id=id,
        name=id,
        counterparty_type=CounterpartyType.SUPPLIER,
    )


def routing(
    id: str = "route-1",
    *,
    book_item_id: str = "book-1",
    account_id: str = "account-1",
    supersedes: str | None = None,
) -> RoutingDecision:
    return RoutingDecision(
        id=id,
        book_item_id=book_item_id,
        bank_account_id=account_id,
        source=RoutingDecisionSource.DETERMINISTIC_RULE,
        supersedes_routing_decision_id=supersedes,
        state_revision_at_creation=0,
        created_at=FIXED_TIME,
    )


def routing_invalidation(
    id: str = "route-invalidation-1", *, target: str = "route-1"
) -> RoutingDecisionInvalidation:
    return RoutingDecisionInvalidation(
        id=id,
        routing_decision_id=target,
        reason="correction",
        state_revision_at_invalidation=0,
        created_at=FIXED_TIME,
    )


def classification(
    id: str = "classification-1",
    *,
    book_item_id: str = "book-1",
    account_code: str = "6111",
    supersedes: str | None = None,
    evidence_refs: tuple[str, ...] = (),
) -> ClassificationDecision:
    return ClassificationDecision(
        id=id,
        book_item_id=book_item_id,
        account_code=account_code,
        source=ClassificationSource.DETERMINISTIC_RULE,
        evidence_refs=evidence_refs,
        supersedes_classification_id=supersedes,
        state_revision_at_decision=0,
        created_at=FIXED_TIME,
    )


def classification_invalidation(
    id: str = "classification-invalidation-1", *, target: str = "classification-1"
) -> ClassificationInvalidation:
    return ClassificationInvalidation(
        id=id,
        classification_id=target,
        reason="correction",
        state_revision_at_invalidation=0,
        created_at=FIXED_TIME,
    )


def reconciliation(
    id: str = "reconciliation-1",
    *,
    bank: tuple[tuple[str, int], ...] = (("bank-1", 1_000_000),),
    book: tuple[tuple[str, int], ...] = (("book-1", 1_000_000),),
    evidence_refs: tuple[str, ...] = (),
) -> Reconciliation:
    return Reconciliation(
        id=id,
        bank_allocations=tuple(
            BankAllocation(bank_item_id=item_id, amount_units=str(amount))
            for item_id, amount in bank
        ),
        book_allocations=tuple(
            BookAllocation(book_item_id=item_id, amount_units=str(amount))
            for item_id, amount in book
        ),
        evidence_refs=evidence_refs,
        state_revision_at_creation=0,
        created_at=FIXED_TIME,
    )


def reconciliation_invalidation(
    id: str = "reconciliation-invalidation-1", *, target: str = "reconciliation-1"
) -> ReconciliationInvalidation:
    return ReconciliationInvalidation(
        id=id,
        reconciliation_id=target,
        reason="correction",
        state_revision_at_invalidation=0,
        created_at=FIXED_TIME,
    )


def state(
    *,
    ctx: BookkeepingContext | None = None,
    revision: int = 0,
    persistence_revision: int = 0,
    session_id: str = "session-a",
    **artifacts: object,
) -> BookkeepingState:
    return BookkeepingState(
        context=ctx or context(),
        runtime=RuntimeContext(
            session_id=session_id,
            state_revision=revision,
            persistence_revision=persistence_revision,
            hydrated_at=FIXED_TIME,
        ),
        **artifacts,
    )


def snapshot(**artifacts: object) -> BookkeepingSnapshot:
    return BookkeepingSnapshot(
        persistence_revision=0,
        context=context(),
        **artifacts,
    )
