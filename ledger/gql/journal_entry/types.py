import graphene
from graphene_django import DjangoObjectType
from ledger.models.journal_entry import JournalEntryModel


from ledger.models.transactions import TransactionModel
from graphene_django.types import DjangoObjectType

class TransactionNode(DjangoObjectType):
    class Meta:
        model = TransactionModel
        interfaces = (graphene.relay.Node,)
        fields = (
            'uuid',
            'account',
            'amount',
            'tx_type',
            'description',
            'cleared',
            'reconciled',
            'created',
            'updated',
        )

class JournalEntryNode(DjangoObjectType):
    transactions = graphene.List(TransactionNode)

    class Meta:
        model = JournalEntryModel
        interfaces = (graphene.relay.Node,)
        filter_fields = {
            'uuid': ['exact'],
            'je_number': ['exact', 'icontains'],
            'timestamp': ['exact', 'gte', 'lte'],
            'description': ['icontains'],
            'entity_unit__uuid': ['exact'],
            'ledger__uuid': ['exact'],
            'posted': ['exact'],
            'locked': ['exact'],
            'created': ['exact', 'gte', 'lte'],
            'updated': ['exact', 'gte', 'lte'],
        }
        fields = (
            'uuid',
            'je_number',
            'timestamp',
            'description',
            'entity_unit',
            'ledger',
            'posted',
            'locked',
            'created',
            'updated',
        )

    def resolve_transactions(self, info):
        return self.transactionmodel_set.all()