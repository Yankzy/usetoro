import graphene
from graphene_django import DjangoObjectType
from ledger.models import BankAccountModel

# ... existing imports ...

def get_plaid_account(bank_account_model):
    """Helper to get the matching account from plaid_item.metadata['accounts']"""
    try:
        metadata = bank_account_model.plaid_item.metadata
        accounts = metadata.get('accounts', [])
        # Match by mask or another unique identifier
        for acc in accounts:
            if acc.get('mask') == bank_account_model.account_number[-4:]:
                return acc
        return accounts[0] if accounts else None
    except Exception:
        return None

class BalancesType(graphene.ObjectType):
    limit = graphene.Float()
    current = graphene.Float()
    available = graphene.Float()
    iso_currency_code = graphene.String()
    unofficial_currency_code = graphene.String()

class BankAccountModelNode(DjangoObjectType):
    institution = graphene.String()
    mask = graphene.String()
    type = graphene.String()
    subtype = graphene.String()
    balances = graphene.Field(BalancesType)
    name = graphene.String()
    balance = graphene.String()
    icon = graphene.String()

    class Meta:
        model = BankAccountModel
        interfaces = (graphene.relay.Node,)
        fields = '__all__'

    def resolve_institution(self, info):
        try:
            return self.plaid_item.metadata.get('item', {}).get('institution_name')
        except Exception:
            return None

    def resolve_mask(self, info):
        return self.account_number[-4:] if self.account_number else ""

    def resolve_account_number(self, info):
        return self.account_number[-4:] if self.account_number else ""

    def resolve_type(self, info):
        acc = get_plaid_account(self)
        return acc.get('type') if acc else None

    def resolve_subtype(self, info):
        acc = get_plaid_account(self)
        return acc.get('subtype') if acc else None

    def resolve_balances(self, info):
        acc = get_plaid_account(self)
        if not acc or 'balances' not in acc:
            return None
        b = acc['balances']
        return BalancesType(
            limit=b.get('limit'),
            current=b.get('current'),
            available=b.get('available'),
            iso_currency_code=b.get('iso_currency_code'),
            unofficial_currency_code=b.get('unofficial_currency_code'),
        )

    def resolve_name(self, info):
        acc = get_plaid_account(self)
        return acc.get('name') if acc else getattr(self, 'name', None)
    
    
    def resolve_balance(self, info):
        acc = get_plaid_account(self)
        if acc and 'balances' in acc and acc['balances'].get('current') is not None:
            return str(acc['balances']['current'])
        return "0"  # Default value


    def resolve_icon(self, info):
        # You may want to map institution/type to an icon URL or name
        return None
    