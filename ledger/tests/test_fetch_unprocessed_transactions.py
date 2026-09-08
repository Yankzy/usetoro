# from ledger.bookkeeping_agents.tools import LocalContext, fetch_unprocessed_plaid_transactions
# from agents import RunContextWrapper
# from hypothesis.extra.django import TestCase as HypothesisTestCase
# from hypothesis import given, settings, HealthCheck
# from hypothesis import strategies as st
# from asgiref.sync import sync_to_async
# from ledger.models import PlaidTransaction, BankAccountModel, EntityModel
# import uuid

# # Strategies
# _is_current = st.booleans()
# _is_removed = st.booleans()
# _entity_uuid = st.uuids()


# # Pydantic configuration
# class Config:
#     arbitrary_types_allowed = True

# class FetchUnprocessedPlaidTransactionsHypothesisTests(HypothesisTestCase):
#     # pytest -q ledger-be_py/ledger/tests/test_fetch_unprocessed_transactions.py::FetchUnprocessedPlaidTransactionsHypothesisTests

#     def setUp(self):
#         # Create a user and entity for testing
#         self.entity = EntityModel.objects.create(uuid=uuid.uuid4())
#         self.bank_account = BankAccountModel.objects.create(entity_model=self.entity)

#     @settings(max_examples=20, suppress_health_check=[HealthCheck.too_slow])
#     @given(is_current=_is_current, is_removed=_is_removed)
#     def test_fetch_unprocessed_transactions(self, is_current, is_removed):
#         # Create a PlaidTransaction instance
#         plaid_transaction = PlaidTransaction.objects.create(
#             is_current=is_current,
#             is_removed=is_removed,
#             plaid_item__bankaccountmodel=self.bank_account
#         )

#         # Set up the context
#         context = LocalContext(entity_uuid=str(self.entity.uuid))
#         wrapper = RunContextWrapper(context)

#         # Run the function
#         result = sync_to_async(fetch_unprocessed_plaid_transactions)(wrapper)

#         # Check the result
#         if is_current and not is_removed:
#             self.assertIn(plaid_transaction, result['transactions'])
#         else:
#             self.assertNotIn(plaid_transaction, result['transactions'])