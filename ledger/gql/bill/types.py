import graphene
from graphene_django import DjangoObjectType
from ledger.models.bill import BillModel

class BillModelNode(DjangoObjectType):
    is_paid = graphene.Boolean()

    class Meta:
        model = BillModel
        interfaces = (graphene.relay.Node,)
        filter_fields = {
            'uuid': ['exact'],
            'bill_number': ['exact', 'icontains'],
            'amount_due': ['exact', 'gte', 'lte'],
            'date_due': ['exact', 'gte', 'lte'],
            'vendor__uuid': ['exact'],
            'entity__uuid': ['exact'],
            'created': ['exact', 'gte', 'lte'],
            'updated': ['exact', 'gte', 'lte'],
        }
        fields = (
            'uuid',
            'bill_number',
            'amount_due',
            'date_due',
            'created',
            'updated',
            # Add other fields as needed
        )

    def resolve_is_paid(self, info):
        return bool(self.is_paid())