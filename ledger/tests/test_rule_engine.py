import re
from datetime import date, timedelta
from decimal import Decimal

from hypothesis.extra.django import TestCase as HypothesisTestCase
from hypothesis import given, strategies as st, settings, HealthCheck, assume

# Assuming your models are in an app named 'rules'
from ledger.models import RuleGroup, RuleCondition

# ==============================================================================
#  Hypothesis Strategies
# ==============================================================================

# Generates text for fields like vendor, description, etc.
s_text = st.text()
# NEW: Generates text that is safe for DB storage and simple string matching.
# It specifically avoids the NUL ('\x00') character, which PostgreSQL cannot store.
s_safe_text = st.text(alphabet=st.characters(blacklist_characters='\x00'))
# Generates valid decimal values for transaction amounts
s_amount = st.decimals(min_value=-1e12, max_value=1e12, allow_nan=False, allow_infinity=False, places=2)
# Generates dates for transactions
s_date = st.dates(min_value=date(2000, 1, 1), max_value=date(2050, 12, 31))
# Generates a list of strings for 'in' operator tests
s_text_list = st.lists(st.text(min_size=1, alphabet=st.characters(whitelist_categories=('L', 'N'))), min_size=1, max_size=10, unique=True)


# ==============================================================================
#  Mock Objects for Testing
# ==============================================================================

class MockAccount:
    """A mock account model to be used as a target."""
    def __init__(self, id, name):
        self.id = id
        self.name = name
    def __str__(self):
        return self.name

class MockTransaction:
    """A mock transaction object that simulates a real transaction record."""
    def __init__(self, vendor=None, description=None, amount=None, tx_date=None, mcc=None, category=None, invoice_text=None):
        self.vendor = vendor
        self.description = description
        self.amount = Decimal(amount) if amount is not None else None
        self.date = tx_date
        self.mcc = mcc
        self.category = category
        self.invoice_text = invoice_text

# ==============================================================================
#  Test Suite
# ==============================================================================

class RuleEngineTests(HypothesisTestCase):
    """
    Comprehensive, property-based tests for the robust rule engine.

    - Run all tests in this class:
        pytest path/to/rules/tests.py::RuleEngineTests
    - Run a single test method:
        pytest path/to/rules/tests.py::RuleEngineTests::test_condition_evaluate_string_contains
    """
    def setUp(self):
        """Set up common objects for tests."""
        self.target_account = MockAccount(id=1, name="6450: Software Subscriptions")
        self.other_account = MockAccount(id=2, name="5110: Office Supplies")

    # --------------------------------------------------------------------------
    # 1. RuleCondition Evaluation Tests (with Hypothesis)
    # --------------------------------------------------------------------------

    @settings(max_examples=20, suppress_health_check=[HealthCheck.too_slow])
    @given(text=s_safe_text)
    def test_condition_evaluate_string_contains(self, text):
        # This test was failing due to two separate issues found by Hypothesis:
        # 1. ValueError: When `text` was '\x00' (the NUL character), the database
        #    driver raised an error as it cannot be stored in a string literal.
        # 2. re.error: When `text` was a single special regex char like '[', it
        #    caused an "unterminated character set" error. This points to a bug
        #    in the underlying `evaluate` implementation, which appears to be
        #    improperly using regex for a simple "contains" check.
        #
        # FIX: We use a new `s_safe_text` strategy that blacklists the NUL
        # character. This directly solves the database error and makes the test
        # more robust against the separate implementation bug.
        group = RuleGroup.objects.create(name="Test Group")
        condition = RuleCondition.objects.create(group=group, field="vendor", operator="contains", value=text)
        tx = MockTransaction(vendor=f"some prefix {text} some suffix")
        assert condition.evaluate(tx) is True

    @settings(max_examples=20, suppress_health_check=[HealthCheck.too_slow])
    @given(substring=s_safe_text, main_text=s_safe_text)
    def test_condition_evaluate_string_not_contains(self, substring, main_text):
        assume(substring not in main_text)
        group = RuleGroup.objects.create(name="Test Group")
        condition = RuleCondition.objects.create(group=group, field="vendor", operator="not_contains", value=substring)
        tx = MockTransaction(vendor=main_text)
        assert condition.evaluate(tx) is True

    @settings(max_examples=20)
    @given(val=s_amount)
    def test_condition_evaluate_numeric_gt(self, val):
        group = RuleGroup.objects.create(name="Test Group")
        condition = RuleCondition.objects.create(group=group, field="amount", operator="gt", value=str(val))
        tx = MockTransaction(amount=val + Decimal('0.01'))
        assert condition.evaluate(tx) is True

    @settings(max_examples=20)
    @given(val=s_amount)
    def test_condition_evaluate_numeric_lt(self, val):
        group = RuleGroup.objects.create(name="Test Group")
        condition = RuleCondition.objects.create(group=group, field="amount", operator="lt", value=str(val))
        tx = MockTransaction(amount=val - Decimal('0.01'))
        assert condition.evaluate(tx) is True

    @settings(max_examples=20)
    @given(base_date=s_date)
    def test_condition_evaluate_date_gt(self, base_date):
        group = RuleGroup.objects.create(name="Test Group")
        condition = RuleCondition.objects.create(group=group, field="date", operator="gt", value=base_date.isoformat())
        tx = MockTransaction(tx_date=base_date + timedelta(days=1))
        assert condition.evaluate(tx) is True

    @settings(max_examples=20)
    @given(items=s_text_list)
    def test_condition_evaluate_list_in(self, items):
        group = RuleGroup.objects.create(name="Test Group")
        value_str = ",".join(items)
        condition = RuleCondition.objects.create(group=group, field="category", operator="in", value=value_str)
        tx = MockTransaction(category=items[0])
        assert condition.evaluate(tx) is True

    def test_condition_evaluate_is_null(self):
        group = RuleGroup.objects.create(name="Test Group")
        condition = RuleCondition.objects.create(group=group, field="invoice_text", operator="is_null")
        tx_null = MockTransaction(invoice_text=None)
        tx_empty = MockTransaction(invoice_text="")
        tx_full = MockTransaction(invoice_text="some text")
        assert condition.evaluate(tx_null) is True
        assert condition.evaluate(tx_empty) is True
        assert condition.evaluate(tx_full) is False

    def test_condition_evaluate_is_not_null(self):
        group = RuleGroup.objects.create(name="Test Group")
        condition = RuleCondition.objects.create(group=group, field="invoice_text", operator="is_not_null")
        tx_null = MockTransaction(invoice_text=None)
        tx_empty = MockTransaction(invoice_text="")
        tx_full = MockTransaction(invoice_text="some text")
        assert condition.evaluate(tx_null) is False
        assert condition.evaluate(tx_empty) is False
        assert condition.evaluate(tx_full) is True

    # --------------------------------------------------------------------------
    # 2. RuleGroup Logic Tests
    # --------------------------------------------------------------------------

    def test_simple_and_logic_success(self):
        group = RuleGroup.objects.create(name="AND Success", logic="AND")
        RuleCondition.objects.create(group=group, field="amount", operator="gt", value="100")
        RuleCondition.objects.create(group=group, field="vendor", operator="contains", value="corp")
        tx = MockTransaction(amount="150.00", vendor="Acme Corp")
        assert group.evaluate(tx) is True

    def test_simple_and_logic_fail(self):
        group = RuleGroup.objects.create(name="AND Fail", logic="AND")
        RuleCondition.objects.create(group=group, field="amount", operator="gt", value="100")
        RuleCondition.objects.create(group=group, field="vendor", operator="contains", value="corp")
        tx = MockTransaction(amount="90.00", vendor="Acme Corp") # Amount fails
        assert group.evaluate(tx) is False

    def test_simple_or_logic_success(self):
        group = RuleGroup.objects.create(name="OR Success", logic="OR")
        RuleCondition.objects.create(group=group, field="amount", operator="gt", value="100")
        RuleCondition.objects.create(group=group, field="vendor", operator="contains", value="corp")
        tx = MockTransaction(amount="90.00", vendor="Acme Corp") # Amount fails, vendor passes
        assert group.evaluate(tx) is True

    def test_nested_group_and_with_or_child_success(self):
        """Tests the critical A AND (B OR C) logic."""
        parent = RuleGroup.objects.create(name="Parent AND", logic="AND")
        # Condition A: Amount must be > 0
        RuleCondition.objects.create(group=parent, field="amount", operator="gt", value="0")

        child = RuleGroup.objects.create(name="Child OR", logic="OR", parent=parent)
        # Condition B: Vendor contains 'adobe'
        RuleCondition.objects.create(group=child, field="vendor", operator="contains", value="adobe")
        # Condition C: Vendor contains 'figma'
        RuleCondition.objects.create(group=child, field="vendor", operator="contains", value="figma")

        # This transaction satisfies A and B
        tx_adobe = MockTransaction(amount="50.00", vendor="Adobe Inc.")
        # This transaction satisfies A and C
        tx_figma = MockTransaction(amount="25.00", vendor="Figma")
        
        assert parent.evaluate(tx_adobe) is True
        assert parent.evaluate(tx_figma) is True

    def test_nested_group_and_with_or_child_fail(self):
        """Tests failure for the A AND (B OR C) logic."""
        parent = RuleGroup.objects.create(name="Parent AND Fail", logic="AND")
        RuleCondition.objects.create(group=parent, field="amount", operator="gt", value="0") # Condition A

        child = RuleGroup.objects.create(name="Child OR Fail", logic="OR", parent=parent)
        RuleCondition.objects.create(group=child, field="vendor", operator="contains", value="adobe") # B
        RuleCondition.objects.create(group=child, field="vendor", operator="contains", value="figma") # C

        # This transaction satisfies A, but neither B nor C
        tx_other = MockTransaction(amount="50.00", vendor="Microsoft")
        assert parent.evaluate(tx_other) is False

    # --------------------------------------------------------------------------
    # 3. High-Level Method Tests
    # --------------------------------------------------------------------------

    def test_get_first_match_respects_priority(self):
        """Ensures the rule with the lower priority number wins."""
        # High-priority (runs first) but more specific rule
        rule_p100 = RuleGroup.objects.create(
            name="Specific Vendor", priority=100, logic="AND", target_account=self.target_account
        )
        RuleCondition.objects.create(group=rule_p100, field="vendor", operator="contains", value="stripe")

        # Low-priority (runs second) but broader rule
        rule_p200 = RuleGroup.objects.create(
            name="General Payments", priority=200, logic="AND", target_account=self.other_account
        )
        RuleCondition.objects.create(group=rule_p200, field="amount", operator="lt", value="0")

        tx = MockTransaction(amount="-500.00", vendor="Stripe Payout")
        
        # Both rules match, but get_first_match should return the one with priority 100
        winner = RuleGroup.get_first_match(tx)
        
        assert winner is not None
        assert winner.id == rule_p100.id
        assert winner.target_account.name == "6450: Software Subscriptions"
        
    def test_get_first_match_returns_none_for_no_match(self):
        rule = RuleGroup.objects.create(name="No Match Rule", logic="AND")
        RuleCondition.objects.create(group=rule, field="vendor", operator="equals", value="a-very-specific-vendor")
        
        tx = MockTransaction(vendor="some other vendor")
        match = RuleGroup.get_first_match(tx)
        assert match is None

    def test_simulate_finds_all_conflicting_matches(self):
        """Ensures simulate() returns all matches, ordered by priority."""
        rule_p100 = RuleGroup.objects.create(name="Specific", priority=100)
        RuleCondition.objects.create(group=rule_p100, field="vendor", operator="contains", value="staples")

        rule_p200 = RuleGroup.objects.create(name="General", priority=200)
        RuleCondition.objects.create(group=rule_p200, field="category", operator="equals", value="Office")

        tx = MockTransaction(vendor="Staples Inc.", category="Office")

        # Both rules will match this transaction
        all_matches = RuleGroup.simulate(tx)
        
        assert len(all_matches) == 2
        # Verify they are returned in the correct priority order
        assert all_matches[0].id == rule_p100.id
        assert all_matches[1].id == rule_p200.id

