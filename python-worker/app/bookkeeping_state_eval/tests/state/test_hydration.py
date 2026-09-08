from __future__ import annotations

from datetime import date, datetime, timedelta, timezone

import pytest

from bookkeeping_state_eval.domain.bank import (
    BankAccount,
    BankItem,
)
from bookkeeping_state_eval.domain.books import BookItem
from bookkeeping_state_eval.domain.context import (
    AccountingPolicy,
    BookkeepingContext,
)
from bookkeeping_state_eval.domain.enums import (
    Direction,
    Eligibility,
)
from bookkeeping_state_eval.domain.hypotheses import (
    ReconciliationHypothesis,
)
from bookkeeping_state_eval.domain.reconciliations import (
    BankAllocation,
    BookAllocation,
    Reconciliation,
)
from bookkeeping_state_eval.hydration.hydrator import (
    BookkeepingHydrator,
    InvalidHydratedStateError,
    SnapshotCompanyMismatchError,
)
from bookkeeping_state_eval.persistence.in_memory import (
    InMemoryBookkeepingRepository,
)
from bookkeeping_state_eval.persistence.repository import (
    BookkeepingSnapshot,
)
from bookkeeping_state_eval.state.derived import (
    build_derived_state,
)
from bookkeeping_state_eval.state.fingerprint import (
    artifact_fingerprint,
    state_fingerprint,
    states_are_equivalent,
)
from bookkeeping_state_eval.state.validation import (
    ValidationCode,
    validate_state,
)


FIXED_TIME = datetime(
    2026,
    1,
    31,
    12,
    0,
    0,
    tzinfo=timezone.utc,
)


# ======================================================================
# Basic hydration
# ======================================================================


def test_hydration_creates_fresh_runtime_state_from_durable_snapshot() -> None:
    repository = InMemoryBookkeepingRepository(
        initial_snapshots=[
            _valid_snapshot(),
        ]
    )

    hydrator = BookkeepingHydrator(
        repository=repository,
        clock=lambda: FIXED_TIME,
    )

    state = hydrator.hydrate(
        company_id="atlas-distribution",
        session_id="session-1",
    )

    try:
        # --------------------------------------------------------------
        # Runtime identity
        # --------------------------------------------------------------

        assert state.session_id == "session-1"

        assert state.runtime.hydrated_at == FIXED_TIME

        # Fresh runtime incarnation always starts locally at S0.
        assert state.revision == 0

        # But it knows exactly which durable world created it.
        assert state.persistence_revision == 14

        # --------------------------------------------------------------
        # Durable context
        # --------------------------------------------------------------

        assert (
            state.context.company_id
            == "atlas-distribution"
        )

        assert state.context.base_currency == "MAD"

        # --------------------------------------------------------------
        # Durable artifacts reconstructed
        # --------------------------------------------------------------

        assert set(state.bank_accounts) == {
            "bank-account-1",
        }

        assert set(state.bank_items) == {
            "bank-1",
            "bank-2",
        }

        assert set(state.book_items) == {
            "book-1",
            "book-2",
        }

        assert set(state.reconciliations) == {
            "reconciliation-1",
        }

        # --------------------------------------------------------------
        # Runtime-only structures begin empty
        # --------------------------------------------------------------

        assert (
            len(state.reconciliation_hypotheses)
            == 0
        )

        assert len(state.events) == 0

        # --------------------------------------------------------------
        # Hydrated world is valid
        # --------------------------------------------------------------

        report = validate_state(state)

        assert report.is_valid
        assert report.errors == ()

    finally:
        state.close()


# ======================================================================
# Reconstruction of derived truth
# ======================================================================


def test_hydration_reconstructs_residual_amounts_from_artifacts() -> None:
    """
    The snapshot persists:

        BANK-1 amount = 100
        BOOK-1 amount = 150
        REC-1 allocates 100 between them

    No "remaining amount" is persisted.

    Hydration must reconstruct:

        BANK-1 remaining = 0
        BOOK-1 remaining = 50
    """

    repository = InMemoryBookkeepingRepository(
        initial_snapshots=[
            _valid_snapshot(),
        ]
    )

    state = BookkeepingHydrator(
        repository=repository,
        clock=lambda: FIXED_TIME,
    ).hydrate(
        company_id="atlas-distribution",
        session_id="session-derived",
    )

    try:
        derived = build_derived_state(
            state
        )

        assert (
            state.get_bank_item(
                "bank-1"
            ).amount_int
            == 1_000_000
        )

        assert (
            state.get_book_item(
                "book-1"
            ).amount_int
            == 1_500_000
        )

        assert (
            derived.bank_allocated_units[
                "bank-1"
            ]
            == 1_000_000
        )

        assert (
            derived.bank_remaining_units[
                "bank-1"
            ]
            == 0
        )

        assert (
            derived.book_allocated_units[
                "book-1"
            ]
            == 1_000_000
        )

        assert (
            derived.book_remaining_units[
                "book-1"
            ]
            == 500_000
        )

        assert (
            "bank-1"
            in derived.fully_reconciled_bank_item_ids
        )

        assert (
            "book-1"
            in derived.partially_reconciled_book_item_ids
        )

    finally:
        state.close()


# ======================================================================
# Runtime identity must not affect bookkeeping identity
# ======================================================================


def test_same_durable_world_hydrated_into_different_sessions_is_equivalent() -> None:
    repository = InMemoryBookkeepingRepository(
        initial_snapshots=[
            _valid_snapshot(),
        ]
    )

    times = iter(
        (
            FIXED_TIME,
            FIXED_TIME + timedelta(hours=4),
        )
    )

    hydrator = BookkeepingHydrator(
        repository=repository,
        clock=lambda: next(times),
    )

    first = hydrator.hydrate(
        company_id="atlas-distribution",
        session_id="session-a",
    )

    second = hydrator.hydrate(
        company_id="atlas-distribution",
        session_id="session-b",
    )

    try:
        # --------------------------------------------------------------
        # Runtime identity differs
        # --------------------------------------------------------------

        assert first.session_id == "session-a"
        assert second.session_id == "session-b"

        assert (
            first.runtime.hydrated_at
            != second.runtime.hydrated_at
        )

        # --------------------------------------------------------------
        # Durable origin is identical
        # --------------------------------------------------------------

        assert first.persistence_revision == 14
        assert second.persistence_revision == 14

        assert first.revision == 0
        assert second.revision == 0

        # --------------------------------------------------------------
        # Bookkeeping world is identical
        # --------------------------------------------------------------

        assert (
            artifact_fingerprint(first)
            == artifact_fingerprint(second)
        )

        assert (
            state_fingerprint(first)
            == state_fingerprint(second)
        )

        assert states_are_equivalent(
            first,
            second,
        )

    finally:
        first.close()
        second.close()


# ======================================================================
# Runtime hypotheses are disposable
# ======================================================================


def test_rehydration_does_not_restore_runtime_hypotheses() -> None:
    repository = InMemoryBookkeepingRepository(
        initial_snapshots=[
            _valid_snapshot(),
        ]
    )

    hydrator = BookkeepingHydrator(
        repository=repository,
        clock=lambda: FIXED_TIME,
    )

    first = hydrator.hydrate(
        company_id="atlas-distribution",
        session_id="session-with-hypothesis",
    )

    hypothesis = ReconciliationHypothesis(
        id="runtime-hypothesis-1",

        eligibility=Eligibility.SELECTABLE,

        utility=950,

        bank_allocations=(
            BankAllocation(
                bank_item_id="bank-2",
                amount_units="700000",
            ),
        ),

        book_allocations=(
            BookAllocation(
                book_item_id="book-2",
                amount_units="700000",
            ),
        ),

        semantic_rationale="Runtime-only candidate",

        state_revision=0,

        generated_at=FIXED_TIME,
    )

    first._put_reconciliation_hypothesis(
        hypothesis
    )

    assert (
        first.get_reconciliation_hypothesis(
            "runtime-hypothesis-1"
        )
        == hypothesis
    )

    durable_before = artifact_fingerprint(
        first
    )

    semantic_before = state_fingerprint(
        first
    )

    first.close()

    restored = hydrator.hydrate(
        company_id="atlas-distribution",
        session_id="new-session",
    )

    try:
        # Runtime hypothesis was never durable.
        assert (
            len(restored.reconciliation_hypotheses)
            == 0
        )

        assert (
            restored.get_reconciliation_hypothesis(
                "runtime-hypothesis-1"
            )
            is None
        )

        # Its presence in the old runtime state never affected bookkeeping
        # identity.
        assert (
            artifact_fingerprint(restored)
            == durable_before
        )

        assert (
            state_fingerprint(restored)
            == semantic_before
        )

    finally:
        restored.close()


# ======================================================================
# Invalid durable world
# ======================================================================


def test_invalid_snapshot_is_rejected_before_becoming_operational() -> None:
    repository = InMemoryBookkeepingRepository(
        initial_snapshots=[
            _invalid_snapshot(),
        ]
    )

    hydrator = BookkeepingHydrator(
        repository=repository,
        clock=lambda: FIXED_TIME,
    )

    with pytest.raises(
        InvalidHydratedStateError
    ) as exc_info:
        hydrator.hydrate(
            company_id="broken-company",
            session_id="broken-session",
        )

    report = exc_info.value.report

    assert not report.is_valid

    assert any(
        issue.code
        == ValidationCode.UNKNOWN_BANK_ACCOUNT
        for issue in report.errors
    )

    # Hydration is read-only. Detecting invalid persistence must not mutate it.
    assert (
        repository.current_revision(
            company_id="broken-company"
        )
        == 4
    )


def test_invalid_snapshot_can_be_hydrated_for_diagnostics() -> None:
    repository = InMemoryBookkeepingRepository(
        initial_snapshots=[
            _invalid_snapshot(),
        ]
    )

    hydrator = BookkeepingHydrator(
        repository=repository,
        clock=lambda: FIXED_TIME,
    )

    state = hydrator.hydrate(
        company_id="broken-company",
        session_id="diagnostic-session",
        require_valid=False,
    )

    try:
        report = validate_state(
            state
        )

        assert not report.is_valid

        assert any(
            issue.code
            == ValidationCode.UNKNOWN_BANK_ACCOUNT
            for issue in report.errors
        )

        # Invalid world is inspectable rather than silently repaired.
        broken_item = state.get_bank_item(
            "broken-bank-item"
        )

        assert broken_item is not None

        assert (
            broken_item.bank_account_id
            == "missing-bank-account"
        )

    finally:
        state.close()


def test_validate_snapshot_returns_diagnostics_without_opening_work_session() -> None:
    repository = InMemoryBookkeepingRepository(
        initial_snapshots=[
            _invalid_snapshot(),
        ]
    )

    hydrator = BookkeepingHydrator(
        repository=repository,
        clock=lambda: FIXED_TIME,
    )

    report = hydrator.validate_snapshot(
        company_id="broken-company",
    )

    assert not report.is_valid

    assert any(
        issue.code
        == ValidationCode.UNKNOWN_BANK_ACCOUNT
        for issue in report.errors
    )

    assert (
        repository.current_revision(
            company_id="broken-company"
        )
        == 4
    )


# ======================================================================
# Snapshot identity protection
# ======================================================================


def test_hydrator_rejects_repository_returning_wrong_company() -> None:
    repository = _WrongCompanyRepository(
        snapshot=_valid_snapshot(),
    )

    hydrator = BookkeepingHydrator(
        repository=repository,
        clock=lambda: FIXED_TIME,
    )

    with pytest.raises(
        SnapshotCompanyMismatchError
    ) as exc_info:
        hydrator.hydrate(
            company_id="company-that-was-requested",
            session_id="session-1",
        )

    assert (
        "company-that-was-requested"
        in str(exc_info.value)
    )

    assert (
        "atlas-distribution"
        in str(exc_info.value)
    )


# ======================================================================
# Scenario fixtures
# ======================================================================


def _valid_snapshot() -> BookkeepingSnapshot:
    context = BookkeepingContext(
        company_id="atlas-distribution",

        period_start=date(
            2026,
            1,
            1,
        ),

        period_end=date(
            2026,
            1,
            31,
        ),

        base_currency="MAD",

        policy=AccountingPolicy(
            chart_of_accounts_id="morocco-pcge",

            reconciliation_date_window_days=45,

            require_exact_currency_match=True,

            allow_partial_book_reconciliation=True,

            allow_partial_bank_reconciliation=False,
        ),
    )

    bank_account = BankAccount(
        id="bank-account-1",

        name="Operating Account",

        currency="MAD",

        institution_name="Atlas Bank",
    )

    bank_1 = BankItem(
        id="bank-1",

        bank_account_id="bank-account-1",

        date=date(
            2026,
            1,
            12,
        ),

        amount_units="1000000",

        direction=Direction.BANK_OUTFLOW,

        currency="MAD",

        description="Supplier payment",
    )

    bank_2 = BankItem(
        id="bank-2",

        bank_account_id="bank-account-1",

        date=date(
            2026,
            1,
            18,
        ),

        amount_units="700000",

        direction=Direction.BANK_OUTFLOW,

        currency="MAD",

        description="Office supplies payment",
    )

    book_1 = BookItem(
        id="book-1",

        origin_period="2026-01",

        date=date(
            2026,
            1,
            12,
        ),

        amount_units="1500000",

        direction=Direction.BOOK_BANK_CREDIT,

        currency="MAD",

        description="Supplier payable",
    )

    book_2 = BookItem(
        id="book-2",

        origin_period="2026-01",

        date=date(
            2026,
            1,
            18,
        ),

        amount_units="700000",

        direction=Direction.BOOK_BANK_CREDIT,

        currency="MAD",

        description="Office supplies payable",
    )

    reconciliation = Reconciliation(
        id="reconciliation-1",

        bank_allocations=(
            BankAllocation(
                bank_item_id="bank-1",
                amount_units="1000000",
            ),
        ),

        book_allocations=(
            BookAllocation(
                book_item_id="book-1",
                amount_units="1000000",
            ),
        ),

        semantic_rationale="Seed reconciliation",

        session_id="seed-session",

        state_revision_at_creation=0,

        created_at=FIXED_TIME,
    )

    return BookkeepingSnapshot(
        persistence_revision=14,

        context=context,

        bank_accounts=(
            bank_account,
        ),

        bank_items=(
            bank_1,
            bank_2,
        ),

        book_items=(
            book_1,
            book_2,
        ),

        reconciliations=(
            reconciliation,
        ),
    )


def _invalid_snapshot() -> BookkeepingSnapshot:
    context = BookkeepingContext(
        company_id="broken-company",

        period_start=date(
            2026,
            1,
            1,
        ),

        period_end=date(
            2026,
            1,
            31,
        ),

        base_currency="MAD",

        policy=AccountingPolicy(
            chart_of_accounts_id="morocco-pcge",
            require_exact_currency_match=True,
        ),
    )

    broken_bank_item = BankItem(
        id="broken-bank-item",

        bank_account_id="missing-bank-account",

        date=date(
            2026,
            1,
            10,
        ),

        amount_units="500000",

        direction=Direction.BANK_OUTFLOW,

        currency="MAD",

        description="Broken fixture",
    )

    return BookkeepingSnapshot(
        persistence_revision=4,

        context=context,

        bank_items=(
            broken_bank_item,
        ),
    )


class _WrongCompanyRepository:
    """
    Deliberately broken repository used to prove the Hydrator does not trust
    repository identity blindly.
    """

    def __init__(
        self,
        *,
        snapshot: BookkeepingSnapshot,
    ) -> None:
        self._snapshot = snapshot

    def load_snapshot(
        self,
        *,
        company_id: str,
    ) -> BookkeepingSnapshot:
        del company_id

        return self._snapshot

    def commit(
        self,
        *,
        company_id: str,
        expected_revision: int,
        write_set,
    ):
        raise AssertionError(
            "commit() must not be called during hydration"
        )