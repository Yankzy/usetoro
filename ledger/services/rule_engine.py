import re
import unicodedata
from decimal import Decimal, InvalidOperation
import random
from django.db import transaction

# Use the full roles module (richer role set)
from ledger.io import roles as roles
from ledger.models import (
    AccountModel,
    RuleGroup,
    RuleCondition,
    ChartOfAccountModel,
    TransactionModel,
)
from ledger.models.accounts import DEBIT, CREDIT


def normalize_text(text: str) -> str:
    if not text:
        return ""
    text = text.lower().strip()
    text = unicodedata.normalize("NFKD", text)
    text = "".join(ch for ch in text if not unicodedata.combining(ch))
    text = re.sub(r"[^\w\s]", "", text)
    text = re.sub(r"\s+", " ", text)
    return text.strip()


def _random_account_code(prefix: str = "9", length: int = 6) -> str:
    digits = "".join(random.choices("0123456789", k=length))
    return f"{prefix}{digits}"


def _prefix_for_role(role: str, balance_type: str) -> str:
    # keep existing heuristic but use role semantics
    if balance_type == DEBIT:
        # expenses and COGS -> expense-like (6); assets (1)
        if role.startswith("ex_") or role == roles.COGS:
            return "6"
        if role.startswith("asset"):
            return "1"
        return "6"
    return "4"


def _infer_role_and_balance(category_name: str, merchant_name: str, amount):
    """
    Heuristic to pick a more specific role (uses roles module) and a balance type.
    - Prefer category-based mapping (primary/detailed). Fall back to merchant heuristics.
    - Infer balance_type from role prefix or amount sign.
    """
    # Normalize inputs
    cat = normalize_text(category_name or "")
    m = normalize_text(merchant_name or "")

    # Category-first mapping (look for substrings)
    if any(k in cat for k in ("income", "payroll", "salary", "refund")):
        chosen_role = roles.INCOME_OPERATIONAL
    elif "interest" in cat:
        chosen_role = roles.INCOME_INTEREST
    elif "dividend" in cat:
        chosen_role = roles.INCOME_PASSIVE
    elif any(k in cat for k in ("cogs", "cost_of_goods", "costofgoods", "cost")):
        chosen_role = roles.COGS
    elif "tax" in cat:
        chosen_role = roles.EXPENSE_TAXES
    elif any(k in cat for k in ("loan", "payment", "credit card", "credit_card", "loan_payments")):
        # payments/loan -> treat as a liability target (accounts payable / cc payments)
        chosen_role = roles.LIABILITY_CL_ACC_PAYABLE
    elif any(k in cat for k in ("travel", "transportation", "food", "restaurants", "coffee", "entertainment", "general_merchandise")):
        chosen_role = roles.EXPENSE_OPERATIONAL
    else:
        # Merchant-name heuristics
        if any(k in m for k in ("uber", "lyft", "taxi", "starbucks", "mcdonald", "macdonald", "restaurant", "hotel")):
            chosen_role = roles.EXPENSE_OPERATIONAL
        else:
            # conservative default: operational expense
            chosen_role = roles.EXPENSE_OPERATIONAL

    # Balance type inference: assets & expenses/COGS are debit-normal, income & liabilities are credit-normal
    if chosen_role.startswith("asset") or chosen_role.startswith("ex_") or chosen_role == roles.COGS:
        balance_type = DEBIT
    else:
        # incomes, liabilities, equity -> credit by default
        balance_type = CREDIT
    # If explicit amount sign contradicts (e.g., negative expense) prefer amount sign
    try:
        if amount is not None:
            amt = Decimal(str(amount))
            balance_type = DEBIT if amt > 0 else CREDIT
            # adjust role for sign where appropriate: negative => treat as income/refund
            if amt < 0 and chosen_role == roles.EXPENSE_OPERATIONAL:
                chosen_role = roles.INCOME_OPERATIONAL
    except (InvalidOperation, TypeError):
        pass

    return chosen_role, balance_type


def create_rules_from_plaid_transactions(transactions: TransactionModel, coa_slug: str = None):
    """
    Create RuleGroups from Plaid transactions file, creating target accounts
    under a ChartOfAccountModel.

    Key changes:
    - Do NOT create rules based on Plaid `account_id` (these refer to bank accounts).
    - Instead, create/find target AccountModel for the counterparty/merchant and
      create rules matching vendor/category.
    - Use the richer role set from ledger.io.roles (not only EXPENSE_OPERATIONAL/INCOME_OPERATIONAL).
    """
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
        # Resolve best counterparty/merchant name
        cp_name = None
        counterparties = tx_data.get("counterparties") or []
        if counterparties:
            for cp in counterparties:
                if cp.get("type") == "merchant" and cp.get("name"):
                    cp_name = cp.get("name")
                    break
            if not cp_name and counterparties:
                cp_name = counterparties[0].get("name")
        merchant_name = tx_data.get("merchant_name")
        if not cp_name:
            cp_name = merchant_name or tx_data.get("name")

        pfc = tx_data.get("personal_finance_category", {}) or {}
        category_name = pfc.get("primary") or pfc.get("detailed")

        # Skip if there's nothing sensible to match on (no vendor and no category)
        if not cp_name and not category_name:
            skipped_count += 1
            continue

        normalized_vendor = normalize_text(cp_name or merchant_name or "")
        rule_name = f"Auto rule for {normalized_vendor}"

        # Account title preference: use merchant/counterparty name; fallback to category
        formatted_account_name = (cp_name or category_name or "Uncategorized").strip()[:100]

        # Infer role & balance_type using the richer roles
        amount = tx_data.get("amount")
        role, balance_type = _infer_role_and_balance(category_name, cp_name, amount)

        # Try to find an existing AccountModel that matches this counterparty and role
        accounts_qs = coa.get_non_root_coa_accounts_qs()
        target_account = accounts_qs.filter(name__iexact=formatted_account_name, role=role).first()
        if not target_account and normalized_vendor:
            # fallback: contains search
            target_account = accounts_qs.filter(name__icontains=normalized_vendor).first()

        created_account = False
        if not target_account:
            prefix = _prefix_for_role(role, balance_type)
            code = _random_account_code(prefix=prefix, length=5)
            target_account = coa.create_account(
                code=code,
                role=role,
                name=formatted_account_name,
                balance_type=balance_type,
                active=True,
            )
            created_account = True

        with transaction.atomic():
            existing_group = RuleGroup.objects.filter(name=rule_name).first()
            if existing_group:
                group = existing_group
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

            # vendor equality condition (normalized)
            _, created_vendor_cond = RuleCondition.objects.get_or_create(
                group=group,
                field=RuleCondition.FieldChoices.VENDOR,
                operator=RuleCondition.OperatorChoices.EQUALS,
                value=normalized_vendor,
            )

            created_category_cond = False
            if category_name:
                _, created_category_cond = RuleCondition.objects.get_or_create(
                    group=group,
                    field=RuleCondition.FieldChoices.CATEGORY,
                    operator=RuleCondition.OperatorChoices.EQUALS,
                    value=category_name,
                )

            # IMPORTANT: do NOT create RuleCondition for Plaid account_id (bank account UUID)

        if (not existing_group) or created_account or created_vendor_cond or created_category_cond:
            created_count += 1
        else:
            skipped_count += 1

    print(f"🎉 Rule creation finished. Created: {created_count}, Skipped: {skipped_count}.")