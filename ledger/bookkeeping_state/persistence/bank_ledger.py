from __future__ import annotations

from typing import TYPE_CHECKING

from bookkeeping_state.persistence.repository import PersistenceError
from django.core.exceptions import ObjectDoesNotExist

if TYPE_CHECKING:
    from ledger.models.bank_account import BankAccountModel
    from ledger.models.ledger import LedgerModel


def get_or_create_bank_ledger(*, bank_account: BankAccountModel) -> LedgerModel:
    """
    Deterministically resolves or creates the authoritative posted Bank Journal (LedgerModel)
    for a BankAccountModel using the canonical ledger lifecycle API.

    Rules:
    - exactly one deterministic bank journal per BankAccountModel
    - entity must equal bank_account.entity_model
    - canonical identity: ledger_xid = f"bank-ledger-{bank_account.uuid}"
    - if absent, created using canonical entity.create_ledger(..., posted=True, commit=True)
    - if existing but unposted, transitioned using canonical ledger.post(commit=True, raise_exception=True)
    - if locked, fails closed by raising PersistenceError
    - if existing ledger entity mismatches bank_account.entity_model, fails closed by raising PersistenceError
    """
    from ledger.models.ledger import LedgerModel

    entity = bank_account.entity_model
    if entity is None:
        raise PersistenceError(
            f"BankAccountModel '{bank_account.uuid}' has no associated entity_model."
        )

    ledger_xid = f"bank-ledger-{bank_account.uuid}"

    try:
        ledger = LedgerModel.objects.get(entity=entity, ledger_xid=ledger_xid)
    except ObjectDoesNotExist:
        # Check if an existing ledger with this ledger_xid exists under a different entity
        conflict = LedgerModel.objects.filter(ledger_xid=ledger_xid).exclude(entity=entity).first()
        if conflict is not None:
            conflict_entity_id = conflict.entity.pk if conflict.entity else getattr(conflict, "entity_id", None)
            raise PersistenceError(
                f"Ledger with xid '{ledger_xid}' already exists for different entity {conflict_entity_id}."
            )

        name = f"Bank Journal ({bank_account.name or bank_account.account_number or bank_account.uuid})"
        ledger = entity.create_ledger(
            name=name,
            ledger_xid=ledger_xid,
            posted=True,
            commit=True,
        )

    # Fail-closed validation invariants
    ledger_entity_id = ledger.entity.pk if ledger.entity else getattr(ledger, "entity_id", None)
    if ledger_entity_id != entity.pk:
        raise PersistenceError(
            f"Ledger '{ledger_xid}' belongs to entity {ledger_entity_id}, expected {entity.pk}."
        )

    if ledger.is_locked():
        raise PersistenceError(
            f"Bank ledger '{ledger_xid}' is locked and cannot receive journal entries."
        )

    if not ledger.is_posted():
        ledger.post(commit=True, raise_exception=True)

    return ledger
