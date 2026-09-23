from __future__ import annotations

from decimal import Decimal
from typing import TYPE_CHECKING, Any

from bookkeeping_state.domain.enums import Direction

if TYPE_CHECKING:
    from ledger.models.data_import import StagedTransactionModel
    from ledger.models.entity import EntityModel
    from ledger.models.plaid import PlaidTransaction


def normalize_plaid_movement(
    ptx: PlaidTransaction,
) -> tuple[Decimal, Direction, str]:
    """
    Normalize a PlaidTransaction into (abs_amount, direction, currency).

    Plaid sign convention:
    - Positive amount indicates money leaving the account (BANK_OUTFLOW).
    - Negative amount indicates money entering the account (BANK_INFLOW).
    """
    amount_val = Decimal(str(ptx.amount))
    if amount_val > 0:
        direction = Direction.BANK_OUTFLOW
        abs_amount = amount_val
    elif amount_val < 0:
        direction = Direction.BANK_INFLOW
        abs_amount = abs(amount_val)
    else:
        raise ValueError(f"PlaidTransaction '{ptx.uuid}' has zero amount.")

    currency = str(getattr(ptx, "iso_currency_code", "USD") or "USD").upper()
    return abs_amount, direction, currency


def normalize_staged_movement(
    stx: StagedTransactionModel,
    *,
    entity: EntityModel | None = None,
) -> tuple[Decimal, Direction, str]:
    """
    Normalize a StagedTransactionModel into (abs_amount, direction, currency).

    Standard bank statement convention:
    - Positive amount indicates money entering the account (BANK_INFLOW).
    - Negative amount indicates money leaving the account (BANK_OUTFLOW).
    """
    amount_val = Decimal(str(stx.amount))
    if amount_val > 0:
        direction = Direction.BANK_INFLOW
        abs_amount = amount_val
    elif amount_val < 0:
        direction = Direction.BANK_OUTFLOW
        abs_amount = abs(amount_val)
    else:
        raise ValueError(f"StagedTransactionModel '{stx.uuid}' has zero amount.")

    import_job = getattr(stx, "import_job", None)
    bank_account = getattr(import_job, "bank_account_model", None) if import_job else None
    ba_curr = getattr(bank_account, "currency", None) if bank_account else None
    entity_curr = getattr(entity, "currency", None) if entity else None
    currency = str(ba_curr or entity_curr or "USD").upper()
    return abs_amount, direction, currency
