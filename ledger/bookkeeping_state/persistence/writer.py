from __future__ import annotations

import uuid
from decimal import Decimal
from typing import TYPE_CHECKING, Any, cast

from bookkeeping_state.domain.money import (
    amount_units_to_int,
    major_units_to_solver_units,
    solver_units_to_decimal,
)
from bookkeeping_state.persistence.repository import (
    PersistenceCommitResult,
    PersistenceConflictError,
    PersistenceDuplicateError,
    PersistenceError,
    PersistenceWriteSet,
)
from django.core.exceptions import (
    MultipleObjectsReturned,
    ObjectDoesNotExist,
    ValidationError,
)
from django.db import IntegrityError, transaction


def _is_unique_violation(exc: IntegrityError) -> bool:
    """
    Check if an IntegrityError was caused by a unique constraint violation,
    preferring PostgreSQL error code 23505 where available.
    """
    cause = getattr(exc, "__cause__", None) or getattr(exc, "__context__", None)
    pgcode = getattr(cause, "pgcode", None) or getattr(exc, "pgcode", None)
    if pgcode == "23505":
        return True
    msg = str(exc).lower()
    return "unique" in msg or "duplicate" in msg

if TYPE_CHECKING:
    from ledger.models.accounts import AccountModel
    from ledger.models.bank_account import BankAccountModel
    from ledger.models.bill import BillModel
    from ledger.models.data_import import StagedTransactionModel
    from ledger.models.entity import EntityModel
    from ledger.models.invoice import InvoiceModel
    from ledger.models.plaid import PlaidTransaction
    from ledger.models.transactions import TransactionModel


def _resolve_entity_for_update(*, company_id: str) -> EntityModel:
    """
    Deterministically resolve and row-lock EntityModel by UUID or slug.

    Rules:
    - No .first()
    - No LIMIT 1
    - select_for_update() row-locks the entity boundary
    """
    from ledger.models.entity import EntityModel

    entity: EntityModel | None = None

    # 1. Try UUID lookup
    try:
        entity_uuid = uuid.UUID(company_id)
        try:
            entity = EntityModel.objects.select_for_update(of=("self",)).get(uuid=entity_uuid)
        except ObjectDoesNotExist:
            entity = None
    except (ValueError, TypeError, AttributeError):
        entity = None

    # 2. Try exact match on slug
    if entity is None:
        try:
            entity = EntityModel.objects.select_for_update(of=("self",)).get(slug__exact=company_id)
        except ObjectDoesNotExist:
            raise PersistenceError(
                f"Entity not found for company_id '{company_id}'."
            ) from None
        except MultipleObjectsReturned:
            raise PersistenceError(
                f"Ambiguous company_id '{company_id}': multiple entities found."
            ) from None

    if entity is None:
        raise PersistenceError(f"Entity not found for company_id '{company_id}'.")

    return entity


def _resolve_source_target(
    *,
    book_item_id: str,
    entity: EntityModel,
) -> tuple[InvoiceModel | None, BillModel | None, TransactionModel | None]:
    """
    Resolve book_item_id (invoice:<uuid>, bill:<uuid>, tx:<uuid>) into exactly one Django model,
    validating that it belongs to the given entity.
    """
    from ledger.models.bill import BillModel
    from ledger.models.invoice import InvoiceModel
    from ledger.models.transactions import TransactionModel

    parts = book_item_id.split(":", 1)
    if len(parts) != 2:
        raise PersistenceError(f"Invalid book_item_id format: '{book_item_id}'")

    prefix, raw_uuid = parts[0], parts[1]
    try:
        item_uuid = uuid.UUID(raw_uuid)
    except (ValueError, TypeError):
        raise PersistenceError(
            f"Invalid UUID '{raw_uuid}' in book_item_id '{book_item_id}'."
        )

    if prefix == "invoice":
        try:
            inv = InvoiceModel.objects.select_related("ledger").get(uuid=item_uuid)
        except ObjectDoesNotExist:
            raise PersistenceError(f"Invoice '{item_uuid}' not found.")
        if inv.ledger.entity_id != entity.pk:
            raise PersistenceError(
                f"Invoice '{item_uuid}' belongs to different entity {inv.ledger.entity_id}."
            )
        return inv, None, None

    elif prefix == "bill":
        try:
            bill = BillModel.objects.select_related("ledger").get(uuid=item_uuid)
        except ObjectDoesNotExist:
            raise PersistenceError(f"Bill '{item_uuid}' not found.")
        if bill.ledger.entity_id != entity.pk:
            raise PersistenceError(
                f"Bill '{item_uuid}' belongs to different entity {bill.ledger.entity_id}."
            )
        return None, bill, None

    elif prefix == "tx":
        try:
            tx = TransactionModel.objects.select_related("journal_entry__ledger").get(
                uuid=item_uuid
            )
        except ObjectDoesNotExist:
            raise PersistenceError(f"Transaction '{item_uuid}' not found.")
        if tx.journal_entry.ledger.entity_id != entity.pk:
            raise PersistenceError(
                f"Transaction '{item_uuid}' belongs to different entity {tx.journal_entry.ledger.entity_id}."
            )
        return None, None, tx

    else:
        raise PersistenceError(
            f"Unsupported book_item_id prefix '{prefix}' in '{book_item_id}'."
        )


def _resolve_account_for_entity(
    *,
    account_code: str,
    entity: EntityModel,
) -> AccountModel:
    """
    Resolve AccountModel deterministically within entity's active/default COA.
    """
    from ledger.models.accounts import AccountModel

    default_coa_id = getattr(entity, "default_coa_id", None)
    if default_coa_id:
        accounts = list(
            AccountModel.objects.filter(
                coa_model_id=default_coa_id,
                code=account_code,
            )
        )
    else:
        accounts = list(
            AccountModel.objects.filter(
                coa_model__entity=entity,
                code=account_code,
            )
        )

    if not accounts:
        raise PersistenceError(
            f"Account with code '{account_code}' not found for entity '{entity.slug}'."
        )
    if len(accounts) > 1:
        raise PersistenceError(
            f"Ambiguous account code '{account_code}' for entity '{entity.slug}': found {len(accounts)} accounts."
        )

    account = accounts[0]
    if account.code != account_code:
        raise PersistenceError(
            f"Account code mismatch: expected '{account_code}', found '{account.code}'."
        )
    return account


def _resolve_bank_account_for_entity(
    *,
    bank_account_id: str,
    entity: EntityModel,
) -> BankAccountModel:
    """
    Resolve BankAccountModel by UUID and validate it belongs to entity.
    """
    from ledger.models.bank_account import BankAccountModel

    try:
        ba_uuid = uuid.UUID(bank_account_id)
    except (ValueError, TypeError):
        raise PersistenceError(
            f"Invalid UUID for bank_account_id '{bank_account_id}'."
        )

    try:
        ba = BankAccountModel.objects.get(uuid=ba_uuid, entity_model=entity)
    except ObjectDoesNotExist:
        raise PersistenceError(
            f"BankAccount '{bank_account_id}' not found for entity '{entity.slug}'."
        )
    return ba


def _resolve_reconciliation_book_source(
    *,
    book_item_id: str,
    entity: EntityModel,
) -> TransactionModel:
    """
    Validate and resolve book side of a Stage-2 reconciliation.
    Strictly requires tx:<uuid>, rejects invoice: and bill: targets defensively.
    Must be a posted transaction on a cash/bank account.
    """
    from ledger.io.roles import ASSET_CA_CASH
    from ledger.models.bank_account import BankAccountModel
    from ledger.models.transactions import TransactionModel

    parts = book_item_id.split(":", 1)
    if len(parts) != 2:
        raise PersistenceError(f"Invalid book_item_id format: '{book_item_id}'")

    prefix, raw_uuid = parts[0], parts[1]
    if prefix != "tx":
        raise PersistenceError(
            f"Reconciliation book allocation must target a posted transaction ('tx:<uuid>'), got '{book_item_id}'."
        )

    try:
        tx_uuid = uuid.UUID(raw_uuid)
    except (ValueError, TypeError):
        raise PersistenceError(f"Invalid UUID '{raw_uuid}' in book_item_id.")

    try:
        tx = TransactionModel.objects.select_related(
            "journal_entry__ledger", "account"
        ).get(uuid=tx_uuid)
    except ObjectDoesNotExist:
        raise PersistenceError(f"Transaction '{tx_uuid}' not found.")

    if tx.journal_entry.ledger.entity_id != entity.pk:
        raise PersistenceError(
            f"Transaction '{tx_uuid}' belongs to different entity."
        )

    if not tx.journal_entry.posted:
        raise PersistenceError(f"Transaction '{tx_uuid}' is not posted.")

    is_cash_role = tx.account and tx.account.role == ASSET_CA_CASH
    is_entity_bank_acc = BankAccountModel.objects.filter(
        entity_model=entity, account_model=tx.account
    ).exists()
    if not (is_cash_role or is_entity_bank_acc):
        raise PersistenceError(
            f"Transaction '{tx_uuid}' is not on a cash/bank account eligible for reconciliation."
        )

    return tx


def _resolve_reconciliation_bank_source(
    *,
    bank_item_id: str,
    entity: EntityModel,
) -> tuple[PlaidTransaction | None, StagedTransactionModel | None]:
    """
    Resolve bank side of a Stage-2 reconciliation (plaid:<uuid> or staged:<uuid>).
    """
    from ledger.models.bank_account import BankAccountModel
    from ledger.models.data_import import StagedTransactionModel
    from ledger.models.plaid import PlaidTransaction

    parts = bank_item_id.split(":", 1)
    if len(parts) != 2:
        raise PersistenceError(f"Invalid bank_item_id format: '{bank_item_id}'")

    prefix, raw_uuid = parts[0], parts[1]
    try:
        bank_uuid = uuid.UUID(raw_uuid)
    except (ValueError, TypeError):
        raise PersistenceError(f"Invalid UUID '{raw_uuid}' in bank_item_id.")

    if prefix == "plaid":
        try:
            ptx = PlaidTransaction.objects.select_related("plaid_item").get(
                uuid=bank_uuid
            )
        except ObjectDoesNotExist:
            raise PersistenceError(f"PlaidTransaction '{bank_uuid}' not found.")

        if not BankAccountModel.objects.filter(
            entity_model=entity, plaid_item=ptx.plaid_item
        ).exists():
            raise PersistenceError(
                f"PlaidTransaction '{bank_uuid}' does not belong to entity '{entity.slug}'."
            )
        return ptx, None

    elif prefix == "staged":
        try:
            stx = StagedTransactionModel.objects.select_related(
                "import_job__bank_account_model"
            ).get(uuid=bank_uuid)
        except ObjectDoesNotExist:
            raise PersistenceError(f"StagedTransaction '{bank_uuid}' not found.")

        if stx.import_job.bank_account_model.entity_model_id != entity.pk:
            raise PersistenceError(
                f"StagedTransaction '{bank_uuid}' does not belong to entity '{entity.slug}'."
            )
        return None, stx

    else:
        raise PersistenceError(
            f"Unsupported bank_item_id prefix '{prefix}' in '{bank_item_id}'."
        )


def _persist_reconciliation_record(
    *,
    entity: EntityModel,
    reconciliation_id: str,
    evidence_refs: list[str] | None = None,
    source_hypothesis_id: str | None = None,
    source_hypothesis_state_revision: int | None = None,
    source_hypothesis_utility: int | None = None,
    source_hypothesis_generated_at: Any = None,
    source_hypothesis_admissibility: str | None = None,
    source_hypothesis_allocation_support: str | None = None,
    semantic_rationale: str | None = None,
    session_id: str | None = None,
    state_revision_at_creation: int,
    created_at: Any,
    bank_allocations: list[tuple[PlaidTransaction | None, StagedTransactionModel | None, int]],
    book_allocations: list[tuple[TransactionModel, int]],
) -> Any:
    """
    Canonical shared writer for BookkeepingReconciliation and its allocations.

    Preserves the exact Stage 2 persistence contract:
    - reconciliation ID handling
    - evidence_refs
    - source-hypothesis provenance
    - semantic rationale
    - session_id
    - state_revision_at_creation
    - created_at
    - bank allocations (XOR target: strictly one of plaid_transaction or staged_transaction)
    - book allocations (transaction)
    - AmountUnits positivity (units > 0)
    """
    from ledger.models.bookkeeping import (
        BookkeepingReconciliation,
        BookkeepingReconciliationBankAllocation,
        BookkeepingReconciliationBookAllocation,
    )

    for ptx, stx, units in bank_allocations:
        if units <= 0:
            raise PersistenceError(
                f"Reconciliation bank allocation amount must be positive, got {units}."
            )
        if (ptx is None and stx is None) or (ptx is not None and stx is not None):
            raise PersistenceError(
                "Reconciliation bank allocation must target strictly one of plaid_transaction or staged_transaction."
            )

    for tx, units in book_allocations:
        if units <= 0:
            raise PersistenceError(
                f"Reconciliation book allocation amount must be positive, got {units}."
            )

    rec_row = BookkeepingReconciliation.objects.create(
        id=reconciliation_id,
        entity=entity,
        evidence_refs=list(evidence_refs or ()),
        source_hypothesis_id=source_hypothesis_id,
        source_hypothesis_state_revision=source_hypothesis_state_revision,
        source_hypothesis_utility=source_hypothesis_utility,
        source_hypothesis_generated_at=source_hypothesis_generated_at,
        source_hypothesis_admissibility=source_hypothesis_admissibility,
        source_hypothesis_allocation_support=source_hypothesis_allocation_support,
        semantic_rationale=semantic_rationale,
        session_id=session_id,
        state_revision_at_creation=state_revision_at_creation,
        created_at=created_at,
    )

    for tx, units in book_allocations:
        BookkeepingReconciliationBookAllocation.objects.create(
            reconciliation=rec_row,
            transaction=tx,
            amount_units=units,
        )

    for ptx, stx, units in bank_allocations:
        BookkeepingReconciliationBankAllocation.objects.create(
            reconciliation=rec_row,
            plaid_transaction=ptx,
            staged_transaction=stx,
            amount_units=units,
        )

    return rec_row


def commit_django_write_set(
    *,
    company_id: str,
    expected_revision: int,
    write_set: PersistenceWriteSet,
) -> PersistenceCommitResult:
    """
    Atomically commit a PersistenceWriteSet to Django durable bookkeeping storage.

    OCC Concurrency Contract:
    1. transaction.atomic()
    2. select_for_update() on EntityModel row
    3. If write_set.is_empty:
       - verify revision == expected_revision
       - do NOT create revision row
       - do NOT advance revision
       - return previous_revision == new_revision == expected_revision
    4. If write_set is not empty:
       - get_or_create BookkeepingRevision under entity lock
       - verify revision == expected_revision (raises PersistenceConflictError if stale)
       - validate all write-set references
       - insert entire write set using explicit .objects.create(...)
       - increment revision exactly once
       - commit
    """
    from ledger.models.bookkeeping import (
        BookkeepingClassificationDecision,
        BookkeepingClassificationInvalidation,
        BookkeepingEvidenceAssertion,
        BookkeepingEvidenceInvalidation,
        BookkeepingReconciliation,
        BookkeepingReconciliationBankAllocation,
        BookkeepingReconciliationBookAllocation,
        BookkeepingReconciliationInvalidation,
        BookkeepingResidualBankClassificationDecision,
        BookkeepingResidualBankClassificationInvalidation,
        BookkeepingResidualBankPosting,
        BookkeepingRevision,
        BookkeepingRoutingDecision,
        BookkeepingRoutingInvalidation,
    )

    # Defensively reject read projections from write-set
    if (
        write_set.bank_accounts
        or write_set.bank_items
        or write_set.book_items
        or write_set.documents
        or write_set.counterparties
    ):
        raise PersistenceError(
            "PersistenceWriteSet cannot contain read projections (bank_accounts, bank_items, "
            "book_items, documents, counterparties). Decision commits only."
        )

    try:
        with cast(Any, transaction.atomic)():
            # 1. Row-lock canonical EntityModel
            entity = _resolve_entity_for_update(company_id=company_id)

            # 2. Check empty write set
            if write_set.is_empty:
                rev_row = BookkeepingRevision.objects.filter(entity=entity).first()
                current_revision = rev_row.revision if rev_row is not None else 0
                if current_revision != expected_revision:
                    raise PersistenceConflictError(
                        f"Stale revision for entity '{entity.slug}': "
                        f"expected {expected_revision}, current {current_revision}."
                    )
                return PersistenceCommitResult(
                    previous_revision=expected_revision,
                    new_revision=expected_revision,
                    write_set=write_set,
                )

            # 3. Lock or initialize BookkeepingRevision
            rev_obj, _ = BookkeepingRevision.objects.select_for_update().get_or_create(
                entity=entity,
                defaults={"revision": 0},
            )

            if rev_obj.revision != expected_revision:
                raise PersistenceConflictError(
                    f"Stale persistence revision for entity '{entity.slug}': "
                    f"expected {expected_revision}, current {rev_obj.revision}."
                )

            # 4. Insert Routing Decisions
            for rd in write_set.routing_decisions:
                inv, bill, tx = _resolve_source_target(
                    book_item_id=rd.book_item_id, entity=entity
                )
                bank_acc = _resolve_bank_account_for_entity(
                    bank_account_id=rd.bank_account_id, entity=entity
                )

                supersedes_rd = None
                if rd.supersedes_routing_decision_id:
                    try:
                        supersedes_rd = BookkeepingRoutingDecision.objects.get(
                            id=rd.supersedes_routing_decision_id,
                            entity=entity,
                        )
                    except ObjectDoesNotExist:
                        raise PersistenceError(
                            f"Superseded routing decision '{rd.supersedes_routing_decision_id}' "
                            f"not found for entity '{entity.slug}'."
                        )
                    # Validate supersession targets the same book item
                    if (
                        getattr(supersedes_rd, "invoice_id", None) != (inv.pk if inv else None)
                        or getattr(supersedes_rd, "bill_id", None) != (bill.pk if bill else None)
                        or getattr(supersedes_rd, "transaction_id", None) != (tx.pk if tx else None)
                    ):
                        raise PersistenceError(
                            f"Routing decision '{rd.id}' targets a different book item "
                            f"than superseded decision '{supersedes_rd.id}'."
                        )

                BookkeepingRoutingDecision.objects.create(
                    id=rd.id,
                    entity=entity,
                    invoice=inv,
                    bill=bill,
                    transaction=tx,
                    bank_account=bank_acc,
                    source=str(rd.source),
                    utility=rd.utility,
                    solver_run_id=rd.solver_run_id,
                    supersedes=supersedes_rd,
                    session_id=rd.session_id,
                    state_revision_at_creation=rd.state_revision_at_creation,
                    created_at=rd.created_at,
                )

            # 5. Insert Routing Invalidations
            for ri in write_set.routing_invalidations:
                try:
                    rd_target = BookkeepingRoutingDecision.objects.get(
                        id=ri.routing_decision_id
                    )
                except ObjectDoesNotExist:
                    raise PersistenceError(
                        f"Routing decision '{ri.routing_decision_id}' not found to invalidate."
                    )
                if getattr(rd_target, "entity_id", None) != entity.pk:
                    raise PersistenceError(
                        f"Routing decision '{ri.routing_decision_id}' belongs to different entity."
                    )

                BookkeepingRoutingInvalidation.objects.create(
                    id=ri.id,
                    routing_decision=rd_target,
                    reason=ri.reason,
                    session_id=ri.session_id,
                    state_revision_at_invalidation=ri.state_revision_at_invalidation,
                    created_at=ri.created_at,
                )

            # 6. Insert Classification Decisions
            for cd in write_set.classifications:
                inv, bill, tx = _resolve_source_target(
                    book_item_id=cd.book_item_id, entity=entity
                )
                account = _resolve_account_for_entity(
                    account_code=cd.account_code, entity=entity
                )

                supersedes_cd = None
                if cd.supersedes_classification_id:
                    try:
                        supersedes_cd = (
                            BookkeepingClassificationDecision.objects.get(
                                id=cd.supersedes_classification_id,
                                entity=entity,
                            )
                        )
                    except ObjectDoesNotExist:
                        raise PersistenceError(
                            f"Superseded classification '{cd.supersedes_classification_id}' "
                            f"not found for entity '{entity.slug}'."
                        )
                    # Validate same book item target
                    if (
                        getattr(supersedes_cd, "invoice_id", None) != (inv.pk if inv else None)
                        or getattr(supersedes_cd, "bill_id", None) != (bill.pk if bill else None)
                        or getattr(supersedes_cd, "transaction_id", None) != (tx.pk if tx else None)
                    ):
                        raise PersistenceError(
                            f"Classification '{cd.id}' targets a different book item "
                            f"than superseded classification '{supersedes_cd.id}'."
                        )

                BookkeepingClassificationDecision.objects.create(
                    id=cd.id,
                    entity=entity,
                    invoice=inv,
                    bill=bill,
                    transaction=tx,
                    account_code=account.code,
                    account=account,
                    source=str(cd.source),
                    confidence=cd.confidence,
                    rationale=cd.rationale,
                    evidence_refs=list(cd.evidence_refs or ()),
                    supersedes=supersedes_cd,
                    session_id=cd.session_id,
                    dag_run_id=cd.dag_run_id,
                    ase_node_id=cd.ase_node_id,
                    state_revision_at_decision=cd.state_revision_at_decision,
                    created_at=cd.created_at,
                )

            # 7. Insert Classification Invalidations
            for ci in write_set.classification_invalidations:
                try:
                    cd_target = BookkeepingClassificationDecision.objects.get(
                        id=ci.classification_id
                    )
                except ObjectDoesNotExist:
                    raise PersistenceError(
                        f"Classification '{ci.classification_id}' not found to invalidate."
                    )
                if getattr(cd_target, "entity_id", None) != entity.pk:
                    raise PersistenceError(
                        f"Classification '{ci.classification_id}' belongs to different entity."
                    )

                BookkeepingClassificationInvalidation.objects.create(
                    id=ci.id,
                    classification=cd_target,
                    reason=ci.reason,
                    session_id=ci.session_id,
                    state_revision_at_invalidation=ci.state_revision_at_invalidation,
                    created_at=ci.created_at,
                )

            # 8. Insert Evidence Assertions
            for ea in write_set.book_item_evidence_assertions:
                inv, bill, tx = _resolve_source_target(
                    book_item_id=ea.book_item_id, entity=entity
                )

                supersedes_ea = None
                if ea.supersedes_assertion_id:
                    try:
                        supersedes_ea = BookkeepingEvidenceAssertion.objects.get(
                            id=ea.supersedes_assertion_id,
                            entity=entity,
                        )
                    except ObjectDoesNotExist:
                        raise PersistenceError(
                            f"Superseded evidence assertion '{ea.supersedes_assertion_id}' "
                            f"not found for entity '{entity.slug}'."
                        )
                    # Dimension-safety check: evidence types must match
                    if supersedes_ea.evidence_type != str(ea.evidence_type):
                        raise PersistenceError(
                            f"Evidence assertion '{ea.id}' type '{ea.evidence_type}' "
                            f"does not match superseded assertion type '{supersedes_ea.evidence_type}'."
                        )
                    # Validate same book item target
                    if (
                        getattr(supersedes_ea, "invoice_id", None) != (inv.pk if inv else None)
                        or getattr(supersedes_ea, "bill_id", None) != (bill.pk if bill else None)
                        or getattr(supersedes_ea, "transaction_id", None) != (tx.pk if tx else None)
                    ):
                        raise PersistenceError(
                            f"Evidence assertion '{ea.id}' targets a different book item "
                            f"than superseded assertion '{supersedes_ea.id}'."
                        )

                BookkeepingEvidenceAssertion.objects.create(
                    id=ea.id,
                    entity=entity,
                    invoice=inv,
                    bill=bill,
                    transaction=tx,
                    evidence_type=str(ea.evidence_type),
                    value=ea.value,
                    document_ids=list(ea.document_ids or ()),
                    source=str(ea.source),
                    supersedes=supersedes_ea,
                    reason=ea.reason,
                    confidence=ea.confidence,
                    metadata=dict(ea.metadata or {}),
                    session_id=ea.session_id,
                    state_revision_at_creation=ea.state_revision_at_creation,
                    created_at=ea.created_at,
                )

            # 9. Insert Evidence Invalidations
            for ei in write_set.book_item_evidence_invalidations:
                try:
                    ea_target = BookkeepingEvidenceAssertion.objects.get(
                        id=ei.assertion_id
                    )
                except ObjectDoesNotExist:
                    raise PersistenceError(
                        f"Evidence assertion '{ei.assertion_id}' not found to invalidate."
                    )
                if getattr(ea_target, "entity_id", None) != entity.pk:
                    raise PersistenceError(
                        f"Evidence assertion '{ei.assertion_id}' belongs to different entity."
                    )

                BookkeepingEvidenceInvalidation.objects.create(
                    id=ei.id,
                    assertion=ea_target,
                    reason=ei.reason,
                    session_id=ei.session_id,
                    state_revision_at_invalidation=ei.state_revision_at_invalidation,
                    created_at=ei.created_at,
                )

            # 10. Insert Stage-2 Reconciliations & Allocations
            for r in write_set.reconciliations:
                # Pre-validate all allocations before creating parent
                book_resolved: list[tuple[TransactionModel, int]] = []
                for b_alloc in r.book_allocations:
                    tx = _resolve_reconciliation_book_source(
                        book_item_id=b_alloc.book_item_id, entity=entity
                    )
                    units = amount_units_to_int(b_alloc.amount_units)
                    if units <= 0:
                        raise PersistenceError(
                            f"Reconciliation book allocation amount must be positive, got {units}."
                        )
                    book_resolved.append((tx, units))

                bank_resolved: list[
                    tuple[PlaidTransaction | None, StagedTransactionModel | None, int]
                ] = []
                for b_alloc in r.bank_allocations:
                    ptx, stx = _resolve_reconciliation_bank_source(
                        bank_item_id=b_alloc.bank_item_id, entity=entity
                    )
                    units = amount_units_to_int(b_alloc.amount_units)
                    if units <= 0:
                        raise PersistenceError(
                            f"Reconciliation bank allocation amount must be positive, got {units}."
                        )
                    bank_resolved.append((ptx, stx, units))

                admissibility = (
                    str(r.source_hypothesis_admissibility.value)
                    if r.source_hypothesis_admissibility
                    else None
                )
                allocation_support = (
                    str(r.source_hypothesis_allocation_support.value)
                    if r.source_hypothesis_allocation_support
                    else None
                )

                _persist_reconciliation_record(
                    entity=entity,
                    reconciliation_id=r.id,
                    evidence_refs=list(r.evidence_refs or ()),
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
                    bank_allocations=bank_resolved,
                    book_allocations=book_resolved,
                )

            # 11. Insert Reconciliation Invalidations
            for ri in write_set.reconciliation_invalidations:
                try:
                    rec_target = BookkeepingReconciliation.objects.get(
                        id=ri.reconciliation_id
                    )
                except ObjectDoesNotExist:
                    raise PersistenceError(
                        f"Reconciliation '{ri.reconciliation_id}' not found to invalidate."
                    )
                if getattr(rec_target, "entity_id", None) != entity.pk:
                    raise PersistenceError(
                        f"Reconciliation '{ri.reconciliation_id}' belongs to different entity."
                    )

                from ledger.models.bookkeeping import BookkeepingResidualBankPosting
                if BookkeepingResidualBankPosting.objects.filter(
                    reconciliation_id=ri.reconciliation_id
                ).exists():
                    raise PersistenceError(
                        f"CANNOT_INVALIDATE_POSTING_RECONCILIATION: Reconciliation '{ri.reconciliation_id}' "
                        "is owned by an authoritative residual bank posting and requires formal posting reversal."
                    )

                BookkeepingReconciliationInvalidation.objects.create(
                    id=ri.id,
                    reconciliation=rec_target,
                    reason=ri.reason,
                    session_id=ri.session_id,
                    state_revision_at_invalidation=ri.state_revision_at_invalidation,
                    created_at=ri.created_at,
                )

            # 11.5. Execute Stage-1 Payment Applications & Insert Provenance
            for pa_exec in write_set.payment_applications_to_execute:
                from bookkeeping_state.persistence.bank_normalization import (
                    normalize_plaid_movement,
                    normalize_staged_movement,
                )

                from ledger.io.io_core import get_localtime
                from ledger.io.roles import ASSET_CA_CASH
                from ledger.models.bank_account import BankAccountModel
                from ledger.models.bill import BillModel
                from ledger.models.bookkeeping import (
                    BookkeepingPaymentApplication,
                    BookkeepingPaymentApplicationAllocation,
                    BookkeepingReconciliationBankAllocation,
                )
                from ledger.models.invoice import InvoiceModel

                # A. Resolve bank movement source
                ptx, stx = _resolve_reconciliation_bank_source(
                    bank_item_id=pa_exec.bank_item_id,
                    entity=entity,
                )

                if ptx is not None:
                    ba = BankAccountModel.objects.get(
                        entity_model=entity,
                        plaid_item=ptx.plaid_item,
                    )
                    _, dir_enum, currency = normalize_plaid_movement(ptx)
                    direction = dir_enum.value
                else:
                    assert stx is not None
                    import_job = cast(Any, stx.import_job)
                    ba = import_job.bank_account_model
                    _, dir_enum, currency = normalize_staged_movement(stx, entity=entity)
                    direction = dir_enum.value

                target_cash_account = ba.account_model
                if target_cash_account is None:
                    raise PersistenceError(
                        f"BankAccount '{ba.uuid}' has no linked accounting AccountModel."
                    )
                if target_cash_account.role != ASSET_CA_CASH:
                    raise PersistenceError(
                        f"BankAccount '{ba.uuid}' linked account '{target_cash_account.code}' does not have ASSET_CA_CASH role."
                    )

                # B. Verify bank item is not already claimed in Stage-2 reconciliations or prior payment application
                if ptx is not None:
                    has_st2 = BookkeepingReconciliationBankAllocation.objects.filter(
                        plaid_transaction=ptx,
                        reconciliation__invalidations__isnull=True,
                    ).exists()
                    has_app = BookkeepingPaymentApplication.objects.filter(
                        plaid_transaction=ptx,
                    ).exists()
                else:
                    has_st2 = BookkeepingReconciliationBankAllocation.objects.filter(
                        staged_transaction=stx,
                        reconciliation__invalidations__isnull=True,
                    ).exists()
                    has_app = BookkeepingPaymentApplication.objects.filter(
                        staged_transaction=stx,
                    ).exists()

                if has_st2:
                    raise PersistenceError(
                        f"Bank item '{pa_exec.bank_item_id}' already has active Stage-2 reconciliation allocations."
                    )
                if has_app:
                    raise PersistenceError(
                        f"Bank item '{pa_exec.bank_item_id}' already has an executed payment application."
                    )

                # C. Check amounts
                total_units = amount_units_to_int(pa_exec.total_amount_units)
                alloc_units_sum = sum(
                    amount_units_to_int(alloc.amount_units) for alloc in pa_exec.allocations
                )
                if total_units != alloc_units_sum:
                    raise PersistenceError(
                        f"Total amount units ({total_units}) does not match sum of allocations ({alloc_units_sum})."
                    )

                executed_allocations: list[
                    tuple[InvoiceModel | None, BillModel | None, TransactionModel, int]
                ] = []

                # D. Process each obligation allocation through accounting kernel
                for alloc in pa_exec.allocations:
                    inv, bill, tx = _resolve_source_target(
                        book_item_id=alloc.book_item_id,
                        entity=entity,
                    )
                    if tx is not None:
                        raise PersistenceError(
                            f"Cannot apply payment to posted transaction '{alloc.book_item_id}'; only invoices or bills are valid obligations."
                        )
                    if inv is None and bill is None:
                        raise PersistenceError(f"Obligation '{alloc.book_item_id}' not found.")

                    alloc_units = amount_units_to_int(alloc.amount_units)
                    payment_amount = solver_units_to_decimal(alloc.amount_units)

                    if inv is not None:
                        inv = InvoiceModel.objects.select_for_update().get(uuid=inv.uuid)
                        if not inv.is_approved():
                            raise PersistenceError(
                                f"Invoice '{inv.invoice_number}' is not in approved status."
                            )
                        rem_due = Decimal(str(inv.amount_due)) - Decimal(str(inv.amount_paid))
                        if payment_amount > rem_due:
                            raise PersistenceError(
                                f"Payment amount {payment_amount} exceeds invoice {inv.invoice_number} remaining amount due {rem_due}."
                            )
                        posting_res = inv.make_payment_with_result(
                            payment_amount=payment_amount,
                            payment_date=pa_exec.payment_date,
                            cash_account_override=target_cash_account,
                            commit=True,
                            raise_exception=True,
                        )
                    else:
                        assert bill is not None
                        bill = BillModel.objects.select_for_update().get(uuid=bill.uuid)
                        if not bill.is_approved():
                            raise PersistenceError(
                                f"Bill '{bill.bill_number}' is not in approved status."
                            )
                        rem_due = Decimal(str(bill.amount_due)) - Decimal(str(bill.amount_paid))
                        if payment_amount > rem_due:
                            raise PersistenceError(
                                f"Payment amount {payment_amount} exceeds bill {bill.bill_number} remaining amount due {rem_due}."
                            )
                        posting_res = bill.make_payment_with_result(
                            payment_amount=payment_amount,
                            payment_date=pa_exec.payment_date,
                            cash_account_override=target_cash_account,
                            commit=True,
                            raise_exception=True,
                        )

                    if posting_res is None or posting_res.cash_transaction is None:
                        raise PersistenceError(
                            "Accounting kernel did not produce a posted cash transaction."
                        )

                    cash_tx = posting_res.cash_transaction
                    if cash_tx.account_id != target_cash_account.pk:
                        raise PersistenceError(
                            f"Cash transaction account {cash_tx.account_id} does not match target cash account {target_cash_account.pk}."
                        )
                    if not cash_tx.journal_entry.posted:
                        raise PersistenceError("Resulting cash transaction is not posted.")

                    executed_allocations.append((inv, bill, cash_tx, alloc_units))

                # E. Insert durable BookkeepingPaymentApplication row
                now = get_localtime()
                app_row = BookkeepingPaymentApplication.objects.create(
                    id=pa_exec.payment_application_id,
                    entity=entity,
                    plaid_transaction=ptx,
                    staged_transaction=stx,
                    total_amount_units=total_units,
                    direction=direction,
                    currency=currency,
                    session_id=pa_exec.session_id,
                    state_revision=expected_revision,
                    created_at=now,
                )

                # F. Insert durable BookkeepingPaymentApplicationAllocation rows
                for inv_target, bill_target, cash_tx, units in executed_allocations:
                    BookkeepingPaymentApplicationAllocation.objects.create(
                        payment_application=app_row,
                        invoice=inv_target,
                        bill=bill_target,
                        amount_units=units,
                        cash_transaction=cash_tx,
                    )

            # 11.6. Insert Residual Bank Classification Decisions
            for rcd in write_set.residual_bank_classifications:
                parts = rcd.bank_item_id.split(":", 1)
                if len(parts) != 2 or parts[0] != "staged":
                    raise PersistenceError(
                        f"Invalid bank_item_id format for residual classification: {rcd.bank_item_id!r}"
                    )
                try:
                    stx_uuid = uuid.UUID(parts[1])
                except (ValueError, TypeError):
                    raise PersistenceError(
                        f"Invalid UUID in bank_item_id: {rcd.bank_item_id!r}"
                    )

                from ledger.models.data_import import StagedTransactionModel
                try:
                    stx = StagedTransactionModel.objects.select_related(
                        "import_job__bank_account_model"
                    ).get(uuid=stx_uuid)
                except ObjectDoesNotExist:
                    raise PersistenceError(f"StagedTransaction '{stx_uuid}' not found.")

                import_job_ba = stx.import_job.bank_account_model
                if import_job_ba.entity_model_id != entity.pk:
                    raise PersistenceError(
                        f"StagedTransaction '{stx_uuid}' belongs to different entity."
                    )

                ba = _resolve_bank_account_for_entity(
                    bank_account_id=rcd.bank_account_id,
                    entity=entity,
                )
                if ba.pk != import_job_ba.pk:
                    raise PersistenceError(
                        f"BankAccount {rcd.bank_account_id} does not match "
                        f"staged transaction bank account {import_job_ba.uuid}."
                    )

                account = None
                if rcd.status == "CLASSIFIED":
                    if not rcd.account_code:
                        raise PersistenceError(
                            f"Residual bank classification '{rcd.id}' has status CLASSIFIED but no account_code."
                        )
                    account = _resolve_account_for_entity(
                        account_code=rcd.account_code,
                        entity=entity,
                    )
                    if not account.active:
                        raise PersistenceError(
                            f"Residual bank classification '{rcd.id}' references inactive account '{account.code}'."
                        )
                    if entity.default_coa_id is not None and getattr(account, "coa_model_id", None) != entity.default_coa_id:
                        raise PersistenceError(
                            f"Account '{account.code}' does not belong to entity default CoA."
                        )

                supersedes_dec = None
                if rcd.supersedes_decision_id:
                    try:
                        supersedes_dec = BookkeepingResidualBankClassificationDecision.objects.get(
                            id=rcd.supersedes_decision_id,
                            entity=entity,
                        )
                    except ObjectDoesNotExist:
                        raise PersistenceError(
                            f"Superseded residual classification '{rcd.supersedes_decision_id}' "
                            f"not found for entity '{entity.slug}'."
                        )
                    if getattr(supersedes_dec, "staged_transaction_id", None) != stx.pk:
                        raise PersistenceError(
                            f"Residual classification '{rcd.id}' targets a different "
                            f"staged transaction than superseded decision '{supersedes_dec.id}'."
                        )
                    if BookkeepingResidualBankPosting.objects.filter(classification=supersedes_dec).exists():
                        raise PersistenceError(
                            f"Cannot supersede decision '{supersedes_dec.id}': "
                            "posted decisions are permanently sealed."
                        )

                BookkeepingResidualBankClassificationDecision.objects.create(
                    id=rcd.id,
                    entity=entity,
                    staged_transaction=stx,
                    bank_account=ba,
                    status=str(rcd.status),
                    account_code=account.code if account else None,
                    account=account,
                    original_amount_units=rcd.original_amount_units,
                    residual_amount_units=rcd.residual_amount_units,
                    direction=str(rcd.direction),
                    currency=rcd.currency.upper(),
                    confidence=rcd.confidence,
                    rationale=rcd.rationale,
                    evidence_refs=list(rcd.evidence_refs or ()),
                    hold_reason=rcd.hold_reason,
                    required_evidence=list(rcd.required_evidence or ()),
                    schema_version=rcd.schema_version,
                    dag_id=rcd.dag_id,
                    request_semantic_digest=rcd.request_semantic_digest,
                    session_id=rcd.session_id,
                    state_revision_at_decision=rcd.state_revision_at_decision,
                    persistence_revision_at_decision=rcd.persistence_revision_at_decision,
                    ase_node_id=rcd.ase_node_id,
                    terminal_property=rcd.terminal_property,
                    supersedes=supersedes_dec,
                    created_at=rcd.created_at,
                )

                if str(rcd.status) == BookkeepingResidualBankClassificationDecision.StatusChoices.HOLD:
                    from bookkeeping_state.notifications.service import notify_residual_hold
                    dec_id = str(rcd.id)
                    transaction.on_commit(lambda d_id=dec_id: notify_residual_hold(d_id))

            # 11.7. Insert Residual Bank Classification Invalidations
            for rci in write_set.residual_bank_classification_invalidations:
                try:
                    dec_target = BookkeepingResidualBankClassificationDecision.objects.get(
                        id=rci.classification_id
                    )
                except ObjectDoesNotExist:
                    raise PersistenceError(
                        f"Residual bank classification '{rci.classification_id}' not found to invalidate."
                    )
                if getattr(dec_target, "entity_id", None) != entity.pk:
                    raise PersistenceError(
                        f"Residual bank classification '{rci.classification_id}' belongs to different entity."
                    )
                if BookkeepingResidualBankPosting.objects.filter(classification=dec_target).exists():
                    raise PersistenceError(
                        f"Cannot invalidate decision '{dec_target.id}': "
                        "posted decisions are permanently sealed."
                    )

                BookkeepingResidualBankClassificationInvalidation.objects.create(
                    id=rci.id,
                    classification=dec_target,
                    reason=rci.reason,
                    session_id=rci.session_id,
                    state_revision_at_invalidation=rci.state_revision_at_invalidation,
                    persistence_revision_at_invalidation=rci.persistence_revision_at_invalidation,
                    created_at=rci.created_at,
                )

            # 11.8. Execute Residual Bank Postings & Direct Reconciliation Closure
            for rbp_exec in write_set.residual_bank_postings_to_execute:
                # 3. target classification decision select_for_update
                try:
                    dec_row = BookkeepingResidualBankClassificationDecision.objects.select_for_update().get(id=rbp_exec.decision_id)
                except ObjectDoesNotExist:
                    raise PersistenceError(
                        f"ResidualBankClassificationDecision '{rbp_exec.decision_id}' not found."
                    )

                # 4. staged transaction select_for_update
                from django.db.models import QuerySet
                from ledger.models.data_import import StagedTransactionModel

                try:
                    stx = QuerySet(StagedTransactionModel).select_for_update().get(pk=dec_row.staged_transaction_id)
                except ObjectDoesNotExist:
                    raise PersistenceError(
                        f"StagedTransaction '{dec_row.staged_transaction_id}' not found for decision '{dec_row.id}'."
                    )

                # 5. DB re-checks under lock
                if getattr(dec_row, "entity_id", None) != entity.pk:
                    raise PersistenceError(
                        f"Residual classification '{dec_row.id}' belongs to different entity."
                    )

                if dec_row.status != "CLASSIFIED":
                    raise PersistenceError(
                        f"Residual classification '{dec_row.id}' status is '{dec_row.status}', expected CLASSIFIED."
                    )

                if BookkeepingResidualBankPosting.objects.filter(classification=dec_row).exists():
                    raise PersistenceError(
                        f"DECISION_ALREADY_POSTED: Decision '{dec_row.id}' already has a durable posting."
                    )

                if BookkeepingResidualBankClassificationInvalidation.objects.filter(classification=dec_row).exists():
                    raise PersistenceError(
                        f"Cannot post decision '{dec_row.id}': decision has been invalidated."
                    )

                if BookkeepingResidualBankClassificationDecision.objects.filter(supersedes=dec_row).exists():
                    raise PersistenceError(
                        f"Cannot post decision '{dec_row.id}': decision has been superseded."
                    )

                account = dec_row.account
                if account is None:
                    raise PersistenceError(
                        f"Residual classification '{dec_row.id}' has no associated accounting AccountModel."
                    )
                if not account.active:
                    raise PersistenceError(
                        f"Account '{account.code}' for decision '{dec_row.id}' is inactive."
                    )
                if entity.default_coa_id is not None and getattr(account, "coa_model_id", None) != entity.default_coa_id:
                    raise PersistenceError(
                        f"Account '{account.code}' does not belong to entity default CoA."
                    )

                # Bank account & staged transaction matching
                import_job = stx.import_job
                if import_job is None or import_job.bank_account_model is None:
                    raise PersistenceError(
                        f"StagedTransaction '{stx.pk}' has no associated bank account."
                    )
                ba = import_job.bank_account_model
                if dec_row.bank_account_id != ba.pk:
                    raise PersistenceError(
                        f"Decision bank_account '{dec_row.bank_account_id}' does not match "
                        f"staged transaction bank account '{ba.pk}'."
                    )
                if ba.account_model is None:
                    raise PersistenceError(
                        f"BankAccountModel '{ba.uuid}' has no linked account_model."
                    )

                # Re-check persistent residual consumption
                from ledger.models.bookkeeping import (
                    BookkeepingPaymentApplication,
                    BookkeepingReconciliationBankAllocation,
                )

                if BookkeepingPaymentApplication.objects.filter(staged_transaction=stx).exists():
                    raise PersistenceError(
                        f"Bank movement '{stx.pk}' was consumed by an executed Stage 1 payment application."
                    )

                active_allocs = BookkeepingReconciliationBankAllocation.objects.filter(
                    staged_transaction=stx,
                    reconciliation__invalidations__isnull=True,
                )
                allocated_units = sum(a.amount_units for a in active_allocs)

                from bookkeeping_state.persistence.bank_normalization import normalize_staged_movement
                orig_amount, _, _ = normalize_staged_movement(stx, entity=entity)
                orig_units = major_units_to_solver_units(orig_amount)
                remaining_units = orig_units - allocated_units

                if remaining_units != dec_row.residual_amount_units:
                    raise PersistenceError(
                        f"Residual amount units mismatch under lock: expected {dec_row.residual_amount_units}, remaining {remaining_units}."
                    )

                # Closed accounting period check
                if entity.last_closing_date and entity.last_closing_date >= stx.date_posted:
                    raise PersistenceError(
                        f"CLOSED_ACCOUNTING_PERIOD: Transaction date {stx.date_posted} falls within "
                        f"closed accounting period (last closing date: {entity.last_closing_date})."
                    )

                # 6. Resolve deterministic bank ledger
                from bookkeeping_state.persistence.bank_ledger import get_or_create_bank_ledger
                bank_ledger = get_or_create_bank_ledger(bank_account=ba)

                if bank_ledger.is_locked():
                    raise PersistenceError(
                        f"BANK_LEDGER_LOCKED: Bank ledger '{bank_ledger.ledger_xid}' is locked."
                    )
                if not bank_ledger.is_posted():
                    raise PersistenceError(
                        f"Bank ledger '{bank_ledger.ledger_xid}' is not posted."
                    )

                # 7. Construct and commit Journal Entry
                amount_dec = solver_units_to_decimal(dec_row.residual_amount_units)
                dir_str = str(dec_row.direction).strip().upper()

                from ledger.models.transactions import TransactionModel

                if dir_str in ("OUTFLOW", "BANK_OUTFLOW"):
                    debit_acc = account
                    credit_acc = ba.account_model
                    bank_leg_type = TransactionModel.CREDIT
                    contra_leg_type = TransactionModel.DEBIT
                elif dir_str in ("INFLOW", "BANK_INFLOW"):
                    debit_acc = ba.account_model
                    credit_acc = account
                    bank_leg_type = TransactionModel.DEBIT
                    contra_leg_type = TransactionModel.CREDIT
                else:
                    raise PersistenceError(f"Unsupported direction '{dec_row.direction}'.")

                clean_desc = (stx.name or stx.memo or f"Residual bank posting {stx.uuid}").strip()
                je_desc = clean_desc[:70]
                tx_desc = clean_desc[:100]

                je_txs = [
                    {
                        "account": debit_acc,
                        "amount": amount_dec,
                        "tx_type": TransactionModel.DEBIT,
                        "description": tx_desc,
                    },
                    {
                        "account": credit_acc,
                        "amount": amount_dec,
                        "tx_type": TransactionModel.CREDIT,
                        "description": tx_desc,
                    },
                ]

                from ledger.io.io_core import IOValidationError
                from ledger.models.journal_entry import (
                    JournalEntryModel,
                    JournalEntryValidationError,
                )

                try:
                    je_raw, txs_raw = bank_ledger.commit_txs(
                        je_timestamp=stx.date_posted,
                        je_txs=je_txs,
                        je_posted=True,
                        je_desc=je_desc,
                        je_origin="residual_bank_classification",
                    )
                except (IOValidationError, JournalEntryValidationError) as exc:
                    msg = str(exc)
                    if "closed period" in msg.lower():
                        raise PersistenceError(f"CLOSED_ACCOUNTING_PERIOD: {msg}") from exc
                    if "locked" in msg.lower():
                        raise PersistenceError(f"BANK_LEDGER_LOCKED: {msg}") from exc
                    raise PersistenceError(f"ACCOUNTING_KERNEL_FAILURE: {msg}") from exc
                except Exception as exc:
                    raise PersistenceError(f"ACCOUNTING_KERNEL_FAILURE: {exc}") from exc

                je = cast(JournalEntryModel, je_raw)
                txs = cast(list[TransactionModel], txs_raw)

                # Verify transaction legs
                if len(txs) != 2:
                    raise PersistenceError(
                        f"Accounting kernel returned {len(txs)} transactions, expected exactly 2."
                    )
                if txs[0].uuid == txs[1].uuid:
                    raise PersistenceError("Accounting kernel returned duplicate transaction legs.")

                bank_legs = [
                    t for t in txs
                    if getattr(t, "account_id") == ba.account_model.pk
                    and t.tx_type == bank_leg_type
                    and Decimal(str(t.amount)) == amount_dec
                ]
                contra_legs = [
                    t for t in txs
                    if getattr(t, "account_id") == account.pk
                    and t.tx_type == contra_leg_type
                    and Decimal(str(t.amount)) == amount_dec
                ]

                if len(bank_legs) != 1:
                    raise PersistenceError(
                        f"Expected exactly 1 bank cash leg, found {len(bank_legs)}."
                    )
                if len(contra_legs) != 1:
                    raise PersistenceError(
                        f"Expected exactly 1 contra leg, found {len(contra_legs)}."
                    )

                bank_tx = bank_legs[0]
                contra_tx = contra_legs[0]

                if (
                    getattr(bank_tx, "journal_entry_id") != je.uuid
                    or getattr(contra_tx, "journal_entry_id") != je.uuid
                ):
                    raise PersistenceError("Transaction legs do not belong to created JournalEntry.")

                if not je.is_posted():
                    raise PersistenceError("Created JournalEntry is not posted.")
                if not je.is_locked():
                    raise PersistenceError("Created JournalEntry is not locked.")

                # 8. Direct deterministic reconciliation
                rec_id = f"rec_{uuid.uuid4().hex[:16]}"
                rationale = (
                    f"Direct deterministic closure from residual bank classification posting "
                    f"for decision {dec_row.id}"
                )
                evidence_refs: list[str] = []

                from django.utils import timezone
                now_ts = timezone.now()

                recon = _persist_reconciliation_record(
                    entity=entity,
                    reconciliation_id=rec_id,
                    evidence_refs=evidence_refs,
                    source_hypothesis_id=None,
                    source_hypothesis_state_revision=None,
                    source_hypothesis_utility=None,
                    source_hypothesis_generated_at=None,
                    source_hypothesis_admissibility=None,
                    source_hypothesis_allocation_support=None,
                    semantic_rationale=rationale,
                    session_id=rbp_exec.session_id,
                    state_revision_at_creation=rbp_exec.state_revision_at_creation,
                    created_at=now_ts,
                    bank_allocations=[(None, stx, dec_row.residual_amount_units)],
                    book_allocations=[(bank_tx, dec_row.residual_amount_units)],
                )

                # 9. Posting provenance
                new_rev = expected_revision + 1
                posting_id = f"rbp_{uuid.uuid4().hex[:16]}"

                BookkeepingResidualBankPosting.objects.create(
                    id=posting_id,
                    entity=entity,
                    classification=dec_row,
                    staged_transaction=stx,
                    journal_entry=je,
                    bank_cash_transaction=bank_tx,
                    contra_transaction=contra_tx,
                    reconciliation=recon,
                    persistence_revision=new_rev,
                    posted_at=now_ts,
                )

            # 12. Monotonically increment persistence revision exactly once
            new_rev = expected_revision + 1
            rev_obj.revision = new_rev
            rev_obj.save(update_fields=["revision", "updated_at"])

            return PersistenceCommitResult(
                previous_revision=expected_revision,
                new_revision=new_rev,
                write_set=write_set,
            )

    except PersistenceError:
        raise
    except IntegrityError as exc:
        if _is_unique_violation(exc):
            raise PersistenceDuplicateError(
                f"Database integrity conflict during commit: {exc}"
            ) from exc
        raise PersistenceError(f"Database integrity error during commit: {exc}") from exc
    except (ValidationError, ObjectDoesNotExist) as exc:
        raise PersistenceError(f"Validation or reference failure: {exc}") from exc
    except Exception as exc:
        raise PersistenceError(f"Unexpected persistence error during commit: {exc}") from exc
