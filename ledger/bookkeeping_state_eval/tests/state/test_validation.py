from __future__ import annotations

import pytest

from bookkeeping_state.domain.enums import Direction
from bookkeeping_state.domain.reconciliations import BankAllocation, BookAllocation, Reconciliation
from bookkeeping_state.state.fingerprint import artifact_fingerprint
from bookkeeping_state.state.validation import (
    StateValidationError,
    ValidationCode,
    validate_state,
)
from tests.factories import (
    FIXED_TIME,
    account,
    bank_item,
    book_item,
    classification,
    classification_invalidation,
    context,
    reconciliation,
    reconciliation_invalidation,
    routing,
    routing_invalidation,
    state,
)


def _codes(live) -> set[ValidationCode]:
    return {issue.code for issue in validate_state(live).errors}


@pytest.mark.parametrize(
    ("live", "expected"),
    [
        (state(bank_items=(bank_item(account_id="missing"),)), ValidationCode.UNKNOWN_BANK_ACCOUNT),
        (state(book_items=(book_item(counterparty_id="missing"),)), ValidationCode.UNKNOWN_COUNTERPARTY),
        (state(routing_decisions=(routing(book_item_id="missing"),)), ValidationCode.UNKNOWN_BOOK_ITEM),
        (state(book_items=(book_item(),), routing_decisions=(routing(account_id="missing"),)), ValidationCode.UNKNOWN_BANK_ACCOUNT),
        (state(book_items=(book_item(),), routing_decisions=(routing(supersedes="missing"),)), ValidationCode.UNKNOWN_ROUTING_SUPERSESSION),
        (state(book_items=(book_item(), book_item("book-2")), routing_decisions=(routing("r1"), routing("r2", book_item_id="book-2", supersedes="r1"))), ValidationCode.ROUTING_SUPERSESSION_SUBJECT_MISMATCH),
        (state(routing_invalidations=(routing_invalidation(target="missing"),)), ValidationCode.UNKNOWN_ROUTING_INVALIDATION_TARGET),
        (state(classifications=(classification(book_item_id="missing"),)), ValidationCode.UNKNOWN_BOOK_ITEM),
        (state(book_items=(book_item(),), classifications=(classification(supersedes="missing"),)), ValidationCode.UNKNOWN_CLASSIFICATION_SUPERSESSION),
        (state(book_items=(book_item(), book_item("book-2")), classifications=(classification("c1"), classification("c2", book_item_id="book-2", supersedes="c1"))), ValidationCode.CLASSIFICATION_SUPERSESSION_SUBJECT_MISMATCH),
        (state(classification_invalidations=(classification_invalidation(target="missing"),)), ValidationCode.UNKNOWN_CLASSIFICATION_INVALIDATION_TARGET),
        (state(book_items=(book_item(),), classifications=(classification(evidence_refs=("missing-doc",)),)), ValidationCode.UNKNOWN_DOCUMENT),
        (state(reconciliations=(reconciliation(bank=(("missing", 1_000_000),)),)), ValidationCode.UNKNOWN_BANK_ITEM),
        (state(bank_accounts=(account(),), bank_items=(bank_item(),), reconciliations=(reconciliation(book=(("missing", 1_000_000),)),)), ValidationCode.UNKNOWN_BOOK_ITEM),
        (state(reconciliation_invalidations=(reconciliation_invalidation(target="missing"),)), ValidationCode.UNKNOWN_RECONCILIATION_INVALIDATION_TARGET),
    ],
)
def test_validation_reports_exact_referential_codes(live, expected: ValidationCode) -> None:
    assert expected in _codes(live)


def test_validation_reports_all_reconciliation_economic_codes() -> None:
    imbalanced = Reconciliation.model_construct(
        id="imbalanced",
        bank_allocations=(BankAllocation(bank_item_id="bank-1", amount_units="1000000"),),
        book_allocations=(BookAllocation(book_item_id="book-1", amount_units="500000"),),
        evidence_refs=(),
        source_hypothesis_id=None,
        semantic_rationale=None,
        session_id=None,
        state_revision_at_creation=0,
        created_at=FIXED_TIME,
    )
    common = dict(bank_accounts=(account(),), bank_items=(bank_item(),))
    assert ValidationCode.RECONCILIATION_MONETARY_IMBALANCE in _codes(
        state(**common, book_items=(book_item(),), reconciliations=(imbalanced,))
    )

    foreign_currency = state(
        bank_accounts=(account(currency="USD"),),
        bank_items=(bank_item(currency="USD"),),
        book_items=(book_item(),),
        reconciliations=(reconciliation(),),
    )
    assert ValidationCode.RECONCILIATION_CURRENCY_MISMATCH in _codes(foreign_currency)

    incompatible_direction = state(
        **common,
        book_items=(book_item(direction=Direction.BOOK_BANK_DEBIT),),
        reconciliations=(reconciliation(),),
    )
    assert ValidationCode.RECONCILIATION_DIRECTION_INCOMPATIBILITY in _codes(incompatible_direction)

    no_partials = context(allow_partial_bank_reconciliation=False, allow_partial_book_reconciliation=False)
    partial = state(
        ctx=no_partials,
        bank_accounts=(account(),),
        bank_items=(bank_item(amount=1_000_000),),
        book_items=(book_item(amount=1_000_000),),
        reconciliations=(reconciliation(bank=(("bank-1", 500_000),), book=(("book-1", 500_000),)),),
    )
    partial_codes = _codes(partial)
    assert ValidationCode.PARTIAL_BANK_CONSUMPTION in partial_codes
    assert ValidationCode.PARTIAL_BOOK_CONSUMPTION in partial_codes

    overallocated = state(
        **common,
        book_items=(book_item(),),
        reconciliations=(reconciliation(bank=(("bank-1", 2_000_000),), book=(("book-1", 2_000_000),)),),
    )
    overflow_codes = _codes(overallocated)
    assert ValidationCode.BANK_CAPACITY_OVERFLOW in overflow_codes
    assert ValidationCode.BOOK_CAPACITY_OVERFLOW in overflow_codes


def test_multiple_active_decisions_are_invalid_and_validation_does_not_mutate() -> None:
    live = state(
        bank_accounts=(account(), account("account-2")),
        book_items=(book_item(),),
        routing_decisions=(routing("route-1"), routing("route-2", account_id="account-2")),
        classifications=(classification("class-1"), classification("class-2", account_code="6122")),
    )
    artifact_before = artifact_fingerprint(live)
    report = validate_state(live)

    assert ValidationCode.MULTIPLE_ACTIVE_DECISIONS in {issue.code for issue in report.errors}
    assert artifact_fingerprint(live) == artifact_before
    # Validation must not add events, hypotheses, or change the runtime revision.
    assert live.revision == 0 and live.events == () and live.reconciliation_hypotheses == {}


def test_valid_and_invalid_reports_have_the_required_raise_behavior() -> None:
    valid = state(bank_accounts=(account(),), bank_items=(bank_item(),), book_items=(book_item(),))
    report = validate_state(valid)
    assert report.is_valid
    assert report.require_valid() is None

    invalid = state(bank_items=(bank_item(account_id="missing"),))
    invalid_report = validate_state(invalid)
    assert not invalid_report.is_valid
    with pytest.raises(StateValidationError) as exc_info:
        invalid_report.require_valid()
    assert exc_info.value.report == invalid_report
