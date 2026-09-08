import graphene
from graphene_django.types import DjangoObjectType
from ledger.models.ledger import LedgerModel

class LedgerNode(DjangoObjectType):
    class Meta:
        model = LedgerModel
        interfaces = (graphene.relay.Node,)
        filter_fields = {
            'uuid': ['exact'],
            'name': ['exact', 'icontains'],
            'ledger_xid': ['exact', 'icontains'],
            'entity__uuid': ['exact'],
            'posted': ['exact'],
            'locked': ['exact'],
            'hidden': ['exact'],
            'created': ['exact', 'gte', 'lte'],
            'updated': ['exact', 'gte', 'lte'],
        }
        fields = (
            'uuid',
            'name',
            'ledger_xid',
            'entity',
            'posted',
            'locked',
            'hidden',
            'created',
            'updated',
        )