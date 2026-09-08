import graphene
import logging
from ledger.gql.bank_account.types import BankAccountModelNode
from graphql_jwt.decorators import login_required
from ledger.models import BankAccountModel



logger = logging.getLogger(__name__)




class BankAccountQuery(graphene.ObjectType):
    ledger_bank_accounts = graphene.List(BankAccountModelNode, description="List all bank accounts for the current user")


    @login_required
    def resolve_ledger_bank_accounts(self, info):
        user = info.context.user

        # Create cash accounts for ledger and budgeting
        BankAccountModel.get_or_create_cash_account(user)
        return BankAccountModel.objects.filter(user=user)
