instructions = """
    You are an expert financial analyst building an automated bookkeeping system. 
    Your task is to analyze transactions and propose several distinct rule groups and RuleConditions for categorization.

    Constraints:
    - This will be used in Rule Engine: 

    RuleEngine models for transaction categorization in Django
    ----------------------------------------------------------
    These models allow for highly flexible, explainable, and trackable rule-based bookkeeping automation.
    Each RuleGroup specifies a logic/priority and belongs to many RuleConditions.
    Audit log captures what rules were applied to which transactions and for what reason (full "why" transparency).

    from django.db.models import F

    RuleGroup: A set of RuleConditions plus logic (AND/OR), priority/order, analytics fields.
    class RuleGroup(models.Model):
        '''
        A RuleGroup organizes one or more atomic match conditions and specifies:
        - logical relationship (AND/OR)
        - overall rule priority (lower = first matched)
        - confidence score (determine by LLM)
        - which Account categorization to use if matched
        - coverage analytics (hit/miss counts)
        All RuleCondition objects linked to this RuleGroup define what is tested.
        '''
        class LogicChoices(models.TextChoices):
            AND = 'AND', 'All (AND)'  # All conditions must match (logical AND)
            OR = 'OR', 'Any (OR)'     # Any condition match triggers the rule (logical OR)
        
        name = models.CharField(max_length=128, help_text="Name for this rule group, e.g. 'Big Payroll Rule'")
        logic = models.CharField(
            max_length=3, choices=LogicChoices.choices, default=LogicChoices.AND,
            help_text="All conditions must match (AND) or any (OR) to trigger this rule"
        )
        priority = models.PositiveIntegerField(default=100, help_text="Lower values run earlier (higher priority)")
        confidence = models.FloatField(default=1.0, help_text="Confidence score for ML/AI-generated rules")
        active = models.BooleanField(default=True, help_text="Only active rules are considered")
        target_account = models.ForeignKey(
            'AccountModel', on_delete=models.CASCADE,
            help_text="Target account to assign if this rule matches"
        )
        match_count = models.PositiveIntegerField(default=0, help_text="Number of times this rule group matched")
        miss_count = models.PositiveIntegerField(default=0, help_text="Number of times this rule group didn't match")
        created_at = models.DateTimeField(auto_now_add=True, help_text="When the rule group was created")
        updated_at = models.DateTimeField(auto_now=True, help_text="When the rule group was last updated")
        
        def evaluate(self, tx):
            '''
            Returns True if this rule group matches the transaction,
            based on the group logic and results of each condition.
            '''
            conditions = list(self.conditions.all())
            results = [c.evaluate(tx) for c in conditions]
            if self.logic == self.LogicChoices.AND:
                return all(results)
            return any(results)

        def explain_match(self, tx):
            '''
            Returns a detailed dictionary **explaining** why and how this rule matched (or not).
            Explanation includes:
            - group info
            - per-condition evaluation with actual and expected values
            Returns None if not matched.
            '''
            conditions = list(self.conditions.all())
            explanations = [c.explain(tx) for c in conditions]
            results = [e['result'] for e in explanations]
            matched = (
                (self.logic == self.LogicChoices.AND and all(results)) or
                (self.logic == self.LogicChoices.OR and any(results))
            )
            if matched:
                return {
                    'matched': True,
                    'group_id': self.id,
                    'group_name': self.name,
                    'logic': self.logic,
                    'priority': self.priority,
                    'target_account_id': self.target_account_id,
                    'target_account': str(self.target_account),
                    'conditions': explanations,  # List of why each condition did/didn't match
                }
            return None

        def increment_match(self):
            '''
            Increment the number of times this rule group has matched any transaction.
            Used for analytics and coverage tracking.
            '''
            self.__class__.objects.filter(pk=self.pk).update(match_count=F('match_count') + 1)

        def increment_miss(self):
            '''
            Increment the number of times this rule group was considered but didn't match.
            Used for analytics and coverage tracking.
            '''
            self.__class__.objects.filter(pk=self.pk).update(miss_count=F('miss_count') + 1)

        @classmethod
        def match_any_verbose(cls, tx):
            '''
            Try all active RuleGroups (ordered by priority) for the given transaction.
            Returns the FIRST matching RuleGroup's explanation dict, and updates analytics counters:
            - If a rule matches: rule.increment_match() and return explanation.
            - If no match: increment miss counts (for analytics).
            '''
            rules = cls.objects.filter(active=True).order_by('priority').prefetch_related('conditions')
            for rule in rules:
                detail = rule.explain_match(tx)
                if detail:
                    rule.increment_match()
                    return detail
                else:
                    rule.increment_miss()
            return None  # No rule group matched



    # RuleCondition: One atomic check (field+operator+value), e.g. "amount > 1000"
    class RuleCondition(models.Model):
        '''
        A RuleCondition defines a single field, operator, and value for matching a transaction.
        Example:
        - field: 'description'
        - operator: 'contains'
        - value: 'uber'
        This would be True for any transaction whose description contains 'uber'.
        '''
        class FieldChoices(models.TextChoices):
            '''
            This gives us nuance matching like:
                - Vendor = Starbucks AND MCC = 5814 → Meals & Entertainment
                - Vendor = Starbucks AND invoice_text contains "beans" → COGS
                - Vendor = Starbucks AND amount > 500 AND month = December → Gifts
                That way, if invoice text exists, it overrides the general MCC rule.
            '''
            DESCRIPTION = 'description', 'Description'
            VENDOR = 'vendor', 'Vendor'
            MEMO = 'memo', 'Memo'
            AMOUNT = 'amount', 'Amount'
            ROLE = 'role', 'Account Role'
            UUID = 'uuid', 'Account UUID'
            DATE = 'date', 'Transaction Date'
            TIME = 'time', 'Transaction Time'
            MCC = 'mcc', 'Merchant Category Code'
            CATEGORY = 'category', 'Bank Category'
            INVOICE_TEXT = 'invoice_text', 'Invoice/Receipt Text'


        class OperatorChoices(models.TextChoices):
            CONTAINS = 'contains', 'Contains'
            EQUALS = 'equals', 'Equals'
            STARTSWITH = 'startswith', 'Starts With'
            ENDSWITH = 'endswith', 'Ends With'
            GT = 'gt', 'Greater Than'
            LT = 'lt', 'Less Than'
            IN = 'in', 'In List'
            REGEX = 'regex', 'Regex'

        group = models.ForeignKey(RuleGroup, related_name="conditions", on_delete=models.CASCADE,
                                help_text="Parent RuleGroup this condition belongs to")
        field = models.CharField(max_length=32, choices=FieldChoices.choices, help_text="Transaction field to evaluate")
        operator = models.CharField(max_length=16, choices=OperatorChoices.choices, help_text="Comparison operator to use")
        value = models.TextField(help_text="Condition value. If 'in', use comma-separated list.")

        def evaluate(self, tx):
            '''
            Perform the atomic comparison for this condition against the given transaction.
            - Selects field from tx
            - Applies the operator (e.g., gt, contains, etc.)
            - Handles type casting for numbers
            Returns True if the condition is met, False otherwise.
            '''
            val = getattr(tx, self.field, None)
            # Handle type casting for numeric fields
            if self.field == RuleCondition.FieldChoices.AMOUNT:
                try:
                    val = float(val)
                    cmp = float(self.value)
                except Exception:
                    return False
            else:
                # Normalize non-string tx values (UUID, Decimal, int, etc.) to string,
                # then lowercase for case-insensitive matching.
                if val is None:
                    val = ''
                elif not isinstance(val, str):
                    val = str(val)
                val = val.lower()
                cmp = '' if self.value is None else str(self.value).lower()


            op = self.operator

            if op == self.OperatorChoices.CONTAINS:
                return cmp in (val or '')
            if op == self.OperatorChoices.EQUALS:
                return (val or '') == (cmp or '')
            if op == self.OperatorChoices.STARTSWITH:
                return (val or '').startswith(cmp)
            if op == self.OperatorChoices.ENDSWITH:
                return (val or '').endswith(cmp)
            if op == self.OperatorChoices.GT:
                return val > cmp
            if op == self.OperatorChoices.LT:
                return val < cmp
            if op == self.OperatorChoices.IN:
                items = [x.strip().lower() for x in (self.value or '').split(',')]
                return val in items
            if op == self.OperatorChoices.REGEX:
                try:
                    import re
                    return re.search(cmp, val or '') is not None
                except re.error:
                    return False
            return False  # If an unknown operator, strictly fail

        def explain(self, tx):
            '''
            Returns a dictionary with full details explaining the result of this atomic check.
            For use in audit logs, user-facing 'why' explanations, and analytics dashboards.
            '''
            tx_val = getattr(tx, self.field, None)
            result = self.evaluate(tx)
            return {
                'condition_id': self.id,
                'field': self.field,
                'operator': self.operator,
                'expected_value': self.value,
                'tx_value': tx_val,
                'result': result,
            }



    RuleAuditLog: For tracking/auditing which rule (if any) applied to each transaction
    class RuleAuditLog(models.Model):
        '''
        Records categorization runs for transactions:
        - What rule group (if any) matched?
        - Was there a match?
        - Why (full conditions/explainer)?
        - When?
        Useful for coverage analytics, debugging, compliance, and user support.
        '''
        tx = models.ForeignKey('TransactionModel', on_delete=models.CASCADE, help_text="Categorized transaction")
        rule_group = models.ForeignKey(RuleGroup, on_delete=models.SET_NULL, null=True, help_text="Matched rule group (if any)")
        matched = models.BooleanField(help_text="Did any rule match?")
        match_info = models.JSONField(null=True, blank=True,
                                    help_text="Verbose match explanation: output of explain_match()")
        created_at = models.DateTimeField(auto_now_add=True, help_text="When this audit log entry was created")


    # **How this works in practice**

    - **RuleCondition.evaluate(tx):** Evaluates a single logic condition on a transaction (e.g., does `amount > 1000`?).
    - **RuleCondition.explain(tx):** Returns an object explaining the result of that test (expected value, tx value, pass/fail).
    - **RuleGroup.evaluate(tx):** Evaluates all conditions for the group with AND/OR logic.
    - **RuleGroup.explain_match(tx):** Returns detailed explanation if grouped conditions hit, for UX/audit.
    - **RuleGroup.match_any_verbose(tx):** Tries all (active, priority-ordered) rules for the transaction, returns first match's explanation (and analytic counters).
    - **RuleAuditLog:** Stores the explanation and match result for every transaction processed, for stats/analytics/troubleshooting.

    - Provide at least two rule suggestions.
    - Always respond with a valid JSON object containing a single key "suggestions", which is a list of rule group objects. Do not include any other text or explanation.

    JSON Schema for a single Rule Group:
    {
    "name": "A descriptive name for the rule",
    "logic": "AND",
    "target_account": "The suggested account name",
    "conditions": [
        {
        "field": "field_name",
        "operator": "operator_name",
        "value": "value_to_match"
        }
    ]
    }

    Here is the single transaction (valid JSON) followed by a short list of available account names with roles (JSON).
    Analyze the transaction and return only the required JSON object.

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
from ledger.bookkeeping_agents.tools import list_chart_of_accounts
from agents import function_tool



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



def _random_account_code(prefix: str = "9", length: int = 6) -> str:
    digits = "".join(random.choices("0123456789", k=length))
    return f"{prefix}{digits}"


def _prefix_for_role(role: str, balance_type: str) -> str:
    # heuristic prefixes: expenses ~6, income/credit ~4, default assets ~1
    if balance_type == DEBIT:
        return "6" if role == EXPENSE_OPERATIONAL else "1"
    return "4"


logger = logging.getLogger(__name__)


# helper to call the LLM agent and get structured suggestions
def _call_rule_suggester_agent(tx_data: dict, coa_accounts: list, coa_slug: str = None):
    """
    Calls the Rule Suggestion Agent.

    - Do NOT dump the full COA into the LLM prompt.
    - Provide a small, truncated list of account names/roles as `available_account_names`.
    - Expose the `list_chart_of_accounts` function-tool so the agent can request
      richer chart-of-account data if it needs it (tool calls are audited and controlled).
    """
    # provide only a compact list of account name/role tuples (safe, truncated)
    compact_accounts = [
        {"name": a.get("name"), "role": a.get("role")}
        for a in (coa_accounts or [])[:50]
    ]

    payload = {
        "transaction": tx_data,
        "available_account_names": compact_accounts,
        "coa_slug": coa_slug,
        "note": "If you need exact account codes, balances or metadata, call the `list_chart_of_accounts` tool."
    }

    # keep the main instructions but tell the model to prefer calling tools for detailed COA data
    extra = (
        "\n\nImportant tool guidance: do not invent account codes. "
        "If you need full account metadata use the `list_chart_of_accounts` tool; "
        "otherwise work from `available_account_names`."
    )
    _instructions = json.dumps(instructions, default=str, ensure_ascii=False) + extra

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
        context = Runner.run(
            agent,
            input=prompt,
            run_config=RunConfig(tracing_disabled=True, workflow_name="rule_suggester"),
        )
        return context.final_output
    except Exception as e:
        logger.exception("Agent call failed for transaction: %s", e)
        return None


def create_rules_from_plaid_transactions(coa_slug: str = None):
    """
    Create RuleGroups from Plaid transactions file, creating target accounts
    under a ChartOfAccountModel.

    Uses an LLM agent to propose rule group suggestions per transaction.
    Does NOT create rules on Plaid's account_id (bank account UUID).
    """
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

    # prepare a lightweight account list to pass to the agent
    all_accounts = list(
        coa.get_non_root_coa_accounts_qs()
        .values("name", "role", "code")
        .order_by("name")[:500]
    )

    for tx_data in transactions["added"][3]:
        # If there's no vendor/merchant and no category, skip quickly
        counterparties = tx_data.get("counterparties") or []
        merchant_name = tx_data.get("merchant_name") or tx_data.get("name")
        candidate_vendor = None
        if counterparties:
            for cp in counterparties:
                if cp.get("type") == "merchant" and cp.get("name"):
                    candidate_vendor = cp.get("name")
                    break
            if not candidate_vendor:
                candidate_vendor = counterparties[0].get("name", None)
        if not candidate_vendor:
            candidate_vendor = merchant_name

        pfc = tx_data.get("personal_finance_category", {}) or {}
        category_name = pfc.get("primary") or pfc.get("detailed")

        if not candidate_vendor and not category_name:
            skipped_count += 1
            continue

        # Call agent for this single transaction
        agent_resp = _call_rule_suggester_agent(
            tx_data,
            [{"name": a["name"], "role": a["role"]} for a in all_accounts],
        )

        suggestions = None
        if agent_resp and isinstance(agent_resp, dict):
            suggestions = agent_resp.get("suggestions")

        # If agent didn't return suggestions, fallback to a minimal heuristic: vendor & vendor+description
        if not suggestions:
            normalized_vendor = normalize_text(candidate_vendor or "")
            broad = {
                "name": f"Auto rule for {normalized_vendor}",
                "logic": "AND",
                "target_account": (candidate_vendor or category_name or "Uncategorized"),
                "conditions": [
                    {"field": "vendor", "operator": "equals", "value": normalized_vendor}
                ],
            }
            specific_desc = {
                "name": f"Auto specific rule for {normalized_vendor}",
                "logic": "AND",
                "target_account": (candidate_vendor or category_name or "Uncategorized"),
                "conditions": [
                    {"field": "vendor", "operator": "equals", "value": normalized_vendor},
                    {"field": "description", "operator": "contains", "value": tx_data.get("name", "")},
                ],
            }
            suggestions = [broad, specific_desc]

        # iterate suggestions and create accounts / rule groups
        for sug in suggestions:
            name = sug.get("name") or f"Auto rule {normalize_text(candidate_vendor or '')}"
            logic = sug.get("logic", "AND")
            tgt_account_name = sug.get("target_account") or (candidate_vendor or category_name or "Uncategorized")
            conds = sug.get("conditions", []) or []

            # Determine target account: find exact match first
            accounts_qs = coa.get_non_root_coa_accounts_qs()
            target_account = accounts_qs.filter(name__iexact=tgt_account_name).first()
            created_account = False
            if not target_account:
                # decide role from amount sign (negative => inflow => income), fallback to expense
                amount = tx_data.get("amount")
                try:
                    amt = Decimal(str(amount))
                    role_guess = EXPENSE_OPERATIONAL if amt > 0 else INCOME_OPERATIONAL
                except Exception:
                    role_guess = EXPENSE_OPERATIONAL

                # create new account (keep code generation heuristic)
                prefix = _prefix_for_role(role_guess, DEBIT if role_guess.startswith("ex_") else CREDIT)
                code = _random_account_code(prefix=prefix, length=5)
                target_account = coa.create_account(
                    code=code,
                    role=role_guess,
                    name=str(tgt_account_name)[:100],
                    balance_type=DEBIT if role_guess.startswith("ex_") else CREDIT,
                    active=True,
                )
                created_account = True

            # create or reuse rule group
            with transaction.atomic():
                existing_group = RuleGroup.objects.filter(name=name).first()
                if existing_group:
                    group = existing_group
                    if group.target_account_id != target_account.pk:
                        group.target_account = target_account
                        group.save(update_fields=["target_account"])
                else:
                    group = RuleGroup.objects.create(
                        name=name,
                        logic=logic if logic in (RuleGroup.LogicChoices.AND, RuleGroup.LogicChoices.OR) else RuleGroup.LogicChoices.AND,
                        priority=200,
                        target_account=target_account,
                    )

                created_any_cond = False
                for c in conds:
                    field = c.get("field")
                    operator = c.get("operator")
                    value = c.get("value")

                    # validate allowed fields/operators
                    if field not in ("description", "vendor", "amount"):
                        continue
                    if operator not in ("contains", "equals", "gt", "lt"):
                        continue

                    # map to model enum names if needed (we store strings matching FieldChoices)
                    # Create condition
                    _, created_cond = RuleCondition.objects.get_or_create(
                        group=group,
                        field=field,
                        operator=operator,
                        value=str(value)[:200],
                    )
                    if created_cond:
                        created_any_cond = True

            # update counters
            if created_account or not existing_group or created_any_cond:
                created_count += 1
            else:
                skipped_count += 1

    print(f"🎉 Rule creation finished. Created: {created_count}, Skipped: {skipped_count}.")


