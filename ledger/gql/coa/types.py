import graphene
from graphene_django import DjangoObjectType
from ledger.models.chart_of_accounts import ChartOfAccountModel

class ChartOfAccountModelNode(DjangoObjectType):
    class Meta:
        model = ChartOfAccountModel
        interfaces = (graphene.relay.Node,)
        filter_fields = {
            'uuid': ['exact'],
            'name': ['exact', 'icontains'],
            'active': ['exact'],
            'entity__uuid': ['exact'],
            'updated': ['exact', 'gte', 'lte'],
        }
        fields = (
            'uuid',
            'name',
            'active',
            'entity',
            'created',
            'updated',
        )