import graphene
from django.core.exceptions import ValidationError
from ledger.models.ledger import LedgerModel
from ledger.models import EntityModel
from .types import LedgerNode
from core.schema import BaseMutation



class CreateLedger(BaseMutation):
    """
    Create a new Ledger for an entity.
    """
    _mutation_module = "ledger"
    _mutation_class = "CreateLedger"
    async_mutations = False

    ledger = graphene.Field(LedgerNode, description="The created ledger object")

    class Input(BaseMutation.Input):
        entity_uuid = graphene.UUID(required=True, description="UUID of the entity")
        name = graphene.String(required=True, description="Name of the ledger")
        ledger_xid = graphene.String(required=False, description="External ledger ID")
        posted = graphene.Boolean(required=False, description="Is the ledger posted?")
        locked = graphene.Boolean(required=False, description="Is the ledger locked?")
        hidden = graphene.Boolean(required=False, description="Is the ledger hidden?")

    @classmethod
    def async_mutate(cls, user, **kwargs):
        return []

    @classmethod
    def create(cls, user, **kwargs):

        try:
            entity_uuid = kwargs["entity_uuid"]
            name = kwargs["name"]
            ledger_xid = kwargs.get("ledger_xid")
            posted = kwargs.get("posted", False)
            locked = kwargs.get("locked", False)
            hidden = kwargs.get("hidden", False)

            entity = EntityModel.objects.get(uuid=entity_uuid)
            if not (
                user.is_superuser
                or entity.admin_id == user.pk
                or entity.managers.filter(id=user.pk).exists()
            ):
                return cls(ledger=None, success=False, message="Permission denied.")

            ledger = LedgerModel(
                entity=entity,
                name=name,
                ledger_xid=ledger_xid,
                posted=posted,
                locked=locked,
                hidden=hidden,
            )
            ledger.full_clean()
            ledger.save()
            return cls(ledger=ledger, success=True, message="Ledger created successfully.")
        except EntityModel.DoesNotExist:
            return cls(ledger=None, success=False, message="Entity not found.")
        except ValidationError as e:
            return cls(ledger=None, success=False, message=f"Validation error: {str(e)}")
        except Exception as e:
            return cls(ledger=None, success=False, message=f"Error: {str(e)}")

    @classmethod
    def mutate_and_get_payload(cls, root, info, **data):
        user = info.context.user
        if not user or not user.is_authenticated:
            return cls(ledger=None, success=False, message="Authentication required.")
        return cls.create(user, **data)



