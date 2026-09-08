#!/usr/bin/env python3
"""
Import Plaid transactions from a local JSON file, match against existing TransactionModel rows
and/or create necessary Counterparty / Customer / Vendor / Account / JournalEntry / TransactionModel
records. Runs as a standalone Django script (sets DJANGO_SETTINGS_MODULE and calls django.setup()).

Usage:
    python3 ledger-be_py/ledger/services/import_plaid_transactions.py \
        --file /absolute/path/to/transactions.json \
        --entity 1fb9f680-971f-467b-8c01-858181a279fe

Notes:
- This script is transactional: any unhandled exception will roll back the entire run.
- Defaults are used for matching and account-role mapping (see inline docstrings).
"""

import os
import sys
import json
import uuid
import random
from decimal import Decimal, ROUND_HALF_UP
from datetime import datetime, time
from typing import Dict, Any, List, Optional
import argparse
import logging
from django.db import transaction
from django.utils import timezone
from django.core.exceptions import ObjectDoesNotExist

# Models
from ledger.models import (
    EntityModel,
    ChartOfAccountModel,
    LedgerModel,
    AccountModel,
    JournalEntryModel,
    TransactionModel,
    PlaidItem,
    PlaidTransaction,
    Counterparty,
    TransactionAuditLog,
    CustomerModel,
    VendorModel,
)
from ledger.models import ledger as ledger_models  # sometimes used to reference constants
from ledger.io.roles import (
    ASSET_CA_CASH,
    EXPENSE_OPERATIONAL,
    INCOME_OPERATIONAL,
)
from ledger.models.accounts import DEBIT, CREDIT
from pathlib import Path




logger = logging.getLogger("import_plaid_transactions")
handler = logging.StreamHandler(sys.stdout)
handler.setFormatter(logging.Formatter("%(levelname)s: %(message)s"))
logger.addHandler(handler)
logger.setLevel(logging.INFO)


# -------------------------
# Helpers / Utilities
# -------------------------
def load_plaid_transactions() -> List[Dict[str, Any]]:
    """Load JSON and return a flat list of Plaid transaction dicts.
    Accepts either {"added": [...]} or a top-level list."""
    path = Path(__file__).resolve().parents[2] / "ledger" / "transactions.json"
    with open(path, "r", encoding="utf-8") as f:
        payload = json.load(f)
    if isinstance(payload, dict):
        # Plaid sync shape: look for 'added' key otherwise try to flatten all lists inside
        if "added" in payload:
            return payload["added"]
        # fallback: collect all lists at top-level
        for v in payload.values():
            if isinstance(v, list):
                return v
        raise ValueError("Unsupported JSON shape: top-level dict without 'added' or list values")
    if isinstance(payload, list):
        return payload
    raise ValueError("Unsupported JSON content")


def quantize_amount(v: Any) -> Decimal:
    """Return Decimal quantized to 2 decimal places."""
    d = Decimal(str(v or "0")).quantize(Decimal("0.01"), rounding=ROUND_HALF_UP)
    return d


def choose_script_user_for_entity(entity: EntityModel):
    """Choose a user to attribute creations to: prefer entity admin, fallback to first superuser, then any user."""
    try:
        admin = getattr(entity, "admin", None)
        if admin:
            return admin
    except Exception:
        pass

    from django.contrib.auth import get_user_model

    User = get_user_model()
    su = User.objects.filter(is_superuser=True).first()
    if su:
        return su
    user = User.objects.first()
    if not user:
        raise RuntimeError("No Django User available to attribute created objects to.")
    return user


def get_or_create_plaid_item_for_user(user, entity):
    """Find a PlaidItem owned by the user, otherwise create a simple placeholder PlaidItem."""
    try:
        pi = PlaidItem.objects.filter(user=user).first()
        if pi:
            return pi
        # Create a minimal PlaidItem
        item = PlaidItem.objects.create(
            item_id=f"import-{entity.slug if hasattr(entity, 'slug') else str(entity.uuid)[:8]}-{uuid.uuid4().hex[:6]}",
            user=user,
            metadata={"imported_by": "import_plaid_transactions"},
        )
        return item
    except Exception as e:
        raise


def find_matching_transaction(entity: EntityModel, tx_date_str: str, amount: Decimal, merchant_name: Optional[str], name: Optional[str]):
    """Heuristic matching: date, amount (2-decimals), and merchant/name substring.
    Returns TransactionModel or None.
    """
    if not tx_date_str:
        return None
    try:
        tx_date = datetime.fromisoformat(tx_date_str).date()
    except Exception:
        try:
            tx_date = datetime.strptime(tx_date_str, "%Y-%m-%d").date()
        except Exception:
            return None

    # TransactionModel amount is always positive; tx_type indicates debit/credit.
    # We'll search for either DEBIT or CREDIT where amount equals abs(amount).
    amt = abs(amount)
    qs = TransactionModel.objects.for_entity(entity.uuid)
    matches = qs.filter(
        amount=amt,
        journal_entry__timestamp__date=tx_date,
    )
    if merchant_name:
        matches = matches.filter(description__icontains=merchant_name)
    elif name:
        matches = matches.filter(description__icontains=name)
    # Return first match or None
    return matches.select_related("account", "journal_entry").first()


def ensure_counterparty(counterparty_data: Dict[str, Any]) -> Counterparty:
    """Return existing Counterparty by entity_id or create one."""
    if not counterparty_data:
        raise ValueError("No counterparty_data provided")
    eid = counterparty_data.get("entity_id") or counterparty_data.get("merchant_entity_id") or counterparty_data.get("merchant_id")
    name = counterparty_data.get("name") or counterparty_data.get("merchant_name") or "Unknown"
    ctype = counterparty_data.get("type") or "merchant"
    logo = counterparty_data.get("logo_url")
    website = counterparty_data.get("website")
    confidence = counterparty_data.get("confidence_level") or "LOW"

    if not eid:
        # fallback to name-based synthetic id
        eid = f"import-{uuid.uuid5(uuid.NAMESPACE_DNS, name) .hex}"

    cp, created = Counterparty.objects.get_or_create(
        entity_id=eid,
        defaults={
            "name": name[:255],
            "type": ctype,
            "logo_url": logo,
            "website": website,
            "confidence_level": confidence if confidence in dict(Counterparty.ConfidenceLevelChoices.choices) else Counterparty.ConfidenceLevelChoices.LOW,
        }
    )
    if not created:
        # Optionally update fields if changed
        updated = False
        if cp.name != name and name:
            cp.name = name[:255]
            updated = True
        if logo and cp.logo_url != logo:
            cp.logo_url = logo
            updated = True
        if website and cp.website != website:
            cp.website = website
            updated = True
        if updated:
            cp.save(update_fields=["name", "logo_url", "website", "updated_at"] if hasattr(cp, "updated_at") else ["name", "logo_url", "website"])
    return cp


def ensure_party_models(entity: EntityModel, cp: Counterparty, amount: Decimal, user):
    """Create CustomerModel or VendorModel as appropriate and return it.
    - inflow (amount < 0 on Plaid) -> CustomerModel (money received)
    - outflow (amount > 0 on Plaid) -> VendorModel (money spent)"""
    # Determine sign: note Plaid convention: positive -> outflow (spent), negative -> inflow (received)
    # But caller should have passed raw amount as Decimal (positive or negative)
    obj = None
    if amount < 0:
        # inflow -> Customer
        obj, created = CustomerModel.objects.get_or_create(
            customer_name=cp.name,
            entity_model=entity,
            defaults={"description": f"Created from Plaid import ({cp.entity_id})", "active": True, "additional_info": {"counterparty_entity_id": cp.entity_id}},
        )
    else:
        # outflow -> Vendor
        obj, created = VendorModel.objects.get_or_create(
            vendor_name=cp.name,
            entity_model=entity,
            defaults={"description": f"Created from Plaid import ({cp.entity_id})", "active": True, "additional_info": {"counterparty_entity_id": cp.entity_id}},
        )
    return obj


def random_account_code(prefix: str = "9", length: int = 6) -> str:
    digits = "".join(random.choices("0123456789", k=length))
    return f"{prefix}{digits}"


def ensure_account_for_role(coa: ChartOfAccountModel, role: str, name: str, balance_type: str) -> AccountModel:
    """Ensure there's at least one AccountModel in this CoA with the specified role; create one if not exists.
    Uses ChartOfAccountModel.create_account(...) to correctly insert account into CoA."""
    # try find an existing, transactable account with this role
    existing = coa.get_coa_accounts(active_only=False).filter(role__exact=role).first()
    if existing:
        return existing
    # generate a code that likely matches prefix constraints
    # heuristic prefix: assets:1 income:4 expense:6
    if balance_type.upper() == 'DEBIT':
        prefix = "6" if role in [EXPENSE_OPERATIONAL] else "1"
    else:
        prefix = "4"
    code = random_account_code(prefix=prefix, length=5)
    # call create_account on CoA; it expects (code, role, name, balance_type, active)
    acc = coa.create_account(code=code, role=role, name=name[:100], balance_type=balance_type, active=True)
    return acc


def create_journal_and_transactions(ledger: LedgerModel, timestamp: datetime, description: str, debit_account: AccountModel, credit_account: AccountModel, amount: Decimal) -> JournalEntryModel:
    """Create a JournalEntry and two TransactionModel rows (debit + credit)."""
    je = JournalEntryModel.objects.create(ledger=ledger, timestamp=timestamp, description=description)
    # Debit
    TransactionModel.objects.create(journal_entry=je, account=debit_account, amount=amount, tx_type=TransactionModel.DEBIT, description=description)
    # Credit
    TransactionModel.objects.create(journal_entry=je, account=credit_account, amount=amount, tx_type=TransactionModel.CREDIT, description=description)
    return je


# -------------------------
# Main processing function
# -------------------------
def process_transactions_file(entity_uuid: str, tx_list: list):

    logger.info(f"Loaded {len(tx_list)} transactions")

    try:
        entity = EntityModel.objects.get(uuid=entity_uuid)
    except EntityModel.DoesNotExist:
        raise RuntimeError(f"Entity with uuid {entity_uuid} not found")

    user = choose_script_user_for_entity(entity)
    plaid_item = get_or_create_plaid_item_for_user(user, entity)

    # pick or create a ChartOfAccountModel for the entity
    coa = ChartOfAccountModel.objects.filter(entity=entity).first()
    if not coa:
        coa = ChartOfAccountModel.objects.create(entity=entity, name=f"Default CoA for {entity.slug if hasattr(entity, 'slug') else entity.uuid}")
        # configure() is called by post_save signal; ensure it's saved & configured
        logger.info("Created default ChartOfAccountModel for entity")

    # pick or create a LedgerModel for the entity
    ledger = LedgerModel.objects.filter(entity=entity).first()
    if not ledger:
        ledger = LedgerModel.objects.create(entity=entity, name=f"Default Ledger for {entity.slug if hasattr(entity, 'slug') else entity.uuid}")
        logger.info("Created default LedgerModel for entity")

    created_counts = {"plaid_tx": 0, "counterparty": 0, "customer_vendor": 0, "accounts": 0, "journal_entries": 0, "matched_existing": 0}

    with transaction.atomic():
        for raw in tx_list:
            # normalize and guard
            txid = raw.get("transaction_id") or raw.get("id") or raw.get("transactionId")
            if not txid:
                logger.warning("Skipping payload without transaction_id")
                continue

            amount = quantize_amount(raw.get("amount", 0))
            date_str = raw.get("date")
            merchant_name = raw.get("merchant_name") or raw.get("merchant") or raw.get("merchantName")
            name = raw.get("name") or merchant_name or "Plaid TX"

            # try match existing TransactionModel
            existing_tx = find_matching_transaction(entity, date_str, amount, merchant_name, name)
            if existing_tx:
                # create a PlaidTransaction record and link counterparties
                pt, created = PlaidTransaction.objects.get_or_create(
                    transaction_id=txid,
                    plaid_item=plaid_item,
                    defaults={
                        "account_id": raw.get("account_id") or "",
                        "amount": amount,
                        "iso_currency_code": raw.get("iso_currency_code") or "USD",
                        "date": date_str,
                        "name": name[:255],
                        "merchant_name": merchant_name[:255] if merchant_name else None,
                        "payment_channel": raw.get("payment_channel") or PlaidTransaction.PaymentChannelChoices.OTHER,
                        "pending": bool(raw.get("pending", False)),
                        "transaction_type": raw.get("transaction_type") or PlaidTransaction.TransactionTypeChoices.UNRESOLVED,
                        "personal_finance_category": raw.get("personal_finance_category") or {},
                        "location": raw.get("location") or {},
                        "payment_meta": raw.get("payment_meta") or {},
                    },
                )
                # attach counterparties if present
                mdata = None
                if raw.get("counterparties"):
                    # take first
                    mdata = raw["counterparties"][0]
                elif raw.get("merchant_metadata"):
                    mdata = raw.get("merchant_metadata")
                elif raw.get("merchant_entity_id") or raw.get("merchant_id"):
                    mdata = {"merchant_entity_id": raw.get("merchant_entity_id") or raw.get("merchant_id"), "name": merchant_name}
                if mdata:
                    try:
                        cp = ensure_counterparty(mdata)
                        pt.counterparties.add(cp)
                        created_counts["counterparty"] += 1 if cp else 0
                    except Exception as e:
                        logger.warning(f"Counterparty create/attach failed for tx {txid}: {e}")
                # audit log
                TransactionAuditLog.objects.create(transaction=pt, user=user, action=TransactionAuditLog.ActionChoices.ADDED, new_state=pt._snapshot_state() if hasattr(pt, "_snapshot_state") else {}, plaid_sync_id=f"import-{uuid.uuid4().hex[:8]}")
                created_counts["plaid_tx"] += 1 if created else 0
                created_counts["matched_existing"] += 1
                logger.info(f"Matched existing TransactionModel {existing_tx.uuid} for Plaid TX {txid} — created PlaidTransaction {pt.uuid}")
                continue

            # Not matched -> create needed objects and ledger entries
            # ensure counterparty
            mdata = None
            if raw.get("counterparties"):
                mdata = raw["counterparties"][0]
            elif raw.get("merchant_metadata"):
                mdata = raw.get("merchant_metadata")
            elif raw.get("merchant_entity_id") or raw.get("merchant_id"):
                mdata = {"merchant_entity_id": raw.get("merchant_entity_id") or raw.get("merchant_id"), "name": merchant_name}
            cp = None
            if mdata:
                cp = ensure_counterparty(mdata)
                created_counts["counterparty"] += 1

            if cp:
                # create customer/vendor
                party = ensure_party_models(entity, cp, amount, user)
                created_counts["customer_vendor"] += 1 if party else 0

            # ensure accounts: bank account (asset), counter account (expense or income)
            try:
                bank_acc = ensure_account_for_role(coa=coa, role=ASSET_CA_CASH, name=f"Bank ({raw.get('account_id', '')[:8]})", balance_type=DEBIT)
                # choose expense vs income by sign: outflow (amount>0) -> expense (debit); inflow (amount<0) -> income (credit)
                if amount > 0:
                    other_role = EXPENSE_OPERATIONAL
                    other_balance = DEBIT
                    other_name = f"Expense - {merchant_name or name}"
                else:
                    other_role = INCOME_OPERATIONAL
                    other_balance = CREDIT
                    other_name = f"Income - {merchant_name or name}"
                other_acc = ensure_account_for_role(coa=coa, role=other_role, name=other_name[:100], balance_type=other_balance)
                created_counts["accounts"] += 2
            except Exception as e:
                logger.error(f"Failed to ensure accounts for tx {txid}: {e}")
                raise

            # create JournalEntry and Transactions (amount must be positive in TransactionModel)
            amt = abs(amount)
            try:
                # timestamp: use date midnight UTC-localized
                ts = None
                if date_str:
                    try:
                        d = datetime.fromisoformat(date_str)
                        ts = datetime.combine(d.date(), time(0, 0))
                        ts = timezone.make_aware(ts, timezone.get_current_timezone())
                    except Exception:
                        try:
                            d = datetime.strptime(date_str, "%Y-%m-%d")
                            ts = timezone.make_aware(datetime.combine(d.date(), time(0, 0)), timezone.get_current_timezone())
                        except Exception:
                            ts = timezone.now()
                else:
                    ts = timezone.now()

                # For outflow: debit expense, credit bank; For inflow: debit bank, credit income
                if amount > 0:
                    debit_acc = other_acc
                    credit_acc = bank_acc
                else:
                    debit_acc = bank_acc
                    credit_acc = other_acc

                journal = create_journal_and_transactions(ledger=ledger, timestamp=ts, description=name[:70], debit_account=debit_acc, credit_account=credit_acc, amount=amt)
                created_counts["journal_entries"] += 1
                logger.info(f"Created JE {je.uuid} with TXs for Plaid TX {txid}")
            except Exception as e:
                logger.error(f"Failed creating journal entry/transactions for tx {txid}: {e}")
                raise

            # create PlaidTransaction record
            pt = PlaidTransaction.objects.create(
                transaction_id=txid,
                plaid_item=plaid_item,
                account_id=raw.get("account_id") or "",
                amount=amount,
                iso_currency_code=raw.get("iso_currency_code") or "USD",
                date=date_str,
                name=name[:255],
                merchant_name=merchant_name[:255] if merchant_name else None,
                payment_channel=raw.get("payment_channel") or PlaidTransaction.PaymentChannelChoices.OTHER,
                pending=bool(raw.get("pending", False)),
                transaction_type=raw.get("transaction_type") or PlaidTransaction.TransactionTypeChoices.UNRESOLVED,
                personal_finance_category=raw.get("personal_finance_category") or {},
                location=raw.get("location") or {},
                payment_meta=raw.get("payment_meta") or {},
            )
            if cp:
                pt.counterparties.add(cp)
            TransactionAuditLog.objects.create(transaction=pt, user=user, action=TransactionAuditLog.ActionChoices.ADDED, new_state=pt._snapshot_state() if hasattr(pt, "_snapshot_state") else {}, plaid_sync_id=f"import-{uuid.uuid4().hex[:8]}")
            created_counts["plaid_tx"] += 1

        # end for tx_list
        logger.info("Finished processing transactions (within DB transaction). Commit will occur on success.")

    # end with atomic
    logger.info("Import completed successfully.")
    logger.info(f"Summary: {created_counts}")
    return created_counts


# -------------------------
# CLI Entrypoint
# -------------------------
def run_simulation():

    try:
        tx_list = load_plaid_transactions()
        process_transactions_file('1fb9f680-971f-467b-8c01-858181a279fe', tx_list)
    except Exception as e:
        logger.exception(f"Import failed: {e}")
        sys.exit(1)


if __name__ == "__main__":
    run_simulation()