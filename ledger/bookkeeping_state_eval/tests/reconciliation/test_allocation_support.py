from __future__ import annotations

from datetime import date, datetime, timezone
import pytest

from bookkeeping_state.domain.bank import BankAccount, BankItem
from bookkeeping_state.domain.books import BookItem
from bookkeeping_state.domain.commands import (
    CommandSource,
    CreateReconciliationCommand,
)
from bookkeeping_state.domain.context import (
    AccountingPolicy,
    BookkeepingContext,
)
from bookkeeping_state.domain.counterparties import (
    Counterparty,
    CounterpartyType,
)
from bookkeeping_state.domain.enums import (
    AllocationSupport,
    Direction,
    Eligibility,
    SemanticAdmissibility,
    SourceType,
)
from bookkeeping_state.domain.evidence import (
    BookItemEvidenceAssertion,
    BookItemEvidenceType,
    EvidenceSource,
)
from bookkeeping_state.domain.hypotheses import ReconciliationHypothesis
from bookkeeping_state.domain.reconciliations import (
    BankAllocation,
    BookAllocation,
)
from bookkeeping_state.hydration.hydrator import BookkeepingHydrator
from bookkeeping_state_eval.persistence.in_memory import (
    InMemoryBookkeepingRepository,
)
from bookkeeping_state.persistence.repository import BookkeepingSnapshot
from bookkeeping_state.reconciliation.allocation_analysis import (
    evaluate_allocation_support,
)
from bookkeeping_state.reconciliation.candidate_generation import (
    CandidateGenerator,
    DefaultReconciliationScorer,
)
from bookkeeping_state.reconciliation.models import (
    CandidateType,
    ReconciliationCandidate,
    UnresolvedReason,
)
from bookkeeping_state.reconciliation.optimizer import (
    optimize_reconciliation,
)
from bookkeeping_state.reconciliation.service import (
    ReconciliationService,
)
from bookkeeping_state.reconciliation.view import (
    ReconciliationViewConfig,
    build_reconciliation_view,
)
from bookkeeping_state.state.queries import BookkeepingQueries
from bookkeeping_state.transitions.engine import TransitionEngine
from bookkeeping_state.transitions.result import (
    RejectionCode,
    TransitionStatus,
)
from bookkeeping_state_eval.scenarios.temporal import (
    get_temporal_challenge,
    list_temporal_challenges,
)


def _setup_hydrated_state(
    bank_items: list[BankItem],
    book_items: list[BookItem],
    policy: AccountingPolicy | None = None,
    evidence_assertions: list[BookItemEvidenceAssertion] | None = None,
):
    pol = policy or AccountingPolicy(
        chart_of_accounts_id="pcge",
        reconciliation_date_window_days=45,
        require_exact_currency_match=True,
        allow_partial_book_reconciliation=True,
        allow_partial_bank_reconciliation=True,
        auto_reconcile_unique_inferred_allocation=True,
    )
    ctx = BookkeepingContext(
        company_id="comp-test",
        period_start=date(2026, 1, 1),
        period_end=date(2026, 1, 31),
        base_currency="MAD",
        policy=pol,
    )
    acc = BankAccount(id="acc-1", name="BMCE MAD", currency="MAD")
    resolved_book_items = tuple(
        b.model_copy(update={"source_type": SourceType.POSTED_BOOK_ITEM})
        if b.source_type == SourceType.STAGING_BOOK_ITEM
        else b
        for b in book_items
    )
    snapshot = BookkeepingSnapshot(
        persistence_revision=1,
        context=ctx,
        bank_accounts=(acc,),
        bank_items=tuple(bank_items),
        book_items=resolved_book_items,
        book_item_evidence_assertions=tuple(evidence_assertions or ()),
    )
    repo = InMemoryBookkeepingRepository()
    repo.seed_snapshot(snapshot)
    hydrator = BookkeepingHydrator(
        repository=repo,
        clock=lambda: datetime(2026, 1, 31, tzinfo=timezone.utc),
        session_id_factory=lambda: "sess-test",
    )
    state = hydrator.hydrate(company_id="comp-test", session_id="sess-test")
    return state, repo


def test_1_to_1_exact_match_with_reference_has_explicit_evidence():
    b_item = BankItem(
        id="b1",
        bank_account_id="acc-1",
        date=date(2026, 1, 10),
        amount_units="1000",
        direction=Direction.BANK_OUTFLOW,
        currency="MAD",
        description="Paiement Facture INV-101",
        reference="INV-101",
    )
    j_item = BookItem(
        id="j1",
        origin_period="2026-01",
        date=date(2026, 1, 10),
        amount_units="1000",
        direction=Direction.BOOK_BANK_CREDIT,
        currency="MAD",
        description="Facture Fournisseur",
        reference="INV-101",
    )
    state, _ = _setup_hydrated_state([b_item], [j_item])
    try:
        queries = BookkeepingQueries(state)
        view = build_reconciliation_view(queries)
        cand = ReconciliationCandidate(
            candidate_id="cand1",
            candidate_type=CandidateType.ONE_TO_ONE_EXACT,
            bank_allocations=(BankAllocation(bank_item_id="b1", amount_units="1000"),),
            book_allocations=(BookAllocation(book_item_id="j1", amount_units="1000"),),
            total_amount_units="1000",
        )
        support, _ = evaluate_allocation_support(cand, view)
        assert support == AllocationSupport.EXPLICIT_EVIDENCE
    finally:
        state.close()


def test_1_to_n_grouped_with_all_leg_references_has_explicit_evidence():
    b_item = BankItem(
        id="b1",
        bank_account_id="acc-1",
        date=date(2026, 1, 12),
        amount_units="50000",
        direction=Direction.BANK_OUTFLOW,
        currency="MAD",
        description="VRT Fournisseur Delta DELTA-1 DELTA-2",
        reference="DELTA-1 DELTA-2",
    )
    j1 = BookItem(
        id="j1",
        origin_period="2026-01",
        date=date(2026, 1, 10),
        amount_units="20000",
        direction=Direction.BOOK_BANK_CREDIT,
        currency="MAD",
        description="Fournisseur Delta Facture 1",
        reference="DELTA-1",
    )
    j2 = BookItem(
        id="j2",
        origin_period="2026-01",
        date=date(2026, 1, 10),
        amount_units="30000",
        direction=Direction.BOOK_BANK_CREDIT,
        currency="MAD",
        description="Fournisseur Delta Facture 2",
        reference="DELTA-2",
    )
    state, _ = _setup_hydrated_state([b_item], [j1, j2])
    try:
        queries = BookkeepingQueries(state)
        view = build_reconciliation_view(queries)
        cand = ReconciliationCandidate(
            candidate_id="cand-1-n",
            candidate_type=CandidateType.ONE_TO_MANY,
            bank_allocations=(BankAllocation(bank_item_id="b1", amount_units="50000"),),
            book_allocations=(
                BookAllocation(book_item_id="j1", amount_units="20000"),
                BookAllocation(book_item_id="j2", amount_units="30000"),
            ),
            total_amount_units="50000",
        )
        support, _ = evaluate_allocation_support(cand, view)
        assert support == AllocationSupport.EXPLICIT_EVIDENCE
    finally:
        state.close()


def test_1_to_n_grouped_unique_inference():
    b_item = BankItem(
        id="b1",
        bank_account_id="acc-1",
        date=date(2026, 1, 12),
        amount_units="50000",
        direction=Direction.BANK_OUTFLOW,
        currency="MAD",
        description="VRT Fournisseur Delta BATCH-DELTA",
        reference="BATCH-DELTA",
    )
    j1 = BookItem(
        id="j1",
        origin_period="2026-01",
        date=date(2026, 1, 10),
        amount_units="20000",
        direction=Direction.BOOK_BANK_CREDIT,
        currency="MAD",
        description="Fournisseur Delta Facture 1",
        reference="DELTA-1",
    )
    j2 = BookItem(
        id="j2",
        origin_period="2026-01",
        date=date(2026, 1, 10),
        amount_units="30000",
        direction=Direction.BOOK_BANK_CREDIT,
        currency="MAD",
        description="Fournisseur Delta Facture 2",
        reference="DELTA-2",
    )
    state, _ = _setup_hydrated_state([b_item], [j1, j2])
    try:
        queries = BookkeepingQueries(state)
        view = build_reconciliation_view(queries)
        cand = ReconciliationCandidate(
            candidate_id="cand-1-n",
            candidate_type=CandidateType.ONE_TO_MANY,
            bank_allocations=(BankAllocation(bank_item_id="b1", amount_units="50000"),),
            book_allocations=(
                BookAllocation(book_item_id="j1", amount_units="20000"),
                BookAllocation(book_item_id="j2", amount_units="30000"),
            ),
            total_amount_units="50000",
        )
        support, _ = evaluate_allocation_support(cand, view)
        assert support == AllocationSupport.UNIQUE_INFERENCE
    finally:
        state.close()


def test_1_to_n_grouped_ambiguous_subsets_is_insufficient_evidence():
    b_item = BankItem(
        id="b1",
        bank_account_id="acc-1",
        date=date(2026, 1, 12),
        amount_units="50000",
        direction=Direction.BANK_OUTFLOW,
        currency="MAD",
        description="VRT Fournisseur Delta",
        reference="BATCH-DELTA",
    )
    j1 = BookItem(
        id="j1",
        origin_period="2026-01",
        date=date(2026, 1, 10),
        amount_units="20000",
        direction=Direction.BOOK_BANK_CREDIT,
        currency="MAD",
        description="Fournisseur Delta Bill 1",
        reference="DELTA-1",
    )
    j2 = BookItem(
        id="j2",
        origin_period="2026-01",
        date=date(2026, 1, 10),
        amount_units="30000",
        direction=Direction.BOOK_BANK_CREDIT,
        currency="MAD",
        description="Fournisseur Delta Bill 2",
        reference="DELTA-2",
    )
    j3 = BookItem(
        id="j3",
        origin_period="2026-01",
        date=date(2026, 1, 10),
        amount_units="50000",
        direction=Direction.BOOK_BANK_CREDIT,
        currency="MAD",
        description="Fournisseur Delta Bill 3",
        reference="DELTA-3",
    )
    state, _ = _setup_hydrated_state([b_item], [j1, j2, j3])
    try:
        queries = BookkeepingQueries(state)
        view = build_reconciliation_view(queries)
        cand = ReconciliationCandidate(
            candidate_id="cand-1-n",
            candidate_type=CandidateType.ONE_TO_MANY,
            bank_allocations=(BankAllocation(bank_item_id="b1", amount_units="50000"),),
            book_allocations=(
                BookAllocation(book_item_id="j1", amount_units="20000"),
                BookAllocation(book_item_id="j2", amount_units="30000"),
            ),
            total_amount_units="50000",
        )
        support, _ = evaluate_allocation_support(cand, view)
        assert support == AllocationSupport.INSUFFICIENT_EVIDENCE
    finally:
        state.close()


def test_contradicted_allocation_support():
    b_item = BankItem(
        id="b1",
        bank_account_id="acc-1",
        date=date(2026, 1, 10),
        amount_units="1000",
        direction=Direction.BANK_OUTFLOW,
        currency="MAD",
        description="Paiement Alpha Corp",
        reference="ALPHA-99",
    )
    j_item = BookItem(
        id="j1",
        origin_period="2026-01",
        date=date(2026, 1, 10),
        amount_units="1000",
        direction=Direction.BOOK_BANK_CREDIT,
        currency="MAD",
        description="Facture Fournisseur Beta Ltd",
        reference="BETA-101",
    )
    state, _ = _setup_hydrated_state([b_item], [j_item])
    try:
        queries = BookkeepingQueries(state)
        view = build_reconciliation_view(queries)
        cand = ReconciliationCandidate(
            candidate_id="cand1",
            candidate_type=CandidateType.ONE_TO_ONE_EXACT,
            bank_allocations=(BankAllocation(bank_item_id="b1", amount_units="1000"),),
            book_allocations=(BookAllocation(book_item_id="j1", amount_units="1000"),),
            total_amount_units="1000",
        )
        # Conflicting explicit references cause contradiction
        support, _ = evaluate_allocation_support(cand, view)
        assert support == AllocationSupport.CONTRADICTED
    finally:
        state.close()


def test_1_to_1_partial_match_without_explicit_partial_marker_is_unique_inference():
    b_item = BankItem(
        id="b1",
        bank_account_id="acc-1",
        date=date(2026, 1, 8),
        amount_units="50000",
        direction=Direction.BANK_INFLOW,
        currency="MAD",
        description="VRT Client Atlas",
        reference="BATCH-ATLAS",
    )
    j_item = BookItem(
        id="j1",
        origin_period="2026-01",
        date=date(2026, 1, 8),
        amount_units="80000",
        direction=Direction.BOOK_BANK_DEBIT,
        currency="MAD",
        description="Facture Client Atlas",
        reference="ATLAS-INV",
    )
    state, _ = _setup_hydrated_state([b_item], [j_item])
    try:
        queries = BookkeepingQueries(state)
        view = build_reconciliation_view(queries)
        cand = ReconciliationCandidate(
            candidate_id="cand-part",
            candidate_type=CandidateType.ONE_TO_ONE_PARTIAL_BOOK,
            bank_allocations=(BankAllocation(bank_item_id="b1", amount_units="50000"),),
            book_allocations=(BookAllocation(book_item_id="j1", amount_units="50000"),),
            total_amount_units="50000",
        )
        support, _ = evaluate_allocation_support(cand, view)
        assert support == AllocationSupport.UNIQUE_INFERENCE
    finally:
        state.close()


def test_1_to_1_partial_match_with_acompte_marker_is_explicit_evidence():
    b_item = BankItem(
        id="b1",
        bank_account_id="acc-1",
        date=date(2026, 1, 8),
        amount_units="50000",
        direction=Direction.BANK_INFLOW,
        currency="MAD",
        description="VRT Client Atlas Acompte ATLAS-INV",
        reference="ATLAS-INV",
    )
    j_item = BookItem(
        id="j1",
        origin_period="2026-01",
        date=date(2026, 1, 8),
        amount_units="80000",
        direction=Direction.BOOK_BANK_DEBIT,
        currency="MAD",
        description="Facture Client Atlas",
        reference="ATLAS-INV",
    )
    state, _ = _setup_hydrated_state([b_item], [j_item])
    try:
        queries = BookkeepingQueries(state)
        view = build_reconciliation_view(queries)
        cand = ReconciliationCandidate(
            candidate_id="cand-part",
            candidate_type=CandidateType.ONE_TO_ONE_PARTIAL_BOOK,
            bank_allocations=(BankAllocation(bank_item_id="b1", amount_units="50000"),),
            book_allocations=(BookAllocation(book_item_id="j1", amount_units="50000"),),
            total_amount_units="50000",
        )
        support, _ = evaluate_allocation_support(cand, view)
        assert support == AllocationSupport.EXPLICIT_EVIDENCE
    finally:
        state.close()


def test_policy_auto_reconcile_unique_inferred_allocation_controls_planning():
    b_item = BankItem(
        id="b1",
        bank_account_id="acc-1",
        date=date(2026, 1, 12),
        amount_units="50000",
        direction=Direction.BANK_OUTFLOW,
        currency="MAD",
        description="VRT Fournisseur Delta BATCH-DELTA",
        reference="BATCH-DELTA",
    )
    j1 = BookItem(
        id="j1",
        origin_period="2026-01",
        date=date(2026, 1, 10),
        amount_units="20000",
        direction=Direction.BOOK_BANK_CREDIT,
        currency="MAD",
        description="Fournisseur Delta Facture 1",
        reference="DELTA-1",
    )
    j2 = BookItem(
        id="j2",
        origin_period="2026-01",
        date=date(2026, 1, 10),
        amount_units="30000",
        direction=Direction.BOOK_BANK_CREDIT,
        currency="MAD",
        description="Fournisseur Delta Facture 2",
        reference="DELTA-2",
    )

    # 1. Conservative mode: auto_reconcile_unique_inferred_allocation = False
    state_cons, _ = _setup_hydrated_state(
        [b_item],
        [j1, j2],
        policy=AccountingPolicy(
            chart_of_accounts_id="pcge",
            reconciliation_date_window_days=45,
            require_exact_currency_match=True,
            allow_partial_book_reconciliation=True,
            allow_partial_bank_reconciliation=False,
            auto_reconcile_unique_inferred_allocation=False,
        ),
    )
    try:
        service = ReconciliationService()
        queries_cons = BookkeepingQueries(state_cons)
        plan_cons = service.plan(queries_cons)

        assert len(plan_cons.commands) == 0
        assert len(plan_cons.result.selected_hypotheses) == 0
        assert len(plan_cons.result.unresolved_bank_items) == 1
        assert (
            plan_cons.result.unresolved_bank_items[0].reason
            == UnresolvedReason.ALLOCATION_REQUIRES_REVIEW
        )
        assert len(plan_cons.result.review_hypotheses) == 1
        rev_h = plan_cons.result.review_hypotheses[0]
        assert rev_h.eligibility == Eligibility.COUNTERFACTUAL_ONLY
        assert rev_h.allocation_support == AllocationSupport.UNIQUE_INFERENCE
    finally:
        state_cons.close()

    # 2. Aggressive mode: auto_reconcile_unique_inferred_allocation = True
    state_aggr, _ = _setup_hydrated_state(
        [b_item],
        [j1, j2],
        policy=AccountingPolicy(
            chart_of_accounts_id="pcge",
            reconciliation_date_window_days=45,
            require_exact_currency_match=True,
            allow_partial_book_reconciliation=True,
            allow_partial_bank_reconciliation=False,
            auto_reconcile_unique_inferred_allocation=True,
        ),
    )
    try:
        service = ReconciliationService()
        queries_aggr = BookkeepingQueries(state_aggr)
        plan_aggr = service.plan(queries_aggr)

        assert len(plan_aggr.commands) == 1
        assert len(plan_aggr.result.selected_hypotheses) == 1
        assert len(plan_aggr.result.unresolved_bank_items) == 0
        sel_h = plan_aggr.result.selected_hypotheses[0]
        assert sel_h.eligibility == Eligibility.SELECTABLE
        assert sel_h.allocation_support == AllocationSupport.UNIQUE_INFERENCE
    finally:
        state_aggr.close()


def test_transition_handler_rejects_insufficient_or_contradicted_allocation():
    b_item = BankItem(
        id="b1",
        bank_account_id="acc-1",
        date=date(2026, 1, 10),
        amount_units="1000",
        direction=Direction.BANK_OUTFLOW,
        currency="MAD",
        description="Test Bank",
    )
    j_item = BookItem(
        id="j1",
        origin_period="2026-01",
        date=date(2026, 1, 10),
        amount_units="1000",
        direction=Direction.BOOK_BANK_CREDIT,
        currency="MAD",
        description="Test Book",
    )
    state, repo = _setup_hydrated_state([b_item], [j_item])
    engine = TransitionEngine(repository=repo)
    try:
        cmd_bad = CreateReconciliationCommand(
            command_id="cmd-bad",
            expected_state_revision=state.revision,
            source=CommandSource.RECONCILIATION,
            session_id="sess-test",
            issued_at=datetime.now(timezone.utc),
            reconciliation_id="rec-bad",
            bank_allocations=(BankAllocation(bank_item_id="b1", amount_units="1000"),),
            book_allocations=(BookAllocation(book_item_id="j1", amount_units="1000"),),
            source_hypothesis_allocation_support=AllocationSupport.INSUFFICIENT_EVIDENCE,
        )
        res_bad = engine.apply(state=state, command=cmd_bad)
        assert res_bad.status == TransitionStatus.REJECTED
        assert res_bad.rejection.code == RejectionCode.INVALID_HYPOTHESIS_PROVENANCE

        cmd_contra = CreateReconciliationCommand(
            command_id="cmd-contra",
            expected_state_revision=state.revision,
            source=CommandSource.RECONCILIATION,
            session_id="sess-test",
            issued_at=datetime.now(timezone.utc),
            reconciliation_id="rec-contra",
            bank_allocations=(BankAllocation(bank_item_id="b1", amount_units="1000"),),
            book_allocations=(BookAllocation(book_item_id="j1", amount_units="1000"),),
            source_hypothesis_allocation_support=AllocationSupport.CONTRADICTED,
        )
        res_contra = engine.apply(state=state, command=cmd_contra)
        assert res_contra.status == TransitionStatus.REJECTED
        assert res_contra.rejection.code == RejectionCode.INVALID_HYPOTHESIS_PROVENANCE
    finally:
        state.close()


def test_transition_handler_accepts_unique_inference_allocation():
    b_item = BankItem(
        id="b1",
        bank_account_id="acc-1",
        date=date(2026, 1, 10),
        amount_units="1000",
        direction=Direction.BANK_OUTFLOW,
        currency="MAD",
        description="Test Bank",
    )
    j_item = BookItem(
        id="j1",
        origin_period="2026-01",
        date=date(2026, 1, 10),
        amount_units="1000",
        direction=Direction.BOOK_BANK_CREDIT,
        currency="MAD",
        description="Test Book",
    )
    state, repo = _setup_hydrated_state([b_item], [j_item])
    engine = TransitionEngine(repository=repo)
    try:
        hyp = ReconciliationHypothesis(
            id="hyp:cand-1",
            eligibility=Eligibility.SELECTABLE,
            admissibility=SemanticAdmissibility.SUPPORTED,
            allocation_support=AllocationSupport.UNIQUE_INFERENCE,
            utility=100,
            semantic_value=100_000,
            bank_allocations=(BankAllocation(bank_item_id="b1", amount_units="1000"),),
            book_allocations=(BookAllocation(book_item_id="j1", amount_units="1000"),),
            state_revision=state.revision,
            generated_at=datetime.now(timezone.utc),
        )
        cmd_ok = CreateReconciliationCommand(
            command_id="cmd-ok",
            expected_state_revision=state.revision,
            source=CommandSource.RECONCILIATION,
            session_id="sess-test",
            issued_at=datetime.now(timezone.utc),
            reconciliation_id="rec-ok",
            bank_allocations=(BankAllocation(bank_item_id="b1", amount_units="1000"),),
            book_allocations=(BookAllocation(book_item_id="j1", amount_units="1000"),),
            source_hypothesis=hyp,
            source_hypothesis_id=hyp.id,
        )
        res_ok = engine.apply(state=state, command=cmd_ok)
        assert res_ok.status == TransitionStatus.APPLIED
        rec = state.reconciliations["rec-ok"]
        assert rec.source_hypothesis_allocation_support == AllocationSupport.UNIQUE_INFERENCE
        assert rec.hypothesis_provenance is not None
        assert rec.hypothesis_provenance.allocation_support == AllocationSupport.UNIQUE_INFERENCE
    finally:
        state.close()


def test_late_evidence_transforms_unique_inference_to_explicit_evidence():
    b_item = BankItem(
        id="b1",
        bank_account_id="acc-1",
        date=date(2026, 1, 12),
        amount_units="50000",
        direction=Direction.BANK_OUTFLOW,
        currency="MAD",
        description="VRT Fournisseur Delta BATCH-DELTA",
        reference="BATCH-DELTA",
    )
    j1 = BookItem(
        id="j1",
        origin_period="2026-01",
        date=date(2026, 1, 10),
        amount_units="20000",
        direction=Direction.BOOK_BANK_CREDIT,
        currency="MAD",
        description="Fournisseur Delta Facture 1",
        reference="DELTA-1",
    )
    j2 = BookItem(
        id="j2",
        origin_period="2026-01",
        date=date(2026, 1, 10),
        amount_units="30000",
        direction=Direction.BOOK_BANK_CREDIT,
        currency="MAD",
        description="Fournisseur Delta Facture 2",
        reference="DELTA-2",
    )
    # Evidence assertions linking each book item to the bank item's reference
    ev1 = BookItemEvidenceAssertion(
        id="ev-1",
        book_item_id="j1",
        session_id="sess-test",
        evidence_type=BookItemEvidenceType.REFERENCE,
        value="BATCH-DELTA",
        confidence=1.0,
        source=EvidenceSource.HUMAN_ASSERTION,
        created_at=datetime(2026, 1, 15, tzinfo=timezone.utc),
    )
    ev2 = BookItemEvidenceAssertion(
        id="ev-2",
        book_item_id="j2",
        session_id="sess-test",
        evidence_type=BookItemEvidenceType.REFERENCE,
        value="BATCH-DELTA",
        confidence=1.0,
        source=EvidenceSource.HUMAN_ASSERTION,
        created_at=datetime(2026, 1, 15, tzinfo=timezone.utc),
    )

    state, _ = _setup_hydrated_state([b_item], [j1, j2], evidence_assertions=[ev1, ev2])
    try:
        queries = BookkeepingQueries(state)
        view = build_reconciliation_view(queries)
        cand = ReconciliationCandidate(
            candidate_id="cand-1-n",
            candidate_type=CandidateType.ONE_TO_MANY,
            bank_allocations=(BankAllocation(bank_item_id="b1", amount_units="50000"),),
            book_allocations=(
                BookAllocation(book_item_id="j1", amount_units="20000"),
                BookAllocation(book_item_id="j2", amount_units="30000"),
            ),
            total_amount_units="50000",
        )
        support, _ = evaluate_allocation_support(cand, view)
        assert support == AllocationSupport.EXPLICIT_EVIDENCE
    finally:
        state.close()


def test_direct_unique_inference_command_rejected_when_policy_false():
    b_item = BankItem(
        id="b1",
        bank_account_id="acc-1",
        date=date(2026, 1, 10),
        amount_units="1000",
        direction=Direction.BANK_OUTFLOW,
        currency="MAD",
        description="Test Bank",
    )
    j_item = BookItem(
        id="j1",
        origin_period="2026-01",
        date=date(2026, 1, 10),
        amount_units="1000",
        direction=Direction.BOOK_BANK_CREDIT,
        currency="MAD",
        description="Test Book",
    )
    state, repo = _setup_hydrated_state(
        [b_item],
        [j_item],
        policy=AccountingPolicy(
            chart_of_accounts_id="pcge",
            reconciliation_date_window_days=45,
            require_exact_currency_match=True,
            allow_partial_book_reconciliation=True,
            allow_partial_bank_reconciliation=True,
            auto_reconcile_unique_inferred_allocation=False,
        ),
    )
    engine = TransitionEngine(repository=repo)
    try:
        hyp = ReconciliationHypothesis(
            id="hyp:cand-1",
            eligibility=Eligibility.SELECTABLE,
            admissibility=SemanticAdmissibility.SUPPORTED,
            allocation_support=AllocationSupport.UNIQUE_INFERENCE,
            utility=100,
            semantic_value=100_000,
            bank_allocations=(BankAllocation(bank_item_id="b1", amount_units="1000"),),
            book_allocations=(BookAllocation(book_item_id="j1", amount_units="1000"),),
            state_revision=state.revision,
            generated_at=datetime.now(timezone.utc),
        )
        cmd = CreateReconciliationCommand(
            command_id="cmd-reject-cons",
            expected_state_revision=state.revision,
            source=CommandSource.RECONCILIATION,
            session_id="sess-test",
            issued_at=datetime.now(timezone.utc),
            reconciliation_id="rec-reject-cons",
            bank_allocations=(BankAllocation(bank_item_id="b1", amount_units="1000"),),
            book_allocations=(BookAllocation(book_item_id="j1", amount_units="1000"),),
            source_hypothesis=hyp,
            source_hypothesis_id=hyp.id,
        )
        res = engine.apply(state=state, command=cmd)
        assert res.status == TransitionStatus.REJECTED
        assert res.rejection.code == RejectionCode.INVALID_ALLOCATION
        assert "auto_reconcile_unique_inferred_allocation" in res.rejection.message or "UNIQUE_INFERENCE" in res.rejection.message
    finally:
        state.close()


def test_direct_unique_inference_command_accepted_when_policy_true():
    b_item = BankItem(
        id="b1",
        bank_account_id="acc-1",
        date=date(2026, 1, 10),
        amount_units="1000",
        direction=Direction.BANK_OUTFLOW,
        currency="MAD",
        description="Test Bank",
    )
    j_item = BookItem(
        id="j1",
        origin_period="2026-01",
        date=date(2026, 1, 10),
        amount_units="1000",
        direction=Direction.BOOK_BANK_CREDIT,
        currency="MAD",
        description="Test Book",
    )
    state, repo = _setup_hydrated_state(
        [b_item],
        [j_item],
        policy=AccountingPolicy(
            chart_of_accounts_id="pcge",
            reconciliation_date_window_days=45,
            require_exact_currency_match=True,
            allow_partial_book_reconciliation=True,
            allow_partial_bank_reconciliation=True,
            auto_reconcile_unique_inferred_allocation=True,
        ),
    )
    engine = TransitionEngine(repository=repo)
    try:
        hyp = ReconciliationHypothesis(
            id="hyp:cand-1",
            eligibility=Eligibility.SELECTABLE,
            admissibility=SemanticAdmissibility.SUPPORTED,
            allocation_support=AllocationSupport.UNIQUE_INFERENCE,
            utility=100,
            semantic_value=100_000,
            bank_allocations=(BankAllocation(bank_item_id="b1", amount_units="1000"),),
            book_allocations=(BookAllocation(book_item_id="j1", amount_units="1000"),),
            state_revision=state.revision,
            generated_at=datetime.now(timezone.utc),
        )
        cmd = CreateReconciliationCommand(
            command_id="cmd-accept-aggr",
            expected_state_revision=state.revision,
            source=CommandSource.RECONCILIATION,
            session_id="sess-test",
            issued_at=datetime.now(timezone.utc),
            reconciliation_id="rec-accept-aggr",
            bank_allocations=(BankAllocation(bank_item_id="b1", amount_units="1000"),),
            book_allocations=(BookAllocation(book_item_id="j1", amount_units="1000"),),
            source_hypothesis=hyp,
            source_hypothesis_id=hyp.id,
        )
        res = engine.apply(state=state, command=cmd)
        assert res.status == TransitionStatus.APPLIED
        rec = state.reconciliations["rec-accept-aggr"]
        assert rec.source_hypothesis_allocation_support == AllocationSupport.UNIQUE_INFERENCE
    finally:
        state.close()


def test_command_allocation_support_disagreement_with_embedded_hypothesis_rejected():
    b_item = BankItem(
        id="b1",
        bank_account_id="acc-1",
        date=date(2026, 1, 10),
        amount_units="1000",
        direction=Direction.BANK_OUTFLOW,
        currency="MAD",
        description="Test Bank",
    )
    j_item = BookItem(
        id="j1",
        origin_period="2026-01",
        date=date(2026, 1, 10),
        amount_units="1000",
        direction=Direction.BOOK_BANK_CREDIT,
        currency="MAD",
        description="Test Book",
    )
    state, repo = _setup_hydrated_state([b_item], [j_item])
    engine = TransitionEngine(repository=repo)
    try:
        hyp = ReconciliationHypothesis(
            id="hyp:cand-1",
            eligibility=Eligibility.SELECTABLE,
            admissibility=SemanticAdmissibility.SUPPORTED,
            allocation_support=AllocationSupport.UNIQUE_INFERENCE,
            utility=100,
            semantic_value=100_000,
            bank_allocations=(BankAllocation(bank_item_id="b1", amount_units="1000"),),
            book_allocations=(BookAllocation(book_item_id="j1", amount_units="1000"),),
            state_revision=state.revision,
            generated_at=datetime.now(timezone.utc),
        )
        cmd = CreateReconciliationCommand(
            command_id="cmd-disagree-embed",
            expected_state_revision=state.revision,
            source=CommandSource.RECONCILIATION,
            session_id="sess-test",
            issued_at=datetime.now(timezone.utc),
            reconciliation_id="rec-disagree-embed",
            bank_allocations=(BankAllocation(bank_item_id="b1", amount_units="1000"),),
            book_allocations=(BookAllocation(book_item_id="j1", amount_units="1000"),),
            source_hypothesis=hyp,
            source_hypothesis_id=hyp.id,
            source_hypothesis_allocation_support=AllocationSupport.EXPLICIT_EVIDENCE,
        )
        res = engine.apply(state=state, command=cmd)
        assert res.status == TransitionStatus.REJECTED
        assert res.rejection.code == RejectionCode.INVALID_ALLOCATION
        assert "disagrees with embedded hypothesis allocation_support" in res.rejection.message
    finally:
        state.close()


def test_command_allocation_support_disagreement_with_state_stored_hypothesis_rejected():
    b_item = BankItem(
        id="b1",
        bank_account_id="acc-1",
        date=date(2026, 1, 10),
        amount_units="1000",
        direction=Direction.BANK_OUTFLOW,
        currency="MAD",
        description="Test Bank",
    )
    j_item = BookItem(
        id="j1",
        origin_period="2026-01",
        date=date(2026, 1, 10),
        amount_units="1000",
        direction=Direction.BOOK_BANK_CREDIT,
        currency="MAD",
        description="Test Book",
    )
    state, repo = _setup_hydrated_state([b_item], [j_item])
    hyp = ReconciliationHypothesis(
        id="hyp:cand-state",
        eligibility=Eligibility.SELECTABLE,
        admissibility=SemanticAdmissibility.SUPPORTED,
        allocation_support=AllocationSupport.UNIQUE_INFERENCE,
        utility=100,
        semantic_value=100_000,
        bank_allocations=(BankAllocation(bank_item_id="b1", amount_units="1000"),),
        book_allocations=(BookAllocation(book_item_id="j1", amount_units="1000"),),
        state_revision=state.revision,
        generated_at=datetime.now(timezone.utc),
    )
    state._put_reconciliation_hypothesis(hyp)
    engine = TransitionEngine(repository=repo)
    try:
        cmd = CreateReconciliationCommand(
            command_id="cmd-disagree-state",
            expected_state_revision=state.revision,
            source=CommandSource.RECONCILIATION,
            session_id="sess-test",
            issued_at=datetime.now(timezone.utc),
            reconciliation_id="rec-disagree-state",
            bank_allocations=(BankAllocation(bank_item_id="b1", amount_units="1000"),),
            book_allocations=(BookAllocation(book_item_id="j1", amount_units="1000"),),
            source_hypothesis_id=hyp.id,
            source_hypothesis_allocation_support=AllocationSupport.EXPLICIT_EVIDENCE,
        )
        res = engine.apply(state=state, command=cmd)
        assert res.status == TransitionStatus.REJECTED
        assert res.rejection.code == RejectionCode.INVALID_ALLOCATION
        assert "disagrees with state-stored hypothesis allocation_support" in res.rejection.message
    finally:
        state.close()


def test_explicit_evidence_accepted_under_conservative_policy():
    b_item = BankItem(
        id="b1",
        bank_account_id="acc-1",
        date=date(2026, 1, 10),
        amount_units="1000",
        direction=Direction.BANK_OUTFLOW,
        currency="MAD",
        description="Test Bank",
    )
    j_item = BookItem(
        id="j1",
        origin_period="2026-01",
        date=date(2026, 1, 10),
        amount_units="1000",
        direction=Direction.BOOK_BANK_CREDIT,
        currency="MAD",
        description="Test Book",
    )
    state, repo = _setup_hydrated_state(
        [b_item],
        [j_item],
        policy=AccountingPolicy(
            chart_of_accounts_id="pcge",
            reconciliation_date_window_days=45,
            require_exact_currency_match=True,
            allow_partial_book_reconciliation=True,
            allow_partial_bank_reconciliation=True,
            auto_reconcile_unique_inferred_allocation=False,
        ),
    )
    engine = TransitionEngine(repository=repo)
    try:
        hyp = ReconciliationHypothesis(
            id="hyp:cand-explicit",
            eligibility=Eligibility.SELECTABLE,
            admissibility=SemanticAdmissibility.SUPPORTED,
            allocation_support=AllocationSupport.EXPLICIT_EVIDENCE,
            utility=100,
            semantic_value=100_000,
            bank_allocations=(BankAllocation(bank_item_id="b1", amount_units="1000"),),
            book_allocations=(BookAllocation(book_item_id="j1", amount_units="1000"),),
            state_revision=state.revision,
            generated_at=datetime.now(timezone.utc),
        )
        cmd = CreateReconciliationCommand(
            command_id="cmd-explicit-cons",
            expected_state_revision=state.revision,
            source=CommandSource.RECONCILIATION,
            session_id="sess-test",
            issued_at=datetime.now(timezone.utc),
            reconciliation_id="rec-explicit-cons",
            bank_allocations=(BankAllocation(bank_item_id="b1", amount_units="1000"),),
            book_allocations=(BookAllocation(book_item_id="j1", amount_units="1000"),),
            source_hypothesis=hyp,
            source_hypothesis_id=hyp.id,
        )
        res = engine.apply(state=state, command=cmd)
        assert res.status == TransitionStatus.APPLIED
        rec = state.reconciliations["rec-explicit-cons"]
        assert rec.source_hypothesis_allocation_support == AllocationSupport.EXPLICIT_EVIDENCE
    finally:
        state.close()


def test_review_only_counterfactual_hypothesis_cannot_become_provenance_directly():
    b_item = BankItem(
        id="b1",
        bank_account_id="acc-1",
        date=date(2026, 1, 10),
        amount_units="1000",
        direction=Direction.BANK_OUTFLOW,
        currency="MAD",
        description="Test Bank",
    )
    j_item = BookItem(
        id="j1",
        origin_period="2026-01",
        date=date(2026, 1, 10),
        amount_units="1000",
        direction=Direction.BOOK_BANK_CREDIT,
        currency="MAD",
        description="Test Book",
    )
    state, repo = _setup_hydrated_state([b_item], [j_item])
    engine = TransitionEngine(repository=repo)
    try:
        hyp = ReconciliationHypothesis(
            id="hyp:cand-review-only",
            eligibility=Eligibility.COUNTERFACTUAL_ONLY,
            admissibility=SemanticAdmissibility.SUPPORTED,
            allocation_support=AllocationSupport.UNIQUE_INFERENCE,
            utility=100,
            semantic_value=100_000,
            bank_allocations=(BankAllocation(bank_item_id="b1", amount_units="1000"),),
            book_allocations=(BookAllocation(book_item_id="j1", amount_units="1000"),),
            state_revision=state.revision,
            generated_at=datetime.now(timezone.utc),
        )
        cmd = CreateReconciliationCommand(
            command_id="cmd-review-only",
            expected_state_revision=state.revision,
            source=CommandSource.RECONCILIATION,
            session_id="sess-test",
            issued_at=datetime.now(timezone.utc),
            reconciliation_id="rec-review-only",
            bank_allocations=(BankAllocation(bank_item_id="b1", amount_units="1000"),),
            book_allocations=(BookAllocation(book_item_id="j1", amount_units="1000"),),
            source_hypothesis=hyp,
            source_hypothesis_id=hyp.id,
        )
        res = engine.apply(state=state, command=cmd)
        assert res.status == TransitionStatus.REJECTED
        assert res.rejection.code == RejectionCode.INVALID_ALLOCATION
        assert "COUNTERFACTUAL_ONLY" in res.rejection.message
    finally:
        state.close()


def test_temporal_challenge_catalog_contains_exactly_8_entries():
    challenges = list_temporal_challenges()
    assert len(challenges) == 8
    expected_ids = {
        "challenge_08_late_evidence_resolution",
        "challenge_09_bank_before_invoice",
        "challenge_10_invoice_before_bank",
        "challenge_11_duplicate_economic_bank_import",
        "challenge_12_invalidate_and_rebuild",
        "challenge_13_correction_migrates_economic_truth",
        "challenge_14_conservative",
        "challenge_14_aggressive",
    }
    assert set(c.challenge_id for c in challenges) == expected_ids


def test_legacy_challenge_14_alias_resolves_without_duplicate_listing():
    challenges = list_temporal_challenges()
    catalog_ids = [c.challenge_id for c in challenges]
    assert "challenge_14_dirty_multi_session_month" not in catalog_ids
    legacy = get_temporal_challenge("challenge_14_dirty_multi_session_month")
    assert legacy.challenge_id == "challenge_14_conservative"
