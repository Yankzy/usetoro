import graphene
from pydantic import ValidationError
from ledger.plaid_service import PlaidServiceError, PlaidService
import json
import logging
from core.schema import BaseMutation

logger = logging.getLogger(__name__)

plaid_service = PlaidService.instance()

class CreateLinkToken(BaseMutation):

    _mutation_module = "ledger"
    _mutation_class = "CreateLinkToken"
    link_token = graphene.String()
    async_mutations = False

    class Input(BaseMutation.Input):
        pass

    @classmethod
    def async_mutate(cls, user, **data):
        """
        Create a Plaid link token and store it in mutation_results.
        """
        if not user or not user.is_authenticated:
            return [{"message": "Authentication required.", "user_message": "Authentication required."}]
        try:
            mutation_results = data.get("mutation_results", {})
            response = plaid_service.create_link_token(user_id=user.user_uuid)
            mutation_results["link_token"] = response.get("link_token")
            return []
        except (PlaidServiceError, Exception) as oops:
            return [{"message": str(oops), "user_message": "Failed to create link token."}]


    # @classmethod
    # def get_link_token(cls, user):
    #     """
    #     Uses the central registry to get the Plaid link token generation function.
    #     Returns link_token.
    #     """
    #     try:
    #         response = plaid_service.create_link_token(user_id=user.pk)
    #         return response['link_token']
    #     except PlaidServiceError as e:
    #         logger.error(f"Failed to get link token via registry for user {user.pk}: {e}")
    #         raise
    #     except Exception as e:
    #         logger.exception(f"Unexpected error getting link token for user {user.pk}")
    #         raise PlaidServiceError("An unexpected error occurred while creating the link token.") from e


    @classmethod
    def mutate_and_get_payload(cls, root, info, **data):
        """
        Executes BaseMutation lifecycle (including async_mutate), then returns the token.
        """
        mutation_instance = super().mutate_and_get_payload(root, info, **data)
        if mutation_instance.success is False:
            return mutation_instance
            
        mutation_instance.message = "Successfully got link token."
        return mutation_instance


class ExchangePublicToken(BaseMutation):

    _mutation_module = "ledger"
    _mutation_class = "ExchangePublicToken"
    item_id = graphene.String()

    class Input(BaseMutation.Input):
        public_token = graphene.String(required=True)
        module_key = graphene.String(required=True)
        entity_uuid = graphene.String(required=False)

    @classmethod
    def async_mutate(cls, user, **data):
        """
        Exchange the public token for access, store item_id in mutation_results.
        """
        logger.info(f"===MUTATION STARTED WITH: {data}")
        if not user or not user.is_authenticated:
            return [{"message": "Authentication required.", "user_message": "Authentication required."}]
        try:
            public_token = data.get('public_token')
            module_key = data.get('module_key')
            entity_uuid = data.get('entity_uuid', "not_necessary")

            if module_key and module_key.lower() == "ledger" and not entity_uuid:
                raise ValidationError("If ledger module, entity_uuid is required")
            if not public_token:
                raise ValueError("Public token is required.")

            exchange_result = plaid_service.get_access_token(public_token, user, module_key)
            mutation_results = data.get("mutation_results", {})
            mutation_results["item_id"] = exchange_result.get("item_id")
            return []
        except (ValueError, PlaidServiceError) as oops:
            return [{"message": str(oops), "user_message": "Failed to exchange public token."}]
        except Exception as oops:
            logger.exception(f"Unhandled exception in ExchangePublicToken for user {getattr(user, 'pk', None)}")
            return [{"message": "An unexpected server error occurred.", "user_message": "Unexpected error."}]



    @classmethod
    def mutate_and_get_payload(cls, root, info, **data):
        """
        Executes BaseMutation lifecycle (including async_mutate), then returns the item_id.
        """
        mutation_instance = super().mutate_and_get_payload(root, info, **data)
        if mutation_instance.success is False:
            return mutation_instance
            
        mutation_instance.message = "Token successfully exchanged."
        return mutation_instance


# NOTE: (Comment remains relevant) Consider replacing this with a query or a dedicated 'SyncItem' mutation.
class GetTransactions(BaseMutation):

    _mutation_module = "ledger"
    _mutation_class = "GetTransactions"
    transactions_result = graphene.JSONString(description="Raw result from Plaid's /transactions/sync")

    class Input(BaseMutation.Input):
        item_id = graphene.String(required=True)

    @classmethod
    def async_mutate(cls, user, **data):
        """
        Fetch transactions and store serialized JSON in mutation_results.
        """
        if not user or not user.is_authenticated:
            return [{"message": "Authentication required.", "user_message": "Authentication required."}]
        try:
            item_id = data.get('item_id')
            if not item_id:
                raise ValueError("Item ID is required.")
            response = plaid_service.get_transactions(item_id=item_id)
            mutation_results = data.get("mutation_results", {})
            mutation_results["transactions_result"] = json.dumps(response)
            return []
        except (ValueError, PermissionError, PlaidServiceError) as error:
            return [{"message": str(error), "user_message": "Failed to fetch transactions."}]
        except Exception as e:
            logger.exception("Unexpected error fetching transactions")
            return [{"message": "An unexpected error occurred while fetching transactions.", "user_message": "Unexpected error."}]

    @classmethod
    def fetch_transactions(cls, user, **data):
        """
        Uses the central registry to fetch raw transaction data for an item.
        WARNING: Review if this direct fetching aligns with application logic.
        """
        item_id = data.get('item_id')
        if not item_id:
            raise ValueError("Item ID is required.")

        try:
            response = plaid_service.get_transactions(item_id=item_id)
            return response
        except PlaidServiceError as e:
            logger.error(f"Failed to get raw transactions via registry for item {item_id}, user {user.pk}: {e}")
            raise
        except Exception as e:
            logger.exception(f"Unexpected error getting raw transactions for item {item_id}, user {user.pk}")
            raise PlaidServiceError("An unexpected error occurred while fetching transactions.") from e


    @classmethod
    def mutate_and_get_payload(cls, root, info, **data):
        """
        Executes BaseMutation lifecycle (including async_mutate), then returns raw transaction data.
        """
        mutation_instance = super().mutate_and_get_payload(root, info, **data)
        if mutation_instance.success is False:
            return mutation_instance
            
        mutation_instance.message = "Raw transaction data successfully retrieved."
        return mutation_instance
