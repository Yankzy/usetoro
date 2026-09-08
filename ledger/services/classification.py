import os
import django
from datetime import datetime, timedelta
from decimal import Decimal
import json
from typing import Dict, List, Any, Optional

# Mock Django setup for standalone script execution
# In a real Django project, this script would be part of an app
# or a management command, and this setup would not be necessary.
os.environ.setdefault('DJANGO_SETTINGS_MODULE', 'config.settings')
django.setup()

# Assuming your models are imported like this
from ledger.models import PlaidItem, PlaidTransaction, Counterparty # Adjust import path

# ==============================================================================
# 1. Configuration and Mock Data Setup (Updated)
# ==============================================================================

# ==============================================================================
# Refined Internal Ledger Mapping
# ==============================================================================
# This represents a mapping in your OWN system between a Plaid entity_id
# and a specific accounting account or internal record ID.
# In a real app, this would be stored in your database (e.g., a Vendor model
# that has a foreign key to a Counterparty or directly stores entity_id).
# For simplicity, we use a dictionary here.
INTERNAL_LEDGER_ACCOUNT_MAP = {
    "O5W5j4dN9OR3E6ypQmjdkWZZRoXEzVMz2ByWM": {
        "internal_name": "Walmart Inc.",
        "accounting_account": "Accounts Receivable",
        "internal_ref_id": "CUST-001" # e.g., an internal Customer ID
    },
    "M1jN3v1h8TjR5rZf3sM7yLz2pQyXoVwB9kC7q": {
        "internal_name": "Jane Doe",
        "accounting_account": "Payroll Expense",
        "internal_ref_id": "EMP-010" # e.g., an internal Employee ID
    },
    # Add other known mappings
    "AcmeEntityId": {
        "internal_name": "Acme Corp.",
        "accounting_account": "Office Supplies Expense",
        "internal_ref_id": "VEN-005" # e.g., an internal Vendor ID
    }
}

# ==============================================================================
# Mock Raw Plaid Data (as if received directly from Plaid API /transactions/sync or /enrich)
# ==============================================================================

def get_mock_raw_plaid_data() -> List[Dict[str, Any]]:
    """Generates mock raw Plaid transaction data."""
    today_str = datetime.now().date().isoformat()
    yesterday_str = (datetime.now() - timedelta(days=1)).date().isoformat()
    
    return [
        # Scenario 1: Deposit from Walmart
        {
            "transaction_id": "tx1_walmart_deposit",
            "account_id": "acc_walmart_123",
            "amount": -1000.00,  # Plaid amounts are negative for credits (money deposited, inflow)
            "iso_currency_code": "USD",
            "date": yesterday_str,
            "name": "Deposit from Walmart",
            "merchant_name": "Walmart",
            "payment_channel": "other",
            "pending": False,
            "transaction_type": "special",
            "personal_finance_category": "TRANSFER_INCOME",
            "merchant_metadata": {
                "merchant_id": "O5W5j4dN9OR3E6ypQmjdkWZZRoXEzVMz2ByWM",
                "name": "Walmart"
            },
            "location": {}
        },
        # Scenario 2: Withdrawal for Jane Doe payroll
        {
            "transaction_id": "tx2_jane_payroll",
            "account_id": "acc_payroll_456",
            "amount": 2500.00, # Plaid amounts are positive for debits (money spent, outflow)
            "iso_currency_code": "USD",
            "date": yesterday_str,
            "name": "Payroll to Jane Doe",
            "merchant_name": None, # Sometimes merchant_name is missing
            "payment_channel": "online",
            "pending": False,
            "transaction_type": "digital",
            "personal_finance_category": "GENERAL_MERCHANDISE", # Example, usually this would be salary
            "merchant_metadata": {
                "merchant_id": "M1jN3v1h8TjR5rZf3sM7yLz2pQyXoVwB9kC7q",
                "name": "Jane Doe"
            },
            "location": {}
        },
        # Scenario 3: New vendor transaction (needs manual review/mapping)
        {
            "transaction_id": "tx3_new_vendor",
            "account_id": "acc_expenses_789",
            "amount": 500.00,
            "iso_currency_code": "USD",
            "date": today_str,
            "name": "Payment to Unmatched Supplier",
            "merchant_name": "Unmatched Supplier Inc.",
            "payment_channel": "other",
            "pending": False,
            "transaction_type": "place",
            "personal_finance_category": "HOME_IMPROVEMENT_SUPPLY",
            "merchant_metadata": {
                "merchant_id": "NewVendorEntityId123", # This entity_id is not in INTERNAL_LEDGER_ACCOUNT_MAP
                "name": "Unmatched Supplier Inc."
            },
            "location": {}
        },
        # Scenario 4: Transaction with no clear entity_id (e.g., cash deposit/withdrawal, or very generic payment)
        {
            "transaction_id": "tx4_generic_cash",
            "account_id": "acc_cash_101",
            "amount": -200.00,
            "iso_currency_code": "USD",
            "date": today_str,
            "name": "ATM Deposit",
            "merchant_name": None,
            "payment_channel": "in store",
            "pending": False,
            "transaction_type": "place",
            "personal_finance_category": "BANK_FEES", # or CASH_ADVANCE
            "merchant_metadata": None, # No merchant_metadata available
            "location": {}
        },
        # Scenario 5: Another known vendor
        {
            "transaction_id": "tx5_acme_payment",
            "account_id": "acc_expenses_789",
            "amount": 120.50,
            "iso_currency_code": "USD",
            "date": today_str,
            "name": "ACME Payment",
            "merchant_name": "Acme Inc.",
            "payment_channel": "online",
            "pending": False,
            "transaction_type": "digital",
            "personal_finance_category": "HOME_IMPROVEMENT_SUPPLY",
            "merchant_metadata": {
                "merchant_id": "AcmeEntityId",
                "name": "Acme Inc."
            },
            "location": {}
        },
    ]

# ==============================================================================
# 2. Bank Reconciliation Logic
# ==============================================================================

# Function to determine transaction type based on Plaid's amount/direction
def determine_transaction_type(plaid_transaction_amount: Decimal) -> str:
    """
    Determines if a transaction is a credit (inflow) or debit (outflow)
    based on Plaid's transaction amount convention.
    Plaid amounts are negative for credits, positive for debits.
    """
    if plaid_transaction_amount > 0:
        return "outflow" # Money spent
    elif plaid_transaction_amount < 0:
        return "inflow"  # Money received
    return "neutral" # Zero amount transaction

def reconcile_transaction_with_ledger(
    plaid_transaction: PlaidTransaction,
    internal_ledger_map: Dict[str, Dict[str, str]]
):
    """
    Reconciles a single PlaidTransaction instance with the internal ledger.
    """
    print(f"\nProcessing Plaid Transaction: {plaid_transaction.name} (ID: {plaid_transaction.transaction_id})")
    
    transaction_amount = abs(plaid_transaction.amount) # Use absolute amount for ledger entry
    transaction_type = determine_transaction_type(plaid_transaction.amount) # inflow or outflow
    
    primary_counterparty = plaid_transaction.get_primary_counterparty()
    
    if primary_counterparty and primary_counterparty.entity_id:
        entity_id = primary_counterparty.entity_id
        
        if entity_id in internal_ledger_map:
            # A match was found using entity_id
            internal_mapping = internal_ledger_map[entity_id]
            internal_name = internal_mapping["internal_name"]
            accounting_account = internal_mapping["accounting_account"]
            internal_ref_id = internal_mapping["internal_ref_id"]
            
            print(f"  ✅ MATCH FOUND via entity_id '{entity_id}'.")
            print(f"     -> Internal Record: '{internal_name}' (Ref ID: {internal_ref_id})")
            
            # Create the counter credit or debit entry based on transaction_type
            print(f"  -> Creating journal entry for {transaction_type.upper()}:")
            if transaction_type == "inflow":
                # Money received (e.g., customer payment)
                print(f"     - Debit: Bank Account (${transaction_amount:.2f})")
                print(f"     - Credit: {accounting_account} ({internal_name}) (${transaction_amount:.2f})")
            elif transaction_type == "outflow":
                # Money spent (e.g., vendor payment, payroll)
                print(f"     - Debit: {accounting_account} ({internal_name}) (${transaction_amount:.2f})")
                print(f"     - Credit: Bank Account (${transaction_amount:.2f})")
            
            # In a real app, you'd update your models here, e.g.:
            # plaid_transaction.reconciliation_status = "matched"
            # plaid_transaction.internal_ref_id = internal_ref_id
            # plaid_transaction.save()
            return True # Successfully reconciled
        else:
            print(f"  ⚠️ NO INTERNAL LEDGER MAPPING found for entity_id: '{entity_id}' ({primary_counterparty.name}).")
            print(f"     -> Plaid Category: {plaid_transaction.personal_finance_category}")
    else:
        print(f"  ⚠️ NO Primary Counterparty or entity_id found for this transaction.")
        print(f"     -> Name: {plaid_transaction.name}, Merchant Name: {plaid_transaction.merchant_name}")
        print(f"     -> Plaid Category: {plaid_transaction.personal_finance_category}")

    print(f"     -> Transaction {plaid_transaction.transaction_id} needs manual review.")
    return False # Failed to reconcile automatically


def run_full_reconciliation_process(plaid_item_pk: int):
    """
    Simulates the full process: fetching raw Plaid data, creating/updating
    PlaidTransaction and Counterparty objects, then performing reconciliation.
    """
    print("\n--- Starting Full Reconciliation Process ---")

    # 1. Get raw Plaid data (simulated)
    raw_plaid_data = get_mock_raw_plaid_data()
    print(f"\nFetched {len(raw_plaid_data)} mock raw Plaid transactions.")

    # 2. Get/Create a PlaidItem instance (required by your manager)
    # In a real app, this PlaidItem would already exist from your Plaid Link flow.
    try:
        plaid_item = PlaidItem.objects.get(pk=plaid_item_pk)
    except PlaidItem.DoesNotExist:
        plaid_item = PlaidItem.objects.create(pk=plaid_item_pk, item_id="mock_item_id_123")
        print(f"Created mock PlaidItem (PK: {plaid_item_pk}, item_id: {plaid_item.item_id})")

    # 3. Use your manager to create/update transactions and counterparties
    # This will handle creating PlaidTransaction instances and linking to Counterparty instances.
    newly_created_transactions = PlaidTransaction.objects.bulk_create_from_plaid_data(raw_plaid_data, plaid_item)
    print(f"\n{len(newly_created_transactions)} new PlaidTransaction(s) created.")
    
    # After creation/update, fetch all relevant transactions for reconciliation
    # For a real scenario, you might filter by date or sync_id.
    transactions_to_reconcile = list(PlaidTransaction.objects.filter(plaid_item=plaid_item))
    print(f"Total {len(transactions_to_reconcile)} PlaidTransaction(s) to reconcile for PlaidItem {plaid_item_pk}.")

    # 4. Perform reconciliation for each transaction
    reconciled_count = 0
    for transaction in transactions_to_reconcile:
        if reconcile_transaction_with_ledger(transaction, INTERNAL_LEDGER_ACCOUNT_MAP):
            reconciled_count += 1
    
    print(f"\n--- Full Reconciliation Summary ---")
    print(f"Total Transactions Processed: {len(transactions_to_reconcile)}")
    print(f"Automatically Reconciled:   {reconciled_count}")
    print(f"Needs Manual Review:        {len(transactions_to_reconcile) - reconciled_count}")
    print("---------------------------------")


# ==============================================================================
# 3. Main Script Execution
# ==============================================================================

if __name__ == "__main__":
    # You might pass a PlaidItem PK or item_id as an argument
    # For this example, let's use a mock PK.
    mock_plaid_item_pk = 1 
    run_full_reconciliation_process(mock_plaid_item_pk)








# from typing import Optional, Dict
# from ledger.models.transactions import RuleGroup
# from ledger.models.plaid import PlaidTransaction

# class PlaidTxAdapter:
#     """Expose attributes expected by RuleCondition (description, vendor, memo, amount, etc.)."""
#     def __init__(self, tx: PlaidTransaction):
#         self.tx = tx

#     @property
#     def description(self):
#         return (self.tx.name or '')  # RuleCondition expects a string

#     @property
#     def vendor(self):
#         return (self.tx.merchant_name or self.tx.name or '').lower()

#     @property
#     def memo(self):
#         # map some payment_meta or personal_finance_category into memo if useful
#         pm = self.tx.payment_meta or {}
#         return (pm.get('reference_number') or pm.get('ppd_id') or '')

#     @property
#     def amount(self):
#         return float(self.tx.amount or 0.0)


# def classify_plaid_transaction(plaid_tx: PlaidTransaction) -> Optional[Dict]:
#     """
#     Run deterministic rules (RuleGroup) against a PlaidTransaction.
#     Returns dict: {'target_account_id': <id>, 'confidence': 1.0, 'source':'rule', 'explain': {...}}
#     or None if no rule matched.
#     """
#     # adapter = PlaidTxAdapter(plaid_tx)
#     detail = RuleGroup.match_any_verbose(plaid_tx)
#     if not detail:
#         return None
#     return {
#         "target_account_id": detail.get("target_account_id"),
#         "confidence": float(detail.get("priority", 1.0)),  # or use detail.get('confidence') if stored
#         "source": "rule",
#         "explain": detail
#     }