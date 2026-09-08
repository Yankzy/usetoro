instructions = """
    You are an expert financial analyst building an automated, enterprise-grade bookkeeping system. Your task is to analyze financial transactions and propose several distinct, high-accuracy `RuleGroup` and `RuleCondition` sets for categorization. Your goal is to maximize automation coverage while minimizing the risk of miscategorization.

    Core Principles

    1. Guiding Philosophy: Specificity and Priority

    Always prioritize specific rules over general ones. Your suggestions should follow a layered logic:

    - Identify Specifics First: Look for unique keywords (e.g., 'AWS', 'Eats', 'Payroll'). Create a high-priority rule (e.g., `priority: 100`) for this specific case.
    - Create General Fallbacks: After defining specific rules, create a lower-priority rule (e.g., `priority: 200` or higher) for the general vendor (e.g., 'Amazon', 'Uber').
        This ensures that 'Amazon Web Services' is correctly categorized as 'Cloud Hosting' instead of being caught by a general 'Amazon' rule for 'Office Supplies'.

    2. Critical Rule: Always Check Amount Direction (Inflow vs. Outflow)

    This is the most important principle for accuracy. For Plaid transactions, money flow is defined as:
    - Outflow (Expense): The `amount` field is a positive number.
    - Inflow (Income/Refund): The `amount` field is a negative number.

    Every rule you create MUST account for this.

    - Rules for expenses MUST include a condition: `{"field": "amount", "operator": "gt", "value": "0"}`.
    - Rules for income or refunds MUST include a condition: `{"field": "amount", "operator": "lt", "value": "0"}`.
        A rule is incomplete and incorrect without an amount direction check. This prevents miscategorizing refunds as new expenses.

    3. Account Naming Convention

    Propose clear, hierarchical account names using a colon separator (e.g., `Expenses:IT:Cloud Hosting`, `Income:Interest`, `Assets:Fixed Assets:Computer Hardware`). Be specific and avoid generic names like 'Miscellaneous' or 'General Expense'.

    Technical Constraints

    Rule Engine Models

    You will be generating rules based on the following Django models. Adhere strictly to the available `FieldChoices` and `OperatorChoices`.

    
    # RuleGroup: A set of RuleConditions plus logic (AND/OR) and priority.
    class RuleGroup(models.Model):
        class LogicChoices(models.TextChoices):
            AND = 'AND', 'All (AND)'
            OR = 'OR', 'Any (OR)'
        name = models.CharField(max_length=128)
        logic = models.CharField(max_length=3, choices=LogicChoices.choices, default=LogicChoices.AND)
        priority = models.PositiveIntegerField(default=100) # Lower values run first
        target_account = models.ForeignKey('AccountModel', on_delete=models.CASCADE)

    # RuleCondition: One atomic check (field + operator + value).
    class RuleCondition(models.Model):
        class FieldChoices(models.TextChoices):
            DESCRIPTION = 'description', 'Description' # Use this for the main transaction text/name
            VENDOR = 'vendor', 'Vendor'
            AMOUNT = 'amount', 'Amount'
            MCC = 'mcc', 'Merchant Category Code'
            CATEGORY = 'category', 'Bank Category'
        class OperatorChoices(models.TextChoices):
            CONTAINS = 'contains', 'Contains'
            EQUALS = 'equals', 'Equals'
            GT = 'gt', 'Greater Than'
            LT = 'lt', 'Less Than'
            IN = 'in', 'In List' # Value should be a comma-separated string
            REGEX = 'regex', 'Regex'
        
        group = models.ForeignKey(RuleGroup, related_name="conditions", on_delete=models.CASCADE)
        field = models.CharField(max_length=32, choices=FieldChoices.choices)
        operator = models.CharField(max_length=16, choices=OperatorChoices.choices)
        value = models.TextField()
    

    Output Format

    - Provide at least two distinct rule suggestions.
    - Always respond with a single, valid JSON object.
    - This object must contain a single key `"suggestions"`, which is a list of rule group objects.
    - Do not include any other text, explanations, or markdown formatting outside of the JSON object.

    JSON Schema for a single Rule Group:

    
    {
        "name": "A descriptive name for the rule",
        "logic": "AND",
        "priority": 100,
        "target_account_index": 11,
        "confidence": 1,
        "conditions": [
            {
                "field": "field_name",
                "operator": "operator_name",
                "value": "value_to_match"
            }
        ]
    }
    

    Examples

    BAD Vendor Rule (Avoid)

    This rule is brittle because vendor names are often messy.

    
    {
        "name": "Flight Purchase Categorization",
        "logic": "AND",
        "priority": 100,
        "target_account_index": 2,
        "confidence": 0.70,
        "conditions": [
            {"field": "vendor", "operator": "equals", "value": "United Airlines"}
        ]
    }
    

    GOOD Layered Rules (Emulate)

    1. Specific Case: Uber Eats (High Priority)
    This rule correctly isolates food delivery from ride-sharing.

    
    {
        "name": "Meals: Food Delivery - Uber Eats",
        "priority": 100,
        "logic": "AND",
        "target_account_index": 104,
        "confidence": 0.90,
        "conditions": [
            {"field": "description", "operator": "regex", "value": "uber.*eats|eats.*uber"},
            {"field": "amount", "operator": "gt", "value": "0"}
        ]
    }
    

    2. General Case: Uber Rideshare (Low Priority)
    This rule runs after and catches any remaining "Uber" transactions as transportation.

    
    {
        "name": "Transportation: Rideshare - Uber",
        "priority": 200,
        "logic": "AND",
        "target_account_index": 6,
        "confidence": 0.88,
        "conditions": [
            {"field": "description", "operator": "contains", "value": "uber"},
            {"field": "amount", "operator": "gt", "value": "0"}
        ]
    }
    

    3. Inflow vs. Outflow: Interest

    
    [
        {
            "name": "Expense: Loan Interest Paid",
            "priority": 100,
            "logic": "AND",
            "target_account_index": 18,
            "confidence": 0.31,
            "confidence": 0.9,
            "conditions": [
                {"field": "description", "operator": "regex", "value": "interest|intrst"},
                {"field": "amount", "operator": "gt", "value": "0"}
            ]
        },
        {
            "name": "Income: Bank Interest Received",
            "priority": 200,
            "logic": "AND",
            "target_account_index": 31,
            "confidence": 0.98,
            "conditions": [
                {"field": "description", "operator": "regex", "value": "interest|intrst"},
                {"field": "amount", "operator": "lt", "value": "0"}
            ]
        }
    ]
    

    4. Capital Expense: High-Value Purchase
    This rule identifies a large hardware purchase that should be capitalized, not expensed.

    
    {
        "name": "Capital Expenditure: Apple Hardware",
        "logic": "AND",
        "priority": 90,
        "target_account_index": 7,
        "confidence": 0.8,
        "conditions": [
            {"field": "vendor", "operator": "contains", "value": "Apple"},
            {"field": "description", "operator": "regex", "value": "macbook|imac|mac studio"},
            {"field": "amount", "operator": "gt", "value": "1500"}
        ]
    }
    
    NOTE: confidence field is how sure you're about your target_account_index choice.

    Your primary goal is to identify the correct target_account for a given transaction. 
    You must use the list_chart_of_accounts tool to explore the entity's available accounts before making a decision. 
    Start with a broad search (e.g., for an expense, filter by account_types=['debit']). 
    If the transaction description contains specific words like 'software' or 'travel', 
    use the search_keywords parameter to narrow down the results and find the most specific account.


    When using the `list_chart_of_accounts` tool, your goal is to find the single best account for the transaction.
    1.  Start with a specific query using keywords from the transaction description.
    2.  If you receive an empty list of accounts or a message indicating no results, do not invent an account.
    3.  Instead, call the tool a second time with broader search terms. For example, if a search for `'SaaS Subscription'` fails, try just `'Software'`.
    4.  If the second search also fails, call the tool a final time without any `search_keywords` to see all available accounts of the relevant type (e.g., all `'debit'` accounts).
    5.  You must then choose the most plausible account from that general list.

    CRITICAL INSTRUCTIONS FOR ACCOUNT SELECTION:
    - You MUST use the list_chart_of_accounts tool to find a suitable target_account.
    - Your final target_account value MUST EXACTLY MATCH a name from the list of accounts returned by the tool.
    - Do NOT invent, combine, or modify account names. Do NOT use hierarchical names like 'Category:Subcategory' 
        unless that is the exact name in the Chart of Accounts.
    - If you use the tool and cannot find a suitable account, you MUST return "TARGET_ACCOUNT_NOT_FOUND" as the value for target_account. Do not guess.


    CRITICAL INSTRUCTIONS FOR NO ACCOUNT SELECTION:
    - If no account match or you are not confidence the right account exist in CoA, return an object with empty suggestions list
    like:

    
    {"suggestions": []}
    
    Not finding any account match is not a bad thing, it could mean this company just signed up and these transactions are the first batch from plaid.
    So by returning empty list tells our system to create the accounts.
    
    Now, analyze the transaction data provided and generate your suggestions based on all the principles and constraints above.
    IMPORTANT: Do not suggest rule base on account_id even if a transaction has account_id.
    NOTE: Transaction account_id is irrelevant, it points to bank account which we already know.
"""

import re
from decimal import Decimal, InvalidOperation
from pathlib import Path
import json
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
import logging
from agents import Agent, Runner, RunConfig
import unicodedata
from decimal import Decimal, InvalidOperation
from pathlib import Path
import random
from ledger.io.roles import EXPENSE_OPERATIONAL, INCOME_OPERATIONAL
from ledger.models.accounts import DEBIT, CREDIT
from ledger.bookkeeping_agents.tools import LocalContext, list_chart_of_accounts
from asgiref.sync import sync_to_async


logger = logging.getLogger(__name__)

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


# helper to call the LLM agent and get structured suggestions
async def _call_rule_suggester_agent(tx_data: dict):
    """
    Calls the Rule Suggestion Agent.

    - Do NOT dump the full COA into the LLM prompt.
    - Provide a small, truncated list of account names/roles as `available_account_names`.
    - Expose the `list_chart_of_accounts` function-tool so the agent can request
      richer chart-of-account data if it needs it (tool calls are audited and controlled).
    """
    

    payload = {
        "transaction": tx_data,
        "available_account_names": "compact_accounts",
        "coa_slug": "coa_slug",
        "note": "If you need exact account codes, balance types or metadata, call the `list_chart_of_accounts` tool."
    }

    # keep the main instructions but tell the model to prefer calling tools for detailed COA data
    extra = (
        "\n\nImportant tool guidance: do not invent account codes. "
        "If you need full account metadata use the `list_chart_of_accounts` tool; "
        "otherwise work from `available_account_names`."
    )
    _instructions = json.dumps(instructions, default=str, ensure_ascii=False) + extra


    # 1. Get the entity
    entity = await sync_to_async(EntityModel.objects.get)(uuid="1fb9f680-971f-467b-8c01-858181a279fe")

    # 2. Fetch all accounts ONCE and add the index
    all_accounts_qs = await sync_to_async(list)(entity.get_all_accounts(active=True))
    accounts_snapshot = [
        {
            "index": i + 1,
            "uuid": str(a.uuid),
            "name": a.name,
            "role": a.role,
            "balance_type": a.balance_type,
        }
        for i, a in enumerate(all_accounts_qs)
    ]

    # 3. Create the context with the snapshot populated
    context_instance = LocalContext(
        user_uuid="...",
        entity_uuid="1fb9f680-971f-467b-8c01-858181a279fe",
        accounts_snapshot=accounts_snapshot
    )
    
    agent = Agent(
        name="Rule Suggestion Agent",
        instructions=_instructions,
        tools=[list_chart_of_accounts],  # allow the agent to request COA details explicitly
        output_type=None,
        model="gpt-4.1",
    )

    prompt = json.dumps(payload, default=str, ensure_ascii=False)
    try:
        # Runner.run will allow the LLM to call the tool when appropriate
        context = await Runner.run(
            agent,
            input=prompt,
            run_config=RunConfig(tracing_disabled=True, workflow_name="rule_suggester"),
            context=context_instance
        )
        return context.final_output
    except Exception as e:
        logger.exception("Agent call failed for transaction: %s", e)
        return None


async def create_rules_from_plaid_transactions(coa_slug: str = None):
    """
    Create RuleGroups from Plaid transactions file, creating target accounts
    under a ChartOfAccountModel.

    Uses an LLM agent to propose rule group suggestions in batches of 3 transactions.
    Does NOT create rules on Plaid's account_id (bank account UUID).
    Returns a list of agent responses (one per batch).
    """
    path = Path(__file__).resolve().parents[2] / "ledger" / "transactions.json"
    with open(path, "r", encoding="utf-8") as f:
        transactions = json.load(f)

    coa = None
    if coa_slug:
        coa = await sync_to_async(lambda: ChartOfAccountModel.objects.filter(slug=coa_slug).first())()
    else:
        coa = await sync_to_async(lambda: ChartOfAccountModel.objects.first())()
    if not coa:
        raise RuntimeError("No ChartOfAccountModel available.")

    created_count = 0
    skipped_count = 0

    added = transactions.get("added") or []
    if not isinstance(added, list):
        try:
            added = list(added)
        except Exception:
            added = []

    responses = []
    batch = []
    for tx_data in added:
        if not isinstance(tx_data, dict):
            skipped_count += 1
            continue

        counterparties = tx_data.get("counterparties") or []
        merchant_name = tx_data.get("merchant_name") or tx_data.get("name") or None

        candidate_vendor = None
        if counterparties:
            for cp in counterparties:
                if isinstance(cp, dict) and cp.get("type") == "merchant" and cp.get("name"):
                    candidate_vendor = cp.get("name")
                    break
            if not candidate_vendor and isinstance(counterparties[0], dict):
                candidate_vendor = counterparties[0].get("name")

        if not candidate_vendor:
            candidate_vendor = merchant_name

        pfc = tx_data.get("personal_finance_category") or {}
        category_name = pfc.get("primary") or pfc.get("detailed") or None

        # Skip transactions lacking both vendor and category
        if not candidate_vendor and not category_name:
            skipped_count += 1
            continue

        payment_meta = tx_data.get("payment_meta") or {}
        reduced_tx = {
            "amount": tx_data.get("amount"),
            "category": category_name,
            "check_number": tx_data.get("check_number") or payment_meta.get("check_number"),
            "counterparties": counterparties,
            "merchant_name": merchant_name,
            "name": tx_data.get("name"),
            "payment_channel": tx_data.get("payment_channel"),
            "personal_finance_category": pfc,
            "website": None,
            "transaction_id": tx_data.get("transaction_id"),
            "date": tx_data.get("date"),
            "iso_currency_code": tx_data.get("iso_currency_code"),
            "vendor": candidate_vendor,
        }

        # Prefer website from first counterparty, then merchant_metadata, then top-level website
        if counterparties and isinstance(counterparties[0], dict):
            first_cp = counterparties[0] or {}
            reduced_tx["website"] = first_cp.get("website") or first_cp.get("url")
        if not reduced_tx["website"]:
            mm = tx_data.get("merchant_metadata") or {}
            reduced_tx["website"] = mm.get("url") or mm.get("website") or tx_data.get("website")

        batch.append(reduced_tx)

        # When batch reaches 3, call the agent and clear batch
        # if len(batch) >= 3:
        #     try:
        #         agent_resp = await _call_rule_suggester_agent(batch)
        #     except Exception:
        #         logger.exception("Rule suggester agent failed for a batch")
        #         agent_resp = None
        #     responses.append({"batch": batch.copy(), "agent_response": agent_resp})
        #     batch.clear()

    # Send any remaining transactions in a final batch
    if batch:
        try:
            agent_resp = await _call_rule_suggester_agent(batch)
        except Exception:
            logger.exception("Rule suggester agent failed for final batch")
            agent_resp = None
        batch.clear()

    return agent_resp



    
#     suggestions = None
#     if agent_resp and isinstance(agent_resp, dict):
#         suggestions = agent_resp.get("suggestions")

#     # If agent didn't return suggestions, fallback to a minimal heuristic: vendor & vendor+description
#     if not suggestions:
#         normalized_vendor = normalize_text(candidate_vendor or "")
#         broad = {
#             "name": f"Auto rule for {normalized_vendor}",
#             "logic": "AND",
#             "target_account": (candidate_vendor or category_name or "Uncategorized"),
#             "conditions": [
#                 {"field": "vendor", "operator": "equals", "value": normalized_vendor}
#             ],
#         }
#         specific_desc = {
#             "name": f"Auto specific rule for {normalized_vendor}",
#             "logic": "AND",
#             "target_account": (candidate_vendor or category_name or "Uncategorized"),
#             "conditions": [
#                 {"field": "vendor", "operator": "equals", "value": normalized_vendor},
#                 {"field": "description", "operator": "contains", "value": tx_data.get("name", "")},
#             ],
#         }
#         suggestions = [broad, specific_desc]

#     # iterate suggestions and create accounts / rule groups
#     for sug in suggestions:
#         name = sug.get("name") or f"Auto rule {normalize_text(candidate_vendor or '')}"
#         logic = sug.get("logic", "AND")
#         tgt_account_name = sug.get("target_account") or (candidate_vendor or category_name or "Uncategorized")
#         conds = sug.get("conditions", []) or []

#         # Determine target account: find exact match first
#         accounts_qs = coa.get_non_root_coa_accounts_qs()
#         target_account = accounts_qs.filter(name__iexact=tgt_account_name).first()
#         created_account = False
#         if not target_account:
#             # decide role from amount sign (negative => inflow => income), fallback to expense
#             amount = tx_data.get("amount")
#             try:
#                 amt = Decimal(str(amount))
#                 role_guess = EXPENSE_OPERATIONAL if amt > 0 else INCOME_OPERATIONAL
#             except Exception:
#                 role_guess = EXPENSE_OPERATIONAL

#             # create new account (keep code generation heuristic)
#             prefix = _prefix_for_role(role_guess, DEBIT if role_guess.startswith("ex_") else CREDIT)
#             code = _random_account_code(prefix=prefix, length=5)
#             target_account = coa.create_account(
#                 code=code,
#                 role=role_guess,
#                 name=str(tgt_account_name)[:100],
#                 balance_type=DEBIT if role_guess.startswith("ex_") else CREDIT,
#                 active=True,
#             )
#             created_account = True

#         # create or reuse rule group
#         with transaction.atomic():
#             existing_group = RuleGroup.objects.filter(name=name).first()
#             if existing_group:
#                 group = existing_group
#                 if group.target_account_id != target_account.pk:
#                     group.target_account = target_account
#                     group.save(update_fields=["target_account"])
#             else:
#                 group = RuleGroup.objects.create(
#                     name=name,
#                     logic=logic if logic in (RuleGroup.LogicChoices.AND, RuleGroup.LogicChoices.OR) else RuleGroup.LogicChoices.AND,
#                     priority=200,
#                     target_account=target_account,
#                 )

#             created_any_cond = False
#             for c in conds:
#                 field = c.get("field")
#                 operator = c.get("operator")
#                 value = c.get("value")

#                 # validate allowed fields/operators
#                 if field not in ("description", "vendor", "amount"):
#                     continue
#                 if operator not in ("contains", "equals", "gt", "lt"):
#                     continue

#                 # map to model enum names if needed (we store strings matching FieldChoices)
#                 # Create condition
#                 _, created_cond = RuleCondition.objects.get_or_create(
#                     group=group,
#                     field=field,
#                     operator=operator,
#                     value=str(value)[:200],
#                 )
#                 if created_cond:
#                     created_any_cond = True

#         # update counters
#         if created_account or not existing_group or created_any_cond:
#             created_count += 1
#         else:
#             skipped_count += 1

# print(f"🎉 Rule creation finished. Created: {created_count}, Skipped: {skipped_count}.")




