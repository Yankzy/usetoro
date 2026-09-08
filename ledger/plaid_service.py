from plaid.api import plaid_api
from plaid.model.transactions_sync_request import TransactionsSyncRequest
from plaid.model.transactions_sync_response import TransactionsSyncResponse
from plaid import Configuration, ApiClient, Environment
from plaid.model.country_code import CountryCode
from plaid.model.products import Products
from plaid.model.item_public_token_exchange_request import ItemPublicTokenExchangeRequest
from plaid.model.link_token_create_request import LinkTokenCreateRequest
from plaid.model.link_token_create_request_user import LinkTokenCreateRequestUser
from plaid.model.user_create_request import UserCreateRequest
from plaid.model.consumer_report_user_identity import ConsumerReportUserIdentity
from plaid.model.asset_report_create_request import AssetReportCreateRequest
from plaid.model.asset_report_create_request_options import AssetReportCreateRequestOptions
from plaid.model.asset_report_user import AssetReportUser
from plaid.model.asset_report_get_request import AssetReportGetRequest
from plaid.model.asset_report_pdf_get_request import AssetReportPDFGetRequest
from plaid.model.auth_get_request import AuthGetRequest
from plaid.model.identity_get_request import IdentityGetRequest
from plaid.model.item_get_request import ItemGetRequest
from plaid.model.statements_list_request import StatementsListRequest
from plaid.model.link_token_create_request_statements import LinkTokenCreateRequestStatements
from plaid.model.link_token_create_request_cra_options import LinkTokenCreateRequestCraOptions
from plaid.model.statements_download_request import StatementsDownloadRequest
from plaid.model.consumer_report_permissible_purpose import ConsumerReportPermissiblePurpose
from plaid.model.cra_check_report_base_report_get_request import CraCheckReportBaseReportGetRequest
from plaid.model.cra_check_report_pdf_get_request import CraCheckReportPDFGetRequest
from plaid.model.cra_check_report_income_insights_get_request import CraCheckReportIncomeInsightsGetRequest
from plaid.model.cra_check_report_partner_insights_get_request import CraCheckReportPartnerInsightsGetRequest
from plaid.model.cra_pdf_add_ons import CraPDFAddOns
from plaid.model.accounts_get_request import AccountsGetRequest
from plaid.exceptions import ApiException
from django.core.exceptions import ObjectDoesNotExist
from .models import PlaidItem, PlaidTransaction, TransactionAuditLog, TransactionModel, BankAccountModel
from plaid.model.processor_token_create_request import ProcessorTokenCreateRequest
from core.signals import register_service_signal
from plaid.model.sandbox_public_token_create_request import SandboxPublicTokenCreateRequest
from plaid.model.sandbox_public_token_create_request_options import SandboxPublicTokenCreateRequestOptions
from plaid.model.transactions_refresh_request import TransactionsRefreshRequest
from plaid.model.transactions_refresh_response import TransactionsRefreshResponse
from tenacity import retry, retry_if_exception_type, stop_after_attempt, wait_exponential, before_sleep_log, after_log
from functools import wraps
import json, time, requests, datetime, os, logging

logger = logging.getLogger(__name__)



def json_serializable(obj):
    """Recursively converts datetime.date and datetime.datetime to ISO format strings."""
    if isinstance(obj, dict):
        return {k: json_serializable(v) for k, v in obj.items()}
    elif isinstance(obj, list):
        return [json_serializable(v) for v in obj]
    elif isinstance(obj, (datetime.date, datetime.datetime)):
        return obj.isoformat()  # Convert to string format 'YYYY-MM-DD' or 'YYYY-MM-DDTHH:MM:SS'
    else:
        return obj  # Leave other types unchanged
    


PLAID_CLIENT_ID = os.getenv('PLAID_CLIENT_ID')
PLAID_SECRET = os.getenv('PLAID_SECRET')
PLAID_ENV = os.getenv('PLAID_ENV', 'sandbox')
PLAID_PRODUCTS = os.getenv('PLAID_PRODUCTS', 'transactions').split(',')
PLAID_COUNTRY_CODES = os.getenv('PLAID_COUNTRY_CODES', 'US').split(',')
PLAID_REDIRECT_URI = os.getenv('PLAID_REDIRECT_URI')


    
class PlaidServiceError(Exception):
    """Custom error for user-facing or non-recoverable Plaid issues."""


def plaid_api_retry(method_name):
    """
    Decorator for retrying and logging Plaid API calls.
    Retries on network or API errors.
    """
    def decorator(fn):
        @retry(
            retry=(
                retry_if_exception_type(PlaidServiceError) |
                retry_if_exception_type(requests.ConnectionError)
            ),
            stop=stop_after_attempt(3),
            wait=wait_exponential(multiplier=1, min=2, max=10),
            reraise=True,
            before_sleep=before_sleep_log(logger, logging.WARNING),
            after=after_log(logger, logging.INFO)
        )
        @wraps(fn)
        def wrapper(self, *args, **kwargs):
            params_str = []
            if args: params_str.append(f"args={args}")
            if kwargs: params_str.append(f"kwargs={kwargs}")
            param_out = ", ".join(params_str)
            start_ts = time()
            try:
                resp = fn(self, *args, **kwargs)
                dt = time() - start_ts
                return resp
            except Exception as e:
                dt = time() - start_ts
                logger.error(f"PlaidService::{method_name} - ERROR ({str(e)}) in {dt:.2f}s -- {param_out}", exc_info=True)
                raise
        return wrapper
    return decorator


class PlaidService:
    _instance = None  # Class-level variable to store the singleton instance

    def __init__(self):
        if PlaidService._instance is not None:
            raise RuntimeError("PlaidService is a singleton! Use `PlaidService.instance()` instead.")
        
        config = Configuration(
            host=Environment.Sandbox if PLAID_ENV == 'sandbox' else Environment.Production,
            api_key={
                'clientId': PLAID_CLIENT_ID,
                'secret': PLAID_SECRET
            }
        )
        self.client = plaid_api.PlaidApi(ApiClient(config))

    @classmethod
    def instance(cls):
        """Returns the singleton instance (creates it if needed)."""
        if cls._instance is None:
            cls._instance = cls()
        return cls._instance

    def _get_item_by_id(self, item_id: str) -> PlaidItem:
        """Helper to get PlaidItem or raise PlaidServiceError."""
        try:
            return PlaidItem.objects.get(item_id=item_id)
        except ObjectDoesNotExist:
            logger.error(f"PlaidItem with item_id {item_id} does not exist.")
            raise PlaidServiceError("Item not found.")
        except Exception as e:
            logger.error(f"Error retrieving PlaidItem {item_id}: {str(e)}")
            raise PlaidServiceError("Database error retrieving item.") from e

    def get_accounts(self, item_id: str):
        """
        Fetches account information using the Plaid API for a specific item.

        Args:
            item_id (str): The Plaid item ID.

        Returns:
            dict: A dictionary containing account information.

        Raises:
            PlaidServiceError: If the item is not found or an API error occurs.
        """

        try:
            item = self._get_item_by_id(item_id)
            access_token = item.access_token
            
            if not access_token:
                logger.error(f"Access token for item {item_id} is None.")
                raise PlaidServiceError("Access token is missing.")
            
            request = AccountsGetRequest(access_token=access_token)
            response = self.client.accounts_get(request)
            # Consider storing/updating account metadata linked to PlaidItem if needed
            return response.to_dict()
        except ApiException as e:
            error_body = json.loads(e.body)
            error_code = error_body.get("error_code")
            if error_code == "ITEM_LOGIN_REQUIRED":
                logger.warning(f"User login required for item {item_id}.")
                # Potentially update item status here
                raise PlaidServiceError("User login required. Use Link update mode.") from e
            else:
                logger.error(f"Plaid API error getting accounts for item {item_id}: {e.body}")
                raise PlaidServiceError(f"Failed to get accounts: {e.body}") from e

    def create_link_token(self, user_id): 
        """Creates a Plaid Link token."""
        # Future: Could customize products, country_codes if needed
        products = [Products(p) for p in PLAID_PRODUCTS]
        country_codes = [CountryCode(c) for c in PLAID_COUNTRY_CODES]

        request = LinkTokenCreateRequest(
            client_name="Vox Profit", # Consider making this configurable
            country_codes=country_codes,
            language='en',
            user=LinkTokenCreateRequestUser(client_user_id=str(user_id)),
            products=products,
            redirect_uri='https://cdn-testing.plaid.com/link/v2/stable/sandbox-oauth-a2a-react-native-redirect.html', # https://dashboard.plaid.com/developers/api
            # webhook='https://www.intuitero.com/api/', # Strongly recommended for production
            webhook='https://prime-legible-turkey.ngrok-free.app/api/plaid/webhook/',
            # access_token="" https://youtu.be/6wWPKrVPaio?t=162
        )
        try:
            response = self.client.link_token_create(request)
            logger.info(f"Created link token for user {user_id}")
            return response.to_dict()
        except ApiException as e:
            logger.error(f"Failed to create link token for user {user_id}: {e.body}")
            raise PlaidServiceError(f"Could not create link token: {e.body}") from e



    def create_processor_token(self, access_token:str, account_id:str, processor:str="plaid") -> str:
        """
        Generate a processor token for Plaid using Plaid API.

        Args:
            access_token (str): The Plaid access token for the item.
            account_id (str): Specific account ID from a previous accounts call.
            processor (str): Should be "plaid" for Plaid integration.

        Returns:
            str: Processor token to pass to Plaid API.

        Raises:
            PlaidServiceError: if the Plaid API call fails.
        """
        try:
            request = ProcessorTokenCreateRequest(
                access_token=access_token,
                account_id=account_id,
                processor=processor
            )
            response = self.client.processor_token_create(request)
            processor_token = response.processor_token
            logger.info(f"Created processor token for processor '{processor}' successfully.")
            return processor_token
        except ApiException as e:
            logger.error(f"Plaid API error generating processor token: {e.body}")
            raise PlaidServiceError(f"Failed to create processor token: {e.body}") from e
    

    def get_account_numbers(self, item_id: str):
        """
        Fetches account and routing numbers for a given Plaid item.
        Returns a list of dicts with account_id, account_number, routing_number, etc.
        """
        try:
            item = self._get_item_by_id(item_id)
            access_token = item.access_token
            if not access_token:
                raise PlaidServiceError("Access token is missing.")

            request = AuthGetRequest(access_token=access_token)
            response = self.client.auth_get(request)
            # The response contains 'accounts' and 'numbers'
            # 'numbers' contains 'ach' (for US), 'eft' (for Canada), etc.
            return response.to_dict()  # Typically {'ach': [...], ...}
        except ApiException as e:
            logger.error(f"Plaid API error getting account numbers for item {item_id}: {e.body}")
            raise PlaidServiceError(f"Failed to get account numbers: {e.body}") from e


    @register_service_signal('create_bank_connections_from_plaid_item')
    def get_access_token(self, public_token: str, user, module_key: str):
        """
        Exchanges a public token for an access token and item ID.
        Returns immediately after exchange; all further processing is deferred to a Celery task.

        Args:
            public_token (str): The public token from Plaid Link.
            user: The Django user instance.
            module_key (str): The module key for the Plaid item.
        Returns:
            dict: Dictionary containing 'item_id' and 'access_token'.

        Raises:
            PlaidServiceError: If the exchange fails.
        """
        try:
            # This is for backend dev/tesing
            # call ExchangePublicToken mutation with sandboxPublicToken
            if public_token == "sandboxPublicToken":
                sandbox_token_data = self.create_sandbox_public_token()
                public_token = sandbox_token_data['public_token']

            exchange_request = ItemPublicTokenExchangeRequest(public_token=public_token)
            exchange_response = self.client.item_public_token_exchange(exchange_request)

            access_token = exchange_response.access_token
            item_id = exchange_response.item_id
            request_id = exchange_response.request_id  # For logging

            logger.info(f"Successfully exchanged public token for item {item_id}. Request ID: {request_id}")
            request = AccountsGetRequest(access_token=access_token)
            item_metadata = self.client.accounts_get(request)
            
            meta_dict = item_metadata.to_dict()
            plaid_item, created = PlaidItem.objects.get_or_create_from_metadata(
                item_id=item_id,
                access_token=access_token,
                user=user,
                metadata=meta_dict,
                module_key=module_key
            )
            
            return {
                'module_key': module_key,
                'plaid_item': plaid_item,
                'item_id': item_id,
                'access_token': 'True'  # Be cautious about returning access_token directly
            }

        except ApiException as e:
            logger.error(f"Plaid API error exchanging public token: {e.body}")
            raise PlaidServiceError(f"Failed to exchange public token: {e.body}") from e
        except Exception as e:
            logger.error(f"Database error during access token processing: {str(e)}")
            raise PlaidServiceError("Failed to save Plaid item or module link.") from e
        


    def get_transactions(self, plaid_item: PlaidItem):
        """
        Fetches transactions using pagination via Transactions Sync API for a specific item.
        Updates the item's cursor.

        Args:
            item_id (str): The Plaid item ID.

        Returns:
            dict: A dictionary containing 'added', 'modified', 'removed' transactions, and 'next_cursor'.

        Raises:
            PlaidServiceError: If the item is not found, or an API/database error occurs.
        """
        access_token = plaid_item.access_token
        current_cursor = plaid_item.cursor or ""
        item_id = plaid_item.item_id

        added = []
        modified = []
        removed = []
        has_more = True
        request_ids = [] # To store Plaid request_ids if needed for logging/auditing

        try:
            while has_more:
                request = TransactionsSyncRequest(
                    access_token=access_token,
                    cursor=current_cursor
                )
                response: TransactionsSyncResponse = self.client.transactions_sync(request)
                request_ids.append(response.request_id)

                added.extend(response.added)
                modified.extend(response.modified)
                removed.extend(response.removed) # These are transaction IDs
                has_more = response.has_more
                current_cursor = response.next_cursor

            # Update the cursor in the database only after successful sync loop
            if plaid_item.cursor != current_cursor:
                plaid_item.cursor = current_cursor
                plaid_item.save(update_fields=['cursor', 'updated_at'])
                logger.info(f"Updated cursor for item {item_id} to {current_cursor}")

        except ApiException as e:
            logger.error(f"Plaid API error during transactions sync for item {item_id}: {e.body}")
            raise PlaidServiceError(f"Failed to sync transactions: {e.body}") from e
        except Exception as e:
            logger.error(f"Unexpected error during transactions sync for item {item_id}: {str(e)}")
            raise PlaidServiceError("Transaction sync failed.") from e

        # Convert Plaid models to dictionaries for consistent return type
        return {
            'added': [t.to_dict() for t in added],
            'modified': [t.to_dict() for t in modified],
            'removed': [t.to_dict() for t in removed], # Plaid returns dicts { 'transaction_id': '...' } for removed
            'next_cursor': current_cursor,
            'plaid_request_ids': request_ids,
            'has_more': has_more
        }





    def sync_transactions(self, item_id: str):
        """
        Synchronizes transactions for a given item_id and updates the database.

        Args:
            item_id (str): The Plaid item ID to sync.

        Returns:
            dict: Summary of sync results (e.g., counts of added/modified/removed).
                    Returns None if sync fails before processing.
        """
        try:
            plaid_item = self._get_item_by_id(item_id) # Ensure item exists first
            user = plaid_item.user
            sync_result = self.get_transactions(plaid_item) # Fetch raw data from Plaid
        except PlaidServiceError as e:
                logger.error(f"Cannot sync transactions for item {item_id}: {e}")
                return None # Or re-raise depending on desired caller behavior

        audit_logs = []
        added_count = 0
        modified_count = 0
        removed_count = 0
        sync_request_id = sync_result.get('plaid_request_ids', ['unknown'])[0] # Use first request ID for audit log grouping

        # return sync_result

        # # Process removed transactions
        # removed_ids = [tx['transaction_id'] for tx in sync_result.get('removed', [])]
        # if removed_ids:
        #     removed_transactions = PlaidTransaction.objects.filter(transaction_id__in=removed_ids, plaid_item=plaid_item)
        #     for tx in removed_transactions:
        #         # Optional: Check if already marked removed?
        #         # if not tx.is_removed: ...
        #         previous_state = PlaidTransaction.objects._snapshot_state(tx) # Use the snapshot method if available
        #         tx.is_removed = True # Assuming an 'is_removed' flag exists
        #         tx.save(update_fields=['is_removed', 'updated_at']) # Optimize save
        #         audit_logs.append(TransactionAuditLog(
        #             transaction=tx, # Link to the transaction being removed
        #             user=user,
        #             action=TransactionAuditLog.ActionChoices.REMOVED,
        #             previous_state=previous_state,
        #             plaid_sync_id=sync_request_id
        #         ))
        #         removed_count += 1
        #     logger.info(f"Marked {removed_count} transactions as removed for item {item_id}.")

        # # Process modified transactions
        # modified_tx_data = sync_result.get('modified', [])
        # if modified_tx_data:
        #     modified_ids = [tx['transaction_id'] for tx in modified_tx_data]
        #     existing_tx = PlaidTransaction.objects.filter(
        #         transaction_id__in=modified_ids,
        #         plaid_item=plaid_item
        #     ).select_for_update() # Lock rows if using versioning heavily

        #     tx_map = {tx.transaction_id: tx for tx in existing_tx}

        #     for mod_data in modified_tx_data:
        #         tx = tx_map.get(mod_data['transaction_id'])
        #         if tx:
        #             pass
        #             # Decide on versioning strategy:
        #             # 1. Simple Update: Update fields in place.
        #             # 2. Full Versioning: Create a new row, mark old as non-current. (More complex)

        #             # --- Strategy 1: Simple Update ---
        #             # previous_state = PlaidTransaction.objects._snapshot_state(tx) # Snapshot before changes
        #             # updated_fields = tx.update_from_plaid_data(mod_data) # Method returns list of changed fields
        #             # if updated_fields:
        #             #     tx.save(update_fields=updated_fields + ['updated_at'])
        #             #     audit_logs.append(TransactionAuditLog(...))
        #             #     modified_count += 1

        #             # --- Strategy 2: Full Versioning (using your existing methods) ---
        #             # if hasattr(tx, 'create_new_version'):
        #             #     try:
        #             #         new_tx = tx.create_new_version(mod_data, user, sync_request_id) # Pass sync_id to versioning
        #             #         # Audit log creation might be handled within create_new_version
        #             #         modified_count += 1
        #             #     except Exception as version_exc:
        #             #         logger.error(f"Failed to create new version for TX {tx.transaction_id}: {version_exc}")
        #             # else:
        #             #     # Fallback to simple update if versioning method doesn't exist
        #             #         logger.warning(f"Transaction {tx.transaction_id} lacks 'create_new_version'. Performing in-place update.")
        #             #         previous_state = PlaidTransaction.objects._snapshot_state(tx)
        #             #         changed = tx._prepare_update_data(mod_data) # Assuming this returns changed fields dict
        #             #         updated = False
        #             #         for field, value in changed.items():
        #             #             if getattr(tx, field) != value:
        #             #                 setattr(tx, field, value)
        #             #                 updated = True
        #             #         if updated:
        #             #             tx.save()
        #             #             audit_logs.append(TransactionAuditLog(
        #             #                 transaction=tx, user=user, action='MODIFIED',
        #             #                 previous_state=previous_state, new_state=PlaidTransaction.objects._snapshot_state(tx),
        #             #                 plaid_sync_id=sync_request_id
        #             #             ))
        #             #             modified_count += 1

        #         else:
        #             logger.warning(f"Received modification for unknown transaction ID {mod_data['transaction_id']} on item {item_id}. Treating as ADD.")
        #             # Optionally treat as an 'ADD' if it wasn't found
        #             modified_tx_data.remove(mod_data) # Remove from modified list
        #             sync_result.setdefault('added', []).append(mod_data) # Add to added list

        #     logger.info(f"Processed {modified_count} modified transactions for item {item_id}.")


        # Process added transactions
        added_tx_data = sync_result.get('added', [])
        if added_tx_data:
            # Use create_from_plaid_data if it exists and handles everything
            try:
                PlaidTransaction.objects.bulk_create_from_plaid_data(added_tx_data, plaid_item)
            except Exception as create_exc:
                logger.error(f"ERROR: {create_exc}")
            

        # # Bulk create audit logs
        # if audit_logs:
        #     try:
        #         TransactionAuditLog.objects.bulk_create(audit_logs)
        #         logger.info(f"Created {len(audit_logs)} audit log entries for item {item_id} sync.")
        #     except Exception as audit_exc:
        #         logger.error(f"Failed to bulk create audit logs for item {item_id}: {audit_exc}")

        return sync_result
    


    def get_account_identity(self, item_id: str):
        """
        Fetches identity (owner) info for all accounts in a Plaid item.
        Returns a dict with owner info, including addresses.
        """
        try:
            item = self._get_item_by_id(item_id)
            access_token = item.access_token
            if not access_token:
                raise PlaidServiceError("Access token is missing.")

            request = IdentityGetRequest(access_token=access_token)
            response = self.client.identity_get(request)
            return response.to_dict()  # Contains 'accounts' with 'owners' and their addresses
        except ApiException as e:
            logger.error(f"Plaid API error getting identity for item {item_id}: {e.body}")
            raise PlaidServiceError(f"Failed to get identity: {e.body}") from e



    def create_sandbox_public_token(self, institution_id="ins_109508"):
        """
        Creates a public token for a specific institution in the Plaid Sandbox environment.
        This bypasses the Plaid Link flow and is used for testing and backend-only development.

        Args:
            institution_id (str): The Plaid institution ID to create a public token for.

        Returns:
            dict: A dictionary containing the 'public_token'.

        Raises:
            PlaidServiceError: If the Plaid API call fails.
        """
        try:
            products = [Products(p) for p in PLAID_PRODUCTS]
            request = SandboxPublicTokenCreateRequest(
                institution_id=institution_id,
                initial_products=products,
                options=SandboxPublicTokenCreateRequestOptions(
                    override_username="user_transactions_dynamic",
                    override_password="Any-non-blank-password-will-work" 
                )
            )
            response = self.client.sandbox_public_token_create(request)
            public_token = response['public_token']
            logger.info(f"Successfully created sandbox public token for institution {institution_id}")
            return {'public_token': public_token}

        except ApiException as e:
            logger.error(f"Failed to create sandbox public token: {e.body}")
            raise PlaidServiceError(f"Failed to create sandbox public token: {e.body}") from e
        except Exception as e:
            logger.error(f"Unexpected error creating sandbox public token: {str(e)}")
            raise PlaidServiceError("Unexpected error creating sandbox public token.") from e



    def refresh_transactions(self, access_token: str):
        """
        Triggers a transaction refresh for a given Item.
        This can generate new transactions and fire webhooks in the Sandbox.
        """
        try:
            request = TransactionsRefreshRequest(access_token=access_token)
            response: TransactionsRefreshResponse = self.client.transactions_refresh(request)
            logger.info(f"Successfully triggered transaction refresh for item.")
            return response.to_dict()
        except ApiException as e:
            logger.error(f"Failed to refresh transactions: {e.body}")
            raise PlaidServiceError(f"Failed to refresh transactions: {e.body}") from e

