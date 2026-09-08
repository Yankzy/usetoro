import graphene
from graphene_django import DjangoObjectType
from ledger.models import PlaidItem
import json # Import json for potential parsing if metadata is stored as a string
import logging


logger = logging.getLogger(__name__)

# Define a type for the nested account objects
import graphene

class LivePlaidBalanceType(graphene.ObjectType):
    limit = graphene.Float()
    current = graphene.Float()
    available = graphene.Float()
    isoCurrencyCode = graphene.String()
    unofficialCurrencyCode = graphene.String()

class LivePlaidAccountType(graphene.ObjectType):
    id = graphene.String()
    mask = graphene.String()
    name = graphene.String()
    type = graphene.String()
    subtype = graphene.String()
    classType = graphene.String()
    verificationStatus = graphene.String()
    balances = graphene.Field(LivePlaidBalanceType)

class LivePlaidMetadataType(graphene.ObjectType):
    accounts = graphene.List(LivePlaidAccountType)
    institution = graphene.String()

class LivePlaidItemType(graphene.ObjectType):
    itemId = graphene.String()
    metadata = graphene.Field(LivePlaidMetadataType)

class PlaidBalanceType(graphene.ObjectType):
    limit = graphene.Float()
    current = graphene.Float()
    available = graphene.Float()
    isoCurrencyCode = graphene.String()
    unofficialCurrencyCode = graphene.String()

class PlaidAccountType(graphene.ObjectType):
    id = graphene.String()
    mask = graphene.String()
    name = graphene.String()
    type = graphene.String()
    subtype = graphene.String()
    classType = graphene.String()
    verificationStatus = graphene.String()
    balances = graphene.Field(PlaidBalanceType) 


# Define a type for the overall metadata structure
class PlaidMetadataType(graphene.ObjectType):
    """
    Represents the structured metadata associated with a Plaid item.
    """
    accounts = graphene.List(PlaidAccountType)
    institution = graphene.String() # Assuming institution is stored directly as a string name



class PlaidItemType(DjangoObjectType):
    metadata = graphene.Field(PlaidMetadataType, description="Structured metadata associated with the Plaid item, including account and institution information.")

    class Meta:
        model = PlaidItem
        fields = ("uuid", "item_id", "metadata")

    
    def resolve_metadata(self, info):
        meta = self.metadata
        logger.info(f"\n===Resolving metadata for PlaidItem===: {meta}")
        if not meta:
            return None
        if isinstance(meta, str):
            try:
                meta = json.loads(meta)
            except json.JSONDecodeError:
                logger.error("\n===Failed to decode JSON metadata===")
                return None

        institution = None
        item = meta.get("item")
        if item and isinstance(item, dict):
            institution = item.get("institution_name")

        accounts = meta.get("accounts", [])
        def map_account(acc):
            balances = acc.get("balances", {})
            return {
                "id": acc.get("account_id"),
                "mask": acc.get("mask"),
                "name": acc.get("name"),
                "type": acc.get("type"),
                "subtype": acc.get("subtype"),
                "classType": None,
                "verificationStatus": None,
                "balances": PlaidBalanceType(
                    limit=balances.get("limit"),
                    current=balances.get("current"),
                    available=balances.get("available"),
                    isoCurrencyCode=balances.get("iso_currency_code"),
                    unofficialCurrencyCode=balances.get("unofficial_currency_code"),
                ) if balances else None,
            }
        accounts_typed = [PlaidAccountType(**map_account(acc)) for acc in accounts]

        return PlaidMetadataType(
            institution=institution,
            accounts=accounts_typed
        )