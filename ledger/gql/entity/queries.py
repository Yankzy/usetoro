import graphene
from graphene_django.filter import DjangoFilterConnectionField
from ledger.gql.entity.types import EntityModelNode
from ledger.gql.entity.types import EntityModelNode
from ledger.models.entity import EntityModel


class EntityQuery(graphene.ObjectType):
    # get_all_entities = DjangoFilterConnectionField(EntityModelNode)
    entity_dashboard = graphene.Field(
        EntityModelNode,
        uuid=graphene.UUID(required=True),
        date=graphene.Date(required=False),
        from_date=graphene.Date(required=False),
        to_date=graphene.Date(required=False),
        description="Get all dashboard data for an entity for a given date or date range"
    )
    get_all_entities = DjangoFilterConnectionField(EntityModelNode)
    get_entity = graphene.Field(
        EntityModelNode,
        uuid=graphene.UUID(required=True)
    )

    def resolve_get_all_entities(self, info):
        try:
            user = info.context.user
            if not user.is_authenticated:
                raise ValueError("User is not authenticated")
            
            return EntityModel.objects.for_user(user)
        except EntityModel.DoesNotExist:
            return None

    def resolve_get_entity(self, info, uuid):
        try:
            return EntityModel.objects.get(uuid=uuid)
        except EntityModel.DoesNotExist:
            return None

    def resolve_entity_dashboard(self, info, uuid, date=None, from_date=None, to_date=None):
        user = info.context.user
        if not user.is_authenticated:
            return None
        try:
            entity = EntityModel.objects.get(uuid=uuid)
            if not (user.is_superuser or entity.admin_id == user.pk or entity.managers.filter(id=user.pk).exists()):
                return None
            entity._dashboard_date = date
            entity._dashboard_from_date = from_date
            entity._dashboard_to_date = to_date
            return entity
        except EntityModel.DoesNotExist:
            return None