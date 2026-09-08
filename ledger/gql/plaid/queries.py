import graphene
from .types import LivePlaidItemType
from graphql_jwt.decorators import login_required 
import logging
from ledger.plaid_service import PlaidService
from ledger.models import PlaidItem

logger = logging.getLogger(__name__)

class PlaidQuery(graphene.ObjectType):
    plaid_items = graphene.List(
        LivePlaidItemType,
        module_key=graphene.String(required=False)
    )

    @login_required
    def resolve_plaid_items(self, info, module_key=None):
        user = info.context.user
        plaid_items = PlaidItem.objects.filter(user=user)

        if module_key and module_key.lower() in ['ledger', 'budgeting', 'trading']:
            plaid_items = plaid_items.filter(module_keys__contains=[module_key])  

        plaid_service = PlaidService.instance()
        items = []
        for instance in plaid_items:
            item_id = instance.item_id
            try:
                plaid_data = plaid_service.get_accounts(item_id)
                # Extract institution name
                institution = None
                if "item" in plaid_data and isinstance(plaid_data["item"], dict):
                    institution = plaid_data["item"].get("institution_name")
                # Map accounts
                accounts = []
                for acc in plaid_data.get("accounts", []):
                    balances = acc.get("balances", {})
                    accounts.append({
                        "id": acc.get("account_id"),
                        "mask": acc.get("mask"),
                        "name": acc.get("name"),
                        "type": acc.get("type"),
                        "subtype": acc.get("subtype"),
                        "classType": None,
                        "verificationStatus": None,
                        "balances": {
                            "limit": balances.get("limit"),
                            "current": balances.get("current"),
                            "available": balances.get("available"),
                            "isoCurrencyCode": balances.get("iso_currency_code"),
                            "unofficialCurrencyCode": balances.get("unofficial_currency_code"),
                        } if balances else None,
                    })
                items.append({
                    "itemId": item_id,
                    "metadata": {
                        "accounts": accounts,
                        "institution": institution,
                    }
                })
            except Exception as e:
                logger.warning(f"Failed to fetch live Plaid data for item_id {item_id}: {e}")
        return items