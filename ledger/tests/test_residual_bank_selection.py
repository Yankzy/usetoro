from __future__ import annotations

from datetime import date, datetime, timezone
import uuid

from django.test import SimpleTestCase

from bookkeeping_state.bank_categorization.selection import (
    ResidualBankInvariantCorruptionError,
    get_unposted_classified_residual_decisions,
    residual_bank_items_needing_semantic_evaluation,
)
from bookkeeping_state.bank_categorization.transport_models import (
    BankCategorizeRequestEnvelope,
    compute_canonical_bank_payload_digest,
)
from bookkeeping_state.bank_categorization.view import (
    build_residual_bank_categorization_view,
)
from bookkeeping_state.domain.bank import BankAccount, BankItem
from bookkeeping_state.domain.context import (
    AccountingPolicy,
    BookkeepingContext,
    RuntimeContext,
)
from bookkeeping_state.domain.enums import Direction, SourceType
from bookkeeping_state.domain.payment_application import (
    ExecutedPaymentAllocation,
    ExecutedPaymentApplication,
)
from bookkeeping_state.domain.residual_bank_classifications import (
    ResidualBankClassificationDecision,
    ResidualBankClassificationInvalidation,
    ResidualBankClassificationStatus,
    ResidualBankPosting,
)
from bookkeeping_state.state.bookkeeping_state import BookkeepingState
from bookkeeping_state.state.queries import BookkeepingQueries


def _make_bank_account(account_id: str = "ba-1") -> BankAccount:
    return BankAccount(
        id=account_id,
        name="Checking",
        currency="MAD",
    )


def _make_bank_item(
    item_id: str = "item-1",
    account_id: str = "ba-1",
    amount_units: int = 100_0000,
    item_date: date | None = None,
    direction: Direction = Direction.OUTFLOW,
) -> BankItem:
    if item_id.startswith("staged:"):
        bid = item_id
    else:
        bid = f"staged:{uuid.uuid5(uuid.NAMESPACE_DNS, item_id)}"
    return BankItem(
        id=bid,
        bank_account_id=account_id,
        source_type=SourceType.BANK_STATEMENT_LINE,
        date=item_date or date(2026, 4, 10),
        amount_units=str(amount_units),
        direction=direction,
        currency="MAD",
        description="Residual item test",
    )


def _make_decision(
    decision_id: str,
    bank_item: BankItem,
    status: ResidualBankClassificationStatus,
    account_code: str | None = None,
    account_id: str | None = None,
    hold_reason: str | None = None,
    supersedes_decision_id: str | None = None,
    residual_amount_units: int | None = None,
) -> ResidualBankClassificationDecision:
    now = datetime(2026, 4, 10, 12, 0, 0, tzinfo=timezone.utc)
    return ResidualBankClassificationDecision(
        id=decision_id,
        bank_item_id=bank_item.id,
        staged_transaction_id=f"staged:{bank_item.id}",
        bank_account_id=bank_item.bank_account_id,
        status=status,
        account_code=account_code,
        account_id=account_id,
        original_amount_units=int(bank_item.amount_units),
        residual_amount_units=residual_amount_units or int(bank_item.amount_units),
        direction=bank_item.direction,
        currency=bank_item.currency,
        confidence=1.0 if status == ResidualBankClassificationStatus.CLASSIFIED else None,
        rationale="Test rationale",
        hold_reason=hold_reason,
        schema_version="bookkeeping.ase.bank_categorize.v1",
        dag_id="bank-cash-accounting-dag",
        request_semantic_digest="digest-1234567890",
        state_revision_at_decision=1,
        persistence_revision_at_decision=1,
        supersedes_decision_id=supersedes_decision_id,
        created_at=now,
    )


def _make_invalidation(
    invalidation_id: str,
    classification_id: str,
) -> ResidualBankClassificationInvalidation:
    return ResidualBankClassificationInvalidation(
        id=invalidation_id,
        classification_id=classification_id,
        reason="Test invalidation",
        state_revision_at_invalidation=2,
        persistence_revision_at_invalidation=2,
        created_at=datetime(2026, 4, 10, 13, 0, 0, tzinfo=timezone.utc),
    )


def _make_posting(
    posting_id: str,
    decision_id: str,
    bank_item_id: str,
) -> ResidualBankPosting:
    return ResidualBankPosting(
        id=posting_id,
        decision_id=decision_id,
        staged_transaction_id=f"staged:{bank_item_id}",
        bank_item_id=bank_item_id,
        journal_entry_id="je-123",
        bank_cash_transaction_id="tx-cash-1",
        contra_transaction_id="tx-contra-1",
        reconciliation_id="reco-1",
        persistence_revision=3,
        posted_at=datetime(2026, 4, 10, 14, 0, 0, tzinfo=timezone.utc),
    )


class ResidualBankSelectionTests(SimpleTestCase):
    """
    Test suite for residual bank semantic eligibility and posting work selection:
    Tests A through I from Task 27B specification.
    """

    def setUp(self) -> None:
        self.context = BookkeepingContext(
            company_id=str(uuid.uuid4()),
            period_start=date(2026, 1, 1),
            period_end=date(2026, 12, 31),
            base_currency="MAD",
            policy=AccountingPolicy(chart_of_accounts_id="coa-1"),
        )
        self.runtime = RuntimeContext(
            session_id="test-session-1",
            state_revision=1,
            persistence_revision=1,
            hydrated_at=datetime(2026, 4, 10, 10, 0, 0, tzinfo=timezone.utc),
        )
        self.bank_account = _make_bank_account("ba-1")

    def _build_state(
        self,
        bank_items: list[BankItem],
        decisions: list[ResidualBankClassificationDecision] | None = None,
        invalidations: list[ResidualBankClassificationInvalidation] | None = None,
        postings: list[ResidualBankPosting] | None = None,
        executed_payment_applications: list[ExecutedPaymentApplication] | None = None,
    ) -> BookkeepingState:
        return BookkeepingState(
            context=self.context,
            runtime=self.runtime,
            bank_accounts=[self.bank_account],
            bank_items=bank_items,
            residual_bank_classifications=decisions or [],
            residual_bank_classification_invalidations=invalidations or [],
            residual_bank_postings=postings or [],
            executed_payment_applications=executed_payment_applications or [],
        )

    # ------------------------------------------------------------------
    # Test A: Fresh residual => semantic candidate, supersedes_decision_id=None
    # ------------------------------------------------------------------
    def test_a_fresh_residual_yields_semantic_candidate(self) -> None:
        item = _make_bank_item("item-a")
        state = self._build_state([item])
        queries = BookkeepingQueries(state)

        candidates = residual_bank_items_needing_semantic_evaluation(queries)
        self.assertEqual(len(candidates), 1)
        self.assertEqual(candidates[0].bank_item.id, item.id)
        self.assertEqual(candidates[0].remaining_units, 100_0000)
        self.assertIsNone(candidates[0].supersedes_decision_id)

    # ------------------------------------------------------------------
    # Test B: Active HOLD => not semantic candidate, leave unresolved
    # ------------------------------------------------------------------
    def test_b_active_hold_excluded_from_semantic_evaluation(self) -> None:
        item = _make_bank_item("item-b")
        decision = _make_decision(
            "dec-b",
            item,
            status=ResidualBankClassificationStatus.HOLD,
            hold_reason="Need invoice",
        )
        state = self._build_state([item], decisions=[decision])
        queries = BookkeepingQueries(state)

        candidates = residual_bank_items_needing_semantic_evaluation(queries)
        self.assertEqual(len(candidates), 0)

        # Also verify it's not a posting candidate
        posting_work = get_unposted_classified_residual_decisions(queries)
        self.assertEqual(len(posting_work), 0)

    # ------------------------------------------------------------------
    # Test C: Active unposted CLASSIFIED => not ASE candidate, is posting candidate
    # ------------------------------------------------------------------
    def test_c_active_unposted_classified_selected_for_posting(self) -> None:
        item = _make_bank_item("item-c")
        decision = _make_decision(
            "dec-c",
            item,
            status=ResidualBankClassificationStatus.CLASSIFIED,
            account_code="6111",
            account_id="acc-uuid-1",
        )
        state = self._build_state([item], decisions=[decision])
        queries = BookkeepingQueries(state)

        # Excluded from semantic evaluation
        candidates = residual_bank_items_needing_semantic_evaluation(queries)
        self.assertEqual(len(candidates), 0)

        # Included in posting work
        posting_work = get_unposted_classified_residual_decisions(queries)
        self.assertEqual(len(posting_work), 1)
        self.assertEqual(posting_work[0].bank_item_id, item.id)
        self.assertEqual(posting_work[0].id, "dec-c")

    # ------------------------------------------------------------------
    # Test D: Invalidated unposted latest tip => candidate with supersedes ID
    # ------------------------------------------------------------------
    def test_d_invalidated_unposted_tip_yields_candidate_with_supersedes_id(self) -> None:
        item = _make_bank_item("item-d")
        decision = _make_decision(
            "dec-d",
            item,
            status=ResidualBankClassificationStatus.HOLD,
            hold_reason="Need invoice",
        )
        invalidation = _make_invalidation("inv-d", "dec-d")
        state = self._build_state(
            [item],
            decisions=[decision],
            invalidations=[invalidation],
        )
        queries = BookkeepingQueries(state)

        candidates = residual_bank_items_needing_semantic_evaluation(queries)
        self.assertEqual(len(candidates), 1)
        self.assertEqual(candidates[0].bank_item.id, item.id)
        self.assertEqual(candidates[0].supersedes_decision_id, "dec-d")

        # Not in posting work
        posting_work = get_unposted_classified_residual_decisions(queries)
        self.assertEqual(len(posting_work), 0)

    # ------------------------------------------------------------------
    # Test E: Posted decision + economic residual => Case E loud failure
    # ------------------------------------------------------------------
    def test_e_posted_decision_with_economic_residual_fails_loudly(self) -> None:
        item = _make_bank_item("item-e")
        decision = _make_decision(
            "dec-e",
            item,
            status=ResidualBankClassificationStatus.CLASSIFIED,
            account_code="6111",
            account_id="acc-uuid-1",
        )
        posting = _make_posting("post-e", "dec-e", "item-e")
        # Item is still economically residual because remaining_units > 0 (no reconciliation)
        state = self._build_state([item], decisions=[decision], postings=[posting])
        queries = BookkeepingQueries(state)

        # Both selectors must fail loudly with ResidualBankInvariantCorruptionError
        with self.assertRaises(ResidualBankInvariantCorruptionError) as cm_semantic:
            residual_bank_items_needing_semantic_evaluation(queries)
        self.assertIn(item.id, str(cm_semantic.exception))
        self.assertIn("dec-e", str(cm_semantic.exception))

        with self.assertRaises(ResidualBankInvariantCorruptionError) as cm_posting:
            get_unposted_classified_residual_decisions(queries)
        self.assertIn(item.id, str(cm_posting.exception))
        self.assertIn("dec-e", str(cm_posting.exception))

    # ------------------------------------------------------------------
    # Test F: Non-residual => ignored
    # ------------------------------------------------------------------
    def test_f_non_residual_bank_item_ignored(self) -> None:
        item = _make_bank_item("item-f", amount_units=500_0000)
        pa = ExecutedPaymentApplication(
            id="pa-f",
            company_id=str(self.context.company_id),
            bank_item_id=item.id,
            direction=Direction.OUTFLOW,
            currency="MAD",
            total_amount_units=str(500_0000),
            allocations=(
                ExecutedPaymentAllocation(
                    obligation_book_item_id="bill-f",
                    cash_transaction_book_item_id="tx-cash-f",
                    amount_units=str(500_0000),
                ),
            ),
            state_revision_at_creation=1,
            created_at=datetime(2026, 4, 10, 11, 0, 0, tzinfo=timezone.utc),
        )
        state = self._build_state([item], executed_payment_applications=[pa])
        queries = BookkeepingQueries(state)

        self.assertEqual(len(queries.residual_unmatched_bank_items()), 0)

        candidates = residual_bank_items_needing_semantic_evaluation(queries)
        self.assertEqual(len(candidates), 0)

        posting_work = get_unposted_classified_residual_decisions(queries)
        self.assertEqual(len(posting_work), 0)

    # ------------------------------------------------------------------
    # Test G: Filtered view contains only explicit candidate subset
    # ------------------------------------------------------------------
    def test_g_filtered_view_contains_only_candidate_subset(self) -> None:
        item1 = _make_bank_item("item-g1", amount_units=100_0000)
        item2 = _make_bank_item("item-g2", amount_units=200_0000)
        item3 = _make_bank_item("item-g3", amount_units=300_0000)

        state = self._build_state([item1, item2, item3])
        queries = BookkeepingQueries(state)

        # Default includes all 3
        full_view = build_residual_bank_categorization_view(queries)
        self.assertEqual(full_view.item_count, 3)

        # Explicit subset with only item2
        subset_view = build_residual_bank_categorization_view(
            queries,
            candidate_items=[(item2, 200_0000)],
        )
        self.assertEqual(subset_view.item_count, 1)
        self.assertEqual(subset_view.items[0].bank_item_id, item2.id)
        self.assertEqual(subset_view.items[0].residual_amount_units, 200_0000)

    # ------------------------------------------------------------------
    # Test H: Supersedes ID does not enter Go wire payload
    # ------------------------------------------------------------------
    def test_h_supersedes_id_not_in_wire_payload(self) -> None:
        item = _make_bank_item("item-h")
        state = self._build_state([item])
        queries = BookkeepingQueries(state)

        view = build_residual_bank_categorization_view(
            queries,
            candidate_items=[(item, 100_0000)],
        )
        envelope = BankCategorizeRequestEnvelope.from_view(
            view,
            request_id="req-h",
            idempotency_key="idem-h",
            requested_at=datetime.now(timezone.utc).isoformat(),
        )
        self.assertEqual(len(envelope.bank_items), 1)
        payload_dict = envelope.bank_items[0].model_dump()

        self.assertNotIn("supersedes_decision_id", payload_dict)
        self.assertEqual(payload_dict["bank_item_id"], item.id)

    # ------------------------------------------------------------------
    # Test I: Request digest reflects filtered subset only
    # ------------------------------------------------------------------
    def test_i_request_digest_binds_exact_subset(self) -> None:
        item1 = _make_bank_item("item-i1")
        item2 = _make_bank_item("item-i2")
        state = self._build_state([item1, item2])
        queries = BookkeepingQueries(state)

        view_both = build_residual_bank_categorization_view(
            queries,
            candidate_items=[(item1, 100_0000), (item2, 100_0000)],
        )
        view_only1 = build_residual_bank_categorization_view(
            queries,
            candidate_items=[(item1, 100_0000)],
        )

        digest_both = compute_canonical_bank_payload_digest(
            schema_version="bookkeeping.ase.bank_categorize.v1",
            dag_id="bank-cash-accounting-dag",
            company_id=str(self.context.company_id),
            session_id=str(self.runtime.session_id),
            state_revision=self.runtime.state_revision,
            persistence_revision=self.runtime.persistence_revision,
            bank_items=view_both.items,
        )

        digest_only1 = compute_canonical_bank_payload_digest(
            schema_version="bookkeeping.ase.bank_categorize.v1",
            dag_id="bank-cash-accounting-dag",
            company_id=str(self.context.company_id),
            session_id=str(self.runtime.session_id),
            state_revision=self.runtime.state_revision,
            persistence_revision=self.runtime.persistence_revision,
            bank_items=view_only1.items,
        )

        self.assertNotEqual(digest_both, digest_only1)
