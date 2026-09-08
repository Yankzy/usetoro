import graphene
from graphene import relay
from graphene_django import DjangoObjectType
from ledger.models import TransactionModel, AccountModel
from datetime import date, timedelta
import calendar


class TransactionPeriodInput(graphene.InputObjectType):
    year = graphene.Int(description="Year for filtering transactions")
    quarter = graphene.Int(description="Quarter (1-4) for filtering transactions")
    month = graphene.Int(description="Month (1-12) for filtering transactions")

class TransactionNode(DjangoObjectType):
    class Meta:
        model = TransactionModel
        interfaces = (relay.Node,)
        filter_fields = {
            'uuid': ['exact'],
            'tx_type': ['exact'],
            'amount': ['exact', 'gte', 'lte'],
            'cleared': ['exact'],
            'reconciled': ['exact'],
            'account__uuid': ['exact'],
            'account__code': ['exact', 'icontains'],
            'account__name': ['exact', 'icontains'],
            'account__role': ['exact'],
            'journal_entry__uuid': ['exact'],
            'journal_entry__timestamp': ['exact', 'gte', 'lte'],
            # Add more as needed
        }
        fields = (
            'uuid',
            'tx_type',
            'amount',
            'description',
            'cleared',
            'reconciled',
            'account',
            'journal_entry',
        )

    # Expose related and computed fields for convenience
    timestamp = graphene.DateTime()
    account_code = graphene.String()
    account_name = graphene.String()
    account_role = graphene.String()
    entity_unit = graphene.String()
    activity = graphene.String()
    ledger_uuid = graphene.UUID()
    entity_slug = graphene.String()

    def resolve_timestamp(self, info):
        return self.journal_entry.timestamp if self.journal_entry else None

    def resolve_account_code(self, info):
        return self.account.code if self.account else None

    def resolve_account_name(self, info):
        return self.account.name if self.account else None

    def resolve_account_role(self, info):
        return self.account.role if self.account else None

    def resolve_entity_unit(self, info):
        return self.journal_entry.entity_unit.slug if self.journal_entry and self.journal_entry.entity_unit else None

    def resolve_activity(self, info):
        return self.journal_entry.activity if self.journal_entry else None

    def resolve_ledger_uuid(self, info):
        return self.journal_entry.ledger.uuid if self.journal_entry and self.journal_entry.ledger else None

    def resolve_entity_slug(self, info):
        return self.journal_entry.ledger.entity.slug if self.journal_entry and self.journal_entry.ledger and self.journal_entry.ledger.entity else None
    


class AccountNode(DjangoObjectType):
    class Meta:
        model = AccountModel
        interfaces = (relay.Node,)
        filter_fields = {
            'uuid': ['exact'],
            'code': ['exact', 'icontains', 'istartswith'],
            'name': ['exact', 'icontains', 'istartswith'],
            'role': ['exact'],
            'balance_type': ['exact'],
            'active': ['exact'],
            'locked': ['exact'],
            'coa_model__uuid': ['exact'],
        }
        # Only expose safe fields
        fields = (
            'uuid',
            'code',
            'name',
            'role',
            'role_default',
            'balance_type',
            'locked',
            'active',
            'coa_model',
        )

    # Expose UUID as id for Relay
    @classmethod
    def get_node(cls, info, id):
        try:
            return cls._meta.model.objects.get(uuid=id)
        except cls._meta.model.DoesNotExist:
            return None

    # Optionally, expose computed properties as read-only fields
    is_asset = graphene.Boolean()
    is_liability = graphene.Boolean()
    is_income = graphene.Boolean()
    is_expense = graphene.Boolean()
    is_cogs = graphene.Boolean()
    is_capital = graphene.Boolean()
    transactions = graphene.List(
        TransactionNode,
        period=TransactionPeriodInput(required=False),
        from_date=graphene.Date(required=False),
        to_date=graphene.Date(required=False),
        description="Transactions for this account filtered by year, quarter, month, or custom date range"
    )

    def resolve_transactions(self, info, period=None, from_date=None, to_date=None):
        qs = self.transactionmodel_set.all().not_closing_entry().posted().order_by('journal_entry__timestamp')
        if from_date:
            qs = qs.filter(journal_entry__timestamp__gte=from_date)
        if to_date:
            qs = qs.filter(journal_entry__timestamp__lte=to_date)
        
        # Default: current year, all months
        today = date.today()
        year = today.year
        start = date(year, 1, 1)
        end = date(year, 12, 31)

        if period:
            if period.year:
                year = period.year
            if period.quarter:
                if period.quarter not in [1, 2, 3, 4]:
                    raise ValueError("quarter must be 1, 2, 3, or 4")
                start_month = 3 * (period.quarter - 1) + 1
                end_month = start_month + 2
                start = date(year, start_month, 1)
                if end_month == 12:
                    end = date(year, 12, 31)
                else:
                    end = date(year, end_month + 1, 1) - timedelta(days=1)
            elif period.month:
                if not (1 <= period.month <= 12):
                    raise ValueError("month must be between 1 and 12")
                start = date(year, period.month, 1)
                last_day = calendar.monthrange(year, period.month)[1]
                end = date(year, period.month, last_day)
            else:
                # Only year provided
                start = date(year, 1, 1)
                end = date(year, 12, 31)

        return self.transactionmodel_set.filter(
            journal_entry__timestamp__gte=start,
            journal_entry__timestamp__lte=end
        ).not_closing_entry().posted().order_by('journal_entry__timestamp')
        return qs
    
    def resolve_is_asset(self, info):
        return self.is_asset()

    def resolve_is_liability(self, info):
        return self.is_liability()

    def resolve_is_income(self, info):
        return self.is_income()

    def resolve_is_expense(self, info):
        return self.is_expense()

    def resolve_is_cogs(self, info):
        return self.is_cogs()

    def resolve_is_capital(self, info):
        return self.is_capital()
    
