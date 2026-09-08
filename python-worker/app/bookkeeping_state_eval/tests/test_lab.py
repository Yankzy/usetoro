from collections.abc import Mapping
from datetime import date
import io
from contextlib import redirect_stdout

from bookkeeping_state_eval.domain.bank import BankItem
from bookkeeping_state_eval.domain.books import BookItem
from bookkeeping_state_eval.domain.counterparties import Counterparty, CounterpartyType
from bookkeeping_state_eval.domain.enums import (
    AllocationSupport,
    Direction,
)
from bookkeeping_state_eval.domain.evidence import (
    BookItemEvidenceType,
    EvidenceSource,
)
from bookkeeping_state_eval.state.queries import BookkeepingQueries
from lab import BookkeepingLab


def test_lab_default_demo_initialization() -> None:
    lab = BookkeepingLab()
    try:
        assert lab.company_id == "demo-company"
        assert lab.state is not None
        assert not lab.state.is_closed
        assert lab.state.revision == 0
        assert lab.state.persistence_revision == 1
        assert len(lab.state.bank_items) == 5
        assert len(lab.state.book_items) == 5
        assert lab.last_result is None
    finally:
        if lab.state and not lab.state.is_closed:
            lab.state.close()


def test_lab_session_execution_and_hold_retention() -> None:
    lab = BookkeepingLab()
    try:
        buf = io.StringIO()
        with redirect_stdout(buf):
            lab.onecmd("run")

        output = buf.getvalue()
        assert "routing: APPLIED" in output
        assert "dag: APPLIED" in output
        assert "holds: 1" in output
        assert "reconciliation: APPLIED" in output

        # Detached result is retained
        assert lab.last_result is not None
        assert lab.last_result.is_success
        assert lab.last_result.hold_count == 1
        assert lab.last_result.holds[0].book_item_id == "book-mystery"
        assert lab.last_result.holds[0].reason == "HOLD_INSUFFICIENT_EVIDENCE"

        # Inspection state is fresh rehydrated state S0 with advanced persistence revision
        assert lab.state is not None
        assert not lab.state.is_closed
        assert lab.state.revision == 0
        assert lab.state.persistence_revision == 4

        # Verify holds command output
        buf_holds = io.StringIO()
        with redirect_stdout(buf_holds):
            lab.onecmd("holds")
        assert "book-mystery" in buf_holds.getvalue()
        assert "HOLD_INSUFFICIENT_EVIDENCE" in buf_holds.getvalue()
    finally:
        if lab.state and not lab.state.is_closed:
            lab.state.close()


def test_lab_close_and_rehydrate_lifecycle() -> None:
    lab = BookkeepingLab()
    try:
        lab.onecmd("run")
        assert lab.state is not None
        assert lab.state.persistence_revision == 4

        # Close state
        lab.onecmd("close")
        assert lab.state is not None
        assert lab.state.is_closed

        # Commands report closed state without crashing
        buf = io.StringIO()
        with redirect_stdout(buf):
            lab.onecmd("state")
        assert "State is closed. Use `rehydrate`." in buf.getvalue()

        # Rehydrate fresh state
        lab.onecmd("rehydrate")
        assert not lab.state.is_closed
        assert lab.state.revision == 0
        assert lab.state.persistence_revision == 4
    finally:
        if lab.state and not lab.state.is_closed:
            lab.state.close()


def test_lab_scenario_switching() -> None:
    lab = BookkeepingLab()
    try:
        buf = io.StringIO()
        with redirect_stdout(buf):
            lab.onecmd("scenario scenario_c_many_to_one")

        assert "Loaded scenario 'scenario_c_many_to_one'" in buf.getvalue()
        assert lab.company_id == "atlas"
        assert lab.state is not None
        assert not lab.state.is_closed
        assert lab.state.revision == 0
        assert len(lab.state.bank_items) == 3
        assert len(lab.state.book_items) == 1
        assert lab.last_result is None

        # Execute session in loaded scenario
        lab.onecmd("run")
        assert lab.last_result is not None
        assert lab.last_result.is_success
        assert lab.state.persistence_revision == 4
    finally:
        if lab.state and not lab.state.is_closed:
            lab.state.close()


def test_lab_omitted_commands_informative_messages() -> None:
    lab = BookkeepingLab()
    try:
        for cmd_name in ("run-routing", "run-dag", "run-reconciliation"):
            buf = io.StringIO()
            with redirect_stdout(buf):
                lab.onecmd(cmd_name)
            assert "Command unavailable because this subsystem has no safe public stage API" in buf.getvalue()

        buf_ev = io.StringIO()
        with redirect_stdout(buf_ev):
            lab.onecmd("add-evidence book-mystery evidence_text")
        assert "Command unavailable: durable artifacts are append-only" in buf_ev.getvalue()
    finally:
        if lab.state and not lab.state.is_closed:
            lab.state.close()


def test_a_add_bank_advances_persistence_and_fresh_s0_sees_it() -> None:
    lab = BookkeepingLab()
    try:
        init_p = lab.state.persistence_revision
        init_bank_count = len(lab.state.bank_items)
        new_bank = BankItem(
            id="bank-test-a",
            bank_account_id="acc-main",
            date=date(2026, 9, 2),
            amount_units="75000",
            direction=Direction.BANK_INFLOW,
            currency="MAD",
            description="Test A Bank Inflow",
        )
        lab.add_bank_item(new_bank)

        assert lab.state is not None
        assert not lab.state.is_closed
        assert lab.state.revision == 0
        assert lab.state.persistence_revision == init_p + 1
        assert len(lab.state.bank_items) == init_bank_count + 1
        assert "bank-test-a" in lab.state.bank_items
        assert lab.state.bank_items["bank-test-a"].amount_units == "75000"
    finally:
        if lab.state and not lab.state.is_closed:
            lab.state.close()


def test_b_add_book_advances_persistence_and_fresh_s0_sees_it() -> None:
    lab = BookkeepingLab()
    try:
        init_p = lab.state.persistence_revision
        init_book_count = len(lab.state.book_items)
        new_book = BookItem(
            id="book-test-b",
            origin_period="2026-09",
            date=date(2026, 9, 2),
            amount_units="75000",
            direction=Direction.BOOK_BANK_DEBIT,
            currency="MAD",
            description="Test B Book Debit",
        )
        lab.add_book_item(new_book)

        assert lab.state is not None
        assert not lab.state.is_closed
        assert lab.state.revision == 0
        assert lab.state.persistence_revision == init_p + 1
        assert len(lab.state.book_items) == init_book_count + 1
        assert "book-test-b" in lab.state.book_items
        assert lab.state.book_items["book-test-b"].amount_units == "75000"
    finally:
        if lab.state and not lab.state.is_closed:
            lab.state.close()


def test_c_add_counterparty_works_through_authoritative_source_boundary() -> None:
    lab = BookkeepingLab()
    try:
        init_p = lab.state.persistence_revision
        new_cp = Counterparty(
            id="cp-test-c",
            name="Alpha Corp Partner",
            counterparty_type=CounterpartyType.CUSTOMER,
            tax_id="TAX-9988",
        )
        lab.add_counterparty_item(new_cp)

        assert lab.state is not None
        assert not lab.state.is_closed
        assert lab.state.revision == 0
        assert lab.state.persistence_revision == init_p + 1
        assert "cp-test-c" in lab.state.counterparties
        assert lab.state.counterparties["cp-test-c"].name == "Alpha Corp Partner"
    finally:
        if lab.state and not lab.state.is_closed:
            lab.state.close()


def test_d_assert_evidence_uses_transition_engine_and_changes_effective_semantics() -> None:
    lab = BookkeepingLab()
    try:
        # Counterparty must exist for COUNTERPARTY evidence assertion
        lab.add_counterparty_item(
            Counterparty(
                id="cp-cloud",
                name="Cloud Hosting Provider",
                counterparty_type=CounterpartyType.SUPPLIER,
            )
        )
        init_p = lab.state.persistence_revision
        assert "book-mystery" in lab.state.book_items
        assert lab.state.book_items["book-mystery"].counterparty_id is None

        success = lab.assert_book_item_evidence(
            book_item_id="book-mystery",
            evidence_type=BookItemEvidenceType.COUNTERPARTY,
            value="cp-cloud",
            evidence_source=EvidenceSource.HUMAN_ASSERTION,
        )
        assert success is True

        assert lab.state is not None
        assert not lab.state.is_closed
        assert lab.state.revision == 0
        assert lab.state.persistence_revision == init_p + 1
        queries = BookkeepingQueries(lab.state)
        assert queries.effective_counterparty_id("book-mystery") == "cp-cloud"
        eff_cp = queries.effective_counterparty("book-mystery")
        assert eff_cp is not None
        assert eff_cp.name == "Cloud Hosting Provider"
        assert len(lab.state.book_item_evidence_assertions) == 1
    finally:
        if lab.state and not lab.state.is_closed:
            lab.state.close()


def test_e_invalidate_evidence_uses_transition_engine_and_preserves_history() -> None:
    lab = BookkeepingLab()
    try:
        lab.add_counterparty_item(
            Counterparty(
                id="cp-cloud",
                name="Cloud Hosting Provider",
                counterparty_type=CounterpartyType.SUPPLIER,
            )
        )
        lab.assert_book_item_evidence(
            book_item_id="book-mystery",
            evidence_type=BookItemEvidenceType.COUNTERPARTY,
            value="cp-cloud",
            evidence_source=EvidenceSource.HUMAN_ASSERTION,
        )
        assert len(lab.state.book_item_evidence_assertions) == 1
        assertion = next(iter(lab.state.book_item_evidence_assertions.values()))
        assertion_id = assertion.id
        p_after_assert = lab.state.persistence_revision

        success = lab.invalidate_evidence_assertion(
            assertion_id=assertion_id,
            reason="Correcting mistaken assertion",
        )
        assert success is True

        assert lab.state is not None
        assert not lab.state.is_closed
        assert lab.state.revision == 0
        assert lab.state.persistence_revision == p_after_assert + 1

        assert assertion_id in lab.state.book_item_evidence_assertions
        assert len(lab.state.book_item_evidence_invalidations) == 1
        inval = next(iter(lab.state.book_item_evidence_invalidations.values()))
        assert inval.assertion_id == assertion_id
        assert inval.reason == "Correcting mistaken assertion"

        queries = BookkeepingQueries(lab.state)
        assert queries.effective_counterparty_id("book-mystery") is None
        assert queries.effective_counterparty("book-mystery") is None
    finally:
        if lab.state and not lab.state.is_closed:
            lab.state.close()


def test_f_invalidate_reconciliation_releases_capacity_and_preserves_history() -> None:
    lab = BookkeepingLab()
    try:
        buf = io.StringIO()
        with redirect_stdout(buf):
            lab.onecmd("run")
        assert len(lab.state.reconciliations) > 0
        rec = next(iter(lab.state.reconciliations.values()))
        rec_id = rec.id
        bank_id = rec.bank_allocations[0].bank_item_id

        queries_before = BookkeepingQueries(lab.state)
        bank_rem_before = queries_before.bank_remaining_units(bank_id)
        p_before_inval = lab.state.persistence_revision

        success = lab.invalidate_reconciliation_item(
            reconciliation_id=rec_id,
            reason="Erroneous match identified by reviewer",
        )
        assert success is True

        assert lab.state is not None
        assert not lab.state.is_closed
        assert lab.state.revision == 0
        assert lab.state.persistence_revision == p_before_inval + 1

        assert rec_id in lab.state.reconciliations
        assert any(
            inv.reconciliation_id == rec_id
            for inv in lab.state.reconciliation_invalidations.values()
        )

        queries_after = BookkeepingQueries(lab.state)
        bank_rem_after = queries_after.bank_remaining_units(bank_id)
        assert bank_rem_after > bank_rem_before
    finally:
        if lab.state and not lab.state.is_closed:
            lab.state.close()


def test_g_unchanged_second_run_remains_idempotent() -> None:
    lab = BookkeepingLab()
    try:
        buf1 = io.StringIO()
        with redirect_stdout(buf1):
            lab.onecmd("run")
        assert lab.last_result is not None
        rec_stage_1 = lab.last_result.reconciliation_stage_result
        rec_count_1 = rec_stage_1.command_count if rec_stage_1 else 0
        assert rec_count_1 > 0
        pers_rev_1 = lab.state.persistence_revision
        total_recs_1 = len(lab.state.reconciliations)

        buf2 = io.StringIO()
        with redirect_stdout(buf2):
            lab.onecmd("run")
        assert lab.last_result is not None
        rec_stage_2 = lab.last_result.reconciliation_stage_result
        rec_count_2 = rec_stage_2.command_count if rec_stage_2 else 0
        pers_rev_2 = lab.state.persistence_revision
        total_recs_2 = len(lab.state.reconciliations)

        assert rec_count_2 == 0
        assert total_recs_2 == total_recs_1
        assert pers_rev_2 == pers_rev_1
    finally:
        if lab.state and not lab.state.is_closed:
            lab.state.close()


def test_h_durable_world_mutation_causes_later_run_to_perform_new_work() -> None:
    lab = BookkeepingLab()
    try:
        buf1 = io.StringIO()
        with redirect_stdout(buf1):
            lab.onecmd("run")
        initial_recs = len(lab.state.reconciliations)

        new_bank = BankItem(
            id="bank-bonus-h",
            bank_account_id="acc-main",
            date=date(2026, 9, 3),
            amount_units="33000",
            direction=Direction.BANK_OUTFLOW,
            currency="MAD",
            description="Office supplies extra reorder",
            reference="OFF-BONUS",
        )
        new_book = BookItem(
            id="book-bonus-h",
            origin_period="2026-09",
            date=date(2026, 9, 3),
            amount_units="33000",
            direction=Direction.BOOK_BANK_CREDIT,
            currency="MAD",
            description="Office supplies extra reorder",
            reference="OFF-BONUS",
        )
        lab.add_bank_item(new_bank)
        lab.add_book_item(new_book)

        buf2 = io.StringIO()
        with redirect_stdout(buf2):
            lab.onecmd("run")
        assert lab.last_result is not None
        rec_stage = lab.last_result.reconciliation_stage_result
        assert rec_stage is not None
        assert rec_stage.command_count >= 1
        assert len(lab.state.reconciliations) > initial_recs
    finally:
        if lab.state and not lab.state.is_closed:
            lab.state.close()


def test_i_reset_restores_exact_original_scenario_world() -> None:
    lab = BookkeepingLab()
    try:
        orig_bank_count = len(lab.state.bank_items)
        orig_book_count = len(lab.state.book_items)
        assert lab.state.persistence_revision == 1

        new_bank = BankItem(
            id="bank-tmp",
            bank_account_id="acc-main",
            date=date(2026, 9, 1),
            amount_units="1000",
            direction=Direction.BANK_INFLOW,
            currency="MAD",
            description="Temporary BankItem",
        )
        lab.add_bank_item(new_bank)
        buf1 = io.StringIO()
        with redirect_stdout(buf1):
            lab.onecmd("run")
        assert lab.last_result is not None
        assert lab.state.persistence_revision > 1
        assert len(lab.state.bank_items) == orig_bank_count + 1

        buf = io.StringIO()
        with redirect_stdout(buf):
            lab.onecmd("reset")

        assert lab.state is not None
        assert not lab.state.is_closed
        assert lab.state.revision == 0
        assert lab.state.persistence_revision == 1
        assert len(lab.state.bank_items) == orig_bank_count
        assert len(lab.state.book_items) == orig_book_count
        assert len(lab.state.reconciliations) == 0
        assert lab.last_result is None
    finally:
        if lab.state and not lab.state.is_closed:
            lab.state.close()


def test_j_policy_conservative_scenario_b_creates_no_reconciliation() -> None:
    lab = BookkeepingLab()
    try:
        buf0 = io.StringIO()
        with redirect_stdout(buf0):
            lab.onecmd("scenario scenario_b_one_to_many")
            lab.onecmd("set-policy auto_reconcile_unique_inferred_allocation false")
        assert lab.state.context.policy.auto_reconcile_unique_inferred_allocation is False

        buf = io.StringIO()
        with redirect_stdout(buf):
            lab.onecmd("run")

        assert lab.last_result is not None
        rec_stage = lab.last_result.reconciliation_stage_result
        rec_count = rec_stage.command_count if rec_stage else 0
        assert rec_count == 0
        assert len(lab.state.reconciliations) == 0
    finally:
        if lab.state and not lab.state.is_closed:
            lab.state.close()


def test_k_policy_conservative_scenario_b_exposes_review_hypothesis() -> None:
    lab = BookkeepingLab()
    try:
        buf0 = io.StringIO()
        with redirect_stdout(buf0):
            lab.onecmd("scenario scenario_b_one_to_many")
            lab.onecmd("set-policy auto_reconcile_unique_inferred_allocation false")
            lab.onecmd("run")

        assert lab.last_result is not None
        assert len(lab.last_result.review_hypotheses) >= 1
        hyp = lab.last_result.review_hypotheses[0]
        assert hyp.allocation_support == AllocationSupport.UNIQUE_INFERENCE

        buf = io.StringIO()
        with redirect_stdout(buf):
            lab.onecmd("review")
        output = buf.getvalue()
        assert "REVIEW-ONLY HYPOTHESES" in output
        assert "UNIQUE_INFERENCE" in output
        assert "Policy auto_reconcile_unique_inferred_allocation is False" in output
    finally:
        if lab.state and not lab.state.is_closed:
            lab.state.close()


def test_l_policy_aggressive_scenario_b_reconciles() -> None:
    lab = BookkeepingLab()
    try:
        buf0 = io.StringIO()
        with redirect_stdout(buf0):
            lab.onecmd("scenario scenario_b_one_to_many")
            lab.onecmd("set-policy auto_reconcile_unique_inferred_allocation true")
        assert lab.state.context.policy.auto_reconcile_unique_inferred_allocation is True

        buf1 = io.StringIO()
        with redirect_stdout(buf1):
            lab.onecmd("run")
        assert lab.last_result is not None
        rec_stage = lab.last_result.reconciliation_stage_result
        assert rec_stage is not None
        assert rec_stage.command_count == 1
        assert len(lab.state.reconciliations) == 1
        assert lab.last_result.reconciliation_plan is not None
        assert (
            lab.last_result.reconciliation_plan.hypotheses[0].allocation_support
            == AllocationSupport.UNIQUE_INFERENCE
        )
    finally:
        if lab.state and not lab.state.is_closed:
            lab.state.close()


def test_m_no_lab_command_directly_mutates_live_bookkeeping_state_collections() -> None:
    lab = BookkeepingLab()
    try:
        # Step 1: add-bank closes old state and hydrates fresh state
        state_0 = lab.state
        new_bank = BankItem(
            id="bank-test-m",
            bank_account_id="acc-main",
            date=date(2026, 9, 1),
            amount_units="5000",
            direction=Direction.BANK_INFLOW,
            currency="MAD",
            description="Test M Bank Inflow",
        )
        lab.add_bank_item(new_bank)
        assert state_0.is_closed is True
        assert lab.state is not state_0
        assert not lab.state.is_closed

        # Pre-seed counterparty for assert-evidence
        lab.add_counterparty_item(
            Counterparty(id="cp-test", name="CP Test", counterparty_type=CounterpartyType.CUSTOMER)
        )

        # Step 2: assert-evidence closes old state and hydrates fresh state
        state_1 = lab.state
        lab.assert_book_item_evidence(
            book_item_id="book-mystery",
            evidence_type=BookItemEvidenceType.COUNTERPARTY,
            value="cp-test",
        )
        assert state_1.is_closed is True
        assert lab.state is not state_1
        assert not lab.state.is_closed

        # Step 3: set-policy closes old state and hydrates fresh state
        state_2 = lab.state
        buf0 = io.StringIO()
        with redirect_stdout(buf0):
            lab.onecmd("set-policy auto_reconcile_unique_inferred_allocation false")
        assert state_2.is_closed is True
        assert lab.state is not state_2
        assert not lab.state.is_closed

        # Step 4: reset closes old state and hydrates fresh state
        state_3 = lab.state
        buf1 = io.StringIO()
        with redirect_stdout(buf1):
            lab.onecmd("reset")
        assert state_3.is_closed is True
        assert lab.state is not state_3
        assert not lab.state.is_closed
    finally:
        if lab.state and not lab.state.is_closed:
            lab.state.close()


def test_n_every_durable_mutation_leaves_a_fresh_usable_inspection_state_at_s0() -> None:
    lab = BookkeepingLab()
    try:
        def verify_usable_s0(expected_p: int) -> None:
            assert lab.state is not None
            assert not lab.state.is_closed
            assert lab.state.revision == 0
            assert lab.state.persistence_revision == expected_p
            assert isinstance(lab.state.bank_items, Mapping)
            assert isinstance(lab.state.book_items, Mapping)
            assert isinstance(lab.state.reconciliations, Mapping)
            buf = io.StringIO()
            with redirect_stdout(buf):
                lab.onecmd("unresolved")
                lab.onecmd("history")
            assert "UNRESOLVED" in buf.getvalue()
            assert "DURABLE ARTIFACT HISTORY" in buf.getvalue()

        # 1. After init: P1 S0
        verify_usable_s0(1)

        # 2. After add-bank: P2 S0
        lab.add_bank_item(
            BankItem(
                id="bank-n",
                bank_account_id="acc-main",
                date=date(2026, 9, 1),
                amount_units="1000",
                direction=Direction.BANK_INFLOW,
                currency="MAD",
                description="Bank N Inflow",
            )
        )
        verify_usable_s0(2)

        # 3. After add-book: P3 S0
        lab.add_book_item(
            BookItem(
                id="book-n",
                origin_period="2026-09",
                date=date(2026, 9, 1),
                amount_units="1000",
                direction=Direction.BOOK_BANK_DEBIT,
                currency="MAD",
                description="Book N Debit",
            )
        )
        verify_usable_s0(3)

        # 4. After add-counterparty: P4 S0
        lab.add_counterparty_item(
            Counterparty(id="cp-n", name="CP N", counterparty_type=CounterpartyType.SUPPLIER)
        )
        verify_usable_s0(4)

        # 5. After assert-evidence: P5 S0
        lab.assert_book_item_evidence(
            book_item_id="book-n",
            evidence_type=BookItemEvidenceType.COUNTERPARTY,
            value="cp-n",
        )
        verify_usable_s0(5)

        # 6. After invalidate-evidence: P6 S0
        assertion_id = next(iter(lab.state.book_item_evidence_assertions.keys()))
        lab.invalidate_evidence_assertion(assertion_id=assertion_id)
        verify_usable_s0(6)

        # 7. After run session: P9 S0 (routing P7, dag P8, rec P9)
        buf_run = io.StringIO()
        with redirect_stdout(buf_run):
            lab.onecmd("run")
        verify_usable_s0(lab.state.persistence_revision)

        # 8. After invalidate-reconciliation: P+1 S0
        rec_id = next(iter(lab.state.reconciliations.keys()))
        p_cur = lab.state.persistence_revision
        lab.invalidate_reconciliation_item(reconciliation_id=rec_id)
        verify_usable_s0(p_cur + 1)

        # 9. After reset: P1 S0
        buf_reset = io.StringIO()
        with redirect_stdout(buf_reset):
            lab.onecmd("reset")
        verify_usable_s0(1)
    finally:
        if lab.state and not lab.state.is_closed:
            lab.state.close()

