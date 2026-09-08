from decimal import Decimal
from hypothesis.extra.django import TestCase as HypothesisTestCase
from hypothesis import given, settings, HealthCheck
from hypothesis import strategies as st
from django.contrib.auth import get_user_model

from ledger.models.plaid import (
    PlaidTransaction,
    PlaidItem,
    Counterparty,
    TransactionAuditLog,
)
from core.models import LanguageModel
import uuid

User = get_user_model()


def _tx_strategy():
    """Generate Plaid-like transaction dicts suitable for our bulk helpers."""
    txid_re = st.from_regex(r'^[A-Za-z0-9_-]{5,20}$', fullmatch=True)
    merchant_id_re = st.from_regex(r'^[A-Za-z0-9_-]{3,12}$', fullmatch=True)
    merchant_meta = st.one_of(st.just({}), st.fixed_dictionaries({'merchant_id': merchant_id_re}))
    return st.fixed_dictionaries({
        'transaction_id': txid_re,
        'account_id': st.text(min_size=5, max_size=20),
        'amount': st.floats(min_value=-10000, max_value=10000, allow_nan=False, allow_infinity=False),
        'iso_currency_code': st.sampled_from(['USD', 'EUR', 'GBP']),
        'date': st.dates(),
        'name': st.text(min_size=1, max_size=60),
        'merchant_name': st.one_of(st.none(), st.text(min_size=1, max_size=40)),
        'payment_channel': st.sampled_from(['online', 'in store', 'other']),
        'pending': st.booleans(),
        'transaction_type': st.sampled_from(['digital', 'place', 'special', 'unresolved']),
        'payment_meta': st.just({}),
        'merchant_metadata': merchant_meta,
    })

class PlaidTransactionManagerTests(HypothesisTestCase):
    """
    - Run the class:
        pytest -q ledger-be_py/ledger/tests/test_plaid_item_manager.py::PlaidTransactionManagerTests
    - Run single method:
        pytest -q ledger-be_py/ledger/tests/test_plaid_transaction_manager.py::PlaidTransactionManagerTests::test_counterparty_creation_and_attachment
    """

    def setUp(self):
        LanguageModel.objects.get_or_create(code='en', defaults={'name': 'English', 'sort_order': 1})
        email = f"plaid+{uuid.uuid4().hex}@example.com"
        self.user = User.objects.create_user(email=email, password='pass')
        self.plaid_item = PlaidItem.objects.create(user=self.user, item_id='test_item')

    @settings(max_examples=8, suppress_health_check=[HealthCheck.too_slow])
    @given(st.lists(_tx_strategy(), min_size=3, max_size=5, unique_by=lambda d: d['transaction_id']))
    def test_bulk_create_prevents_duplicates_on_repeat_calls(self, tx_list):
        # First call should create all transactions
        created_first = PlaidTransaction.objects.bulk_create_from_plaid_data(tx_list, plaid_item=self.plaid_item)
        assert len(created_first) == len(tx_list)
        total_after_first = PlaidTransaction.objects.filter(plaid_item=self.plaid_item).count()
        assert total_after_first == len(tx_list)

        # Second call with the same payload should not create duplicates
        created_second = PlaidTransaction.objects.bulk_create_from_plaid_data(tx_list, plaid_item=self.plaid_item)
        assert created_second == [] or len(created_second) == 0
        total_after_second = PlaidTransaction.objects.filter(plaid_item=self.plaid_item).count()
        assert total_after_second == total_after_first

    @settings(max_examples=8, suppress_health_check=[HealthCheck.too_slow])
    @given(st.lists(_tx_strategy(), min_size=2, max_size=4, unique_by=lambda d: d['transaction_id']))
    def test_counterparty_creation_and_attachment(self, tx_list):
        # Ensure some tx have merchant_metadata with merchant_id
        for i, pd in enumerate(tx_list):
            if i % 2 == 0:
                pd['merchant_metadata'] = {'merchant_id': f"m-{pd['transaction_id'][:8]}"}

        created = PlaidTransaction.objects.bulk_create_from_plaid_data(tx_list, plaid_item=self.plaid_item)
        assert len(created) == len(tx_list)

        # Counterparties created and attached for merchant_ids
        mids = { (pd.get('merchant_metadata') or {}).get('merchant_id') for pd in tx_list }
        mids = {m for m in mids if m}
        for mid in mids:
            # single Counterparty row per merchant_id
            assert Counterparty.objects.filter(entity_id=mid).count() == 1
            # at least one transaction is attached to it
            assert PlaidTransaction.objects.filter(counterparties__entity_id=mid, plaid_item=self.plaid_item).exists()

        # Re-run with same payload: no additional Counterparty rows created
        PlaidTransaction.objects.bulk_create_from_plaid_data(tx_list, plaid_item=self.plaid_item)
        for mid in mids:
            assert Counterparty.objects.filter(entity_id=mid).count() == 1

    def test_skips_payloads_without_transaction_id(self):
        valid = {
            'transaction_id': 'validtx123',
            'account_id': 'acct12345',
            'amount': 12.3456,
            'iso_currency_code': 'USD',
            'date': '2020-01-01',
            'name': 'Valid',
            'merchant_metadata': {},
            'payment_channel': 'online',
            'pending': False,
            'transaction_type': 'digital',
            'payment_meta': {},
        }
        invalid = {
            # missing transaction_id should be ignored
            'account_id': 'acct99999',
            'amount': 1.23,
            'iso_currency_code': 'USD',
            'date': '2020-01-02',
            'name': 'Invalid',
        }
        created = PlaidTransaction.objects.bulk_create_from_plaid_data([valid, invalid], plaid_item=self.plaid_item)
        assert len(created) == 1
        assert PlaidTransaction.objects.filter(transaction_id='validtx123', plaid_item=self.plaid_item).exists()

    def test_amounts_are_stored_as_two_decimal_places(self):
        payload = [{
            'transaction_id': 'roundtx1',
            'account_id': 'acct-round',
            'amount': 1.23789,
            'iso_currency_code': 'USD',
            'date': '2020-01-01',
            'name': 'RoundTest',
            'merchant_metadata': {},
            'payment_channel': 'online',
            'pending': False,
            'transaction_type': 'digital',
            'payment_meta': {},
        }]
        created = PlaidTransaction.objects.bulk_create_from_plaid_data(payload, plaid_item=self.plaid_item)
        assert len(created) == 1
        tx = PlaidTransaction.objects.get(transaction_id='roundtx1', plaid_item=self.plaid_item)
        # Amount should be quantized to 2 decimal places
        expected = Decimal(str(payload[0]['amount'])).quantize(Decimal("0.01"))
        assert tx.amount == expected

    @settings(max_examples=8, suppress_health_check=[HealthCheck.too_slow])
    @given(st.lists(_tx_strategy(), min_size=2, max_size=4, unique_by=lambda d: d['transaction_id']))
    def test_bulk_update_versions_existing_transactions(self, initial_tx_list):
        # Setup: create initial rows
        created = PlaidTransaction.objects.bulk_create_from_plaid_data(initial_tx_list, plaid_item=self.plaid_item)
        originals = {t.transaction_id: t for t in created}

        # Prepare modified payloads based on the initial list (increase amount and add merchant id)
        modified_payloads = []
        for pd in initial_tx_list:
            new_pd = dict(pd)  # shallow copy
            # mutate amount and merchant metadata to force an update/version
            amt = float(pd['amount']) if pd['amount'] is not None else 0.0
            new_pd['amount'] = amt + 1.23
            # ensure merchant_metadata present to test counterparty handling on update
            merchant_meta = new_pd.get('merchant_metadata') or {}
            merchant_meta['merchant_id'] = f"upd-{pd['transaction_id'][:8]}"
            new_pd['merchant_metadata'] = merchant_meta
            modified_payloads.append(new_pd)

        # Act: perform bulk update (versioning expected)
        updated = PlaidTransaction.objects.bulk_update_from_plaid_data(modified_payloads, plaid_item=self.plaid_item, sync_id='test-sync')

        # Assert: each original should have at least one child version (previous_version set)
        assert len(updated) == len(modified_payloads)
        for pd in modified_payloads:
            orig = originals.get(pd['transaction_id'])
            assert orig is not None
            # There should be a versioned transaction pointing to original
            child_qs = PlaidTransaction.objects.filter(previous_version=orig)
            assert child_qs.exists()
            child = child_qs.order_by('-version').first()
            expected_amount = Decimal(str(pd['amount'])).quantize(Decimal("0.01"))
            assert child.amount == expected_amount
            # Confirm counterparty created/attached for merchant_id
            mid = pd['merchant_metadata'].get('merchant_id')
            if mid:
                assert Counterparty.objects.filter(entity_id=mid).exists()
                assert child.counterparties.filter(entity_id=mid).exists()
            # Confirm audit log entry recorded with sync_id
            assert TransactionAuditLog.objects.filter(transaction=child, plaid_sync_id='test-sync').exists()

    def test_bulk_update_ignores_missing_transaction_id_and_noops_when_no_changes(self):
        # Create one initial tx
        initial = {
            'transaction_id': 'updatetx1',
            'account_id': 'acct1',
            'amount': 10.0,
            'iso_currency_code': 'USD',
            'date': '2020-01-01',
            'name': 'Init',
            'merchant_metadata': {},
            'payment_channel': 'online',
            'pending': False,
            'transaction_type': 'digital',
            'payment_meta': {},
        }
        PlaidTransaction.objects.bulk_create_from_plaid_data([initial], plaid_item=self.plaid_item)

        # Payload with missing transaction_id should be ignored and return empty list
        res = PlaidTransaction.objects.bulk_update_from_plaid_data([{'amount': 1.0}], plaid_item=self.plaid_item, sync_id='s1')
        assert res == [] or len(res) == 0

        # Payload with identical data should result in no updates
        same_payload = dict(initial)
        res2 = PlaidTransaction.objects.bulk_update_from_plaid_data([same_payload], plaid_item=self.plaid_item, sync_id='s2')
        assert res2 == [] or len(res2) == 0


        