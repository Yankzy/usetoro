import graphene
from ledger.models.chart_of_accounts import ChartOfAccountModel
from ledger.gql.account.types import AccountNode
from core.schema import BaseMutation
from django.db import transaction
from ledger.gql.coa.types import ChartOfAccountModelNode
from ledger.models.entity import EntityModel



class CreateChartOfAccountModel(BaseMutation):
    """
    Create a new Chart of Account Model (CoA) for an entity.
    """
    _mutation_module = "ledger"
    _mutation_class = "CreateChartOfAccountModel"
    async_mutations = False

    coa_model = graphene.Field(ChartOfAccountModelNode, description="The created Chart of Account Model")
    

    class Input(BaseMutation.Input):
        entity_uuid = graphene.UUID(required=True, description="UUID of the entity")
        name = graphene.String(required=True, description="Name of the Chart of Accounts")
        active = graphene.Boolean(required=False, default_value=True)

    @classmethod
    def async_mutate(cls, user, **kwargs):
        return []

    @classmethod
    def create(cls, user, **kwargs):
        with transaction.atomic():
            
            try:
                entity = EntityModel.objects.get(uuid=kwargs["entity_uuid"])
                # Permission check: only admin, manager, or superuser
                if not (
                    user.is_superuser or
                    entity.admin_id == user.pk or
                    entity.managers.filter(id=user.pk).exists()
                ):
                    return cls(
                        coa_model=None,
                        success=False,
                        message="Permission denied."
                    )
                coa_model = ChartOfAccountModel.objects.create(
                    entity=entity,
                    name=kwargs["name"],
                    active=kwargs.get("active", True)
                )
                return cls(
                    coa_model=coa_model,
                    success=True,
                    message="Chart of Account Model created successfully."
                )
            except EntityModel.DoesNotExist:
                return cls(
                    coa_model=None,
                    success=False,
                    message="Entity not found."
                )
            except Exception as e:
                return cls(
                    coa_model=None,
                    success=False,
                    message=f"Error: {str(e)}"
                )

    @classmethod
    def mutate_and_get_payload(cls, root, info, **data):
        user = info.context.user
        if not user or not user.is_authenticated:
            return cls(coa_model=None, success=False, message="Authentication required.")
        return cls.create(user, **data)     



class UpdateChartOfAccountModel(BaseMutation):
    """
    Update a Chart of Account Model (CoA).
    """
    _mutation_module = "ledger"
    _mutation_class = "UpdateChartOfAccountModel"
    async_mutations = False

    coa_model = graphene.Field(ChartOfAccountModelNode, description="The updated Chart of Account Model")
    

    class Input(BaseMutation.Input):
        uuid = graphene.UUID(required=True, description="UUID of the ChartOfAccountModel to update")
        name = graphene.String(required=False, description="Name of the Chart of Accounts")
        active = graphene.Boolean(required=False, description="Is the CoA active?")

    @classmethod
    def async_mutate(cls, user, **kwargs):
        return []

    @classmethod
    def update(cls, user, **kwargs):
        with transaction.atomic():
            try:
                coa_model = ChartOfAccountModel.objects.get(uuid=kwargs["uuid"])
                entity = coa_model.entity
                # Permission check: only admin, manager, or superuser
                if not (
                    user.is_superuser or
                    entity.admin_id == user.pk or
                    entity.managers.filter(id=user.pk).exists()
                ):
                    return cls(
                        coa_model=None,
                        success=False,
                        message="Permission denied."
                    )
                if "name" in kwargs and kwargs["name"] is not None:
                    coa_model.name = kwargs["name"]
                if "active" in kwargs and kwargs["active"] is not None:
                    coa_model.active = kwargs["active"]
                coa_model.full_clean()
                coa_model.save()
                return cls(
                    coa_model=coa_model,
                    success=True,
                    message="Chart of Account Model updated successfully."
                )
            except ChartOfAccountModel.DoesNotExist:
                return cls(
                    coa_model=None,
                    success=False,
                    message="Chart of Account Model not found."
                )
            except Exception as e:
                return cls(
                    coa_model=None,
                    success=False,
                    message=f"Error: {str(e)}"
                )

    @classmethod
    def mutate_and_get_payload(cls, root, info, **data):
        user = info.context.user
        if not user or not user.is_authenticated:
            return cls(coa_model=None, success=False, message="Authentication required.")
        return cls.update(user, **data)
    
    
class CoaStateMutation(BaseMutation):
    """
    Perform a state transition (activate, deactivate, mark as default) on a ChartOfAccountModel.
    """
    _mutation_module = "ledger"
    _mutation_class = "CoaStateMutation"
    async_mutations = False

    success = graphene.Boolean()
    message = graphene.String()

    class Input(BaseMutation.Input):
        uuid = graphene.UUID(required=True, description="UUID of the ChartOfAccountModel")
        action = graphene.String(
            required=True, 
            description="Action to perform: MARK_AS_DEFAULT, MARK_AS_ACTIVE, MARK_AS_INACTIVE, LOCK_ALL_ACCOUNTS, UNLOCK_ALL_ACCOUNTS"
        )

    @classmethod
    def async_mutate(cls, user, **kwargs):
        with transaction.atomic():
            uuid = kwargs.get("uuid")
            action = kwargs.get("action", "").lower()
            try:
                coa = ChartOfAccountModel.objects.get(uuid=uuid)
                method_map = {
                    'mark_as_active': coa.mark_as_active,
                    'mark_as_inactive': coa.mark_as_inactive,
                    'mark_as_default': coa.mark_as_default,
                    'lock_all_accounts': coa.lock_all_accounts,
                    'unlock_all_accounts': coa.unlock_all_accounts
                }
                if action not in method_map:
                    return cls(
                        success=False,
                        message=f"Invalid action '{kwargs.get('action', '')}', valid actions are: MARK_AS_DEFAULT, MARK_AS_ACTIVE, MARK_AS_INACTIVE, LOCK_ALL_ACCOUNTS, UNLOCK_ALL_ACCOUNTS."
                    )
                method_map[action](commit=True)
                return cls(
                    success=True,
                    message=f"Action '{action}' performed successfully."
                )
            except ChartOfAccountModel.DoesNotExist:
                return cls(
                    success=False,
                    message="ChartOfAccountModel with this UUID does not exist."
                )
            except Exception as e:
                return cls(
                    success=False,
                    message=str(e)
                )
            



