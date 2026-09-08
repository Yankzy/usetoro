from __future__ import annotations

from datetime import date
import pytest

from bookkeeping_state_eval.dag.adapter import (
    dag_outputs_to_transition_batch,
    dag_plan_to_transition_batch,
)
from bookkeeping_state_eval.dag.models import (
    DagBatchPlan,
    DagClassificationItem,
)
from bookkeeping_state_eval.dag.simulated_ase import SimulatedAseClassifier
from bookkeeping_state_eval.dag.view import build_dag_view
from bookkeeping_state_eval.domain.books import BookItem
from bookkeeping_state_eval.domain.classifications import ClassificationSource
from bookkeeping_state_eval.domain.commands import CommandSource
from bookkeeping_state_eval.domain.enums import Direction
from bookkeeping_state_eval.hydration.hydrator import BookkeepingHydrator
from bookkeeping_state_eval.persistence.in_memory import (
    InMemoryBookkeepingRepository,
)
from bookkeeping_state_eval.persistence.repository import (
    BookkeepingSnapshot,
)
from bookkeeping_state_eval.state.queries import BookkeepingQueries
from bookkeeping_state_eval.transitions.batch import (
    TransitionBatchError,
)
from bookkeeping_state_eval.transitions.engine import (
    TransitionEngine,
)
from tests.factories import (
    FIXED_TIME,
    account,
    bank_item,
    classification,
    context,
)


def _setup_world(
    *,
    books: tuple = (),
    classifications: tuple = (),
):
    account_obj = account("acc-1")
    snap = BookkeepingSnapshot(
        persistence_revision=0,
        context=context(),
        bank_accounts=(account_obj,),
        bank_items=(bank_item("bank-1", account_id="acc-1"),),
        book_items=books,
        classifications=classifications,
    )
    repo = InMemoryBookkeepingRepository(initial_snapshots=[snap])
    hydrator = BookkeepingHydrator(
        repository=repo,
        clock=lambda: FIXED_TIME,
        session_id_factory=lambda: "session-dag-app",
    )
    live = hydrator.hydrate(company_id="atlas")
    engine = TransitionEngine(repository=repo)
    return live, engine, repo, hydrator


class TestDagApplication:
    def test_simulated_ase_rule_matching(self):
        b1 = BookItem(
            id="book-1",
            origin_period="2026-01",
            date=date(2026, 1, 15),
            amount_units="3500000",
            direction=Direction.BOOK_BANK_DEBIT,
            currency="MAD",
            description="Monthly Payroll Jan 2026",
        )
        b2 = BookItem(
            id="book-2",
            origin_period="2026-01",
            date=date(2026, 1, 15),
            amount_units="420000",
            direction=Direction.BOOK_BANK_DEBIT,
            currency="MAD",
            description="AWS EMEA Server Hosting",
        )
        b3 = BookItem(
            id="book-3",
            origin_period="2026-01",
            date=date(2026, 1, 15),
            amount_units="9500000",
            direction=Direction.BOOK_BANK_CREDIT,
            currency="MAD",
            description="Reglement client facture 1042",
        )
        b4 = BookItem(
            id="book-4",
            origin_period="2026-01",
            date=date(2026, 1, 15),
            amount_units="150000",
            direction=Direction.BOOK_BANK_DEBIT,
            currency="MAD",
            description="Misc hardware screws",
        )

        live, _, _, _ = _setup_world(books=(b1, b2, b3, b4))
        q = BookkeepingQueries(live)
        view = build_dag_view(q)

        classifier = SimulatedAseClassifier()
        plan = classifier.classify_view(view)

        assert len(plan.items) == 3
        assert len(plan.hold_items) == 1
        items_by_book = {item.book_item_id: item for item in plan.items}

        assert items_by_book["book-1"].account_code == "6171"
        assert items_by_book["book-1"].ase_node_id == "payroll_node"
        assert items_by_book["book-1"].confidence == 0.99

        assert items_by_book["book-2"].account_code == "6181"
        assert items_by_book["book-2"].ase_node_id == "it_software_node"

        assert items_by_book["book-3"].account_code == "3421"
        assert items_by_book["book-3"].ase_node_id == "customer_receipt_node"

        hold = plan.hold_items[0]
        assert hold.book_item_id == "book-4"
        assert hold.reason == "HOLD_INSUFFICIENT_EVIDENCE"
        assert hold.ase_node_id == "hold_node"

    def test_duplicate_book_item_in_dag_batch_rejected(self):
        item1 = DagClassificationItem(book_item_id="book-1", account_code="6171")
        item2 = DagClassificationItem(book_item_id="book-1", account_code="6181")

        with pytest.raises(TransitionBatchError, match="multiple classifications for book_item 'book-1'"):
            dag_outputs_to_transition_batch(
                items=[item1, item2],
                session_id="session-dag",
                expected_state_revision=0,
                batch_id="batch-dup",
                issued_at=FIXED_TIME,
            )

    def test_end_to_end_dag_batch_application_and_hydration(self):
        b1 = BookItem(
            id="book-1",
            origin_period="2026-01",
            date=date(2026, 1, 15),
            amount_units="3500000",
            direction=Direction.BOOK_BANK_DEBIT,
            currency="MAD",
            description="Payroll Jan 2026",
        )
        b2 = BookItem(
            id="book-2",
            origin_period="2026-01",
            date=date(2026, 1, 15),
            amount_units="420000",
            direction=Direction.BOOK_BANK_DEBIT,
            currency="MAD",
            description="AWS EMEA",
        )

        live, engine, repo, hydrator = _setup_world(books=(b1, b2))
        q_initial = BookkeepingQueries(live)
        assert len(q_initial.unclassified_book_items()) == 2

        view = build_dag_view(q_initial)
        classifier = SimulatedAseClassifier()
        plan = classifier.classify_view(view)
        batch = dag_plan_to_transition_batch(plan, issued_at=FIXED_TIME)

        # Apply atomically as single sibling transition: S0 + {cls1, cls2} -> S1
        result = engine.apply_batch(state=live, batch=batch)

        assert result.is_applied
        assert not result.is_noop
        assert not result.is_rejected
        assert live.revision == 1
        assert live.persistence_revision == 1
        assert result.resulting_state_revision == 1

        # Verify live queries
        q_after = BookkeepingQueries(live)
        assert len(q_after.unclassified_book_items()) == 0
        cls1 = q_after.active_classification("book-1")
        assert cls1 is not None
        assert cls1.account_code == "6171"
        assert cls1.source == ClassificationSource.ASE_DAG

        cls2 = q_after.active_classification("book-2")
        assert cls2 is not None
        assert cls2.account_code == "6181"

        # Verify durable persistence and clean rehydration
        rehydrated = hydrator.hydrate(company_id="atlas")
        assert rehydrated.persistence_revision == 1
        q_rehydrated = BookkeepingQueries(rehydrated)
        assert len(q_rehydrated.unclassified_book_items()) == 0
        assert q_rehydrated.active_classification("book-1").account_code == "6171"
        assert q_rehydrated.active_classification("book-2").account_code == "6181"

    def test_dag_reclassification_supersedes_existing(self):
        b1 = BookItem(
            id="book-1",
            origin_period="2026-01",
            date=date(2026, 1, 15),
            amount_units="500000",
            direction=Direction.BOOK_BANK_DEBIT,
            currency="MAD",
            description="Office Supplies",
        )
        old_cls = classification("cls-old-1", book_item_id="book-1", account_code="6111")

        live, engine, repo, _ = _setup_world(books=(b1,), classifications=(old_cls,))
        q = BookkeepingQueries(live)
        assert q.active_classification("book-1").account_code == "6111"

        # Build view including already classified items
        view = build_dag_view(q, include_already_classified=True)
        classifier = SimulatedAseClassifier()
        plan = classifier.classify_view(view, only_unclassified=False)

        assert len(plan.items) == 1
        assert plan.items[0].supersedes_classification_id == "cls-old-1"
        assert plan.items[0].account_code == "6125"  # Office Supplies

        batch = dag_plan_to_transition_batch(plan, issued_at=FIXED_TIME)
        result = engine.apply_batch(state=live, batch=batch)

        assert result.is_applied
        q_after = BookkeepingQueries(live)
        active = q_after.active_classification("book-1")
        assert active.account_code == "6125"
        assert active.supersedes_classification_id == "cls-old-1"
