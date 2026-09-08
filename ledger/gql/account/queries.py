
import graphene
from graphene_django.filter import DjangoFilterConnectionField
from ledger.models import AccountModel
from ledger.gql.account.types import AccountNode




class AccountQuery(graphene.ObjectType):
    get_all_accounts = DjangoFilterConnectionField(
        AccountNode, 
        entity_slug=graphene.String(required=True, description="Slug name of the entity to filter accounts by")
    )
    get_one_account = graphene.Field(
        AccountNode,
        uuid=graphene.UUID(required=True),
        description="Get a single account by UUID"
    )

    def resolve_get_one_account(self, info, uuid):
        from ledger.models.accounts import AccountModel
        if not info.context.user.is_authenticated:
            return None
        try:
            # Security: only allow if user has access
            account = AccountModel.objects.get(uuid=uuid)
            entity = account.coa_model.entity
            user = info.context.user
            if not (user.is_superuser or entity.admin_id == user.pk or entity.managers.filter(id=user.pk).exists()):
                return None
            return account
        except AccountModel.DoesNotExist:
            return None

    def resolve_get_all_accounts(self, info, entity_slug, **kwargs):
        if info.context.user.is_authenticated:
            all_accounts =  AccountModel.objects.for_entity(
                entity_model=entity_slug,
                user_model=info.context.user,
            )
            return all_accounts.filter(active=True).select_related('coa_model').order_by('code')
        else:
            return AccountModel.objects.none()

