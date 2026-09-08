import graphene
from graphene_django.filter import DjangoFilterConnectionField
from ledger.gql.coa.types import ChartOfAccountModelNode
from ledger.models.chart_of_accounts import ChartOfAccountModel

class ChartOfAccountModelQuery(graphene.ObjectType):
    get_all_chart_of_accounts = DjangoFilterConnectionField(
        ChartOfAccountModelNode,
        entity_uuid=graphene.UUID(required=True, description="UUID of the entity to filter CoAs by")
    )

    def resolve_get_all_chart_of_accounts(self, info, entity_uuid, **kwargs):
        user = info.context.user
        if not user.is_authenticated:
            return ChartOfAccountModel.objects.none()
        # Add your permission logic here if needed
        return ChartOfAccountModel.objects.filter(entity__uuid=entity_uuid).order_by('-updated')