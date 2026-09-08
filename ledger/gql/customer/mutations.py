
import graphene
from ledger.gql.customer.types import CustomerNode
from ledger.models.customer import CustomerModel
from ledger.models.entity import EntityModel
from core.schema import BaseMutation
from django.db import transaction



class CreateCustomer(BaseMutation):
    """
    Create a new customer for an entity.
    """
    _mutation_module = "ledger"
    _mutation_class = "CreateCustomer"
    async_mutations = False
    customer = graphene.Field(CustomerNode, description="The created customer object")

    class Input(BaseMutation.Input):
        customer_name = graphene.String(required=True, description="Customer name")
        entity_uuid = graphene.String(required=True, description="Slug of the entity to attach the customer to")
        email = graphene.String(required=False, description="Customer email")
        address_1 = graphene.String(required=False, description="Customer address line 1")
        address_2 = graphene.String(required=False, description="Customer address line 2")
        city = graphene.String(required=False, description="Customer city")
        state = graphene.String(required=False, description="Customer state")
        zip_code = graphene.String(required=False, description="Customer zip code")
        country = graphene.String(required=False, description="Customer country")
        phone = graphene.String(required=False, description="Customer phone number")
        website = graphene.String(required=False, description="Customer website")
        sales_tax_rate = graphene.Float(required=False, description="Customer sales tax rate")
        description = graphene.String(required=False, description="Customer description")
        active = graphene.Boolean(required=False, default_value=True, description="Is the customer active?")
        hidden = graphene.Boolean(required=False, default_value=False, description="Is the customer hidden?")
        additional_info = graphene.JSONString(required=False, description="Additional information about the customer")
            

    @classmethod
    def async_mutate(cls, user, **kwargs):
        """
        Create the customer and store it in mutation_results.
        """
        if not user or not user.is_authenticated:
            return [{"message": "Authentication required.", "user_message": "Authentication required."}]
        mutation_results = kwargs.get("mutation_results", {})
        try:
            entity_uuid = kwargs.get('entity_uuid')
            with transaction.atomic():
                entity = EntityModel.objects.for_user(user_model=user).get(uuid=entity_uuid)
                fields = {f.name for f in CustomerModel._meta.get_fields()}
                valid_fields = {k: v for k, v in kwargs.items() if k in fields and v is not None}
                customer = CustomerModel(entity_model=entity, **valid_fields)
                customer.save()
                mutation_results["customer"] = customer
            return []
        except EntityModel.DoesNotExist:
            return [{
                "message": "Entity does not exist or permission denied.",
                "user_message": "Invalid entity."
            }]
        except Exception as e:
            return [{
                "message": str(e),
                "user_message": "An error occurred while creating the customer."
            }]

    @classmethod
    def mutate_and_get_payload(cls, root, info, **data):
        """
        Executes BaseMutation lifecycle (including async_mutate), then returns the created customer.
        """
        mutation_instance = super().mutate_and_get_payload(root, info, **data)
        if mutation_instance.success is False:
            return mutation_instance
            
        mutation_instance.message = "Customer created successfully."
        return mutation_instance