from __future__ import annotations

import time
from decimal import Decimal
from typing import Any, cast

from django.contrib.auth import get_user_model
from django.core.management.base import BaseCommand, CommandError
from django.db import connection

from bookkeeping_state.domain.enums import Direction
from bookkeeping_state.domain.money import solver_units_to_decimal
from bookkeeping_state.hydration.hydrator import BookkeepingHydrator
from bookkeeping_state.persistence.repository import BookkeepingRepository
from bookkeeping_state.session.result import SessionResult
from bookkeeping_state.session.service import (
    create_production_bookkeeping_application_service,
    run_bookkeeping_session,
)
from bookkeeping_state.state.queries import BookkeepingQueries
from bookkeeping_state.state.validation import validate_state
from ledger.models.bank_account import BankAccountModel
from ledger.models.bill import BillModel
from ledger.models.bookkeeping import (
    BookkeepingPaymentApplication,
    BookkeepingReconciliation,
    BookkeepingResidualBankClassificationDecision,
    BookkeepingResidualBankPosting,
    BookkeepingRevision,
)
from ledger.models.data_import import StagedTransactionModel
from ledger.models.entity import EntityModel
from ledger.models.invoice import InvoiceModel
from ledger.models.journal_entry import JournalEntryModel
from ledger.models.transactions import TransactionModel

DEFAULT_USER_EMAIL = "yankz@fignode.com"
DEFAULT_ENTITY_SLUG = "toro-synthetic-bookkeeping"


class Command(BaseCommand):
    help = (
        "Executes the full production bookkeeping lifecycle end-to-end on real database tables "
        "for the dedicated synthetic company, asserting all accounting invariants."
    )

    def add_arguments(self, parser):
        parser.add_argument(
            "--user-email",
            type=str,
            default=DEFAULT_USER_EMAIL,
            help=f"Canonical owner user email (default: {DEFAULT_USER_EMAIL})",
        )
        parser.add_argument(
            "--entity-slug",
            type=str,
            default=DEFAULT_ENTITY_SLUG,
            help=f"Synthetic company slug (default: {DEFAULT_ENTITY_SLUG})",
        )
        parser.add_argument(
            "--session-id",
            type=str,
            default=None,
            help="Custom session ID (default: auto-generated)",
        )
        parser.add_argument(
            "--nats-url",
            type=str,
            default=None,
            help="Optional NATS server URL (default: from environment/settings)",
        )

    def handle(self, *args, **options):
        user_email: str = options["user_email"]
        entity_slug: str = options["entity_slug"]
        custom_session_id: str | None = options["session_id"]
        nats_url: str | None = options["nats_url"]

        self.stdout.write(self.style.MIGRATE_HEADING("=== Production Bookkeeping Lifecycle E2E Runner ==="))

        # ------------------------------------------------------------------
        # 1. User & Entity Boundary Resolution
        # ------------------------------------------------------------------
        UserModel = get_user_model()
        auth_user = UserModel.objects.filter(email=user_email).first()
        if not auth_user:
            raise CommandError(f"User {user_email} not found in Django auth_user table. Failing closed.")

        entity = EntityModel.objects.filter(slug=entity_slug).first()
        if not entity:
            raise CommandError(
                f"Synthetic company '{entity_slug}' not found. Run 'seed_production_like_bookkeeping_company' first."
            )

        if entity.admin_id != auth_user.pk:
            raise CommandError(
                f"Entity ownership mismatch: Entity '{entity_slug}' is owned by user id {entity.admin_id}, "
                f"expected {auth_user.pk} ({user_email}). Failing closed."
            )

        self.stdout.write(
            f"Resolved entity '{entity.name}' ({entity.uuid}) owned by {user_email}"
        )

        # ------------------------------------------------------------------
        # 2. Preflight Dependency & Staged Data Verification
        # ------------------------------------------------------------------
        staged_qs = StagedTransactionModel.objects.filter(
            import_job__bank_account_model__entity_model=entity
        )
        staged_count = staged_qs.count()
        if staged_count == 0:
            raise CommandError(
                f"Entity '{entity_slug}' has 0 staged bank transactions. Please run seed command first."
            )

        self.stdout.write(f"Preflight: Found {staged_count} staged transactions in real database.")

        session_id = custom_session_id or f"synth-session-{int(time.time())}"
        self.stdout.write(f"Session ID: {session_id}")

        # ------------------------------------------------------------------
        # 3. Execute Canonical Production Bookkeeping Session
        # ------------------------------------------------------------------
        self.stdout.write("\nExecuting BookkeepingSession via production application service...")
        start_time = time.time()

        if nats_url:
            app_service = create_production_bookkeeping_application_service(nats_url=nats_url)
            result: SessionResult = app_service.run_session(
                company_id=str(entity.uuid),
                session_id=session_id,
            )
        else:
            result = run_bookkeeping_session(
                company_id=str(entity.uuid),
                session_id=session_id,
            )

        elapsed = time.time() - start_time
        self.stdout.write(
            self.style.SUCCESS(
                f"Session completed in {elapsed:.2f}s | Success: {result.is_success} | "
                f"Persistence Rev: {result.starting_persistence_revision} -> {result.final_persistence_revision}"
            )
        )

        if not result.is_success:
            raise CommandError(
                f"BookkeepingSession failed at stage '{result.failure_stage}': {result.failure_reason}"
            )

        # ------------------------------------------------------------------
        # 4. Rehydrate Final BookkeepingState & Run Authoritative Validator
        # ------------------------------------------------------------------
        self.stdout.write("\nRehydrating final BookkeepingState from PostgreSQL...")
        repo = BookkeepingRepository()
        hydrator = BookkeepingHydrator(repository=repo)
        final_state = hydrator.hydrate(company_id=str(entity.uuid), session_id=session_id)

        validation_report = validate_state(final_state)
        if not validation_report.is_valid:
            violations_str = "\n".join(f" - {v}" for v in validation_report.errors)
            raise CommandError(f"Authoritative State Validation failed with violations:\n{violations_str}")

        self.stdout.write(self.style.SUCCESS("Authoritative State Validation passed (0 violations)."))

        # ------------------------------------------------------------------
        # 5. Assert Lifecycle Invariants (Cases 1 - 7)
        # ------------------------------------------------------------------
        queries = BookkeepingQueries(final_state)
        self._verify_accounting_invariants(
            entity=entity,
            final_state=final_state,
            queries=queries,
            result=result,
        )

        # ------------------------------------------------------------------
        # 6. Structured Accounting Output Report
        # ------------------------------------------------------------------
        self._print_accounting_report(
            entity=entity,
            session_id=session_id,
            result=result,
            final_state=final_state,
            queries=queries,
        )

        self.stdout.write(
            self.style.SUCCESS("\n[OK] All 7 production bookkeeping cases and accounting invariants verified cleanly.")
        )

    def _verify_accounting_invariants(
        self,
        *,
        entity: EntityModel,
        final_state: Any,
        queries: BookkeepingQueries,
        result: SessionResult,
    ) -> None:
        """Exhaustively asserts that all 7 synthetic cases executed with strict accounting correctness."""
        # 1. Total bank movements
        total_bank_items = len(final_state.bank_items)
        if total_bank_items != 7:
            raise AssertionError(f"Expected 7 bank items in final state, found {total_bank_items}")

        # Map bank items by fit_id (stored in BankItem.reference)
        items_by_fit: dict[str, Any] = {
            b.reference: b
            for b in final_state.bank_items.values()
            if b.reference
        }

        stage1_bank_ids = set(queries.executed_stage1_bank_item_ids())
        stage2_bank_ids = set(queries.executed_stage2_reconciliation_bank_item_ids())

        # --- Case 1: Approved Invoice INV-SYNTH-2026-001 (10,000 MAD) -> Stage 1 Payment applied ---
        c1_item = items_by_fit.get("FIT-SYNTH-CASE1-INVOICE")
        if not c1_item:
            raise AssertionError("Case 1 bank movement FIT-SYNTH-CASE1-INVOICE missing from final state")
        if str(c1_item.id) not in stage1_bank_ids:
            raise AssertionError(f"Case 1 bank item {c1_item.id} was not consumed by Stage 1 payment application")

        inv = InvoiceModel.objects.filter(ledger__entity=entity, invoice_number="INV-SYNTH-2026-001").first()
        assert inv is not None
        if inv.amount_paid < Decimal("10000.00"):
            raise AssertionError(f"Case 1 Invoice {inv.invoice_number} amount_paid is {inv.amount_paid}, expected 10,000.00")

        # --- Case 2: Approved Bill BILL-SYNTH-2026-001 (4,500 MAD) -> Stage 1 Payment applied ---
        c2_item = items_by_fit.get("FIT-SYNTH-CASE2-BILL")
        if not c2_item:
            raise AssertionError("Case 2 bank movement FIT-SYNTH-CASE2-BILL missing from final state")
        if str(c2_item.id) not in stage1_bank_ids:
            raise AssertionError(f"Case 2 bank item {c2_item.id} was not consumed by Stage 1 payment application")

        bill = BillModel.objects.filter(ledger__entity=entity, bill_number="BILL-SYNTH-2026-001").first()
        assert bill is not None
        if bill.amount_paid < Decimal("4500.00"):
            raise AssertionError(f"Case 2 Bill {bill.bill_number} amount_paid is {bill.amount_paid}, expected 4,500.00")

        # --- Case 3: Cash GL deposit (2,500 MAD) -> Stage 2 Reconciled ---
        c3_item = items_by_fit.get("FIT-SYNTH-CASE3-CASHGL")
        if not c3_item:
            raise AssertionError("Case 3 bank movement FIT-SYNTH-CASE3-CASHGL missing from final state")
        if str(c3_item.id) in stage1_bank_ids:
            raise AssertionError(f"Case 3 bank item {c3_item.id} must NOT be consumed in Stage 1")
        if str(c3_item.id) not in stage2_bank_ids:
            raise AssertionError(f"Case 3 bank item {c3_item.id} was not reconciled in Stage 2")

        # --- Case 4: Residual Bank Outflow (3,400 MAD) -> Classified to 4456 (DGI TVA) ---
        c4_item = items_by_fit.get("FIT-SYNTH-CASE4-DGIVAT")
        if not c4_item:
            raise AssertionError("Case 4 bank movement FIT-SYNTH-CASE4-DGIVAT missing from final state")
        if str(c4_item.id) in stage1_bank_ids or str(c4_item.id) in stage2_bank_ids:
            raise AssertionError("Case 4 bank item was prematurely consumed before residual stage")

        c4_decision = next(
            (d for d in final_state.residual_bank_classifications.values() if d.bank_item_id == str(c4_item.id)),
            None,
        )
        if not c4_decision:
            raise AssertionError(f"Case 4 decision missing for bank item {c4_item.id}")
        if c4_decision.status.value != "CLASSIFIED":
            raise AssertionError(f"Case 4 expected CLASSIFIED, got {c4_decision.status.value}")
        if c4_decision.account_code != "4456":
            raise AssertionError(f"Case 4 expected account code '4456' (DGI TVA), got {c4_decision.account_code}")

        c4_posting = final_state.get_residual_bank_posting_for_decision(c4_decision.id)
        if not c4_posting:
            raise AssertionError(f"Case 4 missing BookkeepingResidualBankPosting for decision {c4_decision.id}")

        self._assert_posting_journal_entry(
            posting_id=c4_posting.id,
            expected_amount=Decimal("3400.00"),
            expected_dr_code="4456",
            expected_cr_code="5141",
            description_substr="TVA",
        )

        # --- Case 5: Residual Bank Outflow (150 MAD) -> Classified to 6147 (Bank fee) ---
        c5_item = items_by_fit.get("FIT-SYNTH-CASE5-BANKFEE")
        if not c5_item:
            raise AssertionError("Case 5 bank movement FIT-SYNTH-CASE5-BANKFEE missing from final state")

        c5_decision = next(
            (d for d in final_state.residual_bank_classifications.values() if d.bank_item_id == str(c5_item.id)),
            None,
        )
        if not c5_decision or c5_decision.status.value != "CLASSIFIED":
            raise AssertionError(f"Case 5 expected CLASSIFIED, got {c5_decision}")
        if c5_decision.account_code != "6147":
            raise AssertionError(f"Case 5 expected account code '6147' (Bank fee), got {c5_decision.account_code}")

        c5_posting = final_state.get_residual_bank_posting_for_decision(c5_decision.id)
        if not c5_posting:
            raise AssertionError(f"Case 5 missing residual posting for decision {c5_decision.id}")

        self._assert_posting_journal_entry(
            posting_id=c5_posting.id,
            expected_amount=Decimal("150.00"),
            expected_dr_code="6147",
            expected_cr_code="5141",
            description_substr="COMMISSION",
        )

        # --- Case 6: Residual Bank Inflow (1,200 MAD) -> Classified to 7127 (Misc income) ---
        c6_item = items_by_fit.get("FIT-SYNTH-CASE6-MISCINC")
        if not c6_item:
            raise AssertionError("Case 6 bank movement FIT-SYNTH-CASE6-MISCINC missing from final state")

        c6_decision = next(
            (d for d in final_state.residual_bank_classifications.values() if d.bank_item_id == str(c6_item.id)),
            None,
        )
        if not c6_decision or c6_decision.status.value != "CLASSIFIED":
            raise AssertionError(f"Case 6 expected CLASSIFIED, got {c6_decision}")
        if c6_decision.account_code not in ("7127", "7111", "7124"):
            raise AssertionError(f"Case 6 expected Moroccan PCGE revenue account (71xx), got {c6_decision.account_code}")

        c6_posting = final_state.get_residual_bank_posting_for_decision(c6_decision.id)
        if not c6_posting:
            raise AssertionError(f"Case 6 missing residual posting for decision {c6_decision.id}")

        self._assert_posting_journal_entry(
            posting_id=c6_posting.id,
            expected_amount=Decimal("1200.00"),
            expected_dr_code="5141",  # Bank Inflow: Debit Bank Asset
            expected_cr_code=c6_decision.account_code,  # Credit Revenue
            description_substr="ACCESSOIRES",
        )

        # --- Case 7: Ambiguous Bank Movement (7,850 MAD) -> Decision HOLD, 0 Postings ---
        c7_item = items_by_fit.get("FIT-SYNTH-CASE7-HOLD")
        if not c7_item:
            raise AssertionError("Case 7 bank movement FIT-SYNTH-CASE7-HOLD missing from final state")

        c7_decision = next(
            (d for d in final_state.residual_bank_classifications.values() if d.bank_item_id == str(c7_item.id)),
            None,
        )
        if not c7_decision:
            raise AssertionError(f"Case 7 decision missing for bank item {c7_item.id}")
        if c7_decision.status.value != "HOLD":
            raise AssertionError(f"Case 7 expected HOLD, got {c7_decision.status.value} ({c7_decision.account_code})")

        c7_posting = final_state.get_residual_bank_posting_for_decision(c7_decision.id)
        if c7_posting is not None:
            raise AssertionError(f"Case 7 HOLD decision {c7_decision.id} must NEVER produce a residual posting!")

        # Total residual postings in real database must be exactly 3
        durable_postings_count = BookkeepingResidualBankPosting.objects.filter(entity=entity).count()
        if durable_postings_count != 3:
            raise AssertionError(f"Expected exactly 3 durable BookkeepingResidualBankPosting records, found {durable_postings_count}")

    def _assert_posting_journal_entry(
        self,
        *,
        posting_id: str,
        expected_amount: Decimal,
        expected_dr_code: str,
        expected_cr_code: str,
        description_substr: str,
    ) -> None:
        """Verifies that the PostgreSQL JournalEntry and TransactionModel legs adhere strictly to double-entry rules."""
        durable_posting = BookkeepingResidualBankPosting.objects.filter(id=posting_id).first()
        if not durable_posting:
            raise AssertionError(f"Durable BookkeepingResidualBankPosting {posting_id} not found in DB")

        je = durable_posting.journal_entry
        if not je.posted:
            raise AssertionError(f"JournalEntry {je.uuid} for posting {posting_id} is not marked posted=True")

        txs = list(je.transactionmodel_set.all())
        if len(txs) != 2:
            raise AssertionError(f"JournalEntry {je.uuid} has {len(txs)} legs, expected 2 (one Dr, one Cr)")

        dr_tx = next((t for t in txs if t.tx_type == "debit"), None)
        cr_tx = next((t for t in txs if t.tx_type == "credit"), None)

        if not dr_tx or not cr_tx:
            raise AssertionError(f"JournalEntry {je.uuid} missing either debit or credit leg")

        if dr_tx.account.code != expected_dr_code:
            raise AssertionError(f"Posting {posting_id} expected Dr {expected_dr_code}, got {dr_tx.account.code}")
        if cr_tx.account.code != expected_cr_code:
            raise AssertionError(f"Posting {posting_id} expected Cr {expected_cr_code}, got {cr_tx.account.code}")

        if abs(dr_tx.amount - expected_amount) > Decimal("0.01"):
            raise AssertionError(f"Posting {posting_id} Dr amount {dr_tx.amount} != expected {expected_amount}")
        if abs(cr_tx.amount - expected_amount) > Decimal("0.01"):
            raise AssertionError(f"Posting {posting_id} Cr amount {cr_tx.amount} != expected {expected_amount}")

    def _print_accounting_report(
        self,
        *,
        entity: EntityModel,
        session_id: str,
        result: SessionResult,
        final_state: Any,
        queries: BookkeepingQueries,
    ) -> None:
        """Renders an authoritative, formatted accounting summary of the run."""
        self.stdout.write("\n" + "=" * 80)
        self.stdout.write(f"           TORO SYNTHETIC BOOKKEEPING AUDIT REPORT: {entity.name}")
        self.stdout.write("=" * 80)
        self.stdout.write(f"Company UUID:          {entity.uuid}")
        self.stdout.write(f"Session ID:            {session_id}")
        self.stdout.write(f"Persistence Revision:  {result.starting_persistence_revision} -> {result.final_persistence_revision}")
        self.stdout.write(f"State Revision:        {final_state.revision}")
        self.stdout.write("-" * 80)

        # Stage 1 summary
        stage1_ids = queries.executed_stage1_bank_item_ids()
        self.stdout.write(f"Stage 1 Settlement:    {len(stage1_ids)} bank movements applied to obligations")
        for pa in final_state.executed_payment_applications.values():
            for alloc in pa.allocations:
                obligation = final_state.book_items.get(alloc.obligation_book_item_id)
                kind = "Obligation"
                doc_ref = alloc.obligation_book_item_id
                if obligation:
                    kind = "Invoice" if obligation.direction == Direction.BOOK_BANK_DEBIT else "Bill"
                    doc_ref = obligation.reference or obligation.id
                amt = solver_units_to_decimal(alloc.amount_units)
                self.stdout.write(f"  * Applied {amt} MAD to {kind} {doc_ref}")

        # Stage 2 summary
        stage2_ids = queries.executed_stage2_reconciliation_bank_item_ids()
        self.stdout.write(f"Stage 2 Reconciliation: {len(stage2_ids)} bank movements matched to pre-posted Cash GL")
        for rec in final_state.reconciliations.values():
            if queries.get_residual_bank_posting_for_reconciliation(rec.id) is None:
                b_amt = sum(solver_units_to_decimal(b.amount_units) for b in rec.bank_allocations)
                self.stdout.write(f"  * Reconciled {b_amt} MAD across {len(rec.book_allocations)} GL legs")

        # Residual summary
        self.stdout.write(
            f"Residual Bank Wave:    {len(final_state.residual_bank_classifications)} decisions "
            f"({result.residual_classified_count} CLASSIFIED, {result.residual_hold_count} HOLD)"
        )
        for dec in final_state.residual_bank_classifications.values():
            posting = final_state.get_residual_bank_posting_for_decision(dec.id)
            posting_status = f"POSTED ({posting.journal_entry_id})" if posting else "NO POSTING (HOLD)"
            conf_str = f"{dec.confidence:.2f}" if dec.confidence is not None else "N/A"
            self.stdout.write(
                f"  * Bank Item {dec.bank_item_id[:8]}... -> {dec.status.value} "
                f"[{dec.account_code or 'N/A'}] (conf: {conf_str}) -> {posting_status}"
            )

        self.stdout.write("=" * 80)
