from __future__ import annotations

import uuid
from decimal import Decimal
from typing import TYPE_CHECKING

from bookkeeping_state.domain.bank import BankAccount, BankItem
from bookkeeping_state.domain.books import BookItem
from bookkeeping_state.domain.context import AccountingPolicy, BookkeepingContext
from bookkeeping_state.domain.counterparties import Counterparty, CounterpartyType
from bookkeeping_state.domain.enums import (
    BookkeepingRole,
    Direction,
    SourceArtifactKind,
    SourceType,
)
from bookkeeping_state.domain.money import (
    major_units_to_amount_units,
    solver_units_to_decimal,
)
from bookkeeping_state.persistence.bank_normalization import (
    normalize_staged_movement,
)
from bookkeeping_state.persistence.repository import (
    BookkeepingSnapshot,
    PersistenceError,
)
from pydantic import ValidationError
from django.core.exceptions import MultipleObjectsReturned, ObjectDoesNotExist
from django.db.models import Q

if TYPE_CHECKING:
    from ledger.models.entity import EntityModel


def _resolve_entity(*, company_id: str) -> EntityModel:
    """
    Resolve company_id deterministically to an EntityModel instance.

    Rules:
    - No LIMIT 1
    - No arbitrary fallback
    - Invalid or missing entity fails explicitly with PersistenceError
    """
    from ledger.models.entity import EntityModel

    entity: EntityModel | None = None

    # 1. Try UUID lookup if company_id is a valid UUID string
    try:
        entity_uuid = uuid.UUID(company_id)
        try:
            entity = EntityModel.objects.get(uuid=entity_uuid)
        except ObjectDoesNotExist:
            entity = None
    except (ValueError, TypeError, AttributeError):
        entity = None

    # 2. Try exact match on slug
    if entity is None:
        try:
            entity = EntityModel.objects.get(slug__exact=company_id)
        except ObjectDoesNotExist:
            raise PersistenceError(
                f"Entity not found for company_id '{company_id}'."
            ) from None
        except MultipleObjectsReturned:
            raise PersistenceError(
                f"Ambiguous company_id '{company_id}': multiple entities found."
            ) from None

    if entity is None:
        raise PersistenceError(
            f"Entity not found for company_id '{company_id}'."
        )
    return entity


def read_django_snapshot(*, company_id: str) -> BookkeepingSnapshot:
    """
    Load a pure, detached BookkeepingSnapshot from the Django ledger models.

    This function reads:
    - Entity and context (canonical company identity = str(entity.uuid))
    - Bank accounts and their bank-side items (strictly scoped to single accounts)
    - Open source documents (invoices, bills) and posted cash GL transactions
    - Counterparties (customers, vendors)

    All data is converted into immutable runtime domain objects with zero ORM attachments.
    """
    from ledger.io.io_core import get_localdate
    from ledger.io.roles import ASSET_CA_CASH, DEBIT
    from ledger.models.bank_account import BankAccountModel
    from ledger.models.bill import BillModel
    from ledger.models.customer import CustomerModel
    from ledger.models.data_import import StagedTransactionModel
    from ledger.models.invoice import InvoiceModel
    from ledger.models.transactions import TransactionModel
    from ledger.models.vendor import VendorModel

    entity = _resolve_entity(company_id=company_id)

    # 1. Counterparties
    counterparties_list: list[Counterparty] = []
    counterparty_ids: set[str] = set()

    customer_models = CustomerModel.objects.filter(entity_model=entity).order_by("uuid")
    for c in customer_models:
        cp_id = str(c.uuid)
        if cp_id not in counterparty_ids:
            counterparty_ids.add(cp_id)
            tax_id = None
            ext_ref = c.customer_number or None
            counterparties_list.append(
                Counterparty(
                    id=cp_id,
                    name=c.customer_name,
                    counterparty_type=CounterpartyType.CUSTOMER,
                    tax_id=tax_id,
                    external_reference=ext_ref,
                    metadata={"customer_number": c.customer_number or ""},
                )
            )

    vendor_models = VendorModel.objects.filter(entity_model=entity).order_by("uuid")
    for v in vendor_models:
        cp_id = str(v.uuid)
        if cp_id not in counterparty_ids:
            counterparty_ids.add(cp_id)
            tax_id = getattr(v, "tax_id_number", None) or None
            ext_ref = v.vendor_number or None
            counterparties_list.append(
                Counterparty(
                    id=cp_id,
                    name=v.vendor_name,
                    counterparty_type=CounterpartyType.SUPPLIER,
                    tax_id=tax_id,
                    external_reference=ext_ref,
                    metadata={"vendor_number": v.vendor_number or ""},
                )
            )

    # 2. Bank Accounts & Bank Items
    bank_accounts_list: list[BankAccount] = []
    bank_items_list: list[BankItem] = []
    seen_bank_item_ids: set[str] = set()

    bank_account_models = (
        BankAccountModel.objects.filter(entity_model=entity)
        .select_related("account_model", "plaid_item")
        .order_by("uuid")
    )

    for ba in bank_account_models:
        ba_id = str(ba.uuid)
        curr = str(getattr(ba.account_model, "currency", None) or getattr(entity, "currency", None) or "USD")
        currency = curr.upper()

        meta = ba.metadata if isinstance(ba.metadata, dict) else {}
        institution_name = None
        if "bank_name" in meta:
            institution_name = str(meta["bank_name"])
        elif "institution_name" in meta:
            institution_name = str(meta["institution_name"])
        elif ba.plaid_item and isinstance(ba.plaid_item.metadata, dict):
            institution_name = ba.plaid_item.metadata.get("institution", {}).get("name")
        elif hasattr(ba, "bank_name"):
            institution_name = getattr(ba, "bank_name", None)
        institution_name = str(institution_name) if institution_name else None

        ext_ref = getattr(ba, "account_number", None) or meta.get("account_number") or meta.get("account_id") or None
        external_reference = str(ext_ref) if ext_ref else None
        ba_name = ba.name or external_reference or f"Bank Account {ba_id}"

        bank_account = BankAccount(
            id=ba_id,
            name=ba_name,
            currency=currency,
            institution_name=institution_name,
            external_reference=external_reference,
        )
        bank_accounts_list.append(bank_account)

        # Bank items: Authoritative staged bank transactions only
        staged_txs = StagedTransactionModel.objects.filter(
            import_job__bank_account_model=ba,
            parent__isnull=True,
        ).order_by("uuid")

        for stx in staged_txs:
            bank_item_id = f"staged:{stx.uuid}"
            if bank_item_id in seen_bank_item_ids:
                raise PersistenceError(
                    f"Bank item {bank_item_id} is duplicated across multiple bank accounts."
                )
            seen_bank_item_ids.add(bank_item_id)

            if Decimal(str(stx.amount)) == Decimal(0):
                continue
            abs_amount, direction, stx_curr = normalize_staged_movement(stx, entity=entity)
            currency = stx_curr

            abs_amount_quantized = abs_amount.quantize(Decimal("0.0001"))
            amount_units = major_units_to_amount_units(abs_amount_quantized)
            tx_desc = stx.name or stx.memo or "Staged Transaction"
            tx_ref = stx.fit_id or None
            if stx.memo and stx.memo.startswith("Ref: "):
                extracted_ref = stx.memo[5:].strip()
                if extracted_ref:
                    tx_ref = extracted_ref


            bank_items_list.append(
                BankItem(
                    id=bank_item_id,
                    bank_account_id=ba_id,
                    source_type=SourceType.BANK_STATEMENT_LINE,
                    date=stx.date_posted,
                    amount_units=amount_units,
                    direction=direction,
                    currency=currency,
                    description=tx_desc,
                    reference=tx_ref,
                    provenance_refs=(str(stx.uuid),),
                )
            )

    # 3. Book Items
    book_items_list: list[BookItem] = []
    seen_book_item_ids: set[str] = set()

    # 3a. Open Invoices (receivables waiting for bank receipt / debit)
    open_invoices = (
        InvoiceModel.objects.filter(
            ledger__entity=entity,
            invoice_status=InvoiceModel.INVOICE_STATUS_APPROVED,
        )
        .select_related("customer")
        .order_by("uuid")
    )
    for inv in open_invoices:
        due = Decimal(str(inv.amount_due or 0))
        paid = Decimal(str(inv.amount_paid or 0))
        remaining = due - paid
        if remaining <= 0:
            continue

        inv_date = inv.date_approved or inv.date_draft or inv.created.date()
        cp_id = (
            str(inv.customer.uuid)
            if inv.customer and str(inv.customer.uuid) in counterparty_ids
            else None
        )
        curr = str(getattr(entity, "currency", None) or "USD").upper()
        item_id = f"invoice:{inv.uuid}"
        seen_book_item_ids.add(item_id)

        book_items_list.append(
            BookItem(
                id=item_id,
                source_type=SourceType.STAGING_BOOK_ITEM,
                origin_period=inv_date.strftime("%Y-%m"),
                date=inv_date,
                amount_units=major_units_to_amount_units(remaining.quantize(Decimal("0.0001"))),
                direction=Direction.BOOK_BANK_DEBIT,
                currency=curr,
                description=inv.markdown_notes or f"Invoice {inv.invoice_number or inv.uuid}",
                counterparty_id=cp_id,
                reference=inv.invoice_number or None,
                provenance_refs=(str(inv.uuid),),
                source_artifact_kind=SourceArtifactKind.INVOICE,
                bookkeeping_role=BookkeepingRole.OPEN_RECEIVABLE,
            )
        )

    # 3b. Open Bills (payables waiting for bank payment / credit)
    open_bills = (
        BillModel.objects.filter(
            ledger__entity=entity,
            bill_status=BillModel.BILL_STATUS_APPROVED,
        )
        .select_related("vendor")
        .order_by("uuid")
    )
    for bill in open_bills:
        due = Decimal(str(bill.amount_due or 0))
        paid = Decimal(str(bill.amount_paid or 0))
        remaining = due - paid
        if remaining <= 0:
            continue

        bill_date = bill.date_approved or bill.date_draft or bill.created.date()
        cp_id = (
            str(bill.vendor.uuid)
            if bill.vendor and str(bill.vendor.uuid) in counterparty_ids
            else None
        )
        curr = str(getattr(entity, "currency", None) or "USD").upper()
        item_id = f"bill:{bill.uuid}"
        seen_book_item_ids.add(item_id)

        book_items_list.append(
            BookItem(
                id=item_id,
                source_type=SourceType.STAGING_BOOK_ITEM,
                origin_period=bill_date.strftime("%Y-%m"),
                date=bill_date,
                amount_units=major_units_to_amount_units(remaining.quantize(Decimal("0.0001"))),
                direction=Direction.BOOK_BANK_CREDIT,
                currency=curr,
                description=bill.markdown_notes or f"Bill {bill.bill_number or bill.uuid}",
                counterparty_id=cp_id,
                reference=bill.bill_number or None,
                provenance_refs=(str(bill.uuid),),
                source_artifact_kind=SourceArtifactKind.BILL,
                bookkeeping_role=BookkeepingRole.OPEN_PAYABLE,
            )
        )

    # 3c. Posted journal transactions on Cash / Bank accounts
    # This includes standalone cash transactions AS WELL AS cash payment legs of invoices and bills.
    cash_accounts = [
        ba.account_model for ba in bank_account_models if ba.account_model is not None
    ]
    cash_filter = Q(account__role=ASSET_CA_CASH)
    if cash_accounts:
        cash_filter |= Q(account__in=cash_accounts)

    posted_cash_txs = (
        TransactionModel.objects.filter(
            journal_entry__ledger__entity=entity,
            journal_entry__posted=True,
            reconciled=False,
        )
        .filter(cash_filter)
        .select_related(
            "journal_entry",
            "journal_entry__ledger",
            "journal_entry__ledger__invoicemodel__customer",
            "journal_entry__ledger__billmodel__vendor",
            "account",
        )
        .order_by("uuid")
    )

    for tx in posted_cash_txs:
        tx_amount = Decimal(str(tx.amount))
        if tx_amount <= 0:
            continue

        tx_date = tx.journal_entry.timestamp.date()
        direction = (
            Direction.BOOK_BANK_DEBIT
            if tx.tx_type == DEBIT
            else Direction.BOOK_BANK_CREDIT
        )
        curr = str(getattr(tx.account, "currency", None) or getattr(entity, "currency", None) or "USD").upper()

        cp_id = None
        ledger = tx.journal_entry.ledger
        inv = getattr(ledger, "invoicemodel", None)
        bill = getattr(ledger, "billmodel", None)
        if inv and inv.customer:
            cust_id = str(inv.customer.uuid)
            if cust_id in counterparty_ids:
                cp_id = cust_id
        elif bill and bill.vendor:
            vend_id = str(bill.vendor.uuid)
            if vend_id in counterparty_ids:
                cp_id = vend_id

        tx_desc = (
            tx.description
            or tx.journal_entry.description
            or (f"Payment for Invoice {inv.invoice_number}" if inv else None)
            or (f"Payment for Bill {bill.bill_number}" if bill else None)
            or f"Transaction {tx.uuid}"
        )
        tx_ref = (
            getattr(tx.journal_entry, "je_number", None)
            or (inv.invoice_number if inv else None)
            or (bill.bill_number if bill else None)
            or None
        )

        provenance = [str(tx.uuid)]
        if inv:
            provenance.append(str(inv.uuid))
        elif bill:
            provenance.append(str(bill.uuid))

        item_id = f"tx:{tx.uuid}"
        if item_id in seen_book_item_ids:
            raise PersistenceError(f"Duplicate book item ID {item_id}")
        seen_book_item_ids.add(item_id)

        book_items_list.append(
            BookItem(
                id=item_id,
                source_type=SourceType.POSTED_BOOK_ITEM,
                origin_period=tx_date.strftime("%Y-%m"),
                date=tx_date,
                amount_units=major_units_to_amount_units(tx_amount.quantize(Decimal("0.0001"))),
                direction=direction,
                currency=curr,
                description=tx_desc,
                counterparty_id=cp_id,
                reference=tx_ref,
                provenance_refs=tuple(provenance),
                source_artifact_kind=SourceArtifactKind.TRANSACTION,
                bookkeeping_role=BookkeepingRole.POSTED_CASH_MOVEMENT,
            )
        )

    # 4. Context and Policy
    entity_currency = getattr(entity, "currency", None)
    if not entity_currency:
        raise PersistenceError(f"Entity {entity.uuid} has no base currency configured.")
    base_currency = str(entity_currency).upper()

    coa_id = None
    default_coa = getattr(entity, "default_coa", None)
    if default_coa:
        coa_id = str(getattr(default_coa, "uuid", default_coa))
    else:
        from ledger.models.chart_of_accounts import ChartOfAccountModel
        first_coa = ChartOfAccountModel.objects.filter(entity=entity).first()
        if first_coa:
            coa_id = str(first_coa.uuid)

    if not coa_id:
        raise PersistenceError(f"Entity {entity.uuid} has no Chart of Accounts configured.")

    policy = AccountingPolicy(
        chart_of_accounts_id=coa_id,
        reconciliation_date_window_days=45,
        require_exact_currency_match=True,
        allow_partial_book_reconciliation=True,
        allow_partial_bank_reconciliation=False,
        auto_reconcile_unique_inferred_allocation=True,
    )

    all_dates = [bi.date for bi in bank_items_list] + [b.date for b in book_items_list]
    today = get_localdate()
    fy = entity.get_fy_for_date(today)
    fy_start, fy_end = entity.get_fiscal_year_dates(int(fy))

    if all_dates:
        period_start = min(fy_start, min(all_dates))
        period_end = max(fy_end, max(all_dates))
    else:
        period_start = fy_start
        period_end = fy_end

    period_end = max(period_end, period_start)

    context = BookkeepingContext(
        company_id=str(entity.uuid),
        period_start=period_start,
        period_end=period_end,
        base_currency=base_currency,
        policy=policy,
    )

    from bookkeeping_state.domain.classifications import (
        ClassificationDecision,
        ClassificationInvalidation,
        ClassificationSource,
    )
    from bookkeeping_state.domain.enums import (
        AllocationSupport,
        SemanticAdmissibility,
    )
    from bookkeeping_state.domain.evidence import (
        BookItemEvidenceAssertion,
        BookItemEvidenceInvalidation,
        BookItemEvidenceType,
        EvidenceSource,
    )
    from bookkeeping_state.domain.payment_application import (
        ExecutedPaymentAllocation,
        ExecutedPaymentApplication,
    )
    from bookkeeping_state.domain.reconciliations import (
        BankAllocation,
        BookAllocation,
        Reconciliation,
        ReconciliationInvalidation,
    )
    from bookkeeping_state.domain.residual_bank_classifications import (
        ResidualBankClassificationDecision,
        ResidualBankClassificationInvalidation,
        ResidualBankClassificationStatus,
        ResidualBankPosting,
    )
    from bookkeeping_state.domain.routing import (
        RoutingDecision,
        RoutingDecisionInvalidation,
        RoutingDecisionSource,
    )

    from ledger.models.bookkeeping import (
        BookkeepingClassificationDecision,
        BookkeepingClassificationInvalidation,
        BookkeepingEvidenceAssertion,
        BookkeepingEvidenceInvalidation,
        BookkeepingPaymentApplication,
        BookkeepingReconciliation,
        BookkeepingReconciliationInvalidation,
        BookkeepingResidualBankClassificationDecision,
        BookkeepingResidualBankClassificationInvalidation,
        BookkeepingResidualBankPosting,
        BookkeepingRevision,
        BookkeepingRoutingDecision,
        BookkeepingRoutingInvalidation,
    )

    # 5.1 Persistence Revision
    rev_record = BookkeepingRevision.objects.filter(entity=entity).first()
    persistence_revision = rev_record.revision if rev_record is not None else 0

    historical_book_item_ids: set[str] = set()

    # 5.2 Routing Decisions & Invalidations
    routing_decisions_list: list[RoutingDecision] = []
    for rd in BookkeepingRoutingDecision.objects.filter(entity=entity).select_related(
        "invoice", "bill", "transaction", "bank_account", "supersedes"
    ).order_by("id"):
        if rd.invoice_id:
            try:
                inv = rd.invoice
                if inv is None or inv.ledger.entity_id != entity.pk:
                    raise PersistenceError(
                        f"BookkeepingRoutingDecision {rd.id} references invalid/foreign invoice {rd.invoice_id}."
                    )
            except ObjectDoesNotExist as exc:
                raise PersistenceError(
                    f"BookkeepingRoutingDecision {rd.id} references non-existent invoice {rd.invoice_id}."
                ) from exc
            target_id = f"invoice:{inv.uuid}"
        elif rd.bill_id:
            try:
                bill = rd.bill
                if bill is None or bill.ledger.entity_id != entity.pk:
                    raise PersistenceError(
                        f"BookkeepingRoutingDecision {rd.id} references invalid/foreign bill {rd.bill_id}."
                    )
            except ObjectDoesNotExist as exc:
                raise PersistenceError(
                    f"BookkeepingRoutingDecision {rd.id} references non-existent bill {rd.bill_id}."
                ) from exc
            target_id = f"bill:{bill.uuid}"
        elif rd.transaction_id:
            try:
                tx = rd.transaction
                if tx is None or tx.journal_entry.ledger.entity_id != entity.pk:
                    raise PersistenceError(
                        f"BookkeepingRoutingDecision {rd.id} references invalid/foreign transaction {rd.transaction_id}."
                    )
            except ObjectDoesNotExist as exc:
                raise PersistenceError(
                    f"BookkeepingRoutingDecision {rd.id} references non-existent transaction {rd.transaction_id}."
                ) from exc
            target_id = f"tx:{tx.uuid}"
        else:
            raise PersistenceError(f"BookkeepingRoutingDecision {rd.id} has no target FK set.")

        if rd.bank_account.entity_model_id != entity.pk:
            raise PersistenceError(
                f"BookkeepingRoutingDecision {rd.id} references BankAccount from different entity."
            )

        if target_id not in seen_book_item_ids:
            historical_book_item_ids.add(target_id)

        routing_decisions_list.append(
            RoutingDecision(
                id=rd.id,
                book_item_id=target_id,
                bank_account_id=str(rd.bank_account.uuid),
                source=RoutingDecisionSource(rd.source),
                utility=rd.utility,
                solver_run_id=rd.solver_run_id,
                supersedes_routing_decision_id=rd.supersedes_id if rd.supersedes_id else None,
                session_id=rd.session_id,
                state_revision_at_creation=rd.state_revision_at_creation,
                created_at=rd.created_at,
            )
        )

    routing_invalidations_list: list[RoutingDecisionInvalidation] = []
    for ri in BookkeepingRoutingInvalidation.objects.filter(
        routing_decision__entity=entity
    ).select_related("routing_decision").order_by("id"):
        routing_invalidations_list.append(
            RoutingDecisionInvalidation(
                id=ri.id,
                routing_decision_id=ri.routing_decision_id,
                reason=ri.reason,
                session_id=ri.session_id,
                state_revision_at_invalidation=ri.state_revision_at_invalidation,
                created_at=ri.created_at,
            )
        )

    # 5.3 Classification Decisions & Invalidations
    classifications_list: list[ClassificationDecision] = []
    for cd in BookkeepingClassificationDecision.objects.filter(entity=entity).select_related(
        "invoice", "bill", "transaction", "account", "supersedes"
    ).order_by("id"):
        if cd.invoice_id:
            try:
                inv = cd.invoice
                if inv is None or inv.ledger.entity_id != entity.pk:
                    raise PersistenceError(
                        f"BookkeepingClassificationDecision {cd.id} references invalid/foreign invoice {cd.invoice_id}."
                    )
            except ObjectDoesNotExist as exc:
                raise PersistenceError(
                    f"BookkeepingClassificationDecision {cd.id} references non-existent invoice {cd.invoice_id}."
                ) from exc
            target_id = f"invoice:{inv.uuid}"
        elif cd.bill_id:
            try:
                bill = cd.bill
                if bill is None or bill.ledger.entity_id != entity.pk:
                    raise PersistenceError(
                        f"BookkeepingClassificationDecision {cd.id} references invalid/foreign bill {cd.bill_id}."
                    )
            except ObjectDoesNotExist as exc:
                raise PersistenceError(
                    f"BookkeepingClassificationDecision {cd.id} references non-existent bill {cd.bill_id}."
                ) from exc
            target_id = f"bill:{bill.uuid}"
        elif cd.transaction_id:
            try:
                tx = cd.transaction
                if tx is None or tx.journal_entry.ledger.entity_id != entity.pk:
                    raise PersistenceError(
                        f"BookkeepingClassificationDecision {cd.id} references invalid/foreign transaction {cd.transaction_id}."
                    )
            except ObjectDoesNotExist as exc:
                raise PersistenceError(
                    f"BookkeepingClassificationDecision {cd.id} references non-existent transaction {cd.transaction_id}."
                ) from exc
            target_id = f"tx:{tx.uuid}"
        else:
            raise PersistenceError(f"BookkeepingClassificationDecision {cd.id} has no target FK set.")

        if target_id not in seen_book_item_ids:
            historical_book_item_ids.add(target_id)

        if cd.account is not None and cd.account.code != cd.account_code:
            raise PersistenceError(
                f"Classification {cd.id} account_code '{cd.account_code}' does not match "
                f"linked AccountModel code '{cd.account.code}'."
            )

        classifications_list.append(
            ClassificationDecision(
                id=cd.id,
                book_item_id=target_id,
                account_code=cd.account_code,
                source=ClassificationSource(cd.source),
                confidence=cd.confidence,
                rationale=cd.rationale,
                evidence_refs=tuple(cd.evidence_refs or ()),
                supersedes_classification_id=cd.supersedes_id if cd.supersedes_id else None,
                session_id=cd.session_id,
                dag_run_id=cd.dag_run_id,
                ase_node_id=cd.ase_node_id,
                state_revision_at_decision=cd.state_revision_at_decision,
                created_at=cd.created_at,
            )
        )

    classification_invalidations_list: list[ClassificationInvalidation] = []
    for ci in BookkeepingClassificationInvalidation.objects.filter(
        classification__entity=entity
    ).select_related("classification").order_by("id"):
        classification_invalidations_list.append(
            ClassificationInvalidation(
                id=ci.id,
                classification_id=ci.classification_id,
                reason=ci.reason,
                session_id=ci.session_id,
                state_revision_at_invalidation=ci.state_revision_at_invalidation,
                created_at=ci.created_at,
            )
        )

    # 5.4 Evidence Assertions & Invalidations
    evidence_assertions_list: list[BookItemEvidenceAssertion] = []
    for ea in BookkeepingEvidenceAssertion.objects.filter(entity=entity).select_related(
        "invoice", "bill", "transaction", "supersedes"
    ).order_by("id"):
        if ea.invoice_id:
            try:
                inv = ea.invoice
                if inv is None or inv.ledger.entity_id != entity.pk:
                    raise PersistenceError(
                        f"BookkeepingEvidenceAssertion {ea.id} references invalid/foreign invoice {ea.invoice_id}."
                    )
            except ObjectDoesNotExist as exc:
                raise PersistenceError(
                    f"BookkeepingEvidenceAssertion {ea.id} references non-existent invoice {ea.invoice_id}."
                ) from exc
            target_id = f"invoice:{inv.uuid}"
        elif ea.bill_id:
            try:
                bill = ea.bill
                if bill is None or bill.ledger.entity_id != entity.pk:
                    raise PersistenceError(
                        f"BookkeepingEvidenceAssertion {ea.id} references invalid/foreign bill {ea.bill_id}."
                    )
            except ObjectDoesNotExist as exc:
                raise PersistenceError(
                    f"BookkeepingEvidenceAssertion {ea.id} references non-existent bill {ea.bill_id}."
                ) from exc
            target_id = f"bill:{bill.uuid}"
        elif ea.transaction_id:
            try:
                tx = ea.transaction
                if tx is None or tx.journal_entry.ledger.entity_id != entity.pk:
                    raise PersistenceError(
                        f"BookkeepingEvidenceAssertion {ea.id} references invalid/foreign transaction {ea.transaction_id}."
                    )
            except ObjectDoesNotExist as exc:
                raise PersistenceError(
                    f"BookkeepingEvidenceAssertion {ea.id} references non-existent transaction {ea.transaction_id}."
                ) from exc
            target_id = f"tx:{tx.uuid}"
        else:
            raise PersistenceError(f"BookkeepingEvidenceAssertion {ea.id} has no target FK set.")

        if target_id not in seen_book_item_ids:
            historical_book_item_ids.add(target_id)

        evidence_assertions_list.append(
            BookItemEvidenceAssertion(
                id=ea.id,
                book_item_id=target_id,
                session_id=ea.session_id or "persisted-session",
                evidence_type=BookItemEvidenceType(ea.evidence_type),
                value=ea.value or "",
                document_ids=tuple(ea.document_ids or ()),
                source=EvidenceSource(ea.source),
                confidence=ea.confidence,
                reason=ea.reason,
                metadata=dict(ea.metadata or {}),
                state_revision_at_creation=ea.state_revision_at_creation,
                created_at=ea.created_at,
                supersedes_assertion_id=ea.supersedes_id if ea.supersedes_id else None,
            )
        )

    evidence_invalidations_list: list[BookItemEvidenceInvalidation] = []
    for ei in BookkeepingEvidenceInvalidation.objects.filter(
        assertion__entity=entity
    ).select_related("assertion").order_by("id"):
        evidence_invalidations_list.append(
            BookItemEvidenceInvalidation(
                id=ei.id,
                assertion_id=ei.assertion_id,
                reason=ei.reason,
                session_id=ei.session_id or "persisted-session",
                state_revision_at_invalidation=ei.state_revision_at_invalidation,
                created_at=ei.created_at,
            )
        )

    # 5.5 Executed Stage-1 Payment Applications & Allocations
    executed_payments_list: list[ExecutedPaymentApplication] = []
    pay_apps = BookkeepingPaymentApplication.objects.filter(entity=entity).select_related(
        "plaid_transaction__plaid_item",
        "staged_transaction__import_job__bank_account_model",
    ).prefetch_related(
        "allocations__cash_transaction__journal_entry__ledger",
        "allocations__cash_transaction__account",
        "allocations__invoice__ledger",
        "allocations__bill__ledger",
    ).order_by("id")

    for pa in pay_apps:
        has_plaid = pa.plaid_transaction_id is not None
        has_staged = pa.staged_transaction_id is not None
        if not (has_plaid ^ has_staged):
            raise PersistenceError(
                f"PaymentApplication {pa.id} must have exactly one bank source: plaid={has_plaid}, staged={has_staged}"
            )

        if has_plaid:
            plaid = pa.plaid_transaction
            if plaid is None:
                raise PersistenceError(
                    f"PaymentApplication {pa.id} has plaid_transaction_id but missing PlaidTransaction relation"
                )
            if not BankAccountModel.objects.filter(entity_model=entity, plaid_item=plaid.plaid_item).exists():
                raise PersistenceError(
                    f"PaymentApplication {pa.id} PlaidTransaction {plaid.uuid} belongs to different entity"
                )
            bank_item_id = f"plaid:{plaid.uuid}"
            bank_original_units = int(major_units_to_amount_units(abs(Decimal(str(plaid.amount))).quantize(Decimal("0.0001"))))
        else:
            staged = pa.staged_transaction
            if staged is None:
                raise PersistenceError(
                    f"PaymentApplication {pa.id} has staged_transaction_id but missing StagedTransaction relation"
                )
            bank_account = getattr(getattr(staged, 'import_job', None), 'bank_account_model', None)
            if getattr(bank_account, 'entity_model_id', getattr(getattr(bank_account, 'entity_model', None), 'pk', None)) != entity.pk:
                raise PersistenceError(
                    f"PaymentApplication {pa.id} StagedTransaction {staged.uuid} belongs to different entity"
                )
            bank_item_id = f"staged:{staged.uuid}"
            bank_original_units = int(major_units_to_amount_units(abs(Decimal(str(staged.amount))).quantize(Decimal("0.0001"))))

        if pa.total_amount_units != bank_original_units:
            raise PersistenceError(
                f"PaymentApplication {pa.id} total {pa.total_amount_units} does not match bank source original amount {bank_original_units}"
            )

        allocations = list(pa.allocations.all())
        if not allocations:
            raise PersistenceError(f"PaymentApplication {pa.id} has no allocations.")

        alloc_sum = sum(alloc.amount_units for alloc in allocations)
        if alloc_sum != pa.total_amount_units:
            raise PersistenceError(
                f"PaymentApplication {pa.id} allocations sum ({alloc_sum}) != total ({pa.total_amount_units})"
            )

        executed_allocs: list[ExecutedPaymentAllocation] = []
        for alloc in allocations:
            if alloc.amount_units <= 0:
                raise PersistenceError(
                    f"PaymentApplicationAllocation {alloc.id} has non-positive amount {alloc.amount_units}"
                )

            has_inv = alloc.invoice_id is not None
            has_bill = alloc.bill_id is not None
            if not (has_inv ^ has_bill):
                raise PersistenceError(
                    f"PaymentApplicationAllocation {alloc.id} must target invoice XOR bill"
                )

            if has_inv:
                inv = alloc.invoice
                if inv.ledger.entity_id != entity.pk:
                    raise PersistenceError(
                        f"PaymentApplicationAllocation {alloc.id} Invoice {inv.uuid} belongs to different entity"
                    )
                oblig_id = f"invoice:{inv.uuid}"
            else:
                bill = alloc.bill
                if bill.ledger.entity_id != entity.pk:
                    raise PersistenceError(
                        f"PaymentApplicationAllocation {alloc.id} Bill {bill.uuid} belongs to different entity"
                    )
                oblig_id = f"bill:{bill.uuid}"

            cash_tx = alloc.cash_transaction
            if cash_tx.journal_entry.ledger.entity_id != entity.pk:
                raise PersistenceError(
                    f"PaymentApplicationAllocation {alloc.id} cash transaction {cash_tx.uuid} belongs to different entity"
                )

            if not (cash_tx.account and cash_tx.account.role == ASSET_CA_CASH):
                raise PersistenceError(
                    f"PaymentApplicationAllocation {alloc.id} cash transaction {cash_tx.uuid} is not on a Cash/Bank account"
                )

            cash_item_id = f"tx:{cash_tx.uuid}"
            historical_book_item_ids.add(oblig_id)
            historical_book_item_ids.add(cash_item_id)
            executed_allocs.append(
                ExecutedPaymentAllocation(
                    obligation_book_item_id=oblig_id,
                    cash_transaction_book_item_id=cash_item_id,
                    amount_units=str(alloc.amount_units),
                )
            )

        executed_payments_list.append(
            ExecutedPaymentApplication(
                id=pa.id,
                company_id=str(entity.uuid),
                bank_item_id=bank_item_id,
                direction=Direction(pa.direction),
                currency=pa.currency.upper(),
                total_amount_units=str(pa.total_amount_units),
                allocations=tuple(executed_allocs),
                session_id=pa.session_id,
                state_revision_at_creation=pa.state_revision,
                created_at=pa.created_at,
            )
        )

    # 5.6 Stage-2 Reconciliations & Invalidations
    reconciliations_list: list[Reconciliation] = []
    recs = BookkeepingReconciliation.objects.filter(entity=entity).prefetch_related(
        "book_allocations__transaction",
        "bank_allocations__plaid_transaction",
        "bank_allocations__staged_transaction",
    ).order_by("id")

    for r in recs:
        bank_alloc_list: list[BankAllocation] = []
        for b_alloc in r.bank_allocations.all():
            if b_alloc.plaid_transaction_id:
                if b_alloc.plaid_transaction is None:
                    raise PersistenceError(f"Reconciliation bank allocation {b_alloc.id} missing PlaidTransaction relation.")
                b_id = f"plaid:{b_alloc.plaid_transaction.uuid}"
            elif b_alloc.staged_transaction_id:
                if b_alloc.staged_transaction is None:
                    raise PersistenceError(f"Reconciliation bank allocation {b_alloc.id} missing StagedTransaction relation.")
                b_id = f"staged:{b_alloc.staged_transaction.uuid}"
            else:
                raise PersistenceError(f"Reconciliation bank allocation {b_alloc.id} has no bank source.")
            bank_alloc_list.append(
                BankAllocation(
                    bank_item_id=b_id,
                    amount_units=str(b_alloc.amount_units),
                )
            )

        book_alloc_list: list[BookAllocation] = []
        for j_alloc in r.book_allocations.all():
            book_alloc_list.append(
                BookAllocation(
                    book_item_id=f"tx:{j_alloc.transaction.uuid}",
                    amount_units=str(j_alloc.amount_units),
                )
            )

        admissibility = (
            SemanticAdmissibility(r.source_hypothesis_admissibility)
            if r.source_hypothesis_admissibility
            else None
        )
        allocation_support = (
            AllocationSupport(r.source_hypothesis_allocation_support)
            if r.source_hypothesis_allocation_support
            else None
        )

        try:
            reconciliations_list.append(
                Reconciliation(
                    id=r.id,
                    bank_allocations=tuple(bank_alloc_list),
                    book_allocations=tuple(book_alloc_list),
                    evidence_refs=tuple(r.evidence_refs or ()),
                    source_hypothesis_id=r.source_hypothesis_id,
                    source_hypothesis_state_revision=r.source_hypothesis_state_revision,
                    source_hypothesis_utility=r.source_hypothesis_utility,
                    source_hypothesis_generated_at=r.source_hypothesis_generated_at,
                    source_hypothesis_admissibility=admissibility,
                    source_hypothesis_allocation_support=allocation_support,
                    semantic_rationale=r.semantic_rationale,
                    session_id=r.session_id,
                    state_revision_at_creation=r.state_revision_at_creation,
                    created_at=r.created_at,
                )
            )
        except (ValidationError, ValueError) as err:
            raise PersistenceError(
                f"Reconciliation {r.id} allocation invalid: {err}"
            ) from err

    reconciliation_invalidations_list: list[ReconciliationInvalidation] = []
    for ri in BookkeepingReconciliationInvalidation.objects.filter(
        reconciliation__entity=entity
    ).select_related("reconciliation").order_by("id"):
        reconciliation_invalidations_list.append(
            ReconciliationInvalidation(
                id=ri.id,
                reconciliation_id=ri.reconciliation_id,
                reason=ri.reason,
                session_id=ri.session_id,
                state_revision_at_invalidation=ri.state_revision_at_invalidation,
                created_at=ri.created_at,
            )
        )

    # 5.7 Residual Bank Classification Decisions & Invalidations
    residual_bank_classifications_list: list[ResidualBankClassificationDecision] = []
    for rcd in BookkeepingResidualBankClassificationDecision.objects.filter(entity=entity).select_related(
        "staged_transaction__import_job__bank_account_model",
        "bank_account",
        "account",
        "supersedes",
    ).order_by("id"):
        stx = rcd.staged_transaction
        if stx.import_job.bank_account_model.entity_model_id != entity.pk:
            raise PersistenceError(
                f"BookkeepingResidualBankClassificationDecision {rcd.id} staged transaction belongs to different entity."
            )

        expected_ba_id = stx.import_job.bank_account_model_id
        if rcd.bank_account_id != expected_ba_id:
            raise PersistenceError(
                f"BookkeepingResidualBankClassificationDecision {rcd.id} bank_account {rcd.bank_account_id} "
                f"does not match staged transaction's import job bank account {expected_ba_id}."
            )

        if rcd.bank_account.entity_model_id != entity.pk:
            raise PersistenceError(
                f"BookkeepingResidualBankClassificationDecision {rcd.id} bank_account belongs to different entity."
            )

        if rcd.status == BookkeepingResidualBankClassificationDecision.StatusChoices.CLASSIFIED:
            acc = rcd.account
            if acc is None:
                raise PersistenceError(
                    f"CLASSIFIED decision {rcd.id} must have account FK set."
                )
            if entity.default_coa_id is None or acc.coa_model_id != entity.default_coa_id:
                raise PersistenceError(
                    f"CLASSIFIED decision {rcd.id} references account {acc.code} not belonging to entity default_coa."
                )
            if not acc.active:
                raise PersistenceError(
                    f"CLASSIFIED decision {rcd.id} references inactive account {acc.code}."
                )
            if rcd.account_code and acc.code != rcd.account_code:
                raise PersistenceError(
                    f"CLASSIFIED decision {rcd.id} account_code '{rcd.account_code}' does not match "
                    f"linked AccountModel code '{acc.code}'."
                )

        bank_item_id = f"staged:{stx.uuid}"

        residual_bank_classifications_list.append(
            ResidualBankClassificationDecision(
                id=rcd.id,
                bank_item_id=bank_item_id,
                staged_transaction_id=str(stx.uuid),
                bank_account_id=str(rcd.bank_account.uuid),
                status=ResidualBankClassificationStatus(rcd.status),
                account_code=rcd.account_code,
                account_id=str(rcd.account.uuid) if rcd.account else None,
                original_amount_units=rcd.original_amount_units,
                residual_amount_units=rcd.residual_amount_units,
                direction=Direction(rcd.direction),
                currency=rcd.currency.upper(),
                confidence=rcd.confidence,
                rationale=rcd.rationale,
                evidence_refs=tuple(rcd.evidence_refs or ()),
                hold_reason=rcd.hold_reason,
                required_evidence=tuple(rcd.required_evidence or ()),
                schema_version=rcd.schema_version,
                dag_id=rcd.dag_id,
                request_semantic_digest=rcd.request_semantic_digest,
                session_id=rcd.session_id,
                state_revision_at_decision=rcd.state_revision_at_decision,
                persistence_revision_at_decision=rcd.persistence_revision_at_decision,
                ase_node_id=rcd.ase_node_id,
                terminal_property=rcd.terminal_property,
                supersedes_decision_id=rcd.supersedes_id if rcd.supersedes_id else None,
                created_at=rcd.created_at,
            )
        )

    residual_bank_classification_invalidations_list: list[ResidualBankClassificationInvalidation] = []
    for rci in BookkeepingResidualBankClassificationInvalidation.objects.filter(
        classification__entity=entity
    ).select_related("classification").order_by("id"):
        residual_bank_classification_invalidations_list.append(
            ResidualBankClassificationInvalidation(
                id=rci.id,
                classification_id=rci.classification_id,
                reason=rci.reason,
                session_id=rci.session_id,
                state_revision_at_invalidation=rci.state_revision_at_invalidation,
                persistence_revision_at_invalidation=rci.persistence_revision_at_invalidation,
                created_at=rci.created_at,
            )
        )

    residual_bank_postings_list: list[ResidualBankPosting] = []
    for rbp in (
        BookkeepingResidualBankPosting.objects.filter(entity=entity)
        .select_related(
            "classification",
            "classification__entity",
            "classification__bank_account",
            "classification__bank_account__account_model",
            "classification__account",
            "staged_transaction",
            "reconciliation",
            "reconciliation__entity",
            "journal_entry",
            "journal_entry__ledger",
            "journal_entry__ledger__entity",
            "bank_cash_transaction",
            "bank_cash_transaction__account",
            "bank_cash_transaction__journal_entry",
            "contra_transaction",
            "contra_transaction__account",
            "contra_transaction__journal_entry",
        )
        .prefetch_related(
            "reconciliation__bank_allocations",
            "reconciliation__book_allocations",
        )
        .order_by("id")
    ):
        dec = rbp.classification

        # 0. classification is not HOLD (strictly CLASSIFIED)
        if dec.status != "CLASSIFIED":
            raise PersistenceError(
                f"Residual bank posting {rbp.id} classification {dec.id} status is '{dec.status}', expected 'CLASSIFIED'."
            )

        # 1. posting.entity == decision.entity
        if rbp.entity_id != dec.entity_id:
            raise PersistenceError(
                f"Residual bank posting {rbp.id} entity ({rbp.entity_id}) does not match decision entity ({dec.entity_id})."
            )

        # 2. posting.staged_transaction == decision.staged_transaction
        if rbp.staged_transaction_id != dec.staged_transaction_id:
            raise PersistenceError(
                f"Residual bank posting {rbp.id} staged_transaction ({rbp.staged_transaction_id}) "
                f"does not match decision staged_transaction ({dec.staged_transaction_id})."
            )

        # 3. posting.reconciliation.entity == posting.entity
        if rbp.reconciliation.entity_id != rbp.entity_id:
            raise PersistenceError(
                f"Residual bank posting {rbp.id} reconciliation entity ({rbp.reconciliation.entity_id}) "
                f"does not match posting entity ({rbp.entity_id})."
            )

        # 4. bank and contra transaction rows both belong to posting.journal_entry and are distinct
        if rbp.bank_cash_transaction.journal_entry_id != rbp.journal_entry_id:
            raise PersistenceError(
                f"Residual bank posting {rbp.id} bank cash transaction {rbp.bank_cash_transaction_id} "
                f"does not belong to journal entry {rbp.journal_entry_id}."
            )
        if rbp.contra_transaction.journal_entry_id != rbp.journal_entry_id:
            raise PersistenceError(
                f"Residual bank posting {rbp.id} contra transaction {rbp.contra_transaction_id} "
                f"does not belong to journal entry {rbp.journal_entry_id}."
            )
        if rbp.bank_cash_transaction_id == rbp.contra_transaction_id:
            raise PersistenceError(
                f"Residual bank posting {rbp.id} has identical bank cash and contra transaction legs ({rbp.bank_cash_transaction_id})."
            )

        # 5. bank leg account == decision.bank_account.account_model
        if dec.bank_account.account_model_id is None or rbp.bank_cash_transaction.account_id != dec.bank_account.account_model_id:
            raise PersistenceError(
                f"Residual bank posting {rbp.id} bank cash transaction account ({rbp.bank_cash_transaction.account_id}) "
                f"does not match bank account cash account ({dec.bank_account.account_model_id})."
            )

        # 6. bank account role remains ASSET_CA_CASH
        if dec.bank_account.account_model.role != ASSET_CA_CASH:
            raise PersistenceError(
                f"Residual bank posting {rbp.id} bank account cash account {dec.bank_account.account_model.code} "
                f"has role {dec.bank_account.account_model.role}, expected ASSET_CA_CASH."
            )

        # 7. contra leg account == decision.account
        if dec.account_id is None or rbp.contra_transaction.account_id != dec.account_id:
            raise PersistenceError(
                f"Residual bank posting {rbp.id} contra transaction account ({rbp.contra_transaction.account_id}) "
                f"does not match decision account ({dec.account_id})."
            )

        # 8. both transaction amounts equal solver_units_to_decimal(decision.residual_amount_units)
        expected_amount = solver_units_to_decimal(dec.residual_amount_units)
        if rbp.bank_cash_transaction.amount != expected_amount:
            raise PersistenceError(
                f"Residual bank posting {rbp.id} bank cash transaction amount ({rbp.bank_cash_transaction.amount}) "
                f"does not equal decision residual amount ({expected_amount})."
            )
        if rbp.contra_transaction.amount != expected_amount:
            raise PersistenceError(
                f"Residual bank posting {rbp.id} contra transaction amount ({rbp.contra_transaction.amount}) "
                f"does not equal decision residual amount ({expected_amount})."
            )

        # 9. direction parity
        dec_dir = dec.direction.upper()
        if dec_dir in ("OUTFLOW", "BANK_OUTFLOW"):
            if rbp.bank_cash_transaction.tx_type != TransactionModel.CREDIT or rbp.contra_transaction.tx_type != TransactionModel.DEBIT:
                raise PersistenceError(
                    f"Residual bank posting {rbp.id} OUTFLOW direction requires bank leg CREDIT and contra leg DEBIT; "
                    f"got bank={rbp.bank_cash_transaction.tx_type}, contra={rbp.contra_transaction.tx_type}."
                )
        elif dec_dir in ("INFLOW", "BANK_INFLOW"):
            if rbp.bank_cash_transaction.tx_type != TransactionModel.DEBIT or rbp.contra_transaction.tx_type != TransactionModel.CREDIT:
                raise PersistenceError(
                    f"Residual bank posting {rbp.id} INFLOW direction requires bank leg DEBIT and contra leg CREDIT; "
                    f"got bank={rbp.bank_cash_transaction.tx_type}, contra={rbp.contra_transaction.tx_type}."
                )
        else:
            raise PersistenceError(
                f"Residual bank posting {rbp.id} decision has invalid direction {dec.direction}."
            )

        # 10. reconciliation bank allocation has exactly the required staged transaction allocation for decision.residual_amount_units
        bank_allocs = list(rbp.reconciliation.bank_allocations.all())
        if len(bank_allocs) != 1:
            raise PersistenceError(
                f"Residual bank posting {rbp.id} reconciliation {rbp.reconciliation_id} "
                f"has {len(bank_allocs)} bank allocations, expected exactly 1."
            )
        b_alloc = bank_allocs[0]
        if b_alloc.plaid_transaction_id is not None:
            raise PersistenceError(
                f"Residual bank posting {rbp.id} reconciliation bank allocation has plaid_transaction_id "
                f"{b_alloc.plaid_transaction_id}, expected staged_transaction only."
            )
        if b_alloc.staged_transaction_id != dec.staged_transaction_id or b_alloc.amount_units != dec.residual_amount_units:
            raise PersistenceError(
                f"Residual bank posting {rbp.id} reconciliation bank allocation does not match decision staged transaction "
                f"({b_alloc.staged_transaction_id} vs {dec.staged_transaction_id}) or units ({b_alloc.amount_units} vs {dec.residual_amount_units})."
            )

        # 11. reconciliation book allocation has exactly the bank cash transaction allocation for decision.residual_amount_units
        book_allocs = list(rbp.reconciliation.book_allocations.all())
        if len(book_allocs) != 1:
            raise PersistenceError(
                f"Residual bank posting {rbp.id} reconciliation {rbp.reconciliation_id} "
                f"has {len(book_allocs)} book allocations, expected exactly 1."
            )
        bk_alloc = book_allocs[0]
        if bk_alloc.transaction_id != rbp.bank_cash_transaction_id or bk_alloc.amount_units != dec.residual_amount_units:
            raise PersistenceError(
                f"Residual bank posting {rbp.id} reconciliation book allocation does not match bank cash transaction "
                f"({bk_alloc.transaction_id} vs {rbp.bank_cash_transaction_id}) or units ({bk_alloc.amount_units} vs {dec.residual_amount_units})."
            )

        # 12. journal entry is posted
        if not rbp.journal_entry.posted:
            raise PersistenceError(
                f"Residual bank posting {rbp.id} journal entry {rbp.journal_entry_id} is not posted."
            )

        # 13. journal entry is locked
        if not rbp.journal_entry.locked:
            raise PersistenceError(
                f"Residual bank posting {rbp.id} journal entry {rbp.journal_entry_id} is not locked."
            )

        # 14. journal entry ledger: entity == decision.entity and ledger_xid == f"bank-ledger-{decision.bank_account.uuid}"
        expected_ledger_xid = f"bank-ledger-{dec.bank_account.uuid}"
        je_ledger = rbp.journal_entry.ledger
        if je_ledger.entity_id != dec.entity_id or je_ledger.ledger_xid != expected_ledger_xid:
            raise PersistenceError(
                f"Residual bank posting {rbp.id} journal entry ledger mismatch: "
                f"entity={je_ledger.entity_id} (expected {dec.entity_id}), "
                f"ledger_xid={je_ledger.ledger_xid} (expected {expected_ledger_xid})."
            )

        # 15. posting.persistence_revision >= 1
        if rbp.persistence_revision < 1:
            raise PersistenceError(
                f"Residual bank posting {rbp.id} has invalid persistence_revision {rbp.persistence_revision}, expected >= 1."
            )

        # 16. classification account is non-null and matches decision account_code
        if dec.account is None or dec.account_id is None:
            raise PersistenceError(
                f"Residual bank posting {rbp.id} classification {dec.id} has no account."
            )
        if dec.account_code is None or dec.account.code != dec.account_code:
            raise PersistenceError(
                f"Residual bank posting {rbp.id} classification {dec.id} account code ({dec.account.code}) "
                f"does not match account_code ({dec.account_code})."
            )

        residual_bank_postings_list.append(
            ResidualBankPosting(
                id=rbp.id,
                decision_id=rbp.classification_id,
                staged_transaction_id=str(rbp.staged_transaction.uuid),
                bank_item_id=f"staged:{rbp.staged_transaction.uuid}",
                journal_entry_id=str(rbp.journal_entry.uuid),
                bank_cash_transaction_id=str(rbp.bank_cash_transaction.uuid),
                contra_transaction_id=str(rbp.contra_transaction.uuid),
                reconciliation_id=rbp.reconciliation_id,
                persistence_revision=rbp.persistence_revision,
                posted_at=rbp.posted_at,
            )
        )

    return BookkeepingSnapshot(
        persistence_revision=persistence_revision,
        context=context,
        bank_accounts=tuple(sorted(bank_accounts_list, key=lambda a: a.id)),
        bank_items=tuple(sorted(bank_items_list, key=lambda a: a.id)),
        book_items=tuple(sorted(book_items_list, key=lambda a: a.id)),
        historical_book_item_ids=tuple(sorted(historical_book_item_ids)),
        counterparties=tuple(sorted(counterparties_list, key=lambda a: a.id)),
        documents=(),
        routing_decisions=tuple(sorted(routing_decisions_list, key=lambda a: a.id)),
        routing_invalidations=tuple(sorted(routing_invalidations_list, key=lambda a: a.id)),
        classifications=tuple(sorted(classifications_list, key=lambda a: a.id)),
        classification_invalidations=tuple(sorted(classification_invalidations_list, key=lambda a: a.id)),
        reconciliations=tuple(sorted(reconciliations_list, key=lambda a: a.id)),
        reconciliation_invalidations=tuple(sorted(reconciliation_invalidations_list, key=lambda a: a.id)),
        book_item_evidence_assertions=tuple(sorted(evidence_assertions_list, key=lambda a: a.id)),
        book_item_evidence_invalidations=tuple(sorted(evidence_invalidations_list, key=lambda a: a.id)),
        executed_payment_applications=tuple(sorted(executed_payments_list, key=lambda a: a.id)),
        residual_bank_classifications=tuple(sorted(residual_bank_classifications_list, key=lambda a: a.id)),
        residual_bank_classification_invalidations=tuple(sorted(residual_bank_classification_invalidations_list, key=lambda a: a.id)),
        residual_bank_postings=tuple(sorted(residual_bank_postings_list, key=lambda a: a.id)),
    )

