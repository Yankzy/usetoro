
import re
import unicodedata
from pathlib import Path
import json
import random
from django.db import transaction
from ledger.models import (
    AccountModel, 
    RuleGroup, 
    RuleCondition, 
    ChartOfAccountModel, 
    TransactionModel, 
    RuleAuditLog, 
    EntityModel,
    JournalEntryModel,
    BankAccountModel,
)
from ledger.io.roles import EXPENSE_OPERATIONAL, INCOME_OPERATIONAL
from ledger.models.accounts import DEBIT, CREDIT
from ledger.io.io_core import get_localtime
from uuid import uuid4


def normalize_text(text: str) -> str:
    """
    Normalize merchant/vendor/category names for consistency:
    - Lowercase
    - Strip whitespace
    - Remove punctuation/apostrophes
    - Replace multiple spaces with single space
    - Remove accents
    """
    if not text:
        return ""

    text = text.lower().strip()
    text = unicodedata.normalize("NFKD", text)
    text = "".join(ch for ch in text if not unicodedata.combining(ch))
    text = re.sub(r"[^\w\s]", "", text)  # remove punctuation/apostrophes
    text = re.sub(r"\s+", " ", text)
    return text.strip()



def categorize_transaction(transactions: TransactionModel, entity_uuid: str):
    """
    Applies the rule engine to a transaction, updates its account,
    and creates an audit log entry.
    """
    
    entity = EntityModel.objects.get(uuid=entity_uuid)
    ledger = entity.ledgermodel_set.get(name='general_ledger')
    entity_unit = entity.entityunitmodel_set.get(name='general_unit') or None
    timestamp = get_localtime()
    je_kwargs = {
        "ledger": ledger,
        "description": 'je_description',
        "timestamp": timestamp,
        "entity_unit": entity_unit,
    }
    journal_entry = JournalEntryModel(**je_kwargs)
    # journal_entry.full_clean()
    # journal_entry.save()
    

    for tx_input in transactions['added']:
        amount = tx_input["amount"]
        tx_type = 'debit' if amount < 0 else 'credit'
        cat = tx_input.get('category') or []
        description = " ".join(str(c).strip() for c in cat if c)
        account_id = str(tx_input['account_id'])

        # Safely pick a counterparty name, fall back to merchant/name or "Unknown"
        cps = tx_input.get("counterparties") or []
        first_counterparty = cps[0] if cps else None
        acct_name = (
            (first_counterparty.get("name") if first_counterparty else None)
            or tx_input.get("merchant_name")
            or tx_input.get("name")
            or "Unknown"
        )

        # _account = AccountModel(
        #     uuid=account_id or uuid4(),
        #     name=acct_name,
        #     active=True,
        #     locked=False,
        #     balance_type=tx_type
        # )

        transaction = TransactionModel(
            journal_entry=journal_entry,
            amount=amount,
            tx_type=tx_type,
            description=description,
        )


        # THIS IS THE KEY CHANGE - Attach the vendor attribute just for the check
        transaction.vendor = normalize_text(acct_name)
        transaction.uuid = account_id 

        # print(f"Attempting to categorize transaction: {transaction} ")

        # 1. Call the main matching function
        match_explanation = RuleGroup.match_any_verbose(transaction)
        print(f"✔️ Match found: Rule {match_explanation}")

    # 2. Check if a rule matched
    if match_explanation:
        # A rule was found! 🎉
        print(f"✔️ Match found: Rule '{match_explanation['group_name']}'")

        # Get the target account from the explanation result
        target_account_id = match_explanation['target_account_id']
        target_account = AccountModel.objects.get(pk=target_account_id)

        # 3. Update the transaction's account
        transaction.account = target_account
        transaction.save()

        # 4. Create a "success" audit log
        RuleAuditLog.objects.create(
            tx=transaction,
            rule_group_id=match_explanation['group_id'],
            matched=True,
            match_info=match_explanation
        )
        print(f"Transaction categorized as '{target_account.name}'.")

    else:
        # No matching rule was found 😟
        print("❌ No matching rule found.")

        # 5. Create a "failure" audit log for tracking coverage
        RuleAuditLog.objects.create(
            tx=transaction,
            rule_group=None,
            matched=False,
            match_info={'reason': 'No active rule group matched the transaction.'}
        )
        print("Transaction needs manual categorization.")


# --- Example Usage ---

# Assuming you have an uncategorized transaction
# new_tx = TransactionModel.objects.get(id='some-transaction-uuid')

# # Run the categorization process
# categorize_transaction(new_tx)