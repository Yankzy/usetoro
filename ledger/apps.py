from django.apps import AppConfig
import logging



logger = logging.getLogger(__name__)


class LedgerConfig(AppConfig):
    default_auto_field = 'django.db.models.BigAutoField'
    name = 'ledger'

    def ready(self):
        """
        Called when the Django app registry is fully populated.
        Register Plaid service methods into the central core registry.
        """
        logger.info("LedgerConfig ready: Registering acountant services...")
        registered_count = 0
        try:
            from core.registry import register_service, ServiceRegistryError
            from .plaid_service import PlaidService 
            from ledger.models import PlaidItem, BankAccountModel, TokenCountModel
            from ledger.views.plaid import plaid_webhook

            # Instantiate the service (it's a singleton)
            plaid_service = PlaidService.instance()

            # Define services to register with unique namespaced keys
            services_to_register = {
                'plaid.plaid_webhook': plaid_webhook,
                'plaid.create_link_token': plaid_service.create_link_token,
                'plaid.get_access_token': plaid_service.get_access_token,
                'plaid.sync_transactions': plaid_service.sync_transactions,
                'plaid.get_accounts': plaid_service.get_accounts,
                'plaid.get_transactions': plaid_service.get_transactions,
                'plaid.get_account_numbers': plaid_service.get_account_numbers,
                'plaid.get_account_identity': plaid_service.get_account_identity,
                'plaid.plaid_item': PlaidItem,
                'ledger.BankAccountModel': BankAccountModel,
                'ledger.TokenCountModel': TokenCountModel,
                'plaid_service': plaid_service,
            }

            # Register each service into the central registry
            for name, service_func in services_to_register.items():
                try:
                    register_service(name, service_func)
                    registered_count += 1
                except ServiceRegistryError as e:
                    # Log the error but continue trying to register others
                    logger.warning(f"Could not register service '{name}': {e}")


            if registered_count > 0:
                 logger.info(f"Registered {registered_count} Plaid services in the central registry.")

        except ImportError as e:
            # This might happen during initial migrations or due to circular dependencies.
            logger.warning(f"Could not register Plaid services in LedgerConfig.ready() due to ImportError: {e}. "
                            "This might be expected during startup/migrations.")
        except Exception as e:
            logger.error(f"Unexpected error registering Plaid services: {e}", exc_info=True)

        # Import signals if you have any
        # import ledger.signals
