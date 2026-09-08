import graphene
from graphene import relay
from graphene_django import DjangoObjectType
from ledger.models.customer import CustomerModel

class CustomerNode(DjangoObjectType):
    class Meta:
        model = CustomerModel
        interfaces = (relay.Node,)
        filter_fields = {
            'customer_number': ['exact', 'icontains'],
            'customer_name': ['exact', 'icontains'],
            'email': ['exact', 'icontains'],
            'active': ['exact'],
            'city': ['exact', 'icontains'],
            'state': ['exact', 'icontains'],
            'country': ['exact', 'icontains'],
            'zip_code': ['exact', 'icontains'],
            'description': ['exact', 'icontains'],
        }
        fields = [
            'id',
            'created',
            'updated',
            'address_1',
            'address_2',
            'city',
            'state',
            'zip_code',
            'country',
            'email',
            'website',
            'phone',
            'sales_tax_rate',
            'uuid',
            'customer_name',
            'customer_number',
            'entity_model',
            'description',
            'active',
            'hidden',
            'additional_info'
        ]