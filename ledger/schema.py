import graphene
from ledger.gql import (
    CreateAccount, 
    UpdateAccount, 
    AccountStateMutation,
    CreateChartOfAccountModel, 
    UpdateChartOfAccountModel, 
    CoaStateMutation,
    CreateEntityModel, 
    UpdateEntityModel, 
    EntityStateMutation,
    EntityQuery,
    AccountQuery,
    UpdateJournalEntry, 
    JournalEntryStateMutation, 
    CreateJournalEntryWithTransactions,
    JournalEntryQuery,
    ChartOfAccountModelQuery,
    CustomerQuery,
    CreateCustomer,
    TransactionQuery,
    CreateLinkToken, 
    ExchangePublicToken, 
    GetTransactions,
    PlaidQuery,
    BankAccountQuery,
    CreateImportedJob,
)
from core.schema import (
    signal_mutation_module_validate, 
    signal_mutation_module_before_mutating, 
    signal_mutation_module_after_mutating
)
import logging 



logger = logging.getLogger(__name__)


class Query(
    EntityQuery,
    AccountQuery,
    JournalEntryQuery,
    ChartOfAccountModelQuery,
    CustomerQuery,
    TransactionQuery,
    PlaidQuery,
    BankAccountQuery,
): ...
    
    
class Mutation(graphene.ObjectType): 
    ledger_account_create = CreateAccount.Field()
    ledger_account_update = UpdateAccount.Field()
    ledger_account_state_mutation = AccountStateMutation.Field()
    coa_account_create = CreateChartOfAccountModel.Field()
    coa_account_update = UpdateChartOfAccountModel.Field()
    coa_state_mutation = CoaStateMutation.Field()
    entity_create = CreateEntityModel.Field()
    entity_update = UpdateEntityModel.Field()
    entity_state_mutation = EntityStateMutation.Field()
    journal_entry_create = CreateJournalEntryWithTransactions.Field()
    journal_entry_update = UpdateJournalEntry.Field()
    journal_entry_state_mutation = JournalEntryStateMutation.Field()
    customer_create = CreateCustomer.Field()
    create_link_token = CreateLinkToken.Field()
    exchange_public_token = ExchangePublicToken.Field()
    get_transactions = GetTransactions.Field()
    create_imported_job = CreateImportedJob.Field()



def on_mutation(sender, **kwargs):
    logger.info(f"===MUTATION DATA IS: {kwargs}")


# use this for signals within module
def bind_signals():
    signal_mutation_module_after_mutating["ledger"].connect(on_mutation)
