from __future__ import annotations

import json
from datetime import date, datetime, timezone
from decimal import Decimal
import pytest

from bookkeeping_state.dag.nats_book_categorizer import (
    BOOK_CATEGORIZATION_DAG_ID,
    BOOK_CATEGORIZATION_SCHEMA_VERSION,
)
from bookkeeping_state.dag.transport_models import (
    BOOK_CATEGORIZE_SCHEMA_VERSION,
    BookCategorizeItemPayload,
    BookCategorizeRequestEnvelope,
    compute_canonical_payload_digest,
    compute_canonical_semantic_dict,
)
from bookkeeping_state.dag.view import DagView, DagViewItem, build_dag_view
from bookkeeping_state.domain.books import BookItem
from bookkeeping_state.domain.enums import (
    BookkeepingRole,
    Direction,
    SourceArtifactKind,
    SourceType,
)
from bookkeeping_state.hydration.hydrator import BookkeepingHydrator
from bookkeeping_state.persistence.reader import read_django_snapshot
from bookkeeping_state.state.queries import BookkeepingQueries


# ---------------------------------------------------------------------------
# Unit / Wire Transport Tests (Pure Python, fast)
# ---------------------------------------------------------------------------

class TestBookItemSourceWireTransport:
    """Tests wire serialization, deserialization, and canonical digests for source semantics."""

    def test_item_payload_roundtrip(self):
        payload = BookCategorizeItemPayload(
            book_item_id="item-inv-1",
            date="2026-01-15",
            amount_units=1500000,
            currency="MAD",
            direction="BOOK_BANK_DEBIT",
            description="Facture Client #001",
            counterparty_id="cust-123",
            counterparty_name="Acme Maroc",
            reference="FAC-2026-001",
            source_artifact_kind="INVOICE",
            bookkeeping_role="OPEN_RECEIVABLE",
        )

        d = payload.to_dict()
        assert d["source_artifact_kind"] == "INVOICE"
        assert d["bookkeeping_role"] == "OPEN_RECEIVABLE"

        # JSON wire serialization roundtrip
        json_str = json.dumps(d)
        parsed = json.loads(json_str)
        restored = BookCategorizeItemPayload.from_dict(parsed)

        assert restored.book_item_id == "item-inv-1"
        assert restored.source_artifact_kind == "INVOICE"
        assert restored.bookkeeping_role == "OPEN_RECEIVABLE"
        assert restored == payload

    def test_request_envelope_roundtrip(self):
        item1 = BookCategorizeItemPayload(
            book_item_id="item-bill-1",
            date="2026-01-16",
            amount_units=800000,
            currency="MAD",
            direction="BOOK_BANK_CREDIT",
            description="Facture Fournisseur",
            source_artifact_kind="BILL",
            bookkeeping_role="OPEN_PAYABLE",
        )
        item2 = BookCategorizeItemPayload(
            book_item_id="item-tx-1",
            date="2026-01-17",
            amount_units=200000,
            currency="MAD",
            direction="BOOK_BANK_CREDIT",
            description="Virement Loyer",
            source_artifact_kind="TRANSACTION",
            bookkeeping_role="POSTED_CASH_MOVEMENT",
        )

        env = BookCategorizeRequestEnvelope(
            schema_version=BOOK_CATEGORIZE_SCHEMA_VERSION,
            request_id="req-123",
            idempotency_key="key-123",
            company_id="company-1",
            session_id="session-1",
            state_revision=1,
            persistence_revision=1,
            dag_id=BOOK_CATEGORIZATION_DAG_ID,
            requested_at="2026-01-17T12:00:00Z",
            book_items=(item1, item2),
        )

        raw = env.to_dict()
        restored = BookCategorizeRequestEnvelope.from_dict(json.loads(json.dumps(raw)))

        assert len(restored.book_items) == 2
        assert restored.book_items[0].source_artifact_kind == "BILL"
        assert restored.book_items[0].bookkeeping_role == "OPEN_PAYABLE"
        assert restored.book_items[1].source_artifact_kind == "TRANSACTION"
        assert restored.book_items[1].bookkeeping_role == "POSTED_CASH_MOVEMENT"

    def test_canonical_semantic_dict_includes_source_semantics(self):
        payload = BookCategorizeItemPayload(
            book_item_id="item-1",
            date="2026-01-15",
            amount_units=10000,
            currency="USD",
            direction="BOOK_BANK_DEBIT",
            source_artifact_kind="INVOICE",
            bookkeeping_role="OPEN_RECEIVABLE",
        )
        cd = compute_canonical_semantic_dict(
            schema_version=BOOK_CATEGORIZE_SCHEMA_VERSION,
            company_id="c-1",
            session_id="s-1",
            state_revision=1,
            persistence_revision=1,
            dag_id=BOOK_CATEGORIZATION_DAG_ID,
            book_items=(payload,),
        )
        assert "items" in cd
        assert len(cd["items"]) == 1
        item_dict = cd["items"][0]
        assert "source_artifact_kind" in item_dict
        assert item_dict["source_artifact_kind"] == "INVOICE"
        assert "bookkeeping_role" in item_dict
        assert item_dict["bookkeeping_role"] == "OPEN_RECEIVABLE"

    def test_digest_participates_source_artifact_kind_and_bookkeeping_role(self):
        base_item = BookCategorizeItemPayload(
            book_item_id="item-1",
            date="2026-01-15",
            amount_units=10000,
            currency="USD",
            direction="BOOK_BANK_DEBIT",
            source_artifact_kind="INVOICE",
            bookkeeping_role="OPEN_RECEIVABLE",
        )

        base_digest = compute_canonical_payload_digest(
            schema_version=BOOK_CATEGORIZE_SCHEMA_VERSION,
            company_id="c-1",
            session_id="s-1",
            state_revision=1,
            persistence_revision=1,
            dag_id=BOOK_CATEGORIZATION_DAG_ID,
            book_items=(base_item,),
        )

        # 1. Change source_artifact_kind: INVOICE -> BILL
        bill_item = BookCategorizeItemPayload(
            book_item_id="item-1",
            date="2026-01-15",
            amount_units=10000,
            currency="USD",
            direction="BOOK_BANK_DEBIT",
            source_artifact_kind="BILL",
            bookkeeping_role="OPEN_RECEIVABLE",
        )
        bill_digest = compute_canonical_payload_digest(
            schema_version=BOOK_CATEGORIZE_SCHEMA_VERSION,
            company_id="c-1",
            session_id="s-1",
            state_revision=1,
            persistence_revision=1,
            dag_id=BOOK_CATEGORIZATION_DAG_ID,
            book_items=(bill_item,),
        )
        assert bill_digest != base_digest

        # 2. Change bookkeeping_role: OPEN_RECEIVABLE -> OPEN_PAYABLE
        payable_item = BookCategorizeItemPayload(
            book_item_id="item-1",
            date="2026-01-15",
            amount_units=10000,
            currency="USD",
            direction="BOOK_BANK_DEBIT",
            source_artifact_kind="INVOICE",
            bookkeeping_role="OPEN_PAYABLE",
        )
        payable_digest = compute_canonical_payload_digest(
            schema_version=BOOK_CATEGORIZE_SCHEMA_VERSION,
            company_id="c-1",
            session_id="s-1",
            state_revision=1,
            persistence_revision=1,
            dag_id=BOOK_CATEGORIZATION_DAG_ID,
            book_items=(payable_item,),
        )
        assert payable_digest != base_digest

        # 3. Change bookkeeping_role: OPEN_RECEIVABLE -> POSTED_CASH_MOVEMENT
        cash_item = BookCategorizeItemPayload(
            book_item_id="item-1",
            date="2026-01-15",
            amount_units=10000,
            currency="USD",
            direction="BOOK_BANK_DEBIT",
            source_artifact_kind="INVOICE",
            bookkeeping_role="POSTED_CASH_MOVEMENT",
        )
        cash_digest = compute_canonical_payload_digest(
            schema_version=BOOK_CATEGORIZE_SCHEMA_VERSION,
            company_id="c-1",
            session_id="s-1",
            state_revision=1,
            persistence_revision=1,
            dag_id=BOOK_CATEGORIZATION_DAG_ID,
            book_items=(cash_item,),
        )
        assert cash_digest != base_digest

        # 4. None source semantics
        none_item = BookCategorizeItemPayload(
            book_item_id="item-1",
            date="2026-01-15",
            amount_units=10000,
            currency="USD",
            direction="BOOK_BANK_DEBIT",
            source_artifact_kind=None,
            bookkeeping_role=None,
        )
        none_digest = compute_canonical_payload_digest(
            schema_version=BOOK_CATEGORIZE_SCHEMA_VERSION,
            company_id="c-1",
            session_id="s-1",
            state_revision=1,
            persistence_revision=1,
            dag_id=BOOK_CATEGORIZATION_DAG_ID,
            book_items=(none_item,),
        )
        assert none_digest != base_digest

    def test_no_truth_manifest_or_simulator_fields_in_payload(self):
        """Strict bounded contract: production payloads must never expose eval/simulation fields."""
        item = BookCategorizeItemPayload(
            book_item_id="item-1",
            date="2026-01-15",
            amount_units=10000,
            currency="MAD",
            direction="BOOK_BANK_DEBIT",
            description="Test item",
            source_artifact_kind="INVOICE",
            bookkeeping_role="OPEN_RECEIVABLE",
        )
        payload_dict = item.to_dict()
        cd = compute_canonical_semantic_dict(
            schema_version=BOOK_CATEGORIZE_SCHEMA_VERSION,
            company_id="c-1",
            session_id="s-1",
            state_revision=1,
            persistence_revision=1,
            dag_id=BOOK_CATEGORIZATION_DAG_ID,
            book_items=(item,),
        )
        canonical_item_dict = cd["items"][0]

        allowed_payload_keys = {
            "active_bank_account_id",
            "amount",
            "amount_units",
            "book_item_id",
            "bookkeeping_role",
            "counterparty_id",
            "counterparty_name",
            "currency",
            "date",
            "description",
            "direction",
            "evidence_refs",
            "existing_account_code",
            "existing_classification_id",
            "reference",
            "safe_evidence_summaries",
            "source_artifact_kind",
        }

        allowed_canonical_item_keys = {
            "active_bank_account_id",
            "amount_units",
            "book_item_id",
            "bookkeeping_role",
            "counterparty_id",
            "counterparty_name",
            "currency",
            "date",
            "description",
            "direction",
            "evidence_refs",
            "existing_account_code",
            "existing_classification_id",
            "reference",
            "safe_evidence_summaries",
            "source_artifact_kind",
        }

        assert set(payload_dict.keys()).issubset(allowed_payload_keys)
        assert set(canonical_item_dict.keys()) == allowed_canonical_item_keys

        # Explicitly assert forbidden eval/simulation substrings
        forbidden = ("truth", "manifest", "simulator", "ground_truth", "target", "label", "eval")
        for k in payload_dict:
            for f in forbidden:
                assert f not in k.lower(), f"Forbidden substring '{f}' found in payload key '{k}'"

    def test_domain_backward_compatibility_inference(self):
        """When not explicitly passed, BookItem semantics are safely inferred from ID/source."""
        # 1. invoice prefix
        inv_item = BookItem(
            id="invoice:abc-123",
            source_type=SourceType.STAGING_BOOK_ITEM,
            origin_period="2026-01",
            date=date(2026, 1, 15),
            amount_units="10000",
            direction=Direction.BOOK_BANK_DEBIT,
            currency="MAD",
            description="Invoice",
        )
        assert inv_item.source_artifact_kind == SourceArtifactKind.INVOICE
        assert inv_item.bookkeeping_role == BookkeepingRole.OPEN_RECEIVABLE

        # 2. bill prefix
        bill_item = BookItem(
            id="bill:def-456",
            source_type=SourceType.STAGING_BOOK_ITEM,
            origin_period="2026-01",
            date=date(2026, 1, 16),
            amount_units="20000",
            direction=Direction.BOOK_BANK_CREDIT,
            currency="MAD",
            description="Bill",
        )
        assert bill_item.source_artifact_kind == SourceArtifactKind.BILL
        assert bill_item.bookkeeping_role == BookkeepingRole.OPEN_PAYABLE

        # 3. tx prefix
        tx_item = BookItem(
            id="tx:ghi-789",
            source_type=SourceType.POSTED_BOOK_ITEM,
            origin_period="2026-01",
            date=date(2026, 1, 17),
            amount_units="30000",
            direction=Direction.BOOK_BANK_DEBIT,
            currency="MAD",
            description="Tx",
        )
        assert tx_item.source_artifact_kind == SourceArtifactKind.TRANSACTION
        assert tx_item.bookkeeping_role == BookkeepingRole.POSTED_CASH_MOVEMENT

        # 4. Explicit overrides are preserved
        explicit_item = BookItem(
            id="custom:999",
            source_type=SourceType.STAGING_BOOK_ITEM,
            origin_period="2026-01",
            date=date(2026, 1, 18),
            amount_units="40000",
            direction=Direction.BOOK_BANK_DEBIT,
            currency="MAD",
            description="Custom",
            source_artifact_kind=SourceArtifactKind.INVOICE,
            bookkeeping_role=BookkeepingRole.OPEN_RECEIVABLE,
        )
        assert explicit_item.source_artifact_kind == SourceArtifactKind.INVOICE
        assert explicit_item.bookkeeping_role == BookkeepingRole.OPEN_RECEIVABLE


# ---------------------------------------------------------------------------
# End-to-End Hydration & DagView Construction Tests (Django DB)
# ---------------------------------------------------------------------------

@pytest.mark.django_db
class TestBookItemSourceHydration:
    """Tests that Django models hydrate with correct source semantics into BookItem and DagView."""

    def test_source_semantics_hydrate_from_django_and_project_to_dag_view(self):
        from django.contrib.auth import get_user_model
        from ledger.io.roles import (
            ASSET_CA_CASH,
            ASSET_CA_PREPAID,
            ASSET_CA_RECEIVABLES,
            CREDIT,
            DEBIT,
            INCOME_OPERATIONAL,
            LIABILITY_CL_ACC_PAYABLE,
            LIABILITY_CL_DEFERRED_REVENUE,
        )
        from ledger.models import (
            BankAccountModel,
            BillModel,
            CustomerModel,
            EntityModel,
            InvoiceModel,
            JournalEntryModel,
            LedgerModel,
            TransactionModel,
            VendorModel,
        )

        user = get_user_model().objects.create_user(
            username="test_transporter",
            email="tt@example.com",
            password="test_password_123",
        )
        entity = EntityModel.add_root(
            name="Source Transport Enterprise",
            admin=user,
            currency="MAD",
            fy_start_month=1,
            accrual_method=False,
        )
        ledger = LedgerModel.objects.create(
            name="Main Operational Ledger",
            entity=entity,
            posted=True,
        )
        coa = entity.create_chart_of_accounts(
            coa_name="Transport COA",
            assign_as_default=True,
            commit=True,
        )

        asset_root = coa.accountmodel_set.get(code="01000000")
        cash_account = asset_root.add_child(
            coa_model=coa,
            code="1010",
            name="Banque Centrale Populaire",
            role=ASSET_CA_CASH,
            balance_type=DEBIT,
        )
        ar_account = asset_root.add_child(
            coa_model=coa,
            code="1020",
            name="Clients",
            role=ASSET_CA_RECEIVABLES,
            balance_type=DEBIT,
        )
        prepaid_account = asset_root.add_child(
            coa_model=coa,
            code="1030",
            name="Charges constatees d'avance",
            role=ASSET_CA_PREPAID,
            balance_type=DEBIT,
        )

        liability_root = coa.accountmodel_set.get(code="02000000")
        def_rev_account = liability_root.add_child(
            coa_model=coa,
            code="2010",
            name="Produits constates d'avance",
            role=LIABILITY_CL_DEFERRED_REVENUE,
            balance_type=CREDIT,
        )
        ap_account = liability_root.add_child(
            coa_model=coa,
            code="2020",
            name="Fournisseurs",
            role=LIABILITY_CL_ACC_PAYABLE,
            balance_type=CREDIT,
        )

        income_root = coa.accountmodel_set.get(code="04000000")
        rev_account = income_root.add_child(
            coa_model=coa,
            code="4010",
            name="Ventes de marchandises",
            role=INCOME_OPERATIONAL,
            balance_type=CREDIT,
        )

        # Bank Account Model
        BankAccountModel.objects.create(
            name="Main BCP Bank Account",
            entity_model=entity,
            account_model=cash_account,
            connection_type="manual",
            account_number="BCP-001",
            active=True,
        )

        # Counterparties
        customer = CustomerModel.objects.create(
            customer_name="Client Test SARL",
            customer_number="CLI-001",
            entity_model=entity,
        )
        vendor = VendorModel.objects.create(
            vendor_name="Fournisseur Test SARL",
            vendor_number="FRN-001",
            entity_model=entity,
        )

        # 2. Open Invoice: 5,000 MAD due, 0 paid -> OPEN_RECEIVABLE
        inv = InvoiceModel(
            cash_account=cash_account,
            prepaid_account=ar_account,
            unearned_account=def_rev_account,
            accrue=False,
        )
        _, inv = inv.configure(entity_slug=entity, user_model=user)
        inv.amount_due = Decimal("5000.00")
        inv.amount_paid = Decimal("0.00")
        inv.date_draft = date(2026, 1, 10)
        inv.invoice_status = InvoiceModel.INVOICE_STATUS_APPROVED
        inv.customer = customer
        inv.clean()
        inv.save()

        # 3. Open Bill: 2,500 MAD due, 0 paid -> OPEN_PAYABLE
        bill = BillModel(
            cash_account=cash_account,
            prepaid_account=prepaid_account,
            unearned_account=ap_account,
            accrue=False,
        )
        _, bill = bill.configure(entity_slug=entity, user_model=user)
        bill.amount_due = Decimal("2500.00")
        bill.amount_paid = Decimal("0.00")
        bill.date_draft = date(2026, 1, 12)
        bill.bill_status = BillModel.BILL_STATUS_APPROVED
        bill.vendor = vendor
        bill.clean()
        bill.save()

        # 4. Posted cash movement transaction -> POSTED_CASH_MOVEMENT
        je = JournalEntryModel.objects.create(
            ledger=ledger,
            description="Paiement frais bancaires",
            posted=False,
            timestamp=datetime(2026, 1, 14, 10, 0, 0, tzinfo=timezone.utc),
        )
        tx = TransactionModel.objects.create(
            journal_entry=je,
            account=cash_account,
            amount=Decimal("150.00"),
            tx_type=CREDIT,
            description="Commission tenue de compte",
            reconciled=False,
        )
        TransactionModel.objects.create(
            journal_entry=je,
            account=rev_account,
            amount=Decimal("150.00"),
            tx_type=DEBIT,
            description="Contrepartie frais",
            reconciled=False,
        )
        je.posted = True
        je.save(update_fields=["posted"], verify=False)

        # 5. Read snapshot through reader
        company_id = str(entity.uuid)
        snapshot = read_django_snapshot(company_id=company_id)

        # Inspect BookItems in snapshot
        book_items_by_id = {b.id: b for b in snapshot.book_items}

        inv_item = book_items_by_id.get(f"invoice:{inv.uuid}")
        assert inv_item is not None, "Invoice BookItem must be present in snapshot"
        assert inv_item.source_artifact_kind == SourceArtifactKind.INVOICE
        assert inv_item.bookkeeping_role == BookkeepingRole.OPEN_RECEIVABLE
        assert inv_item.direction == Direction.BOOK_BANK_DEBIT
        # Assert invoice is an obligation, NOT a settlement
        assert inv_item.bookkeeping_role != BookkeepingRole.POSTED_CASH_MOVEMENT

        bill_item = book_items_by_id.get(f"bill:{bill.uuid}")
        assert bill_item is not None, "Bill BookItem must be present in snapshot"
        assert bill_item.source_artifact_kind == SourceArtifactKind.BILL
        assert bill_item.bookkeeping_role == BookkeepingRole.OPEN_PAYABLE
        assert bill_item.direction == Direction.BOOK_BANK_CREDIT
        # Assert bill is an obligation, NOT a settlement
        assert bill_item.bookkeeping_role != BookkeepingRole.POSTED_CASH_MOVEMENT

        tx_item = book_items_by_id.get(f"tx:{tx.uuid}")
        assert tx_item is not None, "Transaction BookItem must be present in snapshot"
        assert tx_item.source_artifact_kind == SourceArtifactKind.TRANSACTION
        assert tx_item.bookkeeping_role == BookkeepingRole.POSTED_CASH_MOVEMENT

        # 6. Hydrate into BookkeepingState
        from bookkeeping_state.persistence.repository import BookkeepingRepository
        repo = BookkeepingRepository()
        hydrator = BookkeepingHydrator(repository=repo)
        state = hydrator.hydrate(company_id=company_id)
        queries = BookkeepingQueries(state)

        # 7. Project through DagView
        dag_view = build_dag_view(queries, include_already_classified=True)
        view_items_by_id = {it.book_item_id: it for it in dag_view.items}

        inv_view_item = view_items_by_id.get(f"invoice:{inv.uuid}")
        assert inv_view_item is not None
        assert inv_view_item.source_artifact_kind == "INVOICE"
        assert inv_view_item.bookkeeping_role == "OPEN_RECEIVABLE"

        bill_view_item = view_items_by_id.get(f"bill:{bill.uuid}")
        assert bill_view_item is not None
        assert bill_view_item.source_artifact_kind == "BILL"
        assert bill_view_item.bookkeeping_role == "OPEN_PAYABLE"

        tx_view_item = view_items_by_id.get(f"tx:{tx.uuid}")
        assert tx_view_item is not None
        assert tx_view_item.source_artifact_kind == "TRANSACTION"
        assert tx_view_item.bookkeeping_role == "POSTED_CASH_MOVEMENT"
