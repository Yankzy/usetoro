import graphene
from graphene_django.filter import DjangoFilterConnectionField
from ledger.gql.journal_entry.types import JournalEntryNode
from ledger.models.journal_entry import JournalEntryModel
from ledger.models.ledger import LedgerModel
from ledger.models.transactions import TransactionModel
from ledger.gql.account.types import TransactionNode
from ledger.io.roles import (
    GROUP_EXPENSES, GROUP_ASSETS, GROUP_LIABILITIES, GROUP_INCOME, GROUP_COGS, GROUP_CAPITAL
)
import logging



logger = logging.getLogger(__name__)


ACCOUNT_TYPE_TO_GROUP = {
    "expense": GROUP_EXPENSES,
    "asset": GROUP_ASSETS,
    "liability": GROUP_LIABILITIES,
    "income": GROUP_INCOME,
    "cogs": GROUP_COGS,
    "capital": GROUP_CAPITAL,
}


class TransactionQuery(graphene.ObjectType):
    
    transactions_by_type = graphene.List(
        TransactionNode,
        description="List all transactions for specified high-level account types",
        entity_slug=graphene.String(required=True),
        account_types=graphene.List(
            graphene.String,
            required=True,
            description="One or more of: expense, asset, liability, income, cogs, capital"
        ),
    )

        
    def resolve_transactions_by_type(self, info, account_types, entity_slug, **kwargs):
        user = info.context.user
        if not user.is_authenticated:
            raise ValueError("User is not is_authenticated")
        
        # Normalize and validate input
        valid_types = {k for k in ACCOUNT_TYPE_TO_GROUP}
        selected_types = [t.lower() for t in account_types if t.lower() in valid_types]
        if not selected_types:
            raise ValueError(f"No matching query for {account_types}")
        # Flatten all roles for selected types
        roles = []
        for t in selected_types:
            roles.extend(ACCOUNT_TYPE_TO_GROUP[t])
        # Use for_entity to filter by entity, then by roles
        return TransactionModel.objects.for_entity(entity_slug, user_model=user).filter(account__role__in=roles)



class JournalEntryQuery(graphene.ObjectType):
    get_all_journal_entries = DjangoFilterConnectionField(
        JournalEntryNode,
        ledger_uuid=graphene.UUID(required=True, description="Ledger UUID to filter journal entries by")
    )

    get_journal_entries_by_year = graphene.List(
        JournalEntryNode,
        ledger_uuid=graphene.UUID(required=True, description="Ledger UUID"),
        year=graphene.Int(required=True, description="Year to filter journal entries by"),
        description="List all journal entries for a given ledger and year"
    )

    get_journal_entry = graphene.Field(
        JournalEntryNode,
        uuid=graphene.UUID(required=True, description="UUID of the journal entry"),
        description="Get a single journal entry by UUID"
    )

    get_journal_entries_by_month = graphene.List(
        JournalEntryNode,
        ledger_uuid=graphene.UUID(required=True, description="Ledger UUID"),
        year=graphene.Int(required=True, description="Year"),
        month=graphene.Int(required=True, description="Month (1-12)"),
        description="List all journal entries for a given ledger, year, and month"
    )

     
    test_do_cd = graphene.String(description="Test query class")
    def resolve_ttest_do_cd(self, info):
        return "do CD works!"

    def resolve_get_journal_entries_by_month(self, info, ledger_uuid, year, month, **kwargs):
        user = info.context.user
        if not user.is_authenticated:
            raise ValueError("User is not is_authenticated")
        try:
            ledger = LedgerModel.objects.get(uuid=ledger_uuid)
            entity = ledger.entity
            if not (user.is_superuser or entity.admin_id == user.pk or entity.managers.filter(id=user.pk).exists()):
                return []
            return JournalEntryModel.objects.filter(
                ledger=ledger,
                timestamp__year=year,
                timestamp__month=month
            ).order_by('-timestamp')
        except LedgerModel.DoesNotExist:
            return []

    def resolve_get_journal_entry(self, info, uuid, **kwargs):
        user = info.context.user
        if not user.is_authenticated:
            raise ValueError("User is not is_authenticated")
        try:
            je = JournalEntryModel.objects.get(uuid=uuid)
            entity = je.ledger.entity
            if not (user.is_superuser or entity.admin_id == user.pk or entity.managers.filter(id=user.pk).exists()):
                return None
            return je
        except JournalEntryModel.DoesNotExist:
            return None

    def resolve_get_all_journal_entries(self, info, ledger_uuid, **kwargs):
        user = info.context.user
        if not user.is_authenticated:
            raise ValueError("User is not is_authenticated")
        try:
            ledger = LedgerModel.objects.get(uuid=ledger_uuid)
            entity = ledger.entity
            if not (user.is_superuser or entity.admin_id == user.pk or entity.managers.filter(id=user.pk).exists()):
                return JournalEntryModel.objects.none()
            return JournalEntryModel.objects.filter(ledger=ledger).order_by('-timestamp')
        except LedgerModel.DoesNotExist:
            return JournalEntryModel.objects.none()

    def resolve_get_journal_entries_by_year(self, info, ledger_uuid, year, **kwargs):
        user = info.context.user
        if not user.is_authenticated:
            raise ValueError("User is not is_authenticated")
        try:
            ledger = LedgerModel.objects.get(uuid=ledger_uuid)
            entity = ledger.entity
            if not (user.is_superuser or entity.admin_id == user.pk or entity.managers.filter(id=user.pk).exists()):
                return []
            return JournalEntryModel.objects.filter(
                ledger=ledger,
                timestamp__year=year
            ).order_by('-timestamp')
        except LedgerModel.DoesNotExist:
            return []