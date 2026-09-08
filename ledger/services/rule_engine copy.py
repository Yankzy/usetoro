
import re
import unicodedata
from decimal import Decimal, InvalidOperation
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


def _role_heuristic_from_category(category_name: str):
    if not category_name:
        return EXPENSE_OPERATIONAL
    cat = normalize_text(category_name)
    income_kws = ("income", "payroll", "salary", "interest", "dividend", "refund")
    if any(k in cat for k in income_kws):
        return INCOME_OPERATIONAL
    return EXPENSE_OPERATIONAL


def _random_account_code(prefix: str = "9", length: int = 6) -> str:
    digits = "".join(random.choices("0123456789", k=length))
    return f"{prefix}{digits}"


def _prefix_for_role(role: str, balance_type: str) -> str:
    # heuristic prefixes: expenses ~6, income/credit ~4, default assets ~1
    if balance_type == DEBIT:
        return "6" if role == EXPENSE_OPERATIONAL else "1"
    return "4"


def create_rules_from_plaid_transactions(coa_slug: str = None):
    """
    Create RuleGroups from Plaid transactions file, creating target accounts
    under a ChartOfAccountModel.
    """
    print("🚀 Starting rule creation process...")
    path = Path(__file__).resolve().parents[2] / "ledger" / "transactions.json"
    with open(path, "r", encoding="utf-8") as f:
        transactions = json.load(f)

    coa = None
    if coa_slug:
        coa = ChartOfAccountModel.objects.filter(slug=coa_slug).first()
    else:
        coa = ChartOfAccountModel.objects.first()
    if not coa:
        raise RuntimeError("No ChartOfAccountModel available.")

    created_count = 0
    skipped_count = 0

    for tx_data in transactions["added"]:
        merchant_name = tx_data.get("merchant_name")
        pfc = tx_data.get("personal_finance_category", {})
        category_name = pfc.get("primary")

        if not merchant_name or not category_name:
            skipped_count += 1
            continue

        normalized_vendor = normalize_text(merchant_name)
        rule_name = f"Auto rule for {normalized_vendor}"

        # Try to find an existing group with this name (we'll add conditions to it).
        existing_group = RuleGroup.objects.filter(name=rule_name).first()

        formatted_account_name = category_name.replace("_", " ").title()

        # Infer role/balance_type
        amount = tx_data.get("amount")
        role = None
        balance_type = None
        if amount is not None:
            try:
                amt = Decimal(str(amount))
                if amt > 0:
                    role = EXPENSE_OPERATIONAL
                    balance_type = DEBIT
                else:
                    role = INCOME_OPERATIONAL
                    balance_type = CREDIT
            except (InvalidOperation, TypeError):
                pass

        if role is None:
            role = _role_heuristic_from_category(category_name)
        if balance_type is None:
            balance_type = DEBIT if role == EXPENSE_OPERATIONAL else CREDIT

        target_account = coa.get_non_root_coa_accounts_qs().filter(
            name__iexact=formatted_account_name, role=role
        ).first()

        if not target_account:
            prefix = _prefix_for_role(role, balance_type)
            code = _random_account_code(prefix=prefix, length=5)
            target_account = coa.create_account(
                code=code,
                role=role,
                name=formatted_account_name[:100],
                balance_type=balance_type,
                active=True,
            )

        with transaction.atomic():
            # Use existing group if present, otherwise create a new one
            if existing_group:
                group = existing_group
                # If the target account differs, update it to the inferred target_account
                if group.target_account_id != target_account.pk:
                    group.target_account = target_account
                    group.save(update_fields=["target_account"])
            else:
                group = RuleGroup.objects.create(
                    name=rule_name,
                    logic=RuleGroup.LogicChoices.AND,
                    priority=200,
                    target_account=target_account,
                )

            # Create conditions if they don't already exist on this group
            _, created_vendor_cond = RuleCondition.objects.get_or_create(
                group=group,
                field=RuleCondition.FieldChoices.VENDOR,
                operator=RuleCondition.OperatorChoices.EQUALS,
                value=normalized_vendor,
            )

            _, created_uuid_cond = RuleCondition.objects.get_or_create(
                group=group,
                field=RuleCondition.FieldChoices.UUID,
                operator=RuleCondition.OperatorChoices.EQUALS,
                value=tx_data["account_id"],
            )

        # Count creation/skip: increment created_count if we created a new group or added any new conditions
        if not existing_group or created_vendor_cond or created_uuid_cond:
            created_count += 1
        else:
            skipped_count += 1

    print(f"🎉 Rule creation finished. Created: {created_count}, Skipped: {skipped_count}.")


def categorize_transaction():
    """
    Applies the rule engine to a transaction, updates its account,
    and creates an audit log entry.
    """
    path = Path(__file__).resolve().parents[2] / "ledger" / "transactions.json"
    with open(path, "r", encoding="utf-8") as f:
        transactions = json.load(f)
    
    entity = EntityModel.objects.get(uuid='1fb9f680-971f-467b-8c01-858181a279fe')
    ledger = entity.ledgermodel_set.get(name='general_ledger')
    entity_unit = entity.entityunitmodel_set.get(name='general_unit')
    timestamp = get_localtime()
    je_kwargs = {
        "ledger": ledger,
        "description": 'je_description',
        "timestamp": timestamp,
    }
    if entity_unit:
        je_kwargs["entity_unit"] = entity_unit
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

        _account = AccountModel(
            uuid=account_id or uuid4(),
            name=acct_name,
            active=True,
            locked=False,
            balance_type=tx_type
        )

        transaction = TransactionModel(
            journal_entry=journal_entry,
            account=_account,
            amount=amount,
            tx_type=tx_type,
            description=description,
        )


        # THIS IS THE KEY CHANGE - Attach the vendor attribute just for the check
        transaction.vendor = normalize_text(acct_name)
        transaction.uuid = account_id 

        print(f"Attempting to categorize transaction: {transaction} ")

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



def manual():
    # 1. Get the target account you want to assign
    try:
        travel_account = AccountModel.objects.get(name="Travel Expenses")
    except AccountModel.DoesNotExist:
        # Handle case where account doesn't exist, maybe create it
        print("Travel Expenses account not found!")
        # For this example, let's assume it exists.

    # 2. Create the Rule Group with 'OR' logic
    # This rule has a higher priority (10 is lower/earlier than the default 100)
    large_rideshare_rule = RuleGroup.objects.create(
        name="Large Rideshare Transactions",
        logic=RuleGroup.LogicChoices.OR,  # Note: OR logic
        priority=10,
        target_account=travel_account
    )

    # 3. Create the conditions and link them to the group
    RuleCondition.objects.create(
        group=large_rideshare_rule,
        field=RuleCondition.FieldChoices.VENDOR,
        operator=RuleCondition.OperatorChoices.CONTAINS,
        value="uber"
    )

    RuleCondition.objects.create(
        group=large_rideshare_rule,
        field=RuleCondition.FieldChoices.VENDOR,
        operator=RuleCondition.OperatorChoices.CONTAINS,
        value="lyft"
    )

    # You could also add an AND condition for the amount if you wanted
    # e.g., A group for (Vendor contains 'uber' AND amount > 50)



# --- Example Usage ---

# Assuming you have an uncategorized transaction
# new_tx = TransactionModel.objects.get(id='some-transaction-uuid')

# # Run the categorization process
# categorize_transaction(new_tx)