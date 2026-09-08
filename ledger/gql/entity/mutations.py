import graphene
from ledger.models.entity import EntityModel
from ledger.gql.entity.types import EntityModelNode
from core.schema import BaseMutation
from django.db import transaction
from django.core.exceptions import ValidationError
import logging
from ledger.io.io_generator import EntityDataGenerator
from ledger.io.io_core import get_localtime
from decimal import Decimal
from datetime import timedelta
from ledger.models.items import ItemTransactionModel
from ledger.models.transactions import TransactionModel
from ledger.models.ledger import LedgerModel
from ledger.models.unit import EntityUnitModel
from ledger.models.coa_default import get_default_coa


logger = logging.getLogger(__name__)



class CreateEntityModel(BaseMutation):
    """
    Create a new EntityModel (company/legal entity).
    """
    _mutation_module = "ledger"
    _mutation_class = "CreateEntityModel"
    async_mutations = False

    entity = graphene.Field(EntityModelNode, description="The created entity object")

    class Input(BaseMutation.Input):
        name = graphene.String(required=True, description="Entity name")
        fy_start_month = graphene.Int(required=True, description="Fiscal year start month (1-12)")
        accrual_method = graphene.Boolean(required=True, description="Use accrual accounting method?")
        picture = graphene.String(required=False, description="Optional picture (URL or base64)")
        address_1 = graphene.String(required=False)
        address_2 = graphene.String(required=False)
        city = graphene.String(required=False)
        state = graphene.String(required=False)
        zip_code = graphene.String(required=False)
        country = graphene.String(required=False)
        email = graphene.String(required=False)
        website = graphene.String(required=False)
        phone = graphene.String(required=False)
        activate_all_accounts = graphene.Boolean(required=False, default_value=False)
        generate_sample_data = graphene.Boolean(required=False, default_value=False)
        is_ephemeral = graphene.Boolean(required=False, default_value=False, description="Is this a temporary project?")

    @classmethod
    def async_mutate(cls, user, **kwargs):
        return []

    
    @classmethod
    def create(cls, **kwargs):
        try:
            email = kwargs.get("email")
            entity = EntityModel.objects.filter(email=email).first() if email else None

            if entity:
                return cls(
                    success=False,
                    message="Entity already exists.",
                    user_message="An entity with this email already exists.",
                    entity=entity
                )

            with transaction.atomic():
                user = kwargs['user']
                fields = {f.name for f in EntityModel._meta.get_fields()}
                valid_fields = {k: v for k, v in kwargs.items() if k in fields and v is not None}
                valid_fields['admin'] = user
                entity = EntityModel(**valid_fields)
                entity = EntityModel.add_root(instance=entity)

                if picture := kwargs.get('picture'):
                    entity.picture = picture
                    entity.save(update_fields=['picture'])

                # Create default CoA and insert DEFAULT_CHART_OF_ACCOUNTS
                default_coa_model = entity.create_chart_of_accounts(coa_name="general_coa", assign_as_default=True, commit=True)

                activate = bool(kwargs.get('activate_all_accounts'))
                for acc in get_default_coa():
                    account_model = default_coa_model.create_account(
                        code=acc.get('code'),
                        role=acc.get('role'),
                        name=acc.get('name'),
                        balance_type=acc.get('balance_type'),
                        active=activate,
                    )
                    # Preserve same post-creation flags as previous utility
                    account_model.role_default = False
                    account_model.locked = False
                    account_model.save(update_fields=['role_default', 'locked'])

                # Ensure a general ledger exists
                LedgerModel.objects.get_or_create(
                    name="general_ledger",
                    entity=entity,
                )

                # Ensure general unit exists (create root if missing)
                unit = EntityUnitModel.objects.filter(name="general_unit", entity=entity).first()
                if not unit:
                    unit = EntityUnitModel.add_root(
                        name="general_unit",
                        entity=entity,
                        active=True,
                        hidden=False,
                        depth=0,
                    )

                # Optionally populate sample data
                if kwargs.get('generate_sample_data'):
                    entity_generator = EntityDataGenerator(
                        entity_model=entity,
                        user_model=user,
                        start_dttm=get_localtime() - timedelta(days=30 * 8),
                        capital_contribution=Decimal.from_float(50000),
                        days_forward=30 * 7,
                        tx_quantity=50
                    )
                    entity_generator.populate_entity()

            return cls(
                success=True,
                message="Entity created successfully.",
                entity=entity,
            )
        except ValidationError as e:
            return cls(
                success=False,
                message="Validation error occurred.",
                user_message=str(e),
            )
        except Exception as e:
            logger.exception("Error creating entity")
            return cls(
                success=False,
                message="An unexpected error occurred while creating the entity.",
                user_message=str(e),
            )

    @classmethod
    def mutate_and_get_payload(cls, root, info, **data):
        user = info.context.user
        if not user or not user.is_authenticated:
            return cls(
                success=False,
                message="Authentication required.",
                user_message="You must be logged in to create an entity.",
            )
        data['user'] = user
        return cls.create(**data)
        

class UpdateEntityModel(BaseMutation):
    """
    Update an existing EntityModel.
    """
    _mutation_module = "ledger"
    _mutation_class = "UpdateEntityModel"
    async_mutations = False

    entity = graphene.Field(EntityModelNode, description="The updated entity object")

    class Input(BaseMutation.Input):
        uuid = graphene.UUID(required=True, description="UUID of the entity to update")
        name = graphene.String(required=False, description="Entity name")
        fy_start_month = graphene.Int(required=False, description="Fiscal year start month (1-12)")
        accrual_method = graphene.Boolean(required=False, description="Use accrual accounting method?")
        picture = graphene.String(required=False, description="Optional picture (URL or base64)")
        address_1 = graphene.String(required=False)
        address_2 = graphene.String(required=False)
        city = graphene.String(required=False)
        state = graphene.String(required=False)
        zip_code = graphene.String(required=False)
        country = graphene.String(required=False)
        email = graphene.String(required=False)
        website = graphene.String(required=False)
        phone = graphene.String(required=False)
        is_ephemeral = graphene.Boolean(required=False, description="Is this a temporary project?")

    @classmethod
    def async_mutate(cls, user, **kwargs):
        return []

    @classmethod
    def update(cls, user, **kwargs):
        with transaction.atomic():
            try:
                uuid = kwargs.pop('uuid')
                entity = EntityModel.objects.get(uuid=uuid)

                # Permission check: only admin, manager, or superuser
                if not (
                    user.is_superuser or
                    entity.admin_id == user.pk or
                    entity.managers.filter(id=user.pk).exists()
                ):
                    return cls(
                        entity=None,
                        success=False,
                        message="Permission denied.",
                        user_message="You do not have permission to update this entity.",
                    )

                # Update only provided fields
                updatable_fields = [
                    'name', 'fy_start_month', 'accrual_method', 'address_1', 'address_2', 'city', 'state',
                    'zip_code', 'country', 'email', 'website', 'phone', 'is_ephemeral'
                ]
                for field in updatable_fields:
                    if field in kwargs and kwargs[field] is not None:
                        setattr(entity, field, kwargs[field])

                if 'picture' in kwargs and kwargs['picture']:
                    entity.picture = kwargs['picture']

                # Enforce model/form validation
                entity.full_clean()  # <-- Add this line for strict validation

                entity.save()
                return entity
            except EntityModel.DoesNotExist:
                return cls(
                    entity=None,
                    success=False,
                    message="Entity not found.",
                    user_message="Entity with this UUID does not exist.",
                )
            except ValidationError as e:
                return cls(
                    entity=None,
                    success=False,
                    message="Validation error.",
                    user_message="Validation error occurred while updating the entity.",
                )
            except Exception as e:
                return cls(
                    entity=None,
                    success=False,
                    message="Unexpected error occurred.",
                    user_message="An unexpected error occurred while updating the entity.",
                )
            

    @classmethod
    def mutate_and_get_payload(cls, root, info, **data):
        user = info.context.user
        if not user or not user.is_authenticated:
            return cls(
                success=False,
                message="Authentication required.",
                entity=None,
                user_message="You must be logged in to update the entity.",
            )
        try:
            entity = cls.update(user, **data)
            if isinstance(entity, cls):
                return entity
            return cls(
                success=True,
                message="Entity updated successfully.",
                entity=entity,
            )
        except Exception as oops:
            return cls(
                success=False,
                message=str(oops),
                entity=None,
                user_message="An error occurred while updating the entity.",
            )
        

class DeleteEntityModel(BaseMutation):
    """
    Delete an EntityModel and all related ItemTransactionModel and TransactionModel records for the entity.
    Only allowed for admin, manager, or superuser.
    """
    _mutation_module = "ledger"
    _mutation_class = "DeleteEntityModel"
    async_mutations = False

    class Input(BaseMutation.Input):
        uuid = graphene.UUID(required=True, description="UUID of the entity to delete")


    @classmethod
    def async_mutate(cls, user, **kwargs):
        with transaction.atomic():
            try:
                uuid = kwargs["uuid"]
                entity = EntityModel.objects.get(uuid=uuid)

                # Permission check: only admin, manager, or superuser
                if not (
                    user.is_superuser or
                    entity.admin_id == user.pk or
                    entity.managers.filter(id=user.pk).exists()
                ):
                    return cls(
                        success=False,
                        message="Permission denied: you do not have rights to delete this entity."
                    )

                # Remove default_coa reference before deletion
                entity.default_coa = None
                entity.save(update_fields=["default_coa"])

                # Delete related ItemTransactionModel and TransactionModel
                ItemTransactionModel.objects.for_entity(user, entity.slug).delete()
                TransactionModel.objects.for_entity(entity.slug, user).delete()

                entity.delete()
                return []
            except EntityModel.DoesNotExist:
                return cls(success=False, message="Entity not found.")
            except Exception as e:
                return cls(success=False, message=f"Error deleting entity: {str(e)}")


class EntityStateMutation(BaseMutation):
    """
    Perform a state transition (activate, deactivate) on an EntityModel.
    """
    _mutation_module = "ledger"
    _mutation_class = "EntityStateMutation"
    async_mutations = False

    entity = graphene.Field(EntityModelNode, description="The updated entity object")

    class Input(BaseMutation.Input):
        uuid = graphene.UUID(required=True, description="UUID of the entity to operate on")
        action = graphene.String(
            required=True,
            description="Action to perform: ACTIVATE, DEACTIVATE"
        )

    @classmethod
    def async_mutate(cls, user, **kwargs):
        return []

    @classmethod
    def mutate_state(cls, user, **kwargs):
        with transaction.atomic():
            uuid = kwargs.get("uuid")
            action = kwargs.get("action", "").strip().lower()
            try:
                entity = EntityModel.objects.get(uuid=uuid)

                # Permission check: only admin, manager, or superuser
                if not (
                    user.is_superuser or
                    entity.admin_id == user.pk or
                    entity.managers.filter(id=user.pk).exists()
                ):
                    return cls(
                        entity=None,
                        success=False,
                        message="Permission denied.",
                        user_message="You do not have permission to change the state of this entity.",
                    )

                if action == "activate":
                    entity.hidden = False
                elif action == "deactivate":
                    entity.hidden = True
                else:
                    return cls(
                        entity=None,
                        success=False,
                        message=f"Invalid action: '{kwargs.get('action', '')}', valid actions are: ACTIVATE, DEACTIVATE.",
                        user_message="Invalid action specified for entity state change.",
                    )
                entity.save()
                return entity
            except EntityModel.DoesNotExist:
                return cls(
                    entity=None,
                    success=False,
                    message="Entity not found.",
                    user_message="Entity with this UUID does not exist.",
                )
            except Exception as e:
                return cls(
                    entity=None,
                    success=False,
                    message="Unexpected error occurred.",
                    user_message="An unexpected error occurred while changing the entity state.",
                )

    @classmethod
    def mutate_and_get_payload(cls, root, info, **data):
        user = info.context.user
        if not user or not user.is_authenticated:
            return cls(
                success=False,
                message="Authentication required.",
                entity=None,
                user_message="You must be logged in to change the entity state.",
            )
        try:
            entity = cls.mutate_state(user, **data)
            if isinstance(entity, cls):
                return entity
            return cls(
                success=True,
                message="Entity state updated successfully.",
                entity=entity,
            )
        except Exception as oops:
            return cls(
                success=False,
                message=str(oops),
                entity=None,
                user_message="An error occurred while changing the entity state.",
            )
        

