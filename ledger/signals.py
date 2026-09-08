import logging
from core.service_signals import ServiceSignalBindType
from core.signals import bind_service_signal


logger = logging.getLogger(__name__)




def bind_service_signals():
    def on_get_palid_access_token_complete(**kwargs):
        try:
            plaid_service = kwargs.get('cls_')
            plaid_item = kwargs.get('result', {}).get('plaid_item')
            item_id = kwargs.get('result', {}).get('item_id')
            module_key = kwargs.get('result', {}).get('module_key')
            user = None
            # Try to extract user from plaid_item, fallback to signal data
            if plaid_item and hasattr(plaid_item, 'user'):
                user = plaid_item.user
            elif 'data' in kwargs and kwargs['data'] and len(kwargs['data'][0]) > 1:
                user = kwargs['data'][0][1]

            if not (plaid_service and plaid_item and item_id and user):
                logger.error("Missing required data in signal kwargs.")
                return

            # Fetch all data from Plaid
            identity = plaid_service.get_account_identity(item_id)
            auth = plaid_service.get_account_numbers(item_id)
            accounts = plaid_service.get_accounts(item_id)

            # Build lookup tables
            accounts_by_id = {a['account_id']: a for a in accounts['accounts']}
            ach_numbers = {a['account_id']: a for a in auth.get('numbers', {}).get('ach', [])}
            identity_by_id = {a['account_id']: a for a in identity['accounts']}

            for account_id, account in accounts_by_id.items():
                ach = ach_numbers.get(account_id, {})
                account_number = ach.get('account')
                routing_number = ach.get('routing')

                identity_acct = identity_by_id.get(account_id, {})
                owner = identity_acct.get('owners', [{}])[0]

                address = None
                if owner.get('addresses'):
                    address = next((a['data'] for a in owner['addresses'] if a.get('primary')), owner['addresses'][0]['data'])

                street = address.get('street') if address else None
                city = address.get('city') if address else None
                state = address.get('region') if address else None
                postal_code = address.get('postal_code') if address else None
                country = address.get('country') if address else None

                from ledger.models import BankAccountModel  # import here to avoid circular import
                subtype = account.get('subtype', 'checking').lower()
                PLAID_TO_INTERNAL_ACCOUNT_TYPE = {
                    'checking': BankAccountModel.ACCOUNT_CHECKING,
                    'savings': BankAccountModel.ACCOUNT_SAVINGS,
                    'credit': BankAccountModel.ACCOUNT_CREDIT_CARD,
                    'credit_card': BankAccountModel.ACCOUNT_CREDIT_CARD,
                    'mortgage': BankAccountModel.ACCOUNT_MORTGAGE,
                    # add more as needed
                }
                account_type = PLAID_TO_INTERNAL_ACCOUNT_TYPE.get(subtype, 'other')

                bank_account, _ = BankAccountModel.update_or_create_bank_account(
                    user=user,
                    account_number=account_number,
                    routing_number=routing_number,
                    defaults={
                        'connection_type': BankAccountModel.ConnectionType.PLAID,
                        'country': "USA" if country == 'US' else country,
                        'plaid_item': plaid_item,
                        'name': account.get('name') or account.get('official_name') or 'Unknown',
                        'account_type': account_type,
                        'address_1': street,
                        'city': city,
                        'state': state,
                        'zip_code': postal_code,
                        'is_verified': True,
                        'metadata': {'account_id': account_id} # Use this to match transactions to bank account
                    }
                )

                # Add module_key to module_keys if not already present
                if module_key not in bank_account.module_keys:
                    bank_account.module_keys.append(module_key.lower())
                    bank_account.save(update_fields=['module_keys'])
        except Exception as e:
            logger.exception(f"Error linking bank connections from Plaid item {item_id}: {e}")


    # Embed AccountModel into Pinecone after account creation
    def on_ledger_create_account_after(**kwargs):
        try:
            account_model = kwargs.get('result')
            if not account_model:
                logger.error("ledger.create_account_embeddings_signal AFTER missing result (AccountModel).")
                return
            from ledger.bookkeeping_agents.embeddings import embeddings_manager
            embeddings_manager.insert_account_embedding(account_model)
        except Exception as e:
            logger.exception(f"Error inserting embedding for new account: {e}")


    # Bind signals to the appropriate service signals
    bind_service_signal(
        'create_bank_connections_from_plaid_item',
        on_get_palid_access_token_complete,
        bind_type=ServiceSignalBindType.AFTER
    )

    bind_service_signal(
        'ledger.create_account_embeddings_signal',
        on_ledger_create_account_after,
        bind_type=ServiceSignalBindType.AFTER
    )
