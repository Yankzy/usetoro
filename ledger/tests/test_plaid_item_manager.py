from hypothesis.extra.django import TestCase as HypothesisTestCase
from django.contrib.auth import get_user_model

from hypothesis import given, settings, HealthCheck, assume
from hypothesis import strategies as st

from ledger.models.plaid import PlaidItem
from core.models import LanguageModel
import uuid

User = get_user_model()

# Strategies
_item_id = st.from_regex(r'^[A-Za-z0-9_-]{5,30}$', fullmatch=True)
_token = st.from_regex(r'^[A-Za-z0-9_-]{5,40}$', fullmatch=True)
_account_id = st.from_regex(r'^[A-Za-z0-9_-]{5,30}$', fullmatch=True)
_module_key = st.one_of(st.none(), st.from_regex(r'^[A-Za-z0-9_-]{1,20}$', fullmatch=True))
_institution_name = st.text(min_size=1, max_size=40)

_accounts_list = st.lists(st.fixed_dictionaries({'account_id': _account_id}), min_size=0, max_size=4)
_metadata = st.builds(lambda accts, name: {'accounts': accts, 'institution': {'name': name}}, _accounts_list, _institution_name)


class PlaidItemManagerHypothesisTests(HypothesisTestCase):
    """
    - Run the class:
        pytest -q ledger-be_py/ledger/tests/test_plaid_item_manager.py::PlaidItemManagerHypothesisTests
    - Run single method:
        pytest -q ledger-be_py/ledger/tests/test_plaid_item_manager.py::PlaidItemManagerHypothesisTests::test_creates_new_plaid_item_when_no_match
    """
    def setUp(self):
        # Ensure LanguageModel exists like other tests in repo
        LanguageModel.objects.get_or_create(code='en', defaults={'name': 'English', 'sort_order': 1})
        email = f"plaid+{uuid.uuid4().hex}@example.com"
        self.user = User.objects.create_user(email=email, password='pass')

    @settings(max_examples=20, suppress_health_check=[HealthCheck.too_slow])
    @given(item_id=_item_id, token=_token, module_key=_module_key, metadata=_metadata)
    def test_creates_new_plaid_item_when_no_match(self, item_id, token, module_key, metadata):
        # Act
        plaid_item, created = PlaidItem.objects.get_or_create_from_metadata(
            item_id=item_id,
            access_token=token,
            user=self.user,
            metadata=metadata,
            module_key=module_key
        )

        # Assert created and fields persisted
        assert created is True
        assert PlaidItem.objects.filter(pk=plaid_item.pk).exists()
        assert plaid_item.item_id == item_id
        assert plaid_item.access_token == token
        assert plaid_item.user == self.user
        assert plaid_item.metadata == metadata

        # module_keys handling
        if module_key:
            assert plaid_item.module_keys == [module_key.lower()]
        else:
            assert plaid_item.module_keys == []

    @settings(max_examples=15, suppress_health_check=[HealthCheck.too_slow])
    @given(item_id=_item_id, old_token=_token, new_token=_token, old_meta=_metadata, new_meta=_metadata, module_key=_module_key)
    def test_exact_item_id_match_updates_and_returns_existing(self, item_id, old_token, new_token, old_meta, new_meta, module_key):
        # Setup: create a different user and initial PlaidItem with same item_id
        other_email = f"plaid-old+{uuid.uuid4().hex}@example.com"
        other_user = User.objects.create_user(email=other_email, password='pass')
        initial = PlaidItem.objects.create(
            user=other_user,
            item_id=item_id,
            access_token=old_token,
            metadata=old_meta,
            module_keys=[]
        )

        # Act: call with same item_id but a different user and updated token/meta
        returned, created = PlaidItem.objects.get_or_create_from_metadata(
            item_id=item_id,
            access_token=new_token,
            user=self.user,
            metadata=new_meta,
            module_key=module_key
        )

        # Assert it's the same DB row (no new create) and updated
        assert created is False
        assert returned.pk == initial.pk
        returned.refresh_from_db()
        assert returned.access_token == new_token
        assert returned.user == self.user
        assert returned.metadata == new_meta

        # module_keys appended if provided
        if module_key:
            assert module_key.lower() in returned.module_keys

    @settings(max_examples=20, suppress_health_check=[HealthCheck.too_slow])
    @given(
        old_item=_item_id,
        new_item=_item_id,
        shared_acc=_account_id,
        other_acc=_account_id,
        token=_token,
        module_key=_module_key
    )
    def test_adopts_existing_item_on_account_id_overlap(self, old_item, new_item, shared_acc, other_acc, token, module_key):
        # Ensure we exercise the account-adoption branch (item_ids must differ)
        assume(old_item != new_item)

        # Setup existing PlaidItem for same user with accounts including shared_acc
        existing_meta = {'accounts': [{'account_id': shared_acc}, {'account_id': other_acc}], 'institution': {'name': 'X'}}
        existing = PlaidItem.objects.create(
            user=self.user,
            item_id=old_item,
            access_token='old-token',
            metadata=existing_meta,
            module_keys=[]
        )

        # Act: call with a different item_id but metadata that shares account_id
        incoming_meta = {'accounts': [{'account_id': shared_acc}], 'institution': {'name': 'Y'}}
        returned, created = PlaidItem.objects.get_or_create_from_metadata(
            item_id=new_item,
            access_token=token,
            user=self.user,
            metadata=incoming_meta,
            module_key=module_key
        )

        # Assert adopted (no create), same DB row, and item_id/access_token/metadata updated
        assert created is False
        assert returned.pk == existing.pk
        returned.refresh_from_db()
        assert returned.item_id == new_item
        assert returned.access_token == token
        assert returned.metadata == incoming_meta
        if module_key:
            assert module_key.lower() in returned.module_keys