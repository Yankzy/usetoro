from typing import List, Dict, Any, Optional, Literal
from uuid import UUID
from typing import Optional, List, Any, Tuple, Dict
from asgiref.sync import sync_to_async
from ledger.models import EntityModel, AccountModel, TokenCountModel
from agents import Agent, AgentHooks, RunContextWrapper, Tool
from pydantic import BaseModel, Field, field_validator, model_validator
import logging
from django.contrib.auth import get_user_model
from agents.extensions.handoff_filters import remove_all_tools
from pydantic_core.core_schema import FieldValidationInfo
from django.db import transaction



logger = logging.getLogger(__name__)


class CoASearchType(BaseModel):
    """Represents a single financial transaction to be categorized."""
    account_types: Optional[List[str]] = None,
    search_keywords: Optional[List[str]] = None



class PersonalFinanceCategory(BaseModel):
    """Captures Plaid's detailed personal finance category."""
    primary: str
    detailed: str

class PlaidTransaction(BaseModel):
    """
    A Pydantic model designed to capture the most relevant fields from a 
    Plaid transaction for an LLM.
    """
    # Core Transaction Details
    amount: float
    currency: str = Field(..., alias='iso_currency_code')
    date: str
    
    # Description and Vendor Information
    description: str = Field(..., alias='name') # The raw transaction name
    merchant_name: Optional[str] = None # Plaid's clean merchant name
    vendor_name: Optional[str] = None # A consolidated, reliable vendor name
    counterparties: List[Dict] = [] # Raw counterparties for context
    payment_channel: str
    
    # Categorization Details
    plaid_category: Optional[List[str]] = Field(None, alias='category')
    personal_finance_category: Optional[PersonalFinanceCategory] = None
    
    # Custom Logic Field
    is_outflow: bool = True # Default to True for expenses

    @model_validator(mode='before')
    @classmethod
    def prepare_data(cls, data: Dict) -> Dict:
        """
        This validator runs before any other validation to clean up and consolidate data.
        It intelligently sets a single `vendor_name` and determines the transaction direction.
        """
        # 1. Consolidate the vendor name. Prefer the clean merchant_name, but
        #    fall back to the first counterparty's name if needed.
        merchant = data.get('merchant_name')
        counterparties = data.get('counterparties', [])
        if merchant:
            data['vendor_name'] = merchant
        elif counterparties and isinstance(counterparties, list) and counterparties[0].get('name'):
            data['vendor_name'] = counterparties[0]['name']

        # 2. Determine if the transaction is an outflow. In Plaid, positive amounts
        #    are typically debits (outflows) from a checking account.
        amount = data.get('amount', 0)
        data['is_outflow'] = amount > 0
        
        return data


class TransactionType(BaseModel):
    """Represents a single financial transaction to be categorized."""
    transaction_id: str
    amount: float
    description: str
    vendor: Optional[str] = None
    category: Optional[List[str]] = None
    is_outflow: Optional[bool] = None

    @field_validator("is_outflow", mode="before")
    @classmethod
    def set_is_outflow(cls, v, info: FieldValidationInfo): 
        # If explicitly provided, use it; otherwise, infer from amount
        if v is not None:
            return v
        
        # In V2, other fields' data is in the 'info.data' dictionary
        amount = info.data.get("amount")
        if amount is None:
            raise ValueError("amount is required to determine is_outflow")
        return amount > 0


class RoleCategoryType(BaseModel):
    category: str

class RoleType(BaseModel):
    role: str


class AccountType(BaseModel):
    """Represents an account in the Chart of Accounts, reflecting the Django AccountModel."""
    uuid: UUID
    index: int
    code: str
    name: str
    balance_type: Literal["debit", "credit"]
    role: str 

class RuleConditionType(BaseModel):
    """A single condition within a RuleGroup."""
    field: Literal["description", "vendor", "amount", "mcc", "category"]
    operator: Literal["contains", "equals", "gt", "lt", "in", "regex"]
    value: str

class RuleGroupSuggestionType(BaseModel):
    """A complete, proposed rule to be saved to the database."""
    name: str
    logic: Literal["AND", "OR"]
    priority: Literal[100, 200]
    target_account_index: int
    confidence: float = Field(..., ge=0.0, le=1.0)
    conditions: List[RuleConditionType]

class AccountProposalType(BaseModel):
    """A structured suggestion for a new account when no match is found."""
    name: str
    balance_type: Literal["DEBIT", "CREDIT"]
    role: str
    # This is to make the LLM reason a bit on the Suggestion which increases quality
    justification: str 
    confidence: float = Field(..., ge=0.0, le=1.0)

    @field_validator('balance_type', mode='before')
    @classmethod
    def uppercase_balance_type(cls, v: Any) -> Any:
        """Converts the balance_type input to uppercase before validation."""
        if isinstance(v, str):
            return v.upper()
        return v
    
class MatchResultType(BaseModel):
    """The output from the matching agent."""
    is_match: bool
    target_account_index: Optional[int] = None
    confidence: float = 0.0
    reason: str

class ValidationResultType(BaseModel):
    """The output of the proposal validation tool."""
    is_valid: bool
    reason: str


class CreateProposedAccountResultType(BaseModel):
    """The output of the proposal validation tool."""
    is_created: bool
    fail_reason: str


class ToolFailedNotification(BaseModel):
    """
    Pydantic model for sending a notification when a tool call fails.
    """
    tool_name: str = Field(None, description="The name of the tool that failed.")
    error_message: str = Field(None, description="The error message from the tool failure.")


class LocalContext(BaseModel):
    user_uuid: Optional[str] = None 
    entity_uuid: Optional[str] = None 
    entity_name: Optional[str] = None 
    connected_banks: Optional[List[Dict]] = None
    accounts_snapshot: Optional[List[Dict]] = Field(None, exclude=True)
    rules_snapshot: Optional[List[Dict]] = Field(None, exclude=True)
    transaction: Optional[PlaidTransaction] = None



    @staticmethod
    def _fetch_data_sync(entity_uuid: str) -> Optional[List[Dict[str, Any]]]:
        """
        A synchronous helper function to fetch and prepare all required data
        from the database. This keeps all ORM operations in one place.
        """
        try:
            # Using transaction.atomic can also help ensure data consistency.
            with transaction.atomic():
                entity = EntityModel.objects.get(uuid=entity_uuid)
                accounts = list(entity.get_all_accounts(active=True))
                
                # The entire list comprehension also happens here.
                accounts_data = [
                    {
                        "index": i + 1,
                        "uuid": str(a.uuid),
                        "name": a.name,
                        "role": a.role,
                        "balance_type": a.balance_type,
                    }
                    for i, a in enumerate(accounts)
                ]
                return accounts_data
        except EntityModel.DoesNotExist:
            # Let the caller handle the exception if it needs to.
            raise

    @classmethod
    async def build(cls, entity_uuid: str = None, **kwargs) -> "LocalContext":
        """
        Asynchronously fetches the account snapshot and then builds the context object.
        """
        accounts_data = None
        if entity_uuid:
            try:
                # 1. Await the entire synchronous data-fetching operation.
                #    sync_to_async will run _fetch_data_sync in a separate thread.
                accounts_data = await sync_to_async(cls._fetch_data_sync)(entity_uuid)
            except EntityModel.DoesNotExist:
                raise ValueError(f"Entity with UUID {entity_uuid} does not exist")

        # 2. Once all data is gathered, call the regular (synchronous)
        #    constructor to create the model instance.
        return cls(
            entity_uuid=entity_uuid, 
            accounts_snapshot=accounts_data,
            **kwargs
        )


    async def get_user(self):
        """Fetch the User instance for this context's user_uuid."""

        if not self.user_uuid:
            raise ValueError("user_uuid is not set")

        User = get_user_model()
        instance_queryset = await sync_to_async(User.objects.filter)(user_uuid=self.user_uuid)
        instance = instance_queryset.first()
        return instance


    async def get_entity(self):
        """Fetch the EntityModel instance for this context's entity_uuid."""
        if not self.entity_uuid:
            raise ValueError("entity_uuid is not set")
        
        # Wrap the entire operation, including .first(), in one call
        instance = await sync_to_async(
            EntityModel.objects.filter(uuid=self.entity_uuid).first
        )()
        return instance


    async def create_account(
        self,
        name: str,
        role: str,
        balance_type: str,
        code: str = None,  
        active: bool = True  
    ) -> AccountModel:
        """
        Create an AccountModel using ChartOfAccountModel.create_account and
        then update the local accounts_snapshot.
        """
        entity = await self.get_entity()
        if entity is None:
            raise ValueError("entity_uuid is not set or entity not found")

        # This synchronous helper function performs all database operations.
        def _create_account_sync():
            # 1. Get the Chart of Accounts model instance.
            coa_model = entity.get_coa_model_qs().get(name='general_coa')

            # 2. Call the create_account *instance method* on the coa_model.
            return coa_model.create_account(
                name=name,
                role=role,
                balance_type=balance_type.lower(),
                code=code,
                active=active
            )

        # 1. Await the creation of the account in the database.
        new_account = await sync_to_async(_create_account_sync)()

        # 2. Update the in-memory accounts_snapshot.
        if self.accounts_snapshot is None:
            self.accounts_snapshot = []

        next_index = 1
        if self.accounts_snapshot:
            last_index = self.accounts_snapshot[-1]["index"]
            next_index = last_index + 1

        new_snapshot_entry = {
            "index": next_index,
            "uuid": str(new_account.uuid),
            "name": new_account.name,
            "role": new_account.role,
            "balance_type": new_account.balance_type,
        }
        self.accounts_snapshot.append(new_snapshot_entry)

        # 3. Return the newly created Django model instance.
        return new_account


class RuntimeEvents(AgentHooks):
    """
    A class that receives callbacks on various lifecycle events for a specific agent. 
    """

    async def on_start(self, context: RunContextWrapper[LocalContext], agent: Agent[LocalContext]) -> None:
        """Called before the agent is invoked. Called each time the running agent is changed to this
        agent."""
        # log the info
        logger.info(f"\n===AGENT START with context: {context}===")

    async def on_end(self, context: RunContextWrapper[LocalContext], agent: Agent[LocalContext], output: Any) -> None:
        """Called when the agent produces a final output."""
        # log output
        logger.info(f"\n===ON END OUTPUT===: {output}\n")
        logger.info(f"\n===ON END CONTEXT===: {context}\n")
        u = context.usage
        logger.info(f"===== ON AGENT END =====: {agent.name} → {u.requests} requests, {u.total_tokens} total tokens")
        await sync_to_async(TokenCountModel.objects.create)(
            entity_name=context.context.entity_name,
            agent_name=agent.name,
            num_requests=u.requests,
            total_tokens=u.total_tokens,
            metadata={}
        )

    async def on_handoff(
        self,
        context: RunContextWrapper[LocalContext],
        agent: Agent[LocalContext],
        source: Agent[LocalContext],
    ) -> None:
        """Called when the agent is being handed off to. The `source` is the agent that is handing
        off to this agent."""
        pass

    async def on_tool_start(
        self,
        context: RunContextWrapper[LocalContext],
        agent: Agent[LocalContext],
        tool: Tool,
    ) -> None:
        """Called before a tool is invoked."""
        logger.info(f"\n===ON TOOL START CONTEXT===: {context}\n")

    async def on_tool_end(
        self,
        context: RunContextWrapper[LocalContext],
        agent: Agent[LocalContext],
        tool: Tool,
        result: str,
    ) -> None:
        """Called after a tool is invoked."""
        logger.info(f"\n===ON TOOL END CONTEXT===: {context}\n")

