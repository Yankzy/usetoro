from __future__ import annotations

from datetime import date, datetime, timezone
import pytest

from bookkeeping_state.dag.view import build_dag_view
from bookkeeping_state.domain.bank import BankAccount, BankItem
from bookkeeping_state.domain.books import BookItem
from bookkeeping_state.domain.commands import (
    AssertBookItemEvidenceCommand,
    CommandSource,
    InvalidateBookItemEvidenceCommand,
)
from bookkeeping_state.domain.context import AccountingPolicy, BookkeepingContext, RuntimeContext
from bookkeeping_state.domain.counterparties import Counterparty, CounterpartyType
from bookkeeping_state.domain.documents import Document, DocumentStatus, DocumentType
from bookkeeping_state.domain.enums import Direction, SourceType
from bookkeeping_state.domain.evidence import (
    BookItemEvidenceAssertion,
    BookItemEvidenceInvalidation,
    BookItemEvidenceType,
    EvidenceSource,
)
from bookkeeping_state.hydration.hydrator import BookkeepingHydrator
from bookkeeping_state_eval.persistence.in_memory import InMemoryBookkeepingRepository
from bookkeeping_state.persistence.repository import BookkeepingSnapshot, PersistenceWriteSet
from bookkeeping_state.reconciliation.view import build_reconciliation_view
from bookkeeping_state.routing.view import build_routing_view
from bookkeeping_state.state.bookkeeping_state import BookkeepingState
from bookkeeping_state.state.queries import BookkeepingQueries
from bookkeeping_state.state.validation import ValidationCode, validate_state
from bookkeeping_state.transitions.engine import TransitionEngine
from bookkeeping_state.transitions.result import RejectionCode


DEFAULT_DATE = date(2026, 1, 15)
DEFAULT_CLOCK = datetime(2026, 1, 31, 12, 0, 0, tzinfo=timezone.utc)


def _create_base_fixture():
    ctx = BookkeepingContext(
        company_id="test-evidence-co",
        period_start=date(2026, 1, 1),
        period_end=date(2026, 1, 31),
        base_currency="MAD",
        policy=AccountingPolicy(
            chart_of_accounts_id="pcge",
            reconciliation_date_window_days=45,
            require_exact_currency_match=True,
            allow_partial_book_reconciliation=True,
            allow_partial_bank_reconciliation=False,
        ),
    )
    acc = BankAccount(id="acc-1", name="BMCE MAD", currency="MAD")
    cp_alpha = Counterparty(
        id="cp-alpha",
        name="Client Alpha",
        counterparty_type=CounterpartyType.CUSTOMER,
    )
    cp_beta = Counterparty(
        id="cp-beta",
        name="Client Beta",
        counterparty_type=CounterpartyType.CUSTOMER,
    )
    doc_1 = Document(
        id="doc-1",
        document_type=DocumentType.INVOICE,
        status=DocumentStatus.RECEIVED,
        filename="invoice.pdf",
        received_at=DEFAULT_CLOCK,
    )
    bank_item = BankItem(
        id="bank-1",
        bank_account_id="acc-1",
        date=DEFAULT_DATE,
        amount_units="10000",
        direction=Direction.BANK_INFLOW,
        currency="MAD",
        description="Virement INV-100",
        reference="INV-100",
    )
    book_item = BookItem(
        id="book-1",
        source_type=SourceType.POSTED_BOOK_ITEM,
        origin_period="2026-01",
        date=DEFAULT_DATE,
        amount_units="10000",
        direction=Direction.BOOK_BANK_DEBIT,
        currency="MAD",
        description="Ecriture 10000",
        reference="INV-RAW",
        counterparty_id="cp-alpha",
    )

    repo = InMemoryBookkeepingRepository()
    snap = BookkeepingSnapshot(
        context=ctx,
        persistence_revision=1,
        bank_accounts=(acc,),
        bank_items=(bank_item,),
        book_items=(book_item,),
        counterparties=(cp_alpha, cp_beta),
        documents=(doc_1,),
    )
    repo.seed_snapshot(snap)

    hydrator = BookkeepingHydrator(
        repository=repo,
        clock=lambda: DEFAULT_CLOCK,
        session_id_factory=lambda: "session-test",
    )
    state = hydrator.hydrate(company_id=ctx.company_id)
    engine = TransitionEngine(repository=repo)
    return repo, hydrator, state, engine, book_item, cp_alpha, cp_beta, doc_1


def test_property_a_raw_book_item_remains_immutable():
    repo, hydrator, state, engine, book_item, cp_alpha, cp_beta, doc_1 = _create_base_fixture()

    cmd = AssertBookItemEvidenceCommand(
        command_id="cmd-1",
        expected_state_revision=state.revision,
        source=CommandSource.HUMAN,
        assertion_id="ev-1",
        book_item_id="book-1",
        evidence_type=BookItemEvidenceType.REFERENCE,
        value="INV-NEW",
        session_id=state.session_id,
        issued_at=DEFAULT_CLOCK,
    )
    res = engine.apply(command=cmd, state=state)
    assert res.applied

    # Raw BookItem must remain unchanged
    raw_item = state.get_book_item("book-1")
    assert raw_item is not None
    assert raw_item.reference == "INV-RAW"
    assert raw_item.counterparty_id == "cp-alpha"
    assert raw_item.description == "Ecriture 10000"


def test_property_b_active_counterparty_assertion_changes_effective_only():
    repo, hydrator, state, engine, book_item, cp_alpha, cp_beta, doc_1 = _create_base_fixture()

    cmd = AssertBookItemEvidenceCommand(
        command_id="cmd-cp",
        expected_state_revision=state.revision,
        source=CommandSource.HUMAN,
        assertion_id="ev-cp",
        book_item_id="book-1",
        evidence_type=BookItemEvidenceType.COUNTERPARTY,
        value="cp-beta",
        session_id=state.session_id,
        issued_at=DEFAULT_CLOCK,
    )
    res = engine.apply(command=cmd, state=state)
    assert res.applied

    queries = BookkeepingQueries(state)
    assert queries.effective_counterparty_id("book-1") == "cp-beta"
    eff_cp = queries.effective_counterparty("book-1")
    assert eff_cp is not None and eff_cp.name == "Client Beta"
    # Other dimensions unaffected
    assert queries.effective_reference("book-1") == "INV-RAW"
    assert queries.effective_description("book-1") == "Ecriture 10000"


def test_property_c_active_reference_assertion_changes_effective_only():
    repo, hydrator, state, engine, book_item, cp_alpha, cp_beta, doc_1 = _create_base_fixture()

    cmd = AssertBookItemEvidenceCommand(
        command_id="cmd-ref",
        expected_state_revision=state.revision,
        source=CommandSource.HUMAN,
        assertion_id="ev-ref",
        book_item_id="book-1",
        evidence_type=BookItemEvidenceType.REFERENCE,
        value="INV-CORRECTED",
        session_id=state.session_id,
        issued_at=DEFAULT_CLOCK,
    )
    res = engine.apply(command=cmd, state=state)
    assert res.applied

    queries = BookkeepingQueries(state)
    assert queries.effective_reference("book-1") == "INV-CORRECTED"
    assert queries.effective_counterparty_id("book-1") == "cp-alpha"


def test_property_d_superseding_reference_does_not_affect_counterparty():
    repo, hydrator, state, engine, book_item, cp_alpha, cp_beta, doc_1 = _create_base_fixture()

    # Assert CP
    res1 = engine.apply(
        command=AssertBookItemEvidenceCommand(
            command_id="cmd-cp",
            expected_state_revision=state.revision,
            source=CommandSource.HUMAN,
            assertion_id="ev-cp",
            book_item_id="book-1",
            evidence_type=BookItemEvidenceType.COUNTERPARTY,
            value="cp-beta",
            session_id=state.session_id,
            issued_at=DEFAULT_CLOCK,
        ),
        state=state,
    )
    assert res1.applied

    # Assert Ref 1
    res2 = engine.apply(
        command=AssertBookItemEvidenceCommand(
            command_id="cmd-ref-1",
            expected_state_revision=state.revision,
            source=CommandSource.HUMAN,
            assertion_id="ev-ref-1",
            book_item_id="book-1",
            evidence_type=BookItemEvidenceType.REFERENCE,
            value="INV-1",
            session_id=state.session_id,
            issued_at=DEFAULT_CLOCK,
        ),
        state=state,
    )
    assert res2.applied

    # Supersede Ref 1 with Ref 2
    res3 = engine.apply(
        command=AssertBookItemEvidenceCommand(
            command_id="cmd-ref-2",
            expected_state_revision=state.revision,
            source=CommandSource.HUMAN,
            assertion_id="ev-ref-2",
            book_item_id="book-1",
            evidence_type=BookItemEvidenceType.REFERENCE,
            value="INV-2",
            supersedes_assertion_id="ev-ref-1",
            session_id=state.session_id,
            issued_at=DEFAULT_CLOCK,
        ),
        state=state,
    )
    assert res3.applied

    queries = BookkeepingQueries(state)
    # Ref updated to INV-2
    assert queries.effective_reference("book-1") == "INV-2"
    # CP dimension remains cp-beta
    assert queries.effective_counterparty_id("book-1") == "cp-beta"


def test_property_e_invalidation_follows_non_resurrection_semantics():
    repo, hydrator, state, engine, book_item, cp_alpha, cp_beta, doc_1 = _create_base_fixture()

    # Step 1: Assert Ref 1
    engine.apply(
        command=AssertBookItemEvidenceCommand(
            command_id="cmd-1",
            expected_state_revision=state.revision,
            source=CommandSource.HUMAN,
            assertion_id="ev-1",
            book_item_id="book-1",
            evidence_type=BookItemEvidenceType.REFERENCE,
            value="INV-FIRST",
            session_id=state.session_id,
            issued_at=DEFAULT_CLOCK,
        ),
        state=state,
    )
    # Step 2: Supersede with Ref 2
    engine.apply(
        command=AssertBookItemEvidenceCommand(
            command_id="cmd-2",
            expected_state_revision=state.revision,
            source=CommandSource.HUMAN,
            assertion_id="ev-2",
            book_item_id="book-1",
            evidence_type=BookItemEvidenceType.REFERENCE,
            value="INV-SECOND",
            supersedes_assertion_id="ev-1",
            session_id=state.session_id,
            issued_at=DEFAULT_CLOCK,
        ),
        state=state,
    )
    queries = BookkeepingQueries(state)
    assert queries.effective_reference("book-1") == "INV-SECOND"

    # Step 3: Invalidate Ref 2
    res_inv = engine.apply(
        command=InvalidateBookItemEvidenceCommand(
            command_id="cmd-inv",
            expected_state_revision=state.revision,
            source=CommandSource.HUMAN,
            invalidation_id="inv-2",
            assertion_id="ev-2",
            reason="Incorrect reference",
            session_id=state.session_id,
            issued_at=DEFAULT_CLOCK,
        ),
        state=state,
    )
    assert res_inv.applied

    # ev-1 must NOT resurrect!
    queries = BookkeepingQueries(state)
    assert queries.active_evidence_assertion("book-1", BookItemEvidenceType.REFERENCE) is None
    assert queries.effective_reference("book-1") == "INV-RAW"


def test_property_f_hydrated_state_reconstructs_identical_truth():
    repo, hydrator, state, engine, book_item, cp_alpha, cp_beta, doc_1 = _create_base_fixture()

    engine.apply(
        command=AssertBookItemEvidenceCommand(
            command_id="cmd-1",
            expected_state_revision=state.revision,
            source=CommandSource.HUMAN,
            assertion_id="ev-cp",
            book_item_id="book-1",
            evidence_type=BookItemEvidenceType.COUNTERPARTY,
            value="cp-beta",
            session_id=state.session_id,
            issued_at=DEFAULT_CLOCK,
        ),
        state=state,
    )
    state.close()

    fresh_state = hydrator.hydrate(company_id="test-evidence-co", session_id="fresh-session")
    fresh_queries = BookkeepingQueries(fresh_state)
    assert fresh_queries.effective_counterparty_id("book-1") == "cp-beta"
    fresh_state.close()


def test_properties_g_h_i_bounded_views_receive_effective_semantics():
    repo, hydrator, state, engine, book_item, cp_alpha, cp_beta, doc_1 = _create_base_fixture()

    engine.apply(
        command=AssertBookItemEvidenceCommand(
            command_id="cmd-cp",
            expected_state_revision=state.revision,
            source=CommandSource.HUMAN,
            assertion_id="ev-cp",
            book_item_id="book-1",
            evidence_type=BookItemEvidenceType.COUNTERPARTY,
            value="cp-beta",
            session_id=state.session_id,
            issued_at=DEFAULT_CLOCK,
        ),
        state=state,
    )
    engine.apply(
        command=AssertBookItemEvidenceCommand(
            command_id="cmd-ref",
            expected_state_revision=state.revision,
            source=CommandSource.HUMAN,
            assertion_id="ev-ref",
            book_item_id="book-1",
            evidence_type=BookItemEvidenceType.REFERENCE,
            value="INV-EFFECTIVE",
            session_id=state.session_id,
            issued_at=DEFAULT_CLOCK,
        ),
        state=state,
    )
    engine.apply(
        command=AssertBookItemEvidenceCommand(
            command_id="cmd-desc",
            expected_state_revision=state.revision,
            source=CommandSource.HUMAN,
            assertion_id="ev-desc",
            book_item_id="book-1",
            evidence_type=BookItemEvidenceType.DESCRIPTION,
            value="Description Effective",
            session_id=state.session_id,
            issued_at=DEFAULT_CLOCK,
        ),
        state=state,
    )

    queries = BookkeepingQueries(state)

    # DagView
    dag_view = build_dag_view(queries, include_already_classified=True)
    dag_item = dag_view.get_item("book-1")
    assert dag_item is not None
    assert dag_item.counterparty_id == "cp-beta"
    assert dag_item.counterparty_name == "Client Beta"
    assert dag_item.reference == "INV-EFFECTIVE"
    assert dag_item.description == "Description Effective"

    # RoutingView
    routing_view = build_routing_view(queries)
    route_item = next(item for item in routing_view.book_items if item.book_item_id == "book-1")
    assert route_item.counterparty_id == "cp-beta"
    assert route_item.counterparty_name == "Client Beta"
    assert route_item.reference == "INV-EFFECTIVE"
    assert route_item.description == "Description Effective"

    # ReconciliationView
    recon_view = build_reconciliation_view(queries)
    recon_item = next(item for item in recon_view.book_items if item.book_item_id == "book-1")
    assert recon_item.counterparty_id == "cp-beta"
    assert recon_item.counterparty_name == "Client Beta"
    assert recon_item.reference == "INV-EFFECTIVE"
    assert recon_item.description == "Description Effective"


def test_property_j_stale_evidence_command_rejected():
    repo, hydrator, state, engine, book_item, cp_alpha, cp_beta, doc_1 = _create_base_fixture()

    cmd = AssertBookItemEvidenceCommand(
        command_id="cmd-stale",
        expected_state_revision=999,
        source=CommandSource.HUMAN,
        assertion_id="ev-stale",
        book_item_id="book-1",
        evidence_type=BookItemEvidenceType.REFERENCE,
        value="INV-STALE",
        session_id=state.session_id,
        issued_at=DEFAULT_CLOCK,
    )
    res = engine.apply(command=cmd, state=state)
    assert not res.applied
    assert res.rejection is not None
    assert res.rejection.code == RejectionCode.STATE_REVISION_CONFLICT


def test_property_k_assertion_referencing_unknown_book_item_rejected():
    repo, hydrator, state, engine, book_item, cp_alpha, cp_beta, doc_1 = _create_base_fixture()

    cmd = AssertBookItemEvidenceCommand(
        command_id="cmd-unknown-book",
        expected_state_revision=state.revision,
        source=CommandSource.HUMAN,
        assertion_id="ev-unk",
        book_item_id="nonexistent-book-item",
        evidence_type=BookItemEvidenceType.REFERENCE,
        value="INV-TEST",
        session_id=state.session_id,
        issued_at=DEFAULT_CLOCK,
    )
    res = engine.apply(command=cmd, state=state)
    assert not res.applied
    assert res.rejection is not None
    assert res.rejection.code == RejectionCode.UNKNOWN_BOOK_ITEM


def test_property_l_supersession_across_different_book_items_rejected():
    repo, hydrator, state, engine, book_item, cp_alpha, cp_beta, doc_1 = _create_base_fixture()

    engine.apply(
        command=AssertBookItemEvidenceCommand(
            command_id="cmd-1",
            expected_state_revision=state.revision,
            source=CommandSource.HUMAN,
            assertion_id="ev-1",
            book_item_id="book-1",
            evidence_type=BookItemEvidenceType.REFERENCE,
            value="INV-1",
            session_id=state.session_id,
            issued_at=DEFAULT_CLOCK,
        ),
        state=state,
    )

    snap = repo.load_snapshot(company_id=state.context.company_id)
    book_2 = BookItem(
        id="book-2",
        origin_period="2026-01",
        date=DEFAULT_DATE,
        amount_units="5000",
        direction=Direction.BOOK_BANK_DEBIT,
        currency="MAD",
        description="Book 2",
    )
    repo.commit(
        company_id=state.context.company_id,
        expected_revision=snap.persistence_revision,
        write_set=PersistenceWriteSet(book_items=(book_2,)),
    )
    company_id = state.context.company_id
    state.close()
    state = hydrator.hydrate(company_id=company_id, session_id="sess-2")

    cmd = AssertBookItemEvidenceCommand(
        command_id="cmd-mismatch",
        expected_state_revision=state.revision,
        source=CommandSource.HUMAN,
        assertion_id="ev-2",
        book_item_id="book-2",
        evidence_type=BookItemEvidenceType.REFERENCE,
        value="INV-2",
        supersedes_assertion_id="ev-1",
        session_id=state.session_id,
        issued_at=DEFAULT_CLOCK,
    )
    res = engine.apply(command=cmd, state=state)
    assert not res.applied
    assert res.rejection is not None
    assert res.rejection.code == RejectionCode.EVIDENCE_ASSERTION_SUBJECT_MISMATCH


def test_property_m_supersession_across_evidence_types_rejected():
    repo, hydrator, state, engine, book_item, cp_alpha, cp_beta, doc_1 = _create_base_fixture()

    engine.apply(
        command=AssertBookItemEvidenceCommand(
            command_id="cmd-1",
            expected_state_revision=state.revision,
            source=CommandSource.HUMAN,
            assertion_id="ev-ref",
            book_item_id="book-1",
            evidence_type=BookItemEvidenceType.REFERENCE,
            value="INV-1",
            session_id=state.session_id,
            issued_at=DEFAULT_CLOCK,
        ),
        state=state,
    )

    cmd = AssertBookItemEvidenceCommand(
        command_id="cmd-type-mismatch",
        expected_state_revision=state.revision,
        source=CommandSource.HUMAN,
        assertion_id="ev-cp",
        book_item_id="book-1",
        evidence_type=BookItemEvidenceType.COUNTERPARTY,
        value="cp-beta",
        supersedes_assertion_id="ev-ref",
        session_id=state.session_id,
        issued_at=DEFAULT_CLOCK,
    )
    res = engine.apply(command=cmd, state=state)
    assert not res.applied
    assert res.rejection is not None
    assert res.rejection.code == RejectionCode.EVIDENCE_ASSERTION_TYPE_MISMATCH
