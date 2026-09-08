import graphene
from graphene_django.filter import DjangoFilterConnectionField
from ledger.gql.customer.types import CustomerNode
from ledger.models.customer import CustomerModel
from ledger.models.entity import EntityModel

class CustomerQuery(graphene.ObjectType):
    get_all_customers = DjangoFilterConnectionField(
        CustomerNode,
        uuid=graphene.String(required=True, description="Slug of the entity to filter customers by")
    )

    def resolve_get_all_customers(self, info, uuid, **kwargs):
        user = info.context.user
        if not user.is_authenticated:
            return CustomerModel.objects.none()
        # Security: only allow customers for entities the user can access
        try:
            entity = EntityModel.objects.for_user(user_model=user).get(uuid=uuid)
        except EntityModel.DoesNotExist:
            return CustomerModel.objects.none()
        return CustomerModel.objects.filter(entity_model=entity).order_by('-updated')